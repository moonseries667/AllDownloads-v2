package runner

import (
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	"path/filepath"
	"strings"
)

// Recover observes published files and existing private receipts only. It never
// repeats downloads or decides that an unfinished operation should be scheduled.
func (h *Handler) Recover(op Operation) (any, []File, bool, error) {
	if op.OperationType != "DOWNLOAD_MEDIA" || op.Destination == nil {
		return nil, nil, false, nil
	}
	var payload DownloadPayload
	if decode(op.Payload, &payload) != nil {
		return nil, nil, false, media.Fail("INVALID_PERSISTED_PLAN", "RECOVERY", false)
	}
	root, err := h.canonicalRoot(op.Destination.RootPath)
	if err != nil {
		return nil, nil, false, err
	}
	if h.rootID(root) != op.Destination.RootID {
		return nil, nil, false, media.Fail("TARGET_ID_MISMATCH", "RECOVERY", false)
	}
	engine, err := executor.Open(filepath.Join(h.StateDir, "targets", digest(strings.ToLower(root))), root)
	if err != nil {
		return nil, nil, false, err
	}
	defer engine.Close()
	receipts, err := engine.Pending()
	if err != nil {
		return nil, nil, false, err
	}
	byID := map[string]executor.Receipt{}
	for _, receipt := range receipts {
		byID[receipt.OperationID] = receipt
	}
	tracks := map[string]string{}
	for _, unit := range payload.Units {
		for _, track := range unit.Tracks {
			tracks[track.ID] = track.Kind
		}
	}
	files := []File{}
	for _, output := range payload.Outputs {
		receipt, ok := byID[media.OutputOperationID(op.OperationID, output.ID)]
		if !ok {
			continue
		}
		if receipt.RelativePath != strings.ReplaceAll(output.RelativePath, "\\", "/") {
			return nil, nil, false, media.Fail("RECEIPT_PLAN_MISMATCH", "RECOVERY", false)
		}
		kinds := []string{}
		for _, id := range output.TrackIDs {
			kind, ok := tracks[id]
			if !ok {
				return nil, nil, false, media.Fail("RECEIPT_PLAN_MISMATCH", "RECOVERY", false)
			}
			kinds = append(kinds, kind)
		}
		files = append(files, File{OutputID: output.ID, RelativePath: receipt.RelativePath, SizeBytes: receipt.SizeBytes, SHA256: receipt.SHA256, MediaKinds: kinds, Verified: true})
	}
	return map[string]any{"files": files, "warnings": []string{}}, files, len(files) > 0 && len(files) == len(payload.Outputs), nil
}
