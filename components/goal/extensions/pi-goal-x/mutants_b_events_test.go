package pi_goal_x_test

// Extra tests (not twins), lane B: the lifecycle handlers of goal-events.ts (see mutants_b_test.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func mbCtxContents(t *testing.T, r *goalRig, messages []any) string {
	t.Helper()
	return contents(contextMessages(t, r, messages))
}

// events-complete-injects, events-paused-reason-default, events-paused-auditor-inverted, events-paused-auditor-cap,
// events-blocked-default, events-auditor-active-cap, events-context-unchanged-answers
func TestMutantsBContextGoalStates(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	r.Probe.CompleteFocused()
	if got := contextMessages(t, r.goalRig, nil); len(got) != 0 {
		t.Errorf("a completed goal injects nothing, got %v", got)
	}

	r = focusedRig(t, map[string]any{"status": "paused", "stopReason": "agent"}, rigOptions{})
	mustContain(t, mbCtxContents(t, r.goalRig, nil), "\nPause reason: (unknown)\n")

	r = focusedRig(t, map[string]any{"status": "blocked"}, rigOptions{})
	mustContain(t, mbCtxContents(t, r.goalRig, nil), "\nBlocker: unspecified\nThe goal is blocked; the user must run /goal-resume before goal work continues.")

	report := strings.Repeat("m", 400)
	rejection := "[AUDITOR REJECTION] An independent auditor previously rejected a completion request for this goal. Reason: " + strings.Repeat("m", 300) +
		"\nAddress the auditor's objections before requesting completion again."
	r = focusedRig(t, map[string]any{"status": "paused", "stopReason": "user"}, rigOptions{})
	goal.ToolsAppendLedger(r.Cwd, `{"type":"audit_result","goalId":"`+r.goal["id"].(string)+`","verdict":"disapproved","report":"`+report+`","at":"2026-08-03T09:01:00.000Z"}`)
	out := mbCtxContents(t, r.goalRig, nil)
	mustContain(t, out, "\n\n"+rejection+"\n\nThe goal is paused.")
	mustNotContain(t, out, strings.Repeat("m", 301))
	r = focusedRig(t, map[string]any{"status": "paused", "stopReason": "user"}, rigOptions{})
	goal.ToolsAppendLedger(r.Cwd, `{"type":"audit_result","goalId":"`+r.goal["id"].(string)+`","verdict":"approved","report":"fine","at":"2026-08-03T09:01:00.000Z"}`)
	mustNotContain(t, mbCtxContents(t, r.goalRig, nil), "[AUDITOR REJECTION")

	r = focusedRig(t, nil, rigOptions{})
	id := r.goal["id"].(string)
	goal.ToolsAppendLedger(r.Cwd, `{"type":"completion_requested","goalId":"`+id+`","at":"2026-08-03T09:00:00.000Z"}`)
	goal.ToolsAppendLedger(r.Cwd, `{"type":"audit_result","goalId":"`+id+`","verdict":"disapproved","report":"`+report+`","at":"2026-08-03T09:01:00.000Z"}`)
	out = mbCtxContents(t, r.goalRig, nil)
	mustContain(t, out, "\n\n[AUDITOR REJECTION goalId="+id+"]\nAn independent auditor previously rejected a completion request for this goal. Reason: "+strings.Repeat("m", 300)+
		"\nAddress the auditor's objections before requesting completion again.")
	mustNotContain(t, out, strings.Repeat("m", 301))

	// No goal and nothing to compact or filter: the handler answers nothing at all.
	plain := newGoalRig(t, rigOptions{StartSession: true})
	raw := plain.Fire("context", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi", "timestamp": 1}}})
	if string(raw) != `{"_pigContextUnchanged":true}` {
		t.Errorf("an unchanged context is not answered, got %s", raw)
	}
}

// events-budget-over-threshold, events-budget-remaining-text, events-budget-detail-dropped, events-budget-line-dropped
func TestMutantsBContextBudget(t *testing.T) {
	r, _ := budgetRig(t, "=== Goal ===\nObjective: Budgeted goal", 100, 80)
	r.Fire("session_start", map[string]any{"reason": "start"})
	r.Fire("turn_end", map[string]any{"message": turnEndMessage(30)})
	r.Probe.SetTokenBudget(1000)
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "", "systemPromptOptions": map[string]any{}})
	first := mbCtxContents(t, r, nil)
	mustContain(t, first, "\nLifetime ", "(Lifetime ", " \u2014 890 tokens remaining). Wrap up the current work in one final response")

	// A spent budget has nothing remaining.
	r, _ = budgetRig(t, "=== Goal ===\nObjective: Budgeted goal", 100, 80)
	r.Fire("session_start", map[string]any{"reason": "start"})
	r.Fire("turn_end", map[string]any{"message": turnEndMessage(30)})
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "", "systemPromptOptions": map[string]any{}})
	spent := mbCtxContents(t, r, nil)
	mustContain(t, spent, " \u2014 0 tokens remaining). Wrap up the current work in one final response")
	mustNotContain(t, spent, "over the budget")
	mustContain(t, spent, "[PI GOAL BUDGET LIMITED goalId=")
}

// events-active-settings-nil
func TestMutantsBContextUsageSnapshot(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{ContextUsage: map[string]any{"tokens": 100, "contextWindow": 1000, "percent": 10}})
	mustContain(t, mbCtxContents(t, r.goalRig, nil), "Context snapshot: 100/1000 tokens (10%)")
}

