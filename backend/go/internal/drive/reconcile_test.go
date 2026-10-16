package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

func driveEffects(f *fakeDrive) [3]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return [3]int{f.writes, f.puts, f.reserved}
}

func readUploadPlan(t *testing.T, state *executor.State, id string) string {
	t.Helper()
	var record uploadRecord
	if err := state.Read("upload-"+digest([]byte(id)), &record); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestReconcileMissingStateCannotResumeOrCreatePlan(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	before := driveEffects(f)
	result, err := c.Reconcile(context.Background(), state, "missing", root, req)
	if err != nil || result.Complete || result.SafeToResume || result.Archive != nil || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_NOT_CONFIRMED" {
		t.Fatal("missing state was treated as recoverable", result, err)
	}
	var record uploadRecord
	if err := state.Read("upload-"+digest([]byte("missing")), &record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reconciliation created a plan", err)
	}
	if after := driveEffects(f); after != before {
		t.Fatal("reconciliation wrote cloud state", before, after)
	}
}

func TestReconcileMissingReservedObjectIsSafeAndReadOnly(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	f.mu.Lock()
	f.emptySuccess = true
	f.mu.Unlock()
	if result, err := c.Archive(context.Background(), state, "reserved", root, req); err == nil || result.Verified {
		t.Fatal("fixture unexpectedly uploaded")
	}
	before, plan := driveEffects(f), readUploadPlan(t, state, "reserved")
	result, err := c.Reconcile(context.Background(), state, "reserved", root, req)
	if err != nil || result.Complete || !result.SafeToResume || result.Archive != nil || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_NOT_CONFIRMED" {
		t.Fatal("persisted reserved ID was not resumable", result, err)
	}
	if after := driveEffects(f); after != before || readUploadPlan(t, state, "reserved") != plan {
		t.Fatal("read-only reconciliation changed cloud or plan")
	}
}

func TestReconcileUnallocatedFutureItemsCanResume(t *testing.T) {
	for _, scenario := range []string{"first-response-lost", "first-verified"} {
		for _, relative := range []string{"second.bin", "future/second.bin"} {
			t.Run(scenario+"/"+relative, func(t *testing.T) {
				f, c := newFakeDrive(t)
				state, root, req := archiveFixture(t, c)
				data := []byte("later frozen output")
				local := filepath.Join(root, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(local, data, 0600); err != nil {
					t.Fatal(err)
				}
				req.Files = append(req.Files, ArtifactFile{OutputID: "second", RelativePath: relative, SizeBytes: strconv.Itoa(len(data)), SHA256: digest(data), MediaKinds: []string{"FILE"}, Verified: true})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if scenario == "first-response-lost" {
					f.mu.Lock()
					f.loseResponse = true
					f.mu.Unlock()
				} else {
					c.HTTP = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
						resp, err := http.DefaultTransport.RoundTrip(r)
						if err == nil && r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files/remote-1" && resp.StatusCode == http.StatusOK {
							raw, readErr := io.ReadAll(resp.Body)
							resp.Body.Close()
							if readErr != nil {
								return nil, readErr
							}
							resp.Body = io.NopCloser(bytes.NewReader(raw))
							// Interrupt after the first uploaded object is observed,
							// before the next loop can allocate any IDs or folders.
							cancel()
						}
						return resp, err
					})}
				}
				archive, err := c.Archive(ctx, state, "future-items", root, req)
				if err == nil || archive.Verified {
					t.Fatal("fixture did not interrupt the multi-file upload")
				}
				if scenario == "first-verified" && (!errors.Is(err, context.Canceled) || len(archive.Files) != 1 || !archive.Files[0].Verified) {
					t.Fatal("fixture did not finish verifying the first item before interruption", err)
				}
				var record uploadRecord
				if err := state.Read("upload-"+digest([]byte("future-items")), &record); err != nil {
					t.Fatal(err)
				}
				if record.RemoteIDs[req.Files[0].OutputID] == "" || record.RemoteIDs["second"] != "" || len(record.Folders) != 0 || driveEffects(f) != [3]int{1, 1, 1} {
					t.Fatal("fixture did not leave the second item unallocated")
				}
				// Reconciliation is a separate read-only operation with a fresh context.
				c.HTTP = f.server.Client()
				before, plan := driveEffects(f), readUploadPlan(t, state, "future-items")
				result, err := c.Reconcile(context.Background(), state, "future-items", root, req)
				if err != nil || result.Complete || !result.SafeToResume || result.Archive != nil || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_NOT_CONFIRMED" {
					t.Fatal("unallocated future item blocked the original upload", result, err)
				}
				if after := driveEffects(f); after != before || readUploadPlan(t, state, "future-items") != plan {
					t.Fatal("reconciliation allocated an ID, wrote cloud state, or changed the plan")
				}
				if relative == "second.bin" {
					resumed, err := c.Archive(context.Background(), state, "future-items", root, req)
					if err != nil || !resumed.Verified || len(resumed.Files) != 2 || driveEffects(f) != [3]int{2, 2, 2} {
						t.Fatal("resuming the original operation repeated the first upload or lost the second", err)
					}
				}
			})
		}
	}
}

