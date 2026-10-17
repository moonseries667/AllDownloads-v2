package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

func setupHandler(t *testing.T) (*Handler, string, string) {
	t.Helper()
	stateDir, root := t.TempDir(), t.TempDir()
	state, err := executor.OpenState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	return &Handler{State: state, StateDir: stateDir, AllowedRoots: []string{root}, Roots: map[string]string{"default": root}}, root, stateDir
}
func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func testMetadata(server string) media.Metadata {
	return media.Metadata{ID: "content", ExternalID: "BVfixture", IdentifierType: "BVID", Kind: "SINGLE", Title: "标题", OriginalURL: "https://www.bilibili.com/video/BVfixture", Units: []media.Unit{{ID: "unit", ExternalID: "cid", Kind: "MAIN", Index: 1, Tracks: []media.Track{
		{ID: "sub-en", Kind: "SUBTITLE", Language: "en", LanguageName: "英语", SubtitleType: "HUMAN", Availability: "AVAILABLE", Source: media.Source{URL: server + "/en", Version: "s1"}},
		{ID: "sub-zh", Kind: "SUBTITLE", Language: "zh", LanguageName: "中文", SubtitleType: "HUMAN", Availability: "AVAILABLE", Source: media.Source{URL: server + "/zh", Version: "s2"}},
		{ID: "sub-ai", Kind: "SUBTITLE", SubtitleType: "AI", Availability: "AVAILABLE"},
		{ID: "sub-unknown", Kind: "SUBTITLE", SubtitleType: "UNKNOWN", Availability: "AVAILABLE"},
	}}}}
}

type accountReferenceProvider func(context.Context, media.Reference) (media.Metadata, error)

func (p accountReferenceProvider) Fetch(ctx context.Context, ref media.Reference) (media.Metadata, error) {
	return p(ctx, ref)
}

func fixedBilibiliOperations() []Operation {
	return []Operation{
		{ProtocolVersion: 1, OperationType: "SYNC_BIL_SOURCE", Platform: "BILIBILI", AccountRef: pointer("bilibili-main"), Payload: raw(map[string]any{"schemaVersion": 1, "sourceId": "source", "kind": "USER_ADDED", "externalId": "BV1WhJ46vETE", "url": "https://www.bilibili.com/video/BV1WhJ46vETE"})},
		{ProtocolVersion: 1, OperationType: "FETCH_BIL_METADATA", Platform: "BILIBILI", AccountRef: pointer("bilibili-main"), Payload: raw(map[string]any{"schemaVersion": 1, "contentId": "content", "externalId": "BV1WhJ46vETE", "externalIdType": "BVID"})},
	}
}

func TestFixedBilibiliAccountReferenceReachesProvider(t *testing.T) {
	for _, operation := range fixedBilibiliOperations() {
		t.Run(operation.OperationType, func(t *testing.T) {
			h, _, _ := setupHandler(t)
			calls := 0
			h.Provider = accountReferenceProvider(func(_ context.Context, ref media.Reference) (media.Metadata, error) {
				calls++
				if ref.Kind != "BVID" || ref.ID != "BV1WhJ46vETE" {
					t.Fatalf("account routing changed the selected video: %+v", ref)
				}
				metadata := testMetadata("http://127.0.0.1:1")
				metadata.ExternalID = ref.ID
				return metadata, nil
			})
			value, files, err := h.Handle(context.Background(), operation)
			if err != nil || value == nil || len(files) != 0 || calls != 1 {
				t.Fatalf("fixed account did not reach metadata provider: value=%v files=%v calls=%d err=%v", value, files, calls, err)
			}
			if operation.AccountRef == nil || *operation.AccountRef != "bilibili-main" {
				t.Fatal("account reference was cleared during dispatch")
			}
		})
	}
}

func TestBilibiliAccountReferenceRejectsOtherAccountsPlatformsAndOperations(t *testing.T) {
	base := fixedBilibiliOperations()[0]
	for _, test := range []struct{ name, account, platform, operation string }{
		{"other account", "bilibili-secondary", "BILIBILI", "SYNC_BIL_SOURCE"},
		{"wrong platform", "bilibili-main", "TELEGRAM", "SYNC_BIL_SOURCE"},
		{"missing platform", "bilibili-main", "", "SYNC_BIL_SOURCE"},
		{"non Bilibili operation", "bilibili-main", "BILIBILI", "VALIDATE_TARGET"},
		{"unknown operation", "bilibili-main", "BILIBILI", "UNKNOWN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, _, _ := setupHandler(t)
			calls := 0
			h.Provider = accountReferenceProvider(func(context.Context, media.Reference) (media.Metadata, error) {
				calls++
				return media.Metadata{}, nil
			})
			operation := base
			operation.AccountRef, operation.Platform, operation.OperationType = &test.account, test.platform, test.operation
			_, _, err := h.Handle(context.Background(), operation)
			if err == nil || media.Classify(err, "").Code != "ACCOUNT_NOT_CONFIGURED" || calls != 0 {
				t.Fatalf("invalid account route reached provider: calls=%d err=%v", calls, err)
			}
		})
	}
}

