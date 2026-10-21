package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Tests that pin behaviors the first mutation run showed nothing pinned.

func TestRefusalTextIsExact(t *testing.T) {
	eq(t, originalSpec, "npm:@tintinweb/pi-subagents@0.19.0")
	eq(t, refusalText("x"), "@pi-in-go/pigpen-tintinweb-subagents (Go port) does not support x yet. Install the original extension, npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.")
}

func TestRPCSpawnNamesTheAgent(t *testing.T) {
	r := startRig(t)
	reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "x", "options": obj{"name": "scout"}})
	eq(t, reply["success"], true)
	hasStr(t, r.must("steer_subagent", obj{"agent_id": "scout", "message": "hi"}), "Steering message sent to agent")
}

func TestColorNamesIgnoreCaseAndPadding(t *testing.T) {
	got, ok := resolveAgentColor("  RED ")
	if !ok || got != "#DC2626" {
		t.Errorf("%q %v", got, ok)
	}
}

func TestOnlyNearNeutralColorsTakeTheGrayRamp(t *testing.T) {
	// (100,110,100) is closer to a gray than to its cube color, but it is tinted: it keeps the cube (index 59).
	if idx, _ := rgbTo256(rgb{100, 110, 100}); idx != 59 {
		t.Errorf("index %d", idx)
	}
	if idx, _ := rgbTo256(rgb{100, 100, 100}); idx < 232 {
		t.Errorf("a neutral color takes the gray ramp: %d", idx)
	}
}

func TestLifetimeTotalLeavesOutCacheReads(t *testing.T) {
	eq(t, getLifetimeTotal(lifetimeUsage{Input: 1, Output: 2, CacheWrite: 3, CacheRead: 100}), 6)
}

func TestRefreshModelKeepsTheRequestOnlyWhenTheLevelDiffers(t *testing.T) {
	query := func(level string) func(string) (map[string]any, error) {
		return func(string) (map[string]any, error) {
			return obj{"data": obj{"model": obj{"provider": "p", "id": "m", "name": "M"}, "thinkingLevel": level}}, nil
		}
	}
	u := newUIAgent(&invocation{Thinking: "high"}, nil)
	u.refreshModel(query("high"))
	eqStr(t, u.inv.RequestedThinking, "")
	u = newUIAgent(&invocation{Thinking: "high"}, nil)
	u.refreshModel(query("low"))
	eqStr(t, u.inv.RequestedThinking, "high")
	eqStr(t, u.inv.Thinking, "low")
}

func TestDescribeActivityNounsAndLimit(t *testing.T) {
	two := func(name string) []activeTool { return []activeTool{{name: name}, {name: name}} }
	eqStr(t, describeActivity(two("grep"), ""), "searching 2 patterns…")
	eqStr(t, describeActivity(two("read"), ""), "reading 2 files…")
	eqStr(t, describeActivity(nil, strings.Repeat("x", 100)), strings.Repeat("x", 60)+"…")
}

func TestFinishedRowCostFollowsTheSetting(t *testing.T) {
	finished := &agentView{ID: "a1", Type: "general-purpose", Description: "done agent", Status: "completed", ToolUses: 2,
		StartedAt: nowMs() - 1000, CompletedAt: nowMs(), Usage: lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0042}}
	w := newAgentWidget(&fixedList{agents: []*agentView{finished}}, nil, func() widgetMode { return widgetModeAll }, func() bool { return false }, nil)
	ui := &fakeWidgetUI{}
	w.setUI(ui)
	w.update()
	lacksStr(t, ui.frame(200, plainTheme()), "$")
}

func TestWidgetCutsWithThreeDots(t *testing.T) {
	finished := &agentView{ID: "a1", Type: "general-purpose", Description: "a description that is far too long for the row", Status: "completed", ToolUses: 2,
		StartedAt: nowMs() - 1000, CompletedAt: nowMs()}
	w := newAgentWidget(&fixedList{agents: []*agentView{finished}}, nil, func() widgetMode { return widgetModeAll }, nil, nil)
	ui := &fakeWidgetUI{}
	w.setUI(ui)
	w.update()
	out := ui.frame(24, plainTheme())
	hasStr(t, out, "...")
	lacksStr(t, out, "…")
}

func TestFleetRightAlignCutsTheLeftWithThreeDots(t *testing.T) {
	// pi-tui wraps the ellipsis in resets.
	eqStr(t, rightAlign("a very long left text here", "right", 15), "a very\x1b[0m...\x1b[0m right")
}

