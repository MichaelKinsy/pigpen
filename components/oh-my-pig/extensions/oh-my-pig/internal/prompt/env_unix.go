//go:build linux

package prompt

import (
	"bytes"
	"syscall"
)

// osRelease returns the Linux kernel release string (uname -r), matching Node/Bun os.release().
// upstream: packages/coding-agent/src/system-prompt.ts:223
func osRelease() string {
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err != nil {
		return ""
	}
	buf := make([]byte, 0, len(uts.Release))
	for _, b := range uts.Release {
		if b == 0 {
			break
		}
		buf = append(buf, byte(b))
	}
	return string(bytes.TrimSpace(buf))
}
