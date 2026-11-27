//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// checkPigFile accepts only an executable regular file owned by root or by this
// process's user, so a binary another user could have replaced is never run.
func checkPigFile(info os.FileInfo, resolved string) error {
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%q is not an executable regular file", resolved)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %q", resolved)
	}
	if stat.Uid != 0 && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%q is owned by uid %d, want root or uid %d", resolved, stat.Uid, os.Getuid())
	}
	return nil
}
