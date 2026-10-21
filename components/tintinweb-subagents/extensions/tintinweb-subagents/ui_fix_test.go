package tintinweb_subagents

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func openViewer(h *fleetHarness) viewerComponent {
	h.press(keyDown) // activate (main)
	h.press(keyDown) // the agent
	h.press(keyEnter)
	return h.overlayComponent()
}

func TestStopFromTheViewerDoesNotBlockInput(t *testing.T) {
	h := newFleetHarness(t, []*agentView{idRecord("live", "the one")})
	h.manager.abortGate = make(chan struct{})
	v := openViewer(h)
	done := make(chan struct{})
	go func() { v.HandleInput("x"); v.HandleInput("x"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the second x blocked the input path on the abort")
	}
	close(h.manager.abortGate)
}

func TestStopNoticeQuotesTheDescriptionVerbatim(t *testing.T) {
	h := newFleetHarness(t, []*agentView{idRecord("live", "say \"hi\"\\n")})
	v := openViewer(h)
	v.HandleInput("x")
	v.HandleInput("x")
	want := "Stopped \"say \"hi\"\\n\"."
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.ui.mu.Lock()
		got := append([]string(nil), h.ui.notices...)
		h.ui.mu.Unlock()
		for _, n := range got {
			if n == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("no %q notice", want)
}

func TestViewerHoldsTheActivityItOpenedWith(t *testing.T) {
	src := activityMap{}
	f := newFleetList(&stubFleetManager{fixedList: &fixedList{}}, src, nil, nil, nil)
	if f.activityOfFn("a")() != nil {
		t.Fatal("an agent without activity")
	}
	src["a"] = &agentActivity{ToolUses: 2}
	held := f.activityOfFn("a") // opened while it runs
	late := f.activityOfFn("b")
	src["b"] = &agentActivity{ToolUses: 9}
	if late() != nil {
		t.Error("an agent that had no activity when the viewer opened must not gain one")
	}
	if got := held(); got == nil || got.ToolUses != 2 {
		t.Fatalf("live: %+v", got)
	}
	src["a"] = &agentActivity{ToolUses: 5}
	if got := held(); got.ToolUses != 5 {
		t.Errorf("follows the agent: %+v", got)
	}
	delete(src, "a") // the agent settled: its activity leaves the map, the viewer keeps the last state
	if got := held(); got == nil || got.ToolUses != 5 {
		t.Errorf("frozen: %+v", got)
	}
}

func TestSettledAgentsHaveNoLiveActivity(t *testing.T) {
	r := startRig(t)
	id := spawnBackground(r, "short")
	u := r.uiAgentOf(id)
	u.observe(obj{"type": "agent_start"}, nil)
	u.observe(obj{"type": "turn_end"}, nil)
	live := liveActivities{r.app.ui}
	if live.activityOf(id) == nil {
		t.Fatal("a running agent has activity")
	}
	r.fleet.wait(t, 1)[0].finish("done")
	r.waitEmitted("subagents:completed")
	r.waitFor("the activity to be dropped", func() bool { return live.activityOf(id) == nil })
	// The final result and the notification still read the turn count.
	if r.app.ui.activityOf(id) == nil {
		t.Error("the settled activity is still readable for the details")
	}
}

func TestMaxTurnsZeroIsNoLimitToDisclose(t *testing.T) {
	zero, three := 0, 3
	if inv := buildInvocation(sdk.Context{}, resolvedInvocation{maxTurns: &zero}, false, false); inv.MaxTurns != nil {
		t.Errorf("max turns 0 surfaced: %d", *inv.MaxTurns)
	}
	if inv := buildInvocation(sdk.Context{}, resolvedInvocation{maxTurns: &three}, false, false); inv.MaxTurns == nil || *inv.MaxTurns != 3 {
		t.Errorf("max turns 3: %v", inv.MaxTurns)
	}
}

func TestFleetListIsBoundAgainAfterAShutdown(t *testing.T) {
	r := startRig(t)
	r.Fire("tool_execution_start", obj{})
	r.waitFor("the first subscription", func() bool { return len(r.CallsTo("ui.onTerminalInput")) >= 1 })
	n := len(r.CallsTo("ui.onTerminalInput"))
	r.Fire("session_shutdown", obj{})
	r.Fire("tool_execution_start", obj{})
	r.waitFor("a new subscription", func() bool { return len(r.CallsTo("ui.onTerminalInput")) > n })
}

func TestViewerMarkdownPersistFailureWarnsAndAnnounces(t *testing.T) {
	r := startRig(t)
	old := settingsWriter
	settingsWriter = func(subagentsSettings, string) bool { return false }
	t.Cleanup(func() { settingsWriter = old })
	r.app.ui.chooseViewerMarkdown(viewerMarkdownAll)
	r.waitEmitted("subagents:settings_changed")
	found := false
	for _, n := range notices(r) {
		if n == "Viewer markdown set to all (session only; failed to persist)" {
			found = true
		}
	}
	if !found {
		t.Errorf("notices: %q", notices(r))
	}
	// A successful write is silent but still announced.
	before := len(notices(r))
	settingsWriter = func(subagentsSettings, string) bool { return true }
	r.app.ui.chooseViewerMarkdown(viewerMarkdownOff)
	time.Sleep(50 * time.Millisecond)
	if len(notices(r)) != before {
		t.Errorf("a saved choice should not notify: %q", notices(r)[before:])
	}
}

func TestShutdownCancelsPendingCompletionNotices(t *testing.T) {
	r := startRig(t)
	spawnBackground(r, "late")
	r.fleet.wait(t, 1)[0].finish("done")
	r.waitEmitted("subagents:completed")
	r.Fire("session_shutdown", obj{})
	time.Sleep(nudgeHold + 300*time.Millisecond)
	if n := len(r.messages()); n != 0 {
		t.Errorf("%d notification(s) sent after the session ended", n)
	}
}

func TestSettingsChangedPayloadCarriesTheSettings(t *testing.T) {
	s := defaultUISettings()
	s.viewerMarkdown = viewerMarkdownAll
	m := s.asMap()
	if m["viewerMarkdown"] != "all" || m["widgetMode"] != "background" || m["fleetView"] != true {
		t.Errorf("%v", m)
	}
}

// pi-tui's truncateToWidth cuts with "..." (three cells), never a single "…".
func TestEveryCutUsesPiTuisThreeDots(t *testing.T) {
	h := newFleetHarness(t, []*agentView{idRecord("a1", "a very long agent description that cannot fit the row")})
	rows := h.render(30)
	if !anyLine(rows, "...") || strings.Contains(strings.Join(rows, "\n"), "…") {
		t.Errorf("fleet row: %q", rows)
	}
	narrow := h.render(12)
	if hint := narrow[0]; !strings.HasSuffix(ansiSGR.ReplaceAllString(hint, ""), "...") {
		t.Errorf("fleet hint: %q", hint)
	}
	v, _ := newViewer(30, mockSession(obj{"role": "assistant", "content": []any{obj{"type": "toolCall", "name": "a_tool_with_a_very_long_name"}}}),
		mockRecordView(nil), nil, plainTheme(), viewerOptions{})
	out := strings.Join(v.Render(20), "\n")
	if !strings.Contains(out, "...") || strings.Contains(out, "…") {
		t.Errorf("viewer: %q", out)
	}
}

func TestPersistingTheMarkdownModeRunsOutsideTheViewerLock(t *testing.T) {
	var v *conversationViewer
	done := make(chan struct{})
	v, _ = newViewer(30, mockSession(), mockRecordView(nil), nil, plainTheme(), viewerOptions{OnMarkdownMode: func(viewerMarkdownMode) {
		v.Render(80) // would deadlock if the viewer were still locked
		close(done)
	}})
	go v.HandleInput("m")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnMarkdownMode ran under the viewer's lock")
	}
}
