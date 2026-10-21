package tintinweb_subagents

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twins of the original's test/workflow-render.test.ts: the inline workflow card.
const fWorkflowRender = "workflow-render"

const cardStart int64 = 1_000_000

func aEntry(index int, mut ...func(*workflowAgentEntry)) *workflowAgentEntry {
	e := &workflowAgentEntry{Type: "workflow_agent", Index: index, Label: "agent-" + itoa(index), PhaseIndex: intp(0), State: entryStart}
	for _, m := range mut {
		m(e)
	}
	return e
}

// card lays out a card at width 120 and now = START + 1000 unless the input says otherwise, as plain text.
func card(in workflowCardInput) []string {
	if in.Width == 0 {
		in.Width = 120
	}
	if in.Now == 0 {
		in.Now = cardStart + 1000
	}
	if in.Task.Status == "" {
		in.Task.Status = runRunning
	}
	if in.Task.WorkflowName == "" {
		in.Task.WorkflowName = "review-changes"
	}
	if in.Task.StartTime == 0 {
		in.Task.StartTime = cardStart
	}
	return plainWorkflowCardLines(layoutWorkflowCard(in))
}

var treeRowPattern = regexp.MustCompile("[╭╰├└,`|]")

// treeRows are the tree rows only, with the leading indent removed so assertions read short.
func treeRows(lines []string) []string {
	var out []string
	for _, l := range lines {
		if treeRowPattern.MatchString(l) {
			out = append(out, l[2:])
		}
	}
	return out
}

func phaseEntry(index int, title string) workflowEntry { return newPhaseEntry(index, title) }

func TestWorkflowCardInlineGlyphMapping(t *testing.T) {
	progress := func() []workflowEntry {
		return []workflowEntry{
			aEntry(0, func(e *workflowAgentEntry) { e.Label, e.State = "done", entryDone }),
			aEntry(1, func(e *workflowAgentEntry) { e.Label, e.State = "failed", entryError }),
			aEntry(2, func(e *workflowAgentEntry) { e.Label, e.State = "started", entryStart }),
			aEntry(3, func(e *workflowAgentEntry) { e.Label, e.State = "progressing", entryProgress }),
		}
	}
	tw(t, fWorkflowRender, "keys off the raw entry state, not the display state", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: progress()}))[1:]
		hasStr(t, rows[0], "✔ done")
		hasStr(t, rows[1], "✘ failed")
		hasStr(t, rows[2], "⟳ started")
		hasStr(t, rows[3], "⟳ progressing")
	})
	tw(t, fWorkflowRender, "does not use the /workflows dialog's queued glyph for a queued agent", func(t *testing.T) {
		q := cardStart
		lines := card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.QueuedAt = "waiting", entryStart, &q })}})
		lacksStr(t, strings.Join(lines, "\n"), "◌")
		hasStr(t, treeRows(lines)[1], "⟳ waiting")
	})
	tw(t, fWorkflowRender, "says a replayed agent came from the resume journal", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.State, e.Cached, e.AgentType = "audit", entryDone, true, "general-purpose"
		})}})
		row := treeRows(lines)[1]
		// Ahead of the stat tail, so the row reads "why" before "how much".
		hasStr(t, row, "from resume journal")
		eq(t, strings.Index(row, "from resume journal") < strings.Index(row, "general-purpose"), true)
	})
	tw(t, fWorkflowRender, "leaves an agent that actually ran unannotated", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.AgentType = "audit", entryDone, "general-purpose" })}})
		lacksStr(t, treeRows(lines)[1], "resume journal")
	})
	tw(t, fWorkflowRender, "renders a skipped and a blocked agent as a plain cross (the dialog splits them, this does not)", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{
			aEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.Skipped = "skipped", entryError, true }),
			aEntry(1, func(e *workflowAgentEntry) { e.Label, e.State, e.Blocked = "blocked", entryError, true }),
		}})
		rows := treeRows(lines)[1:]
		hasStr(t, rows[0], "✘ skipped")
		hasStr(t, rows[1], "✘ blocked")
	})
	tw(t, fWorkflowRender, "colours done success, error error, and leaves a running row at the terminal default", func(t *testing.T) {
		styled := styleWorkflowCardLines(layoutWorkflowCard(workflowCardInput{Progress: progress(),
			Task: workflowCardTask{Status: runRunning, WorkflowName: "wf", StartTime: cardStart}, Now: cardStart}), tagTheme())
		find := func(sub string) string {
			for _, l := range styled {
				if strings.Contains(l, sub) {
					return l
				}
			}
			return ""
		}
		hasStr(t, find("done"), "<success>✔</success>")
		hasStr(t, find("failed"), "<error>✘</error>")
		running := find("started")
		hasStr(t, running, "⟳")
		lacksStr(t, running, ">⟳<")
	})
}

