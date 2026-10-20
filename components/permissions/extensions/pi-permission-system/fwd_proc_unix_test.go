//go:build !windows

package pi_permission_system

import (
	"os"
	"os/exec"
	"testing"
)

// Go-specific: the liveness probe against real processes. Signal 0 reports a live process of another user (init) as alive through
// EPERM, which is the direction that falls back to the timeout; a reaped child is gone.
func TestIsRunningProcess(t *testing.T) {
	if !isRunningProcess(os.Getpid()) {
		t.Fatal("this process reads as dead")
	}
	if !isRunningProcess(1) {
		t.Fatal("pid 1 reads as dead (a live process of another user is alive)")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if isRunningProcess(cmd.Process.Pid) {
		t.Fatal("a reaped child reads as alive")
	}
}
