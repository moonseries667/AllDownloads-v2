package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestEngine(t *testing.T) (*Engine, string, string) {
	t.Helper()
	root := t.TempDir()
	state, output := filepath.Join(root, "state"), filepath.Join(root, "output")
	engine, err := Open(state, output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})
	return engine, state, output
}

func fixtureRequest(id string) Request {
	return Request{
		OperationID: id, ResourceKey: "fixture-resource-v1",
		RelativePath: "中文目录/fixture.txt", Content: "synthetic content\n",
	}
}

func TestExecuteIdempotencyAndInputConflict(t *testing.T) {
	engine, _, output := openTestEngine(t)
	req := fixtureRequest("op-repeat")
	first, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Execute(context.Background(), req)
	if err != nil || first != second {
		t.Fatalf("repeated result: receipt=%+v err=%v", second, err)
	}
	if first.Status != "SUCCEEDED" || first.SizeBytes != "18" {
		t.Fatalf("unexpected receipt: %+v", first)
	}
	data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(req.RelativePath)))
	if err != nil || string(data) != req.Content {
		t.Fatalf("output=%q err=%v", data, err)
	}
	req.Content += "different"
	if _, err := engine.Execute(context.Background(), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed input error = %v, want ErrConflict", err)
	}
}

func TestPendingRecoversPublishedFileWithoutReceipt(t *testing.T) {
	engine, state, output := openTestEngine(t)
	req := fixtureRequest("op-crash")
	injected := errors.New("simulated crash")
	engine.afterPublishHook = func(string) error { return injected }
	if _, err := engine.Execute(context.Background(), req); !errors.Is(err, injected) {
		t.Fatalf("first execution error = %v", err)
	}
	final := filepath.Join(output, filepath.FromSlash(req.RelativePath))
	before, err := os.Stat(final)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(state, output)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	pending, err := restarted.Pending()
	if err != nil || len(pending) != 1 || pending[0].OperationID != req.OperationID {
		t.Fatalf("Pending did not recover the published result: pending=%+v err=%v", pending, err)
	}
	after, err := os.Stat(final)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("recovery rewrote output: before=%v after=%v err=%v", before.ModTime(), after, err)
	}
}

