package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

type Handler struct {
	State        *executor.State
	StateDir     string
	AllowedRoots []string
	Roots        map[string]string
	Provider     media.Provider
	Downloads    media.Downloader
	Tools        *media.Tools
}
type snapshot struct {
	Content  Content
	Metadata media.Metadata
}

func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func pointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func decode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

func (h *Handler) Capabilities() []string {
	result := []string{"VALIDATE_TARGET"}
	if h.Provider != nil {
		result = append(result, "FETCH_BIL_METADATA", "SYNC_BIL_SOURCE")
		if h.Tools != nil {
			result = append(result, "DOWNLOAD_MEDIA")
		}
	}
	return result
}
func (h *Handler) canonicalRoot(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", media.Fail("INVALID_TARGET", "TARGET", false)
	}
	root := filepath.Clean(path)
	allowed := false
	for _, parent := range h.AllowedRoots {
		rel, err := filepath.Rel(filepath.Clean(parent), root)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
		}
	}
	if !allowed {
		return "", media.Fail("TARGET_OUTSIDE_ALLOWED_ROOTS", "TARGET", false)
	}
	if err := executor.ValidateRoot(root); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", media.Fail("TARGET_PERMISSION_DENIED", "TARGET", false)
		}
		if errors.Is(err, os.ErrNotExist) {
			return "", media.Fail("TARGET_NOT_FOUND", "TARGET", false)
		}
		return "", media.Fail("TARGET_NOT_WRITABLE", "TARGET", false)
	}
	return root, nil
}
func (h *Handler) rootID(root string) string {
	for id, path := range h.Roots {
		if strings.EqualFold(filepath.Clean(path), root) {
			return id
		}
	}
	return "root-" + digest(strings.ToLower(root))[:24]
}
func (h *Handler) Handle(ctx context.Context, op Operation) (any, []File, error) {
	if op.AccountRef != nil && *op.AccountRef != "" {
		bilibiliOperation := op.OperationType == "SYNC_BIL_SOURCE" || op.OperationType == "FETCH_BIL_METADATA" || op.OperationType == "DOWNLOAD_MEDIA"
		if op.Platform != "BILIBILI" || *op.AccountRef != "bilibili-main" || !bilibiliOperation {
			return nil, nil, media.Fail("ACCOUNT_NOT_CONFIGURED", "ACCOUNT", false)
		}
	}
	switch op.OperationType {
	case "VALIDATE_TARGET":
		var p struct {
			SchemaVersion int    `json:"schemaVersion"`
			TargetID      string `json:"targetId"`
			Path          string `json:"path"`
		}
		if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 || p.TargetID == "" {
			return nil, nil, media.Fail("INVALID_PAYLOAD", "VALIDATE", false)
		}
		root, err := h.canonicalRoot(p.Path)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"targetId": p.TargetID, "rootId": h.rootID(root), "canonicalPath": root, "writable": true}, nil, nil
	case "FETCH_BIL_METADATA":
		var p struct {
			SchemaVersion  int    `json:"schemaVersion"`
			ContentID      string `json:"contentId"`
			ExternalID     string `json:"externalId"`
			ExternalIDType string `json:"externalIdType"`
		}
		if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 {
			return nil, nil, media.Fail("INVALID_PAYLOAD", "VALIDATE", false)
		}
		content, err := h.fetch(ctx, media.Reference{Kind: p.ExternalIDType, ID: p.ExternalID}, p.ContentID)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"content": content}, nil, nil
	case "SYNC_BIL_SOURCE":
		return h.sync(ctx, op)
	case "DOWNLOAD_MEDIA":
		return h.download(ctx, op)
	default:
		return nil, nil, media.Fail("UNSUPPORTED_OPERATION", "VALIDATE", false)
	}
}
func (h *Handler) fetch(ctx context.Context, ref media.Reference, id string) (Content, error) {
	if h.Provider == nil {
		return Content{}, media.Fail("PLATFORM_NOT_CONFIGURED", "METADATA", false)
	}
	meta, err := h.Provider.Fetch(ctx, ref)
	if err != nil {
		return Content{}, err
	}
	return h.saveMetadata(meta, id)
}
func (h *Handler) saveMetadata(meta media.Metadata, id string) (Content, error) {
	if id == "" {
		id = meta.ID
	}
	if id == "" {
		return Content{}, media.Fail("CONTENT_ID_MISSING", "METADATA", false)
	}
	content := Content{ID: id, Platform: "BILIBILI", ExternalID: meta.ExternalID, ExternalIDType: meta.IdentifierType, Kind: meta.Kind, Title: meta.Title, CoverURL: pointer(meta.CoverURL), SourceIDs: []string{}, Sources: []Source{}, OriginalURL: meta.OriginalURL, Availability: "AVAILABLE", DownloadSummary: map[string]string{"state": "NOT_REQUESTED"}, ArchiveSummary: map[string]string{"state": "NOT_REQUESTED"}, PublishedAt: meta.PublishedAt, Description: meta.Description, MediaUnits: []Unit{}, Warnings: append([]string{}, meta.Warnings...)}
	if meta.Author != "" {
		content.Author = map[string]string{"id": "", "name": meta.Author}
	}
	if meta.IdentifierType == "SEASON_ID" {
		content.SeasonID = pointer(meta.ExternalID)
	}
	seen := map[string]bool{}
	for _, u := range meta.Units {
		unit := Unit{ID: u.ID, ContentID: id, ExternalID: u.ExternalID, Title: u.Title, Index: u.Index, Kind: u.Kind, Language: pointer(u.Language), LanguageLabel: pointer(u.LanguageName), Duration: u.Duration, Availability: "AVAILABLE", Tracks: []Track{}}
		content.Warnings = append(content.Warnings, u.Warnings...)
		for _, t := range u.Tracks {
			if t.Kind == "SUBTITLE" && t.SubtitleType != "HUMAN" {
				content.Warnings = append(content.Warnings, "SUBTITLE_"+t.SubtitleType+"_EXCLUDED")
				continue
			}
			if t.ID == "" || seen[t.ID] {
				return Content{}, media.Fail("DUPLICATE_TRACK_ID", "METADATA", false)
			}
			seen[t.ID] = true
			var size *string
			if t.Size != nil {
				if *t.Size < 0 {
					return Content{}, media.Fail("INVALID_METADATA_SIZE", "METADATA", false)
				}
				v := strconv.FormatInt(*t.Size, 10)
				size = &v
			}
			unit.Tracks = append(unit.Tracks, Track{ID: t.ID, Kind: t.Kind, Available: t.Selectable(), ReasonCode: pointer(t.Reason), Language: pointer(t.Language), LanguageLabel: pointer(t.LanguageName), SubtitleOrigin: t.SubtitleType, Duration: t.Duration, Size: size})
		}
		if len(unit.Tracks) == 0 {
			unit.Availability = "UNKNOWN"
			unit.AvailabilityReason = pointer("MEDIA_METADATA_UNKNOWN")
		}
		content.MediaUnits = append(content.MediaUnits, unit)
	}
	// The revision excludes acquisition time and private expiring locators.
	raw, _ := json.Marshal(content)
	content.Revision = digest(string(raw))
	content.UpdatedAt = time.Now().UTC()
	if err := h.State.Write("snapshot-"+digest(id+"@"+content.Revision), snapshot{content, meta}); err != nil {
		return Content{}, err
	}
	return content, nil
}
func (h *Handler) sync(ctx context.Context, op Operation) (any, []File, error) {
	var p struct {
		SchemaVersion int    `json:"schemaVersion"`
		SourceID      string `json:"sourceId"`
		Kind          string `json:"kind"`
		ExternalID    string `json:"externalId"`
		URL           string `json:"url"`
	}
	if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 || p.SourceID == "" {
		return nil, nil, media.Fail("INVALID_PAYLOAD", "VALIDATE", false)
	}
	if p.Kind == "FAVORITE" || p.Kind == "COLLECTION" {
		provider, ok := h.Provider.(media.SourceProvider)
		if !ok {
			return nil, nil, media.Fail("SOURCE_TYPE_UNSUPPORTED", "METADATA", false)
		}
		metas, title, err := provider.FetchSource(ctx, p.Kind, p.ExternalID, p.URL)
		if err != nil {
			return nil, nil, err
		}
		contents := []Content{}
		revisions := ""
		for _, meta := range metas {
			content, err := h.saveMetadata(meta, "")
			if err != nil {
				return nil, nil, err
			}
			contents = append(contents, content)
			revisions += content.ID + "@" + content.Revision + "\n"
		}
		now := time.Now().UTC()
		group, label := "FAVORITES", "收藏夹"
		if p.Kind == "COLLECTION" {
			group, label = "COLLECTIONS", "合集"
		}
		source := Source{ID: p.SourceID, Platform: "BILIBILI", Kind: p.Kind, Title: title, URL: p.URL, Status: "READY", LastSyncedAt: &now, ItemCount: len(contents), Revision: digest(revisions), NavigationGroup: group, DisplayLabel: label + "-" + title}
		for i := range contents {
			contents[i].Sources = []Source{source}
			contents[i].SourceIDs = []string{p.SourceID}
		}
		return map[string]any{"source": source, "contents": contents, "complete": true}, nil, nil
	}
	ref := media.Reference{Kind: "BVID", ID: p.ExternalID}
	switch p.Kind {
	case "USER_ADDED":
	case "SERIES", "MOVIE":
		ref.Kind = "SEASON_ID"
		if strings.HasPrefix(p.ExternalID, "ep") {
			ref.Kind = "EPISODE_ID"
		}
		if strings.HasPrefix(p.ExternalID, "md") {
			ref.Kind = "MEDIA_ID"
		}
	default:
		return nil, nil, media.Fail("SOURCE_TYPE_UNSUPPORTED", "METADATA", false)
	}
	content, err := h.fetch(ctx, ref, "")
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	label := "用户添加视频"
	if p.Kind == "SERIES" {
		label = "番剧"
	}
	if p.Kind == "MOVIE" {
		label = "电影"
	}
	source := Source{ID: p.SourceID, Platform: "BILIBILI", Kind: p.Kind, Title: content.Title, URL: p.URL, Status: "READY", LastSyncedAt: &now, ItemCount: 1, Revision: content.Revision, Pinned: false, NavigationGroup: "OTHER", DisplayLabel: label + "-" + content.Title}
	content.SourceIDs = []string{p.SourceID}
	content.Sources = []Source{source}
	return map[string]any{"source": source, "contents": []Content{content}, "complete": true}, nil, nil
}
func (h *Handler) download(ctx context.Context, op Operation) (any, []File, error) {
	var p DownloadPayload
	if decode(op.Payload, &p) != nil || p.SchemaVersion != 1 || p.QualityPolicy != "HIGHEST_AVAILABLE" || (p.ArchivePolicy != "AUTO" && p.ArchivePolicy != "NONE") || op.Destination == nil {
		return nil, nil, media.Fail("INVALID_PAYLOAD", "VALIDATE", false)
	}
	root, err := h.canonicalRoot(op.Destination.RootPath)
	if err != nil {
		return nil, nil, err
	}
	if h.rootID(root) != op.Destination.RootID {
		return nil, nil, media.Fail("TARGET_ID_MISMATCH", "TARGET", false)
	}
	var snap snapshot
	if err := h.State.Read("snapshot-"+digest(p.ContentID+"@"+p.ContentRevision), &snap); err != nil {
		return nil, nil, media.Fail("METADATA_SNAPSHOT_MISSING", "RESOLVE", false)
	}
	if snap.Content.Kind != p.ContentKind || snap.Content.ExternalID != p.ExternalID || snap.Content.ExternalIDType != p.ExternalIDType {
		return nil, nil, media.Fail("RESOURCE_VERSION_CHANGED", "RESOLVE", false)
	}
	tracks := map[string]media.Track{}
	units := map[string]media.Unit{}
	for _, unit := range snap.Metadata.Units {
		units[unit.ID] = unit
		for _, track := range unit.Tracks {
			tracks[track.ID] = track
		}
	}
	chosen := map[string]bool{}
	for _, unit := range p.Units {
		actual, ok := units[unit.ID]
		if !ok || actual.ExternalID != unit.ExternalID || unit.ContentID != p.ContentID {
			return nil, nil, media.Fail("RESOURCE_VERSION_CHANGED", "RESOLVE", false)
		}
		membership := map[string]bool{}
		for _, track := range actual.Tracks {
			membership[track.ID] = true
		}
		for _, track := range unit.Tracks {
			t, ok := tracks[track.ID]
			if !ok || !membership[track.ID] || !t.Selectable() || !track.Available || t.Kind != track.Kind || chosen[track.ID] {
				return nil, nil, media.Fail("INVALID_TRACK_SELECTION", "RESOLVE", false)
			}
			chosen[track.ID] = true
		}
	}
	if len(chosen) == 0 || len(p.Outputs) == 0 {
		return nil, nil, media.Fail("EMPTY_SELECTION", "RESOLVE", false)
	}
	plan := media.Plan{OperationID: op.OperationID, ResourceKey: op.ResourceKey}
	used := map[string]bool{}
	base := strings.TrimSuffix(strings.ReplaceAll(op.Destination.RelativePath, "\\", "/"), "/") + "/"
	if err := executor.ValidateRelativePath(op.Destination.RelativePath); err != nil {
		return nil, nil, err
	}
	for _, out := range p.Outputs {
		path := strings.ReplaceAll(out.RelativePath, "\\", "/")
		if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(base)) {
			return nil, nil, media.Fail("OUTPUT_OUTSIDE_CONTENT_PATH", "PLAN", false)
		}
		output := media.Output{ID: out.ID, RelativePath: out.RelativePath, Mode: "SEPARATE"}
		if out.Kind == "MUXED" {
			output.Mode = "MERGE_AV"
		}
		for _, id := range out.TrackIDs {
			if !chosen[id] || used[id] {
				return nil, nil, media.Fail("OUTPUT_SELECTION_MISMATCH", "PLAN", false)
			}
			used[id] = true
			t := tracks[id]
			if out.Kind != "MUXED" && t.Kind != out.Kind {
				return nil, nil, media.Fail("OUTPUT_KIND_MISMATCH", "PLAN", false)
			}
			output.Inputs = append(output.Inputs, media.Input{TrackID: id, Kind: t.Kind, Source: t.Source})
		}
		plan.Outputs = append(plan.Outputs, output)
	}
	if len(used) != len(chosen) {
		return nil, nil, media.Fail("OUTPUT_SELECTION_MISMATCH", "PLAN", false)
	}
	engine, err := executor.Open(filepath.Join(h.StateDir, "targets", digest(strings.ToLower(root))), root)
	if err != nil {
		return nil, nil, err
	}
	defer engine.Close()
	worker := media.Executor{Files: engine, Downloads: h.Downloads, Tools: h.Tools}
	facts, execErr := worker.Execute(ctx, plan)
	files := make([]File, 0, len(facts))
	for _, fact := range facts {
		kinds := []string{}
		for _, out := range plan.Outputs {
			if out.ID == fact.ID {
				for _, input := range out.Inputs {
					kinds = append(kinds, input.Kind)
				}
			}
		}
		files = append(files, File{fact.ID, fact.RelativePath, fact.SizeBytes, fact.SHA256, kinds, fact.Duration, true})
	}
	return map[string]any{"files": files, "warnings": []string{}}, files, execErr
}

// FixtureProvider is opt-in, local-only test input; production never seeds a
// successful catalog or accesses a platform on ordinary runner startup.
type FixtureProvider map[string]media.Metadata

func (p FixtureProvider) Fetch(ctx context.Context, ref media.Reference) (media.Metadata, error) {
	if err := ctx.Err(); err != nil {
		return media.Metadata{}, err
	}
	meta, ok := p[ref.ID]
	if !ok {
		return meta, media.Fail("FIXTURE_NOT_FOUND", "METADATA", false)
	}
	return meta, nil
}
