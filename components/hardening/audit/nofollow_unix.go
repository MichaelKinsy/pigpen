//go:build unix

package audit

import "syscall"

// noFollow refuses a symbolic link as the last element of the audit path.
const noFollow = syscall.O_NOFOLLOW
