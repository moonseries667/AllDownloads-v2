package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type DownloadAttachment struct {
	Attachment
	MessageID string `json:"messageId"`
}
type DownloadRequest struct {
	SchemaVersion int                  `json:"schemaVersion"`
	AccountID     string               `json:"accountId"`
	ResourceID    string               `json:"resourceId"`
	MessageIDs    []string             `json:"messageIds"`
	Attachments   []DownloadAttachment `json:"attachments"`
	Outputs       []p.Output           `json:"outputs"`
	ArchivePolicy string               `json:"archivePolicy"`
	Metadata      map[string]any       `json:"metadata"`
}

type DownloadClient interface {
	// OpenAttachment must resolve this frozen Saved Messages attachment only,
	// including its actual size/version; it must not discover extra attachments.
	OpenAttachment(context.Context, string, []string, DownloadAttachment) (io.ReadCloser, error)
}

type recordingPublisher struct {
	delegate p.Publisher
	verified map[string]p.FileEvidence
}

func (r *recordingPublisher) Ensure(ctx context.Context, key string, out p.Output, open p.OpenSource) (p.FileEvidence, error) {
	e, err := r.delegate.Ensure(ctx, key, out, open)
	if err == nil {
		err = p.CheckEvidence(out, e)
	}
	if err == nil {
		r.verified[out.ID] = e
	}
	return e, err
}

// Download deliberately has no eligibility logic: Java freezes a qualified
// unit. It requires every selected attachment plus exactly one matching sidecar.
func Download(ctx context.Context, c DownloadClient, publisher p.Publisher, req DownloadRequest) (p.DownloadResult, error) {
	empty := p.DownloadResult{Files: []p.FileEvidence{}, Warnings: []string{}}
	if req.SchemaVersion != 1 || req.AccountID == "" || req.ResourceID == "" || len(req.MessageIDs) == 0 || len(req.Attachments) == 0 || req.ArchivePolicy != "AUTO" || req.Metadata == nil {
		return empty, p.NewFault("INVALID_INPUT", "PLAN")
	}
	if err := p.ValidateOutputs(req.Outputs); err != nil {
		return empty, err
	}
	if c == nil || publisher == nil {
		return empty, p.NewFault("PROVIDER_UNAVAILABLE", "DOWNLOAD")
	}
	recorder := &recordingPublisher{delegate: publisher, verified: map[string]p.FileEvidence{}}
	attachments := map[string]DownloadAttachment{}
	for _, a := range req.Attachments {
		if a.ID == "" || a.ExternalID == "" || a.Name == "" {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		if _, exists := attachments[a.ID]; exists {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		attachments[a.ID] = a
	}
	media, metadata := map[string]p.Output{}, map[string]p.Output{}
	for _, o := range req.Outputs {
		a, exists := attachments[o.AttachmentID]
		if !exists || o.NodeID != "" || len(o.TrackIDs) > 0 {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		if o.Kind == "METADATA" {
			if _, exists := metadata[a.ID]; exists {
				return empty, p.NewFault("INVALID_INPUT", "PLAN")
			}
			metadata[a.ID] = o
		} else {
			kind := a.MediaType
			if kind == "UNKNOWN" {
				kind = "DOCUMENT"
			}
			switch kind {
			case "VIDEO", "AUDIO", "PHOTO", "DOCUMENT":
			default:
				return empty, p.NewFault("INVALID_INPUT", "PLAN")
			}
			if _, exists := media[a.ID]; exists || kind != o.Kind || (a.SizeBytes != nil && (o.SizeBytes == nil || *a.SizeBytes != *o.SizeBytes)) {
				return empty, p.NewFault("INVALID_INPUT", "PLAN")
			}
			media[a.ID] = o
		}
	}
	if len(media) != len(attachments) || len(metadata) != len(attachments) {
		return empty, p.NewFault("INVALID_INPUT", "PLAN")
	}
	// Stable per-attachment ordering means its sidecar follows verified bytes,
	// even when the Java output list is reordered for presentation.
	ids := make([]string, 0, len(attachments))
	for id := range attachments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	files := []p.File{}
	for _, id := range ids {
		a, m, side := attachments[id], media[id], metadata[id]
		if side.RelativePath != m.RelativePath+".metadata.json" {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		keyBytes, _ := json.Marshal(struct {
			Account    string
			Messages   []string
			Attachment DownloadAttachment
		}{req.AccountID, req.MessageIDs, a})
		key := string(keyBytes)
		files = append(files, p.File{Key: key, Output: m, Open: func(ctx context.Context) (io.ReadCloser, error) {
			return c.OpenAttachment(ctx, req.AccountID, req.MessageIDs, a)
		}})
		metaBytes, err := json.Marshal(req.Metadata)
		if err != nil {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		files = append(files, p.File{Key: key + string(metaBytes) + ":metadata-v1", Output: side, Open: func(ctx context.Context) (io.ReadCloser, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			evidence, ok := recorder.verified[m.ID]
			if !ok {
				return nil, p.NewFault("VERIFICATION_FAILED", "METADATA")
			}
			// Reuse is delegated to Publisher. This read must therefore not
			// fetch the attachment again and never includes private locators.
			body := map[string]any{}
			if err := json.Unmarshal(metaBytes, &body); err != nil {
				return nil, p.NewFault("INVALID_INPUT", "METADATA")
			}
			body["schemaVersion"] = 1
			body["message_id"] = a.MessageID
			body["original_file_name"] = a.Name
			body["media_type"] = a.MediaType
			body["expected_size"] = a.SizeBytes
			body["local_path"] = m.RelativePath
			body["size"] = evidence.SizeBytes
			body["downloaded_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			body["status"] = "VERIFIED"
			data, err := json.Marshal(body)
			if err != nil {
				return nil, p.NewFault("INVALID_INPUT", "METADATA")
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		}})
	}
	return p.ExecuteFiles(ctx, recorder, files)
}
