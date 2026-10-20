//go:build !linux && !darwin

package powerline_footer

import "os"

// fileCtimeNs: where the Go standard library exposes no change time (Windows has no st_ctime field in os.FileInfo) the
// modification time stands in, so a permission-only change is not seen by the fingerprint there.
func fileCtimeNs(fi os.FileInfo) int64 { return *new(int64) }
