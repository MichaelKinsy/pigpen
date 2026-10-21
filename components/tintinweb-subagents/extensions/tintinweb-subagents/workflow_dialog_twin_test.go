package tintinweb_subagents

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twins of the original's test/workflow-dialog.test.ts: the two-pane inspector's layout, keys and component.
const fDialog = "workflow-dialog"

func dEntry(index int, mut func(*workflowAgentEntry)) *workflowAgentEntry {
	return aEntryWith(index, mut)
}

func TestDialogGlyphMapping(t *testing.T) {
	live := func() []workflowEntry {
		return []workflowEntry{
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State = "done", entryDone }),
			dEntry(1, func(e *workflowAgentEntry) { e.Label, e.State = "failed", entryError }),
			dEntry(2, func(e *workflowAgentEntry) { e.Label, e.State, e.Skipped = "skipped", entryError, true }),
			dEntry(3, func(e *workflowAgentEntry) { e.Label, e.State, e.Blocked = "blocked", entryError, true }),
			dEntry(4, func(e *workflowAgentEntry) { e.Label, e.State, e.QueuedAt = "queued", entryStart, startedAt(dlgStart) }),
			dEntry(5, func(e *workflowAgentEntry) {
				e.Label, e.State, e.StartedAt = "running", entryProgress, startedAt(dlgStart)
			}),
		}
	}
	tw(t, fDialog, "maps every one of the seven display states to its glyph and colour", func(t *testing.T) {
		g := unicodeDialogGlyphs
		eq(t, dialogRowGlyph("done", g, 0), workflowCardSegment{Text: "✔", Color: "success"})
		eq(t, dialogRowGlyph("failed", g, 0), workflowCardSegment{Text: "✘", Color: "error"})
		eq(t, dialogRowGlyph("skipped", g, 0), workflowCardSegment{Text: "✘", Color: "dim"})
		eq(t, dialogRowGlyph("blocked", g, 0), workflowCardSegment{Text: "✘", Color: "warning"})
		eq(t, dialogRowGlyph("queued", g, 0), workflowCardSegment{Text: "◌", Color: "dim"})
		eq(t, dialogRowGlyph("interrupted", g, 0), workflowCardSegment{Text: "◌", Color: "dim"})
		eq(t, dialogRowGlyph("running", g, 3), workflowCardSegment{Text: spinner[3], Color: "dim"})
	})
	tw(t, fDialog, "keys off the derived display state, not the raw entry state", func(t *testing.T) {
		rows := rightRows(dialogLines(dialogOver{Progress: live()}))
		hasStr(t, rows[0], "✔ done")
		hasStr(t, rows[1], "✘ failed")
		hasStr(t, rows[2], "✘ skipped")
		hasStr(t, rows[3], "✘ blocked")
		// All four above are done or error inline; only the dialog splits the last three apart, and only it can draw ◌
		// for the queued row.
		hasStr(t, rows[4], "◌ queued")
		hasStr(t, rows[5], spinner[0]+" running")
	})
	tw(t, fDialog, "renders blocked distinctly from skipped despite sharing the cross", func(t *testing.T) {
		lines := styledDialog(dialogOver{Progress: live()})
		find := func(sub string) string {
			for _, l := range lines {
				if strings.Contains(l, sub) {
					return l
				}
			}
			return ""
		}
		hasStr(t, find("skipped"), "<dim>✘</dim>")
		hasStr(t, find("blocked"), "<warning>✘</warning>")
		hasStr(t, find("failed"), "<error>✘</error>")
		hasStr(t, find(">done"), "<success>✔</success>")
	})
	tw(t, fDialog, "does not use the card's ⟳ for a running row — it spins, and queues draw ◌", func(t *testing.T) {
		joined := strings.Join(dialogLines(dialogOver{Progress: live()}), "\n")
		lacksStr(t, joined, "⟳")
		hasStr(t, joined, "◌")
		// And the spinner really advances, which a static glyph could not do.
		later := strings.Join(dialogLines(dialogOver{Progress: live(), SpinnerFrame: 4}), "\n")
		hasStr(t, later, spinner[4]+" running")
		lacksStr(t, later, spinner[0]+" running")
	})
	tw(t, fDialog, "draws ◌ for an agent still live when the run stopped", func(t *testing.T) {
		rows := rightRows(dialogLines(dialogOver{
			Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) {
				e.Label, e.State, e.StartedAt = "cutoff", entryProgress, startedAt(dlgStart)
			})},
			Task: taskStatus(runKilled)}))
		hasStr(t, rows[0], "◌ cutoff")
	})
	tw(t, fDialog, "keeps an ASCII tier one column wide for every glyph", func(t *testing.T) {
		g := asciiDialogGlyphs
		for _, s := range []string{g.tick, g.cross, g.queued, g.pointer, g.focus} {
			eq(t, widthx.VisibleWidth(s), 1)
		}
		joined := strings.Join(dialogLines(dialogOver{Progress: live(), ASCII: true}), "\n")
		eq(t, regexp.MustCompile(`[✔✘◌❯▸]`).MatchString(joined), false)
		hasStr(t, joined, "√ done")
		hasStr(t, joined, "o queued")
	})
}

