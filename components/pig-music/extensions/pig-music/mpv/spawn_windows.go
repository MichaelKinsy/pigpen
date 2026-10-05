//go:build windows

package mpv

import (
	"context"
	"os/exec"
)

// Windows is out of scope for pig-music: unix sockets and Setsid assume a
// Unix-like system. These stubs keep the package compiling.
func detach(cmd *exec.Cmd) {}

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func lockFile(ctx context.Context, path string) (func(), error) { return func() {}, nil }
