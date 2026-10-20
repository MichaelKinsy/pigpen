package tintinweb_subagents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The background record one workflow run lives in: a SubagentWorkflow call returns a task id at once and the run
// continues without it, so the run's state cannot live in the call. Shaped after Claude Code's local_workflow task.
// Every derived counter is recomputed from the append-only log rather than incremented as entries arrive.
// upstream: src/workflow/task.ts.

// workflowRunID is `wf_` + 12 hex, matching Claude Code's `^wf_[a-z0-9-]{6,}$` run ids. upstream: workflowRunId.
func workflowRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "wf_" + hex.EncodeToString(b)
}

// workflowTask is the record of one run. The run goroutine and the readers (tool, fleet, notification) share it, so
// the functions of this file take mu; a test that drives a task alone may set fields directly.
type workflowTask struct {
	mu sync.Mutex

	// Type is the discriminator, alongside Claude Code's local_agent / local_bash.
	Type   string // "local_workflow"
	ID     string
	Status string
	Script string
	// ScriptPath is where the script can be edited and re-run from.
	ScriptPath   string
	Args         any
	HasArgs      bool
	Meta         *workflowMeta
	WorkflowName string
	// ToolCallID is the tool_use_id of the call that started this, when one did.
	ToolCallID string

	// Control is pause, skip and retry once the run is up; absent before the runtime hands it over and after it settles.
	Control workflowControl
	// PausedAt is when the current pause started, so TotalPausedMs can be closed out.
	PausedAt *int64

	// JournalPath is where this run records its own settled calls, for a later resume.
	JournalPath string
	// Replay is a previous run's journal, when this call asked to resume one.
	Replay []workflowJournalEntry
	// ResumedFrom is the run id this one resumed, for the result line that says so.
	ResumedFrom string
	// ReplayedCount is how many agents came back from Replay instead of being spawned.
	ReplayedCount int

	// WorkflowProgress is the append-only event log, in emission order.
	WorkflowProgress []workflowEntry
	// ProgressVersion is bumped once per applied batch, so a renderer can tell nothing changed.
	ProgressVersion int
	AgentCount      int
	// DoneCount is agents that have settled successfully, cached with the other counters (the fleet list asks five
	// times a second).
	DoneCount      int
	TotalTokens    int
	TotalToolCalls int
	Logs           []string

	// Abort cancels the run (and so its children); Ctx is done once it has.
	Ctx           context.Context
	Abort         context.CancelFunc
	StartTime     int64
	EndTime       *int64
	TotalPausedMs int64 // excluded from the elapsed clock the header shows

	// Value is the script's return value, once the run produced one; ValueJSON is the same as the JSON text the
	// script produced (key order kept), nil when the script returned undefined.
	Value     any
	ValueJSON json.RawMessage
	Error     string
	HasError  bool
}

func wfNowMs() int64 { return time.Now().UnixMilli() }

type workflowTaskInit struct {
	ID          string
	Script      string
	ScriptPath  string
	Args        any
	HasArgs     bool
	Meta        *workflowMeta
	ToolCallID  string
	StartTime   int64 // 0 = now
	JournalPath string
	Replay      []workflowJournalEntry
	ResumedFrom string
}

// createWorkflowTask is the record of a run about to start. upstream: createWorkflowTask.
func createWorkflowTask(init workflowTaskInit) *workflowTask {
	ctx, cancel := context.WithCancel(context.Background())
	start := init.StartTime
	if start == 0 {
		start = wfNowMs()
	}
	t := &workflowTask{Type: "local_workflow", ID: init.ID, Status: runRunning, Script: init.Script, ScriptPath: init.ScriptPath,
		Args: init.Args, HasArgs: init.HasArgs, Meta: init.Meta, ToolCallID: init.ToolCallID, JournalPath: init.JournalPath,
		Replay: init.Replay, ResumedFrom: init.ResumedFrom, WorkflowProgress: []workflowEntry{}, Logs: []string{},
		Ctx: ctx, Abort: cancel, StartTime: start}
	if init.Meta != nil {
		t.WorkflowName = init.Meta.Name
	}
	return t
}