// events-postcompact-other-count, events-postcompact-no-ledger
func TestMutantsBPostCompaction(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	id := r.goal["id"].(string)
	goal.ToolsGoal(r.Cwd, "Other one", false, evAt+1000, "")
	goal.ToolsGoal(r.Cwd, "Other two", false, evAt+2000, "")
	goal.ToolsAppendLedger(r.Cwd, `{"type":"completion_requested","goalId":"`+id+`","at":"2026-08-03T09:00:00.000Z"}`)
	r.Fire("session_start", map[string]any{"reason": "start"})
	r.Fire("session_compact", map[string]any{})
	out := mbCtxContents(t, r.goalRig, nil)
	mustContain(t, out, "[POST-COMPACTION RESYNC goalId="+id+"]\nRecent events:\n  09:00:00 completion_requested\nOther open goals: 2\nContinue from authoritative files and goal storage.")
}

// events-context-base-lost
func TestMutantsBContextKeepsCompactedBase(t *testing.T) {
	r := newGoalRig(t, rigOptions{StartSession: true})
	user := map[string]any{"role": "user", "content": "keep me", "timestamp": 1}
	checkpoint := map[string]any{"role": "custom", "customType": "pi-goal-event", "content": "legacy long text", "display": true, "timestamp": 3,
		"details": map[string]any{"kind": "checkpoint", "version": 1, "goalId": "g-1"}}
	// With a goal, the answer is the compacted conversation plus the goal tails.
	f := focusedRig(t, nil, rigOptions{})
	out := mbCtxContents(t, f.goalRig, []any{user, checkpoint})
	mustContain(t, out, "keep me", "[PI GOAL ACTIVE goalId=")
	mustNotContain(t, out, "legacy long text")
	_ = r
}

func mbUsage(r *goalRig) (tokens, seconds float64) {
	u, _ := r.Probe.Goal()["usage"].(map[string]any)
	tokens, _ = u["tokensUsed"].(float64)
	seconds, _ = u["activeSeconds"].(float64)
	return
}

// events-agent-start-no-begin, events-turn-start-no-turn, events-turn-start-no-begin-accounting, events-turn-start-no-activity,
// events-toolcall-flag-not-set, events-toolexec-no-activity, events-toolexec-no-account
func TestMutantsBTurnEvents(t *testing.T) {
	const settings = `{"maxAutonomousRuns": 5, "stallTimeoutMinutes": 1}`
	e := newEvRig(t, settings, nil)
	e.startSession()
	if e.Probe.RigRuntime().SchedInRun {
		t.Error("no run before agent_start")
	}
	e.fire("agent_start", map[string]any{})
	rt := e.Probe.RigRuntime()
	if !rt.SchedInRun || rt.RunningGoalID != "golden_fixture_goal" {
		t.Errorf("agent_start begins the run: %+v", rt)
	}
	// A declaration is invalidated by the next model turn.
	if !e.Probe.SchedulerDeclareReadyTerminate() {
		t.Fatal("declare ready")
	}
	if !e.Probe.RigRuntime().SchedDeclared {
		t.Fatal("declared")
	}
	e.fire("turn_start", map[string]any{})
	if e.Probe.RigRuntime().SchedDeclared {
		t.Error("turn_start invalidates the declaration")
	}
	sch, _ := e.Probe.Goal()["scheduler"].(map[string]any)
	if sch["phase"] != "idle" {
		t.Errorf("the scheduler goes idle on a new turn: %v", sch)
	}
	e.fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
	if !e.Probe.RigRuntime().GoalWorkToolCalled {
		t.Error("a work tool call is recorded for the turn")
	}
	e.fire("turn_start", map[string]any{})
	if e.Probe.RigRuntime().GoalWorkToolCalled {
		t.Error("the record is reset by the next turn")
	}
}

// events-turn-start-no-begin-accounting, events-toolexec-no-account
func TestMutantsBTurnAccounting(t *testing.T) {
	e := newEvRig(t, "", nil)
	fc := e.Probe.FakeClock(time.Now().UnixMilli())
	e.startSession()
	_, base := mbUsage(e.goalRig)
	fc.Tick(10_000)
	e.fire("turn_start", map[string]any{}) // accounting starts over here
	fc.Tick(5_000)
	e.fire("tool_execution_end", map[string]any{})
	if _, seconds := mbUsage(e.goalRig); seconds-base != 5 {
		t.Errorf("a turn is charged from its start: %v seconds", seconds-base)
	}
	fc.Tick(7_000)
	e.fire("tool_execution_end", map[string]any{})
	if _, seconds := mbUsage(e.goalRig); seconds-base != 12 {
		t.Errorf("a tool run is charged when it ends: %v seconds", seconds-base)
	}
}

// events-turn-start-no-activity, events-toolexec-no-activity: activity keeps a goal from stalling
func TestMutantsBActivityKeepsGoalAlive(t *testing.T) {
	for _, event := range []string{"turn_start", "tool_execution_end"} {
		r := focusedRig(t, nil, rigOptions{Settings: `{"stallTimeoutMinutes": 1}`})
		goal.SaveGoalSettingsForRig(r.Cwd, `{"stallTimeoutMinutes": 1}`) // drops the settings cache earlier tests left behind
		fc := r.Probe.FakeClock(time.Now().UnixMilli())
		fc.Tick(70_000)
		r.Fire(event, map[string]any{})
		mustNotContain(t, mbCtxContents(t, r.goalRig, nil), "[GOAL STALLED")
		fc.Tick(70_000)
		mustContain(t, mbCtxContents(t, r.goalRig, nil), "[GOAL STALLED goalId=")
	}
}

func mbDiskUsage(t *testing.T, cwd string) (tokens, seconds float64) {
	t.Helper()
	g := goal.ToolsActiveGoal(cwd)
	u, _ := g["usage"].(map[string]any)
	tokens, _ = u["tokensUsed"].(float64)
	seconds, _ = u["activeSeconds"].(float64)
	return
}

