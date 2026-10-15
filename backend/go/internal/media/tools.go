package media

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const FFmpegVersion = "9.0.2"

type Tools struct {
	FFmpeg, FFprobe string
	Timeout         time.Duration
	mu              sync.Mutex
	checked         bool
}
type Probe struct {
	Duration *float64
	Streams  []string
}
type boundedBuffer struct {
	data []byte
	max  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data) < b.max {
		keep := b.max - len(b.data)
		if keep > n {
			keep = n
		}
		b.data = append(b.data, p[:keep]...)
	}
	return n, nil
}

func (t *Tools) run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	if !filepath.IsAbs(executable) {
		return nil, Fail("TOOL_NOT_CONFIGURED", "TOOL", false)
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	prepareProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	out := &boundedBuffer{max: 1 << 20}
	errout := &boundedBuffer{max: 16 << 10}
	cmd.Stdout = out
	cmd.Stderr = errout
	if err := cmd.Start(); err != nil {
		return nil, Fail("TOOL_START_FAILED", "TOOL", false)
	}
	closer, err := bindProcessTree(cmd.Process)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, Fail("TOOL_PROCESS_CONTROL_FAILED", "TOOL", false)
	}
	defer closer.Close()
	if err := resumeProcess(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, Fail("TOOL_PROCESS_CONTROL_FAILED", "TOOL", false)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, Fail("TOOL_FAILED", "TOOL", false)
	}
	return out.data, nil
}
func (t *Tools) Check(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.checked {
		return nil
	}
	for _, tool := range []struct{ path, name string }{{t.FFmpeg, "ffmpeg"}, {t.FFprobe, "ffprobe"}} {
		raw, err := t.run(ctx, tool.path, "-version")
		if err != nil {
			return err
		}
		first := strings.SplitN(string(raw), "\n", 2)[0]
		if !strings.HasPrefix(first, tool.name+" version "+FFmpegVersion+" ") && !strings.HasPrefix(first, tool.name+" version "+FFmpegVersion+"-") {
			return Fail("TOOL_VERSION_MISMATCH", "TOOL", false)
		}
	}
	t.checked = true
	return nil
}
func (t *Tools) Inspect(ctx context.Context, path string, expected []string) (Probe, error) {
	return t.inspect(ctx, path, expected, true)
}

// InspectContaining validates an unchanged platform attachment. Unlike selected
// Bilibili outputs it may legitimately contain additional audio/subtitle streams.
func (t *Tools) InspectContaining(ctx context.Context, path string, expected []string) (Probe, error) {
	return t.inspect(ctx, path, expected, false)
}

func (t *Tools) inspect(ctx context.Context, path string, expected []string, exact bool) (Probe, error) {
	if err := t.Check(ctx); err != nil {
		return Probe{}, err
	}
	raw, err := t.run(ctx, t.FFprobe, "-v", "error", "-show_entries", "stream=codec_type,duration:format=duration", "-of", "json", path)
	if err != nil {
		return Probe{}, err
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		}
		Format struct{ Duration string }
	}
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&result) != nil {
		return Probe{}, Fail("INVALID_MEDIA", "VERIFY", false)
	}
	probe := Probe{}
	for _, stream := range result.Streams {
		probe.Streams = append(probe.Streams, stream.CodecType)
	}
	if duration, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil && duration >= 0 {
		probe.Duration = &duration
	}
	for _, kind := range expected {
		found := false
		for _, actual := range probe.Streams {
			if actual == kind {
				found = true
			}
		}
		if !found {
			return Probe{}, Fail("MEDIA_STREAM_MISSING", "VERIFY", false)
		}
	}
	if exact && len(probe.Streams) != len(expected) {
		return Probe{}, Fail("UNEXPECTED_MEDIA_STREAM", "VERIFY", false)
	}
	return probe, nil
}
func (t *Tools) Mux(ctx context.Context, video, audio, out string) error {
	if err := t.Check(ctx); err != nil {
		return err
	}
	format := "mp4"
	if strings.EqualFold(filepath.Ext(out), ".mkv") {
		format = "matroska"
	}
	_, err := t.run(ctx, t.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c", "copy", "-f", format, out)
	return err
}
