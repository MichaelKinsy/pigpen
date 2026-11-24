package todo

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// WidgetKey is the SDK widget identifier for the todo HUD above the editor.
	WidgetKey = "oh-my-pig:todo"

	// subsequentStageCap and activeTaskCap match omp's HUD budgets.
	// upstream: packages/coding-agent/src/modes/interactive-mode.ts:4304-4305
	subsequentStageCap = 4
	activeTaskCap      = 5

	// collapsedClosedContext is the number of closed tasks kept directly above
	// the open window so a completion remains visible as it lands.
	// upstream: packages/tui/src/tools/todo.ts:151
	collapsedClosedContext = 1
)

var (
	ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
)

// sanitizeDisplayText strips ANSI escape sequences and C0/C1 control characters
// and replaces tabs with spaces so hostile task labels cannot corrupt terminal output.
// upstream: packages/tui/src/tools/todo.ts:301-303
func sanitizeDisplayText(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("   ")
		case r < 0x20 || r == 0x7f:
			// Strip C0 control chars and DEL.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PhaseRomanNumeral returns the one-based ASCII roman numeral (I, II, III, IV, …).
// upstream: packages/tui/src/tools/todo.ts:280-291
func PhaseRomanNumeral(oneBased int) string {
	if oneBased <= 0 {
		return ""
	}
	pairs := []struct {
		val int
		sym string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	rem := oneBased
	for _, p := range pairs {
		for rem >= p.val {
			b.WriteString(p.sym)
			rem -= p.val
		}
	}
	return b.String()
}

// FormatPhaseDisplayName formats a multi-phase display header: `I. Foundation`.
// upstream: packages/tui/src/tools/todo.ts:312-314
func FormatPhaseDisplayName(name string, oneBased int) string {
	return PhaseRomanNumeral(oneBased) + ". " + sanitizeDisplayText(name)
}

// CollapsedTodoSelection is the result of SelectCollapsedTodos.
// upstream: packages/tui/src/tools/todo.ts:138-141
type CollapsedTodoSelection struct {
	Items   []TodoItem
	Summary string
}

func pluralize(noun string, count int) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}

func formatMoreItems(count int, noun string) string {
	return fmt.Sprintf("… %d more %s", count, pluralize(noun, count))
}

func selectWithinCap(base []TodoItem, capN int) CollapsedTodoSelection {
	if len(base) <= capN+1 {
		return CollapsedTodoSelection{Items: append([]TodoItem(nil), base...)}
	}
	var active []TodoItem
	firstActiveIdx := -1
	for i, task := range base {
		if task.Status == StatusInProgress {
			if firstActiveIdx == -1 {
				firstActiveIdx = i
			}
			active = append(active, task)
		}
	}
	if len(active) > capN {
		hiddenActive := len(active) - capN
		return CollapsedTodoSelection{
			Items:   append([]TodoItem(nil), active[:capN]...),
			Summary: fmt.Sprintf("… %d more active %s", hiddenActive, pluralize("todo", hiddenActive)),
		}
	}
	startIdx := 0
	if firstActiveIdx >= 0 {
		startIdx = firstActiveIdx
	}
	var fill []TodoItem
	for i := startIdx; i < len(base) && len(active)+len(fill) < capN; i++ {
		if base[i].Status == StatusInProgress {
			continue
		}
		fill = append(fill, base[i])
	}
	items := make([]TodoItem, 0, len(active)+len(fill))
	items = append(items, active...)
	items = append(items, fill...)
	hidden := len(base) - len(items)
	summary := ""
	if hidden > 0 {
		summary = formatMoreItems(hidden, "todo")
	}
	return CollapsedTodoSelection{Items: items, Summary: summary}
}

// SelectCollapsedTodos applies omp's walking-viewport selection for a phase's collapsed preview.
// upstream: packages/tui/src/tools/todo.ts:215-227
func SelectCollapsedTodos(tasks []TodoItem, capN int) CollapsedTodoSelection {
	var open []TodoItem
	var closed []TodoItem
	for _, t := range tasks {
		if IsClosedTodo(t) {
			closed = append(closed, t)
		} else {
			open = append(open, t)
		}
	}
	if len(open) == 0 {
		return selectWithinCap(tasks, capN)
	}
	var lead []TodoItem
	if len(closed) > 0 {
		start := len(closed) - collapsedClosedContext
		if start < 0 {
			start = 0
		}
		lead = closed[start:]
	}
	sel := selectWithinCap(open, capN)
	items := make([]TodoItem, 0, len(lead)+len(sel.Items))
	items = append(items, lead...)
	items = append(items, sel.Items...)
	return CollapsedTodoSelection{Items: items, Summary: sel.Summary}
}

// formatTodoMarkLine formats one task line using omp's checkbox marks and status tags.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:4070-4086
func formatTodoMarkLine(todo TodoItem) string {
	content := sanitizeDisplayText(todo.Content)
	switch todo.Status {
	case StatusCompleted:
		return "☑ " + content
	case StatusInProgress:
		return "☐ " + content
	case StatusAbandoned:
		return "☐ " + content + " (dropped)"
	case StatusBlocked:
		if todo.Blocker != "" {
			return "☐ " + content + " (blocked: " + sanitizeDisplayText(todo.Blocker) + ")"
		}
		return "☐ " + content + " (blocked)"
	default:
		return "☐ " + content
	}
}

func renderTreeLines(items []TodoItem, summary string) []string {
	total := len(items)
	hasSummary := summary != ""
	if hasSummary {
		total++
	}
	out := make([]string, 0, total)
	for i, item := range items {
		branch := "├─ "
		if i == total-1 {
			branch = "└─ "
		}
		out = append(out, branch+formatTodoMarkLine(item))
	}
	if hasSummary {
		out = append(out, "└─ "+summary)
	}
	return out
}

func activePhaseIndex(phases []TodoPhase) int {
	for i, p := range phases {
		for _, t := range p.Tasks {
			if t.Status == StatusInProgress {
				return i
			}
		}
	}
	for i, p := range phases {
		for _, t := range p.Tasks {
			if t.Status == StatusPending {
				return i
			}
		}
	}
	return 0
}

// RenderWidgetLines builds the HUD widget lines displayed above the editor in omp's
// wording and marks. Returns nil when there are no non-empty phases.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:4292-4408
func RenderWidgetLines(phases []TodoPhase, expanded bool) []string {
	var nonEmpty []TodoPhase
	for _, p := range phases {
		if len(p.Tasks) > 0 {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}

	multiPhase := len(nonEmpty) > 1
	activeIdx := activePhaseIndex(nonEmpty)

	baseIdx := activeIdx
	endIdx := activeIdx + 1 + subsequentStageCap
	if expanded {
		baseIdx = 0
		endIdx = len(nonEmpty)
	}
	if endIdx > len(nonEmpty) {
		endIdx = len(nonEmpty)
	}
	phaseSlice := nonEmpty[baseIdx:endIdx]
	hiddenStages := len(nonEmpty) - baseIdx - len(phaseSlice)

	var lines []string
	lines = append(lines, "TODO")

	pushBlock := func(rows []string) {
		if len(rows) == 0 {
			return
		}
		lines = append(lines, " ├─ "+rows[0])
		for _, r := range rows[1:] {
			lines = append(lines, " │  "+r)
		}
	}

	for i, phase := range phaseSlice {
		oneBased := baseIdx + i + 1
		isActive := baseIdx+i == activeIdx
		label := sanitizeDisplayText(phase.Name)
		if multiPhase {
			label = FormatPhaseDisplayName(phase.Name, oneBased)
		}
		done := 0
		for _, t := range phase.Tasks {
			if IsClosedTodo(t) {
				done++
			}
		}
		header := fmt.Sprintf("%s · %d/%d", label, done, len(phase.Tasks))
		if !isActive && !expanded {
			pushBlock([]string{header})
			continue
		}
		var taskLines []string
		if expanded {
			taskLines = renderTreeLines(phase.Tasks, "")
		} else {
			sel := SelectCollapsedTodos(phase.Tasks, activeTaskCap)
			taskLines = renderTreeLines(sel.Items, sel.Summary)
		}
		rows := make([]string, 0, 1+len(taskLines))
		rows = append(rows, header)
		rows = append(rows, taskLines...)
		pushBlock(rows)
	}

	if hiddenStages > 0 {
		pushBlock([]string{formatMoreItems(hiddenStages, "stage")})
	}

	lines = append(lines, " └─────")
	return lines
}
