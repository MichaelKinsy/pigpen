package rpiv_todo

import (
	"fmt"
	"strings"
	"testing"
)

const (
	fRender    = "todo-overlay.render"
	fLifecycle = "todo-overlay.lifecycle"
)

// identityTheme returns text unstyled (upstream test: todo-overlay.render.test.ts:20-25).
type identityTheme struct{}

func (identityTheme) Fg(_, text string) string         { return text }
func (identityTheme) Bold(text string) string          { return text }
func (identityTheme) Strikethrough(text string) string { return text }

// sinkCall is one push to the widget; Lines nil is a clear.
type sinkCall struct {
	Key   string
	Lines []string
}

type fakeSink struct{ calls []sinkCall }

func (f *fakeSink) SetWidgetLines(key string, lines []string) error {
	f.calls = append(f.calls, sinkCall{key, lines})
	return nil
}

func (f *fakeSink) last() sinkCall { return at(f.calls, len(f.calls)-1) }

type act = params

// runActions applies tool actions to the store like the tool's execute does (todo-overlay.render.test.ts:27-47).
func runActions(st *store, session string, actions ...act) {
	for _, a := range actions {
		action, _ := a["action"].(string)
		r := applyTaskMutation(st.getState(session), action, a)
		st.commitState(session, r.State)
	}
}

type overlayRig struct {
	st   *store
	ov   *overlay
	sink *fakeSink
	env  viewEnv
}

func newRig(actions ...act) *overlayRig {
	st := newStore()
	st.setActiveRenderSession("test-session")
	runActions(st, "test-session", actions...)
	ov := newOverlay(st, 0)
	sink := &fakeSink{}
	r := &overlayRig{st: st, ov: ov, sink: sink, env: viewEnv{sink: sink, theme: identityTheme{}, width: 200}}
	ov.bind()
	ov.update(r.env)
	return r
}

func (r *overlayRig) render() []string { return r.ov.render(r.env) }

func creates(prefix string, from, to int) []act {
	var out []act
	for i := from; i <= to; i++ {
		out = append(out, act{"action": "create", "subject": fmt.Sprintf("%s%d", prefix, i)})
	}
	return out
}

func completes(prefix string, from, to int) []act {
	var out []act
	for i := from; i <= to; i++ {
		out = append(out, act{"action": "create", "subject": fmt.Sprintf("%s%d", prefix, i)}, act{"action": "update", "id": float64(i), "status": "completed"})
	}
	return out
}

func join(lines []string) string { return strings.Join(lines, "\n") }

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

func notContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("%q contains %q", s, sub)
	}
}

func TestOverlayRenderHeading(t *testing.T) {
	tw(t, fRender, "includes 'Todos (completed/total)' count", func(t *testing.T) {
		r := newRig(append(creates("t", 1, 3), act{"action": "update", "id": 1.0, "status": "completed"})...)
		contains(t, at(r.render(), 0), "Todos (1/3)")
	})
	tw(t, fRender, "uses filled icon '●' when any task is active (pending/in_progress)", func(t *testing.T) {
		contains(t, at(newRig(creates("t", 1, 1)...).render(), 0), "●")
	})
	tw(t, fRender, "uses hollow icon '○' when all tasks are completed", func(t *testing.T) {
		contains(t, at(newRig(completes("t", 1, 1)...).render(), 0), "○")
	})
	tw(t, fRender, "renders one line per visible task plus heading, last row uses '└─'", func(t *testing.T) {
		lines := newRig(creates("t", 1, 3)...).render()
		eq(t, len(lines), 5, "heading + 3 tasks + trailing spacer")
		contains(t, at(lines, 1), "├─")
		contains(t, at(lines, 3), "└─")
		eq(t, at(lines, 4), "", "spacer")
	})
	tw(t, fRender, "omits deleted tasks from the rendered output", func(t *testing.T) {
		r := newRig(append(creates("t", 1, 2), act{"action": "delete", "id": 1.0})...)
		out := join(r.render())
		notContains(t, out, "t1")
		contains(t, out, "t2")
	})
}

