package quark

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

const transferTestShareID = "sharecode"

type nativeTransferFixture struct {
	t  *testing.T
	mu sync.Mutex

	server    *httptest.Server
	serverURL string
	stateDir  string
	state     *executor.State
	client    *NativeClient
	transfers *Transfers

	uid          string
	shareEntries map[string]shareEntry
	files        map[string]shareEntry
	sourceBytes  map[string][]byte
	tasks        map[string]string
	contents     map[string][]byte

	nextID          int
	nextTask        int
	saveCalls       int
	tokenCalls      int
	downloadCalls   int
	nextSaveFailure string
	downloadStatus  int
}

func newNativeTransferFixture(t *testing.T) *nativeTransferFixture {
	t.Helper()
	f := &nativeTransferFixture{
		t:            t,
		uid:          fixtureUID,
		shareEntries: map[string]shareEntry{},
		sourceBytes:  map[string][]byte{},
		tasks:        map[string]string{},
		contents:     map[string][]byte{},
		files:        map[string]shareEntry{},
	}
	// The selected destination is a real folder below the synthetic Quark root.
	f.files["target-folder"] = shareEntry{
		ID: "target-folder", Parent: "0", Name: "目标目录", Dir: true, File: false,
		Size: json.Number("0"), Updated: json.Number("100"),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	f.serverURL = f.server.URL
	f.stateDir = t.TempDir()
	state, err := executor.OpenState(f.stateDir)
	if err != nil {
		f.server.Close()
		t.Fatalf("open transfer state: %v", err)
	}
	f.state = state
	f.client = testNativeClient(f.serverURL)
	f.transfers = &Transfers{State: state, Clients: map[string]*NativeClient{"account-1": f.client}}
	t.Cleanup(func() {
		if f.state != nil {
			_ = f.state.Close()
		}
		f.server.Close()
	})
	return f
}

func (f *nativeTransferFixture) addSource(entry shareEntry, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry.Children = nil
	f.shareEntries[entry.ID] = entry
	if content != nil {
		f.sourceBytes[entry.ID] = append([]byte(nil), content...)
	}
}

func (f *nativeTransferFixture) addSourceFile(id, parent, name string, content []byte, updated int64) shareEntry {
	sha := sha1.Sum(content)
	md := md5.Sum(content)
	entry := shareEntry{
		ID: id, Token: "share-token-" + id, Parent: parent, Name: name, File: true,
		Size: json.Number(strconv.Itoa(len(content))), Updated: json.Number(strconv.FormatInt(updated, 10)),
		SHA1: hex.EncodeToString(sha[:]), MD5: hex.EncodeToString(md[:]),
	}
	f.addSource(entry, content)
	return entry
}

func (f *nativeTransferFixture) addSourceFolder(id, parent, name string, updated int64) shareEntry {
	entry := shareEntry{
		ID: id, Token: "share-token-" + id, Parent: parent, Name: name, Dir: true,
		Size: json.Number("0"), Updated: json.Number(strconv.FormatInt(updated, 10)),
	}
	f.addSource(entry, nil)
	return entry
}

func (f *nativeTransferFixture) request() TransferRequest {
	f.t.Helper()
	target, err := f.client.Node(context.Background(), "account-1", "target-folder")
	if err != nil {
		f.t.Fatalf("read target node: %v", err)
	}
	return TransferRequest{
		SchemaVersion:        1,
		ShareID:              transferTestShareID,
		URL:                  "https://pan.quark.cn/s/" + transferTestShareID,
		TargetFolderID:       target.ID,
		TargetFolderRevision: target.Revision,
	}
}

func (f *nativeTransferFixture) restartState(t *testing.T) {
	t.Helper()
	if f.state != nil {
		if err := f.state.Close(); err != nil {
			t.Fatalf("close transfer state: %v", err)
		}
		f.state = nil
	}
	state, err := executor.OpenState(f.stateDir)
	if err != nil {
		t.Fatalf("reopen transfer state: %v", err)
	}
	f.state = state
	f.transfers.State = state
}

func (f *nativeTransferFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Cookie") != "sid=synthetic-test-cookie" {
		f.t.Errorf("unexpected synthetic request cookie: %q", r.Header.Get("Cookie"))
	}
	switch r.URL.Path {
	case "/account/info":
		f.mu.Lock()
		uid := f.uid
		f.mu.Unlock()
		fmt.Fprintf(w, `{"status":200,"code":0,"data":{"uid":%q}}`, uid)
	case "/1/clouddrive/file/info":
		id := r.URL.Query().Get("fid")
		f.mu.Lock()
		entry, ok := f.files[id]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeNativeEnvelope(f.t, w, entry, 0)
	case "/1/clouddrive/file/sort":
		parent := r.URL.Query().Get("pdir_fid")
		page, _ := strconv.Atoi(r.URL.Query().Get("_page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("_size"))
		if page < 1 || pageSize < 1 || r.URL.Query().Get("_fetch_total") != "1" || r.URL.Query().Get("_fetch_sub_dirs") != "0" {
			http.Error(w, "invalid file/sort query", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		entries := make([]shareEntry, 0)
		for _, entry := range f.files {
			if entry.Parent == parent {
				entries = append(entries, entry)
			}
		}
		f.mu.Unlock()
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
		total := len(entries)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		writeNativeEnvelope(f.t, w, map[string]any{"list": entries[start:end]}, total)
	case "/1/clouddrive/share/sharepage/token":
		var request struct {
			PwdID    string `json:"pwd_id"`
			Passcode string `json:"passcode"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.PwdID != transferTestShareID {
			http.Error(w, "invalid share token request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.tokenCalls++
		f.mu.Unlock()
		writeNativeEnvelope(f.t, w, map[string]any{"stoken": "synthetic-share-token"}, 0)
	case "/1/clouddrive/share/sharepage/detail":
		parent := r.URL.Query().Get("pdir_fid")
		page, _ := strconv.Atoi(r.URL.Query().Get("_page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("_size"))
		if r.URL.Query().Get("pwd_id") != transferTestShareID || r.URL.Query().Get("stoken") != "synthetic-share-token" || page < 1 || pageSize < 1 {
			http.Error(w, "invalid share detail query", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		entries := make([]shareEntry, 0)
		for _, entry := range f.shareEntries {
			if entry.Parent == parent {
				entry.Children = nil
				entries = append(entries, entry)
			}
		}
		f.mu.Unlock()
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
		total := len(entries)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		writeNativeEnvelope(f.t, w, map[string]any{"list": entries[start:end]}, total)
	case "/1/clouddrive/share/sharepage/save":
		f.handleSave(w, r)
	case "/1/clouddrive/task":
		taskID := r.URL.Query().Get("task_id")
		f.mu.Lock()
		remoteID, ok := f.tasks[taskID]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeNativeEnvelope(f.t, w, map[string]any{
			"status":  2,
			"save_as": map[string]any{"save_as_top_fids": []string{remoteID}},
		}, 0)
	case "/1/clouddrive/file/download":
		f.handleDownload(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/bytes/") {
			f.handleBytes(w, r)
			return
		}
		http.NotFound(w, r)
	}
}

// files are initialized with the single selectable target; this helper keeps
// all mutable personal-drive state under the fixture mutex.
func (f *nativeTransferFixture) handleSave(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FIDs       []string `json:"fid_list"`
		Tokens     []string `json:"fid_token_list"`
		Parent     string   `json:"to_pdir_fid"`
		PwdID      string   `json:"pwd_id"`
		ShareToken string   `json:"stoken"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.FIDs) != 1 || len(request.Tokens) != 1 || request.PwdID != transferTestShareID || request.ShareToken != "synthetic-share-token" || request.Parent == "" {
		http.Error(w, "invalid save request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.saveCalls++
	failure := f.nextSaveFailure
	f.nextSaveFailure = ""
	var remoteID string
	if failure != "before" {
		remoteID = f.copySourceLocked(request.FIDs[0], request.Parent)
	}
	taskID := ""
	if failure == "" {
		f.nextTask++
		taskID = fmt.Sprintf("task-%03d", f.nextTask)
		f.tasks[taskID] = remoteID
	}
	f.mu.Unlock()
	if failure == "before" || failure == "after" {
		f.closeResponse(w)
		return
	}
	writeNativeEnvelope(f.t, w, map[string]any{"task_id": taskID}, 0)
}

func (f *nativeTransferFixture) copySourceLocked(sourceID, destinationParent string) string {
	source, ok := f.shareEntries[sourceID]
	if !ok {
		return ""
	}
	f.nextID++
	remoteID := fmt.Sprintf("remote-%04d", f.nextID)
	remote := shareEntry{
		ID: remoteID, Parent: destinationParent, Name: source.Name,
		Dir: source.Dir, File: source.File, Size: source.Size, Updated: source.Updated,
		SHA1: source.SHA1, MD5: source.MD5,
	}
	f.files[remoteID] = remote
	if content, exists := f.sourceBytes[sourceID]; exists {
		f.contents[remoteID] = append([]byte(nil), content...)
	}
	for _, child := range f.shareEntries {
		if child.Parent == sourceID {
			f.copySourceLocked(child.ID, remoteID)
		}
	}
	return remoteID
}

func (f *nativeTransferFixture) closeResponse(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "response hijacking unavailable", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func (f *nativeTransferFixture) handleDownload(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FIDs []string `json:"fids"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.FIDs) != 1 {
		http.Error(w, "invalid download request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.downloadCalls++
	status := f.downloadStatus
	f.downloadStatus = 0
	_, exists := f.contents[request.FIDs[0]]
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	writeNativeEnvelope(f.t, w, []map[string]any{{"fid": request.FIDs[0], "download_url": f.serverURL + "/bytes/" + request.FIDs[0]}}, 0)
}

func (f *nativeTransferFixture) handleBytes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/bytes/")
	f.mu.Lock()
	content, ok := f.contents[id]
	content = append([]byte(nil), content...)
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	_, _ = w.Write(content)
}

func (f *nativeTransferFixture) addNewSourceFile(id, name string, content []byte) shareEntry {
	return f.addSourceFile(id, "0", name, content, 300)
}

func (f *nativeTransferFixture) saveCountValue() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveCalls
}

func (f *nativeTransferFixture) tokenCountValue() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls
}

func (f *nativeTransferFixture) downloadCountValue() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloadCalls
}

func (f *nativeTransferFixture) setSaveFailure(mode string) {
	f.mu.Lock()
	f.nextSaveFailure = mode
	f.mu.Unlock()
}

func (f *nativeTransferFixture) setDownloadStatus(status int) {
	f.mu.Lock()
	f.downloadStatus = status
	f.mu.Unlock()
}

func (f *nativeTransferFixture) setUID(uid string) {
	f.mu.Lock()
	f.uid = uid
	f.mu.Unlock()
}

func (f *nativeTransferFixture) renameTarget(name string) {
	f.mu.Lock()
	entry := f.files["target-folder"]
	entry.Name = name
	f.files["target-folder"] = entry
	f.mu.Unlock()
}

func (f *nativeTransferFixture) remoteEntry(id string) shareEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[id]
}

func (f *nativeTransferFixture) remoteBytesFor(id string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.contents[id]...)
}

func (f *nativeTransferFixture) sourceEntries() []shareEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries := make([]shareEntry, 0, len(f.shareEntries))
	for _, source := range f.shareEntries {
		entries = append(entries, source)
	}
	return entries
}

func (f *nativeTransferFixture) managedShare(t *testing.T, share string) managedShare {
	t.Helper()
	var registry managedShare
	name := "quark-share-" + qhash([]string{"account-1", share})
	if err := f.state.Read(name, &registry); err != nil {
		t.Fatalf("read managed share: %v", err)
	}
	return registry
}

func (f *nativeTransferFixture) assertRegistryMatchesSources(t *testing.T, req TransferRequest) managedShare {
	t.Helper()
	registry := f.managedShare(t, req.ShareID)
	sources := f.sourceEntries()
	bySourceID := make(map[string]shareEntry, len(sources))
	for _, source := range sources {
		bySourceID[source.ID] = source
	}
	for _, source := range sources {
		copy, ok := registry.Entries[entryKey(source)]
		if !ok || copy.RemoteID == "" {
			t.Fatalf("missing source mapping for %q", source.ID)
		}
		remote := f.remoteEntry(copy.RemoteID)
		wantParent := req.TargetFolderID
		if source.Parent != "0" {
			parent, ok := bySourceID[source.Parent]
			if !ok {
				t.Fatalf("source %q has unknown parent %q", source.ID, source.Parent)
			}
			parentCopy, ok := registry.Entries[entryKey(parent)]
			if !ok {
				t.Fatalf("missing parent mapping for %q", source.Parent)
			}
			wantParent = parentCopy.RemoteID
		}
		if remote.ID != copy.RemoteID || remote.Parent != wantParent || remote.Name != source.Name || remote.Dir != source.Dir || remote.File != source.File || remote.Size != source.Size || remote.Updated != source.Updated || !strings.EqualFold(remote.SHA1, source.SHA1) || !strings.EqualFold(remote.MD5, source.MD5) {
			t.Fatalf("remote fact mismatch for %q: got %#v, want source %#v under %q", source.ID, remote, source, wantParent)
		}
		if source.File {
			if got, want := f.remoteBytesFor(copy.RemoteID), f.sourceBytesFor(source.ID); string(got) != string(want) {
				t.Fatalf("bytes mismatch for %q: got %q, want %q", source.ID, got, want)
			}
		}
	}
	return registry
}

func (f *nativeTransferFixture) sourceBytesFor(id string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.sourceBytes[id]...)
}

func transferFault(t *testing.T, err error, code string) *p.Fault {
	t.Helper()
	var fault *p.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("error = %#v, want fault %s", err, code)
	}
	return fault
}

func TestNativeTransferCopiesNestedShareAndOnlySavesNewEntries(t *testing.T) {
	f := newNativeTransferFixture(t)
	f.addSourceFolder("src-folder", "0", "课程", 100)
	inside := []byte("nested-file-data")
	f.addSourceFile("src-nested", "src-folder", "inside.bin", inside, 101)
	rootData := []byte("root-file-bytes")
	f.addSourceFile("src-root", "0", "root.bin", rootData, 102)
	req := f.request()

	first, err := f.transfers.Transfer(context.Background(), "op-transfer-1", "account-1", req, false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Complete || len(first.Nodes) != 3 || f.saveCountValue() != 2 {
		t.Fatalf("first transfer result=%#v saveCount=%d", first, f.saveCountValue())
	}
	registry := f.assertRegistryMatchesSources(t, req)
	paths := map[string]bool{}
	for _, node := range first.Nodes {
		paths[node.RelativePath] = true
	}
	for _, want := range []string{"课程", "课程/inside.bin", "root.bin"} {
		if !paths[want] {
			t.Fatalf("missing transferred path %q in %#v", want, paths)
		}
	}

	// Replaying the same operation verifies the persisted mappings and does not
	// submit another remote save.
	if _, err := f.transfers.Transfer(context.Background(), "op-transfer-1", "account-1", req, false); err != nil {
		t.Fatal(err)
	}
	if got := f.saveCountValue(); got != 2 {
		t.Fatalf("same operation repeated %d saves, want 2", got)
	}

	oldRemoteIDs := map[string]string{}
	for key, value := range registry.Entries {
		oldRemoteIDs[key] = value.RemoteID
	}
	newEntry := f.addNewSourceFile("src-new", "new.bin", []byte("newly-added-content"))
	updated, err := f.transfers.Transfer(context.Background(), "op-transfer-2", "account-1", req, false)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Complete || len(updated.Nodes) != 4 || f.saveCountValue() != 3 {
		t.Fatalf("update result=%#v saveCount=%d", updated, f.saveCountValue())
	}
	registry = f.assertRegistryMatchesSources(t, req)
	if _, ok := registry.Entries[entryKey(newEntry)]; !ok {
		t.Fatal("new share entry was not registered")
	}
	for key, oldID := range oldRemoteIDs {
		if got := registry.Entries[key].RemoteID; got != oldID {
			t.Fatalf("existing remote mapping changed: %s -> %s, want %s", key, got, oldID)
		}
	}
}

func TestNativeTransferPinsSessionAcrossIdentityCheckAndSave(t *testing.T) {
	f := newNativeTransferFixture(t)
	f.addSourceFile("session-source", "0", "pinned.bin", []byte("same-account-session"), 101)
	req := f.request()
	original := f.client.Cookie
	reads := 0
	f.client.Cookie = func() (string, error) {
		reads++
		if reads > 1 {
			return "sid=different-account", nil
		}
		return original()
	}
	result, err := f.transfers.Transfer(context.Background(), "op-pinned-session", "account-1", req, false)
	if err != nil || !result.Complete || reads != 1 || f.saveCountValue() != 1 {
		t.Fatalf("session was not pinned for the frozen operation: complete=%v reads=%d saves=%d err=%v", result.Complete, reads, f.saveCountValue(), err)
	}
}

type transferRoundTrip func(*http.Request) (*http.Response, error)

func (f transferRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeTransferRevalidatesTargetBetweenSaveRequests(t *testing.T) {
	f := newNativeTransferFixture(t)
	f.addSourceFile("src-a", "0", "a.bin", []byte("first file"), 101)
	f.addSourceFile("src-b", "0", "b.bin", []byte("second file"), 102)
	req := f.request()
	var moved sync.Once
	f.client.HTTP.Transport = transferRoundTrip(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sharepage/save") {
			moved.Do(func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				target := f.files["target-folder"]
				target.Parent = "moved-parent"
				f.files["target-folder"] = target
			})
		}
		return response, err
	})
	_, err := f.transfers.Transfer(context.Background(), "op-target-moved", "account-1", req, false)
	transferFault(t, err, "VERSION_CHANGED")
	if calls := f.saveCountValue(); calls != 1 {
		t.Fatalf("target changed after the first effect, but %d saves were sent", calls)
	}
}

func TestNativeTransferRejectsRenamedManagedParentBeforeIncrementalSave(t *testing.T) {
	f := newNativeTransferFixture(t)
	f.addSourceFolder("src-folder", "0", "Original", 100)
	f.addSourceFile("src-first", "src-folder", "first.bin", []byte("original child"), 101)
	req := f.request()
	if _, err := f.transfers.Transfer(context.Background(), "op-initial-folder", "account-1", req, false); err != nil {
		t.Fatal(err)
	}
	registry, err := f.transfers.loadShare("account-1", req.ShareID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range registry.Entries {
		if entry.Source.ID == "src-folder" {
			f.mu.Lock()
			node := f.files[entry.RemoteID]
			node.Name = "Changed"
			f.files[entry.RemoteID] = node
			f.mu.Unlock()
		}
	}
	f.addSourceFile("src-next", "src-folder", "next.bin", []byte("new child"), 102)
	before := f.saveCountValue()
	_, err = f.transfers.Transfer(context.Background(), "op-increment-renamed", "account-1", req, false)
	transferFault(t, err, "VERSION_CHANGED")
	if calls := f.saveCountValue(); calls != before {
		t.Fatalf("renamed parent received another save: before=%d after=%d", before, calls)
	}
}

func TestNativeTransferLostSaveResponseReconcilesAfterRestartWithoutResubmitting(t *testing.T) {
	f := newNativeTransferFixture(t)
	entry := f.addSourceFile("src-lost", "0", "lost.bin", []byte("save-effect-exists"), 201)
	req := f.request()
	f.setSaveFailure("after")

	result, err := f.transfers.Transfer(context.Background(), "op-lost-response", "account-1", req, false)
	if err != nil {
		t.Fatalf("lost response should reconcile from the new node: %v", err)
	}
	if !result.Complete || f.saveCountValue() != 1 {
		t.Fatalf("result=%#v saveCount=%d", result, f.saveCountValue())
	}
	registry := f.assertRegistryMatchesSources(t, req)
	oldID := registry.Entries[entryKey(entry)].RemoteID
	tokenCalls, saveCalls := f.tokenCountValue(), f.saveCountValue()
	f.restartState(t)
	req.OriginalOperationID = "op-lost-response"
	reconciled, err := f.transfers.Transfer(context.Background(), "different-retry-id", "account-1", req, false)
	if err != nil {
		t.Fatalf("read-only reconciliation after restart: %v", err)
	}
	if !reconciled.Complete || f.saveCountValue() != saveCalls || f.tokenCountValue() != tokenCalls {
		t.Fatalf("reconcile result=%#v saves=%d tokens=%d", reconciled, f.saveCountValue(), f.tokenCountValue())
	}
	if got := f.managedShare(t, req.ShareID).Entries[entryKey(entry)].RemoteID; got != oldID {
		t.Fatalf("reconciliation changed remote identity from %q to %q", oldID, got)
	}
}

func TestNativeTransferUnknownResultSurvivesRestartWithoutAnotherSave(t *testing.T) {
	f := newNativeTransferFixture(t)
	f.addSourceFile("src-unknown", "0", "unknown.bin", []byte("no-effect-evidence"), 301)
	req := f.request()
	f.setSaveFailure("before")
	_, err := f.transfers.Transfer(context.Background(), "op-unknown", "account-1", req, false)
	transferFault(t, err, "RESULT_UNKNOWN")
	if f.saveCountValue() != 1 {
		t.Fatalf("initial save calls = %d, want 1", f.saveCountValue())
	}
	tokenCalls := f.tokenCountValue()
	f.restartState(t)
	req.OriginalOperationID = "op-unknown"
	_, err = f.transfers.Transfer(context.Background(), "op-retry", "account-1", req, false)
	transferFault(t, err, "RESULT_UNKNOWN")
	if f.saveCountValue() != 1 || f.tokenCountValue() != tokenCalls {
		t.Fatalf("read-only retry resubmitted side effects: saves=%d tokens=%d", f.saveCountValue(), f.tokenCountValue())
	}
}

func TestNativeTransferBlocksChangedTargetAndAccount(t *testing.T) {
	f := newNativeTransferFixture(t)
	f.addSourceFile("src-blocked", "0", "blocked.bin", []byte("blocked"), 401)
	req := f.request()
	f.renameTarget("changed target")
	_, err := f.transfers.Transfer(context.Background(), "op-target-change", "account-1", req, false)
	transferFault(t, err, "VERSION_CHANGED")
	if f.saveCountValue() != 0 || f.tokenCountValue() != 0 {
		t.Fatalf("changed target caused remote writes: saves=%d tokens=%d", f.saveCountValue(), f.tokenCountValue())
	}

	g := newNativeTransferFixture(t)
	g.addSourceFile("src-blocked", "0", "blocked.bin", []byte("blocked"), 401)
	request := g.request()
	g.setUID("different-user")
	_, err = g.transfers.Transfer(context.Background(), "op-account-change", "account-1", request, false)
	transferFault(t, err, "AUTH_REQUIRED")
	if g.saveCountValue() != 0 || g.tokenCountValue() != 0 {
		t.Fatalf("changed account caused remote writes: saves=%d tokens=%d", g.saveCountValue(), g.tokenCountValue())
	}
}

func TestNativeTransferOpenNodeChecksManagedVersionAndRealBytes(t *testing.T) {
	f := newNativeTransferFixture(t)
	entry := f.addSourceFile("src-download", "0", "download.bin", []byte("verified-download-bytes"), 501)
	req := f.request()
	result, err := f.transfers.Transfer(context.Background(), "op-download", "account-1", req, false)
	if err != nil {
		t.Fatal(err)
	}
	var file CloudNode
	for _, node := range result.Nodes {
		if node.Kind == "FILE" && node.Name == entry.Name {
			file = node
			break
		}
	}
	if file.ExternalID == "" || file.SizeBytes == nil {
		t.Fatalf("download node missing immutable ID or size: %#v", file)
	}

	body, err := f.transfers.OpenNode(context.Background(), "account-1", file)
	if err != nil {
		t.Fatalf("OpenNode: %v", err)
	}
	actual, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(actual) != string(f.remoteBytesFor(file.ExternalID)) || string(actual) != string(f.sourceBytesFor(entry.ID)) {
		t.Fatalf("download bytes=%q readErr=%v closeErr=%v", actual, readErr, closeErr)
	}
	if f.downloadCountValue() != 1 {
		t.Fatalf("download endpoint calls=%d, want 1", f.downloadCountValue())
	}

	unknownSize := file
	unknownSize.SizeBytes = nil
	_, err = f.transfers.OpenNode(context.Background(), "account-1", unknownSize)
	transferFault(t, err, "VERSION_CHANGED")
	wrongVersion := file
	wrongVersion.Version = "stale-version"
	_, err = f.transfers.OpenNode(context.Background(), "account-1", wrongVersion)
	transferFault(t, err, "VERSION_CHANGED")
	outsideShare := file
	outsideShare.ExternalID = "unmanaged-id"
	outsideShare.ID = outsideShare.ExternalID
	_, err = f.transfers.OpenNode(context.Background(), "account-1", outsideShare)
	transferFault(t, err, "PERMISSION_DENIED")
	if f.downloadCountValue() != 1 {
		t.Fatalf("invalid open requests reached download endpoint: %d", f.downloadCountValue())
	}

	f.setDownloadStatus(http.StatusUnauthorized)
	_, err = f.transfers.OpenNode(context.Background(), "account-1", file)
	transferFault(t, err, "AUTH_REQUIRED")

	remote := f.remoteEntry(file.ExternalID)
	remote.MD5 = "different-md5"
	f.mu.Lock()
	f.files[file.ExternalID] = remote
	f.mu.Unlock()
	_, err = f.transfers.OpenNode(context.Background(), "account-1", file)
	transferFault(t, err, "VERSION_CHANGED")
}
