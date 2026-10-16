package main

import (
	"context"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
)

type mountLifecycle interface {
	Start(context.Context) error
	Status() drive.MountStatus
	Close() error
}

// Failed authentication, proxy loss or a missing driver leaves the rest of the
// application available. The component retains ownership of any unconfirmed
// child and rejects a second instance; retrying here cannot mount over a user G:.
func maintainMount(ctx context.Context, mount mountLifecycle, retryInterval time.Duration) error {
	for {
		if ctx.Err() != nil {
			return mount.Close()
		}
		status := mount.Status()
		if status.State != "MOUNTED" && status.State != "STARTING" {
			_ = mount.Start(ctx)
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return mount.Close()
		case <-timer.C:
		}
	}
}

func mountFacts(status drive.MountStatus) map[string]any {
	state, code := "NOT_MOUNTED", status.ReasonCode
	switch status.State {
	case "MOUNTED":
		state, code = "MOUNTED", ""
	case "NOT_CONFIGURED", "UNSUPPORTED", "UNKNOWN":
		state = status.State
	case "STARTING":
		if code == "" {
			code = "MOUNT_STARTING"
		}
	}
	if code == "WINFSP_REQUIRED" {
		state = "UNSUPPORTED"
	}
	if state != "MOUNTED" && code == "" {
		code = "MOUNT_NOT_RUNNING"
	}
	var reason *string
	if code != "" {
		reason = &code
	}
	return map[string]any{"mount": map[string]any{"state": state, "reasonCode": reason, "observedAt": time.Now().UTC()}}
}
