package tintinweb_tasks

import (
	"fmt"
	"slices"
	"strconv"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// tasksCommand is the interactive /tasks menu: view, create, clear, settings. A cancelled dialog ends the
// menu. The dialogs wait for the user, so the app lock is taken only around each store access.
// upstream: index.ts /tasks command.
func (a *app) tasksCommand(ctx sdk.Context, _ string) error {
	a.mu.Lock()
	a.touch(ctx)
	a.initializeStoreForContext(ctx, false)
	a.mu.Unlock()
	locked := func(fn func()) { a.mu.Lock(); defer a.mu.Unlock(); fn() }

	var mainMenu, viewTasks, createTask, settingsMenu func() error
	viewTaskDetail := func(taskID string) error {
		var t *task
		locked(func() { t = a.store.get(taskID) })
		if t == nil {
			return viewTasks()
		}
		var actions []string
		if t.Status == statusPending {
			actions = append(actions, "▸ Start (in_progress)")
		}
		if t.Status == statusInProgress {
			actions = append(actions, "✓ Complete")
		}
		actions = append(actions, "✗ Delete", "← Back")
		title := fmt.Sprintf("#%s [%s] %s\n%s", t.ID, t.Status, t.Subject, t.Description)
		action, ok, err := ctx.Select(title, actions)
		if err != nil {
			return err
		}
		if ok {
			locked(func() {
				switch action {
				case "▸ Start (in_progress)":
					a.store.update(taskID, updateFields{Status: strPtr(statusInProgress)})
					a.widget.setActiveTask(taskID, true)
					a.widget.update()
				case "✓ Complete":
					a.store.update(taskID, updateFields{Status: strPtr(statusCompleted)})
					a.autoClear.trackCompletion(taskID, a.cadence.CurrentTurn)
					a.widget.setActiveTask(taskID, false)
					a.widget.update()
				case "✗ Delete":
					a.store.update(taskID, updateFields{Status: strPtr(statusDeleted)})
					a.widget.setActiveTask(taskID, false)
					a.widget.update()
				}
			})
		}
		return viewTasks()
	}

	viewTasks = func() error {
		var tasks []*task
		var glyphs taskGlyphs
		locked(func() { tasks, glyphs = a.store.list(nil), resolveTaskGlyphs(a.cfg["glyphs"]) })
		if len(tasks) == 0 {
			if _, _, err := ctx.Select("No tasks", []string{"← Back"}); err != nil {
				return err
			}
			return mainMenu()
		}
		statusGlyph := func(status string) string {
			switch status {
			case statusCompleted:
				return glyphs.Completed
			case statusInProgress:
				return glyphs.InProgress
			}
			return glyphs.Pending
		}
		choices := make([]string, 0, len(tasks)+1)
		for _, t := range tasks {
			choices = append(choices, fmt.Sprintf("%s #%s [%s] %s", statusGlyph(t.Status), t.ID, t.Status, t.Subject))
		}
		choices = append(choices, "← Back")
		selected, ok, err := ctx.Select("Tasks", choices)
		if err != nil {
			return err
		}
		if !ok || selected == "← Back" {
			return mainMenu()
		}
		// Matched by row position rather than parsed out of the label: both the glyph and the subject are
		// free text, and either can contain something like "#42".
		if i := slices.Index(choices, selected); i >= 0 && i < len(tasks) {
			return viewTaskDetail(tasks[i].ID)
		}
		return viewTasks()
	}

	createTask = func() error {
		subject, ok, err := ctx.Input("Task subject", "")
		if err != nil {
			return err
		}
		if !ok || subject == "" {
			return mainMenu()
		}
		description, ok, err := ctx.Input("Task description", "")
		if err != nil {
			return err
		}
		if !ok || description == "" {
			return mainMenu()
		}
		locked(func() {
			a.store.create(subject, description, "", nil)
			a.widget.update()
		})
		return mainMenu()
	}

	settingsMenu = func() error {
		if err := a.settingsMenu(ctx); err != nil {
			return err
		}
		return mainMenu()
	}

	mainMenu = func() error {
		var tasks []*task
		locked(func() { tasks = a.store.list(nil) })
		completed := 0
		for _, t := range tasks {
			if t.Status == statusCompleted {
				completed++
			}
		}
		choices := []string{fmt.Sprintf("View all tasks (%d)", len(tasks)), "Create task"}
		if completed > 0 {
			choices = append(choices, fmt.Sprintf("Clear completed (%d)", completed))
		}
		if len(tasks) > 0 {
			choices = append(choices, fmt.Sprintf("Clear all (%d)", len(tasks)))
		}
		choices = append(choices, "Settings")
		choice, ok, err := ctx.Select("Tasks", choices)
		if err != nil {
			return err
		}
		if !ok || choice == "" {
			return nil
		}
		switch {
		case hasPrefix(choice, "View"):
			return viewTasks()
		case choice == "Create task":
			return createTask()
		case choice == "Settings":
			return settingsMenu()
		case hasPrefix(choice, "Clear completed"):
			locked(func() {
				a.store.clearCompleted()
				if a.isSessionScope() {
					a.deleteSessionFileIfEmpty()
				}
				a.widget.update()
			})
			return mainMenu()
		case hasPrefix(choice, "Clear all"):
			locked(func() {
				a.store.clearAll()
				if a.isSessionScope() {
					a.deleteSessionFileIfEmpty()
				}
				a.widget.update()
			})
			return mainMenu()
		}
		return nil
	}
	return mainMenu()
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// settingRow is one row of the settings panel. upstream: ui/settings-menu.ts items.
type settingRow struct {
	id, label string
	current   func(cfg tasksConfig) string
	values    []string
	apply     func(cfg tasksConfig, v string)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func settingRows() []settingRow {
	return []settingRow{
		{"taskScope", "Task storage", func(c tasksConfig) string { return c.str("taskScope", "session") },
			[]string{"memory", "session", "session-global", "project"}, func(c tasksConfig, v string) { c["taskScope"] = v }},
		{"autoCascade", "Auto-cascade agent tasks", func(c tasksConfig) string { return onOff(c.flag("autoCascade")) },
			[]string{"on", "off"}, func(c tasksConfig, v string) { c["autoCascade"] = v == "on" }},
		{"collapseCompleted", "Collapse completed tasks", func(c tasksConfig) string { return onOff(c.flag("collapseCompleted")) },
			[]string{"on", "off"}, func(c tasksConfig, v string) { c["collapseCompleted"] = v == "on" }},
		{"showAll", "Show all tasks in widget", func(c tasksConfig) string { return onOff(c.flag("showAll")) },
			[]string{"on", "off"}, func(c tasksConfig, v string) { c["showAll"] = v == "on" }},
		{"maxVisible", "Max visible tasks in widget", func(c tasksConfig) string {
			switch n := c["maxVisible"].(type) {
			case float64:
				return strconv.FormatFloat(n, 'f', -1, 64)
			case int:
				return strconv.Itoa(n)
			}
			return "10"
		}, []string{"5", "10", "15", "20", "30", "50", "100"}, func(c tasksConfig, v string) { n, _ := strconv.Atoi(v); c["maxVisible"] = float64(n) }},
		{"sortOrder", "Widget sort order", func(c tasksConfig) string {
			if _, custom := c["sortOrder"].([]any); custom {
				return "custom" // shown, but deliberately left out of the cycle
			}
			return c.str("sortOrder", "id")
		}, builtInSortOrders, func(c tasksConfig, v string) { c["sortOrder"] = v }},
		{"hiddenAt", "Hidden tasks position", func(c tasksConfig) string { return c.str("hiddenAt", "bottom") },
			[]string{"bottom", "top"}, func(c tasksConfig, v string) { c["hiddenAt"] = v }},
		{"autoClearCompleted", "Auto-clear completed tasks", func(c tasksConfig) string { return c.str("autoClearCompleted", "on_list_complete") },
			[]string{"never", "on_list_complete", "on_task_complete"}, func(c tasksConfig, v string) { c["autoClearCompleted"] = v }},
	}
}

// settingsMenu is /tasks → Settings: each row cycles to its next value when picked and saves it as a project
// override. The original draws a SettingsList component; a Go extension has selects only (PORT.md).
func (a *app) settingsMenu(ctx sdk.Context) error {
	rows := settingRows()
	for {
		a.mu.Lock()
		labels := make([]string, 0, len(rows)+1)
		for _, r := range rows {
			labels = append(labels, fmt.Sprintf("%s: %s", r.label, r.current(a.cfg)))
		}
		a.mu.Unlock()
		labels = append(labels, "← Back")
		selected, ok, err := ctx.Select("⚙  Task Settings", labels)
		if err != nil {
			return err
		}
		i := slices.Index(labels, selected)
		if !ok || i < 0 || i >= len(rows) {
			return nil
		}
		a.mu.Lock()
		r := rows[i]
		cur := r.current(a.cfg)
		next := r.values[0]
		if at := slices.Index(r.values, cur); at >= 0 {
			next = r.values[(at+1)%len(r.values)]
		}
		r.apply(a.cfg, next)
		_ = saveTasksConfig(a.cfg, ctx.Cwd(), agentDir())
		a.mu.Unlock()
	}
}
