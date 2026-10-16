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
	if mount.starts != 2 || mount.closes != 1 {
		t.Fatalf("starts=%d closes=%d", mount.starts, mount.closes)
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
