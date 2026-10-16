// Package folder selects a directory on the runner's interactive host. It
// never interprets a browser FileSystemHandle as a server filesystem path.
package folder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

type Result struct {
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reasonCode,omitempty"`
}

type Picker struct {
	mu sync.Mutex
	// These private seams exercise host capability and helper lifecycle without
	// opening a dialog during automated tests.
	available func() bool
	run       func(context.Context) ([]byte, error)
}

func (p *Picker) Pick(ctx context.Context, timeout time.Duration) Result {
	if !p.mu.TryLock() {
		return Result{Status: "UNAVAILABLE", Reason: "PICKER_BUSY"}
	}
	defer p.mu.Unlock()
	available := p.available
	if available == nil {
		available = InteractiveDesktop
	}
	if !available() {
		return Result{Status: "UNAVAILABLE", Reason: "NO_INTERACTIVE_DESKTOP"}
	}
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := p.run
	if run == nil {
		run = func(ctx context.Context) ([]byte, error) {
			executable, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return media.RunControlled(ctx, executable, timeout, "-native-folder-dialog")
		}
	}
	raw, err := run(ctx)
	if errors.Is(err, media.ErrProcessStopUnconfirmed) {
		return Result{Status: "UNAVAILABLE", Reason: "NATIVE_PICKER_STOP_UNCONFIRMED"}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Result{Status: "CANCELLED", Reason: "PICKER_TIMED_OUT"}
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return Result{Status: "CANCELLED", Reason: "PICKER_CANCELLED"}
	}
	if err != nil {
		return Result{Status: "UNAVAILABLE", Reason: "NATIVE_PICKER_FAILED"}
	}
	var result Result
	if json.Unmarshal(raw, &result) != nil {
		return Result{Status: "UNAVAILABLE", Reason: "NATIVE_PICKER_FAILED"}
	}
	switch result.Status {
	case "SELECTED":
		if result.Path != "" {
			return result
		}
	case "CANCELLED", "UNAVAILABLE":
		result.Path = ""
		return result
	}
	return Result{Status: "UNAVAILABLE", Reason: "NATIVE_PICKER_FAILED"}
}