func TestRestartAcknowledgementAndReceiptPersistence(t *testing.T) {
	engine, state, output := openTestEngine(t)
	req := fixtureRequest("op-ack")
	receipt, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := engine.Pending()
	if err != nil || len(pending) != 1 || pending[0] != receipt {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := engine.Acknowledge(req.OperationID, "java-event-1"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Acknowledge(req.OperationID, "java-event-1"); err != nil {
		t.Fatalf("same ack should be idempotent: %v", err)
	}
	if err := engine.Acknowledge(req.OperationID, "other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("different ack error = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(state, output)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	pending, err = restarted.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after ack=%+v err=%v", pending, err)
	}
	again, err := restarted.Execute(context.Background(), req)
	if err != nil || again.AckID != "java-event-1" {
		t.Fatalf("repeat after ack=%+v err=%v", again, err)
	}
}

func TestDamagedAndPreexistingFilesAreNotOverwritten(t *testing.T) {
	engine, _, output := openTestEngine(t)
	req := fixtureRequest("op-damage")
	if _, err := engine.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(output, filepath.FromSlash(req.RelativePath))
	if err := os.WriteFile(final, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("damaged file error = %v", err)
	}
	data, err := os.ReadFile(final)
	if err != nil || string(data) != "damaged" {
		t.Fatalf("damaged output changed: %q err=%v", data, err)
	}

	req2 := fixtureRequest("op-existing")
	final2 := filepath.Join(output, filepath.FromSlash(req2.RelativePath))
	if err := os.WriteFile(final2, []byte(req2.Content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), req2); !errors.Is(err, ErrConflict) {
		t.Fatalf("preexisting file error = %v", err)
	}
}

func TestPublishRaceDoesNotReplaceDestination(t *testing.T) {
	engine, _, output := openTestEngine(t)
	req := fixtureRequest("op-race")
	final := filepath.Join(output, filepath.FromSlash(req.RelativePath))
	engine.beforePublishHook = func(string) error {
		if err := os.MkdirAll(filepath.Dir(final), 0700); err != nil {
			return err
		}
		return os.WriteFile(final, []byte("external"), 0600)
	}
	if _, err := engine.Execute(context.Background(), req); err == nil {
		t.Fatal("publish unexpectedly replaced a destination created in the race window")
	}
	data, err := os.ReadFile(final)
	if err != nil || string(data) != "external" {
		t.Fatalf("racing destination changed: %q err=%v", data, err)
	}
}

func TestCanceledAndExpiredContextCreateNoIntent(t *testing.T) {
	engine, state, output := openTestEngine(t)
	req := fixtureRequest("op-canceled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Execute(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	req.OperationID = "op-expired"
	if _, err := engine.Execute(expired, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	for _, id := range []string{"op-canceled", "op-expired"} {
		if _, err := os.Stat(filepath.Join(state, "intents", id+".json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected intent %s: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(req.RelativePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected output: %v", err)
	}
}

func TestOneMiBNULContentSurvivesRestartAndPendingRead(t *testing.T) {
	engine, state, output := openTestEngine(t)
	req := fixtureRequest("op-nul-large")
	req.RelativePath = "large/zero-bytes.bin"
	req.Content = strings.Repeat("\x00", maxContentBytes)
	first, err := engine.Execute(context.Background(), req)
	if err != nil || first.SizeBytes != strconv.Itoa(maxContentBytes) {
		t.Fatalf("large NUL Execute receipt=%+v err=%v", first, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(state, output)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	pending, err := restarted.Pending()
	if err != nil || len(pending) != 1 || pending[0] != first {
		t.Fatalf("large NUL Pending=%+v err=%v", pending, err)
	}
}

func TestDangerousPathsAndOversizeContentAreRejected(t *testing.T) {
	engine, _, _ := openTestEngine(t)
	paths := []string{
		"", "C:\\outside", "\\rooted", "../outside", "a/../../outside",
		"a\\..\\outside", "CON.txt", "nested/Lpt9.log", "name.", "name ",
		"folder/stream:zone", ".work/op/file", "double//separator",
	}
	for i, path := range paths {
		req := fixtureRequest("op-path-" + strconv.Itoa(i))
		req.RelativePath = path
		if _, err := engine.Execute(context.Background(), req); err == nil {
			t.Errorf("accepted unsafe path %q", path)
		}
	}
	req := fixtureRequest("op-big")
	req.Content = strings.Repeat("x", maxContentBytes+1)
	if _, err := engine.Execute(context.Background(), req); err == nil {
		t.Fatal("accepted content larger than 1 MiB")
	}
}

func TestSymlinkPathIsRejected(t *testing.T) {
	engine, _, output := openTestEngine(t)
	target := filepath.Join(t.TempDir(), "target")
	link := filepath.Join(output, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	req := fixtureRequest("op-link")
	req.RelativePath = "linked/result.txt"
	if _, err := engine.Execute(context.Background(), req); err == nil {
		t.Fatal("accepted symlink path")
	}
}

func TestCaseInsensitiveDestinationCollisionIsRejected(t *testing.T) {
	engine, _, _ := openTestEngine(t)
	first := fixtureRequest("op-case-one")
	first.RelativePath = "Course/lesson.txt"
	if _, err := engine.Execute(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := fixtureRequest("op-case-two")
	second.RelativePath = "course/LESSON.txt"
	if _, err := engine.Execute(context.Background(), second); !errors.Is(err, ErrConflict) {
		t.Fatalf("case collision error = %v", err)
	}
}

func TestOutputRootIsBoundToStateDirectory(t *testing.T) {
	engine, state, _ := openTestEngine(t)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	otherOutput := filepath.Join(t.TempDir(), "other-output")
	if _, err := Open(state, otherOutput); !errors.Is(err, ErrConflict) {
		t.Fatalf("opening same state with another output root error = %v", err)
	}
}

func TestLockHolderProcess(t *testing.T) {
	if os.Getenv("GO_RECEIPT_LOCK_CHILD") != "1" {
		return
	}
	state := os.Getenv("GO_RECEIPT_LOCK_STATE")
	output := os.Getenv("GO_RECEIPT_LOCK_OUTPUT")
	signalPath := os.Getenv("GO_RECEIPT_LOCK_SIGNAL")
	engine, err := Open(state, output)
	if err != nil {
		if strings.Contains(err.Error(), "lock is already held") {
			if writeErr := os.WriteFile(signalPath, []byte("locked"), 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			return
		}
		t.Fatalf("child Open failed for a non-lock error: %v", err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), fixtureRequest("op-child-process")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signalPath, []byte("acquired"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func waitForSignal(t *testing.T, cmd *exec.Cmd, signalPath string, done <-chan error) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(signalPath); err == nil && len(data) > 0 {
			return string(data)
		}
		select {
		case err := <-done:
			t.Fatalf("helper exited before signal: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatalf("helper did not signal; process output may contain the cause")
	return ""
}

func startLockHelper(t *testing.T, state, output, signalPath string) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHolderProcess$")
	cmd.Env = append(os.Environ(),
		"GO_RECEIPT_LOCK_CHILD=1",
		"GO_RECEIPT_LOCK_STATE="+state,
		"GO_RECEIPT_LOCK_OUTPUT="+output,
		"GO_RECEIPT_LOCK_SIGNAL="+signalPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("lock helper process did not stop after Kill")
		}
	})
	return cmd, done
}

func waitForProcess(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err, ok := <-done:
		if !ok {
			return nil
		}
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("lock helper process did not exit before timeout")
		return nil
	}
}

func TestProcessCrashReleasesLocksAndPreservesReceipt(t *testing.T) {
	root := t.TempDir()
	state, output := filepath.Join(root, "state"), filepath.Join(root, "output")
	firstSignal := filepath.Join(root, "first.signal")
	first, firstDone := startLockHelper(t, state, output, firstSignal)
	if got := waitForSignal(t, first, firstSignal, firstDone); got != "acquired" {
		t.Fatalf("first helper signal=%q", got)
	}

	secondState := filepath.Join(root, "second-state")
	secondSignal := filepath.Join(root, "second.signal")
	second, secondDone := startLockHelper(t, secondState, output, secondSignal)
	if got := waitForSignal(t, second, secondSignal, secondDone); got != "locked" {
		_ = second.Process.Kill()
		_ = waitForProcess(t, secondDone)
		t.Fatalf("second state acquired same output root: signal=%q", got)
	}
	if err := waitForProcess(t, secondDone); err != nil {
		t.Fatalf("lock-conflict helper should exit cleanly: %v", err)
	}

	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := waitForProcess(t, firstDone); err == nil {
		t.Fatal("killed helper exited without an error")
	}
	restarted, err := Open(state, output)
	if err != nil {
		t.Fatalf("locks were not released after process death: %v", err)
	}
	defer restarted.Close()
	pending, err := restarted.Pending()
	if err != nil || len(pending) != 1 || pending[0].OperationID != "op-child-process" {
		t.Fatalf("receipt not available after process death: pending=%+v err=%v", pending, err)
	}
}

func TestConcurrentSameOperationPublishesOneReceipt(t *testing.T) {
	engine, _, output := openTestEngine(t)
	req := fixtureRequest("op-concurrent")
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	receipts := make(chan Receipt, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := engine.Execute(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			receipts <- receipt
		}()
	}
	wg.Wait()
	close(errs)
	close(receipts)
	for err := range errs {
		t.Errorf("execute failed: %v", err)
	}
	count := 0
	var first Receipt
	for receipt := range receipts {
		if count == 0 {
			first = receipt
		} else if receipt != first {
			t.Errorf("receipts differ: %+v vs %+v", first, receipt)
		}
		count++
	}
	if count != workers {
		t.Fatalf("successful calls=%d, want %d", count, workers)
	}
	pending, err := engine.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(req.RelativePath)))
	if err != nil || string(data) != req.Content {
		t.Fatalf("output=%q err=%v", data, err)
	}
}
