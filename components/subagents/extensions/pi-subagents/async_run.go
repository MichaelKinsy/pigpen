package pi_subagents

// A background run: its configuration, the detached child it supervises, the progress it reads from the child's output, its
// control inbox and the files it publishes (status, events, result, process-terminal proof). Ports the single-agent path of
// async-execution.ts (spawnRunner, executeAsyncSingle) and subagent-runner.ts, and the portable control channel of
// control-channel.ts. The original's runner is a Node process that hosts the child session in-process; here the child is a
// `pig --mode json` process in its own session, and the same files carry what the runner knew.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	stopMessage            = "Subagent stopped by user."
	stopInboxClosedMessage = "Runner stop inbox is closed. Retry stop after runner shutdown is observed."
	superviseTickMs        = 250
	statusWriteEvery       = 500
	// stopGrace is how long a stopped or timed-out child has to end after SIGTERM before it is killed.
	stopGrace             = 5 * time.Second
	asyncDefaultTimeoutMs = 30 * 60 * 1000
)

// RunConfig is runner-config.json: what a run needs to be finished by whoever is still alive when its child ends.
type RunConfig struct {
	ID                         string `json:"id"`
	AsyncDir                   string `json:"asyncDir"`
	ResultsDir                 string `json:"resultsDir"`
	Agent                      string `json:"agent"`
	Task                       string `json:"task"`
	Cwd                        string `json:"cwd"`
	Model                      string `json:"model,omitempty"`
	Thinking                   string `json:"thinking,omitempty"`
	SessionID                  string `json:"sessionId,omitempty"`
	CompletionOwnerID          string `json:"completionOwnerId,omitempty"`
	ToolCallID                 string `json:"toolCallId,omitempty"`
	StartedAt                  int64  `json:"startedAt"`
	TimeoutMs                  *int64 `json:"timeoutMs,omitempty"`
	DeadlineAt                 *int64 `json:"deadlineAt,omitempty"`
	CheckpointBeforeDeadlineMs *int64 `json:"checkpointBeforeDeadlineMs,omitempty"`
	SessionDir                 string `json:"sessionDir,omitempty"`
	RunnerProcessInstanceID    string `json:"runnerProcessInstanceId"`
	LaunchContractDigest       string `json:"launchContractDigest,omitempty"`
	Context                    string `json:"context,omitempty"`
	ResumeOf                   string `json:"resumeOf,omitempty"`
}

func runConfigPath(asyncDir string) string { return filepath.Join(asyncDir, "runner-config.json") }

func readRunConfig(asyncDir string) (*RunConfig, error) {
	data, err := os.ReadFile(runConfigPath(asyncDir))
	if err != nil {
		return nil, err
	}
	var c RunConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ── the child process seam ──

// bgChildSpec is how a background child is started: its files are all in the run directory.
type bgChildSpec struct {
	Bin, Cwd                              string
	Args, Env                             []string
	StdinPath, OutPath, ErrPath, ExitPath string
}

// bgChild is a started background child (the runner of the original).
type bgChild interface {
	PID() int
	// Wait returns the exit code (the shell's, 128+signal, for a signalled child) once the child is gone.
	Wait() (exitCode int, err error)
}

// startBgChild starts a child; a test replaces it with a fake that needs no process.
var startBgChild = defaultStartBgChild

type osBgChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
	err  error
}

func (c *osBgChild) PID() int { return c.cmd.Process.Pid }

func (c *osBgChild) Wait() (int, error) {
	<-c.done
	return c.code, c.err
}

const wrapperScript = `trap : TERM INT HUP
in="$1"; out="$2"; err="$3"; exitf="$4"; shift 4
"$@" <"$in" >"$out" 2>"$err"
code=$?
printf '%s' "$code" >"$exitf.tmp" && mv "$exitf.tmp" "$exitf"
exit "$code"
`

// posixShell is the shell Node's spawn(command, {shell: true}) uses on unix. It is a platform shell, not a program a scenario shims, so
// it is named once here (the exec-coverage scan of pigeq reads literal command names only).
const posixShell = "/bin/sh"