func TestOverlayRenderRows(t *testing.T) {
	tw(t, fRender, "pending task uses '○' glyph", func(t *testing.T) {
		contains(t, at(newRig(creates("t", 1, 1)...).render(), 1), "○")
	})
	tw(t, fRender, "in_progress task uses '◐' glyph and appends (activeForm)", func(t *testing.T) {
		line := at(newRig(act{"action": "create", "subject": "do it", "activeForm": "Doing it"}, act{"action": "update", "id": 1.0, "status": "in_progress"}).render(), 1)
		contains(t, line, "◐")
		contains(t, line, "do it")
		contains(t, line, "(Doing it)")
	})
	tw(t, fRender, "completed task stays visible until the next agent turn starts", func(t *testing.T) {
		r := newRig(completes("done", 1, 1)...)
		first := r.render()
		contains(t, at(first, 1), "✓")
		contains(t, at(first, 1), "done")
		contains(t, at(r.render(), 1), "done")
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		eq(t, len(r.render()), 0, "render after the next turn starts")
	})
	tw(t, fRender, "does NOT show #id prefix when no task has blockedBy", func(t *testing.T) {
		out := join(newRig(creates("t", 1, 2)...).render())
		for _, id := range []string{"#1", "#2"} {
			notContains(t, out, id)
		}
	})
	tw(t, fRender, "shows #id prefix and '⛓' dep suffix when any task has blockedBy", func(t *testing.T) {
		out := join(newRig(act{"action": "create", "subject": "base"}, act{"action": "create", "subject": "follow-up", "blockedBy": []any{1.0}}).render())
		contains(t, out, "#1")
		contains(t, out, "#2")
		contains(t, out, "⛓")
	})
}

func TestOverlayRenderOverflow(t *testing.T) {
	tw(t, fRender, "drops completed first when dropping is enough", func(t *testing.T) {
		// 12 total = 8 pending + 4 completed. budget=10: all pending fit plus 2 of the 4 completed.
		r := newRig(append(creates("p", 1, 8), completes("c", 9, 12)...)...)
		lines := r.render()
		eq(t, len(lines), 13, "heading + 10 visible + 1 summary + trailing spacer")
		out := join(lines)
		for i := 1; i <= 8; i++ {
			contains(t, out, fmt.Sprintf("p%d", i))
		}
		contains(t, out, "c9")
		contains(t, out, "c10")
		notContains(t, out, "c11")
		contains(t, at(lines, len(lines)-2), "+2 more")
		contains(t, at(lines, len(lines)-2), "2 completed")
	})
	tw(t, fRender, "truncates pending tail when dropping all completed isn't enough", func(t *testing.T) {
		lines := newRig(creates("t", 1, 12)...).render()
		eq(t, len(lines), 13, "lines")
		eq(t, at(lines, len(lines)-1), "", "spacer")
		summary := at(lines, len(lines)-2)
		contains(t, summary, "+2 more")
		contains(t, summary, "2 pending")
		notContains(t, summary, "completed")
	})
	tw(t, fRender, "summary contains both 'completed' and 'pending' when mixed overflow", func(t *testing.T) {
		r := newRig(append(creates("p", 1, 12), completes("c", 13, 15)...)...)
		lines := r.render()
		summary := at(lines, len(lines)-2)
		contains(t, summary, "+5 more")
		contains(t, summary, "3 completed")
		contains(t, summary, "2 pending")
	})
	tw(t, fRender, "hides overflowed completed tasks on the next agent turn too", func(t *testing.T) {
		r := newRig(append(creates("p", 1, 11), completes("c", 12, 16)...)...)
		before := join(r.render())
		contains(t, before, "Todos (5/16)")
		contains(t, before, "+6 more")
		contains(t, before, "5 completed")
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		after := join(r.render())
		contains(t, after, "Todos (0/11)")
		notContains(t, after, "completed")
		notContains(t, after, " more")
	})
	tw(t, fRender, "does not engage overflow at exactly 11 visible tasks", func(t *testing.T) {
		lines := newRig(creates("t", 1, 11)...).render()
		eq(t, len(lines), 13, "lines")
		eq(t, at(lines, len(lines)-1), "", "spacer")
		notContains(t, at(lines, len(lines)-2), "+")
		contains(t, at(lines, len(lines)-2), "└─")
	})
	tw(t, fRender, "follows Pi's tool-output expansion mode and renders every task", func(t *testing.T) {
		r := newRig(creates("t", 1, 17)...)
		collapsed := join(r.render())
		contains(t, collapsed, "+7 more")
		notContains(t, collapsed, "t17")
		r.env.toolsExpanded = true
		expanded := r.render()
		eq(t, len(expanded), 19, "heading + 17 tasks + trailing spacer")
		contains(t, join(expanded), "t17")
		notContains(t, join(expanded), " more")
	})
	tw(t, fRender, "keeps the configured budget when the host has no expansion-state API", func(t *testing.T) {
		contains(t, join(newRig(creates("t", 1, 17)...).render()), "+7 more")
	})
}

