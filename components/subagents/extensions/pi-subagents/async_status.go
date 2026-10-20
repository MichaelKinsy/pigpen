package pi_subagents

// The run directory's bookkeeping: the active and terminal run indexes, the process-terminal proof, the reconciliation of a
// stale run, the run summaries and their text. Ports active-run-index.ts, terminal-run-index.ts, process-terminal.ts,
// process-terminal-candidate.ts, stale-run-reconciler.ts and async-status.ts for the runs this port starts (single children;
// workflow, nested and parallel-group fields are not modelled).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const (
	activeRunIndexDir                = ".active-runs"
	terminalRunIndexDir              = ".terminal-runs"
	defaultStaleTerminalActiveMarker = 24 * 60 * 60 * 1000
	toolCallIndexDirName             = "tool-calls"
	terminalIndexVersion             = 1
	terminalTimestampWidth           = 16
)

// ── active-run-index.ts ──

func activeIndexDir(asyncDirRoot string) string {
	return filepath.Join(asyncDirRoot, activeRunIndexDir)
}

func activeToolCallIndexDir(asyncDirRoot, toolCallID string) string {
	return filepath.Join(activeIndexDir(asyncDirRoot), toolCallIndexDirName, encodeIndexSegment(toolCallID))
}

func activeMarkerPath(asyncDir string) string {
	return filepath.Join(activeIndexDir(filepath.Dir(asyncDir)), filepath.Base(asyncDir))
}

func removeEmptyAncestors(start, stop string) {
	cur := start
	for cur != stop && strings.HasPrefix(cur, stop+string(filepath.Separator)) {
		if os.Remove(cur) != nil {
			return
		}
		cur = filepath.Dir(cur)
	}
}

func releaseToolCallAliases(asyncDir string) {
	root := filepath.Join(activeIndexDir(filepath.Dir(asyncDir)), toolCallIndexDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		marker := filepath.Join(root, e.Name(), filepath.Base(asyncDir))
		_ = os.Remove(marker)
		removeEmptyAncestors(filepath.Dir(marker), root)
	}
}

// releaseActiveRunIndex removes the active marker of a run and its tool-call aliases.
func releaseActiveRunIndex(asyncDir string) error {
	if err := os.Remove(activeMarkerPath(asyncDir)); err != nil && !isNotExist(err) {
		return err
	}
	releaseToolCallAliases(asyncDir)
	return nil
}

type indexOptions struct {
	retryCapacityErrors        bool
	terminalIndexBeforeRelease bool
}

