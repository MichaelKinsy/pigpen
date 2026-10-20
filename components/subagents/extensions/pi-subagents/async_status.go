package pi_subagents

// The run directory's bookkeeping: the active and terminal run indexes, the process-terminal proof, the reconciliation of a
// stale run, the run summaries and their text. Ports active-run-index.ts, terminal-run-index.ts, process-terminal.ts,
// process-terminal-candidate.ts, stale-run-reconciler.ts and async-status.ts for the runs this port starts (single children;
// workflow, nested and parallel-group fields are not modelled).

import ()

const (
	activeRunIndexDir                = ".active-runs"
	terminalRunIndexDir              = ".terminal-runs"
	defaultStaleTerminalActiveMarker = 24 * 60 * 60 * 1000
	toolCallIndexDirName             = "tool-calls"
	terminalIndexVersion             = 1
	terminalTimestampWidth           = 16
)

// ── active-run-index.ts ──

func activeIndexDir(asyncDirRoot string) string { panic("not ported yet") }

func activeToolCallIndexDir(asyncDirRoot, toolCallID string) string { panic("not ported yet") }

func activeMarkerPath(asyncDir string) string { panic("not ported yet") }

func removeEmptyAncestors(start, stop string) { panic("not ported yet") }

func releaseToolCallAliases(asyncDir string) { panic("not ported yet") }

// releaseActiveRunIndex removes the active marker of a run and its tool-call aliases.
func releaseActiveRunIndex(asyncDir string) error { panic("not ported yet") }

type indexOptions struct {
	retryCapacityErrors        bool
	terminalIndexBeforeRelease bool
}

// updateActiveRunIndex marks an active run, or releases a finished one and records it in the terminal index.
func updateActiveRunIndex(asyncDir, state, toolCallID string, opts indexOptions) error {
	panic("not ported yet")
}

// tool-call aliases are optional: the authoritative marker keeps the launch discoverable without them

func isStorageCapacityError(err error) bool { panic("not ported yet") }

// activeRunMarkerAgeMs is how long ago the active marker was written; -1 when there is none.
func activeRunMarkerAgeMs(asyncDir string, now int64) int64 { panic("not ported yet") }

// readActiveRunIndex lists the runs marked active; nil when the index does not exist.
func readActiveRunIndex(asyncDirRoot string) ([]string, bool) { panic("not ported yet") }

func readActiveRunToolCallIndex(asyncDirRoot, toolCallID string) []string { panic("not ported yet") }

// ── terminal-run-index.ts ──

type terminalIndexEntry struct {
	Version   int    `json:"version"`
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	EndedAt   ms     `json:"endedAt"`
}

func terminalMarkerName(endedAt int64, runID string) string { panic("not ported yet") }

func terminalSessionIndexDir(asyncDirRoot, sessionID string) string { panic("not ported yet") }

// updateTerminalRunIndex records a finished, session-owned run so it can be listed without scanning the root.
func updateTerminalRunIndex(asyncDir string, st *AsyncStatus) error { panic("not ported yet") }

// readRecentTerminalRunIndex lists the newest finished runs (of one session, or all); stale markers are removed.
func readRecentTerminalRunIndex(asyncDirRoot, sessionID string, limit int) []string {
	panic("not ported yet")
}

func readStatusQuiet(asyncDir string) (*AsyncStatus, error) { panic("not ported yet") }

// ── process-terminal.ts ──

func processTerminalPath(asyncDir string) string { panic("not ported yet") }

func processTerminalCandidatePath(asyncDir string) string { panic("not ported yet") }

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
	panic("not ported yet")
}

func writeProcessTerminalCandidate(asyncDir string, c *ProcessTerminalCandidate) error {
	panic("not ported yet")
}

func validProcessInstance(v ProcessInstanceExit, kind string) bool { panic("not ported yet") }

// initializeProcessTerminal establishes ownership before the run starts any child.
func initializeProcessTerminal(asyncDir, runID, runnerInstanceID string) error {
	panic("not ported yet")
}