func TestDialogPhasesPane(t *testing.T) {
	meta := &workflowMeta{Name: "review-changes", Description: "Review changed files", Phases: []workflowPhaseMeta{{Title: "Review"}, {Title: "Verify"}, {Title: "Report"}}}
	progress := func() []workflowEntry {
		return []workflowEntry{phaseEntry(0, "Review"), phaseEntry(1, "Verify"),
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "a", intp(0), entryDone }),
			dEntry(1, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "b", intp(1), entryStart })}
	}
	tw(t, fDialog, "shows a phase's number until it finishes, then its glyph", func(t *testing.T) {
		rows := leftRows(dialogLines(dialogOver{Progress: progress(), Meta: meta}))
		// Review is fully done: tick. Verify is still running: its NUMBER, not a glyph. Report never started: its number too.
		hasStr(t, rows[0], "✔ Review")
		hasStr(t, rows[1], "2 Verify")
		hasStr(t, rows[2], "3 Report")
		lacksStr(t, rows[1], "✔")
		lacksStr(t, rows[1], "✘")
	})
	tw(t, fDialog, "shows a cross once a phase has a failure", func(t *testing.T) {
		rows := leftRows(dialogLines(dialogOver{Progress: []workflowEntry{phaseEntry(0, "Review"),
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "a", intp(0), entryError })}}))
		hasStr(t, rows[0], "✘ Review")
	})
	tw(t, fDialog, "points at the selected phase and accents it, leaving the others alone", func(t *testing.T) {
		rows := leftRows(styledDialog(dialogOver{Progress: progress(), Meta: meta, State: func(s *workflowDialogState) { s.SelectedPhase = 1 }}))
		hasStr(t, rows[1], "<accent>❯</accent>")
		hasStr(t, rows[1], "<accent>Verify</accent>")
		lacksStr(t, rows[0], "❯")
		hasStr(t, rows[0], "<success>")
		hasStr(t, rows[2], "<dim>")
	})
	tw(t, fDialog, "leaves a declared-but-unseen phase without a count", func(t *testing.T) {
		// The narrow pane has no room for "Not started yet"; an absent count says the same thing.
		rows := leftRows(dialogLines(dialogOver{Progress: progress(), Meta: meta}))
		hasStr(t, rows[2], "Report")
		eq(t, regexp.MustCompile(`\d/\d`).MatchString(rows[2]), false)
		hasStr(t, rows[0], "1/1")
	})
	tw(t, fDialog, "swaps the agent list when the phase selection moves", func(t *testing.T) {
		hasStr(t, rightRows(dialogLines(dialogOver{Progress: progress(), Meta: meta}))[0], " a")
		hasStr(t, rightRows(dialogLines(dialogOver{Progress: progress(), Meta: meta, State: func(s *workflowDialogState) { s.SelectedPhase = 1 }}))[0], " b")
	})
	tw(t, fDialog, "names the selected phase and its agent count on the right pane", func(t *testing.T) {
		l, r := paneTitles(t, dialogLines(dialogOver{Progress: progress(), Meta: meta}))
		eq(t, l, "Phases")
		eq(t, r, "Review · 1 agent")
		_, r = paneTitles(t, dialogLines(dialogOver{Progress: progress(), Meta: meta, State: func(s *workflowDialogState) { s.SelectedPhase = 1 }}))
		eq(t, r, "Verify · 1 agent")
	})
}

func TestDialogStateFilter(t *testing.T) {
	progress := func() []workflowEntry {
		return []workflowEntry{
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State = "one", entryDone }),
			dEntry(1, func(e *workflowAgentEntry) { e.Label, e.State, e.StartedAt = "two", entryProgress, startedAt(dlgStart) }),
			dEntry(2, func(e *workflowAgentEntry) {
				e.Label, e.State, e.StartedAt = "three", entryProgress, startedAt(dlgStart)
			}),
		}
	}
	filter := func(f string) func(*workflowDialogState) { return func(s *workflowDialogState) { s.Filter = f } }
	tw(t, fDialog, "counts unfiltered agents with a plural that agrees", func(t *testing.T) {
		// Exact: "1 agent" is a substring of "1 agents". "Phase 0" because the fixtures carry a phase index with no
		// declared title; what is pinned is the count and its plural.
		_, r := paneTitles(t, dialogLines(dialogOver{Progress: progress()}))
		eq(t, r, "Phase 0 · 3 agents")
		_, r = paneTitles(t, dialogLines(dialogOver{Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) { e.State = entryDone })}}))
		eq(t, r, "Phase 0 · 1 agent")
	})
	tw(t, fDialog, "narrows the visible set and names the filter in the pane title", func(t *testing.T) {
		lines := dialogLines(dialogOver{Progress: progress(), State: filter("running")})
		// The count is of what survived, so the title has to say which filter or "2 agents" would read as the whole phase.
		_, r := paneTitles(t, lines)
		eq(t, r, "Phase 0 · 2 running")
		rows := rightRows(lines)
		eq(t, len(rows), 2)
		lacksStr(t, strings.Join(rows, "\n"), "one")
		hasStr(t, strings.Join(rows, "\n"), "two")
	})
	tw(t, fDialog, "reports a filter that matches nothing, and says so in the list", func(t *testing.T) {
		lines := dialogLines(dialogOver{Progress: progress(), State: filter("blocked")})
		_, r := paneTitles(t, lines)
		eq(t, r, "Phase 0 · 0 blocked")
		var trimmed []string
		for _, row := range rightRows(lines) {
			trimmed = append(trimmed, strings.TrimSpace(row))
		}
		eq(t, trimmed, []string{copyNoAgents})
		eq(t, copyNoAgents, "No agents")
	})
	tw(t, fDialog, "filters on the derived state, so a stopped run's live agents match interrupted", func(t *testing.T) {
		view := resolveWorkflowDialog(dlgInput(dialogOver{Progress: progress(), Task: taskStatus(runKilled), State: filter("interrupted")}))
		var labels []string
		for _, a := range view.VisibleAgents {
			labels = append(labels, a.Label)
		}
		eq(t, labels, []string{"two", "three"})
	})
}

func TestDialogSubStatusAnnotations(t *testing.T) {
	tw(t, fDialog, "renders the retry reason and attempt number from the entry", func(t *testing.T) {
		eq(t, workflowSubStatusAnnotations(dEntry(0, func(e *workflowAgentEntry) { e.Attempt, e.LastAttemptReason = 3, "throttled" }), "running", dlgStart), []string{"throttled", "attempt 3"})
		eq(t, workflowSubStatusAnnotations(dEntry(0, func(e *workflowAgentEntry) { e.LastAttemptReason = "user-retry" }), "running", dlgStart), []string{"user retry"})
		eq(t, workflowSubStatusAnnotations(dEntry(0, func(e *workflowAgentEntry) { e.LastAttemptReason = "stalled" }), "running", dlgStart), []string{"stalled"})
	})
	tw(t, fDialog, "does not annotate a first attempt", func(t *testing.T) {
		eq(t, len(workflowSubStatusAnnotations(dEntry(0, func(e *workflowAgentEntry) { e.Attempt = 1 }), "running", dlgStart)), 0)
	})
	tw(t, fDialog, "marks a journal replay and an isolated child", func(t *testing.T) {
		eq(t, workflowSubStatusAnnotations(dEntry(0, func(e *workflowAgentEntry) { e.Cached, e.Isolation = true, "worktree" }), "done", dlgStart), []string{"worktree", "from resume journal"})
	})
	tw(t, fDialog, "shows how long a queued agent has waited, and only while it is queued", func(t *testing.T) {
		entry := dEntry(0, func(e *workflowAgentEntry) { e.QueuedAt = startedAt(dlgStart) })
		eq(t, workflowSubStatusAnnotations(entry, "queued", dlgStart+8000), []string{"waiting 8s"})
		eq(t, len(workflowSubStatusAnnotations(entry, "running", dlgStart+8000)), 0)
	})
	tw(t, fDialog, "puts the annotations on the row ahead of the card's stat tail", func(t *testing.T) {
		rows := rightRows(dialogLines(dialogOver{Now: dlgStart + 8000, Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.State, e.QueuedAt, e.Attempt, e.LastAttemptReason, e.AgentType, e.ToolCalls = "retry-me", entryStart, startedAt(dlgStart), 2, "throttled", "Explore", intp(4)
		})}}))
		// The agent type and the tool-call count are the detail pane's, not the row's: the row carries why it looks the
		// way it does, then the model and the token count.
		eq(t, strings.TrimSpace(rows[0]), "❯ ◌ retry-me · throttled · attempt 2 · waiting 8s")
	})
}

