package tintinweb_subagents

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Tests of the surfaces' behavior beyond the original's cases: what the Go port adds (the child's event stream feeding
// the surfaces, the model resolution) and the rows and states the original's tests leave to its screenshots.

func TestAgentWidgetRowsAndConnectors(t *testing.T) {
	th := tagTheme()
	mk := func(id, status string) *agentView {
		return &agentView{ID: id, Type: "general-purpose", Description: id + " d", Status: status, StartedAt: nowMs(), IsBackground: boolp(true)}
	}
	frame := func(agents []*agentView, activity activityMap, mutate func(*agentWidget)) []string {
		w := newAgentWidget(&fixedList{agents: agents}, activity, func() widgetMode { return widgetModeAll }, nil, nil)
		if mutate != nil {
			mutate(w)
		}
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.update()
		return ui.render(2000, th)
	}
	t.Run("the last connector closes the tree", func(t *testing.T) {
		lines := frame([]*agentView{mk("a", "running"), mk("b", "running")}, nil, nil)
		// heading, then per agent a header and an activity line; the last pair closes with └─ and a blank gutter.
		hasStr(t, lines[1], "├─")
		hasStr(t, lines[3], "└─")
		hasStr(t, lines[4], "   ")
		lacksStr(t, lines[4], "│")
		hasStr(t, lines[2], "│")
	})
	t.Run("a queued summary is the last row and closes the tree", func(t *testing.T) {
		lines := frame([]*agentView{mk("a", "running"), mk("q", "queued")}, nil, nil)
		hasStr(t, lines[len(lines)-1], "└─")
		hasStr(t, lines[len(lines)-1], "1 queued")
		hasStr(t, lines[2], "│")
	})
	t.Run("a running agent shows its turns, tools and activity", func(t *testing.T) {
		act := activityMap{"a": {ActiveTools: []activeTool{{"k1", "read"}, {"k2", "read"}, {"k3", "grep"}}, ToolUses: 3, TurnCount: 2, MaxTurns: intp(30)}}
		lines := strings.Join(frame([]*agentView{mk("a", "running")}, act, nil), "\n")
		hasStr(t, lines, "↻2≤30")
		hasStr(t, lines, "3 tool uses")
		hasStr(t, lines, "reading 2 files, searching…")
		hasStr(t, lines, "<accent>●</accent> <accent>Agents</accent>")
	})
	t.Run("a one-tool agent says tool use", func(t *testing.T) {
		a := mk("a", "running")
		a.ToolUses = 1
		hasStr(t, strings.Join(frame([]*agentView{a}, nil, nil), "\n"), "1 tool use ")
	})
	t.Run("finished agents read by outcome", func(t *testing.T) {
		fin := func(id, status, errText string) *agentView {
			a := mk(id, status)
			a.CompletedAt, a.Error, a.ToolUses = nowMs(), errText, 2
			return a
		}
		agents := []*agentView{fin("ok", "completed", ""), fin("st", "steered", ""), fin("sp", "stopped", ""), fin("er", "error", strings.Repeat("x", 80)), fin("ab", "aborted", "")}
		lines := frame(agents, nil, func(w *agentWidget) {
			for _, a := range agents {
				w.markFinished(a.ID)
			}
		})
		out := strings.Join(lines, "\n")
		hasStr(t, out, "<success>✓</success>")
		hasStr(t, out, "<warning>✓</warning>")
		hasStr(t, out, "<warning> (turn limit)</warning>")
		hasStr(t, out, "<dim>■</dim>")
		hasStr(t, out, "<dim> stopped</dim>")
		hasStr(t, out, "<error>✗</error>")
		hasStr(t, out, "<error> error: "+strings.Repeat("x", 60)+"</error>")
		hasStr(t, out, "<warning> aborted</warning>")
		hasStr(t, out, "2 tool uses")
		eqStr(t, strings.Split(out, "\n")[0], "<dim>○</dim> <dim>Agents</dim>")
	})
	t.Run("a twin agent carries its mode label", func(t *testing.T) {
		hasStr(t, strings.Join(frame([]*agentView{mk("a", "running")}, nil, nil), "\n"), "<dim>(twin)</dim>")
	})
	t.Run("the status bar counts what runs and what waits", func(t *testing.T) {
		w := newAgentWidget(&fixedList{agents: []*agentView{mk("a", "running"), mk("b", "running"), mk("q", "queued")}}, nil, nil, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.update()
		w.update() // unchanged: the status is not set again
		eq(t, ui.statuses, []string{"2 running, 1 queued agents"})
		w2 := newAgentWidget(&fixedList{agents: []*agentView{mk("a", "running")}}, nil, nil, nil, nil)
		ui2 := &fakeWidgetUI{}
		w2.setUI(ui2)
		w2.update()
		eq(t, ui2.statuses, []string{"1 running agent"})
	})
	t.Run("the widget is registered once and refreshed after", func(t *testing.T) {
		w := newAgentWidget(&fixedList{agents: []*agentView{mk("a", "running")}}, nil, nil, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.update()
		w.update()
		w.update()
		if ui.rerender != 2 {
			t.Errorf("re-renders: %d", ui.rerender)
		}
	})
	t.Run("an emptied widget clears itself and its status", func(t *testing.T) {
		list := &fixedList{agents: []*agentView{mk("a", "running")}}
		w := newAgentWidget(list, nil, nil, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.update()
		list.agents = nil
		w.update()
		if ui.cleared != 1 || !reflect.DeepEqual(ui.statuses, []string{"1 running agent", "<nil>"}) {
			t.Errorf("cleared %d, statuses %v", ui.cleared, ui.statuses)
		}
		w.update() // already clear: nothing more
		if ui.cleared != 1 {
			t.Errorf("cleared %d", ui.cleared)
		}
	})
	t.Run("errors linger for two turns, completions for one", func(t *testing.T) {
		okAgent, errAgent := mk("ok", "completed"), mk("er", "error")
		okAgent.CompletedAt, errAgent.CompletedAt = nowMs(), nowMs()
		w := newAgentWidget(&fixedList{agents: []*agentView{okAgent, errAgent}}, nil, nil, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.markFinished("ok")
		w.markFinished("er")
		w.update()
		hasStr(t, ui.frame(200, plainTheme()), "ok d")
		w.onTurnStart()
		out := ui.frame(200, plainTheme())
		lacksStr(t, out, "ok d")
		hasStr(t, out, "er d")
		w.onTurnStart()
		eqStr(t, ui.frame(200, plainTheme()), "")
	})
	t.Run("the timer ticks until the widget empties", func(t *testing.T) {
		clock, restore := installManualClock()
		defer restore()
		list := &fixedList{agents: []*agentView{mk("a", "running")}}
		w := newAgentWidget(list, nil, nil, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		w.ensureTimer()
		w.ensureTimer() // one timer
		if clock.running() != 1 {
			t.Fatalf("%d timers", clock.running())
		}
		w.update()
		clock.fire()
		if ui.rerender < 1 {
			t.Errorf("re-renders: %d", ui.rerender)
		}
		list.agents = nil
		clock.fire()
		if clock.running() != 0 {
			t.Error("the timer outlived the widget")
		}
	})
}

func TestAgentResultRendering(t *testing.T) {
	th := tagTheme()
	render := func(text string, d agentDetails, expanded, partial, isError, cost bool) string {
		return strings.Join(renderAgentResult(text, &d, expanded, partial, isError, th, cost, 2000), "\n")
	}
	base := agentDetails{Status: "completed", ModelName: "haiku", Tags: []string{"thinking: high"}, TurnCount: intp(5), MaxTurns: intp(30), ToolUses: 3, Tokens: "33.8k token", DurationMs: 2500, Cost: floatp(0.0042)}
	t.Run("completed, collapsed", func(t *testing.T) {
		out := render("body", base, false, false, false, true)
		hasStr(t, out, "<success>✓</success> <dim>haiku</dim> <dim>·</dim> <dim>thinking: high</dim> <dim>·</dim> <dim>↻5≤30</dim> <dim>·</dim> <dim>3 tool uses</dim> <dim>·</dim> <dim>33.8k token</dim> <dim>·</dim> <dim>~$0.0042</dim> <dim>·</dim> <dim>2.5s</dim>")
		hasStr(t, out, "<dim>  ⎿  Done</dim>")
		lacksStr(t, out, "body")
		lacksStr(t, render("body", base, false, false, false, false), "$")
	})
	t.Run("completed, expanded shows the text, 50 lines at most", func(t *testing.T) {
		var lines []string
		for i := 0; i < 60; i++ {
			lines = append(lines, fmt.Sprintf("row %d", i))
		}
		out := render(strings.Join(lines, "\n"), base, true, false, false, false)
		hasStr(t, out, "<dim>  row 49</dim>")
		lacksStr(t, out, "row 50")
		hasStr(t, out, "<muted>  ... (use get_subagent_result with verbose for full output)</muted>")
		lacksStr(t, out, "Done")
		short := render("a\nb", base, true, false, false, false)
		lacksStr(t, short, "use get_subagent_result")
	})
	t.Run("steered", func(t *testing.T) {
		d := base
		d.Status = "steered"
		out := render("", d, false, false, false, false)
		hasStr(t, out, "<warning>✓</warning>")
		hasStr(t, out, "Wrapped up (turn limit)")
	})
	t.Run("stopped", func(t *testing.T) {
		d := base
		d.Status = "stopped"
		out := render("", d, false, false, false, false)
		hasStr(t, out, "<dim>■</dim>")
		hasStr(t, out, "<dim>  ⎿  Stopped</dim>")
	})
	t.Run("error and aborted", func(t *testing.T) {
		d := base
		d.Status, d.Error = "error", "boom"
		hasStr(t, render("", d, false, false, false, false), "<error>  ⎿  Error: boom</error>")
		d.Error = ""
		hasStr(t, render("", d, false, false, false, false), "Error: unknown")
		d.Status = "aborted"
		out := render("", d, false, false, false, false)
		hasStr(t, out, "<error>✗</error>")
		hasStr(t, out, "<warning>  ⎿  Aborted (max turns exceeded)</warning>")
	})
	t.Run("running shows the spinner frame and the activity", func(t *testing.T) {
		d := base
		d.Status, d.SpinnerFrame, d.Activity = "running", intp(3), "reading…"
		out := render("", d, false, false, false, false)
		hasStr(t, out, "<accent>"+spinner[3]+"</accent>")
		hasStr(t, out, "<dim>  ⎿  reading…</dim>")
		d.Activity, d.SpinnerFrame = "", nil
		out = render("", d, false, true, false, false)
		hasStr(t, out, "<accent>"+spinner[0]+"</accent>")
		hasStr(t, out, "thinking…")
	})
	t.Run("a stat-less result has no leading blank", func(t *testing.T) {
		out := render("", agentDetails{Status: "stopped"}, false, false, false, false)
		eqStr(t, strings.TrimRight(strings.Split(out, "\n")[0], " "), "<dim>■</dim>")
	})
}

func floatp(f float64) *float64 { return &f }

func TestNotificationRendering(t *testing.T) {
	th := tagTheme()
	render := func(d notificationDetails, expanded bool) string {
		return strings.Join(renderNotification(d, expanded, th, false, 2000), "\n")
	}
	base := notificationDetails{ID: "a", Description: "did it", Status: "completed", ResultPreview: "first\nsecond"}
	t.Run("statuses", func(t *testing.T) {
		hasStr(t, render(base, false), "<success>✓</success> *did it* <dim>completed</dim>")
		for _, s := range []string{"error", "stopped", "aborted"} {
			d := base
			d.Status = s
			hasStr(t, render(d, false), "<error>✗</error> *did it* <dim>"+s+"</dim>")
		}
		d := base
		d.Status = "steered"
		hasStr(t, render(d, false), "<success>✓</success> *did it* <dim>completed (steered)</dim>")
	})
	t.Run("stats, preview and transcript", func(t *testing.T) {
		d := base
		d.TurnCount, d.MaxTurns, d.ToolUses, d.TotalTokens, d.DurationMs, d.OutputFile = 4, intp(10), 1, 2500, 3200, "/tmp/x.output"
		out := render(d, false)
		hasStr(t, out, "  <dim>↻4≤10</dim> <dim>·</dim> <dim>1 tool use</dim> <dim>·</dim> <dim>2.5k token</dim> <dim>·</dim> <dim>3.2s</dim>")
		hasStr(t, out, "  <dim>⎿  first</dim>")
		lacksStr(t, out, "second")
		hasStr(t, out, "  <muted>transcript: /tmp/x.output</muted>")
		lacksStr(t, render(base, false), "transcript")
		lacksStr(t, render(base, false), "·")
	})
	t.Run("the collapsed preview is 80 units, the expanded one 30 lines", func(t *testing.T) {
		d := base
		d.ResultPreview = strings.Repeat("a", 100)
		hasStr(t, render(d, false), "⎿  "+strings.Repeat("a", 80)+"</dim>")
		lacksStr(t, render(d, false), strings.Repeat("a", 81))
		var lines []string
		for i := 0; i < 40; i++ {
			lines = append(lines, fmt.Sprintf("l%d", i))
		}
		d.ResultPreview = strings.Join(lines, "\n")
		out := render(d, true)
		hasStr(t, out, "<dim>  l29</dim>")
		lacksStr(t, out, "l30")
	})
	t.Run("cost appears only when shown", func(t *testing.T) {
		d := base
		d.TotalCost = 0.0042
		hasStr(t, strings.Join(renderNotification(d, false, th, true, 2000), "\n"), "<dim>~$0.0042</dim>")
	})
}

func TestResolveInvocationPrecedence(t *testing.T) {
	cfg := &agentConfig{Model: "anthropic/claude-haiku-4-5", Thinking: "low", MaxTurns: intp(7)}
	got := resolveInvocation(cfg, "opus", "max", 99)
	if got.modelInput != cfg.Model || got.thinking != "low" || *got.maxTurns != 7 || got.overriddenModel != "opus" || got.overriddenThinking != "max" || got.modelFromParams {
		t.Errorf("%+v", got)
	}
	got = resolveInvocation(&agentConfig{}, "opus", "max", 99)
	if got.modelInput != "opus" || got.thinking != "max" || *got.maxTurns != 99 || got.overriddenModel != "" || got.overriddenThinking != "" || !got.modelFromParams {
		t.Errorf("%+v", got)
	}
	got = resolveInvocation(nil, "", "", 0)
	if got.modelInput != "" || got.thinking != "" || got.maxTurns != nil || got.modelFromParams {
		t.Errorf("%+v", got)
	}
	// the same spelling is not an override
	got = resolveInvocation(cfg, cfg.Model, "low", 0)
	if got.overriddenModel != "" || got.overriddenThinking != "" {
		t.Errorf("%+v", got)
	}
}

func TestResolveModelAmong(t *testing.T) {
	models := []map[string]any{
		{"provider": "anthropic", "id": "claude-haiku-4-5", "name": "Claude Haiku 4.5"},
		{"provider": "anthropic", "id": "claude-sonnet-4-6", "name": "Claude Sonnet 4.6"},
		{"provider": "google", "id": "gemini-3-pro", "name": "Gemini 3 Pro"},
		{"provider": "openai", "id": "sonnet-4-6-extra-long-id", "name": "Other"},
	}
	idOf := func(input string) string {
		m, msg := resolveModelAmong(input, models)
		if msg != "" {
			return "ERR:" + msg
		}
		return m["provider"].(string) + "/" + m["id"].(string)
	}
	for in, want := range map[string]string{
		"anthropic/claude-haiku-4-5": "anthropic/claude-haiku-4-5",
		"ANTHROPIC/Claude-Haiku-4-5": "anthropic/claude-haiku-4-5",
		"haiku":                      "anthropic/claude-haiku-4-5",
		"claude-haiku-4.5":           "anthropic/claude-haiku-4-5",  // dot vs dash
		"sonnet-4-6":                 "anthropic/claude-sonnet-4-6", // the tighter substring wins
		"gemini pro":                 "google/gemini-3-pro",         // every part present
		"Gemini 3 Pro":               "google/gemini-3-pro",         // by name
		"claude-haiku-4-5-20251001":  "anthropic/claude-haiku-4-5",  // an optional date stamp
		"wrongprovider/gemini-3-pro": "google/gemini-3-pro",         // the provider fallback
		"anthropic/gemini":           "google/gemini-3-pro",
	} {
		if got := idOf(in); got != want {
			t.Errorf("%q -> %s, want %s", in, got, want)
		}
	}
	got := idOf("gpt-9")
	hasStr(t, got, `ERR:Model not found: "gpt-9".`)
	hasStr(t, got, "Available models:\n  anthropic/claude-haiku-4-5\n  anthropic/claude-sonnet-4-6\n  google/gemini-3-pro\n  openai/sonnet-4-6-extra-long-id")
}

func TestObserverBuildsTheSurfacesState(t *testing.T) {
	u := newUIAgent(&invocation{ModelName: "x", Thinking: "high"}, intp(5))
	var notified int
	u.onChange = func() { notified++ }
	var viewerCalls int
	unsub := u.Subscribe(func() { viewerCalls++ })

	u.observe(obj{"type": "message_start", "message": obj{"role": "user", "content": "do it"}}, nil)
	u.observe(obj{"type": "message_end", "message": obj{"role": "user", "content": "do it"}}, nil)
	u.observe(obj{"type": "message_start", "message": obj{"role": "assistant", "content": []any{}}}, nil)
	u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": float64(0), "delta": "hel"}}, nil)
	msgs := u.Messages()
	if len(msgs) != 2 {
		t.Fatalf("%d messages", len(msgs))
	}
	streaming := msgs[1]
	u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": float64(0), "delta": "lo"}}, nil)
	u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "toolcall_start", "contentIndex": float64(1), "toolName": "read"}}, nil)
	eqStr(t, textOf(streaming["content"]), "hel") // a copy already handed out is never changed under its reader
	updated := u.Messages()[1]
	eqStr(t, textOf(updated["content"]), "hello")
	if updated[viewerIDKey] != streaming[viewerIDKey] {
		t.Errorf("the message changed identity while it streamed: %v then %v", streaming[viewerIDKey], updated[viewerIDKey])
	}
	if got := u.snapshotActivity().ResponseText; got != "hello" {
		t.Errorf("response text %q", got)
	}
	final := obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "hello!"}}, "usage": obj{"input": float64(1000), "output": float64(200), "cacheWrite": float64(50), "cacheRead": float64(9000), "cost": obj{"total": 0.0042}}}
	u.observe(obj{"type": "message_end", "message": final}, nil)
	if len(u.Messages()) != 2 || textOf(u.Messages()[1]["content"]) != "hello!" {
		t.Errorf("%v", u.Messages())
	}
	if u.usage != (lifetimeUsage{Input: 1000, Output: 200, CacheWrite: 50, CacheRead: 9000, Cost: 0.0042}) {
		t.Errorf("%+v", u.usage)
	}
	u.observe(obj{"type": "message_start", "message": obj{"role": "toolResult", "content": []any{obj{"type": "text", "text": "out"}}}}, nil)
	u.observe(obj{"type": "message_end", "message": obj{"role": "toolResult", "content": []any{obj{"type": "text", "text": "out"}}}}, nil)
	if len(u.Messages()) != 3 {
		t.Errorf("%d messages", len(u.Messages()))
	}

	u.observe(obj{"type": "tool_execution_start", "toolName": "read", "toolCallId": "c1"}, nil)
	u.observe(obj{"type": "tool_execution_start", "toolName": "grep", "toolCallId": "c2"}, nil)
	act := u.snapshotActivity()
	if len(act.ActiveTools) != 2 || describeActivity(act.ActiveTools, "") != "reading, searching…" {
		t.Errorf("%+v", act.ActiveTools)
	}
	u.observe(obj{"type": "tool_execution_end", "toolName": "read", "toolCallId": "c1"}, nil)
	act = u.snapshotActivity()
	if len(act.ActiveTools) != 1 || act.ToolUses != 1 || act.TurnCount != 1 {
		t.Errorf("%+v", act)
	}
	u.observe(obj{"type": "turn_end"}, nil)
	u.observe(obj{"type": "turn_end"}, nil)
	if tc := u.snapshotActivity().TurnCount; tc != 2 || *u.snapshotActivity().MaxTurns != 5 {
		t.Errorf("turn count %d", tc)
	}
	u.observe(obj{"type": "compaction_end", "aborted": false, "result": obj{"summary": "s"}}, nil)
	u.observe(obj{"type": "compaction_end", "aborted": true, "result": obj{"summary": "s"}}, nil)
	u.observe(obj{"type": "compaction_end", "aborted": false}, nil)
	if u.compactions != 1 {
		t.Errorf("compactions %d", u.compactions)
	}
	if !u.hasSession {
		t.Error("no session after events")
	}
	if notified == 0 || viewerCalls == 0 {
		t.Error("nobody was told")
	}
	n := viewerCalls
	unsub()
	u.observe(obj{"type": "turn_end"}, nil)
	if viewerCalls != n {
		t.Error("an unsubscribed viewer was still told")
	}
}