func defaultStartBgChild(spec bgChildSpec) (bgChild, error) {
	var cmd *exec.Cmd
	var files []*os.File
	closeFiles := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	if backgroundSupported {
		cmd = exec.Command(posixShell, append([]string{"-c", wrapperScript, "sh", spec.StdinPath, spec.OutPath, spec.ErrPath, spec.ExitPath, spec.Bin}, spec.Args...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	} else {
		cmd = exec.Command(spec.Bin, spec.Args...)
		in, err := os.Open(spec.StdinPath)
		if err != nil {
			return nil, err
		}
		files = append(files, in)
		out, err := os.OpenFile(spec.OutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			closeFiles()
			return nil, err
		}
		files = append(files, out)
		errf, err := os.OpenFile(spec.ErrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			closeFiles()
			return nil, err
		}
		files = append(files, errf)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errf
	}
	detachBackground(cmd)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	if err := cmd.Start(); err != nil {
		closeFiles()
		return nil, err
	}
	c := &osBgChild{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		closeFiles()
		c.code = cmd.ProcessState.ExitCode()
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			c.err = err
		}
		close(c.done)
	}()
	return c, nil
}

// ── the progress a child's JSON event stream shows ──

// progressTracker reads the child's `--mode json` output as it grows.
type progressTracker struct {
	path        string
	off         int64
	partial     []byte
	SessionID   string
	TurnCount   int
	ToolCount   int
	CurrentTool string
	CurrentArgs string
	CurrentPath string
	ToolStarted int64
	LastEventAt int64
	FinalText   string
	StopReason  string
	ErrorText   string
	Tokens      TokenUsage
	Cost        float64
	Model       string
	HasModelEnd bool
}

func newProgressTracker(path string) *progressTracker { return &progressTracker{path: path} }

func textOfMessage(m map[string]any) string {
	var parts []string
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		for _, b := range c {
			if bm, ok := b.(map[string]any); ok && bm["type"] == "text" {
				if s, ok := bm["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func compactArgs(v any) (summary, path string) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range []string{"path", "file_path", "filePath", "file"} {
		if s, ok := m[k].(string); ok && s != "" {
			path = s
			break
		}
	}
	for _, k := range []string{"command", "pattern", "query", "path", "file_path", "url"} {
		if s, ok := m[k].(string); ok && s != "" {
			summary = strings.Join(strings.Fields(s), " ")
			if len(summary) > 140 {
				summary = summary[:139] + "…"
			}
			break
		}
	}
	return summary, path
}

// poll reads what the child appended since the last call; it reports whether anything changed.
func (p *progressTracker) poll(now int64) bool {
	f, err := os.Open(p.path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() <= p.off {
		return false
	}
	buf := make([]byte, st.Size()-p.off)
	n, _ := f.ReadAt(buf, p.off)
	p.off += int64(n)
	data := append(p.partial, buf[:n]...)
	p.partial = nil
	if i := bytes.LastIndexByte(data, '\n'); i < 0 {
		p.partial = data
		return true
	} else if i < len(data)-1 {
		p.partial = append([]byte(nil), data[i+1:]...)
		data = data[:i+1]
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		p.apply(sc.Bytes(), now)
	}
	p.LastEventAt = now
	return true
}

func (p *progressTracker) apply(line []byte, now int64) {
	var ev map[string]any
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	switch ev["type"] {
	case "session":
		if id, ok := ev["id"].(string); ok {
			p.SessionID = id
		}
	case "turn_start":
		p.TurnCount++
	case "tool_execution_start":
		p.CurrentTool, _ = ev["toolName"].(string)
		p.CurrentArgs, p.CurrentPath = compactArgs(ev["args"])
		p.ToolStarted = now
	case "tool_execution_end":
		p.ToolCount++
		p.CurrentTool, p.CurrentArgs, p.CurrentPath, p.ToolStarted = "", "", "", 0
	case "message_end":
		m, ok := ev["message"].(map[string]any)
		if !ok || m["role"] != "assistant" {
			return
		}
		p.HasModelEnd = true
		p.FinalText = strings.TrimSpace(textOfMessage(m))
		p.StopReason, _ = m["stopReason"].(string)
		if e, ok := m["errorMessage"].(string); ok && e != "" {
			p.ErrorText = e
		}
		if mm, ok := m["model"].(string); ok && mm != "" {
			p.Model = mm
		}
		if u, ok := m["usage"].(map[string]any); ok {
			in, _ := u["input"].(float64)
			out, _ := u["output"].(float64)
			total, _ := u["totalTokens"].(float64)
			if total == 0 {
				total = in + out
			}
			p.Tokens.Input += int64(in)
			p.Tokens.Output += int64(out)
			p.Tokens.Total += int64(total)
			if c, ok := u["cost"].(map[string]any); ok {
				if t, ok := c["total"].(float64); ok {
					p.Cost += t
				}
			}
		}
	}
}

// ── control inbox (control-channel.ts) ──

func controlInboxDir(asyncDir string) string { return filepath.Join(asyncDir, "control") }
func interruptRequestPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "interrupt.json")
}
func timeoutRequestPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "timeout.json")
}
func stopRequestsDir(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "stop-requests")
}
func stopInboxClosedPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "stop-inbox-closed.json")
}

type controlRequest struct {
	Type        string `json:"type"`
	TS          int64  `json:"ts,omitempty"`
	Source      string `json:"source,omitempty"`
	Reason      string `json:"reason,omitempty"`
	TargetIndex *int   `json:"targetIndex,omitempty"`
	ChildID     string `json:"childId,omitempty"`
}

// requestAsyncInterrupt drops the portable interrupt request the runner's inbox picks up on any OS.
func requestAsyncInterrupt(asyncDir, source string) (string, error) {
	p := interruptRequestPath(asyncDir)
	return p, asyncWriteJSON(p, controlRequest{Type: "interrupt", TS: nowMs(), Source: source})
}

