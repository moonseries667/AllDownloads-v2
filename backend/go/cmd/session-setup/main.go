// session-setup is a local interactive tool, never a runner HTTP endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var err error
	if strings.HasPrefix(filepath.Base(os.Args[0]), "tdl-"+telegram.IdentityExtension) && os.Getenv("TDL_EXTENSION") != "" {
		err = runIdentityExtension(ctx, os.Stdout)
	} else {
		err = runCLI(ctx, os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("session-setup", flag.ContinueOnError)
	configPath := flags.String("config", "", "absolute path to the new private runner JSON")
	platform := flags.String("platform", "", "telegram or quark")
	check := flags.Bool("check", false, "check only the bound identity; never log in or change configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*platform != "telegram" && *platform != "quark") || !filepath.IsAbs(*configPath) {
		return errors.New("use --config <absolute runner.json> --platform telegram|quark [--check]")
	}
	if !*check && !interactiveTerminal() {
		return errors.New("setup requires a local interactive console; do not pipe or redirect credentials")
	}
	settings, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *check {
		return checkIdentity(ctx, settings, *platform)
	}
	// The lock covers the interactive authorization and final update, so two
	// setup processes cannot replace the same account while a user scans a QR.
	unlock, err := lockConfig(*configPath)
	if err != nil {
		return err
	}
	defer unlock()
	settings, err = loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *platform == "telegram" {
		return setupTelegram(ctx, settings)
	}
	return setupQuark(ctx, settings, readSecret)
}

func setupQuark(ctx context.Context, cfg *localConfig, secret func() ([]byte, error)) error {
	a, err := cfg.quarkAccount()
	if err != nil {
		return err
	}
	if a.ExpectedUserID == "" {
		if _, err := os.Lstat(a.CookieFile); !errors.Is(err, os.ErrNotExist) {
			return errors.New("an unbound cookie file already exists; choose a fresh v2 private directory")
		}
	}
	fmt.Fprintln(os.Stdout, "请在 Y7000P 的新浏览器中亲自登录 https://pan.quark.cn，再从该新会话复制 Cookie。")
	fmt.Fprint(os.Stdout, "粘贴新 Cookie（隐藏输入，按 Enter 完成）：")
	cookie, err := secret()
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return errors.New("cannot read a hidden Cookie from this console")
	}
	defer clear(cookie)
	cookie = []byte(strings.TrimSpace(string(cookie)))
	defer clear(cookie)
	if len(cookie) == 0 || len(cookie) > 16384 || strings.ContainsAny(string(cookie), "\r\n\x00") {
		return errors.New("invalid Cookie input")
	}
	client := a.client(func() (string, error) { return string(cookie), nil })
	uid, err := client.IdentifyForSetup(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return errors.New("setup was cancelled before saving the new session")
	}
	// Verify configuration has not changed before writing any credential.
	if err := cfg.unchanged(); err != nil {
		return err
	}
	a.ExpectedUserID = uid
	if err := cfg.saveQuark(a); err != nil {
		return err
	}
	// Persist the verified binding first. A crash before the atomic cookie write
	// leaves an AUTH_REQUIRED account which can only be resumed with that UID.
	if err := writeAtomic(a.CookieFile, cookie, 0600); err != nil {
		return errors.New("the verified UID was bound, but the Cookie could not be saved; rerun setup for the same account")
	}
	fmt.Fprintln(os.Stdout, "夸克身份已核对并绑定。请在账号页面点击检查；若 runner 已运行，请先重启 runner 读取新配置。")
	return nil
}

func setupTelegram(ctx context.Context, cfg *localConfig) error {
	tdl, a, err := cfg.telegramAccount()
	if err != nil {
		return err
	}
	if err := installHelper(tdl); err != nil {
		return err
	}
	if a.PeerID != "" {
		if _, err := tdl.CheckSession(ctx, "telegram-main"); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Telegram 已绑定且身份核对成功；此入口不会替换既有账号。")
		return nil
	}
	// An interrupted first setup can already have a valid private session. A
	// read-only self request recovers its binding without replacing that session.
	self, err := tdl.CurrentIdentity(ctx, "telegram-main")
	if err != nil {
		if _, statErr := os.Lstat(a.StorageDir); statErr == nil {
			entries, readErr := os.ReadDir(a.StorageDir)
			if readErr != nil || len(entries) > 0 {
				return errors.New("existing unbound Telegram storage could not be checked; resolve tool/network errors and retry setup, or configure a fresh v2 private directory")
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return errors.New("cannot inspect the new Telegram directory")
		}
		fmt.Fprintln(os.Stdout, "请使用 Telegram 官方客户端扫描本终端二维码；如有两步验证，请仅在此终端输入。")
		if err := tdl.LoginQR(ctx, "telegram-main", os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
		self, err = tdl.CurrentIdentity(ctx, "telegram-main")
		if err != nil {
			return err
		}
	}
	a.PeerID = self
	if err := cfg.saveTelegram(tdl.Executable, a); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Telegram 当前身份已核对并绑定为 Saved Messages。请在账号页面点击检查；若 runner 已运行，请先重启 runner 读取新配置。")
	return nil
}

func checkIdentity(ctx context.Context, cfg *localConfig, platform string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if platform == "telegram" {
		tdl, _, err := cfg.telegramAccount()
		if err != nil {
			return err
		}
		if _, err := tdl.CheckSession(ctx, "telegram-main"); err != nil {
			return err
		}
	} else {
		a, err := cfg.quarkAccount()
		if err != nil {
			return err
		}
		if _, err := a.client(func() (string, error) {
			raw, err := os.ReadFile(a.CookieFile)
			defer clear(raw)
			return strings.TrimSpace(string(raw)), err
		}).CheckSession(ctx); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, "已通过只读身份检查。")
	return nil
}

type quarkSettings struct {
	CookieFile     string `json:"cookieFile"`
	ExpectedUserID string `json:"expectedUserId"`
	BaseURL        string `json:"baseUrl,omitempty"`
	AccountURL     string `json:"accountUrl,omitempty"`
}

func (a quarkSettings) client(cookie func() (string, error)) *quark.NativeClient {
	return &quark.NativeClient{AccountID: "quark-main", ExpectedUserID: a.ExpectedUserID, Cookie: cookie, BaseURL: a.BaseURL, AccountURL: a.AccountURL}
}