type unavailableBilibiliSession struct{ calls int }

func (s *unavailableBilibiliSession) Bind(context.Context) (*http.Client, string, error) {
	s.calls++
	return nil, "", media.Fail("AUTH_REQUIRED", "BILIBILI_SESSION", false)
}

func TestFixedBilibiliAccountReferenceStillRequiresNativeSessionBinding(t *testing.T) {
	for _, operation := range fixedBilibiliOperations() {
		t.Run(operation.OperationType, func(t *testing.T) {
			h, _, _ := setupHandler(t)
			session := &unavailableBilibiliSession{}
			h.Provider = &media.Bilibili{Session: session}
			_, _, err := h.Handle(context.Background(), operation)
			if err == nil || media.Classify(err, "").Code != "AUTH_REQUIRED" || session.calls != 1 {
				t.Fatalf("native session binding was bypassed: calls=%d err=%v", session.calls, err)
			}
		})
	}
}

func TestBilibiliAccountReferenceKeepsEmptyFixtureCompatibility(t *testing.T) {
	empty := ""
	for _, account := range []*string{nil, &empty} {
		h, _, _ := setupHandler(t)
		h.Provider = FixtureProvider{"BV1WhJ46vETE": testMetadata("http://127.0.0.1:1")}
		operation := fixedBilibiliOperations()[0]
		operation.AccountRef = account
		if _, _, err := h.Handle(context.Background(), operation); err != nil {
			t.Fatalf("empty account reference broke isolated fixture: %v", err)
		}
	}
}