func requestAsyncTimeout(asyncDir, source, reason string) (string, error) {
	p := timeoutRequestPath(asyncDir)
	return p, asyncWriteJSON(p, controlRequest{Type: "timeout", TS: nowMs(), Source: source, Reason: reason})
}

func closeStopInbox(asyncDir string) error {
	return asyncWriteJSON(stopInboxClosedPath(asyncDir), map[string]any{"version": 1, "closedAt": nowMs()})
}

func validStopChildID(id string) bool {
	return jsTrim(id) != "" && len(id) <= 256 && !strings.ContainsAny(id, "\r\n")
}

// requestAsyncStop writes a stop request into the inbox; once the runner closed the inbox it is refused.
func requestAsyncStop(asyncDir, source string, targetIndex *int, childID string) (string, error) {
	if targetIndex != nil && (*targetIndex < 0 || *targetIndex > 1_000_000) {
		return "", errors.New("child index must be a non-negative integer.")
	}
	if childID != "" && !validStopChildID(childID) {
		return "", errors.New("stop childId must be a non-empty string without newlines and at most 256 characters.")
	}
	closed := stopInboxClosedPath(asyncDir)
	if regularFileExists(closed) {
		return "", errors.New(stopInboxClosedMessage)
	}
	req := controlRequest{Type: "stop", TS: nowMs(), Source: source, TargetIndex: targetIndex, ChildID: childID}
	p := filepath.Join(stopRequestsDir(asyncDir), fmt.Sprintf("%013d-%s.json", req.TS, randomToken()))
	if err := asyncWriteJSON(p, req); err != nil {
		return "", err
	}
	if regularFileExists(closed) {
		_ = os.Remove(p)
		return "", errors.New(stopInboxClosedMessage)
	}
	return p, nil
}

// consumeControl reads and removes the pending interrupt, stop and timeout requests of a run.
func consumeControl(asyncDir string) (interrupt, stop, timeout bool) {
	take := func(p string) bool {
		if !regularFileExists(p) {
			return false
		}
		return os.Remove(p) == nil
	}
	interrupt = take(interruptRequestPath(asyncDir))
	timeout = take(timeoutRequestPath(asyncDir))
	if entries, err := os.ReadDir(stopRequestsDir(asyncDir)); err == nil {
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			if take(filepath.Join(stopRequestsDir(asyncDir), n)) {
				stop = true
			}
		}
	}
	return
}

// ── the run ──

// RunHandle is a run this process supervises.
type RunHandle struct {
	Config     *RunConfig
	child      bgChild
	adoptedPID int
	cancel     chan struct{}
	finished   chan struct{}
	once       sync.Once
}

func (h *RunHandle) Done() <-chan struct{} { return h.finished }

// Stop ends the supervision without finalizing (the extension is shutting down; the child keeps running and a later
// session adopts it).
func (h *RunHandle) Detach() { h.once.Do(func() { close(h.cancel) }) }

// runHooks are what a finished run tells the extension.
type runHooks struct {
	// OnProgress is called when the run's status changed (the widget refreshes).
	OnProgress func(asyncDir string)
	// OnComplete is called with the result payload after the result file and the status are published.
	OnComplete func(cfg *RunConfig, result map[string]any, st *AsyncStatus)
}

func stepStatusOf(state string) string {
	switch state {
	case "complete":
		return "complete"
	case "paused":
		return "paused"
	case "stopped":
		return "stopped"
	}
	return "failed"
}

// launchSpec is everything startBackgroundRun needs.
type launchSpec struct {
	Cfg      RunConfig
	Bin      string
	Args     []string
	Env      []string
	Agent    *AgentConfig
	Digest   string
	Recovery *RecoveryDescriptor
}

