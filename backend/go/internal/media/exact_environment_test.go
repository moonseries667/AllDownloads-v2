package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type exactOverlayReport struct {
	Inherited, Overridden, Added bool
}

func TestExactEnvironmentFixture(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--exact-environment-fixture" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	mode, path := os.Args[index+1], os.Args[index+2]
	switch mode {
	case "overlay":
		report := exactOverlayReport{
			Inherited:  os.Getenv("RCLONE_EXACT_PARENT") == "synthetic-parent-setting",
			Overridden: os.Getenv("RCLONE_EXACT_OVERRIDE") == "child-value",
			Added:      os.Getenv("EXACT_ADDED") == "yes",
		}
		raw, _ := json.Marshal(report)
		if os.WriteFile(path, raw, 0600) != nil {
			os.Exit(2)
		}
	case "bounded-failure":
		secret := os.Getenv("EXACT_PRIVATE")
		fmt.Fprint(os.Stdout, strings.Repeat(secret, (2<<20)/len(secret)+1))
		fmt.Fprint(os.Stderr, strings.Repeat(secret, (64<<10)/len(secret)+1))
		os.Exit(7)
	default:
		os.Exit(3)
	}
}

func exactFixtureArgs(mode, path string) []string {
	return []string{"-test.run=^TestExactEnvironmentFixture$", "--", "--exact-environment-fixture", mode, path}
}

func TestExactControlledEnvironmentIncludesNilAndEmptyWithoutParentInheritance(t *testing.T) {
	t.Setenv("RCLONE_EXACT_PARENT", "synthetic-parent-setting")
	t.Setenv("MANAGED_EXACT", "parent-value")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
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
			path := filepath.Join(t.TempDir(), "exact-report.json")
			_, _, err := RunControlledExactEnvironment(context.Background(), executable, 5*time.Second, test.environment, managedFixtureArgs("environment", path)...)
			if err != nil {
				t.Fatal("exact environment fixture", err)
			}
			raw, err := os.ReadFile(path)
			var report managedEnvironmentReport
			if err != nil || json.Unmarshal(raw, &report) != nil {
				t.Fatal("exact environment report unavailable", err)
			}
			if report.ExactValue != test.exact || report.RclonePresent || report.Unexpected {
				t.Fatal("exact environment inherited or replaced parent settings")
			}
			if os.Getenv("RCLONE_EXACT_PARENT") != "synthetic-parent-setting" || os.Getenv("MANAGED_EXACT") != "parent-value" {
				t.Fatal("exact environment changed the parent")
			}
		})
	}
}

func TestExactEnvironmentRefactorPreservesOverlayAndDefaultInheritance(t *testing.T) {
	t.Setenv("RCLONE_EXACT_PARENT", "synthetic-parent-setting")
	t.Setenv("RCLONE_EXACT_OVERRIDE", "parent-value")
	t.Setenv("EXACT_ADDED", "parent-value")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		environment map[string]string
		overridden  bool
	}{
		{"overlay", map[string]string{"rclone_exact_override": "child-value", "EXACT_ADDED": "yes"}, true},
		{"nil", nil, false},
		{"empty", map[string]string{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "overlay-report.json")
			// A non-positive timeout retains the existing ten-minute default,
			// rather than becoming an immediately expired tool invocation.
			_, _, err := RunControlledEnvironment(context.Background(), executable, 0, test.environment, exactFixtureArgs("overlay", path)...)
			if err != nil {
				t.Fatal("overlay environment fixture", err)
			}
			raw, err := os.ReadFile(path)
			var report exactOverlayReport
			if err != nil || json.Unmarshal(raw, &report) != nil {
				t.Fatal("overlay environment report unavailable", err)
			}
			if !report.Inherited || report.Overridden != test.overridden || report.Added != test.overridden {
				t.Fatal("existing overlay/inheritance behavior changed")
			}
			if os.Getenv("RCLONE_EXACT_OVERRIDE") != "parent-value" || os.Getenv("EXACT_ADDED") != "parent-value" {
				t.Fatal("overlay changed the parent environment")
			}
		})
	}
}

func TestExactControlledOutputIsBoundedAndErrorsDoNotEchoDiagnostics(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const secret = "synthetic-private-authentication-value"
	output, diagnostic, err := RunControlledExactEnvironment(context.Background(), executable, 5*time.Second,
		[]string{"EXACT_PRIVATE=" + secret}, exactFixtureArgs("bounded-failure", "unused")...)
	if len(output) != 1<<20 || len(diagnostic) != 16<<10 {
		t.Fatal("controlled stdout/stderr bounds changed")
	}
	if !bytes.HasPrefix(output, []byte(secret)) || !bytes.HasPrefix(diagnostic, []byte(secret)) {
		t.Fatal("private adapter output was not retained for internal parsing")
	}
	if err == nil || err.Error() != "TOOL: TOOL_FAILED" || strings.Contains(err.Error(), secret) {
		t.Fatal("raw diagnostics escaped into the tool error")
	}
	missing := filepath.Join(t.TempDir(), secret+".exe")
	output, diagnostic, err = RunControlledExactEnvironment(context.Background(), missing, time.Second,
		[]string{"EXACT_PRIVATE=" + secret}, secret)
	if output != nil || diagnostic != nil || err == nil || err.Error() != "TOOL: TOOL_START_FAILED" {
		t.Fatal("startup failure exposed private executable, arguments or environment")
	}
}

func TestExactControlledCancellationStopsOnlyItsOwnSubtree(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	ownedPath, siblingPath := filepath.Join(directory, "owned"), filepath.Join(directory, "sibling")
	sibling := managedTestProcess(t, context.Background(), "heartbeat", siblingPath, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, _, err := RunControlledExactEnvironment(ctx, executable, 10*time.Second, nil, managedFixtureArgs("tree", ownedPath)...)
		finished <- err
	}()
	if !managedAwaitFile(ownedPath, 5*time.Second) || !managedAwaitFile(siblingPath, 5*time.Second) {
		t.Fatal("owned exact-environment subtree and sibling did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrProcessStopUnconfirmed) {
			t.Fatal("exact environment cancellation was not confirmed", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("exact environment tool did not stop within its bound")
	}
	beforeOwned, beforeSibling := managedReadHeartbeat(t, ownedPath), managedReadHeartbeat(t, siblingPath)
	time.Sleep(100 * time.Millisecond)
	if managedReadHeartbeat(t, ownedPath) != beforeOwned {
		t.Fatal("exact environment cancellation left a descendant running")
	}
	if managedReadHeartbeat(t, siblingPath) == beforeSibling {
		t.Fatal("exact environment cancellation affected an unrelated sibling")
	}
	select {
	case <-sibling.Done():
		t.Fatal("unrelated sibling exited")
	default:
	}
}
