package tintinweb_subagents

import (
	"fmt"
	"sort"
	"strings"
)

// The workflow progress model, ported from Claude Code. Progress is an append-only event log, not a tree: agent entries
// are keyed by index and last-write-wins, and every view derives its shape by collapsing the log. upstream:
// src/workflow/progress.ts.

// workflowEntry is one line of the log: a *workflowPhaseEntry, *workflowLogEntry or *workflowAgentEntry.
type workflowEntry interface{ entryType() string }

type workflowPhaseEntry struct {
	Type  string `json:"type"` // "workflow_phase"
	Index int    `json:"index"`
	Title string `json:"title"`
}

type workflowLogEntry struct {
	Type    string `json:"type"` // "workflow_log"
	Message string `json:"message"`
}

func (*workflowPhaseEntry) entryType() string { return "workflow_phase" }
func (*workflowLogEntry) entryType() string   { return "workflow_log" }
func (*workflowAgentEntry) entryType() string { return "workflow_agent" }

func newPhaseEntry(index int, title string) *workflowPhaseEntry {
	return &workflowPhaseEntry{Type: "workflow_phase", Index: index, Title: title}
}

func newLogEntry(message string) *workflowLogEntry {
	return &workflowLogEntry{Type: "workflow_log", Message: message}
}

// Raw entry lifecycle, as written by the runtime. upstream: WorkflowEntryState.
const (
	entryStart    = "start"
	entryProgress = "progress"
	entryDone     = "done"
	entryError    = "error"
)

// Derived per-agent state, as rendered. upstream: WorkflowDisplayState.
const (
	displayQueued      = "queued"
	displayRunning     = "running"
	displayDone        = "done"
	displayFailed      = "failed"
	displaySkipped     = "skipped"
	displayBlocked     = "blocked"
	displayInterrupted = "interrupted"
)

// workflowAgentEntry is an agent's row. Optional numbers are pointers (a zero is a value). upstream: WorkflowAgentEntry.
type workflowAgentEntry struct {
	Type  string `json:"type"` // "workflow_agent"
	Index int    `json:"index"`
	Label string `json:"label"`
	// PhaseIndex is absent when the agent ran before any phase() call: that, not a default of 0, turns the run into one "Agents" group.
	PhaseIndex *int   `json:"phaseIndex,omitempty"`
	PhaseTitle string `json:"phaseTitle,omitempty"`
	State      string `json:"state"`
	AgentID    string `json:"agentId,omitempty"`
	// RecordID is the manager's record id once the child has one (AgentID is the run's own wf-agent-N handle).
	RecordID  string `json:"recordId,omitempty"`
	AgentType string `json:"agentType,omitempty"`
	// Model is the short model label; seeded from the request, then replaced by what the child ran on (#168).
	Model   string `json:"model,omitempty"`
	ModelID string `json:"modelId,omitempty"`
	// Thinking is the level in effect; RequestedThinking/RequestedModel are kept only when the call did not get them (#182).
	Thinking          string `json:"thinking,omitempty"`
	RequestedThinking string `json:"requestedThinking,omitempty"`
	RequestedModel    string `json:"requestedModel,omitempty"`
	FallbackModel     string `json:"fallbackModel,omitempty"`
	Isolation         string `json:"isolation,omitempty"`
	Error             string `json:"error,omitempty"`
	Skipped           bool   `json:"skipped,omitempty"`
	Blocked           bool   `json:"blocked,omitempty"`
	Cached            bool   `json:"cached,omitempty"`
	QueuedAt          *int64 `json:"queuedAt,omitempty"`
	StartedAt         *int64 `json:"startedAt,omitempty"`
	LastProgressAt    *int64 `json:"lastProgressAt,omitempty"`
	Attempt           int    `json:"attempt,omitempty"`
	// LastAttemptReason is "throttled", "user-retry" or "stalled".
	LastAttemptReason string `json:"lastAttemptReason,omitempty"`
	PromptPreview     string `json:"promptPreview,omitempty"`
	ResultPreview     string `json:"resultPreview,omitempty"`
	Tokens            *int   `json:"tokens,omitempty"`
	ToolCalls         *int   `json:"toolCalls,omitempty"`
	DurationMs        *int64 `json:"durationMs,omitempty"`
}