// startBackgroundRun writes the run directory, starts the child and supervises it on a goroutine.
func startBackgroundRun(spec launchSpec, hooks runHooks) (*RunHandle, error) {
	cfg := spec.Cfg
	if err := os.MkdirAll(cfg.AsyncDir, 0o755); err != nil {
		return nil, fmt.Errorf("Failed to create async run directory '%s': %s", cfg.AsyncDir, err.Error())
	}
	if spec.Recovery != nil {
		if err := asyncWritePrivateJSON(filepath.Join(cfg.AsyncDir, "recovery-descriptor.json"), spec.Recovery); err != nil {
			return nil, fmt.Errorf("Failed to persist async recovery descriptor for '%s': %s", cfg.ID, err.Error())
		}
	}
	cfg.RunnerProcessInstanceID = randomUUID()
	if err := asyncWritePrivateJSON(runConfigPath(cfg.AsyncDir), &cfg); err != nil {
		return nil, err
	}
	stdinPath := filepath.Join(cfg.AsyncDir, "task.txt")
	if err := os.WriteFile(stdinPath, []byte(cfg.Task), 0o600); err != nil {
		return nil, err
	}
	for _, f := range []string{"runner-startup-proceed.json"} {
		_ = os.Remove(filepath.Join(cfg.AsyncDir, f))
	}
	started := cfg.StartedAt
	step := AsyncStep{Agent: cfg.Agent, Status: "pending", Model: cfg.Model, Context: cfg.Context, LaunchDigest: cfg.LaunchContractDigest}
	initial := &AsyncStatus{LifecycleArtifactVersion: lifecycleArtifactVersion, RunID: cfg.ID, CompletionOwnerID: cfg.CompletionOwnerID, ToolCallID: nonEmptyIfDifferent(cfg.ToolCallID, cfg.ID), SessionID: cfg.SessionID,
		Mode: "single", State: "running", StartedAt: ms(started), LastUpdate: msPtrOf(started), TimeoutMs: msPtrOfPtr(cfg.TimeoutMs), DeadlineAt: msPtrOfPtr(cfg.DeadlineAt),
		CurrentStep: intPtr(0), ChainStepCount: intPtr(1), Cwd: cfg.Cwd, SessionDir: cfg.SessionDir, LaunchContractDigest: cfg.LaunchContractDigest, Steps: []AsyncStep{step},
		OutputFile: filepath.Join(cfg.AsyncDir, "output-0.log"), PidNamespaceScope: currentPidNamespaceScope(), Context: cfg.Context}
	if err := initializeProcessTerminal(cfg.AsyncDir, cfg.ID, cfg.RunnerProcessInstanceID); err != nil {
		return nil, fmt.Errorf("Failed to establish async runner lifecycle sidecar: %s", err.Error())
	}
	child, err := startBgChild(bgChildSpec{Bin: spec.Bin, Args: spec.Args, Env: spec.Env, Cwd: cfg.Cwd, StdinPath: stdinPath,
		OutPath: filepath.Join(cfg.AsyncDir, "output-0.log"), ErrPath: filepath.Join(cfg.AsyncDir, "runner.stderr.log"), ExitPath: filepath.Join(cfg.AsyncDir, "exit-0")})
	if err != nil {
		msg := fmt.Sprintf("Failed to start async run '%s': %s", cfg.ID, err.Error())
		persistRunnerStartupFailure(&cfg, msg)
		return nil, errors.New(msg)
	}
	initial.PID = intPtr(child.PID())
	initial.ProcessTerminal = &ProcessTerminal{Version: 1, State: "pending", RunID: cfg.ID, RunnerProcessInstanceID: cfg.RunnerProcessInstanceID}
	if err := writeStatus(cfg.AsyncDir, initial); err != nil {
		return nil, fmt.Errorf("Failed to persist initial async status: %s", err.Error())
	}
	_ = updateActiveRunIndex(cfg.AsyncDir, "running", cfg.ToolCallID, indexOptions{}) // aggregate waits must see the launch before the first status update
	appendJSONLine(filepath.Join(cfg.AsyncDir, "events.jsonl"), map[string]any{"type": "subagent.run.started", "lifecycleArtifactVersion": lifecycleArtifactVersion, "ts": started, "runId": cfg.ID, "mode": "single", "agents": []string{cfg.Agent}, "pid": child.PID()})
	h := &RunHandle{Config: &cfg, child: child, cancel: make(chan struct{}), finished: make(chan struct{})}
	go superviseRun(h, hooks)
	return h, nil
}

func nonEmptyIfDifferent(v, other string) string {
	if v == other {
		return ""
	}
	return v
}

func msPtrOfPtr(v *int64) *ms {
	if v == nil {
		return nil
	}
	return msPtrOf(*v)
}

// persistRunnerStartupFailure records a run that never started as failed, with a result the session is told about.
func persistRunnerStartupFailure(cfg *RunConfig, message string) {
	now := nowMs()
	st := &AsyncStatus{LifecycleArtifactVersion: lifecycleArtifactVersion, RunID: cfg.ID, CompletionOwnerID: cfg.CompletionOwnerID, SessionID: cfg.SessionID, Mode: "single", State: "failed", Error: message,
		StartedAt: ms(cfg.StartedAt), EndedAt: msPtrOf(now), LastUpdate: msPtrOf(now), Cwd: cfg.Cwd, Steps: []AsyncStep{{Agent: cfg.Agent, Status: "failed", Error: message, ExitCode: intPtr(1)}},
		ProcessTerminal: &ProcessTerminal{Version: 1, State: "unknown", RunID: cfg.ID, RunnerProcessInstanceID: cfg.RunnerProcessInstanceID, Reason: "runner-candidate-missing"}}
	_ = writeStatus(cfg.AsyncDir, st)
	_ = updateActiveRunIndex(cfg.AsyncDir, "failed", cfg.ToolCallID, indexOptions{})
}

// ── supervision ──

