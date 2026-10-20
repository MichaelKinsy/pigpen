package pi_subagents

// Completion records: what bg_wait and the completion notice carry about a finished run, and the durable replay that keeps
// them after the one-shot result file is consumed. Ports completion-replay.ts, wait-completions.ts, the bounded parts of
// workflow-child-summary.ts (parse only) and mutation-evidence.ts (projectTimeoutRecovery, formatTimeoutRecoveryLines),
// and utf8.ts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// ── utf8.ts ──

// utf8Tail is the last maxBytes bytes of value, cut at a character boundary.
func utf8Tail(value string, maxBytes int) (text string, truncated bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	tail := value[len(value)-maxBytes:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return tail, true
}

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
func projectTimeoutRecovery(value any) *TimeoutRecoveryProjection {
	source, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	termination, _ := source["termination"].(string)
	if termination != "timed-out" && termination != "stopped" {
		return nil
	}
	files, ok := source["changedFiles"].([]any)
	if !ok {
		return nil
	}
	var all []string
	for _, f := range files {
		if s, ok := f.(string); ok {
			all = append(all, s)
		}
	}
	changed := all
	if len(changed) > maxTimeoutFiles {
		changed = changed[:maxTimeoutFiles]
	}
	if changed == nil {
		changed = []string{}
	}
	p := &TimeoutRecoveryProjection{Termination: termination, ChangedFiles: changed}
	if t, _ := source["truncated"].(bool); t || len(all) > len(changed) {
		p.Truncated = true
	}
	if r, _ := source["recoveryNeeded"].(bool); r {
		p.RecoveryNeeded = true
	}
	if source["reason"] == "timed-out-with-dirty-worktree" {
		p.Reason = "timed-out-with-dirty-worktree"
	}
	switch rs, _ := source["reportStatus"].(string); rs {
	case "missing", "written", "not-requested", "unknown":
		p.ReportStatus = rs
	}
	return p
}

// formatTimeoutRecoveryLines renders only the bounded recovery route a parent or operator needs.
func formatTimeoutRecoveryLines(value any, indent string) []string {
	r := projectTimeoutRecovery(value)
	if r == nil || !r.RecoveryNeeded {
		return nil
	}
	files, count := "none", "0"
	if len(r.ChangedFiles) > 0 {
		tail := ""
		plus := ""
		if r.Truncated {
			tail, plus = ", …", "+"
		}
		files = strings.Join(r.ChangedFiles, ", ") + tail
		count = fmt.Sprint(len(r.ChangedFiles)) + plus
	}
	report := r.ReportStatus
	if report == "" {
		report = "unknown"
	}
	class := r.Reason
	if class == "" {
		class = r.Termination
	}
	return []string{
		indent + "Recovery needed: review the diff and artifacts before resuming or launching dependent stages.",
		fmt.Sprintf("%sRecovery evidence: requested report: %s; changed tracked files: %s (%s); classification: %s", indent, report, files, count, class),
	}
}

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

func boundedString(v any, maxBytes int) string {
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" || len(s) > maxBytes {
		return ""
	}
	return s
}

func requiredID(v, label string) (string, error) {
	if boundedString(v, maxRequiredIDBytes) == "" {
		return "", fmt.Errorf("%s must be a non-empty identifier of at most %d UTF-8 bytes.", label, maxRequiredIDBytes)
	}
	return v, nil
}

func parseWorkflowActivity(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("workflowChildren child activity is invalid.")
	}
	for k, val := range m {
		if k == "currentTool" {
			if boundedString(val, 256) == "" {
				return nil, errors.New("workflowChildren child activity is invalid.")
			}
			continue
		}
		known := false
		for _, c := range workflowActivityCounters {
			known = known || c == k
		}
		f, isNum := val.(float64)
		if !known || !isNum || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return nil, errors.New("workflowChildren child activity is invalid.")
		}
	}
	return m, nil
}

