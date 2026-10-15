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
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

type fakeDrive struct {
	mu                         sync.Mutex
	server                     *httptest.Server
	objects                    map[string]Object
	pending                    map[string]Object
	writes, puts, reserved     int
	loseResponse, emptySuccess bool
}

func folderObject(id, name, parent string) Object {
	o := Object{ID: id, Name: name, MimeType: folderMIME, Version: "1"}
	if parent != "" {
		o.Parents = []string{parent}
	}
	o.Capabilities.CanAdd = true
	o.Capabilities.CanList = true
	return o
}
func newFakeDrive(t *testing.T) (*fakeDrive, *Client) {
	t.Helper()
	f := &fakeDrive{objects: map[string]Object{"root-id": folderObject("root-id", "实际根目录", ""), "a": folderObject("a", "同名", "root-id"), "b": folderObject("b", "同名", "root-id")}, pending: map[string]Object{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fixture-only" {
			t.Error("missing synthetic identity")
			w.WriteHeader(401)
			return
		}
		respond := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
		switch {
		case r.URL.Path == "/drive/v3/files/generateIds":
			if r.Method != "GET" {
				t.Error("ID reservation method")
			}
			f.reserved++
			respond(map[string]any{"ids": []string{fmt.Sprint("remote-", f.reserved)}})
		case r.URL.Path == "/drive/v3/files":
			if r.Method != "GET" {
				t.Error("directory selection wrote cloud")
				w.WriteHeader(500)
				return
			}
			q := r.URL.Query().Get("q")
			if !strings.Contains(q, "mimeType = '"+folderMIME+"'") || !strings.Contains(q, "trashed = false") {
				t.Error("not folder-only")
			}
			items := []Object{}
			for _, o := range f.objects {
				if o.MimeType == folderMIME && !o.Trashed && len(o.Parents) == 1 && strings.HasPrefix(q, quote(o.Parents[0])+" in parents") && (!strings.Contains(q, " and name = ") || strings.HasSuffix(q, " and name = "+quote(o.Name))) {
					items = append(items, o)
				}
			}
			sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
			offset := 0
			if token := r.URL.Query().Get("pageToken"); token != "" {
				var err error
				offset, err = strconv.Atoi(strings.TrimPrefix(token, "page-"))
				if err != nil || offset < 0 || offset > len(items) {
					w.WriteHeader(400)
					return
				}
			}
			size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
			end := offset + size
			if end > len(items) {
				end = len(items)
			}
			next := ""
			if end < len(items) {
				next = fmt.Sprint("page-", end)
			}
			respond(map[string]any{"files": items[offset:end], "nextPageToken": next, "incompleteSearch": false})
		case strings.HasPrefix(r.URL.Path, "/drive/v3/files/"):
			id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
			if id == "root" {
				id = "root-id"
			}
			o, ok := f.objects[id]
			if !ok {
				w.WriteHeader(404)
				return
			}
			respond(o)
		case r.URL.Path == "/upload/drive/v3/files" && r.Method == "POST":
			var o Object
			if json.NewDecoder(r.Body).Decode(&o) != nil {
				w.WriteHeader(400)
				return
			}
			f.writes++
			f.pending[o.ID] = o
			w.Header().Set("Location", f.server.URL+"/upload/drive/v3/files/session/"+o.ID)
			w.WriteHeader(200)
		case strings.HasPrefix(r.URL.Path, "/upload/drive/v3/files/session/") && r.Method == "PUT":
			id := strings.TrimPrefix(r.URL.Path, "/upload/drive/v3/files/session/")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(400)
				return
			}
			f.puts++
			o := f.pending[id]
			o.MimeType = "application/octet-stream"
			o.Size = strconv.Itoa(len(data))
			o.SHA256 = digest(data)
			sum := md5.Sum(data)
			o.MD5 = hex.EncodeToString(sum[:])
			if !f.emptySuccess {
				f.objects[id] = o
			}
			if f.loseResponse {
				f.loseResponse = false
				w.WriteHeader(503)
				return
			}
			respond(o)
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(f.server.Close)
	return f, &Client{AccountID: "account", HTTP: f.server.Client(), Token: func() string { return "fixture-only" }, BaseURL: f.server.URL}
}

func TestFolderSelectionNamesIDsPaginationAndPermissions(t *testing.T) {
	f, c := newFakeDrive(t)
	ctx := context.Background()
	page, err := c.List(ctx, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.Parent.ID != "root-id" || page.NextCursor == nil {
		t.Fatalf("%+v %v", page, err)
	}
	second, err := c.List(ctx, "root-id", *page.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID || second.Items[0].Name != page.Items[0].Name || second.NextCursor != nil {
		t.Fatalf("%+v %v", second, err)
	}
	child, err := c.List(ctx, "a", "", 50)
	if err != nil || len(child.Items) != 0 || len(child.Breadcrumbs) != 2 || child.Parent.Path != "同名" {
		t.Fatal(child, err)
	}
	target, err := c.Validate(ctx, "a", child.Parent.Revision)
	if err != nil || target.FolderID != "a" {
		t.Fatal(target, err)
	}
	f.mu.Lock()
	a := f.objects["a"]
	a.Name = "移动后名称"
	f.objects["a"] = a
	f.mu.Unlock()
	if _, err = c.Validate(ctx, "a", target.Revision); !isCode(err, "TARGET_CHANGED") {
		t.Fatal(err)
	}
	f.mu.Lock()
	a.Capabilities.CanAdd = false
	f.objects["a"] = a
	f.mu.Unlock()
	if _, err = c.Validate(ctx, "a", target.Revision); !isCode(err, "TARGET_PERMISSION_DENIED") {
		t.Fatal(err)
	}
	if _, err = c.List(ctx, "root-id", "invalid", 50); !isCode(err, "INVALID_CURSOR") {
		t.Fatal(err)
	}
	f.mu.Lock()
	a.Trashed = true
	f.objects["a"] = a
	writes := f.writes
	f.mu.Unlock()
	if _, err = c.Validate(ctx, "a", target.Revision); !isCode(err, "TARGET_NOT_FOUND") {
		t.Fatal(err)
	}
	if writes != 0 || f.reserved != 0 {
		t.Fatal("selection wrote to cloud")
	}
	c.Token = func() string { return "" }
	if _, err = c.List(ctx, "", "", 50); !isCode(err, "AUTH_REQUIRED") {
		t.Fatal(err)
	}
}

func archiveFixture(t *testing.T, c *Client) (*executor.State, string, ArchiveRequest) {
	t.Helper()
	root := t.TempDir()
	state, err := executor.OpenState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	data := []byte("frozen media fixture")
	if err = os.WriteFile(filepath.Join(root, "media.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "unselected.bin"), []byte("must never upload"), 0600)
	page, err := c.List(context.Background(), "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	target, err := c.Validate(context.Background(), page.Parent.ID, page.Parent.Revision)
	if err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{SchemaVersion: 1, ProjectID: "project", ArchivePolicy: "AUTO", ArchiveTarget: target, Files: []ArtifactFile{{OutputID: "file", RelativePath: "media.bin", SizeBytes: strconv.Itoa(len(data)), SHA256: digest(data), MediaKinds: []string{"FILE"}, Verified: true}}}
	return state, root, req
}

func TestArchiveFixedManifestLostResponseAndReadOnlyReconciliation(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	ctx := context.Background()
	f.mu.Lock()
	f.loseResponse = true
	f.mu.Unlock()
	if result, err := c.Archive(ctx, state, "op", root, req); err == nil || result.Verified {
		t.Fatal("lost upload response counted as verified")
	}
	// A restart only observes the pre-reserved ID and never resends data.
	result, err := c.Verify(ctx, state, "op", root, req)
	if err != nil || !result.Verified || len(result.Files) != 1 || result.Files[0].RemoteName != "media.bin" {
		t.Fatal(result, err)
	}
	result, err = c.Archive(ctx, state, "op", root, req)
	if err != nil || !result.Verified {
		t.Fatal(result, err)
	}
	f.mu.Lock()
	puts, writes, reserved := f.puts, f.writes, f.reserved
	f.mu.Unlock()
	if puts != 1 || writes != 1 || reserved != 1 {
		t.Fatalf("retry repeated remote effect: %d %d %d", puts, writes, reserved)
	}
	if _, err = os.Stat(filepath.Join(root, "media.bin")); err != nil {
		t.Fatal("local original removed", err)
	}
	changed := req
	changed.ProjectID = "different-project"
	if _, err = c.Archive(ctx, state, "op", root, changed); !isCode(err, "ARCHIVE_MANIFEST_CHANGED") {
		t.Fatal(err)
	}
}

func TestArchiveRequiresActualRemoteEvidenceAndRetainsLocalFiles(t *testing.T) {
	for _, mutation := range []string{"missing", "name", "size", "hash", "parent", "no-checksum", "md5"} {
		t.Run(mutation, func(t *testing.T) {
			f, c := newFakeDrive(t)
			state, root, req := archiveFixture(t, c)
			ctx := context.Background()
			if mutation == "missing" {
				f.mu.Lock()
				f.emptySuccess = true
				f.mu.Unlock()
			}
			result, err := c.Archive(ctx, state, "op", root, req)
			if mutation == "missing" {
				if err == nil || result.Verified {
					t.Fatal("HTTP success was mistaken for actual object")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			o := f.objects[result.Files[0].RemoteID]
			switch mutation {
			case "name":
				o.Name = "wrong"
			case "size":
				o.Size = "999"
			case "hash":
				o.SHA256 = strings.Repeat("0", 64)
			case "parent":
				o.Parents = []string{"different"}
			case "no-checksum":
				o.SHA256 = ""
				o.MD5 = ""
			case "md5":
				o.SHA256 = ""
			}
			f.objects[o.ID] = o
			f.mu.Unlock()
			verified, err := c.Verify(ctx, state, "op", root, req)
			if mutation == "md5" {
				if err != nil || !verified.Verified {
					t.Fatal(err)
				}
			} else if err == nil || verified.Verified {
				t.Fatal("unverified remote result", verified)
			}
			f.mu.Lock()
			puts := f.puts
			f.mu.Unlock()
			if puts != 1 {
				t.Fatal("verification uploaded")
			}
			if _, err = os.Stat(filepath.Join(root, "media.bin")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestArchiveRejectsNoneIncompleteTemporaryAndUnknownRemoteDirectory(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	for _, mode := range []string{"NONE", "unverified", "work", "missing-directory", "ambiguous-directory"} {
		copyReq := req
		copyReq.Files = append([]ArtifactFile{}, req.Files...)
		switch mode {
		case "NONE":
			copyReq.ArchivePolicy = "NONE"
		case "unverified":
			copyReq.Files[0].Verified = false
		case "work":
			copyReq.Files[0].RelativePath = ".work/media.bin"
		default:
			dir := "missing"
			if mode == "ambiguous-directory" {
				dir = "同名"
			}
			_ = os.MkdirAll(filepath.Join(root, dir), 0700)
			data, _ := os.ReadFile(filepath.Join(root, "media.bin"))
			_ = os.WriteFile(filepath.Join(root, dir, "media.bin"), data, 0600)
			copyReq.Files[0].RelativePath = dir + "/media.bin"
		}
		if got, err := c.Archive(context.Background(), state, mode, root, copyReq); err == nil || got.Verified {
			t.Fatal(mode, got)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 0 || f.writes != 0 || f.reserved != 0 {
		t.Fatal("invalid manifest caused remote write")
	}
}
