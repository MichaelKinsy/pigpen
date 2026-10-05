//go:build windows

package ytdlp

import "os/exec"

// ownGroup is a no-op on Windows, which pig-music does not support.
func ownGroup(cmd *exec.Cmd) {}