func TestFleetElapsedNeverReadsNegative(t *testing.T) {
	eqStr(t, formatFleetElapsed(-5000), "0s")
	eqStr(t, formatFleetElapsed(1500), "2s")
}

func TestDisablingTheFleetViewLeavesTheList(t *testing.T) {
	h := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
	h.press(keyDown) // active
	hasStr(t, strings.Join(h.renderDefault(), "\n"), "↑↓ select")
	h.fleet.setEnabled(false)
	h.fleet.setEnabled(true)
	out := strings.Join(h.renderDefault(), "\n")
	hasStr(t, out, "← for agents")
	lacksStr(t, out, "↑↓ select")
}

// ---- the viewer ----

func manyUserMessages(n int) []map[string]any {
	var msgs []map[string]any
	for i := range n {
		msgs = append(msgs, userMsg("question number "+string(rune('a'+i%26))))
	}
	return msgs
}

func TestViewerScrollingDownStopsAtTheEnd(t *testing.T) {
	s := mockSession(manyUserMessages(30)...)
	v, _ := newViewer(10, s, mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, plainTheme(), viewerOptions{})
	v.Render(80)
	v.HandleInput("\x1b[F") // end
	for range 5 {
		v.HandleInput(keyDown)
	}
	v.HandleInput(keyUp)
	out := strings.Join(v.Render(80), "\n")
	hasStr(t, out, "lines · ") // the readout is shown, and one line up from the end is not 100%
	lacksStr(t, out, "100%")
}

func TestViewerEndKeyFollowsNewMessages(t *testing.T) {
	s := mockSession(manyUserMessages(30)...)
	v, _ := newViewer(10, s, mockRecordView(nil), nil, plainTheme(), viewerOptions{})
	v.Render(80)
	v.HandleInput("\x1b[H") // home
	v.HandleInput("\x1b[F") // end: follow the tail again
	s.messages = append(append([]map[string]any(nil), s.messages...), userMsg("the very last words"))
	hasStr(t, strings.Join(v.Render(80), "\n"), "the very last words")
}

func TestViewerForgetsMarkdownOfMessagesThatAreGone(t *testing.T) {
	s := mockSession(textMsg("assistant", "first **answer**"), textMsg("assistant", "second **answer**"))
	v, _ := newViewer(30, s, mockRecordView(nil), nil, plainTheme(), viewerOptions{})
	v.Render(80)
	v.mu.Lock()
	n := len(v.markdownCache)
	v.mu.Unlock()
	eq(t, n, 2)
	s.messages = s.messages[:1]
	v.Render(80)
	v.mu.Lock()
	n = len(v.markdownCache)
	v.mu.Unlock()
	eq(t, n, 1)
}

func TestViewerTrimsUserText(t *testing.T) {
	v, _ := newViewer(30, mockSession(userMsg("   hello   ")), mockRecordView(nil), nil, plainTheme(), viewerOptions{})
	hasStr(t, strings.Join(v.Render(40), "\n"), "│ hello ")
}

func TestStreamingIndicatorOnlyWhileRunning(t *testing.T) {
	act := func() *agentActivity { return &agentActivity{ResponseText: "working on it"} }
	running, _ := newViewer(30, mockSession(userMsg("hi")), mockRecordView(nil), act, plainTheme(), viewerOptions{})
	hasStr(t, strings.Join(running.Render(60), "\n"), "▍")
	done, _ := newViewer(30, mockSession(userMsg("hi")), mockRecordView(func(r *agentView) { r.Status = "completed" }), act, plainTheme(), viewerOptions{})
	lacksStr(t, strings.Join(done.Render(60), "\n"), "▍")
}

func TestTruncationNotePluralises(t *testing.T) {
	eqStr(t, truncationNote(1), "... (truncated, 1 more character)")
	eqStr(t, truncationNote(2), "... (truncated, 2 more characters)")
}

// ---- model resolution ----

func TestResolveModelAmongPicksTheBestOfManyCandidates(t *testing.T) {
	pick := func(input string, models ...map[string]any) string {
		m, msg := resolveModelAmong(input, models)
		if msg != "" {
			return "ERR"
		}
		return msgStr(m["id"])
	}
	mk := func(id, name string) map[string]any { return map[string]any{"provider": "openai", "id": id, "name": name} }
	// An exact provider/id match beats the fuzzy match, whatever the case.
	eqStr(t, pick("OpenAI/GPT-4-1", mk("gpt-4.1", ""), mk("gpt-4-1", "")), "gpt-4-1")
	// A dot reads as a dash.
	eqStr(t, pick("claude-3.5", mk("claude-3-5-sonnet", ""), mk("claude-3-5", "")), "claude-3-5")
	// The tighter id wins among those that contain the query.
	eqStr(t, pick("claude-3-5", mk("claude-3-5-sonnet-latest", ""), mk("claude-3-5-sonnet", "")), "claude-3-5-sonnet")
	// A name match outranks the every-part fallback, and a closer name outranks a looser one.
	eqStr(t, pick("fancy", mk("yyy", "super fancy thing"), mk("zzz", "fancy")), "zzz")
}

