package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const mainTestTokenEnv = "ALLDOWNLOADS_RUNNER_MAIN_TEST_TOKEN"

type mainTestCapture struct {
	registers  int
	acquires   int
	results    int
	otherCalls int
	registered struct {
		RunnerID     string   `json:"runnerId"`
		Capabilities []string `json:"capabilities"`
	}
	result map[string]any
	ack    map[string]any
}

func newMainTestControl(t *testing.T, operation map[string]any) (*httptest.Server, *mainTestCapture) {
	t.Helper()
	capture := &mainTestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer synthetic-main-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		respond := func(data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "requestId": "request-synthetic"})
		}
		switch r.URL.Path {
		case "/internal/v1/runners/register":
			capture.registers++
			if err := json.NewDecoder(r.Body).Decode(&capture.registered); err != nil {
				t.Errorf("decode registration: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			respond(map[string]any{"runnerId": capture.registered.RunnerID, "protocolVersion": 1, "heartbeatSeconds": 15})
		case "/internal/v1/leases/acquire":
			capture.acquires++
			respond(map[string]any{"operation": operation})
		case "/internal/v1/operations/" + mapString(operation, "operationId") + "/result":
			capture.results++
			var result map[string]any
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				t.Errorf("decode result: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			capture.result = result
			eventID, _ := result["eventId"].(string)
			capture.ack = map[string]any{"ackId": "ack-" + eventID, "eventId": eventID}
			respond(capture.ack)
		default:
			capture.otherCalls++
			t.Errorf("unexpected control/platform request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server, capture
}

func TestFormalRunnerRegistersAndReportsMissingTelegramAndQuarkAccounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		platform  string
		payload   string
	}{
		{"telegram saved scan", "SCAN_TEL_SAVED", "TELEGRAM", `{"schemaVersion":1,"accountId":"missing-telegram","afterMessageId":"0","continuation":null}`},
		{"quark transfer", "TRANSFER_QUARK_ITEMS", "QUARK", `{"schemaVersion":1,"shareId":"synthetic-share","url":"https://pan.quark.cn/s/synthetic","targetFolderId":"folder-id","targetFolderRevision":"revision-1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation := map[string]any{
				"protocolVersion": 1,
				"operationId":     "operation-" + strings.ToLower(tc.operation),
				"taskId":          "task-synthetic",
				"attempt":         1,
				"leaseId":         "lease-synthetic",
				"runnerId":        "runner-main-test",
				"fencingToken":    "1",
				"leaseExpiresAt":  time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
				"operationType":   tc.operation,
				"platform":        tc.platform,
				"accountRef":      "not-configured",
				"resourceKey":     "synthetic-resource",
				"storageRevision": "1",
				"destination":     nil,
				"payload":         json.RawMessage(tc.payload),
			}
			server, capture := newMainTestControl(t, operation)
			defer server.Close()
			configPath := writeMainTestConfig(t, server.URL, "", false)

			if err := run(context.Background(), configPath, true); err != nil {
				t.Fatalf("formal run failed instead of reporting the missing account: %v", err)
			}
			if capture.registers != 1 || capture.acquires != 1 || capture.results != 1 || capture.otherCalls != 0 {
				t.Fatalf("control calls: register=%d acquire=%d result=%d other=%d", capture.registers, capture.acquires, capture.results, capture.otherCalls)
			}
			if capture.registered.RunnerID != "runner-main-test" || !hasMainTestString(capture.registered.Capabilities, "SCAN_TEL_SAVED") || !hasMainTestString(capture.registered.Capabilities, "TRANSFER_QUARK_ITEMS") {
				t.Fatalf("formal worker registered unexpected capabilities: %+v", capture.registered)
			}
			if capture.result == nil || capture.result["status"] != "NEEDS_ACTION" {
				t.Fatalf("operation result = %+v, want NEEDS_ACTION", capture.result)
			}
			failure, _ := capture.result["error"].(map[string]any)
			if failure["code"] != "AUTH_REQUIRED" {
				t.Fatalf("operation error = %+v, want AUTH_REQUIRED", failure)
			}
			if capture.ack == nil || capture.ack["ackId"] == "" || capture.ack["eventId"] != capture.result["eventId"] {
				t.Fatalf("result acknowledgement did not use the durable ackId/eventId shape: %+v", capture.ack)
			}
		})
	}
}

func TestFormalRunnerRejectsFixtureCatalogOutsideIsolatedModeBeforeControlIO(t *testing.T) {
	server, capture := newMainTestControl(t, nil)
	defer server.Close()
	missingFixture := filepath.Join(t.TempDir(), "fixture-that-must-not-be-read.json")
	configPath := writeMainTestConfig(t, server.URL, missingFixture, false)

	err := run(context.Background(), configPath, true)
	if err == nil || !strings.Contains(err.Error(), "fixtureCatalog requires isolatedTestMode") {
		t.Fatalf("run error = %v, want isolatedTestMode rejection", err)
	}
	if capture.registers != 0 || capture.acquires != 0 || capture.results != 0 || capture.otherCalls != 0 {
		t.Fatalf("fixture rejection made control I/O: %+v", capture)
	}
}

func writeMainTestConfig(t *testing.T, controlURL, fixture string, isolated bool) string {
	t.Helper()
	t.Setenv(mainTestTokenEnv, "synthetic-main-token")
	root := t.TempDir()
	configValue := config{
		RunnerID:         "runner-main-test",
		ControlURL:       controlURL,
		TokenEnv:         mainTestTokenEnv,
		StateDir:         filepath.Join(root, "state"),
		AllowedRoots:     []string{root},
		Roots:            map[string]string{"root-test": root},
		FixtureCatalog:   fixture,
		IsolatedTestMode: isolated,
	}
	raw, err := json.Marshal(configValue)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "runner-config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasMainTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mapString(value map[string]any, key string) string {
	if text, ok := value[key].(string); ok {
		return text
	}
	return fmt.Sprint(value[key])
}
