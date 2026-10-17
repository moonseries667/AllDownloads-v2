package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type mirrorTransport func(*http.Request) (*http.Response, error)

func (f mirrorTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestMirrorFallbackBeforeBodyKeepsFrozenRepresentation(t *testing.T) {
	for _, mode := range []string{"transport", "503"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/final" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("ETag", `"same-representation"`)
				fmt.Fprint(w, "data")
			}))
			defer server.Close()
			base := http.DefaultTransport.(*http.Transport).Clone()
			defer base.CloseIdleConnections()
			var requests []string
			headers := http.Header{BilibiliIdentityHeader: {"synthetic-id"}, "Referer": {"https://www.bilibili.com/"}, "X-Representation": {"frozen"}}
			client := &http.Client{Transport: mirrorTransport(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.URL.Path)
				if req.Header.Get(BilibiliIdentityHeader) != "synthetic-id" || req.Header.Get("X-Representation") != "frozen" || req.Header.Get("Referer") != "https://www.bilibili.com/" || req.Header.Get("Accept-Encoding") != "identity" {
					t.Error("mirror request changed frozen headers")
				}
				if mode == "transport" && req.URL.Path == "/primary" {
					return nil, errors.New("private transport detail")
				}
				return base.RoundTrip(req)
			})}
			size := int64(4)
			source := Source{URL: server.URL + "/primary", Version: "track-v1", Headers: headers.Clone(), Size: &size, BackupURLs: []string{server.URL + "/primary", "", server.URL + "/backup", server.URL + "/backup", server.URL + "/final", server.URL + "/primary"}}
			path := filepath.Join(t.TempDir(), "input.part")
			if err := (Downloader{Client: client}).Fetch(context.Background(), source, path); err != nil {
				t.Fatalf("pre-body failure did not try official backups: %v", err)
			}
			if !reflect.DeepEqual(requests, []string{"/primary", "/backup", "/final"}) {
				t.Fatalf("mirror order or deduplication changed: %v", requests)
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != "data" || !reflect.DeepEqual(source.Headers, headers) || source.Version != "track-v1" || *source.Size != 4 {
				t.Fatal("mirror changed the frozen representation or result", err)
			}
			var state downloadState
			encoded, err := os.ReadFile(path + ".json")
			if err != nil || json.Unmarshal(encoded, &state) != nil || state.Version != "track-v1" || !state.Complete || state.Total != 4 {
				t.Fatal("mirror changed persisted version or completeness", err)
			}
		})
	}
}

func TestMirrorDoesNotSwitchForHTTPOrRepresentationFailures(t *testing.T) {
	changedSize := int64(8)
	for _, test := range []struct {
		name   string
		status int
		size   *int64
		code   string
	}{
		{"unauthorized", 401, nil, "AUTH_REQUIRED"},
		{"forbidden", 403, nil, "PERMISSION_DENIED"},
		{"unavailable", 404, nil, "RESOURCE_UNAVAILABLE"},
		{"gone", 410, nil, "RESOURCE_UNAVAILABLE"},
		{"rate limited", 429, nil, "RATE_LIMITED"},
		{"unknown response", 302, nil, "PLATFORM_RESPONSE_UNKNOWN"},
		{"invalid range", 206, nil, "RESOURCE_VERSION_CHANGED"},
		{"size changed", 200, &changedSize, "RESOURCE_VERSION_CHANGED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var primary, backups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/backup" {
					backups.Add(1)
					fmt.Fprint(w, "data")
					return
				}
				primary.Add(1)
				w.Header().Set("Content-Length", "4")
				w.WriteHeader(test.status)
				fmt.Fprint(w, "data")
			}))
			defer server.Close()
			source := Source{URL: server.URL + "/primary", Version: "same", Size: test.size, BackupURLs: []string{server.URL + "/backup"}}
			err := (Downloader{Client: server.Client()}).Fetch(context.Background(), source, filepath.Join(t.TempDir(), "part"))
			if err == nil || Classify(err, "").Code != test.code || primary.Load() != 1 || backups.Load() != 0 {
				t.Fatalf("non-network failure switched mirror: primary=%d backup=%d err=%v", primary.Load(), backups.Load(), err)
			}
		})
	}
}

func TestMirrorPreservesTypedFailureWithoutSwitching(t *testing.T) {
	for _, test := range []struct {
		code      string
		retryable bool
	}{
		{"AUTH_REQUIRED", false}, {"PERMISSION_DENIED", false}, {"RATE_LIMITED", true},
		{"PLATFORM_RESPONSE_UNKNOWN", false}, {"NETWORK_TEMPORARY", true}, {"NETWORK_ERROR", true},
	} {
		t.Run(test.code, func(t *testing.T) {
			failure := &Failure{Code: test.code, Stage: "BILIBILI_SESSION", Retryable: test.retryable}
			requests := 0
			client := &http.Client{Transport: mirrorTransport(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, fmt.Errorf("private upstream context: %w", failure)
			})}
			source := Source{URL: "http://127.0.0.1:1/primary", Version: "same", BackupURLs: []string{"http://127.0.0.1:1/backup"}}
			err := (Downloader{Client: client}).Fetch(context.Background(), source, filepath.Join(t.TempDir(), "part"))
			if err != failure || Classify(err, "").Retryable != test.retryable || requests != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("typed controlled failure was changed or used as mirror permission: requests=%d err=%v", requests, err)
			}
		})
	}
}