// Overall run status, mirroring the task record. upstream: WorkflowRunStatus.
const (
	runRunning   = "running"
	runCompleted = "completed"
	runFailed    = "failed"
	runKilled    = "killed"
	runPaused    = "paused"
)

type collapsedProgress struct {
	Agents      []*workflowAgentEntry
	Logs        []string
	PhaseTitles map[int]string
}

type phaseGroup struct {
	Title      string
	Status     string // not-started, running, done, failed
	Agents     []*workflowAgentEntry
	DoneCount  int
	TotalCount int
	Tokens     int
	DurationMs int64
}

type workflowStats struct {
	Done        int
	FailedCount int
	Running     bool
	Total       int
	Started     int
	Complete    bool
}

// collapseWorkflowProgress folds the event log into its latest state: agent entries by index (last write wins), logs in order, phase
// titles as a lookup. upstream: collapse.
func collapseWorkflowProgress(progress []workflowEntry) collapsedProgress {
	byIndex := map[int]*workflowAgentEntry{}
	logs := []string{}
	titles := map[int]string{}
	for _, entry := range progress {
		switch e := entry.(type) {
		case *workflowAgentEntry:
			byIndex[e.Index] = e
		case *workflowLogEntry:
			logs = append(logs, e.Message)
		case *workflowPhaseEntry:
			titles[e.Index] = e.Title
		}
	}
	agents := make([]*workflowAgentEntry, 0, len(byIndex))
	for _, a := range byIndex {
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Index < agents[j].Index })
	return collapsedProgress{Agents: agents, Logs: logs, PhaseTitles: titles}
}

// workflowDisplayState derives what to render for one agent. workflowActive is false once the run has stopped: anything still
// mid-flight was cut off rather than finished, hence "interrupted". upstream: displayState.
func workflowDisplayState(entry *workflowAgentEntry, workflowActive bool) string {
	if entry.State == entryDone {
		return displayDone
	}
	if entry.State == entryError {
		if entry.Skipped {
			return displaySkipped
		}
		if entry.Blocked {
			return displayBlocked
		}
		return displayFailed
	}
	if !workflowActive {
		return displayInterrupted
	}
	// Queued means accepted but never given a slot. An entry with no queuedAt at all predates the semaphore.
	if entry.QueuedAt != nil && entry.StartedAt == nil {
		return displayQueued
	}
	return displayRunning
}

// workflowEntryLive is true while an entry is still expected to change. upstream: isLive.
func workflowEntryLive(entry *workflowAgentEntry) bool {
	return entry.State == entryStart || entry.State == entryProgress
}

type observedPhase struct {
	phaseIndex int
	title      string
	agents     []*workflowAgentEntry
}

// groupAgentsByPhase buckets agents by phase; nil when no agent declared a phase. upstream: groupByPhase.
func groupAgentsByPhase(agents []*workflowAgentEntry, phaseTitles map[int]string) []*observedPhase {
	any := false
	for _, a := range agents {
		if a.PhaseIndex != nil {
			any = true
		}
	}
	if !any {
		return nil
	}
	byPhase := map[int]*observedPhase{}
	for _, a := range agents {
		idx := 0
		if a.PhaseIndex != nil {
			idx = *a.PhaseIndex
		}
		g := byPhase[idx]
		if g == nil {
			title, ok := phaseTitles[idx]
			if !ok {
				title = fmt.Sprintf("Phase %d", idx)
			}
			g = &observedPhase{phaseIndex: idx, title: title}
			byPhase[idx] = g
		}
		g.agents = append(g.agents, a)
	}
	out := make([]*observedPhase, 0, len(byPhase))
	for _, g := range byPhase {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].phaseIndex < out[j].phaseIndex })
	return out
}

