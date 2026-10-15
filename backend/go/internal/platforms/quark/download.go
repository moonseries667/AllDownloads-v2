package quark

import (
	"context"
	"encoding/json"
	"io"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type DownloadRequest struct {
	SchemaVersion int         `json:"schemaVersion"`
	ShareID       string      `json:"shareId"`
	Nodes         []CloudNode `json:"nodes"`
	Outputs       []p.Output  `json:"outputs"`
	ArchivePolicy string      `json:"archivePolicy"`
}

type DownloadClient interface {
	// OpenNode checks managed-share membership and the frozen version before
	// opening bytes. A missing ID must never fall back to another equal name.
	OpenNode(context.Context, string, CloudNode) (io.ReadCloser, error)
}

func Download(ctx context.Context, c DownloadClient, publisher p.Publisher, accountRef string, req DownloadRequest) (p.DownloadResult, error) {
	empty := p.DownloadResult{Files: []p.FileEvidence{}, Warnings: []string{}}
	if req.SchemaVersion != 1 || accountRef == "" || req.ShareID == "" || len(req.Nodes) == 0 || (req.ArchivePolicy != "AUTO" && req.ArchivePolicy != "NONE") {
		return empty, p.NewFault("INVALID_INPUT", "PLAN")
	}
	if err := p.ValidateOutputs(req.Outputs); err != nil {
		return empty, err
	}
	if c == nil {
		return empty, p.NewFault("PROVIDER_UNAVAILABLE", "DOWNLOAD")
	}
	nodes := map[string]CloudNode{}
	for _, n := range req.Nodes {
		if n.ID == "" || n.ExternalID == "" || n.Version == "" || n.Kind != "FILE" || n.ShareID != req.ShareID || n.Availability != "AVAILABLE" {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		if _, exists := nodes[n.ID]; exists {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		nodes[n.ID] = n
	}
	files := []p.File{}
	seen := map[string]bool{}
	for _, o := range req.Outputs {
		n, exists := nodes[o.NodeID]
		if !exists || seen[n.ID] || o.Kind != "FILE" || o.AttachmentID != "" || len(o.TrackIDs) > 0 || (n.SizeBytes != nil && (o.SizeBytes == nil || *o.SizeBytes != *n.SizeBytes)) {
			return empty, p.NewFault("INVALID_INPUT", "PLAN")
		}
		seen[n.ID] = true
		key, _ := json.Marshal([]string{accountRef, req.ShareID, n.ExternalID, n.Version})
		files = append(files, p.File{Key: string(key), Output: o, Open: func(ctx context.Context) (io.ReadCloser, error) { return c.OpenNode(ctx, accountRef, n) }})
	}
	if len(seen) != len(nodes) {
		return empty, p.NewFault("INVALID_INPUT", "PLAN")
	}
	return p.ExecuteFiles(ctx, publisher, files)
}
