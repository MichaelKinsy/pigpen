package pi_subagents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Port of executeWorkflowHostCommand and the workflowHostCommandRunner of the executor (workflows/host-command.ts:104-235,
// subagent-executor.ts:3665): `runs.host(key, { kind: "command", ... })`, a trusted shell command of the workflow's cwd whose
// output is saved to a claimed file.

const (
	hostMaxCaptureBytes = 1024 * 1024
	hostMaxPreviewBytes = 4 * 1024
)

// boundedAppend is appendBounded: current plus the chunk, cut to limit bytes (on a character boundary, with a marker).
func boundedAppend(current string, chunk []byte, limit int) string {
	if len(current) >= limit {
		return current
	}
	s := current + string(chunk)
	if len(s) <= limit {
		return s
	}
	end := limit - 3
	if end < 0 {
		end = 0
	}
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "..."
}

type hostCapture struct {
	mu                    sync.Mutex
	stdout, stderr, whole string
}

type hostStreamWriter struct {
	c      *hostCapture
	stderr bool
}

func (w hostStreamWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	if w.stderr {
		w.c.stderr = boundedAppend(w.c.stderr, p, hostMaxPreviewBytes)
	} else {
		w.c.stdout = boundedAppend(w.c.stdout, p, hostMaxPreviewBytes)
	}
	w.c.whole = boundedAppend(w.c.whole, p, hostMaxCaptureBytes)
	return len(p), nil
}

func wfIsWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// assertSafeExplicitHostOutput is assertSafeExplicitOutput: the output file stays inside the workflow cwd, through links.
func assertSafeExplicitHostOutput(cwd, outputPath, key string) (string, error) {
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	existing := filepath.Dir(outputPath)
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		next := filepath.Dir(existing)
		if next == existing {
			break
		}
		existing = next
	}
	existingReal, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	outside := fmt.Errorf("runs.host('%s') output resolves outside the workflow cwd.", key)
	if !wfIsWithin(root, existingReal) {
		return "", outside
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(outputPath))
	if err != nil {
		return "", err
	}
	if !wfIsWithin(root, parent) && parent != root {
		return "", outside
	}
	if st, err := os.Lstat(outputPath); err == nil {
		if !st.Mode().IsRegular() || hardLinkCount(st) > 1 {
			return "", fmt.Errorf("runs.host('%s') output must be a regular, non-linked file path.", key)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return parent, nil
}

func writeExplicitHostOutput(parent, outputPath, capture, key string) error {
	dest := filepath.Join(parent, filepath.Base(outputPath))
	var b [8]byte
	_, _ = rand.Read(b[:])
	tmp := filepath.Join(parent, fmt.Sprintf(".pi-host-output-%d-%s.tmp", os.Getpid(), hex.EncodeToString(b[:])))
	werr := func() error {
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(capture); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, dest)
	}()
	_ = os.Remove(tmp)
	if werr != nil {
		return fmt.Errorf("runs.host('%s') could not atomically replace its output.", key)
	}
	return nil
}

type hostCommandInput struct {
	Key               string
	Params            WorkflowHostCommandParams
	Cwd               string
	DefaultOutputPath string
	ClaimedOutputPath string
}

// executeWorkflowHostCommand runs one command to its end, its deadline or the abort of ctx.
func executeWorkflowHostCommand(ctx context.Context, in hostCommandInput) (WorkflowHostCommandResult, error) {
	outputPath := in.DefaultOutputPath
	if in.Params.Output != "" {
		outputPath = filepath.Join(in.Cwd, in.Params.Output)
		if filepath.IsAbs(in.Params.Output) {
			outputPath = in.Params.Output
		}
		if rel, err := filepath.Rel(in.Cwd, outputPath); err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return WorkflowHostCommandResult{}, fmt.Errorf("runs.host('%s') output escapes the workflow cwd.", in.Key)
		}
	}
	var explicitParent string
	if in.Params.Output != "" {
		p, err := assertSafeExplicitHostOutput(in.Cwd, outputPath, in.Key)
		if err != nil {
			return WorkflowHostCommandResult{}, err
		}
		explicitParent = p
	} else if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return WorkflowHostCommandResult{}, err
	}
	started := time.Now()
	cap := &hostCapture{}
	cmd := shellCommand(quoteExecutableForShell(in.Params.Command, hostPlatform()))
	cmd.Dir = in.Cwd
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = hostStreamWriter{c: cap}, hostStreamWriter{c: cap, stderr: true}
	detachBackground(cmd)
	var startErr error
	if err := cmd.Start(); err != nil {
		startErr = err
	}
	timedOut, stopped := false, false
	var exitCode *int
	if startErr == nil {
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		timer := time.NewTimer(time.Duration(in.Params.TimeoutMs) * time.Millisecond)
		defer timer.Stop()
		var werr error
		kill := func() {
			pid := cmd.Process.Pid
			if runtime.GOOS == "windows" {
				_ = cmd.Process.Kill()
				return
			}
			_ = signalGroup(pid, signalTerm)
		}
		select {
		case werr = <-waited:
		case <-timer.C:
			timedOut = true
			kill()
		case <-ctx.Done():
			stopped = true
			kill()
		}
		if timedOut || stopped {
			select {
			case werr = <-waited:
			case <-time.After(time.Second):
				if runtime.GOOS == "windows" {
					_ = cmd.Process.Kill()
				} else {
					_ = signalGroup(cmd.Process.Pid, signalKill)
				}
				werr = <-waited
			}
		}
		var ee *exec.ExitError
		switch {
		case werr == nil:
			zero := 0
			exitCode = &zero
		case errors.As(werr, &ee):
			if n := ee.ExitCode(); n >= 0 {
				exitCode = &n
			}
		}
	}
	state := "failed"
	switch {
	case timedOut:
		state = "timed-out"
	case stopped:
		state = "stopped"
	case exitCode != nil && *exitCode == 0 && startErr == nil:
		state = "passed"
	}
	errText := ""
	switch {
	case startErr != nil:
		errText = startErr.Error()
	case state == "timed-out":
		errText = fmt.Sprintf("Command timed out after %dms.", in.Params.TimeoutMs)
	case state == "stopped":
		errText = "Command stopped because the workflow was aborted."
	case state == "failed":
		code := "unknown"
		if exitCode != nil {
			code = fmt.Sprint(*exitCode)
		}
		errText = "Command exited with code " + code + "."
	}
	cap.mu.Lock()
	whole, stdout, stderr := cap.whole, cap.stdout, cap.stderr
	cap.mu.Unlock()
	save := func() error {
		if in.ClaimedOutputPath != "" && workflowHostOutputClaimPath(outputPath) != in.ClaimedOutputPath {
			return errors.New("output path changed after it was claimed.")
		}
		if explicitParent != "" {
			cur, err := assertSafeExplicitHostOutput(in.Cwd, outputPath, in.Key)
			if err != nil {
				return err
			}
			if cur != explicitParent {
				return errors.New("output parent changed while the command was running.")
			}
			return writeExplicitHostOutput(explicitParent, outputPath, whole, in.Key)
		}
		return os.WriteFile(outputPath, []byte(whole), 0o600)
	}
	if err := save(); err != nil {
		return WorkflowHostCommandResult{}, fmt.Errorf("runs.host('%s') could not save command output: %s", in.Key, err.Error())
	}
	return WorkflowHostCommandResult{Key: in.Key, OK: state == "passed", State: state, ExitCode: exitCode, Stdout: stdout, Stderr: stderr, OutputPath: outputPath, DurationMs: time.Since(started).Milliseconds(), Error: errText}, nil
}

