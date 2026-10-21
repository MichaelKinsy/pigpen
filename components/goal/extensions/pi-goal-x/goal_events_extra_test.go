package pi_goal_x_test

// Behavior of the lifecycle handlers (goal-events.ts) that the original's ledger files only reach indirectly. Not twins: no upstream
// title. Each case drives the real SDK through the fake host.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

var evAt = time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC).UnixMilli()

type evFocused struct {
	*goalRig
	goal map[string]any
}

// focusedRig starts a session over one goal (patched fields) that the branch focuses, UI session, idle agent.
func focusedRig(t *testing.T, patch map[string]any, opts rigOptions) *evFocused {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	g := goal.WritePatchedGoalForRig(cwd, "Ship the extra tests", evAt, patch)
	opts.Cwd = cwd
	if opts.Settings == "" {
		opts.Settings = "-"
	}
	opts.Branch = []any{map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": g["id"], "reason": "created"}}}
	r := newGoalRig(t, opts)
	t.Cleanup(func() { r.Probe.Teardown() })
	r.Fire("session_start", map[string]any{"reason": "start"})
	return &evFocused{r, g}
}

// contexts fires the context event for messages and returns the messages it answered ("" for no change).
func contextMessages(t *testing.T, r *goalRig, messages []any) []map[string]any {
	t.Helper()
	raw := r.Fire("context", map[string]any{"messages": messages})
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out.Messages
}

func contents(messages []map[string]any) string {
	var parts []string
	for _, m := range messages {
		s, _ := m["content"].(string)
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n---\n")
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q not in %q", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, bads ...string) {
	t.Helper()
	for _, b := range bads {
		if strings.Contains(got, b) {
			t.Errorf("%q found in %q", b, got)
		}
	}
}

func TestContextInjectsTheGoalState(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	out := contents(contextMessages(t, r.goalRig, []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1}}))
	mustContain(t, out, "hello", "[PI GOAL ACTIVE goalId="+r.goal["id"].(string)+"]", "Goal snapshot:", "Limits: lifetime tokens=none")
	// A second request retains the tails it already sent at their anchors and answers the same state.
	again := contents(contextMessages(t, r.goalRig, []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1}}))
	mustContain(t, again, "[PI GOAL ACTIVE goalId=")
}

func TestContextPausedBlockedAndBudgetLimited(t *testing.T) {
	r := focusedRig(t, map[string]any{"status": "paused", "stopReason": "agent", "pauseReason": "waiting on review", "pauseSuggestedAction": "ask the user"}, rigOptions{})
	mustContain(t, contents(contextMessages(t, r.goalRig, nil)), "[PI GOAL PAUSED goalId=", "Pause reason: waiting on review", "Suggested action: ask the user", "/goal-resume")

	r = focusedRig(t, map[string]any{"status": "paused", "stopReason": "user"}, rigOptions{})
	out := contents(contextMessages(t, r.goalRig, nil))
	mustContain(t, out, "[PI GOAL PAUSED goalId=")
	mustNotContain(t, out, "Pause reason:")

	r = focusedRig(t, map[string]any{"status": "blocked", "pauseReason": "api is down"}, rigOptions{})
	mustContain(t, contents(contextMessages(t, r.goalRig, nil)), "[PI GOAL BLOCKED goalId=", "Blocker: api is down", "/goal-resume")

	r = focusedRig(t, map[string]any{"status": "budget_limited", "tokenBudget": 100, "usage": map[string]any{"tokensUsed": 150, "activeSeconds": 0}}, rigOptions{})
	out = contents(contextMessages(t, r.goalRig, nil))
	mustContain(t, out, "[PI GOAL BUDGET LIMITED goalId=")
	mustNotContain(t, out, "TOKEN BUDGET REACHED")
}

