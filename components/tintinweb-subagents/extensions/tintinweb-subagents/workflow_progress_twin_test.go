package tintinweb_subagents

import (
	"strconv"
	"testing"
)

// Twins of test/workflow-progress.test.ts.

// wfAgentRow is agentEntry: phase 0 by default, label agent-N, state start.
func wfAgentRow(index int, mod ...func(*workflowAgentEntry)) *workflowAgentEntry {
	e := &workflowAgentEntry{Type: "workflow_agent", Index: index, Label: "agent-" + wfItoa(index), PhaseIndex: new(0), State: entryStart}
	for _, m := range mod {
		m(e)
	}
	return e
}

// wfUnphasedRow is an agent emitted with no ambient phase.
func wfUnphasedRow(index int, mod ...func(*workflowAgentEntry)) *workflowAgentEntry {
	e := wfAgentRow(index, mod...)
	e.PhaseIndex, e.PhaseTitle = nil, ""
	return e
}

func wfItoa(n int) string { return strconv.Itoa(n) }

func wfRowState(s string) func(*workflowAgentEntry) {
	return func(e *workflowAgentEntry) { e.State = s }
}
func wfRowPhase(i int) func(*workflowAgentEntry) {
	return func(e *workflowAgentEntry) { e.PhaseIndex = new(i) }
}

func wfEntries(es ...workflowEntry) []workflowEntry { return es }

func wfTitlesOf(gs []phaseGroup) []string {
	out := []string{}
	for _, g := range gs {
		out = append(out, g.Title)
	}
	return out
}

