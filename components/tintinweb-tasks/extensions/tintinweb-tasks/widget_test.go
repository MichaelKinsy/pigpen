package tintinweb_tasks

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// plainTheme returns raw text (no ANSI escapes); strikethrough is ~~text~~. upstream: task-widget.test.ts mockTheme.
type plainTheme struct{}

func (plainTheme) Fg(_, text string) string         { return text }
func (plainTheme) Strikethrough(text string) string { return "~~" + text + "~~" }

// mockUI captures setWidget calls. upstream: task-widget.test.ts mockUICtx.
type mockUI struct {
	registered bool
	lines      []string
	cols       int
	sets       int
}

func (u *mockUI) setWidget(key string, lines []string) {
	if key != "tasks" {
		panic("unexpected widget key " + key)
	}
	u.sets++
	u.registered = lines != nil
	u.lines = lines
}
func (u *mockUI) theme() widgetTheme { return plainTheme{} }
func (u *mockUI) alive() bool        { return true }
func (u *mockUI) columns() int {
	if u.cols == 0 {
		return 200
	}
	return u.cols
}

// fakeTimers is vi.useFakeTimers(): the clock the store and widget read, and the repeating timer.
type fakeTimers struct {
	clock    atomic.Int64
	now      int64
	interval time.Duration
	fn       func()
	next     int64
}

func useFakeTimers(t *testing.T) *fakeTimers {
	f := &fakeTimers{now: 1_000_000}
	f.clock.Store(f.now)
	restoreClock := setClock(f.clock.Load)
	restoreTicker := setTicker(func(d time.Duration, fn func()) func() {
		f.interval, f.fn, f.next = d, fn, f.now+d.Milliseconds()
		return func() { f.fn = nil }
	})
	t.Cleanup(func() { restoreTicker(); restoreClock() })
	return f
}

// advance moves the clock, firing the repeating timer at each of its ticks.
func (f *fakeTimers) advance(ms int64) {
	end := f.now + ms
	for f.fn != nil && f.next <= end {
		f.now = f.next
		f.clock.Store(f.now)
		f.next += f.interval.Milliseconds()
		f.fn()
	}
	f.now = end
	f.clock.Store(end)
}

type widgetEnv struct {
	store  *taskStore
	widget *taskWidget
	ui     *mockUI
	timers *fakeTimers
}

func newWidgetEnv(t *testing.T, cfg tasksConfig) *widgetEnv {
	t.Helper()
	e := &widgetEnv{timers: useFakeTimers(t), store: newTaskStore(""), ui: &mockUI{}}
	e.widget = newTaskWidget(e.store, cfg)
	e.widget.setUI(e.ui)
	t.Cleanup(func() { e.widget.dispose() })
	return e
}

// reconfigure replaces the widget (the original builds a new TaskWidget over the same store).
func (e *widgetEnv) reconfigure(t *testing.T, cfg tasksConfig) {
	e.widget.dispose()
	e.widget = newTaskWidget(e.store, cfg)
	e.widget.setUI(e.ui)
	t.Cleanup(func() { e.widget.dispose() })
}

// render is renderWidget: the lines of the registered widget, drawn live as the host's render pass does.
func (e *widgetEnv) render(columns ...int) []string {
	if !e.ui.registered {
		return []string{}
	}
	cols := 200
	if len(columns) > 0 {
		cols = columns[0]
	}
	return e.widget.buildLines(plainTheme{}, cols)
}

func (e *widgetEnv) create(subject string, activeForm string, meta map[string]any) *task {
	return e.store.create(subject, "Desc", activeForm, meta)
}

func (e *widgetEnv) setStatus(id, status string) { e.store.update(id, updateFields{Status: &status}) }

func anyLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func lineWith(lines []string, sub string) string {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func last(lines []string) string { return at(lines, len(lines)-1) }

func tail(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	return lines[1:]
}

func at(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

func TestTaskWidget(t *testing.T) {
	const f = "task-widget"
	has := strings.Contains
	tw(t, f, "shows nothing when no tasks exist", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.widget.update()
		eq(t, e.ui.registered, false)
	})
	tw(t, f, "renders pending tasks with ◻ icon", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Do something", "", nil)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 2) // header + 1 task
		eq(t, has(at(lines, 0), "1 tasks"), true)
		eq(t, has(at(lines, 0), "1 open"), true)
		eq(t, has(at(lines, 1), "◻"), true)
		eq(t, has(at(lines, 1), "Do something"), true)
	})
	tw(t, f, "renders in-progress tasks with ◼ icon", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Working on it", "", nil)
		e.setStatus("1", "in_progress")
		e.widget.update()
		lines := e.render()
		eq(t, has(at(lines, 1), "◼"), true)
		eq(t, has(at(lines, 1), "Working on it"), true)
	})
	tw(t, f, "renders completed tasks with ✔ icon and strikethrough", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Done task", "", nil)
		e.setStatus("1", "completed")
		e.widget.update()
		lines := e.render()
		eq(t, has(at(lines, 1), "✔"), true)
		eq(t, has(at(lines, 1), "~~#1 Done task~~"), true)
	})
	tw(t, f, "renders active tasks with spinner icon", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Running thing", "Processing data", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		lines := e.render()
		// Should show activeForm text with "…" suffix, and NOT show ◼ for an active task.
		eq(t, has(at(lines, 1), "Processing data…"), true)
		eq(t, has(at(lines, 1), "◼"), false)
	})
	tw(t, f, "shows blocked-by info for pending tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Blocker", "", nil)
		e.create("Blocked", "", nil)
		e.store.update("2", updateFields{AddBlockedBy: []string{"1"}})
		e.widget.update()
		eq(t, has(lineWith(e.render(), "Blocked"), "blocked by #1"), true)
	})
	tw(t, f, "hides completed blockers in blocked-by suffix", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Blocker", "", nil)
		e.create("Blocked", "", nil)
		e.store.update("2", updateFields{AddBlockedBy: []string{"1"}})
		e.setStatus("1", "completed")
		e.widget.update()
		eq(t, has(lineWith(e.render(), "Blocked"), "blocked by"), false)
	})
	tw(t, f, "does not crash the host when a task is missing legacy fields", func(t *testing.T) {
		// A record persisted before the blocking feature has no blockedBy. A throw in the render would kill
		// the whole host, so the render must never panic (the guard returns a safe fallback).
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Legacy pending", "", nil)
		raw := e.store.get("1")
		raw.BlockedBy, raw.Blocks, raw.Metadata = nil, nil, nil
		e.widget.update()
		e.render()
	})
	tw(t, f, "shows status summary in header", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		for _, s := range []string{"Task A", "Task B", "Task C"} {
			e.create(s, "", nil)
		}
		e.setStatus("1", "completed")
		e.setStatus("2", "in_progress")
		e.widget.update()
		lines := e.render()
		for _, want := range []string{"3 tasks", "1 done", "1 in progress", "1 open"} {
			eq(t, has(at(lines, 0), want), true)
		}
	})
	tw(t, f, "clears widget when all tasks are deleted", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task", "", nil)
		e.widget.update()
		eq(t, e.ui.registered, true)
		e.setStatus("1", "deleted")
		e.widget.update()
		eq(t, e.ui.registered, false)
	})
	many := func(e *widgetEnv, n int) {
		for i := 0; i < n; i++ {
			e.create("Task "+strconv.Itoa(i+1), "", nil)
		}
	}
	tw(t, f, "limits visible tasks to MAX_VISIBLE_TASKS", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		many(e, 15)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 12) // header + 10 tasks + "… and 5 more"
		eq(t, has(at(lines, 11), "5 more"), true)
	})
	tw(t, f, "respects maxVisible config", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"maxVisible": 5})
		many(e, 15)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 7) // header + 5 tasks + "… and 10 more"
		eq(t, has(at(lines, 6), "10 more"), true)
	})
	tw(t, f, "shows all tasks when limit exceeds task count", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"maxVisible": 10})
		many(e, 3)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 4)
		eq(t, has(last(lines), "more"), false)
	})
	tw(t, f, "shows all tasks when showAll is true even with maxVisible set", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"showAll": true, "maxVisible": 5})
		many(e, 15)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 16)
		eq(t, has(last(lines), "more"), false)
	})
	tw(t, f, "truncates from top when hiddenAt is 'top'", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"sortOrder": "status", "hiddenAt": "top", "showAll": false, "maxVisible": 5})
		for i := 1; i <= 4; i++ {
			e.create("Done "+strconv.Itoa(i), "", nil)
		}
		for i := 1; i <= 2; i++ {
			e.create("Working "+strconv.Itoa(i), "", nil)
		}
		for i := 1; i <= 2; i++ {
			e.create("Todo "+strconv.Itoa(i), "", nil)
		}
		for i := 1; i <= 4; i++ {
			e.setStatus(strconv.Itoa(i), "completed")
		}
		for i := 5; i <= 6; i++ {
			e.setStatus(strconv.Itoa(i), "in_progress")
		}
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 7)
		eq(t, has(at(lines, 1), "3 more"), true)
		eq(t, anyLine(lines, "Working 1"), true)
		eq(t, anyLine(lines, "Todo 2"), true)
		eq(t, anyLine(lines, "Done 4"), true)
		eq(t, anyLine(lines, "Done 1"), false)
		eq(t, anyLine(lines, "Done 3"), false)
	})
	tw(t, f, "truncates from bottom when hiddenAt holds an unrecognised value", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"hiddenAt": "middle", "maxVisible": 3})
		for i := 1; i <= 5; i++ {
			e.create("Task "+strconv.Itoa(i), "", nil)
		}
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 5)
		eq(t, has(at(lines, 1), "Task 1"), true)
		eq(t, has(at(lines, 4), "2 more"), true)
	})
	tw(t, f, "truncates from bottom by default", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"maxVisible": 3})
		for i := 1; i <= 5; i++ {
			e.create("Task "+strconv.Itoa(i), "", nil)
		}
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 5)
		eq(t, has(at(lines, 1), "Task 1"), true)
		eq(t, has(at(lines, 3), "Task 3"), true)
		eq(t, has(at(lines, 4), "2 more"), true)
		eq(t, anyLine(lines, "Task 4"), false)
	})
	// collapseCompleted: 2 completed (#1,#2), 1 in_progress (#3), 2 pending (#4,#5).
	seedCollapse := func(e *widgetEnv) {
		for i := 1; i <= 5; i++ {
			e.create("Task "+strconv.Itoa(i), "", nil)
		}
		e.setStatus("1", "completed")
		e.setStatus("2", "completed")
		e.setStatus("3", "in_progress")
	}
	tw(t, f, "replaces completed tasks with a single count line", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"collapseCompleted": true})
		seedCollapse(e)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 5)
		eq(t, anyLine(lines, "Task 1"), false)
		eq(t, anyLine(lines, "Task 2"), false)
		eq(t, has(last(lines), "2 completed"), true)
	})
	tw(t, f, "leaves the header counts untouched", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"collapseCompleted": true})
		seedCollapse(e)
		e.widget.update()
		eq(t, has(at(e.render(), 0), "5 tasks (2 done, 1 in progress, 2 open)"), true)
	})
	tw(t, f, "applies the visible limit to the remaining tasks only", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"collapseCompleted": true, "maxVisible": 2})
		seedCollapse(e)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 5)
		eq(t, has(at(lines, 1), "Task 3"), true)
		eq(t, has(at(lines, 2), "Task 4"), true)
		eq(t, has(at(lines, 3), "1 more"), true)
		eq(t, has(at(lines, 4), "2 completed"), true)
	})
	tw(t, f, "emits no count line when nothing is completed", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"collapseCompleted": true})
		many(e, 3)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 4)
		eq(t, anyLine(lines, "completed"), false)
	})
	tw(t, f, "stays visible as header plus count line when everything is completed", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"collapseCompleted": true})
		many(e, 3)
		for i := 1; i <= 3; i++ {
			e.setStatus(strconv.Itoa(i), "completed")
		}
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 2)
		eq(t, has(at(lines, 1), "3 completed"), true)
	})
	tw(t, f, "lists completed tasks individually when off", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"collapseCompleted": false})
		seedCollapse(e)
		e.widget.update()
		lines := e.render()
		eq(t, len(lines), 6)
		eq(t, has(at(lines, 1), "Task 1"), true)
	})
	threeStates := func(e *widgetEnv) {
		e.create("Pending task", "", nil)     // #1
		e.create("Completed task", "", nil)   // #2
		e.create("In progress task", "", nil) // #3
		e.setStatus("2", "completed")
		e.setStatus("3", "in_progress")
		e.widget.update()
	}
	tw(t, f, "sorts tasks by status when sortOrder is 'status'", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"sortOrder": "status"})
		threeStates(e)
		lines := e.render()
		eq(t, has(at(lines, 1), "Completed task"), true)
		eq(t, has(at(lines, 2), "In progress task"), true)
		eq(t, has(at(lines, 3), "Pending task"), true)
	})
	tw(t, f, "sorts active work first when sortOrder is 'active'", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"sortOrder": "active"})
		threeStates(e)
		lines := e.render()
		eq(t, has(at(lines, 1), "In progress task"), true)
		eq(t, has(at(lines, 2), "Pending task"), true)
		eq(t, has(at(lines, 3), "Completed task"), true)
	})
	tw(t, f, "honours a custom sort spec from config", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{"sortOrder": arr{obj{"field": "id", "direction": "desc"}}})
		many(e, 3)
		e.widget.update()
		re := regexp.MustCompile(`#(\d+)`)
		got := []string{}
		for _, l := range tail(e.render()) {
			m := re.FindStringSubmatch(l)
			if len(m) > 1 {
				got = append(got, m[1])
			}
		}
		eq(t, got, []string{"3", "2", "1"})
	})
	idOrder := func(t *testing.T, cfg tasksConfig) {
		e := newWidgetEnv(t, cfg)
		threeStates(e)
		lines := e.render()
		eq(t, has(at(lines, 1), "Pending task"), true)
		eq(t, has(at(lines, 2), "Completed task"), true)
		eq(t, has(at(lines, 3), "In progress task"), true)
	}
	tw(t, f, "defaults to ID order when sortOrder is unset", func(t *testing.T) { idOrder(t, tasksConfig{}) })
	tw(t, f, "keeps ID order when sortOrder is 'id'", func(t *testing.T) { idOrder(t, tasksConfig{"sortOrder": "id"}) })
	tw(t, f, "tracks token usage for active tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Active task", "Running", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		e.widget.addTokenUsage(1000, 500)
		e.widget.addTokenUsage(500, 300)
		line := lineWith(e.render(), "Running…")
		eq(t, has(line, "↑ 1.5k"), true)
		eq(t, has(line, "↓ 800"), true)
	})
	tw(t, f, "deactivates a task with setActiveTask(id, false)", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task", "Doing work", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		eq(t, has(at(e.render(), 1), "Doing work…"), true)
		e.widget.setActiveTask("1", false)
		lines := e.render()
		eq(t, has(at(lines, 1), "◼"), true)
		eq(t, has(at(lines, 1), "Doing work…"), false)
	})
	tw(t, f, "prunes stale active IDs on update", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task", "", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		e.setStatus("1", "completed")
		e.widget.update()
		lines := e.render()
		eq(t, has(at(lines, 1), "✔"), true)
		eq(t, has(at(lines, 1), "~~#1 Task~~"), true)
	})
	tw(t, f, "supports multiple active tasks simultaneously", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task A", "Processing A", nil)
		e.create("Task B", "Processing B", nil)
		e.setStatus("1", "in_progress")
		e.setStatus("2", "in_progress")
		e.widget.setActiveTask("1", true)
		e.widget.setActiveTask("2", true)
		lines := e.render()
		eq(t, has(at(lines, 1), "Processing A…"), true)
		eq(t, has(at(lines, 2), "Processing B…"), true)
	})
	tw(t, f, "distributes token usage across all active tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task A", "A", nil)
		e.create("Task B", "B", nil)
		e.setStatus("1", "in_progress")
		e.setStatus("2", "in_progress")
		e.widget.setActiveTask("1", true)
		e.widget.setActiveTask("2", true)
		e.widget.addTokenUsage(100, 50)
		lines := e.render()
		eq(t, has(at(lines, 1), "↑ 100"), true)
		eq(t, has(at(lines, 2), "↑ 100"), true)
	})
	tw(t, f, "dispose clears widget and timer", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task", "", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		e.widget.dispose()
		eq(t, e.ui.registered, false)
		eq(t, e.timers.fn == nil, true)
	})
	tw(t, f, "uses subject as fallback when no activeForm", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("My Subject", "", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		eq(t, has(at(e.render(), 1), "My Subject…"), true)
	})
	tw(t, f, "shows elapsed time but no token arrows when tokens are zero", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("No tokens", "Working", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		e.timers.advance(5000)
		e.widget.update()
		line := lineWith(e.render(), "Working…")
		eq(t, has(line, "5s"), true)
		eq(t, has(line, "↑"), false)
		eq(t, has(line, "↓"), false)
	})
	tw(t, f, "cleans up metrics when stale active IDs are pruned", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task", "Running", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		e.widget.addTokenUsage(100, 50)
		e.setStatus("1", "deleted")
		e.widget.update()
		e.create("Task 2", "Running", nil) // ID 2
		e.setStatus("2", "in_progress")
		e.widget.setActiveTask("2", true)
		eq(t, has(at(e.render(), 1), "↑ 100"), false)
	})
	tw(t, f, "indents task lines under header", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Indented task", "", nil)
		e.widget.update()
		eq(t, strings.HasPrefix(at(e.render(), 1), "  "), true)
	})
	tw(t, f, "widget is placed aboveEditor", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Task", "", nil)
		e.widget.update()
		eq(t, widgetPlacement, "aboveEditor")
		eq(t, e.ui.registered, true)
	})
}