func unknownProof(runID, runnerInstanceID, reason, diagnostic string) *ProcessTerminal {
	panic("not ported yet")
}

func validateProof(p *ProcessTerminal, asyncDir, runID, runnerID string) error {
	panic("not ported yet")
}

// sanitizeProcessTerminal validates a proof embedded in a status; an invalid one reads as an unknown proof.
func sanitizeProcessTerminal(p *ProcessTerminal, runID, runnerID, label string) *ProcessTerminal {
	panic("not ported yet")
}

func firstNonEmpty(vals ...string) string { panic("not ported yet") }

// readProcessTerminal reads process-terminal.json; nil when there is none, an unknown proof when it is unreadable.
func readProcessTerminal(asyncDir, runID, runnerID string) *ProcessTerminal { panic("not ported yet") }

func resumeDispositionOf(state, sessionFile string) string { panic("not ported yet") }

// RunnerClose is the observed end of the runner process.
type RunnerClose struct {
	ProcessInstanceID string
	CloseObservedAt   int64
	ExitCode          *int
	Signal            *string
}

// overlayStatus puts the proof into status.json (and into each step), as the original's does.
func overlayStatus(asyncDir string, proof *ProcessTerminal, cand *ProcessTerminalCandidate) {
	panic("not ported yet")
}

// the sidecar stays authoritative when the status is unavailable

func candidateSession(c *ProcessTerminalCandidate) string { panic("not ported yet") }

func msPtrOf(v int64) *ms { panic("not ported yet") }

// finalizeProcessTerminal publishes the proof when the runner closes: observed when the runner and every writer's process
// tree were seen gone, otherwise unknown with the reason. It also releases the active-run marker.
func finalizeProcessTerminal(asyncDir, runID string, rc RunnerClose) *ProcessTerminal {
	panic("not ported yet")
}

// an unverified tree still has a directly observed runner exit

// appendJSONLine appends one JSON line to path, best effort (a broken event log must not stop the run).
func appendJSONLine(path string, payload any) { panic("not ported yet") }

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

func (o ReconcileOptions) now() int64 { panic("not ported yet") }

func readRunnerStartupDiagnostics(asyncDir string) string { panic("not ported yet") }

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

func regularFileExists(p string) bool { panic("not ported yet") }

func readResultRepairData(resultPath string) (*resultRepairData, error) { panic("not ported yet") }

// jsParseErrorText renders a JSON syntax error the way V8's JSON.parse words the common cases.
func jsParseErrorText(data []byte, err error) string { panic("not ported yet") }

func childStateOf(overall string, c *resultChildOutcome) string { panic("not ported yet") }

func failureDiagnostic(s string) string { panic("not ported yet") }

func terminalStatusFromResult(st *AsyncStatus, resultPath string, now int64) (*AsyncStatus, error) {
	panic("not ported yet")
}

func buildStartedStatus(asyncDir string, sr *StartedRunMetadata, now int64) *AsyncStatus {
	panic("not ported yet")
}

type failedRepair struct {
	status  *AsyncStatus
	result  map[string]any
	message string
}

func buildFailedRepair(st *AsyncStatus, asyncDir string, now int64, reason string) failedRepair {
	panic("not ported yet")
}

func writeFailedRepair(asyncDir string, st *AsyncStatus, resultPath string, now int64, reason string) ReconcileResult {
	panic("not ported yet")
}

func terminalState(s string) bool { panic("not ported yet") }

// reconcileAsyncRun repairs a run whose runner is gone: from an existing result file, or by failing it when its pid is dead
// (or has been silent for a day with unverified ownership).
func reconcileAsyncRun(asyncDir string, o ReconcileOptions) (ReconcileResult, error) {
	panic("not ported yet")
}

// an observer without a scope (a host sharing a container's temp root) cannot match a recorded one

const defaultStaleAlivePidMs = 24 * 60 * 60 * 1000