func TestWorkflowCardTreeBranches(t *testing.T) {
	progress := func() []workflowEntry {
		return []workflowEntry{
			phaseEntry(0, "Review"), phaseEntry(1, "Verify"),
			aEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.State = "review:bugs", intp(0), entryDone }),
			aEntry(1, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex = "review:perf", intp(0) }),
			aEntry(2, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex = "verify:auth.ts", intp(1) }),
		}
	}
	tw(t, fWorkflowRender, "uses ├─ for every row but the last in a group, and └─ for the last", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: progress()}))
		eq(t, regexp.MustCompile(`^│ ├─ `).MatchString(rows[1]), true)
		eq(t, regexp.MustCompile(`^│ └─ `).MatchString(rows[2]), true)
		eq(t, regexp.MustCompile(`^ {2}└─ `).MatchString(rows[4]), true)
	})
	tw(t, fWorkflowRender, "opens the first group with ╭─ and closes the last with ╰─", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: progress()}))
		eq(t, rows[0], "╭─ Review")
		eq(t, rows[3], "╰─ Verify")
	})
	tw(t, fWorkflowRender, "branches the middle groups instead of re-opening the box", func(t *testing.T) {
		// Three phases and no agents is what a run looks like the moment it starts, and every non-final group used to
		// draw ╭─, so it read as a stack of half-drawn boxes rather than one tree.
		rows := treeRows(card(workflowCardInput{Progress: []workflowEntry{}, Meta: &workflowMeta{Name: "src-vuln-scan", Description: "d",
			Phases: []workflowPhaseMeta{{Title: "Discover"}, {Title: "Scan"}, {Title: "Verify"}}}}))
		eq(t, rows, []string{"╭─ Discover", "├─ Scan", "╰─ Verify"})
	})
	tw(t, fWorkflowRender, "still draws a lone group as a closed box", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: []workflowEntry{}, Meta: &workflowMeta{Name: "n", Description: "d", Phases: []workflowPhaseMeta{{Title: "Only"}}}}))
		eq(t, rows, []string{"╰─ Only"})
	})
	tw(t, fWorkflowRender, "hangs rows off a │ rail under a non-final group and off blanks under the final one", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: progress()}))
		eq(t, strings.HasPrefix(rows[1], "│"), true)
		eq(t, strings.HasPrefix(rows[4], "│"), false)
	})
	tw(t, fWorkflowRender, "collapses unphased agents into a single Agents group", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: []workflowEntry{
			aEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex = "solo", nil }),
			aEntry(1, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex = "solo2", nil }),
		}}))
		eq(t, rows[0], "╰─ Agents")
		eq(t, regexp.MustCompile(`^ {2}├─ ⟳ solo`).MatchString(rows[1]), true)
		eq(t, regexp.MustCompile(`^ {2}└─ ⟳ solo2`).MatchString(rows[2]), true)
	})
}

