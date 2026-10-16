package drive

import (
	"context"
	"errors"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

type ArchiveReconciliation struct {
	OriginalOperationID string         `json:"originalOperationId"`
	Complete            bool           `json:"complete"`
	SafeToResume        bool           `json:"safeToResume"`
	Archive             *ArchiveResult `json:"archive"`
	ReasonCode          *string        `json:"reasonCode"`
}

// Reconcile uses the same read-only verification as Verify and returns the
// evidence needed to resume the original operation. It never creates a plan,
// reserves an ID, creates a directory, or uploads bytes. Partial file facts do
// not become a complete archive result. Unallocated future items can resume
// under Archive's persist-before-write invariant, only with a matching plan.
func (c *Client) Reconcile(ctx context.Context, state *executor.State, originalOperationID, root string, req ArchiveRequest) (ArchiveReconciliation, error) {
	result := ArchiveReconciliation{OriginalOperationID: originalOperationID}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	archive, proof, err := c.verifyArchive(ctx, state, originalOperationID, root, req)
	if err == nil && archive.Verified {
		result.Complete = true
		result.Archive = &archive
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return result, err
	}
	code := "ARCHIVE_VERIFICATION_FAILED"
	var failure *media.Failure
	if errors.As(err, &failure) {
		code = failure.Code
	}
	result.ReasonCode = &code
	result.SafeToResume = isCode(err, "ARCHIVE_NOT_CONFIRMED") && proof.planExists && proof.resumable
	return result, nil
}
