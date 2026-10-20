package pi_goal_x_test

// Twins of tests/goal-tweak-status-persistence.test.ts: a completed task's status/evidence/completedAt must survive a task-list tweak
// through the full draft -> confirm -> apply -> disk -> reload flow; a tweak without a task list retains the current list; nested
// subtask completion survives; currentTaskId survives only while its task stays pending; and the budget-only tweaks.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const tweakSession = "tweak-status-session"

func tweakRig(t *testing.T) (*goalRig, *dialogDriver) {
	t.Helper()
	return draftRig(t, draftOpts{hasUI: true, session: tweakSession})
}

func countTaskTree(tasks []any) int {
	n := 0
	for _, x := range tasks {
		n++
		n += countTaskTree(anySlice(x.(map[string]any)["subtasks"]))
	}
	return n
}

// createGoalWithTasks is createGoalWithTasks(h, objective, tasks): /goal, then the confirmed proposal.
func createGoalWithTasks(t *testing.T, r *goalRig, d *dialogDriver, objective string, tasks []any) {
	t.Helper()
	r.Command("goal", objective)
	pending, _ := startProposal(t, r, proposalParams(objective+"\nSuccess criteria: tests pass.", map[string]any{"tasks": tasks}))
	d.waitOpen(t)
	d.result(confirmAnswer, nil)
	pending.wait(t)
	g := firstGoal(t, r.Cwd)
	tl, _ := g["taskList"].(map[string]any)
	if tl == nil || countTaskTree(anySlice(tl["tasks"])) != len(tasks) {
		t.Fatalf("goal created with the proposed task tree: %v", g["taskList"])
	}
}

func callTaskTool(t *testing.T, r *goalRig, name string, params map[string]any) string {
	t.Helper()
	if !r.Host.tools[name] {
		t.Fatalf("%s must be registered", name)
	}
	out, failure := r.Tool(name, params)
	if failure != "" {
		t.Fatalf("%s: %s", name, failure)
	}
	return string(out)
}

func goalTaskByID(g map[string]any, id string) map[string]any {
	var find func(tasks []any) map[string]any
	find = func(tasks []any) map[string]any {
		for _, x := range tasks {
			m := x.(map[string]any)
			if m["id"] == id {
				return m
			}
			if f := find(anySlice(m["subtasks"])); f != nil {
				return f
			}
		}
		return nil
	}
	tl, _ := g["taskList"].(map[string]any)
	return find(anySlice(tl["tasks"]))
}

