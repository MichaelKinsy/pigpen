package tintinweb_tasks

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Background process management for tasks: output buffering, blocking wait and graceful stop (SIGTERM, 5 s,
// SIGKILL). upstream: process-tracker.ts. Nothing in the extension calls track yet (Pi's bash tool has no
// background mode), as in the original; the tracker is exercised by its own tests.

type processOutput struct {
	Output      string
	Status      string // running | completed | error | stopped
	ExitCode    *int
	StartedAt   int64
	CompletedAt int64 // 0 while running
	Command     string
}

type backgroundProcess struct {
	TaskID  string
	PID     int
	Command string
	Status  string
}

type tracked struct {
	taskID, command string
	pid             int
	cmd             *exec.Cmd
	mu              sync.Mutex
	output          []string
	status          string
	exitCode        *int
	startedAt       int64
	completedAt     int64
	waiters         []chan struct{}
	closed          chan struct{} // closed once the process was reaped
}

type processTracker struct {
	mu        sync.Mutex
	processes map[string]*tracked
}

func newProcessTracker() *processTracker { return &processTracker{processes: map[string]*tracked{}} }

// notify wakes every waiter. The caller holds bp.mu.
func (bp *tracked) notify() {
	for _, w := range bp.waiters {
		close(w)
	}
	bp.waiters = nil
}

// track starts cmd for a task and registers it, buffering stdout and stderr; a process that cannot start is
// recorded as an error with its message, as the original's `error` event is. upstream: process-tracker.ts:25-70.
func (p *processTracker) track(taskID string, cmd *exec.Cmd, command string) {
	bp := &tracked{taskID: taskID, command: command, cmd: cmd, status: "running", startedAt: nowMs(), closed: make(chan struct{})}
	p.mu.Lock()
	p.processes[taskID] = bp
	p.mu.Unlock()
	stdout, err1 := cmd.StdoutPipe()
	stderr, err2 := cmd.StderrPipe()
	err := firstErr(err1, err2)
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		bp.mu.Lock()
		bp.status = "error"
		bp.output = append(bp.output, "Process error: "+err.Error())
		bp.completedAt = nowMs()
		bp.notify()
		bp.mu.Unlock()
		close(bp.closed)
		return
	}
	bp.pid = cmd.Process.Pid
	var readers sync.WaitGroup
	for _, r := range []io.Reader{stdout, stderr} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			buf := make([]byte, 32*1024)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					bp.mu.Lock()
					bp.output = append(bp.output, string(buf[:n]))
					bp.mu.Unlock()
				}
				if err != nil {
					return
				}
			}
		}()
	}
	go func() {
		readers.Wait()
		werr := cmd.Wait()
		bp.mu.Lock()
		code := cmd.ProcessState.ExitCode()
		if code >= 0 { // a process killed by a signal has no exit code, like `code ?? undefined`
			bp.exitCode = &code
		}
		if bp.status == "running" {
			bp.status = "completed"
			if werr != nil {
				bp.status = "error"
			}
		}
		bp.completedAt = nowMs()
		bp.notify()
		bp.mu.Unlock()
		close(bp.closed)
	}()
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (p *processTracker) lookup(taskID string) *tracked {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.processes[taskID]
}

func (bp *tracked) snapshot() *processOutput {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return &processOutput{Output: strings.Join(bp.output, ""), Status: bp.status, ExitCode: bp.exitCode,
		StartedAt: bp.startedAt, CompletedAt: bp.completedAt, Command: bp.command}
}

// getOutput is the current output and status of a task's process, nil when untracked.
func (p *processTracker) getOutput(taskID string) *processOutput {
	if bp := p.lookup(taskID); bp != nil {
		return bp.snapshot()
	}
	return nil
}

func (p *processTracker) getProcess(taskID string) *backgroundProcess {
	bp := p.lookup(taskID)
	if bp == nil {
		return nil
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return &backgroundProcess{TaskID: bp.taskID, PID: bp.pid, Command: bp.command, Status: bp.status}
}

// waitForCompletion waits for a task's process to finish, up to timeout or until ctx is done, and returns its
// output then (status running when it did not finish); nil when the task is untracked.
func (p *processTracker) waitForCompletion(ctx context.Context, taskID string, timeout time.Duration) *processOutput {
	bp := p.lookup(taskID)
	if bp == nil {
		return nil
	}
	bp.mu.Lock()
	if bp.status != "running" {
		bp.mu.Unlock()
		return bp.snapshot()
	}
	w := make(chan struct{})
	bp.waiters = append(bp.waiters, w)
	bp.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w:
	case <-timer.C:
	case <-ctx.Done():
	}
	return bp.snapshot()
}

// stop terminates a task's running process: SIGTERM, then SIGKILL after 5 s. It reports false for an
// untracked or already finished process. upstream: process-tracker.ts:106-127.
func (p *processTracker) stop(taskID string) bool {
	bp := p.lookup(taskID)
	if bp == nil {
		return false
	}
	bp.mu.Lock()
	if bp.status != "running" {
		bp.mu.Unlock()
		return false
	}
	bp.status = "stopped"
	bp.mu.Unlock()
	if err := bp.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		bp.cmd.Process.Kill() // no SIGTERM on this platform
	}
	select {
	case <-bp.closed:
	case <-time.After(5 * time.Second):
		bp.cmd.Process.Kill()
		<-bp.closed
	}
	bp.mu.Lock()
	bp.completedAt = nowMs()
	bp.notify()
	bp.mu.Unlock()
	return true
}