func TestReconcileCompleteUsesOriginalIDAndExactJSON(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	f.mu.Lock()
	f.loseResponse = true
	f.mu.Unlock()
	if _, err := c.Archive(context.Background(), state, "original", root, req); err == nil {
		t.Fatal("fixture response was not lost")
	}
	before, plan := driveEffects(f), readUploadPlan(t, state, "original")
	result, err := c.Reconcile(context.Background(), state, "original", root, req)
	if err != nil || !result.Complete || result.SafeToResume || result.Archive == nil || !result.Archive.Verified || len(result.Archive.Files) != 1 || result.ReasonCode != nil {
		t.Fatal("complete archive was not recovered", result, err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(raw, &wire) != nil || len(wire) != 5 || wire["originalOperationId"] != "original" || wire["complete"] != true || wire["safeToResume"] != false || wire["archive"] == nil || wire["reasonCode"] != nil {
		t.Fatal("reconciliation JSON does not match the frozen contract")
	}
	if after := driveEffects(f); after != before || readUploadPlan(t, state, "original") != plan {
		t.Fatal("complete reconciliation changed cloud or plan")
	}
}

func TestReconcilePersistedPlanBeforeFirstReservationCanResume(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	c.HTTP = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/drive/v3/files/generateIds" {
			return nil, errors.New("synthetic failure before reservation")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	if _, err := c.Archive(context.Background(), state, "unreserved", root, req); !isCode(err, "NETWORK_ERROR") {
		t.Fatal("fixture did not stop before first reservation", err)
	}
	c.HTTP = f.server.Client()
	before, plan := driveEffects(f), readUploadPlan(t, state, "unreserved")
	result, err := c.Reconcile(context.Background(), state, "unreserved", root, req)
	if err != nil || result.Complete || !result.SafeToResume || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_NOT_CONFIRMED" {
		t.Fatal("a valid plan could not resume its never-sent first item", result, err)
	}
	if after := driveEffects(f); before != [3]int{} || after != before || readUploadPlan(t, state, "unreserved") != plan {
		t.Fatal("reconciliation allocated an ID or changed cloud state or plan")
	}
}

func TestReconcileMissingFolderMappingCannotHideAllocatedFile(t *testing.T) {
	f, c := newArchiveFolderDrive(t)
	state, root, req := archiveFolderFixture(t, c, "nested/media.bin")
	if _, err := c.Archive(context.Background(), state, "inconsistent", root, req); err != nil {
		t.Fatal(err)
	}
	name := "upload-" + digest([]byte("inconsistent"))
	var record uploadRecord
	if err := state.Read(name, &record); err != nil {
		t.Fatal(err)
	}
	// An allocated file with no recorded parent chain is not an unstarted item.
	record.Folders = map[string]folderRecord{}
	if err := state.Write(name, record); err != nil {
		t.Fatal(err)
	}
	plan := readUploadPlan(t, state, "inconsistent")
	f.mu.Lock()
	before := [4]int{f.ids, f.folderCreates, f.fileCreates, f.filePuts}
	f.mu.Unlock()
	result, err := c.Reconcile(context.Background(), state, "inconsistent", root, req)
	if err != nil || result.Complete || result.SafeToResume || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_NOT_CONFIRMED" {
		t.Fatal("an unchecked allocated file was treated as never sent", result, err)
	}
	f.mu.Lock()
	after := [4]int{f.ids, f.folderCreates, f.fileCreates, f.filePuts}
	f.mu.Unlock()
	if before != after || readUploadPlan(t, state, "inconsistent") != plan {
		t.Fatal("reconciliation changed cloud state or the inconsistent plan")
	}
}

