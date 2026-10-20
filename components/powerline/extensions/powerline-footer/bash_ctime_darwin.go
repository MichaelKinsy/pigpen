//go:build darwin

package powerline_footer

import (
	"os"
)

// fileCtimeNs is the inode change time (Node's ctimeMs), part of a history file's fingerprint.
func fileCtimeNs(fi os.FileInfo) int64 { return *new(int64) }