func TestRequestedModelIsDisclosedOnlyWhenItDiffers(t *testing.T) {
	a := map[string]any{"provider": "p", "id": "a", "name": "A"}
	b := map[string]any{"provider": "p", "id": "b", "name": "B"}
	avail := []map[string]any{a, b}
	eqStr(t, requestedModel("", avail, a), "")
	eqStr(t, requestedModel("p/a", avail, a), "")        // the same model: nothing to disclose
	eqStr(t, requestedModel("p/b", avail, a), "p/b")     // a different one
	eqStr(t, requestedModel("nonesuch", avail, a), "nonesuch") // one that resolves to nothing cannot have taken effect
	eqStr(t, requestedModel("p/a", avail, nil), "p/a")
}

// ---- the observer, details and wiring ----

func TestAnAbortedCompactionDoesNotCount(t *testing.T) {
	u := newUIAgent(nil, nil)
	u.observe(obj{"type": "compaction_end", "result": obj{}, "aborted": true}, nil)
	eq(t, u.compactions, 0)
	u.observe(obj{"type": "compaction_end", "result": obj{}}, nil)
	eq(t, u.compactions, 1)
}

func TestNotificationPreviewIsCutAtTheLimit(t *testing.T) {
	r := startRig(t)
	rec := &agentRecord{ID: "x", Description: "d", Status: statusCompleted, Result: strings.Repeat("y", 30)}
	d := r.app.ui.notificationDetails(rec, 10)
	eqStr(t, d.ResultPreview, strings.Repeat("y", 10)+"…")
	rec.Result = strings.Repeat("y", 10)
	eqStr(t, r.app.ui.notificationDetails(rec, 10).ResultPreview, strings.Repeat("y", 10))
}

func TestQueuedAgentStreamsAsRunning(t *testing.T) {
	r := startRig(t)
	rec := &agentRecord{ID: "q", Status: statusQueued, StartedAt: time.Now()}
	eqStr(t, r.app.runningDetails(rec, "general-purpose", "d", 0).Status, "running")
}

func TestForegroundRunsReportThemselvesAsBackgroundDetailsWhenBackground(t *testing.T) {
	r := startRig(t)
	raw, failure := r.Host.Tool("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose", "run_in_background": true})
	if failure != "" {
		t.Fatal(failure)
	}
	var res struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	eqStr(t, msgStr(res.Details["status"]), "background")
	if msgStr(res.Details["agentId"]) == "" {
		t.Errorf("no agent id: %v", res.Details)
	}
}

func TestAnAgentFileOutranksTheCallsModelAndThinking(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := "---\ndescription: pinned\nmodel: anthropic/claude-sonnet-4-6\nthinking: high\n---\nYou are pinned.\n"
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "agents", "pinned.md"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	r := startRigIn(t, cwd)
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "pinned", "model": "openai/gpt-5", "thinking": "low"})
	c := r.fleet.wait(t, 1)[0]
	eqStr(t, c.spec.Model, "anthropic/claude-sonnet-4-6")
	eqStr(t, c.spec.Thinking, "high")
}

type recordingObserver struct{ events atomic.Int32 }

func (o *recordingObserver) observe(map[string]any, func(string) (map[string]any, error)) {
	o.events.Add(1)
}

func TestTheRunnerFeedsEveryEventToTheObserver(t *testing.T) {
	o := &recordingObserver{}
	c := &rpcChild{observer: o, pending: map[string]chan map[string]any{}, settled: make(chan struct{})}
	c.handle(obj{"type": "message_start"})
	c.handle(obj{"type": "turn_start"})
	eq(t, int(o.events.Load()), 2)
}

func TestTheSurfacesAreLeftAloneWithoutAUI(t *testing.T) {
	cwd := t.TempDir()
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	r.app.register(e)
	no := false
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: r.answer, HasUI: &no})
	r.Fire("session_start", obj{"reason": "startup"})
	r.Fire("tool_execution_start", obj{})
	time.Sleep(100 * time.Millisecond)
	if n := len(r.CallsTo("ui.onTerminalInput")); n != 0 {
		t.Errorf("%d terminal input subscriptions without a UI", n)
	}
}