func TestReconcileDistinguishesMissingReservedAndExistingFolders(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		t.Run(strconv.FormatBool(adopted), func(t *testing.T) {
			f, c := newArchiveFolderDrive(t)
			if adopted {
				f.mu.Lock()
				f.objects["existing"] = folderObject("existing", "nested", "root-id")
				f.mu.Unlock()
			}
			state, root, req := archiveFolderFixture(t, c, "nested/media.bin")
			if _, err := c.Archive(context.Background(), state, "folder", root, req); err != nil {
				t.Fatal(err)
			}
			var record uploadRecord
			if err := state.Read("upload-"+digest([]byte("folder")), &record); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			for _, folder := range record.Folders {
				delete(f.objects, folder.ID)
			}
			before := [4]int{f.ids, f.folderCreates, f.fileCreates, f.filePuts}
			f.mu.Unlock()
			plan := readUploadPlan(t, state, "folder")
			result, err := c.Reconcile(context.Background(), state, "folder", root, req)
			if err != nil || result.Complete || result.SafeToResume == adopted || result.Archive != nil || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_NOT_CONFIRMED" {
				t.Fatal("missing folder did not retain its reserved/adopted identity", result, err)
			}
			f.mu.Lock()
			after := [4]int{f.ids, f.folderCreates, f.fileCreates, f.filePuts}
			f.mu.Unlock()
			if before != after || readUploadPlan(t, state, "folder") != plan {
				t.Fatal("folder reconciliation changed cloud or plan")
			}
		})
	}
}

func TestReconcileMissingFirstObjectCannotHideLaterConflict(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	second, err := os.ReadFile(filepath.Join(root, "unselected.bin"))
	if err != nil {
		t.Fatal(err)
	}
	req.Files = append(req.Files, ArtifactFile{OutputID: "second", RelativePath: "unselected.bin", SizeBytes: strconv.Itoa(len(second)), SHA256: digest(second), MediaKinds: []string{"FILE"}, Verified: true})
	archive, err := c.Archive(context.Background(), state, "mixed", root, req)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.objects, archive.Files[0].RemoteID)
	conflict := f.objects[archive.Files[1].RemoteID]
	conflict.SHA256 = strings.Repeat("0", 64)
	f.objects[conflict.ID] = conflict
	f.mu.Unlock()
	before := driveEffects(f)
	result, err := c.Reconcile(context.Background(), state, "mixed", root, req)
	if err != nil || result.Complete || result.SafeToResume || result.Archive != nil || result.ReasonCode == nil || *result.ReasonCode != "ARCHIVE_VERIFICATION_FAILED" {
		t.Fatal("missing early object hid later checksum conflict", result, err)
	}
	if after := driveEffects(f); after != before {
		t.Fatal("conflicting reconciliation wrote cloud state")
	}
}

func TestReconcileDoesNotResumeTargetLocalAuthOrNetworkFailures(t *testing.T) {
	for _, mode := range []string{"target", "local", "auth", "network", "manifest"} {
		t.Run(mode, func(t *testing.T) {
			f, c := newFakeDrive(t)
			state, root, req := archiveFixture(t, c)
			if _, err := c.Archive(context.Background(), state, "failure", root, req); err != nil {
				t.Fatal(err)
			}
			want := ""
			switch mode {
			case "target":
				f.mu.Lock()
				obj := f.objects["root-id"]
				obj.Name = "changed"
				f.objects[obj.ID] = obj
				f.mu.Unlock()
				want = "TARGET_CHANGED"
			case "local":
				if err := os.WriteFile(filepath.Join(root, "media.bin"), []byte("different"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "LOCAL_VERIFICATION_FAILED"
			case "auth":
				c.Token = func() string { return "" }
				want = "AUTH_REQUIRED"
			case "network":
				c.HTTP = &http.Client{Transport: authRoundTrip(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("synthetic private network details")
				})}
				want = "NETWORK_ERROR"
			case "manifest":
				req.ProjectID = "another-project"
				want = "ARCHIVE_MANIFEST_CHANGED"
			}
			before := driveEffects(f)
			result, err := c.Reconcile(context.Background(), state, "failure", root, req)
			if err != nil || result.Complete || result.SafeToResume || result.Archive != nil || result.ReasonCode == nil || *result.ReasonCode != want {
				t.Fatal("unproven reconciliation was considered safe", result, err)
			}
			if after := driveEffects(f); after != before {
				t.Fatal("failed reconciliation wrote cloud state")
			}
		})
	}
}

func TestReconcileCancellationIsAnError(t *testing.T) {
	f, c := newFakeDrive(t)
	state, root, req := archiveFixture(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := c.Reconcile(ctx, state, "cancelled", root, req)
	if !errors.Is(err, context.Canceled) || result.SafeToResume || result.Complete || result.Archive != nil {
		t.Fatal(result, err)
	}
	if effects := driveEffects(f); effects != [3]int{} {
		t.Fatal("cancelled reconciliation wrote cloud state")
	}
}
