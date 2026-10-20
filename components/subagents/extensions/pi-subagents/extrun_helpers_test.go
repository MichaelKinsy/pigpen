//go:build unix

package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Shared helpers of the external-runner twins. The original's tests start `node -e ...` or a node script; the Go twins start a
// shell script or `sh -c` (never a real Claude Code, Codex or Cursor Agent), so a fake executable stands where the CLI would.

func exTemp(t *testing.T) string {
	t.Helper()
	clearExternalCliPreflightCache()
	t.Cleanup(clearExternalCliPreflightCache)
	return tmp(t)
}

// exScript writes an executable shell script and returns its path.
func exScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// exRun runs the CLI runner and fails the test when it rejects.
func exRun(t *testing.T, in ExternalCliRunInput) ExternalCliRunResult {
	t.Helper()
	r, err := RunExternalCli(in)
	if err != nil {
		t.Fatalf("RunExternalCli: %v", err)
	}
	return r
}

func exCode(r ExternalCliRunResult) int {
	if r.ExitCode == nil {
		return -1000
	}
	return *r.ExitCode
}

// exShell is an input that runs `sh -c script args...` in dir.
func exShell(dir string, stepIndex int, script string, args ...string) ExternalCliRunInput {
	return ExternalCliRunInput{Command: "/bin/sh", Args: append([]string{"-c", script, "sh"}, args...), Cwd: dir, Prompt: "x", AsyncDir: dir, StepIndex: stepIndex}
}

func exRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// exWaitFile waits until p exists.
func exWaitFile(t *testing.T, p string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if _, err := os.Stat(p); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// exRegistered captures the callback a runner registers (registerTimeout / registerStop) and hands it to the test.
type exRegistered struct {
	mu   sync.Mutex
	fn   func()
	seen chan struct{}
	once sync.Once
}

func newExRegistered() *exRegistered { return &exRegistered{seen: make(chan struct{})} }

func (r *exRegistered) register(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fn = fn
	if fn != nil {
		r.once.Do(func() { close(r.seen) })
	}
}

// call waits for the registration, then calls it.
func (r *exRegistered) call(t *testing.T) {
	t.Helper()
	select {
	case <-r.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the callback was not registered")
	}
	r.mu.Lock()
	fn := r.fn
	r.mu.Unlock()
	if fn == nil {
		t.Fatal("the callback was already withdrawn")
	}
	fn()
}

func exHas(t *testing.T, s, substring, what string) {
	t.Helper()
	if !strings.Contains(s, substring) {
		t.Errorf("%s: %q does not contain %q", what, s, substring)
	}
}

func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}