func mbNormalTurnEnd() map[string]any {
	return map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 0, "output": 0}}}
}

// events-turnend-tokens-dropped, events-turnend-error-continues, events-turnend-archive-twice
func TestMutantsBTurnEnd(t *testing.T) {
	e := newEvRig(t, "", nil)
	e.startSession()
	tokens, _ := mbUsage(e.goalRig)
	e.fire("turn_end", map[string]any{"message": turnEndMessage(30)})
	if got, _ := mbUsage(e.goalRig); got-tokens != 30 {
		t.Errorf("a turn's tokens are charged to the goal: %v", got-tokens)
	}

	// A complete goal waits for its archival until a turn ends without an error.
	e = newEvRig(t, "", nil)
	e.startSession()
	e.Probe.CompleteFocused()
	e.fire("turn_end", map[string]any{"message": errMessage("boom")})
	if n := len(activeGoalFiles(t, e.Cwd)); n != 1 {
		t.Errorf("an error turn does not archive: %d active files", n)
	}
	if n := len(ledgerOf(t, e.Cwd, "goal_archived")); n != 0 {
		t.Errorf("goal_archived after an error turn: %d", n)
	}
	e.fire("turn_end", mbNormalTurnEnd())
	if n := len(activeGoalFiles(t, e.Cwd)); n != 0 {
		t.Errorf("the next turn archives: %d active files", n)
	}
	if n := len(ledgerOf(t, e.Cwd, "goal_archived")); n != 1 {
		t.Errorf("goal_archived: %d", n)
	}
	if m := e.lastMessage(); !strings.HasPrefix(m, "Goal archived.\nFile: .pi/goals/archived/") {
		t.Errorf("archive notice %q", m)
	}

	// Already archived: not archived twice.
	e = newEvRig(t, "", nil)
	e.startSession()
	e.Probe.CompleteFocused()
	e.Probe.SetGoalField("archivedPath", ".pi/goals/archived/x.md")
	e.fire("turn_end", mbNormalTurnEnd())
	if n := len(activeGoalFiles(t, e.Cwd)); n != 1 {
		t.Errorf("a goal that records its archive is not archived again: %d active files", n)
	}
	if n := len(ledgerOf(t, e.Cwd, "goal_archived")); n != 0 {
		t.Errorf("goal_archived: %d", n)
	}
}

// events-archive-error-text, events-archive-fail-unknown, events-archive-fail-path, events-archive-fail-message
func TestMutantsBArchiveFailures(t *testing.T) {
	// The archive directory cannot be written.
	e := newEvRig(t, "", nil)
	e.startSession()
	e.Probe.CompleteFocused()
	archived := filepath.Join(e.Cwd, ".pi", "goals", "archived")
	if err := os.RemoveAll(archived); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archived, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.fire("turn_end", mbNormalTurnEnd())
	m := e.lastMessage()
	evMatch(t, m, `^Failed to archive completed goal: .+\. The complete record remains at \.pi/goals/active_goal_fixture\.md\.$`, "failure notice")
	failed := ledgerOf(t, e.Cwd, "goal_archive_failed")
	if len(failed) != 1 || failed[0]["message"] == "" || failed[0]["message"] == "archive write failed" {
		t.Errorf("goal_archive_failed carries the write error: %v", failed)
	}
	if n := len(activeGoalFiles(t, e.Cwd)); n != 1 {
		t.Errorf("the complete record stays at its active path: %d", n)
	}

	// A rejected mutation without a message.
	e = newEvRig(t, "", nil)
	e.startSession()
	e.Probe.CompleteFocused()
	restore := e.Probe.RejectMutations("")
	e.fire("turn_end", mbNormalTurnEnd())
	restore()
	failed = ledgerOf(t, e.Cwd, "goal_archive_failed")
	if len(failed) != 1 || failed[0]["message"] != "archive write failed" {
		t.Errorf("the default failure message: %v", failed)
	}

	// No active path to point at.
	e = newEvRig(t, "", nil)
	e.startSession()
	e.Probe.CompleteFocused()
	e.Probe.SetGoalField("activePath", "")
	restore = e.Probe.RejectMutations("nope")
	e.fire("turn_end", mbNormalTurnEnd())
	restore()
	if m := e.lastMessage(); m != "Failed to archive completed goal: nope. The complete record remains at (unknown)." {
		t.Errorf("without an active path: %q", m)
	}
}

