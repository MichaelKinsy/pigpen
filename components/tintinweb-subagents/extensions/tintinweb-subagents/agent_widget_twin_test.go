package tintinweb_subagents

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Twins of test/agent-widget.test.ts.

func TestAgentWidgetTwins(t *testing.T) {
	const file = "agent-widget"

	t.Run("formatSessionTokens", func(t *testing.T) {
		th := tagTheme()
		ansi := fnTheme{
			fg: func(c, s string) string {
				codes := map[string]string{"dim": "2", "warning": "33", "accent": "35"}
				code, ok := codes[c]
				if !ok {
					code = "31"
				}
				return "\x1b[" + code + "m" + s + "\x1b[39m"
			},
			bold: func(s string) string { return s },
		}
		pct := func(n float64) *float64 { return &n }
		tw(t, file, "applies threshold colors (<70 dim, 70–85 warning, ≥85 error)", func(t *testing.T) {
			eqStr(t, formatSessionTokens(1234, nil, th, 0), "1.2k token")
			eqStr(t, formatSessionTokens(1234, pct(50), th, 0), "1.2k token (<dim>50%</dim>)")
			eqStr(t, formatSessionTokens(1234, pct(70), th, 0), "1.2k token (<warning>70%</warning>)")
			eqStr(t, formatSessionTokens(1234, pct(84), th, 0), "1.2k token (<warning>84%</warning>)")
			eqStr(t, formatSessionTokens(1234, pct(85), th, 0), "1.2k token (<error>85%</error>)")
			eqStr(t, formatSessionTokens(1234, pct(99), th, 0), "1.2k token (<error>99%</error>)")
		})
		tw(t, file, "annotates compaction count alongside percent", func(t *testing.T) {
			eqStr(t, formatSessionTokens(1234, nil, th, 1), "1.2k token (<dim>⇊1</dim>)")
			eqStr(t, formatSessionTokens(1234, nil, th, 3), "1.2k token (<dim>⇊3</dim>)")
			eqStr(t, formatSessionTokens(1234, pct(45), th, 2), "1.2k token (<dim>45%</dim> · <dim>⇊2</dim>)")
			eqStr(t, formatSessionTokens(1234, pct(88), th, 4), "1.2k token (<error>88%</error> · <dim>⇊4</dim>)")
			eqStr(t, formatSessionTokens(1234, pct(45), th, 0), "1.2k token (<dim>45%</dim>)")
		})
		tw(t, file, "preserves the outer style after nested annotation styles reset", func(t *testing.T) {
			tokenText := formatSessionTokens(1234, pct(70), ansi, 0)
			eqStr(t, fgPreservingNestedStyles(ansi, "accent", tokenText), "\x1b[35m1.2k token (\x1b[33m70%\x1b[39m\x1b[35m)\x1b[39m")
		})
	})

	t.Run("renderRunningAgentStatus", func(t *testing.T) {
		tw(t, file, "renders running status as separate component lines", func(t *testing.T) {
			th := fnTheme{fg: func(_, s string) string { return s }, bold: func(s string) string { return s }}
			lines := renderRunningAgentStatus("⠋", "thinking: xhigh · 4 tool uses", "thinking…", th, 120)
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " ")
			}
			eqStr(t, strings.Join(lines, "|"), "⠋ thinking: xhigh · 4 tool uses|  ⎿  thinking…")
		})
	})

	t.Run("AgentWidget", func(t *testing.T) {
		th := plainTheme()
		makeRecord := func(id string, isBackground *bool, parent, workflow string) *agentView {
			return &agentView{ID: id, Type: "general-purpose", Description: id + " description", Status: "running", StartedAt: nowMs(),
				Invocation:   &invocation{ModelName: "sonnet 4.6", ModelID: "anthropic/claude-sonnet-4-6", Thinking: "high"},
				IsBackground: isBackground, ParentAgentID: parent, WorkflowID: workflow}
		}
		// renderLines renders the widget for a manager ("" if nothing rendered).
		renderLines := func(agents []*agentView, activityID string, mode func() widgetMode, showModel bool) string {
			w := newAgentWidget(&fixedList{agents: agents}, activityMap{activityID: {TurnCount: 1}}, mode, func() bool { return false }, func() bool { return showModel })
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			return ui.frame(120, th)
		}
		modeOf := func(m widgetMode) func() widgetMode { return func() widgetMode { return m } }

		tw(t, file, "shows foreground agents in 'all' mode (and by default)", func(t *testing.T) {
			agents := []*agentView{makeRecord("foreground", boolp(false), "", "")}
			hasStr(t, renderLines(agents, "foreground", nil, false), "foreground description")
			hasStr(t, renderLines(agents, "foreground", modeOf(widgetModeAll), false), "foreground description")
		})
		tw(t, file, "hides nested children in every coordinator widget mode", func(t *testing.T) {
			agents := []*agentView{makeRecord("nested", boolp(true), "parent", "")}
			eqStr(t, renderLines(agents, "nested", modeOf(widgetModeAll), false), "")
			eqStr(t, renderLines(agents, "nested", modeOf(widgetModeBackground), false), "")
		})
		tw(t, file, "hides a workflow's agents in every coordinator widget mode", func(t *testing.T) {
			agents := []*agentView{makeRecord("child", boolp(true), "", "wf_abc")}
			eqStr(t, renderLines(agents, "child", modeOf(widgetModeAll), false), "")
			eqStr(t, renderLines(agents, "child", modeOf(widgetModeBackground), false), "")
		})
		tw(t, file, "excludes foreground agents in 'background' mode", func(t *testing.T) {
			agents := []*agentView{makeRecord("foreground", boolp(false), "", "")}
			eqStr(t, renderLines(agents, "foreground", modeOf(widgetModeBackground), false), "")
		})
		tw(t, file, "renders background agents in 'background' mode", func(t *testing.T) {
			agents := []*agentView{makeRecord("background", boolp(true), "", "")}
			lines := renderLines(agents, "background", modeOf(widgetModeBackground), false)
			hasStr(t, lines, "Agents")
			hasStr(t, lines, "background description")
		})
		tw(t, file, "keeps agents with no isBackground flag in 'background' mode", func(t *testing.T) {
			agents := []*agentView{makeRecord("unflagged", nil, "", "")}
			hasStr(t, renderLines(agents, "unflagged", modeOf(widgetModeBackground), false), "unflagged description")
		})
		tw(t, file, "names the model and thinking on a running row under showModel", func(t *testing.T) {
			agents := []*agentView{makeRecord("bg", boolp(true), "", "")}
			hasStr(t, renderLines(agents, "bg", modeOf(widgetModeBackground), true), "sonnet 4.6 · thinking: high")
		})
		tw(t, file, "renders the row exactly as before when showModel is off", func(t *testing.T) {
			agents := []*agentView{makeRecord("bg", boolp(true), "", "")}
			off := renderLines(agents, "bg", modeOf(widgetModeBackground), false)
			hasStr(t, off, "bg description")
			lacksStr(t, off, "sonnet 4.6")
			lacksStr(t, off, "thinking:")
		})
		tw(t, file, "carries the short label, never the canonical id, onto the row", func(t *testing.T) {
			agents := []*agentView{makeRecord("bg", boolp(true), "", "")}
			lacksStr(t, renderLines(agents, "bg", modeOf(widgetModeBackground), true), "anthropic/claude-sonnet-4-6")
		})
		tw(t, file, "discloses a level the run did not honor", func(t *testing.T) {
			rec := makeRecord("bg", boolp(true), "", "")
			rec.Invocation = &invocation{ModelName: "haiku 4.5", Thinking: "high", RequestedThinking: "max"}
			hasStr(t, renderLines([]*agentView{rec}, "bg", modeOf(widgetModeBackground), true), "haiku 4.5 · thinking: high (asked max)")
		})
		tw(t, file, "keeps queued agents on one summary line and finished agents visible", func(t *testing.T) {
			var records []*agentView
			for i := 1; i <= 3; i++ {
				records = append(records, makeRecord(fmt.Sprintf("run%d", i), boolp(true), "", ""))
			}
			for i := 1; i <= 7; i++ {
				r := makeRecord(fmt.Sprintf("q%d", i), boolp(true), "", "")
				r.Status = "queued"
				records = append(records, r)
			}
			for i := 1; i <= 3; i++ {
				r := makeRecord(fmt.Sprintf("fin%d", i), boolp(true), "", "")
				r.Status, r.CompletedAt = "completed", nowMs()
				records = append(records, r)
			}
			w := newAgentWidget(&fixedList{agents: records}, nil, modeOf(widgetModeBackground), func() bool { return false }, func() bool { return true })
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			for _, r := range records {
				if r.Status == "completed" {
					w.markFinished(r.ID)
				}
			}
			w.update()
			lines := ui.frame(200, th)
			hasStr(t, lines, "7 queued")
			lacksStr(t, lines, "q1 description")
			for i := 1; i <= 3; i++ {
				hasStr(t, lines, fmt.Sprintf("fin%d description", i))
			}
			lacksStr(t, lines, "more (")
		})
		tw(t, file, "renders nothing in 'off' mode", func(t *testing.T) {
			agents := []*agentView{makeRecord("background", boolp(true), "", "")}
			eqStr(t, renderLines(agents, "background", modeOf(widgetModeOff), false), "")
		})
	})

	t.Run("formatCost", func(t *testing.T) {
		tw(t, file, "keeps the precision that distinguishes one run from another", func(t *testing.T) {
			eqStr(t, formatCost(0.0042), "~$0.0042")
			eqStr(t, formatCost(0.0123), "~$0.0123")
			eqStr(t, formatCost(1.239), "~$1.24")
		})
		tw(t, file, "never pads a round figure with noise, nor cuts it below cents", func(t *testing.T) {
			eqStr(t, formatCost(0.05), "~$0.05")
			eqStr(t, formatCost(0.4), "~$0.40")
			eqStr(t, formatCost(12), "~$12.00")
		})
		tw(t, file, "shows nothing when there is nothing to show", func(t *testing.T) {
			eqStr(t, formatCost(0), "")
			eqStr(t, formatCost(math.NaN()), "")
			eqStr(t, formatCost(-1), "")
		})
		tw(t, file, "says a real but tiny cost is tiny, not zero", func(t *testing.T) {
			eqStr(t, formatCost(0.00002), "<$0.0001")
			eqStr(t, formatCost(0), "")
		})
		tw(t, file, "marks the figure as an estimate", func(t *testing.T) {
			if !strings.HasPrefix(formatCost(0.5), "~") {
				t.Error("no tilde")
			}
		})
	})

	t.Run("AgentWidget cost display", func(t *testing.T) {
		th := plainTheme()
		render := func(showCost bool, cost float64) string {
			agent := &agentView{ID: "a1", Type: "general-purpose", Description: "spending agent", Status: "running", ToolUses: 1, StartedAt: nowMs(),
				Usage: lifetimeUsage{Input: 1000, Output: 200, Cost: cost}}
			// Carries figures of its own: spend is read from the record, so nothing of this may reach the line.
			activity := activityMap{"a1": {ToolUses: 1, TurnCount: 1}}
			w := newAgentWidget(&fixedList{agents: []*agentView{agent}}, activity, func() widgetMode { return widgetModeAll }, func() bool { return showCost }, nil)
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			return ui.frame(200, th)
		}
		tw(t, file, "shows the cost beside the token count when enabled", func(t *testing.T) {
			line := render(true, 0.0042)
			hasStr(t, line, "1.2k token")
			hasStr(t, line, "~$0.0042")
		})
		tw(t, file, "shows no cost when disabled", func(t *testing.T) {
			line := render(false, 0.0042)
			hasStr(t, line, "1.2k token")
			lacksStr(t, line, "$")
		})
		tw(t, file, "shows no cost for an unpriced model, even when enabled", func(t *testing.T) {
			line := render(true, 0)
			hasStr(t, line, "1.2k token")
			lacksStr(t, line, "$")
		})
		tw(t, file, "keeps the cost visible after the agent finishes", func(t *testing.T) {
			finished := &agentView{ID: "a1", Type: "general-purpose", Description: "done agent", Status: "completed", ToolUses: 2,
				StartedAt: nowMs() - 1000, CompletedAt: nowMs(), Usage: lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0042}}
			w := newAgentWidget(&fixedList{agents: []*agentView{finished}}, nil, func() widgetMode { return widgetModeAll }, func() bool { return true }, nil)
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			out := ui.frame(200, th)
			hasStr(t, out, "done agent")
			hasStr(t, out, "~$0.0042")
		})
		tw(t, file, "shows stats for an agent nobody is tracking live", func(t *testing.T) {
			running := &agentView{ID: "sched", Type: "general-purpose", Description: "scheduled agent", Status: "running", ToolUses: 1, StartedAt: nowMs(),
				Usage: lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0042}}
			w := newAgentWidget(&fixedList{agents: []*agentView{running}}, nil, func() widgetMode { return widgetModeAll }, func() bool { return true }, nil)
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			out := ui.frame(200, th)
			hasStr(t, out, "1.2k token")
			hasStr(t, out, "~$0.0042")
		})
		tw(t, file, "defaults to hiding it", func(t *testing.T) {
			agent := &agentView{ID: "a1", Type: "general-purpose", Description: "d", Status: "running", StartedAt: nowMs(), Usage: lifetimeUsage{Input: 1000, Output: 200, Cost: 0.5}}
			w := newAgentWidget(&fixedList{agents: []*agentView{agent}}, activityMap{"a1": {TurnCount: 1}}, func() widgetMode { return widgetModeAll }, nil, nil)
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			lacksStr(t, ui.frame(200, th), "$")
		})
	})

	t.Run("AgentWidget overflow accounting", func(t *testing.T) {
		th := plainTheme()
		record := func(id, status string) *agentView {
			r := &agentView{ID: id, Type: "general-purpose", Description: id + " description", Status: status, StartedAt: nowMs(), IsBackground: boolp(true)}
			if status == "completed" {
				r.CompletedAt = nowMs()
			}
			return r
		}
		type shape struct{ running, queued, finished int }
		renderFleet := func(c shape) []string {
			var agents []*agentView
			for i := 0; i < c.running; i++ {
				agents = append(agents, record(fmt.Sprintf("run%d", i), "running"))
			}
			for i := 0; i < c.queued; i++ {
				agents = append(agents, record(fmt.Sprintf("q%d", i), "queued"))
			}
			for i := 0; i < c.finished; i++ {
				agents = append(agents, record(fmt.Sprintf("fin%d", i), "completed"))
			}
			activity := activityMap{}
			for _, a := range agents {
				activity[a.ID] = &agentActivity{TurnCount: 1}
			}
			w := newAgentWidget(&fixedList{agents: agents}, activity, func() widgetMode { return widgetModeAll }, nil, nil)
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			if ui.render == nil {
				return nil
			}
			return ui.render(200, th)
		}
		footer := func(lines []string) (string, bool) {
			for _, l := range lines {
				if strings.Contains(l, "more (") {
					return l, true
				}
			}
			return "", false
		}
		var shapes []shape
		for r := 0; r <= 8; r++ {
			for q := 0; q <= 8; q++ {
				for f := 0; f <= 8; f++ {
					shapes = append(shapes, shape{r, q, f})
				}
			}
		}
		more := regexp.MustCompile(`\+(\d+) more`)

		tw(t, file, "never exceeds the line cap, for any fleet shape", func(t *testing.T) {
			for _, c := range shapes {
				if n := len(renderFleet(c)); n > 12 {
					t.Fatalf("%+v: %d lines", c, n)
				}
			}
		})
		tw(t, file, "never prints a footer that miscounts what it hid, for any fleet shape", func(t *testing.T) {
			for _, c := range shapes {
				f, ok := footer(renderFleet(c))
				if !ok {
					continue
				}
				total, _ := strconv.Atoi(more.FindStringSubmatch(f)[1])
				if total <= 0 {
					t.Fatalf("%+v -> %s: a footer means something was dropped", c, f)
				}
				if total > c.running+c.finished {
					t.Fatalf("%+v -> %s: counts more than the agents that have a row", c, f)
				}
			}
		})
		tw(t, file, "keeps the queued summary visible when the running agents fill the widget", func(t *testing.T) {
			hasStr(t, strings.Join(renderFleet(shape{5, 3, 1}), "\n"), "3 queued")
		})
		tw(t, file, "counts everything it hid — the footer total matches what is missing", func(t *testing.T) {
			c := shape{5, 3, 1}
			lines := renderFleet(c)
			body := strings.Join(lines, "\n")
			hidden := 0
			for i := 0; i < c.running; i++ {
				if !strings.Contains(body, fmt.Sprintf("run%d description", i)) {
					hidden++
				}
			}
			for i := 0; i < c.finished; i++ {
				if !strings.Contains(body, fmt.Sprintf("fin%d description", i)) {
					hidden++
				}
			}
			f, _ := footer(lines)
			m := more.FindStringSubmatch(f)
			if m == nil {
				t.Fatalf("no footer in %q", body)
			}
			if reported, _ := strconv.Atoi(m[1]); reported != hidden {
				t.Errorf("reported %d, hidden %d", reported, hidden)
			}
		})
		tw(t, file, "gives the queued summary priority over finished lines", func(t *testing.T) {
			hasStr(t, strings.Join(renderFleet(shape{4, 2, 3}), "\n"), "2 queued")
		})
		tw(t, file, "renders everything with no footer when the fleet fits", func(t *testing.T) {
			lines := renderFleet(shape{2, 1, 1})
			hasStr(t, strings.Join(lines, "\n"), "1 queued")
			if _, ok := footer(lines); ok {
				t.Error("a footer for a fleet that fits")
			}
		})
		tw(t, file, "shows the completion line again after a finished agent is resumed", func(t *testing.T) {
			agent := record("resumed", "completed")
			w := newAgentWidget(&fixedList{agents: []*agentView{agent}}, activityMap{agent.ID: {TurnCount: 1}}, func() widgetMode { return widgetModeAll }, nil, nil)
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			render := func() string { w.update(); return ui.frame(200, th) }

			// First run finishes and ages out of the widget.
			w.markFinished(agent.ID)
			w.onTurnStart()
			w.onTurnStart()
			lacksStr(t, render(), "resumed description")

			// Background resume puts it back on the running list.
			agent.Status = "running"
			w.markRunning(agent.ID)
			hasStr(t, render(), "resumed description")

			// ...and its completion is visible when the resumed run settles.
			agent.Status = "completed"
			w.markFinished(agent.ID)
			hasStr(t, render(), "resumed description")
		})
	})
}
