//go:build !linux && !darwin && !windows

package prompt

func osRelease() string {
	return ""
}