type superviseOutcome struct {
	exitCode     int
	hasExit      bool
	interrupted  bool
	stopped      bool
	timedOut     bool
	startFailure string
}

func exitFileCode(asyncDir string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(asyncDir, "exit-0"))
	if err != nil {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

func superviseRun(h *RunHandle, hooks runHooks) {
	defer close(h.finished)
	cfg := h.Config
	tracker := newProgressTracker(filepath.Join(cfg.AsyncDir, "output-0.log"))
	exited := make(chan struct{})
	var exitCode int
	var waitErr error
	var pid int
	if h.child != nil {
		pid = h.child.PID()
		go func() {
			exitCode, waitErr = h.child.Wait()
			close(exited)
		}()
	} else {
		// an adopted run: the child is not ours to wait for, so its pid is probed and its exit file read
		pid = h.adoptedPID
		go func() {
			for checkPidLiveness(pid, nil) != pidDead {
				select {
				case <-h.cancel:
					return
				case <-time.After(superviseTickMs * time.Millisecond):
				}
			}
			if code, ok := exitFileCode(cfg.AsyncDir); ok {
				exitCode = code
			} else {
				exitCode = 137
				waitErr = nil
			}
			close(exited)
		}()
	}
	var out superviseOutcome
	var killAt time.Time
	lastWrite := int64(0)
	ticker := time.NewTicker(superviseTickMs * time.Millisecond)
	defer ticker.Stop()
	terminate := func(sig int) { _ = signalGroup(pid, sig) }
	end := false
	for !end {
		select {
		case <-h.cancel:
			return // detached: the child lives on, and the next session adopts the run
		case <-exited:
			end = true
		case <-ticker.C:
		}
		now := nowMs()
		changed := tracker.poll(now)
		interrupt, stop, timeout := consumeControl(cfg.AsyncDir)
		if cfg.DeadlineAt != nil && now >= *cfg.DeadlineAt && !out.timedOut && !out.stopped {
			timeout = true
		}
		switch {
		case stop && !out.stopped:
			out.stopped = true
			_ = closeStopInbox(cfg.AsyncDir)
			terminate(signalTerm)
			killAt = time.Now().Add(stopGrace)
		case timeout && !out.timedOut && !out.stopped:
			out.timedOut = true
			terminate(signalTerm)
			killAt = time.Now().Add(stopGrace)
		case interrupt && !out.interrupted && !out.stopped && !out.timedOut:
			out.interrupted = true
			terminate(signalTerm)
			killAt = time.Now().Add(stopGrace)
		}
		if !killAt.IsZero() && time.Now().After(killAt) {
			terminate(signalKill)
			killAt = time.Time{}
		}
		if (changed || now-lastWrite >= statusWriteEvery) && !end {
			writeProgressStatus(cfg, tracker, now)
			lastWrite = now
			if hooks.OnProgress != nil {
				hooks.OnProgress(cfg.AsyncDir)
			}
		}
	}
	tracker.poll(nowMs())
	out.exitCode, out.hasExit = exitCode, true
	if waitErr != nil {
		out.startFailure = waitErr.Error()
	}
	finalizeRun(cfg, tracker, out, hooks)
}

// writeProgressStatus republishes status.json with the live facts of the child.
func writeProgressStatus(cfg *RunConfig, t *progressTracker, now int64) {
	statusMu.Lock()
	defer statusMu.Unlock()
	st, err := readStatus(cfg.AsyncDir)
	if err != nil || st == nil || !isActiveAsyncState(st.State) {
		return
	}
	st.LastUpdate = msPtrOf(now)
	st.LastActivityAt = msPtrOf(now)
	if len(st.Steps) > 0 {
		s := &st.Steps[0]
		if s.Status == "pending" {
			s.Status = "running"
			s.StartedAt = msPtrOf(cfg.StartedAt)
		}
		s.LastActivityAt = msPtrOf(now)
		s.CurrentTool, s.CurrentToolArgs, s.CurrentPath = t.CurrentTool, t.CurrentArgs, t.CurrentPath
		s.CurrentToolStart = nil
		if t.ToolStarted != 0 {
			s.CurrentToolStart = msPtrOf(t.ToolStarted)
		}
		s.TurnCount, s.ToolCount = intPtr(t.TurnCount), intPtr(t.ToolCount)
		if t.Tokens.Total > 0 {
			tok := t.Tokens
			s.Tokens = &tok
		}
		if t.Model != "" && s.Model == "" {
			s.Model = t.Model
		}
	}
	st.CurrentTool, st.CurrentPath = t.CurrentTool, t.CurrentPath
	st.CurrentToolStartedAt = nil
	if t.ToolStarted != 0 {
		st.CurrentToolStartedAt = msPtrOf(t.ToolStarted)
	}
	st.TurnCount, st.ToolCount = intPtr(t.TurnCount), intPtr(t.ToolCount)
	if sf := latestSessionFile(cfg.SessionDir); sf != "" {
		st.SessionFile = sf
		if len(st.Steps) > 0 {
			st.Steps[0].SessionFile = sf
		}
	}
	if t.Tokens.Total > 0 {
		tok := t.Tokens
		st.TotalTokens = &tok
	}
	_ = writeStatus(cfg.AsyncDir, st)
}

// latestSessionFile is the newest .jsonl in the child's session directory.
func latestSessionFile(dir string) string {
	if dir == "" {
		return ""
	}
	var best string
	var bestTime time.Time
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && (best == "" || info.ModTime().After(bestTime)) {
			best, bestTime = p, info.ModTime()
		}
		return nil
	})
	return best
}

