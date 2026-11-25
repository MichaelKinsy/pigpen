package todo

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// defaultInitPhase is the phase name for `init` given a flat `items` list with no explicit `phase`.
// upstream: packages/coding-agent/src/tools/todo.ts:330
const defaultInitPhase = "Tasks"

var (
	taskIDPattern       = regexp.MustCompile(`^task-\d+$`)
	mdHeadingRe         = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
	mdTaskRe            = regexp.MustCompile(`^[-*+]\s*\\?\[(.?)\\?\]\s+(.+?)\s*$`)
	internalURLSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	taskListDelimiters  = []struct {
		pattern *regexp.Regexp
		partial bool
	}{
		{regexp.MustCompile(`\r?\n`), true},
		{regexp.MustCompile(`;`), true},
		{regexp.MustCompile(`,`), true},
		{regexp.MustCompile(`\s+`), false},
		{regexp.MustCompile(`[\s,;]+`), false},
	}
)

func normalizeTaskPart(part string) string {
	trimmed := strings.TrimSpace(part)
	if len(trimmed) >= 2 {
		first, last := trimmed[0], trimmed[len(trimmed)-1]
		if (first == '"' || first == '\'' || first == '`') && last == first {
			return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		}
	}
	return trimmed
}

func splitTaskList(content string, known map[string]struct{}) []string {
	if _, ok := known[content]; ok {
		return []string{content}
	}
	var partial []string
	for _, d := range taskListDelimiters {
		raw := d.pattern.Split(content, -1)
		parts := make([]string, 0, len(raw))
		for _, p := range raw {
			if norm := normalizeTaskPart(p); norm != "" {
				parts = append(parts, norm)
			}
		}
		named := 0
		for _, p := range parts {
			if _, ok := known[p]; ok {
				named++
			}
		}
		if named > 0 && named == len(parts) {
			return parts
		}
		if partial == nil && d.partial && named > 0 && len(parts) > 1 {
			partial = parts
		}
	}
	if partial != nil {
		return partial
	}
	return []string{content}
}

// TodoTaskTargets expands a TodoParams's task/tasks into target task contents.
// upstream: packages/tui/src/tools/todo.ts:169-174
func TodoTaskTargets(phases []TodoPhase, entry TodoParams) []string {
	var raw []string
	if len(entry.Tasks) > 0 {
		raw = entry.Tasks
	} else if entry.Task != "" {
		raw = []string{entry.Task}
	} else {
		return nil
	}
	known := make(map[string]struct{})
	for _, p := range phases {
		for _, t := range p.Tasks {
			known[t.Content] = struct{}{}
		}
	}
	var out []string
	for _, c := range raw {
		out = append(out, splitTaskList(c, known)...)
	}
	return out
}

type taskRef struct {
	phaseIdx int
	taskIdx  int
}

func findTaskByContent(phases []TodoPhase, content string) (taskRef, bool) {
	for pi := range phases {
		for ti := range phases[pi].Tasks {
			if phases[pi].Tasks[ti].Content == content {
				return taskRef{phaseIdx: pi, taskIdx: ti}, true
			}
		}
	}
	return taskRef{}, false
}

