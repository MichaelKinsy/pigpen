package frontend

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func takeOps(h *harness) []any {
	h.t.Helper()
	var all []any
	for _, f := range h.frames() {
		if ops, ok := f.body["ops"].([]any); ok {
			all = append(all, ops...)
		}
	}
	return all
}
func TestOmpBillingSummaryFormatting(t *testing.T) {
	cases := []struct {
		name         string
		cost         float64
		sub          bool
		subagentCost float64
		want         string
	}{
		{"zero metered", 0, false, 0, ""},
		{"zero sub", 0, true, 0, ompSubscriptionIcon},
		{"metered cost", 1.5, false, 0, "$1.50"},
		{"sub cost", 0.01, true, 0, ompSubscriptionIcon + " 0.01"},
		{"sub cost + subagent", 0.54, true, 1.2, ompSubscriptionIcon + " 0.54 (+1.20)"},
		{"metered + subagent", 1.5, false, 0.25, "$1.50 (+0.25)"},
		{"zero primary + subagent metered", 0, false, 0.42, "$0.00 (+0.42)"},
		{"zero primary + subagent sub", 0, true, 0.42, ompSubscriptionIcon + " 0.00 (+0.42)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fe.Footer{
				UsingSubscription: tc.sub,
				UsageTotals:       fe.UsageTotals{Cost: tc.cost},
			}
			if got := ompCostSummary(f, tc.subagentCost); got != tc.want {
				t.Fatalf("ompCostSummary(%v, %v) = %q, want %q", f, tc.subagentCost, got, tc.want)
			}
		})
	}
}

func TestOmpAgentsHudPillAndComposerSubagentCost(t *testing.T) {
	h := readyOmpRoles(t)
	footer := sampleFooter()
	footer.UsingSubscription = true
	footer.UsageTotals.Cost = 0.54

	// 0 agents -> NO omp.hud node emitted at all.
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: footer},
	)
	if hud := node(ops, "dock.hud"); hud != nil {
		t.Fatalf("0-agent frame must not emit dock.hud: %#v", hud)
	}

	// Publish 0 running, 2 inspectable + subagent cost -> muted HUD pill + (+1.20) in composer usage.
	h.session.onAgentHubSnapshot(AgentHubSnapshot{
		RunningCount:     0,
		InspectableCount: 2,
		SubagentCost:     1.20,
		Agents: []AgentHubItem{
			{ID: "scout-1", DisplayName: "scout-1", AgentType: "scout", Status: "idle", Cost: 0.50},
			{ID: "worker-1", DisplayName: "worker-1", AgentType: "task", Status: "parked", Cost: 0.70, CanRevive: true},
		},
	})
	ops = takeOps(h)
	hud := node(ops, "dock.hud")
	if hud == nil || props(hud)["role"] != "omp.hud" || props(hud)["justify"] != "end" {
		t.Fatalf("dock.hud = %#v", hud)
	}
	seg := node(ops, "dock.hud.running.agents")
	if seg == nil || props(seg)["role"] != "omp.hud.seg" || !strings.Contains(props(seg)["title"].(string), "2 in this session") {
		t.Fatalf("muted hud seg = %#v", seg)
	}
	icon := node(ops, "dock.hud.running.agents.icon")
	if icon == nil || props(icon)["tone"] != "muted" {
		t.Fatalf("muted hud icon = %#v", icon)
	}
	if count := node(ops, "dock.hud.running.agents.count"); count != nil {
		t.Fatalf("muted hud must not emit count text: %#v", count)
	}
	usage := shownBy(ops)["dock.editor.editor.bar.usage"]
	if usage == nil || usage["title"] != "Session cost "+ompSubscriptionIcon+" 0.54 (+1.20)" || usage["text"] != " (+1.20)" {
		t.Fatalf("composer usage with subagent cost = %#v", usage)
	}

	// Publish 2 running agents -> accent icon + count "2".
	h.session.onAgentHubSnapshot(AgentHubSnapshot{
		RunningCount:     2,
		InspectableCount: 2,
		SubagentCost:     1.20,
		Agents: []AgentHubItem{
			{ID: "scout-1", DisplayName: "scout-1", AgentType: "scout", Status: "running", Cost: 0.50},
			{ID: "worker-1", DisplayName: "worker-1", AgentType: "task", Status: "running", Cost: 0.70},
		},
	})
	ops = takeOps(h)
	iconState := shownBy(ops)["dock.hud.running.agents.icon"]
	if iconState == nil || iconState["tone"] != "accent" {
		t.Fatalf("running hud icon = %#v", iconState)
	}
	count := node(ops, "dock.hud.running.agents.count")
	if count == nil {
		t.Fatalf("running hud missing count: %#v", ops)
	}
	spans, _ := props(count)["spans"].([]any)
	if len(spans) == 0 || spans[0].(map[string]any)["t"] != "2" {
		t.Fatalf("running hud count spans = %#v", spans)
	}

	// Clicking HUD pill while working (s.progress == true) still opens Agent Hub!
	_ = h.applyOps(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}))
	h.event(`{"ev":"action","sf":"pig","id":"dock.hud.running.agents","act":"agents.open"}`)
	h.synced()
	ops = takeOps(h)
	hub := node(ops, "layer.agent-hub")
	if hub == nil || hub["k"] != "picker" || props(hub)["title"] != "Agents" {
		t.Fatalf("clicking HUD pill while working did not open agent-hub picker: %#v", ops)
	}
}

