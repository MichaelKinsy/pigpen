//go:build darwin

package prompt

import (
	"strings"
	"syscall"
)

// osRelease returns the Darwin kernel release string (kern.osrelease, matching uname -r
// and Node/Bun os.release()). Never reads kern.osversion / os.version(), matching omp #4141.
// upstream: packages/coding-agent/src/system-prompt.ts:223
func osRelease() string {
	rel, err := syscall.Sysctl("kern.osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(rel)
}
