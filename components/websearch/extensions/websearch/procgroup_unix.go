//go:build unix

package websearch

import (
	"os/exec"
	"syscall"
)

// killWithChildren runs cmd in its own process group and makes cancellation kill the whole group.
// A credential command is run through `sh -c`, so the process the context deadline kills is the
// shell; a pipeline or a background helper (a gpg or op daemon) it started would otherwise survive
// and keep the output pipe open.
func killWithChildren(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
