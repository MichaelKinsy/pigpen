package tintinweb_subagents

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Twins of test/conversation-viewer-keybindings.test.ts, test/usage.test.ts (the display readers),
// test/agent-tool-error-rendering.test.ts, the notification group of test/cost-display.test.ts and the perf guards.

func TestViewerKeybindingsTwins(t *testing.T) {
	const file = "conversation-viewer-keybindings"
	const (
		ctrlP     = "\x10"
		ctrlN     = "\x0e"
		up        = "\x1b[A"
		down      = "\x1b[B"
		shiftUp   = "\x1b[1;2A"
		shiftDown = "\x1b[1;2B"
		pageUp    = "\x1b[5~"
		pageDown  = "\x1b[6~"
	)
	manager := func(user tui.KeybindingsConfig) viewerKeybindings {
		return tui.NewKeybindingsManager(tui.TUIKeybindings, user)
	}
	emacs := func() viewerKeybindings {
		return manager(tui.KeybindingsConfig{"tui.select.up": {"up", "ctrl+p"}, "tui.select.down": {"down", "ctrl+n"}})
	}
	createViewer := func(kb viewerKeybindings) *conversationViewer {
		var messages []obj
		for i := 0; i < 60; i++ {
			messages = append(messages, userMsg(fmt.Sprintf("message %d", i)))
		}
		v, _ := newViewer(20, mockSession(messages...), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, plainTheme(), viewerOptions{Keybindings: kb})
		v.Render(80) // sets lastInnerW and scrolls to bottom (autoScroll)
		return v
	}

	t.Run("viewer-keys", func(t *testing.T) {
		tw(t, file, "honors user keybindings when a manager is provided", func(t *testing.T) {
			keys := createViewerKeys(emacs())
			for _, c := range []struct {
				f func(string) bool
				k string
			}{{keys.scrollUp, ctrlP}, {keys.scrollUp, up}, {keys.scrollDown, ctrlN}, {keys.scrollDown, down}} {
				if !c.f(c.k) {
					t.Errorf("%q not matched", c.k)
				}
			}
		})
		tw(t, file, "falls back to hardcoded defaults without a manager", func(t *testing.T) {
			keys := createViewerKeys(nil)
			if !keys.scrollUp(up) || keys.scrollUp(ctrlP) || !keys.scrollDown(down) || keys.scrollDown(ctrlN) || !keys.pageUp(pageUp) || !keys.pageDown(pageDown) {
				t.Error("defaults differ")
			}
		})
		tw(t, file, "keeps the k/j and shift+arrow aliases with and without a manager", func(t *testing.T) {
			for _, keys := range []viewerKeys{createViewerKeys(nil), createViewerKeys(emacs())} {
				if !keys.scrollUp("k") || !keys.scrollDown("j") || !keys.pageUp(shiftUp) || !keys.pageDown(shiftDown) {
					t.Error("an alias is gone")
				}
			}
		})
		tw(t, file, "manager with no user overrides behaves like the hardcoded defaults", func(t *testing.T) {
			keys := createViewerKeys(manager(tui.KeybindingsConfig{}))
			if !keys.scrollUp(up) || !keys.scrollDown(down) || !keys.pageUp(pageUp) || !keys.pageDown(pageDown) || keys.scrollUp(ctrlP) || keys.scrollDown(ctrlN) {
				t.Error("defaults differ")
			}
		})
		tw(t, file, "respects rebinding that removes a default key", func(t *testing.T) {
			keys := createViewerKeys(manager(tui.KeybindingsConfig{"tui.select.up": {"ctrl+p"}}))
			if !keys.scrollUp(ctrlP) || keys.scrollUp(up) {
				t.Error("the rebinding was not honored")
			}
		})
	})

	t.Run("ConversationViewer custom keybindings", func(t *testing.T) {
		tw(t, file, "scrolls with ctrl+p/ctrl+n when bound to tui.select.up/down", func(t *testing.T) {
			v := createViewer(emacs())
			bottom := v.scrollOffset
			if bottom <= 0 {
				t.Fatalf("bottom %d", bottom)
			}
			v.HandleInput(ctrlP)
			if v.scrollOffset != bottom-1 {
				t.Errorf("%d", v.scrollOffset)
			}
			v.HandleInput(ctrlN)
			if v.scrollOffset != bottom {
				t.Errorf("%d", v.scrollOffset)
			}
		})
		tw(t, file, "keeps arrows and k/j working alongside custom bindings", func(t *testing.T) {
			v := createViewer(emacs())
			bottom := v.scrollOffset
			v.HandleInput(up)
			v.HandleInput("k")
			if v.scrollOffset != bottom-2 {
				t.Errorf("%d", v.scrollOffset)
			}
			v.HandleInput(down)
			v.HandleInput("j")
			if v.scrollOffset != bottom {
				t.Errorf("%d", v.scrollOffset)
			}
		})
		tw(t, file, "treats ctrl+p/ctrl+n as unbound without a keybindings manager", func(t *testing.T) {
			v := createViewer(nil)
			bottom := v.scrollOffset
			v.HandleInput(ctrlP)
			v.HandleInput(ctrlN)
			if v.scrollOffset != bottom {
				t.Errorf("%d", v.scrollOffset)
			}
			v.HandleInput(up)
			if v.scrollOffset != bottom-1 {
				t.Errorf("%d", v.scrollOffset)
			}
		})
	})
}

