package binding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

type stoppableSource struct {
	*io.PipeReader
	stopped chan struct{}
	once    sync.Once
}

func (s *stoppableSource) Close() error {
	s.once.Do(func() { close(s.stopped) })
	return s.PipeReader.Close()
}

type cancellationClient struct {
	started, stopped chan struct{}
	mu               sync.Mutex
	calls            map[string]int
	data             map[string][]byte
}

func (c *cancellationClient) open(id string) (io.ReadCloser, error) {
	c.mu.Lock()
	c.calls[id]++
	c.mu.Unlock()
	if id != "cancel" {
		return io.NopCloser(bytes.NewReader(c.data[id])), nil
	}
	r, w := io.Pipe()
	source := &stoppableSource{PipeReader: r, stopped: c.stopped}
	close(c.started)
	go func() { _, _ = w.Write([]byte("partial file")); <-c.stopped; _ = w.Close() }()
	return source, nil
}
func (c *cancellationClient) OpenNode(_ context.Context, _ string, n quark.CloudNode) (io.ReadCloser, error) {
	return c.open(n.ID)
}
func (c *cancellationClient) OpenAttachment(_ context.Context, _ string, _ []string, a telegram.DownloadAttachment) (io.ReadCloser, error) {
	return c.open(a.ID)
}

// All operations belong to one visible project but have independent leases and
// outputs. This uses the real publisher, result journal and heartbeat path.
func TestQuarkSingleFileCancellationStopsStreamAndNextFileCompletes(t *testing.T) {
	runCancellationProject(t, "QUARK", false)
}
func TestTelegramGroupVideoCancellationKeepsAudioPhotoAndSidecars(t *testing.T) {
	if os.Getenv("ALLDOWNLOADS_TEST_FFMPEG_DIR") == "" {
		t.Skip("set ALLDOWNLOADS_TEST_FFMPEG_DIR for mixed audio/photo verification")
	}
	runCancellationProject(t, "TELEGRAM", true)
}

