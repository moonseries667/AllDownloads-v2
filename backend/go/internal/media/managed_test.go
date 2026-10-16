package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type managedEnvironmentReport struct {
	ExactValue, RclonePresent, Unexpected bool
}

// This test executable acts only as its own local, disposable tool fixture.
// No installed rclone, mount, platform session or other user's process is used.
func TestManagedProcessFixture(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--managed-fixture" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	mode, path := os.Args[index+1], os.Args[index+2]
	switch mode {
	case "environment":
		report := managedEnvironmentReport{ExactValue: os.Getenv("MANAGED_EXACT") == "child-value"}
		for _, item := range os.Environ() {
			name, _, _ := strings.Cut(item, "=")
			report.RclonePresent = report.RclonePresent || strings.HasPrefix(strings.ToUpper(name), "RCLONE_")
			if name != "MANAGED_EXACT" && !strings.EqualFold(name, "SYSTEMROOT") {
				report.Unexpected = true
			}
		}
		raw, _ := json.Marshal(report)
		if os.WriteFile(path, raw, 0600) != nil {
			os.Exit(2)
		}
		return
	case "exit":
		return
	case "fail":
		fmt.Fprint(os.Stdout, strings.Repeat("private-tool-output", 1<<15))
		fmt.Fprint(os.Stderr, strings.Repeat("private-tool-diagnostic", 1<<15))
		os.Exit(7)
	case "tree", "exit-with-child":
		executable, _ := os.Executable()
		child := exec.Command(executable, managedFixtureArgs("heartbeat", path)...)
		if child.Start() != nil {
			os.Exit(3)
		}
		if mode == "exit-with-child" {
			if !managedAwaitFile(path, 5*time.Second) {
				os.Exit(4)
			}
			return
		}
		for {
			time.Sleep(time.Second)
		}
	case "heartbeat":
		for i := 0; ; i++ {
			if os.WriteFile(path, []byte(strconv.Itoa(i)), 0600) != nil {
				os.Exit(5)
			}
			time.Sleep(15 * time.Millisecond)
		}
	default:
		os.Exit(6)
	}
}

func managedFixtureArgs(mode, path string) []string {
	return []string{"-test.run=^TestManagedProcessFixture$", "--", "--managed-fixture", mode, path}
}

func managedAwaitFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func managedTestProcess(t *testing.T, ctx context.Context, mode, path string, environment []string) *ManagedProcess {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := StartManaged(ctx, executable, managedFixtureArgs(mode, path), environment)
	if err != nil {
		t.Fatal("managed fixture start", err)
	}
	t.Cleanup(func() {
		if err := process.Stop(); err != nil {
			t.Error("managed fixture cleanup", err)
		}
	})
	return process
}

func managedAwaitDone(t *testing.T, process *ManagedProcess) {
	t.Helper()
	select {
	case <-process.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("owned fixture termination was not confirmed")
	}
}

func managedReadHeartbeat(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("owned fixture heartbeat", err)
	}
	return string(data)
}

func TestManagedEnvironmentIsExactIncludingNilAndEmpty(t *testing.T) {
	t.Setenv("RCLONE_MANAGED_PARENT", "private-parent-setting")
	t.Setenv("MANAGED_EXACT", "parent-value")
	for _, test := range []struct {
		name        string
		environment []string
		exact       bool
	}{
		{"specified", []string{"MANAGED_EXACT=child-value"}, true},
		{"nil", nil, false},
		{"empty", []string{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "environment-report.json")
			process := managedTestProcess(t, context.Background(), "environment", path, test.environment)
			managedAwaitDone(t, process)
			if process.Err() != nil {
				t.Fatal("environment fixture failed", process.Err())
			}
			raw, err := os.ReadFile(path)
			var report managedEnvironmentReport
			if err != nil || json.Unmarshal(raw, &report) != nil {
				t.Fatal("environment report unavailable", err)
			}
			if report.ExactValue != test.exact || report.RclonePresent || report.Unexpected {
				t.Fatal("child inherited or changed its exact environment")
			}
			if os.Getenv("RCLONE_MANAGED_PARENT") != "private-parent-setting" || os.Getenv("MANAGED_EXACT") != "parent-value" {
				t.Fatal("managed start modified the parent's environment")
			}
		})
	}
}

