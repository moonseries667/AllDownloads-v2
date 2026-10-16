package binding

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

func str(v string) *string { return &v }

type fileClient struct {
	calls map[string]int
	fail  string
}

func (c *fileClient) OpenNode(ctx context.Context, account string, n quark.CloudNode) (io.ReadCloser, error) {
	c.calls[n.ID]++
	if n.ID == c.fail {
		return nil, p.NewFault("NETWORK_TEMPORARY", "DOWNLOAD")
	}
	return io.NopCloser(strings.NewReader(n.ID)), nil
}
func setup(t *testing.T) (*Worker, runner.Operation, *fileClient) {
	root := t.TempDir()
	state := t.TempDir()
	c := &fileClient{calls: map[string]int{}}
	w := &Worker{Base: &runner.Handler{StateDir: state, AllowedRoots: []string{root}, Roots: map[string]string{"test-root": root}}, Quark: c}
	req := quark.DownloadRequest{SchemaVersion: 1, ShareID: "share", ArchivePolicy: "NONE"}
	for _, id := range []string{"a", "b"} {
		req.Nodes = append(req.Nodes, quark.CloudNode{ID: id, ExternalID: id, ShareID: "share", Version: "v1", Name: id, Kind: "FILE", Availability: "AVAILABLE"})
		req.Outputs = append(req.Outputs, p.Output{ID: id, NodeID: id, Kind: "FILE", RelativePath: "Quark/share/" + id, TrackIDs: []string{}})
	}
	raw, _ := json.Marshal(req)
	op := runner.Operation{ProtocolVersion: 1, OperationID: "op-test", Platform: "QUARK", OperationType: "DOWNLOAD_MEDIA", ResourceKey: "frozen-share-v1", AccountRef: str("isolated-account"), Destination: &runner.Destination{RootID: "test-root", RootPath: root, RelativePath: "Quark/share"}, Payload: raw}
	return w, op, c
}

func TestSharedPublisherPartialRecoveryReopenAndNoRedownload(t *testing.T) {
	w, op, c := setup(t)
	c.fail = "b"
	_, files, err := w.Handle(context.Background(), op)
	if err == nil || len(files) != 1 {
		t.Fatalf("partial: %v %+v", err, files)
	}
	_, recovered, complete, err := w.Recover(op)
	if err != nil || complete || len(recovered) != 1 {
		t.Fatal(recovered, complete, err)
	}
	// Reopening engines on every Handle/Recover exercises the same persistent
	// receipts as a process restart, with the production filesystem publisher.
	c.fail = ""
	_, files, err = w.Handle(context.Background(), op)
	if err != nil || len(files) != 2 || c.calls["a"] != 1 || c.calls["b"] != 2 {
		t.Fatal(files, c.calls, err)
	}
	_, recovered, complete, err = w.Recover(op)
	if err != nil || !complete || len(recovered) != 2 {
		t.Fatal(recovered, complete, err)
	}
	_, _, err = w.Handle(context.Background(), op)
	if err != nil || c.calls["a"] != 1 || c.calls["b"] != 2 {
		t.Fatal("refetched", c.calls, err)
	}
	if err := os.WriteFile(filepath.Join(op.Destination.RootPath, "Quark/share/a"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = w.Handle(context.Background(), op)
	var f *p.Fault
	if !errors.As(err, &f) || f.Code != "VERIFICATION_FAILED" || c.calls["a"] != 1 {
		t.Fatal("corruption overwritten", err)
	}
}

func TestSharedPublisherRejectsChangedRootAndOutputBeforeSourceIO(t *testing.T) {
	for _, mode := range []string{"root-id", "root-path", "relative"} {
		t.Run(mode, func(t *testing.T) {
			w, op, c := setup(t)
			switch mode {
			case "root-id":
				op.Destination.RootID = "wrong"
			case "root-path":
				op.Destination.RootPath = t.TempDir()
			case "relative":
				op.Destination.RelativePath = "Quark/other"
			}
			_, _, err := w.Handle(context.Background(), op)
			if err == nil || len(c.calls) > 0 {
				t.Fatal("bad target executed", err)
			}
		})
	}
}

type attachmentClient struct {
	data  map[string][]byte
	calls int
}

func (c *attachmentClient) OpenAttachment(ctx context.Context, account string, ids []string, a telegram.DownloadAttachment) (io.ReadCloser, error) {
	c.calls++
	return io.NopCloser(bytes.NewReader(c.data[a.ID])), nil
}
func wav() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+16000))
	b.WriteString("WAVEfmt ")
	for _, n := range []any{uint32(16), uint16(1), uint16(1), uint32(8000), uint32(16000), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, n)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	b.Write(make([]byte, 16000))
	return b.Bytes()
}