// summarizePhase rolls a phase's agents up into the counts and totals its header shows. upstream: summarize.
func summarizePhase(title string, agents []*workflowAgentEntry) phaseGroup {
	done, failed, tokens := 0, 0, 0
	var minStart, maxProgress int64
	haveStart := false
	for _, a := range agents {
		if a.State == entryDone {
			done++
		} else if a.State == entryError {
			failed++
		}
		if a.Tokens != nil {
			tokens += *a.Tokens
		}
		if a.StartedAt != nil {
			if !haveStart || *a.StartedAt < minStart {
				minStart, haveStart = *a.StartedAt, true
			}
			last := *a.StartedAt
			if a.LastProgressAt != nil {
				last = *a.LastProgressAt
			}
			if last > maxProgress {
				maxProgress = last
			}
		}
	}
	total := len(agents)
	finished := done+failed == total && total > 0
	status := "running"
	if finished {
		status = "done"
		if failed > 0 {
			status = "failed"
		}
	}
	var duration int64
	if haveStart {
		// Wall-clock across the phase, not the sum of its agents: they overlap.
		duration = maxProgress - minStart
	}
	return phaseGroup{Title: title, Status: status, Agents: agents, DoneCount: done, TotalCount: total, Tokens: tokens, DurationMs: duration}
}

func placeholderWorkflowPhase(title string) phaseGroup {
	return phaseGroup{Title: title, Status: "not-started"}
}

func normalizeWorkflowPhaseTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

// mergeWorkflowPhases reconciles the phases declared in meta with the phases actually observed: matching is fuzzy (either
// title a prefix of the other), each observed group is consumed at most once, declared-but-unseen phases are
// not-started placeholders and observed groups with no declaration follow. upstream: mergePhases.
func mergeWorkflowPhases(declared []workflowPhaseMeta, observed []*observedPhase) []phaseGroup {
	consumed := map[*observedPhase]bool{}
	var merged []phaseGroup
	for _, phase := range declared {
		wanted := normalizeWorkflowPhaseTitle(phase.Title)
		var match *observedPhase
		for _, g := range observed {
			if consumed[g] {
				continue
			}
			actual := normalizeWorkflowPhaseTitle(g.title)
			if actual == wanted || strings.HasPrefix(actual, wanted) || strings.HasPrefix(wanted, actual) {
				match = g
				break
			}
		}
		if match != nil {
			consumed[match] = true
			merged = append(merged, summarizePhase(match.title, match.agents))
		} else {
			merged = append(merged, placeholderWorkflowPhase(phase.Title))
		}
	}
	for _, g := range observed {
		if !consumed[g] {
			merged = append(merged, summarizePhase(g.title, g.agents))
		}
	}
	return merged
}

// buildPhaseGroups builds the phase groups a renderer walks. When nothing declared or emitted a phase every agent
// collapses into a single "Agents" group. upstream: buildPhaseGroups.
func buildPhaseGroups(progress []workflowEntry, declared []workflowPhaseMeta) []phaseGroup {
	c := collapseWorkflowProgress(progress)
	merged := mergeWorkflowPhases(declared, groupAgentsByPhase(c.Agents, c.PhaseTitles))
	if len(merged) == 0 && len(c.Agents) > 0 {
		return []phaseGroup{summarizePhase("Agents", c.Agents)}
	}
	// A run that declared phases but produced un-phased agents would otherwise render placeholders and drop those
	// agents from the tree entirely (Claude Code has the same hole; showing the work is the small divergence).
	if len(c.Agents) > 0 {
		anyTotal := false
		for _, g := range merged {
			if g.TotalCount > 0 {
				anyTotal = true
			}
		}
		if !anyTotal {
			return append(merged, summarizePhase("Agents", c.Agents))
		}
	}
	return merged
}

// progressStats are the aggregate counts for the header line. agentCount is the number the runtime has scheduled,
// which can exceed the number that has emitted an entry. upstream: stats.
func progressStats(progress []workflowEntry, agentCount int) workflowStats {
	seen, done, failed, started := 0, 0, 0, 0
	anyLive := false
	for _, entry := range progress {
		e, ok := entry.(*workflowAgentEntry)
		if !ok {
			continue
		}
		seen++
		switch e.State {
		case entryDone:
			done++
			started++
		case entryError:
			failed++
			started++
		default:
			anyLive = true
			// Counted as started unless it is provably still waiting for a slot.
			if e.StartedAt != nil || e.QueuedAt == nil {
				started++
			}
		}
	}
	total := agentCount
	if seen > total {
		total = seen
	}
	return workflowStats{Done: done, FailedCount: failed, Running: anyLive, Total: total, Started: started,
		Complete: !anyLive && seen > 0 && done+failed >= total}
}