func tailOf(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return ""
	}
	n := min(st.Size(), max)
	buf := make([]byte, n)
	_, _ = f.ReadAt(buf, st.Size()-n)
	return strings.TrimSpace(strings.ToValidUTF8(string(buf), ""))
}

// resultShape is the state a finished child maps to.
type resultShape struct {
	state       string
	success     bool
	exitCode    int
	error       string
	output      string
	timedOut    bool
	stopped     bool
	interrupted bool
}

func classify(cfg *RunConfig, t *progressTracker, out superviseOutcome) resultShape {
	finalText := t.FinalText
	r := resultShape{output: finalText}
	switch {
	case out.stopped:
		return resultShape{state: "stopped", exitCode: 1, error: stopMessage, output: stopMessage, stopped: true}
	case out.timedOut:
		msg := "Subagent timed out."
		if cfg.TimeoutMs != nil {
			msg = fmt.Sprintf("Subagent timed out after %dms.", *cfg.TimeoutMs)
		}
		if finalText == "" {
			finalText = msg
		}
		return resultShape{state: "failed", exitCode: 1, error: msg, output: finalText, timedOut: true}
	case out.interrupted:
		return resultShape{state: "paused", exitCode: 0, output: finalText, interrupted: true}
	case out.startFailure != "":
		return resultShape{state: "failed", exitCode: 1, error: out.startFailure, output: out.startFailure}
	case out.exitCode == 0 && t.StopReason != "error" && t.StopReason != "aborted":
		r.state, r.success = "complete", true
		return r
	}
	detail := t.ErrorText
	if detail == "" {
		detail = tailOf(filepath.Join(cfg.AsyncDir, "runner.stderr.log"), stderrTail)
	}
	if detail == "" {
		detail = finalText
	}
	if out.exitCode > 128 && t.StopReason == "" { // killed by a signal nobody asked for
		sig := out.exitCode - 128
		return resultShape{state: "stopped", exitCode: out.exitCode, error: fmt.Sprintf("Subagent process terminated by signal %d.", sig), output: finalText, stopped: true}
	}
	if detail == "" {
		detail = fmt.Sprintf("%s exited with code %d", cfg.Agent, out.exitCode)
	}
	return resultShape{state: "failed", exitCode: max(out.exitCode, 1), error: detail, output: finalText}
}