func runCancellationProject(t *testing.T, platform string, mixed bool) {
	t.Helper()
	root, stateDir := t.TempDir(), t.TempDir()
	provider := &cancellationClient{started: make(chan struct{}), stopped: make(chan struct{}), calls: map[string]int{}, data: map[string][]byte{"sibling": []byte("sibling content")}}
	base := &runner.Handler{StateDir: stateDir, AllowedRoots: []string{root}, Roots: map[string]string{"root": root}}
	worker := &Worker{Base: base, Quark: provider, Telegram: provider}
	ids := []string{"cancel", "sibling"}
	if mixed {
		toolDir := os.Getenv("ALLDOWNLOADS_TEST_FFMPEG_DIR")
		base.Tools = &media.Tools{FFmpeg: filepath.Join(toolDir, "ffmpeg.exe"), FFprobe: filepath.Join(toolDir, "ffprobe.exe")}
		var photo bytes.Buffer
		if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		provider.data = map[string][]byte{"audio": wav(), "photo": photo.Bytes()}
		ids = []string{"cancel", "audio", "photo"}
	}
	ops := []runner.Operation{}
	for _, id := range ids {
		op := runner.Operation{ProtocolVersion: 1, OperationID: "op-" + id, TaskID: "task-" + id, Attempt: 1, LeaseID: "lease-" + id, RunnerID: "runner", FencingToken: "1", LeaseExpiresAt: time.Now().Add(time.Minute), OperationType: "DOWNLOAD_MEDIA", Platform: platform, AccountRef: str("fixture-account"), ResourceKey: "project-child-" + id, StorageRevision: "1", Destination: &runner.Destination{RootID: "root", RootPath: root, RelativePath: platform + "/project"}}
		out := p.Output{ID: "out-" + id, RelativePath: platform + "/project/" + id, Kind: "FILE", NodeID: id, TrackIDs: []string{}}
		if platform == "QUARK" {
			op.Payload, _ = json.Marshal(quark.DownloadRequest{SchemaVersion: 1, ShareID: "share", ArchivePolicy: "AUTO", Nodes: []quark.CloudNode{{ID: id, ExternalID: id, ShareID: "share", Version: "frozen-v1", Name: id, Kind: "FILE", Availability: "AVAILABLE"}}, Outputs: []p.Output{out}})
		} else {
			kind := "VIDEO"
			if id == "audio" {
				kind = "AUDIO"
			}
			if id == "photo" {
				kind = "PHOTO"
			}
			out.Kind = kind
			out.NodeID = ""
			out.AttachmentID = id
			side := p.Output{ID: "metadata-" + id, RelativePath: out.RelativePath + ".metadata.json", Kind: "METADATA", AttachmentID: id, TrackIDs: []string{}}
			a := telegram.DownloadAttachment{Attachment: telegram.Attachment{ID: id, ExternalID: id, Name: id, MediaType: kind}, MessageID: "101"}
			op.Payload, _ = json.Marshal(telegram.DownloadRequest{SchemaVersion: 1, AccountID: "fixture-account", ResourceID: "one-logical-group", MessageIDs: []string{"101", "102", "103"}, Attachments: []telegram.DownloadAttachment{a}, Outputs: []p.Output{out, side}, ArchivePolicy: "AUTO", Metadata: map[string]any{"groupId": "stable-group", "messageIds": []string{"101", "102", "103"}}})
		}
		ops = append(ops, op)
	}
	var mu sync.Mutex
	reports := map[string]runner.Result{}
	index := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		respond := func(value any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": value}) }
		switch {
		case strings.HasSuffix(req.URL.Path, "/register"):
			respond(map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15})
		case strings.HasSuffix(req.URL.Path, "/acquire"):
			mu.Lock()
			defer mu.Unlock()
			var op *runner.Operation
			if index < len(ops) {
				op = &ops[index]
				index++
			}
			respond(map[string]any{"operation": op})
		case strings.HasSuffix(req.URL.Path, "/heartbeat"):
			action := "CONTINUE"
			if strings.Contains(req.URL.Path, "lease-cancel/") {
				select {
				case <-provider.started:
				case <-time.After(5 * time.Second):
					t.Error("stream did not start")
				}
				action = "CANCEL"
			}
			respond(map[string]any{"desiredAction": action, "leaseExpiresAt": time.Now().Add(time.Minute)})
		case strings.HasSuffix(req.URL.Path, "/result") || strings.HasSuffix(req.URL.Path, "/reconcile"):
			var result runner.Result
			if err := json.NewDecoder(req.Body).Decode(&result); err != nil {
				t.Error(err)
			}
			id := strings.Split(req.URL.Path, "/")[4]
			mu.Lock()
			reports[id] = result
			mu.Unlock()
			respond(map[string]any{"ackId": "ack-" + id, "eventId": result.EventID})
		default:
			t.Error(req.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client, err := runner.NewClient(server.URL, "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		state, err := executor.OpenState(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		storage := &runner.StorageWorker{Next: worker, Roots: base, State: state, HostID: "fixture-host"}
		r := runner.Runner{ID: "runner", Client: client, State: state, Worker: storage, HeartbeatInterval: 15 * time.Millisecond}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err = r.Run(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	for range ops {
		run()
	}
	// Reopen the runner once more: cancelled pending media must not be reopened.
	run()
	select {
	case <-provider.stopped:
	default:
		t.Fatal("cancelled source was never closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if reports["op-cancel"].Status != "CANCELLED" {
		t.Fatal(reports["op-cancel"])
	}
	if _, err := os.Stat(filepath.Join(root, platform, "project", "cancel")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled output published", err)
	}
	for _, id := range ids[1:] {
		if reports["op-"+id].Status != "SUCCEEDED" {
			t.Fatal("sibling did not continue", reports["op-"+id])
		}
		_, files, complete, err := worker.Recover(ops[indexOf(ids, id)])
		if err != nil || !complete || len(files) == 0 {
			t.Fatal("missing durable sibling facts", files, complete, err)
		}
		for _, file := range files {
			if err := executor.VerifyFile(root, file.RelativePath, file.SHA256, file.SizeBytes); err != nil {
				t.Fatal(err)
			}
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, id := range ids {
		if provider.calls[id] != 1 {
			t.Fatal("restarted or redownloaded", provider.calls)
		}
	}
}
func indexOf(values []string, needle string) int {
	for i, v := range values {
		if v == needle {
			return i
		}
	}
	return -1
}