func TestDialogPerAgentDetail(t *testing.T) {
	long := func() string {
		var lines []string
		for i := range 9 {
			lines = append(lines, "prompt line "+itoa(i))
		}
		return strings.Join(lines, "\n")
	}
	one := func(mut func(*workflowAgentEntry)) []workflowEntry { return []workflowEntry{dEntry(0, mut)} }
	bareRows := func(lines []string) []string {
		var out []string
		for _, r := range rightRows(lines) {
			out = append(out, bare(r))
		}
		return out
	}
	headingOf := func(lines []string, prefix string) string {
		for _, r := range rightRows(lines) {
			if b := strings.TrimSpace(bare(r)); strings.HasPrefix(b, prefix) {
				return b
			}
		}
		return ""
	}
	// The detail pane is where per-agent configuration belongs: it has the width the card row does not.
	tw(t, fDialog, "names the canonical model id, which the tight card row has no room for", func(t *testing.T) {
		rows := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) {
			e.State, e.Model, e.ModelID = entryDone, "haiku 4.5", "anthropic/claude-haiku-4-5"
		})})
		hasStr(t, strings.Join(bareRows(rows), "\n"), "anthropic/claude-haiku-4-5")
	})
	tw(t, fDialog, "shows the thinking level and discloses one that was clamped", func(t *testing.T) {
		rows := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.Thinking, e.RequestedThinking = entryDone, "low", "max" })})
		hasStr(t, strings.Join(bareRows(rows), "\n"), "thinking: low (asked max)")
	})
	tw(t, fDialog, "collapses a long prompt behind an `expand` affordance and counts its lines", func(t *testing.T) {
		collapsed := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.PromptPreview = entryDone, long() })})
		hasStr(t, headingOf(collapsed, "Prompt"), "Prompt · 9 lines · ⏎ expand")
		// The shown lines plus the row that names how many are not shown.
		sec := section(t, collapsed, "Prompt")
		eq(t, len(sec), promptCollapsedLines+1)
		hasStr(t, sec[len(sec)-1], "… 5 more lines")
		expanded := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.PromptPreview = entryDone, long() }),
			State: func(s *workflowDialogState) { s.PromptExpanded = true }})
		hasStr(t, headingOf(expanded, "Prompt"), "Prompt · 9 lines · ⏎ collapse")
		eq(t, len(section(t, expanded, "Prompt")), 9)
	})
	tw(t, fDialog, "does not offer expand for a prompt that already fits, and counts one line as one", func(t *testing.T) {
		two := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.PromptPreview = entryDone, "one\ntwo" })})
		eq(t, headingOf(two, "Prompt"), "Prompt · 2 lines")
		single := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.PromptPreview = entryDone, "solo" })})
		eq(t, headingOf(single, "Prompt"), "Prompt · 1 line")
	})
	tw(t, fDialog, "uses Claude Code's copy for a prompt and activity that do not exist yet", func(t *testing.T) {
		lines := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.QueuedAt = entryStart, startedAt(dlgStart) })})
		eq(t, section(t, lines, "Prompt"), []string{"  " + copyAvailableOnceStart})
		eq(t, section(t, lines, "Activity"), []string{"  Available once the agent starts."})
		eq(t, section(t, lines, "Outcome"), []string{"  Waiting for an agent slot."})
	})
	tw(t, fDialog, "distinguishes no-tool-calls-yet from no-tool-calls-ever", func(t *testing.T) {
		running := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.StartedAt = entryProgress, startedAt(dlgStart) })})
		eq(t, section(t, running, "Activity"), []string{"  No tool calls yet."})
		finished := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State = entryDone })})
		eq(t, section(t, finished, "Activity"), []string{"  No tool calls."})
	})
	tw(t, fDialog, "heads Activity with the tool-call count and admits it has no transcript", func(t *testing.T) {
		lines := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.ToolCalls = entryDone, intp(3) })})
		eq(t, headingOf(lines, "Activity"), "Activity · 3 tool calls")
		eq(t, section(t, lines, "Activity"), []string{"  Transcript not available."})
		single := detailLines(dialogOver{Progress: one(func(e *workflowAgentEntry) { e.State, e.ToolCalls = entryDone, intp(1) })})
		eq(t, headingOf(single, "Activity"), "Activity · 1 tool call")
	})
	tw(t, fDialog, "writes a different Outcome for every terminal state", func(t *testing.T) {
		outcome := func(mut func(*workflowAgentEntry), task func(*workflowCardTask)) []string {
			return section(t, detailLines(dialogOver{Progress: one(mut), Task: task}), "Outcome")
		}
		eq(t, outcome(func(e *workflowAgentEntry) { e.State, e.Skipped = entryError, true }, nil), []string{"  Skipped by user."})
		eq(t, outcome(func(e *workflowAgentEntry) { e.State, e.Error = entryError, "boom" }, nil), []string{"  boom"})
		eq(t, outcome(func(e *workflowAgentEntry) { e.State, e.Blocked = entryError, true }, nil), []string{"  " + copyNoTranscript})
		eq(t, outcome(func(e *workflowAgentEntry) { e.State, e.ResultPreview = entryDone, "shipped" }, nil), []string{"  shipped"})
		eq(t, outcome(func(e *workflowAgentEntry) { e.State, e.StartedAt = entryProgress, startedAt(dlgStart) }, nil), []string{"  Not available yet (agent still running)."})
		eq(t, outcome(func(e *workflowAgentEntry) { e.State, e.StartedAt = entryProgress, startedAt(dlgStart) }, taskStatus(runKilled)),
			[]string{"  The workflow stopped before this agent finished."})
	})
	tw(t, fDialog, "omits the detail sections entirely when nothing is selected", func(t *testing.T) {
		lines := detailLines(dialogOver{Progress: []workflowEntry{}})
		joined := strings.Join(lines, "\n")
		lacksStr(t, joined, "Prompt")
		lacksStr(t, joined, "Outcome")
		for _, r := range rightRows(lines) {
			eq(t, strings.TrimSpace(r), "")
		}
		var left []string
		for _, r := range leftRows(lines) {
			left = append(left, strings.TrimSpace(r))
		}
		eq(t, left, []string{copyNoAgents})
	})
}