// events-beforecompact-no-account, events-compact-no-flush, events-compact-no-accounting, events-compactfail-detail,
// events-compactfail-no-persist
func TestMutantsBCompaction(t *testing.T) {
	e := newEvRig(t, "", nil)
	fc := e.Probe.FakeClock(time.Now().UnixMilli())
	e.startSession()
	_, base := mbUsage(e.goalRig)
	fc.Tick(7_000)
	e.fire("session_before_compact", map[string]any{})
	if _, s := mbUsage(e.goalRig); s-base != 7 {
		t.Errorf("progress is charged before a compaction: %v", s-base)
	}

	// A buffered turn is persisted at the compaction.
	e = newEvRig(t, "", nil)
	fc = e.Probe.FakeClock(time.Now().UnixMilli())
	e.startSession()
	_, base = mbDiskUsage(t, e.Cwd)
	e.fire("turn_start", map[string]any{})
	fc.Tick(7_000)
	e.fire("tool_execution_end", map[string]any{})
	if _, s := mbDiskUsage(t, e.Cwd); s != base {
		t.Errorf("the turn's charge is still buffered: %v", s-base)
	}
	e.fire("session_compact", map[string]any{})
	if _, s := mbDiskUsage(t, e.Cwd); s-base != 7 {
		t.Errorf("the compaction persists the buffered turn: %v", s-base)
	}

	// Accounting starts over after a compaction.
	e = newEvRig(t, "", nil)
	fc = e.Probe.FakeClock(time.Now().UnixMilli())
	e.startSession()
	_, base = mbUsage(e.goalRig)
	fc.Tick(7_000)
	e.fire("session_compact", map[string]any{})
	fc.Tick(3_000)
	e.fire("tool_execution_end", map[string]any{})
	if _, s := mbUsage(e.goalRig); s-base != 3 {
		t.Errorf("accounting restarts at the compaction: %v", s-base)
	}

	// The failure notice names the reason and the error; the goal state is persisted first.
	e = newEvRig(t, "", nil)
	e.startSession()
	e.Probe.SetGoalTokensUsed(777)
	e.fire("session_compact_failed", map[string]any{"reason": "threshold", "errorMessage": "provider said no", "aborted": false})
	if m := e.lastMessage(); m != "Goal context compaction failed (threshold): provider said no. The goal stays active; retry with /compact or keep working." {
		t.Errorf("notice %q", m)
	}
	if tokens, _ := mbDiskUsage(t, e.Cwd); tokens != 777 {
		t.Errorf("the goal is persisted when a compaction fails: %v", tokens)
	}
	e.fire("session_compact_failed", map[string]any{"reason": "manual", "aborted": false})
	if m := e.lastMessage(); m != "Goal context compaction failed (manual). The goal stays active; retry with /compact or keep working." {
		t.Errorf("notice without an error %q", m)
	}
}

func mbWriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// events-start-settings-cache, events-start-pool-cache, events-start-ledger-cache: a new session reads fresh from disk
func TestMutantsBSessionStartReadsFresh(t *testing.T) {
	// A second goal file, made before the rig starts (writing a goal clears the caches).
	other := t.TempDir()
	rec := goal.ToolsGoal(other, "Second goal", false, evAt+5000, "")
	otherFile := filepath.Join(other, ".pi", "goals", filepath.Base(rec["activePath"].(string)))
	text, err := os.ReadFile(otherFile)
	if err != nil {
		t.Fatal(err)
	}

	e := newEvRig(t, `{"disableTasks": false}`, nil)
	e.startSession()
	if !strings.Contains(strings.Join(e.ActiveTools(), ","), "set_goal_tasks") {
		t.Fatalf("tasks are enabled: %v", e.ActiveTools())
	}
	equal(t, len(e.Probe.GoalIDs()), 1, "one goal")
	before := e.callTool("get_goal", map[string]any{"include_history": true}).Content
	textNotContains(t, before, "audit_result", "no history yet")

	// Everything below changes behind the extension's back.
	mbWriteFile(t, filepath.Join(e.Cwd, ".pi", "pi-goal-x-settings.json"), `{"disableTasks": true}`)
	mbWriteFile(t, filepath.Join(e.Cwd, ".pi", "goals", filepath.Base(otherFile)), string(text))
	f, err := os.OpenFile(filepath.Join(e.Cwd, ".pi", "goals", "goal_events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"audit_result","goalId":"golden_fixture_goal","verdict":"disapproved","report":"late","at":"2026-08-03T09:30:00.000Z"}` + "\n")
	_ = f.Close()

	e.startSession()
	if strings.Contains(strings.Join(e.ActiveTools(), ","), "set_goal_tasks") {
		t.Errorf("the new session reads the settings again: %v", e.ActiveTools())
	}
	equal(t, len(e.Probe.GoalIDs()), 2, "the new session reads the goal pool again")
	after := e.callTool("get_goal", map[string]any{"include_history": true}).Content
	textContains(t, after, "09:30:00 audit_result", "the new session reads the ledger again")
}

// events-start-no-rehydrate
func TestMutantsBSessionStartRehydratesDraft(t *testing.T) {
	draft := map[string]any{"type": "custom", "customType": "pi-goal-draft", "data": map[string]any{
		"version": 1, "mode": "goal", "seed": "a topic", "startedAt": "2026-08-03T09:00:00.000Z", "auditorEnabled": true}}
	r := newGoalRig(t, rigOptions{Branch: []any{draft}})
	if r.Probe.HasActiveDraft() {
		t.Fatal("no draft before the session starts")
	}
	r.Fire("session_start", map[string]any{"reason": "start"})
	if !r.Probe.HasActiveDraft() {
		t.Error("a draft in the session branch is active again after the session starts")
	}
}

// events-start-audit-not-cleared, events-tree-no-clear, events-shutdown-audit
func TestMutantsBSessionEventsDropQueuedAudit(t *testing.T) {
	for _, event := range []string{"session_start", "session_tree", "session_shutdown"} {
		r, _ := mbAuditRig(t, "Queued audit", "", rigOptions{}, func(goal.RigAuditCall) goal.RigAuditResult {
			return goal.RigAuditResult{Approved: true, Output: "ok\n<approved/>", Model: "mock"}
		})
		r.callTool("update_goal", map[string]any{"status": "complete"})
		if n := r.Probe.RigRuntime().AuditPending; n != 2 {
			t.Fatalf("%s: queued audit messages: %d", event, n)
		}
		r.Fire(event, map[string]any{"reason": "start"})
		if n := r.Probe.RigRuntime().AuditPending; n != 0 {
			t.Errorf("%s drops the queued audit messages: %d", event, n)
		}
		if got := r.auditContents(); len(got) != 0 {
			t.Errorf("%s: nothing is delivered after it: %v", event, got)
		}
	}
}

// events-start-no-restore, events-tree-no-restore, events-shutdown-scheduler, shutdown-scheduler-detach
func TestMutantsBSchedulerAttachment(t *testing.T) {
	r := newEvRig(t, "", nil)
	if r.Probe.RigRuntime().SchedAttached {
		t.Error("not attached before a session")
	}
	r.startSession()
	if !r.Probe.RigRuntime().SchedAttached {
		t.Error("session_start restores the scheduler (the wake listener is attached)")
	}
	r.fire("session_shutdown", map[string]any{})
	if r.Probe.RigRuntime().SchedAttached {
		t.Error("session_shutdown detaches the scheduler")
	}
	r.fire("session_tree", map[string]any{})
	if !r.Probe.RigRuntime().SchedAttached {
		t.Error("session_tree restores the scheduler")
	}

	ui := true
	u := newGoalRig(t, rigOptions{HasUI: &ui})
	u.Fire("session_start", map[string]any{"reason": "start"})
	if u.Probe.RigRuntime().TerminalUnsubscribed {
		t.Fatal("the terminal input listener is registered")
	}
	before := len(u.Host.Calls())
	u.Fire("session_shutdown", map[string]any{})
	var calls []string
	for _, c := range u.Host.Calls()[before:] {
		calls = append(calls, c.Method)
	}
	joined := strings.Join(calls, ",")
	if !strings.Contains(joined, "events.off") {
		t.Errorf("shutdown unsubscribes the wake listener: %v", calls)
	}
	if !strings.Contains(joined, "ui.offTerminalInput") {
		t.Errorf("shutdown removes the terminal input listener: %v", calls)
	}
	if !u.Probe.RigRuntime().TerminalUnsubscribed {
		t.Error("the listener is forgotten")
	}
}

// events-shutdown-pending, events-shutdown-accounting, events-shutdown-continuation
func TestMutantsBShutdownCleanup(t *testing.T) {
	const settings = `{"maxAutonomousRuns": 5}`
	// The settle bookkeeping of a finished run does not survive the session.
	e := newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.declareNextRun(t)
	e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
	e.fire("session_shutdown", map[string]any{})
	e.fireIdle("agent_settled", map[string]any{})
	if n := e.checkpoints(); n != 0 {
		t.Errorf("no continuation is queued for a run that ended before shutdown: %d", n)
	}

	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fire("session_shutdown", map[string]any{})
	e.fireIdle("agent_settled", map[string]any{})
	for _, m := range e.messages() {
		if strings.Contains(m, "Retrying the goal") {
			t.Errorf("no network recovery is scheduled after shutdown: %q", m)
		}
	}

	// Progress is charged and persisted at shutdown, and the recovery state is dropped.
	e = newEvRig(t, settings, nil)
	fc := e.Probe.FakeClock(time.Now().UnixMilli())
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	_, base := mbDiskUsage(t, e.Cwd)
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_settled", map[string]any{})
	if rt := e.Probe.RigRuntime(); rt.NetworkRetryAttempt != 1 || !rt.NetworkRetryTimer {
		t.Fatalf("a recovery is scheduled: %+v", rt)
	}
	fc.Tick(2_000)
	e.fire("session_shutdown", map[string]any{})
	if _, s := mbDiskUsage(t, e.Cwd); s-base != 2 {
		t.Errorf("shutdown charges the progress: %v", s-base)
	}
	if rt := e.Probe.RigRuntime(); rt.NetworkRetryAttempt != 0 || rt.NetworkRetryTimer || rt.ContinuationPending {
		t.Errorf("shutdown clears the continuation and recovery state: %+v", rt)
	}
}

// events-start-select-many, events-start-select-ui, events-start-select-reason, events-start-select-modal, events-start-select-modal-exit,
// events-start-select-no-arm, events-start-confirm-ui
func TestMutantsBResumeSelectsGoal(t *testing.T) {
	setup := func(t *testing.T, goals int, ui bool, selectFn func(string, []string) (string, bool)) *goalRig {
		f := newToolsFixture(t, "First open goal", false, mbAt, "")
		for i := 1; i < goals; i++ {
			goal.ToolsGoal(f.cwd, "Open goal "+strconv.Itoa(i+1), false, mbAt+int64(i)*1000, "")
		}
		f.entries = nil
		return newToolsRig(t, f, rigOptions{HasUI: &ui, Settings: `{"autoSelectSingleGoal": false}`, Select: selectFn})
	}
	selects := func(r *goalRig) int { return len(r.Host.CallsTo("ui.select")) }

	var r *goalRig
	depthDuring := -1
	r = setup(t, 2, true, func(title string, options []string) (string, bool) {
		depthDuring = r.Probe.GoalModalDepth()
		return options[0], true
	})
	r.Fire("session_start", map[string]any{"reason": "resume"})
	if selects(r) != 1 {
		t.Fatalf("a resumed session with two open goals asks which to focus: %d", selects(r))
	}
	equal(t, depthDuring, 1, "the picker is a goal modal")
	equal(t, r.Probe.GoalModalDepth(), 0, "the modal is left")
	if r.Probe.FocusedGoalID() == "" {
		t.Error("the chosen goal is focused")
	}
	var reasons []string
	for _, e := range r.Entries() {
		if e.CustomType == "pi-goal-focus" {
			reasons = append(reasons, str(e.Data["reason"]))
		}
	}
	equal(t, reasons, []string{"selected"}, "the focus entry says it was selected")

	r = setup(t, 1, true, func(string, []string) (string, bool) { return "", false })
	r.Fire("session_start", map[string]any{"reason": "resume"})
	if selects(r) != 0 {
		t.Errorf("one open goal is not a choice: %d", selects(r))
	}
	r = setup(t, 2, false, func(string, []string) (string, bool) { return "", false })
	r.Fire("session_start", map[string]any{"reason": "resume"})
	if selects(r) != 0 {
		t.Errorf("without a UI nothing is asked: %d", selects(r))
	}
	r = setup(t, 2, true, func(string, []string) (string, bool) { return "", false })
	r.Fire("session_start", map[string]any{"reason": "start"})
	if selects(r) != 0 {
		t.Errorf("only a resumed session asks: %d", selects(r))
	}
}

// events-start-confirm-ui
func TestMutantsBResumeConfirmsPausedGoal(t *testing.T) {
	for _, ui := range []bool{false, true} {
		f := newToolsFixture(t, "Paused goal", false, mbAt, `{"status":"paused","autoContinue":false,"stopReason":"user"}`)
		r := newToolsRig(t, f, rigOptions{HasUI: &ui})
		r.Fire("session_start", map[string]any{"reason": "resume"})
		want := 0
		if ui {
			want = 1
		}
		if n := len(r.Host.CallsTo("ui.confirm")); n != want {
			t.Errorf("ui=%v: the paused goal asks for confirmation %d times, want %d", ui, n, want)
		}
	}
}

// events-turn-start-no-ui, events-archive-no-ui
func TestMutantsBUIUpdates(t *testing.T) {
	ui := true
	r := focusedRig(t, nil, rigOptions{HasUI: &ui})
	before := len(r.Host.CallsTo("ui.setStatus"))
	r.Fire("turn_start", map[string]any{})
	if len(r.Host.CallsTo("ui.setStatus")) == before {
		t.Error("turn_start refreshes the goal status")
	}
	r.Probe.CompleteFocused()
	r.Fire("turn_end", mbNormalTurnEnd())
	calls := r.Host.Calls()
	last := -1
	for i, c := range calls {
		if c.Method == "ui.notify" {
			last = i
		}
	}
	var after []string
	for _, c := range calls[last+1:] {
		after = append(after, c.Method)
	}
	if !strings.Contains(strings.Join(after, ","), "ui.setWidget") {
		t.Errorf("the dashboard is refreshed after the archive notice: %v", after)
	}
}

// mbPendingContinuation runs a work turn that declares the next run and settles on a busy host: the continuation is queued and waits
// for the host to go idle.
func mbPendingContinuation(t *testing.T, e *evRig) {
	t.Helper()
	e.declareNextRun(t)
	e.fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
	e.fire("agent_settled", map[string]any{})
	if !e.Probe.RigRuntime().ContinuationPending {
		t.Fatalf("a continuation is pending: %+v", e.Probe.RigRuntime())
	}
}

const mbCheckpointPrompt = `<pi_goal_continuation goal_id="golden_fixture_goal" kind="checkpoint">continue`

// mbPauseOnDisk is an external edit: the goal file says paused at a newer revision.
func mbPauseOnDisk(t *testing.T, cwd string) {
	t.Helper()
	g := goal.ToolsActiveGoal(cwd)
	g["status"], g["autoContinue"], g["stopReason"] = "paused", false, "user"
	g["revision"] = g["revision"].(float64) + 1
	goal.ToolsWriteGoal(cwd, jsonOf(g))
}

// events-bas-no-seq
func TestMutantsBBeforeAgentStartAdvancesTurn(t *testing.T) {
	r, _ := mbRig(t, "Stopped goal", "", rigOptions{})
	r.callTool("update_goal", map[string]any{"status": "paused", "reason": "waiting"})
	blocked := func() bool {
		raw := r.Fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
		return strings.Contains(string(raw), `"block":true`)
	}
	if !blocked() {
		t.Fatal("tools are blocked after the goal stopped the turn")
	}
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "next", "systemPromptOptions": map[string]any{}})
	if blocked() {
		t.Error("a new run starts a new turn: the stop marker no longer applies")
	}
}