// updateActiveRunIndex marks an active run, or releases a finished one and records it in the terminal index.
func updateActiveRunIndex(asyncDir, state, toolCallID string, opts indexOptions) error {
	marker := activeMarkerPath(asyncDir)
	touch := func(p string) error {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if isActiveAsyncState(state) {
		if err := touch(marker); err != nil {
			return err
		}
		if toolCallID != "" {
			// tool-call aliases are optional: the authoritative marker keeps the launch discoverable without them
			_ = touch(filepath.Join(activeToolCallIndexDir(filepath.Dir(asyncDir), toolCallID), filepath.Base(asyncDir)))
		}
		return nil
	}
	if opts.terminalIndexBeforeRelease {
		if err := touch(marker); err != nil {
			return err
		}
		if st, _ := readStatus(asyncDir); st != nil && st.State == state {
			if err := updateTerminalRunIndex(asyncDir, st); err != nil {
				if opts.retryCapacityErrors {
					return err
				}
				return nil
			}
		}
		return releaseActiveRunIndex(asyncDir)
	}
	if err := releaseActiveRunIndex(asyncDir); err != nil {
		return err
	}
	if st, _ := readStatus(asyncDir); st != nil && st.State == state {
		if err := updateTerminalRunIndex(asyncDir, st); err != nil && opts.retryCapacityErrors && isStorageCapacityError(err) {
			return err
		}
	}
	return nil
}

func isStorageCapacityError(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

// activeRunMarkerAgeMs is how long ago the active marker was written; -1 when there is none.
func activeRunMarkerAgeMs(asyncDir string, now int64) int64 {
	st, err := os.Stat(activeMarkerPath(asyncDir))
	if err != nil {
		return -1
	}
	if now == 0 {
		now = nowMs()
	}
	return max(0, now-st.ModTime().UnixMilli())
}

// readActiveRunIndex lists the runs marked active; nil when the index does not exist.
func readActiveRunIndex(asyncDirRoot string) ([]string, bool) {
	entries, err := os.ReadDir(activeIndexDir(asyncDirRoot))
	if err != nil {
		return nil, false
	}
	out := []string{}
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, true
}

func readActiveRunToolCallIndex(asyncDirRoot, toolCallID string) []string {
	entries, err := os.ReadDir(activeToolCallIndexDir(asyncDirRoot, toolCallID))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out
}

// ── terminal-run-index.ts ──

type terminalIndexEntry struct {
	Version   int    `json:"version"`
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	EndedAt   ms     `json:"endedAt"`
}

func terminalMarkerName(endedAt int64, runID string) string {
	return fmt.Sprintf("%0*d-%s.json", terminalTimestampWidth, max(0, endedAt), encodeIndexSegment(runID))
}

func terminalSessionIndexDir(asyncDirRoot, sessionID string) string {
	return filepath.Join(asyncDirRoot, terminalRunIndexDir, encodeIndexSegment(sessionID))
}

// updateTerminalRunIndex records a finished, session-owned run so it can be listed without scanning the root.
func updateTerminalRunIndex(asyncDir string, st *AsyncStatus) error {
	if !isTerminalAsyncState(st.State) || st.SessionID == "" {
		return nil
	}
	endedAt := int64(st.StartedAt)
	if st.LastUpdate != nil {
		endedAt = int64(*st.LastUpdate)
	}
	if st.EndedAt != nil {
		endedAt = int64(*st.EndedAt)
	}
	runID := st.RunID
	if runID == "" {
		runID = filepath.Base(asyncDir)
	}
	marker := filepath.Join(terminalSessionIndexDir(filepath.Dir(asyncDir), st.SessionID), terminalMarkerName(endedAt, filepath.Base(asyncDir)))
	return asyncWriteJSON(marker, terminalIndexEntry{Version: terminalIndexVersion, RunID: runID, SessionID: st.SessionID, EndedAt: ms(endedAt)})
}

// readRecentTerminalRunIndex lists the newest finished runs (of one session, or all); stale markers are removed.
func readRecentTerminalRunIndex(asyncDirRoot, sessionID string, limit int) []string {
	var dirs []string
	if sessionID != "" {
		dirs = []string{terminalSessionIndexDir(asyncDirRoot, sessionID)}
	} else if entries, err := os.ReadDir(filepath.Join(asyncDirRoot, terminalRunIndexDir)); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(asyncDirRoot, terminalRunIndexDir, e.Name()))
			}
		}
	}
	type marker struct{ dir, name string }
	var markers []marker
	for _, d := range dirs {
		if entries, err := os.ReadDir(d); err == nil {
			for _, e := range entries {
				if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
					markers = append(markers, marker{d, e.Name()})
				}
			}
		}
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].name > markers[j].name })
	if limit >= 0 && len(markers) > limit {
		markers = markers[:limit]
	}
	var runIDs []string
	seen := map[string]bool{}
	for _, m := range markers {
		p := filepath.Join(m.dir, m.name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var e terminalIndexEntry
		var raw map[string]any
		ok := json.Unmarshal(data, &raw) == nil && json.Unmarshal(data, &e) == nil && e.Version == terminalIndexVersion && e.RunID != "" && e.SessionID != "" && raw["endedAt"] != nil
		if !ok || (sessionID != "" && e.SessionID != sessionID) {
			_ = os.Remove(p)
			continue
		}
		st, _ := readStatusQuiet(filepath.Join(asyncDirRoot, e.RunID))
		if st == nil || !isTerminalAsyncState(st.State) || st.SessionID != e.SessionID || (st.RunID != "" && st.RunID != e.RunID) || seen[e.RunID] {
			_ = os.Remove(p)
			continue
		}
		seen[e.RunID] = true
		runIDs = append(runIDs, e.RunID)
	}
	return runIDs
}

func readStatusQuiet(asyncDir string) (*AsyncStatus, error) {
	st, err := readStatus(asyncDir)
	if err != nil {
		return nil, nil
	}
	return st, nil
}

// ── process-terminal.ts ──

func processTerminalPath(asyncDir string) string {
	return filepath.Join(asyncDir, "process-terminal.json")
}

func processTerminalCandidatePath(asyncDir string) string {
	return filepath.Join(asyncDir, "process-terminal-candidate.json")
}

// ProcessTerminalCandidate is what the supervisor knows while a run is live: the runner, and the children it started.
type ProcessTerminalCandidate struct {
	Version                 int                              `json:"version"`
	RunID                   string                           `json:"runId"`
	RunnerProcessInstanceID string                           `json:"runnerProcessInstanceId"`
	Writers                 map[string][]ProcessInstanceExit `json:"writers"`
	ExpectedWriters         map[string]int                   `json:"expectedWriters,omitempty"`
	SessionFile             string                           `json:"sessionFile,omitempty"`
}

func readProcessTerminalCandidate(asyncDir string) (*ProcessTerminalCandidate, error) {
	data, err := os.ReadFile(processTerminalCandidatePath(asyncDir))
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]any
	var c ProcessTerminalCandidate
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(data, &c) != nil || c.Version != 1 || c.RunID == "" || c.RunnerProcessInstanceID == "" || raw["writers"] == nil {
		return nil, fmt.Errorf("Invalid process-terminal candidate in '%s'.", asyncDir)
	}
	for idx, entries := range c.Writers {
		for _, w := range entries {
			if !validProcessInstance(w, "pi-writer") {
				return nil, fmt.Errorf("Invalid writer process records for child '%s'.", idx)
			}
		}
	}
	return &c, nil
}

func writeProcessTerminalCandidate(asyncDir string, c *ProcessTerminalCandidate) error {
	return asyncWritePrivateJSON(processTerminalCandidatePath(asyncDir), c)
}

