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
	Telegram         struct {
		Executable string                         `json:"executable"`
		Accounts   map[string]telegram.TDLAccount `json:"accounts"`
	} `json:"telegram"`
	QuarkAccounts map[string]struct {
		CookieFile     string `json:"cookieFile"`
		ExpectedUserID string `json:"expectedUserId"`
		BaseURL        string `json:"baseUrl"`
		AccountURL     string `json:"accountUrl"`
	} `json:"quarkAccounts"`
	DriveAccounts map[string]struct {
		TokenEnv string `json:"tokenEnv"`
		RootID   string `json:"rootId"`
	} `json:"driveAccounts"`
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
func run(ctx context.Context, path string, once bool) error {
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
	var provider media.Provider = &media.Bilibili{Client: &http.Client{Timeout: 30 * time.Second}}
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
	handler := &runner.Handler{State: state, StateDir: cfg.StateDir, AllowedRoots: cfg.AllowedRoots, Roots: cfg.Roots, Provider: provider, Tools: mediaTools, Downloads: media.Downloader{Client: &http.Client{Timeout: 30 * time.Minute}}}
	tel := &telegram.TDL{Executable: cfg.Telegram.Executable, Accounts: cfg.Telegram.Accounts, State: state, WorkDir: filepath.Join(cfg.StateDir, "telegram-work")}
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
		if account == "" || settings.TokenEnv == "" {
			return errors.New("drive account and tokenEnv are required")
		}
		drives[account] = &drive.Client{AccountID: account, RootID: settings.RootID, Token: func() string { return os.Getenv(settings.TokenEnv) }}
	}
	storage := &runner.StorageWorker{Next: platforms, Roots: handler, State: state, HostID: cfg.RunnerID, Picker: &folder.Picker{}, PickerReady: folder.InteractiveDesktop, Drives: drives}
	service := runner.Runner{ID: cfg.RunnerID, Client: client, State: state, Worker: storage, ToolVersions: versions, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	return service.Run(ctx, once)
}