func TestDialogKeys(t *testing.T) {
	progress := func() []workflowEntry {
		return []workflowEntry{phaseEntry(0, "Review"), phaseEntry(1, "Verify"),
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "a", intp(0), entryDone }),
			dEntry(1, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "b", intp(0), entryDone }),
			dEntry(2, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "c", intp(1), entryDone })}
	}
	press := func(data string, st func(*workflowDialogState)) *workflowDialogKeyResult {
		full := dlgInput(dialogOver{Progress: progress(), State: st})
		return handleWorkflowDialogKey(data, full.State, resolveWorkflowDialog(full))
	}
	agents := func(sel int) func(*workflowDialogState) {
		return func(s *workflowDialogState) { s.Level, s.SelectedAgent = dialogLevelAgent, sel }
	}
	phase := func(n int) func(*workflowDialogState) { return func(s *workflowDialogState) { s.SelectedPhase = n } }
	tw(t, fDialog, "moves the phase selection with j/k and clamps at both ends", func(t *testing.T) {
		eq(t, press("j", nil).State.SelectedPhase, 1)
		eq(t, press("j", phase(1)).State.SelectedPhase, 1)
		eq(t, press("k", phase(1)).State.SelectedPhase, 0)
		eq(t, press("k", phase(0)).State.SelectedPhase, 0)
	})
	tw(t, fDialog, "moves the agent selection with j/k and clamps at both ends", func(t *testing.T) {
		eq(t, press("j", agents(0)).State.SelectedAgent, 1)
		eq(t, press("j", agents(1)).State.SelectedAgent, 1)
		eq(t, press("k", agents(1)).State.SelectedAgent, 0)
		eq(t, press("k", agents(0)).State.SelectedAgent, 0)
	})
	tw(t, fDialog, "re-points the agent cursor at the top when the phase changes", func(t *testing.T) {
		moved := press("j", func(s *workflowDialogState) { s.SelectedAgent = 1 })
		eq(t, moved.State.SelectedPhase, 1)
		eq(t, moved.State.SelectedAgent, 0)
	})
	tw(t, fDialog, "never runs the cursor off an empty list", func(t *testing.T) {
		empty := dlgInput(dialogOver{Progress: []workflowEntry{}, State: func(s *workflowDialogState) { s.Level = dialogLevelAgent }})
		view := resolveWorkflowDialog(empty)
		eq(t, handleWorkflowDialogKey("j", empty.State, view).State.SelectedAgent, 0)
		eq(t, view.SelectedEntry == nil, true)
	})
	tw(t, fDialog, "opens the selected phase's agents, and escape comes back", func(t *testing.T) {
		eq(t, press("\r", nil).State.Level, dialogLevelAgent)
		eq(t, press("\x1b", agents(0)).State.Level, dialogLevelPhases)
		// Back one level, not out of the dialog: a wrong turn must not cost it.
		eq(t, press("\x1b", agents(0)).Action == nil, true)
	})
	tw(t, fDialog, "refuses to open a phase with nothing in it", func(t *testing.T) {
		// A subview with no rows and no detail is a dead end to back out of.
		empty := dlgInput(dialogOver{Progress: []workflowEntry{}})
		eq(t, handleWorkflowDialogKey("\r", empty.State, resolveWorkflowDialog(empty)).State.Level, dialogLevelPhases)
	})
	tw(t, fDialog, "swaps which pane holds which list when it opens", func(t *testing.T) {
		l, r := paneTitles(t, dialogLines(dialogOver{Progress: progress()}))
		eq(t, l, "Phases")
		eq(t, r, "Review · 2 agents")
		l, r = paneTitles(t, dialogLines(dialogOver{Progress: progress(), State: func(s *workflowDialogState) { s.Level = dialogLevelAgent }}))
		// The agents move left and the selected one's detail takes the right. The left title is truncated to the narrow
		// pane, marker and all.
		eq(t, l, "Review · 2 agen…")
		eq(t, r, "a")
	})
	tw(t, fDialog, "cycles the filter and resets the agent cursor with it", func(t *testing.T) {
		eq(t, press("f", nil).State.Filter, "running")
		eq(t, press("f", func(s *workflowDialogState) { s.Filter = "running" }).State.Filter, "queued")
		eq(t, press("f", func(s *workflowDialogState) { s.Filter = "interrupted" }).State.Filter, "all")
		eq(t, press("f", func(s *workflowDialogState) { s.SelectedAgent = 1 }).State.SelectedAgent, 0)
	})
	tw(t, fDialog, "toggles the prompt expansion", func(t *testing.T) {
		eq(t, press("e", nil).State.PromptExpanded, true)
		eq(t, press("e", func(s *workflowDialogState) { s.PromptExpanded = true }).State.PromptExpanded, false)
		// Enter means "open" at the overview and "expand" once opened.
		eq(t, press("\r", func(s *workflowDialogState) { s.Level = dialogLevelAgent }).State.PromptExpanded, true)
	})
	tw(t, fDialog, "cancels on escape from the overview", func(t *testing.T) {
		eq(t, *press("\x1b", nil).Action, workflowDialogAction{Kind: "cancel"})
	})
	tw(t, fDialog, "closes on ctrl+c from either level", func(t *testing.T) {
		eq(t, *press("\x03", nil).Action, workflowDialogAction{Kind: "cancel"})
		// The case that separates it from `esc`, which only steps back a level.
		opened := press("\x03", func(s *workflowDialogState) { s.Level = dialogLevelAgent })
		eq(t, *opened.Action, workflowDialogAction{Kind: "cancel"})
		eq(t, opened.State.Level, dialogLevelAgent)
	})
	tw(t, fDialog, "raises the run-level actions, and pause flips to resume once paused", func(t *testing.T) {
		eq(t, *press("x", nil).Action, workflowDialogAction{Kind: "kill"})
		eq(t, *press("p", nil).Action, workflowDialogAction{Kind: "pause"})
		paused := dlgInput(dialogOver{Progress: progress(), Task: taskStatus(runPaused)})
		eq(t, *handleWorkflowDialogKey("p", paused.State, resolveWorkflowDialog(paused)).Action, workflowDialogAction{Kind: "resume"})
	})

	// A run whose phase 0 holds one queued, one running and one finished agent.
	mixed := func() []workflowEntry {
		return []workflowEntry{phaseEntry(0, "Review"),
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.QueuedAt = "queued", entryStart, startedAt(dlgStart) }),
			dEntry(1, func(e *workflowAgentEntry) {
				e.Label, e.State, e.QueuedAt, e.StartedAt = "running", entryProgress, startedAt(dlgStart), startedAt(dlgStart)
			}),
			dEntry(2, func(e *workflowAgentEntry) { e.Label, e.State = "done", entryDone })}
	}
	pressMixed := func(data string, sel int) *workflowDialogKeyResult {
		full := dlgInput(dialogOver{Progress: mixed(), State: agents(sel)})
		return handleWorkflowDialogKey(data, full.State, resolveWorkflowDialog(full))
	}
	tw(t, fDialog, "raises skip and retry against the selected agent's stable index", func(t *testing.T) {
		eq(t, *pressMixed("s", 1).Action, workflowDialogAction{Kind: "skip", Index: 1})
		eq(t, *pressMixed("r", 1).Action, workflowDialogAction{Kind: "retry", Index: 1})
	})
	tw(t, fDialog, "uses the entry's index, not its row position", func(t *testing.T) {
		// The distinction only shows across phases: phase 1's first row is index 3, so a dialog reporting the row
		// would skip the wrong agent entirely.
		across := []workflowEntry{phaseEntry(0, "Review"), phaseEntry(1, "Verify"),
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "r0", intp(0), entryDone }),
			dEntry(1, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "r1", intp(0), entryDone }),
			dEntry(2, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "r2", intp(0), entryDone }),
			dEntry(3, func(e *workflowAgentEntry) {
				e.Label, e.PhaseIndex, e.State, e.QueuedAt, e.StartedAt = "v0", intp(1), entryProgress, startedAt(dlgStart), startedAt(dlgStart)
			})}
		full := dlgInput(dialogOver{Progress: across, State: func(s *workflowDialogState) { s.Level, s.SelectedPhase, s.SelectedAgent = dialogLevelAgent, 1, 0 }})
		view := resolveWorkflowDialog(full)
		eq(t, view.SelectedEntry.Label, "v0")
		eq(t, *handleWorkflowDialogKey("s", full.State, view).Action, workflowDialogAction{Kind: "skip", Index: 3})
		eq(t, *handleWorkflowDialogKey("r", full.State, view).Action, workflowDialogAction{Kind: "retry", Index: 3})
	})
	tw(t, fDialog, "offers skip but not retry on an agent that has not started", func(t *testing.T) {
		// Nothing has been spawned to stop and start over, but the call can still be given up on.
		eq(t, *pressMixed("s", 0).Action, workflowDialogAction{Kind: "skip", Index: 0})
		eq(t, pressMixed("r", 0) == nil, true)
	})
	tw(t, fDialog, "refuses both on an agent that has already settled", func(t *testing.T) {
		// Its agent() call has its value; there is nothing left to skip or redo.
		eq(t, pressMixed("s", 2) == nil, true)
		eq(t, pressMixed("r", 2) == nil, true)
	})
	tw(t, fDialog, "refuses both once the run itself has stopped", func(t *testing.T) {
		end := dlgStart + 10
		settled := dlgInput(dialogOver{Progress: mixed(), State: agents(1), Task: func(c *workflowCardTask) { c.Status, c.EndTime = runKilled, &end }})
		view := resolveWorkflowDialog(settled)
		eq(t, handleWorkflowDialogKey("s", settled.State, view) == nil, true)
		eq(t, handleWorkflowDialogKey("r", settled.State, view) == nil, true)
	})

	// The same three agents, each carrying the manager record id `c` opens.
	withRecords := func() []workflowEntry {
		return []workflowEntry{phaseEntry(0, "Review"),
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.QueuedAt = "queued", entryStart, startedAt(dlgStart) }),
			dEntry(1, func(e *workflowAgentEntry) {
				e.Label, e.State, e.QueuedAt, e.StartedAt, e.RecordID = "running", entryProgress, startedAt(dlgStart), startedAt(dlgStart), "rec-1"
			}),
			dEntry(2, func(e *workflowAgentEntry) { e.Label, e.State, e.RecordID = "done", entryDone, "rec-2" })}
	}
	pressRecords := func(data string, st func(*workflowDialogState)) *workflowDialogKeyResult {
		full := dlgInput(dialogOver{Progress: withRecords(), State: st})
		return handleWorkflowDialogKey(data, full.State, resolveWorkflowDialog(full))
	}
	tw(t, fDialog, "opens the selected agent's conversation on c, at either level", func(t *testing.T) {
		// Both levels, because the selected row is the one marked in the agents pane either way: unlike skip/retry,
		// which change the run and so belong to the level that shows one agent.
		eq(t, *pressRecords("c", func(s *workflowDialogState) { s.Level, s.SelectedAgent = dialogLevelAgent, 1 }).Action, workflowDialogAction{Kind: "open", RecordID: "rec-1"})
		eq(t, *pressRecords("c", func(s *workflowDialogState) { s.Level, s.SelectedAgent = dialogLevelPhases, 1 }).Action, workflowDialogAction{Kind: "open", RecordID: "rec-1"})
	})
	tw(t, fDialog, "opens a settled agent's conversation, unlike skip and retry", func(t *testing.T) {
		// Reading what a finished child actually did is most of the reason to open this dialog at all.
		eq(t, *pressRecords("c", agents(2)).Action, workflowDialogAction{Kind: "open", RecordID: "rec-2"})
	})
	tw(t, fDialog, "opens nothing for a row whose child has no record yet", func(t *testing.T) {
		// A queued agent has been scheduled, not spawned, so there is no conversation behind the row: the key falls
		// through as unbound rather than raising an action the caller can only refuse.
		eq(t, pressRecords("c", agents(0)) == nil, true)
	})
	tw(t, fDialog, "still opens a conversation once the run itself has stopped", func(t *testing.T) {
		end := dlgStart + 10
		settled := dlgInput(dialogOver{Progress: withRecords(), State: agents(2), Task: func(c *workflowCardTask) { c.Status, c.EndTime = runKilled, &end }})
		eq(t, *handleWorkflowDialogKey("c", settled.State, resolveWorkflowDialog(settled)).Action, workflowDialogAction{Kind: "open", RecordID: "rec-2"})
	})
	tw(t, fDialog, "leaves an unbound key alone", func(t *testing.T) { eq(t, press("z", nil) == nil, true) })
	tw(t, fDialog, "refuses the run-level actions once the run has settled", func(t *testing.T) {
		// The footer already hides `x stop` and `p pause` on a finished run, so the keys have to agree: stopping
		// something that already stopped reports a success that did not happen.
		for _, status := range []string{runCompleted, runFailed, runKilled} {
			end := dlgStart + 10
			settled := dlgInput(dialogOver{Progress: progress(), Task: func(c *workflowCardTask) { c.Status, c.EndTime = status, &end }})
			view := resolveWorkflowDialog(settled)
			eq(t, handleWorkflowDialogKey("x", settled.State, view) == nil, true)
			eq(t, handleWorkflowDialogKey("p", settled.State, view) == nil, true)
		}
	})
}

