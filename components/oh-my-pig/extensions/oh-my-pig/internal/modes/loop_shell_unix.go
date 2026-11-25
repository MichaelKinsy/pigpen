//go:build !windows

package modes

import (
	"os"
	"os/exec"
)

// resolveConditionShell resolves the POSIX shell for `/loop --while`/`--until`
// on Unix: `bash` on PATH, then `/bin/bash`, then `sh`.
func resolveConditionShell() (shellPath string, shellArgs []string, err error) {
	if p, lookErr := exec.LookPath("bash"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	if _, statErr := os.Stat("/bin/bash"); statErr == nil {
		return "/bin/bash", []string{"-c"}, nil
	}
	return "sh", []string{"-c"}, nil
}