func TestSessionReportsTheEffectiveModel(t *testing.T) {
	u := newUIAgent(&invocation{ModelName: "pre", ModelID: "p/pre", Thinking: "max"}, nil)
	u.refreshModel(sessionReports("anthropic", "claude-haiku-4-5", "Claude Haiku 4.5", "high"))
	if u.inv.ModelName != "haiku 4.5" || u.inv.ModelID != "anthropic/claude-haiku-4-5" || u.inv.Thinking != "high" || u.inv.RequestedThinking != "max" {
		t.Errorf("%+v", u.inv)
	}
	// What was requested is kept once set, and replaced by the effective level one report later.
	u.refreshModel(sessionReports("anthropic", "claude-haiku-4-5", "Claude Haiku 4.5", "low"))
	if u.inv.Thinking != "low" || u.inv.RequestedThinking != "max" {
		t.Errorf("%+v", u.inv)
	}
	// A session that reports no level keeps the request.
	v := newUIAgent(&invocation{Thinking: "medium"}, nil)
	v.refreshModel(sessionReports("a", "b", "", ""))
	if v.inv.Thinking != "medium" || v.inv.RequestedThinking != "" || v.inv.ModelName != "b" {
		t.Errorf("%+v", v.inv)
	}
	// An honored request is not a disclosure.
	w := newUIAgent(&invocation{Thinking: "high"}, nil)
	w.refreshModel(sessionReports("a", "b", "B", "high"))
	if w.inv.RequestedThinking != "" {
		t.Errorf("%+v", w.inv)
	}
	// A failing query changes nothing.
	x := newUIAgent(&invocation{ModelName: "keep"}, nil)
	x.refreshModel(func(string) (obj, error) { return nil, fmt.Errorf("down") })
	eqStr(t, x.inv.ModelName, "keep")
}