func validProcessInstance(v ProcessInstanceExit, kind string) bool {
	if v.ProcessInstanceID == "" || (kind != "" && v.Kind != kind) || (kind == "" && v.Kind != "runner" && v.Kind != "pi-writer") {
		return false
	}
	if v.Kind == "runner" {
		return v.Attempt == nil
	}
	if v.Attempt == nil || *v.Attempt < 0 || v.ProcessTree == nil {
		return false
	}
	if v.ProcessTree.State == "observed" {
		return v.ProcessTree.Mechanism == "posix-process-group" && v.ProcessTree.ProcessGroupID > 0
	}
	switch v.ProcessTree.Reason {
	case "unsupported-platform", "signal-failed", "verification-failed":
		return v.ProcessTree.State == "unknown"
	}
	return false
}

// initializeProcessTerminal establishes ownership before the run starts any child.
func initializeProcessTerminal(asyncDir, runID, runnerInstanceID string) error {
	if err := writeProcessTerminalCandidate(asyncDir, &ProcessTerminalCandidate{Version: 1, RunID: runID, RunnerProcessInstanceID: runnerInstanceID, Writers: map[string][]ProcessInstanceExit{}}); err != nil {
		return err
	}
	return asyncWriteJSON(processTerminalPath(asyncDir), &ProcessTerminal{Version: 1, State: "pending", RunID: runID, RunnerProcessInstanceID: runnerInstanceID})
}

func unknownProof(runID, runnerInstanceID, reason, diagnostic string) *ProcessTerminal {
	return &ProcessTerminal{Version: 1, State: "unknown", RunID: runID, RunnerProcessInstanceID: runnerInstanceID, Reason: reason, Diagnostic: diagnostic}
}

func validateProof(p *ProcessTerminal, asyncDir, runID, runnerID string) error {
	if p.Version != 1 || p.RunID == "" || p.RunnerProcessInstanceID == "" {
		return fmt.Errorf("Invalid process-terminal proof in '%s'.", asyncDir)
	}
	switch p.State {
	case "pending", "observed", "unknown", "not-started":
	default:
		return fmt.Errorf("Invalid process-terminal proof in '%s'.", asyncDir)
	}
	if runID != "" && p.RunID != runID {
		return fmt.Errorf("Process-terminal proof in '%s' belongs to run '%s', expected '%s'.", asyncDir, p.RunID, runID)
	}
	if runnerID != "" && p.RunnerProcessInstanceID != runnerID {
		return fmt.Errorf("Process-terminal proof in '%s' belongs to runner '%s', expected '%s'.", asyncDir, p.RunnerProcessInstanceID, runnerID)
	}
	for _, in := range p.Instances {
		if !validProcessInstance(in, "") {
			return fmt.Errorf("Invalid process-terminal instances in '%s'.", asyncDir)
		}
	}
	if p.State == "observed" {
		if p.ObservedAt == nil {
			return fmt.Errorf("Observed process-terminal proof in '%s' is missing observedAt.", asyncDir)
		}
		if p.Instances == nil {
			return fmt.Errorf("Observed process-terminal proof in '%s' is missing instances.", asyncDir)
		}
		okRunner := false
		for _, in := range p.Instances {
			okRunner = okRunner || (in.Kind == "runner" && in.ProcessInstanceID == p.RunnerProcessInstanceID)
		}
		if !okRunner {
			return fmt.Errorf("Observed process-terminal proof in '%s' has no matching runner instance.", asyncDir)
		}
	}
	switch p.ResumeDisposition {
	case "", "resumable", "non-resumable", "unavailable":
	default:
		return fmt.Errorf("Invalid process-terminal resume disposition in '%s'.", asyncDir)
	}
	return nil
}

// sanitizeProcessTerminal validates a proof embedded in a status; an invalid one reads as an unknown proof.
func sanitizeProcessTerminal(p *ProcessTerminal, runID, runnerID, label string) *ProcessTerminal {
	if p == nil {
		return nil
	}
	if err := validateProof(p, label, runID, runnerID); err != nil {
		if runnerID == "" {
			runnerID = "unknown"
		}
		return unknownProof(firstNonEmpty(runID, label), runnerID, "proof-write-failed", err.Error())
	}
	return p
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// readProcessTerminal reads process-terminal.json; nil when there is none, an unknown proof when it is unreadable.
func readProcessTerminal(asyncDir, runID, runnerID string) *ProcessTerminal {
	data, err := os.ReadFile(processTerminalPath(asyncDir))
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return unknownProof(firstNonEmpty(runID, filepath.Base(asyncDir)), firstNonEmpty(runnerID, "unknown"), "proof-write-failed", err.Error())
	}
	var p ProcessTerminal
	if err := json.Unmarshal(data, &p); err != nil {
		return unknownProof(firstNonEmpty(runID, filepath.Base(asyncDir)), firstNonEmpty(runnerID, "unknown"), "proof-write-failed", err.Error())
	}
	if err := validateProof(&p, asyncDir, runID, runnerID); err != nil {
		return unknownProof(firstNonEmpty(runID, filepath.Base(asyncDir)), firstNonEmpty(runnerID, "unknown"), "proof-write-failed", err.Error())
	}
	return &p
}