func TestContextUnfocusedPromptAndItsSetting(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	goal.WritePatchedGoalForRig(cwd, "Left open", evAt, nil)
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: "-", StartSession: true})
	mustContain(t, contents(contextMessages(t, r, nil)), "[PI GOAL UNFOCUSED]", "1 open pi goal exists")

	cwd = t.TempDir()
	goal.WritePatchedGoalForRig(cwd, "Left open", evAt, nil)
	r = newGoalRig(t, rigOptions{Cwd: cwd, Settings: `{"hideUnfocusedPrompt": true}`, StartSession: true})
	if got := contextMessages(t, r, nil); len(got) != 0 {
		t.Errorf("hideUnfocusedPrompt: %v", got)
	}
}

func TestContextRemovesAuditCardsAndCompactsCheckpoints(t *testing.T) {
	r := newGoalRig(t, rigOptions{StartSession: true})
	audit := map[string]any{"role": "custom", "customType": "pi-goal-audit-event", "content": "card", "display": true, "timestamp": 2}
	user := map[string]any{"role": "user", "content": "keep me", "timestamp": 1}
	checkpoint := map[string]any{"role": "custom", "customType": "pi-goal-event", "content": "legacy long text", "display": true, "timestamp": 3,
		"details": map[string]any{"kind": "checkpoint", "version": 1, "goalId": "g-1"}}
	got := contextMessages(t, r, []any{audit, user, checkpoint})
	if len(got) != 2 {
		t.Fatalf("messages %v", got)
	}
	if got[0]["content"] != "keep me" {
		t.Errorf("first message %v", got[0])
	}
	if got[1]["display"] != false || strings.Contains(got[1]["content"].(string), "legacy long text") {
		t.Errorf("checkpoint not normalized: %v", got[1])
	}
	d, _ := got[1]["details"].(map[string]any)
	if d["version"] != float64(2) || d["kind"] != "checkpoint" || d["goalId"] != "g-1" {
		t.Errorf("checkpoint details %v", d)
	}
	// Nothing to remove, nothing to compact, no goal: the context is left alone.
	if got := contextMessages(t, r, []any{user}); len(got) != 0 && len(got) != 1 {
		t.Errorf("unchanged context answered %v", got)
	}
}

func TestContextCarriesStallAuditorRejectionAndPostCompaction(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{Settings: `{"stallTimeoutMinutes": 1}`})
	id := r.goal["id"].(string)
	line := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) + "\n" }
	ledger := line(map[string]any{"type": "completion_requested", "goalId": id, "at": "2026-08-03T09:00:00.000Z"}) +
		line(map[string]any{"type": "audit_result", "goalId": id, "verdict": "disapproved", "report": "Tests were never run.", "at": "2026-08-03T09:01:00.000Z"})
	if err := os.WriteFile(filepath.Join(r.Cwd, ".pi", "goals", "goal_events.jsonl"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	goal.InvalidateLedgerForRig()
	r.Probe.FakeClock(time.Now().UnixMilli() + 70_000)
	r.Fire("session_compact", map[string]any{})
	out := contents(contextMessages(t, r.goalRig, nil))
	mustContain(t, out, "[GOAL STALLED goalId="+id+"]", "[AUDITOR REJECTION goalId="+id+"]", "Tests were never run.", "[POST-COMPACTION RESYNC goalId="+id+"]")
	// The post-compaction delta is one-shot.
	mustNotContain(t, contents(contextMessages(t, r.goalRig, nil)), "[POST-COMPACTION RESYNC")
}

func TestBeforeProviderRequestMovesTheCacheMarker(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	tails := contextMessages(t, r.goalRig, []any{map[string]any{"role": "user", "content": "history", "timestamp": 1}})
	live := tails[len(tails)-1]["content"].(string)
	payload := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "history"}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": live, "cache_control": map[string]any{"type": "ephemeral"}}}},
	}}
	raw := r.Fire("before_provider_request", map[string]any{"payload": payload})
	var out struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Messages) != 2 {
		t.Fatalf("answer %s (%v)", raw, err)
	}
	if out.Messages[0].Content[0]["cache_control"] == nil || out.Messages[1].Content[0]["cache_control"] != nil {
		t.Errorf("the explicit marker must move off the live tail onto history: %s", raw)
	}
	// A payload with no retained tail for this session is not touched.
	other := newGoalRig(t, rigOptions{StartSession: true})
	if got := other.Fire("before_provider_request", map[string]any{"payload": payload}); len(got) != 0 && string(got) != "null" {
		t.Errorf("untouched payload answered %s", got)
	}
}