func TestWorkflowProgress(t *testing.T) {
	const f = "workflow-progress"
	// collapseWorkflowProgress
	tw(t, f, "keeps the last entry written for an index", func(t *testing.T) {
		c := collapseWorkflowProgress(wfEntries(
			wfAgentRow(0, wfRowState("start"), func(e *workflowAgentEntry) { e.Label = "first" }),
			wfAgentRow(0, wfRowState("done"), func(e *workflowAgentEntry) { e.Label = "first"; e.ResultPreview = "ok" })))
		eq(t, len(c.Agents), 1)
		eq(t, c.Agents[0].State, "done")
		eq(t, c.Agents[0].ResultPreview, "ok")
	})
	tw(t, f, "orders agents by index regardless of arrival order", func(t *testing.T) {
		c := collapseWorkflowProgress(wfEntries(wfAgentRow(2), wfAgentRow(0), wfAgentRow(1)))
		eq(t, []int{c.Agents[0].Index, c.Agents[1].Index, c.Agents[2].Index}, []int{0, 1, 2})
	})
	tw(t, f, "accumulates logs in order and indexes phase titles", func(t *testing.T) {
		c := collapseWorkflowProgress(wfEntries(newLogEntry("first"), newPhaseEntry(0, "Scan"), newLogEntry("second"), newPhaseEntry(1, "Verify")))
		eq(t, c.Logs, []string{"first", "second"})
		eq(t, c.PhaseTitles[0], "Scan")
		eq(t, c.PhaseTitles[1], "Verify")
	})
	// workflowDisplayState
	for _, c := range []struct {
		name   string
		mod    func(*workflowAgentEntry)
		active bool
		want   string
	}{
		{"done wins outright", wfRowState("done"), true, "done"},
		{"error + skipped", func(e *workflowAgentEntry) { e.State, e.Skipped = "error", true }, true, "skipped"},
		{"error + blocked", func(e *workflowAgentEntry) { e.State, e.Blocked = "error", true }, true, "blocked"},
		{"bare error", wfRowState("error"), true, "failed"},
		{"live but run stopped", wfRowState("progress"), false, "interrupted"},
		{"queued with no start", func(e *workflowAgentEntry) { e.State, e.QueuedAt = "start", new(int64(5)) }, true, "queued"},
		{"queued then started", func(e *workflowAgentEntry) { e.State, e.QueuedAt, e.StartedAt = "start", new(int64(5)), new(int64(6)) }, true, "running"},
		{"no queuedAt at all", wfRowState("progress"), true, "running"},
	} {
		t.Run("workflowDisplayState/"+c.name, func(t *testing.T) { eq(t, workflowDisplayState(wfAgentRow(0, c.mod), c.active), c.want) })
	}
	tw(t, f, "prefers skipped over blocked when both are set", func(t *testing.T) {
		e := wfAgentRow(0, func(e *workflowAgentEntry) { e.State, e.Skipped, e.Blocked = "error", true, true })
		eq(t, workflowDisplayState(e, true), "skipped")
	})
	tw(t, f, "reports done even after the run stops", func(t *testing.T) {
		eq(t, workflowDisplayState(wfAgentRow(0, wfRowState("done")), false), "done")
	})
	// workflowEntryLive
	tw(t, f, "is true only for start and progress", func(t *testing.T) {
		eq(t, workflowEntryLive(wfAgentRow(0, wfRowState("start"))), true)
		eq(t, workflowEntryLive(wfAgentRow(0, wfRowState("progress"))), true)
		eq(t, workflowEntryLive(wfAgentRow(0, wfRowState("done"))), false)
		eq(t, workflowEntryLive(wfAgentRow(0, wfRowState("error"))), false)
	})
	// buildPhaseGroups
	tw(t, f, "uses the phase title emitted for that index", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "Scan"), wfAgentRow(0, wfRowPhase(0))), nil)
		eq(t, wfTitlesOf(gs), []string{"Scan"})
	})
	tw(t, f, "falls back to `Phase N` when no title was emitted", func(t *testing.T) {
		eq(t, buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowPhase(3))), nil)[0].Title, "Phase 3")
	})
	tw(t, f, "collapses into a single `Agents` group when no agent has a phase", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfUnphasedRow(0), wfUnphasedRow(1)), nil)
		eq(t, len(gs), 1)
		eq(t, gs[0].Title, "Agents")
		eq(t, gs[0].TotalCount, 2)
	})
	tw(t, f, "still shows un-phased agents when meta declared phases", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfUnphasedRow(0)), []workflowPhaseMeta{{Title: "Scan"}})
		eq(t, wfTitlesOf(gs), []string{"Scan", "Agents"})
		eq(t, gs[1].TotalCount, 1)
	})
	tw(t, f, "orders groups by phase index, not first appearance", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(1, "Verify"), newPhaseEntry(0, "Scan"), wfAgentRow(0, wfRowPhase(1)), wfAgentRow(1, wfRowPhase(0))), nil)
		eq(t, wfTitlesOf(gs), []string{"Scan", "Verify"})
	})
	// merging with meta.phases
	tw(t, f, "shows a declared-but-unseen phase as not-started", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "Scan"), wfAgentRow(0)), []workflowPhaseMeta{{Title: "Scan"}, {Title: "Verify"}})
		eq(t, []string{gs[0].Title, gs[0].Status, gs[1].Title, gs[1].Status}, []string{"Scan", "running", "Verify", "not-started"})
	})
	tw(t, f, "matches when the declared title is a prefix of the observed one", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "Review changed files"), wfAgentRow(0)), []workflowPhaseMeta{{Title: "Review"}})
		eq(t, len(gs), 1)
		eq(t, gs[0].TotalCount, 1)
	})
	tw(t, f, "matches when the observed title is a prefix of the declared one", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "Review"), wfAgentRow(0)), []workflowPhaseMeta{{Title: "Review changed files"}})
		eq(t, len(gs), 1)
		eq(t, gs[0].TotalCount, 1)
	})
	tw(t, f, "matches case- and whitespace-insensitively", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "  scan  "), wfAgentRow(0)), []workflowPhaseMeta{{Title: "Scan"}})
		eq(t, gs[0].TotalCount, 1)
	})
	tw(t, f, "consumes each observed group at most once", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "Scan"), wfAgentRow(0)), []workflowPhaseMeta{{Title: "Scan"}, {Title: "Sc"}})
		eq(t, []string{gs[0].Status, gs[1].Status}, []string{"running", "not-started"})
		eq(t, gs[0].TotalCount, 1)
		eq(t, gs[1].TotalCount, 0)
	})
	tw(t, f, "appends an undeclared phase after the declared ones", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(newPhaseEntry(0, "Scan"), newPhaseEntry(1, "Improvise"), wfAgentRow(0, wfRowPhase(0)), wfAgentRow(1, wfRowPhase(1))), []workflowPhaseMeta{{Title: "Scan"}})
		eq(t, wfTitlesOf(gs), []string{"Scan", "Improvise"})
	})
	// summarizePhase
	tw(t, f, "is done only when every agent finished without error", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("done"))), nil)
		eq(t, gs[0].Status, "done")
		eq(t, gs[0].DoneCount, 2)
	})
	tw(t, f, "is failed when any agent errored, even if the rest are done", func(t *testing.T) {
		eq(t, buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("error"))), nil)[0].Status, "failed")
	})
	tw(t, f, "is running while any agent is still live", func(t *testing.T) {
		eq(t, buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("progress"))), nil)[0].Status, "running")
	})
	tw(t, f, "sums tokens across agents", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, func(e *workflowAgentEntry) { e.Tokens = new(100) }), wfAgentRow(1, func(e *workflowAgentEntry) { e.Tokens = new(250) })), nil)
		eq(t, gs[0].Tokens, 350)
	})
	tw(t, f, "measures phase duration as wall clock, not the sum of overlapping agents", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(
			wfAgentRow(0, func(e *workflowAgentEntry) { e.StartedAt, e.LastProgressAt = new(int64(1000)), new(int64(3000)) }),
			wfAgentRow(1, func(e *workflowAgentEntry) { e.StartedAt, e.LastProgressAt = new(int64(1500)), new(int64(4000)) })), nil)
		eq(t, gs[0].DurationMs, int64(3000)) // 4000 - 1000, not 2000 + 2500
	})
	tw(t, f, "reports zero duration when nothing started", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, func(e *workflowAgentEntry) { e.QueuedAt = new(int64(5)) })), nil)
		eq(t, gs[0].DurationMs, int64(0))
	})
	// stats
	tw(t, f, "counts done and failed toward started", func(t *testing.T) {
		r := progressStats(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("error"))), 0)
		eq(t, [4]any{r.Done, r.FailedCount, r.Started, r.Running}, [4]any{1, 1, 2, false})
	})
	tw(t, f, "does not count a queued-but-unstarted agent as started", func(t *testing.T) {
		r := progressStats(wfEntries(wfAgentRow(0, wfRowState("start"), func(e *workflowAgentEntry) { e.QueuedAt = new(int64(5)) })), 0)
		eq(t, r.Started, 0)
		eq(t, r.Running, true)
	})
	tw(t, f, "counts a live agent with no queuedAt as started", func(t *testing.T) {
		eq(t, progressStats(wfEntries(wfAgentRow(0, wfRowState("progress"))), 0).Started, 1)
	})
	tw(t, f, "uses the scheduled agent count when it exceeds entries seen", func(t *testing.T) {
		r := progressStats(wfEntries(wfAgentRow(0, wfRowState("done"))), 7)
		eq(t, r.Total, 7)
		eq(t, r.Complete, false)
	})
	tw(t, f, "is complete only when nothing is live and every scheduled agent settled", func(t *testing.T) {
		eq(t, progressStats(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("error"))), 2).Complete, true)
	})
	tw(t, f, "is not complete when an agent is still live", func(t *testing.T) {
		eq(t, progressStats(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("progress"))), 0).Complete, false)
	})
	tw(t, f, "is not complete for an empty log", func(t *testing.T) {
		eq(t, progressStats(nil, 0).Complete, false)
	})
	tw(t, f, "ignores log and phase entries", func(t *testing.T) {
		r := progressStats(wfEntries(newLogEntry("hi"), newPhaseEntry(0, "Scan")), 0)
		eq(t, [3]int{r.Done, r.Total, r.Started}, [3]int{0, 0, 0})
	})
	// workflowElapsedMs
	tw(t, f, "uses now while running", func(t *testing.T) { eq(t, workflowElapsedMs(workflowClock{StartTime: 1000}, 4000), int64(3000)) })
	tw(t, f, "freezes at endTime once finished", func(t *testing.T) {
		eq(t, workflowElapsedMs(workflowClock{StartTime: 1000, EndTime: new(int64(2500))}, 9999), int64(1500))
	})
	tw(t, f, "subtracts paused time", func(t *testing.T) {
		eq(t, workflowElapsedMs(workflowClock{StartTime: 1000, TotalPausedMs: 500}, 4000), int64(2500))
	})
	tw(t, f, "never goes negative", func(t *testing.T) {
		eq(t, workflowElapsedMs(workflowClock{StartTime: 1000, TotalPausedMs: 99_999}, 2000), int64(0))
	})
	// workflowFormatDuration (it.each)
	for _, c := range []struct {
		ms   float64
		want string
	}{{0, "0ms"}, {340, "340ms"}, {9000, "9s"}, {72_000, "1m12s"}, {3_600_000, "60m00s"}} {
		eq(t, workflowFormatDuration(c.ms), c.want)
	}
	// header
	meta := &workflowMeta{Name: "review-changes", Description: "review the diff"}
	task := workflowHeaderTask{Status: "running", StartTime: 1000}
	tw(t, f, "renders count and elapsed with no phase count", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done")), wfAgentRow(1, wfRowState("progress"))), nil)
		line := workflowHeaderLine(task, meta, gs, 7, 73_000)
		eq(t, line.Stats, "1/7 agents · 1m12s")
		eq(t, line.Name, "review-changes")
		eq(t, line.Subtext, "review the diff")
	})
	tw(t, f, "singularizes a lone agent", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done"))), nil)
		eq(t, workflowHeaderLine(task, meta, gs, 1, 2000).Stats, "1/1 agent · 1s")
	})
	for _, c := range [][2]string{{"completed", " · done"}, {"killed", " · stopped"}, {"paused", " · paused"}, {"failed", " · failed"}} {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done"))), nil)
		tk := task
		tk.Status = c[0]
		line := workflowHeaderLine(tk, meta, gs, 1, 2000)
		eq(t, line.Stats, "1/1 agent · 1s"+c[1])
	}
	tw(t, f, "adds no suffix while running", func(t *testing.T) {
		gs := buildPhaseGroups(wfEntries(wfAgentRow(0, wfRowState("done"))), nil)
		eq(t, workflowHeaderLine(task, meta, gs, 1, 2000).Stats, "1/1 agent · 1s")
	})
	tw(t, f, "prefers the task's workflow name over meta", func(t *testing.T) {
		tk := task
		tk.WorkflowName = "pinned"
		eq(t, workflowHeaderLine(tk, meta, buildPhaseGroups(nil, nil), 0, 1000).Name, "pinned")
	})
	// workflowSizeWarningResult
	tw(t, f, "stays silent for an ordinary run", func(t *testing.T) {
		eq(t, workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 5, StartedAgents: 5, TotalTokens: 10_000}) == nil, true)
	})
	tw(t, f, "fires on the agent axis past the cap", func(t *testing.T) {
		eq(t, workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 26, StartedAgents: 1, TotalTokens: 10}).Axis, "agents")
	})
	tw(t, f, "does not fire exactly at the cap", func(t *testing.T) {
		eq(t, workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 25, StartedAgents: 25, TotalTokens: 10}) == nil, true)
	})
	tw(t, f, "projects spend from agents that have already reported", func(t *testing.T) {
		w := workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 20, StartedAgents: 10, TotalTokens: 1_000_000})
		eq(t, w.Axis, "tokens")
		eq(t, w.ProjectedTokens, 2_000_000)
	})
	tw(t, f, "assumes a per-agent cost before anything has started", func(t *testing.T) {
		w := workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 22})
		eq(t, w.Axis, "tokens")
		eq(t, w.ProjectedTokens, 1_540_000)
	})
	tw(t, f, "reports both axes together", func(t *testing.T) {
		eq(t, workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 100}).Axis, "both")
	})
	tw(t, f, "honours overridden caps", func(t *testing.T) {
		w := workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 3, StartedAgents: 3, TotalTokens: 1, AgentCap: 2})
		eq(t, w.Axis, "agents")
		eq(t, w.AgentCap, 2)
	})
	tw(t, f, "never projects below what has already been spent", func(t *testing.T) {
		eq(t, workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: 1, StartedAgents: 10, TotalTokens: 2_000_000}).ProjectedTokens, 2_000_000)
	})
	// workflowGerund (it.each)
	for _, c := range [][2]string{{"Scan", "Scanning"}, {"Review", "Reviewing"}, {"commit", "committing"}, {"Commit", "Committing"}, {"submit", "submitting"},
		{"format", "formatting"}, {"Verify", "Verifying"}, {"run", "running"}, {"tie", "tying"}} {
		eq(t, workflowGerund(c[0]), c[1])
	}
	for _, w := range []string{"setup", "cleanup", "Running", "QA", "a-very-long-phase-title", "Phase 1"} {
		eq(t, workflowGerund(w), w)
	}
	tw(t, f, "does not double a final w, x or y", func(t *testing.T) {
		eq(t, workflowGerund("saw"), "sawing")
		eq(t, workflowGerund("fix"), "fixing")
	})
	// footerPhaseLabel
	tw(t, f, "shows position for a single active phase", func(t *testing.T) {
		eq(t, footerPhaseLabel(footerPhaseInput{Titles: []string{"Scan"}, PositionStart: 1, TotalPhases: 3}), "Scanning (1/3)")
	})
	tw(t, f, "joins two concurrent phases", func(t *testing.T) {
		eq(t, footerPhaseLabel(footerPhaseInput{Titles: []string{"Scan", "Verify"}, PositionStart: 1, TotalPhases: 3}), "Scanning & Verifying")
	})
	tw(t, f, "truncates a long title", func(t *testing.T) {
		eq(t, footerPhaseLabel(footerPhaseInput{Titles: []string{"Comprehensive review"}, PositionStart: 1, TotalPhases: 2}), "Comprehensive r… (1/2)")
	})
	tw(t, f, "leaves a title that exactly fits alone", func(t *testing.T) {
		eq(t, footerPhaseLabel(footerPhaseInput{Titles: []string{"Sixteen chars!!!"}, PositionStart: 1, TotalPhases: 2}), "Sixteen chars!!! (1/2)")
	})
	tw(t, f, "returns empty when no phase is active", func(t *testing.T) {
		eq(t, footerPhaseLabel(footerPhaseInput{}), "")
	})
}