// #182 reaches the workflow surfaces: a model or level the call asked for and did not get is disclosed beside the
// effective one, never silently replaced by it.
func TestWorkflowCardEffectiveVsRequested(t *testing.T) {
	tw(t, fWorkflowRender, "names the model alone when the request was honoured", func(t *testing.T) {
		eq(t, formatWorkflowModel(aEntry(0, func(e *workflowAgentEntry) { e.Model = "haiku 4.5" }), false), "haiku 4.5")
	})
	tw(t, fWorkflowRender, "discloses a model an agent file pinned over the call's", func(t *testing.T) {
		eq(t, formatWorkflowModel(aEntry(0, func(e *workflowAgentEntry) { e.Model, e.RequestedModel = "haiku 4.5", "opus" }), false), "haiku 4.5 (asked opus)")
	})
	tw(t, fWorkflowRender, "says nothing when the requested model is the one that ran", func(t *testing.T) {
		// Disclosing a request that WAS honoured would be noise on every row.
		eq(t, formatWorkflowModel(aEntry(0, func(e *workflowAgentEntry) { e.Model, e.RequestedModel = "haiku 4.5", "haiku 4.5" }), false), "haiku 4.5")
	})
	tw(t, fWorkflowRender, "discloses a thinking level pi clamped", func(t *testing.T) {
		eq(t, formatWorkflowThinking(aEntry(0, func(e *workflowAgentEntry) { e.Thinking, e.RequestedThinking = "low", "max" })), "thinking: low (asked max)")
	})
	tw(t, fWorkflowRender, "renders the level alone when it was honoured", func(t *testing.T) {
		eq(t, formatWorkflowThinking(aEntry(0, func(e *workflowAgentEntry) { e.Thinking, e.RequestedThinking = "high", "high" })), "thinking: high")
	})
	tw(t, fWorkflowRender, "has nothing to say when no level is known", func(t *testing.T) {
		eq(t, formatWorkflowThinking(aEntry(0)), "")
	})
	tw(t, fWorkflowRender, "keeps the thinking level off the tight card row", func(t *testing.T) {
		// It lives in the dialog's detail pane instead: `thinking: medium` on every row of a fan-out is width the
		// description needs more.
		eq(t, workflowAgentStatSegments(aEntry(0, func(e *workflowAgentEntry) { e.AgentType, e.Model, e.Thinking = "Explore", "haiku", "low" })), []string{"Explore", "haiku"})
	})
}

func TestWorkflowCardStatSegments(t *testing.T) {
	tw(t, fWorkflowRender, "appends agentType, model, tokens, toolCalls, durationMs in that order", func(t *testing.T) {
		eq(t, workflowAgentStatSegments(aEntry(0, func(e *workflowAgentEntry) {
			e.AgentType, e.Model, e.Tokens, e.ToolCalls, e.DurationMs = "Explore", "haiku", intp(18_400), intp(12), i64p(42_000)
		})), []string{"Explore", "haiku", "18.4k", "12 tool calls", "42s"})
	})
	tw(t, fWorkflowRender, "omits every stat that is absent", func(t *testing.T) {
		eq(t, len(workflowAgentStatSegments(aEntry(0))), 0)
		eq(t, workflowAgentStatSegments(aEntry(0, func(e *workflowAgentEntry) { e.ToolCalls = intp(1) })), []string{"1 tool call"})
	})
	tw(t, fWorkflowRender, "merges the fallback model into the model segment", func(t *testing.T) {
		eq(t, workflowAgentStatSegments(aEntry(0, func(e *workflowAgentEntry) { e.Model, e.FallbackModel = "haiku", "sonnet" })), []string{"haiku→sonnet"})
		eq(t, workflowAgentStatSegments(aEntry(0, func(e *workflowAgentEntry) { e.Model, e.FallbackModel = "haiku", "haiku" })), []string{"haiku"})
		eq(t, workflowAgentStatSegments(aEntry(0, func(e *workflowAgentEntry) { e.FallbackModel = "sonnet" })), []string{"sonnet"})
	})
	tw(t, fWorkflowRender, "renders the segments · separated after the label", func(t *testing.T) {
		rows := treeRows(card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.State, e.AgentType, e.Model = "review:bugs", entryDone, "Explore", "haiku"
			e.Tokens, e.ToolCalls, e.DurationMs = intp(18_400), intp(12), i64p(42_000)
		})}}))
		eq(t, strings.TrimRight(rows[1], " "), "  └─ ✔ review:bugs · Explore · haiku · 18.4k · 12 tool calls · 42s")
	})
	tw(t, fWorkflowRender, "aligns stats into one column across groups", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{
			phaseEntry(0, "Review"), phaseEntry(1, "Verify"),
			aEntry(0, func(e *workflowAgentEntry) { e.Label, e.PhaseIndex, e.ToolCalls = "short", intp(0), intp(1) }),
			aEntry(1, func(e *workflowAgentEntry) {
				e.Label, e.PhaseIndex, e.ToolCalls = "a-much-longer-label", intp(1), intp(2)
			}),
		}})
		var columns []int
		for _, l := range treeRows(lines) {
			if strings.Contains(l, " · ") {
				columns = append(columns, len([]rune(l[:strings.Index(l, " · ")])))
			}
		}
		eq(t, len(columns), 2)
		eq(t, columns[0], columns[1])
	})
}

func i64p(n int64) *int64 { return &n }

