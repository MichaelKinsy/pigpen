package tintinweb_tasks

import (
	"fmt"
	"slices"

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
		action, ok, err := selectString(ctx, title, actions)
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
			if _, _, err := selectString(ctx, "No tasks", []string{"← Back"}); err != nil {
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
		selected, ok, err := selectString(ctx, "Tasks", choices)
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
		subject, ok, err := inputString(ctx, "Task subject", "")
		if err != nil {
			return err
		}
		if !ok || subject == "" {
			return mainMenu()
		}
		description, ok, err := inputString(ctx, "Task description", "")
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
		choice, ok, err := selectString(ctx, "Tasks", choices)
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

// selectString is ctx.Select with the answer as a string: a dialog resolves with whatever JSON the client answered, and a
// task menu entry is always a string.
func selectString(ctx sdk.Context, title string, options []string) (string, bool, error) {
	answer, ok, err := ctx.Select(title, options)
	choice, _ := answer.(string)
	return choice, ok, err
}

// inputString is ctx.Input with the answer as a string.
func inputString(ctx sdk.Context, title, placeholder string) (string, bool, error) {
	answer, ok, err := ctx.Input(title, placeholder)
	text, _ := answer.(string)
	return text, ok, err
}
