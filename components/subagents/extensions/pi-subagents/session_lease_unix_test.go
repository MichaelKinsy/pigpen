//go:build unix

package pi_subagents

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

func leasePidAlive(pid int) bool {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// a zombie (state Z) is gone: it holds nothing
		if i := bytes.LastIndexByte(b, ')'); i >= 0 && i+2 < len(b) && b[i+2] == 'Z' {
			return false
		}
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func leaseKillPid(pid int, hard bool) {
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(pid, sig)
}

// leaseSpawnWriter starts a long-lived process outside this one's group (the writer a dead runner leaves behind).
func leaseSpawnWriter(t *testing.T) int {
	w := exec.Command("sleep", "1000")
	w.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	return w.Process.Pid
}