// wfHostRunner is workflowHostCommandRunner: it claims the output path of each command (and so refuses two commands, or a
// command and a child, writing one file) and runs it.
type wfHostRunner struct {
	WorkflowCwd, ArtifactsDir, WorkflowRunID string
	Claimed                                  map[string]string
	Produced                                 map[string]bool
	Authorize                                func(key string, p WorkflowHostCommandParams) string
	mu                                       sync.Mutex
}

func (r *wfHostRunner) Host(ctx context.Context, key string, params WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
	return WorkflowAsync(func() (WorkflowHostCommandResult, error) {
		if r.Authorize != nil {
			if msg := r.Authorize(key, params); msg != "" {
				return WorkflowHostCommandResult{}, errors.New(msg)
			}
		}
		defaultOutput := filepath.Join(r.ArtifactsDir, "outputs", sanitizeRunPathSegment(r.WorkflowRunID, 120), "host", sanitizeRunPathSegment(key, 120)+".log")
		outputPath := defaultOutput
		if params.Output != "" {
			outputPath = filepath.Join(r.WorkflowCwd, params.Output)
		}
		claim := workflowHostOutputClaimPath(outputPath)
		r.mu.Lock()
		if prev, ok := r.Claimed[claim]; ok {
			r.mu.Unlock()
			return WorkflowHostCommandResult{}, fmt.Errorf("runs.host('%s') output path is already claimed by '%s': %s.", key, prev, outputPath)
		}
		r.Claimed[claim] = "host:" + key
		r.mu.Unlock()
		res, err := executeWorkflowHostCommand(ctx, hostCommandInput{Key: key, Params: params, Cwd: r.WorkflowCwd, DefaultOutputPath: defaultOutput, ClaimedOutputPath: claim})
		if err != nil {
			return res, err
		}
		r.mu.Lock()
		r.Produced[workflowHostOutputClaimPath(res.OutputPath)] = true
		r.mu.Unlock()
		return res, nil
	})
}
