//go:build windows

package websearch

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) { hideWindow(cmd) }

// terminateProcessTree ends the process tree with taskkill /T /F, and the process itself when
// taskkill fails.
func terminateProcessTree(cmd *exec.Cmd) {
	killer := exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	hideWindow(killer)
	go func() {
		if err := killer.Run(); err != nil {
			_ = cmd.Process.Kill()
		}
	}()
}

// processGone reports that no process has this id: OpenProcess fails with ERROR_INVALID_PARAMETER.
// Any other failure (access denied, say) is not proof.
func processGone(pid int) bool {
	p, err := os.FindProcess(pid)
	if err == nil {
		_ = p.Release()
		return false
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == 87
}

func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
