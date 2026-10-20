package pi_goal_x_test

// Twins of tests/goal-tool-visibility.test.ts: lifecycle-specific tool profiles preserve host tools and executor guards.

import (
	"sort"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const gtvFile = "goal-tool-visibility"

var (
	gtvFive         = []string{"create_goal", "get_goal", "update_goal", "set_goal_tasks", "update_goal_task"}
	gtvCore         = []string{"create_goal", "get_goal", "update_goal"}
	gtvBase         = []string{"create_goal", "get_goal"}
	gtvActiveNoTask = []string{"create_goal", "get_goal", "update_goal", "set_goal_tasks"}
	gtvHostA        = []string{"read", "bash", "edit", "write"}
	gtvHostB        = []string{"read", "grep", "find", "ls", "fetch", "custom-ext-tool"}
	gtvRemoved      = []string{"complete_goal", "pause_goal", "abort_goal", "propose_goal_tweak", "step_complete", "propose_task_list", "complete_task", "skip_task"}
)

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func gtvExpectGoalProfile(t *testing.T, r *goalRig, expected []string) {
	t.Helper()
	active := r.ActiveTools()
	for _, tool := range expected {
		if !inList(active, tool) {
			t.Errorf("goal tool %q must be advertised. Active: %v", tool, active)
		}
	}
	for _, removed := range gtvRemoved {
		if inList(active, removed) {
			t.Errorf("removed tool %q must never be advertised. Active: %v", removed, active)
		}
	}
	// Exactly the expected goal tools: no extras, no missing.
	known := append(append(append([]string{}, gtvFive...), "goal_question", "goal_questionnaire", "propose_goal_draft"), gtvRemoved...)
	var goalTools []string
	for _, a := range active {
		if inList(known, a) {
			goalTools = append(goalTools, a)
		}
	}
	if len(goalTools) != len(expected) {
		t.Errorf("goal profile must be exactly [%s], got: %v", strings.Join(expected, ", "), goalTools)
	}
}

func gtvExpectHostUntouched(t *testing.T, r *goalRig, seed []string) {
	t.Helper()
	var host []string
	for _, a := range r.ActiveTools() {
		if !inList(gtvFive, a) {
			host = append(host, a)
		}
	}
	sort.Strings(host)
	want := append([]string{}, seed...)
	sort.Strings(want)
	equal(t, host, want, "host tool selection must be untouched")
}

// gtvFixture is testFixture(): one active goal in a fresh cwd; entries focus it and carry the legacy state entry.
func gtvFixture(t *testing.T) toolsFixture {
	f := newToolsFixture(t, "Tool visibility test", false, goalAt(2026, time.June, 26, 9, 0, 0), "")
	f.entries = []any{goal.ToolsFocusEntry(f.goal["id"].(string)), goal.ToolsStateEntry(f.goal)}
	return f
}

func gtvRig(t *testing.T, f toolsFixture, entries []any, seed []string, settings string) *goalRig {
	f.entries = entries
	opts := rigOptions{ActiveTools: seed}
	if settings != "" {
		opts.Settings = settings
	}
	return newToolsRig(t, f, opts)
}

func gtvSession(r *goalRig) { r.Fire("session_start", map[string]any{"reason": "start"}) }

func TestGoalToolVisibility(t *testing.T) {
	tw(t, gtvFile, "active goal without tasks advertises four operations and host tools are untouched", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostA, "")
		gtvSession(r)
		gtvExpectGoalProfile(t, r, gtvActiveNoTask)
		gtvExpectHostUntouched(t, r, gtvHostA)
	})

	tw(t, gtvFile, "no-focus session advertises creation and inspection", func(t *testing.T) {
		f := newEmptyToolsFixture(t)
		r := gtvRig(t, f, []any{}, gtvHostA, "")
		gtvSession(r)
		// No goal: creation and inspection remain available.
		gtvExpectGoalProfile(t, r, gtvBase)
		gtvExpectHostUntouched(t, r, gtvHostA)
	})

	tw(t, gtvFile, "non-active states advertise only valid operations", func(t *testing.T) {
		for _, status := range []string{"paused", "blocked", "budget_limited", "complete"} {
			f := gtvFixture(t)
			overrides := `{"status":"` + status + `"`
			if status == "complete" {
				overrides += `,"autoContinue":false`
			}
			if status == "paused" || status == "blocked" {
				overrides += `,"stopReason":"agent","pauseReason":"Testing"`
			}
			second := goal.ToolsGoal(f.cwd, "Status: "+status, false, goalAt(2026, time.June, 26, 10, 0, 0), overrides+"}")
			entries := []any{goal.ToolsFocusEntry(second["id"].(string)), goal.ToolsStateEntry(second)}
			r := gtvRig(t, f, entries, gtvHostA, "")
			gtvSession(r)
			want := gtvBase
			switch status {
			case "paused":
				want = gtvActiveNoTask
			case "budget_limited":
				want = gtvCore
			}
			gtvExpectGoalProfile(t, r, want)
			gtvExpectHostUntouched(t, r, gtvHostA)
		}
	})

	tw(t, gtvFile, "tasks disabled: task tools remain hidden across states", func(t *testing.T) {
		for _, status := range []string{"active", "paused", "complete"} {
			f := newEmptyToolsFixture(t)
			overrides := `{}`
			if status != "active" {
				overrides = `{"status":"` + status + `"}`
			}
			g := goal.ToolsGoal(f.cwd, "No tasks "+status, false, goalAt(2026, time.June, 26, 11, 0, 0), overrides)
			entries := []any{goal.ToolsFocusEntry(g["id"].(string)), goal.ToolsStateEntry(g)}
			r := gtvRig(t, f, entries, gtvHostB, `{"disableTasks":true}`)
			gtvSession(r)
			want := gtvCore
			if status == "complete" {
				want = gtvBase
			}
			gtvExpectGoalProfile(t, r, want)
			gtvExpectHostUntouched(t, r, gtvHostB)
		}
	})

	tw(t, gtvFile, "turn_start / before_agent_start / turn_end cycles leave profile and host unchanged", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostB, "")
		gtvSession(r)
		gtvExpectGoalProfile(t, r, gtvActiveNoTask)
		gtvExpectHostUntouched(t, r, gtvHostB)
		for _, event := range []string{"turn_start", "before_agent_start", "turn_end"} {
			if !r.Host.Registered(event) {
				t.Fatalf("%s handler must be registered", event)
			}
		}
		for i := 0; i < 3; i++ {
			r.Fire("turn_start", map[string]any{})
			r.Fire("before_agent_start", map[string]any{"systemPrompt": "", "prompt": "turn-" + string(rune('0'+i)), "systemPromptOptions": map[string]any{}})
			r.turnEnd()
			gtvExpectGoalProfile(t, r, gtvActiveNoTask)
			gtvExpectHostUntouched(t, r, gtvHostB)
		}
	})

	tw(t, gtvFile, "blocking removes mutation tools", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostA, "")
		gtvSession(r)
		r.Fire("before_agent_start", map[string]any{"systemPrompt": "", "prompt": "start", "systemPromptOptions": map[string]any{}})
		// update_goal(blocked) transitions active -> blocked; only creation/inspection remain.
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "test blocker"})
		if !out.terminated() {
			t.Error("blocked terminates the turn")
		}
		gtvExpectGoalProfile(t, r, gtvBase)
		gtvExpectHostUntouched(t, r, gtvHostA)
	})

	tw(t, gtvFile, "update_goal(blocked) from a paused goal is rejected with a state-aware message while tools stay", func(t *testing.T) {
		f := gtvFixture(t)
		// Pause the goal on disk, then reload the session.
		paused := goal.ToolsGoal(f.cwd, "Paused goal", false, goalAt(2026, time.June, 26, 12, 0, 0),
			`{"autoContinue":false,"status":"paused","stopReason":"agent","pauseReason":"waiting on user"}`)
		entries := []any{goal.ToolsFocusEntry(paused["id"].(string)), goal.ToolsStateEntry(paused)}
		r := gtvRig(t, f, entries, gtvHostA, "")
		gtvSession(r)
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "test blocker"})
		textContains(t, out.Content, "applies only to an active goal", "blocked from paused must be a state-aware failure")
		// The paused profile retains outcome and task-planning tools after rejection.
		gtvExpectGoalProfile(t, r, gtvActiveNoTask)
		gtvExpectHostUntouched(t, r, gtvHostA)
	})

	tw(t, gtvFile, "execution and transient drafting tools are registered with execute handlers", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostA, "")
		for _, name := range gtvFive {
			if !r.Host.tools[name] {
				t.Errorf("Tool %q must be registered", name)
			}
		}
		for _, name := range []string{"goal_question", "goal_questionnaire", "propose_goal_draft"} {
			if !r.Host.tools[name] {
				t.Errorf("Drafting tool %q must be registered", name)
			}
		}
		for _, removed := range gtvRemoved {
			if r.Host.tools[removed] {
				t.Errorf("%s must not be registered", removed)
			}
		}
	})

	tw(t, gtvFile, "tool_call handler is registered", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostA, "")
		if !r.Host.Registered("tool_call") {
			t.Error("tool_call handler must be registered")
		}
	})

	tw(t, gtvFile, "escape dialog handler paths are wired", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostA, "")
		if !r.Host.Registered("tool_call") {
			t.Error("tool_call handler must exist for escape dialog path")
		}
		if !r.Host.tools["update_goal"] {
			t.Error("update_goal tool must be registered")
		}
	})

	tw(t, gtvFile, "update_goal_task executes and the five-tool profile stays fixed", func(t *testing.T) {
		f := gtvFixture(t)
		now := nowZ()
		goalWithTasks := goal.ToolsGoal(f.cwd, "Tasks: complete them", false, goalAt(2026, time.July, 7, 14, 0, 0),
			`{"taskList":{"tasks":[{"id":"t1","title":"Task one","status":"pending"},{"id":"t2","title":"Task two","status":"pending"}],"blockCompletion":false,"proposedAt":"`+now+`"},"updatedAt":"`+now+`"}`)
		entries := []any{goal.ToolsFocusEntry(goalWithTasks["id"].(string)), goal.ToolsStateEntry(goalWithTasks)}
		r := gtvRig(t, f, entries, gtvHostA, "")
		gtvSession(r)
		r.Fire("before_agent_start", map[string]any{"systemPrompt": "", "prompt": "start", "systemPromptOptions": map[string]any{}})
		out := r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "Done"})
		if !strings.Contains(out.Content, "t1 complete") && !strings.Contains(out.Content, "1/2") {
			t.Errorf("update_goal_task should report t1 complete. Got: %s", out.Content)
		}
		// After executing update_goal_task, the profile and host set are unchanged.
		gtvExpectGoalProfile(t, r, gtvFive)
		gtvExpectHostUntouched(t, r, gtvHostA)
	})

	tw(t, gtvFile, "arbitrary host tool sets survive session, turn, and tool events untouched", func(t *testing.T) {
		f := gtvFixture(t)
		r := gtvRig(t, f, f.entries, gtvHostB, "")
		gtvSession(r)
		r.Fire("turn_start", map[string]any{})
		r.Fire("before_agent_start", map[string]any{"systemPrompt": "", "prompt": "seed", "systemPromptOptions": map[string]any{}})
		r.callTool("get_goal", map[string]any{})
		gtvExpectGoalProfile(t, r, gtvActiveNoTask)
		gtvExpectHostUntouched(t, r, gtvHostB)
	})
}
