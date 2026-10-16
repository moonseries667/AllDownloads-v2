package drive

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

const RcloneVersion = "1.74.4"
const MountRemote = "gdrive"
const MountPoint = "G:"
const MountVolume = "MyGDrive"
const warpProxy = "http://127.0.0.1:40000"

// MountProcess must be a directly owned, suspended-then-Job-bound child.
// Stop is idempotent, bounded, and confirms termination of its entire Job.
// The process implementation must discard or privately bound stdout/stderr.
type MountProcess interface {
	Done() <-chan struct{}
	Stop() error
}

// MountStarter receives an exact environment, not overrides to inherit. The
// shared media adapter supplies the existing Windows Job Object lifecycle.
type MountStarter func(context.Context, string, []string, []string) (MountProcess, error)

type MountConfig struct {
	CacheDir       string
	StartupTimeout time.Duration
}

// MountSettings is the private runner JSON section. Main owns when to start it
// and supplies drive-main plus the shared netpolicy HTTP client/environment.
type MountSettings struct {
	Enabled   bool   `json:"enabled"`
	AccountID string `json:"accountId"`
	CacheDir  string `json:"cacheDir"`
}
type MountStatus struct {
	State      string
	ReasonCode string
	Remote     string
	MountPoint string
	VolumeName string
}
type mountObservation struct{ occupied, matches bool }
type mountRun struct {
	process MountProcess
	cancel  context.CancelFunc
	unlock  func()
}

type Mounter struct {
	client       *Client
	config       MountConfig
	start        MountStarter
	lifecycle    sync.Mutex
	mu           sync.Mutex
	status       MountStatus
	run          *mountRun
	probe        func() (mountObservation, error)
	winFsp       func() error
	proxyReady   func(context.Context) error
	checkVersion func(context.Context) error
	pollInterval time.Duration
}

// NewMounter does not read credentials, start OAuth, or spawn a process.
// CacheDir must be the new v2 runtime/cache/gdrive directory selected by main.
func NewMounter(c *Client, config MountConfig, start MountStarter) (*Mounter, error) {
	if c == nil || c.rclone == nil || c.AccountID != "drive-main" || c.HTTP == nil || start == nil || !filepath.IsAbs(config.CacheDir) {
		return nil, fault("MOUNT_CONFIG_INVALID")
	}
	if config.StartupTimeout == 0 {
		config.StartupTimeout = 30 * time.Second
	}
	if config.StartupTimeout < time.Second || config.StartupTimeout > 2*time.Minute {
		return nil, fault("MOUNT_CONFIG_INVALID")
	}
	m := &Mounter{client: c, config: config, start: start, probe: observeMount, winFsp: checkWinFsp, proxyReady: checkWarpListener, pollInterval: time.Second, status: MountStatus{State: "STOPPED", Remote: MountRemote, MountPoint: MountPoint, VolumeName: MountVolume}}
	m.checkVersion = m.version
	return m, nil
}

