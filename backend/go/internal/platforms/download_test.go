package platforms_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
)

// This is a local publisher substitute, not a second production receipt store.
// Production recovery is supplied by part one's shared publication core.
type testPublisher struct {
	root  string
	keys  map[string]string
	reads map[string]int
}

func (s *testPublisher) Ensure(ctx context.Context, key string, o p.Output, open p.OpenSource) (p.FileEvidence, error) {
	name := filepath.Join(s.root, filepath.FromSlash(o.RelativePath))
	data, err := os.ReadFile(name)
	if err == nil && s.keys[o.ID] != key {
		return p.FileEvidence{}, p.NewFault("VERSION_CHANGED", "PUBLISH")
	}
	if errors.Is(err, os.ErrNotExist) {
		s.reads[o.ID]++
		r, e := open(ctx)
		if e != nil {
			return p.FileEvidence{}, e
		}
		data, e = io.ReadAll(r)
		closeErr := r.Close()
		if e != nil {
			return p.FileEvidence{}, e
		}
		if closeErr != nil {
			return p.FileEvidence{}, closeErr
		}
		if err := ctx.Err(); err != nil {
			return p.FileEvidence{}, err
		}
		if e = os.MkdirAll(filepath.Dir(name), 0700); e != nil {
			return p.FileEvidence{}, e
		}
		if e = os.WriteFile(name, data, 0600); e != nil {
			return p.FileEvidence{}, e
		}
		s.keys[o.ID] = key
	} else if err != nil {
		return p.FileEvidence{}, err
	}
	h := sha256.Sum256(data)
	return p.FileEvidence{OutputID: o.ID, RelativePath: o.RelativePath, SizeBytes: strconv.Itoa(len(data)), SHA256: hex.EncodeToString(h[:]), MediaKinds: []string{o.Kind}, Verified: true}, nil
}
func newPublisher(t *testing.T) *testPublisher {
	return &testPublisher{root: t.TempDir(), keys: map[string]string{}, reads: map[string]int{}}
}

type telBytes struct {
	fail   string
	calls  []string
	cancel context.CancelFunc
}

func (c *telBytes) OpenAttachment(ctx context.Context, a string, ids []string, item telegram.DownloadAttachment) (io.ReadCloser, error) {
	c.calls = append(c.calls, item.ID)
	if c.cancel != nil {
		c.cancel()
	}
	if item.ID == c.fail {
		return nil, errors.New("secret raw provider URL must never appear")
	}
	return io.NopCloser(strings.NewReader(item.ID)), nil
}
func telPlan() telegram.DownloadRequest {
	attachments := []telegram.DownloadAttachment{}
	outputs := []p.Output{}
	for _, item := range []struct{ id, kind string }{{"a-photo", "PHOTO"}, {"b-video", "VIDEO"}, {"c-document", "DOCUMENT"}, {"d-unknown", "UNKNOWN"}} {
		attachments = append(attachments, telegram.DownloadAttachment{Attachment: telegram.Attachment{ID: item.id, ExternalID: "ext-" + item.id, Name: item.id, MediaType: item.kind}, MessageID: "1"})
		kind := item.kind
		if kind == "UNKNOWN" {
			kind = "DOCUMENT"
		}
		outputs = append(outputs, p.Output{ID: item.id, RelativePath: "Telegram/batch/1/" + item.id, Kind: kind, AttachmentID: item.id, TrackIDs: []string{}}, p.Output{ID: item.id + "-meta", RelativePath: "Telegram/batch/1/" + item.id + ".metadata.json", Kind: "METADATA", AttachmentID: item.id, TrackIDs: []string{}})
	}
	return telegram.DownloadRequest{SchemaVersion: 1, AccountID: "test", ResourceID: "resource", MessageIDs: []string{"1"}, Attachments: attachments, Outputs: outputs, ArchivePolicy: "AUTO", Metadata: map[string]any{"message_preview": map[string]any{"text": "正文"}, "source_links": []string{}, "author_tags": []string{}, "channel": nil}}
}