func TestOverlayRenderCollapse(t *testing.T) {
	tw(t, fRender, "collapsed view returns exactly three lines: heading with (completed/total), expand hint, trailing spacer", func(t *testing.T) {
		r := newRig(act{"action": "create", "subject": "a"}, act{"action": "create", "subject": "b"}, act{"action": "update", "id": 1.0, "status": "completed"})
		r.ov.toggleCollapse(r.env)
		lines := r.render()
		eq(t, len(lines), 3, "lines")
		contains(t, at(lines, 0), "Todos (1/2)")
		contains(t, at(lines, 1), "└─")
		contains(t, at(lines, 1), "ctrl+shift+t to expand")
		eq(t, at(lines, 2), "", "spacer")
	})
	tw(t, fRender, "uncollapsed (default) yields the unchanged full render (regression-safe)", func(t *testing.T) {
		lines := newRig(act{"action": "create", "subject": "a"}, act{"action": "create", "subject": "b"}).render()
		eq(t, len(lines), 4, "heading + 2 tasks + trailing spacer")
	})
	tw(t, fRender, "collapsed render short-circuits before completed-display tracking (no task queued for hide while collapsed)", func(t *testing.T) {
		// Adaptation: the original registers a factory and renders only when the test calls it; here an update
		// renders as it pushes, so the overlay is collapsed before its first update.
		st := newStore()
		st.setActiveRenderSession("test-session")
		runActions(st, "test-session", completes("done", 1, 1)...)
		sink := &fakeSink{}
		env := viewEnv{sink: sink, theme: identityTheme{}, width: 200}
		ov := newOverlay(st, 0)
		ov.bind()
		ov.toggleCollapse(env)
		ov.update(env) // collapsed render: must NOT queue the completed task
		ov.hideCompletedTasksFromPreviousTurn(env)
		ov.toggleCollapse(env)
		expanded := join(ov.render(env))
		contains(t, expanded, "done")
		contains(t, expanded, "✓")
	})
	tw(t, fRender, "renders the configured key in the collapsed hint (alt+o)", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"alt+o"}`)
		r := newRig(creates("a", 1, 1)...)
		r.ov.toggleCollapse(r.env)
		hint := at(r.render(), 1)
		contains(t, hint, "alt+o to expand")
		notContains(t, hint, "{key}")
		notContains(t, hint, "ctrl+shift+t")
	})
	tw(t, fRender, "renders the default key in the collapsed hint when config is missing", func(t *testing.T) {
		configHome(t)
		r := newRig(creates("a", 1, 1)...)
		r.ov.toggleCollapse(r.env)
		hint := at(r.render(), 1)
		contains(t, hint, "ctrl+shift+t to expand")
		notContains(t, hint, "{key}")
	})
	tw(t, fRender, "renders the default key when the configured spec is invalid", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"ctr+t"}`)
		r := newRig(creates("a", 1, 1)...)
		r.ov.toggleCollapse(r.env)
		contains(t, at(r.render(), 1), "ctrl+shift+t to expand")
	})
	tw(t, fRender, "renders a static collapsed label — not the sentinel — when the key resolves to off", func(t *testing.T) {
		w, _ := configHome(t)
		r := newRig(creates("a", 1, 1)...)
		r.ov.toggleCollapse(r.env)
		w(`{"collapseKey":"off"}`)
		hint := at(r.render(), 1)
		contains(t, hint, "collapsed")
		notContains(t, hint, "off to expand")
		notContains(t, hint, "{key}")
	})
}

