//go:build windows

package modes

import (
	"os"
	"os/exec"
)

// resolveConditionShell resolves Git Bash or a POSIX `bash.exe`/`sh.exe` on
// Windows. Never falls back to `cmd.exe /C`: cmd exits 1 for unknown POSIX
// builtins such as `test -f done`, which would misclassify a broken condition
// as "false" (`--until` would loop indefinitely) instead of reporting an error.
func resolveConditionShell() (shellPath string, shellArgs []string, err error) {
	return resolveWindowsBashShell(
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		func(p string) bool { _, statErr := os.Stat(p); return statErr == nil },
		exec.LookPath,
	)
}
