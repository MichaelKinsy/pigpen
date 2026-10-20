package pi_subagents

// The session's background state and the completions bg_wait reads from it. Ports the SubagentState fields of the background
// runs (types.ts), wait-completions.ts and api/background-work.ts (the process-local provider registry).

import (
	"path/filepath"

	"sync"
)

// ForegroundChildRecord is a foreground child remembered after it detached from its parent.
type ForegroundChildRecord struct {
	Index           int
	Agent           string
	Status          string // detached, completed, failed, ...
	ActivityState   string
	CurrentTool     string
	CurrentToolArgs string
	TranscriptPath  string
}

// ForegroundRunRecord is a foreground run remembered for bg_wait and resume.
type ForegroundRunRecord struct {
	RunID     string
	SessionID string
	Children  []ForegroundChildRecord
}

// completedResult is a terminal payload the result watcher observed, kept until its TTL ends.
type completedResult struct {
	SessionID  string
	SeenAt     int64
	Completion WaitCompletion
}

// completedStore is the session's record of consumed completions (a Map in the original; an interface so a test can race it).
type completedStore interface {
	Get(runID string) (completedResult, bool)
	Set(runID string, v completedResult)
	Prune(now, ttlMs int64)
}

type mapCompletedStore struct{ m map[string]completedResult }

func newCompletedStore() completedStore { panic("not ported yet") }

func (s *mapCompletedStore) Get(id string) (completedResult, bool) { panic("not ported yet") }
func (s *mapCompletedStore) Set(id string, v completedResult)      { panic("not ported yet") }
func (s *mapCompletedStore) Prune(now, ttl int64)                  { panic("not ported yet") }

// SubagentState is the extension's per-session state for background runs.
type SubagentState struct {
	Mu               sync.Mutex
	BaseCwd          string
	CurrentSessionID string
	AsyncJobs        map[string]*AsyncJobState
	CompletedResults completedStore
	CompletionSeen   map[string]int64
	ForegroundRuns   map[string]*ForegroundRunRecord
	WidgetsSuspended bool
}

func newSubagentState() *SubagentState { panic("not ported yet") }

// AsyncJobState is a run the session tracks (the original's AsyncJobState, for single-child runs).
type AsyncJobState struct {
	AsyncID           string
	AsyncDir          string
	ToolCallID        string
	Cwd               string
	SessionRoot       string
	Status            string
	Description       string
	PID               int
	SessionID         string
	CompletionOwnerID string
	ActivityState     string
	LastActivityAt    *ms
	CurrentTool       string
	CurrentToolAt     *ms
	CurrentPath       string
	TurnCount         *int
	ToolCount         *int
	Mode              string
	Agents            []string
	CurrentStep       *int
	Steps             []AsyncRunStepSummary
	StartedAt         int64
	UpdatedAt         int64
	TimeoutMs         *ms
	DeadlineAt        *ms
	SessionFile       string
	OutputFile        string
	SessionDir        string
	TotalTokens       *TokenUsage
}

// ── wait-completions.ts ──

func waitResultPayloadCandidate(resultsDir string, run AsyncRunSummary) (string, error) {
	panic("not ported yet")
}

// readWaitResultPayload reads the payload at candidatePath; nil when it is not this session's run (a run without a session owns nothing).
func readWaitResultPayload(candidatePath string, run AsyncRunSummary) (map[string]any, error) {
	panic("not ported yet")
}

// recordWaitCompletion records a consumed terminal payload for bg_wait, pruning stale entries with the notification TTL.
// Payload ownership must be explicit and agree with persistence ownership. It reports whether the replay is durable.
func recordWaitCompletion(state *SubagentState, runID string, data map[string]any, now, ttlMs int64, persistence *struct{ ResultsDir, SessionID string }) bool {
	panic("not ported yet")
}

// collectWaitCompletions: the terminal payloads of the runs a wait covered: the watcher's in-memory record first, then the
// not-yet-consumed result file. The read is read-only; the watcher owns notification and cleanup.
func collectWaitCompletions(terminal []AsyncRunSummary, state *SubagentState, resultsDir string, onReference func(string)) ([]WaitCompletion, error) {
	panic("not ported yet")
}

// tool details are not model context: publish the evidence address in content, never the output text

// the watcher may have consumed the file between the store check and the read: its in-memory record, then the replay

// ── api/background-work.ts ──

const (
	backgroundWorkProtocolVersion = 1
	maxProviderNameLength         = 128
	maxProviders                  = 100
	maxItemIDLength               = 256
	maxBackgroundSessionIDLength  = 4096
	maxWakeChannelLength          = 256
	maxItemsPerProvider           = 10_000
)

// BackgroundWorkItem is one active unit of provider work.
type BackgroundWorkItem struct {
	ID        string
	SessionID string
	// extra names the unknown fields a provider's item carried (a JavaScript provider can add any; a Go one cannot, a test can).
	Extra []string
}

// RegisteredBackgroundWorkItem is a work item with its provider.
type RegisteredBackgroundWorkItem struct {
	Provider  string
	ID        string
	SessionID string
}

// BackgroundWorkSnapshot is the active provider work of one session.
type BackgroundWorkSnapshot struct {
	Providers []string
	Items     []RegisteredBackgroundWorkItem
}

// BackgroundWorkContext is what a provider is asked with.
type BackgroundWorkContext struct {
	SessionID string
	NowMs     int64
}

// BackgroundWorkProvider is a source of background work that has no native completion notification.
type BackgroundWorkProvider struct {
	Name           string
	ListActiveWork func(ctx BackgroundWorkContext) ([]BackgroundWorkItem, error)
	WakeChannels   []string
	Reconcile      func(ctx BackgroundWorkContext) error
	// unknownFields lets a test model a provider object with fields the protocol does not know.
	UnknownFields []string
}

var (
	bgWorkMu        sync.Mutex
	bgWorkProviders = map[string]*BackgroundWorkProvider{}
	bgWorkOrder     []string
)

func validateBGString(value any, field string, maxLength int) (string, error) {
	panic("not ported yet")
}

func validateBGProvider(p *BackgroundWorkProvider) error { panic("not ported yet") }

func validateBGItem(provider string, item BackgroundWorkItem, index int) error {
	panic("not ported yet")
}

// registerBackgroundWorkProvider registers or replaces a process-local provider. The disposer removes only this exact
// registration, so an old extension reload cannot unregister its replacement.
func registerBackgroundWorkProvider(p *BackgroundWorkProvider) (func(), error) {
	panic("not ported yet")
}

func clearBackgroundWorkRegistry() { panic("not ported yet") }

func listBackgroundWorkProviders() ([]*BackgroundWorkProvider, error) { panic("not ported yet") }

// listBackgroundWorkWakeChannels reads validated provider wake channels without reconciling or listing work.
func listBackgroundWorkWakeChannels() ([]string, error) { panic("not ported yet") }

// snapshotBackgroundWork reconciles and snapshots the active provider work owned by one exact session.
func snapshotBackgroundWork(sessionID string, nowMs int64) (BackgroundWorkSnapshot, error) {
	panic("not ported yet")
}

var _ = filepath.Join
