package pi_subagents

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Background (async) runs: the original runs a child through its own executor
// and a detached runner process (src/runs/background/*); the port starts the
// child `pig --print` process in a goroutine of this extension process and
// tracks it in a session-scoped registry, persisting the original's artifacts
// (async dir with status.json / events.jsonl / output logs, result file under
// async-subagent-results) so status, bg_wait, stop and the completion
// notification behave as the original's do.

const (
	// maxWidgetJobs is the original's MAX_WIDGET_JOBS.
	maxWidgetJobs = 4
	// stoppedMessage is the original's STOP_MESSAGE.
	stoppedMessage = "Subagent stopped by user."
)

// nowMs is the clock; tests replace it.
var nowMs = func() int64 { return time.Now().UnixMilli() }

// tempScopeID is the original's resolveTempScopeId: the user name (getuid is
// Unix-only; the original falls to the same name without it).
func tempScopeID(getenv func(string) string) string {
	for _, key := range []string{"USERNAME", "USER", "LOGNAME"} {
		if v := getenv(key); v != "" {
			return "user-" + sanitizeTempScopeSegment(v)
		}
	}
	return "user-" + sanitizeTempScopeSegment(currentUsername())
}

func sanitizeTempScopeSegment(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "unknown"
	}
	return s
}

func currentUsername() string {
	if u, err := os.UserHomeDir(); err == nil && u != "" {
		return filepath.Base(u)
	}
	return "unknown"
}

// tempRoot is the original's TEMP_ROOT_DIR (PI_SUBAGENTS_TEMP_ROOT or the system temp).
func tempRoot() string {
	if r := strings.TrimSpace(os.Getenv("PI_SUBAGENTS_TEMP_ROOT")); r != "" {
		return r
	}
	return filepath.Join(os.TempDir(), "pi-subagents-"+tempScopeID(os.Getenv))
}

// resultsDir is the original's DIRS.results.
func resultsDir() string { return filepath.Join(tempRoot(), "async-subagent-results") }

// asyncRootDir is the original's DIRS.async.
func asyncRootDir() string { return filepath.Join(tempRoot(), "async-subagent-runs") }

// newRunID is a UUID v4, the original's run id (crypto randomUUID).
func newRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A run id must exist even when the system entropy fails.
		fallback := fmt.Sprintf("%d", nowMs())
		return strings.Repeat("0", 32-len(fallback)) + fallback
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 32)
	hex.Encode(dst, b[:])
	return string(dst[0:8]) + "-" + string(dst[8:12]) + "-" + string(dst[12:16]) + "-" + string(dst[16:20]) + "-" + string(dst[20:32])
}