func TestOverlayRenderWidth(t *testing.T) {
	tw(t, fRender, "renders without throwing at small widths", func(t *testing.T) {
		r := newRig(act{"action": "create", "subject": "a very long subject that would overflow a narrow column"})
		r.env.width = 20
		for _, l := range r.render() {
			if w := visibleWidth(l); w > 20 {
				t.Fatalf("row %q is %d cells wide at width 20", l, w)
			}
		}
	})
	tw(t, fRender, "drops completed tasks from counts after the next agent turn starts", func(t *testing.T) {
		r := newRig(append(completes("done", 1, 1), act{"action": "create", "subject": "next"})...)
		contains(t, join(r.render()), "Todos (1/2)")
		second := join(r.render())
		contains(t, second, "Todos (1/2)")
		contains(t, second, "next")
		contains(t, second, "done")
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		hidden := join(r.render())
		contains(t, hidden, "Todos (0/1)")
		contains(t, hidden, "next")
		notContains(t, hidden, "done")
	})
	tw(t, fRender, "re-renders reflect live state changes without re-registering", func(t *testing.T) {
		r := newRig(creates("first", 1, 1)...)
		contains(t, join(r.render()), "first1")
		runActions(r.st, "test-session", act{"action": "create", "subject": "second"})
		out := join(r.render())
		contains(t, out, "first1")
		contains(t, out, "second")
	})
}

// The host shows a string[] widget's first ten rows only (Pi interactive-mode.ts:2321-2336), so the
// production overlay caps its own budget instead of letting the host truncate mid-list.
func TestOverlayRowCap(t *testing.T) {
	r := newRig(creates("t", 1, 17)...)
	r.ov = newOverlay(r.st, hostWidgetRows-1)
	lines := r.render()
	if len(lines) > hostWidgetRows {
		t.Fatalf("%d rows, the host shows %d", len(lines), hostWidgetRows)
	}
	contains(t, join(lines), "more")
	r.env.toolsExpanded = true
	if got := len(r.render()); got > hostWidgetRows {
		t.Fatalf("expanded: %d rows, the host shows %d", got, hostWidgetRows)
	}
}

