//go:build windows

package warden

import "os"

// statDevIno: Go's FileInfo does not carry the file index on Windows without an open handle, so the identity
// compares size and modification time only (see PORT.md).
func statDevIno(os.FileInfo) (uint64, uint64) { return 0, 0 }