func TestWorkflowCardASCIITier(t *testing.T) {
	tw(t, fWorkflowRender, "swaps the tick and cross for √ and ×", func(t *testing.T) {
		lines := card(workflowCardInput{ASCII: true, Progress: []workflowEntry{
			aEntry(0, func(e *workflowAgentEntry) { e.Label, e.State = "done", entryDone }),
			aEntry(1, func(e *workflowAgentEntry) { e.Label, e.State = "failed", entryError }),
		}})
		joined := strings.Join(lines, "\n")
		hasStr(t, joined, "√ done")
		hasStr(t, joined, "× failed")
		lacksStr(t, joined, "✔")
		lacksStr(t, joined, "✘")
	})
	tw(t, fWorkflowRender, "swaps the box drawing too, keeping each glyph's column width", func(t *testing.T) {
		joined := strings.Join(card(workflowCardInput{ASCII: true, Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) { e.Label = "x" })}}), "\n")
		eq(t, regexp.MustCompile("[╭╰├└│⎿▸⟳]").MatchString(joined), false)
		for _, g := range []string{asciiWorkflowGlyphs.groupTop, asciiWorkflowGlyphs.groupBottom, asciiWorkflowGlyphs.branch, asciiWorkflowGlyphs.lastBranch,
			asciiWorkflowGlyphs.running, asciiWorkflowGlyphs.tick, asciiWorkflowGlyphs.cross} {
			eq(t, widthx.VisibleWidth(g) > 0, true)
		}
	})
}

func TestWorkflowCardHeader(t *testing.T) {
	sevenAgents := func() []workflowEntry {
		out := make([]workflowEntry, 7)
		for i := range out {
			i := i
			out[i] = aEntry(i, func(e *workflowAgentEntry) {
				if i < 3 {
					e.State = entryDone
				} else {
					e.State = entryStart
				}
			})
		}
		return out
	}
	tw(t, fWorkflowRender, "reads N/M agents · elapsed, with no phase count", func(t *testing.T) {
		header := card(workflowCardInput{Progress: append([]workflowEntry{phaseEntry(0, "Review")}, sevenAgents()...), Now: cardStart + 72_000})[0]
		hasStr(t, header, "3/7 agents · 1m12s")
		eq(t, regexp.MustCompile(`(?i)phase`).MatchString(header), false)
		eq(t, regexp.MustCompile(`1/1|\d+ phases?`).MatchString(header), false)
	})
	tw(t, fWorkflowRender, "names the workflow, and the tool only when it stands alone", func(t *testing.T) {
		// As a tool result there is a `▸ SubagentWorkflow …` call line directly above, so repeating it here put two
		// near-identical pointer lines back to back. A session entry has no such line and asks for the prefix.
		asResult := card(workflowCardInput{Progress: sevenAgents()})[0]
		eq(t, regexp.MustCompile(`^ {2}review-changes`).MatchString(asResult), true)
		lacksStr(t, asResult, "SubagentWorkflow")
		standalone := card(workflowCardInput{Progress: sevenAgents(), ShowToolTitle: true})[0]
		eq(t, regexp.MustCompile(`^▸ SubagentWorkflow {2}review-changes`).MatchString(standalone), true)
	})
	tw(t, fWorkflowRender, "appends the terminal suffix for each stopped status", func(t *testing.T) {
		for status, suffix := range map[string]string{runRunning: "", runCompleted: " · done", runKilled: " · stopped", runPaused: " · paused", runFailed: " · failed"} {
			header := card(workflowCardInput{Progress: sevenAgents(), Task: workflowCardTask{Status: status, StartTime: cardStart}})[0]
			hasStr(t, header, "3/7 agents · 1s"+suffix)
		}
	})
	tw(t, fWorkflowRender, "shows meta.description as the subtext line", func(t *testing.T) {
		meta := &workflowMeta{Name: "review-changes", Description: "Review changed files across dimensions, verify each finding"}
		eq(t, card(workflowCardInput{Progress: sevenAgents(), Meta: meta})[1], "  Review changed files across dimensions, verify each finding")
	})
	tw(t, fWorkflowRender, "right-aligns the stats to the card width", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: sevenAgents(), Width: 60})
		eq(t, widthx.VisibleWidth(lines[0]), 60)
		eq(t, strings.HasSuffix(lines[0], "3/7 agents · 1s"), true)
	})
}

