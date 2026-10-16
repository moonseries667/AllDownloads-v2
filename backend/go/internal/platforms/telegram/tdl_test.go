package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type tdlTestMessage struct {
	id      int64
	date    int64
	text    string
	file    string
	groupID int64
	media   json.RawMessage
}

type tdlDownloadInput struct {
	ID       int64 `json:"id"`
	Messages []struct {
		ID   int    `json:"id"`
		File string `json:"file"`
	} `json:"messages"`
}

type tdlCall struct {
	args []string
}

type tdlDownloadCall struct {
	args  []string
	input tdlDownloadInput
	dir   string
}

type tdlRunStub struct {
	t                   *testing.T
	executable          string
	peerID              int64
	messages            []tdlTestMessage
	calls               []tdlCall
	downloads           []tdlDownloadCall
	versionOutput       string
	exportError         error
	badExport           []byte
	downloadError       error
	downloadBytes       []byte
	afterDownload       func()
	writeNonPayloadFile bool
}

func (s *tdlRunStub) Run(ctx context.Context, executable string, timeout time.Duration, args ...string) ([]byte, error) {
	s.t.Helper()
	if executable != s.executable {
		s.t.Fatalf("executable = %q, want %q", executable, s.executable)
	}
	if timeout <= 0 {
		s.t.Fatal("command timeout must be positive")
	}
	call := append([]string{}, args...)
	s.calls = append(s.calls, tdlCall{args: call})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 1 && args[0] == "version" {
		return []byte(s.versionOutput), nil
	}
	if hasArgs(args, "chat", "export") {
		if s.exportError != nil {
			return nil, s.exportError
		}
		path := argValue(args, "--output")
		if path == "" {
			s.t.Fatal("chat export did not provide --output")
		}
		var raw []byte
		if s.badExport != nil {
			raw = append([]byte{}, s.badExport...)
		} else {
			bounds := strings.Split(argValue(args, "--input"), ",")
			if len(bounds) != 2 {
				s.t.Fatalf("chat export --input = %q", argValue(args, "--input"))
			}
			from, errFrom := strconv.ParseInt(bounds[0], 10, 64)
			to, errTo := strconv.ParseInt(bounds[1], 10, 64)
			if errFrom != nil || errTo != nil {
				s.t.Fatalf("invalid chat export range %q", argValue(args, "--input"))
			}
			messages := []map[string]any{}
			for _, message := range s.messages {
				if message.id < from || message.id > to {
					continue
				}
				row := map[string]any{"id": message.id, "file": message.file, "date": message.date, "text": message.text}
				rawMessage := map[string]any{"ID": message.id, "Date": message.date, "Message": message.text}
				if message.groupID != 0 {
					rawMessage["GroupedID"] = message.groupID
				}
				if len(message.media) > 0 {
					rawMessage["Media"] = message.media
				}
				row["raw"] = rawMessage
				messages = append(messages, row)
			}
			var err error
			raw, err = json.Marshal(map[string]any{"id": s.peerID, "messages": messages})
			if err != nil {
				s.t.Fatal(err)
			}
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			s.t.Fatal(err)
		}
		return nil, nil
	}
	if hasArgs(args, "download") {
		if s.downloadError != nil {
			return nil, s.downloadError
		}
		inputPath, dir := argValue(args, "--file"), argValue(args, "--dir")
		if inputPath == "" || dir == "" {
			s.t.Fatal("download did not provide --file and --dir")
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			s.t.Fatal(err)
		}
		var input tdlDownloadInput
		if err := json.Unmarshal(data, &input); err != nil {
			s.t.Fatal(err)
		}
		s.downloads = append(s.downloads, tdlDownloadCall{args: call, input: input, dir: dir})
		if err := os.WriteFile(filepath.Join(dir, "payload"), s.downloadBytes, 0600); err != nil {
			s.t.Fatal(err)
		}
		if s.writeNonPayloadFile {
			if err := os.WriteFile(filepath.Join(dir, "input.json"), data, 0600); err != nil {
				s.t.Fatal(err)
			}
		}
		if s.afterDownload != nil {
			s.afterDownload()
		}
		return nil, nil
	}
	s.t.Fatalf("unexpected tdl invocation: %q", args)
	return nil, errors.New("unexpected invocation")
}