func TestTelegramFrozenAlbumPartialFailureRecoveryAndSidecars(t *testing.T) {
	plan := telPlan()
	publisher := newPublisher(t)
	client := &telBytes{fail: "b-video"}
	res, err := telegram.Download(context.Background(), client, publisher, plan)
	var fault *p.Fault
	if !errors.As(err, &fault) || fault.Code != "NETWORK_TEMPORARY" || len(res.Files) != 2 || len(fault.CompletedEffects) != 2 || len(fault.PendingEffects) != 6 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("%+v %v", res, err)
	}
	client.fail = ""
	res, err = telegram.Download(context.Background(), client, publisher, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 8 || publisher.reads["a-photo"] != 1 || publisher.reads["b-video"] != 2 {
		t.Fatalf("wrong recovery: %+v %+v", res, publisher.reads)
	}
	data, err := os.ReadFile(filepath.Join(publisher.root, "Telegram/batch/1/a-photo.metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err = json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["size"] != "7" || metadata["message_id"] != "1" || metadata["status"] != "VERIFIED" || metadata["message_preview"].(map[string]any)["text"] != "正文" {
		t.Fatal(string(data))
	}
	before := len(client.calls)
	_, err = telegram.Download(context.Background(), client, publisher, plan)
	if err != nil || len(client.calls) != before {
		t.Fatal("duplicate fetched bytes", err)
	}
}

func TestTelegramRejectsMissingSidecarAndCancellationKeepsPartialManifest(t *testing.T) {
	plan := telPlan()
	plan.Outputs = plan.Outputs[:len(plan.Outputs)-1]
	c := &telBytes{}
	_, err := telegram.Download(context.Background(), c, newPublisher(t), plan)
	if err == nil || len(c.calls) > 0 {
		t.Fatal("incomplete plan executed")
	}
	plan = telPlan()
	ctx, cancel := context.WithCancel(context.Background())
	c = &telBytes{cancel: cancel}
	res, err := telegram.Download(ctx, c, newPublisher(t), plan)
	var fault *p.Fault
	if !errors.As(err, &fault) || fault.Status != "CANCELLED" || len(res.Files) != 0 || len(c.calls) != 1 {
		t.Fatal(res, err)
	}
}

type quarkBytes struct {
	calls   []quark.CloudNode
	version string
}

func (c *quarkBytes) OpenNode(ctx context.Context, a string, n quark.CloudNode) (io.ReadCloser, error) {
	c.calls = append(c.calls, n)
	if n.Version != c.version {
		return nil, p.NewFault("VERSION_CHANGED", "DOWNLOAD")
	}
	return io.NopCloser(strings.NewReader("file")), nil
}
func quarkPlan() quark.DownloadRequest {
	return quark.DownloadRequest{SchemaVersion: 1, ShareID: "managed", ArchivePolicy: "NONE", Nodes: []quark.CloudNode{{ID: "f", ExternalID: "external-f", Version: "v1", Name: "file", Kind: "FILE", ShareID: "managed", Availability: "AVAILABLE", SizeBytes: ptr("4")}}, Outputs: []p.Output{{ID: "out", NodeID: "f", RelativePath: "Quark/12345678/folder/file", Kind: "FILE", SizeBytes: ptr("4"), TrackIDs: []string{}}}}
}
func TestQuarkFrozenVersionAndBatchTargetDoNotUseCloudFolderAsLocalPath(t *testing.T) {
	plan := quarkPlan()
	c := &quarkBytes{version: "v1"}
	one, two := newPublisher(t), newPublisher(t)
	for _, target := range []*testPublisher{one, two} {
		res, err := quark.Download(context.Background(), c, target, "session-ref", plan)
		if err != nil || len(res.Files) != 1 {
			t.Fatal(res, err)
		}
		if _, err := os.Stat(filepath.Join(target.root, "Quark/12345678/folder/file")); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.calls) != 2 || plan.ArchivePolicy != "NONE" {
		t.Fatal("batch root or policy changed")
	}
	c.version = "v2"
	_, err := quark.Download(context.Background(), c, newPublisher(t), "session-ref", plan)
	var f *p.Fault
	if !errors.As(err, &f) || f.Code != "VERSION_CHANGED" {
		t.Fatal(err)
	}
	plan = quarkPlan()
	plan.Nodes = append(plan.Nodes, quark.CloudNode{ID: "new"})
	before := len(c.calls)
	_, err = quark.Download(context.Background(), c, newPublisher(t), "session-ref", plan)
	if err == nil || len(c.calls) != before {
		t.Fatal("expanded frozen selection")
	}
}

func TestPublisherCannotClaimEvidenceForOtherFile(t *testing.T) {
	out := p.Output{ID: "x", Kind: "PHOTO", RelativePath: "Telegram/x"}
	if err := p.CheckEvidence(out, p.FileEvidence{OutputID: "other", RelativePath: out.RelativePath, SizeBytes: "0", SHA256: strings.Repeat("0", 64), Verified: true, MediaKinds: []string{"PHOTO"}}); err == nil {
		t.Fatal("wrong output evidence accepted")
	}
	for _, path := range []string{"../outside", "C:/outside", ".work/file", "a\\b", "a/../b"} {
		out.RelativePath = path
		if err := p.ValidateOutputs([]p.Output{out}); err == nil {
			t.Fatal(path)
		}
	}
}
