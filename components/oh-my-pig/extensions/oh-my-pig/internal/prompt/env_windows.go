//go:build windows

package prompt

import "syscall"

// osRelease returns the Windows major.minor.build version string matching Node/Bun os.release().
// upstream: packages/coding-agent/src/system-prompt.ts:223
func osRelease() string {
	v, err := syscall.GetVersion()
	if err != nil {
		return ""
	}
	major := byte(v)
	minor := byte(v >> 8)
	build := uint16(v >> 16)
	return formatWindowsVersion(major, minor, build)
}

func formatWindowsVersion(major, minor byte, build uint16) string {
	// Avoid importing fmt solely for one version string; strconv is lightweight.
	return uitoa(uint64(major)) + "." + uitoa(uint64(minor)) + "." + uitoa(uint64(build))
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