func TestDialogFrameHeight(t *testing.T) {
	// bodyHeight is every row of the frame's body, padding included: the box's real height.
	bodyHeight := func(over dialogOver) int { return len(cellsOf(dialogLines(over))) }
	phased := func(agents int) []workflowEntry {
		out := []workflowEntry{phaseEntry(0, "Review")}
		for i := range agents {
			out = append(out, dEntry(i, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "a"+itoa(i), intp(0), entryDone }))
		}
		return out
	}
	tw(t, fDialog, "sizes the box to its content instead of a fixed height", func(t *testing.T) {
		// A three-agent run in a twenty-two row box is twenty rows of nothing, sitting in the conversation.
		eq(t, bodyHeight(dialogOver{Progress: phased(3)}) < defaultPaneBodyRows, true)
	})
	tw(t, fDialog, "keeps a floor so a one-agent run still reads as a pane", func(t *testing.T) {
		eq(t, bodyHeight(dialogOver{Progress: phased(1)}), 6)
	})
	tw(t, fDialog, "grows with the content", func(t *testing.T) {
		small, large := bodyHeight(dialogOver{Progress: phased(3)}), bodyHeight(dialogOver{Progress: phased(12)})
		eq(t, large > small, true)
		eq(t, large, 12)
	})
	tw(t, fDialog, "stops growing at the cap, however large the run", func(t *testing.T) {
		// A 200-agent fan-out must not paste 200 rows into the scrollback; the window inside the pane scrolls instead.
		eq(t, bodyHeight(dialogOver{Progress: phased(200)}), defaultPaneBodyRows)
	})
	// The detail pane is the one thing in the dialog that is never windowed: an expanded prompt is however many lines
	// the prompt has, so it is where the cap has to actually hold.
	longPrompt := func() []workflowEntry {
		var lines []string
		for i := range 60 {
			lines = append(lines, "prompt line "+itoa(i))
		}
		return []workflowEntry{phaseEntry(0, "Review"), dEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.PhaseIndex, e.State, e.PromptPreview = "a", intp(0), entryDone, strings.Join(lines, "\n")
		})}
	}
	expanded := func(s *workflowDialogState) { s.Level, s.PromptExpanded = dialogLevelAgent, true }
	tw(t, fDialog, "caps the detail pane when a long prompt is expanded", func(t *testing.T) {
		eq(t, bodyHeight(dialogOver{Progress: longPrompt(), State: expanded}), defaultPaneBodyRows)
	})
	tw(t, fDialog, "honours an explicit cap from the caller", func(t *testing.T) {
		eq(t, bodyHeight(dialogOver{Progress: longPrompt(), State: expanded, BodyRows: 9}), 9)
		eq(t, bodyHeight(dialogOver{Progress: phased(200), BodyRows: 9}), 9)
	})
}

