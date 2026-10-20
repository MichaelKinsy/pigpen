//go:build unix

package pi_subagents

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// readPidNamespaceScope is the Linux PID namespace identity (/proc/self/ns/pid); other systems have none.
func readPidNamespaceScope() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	link, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(link)
}

// defaultKill sends signal to pid (0 only probes it); a missing process and a refused signal read as the two sentinels.
func defaultKill(pid, signal int) error {
	err := syscall.Kill(pid, syscall.Signal(signal))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ESRCH):
		return errNoSuchProcess
	case errors.Is(err, syscall.EPERM):
		return errNotPermitted
	}
	return err
}

// detachBackground starts cmd in its own session, so it keeps running when this process (and its terminal) ends. The child
// leads its own process group (pgid == pid): killing -pid reaches everything it started.
func detachBackground(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalGroup sends signal to the process group led by pgid.
func signalGroup(pgid, signal int) error {
	if pgid <= 1 { // kill(-0) and kill(-1) would reach this process's own group, or every process
		return errNoSuchProcess
	}
	return defaultKill(-pgid, signal)
}

// signalTerm and signalInt are the numbers of SIGTERM and SIGINT.
const (
	signalTerm = int(syscall.SIGTERM)
	signalKill = int(syscall.SIGKILL)
)

// groupHasMembers reports whether the process group still has a live (not zombie) member.
func groupHasMembers(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := defaultKill(-pgid, 0)
	return err == nil || errors.Is(err, errNotPermitted)
}

// backgroundSupported reports whether a run's child can outlive this process with its exit recorded for the next session to
// adopt (a detached shell wrapper). Elsewhere the child is started directly and a run ends with its supervisor.
const backgroundSupported = true
