package media

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDownloadResumesOnlyMatchingStrongVersion(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if count.Add(1) == 1 {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(200)
			fmt.Fprint(w, "0123")
			return
		}
		if r.Header.Get("Range") != "bytes=4-" || r.Header.Get("If-Range") != `"v1"` {
			t.Errorf("range headers: %v", r.Header)
		}
		w.Header().Set("Content-Range", "bytes 4-9/10")
		w.Header().Set("Content-Length", "6")
		w.WriteHeader(206)
		fmt.Fprint(w, "456789")
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "media.part")
	d := Downloader{Client: server.Client()}
	source := Source{URL: server.URL, Version: "track-v1"}
	if err := d.Fetch(context.Background(), source, path); Classify(err, "").Code != "NETWORK_INTERRUPTED" {
		t.Fatalf("first error %v", err)
	}
	if err := d.Fetch(context.Background(), source, path); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "0123456789" {
		t.Fatalf("content %s", raw)
	}
	if err := d.Fetch(context.Background(), source, path); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 2 {
		t.Fatal("complete file re-downloaded")
	}
	if err := os.WriteFile(path, []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Fetch(context.Background(), source, path); Classify(err, "").Code != "PARTIAL_FILE_CORRUPT" {
		t.Fatalf("corruption accepted: %v", err)
	}
}
func TestChangedRangeRejectedAndNoValidatorRestarts(t *testing.T) {
	for _, etag := range []string{`"old"`, ""} {
		t.Run(etag, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if count.Add(1) == 1 {
					w.Header().Set("ETag", etag)
					w.Header().Set("Content-Length", "6")
					fmt.Fprint(w, "abc")
					return
				}
				if etag != "" {
					w.Header().Set("ETag", `"changed"`)
					w.Header().Set("Content-Range", "bytes 3-5/6")
					w.WriteHeader(206)
					fmt.Fprint(w, "xyz")
				} else {
					if r.Header.Get("Range") != "" {
						t.Error("unvalidated range")
					}
					fmt.Fprint(w, "abcdef")
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "part")
			d := Downloader{server.Client()}
			src := Source{URL: server.URL, Version: "stable"}
			_ = d.Fetch(context.Background(), src, path)
			err := d.Fetch(context.Background(), src, path)
			if etag != "" {
				if Classify(err, "").Code != "RESOURCE_VERSION_CHANGED" {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestFailuresAreNotDeletionEvidence(t *testing.T) {
	for _, status := range []int{403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer s.Close()
			err := (Downloader{s.Client()}).Fetch(context.Background(), Source{URL: s.URL + "?private=secret", Version: "v"}, filepath.Join(t.TempDir(), "file"))
			if err == nil || strings.Contains(err.Error(), "secret") || Classify(err, "").Code == "RESOURCE_DELETED" {
				t.Fatal(err)
			}
		})
	}
}
func TestUnknownLengthAndCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "unknown length")
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "file")
	if err := (Downloader{s.Client()}).Fetch(context.Background(), Source{URL: s.URL, Version: "v"}, path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Downloader{s.Client()}).Fetch(ctx, Source{URL: s.URL, Version: "new"}, filepath.Join(t.TempDir(), "file")); err != context.Canceled {
		t.Fatal(err)
	}
}
