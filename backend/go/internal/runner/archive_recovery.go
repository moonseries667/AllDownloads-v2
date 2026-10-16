package runner

import (
	"context"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

// The reconciliation operation has its own lease but observes only the original
// upload's private reserved IDs and the exact manifest supplied by Java.
func (w *StorageWorker) reconcileArchive(ctx context.Context, op Operation) (any, []File, error) {
	var payload struct {
		SchemaVersion       int                  `json:"schemaVersion"`
		OriginalOperationID string               `json:"originalOperationId"`
		Upload              drive.ArchiveRequest `json:"upload"`
	}
	if decode(op.Payload, &payload) != nil || payload.SchemaVersion != 1 || payload.OriginalOperationID == "" || payload.OriginalOperationID == op.OperationID || op.Destination == nil || w.Roots == nil || w.State == nil {
		return nil, nil, media.Fail("INVALID_PAYLOAD", "VERIFY", false)
	}
	client, err := w.client(payload.Upload.ArchiveTarget.AccountID, op)
	if err != nil {
		return nil, nil, err
	}
	root, err := w.Roots.canonicalRoot(op.Destination.RootPath)
	if err != nil {
		return nil, nil, err
	}
	if w.Roots.rootID(root) != op.Destination.RootID {
		return nil, nil, media.Fail("TARGET_ID_MISMATCH", "VERIFY", false)
	}
	proof, err := client.Reconcile(ctx, w.State, payload.OriginalOperationID, root, payload.Upload)
	return proof, nil, err
}
