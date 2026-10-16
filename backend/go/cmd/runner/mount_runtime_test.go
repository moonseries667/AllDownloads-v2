package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
)

type mountFixture struct {
	mu             sync.Mutex
	status         drive.MountStatus
	starts, closes int
	mounted        chan struct{}
	closeErr       error
}

func (m *mountFixture) Status() drive.MountStatus { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *mountFixture) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starts++
	if m.starts == 1 {
		m.status = drive.MountStatus{State: "ERROR", ReasonCode: "MOUNT_PROXY_UNAVAILABLE"}
		return errors.New("controlled proxy unavailable")
	}
	m.status = drive.MountStatus{State: "MOUNTED"}
	close(m.mounted)
	return nil
}
func (m *mountFixture) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.closes++; return m.closeErr }

func TestMountSupervisorRecoversWithoutRestartingTheApplication(t *testing.T) {
	mount := &mountFixture{mounted: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- maintainMount(ctx, mount, 5*time.Millisecond) }()
	select {
	case <-mount.mounted:
	case <-time.After(time.Second):
		t.Fatal("proxy recovery did not start the same managed mount")
	}
	// The healthy mount is not spawned a second time on the next retry tick.
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mount.mu.Lock()
	defer mount.mu.Unlock()
	if mount.starts != 2 || mount.closes != 3 {
		t.Fatalf("starts=%d closes=%d", mount.starts, mount.closes)
	}
}

type recoveringMountFixture struct {
	mu                      sync.Mutex
	status                  drive.MountStatus
	owned                   bool
	starts, closes          int
	startedBeforeCleanup    bool
	firstStartAfterClose    int
	mounted                 chan struct{}
	initialStopErr, stopErr error
}

func (m *recoveringMountFixture) Status() drive.MountStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}
func (m *recoveringMountFixture) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starts++
	if m.starts == 1 {
		m.firstStartAfterClose = m.closes
	}
	if m.owned {
		m.startedBeforeCleanup = true
		return errors.New("owned mount has not stopped")
	}
	m.owned = true
	m.status = drive.MountStatus{State: "MOUNTED"}
	if m.starts == 1 {
		close(m.mounted)
	}
	return nil
}
func (m *recoveringMountFixture) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closes++
	if m.closes == 1 {
		return m.initialStopErr
	}
	if m.closes == 2 {
		m.owned = false
		m.status = drive.MountStatus{State: "STOPPED"}
		return nil
	}
	return m.stopErr
}

func TestMountSupervisorRetriesUnconfirmedCleanupBeforeRestart(t *testing.T) {
	finalStopErr := errors.New("final owned mount stop remains unconfirmed")
	mount := &recoveringMountFixture{status: drive.MountStatus{State: "ERROR", ReasonCode: "MOUNT_STOP_UNCONFIRMED"}, owned: true, mounted: make(chan struct{}), initialStopErr: errors.New("old owned mount stop unconfirmed"), stopErr: finalStopErr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- maintainMount(ctx, mount, 5*time.Millisecond) }()
	select {
	case <-mount.mounted:
	case <-time.After(time.Second):
		t.Fatal("mount did not recover after old process cleanup became confirmed")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, finalStopErr) {
			t.Fatalf("final cleanup failure was hidden: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("mount supervisor did not finish final cleanup")
	}
	mount.mu.Lock()
	defer mount.mu.Unlock()
	if mount.startedBeforeCleanup || mount.starts != 1 || mount.firstStartAfterClose != 2 || mount.closes != 3 {
		t.Fatalf("unconfirmed ownership permitted restart: starts=%d closes=%d firstStartAfterClose=%d premature=%v", mount.starts, mount.closes, mount.firstStartAfterClose, mount.startedBeforeCleanup)
	}
}

func TestMountFactsNeverPublishProcessStartupAsMounted(t *testing.T) {
	for _, candidate := range []drive.MountStatus{{State: "STOPPED"}, {State: "STARTING"}, {State: "ERROR", ReasonCode: "MOUNT_PROXY_UNAVAILABLE"}, {State: "ERROR", ReasonCode: "WINFSP_REQUIRED"}} {
		fact := mountFacts(candidate)["mount"].(map[string]any)
		if fact["state"] == "MOUNTED" || fact["reasonCode"] == nil {
			t.Fatalf("unverified mount presented as ready: %#v", fact)
		}
	}
	ready := mountFacts(drive.MountStatus{State: "MOUNTED"})["mount"].(map[string]any)
	if ready["state"] != "MOUNTED" || ready["reasonCode"] != (*string)(nil) {
		t.Fatal(ready)
	}
	stopErr := errors.New("controlled stop unconfirmed")
	mount := &mountFixture{mounted: make(chan struct{}), closeErr: stopErr}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := maintainMount(ctx, mount, time.Second); !errors.Is(err, stopErr) {
		t.Fatal("unconfirmed cleanup was hidden", err)
	}
}
