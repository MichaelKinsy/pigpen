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

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
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
	TailBytes      int
	MaxColumns     int
	ArtifactStore  *ArtifactStore
	SessionID      string
	SDKContext     sdk.Context
	OnPreview      func(text string)
	OnChunk        func(chunk string)
	OnStart        func(pids func() []int)
	OnSpawnPGID    func(pgid int)
	OnExitPGID     func(pgid int)
	StateLoadFile  string // optional snapshot file to source before command
	StateSaveFile  string // optional fallback save file on platforms without ExtraFiles pipes
	StateNonce     string // per-run framing nonce (non-empty enables state capture)
	StatePipeFD    int    // per-run high FD number on Unix (0 => default)
	PreambleFile   string // optional tool-command preamble file sourced before command, excluded from snapshot
	UsePTY         bool   // allocate a pseudo-terminal on Unix when supported
	PTYCols        int
	PTYRows        int
}

// ExecResult mirrors omp's BashResult (bash-executor.ts:148).
type ExecResult struct {
	OutputSummary
	ExitCode          *int
	Cancelled         bool
	TimedOut          bool
	UsedPTY           bool
	ShellNotes        []string
	CapturedState     string
	StateCaptureTrunc bool
}

// executeCommand runs command in the system shell (bash on PATH, else /bin/bash,
// else sh; Git Bash on Windows) inside its own process group (or pseudo-terminal
// when UsePTY is set and supported), streaming output through OutputSink and
// killing the whole process group (SIGTERM -> grace -> SIGKILL) on timeout or
// context cancellation.
func executeCommand(ctx context.Context, opts ExecOptions) (ExecResult, error) {
	sink := NewOutputSink(OutputSinkOptions{
		SpillThreshold: opts.SpillThreshold,
		HeadBytes:      opts.HeadBytes,
		TailBytes:      opts.TailBytes,
		MaxColumns:     opts.MaxColumns,
		ArtifactStore:  opts.ArtifactStore,
		SessionID:      opts.SessionID,
		SDKContext:     opts.SDKContext,
		OnPreview:      opts.OnPreview,
		OnChunk:        opts.OnChunk,
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

	var shellNotes []string
	runCommand := opts.Command
	wantStateCapture := opts.StateNonce != "" && (statePipeSupported || opts.StateSaveFile != "")
	useStatePipe := false
	var stateReadPipe, stateWritePipe *os.File
	if isBashShell(shellPath) {
		if wantStateCapture && statePipeSupported {
			if sr, sw, pErr := os.Pipe(); pErr == nil {
				stateReadPipe = sr
				stateWritePipe = sw
				useStatePipe = true
			}
		}
		if opts.StateLoadFile != "" || opts.PreambleFile != "" || (wantStateCapture && (useStatePipe || opts.StateSaveFile != "")) {
			runCommand = wrapCommandWithState(opts.Command, opts.StateLoadFile, opts.StateSaveFile, opts.PreambleFile, opts.StateNonce, useStatePipe, opts.StatePipeFD)
		}
	} else if opts.StateLoadFile != "" || opts.StateSaveFile != "" || opts.StateNonce != "" || opts.PreambleFile != "" {
		// (shopt, compgen, declare, process substitution). Run command directly
		// and surface a clear note instead of failing with a syntax error.
		shellNotes = append(shellNotes, "bash not found on PATH; ran under sh without persistent shell state or tool-command preamble.")
	}
	args := append(append([]string(nil), shellArgs...), runCommand)
	cmd := exec.Command(shellPath, args...)
	cmd.Dir = opts.Cwd
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}

	var (
		reader    *os.File
		childSide *os.File
		usedPTY   bool
	)
	if opts.UsePTY && ptySupported {
		cols := opts.PTYCols
		if cols <= 0 {
			cols = 120
		}
		rows := opts.PTYRows
		if rows <= 0 {
			rows = 40
		}
		ptmx, pts, ptyErr := openPTY(cols, rows)
		if ptyErr == nil {
			reader = ptmx
			childSide = pts
			usedPTY = true
			configurePTYCommand(cmd, pts)
		}
	}

	if !usedPTY {
		configureProcessGroup(cmd)
		pr, pw, pipeErr := os.Pipe()
		if pipeErr != nil {
			sink.Dispose()
			return ExecResult{}, pipeErr
		}
		reader = pr
		childSide = pw
		cmd.Stdout = pw
		cmd.Stderr = pw
	}
	if useStatePipe && stateWritePipe != nil {
		attachStatePipe(cmd, stateWritePipe)
	}

	if startErr := cmd.Start(); startErr != nil {
		_ = reader.Close()
		_ = childSide.Close()
		if stateReadPipe != nil {
			_ = stateReadPipe.Close()
		}
		if stateWritePipe != nil {
			_ = stateWritePipe.Close()
		}
		sink.Dispose()
		return ExecResult{}, startErr
	}
	_ = childSide.Close()
	if stateWritePipe != nil {
		_ = stateWritePipe.Close()
	}

	var stateBuf strings.Builder
	var stateTruncated bool
	var stateReadDone chan struct{}
	if useStatePipe && stateReadPipe != nil {
		stateReadDone = make(chan struct{})
		endPostMarker := "#__OMP_END_POST_" + opts.StateNonce + "__"
		go func() {
			defer close(stateReadDone)
			buf := make([]byte, 32*1024)
			for {
				n, rErr := stateReadPipe.Read(buf)
				if n > 0 {
					if stateBuf.Len() < maxStateCaptureBytes {
						take := n
						if stateBuf.Len()+take > maxStateCaptureBytes {
							take = maxStateCaptureBytes - stateBuf.Len()
							stateTruncated = true
						}
						stateBuf.Write(buf[:take])
					} else {
						stateTruncated = true
					}
					// Stop immediately once the POST sentinel arrives so any
					// `&`-backgrounded child that inherited pipeFD cannot hold
					// the state pipe open past parent exit.
					if !stateTruncated && strings.Contains(stateBuf.String(), endPostMarker) {
						return
					}
				}
				if rErr != nil {
					return
				}
			}
		}()
	}

	pid := cmd.Process.Pid
	attachProcessGroup(pid)
	if opts.OnSpawnPGID != nil {
		opts.OnSpawnPGID(pid)
	}
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

	var acceptMu sync.Mutex
	acceptingChunks := true

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, rErr := reader.Read(buf)
			if n > 0 {
				acceptMu.Lock()
				ok := acceptingChunks
				acceptMu.Unlock()
				if ok {
					sink.Push(string(buf[:n]))
				}
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
		// Match omp (`exec/bash-executor.ts:1008-1037`): stop accepting chunks
		// BEFORE signalling the process group so shell job-termination messages
		// (e.g. `Terminated: 15   sleep 20`) triggered by our own kill can
		// never leak into the captured output.
		acceptMu.Lock()
		acceptingChunks = false
		acceptMu.Unlock()
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
		// Match omp (`exec/bash-executor.ts:1008-1037`): on abort, immediately
		// stop accepting chunks, send SIGTERM to the process group and wait
		// briefly for a fast exit (up to 100ms); if the process group is still
		// finish the graceful SIGTERM->SIGKILL reap asynchronously so the job
		// settles with `[Command cancelled]\n` immediately instead of stalling
		// the full 2s grace window while the next `read proc://` runs.
		acceptMu.Lock()
		acceptingChunks = false
		acceptMu.Unlock()
		done := make(chan struct{})
		go func() {
			<-waitDone
			close(done)
		}()
		reapDone := make(chan struct{})
		go func() {
			defer close(reapDone)
			killProcessTreeGraceful(cmd.Process, done, termCleanupGrace)
			<-done
			pidMu.Lock()
			alive = false
			pidMu.Unlock()
			if opts.OnExitPGID != nil {
				opts.OnExitPGID(pid)
			}
			select {
			case <-readDone:
				_ = reader.Close()
			case <-time.After(250 * time.Millisecond):
				_ = reader.Close()
				<-readDone
			}
			if stateReadDone != nil {
				select {
				case <-stateReadDone:
					_ = stateReadPipe.Close()
				case <-time.After(250 * time.Millisecond):
					_ = stateReadPipe.Close()
					<-stateReadDone
				}
			}
		}()
		select {
		case <-reapDone:
		case <-time.After(100 * time.Millisecond):
		}
		return ExecResult{
			OutputSummary: sink.Dump("Command cancelled"),
			Cancelled:     true,
			UsedPTY:       usedPTY,
			ShellNotes:    shellNotes,
		}, nil
	}

	pidMu.Lock()
	alive = false
	pidMu.Unlock()
	if opts.OnExitPGID != nil {
		opts.OnExitPGID(pid)
	}

	select {
	case <-readDone:
		_ = reader.Close()
	case <-time.After(250 * time.Millisecond):
		_ = reader.Close()
		<-readDone
	}

	var capturedState string
	if stateReadDone != nil {
		select {
		case <-stateReadDone:
			_ = stateReadPipe.Close()
		case <-time.After(250 * time.Millisecond):
			_ = stateReadPipe.Close()
			<-stateReadDone
		}
		capturedState = stateBuf.String()
	}

	if timedOut {
		notice := "Command timed out"
		if opts.Timeout > 0 {
			secs := formatSecondsLabel(opts.Timeout.Seconds())
			notice = fmt.Sprintf("Command timed out after %s seconds", secs)
		}
		return ExecResult{
			OutputSummary: sink.Dump(notice),
			Cancelled:     true,
			TimedOut:      true,
			UsedPTY:       usedPTY,
			ShellNotes:    shellNotes,
		}, nil
	}

	if cancelled {
		return ExecResult{
			OutputSummary: sink.Dump("Command cancelled"),
			Cancelled:     true,
			UsedPTY:       usedPTY,
			ShellNotes:    shellNotes,
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
		OutputSummary:     summary,
		ExitCode:          &exitCode,
		UsedPTY:           usedPTY,
		ShellNotes:        shellNotes,
		CapturedState:     capturedState,
		StateCaptureTrunc: stateTruncated,
	}, nil
}

func formatSecondsLabel(secs float64) string {
	if secs == float64(int(secs)) {
		return fmt.Sprintf("%d", int(secs))
	}
	return fmt.Sprintf("%g", secs)
}

func singleQuotePosix(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// resolveToCwd resolves an explicit tool `cwd` parameter against baseCwd,
// expanding leading `~` and aliasing a bare root slash (`/`, `//`, ...) to
// baseCwd so tools don't escape to the filesystem root unintentionally.
// upstream: packages/coding-agent/src/tools/path-utils.ts:323
func resolveToCwd(target, baseCwd string) string {
	return resolveDirectory(target, baseCwd, true)
}

// resolveShellDirectory resolves a hoisted shell `cd <path>` target against
// baseCwd, where `/` means the real filesystem root.
// upstream: packages/coding-agent/src/tools/path-utils.ts:333
func resolveShellDirectory(target, baseCwd string) string {
	return resolveDirectory(target, baseCwd, false)
}

func resolveDirectory(target, baseCwd string, aliasWorkspaceRoot bool) string {
	if target == "" {
		return baseCwd
	}
	if aliasWorkspaceRoot && isBareRootSlash(target) {
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

func isBareRootSlash(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '/' {
			return false
		}
	}
	return true
}