func findPhaseByName(phases []TodoPhase, name string) (int, bool) {
	for i := range phases {
		if phases[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

func todoTransitionKey(phase, content string) string {
	return phase + "\x00" + content
}

// getCompletionTransitions returns tasks that transitioned to completed between previous and updated.
// upstream: packages/coding-agent/src/tools/todo.ts:107-126
func getCompletionTransitions(previous, updated []TodoPhase) []TodoCompletionTransition {
	prevStatus := make(map[string]TodoStatus)
	for _, phase := range previous {
		for _, task := range phase.Tasks {
			prevStatus[todoTransitionKey(phase.Name, task.Content)] = task.Status
		}
	}
	var transitions []TodoCompletionTransition
	for _, phase := range updated {
		for _, task := range phase.Tasks {
			if task.Status != StatusCompleted {
				continue
			}
			prev, ok := prevStatus[todoTransitionKey(phase.Name, task.Content)]
			if ok && prev != StatusCompleted {
				transitions = append(transitions, TodoCompletionTransition{
					Phase:   phase.Name,
					Content: task.Content,
				})
			}
		}
	}
	return transitions
}

// normalizeInProgressTask enforces at most one in_progress task across all phases;
// if none is in_progress, promotes the earliest pending task in phase order.
// upstream: packages/coding-agent/src/tools/todo.ts:128-143
func normalizeInProgressTask(phases []TodoPhase) {
	var inProgress []*TodoItem
	var firstPending *TodoItem
	for pi := range phases {
		for ti := range phases[pi].Tasks {
			t := &phases[pi].Tasks[ti]
			if t.Status == StatusInProgress {
				inProgress = append(inProgress, t)
			} else if firstPending == nil && t.Status == StatusPending {
				firstPending = t
			}
		}
	}
	if len(inProgress) > 1 {
		for _, t := range inProgress[1:] {
			t.Status = StatusPending
		}
	}
	if len(inProgress) > 0 {
		return
	}
	if firstPending != nil {
		firstPending.Status = StatusInProgress
	}
}

func resolveTaskOrError(phases []TodoPhase, content string, errs *[]string) (taskRef, bool) {
	if content == "" {
		*errs = append(*errs, "Missing task content")
		return taskRef{}, false
	}
	ref, ok := findTaskByContent(phases, content)
	if !ok {
		if taskIDPattern.MatchString(content) {
			*errs = append(*errs, fmt.Sprintf(
				`Task "%s" not found. Tasks are referenced by content, not by IDs — pass the task's full text from the previous result.`,
				content,
			))
		} else {
			total := 0
			for _, p := range phases {
				total += len(p.Tasks)
			}
			hint := ""
			if total == 0 {
				hint = " (todo list is empty — was it replaced or not yet created?)"
			}
			*errs = append(*errs, fmt.Sprintf(`Task "%s" not found%s`, content, hint))
		}
	}
	return ref, ok
}

func resolvePhaseOrError(phases []TodoPhase, name string, errs *[]string) (int, bool) {
	if name == "" {
		*errs = append(*errs, "Missing phase name")
		return -1, false
	}
	idx, ok := findPhaseByName(phases, name)
	if !ok {
		*errs = append(*errs, fmt.Sprintf(`Phase "%s" not found`, name))
	}
	return idx, ok
}

func resolveTasksOrError(phases []TodoPhase, contents []string, errs *[]string) []taskRef {
	hits := make([]taskRef, 0, len(contents))
	for _, c := range contents {
		if ref, ok := resolveTaskOrError(phases, c, errs); ok {
			hits = append(hits, ref)
		}
	}
	return hits
}

func namedTaskContents(phases []TodoPhase, entry TodoParams) []string {
	contents := TodoTaskTargets(phases, entry)
	if len(contents) > 0 {
		return contents
	}
	return entry.Items
}

func getTaskTargets(phases []TodoPhase, entry TodoParams, errs *[]string) []*TodoItem {
	contents := namedTaskContents(phases, entry)
	if len(contents) > 0 {
		refs := resolveTasksOrError(phases, contents, errs)
		out := make([]*TodoItem, len(refs))
		for i, ref := range refs {
			out[i] = &phases[ref.phaseIdx].Tasks[ref.taskIdx]
		}
		return out
	}
	if entry.Phase != "" {
		idx, ok := resolvePhaseOrError(phases, entry.Phase, errs)
		if !ok {
			return nil
		}
		out := make([]*TodoItem, len(phases[idx].Tasks))
		for i := range phases[idx].Tasks {
			out[i] = &phases[idx].Tasks[i]
		}
		return out
	}
	*errs = append(*errs, fmt.Sprintf("%s needs a task, items or a phase", entry.Op))
	return nil
}

func initPhases(entry TodoParams, errs *[]string) []TodoPhase {
	var list []InitListEntry
	hasList := false
	if len(entry.List) > 0 {
		list = entry.List
		hasList = true
	} else if len(entry.Items) > 0 {
		phaseName := entry.Phase
		if phaseName == "" {
			phaseName = defaultInitPhase
		}
		list = []InitListEntry{{Phase: phaseName, Items: entry.Items}}
		hasList = true
	} else if entry.ListSet || entry.List != nil {
		list = entry.List
		hasList = true
	}
	if !hasList {
		*errs = append(*errs, "Missing list for init operation")
		return []TodoPhase{}
	}
	seenPhases := make(map[string]struct{}, len(list))
	seenTasks := make(map[string]struct{})
	phases := make([]TodoPhase, len(list))
	for i, le := range list {
		name := le.Phase
		if name == "" {
			name = defaultInitPhase
		}
		if _, dup := seenPhases[name]; dup {
			*errs = append(*errs, fmt.Sprintf(`Duplicate phase "%s" in init list`, name))
		}
		seenPhases[name] = struct{}{}
		tasks := make([]TodoItem, len(le.Items))
		for j, content := range le.Items {
			if _, dup := seenTasks[content]; dup {
				*errs = append(*errs, fmt.Sprintf(`Duplicate task "%s" in init list`, content))
			}
			seenTasks[content] = struct{}{}
			tasks[j] = TodoItem{Content: content, Status: StatusPending}
		}
		phases[i] = TodoPhase{Name: name, Tasks: tasks}
	}
	return phases
}

func appendItems(phases []TodoPhase, entry TodoParams, errs *[]string) []TodoPhase {
	if entry.PhaseSet && entry.Phase == "" {
		*errs = append(*errs, "Missing phase name for append operation")
		return phases
	}
	if len(entry.Items) == 0 {
		*errs = append(*errs, "Missing items for append operation")
		return phases
	}
	seen := make(map[string]struct{}, len(entry.Items))
	hasDup := false
	for _, content := range entry.Items {
		_, dupInBatch := seen[content]
		_, exists := findTaskByContent(phases, content)
		if dupInBatch || exists {
			*errs = append(*errs, fmt.Sprintf(`Task "%s" already exists`, content))
			hasDup = true
		}
		seen[content] = struct{}{}
	}
	if hasDup {
		return phases
	}
	name := entry.Phase
	if !entry.PhaseSet && name == "" {
		if active := NextActionableTask(phases); active != nil {
			if ref, ok := findTaskByContent(phases, active.Content); ok {
				name = phases[ref.phaseIdx].Name
			}
		}
		if name == "" && len(phases) > 0 {
			name = phases[len(phases)-1].Name
		}
		if name == "" {
			name = defaultInitPhase
		}
	}
	idx, ok := findPhaseByName(phases, name)
	if !ok {
		phases = append(phases, TodoPhase{Name: name, Tasks: []TodoItem{}})
		idx = len(phases) - 1
	}
	for _, content := range entry.Items {
		phases[idx].Tasks = append(phases[idx].Tasks, TodoItem{Content: content, Status: StatusPending})
	}
	return phases
}

func removeTasks(phases []TodoPhase, entry TodoParams, errs *[]string) []TodoPhase {
	contents := namedTaskContents(phases, entry)
	if len(contents) > 0 {
		toRemove := make(map[string]struct{}, len(contents))
		for _, ref := range resolveTasksOrError(phases, contents, errs) {
			toRemove[phases[ref.phaseIdx].Tasks[ref.taskIdx].Content] = struct{}{}
		}
		for pi := range phases {
			kept := phases[pi].Tasks[:0]
			for _, t := range phases[pi].Tasks {
				if _, rm := toRemove[t.Content]; !rm {
					kept = append(kept, t)
				}
			}
			phases[pi].Tasks = kept
		}
		return phases
	}
	if entry.Phase != "" {
		idx, ok := resolvePhaseOrError(phases, entry.Phase, errs)
		if !ok {
			return phases
		}
		phases[idx].Tasks = []TodoItem{}
		return phases
	}
	*errs = append(*errs, "rm needs a task, items or a phase")
	return phases
}

func applyEntry(phases []TodoPhase, entry TodoParams, errs *[]string) []TodoPhase {
	switch entry.Op {
	case OpInit:
		return initPhases(entry, errs)
	case OpStart:
		contents := TodoTaskTargets(phases, entry)
		if len(contents) > 1 {
			*errs = append(*errs, "start takes a single task: only one task can be in progress")
			return phases
		}
		target := ""
		if len(contents) == 1 {
			target = contents[0]
		}
		ref, ok := resolveTaskOrError(phases, target, errs)
		if !ok {
			return phases
		}
		for pi := range phases {
			for ti := range phases[pi].Tasks {
				if phases[pi].Tasks[ti].Status == StatusInProgress && (pi != ref.phaseIdx || ti != ref.taskIdx) {
					phases[pi].Tasks[ti].Status = StatusPending
				}
			}
		}
		phases[ref.phaseIdx].Tasks[ref.taskIdx].Status = StatusInProgress
		return phases
	case OpDone:
		for _, t := range getTaskTargets(phases, entry, errs) {
			t.Status = StatusCompleted
		}
		return phases
	case OpDrop:
		for _, t := range getTaskTargets(phases, entry, errs) {
			t.Status = StatusAbandoned
		}
		return phases
	case OpRm:
		return removeTasks(phases, entry, errs)
	case OpAppend:
		return appendItems(phases, entry, errs)
	case OpView:
		return phases
	default:
		*errs = append(*errs, fmt.Sprintf("Unknown op %q", entry.Op))
		return phases
	}
}

// ApplyParams applies one todo operation and normalizes the in_progress pointer.
// upstream: packages/coding-agent/src/tools/todo.ts:511-516
func ApplyParams(phases []TodoPhase, params TodoParams) ([]TodoPhase, []string) {
	var errs []string
	next := applyEntry(phases, params, &errs)
	normalizeInProgressTask(next)
	return next, errs
}

// ApplyOpsToPhases applies a slice of todo ops to a cloned copy of currentPhases.
// upstream: packages/coding-agent/src/tools/todo.ts:519-530
func ApplyOpsToPhases(currentPhases []TodoPhase, ops []TodoParams) ([]TodoPhase, []string) {
	var errs []string
	next := ClonePhases(currentPhases)
	for _, op := range ops {
		next = applyEntry(next, op, &errs)
	}
	normalizeInProgressTask(next)
	return next, errs
}

// inferTodoOp infers a missing `op` from unambiguous argument shapes.
// upstream: packages/coding-agent/src/tools/todo.ts:481-488
func inferTodoOp(raw map[string]any, hasExistingPhases bool) (TodoOperation, bool) {
	if list, ok := raw["list"].([]any); ok && len(list) > 0 {
		return OpInit, true
	}
	if items, ok := raw["items"].([]any); ok && len(items) > 0 {
		if phase, ok := raw["phase"].(string); ok && phase != "" {
			return OpAppend, true
		}
		if !hasExistingPhases {
			return OpInit, true
		}
	}
	return "", false
}

// formatTodoActual mirrors omptype's describeValue/describeKind formatting
// (upstream: packages/omptype/src/errors.ts:146-198).
func formatTodoActual(v any, domain bool) string {
	if v == nil {
		return "null"
	}
	switch x := v.(type) {
	case string:
		if domain {
			return "a string"
		}
		if len(x) <= 40 {
			b, _ := json.Marshal(x)
			return string(b)
		}
		return fmt.Sprintf("a string (length %d)", len(x))
	case bool:
		if domain {
			return "boolean"
		}
		if x {
			return "true"
		}
		return "false"
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		if domain {
			return "a number"
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		if domain {
			return "a number"
		}
		return strconv.Itoa(x)
	case int64:
		if domain {
			return "a number"
		}
		return strconv.FormatInt(x, 10)
	case []any:
		if domain {
			return "an object"
		}
		return "an array"
	case map[string]any:
		if domain {
			return "an object"
		}
		b, err := json.Marshal(x)
		if err != nil {
			return "an object"
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ResolveTodoParams validates raw tool arguments, repairing an omitted `op` for unambiguous shapes.
// upstream: packages/coding-agent/src/tools/todo.ts:498-509
func ResolveTodoParams(raw map[string]any, hasExistingPhases bool) (TodoParams, string) {
	if raw == nil {
		return TodoParams{}, `Invalid todo arguments: op must be "init", "start", "done", "rm", "drop", "append" or "view" (was missing)`
	}
	opVal, hasOp := raw["op"]
	var opStr string
	if !hasOp || opVal == nil {
		inferred, ok := inferTodoOp(raw, hasExistingPhases)
		if !ok {
			return TodoParams{}, `Invalid todo arguments: op must be "init", "start", "done", "rm", "drop", "append" or "view" (was missing)`
		}
		opStr = string(inferred)
	} else {
		s, ok := opVal.(string)
		if !ok || !IsValidOp(s) {
			return TodoParams{}, fmt.Sprintf(`Invalid todo arguments: op must be "init", "start", "done", "rm", "drop", "append" or "view" (was %s)`, formatTodoActual(opVal, false))
		}
		opStr = s
	}

	params := TodoParams{Op: TodoOperation(opStr)}

	if v, ok := raw["task"]; ok {
		switch tv := v.(type) {
		case string:
			params.Task = tv
		case []any:
			tasks := make([]string, len(tv))
			for i, elem := range tv {
				s, ok := elem.(string)
				if !ok {
					return TodoParams{}, fmt.Sprintf("Invalid todo arguments: task[%d] must be a string (was %s)", i, formatTodoActual(elem, true))
				}
				tasks[i] = s
			}
			params.Tasks = tasks
		default:
			return TodoParams{}, fmt.Sprintf("Invalid todo arguments: task must be a string or an array (was %s)", formatTodoActual(v, true))
		}
	}
	if v, ok := raw["phase"]; ok {
		s, ok := v.(string)
		if !ok {
			return TodoParams{}, fmt.Sprintf("Invalid todo arguments: phase must be a string (was %s)", formatTodoActual(v, true))
		}
		params.Phase = s
		params.PhaseSet = true
	}
	if v, ok := raw["items"]; ok {
		arr, ok := v.([]any)
		if !ok {
			return TodoParams{}, fmt.Sprintf("Invalid todo arguments: items must be an array (was %s)", formatTodoActual(v, true))
		}
		items := make([]string, len(arr))
		for i, elem := range arr {
			s, ok := elem.(string)
			if !ok {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: items[%d] must be a string (was %s)", i, formatTodoActual(elem, true))
			}
			items[i] = s
		}
		params.Items = items
	}
	if v, ok := raw["list"]; ok {
		arr, ok := v.([]any)
		if !ok {
			return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list must be an array (was %s)", formatTodoActual(v, true))
		}
		list := make([]InitListEntry, len(arr))
		for i, elem := range arr {
			m, ok := elem.(map[string]any)
			if !ok {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d] must be an object (was %s)", i, formatTodoActual(elem, true))
			}
			rawPhase, hasPhase := m["phase"]
			if !hasPhase {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d].phase must be a string (was missing)", i)
			}
			phase, ok := rawPhase.(string)
			if !ok {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d].phase must be a string (was %s)", i, formatTodoActual(rawPhase, true))
			}
			rawItemsVal, hasItems := m["items"]
			if !hasItems {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d].items must be an array (was missing)", i)
			}
			rawItems, ok := rawItemsVal.([]any)
			if !ok {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d].items must be an array (was %s)", i, formatTodoActual(rawItemsVal, true))
			}
			if len(rawItems) == 0 {
				return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d].items must be at least length 1 (was 0)", i)
			}
			items := make([]string, len(rawItems))
			for j, it := range rawItems {
				s, ok := it.(string)
				if !ok {
					return TodoParams{}, fmt.Sprintf("Invalid todo arguments: list[%d].items[%d] must be a string (was %s)", i, j, formatTodoActual(it, true))
				}
				items[j] = s
			}
			list[i] = InitListEntry{Phase: phase, Items: items}
		}
		params.List = list
		params.ListSet = true
	}
	return params, ""
}

// FormatSummary renders the model-facing result text for a todo call.
// upstream: packages/coding-agent/src/tools/todo.ts:617-679
func FormatSummary(phases []TodoPhase, errs []string, readOnly bool) string {
	var allTasks []TodoItem
	for _, p := range phases {
		allTasks = append(allTasks, p.Tasks...)
	}
	if len(allTasks) == 0 {
		if len(errs) > 0 {
			return "Errors: " + strings.Join(errs, "; ")
		}
		if readOnly {
			return "Todo list is empty."
		}
		return "Todo list cleared."
	}

	type remainingItem struct {
		TodoItem
		phase string
	}
	var remaining []remainingItem
	currentIdx := -1
	for i, phase := range phases {
		for _, task := range phase.Tasks {
			if task.Status == StatusInProgress {
				if currentIdx == -1 {
					currentIdx = i
				}
			}
			if task.Status == StatusPending || task.Status == StatusInProgress {
				remaining = append(remaining, remainingItem{TodoItem: task, phase: phase.Name})
			}
		}
	}
	if currentIdx == -1 {
		for i, phase := range phases {
			for _, task := range phase.Tasks {
				if task.Status == StatusPending {
					currentIdx = i
					break
				}
			}
			if currentIdx != -1 {
				break
			}
		}
	}
	if currentIdx == -1 {
		currentIdx = len(phases) - 1
	}
	current := phases[currentIdx]
	currentDone := 0
	for _, task := range current.Tasks {
		if IsClosedTodo(task) {
			currentDone++
		}
	}

	var lines []string
	if len(errs) > 0 {
		lines = append(lines, "Errors: "+strings.Join(errs, "; "))
	}
	if len(remaining) == 0 {
		lines = append(lines, "Remaining items: none.")
	} else {
		lines = append(lines, fmt.Sprintf("Remaining items (%d):", len(remaining)))
		for _, task := range remaining {
			lines = append(lines, fmt.Sprintf("  - %s [%s] (%s)", task.Content, task.Status, task.phase))
		}
	}

	closedAll := 0
	for _, task := range allTasks {
		if IsClosedTodo(task) {
			closedAll++
		}
	}

	workedAhead := false
	for i := currentIdx + 1; i < len(phases); i++ {
		for _, task := range phases[i].Tasks {
			if IsClosedTodo(task) {
				workedAhead = true
				break
			}
		}
		if workedAhead {
			break
		}
	}

	lines = append(lines, fmt.Sprintf("Overall: %d/%d done, %d open.", closedAll, len(allTasks), len(remaining)))

	activeSuffix := "."
	if workedAhead {
		activeSuffix = " — the in-progress pointer auto-advances to the earliest open task on each completion, so it can sit behind out-of-order work (nothing was un-completed)."
	}
	lines = append(lines, fmt.Sprintf(`Active phase %d/%d "%s" (%d/%d)%s`, currentIdx+1, len(phases), current.Name, currentDone, len(current.Tasks), activeSuffix))

	for _, phase := range phases {
		lines = append(lines, "  "+phase.Name+":")
		for _, task := range phase.Tasks {
			checkbox := "[ ]"
			if task.Status == StatusCompleted {
				checkbox = "[X]"
			}
			tag := ""
			switch task.Status {
			case StatusInProgress:
				tag = " (in progress)"
			case StatusAbandoned:
				tag = " (dropped)"
			}
			lines = append(lines, fmt.Sprintf("    - %s %s%s", checkbox, task.Content, tag))
		}
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Markdown round-trip
// =============================================================================

func statusToMarker(s TodoStatus) string {
	switch s {
	case StatusInProgress:
		return "/"
	case StatusCompleted:
		return "x"
	case StatusAbandoned:
		return "-"
	default:
		return " "
	}
}

func markerToStatus(m string) (TodoStatus, bool) {
	switch m {
	case " ", "":
		return StatusPending, true
	case "x", "X":
		return StatusCompleted, true
	case "/", ">":
		return StatusInProgress, true
	case "-", "~":
		return StatusAbandoned, true
	default:
		return "", false
	}
}

// ResolveTodoMarkdownPath resolves a user-supplied path for /todo export or import,
// defaulting to TODO.md under cwd and rejecting internal URL schemes.
// upstream: packages/coding-agent/src/tools/todo.ts:544-547
func ResolveTodoMarkdownPath(input, cwd string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) >= 2 && ((trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') || (trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'')) {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	if trimmed == "" {
		trimmed = "TODO.md"
	}
	if internalURLSchemeRe.MatchString(trimmed) {
		return "", fmt.Errorf("internal scheme paths are not supported: %s", trimmed)
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if trimmed == "~" {
				trimmed = home
			} else {
				trimmed = filepath.Join(home, trimmed[2:])
			}
		}
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), nil
	}
	return filepath.Join(cwd, trimmed), nil
}

// PhasesToMarkdown renders todo phases as a Markdown checklist.
// upstream: packages/coding-agent/src/tools/todo.ts:550-566
func PhasesToMarkdown(phases []TodoPhase) string {
	if len(phases) == 0 {
		return "# Todos\n"
	}
	var out []string
	for i, phase := range phases {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, "# "+phase.Name)
		for _, task := range phase.Tasks {
			out = append(out, fmt.Sprintf("- [%s] %s", statusToMarker(task.Status), task.Content))
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// MarkdownToPhases parses a Markdown checklist back into todo phases.
// upstream: packages/coding-agent/src/tools/todo.ts:581-632
func MarkdownToPhases(md string) ([]TodoPhase, []string) {
	var errs []string
	phases := []TodoPhase{}
	currentIdx := -1

	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	for lineNum, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if m := mdHeadingRe.FindStringSubmatch(trimmed); m != nil {
			phases = append(phases, TodoPhase{Name: strings.TrimSpace(m[1]), Tasks: []TodoItem{}})
			currentIdx = len(phases) - 1
			continue
		}
		if m := mdTaskRe.FindStringSubmatch(trimmed); m != nil {
			if currentIdx == -1 {
				phases = append(phases, TodoPhase{Name: "Todos", Tasks: []TodoItem{}})
				currentIdx = len(phases) - 1
			}
			marker := m[1]
			status, ok := markerToStatus(marker)
			if !ok {
				errs = append(errs, fmt.Sprintf(`Line %d: unknown status marker "[%s]" (use [ ], [x], [/], [-])`, lineNum+1, marker))
				continue
			}
			rawContent := strings.TrimSpace(m[2])
			phases[currentIdx].Tasks = append(phases[currentIdx].Tasks, TodoItem{
				Content: rawContent,
				Status:  status,
			})
			continue
		}
		errs = append(errs, fmt.Sprintf(`Line %d: unrecognized syntax "%s"`, lineNum+1, trimmed))
	}

	normalizeInProgressTask(phases)
	return phases, errs
}