func TestOmpAgentHubInteractionsAndTranscriptViewer(t *testing.T) {
	h := readyOmpRoles(t)
	var mu sync.Mutex
	var killed []string
	var revived []string
	var sent [][2]string
	transcripts := map[string][]AgentHubTranscriptEntry{
		"scout-1": {
			{Role: "user", Text: "Find all tests"},
			{Role: "assistant", Thinking: "Let me grep", Text: "Found 12 tests."},
			{Role: "tool", ToolName: "grep", ToolArgs: "TestOmp", ToolOutput: "omp_test.go:22"},
		},
	}

	h.session.killAgentFn = func(id string) (string, error) {
		mu.Lock()
		killed = append(killed, id)
		mu.Unlock()
		return "Killed " + id, nil
	}
	h.session.reviveAgentFn = func(id string) (string, error) {
		mu.Lock()
		revived = append(revived, id)
		mu.Unlock()
		return "Revived " + id, nil
	}
	h.session.loadAgentTranscriptFn = func(id string) []AgentHubTranscriptEntry {
		mu.Lock()
		defer mu.Unlock()
		return append([]AgentHubTranscriptEntry(nil), transcripts[id]...)
	}
	h.session.sendAgentMessageFn = func(id, body string) error {
		mu.Lock()
		sent = append(sent, [2]string{id, body})
		transcripts[id] = append(transcripts[id], AgentHubTranscriptEntry{Role: "user", Text: body})
		mu.Unlock()
		return nil
	}

	nowMs := time.Now().UnixMilli()
	snap := AgentHubSnapshot{
		RunningCount:     1,
		InspectableCount: 3,
		SubagentCost:     1.75,
		TotalDurationMs:  18_700,
		TotalRequests:    6,
		TotalTokens:      114_000,
		TotalTools:       4,
		Agents: []AgentHubItem{
			{
				ID: "scout-1", DisplayName: "scout-1", AgentType: "scout", Status: "running",
				Task: "Scan repo", Activity: "grep TestOmp", Model: "claude-sonnet-4",
				Cost: 0.50, DurationMs: 5_000, Requests: 2, Tools: 1, Tokens: 20_000,
				ContextTokens: 10_000, ContextWindow: 200_000,
			},
			{
				ID: "child-1", DisplayName: "child-1", AgentType: "task", ParentID: "scout-1", Status: "idle",
				Task: "Inspect child", Model: "claude-haiku-4", Cost: 0.25, DurationMs: 3_700,
				Requests: 1, Tools: 1, Tokens: 14_000,
			},
			{
				ID: "parked-1", DisplayName: "parked-1", AgentType: "reviewer", Status: "parked",
				Task: "Review PR", Model: "claude-opus-4", Cost: 1.00, DurationMs: 10_000,
				Requests: 3, Tools: 2, Tokens: 80_000, CanRevive: true,
			},
		},
		Activity: []AgentHubActivity{
			{ID: "act-1", AgentID: "scout-1", Kind: "tool", Title: "grep", Detail: "TestOmp", Status: "ok", TimestampMs: nowMs},
			{ID: "act-2", AgentID: "parked-1", Kind: "response", Title: "Review failed", Detail: "timeout", Status: "error", TimestampMs: nowMs},
		},
	}
	h.session.onAgentHubSnapshot(snap)
	takeOps(h)

	// Open Agent Hub via opener.
	h.session.openAgentHub()
	ops := takeOps(h)
	hub := node(ops, "layer.agent-hub")
	if hub == nil || props(hub)["layout"] != "rows" || props(hub)["preview"] != "side" {
		t.Fatalf("agent-hub initial = %#v", hub)
	}
	if sub, _ := props(hub)["subtitle"].(string); !strings.Contains(sub, "$1.75") || !strings.Contains(sub, "6 req") {
		t.Fatalf("agent-hub subtitle = %q", sub)
	}
	if previewTitle := node(ops, "layer.agent-hub.title"); previewTitle == nil {
		t.Fatalf("missing side preview title: %#v", ops)
	}

	// Scope switching: "parked" filters to parked-1 and enables Revive.
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"scope","value":"parked"}`)
	h.synced()
	ops = takeOps(h)
	if h.session.agentHubUI.selectedID != "parked-1" {
		t.Fatalf("parked scope selectedID = %q, want parked-1", h.session.agentHubUI.selectedID)
	}
	// Revive parked-1.
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"revive"}`)
	h.synced()
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(revived) == 1
	})
	takeOps(h)
	mu.Lock()
	gotRevived := append([]string(nil), revived...)
	mu.Unlock()
	if !reflect.DeepEqual(gotRevived, []string{"parked-1"}) {
		t.Fatalf("revived = %v, want [parked-1]", gotRevived)
	}

	// Switch back to "all" and toggle tree mode ("view" -> By parent).
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"scope","value":"all"}`)
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"view"}`)
	h.synced()
	ops = takeOps(h)
	if !h.session.agentHubUI.treeMode {
		t.Fatalf("expected treeMode=true after view action")
	}
	foundTree := false
	for _, op := range opsOn(ops, "layer.agent-hub") {
		if m, ok := op.([]any)[2].(map[string]any); ok && m["layout"] == "tree" {
			foundTree = true
		}
	}
	if !foundTree {
		t.Fatalf("tree toggle did not set layout=tree: %#v", ops)
	}

	// Select scout-1 and kill it.
	h.event(`{"ev":"select","sf":"pig","id":"layer.agent-hub","item":"scout-1"}`)
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"kill"}`)
	h.synced()
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(killed) == 1
	})
	takeOps(h)
	mu.Lock()
	gotKilled := append([]string(nil), killed...)
	mu.Unlock()
	if !reflect.DeepEqual(gotKilled, []string{"scout-1"}) {
		t.Fatalf("killed = %v, want [scout-1]", gotKilled)
	}

	// Switch to Activity tab and filter by errors.
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"tab","value":"activity"}`)
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"scope","value":"errors"}`)
	h.synced()
	takeOps(h)
	if h.session.agentHubUI.tab != "activity" || h.session.agentHubUI.selectedActID != "act-2" {
		t.Fatalf("activity errors state = tab:%q selectedAct:%q", h.session.agentHubUI.tab, h.session.agentHubUI.selectedActID)
	}

	// Switch back to Agents tab, open scout-1 transcript via activate.
	h.event(`{"ev":"action","sf":"pig","id":"layer.agent-hub","act":"tab","value":"agents"}`)
	h.event(`{"ev":"activate","sf":"pig","id":"layer.agent-hub","item":"scout-1"}`)
	h.synced()
	waitUntil(t, func() bool {
		h.session.mu.Lock()
		defer h.session.mu.Unlock()
		return h.session.agentHubUI != nil && h.session.agentHubUI.transcriptOpen
	})
	ops = takeOps(h)
	viewer := node(ops, "layer.agent-transcript")
	if viewer == nil || viewer["k"] != "overlay" || props(viewer)["role"] != "omp.hub.transcript" {
		t.Fatalf("transcript overlay = %#v", ops)
	}
	if msg0 := node(ops, "layer.agent-transcript.transcript.msg-0"); msg0 == nil || props(msg0)["role"] != "omp.user" {
		t.Fatalf("transcript user message = %#v", msg0)
	}
	if msg1 := node(ops, "layer.agent-transcript.transcript.msg-1"); msg1 == nil || props(msg1)["role"] != "omp.assistant" {
		t.Fatalf("transcript assistant message = %#v", msg1)
	}

	// Send a message from the transcript viewer via input OnSend.
	h.event(`{"ev":"send","sf":"pig","id":"layer.agent-transcript.compose.input","text":"check edge cases"}`)
	h.synced()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	gotSent := append([][2]string(nil), sent...)
	mu.Unlock()
	if want := [][2]string{{"scout-1", "check edge cases"}}; !reflect.DeepEqual(gotSent, want) {
		t.Fatalf("sent = %v, want %v", gotSent, want)
	}

	// Esc from transcript viewer returns to Agent Hub (consumed by HandleInput).
	if !h.session.HandleInput("\x1b") {
		t.Fatalf("Esc in transcript viewer should be consumed")
	}
	ops = takeOps(h)
	if !h.session.agentHubUI.open || h.session.agentHubUI.transcriptOpen {
		t.Fatalf("Esc should return to Agent Hub, got open=%v transcriptOpen=%v", h.session.agentHubUI.open, h.session.agentHubUI.transcriptOpen)
	}
	if hub := node(ops, "layer.agent-hub"); hub == nil {
		t.Fatalf("Esc from transcript did not restore layer.agent-hub: %#v", ops)
	}

	// Second Esc closes Agent Hub completely.
	if !h.session.HandleInput("\x1b") {
		t.Fatalf("Esc in Agent Hub should be consumed")
	}
	takeOps(h)
	if h.session.agentHubUI.open {
		t.Fatalf("second Esc should close Agent Hub")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for condition")
}

func TestOmpAgentHubKittyKeysAndFocusAndSelectorPreemption(t *testing.T) {
	h := readyOmpRoles(t)
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: sampleFooter()},
	)
	h.session.mu.Lock()
	h.session.sendAgentMessageFn = func(agentID, body string) error { return nil }
	h.session.mu.Unlock()
	h.session.onAgentHubSnapshot(AgentHubSnapshot{
		RunningCount:     2,
		InspectableCount: 2,
		Agents: []AgentHubItem{
			{ID: "a-1", DisplayName: "alpha", Status: "running"},
			{ID: "a-2", DisplayName: "beta", Status: "running"},
		},
	})
	takeOps(h)

	// Open Agent Hub -> emits OpFocus("") to release editor focus.
	h.session.openAgentHub()
	ops := takeOps(h)
	foundBlur := false
	for _, op := range ops {
		if arr, ok := op.([]any); ok && len(arr) >= 2 && arr[0] == "focus" && arr[1] == nil {
			foundBlur = true
		}
	}
	if !foundBlur {
		t.Fatalf("openAgentHub did not emit OpFocus(\"\"): %#v", ops)
	}

	// Kitty held Down repeat (\x1b[1;1:2B) moves selection from a-1 to a-2.
	if !h.session.HandleInput("\x1b[1;1:2B") {
		t.Fatalf("kitty Down repeat should be consumed")
	}
	if h.session.agentHubUI.selectedID != "a-2" {
		t.Fatalf("after kitty Down repeat selectedID = %q, want a-2", h.session.agentHubUI.selectedID)
	}

	// Kitty Esc release (\x1b[27;1:3u) is consumed without closing Agent Hub.
	if !h.session.HandleInput("\x1b[27;1:3u") {
		t.Fatalf("kitty Esc release should be consumed")
	}
	if !h.session.agentHubUI.open {
		t.Fatalf("kitty Esc release must not close Agent Hub")
	}

	// Ctrl+C (both legacy \x03 and kitty \x1b[99;5u) passes through to PiG!
	if h.session.HandleInput("\x03") {
		t.Fatalf("legacy Ctrl+C must pass through to PiG")
	}
	if h.session.HandleInput("\x1b[99;5u") {
		t.Fatalf("kitty Ctrl+C must pass through to PiG")
	}

	// Filter mode + bracketed paste appends into query.
	if !h.session.HandleInput("/") {
		t.Fatalf("/ should enter filter mode")
	}
	if !h.session.HandleInput("\x1b[200~alp\x1b[201~") {
		t.Fatalf("bracketed paste in filter mode should be consumed")
	}
	if h.session.agentHubUI.query != "alp" || h.session.agentHubUI.selectedID != "a-1" {
		t.Fatalf("after bracketed paste query=%q selectedID=%q", h.session.agentHubUI.query, h.session.agentHubUI.selectedID)
	}
	// Kitty Esc (\x1b[27u) exits filter mode first, then second kitty Esc closes Agent Hub and restores editor focus.
	if !h.session.HandleInput("\x1b[27u") {
		t.Fatalf("kitty Esc should exit filter mode")
	}
	if h.session.agentHubUI.filtering || !h.session.agentHubUI.open {
		t.Fatalf("first kitty Esc should exit filter mode only")
	}
	takeOps(h)
	if !h.session.HandleInput("\x1b[27u") {
		t.Fatalf("second kitty Esc should close Agent Hub")
	}
	ops = takeOps(h)
	if h.session.agentHubUI.open {
		t.Fatalf("second kitty Esc did not close Agent Hub")
	}
	foundEditorFocus := false
	for _, op := range ops {
		if arr, ok := op.([]any); ok && len(arr) >= 2 && arr[0] == "focus" && arr[1] == "dock.editor.editor.line.input" {
			foundEditorFocus = true
		}
	}
	if !foundEditorFocus {
		t.Fatalf("closing Agent Hub did not restore editor focus: %#v", ops)
	}

	// When a PiG fe.Selector arrives while Agent Hub is open, Agent Hub closes cleanly
	// and keys pass through to PiG's selector.
	h.session.openAgentHub()
	takeOps(h)
	if !h.session.agentHubUI.open {
		t.Fatalf("expected Agent Hub open")
	}
	_ = h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionDock,
		ID:     "selector",
		Index:  2,
		Node:   fe.Selector{Title: "Select model", Items: []fe.SelectorItem{{ID: "claude", Label: "claude"}}},
	})
	if h.session.agentHubUI.open {
		t.Fatalf("fe.Selector arrival must close Agent Hub")
	}
	if h.session.HandleInput("j") {
		t.Fatalf("keys must not be stolen while fe.Selector is open")
	}
}

func TestOmpAgentPickerItemRunningElapsedNotDoubled(t *testing.T) {
	nowMs := int64(1_700_000_010_000)
	item := ompAgentPickerItem(AgentHubItem{
		ID:           "scout-1",
		Status:       "running",
		StartedAtMs:  nowMs - 10_000,
		DurationMs:   9_000,
		LastActiveMs: nowMs - 1_000,
	}, false, 0, false, nowMs)
	got, ok := item.Facts["time"].(int64)
	if !ok || got != 10_000 {
		t.Fatalf("running time fact = %#v, want int64(10000)", item.Facts["time"])
	}

	// Fallback when DurationMs == 0 uses StartedAtMs.
	item2 := ompAgentPickerItem(AgentHubItem{
		ID:          "scout-2",
		Status:      "running",
		StartedAtMs: nowMs - 7_500,
	}, false, 0, false, nowMs)
	got2, ok := item2.Facts["time"].(int64)
	if !ok || got2 != 7_500 {
		t.Fatalf("fallback running time fact = %#v, want int64(7500)", item2.Facts["time"])
	}
}