func TestContextPercentFromStats(t *testing.T) {
	u := newUIAgent(nil, nil)
	u.refreshStats(func(cmd string) (obj, error) {
		eqStr(t, cmd, "get_session_stats")
		return obj{"data": obj{"contextUsage": obj{"percent": float64(42)}}}, nil
	})
	if p := getSessionContextPercent(u); p == nil || *p != 42 {
		t.Errorf("%v", p)
	}
	u.refreshStats(func(string) (obj, error) { return obj{"data": obj{"contextUsage": obj{"percent": nil}}}, nil })
	if getSessionContextPercent(u) != nil {
		t.Error("a null percent must read as unknown")
	}
}

func TestViewerFooterAndKeys(t *testing.T) {
	plain := plainTheme()
	t.Run("the line count shows when there is room, and is the first to go", func(t *testing.T) {
		v, _ := newViewer(30, mockSession(userMsg("hi")), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, plain, viewerOptions{})
		wide := strings.Join(v.Render(120), "\n")
		hasStr(t, wide, "lines · 100%")
		v2, _ := newViewer(30, mockSession(userMsg("hi")), mockRecordView(nil), nil, plain, viewerOptions{OnStop: func() {}, OnSteer: func(string) {}})
		lacksStr(t, strings.Join(v2.Render(80), "\n"), "lines ·")
	})
	t.Run("the scroll percentage follows the window", func(t *testing.T) {
		var messages []obj
		for i := 0; i < 80; i++ {
			messages = append(messages, userMsg(fmt.Sprintf("message %d", i)))
		}
		v, _ := newViewer(20, mockSession(messages...), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, plain, viewerOptions{})
		hasStr(t, strings.Join(v.Render(120), "\n"), "100%")
		v.HandleInput("\x1b[5~") // page up
		out := strings.Join(v.Render(120), "\n")
		lacksStr(t, out, " · 100%")
		hasStr(t, out, "%")
		v.HandleInput("home")
		v.HandleInput("\x1b[H")
		first := strings.Join(v.Render(120), "\n")
		hasStr(t, first, "message 0")
		v.HandleInput("\x1b[F") // end
		hasStr(t, strings.Join(v.Render(120), "\n"), "message 79")
	})
	t.Run("Esc, q and ctrl+c close it", func(t *testing.T) {
		for _, key := range []string{"\x1b", "q", "\x03"} {
			var closed int
			v := newConversationViewer(&fakeHost{rows: 30}, emptySession{}, mockRecordView(nil), nil, plain, func() { closed++ }, viewerOptions{})
			v.HandleInput(key)
			if closed != 1 {
				t.Errorf("%q: closed %d times", key, closed)
			}
		}
	})
	t.Run("a queued agent can still be stopped and steered", func(t *testing.T) {
		v, _ := newViewer(30, emptySession{}, mockRecordView(func(r *agentView) { r.Status = "queued" }), nil, plain, viewerOptions{OnStop: func() {}, OnSteer: func(string) {}})
		out := strings.Join(v.Render(120), "\n")
		hasStr(t, out, "x stop")
		hasStr(t, out, "Enter steer")
	})
	t.Run("an empty transcript says it is waiting", func(t *testing.T) {
		v, _ := newViewer(30, emptySession{}, mockRecordView(nil), nil, plain, viewerOptions{})
		hasStr(t, strings.Join(v.Render(80), "\n"), "(waiting for first message...)")
	})
	t.Run("the transcript shows each role", func(t *testing.T) {
		msgs := []obj{userMsg("question"), {"role": "assistant", "content": []any{obj{"type": "toolCall", "name": "read"}}}, resultMsg("file text"), {"role": "bashExecution", "command": "ls -l", "output": "total 0"}, {"role": "custom", "content": "ignored"}}
		v, _ := newViewer(60, mockSession(msgs...), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, tagTheme(), viewerOptions{})
		out := strings.Join(v.buildContentLines(80), "\n")
		for _, want := range []string{"<accent>[User]</accent>", "question", "*[Assistant]*", "<muted>  [Tool: read]</muted>", "<dim>[Result]</dim>", "file text", "<muted>  $ ls -l</muted>", "total 0", "<dim>───</dim>"} {
			hasStr(t, out, want)
		}
		lacksStr(t, out, "ignored")
	})
	t.Run("a running agent shows its activity at the end", func(t *testing.T) {
		act := &agentActivity{ActiveTools: []activeTool{{"k", "bash"}}}
		v, _ := newViewer(60, mockSession(userMsg("q")), mockRecordView(nil), func() *agentActivity { return act }, tagTheme(), viewerOptions{})
		lines := v.buildContentLines(80)
		eqStr(t, lines[len(lines)-1], "<accent>▍ </accent><dim>running command…</dim>")
	})
	t.Run("the markdown theme keeps emphasis", func(t *testing.T) {
		v, _ := newViewer(100, mockSession(assistantMsg("an *emphasised* and `code` word")), mockRecordView(nil), nil, plainTheme(), viewerOptions{})
		out := strings.Join(v.buildContentLines(60), "\n")
		hasStr(t, out, "\x1b[3memphasised\x1b[23m")
	})
	t.Run("the header shows the status icon", func(t *testing.T) {
		for status, want := range map[string]string{"running": "<accent>●</accent>", "completed": "<success>✓</success>", "error": "<error>✗</error>", "stopped": "<dim>○</dim>"} {
			v, _ := newViewer(30, emptySession{}, mockRecordView(func(r *agentView) { r.Status = status }), nil, tagTheme(), viewerOptions{})
			hasStr(t, strings.Join(v.Render(100), "\n"), want)
		}
	})
	t.Run("the header shows tools, context percent and compactions", func(t *testing.T) {
		pct := 72.0
		act := &agentActivity{ToolUses: 4, Session: &statsSession{stats: sessionStats{ContextPercent: &pct}}}
		rec := mockRecordView(func(r *agentView) { r.Usage = lifetimeUsage{Input: 5000}; r.CompactionCount = 2 })
		v, _ := newViewer(30, emptySession{}, rec, func() *agentActivity { return act }, tagTheme(), viewerOptions{})
		out := strings.Join(v.Render(400), "\n")
		hasStr(t, out, "4 tools")
		hasStr(t, out, "5.0k token (<warning>72%</warning> · <dim>⇊2</dim>)")
	})
	t.Run("a narrow terminal draws nothing", func(t *testing.T) {
		v, _ := newViewer(30, emptySession{}, mockRecordView(nil), nil, plain, viewerOptions{})
		if got := v.Render(5); len(got) != 0 {
			t.Errorf("%q", got)
		}
	})
}