// finalizeRun publishes the end of a run: the result file, the status, the events and the process proof.
func finalizeRun(cfg *RunConfig, t *progressTracker, out superviseOutcome, hooks runHooks) {
	statusMu.Lock()
	defer statusMu.Unlock()
	endedAt := nowMs()
	shape := classify(cfg, t, out)
	st, _ := readStatusQuiet(cfg.AsyncDir)
	if st == nil {
		st = &AsyncStatus{LifecycleArtifactVersion: lifecycleArtifactVersion, RunID: cfg.ID, SessionID: cfg.SessionID, CompletionOwnerID: cfg.CompletionOwnerID, Mode: "single", StartedAt: ms(cfg.StartedAt), Steps: []AsyncStep{{Agent: cfg.Agent}}}
	}
	if !isActiveAsyncState(st.State) {
		return // a reconciler or a stop already published the end
	}
	sessionFile := latestSessionFile(cfg.SessionDir)
	durationMs := endedAt - cfg.StartedAt
	summaryBody := shape.output
	if summaryBody == "" && shape.exitCode != 0 {
		summaryBody = shape.error
	}
	if summaryBody == "" {
		summaryBody = "(no output)"
	}
	summary := fmt.Sprintf("%s:\n%s", cfg.Agent, summaryBody)
	truncated := false
	hw := &headWriter{max: outputMaxBytes}
	_, _ = hw.Write([]byte(summary))
	if tt := hw.text(); tt != strings.TrimSpace(summary) {
		summary, truncated = tt, true
	}
	switch {
	case shape.stopped:
		summary = stopMessage
	case shape.timedOut:
		summary = shape.error
	case shape.interrupted:
		summary = "Paused after interrupt. Waiting for explicit next action."
	}
	child := map[string]any{"agent": cfg.Agent, "output": shape.output, "success": shape.success}
	if shape.output != "" {
		child["outputState"] = "present"
	} else {
		child["outputState"] = "absent"
	}
	if shape.error != "" {
		child["error"] = shape.error
	}
	if shape.interrupted {
		child["interrupted"] = true
	}
	if shape.timedOut {
		child["timedOut"] = true
	}
	if shape.stopped {
		child["stopped"] = true
	}
	if sessionFile != "" {
		child["sessionFile"] = sessionFile
	}
	model := firstNonEmpty(t.Model, cfg.Model)
	if model != "" {
		child["model"] = model
	}
	if cfg.Thinking != "" {
		child["thinking"] = cfg.Thinking
	}
	if cfg.Context != "" {
		child["context"] = cfg.Context
	}
	if t.Tokens.Total > 0 || t.Cost > 0 {
		child["usage"] = map[string]any{"input": t.Tokens.Input, "output": t.Tokens.Output, "cacheRead": 0, "cacheWrite": 0, "cost": t.Cost, "turns": t.TurnCount}
	}
	if cfg.LaunchContractDigest != "" {
		child["launchContractDigest"] = cfg.LaunchContractDigest
	}
	result := map[string]any{"lifecycleArtifactVersion": lifecycleArtifactVersion, "id": cfg.ID, "agent": cfg.Agent, "mode": "single", "success": shape.success, "state": shape.state, "summary": summary,
		"results": []any{child}, "exitCode": shape.exitCode, "timestamp": endedAt, "durationMs": durationMs, "truncated": truncated, "cwd": cfg.Cwd, "asyncDir": cfg.AsyncDir}
	if cfg.TimeoutMs != nil {
		result["timeoutMs"] = *cfg.TimeoutMs
	}
	if cfg.DeadlineAt != nil {
		result["deadlineAt"] = *cfg.DeadlineAt
	}
	if shape.stopped {
		result["stopped"], result["error"] = true, stopMessage
	} else if shape.timedOut {
		result["timedOut"], result["error"] = true, shape.error
	}
	if cfg.SessionID != "" {
		result["sessionId"] = cfg.SessionID
	}
	if cfg.CompletionOwnerID != "" {
		result["completionOwnerId"] = cfg.CompletionOwnerID
	}
	if cfg.ToolCallID != "" && cfg.ToolCallID != cfg.ID {
		result["toolCallId"] = cfg.ToolCallID
	}
	if sessionFile != "" {
		result["sessionFile"] = sessionFile
	}
	if cfg.LaunchContractDigest != "" {
		result["launchContractDigest"] = cfg.LaunchContractDigest
	}
	if t.Tokens.Total > 0 {
		result["totalTokens"] = t.Tokens
	}
	// status
	st.State = shape.state
	st.EndedAt, st.LastUpdate = msPtrOf(endedAt), msPtrOf(endedAt)
	st.ActivityState = ""
	st.CurrentTool, st.CurrentPath, st.CurrentToolStartedAt = "", "", nil
	if shape.error != "" && (shape.state == "failed" || shape.state == "stopped" || shape.state == "partial") {
		st.Error = shape.error
	}
	if shape.stopped {
		st.Stopped = new(true)
	}
	if shape.timedOut {
		st.TimedOut = new(true)
	}
	if sessionFile != "" {
		st.SessionFile = sessionFile
	}
	if t.Tokens.Total > 0 {
		tok := t.Tokens
		st.TotalTokens = &tok
	}
	if len(st.Steps) > 0 {
		s := &st.Steps[0]
		s.Status = stepStatusOf(shape.state)
		s.EndedAt, s.DurationMs = msPtrOf(endedAt), msPtrOf(durationMs)
		s.ExitCode = intPtr(shape.exitCode)
		s.ActivityState, s.CurrentTool, s.CurrentToolArgs, s.CurrentPath, s.CurrentToolStart = "", "", "", "", nil
		s.TurnCount, s.ToolCount = intPtr(t.TurnCount), intPtr(t.ToolCount)
		if shape.error != "" {
			s.Error = shape.error
		}
		if shape.timedOut {
			s.TimedOut = new(true)
		}
		if shape.stopped {
			s.Stopped = new(true)
		}
		if sessionFile != "" {
			s.SessionFile = sessionFile
		}
		if t.Tokens.Total > 0 {
			tok := t.Tokens
			s.Tokens = &tok
		}
		if model != "" {
			s.Model = model
		}
		if cfg.Thinking != "" && s.Thinking == "" {
			s.Thinking = cfg.Thinking
		}
	}
	_ = closeStopInbox(cfg.AsyncDir)
	// the runner's end, observed: the process proof, then the result, then the status
	code := out.exitCode
	rc := RunnerClose{ProcessInstanceID: cfg.RunnerProcessInstanceID, CloseObservedAt: endedAt, ExitCode: &code}
	writeWriterRecord(cfg, rc, st)
	if cfg.SessionID != "" {
		if _, err := writeAsyncResultFile(filepath.Join(cfg.ResultsDir, resultFileName(cfg.ID)), result); err != nil {
			st.State, st.Error = "failed", fmt.Sprintf("Failed to write result file %s: %s", filepath.Join(cfg.ResultsDir, resultFileName(cfg.ID)), err.Error())
		}
	}
	_ = writeStatus(cfg.AsyncDir, st)
	finalizeProcessTerminal(cfg.AsyncDir, cfg.ID, rc)
	_ = updateActiveRunIndex(cfg.AsyncDir, st.State, st.ToolCallID, indexOptions{terminalIndexBeforeRelease: true})
	appendJSONLine(filepath.Join(cfg.AsyncDir, "events.jsonl"), map[string]any{"type": stepEventType(shape), "ts": endedAt, "runId": cfg.ID, "stepIndex": 0, "agent": cfg.Agent, "exitCode": shape.exitCode, "durationMs": durationMs})
	appendJSONLine(filepath.Join(cfg.AsyncDir, "events.jsonl"), map[string]any{"type": "subagent.run.completed", "lifecycleArtifactVersion": lifecycleArtifactVersion, "ts": endedAt, "runId": cfg.ID, "status": st.State, "durationMs": durationMs})
	_ = os.WriteFile(filepath.Join(cfg.AsyncDir, fmt.Sprintf("subagent-log-%s.md", cfg.ID)), []byte(runLog(cfg, st, summary, endedAt)), 0o644)
	if hooks.OnComplete != nil {
		fresh, _ := readStatusQuiet(cfg.AsyncDir)
		hooks.OnComplete(cfg, result, fresh)
	}
}

