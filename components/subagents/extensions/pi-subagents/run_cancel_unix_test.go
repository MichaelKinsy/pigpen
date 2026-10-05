//go:build unix

package pi_subagents

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func childPid(t *testing.T, pidfile string) int {
	t.Helper()
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(pidfile); err == nil && len(b) > 0 {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the child did not start")
	return 0
}

func gone(pid int) bool {
	for i := 0; i < 200; i++ {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// A cancelled call (the user pressed Escape, the session ended) stops its child instead of waiting for it.
func TestCancelStopsTheChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	done := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		_, err := execChild(done, helperRequest(t, "", "PISUB_PIDFILE="+pidfile, "PISUB_SLEEP=60"), time.Minute)
		errc <- err
	}()
	pid := childPid(t, pidfile)
	close(done)
	select {
	case err := <-errc:
		if err != errCancelled {
			t.Errorf("err %v, want errCancelled", err)
		}
	case <-time.After(10 * time.Second): // well before the grace period: the child was asked to stop, not killed late
		t.Fatal("the runner kept waiting for a cancelled child")
	}
	if !gone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Error("the child is still running")
	}
}

// A child that ignores the polite signal is killed after the grace period.
func TestCancelKillsAStubbornChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	done := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		_, err := execChild(done, helperRequest(t, "", "PISUB_PIDFILE="+pidfile, "PISUB_SLEEP=60", "PISUB_IGNORE_TERM=1"), 300*time.Millisecond)
		errc <- err
	}()
	pid := childPid(t, pidfile)
	time.Sleep(100 * time.Millisecond) // let the helper install its handler
	close(done)
	select {
	case <-errc:
	case <-time.After(10 * time.Second):
		t.Fatal("the runner kept waiting for a stubborn child")
	}
	if !gone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Error("the stubborn child is still running")
	}
}
