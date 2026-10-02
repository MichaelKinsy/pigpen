//go:build !windows

package websearch

import "syscall"

const (
	oNoFollow  = syscall.O_NOFOLLOW
	oDirectory = syscall.O_DIRECTORY
)
