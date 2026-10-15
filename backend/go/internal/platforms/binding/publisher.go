// Package binding connects part-two adapters to the already committed shared
// executor and runner interfaces. It does not own a second runner loop or log.
package binding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type Publisher struct {
	Files                    *executor.Engine
	OperationID, ResourceKey string
	Tools                    *media.Tools
}

func hash(raw string) string { b := sha256.Sum256([]byte(raw)); return hex.EncodeToString(b[:]) }
func (s *Publisher) identity(key string, out p.Output) (string, string) {
	raw, _ := json.Marshal(out)
	return media.OutputOperationID(s.OperationID, out.ID), hash(s.ResourceKey + "\x00" + key + "\x00" + string(raw))
}
func evidence(out p.Output, r executor.Receipt) p.FileEvidence {
	return p.FileEvidence{OutputID: out.ID, RelativePath: r.RelativePath, SizeBytes: r.SizeBytes, SHA256: r.SHA256, MediaKinds: []string{out.Kind}, Verified: true}
}
func fileFault(err error) *p.Fault {
	if errors.Is(err, media.ErrProcessStopUnconfirmed) {
		return p.NewFault("RESULT_UNKNOWN", "CONTROL")
	}
	if errors.Is(err, executor.ErrConflict) {
		return p.NewFault("VERIFICATION_FAILED", "PUBLISH")
	}
	if errors.Is(err, os.ErrPermission) {
		return p.NewFault("PERMISSION_DENIED", "PUBLISH")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return p.SafeFault(err, "PUBLISH")
	}
	var failure *media.Failure
	if errors.As(err, &failure) {
		return p.NewFault("VERIFICATION_FAILED", "VERIFY")
	}
	return p.SafeFault(err, "PUBLISH")
}

// Existing delegates both crash-between-publish-and-receipt recovery and actual
// byte validation to the shared engine. No platform request is made on reuse.
func (s *Publisher) Existing(key string, out p.Output) (p.FileEvidence, bool, error) {
	if s.Files == nil || s.OperationID == "" || s.ResourceKey == "" {
		return p.FileEvidence{}, false, p.NewFault("INVALID_INPUT", "PUBLISH")
	}
	id, resource := s.identity(key, out)
	r, found, err := s.Files.Existing(id, resource, out.RelativePath)
	if err != nil {
		return p.FileEvidence{}, false, fileFault(err)
	}
	if !found {
		return p.FileEvidence{}, false, nil
	}
	fact := evidence(out, r)
	if err := p.CheckEvidence(out, fact); err != nil {
		return p.FileEvidence{}, false, err
	}
	return fact, true, nil
}

type cancelReader struct {
	ctx    context.Context
	source io.Reader
}

func (r cancelReader) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buf)
}

func (s *Publisher) Ensure(ctx context.Context, key string, out p.Output, open p.OpenSource) (p.FileEvidence, error) {
	if err := p.ValidateOutputs([]p.Output{out}); err != nil {
		return p.FileEvidence{}, err
	}
	if err := executor.ValidateRelativePath(out.RelativePath); err != nil {
		return p.FileEvidence{}, p.NewFault("INVALID_INPUT", "PLAN")
	}
	if err := ctx.Err(); err != nil {
		return p.FileEvidence{}, p.SafeFault(err, "DOWNLOAD")
	}
	if existing, found, err := s.Existing(key, out); err != nil || found {
		return existing, err
	}
	if open == nil {
		return p.FileEvidence{}, p.NewFault("INVALID_INPUT", "DOWNLOAD")
	}
	id, resource := s.identity(key, out)
	work, err := s.Files.WorkFile(id, "platform-input.part")
	if err != nil {
		return p.FileEvidence{}, fileFault(err)
	}
	source, err := open(ctx)
	if err != nil {
		return p.FileEvidence{}, p.SafeFault(err, "DOWNLOAD")
	}
	if source == nil {
		return p.FileEvidence{}, p.NewFault("PROVIDER_CHANGED", "DOWNLOAD")
	}
	outFile, err := os.OpenFile(work, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		_ = source.Close()
		return p.FileEvidence{}, fileFault(err)
	}
	// Closing the source unblocks a cancelled network read. Providers also
	// receive the context; the watcher is joined before the method returns.
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			_ = source.Close()
		case <-done:
		}
	}()
	size, copyErr := io.Copy(outFile, cancelReader{ctx, source})
	close(done)
	<-joined
	err = errors.Join(copyErr, source.Close(), outFile.Sync(), outFile.Close(), ctx.Err())
	if err != nil {
		return p.FileEvidence{}, p.SafeFault(err, "DOWNLOAD")
	}
	if out.SizeBytes != nil && strconv.FormatInt(size, 10) != *out.SizeBytes {
		return p.FileEvidence{}, p.NewFault("VERIFICATION_FAILED", "VERIFY")
	}
	var duration *float64
	switch out.Kind {
	case "VIDEO", "AUDIO", "PHOTO":
		if s.Tools == nil {
			return p.FileEvidence{}, p.NewFault("PROVIDER_UNAVAILABLE", "VERIFY")
		}
		expected := []string{"video"}
		if out.Kind == "AUDIO" {
			expected = []string{"audio"}
		}
		probe, err := s.Tools.InspectContaining(ctx, work, expected)
		if err != nil {
			return p.FileEvidence{}, fileFault(err)
		}
		duration = probe.Duration
	case "METADATA":
		data, err := os.ReadFile(work)
		if err != nil || !json.Valid(data) {
			return p.FileEvidence{}, p.NewFault("VERIFICATION_FAILED", "VERIFY")
		}
	case "FILE", "DOCUMENT":
	default:
		return p.FileEvidence{}, p.NewFault("INVALID_INPUT", "PLAN")
	}
	r, err := s.Files.ExecuteFile(ctx, executor.Request{OperationID: id, ResourceKey: resource, RelativePath: out.RelativePath}, work)
	if err != nil {
		return p.FileEvidence{}, fileFault(err)
	}
	fact := evidence(out, r)
	fact.DurationSeconds = duration
	return fact, p.CheckEvidence(out, fact)
}
