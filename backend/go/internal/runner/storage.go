package runner

import (
	"context"
	"errors"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/folder"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

type DirectoryPicker interface {
	Pick(context.Context, time.Duration) folder.Result
}

// StorageWorker extends the existing worker dispatch, not the Java scheduler.
// Account sessions and the native dialog are owned by the execution host.
type StorageWorker struct {
	Next        Worker
	Roots       *Handler
	State       *executor.State
	HostID      string
	Picker      DirectoryPicker
	PickerReady func() bool
	Drives      map[string]*drive.Client
}

func (w *StorageWorker) SupportsOperation(op Operation) bool {
	if !validPlatform(op.Platform) {
		return false
	}
	switch op.OperationType {
	case "SELECT_LOCAL_DIRECTORY":
		return w.Picker != nil
	case "LIST_DRIVE_FOLDERS", "VALIDATE_DRIVE_TARGET", "UPLOAD_ARTIFACT", "VERIFY_ARCHIVE":
		return len(w.Drives) > 0
	}
	if adapter, ok := w.Next.(interface{ SupportsOperation(Operation) bool }); ok {
		return adapter.SupportsOperation(op)
	}
	if adapter, ok := w.Next.(interface{ SupportsPlatform(string) bool }); ok {
		return adapter.SupportsPlatform(op.Platform)
	}
	return w.Next != nil && op.Platform == "BILIBILI"
}

func (w *StorageWorker) Capabilities() []string {
	caps := []string{}
	if w.Next != nil {
		caps = append(caps, w.Next.Capabilities()...)
	}
	if w.Picker != nil {
		caps = append(caps, "SELECT_LOCAL_DIRECTORY")
	}
	if len(w.Drives) > 0 {
		caps = append(caps, "LIST_DRIVE_FOLDERS", "VALIDATE_DRIVE_TARGET", "UPLOAD_ARTIFACT", "VERIFY_ARCHIVE")
	}
	return caps
}
func (w *StorageWorker) RegistrationFacts() map[string]any {
	available := w.Picker != nil && w.PickerReady != nil && w.PickerReady()
	var reason *string
	if !available {
		reason = pointer("NATIVE_PICKER_UNAVAILABLE")
	}
	return map[string]any{"directoryPicker": map[string]any{"available": available, "reasonCode": reason, "hostId": w.HostID}}
}
func (w *StorageWorker) client(account string, op Operation) (*drive.Client, error) {
	c := w.Drives[account]
	if c == nil || c.AccountID != account || (op.AccountRef != nil && *op.AccountRef != "" && *op.AccountRef != account) {
		return nil, media.Fail("AUTH_REQUIRED", "DRIVE", false)
	}
	return c, nil
}
func (w *StorageWorker) Handle(ctx context.Context, op Operation) (any, []File, error) {
	switch op.OperationType {
	case "SELECT_LOCAL_DIRECTORY":
		var p struct {
			SchemaVersion int    `json:"schemaVersion"`
			SelectionID   string `json:"selectionId"`
			Title         string `json:"title"`
			Timeout       int    `json:"timeoutSeconds"`
		}
		if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 || p.SelectionID == "" || p.Timeout < 1 || p.Timeout > 120 || p.Title != "选择本机下载目录" || op.Destination != nil {
			return nil, nil, media.Fail("INVALID_PAYLOAD", "DIRECTORY", false)
		}
		if w.Picker == nil || w.PickerReady == nil || !w.PickerReady() {
			return nil, nil, media.Fail("NATIVE_PICKER_UNAVAILABLE", "DIRECTORY", false)
		}
		selected := w.Picker.Pick(ctx, time.Duration(p.Timeout)*time.Second)
		if selected.Status == "CANCELLED" {
			return map[string]any{"selectionId": p.SelectionID, "cancelled": true, "target": nil}, nil, nil
		}
		if selected.Status != "SELECTED" {
			return nil, nil, media.Fail("NATIVE_PICKER_UNAVAILABLE", "DIRECTORY", false)
		}
		if w.Roots == nil {
			return nil, nil, errors.New("root validator is not configured")
		}
		root, err := w.Roots.canonicalRoot(selected.Path)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"selectionId": p.SelectionID, "cancelled": false, "target": map[string]any{"rootId": w.Roots.rootID(root), "canonicalPath": root, "writable": true}}, nil, nil
	case "LIST_DRIVE_FOLDERS":
		var p struct {
			SchemaVersion int     `json:"schemaVersion"`
			AccountID     string  `json:"accountId"`
			ParentID      *string `json:"parentId"`
			Cursor        *string `json:"cursor"`
			PageSize      int     `json:"pageSize"`
		}
		if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 {
			return nil, nil, media.Fail("INVALID_PAYLOAD", "DRIVE", false)
		}
		c, err := w.client(p.AccountID, op)
		if err != nil {
			return nil, nil, err
		}
		var parent, cursor string
		if p.ParentID != nil {
			parent = *p.ParentID
		}
		if p.Cursor != nil {
			cursor = *p.Cursor
		}
		page, err := c.List(ctx, parent, cursor, p.PageSize)
		return page, nil, err
	case "VALIDATE_DRIVE_TARGET":
		var p struct {
			SchemaVersion int    `json:"schemaVersion"`
			ValidationID  string `json:"validationId"`
			AccountID     string `json:"accountId"`
			FolderID      string `json:"folderId"`
			Revision      string `json:"revision"`
		}
		if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 || p.ValidationID == "" {
			return nil, nil, media.Fail("INVALID_PAYLOAD", "DRIVE", false)
		}
		c, err := w.client(p.AccountID, op)
		if err != nil {
			return nil, nil, err
		}
		target, err := c.Validate(ctx, p.FolderID, p.Revision)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"validationId": p.ValidationID, "target": target, "writable": true}, nil, nil
	case "UPLOAD_ARTIFACT":
		return w.archive(ctx, op, false)
	case "VERIFY_ARCHIVE":
		return w.reconcileArchive(ctx, op)
	default:
		if w.Next == nil {
			return nil, nil, media.Fail("UNSUPPORTED_OPERATION", "EXECUTE", false)
		}
		return w.Next.Handle(ctx, op)
	}
}
func (w *StorageWorker) archive(ctx context.Context, op Operation, verify bool) (any, []File, error) {
	var p drive.ArchiveRequest
	if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 || p.ProjectID == "" || p.ArchivePolicy != "AUTO" || len(p.Files) == 0 || op.Destination == nil || w.Roots == nil {
		return nil, nil, media.Fail("INVALID_PAYLOAD", "UPLOAD", false)
	}
	c, err := w.client(p.ArchiveTarget.AccountID, op)
	if err != nil {
		if !verify && media.Classify(err, "DRIVE").Code == "AUTH_REQUIRED" {
			return nil, nil, &uploadPreflightAuthFailure{code: "AUTH_REQUIRED"}
		}
		return nil, nil, err
	}
	if !verify {
		// This stage is read-only and precedes every upload write. Its precise
		// no-effects authentication failure lets the user log in and retry the
		// same intent; later failures still require remote reconciliation.
		if err := c.Check(ctx); err != nil {
			code := media.Classify(err, "DRIVE").Code
			if code == "AUTH_REQUIRED" || code == "ACCOUNT_NOT_CONFIGURED" {
				return nil, nil, &uploadPreflightAuthFailure{code: code}
			}
			return nil, nil, err
		}
	}
	root, err := w.Roots.canonicalRoot(op.Destination.RootPath)
	if err != nil {
		return nil, nil, err
	}
	if w.Roots.rootID(root) != op.Destination.RootID {
		return nil, nil, media.Fail("TARGET_ID_MISMATCH", "UPLOAD", false)
	}
	var result drive.ArchiveResult
	if verify {
		result, err = c.Verify(ctx, w.State, op.OperationID, root, p)
	} else {
		result, err = c.Archive(ctx, w.State, op.OperationID, root, p)
	}
	files := make([]File, 0, len(result.Files))
	for _, done := range result.Files {
		files = append(files, File{OutputID: done.OutputID, RelativePath: done.RelativePath, SizeBytes: done.SizeBytes, SHA256: done.SHA256, Verified: true})
	}
	return result, files, err
}

type uploadPreflightAuthFailure struct{ code string }

func (f *uploadPreflightAuthFailure) Error() string { return "DRIVE: " + f.code }
func (f *uploadPreflightAuthFailure) RunnerFailure() (string, *ResultError) {
	return "NEEDS_ACTION", &ResultError{Code: f.code, Message: "上传尚未开始，请完成账号登录后重试", CompletedEffects: []string{}, PendingEffects: []string{}}
}
func (w *StorageWorker) Recover(op Operation) (any, []File, bool, error) {
	if op.OperationType == "UPLOAD_ARTIFACT" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		value, files, err := w.archive(ctx, op, true)
		// An unconfirmed remote state is an observed incomplete recovery, not
		// permission to send bytes during replay or manufacture cancellation.
		if err != nil {
			return value, files, false, nil
		}
		return value, files, true, nil
	}
	if recovery, ok := w.Next.(interface {
		Recover(Operation) (any, []File, bool, error)
	}); ok {
		return recovery.Recover(op)
	}
	return nil, nil, false, nil
}