// events-bas-draft-inverted
func TestMutantsBBeforeAgentStartKeepsDraftProfile(t *testing.T) {
	draft := map[string]any{"type": "custom", "customType": "pi-goal-draft", "data": map[string]any{
		"version": 1, "mode": "goal", "seed": "a topic", "startedAt": "2026-08-03T09:00:00.000Z", "auditorEnabled": true}}
	r := newGoalRig(t, rigOptions{Branch: []any{draft}})
	r.Fire("session_start", map[string]any{"reason": "start"})
	has := func(name string) bool {
		return strings.Contains(","+strings.Join(r.ActiveTools(), ",")+",", ","+name+",")
	}
	if !has("propose_goal_draft") {
		t.Fatalf("the drafting tools are active: %v", r.ActiveTools())
	}
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "next", "systemPromptOptions": map[string]any{}})
	if !has("propose_goal_draft") || has("create_goal") {
		t.Errorf("an active draft keeps its tool profile: %v", r.ActiveTools())
	}
}

// events-bas-no-reconcile-checkpoint, events-bas-no-reconcile, events-bas-backoff-reset, events-bas-no-clear-checkpoint,
// events-bas-checkpoint-kept, events-bas-user-checkpoint, events-bas-user-no-clear, events-bas-user-recovery-kept
func TestMutantsBBeforeAgentStart(t *testing.T) {
	const settings = `{"maxAutonomousRuns": 100}`
	// An external pause is seen before a checkpoint turn is judged.
	e := newEvRig(t, settings, nil)
	e.startSession()
	mbPauseOnDisk(t, e.Cwd)
	e.beforeAgentStart(mbCheckpointPrompt)
	if n := e.aborts(); n != 1 {
		t.Errorf("a checkpoint for a goal paused on disk aborts the turn: %d", n)
	}
	// ... and before a user turn marks the goal as running.
	e = newEvRig(t, settings, nil)
	e.startSession()
	mbPauseOnDisk(t, e.Cwd)
	e.beforeAgentStart("user typed")
	if rt := e.Probe.RigRuntime(); rt.RunningGoalID != "" {
		t.Errorf("a goal paused on disk is not running: %+v", rt)
	}

	// An actionable checkpoint keeps the recovery count but clears the queued continuation and the checkpoint marker.
	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_settled", map[string]any{})
	if rt := e.Probe.RigRuntime(); rt.NetworkRetryAttempt != 1 {
		t.Fatalf("a recovery is scheduled: %+v", rt)
	}
	e.beforeAgentStart(mbCheckpointPrompt)
	rt := e.Probe.RigRuntime()
	if rt.NetworkRetryAttempt != 1 {
		t.Errorf("a checkpoint turn keeps the recovery count: %+v", rt)
	}
	if rt.Checkpoint != "" {
		t.Errorf("the checkpoint marker is cleared once the turn is accepted: %+v", rt)
	}
	if n := e.aborts(); n != 0 {
		t.Errorf("an actionable checkpoint is not aborted: %d", n)
	}
	e = newEvRig(t, settings, nil)
	e.startSession()
	mbPendingContinuation(t, e)
	e.beforeAgentStart(mbCheckpointPrompt)
	if e.Probe.RigRuntime().ContinuationPending {
		t.Error("a checkpoint turn consumes the queued continuation")
	}

	// A user turn resets the recovery count and drops a stale checkpoint marker.
	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart(`<pi_goal_continuation goal_id="ghost-goal" kind="checkpoint">continue`)
	if rt := e.Probe.RigRuntime(); rt.Checkpoint != "ghost-goal" {
		t.Fatalf("a stale checkpoint is remembered: %+v", rt)
	}
	e.beforeAgentStart("user typed: continue")
	if rt := e.Probe.RigRuntime(); rt.Checkpoint != "" {
		t.Errorf("a user turn forgets the checkpoint: %+v", rt)
	}
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_settled", map[string]any{})
	if rt := e.Probe.RigRuntime(); rt.NetworkRetryAttempt != 1 {
		t.Fatalf("a recovery is pending: %+v", rt)
	}
	e.beforeAgentStart("user typed again")
	if rt := e.Probe.RigRuntime(); rt.NetworkRetryAttempt != 0 {
		t.Errorf("a user turn clears the recovery count: %+v", rt)
	}
	e = newEvRig(t, settings, nil)
	e.startSession()
	mbPendingContinuation(t, e)
	e.beforeAgentStart("user typed again")
	if e.Probe.RigRuntime().ContinuationPending {
		t.Error("a user turn drops the queued continuation")
	}

	// ... and the recovery that was waiting for the run to settle.
	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.beforeAgentStart("user typed: something else")
	e.fireIdle("agent_settled", map[string]any{})
	for _, m := range e.messages() {
		if strings.Contains(m, "Retrying the goal") {
			t.Errorf("a user turn cancels the pending recovery: %q", m)
		}
	}
}

