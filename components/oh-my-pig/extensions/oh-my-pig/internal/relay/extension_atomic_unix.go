//go:build !windows

package relay

import "os"

func renameAtomic(src, dst string) error {
	return os.Rename(src, dst)
}