func TestOverlayLifecycle(t *testing.T) {
	tw(t, fLifecycle, "update() with no UI ctx bound is a no-op", func(t *testing.T) {
		st := newStore()
		st.setActiveRenderSession("s")
		runActions(st, "s", creates("t", 1, 1)...)
		sink := &fakeSink{}
		ov := newOverlay(st, 0)
		ov.update(viewEnv{sink: sink, theme: identityTheme{}, width: 80})
		eq(t, len(sink.calls), 0, "widget calls")
	})
	tw(t, fLifecycle, "update() with empty todos does not register a widget", func(t *testing.T) {
		r := newRig()
		eq(t, len(r.sink.calls), 0, "widget calls")
	})
	tw(t, fLifecycle, "first update() with non-empty todos registers the widget exactly once", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		eq(t, len(r.sink.calls), 1, "widget calls")
		eq(t, at(r.sink.calls, 0).Key, "rpiv-todos", "key")
		eq(t, r.ov.isRegistered(), true, "registered")
	})
	tskip(t, fLifecycle, "second update() after registration calls tui.requestRender instead of re-registering", "a subprocess extension has no TUI handle to requestRender; update re-pushes the rows instead (TestUpdateAfterRegistrationRepushesRows)")
	tw(t, fLifecycle, "transition non-empty → empty unregisters the widget", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		runActions(r.st, "test-session", act{"action": "clear"})
		r.ov.update(r.env)
		if r.sink.last().Lines != nil || r.sink.last().Key != "rpiv-todos" {
			t.Fatalf("last call = %#v, want a clear of rpiv-todos", r.sink.last())
		}
		eq(t, r.ov.isRegistered(), false, "registered")
	})
	tw(t, fLifecycle, "empty → non-empty after empty transition re-registers", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		runActions(r.st, "test-session", act{"action": "clear"})
		r.ov.update(r.env)
		runActions(r.st, "test-session", act{"action": "create", "subject": "again"})
		r.ov.update(r.env)
		if r.sink.last().Lines == nil {
			t.Fatal("the widget was not pushed again")
		}
		eq(t, r.ov.isRegistered(), true, "registered")
	})
	tskip(t, fLifecycle, "setUICtx(same ctx) is idempotent", "each host event hands the extension a new Context value, so there is no identity to compare; the overlay is bound once per session_start (bind)")
	tw(t, fLifecycle, "setUICtx(different ctx) resets cached registration; next update re-registers under the new ctx", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		r.ov.bind()
		eq(t, r.ov.isRegistered(), false, "registered after rebinding")
		before := len(r.sink.calls)
		r.ov.update(r.env)
		eq(t, len(r.sink.calls), before+1, "re-registered")
	})
	tw(t, fLifecycle, "dispose() unregisters the widget and clears ctx; later update() without setUICtx is a no-op", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		r.ov.dispose(r.sink)
		if r.sink.last().Lines != nil || r.sink.last().Key != "rpiv-todos" {
			t.Fatalf("last call = %#v, want a clear", r.sink.last())
		}
		n := len(r.sink.calls)
		r.ov.update(r.env)
		eq(t, len(r.sink.calls), n, "update after dispose")
	})
	tw(t, fLifecycle, "uses the current UI theme after invalidation without re-registering", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		r.env.theme = recordingTheme{}
		contains(t, at(r.render(), 0), "<accent>")
		r.env.theme = identityTheme{}
		notContains(t, at(r.render(), 0), "<accent>")
		eq(t, len(r.sink.calls), 1, "no re-registration")
	})
	tw(t, fLifecycle, "resetCompletedDisplayState() lets replayed completed tasks be shown once again", func(t *testing.T) {
		r := newRig(completes("done", 1, 1)...)
		r.render()
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		eq(t, len(r.render()), 0, "hidden")
		r.ov.resetCompletedDisplayState()
		contains(t, join(r.render()), "done")
	})
	tw(t, fLifecycle, "hideCompletedTasksFromPreviousTurn() is a no-op when nothing is pending hide", func(t *testing.T) {
		r := newRig(creates("t", 1, 1)...)
		n := len(r.sink.calls)
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		eq(t, len(r.sink.calls), n, "no push")
	})
	tw(t, fLifecycle, "all-deleted todos count as empty (no widget)", func(t *testing.T) {
		r := newRig(append(creates("t", 1, 1), act{"action": "delete", "id": 1.0})...)
		eq(t, len(r.sink.calls), 0, "widget calls")
	})
	tw(t, fLifecycle, "a new TodoOverlay starts with collapsed = false (renders the full view, not the 3-line collapsed shape)", func(t *testing.T) {
		eq(t, len(newRig(creates("t", 1, 3)...).render()), 5, "lines")
	})
	tw(t, fLifecycle, "toggleCollapse() flips collapsed and calls requestRender(true) (forced, distinct from the non-forced requestRender())", func(t *testing.T) {
		r := newRig(creates("t", 1, 3)...)
		n := len(r.sink.calls)
		r.ov.toggleCollapse(r.env)
		eq(t, len(r.render()), 3, "collapsed")
		eq(t, len(r.sink.calls), n+1, "the new shape is pushed")
		eq(t, len(r.sink.last().Lines), 3, "pushed rows")
		r.ov.toggleCollapse(r.env)
		eq(t, len(r.render()), 5, "expanded")
	})
	tw(t, fLifecycle, "isRegistered() reflects the widget registration state", func(t *testing.T) {
		st := newStore()
		st.setActiveRenderSession("s")
		sink := &fakeSink{}
		ov := newOverlay(st, 0)
		env := viewEnv{sink: sink, theme: identityTheme{}, width: 80}
		ov.bind()
		eq(t, ov.isRegistered(), false, "before")
		runActions(st, "s", creates("t", 1, 1)...)
		ov.update(env)
		eq(t, ov.isRegistered(), true, "after")
		ov.dispose(sink)
		eq(t, ov.isRegistered(), false, "disposed")
	})
	tw(t, fLifecycle, "resetCompletedDisplayState() does NOT reset collapsed", func(t *testing.T) {
		r := newRig(creates("t", 1, 3)...)
		r.ov.toggleCollapse(r.env)
		r.ov.resetCompletedDisplayState()
		eq(t, len(r.render()), 3, "still collapsed")
	})
}

