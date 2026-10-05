//go:build !windows

package herdr

import (
	"context"
	"os/exec"
)

// newCommand builds the herdr command.
func newCommand(ctx context.Context, bin string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}
