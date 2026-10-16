package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const maxContentBytes = 1 << 20
const maxPersistedJSONBytes = 8 << 20

var (
	ErrConflict = errors.New("operation or destination conflict")
	ErrClosed   = errors.New("executor is closed")
)

type Request struct {
	OperationID  string
	ResourceKey  string
	RelativePath string
	Content      string
	// File inputs carry verified identity instead of embedding media in the journal.
	FileSHA256 string `json:",omitempty"`
	FileSize   string `json:",omitempty"`
}

type Receipt struct {
	OperationID  string
	ResourceKey  string
	RelativePath string
	SHA256       string
	SizeBytes    string
	Status       string
	AckID        string
}

type intentRecord struct {
	Version        int     "json:\"version\""
	Request        Request "json:\"request\""
	ExpectedSHA256 string  "json:\"expectedSha256\""
	ExpectedSize   string  "json:\"expectedSize\""
}

type receiptRecord struct {
	Version int     "json:\"version\""
	Receipt Receipt "json:\"receipt\""
	Acked   bool    "json:\"acked\""
}

type runnerManifest struct {
	Version    int    "json:\"version\""
	OutputRoot string "json:\"outputRoot\""
}

type Engine struct {
	mu sync.Mutex

	stateDir    string
	outputRoot  string
	intentsDir  string
	receiptsDir string
	runLock     io.Closer
	closed      bool

	// afterPublishHook is a package-private fault injection seam.
	beforePublishHook func(finalPath string) error
	afterPublishHook  func(operationID string) error
}

func Open(stateDir, outputRoot string) (*Engine, error) {
	state, err := prepareRoot(stateDir)
	if err != nil {
		return nil, fmt.Errorf("prepare state directory: %w", err)
	}
	output, err := prepareRoot(outputRoot)
	if err != nil {
		return nil, fmt.Errorf("prepare output root: %w", err)
	}
	intents := filepath.Join(state, "intents")
	receipts := filepath.Join(state, "receipts")
	workDir := filepath.Join(output, ".work")
	if err := ensureDirectory(intents); err != nil {
		return nil, fmt.Errorf("prepare intents directory: %w", err)
	}
	if err := ensureDirectory(receipts); err != nil {
		return nil, fmt.Errorf("prepare receipts directory: %w", err)
	}
	if err := ensureDirectory(workDir); err != nil {
		return nil, fmt.Errorf("prepare work directory: %w", err)
	}
	runLock, err := acquireRunLocks(filepath.Join(state, ".executor.lock"), filepath.Join(workDir, ".executor.lock"))
	if err != nil {
		return nil, fmt.Errorf("acquire runner locks: %w", err)
	}
	manifestPath := filepath.Join(state, "runner.manifest.json")
	if err := rejectReparseAncestors(manifestPath); err != nil {
		runLock.Close()
		return nil, err
	}
	var manifest runnerManifest
	err = readJSON(manifestPath, &manifest)
	if errors.Is(err, os.ErrNotExist) {
		manifest = runnerManifest{Version: 1, OutputRoot: output}
		err = atomicWriteJSON(manifestPath, manifest)
	} else if err == nil {
		if manifest.Version != 1 || !strings.EqualFold(filepath.Clean(manifest.OutputRoot), output) {
			err = fmt.Errorf("%w: state directory is already bound to output root %q", ErrConflict, manifest.OutputRoot)
		}
	}
	if err != nil {
		closeErr := runLock.Close()
		return nil, errors.Join(fmt.Errorf("load runner manifest: %w", err), closeErr)
	}
	return &Engine{
		stateDir: state, outputRoot: output,
		intentsDir: intents, receiptsDir: receipts, runLock: runLock,
	}, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.runLock == nil {
		return nil
	}
	err := e.runLock.Close()
	e.runLock = nil
	if err != nil {
		return fmt.Errorf("release runner locks: %w", err)
	}
	return nil
}