// workflowClock is the part of a task the elapsed clock reads.
type workflowClock struct {
	StartTime     int64
	EndTime       *int64
	TotalPausedMs int64
}

// workflowElapsedMs is the elapsed run time, excluding any time spent paused. upstream: elapsedMs.
func workflowElapsedMs(task workflowClock, now int64) int64 {
	end := now
	if task.EndTime != nil {
		end = *task.EndTime
	}
	if d := end - task.StartTime - task.TotalPausedMs; d > 0 {
		return d
	}
	return 0
}

func wfPlural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// workflowFormatDuration is `1m12s` / `9s` / `340ms`. upstream: formatDuration.
func workflowFormatDuration(ms float64) string {
	if ms < 1000 {
		r := wfJSRound(ms)
		if r < 0 {
			r = 0
		}
		return fmt.Sprintf("%dms", int64(r))
	}
	totalSeconds := int64(wfJSRound(ms / 1000))
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if minutes == 0 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

// wfJSRound is Math.round: halves round toward +Infinity.
func wfJSRound(f float64) float64 {
	r := float64(int64(f))
	if f-r >= 0.5 {
		return r + 1
	}
	if f < 0 && r-f > 0.5 {
		return r - 1
	}
	return r
}

type workflowHeaderTask struct {
	Status        string
	WorkflowName  string
	Summary       string
	Description   string
	StartTime     int64
	EndTime       *int64
	TotalPausedMs int64
}

type workflowHeader struct{ Name, Subtext, Stats string }

// workflowHeaderLine is the one-line summary above the tree: `3/7 agents · 1m12s`, plus a terminal suffix once the
// run stops. upstream: header.
func workflowHeaderLine(task workflowHeaderTask, meta *workflowMeta, groups []phaseGroup, agentCount int, now int64) workflowHeader {
	suffix := ""
	switch task.Status {
	case runCompleted:
		suffix = " · done"
	case runKilled:
		suffix = " · stopped"
	case runPaused:
		suffix = " · paused"
	case runFailed:
		suffix = " · failed"
	}
	doneAgents, totalAgents := 0, 0
	for _, g := range groups {
		doneAgents += g.DoneCount
		totalAgents += g.TotalCount
	}
	for _, n := range []int{agentCount, doneAgents} {
		if n > totalAgents {
			totalAgents = n
		}
	}
	first := func(values ...string) string {
		for _, v := range values {
			if v != "" {
				return v
			}
		}
		return ""
	}
	metaName, metaDescription := "", ""
	if meta != nil {
		metaName, metaDescription = meta.Name, meta.Description
	}
	return workflowHeader{
		Name:    first(task.WorkflowName, metaName, task.Summary, task.Description, "workflow"),
		Subtext: first(metaDescription, task.Description, task.Summary),
		Stats: fmt.Sprintf("%d/%d %s · %s%s", doneAgents, totalAgents, wfPlural(totalAgents, "agent"),
			workflowFormatDuration(float64(workflowElapsedMs(workflowClock{task.StartTime, task.EndTime, task.TotalPausedMs}, now))), suffix),
	}
}

const (
	defaultAgentCap       = 25
	defaultTokenCap       = 1_500_000
	assumedTokensPerAgent = 70_000 // spend per agent assumed before any has reported, for the projection
	footerPhaseTitleWidth = 16
)

type workflowSizeWarningInput struct {
	ScheduledAgents, StartedAgents, TotalTokens int
	AgentCap, TokenCap                          int // 0 = the default
}

type workflowSizeWarningResult struct {
	Axis                                                              string // agents, tokens or both
	ScheduledAgents, TotalTokens, ProjectedTokens, AgentCap, TokenCap int
}

// workflowSizeWarning warns when a run is about to get expensive. The projection matters more than the current
// total. nil means no warning. upstream: sizeWarning.
func workflowSizeWarning(in workflowSizeWarningInput) *workflowSizeWarningResult {
	agentCap, tokenCap := in.AgentCap, in.TokenCap
	if agentCap == 0 {
		agentCap = defaultAgentCap
	}
	if tokenCap == 0 {
		tokenCap = defaultTokenCap
	}
	perAgent := float64(assumedTokensPerAgent)
	if in.StartedAgents > 0 {
		perAgent = float64(in.TotalTokens) / float64(in.StartedAgents)
	}
	projected := int(wfJSRound(perAgent * float64(in.ScheduledAgents)))
	if in.TotalTokens > projected {
		projected = in.TotalTokens
	}
	overAgents := in.ScheduledAgents > agentCap
	overTokens := in.TotalTokens > tokenCap || projected > tokenCap
	if !overAgents && !overTokens {
		return nil
	}
	axis := "tokens"
	switch {
	case overAgents && overTokens:
		axis = "both"
	case overAgents:
		axis = "agents"
	}
	return &workflowSizeWarningResult{Axis: axis, ScheduledAgents: in.ScheduledAgents, TotalTokens: in.TotalTokens, ProjectedTokens: projected, AgentCap: agentCap, TokenCap: tokenCap}
}

// Words whose gerund is irregular, or which read better left alone ("" = leave alone). upstream: GERUND_OVERRIDES.
var workflowGerundOverrides = map[string]string{"commit": "committing", "submit": "submitting", "format": "formatting", "setup": "", "cleanup": ""}

func isWorkflowGerundCandidate(word string) bool {
	if n := len(word); n < 3 || n > 12 {
		return false
	}
	for _, r := range word {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// workflowGerund renders a phase title as an activity: `Scan` → `Scanning`. Anything that is not a plain short word is left
// untouched. upstream: gerund.
func workflowGerund(word string) string {
	if !isWorkflowGerundCandidate(word) {
		return word
	}
	lower := strings.ToLower(word)
	if override, ok := workflowGerundOverrides[lower]; ok {
		if override == "" {
			return word
		}
		return word[:1] + override[1:]
	}
	switch {
	case strings.HasSuffix(lower, "ing"):
		return word
	case strings.HasSuffix(lower, "ie"):
		return word[:len(word)-2] + "ying"
	case strings.HasSuffix(lower, "e") && !strings.HasSuffix(lower, "ee") && !strings.HasSuffix(lower, "ye"):
		return word[:len(word)-1] + "ing"
	}
	// Short consonant-vowel-consonant words double the final consonant: run → running. w, x and y never double.
	n := len(lower)
	last := lower[n-1]
	isVowel := func(b byte) bool { return strings.IndexByte("aeiou", b) >= 0 }
	if n <= 4 && !isVowel(lower[n-3]) && isVowel(lower[n-2]) && !isVowel(last) && !strings.ContainsRune("wxy", rune(last)) {
		return word + string(last) + "ing"
	}
	return word + "ing"
}

// wfTruncateUnits cuts text to width UTF-16 code units, ending in an ellipsis. upstream: truncate.
func wfTruncateUnits(text string, width int) string {
	if wfUTF16Len(text) <= width {
		return text
	}
	keep := width - 1
	if keep < 1 {
		keep = 1
	}
	var b strings.Builder
	n := 0
	for _, r := range text {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if n+w > keep {
			break
		}
		b.WriteRune(r)
		n += w
	}
	return b.String() + "…"
}

type footerPhaseInput struct {
	Titles        []string
	PositionStart int
	TotalPhases   int
}

// footerPhaseLabel is the footer's "what is this run doing" label: one active phase shows its position, concurrent
// phases are joined. upstream: footerPhaseLabel.
func footerPhaseLabel(in footerPhaseInput) string {
	titles := make([]string, len(in.Titles))
	for i, t := range in.Titles {
		titles[i] = workflowGerund(t)
	}
	if len(titles) == 0 {
		return ""
	}
	if len(titles) == 1 {
		return fmt.Sprintf("%s (%d/%d)", wfTruncateUnits(titles[0], footerPhaseTitleWidth), in.PositionStart, in.TotalPhases)
	}
	parts := make([]string, len(titles))
	for i, t := range titles {
		parts[i] = wfTruncateUnits(t, footerPhaseTitleWidth)
	}
	return strings.Join(parts, " & ")
}