// updateWorkflowProgressBatch applies one batch of progress entries: batched because that is how the worker emits
// them, and every counter below is recomputed once per fan-out frame. upstream: updateWorkflowProgressBatch.
func updateWorkflowProgressBatch(task *workflowTask, entries []workflowEntry) {
	if len(entries) == 0 {
		return
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	task.WorkflowProgress = append(task.WorkflowProgress, entries...)
	task.ProgressVersion++
	c := collapseWorkflowProgress(task.WorkflowProgress)
	task.Logs = c.Logs
	// AgentCount is what the runtime has scheduled, which can lead what the log has seen: never walk it backwards.
	if len(c.Agents) > task.AgentCount {
		task.AgentCount = len(c.Agents)
	}
	tokens, calls, done := 0, 0, 0
	for _, a := range c.Agents {
		if a.Tokens != nil {
			tokens += *a.Tokens
		}
		if a.ToolCalls != nil {
			calls += *a.ToolCalls
		}
		// Counted off the collapsed agents, so a re-emitted row counts once.
		if a.State == entryDone {
			done++
		}
	}
	task.TotalTokens, task.TotalToolCalls, task.DoneCount = tokens, calls, done
}

// pauseWorkflowTask holds the run and stops its clock. It reports false when there is no run behind the record or it
// is not running. The control is called without the record's lock: the run reports progress into the record while it
// holds its own. upstream: pauseWorkflowTask.
func pauseWorkflowTask(task *workflowTask, now int64) bool {
	task.mu.Lock()
	if task.Status != runRunning || task.Control == nil {
		task.mu.Unlock()
		return false
	}
	control := task.Control
	task.mu.Unlock()
	// The run is paused outside the task's lock (the control takes the run's own), so the task is re-checked after:
	// a run that settled in between keeps its final status rather than being relabelled paused.
	control.Pause()
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Status != runRunning {
		return false
	}
	task.Status = runPaused
	task.PausedAt = &now
	return true
}

// resumeWorkflowTask lets a paused run go again, banking however long it was held. upstream: resumeWorkflowTask.
func resumeWorkflowTask(task *workflowTask, now int64) bool {
	task.mu.Lock()
	if task.Status != runPaused || task.Control == nil {
		task.mu.Unlock()
		return false
	}
	control := task.Control
	task.mu.Unlock()
	control.Resume()
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Status != runPaused {
		return false
	}
	task.Status = runRunning
	pausedAt := now
	if task.PausedAt != nil {
		pausedAt = *task.PausedAt
	}
	task.TotalPausedMs += max(0, now-pausedAt)
	task.PausedAt = nil
	return true
}

// completeWorkflowTask settles a task from the run's own result. upstream: completeWorkflowTask.
func completeWorkflowTask(task *workflowTask, result *workflowRunResult) {
	task.mu.Lock()
	defer task.mu.Unlock()
	// Banked before the status moves off "paused": a run that finished while held still spent that time held.
	if task.PausedAt != nil {
		task.TotalPausedMs += max(0, wfNowMs()-*task.PausedAt)
		task.PausedAt = nil
	}
	// Nothing left to control: holding the handle would let the dialog offer pause on a stopped run.
	task.Control = nil
	task.Status = result.Status
	if task.Meta == nil {
		meta := result.Meta
		task.Meta = &meta
	}
	if task.WorkflowName == "" {
		task.WorkflowName = result.Meta.Name
	}
	if result.AgentCount > task.AgentCount {
		task.AgentCount = result.AgentCount
	}
	task.ReplayedCount = result.ReplayedCount
	task.Value, task.ValueJSON = result.Value, result.ValueJSON
	task.Error, task.HasError = result.Error, result.HasError
	task.EndTime = new(wfNowMs())
}

// failWorkflowTask settles a task that never produced a result: a script rejected before the worker started (bad
// meta, oversized source, non-JSON args). upstream: failWorkflowTask.
func failWorkflowTask(task *workflowTask, message string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	task.Control = nil
	task.PausedAt = nil
	task.Status = runFailed
	task.Error, task.HasError = message, true
	task.EndTime = new(wfNowMs())
}

// workflowResultText is the run's outcome as text, for the notification and the LLM-facing result. upstream: workflowResultText.
func workflowResultText(task *workflowTask) string {
	task.mu.Lock()
	defer task.mu.Unlock()
	return resultTextLocked(task)
}

func resultTextLocked(task *workflowTask) string {
	if task.HasError {
		return task.Error
	}
	if task.ValueJSON == nil {
		return "No output."
	}
	if s, ok := task.Value.(string); ok {
		return s
	}
	var out bytes.Buffer
	if json.Indent(&out, task.ValueJSON, "", "  ") != nil {
		return string(task.ValueJSON)
	}
	return out.String()
}

// resumeTarget is a resumeFromRunId resolved against the runs this session has seen.
type resumeTarget struct {
	OK          bool
	RunID       string
	JournalPath string
	ScriptPath  string
	Message     string // when !OK
}

// workflowTaskStore holds the runs of a session in the order they started (the original keeps a Map).
type workflowTaskStore struct {
	mu    sync.Mutex
	order []string
	byID  map[string]*workflowTask
}

func newWorkflowTaskStore() *workflowTaskStore {
	return &workflowTaskStore{byID: map[string]*workflowTask{}}
}

func (s *workflowTaskStore) set(task *workflowTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[task.ID]; !ok {
		s.order = append(s.order, task.ID)
	}
	s.byID[task.ID] = task
}

func (s *workflowTaskStore) get(id string) *workflowTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID[id]
}