func TestFrozenSelectionTargetBindingAndNoExtraIO(t *testing.T) {
	h, root, _ := setupHandler(t)
	var en, zh atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/en" {
			en.Add(1)
		} else {
			zh.Add(1)
		}
		fmt.Fprint(w, `{"body":[{"from":0,"to":1,"content":"subtitle"}]}`)
	}))
	defer server.Close()
	meta := testMetadata(server.URL)
	h.Provider = FixtureProvider{"BVfixture": meta}
	h.Downloads = media.Downloader{Client: server.Client()}
	content, err := h.fetch(context.Background(), media.Reference{Kind: "BVID", ID: "BVfixture"}, "content")
	if err != nil {
		t.Fatal(err)
	}
	if len(content.MediaUnits[0].Tracks) != 2 || len(content.Warnings) != 2 || content.MediaUnits[0].Tracks[0].Size != nil {
		t.Fatal("public metadata exposes AI/unknown or invented size")
	}
	bytes, _ := json.Marshal(content)
	if strings.Contains(string(bytes), server.URL) {
		t.Fatal("private media URL leaked")
	}
	selected := content.MediaUnits[0]
	selected.Tracks = selected.Tracks[:1]
	payload := DownloadPayload{SchemaVersion: 1, ContentID: content.ID, ContentRevision: content.Revision, ContentKind: "SINGLE", ExternalID: "BVfixture", ExternalIDType: "BVID", QualityPolicy: "HIGHEST_AVAILABLE", ArchivePolicy: "NONE", Units: []Unit{selected}, Outputs: []Output{{ID: "out", RelativePath: "Bilibili/用户添加/标题(BVfixture)/en.json", TrackIDs: []string{"sub-en"}, Kind: "SUBTITLE"}}}
	op := Operation{OperationID: "order", ResourceKey: "frozen", Platform: "BILIBILI", AccountRef: pointer("bilibili-main"), Destination: &Destination{RootID: "default", RootPath: root, RelativePath: "Bilibili/用户添加/标题(BVfixture)"}, OperationType: "DOWNLOAD_MEDIA", Payload: raw(payload)}
	_, files, err := h.Handle(context.Background(), op)
	if err != nil || len(files) != 1 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if en.Load() != 1 || zh.Load() != 0 {
		t.Fatal("selection expanded")
	}
	if _, recovered, complete, err := h.Recover(op); err != nil || !complete || len(recovered) != 1 {
		t.Fatalf("publication recovery: %v %v %v", recovered, complete, err)
	}
	// A metadata refresh adds a new track. The old snapshot still selects only en.
	meta.Units[0].Tracks = append(meta.Units[0].Tracks, media.Track{ID: "new", Kind: "SUBTITLE", SubtitleType: "HUMAN", Availability: "AVAILABLE", Source: media.Source{URL: server.URL + "/new", Version: "s3"}})
	h.Provider = FixtureProvider{"BVfixture": meta}
	next, err := h.fetch(context.Background(), media.Reference{Kind: "BVID", ID: "BVfixture"}, "content")
	if err != nil || next.Revision == content.Revision {
		t.Fatal("revision failed to change")
	}
	if _, files, err := h.Handle(context.Background(), op); err != nil || len(files) != 1 {
		t.Fatal(err)
	}
	if en.Load() != 1 || zh.Load() != 0 {
		t.Fatal("replay accessed platform")
	}
	custom := filepath.Join(root, "custom")
	if err := os.Mkdir(custom, 0700); err != nil {
		t.Fatal(err)
	}
	targetOp := Operation{OperationType: "VALIDATE_TARGET", Payload: raw(map[string]any{"schemaVersion": 1, "targetId": "target", "path": custom})}
	target, _, err := h.Handle(context.Background(), targetOp)
	if err != nil {
		t.Fatal(err)
	}
	op.OperationID = "custom-order"
	op.ResourceKey = "custom-frozen"
	op.Destination = &Destination{RootID: target.(map[string]any)["rootId"].(string), RootPath: custom, RelativePath: op.Destination.RelativePath}
	if _, _, err := h.Handle(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if en.Load() != 2 {
		t.Fatal("custom target incorrectly reused default artifact")
	}
	if _, err := os.Stat(filepath.Join(custom, filepath.FromSlash(payload.Outputs[0].RelativePath))); err != nil {
		t.Fatal(err)
	}
	op.Destination.RootID = "default"
	if _, _, err := h.Handle(context.Background(), op); media.Classify(err, "").Code != "TARGET_ID_MISMATCH" {
		t.Fatal(err)
	}
	op.Destination.RootID = h.rootID(custom)
	payload.Units[0].Tracks = []Track{}
	op.Payload = raw(payload)
	if _, _, err := h.Handle(context.Background(), op); media.Classify(err, "").Code != "EMPTY_SELECTION" {
		t.Fatal("empty selection became default", err)
	}
}
func TestRootValidationAndPlanMismatch(t *testing.T) {
	h, root, _ := setupHandler(t)
	for _, path := range []string{filepath.Join(root, "absent"), t.TempDir(), "relative", `\\server\share`} {
		if _, err := h.canonicalRoot(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.canonicalRoot(file); err == nil {
		t.Fatal("accepted file as directory")
	}
}
func TestMovieLanguageAndSeasonIdentityMetadata(t *testing.T) {
	h, _, _ := setupHandler(t)
	meta := testMetadata("http://127.0.0.1:1")
	meta.Kind = "MOVIE"
	meta.IdentifierType = "SEASON_ID"
	meta.ExternalID = "1"
	meta.Units = []media.Unit{{ID: "zh", ExternalID: "video1", Index: 1, Kind: "LANGUAGE", Language: "zh", LanguageName: "普通话", Tracks: []media.Track{{ID: "zh-video", Kind: "VIDEO", SubtitleType: "NOT_APPLICABLE", Availability: "AVAILABLE", Source: media.Source{Version: "shared-video"}}, {ID: "zh-audio", Kind: "AUDIO", Language: "zh", SubtitleType: "NOT_APPLICABLE", Availability: "AVAILABLE"}}}, {ID: "en", ExternalID: "video1", Index: 2, Kind: "LANGUAGE", Language: "en", LanguageName: "英语", Tracks: []media.Track{{ID: "en-video", Kind: "VIDEO", SubtitleType: "NOT_APPLICABLE", Availability: "AVAILABLE", Source: media.Source{Version: "shared-video"}}, {ID: "en-audio", Kind: "AUDIO", Language: "en", SubtitleType: "NOT_APPLICABLE", Availability: "AVAILABLE"}}}}
	content, err := h.saveMetadata(meta, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if content.MediaUnits[0].Kind != "LANGUAGE" || *content.MediaUnits[0].LanguageLabel != "普通话" || content.MediaUnits[0].ExternalID != content.MediaUnits[1].ExternalID {
		t.Fatal("movie audio relation changed into fake P")
	}
	meta.Kind = "SERIES"
	meta.ID = "season2"
	meta.ExternalID = "2"
	season, err := h.saveMetadata(meta, "")
	if err != nil {
		t.Fatal(err)
	}
	if season.ID == content.ID || *season.SeasonID == *content.SeasonID {
		t.Fatal("season identity merged")
	}
}
