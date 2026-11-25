//go:build windows

package browser

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureChildProcess(_ *exec.Cmd) {}

func attachDebuggingPipe(_ *exec.Cmd) ([]*os.File, []*os.File, bool) {
	return nil, nil, false
}

func killProcessTree(cmd *exec.Cmd, _ time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid > 0 {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	}
	_ = cmd.Process.Kill()
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const processQueryLimitedInformation = 0x1000
	const stillActive = 259
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// ACCESS_DENIED (5) means the process exists under another token/privilege.
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
