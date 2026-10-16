package runner

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/folder"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

type pickerFunc func(context.Context, time.Duration) folder.Result

func (p pickerFunc) Pick(ctx context.Context, d time.Duration) folder.Result { return p(ctx, d) }

func TestNativeSelectionUsesHostValidationAndDoesNotSaveSettings(t *testing.T) {
	root := t.TempDir()
	h := &Handler{AllowedRoots: []string{root}, Roots: map[string]string{"root-id": root}}
	op := testOperation()
	op.OperationType = "SELECT_LOCAL_DIRECTORY"
	op.Payload = raw(map[string]any{"schemaVersion": 1, "selectionId": "select", "title": "选择本机下载目录", "timeoutSeconds": 120})
	w := &StorageWorker{Roots: h, HostID: "opaque-host", PickerReady: func() bool { return true }}
	for _, status := range []string{"SELECTED", "CANCELLED", "UNAVAILABLE"} {
		w.Picker = pickerFunc(func(context.Context, time.Duration) folder.Result { return folder.Result{Status: status, Path: root} })
		value, files, err := w.Handle(context.Background(), op)
		if status == "UNAVAILABLE" {
			if media.Classify(err, "").Code != "NATIVE_PICKER_UNAVAILABLE" {
				t.Fatal(err)
			}
			continue
		}
		if err != nil || len(files) != 0 {
			t.Fatal(err)
		}
		result := value.(map[string]any)
		if result["cancelled"] != (status == "CANCELLED") {
			t.Fatal(result)
		}
		if status == "CANCELLED" && result["target"] != nil {
			t.Fatal("cancel created target")
		}
		if status == "SELECTED" {
			target := result["target"].(map[string]any)
			if target["canonicalPath"] != root || target["rootId"] != "root-id" {
				t.Fatal(target)
			}
		}
	}
	w.Picker = pickerFunc(func(context.Context, time.Duration) folder.Result {
		t.Fatal("unavailable picker invoked")
		return folder.Result{}
	})
	w.PickerReady = func() bool { return false }
	if _, _, err := w.Handle(context.Background(), op); media.Classify(err, "").Code != "NATIVE_PICKER_UNAVAILABLE" {
		t.Fatal(err)
	}
	fact := w.RegistrationFacts()["directoryPicker"].(map[string]any)
	if fact["available"] != false || fact["hostId"] != "opaque-host" {
		t.Fatal(fact)
	}
}

type uploadNoNetworkTransport struct{ t *testing.T }

func (n uploadNoNetworkTransport) RoundTrip(*http.Request) (*http.Response, error) {
	n.t.Fatal("missing authentication must not call Drive")
	return nil, errors.New("unexpected I/O")
}

type uploadProxyFailureTransport struct{}

func (uploadProxyFailureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("synthetic proxy unavailable")
}

func TestUploadAuthenticationPreflightReportsNoWriteEffects(t *testing.T) {
	root := t.TempDir()
	state, err := executor.OpenState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	client, err := drive.NewGoogle(drive.GoogleConfig{AccountID: "drive-main", TokenFile: filepath.Join(root, "new-auth.session")})
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: uploadNoNetworkTransport{t}}
	op := testOperation()
	op.OperationID, op.OperationType = "upload-new", "UPLOAD_ARTIFACT"
	accountID := "drive-main"
	op.AccountRef = &accountID
	op.Destination = &Destination{RootID: "root-id", RootPath: root}
	op.Payload = raw(drive.ArchiveRequest{SchemaVersion: 1, ProjectID: "project-new", ArchivePolicy: "AUTO", ArchiveTarget: drive.Target{AccountID: accountID}, Files: []drive.ArtifactFile{{OutputID: "file-new", RelativePath: "file.txt", SizeBytes: "1", SHA256: "fixture", Verified: true}}})
	worker := &StorageWorker{State: state, Roots: &Handler{AllowedRoots: []string{root}, Roots: map[string]string{"root-id": root}}, Drives: map[string]*drive.Client{"drive-main": client}}
	for _, configured := range []bool{true, false} {
		if !configured {
			worker.Drives = nil
		}
		_, files, err := worker.Handle(context.Background(), op)
		var proof interface{ RunnerFailure() (string, *ResultError) }
		if !errors.As(err, &proof) {
			t.Fatalf("authentication must carry its no-effects proof: %v", err)
		}
		status, detail := proof.RunnerFailure()
		if status != "NEEDS_ACTION" || detail.Code != "AUTH_REQUIRED" || len(files) != 0 || len(detail.CompletedEffects) != 0 || len(detail.PendingEffects) != 0 {
			t.Fatalf("unexpected preflight proof: %s, %#v", status, detail)
		}
	}
	names, err := state.Names("upload-")
	if err != nil || len(names) != 0 {
		t.Fatalf("authentication failure created an upload intent: %v, %v", names, err)
	}
	worker.Drives = map[string]*drive.Client{"drive-main": {AccountID: "drive-main", Token: func() string { return "synthetic-token" }, HTTP: &http.Client{Transport: uploadProxyFailureTransport{}}}}
	_, _, err = worker.Handle(context.Background(), op)
	var proxyProof interface{ RunnerFailure() (string, *ResultError) }
	if !errors.As(err, &proxyProof) {
		t.Fatalf("read-only proxy failure lost its no-effects proof: %v", err)
	}
	_, detail := proxyProof.RunnerFailure()
	if detail.Code != "NETWORK_ERROR" || len(detail.CompletedEffects) != 0 || len(detail.PendingEffects) != 0 {
		t.Fatalf("proxy preflight was misclassified: %#v", detail)
	}
}