func stepEventType(s resultShape) string {
	switch {
	case s.stopped:
		return "subagent.step.stopped"
	case s.timedOut:
		return "subagent.step.failed"
	case s.interrupted:
		return "subagent.step.paused"
	case s.exitCode == 0:
		return "subagent.step.completed"
	}
	return "subagent.step.failed"
}

func runLog(cfg *RunConfig, st *AsyncStatus, summary string, endedAt int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Subagent run %s\n\n", cfg.ID)
	fmt.Fprintf(&b, "- Mode: single\n- Cwd: %s\n- Started: %s\n- Ended: %s\n\n## Steps\n\n", cfg.Cwd, time.UnixMilli(cfg.StartedAt).UTC().Format(time.RFC3339), time.UnixMilli(endedAt).UTC().Format(time.RFC3339))
	for i, s := range st.Steps {
		d := ""
		if s.DurationMs != nil {
			d = " (" + bgFormatDuration(int64(*s.DurationMs)) + ")"
		}
		fmt.Fprintf(&b, "%d. %s: %s%s\n", i+1, s.Agent, s.Status, d)
	}
	fmt.Fprintf(&b, "\n## Summary\n\n%s\n", summary)
	return b.String()
}

// writeWriterRecord records the child (the writer of the original's proof) with its process tree verified gone.
func writeWriterRecord(cfg *RunConfig, rc RunnerClose, st *AsyncStatus) {
	cand, _ := readProcessTerminalCandidate(cfg.AsyncDir)
	if cand == nil {
		return
	}
	pid := 0
	if st.PID != nil {
		pid = *st.PID
	}
	tree := &ProcessTreeProof{State: "unknown", Reason: "unsupported-platform"}
	if backgroundSupported && pid > 0 {
		tree = verifyGroupGone(pid)
	}
	attempt := 0
	w := ProcessInstanceExit{Kind: "pi-writer", ProcessInstanceID: randomUUID(), CloseObservedAt: ms(rc.CloseObservedAt), ExitCode: rc.ExitCode, Attempt: &attempt, ProcessTree: tree}
	cand.Writers = map[string][]ProcessInstanceExit{"0": {w}}
	cand.ExpectedWriters = map[string]int{"0": 1}
	_ = writeProcessTerminalCandidate(cfg.AsyncDir, cand)
}

// verifyGroupGone waits briefly for the child's process group to empty (a straggler is killed once) and reports the proof.
func verifyGroupGone(pgid int) *ProcessTreeProof {
	deadline := time.Now().Add(1500 * time.Millisecond)
	for groupHasMembers(pgid) {
		if time.Now().After(deadline) {
			_ = signalGroup(pgid, signalKill)
			time.Sleep(200 * time.Millisecond)
			if groupHasMembers(pgid) {
				return &ProcessTreeProof{State: "unknown", Reason: "verification-failed", Diagnostic: fmt.Sprintf("Process group %d still has active members.", pgid)}
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return &ProcessTreeProof{State: "observed", Mechanism: "posix-process-group", ProcessGroupID: pgid, VerifiedAt: ms(nowMs())}
}

// adoptRun supervises a run an earlier session started: its child may still be running, or may have ended while no one was
// watching (its exit file and output are then all the run left).
func adoptRun(cfg *RunConfig, pid int, hooks runHooks) *RunHandle {
	h := &RunHandle{Config: cfg, adoptedPID: pid, cancel: make(chan struct{}), finished: make(chan struct{})}
	go superviseRun(h, hooks)
	return h
}
