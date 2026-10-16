package media

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ManagedProcess owns one process tree. Done closes after the root is reaped
// and its Job Object has been closed; Err reports only a controlled result.
type ManagedProcess struct {
	ctx       context.Context
	cancel    context.CancelFunc
	pid       int
	done      chan struct{}
	job       io.Closer
	closeOnce sync.Once
	closeErr  error
	err       error
	stopErr   error
}

// StartManaged starts a trusted long-lived tool without a shell or an implicit
// deadline. ctx controls its whole lifetime. exactEnvironment is the complete
// child environment; even nil/empty does not inherit the runner's RCLONE_*
// settings. The Go runtime may supply Windows' required SYSTEMROOT variable.
// Output is discarded directly, so provider diagnostics and credentials cannot
// become public error messages or remain in an unbounded buffer.
func StartManaged(ctx context.Context, executable string, args []string, exactEnvironment []string) (*ManagedProcess, error) {
	if !filepath.IsAbs(executable) {
		return nil, Fail("TOOL_NOT_CONFIGURED", "TOOL", false)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(lifetime, executable, args...)
	cmd.Env = make([]string, len(exactEnvironment))
	copy(cmd.Env, exactEnvironment)
	cmd.WaitDelay = 2 * time.Second
	// nil Stdout/Stderr connect directly to the null device, with no pipe
	// copier goroutines or retained raw diagnostics.
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, Fail("TOOL_START_FAILED", "TOOL", false)
	}
	job, err := bindProcessTree(cmd.Process)
	if err != nil {
		return nil, managedStartFailure(ctx, cmd, nil, cancel)
	}
	if err := resumeProcess(cmd.Process); err != nil {
		return nil, managedStartFailure(ctx, cmd, job, cancel)
	}
	if ctx.Err() != nil {
		return nil, managedStartFailure(ctx, cmd, job, cancel)
	}
	process := &ManagedProcess{ctx: lifetime, cancel: cancel, pid: cmd.Process.Pid, done: make(chan struct{}), job: job}
	stopWatch := context.AfterFunc(lifetime, func() { process.closeJob() })
	go process.wait(cmd, stopWatch)
	return process, nil
}

func managedStartFailure(ctx context.Context, cmd *exec.Cmd, job io.Closer, cancel context.CancelFunc) error {
	cancel()
	var closeErr error
	if job != nil {
		closeErr = job.Close()
	}
	// A failure before resume cannot have launched any descendant. Reap the
	// suspended root even when assigning its Job Object failed.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if closeErr != nil || cmd.ProcessState == nil {
		return ErrProcessStopUnconfirmed
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return Fail("TOOL_PROCESS_CONTROL_FAILED", "TOOL", false)
}

func (p *ManagedProcess) closeJob() error {
	p.closeOnce.Do(func() {
		if p.job.Close() != nil {
			p.closeErr = ErrProcessStopUnconfirmed
		}
	})
	return p.closeErr
}

func (p *ManagedProcess) wait(cmd *exec.Cmd, stopWatch func() bool) {
	defer p.cancel()
	waitErr := cmd.Wait()
	stopWatch()
	// A natural root exit must also reap descendants. When cancellation has
	// already started closing the job, Once waits for that same confirmation.
	p.stopErr = p.closeJob()
	switch {
	case p.stopErr != nil:
		p.err = ErrProcessStopUnconfirmed
	case p.ctx.Err() != nil:
		p.err = p.ctx.Err()
	case waitErr != nil:
		p.err = Fail("TOOL_FAILED", "TOOL", false)
	}
	close(p.done)
}

func (p *ManagedProcess) PID() int { return p.pid }

func (p *ManagedProcess) Done() <-chan struct{} { return p.done }

// Err is nil while the process is running. After Done it describes the final
// execution result; cancellation and deadlines retain their context errors.
func (p *ManagedProcess) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

// Stop is concurrent-safe and idempotent. It cancels only this process tree and
// returns after Wait and Job Object cleanup confirm termination. A confirmed
// stop returns nil; the execution outcome remains available through Err.
func (p *ManagedProcess) Stop() error {
	select {
	case <-p.done:
		return p.stopErr
	default:
	}
	p.cancel()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.stopErr
	case <-timer.C:
		return ErrProcessStopUnconfirmed
	}
}