// mountEnvironment drops inherited remote overrides and proxy bypasses. Only
// the explicitly permitted existing config-unlock environment can survive.
func mountEnvironment(overrides map[string]string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if !ok || key == "" || (strings.HasPrefix(upper, "RCLONE_") && upper != "RCLONE_CONFIG_PASS") || strings.HasPrefix(upper, "GOOGLE_") || upper == "HTTP_PROXY" || upper == "HTTPS_PROXY" || upper == "NO_PROXY" || upper == "ALL_PROXY" {
			continue
		}
		values[key] = value
	}
	for key, value := range overrides {
		values[key] = value
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		values[key] = warpProxy
	}
	for _, key := range []string{"NO_PROXY", "no_proxy", "ALL_PROXY", "all_proxy"} {
		values[key] = ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

func (m *Mounter) version(ctx context.Context) error {
	m.client.rclone.mu.Lock()
	defer m.client.rclone.mu.Unlock()
	return m.client.rclone.version(ctx)
}

func checkWarpListener(ctx context.Context) error {
	connection, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:40000")
	if err != nil {
		return fault("MOUNT_PROXY_UNAVAILABLE")
	}
	return connection.Close()
}

func (m *Mounter) setStatus(state, code string) {
	m.mu.Lock()
	m.status.State = state
	m.status.ReasonCode = code
	m.mu.Unlock()
}
func mountReason(err error) string {
	var failure *media.Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return "MOUNT_FAILED"
}

func (m *Mounter) Start(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.run != nil {
		return fault("MOUNT_ALREADY_RUNNING")
	}
	startup, cancelStartup := context.WithTimeout(ctx, m.config.StartupTimeout)
	defer cancelStartup()
	m.setStatus("STARTING", "")
	err := m.begin(startup, ctx)
	if err != nil {
		m.setStatus("ERROR", mountReason(err))
	}
	return err
}

func (m *Mounter) begin(startup, lifetime context.Context) error {
	observation, err := m.probe()
	if err != nil {
		return err
	}
	if observation.occupied {
		return fault("MOUNT_DRIVE_OCCUPIED")
	}
	if err = m.winFsp(); err != nil {
		return err
	}
	if err = m.checkVersion(startup); err != nil {
		return err
	}
	if err = m.proxyReady(startup); err != nil {
		return err
	}
	if err = m.client.Check(startup); err != nil {
		return err
	}
	stored, err := m.client.credentials(startup)
	if err != nil {
		return err
	}
	if m.client.RootID != "" && m.client.RootID != "root" && m.client.RootID != stored.SourceRootID {
		return fault("AUTH_ROOT_MISMATCH")
	}
	if err = privatePath(m.config.CacheDir); err != nil {
		return fault("MOUNT_CACHE_UNAVAILABLE")
	}
	if err = os.MkdirAll(m.config.CacheDir, 0700); err != nil {
		return fault("MOUNT_CACHE_UNAVAILABLE")
	}
	unlock, err := lockPrivate(filepath.Join(m.config.CacheDir, "mount-v2-gdrive"))
	if err != nil {
		return fault("MOUNT_ALREADY_RUNNING")
	}
	runCtx, cancel := context.WithCancel(lifetime)
	run := &mountRun{cancel: cancel, unlock: unlock}
	environment := mountEnvironment(nil)
	args := []string{"mount", MountRemote + ":", MountPoint, "--ask-password=false", "--config", m.client.rclone.config.ConfigFile, "--volname", MountVolume, "--network-mode", "--vfs-cache-mode", "full", "--vfs-cache-max-size", "5G", "--vfs-cache-max-age", "12h", "--cache-dir", m.config.CacheDir, "--dir-cache-time", "24h", "--vfs-read-chunk-size", "64M", "--vfs-read-chunk-size-limit", "256M", "--buffer-size", "64M", "--transfers", "32", "--poll-interval", "1m", "--log-level", "INFO", "--stats", "0"}
	run.process, err = m.start(runCtx, m.client.rclone.config.Executable, args, environment)
	if err != nil || run.process == nil {
		cancel()
		unlock()
		return fault("MOUNT_PROCESS_START_FAILED")
	}
	if run.process.Done() == nil {
		_ = run.process.Stop()
		cancel()
		unlock()
		return fault("MOUNT_PROCESS_CONTROL_FAILED")
	}
	m.mu.Lock()
	m.run = run
	m.mu.Unlock()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-startup.Done():
			err = fault("MOUNT_START_TIMEOUT")
		case <-run.process.Done():
			err = fault("MOUNT_PROCESS_EXITED")
		case <-ticker.C:
			observation, err = m.probe()
			if err == nil && observation.matches {
				select {
				case <-run.process.Done():
					err = fault("MOUNT_PROCESS_EXITED")
				default:
					m.setStatus("MOUNTED", "")
					go m.monitor(runCtx, run)
					return nil
				}
			}
			if err == nil {
				continue
			}
		}
		if stopErr := m.stop(run); stopErr != nil {
			return stopErr
		}
		return err
	}
}

func (m *Mounter) monitor(ctx context.Context, run *mountRun) {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	code := ""
	for code == "" {
		select {
		case <-ctx.Done():
			code = "MOUNT_STOPPED"
		case <-run.process.Done():
			code = "MOUNT_PROCESS_EXITED"
		case <-ticker.C:
			observation, err := m.probe()
			if err != nil || !observation.matches {
				code = "MOUNT_DISAPPEARED"
			} else if err = m.proxyReady(ctx); err != nil {
				code = "MOUNT_PROXY_UNAVAILABLE"
			} else if m.client.rclone.changed() {
				checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				err = m.client.Check(checkCtx)
				cancel()
				if err != nil {
					code = mountReason(err)
				}
			}
		}
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.run != run {
		return
	}
	if err := m.stop(run); err != nil {
		code = mountReason(err)
	}
	if code == "MOUNT_STOPPED" {
		m.setStatus("STOPPED", "")
	} else {
		m.setStatus("ERROR", code)
	}
}

func (m *Mounter) stop(run *mountRun) error {
	run.cancel()
	err := run.process.Stop()
	if err != nil {
		// Preserve ownership and the cache lock: a failed stop must not permit
		// another Start to spawn a second mount with unknown old effects.
		return fault("MOUNT_STOP_UNCONFIRMED")
	}
	run.unlock()
	m.mu.Lock()
	m.run = nil
	m.mu.Unlock()
	return nil
}

func (m *Mounter) Close() error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.run == nil {
		return nil
	}
	err := m.stop(m.run)
	if err != nil {
		m.setStatus("ERROR", mountReason(err))
		return err
	}
	m.setStatus("STOPPED", "")
	return nil
}

// Status does not perform Google I/O. MOUNTED always requires both this owned
// process to be alive and the new network drive mapping to still be present.
func (m *Mounter) Status() MountStatus {
	m.mu.Lock()
	status, run := m.status, m.run
	m.mu.Unlock()
	if status.State == "MOUNTED" {
		if m.client.rclone.changed() {
			status.State = "ERROR"
			status.ReasonCode = "MOUNT_IDENTITY_RECHECK_REQUIRED"
			return status
		}
		if run == nil {
			status.State = "ERROR"
			status.ReasonCode = "MOUNT_PROCESS_EXITED"
			return status
		}
		select {
		case <-run.process.Done():
			status.State = "ERROR"
			status.ReasonCode = "MOUNT_PROCESS_EXITED"
			return status
		default:
		}
		observation, err := m.probe()
		if err != nil || !observation.matches {
			status.State = "ERROR"
			status.ReasonCode = "MOUNT_DISAPPEARED"
		}
	}
	return status
}