func TestToolsDescribeOneUse(t *testing.T) {
	// the singular of the stats in a result
	hasStr(t, agentStats(agentDetails{ToolUses: 1}, plainTheme(), false), "1 tool use")
	lacksStr(t, agentStats(agentDetails{ToolUses: 1}, plainTheme(), false), "1 tool uses")
	eqStr(t, agentStats(agentDetails{}, plainTheme(), false), "")
}

func TestHubViews(t *testing.T) {
	r := startRig(t)
	id1 := spawnBackground(r, "first")
	time.Sleep(3 * time.Millisecond)
	id2 := spawnBackground(r, "second")
	views := r.app.ui.listAgents()
	if len(views) != 2 || views[0].ID != id2 || views[1].ID != id1 { // newest first, as the manager lists them
		t.Fatalf("%v", views)
	}
	if views[0].IsBackground == nil || !*views[0].IsBackground || views[0].HasSession {
		t.Errorf("%+v", views[0])
	}
	// Events from the child give it a session, usage and a live tool count.
	ua := r.uiAgentOf(id1)
	ua.observe(obj{"type": "message_end", "message": obj{"role": "assistant", "content": []any{}, "usage": obj{"input": float64(10), "output": float64(5)}}}, nil)
	ua.observe(obj{"type": "tool_execution_end", "toolName": "read"}, nil)
	for _, v := range r.app.ui.listAgents() {
		if v.ID == id1 && (!v.HasSession || v.Usage.Input != 10 || v.ToolUses != 1) {
			t.Errorf("%+v", v)
		}
	}
	// The viewer reads the transcript by id; an unknown id is an empty session.
	if _, ok := r.app.ui.sessionOf(id1).(*uiAgent); !ok {
		t.Error("no session for a known agent")
	}
	if _, ok := r.app.ui.sessionOf("nope").(emptySession); !ok {
		t.Error("a session for an unknown agent")
	}
	if r.app.ui.activityOf("nope") != nil {
		t.Error("activity for an unknown agent")
	}
	// Stop is the manager's.
	if !r.app.ui.abort(id2) || r.app.ui.abort("nope") {
		t.Error("abort")
	}
}

