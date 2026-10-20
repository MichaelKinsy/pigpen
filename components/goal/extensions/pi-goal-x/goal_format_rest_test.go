package pi_goal_x

import (
	"strings"
	"testing"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

func gfParse(t *testing.T, raw string) *jsObject {
	t.Helper()
	v, err := parseJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

// gfText wraps a Text so Render drops the trailing width padding pi-tui adds (the original's tests .trim() too).
type gfText struct{ x *tui.Text }

func (g gfText) Render(w int) []string {
	out := g.x.Render(w)
	for i := range out {
		out[i] = strings.TrimRight(out[i], " ")
	}
	return out
}

func gf_renderGoalEvent(d any, e bool, th goalTheme) gfText { return gfText{renderGoalEvent(d, e, th)} }
func gf_renderGoalAuditEvent(c any, p string, th goalTheme) gfText {
	return gfText{renderGoalAuditEvent(c, p, th)}
}

// Expectations derive from goal-format.ts and from goal-enhancements.test.ts (E1 history block, E7 expandable detail) and
// goal-features.test.ts (F1 task detail block).

type gfTagTheme struct{}

func (gfTagTheme) fg(c, text string) string { return "<" + c + ">" + text + "</" + c + ">" }
func (gfTagTheme) bold(text string) string  { return text }

func gfEq(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func gfGoal(t *testing.T, objective, status string) *jsObject {
	t.Helper()
	return gfParse(t, `{"id":"g-1","objective":"`+objective+`","status":"`+status+`","autoContinue":true,"sisyphus":false,"usage":{"tokensUsed":0,"activeSeconds":0}}`)
}

func TestGoalFormatRestConstants(t *testing.T) {
	gfEq(t, stateEntryType, "pi-goal-state")
	gfEq(t, focusEntryType, "pi-goal-focus")
	gfEq(t, goalEventEntry, "pi-goal-event")
	gfEq(t, goalAuditEntry, "pi-goal-audit-event")
	gfEq(t, completeStatus, "complete")
	for _, n := range goalProgressToolNames {
		gfEq(t, goalProgressToolSet[n], true)
	}
	gfEq(t, len(goalProgressToolSet), len(goalProgressToolNames))
	gfEq(t, goalProgressToolSet["get_goal"], false)
}

func TestGoalFormatUsageLines(t *testing.T) {
	g := gfParse(t, `{"usage":{"tokensUsed":45000,"activeSeconds":125}}`)
	got := usageLines(g)
	gfEq(t, len(got), 2)
	gfEq(t, got[0], "Time spent: "+formatDuration(125))
	gfEq(t, got[1], "Tokens used: "+formatTokenValue(45000))
}

func TestGoalFormatNormalizeGoalEventDetails(t *testing.T) {
	d := normalizeGoalEventDetails(nil)
	gfEq(t, d.kind, "checkpoint")
	gfEq(t, d.goalId, "unknown")
	gfEq(t, d.status, "")
	gfEq(t, d.focus, "")
	gfEq(t, d.hasObjective || d.hasTimestamp || d.hasCurrentGoalId || d.currentStatusNull, false)
	d = normalizeGoalEventDetails(gfParse(t, `{"kind":"stale","goalId":"g","status":"active","objective":"o","timestamp":5,"currentGoalId":null,"currentStatus":null,"focus":"sisyphus"}`))
	gfEq(t, d.kind, "stale")
	gfEq(t, d.goalId, "g")
	gfEq(t, d.status, "active")
	gfEq(t, d.objective, "o")
	gfEq(t, d.timestamp, float64(5))
	gfEq(t, d.hasTimestamp, true)
	gfEq(t, d.hasCurrentGoalId && d.currentGoalIdNull, true)
	gfEq(t, d.currentStatusNull, true)
	gfEq(t, d.focus, "sisyphus")
	d = normalizeGoalEventDetails(gfParse(t, `{"kind":"other","goalId":3,"status":"budget_limited","currentGoalId":"c","currentStatus":"paused","focus":"goal","timestamp":"x","objective":4}`))
	gfEq(t, d.kind, "checkpoint")
	gfEq(t, d.goalId, "unknown")
	gfEq(t, d.status, "")
	gfEq(t, d.currentGoalId, "c")
	gfEq(t, d.currentGoalIdNull, false)
	gfEq(t, d.currentStatus, "paused")
	gfEq(t, d.currentStatusNull, false)
	gfEq(t, d.focus, "goal")
	gfEq(t, d.hasTimestamp || d.hasObjective, false)
	d = normalizeGoalEventDetails(gfParse(t, `{"status":"complete","focus":"x","currentStatus":"bogus"}`))
	gfEq(t, d.status, "complete")
	gfEq(t, d.focus, "")
	gfEq(t, d.currentStatus, "")
	gfEq(t, d.currentStatusNull, false)
}

func TestGoalFormatRenderGoalEvent(t *testing.T) {
	theme := gfTagTheme{}
	join := func(l []string) string { return strings.Join(l, "\n") }
	cp := gfParse(t, `{"kind":"checkpoint","goalId":"g1","status":"active","objective":"Ship it","currentGoalId":"g2","currentStatus":"paused"}`)
	gfEq(t, join(gf_renderGoalEvent(cp, false, theme).Render(300)), "<customMessageLabel>Goal </customMessageLabel><customMessageText>checkpoint</customMessageText>")
	gfEq(t, join(gf_renderGoalEvent(gfParse(t, `{"kind":"stale"}`), false, theme).Render(300)), "<customMessageLabel>Goal </customMessageLabel><customMessageText>stale checkpoint</customMessageText>")
	got := gf_renderGoalEvent(cp, true, theme).Render(300)
	gfEq(t, got[0], "<customMessageLabel>Goal checkpoint</customMessageLabel>")
	gfEq(t, got[1], "<customMessageText>Status: running")
	gfEq(t, got[2], "Objective: Ship it")
	gfEq(t, got[3], "Goal id: g1")
	gfEq(t, got[4], "Current: g2 (paused)</customMessageText>")
	// minimal: unknown status, no objective, no current
	got = gf_renderGoalEvent(nil, true, theme).Render(300)
	gfEq(t, strings.Join(got, "|"), "<customMessageLabel>Goal checkpoint</customMessageLabel>|<customMessageText>Status: unknown|Goal id: unknown</customMessageText>")
	// current line variants
	got = gf_renderGoalEvent(gfParse(t, `{"status":"paused","currentStatus":"complete"}`), true, theme).Render(300)
	gfEq(t, strings.Join(got, "|"), "<customMessageLabel>Goal checkpoint</customMessageLabel>|<customMessageText>Status: paused|Goal id: unknown|Current: none (complete)</customMessageText>")
	got = gf_renderGoalEvent(gfParse(t, `{"currentGoalId":"c"}`), true, theme).Render(300)
	gfEq(t, got[len(got)-1], "Current: c</customMessageText>")
	got = gf_renderGoalEvent(gfParse(t, `{"currentGoalId":null}`), true, theme).Render(300)
	gfEq(t, strings.Contains(strings.Join(got, "|"), "Current:"), false)
}

func TestGoalFormatRenderGoalAuditEvent(t *testing.T) {
	theme := gfTagTheme{}
	join := func(l []string) string { return strings.Join(l, "|") }
	for _, p := range []string{"approved", "rejected", "skipped", "started"} {
		gfEq(t, join(gf_renderGoalAuditEvent(nil, p, theme).Render(300)),
			"<customMessageLabel>Goal audit "+p+"</customMessageLabel>|<customMessageText>Goal audit "+p+".</customMessageText>")
	}
	gfEq(t, join(gf_renderGoalAuditEvent(nil, "", theme).Render(300)),
		"<customMessageLabel>Goal audit started</customMessageLabel>|<customMessageText>Goal audit started.</customMessageText>")
	gfEq(t, join(gf_renderGoalAuditEvent(nil, "weird", theme).Render(300)),
		"<customMessageLabel>Goal audit started</customMessageLabel>|<customMessageText>Goal audit started.</customMessageText>")
	gfEq(t, join(gf_renderGoalAuditEvent("custom body", "approved", theme).Render(300)),
		"<customMessageLabel>Goal audit approved</customMessageLabel>|<customMessageText>custom body</customMessageText>")
	gfEq(t, join(gf_renderGoalAuditEvent(float64(3), "rejected", theme).Render(300)),
		"<customMessageLabel>Goal audit rejected</customMessageLabel>|<customMessageText>Goal audit rejected.</customMessageText>")
}

func TestGoalFormatExtractGoalIdFromInjectedMessage(t *testing.T) {
	cases := []struct {
		in, id string
		ok     bool
	}{
		{`<pi_goal_continuation goal_id="g1" kind="checkpoint" v="2"/>`, "g1", true},
		{`<pi_goal_continuation goal_id="g1" kind="checkpoint" v="2" />`, "g1", true},
		{`<pi_goal_continuation goal_id="g1" kind="checkpoint" v="2">`, "g1", true},
		{`<pi_goal_continuation goal_id="g1" kind="checkpoint">`, "g1", true},
		{`<pi_goal_continuation goal_id="g1"`, "g1", true},
		{`<pi_goal_continuation goal_id="">`, "", false},
		{`<pi_goal_continuation   goal_id="g 2" kind="x">body`, "g 2", true},
		{`<pi_goal_continuationgoal_id="g1">`, "", false},
		{` <pi_goal_continuation goal_id="g1">`, "", false},
		{`[GOAL CHECKPOINT goalId=abc-1]`, "abc-1", true},
		{`[GOAL CONTINUATION goalId=abc]`, "abc", true},
		{`[GOAL STALE goalId=abc]`, "abc", true},
		{`[GOAL OTHER goalId=abc]`, "", false},
		{`[GOAL STALE goalId=a b]`, "", false},
		{"[GOAL STALE goalId=a\u00a0b]", "", false},
		{`[GOAL STALE goalId=]`, "", false},
		{`[GOAL STALE goalId=abc`, "", false},
		{`x [GOAL STALE goalId=abc]`, "", false},
		{`plain text`, "", false},
		{``, "", false},
	}
	for _, c := range cases {
		id, ok := extractGoalIdFromInjectedMessage(c.in)
		if id != c.id || ok != c.ok {
			t.Errorf("%q: got (%q,%v), want (%q,%v)", c.in, id, ok, c.id, c.ok)
		}
	}
	// v2 matcher needs v="2"; the legacy matcher takes any attributes.
	_, ok := matchV2SelfClosingContinuation(`<pi_goal_continuation goal_id="g" v="3"/>`)
	gfEq(t, ok, false)
	id, ok := matchLegacyContinuation(`<pi_goal_continuation goal_id="g" v="3"/>`)
	gfEq(t, id+"|"+jsBool(ok), "g|true")
	_, ok = matchLegacyBracketCheckpoint(`<pi_goal_continuation goal_id="g"/>`)
	gfEq(t, ok, false)
}

func TestGoalFormatGoalEventMessageId(t *testing.T) {
	cases := []struct {
		json, id string
		ok       bool
	}{
		{`{"customType":"pi-goal-event","details":{"goalId":"d1"},"content":"[GOAL STALE goalId=c1]"}`, "d1", true},
		{`{"customType":"pi-goal-event","details":{"goalId":""},"content":"[GOAL STALE goalId=c1]"}`, "c1", true},
		{`{"customType":"pi-goal-event","details":{"goalId":3},"content":"[GOAL STALE goalId=c1]"}`, "c1", true},
		{`{"customType":"pi-goal-event","content":"<pi_goal_continuation goal_id=\"c2\" kind=\"checkpoint\" v=\"2\"/>"}`, "c2", true},
		{`{"customType":"pi-goal-event","details":"x","content":"[GOAL CHECKPOINT goalId=c3]"}`, "c3", true},
		{`{"customType":"pi-goal-event","content":[{"type":"text"}]}`, "", false},
		{`{"customType":"pi-goal-event"}`, "", false},
		{`{"customType":"pi-goal-event","content":"nothing"}`, "", false},
		{`{"customType":"other","details":{"goalId":"d1"}}`, "", false},
		{`{"details":{"goalId":"d1"}}`, "", false},
	}
	for _, c := range cases {
		id, ok := goalEventMessageId(gfParse(t, c.json))
		if id != c.id || ok != c.ok {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", c.json, id, ok, c.id, c.ok)
		}
	}
	id, ok := goalEventMessageId(map[string]any{"customType": "pi-goal-event", "content": "[GOAL STALE goalId=m]"})
	gfEq(t, id+"|"+jsBool(ok), "m|true")
	_, ok = goalEventMessageId(nil)
	gfEq(t, ok, false)
}

func TestGoalFormatAssistantMessageClassifiers(t *testing.T) {
	mk := func(role, stop string) any {
		return gfParse(t, `{"role":"`+role+`","stopReason":"`+stop+`"}`)
	}
	gfEq(t, isAbortedAssistantMessage(mk("assistant", "aborted")), true)
	gfEq(t, isAbortedAssistantMessage(mk("user", "aborted")), false)
	gfEq(t, isAbortedAssistantMessage(mk("assistant", "error")), false)
	gfEq(t, isAbortedAssistantMessage("aborted"), false)
	gfEq(t, isAbortedAssistantMessage(nil), false)
	gfEq(t, isToolUseAssistantMessage(mk("assistant", "toolUse")), true)
	gfEq(t, isToolUseAssistantMessage(mk("user", "toolUse")), false)
	gfEq(t, isToolUseAssistantMessage(mk("assistant", "stop")), false)
	gfEq(t, isToolUseAssistantMessage(nil), false)
	gfEq(t, hasAbortedAssistantMessage([]any{mk("assistant", "stop"), mk("assistant", "aborted")}), true)
	gfEq(t, hasAbortedAssistantMessage([]any{mk("assistant", "stop"), mk("assistant", "error")}), false)
	gfEq(t, hasAbortedAssistantMessage(nil), false)
	gfEq(t, hasErrorAssistantMessage([]any{mk("assistant", "stop"), mk("assistant", "error")}), true)
	gfEq(t, hasErrorAssistantMessage([]any{mk("assistant", "aborted"), mk("user", "error")}), false)
	gfEq(t, hasErrorAssistantMessage(nil), false)
}

func TestGoalFormatTokens(t *testing.T) {
	gfEq(t, usageChannelTokens(float64(12.9)), float64(12))
	gfEq(t, usageChannelTokens(float64(-12.9)), float64(0))
	gfEq(t, usageChannelTokens(float64(0)), float64(0))
	gfEq(t, usageChannelTokens("5"), float64(0))
	gfEq(t, usageChannelTokens(nil), float64(0))
	gfEq(t, usageChannelTokens(float64(1)/zeroF()), float64(0))
	gfEq(t, usageChannelTokens(zeroF()/zeroF()), float64(0))
	gfEq(t, assistantTurnTokens(gfParse(t, `{"role":"assistant","usage":{"input":10.7,"output":5,"cacheRead":100}}`)), float64(15))
	gfEq(t, assistantTurnTokens(gfParse(t, `{"role":"assistant","usage":{"input":-3,"output":"x"}}`)), float64(0))
	gfEq(t, assistantTurnTokens(gfParse(t, `{"role":"user","usage":{"input":10,"output":5}}`)), float64(0))
	gfEq(t, assistantTurnTokens(gfParse(t, `{"role":"assistant"}`)), float64(0))
	gfEq(t, assistantTurnTokens(gfParse(t, `{"role":"assistant","usage":[1]}`)), float64(0))
	gfEq(t, assistantTurnTokens(nil), float64(0))
}

func zeroF() float64 { var z float64; return z }

func TestGoalFormatIsMeaningfulProgressToolCall(t *testing.T) {
	args := func(j string) any { return gfParse(t, j) }
	gfEq(t, isMeaningfulProgressToolCall("write", nil), true)
	gfEq(t, isMeaningfulProgressToolCall("get_goal", nil), false)
	gfEq(t, isMeaningfulProgressToolCall("unknown", nil), false)
	gfEq(t, isMeaningfulProgressToolCall("read", args(`{"path":"src/a.go"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("read", args(`{"path":".pi/goals"}`)), false)
	gfEq(t, isMeaningfulProgressToolCall("read", args(`{"path":".pi/goals/x.md"}`)), false)
	gfEq(t, isMeaningfulProgressToolCall("read", args(`{"path":".pi/goalsx"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("read", args(`{"path":5}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("read", nil), true)
	gfEq(t, isMeaningfulProgressToolCall("grep", args(`{"path":".pi/goals"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("bash", args(`{"command":"echo hi"}`)), false)
	gfEq(t, isMeaningfulProgressToolCall("bash", args(`{"command":"  \techo"}`)), false)
	gfEq(t, isMeaningfulProgressToolCall("bash", args(`{"command":"echoes"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("bash", args(`{"command":"ls; echo hi"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("bash", args(`{"command":"go test"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("bash", args(`{"command":7}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("write", args(`{"command":"echo hi","path":".pi/goals/x"}`)), true)
	gfEq(t, isMeaningfulProgressToolCall("update_goal_task", nil), true)
}

func TestGoalFormatBuildGoalTaskDetailBlock(t *testing.T) {
	goal := gfGoal(t, "F1 test", "active")
	gfEq(t, buildGoalTaskDetailBlock(goal), "")
	goal.set("taskList", gfParse(t, `{"tasks":[],"blockCompletion":false}`))
	gfEq(t, buildGoalTaskDetailBlock(goal), "")
	goal.set("taskList", gfParse(t, `{"tasks":[
		{"id":"t1","title":"Done task","status":"complete","evidence":"verified green"},
		{"id":"t2","title":"Next task","status":"pending","verificationContract":"run the suite"}],"blockCompletion":false}`))
	gfEq(t, buildGoalTaskDetailBlock(goal), strings.Join([]string{
		"Tasks: 1/2 tasks complete",
		"[ ] t2: Next task",
		"  contract: run the suite",
		"[x] t1: Done task — verified green",
	}, "\n"))
	// nesting, skipped, >3 pending, only two recent completions, evidence-less completion
	goal.set("taskList", gfParse(t, `{"tasks":[
		{"id":"a","title":"A","status":"complete","subtasks":[
			{"id":"a1","title":"A1","status":"pending","verificationContract":"c1","subtasks":[{"id":"a1x","title":"A1X","status":"pending"}]},
			{"id":"a2","title":"A2","status":"complete","evidence":"e2"}]},
		{"id":"b","title":"B","status":"complete"},
		{"id":"c","title":"C","status":"skipped"},
		{"id":"d","title":"D","status":"pending"},
		{"id":"e","title":"E","status":"pending"}],"blockCompletion":false}`))
	gfEq(t, buildGoalTaskDetailBlock(goal), strings.Join([]string{
		"Tasks: 3/8 tasks complete (1 skipped)",
		"  [ ] a1: A1",
		"    contract: c1",
		"    [ ] a1x: A1X",
		"[ ] d: D",
		"(+1 more pending — expand the dashboard with Ctrl+Shift+T)",
		"[x] a: A",
		"[x] a2: A2 — e2",
	}, "\n"))
}

func TestGoalWidgetPureHelpers(t *testing.T) {
	gfEq(t, formatModeLabelDebug(true), "Sisyphus (prompt/criteria style)")
	gfEq(t, formatModeLabelDebug(false), "Normal goal")
	gfEq(t, strings.Join(formatPrefixedLinesDebug("a\n\n  \n│ b\n  │ c\nd"), "|"), "│   a|│ b|  │ c|│   d")
	gfEq(t, len(formatPrefixedLinesDebug("")), 0)
	gfEq(t, strings.Join(formatSectionDebug("T", "x"), "|"), "|─── T ───||│   x")
	tasks := gfParse(t, `{"t":[
		{"id":"t1","title":"One","status":"complete","subtasks":[{"id":"t1a","title":"Sub","status":"skipped","lightweightSubtasks":true},{"id":"t1b","title":"Sub2","status":"pending"}]},
		{"id":"t2","title":"Two","status":"pending","lightweightSubtasks":true}]}`)
	gfEq(t, strings.Join(renderDebugTaskLines(tasks.vals["t"].([]any), 0), "|"),
		"[x] t1: One|  [~] t1a: Sub (lightweight)|  [ ] t1b: Sub2|[ ] t2: Two (lightweight)")
	gfEq(t, renderDebugTaskLines(tasks.vals["t"].([]any)[:1], 2)[0], "    [x] t1: One")
}
