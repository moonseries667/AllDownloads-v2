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
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

type config struct {
	RunnerID       string            `json:"runnerId"`
	ControlURL     string            `json:"controlUrl"`
	TokenEnv       string            `json:"tokenEnv"`
	StateDir       string            `json:"stateDir"`
	AllowedRoots   []string          `json:"allowedRoots"`
	Roots          map[string]string `json:"roots"`
	FFmpeg         string            `json:"ffmpeg"`
	FFprobe        string            `json:"ffprobe"`
	FixtureCatalog string            `json:"fixtureCatalog"`
}

func main() {
	path := flag.String("config", "", "runner configuration JSON path")
	once := flag.Bool("once", false, "register, reconcile pending results and acquire at most one operation")
	flag.Parse()
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
	if cfg.TokenEnv == "" {
		cfg.TokenEnv = "ALLDOWNLOADS_INTERNAL_TOKEN"
	}
	client, err := runner.NewClient(cfg.ControlURL, os.Getenv(cfg.TokenEnv))
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
	service := runner.Runner{ID: cfg.RunnerID, Client: client, State: state, Worker: handler, ToolVersions: versions, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	return service.Run(ctx, once)
}
