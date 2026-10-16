// The integration executable uses production workers against loopback-only synthetic providers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

type config struct{ ControlURL, TokenFile, Root, StateDir, Catalog, DriveURL, FFmpegDir string }

func main() {
	if len(os.Args) != 2 {
		panic("expected integration configuration path")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var cfg config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		panic(err)
	}
	for _, address := range []string{cfg.ControlURL, cfg.DriveURL} {
		u, e := url.Parse(address)
		if e != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			panic("integration endpoints must be loopback")
		}
	}
	token, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		panic(err)
	}
	client, err := runner.NewClient(cfg.ControlURL, string(token))
	if err != nil {
		panic(err)
	}
	state, err := executor.OpenState(cfg.StateDir)
	if err != nil {
		panic(err)
	}
	defer state.Close()
	raw, err = os.ReadFile(cfg.Catalog)
	if err != nil {
		panic(err)
	}
	catalog := runner.FixtureProvider{}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		panic(err)
	}
	mediaTools := &media.Tools{FFmpeg: filepath.Join(cfg.FFmpegDir, "ffmpeg.exe"), FFprobe: filepath.Join(cfg.FFmpegDir, "ffprobe.exe")}
	if err = mediaTools.Check(context.Background()); err != nil {
		panic(err)
	}
	handler := &runner.Handler{State: state, StateDir: cfg.StateDir, AllowedRoots: []string{cfg.Root}, Roots: map[string]string{}, Provider: catalog, Tools: mediaTools, Downloads: media.Downloader{Client: &http.Client{Timeout: time.Minute}}}
	worker := &runner.StorageWorker{Next: handler, Roots: handler, State: state, HostID: "integration-host", Drives: map[string]*drive.Client{"fixture-drive": {AccountID: "fixture-drive", RootID: "root", BaseURL: cfg.DriveURL, Token: func() string { return "synthetic-fixture-only" }}}}
	service := runner.Runner{ID: "integration-runner", Client: client, State: state, Worker: worker, PollInterval: 50 * time.Millisecond, HeartbeatInterval: 100 * time.Millisecond, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err = service.Run(ctx, false); err != nil && ctx.Err() == nil {
		panic(err)
	}
}
