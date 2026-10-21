package pi_background_tasks

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Time-limited subprocess probes and the bounded pool that runs many of them. upstream: extensions/probes.ts.

// probeMaxBuffer is what a `systemctl` or `ps` may print before the probe gives up on it.
const probeMaxBuffer = 1024 * 1024

// probeResult is how a probe ended, and whether that settles an answer. kind: "exited" (the command ran to an exit status),
// "missing" (the command does not exist, which every later call repeats), "unsettled" (a timeout, a signal or another start
// failure, which a later call may not repeat, so the caller asks again; cause says which).
type probeResult struct {
	kind   string
	status int
	stdout string
	cause  string // "timed-out", "signalled", "spawn-failed"
	signal string
	code   string
}

func exitedProbe(status int, stdout string) probeResult {
	return probeResult{kind: "exited", status: status, stdout: stdout}
}

var (
	probeMissing  = probeResult{kind: "missing"}
	probeTimedOut = probeResult{kind: "unsettled", cause: "timed-out"}
)

func probeSignalled(signal string) probeResult {
	return probeResult{kind: "unsettled", cause: "signalled", signal: signal}
}

func probeSpawnFailed(code string) probeResult {
	return probeResult{kind: "unsettled", cause: "spawn-failed", code: code}
}

// probeRunner runs one probe command. The original has an asynchronous and a synchronous runner of the same result shape;
// in Go both block the calling goroutine.
type probeRunner func(file string, args []string) probeResult

// startFailure is the result of a command that could not start: ENOENT means the command does not exist.
func startFailure(err error) probeResult {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, syscall.ENOENT) {
		return probeMissing
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return probeSpawnFailed(errnoName(errno))
	}
	return probeSpawnFailed(err.Error())
}

type limitedBuffer struct {
	buf      bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.exceeded {
		return len(p), nil
	}
	if l.buf.Len()+len(p) > probeMaxBuffer {
		l.exceeded = true
		l.cancel()
		return len(p), nil
	}
	return l.buf.Write(p)
}

// runProbe runs one probe command: bounded in time and output, never blocking beyond probeTimeout.
func runProbe(file string, args []string) probeResult {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	killed, killedCancel := context.WithCancel(context.Background())
	defer killedCancel()
	cmd := exec.Command(file, args...)
	hideWindow(cmd)
	out := &limitedBuffer{cancel: killedCancel}
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		return startFailure(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-ctx.Done():
		timedOut = true
		_ = cmd.Process.Kill()
		err = <-done
	case <-killed.Done():
		_ = cmd.Process.Kill()
		err = <-done
	}
	stdout := decodeUTF8(out.buf.Bytes())
	switch {
	case out.exceeded:
		return probeSpawnFailed("ERR_CHILD_PROCESS_STDIO_MAXBUFFER")
	case timedOut:
		return probeTimedOut
	case err == nil:
		return exitedProbe(0, stdout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if name := exitSignalName(ee); name != "" {
			return probeSignalled(name)
		}
		return exitedProbe(ee.ExitCode(), stdout)
	}
	return startFailure(err)
}

// mapWithConcurrency maps items through work with at most limit calls in flight; results keep the input order.
func mapWithConcurrency[T, R any](items []T, limit int, work func(T) R) []R {
	results := make([]R, len(items))
	var next int
	var mu sync.Mutex
	var wg sync.WaitGroup
	workers := min(limit, len(items))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= len(items) {
					return
				}
				results[i] = work(items[i])
			}
		}()
	}
	wg.Wait()
	return results
}

var _ = time.Second
