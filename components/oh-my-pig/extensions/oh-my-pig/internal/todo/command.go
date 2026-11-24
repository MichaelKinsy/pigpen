package todo

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// todoUsage lists the /todo sub-commands ported in oh-my-pig.
// upstream: packages/coding-agent/src/modes/controllers/todo-command-controller.ts:15-29
const todoUsage = `Usage: /todo <verb> [args]
  /todo                              Show current todos
  /todo expand                       Show every phase and task in the HUD
  /todo collapse                     Restore the bounded HUD preview
  /todo export [<path>]              Write todos to file (default: TODO.md)
  /todo import [<path>]              Replace todos from file (default: TODO.md)
  /todo append [<phase>] <task...>   Append a task; phase fuzzy-matched or auto-created
  /todo start  <task>                Mark task in_progress (fuzzy content match)
  /todo done   [<task|phase>]        Mark task/phase/all completed
  /todo drop   [<task|phase>]        Mark task/phase/all abandoned
  /todo rm     [<task|phase>]        Remove task/phase/all`

// tokenize splits command arguments respecting double-quoted strings and backslash escapes.
// upstream: packages/coding-agent/src/modes/controllers/todo-command-controller.ts:35-60
func tokenize(input string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch == '\\' && i+1 < len(input) {
			i++
			cur.WriteByte(input[i])
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r') {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		runes := []rune(w)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

func titleCaseSentence(s string) string {
	trimmed := strings.TrimSpace(s)
	runes := []rune(trimmed)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// findPhaseFuzzy matches a phase by exact case-insensitive name, unique prefix, or unique substring.
// upstream: packages/coding-agent/src/modes/controllers/todo-command-controller.ts:78-90
func findPhaseFuzzy(phases []TodoPhase, query string) (int, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return -1, false
	}
	for i, p := range phases {
		if strings.ToLower(p.Name) == q {
			return i, true
		}
	}
	var prefixHits []int
	for i, p := range phases {
		if strings.HasPrefix(strings.ToLower(p.Name), q) {
			prefixHits = append(prefixHits, i)
		}
	}
	if len(prefixHits) == 1 {
		return prefixHits[0], true
	}
	var subHits []int
	for i, p := range phases {
		if strings.Contains(strings.ToLower(p.Name), q) {
			subHits = append(subHits, i)
		}
	}
	if len(subHits) == 1 {
		return subHits[0], true
	}
	return -1, false
}

// findTaskFuzzy matches a task by exact case-insensitive content or unique substring
// (preferring a single active hit when ambiguous).
// upstream: packages/coding-agent/src/modes/controllers/todo-command-controller.ts:92-114
func findTaskFuzzy(phases []TodoPhase, query string) (taskRef, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return taskRef{}, false
	}
	for pi, p := range phases {
		for ti, t := range p.Tasks {
			if strings.ToLower(t.Content) == q {
				return taskRef{phaseIdx: pi, taskIdx: ti}, true
			}
		}
	}
	var matches []taskRef
	for pi, p := range phases {
		for ti, t := range p.Tasks {
			if strings.Contains(strings.ToLower(t.Content), q) {
				matches = append(matches, taskRef{phaseIdx: pi, taskIdx: ti})
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	var active []taskRef
	for _, ref := range matches {
		st := phases[ref.phaseIdx].Tasks[ref.taskIdx].Status
		if st == StatusInProgress || st == StatusPending {
			active = append(active, ref)
		}
	}
	if len(active) == 1 {
		return active[0], true
	}
	return taskRef{}, false
}

func (a *app) handleCommand(ctx sdk.Context, args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		phases := a.getPhases(ctx)
		if len(phases) == 0 {
			ctx.Notify("No todos. Use /todo append <task> to start one.", "info")
			return nil
		}
		ctx.Notify(strings.TrimRight(PhasesToMarkdown(phases), "\n"), "info")
		return nil
	}

	verb := trimmed
	rest := ""
	if idx := strings.IndexAny(trimmed, " \t\n\r"); idx != -1 {
		verb = trimmed[:idx]
		rest = strings.TrimSpace(trimmed[idx+1:])
	}
	verb = strings.ToLower(verb)

	switch verb {
	case "expand":
		a.setExpanded(true)
		a.refreshWidget(ctx)
		return nil
	case "collapse":
		a.setExpanded(false)
		a.refreshWidget(ctx)
		return nil
	case "help", "?":
		ctx.Notify(todoUsage, "info")
		return nil
	case "export":
		return a.cmdExport(ctx, rest)
	case "import":
		return a.cmdImport(ctx, rest)
	case "append":
		return a.cmdAppend(ctx, rest)
	case "start":
		return a.cmdStart(ctx, rest)
	case "done":
		return a.cmdMutateStatus(ctx, rest, StatusCompleted)
	case "drop":
		return a.cmdMutateStatus(ctx, rest, StatusAbandoned)
	case "rm":
		return a.cmdRemove(ctx, rest)
	default:
		ctx.Notify(fmt.Sprintf("Unknown /todo verb %q.\n%s", verb, todoUsage), "error")
		return nil
	}
}

func (a *app) cmdExport(ctx sdk.Context, rest string) error {
	phases := a.getPhases(ctx)
	if len(phases) == 0 {
		ctx.Notify("No todos to export.", "warning")
		return nil
	}
	target, err := ResolveTodoMarkdownPath(rest, ctx.Cwd())
	if err != nil {
		ctx.Notify("Failed to write todos: "+err.Error(), "error")
		return nil
	}
	if err := os.WriteFile(target, []byte(PhasesToMarkdown(phases)), 0o644); err != nil {
		ctx.Notify("Failed to write todos: "+err.Error(), "error")
		return nil
	}
	ctx.Notify("Wrote todos to "+target, "info")
	return nil
}

func (a *app) cmdImport(ctx sdk.Context, rest string) error {
	source, err := ResolveTodoMarkdownPath(rest, ctx.Cwd())
	if err != nil {
		ctx.Notify("Failed to read todos: "+err.Error(), "error")
		return nil
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		ctx.Notify("Failed to read todos: "+err.Error(), "error")
		return nil
	}
	phases, errs := MarkdownToPhases(string(raw))
	if len(errs) > 0 {
		ctx.Notify(fmt.Sprintf("Could not parse %s:\n  %s", source, strings.Join(errs, "\n  ")), "error")
		return nil
	}
	a.commitUserEdit(ctx, phases, "/todo import "+source, false)
	taskCount := 0
	for _, p := range phases {
		taskCount += len(p.Tasks)
	}
	ctx.Notify(fmt.Sprintf("Imported %d phase(s), %d task(s) from %s.", len(phases), taskCount, source), "info")
	return nil
}

func (a *app) cmdAppend(ctx sdk.Context, rest string) error {
	tokens := tokenize(rest)
	if len(tokens) == 0 {
		ctx.Notify("Usage: /todo append [<phase>] <task...>", "error")
		return nil
	}
	current := a.getPhases(ctx)
	var phaseName, content string
	if len(tokens) == 1 {
		content = tokens[0]
	} else {
		phaseName = tokens[0]
		content = strings.Join(tokens[1:], " ")
	}

	next := ClonePhases(current)
	targetIdx := -1
	if phaseName != "" {
		if idx, ok := findPhaseFuzzy(next, phaseName); ok {
			targetIdx = idx
		} else {
			next = append(next, TodoPhase{Name: titleCase(phaseName), Tasks: []TodoItem{}})
			targetIdx = len(next) - 1
		}
	} else if len(next) > 0 {
		targetIdx = len(next) - 1
	} else {
		next = append(next, TodoPhase{Name: "Todos", Tasks: []TodoItem{}})
		targetIdx = 0
	}

	finalContent := titleCaseSentence(content)
	next[targetIdx].Tasks = append(next[targetIdx].Tasks, TodoItem{
		Content: finalContent,
		Status:  StatusPending,
	})
	normalizeInProgressTask(next)
	a.commitUserEdit(ctx, next, "/todo append → "+next[targetIdx].Name, false)
	ctx.Notify(fmt.Sprintf("Appended to %s: %s", next[targetIdx].Name, finalContent), "info")
	return nil
}

func (a *app) cmdStart(ctx sdk.Context, rest string) error {
	if rest == "" {
		ctx.Notify("Usage: /todo start <task>", "error")
		return nil
	}
	current := a.getPhases(ctx)
	ref, ok := findTaskFuzzy(current, rest)
	if !ok {
		ctx.Notify(fmt.Sprintf("No task matched %q. Use /todo to list current tasks.", rest), "error")
		return nil
	}
	taskContent := current[ref.phaseIdx].Tasks[ref.taskIdx].Content
	next, errs := ApplyOpsToPhases(current, []TodoParams{{Op: OpStart, Task: taskContent}})
	if len(errs) > 0 {
		ctx.Notify(strings.Join(errs, "; "), "error")
		return nil
	}
	a.commitUserEdit(ctx, next, "/todo start "+taskContent, false)
	ctx.Notify("Started: "+taskContent, "info")
	return nil
}

func (a *app) cmdMutateStatus(ctx sdk.Context, rest string, target TodoStatus) error {
	op := OpDone
	if target == StatusAbandoned {
		op = OpDrop
	}
	current := a.getPhases(ctx)
	trimmed := strings.TrimSpace(rest)
	if trimmed == "" {
		next, errs := ApplyOpsToPhases(current, []TodoParams{{Op: op}})
		if len(errs) > 0 {
			ctx.Notify(strings.Join(errs, "; "), "error")
			return nil
		}
		a.commitUserEdit(ctx, next, fmt.Sprintf("/todo %s (all)", op), false)
		ctx.Notify(fmt.Sprintf("Marked all tasks %s.", target), "info")
		return nil
	}
	if ref, ok := findTaskFuzzy(current, trimmed); ok {
		taskContent := current[ref.phaseIdx].Tasks[ref.taskIdx].Content
		next, errs := ApplyOpsToPhases(current, []TodoParams{{Op: op, Task: taskContent}})
		if len(errs) > 0 {
			ctx.Notify(strings.Join(errs, "; "), "error")
			return nil
		}
		a.commitUserEdit(ctx, next, fmt.Sprintf("/todo %s %s", op, taskContent), false)
		ctx.Notify(fmt.Sprintf("Marked %s: %s", target, taskContent), "info")
		return nil
	}
	if idx, ok := findPhaseFuzzy(current, trimmed); ok {
		phaseName := current[idx].Name
		next, errs := ApplyOpsToPhases(current, []TodoParams{{Op: op, Phase: phaseName}})
		if len(errs) > 0 {
			ctx.Notify(strings.Join(errs, "; "), "error")
			return nil
		}
		a.commitUserEdit(ctx, next, fmt.Sprintf("/todo %s %s", op, phaseName), false)
		ctx.Notify(fmt.Sprintf("Marked phase %s %s.", phaseName, target), "info")
		return nil
	}
	ctx.Notify(fmt.Sprintf("No task or phase matched %q.", trimmed), "error")
	return nil
}

func (a *app) cmdRemove(ctx sdk.Context, rest string) error {
	current := a.getPhases(ctx)
	trimmed := strings.TrimSpace(rest)
	if trimmed == "" {
		a.commitUserEdit(ctx, []TodoPhase{}, "/todo rm (all)", true)
		ctx.Notify("Cleared all todos.", "info")
		return nil
	}
	if ref, ok := findTaskFuzzy(current, trimmed); ok {
		taskContent := current[ref.phaseIdx].Tasks[ref.taskIdx].Content
		next, errs := ApplyOpsToPhases(current, []TodoParams{{Op: OpRm, Task: taskContent}})
		if len(errs) > 0 {
			ctx.Notify(strings.Join(errs, "; "), "error")
			return nil
		}
		a.commitUserEdit(ctx, next, "/todo rm "+taskContent, true)
		ctx.Notify("Removed: "+taskContent, "info")
		return nil
	}
	if idx, ok := findPhaseFuzzy(current, trimmed); ok {
		phaseName := current[idx].Name
		next, errs := ApplyOpsToPhases(current, []TodoParams{{Op: OpRm, Phase: phaseName}})
		if len(errs) > 0 {
			ctx.Notify(strings.Join(errs, "; "), "error")
			return nil
		}
		a.commitUserEdit(ctx, next, "/todo rm "+phaseName, true)
		ctx.Notify("Removed phase: "+phaseName, "info")
		return nil
	}
	ctx.Notify(fmt.Sprintf("No task or phase matched %q.", trimmed), "error")
	return nil
}

func (a *app) commitUserEdit(ctx sdk.Context, next []TodoPhase, action string, removed bool) {
	a.setPhases(ctx, next)
	_ = ctx.AppendEntry(UserTodoEditCustomType, map[string]any{"phases": next})
	a.refreshWidget(ctx)
	reminder := BuildManualEditReminder(action, next, removed)
	_ = ctx.SendMessage("todo-user-edit", reminder, false, sdk.SendMessageOptions{})
}