// statsSession is a session reporting scripted stats.
type statsSession struct {
	stats sessionStats
	err   error
}

func (s *statsSession) GetSessionStats() (sessionStats, error) { return s.stats, s.err }

func TestUsageDisplayTwins(t *testing.T) {
	const file = "usage"
	pct := func(n float64) *float64 { return &n }
	t.Run("getSessionTokens", func(t *testing.T) {
		tw(t, file, "uses billed-token semantics (input + output + cacheWrite), not inflated total", func(t *testing.T) {
			// cacheRead (500,000) and the inflated total are not part of the figure.
			s := &statsSession{stats: sessionStats{Input: 100, Output: 200, CacheWrite: 50, ContextPercent: pct(25)}}
			if got := getSessionTokens(s); got != 350 {
				t.Errorf("%d", got)
			}
		})
		tw(t, file, "returns 0 when session is undefined or stats throw", func(t *testing.T) {
			if getSessionTokens(nil) != 0 {
				t.Error("nil session")
			}
			if getSessionTokens(&statsSession{err: errors.New("nope")}) != 0 {
				t.Error("failing stats")
			}
		})
	})
	t.Run("getSessionContextPercent", func(t *testing.T) {
		tw(t, file, "returns null when contextUsage is unavailable", func(t *testing.T) {
			if getSessionContextPercent(&statsSession{stats: sessionStats{Input: 10, Output: 20, CacheWrite: 5}}) != nil {
				t.Error("not nil")
			}
		})
		tw(t, file, "returns null when percent is null (post-compaction)", func(t *testing.T) {
			if getSessionContextPercent(&statsSession{stats: sessionStats{Input: 10, Output: 20, CacheWrite: 5, ContextPercent: nil}}) != nil {
				t.Error("not nil")
			}
		})
		tw(t, file, "returns the upstream percent when available", func(t *testing.T) {
			got := getSessionContextPercent(&statsSession{stats: sessionStats{Input: 10, Output: 20, CacheWrite: 5, ContextPercent: pct(25)}})
			if got == nil || *got != 25 {
				t.Errorf("%v", got)
			}
		})
	})
	t.Run("getLifetimeTotal", func(t *testing.T) {
		tw(t, file, "sums components and handles undefined", func(t *testing.T) {
			if getLifetimeTotal(lifetimeUsage{}) != 0 || getLifetimeTotal(lifetimeUsage{Input: 100, Output: 200, CacheWrite: 50}) != 350 {
				t.Error("totals differ")
			}
		})
		tw(t, file, "agrees with getSessionTokens pre-compaction, diverges after", func(t *testing.T) {
			session := &statsSession{stats: sessionStats{Input: 100, Output: 200, CacheWrite: 50}}
			lifetime := lifetimeUsage{Input: 100, Output: 200, CacheWrite: 50}
			if getSessionTokens(session) != 350 || getLifetimeTotal(lifetime) != 350 {
				t.Fatal("pre-compaction")
			}
			// Compaction: upstream replaces session.state.messages, so stats reset. Our accumulator is independent.
			session.stats = sessionStats{}
			if getSessionTokens(session) != 0 || getLifetimeTotal(lifetime) != 350 {
				t.Fatal("at compaction")
			}
			// Subsequent message_end events feed both: the session re-fills, the accumulator continues.
			session.stats = sessionStats{Input: 80, Output: 150, CacheWrite: 30}
			lifetime.Input += 80
			lifetime.Output += 150
			lifetime.CacheWrite += 30
			if getSessionTokens(session) != 260 || getLifetimeTotal(lifetime) != 610 {
				t.Error("post-compaction")
			}
		})
		tw(t, file, "stays monotone across simulated compaction when fed via addUsage-style accumulation", func(t *testing.T) {
			var usage lifetimeUsage
			onUsage := func(in, out, cw int) { usage.Input += in; usage.Output += out; usage.CacheWrite += cw }
			for i := 0; i < 5; i++ { // 5 normal turns
				onUsage(1000, 200, 50)
			}
			if getLifetimeTotal(usage) != 5*1250 {
				t.Fatal("5 turns")
			}
			beforeCompaction := getLifetimeTotal(usage)
			for i := 0; i < 3; i++ { // 3 more turns post-"compaction"
				onUsage(800, 150, 30)
			}
			after := getLifetimeTotal(usage)
			if after != beforeCompaction+3*980 || after <= beforeCompaction {
				t.Errorf("%d", after)
			}
			// input + output + cacheWrite = total, by construction.
			if usage.Input+usage.Output+usage.CacheWrite != after {
				t.Error("drift")
			}
		})
	})
}

