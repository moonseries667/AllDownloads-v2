package drive

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

type archiveFolderServer struct {
	mu                     sync.Mutex
	server                 *httptest.Server
	objects                map[string]Object
	pending                map[string]Object
	ids                    int
	folderCreates          int
	fileCreates            int
	filePuts               int
	loseNextFolderResponse bool
}

func newArchiveFolderDrive(t *testing.T) (*archiveFolderServer, *Client) {
	t.Helper()
	f := &archiveFolderServer{
		objects: map[string]Object{"root-id": folderObject("root-id", "实际根目录", "")},
		pending: map[string]Object{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer folder-fixture" {
			t.Error("missing synthetic identity")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		respond := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.URL.Path == "/drive/v3/files/generateIds" && r.Method == http.MethodGet:
			f.ids++
			respond(map[string]any{"ids": []string{fmt.Sprintf("reserved-%d", f.ids)}})
		case r.URL.Path == "/drive/v3/files" && r.Method == http.MethodGet:
			query := r.URL.Query().Get("q")
			parentEnd := strings.Index(query, "' in parents")
			if parentEnd < 1 {
				t.Errorf("missing parent filter: %s", query)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			parent := query[1:parentEnd]
			name := ""
			if at := strings.LastIndex(query, " and name = '"); at >= 0 {
				name = strings.TrimSuffix(query[at+len(" and name = '"):], "'")
			}
			items := []Object{}
			for _, obj := range f.objects {
				if obj.Trashed || obj.MimeType != folderMIME || obj.Name == "" || (name != "" && obj.Name != name) {
					continue
				}
				if len(obj.Parents) == 1 && obj.Parents[0] == parent {
					items = append(items, obj)
				}
			}
			respond(map[string]any{"files": items, "incompleteSearch": false})
		case r.URL.Path == "/drive/v3/files" && r.Method == http.MethodPost:
			var obj Object
			if json.NewDecoder(r.Body).Decode(&obj) != nil || obj.ID == "" || obj.Name == "" || obj.MimeType != folderMIME || len(obj.Parents) != 1 || obj.Properties["alldownloadsKey"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if _, exists := f.objects[obj.ID]; exists {
				w.WriteHeader(http.StatusConflict)
				return
			}
			obj.Version = "1"
			obj.Capabilities.CanAdd = true
			obj.Capabilities.CanList = true
			f.folderCreates++
			f.objects[obj.ID] = obj
			if f.loseNextFolderResponse {
				f.loseNextFolderResponse = false
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			respond(obj)
		case strings.HasPrefix(r.URL.Path, "/drive/v3/files/") && r.Method == http.MethodGet:
			id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
			obj, ok := f.objects[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			respond(obj)
		case r.URL.Path == "/upload/drive/v3/files" && r.Method == http.MethodPost:
			var obj Object
			if json.NewDecoder(r.Body).Decode(&obj) != nil || obj.ID == "" || obj.Name == "" || len(obj.Parents) != 1 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.fileCreates++
			f.pending[obj.ID] = obj
			w.Header().Set("Location", f.server.URL+"/upload/drive/v3/files/session/"+obj.ID)
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/upload/drive/v3/files/session/") && r.Method == http.MethodPut:
			id := strings.TrimPrefix(r.URL.Path, "/upload/drive/v3/files/session/")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			obj, exists := f.pending[id]
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			obj.MimeType = "application/octet-stream"
			obj.Size = strconv.Itoa(len(data))
			obj.SHA256 = digest(data)
			md5sum := md5.Sum(data)
			obj.MD5 = hex.EncodeToString(md5sum[:])
			f.filePuts++
			f.objects[id] = obj
			respond(obj)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(f.server.Close)
	c := &Client{AccountID: "account", RootID: "root-id", HTTP: f.server.Client(), Token: func() string { return "folder-fixture" }, BaseURL: f.server.URL}
	return f, c
}

func archiveFolderFixture(t *testing.T, c *Client, relative string) (*executor.State, string, ArchiveRequest) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	local := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("verified nested media")
	if err := os.WriteFile(local, data, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := executor.OpenState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	page, err := c.List(ctx, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	target, err := c.Validate(ctx, page.Parent.ID, page.Parent.Revision)
	if err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{
		SchemaVersion: 1,
		ProjectID:     "folder-project",
		ArchivePolicy: "AUTO",
		ArchiveTarget: target,
		Files: []ArtifactFile{{
			OutputID: "nested-output", RelativePath: relative, SizeBytes: strconv.Itoa(len(data)),
			SHA256: digest(data), MediaKinds: []string{"FILE"}, Verified: true,
		}},
	}
	return state, root, req
}

func TestArchiveCreatesRelativeFoldersAndReusesPinnedIdentity(t *testing.T) {
	f, c := newArchiveFolderDrive(t)
	state, root, req := archiveFolderFixture(t, c, "课程/第一课/media.bin")
	ctx := context.Background()
	result, err := c.Archive(ctx, state, "nested-op", root, req)
	if err != nil || !result.Verified || len(result.Files) != 1 {
		t.Fatal(result, err)
	}
	f.mu.Lock()
	firstCreates, firstIDs, firstFilePuts := f.folderCreates, f.ids, f.filePuts
	first := f.objects["reserved-1"]
	second := f.objects["reserved-2"]
	f.mu.Unlock()
	if firstCreates != 2 || firstIDs != 3 || firstFilePuts != 1 {
		t.Fatalf("unexpected initial writes: folders=%d IDs=%d uploads=%d", firstCreates, firstIDs, firstFilePuts)
	}
	if first.Name != "课程" || first.MimeType != folderMIME || len(first.Parents) != 1 || first.Parents[0] != "root-id" || first.Properties["alldownloadsKey"] == "" {
		t.Fatalf("invalid first directory metadata: %+v", first)
	}
	if second.Name != "第一课" || second.MimeType != folderMIME || len(second.Parents) != 1 || second.Parents[0] != first.ID || second.Properties["alldownloadsKey"] == "" {
		t.Fatalf("invalid nested directory metadata: %+v", second)
	}
	if _, err = c.Archive(ctx, state, "nested-op", root, req); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Verify(ctx, state, "nested-op", root, req); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.folderCreates != firstCreates || f.ids != firstIDs || f.filePuts != firstFilePuts {
		t.Fatalf("retry repeated remote writes: folders=%d IDs=%d uploads=%d", f.folderCreates, f.ids, f.filePuts)
	}
	if _, err = os.Stat(filepath.Join(root, filepath.FromSlash(req.Files[0].RelativePath))); err != nil {
		t.Fatal("local source was removed", err)
	}
}

func TestArchiveLostFolderResponseReconcilesSameIDBeforeRetry(t *testing.T) {
	f, c := newArchiveFolderDrive(t)
	state, root, req := archiveFolderFixture(t, c, "课程/media.bin")
	f.mu.Lock()
	f.loseNextFolderResponse = true
	f.mu.Unlock()
	if result, err := c.Archive(context.Background(), state, "lost-folder-op", root, req); err == nil || result.Verified {
		t.Fatal("lost folder response was counted as success", result)
	}
	f.mu.Lock()
	reservedAfterLoss, createsAfterLoss := f.ids, f.folderCreates
	f.mu.Unlock()
	if reservedAfterLoss != 1 || createsAfterLoss != 1 {
		t.Fatalf("folder identity was not reserved once: ids=%d creates=%d", reservedAfterLoss, createsAfterLoss)
	}
	if _, err := c.Verify(context.Background(), state, "lost-folder-op", root, req); !isCode(err, "ARCHIVE_NOT_CONFIRMED") {
		t.Fatalf("read-only reconcile should report missing file evidence: %v", err)
	}
	f.mu.Lock()
	verifiedFolder := f.objects["reserved-1"]
	f.mu.Unlock()
	for _, mutation := range []struct {
		name   string
		change func(*Object)
	}{
		{name: "parent", change: func(obj *Object) { obj.Parents = []string{"different-parent"} }},
		{name: "name", change: func(obj *Object) { obj.Name = "different-name" }},
		{name: "mime", change: func(obj *Object) { obj.MimeType = "application/octet-stream" }},
		{name: "application-key", change: func(obj *Object) { obj.Properties["alldownloadsKey"] = "different-key" }},
	} {
		t.Run("folder-evidence-"+mutation.name, func(t *testing.T) {
			f.mu.Lock()
			changed := verifiedFolder
			changed.Properties = map[string]string{"alldownloadsKey": verifiedFolder.Properties["alldownloadsKey"]}
			mutation.change(&changed)
			f.objects["reserved-1"] = changed
			f.mu.Unlock()
			if _, err := c.Verify(context.Background(), state, "lost-folder-op", root, req); err == nil {
				t.Fatal("verification accepted changed folder identity")
			}
			f.mu.Lock()
			f.objects["reserved-1"] = verifiedFolder
			f.mu.Unlock()
		})
	}
	f.mu.Lock()
	reservedAfterVerify, createsAfterVerify := f.ids, f.folderCreates
	f.mu.Unlock()
	if reservedAfterVerify != reservedAfterLoss || createsAfterVerify != createsAfterLoss {
		t.Fatal("Verify performed a write")
	}
	result, err := c.Archive(context.Background(), state, "lost-folder-op", root, req)
	if err != nil || !result.Verified {
		t.Fatal(result, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.folderCreates != 1 || f.ids != 2 || f.filePuts != 1 {
		t.Fatalf("retry did not retain the same folder identity: folders=%d IDs=%d uploads=%d", f.folderCreates, f.ids, f.filePuts)
	}
}

func TestArchiveReusesOnlyOneWritableExistingDirectory(t *testing.T) {
	f, c := newArchiveFolderDrive(t)
	f.mu.Lock()
	f.objects["existing"] = folderObject("existing", "课程", "root-id")
	f.mu.Unlock()
	state, root, req := archiveFolderFixture(t, c, "课程/media.bin")
	result, err := c.Archive(context.Background(), state, "existing-folder-op", root, req)
	if err != nil || !result.Verified {
		t.Fatal(result, err)
	}
	f.mu.Lock()
	if f.folderCreates != 0 || f.ids != 1 || f.objects[result.Files[0].RemoteID].Parents[0] != "existing" {
		t.Fatalf("existing directory was not reused: creates=%d ids=%d file=%+v", f.folderCreates, f.ids, f.objects[result.Files[0].RemoteID])
	}
	f.mu.Unlock()
}

func TestArchiveRejectsAmbiguousDirectoryAndVerifyNeverAdoptsReplacement(t *testing.T) {
	t.Run("ambiguous", func(t *testing.T) {
		f, c := newArchiveFolderDrive(t)
		f.mu.Lock()
		f.objects["one"] = folderObject("one", "课程", "root-id")
		f.objects["two"] = folderObject("two", "课程", "root-id")
		f.mu.Unlock()
		state, root, req := archiveFolderFixture(t, c, "课程/media.bin")
		if _, err := c.Archive(context.Background(), state, "ambiguous-folder-op", root, req); !isCode(err, "ARCHIVE_DIRECTORY_AMBIGUOUS") {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.folderCreates != 0 || f.ids != 0 || f.filePuts != 0 {
			t.Fatal("ambiguous directory triggered a write")
		}
	})

	t.Run("verify-does-not-adopt-replacement", func(t *testing.T) {
		f, c := newArchiveFolderDrive(t)
		f.mu.Lock()
		f.objects["old"] = folderObject("old", "课程", "root-id")
		f.mu.Unlock()
		state, root, req := archiveFolderFixture(t, c, "课程/media.bin")
		if _, err := c.Archive(context.Background(), state, "removed-folder-op", root, req); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		delete(f.objects, "old")
		f.objects["replacement"] = folderObject("replacement", "课程", "root-id")
		writesBefore := f.folderCreates + f.fileCreates + f.filePuts + f.ids
		f.mu.Unlock()
		if _, err := c.Verify(context.Background(), state, "removed-folder-op", root, req); !isCode(err, "ARCHIVE_NOT_CONFIRMED") {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.folderCreates+f.fileCreates+f.filePuts+f.ids != writesBefore {
			t.Fatal("Verify wrote or reserved a replacement")
		}
	})
}
