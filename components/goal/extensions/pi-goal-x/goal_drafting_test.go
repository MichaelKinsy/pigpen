package pi_goal_x_test

// Twins of tests/goal-drafting.test.ts: handler-level guided-drafting coverage (confirmation decisions, atomic creation with
// verification contract and nested task tree, Sisyphus fidelity, tasks/contracts-disabled variants, /goal-tweak under focus races,
// headless auto-confirm). The original drives ui.custom's component result (`dialogResult`); the fake host cannot run a local
// component, so the same questionnaire runs on its basic select dialogs and the dialogDriver answers them.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

type draftOpts struct {
	hasUI    bool
	settings string // "" none (the original's cwd has no settings file)
	env      map[string]string
	cwd      string
	branch   []any
	seed     map[string]string
	select_  func(string, []string) (string, bool)
	input    func(string, string) (string, bool)
}

// draftRig is the drafting file's createHarness (session "draft-session", tools read/bash/edit/write) plus its dialog driver.
func draftRig(t *testing.T, o draftOpts) (*goalRig, *dialogDriver) {
	t.Helper()
	d := newDialogDriver()
	if o.cwd == "" {
		o.cwd = archivedCwd(t)
	}
	settings := o.settings
	if settings == "" {
		settings = "-"
	}
	sel := d.selectFn
	if o.select_ != nil {
		sel = o.select_
	}
	r := newGoalRig(t, rigOptions{
		Cwd: o.cwd, HasUI: boolPtr(o.hasUI), SessionID: "draft-session", Settings: settings, Env: o.env, Branch: o.branch, Seed: o.seed,
		ActiveTools: []string{"read", "bash", "edit", "write"}, Select: sel, Input: o.input,
	})
	return r, d
}

func startDraftSession(r *goalRig) { r.Fire("session_start", map[string]any{"reason": "start"}) }