func TestFormatDuration(t *testing.T) {
	const f = "task-widget"
	run := func(t *testing.T, ms int64, tokens [2]int, want ...string) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Quick", "Working", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		if tokens[0] != 0 || tokens[1] != 0 {
			e.widget.addTokenUsage(tokens[0], tokens[1])
		}
		e.timers.advance(ms)
		e.widget.update()
		for _, w := range want {
			eq(t, strings.Contains(at(e.render(), 1), w), true)
		}
	}
	tw(t, f, "shows seconds for short durations", func(t *testing.T) { run(t, 30_000, [2]int{}, "30s") })
	tw(t, f, "shows hours for long durations", func(t *testing.T) { run(t, 3_723_000, [2]int{}, "1h 2m") }) // 1h 2m 3s
	tw(t, f, "shows exact hours without minutes", func(t *testing.T) { run(t, 7_200_000, [2]int{}, "2h)") })
	tw(t, f, "shows minutes and seconds", func(t *testing.T) { run(t, 169_000, [2]int{}, "2m 49s") })
	tw(t, f, "formats small token counts without k suffix", func(t *testing.T) {
		run(t, 0, [2]int{500, 200}, "↑ 500", "↓ 200")
	})
	tw(t, f, "formats token counts with k suffix and removes .0", func(t *testing.T) {
		run(t, 0, [2]int{2000, 4100}, "↑ 2k", "↓ 4.1k") // 2000 → "2k" (not "2.0k")
	})
}