func resumeDispositionOf(state, sessionFile string) string {
	if state == "stopped" {
		return "non-resumable"
	}
	switch state {
	case "complete", "completed", "failed", "paused":
		if sessionFile != "" {
			if _, err := os.Stat(sessionFile); err == nil {
				return "resumable"
			}
		}
	}
	return "unavailable"
}

// RunnerClose is the observed end of the runner process.
type RunnerClose struct {
	ProcessInstanceID string
	CloseObservedAt   int64
	ExitCode          *int
	Signal            *string
}

// overlayStatus puts the proof into status.json (and into each step), as the original's does.
func overlayStatus(asyncDir string, proof *ProcessTerminal, cand *ProcessTerminalCandidate) {
	st, err := readStatus(asyncDir)
	if err != nil || st == nil {
		return // the sidecar stays authoritative when the status is unavailable
	}
	st.ProcessTerminal = proof
	for i := range st.Steps {
		step := &st.Steps[i]
		var records []ProcessInstanceExit
		expected := 0
		if cand != nil {
			records = cand.Writers[fmt.Sprint(i)]
			if e, ok := cand.ExpectedWriters[fmt.Sprint(i)]; ok {
				expected = e
			} else {
				expected = len(records)
			}
		}
		state := "unknown"
		switch {
		case expected == 0:
			state = "not-started"
		case proof.State == "observed" && len(records) == expected:
			state = "observed"
		case proof.State == "pending":
			state = "pending"
		}
		sp := &ProcessTerminal{Version: 1, RunID: proof.RunID, ChildIndex: intPtr(i), RunnerProcessInstanceID: proof.RunnerProcessInstanceID, State: state,
			ResumeDisposition: resumeDispositionOf(step.Status, firstNonEmpty(step.SessionFile, candidateSession(cand)))}
		switch state {
		case "observed":
			if proof.State == "observed" {
				sp.ObservedAt = proof.ObservedAt
			} else {
				sp.ObservedAt = msPtrOf(nowMs())
			}
			sp.Instances = records
		case "unknown":
			sp.Reason = "writer-close-unverified"
			if proof.State == "unknown" && proof.Reason != "" {
				sp.Reason = proof.Reason
			}
		}
		step.ProcessTerminal = sp
	}
	_ = writeStatus(asyncDir, st)
}

func candidateSession(c *ProcessTerminalCandidate) string {
	if c == nil {
		return ""
	}
	return c.SessionFile
}

func msPtrOf(v int64) *ms { m := ms(v); return &m }

// finalizeProcessTerminal publishes the proof when the runner closes: observed when the runner and every writer's process
// tree were seen gone, otherwise unknown with the reason. It also releases the active-run marker.
func finalizeProcessTerminal(asyncDir, runID string, rc RunnerClose) *ProcessTerminal {
	existing := readProcessTerminal(asyncDir, runID, rc.ProcessInstanceID)
	if existing != nil {
		if existing.State == "observed" && existing.RunID == runID && existing.RunnerProcessInstanceID == rc.ProcessInstanceID {
			return existing
		}
		if existing.State == "unknown" {
			if len(existing.Instances) > 0 || existing.Reason == "proof-write-failed" {
				return existing
			}
			withExit := *existing
			withExit.Instances = []ProcessInstanceExit{{Kind: "runner", ProcessInstanceID: rc.ProcessInstanceID, CloseObservedAt: ms(rc.CloseObservedAt), ExitCode: rc.ExitCode, Signal: rc.Signal}}
			if asyncWriteJSON(processTerminalPath(asyncDir), &withExit) == nil {
				return &withExit
			}
			return existing
		}
	}
	runner := ProcessInstanceExit{Kind: "runner", ProcessInstanceID: rc.ProcessInstanceID, CloseObservedAt: ms(rc.CloseObservedAt), ExitCode: rc.ExitCode, Signal: rc.Signal}
	var proof *ProcessTerminal
	cand, cerr := readProcessTerminalCandidate(asyncDir)
	switch {
	case cerr != nil:
		proof = unknownProof(runID, rc.ProcessInstanceID, "proof-write-failed", cerr.Error())
	case cand == nil:
		proof = unknownProof(runID, rc.ProcessInstanceID, "runner-candidate-missing", "")
	case cand.RunID != runID || cand.RunnerProcessInstanceID != rc.ProcessInstanceID:
		proof = unknownProof(runID, rc.ProcessInstanceID, "runner-instance-mismatch", "")
	default:
		var all []ProcessInstanceExit
		for _, k := range sortedKeys(cand.Writers) {
			all = append(all, cand.Writers[k]...)
		}
		expected := cand.ExpectedWriters
		if expected == nil {
			expected = map[string]int{}
			for k, r := range cand.Writers {
				expected[k] = len(r)
			}
		}
		inconsistent := false
		for k, r := range cand.Writers {
			if e, ok := expected[k]; !ok || len(r) != e {
				inconsistent = true
			}
		}
		for k, e := range expected {
			if _, ok := cand.Writers[k]; !ok && e != 0 {
				inconsistent = true
			}
		}
		treeUnverified := false
		for _, w := range all {
			if w.Kind == "pi-writer" && (w.ProcessTree == nil || w.ProcessTree.State != "observed") {
				treeUnverified = true
			}
		}
		st, _ := readStatusQuiet(asyncDir)
		stateName := ""
		sessionFromStatus := ""
		if st != nil {
			stateName, sessionFromStatus = st.State, st.SessionFile
		}
		switch {
		case inconsistent || (len(all) == 0 && len(expected) == 0):
			proof = unknownProof(runID, rc.ProcessInstanceID, "writer-close-unverified", "")
		case treeUnverified:
			proof = unknownProof(runID, rc.ProcessInstanceID, "process-tree-unverified", "")
		default:
			proof = &ProcessTerminal{Version: 1, State: "observed", RunID: runID, RunnerProcessInstanceID: rc.ProcessInstanceID, ObservedAt: msPtrOf(rc.CloseObservedAt),
				Instances: append([]ProcessInstanceExit{runner}, all...), ResumeDisposition: resumeDispositionOf(stateName, firstNonEmpty(cand.SessionFile, sessionFromStatus))}
		}
	}
	if proof.State == "unknown" {
		proof.Instances = []ProcessInstanceExit{runner} // an unverified tree still has a directly observed runner exit
	}
	if err := asyncWriteJSON(processTerminalPath(asyncDir), proof); err != nil {
		return unknownProof(runID, rc.ProcessInstanceID, "proof-write-failed", "Failed to persist process-terminal proof.")
	}
	if proof.State == "observed" {
		_ = releaseActiveRunIndex(asyncDir)
	}
	overlayStatus(asyncDir, proof, cand)
	appendJSONLine(filepath.Join(asyncDir, "events.jsonl"), map[string]any{"type": "subagent.run.process_terminal", "lifecycleArtifactVersion": lifecycleArtifactVersion, "ts": nowMs(), "runId": runID, "processTerminal": proof})
	return proof
}

