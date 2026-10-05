//go:build !windows

package ytdlp

import (
	"os/exec"
	"syscall"
)

// ownGroup runs the command as the leader of a process group of its own and
// makes cancellation kill that whole group, so that nothing yt-dlp started (a
// wrapper's child, a JavaScript runtime) outlives a timed-out run.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