// events-end-aborted-sum, events-end-no-clear, events-end-network-persist, events-end-no-persist, events-end-running-kept,
// events-end-continuation-kept, events-end-recovery-kept
func TestMutantsBAgentEnd(t *testing.T) {
	const settings = `{"maxAutonomousRuns": 100}`
	aborted := func(tokens float64) any {
		return map[string]any{"role": "assistant", "stopReason": "aborted", "usage": map[string]any{"input": tokens, "output": 0}}
	}
	e := newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	before, _ := mbUsage(e.goalRig)
	e.fireIdle("agent_end", map[string]any{"messages": []any{aborted(10), aborted(20)}})
	if got, _ := mbUsage(e.goalRig); got-before != 30 {
		t.Errorf("the tokens of every aborted message are charged: %v", got-before)
	}
	if rt := e.Probe.RigRuntime(); rt.RunningGoalID != "" {
		t.Errorf("the run is over: %+v", rt)
	}

	// A pending continuation does not survive the end of a run.
	e = newEvRig(t, settings, nil)
	e.startSession()
	mbPendingContinuation(t, e)
	e.fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
	if e.Probe.RigRuntime().ContinuationPending {
		t.Error("agent_end clears the pending continuation")
	}

	// The goal in memory is written out at the end of a run, whether it failed on the network or not.
	for _, messages := range [][]any{{errMessage(reported503)}, {map[string]any{"role": "assistant", "stopReason": "end_turn"}}} {
		e = newEvRig(t, settings, nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.Probe.SetGoalTokensUsed(777)
		e.fireIdle("agent_end", map[string]any{"messages": messages})
		if tokens, _ := mbDiskUsage(t, e.Cwd); tokens != 777 {
			t.Errorf("the goal is persisted at agent_end (%s): %v", jsonOf(messages), tokens)
		}
	}

	// Settle bookkeeping is per run.
	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.declareNextRun(t)
	e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("boom")}})
	e.fireIdle("agent_settled", map[string]any{})
	if n := e.checkpoints(); n != 0 {
		t.Errorf("a later failed run does not inherit the earlier run's continuation: %d", n)
	}

	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("boom")}})
	e.fireIdle("agent_settled", map[string]any{})
	for _, m := range e.messages() {
		if strings.Contains(m, "Retrying the goal") {
			t.Errorf("a later failed run does not inherit the earlier run's recovery: %q", m)
		}
	}
}