func TestAgentToolErrorRenderingTwins(t *testing.T) {
	const file = "agent-tool-error-rendering"
	th := plainTheme()
	tw(t, file, "shows a Pi tool error instead of structured terminal status", func(t *testing.T) {
		details := &agentDetails{Status: "aborted"}
		out := strings.Join(renderAgentResult(`Cannot run with isolation: "worktree" — Git probe failed.`, details, false, false, true, th, false, 120), "\n")
		hasStr(t, out, `Cannot run with isolation: "worktree" — Git probe failed.`)
		lacksStr(t, out, "Aborted (max turns exceeded)")
	})
	// The original's it.each (four cases) is not in the ledger: the cases run as plain subtests.
	t.Run("shows the real result text for %s", func(t *testing.T) {
		for _, c := range []struct {
			name    string
			details *agentDetails
		}{
			{"missing details", nil},
			{"empty details", &agentDetails{}},
			{"unknown status", &agentDetails{Status: "unknown"}},
			{"a status with no rendering of its own", &agentDetails{Status: "queued"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				out := strings.Join(renderAgentResult("Unstructured Agent result.", c.details, false, false, false, th, false, 120), "\n")
				hasStr(t, out, "Unstructured Agent result.")
				lacksStr(t, out, "Aborted (max turns exceeded)")
			})
		}
	})
}

func TestCostDisplayNotificationTwins(t *testing.T) {
	const file = "cost-display"
	th := plainTheme()
	render := func(showCost bool, d notificationDetails) string {
		return strings.Join(renderNotification(d, false, th, showCost, 120), "\n")
	}
	agent := func(description string, tokens int, cost float64) notificationDetails {
		return notificationDetails{ID: description, Description: description, Status: "completed", ToolUses: 1, TurnCount: 1, TotalTokens: tokens, TotalCost: cost, DurationMs: 1000, ResultPreview: "done"}
	}
	tw(t, file, "totals a group, so nobody adds four figures by hand", func(t *testing.T) {
		d := agent("first", 1000, 0.01)
		d.Others = []notificationDetails{agent("second", 3000, 0.02)}
		hasStr(t, render(true, d), "2 agents · 4.0k token · ~$0.03")
	})
	tw(t, file, "does not total a single agent — the line above already says it", func(t *testing.T) {
		out := render(true, agent("only", 1000, 0.01))
		hasStr(t, out, "~$0.01")
		lacksStr(t, out, "1 agents")
	})
	tw(t, file, "shows no total, and no per-agent cost, when unpriced", func(t *testing.T) {
		d := agent("first", 1000, 0)
		d.Others = []notificationDetails{agent("second", 3000, 0)}
		lacksStr(t, render(true, d), "$")
	})
	tw(t, file, "shows nothing when the setting is off", func(t *testing.T) {
		d := agent("first", 1000, 0.01)
		d.Others = []notificationDetails{agent("second", 3000, 0.02)}
		lacksStr(t, render(false, d), "$")
	})
}

