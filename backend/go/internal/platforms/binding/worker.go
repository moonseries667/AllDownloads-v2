package binding

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

// Worker composes with the shared first-part handler. Provider implementations
// are injected only after session/operation support has actually been set up.
type Worker struct {
	Base      *runner.Handler
	Saved     telegram.SavedClient
	Telegram  telegram.DownloadClient
	Tree      quark.Client
	Quark     quark.DownloadClient
	Transfers *quark.Transfers
}

var _ runner.Worker = (*Worker)(nil)

func (w *Worker) SupportsOperation(op runner.Operation) bool {
	switch op.Platform {
	case "TELEGRAM":
		return op.OperationType == "SCAN_TEL_SAVED" && w.Saved != nil || op.OperationType == "DOWNLOAD_MEDIA" && w.Telegram != nil
	case "QUARK":
		return op.OperationType == "LIST_QUARK_TREE" && w.Tree != nil || op.OperationType == "DOWNLOAD_MEDIA" && w.Quark != nil || (op.OperationType == "TRANSFER_QUARK_ITEMS" || op.OperationType == "RECONCILE_QUARK_TRANSFER") && w.Transfers != nil
	default:
		return op.Platform == "BILIBILI" && w.Base != nil
	}
}

func (w *Worker) SupportsPlatform(platform string) bool {
	switch platform {
	case "BILIBILI":
		return w.Base != nil
	case "TELEGRAM":
		return w.Saved != nil || w.Telegram != nil
	case "QUARK":
		return w.Tree != nil || w.Quark != nil
	default:
		return false
	}
}

type Failure struct{ Fault *p.Fault }

func (f *Failure) Error() string { return f.Fault.Error() }
func (f *Failure) Unwrap() error { return f.Fault }
func (f *Failure) RunnerFailure() (string, *runner.ResultError) {
	return f.Fault.Status, &runner.ResultError{Code: f.Fault.Code, Message: f.Fault.Message, RetryAfterSeconds: f.Fault.RetryAfterSeconds, CompletedEffects: f.Fault.CompletedEffects, PendingEffects: f.Fault.PendingEffects}
}

func (w *Worker) Capabilities() []string {
	set := map[string]bool{}
	if w.Base != nil {
		for _, c := range w.Base.Capabilities() {
			set[c] = true
		}
	}
	if w.Saved != nil {
		set["SCAN_TEL_SAVED"] = true
	}
	if w.Tree != nil {
		set["LIST_QUARK_TREE"] = true
	}
	if w.Transfers != nil {
		set["TRANSFER_QUARK_ITEMS"] = true
		set["RECONCILE_QUARK_TRANSFER"] = true
	}
	if w.Base != nil && (w.Quark != nil || (w.Telegram != nil && w.Base.Tools != nil)) {
		set["DOWNLOAD_MEDIA"] = true
	}
	result := []string{}
	for c := range set {
		result = append(result, c)
	}
	sort.Strings(result)
	return result
}

func wire(files []p.FileEvidence) []runner.File {
	result := make([]runner.File, 0, len(files))
	for _, f := range files {
		result = append(result, runner.File{OutputID: f.OutputID, RelativePath: f.RelativePath, SizeBytes: f.SizeBytes, SHA256: f.SHA256, MediaKinds: f.MediaKinds, Duration: f.DurationSeconds, Verified: f.Verified})
	}
	return result
}

func (w *Worker) open(ctx context.Context, op runner.Operation, outputs []p.Output) (*Publisher, error) {
	if w.Base == nil || op.Destination == nil || op.OperationID == "" || op.ResourceKey == "" {
		return nil, p.NewFault("INVALID_INPUT", "PLAN")
	}
	if err := p.ValidateOutputs(outputs); err != nil {
		return nil, err
	}
	if err := executor.ValidateRelativePath(op.Destination.RelativePath); err != nil {
		return nil, p.NewFault("INVALID_INPUT", "PLAN")
	}
	base := strings.ToLower(strings.TrimSuffix(op.Destination.RelativePath, "/")) + "/"
	for _, out := range outputs {
		if !strings.HasPrefix(strings.ToLower(out.RelativePath), base) {
			return nil, p.NewFault("INVALID_INPUT", "PLAN")
		}
	}
	// Reuse the shared root authorization and rootId derivation exactly; never
	// implement a second interpretation of the batch's local target.
	payload, _ := json.Marshal(map[string]any{"schemaVersion": 1, "targetId": op.Destination.RootID, "path": op.Destination.RootPath})
	value, _, err := w.Base.Handle(ctx, runner.Operation{OperationType: "VALIDATE_TARGET", Payload: payload})
	if err != nil {
		return nil, p.NewFault("INVALID_STORAGE_ROOT", "TARGET")
	}
	validated, ok := value.(map[string]any)
	if !ok || validated["rootId"] != op.Destination.RootID {
		return nil, p.NewFault("VERSION_CHANGED", "TARGET")
	}
	root, ok := validated["canonicalPath"].(string)
	if !ok {
		return nil, p.NewFault("INVALID_INPUT", "TARGET")
	}
	engine, err := executor.Open(filepath.Join(w.Base.StateDir, "targets", hash(strings.ToLower(root))), root)
	if err != nil {
		return nil, fileFault(err)
	}
	return &Publisher{Files: engine, OperationID: op.OperationID, ResourceKey: op.ResourceKey, Tools: w.Base.Tools}, nil
}