// appendJSONLine appends one JSON line to path, best effort (a broken event log must not stop the run).
func appendJSONLine(path string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// ── stale-run-reconciler.ts ──

// ReconcileResult is the outcome of looking at one run directory.
type ReconcileResult struct {
	Status     *AsyncStatus
	Repaired   bool
	ResultPath string
	Message    string
}

// ReconcileOptions are the seams of the reconciler.
type ReconcileOptions struct {
	ResultsDir         string
	Kill               killFunc
	Now                func() int64
	StartedRun         *StartedRunMetadata
	MissingStatusGrace *int64
	StaleAlivePidMs    *int64
	PidScope           func() string
}

// StartedRunMetadata is what the start event knew about a run whose status file is not written yet.
type StartedRunMetadata struct {
	RunID             string
	PID               *int
	SessionID         string
	CompletionOwnerID string
	Mode              string
	Agents            []string
	StartedAt         *int64
	SessionFile       string
}

func (o ReconcileOptions) now() int64 {
	if o.Now != nil {
		return o.Now()
	}
	return nowMs()
}

func readRunnerStartupDiagnostics(asyncDir string) string {
	const maxBytes = 64 * 1024
	f, err := os.Open(filepath.Join(asyncDir, "runner.stderr.log"))
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() <= 0 {
		return ""
	}
	n := min(st.Size(), maxBytes)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, st.Size()-n); err != nil {
		return ""
	}
	content := jsTrim(string(buf))
	if content == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	tail := strings.Join(lines, "\n")
	if len(tail) > 4000 {
		return tail[len(tail)-4000:] + "\n[stderr tail truncated]"
	}
	return tail
}

type resultChildOutcome struct {
	Agent           string
	SessionName     string
	Success         *bool
	Error           string
	SessionFile     string
	Model           string
	Thinking        string
	RequestedModel  string
	ContextOverflow *bool
}

type resultRepairData struct {
	State   string
	Error   string
	Results []resultChildOutcome
}

func regularFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func readResultRepairData(resultPath string) (*resultRepairData, error) {
	data, err := os.ReadFile(resultPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to read async result file '%s': %s", resultPath, err.Error())
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("Failed to read async result file '%s': %s", resultPath, jsParseErrorText(data, err))
	}
	if e, has := d["error"]; has && e != nil {
		if _, ok := e.(string); !ok {
			return nil, fmt.Errorf("Invalid async result file '%s': error must be a string.", resultPath)
		}
	}
	state := "failed"
	exit, _ := d["exitCode"].(float64)
	switch s, _ := d["state"].(string); {
	case d["success"] == true:
		state = "complete"
	case s == "stopped", s == "rejected", s == "partial":
		state = s
	case s == "paused" || (d["exitCode"] != nil && exit == 0):
		state = "paused"
	}
	out := &resultRepairData{State: state, Error: nonEmptyString(d["error"])}
	if arr, ok := d["results"].([]any); ok {
		for i, e := range arr {
			c, ok := e.(map[string]any)
			if !ok {
				out.Results = append(out.Results, resultChildOutcome{})
				continue
			}
			for _, f := range []string{"model", "thinking"} {
				if v, has := c[f]; has && v != nil {
					if _, ok := v.(string); !ok {
						return nil, fmt.Errorf("Invalid async result file '%s': results[%d].%s must be a string.", resultPath, i, f)
					}
				}
			}
			o := resultChildOutcome{Agent: nonEmptyString(c["agent"]), SessionName: nonEmptyString(c["sessionName"]), Error: nonEmptyString(c["error"]),
				SessionFile: nonEmptyString(c["sessionFile"]), Model: nonEmptyString(c["model"]), Thinking: nonEmptyString(c["thinking"]), RequestedModel: nonEmptyString(c["requestedModel"])}
			if b, ok := c["success"].(bool); ok {
				o.Success = &b
			}
			if b, ok := c["contextOverflow"].(bool); ok {
				o.ContextOverflow = &b
			}
			out.Results = append(out.Results, o)
		}
	}
	return out, nil
}