func (e *Engine) Execute(ctx context.Context, request Request) (Receipt, error) {
	if ctx == nil {
		return Receipt{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	request, digest, size, err := validateRequest(request)
	if err != nil {
		return Receipt{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Receipt{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if request.FileSHA256 != "" {
		return Receipt{}, errors.New("file input requires ExecuteFile")
	}
	return e.executeLocked(ctx, request, digest, size, func(path string) error { return writeStage(path, []byte(request.Content)) })
}

func (e *Engine) executeLocked(ctx context.Context, request Request, digest string, size int64, write func(string) error) (Receipt, error) {
	intent, found, err := e.loadIntent(request.OperationID)
	if err != nil {
		return Receipt{}, err
	}
	if found {
		if intent.Request != request {
			return Receipt{}, fmt.Errorf("%w: operation ID has different input", ErrConflict)
		}
		record, hasReceipt, err := e.loadReceipt(request.OperationID)
		if err != nil {
			return Receipt{}, err
		}
		if hasReceipt {
			if err := e.verifyReceiptFile(record.Receipt); err != nil {
				return Receipt{}, err
			}
			return record.Receipt, nil
		}
		digest = intent.ExpectedSHA256
		size, err = strconv.ParseInt(intent.ExpectedSize, 10, 64)
		if err != nil || size < 0 {
			return Receipt{}, fmt.Errorf("invalid persisted intent size for %q", request.OperationID)
		}
	} else {
		if _, hasReceipt, err := e.loadReceipt(request.OperationID); err != nil {
			return Receipt{}, err
		} else if hasReceipt {
			return Receipt{}, fmt.Errorf("%w: receipt exists without intent", ErrConflict)
		}
		if err := e.checkPathNotUsedByAnotherIntent(request); err != nil {
			return Receipt{}, err
		}
		_, exists, err := e.inspectOutput(request.RelativePath)
		if err != nil {
			return Receipt{}, err
		}
		if exists {
			return Receipt{}, fmt.Errorf("%w: destination already exists: %s", ErrConflict, request.RelativePath)
		}
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		intent = intentRecord{
			Version: 1, Request: request, ExpectedSHA256: digest,
			ExpectedSize: strconv.FormatInt(size, 10),
		}
		if err := e.saveIntent(intent); err != nil {
			return Receipt{}, err
		}
	}

	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	finalPath, exists, err := e.inspectOutput(request.RelativePath)
	if err != nil {
		return Receipt{}, err
	}
	if exists {
		ok, err := matchesFile(finalPath, digest, size)
		if err != nil {
			return Receipt{}, err
		}
		if !ok {
			return Receipt{}, fmt.Errorf("%w: destination does not match persisted intent: %s", ErrConflict, request.RelativePath)
		}
		return e.saveSuccessfulReceipt(request, digest, size, finalPath)
	}
	stagePath, err := e.prepareStage(request.OperationID)
	if err != nil {
		return Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, errors.Join(err, removeIfExists(stagePath))
	}
	if err := write(stagePath); err != nil {
		return Receipt{}, err
	}
	if ok, err := matchesFile(stagePath, digest, size); err != nil {
		return Receipt{}, err
	} else if !ok {
		return Receipt{}, fmt.Errorf("%w: staged file changed before publication", ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, errors.Join(err, removeIfExists(stagePath))
	}
	if err := e.ensureOutputParents(request.RelativePath); err != nil {
		return Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, errors.Join(err, removeIfExists(stagePath))
	}
	finalPath, exists, err = e.inspectOutput(request.RelativePath)
	if err != nil {
		return Receipt{}, err
	}
	if exists {
		return Receipt{}, fmt.Errorf("%w: destination appeared before publish: %s", ErrConflict, request.RelativePath)
	}
	if e.beforePublishHook != nil {
		if err := e.beforePublishHook(finalPath); err != nil {
			return Receipt{}, err
		}
	}
	if err := publishNewFile(stagePath, finalPath); err != nil {
		return Receipt{}, fmt.Errorf("publish output file without replacement: %w", err)
	}
	if e.afterPublishHook != nil {
		if err := e.afterPublishHook(request.OperationID); err != nil {
			return Receipt{}, fmt.Errorf("injected failure after publish: %w", err)
		}
	}
	ok, err := matchesFile(finalPath, digest, size)
	if err != nil {
		return Receipt{}, err
	}
	if !ok {
		return Receipt{}, fmt.Errorf("published file does not match intent: %s", request.RelativePath)
	}
	return e.saveSuccessfulReceipt(request, digest, size, finalPath)
}

func (e *Engine) saveSuccessfulReceipt(request Request, digest string, size int64, finalPath string) (Receipt, error) {
	ok, err := matchesFile(finalPath, digest, size)
	if err != nil {
		return Receipt{}, err
	}
	if !ok {
		return Receipt{}, fmt.Errorf("%w: output file failed integrity check: %s", ErrConflict, request.RelativePath)
	}
	receipt := Receipt{
		OperationID: request.OperationID, ResourceKey: request.ResourceKey,
		RelativePath: request.RelativePath, SHA256: digest,
		SizeBytes: strconv.FormatInt(size, 10), Status: "SUCCEEDED",
	}
	if err := e.saveReceipt(receiptRecord{Version: 1, Receipt: receipt}); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (e *Engine) Pending() ([]Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrClosed
	}
	entries, err := os.ReadDir(e.intentsDir)
	if err != nil {
		return nil, fmt.Errorf("read intents: %w", err)
	}
	pending := make([]Receipt, 0)
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operationID := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateOperationID(operationID); err != nil {
			return nil, fmt.Errorf("unsafe intent filename %q: %w", entry.Name(), err)
		}
		if entry.Name() != operationID+".json" {
			return nil, fmt.Errorf("invalid intent filename %q", entry.Name())
		}
		intent, found, err := e.loadIntent(operationID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("intent disappeared while reading %q", operationID)
		}
		seen[operationID] = struct{}{}
		record, hasReceipt, err := e.loadReceipt(operationID)
		if err != nil {
			return nil, err
		}
		if hasReceipt {
			if !record.Acked {
				if err := e.verifyReceiptFile(record.Receipt); err != nil {
					return nil, err
				}
				pending = append(pending, record.Receipt)
			}
			continue
		}
		finalPath, exists, err := e.inspectOutput(intent.Request.RelativePath)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		size, err := strconv.ParseInt(intent.ExpectedSize, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid persisted intent size for %q", operationID)
		}
		ok, err := matchesFile(finalPath, intent.ExpectedSHA256, size)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: published output does not match intent %q", ErrConflict, operationID)
		}
		receipt := makeReceipt(intent)
		if err := e.saveReceipt(receiptRecord{Version: 1, Receipt: receipt}); err != nil {
			return nil, err
		}
		pending = append(pending, receipt)
	}

	receiptEntries, err := os.ReadDir(e.receiptsDir)
	if err != nil {
		return nil, fmt.Errorf("read receipts: %w", err)
	}
	for _, entry := range receiptEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operationID := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateOperationID(operationID); err != nil {
			return nil, fmt.Errorf("unsafe receipt filename %q: %w", entry.Name(), err)
		}
		if _, ok := seen[operationID]; !ok {
			return nil, fmt.Errorf("receipt %q has no matching intent", operationID)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].OperationID < pending[j].OperationID
	})
	return pending, nil
}

func makeReceipt(intent intentRecord) Receipt {
	return Receipt{
		OperationID:  intent.Request.OperationID,
		ResourceKey:  intent.Request.ResourceKey,
		RelativePath: intent.Request.RelativePath,
		SHA256:       intent.ExpectedSHA256,
		SizeBytes:    intent.ExpectedSize,
		Status:       "SUCCEEDED",
	}
}

func (e *Engine) Acknowledge(operationID, ackID string) error {
	if err := validateOperationID(operationID); err != nil {
		return err
	}
	if strings.TrimSpace(ackID) == "" || !utf8.ValidString(ackID) || len(ackID) > 256 {
		return errors.New("ack ID must be non-empty valid UTF-8 and at most 256 bytes")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	record, found, err := e.loadReceipt(operationID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("receipt for operation %q does not exist", operationID)
	}
	if record.Receipt.Status != "SUCCEEDED" {
		return fmt.Errorf("operation %q has no successful receipt", operationID)
	}
	if record.Acked {
		if record.Receipt.AckID == ackID {
			return nil
		}
		return fmt.Errorf("%w: operation %q was acknowledged with another ID", ErrConflict, operationID)
	}
	if err := e.verifyReceiptFile(record.Receipt); err != nil {
		return err
	}
	record.Acked = true
	record.Receipt.AckID = ackID
	if err := atomicWriteJSON(e.receiptPath(operationID), record); err != nil {
		return fmt.Errorf("save acknowledgement: %w", err)
	}
	return nil
}

func (e *Engine) verifyReceiptFile(receipt Receipt) error {
	intent, found, err := e.loadIntent(receipt.OperationID)
	if err != nil {
		return err
	}
	if !found || !receiptMatchesIntent(receipt, intent) {
		return fmt.Errorf("receipt for %q does not match its intent", receipt.OperationID)
	}
	size, err := strconv.ParseInt(receipt.SizeBytes, 10, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("receipt for %q has invalid size", receipt.OperationID)
	}
	path, exists, err := e.inspectOutput(receipt.RelativePath)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: completed file is missing: %s", ErrConflict, receipt.RelativePath)
	}
	ok, err := matchesFile(path, receipt.SHA256, size)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: completed file is damaged: %s", ErrConflict, receipt.RelativePath)
	}
	return nil
}

func receiptMatchesIntent(receipt Receipt, intent intentRecord) bool {
	return receipt.OperationID == intent.Request.OperationID &&
		receipt.ResourceKey == intent.Request.ResourceKey &&
		receipt.RelativePath == intent.Request.RelativePath &&
		receipt.SHA256 == intent.ExpectedSHA256 &&
		receipt.SizeBytes == intent.ExpectedSize &&
		receipt.Status == "SUCCEEDED"
}

func validateRequest(request Request) (Request, string, int64, error) {
	if err := validateOperationID(request.OperationID); err != nil {
		return Request{}, "", 0, err
	}
	if request.ResourceKey == "" || len(request.ResourceKey) > 1024 || !utf8.ValidString(request.ResourceKey) {
		return Request{}, "", 0, errors.New("resource key must be non-empty valid UTF-8")
	}
	if !utf8.ValidString(request.Content) {
		return Request{}, "", 0, errors.New("content must be valid UTF-8")
	}
	content := []byte(request.Content)
	if len(content) > maxContentBytes {
		return Request{}, "", 0, fmt.Errorf("content exceeds %d bytes", maxContentBytes)
	}
	relative, err := normalizeRelativePath(request.RelativePath)
	if err != nil {
		return Request{}, "", 0, err
	}
	request.RelativePath = relative
	if request.FileSHA256 != "" || request.FileSize != "" {
		size, sizeErr := strconv.ParseInt(request.FileSize, 10, 64)
		_, hashErr := hex.DecodeString(request.FileSHA256)
		if request.Content != "" || len(request.FileSHA256) != 64 || hashErr != nil || sizeErr != nil || size < 0 {
			return Request{}, "", 0, errors.New("invalid file input identity")
		}
		return request, request.FileSHA256, size, nil
	}
	sum := sha256.Sum256(content)
	return request, hex.EncodeToString(sum[:]), int64(len(content)), nil
}

func validateOperationID(value string) error {
	if len(value) == 0 || len(value) > 128 {
		return errors.New("operation ID must contain 1 to 128 ASCII characters")
	}
	isAlphaNumeric := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	if !isAlphaNumeric(value[0]) || !isAlphaNumeric(value[len(value)-1]) {
		return errors.New("operation ID must start and end with an ASCII letter or digit")
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if !isAlphaNumeric(b) && b != '.' && b != '_' && b != '-' {
			return errors.New("operation ID contains an unsafe character")
		}
	}
	if isWindowsReservedName(value) {
		return errors.New("operation ID is a Windows reserved name")
	}
	return nil
}

func normalizeRelativePath(value string) (string, error) {
	if value == "" || len(value) > 32*1024 || !utf8.ValidString(value) {
		return "", errors.New("relative path must be non-empty valid UTF-8")
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") ||
		filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", errors.New("absolute paths are not allowed")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if err := validatePathComponent(part); err != nil {
			return "", err
		}
	}
	if strings.EqualFold(parts[0], ".work") {
		return "", errors.New("the .work directory is reserved")
	}
	return strings.Join(parts, "/"), nil
}

