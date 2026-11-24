package todo

import (
	"fmt"
	"strings"
)

// defaultRemindersMax matches omp's default `todo.remindersMax` setting.
// upstream: packages/coding-agent/src/tools/settings.ts
const defaultRemindersMax = 3

// BuildStopReminder constructs omp's completion reminder text when the agent stops
// with incomplete (pending or in_progress) todo items. Blocked, completed, and
// abandoned tasks do not trigger the reminder.
// Returns ("", 0) when no open tasks remain.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:258-296
func BuildStopReminder(phases []TodoPhase, attempt, maxAttempts int) (string, int) {
	if len(phases) == 0 {
		return "", 0
	}
	type openPhase struct {
		name  string
		tasks []TodoItem
	}
	var incompleteByPhase []openPhase
	totalIncomplete := 0
	for _, phase := range phases {
		var open []TodoItem
		for _, task := range phase.Tasks {
			if task.Status == StatusPending || task.Status == StatusInProgress {
				open = append(open, task)
			}
		}
		if len(open) > 0 {
			incompleteByPhase = append(incompleteByPhase, openPhase{name: phase.Name, tasks: open})
			totalIncomplete += len(open)
		}
	}
	if totalIncomplete == 0 {
		return "", 0
	}

	var listLines []string
	for _, p := range incompleteByPhase {
		listLines = append(listLines, "- "+p.name)
		for _, t := range p.tasks {
			listLines = append(listLines, "  - "+t.Content)
		}
	}
	todoList := strings.Join(listLines, "\n")
	reminder := fmt.Sprintf(
		"<system-reminder>\n"+
			"You stopped with %d incomplete todo item(s):\n%s\n\n"+
			"Please continue working on these tasks or mark them complete if finished.\n"+
			"(Reminder %d/%d)\n"+
			"</system-reminder>",
		totalIncomplete,
		todoList,
		attempt,
		maxAttempts,
	)
	return reminder, totalIncomplete
}

// BuildManualEditReminder constructs omp's system reminder injected when the user
// modifies the todo list via /todo.
// upstream: packages/coding-agent/src/modes/controllers/todo-command-controller.ts:120-132
func BuildManualEditReminder(action string, phases []TodoPhase, removed bool) string {
	md := "(empty)"
	if len(phases) > 0 {
		md = strings.TrimRight(PhasesToMarkdown(phases), "\n")
	}
	lines := []string{
		"<system-reminder>",
		fmt.Sprintf("The user manually modified the todo list (%s).", action),
	}
	if removed {
		if len(phases) == 0 {
			lines = append(lines, "The user intentionally cleared the todo list. Do NOT recreate or re-populate it unless the user explicitly asks; continue the current request without a todo list.")
		} else {
			lines = append(lines, "The user intentionally removed the entries no longer shown below. Do NOT re-add them unless the user explicitly asks.")
		}
	}
	lines = append(lines, "Current todo list:", "", md, "</system-reminder>")
	return strings.Join(lines, "\n")
}