func TestMessageEndHidesGoalEventMessages(t *testing.T) {
	r := newGoalRig(t, rigOptions{StartSession: true})
	raw := r.Fire("message_end", map[string]any{"message": map[string]any{"role": "custom", "customType": "pi-goal-event", "content": "x", "display": true}})
	var out struct {
		Message map[string]any `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Message["display"] != false || out.Message["content"] != "x" {
		t.Errorf("answer %s", raw)
	}
	for _, m := range []map[string]any{
		{"role": "custom", "customType": "pi-goal-event", "content": "x", "display": false},
		{"role": "custom", "customType": "other", "content": "x", "display": true},
		{"role": "user", "content": "x"},
	} {
		if got := r.Fire("message_end", map[string]any{"message": m}); len(got) != 0 && string(got) != "null" {
			t.Errorf("message %v answered %s", m, got)
		}
	}
}

// toolCall delivers a tool_call event and reads the handler's {block, reason}, whether the SDK answers it bare or wrapped in
// {_pigToolCallResult} (the fake host's Fire only reads the wrapped form).
func toolCall(r *goalRig, payload map[string]any) (bool, string) {
	h := r.Host
	h.mu.Lock()
	id, ok := h.handlers["tool_call"]
	h.mu.Unlock()
	if !ok {
		return false, ""
	}
	payload["type"] = "tool_call"
	args, _ := json.Marshal(payload)
	raw, failure := h.roundTrip(map[string]any{"method": "event", "event": "tool_call", "handler_id": id, "args": json.RawMessage(args)})
	if failure != "" {
		r.t.Errorf("tool_call handler failed: %s", failure)
	}
	var out struct {
		Block  bool            `json:"block"`
		Reason string          `json:"reason"`
		Result json.RawMessage `json:"_pigToolCallResult"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Result) > 0 {
		_ = json.Unmarshal(out.Result, &out)
	}
	return out.Block, out.Reason
}

func TestToolCallGates(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	// A rejected checkpoint denies every later tool call of the run.
	r.Fire("message_start", map[string]any{"message": map[string]any{"role": "custom", "customType": "pi-goal-event", "content": "x", "details": map[string]any{"kind": "checkpoint", "version": 3, "goalId": r.goal["id"], "generation": "g", "dispatchId": "d"}}})
	if b, reason := toolCall(r.goalRig, map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}}); !b || reason != "Stale goal dispatch; use /goal-resume." {
		t.Errorf("denied dispatch: %v %q", b, reason)
	}

	// A checkpoint for a goal that is not active blocks work tools, not inspection.
	r = focusedRig(t, nil, rigOptions{})
	r.Fire("before_agent_start", map[string]any{"prompt": `<pi_goal_continuation goal_id="ghost" kind="checkpoint">go`, "systemPromptOptions": map[string]any{}})
	b, reason := toolCall(r.goalRig, map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
	if !b || !strings.HasPrefix(reason, "Cannot call bash: the goal checkpoint that triggered this turn is no longer active. Goal ghost has been paused, cleared, or replaced.") {
		t.Errorf("stale checkpoint: %v %q", b, reason)
	}
	if b, _ := toolCall(r.goalRig, map[string]any{"toolName": "get_goal"}); b {
		t.Error("get_goal must stay available")
	}

	// An ordinary tool call is let through.
	r = focusedRig(t, nil, rigOptions{})
	if b, _ := toolCall(r.goalRig, map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}}); b {
		t.Error("ordinary tool call blocked")
	}
}