// jsParseErrorText renders a JSON syntax error the way V8's JSON.parse words the common cases.
func jsParseErrorText(data []byte, err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) && strings.TrimSpace(string(data)) != "" {
		if strings.Contains(err.Error(), "unexpected end of JSON input") {
			return "Unexpected end of JSON input"
		}
	}
	if strings.Contains(err.Error(), "unexpected end of JSON input") {
		return "Unexpected end of JSON input"
	}
	return err.Error()
}

func childStateOf(overall string, c *resultChildOutcome) string {
	if c != nil && c.Success != nil {
		if *c.Success {
			return "complete"
		}
		return "failed"
	}
	return overall
}

func failureDiagnostic(s string) string {
	if jsTrim(s) != "" {
		return s
	}
	return ""
}

func terminalStatusFromResult(st *AsyncStatus, resultPath string, now int64) (*AsyncStatus, error) {
	repair, err := readResultRepairData(resultPath)
	if err != nil || repair == nil {
		return nil, err
	}
	rootError := firstNonEmpty(failureDiagnostic(st.Error), failureDiagnostic(repair.Error))
	if rootError == "" {
		for _, c := range repair.Results {
			if c.Success != nil && !*c.Success && failureDiagnostic(c.Error) != "" {
				rootError = c.Error
				break
			}
		}
	}
	out := *st
	out.Steps = append([]AsyncStep(nil), st.Steps...)
	for i := range out.Steps {
		step := &out.Steps[i]
		if step.Status != "running" && step.Status != "pending" {
			continue
		}
		var child *resultChildOutcome
		if i < len(repair.Results) {
			child = &repair.Results[i]
		}
		state := childStateOf(repair.State, child)
		step.Status = state
		if step.EndedAt == nil {
			step.EndedAt = msPtrOf(now)
		}
		if step.StartedAt != nil && step.DurationMs == nil {
			step.DurationMs = msPtrOf(max(0, now-int64(*step.StartedAt)))
		}
		if step.ExitCode == nil {
			code := 1
			if state == "complete" || state == "paused" {
				code = 0
			}
			step.ExitCode = &code
		}
		if (state == "failed" || state == "partial" || state == "stopped") && step.Error == "" && child != nil {
			step.Error = child.Error
		}
		if state == "stopped" {
			step.Stopped = new(true)
		}
		if child != nil {
			step.SessionName = firstNonEmpty(step.SessionName, child.SessionName)
			step.SessionFile = firstNonEmpty(step.SessionFile, child.SessionFile)
			step.Model = firstNonEmpty(child.Model, step.Model)
			step.Thinking = firstNonEmpty(child.Thinking, step.Thinking)
			step.RequestedModel = firstNonEmpty(child.RequestedModel, step.RequestedModel)
			if child.ContextOverflow != nil {
				step.ContextOverflow = child.ContextOverflow
			}
		}
	}
	out.State = repair.State
	if rootError != "" && (repair.State == "failed" || repair.State == "partial") {
		out.Error = rootError
	}
	if st.LifecycleArtifactVersion == lifecycleArtifactVersion && (st.ProcessTerminal == nil || st.ProcessTerminal.State == "pending") {
		out.ProcessTerminal = &ProcessTerminal{Version: 1, State: "unknown", RunID: st.RunID, RunnerProcessInstanceID: "observer-unavailable", Reason: "observer-unavailable"}
	}
	if repair.State == "stopped" {
		out.Stopped = new(true)
	}
	out.ActivityState = ""
	out.LastUpdate = msPtrOf(now)
	if out.EndedAt == nil {
		out.EndedAt = msPtrOf(now)
	}
	out.DisplayDismissedAt = nil
	return &out, nil
}

func buildStartedStatus(asyncDir string, sr *StartedRunMetadata, now int64) *AsyncStatus {
	startedAt := now
	if sr.StartedAt != nil {
		startedAt = *sr.StartedAt
	}
	agents := sr.Agents
	if len(agents) == 0 {
		agents = []string{"subagent"}
	}
	mode := sr.Mode
	if mode == "" {
		mode = "single"
	}
	st := &AsyncStatus{RunID: firstNonEmpty(sr.RunID, filepath.Base(asyncDir)), SessionID: sr.SessionID, CompletionOwnerID: sr.CompletionOwnerID, Mode: mode, State: "running",
		PID: sr.PID, PidNamespaceScope: currentPidNamespaceScope(), StartedAt: ms(startedAt), LastUpdate: msPtrOf(now), CurrentStep: intPtr(0), SessionFile: sr.SessionFile}
	for _, a := range agents {
		st.Steps = append(st.Steps, AsyncStep{Agent: a, Status: "running", StartedAt: msPtrOf(startedAt)})
	}
	return st
}

