package pi_subagents

import (
	"os/exec"
	"runtime"
	"syscall"
)

// configureChild makes the child get SIGTERM when its parent thread ends, so a host killed outright (SIGKILL) leaves no
// child behind.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

// lockThread keeps the goroutine that starts a child on one thread until the child is waited for: the parent-death signal
// is tied to the thread that started the child, not to the process.
func lockThread() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}
