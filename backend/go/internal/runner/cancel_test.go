package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

type recoveryWorker struct {
	complete bool
	calls    int
}

func (w *recoveryWorker) Capabilities() []string { return []string{"DOWNLOAD_MEDIA"} }
func (w *recoveryWorker) Handle(context.Context, Operation) (any, []File, error) {
	w.calls++
	return nil, nil, nil
}
func (w *recoveryWorker) Recover(Operation) (any, []File, bool, error) {
	return map[string]any{"files": []File{}, "warnings": []string{}}, nil, w.complete, nil
}

func TestDurableCancelRecoveryDoesNotResurrectAndCompletionWins(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "published"}[complete], func(t *testing.T) {
			stateDir := t.TempDir()
			state, err := executor.OpenState(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			op := testOperation()
			op.OperationType = "DOWNLOAD_MEDIA"
			r := Runner{ID: "runner", State: state}
			if err := r.saveControl(op, "CANCEL"); err != nil {
				t.Fatal(err)
			}
			if err := state.Write("operation-"+digest(op.OperationID), journal{Operation: op}); err != nil {
				t.Fatal(err)
			}
			_ = state.Close()
			state, err = executor.OpenState(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			var reported Result
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if !strings.HasSuffix(req.URL.Path, "/reconcile") {
					t.Error(req.URL.Path)
				}
				_ = json.NewDecoder(req.Body).Decode(&reported)
				envelope(w, map[string]any{"ackId": "ack", "eventId": reported.EventID})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, "fixture")
			worker := &recoveryWorker{complete: complete}
			r.State = state
			r.Client = client
			r.Worker = worker
			if err := r.replay(context.Background()); err != nil {
				t.Fatal(err)
			}
			expected := "CANCELLED"
			if complete {
				expected = "SUCCEEDED"
			}
			if reported.Status != expected || worker.calls != 0 {
				t.Fatal(reported, worker.calls)
			}
			op.Attempt++
			op.FencingToken = "2"
			if err := r.runOperation(context.Background(), op); err == nil || worker.calls != 0 {
				t.Fatal("restarted cancelled/completed operation", err)
			}
		})
	}
}

func TestCancelCompletionRaceReportsPublishedFacts(t *testing.T) {
	state, err := executor.OpenState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	op := testOperation()
	root := t.TempDir()
	engine, err := executor.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	op.Destination = &Destination{RootID: "root", RootPath: root, RelativePath: ""}
	published := make(chan struct{})
	var reported Result
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/heartbeat") {
			<-published
			envelope(w, map[string]any{"desiredAction": "CANCEL", "leaseExpiresAt": time.Now().Add(time.Minute)})
			return
		}
		_ = json.NewDecoder(req.Body).Decode(&reported)
		envelope(w, map[string]any{"ackId": "ack", "eventId": reported.EventID})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "fixture")
	worker := workerFunc(func(ctx context.Context, op Operation) (any, []File, error) {
		receipt, err := engine.Execute(ctx, executor.Request{OperationID: op.OperationID, ResourceKey: "fixture", RelativePath: "finished.txt", Content: "published before cancellation"})
		close(published)
		if err != nil {
			return nil, nil, err
		}
		files := []File{{OutputID: "output", RelativePath: receipt.RelativePath, SizeBytes: receipt.SizeBytes, SHA256: receipt.SHA256, Verified: true}}
		<-ctx.Done()
		return map[string]any{"files": files, "warnings": []string{}}, files, nil
	})
	r := Runner{ID: "runner", State: state, Client: client, Worker: worker, HeartbeatInterval: 10 * time.Millisecond}
	if err := r.runOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if reported.Status != "SUCCEEDED" || reported.Error != nil {
		t.Fatal(reported)
	}
	var record journal
	if err := state.Read("operation-"+digest(op.OperationID), &record); err != nil || len(record.Files) != 1 {
		t.Fatal("published fact missing", err)
	}
	if err := executor.VerifyFile(root, record.Files[0].RelativePath, record.Files[0].SHA256, record.Files[0].SizeBytes); err != nil {
		t.Fatal("completed file removed", err)
	}
	if intent, err := r.controlIntent(op); err != nil || intent != "CANCEL" {
		t.Fatal("cancel not durable", intent, err)
	}
}
