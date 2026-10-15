package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

type Worker interface {
	Capabilities() []string
	Handle(context.Context, Operation) (any, []File, error)
}
type Runner struct {
	ID                string
	Client            *Client
	State             *executor.State
	Worker            Worker
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	ToolVersions      map[string]string
	Log               func(string)
}
type journal struct {
	Operation Operation
	Result    *Result
	Files     []File
	AckID     string
}
type acknowledgement struct {
	AckID   string `json:"ackId"`
	EventID string `json:"eventId"`
}

func (r *Runner) Run(ctx context.Context, once bool) error {
	if r.ID == "" || r.Client == nil || r.State == nil || r.Worker == nil {
		return errors.New("incomplete runner configuration")
	}
	var registration struct {
		ProtocolVersion  int `json:"protocolVersion"`
		HeartbeatSeconds int `json:"heartbeatSeconds"`
	}
	facts := map[string]any{"mount": map[string]any{"state": "UNSUPPORTED", "reasonCode": "MOUNT_PROBE_UNSUPPORTED", "observedAt": time.Now().UTC()}, "accountState": "UNKNOWN"}
	versions := r.ToolVersions
	if versions == nil {
		versions = map[string]string{}
	}
	if err := r.Client.Post(ctx, "/runners/register", map[string]any{"runnerId": r.ID, "protocolVersion": 1, "capabilities": r.Worker.Capabilities(), "tools": versions, "facts": facts}, &registration); err != nil {
		return err
	}
	if registration.ProtocolVersion != 1 {
		return errors.New("unsupported control protocol")
	}
	if r.HeartbeatInterval <= 0 {
		r.HeartbeatInterval = time.Duration(registration.HeartbeatSeconds) * time.Second
		if r.HeartbeatInterval <= 0 {
			r.HeartbeatInterval = 15 * time.Second
		}
	}
	if r.PollInterval <= 0 {
		r.PollInterval = 2 * time.Second
	}
	if err := r.replay(ctx); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var response struct {
			Operation *Operation `json:"operation"`
		}
		err := r.Client.Post(ctx, "/leases/acquire", map[string]any{"runnerId": r.ID, "capabilities": r.Worker.Capabilities()}, &response)
		if err != nil {
			if once {
				return err
			}
			r.log("control unavailable; acquisition paused")
			if err := wait(ctx, r.PollInterval); err != nil {
				return err
			}
			continue
		}
		if response.Operation == nil {
			if once {
				return nil
			}
			if err := wait(ctx, r.PollInterval); err != nil {
				return err
			}
			continue
		}
		if err := r.runOperation(ctx, *response.Operation); err != nil {
			return err
		}
		if once {
			return nil
		}
	}
}
func (r *Runner) log(message string) {
	if r.Log != nil {
		r.Log(message)
	}
}
func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (r *Runner) runOperation(ctx context.Context, op Operation) error {
	supportedPlatform := op.Platform == "BILIBILI"
	if adapter, ok := r.Worker.(interface{ SupportsPlatform(string) bool }); ok {
		supportedPlatform = adapter.SupportsPlatform(op.Platform)
	}
	if op.ProtocolVersion != 1 || op.RunnerID != r.ID || op.Attempt < 1 || op.OperationID == "" || op.LeaseID == "" || !supportedPlatform {
		return errors.New("invalid acquired operation")
	}
	if token, err := strconv.ParseUint(op.FencingToken, 10, 64); err != nil || token == 0 {
		return errors.New("invalid fencing token")
	}
	name := "operation-" + digest(op.OperationID)
	var previous journal
	if err := r.State.Read(name, &previous); err == nil {
		if !sameIntent(previous.Operation, op) {
			return errors.New("operation identity changed")
		}
		if previous.Result != nil && previous.Operation.Attempt == op.Attempt && previous.Operation.FencingToken == op.FencingToken {
			return r.deliver(ctx, name, &previous, true)
		}
		if previous.Result == nil {
			return errors.New("unfinished operation requires reconciliation")
		}
		if previous.Result.Status == "SUCCEEDED" {
			return errors.New("completed operation cannot be executed under a new lease without reconciliation")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record := journal{Operation: op, Files: []File{}}
	if err := r.State.Write(name, record); err != nil {
		return err
	}
	expiry := op.LeaseExpiresAt.Add(-2 * time.Second)
	if time.Until(expiry) <= 0 {
		return errors.New("acquired lease is already expiring")
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	done := make(chan struct{})
	control := make(chan string, 1)
	go func() { defer close(done); r.heartbeat(heartbeatCtx, op, expiry, cancel, control) }()
	value, files, execErr := r.Worker.Handle(execCtx, op)
	stopHeartbeat()
	<-done
	desired := ""
	select {
	case desired = <-control:
	default:
	}
	result := Result{EventID: "result-" + digest(op.OperationID+":"+strconv.Itoa(op.Attempt)+":"+op.FencingToken), RunnerID: r.ID, LeaseID: op.LeaseID, Attempt: op.Attempt, FencingToken: op.FencingToken, Status: "SUCCEEDED", Result: value}
	if execErr != nil {
		failure := media.Classify(execErr, "EXECUTE")
		result.Status = "NEEDS_ACTION"
		if failure.Retryable {
			result.Status = "RETRYABLE_ERROR"
		}
		completed := []string{}
		for _, file := range files {
			completed = append(completed, file.OutputID)
		}
		result.Error = &ResultError{Code: failure.Code, Message: failure.Error(), CompletedEffects: completed, PendingEffects: []string{"unfinished frozen outputs"}}
		// Platform adapters expose controlled messages and precise remaining
		// outputs without making the shared runner parse provider error strings.
		var structured interface{ RunnerFailure() (string, *ResultError) }
		if errors.As(execErr, &structured) {
			result.Status, result.Error = structured.RunnerFailure()
		}
		if desired == "CANCEL" {
			result.Status = "CANCELLED"
		} else if desired == "PAUSE" {
			result.Status = "PAUSED"
		} else if desired == "LEASE_LOST" || ctx.Err() != nil {
			result.Status = "RESULT_UNKNOWN"
		}
	}
	record.Files = files
	record.Result = &result
	if err := r.State.Write(name, record); err != nil {
		return err
	}
	// Parent cancellation stops acquisition, but leaves time to persist a final
	// outcome. Unconfirmed delivery stays in the journal for the next startup.
	deliveryCtx, deliveryCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer deliveryCancel()
	return r.deliver(deliveryCtx, name, &record, desired == "LEASE_LOST")
}
func (r *Runner) heartbeat(ctx context.Context, op Operation, expiry time.Time, cancel context.CancelFunc, control chan<- string) {
	sequence := int64(0)
	signal := func(action string) {
		select {
		case control <- action:
		default:
		}
		cancel()
	}
	for {
		delay := r.HeartbeatInterval
		if remaining := time.Until(expiry); remaining < delay {
			delay = remaining
		}
		if delay <= 0 {
			signal("LEASE_LOST")
			return
		}
		if wait(ctx, delay) != nil {
			return
		}
		if time.Now().After(expiry) {
			signal("LEASE_LOST")
			return
		}
		sequence++
		requestCtx, requestCancel := context.WithDeadline(ctx, expiry)
		var response struct {
			DesiredAction  string    `json:"desiredAction"`
			LeaseExpiresAt time.Time `json:"leaseExpiresAt"`
		}
		err := r.Client.Post(requestCtx, "/leases/"+op.LeaseID+"/heartbeat", map[string]any{"eventId": fmt.Sprintf("heartbeat-%s-%d", digest(op.LeaseID), sequence), "runnerId": r.ID, "operationId": op.OperationID, "attempt": op.Attempt, "fencingToken": op.FencingToken, "sequence": sequence}, &response)
		requestCancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && api.Status == 409 {
				signal("LEASE_LOST")
				return
			}
			continue
		}
		if response.DesiredAction == "PAUSE" || response.DesiredAction == "CANCEL" {
			signal(response.DesiredAction)
			return
		}
		if response.DesiredAction != "CONTINUE" || response.LeaseExpiresAt.IsZero() {
			signal("LEASE_LOST")
			return
		}
		expiry = response.LeaseExpiresAt.Add(-2 * time.Second)
	}
}
func sameIntent(a, b Operation) bool {
	normalize := func(op Operation) []byte {
		op.Attempt = 0
		op.LeaseID = ""
		op.RunnerID = ""
		op.FencingToken = ""
		op.LeaseExpiresAt = time.Time{}
		raw, _ := json.Marshal(op)
		return raw
	}
	return string(normalize(a)) == string(normalize(b))
}
func (r *Runner) deliver(ctx context.Context, name string, record *journal, reconcile bool) error {
	if record.Result == nil {
		return errors.New("missing durable result")
	}
	if len(record.Files) > 0 {
		if record.Operation.Destination == nil {
			return errors.New("receipt has no target")
		}
		for _, file := range record.Files {
			if err := executor.VerifyFile(record.Operation.Destination.RootPath, file.RelativePath, file.SHA256, file.SizeBytes); err != nil {
				return errors.New("receipt file validation failed; reconciliation required")
			}
		}
	}
	endpoint := "result"
	if reconcile {
		endpoint = "reconcile"
	}
	var ack acknowledgement
	if err := r.Client.Post(ctx, "/operations/"+record.Operation.OperationID+"/"+endpoint, record.Result, &ack); err != nil {
		return err
	}
	if ack.AckID == "" || ack.EventID != record.Result.EventID {
		return errors.New("invalid durable acknowledgement")
	}
	if record.AckID != "" && record.AckID != ack.AckID {
		return errors.New("acknowledgement identity changed")
	}
	record.AckID = ack.AckID
	return r.State.Write(name, record)
}
func (r *Runner) replay(ctx context.Context) error {
	names, err := r.State.Names("operation-")
	if err != nil {
		return err
	}
	for _, name := range names {
		var record journal
		if err := r.State.Read(name, &record); err != nil {
			return err
		}
		if record.AckID != "" {
			continue
		}
		if record.Result == nil {
			op := record.Operation
			record.Result = &Result{EventID: "interrupted-" + digest(op.OperationID+":"+op.FencingToken), RunnerID: r.ID, LeaseID: op.LeaseID, Attempt: op.Attempt, FencingToken: op.FencingToken, Status: "RESULT_UNKNOWN", Error: &ResultError{Code: "RUNNER_INTERRUPTED", Message: "execution stopped before a durable result; reconcile files before retry", CompletedEffects: []string{}, PendingEffects: []string{"frozen operation"}}}
			if recovery, ok := r.Worker.(interface {
				Recover(Operation) (any, []File, bool, error)
			}); ok {
				value, files, complete, recoveryErr := recovery.Recover(op)
				if recoveryErr != nil {
					return recoveryErr
				}
				record.Files = files
				record.Result.Result = value
				for _, file := range files {
					record.Result.Error.CompletedEffects = append(record.Result.Error.CompletedEffects, file.OutputID)
				}
				if complete {
					record.Result.Status = "SUCCEEDED"
					record.Result.Error = nil
				}
			}
			if err := r.State.Write(name, record); err != nil {
				return err
			}
		}
		if err := r.deliver(ctx, name, &record, true); err != nil {
			return err
		}
	}
	return nil
}