func validatePathComponent(part string) error {
	if part == "" || part == "." || part == ".." {
		return errors.New("relative path contains an empty or traversal component")
	}
	if strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
		return fmt.Errorf("path component %q ends with a space or dot", part)
	}
	if isWindowsReservedName(part) {
		return fmt.Errorf("path component %q is a Windows reserved name", part)
	}
	for _, r := range part {
		if r < 32 || strings.ContainsRune("<>:\"|?*", r) {
			return fmt.Errorf("path component %q contains a Windows-invalid character", part)
		}
	}
	return nil
}

func isWindowsReservedName(value string) bool {
	base := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 {
		prefix, last := base[:3], base[3]
		if (prefix == "COM" || prefix == "LPT") && last >= '1' && last <= '9' {
			return true
		}
	}
	runes := []rune(base)
	if len(runes) == 4 {
		prefix, last := string(runes[:3]), runes[3]
		if (prefix == "COM" || prefix == "LPT") && (last == '¹' || last == '²' || last == '³') {
			return true
		}
	}
	return false
}

func (e *Engine) checkPathNotUsedByAnotherIntent(request Request) error {
	entries, err := os.ReadDir(e.intentsDir)
	if err != nil {
		return fmt.Errorf("read intents: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operationID := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateOperationID(operationID); err != nil {
			return fmt.Errorf("unsafe intent filename %q: %w", entry.Name(), err)
		}
		other, found, err := e.loadIntent(operationID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("intent disappeared while reading %q", operationID)
		}
		if strings.EqualFold(other.Request.RelativePath, request.RelativePath) {
			return fmt.Errorf("%w: destination is already assigned to operation %q", ErrConflict, other.Request.OperationID)
		}
	}
	return nil
}

