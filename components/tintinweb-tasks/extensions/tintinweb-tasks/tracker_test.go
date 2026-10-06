package tintinweb_tasks

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startProc starts a process the way the original's tests spawn one (a shell command line) and tracks it.
func startProc(t *testing.T, tr *processTracker, taskID, command string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(command, args...)
	tr.track(taskID, cmd, strings.Join(append([]string{command}, args...), " "))
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	return cmd
}

// waitClosed waits for the tracked process to finish and for the tracker to have processed it (the original
// awaits `close` and then sleeps 50 ms).
func waitClosed(t *testing.T, tr *processTracker, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if o := tr.getOutput(taskID); o != nil && o.Status != "running" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process did not finish")
}

func TestProcessTracker(t *testing.T) {
	const f = "process-tracker"
	tw(t, f, "returns undefined for untracked task", func(t *testing.T) {
		tr := newProcessTracker()
		if tr.getOutput("999") != nil || tr.getProcess("999") != nil {
			t.Fatal("expected nothing for an untracked task")
		}
	})
	tw(t, f, "tracks a process and captures stdout", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "echo", "hello world")
		waitClosed(t, tr, "1")
		out := tr.getOutput("1")
		if !strings.Contains(out.Output, "hello world") {
			t.Fatalf("output %q", out.Output)
		}
		eq(t, out.Status, "completed")
		eq(t, *out.ExitCode, 0)
		eq(t, out.Command, "echo hello world")
		eq(t, out.StartedAt > 0, true)
		eq(t, out.CompletedAt > 0, true)
	})
	tw(t, f, "tracks a process and captures stderr", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sh", "-c", "echo errdata >&2")
		waitClosed(t, tr, "1")
		eq(t, strings.Contains(tr.getOutput("1").Output, "errdata"), true)
	})
	tw(t, f, "reports error status for non-zero exit", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sh", "-c", "exit 42")
		waitClosed(t, tr, "1")
		out := tr.getOutput("1")
		eq(t, out.Status, "error")
		eq(t, *out.ExitCode, 42)
	})
	tw(t, f, "waitForCompletion returns immediately for already-completed process", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "echo", "done")
		waitClosed(t, tr, "1")
		out := tr.waitForCompletion(context.Background(), "1", time.Second)
		eq(t, out.Status, "completed")
	})
	tw(t, f, "waitForCompletion returns undefined for untracked task", func(t *testing.T) {
		if newProcessTracker().waitForCompletion(context.Background(), "999", time.Second) != nil {
			t.Fatal("expected nothing")
		}
	})
	tw(t, f, "waitForCompletion waits for process to finish", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sh", "-c", "sleep 0.1 && echo waited")
		out := tr.waitForCompletion(context.Background(), "1", 5*time.Second)
		eq(t, strings.Contains(out.Output, "waited"), true)
		eq(t, out.Status, "completed")
	})
	tw(t, f, "waitForCompletion times out if process takes too long", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sleep", "10")
		out := tr.waitForCompletion(context.Background(), "1", 200*time.Millisecond)
		eq(t, out.Status, "running")
	})
	tw(t, f, "stop sends SIGTERM and marks process stopped", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sleep", "10")
		time.Sleep(50 * time.Millisecond) // let the process start
		eq(t, tr.stop("1"), true)
		out := tr.getOutput("1")
		eq(t, out.Status, "stopped")
		eq(t, out.CompletedAt > 0, true)
	})
	tw(t, f, "stop returns false for untracked task", func(t *testing.T) {
		eq(t, newProcessTracker().stop("999"), false)
	})
	tw(t, f, "stop returns false for already-completed process", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "echo", "quick")
		waitClosed(t, tr, "1")
		eq(t, tr.stop("1"), false)
	})
	tw(t, f, "getProcess returns the background process record", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "echo", "test")
		bp := tr.getProcess("1")
		if bp == nil {
			t.Fatal("no record")
		}
		eq(t, bp.TaskID, "1")
		eq(t, bp.Command, "echo test")
		eq(t, bp.Status, "running")
		eq(t, bp.PID > 0, true)
	})
	tw(t, f, "handles process error event", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "nonexistent-binary-that-does-not-exist-xyz")
		out := tr.getOutput("1")
		eq(t, out.Status, "error")
		eq(t, strings.Contains(out.Output, "Process error:"), true)
	})
	tw(t, f, "waitForCompletion respects abort signal", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sleep", "10")
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		out := tr.waitForCompletion(ctx, "1", time.Minute)
		eq(t, out.Status, "running")
	})
	tw(t, f, "notifies waiters when process completes", func(t *testing.T) {
		tr := newProcessTracker()
		startProc(t, tr, "1", "sh", "-c", "sleep 0.1")
		r1, r2 := make(chan *processOutput, 1), make(chan *processOutput, 1)
		go func() { r1 <- tr.waitForCompletion(context.Background(), "1", 5*time.Second) }()
		go func() { r2 <- tr.waitForCompletion(context.Background(), "1", 5*time.Second) }()
		eq(t, (<-r1).Status, "completed")
		eq(t, (<-r2).Status, "completed")
	})
}