func newTDLTest(t *testing.T, messages []tdlTestMessage) (*TDL, *tdlRunStub, string) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	state, err := executor.OpenState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0700); err != nil {
		t.Fatal(err)
	}
	storageDir := filepath.Join(root, "storage")
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "tdl.exe")
	stub := &tdlRunStub{t: t, executable: executable, peerID: 777, messages: append([]tdlTestMessage{}, messages...), versionOutput: "tdl Version: " + TDLVersion, downloadBytes: []byte("media")}
	tdl := &TDL{
		Executable: executable,
		State:      state,
		WorkDir:    workDir,
		Accounts: map[string]TDLAccount{
			"saved": {Namespace: "saved-test", StorageDir: storageDir, PeerID: "777"},
			"other": {Namespace: "other-test", StorageDir: storageDir, PeerID: "777"},
		},
		Timeout: time.Minute,
		Run:     stub.Run,
	}
	return tdl, stub, stateDir
}

func tdlMessage(id int64, file string, group int64, media string) tdlTestMessage {
	return tdlTestMessage{id: id, date: 1710000000 + id, text: "saved message", file: file, groupID: group, media: json.RawMessage(media)}
}

func TestTDLCheckUsesOnlyLocalVersionCommand(t *testing.T) {
	tdl, stub, _ := newTDLTest(t, nil)
	if err := os.Remove(tdl.WorkDir); err != nil {
		t.Fatal(err)
	}
	account := tdl.Accounts["saved"]
	account.StorageDir = filepath.Join(t.TempDir(), "not-created")
	tdl.Accounts["saved"] = account

	if err := tdl.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.calls) != 1 || len(stub.calls[0].args) != 1 || stub.calls[0].args[0] != "version" {
		t.Fatalf("Check accessed more than local version: %+v", stub.calls)
	}
	if _, err := os.Stat(account.StorageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Check touched account storage: %v", err)
	}
	if _, err := os.Stat(tdl.WorkDir); err != nil {
		t.Fatalf("Check did not create isolated work directory: %v", err)
	}

	stub.calls = nil
	stub.versionOutput = "Version: 0.20.3"
	err := tdl.Check(context.Background())
	assertTDLFault(t, err, "TOOL_VERSION_MISMATCH")
	if len(stub.calls) != 1 || stub.calls[0].args[0] != "version" {
		t.Fatalf("version check made unexpected calls: %+v", stub.calls)
	}
}

