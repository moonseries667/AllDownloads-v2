package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

// A real local child process writes a heartbeat. Its parent and unrelated
// sibling are both launched through the production Job Object path.
func TestControlledCancellationHelper(t *testing.T) {
	args := os.Args
	idx := -1
	for i, arg := range args {
		if arg == "--" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	mode, path := args[idx+1], args[idx+2]
	if mode == "parent" {
		executable, _ := os.Executable()
		child := exec.Command(executable, "-test.run=^TestControlledCancellationHelper$", "--", "child", path)
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "sibling" {
		time.Sleep(200 * time.Millisecond)
		if os.WriteFile(path, []byte("completed"), 0600) != nil {
			os.Exit(3)
		}
		return
	}
	for i := 0; ; i++ {
		if err := os.WriteFile(path, []byte(fmt.Sprint(i)), 0600); err != nil {
			os.Exit(4)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func TestCancelControlledProcessTreePreservesSibling(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "child-heartbeat")
	sibling := filepath.Join(dir, "sibling-result")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	finished := make(chan error, 1)
	go func() {
		_, err := RunControlled(ctx, executable, 10*time.Second, "-test.run=^TestControlledCancellationHelper$", "--", "parent", heartbeat)
		stopped <- err
	}()
	go func() {
		_, err := RunControlled(context.Background(), executable, 10*time.Second, "-test.run=^TestControlledCancellationHelper$", "--", "sibling", sibling)
		finished <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(heartbeat); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-stopped; !errors.Is(err, context.Canceled) || errors.Is(err, ErrProcessStopUnconfirmed) {
		t.Fatalf("stop fact: %v", err)
	}
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil || string(before) != string(after) {
		t.Fatal("orphan child still writes", err)
	}
	if err := <-finished; err != nil {
		t.Fatal("sibling stopped", err)
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != "completed" {
		t.Fatal("sibling result", err)
	}
}

func TestCancellationClosesNetworkAndBlocksMergeRetainingPublishedOutput(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	var unselected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/done":
			fmt.Fprint(w, `{"body":[]}`)
		case "/video":
			fmt.Fprint(w, "partial video fixture")
		case "/audio":
			w.WriteHeader(200)
			fmt.Fprint(w, "partial audio")
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(closed)
		default:
			unselected.Add(1)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	engine, err := executor.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	plan := Plan{OperationID: "one-part", ResourceKey: "frozen", Outputs: []Output{
		{ID: "done", RelativePath: "already.json", Mode: "SEPARATE", Inputs: []Input{{TrackID: "sub", Kind: "SUBTITLE", Source: Source{URL: server.URL + "/done", Version: "v1"}}}},
		{ID: "merged", RelativePath: "media.mp4", Mode: "MERGE_AV", Inputs: []Input{{TrackID: "video", Kind: "VIDEO", Source: Source{URL: server.URL + "/video", Version: "v1"}}, {TrackID: "audio", Kind: "AUDIO", Source: Source{URL: server.URL + "/audio", Version: "v1"}}}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var facts []FileFact
	var executionErr error
	go func() {
		facts, executionErr = (&Executor{Files: engine, Downloads: Downloader{server.Client()}}).Execute(ctx, plan)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("audio not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("file did not stop")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("network stream remains open")
	}
	if !errors.Is(executionErr, context.Canceled) || len(facts) != 1 || facts[0].ID != "done" {
		t.Fatal(facts, executionErr)
	}
	if _, err := os.Stat(filepath.Join(root, "media.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled merge published", err)
	}
	if err := executor.VerifyFile(root, facts[0].RelativePath, facts[0].SHA256, facts[0].SizeBytes); err != nil {
		t.Fatal("completed output removed", err)
	}
	if unselected.Load() != 0 {
		t.Fatal("unselected request")
	}
}
