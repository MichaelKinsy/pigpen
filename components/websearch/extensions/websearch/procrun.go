package websearch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// Child processes of the GitHub extractors. The original starts gh and git with Node's
// child_process (execFile for the short calls, spawn in its own process group for a clone), not
// with pi.exec: it sets GIT_TERMINAL_PROMPT, GCM_INTERACTIVE and GH_PROMPT_DISABLED for the
// children so a credential helper never reads the terminal, and it kills the whole process group of
// a clone that times out. Context.Exec carries neither an environment nor a process group, so these
// run through os/exec (docs: port/PORT.md, deliberate differences), as credential.go does.

const (
	// defaultMaxBuffer is execFile's default maxBuffer.
	defaultMaxBuffer = 1024 * 1024
	// pipeGrace bounds the wait for a child's pipes once the child is gone or killed: a shell script
	// that was killed leaves its `sleep` holding the pipe, and execFile's callback fires on abort
	// without waiting for the pipes.
	pipeGrace = 500 * time.Millisecond
	// processKillGrace is PROCESS_KILL_GRACE_MS of github-extract.ts.
	processKillGrace = 3 * time.Second
)

// processOptions are the execFile options the extractors pass.
type processOptions struct {
	Timeout time.Duration
	// MaxBuffer is the per-stream byte limit; 0 means execFile's default.
	MaxBuffer int
	// Env is added to the inherited environment.
	Env []string
}

// processResult is execFile's callback: OK is "no error".
type processResult struct {
	OK     bool
	Stdout string
	Stderr string
	// Error is the error's message ("Command failed: ...", or the abort).
	Error string
}

type cappedBuffer struct {
	buf    bytes.Buffer
	max    int
	over   *atomic.Bool
	cancel context.CancelFunc
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); len(p) > room {
		if room > 0 {
			w.buf.Write(p[:room])
		}
		w.over.Store(true)
		w.cancel()
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// runProcess is execFile(name, args, options): it fails on a spawn error, a non-zero exit, the
// timeout, a stream over maxBuffer and the caller's cancellation, and a cancelled or timed out child
// is killed.
func runProcess(ctx context.Context, name string, args []string, o processOptions) processResult {
	if ctx.Err() != nil {
		return processResult{Error: "The operation was aborted"}
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.Timeout > 0 {
		var stop context.CancelFunc
		rctx, stop = context.WithTimeout(rctx, o.Timeout)
		defer stop()
	}
	limit := o.MaxBuffer
	if limit <= 0 {
		limit = defaultMaxBuffer
	}
	var over atomic.Bool
	stdout := &cappedBuffer{max: limit, over: &over, cancel: cancel}
	stderr := &cappedBuffer{max: limit, over: &over, cancel: cancel}
	cmd := exec.CommandContext(rctx, name, args...)
	if len(o.Env) > 0 {
		cmd.Env = append(os.Environ(), o.Env...)
	}
	hideWindow(cmd)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = pipeGrace
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	res := processResult{OK: err == nil, Stdout: stdout.buf.String(), Stderr: stderr.buf.String()}
	switch {
	case ctx.Err() != nil:
		res.OK, res.Error = false, "The operation was aborted"
	case over.Load():
		res.OK, res.Error = false, "stdout maxBuffer length exceeded"
	case err != nil:
		res.Error = "Command failed: " + strings.TrimSpace(name+" "+strings.Join(args, " ")) + "\n" + res.Stderr
		if rctx.Err() != nil {
			res.Error = "Command timed out: " + name
		}
	}
	return res
}

// runClone is execClone of github-extract.ts: the command runs in its own process group with the
// non-interactive environment and no stdio; the timeout and the caller's cancellation end the
// whole group (SIGTERM, then SIGKILL after the grace). It reports whether the command exited 0.
func runClone(ctx context.Context, argv []string, timeout time.Duration) bool {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GH_PROMPT_DISABLED=1")
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	abort := ctx.Done()
	for {
		select {
		case err := <-done:
			return err == nil
		case <-timer.C:
			terminateProcessTree(cmd)
		case <-abort:
			abort = nil
			timer.Stop()
			terminateProcessTree(cmd)
		}
	}
}
