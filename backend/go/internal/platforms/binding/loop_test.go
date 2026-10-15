package binding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

func TestSharedRunnerQuarkLeaseFailureReplayAndRetry(t *testing.T) {
	w, op, provider := setup(t)
	op.RunnerID = "part2-test"
	op.TaskID = "task-test"
	op.Attempt = 1
	op.LeaseID = "lease1"
	op.FencingToken = "1"
	op.LeaseExpiresAt = time.Now().Add(time.Minute)
	provider.fail = "b"
	var mu sync.Mutex
	next := &op
	drop := true
	reports := []runner.Result{}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Header.Get("Authorization") != "Bearer isolated-test-token" {
			rw.WriteHeader(401)
			return
		}
		switch req.URL.Path {
		case "/internal/v1/runners/register":
			json.NewEncoder(rw).Encode(map[string]any{"data": map[string]any{"protocolVersion": 1, "heartbeatSeconds": 15}})
		case "/internal/v1/leases/acquire":
			json.NewEncoder(rw).Encode(map[string]any{"data": map[string]any{"operation": next}})
			next = nil
		case "/internal/v1/operations/op-test/result", "/internal/v1/operations/op-test/reconcile":
			var res runner.Result
			if err := json.NewDecoder(req.Body).Decode(&res); err != nil {
				t.Error(err)
				rw.WriteHeader(400)
				return
			}
			reports = append(reports, res)
			if drop {
				drop = false
				rw.WriteHeader(503)
				return
			}
			json.NewEncoder(rw).Encode(map[string]any{"data": map[string]any{"ackId": "ack-" + res.EventID, "eventId": res.EventID}})
		default:
			t.Errorf("unexpected request %s", req.URL.Path)
			rw.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := runner.NewClient(server.URL, "isolated-test-token")
	if err != nil {
		t.Fatal(err)
	}
	run := func() error {
		state, err := executor.OpenState(w.Base.StateDir)
		if err != nil {
			return err
		}
		defer state.Close()
		r := runner.Runner{ID: op.RunnerID, Client: client, State: state, Worker: w}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return r.Run(ctx, true)
	}
	if err := run(); err == nil {
		t.Fatal("lost acknowledgement should remain pending")
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(reports) != 2 || reports[0].EventID != reports[1].EventID || reports[1].Status != "RETRYABLE_ERROR" || reports[1].Error.Code != "NETWORK_TEMPORARY" || reports[1].Error.Message != "平台请求暂时失败，可重试未完成部分" || len(reports[1].Error.CompletedEffects) != 1 || len(reports[1].Error.PendingEffects) != 1 {
		t.Fatalf("wrong wire facts: %+v", reports)
	}
	op.Attempt = 2
	op.LeaseID = "lease2"
	op.FencingToken = "2"
	op.LeaseExpiresAt = time.Now().Add(time.Minute)
	next = &op
	mu.Unlock()
	provider.fail = ""
	if err := run(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 3 || reports[2].Status != "SUCCEEDED" || provider.calls["a"] != 1 || provider.calls["b"] != 2 {
		t.Fatal(reports, provider.calls)
	}
}