// parseWorkflowChildSummary validates a persisted summary; nil for an absent value.
func parseWorkflowChildSummary(value any, present bool) (*WorkflowChildSummary, error) {
	if !present || value == nil && !present {
		return nil, nil
	}
	input, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("workflowChildren must be an object.")
	}
	for k := range input {
		switch k {
		case "version", "parentToolCallId", "workflowRunId", "inventoryComplete", "workflowState", "children":
		default:
			return nil, errors.New("workflowChildren has unsupported fields.")
		}
	}
	version, _ := input["version"].(float64)
	_, boolOK := input["inventoryComplete"].(bool)
	rows, rowsOK := input["children"].([]any)
	if version != 1 || !boolOK || !rowsOK {
		return nil, errors.New("workflowChildren is invalid.")
	}
	state, _ := input["workflowState"].(string)
	switch state {
	case "queued", "running", "completed", "failed", "paused", "stopped":
	default:
		return nil, errors.New("workflowChildren.workflowState is invalid.")
	}
	children := []WorkflowChildRow{}
	seen := map[string]bool{}
	for _, r := range rows {
		child, ok := r.(map[string]any)
		if !ok {
			return nil, errors.New("workflowChildren child row is invalid.")
		}
		for k := range child {
			switch k {
			case "childId", "runId", "agent", "sessionName", "model", "thinking", "state", "activity":
			default:
				return nil, errors.New("workflowChildren child row has unsupported fields.")
			}
		}
		childID, _ := child["childId"].(string)
		if !workflowKeyPattern.MatchString(childID) {
			return nil, errors.New("workflowChildren childId is invalid.")
		}
		cs, _ := child["state"].(string)
		switch cs {
		case "pending", "running", "completed", "failed", "paused", "stopped", "rejected", "detached":
		default:
			return nil, errors.New("workflowChildren child state is invalid.")
		}
		for _, f := range []struct {
			name string
			max  int
		}{{"runId", 256}, {"agent", 256}, {"sessionName", 256}, {"model", 256}, {"thinking", 32}} {
			if v, has := child[f.name]; has && v != nil && boundedString(v, f.max) == "" {
				return nil, fmt.Errorf("workflowChildren child %s is invalid.", f.name)
			}
		}
		activity, err := parseWorkflowActivity(child["activity"])
		if err != nil {
			return nil, err
		}
		if activity != nil && cs != "running" {
			return nil, errors.New("workflowChildren child activity requires running state.")
		}
		row := WorkflowChildRow{ChildID: childID, State: cs, Activity: activity}
		row.RunID, _ = child["runId"].(string)
		row.Agent, _ = child["agent"].(string)
		row.SessionName, _ = child["sessionName"].(string)
		row.Model, _ = child["model"].(string)
		row.Thinking, _ = child["thinking"].(string)
		children = append(children, row)
		if seen[childID] {
			return nil, errors.New("workflowChildren has duplicate childId values.")
		}
		seen[childID] = true
	}
	parent, ok := input["parentToolCallId"].(string)
	if !ok {
		return nil, errors.New("workflowChildren.parentToolCallId is invalid.")
	}
	run, ok := input["workflowRunId"].(string)
	if !ok {
		return nil, errors.New("workflowChildren.workflowRunId is invalid.")
	}
	if _, err := requiredID(parent, "workflowChildren.parentToolCallId"); err != nil {
		return nil, err
	}
	if _, err := requiredID(run, "workflowChildren.workflowRunId"); err != nil {
		return nil, err
	}
	inv, _ := input["inventoryComplete"].(bool)
	return &WorkflowChildSummary{Version: 1, ParentToolCallID: parent, WorkflowRunID: run, InventoryComplete: inv, WorkflowState: state, Children: children}, nil
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

func nonNegativeNumber(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}