func TestDialogWidth(t *testing.T) {
	wide := func() []workflowEntry {
		return []workflowEntry{phaseEntry(0, "A phase title that runs on well past any sane terminal width"),
			dEntry(0, func(e *workflowAgentEntry) {
				e.Label = "an-extremely-long-agent-label-that-would-otherwise-wrap-the-whole-dialog"
				e.State, e.AgentType, e.Model, e.Tokens, e.ToolCalls, e.DurationMs = entryDone, "general-purpose", "claude-opus-4-5-20260101", intp(1_240_000), intp(412), i64p(3_723_000)
				e.PromptPreview = "a prompt line long enough to overflow a narrow terminal all by itself"
				e.ResultPreview = "an outcome line long enough to overflow a narrow terminal all by itself"
			})}
	}
	maxWidth := func(lines []string) int {
		m := 0
		for _, l := range lines {
			m = max(m, widthx.VisibleWidth(l))
		}
		return m
	}
	tw(t, fDialog, "reserves six columns of chrome from the terminal width", func(t *testing.T) {
		eq(t, workflowDialogContentWidth(86), 80)
		eq(t, maxWidth(dialogLines(dialogOver{Progress: wide(), Width: 86})), 80)
	})
	tw(t, fDialog, "floors the content width at 12 rather than going negative", func(t *testing.T) {
		eq(t, workflowDialogContentWidth(4), 12)
		// Without the floor the layout would clamp every line to nothing at all.
		eq(t, maxWidth(dialogLines(dialogOver{Progress: wide(), Width: 4})), 12)
	})
	tw(t, fDialog, "never exceeds the content width", func(t *testing.T) {
		for _, width := range []int{4, 12, 20, 40, 80} {
			lines := dialogLines(dialogOver{Progress: wide(), Width: width})
			eq(t, len(lines) > 5, true)
			assertAllLinesFit(t, lines, workflowDialogContentWidth(width))
		}
	})
	tw(t, fDialog, "counts wide characters rather than code points", func(t *testing.T) {
		lines := dialogLines(dialogOver{Width: 30, Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.ToolCalls = "日本語のラベルがとても長い場合の折り返し確認", intp(3)
		})}})
		assertAllLinesFit(t, lines, 24)
	})
}

