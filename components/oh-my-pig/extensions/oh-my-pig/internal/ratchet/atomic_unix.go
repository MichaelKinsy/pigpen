//go:build !windows

package ratchet

import "os"

func renameAtomic(src, dst string) error {
	return os.Rename(src, dst)
}
