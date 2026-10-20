package websearch

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Child processes run through the host, as the original's pi.exec does: the extension builds
// no process of its own. Extension() installs the runner over the SDK context; the library
// has none until then (a test installs a canned one).

// ExecResult is the outcome of one host command.
type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
	// Killed reports that the timeout or a cancellation fired.
	Killed bool
}

// ExecRunner runs command with args (no shell) and gives up after timeout when it is positive. An
// error is a failure of the host call; a command that cannot start is a result with code 1.
type ExecRunner func(ctx context.Context, command string, args []string, timeout time.Duration) (ExecResult, error)

var (
	execRunnerMu sync.Mutex
	execRunner   ExecRunner
)

// SetExecRunner installs the host command runner and returns a restore function.
func SetExecRunner(r ExecRunner) func() {
	execRunnerMu.Lock()
	prev := execRunner
	execRunner = r
	execRunnerMu.Unlock()
	return func() {
		execRunnerMu.Lock()
		execRunner = prev
		execRunnerMu.Unlock()
	}
}

var errNoExecRunner = errors.New("no host command runner is installed")

func runHostCommand(ctx context.Context, command string, args []string, timeout time.Duration) (ExecResult, error) {
	execRunnerMu.Lock()
	r := execRunner
	execRunnerMu.Unlock()
	if r == nil {
		return ExecResult{}, errNoExecRunner
	}
	return r(ctx, command, args, timeout)
}
