package todo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// defaultInitPhase is the phase name for `init` given a flat `items` list with no explicit `phase`.
// upstream: packages/coding-agent/src/tools/todo.ts:307
const defaultInitPhase = "Tasks"

var (
	taskIDPattern       = regexp.MustCompile(`^task-\d+$`)
	whitespaceRunRe     = regexp.MustCompile(`\s+`)
	mdHeadingRe         = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
	mdTaskRe            = regexp.MustCompile(`^[-*+]\s*\\?\[(.?)\\?\]\s+(.+?)\s*$`)
	mdBlockerCommentRe  = regexp.MustCompile(`^(.*?)\s*<!--\s*blocker:\s*(.*?)\s*-->$`)
	internalURLSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
)

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
				`Task %q not found. Tasks are referenced by content, not by IDs — pass the task's full text from the previous result.`,
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
			*errs = append(*errs, fmt.Sprintf(`Task %q not found%s`, content, hint))
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
		*errs = append(*errs, fmt.Sprintf(`Phase %q not found`, name))
	}
	return idx, ok
}

func getTaskTargets(phases []TodoPhase, entry TodoParams, errs *[]string) []*TodoItem {
	if entry.Task != "" {
		ref, ok := resolveTaskOrError(phases, entry.Task, errs)
		if !ok {
			return nil
		}
		return []*TodoItem{&phases[ref.phaseIdx].Tasks[ref.taskIdx]}
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
	var out []*TodoItem
	for pi := range phases {
		for ti := range phases[pi].Tasks {
			out = append(out, &phases[pi].Tasks[ti])
		}
	}
	return out
}

func initPhases(entry TodoParams, errs *[]string) []TodoPhase {
	list := entry.List
	if len(list) == 0 && len(entry.Items) > 0 {
		phaseName := entry.Phase
		if phaseName == "" {
			phaseName = defaultInitPhase
		}
		list = []InitListEntry{{Phase: phaseName, Items: entry.Items}}
	}
	if len(list) == 0 {
		*errs = append(*errs, "Missing list for init operation")
		return []TodoPhase{}
	}
	seenPhases := make(map[string]struct{}, len(list))
	seenTasks := make(map[string]struct{})
	for _, le := range list {
		if _, dup := seenPhases[le.Phase]; dup {
			*errs = append(*errs, fmt.Sprintf(`Duplicate phase %q in init list`, le.Phase))
		}
		seenPhases[le.Phase] = struct{}{}
		for _, content := range le.Items {
			if _, dup := seenTasks[content]; dup {
				*errs = append(*errs, fmt.Sprintf(`Duplicate task %q in init list`, content))
			}
			seenTasks[content] = struct{}{}
		}
	}
	phases := make([]TodoPhase, len(list))
	for i, le := range list {
		tasks := make([]TodoItem, len(le.Items))
		for j, content := range le.Items {
			tasks[j] = TodoItem{Content: content, Status: StatusPending}
		}
		phases[i] = TodoPhase{Name: le.Phase, Tasks: tasks}
	}
	return phases
}

func appendItems(phases []TodoPhase, entry TodoParams, errs *[]string) []TodoPhase {
	if entry.Phase == "" {
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
			*errs = append(*errs, fmt.Sprintf(`Task %q already exists`, content))
			hasDup = true
		}
		seen[content] = struct{}{}
	}
	if hasDup {
		return phases
	}
	idx, ok := findPhaseByName(phases, entry.Phase)
	if !ok {
		phases = append(phases, TodoPhase{Name: entry.Phase, Tasks: []TodoItem{}})
		idx = len(phases) - 1
	}
	for _, content := range entry.Items {
		phases[idx].Tasks = append(phases[idx].Tasks, TodoItem{Content: content, Status: StatusPending})
	}
	return phases
}

