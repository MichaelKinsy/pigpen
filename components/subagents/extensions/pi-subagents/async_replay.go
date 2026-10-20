package pi_subagents

// Completion records: what bg_wait and the completion notice carry about a finished run, and the durable replay that keeps
// them after the one-shot result file is consumed. Ports completion-replay.ts, wait-completions.ts, the bounded parts of
// workflow-child-summary.ts (parse only) and mutation-evidence.ts (projectTimeoutRecovery, formatTimeoutRecoveryLines),
// and utf8.ts.

import (
	"regexp"

	"sync"
)

// ── utf8.ts ──

// utf8Tail is the last maxBytes bytes of value, cut at a character boundary.
func utf8Tail(value string, maxBytes int) (text string, truncated bool) { panic("not ported yet") }

// ── mutation-evidence.ts ──

const maxTimeoutFiles = 20

// TimeoutRecoveryProjection is the bounded routing evidence of a timed-out or stopped child.
type TimeoutRecoveryProjection struct {
	Termination    string   `json:"termination"`
	ChangedFiles   []string `json:"changedFiles"`
	Truncated      bool     `json:"truncated,omitempty"`
	RecoveryNeeded bool     `json:"recoveryNeeded,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	ReportStatus   string   `json:"reportStatus,omitempty"`
}

// projectTimeoutRecovery keeps status and completion details to bounded routing evidence, not raw output or effects.
func projectTimeoutRecovery(value any) *TimeoutRecoveryProjection { panic("not ported yet") }

// formatTimeoutRecoveryLines renders only the bounded recovery route a parent or operator needs.
func formatTimeoutRecoveryLines(value any, indent string) []string { panic("not ported yet") }

// ── workflow-child-summary.ts (parseWorkflowChildSummary) ──

var workflowKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const maxRequiredIDBytes = 4096

var workflowActivityCounters = []string{"currentToolStartedAt", "lastActivityAt", "durationMs", "toolCount", "turnCount", "tokens", "inputTokens", "outputTokens"}

// WorkflowChildRow is one child of a workflow summary.
type WorkflowChildRow struct {
	ChildID     string         `json:"childId"`
	RunID       string         `json:"runId,omitempty"`
	Agent       string         `json:"agent,omitempty"`
	SessionName string         `json:"sessionName,omitempty"`
	Model       string         `json:"model,omitempty"`
	Thinking    string         `json:"thinking,omitempty"`
	State       string         `json:"state"`
	Activity    map[string]any `json:"activity,omitempty"`
}

// WorkflowChildSummary is the bounded child inventory of a workflow run.
type WorkflowChildSummary struct {
	Version           int                `json:"version"`
	ParentToolCallID  string             `json:"parentToolCallId"`
	WorkflowRunID     string             `json:"workflowRunId"`
	InventoryComplete bool               `json:"inventoryComplete"`
	WorkflowState     string             `json:"workflowState"`
	Children          []WorkflowChildRow `json:"children"`
}

func boundedString(v any, maxBytes int) string { panic("not ported yet") }

func requiredID(v, label string) (string, error) { panic("not ported yet") }

func parseWorkflowActivity(v any) (map[string]any, error) { panic("not ported yet") }

// parseWorkflowChildSummary validates a persisted summary; nil for an absent value.
func parseWorkflowChildSummary(value any, present bool) (*WorkflowChildSummary, error) {
	panic("not ported yet")
}

// ── the completion shapes (types.ts WaitCompletion) ──

// Usage is a child's token and cost accounting.
type Usage struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	Turns      int64   `json:"turns"`
}

// WaitCompletionChild is one child's bounded outcome.
type WaitCompletionChild struct {
	Agent                string                     `json:"agent,omitempty"`
	RunID                string                     `json:"runId,omitempty"`
	Usage                *Usage                     `json:"usage,omitempty"`
	SessionFile          string                     `json:"sessionFile,omitempty"`
	Success              *bool                      `json:"success,omitempty"`
	OutputState          string                     `json:"outputState,omitempty"`
	StructuredOutput     any                        `json:"structuredOutput,omitempty"`
	StructuredOutputPath string                     `json:"structuredOutputPath,omitempty"`
	Error                string                     `json:"error,omitempty"`
	Model                string                     `json:"model,omitempty"`
	ContextOverflow      bool                       `json:"contextOverflow,omitempty"`
	ArtifactPaths        map[string]any             `json:"artifactPaths,omitempty"`
	TimeoutRecovery      *TimeoutRecoveryProjection `json:"timeoutRecovery,omitempty"`
}

// WaitCompletion is the terminal completion of a run a bg_wait call covered: identity and the artifact trail, never the output text.
type WaitCompletion struct {
	RunID               string                `json:"runId"`
	WorkflowReceiptPath string                `json:"workflowReceiptPath,omitempty"`
	Agent               string                `json:"agent,omitempty"`
	Mode                string                `json:"mode,omitempty"`
	State               string                `json:"state,omitempty"`
	Success             *bool                 `json:"success,omitempty"`
	ArchivePath         string                `json:"archivePath,omitempty"`
	Results             []WaitCompletionChild `json:"results,omitempty"`
	WorkflowChildren    *WorkflowChildSummary `json:"workflowChildren,omitempty"`
}

const structuredOutputInlineLimitBytes = 4 * 1024

func nonNegativeNumber(v any) (float64, bool) { panic("not ported yet") }

func projectedUsage(v any) *Usage { panic("not ported yet") }

// projectStructuredOutput keeps a value of at most 4 KiB serialized, drops a larger one, and rejects what is not JSON.
func projectStructuredOutput(v any) (any, error) { panic("not ported yet") }

// toWaitCompletion projects a terminal result payload into the slim shape that is safe in tool_result details.
func toWaitCompletion(data map[string]any, runID string) (WaitCompletion, error) {
	panic("not ported yet")
}

// ── completion-replay.ts ──

const (
	replayVersion             = 1
	archiveVersion            = 1
	archiveTextLimitBytes     = 64 * 1024
	replayDirName             = "completion-replay"
	archiveDirName            = "output-archives"
	completionCleanupInterval = 60_000
)

var (
	replayCleanupMu      sync.Mutex
	lastCleanupByResults = map[string]int64{}
	// replayScanHook is called with each directory the replay cleanup scans (a test counts them).
	replayScanHook func(dir string)
)

// CompletionArchiveEntry is one reference an archive keeps: a saved output, a session, or a bounded text tail.
type CompletionArchiveEntry struct {
	Agent       string `json:"agent,omitempty"`
	ResultIndex *int   `json:"resultIndex,omitempty"`
	Source      string `json:"source"`
	Path        string `json:"path,omitempty"`
	Text        string `json:"text,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// CompletionArchive references the saved artifacts of a finished run.
type CompletionArchive struct {
	Version   int                      `json:"version"`
	RunID     string                   `json:"runId"`
	CreatedAt ms                       `json:"createdAt"`
	Entries   []CompletionArchiveEntry `json:"entries"`
}

// CompletionReplayRecord is the durable copy of a completion.
type CompletionReplayRecord struct {
	Version     int            `json:"version"`
	RunID       string         `json:"runId"`
	SessionID   string         `json:"sessionId"`
	CompletedAt ms             `json:"completedAt"`
	ExpiresAt   ms             `json:"expiresAt"`
	Completion  WaitCompletion `json:"completion"`
	ArchivePath string         `json:"archivePath"`
}

func safeRunFile(runID string) string { panic("not ported yet") }

func completionReplayPath(resultsDir, runID string) string { panic("not ported yet") }

func completionArchivePath(resultsDir, runID string) string { panic("not ported yet") }

func existingFile(v any) string { panic("not ported yet") }

func intPtr(i int) *int { panic("not ported yet") }

// writeCompletionArchive creates a small archive that references saved child artifacts and keeps only bounded fallback text.
func writeCompletionArchive(resultsDir, runID string, data map[string]any, createdAt int64) (string, error) {
	panic("not ported yet")
}

func parseReplay(data []byte) *CompletionReplayRecord { panic("not ported yet") }

// validateReplayRecord accepts a record only for its own run and the archive path this module derives for it.
func validateReplayRecord(resultsDir, runID string, rec *CompletionReplayRecord) *CompletionReplayRecord {
	panic("not ported yet")
}

func runIDFromReplayFile(file string) string { panic("not ported yet") }

// writeCompletionReplay persists a terminal completion before its one-shot result file is removed.
func writeCompletionReplay(resultsDir, runID, sessionID string, completion WaitCompletion, data map[string]any, now, ttlMs int64) (*CompletionReplayRecord, error) {
	panic("not ported yet")
}

// readCompletionReplay reads a current replay record; an unsafe, foreign or expired one reads as absent. sessionID "" matches any.
func readCompletionReplay(resultsDir, runID, sessionID string, now int64) (*CompletionReplayRecord, error) {
	panic("not ported yet")
}

func readCompletionArchive(archivePath string) (*CompletionArchive, error) { panic("not ported yet") }

func cleanupCompletionReplayIfDue(resultsDir string, now, maxAgeMs, intervalMs int64) bool {
	panic("not ported yet")
}

// cleanupCompletionReplay removes expired replay records and orphan archives without affecting delivery.
func cleanupCompletionReplay(resultsDir string, now, maxAgeMs int64) { panic("not ported yet") }
