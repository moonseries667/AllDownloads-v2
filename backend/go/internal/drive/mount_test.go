package drive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureMountProcess struct {
	done      chan struct{}
	once      sync.Once
	drive     *atomic.Bool
	stopFails atomic.Bool
	stops     atomic.Int32
}

func (p *fixtureMountProcess) Done() <-chan struct{} { return p.done }
func (p *fixtureMountProcess) Stop() error {
	p.stops.Add(1)
	if p.stopFails.Load() {
		return errors.New("synthetic unconfirmed stop")
	}
	p.once.Do(func() { p.drive.Store(false); close(p.done) })
	return nil
}

func mountFixture(t *testing.T) (*Mounter, *fixtureMountProcess, *rcloneFixture) {
	t.Helper()
	_, c, auth := newRcloneFixture(t)
	drive := &atomic.Bool{}
	p := &fixtureMountProcess{done: make(chan struct{}), drive: drive}
	starter := func(ctx context.Context, exe string, args, environment []string) (MountProcess, error) {
		if exe != c.rclone.config.Executable || args[0] != "mount" || args[1] != "gdrive:" || args[2] != "G:" || !containsPair(args, "--config", c.rclone.config.ConfigFile) || !containsPair(args, "--volname", "MyGDrive") || !containsPair(args, "--vfs-cache-mode", "full") {
			t.Error("mount did not use the exact authorized configuration")
		}
		for _, arg := range args {
			if strings.Contains(arg, "fixture-only") || strings.Contains(arg, "synthetic-do-not") {
				t.Error("mount argument contained a credential")
			}
		}
		for _, entry := range environment {
			key, value, _ := strings.Cut(entry, "=")
			upper := strings.ToUpper(key)
			if strings.HasPrefix(upper, "RCLONE_") && upper != "RCLONE_CONFIG_PASS" {
				t.Error("mount environment inherited remote overrides")
			}
			if upper == "NO_PROXY" && value != "" {
				t.Error("mount bypassed WARP")
			}
		}
		drive.Store(true)
		return p, nil
	}
	m, err := NewMounter(c, MountConfig{CacheDir: filepath.Join(t.TempDir(), "runtime", "cache", "gdrive")}, starter)
	if err != nil {
		t.Fatal(err)
	}
	m.pollInterval = 5 * time.Millisecond
	m.probe = func() (mountObservation, error) {
		present := drive.Load()
		return mountObservation{occupied: present, matches: present}, nil
	}
	m.winFsp = func() error { return nil }
	m.proxyReady = func(context.Context) error { return nil }
	t.Cleanup(func() { p.stopFails.Store(false); _ = m.Close() })
	return m, p, auth
}

func TestMountRequiresOwnedProcessAndActualDriveThenStopsOnlyOwnedProcess(t *testing.T) {
	m, p, _ := mountFixture(t)
	if m.Status().State != "STOPPED" {
		t.Fatal("constructor pretended to mount")
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(); status.State != "MOUNTED" || status.MountPoint != "G:" || status.Remote != "gdrive" {
		t.Fatal(status)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if m.Status().State != "STOPPED" || p.stops.Load() != 1 {
		t.Fatal("owned process did not stop")
	}
	if err := m.Close(); err != nil || p.stops.Load() != 1 {
		t.Fatal("Close was not idempotent")
	}
}

func TestMountRejectsOccupiedDriveMissingWinFspAndProxy(t *testing.T) {
	for _, mode := range []string{"occupied", "winfsp", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			m, p, _ := mountFixture(t)
			want := ""
			switch mode {
			case "occupied":
				m.probe = func() (mountObservation, error) { return mountObservation{occupied: true}, nil }
				want = "MOUNT_DRIVE_OCCUPIED"
			case "winfsp":
				m.winFsp = func() error { return fault("WINFSP_REQUIRED") }
				want = "WINFSP_REQUIRED"
			case "proxy":
				m.proxyReady = func(context.Context) error { return fault("MOUNT_PROXY_UNAVAILABLE") }
				want = "MOUNT_PROXY_UNAVAILABLE"
			}
			if err := m.Start(context.Background()); !isCode(err, want) {
				t.Fatal(err)
			}
			if m.Status().State == "MOUNTED" || p.stops.Load() != 0 {
				t.Fatal("preflight failure spawned or stopped a process")
			}
		})
	}
}

func TestMountStartupWithoutDriveDoesNotSucceed(t *testing.T) {
	m, p, _ := mountFixture(t)
	m.config.StartupTimeout = 20 * time.Millisecond
	m.start = func(context.Context, string, []string, []string) (MountProcess, error) { return p, nil }
	if err := m.Start(context.Background()); !isCode(err, "MOUNT_START_TIMEOUT") {
		t.Fatal(err)
	}
	if m.Status().State == "MOUNTED" || p.stops.Load() != 1 {
		t.Fatal("process-only start was called mounted")
	}
}

func TestMountDetectsConfigIdentityChangeAndEndsOwnProcess(t *testing.T) {
	m, p, fixture := mountFixture(t)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.fields["client_id"] = "changed-client"
	fixture.clientID = "changed-client"
	fixture.mu.Unlock()
	if err := os.WriteFile(m.client.rclone.config.ConfigFile, []byte("synthetic changed source configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	if m.Status().State == "MOUNTED" {
		t.Fatal("unverified changed configuration remained mounted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.stops.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.stops.Load() == 0 || m.Status().State == "MOUNTED" {
		t.Fatal("identity change did not stop the owned mount")
	}
}

func TestMountUnconfirmedStopRetainsOwnership(t *testing.T) {
	m, p, _ := mountFixture(t)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.stopFails.Store(true)
	if err := m.Close(); !isCode(err, "MOUNT_STOP_UNCONFIRMED") {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); !isCode(err, "MOUNT_ALREADY_RUNNING") {
		t.Fatal("unconfirmed process permitted a second mount", err)
	}
	p.stopFails.Store(false)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMountLifetimeCancellationAndProxyLossStopOwnProcess(t *testing.T) {
	for _, mode := range []string{"cancel", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			m, p, _ := mountFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var failed atomic.Bool
			m.proxyReady = func(context.Context) error {
				if failed.Load() {
					return fault("MOUNT_PROXY_UNAVAILABLE")
				}
				return nil
			}
			if err := m.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			} else {
				failed.Store(true)
			}
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
				t.Fatal("owned mount survived its lifetime/proxy")
			}
		})
	}
}
