package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/exec/bash-executor.ts

const (
	// termCleanupGrace matches omp's 2s TERM->KILL grace so cancelled commands
	// can run trap '…' TERM cleanup handlers (bash-job-lifecycle.test.ts).
	termCleanupGrace = 2 * time.Second
)

// ExecOptions configures a single local shell command execution.
type ExecOptions struct {
	Command        string
	Cwd            string
	Env            []string
	Timeout        time.Duration // 0 disables the deadline
	SpillThreshold int
	HeadBytes      int
	MaxColumns     int
	ArtifactStore  *ArtifactStore
	OnPreview      func(text string)
	OnStart        func(pids func() []int)
	StateLoadFile  string // optional snapshot file to source before command
	StateSaveFile  string // optional temp file to write updated shell state into
}

// ExecResult mirrors omp's BashResult (bash-executor.ts:111).
type ExecResult struct {
	OutputSummary
	ExitCode  *int
	Cancelled bool
	TimedOut  bool
}

// executeCommand runs command in the system shell (bash, else sh; Git Bash on
// Windows) inside its own process group, streaming output through OutputSink
// and killing the whole process group on timeout or context cancellation.
func executeCommand(ctx context.Context, opts ExecOptions) (ExecResult, error) {
	sink := NewOutputSink(OutputSinkOptions{
		SpillThreshold: opts.SpillThreshold,
		HeadBytes:      opts.HeadBytes,
		MaxColumns:     opts.MaxColumns,
		ArtifactStore:  opts.ArtifactStore,
		ArtifactPrefix: "bash",
		OnPreview:      opts.OnPreview,
	})

	if ctx.Err() != nil {
		return ExecResult{
			OutputSummary: sink.Dump("Command cancelled"),
			Cancelled:     true,
		}, nil
	}

	shellPath, shellArgs, err := defaultShellConfig()
	if err != nil {
		sink.Dispose()
		return ExecResult{}, err
	}

	// Wrap with persistent shell state restore/save when requested.
	// Working directory is NOT persisted: cmd.Dir sets the requested cwd
	// (explicit `cwd` param or session directory) for every call.
	runCommand := opts.Command
	if opts.StateLoadFile != "" || opts.StateSaveFile != "" {
		runCommand = wrapCommandWithState(opts.Command, opts.StateLoadFile, opts.StateSaveFile)
	}
	args := append(append([]string(nil), shellArgs...), runCommand)
	cmd := exec.Command(shellPath, args...)
	cmd.Dir = opts.Cwd
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}
	configureProcessGroup(cmd)

	// Combine stdout and stderr through a single pipe so stream order matches
	// what a terminal sees and a single reader drains both.
	pr, pw, pipeErr := os.Pipe()
	if pipeErr != nil {
		sink.Dispose()
		return ExecResult{}, pipeErr
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if startErr := cmd.Start(); startErr != nil {
		_ = pr.Close()
		_ = pw.Close()
		sink.Dispose()
		return ExecResult{}, startErr
	}
	// Close the parent's write end so pr sees EOF when the process tree exits.
	_ = pw.Close()

	pid := cmd.Process.Pid
	var pidMu sync.Mutex
	alive := true
	if opts.OnStart != nil {
		opts.OnStart(func() []int {
			pidMu.Lock()
			defer pidMu.Unlock()
			if !alive {
				return nil
			}
			return []int{pid}
		})
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, rErr := pr.Read(buf)
			if n > 0 {
				sink.Push(string(buf[:n]))
			}
			if rErr != nil {
				if rErr != io.EOF {
					_ = rErr
				}
				return
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var timeoutCh <-chan time.Time
	var timer *time.Timer
	if opts.Timeout > 0 {
		timer = time.NewTimer(opts.Timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	var (
		waitErr   error
		timedOut  bool
		cancelled bool
	)

	select {
	case waitErr = <-waitDone:
	case <-timeoutCh:
		timedOut = true
		cancelled = true
		done := make(chan struct{})
		go func() {
			<-waitDone
			close(done)
		}()
		killProcessTreeGraceful(cmd.Process, done, termCleanupGrace)
		<-done
	case <-ctx.Done():
		cancelled = true
		done := make(chan struct{})
		go func() {
			<-waitDone
			close(done)
		}()
		killProcessTreeGraceful(cmd.Process, done, termCleanupGrace)
		<-done
	}

	pidMu.Lock()
	alive = false
	pidMu.Unlock()

	// Wait briefly for the pipe reader to drain any buffered output; if a
	// detached grandchild kept the pipe open, close our read end so we do not
	// hang.
	select {
	case <-readDone:
		_ = pr.Close()
	case <-time.After(250 * time.Millisecond):
		_ = pr.Close()
		<-readDone
	}

	if timedOut {
		secs := int(opts.Timeout.Round(time.Second) / time.Second)
		notice := "Command timed out"
		if secs > 0 {
			notice = fmt.Sprintf("Command timed out after %d seconds", secs)
		}
		return ExecResult{
			OutputSummary: sink.Dump(notice),
			Cancelled:     true,
			TimedOut:      true,
		}, nil
	}

	if cancelled {
		return ExecResult{
			OutputSummary: sink.Dump("Command cancelled"),
			Cancelled:     true,
		}, nil
	}

	var exitCode int
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = processExitCode(exitErr.ProcessState)
		} else {
			sink.Dispose()
			return ExecResult{}, waitErr
		}
	} else {
		exitCode = processExitCode(cmd.ProcessState)
	}

	summary := sink.Dump("")
	return ExecResult{
		OutputSummary: summary,
		ExitCode:      &exitCode,
	}, nil
}

func singleQuotePosix(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// resolveToCwd resolves path against baseCwd, expanding a leading `~`.
func resolveToCwd(target, baseCwd string) string {
	if target == "" {
		return baseCwd
	}
	if target == "~" || strings.HasPrefix(target, "~/") || strings.HasPrefix(target, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if target == "~" {
				return home
			}
			return filepath.Join(home, target[2:])
		}
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(baseCwd, target))
}
