package pi_goal_x_test

// Twins of tests/e2e/goal-lifecycle-dashboard.test.ts (plan §19.9): guided creation -> clarification -> confirmation -> auto-start ->
// task focus -> dashboard states -> completion -> staged audit -> approval -> deferred archival -> reload. The real extension with a
// mock auditor; the dashboard assertions derive from the persisted goal through the shared pure model (the pipeline the widget uses).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func archivedGoalFiles(cwd string) []string {
	entries, _ := os.ReadDir(filepath.Join(cwd, ".pi", "goals", "archived"))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "goal_") {
			out = append(out, e.Name())
		}
	}
	return out
}

func lifecycleRig(t *testing.T, auditor func(context.Context, map[string]any, string, string) goal.RigAuditResult, d *dialogDriver) *goalRig {
	t.Helper()
	cwd := archivedCwd(t)
	return newGoalRig(t, rigOptions{
		Cwd: cwd, HasUI: boolPtr(true), SessionID: "lifecycle-e2e-session", Settings: "-",
		ActiveTools: []string{"read", "bash", "edit", "write"}, Select: d.selectFn, Confirm: func(string, string) bool { return true },
		Deps: goal.RigDeps{Auditor: auditor},
	})
}

func TestGoalLifecycleDashboard(t *testing.T) {
	const f = "e2e/goal-lifecycle-dashboard"

	tw(t, f, "full guided lifecycle: create → focus → tasks → audit → archive (§19.9)", func(t *testing.T) {
		d := newDialogDriver()
		var mu sync.Mutex
		var staged []string
		r := lifecycleRig(t, func(_ context.Context, _ map[string]any, _, _ string) goal.RigAuditResult {
			// §19.9 step 12: the five audit stages.
			mu.Lock()
			staged = append(staged, "Starting audit...", "Inspecting files...", "Verifying success criteria...", "Evaluating evidence...", "Producing report...")
			mu.Unlock()
			return goal.RigAuditResult{Approved: true, Output: "All requirements verified.\n<approved/>", Model: "mock/auditor"}
		}, d)
		cwd := r.Cwd
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "go", "systemPromptOptions": map[string]any{}})

		// ── Step 1: guided goal creation ──
		r.Command("goal", "Add CSV export to the reports page")

		// ── Step 2: clarification questions ──
		var qOut string
		q := goAsync(func() {
			out, failure := r.Tool("goal_questionnaire", map[string]any{"questions": []any{map[string]any{"id": "filters", "question": "Which filters apply?", "options": []any{"Active filters", "All rows"}}}})
			if failure != "" {
				t.Errorf("goal_questionnaire: %s", failure)
			}
			qOut = toolText(t, out)
		})
		d.waitOpen(t)
		d.result("Active filters", nil)
		q.wait(t)
		if !strings.Contains(qOut, "Active filters") {
			t.Errorf("answer recorded: %s", qOut)
		}

		// ── Step 3: confirm a five-task plan ──
		objective := "Add CSV export to the reports page using active filters.\nSuccess criteria: exports match the visible columns.\nVerification contract: Run npm test (0 failures)"
		flat := []any{
			map[string]any{"id": "t1", "title": "Review reports page and data source"},
			map[string]any{"id": "t2", "title": "Implement filtered CSV export"},
			map[string]any{"id": "t3", "title": "Add the download button", "verification_contract": "The button downloads a CSV using the active filters."},
			map[string]any{"id": "t3.1", "title": "Add loading state", "parent_id": "t3"},
			map[string]any{"id": "t3.2", "title": "Generate timestamped filename", "parent_id": "t3"},
			map[string]any{"id": "t3.3", "title": "Add error handling", "parent_id": "t3"},
			map[string]any{"id": "t4", "title": "Add documentation"},
			map[string]any{"id": "t5", "title": "Add and run tests"},
		}
		pending, _ := startProposal(t, r, map[string]any{"objective": objective, "sisyphus": false, "tasks": flat})
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)

		// ── Step 4: creation, focus, persistence, auto-continuation ──
		g := firstGoal(t, cwd)
		if g["status"] != "active" || g["autoContinue"] != true {
			t.Errorf("goal active and continuing: %v %v", g["status"], g["autoContinue"])
		}
		if vc, _ := g["verificationContract"].(string); !strings.Contains(vc, "npm test") {
			t.Errorf("verification contract extracted: %v", g["verificationContract"])
		}
		tasks := anySlice(g["taskList"].(map[string]any)["tasks"])
		if len(tasks) != 5 {
			t.Errorf("five top-level tasks: %d", len(tasks))
		}
		var subIDs []string
		for _, s := range anySlice(goalTaskByID(g, "t3")["subtasks"]) {
			subIDs = append(subIDs, s.(map[string]any)["id"].(string))
		}
		if strings.Join(subIDs, ",") != "t3.1,t3.2,t3.3" {
			t.Errorf("nested tasks persisted: %v", subIDs)
		}
		if r.Probe.FocusedGoalID() != g["id"] {
			t.Error("goal is focused")
		}
		if len(eventsOfType(cwd, "goal_created")) == 0 {
			t.Error("goal_created ledger event")
		}

		// ── Step 5: start the first task ──
		callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "t1", "status": "start"})
		if firstGoal(t, cwd)["currentTaskId"] != "t1" {
			t.Error("start sets the persisted current task")
		}

		// ── Step 6: complete three of five top-level tasks ──
		for _, c := range []map[string]any{
			{"task_id": "t1", "status": "complete", "evidence": "Source reviewed"},
			{"task_id": "t2", "status": "start"},
			{"task_id": "t2", "status": "complete", "evidence": "Export implemented"},
			{"task_id": "t3", "status": "start"},
			{"task_id": "t3.1", "status": "complete", "evidence": "Loading state"},
			{"task_id": "t3.2", "status": "complete", "evidence": "Filename"},
			// §9.1: a skipped top-level task counts as done toward the main bar.
			{"task_id": "t5", "status": "skipped", "reason": "Covered by t2"},
		} {
			callTaskTool(t, r, "update_goal_task", c)
		}

		// ── Step 7: compact dashboard shows header counts + bar + current + subtasks + contract ──
		g = firstGoal(t, cwd)
		compact := goal.DashboardText(cwd, g, false)
		for _, p := range []struct{ re, msg string }{
			{`Tasks · ✓3 done · 2 open`, "compact header shows done/open counts"},
			{`Current  t3 · Add the download button`, "current task shown"},
			{`· Sub 2/3 \[.*\]`, "subtask bar sits beside the task bar in the header"},
			{`Verify   Run npm test \(0 failures\)`, "verification contract shown"},
			{`File     \.pi/goals/active_goal_`, "file path shown"},
		} {
			matchText(t, compact, p.re, p.msg)
		}

		// ── Steps 8-9: expanded dashboard shows the full tree + current block ──
		expanded := goal.DashboardText(cwd, g, true)
		matchText(t, expanded, `├─ Tasks `, "tasks block")
		for _, id := range []string{"t1", "t2", "t3", "t4", "t5", "t3.1", "t3.2", "t3.3"} {
			if !strings.Contains(expanded, id) {
				t.Errorf("task tree contains %s", id)
			}
		}
		for _, p := range []string{`▸ t3  Add the download button`, `~ t5  Add and run tests`, `├─ Current task `, `Subtasks \[.*\] 2/3 · 67%`, `Contract: The button downloads a CSV`, `├─ Verification `} {
			matchText(t, expanded, p, "expanded")
		}

		// ── Step 10: complete all tasks ──
		for _, c := range []map[string]any{
			{"task_id": "t3.3", "status": "complete", "evidence": "Error handling"},
			{"task_id": "t3", "status": "complete", "evidence": "Button works"},
			{"task_id": "t4", "status": "complete", "evidence": "Docs written"},
		} {
			callTaskTool(t, r, "update_goal_task", c)
		}
		g = firstGoal(t, cwd)
		matchText(t, goal.DashboardText(cwd, g, false), `✓5 done · 0 open`, "all tasks done")

		// ── Step 11: request completion ──
		updateGoal(t, r, map[string]any{"status": "complete"})
		if len(eventsOfType(cwd, "completion_requested")) == 0 {
			t.Error("completion_requested ledger event")
		}
		if len(eventsOfType(cwd, "audit_started")) == 0 {
			t.Error("audit_started ledger event")
		}

		// ── Steps 12-13: staged audit + approval ──
		mu.Lock()
		if len(staged) != 5 || staged[4] != "Producing report..." {
			t.Errorf("all five audit stages reported: %v", staged)
		}
		mu.Unlock()
		// The structured dashboard at 72% would show workspace running (the stage derivation used by the audit widget).
		if got := strings.Join(goal.AuditorCheckStates("Evaluating evidence...", 72), ","); got != "passed,passed,passed,running,pending" {
			t.Errorf("audit checks %s", got)
		}
		if v := r.Probe.AuditResultVerdict(); v != "approved" {
			t.Errorf("approval sets the result card: %q", v)
		}

		// ── Step 14: deferred completion ──
		g = firstGoal(t, cwd)
		if g["status"] != "complete" {
			t.Errorf("goal marked complete but NOT archived yet: %v", g["status"])
		}
		if n := len(archivedGoalFiles(cwd)); n != 0 {
			t.Errorf("deferred: not archived yet: %d", n)
		}
		approved := false
		for _, e := range eventsOfType(cwd, "audit_result") {
			approved = approved || e["verdict"] == "approved"
		}
		if !approved {
			t.Error("approved audit recorded")
		}

		// ── Steps 15-16: archive creation + final archive path ──
		turnEnd(r)
		archived := archivedGoalFiles(cwd)
		if len(archived) != 1 {
			t.Fatalf("archive created at turn_end: %d", len(archived))
		}
		archivePath := ".pi/goals/archived/" + archived[0]
		hasPath := func(typ string) bool {
			for _, e := range eventsOfType(cwd, typ) {
				if e["archivePath"] == archivePath {
					return true
				}
			}
			return false
		}
		if !hasPath("goal_archived") {
			t.Error("goal_archived ledger event with the real path")
		}
		if !hasPath("goal_completed") {
			t.Error("goal_completed kept for compatibility")
		}
		found := false
		for _, n := range r.Notices() {
			found = found || (strings.Contains(n, "Goal archived.") && strings.Contains(n, archivePath))
		}
		if !found {
			t.Errorf("success message emits the archive path: %v", r.Notices())
		}
		if n := len(goal.ActiveGoalFiles(cwd)); n != 0 {
			t.Errorf("active record retired: %d", n)
		}

		// ── Step 17: reload — the goal is no longer active ──
		goal.InvalidateCaches()
		if n := len(goal.PoolIDs(cwd)); n != 0 {
			t.Errorf("no active goals after reload: %d", n)
		}
		if rec := goal.ParseGoalFileFor(filepath.Join(cwd, ".pi", "goals", "archived", archived[0])); rec["status"] != "complete" {
			t.Errorf("archived record is complete and durable: %v", rec["status"])
		}
	})

	tw(t, f, "archive failure never reports success; the complete record stays recoverable (§16.6)", func(t *testing.T) {
		d := newDialogDriver()
		r := lifecycleRig(t, func(context.Context, map[string]any, string, string) goal.RigAuditResult {
			return goal.RigAuditResult{Approved: true, Output: "ok\n<approved/>", Model: "mock/auditor"}
		}, d)
		cwd := r.Cwd
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "go", "systemPromptOptions": map[string]any{}})
		r.Command("goal-direct", "Ship the report feature.\nVerification contract: Run npm test (0 failures)")
		// Complete it through the normal deferred flow (approval).
		updateGoal(t, r, map[string]any{"status": "complete"})
		if firstGoal(t, cwd)["status"] != "complete" {
			t.Error("deferred completion before archive")
		}

		// Make the archive directory unwritable so the archive write fails.
		archiveDir := filepath.Join(cwd, ".pi", "goals", "archived")
		if err := os.Chmod(archiveDir, 0o555); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(archiveDir, 0o755) }()

		turnEnd(r)

		// Failure behavior (§16.6): never claim success, keep the record recoverable at its active path, and write a diagnostic ledger
		// event.
		if anyHas(r.Notices(), "Goal archived.") {
			t.Error("failure must never report success")
		}
		found := false
		for _, n := range r.Notices() {
			found = found || (strings.Contains(n, "Failed to archive completed goal") && strings.Contains(n, "remains at"))
		}
		if !found {
			t.Errorf("failure reports the remaining active path: %v", r.Notices())
		}
		if len(eventsOfType(cwd, "goal_archive_failed")) == 0 {
			t.Error("diagnostic goal_archive_failed ledger event")
		}
		if len(eventsOfType(cwd, "goal_archived")) != 0 {
			t.Error("no goal_archived on failure")
		}
		if g := firstGoal(t, cwd); g["status"] != "complete" {
			t.Errorf("complete record remains recoverable: %v", g["status"])
		}
	})
}