func TestStatusIsNotRepeated(t *testing.T) {
	r := startRig(t)
	r.Fire("tool_execution_start", obj{})
	spawnBackground(r, "one")
	r.waitFor("the status", func() bool { return len(r.statusCalls()) > 0 })
	for i := 0; i < 5; i++ {
		r.app.ui.widget.update()
	}
	if got := r.statusCalls(); len(got) != 1 {
		t.Errorf("%v", got)
	}
}

func TestSelectItemKeepsRowsUnique(t *testing.T) {
	ui := &fakeSelectUI{pick: 1}
	items := []string{"same", "same", "other"}
	got, ok := selectItem(ui, "Pick", items, func(s string, _ int) string { return s })
	if !ok || got != "same" || ui.chosen != 1 {
		t.Errorf("%q %v (chose row %d)", got, ok, ui.chosen)
	}
	eq(t, ui.options, []string{"1. same", "2. same", "3. other"})
	var many []int
	for i := 0; i < 10; i++ {
		many = append(many, i)
	}
	ui2 := &fakeSelectUI{pick: 0}
	selectItem(ui2, "t", many, func(n int, _ int) string { return fmt.Sprint(n) })
	eqStr(t, ui2.options[0], " 1. 0") // the number is padded to the width of the largest
	if _, ok := selectItem(&fakeSelectUI{pick: -1}, "t", items, func(s string, _ int) string { return s }); ok {
		t.Error("escape returned an item")
	}
	if _, ok := selectItem(&fakeSelectUI{pick: -2}, "t", items, func(s string, _ int) string { return s }); ok {
		t.Error("an unoffered string returned an item")
	}
}

type fakeSelectUI struct {
	pick    int // -1: escape, -2: a string that was not offered
	options []string
	chosen  int
}

func (f *fakeSelectUI) Select(_ string, options []string) (string, bool, error) {
	f.options = options
	switch {
	case f.pick == -1:
		return "", false, nil
	case f.pick == -2:
		return "not offered", true, nil
	}
	f.chosen = f.pick
	return options[f.pick], true, nil
}