// asyncStep is one child of a run (the original's AsyncStatus.steps entry, the
// subset this port produces).
type asyncStep struct {
	Agent        string `json:"agent"`
	Status       string `json:"status"` // pending, running, complete, failed, stopped
	StartedAt    int64  `json:"startedAt,omitempty"`
	EndedAt      int64  `json:"endedAt,omitempty"`
	DurationMs   int64  `json:"durationMs,omitempty"`
	Error        string `json:"error,omitempty"`
	TimedOut     bool   `json:"timedOut,omitempty"`
	Stopped      bool   `json:"stopped,omitempty"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	WorktreePath string `json:"worktreePath,omitempty"`
	Branch       string `json:"branch,omitempty"`
}

// asyncStatusFile is the persisted AsyncStatus subset (status.json).
type asyncStatusFile struct {
	RunID          string       `json:"runId"`
	SessionID      string       `json:"sessionId,omitempty"`
	Mode           string       `json:"mode"`
	State          string       `json:"state"`
	Error          string       `json:"error,omitempty"`
	StartedAt      int64        `json:"startedAt"`
	EndedAt        int64        `json:"endedAt,omitempty"`
	LastUpdate     int64        `json:"lastUpdate,omitempty"`
	TimeoutMs      int64        `json:"timeoutMs,omitempty"`
	DeadlineAt     int64        `json:"deadlineAt,omitempty"`
	TimedOut       bool         `json:"timedOut,omitempty"`
	Stopped        bool         `json:"stopped,omitempty"`
	PID            int          `json:"pid,omitempty"`
	Cwd            string       `json:"cwd,omitempty"`
	CurrentStep    *int         `json:"currentStep,omitempty"`
	ChainStepCount int          `json:"chainStepCount,omitempty"`
	OutputFile     string       `json:"outputFile,omitempty"`
	Steps          []*asyncStep `json:"steps,omitempty"`
}

// asyncResultFile is the persisted result (async-subagent-results/<id>.json).
type asyncResultFile struct {
	ID         string       `json:"id"`
	RunID      string       `json:"runId"`
	Agent      string       `json:"agent"`
	Success    bool         `json:"success"`
	Summary    string       `json:"summary"`
	Output     string       `json:"output"`
	ExitCode   int          `json:"exitCode"`
	State      string       `json:"state"`
	Stopped    bool         `json:"stopped,omitempty"`
	TimedOut   bool         `json:"timedOut,omitempty"`
	SessionID  string       `json:"sessionId,omitempty"`
	Cwd        string       `json:"cwd,omitempty"`
	Mode       string       `json:"mode"`
	Timestamp  int64        `json:"timestamp"`
	DurationMs int64        `json:"durationMs"`
	Results    []*asyncStep `json:"results,omitempty"`
}

// asyncRun is one live or remembered run of this session.
type asyncRun struct {
	mu            sync.Mutex
	ID            string
	SessionID     string
	Mode          string // "single"
	State         string // queued, running, complete, failed, stopped
	Agent         string
	Task          string
	Cwd           string
	Model         string
	Dir           string
	OutputFile    string
	StartedAt     int64
	LastUpdate    int64
	EndedAt       int64
	TimeoutMs     int64
	DeadlineAt    int64
	TimedOut      bool
	Stopped       bool
	Error         string
	PID           int
	Steps         []*asyncStep
	Output        string // the child's answer
	ExitCode      int
	Success       bool
	OutputBinding outputBinding

	cancel    func()
	doneCh    chan struct{}
	retention time.Duration
}

// statusSnapshot renders the persisted status subset of the run.
func (r *asyncRun) statusSnapshot() *asyncStatusFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := &asyncStatusFile{
		RunID: r.ID, SessionID: r.SessionID, Mode: r.Mode, State: r.State,
		Error: r.Error, StartedAt: r.StartedAt, EndedAt: r.EndedAt, LastUpdate: r.LastUpdate,
		TimeoutMs: r.TimeoutMs, DeadlineAt: r.DeadlineAt, TimedOut: r.TimedOut, Stopped: r.Stopped,
		PID: r.PID, Cwd: r.Cwd, ChainStepCount: len(r.Steps), OutputFile: r.OutputFile,
	}
	for _, s := range r.Steps {
		cp := *s
		st.Steps = append(st.Steps, &cp)
	}
	return st
}

func (r *asyncRun) touch() { r.LastUpdate = nowMs() }

// registry keeps this session's runs; runs of dead sessions stay on disk.
type registry struct {
	mu    sync.Mutex
	runs  map[string]*asyncRun
	order []string
}

var runs = &registry{runs: map[string]*asyncRun{}}

func (rg *registry) add(r *asyncRun) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if _, ok := rg.runs[r.ID]; !ok {
		rg.order = append(rg.order, r.ID)
	}
	rg.runs[r.ID] = r
}

func (rg *registry) get(id string) *asyncRun {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return rg.runs[id]
}

// activeForSession lists this session's queued/running runs, newest lastUpdate first.
func (rg *registry) activeForSession(sessionID string) []*asyncRun {
	rg.mu.Lock()
	ids := append([]string(nil), rg.order...)
	out := []*asyncRun{}
	for _, id := range ids {
		r := rg.runs[id]
		if r == nil {
			continue
		}
		r.mu.Lock()
		active := r.State == "queued" || r.State == "running"
		same := sessionID == "" || r.SessionID == sessionID
		r.mu.Unlock()
		if active && same {
			out = append(out, r)
		}
	}
	rg.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastUpdate < out[j].LastUpdate })
	return out
}

// resolveRun finds a run by exact id or unique prefix in this process; "" when absent.
func (rg *registry) resolveRun(id string) *asyncRun {
	if r := rg.get(id); r != nil {
		return r
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	var found *asyncRun
	for _, rid := range rg.order {
		if strings.HasPrefix(rid, id) {
			if found != nil {
				return found // ambiguous prefixes are reported by the caller through listMatching
			}
			found = rg.runs[rid]
		}
	}
	return found
}

// listMatching returns every live run whose id equals or starts with prefix.
func (rg *registry) listMatching(prefix string) []*asyncRun {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	var out []*asyncRun
	for _, rid := range rg.order {
		if rid == prefix || strings.HasPrefix(rid, prefix) {
			out = append(out, rg.runs[rid])
		}
	}
	return out
}

// asyncRunRequest describes one detached run to start.
type asyncRunRequest struct {
	SessionID string
	Cwd       string
	Agent     *AgentConfig
	Task      string
	Model     string
	TimeoutMs int64
	Output    outputBinding
	Worktree  *worktreeAllocation
}

// startAsyncRun starts one agent in the background and returns its run. The
// capacity check and the artifacts mirror the original's async launch.
func startAsyncRun(ctx sdk.Context, req asyncRunRequest) (*asyncRun, error) {
	sessionID := req.SessionID
	return startDetachedRun(req, sessionID,
		func(r *asyncRun) { deliverCompletion(ctx, r) },
		func() { updateAsyncWidget(ctx) })
}

// startDetachedRun is startAsyncRun with the host-dependent completion and
// widget delivery injected; tests drive it directly.
func startDetachedRun(req asyncRunRequest, sessionID string, notify func(*asyncRun), widget func()) (*asyncRun, error) {
	req.SessionID = sessionID
	cfg := loadSubagentConfig()
	limit := resolveMaxActiveAsyncRunsPerSession(cfg.MaxActiveAsyncRunsPerSess)
	if limit > 0 {
		active := runs.activeForSession(req.SessionID)
		if len(active) >= limit {
			return nil, fmt.Errorf("Active async run capacity exhausted: %d/%d used.", len(active), limit)
		}
	}
	id := newRunID()
	dir := filepath.Join(asyncRootDir(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("Failed to create async run directory '%s': %s", dir, err.Error())
	}
	r := &asyncRun{
		ID: id, SessionID: req.SessionID, Mode: "single", State: "queued",
		Agent: req.Agent.Name, Task: req.Task, Cwd: req.Cwd, Model: req.Model,
		Dir: dir, OutputFile: "output-0.log", StartedAt: nowMs(), LastUpdate: nowMs(),
		TimeoutMs: req.TimeoutMs, doneCh: make(chan struct{}),
		Steps:         []*asyncStep{{Agent: req.Agent.Name, Status: "pending"}},
		OutputBinding: req.Output,
		// The runner here is this extension process; the original's status.pid is its runner too.
		PID: os.Getpid(),
	}
	if req.Output.Path != "" {
		r.OutputFile = req.Output.Path
	}
	if req.Worktree != nil {
		r.Steps[0].WorktreePath = req.Worktree.Path
		r.Steps[0].Branch = req.Worktree.Branch
	}
	if req.TimeoutMs > 0 {
		r.DeadlineAt = r.StartedAt + req.TimeoutMs
	}
	runs.add(r)
	r.writeStatus()
	r.appendEvent("queued", "")

	cancelCh := make(chan struct{})
	var once sync.Once
	r.cancel = func() { once.Do(func() { close(cancelCh) }) }

	go r.execute(req, notify, widget, cancelCh)

	// The deadline kill: a run that outlives timeoutMs is stopped as timed out.
	if req.TimeoutMs > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(req.TimeoutMs) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				r.mu.Lock()
				terminal := r.State != "queued" && r.State != "running"
				r.mu.Unlock()
				if terminal {
					return
				}
				r.mu.Lock()
				r.TimedOut = true
				r.mu.Unlock()
				r.cancel()
			case <-r.doneCh:
			}
		}()
	}
	return r, nil
}

// execute runs the child and records the outcome; it is the run's goroutine.
func (r *asyncRun) execute(req asyncRunRequest, notify func(*asyncRun), widget func(), cancelCh <-chan struct{}) {
	taskForChild := req.Task
	if r.OutputBinding.Path != "" {
		taskForChild = injectSingleOutputInstruction(taskForChild, r.OutputBinding.Path)
	}
	r.mu.Lock()
	r.State = "running"
	r.Steps[0].Status = "running"
	r.Steps[0].StartedAt = nowMs()
	r.touch()
	r.mu.Unlock()
	r.writeStatus()
	r.appendEvent("step_start", "0")

	out, err := runAgentChannelled(cancelCh, req.Cwd, req.Agent, taskForChild, req.Model, supervisorDir(r.Dir))
	if err == nil && r.OutputBinding.Path != "" {
		if ref, werr := savedOutputReference(r.OutputBinding.Path, out); werr == nil {
			out = ref
		}
	} else if r.OutputBinding.Suppress {
		out = "(no output)"
	}

	now := nowMs()
	r.mu.Lock()
	r.EndedAt = now
	r.LastUpdate = now
	r.Steps[0].EndedAt = now
	r.Steps[0].DurationMs = now - r.Steps[0].StartedAt
	r.Output = out
	switch {
	case err == nil:
		r.State, r.Steps[0].Status, r.Success = "complete", "complete", true
		r.ExitCode = 0
	case isCancelled(err):
		r.State, r.Steps[0].Status, r.Stopped = "stopped", "stopped", true
		r.Steps[0].Error = stoppedMessage
		r.Error = stoppedMessage
		r.ExitCode = 1
		r.Steps[0].ExitCode = intPtr(1)
	default:
		r.State, r.Steps[0].Status = "failed", "failed"
		r.Error = err.Error()
		r.ExitCode = 1
		if ee, ok := err.(*childExit); ok {
			r.ExitCode = ee.code
			code := ee.code
			r.Steps[0].ExitCode = &code
			r.Steps[0].Error = ee.detail
		}
	}
	if r.TimedOut {
		r.State = "failed"
		r.Steps[0].TimedOut = true
	}
	state := r.State
	r.mu.Unlock()

	// The child's answer is the step's output artifact (a bound output path
	// already carries it).
	if out != "" && r.OutputBinding.Path == "" {
		_ = os.WriteFile(r.outputArtifactPath(), []byte(out), 0o644)
	}
	r.appendEvent("step_end", state)
	r.writeStatus()
	r.writeResult()
	r.appendEvent(state, "")
	if notify != nil {
		notify(r)
	}
	if widget != nil {
		widget()
	}
	closeDone(r)
}

func closeDone(r *asyncRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.doneCh:
	default:
		close(r.doneCh)
	}
}

// outputArtifactPath is the file the child's answer lands in: the bound
// output path when the call set one, else the run's own log.
func (r *asyncRun) outputArtifactPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if filepath.IsAbs(r.OutputFile) {
		return r.OutputFile
	}
	return filepath.Join(r.Dir, r.OutputFile)
}

func intPtr(n int) *int { return &n }

// stop asks a running run to stop, as the original's stop action does.
func (r *asyncRun) stop() {
	r.mu.Lock()
	r.State = "stopped"
	r.Stopped = true
	r.Error = stoppedMessage
	r.touch()
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	r.writeStatus()
	r.appendEvent("stop_requested", "")
}

// requestStop flags the run stopped without waiting; the goroutine records the
// terminal state when the child exits.
func (r *asyncRun) requestStop() {
	r.mu.Lock()
	active := r.State == "queued" || r.State == "running"
	r.Stopped = true
	r.mu.Unlock()
	if active && r.cancel != nil {
		r.cancel()
	}
}

// isCancelled reports whether err is the run-cancelled error.
func isCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "was cancelled")
}

// appendEvent writes one events.jsonl line, as the original's run directories carry.
func (r *asyncRun) appendEvent(kind, detail string) {
	line, _ := json.Marshal(map[string]any{"ts": nowMs(), "event": kind, "detail": detail})
	f, err := os.OpenFile(filepath.Join(r.Dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// writeStatus persists status.json atomically.
func (r *asyncRun) writeStatus() {
	data, err := json.MarshalIndent(r.statusSnapshot(), "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(r.Dir, "status.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// resultPath is the original's resultFilePath (async-subagent-results/<id>.json).
func resultPath(id string) string { return filepath.Join(resultsDir(), id+".json") }

// writeResult persists the result file bg_wait and status read.
func (r *asyncRun) writeResult() {
	r.mu.Lock()
	res := &asyncResultFile{
		ID: r.ID, RunID: r.ID, Agent: r.Agent, Success: r.Success,
		Summary: summaryFor(r), Output: r.Output, ExitCode: r.ExitCode, State: r.State,
		Stopped: r.Stopped, TimedOut: r.TimedOut, SessionID: r.SessionID, Cwd: r.Cwd,
		Mode: r.Mode, Timestamp: nowMs(), DurationMs: r.EndedAt - r.StartedAt,
	}
	for _, s := range r.Steps {
		cp := *s
		res.Results = append(res.Results, &cp)
	}
	r.mu.Unlock()
	if err := os.MkdirAll(resultsDir(), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(resultPath(r.ID), data, 0o644)
}

// summaryFor is the result summary: the child's answer, or the error. A
// stopped run carries the original's stop message, as its sealed result does.
func summaryFor(r *asyncRun) string {
	if r.Stopped || r.State == "stopped" {
		return stoppedMessage
	}
	if r.Output != "" {
		return r.Agent + ":\n" + r.Output
	}
	if r.Error != "" {
		return r.Error
	}
	return "(no output)"
}

// readStatusFile loads a run directory's status.json.
func readStatusFile(dir string) *asyncStatusFile {
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return nil
	}
	var st asyncStatusFile
	if err := json.Unmarshal(data, &st); err != nil {
		return nil
	}
	return &st
}

// reconcileDiskRun repairs a status that says running while no live run backs it:
// the extension process (the run's runner here) is gone. The original reconciles
// the same way through its runner-process proof.
func reconcileDiskRun(dir string) *asyncStatusFile {
	st := readStatusFile(dir)
	if st == nil {
		return nil
	}
	if st.State != "queued" && st.State != "running" {
		return st
	}
	if r := runs.get(st.RunID); r != nil {
		return r.statusSnapshot()
	}
	now := nowMs()
	pid := st.PID
	message := fmt.Sprintf("Async runner process %d exited or disappeared before writing a result. Marked run failed by stale-run reconciliation.", pid)
	st.State = "failed"
	st.Error = message
	st.EndedAt = now
	st.LastUpdate = now
	for _, s := range st.Steps {
		if s.Status == "running" || s.Status == "pending" {
			s.Status = "failed"
			s.Error = message
			s.EndedAt = now
		}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(dir, "status.json"), data, 0o644)
	}
	// The repaired run also gets the result file the original's repair writes.
	res := &asyncResultFile{
		ID: st.RunID, RunID: st.RunID, Agent: agentOf(st), Success: false,
		Summary: message, Output: message, ExitCode: 1, State: "failed",
		SessionID: st.SessionID, Cwd: st.Cwd, Mode: st.Mode, Timestamp: now, Results: st.Steps,
	}
	if data, err := json.MarshalIndent(res, "", " "); err == nil {
		_ = os.MkdirAll(resultsDir(), 0o755)
		_ = os.WriteFile(resultPath(st.RunID), data, 0o644)
	}
	return st
}

func agentOf(st *asyncStatusFile) string {
	if st == nil {
		return "unknown"
	}
	if len(st.Steps) > 0 {
		return st.Steps[0].Agent
	}
	return "unknown"
}

// sessionIDOf asks the host for the session identity; "" when unavailable.
func sessionIDOf(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return id
}

// formatAsyncStartedMessage is the original's: the headline plus the guidance
// that tells the model how a detached run is delivered.
func formatAsyncStartedMessage(headline string, interactive bool) string {
	var guidance []string
	if interactive {
		guidance = []string{
			"The async run is detached and running in the background.",
			"You are in an interactive session. Return control to the user now; Pi will wake you through the native completion notification when this subagent completes or needs attention. Do not run sleep/polling loops to wait for this async subagent; it does not need a wait call.",
			"Use bg_wait only for provider, detached, or other background work that lacks a native completion notification.",
			"If the current turn must receive results from work without a native notification before it ends, call blocking bg_wait(); ordinary async subagent runs do not need a wait call because their completion is delivered natively.",
			"Otherwise, continue any independent work or return control to the user. Use subagent({ action: \"status\", id: \"...\" }) for a one-shot status/result or to inspect a blocked/stale run, never as a wait loop.",
		}
	} else {
		guidance = []string{
			"The async run is detached. Do not run sleep timers or polling loops just to wait for it.",
			"This is a non-interactive run: Pi auto-drains current-session subagent work at agent_end so detached children are not abandoned. Use bg_wait only when this turn must receive provider, detached, or other background-work results that have no native completion notification.",
			"Use subagent({ action: \"status\", id: \"...\" }) when you need a one-shot status/result or to inspect a blocked/stale run; do not poll in a loop.",
		}
	}
	return strings.Join(append([]string{headline, ""}, guidance...), "\n")
}

// updateAsyncWidget shows the session's active async runs, as the original's
// subagent-async widget does (a static line list is the Go port's form of the
// original's animated component).
func updateAsyncWidget(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	active := runs.activeForSession(sessionIDOf(ctx))
	if len(active) == 0 {
		_ = ctx.SetWidget(asyncWidget, nil)
		return
	}
	lines := []string{}
	for i, r := range active {
		if i >= maxWidgetJobs {
			lines = append(lines, fmt.Sprintf("+%d more", len(active)-maxWidgetJobs))
			break
		}
		r.mu.Lock()
		lines = append(lines, fmt.Sprintf("%s · %s · %s", r.Agent, r.State, formatDuration(maxInt64(0, nowMs()-r.StartedAt))))
		r.mu.Unlock()
	}
	_ = ctx.SetWidget(asyncWidget, lines)
}
