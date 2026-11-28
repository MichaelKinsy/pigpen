//go:build !darwin && !linux

package shell

import (
	"errors"
	"os"
	"os/exec"
)

// ptySupported reports whether openPTY is available on this platform.
const ptySupported = false

func openPTY(cols, rows int) (*os.File, *os.File, error) {
	return nil, nil, errors.New("pty unavailable on this platform")
}

func configurePTYCommand(cmd *exec.Cmd, pts *os.File) {
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
}