type failedRepair struct {
	status  *AsyncStatus
	result  map[string]any
	message string
}

func buildFailedRepair(st *AsyncStatus, asyncDir string, now int64, reason string) failedRepair {
	runID := firstNonEmpty(st.RunID, filepath.Base(asyncDir))
	pid := "unknown"
	if st.PID != nil {
		pid = fmt.Sprint(*st.PID)
	}
	term := readProcessTerminal(asyncDir, runID, "")
	if term == nil {
		term = st.ProcessTerminal
	}
	exitText := "exited or disappeared"
	if term != nil && (term.State == "observed" || term.State == "unknown") {
		for _, in := range term.Instances {
			if in.Kind == "runner" && in.ProcessInstanceID == term.RunnerProcessInstanceID {
				code := "none"
				if in.ExitCode != nil {
					code = fmt.Sprint(*in.ExitCode)
				}
				exitText = "exited with code " + code
				if in.Signal != nil && *in.Signal != "" {
					exitText += " (signal " + *in.Signal + ")"
				}
			}
		}
	}
	base := reason
	if base == "" {
		base = fmt.Sprintf("Async runner process %s %s before writing a result. Marked run failed by stale-run reconciliation.", pid, exitText)
	}
	message := base
	if d := readRunnerStartupDiagnostics(asyncDir); d != "" {
		message = base + "\n\nRunner stderr tail:\n" + d
	}
	steps := append([]AsyncStep(nil), st.Steps...)
	if len(steps) == 0 {
		steps = []AsyncStep{{Agent: "subagent", Status: "running"}}
	}
	for i := range steps {
		s := &steps[i]
		if s.Status != "running" && s.Status != "pending" {
			continue
		}
		s.Status = "failed"
		s.ActivityState = ""
		if s.EndedAt == nil {
			s.EndedAt = msPtrOf(now)
		}
		if s.StartedAt != nil && s.DurationMs == nil {
			s.DurationMs = msPtrOf(max(0, now-int64(*s.StartedAt)))
		}
		if s.ExitCode == nil {
			s.ExitCode = intPtr(1)
		}
		if s.Error == "" {
			s.Error = message
		}
	}
	repaired := *st
	repaired.State = "failed"
	if st.LifecycleArtifactVersion == lifecycleArtifactVersion && (st.ProcessTerminal == nil || st.ProcessTerminal.State == "pending") {
		repaired.ProcessTerminal = &ProcessTerminal{Version: 1, State: "unknown", RunID: runID, RunnerProcessInstanceID: "observer-unavailable", Reason: "stale-repair"}
	}
	repaired.ActivityState = ""
	repaired.LastUpdate = msPtrOf(now)
	repaired.EndedAt = msPtrOf(now)
	repaired.Steps = steps
	cur := 0
	if st.CurrentStep != nil {
		cur = *st.CurrentStep
	}
	agent := "subagent"
	if cur < len(steps) {
		agent = steps[cur].Agent
	} else if len(steps) > 0 {
		agent = steps[0].Agent
	}
	results := []any{}
	for _, s := range steps {
		done := s.Status == "complete" || s.Status == "completed"
		r := map[string]any{"agent": s.Agent, "output": message, "success": done}
		if done {
			r["output"] = ""
		} else {
			r["error"] = firstNonEmpty(s.Error, message)
		}
		if s.SessionName != "" {
			r["sessionName"] = s.SessionName
		}
		if s.Model != "" {
			r["model"] = s.Model
		}
		if s.RequestedModel != "" {
			r["requestedModel"] = s.RequestedModel
		}
		if s.ContextOverflow != nil {
			r["contextOverflow"] = *s.ContextOverflow
		}
		if s.SessionFile != "" {
			r["sessionFile"] = s.SessionFile
		}
		results = append(results, r)
	}
	result := map[string]any{"id": runID, "agent": agent, "mode": st.Mode, "success": false, "state": "failed", "summary": message, "results": results,
		"exitCode": 1, "timestamp": now, "durationMs": max(0, now-int64(st.StartedAt)), "asyncDir": asyncDir}
	if st.CompletionOwnerID != "" {
		result["completionOwnerId"] = st.CompletionOwnerID
	}
	if st.SessionID != "" {
		result["sessionId"] = st.SessionID
	}
	if st.SessionFile != "" {
		result["sessionFile"] = st.SessionFile
	}
	return failedRepair{status: &repaired, result: result, message: message}
}

func writeFailedRepair(asyncDir string, st *AsyncStatus, resultPath string, now int64, reason string) ReconcileResult {
	repair := buildFailedRepair(st, asyncDir, now, reason)
	hasSession := repair.result["sessionId"] != nil
	if hasSession {
		_, _ = writeAsyncResultFile(resultPath, repair.result)
	}
	_ = writeStatus(asyncDir, repair.status)
	_ = updateActiveRunIndex(asyncDir, repair.status.State, repair.status.ToolCallID, indexOptions{})
	ev := map[string]any{"type": "subagent.run.repaired_stale", "ts": now, "runId": repair.status.RunID, "message": repair.message}
	if st.PID != nil {
		ev["pid"] = *st.PID
	}
	if hasSession {
		ev["resultPath"] = resultPath
	}
	appendJSONLine(filepath.Join(asyncDir, "events.jsonl"), ev)
	out := ReconcileResult{Status: repair.status, Repaired: true, Message: repair.message}
	if hasSession {
		out.ResultPath = resultPath
	}
	return out
}

