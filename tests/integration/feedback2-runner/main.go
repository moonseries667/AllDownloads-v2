// Isolated test composition only. No platform URLs, accounts, or production configuration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/binding"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

type fixture struct {
	ControlURL, StateDir, Root, FFmpeg, FFprobe, FailOnce string
	Messages                                              []telegram.RawMessage
	Nodes                                                 []quark.Node
	Files                                                 map[string]string
}
type provider struct{ fixture }

func (p *provider) SavedPage(ctx context.Context, account, after string, cursor *string) (telegram.Page, error) {
	a, _ := strconv.ParseUint(after, 10, 64)
	messages := []telegram.RawMessage{}
	through := after
	for _, m := range p.Messages {
		n, _ := strconv.ParseUint(m.ID, 10, 64)
		if n > a {
			messages = append(messages, m)
			through = m.ID
			if len(messages) == 5 {
				next := "next-page"
				return telegram.Page{Messages: messages, Through: through, Next: &next, Complete: false}, nil
			}
		}
	}
	return telegram.Page{Messages: messages, Through: through, Complete: true}, nil
}
func (p *provider) SavedGroup(ctx context.Context, account, group string) (telegram.Group, error) {
	g := telegram.Group{Complete: true}
	for _, m := range p.Messages {
		if m.GroupID != nil && *m.GroupID == group {
			g.Messages = append(g.Messages, m)
		}
	}
	return g, nil
}
func (p *provider) Root(ctx context.Context, account string) (quark.Node, error) {
	return p.Node(ctx, account, "0")
}
func (p *provider) Node(ctx context.Context, account, id string) (quark.Node, error) {
	for _, n := range p.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return quark.Node{}, errors.New("fixture node missing")
}
func (p *provider) Children(ctx context.Context, account, parent string, cursor *string) (quark.Page, error) {
	r := quark.Page{Complete: true, Nodes: []quark.Node{}}
	for _, n := range p.Nodes {
		if n.ParentID != nil && *n.ParentID == parent {
			r.Nodes = append(r.Nodes, n)
		}
	}
	return r, nil
}
func (p *provider) ShareRoot(ctx context.Context, account, share string) (quark.Node, error) {
	return p.Node(ctx, account, "managed")
}
func (p *provider) open(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	marker := filepath.Join(p.StateDir, "failed-once")
	if id == p.FailOnce {
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if err = os.WriteFile(marker, []byte("injected"), 0600); err != nil {
				return nil, err
			}
			return nil, pkgFault()
		}
	}
	path, ok := p.Files[id]
	if !ok {
		return nil, errors.New("fixture source missing")
	}
	log, err := os.OpenFile(filepath.Join(p.StateDir, "opens.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = fmt.Fprintln(log, id)
	closeErr := log.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return os.Open(path)
}
func pkgFault() error { return p.NewFault("NETWORK_TEMPORARY", "FIXTURE") }
func (p *provider) OpenAttachment(ctx context.Context, account string, messages []string, a telegram.DownloadAttachment) (io.ReadCloser, error) {
	return p.open(ctx, a.ExternalID)
}
func (p *provider) OpenNode(ctx context.Context, account string, n quark.CloudNode) (io.ReadCloser, error) {
	for _, v := range p.Nodes {
		if v.ID == n.ExternalID && v.Revision == n.Version {
			return p.open(ctx, n.ExternalID)
		}
	}
	return nil, errors.New("fixture version changed")
}

// Only this test composition simulates transfer; production has no transfer provider.
type worker struct {
	*binding.Worker
	source *provider
}

func (w *worker) Capabilities() []string {
	return append(w.Worker.Capabilities(), "TRANSFER_QUARK_ITEMS")
}
func (w *worker) Handle(ctx context.Context, op runner.Operation) (any, []runner.File, error) {
	if op.OperationType != "TRANSFER_QUARK_ITEMS" {
		return w.Worker.Handle(ctx, op)
	}
	var req struct {
		ShareID              string `json:"shareId"`
		TargetFolderID       string `json:"targetFolderId"`
		TargetFolderRevision string `json:"targetFolderRevision"`
	}
	if json.Unmarshal(op.Payload, &req) != nil || op.AccountRef == nil {
		return nil, nil, errors.New("invalid transfer fixture")
	}
	if _, err := quark.ValidateTarget(ctx, w.source, *op.AccountRef, quark.Target{FolderID: req.TargetFolderID, Revision: req.TargetFolderRevision}); err != nil {
		return nil, nil, err
	}
	result, err := quark.List(ctx, w.source, quark.ListRequest{SchemaVersion: 1, AccountID: *op.AccountRef, ShareID: &req.ShareID})
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"shareId": req.ShareID, "targetFolderId": req.TargetFolderID, "targetFolderRevision": req.TargetFolderRevision, "nodes": result.Nodes, "complete": true}, nil, nil
}
func main() {
	path := flag.String("fixture", "", "isolated fixture JSON")
	flag.Parse()
	if err := run(*path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f := fixture{}
	if err = json.Unmarshal(raw, &f); err != nil {
		return err
	}
	state, err := executor.OpenState(f.StateDir)
	if err != nil {
		return err
	}
	defer state.Close()
	client, err := runner.NewClient(f.ControlURL, os.Getenv("ALLDOWNLOADS_TEST_TOKEN"))
	if err != nil {
		return err
	}
	tools := &media.Tools{FFmpeg: f.FFmpeg, FFprobe: f.FFprobe}
	if err = tools.Check(context.Background()); err != nil {
		return err
	}
	source := &provider{f}
	base := &runner.Handler{State: state, StateDir: f.StateDir, AllowedRoots: []string{f.Root}, Tools: tools}
	w := &worker{Worker: &binding.Worker{Base: base, Saved: source, Telegram: source, Tree: source, Quark: source}, source: source}
	service := runner.Runner{ID: "part2-fixture", Client: client, State: state, Worker: w, ToolVersions: map[string]string{}, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	return service.Run(context.Background(), true)
}