func TestToolCallRecordsTheFirstAttemptAgainstOracleAdvice(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	id := r.goal["id"].(string)
	goal.ArmOracleAdviceForRig(id, "fp-1", "advice-1")
	r.Fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "echo hi"}})
	if n := len(ledgerOf(t, r.Cwd, "oracle_followup_attempted")); n != 0 {
		t.Errorf("an echo is not a work attempt: %d events", n)
	}
	r.Fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
	events := ledgerOf(t, r.Cwd, "oracle_followup_attempted")
	if len(events) != 1 || events[0]["goalId"] != id || events[0]["fingerprint"] != "fp-1" || events[0]["adviceId"] != "advice-1" || events[0]["firstToolName"] != "bash" {
		t.Fatalf("events %v", events)
	}
	r.Fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
	if n := len(ledgerOf(t, r.Cwd, "oracle_followup_attempted")); n != 1 {
		t.Errorf("the marker is consumed: %d events", n)
	}
}

func TestTurnEndArchivesACompletedGoal(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	id := r.goal["id"].(string)
	r.Probe.CompleteFocused()
	r.Fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 1, "output": 1}}})
	if got := r.LastNotice(); !strings.HasPrefix(got, "info: Goal archived.\nFile: ") {
		t.Errorf("notice %q", got)
	}
	if r.Probe.FocusedGoalID() != "" || len(r.Probe.GoalIDs()) != 0 {
		t.Errorf("focus %q goals %v", r.Probe.FocusedGoalID(), r.Probe.GoalIDs())
	}
	if len(activeGoalFiles(t, r.Cwd)) != 0 {
		t.Errorf("active files %v", activeGoalFiles(t, r.Cwd))
	}
	archived := ledgerOf(t, r.Cwd, "goal_archived")
	if len(archived) != 1 || archived[0]["goalId"] != id || !strings.Contains(archived[0]["archivePath"].(string), "archived") {
		t.Errorf("goal_archived %v", archived)
	}
	if n := len(ledgerOf(t, r.Cwd, "goal_completed")); n != 1 {
		t.Errorf("goal_completed events %d", n)
	}
	entries := r.Entries()
	if last := entries[len(entries)-1]; last.CustomType != "pi-goal-focus" || last.Data["reason"] != "completed" || last.Data["focusedGoalId"] != nil {
		t.Errorf("focus entry %+v", last)
	}
}

func TestTurnEndReportsAnArchiveFailure(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	id := r.goal["id"].(string)
	dir := filepath.Join(r.Cwd, ".pi", "goals", "archived")
	_ = os.RemoveAll(dir)
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Probe.CompleteFocused()
	r.Fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop"}})
	if got := r.LastNotice(); !strings.HasPrefix(got, "warning: Failed to archive completed goal: ") || !strings.Contains(got, "The complete record remains at ") {
		t.Errorf("notice %q", got)
	}
	failed := ledgerOf(t, r.Cwd, "goal_archive_failed")
	if len(failed) != 1 || failed[0]["goalId"] != id || failed[0]["message"] == "" {
		t.Errorf("goal_archive_failed %v", failed)
	}
	if r.Probe.FocusedGoalID() != id {
		t.Error("the goal stays focused when archival failed")
	}
}

func TestTurnEndPausesOnlyOnAUserAbort(t *testing.T) {
	aborted := map[string]any{"role": "assistant", "stopReason": "aborted", "usage": map[string]any{"input": 0, "output": 0}}
	r := focusedRig(t, nil, rigOptions{})
	r.Fire("turn_end", map[string]any{"message": aborted})
	if r.Probe.Goal()["status"] != "active" {
		t.Error("a provider-side abort must not pause")
	}
	r.Host.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "run_signal", "args": map[string]any{"run": 1, "active": true, "aborted": true}}})
	r.Fire("turn_end", map[string]any{"message": aborted})
	if r.Probe.Goal()["status"] != "paused" {
		t.Errorf("a user abort pauses: %v", r.Probe.Goal()["status"])
	}
}