func TestConfigurableGlyphs(t *testing.T) {
	const f = "task-widget"
	// seed builds a widget over the glyph config and one task per status. upstream: task-widget.test.ts seed().
	seed := func(t *testing.T, glyphs any, cfg tasksConfig) (*widgetEnv, []string) {
		c := tasksConfig{}
		for k, v := range cfg {
			c[k] = v
		}
		if glyphs != nil {
			c["glyphs"] = glyphs
		}
		e := newWidgetEnv(t, c)
		e.create("Done task", "", nil)
		e.create("Open task", "", nil)
		e.create("Running task", "Running", nil)
		e.setStatus("1", "completed")
		e.setStatus("3", "in_progress")
		e.widget.update()
		return e, e.render()
	}
	// clippedAt20 renders one over-long task at a 20-column terminal, ANSI stripped.
	clippedAt20 := func(t *testing.T, glyphs any) string {
		c := tasksConfig{}
		if glyphs != nil {
			c["glyphs"] = glyphs
		}
		e := newWidgetEnv(t, c)
		e.create("A subject far too long for this terminal", "", nil)
		e.widget.update()
		return regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(at(e.render(20), 1), "")
	}
	has := strings.Contains
	tw(t, f, "renders the default glyphs when none are configured", func(t *testing.T) {
		_, lines := seed(t, nil, nil)
		eq(t, has(at(lines, 0), "●"), true)
		eq(t, has(at(lines, 1), "✔"), true)
		eq(t, has(at(lines, 2), "◻"), true)
		eq(t, has(at(lines, 3), "◼"), true)
	})
	tw(t, f, "renders configured status glyphs", func(t *testing.T) {
		_, lines := seed(t, obj{"completed": "[x]", "pending": "[ ]", "inProgress": "[>]"}, nil)
		eq(t, has(at(lines, 1), "[x] ~~#1 Done task~~"), true)
		eq(t, has(at(lines, 2), "[ ] #2 Open task"), true)
		eq(t, has(at(lines, 3), "[>] #3 Running task"), true)
	})
	tw(t, f, "renders a configured header glyph", func(t *testing.T) {
		_, lines := seed(t, obj{"header": "▸"}, nil)
		eq(t, has(at(lines, 0), "▸ 3 tasks"), true)
	})
	tw(t, f, "follows the completed glyph on the collapsed count line", func(t *testing.T) {
		_, lines := seed(t, obj{"completed": "[x]"}, tasksConfig{"collapseCompleted": true})
		eq(t, has(last(lines), "[x] 1 completed"), true)
	})
	tw(t, f, "prefers an explicit completedSummary on the collapsed count line", func(t *testing.T) {
		_, lines := seed(t, obj{"completed": "[x]", "completedSummary": "[=]"}, tasksConfig{"collapseCompleted": true})
		eq(t, has(last(lines), "[=] 1 completed"), true)
		eq(t, anyLine(lines, "[x]"), false)
	})
	firstWord := func(l string) string { return strings.Split(strings.TrimSpace(l), " ")[0] }
	tw(t, f, "cycles the configured spinner frames on the active task", func(t *testing.T) {
		e, _ := seed(t, obj{"spinner": arr{"<", "^", ">", "v"}}, nil)
		e.widget.setActiveTask("3", true)
		frames := []string{}
		for i := 0; i < 5; i++ {
			frames = append(frames, firstWord(at(e.render(), 3)))
			e.timers.advance(150)
		}
		eq(t, frames, []string{"<", "^", ">", "v", "<"})
	})
	tw(t, f, "renders a multi-glyph spinner frame whole", func(t *testing.T) {
		e, _ := seed(t, obj{"spinner": arr{"⣾⣾", "⣽⣽"}}, nil)
		e.widget.setActiveTask("3", true)
		eq(t, has(at(e.render(), 3), "⣾⣾ #3"), true)
	})
	tw(t, f, "renders a configured overflow glyph", func(t *testing.T) {
		_, lines := seed(t, obj{"overflow": "~"}, tasksConfig{"maxVisible": 2})
		eq(t, has(last(lines), "~ and 1 more"), true)
	})
	tw(t, f, "renders a configured blocked glyph", func(t *testing.T) {
		e, _ := seed(t, obj{"blocked": "->"}, nil)
		e.store.update("2", updateFields{AddBlockedBy: []string{"3"}})
		e.widget.update()
		eq(t, has(lineWith(e.render(), "Open task"), "-> blocked by #3"), true)
	})
	tw(t, f, "renders configured token, separator and trailing glyphs on the active row", func(t *testing.T) {
		e, _ := seed(t, obj{"inputTokens": "in", "outputTokens": "out", "statsSeparator": "|", "trailingEllipsis": "~~"}, nil)
		e.widget.setActiveTask("3", true)
		e.widget.addTokenUsage(1500, 800)
		e.timers.advance(5_000)
		e.widget.update()
		line := at(e.render(), 3)
		eq(t, has(line, "Running~~"), true)
		eq(t, has(line, "(5s | in 1.5k out 800)"), true)
	})
	tw(t, f, "clips over-wide lines with the configured truncation glyph", func(t *testing.T) {
		line := clippedAt20(t, obj{"truncation": "…"})
		eq(t, strings.HasSuffix(line, "…"), true)
		eq(t, visibleWidth(line) <= 20, true)
		eq(t, has(line, "terminal"), false)
	})
	tw(t, f, "clips with three ASCII dots by default", func(t *testing.T) {
		line := clippedAt20(t, nil)
		eq(t, strings.HasSuffix(line, "..."), true)
		eq(t, has(line, "…"), false)
	})
	tw(t, f, "falls back to the defaults for unusable glyph values", func(t *testing.T) {
		e, lines := seed(t, obj{"completed": "", "pending": float64(3), "header": nil, "spinner": arr{}}, nil)
		e.widget.setActiveTask("3", true)
		eq(t, has(at(lines, 0), "●"), true)
		eq(t, has(at(lines, 1), "✔"), true)
		eq(t, has(at(lines, 2), "◻"), true)
		eq(t, firstWord(at(e.render(), 3)), "✳")
	})
	tw(t, f, "never lets a glyph carry a control character into a rendered line", func(t *testing.T) {
		_, lines := seed(t, obj{"pending": "X\n\x1b]0;pwned\x07"}, nil)
		eq(t, has(at(lines, 2), "◻ #2 Open task"), true)
		for _, r := range strings.Join(lines, "") {
			if r < 0x20 || (r >= 0x7f && r < 0xa0) {
				t.Fatalf("control character %U in a rendered line", r)
			}
		}
	})
}