func TestRealPhotoAudioAndMetadataUseSharedPublication(t *testing.T) {
	toolDir := os.Getenv("ALLDOWNLOADS_TEST_FFMPEG_DIR")
	if toolDir == "" {
		t.Skip("set ALLDOWNLOADS_TEST_FFMPEG_DIR for real media verification")
	}
	w, op, _ := setup(t)
	w.Base.Tools = &media.Tools{FFmpeg: filepath.Join(toolDir, "ffmpeg.exe"), FFprobe: filepath.Join(toolDir, "ffprobe.exe")}
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	c := &attachmentClient{data: map[string][]byte{"photo": photo.Bytes(), "voice": wav(), "doc": []byte("document")}}
	w.Telegram = c
	req := telegram.DownloadRequest{SchemaVersion: 1, AccountID: "test", ResourceID: "album", MessageIDs: []string{"1", "2", "3"}, ArchivePolicy: "AUTO", Metadata: map[string]any{"messages": []any{map[string]any{"externalId": "1", "text": "完整正文\n第二行", "sentAt": "2026-10-15T00:00:00Z", "channel": nil, "authorTags": []string{}, "sourceLinks": []string{}}}}}
	for _, item := range []struct{ id, kind, msg string }{{"photo", "PHOTO", "1"}, {"voice", "AUDIO", "2"}, {"doc", "DOCUMENT", "3"}} {
		req.Attachments = append(req.Attachments, telegram.DownloadAttachment{Attachment: telegram.Attachment{ID: item.id, ExternalID: item.id, Name: item.id, MediaType: item.kind}, MessageID: item.msg})
		path := "Telegram/2026.10.15-08.00.00/1/" + item.id
		req.Outputs = append(req.Outputs, p.Output{ID: item.id, AttachmentID: item.id, RelativePath: path, Kind: item.kind, TrackIDs: []string{}}, p.Output{ID: item.id + "-metadata", AttachmentID: item.id, RelativePath: path + ".metadata.json", Kind: "METADATA", TrackIDs: []string{}})
	}
	op.Platform = "TELEGRAM"
	op.OperationID = "op-album"
	op.ResourceKey = "frozen-album"
	op.Destination.RelativePath = "Telegram/2026.10.15-08.00.00/1"
	op.Payload, _ = json.Marshal(req)
	_, files, err := w.Handle(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 || c.calls != 3 {
		t.Fatal(files, c.calls)
	}
	data, err := os.ReadFile(filepath.Join(op.Destination.RootPath, "Telegram/2026.10.15-08.00.00/1/photo.metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err = json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["message_preview"].(map[string]any)["text"] != "完整正文\n第二行" || meta["size"] == nil {
		t.Fatal(string(data))
	}
	_, _, complete, err := w.Recover(op)
	if err != nil || !complete {
		t.Fatal(complete, err)
	}
	_, _, err = w.Handle(context.Background(), op)
	if err != nil || c.calls != 3 {
		t.Fatal("refetch on restart", c.calls, err)
	}
}

func TestSharedPublisherCancellationClosesBlockedSource(t *testing.T) {
	w, op, _ := setup(t)
	pub, err := w.open(context.Background(), op, []p.Output{{ID: "blocked", RelativePath: "Quark/share/blocked", Kind: "FILE"}})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Files.Close()
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	_, err = pub.Ensure(ctx, "frozen", p.Output{ID: "blocked", RelativePath: "Quark/share/blocked", Kind: "FILE"}, func(context.Context) (io.ReadCloser, error) { cancel(); return reader, nil })
	var f *p.Fault
	if !errors.As(err, &f) || f.Status != "CANCELLED" {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(op.Destination.RootPath, "Quark/share/blocked")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel published", err)
	}
}

func TestTelegramKeepsAdditionalStreamsInOriginalAttachment(t *testing.T) {
	toolDir := os.Getenv("ALLDOWNLOADS_TEST_FFMPEG_DIR")
	if toolDir == "" {
		t.Skip("requires FFmpeg 9.0.2")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "two-audio.mka")
	cmd := exec.CommandContext(ctx, filepath.Join(toolDir, "ffmpeg.exe"), "-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-map", "0:a", "-map", "0:a", "-t", "0.1", "-c:a", "pcm_s16le", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic fixture: %v %s", err, out)
	}
	tools := &media.Tools{FFmpeg: filepath.Join(toolDir, "ffmpeg.exe"), FFprobe: filepath.Join(toolDir, "ffprobe.exe")}
	if _, err := tools.Inspect(ctx, path, []string{"audio"}); err == nil {
		t.Fatal("first-part exact selection behavior changed")
	}
	probe, err := tools.InspectContaining(ctx, path, []string{"audio"})
	if err != nil || len(probe.Streams) != 2 {
		t.Fatal(probe, err)
	}
	if _, err := tools.InspectContaining(ctx, path, []string{"video"}); err == nil {
		t.Fatal("audio file accepted as video")
	}
}