// events-settled-network-or, events-settled-notice-text, events-settled-notice-level, events-settled-exhausted-text,
// events-settled-exhausted-level
func TestMutantsBSettledNotices(t *testing.T) {
	const settings = `{"maxAutonomousRuns": 100}`
	e := newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_settled", map[string]any{})
	equal(t, e.Notices(), []string{"warning: Provider network error. Retrying the goal in 5s (recovery 1, unbounded)."}, "the recovery notice")

	// A goal that is no longer active at the settle is left alone.
	e = newEvRig(t, settings, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.Probe.SetGoalField("status", "paused")
	e.fireIdle("agent_settled", map[string]any{})
	equal(t, e.Notices(), []string(nil), "no notice for a paused goal")

	e = newEvRig(t, settings, map[string]string{"PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS": "1", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS": "25"})
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_settled", map[string]any{})
	time.Sleep(80 * time.Millisecond)
	e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
	e.fireIdle("agent_settled", map[string]any{})
	notices := e.Notices()
	equal(t, notices[len(notices)-1], "warning: Provider network errors persisted after all recovery attempts. The goal remains active; resume it when the provider is healthy.", "the exhausted notice")
}

// events-stale-prompt-wrong-goal
func TestMutantsBStalePromptNamesCurrentGoal(t *testing.T) {
	e := newEvRig(t, "", nil)
	e.startSession()
	e.beforeAgentStart(`<pi_goal_continuation goal_id="ghost-goal" kind="checkpoint">continue`)
	mustContain(t, e.contextTail(t), "Current goal: golden_fixture_goal (running) - Golden fixture goal objective\n")
}

// events-context-no-clear: a host without a session id keeps no retained tails
func TestMutantsBContextWithoutSessionID(t *testing.T) {
	position := func(sessionID string) (index, total int) {
		r := focusedRig(t, nil, rigOptions{SessionID: sessionID})
		user1 := map[string]any{"role": "user", "content": "one", "timestamp": 1}
		mbRawContext(r.goalRig, []any{user1})
		again := mbRawContext(r.goalRig, []any{user1, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "timestamp": 2},
			map[string]any{"role": "user", "content": "two", "timestamp": 3}})
		for i, m := range again {
			if c, _ := m["content"].(string); strings.Contains(c, "[PI GOAL ACTIVE goalId=") {
				return i, len(again)
			}
		}
		return -1, len(again)
	}
	if i, n := position("a-session"); i != 1 || n != 5 {
		t.Fatalf("an identified session keeps the tail where it sent it: %d of %d", i, n)
	}
	if i, n := position("-"); i != 3 || n != 5 {
		t.Errorf("an unidentified session sends the tail at the end again: %d of %d", i, n)
	}
}

// events-context-no-clear: with no goal state to send, the retained tails are dropped
func TestMutantsBContextDropsRetentionWithoutGoal(t *testing.T) {
	r := focusedRig(t, nil, rigOptions{})
	user1 := map[string]any{"role": "user", "content": "one", "timestamp": 1}
	convo := []any{user1, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "timestamp": 2},
		map[string]any{"role": "user", "content": "two", "timestamp": 3}}
	mbRawContext(r.goalRig, []any{user1})
	r.Probe.CompleteFocused()
	mbRawContext(r.goalRig, convo)
	r.Probe.SetGoalField("status", "active")
	again := mbRawContext(r.goalRig, convo)
	for i, m := range again {
		if c, _ := m["content"].(string); strings.Contains(c, "[PI GOAL ACTIVE goalId=") && i != 3 {
			t.Errorf("the tail is sent fresh at the end, found at %d of %d", i, len(again))
		}
	}
}

// events-compact-no-queue: a compaction queues the declared continuation
func TestMutantsBCompactionQueuesContinuation(t *testing.T) {
	e := newEvRig(t, `{"maxAutonomousRuns": 5}`, nil)
	e.startSession()
	e.beforeAgentStart("user typed: continue")
	e.declareNextRun(t)
	if e.Probe.RigRuntime().ContinuationPending {
		t.Fatal("nothing is queued before the compaction")
	}
	e.fire("session_compact", map[string]any{})
	if !e.Probe.RigRuntime().ContinuationPending {
		t.Error("the compaction queues the continuation")
	}
}

// update-flush-first
func TestMutantsBUpdateGoalFlushesTurn(t *testing.T) {
	f := fixtureWithTasks(t, "Flush goal", `[{"id":"t1","title":"T1","status":"pending"}]`, "")
	r := startedRig(t, f, rigOptions{})
	r.Fire("turn_start", map[string]any{})
	r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "done"})
	status := func() any { return taskByID(listTasks(goal.ToolsActiveGoal(f.cwd)), "t1")["status"] }
	equal(t, status(), "pending", "the turn's change is still buffered")
	r.callTool("update_goal", map[string]any{})
	equal(t, status(), "complete", "update_goal persists the buffered turn first")
}

// blocked-refresh-disk: a hand edit of the objective is not clobbered by the block
func TestMutantsBBlockedKeepsDiskObjective(t *testing.T) {
	r, f := mbRig(t, "Original objective", "", rigOptions{})
	g := goal.ToolsActiveGoal(f.cwd)
	g["objective"] = "Edited on disk"
	goal.ToolsWriteGoal(f.cwd, jsonOf(g))
	r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "stuck"})
	after := goal.ToolsActiveGoal(f.cwd)
	equal(t, after["status"], "blocked", "blocked")
	equal(t, after["objective"], "Edited on disk", "the objective edited on disk survives")
}

func mbRawContext(r *goalRig, messages []any) []map[string]any {
	raw := r.Fire("context", map[string]any{"messages": messages})
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.Messages
}

var _ = os.Getpid
var _ = filepath.Join
var _ = time.Second
