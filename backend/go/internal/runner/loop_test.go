package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
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

type registrationWorker struct {
	workerFunc
	facts        func() map[string]any
	capabilities []string
}

func (w *registrationWorker) RegistrationFacts() map[string]any { return w.facts() }
func (w *registrationWorker) Capabilities() []string            { return w.capabilities }

func TestRegistrationRefreshUpdatesLocalFactsDuringOperation(t *testing.T) {
	var acquires, registrations, heartbeats atomic.Int32
	var mounted atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	refreshed, heartbeat := make(chan struct{}, 1), make(chan struct{}, 1)
	delivered := make(chan Result, 1)
	logs := make(chan string, 10)
	op := testOperation()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/internal/v1/runners/register":
			var body struct {
				RunnerID     string            `json:"runnerId"`
				Capabilities []string          `json:"capabilities"`
				Tools        map[string]string `json:"tools"`
				Facts        struct {
					Mount struct {
						State string `json:"state"`
					} `json:"mount"`
				} `json:"facts"`
			}
			if json.NewDecoder(req.Body).Decode(&body) != nil || body.RunnerID != "runner" || len(body.Capabilities) != 1 || body.Capabilities[0] != "FETCH_BIL_METADATA" || body.Tools["synthetic-tool"] != "1" {
				t.Error("registration identity or capabilities changed")
			}
			count := registrations.Add(1)
			if count == 1 && body.Facts.Mount.State != "NOT_MOUNTED" {
				t.Error("initial mount fact was not sampled")
			}
			if count == 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":{"code":"synthetic-private-error"}}`)
				return
			}
			envelope(w, map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15})
			if count > 2 && body.Facts.Mount.State == "MOUNTED" {
				select {
				case refreshed <- struct{}{}:
				default:
				}
			}
		case "/internal/v1/leases/acquire":
			if acquires.Add(1) == 1 {
				envelope(w, map[string]any{"operation": op})
			} else {
				envelope(w, map[string]any{"operation": nil})
			}
		case "/internal/v1/leases/lease-test/heartbeat":
			heartbeats.Add(1)
			envelope(w, map[string]any{"desiredAction": "CONTINUE", "leaseExpiresAt": time.Now().Add(time.Minute)})
			select {
			case heartbeat <- struct{}{}:
			default:
			}
		case "/internal/v1/operations/op-test/result":
			var result Result
			if err := json.NewDecoder(req.Body).Decode(&result); err != nil {
				t.Error(err)
			}
			envelope(w, map[string]any{"ackId": "ack", "eventId": result.EventID})
			delivered <- result
		default:
			t.Error(req.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	state, err := executor.OpenState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	client, err := NewClient(server.URL, "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := []string{"FETCH_BIL_METADATA"}
	versions := map[string]string{"synthetic-tool": "1"}
	worker := &registrationWorker{capabilities: capabilities, facts: func() map[string]any {
		mountState := "NOT_MOUNTED"
		if mounted.Load() {
			mountState = "MOUNTED"
		}
		return map[string]any{"mount": map[string]any{"state": mountState}}
	}, workerFunc: func(ctx context.Context, _ Operation) (any, []File, error) {
		close(started)
		select {
		case <-release:
			return map[string]any{"completed": true}, nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}}
	runner := Runner{ID: "runner", State: state, Client: client, Worker: worker, PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond, ToolVersions: versions, Log: func(message string) { logs <- message }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, false) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not start")
	}
	// The registration snapshot remains stable while only local facts change.
	capabilities[0], versions["synthetic-tool"] = "CHANGED", "2"
	mounted.Store(true)
	select {
	case <-refreshed:
	case err := <-done:
		t.Fatalf("registration refresh interrupted operation: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("updated local fact was not registered during operation")
	}
	select {
	case <-heartbeat:
	case <-time.After(3 * time.Second):
		t.Fatal("lease heartbeat stopped during registration refresh")
	}
	select {
	case message := <-logs:
		if message != "control registration refresh failed; will retry" {
			t.Fatalf("uncontrolled registration diagnostic: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("registration failure had no diagnostic")
	}
	close(release)
	select {
	case result := <-delivered:
		if result.Status != "SUCCEEDED" || result.Error != nil {
			t.Fatalf("refresh failure changed operation result: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not deliver its result")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop registration refresh")
	}
	if registrations.Load() < 3 || heartbeats.Load() == 0 {
		t.Fatal("registration refresh and lease heartbeat were not independent")
	}
}

func TestOnceRegistersOnlyAtStartupDuringLongOperation(t *testing.T) {
	var registrations, facts, heartbeats, acquires atomic.Int32
	operationDone := make(chan struct{})
	op := testOperation()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/internal/v1/runners/register":
			registrations.Add(1)
			envelope(w, map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15})
		case "/internal/v1/leases/acquire":
			acquires.Add(1)
			envelope(w, map[string]any{"operation": op})
		case "/internal/v1/leases/lease-test/heartbeat":
			envelope(w, map[string]any{"desiredAction": "CONTINUE", "leaseExpiresAt": time.Now().Add(time.Minute)})
			if heartbeats.Add(1) == 2 {
				close(operationDone)
			}
		case "/internal/v1/operations/op-test/result":
			var result Result
			json.NewDecoder(req.Body).Decode(&result)
			if result.Status != "SUCCEEDED" {
				t.Errorf("once operation status=%s", result.Status)
			}
			envelope(w, map[string]any{"ackId": "ack", "eventId": result.EventID})
		default:
			t.Error(req.URL.Path)
		}
	}))
	defer server.Close()
	state, err := executor.OpenState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	client, _ := NewClient(server.URL, "synthetic-token")
	worker := &registrationWorker{capabilities: []string{"FETCH_BIL_METADATA"}, facts: func() map[string]any { facts.Add(1); return nil }, workerFunc: func(ctx context.Context, _ Operation) (any, []File, error) {
		select {
		case <-operationDone:
			return nil, nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}}
	runner := Runner{ID: "runner", State: state, Client: client, Worker: worker, HeartbeatInterval: 15 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runner.Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	if registrations.Load() != 1 || facts.Load() != 1 || acquires.Load() != 1 || heartbeats.Load() < 2 {
		t.Fatalf("once registration=%d facts=%d acquire=%d heartbeat=%d", registrations.Load(), facts.Load(), acquires.Load(), heartbeats.Load())
	}
}

func TestRegistrationRefreshCancellationJoinsInFlightRequest(t *testing.T) {
	var registrations, facts, diagnostics atomic.Int32
	refreshStarted, requestStopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/internal/v1/runners/register":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if registrations.Add(1) == 1 {
				envelope(w, map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15})
				return
			}
			close(refreshStarted)
			<-req.Context().Done()
			close(requestStopped)
		case "/internal/v1/leases/acquire":
			envelope(w, map[string]any{"operation": nil})
		default:
			t.Error(req.URL.Path)
		}
	}))
	defer server.Close()
	state, err := executor.OpenState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	client, _ := NewClient(server.URL, "synthetic-token")
	worker := &registrationWorker{capabilities: []string{"FETCH_BIL_METADATA"}, facts: func() map[string]any { facts.Add(1); return nil }, workerFunc: func(context.Context, Operation) (any, []File, error) {
		t.Error("idle refresh executed a business operation")
		return nil, nil, nil
	}}
	runner := Runner{ID: "runner", State: state, Client: client, Worker: worker, PollInterval: 10 * time.Millisecond, HeartbeatInterval: 15 * time.Millisecond, Log: func(message string) {
		if message == "control registration refresh failed; will retry" {
			diagnostics.Add(1)
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, false) }()
	select {
	case <-refreshStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("registration refresh did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not join cancelled registration request")
	}
	select {
	case <-requestStopped:
	case <-time.After(time.Second):
		t.Fatal("registration HTTP request remained active after shutdown")
	}
	<-time.After(50 * time.Millisecond)
	if registrations.Load() != 2 || facts.Load() != 2 || diagnostics.Load() != 0 {
		t.Fatalf("refresh continued or logged cancellation: registration=%d facts=%d logs=%d", registrations.Load(), facts.Load(), diagnostics.Load())
	}
}