func TestDialogReplayedRows(t *testing.T) {
	tw(t, fDialog, "annotates a replayed agent's row in the agents pane", func(t *testing.T) {
		// The helper is unit-tested above; this pins that the row actually carries it, which is the thing a person
		// resuming a run looks at.
		lines := dialogLines(dialogOver{Now: dlgStart, Progress: []workflowEntry{
			dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.Cached = "audit", entryDone, true }),
			dEntry(1, func(e *workflowAgentEntry) { e.Label, e.State = "verify", entryDone }),
		}})
		var audit, verify string
		for _, l := range lines {
			if audit == "" && strings.Contains(l, "audit") {
				audit = l
			}
			if verify == "" && strings.Contains(l, "verify") {
				verify = l
			}
		}
		hasStr(t, audit, "from resume journal")
		lacksStr(t, verify, "resume journal")
	})
}

func TestDialogHeader(t *testing.T) {
	tw(t, fDialog, "carries the shared N/M agents · elapsed line and the run's subtext", func(t *testing.T) {
		lines := dialogLines(dialogOver{Now: dlgStart + 72_000, Meta: &workflowMeta{Name: "review-changes", Description: "Review changed files"},
			Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) { e.State = entryDone }),
				dEntry(1, func(e *workflowAgentEntry) { e.State, e.StartedAt = entryStart, startedAt(dlgStart) })}})
		// The run's own name leads, with the description and the counters under it: the tool's name is the card's job.
		eq(t, lines[0], " review-changes")
		hasStr(t, lines[1], "Review changed files")
		hasStr(t, lines[1], "1/2 agents · 1m12s")
	})
}

// fakeDialogHost counts the redraws the dialog asks for.
type fakeDialogHost struct {
	mu      sync.Mutex
	renders int
}

func (h *fakeDialogHost) Rows() int { return 40 }
func (h *fakeDialogHost) RequestRender() {
	h.mu.Lock()
	h.renders++
	h.mu.Unlock()
}

type dialogHarness struct {
	dialog *workflowDialog
	calls  []string
	closed []bool
}

func newDialogHarness(from func() workflowDialogSource) *dialogHarness {
	h := &dialogHarness{}
	h.dialog = newWorkflowDialog(&fakeDialogHost{}, from, tagTheme(), func() { h.closed = append(h.closed, true) }, workflowDialogActions{
		OnKill:       func() { h.calls = append(h.calls, "kill") },
		OnPause:      func() { h.calls = append(h.calls, "pause") },
		OnSkipAgent:  func(i int) { h.calls = append(h.calls, "skip:"+itoa(i)) },
		OnRetryAgent: func(i int) { h.calls = append(h.calls, "retry:"+itoa(i)) },
		OnOpenAgent:  func(id string) { h.calls = append(h.calls, "open:"+id) },
	}, 0)
	return h
}

func dlgSource(entries ...workflowEntry) func() workflowDialogSource {
	return func() workflowDialogSource {
		return workflowDialogSource{Progress: entries, Task: workflowCardTask{Status: runRunning, WorkflowName: "wf", StartTime: dlgStart}}
	}
}

func TestDialogComponent(t *testing.T) {
	doneSource := dlgSource(dEntry(7, func(e *workflowAgentEntry) { e.Label, e.State = "only", entryDone }))
	liveSource := dlgSource(dEntry(7, func(e *workflowAgentEntry) {
		e.Label, e.State, e.QueuedAt, e.StartedAt = "only", entryProgress, startedAt(dlgStart), startedAt(dlgStart)
	}))
	tw(t, fDialog, "themes the layout and keeps its line count", func(t *testing.T) {
		h := newDialogHarness(doneSource)
		defer h.dialog.Dispose()
		rendered := h.dialog.Render(86)
		src := doneSource()
		eq(t, len(rendered), len(layoutWorkflowDialog(workflowDialogInput{workflowDialogSource: src, State: initialWorkflowDialogState(0), Width: 86})))
		hasStr(t, rendered[0], "<toolTitle>*wf*</toolTitle>")
		hasStr(t, strings.Join(rendered, "\n"), "<success>✔</success>")
	})
	tw(t, fDialog, "dispatches the injected actions and closes on escape", func(t *testing.T) {
		h := newDialogHarness(liveSource)
		defer h.dialog.Dispose()
		for _, k := range []string{"x", "p", "s", "r"} {
			h.dialog.HandleInput(k)
		}
		eq(t, h.calls, []string{"kill", "pause", "skip:7", "retry:7"})
		eq(t, len(h.closed), 0)
		h.dialog.HandleInput("\x1b")
		eq(t, h.closed, []bool{true})
	})
	tw(t, fDialog, "hands the record id to the injected opener", func(t *testing.T) {
		h := newDialogHarness(dlgSource(dEntry(7, func(e *workflowAgentEntry) { e.Label, e.State, e.RecordID = "only", entryDone, "rec-7" })))
		defer h.dialog.Dispose()
		h.dialog.HandleInput("c")
		eq(t, h.calls, []string{"open:rec-7"})
		// Opening a conversation is not closing the dialog: the reader comes back to the run they were looking at.
		eq(t, len(h.closed), 0)
	})
	tw(t, fDialog, "keeps its state across keypresses", func(t *testing.T) {
		h := newDialogHarness(doneSource)
		defer h.dialog.Dispose()
		// Opening a phase is state the next render has to still be in.
		h.dialog.HandleInput("\r")
		found := false
		for _, l := range h.dialog.Render(86) {
			found = found || strings.Contains(l, "Prompt")
		}
		eq(t, found, true)
	})
}