func TestAnOneAgentSettleStartsTheNextQueuedEvenWhenTheHookPanics(t *testing.T) {
	r := startRig(t)
	r.app.mgr.mu.Lock()
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.mu.Unlock()
	orig := r.app.mgr.onSettle
	r.app.mgr.onSettle = func(rec *agentRecord) { orig(rec); panic("settle hook") }
	spawnBackground(r, "first")
	spawnBackground(r, "second")
	first := r.fleet.wait(t, 1)[0]
	first.finish("done")
	r.fleet.wait(t, 2) // the queued agent starts although the hook of the first panicked
}

func TestTheViewerOverlayIsOpenedAtTheRequestedSize(t *testing.T) {
	r := startRig(t)
	r.Fire("tool_execution_start", obj{})
	go r.app.ui.ui.Custom(func(host viewerHost, th uiTheme, kb viewerKeybindings, done func()) viewerComponent {
		return panickingComponent{}
	}, overlayOptions{Anchor: "center", WidthPercent: 90, HeightPercent: 70}, func() {})
	r.waitFor("the overlay request", func() bool { return len(r.CallsTo("ui.custom")) > 0 })
	raw, _ := json.Marshal(r.CallsTo("ui.custom")[0].Args)
	hasStr(t, string(raw), "90%")
	hasStr(t, string(raw), "70%")
}

func TestConsecutiveWordKillsAccumulateInOneYank(t *testing.T) {
	in := newSteerInput()
	typed(in, "one two three")
	in.handleInput("\x17") // ctrl+w
	in.handleInput("\x17")
	eqStr(t, in.value, "one ")
	in.handleInput("\x19") // ctrl+y yanks both words as one entry
	eqStr(t, in.value, "one two three")
}

func TestAskedAnnotationOnlyWhenTheValuesDiffer(t *testing.T) {
	eqStr(t, asked("m", "m"), "m")
	eqStr(t, asked("m", ""), "m")
	eqStr(t, asked("m", "x"), "m (asked x)")
}

func TestARefusedSettingIsAnnouncedOncePerText(t *testing.T) {
	r := settingsRig(t, `{"scopeModels": true}`)
	count := func() int {
		n := 0
		for _, m := range notices(r) {
			if strings.Contains(m, "scopeModels") {
				n++
			}
		}
		return n
	}
	eq(t, count(), 1)
	r.Fire("session_start", obj{"reason": "reload"})
	eq(t, count(), 1)
}

func TestRunningDetailsCarryTheTurnCount(t *testing.T) {
	r := startRig(t)
	rec := &agentRecord{ID: "q", Status: statusRunning, StartedAt: time.Now()}
	d := r.app.runningDetails(rec, "general-purpose", "d", 0)
	if d.TurnCount == nil || *d.TurnCount != 1 {
		t.Errorf("turn count %v", d.TurnCount)
	}
}

func TestModelNotFoundListsTheAvailableModelsSorted(t *testing.T) {
	_, msg := resolveModelAmong("nonesuch", []map[string]any{
		{"provider": "z", "id": "last"}, {"provider": "a", "id": "first"}})
	hasStr(t, msg, "Available models:\n  a/first\n  z/last")
}

func TestUnresolvedAskedModelIsDisclosedEvenAgainstAnEmptyWinner(t *testing.T) {
	eqStr(t, requestedModel("nonesuch", nil, map[string]any{}), "nonesuch")
}

func TestNoUIMeansNoSurfaceIsBound(t *testing.T) {
	cwd := t.TempDir()
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	r.app.register(e)
	no := false
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: r.answer, HasUI: &no})
	r.Fire("session_start", obj{"reason": "startup"})
	r.Fire("tool_execution_start", obj{})
	r.app.ui.fleet.mu.Lock()
	bound := r.app.ui.fleet.ui != nil
	r.app.ui.fleet.mu.Unlock()
	if bound {
		t.Error("the fleet list was bound to a UI that does not exist")
	}
}

func TestAnUnchangedWidgetIsNotSentAgain(t *testing.T) {
	r := startRig(t)
	r.Fire("tool_execution_start", obj{})
	r.app.ui.ui.SetWidget("probe", func(int, uiTheme) []string { return []string{"same rows"} }, aboveEditor)
	for range 3 {
		r.app.ui.ui.RequestRender()
	}
	eq(t, len(r.widgetCalls("probe")), 1)
}