func removeTasks(phases []TodoPhase, entry TodoParams, errs *[]string) []TodoPhase {
	if entry.Task != "" {
		ref, ok := resolveTaskOrError(phases, entry.Task, errs)
		if !ok {
			return phases
		}
		tasks := phases[ref.phaseIdx].Tasks
		phases[ref.phaseIdx].Tasks = append(tasks[:ref.taskIdx], tasks[ref.taskIdx+1:]...)
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
	for i := range phases {
		phases[i].Tasks = []TodoItem{}
	}
	return phases
}

func applyEntry(phases []TodoPhase, entry TodoParams, errs *[]string) []TodoPhase {
	switch entry.Op {
	case OpInit:
		return initPhases(entry, errs)
	case OpStart:
		ref, ok := resolveTaskOrError(phases, entry.Task, errs)
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
	case OpBlock:
		if entry.Task == "" && entry.Phase == "" {
			*errs = append(*errs, "block requires a task or phase target")
			return phases
		}
		reason := strings.TrimSpace(whitespaceRunRe.ReplaceAllString(entry.Reason, " "))
		for _, t := range getTaskTargets(phases, entry, errs) {
			if t.Status != StatusPending && t.Status != StatusInProgress && t.Status != StatusBlocked {
				continue
			}
			t.Status = StatusBlocked
			t.Blocker = reason
		}
		return phases
	case OpUnblock:
		if entry.Task == "" && entry.Phase == "" {
			*errs = append(*errs, "unblock requires a task or phase target")
			return phases
		}
		for _, t := range getTaskTargets(phases, entry, errs) {
			if t.Status == StatusBlocked {
				t.Status = StatusPending
				t.Blocker = ""
			}
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

// ResolveTodoParams validates raw tool arguments, repairing an omitted `op` for unambiguous shapes.
// upstream: packages/coding-agent/src/tools/todo.ts:498-509
func ResolveTodoParams(raw map[string]any, hasExistingPhases bool) (TodoParams, string) {
	if raw == nil {
		return TodoParams{}, "Invalid todo arguments: missing op"
	}
	opVal, hasOp := raw["op"]
	var opStr string
	if !hasOp || opVal == nil {
		inferred, ok := inferTodoOp(raw, hasExistingPhases)
		if !ok {
			return TodoParams{}, "Invalid todo arguments: op is required"
		}
		opStr = string(inferred)
	} else {
		s, ok := opVal.(string)
		if !ok || !IsValidOp(s) {
			return TodoParams{}, fmt.Sprintf("Invalid todo arguments: op must be one of init, start, done, rm, drop, block, unblock, append, view (got %v)", opVal)
		}
		opStr = s
	}

	params := TodoParams{Op: TodoOperation(opStr)}

	if v, ok := raw["task"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return TodoParams{}, "Invalid todo arguments: task must be a string"
		}
		params.Task = s
	}
	if v, ok := raw["phase"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return TodoParams{}, "Invalid todo arguments: phase must be a string"
		}
		params.Phase = s
	}
	if v, ok := raw["reason"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return TodoParams{}, "Invalid todo arguments: reason must be a string"
		}
		params.Reason = s
	}
	if v, ok := raw["items"]; ok && v != nil {
		arr, ok := v.([]any)
		if !ok {
			return TodoParams{}, "Invalid todo arguments: items must be an array of strings"
		}
		items := make([]string, len(arr))
		for i, elem := range arr {
			s, ok := elem.(string)
			if !ok {
				return TodoParams{}, "Invalid todo arguments: items must be an array of strings"
			}
			items[i] = s
		}
		params.Items = items
	}
	if v, ok := raw["list"]; ok && v != nil {
		arr, ok := v.([]any)
		if !ok {
			return TodoParams{}, "Invalid todo arguments: list must be an array"
		}
		list := make([]InitListEntry, len(arr))
		for i, elem := range arr {
			m, ok := elem.(map[string]any)
			if !ok {
				return TodoParams{}, "Invalid todo arguments: list entries must be objects"
			}
			phase, ok := m["phase"].(string)
			if !ok {
				return TodoParams{}, "Invalid todo arguments: list entry phase must be a string"
			}
			rawItems, ok := m["items"].([]any)
			if !ok || len(rawItems) == 0 {
				return TodoParams{}, "Invalid todo arguments: list entry items must be a non-empty array of strings"
			}
			items := make([]string, len(rawItems))
			for j, it := range rawItems {
				s, ok := it.(string)
				if !ok {
					return TodoParams{}, "Invalid todo arguments: list entry items must be strings"
				}
				items[j] = s
			}
			list[i] = InitListEntry{Phase: phase, Items: items}
		}
		params.List = list
	}

	return params, ""
}

// FormatSummary renders the model-facing result text for a todo call.
// upstream: packages/coding-agent/src/tools/todo.ts:634-706
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
			if task.Status == StatusPending || task.Status == StatusInProgress {
				if currentIdx == -1 {
					currentIdx = i
				}
				remaining = append(remaining, remainingItem{TodoItem: task, phase: phase.Name})
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
	blockedAll := 0
	for _, task := range allTasks {
		if IsClosedTodo(task) {
			closedAll++
		}
		if task.Status == StatusBlocked {
			blockedAll++
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

	blockedSuffix := ""
	if blockedAll > 0 {
		blockedSuffix = fmt.Sprintf(", %d blocked", blockedAll)
	}
	lines = append(lines, fmt.Sprintf("Overall: %d/%d done, %d open%s.", closedAll, len(allTasks), len(remaining), blockedSuffix))

	activeSuffix := "."
	if workedAhead {
		activeSuffix = " — earliest phase with open tasks; the in-progress pointer auto-advances to the earliest open task on each completion, so it can sit behind out-of-order work (nothing was un-completed)."
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
			case StatusBlocked:
				if task.Blocker != "" {
					tag = fmt.Sprintf(" (blocked: %s)", task.Blocker)
				} else {
					tag = " (blocked)"
				}
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
	case StatusBlocked:
		return "!"
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
	case "!":
		return StatusBlocked, true
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
			blockerNote := ""
			if task.Status == StatusBlocked && task.Blocker != "" {
				blockerNote = fmt.Sprintf(" <!-- blocker: %s -->", task.Blocker)
			}
			out = append(out, fmt.Sprintf("- [%s] %s%s", statusToMarker(task.Status), task.Content, blockerNote))
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// MarkdownToPhases parses a Markdown checklist back into todo phases.
// upstream: packages/coding-agent/src/tools/todo.ts:581-632
func MarkdownToPhases(md string) ([]TodoPhase, []string) {
	var errs []string
	var phases []TodoPhase
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
				errs = append(errs, fmt.Sprintf(`Line %d: unknown status marker "[%s]" (use [ ], [x], [/], [-], [!])`, lineNum+1, marker))
				continue
			}
			rawContent := strings.TrimSpace(m[2])
			if status == StatusBlocked {
				if bm := mdBlockerCommentRe.FindStringSubmatch(rawContent); bm != nil {
					phases[currentIdx].Tasks = append(phases[currentIdx].Tasks, TodoItem{
						Content: strings.TrimSpace(bm[1]),
						Status:  status,
						Blocker: strings.TrimSpace(bm[2]),
					})
					continue
				}
			}
			phases[currentIdx].Tasks = append(phases[currentIdx].Tasks, TodoItem{
				Content: rawContent,
				Status:  status,
			})
			continue
		}
		errs = append(errs, fmt.Sprintf(`Line %d: unrecognized syntax %q`, lineNum+1, trimmed))
	}

	normalizeInProgressTask(phases)
	return phases, errs
}