func (e *Engine) prepareStage(operationID string) (string, error) {
	workRoot := filepath.Join(e.outputRoot, ".work")
	if err := ensureDirectory(workRoot); err != nil {
		return "", fmt.Errorf("prepare work directory: %w", err)
	}
	opDir := filepath.Join(workRoot, operationID)
	if err := ensureDirectory(opDir); err != nil {
		return "", fmt.Errorf("prepare operation work directory: %w", err)
	}
	stage := filepath.Join(opDir, "content.part")
	if err := rejectReparseAncestors(stage); err != nil {
		return "", err
	}
	info, err := os.Lstat(stage)
	if err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("staging path is not a regular file: %s", stage)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return stage, nil
}

func writeStage(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open staging file: %w", err)
	}
	written, writeErr := file.Write(content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("persist staging file: %w", err)
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (e *Engine) ensureOutputParents(relative string) error {
	parts := strings.Split(relative, "/")
	current := e.outputRoot
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		if err := rejectReparseAncestors(current); err != nil {
			return err
		}
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create output directory %s: %w", current, err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect output directory %s: %w", current, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: output parent is not a directory: %s", ErrConflict, current)
		}
	}
	return nil
}

func (e *Engine) inspectOutput(relative string) (string, bool, error) {
	if _, err := normalizeRelativePath(relative); err != nil {
		return "", false, err
	}
	path := filepath.Join(e.outputRoot, filepath.FromSlash(relative))
	rel, err := filepath.Rel(e.outputRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false, errors.New("resolved output path escapes output root")
	}
	if err := rejectReparseAncestors(path); err != nil {
		return "", false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect output path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%w: output destination is not a regular file: %s", ErrConflict, relative)
	}
	return path, true, nil
}

func prepareRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("directory path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if err := rejectReparseAncestors(absolute); err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return "", err
	}
	if err := rejectReparseAncestors(absolute); err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", absolute)
	}
	return absolute, nil
}

func ensureDirectory(path string) error {
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}
	return nil
}

func rejectReparseAncestors(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	rest := strings.TrimPrefix(absolute, volume)
	separator := string(os.PathSeparator)
	current := volume + separator
	for _, part := range strings.Split(rest, separator) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("inspect path component %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in managed paths: %s", current)
		}
		reparse, err := isReparsePoint(current)
		if err != nil {
			return fmt.Errorf("inspect reparse point %s: %w", current, err)
		}
		if reparse {
			return fmt.Errorf("reparse points are not allowed in managed paths: %s", current)
		}
	}
	return nil
}

func matchesFile(path, expectedHash string, expectedSize int64) (bool, error) {
	if err := rejectReparseAncestors(path); err != nil {
		return false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open output for verification: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return false, nil
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return false, fmt.Errorf("hash output: %w", err)
	}
	return size == expectedSize && hex.EncodeToString(hash.Sum(nil)) == expectedHash, nil
}

func (e *Engine) intentPath(operationID string) string {
	return filepath.Join(e.intentsDir, operationID+".json")
}

func (e *Engine) receiptPath(operationID string) string {
	return filepath.Join(e.receiptsDir, operationID+".json")
}

