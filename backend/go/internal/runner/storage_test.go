package runner

import (
	"context"
	"testing"
	"time"

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