func TestWorkflowCardLogLines(t *testing.T) {
	tw(t, fWorkflowRender, "renders below the tree with a ⎿ prefix", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) { e.Label = "a" }), newLogEntry("scanned 41 changed files")}})
		eq(t, lines[len(lines)-1], "  ⎿  scanned 41 changed files")
	})
	tw(t, fWorkflowRender, "keeps every log in order and indents continuation lines under the first", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{newLogEntry("first"), newLogEntry("second\nwrapped")}})
		eq(t, lines[len(lines)-3:], []string{"  ⎿  first", "  ⎿  second", "     wrapped"})
	})
}

func TestWorkflowCardWidth(t *testing.T) {
	wide := func() []workflowEntry {
		return []workflowEntry{
			phaseEntry(0, "A phase title that runs on well past any sane terminal width"),
			aEntry(0, func(e *workflowAgentEntry) {
				e.Label = "an-extremely-long-agent-label-that-would-otherwise-wrap-the-whole-card"
				e.AgentType, e.Model, e.Tokens, e.ToolCalls, e.DurationMs = "general-purpose", "claude-opus-4-5-20260101", intp(1_240_000), intp(412), i64p(3_723_000)
			}),
			newLogEntry("a log line long enough to overflow a narrow terminal on its own"),
		}
	}
	tw(t, fWorkflowRender, "never exceeds ${width} columns", func(t *testing.T) {
		for _, width := range []int{20, 40, 80} {
			lines := card(workflowCardInput{Progress: wide(), Width: width, Task: workflowCardTask{Status: runRunning, StartTime: cardStart}})
			eq(t, len(lines) > 3, true)
			assertAllLinesFit(t, lines, width)
		}
	})
	tw(t, fWorkflowRender, "keeps the header stats when the name has to be cut", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: wide(), Width: 34, Task: workflowCardTask{Status: runRunning, WorkflowName: "a-workflow-name-far-too-long-to-fit", StartTime: cardStart}})
		hasStr(t, lines[0], "0/1 agent · 1s")
		hasStr(t, lines[0], "…")
		eq(t, widthx.VisibleWidth(lines[0]), 34)
	})
	tw(t, fWorkflowRender, "counts wide characters rather than code points", func(t *testing.T) {
		lines := card(workflowCardInput{Width: 30, Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) {
			e.Label, e.ToolCalls = "日本語のラベルがとても長い場合の折り返し確認", intp(3)
		})}})
		assertAllLinesFit(t, lines, 30)
	})
}

func TestWorkflowCardSizeWarning(t *testing.T) {
	tw(t, fWorkflowRender, "appears once the scheduled agents pass the cap, with Claude Code's wording", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) { e.Label = "a" })}, AgentCount: 40})
		eq(t, lines[len(lines)-1], "  ⚠ Large workflow · /agents → Workflows to stop")
	})
	tw(t, fWorkflowRender, "stays away for a small run", func(t *testing.T) {
		lines := card(workflowCardInput{Progress: []workflowEntry{aEntry(0, func(e *workflowAgentEntry) { e.Label = "a" })}})
		lacksStr(t, strings.Join(lines, "\n"), "Large workflow")
	})
}

func TestWorkflowCardComponentRendering(t *testing.T) {
	tw(t, fWorkflowRender, "themes the card and hands back one Text of the same lines", func(t *testing.T) {
		end := cardStart + 42_000
		in := workflowCardInput{
			Progress: []workflowEntry{phaseEntry(0, "Review"), aEntry(0, func(e *workflowAgentEntry) { e.Label, e.State, e.DurationMs = "review:bugs", entryDone, i64p(42_000) })},
			Task:     workflowCardTask{Status: runCompleted, WorkflowName: "review-changes", StartTime: cardStart, EndTime: &end},
			Now:      cardStart + 42_000, Width: 80,
		}
		// Rendered wide: the fake theme's markup is visible width, unlike real ANSI, so a realistic width would wrap
		// lines the terminal never would.
		rendered := renderWorkflowCard(in, tagTheme(), 400)
		eq(t, len(rendered), len(layoutWorkflowCard(in)))
		hasStr(t, rendered[0], "<toolTitle>*review-changes*</toolTitle>")
		hasStr(t, rendered[0], "<dim>1/1 agent · 42s · done</dim>")
		hasStr(t, strings.Join(rendered, "\n"), "<success>✔</success>")
	})
}
