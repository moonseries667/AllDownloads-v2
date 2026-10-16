package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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
	return RunControlled(ctx, executable, t.Timeout, args...)
}

// RunControlled runs a trusted project tool without a shell. Each invocation
// owns its Windows Job Object; cancelling it cannot stop a sibling invocation.
func RunControlled(ctx context.Context, executable string, timeout time.Duration, args ...string) (output []byte, resultErr error) {
	output, _, resultErr = RunControlledDiagnostics(ctx, executable, timeout, args...)
	return
}

// RunControlledDiagnostics keeps bounded tool diagnostics inside the adapter.
// Callers must translate them to fixed error codes and never log raw output.
func RunControlledDiagnostics(ctx context.Context, executable string, timeout time.Duration, args ...string) (output, diagnostic []byte, resultErr error) {
	return RunControlledEnvironment(ctx, executable, timeout, nil, args...)
}

// RunControlledEnvironment scopes tool-specific directories/settings to this
// child process. It never changes the runner's or operating system's profile.
func RunControlledEnvironment(ctx context.Context, executable string, timeout time.Duration, environment map[string]string, args ...string) (output, diagnostic []byte, resultErr error) {
	var childEnvironment []string
	if len(environment) > 0 {
		for _, item := range os.Environ() {
			key, _, _ := strings.Cut(item, "=")
			replace := false
			for name := range environment {
				if strings.EqualFold(key, name) {
					replace = true
					break
				}
			}
			if !replace {
				childEnvironment = append(childEnvironment, item)
			}
		}
		for key, value := range environment {
			childEnvironment = append(childEnvironment, key+"="+value)
		}
	}
	return runControlledEnvironment(ctx, executable, timeout, childEnvironment, args...)
}

// RunControlledExactEnvironment uses only the supplied child environment;
// nil/empty does not inherit the runner's RCLONE_* or other settings. Windows'
// required SYSTEMROOT may be supplied by the Go runtime. Output and diagnostics
// retain the standard 1 MiB/16 KiB bounds. Callers must keep these raw bytes in
// their authentication adapter and never log or echo them in public errors.
func RunControlledExactEnvironment(ctx context.Context, executable string, timeout time.Duration, environment []string, args ...string) (output, diagnostic []byte, resultErr error) {
	childEnvironment := make([]string, len(environment))
	copy(childEnvironment, environment)
	return runControlledEnvironment(ctx, executable, timeout, childEnvironment, args...)
}

// environment follows exec.Cmd.Env semantics: nil inherits the parent; a
// non-nil slice is complete. Public exact calls always pass a non-nil copy.
func runControlledEnvironment(ctx context.Context, executable string, timeout time.Duration, environment []string, args ...string) (output, diagnostic []byte, resultErr error) {
	if !filepath.IsAbs(executable) {
		return nil, nil, Fail("TOOL_NOT_CONFIGURED", "TOOL", false)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = environment
	prepareProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	out := &boundedBuffer{max: 1 << 20}
	errout := &boundedBuffer{max: 16 << 10}
	cmd.Stdout = out
	cmd.Stderr = errout
	if err := cmd.Start(); err != nil {
		return nil, nil, Fail("TOOL_START_FAILED", "TOOL", false)
	}
	closer, err := bindProcessTree(cmd.Process)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, Fail("TOOL_PROCESS_CONTROL_FAILED", "TOOL", false)
	}
	defer func() { resultErr = errors.Join(resultErr, closer.Close()) }()
	if err := resumeProcess(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, Fail("TOOL_PROCESS_CONTROL_FAILED", "TOOL", false)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return out.data, errout.data, ctx.Err()
		}
		return out.data, errout.data, Fail("TOOL_FAILED", "TOOL", false)
	}
	return out.data, errout.data, nil
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
