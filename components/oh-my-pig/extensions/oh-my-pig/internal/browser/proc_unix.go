//go:build !windows

package browser

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// attachDebuggingPipe creates the two unidirectional pipes Chromium expects on
// fds 3 (read by Chromium) and 4 (written by Chromium) when launched with
// `--remote-debugging-pipe`. Holding the parent ends ties Chromium's lifetime
// to this process even on SIGKILL.
// upstream: launch.ts:718-729
func attachDebuggingPipe(cmd *exec.Cmd) (parentEnds []*os.File, childEnds []*os.File, ok bool) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, false
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, nil, false
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, inR, outW)
	return []*os.File{inW, outR}, []*os.File{inR, outW}, true
}

func killProcessTree(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if grace > 0 {
		deadline := time.Now().Add(grace)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(-pid, 0); err != nil {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	// Wait briefly for every member of the killed process group to exit so
	// no dying Chromium helper can race os.RemoveAll and leave behind an
	// empty userDataDir.
	waitEnd := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(waitEnd) {
		if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