func (e *Engine) loadIntent(operationID string) (intentRecord, bool, error) {
	if err := validateOperationID(operationID); err != nil {
		return intentRecord{}, false, err
	}
	path := e.intentPath(operationID)
	if err := rejectReparseAncestors(path); err != nil {
		return intentRecord{}, false, err
	}
	var intent intentRecord
	err := readJSON(path, &intent)
	if errors.Is(err, os.ErrNotExist) {
		return intentRecord{}, false, nil
	}
	if err != nil {
		return intentRecord{}, false, fmt.Errorf("read intent for %q: %w", operationID, err)
	}
	if intent.Version != 1 || intent.Request.OperationID != operationID {
		return intentRecord{}, false, fmt.Errorf("invalid persisted intent for %q", operationID)
	}
	normalized, digest, size, err := validateRequest(intent.Request)
	if err != nil || normalized != intent.Request ||
		digest != intent.ExpectedSHA256 || strconv.FormatInt(size, 10) != intent.ExpectedSize {
		return intentRecord{}, false, fmt.Errorf("persisted intent %q failed integrity validation", operationID)
	}
	return intent, true, nil
}

func (e *Engine) loadReceipt(operationID string) (receiptRecord, bool, error) {
	if err := validateOperationID(operationID); err != nil {
		return receiptRecord{}, false, err
	}
	path := e.receiptPath(operationID)
	if err := rejectReparseAncestors(path); err != nil {
		return receiptRecord{}, false, err
	}
	var record receiptRecord
	err := readJSON(path, &record)
	if errors.Is(err, os.ErrNotExist) {
		return receiptRecord{}, false, nil
	}
	if err != nil {
		return receiptRecord{}, false, fmt.Errorf("read receipt for %q: %w", operationID, err)
	}
	receipt := record.Receipt
	size, sizeErr := strconv.ParseInt(receipt.SizeBytes, 10, 64)
	if record.Version != 1 || receipt.OperationID != operationID || receipt.Status != "SUCCEEDED" ||
		sizeErr != nil || size < 0 || len(receipt.SHA256) != 64 {
		return receiptRecord{}, false, fmt.Errorf("invalid persisted receipt for %q", operationID)
	}
	if record.Acked == (receipt.AckID == "") {
		return receiptRecord{}, false, fmt.Errorf("invalid acknowledgement state for %q", operationID)
	}
	if _, err := hex.DecodeString(receipt.SHA256); err != nil {
		return receiptRecord{}, false, fmt.Errorf("invalid receipt hash for %q", operationID)
	}
	if _, err := normalizeRelativePath(receipt.RelativePath); err != nil {
		return receiptRecord{}, false, fmt.Errorf("invalid receipt path for %q: %w", operationID, err)
	}
	return record, true, nil
}

func (e *Engine) saveIntent(intent intentRecord) error {
	if err := atomicWriteJSON(e.intentPath(intent.Request.OperationID), intent); err != nil {
		return fmt.Errorf("persist operation intent: %w", err)
	}
	return nil
}

func (e *Engine) saveReceipt(record receiptRecord) error {
	if err := atomicWriteJSON(e.receiptPath(record.Receipt.OperationID), record); err != nil {
		return fmt.Errorf("persist operation receipt: %w", err)
	}
	return nil
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxPersistedJSONBytes {
		return fmt.Errorf("persisted JSON exceeds %d bytes", maxPersistedJSONBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxPersistedJSONBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxPersistedJSONBytes {
		return fmt.Errorf("persisted JSON exceeds %d bytes", maxPersistedJSONBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func atomicWriteJSON(path string, value any) (result error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer func() {
		if temp != "" {
			cleanupErr := os.Remove(temp)
			if !errors.Is(cleanupErr, os.ErrNotExist) {
				result = errors.Join(result, cleanupErr)
			}
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(encoded); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := replaceFile(temp, path); err != nil {
		return err
	}
	temp = ""
	return nil
}
