//go:build !unix

package websearch

import "os/exec"

// killWithChildren is a no-op here: Windows has no process groups to signal this way, and the
// command's WaitDelay still bounds the wait for a child that keeps the output pipe open.
func killWithChildren(*exec.Cmd) {}
