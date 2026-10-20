package pi_goal_x_test

// Twins of tests/goal-drafting.test.ts, second half: questionnaire tools, durable draft state and rehydration, the auditor choice,
// tweak resume, objective limits, the durable proposal summary, the proposal text and the tweak merge.

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

// selectLabel picks the option of a questionnaire select whose answer text is want ("1. A", "2. B (Recommended)").
func selectLabel(options []string, want string) (string, bool) {
	for _, o := range options {
		if strings.Contains(o, ". "+want) {
			return o, true
		}
	}
	return "", false
}

func lastDraftData(r *goalRig) map[string]any {
	es := draftEntries(r)
	if len(es) == 0 {
		return nil
	}
	return es[len(es)-1].Data
}

func TestGoalDraftingMore(t *testing.T) {
	const f = "goal-drafting"

	tw(t, f, "questionnaire tools require an active draft and return structured answers", func(t *testing.T) {
		var mu sync.Mutex
		var selects, inputs []string
		r, _ := draftRig(t, draftOpts{hasUI: true,
			select_: func(title string, options []string) (string, bool) {
				mu.Lock()
				selects = append(selects, title)
				mu.Unlock()
				return selectLabel(options, "A")
			},
			input: func(title, _ string) (string, bool) {
				mu.Lock()
				inputs = append(inputs, title)
				mu.Unlock()
				return "Next week", true
			}})
		startDraftSession(r)
		if !r.Host.tools["goal_questionnaire"] {
			t.Fatal("goal_questionnaire registered")
		}
		noDraft, failure := r.Tool("goal_questionnaire", map[string]any{"questions": []any{}})
		if failure != "" {
			t.Fatal(failure)
		}
		matchText(t, toolText(t, noDraft), `No guided goal draft is active`, "no draft")
		r.Command("goal", "Plan a migration")
		out, failure := r.Tool("goal_questionnaire", map[string]any{"questions": []any{
			map[string]any{"id": "scope", "question": "Which systems?", "options": []any{"A", "B"}},
			map[string]any{"id": "deadline", "question": "When?", "options": []any{}},
		}})
		if failure != "" {
			t.Fatal(failure)
		}
		mu.Lock()
		if len(selects) == 0 || len(inputs) == 0 {
			t.Errorf("batch questionnaire opens the dialogs: selects %v inputs %v", selects, inputs)
		}
		mu.Unlock()
		text := toolText(t, out)
		matchText(t, text, `\*\*Q:\*\* Which systems\?`, "first question")
		matchText(t, text, `\*\*A:\*\* A`, "first answer")
		matchText(t, text, `\*\*A:\*\* Next week`, "custom answer")
	})

	tw(t, f, "single dependent follow-up question returns a structured answer", func(t *testing.T) {
		opened := false
		r, _ := draftRig(t, draftOpts{hasUI: true, select_: func(_ string, options []string) (string, bool) {
			opened = true
			return selectLabel(options, "staging")
		}})
		startDraftSession(r)
		r.Command("goal", "Automate deploys")
		if !r.Host.tools["goal_question"] {
			t.Fatal("goal_question registered")
		}
		out, failure := r.Tool("goal_question", map[string]any{"question": "Which environment first?", "options": []any{"staging", "production"}})
		if failure != "" {
			t.Fatal(failure)
		}
		if !opened {
			t.Error("single question opens the dialog")
		}
		text := toolText(t, out)
		matchText(t, text, `\*\*Q:\*\* Which environment first\?`, "question")
		matchText(t, text, `\*\*A:\*\* staging`, "answer")
	})

	tw(t, f, "/goal-cancel clears the draft as a durable no-op", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("goal", "Build the widget")
		if !contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("drafting profile installed")
		}
		r.Command("goal-cancel", "")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("cancel writes no goal file: %d", n)
		}
		if n := len(eventsOfType(r.Cwd, "goal_created")); n != 0 {
			t.Errorf("cancel writes no ledger event: %d", n)
		}
		unfocusedProfile(t, r, "unfocused execution profile restored")
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("drafting tools removed")
		}
		if !anyHas(r.Notices(), "Draft cancelled") {
			t.Error("cancel notification")
		}
		// The durable entry is tombstoned, not removed.
		es := draftEntries(r)
		if len(es) != 2 {
			t.Fatalf("start entry plus tombstone: %d", len(es))
		}
		if es[1].Data["clearedAt"] == nil || es[1].Data["clearedAt"] == "" {
			t.Error("tombstone carries clearedAt")
		}
		// A second cancel is a no-op with guidance.
		r.Command("goal-cancel", "")
		if !anyHas(r.Notices(), "No active draft") {
			t.Error("second cancel guidance")
		}
	})

	tw(t, f, "an unconfirmed draft survives session_tree rehydration", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("goal", "Migrate the database")
		// Tree navigation reloads state; the durable draft must come back.
		r.Fire("session_tree", map[string]any{})
		if !contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("drafting profile restored after rehydration")
		}
		if !contains(r.ActiveTools(), "propose_goal_draft") {
			t.Error("proposal tool restored")
		}
		// And the restored draft still confirms atomically.
		runProposal(t, r, proposalParams("Migrate the database.\nSuccess criteria: no data loss.", nil))
		if !strings.Contains(firstGoal(t, r.Cwd)["objective"].(string), "Migrate the database") {
			t.Error("restored draft confirms")
		}
		// After confirmation the draft is gone and stays gone across rehydration.
		r.Fire("session_tree", map[string]any{})
		tools := r.ActiveTools()
		if !contains(tools, "update_goal") {
			t.Error("execution profile after confirm")
		}
		if contains(tools, "goal_questionnaire") {
			t.Error("no draft restored after confirm")
		}
	})

	tw(t, f, "a second draft offers resume, replace, or cancel and never silently discards", func(t *testing.T) {
		cwd := archivedCwd(t)
		// Resume keeps the first draft.
		answer := ""
		h1, _ := draftRig(t, draftOpts{cwd: cwd, hasUI: true, select_: func(string, []string) (string, bool) { return answer, answer != "" }})
		startDraftSession(h1)
		h1.Command("goal", "First topic")
		firstPromptCount := len(h1.SentUser())
		answer = "Resume the existing draft"
		h1.Command("goal", "Second topic")
		if len(h1.SentUser()) != firstPromptCount {
			t.Error("no new draft prompt when resuming")
		}
		if !anyHas(h1.Notices(), "already active; resuming") {
			t.Error("resume notification")
		}
		if !contains(h1.ActiveTools(), "goal_questionnaire") {
			t.Error("first draft still active")
		}

		// Cancel keeps the first draft.
		answer = "Cancel"
		h1.Command("goal", "Third topic")
		if len(h1.SentUser()) != firstPromptCount {
			t.Error("no new prompt when cancelling")
		}
		if !anyHas(h1.Notices(), "existing draft stays active") {
			t.Error("cancel notification")
		}

		// Replace starts the new draft and tombstones the old one.
		answer = "Replace it with a new draft"
		h1.Command("goal", "Fourth topic")
		if len(h1.SentUser()) <= firstPromptCount {
			t.Error("replacement starts a fresh draft")
		}
		if !anyHas(h1.Notices(), "Replacing the active draft") {
			t.Error("replacement notification")
		}
		es := draftEntries(h1)
		tombstones := 0
		for _, e := range es {
			if e.Data["clearedAt"] != nil {
				tombstones++
			}
		}
		if tombstones != 1 {
			t.Errorf("the replaced draft is tombstoned: %d", tombstones)
		}
		if es[len(es)-1].Data["clearedAt"] != nil {
			t.Error("the newest entry is the replacement start, not a tombstone")
		}

		// Headless: a second draft replaces with an explicit warning (not silent).
		h2, _ := draftRig(t, draftOpts{cwd: cwd})
		startDraftSession(h2)
		h2.Command("goal", "Headless one")
		before := len(h2.SentUser())
		h2.Command("goal", "Headless two")
		if len(h2.SentUser()) <= before {
			t.Error("headless second draft proceeds")
		}
		if !anyHas(h2.Notices(), "Replacing the active draft") {
			t.Error("headless replacement is not silent")
		}
	})

	tw(t, f, "a stale tweak draft is invalidated on rehydration when its target is unfocused", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", "Revise it")
		r.Command("goal-unfocus", "")
		r.Fire("session_tree", map[string]any{})
		unfocusedProfile(t, r, "unfocused execution profile after stale tweak invalidation")
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("stale tweak draft not restored")
		}
		if !anyHas(r.Notices(), "stale") {
			t.Error("stale draft warning")
		}
		// The stale tweak draft cannot confirm anything.
		out := runProposal(t, r, proposalParams("Changed objective", map[string]any{"sisyphus": false}))
		matchText(t, toolText(t, out), `No guided goal draft is active`, "stale draft")
	})

	tw(t, f, "direct goal creation interrupts and clears an active draft", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("goal", "Drafted topic")
		if !contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("drafting active")
		}
		r.Command("goal-direct", "Immediate goal")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 1 {
			t.Errorf("direct creation proceeds: %d", n)
		}
		tools := r.ActiveTools()
		if !contains(tools, "update_goal") {
			t.Error("active execution profile restored after direct creation")
		}
		if contains(tools, "goal_questionnaire") {
			t.Error("draft cleared by direct creation")
		}
		// The tombstoned draft must not resurrect across rehydration.
		r.Fire("session_tree", map[string]any{})
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("draft stays cleared after rehydration")
		}
	})

	tw(t, f, "/goal-status reports state without initiating drafting", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{})
		startDraftSession(r)
		r.Command("goal-direct", "Status target")
		messagesBefore := len(r.SentUser())
		r.Command("goal-status", "")
		if len(r.SentUser()) != messagesBefore {
			t.Error("status must not initiate a draft or agent turn")
		}
		if !anyHas(r.Notices(), "Status target") {
			t.Error("focused goal reported")
		}
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("no drafting profile")
		}
		// Unfocused with open goals: reports the pool without mutating.
		r.Command("goal-unfocus", "")
		r.Command("goal-status", "")
		found := false
		for _, n := range r.Notices() {
			found = found || (strings.Contains(n, "open goal") && strings.Contains(n, "/goal-focus"))
		}
		if !found {
			t.Errorf("unfocused panel reported: %v", r.Notices())
		}
	})

	tw(t, f, "the auditor choice persists on create and through continue refining", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Audited work")

		// First proposal: user disables the auditor but keeps refining.
		p1, _ := startProposal(t, r, proposalParams("Audited work.\nSuccess criteria: done.", nil))
		d.waitOpen(t)
		d.result(continueAnswer, boolPtr(false))
		p1.wait(t)
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("no goal while refining: %d", n)
		}
		// The choice is preserved in the durable session entry.
		if lastDraftData(r)["auditorEnabled"] != false {
			t.Errorf("auditor choice preserved through continue: %v", lastDraftData(r))
		}

		// Second proposal: confirm with the same (disabled) auditor choice.
		p2, _ := startProposal(t, r, proposalParams("Audited work.\nSuccess criteria: done.", nil))
		d.waitOpen(t)
		d.result(confirmAnswer, boolPtr(false))
		p2.wait(t)
		if firstGoal(t, r.Cwd)["skipAuditor"] != true {
			t.Error("skipAuditor persisted on create")
		}

		// A third goal created with the auditor enabled has no skipAuditor.
		r.Command("goal", "Enabled work")
		p3, _ := startProposal(t, r, proposalParams("Enabled work.", nil))
		d.waitOpen(t)
		d.result(confirmAnswer, boolPtr(true))
		p3.wait(t)
		var enabled map[string]any
		for _, g := range goal.DiskGoals(r.Cwd) {
			if strings.Contains(g["objective"].(string), "Enabled work") {
				enabled = g
			}
		}
		if enabled == nil {
			t.Fatal("the enabled-auditor goal was not created")
		}
		if _, has := enabled["skipAuditor"]; has {
			t.Errorf("auditor enabled means no skipAuditor: %v", enabled["skipAuditor"])
		}
	})

	tw(t, f, "headless confirmation uses effective settings for the auditor", func(t *testing.T) {
		cwd := archivedCwd(t)
		// Default settings: auditor enabled.
		h1, _ := draftRig(t, draftOpts{cwd: cwd})
		startDraftSession(h1)
		h1.Command("goal", "Headless audited")
		runProposal(t, h1, proposalParams("Headless audited.", nil))
		if _, has := firstGoal(t, cwd)["skipAuditor"]; has {
			t.Error("auditor enabled by default in headless")
		}

		// settings.disabled: auditor off at draft start.
		h2, _ := draftRig(t, draftOpts{cwd: cwd, settings: `{"disabled": true}`})
		startDraftSession(h2)
		h2.Command("goal", "Headless unaudited")
		runProposal(t, h2, proposalParams("Headless unaudited.", nil))
		var g2 map[string]any
		for _, g := range goal.DiskGoals(cwd) {
			if strings.Contains(g["objective"].(string), "Headless unaudited") {
				g2 = g
			}
		}
		if g2 == nil || g2["skipAuditor"] != true {
			t.Errorf("disabled settings default the auditor off: %v", g2)
		}
	})

	tw(t, f, "a tweak confirmation persists the auditor choice in the same transaction", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		before := firstGoal(t, r.Cwd)
		if _, has := before["skipAuditor"]; has {
			t.Error("no per-goal auditor setting before the tweak")
		}
		r.Command("goal-tweak", "Revise the scope")
		pending, _ := startProposal(t, r, proposalParams("Revised objective", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, boolPtr(false))
		pending.wait(t)
		after := firstGoal(t, r.Cwd)
		if after["id"] != before["id"] {
			t.Error("same goal")
		}
		if !strings.Contains(after["objective"].(string), "Revised objective") {
			t.Error("objective updated")
		}
		if after["skipAuditor"] != true {
			t.Error("skipAuditor mutated in the tweak transaction")
		}
		if len(eventsOfType(r.Cwd, "goal_tweaked")) == 0 {
			t.Error("tweak recorded")
		}
	})

	tw(t, f, "a tweak confirmation resumes a paused goal (active, pause metadata cleared, goal_resumed event)", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-pause", "")
		paused := firstGoal(t, r.Cwd)
		if paused["status"] != "paused" {
			t.Fatalf("goal paused before the tweak: %v", paused["status"])
		}
		if paused["stopReason"] != "user" {
			t.Errorf("pause records the stop reason: %v", paused["stopReason"])
		}
		r.Command("goal-tweak", "Revise scope")
		pending, out := startProposal(t, r, proposalParams("Revised objective after pause", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		matchText(t, toolText(t, *out), `tweak confirmed`, "result")
		after := firstGoal(t, r.Cwd)
		if after["id"] != paused["id"] {
			t.Error("same goal revised")
		}
		if after["status"] != "active" {
			t.Errorf("paused goal resumed by the tweak: %v", after["status"])
		}
		if after["autoContinue"] != true {
			t.Error("resume re-arms auto-continuation")
		}
		for _, k := range []string{"stopReason", "pauseReason", "pauseSuggestedAction"} {
			if _, has := after[k]; has {
				t.Errorf("%s cleared: %v", k, after[k])
			}
		}
		if !strings.Contains(after["objective"].(string), "Revised objective after pause") {
			t.Error("objective updated")
		}
		if len(eventsOfType(r.Cwd, "goal_tweaked")) == 0 {
			t.Error("goal_tweaked recorded")
		}
		resumed := eventsOfType(r.Cwd, "goal_resumed")
		if len(resumed) != 1 {
			t.Fatalf("exactly one goal_resumed event: %d", len(resumed))
		}
		if resumed[0]["reason"] != "tweak" {
			t.Errorf("resume reason is the tweak: %v", resumed[0]["reason"])
		}
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("tweak draft cleared after confirmation")
		}
	})

	tw(t, f, "a tweak confirmation resumes a blocked goal", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		blocked, failure := r.Tool("update_goal", map[string]any{"status": "blocked", "reason": "test blocker"})
		if failure != "" {
			t.Fatal(failure)
		}
		if !toolTerminate(blocked) {
			t.Error("blocked terminates the turn")
		}
		if firstGoal(t, r.Cwd)["status"] != "blocked" {
			t.Error("goal blocked before the tweak")
		}
		r.Command("goal-tweak", "Revise scope")
		pending, _ := startProposal(t, r, proposalParams("Revised objective after block", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		after := firstGoal(t, r.Cwd)
		if after["status"] != "active" {
			t.Errorf("blocked goal resumed by the tweak: %v", after["status"])
		}
		if after["autoContinue"] != true {
			t.Error("resume re-arms auto-continuation")
		}
		if _, has := after["stopReason"]; has {
			t.Error("stopReason cleared")
		}
		resumed := eventsOfType(r.Cwd, "goal_resumed")
		if len(resumed) != 1 || resumed[0]["reason"] != "tweak" {
			t.Errorf("exactly one goal_resumed event with reason tweak: %v", resumed)
		}
	})

	tw(t, f, "a tweak confirmation leaves a budget_limited goal behind its hard budget gate", func(t *testing.T) {
		cwd := archivedCwd(t)
		seeded := goal.NewGoalFor("Seed budget-limited objective", true, false, 0)
		seeded["status"] = "budget_limited"
		goal.WriteGoalFor(cwd, seeded)
		r, d := draftRig(t, draftOpts{cwd: cwd, hasUI: true})
		startDraftSession(r)
		if firstGoal(t, cwd)["status"] != "budget_limited" {
			t.Fatal("seeded budget_limited goal focused")
		}
		r.Command("goal-tweak", "Revise scope")
		pending, _ := startProposal(t, r, proposalParams("Revised while budget-limited", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		after := firstGoal(t, cwd)
		if after["status"] != "budget_limited" {
			t.Errorf("budget_limited is a hard gate: the tweak must not resume it: %v", after["status"])
		}
		if !strings.Contains(after["objective"].(string), "Revised while budget-limited") {
			t.Error("objective still revised")
		}
		if n := len(eventsOfType(cwd, "goal_resumed")); n != 0 {
			t.Errorf("no resume event for a budget_limited goal: %d", n)
		}
	})

	tw(t, f, "a tweak confirmation on an active goal stays active without a resume event", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", "Revise scope")
		pending, _ := startProposal(t, r, proposalParams("Revised while active", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		if firstGoal(t, r.Cwd)["status"] != "active" {
			t.Error("active goal stays active")
		}
		if n := len(eventsOfType(r.Cwd, "goal_resumed")); n != 0 {
			t.Errorf("no resume event when the goal was already active: %d", n)
		}
	})

	tw(t, f, "tweak drafting defaults the auditor toggle to the goal's persisted skipAuditor", func(t *testing.T) {
		cwd := archivedCwd(t)
		seeded := goal.NewGoalFor("Auditor-off seed objective", true, false, 0)
		seeded["skipAuditor"] = true
		goal.WriteGoalFor(cwd, seeded)
		r, _ := draftRig(t, draftOpts{cwd: cwd, hasUI: true})
		startDraftSession(r)
		r.Command("goal-tweak", "Revise scope")
		if len(draftEntries(r)) == 0 {
			t.Fatal("draft session entry recorded")
		}
		if lastDraftData(r)["auditorEnabled"] != false {
			t.Errorf("tweak draft defaults to the goal's persisted auditor-off setting: %v", lastDraftData(r))
		}
	})

	tw(t, f, "tweak drafting defaults the auditor toggle on when the goal has no per-goal setting", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", "Revise scope")
		if lastDraftData(r)["auditorEnabled"] != true {
			t.Errorf("tweak draft defaults the auditor on without a per-goal or global disable: %v", lastDraftData(r))
		}
	})

	tw(t, f, "tweak drafting falls back to global settings when the goal has no per-goal auditor setting", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{hasUI: true, settings: `{"disabled": true}`})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", "Revise scope")
		if lastDraftData(r)["auditorEnabled"] != false {
			t.Errorf("tweak draft falls back to the global auditor-disabled setting: %v", lastDraftData(r))
		}
	})

	tw(t, f, "propose_goal_draft accepts long objectives by default (no hard 4000 limit)", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", "Revise scope")
		long := strings.Repeat("x", 5000)
		pending, out := startProposal(t, r, proposalParams(long, map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		matchText(t, toolText(t, *out), `tweak confirmed`, "result")
		if n := len(firstGoal(t, r.Cwd)["objective"].(string)); n != len(long) {
			t.Errorf("long objective survives without a configured limit: %d", n)
		}
	})

	tw(t, f, "propose_goal_draft enforces the configured max objective length setting", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true, settings: `{"objectiveMaxChars": 20}`})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", "Revise scope")
		result := runProposal(t, r, proposalParams(strings.Repeat("x", 21), map[string]any{"sisyphus": false}))
		matchText(t, toolText(t, result), `at most 20 characters \(21 given\)`, "limit message")
		if firstGoal(t, r.Cwd)["objective"] != "Initial objective" {
			t.Error("oversized proposal must not mutate the goal")
		}
		// An at-limit objective is accepted.
		pending, ok := startProposal(t, r, proposalParams(strings.Repeat("y", 20), map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		matchText(t, toolText(t, *ok), `tweak confirmed`, "at-limit result")
		if n := len(firstGoal(t, r.Cwd)["objective"].(string)); n != 20 {
			t.Errorf("at-limit objective is accepted: %d", n)
		}
	})

	tw(t, f, "/goal-tweak command rejects an oversized replacement per the configured limit", func(t *testing.T) {
		r, _ := draftRig(t, draftOpts{hasUI: true, settings: `{"objectiveMaxChars": 30}`})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		r.Command("goal-tweak", strings.Repeat("x", 31))
		if contains(r.ActiveTools(), "goal_questionnaire") {
			t.Error("no draft starts for an oversized replacement")
		}
		if firstGoal(t, r.Cwd)["objective"] != "Initial objective" {
			t.Error("goal unchanged")
		}
	})

	tw(t, f, "confirmed proposal writes the durable summary and the richer confirmation report", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Add CSV export")
		objective := "Add CSV export to the reports page.\nSuccess criteria: exports use active filters.\nVerification contract: Run npm test (0 failures)"
		tasks := taskParams([3]string{"review", "Review the reports page and data source", ""}, [3]string{"export", "Implement filtered CSV export", ""}, [3]string{"download", "Add the download control", ""})
		pending, out := startProposal(t, r, proposalParams(objective, map[string]any{"tasks": tasks}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		text := toolText(t, *out)
		for _, p := range []string{
			// Durable proposal summary in the transcript (§14).
			`Proposed objective:`, `Add CSV export to the reports page\.`, `Proposed plan:`, `1\. Review the reports page and data source`,
			`2\. Implement filtered CSV export`, `Verification:`, `Run npm test \(0 failures\)`, `Automatic continuation: enabled`, `Independent auditor: enabled`,
			// Richer confirmation output (§14).
			`✓ Goal created and focused\.`, `Continuing automatically with the confirmed plan\.`, `Goal id: `, `File: \.pi/goals/active_goal_`, `Tasks: 3`,
			`Verification: Run npm test \(0 failures\)`, `Auditor: enabled`,
		} {
			matchText(t, text, p, "report")
		}
	})

	tw(t, f, "questionnaire answers are captured in the confirmed-goal report (E5 §14)", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Scope the migration")
		var q json.RawMessage
		pendingQ := goAsync(func() {
			out, failure := r.Tool("goal_questionnaire", map[string]any{"questions": []any{map[string]any{"id": "scope", "question": "Which systems?", "options": []any{"A", "B"}}}})
			if failure != "" {
				t.Errorf("goal_questionnaire: %s", failure)
			}
			q = out
		})
		d.waitOpen(t)
		d.result("A", nil)
		pendingQ.wait(t)
		matchText(t, toolText(t, q), `\*\*A:\*\* A`, "answer returned")
		pending, out := startProposal(t, r, proposalParams("Migrate systems A.\nSuccess criteria: all services moved.", nil))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		// The Q&A echo survives draft clearing and appears in the report.
		matchText(t, toolText(t, *out), `Which systems\?`, "question echoed")
		matchText(t, toolText(t, *out), `A`, "answer echoed")
	})

	tw(t, f, "cancel and refine outcomes still carry the durable proposal summary", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal", "Ship a feature")
		// Cancel carries the summary.
		pendingCancel, cancelled := startProposal(t, r, proposalParams("Ship a feature.\nSuccess criteria: tests pass.", nil))
		d.waitOpen(t)
		d.result(cancelAnswer, nil)
		pendingCancel.wait(t)
		matchText(t, toolText(t, *cancelled), `Proposed objective:`, "cancel summary")
		matchText(t, toolText(t, *cancelled), `Draft cancelled`, "cancel text")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("cancel must not create a goal: %d", n)
		}
		// Refine carries the summary and keeps the draft.
		r.Command("goal", "Ship a feature")
		pendingRefine, refined := startProposal(t, r, proposalParams("Ship a feature.\nSuccess criteria: tests pass.", nil))
		d.waitOpen(t)
		d.result(continueAnswer, nil)
		pendingRefine.wait(t)
		for _, p := range []string{`Proposed objective:`, `Automatic continuation: enabled`, `refinement requested`} {
			matchText(t, toolText(t, *refined), p, "refine")
		}
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("refining must not create a goal: %d", n)
		}
	})

	// ── F2 regression: proposals always show the task list exactly once ──

	taskList := func(tasks ...[2]string) map[string]any {
		var list []any
		for _, tk := range tasks {
			list = append(list, map[string]any{"id": tk[0], "title": tk[1], "status": "pending"})
		}
		return map[string]any{"tasks": list, "blockCompletion": false, "proposedAt": "2026-08-05T00:00:00.000Z"}
	}
	currentGoal := func(objective string, tasks ...[2]string) map[string]any {
		tl := taskList(tasks...)
		// "complete" tasks in the original; the preview only lists them.
		return map[string]any{"id": "g1", "objective": objective, "status": "active", "autoContinue": true, "sisyphus": false, "taskList": tl}
	}
	count := func(text, sub string) int { return strings.Count(text, sub) }

	tw(t, f, "a numbered-step objective with no explicit tasks previews the derived task list in the confirmation", func(t *testing.T) {
		// Regression: the F2 preview derived from the boxed confirmation text (every line prefixed with "│   ") so the ordered-marker
		// regex never matched and numbered objectives showed NO task list. Deriving from the raw objective must surface the derived
		// tree in the confirmation.
		objective := "Ship the release.\n1) Implement core\n2) Add tests\n3) Write docs"
		text := goal.ProposalText("goal", "Ship the release", objective, true, nil, nil, false, nil)
		matchText(t, text, `Tasks derived from the objective \(confirm or ask the agent to adjust\):`, "header")
		for _, l := range []string{`\[ \] step-1: Implement core`, `\[ \] step-2: Add tests`, `\[ \] step-3: Write docs`} {
			matchText(t, text, l, "derived line")
		}
		if strings.Contains(text, "Tasks proposed for confirmation") {
			t.Error("no explicit block")
		}
		if count(text, "Tasks derived from the objective") != 1 {
			t.Error("derived preview appears exactly once")
		}
	})

	tw(t, f, "a new-draft proposal with explicit tasks shows them once and never adds a derived block", func(t *testing.T) {
		text := goal.ProposalText("goal", "Build a tool", "Build a tool.\n1) wire it\n2) test it", true, taskList([2]string{"t1", "Explicit task"}), nil, false, nil)
		if n := count(text, "Tasks proposed for confirmation:"); n != 1 {
			t.Errorf("explicit list shown exactly once: %d", n)
		}
		if n := count(text, "Tasks derived from the objective"); n != 0 {
			t.Errorf("no derived block when tasks are explicit: %d", n)
		}
		matchText(t, text, `\[ \] t1: Explicit task`, "explicit task")
	})

	tw(t, f, "a tweak confirmation renders the explicit task list exactly once", func(t *testing.T) {
		// Regression: buildTweakConfirmationText already renders tasks inside its ┌─ TASKS ─┐ box; appending a second "Tasks proposed"
		// block duplicated it.
		current := currentGoal("Old objective.", [2]string{"old", "Old task"})
		text := goal.ProposalText("tweak", "Revise the plan", "New objective.", true, taskList([2]string{"new-1", "New task"}), current, false, nil)
		if n := count(text, "┌─ TASKS ─"); n != 1 {
			t.Errorf("the task box renders exactly once: %d", n)
		}
		if n := count(text, "Tasks proposed for confirmation:"); n != 0 {
			t.Errorf("no duplicated appended list: %d", n)
		}
		matchText(t, text, `\[ \] new-1: New task`, "new task")
		if strings.Contains(text, "Current task list (retained unchanged):") {
			t.Error("explicit tasks replace the retained preview")
		}
	})

	tw(t, f, "a tweak without explicit tasks previews the retained current list exactly once", func(t *testing.T) {
		current := currentGoal("Old objective.", [2]string{"keep", "Retained task"})
		text := goal.ProposalText("tweak", "Clarify wording", "Clarified objective.", true, nil, current, false, nil)
		if n := count(text, "Current task list (retained unchanged):"); n != 1 {
			t.Errorf("retained list shown exactly once: %d", n)
		}
		matchText(t, text, `\[ \] keep: Retained task`, "retained task")
		if n := count(text, "┌─ TASKS ─"); n != 0 {
			t.Errorf("no empty explicit task box: %d", n)
		}
		if strings.Contains(text, "Tasks proposed for confirmation:") || strings.Contains(text, "Tasks derived from the objective") {
			t.Error("no other task block")
		}
	})

	// ── §7.5 regression: a tweak never changes the status of persisting steps ──

	tw(t, f, "a tweak merges the proposed task list by id, preserving statuses of surviving steps", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		// Seed a task list on disk with a completed step, a removable step, and the current task. /goal-tweak reconciles from disk
		// before drafting.
		g := firstGoal(t, r.Cwd)
		g["currentTaskId"] = "ct"
		g["taskList"] = map[string]any{
			"tasks": []any{
				map[string]any{"id": "keep", "title": "Surviving task", "status": "complete", "evidence": "Done it.", "completedAt": "2026-08-05T10:00:00.000Z", "verificationContract": "tests pass"},
				map[string]any{"id": "drop", "title": "Removed task", "status": "pending"},
				map[string]any{"id": "ct", "title": "Current task", "status": "pending"},
			},
			"blockCompletion": false,
			"proposedAt":      "2026-08-05T09:00:00.000Z",
		}
		goal.WriteGoalFor(r.Cwd, g)
		r.Command("goal-tweak", "Revise the scope")
		pending, _ := startProposal(t, r, proposalParams("Revised objective", map[string]any{
			"sisyphus": false,
			"tasks": []any{
				map[string]any{"id": "keep", "title": "Surviving task (retitled)", "verification_contract": "contract v2"},
				map[string]any{"id": "fresh", "title": "Brand new task"},
			},
		}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		after := firstGoal(t, r.Cwd)
		byID := map[string]map[string]any{}
		for _, x := range anySlice(after["taskList"].(map[string]any)["tasks"]) {
			m := x.(map[string]any)
			byID[m["id"].(string)] = m
		}
		keep := byID["keep"]
		if keep["status"] != "complete" {
			t.Errorf("persisting step keeps its status across the tweak: %v", keep["status"])
		}
		if keep["evidence"] != "Done it." {
			t.Errorf("evidence preserved: %v", keep["evidence"])
		}
		if keep["completedAt"] != "2026-08-05T10:00:00.000Z" {
			t.Errorf("completedAt preserved: %v", keep["completedAt"])
		}
		if keep["verificationContract"] != "contract v2" {
			t.Errorf("structural fields (contract) follow the proposal: %v", keep["verificationContract"])
		}
		if keep["title"] != "Surviving task (retitled)" {
			t.Errorf("structural fields follow the proposal: %v", keep["title"])
		}
		if _, has := byID["drop"]; has {
			t.Error("removed step is dropped")
		}
		if _, has := byID["ct"]; has {
			t.Error("removed current task is dropped")
		}
		if byID["fresh"]["status"] != "pending" {
			t.Errorf("new step starts pending: %v", byID["fresh"]["status"])
		}
		if _, has := after["currentTaskId"]; has {
			t.Errorf("currentTaskId cleared when its task no longer survives pending: %v", after["currentTaskId"])
		}
		sets := eventsOfType(r.Cwd, "task_list_set")
		if len(sets) == 0 || sets[len(sets)-1]["taskCount"] != float64(2) {
			t.Errorf("ledger records the merged task count: %v", sets)
		}
	})

	tw(t, f, "a tweak with no task list retains the current list and keeps its statuses", func(t *testing.T) {
		r, d := draftRig(t, draftOpts{hasUI: true})
		startDraftSession(r)
		r.Command("goal-direct", "Initial objective")
		g := firstGoal(t, r.Cwd)
		g["currentTaskId"] = "do"
		g["taskList"] = map[string]any{
			"tasks": []any{
				map[string]any{"id": "do", "title": "In progress", "status": "pending"},
				map[string]any{"id": "done", "title": "Already done", "status": "complete", "evidence": "Shipped.", "completedAt": "2026-08-05T08:00:00.000Z"},
			},
			"blockCompletion": false,
			"proposedAt":      "2026-08-05T09:00:00.000Z",
		}
		goal.WriteGoalFor(r.Cwd, g)
		r.Command("goal-tweak", "Clarify the wording")
		// No tasks in the proposal: the current list is retained unchanged.
		pending, _ := startProposal(t, r, proposalParams("Clarified objective", map[string]any{"sisyphus": false}))
		d.waitOpen(t)
		d.result(confirmAnswer, nil)
		pending.wait(t)
		after := firstGoal(t, r.Cwd)
		byID := map[string]map[string]any{}
		for _, x := range anySlice(after["taskList"].(map[string]any)["tasks"]) {
			m := x.(map[string]any)
			byID[m["id"].(string)] = m
		}
		if byID["do"]["status"] != "pending" {
			t.Error("pending task untouched")
		}
		if byID["done"]["status"] != "complete" {
			t.Error("completed task untouched")
		}
		if after["currentTaskId"] != "do" {
			t.Errorf("currentTaskId retained when its task is still pending: %v", after["currentTaskId"])
		}
		if n := len(eventsOfType(r.Cwd, "task_list_set")); n != 0 {
			t.Errorf("no task_list_set event when the list is retained: %d", n)
		}
	})

	tw(t, f, "all drafting tools preserve the draft when dialogs are ${unavailable ? \"unavailable\" : \"failing\"}", func(t *testing.T) {
		t.Run("failing", func(t *testing.T) {
			r, _ := draftRig(t, draftOpts{hasUI: true})
			// The host's ui.custom fails ("Host disconnected"); the original's ctx.ui.select is removed too, which the SDK host does
			// not model (it always answers select), so the select-based fallback is never reached here.
			prev := r.Host.opts.OnCallValue
			r.Host.opts.OnCallValue = func(method string, args map[string]any) (any, string) {
				if method == "ui.custom" {
					return nil, "Host disconnected"
				}
				return prev(method, args)
			}
			startDraftSession(r)
			r.Command("goal", "Ship a tested feature")
			for _, c := range []struct {
				name   string
				params map[string]any
			}{
				{"goal_question", map[string]any{"question": "Scope?", "options": []any{"A"}}},
				{"goal_questionnaire", map[string]any{"questions": []any{map[string]any{"id": "scope", "question": "Scope?", "options": []any{"A"}}}}},
				{"propose_goal_draft", proposalParams("Ship a tested feature. Success criteria: tests pass.", nil)},
			} {
				out, failure := r.Tool(c.name, c.params)
				if failure != "" {
					t.Fatalf("%s: %s", c.name, failure)
				}
				text := toolText(t, out)
				matchText(t, text, `Host disconnected`, c.name)
				if regexpMatch(`user cancelled|refinement requested`, text) {
					t.Errorf("%s: %q", c.name, text)
				}
				if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
					t.Errorf("%s created a goal", c.name)
				}
				if !contains(r.ActiveTools(), "propose_goal_draft") {
					t.Errorf("%s dropped the draft", c.name)
				}
			}
		})
		t.Run("unavailable", func(t *testing.T) {
			t.Skip("a host without select/input dialogs cannot exist behind the SDK: every Go host answers ui.select and ui.input, so the " +
				"'cannot display the required dialogs' result is unreachable end to end (the questionnaire module twins pin it on a context without select/input)")
		})
	})
}