func TestTDLSavedPageExportsAllMediaPagesAt200AndSurvivesStateRestart(t *testing.T) {
	messages := make([]tdlTestMessage, 0, 201)
	messages = append(messages,
		tdlMessage(1, "", 0, ""),
		tdlMessage(2, "photo.jpg", 0, `{"Photo":{"ID":202,"Sizes":[{"Type":"x","Size":50}]}}`),
		tdlMessage(3, "looks-like-video.mp4", 0, `{"Document":{"ID":303,"Size":9,"MimeType":"application/pdf","Attributes":[{"FileName":"looks-like-video.mp4"}]}}`),
		tdlMessage(4, "clip.mp4", 0, `{"Document":{"ID":404,"Size":12,"MimeType":"video/mp4","Attributes":[{"Duration":1.5,"W":640,"H":360,"SupportsStreaming":true}]}}`),
	)
	for id := int64(5); id <= 201; id++ {
		messages = append(messages, tdlMessage(id, "", 0, ""))
	}
	tdl, stub, _ := newTDLTest(t, messages)

	first, err := tdl.SavedPage(context.Background(), "saved", "0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.Next == nil || len(first.Messages) != 200 || first.Through != "200" {
		t.Fatalf("unexpected first export page: complete=%v next=%v count=%d through=%q", first.Complete, first.Next, len(first.Messages), first.Through)
	}
	if len(first.Messages[0].Media) != 0 || Classify(first.Messages[1].Media[0]) != "PHOTO" || Classify(first.Messages[2].Media[0]) != "DOCUMENT" || Classify(first.Messages[3].Media[0]) != "VIDEO" {
		t.Fatalf("export omitted or misclassified message types: %+v", first.Messages[:4])
	}
	assertTDLExportArgs(t, stub.calls[0].args, "1", "2147483647")

	stateDir := filepath.Join(filepath.Dir(tdl.WorkDir), "state")
	if err := tdl.State.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := executor.OpenState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	tdl.State = reopened

	second, err := tdl.SavedPage(context.Background(), "saved", "0", first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Complete || second.Next != nil || len(second.Messages) != 1 || second.Messages[0].ID != "201" || second.Through != "201" {
		t.Fatalf("unexpected second export page: %+v", second)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("continuation re-exported history instead of using its snapshot: %+v", stub.calls)
	}
	if !hasArgs(stub.calls[0].args, "--all", "--raw", "--with-content") {
		t.Fatalf("saved export did not request all raw content: %q", stub.calls[0].args)
	}
}

func TestTDLContinuationCannotCrossAccountOrSnapshot(t *testing.T) {
	messages := make([]tdlTestMessage, 201)
	for i := range messages {
		messages[i] = tdlMessage(int64(i+1), "", 0, "")
	}
	tdl, stub, _ := newTDLTest(t, messages)
	first, err := tdl.SavedPage(context.Background(), "saved", "0", nil)
	if err != nil || first.Next == nil {
		t.Fatalf("first page: page=%+v err=%v", first, err)
	}
	if _, err := tdl.SavedPage(context.Background(), "other", "0", first.Next); err == nil {
		t.Fatal("continuation was accepted for another account")
	} else {
		assertTDLFault(t, err, "VERSION_CHANGED")
	}

	// Starting another snapshot for the same account replaces the durable
	// snapshot; a cursor from the previous export must no longer be accepted.
	if _, err := tdl.SavedPage(context.Background(), "saved", "1", nil); err != nil {
		t.Fatal(err)
	}
	callsBefore := len(stub.calls)
	if _, err := tdl.SavedPage(context.Background(), "saved", "0", first.Next); err == nil {
		t.Fatal("continuation from a replaced snapshot was accepted")
	} else {
		assertTDLFault(t, err, "VERSION_CHANGED")
	}
	if len(stub.calls) != callsBefore {
		t.Fatalf("rejected continuation performed platform I/O: calls grew from %d to %d", callsBefore, len(stub.calls))
	}
}

func TestTDLIncrementalGroupUsesPriorIndexAndRepairsMissingIndexReadOnly(t *testing.T) {
	oldPhoto := tdlMessage(2, "old.jpg", 900, `{"Photo":{"ID":902,"Sizes":[{"Type":"x","Size":51}]}}`)
	oldPhoto.date = 1710000100
	boundary := tdlMessage(3, "", 0, "")
	tdl, stub, stateDir := newTDLTest(t, []tdlTestMessage{tdlMessage(1, "", 0, ""), oldPhoto, boundary})
	initial, err := tdl.SavedPage(context.Background(), "saved", "0", nil)
	if err != nil || !initial.Complete || initial.Through != "3" {
		t.Fatalf("initial scan: page=%+v err=%v", initial, err)
	}
	newVideo := tdlMessage(4, "new.mp4", 900, `{"Document":{"ID":904,"Size":12,"MimeType":"video/mp4","Attributes":[{"Duration":2,"W":640,"H":360,"SupportsStreaming":true}]}}`)
	newVideo.date = 1710000200
	stub.messages = append(stub.messages, newVideo)

	incremental, err := tdl.SavedPage(context.Background(), "saved", "3", nil)
	if err != nil || !incremental.Complete || len(incremental.Messages) != 1 || incremental.Messages[0].ID != "4" {
		t.Fatalf("incremental scan: page=%+v err=%v", incremental, err)
	}
	assertTDLExportArgs(t, stub.calls[1].args, "4", "2147483647")
	group, err := tdl.SavedGroup(context.Background(), "saved", "900")
	if err != nil || !group.Complete || len(group.Messages) != 2 {
		t.Fatalf("group did not span the cursor boundary: group=%+v err=%v", group, err)
	}
	ids := map[string]bool{}
	for _, message := range group.Messages {
		ids[message.ID] = true
	}
	if !ids["2"] || !ids["4"] {
		t.Fatalf("group members = %v, want old and new members", ids)
	}

	index := filepath.Join(stateDir, "tel-groups-"+telHash("saved")+".json")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	newer := tdlMessage(5, "newer.mp4", 900, `{"Document":{"ID":905,"Size":13,"MimeType":"video/mp4","Attributes":[{"Duration":2,"W":640,"H":360,"SupportsStreaming":true}]}}`)
	stub.messages = append(stub.messages, newer)
	before := len(stub.calls)
	repaired, err := tdl.SavedPage(context.Background(), "saved", "4", nil)
	if err != nil || !repaired.Complete || len(repaired.Messages) != 1 || repaired.Messages[0].ID != "5" {
		t.Fatalf("index repair scan: page=%+v err=%v", repaired, err)
	}
	assertTDLExportArgs(t, stub.calls[before].args, "1", "2147483647")
	for _, call := range stub.calls[before:] {
		if hasArgs(call.args, "download") {
			t.Fatalf("index repair triggered a download: %q", call.args)
		}
	}
	group, err = tdl.SavedGroup(context.Background(), "saved", "900")
	if err != nil || len(group.Messages) != 3 {
		t.Fatalf("repaired group = %+v, err=%v", group, err)
	}
}

func TestTDLSavedPageMapsRunCancelPeerAndMalformedExport(t *testing.T) {
	t.Run("command failure is sanitized", func(t *testing.T) {
		tdl, stub, _ := newTDLTest(t, []tdlTestMessage{tdlMessage(1, "", 0, "")})
		stub.exportError = errors.New("private session detail")
		_, err := tdl.SavedPage(context.Background(), "saved", "0", nil)
		assertTDLFault(t, err, "AUTH_OR_TOOL_REQUIRED")
		if strings.Contains(err.Error(), "private session detail") {
			t.Fatalf("command error leaked: %v", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		tdl, _, _ := newTDLTest(t, []tdlTestMessage{tdlMessage(1, "", 0, "")})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := tdl.SavedPage(ctx, "saved", "0", nil)
		assertTDLFault(t, err, "CANCELLED")
	})
	t.Run("peer identity changed", func(t *testing.T) {
		tdl, stub, _ := newTDLTest(t, []tdlTestMessage{tdlMessage(1, "", 0, "")})
		stub.peerID = 778
		_, err := tdl.SavedPage(context.Background(), "saved", "0", nil)
		assertTDLFault(t, err, "ACCOUNT_IDENTITY_CHANGED")
	})
	t.Run("malformed export", func(t *testing.T) {
		tdl, stub, _ := newTDLTest(t, nil)
		stub.badExport = []byte(`{"id":777,"messages":[`)
		_, err := tdl.SavedPage(context.Background(), "saved", "0", nil)
		assertTDLFault(t, err, "PROVIDER_CHANGED")
	})
}

func TestTDLOpenAttachmentScopesOneMessageAndCleansTemporaryOutput(t *testing.T) {
	media := json.RawMessage(`{"Document":{"ID":501,"Size":5,"MimeType":"application/pdf","Attributes":[{"FileName":"clip.mp4"}]}}`)
	tdl, stub, _ := newTDLTest(t, []tdlTestMessage{tdlMessage(50, "clip.mp4", 0, string(media))})
	stub.downloadBytes = []byte("12345")
	want := DownloadAttachment{Attachment: Attachment{ID: "attachment", ExternalID: "document:501:5", Name: "clip.mp4", MediaType: "DOCUMENT", MIMEType: strPtr("application/pdf"), SizeBytes: strPtr("5")}, MessageID: "50"}
	reader, err := tdl.OpenAttachment(context.Background(), "saved", []string{"50", "51", "52"}, want)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = reader.Close()
		}
	})
	if len(stub.downloads) != 1 {
		t.Fatalf("download calls = %d, want one", len(stub.downloads))
	}
	call := stub.downloads[0]
	if call.input.ID != 777 || len(call.input.Messages) != 1 || call.input.Messages[0].ID != 50 || call.input.Messages[0].File != "clip.mp4" {
		t.Fatalf("download input expanded the frozen message: %+v", call.input)
	}
	if hasArg(call.args, "--group") || !hasArg(call.args, "download") || !hasArg(call.args, "--file") || !hasArg(call.args, "--dir") || !hasArgs(call.args, "--template", "payload") || !hasArg(call.args, "--continue") {
		t.Fatalf("download command was not one-message scoped: %q", call.args)
	}
	if !hasArgs(stub.calls[0].args, "chat", "export") || !hasArgs(stub.calls[0].args, "--type", "id") || argValue(stub.calls[0].args, "--input") != "50,50" || len(stub.calls) != 3 {
		t.Fatalf("expected pre- and post-download frozen-fact exports: %+v", stub.calls)
	}
	if _, err := os.Stat(call.dir); err != nil {
		t.Fatalf("temporary output missing while reader is open: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "12345" {
		t.Fatalf("download stream = %q, err=%v", data, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if _, err := os.Stat(call.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary output survived Close: %v", err)
	}
}

func TestTDLOpenAttachmentRejectsChangedFrozenFactsAndExtensionGuess(t *testing.T) {
	base := DownloadAttachment{Attachment: Attachment{ID: "a", ExternalID: "document:601:5", Name: "looks-like-video.mp4", MediaType: "DOCUMENT", MIMEType: strPtr("application/pdf"), SizeBytes: strPtr("5")}, MessageID: "60"}
	cases := map[string]func(*DownloadAttachment){
		"external id": func(w *DownloadAttachment) { w.ExternalID = "document:601:6" },
		"name":        func(w *DownloadAttachment) { w.Name = "renamed.pdf" },
		"size":        func(w *DownloadAttachment) { w.SizeBytes = strPtr("4") },
		"extension is not type evidence": func(w *DownloadAttachment) {
			w.MediaType = "VIDEO"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tdl, stub, _ := newTDLTest(t, []tdlTestMessage{tdlMessage(60, "looks-like-video.mp4", 0, `{"Document":{"ID":601,"Size":5,"MimeType":"application/pdf","Attributes":[{"FileName":"looks-like-video.mp4"}]}}`)})
			want := base
			mutate(&want)
			reader, err := tdl.OpenAttachment(context.Background(), "saved", []string{"60"}, want)
			if reader != nil {
				_ = reader.Close()
			}
			assertTDLFault(t, err, "VERSION_CHANGED")
			if len(stub.downloads) != 0 {
				t.Fatalf("changed frozen facts reached download: %+v", stub.downloads)
			}
		})
	}
}

func TestTDLOpenAttachmentRechecksFactsAfterDownload(t *testing.T) {
	document := tdlMessage(70, "media.bin", 0, `{"Document":{"ID":701,"Size":5,"MimeType":"application/octet-stream","Attributes":[{"FileName":"media.bin"}]}}`)
	tdl, stub, _ := newTDLTest(t, []tdlTestMessage{document})
	want := DownloadAttachment{Attachment: Attachment{ID: "a", ExternalID: "document:701:5", Name: "media.bin", MediaType: "DOCUMENT", MIMEType: strPtr("application/octet-stream"), SizeBytes: strPtr("5")}, MessageID: "70"}
	stub.downloadBytes = []byte("12345")
	stub.afterDownload = func() {
		stub.messages[0].media = json.RawMessage(`{"Document":{"ID":701,"Size":6,"MimeType":"application/octet-stream","Attributes":[{"FileName":"media.bin"}]}}`)
	}
	reader, err := tdl.OpenAttachment(context.Background(), "saved", []string{"70"}, want)
	if reader != nil {
		t.Fatal("opened attachment after platform facts changed")
	}
	assertTDLFault(t, err, "VERSION_CHANGED")
	if len(stub.downloads) != 1 || len(stub.calls) != 3 {
		t.Fatalf("expected one download between two fact checks: downloads=%d calls=%d", len(stub.downloads), len(stub.calls))
	}
	if _, err := os.Stat(stub.downloads[0].dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed attachment left temporary output: %v", err)
	}
}

func assertTDLExportArgs(t *testing.T, args []string, from, to string) {
	t.Helper()
	if !hasArgs(args, "chat", "export", "--all", "--raw", "--with-content", "--type", "id", "--input", from+","+to, "--output") {
		t.Fatalf("unexpected export range or options: %q", args)
	}
}

func assertTDLFault(t *testing.T, err error, code string) {
	t.Helper()
	var fault *p.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func argValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func hasArgs(args []string, values ...string) bool {
	if len(values) == 0 {
		return true
	}
	for i := 0; i+len(values) <= len(args); i++ {
		match := true
		for j, value := range values {
			if args[i+j] != value {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func strPtr(value string) *string { return &value }
