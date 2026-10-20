package pi_goal_x_test

// Twins of tests/goal-core-tools.test.ts (create_goal / get_goal / update_goal through the whole extension).

import (
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const gctFile = "goal-core-tools"

func gctFixture(t *testing.T) toolsFixture {
	return newToolsFixture(t, "=== Goal ===\nObjective: Core tools test", false, goalAt(2026, time.August, 4, 9, 0, 0), "")
}

func approvedAudit(call goal.RigAuditCall) goal.RigAuditResult {
	return goal.RigAuditResult{Approved: true, Output: "All good\n<approved/>", Model: "mock"}
}

func TestGoalCoreTools(t *testing.T) {
	tw(t, gctFile, "exactly three goal tools are advertised when tasks are disabled", func(t *testing.T) {
		f := newToolsFixture(t, "Tasks disabled", false, goalAt(2026, time.August, 4, 10, 0, 0), "")
		r := newToolsRig(t, f, rigOptions{Settings: `{"disableTasks":true}`})
		r.startToolsSession()
		active := strings.Join(r.ActiveTools(), ",")
		for _, present := range []string{"create_goal", "get_goal", "update_goal"} {
			contains(t, ","+active+",", ","+present+",", present+" must be advertised")
		}
		for _, absent := range []string{"propose_task_list", "complete_task", "skip_task", "complete_goal", "pause_goal", "abort_goal", "propose_goal_draft", "propose_goal_tweak", "step_complete"} {
			notContains(t, ","+active+",", ","+absent+",", absent+" must NOT be advertised")
		}
	})

	tw(t, gctFile, "create_goal creates, focuses, and reports other open goals", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		before := len(goal.ToolsActiveGoalFiles(f.cwd))
		out := r.callTool("create_goal", map[string]any{"objective": "=== Goal ===\nObjective: Second goal from create_goal", "mode": "regular"})
		contains(t, out.Content, "Second goal from create_goal", "result should confirm creation")
		contains(t, out.Content, "1 other open goal", "result should report other open goals")
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), before+1, "a new active goal file must land on disk")
		if !hasEvent(f.cwd, "goal_created") {
			t.Error("goal_created ledger event")
		}
	})

	tw(t, gctFile, "create_goal accepts token_budget and sisyphus mode", func(t *testing.T) {
		f := newEmptyToolsFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		out := r.callTool("create_goal", map[string]any{"objective": "=== Goal ===\nObjective: Budgeted sisyphus", "mode": "sisyphus", "token_budget": 5000})
		active := goal.ToolsActiveGoalFiles(f.cwd)
		equal(t, len(active), 1, "one active goal")
		parsed := goal.ToolsActiveGoal(f.cwd)
		if parsed == nil {
			t.Fatal("goal must parse")
		}
		equal(t, parsed["tokenBudget"], 5000, "token_budget must be persisted")
		contains(t, jsonOf(out.Content), "Budget: 5000 tokens", "budget in the report")
		equal(t, parsed["sisyphus"], true, "sisyphus mode must be persisted")
	})

	tw(t, gctFile, "create_goal accepts long objectives by default (no hard 4000 limit)", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		out := r.callTool("create_goal", map[string]any{"objective": strings.Repeat("x", 5000)})
		contains(t, out.Content, strings.Repeat("x", 40), "long objective is accepted when no max objective length is configured")
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 2, "the long-objective goal is created alongside the fixture goal")
	})

	tw(t, gctFile, "create_goal enforces the configured max objective length setting", func(t *testing.T) {
		f := gctFixture(t)
		writeSettings(t, f.cwd, `{"objectiveMaxChars":100}`)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		rejected := r.callTool("create_goal", map[string]any{"objective": strings.Repeat("x", 101)})
		contains(t, rejected.Content, "exceeds 100 characters", "must reject objectives over the configured limit")
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 1, "no goal is created when the objective is over the limit (fixture goal only)")
		accepted := r.callTool("create_goal", map[string]any{"objective": strings.Repeat("x", 100)})
		if accepted.Content == "" {
			t.Error("objective exactly at the limit is accepted")
		}
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 2, "at-limit objective creates the goal")
	})

	tw(t, gctFile, "get_goal returns the complete stable snapshot", func(t *testing.T) {
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		f := newToolsFixture(t, "Snapshot goal", true, goalAt(2026, time.August, 4, 11, 0, 0),
			`{"tokenBudget":1000,"usage":{"tokensUsed":250,"activeSeconds":120},"verificationContract":"Run npm test (0 failures).",`+
				`"taskList":{"tasks":[{"id":"t1","title":"Task one","status":"pending"}],"blockCompletion":false,"proposedAt":"`+now+`"}}`)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		// PR E: the concise default omits verbose-only sections; pass verbose to pin the complete stable snapshot.
		text := r.callTool("get_goal", map[string]any{"verbose": true}).Content
		contains(t, text, "Snapshot goal", "objective present")
		contains(t, text, "Status:", "status present")
		contains(t, text, "running", "status present")
		contains(t, text, "Mode: sisyphus", "mode present")
		contains(t, text, "Budget:", "budget present")
		contains(t, text, "750 remaining", "remaining budget present")
		contains(t, text, "Tasks:", "task summary present")
		contains(t, text, "Verification contract:", "contract present")
		contains(t, text, "Path: "+f.goal["activePath"].(string), "path present")
	})

	tw(t, gctFile, "get_goal default is concise; verbose retains full diagnostics", func(t *testing.T) {
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		f := newToolsFixture(t, "Concise snapshot goal", false, goalAt(2026, time.August, 4, 11, 0, 0),
			`{"verificationContract":"Run npm test (0 failures).","taskList":{"tasks":[`+
				`{"id":"t1","title":"Done task","status":"complete"},`+
				`{"id":"t2","title":"Current task","status":"pending","verificationContract":"prove it"},`+
				`{"id":"t3","title":"Next up","status":"pending"}],"blockCompletion":false,"proposedAt":"`+now+`"},"currentTaskId":"t2"}`)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		text := r.callTool("get_goal", map[string]any{}).Content
		contains(t, text, "Objective: Concise snapshot goal", "full objective kept (explicit state-read tool)")
		contains(t, text, "Current task: t2 — Current task — contract: prove it", "current task with contract")
		contains(t, text, "Next pending: t3 — Next up", "next pending (distinct from current)")
		notContains(t, text, "Lifecycle:", "lifecycle prose omitted from the concise default")
		notContains(t, text, "Path:", "paths are verbose-only")
		vtext := r.callTool("get_goal", map[string]any{"verbose": true}).Content
		contains(t, vtext, "Lifecycle:", "verbose keeps lifecycle guidance")
		contains(t, vtext, "Path:", "verbose keeps paths")
	})

	tw(t, gctFile, "update_goal(complete) runs the auditor without a verification-summary parameter", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		var call *goal.RigAuditCall
		r.Probe.SetCompletionAuditor(func(c goal.RigAuditCall) goal.RigAuditResult { call = &c; return approvedAudit(c) })
		r.startToolsSession()
		goal.ToolsAppendLedger(f.cwd, `{"type":"audit_result","goalId":"`+f.goal["id"].(string)+`","verdict":"disapproved","report":"Missing boundary test `+strings.Repeat("x", 1000)+`","at":"`+time.Now().UTC().Format("2006-01-02T15:04:05.000Z")+`"}`)
		r.callTool("update_goal", map[string]any{"status": "complete"})
		if call == nil {
			t.Fatal("auditor must run")
		}
		equal(t, call.CompletionSummary, "", "no completion claim required")
		if !regexpMatch(`Previous rejection.*Missing boundary test`, call.WarmContext) {
			t.Errorf("warm context %q", call.WarmContext)
		}
		if len(call.WarmContext) >= 1200 {
			t.Errorf("previous findings are bounded, got %d", len(call.WarmContext))
		}
		// Deferred archival happens at turn_end.
		r.turnEnd()
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 0, "approved completion archives the goal")
		approved := false
		for _, e := range eventsOfType(f.cwd, "audit_result") {
			approved = approved || e["verdict"] == "approved"
		}
		if !approved {
			t.Error("audit_result ledger event")
		}
		if !hasEvent(f.cwd, "goal_completed") {
			t.Error("goal_completed ledger event")
		}
		// §15.4: approval shows the result card during deferred completion.
		equal(t, r.Probe.AuditResultVerdict(), "approved", "approval sets the result card verdict")
	})

	tw(t, gctFile, "update_goal(complete) with a rejection keeps the goal open with feedback", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.Probe.SetCompletionAuditor(func(goal.RigAuditCall) goal.RigAuditResult {
			return goal.RigAuditResult{Disapproved: true, Output: "Missing requirement\n<disapproved/>", Model: "mock"}
		})
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "complete"})
		contains(t, out.Content, "Goal audit rejected", "rejection feedback returned")
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 1, "goal stays open")
		rejected := false
		for _, e := range eventsOfType(f.cwd, "audit_result") {
			rejected = rejected || e["verdict"] == "disapproved"
		}
		if !rejected {
			t.Error("disapproved audit recorded")
		}
		// §15.4: the result card state is set for the widget, then clears.
		equal(t, r.Probe.AuditResultVerdict(), "disapproved", "rejection sets the result card verdict")
		r.Probe.ClearAuditResult()
		if r.Probe.HasAuditResult() {
			t.Error("clearAuditResult restores the normal dashboard state")
		}
	})

	tw(t, gctFile, "update_goal(blocked) records a distinct agent-blocked state from active", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "test blocker"})
		if !out.terminated() {
			t.Error("blocked terminates the turn")
		}
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 1, "goal remains in the active dir")
		parsed := goal.ToolsActiveGoal(f.cwd)
		if parsed == nil {
			t.Fatal("goal must parse")
		}
		equal(t, parsed["status"], "blocked", "status must be blocked")
		equal(t, parsed["stopReason"], "agent", "stopReason agent")
		blocked := eventsOfType(f.cwd, "goal_blocked")
		if len(blocked) == 0 {
			t.Fatal("goal_blocked ledger event")
		}
		equal(t, blocked[0]["source"], "agent", "source")
		if s, _ := blocked[0]["reason"].(string); s == "" {
			t.Error("reason must be a nonempty string")
		}
	})

	tw(t, gctFile, "update_goal(blocked) is rejected from a non-active goal", func(t *testing.T) {
		f := newToolsFixture(t, "=== Goal ===\nObjective: Core tools test", false, goalAt(2026, time.August, 4, 9, 0, 0),
			`{"status":"paused","autoContinue":false,"stopReason":"agent","pauseReason":"waiting on user"}`)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "test blocker"})
		contains(t, out.Content, "applies only to an active goal", "blocked must be rejected from paused")
		equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "paused", "goal unchanged")
		equal(t, len(eventsOfType(f.cwd, "goal_blocked")), 0, "no goal_blocked event")
	})

	tw(t, gctFile, "update_goal(blocked) surfaces an apply failure instead of claiming success (#22)", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		// Simulate a failed mutation (lock contention / revision conflict).
		restore := r.Probe.RejectMutations("simulated conflict: goal modified by another process")
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "test blocker"})
		restore()
		contains(t, out.Content, "simulated conflict", "must surface the mutation message")
		contains(t, out.Content, "NOT marked blocked", "must not claim the goal is blocked")
		if out.terminated() {
			t.Error("must not terminate the turn so the agent can retry")
		}
		// The goal on disk is untouched: still active, no blocked event.
		equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "active", "goal must remain active")
		equal(t, len(eventsOfType(f.cwd, "goal_blocked")), 0, "no goal_blocked event")
	})

	tw(t, gctFile, "update_goal(paused) surfaces an apply failure instead of claiming success (#22)", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		restore := r.Probe.RejectMutations("simulated lock contention")
		out := r.callTool("update_goal", map[string]any{"status": "paused", "reason": "waiting on user input"})
		restore()
		contains(t, out.Content, "simulated lock contention", "must surface the mutation message")
		contains(t, out.Content, "NOT paused", "must not claim the goal is paused")
		if out.terminated() {
			t.Error("must not terminate the turn so the agent can retry")
		}
		equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "active", "goal must remain active")
		equal(t, len(eventsOfType(f.cwd, "goal_paused")), 0, "no goal_paused event")
	})

	tw(t, gctFile, "legacy paused record with autoContinue:true stays paused after session restore", func(t *testing.T) {
		f := newEmptyToolsFixture(t)
		legacy := goal.ToolsNormalize(`{"id":"legacy-paused","objective":"=== Goal ===\nObjective: Legacy paused goal","status":"paused","autoContinue":true,` +
			`"createdAt":"2026-08-03T09:00:00.000Z","updatedAt":"2026-08-03T09:00:00.000Z"}`)
		written := goal.ToolsWriteGoal(f.cwd, jsonOf(legacy))
		f.entries = []any{goal.ToolsStateEntry(written), goal.ToolsFocusEntry("legacy-paused")}
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		text := r.callTool("get_goal", map[string]any{}).Content
		contains(t, text, "paused", "restored legacy goal must be paused")
		notContains(t, text, "running", "paused goal must not read as running")
		// The on-disk file must also still say paused after the restore reads.
		parsed := goal.ToolsParseGoalFile(f.cwd + "/" + written["activePath"].(string))
		equal(t, parsed["status"], "paused", "disk record must stay paused after restore")
		equal(t, parsed["autoContinue"], true, "autoContinue flag survives as data")
	})

	tw(t, gctFile, "update_goal(complete) with auditor disabled in settings skips audit and records audit_skipped", func(t *testing.T) {
		f := gctFixture(t)
		writeSettings(t, f.cwd, `{"disabled":true}`)
		r := newToolsRig(t, f, rigOptions{})
		auditorCalled := 0
		r.Probe.SetCompletionAuditor(func(goal.RigAuditCall) goal.RigAuditResult { auditorCalled++; return goal.RigAuditResult{} })
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "complete"})
		if !out.terminated() {
			t.Error("disabled-auditor completion terminates the turn")
		}
		contains(t, out.Content, "Goal audit skipped", "report must state audit skipped")
		contains(t, out.Content, "auditor disabled in settings", "report must carry the settings reason")
		equal(t, auditorCalled, 0, "auditor must not run when globally disabled")
		// Deferred archival at turn_end.
		r.turnEnd()
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 0, "disabled-auditor completion archives the goal")
		var order []string
		for _, e := range goal.ToolsLedgerEvents(f.cwd) {
			order = append(order, e["type"].(string))
		}
		idx := func(typ string) int {
			for i, o := range order {
				if o == typ {
					return i
				}
			}
			return -1
		}
		if idx("completion_requested") < 0 {
			t.Error("completion_requested first")
		}
		skipped := eventsOfType(f.cwd, "audit_skipped")
		if len(skipped) == 0 {
			t.Fatal("audit_skipped recorded")
		}
		equal(t, skipped[0]["reason"], "disabled", "reason")
		if idx("goal_completed") < 0 {
			t.Error("goal_completed recorded")
		}
		if !(idx("completion_requested") < idx("audit_skipped")) {
			t.Error("semantic order: completion_requested before audit_skipped")
		}
		if !(idx("audit_skipped") < idx("goal_completed")) {
			t.Error("semantic order: audit_skipped before deferred completion")
		}
	})

	tw(t, gctFile, "update_goal(complete) honors a legacy persisted skipAuditor:true record", func(t *testing.T) {
		f := gctFixture(t)
		// Simulate the legacy per-goal bypass record on disk.
		parsed := goal.ToolsActiveGoal(f.cwd)
		parsed["skipAuditor"] = true
		goal.ToolsWriteGoal(f.cwd, jsonOf(parsed))
		r := newToolsRig(t, f, rigOptions{})
		auditorCalled := 0
		r.Probe.SetCompletionAuditor(func(goal.RigAuditCall) goal.RigAuditResult { auditorCalled++; return goal.RigAuditResult{} })
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "complete"})
		contains(t, out.Content, "per-goal auditor disabled", "legacy skipAuditor honored with its reason")
		equal(t, auditorCalled, 0, "auditor must not run for legacy per-goal skip")
		skipped := eventsOfType(f.cwd, "audit_skipped")
		if len(skipped) == 0 {
			t.Fatal("audit_skipped recorded for legacy skip")
		}
		equal(t, skipped[0]["reason"], "disabled", "reason")
	})

	tw(t, gctFile, "aborted audit: Escape complete_without_audit commits with audit_skipped(user_aborted) without terminating", func(t *testing.T) {
		f := gctFixture(t)
		escapeDialogShown := false
		r := newToolsRig(t, f, rigOptions{HasUI: boolPtr(true), Custom: func(map[string]any) map[string]any {
			escapeDialogShown = true
			return map[string]any{"ok": true, "result": "complete_without_audit"}
		}})
		r.Probe.SetCompletionAuditor(func(goal.RigAuditCall) goal.RigAuditResult {
			return goal.RigAuditResult{Output: "Auditor aborted.", Err: "Auditor aborted."}
		})
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "complete"})
		if !escapeDialogShown {
			t.Error("Escape dialog must be shown after auditor abort")
		}
		if out.terminated() {
			t.Error("Esc-bypass completion must NOT terminate the turn (agent continues to summarize)")
		}
		contains(t, out.Content, "Goal audit skipped", "report must state audit skipped")
		contains(t, out.Content, "Provide a final summary", "Esc path keeps the final-summary guidance")
		skipped := eventsOfType(f.cwd, "audit_skipped")
		if len(skipped) == 0 {
			t.Fatal("audit_skipped recorded for user abort")
		}
		equal(t, skipped[0]["reason"], "user_aborted", "reason")
	})

	tw(t, gctFile, "focus changed while audit runs cancels completion without modifying the goal", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.Probe.SetCompletionAuditor(func(goal.RigAuditCall) goal.RigAuditResult {
			// The user changes focus during the independent audit, before its result.
			r.Command("goal-unfocus", "")
			return goal.RigAuditResult{Approved: true, Output: "All good\n<approved/>"}
		})
		r.startToolsSession()
		out := r.callTool("update_goal", map[string]any{"status": "complete"})
		contains(t, out.Content, "no longer focused", "completion must be cancelled")
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 1, "goal must remain open and unmodified")
		equal(t, hasEvent(f.cwd, "audit_result"), false, "no audit result when focus changed during audit")
	})

	tw(t, gctFile, "create_goal rejects fractional, zero, and unsafe token_budget values", func(t *testing.T) {
		f := gctFixture(t)
		r := newToolsRig(t, f, rigOptions{})
		r.startToolsSession()
		for _, bad := range []float64{1.5, 0, -10, 9007199254740992} {
			out := r.callTool("create_goal", map[string]any{"objective": "Budget rejection test", "token_budget": bad})
			contains(t, out.Content, "token_budget", "must reject token_budget")
		}
		equal(t, len(goal.ToolsActiveGoalFiles(f.cwd)), 1, "no NEW goal may be created from invalid budget input (fixture goal remains)")
	})
}
