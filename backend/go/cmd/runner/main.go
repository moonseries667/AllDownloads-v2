package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/folder"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/netpolicy"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/bilibili"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/binding"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

type config struct {
	RunnerID         string            `json:"runnerId"`
	ControlURL       string            `json:"controlUrl"`
	TokenEnv         string            `json:"tokenEnv"`
	TokenFile        string            `json:"tokenFile"`
	StateDir         string            `json:"stateDir"`
	AllowedRoots     []string          `json:"allowedRoots"`
	Roots            map[string]string `json:"roots"`
	FFmpeg           string            `json:"ffmpeg"`
	FFprobe          string            `json:"ffprobe"`
	FixtureCatalog   string            `json:"fixtureCatalog"`
	IsolatedTestMode bool              `json:"isolatedTestMode"`
	Bilibili         struct {
		CookieFile     string `json:"cookieFile"`
		ExpectedUserID string `json:"expectedUserId"`
		APIBase        string `json:"apiBase"`
		PassportBase   string `json:"passportBase"`
	} `json:"bilibili"`
	Telegram struct {
		Executable string                         `json:"executable"`
		ProxyURL   string                         `json:"proxyURL"`
		Accounts   map[string]telegram.TDLAccount `json:"accounts"`
	} `json:"telegram"`
	QuarkAccounts map[string]struct {
		CookieFile     string `json:"cookieFile"`
		ExpectedUserID string `json:"expectedUserId"`
		BaseURL        string `json:"baseUrl"`
		AccountURL     string `json:"accountUrl"`
	} `json:"quarkAccounts"`
	DriveAccounts map[string]struct {
		TokenEnv   string `json:"tokenEnv"`
		TokenFile  string `json:"tokenFile"`
		ClientFile string `json:"clientFile"`
		RootID     string `json:"rootId"`
		ProxyURL   string `json:"proxyURL"`
		Rclone     *struct {
			Executable  string `json:"executable"`
			ConfigFile  string `json:"configFile"`
			Remote      string `json:"remote"`
			BindingFile string `json:"bindingFile"`
		} `json:"rclone"`
	} `json:"driveAccounts"`
	DriveMount drive.MountSettings `json:"driveMount"`
}