// countingLister counts how often the manager is asked for the agent list (the perf guard of one frame).
func TestRenderInvariantsPerfTwins(t *testing.T) {
	const file = "perf/render-invariants.perf"
	// Counters for the leaves the viewer wraps its text with.
	mount := func(n int, mode viewerMarkdownMode, c *perfCounts) *conversationViewer {
		var messages []obj
		for i := 0; i < n; i++ {
			switch i % 3 {
			case 0:
				messages = append(messages, userMsg(fmt.Sprintf("question %d about the code", i)))
			case 1:
				messages = append(messages, assistantMsg(fmt.Sprintf("## Answer %d\n\nsome **bold** text and `code` for message %d", i, i)))
			default:
				messages = append(messages, resultMsg(fmt.Sprintf("result line %d\nsecond line", i)))
			}
		}
		v, _ := newViewer(40, mockSession(messages...), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, plainTheme(), viewerOptions{ViewerMarkdown: func() viewerMarkdownMode { return mode }})
		wrap := v.wrap
		v.wrap = func(s string, w int) []string { c.wrap++; return wrap(s, w) }
		p := &markdownProbe{}
		p.install(v)
		inner := v.newMarkdown
		v.newMarkdown = func(text string, th *tui.MarkdownTheme, style *tui.DefaultTextStyle) markdownComponent {
			c.mdNew++
			return &countedMarkdown{inner: inner(text, th, style), c: c}
		}
		return v
	}
	// wrapsFor is the leaf calls one render makes over a transcript of n messages, once the caches are warm.
	wrapsFor := func(n int, mode viewerMarkdownMode) int {
		var c perfCounts
		v := mount(n, mode, &c)
		v.Render(120) // prime, so caches are warm and only steady state counts
		c.wrap, c.mdRender = 0, 0
		v.Render(120)
		return c.wrap + c.mdRender
	}
	// The viewer rebuilds every line of the transcript on every frame, so the work is expected to grow with it. What
	// must not happen is growing FASTER than it: ten times the messages, at most ~ten times the work.
	tw(t, file, "does ~10x the work for 10x the messages (raw wrap path)", func(t *testing.T) {
		small, large := wrapsFor(30, viewerMarkdownOff), wrapsFor(300, viewerMarkdownOff)
		if small <= 0 || float64(large)/float64(small) > 11 {
			t.Errorf("small %d, large %d", small, large)
		}
	})
	tw(t, file, "does ~10x the work for 10x the messages (markdown path)", func(t *testing.T) {
		small, large := wrapsFor(30, viewerMarkdownAssistant), wrapsFor(300, viewerMarkdownAssistant)
		if small <= 0 || float64(large)/float64(small) > 11 {
			t.Errorf("small %d, large %d", small, large)
		}
	})
	// The per-message cache is keyed by the message object: if a refactor rebuilds messages, or keys the cache on
	// something that changes per frame, every frame re-parses the whole transcript as Markdown.
	tw(t, file, "re-renders without re-parsing: the markdown cache survives a frame", func(t *testing.T) {
		var c perfCounts
		v := mount(60, viewerMarkdownAssistant, &c)
		v.Render(120)
		afterFirst := c.mdNew
		if afterFirst <= 0 {
			t.Fatal("nothing parsed")
		}
		v.Render(120)
		v.Render(120)
		if c.mdNew != afterFirst {
			t.Errorf("constructions %d -> %d", afterFirst, c.mdNew)
		}
	})
	// listCallsPerRender renders one frame over n agents and counts how often the manager was asked.
	tw(t, file, "asks the manager for the agent list a constant number of times", func(t *testing.T) {
		callsFor := func(n int) int {
			records := makeFleet(n, 0, 0)
			list := &fixedList{agents: records}
			w := newAgentWidget(list, makeActivityFor(records), func() widgetMode { return widgetModeAll }, func() bool { return false }, func() bool { return false })
			ui := &fakeWidgetUI{}
			w.setUI(ui)
			w.update()
			ui.frame(120, plainTheme()) // prime
			list.mu.Lock()
			list.calls = 0
			list.mu.Unlock()
			ui.frame(120, plainTheme())
			w.dispose()
			return list.calls
		}
		// Today a render is exactly one scan (update() does the other). The bound is "a constant, and the same constant
		// at 100 agents as at 1".
		if callsFor(1) > 2 || callsFor(100) > 2 || callsFor(100) != callsFor(1) {
			t.Errorf("1 agent: %d, 100 agents: %d", callsFor(1), callsFor(100))
		}
	})
}

