package media

import (
	"context"
	"fmt"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupplementOnlyAndReplay(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/sub":
			fmt.Fprint(w, `{"body":[{"from":0,"to":1,"content":"人工字幕"}]}`)
		case "/dm":
			fmt.Fprint(w, `<i><d p="0">弹幕</d></i>`)
		default:
			t.Error("unselected media requested")
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	state, root := t.TempDir(), t.TempDir()
	engine, err := executor.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{OperationID: "order", ResourceKey: "revision1", Outputs: []Output{
		{ID: "sub", RelativePath: "Bilibili/来源(收藏夹)/标题(BV)/zh.json", Mode: "SEPARATE", Inputs: []Input{{TrackID: "s", Kind: "SUBTITLE", Source: Source{URL: server.URL + "/sub", Version: "s1"}}}},
		{ID: "dm", RelativePath: "Bilibili/来源(收藏夹)/标题(BV)/danmaku.xml", Mode: "SEPARATE", Inputs: []Input{{TrackID: "d", Kind: "DANMAKU", Source: Source{URL: server.URL + "/dm", Version: "d1"}}}},
	}}
	worker := Executor{Files: engine, Downloads: Downloader{server.Client()}}
	facts, err := worker.Execute(context.Background(), plan)
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts=%v error=%v", facts, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = executor.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	worker.Files = engine
	if facts, err := worker.Execute(context.Background(), plan); err != nil || len(facts) != 2 {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("receipt replay re-downloaded media")
	}
	plan.Outputs[0].Inputs[0].Source.Version = "s2"
	if _, err := worker.Execute(context.Background(), plan); err == nil {
		t.Fatal("changed selection reused receipt")
	}
}
func TestFrozenPlanRejectsEmptyAndInvalidMergeBeforeIO(t *testing.T) {
	for _, plan := range []Plan{{}, {OperationID: "x", ResourceKey: "v", Outputs: []Output{{ID: "o", RelativePath: "ok.mp4", Mode: "MERGE_AV", Inputs: []Input{{TrackID: "v", Kind: "VIDEO", Source: Source{Version: "v"}}}}}}, {OperationID: "x", ResourceKey: "v", Outputs: []Output{{ID: "o", RelativePath: "../escape", Mode: "SEPARATE"}}}} {
		if err := validatePlan(plan); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
}
func TestRealFFmpegMergeAndSingleTracks(t *testing.T) {
	dir := os.Getenv("ALLDOWNLOADS_TEST_FFMPEG_DIR")
	if dir == "" {
		t.Skip("set ALLDOWNLOADS_TEST_FFMPEG_DIR to pinned FFmpeg 9.0.2 bin for real media verification")
	}
	tools := &Tools{FFmpeg: filepath.Join(dir, "ffmpeg.exe"), FFprobe: filepath.Join(dir, "ffprobe.exe"), Timeout: 30 * time.Second}
	ctx := context.Background()
	if err := tools.Check(ctx); err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	video := filepath.Join(fixture, "video.mp4")
	audio := filepath.Join(fixture, "audio.m4a")
	if _, err := tools.run(ctx, tools.FFmpeg, "-nostdin", "-y", "-f", "lavfi", "-i", "color=c=blue:s=32x32:r=5", "-t", "0.6", "-an", "-c:v", "mpeg4", video); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.run(ctx, tools.FFmpeg, "-nostdin", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=8000", "-t", "0.6", "-vn", "-c:a", "aac", audio); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(fixture)))
	defer server.Close()
	for _, mode := range []string{"both", "video", "audio"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			engine, err := executor.Open(t.TempDir(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			v := Input{TrackID: "video", Kind: "VIDEO", Source: Source{URL: server.URL + "/video.mp4", Version: "v1"}}
			a := Input{TrackID: "audio", Kind: "AUDIO", Source: Source{URL: server.URL + "/audio.m4a", Version: "a1"}}
			out := Output{ID: "file", RelativePath: "output.mp4", Mode: "MERGE_AV", Inputs: []Input{v, a}}
			expected := []string{"video", "audio"}
			if mode == "video" {
				out.Mode = "SEPARATE"
				out.Inputs = []Input{v}
				expected = []string{"video"}
			}
			if mode == "audio" {
				out.RelativePath = "output.m4a"
				out.Mode = "SEPARATE"
				out.Inputs = []Input{a}
				expected = []string{"audio"}
			}
			facts, err := (&Executor{engine, Downloader{server.Client()}, tools}).Execute(ctx, Plan{OperationID: "op", ResourceKey: "v", Outputs: []Output{out}})
			if err != nil || len(facts) != 1 {
				t.Fatal(err)
			}
			probe, err := tools.Inspect(ctx, filepath.Join(root, out.RelativePath), expected)
			if err != nil || probe.Duration == nil || *probe.Duration <= 0 {
				t.Fatalf("probe=%v err=%v", probe, err)
			}
		})
	}
	if _, err := tools.Inspect(ctx, video, []string{"video", "audio"}); Classify(err, "").Code != "MEDIA_STREAM_MISSING" {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tools.run(cancelled, tools.FFmpeg, "-version"); err == nil {
		t.Fatal("cancelled tool ran")
	}
}
