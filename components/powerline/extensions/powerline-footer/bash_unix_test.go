//go:build !windows

package powerline_footer

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// signalShellGroup signals the process group, and the process alone when it leads no group. upstream: shell-session.ts:196.
func TestBashSignalShellGroupFallsBackToTheProcess(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30") // no Setsid: it is not a group leader, so kill(-pid) fails
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := signalShellGroup(cmd.Process, syscall.SIGKILL); err != nil {
		t.Fatalf("signalShellGroup: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process was not signalled")
	}
}

// The shell leads its own session (spawn detached upstream), so its process group can be signalled as a whole.
func TestBashShellProcessLeadsItsOwnGroup(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	configureShellProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, pgid, cmd.Process.Pid, "process group id is the shell's pid")
}
