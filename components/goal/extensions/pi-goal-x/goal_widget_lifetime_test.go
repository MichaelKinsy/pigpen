package pi_goal_x

import (
	"strings"
	"testing"
)

const gwlF = "goal-widget-lifetime"

func gwlWrite(t *testing.T, cwd string, goal *jsObject) *jsObject {
	t.Helper()
	written, err := storage{cwd: cwd}.writeActiveGoalFile(goal)
	if err != nil {
		t.Fatal(err)
	}
	return written
}

func TestGoalWidgetLifetimeTwins(t *testing.T) {
	tw(t, gwlF, "deferred widget render survives stale context (focused=${focused})", func(t *testing.T) {
		for _, focused := range []bool{false, true} {
			t.Run(map[bool]string{false: "focused=false", true: "focused=true"}[focused], func(t *testing.T) {
				t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
				cwd := t.TempDir()
				goal := gwlWrite(t, cwd, createGoal("Keep working", true, false, false, clockMs()))
				h := newKernelHost(cwd, "test")
				if focused {
					h.branch = []any{focusEntryFor(gstr(goal, "id"))}
				}
				core := newCore(h)
				core.loadState()
				core.updateUI()
				core.flush()
				eq(t, len(h.pushes()) > 0, true, "the widget was registered")
				component := core.widgetRef.current
				eq(t, component != nil, true, "the factory exposed the live component")
				core.updateUI()
				core.clearGoalWidget()
				h.stale = true
				core.flush() // the deferred render the cleared widget cancelled
				core.toggleDashboardExpanded()
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("render with a stale context: %v", r)
						}
					}()
					component.Render(100)
				}()
				h.stale = false

				nextCwd := t.TempDir()
				nextGoal := gwlWrite(t, nextCwd, createGoal("Replacement objective", true, false, false, clockMs()))
				next := newKernelHost(nextCwd, "replacement")
				next.branch = []any{focusEntryFor(gstr(nextGoal, "id"))}
				core.h, core.st = next, storage{cwd: nextCwd}
				core.loadState()
				core.updateUI()
				core.flush()
				eq(t, len(next.pushes()) > 0, true, "replacement re-registers the widget")
				eq(t, gstr(core.state(), "id"), gstr(nextGoal, "id"), "the replacement goal is focused")
				last := next.pushes()[len(next.pushes())-1]
				if !strings.Contains(strings.Join(last.lines, "\n"), "Replacement objective") {
					t.Errorf("replacement widget lines %q", last.lines)
				}
			})
		}
	})

	tw(t, gwlF, "registered dashboard widget receives scroll keys and releases its reference", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
		cwd := t.TempDir()
		goal := createGoal("Scroll tasks", false, false, false, clockMs())
		var tasks []any
		for i := range 30 {
			task := newObject()
			task.set("id", "task-"+jsNumber(float64(i)))
			task.set("title", "Task "+jsNumber(float64(i)))
			task.set("status", "pending")
			tasks = append(tasks, task)
		}
		tl := newObject()
		tl.set("tasks", tasks)
		tl.set("blockCompletion", false)
		tl.set("proposedAt", gstr(goal, "createdAt"))
		goal.set("taskList", tl)
		goal = gwlWrite(t, cwd, goal)

		h := newKernelHost(cwd, "scroll-test")
		h.branch = []any{focusEntryFor(gstr(goal, "id"))}
		core := newCore(h)
		core.loadState()
		core.updateUI()
		core.flush()
		component := core.widgetRef.current
		eq(t, component != nil, true, "the widget factory exposes the live component to the input handler")
		component.Render(100)
		syncTerminalInputPause(newCoreWidgetCore(core), hostDialogCtx{h})
		onInput := h.lastInput(t)
		consumed := func(data string) bool { r := onInput(data); return r != nil && r.consume }
		render := func() string { return strings.Join(component.Render(100), "\n") }

		eq(t, consumed("\x1b[1;6B"), true, "ctrl+shift+down scrolls")
		eq(t, strings.Contains(render(), "↑ 1 more task"), true, "scrolled one task")
		eq(t, consumed("\x1b[116;6u"), true, "ctrl+shift+t toggles")
		eq(t, core.isDashboardExpanded(), true, "expanded")
		eq(t, consumed("\x1b[B"), true, "plain down scrolls the expanded dashboard")
		eq(t, strings.Contains(render(), "↑ 1 more task"), true, "expanded scrolled")
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: cwd, mutation: settingsMutation{op: "set", path: []string{"keybindings", "dashboard", "toggleExpand"}, value: "ctrl+alt+t"}}); err != nil {
			t.Fatal(err)
		}
		eq(t, consumed("\x1b[116;6u"), false, "the previous binding must stop toggling after a settings edit")
		eq(t, consumed("\x1b[116;7u"), true, "the new binding must work without restarting the session")
		eq(t, core.isDashboardExpanded(), false, "collapsed again")

		core.clearGoalWidget()
		eq(t, core.widgetRef.current == nil, true, "the widget reference is released")
		eq(t, consumed("\x1b[1;6B"), false, "no widget, no scroll")
	})
}
