package pi_goal_x

import (
	"os"
	"strings"
	"testing"
)

// Twins of tests/task-list-overlay.test.ts.

func overlayMakeGoal(id, objective string, kv ...any) *jsObject {
	g := wgObj("status", "active", "autoContinue", true, "usage", wgObj("tokensUsed", 0.0, "activeSeconds", 0.0), "sisyphus", false,
		"createdAt", "2026-01-01T00:00:00.000Z", "updatedAt", "2026-01-01T00:00:00.000Z")
	g = with(g, "id", id, "objective", objective)
	return with(g, kv...)
}

func overlayGoalWithTasks(id, objective string, titles ...string) *jsObject {
	tasks := []any{}
	for i, title := range titles {
		tasks = append(tasks, wgTask(id+"-task-"+itoa(i), title, "pending"))
	}
	return overlayMakeGoal(id, objective, "taskList", wgTaskList(tasks...))
}

func itoa(i int) string { return jsNumber(float64(i)) }

func overlayGoalWithMixedTasks(id, objective string) *jsObject {
	return overlayMakeGoal(id, objective, "taskList", wgTaskList(
		wgTask(id+"-t1", "Do thing one", "complete"),
		wgTask(id+"-t2", "Do thing two", "pending"),
		wgTask(id+"-t3", "Do thing three", "skipped"),
	))
}

// overlayFixture runs showTaskListOverlay (it blocks until done) and returns the created component.
func overlayFixture(t *testing.T, pool *goalPool, focused *string) goalComponent {
	t.Helper()
	ctx := createMockExtensionContext(t)
	ctx.startCustom(func() { showTaskListOverlay(ctx, pool, focused, taskListOverlayOptions{}) })
	component, _, _ := invokeCustomFactory(t, ctx.customCalls(), 0)
	return component
}

func sp(s string) *string { return &s }