func projectedUsage(v any) *Usage {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var vals [6]float64
	for i, k := range []string{"input", "output", "cacheRead", "cacheWrite", "cost", "turns"} {
		f, ok := nonNegativeNumber(m[k])
		if !ok {
			return nil
		}
		vals[i] = f
	}
	if vals[5] != math.Trunc(vals[5]) || vals[5] > 1<<53-1 {
		return nil
	}
	return &Usage{Input: vals[0], Output: vals[1], CacheRead: vals[2], CacheWrite: vals[3], Cost: vals[4], Turns: int64(vals[5])}
}

// projectStructuredOutput keeps a value of at most 4 KiB serialized, drops a larger one, and rejects what is not JSON.
func projectStructuredOutput(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, errors.New("Structured output must be JSON-serializable")
	}
	if len(b) > structuredOutputInlineLimitBytes {
		return nil, nil
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

// toWaitCompletion projects a terminal result payload into the slim shape that is safe in tool_result details.
func toWaitCompletion(data map[string]any, runID string) (WaitCompletion, error) {
	var results []WaitCompletionChild
	if entries, ok := data["results"].([]any); ok {
		for _, e := range entries {
			child, ok := e.(map[string]any)
			if !ok {
				continue
			}
			c := WaitCompletionChild{Agent: nonEmptyString(child["agent"]), RunID: nonEmptyString(child["runId"]), Usage: projectedUsage(child["usage"]),
				SessionFile: nonEmptyString(child["sessionFile"]), StructuredOutputPath: nonEmptyString(child["structuredOutputPath"]),
				Error: nonEmptyString(child["error"]), Model: nonEmptyString(child["model"]), TimeoutRecovery: projectTimeoutRecovery(child["timeoutRecovery"])}
			if b, ok := child["success"].(bool); ok {
				c.Success = &b
			}
			switch os, _ := child["outputState"].(string); os {
			case "present", "absent", "unknown":
				c.OutputState = os
			}
			so, err := projectStructuredOutput(child["structuredOutput"])
			if err != nil {
				return WaitCompletion{}, err
			}
			c.StructuredOutput = so
			c.ContextOverflow, _ = child["contextOverflow"].(bool)
			if ap, ok := child["artifactPaths"].(map[string]any); ok {
				c.ArtifactPaths = ap
			}
			results = append(results, c)
		}
	}
	comp := WaitCompletion{RunID: runID, Agent: nonEmptyString(data["agent"]), Mode: nonEmptyString(data["mode"]), State: nonEmptyString(data["state"])}
	if receipt, ok := data["workflowReceipt"].(map[string]any); ok {
		comp.WorkflowReceiptPath = nonEmptyString(receipt["path"])
	}
	wc, present := data["workflowChildren"]
	summary, err := parseWorkflowChildSummary(wc, present)
	if err != nil {
		return WaitCompletion{}, err
	}
	if summary != nil && summary.WorkflowRunID != runID {
		return WaitCompletion{}, errors.New("workflowChildren.workflowRunId does not match its completion run id.")
	}
	comp.WorkflowChildren = summary
	if b, ok := data["success"].(bool); ok {
		comp.Success = &b
	}
	if len(results) > 0 {
		comp.Results = results
	}
	return comp, nil
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

func safeRunFile(runID string) string {
	enc, _ := jsEncodeURIComponent(runID)
	return enc + ".json"
}

func completionReplayPath(resultsDir, runID string) string {
	return filepath.Join(resultsDir, replayDirName, safeRunFile(runID))
}

func completionArchivePath(resultsDir, runID string) string {
	return filepath.Join(resultsDir, archiveDirName, safeRunFile(runID))
}

func existingFile(v any) string {
	p := nonEmptyString(v)
	if p == "" {
		return ""
	}
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return p
	}
	return ""
}

func intPtr(i int) *int { return &i }

// writeCompletionArchive creates a small archive that references saved child artifacts and keeps only bounded fallback text.
func writeCompletionArchive(resultsDir, runID string, data map[string]any, createdAt int64) (string, error) {
	entries := []CompletionArchiveEntry{}
	results, _ := data["results"].([]any)
	for i, v := range results {
		child, ok := v.(map[string]any)
		if !ok {
			continue
		}
		agent := nonEmptyString(child["agent"])
		if ap, ok := child["artifactPaths"].(map[string]any); ok {
			if p := existingFile(ap["outputPath"]); p != "" {
				entries = append(entries, CompletionArchiveEntry{Agent: agent, ResultIndex: intPtr(i), Source: "output-artifact", Path: p})
				continue
			}
		}
		if p := existingFile(child["sessionFile"]); p != "" {
			entries = append(entries, CompletionArchiveEntry{Agent: agent, ResultIndex: intPtr(i), Source: "session", Path: p})
			continue
		}
		output, errText := nonEmptyString(child["output"]), nonEmptyString(child["error"])
		if output != "" || errText != "" {
			var parts []string
			if errText != "" {
				parts = append(parts, "Error: "+errText)
			}
			if output != "" {
				parts = append(parts, output)
			}
			text, truncated := utf8Tail(strings.Join(parts, "\n"), archiveTextLimitBytes)
			entries = append(entries, CompletionArchiveEntry{Agent: agent, ResultIndex: intPtr(i), Source: "result-tail", Text: text, Truncated: truncated})
		}
	}
	if len(results) == 0 {
		if p := existingFile(data["sessionFile"]); p != "" {
			entries = append(entries, CompletionArchiveEntry{Source: "session", Path: p})
		}
	}
	if len(entries) == 0 {
		if summary := nonEmptyString(data["summary"]); summary != "" {
			text, truncated := utf8Tail(summary, archiveTextLimitBytes)
			entries = append(entries, CompletionArchiveEntry{Source: "result-tail", Text: text, Truncated: truncated})
		}
	}
	archivePath := completionArchivePath(resultsDir, runID)
	if err := asyncWritePrivateJSON(archivePath, CompletionArchive{Version: archiveVersion, RunID: runID, CreatedAt: ms(createdAt), Entries: entries}); err != nil {
		return "", err
	}
	return archivePath, nil
}

func parseReplay(data []byte) *CompletionReplayRecord {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	v, _ := raw["version"].(float64)
	_, a := raw["runId"].(string)
	_, b := raw["sessionId"].(string)
	_, c := raw["completedAt"].(float64)
	_, d := raw["expiresAt"].(float64)
	_, e := raw["archivePath"].(string)
	if v != replayVersion || !a || !b || !c || !d || !e {
		return nil
	}
	var rec CompletionReplayRecord
	if json.Unmarshal(data, &rec) != nil {
		return nil
	}
	if cm, ok := raw["completion"].(map[string]any); !ok || cm["runId"] != rec.RunID {
		return nil
	}
	return &rec
}

// validateReplayRecord accepts a record only for its own run and the archive path this module derives for it.
func validateReplayRecord(resultsDir, runID string, rec *CompletionReplayRecord) *CompletionReplayRecord {
	if rec.RunID != runID {
		return nil
	}
	archive := completionArchivePath(resultsDir, runID)
	a, _ := filepath.Abs(rec.ArchivePath)
	b, _ := filepath.Abs(archive)
	if a != b {
		return nil
	}
	out := *rec
	out.ArchivePath = archive
	out.Completion.ArchivePath = archive
	return &out
}

func runIDFromReplayFile(file string) string {
	if !strings.HasSuffix(file, ".json") {
		return ""
	}
	id, err := url.PathUnescape(strings.TrimSuffix(file, ".json"))
	if err != nil || safeRunFile(id) != file {
		return ""
	}
	return id
}

// writeCompletionReplay persists a terminal completion before its one-shot result file is removed.
func writeCompletionReplay(resultsDir, runID, sessionID string, completion WaitCompletion, data map[string]any, now, ttlMs int64) (*CompletionReplayRecord, error) {
	archivePath, err := writeCompletionArchive(resultsDir, runID, data, now)
	if err != nil {
		return nil, err
	}
	completion.ArchivePath = archivePath
	rec := &CompletionReplayRecord{Version: replayVersion, RunID: runID, SessionID: sessionID, CompletedAt: ms(now), ExpiresAt: ms(now + ttlMs), Completion: completion, ArchivePath: archivePath}
	if err := asyncWritePrivateJSON(completionReplayPath(resultsDir, runID), rec); err != nil {
		return nil, err
	}
	cleanupCompletionReplayIfDue(resultsDir, now, ttlMs, completionCleanupInterval)
	return rec, nil
}

// readCompletionReplay reads a current replay record; an unsafe, foreign or expired one reads as absent. sessionID "" matches any.
func readCompletionReplay(resultsDir, runID, sessionID string, now int64) (*CompletionReplayRecord, error) {
	replayPath := completionReplayPath(resultsDir, runID)
	data, err := os.ReadFile(replayPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	rec := parseReplay(data)
	if rec == nil {
		return nil, nil
	}
	safe := validateReplayRecord(resultsDir, runID, rec)
	if safe == nil {
		_ = os.Remove(replayPath)
		return nil, nil
	}
	if sessionID != "" && safe.SessionID != sessionID {
		return nil, nil
	}
	if now == 0 {
		now = nowMs()
	}
	if int64(safe.ExpiresAt) <= now {
		_ = os.Remove(replayPath)
		_ = os.Remove(safe.ArchivePath)
		return nil, nil
	}
	return safe, nil
}

func readCompletionArchive(archivePath string) (*CompletionArchive, error) {
	data, err := os.ReadFile(archivePath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var a CompletionArchive
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(data, &a) != nil || a.Version != archiveVersion || raw["entries"] == nil {
		return nil, errors.New("Completion archive is malformed.")
	}
	kept := []CompletionArchiveEntry{}
	for _, e := range a.Entries {
		switch e.Source {
		case "output-artifact", "session", "result-tail":
			kept = append(kept, e)
		}
	}
	a.Entries = kept
	return &a, nil
}

func cleanupCompletionReplayIfDue(resultsDir string, now, maxAgeMs, intervalMs int64) bool {
	replayCleanupMu.Lock()
	last, seen := lastCleanupByResults[resultsDir]
	if seen && now-last < intervalMs {
		replayCleanupMu.Unlock()
		return false
	}
	lastCleanupByResults[resultsDir] = now
	replayCleanupMu.Unlock()
	cleanupCompletionReplay(resultsDir, now, maxAgeMs)
	return true
}

// cleanupCompletionReplay removes expired replay records and orphan archives without affecting delivery.
func cleanupCompletionReplay(resultsDir string, now, maxAgeMs int64) {
	replayDir := filepath.Join(resultsDir, replayDirName)
	if replayScanHook != nil {
		replayScanHook(replayDir)
	}
	if files, err := os.ReadDir(replayDir); err == nil {
		for _, f := range files {
			runID := runIDFromReplayFile(f.Name())
			if runID == "" {
				continue
			}
			filePath := filepath.Join(replayDir, f.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}
			rec := parseReplay(data)
			var safe *CompletionReplayRecord
			if rec != nil {
				safe = validateReplayRecord(resultsDir, runID, rec)
			}
			switch {
			case rec != nil && safe == nil:
				_ = os.Remove(filePath)
			case safe != nil && int64(safe.ExpiresAt) <= now:
				_ = os.Remove(filePath)
				_ = os.Remove(safe.ArchivePath)
			case rec == nil:
				if st, err := os.Stat(filePath); err == nil && now-st.ModTime().UnixMilli() > maxAgeMs {
					_ = os.Remove(filePath)
				}
			}
		}
	}
	archiveDir := filepath.Join(resultsDir, archiveDirName)
	if files, err := os.ReadDir(archiveDir); err == nil {
		for _, f := range files {
			p := filepath.Join(archiveDir, f.Name())
			if st, err := os.Stat(p); err == nil && now-st.ModTime().UnixMilli() > maxAgeMs {
				_ = os.Remove(p)
			}
		}
	}
}