func TestManagedStopAndContextCancellationReapOnlyOwnedTree(t *testing.T) {
	for _, mode := range []string{"stop", "context"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			ownedPath, siblingPath := filepath.Join(directory, "owned"), filepath.Join(directory, "sibling")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owned := managedTestProcess(t, ctx, "tree", ownedPath, nil)
			sibling := managedTestProcess(t, context.Background(), "heartbeat", siblingPath, nil)
			if owned.PID() <= 0 || sibling.PID() <= 0 || owned.PID() == sibling.PID() {
				t.Fatal("managed processes did not retain distinct PIDs")
			}
			if !managedAwaitFile(ownedPath, 5*time.Second) || !managedAwaitFile(siblingPath, 5*time.Second) {
				t.Fatal("owned subtree and sibling did not start")
			}
			if owned.Err() != nil {
				t.Fatal("running managed process has an exit error")
			}
			if mode == "context" {
				cancel()
			} else {
				var callers sync.WaitGroup
				failures := make(chan error, 12)
				for range 12 {
					callers.Go(func() { failures <- owned.Stop() })
				}
				callers.Wait()
				close(failures)
				for err := range failures {
					if err != nil {
						t.Fatal("concurrent Stop did not confirm termination", err)
					}
				}
			}
			managedAwaitDone(t, owned)
			if !errors.Is(owned.Err(), context.Canceled) || errors.Is(owned.Err(), ErrProcessStopUnconfirmed) {
				t.Fatal("context stop result", owned.Err())
			}
			ownedBefore := managedReadHeartbeat(t, ownedPath)
			siblingBefore := managedReadHeartbeat(t, siblingPath)
			time.Sleep(100 * time.Millisecond)
			if managedReadHeartbeat(t, ownedPath) != ownedBefore {
				t.Fatal("owned descendant survived a confirmed stop")
			}
			if managedReadHeartbeat(t, siblingPath) == siblingBefore {
				t.Fatal("stopping an owned tree affected its unrelated sibling")
			}
			select {
			case <-sibling.Done():
				t.Fatal("unrelated sibling exited")
			default:
			}
			if err := owned.Stop(); err != nil {
				t.Fatal("repeated Stop failed", err)
			}
		})
	}
}

func TestManagedLifetimeKeepsCallerDeadlineAndHonorsShortDeadline(t *testing.T) {
	longCtx, longCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer longCancel()
	long := managedTestProcess(t, longCtx, "heartbeat", filepath.Join(t.TempDir(), "long"), nil)
	wantDeadline, _ := longCtx.Deadline()
	if deadline, ok := long.ctx.Deadline(); !ok || !deadline.Equal(wantDeadline) {
		t.Fatal("long-lived tool acquired an earlier implicit deadline")
	}
	background := managedTestProcess(t, context.Background(), "heartbeat", filepath.Join(t.TempDir(), "background"), nil)
	if _, ok := background.ctx.Deadline(); ok {
		t.Fatal("background lifetime acquired an implicit deadline")
	}
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shortCancel()
	shortPath := filepath.Join(t.TempDir(), "short")
	short := managedTestProcess(t, shortCtx, "tree", shortPath, nil)
	if !managedAwaitFile(shortPath, time.Second) {
		t.Fatal("short-deadline subtree did not start")
	}
	managedAwaitDone(t, short)
	if !errors.Is(short.Err(), context.DeadlineExceeded) {
		t.Fatal("caller deadline was not retained", short.Err())
	}
	before := managedReadHeartbeat(t, shortPath)
	time.Sleep(80 * time.Millisecond)
	if managedReadHeartbeat(t, shortPath) != before {
		t.Fatal("deadline left an owned descendant running")
	}
}

func TestManagedNaturalExitReapsDescendantsAndControlsDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "natural-child")
	natural := managedTestProcess(t, context.Background(), "exit-with-child", path, nil)
	managedAwaitDone(t, natural)
	if natural.Err() != nil || natural.Stop() != nil || natural.Stop() != nil {
		t.Fatal("natural exit or repeated Stop failed", natural.Err())
	}
	before := managedReadHeartbeat(t, path)
	time.Sleep(80 * time.Millisecond)
	if managedReadHeartbeat(t, path) != before {
		t.Fatal("natural root exit left an orphan descendant")
	}
	failed := managedTestProcess(t, context.Background(), "fail", filepath.Join(t.TempDir(), "unused"), nil)
	managedAwaitDone(t, failed)
	var fault *Failure
	if !errors.As(failed.Err(), &fault) || fault.Code != "TOOL_FAILED" || failed.Err().Error() != "TOOL: TOOL_FAILED" {
		t.Fatal("raw diagnostic or exit details escaped the managed tool boundary")
	}
	if err := failed.Stop(); err != nil {
		t.Fatal("confirmed failed process could not be stopped idempotently", err)
	}
}

func TestManagedStartFailuresAreControlledAndPreCancelledStartsDoNotRun(t *testing.T) {
	if process, err := StartManaged(context.Background(), "relative-tool.exe", nil, nil); process != nil || Classify(err, "TOOL").Code != "TOOL_NOT_CONFIGURED" {
		t.Fatal("relative executable was accepted")
	}
	missing := filepath.Join(t.TempDir(), "private-tool-not-present.exe")
	process, err := StartManaged(context.Background(), missing, []string{"private-argument"}, []string{"PRIVATE_ENV=private-value"})
	if process != nil || err == nil || err.Error() != "TOOL: TOOL_START_FAILED" {
		t.Fatal("startup failure exposed private tool configuration")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "must-not-start")
	process, err = StartManaged(ctx, executable, managedFixtureArgs("heartbeat", path), nil)
	if process != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled start did not retain the context result")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-cancelled start launched a fixture")
	}
}
