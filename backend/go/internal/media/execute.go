package media

import (
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

type Executor struct {
	Files     *executor.Engine
	Downloads Downloader
	Tools     *Tools
}

func (e *Executor) Execute(ctx context.Context, plan Plan) ([]FileFact, error) {
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	facts := make([]FileFact, 0, len(plan.Outputs))
	for _, output := range plan.Outputs {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		id := fileOperationID(plan.OperationID, output.ID)
		identity := fileResourceKey(plan.ResourceKey, output)
		receipt, found, err := e.Files.Existing(id, identity, output.RelativePath)
		if err != nil {
			return facts, err
		}
		if found {
			facts = append(facts, FileFact{ID: output.ID, RelativePath: receipt.RelativePath, SizeBytes: receipt.SizeBytes, SHA256: receipt.SHA256})
			continue
		}
		inputs := make([]string, 0, len(output.Inputs))
		expected := []string{}
		for i, input := range output.Inputs {
			path, err := e.Files.WorkFile(id, "input-"+string(rune('0'+i))+".part")
			if err != nil {
				return facts, err
			}
			if err := e.Downloads.Fetch(ctx, input.Source, path); err != nil {
				return facts, err
			}
			inputs = append(inputs, path)
			if input.Kind == "VIDEO" || input.Kind == "AUDIO" {
				expected = append(expected, strings.ToLower(input.Kind))
			}
		}
		final := inputs[0]
		if output.Mode == "MERGE_AV" {
			if e.Tools == nil {
				return facts, Fail("TOOL_NOT_CONFIGURED", "MERGE", false)
			}
			final, err = e.Files.WorkFile(id, "merged"+filepath.Ext(output.RelativePath))
			if err != nil {
				return facts, err
			}
			video, audio := inputs[0], inputs[1]
			if output.Inputs[0].Kind == "AUDIO" {
				video, audio = audio, video
			}
			if err := e.Tools.Mux(ctx, video, audio, final); err != nil {
				return facts, err
			}
		}
		var probe Probe
		if len(expected) > 0 {
			if e.Tools == nil {
				return facts, Fail("TOOL_NOT_CONFIGURED", "VERIFY", false)
			}
			probe, err = e.Tools.Inspect(ctx, final, expected)
			if err != nil {
				return facts, err
			}
		} else if output.Inputs[0].Kind == "DANMAKU" {
			decoded, err := e.Files.WorkFile(id, "danmaku.xml")
			if err != nil {
				return facts, err
			}
			final, err = prepareDanmaku(ctx, final, decoded)
			if err != nil {
				return facts, err
			}
		} else if err := verifySupplement(final, output.Inputs[0].Kind); err != nil {
			return facts, err
		}
		receipt, err = e.Files.ExecuteFile(ctx, executor.Request{OperationID: id, ResourceKey: identity, RelativePath: output.RelativePath}, final)
		if err != nil {
			return facts, err
		}
		facts = append(facts, FileFact{ID: output.ID, RelativePath: receipt.RelativePath, SizeBytes: receipt.SizeBytes, SHA256: receipt.SHA256, Duration: probe.Duration, Streams: probe.Streams})
	}
	return facts, nil
}
func validatePlan(plan Plan) error {
	if plan.OperationID == "" || plan.ResourceKey == "" || len(plan.Outputs) == 0 {
		return Fail("EMPTY_SELECTION", "PLAN", false)
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, out := range plan.Outputs {
		if out.ID == "" || ids[out.ID] {
			return Fail("INVALID_FILE_PLAN", "PLAN", false)
		}
		ids[out.ID] = true
		if err := executor.ValidateRelativePath(out.RelativePath); err != nil {
			return err
		}
		key := strings.ToLower(strings.ReplaceAll(out.RelativePath, "\\", "/"))
		if seen[key] {
			return Fail("PATH_COLLISION", "PLAN", false)
		}
		seen[key] = true
		if out.Mode == "MERGE_AV" {
			if len(out.Inputs) != 2 || !((out.Inputs[0].Kind == "VIDEO" && out.Inputs[1].Kind == "AUDIO") || (out.Inputs[0].Kind == "AUDIO" && out.Inputs[1].Kind == "VIDEO")) {
				return Fail("INVALID_MERGE_SELECTION", "PLAN", false)
			}
		} else if out.Mode != "SEPARATE" || len(out.Inputs) != 1 {
			return Fail("INVALID_FILE_PLAN", "PLAN", false)
		}
		for _, input := range out.Inputs {
			if input.TrackID == "" || input.Source.Version == "" {
				return Fail("RESOURCE_VERSION_MISSING", "PLAN", false)
			}
			switch input.Kind {
			case "VIDEO", "AUDIO", "SUBTITLE", "DANMAKU":
			default:
				return Fail("UNSUPPORTED_TRACK", "PLAN", false)
			}
		}
	}
	return nil
}
func fileOperationID(op, id string) string {
	sum := sha256.Sum256([]byte(op + "\x00" + id))
	return "media-" + hex.EncodeToString(sum[:])
}

// OutputOperationID is the stable private receipt key for one frozen output.
func OutputOperationID(op, id string) string { return fileOperationID(op, id) }
func fileResourceKey(resource string, out Output) string {
	// Expiring URLs and headers are not identity. The frozen track versions are.
	identity := struct {
		Resource, Mode, Path string
		Tracks               []string
	}{Resource: resource, Mode: out.Mode, Path: out.RelativePath}
	for _, input := range out.Inputs {
		identity.Tracks = append(identity.Tracks, input.TrackID+"@"+input.Source.Version)
	}
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func verifySupplement(path, kind string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	switch kind {
	case "SUBTITLE":
		var body struct {
			Body []struct {
				From, To float64
				Content  string
			}
		}
		decoder := json.NewDecoder(io.LimitReader(f, 32<<20))
		if decoder.Decode(&body) != nil || body.Body == nil {
			return Fail("INVALID_SUBTITLE", "VERIFY", false)
		}
		for _, line := range body.Body {
			if line.From < 0 || line.To < line.From {
				return Fail("INVALID_SUBTITLE", "VERIFY", false)
			}
		}
	case "DANMAKU":
		return verifyDanmaku(context.Background(), f)
	default:
		return Fail("UNSUPPORTED_TRACK", "VERIFY", false)
	}
	return nil
}

const maxDanmakuBytes = 64 << 20

// Keep the transport cache intact: a Complete raw-DEFLATE input can be reused
// after restart, while the independently decoded XML is verified and published.
func prepareDanmaku(ctx context.Context, inputPath, decodedPath string) (string, error) {
	input, err := os.Open(inputPath)
	if err != nil {
		return "", err
	}
	defer input.Close()
	if err := verifyDanmaku(ctx, input); err == nil {
		return inputPath, nil
	} else {
		var failure *Failure
		if !errors.As(err, &failure) || failure.Code != "INVALID_DANMAKU" {
			return "", err
		}
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	inflated := flate.NewReader(input)
	defer inflated.Close()
	decoded, err := os.OpenFile(decodedPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	bytes, decodeErr := io.Copy(decoded, io.LimitReader(danmakuContextReader{ctx, inflated}, maxDanmakuBytes+1))
	persistErr := errors.Join(decoded.Sync(), decoded.Close())
	if persistErr != nil {
		return "", persistErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if decodeErr != nil {
		var localError *os.PathError
		if errors.As(decodeErr, &localError) {
			return "", decodeErr
		}
		return "", Fail("INVALID_DANMAKU", "VERIFY", false)
	}
	if bytes > maxDanmakuBytes {
		return "", Fail("INVALID_DANMAKU", "VERIFY", false)
	}
	file, err := os.Open(decodedPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := verifyDanmaku(ctx, file); err != nil {
		return "", err
	}
	return decodedPath, nil
}

type danmakuContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r danmakuContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func verifyDanmaku(ctx context.Context, input io.Reader) error {
	limited := &io.LimitedReader{R: danmakuContextReader{ctx, input}, N: maxDanmakuBytes + 1}
	decoder := xml.NewDecoder(limited)
	root := false
	for {
		token, err := decoder.Token()
		if err := ctx.Err(); err != nil {
			return err
		}
		if limited.N == 0 {
			return Fail("INVALID_DANMAKU", "VERIFY", false)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var localError *os.PathError
			if errors.As(err, &localError) {
				return err
			}
			return Fail("INVALID_DANMAKU", "VERIFY", false)
		}
		if start, ok := token.(xml.StartElement); ok && !root {
			if start.Name.Local != "i" {
				return Fail("INVALID_DANMAKU", "VERIFY", false)
			}
			root = true
		}
	}
	if !root {
		return Fail("INVALID_DANMAKU", "VERIFY", false)
	}
	return nil
}
