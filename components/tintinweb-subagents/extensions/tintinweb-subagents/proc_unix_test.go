//go:build !windows

package tintinweb_subagents

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports a process that exists and is not a zombie.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndex(string(b), ")"); i >= 0 && strings.HasPrefix(string(b[i+1:]), " Z") {
			return false
		}
	}
	return true
}

// stopGrandchild starts a child that starts a process of its own, stops the child, and reports how long the
// grandchild outlived the stop (or that it is still running after the deadline).
func stopGrandchild(t *testing.T, ignoresTerm bool, deadline time.Duration) (time.Duration, bool) {
	t.Helper()
	useFakePig(t, "hang-grandchild")
	pidFile := t.TempDir() + "/grandchild"
	t.Setenv("FAKE_PIG_GRANDCHILD", pidFile)
	if ignoresTerm {
		t.Setenv("FAKE_PIG_GRANDCHILD_IGNORES_TERM", "1")
	}
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir()})
	eq(t, err, nil)
	var pid int
	for i := 0; i < 500 && pid == 0; i++ {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(string(b))
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the child started no process")
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	time.Sleep(100 * time.Millisecond) // let sh install its trap
	eq(t, alive(pid), true)
	start := time.Now()
	c.abort()
	for alive(pid) && time.Since(start) < deadline {
		time.Sleep(10 * time.Millisecond)
	}
	return time.Since(start), !alive(pid)
}

// Stopping an agent stops what it started: the child runs in a process group of its own, which gets SIGTERM at once.
func TestStoppingAnAgentStopsItsProcessGroup(t *testing.T) {
	took, gone := stopGrandchild(t, false, 3*time.Second)
	if !gone {
		t.Fatalf("the grandchild still runs %v after the stop", took)
	}
}

// A process that ignores SIGTERM gets SIGKILL five seconds later.
func TestAProcessThatIgnoresTheStopIsKilled(t *testing.T) {
	took, gone := stopGrandchild(t, true, 9*time.Second)
	if !gone {
		t.Fatalf("the grandchild still runs %v after the stop", took)
	}
	if took < 4*time.Second {
		t.Fatalf("gone after %v: it should have ignored the SIGTERM", took)
	}
}