// perfCounts counts the leaves a render reaches.
type perfCounts struct{ wrap, mdNew, mdRender int }

type countedMarkdown struct {
	inner markdownComponent
	c     *perfCounts
}

func (m *countedMarkdown) SetText(t string) { m.inner.SetText(t) }
func (m *countedMarkdown) Render(w int) []string {
	m.c.mdRender++
	return m.inner.Render(w)
}

// perfNow is frozen so every elapsed string keeps a stable width.
var perfNow = nowMs()

// makeFleet is a fleet in the mix the widget renders: running, queued, finished (the perf fixtures).
func makeFleet(running, queued, finished int) []*agentView {
	var out []*agentView
	i := 0
	mk := func(status string) *agentView {
		typ := "general-purpose"
		if i%3 == 0 {
			typ = "Explore"
		}
		r := &agentView{ID: fmt.Sprintf("perf-agent-%d", i), Type: typ, Description: fmt.Sprintf("agent %d inspecting a subsystem for the benchmark fixture", i),
			Status: status, ToolUses: i % 7, StartedAt: perfNow - 1000 - int64(i%5)*100, HasSession: true, IsBackground: boolp(true),
			Usage: lifetimeUsage{Input: 1000 + i*37, Output: 200 + i*11}, CompactionCount: i % 3,
			Invocation: &invocation{ModelName: "sonnet 4.6", ModelID: "anthropic/claude-sonnet-4-6", Thinking: "high", RunInBackground: true}}
		if status == "completed" {
			r.CompletedAt = perfNow
		}
		i++
		return r
	}
	for n := 0; n < running; n++ {
		out = append(out, mk("running"))
	}
	for n := 0; n < queued; n++ {
		out = append(out, mk("queued"))
	}
	for n := 0; n < finished; n++ {
		out = append(out, mk("completed"))
	}
	return out
}

func makeActivityFor(records []*agentView) activityMap {
	m := activityMap{}
	for _, r := range records {
		m[r.ID] = &agentActivity{ToolUses: r.ToolUses, ResponseText: "working on the fixture", TurnCount: 1}
	}
	return m
}

func TestNoFSOnRenderPerfTwins(t *testing.T) {
	const file = "perf/no-fs-on-render.perf"
	// The original mocks node:fs and asserts a rendered frame calls none of its entry points. Go cannot interpose on
	// os.ReadFile, so the guard is static: the files a frame runs through must not import anything that reaches the disk.
	noDisk := func(t *testing.T, files ...string) {
		t.Helper()
		fset := token.NewFileSet()
		for _, f := range files {
			parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				switch path := strings.Trim(imp.Path.Value, `"`); path {
				case "os", "io/ioutil", "io/fs", "path/filepath", "os/exec", "net", "net/http":
					t.Errorf("%s imports %s: a frame must not reach the disk", f, path)
				}
			}
		}
	}
	tw(t, file, "AgentWidget.render", func(t *testing.T) {
		w := newAgentWidget(&fixedList{agents: makeFleet(5, 3, 2)}, nil, func() widgetMode { return widgetModeAll }, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.update()
		ui.frame(120, plainTheme()) // construction and priming may legitimately read; the frame may not
		ui.frame(120, plainTheme())
		w.dispose()
		noDisk(t, "agent_widget.go", "agent_color.go", "ui_model.go", "ui_usage.go")
	})
	tw(t, file, "ConversationViewer.render", func(t *testing.T) {
		var messages []obj
		for i := 0; i < 40; i++ {
			messages = append(messages, userMsg("hello"), assistantMsg("# hi"))
		}
		v, _ := newViewer(40, mockSession(messages...), mockRecordView(nil), nil, plainTheme(), viewerOptions{})
		v.Render(120)
		v.Render(120)
		noDisk(t, "conversation_viewer.go", "viewer_keys.go")
	})
}