func proposalParams(objective string, extra map[string]any) map[string]any {
	p := map[string]any{"objective": objective, "sisyphus": false}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func taskParams(items ...[3]string) []any {
	var out []any
	for _, it := range items {
		m := map[string]any{"id": it[0], "title": it[1]}
		if it[2] != "" {
			m["parent_id"] = it[2]
		}
		out = append(out, m)
	}
	return out
}

// runProposal is runProposal(h, params): the registered tool run to completion.
func runProposal(t *testing.T, r *goalRig, params map[string]any) json.RawMessage {
	t.Helper()
	if !r.Host.tools["propose_goal_draft"] {
		t.Fatal("propose_goal_draft must be registered during a draft")
	}
	out, failure := r.Tool("propose_goal_draft", params)
	if failure != "" {
		t.Fatalf("propose_goal_draft: %s", failure)
	}
	return out
}

// startProposal runs the proposal on its own goroutine (the original's pending promise).
func startProposal(t *testing.T, r *goalRig, params map[string]any) (*async, *json.RawMessage) {
	t.Helper()
	var out json.RawMessage
	a := goAsync(func() {
		o, failure := r.Tool("propose_goal_draft", params)
		if failure != "" {
			t.Errorf("propose_goal_draft: %s", failure)
		}
		out = o
	})
	return a, &out
}

func firstGoal(t *testing.T, cwd string) map[string]any {
	t.Helper()
	goals := goal.DiskGoals(cwd)
	if len(goals) != 1 {
		t.Fatalf("exactly one active goal expected, got %d", len(goals))
	}
	return goals[0]
}

func eventsOfType(cwd, typ string) []map[string]any {
	events, _ := goal.LedgerFor(cwd)
	var out []map[string]any
	for _, e := range events {
		if e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

func taskIDs(g map[string]any) []string {
	tl, _ := g["taskList"].(map[string]any)
	var ids []string
	for _, x := range anySlice(tl["tasks"]) {
		ids = append(ids, x.(map[string]any)["id"].(string))
	}
	return ids
}

func anySlice(v any) []any { s, _ := v.([]any); return s }

func unfocusedProfile(t *testing.T, r *goalRig, msg string) {
	t.Helper()
	tools := r.ActiveTools()
	if !(contains(tools, "create_goal") && contains(tools, "get_goal") && !contains(tools, "update_goal")) {
		t.Errorf("%s: %v", msg, tools)
	}
}

func draftEntries(r *goalRig) []rigEntry {
	var out []rigEntry
	for _, e := range r.Entries() {
		if e.CustomType == "pi-goal-draft" {
			out = append(out, e)
		}
	}
	return out
}

func matchText(t *testing.T, got, pattern, msg string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(got) {
		t.Errorf("%s: %q does not match /%s/", msg, got, pattern)
	}
}

func renderedCall(t *testing.T, r *goalRig, params map[string]any) string {
	t.Helper()
	lines, failure := r.Host.RenderTool("propose_goal_draft", ToolRender{Card: "draft-card", Args: params, Width: 120})
	if failure != "" {
		t.Fatalf("renderCall: %s", failure)
	}
	return strings.Join(lines, "\n")
}

func TestGoalDrafting(t *testing.T) {
	const f = "goal-drafting"

	tw(t, f, "dialog cancel is a durable no-op and clears the draft", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Ship a small feature")
		pending, out := startProposal(t, r, proposalParams("Ship a small feature.\nSuccess criteria: tests pass.", nil))
		d.waitOpen(t)
		d.result(cancelAnswer, nil)
		pending.wait(t)
		matchText(t, toolText(t, *out), `Draft cancelled`, "result")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("cancel must not create a goal: %d", n)
		}
		if n := len(eventsOfType(r.Cwd, "goal_created")); n != 0 {
			t.Errorf("cancel must not write a goal_created event: %d", n)
		}
		// Drafting tools removed; execution profile restored.
		unfocusedProfile(t, r, "unfocused execution profile restored")
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("drafting tools removed")
		}
	})

	tw(t, f, "regression: the proposal emits the complete goal presentation to the transcript before the dialog decision", func(t *testing.T) {
		// Reported repro: the goal draft was not presenting tasks. The complete goal — every objective contract section and every task
		// line — must be in the terminal buffer while the confirmation dialog is open, so the user can scroll up and read it.
		// propose_goal_draft renders this via renderCall, which pi displays in the transcript as soon as the tool call starts — before
		// the dialog opens.
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Present the complete goal in the confirmation dialog")
		objective := strings.Join([]string{
			"=== Goal ===",
			"Objective: Fix the goal draft so the complete goal is presented.",
			"Success criteria: every section and every task line is readable.",
			"Boundaries: in scope: extensions; out of scope: pi-tui API changes.",
			"Constraints: the dialog frame never exceeds the terminal height.",
			"Verification contract: npm run check (0 errors); npm test (0 failures).",
			"If blocked: stop and ask the user.",
		}, "\n")
		tasks := taskParams([3]string{"task-1", "Add the failing regression tests", ""}, [3]string{"task-2", "Fix the dialog slice", ""}, [3]string{"task-3", "Guarantee scrollback completeness", ""})
		params := proposalParams(objective, map[string]any{"tasks": tasks})
		pending, _ := startProposal(t, r, params)
		d.waitOpen(t)

		// The tool-call renderer is what pi puts into the transcript while the dialog is open; render it exactly as pi would at call time.
		rendered := renderedCall(t, r, params)
		for _, section := range []string{"Objective: Fix the goal draft", "Success criteria:", "Boundaries:", "Constraints:", "Verification contract:", "If blocked:"} {
			if !strings.Contains(rendered, section) {
				t.Errorf("objective contract section must be in the transcript presentation: %s", section)
			}
		}
		if !strings.Contains(rendered, "Tasks proposed for confirmation:") {
			t.Error("tasks header must be in the transcript presentation")
		}
		for _, task := range tasks {
			m := task.(map[string]any)
			if line := "[ ] " + m["id"].(string) + ": " + m["title"].(string); !strings.Contains(rendered, line) {
				t.Errorf("task line must be in the transcript presentation: %s", line)
			}
		}
		d.result(continueAnswer, nil)
		pending.wait(t)

		// Derived-task fallback: when the proposal carries no explicit task list, the transcript presentation still shows the tasks
		// derived from the objective's ordered-step structure (F2), so the user can review them.
		derivedObjective := "Present the complete goal.\n1. Add the failing regression tests.\n2. Fix the dialog slice.\n3. Guarantee scrollback completeness."
		pending2, _ := startProposal(t, r, proposalParams(derivedObjective, nil))
		d.waitOpen(t)
		rendered2 := renderedCall(t, r, proposalParams(derivedObjective, nil))
		if !strings.Contains(rendered2, "Tasks derived from the objective") {
			t.Error("derived tasks header must be in the transcript presentation")
		}
		for _, step := range []string{"[ ] step-1: Add the failing regression tests", "[ ] step-2: Fix the dialog slice", "[ ] step-3: Guarantee scrollback completeness"} {
			if !strings.Contains(rendered2, step) {
				t.Errorf("derived task line must be in the transcript presentation: %s", step)
			}
		}
		d.result(cancelAnswer, nil)
		pending2.wait(t)
	})

	tw(t, f, "renderCall for a tweak without explicit tasks shows the retained current list, never a derived phantom", func(t *testing.T) {
		// §single-task-set regression: the scrollback presentation derived tasks from the objective without knowing the draft mode, so a
		// tweak without an explicit task list showed a phantom "derived from the objective" set that is never persisted (the apply
		// retains the current list). The presentation must mirror the dialog preview: the retained current list.
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Initial objective")
		pending, _ := startProposal(t, r, proposalParams("Initial objective.", map[string]any{
			"tasks": taskParams([3]string{"keep-1", "Retained task", ""}, [3]string{"keep-2", "Another task", ""}),
		}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)

		r.Command("goal-tweak", "Tighten wording")
		if !r.Host.tools["propose_goal_draft"] {
			t.Fatal("propose_goal_draft registered during a tweak draft")
		}
		// A tweak proposal WITHOUT explicit tasks: the retained list is the one task set that will be persisted.
		rendered := renderedCall(t, r, proposalParams("Tightened objective", nil))
		for _, want := range []string{"Current task list (retained unchanged):", "[ ] keep-1: Retained task", "[ ] keep-2: Another task"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("missing %q in %q", want, rendered)
			}
		}
		if strings.Contains(rendered, "Tasks derived from the objective") {
			t.Error("no derived phantom for a tweak")
		}
		if strings.Contains(rendered, "Tasks proposed for confirmation:") {
			t.Error("no explicit-tasks label without an explicit list")
		}

		// The dialog preview agrees with the presentation (one task set).
		pending2, _ := startProposal(t, r, proposalParams("Tightened objective", nil))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending2.wait(t)
		if ids := strings.Join(taskIDs(firstGoal(t, r.Cwd)), ","); ids != "keep-1,keep-2" {
			t.Errorf("retained list persisted unchanged: %s", ids)
		}
	})

	tw(t, f, "renderCall for a new draft derives from the same objective text the apply path persists (shown == persisted)", func(t *testing.T) {
		// §single-task-set regression: the derived preview must equal the persisted task list. The apply path derives from the extracted
		// objective (the Verification contract line removed); the presentation must derive from the same input.
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Ship the release")
		objective := "Ship the release.\n1) Implement core\n2) Add tests\n3) Write docs\nVerification contract: npm test (0 failures)."
		pending, _ := startProposal(t, r, proposalParams(objective, nil))
		d.waitOpen(t)
		presented := renderedCall(t, r, proposalParams(objective, nil))
		if !strings.Contains(presented, "Tasks derived from the objective") {
			t.Error("derived preview shown for a structured new draft")
		}
		d.result(confirmAnswer, nil)
		pending.wait(t)
		g := firstGoal(t, r.Cwd)
		if vc, _ := g["verificationContract"].(string); !strings.Contains(vc, "npm test") {
			t.Errorf("verification contract persisted: %v", g["verificationContract"])
		}
		ids := taskIDs(g)
		for _, id := range []string{"step-1", "step-2", "step-3"} {
			if !contains(ids, id) {
				t.Errorf("persisted derived task %s: %v", id, ids)
			}
			if !strings.Contains(presented, "[ ] "+id+": ") {
				t.Errorf("presented derived task %s matches persisted", id)
			}
		}
		if len(ids) != 3 {
			t.Errorf("no phantom task from the contract line: %v", ids)
		}
	})

	tw(t, f, "continue refining keeps the draft alive and a second proposal confirms", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Build a tiny app")
		tasks := taskParams([3]string{"setup", "Set up", ""}, [3]string{"verify", "Verify", ""})
		pending1, out1 := startProposal(t, r, proposalParams("Build a tiny app.\nSuccess criteria: it runs.", map[string]any{"tasks": tasks}))
		d.waitOpen(t)
		d.result(continueAnswer, nil)
		pending1.wait(t)
		matchText(t, toolText(t, *out1), `refinement requested`, "first result")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("refining must not create a goal: %d", n)
		}
		if !contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("drafting tools remain while refining")
		}
		// Second proposal confirms with the same task plan.
		pending2, _ := startProposal(t, r, proposalParams("Build a tiny app.\nSuccess criteria: it runs.", map[string]any{"tasks": tasks}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending2.wait(t)
		if ids := strings.Join(taskIDs(firstGoal(t, r.Cwd)), ","); ids != "setup,verify" {
			t.Errorf("answers and proposed tasks survive refining: %s", ids)
		}
	})

	tw(t, f, "confirmed proposal persists verification contract and nested tasks, then restores the profile", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Add search")
		objective := "Add search.\nSuccess criteria: queries return results.\nVerification contract: Run npm test (0 failures)"
		tasks := taskParams([3]string{"index", "Index documents", ""}, [3]string{"rank", "Rank results", "index"}, [3]string{"surface", "Surface in UI", "index"})
		pending, _ := startProposal(t, r, proposalParams(objective, map[string]any{"tasks": tasks, "block_completion": true}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		g := firstGoal(t, r.Cwd)
		if vc, _ := g["verificationContract"].(string); !strings.Contains(vc, "npm test") {
			t.Errorf("verification contract persisted: %v", g["verificationContract"])
		}
		if strings.Contains(g["objective"].(string), "Verification contract") {
			t.Error("contract line removed from objective")
		}
		tl := g["taskList"].(map[string]any)
		if tl["blockCompletion"] != true {
			t.Errorf("blockCompletion %v", tl["blockCompletion"])
		}
		if ids := strings.Join(taskIDs(g), ","); ids != "index" {
			t.Errorf("parent task is the root: %s", ids)
		}
		var children []string
		for _, s := range anySlice(tl["tasks"].([]any)[0].(map[string]any)["subtasks"]) {
			children = append(children, s.(map[string]any)["id"].(string))
		}
		if strings.Join(children, ",") != "rank,surface" {
			t.Errorf("children become subtasks: %v", children)
		}
		// Execution profile restored: drafting tools gone, five-tool profile back.
		tools := r.ActiveTools()
		for _, name := range []string{"update_goal", "set_goal_tasks", "update_goal_task"} {
			if !contains(tools, name) {
				t.Errorf("%s missing: %v", name, tools)
			}
		}
		if contains(tools, "propose_goal_draft") {
			t.Error("drafting tools removed after confirmation")
		}
	})

	tw(t, f, "an unrequested token budget is never created: no guideline invites one, and omitting it stores no budget", func(t *testing.T) {
		// A drafted goal must carry no token budget unless the user explicitly asked for one. Two independent guards: the tool's
		// promptGuidelines tell the model to omit token_budget unless the user asked, and omitting token_budget persists a goal with no
		// budget at all.
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Add search")
		if !r.Host.tools["propose_goal_draft"] {
			t.Fatal("propose_goal_draft must be registered during a draft")
		}
		var budgetGuideline string
		for _, line := range goal.ProposeDraftGuidelines() {
			if regexp.MustCompile(`(?i)token_budget`).MatchString(line) {
				budgetGuideline = line
				break
			}
		}
		if budgetGuideline == "" {
			t.Fatal("propose_goal_draft must carry a token_budget prompt guideline")
		}
		matchText(t, budgetGuideline, `(?i)only when the user explicitly asked`, "guideline must scope budgets to explicit user requests")
		matchText(t, budgetGuideline, `(?i)never invent|never.*infer`, "guideline must forbid inventing a budget")

		// A large-sounding objective with no budget request must stay budgetless.
		pending, _ := startProposal(t, r, proposalParams("Rewrite the entire scheduling subsystem.\nSuccess criteria: behavior is preserved.", nil))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		g := firstGoal(t, r.Cwd)
		if _, has := g["tokenBudget"]; has {
			t.Errorf("no budget is persisted when the user did not request one: %v", g["tokenBudget"])
		}
		if n := len(eventsOfType(r.Cwd, "goal_budget_warning")) + len(eventsOfType(r.Cwd, "goal_budget_limited")); n != 0 {
			t.Errorf("no budget lifecycle events for an unbudgeted goal: %d", n)
		}
		if g["status"] == "budget_limited" {
			t.Error("an unbudgeted goal is never budget_limited")
		}
	})

	tw(t, f, "sisyphus mode mismatch and structural sufficiency are validated before confirmation", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("sisyphus", "Refactor auth")
		wrong := runProposal(t, r, proposalParams("Refactor auth cleanly.", map[string]any{"sisyphus": false}))
		matchText(t, toolText(t, wrong), `mode does not match`, "wrong mode")
		noSteps := runProposal(t, r, proposalParams("Refactor auth cleanly.", map[string]any{"sisyphus": true}))
		matchText(t, toolText(t, noSteps), `ordered steps`, "no steps")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("no goal created for invalid proposals: %d", n)
		}
		ok := runProposal(t, r, proposalParams("Refactor auth: 1) extract token validation. 2) wire it into login. 3) update tests.", map[string]any{"sisyphus": true}))
		if !strings.Contains(toolText(t, ok), "Goal created") && !toolTerminate(ok) {
			t.Errorf("created: %s", ok)
		}
		if firstGoal(t, r.Cwd)["sisyphus"] != true {
			t.Error("sisyphus mode preserved")
		}
	})

	tw(t, f, "/sisyphus-direct rejects structurally insufficient objectives", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("sisyphus-direct", "Just do the thing")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("insufficient sisyphus objective rejected: %d", n)
		}
		if !anyHas(r.Notices(), "ordered steps") {
			t.Error("guidance notification emitted")
		}
		r.Command("sisyphus-direct", "Refactor: 1) extract. 2) wire. 3) test.")
		if firstGoal(t, r.Cwd)["sisyphus"] != true {
			t.Error("sisyphus goal created")
		}
	})

	tw(t, f, "tasks-disabled settings reject task proposals and confirm without a task list", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{settings: `{"disableTasks": true}`})
		startDraftSession(r)
		r.Command("goal", "Write a guide")
		withTasks := runProposal(t, r, proposalParams("Write a guide.", map[string]any{"tasks": taskParams([3]string{"a", "A", ""})}))
		matchText(t, toolText(t, withTasks), `disabled by settings`, "tasks disabled")
		runProposal(t, r, proposalParams("Write a guide.", nil))
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 1 {
			t.Errorf("task-free proposal confirms: %d", n)
		}
		if _, has := firstGoal(t, r.Cwd)["taskList"]; has {
			t.Error("no task list created when tasks disabled")
		}
	})

	tw(t, f, "contracts-disabled settings strip the verification contract from a confirmed proposal", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{settings: `{"disableContracts": true}`})
		startDraftSession(r)
		r.Command("goal", "Polish the docs")
		runProposal(t, r, proposalParams("Polish the docs.\nVerification contract: Run npm test (0 failures)", nil))
		g := firstGoal(t, r.Cwd)
		if _, has := g["verificationContract"]; has {
			t.Error("contract not persisted when contracts disabled")
		}
		// The line is left as plain objective prose; it is never promoted to the structured contract field.
		if !strings.Contains(g["objective"].(string), "Verification contract: Run npm test") {
			t.Errorf("contract line retained as prose: %q", g["objective"])
		}
	})

	tw(t, f, "/goal-tweak confirms a revision under focus validation", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		before := firstGoal(t, r.Cwd)
		r.Command("goal-tweak", "Make it better")
		pending, out := startProposal(t, r, proposalParams("Revised objective with clarity.", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		matchText(t, toolText(t, *out), `tweak confirmed`, "result")
		after := firstGoal(t, r.Cwd)
		if after["id"] != before["id"] {
			t.Error("same goal revised")
		}
		if !strings.Contains(after["objective"].(string), "Revised objective") {
			t.Error("objective updated")
		}
		if len(eventsOfType(r.Cwd, "goal_tweaked")) == 0 {
			t.Error("goal_tweaked event recorded")
		}
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("tweak draft cleared after confirmation")
		}
	})

	tw(t, f, "tweak against a changed focus is rejected without mutation", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		before := firstGoal(t, r.Cwd)
		r.Command("goal-tweak", "Revise it")
		r.Command("goal-unfocus", "")
		out := runProposal(t, r, proposalParams("Changed objective", map[string]any{"sisyphus": false}))
		matchText(t, toolText(t, out), `goal changed while drafting`, "result")
		after := goal.DiskGoals(r.Cwd)[0]
		if after["objective"] != before["objective"] {
			t.Error("no mutation on stale tweak target")
		}
	})

	tw(t, f, "headless proposal auto-confirm semantics are explicit", func(t *testing.T) {
		cwd := archivedCwd(t)
		// Headless with no override: proposal auto-confirms (harness-friendly).
		h1, _ := draftRig(t, draftOpts{cwd: cwd})
		startDraftSession(h1)
		h1.Command("goal", "One thing")
		runProposal(t, h1, proposalParams("One thing.", nil))
		if n := len(goal.ActiveGoalFiles(cwd)); n != 1 {
			t.Errorf("headless auto-confirms by default: %d", n)
		}

		// Explicit opt-out keeps the draft pending and creates nothing.
		h2, _ := draftRig(t, draftOpts{cwd: cwd, env: map[string]string{"PI_GOAL_AUTO_CONFIRM": "0"}})
		startDraftSession(h2)
		h2.Command("goal", "Another thing")
		result := runProposal(t, h2, proposalParams("Another thing.", nil))
		matchText(t, toolText(t, result), `refinement requested`, "opt-out")
		if n := len(goal.ActiveGoalFiles(cwd)); n != 1 {
			t.Errorf("opt-out must not create a second goal: %d", n)
		}

		// UI present + explicit override: confirm without the dialog.
		h3, d3 := draftRig(t, draftOpts{cwd: cwd, hasUI: true, env: map[string]string{"PI_GOAL_AUTO_CONFIRM": "1"}})
		startDraftSession(h3)
		h3.Command("goal", "Third thing")
		runProposal(t, h3, proposalParams("Third thing.", nil))
		if d3.hasDialog() {
			t.Error("override confirms without opening the dialog")
		}
		if n := len(goal.ActiveGoalFiles(cwd)); n != 2 {
			t.Errorf("goals %d", n)
		}
	})
}
