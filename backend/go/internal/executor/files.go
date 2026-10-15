package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// WorkFile returns an engine-owned path on the bound output volume. Callers
// serialize operations and never share a work file between producers.
func (e *Engine) WorkFile(operationID, name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", ErrClosed
	}
	if err := validateOperationID(operationID); err != nil {
		return "", err
	}
	if err := validatePathComponent(name); err != nil {
		return "", err
	}
	if filepath.Base(name) != name || name == "content.part" {
		return "", errors.New("invalid work file name")
	}
	dir := filepath.Join(e.outputRoot, ".work", operationID)
	if err := ensureDirectory(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := rejectReparseAncestors(path); err != nil {
		return "", err
	}
	return path, nil
}

// Existing checks a previously published file without re-fetching the platform.
func (e *Engine) Existing(operationID, resourceKey, relativePath string) (Receipt, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Receipt{}, false, ErrClosed
	}
	intent, found, err := e.loadIntent(operationID)
	if err != nil || !found {
		return Receipt{}, false, err
	}
	relativePath, err = normalizeRelativePath(relativePath)
	if err != nil {
		return Receipt{}, false, err
	}
	if intent.Request.ResourceKey != resourceKey || intent.Request.RelativePath != relativePath {
		return Receipt{}, false, ErrConflict
	}
	path, exists, err := e.inspectOutput(relativePath)
	if err != nil || !exists {
		return Receipt{}, false, err
	}
	size, _ := strconv.ParseInt(intent.ExpectedSize, 10, 64)
	ok, err := matchesFile(path, intent.ExpectedSHA256, size)
	if err != nil {
		return Receipt{}, false, err
	}
	if !ok {
		return Receipt{}, false, ErrConflict
	}
	record, found, err := e.loadReceipt(operationID)
	if err != nil {
		return Receipt{}, false, err
	}
	if found {
		if err := e.verifyReceiptFile(record.Receipt); err != nil {
			return Receipt{}, false, err
		}
		return record.Receipt, true, nil
	}
	receipt, err := e.saveSuccessfulReceipt(intent.Request, intent.ExpectedSHA256, size, path)
	return receipt, err == nil, err
}

// ExecuteFile extends the existing intent/publication/receipt transaction to
// streaming media, preserving the same no-replace and crash-recovery rules.
func (e *Engine) ExecuteFile(ctx context.Context, request Request, source string) (Receipt, error) {
	if ctx == nil {
		return Receipt{}, errors.New("context is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Receipt{}, ErrClosed
	}
	if err := rejectReparseAncestors(source); err != nil {
		return Receipt{}, err
	}
	file, err := os.Open(source)
	if err != nil {
		return Receipt{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Receipt{}, err
	}
	if !info.Mode().IsRegular() {
		return Receipt{}, errors.New("media input is not a regular file")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, &contextReader{ctx, file})
	if err != nil {
		return Receipt{}, err
	}
	request.Content = ""
	request.FileSHA256 = hex.EncodeToString(hash.Sum(nil))
	request.FileSize = strconv.FormatInt(size, 10)
	request, digest, size, err := validateRequest(request)
	if err != nil {
		return Receipt{}, err
	}
	return e.executeLocked(ctx, request, digest, size, func(path string) error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, &contextReader{ctx, file})
		return errors.Join(copyErr, out.Sync(), out.Close())
	})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// ValidateRoot checks the real local path and writability, not cloud mount state.
func ValidateRoot(path string) error {
	if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 {
		return errors.New("a local absolute drive path is required")
	}
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("target is not a directory")
	}
	f, err := os.CreateTemp(path, ".alldownloads-write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	return errors.Join(f.Close(), os.Remove(name))
}

func ValidateRelativePath(path string) error { _, err := normalizeRelativePath(path); return err }