func TestSpinnerAnimationTiming(t *testing.T) {
	const f = "task-widget"
	setup := func(t *testing.T) (*widgetEnv, func() string) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Long job", "Working", nil)
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		return e, func() string { return strings.Split(strings.TrimSpace(at(e.render(), 1)), " ")[0] }
	}
	tw(t, f, "advances one frame per timer tick", func(t *testing.T) {
		e, glyph := setup(t)
		frames := map[string]bool{glyph(): true}
		for i := 0; i < 3; i++ {
			e.timers.advance(150)
			frames[glyph()] = true
		}
		eq(t, len(frames), 4)
	})
	tw(t, f, "does not advance when task activity redraws the widget", func(t *testing.T) {
		e, glyph := setup(t)
		before := glyph()
		for i := 0; i < 5; i++ {
			e.widget.update()
		}
		eq(t, glyph(), before)
	})
	tw(t, f, "still animates after an unrelated redraw", func(t *testing.T) {
		e, glyph := setup(t)
		e.widget.update()
		before := glyph()
		e.timers.advance(150)
		eq(t, glyph() != before, true)
	})
}

func TestWidgetAgentIDDisplay(t *testing.T) {
	const f = "subagent-integration"
	has := strings.Contains
	tw(t, f, "shows agent ID for active agent-backed tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Agent task", "Running tests", obj{"agentType": "general-purpose", "agentId": "abc1234567890"})
		e.setStatus("1", "in_progress")
		e.widget.setActiveTask("1", true)
		lines := e.render()
		eq(t, has(at(lines, 1), "agent abc12"), true)
		eq(t, has(at(lines, 1), "Running tests"), true)
	})
	tw(t, f, "shows agent ID for non-active in_progress agent-backed tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Agent task", "", obj{"agentType": "general-purpose", "agentId": "xyz9876543210"})
		e.setStatus("1", "in_progress")
		e.widget.update()
		lines := e.render()
		eq(t, has(at(lines, 1), "agent xyz98"), true)
		eq(t, has(at(lines, 1), "Agent task"), true)
	})
	tw(t, f, "does not show agent ID for tasks without agentId", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Manual task", "", nil)
		e.setStatus("1", "in_progress")
		e.widget.update()
		lines := e.render()
		eq(t, has(at(lines, 1), "agent"), false)
		eq(t, has(at(lines, 1), "Manual task"), true)
	})
	tw(t, f, "does not show agent ID for pending tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Pending agent task", "", obj{"agentType": "general-purpose", "agentId": "abc12345"})
		e.widget.update()
		eq(t, has(at(e.render(), 1), "agent abc"), false)
	})
	tw(t, f, "does not show agent ID for completed tasks", func(t *testing.T) {
		e := newWidgetEnv(t, tasksConfig{})
		e.create("Done", "", obj{"agentType": "general-purpose", "agentId": "abc12345"})
		e.setStatus("1", "completed")
		e.widget.update()
		eq(t, has(at(e.render(), 1), "agent abc"), false)
	})
}