func (w *Worker) Handle(ctx context.Context, op runner.Operation) (value any, files []runner.File, err error) {
	defer func() {
		var fault *p.Fault
		if errors.As(err, &fault) {
			err = &Failure{Fault: fault}
		}
	}()
	switch {
	case op.OperationType == "SCAN_TEL_SAVED" && op.Platform == "TELEGRAM":
		var req telegram.ScanRequest
		if json.Unmarshal(op.Payload, &req) != nil {
			return nil, nil, p.NewFault("INVALID_INPUT", "SCAN")
		}
		value, err := telegram.Scan(ctx, w.Saved, req)
		return value, nil, err
	case op.OperationType == "LIST_QUARK_TREE" && op.Platform == "QUARK":
		var req quark.ListRequest
		if json.Unmarshal(op.Payload, &req) != nil {
			return nil, nil, p.NewFault("INVALID_INPUT", "LIST")
		}
		client := w.Tree
		if req.ShareID != nil {
			if scoped, ok := client.(interface {
				ForShare(string, string) (quark.Client, error)
			}); ok {
				client, err = scoped.ForShare(req.AccountID, *req.ShareID)
				if err != nil {
					return nil, nil, err
				}
			}
		}
		value, err := quark.List(ctx, client, req)
		return value, nil, err
	case (op.OperationType == "TRANSFER_QUARK_ITEMS" || op.OperationType == "RECONCILE_QUARK_TRANSFER") && op.Platform == "QUARK":
		var req quark.TransferRequest
		if json.Unmarshal(op.Payload, &req) != nil || op.AccountRef == nil || w.Transfers == nil {
			return nil, nil, p.NewFault("INVALID_INPUT", "TRANSFER")
		}
		value, err := w.Transfers.Transfer(ctx, op.OperationID, *op.AccountRef, req, op.OperationType == "RECONCILE_QUARK_TRANSFER")
		return value, nil, err
	case op.OperationType == "DOWNLOAD_MEDIA" && op.Platform == "TELEGRAM":
		var req telegram.DownloadRequest
		if json.Unmarshal(op.Payload, &req) != nil {
			return nil, nil, p.NewFault("INVALID_INPUT", "PLAN")
		}
		publisher, err := w.open(ctx, op, req.Outputs)
		if err != nil {
			return nil, nil, err
		}
		defer publisher.Files.Close()
		result, err := telegram.Download(ctx, w.Telegram, publisher, req)
		return result, wire(result.Files), err
	case op.OperationType == "DOWNLOAD_MEDIA" && op.Platform == "QUARK":
		var req quark.DownloadRequest
		if json.Unmarshal(op.Payload, &req) != nil || op.AccountRef == nil || *op.AccountRef == "" {
			return nil, nil, p.NewFault("INVALID_INPUT", "PLAN")
		}
		publisher, err := w.open(ctx, op, req.Outputs)
		if err != nil {
			return nil, nil, err
		}
		defer publisher.Files.Close()
		result, err := quark.Download(ctx, w.Quark, publisher, *op.AccountRef, req)
		return result, wire(result.Files), err
	default:
		if w.Base != nil && op.Platform == "BILIBILI" {
			return w.Base.Handle(ctx, op)
		}
		return nil, nil, p.NewFault("PROVIDER_UNAVAILABLE", "DISPATCH")
	}
}

// Recover uses shared durable intents/receipts, including files published before
// the final result was saved. It never calls a platform or starts a new download.
func (w *Worker) Recover(op runner.Operation) (any, []runner.File, bool, error) {
	if op.OperationType == "TRANSFER_QUARK_ITEMS" && w.Transfers != nil && op.AccountRef != nil {
		var req quark.TransferRequest
		if json.Unmarshal(op.Payload, &req) != nil {
			return nil, nil, false, p.NewFault("INVALID_INPUT", "RECOVER")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := w.Transfers.Transfer(ctx, op.OperationID, *op.AccountRef, req, true)
		if err != nil {
			return nil, nil, false, nil
		}
		return result, nil, result.Complete, nil
	}
	if op.Platform == "BILIBILI" && w.Base != nil {
		return w.Base.Recover(op)
	}
	if op.OperationType != "DOWNLOAD_MEDIA" {
		return nil, nil, false, nil
	}
	var plan struct {
		Outputs []p.Output `json:"outputs"`
	}
	if json.Unmarshal(op.Payload, &plan) != nil {
		return nil, nil, false, p.NewFault("INVALID_INPUT", "RECOVER")
	}
	publisher, err := w.open(context.Background(), op, plan.Outputs)
	if err != nil {
		return nil, nil, false, err
	}
	defer publisher.Files.Close()
	receipts, err := publisher.Files.Pending()
	if err != nil {
		return nil, nil, false, fileFault(err)
	}
	byID := map[string]executor.Receipt{}
	for _, receipt := range receipts {
		byID[receipt.OperationID] = receipt
	}
	result := p.DownloadResult{Files: []p.FileEvidence{}, Warnings: []string{}}
	for _, output := range plan.Outputs {
		receipt, ok := byID[media.OutputOperationID(op.OperationID, output.ID)]
		if !ok {
			continue
		}
		fact := evidence(output, receipt)
		if err := p.CheckEvidence(output, fact); err != nil {
			return nil, nil, false, err
		}
		result.Files = append(result.Files, fact)
	}
	return result, wire(result.Files), len(result.Files) == len(plan.Outputs), nil
}