func main() {
	nativeDialog := flag.Bool("native-folder-dialog", false, "internal bounded native folder dialog helper")
	path := flag.String("config", "", "runner configuration JSON path")
	once := flag.Bool("once", false, "register, reconcile pending results and acquire at most one operation")
	flag.Parse()
	if *nativeDialog {
		_ = json.NewEncoder(os.Stdout).Encode(folder.ShowNative())
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, *path, *once); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, path string, once bool) (runErr error) {
	if path == "" {
		return errors.New("-config is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("cannot read configuration")
	}
	var cfg config
	if json.Unmarshal(raw, &cfg) != nil {
		return errors.New("invalid configuration JSON")
	}
	if cfg.RunnerID == "" || cfg.StateDir == "" || len(cfg.AllowedRoots) == 0 {
		return errors.New("runnerId, stateDir and allowedRoots are required")
	}
	if cfg.TokenFile != "" && cfg.TokenEnv != "" {
		return errors.New("choose tokenFile or tokenEnv")
	}
	if cfg.TokenEnv == "" && cfg.TokenFile == "" {
		cfg.TokenEnv = "ALLDOWNLOADS_INTERNAL_TOKEN"
	}
	internalToken := os.Getenv(cfg.TokenEnv)
	if cfg.TokenFile != "" {
		if !filepath.IsAbs(cfg.TokenFile) {
			return errors.New("tokenFile must be an absolute private runtime path")
		}
		value, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return errors.New("cannot read internal authentication file")
		}
		internalToken = strings.TrimSpace(string(value))
	}
	client, err := runner.NewClient(cfg.ControlURL, internalToken)
	if err != nil {
		return err
	}
	state, err := executor.OpenState(cfg.StateDir)
	if err != nil {
		return err
	}
	defer state.Close()
	if cfg.Bilibili.CookieFile == "" {
		cfg.Bilibili.CookieFile, err = filepath.Abs(filepath.Join(cfg.StateDir, "auth", "bilibili.session"))
		if err != nil {
			return errors.New("cannot resolve private Bilibili session path")
		}
	}
	bilSession, err := bilibili.New(bilibili.Config{CookieFile: cfg.Bilibili.CookieFile, ExpectedUserID: cfg.Bilibili.ExpectedUserID,
		IsolatedTestMode: cfg.IsolatedTestMode, APIBase: cfg.Bilibili.APIBase, PassportBase: cfg.Bilibili.PassportBase})
	if err != nil {
		return err
	}
	var provider media.Provider = &media.Bilibili{Session: bilSession}
	downloadClient := bilSession.DownloadClient()
	if cfg.FixtureCatalog != "" {
		if !cfg.IsolatedTestMode {
			return errors.New("fixtureCatalog requires isolatedTestMode")
		}
		raw, err := os.ReadFile(cfg.FixtureCatalog)
		if err != nil {
			return errors.New("cannot read fixture catalog")
		}
		catalog := runner.FixtureProvider{}
		if json.Unmarshal(raw, &catalog) != nil {
			return errors.New("invalid fixture catalog")
		}
		for _, meta := range catalog {
			for _, unit := range meta.Units {
				for _, track := range unit.Tracks {
					if !track.Selectable() {
						continue
					}
					u, err := url.Parse(track.Source.URL)
					if err != nil || u.User != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
						return errors.New("fixture media URLs must be loopback HTTP")
					}
				}
			}
		}
		provider = catalog
		downloadClient = &http.Client{Timeout: 30 * time.Minute}
	}
	var mediaTools *media.Tools
	versions := map[string]string{}
	if cfg.FFmpeg != "" || cfg.FFprobe != "" {
		mediaTools = &media.Tools{FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe}
		if err := mediaTools.Check(ctx); err != nil {
			return err
		}
		versions["ffmpeg"] = media.FFmpegVersion
		versions["ffprobe"] = media.FFmpegVersion
	}
	handler := &runner.Handler{State: state, StateDir: cfg.StateDir, AllowedRoots: cfg.AllowedRoots, Roots: cfg.Roots, Provider: provider, Tools: mediaTools, Downloads: media.Downloader{Client: downloadClient}}
	if _, err := netpolicy.Environment(cfg.Telegram.ProxyURL); err != nil {
		return errors.New("invalid Telegram local proxy configuration")
	}
	tel := &telegram.TDL{Executable: cfg.Telegram.Executable, ProxyURL: cfg.Telegram.ProxyURL, Accounts: cfg.Telegram.Accounts, State: state, WorkDir: filepath.Join(cfg.StateDir, "telegram-work")}
	if len(cfg.Telegram.Accounts) > 0 {
		if err := tel.Check(ctx); err != nil {
			return err
		}
		versions["tdl"] = telegram.TDLVersion
	}
	transfers := &quark.Transfers{State: state, Clients: map[string]*quark.NativeClient{}}
	for account, settings := range cfg.QuarkAccounts {
		if account == "" || settings.ExpectedUserID == "" || !filepath.IsAbs(settings.CookieFile) {
			return errors.New("quark account requires expectedUserId and private cookieFile")
		}
		if !cfg.IsolatedTestMode && (settings.BaseURL != "" || settings.AccountURL != "") {
			return errors.New("platform endpoint overrides require isolatedTestMode")
		}
		transfers.Clients[account] = &quark.NativeClient{AccountID: account, ExpectedUserID: settings.ExpectedUserID, BaseURL: settings.BaseURL, AccountURL: settings.AccountURL, Cookie: func() (string, error) {
			raw, err := os.ReadFile(settings.CookieFile)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(raw)), nil
		}}
	}
	platforms := &binding.Worker{Base: handler, Saved: tel, Telegram: tel, Tree: transfers, Quark: transfers, Transfers: transfers}
	drives := map[string]*drive.Client{}
	for account, settings := range cfg.DriveAccounts {
		if account == "" || (settings.TokenEnv != "" && settings.TokenFile != "") || (settings.Rclone != nil && (settings.TokenEnv != "" || settings.TokenFile != "" || settings.ClientFile != "")) {
			return errors.New("drive account requires exactly one private authentication source")
		}
		if settings.Rclone != nil {
			drives[account], err = drive.NewRclone(drive.RcloneConfig{AccountID: account, RootID: settings.RootID, Executable: settings.Rclone.Executable,
				ConfigFile: settings.Rclone.ConfigFile, Remote: settings.Rclone.Remote, BindingFile: settings.Rclone.BindingFile, ProxyURL: settings.ProxyURL,
				Run: func(ctx context.Context, executable string, timeout time.Duration, environment, args []string) ([]byte, []byte, error) {
					return media.RunControlledExactEnvironment(ctx, executable, timeout, environment, args...)
				}})
			if err != nil {
				return err
			}
		} else if settings.TokenEnv != "" {
			drives[account] = &drive.Client{AccountID: account, RootID: settings.RootID, Token: func() string { return os.Getenv(settings.TokenEnv) }}
		} else {
			if settings.TokenFile == "" {
				return errors.New("drive account requires a private tokenFile")
			}
			drives[account], err = drive.NewGoogle(drive.GoogleConfig{AccountID: account, RootID: settings.RootID, TokenFile: settings.TokenFile, ClientFile: settings.ClientFile})
			if err != nil {
				return err
			}
		}
		drives[account].HTTP, err = netpolicy.HTTPClient(settings.ProxyURL, 30*time.Minute)
		if err != nil {
			return errors.New("invalid Drive local proxy configuration")
		}
	}
	storage := &runner.StorageWorker{Next: platforms, Roots: handler, State: state, HostID: cfg.RunnerID, Picker: &folder.Picker{}, PickerReady: folder.InteractiveDesktop, Drives: drives}
	if cfg.DriveMount.Enabled {
		if cfg.DriveMount.AccountID != "drive-main" {
			return errors.New("Drive mount requires the fixed drive-main account")
		}
		mount, err := drive.NewMounter(drives[cfg.DriveMount.AccountID], drive.MountConfig{CacheDir: cfg.DriveMount.CacheDir},
			func(ctx context.Context, executable string, args, environment []string) (drive.MountProcess, error) {
				return media.StartManaged(ctx, executable, args, environment)
			})
		if err != nil {
			return err
		}
		storage.LocalFacts = func() map[string]any { return mountFacts(mount.Status()) }
		if !once {
			mountCtx, stopMount := context.WithCancel(ctx)
			mountDone := make(chan error, 1)
			go func() { mountDone <- maintainMount(mountCtx, mount, 15*time.Second) }()
			defer func() {
				stopMount()
				if err := <-mountDone; err != nil {
					runErr = err
				}
			}()
		}
	}
	accounts := &runner.AccountWorker{Next: storage, Accounts: accountAdapters(bilSession, drives, transfers, tel)}
	service := runner.Runner{ID: cfg.RunnerID, Client: client, State: state, Worker: accounts, ToolVersions: versions, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	return service.Run(ctx, once)
}