func (s *workflowTaskStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func (s *workflowTaskStore) values() []*workflowTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*workflowTask, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id])
	}
	return out
}

func (s *workflowTaskStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order, s.byID = nil, map[string]*workflowTask{}
}

// resolveResumeTarget resolves a resumeFromRunId against the runs this session has seen. Same session only: the
// journal lives beside the session's task files, and an unknown id is an error rather than a cold start, because a
// caller that asked to resume is expecting not to pay. nil means no run id was given. upstream: resolveResumeTarget.
func resolveResumeTarget(runID string, tasks *workflowTaskStore) *resumeTarget {
	id := strings.TrimSpace(runID)
	if id == "" {
		return nil
	}
	prior := tasks.get(id)
	if prior == nil {
		known := tasks.keys()
		hint := "Nothing has run yet — call this without `resumeFromRunId`."
		if len(known) > 0 {
			hint = "Runs this session: " + strings.Join(known, ", ") + "."
		}
		return &resumeTarget{Message: fmt.Sprintf(`No workflow run "%s" in this session. %s`, id, hint)}
	}
	prior.mu.Lock()
	defer prior.mu.Unlock()
	if prior.Status == runRunning {
		return &resumeTarget{Message: fmt.Sprintf(`Workflow "%s" is still running. Resume it once it has finished.`, id)}
	}
	if prior.JournalPath == "" {
		return &resumeTarget{Message: fmt.Sprintf(`Workflow "%s" has no journal to resume from.`, id)}
	}
	// ScriptPath is the persisted copy, which is what scriptPath holds when the call had no file of its own.
	return &resumeTarget{OK: true, RunID: id, JournalPath: prior.JournalPath, ScriptPath: prior.ScriptPath}
}

// wfUTF16Prefix is the first n UTF-16 code units of s, as `s.slice(0, n)`. A cut inside a surrogate pair leaves a lone
// high surrogate in JavaScript, which UTF-8 encoding turns into U+FFFD; Go strings hold no lone surrogate, so that
// is what is kept.
func wfUTF16Prefix(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			if units < n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += w
	}
	return s
}

// formatWorkflowNotification is the `<task-notification>`, in the same shape a finished background agent sends.
// upstream: formatWorkflowNotification.
func formatWorkflowNotification(task *workflowTask, now int64) string {
	task.mu.Lock()
	defer task.mu.Unlock()
	totals := progressStats(task.WorkflowProgress, task.AgentCount)
	status := "Error: unknown"
	switch {
	case task.Status == runCompleted:
		status = "Done"
	case task.Status == runKilled:
		status = "Stopped"
	case task.HasError:
		status = "Error: " + task.Error
	}
	result := resultTextLocked(task)
	if wfUTF16Len(result) > 4000 {
		result = wfUTF16Prefix(result, 4000) + "\n...(truncated)"
	}
	name := task.WorkflowName
	if name == "" {
		name = task.ID
	}
	var lines []string
	lines = append(lines, "<task-notification>", "<task-id>"+task.ID+"</task-id>")
	if task.ToolCallID != "" {
		lines = append(lines, "<tool-use-id>"+escapeXML(task.ToolCallID)+"</tool-use-id>")
	}
	if task.ScriptPath != "" {
		lines = append(lines, "<script>"+escapeXML(task.ScriptPath)+"</script>")
	}
	replayed := ""
	if task.ReplayedCount > 0 {
		from := task.ResumedFrom
		if from == "" {
			from = "an earlier run"
		}
		replayed = fmt.Sprintf(", %d replayed from %s", task.ReplayedCount, escapeXML(from))
	}
	lines = append(lines,
		"<status>"+escapeXML(status)+"</status>",
		fmt.Sprintf(`<summary>Workflow "%s" %s — %d/%d agents%s</summary>`, escapeXML(name), task.Status, totals.Done, totals.Total, replayed),
		"<result>"+escapeXML(result)+"</result>",
		fmt.Sprintf("<usage><total_tokens>%d</total_tokens><tool_uses>%d</tool_uses><duration_ms>%d</duration_ms></usage>",
			task.TotalTokens, task.TotalToolCalls, workflowElapsedMs(workflowClock{task.StartTime, task.EndTime, task.TotalPausedMs}, now)),
		"</task-notification>")
	return strings.Join(lines, "\n")
}
