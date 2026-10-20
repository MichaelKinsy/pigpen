package pi_subagents

// Run summaries and the run list: the projection of status.json that the wait tool, the status action and the widget read.
// Ports the parts of async-status.ts and child-identity.ts for runs without workflow, nested or parallel-group state.

import ()

// AsyncRunStepSummary is one child of a run summary.
type AsyncRunStepSummary struct {
	AsyncStep
	Index   int
	ChildID string
}

// AsyncRunSummary is a run as the listing sees it.
type AsyncRunSummary struct {
	ID              string
	AsyncDir        string
	ToolCallID      string
	SessionID       string
	State           string
	Error           string
	ActivityState   string
	LastActivityAt  *ms
	CurrentTool     string
	CurrentToolAt   *ms
	CurrentPath     string
	TurnCount       *int
	ToolCount       *int
	Mode            string
	Context         string
	Cwd             string
	SessionRoot     string
	StartedAt       int64
	LastUpdate      *ms
	EndedAt         *ms
	TimeoutMs       *ms
	DeadlineAt      *ms
	TimedOut        *bool
	Stopped         *bool
	CurrentStep     *int
	ChainStepCount  *int
	Steps           []AsyncRunStepSummary
	SessionDir      string
	OutputFile      string
	TotalTokens     *TokenUsage
	TotalCost       *CostSummary
	SessionFile     string
	ProcessTerminal *ProcessTerminal
}

// asyncStatusChildIdentity is child-identity.ts: the stable caller-facing id of a step.
func asyncStatusChildIdentity(step AsyncStep, index int) string { panic("not ported yet") }

func asyncStatusChildIdentityCandidates(step AsyncStep, index int) []string { panic("not ported yet") }

func outputFileMtime(outputFile string) (int64, bool) { panic("not ported yet") }

// deriveAsyncActivity: a running run's last activity falls back to its output file's mtime, then its current step.
func deriveAsyncActivity(asyncDir string, st *AsyncStatus) (string, *ms) { panic("not ported yet") }

// statusToSummary projects a status into a summary.
func statusToSummary(asyncDir string, st *AsyncStatus) AsyncRunSummary { panic("not ported yet") }

func sortRuns(runs []AsyncRunSummary) { panic("not ported yet") }

// TargetedRun is the resolution of an id against the async root.
type TargetedRun struct {
	Kind string // exact, prefix, reject
	ID   string
}

// resolveTargetedAsyncRun resolves an exact run without following a symlinked run directory or accepting a path whose
// canonical location left the root.
func resolveTargetedAsyncRun(asyncDirRoot, id, sessionID string) (TargetedRun, error) {
	panic("not ported yet")
}

// AsyncRunListOptions selects runs for listAsyncRuns.
type AsyncRunListOptions struct {
	States      []string
	SessionID   string
	Limit       int // 0: no limit
	EntryLimit  int // 0: no limit
	ResultsDir  string
	Kill        killFunc
	Now         func() int64
	NoReconcile bool
	RunID       string
	ExactRunID  bool
	RepairScan  bool
	// Observe is called with each raw status read (nil for a missing or unreadable one): the drain's read-only observation.
	Observe func(*AsyncStatus)
}

func isAsyncRunDir(root, entry string) bool { panic("not ported yet") }

// isolateCorruptActiveRun logs nothing (an extension must not write to stdout); an observed process end releases the marker.
func isolateCorruptActiveRun(asyncDir, runID string, now func() int64) { panic("not ported yet") }

func isStatusIsolationError(asyncDir string, err error) bool { panic("not ported yet") }

// listAsyncRuns lists the runs of a root: the active ones from the active index, the finished ones from the terminal index.
func listAsyncRuns(asyncDirRoot string, o AsyncRunListOptions) ([]AsyncRunSummary, error) {
	panic("not ported yet")
}

// ── text ──

func formatActivityFacts(r *AsyncRunSummary) string { panic("not ported yet") }

func activityFactsOf(activity string, last *ms, tool string, toolAt *ms, path string, turns, tools *int) string {
	panic("not ported yet")
}

func formatStepLine(s AsyncRunStepSummary) string { panic("not ported yet") }

// formatAsyncRunOutputPath is the run's output file, made absolute against the run directory.
func formatAsyncRunOutputPath(asyncDir, outputFile string) string { panic("not ported yet") }

// formatAsyncRunProgressLabel is "step N/M" (or "steps M" before the first step starts).
func formatAsyncRunProgressLabel(mode string, current *int, stepCount int) string {
	panic("not ported yet")
}

func formatRunHeader(r AsyncRunSummary) string { panic("not ported yet") }

// formatAsyncRunList renders the runs (heading defaults to "Active async runs").
func formatAsyncRunList(runs []AsyncRunSummary, heading string) string { panic("not ported yet") }