func TestAgentEndChargesAbortedTokensToTheRunningGoal(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	r.Fire("before_agent_start", map[string]any{"prompt": "go", "systemPromptOptions": map[string]any{}})
	r.Fire("agent_end", map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "stopReason": "aborted", "usage": map[string]any{"input": 400, "output": 100}},
		map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 9000, "output": 9000}},
	}})
	usage, _ := r.Probe.Goal()["usage"].(map[string]any)
	if usage["tokensUsed"] != float64(500) {
		t.Errorf("usage %v", usage)
	}
}

func TestSessionStartOnResume(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	a := goal.WritePatchedGoalForRig(cwd, "First open goal", evAt, nil)
	b := goal.WritePatchedGoalForRig(cwd, "Second open goal", evAt+1000, nil)
	var asked []string
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: "-", Select: func(title string, options []string) (string, bool) {
		asked = append(asked, title)
		for _, o := range options {
			if strings.Contains(o, "Second open goal") {
				return o, true
			}
		}
		return "", false
	}})
	r.Fire("session_start", map[string]any{"reason": "resume"})
	if len(asked) != 1 || asked[0] != "Focus open goal" {
		t.Fatalf("asked %v", asked)
	}
	if r.Probe.FocusedGoalID() != b["id"] {
		t.Errorf("focused %q, want %v (other %v)", r.Probe.FocusedGoalID(), b["id"], a["id"])
	}
	// A startup does not ask.
	r2 := newGoalRig(t, rigOptions{Cwd: cwd, Settings: "-", Select: func(string, []string) (string, bool) { t.Error("asked on startup"); return "", false }})
	r2.Fire("session_start", map[string]any{"reason": "startup"})
}

func TestSessionStartOnResumeOffersAPausedGoal(t *testing.T) {
	var asked []string
	r := focusedRig(t, map[string]any{"status": "paused", "stopReason": "user"}, rigOptions{Confirm: func(title, message string) bool {
		asked = append(asked, title+"|"+message)
		return true
	}})
	asked = nil
	r.Fire("session_start", map[string]any{"reason": "resume"})
	if len(asked) != 1 || asked[0] != "Resume paused goal?|Goal: Ship the extra tests" {
		t.Fatalf("asked %v", asked)
	}
	if r.Probe.Goal()["status"] != "active" {
		t.Errorf("status %v", r.Probe.Goal()["status"])
	}
}

func TestSessionTreeReloadsTheFocus(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	if r.Probe.FocusedGoalID() == "" {
		t.Fatal("not focused")
	}
	r.Host.write(nil)
	r.mu.Lock()
	r.branch = append(r.branch, map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": nil, "reason": "unfocused"}})
	r.mu.Unlock()
	r.Fire("session_tree", map[string]any{})
	if r.Probe.FocusedGoalID() != "" {
		t.Errorf("focus %q after the tree moved to an unfocused branch", r.Probe.FocusedGoalID())
	}
}

func TestSessionShutdownPersistsAndClearsTheWidget(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	r.Fire("session_shutdown", map[string]any{})
	ws := r.Widgets()
	if len(ws) == 0 || !ws[len(ws)-1].Clear {
		t.Errorf("the goal widget is cleared at shutdown: %+v", ws)
	}
	if len(activeGoalFiles(t, r.Cwd)) != 1 {
		t.Error("the goal stays on disk")
	}
}

func TestSessionCompactFlushesAndFailureWarnsOnlyForAGoal(t *testing.T) {
	r := newGoalRig(t, rigOptions{StartSession: true})
	r.Fire("session_compact_failed", map[string]any{"reason": "overflow", "aborted": false})
	if n := len(r.Notices()); n != 0 {
		t.Errorf("no goal, no warning: %v", r.Notices())
	}
	f := focusedRig(t, nil, rigOptions{})
	f.Fire("session_compact_failed", map[string]any{"reason": "overflow", "aborted": false})
	if got := f.LastNotice(); got != "warning: Goal context compaction failed (overflow). The goal stays active; retry with /compact or keep working." {
		t.Errorf("notice %q", got)
	}
}
