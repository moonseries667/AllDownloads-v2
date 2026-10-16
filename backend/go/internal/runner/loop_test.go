package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type workerFunc func(context.Context, Operation) (any, []File, error)

func (w workerFunc) Capabilities() []string                                        { return []string{"FETCH_BIL_METADATA"} }
func (w workerFunc) Handle(ctx context.Context, op Operation) (any, []File, error) { return w(ctx, op) }
func envelope(w http.ResponseWriter, value any) {
	json.NewEncoder(w).Encode(map[string]any{"data": value, "requestId": "test"})
}
func testOperation() Operation {
	return Operation{ProtocolVersion: 1, OperationID: "op-test", TaskID: "task", Attempt: 1, LeaseID: "lease-test", RunnerID: "runner", FencingToken: "1", LeaseExpiresAt: time.Now().Add(time.Minute), OperationType: "FETCH_BIL_METADATA", Platform: "BILIBILI", ResourceKey: "resource", StorageRevision: "1", Payload: raw(map[string]any{"schemaVersion": 1})}
}

func TestLostConfirmationReplaysDurableResultWithoutExecution(t *testing.T) {
	var executions, acquires, deliveries atomic.Int32
	op := testOperation()
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer synthetic-test-token" || req.Header.Get("Origin") != "" {
			t.Error("internal authentication")
		}
		switch req.URL.Path {
		case "/internal/v1/runners/register":
			envelope(w, map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15})
		case "/internal/v1/leases/acquire":
			if acquires.Add(1) == 1 {
				envelope(w, map[string]any{"operation": op})
			} else {
				envelope(w, map[string]any{"operation": nil})
			}
		case "/internal/v1/operations/op-test/result", "/internal/v1/operations/op-test/reconcile":
			var result Result
			json.NewDecoder(req.Body).Decode(&result)
			received = append(received, result.EventID)
			if deliveries.Add(1) == 1 {
				w.WriteHeader(503)
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "RESPONSE_LOST"}})
				return
			}
			envelope(w, map[string]any{"ackId": "durable-ack", "eventId": result.EventID})
		default:
			t.Error(req.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	stateDir := t.TempDir()
	state, err := executor.OpenState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(server.URL, "synthetic-test-token")
	if err != nil {
		t.Fatal(err)
	}
	worker := workerFunc(func(ctx context.Context, op Operation) (any, []File, error) {
		executions.Add(1)
		return map[string]any{"content": "fixture"}, nil, nil
	})
	r := Runner{ID: "runner", State: state, Client: client, Worker: worker}
	if err := r.Run(context.Background(), true); err == nil {
		t.Fatal("lost confirmation ignored")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = executor.OpenState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	r.State = state
	if err := r.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 1 || deliveries.Load() != 2 || len(received) != 2 || received[0] != received[1] {
		t.Fatal("result replay not idempotent")
	}
	var entry journal
	if err := state.Read("operation-"+digest(op.OperationID), &entry); err != nil || entry.AckID != "durable-ack" {
		t.Fatal(err)
	}
}
func TestHeartbeatPauseCancelAndLeaseLossStopExecution(t *testing.T) {
	for _, action := range []string{"PAUSE", "CANCEL", "LEASE_LOST"} {
		t.Run(action, func(t *testing.T) {
			op := testOperation()
			if action == "LEASE_LOST" {
				op.LeaseExpiresAt = time.Now().Add(2200 * time.Millisecond)
			}
			var status string
			var heartbeats atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/register"):
					envelope(w, map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15})
				case strings.HasSuffix(r.URL.Path, "/acquire"):
					envelope(w, map[string]any{"operation": op})
				case strings.HasSuffix(r.URL.Path, "/heartbeat"):
					heartbeats.Add(1)
					if action == "LEASE_LOST" {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":{"code":"UNAVAILABLE"}}`)
					} else {
						envelope(w, map[string]any{"desiredAction": action, "leaseExpiresAt": time.Now().Add(time.Minute)})
					}
				case strings.HasSuffix(r.URL.Path, "/result") || strings.HasSuffix(r.URL.Path, "/reconcile"):
					var result Result
					json.NewDecoder(r.Body).Decode(&result)
					status = result.Status
					envelope(w, map[string]any{"ackId": "ack", "eventId": result.EventID})
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer server.Close()
			state, err := executor.OpenState(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			client, _ := NewClient(server.URL, "test")
			worker := workerFunc(func(ctx context.Context, op Operation) (any, []File, error) {
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(3 * time.Second):
					t.Error("execution did not stop")
					return nil, nil, nil
				}
			})
			r := Runner{ID: "runner", State: state, Client: client, Worker: worker, HeartbeatInterval: 20 * time.Millisecond}
			if err := r.Run(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{"PAUSE": "PAUSED", "CANCEL": "CANCELLED", "LEASE_LOST": "RESULT_UNKNOWN"}[action]
			if status != expected || heartbeats.Load() == 0 {
				t.Fatalf("status=%s heartbeat=%d", status, heartbeats.Load())
			}
		})
	}
}
func TestControlClientRejectsNonLoopback(t *testing.T) {
	for _, address := range []string{"https://example.com", "http://localhost:8080", "http://127.0.0.1:8080/?token=x", "http://user:secret@127.0.0.1"} {
		if _, err := NewClient(address, "test"); err == nil {
			t.Fatal("accepted", address)
		}
	}
}