func TestUpdateAfterRegistrationRepushesRows(t *testing.T) {
	r := newRig(creates("t", 1, 2)...)
	runActions(r.st, "test-session", act{"action": "create", "subject": "more"})
	r.ov.update(r.env)
	eq(t, len(r.sink.calls), 2, "pushes")
	contains(t, join(r.sink.last().Lines), "more")
	r.env.width = 12
	r.ov.update(r.env)
	for _, l := range r.sink.last().Lines {
		if visibleWidth(l) > 12 {
			t.Fatalf("pushed row %q is wider than the width the rows were laid out for", l)
		}
	}
}

func TestCompletedTaskTrackingFollowsTheStore(t *testing.T) {
	// upstream: todo-overlay.ts:122-135 (getSnapshot): a nextId that went back (clear, or a replay of an
	// older branch) resets the completed-display state, and ids no longer completed leave both sets.
	r := newRig(completes("done", 1, 1)...)
	r.render()
	r.ov.hideCompletedTasksFromPreviousTurn(r.env)
	eq(t, len(r.render()), 0, "hidden")
	runActions(r.st, "test-session", act{"action": "clear"})
	r.render() // the overlay sees nextId go back to 1: the display state resets
	runActions(r.st, "test-session", act{"action": "create", "subject": "x"}, act{"action": "update", "id": 1.0, "status": "completed"})
	contains(t, join(r.render()), "x")
}

func TestOverlayExactSummaryAndClear(t *testing.T) {
	t.Run("the overflow summary names both kinds in the original's shape", func(t *testing.T) {
		// 12 pending + 3 completed: "+5 more (3 completed, 2 pending)" (todo-overlay.render.test.ts:205-207).
		r := newRig(append(creates("p", 1, 12), completes("c", 13, 15)...)...)
		lines := r.render()
		eq(t, at(lines, len(lines)-2), "└─ +5 more (3 completed, 2 pending)", "summary row")
	})
	t.Run("hiding the last completed task clears the widget", func(t *testing.T) {
		r := newRig(completes("done", 1, 1)...)
		r.render()
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		if r.sink.last().Lines != nil {
			t.Fatalf("last push = %#v, want a clear (nil rows)", r.sink.last())
		}
	})
	t.Run("a replay that lowers nextId shows the completed tasks again", func(t *testing.T) {
		r := newRig(append(completes("done", 1, 1), act{"action": "create", "subject": "next"})...)
		r.render()
		r.ov.hideCompletedTasksFromPreviousTurn(r.env)
		notContains(t, join(r.render()), "done")
		// session_tree back to an earlier snapshot: the same completed task, a smaller nextId.
		r.st.replaceState("test-session", taskState{Tasks: []task{tk(1, "done", withStatus(statusCompleted))}, NextID: 2})
		contains(t, join(r.render()), "done")
	})
}
