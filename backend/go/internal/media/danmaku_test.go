package media

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

func deflateDanmakuFixture(t *testing.T, content []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer, err := flate.NewWriter(&encoded, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestDanmakuCachedDeflatePublishesXMLAndReusesEarlierReceipt(t *testing.T) {
	plain := []byte(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>fixture.invalid</chatserver><chatid>123</chatid><mission>0</mission><maxlimit>0</maxlimit><state>0</state><real_name>0</real_name></i>`)
	compressed := deflateDanmakuFixture(t, plain)
	transportSize := int64(len(compressed))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.URL.Path != "/dm" || req.Header.Get("Accept-Encoding") != "identity" {
			t.Error("replayed media or changed the cached danmaku request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", `"raw-deflate"`)
		w.Header().Set("Content-Encoding", "deflate")
		w.Write(compressed)
	}))
	defer server.Close()
	state, root := t.TempDir(), t.TempDir()
	engine, err := executor.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{OperationID: "existing-order", ResourceKey: "frozen-revision", Outputs: []Output{
		{ID: "mux", RelativePath: "video.mp4", Mode: "MERGE_AV", Inputs: []Input{{TrackID: "video", Kind: "VIDEO", Source: Source{URL: server.URL + "/video", Version: "video-v1"}}, {TrackID: "audio", Kind: "AUDIO", Source: Source{URL: server.URL + "/audio", Version: "audio-v1"}}}},
		{ID: "dm", RelativePath: "danmaku.xml", Mode: "SEPARATE", Inputs: []Input{{TrackID: "dm", Kind: "DANMAKU", Source: Source{URL: server.URL + "/dm", Version: "dm-v1", Size: &transportSize}}}},
	}}
	// Seed a synthetic earlier publication: this test verifies receipt reuse,
	// rather than generating or claiming a real media encode.
	previous := filepath.Join(t.TempDir(), "already-verified.fixture")
	if err := os.WriteFile(previous, []byte("synthetic previously verified mux"), 0600); err != nil {
		t.Fatal(err)
	}
	first := plan.Outputs[0]
	if _, err := engine.ExecuteFile(context.Background(), executor.Request{OperationID: OutputOperationID(plan.OperationID, first.ID), ResourceKey: fileResourceKey(plan.ResourceKey, first), RelativePath: first.RelativePath}, previous); err != nil {
		t.Fatal(err)
	}
	download := Downloader{Client: server.Client()}
	input, err := engine.WorkFile(OutputOperationID(plan.OperationID, "dm"), "input-0.part")
	if err != nil {
		t.Fatal(err)
	}
	if err := download.Fetch(context.Background(), plan.Outputs[1].Inputs[0].Source, input); err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadFile(input + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = executor.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	// No Tools are supplied: an attempt to repeat the earlier mux must fail.
	facts, err := (&Executor{Files: engine, Downloads: download}).Execute(context.Background(), plan)
	if err != nil || len(facts) != 2 {
		t.Fatalf("cached raw DEFLATE was not repaired: facts=%d err=%v", len(facts), err)
	}
	content, err := os.ReadFile(filepath.Join(root, "danmaku.xml"))
	if err != nil || !bytes.Equal(content, plain) {
		t.Fatal("published bytes are not the decoded XML", err)
	}
	expectedHash := sha256.Sum256(plain)
	if facts[1].SHA256 != hex.EncodeToString(expectedHash[:]) || facts[1].SizeBytes != strconv.Itoa(len(plain)) {
		t.Fatal("public facts describe compressed rather than published XML bytes")
	}
	keptInput, inputErr := os.ReadFile(input)
	keptState, stateErr := os.ReadFile(input + ".json")
	if inputErr != nil || stateErr != nil || !bytes.Equal(keptInput, compressed) || !bytes.Equal(keptState, beforeState) || requests.Load() != 1 {
		t.Fatal("repair changed the raw cache or fetched a completed input", inputErr, stateErr, requests.Load())
	}
	if _, err := os.Stat(filepath.Join(root, "video.mp4")); err != nil {
		t.Fatal("earlier verified publication disappeared", err)
	}
}

func TestDanmakuPlainXMLKeepsOriginalBytes(t *testing.T) {
	for _, plain := range []string{"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\r\n<i></i>\r\n", `<i><d p="0">合成弹幕</d></i>`} {
		t.Run(strconv.Itoa(len(plain)), func(t *testing.T) {
			root, facts, err := executeDanmakuFixture(t, []byte(plain), "DANMAKU")
			if err != nil || len(facts) != 1 {
				t.Fatalf("plain XML no longer publishes: facts=%d err=%v", len(facts), err)
			}
			published, err := os.ReadFile(filepath.Join(root, "supplement.xml"))
			if err != nil || string(published) != plain {
				t.Fatal("plain XML bytes were rewritten", err)
			}
		})
	}
}

func executeDanmakuFixture(t *testing.T, body []byte, kind string) (string, []FileFact, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
	t.Cleanup(server.Close)
	root := t.TempDir()
	engine, err := executor.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	size := int64(len(body))
	plan := Plan{OperationID: "supplement-order", ResourceKey: "same-representation", Outputs: []Output{{ID: "supplement", RelativePath: "supplement.xml", Mode: "SEPARATE", Inputs: []Input{{TrackID: "track", Kind: kind, Source: Source{URL: server.URL, Version: "track-v1", Size: &size}}}}}}
	facts, err := (&Executor{Files: engine, Downloads: Downloader{Client: server.Client()}}).Execute(context.Background(), plan)
	return root, facts, err
}

func TestDanmakuInvalidAndTruncatedInputsNeverPublish(t *testing.T) {
	compressed := deflateDanmakuFixture(t, []byte(`<i><d p="0">合成弹幕</d></i>`))
	for _, test := range []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"non XML", []byte("synthetic non-XML response")},
		{"wrong root", []byte("<html></html>")},
		{"truncated XML", []byte("<i><d>")},
		{"deflated non XML", deflateDanmakuFixture(t, []byte("synthetic non-XML response"))},
		{"deflated wrong root", deflateDanmakuFixture(t, []byte("<html></html>"))},
		{"truncated raw DEFLATE", compressed[:len(compressed)-1]},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, facts, err := executeDanmakuFixture(t, test.body, "DANMAKU")
			if err == nil || Classify(err, "").Code != "INVALID_DANMAKU" || len(facts) != 0 {
				t.Fatalf("invalid danmaku returned published facts: facts=%d err=%v", len(facts), err)
			}
			if _, err := os.Stat(filepath.Join(root, "supplement.xml")); !os.IsNotExist(err) {
				t.Fatal("invalid danmaku was published", err)
			}
		})
	}
}

func TestDanmakuOnlyDecodesItsOwnSupplementKind(t *testing.T) {
	root, facts, err := executeDanmakuFixture(t, deflateDanmakuFixture(t, []byte(`{"body":[]}`)), "SUBTITLE")
	if err == nil || Classify(err, "").Code != "INVALID_SUBTITLE" || len(facts) != 0 {
		t.Fatal("danmaku decoding changed another supplement format", err)
	}
	if _, err := os.Stat(filepath.Join(root, "supplement.xml")); !os.IsNotExist(err) {
		t.Fatal("compressed subtitle was published", err)
	}
}

func TestDanmakuDecodedSizeIsBoundedAt64MiB(t *testing.T) {
	var compressed bytes.Buffer
	writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "<i>"); err != nil {
		t.Fatal(err)
	}
	padding := bytes.Repeat([]byte{' '}, 32<<10)
	for remaining := 64 << 20; remaining > 0; remaining -= len(padding) {
		if _, err := writer.Write(padding); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.WriteString(writer, "</i>"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	root, facts, err := executeDanmakuFixture(t, compressed.Bytes(), "DANMAKU")
	if err == nil || Classify(err, "").Code != "INVALID_DANMAKU" || len(facts) != 0 {
		t.Fatal("over-limit decoded XML was accepted", err)
	}
	if _, err := os.Stat(filepath.Join(root, "supplement.xml")); !os.IsNotExist(err) {
		t.Fatal("over-limit decoded XML was published", err)
	}
}

func TestDanmakuPreparationPreservesCancellationAndLocalWriteFailure(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "raw.part")
	raw := deflateDanmakuFixture(t, []byte("<i></i>"))
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	decoded := filepath.Join(root, "decoded.xml")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareDanmaku(ctx, input, decoded); !errors.Is(err, context.Canceled) {
		t.Fatal("preparation hid cancellation", err)
	}
	if _, err := os.Stat(decoded); !os.IsNotExist(err) {
		t.Fatal("cancelled preparation created an output", err)
	}
	if err := os.Mkdir(decoded, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := prepareDanmaku(context.Background(), input, decoded)
	var localError *os.PathError
	if !errors.As(err, &localError) || Classify(err, "").Code == "INVALID_DANMAKU" {
		t.Fatal("local output failure became a platform/format error", err)
	}
	kept, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(kept, raw) {
		t.Fatal("failed preparation changed the raw input", err)
	}
}