func TestGoalTweakStatusPersistence(t *testing.T) {
	const f = "goal-tweak-status-persistence"

	tw(t, f, "e2e: completed task status, evidence, and completedAt survive a task-list tweak through disk reload", func(t *testing.T) {
		r, d := tweakRig(t)
		startDraftSession(r)
		createGoalWithTasks(t, r, d, "Initial objective", taskParams([3]string{"a", "Task A", ""}, [3]string{"b", "Task B", ""}))

		// Complete task a via the real flow (with evidence), then start b.
		complete := callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "a", "status": "complete", "evidence": "verified-e2e"})
		if !strings.Contains(complete, "a") {
			t.Error("complete result mentions the task")
		}
		callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "b", "status": "start"})
		if firstGoal(t, r.Cwd)["currentTaskId"] != "b" {
			t.Error("currentTaskId set before the tweak")
		}

		// Tweak: same ids + a new task c. The merge must keep a complete.
		r.Command("goal-tweak", "Revise the plan")
		pending, _ := startProposal(t, r, proposalParams("Revised objective", map[string]any{"tasks": taskParams(
			[3]string{"a", "Task A (retitled)", ""}, [3]string{"b", "Task B", ""}, [3]string{"c", "Task C", ""})}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)

		// Reload the persisted goal from disk.
		g := firstGoal(t, r.Cwd)
		if !strings.Contains(g["objective"].(string), "Revised objective") {
			t.Error("objective updated by the tweak")
		}
		a := goalTaskByID(g, "a")
		if a["status"] != "complete" {
			t.Errorf("completed status survives the tweak: %v", a["status"])
		}
		if a["evidence"] != "verified-e2e" {
			t.Errorf("evidence survives the tweak: %v", a["evidence"])
		}
		if a["completedAt"] == nil || a["completedAt"] == "" {
			t.Error("completedAt timestamp survives the tweak")
		}
		if a["title"] != "Task A (retitled)" {
			t.Errorf("structural title comes from the incoming proposal: %v", a["title"])
		}
		if goalTaskByID(g, "c")["status"] != "pending" {
			t.Error("new id starts pending")
		}
		if goalTaskByID(g, "b")["status"] != "pending" {
			t.Error("pending task stays pending")
		}
		if g["currentTaskId"] != "b" {
			t.Errorf("currentTaskId survives while its task is still pending: %v", g["currentTaskId"])
		}
		if len(eventsOfType(r.Cwd, "task_list_set")) == 0 {
			t.Error("task_list_set ledger event on the tweak")
		}
	})

	tw(t, f, "e2e: a tweak without a task list retains the current list unchanged (statuses included)", func(t *testing.T) {
		r, d := tweakRig(t)
		startDraftSession(r)
		createGoalWithTasks(t, r, d, "Initial objective", taskParams([3]string{"a", "Task A", ""}, [3]string{"b", "Task B", ""}))
		callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "a", "status": "complete", "evidence": "kept"})
		callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "b", "status": "skipped", "reason": "user direction"})

		// Tweak WITHOUT tasks in the proposal: the current list must be retained unchanged.
		r.Command("goal-tweak", "Tighten the wording")
		pending, _ := startProposal(t, r, proposalParams("Tightened objective wording", nil))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)

		g := firstGoal(t, r.Cwd)
		if !strings.Contains(g["objective"].(string), "Tightened") {
			t.Error("objective updated")
		}
		a, b := goalTaskByID(g, "a"), goalTaskByID(g, "b")
		if a["status"] != "complete" || a["evidence"] != "kept" || a["completedAt"] == nil {
			t.Errorf("completed task retained: %v", a)
		}
		if b["status"] != "skipped" || b["skipReason"] != "user direction" || b["skippedAt"] == nil {
			t.Errorf("skipped task retained: %v", b)
		}
		if n := len(anySlice(g["taskList"].(map[string]any)["tasks"])); n != 2 {
			t.Errorf("no tasks added or removed: %d", n)
		}
	})

	tw(t, f, "e2e: subtask completion status survives a task-list tweak", func(t *testing.T) {
		r, d := tweakRig(t)
		startDraftSession(r)
		createGoalWithTasks(t, r, d, "Initial objective", taskParams([3]string{"p", "Parent", ""}, [3]string{"p1", "Child one", "p"}))
		callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "p1", "status": "complete", "evidence": "child-done"})

		// Tweak proposing the same parent/subtask structure.
		r.Command("goal-tweak", "Revise")
		pending, _ := startProposal(t, r, proposalParams("Revised objective", map[string]any{"tasks": taskParams(
			[3]string{"p", "Parent", ""}, [3]string{"p1", "Child one (renamed)", "p"})}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)

		p1 := goalTaskByID(firstGoal(t, r.Cwd), "p1")
		if p1["status"] != "complete" {
			t.Errorf("subtask completion survives the tweak: %v", p1["status"])
		}
		if p1["evidence"] != "child-done" {
			t.Errorf("subtask evidence survives: %v", p1["evidence"])
		}
		if p1["completedAt"] == nil {
			t.Error("subtask completedAt survives")
		}
		if p1["title"] != "Child one (renamed)" {
			t.Errorf("structural subtask title comes from the proposal: %v", p1["title"])
		}
	})

	tw(t, f, "e2e: currentTaskId survives while its task stays pending and clears when removed", func(t *testing.T) {
		r, d := tweakRig(t)
		startDraftSession(r)
		createGoalWithTasks(t, r, d, "Initial objective", taskParams([3]string{"x", "Task X", ""}, [3]string{"y", "Task Y", ""}))
		callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "x", "status": "start"})
		if firstGoal(t, r.Cwd)["currentTaskId"] != "x" {
			t.Fatal("currentTaskId x")
		}

		// Tweak keeping x: focus survives.
		r.Command("goal-tweak", "Keep the plan")
		pending, _ := startProposal(t, r, proposalParams("Kept objective", map[string]any{"tasks": taskParams([3]string{"x", "Task X", ""}, [3]string{"y", "Task Y", ""})}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		if firstGoal(t, r.Cwd)["currentTaskId"] != "x" {
			t.Error("currentTaskId survives when its task stays pending")
		}

		// Tweak removing x: focus clears.
		r.Command("goal-tweak", "Drop task X")
		pending2, _ := startProposal(t, r, proposalParams("Dropped objective", map[string]any{"tasks": taskParams([3]string{"y", "Task Y", ""})}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending2.wait(t)
		g := firstGoal(t, r.Cwd)
		if _, has := g["currentTaskId"]; has {
			t.Errorf("currentTaskId clears when its task is removed by the tweak: %v", g["currentTaskId"])
		}
		if goalTaskByID(g, "x") != nil {
			t.Error("removed task dropped")
		}
	})

	tw(t, f, "unconfirmed budget proposal (${answer}) preserves disk state", func(t *testing.T) {
		for _, answer := range []string{"Cancel", "Continue chatting"} {
			t.Run(answer, func(t *testing.T) {
				r, d := tweakRig(t)
				createGoalWithTasks(t, r, d, "Objective", taskParams([3]string{"one", "One", ""}))
				r.Command("goal-tweak", "Set budget to 100")
				before := firstGoal(t, r.Cwd)
				pending, _ := startProposal(t, r, proposalParams(before["objective"].(string), map[string]any{"token_budget": 100.0}))
				d.waitOpen(t)
				if answer == "Cancel" {
					d.result("", nil) // the dialog is dismissed: cancelled
				} else {
					d.result(continueAnswer, nil)
				}
				pending.wait(t)
				if after := firstGoal(t, r.Cwd); !reflect.DeepEqual(after, before) {
					t.Errorf("disk state changed:\n%v\n%v", before, after)
				}
			})
		}
	})

	tw(t, f, "budget-only confirmation displays current and proposed limits", func(t *testing.T) {
		g := goal.NewGoalFor("Keep objective", true, false, 0)
		g["tokenBudget"] = 10.0
		text := goal.ProposalText("tweak", "Remove the budget", g["objective"].(string), true, nil, g, true, nil)
		matchText(t, text, `Current Budget: 10 tokens`, "current")
		matchText(t, text, `Proposed Budget: none`, "proposed")
	})

	tw(t, f, "guided creation reports and persists budget ${budget}", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			budget any // nil key absent: "undefined"; json null: "null"
			set    bool
		}{{"undefined", nil, false}, {"null", nil, true}, {"1", 1.0, true}, {"50000", 50000.0, true}} {
			t.Run(c.name, func(t *testing.T) {
				r, d := tweakRig(t)
				r.Command("goal", "Keep objective")
				extra := map[string]any{}
				if c.set {
					extra["token_budget"] = c.budget
				}
				pending, out := startProposal(t, r, proposalParams("Keep objective", extra))
				d.waitOpen(t)
				d.result(confirmAnswer, nil)
				pending.wait(t)
				g := firstGoal(t, r.Cwd)
				if c.budget == nil {
					if _, has := g["tokenBudget"]; has {
						t.Errorf("tokenBudget persisted: %v", g["tokenBudget"])
					}
				} else if g["tokenBudget"] != c.budget {
					t.Errorf("tokenBudget %v want %v", g["tokenBudget"], c.budget)
				}
				want := "Budget: none"
				if c.budget != nil {
					want = fmt.Sprintf("Budget: %v tokens", c.budget)
				}
				if !strings.Contains(toolText(t, *out), want) {
					t.Errorf("report lacks %q: %s", want, toolText(t, *out))
				}
			})
		}
	})

	tw(t, f, "budget-only tweak ${budget} preserves progress and recovers only with remaining budget", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			budget any
			set    bool
		}{{"undefined", nil, false}, {"null", nil, true}, {"5", 5.0, true}, {"50000", 50000.0, true}} {
			t.Run(c.name, func(t *testing.T) {
				r, d := tweakRig(t)
				createGoalWithTasks(t, r, d, "Keep objective", taskParams([3]string{"done", "Finished work", ""}))
				callTaskTool(t, r, "update_goal_task", map[string]any{"task_id": "done", "status": "complete", "evidence": "verified"})
				original := firstGoal(t, r.Cwd)
				original["tokenBudget"] = 10.0
				original["usage"].(map[string]any)["tokensUsed"] = 20.0
				original["status"] = "budget_limited"
				sch := goal.NewSchedulerFor(tweakSession)
				sch["used"] = 3.0
				original["scheduler"] = sch
				goal.WriteGoalFor(r.Cwd, original)
				r.Probe.ReconcileFromDisk()
				r.Probe.ArmPostBudgetReminder()
				r.Command("goal-tweak", "Change only the budget")
				extra := map[string]any{}
				if c.set {
					extra["token_budget"] = c.budget
				}
				pending, out := startProposal(t, r, proposalParams(original["objective"].(string), extra))
				d.waitOpen(t)
				d.result(confirmAnswer, nil)
				pending.wait(t)
				saved := firstGoal(t, r.Cwd)
				if saved["id"] != original["id"] || saved["objective"] != original["objective"] {
					t.Error("same goal and objective")
				}
				if !reflect.DeepEqual(saved["taskList"], original["taskList"]) {
					t.Errorf("taskList changed:\n%v\n%v", original["taskList"], saved["taskList"])
				}
				if saved["skipAuditor"] != original["skipAuditor"] {
					t.Error("skipAuditor changed")
				}
				wantBudget := c.budget
				if !c.set {
					wantBudget = 10.0
				}
				if wantBudget == nil {
					if _, has := saved["tokenBudget"]; has {
						t.Errorf("tokenBudget %v", saved["tokenBudget"])
					}
				} else if saved["tokenBudget"] != wantBudget {
					t.Errorf("tokenBudget %v want %v", saved["tokenBudget"], wantBudget)
				}
				if saved["usage"].(map[string]any)["tokensUsed"] != 20.0 || saved["scheduler"].(map[string]any)["used"] != 3.0 {
					t.Errorf("progress preserved: %v %v", saved["usage"], saved["scheduler"])
				}
				exhausted := !c.set || c.budget == 5.0
				wantStatus := "active"
				if exhausted {
					wantStatus = "budget_limited"
				}
				if saved["status"] != wantStatus {
					t.Errorf("status %v want %v", saved["status"], wantStatus)
				}
				if got := r.Probe.ConsumePostBudgetReminder(); got != exhausted {
					t.Errorf("post-budget reminder %v want %v", got, exhausted)
				}
				if exhausted {
					matchText(t, toolText(t, *out), `remains budget-limited`, "result")
				} else {
					matchText(t, toolText(t, *out), `Budget: (none|50000 tokens)`, "result")
				}
				wantEvents := 1
				if !c.set {
					wantEvents = 0
				}
				if n := len(eventsOfType(r.Cwd, "goal_budget_changed")); n != wantEvents {
					t.Errorf("goal_budget_changed events %d want %d", n, wantEvents)
				}
			})
		}
	})

	tw(t, f, "budget recovery respects ${barrier} scheduling barrier", func(t *testing.T) {
		for _, barrier := range []string{"foreign", "interrupted", "claimed", "allowance"} {
			t.Run(barrier, func(t *testing.T) {
				r, d := tweakRig(t)
				createGoalWithTasks(t, r, d, "Objective", taskParams([3]string{"one", "One", ""}))
				g := firstGoal(t, r.Cwd)
				g["tokenBudget"] = 1.0
				g["usage"].(map[string]any)["tokensUsed"] = 2.0
				g["status"] = "budget_limited"
				owner := tweakSession
				if barrier == "foreign" {
					owner = "other"
				}
				sch := goal.NewSchedulerFor(owner)
				sch["used"] = 3.0
				if barrier == "interrupted" {
					sch["phase"] = "interrupted"
				}
				if barrier == "claimed" {
					sch["phase"] = "claimed"
					sch["dispatch"] = map[string]any{"id": "old", "kind": "ready", "claimedAt": float64(1_700_000_000_000)}
				}
				if barrier == "allowance" {
					goal.SaveSettings(r.Cwd, map[string]any{"maxAutonomousRuns": 3.0})
				}
				g["scheduler"] = sch
				goal.WriteGoalFor(r.Cwd, g)
				r.Probe.ReconcileFromDisk()
				r.Command("goal-tweak", "Remove budget")
				pending, _ := startProposal(t, r, proposalParams(g["objective"].(string), map[string]any{"token_budget": nil}))
				d.waitOpen(t)
				d.result(confirmAnswer, nil)
				pending.wait(t)
				saved := firstGoal(t, r.Cwd)
				if saved["status"] != "paused" {
					t.Errorf("status %v", saved["status"])
				}
				if _, has := saved["tokenBudget"]; has {
					t.Errorf("tokenBudget %v", saved["tokenBudget"])
				}
				if saved["scheduler"].(map[string]any)["used"] != 3.0 {
					t.Errorf("scheduler used %v", saved["scheduler"])
				}
			})
		}
	})

	tw(t, f, "lowering active budget stops immediately and invalidates queued dispatch", func(t *testing.T) {
		r, d := tweakRig(t)
		createGoalWithTasks(t, r, d, "Objective", taskParams([3]string{"one", "One", ""}))
		g := firstGoal(t, r.Cwd)
		g["usage"].(map[string]any)["tokensUsed"] = 20.0
		// A goal saved before next actions were removed still carries decision.nextAction on disk. Seeding it here shows that such a
		// goal loads and tweaks normally.
		sch := goal.NewSchedulerFor(tweakSession)
		sch["phase"] = "ready"
		sch["decision"] = map[string]any{"kind": "ready", "purpose": "ready", "nextAction": "Old action"}
		sch["used"] = 2.0
		generation := sch["generation"]
		g["scheduler"] = sch
		goal.WriteGoalFor(r.Cwd, g)
		r.Probe.ReconcileFromDisk()
		r.Command("goal-tweak", "Set budget to 1")
		pending, _ := startProposal(t, r, proposalParams(g["objective"].(string), map[string]any{"token_budget": 1.0}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		saved := firstGoal(t, r.Cwd)
		ss := saved["scheduler"].(map[string]any)
		if saved["status"] != "budget_limited" {
			t.Errorf("status %v", saved["status"])
		}
		if _, has := ss["decision"]; has {
			t.Errorf("decision %v", ss["decision"])
		}
		if ss["generation"] == generation {
			t.Error("the generation is renewed")
		}
		if ss["used"] != 2.0 {
			t.Errorf("used %v", ss["used"])
		}
	})

	tw(t, f, "budget confirmation rejects a changed revision", func(t *testing.T) {
		r, d := tweakRig(t)
		createGoalWithTasks(t, r, d, "Objective", taskParams([3]string{"one", "One", ""}))
		r.Command("goal-tweak", "Remove budget")
		g := firstGoal(t, r.Cwd)
		pending, out := startProposal(t, r, proposalParams(g["objective"].(string), map[string]any{"token_budget": nil}))
		d.waitOpen(t)
		rev, _ := g["revision"].(float64)
		changed := map[string]any{}
		for k, v := range g {
			changed[k] = v
		}
		changed["tokenBudget"], changed["revision"] = 123.0, rev+1
		goal.WriteGoalFor(r.Cwd, changed)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		matchText(t, toolText(t, *out), `changed during confirmation`, "result")
		if firstGoal(t, r.Cwd)["tokenBudget"] != 123.0 {
			t.Errorf("tokenBudget %v", firstGoal(t, r.Cwd)["tokenBudget"])
		}
	})

	tw(t, f, "invalid draft budget ${value} is rejected before confirmation", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			value any
		}{{"0", 0.0}, {"-1", -1.0}, {"1.5", 1.5}, {"9007199254740992", math.Pow(2, 53)}, {"50", "50"}} {
			t.Run(c.name, func(t *testing.T) {
				r, d := tweakRig(t)
				r.Command("goal", "Objective")
				out := runProposal(t, r, proposalParams("Objective", map[string]any{"token_budget": c.value}))
				if d.hasDialog() {
					t.Error("the dialog never opens")
				}
				matchText(t, toolText(t, out), `token_budget`, "message")
				if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
					t.Errorf("goal files %d", n)
				}
			})
		}
	})

	tw(t, f, "failed budget mutation never reports success or changes the saved goal", func(t *testing.T) {
		r, d := tweakRig(t)
		createGoalWithTasks(t, r, d, "Objective", taskParams([3]string{"one", "One", ""}))
		r.Command("goal-tweak", "Set budget")
		before := firstGoal(t, r.Cwd)
		pending, out := startProposal(t, r, proposalParams(before["objective"].(string), map[string]any{"token_budget": 123.0}))
		d.waitOpen(t)
		// The original mocks goalService.apply to fail with "injected storage failure"; here the storage itself fails: the goal
		// directory stops accepting writes while the confirmation is open.
		dir := filepath.Join(r.Cwd, ".pi", "goals")
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(dir, 0o755) }()
		d.result(confirmAnswer, nil)
		pending.wait(t)
		_ = os.Chmod(dir, 0o755)
		matchText(t, toolText(t, *out), `not applied: `, "result")
		if after := firstGoal(t, r.Cwd); !reflect.DeepEqual(after, before) {
			t.Errorf("disk goal changed:\n%v\n%v", before, after)
		}
	})
}
