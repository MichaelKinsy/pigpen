//go:build linux

package pi_goal_x

import (
	"os/exec"
	"runtime"
	"syscall"
)

// configureAuditChild puts the child in its own process group and gives it SIGTERM when its parent thread ends, so a host
// killed outright leaves no pig behind.
func configureAuditChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// lockAuditThread keeps the goroutine that starts a child on one thread until the child is waited for: the parent-death
// signal is tied to the thread that started the child, not to the process.
func lockAuditThread() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}
