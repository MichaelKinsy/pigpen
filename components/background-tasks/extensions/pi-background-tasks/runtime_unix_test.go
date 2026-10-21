//go:build !windows

package pi_background_tasks_test

import (
	"syscall"
	"testing"
)

func TestSessionShutdownKillsTheRunningTasks(t *testing.T) {
	h := started(t, nil)
	raw, _ := h.Tool("bg_task", map[string]any{"action": "spawn", "command": "sleep 300", "notifyOnExit": false})
	details, _ := text(t, raw).Details["task"].(map[string]any)
	pid, _ := details["pid"].(float64)
	if pid == 0 {
		t.Fatalf("no pid in %v", details)
	}
	h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	eventually(t, "the process to be gone", func() bool {
		return syscall.Kill(int(pid), 0) != nil
	})
}
