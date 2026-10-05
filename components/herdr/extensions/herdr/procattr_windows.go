//go:build windows

package herdr

import (
	"context"
	"os/exec"
	"syscall"
)

// newCommand builds the herdr command without a console window.
func newCommand(ctx context.Context, bin string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