func TestMirrorCancellationTakesPrecedenceOverTypedFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := 0
	client := &http.Client{Transport: mirrorTransport(func(*http.Request) (*http.Response, error) {
		requests++
		cancel()
		return nil, Fail("AUTH_REQUIRED", "BILIBILI_SESSION", false)
	})}
	source := Source{URL: "http://127.0.0.1:1/primary", Version: "same", BackupURLs: []string{"http://127.0.0.1:1/backup"}}
	if err := (Downloader{Client: client}).Fetch(ctx, source, filepath.Join(t.TempDir(), "part")); !errors.Is(err, context.Canceled) || requests != 1 {
		t.Fatal("cancelled request changed failure or tried a backup", err, requests)
	}
}

func TestMirrorBodyInterruptionAndCancellationDoNotSwitch(t *testing.T) {
	for _, mode := range []string{"interruption", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			var backups atomic.Int32
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/backup" {
					backups.Add(1)
					fmt.Fprint(w, "backup data")
					return
				}
				w.Header().Set("Content-Length", "10")
				w.Header().Set("ETag", `"same"`)
				fmt.Fprint(w, "part")
				if mode == "cancellation" {
					w.(http.Flusher).Flush()
					close(entered)
					<-req.Context().Done()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := Source{URL: server.URL + "/primary", Version: "same", BackupURLs: []string{server.URL + "/backup"}}
			path := filepath.Join(t.TempDir(), "part")
			done := make(chan error, 1)
			go func() { done <- (Downloader{Client: server.Client()}).Fetch(ctx, source, path) }()
			if mode == "cancellation" {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("body did not start")
				}
				deadline := time.Now().Add(time.Second)
				for {
					if info, err := os.Stat(path); err == nil && info.Size() == 4 {
						cancel()
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("body bytes were not consumed before cancellation")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			select {
			case err := <-done:
				if err == nil || mode == "interruption" && Classify(err, "").Code != "NETWORK_INTERRUPTED" || mode == "cancellation" && !errors.Is(err, context.Canceled) || backups.Load() != 0 {
					t.Fatalf("body failure switched mirror: backup=%d err=%v", backups.Load(), err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("body failure did not stop")
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != "part" {
				t.Fatal("body failure did not preserve downloaded bytes", err)
			}
		})
	}
}

func TestMirrorPartialValidatorPreventsMixedBytesAndAllowsFullRestart(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusPartialContent} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var primary, backups, unexpected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/primary":
					if primary.Add(1) == 1 {
						w.Header().Set("ETag", `"old"`)
						w.Header().Set("Content-Length", "10")
						fmt.Fprint(w, "old")
						return
					}
					if req.Header.Get("Range") != "bytes=3-" || req.Header.Get("If-Range") != `"old"` {
						t.Error("primary retry lost the partial validator")
					}
					w.WriteHeader(http.StatusServiceUnavailable)
				case "/backup":
					backups.Add(1)
					if req.Header.Get("Range") != "bytes=3-" || req.Header.Get("If-Range") != `"old"` {
						t.Error("mirror request lost the partial validator")
					}
					w.Header().Set("ETag", `"new"`)
					if status == http.StatusPartialContent {
						w.Header().Set("Content-Range", "bytes 3-9/10")
						w.WriteHeader(http.StatusPartialContent)
						fmt.Fprint(w, "CONTENT")
					} else {
						fmt.Fprint(w, "NEWCONTENT")
					}
				default:
					unexpected.Add(1)
					fmt.Fprint(w, "unexpected")
				}
			}))
			defer server.Close()
			download := Downloader{Client: server.Client()}
			size := int64(10)
			source := Source{URL: server.URL + "/primary", Version: "stable-track", Size: &size, BackupURLs: []string{server.URL + "/backup", server.URL + "/unexpected"}}
			path := filepath.Join(t.TempDir(), "part")
			if err := download.Fetch(context.Background(), source, path); err == nil || Classify(err, "").Code != "NETWORK_INTERRUPTED" || backups.Load() != 0 {
				t.Fatal("initial body interruption tried a mirror", err)
			}
			err := download.Fetch(context.Background(), source, path)
			content, readErr := os.ReadFile(path)
			if status == http.StatusPartialContent {
				if err == nil || Classify(err, "").Code != "RESOURCE_VERSION_CHANGED" || readErr != nil || string(content) != "old" || unexpected.Load() != 0 {
					t.Fatal("mirror combined different validators or continued after a version change", err, readErr)
				}
				return
			}
			if err != nil || readErr != nil || string(content) != "NEWCONTENT" || primary.Load() != 2 || backups.Load() != 1 || unexpected.Load() != 0 {
				t.Fatal("200 with a new ETag did not replace the old partial bytes", err, readErr)
			}
			if err := download.Fetch(context.Background(), source, path); err != nil || primary.Load() != 2 || backups.Load() != 1 {
				t.Fatal("complete mirrored file made another request", err)
			}
			source.Version = "changed-track"
			if err := download.Fetch(context.Background(), source, path); err == nil || Classify(err, "").Code != "RESOURCE_VERSION_CHANGED" || primary.Load() != 2 || backups.Load() != 1 {
				t.Fatal("a changed source version accessed another mirror", err)
			}
		})
	}
}

func TestMirrorAllTransportFailuresReturnControlledError(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: mirrorTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("private upstream address and diagnostic")
	})}
	source := Source{URL: "http://127.0.0.1:1/primary", Version: "same", BackupURLs: []string{"http://127.0.0.1:1/backup"}}
	err := (Downloader{Client: client}).Fetch(context.Background(), source, filepath.Join(t.TempDir(), "part"))
	if err == nil || err.Error() != "DOWNLOAD: NETWORK_ERROR" || !Classify(err, "").Retryable || requests != 2 {
		t.Fatal("mirror failure lost its controlled retryable classification", err)
	}
}