func TestTaskListOverlay(t *testing.T) {
	f := "task-list-overlay"
	has := func(t *testing.T, joined, want, msg string) {
		t.Helper()
		if !strings.Contains(joined, want) {
			t.Errorf("%s: %q not in output", msg, want)
		}
	}
	hasNot := func(t *testing.T, joined, want, msg string) {
		t.Helper()
		if strings.Contains(joined, want) {
			t.Errorf("%s: %q found in output", msg, want)
		}
	}
	tw(t, f, "defaults to current goal only", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "Focused goal", "Task A"), overlayGoalWithTasks("g2", "Other goal", "Task B")), sp("g1"))
		joined := strings.Join(renderComponent(c, 80), "\n")
		has(t, joined, "Focused goal", "shows focused goal")
		hasNot(t, joined, "Other goal", "does not show other goal")
		has(t, joined, "current goal", "header says 'current goal'")
	})
	tw(t, f, "toggles to all open goals with 'a'", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "Goal one", "Task A"), overlayGoalWithTasks("g2", "Goal two", "Task B")), sp("g1"))
		in := c.(goalInputComponent)
		in.HandleInput("a")
		all := strings.Join(renderComponent(c, 80), "\n")
		has(t, all, "Goal one", "shows first goal after toggle")
		has(t, all, "Goal two", "shows second goal after toggle")
		has(t, all, "2 goals", "header says '2 goals' after toggle")
		in.HandleInput("a")
		back := strings.Join(renderComponent(c, 80), "\n")
		has(t, back, "Goal one", "shows focused goal after toggle back")
		hasNot(t, back, "Goal two", "hides other goal after toggle back")
		has(t, back, "current goal", "header says 'current goal' after toggle back")
	})
	tw(t, f, "renders without crashing with empty goals", func(t *testing.T) {
		c := overlayFixture(t, poolOf(), nil)
		has(t, strings.Join(renderComponent(c, 80), "\n"), "No open goals", "shows empty message")
	})
	tw(t, f, "renders a single goal with tasks", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "Build the thing", "Design", "Implement", "Test")), sp("g1"))
		joined := strings.Join(renderComponent(c, 80), "\n")
		for _, w := range []string{"Build the thing", "Design", "Implement", "Test"} {
			has(t, joined, w, "shows "+w)
		}
	})
	tw(t, f, "renders a paused goal without tasks", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayMakeGoal("g2", "Research topic", "status", "paused")), sp("g2"))
		joined := strings.Join(renderComponent(c, 80), "\n")
		has(t, joined, "Research topic", "shows paused goal title")
		has(t, joined, "no tasks", "shows no tasks indicator")
	})
	tw(t, f, "renders mixed task statuses", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithMixedTasks("g3", "A goal with mixed tasks")), sp("g3"))
		joined := strings.Join(renderComponent(c, 80), "\n")
		for _, w := range []string{"1/3 done", "Do thing one", "Do thing two", "Do thing three"} {
			has(t, joined, w, "shows "+w)
		}
	})
	tw(t, f, "renders multiple goals when toggled to all", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "First goal", "Task A", "Task B"), overlayGoalWithTasks("g2", "Second goal", "Task C")), sp("g1"))
		c.(goalInputComponent).HandleInput("a")
		joined := strings.Join(renderComponent(c, 80), "\n")
		has(t, joined, "2 goals", "header mentions 2 goals")
		has(t, joined, "First goal", "shows first goal")
		has(t, joined, "Second goal", "shows second goal")
	})
	tw(t, f, "renders at narrow width without crashing", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "Some goal", "Task X")), sp("g1"))
		for _, w := range []int{40, 60, 120} {
			if len(renderComponent(c, w)) == 0 {
				t.Errorf("renders at width %d", w)
			}
		}
	})
	tw(t, f, "handles sisyphus goal", func(t *testing.T) {
		g := overlayMakeGoal("g4", "Sisyphus goal", "sisyphus", true, "autoContinue", false,
			"taskList", wgTaskList(wgTask("g4-t1", "Step one", "pending")))
		c := overlayFixture(t, poolOf(g), sp("g4"))
		joined := strings.Join(renderComponent(c, 80), "\n")
		has(t, joined, "Sisyphus goal", "shows sisyphus goal title")
		has(t, joined, "Step one", "shows step")
	})
	tw(t, f, "renders subtasks", func(t *testing.T) {
		g := overlayMakeGoal("g5", "Goal with subtasks", "taskList", wgTaskList(
			wgTask("g5-t1", "Parent task", "pending", "subtasks", []any{
				wgTask("g5-s1", "Subtask A", "complete"),
				wgTask("g5-s2", "Subtask B", "pending"),
			})))
		c := overlayFixture(t, poolOf(g), sp("g5"))
		joined := strings.Join(renderComponent(c, 80), "\n")
		has(t, joined, "Parent task", "shows parent task")
		has(t, joined, "Subtask A", "shows subtask A")
		has(t, joined, "Subtask B", "shows subtask B")
	})
	tw(t, f, "wraps long task titles at narrow width", func(t *testing.T) {
		g := overlayGoalWithTasks("g1", "Goal", "This is a very long task title that should definitely wrap at narrow terminal widths")
		c := overlayFixture(t, poolOf(g), sp("g1"))
		joined := strings.Join(renderComponent(c, 50), "\n")
		has(t, joined, "very long task title", "long title appears via wrapping")
	})
	tw(t, f, "scroll state changes on page up/down", func(t *testing.T) {
		titles := []string{}
		for i := 1; i <= 30; i++ {
			titles = append(titles, "Task "+itoa(i))
		}
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "Long goal", titles...)), sp("g1"))
		in := c.(goalInputComponent)
		has(t, strings.Join(renderComponent(c, 80), "\n"), "Task 1", "renders top of list at offset 0")
		in.HandleInput("down")
		joined1 := strings.Join(renderComponent(c, 80), "\n")
		if strings.Contains(joined1, "Task 1") && !strings.Contains(joined1, "Task 2") {
			t.Error("content changes after scrolling down once")
		}
		for _, k := range []string{"up", "pagedown", "home", "end", "home", "j", "k"} {
			in.HandleInput(k)
			renderComponent(c, 80)
		}
	})
	tw(t, f, "dismisses on escape and enter", func(t *testing.T) {
		c := overlayFixture(t, poolOf(overlayGoalWithTasks("g1", "Test goal", "Task A")), sp("g1"))
		in := c.(goalInputComponent)
		in.HandleInput("escape")
		in.HandleInput("enter")
	})
	tw(t, f, "configured task shortcut toggles the unified dashboard expansion (overlay registration removed)", func(t *testing.T) {
		// The TS test reads goal-widget.ts; the Go twin reads goal_widget_keys.go, where the same handler lives.
		src, err := os.ReadFile("goal_widget_keys.go")
		if err != nil {
			t.Fatal(err)
		}
		goalSource := string(src)
		if !strings.Contains(goalSource, "pitui.MatchesKey(data, keybindings.toggleExpand)") {
			t.Error("goal_widget_keys.go reads the configured dashboard keybinding")
		}
		if !strings.Contains(goalSource, "core.toggleDashboardExpanded()") {
			t.Error("the configured shortcut toggles the unified dashboard (overlay registration removed)")
		}
		if strings.Contains(goalSource, "showTaskListOverlay(") {
			t.Error("the separate task-list overlay is no longer registered from the keybinding")
		}
		if !strings.Contains(goalSource, "return consume") {
			t.Error("handler consumes the keyboard event")
		}
	})
}