func TestDialogKeyHints(t *testing.T) {
	live := func() []workflowEntry {
		return []workflowEntry{dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.StartedAt = "a", entryProgress, startedAt(dlgStart) })}
	}
	// Wide enough that the footer is never clipped: these assert which hints are present, not how they truncate.
	last := func(lines []string) string { return lines[len(lines)-1] }
	hintLine := func(over dialogOver) string {
		over.Width = 200
		return last(dialogLines(over))
	}
	agentHints := func(over dialogOver) string {
		prev := over.State
		over.State = func(s *workflowDialogState) {
			if prev != nil {
				prev(s)
			}
			s.Level = dialogLevelAgent
		}
		return hintLine(over)
	}
	tw(t, fDialog, "advertises the overview's own keys", func(t *testing.T) {
		hints := hintLine(dialogOver{Progress: live()})
		for _, key := range []string{"↑↓ select", "⏎ open", "f filter", "p pause", "x stop", "esc close"} {
			hasStr(t, hints, key)
		}
		// Nothing that belongs to a single agent, because none is open.
		lacksStr(t, hints, "s skip")
	})
	tw(t, fDialog, "advertises every per-agent key when availability is not declared", func(t *testing.T) {
		hints := agentHints(dialogOver{Progress: live()})
		for _, key := range []string{"↑↓ agent", "s skip", "r retry", "p pause", "x stop", "esc back"} {
			hasStr(t, hints, key)
		}
	})
	tw(t, fDialog, "hides the keys the caller did not wire", func(t *testing.T) {
		// A footer promising a key that silently does nothing is worse than no key.
		hints := agentHints(dialogOver{Progress: live(), Available: map[string]bool{"onKill": true, "onPause": false, "onResume": false, "onSkipAgent": false, "onRetryAgent": false}})
		hasStr(t, hints, "x stop")
		hasStr(t, hints, "esc back")
		lacksStr(t, hints, "s skip")
		lacksStr(t, hints, "r retry")
		lacksStr(t, hints, "p pause")
	})
	tw(t, fDialog, "offers skip but not retry on an agent that has not started", func(t *testing.T) {
		hints := agentHints(dialogOver{Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.QueuedAt = "a", entryStart, startedAt(dlgStart) })}})
		hasStr(t, hints, "s skip")
		// There is no child to stop and start again yet.
		lacksStr(t, hints, "r retry")
	})
	tw(t, fDialog, "offers neither once the agent has settled", func(t *testing.T) {
		hints := agentHints(dialogOver{Progress: []workflowEntry{dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State = "a", entryDone })}})
		lacksStr(t, hints, "s skip")
		lacksStr(t, hints, "r retry")
	})
	tw(t, fDialog, "advertises the conversation key on a row that has a record", func(t *testing.T) {
		openable := []workflowEntry{dEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.State, e.StartedAt, e.RecordID = "a", entryProgress, startedAt(dlgStart), "rec-1"
		})}
		// Both levels, matching where the key works.
		hasStr(t, agentHints(dialogOver{Progress: openable}), "c convo")
		hasStr(t, hintLine(dialogOver{Progress: openable}), "c convo")
	})
	tw(t, fDialog, "says nothing about the conversation key without a record to open", func(t *testing.T) {
		// `live` is a running agent the host has not reported a record id for.
		lacksStr(t, agentHints(dialogOver{Progress: live()}), "c convo")
	})
	tw(t, fDialog, "hides the conversation key when the caller did not wire it", func(t *testing.T) {
		openable := []workflowEntry{dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.RecordID = "a", entryDone, "rec-1" })}
		lacksStr(t, agentHints(dialogOver{Progress: openable, Available: map[string]bool{"onOpenAgent": false}}), "c convo")
	})
	// Every per-agent key live at once: running, openable, long enough prompt.
	everyKey := func() []workflowEntry {
		var lines []string
		for i := range promptCollapsedLines + 2 {
			lines = append(lines, "line "+itoa(i))
		}
		return []workflowEntry{dEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.State, e.StartedAt, e.RecordID, e.PromptPreview = "a", entryProgress, startedAt(dlgStart), "rec-1", strings.Join(lines, "\n")
		})}
	}
	atAgent := func(s *workflowDialogState) { s.Level = dialogLevelAgent }
	tw(t, fDialog, "drops the conversation hint before the way out on a narrow terminal", func(t *testing.T) {
		// The footer is clamped, not wrapped, so a hint added at the end costs whatever was already there. `c convo`
		// is last precisely so that an 80-column terminal loses it rather than `esc back`.
		narrow := last(dialogLines(dialogOver{Progress: everyKey(), Width: 80, State: atAgent}))
		hasStr(t, narrow, "esc back")
	})
	tw(t, fDialog, "shows every hint once the terminal has room", func(t *testing.T) {
		wide := last(dialogLines(dialogOver{Progress: everyKey(), Width: 100, State: atAgent}))
		for _, key := range []string{"↑↓ agent", "⏎ prompt", "s skip", "r retry", "p pause", "x stop", "esc back", "c convo"} {
			hasStr(t, wide, key)
		}
		lacksStr(t, wide, "…")
	})
	tw(t, fDialog, "hides stop when kill is not wired", func(t *testing.T) {
		lacksStr(t, hintLine(dialogOver{Progress: live(), Available: map[string]bool{"onKill": false}}), "x stop")
	})
	tw(t, fDialog, "hides resume on a paused run when resume is not wired", func(t *testing.T) {
		lacksStr(t, hintLine(dialogOver{Progress: live(), Task: taskStatus(runPaused), Available: map[string]bool{"onResume": false}}), "p resume")
	})
}

func TestDialogComponentAvailability(t *testing.T) {
	liveSource := dlgSource(dEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.StartedAt = "a", entryProgress, startedAt(dlgStart) }))
	// footer is the subview's, where the per-agent keys are advertised.
	footer := func(d *workflowDialog) string {
		d.HandleInput("\r")
		lines := d.Render(200)
		return lines[len(lines)-1]
	}
	host := &fakeDialogHost{}
	tw(t, fDialog, "advertises only the actions it was given", func(t *testing.T) {
		// Wiring just onKill must not promise skip/retry/pause.
		d := newWorkflowDialog(host, liveSource, tagTheme(), func() {}, workflowDialogActions{OnKill: func() {}}, 0)
		defer d.Dispose()
		hints := footer(d)
		hasStr(t, hints, "x stop")
		lacksStr(t, hints, "s skip")
		lacksStr(t, hints, "r retry")
		lacksStr(t, hints, "p pause")
	})
	tw(t, fDialog, "advertises the full set when every action is wired", func(t *testing.T) {
		d := newWorkflowDialog(host, liveSource, tagTheme(), func() {}, workflowDialogActions{OnKill: func() {}, OnPause: func() {}, OnResume: func() {},
			OnSkipAgent: func(int) {}, OnRetryAgent: func(int) {}, OnOpenAgent: func(string) {}}, 0)
		defer d.Dispose()
		hints := footer(d)
		for _, key := range []string{"s skip", "r retry", "p pause", "x stop"} {
			hasStr(t, hints, key)
		}
	})
	tw(t, fDialog, "advertises nothing actionable when no actions are wired", func(t *testing.T) {
		d := newWorkflowDialog(host, liveSource, tagTheme(), func() {}, workflowDialogActions{}, 0)
		defer d.Dispose()
		hints := footer(d)
		for _, key := range []string{"s skip", "r retry", "p pause", "x stop"} {
			lacksStr(t, hints, key)
		}
		hasStr(t, hints, "esc back")
	})
}
