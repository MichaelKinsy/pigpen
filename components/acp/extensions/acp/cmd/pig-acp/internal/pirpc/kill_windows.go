//go:build windows

package pirpc

import (
	"os/exec"
	"time"
)

// termGrace is how long a child may take to stop; on Windows it is killed at once.
const termGrace = time.Duration(0)

func terminate(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