func terminalState(s string) bool { return isTerminalAsyncState(s) }

// reconcileAsyncRun repairs a run whose runner is gone: from an existing result file, or by failing it when its pid is dead
// (or has been silent for a day with unverified ownership).
func reconcileAsyncRun(asyncDir string, o ReconcileOptions) (ReconcileResult, error) {
	now := o.now()
	st, err := readStatus(asyncDir)
	if err != nil {
		return ReconcileResult{}, err
	}
	var started *AsyncStatus
	if st == nil && o.StartedRun != nil {
		started = buildStartedStatus(asyncDir, o.StartedRun, now)
	}
	eff := st
	if eff == nil {
		eff = started
	}
	if eff == nil {
		return ReconcileResult{}, nil
	}
	runID := firstNonEmpty(eff.RunID, filepath.Base(asyncDir))
	resultsDir := o.ResultsDir
	resultPath := resultFilePath(resultsDir, runID)
	existing := ""
	if eff.SessionID != "" {
		p, perr := resultPayloadPathForSessionRun(resultsDir, eff.SessionID, runID)
		if perr != nil {
			return ReconcileResult{}, perr
		}
		existing = p
		if existing == "" && regularFileExists(resultPath) {
			existing = resultPath
		}
	} else if regularFileExists(resultPath) {
		existing = resultPath
	}
	if existing != "" {
		var terminal *AsyncStatus
		if eff.State == "running" || eff.State == "queued" {
			terminal, err = terminalStatusFromResult(eff, existing, now)
			if err != nil {
				return ReconcileResult{}, err
			}
		}
		if terminal != nil {
			cur, _ := readStatusQuiet(asyncDir)
			write := *terminal
			if cur != nil {
				if rootErr := failureDiagnostic(cur.Error); rootErr != "" {
					write.Error = rootErr
				}
				if cur.ProcessTerminal != nil && (cur.ProcessTerminal.State == "observed" || cur.ProcessTerminal.State == "unknown") {
					write.ProcessTerminal = cur.ProcessTerminal
				}
				for i := range write.Steps {
					if i < len(cur.Steps) {
						if sp := cur.Steps[i].ProcessTerminal; sp != nil && (sp.State == "observed" || sp.State == "unknown") {
							write.Steps[i].ProcessTerminal = sp
						}
					}
				}
			}
			if err := writeStatus(asyncDir, &write); err != nil {
				return ReconcileResult{}, err
			}
			_ = updateActiveRunIndex(asyncDir, write.State, write.ToolCallID, indexOptions{})
			return ReconcileResult{Status: &write, Repaired: true, ResultPath: existing, Message: "Existing async result file was used to repair stale running status."}, nil
		}
		if eff.DisplayDismissedAt == nil {
			return ReconcileResult{Status: eff, ResultPath: existing}, nil
		}
	}
	if eff.DisplayDismissedAt != nil {
		return ReconcileResult{ResultPath: resultPath}, nil
	}
	if eff.State != "running" || eff.PID == nil {
		return ReconcileResult{Status: st, ResultPath: resultPath}, nil
	}
	if st == nil {
		startedAt := int64(eff.StartedAt)
		grace := int64(1000)
		if o.MissingStatusGrace != nil {
			grace = *o.MissingStatusGrace
		}
		if now-startedAt < grace {
			return ReconcileResult{ResultPath: resultPath}, nil
		}
	}
	observedScope := currentPidNamespaceScope()
	if o.PidScope != nil {
		observedScope = o.PidScope()
	}
	// an observer without a scope (a host sharing a container's temp root) cannot match a recorded one
	scopeMismatch := eff.PidNamespaceScope != "" && eff.PidNamespaceScope != observedScope
	liveness := checkPidLiveness(*eff.PID, o.Kill)
	if liveness == pidDead && scopeMismatch {
		liveness = pidUnknown
	}
	if liveness != pidDead {
		staleAfter := int64(defaultStaleAlivePidMs)
		if o.StaleAlivePidMs != nil {
			staleAfter = *o.StaleAlivePidMs
		}
		lastUpdate := int64(eff.StartedAt)
		if eff.LastUpdate != nil {
			lastUpdate = int64(*eff.LastUpdate)
		}
		if now-lastUpdate <= staleAfter {
			return ReconcileResult{Status: st, ResultPath: resultPath}, nil
		}
		probe := "cannot be probed from this process"
		if liveness == pidAlive {
			probe = "is still live"
		}
		message := fmt.Sprintf("Async runner PID %d %s; status has not updated for %dms, so stale-run reconciliation marked the run failed because PID ownership is unverified.", *eff.PID, probe, now-lastUpdate)
		return writeFailedRepair(asyncDir, eff, resultPath, now, message), nil
	}
	return writeFailedRepair(asyncDir, eff, resultPath, now, ""), nil
}

const defaultStaleAlivePidMs = 24 * 60 * 60 * 1000
