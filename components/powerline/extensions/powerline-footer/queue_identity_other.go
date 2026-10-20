//go:build !linux && !darwin && !freebsd

package powerline_footer

import "os"

// inboxIdentity on a platform without a change time in Go's stat: size, modification time and mode. An in-place edit that
// keeps the size and restores the modification time goes unnoticed there (the original's fingerprint also has the change time).
func inboxIdentity(fi os.FileInfo) string { return genericIdentity(fi) }
