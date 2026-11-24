package todo

import (
	_ "embed"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

//go:embed todo.md
var todoPrompt string

// toolSchema matches omp's todoSchema in packages/coding-agent/src/tools/todo.ts:53-70.
func toolSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type": "string",
				"enum": []any{"init", "start", "done", "rm", "drop", "block", "unblock", "append", "view"},
			},
			"list": map[string]any{
				"type":        "array",
				"description": "phases for init",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"phase": map[string]any{"type": "string"},
						"items": map[string]any{
							"type":     "array",
							"minItems": 1,
							"items":    map[string]any{"type": "string"},
						},
					},
					"required": []any{"phase", "items"},
				},
			},
			"task": map[string]any{
				"type":        "string",
				"description": "verbatim task content",
			},
			"phase": map[string]any{
				"type": "string",
			},
			"items": map[string]any{
				"type":        "array",
				"description": "tasks for flat init or append",
				"items":       map[string]any{"type": "string"},
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "blocker note for block",
			},
		},
		"required": []any{"op"},
	}
}

type sessionState struct {
	phases        []TodoPhase
	reminderCount int
}

type app struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	expanded bool
}

func newApp() *app {
	return &app{
		sessions: make(map[string]*sessionState),
	}
}

func sessionKey(ctx sdk.Context) string {
	if id, err := ctx.SessionManager().GetSessionID(); err == nil && id != "" {
		return id
	}
	return "_default"
}

func (a *app) stateLocked(key string) *sessionState {
	st, ok := a.sessions[key]
	if !ok {
		st = &sessionState{}
		a.sessions[key] = st
	}
	return st
}

func (a *app) getPhases(ctx sdk.Context) []TodoPhase {
	key := sessionKey(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	return ClonePhases(a.stateLocked(key).phases)
}

func (a *app) setPhases(ctx sdk.Context, phases []TodoPhase) {
	key := sessionKey(ctx)
	a.mu.Lock()
	a.stateLocked(key).phases = ClonePhases(phases)
	a.mu.Unlock()
}

func (a *app) setExpanded(expanded bool) {
	a.mu.Lock()
	a.expanded = expanded
	a.mu.Unlock()
}

func (a *app) isExpanded() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.expanded
}

func (a *app) refreshWidget(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	phases := a.getPhases(ctx)
	lines := RenderWidgetLines(phases, a.isExpanded())
	if len(lines) == 0 {
		_ = ctx.SetWidget(WidgetKey, nil)
		return
	}
	_ = ctx.SetWidget(WidgetKey, lines)
}

func (a *app) replayFromSession(ctx sdk.Context) {
	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return
	}
	phases := GetLatestTodoPhasesFromEntries(branch)
	a.setPhases(ctx, phases)
	a.refreshWidget(ctx)
}

// prepareTodoArguments repairs an omitted `op` for unambiguous payloads before
// schema validation, matching omp's `lenientArgValidation` + `inferTodoOp`.
// upstream: packages/coding-agent/src/tools/todo.ts:481-509
func (a *app) prepareTodoArguments(params map[string]any) (map[string]any, error) {
	if params == nil {
		return params, nil
	}
	if _, hasOp := params["op"]; hasOp {
		return params, nil
	}
	a.mu.Lock()
	hasExisting := false
	for _, st := range a.sessions {
		if len(st.phases) > 0 {
			hasExisting = true
			break
		}
	}
	a.mu.Unlock()
	inferred, ok := inferTodoOp(params, hasExisting)
	if !ok {
		return params, nil
	}
	cloned := make(map[string]any, len(params)+1)
	for k, v := range params {
		cloned[k] = v
	}
	cloned["op"] = string(inferred)
	return cloned, nil
}

func (a *app) handleTool(ctx sdk.Context, params map[string]any) (any, error) {
	previousPhases := a.getPhases(ctx)
	storage := "memory"
	if file, err := ctx.GetSessionFile(); err == nil && file != nil && *file != "" {
		storage = "session"
	}

	resolved, errText := ResolveTodoParams(params, len(previousPhases) > 0)
	if errText != "" {
		return sdk.ToolResult{
			Content: errText,
			Details: TodoToolDetails{Phases: previousPhases, Storage: storage},
			IsError: true,
		}, nil
	}

	op := resolved.Op
	readOnly := op == OpView
	var updated []TodoPhase
	var errs []string
	if readOnly {
		updated = previousPhases
	} else {
		updated, errs = ApplyParams(ClonePhases(previousPhases), resolved)
	}

	failed := len(errs) > 0
	effective := updated
	if failed {
		effective = previousPhases
	}

	var completed []TodoCompletionTransition
	if !readOnly && !failed {
		completed = getCompletionTransitions(previousPhases, updated)
		a.setPhases(ctx, updated)
		a.refreshWidget(ctx)
	}

	details := TodoToolDetails{
		Op:             op,
		Phases:         effective,
		Storage:        storage,
		CompletedTasks: completed,
	}

	return sdk.ToolResult{
		Content: FormatSummary(effective, errs, readOnly),
		Details: details,
		IsError: failed,
	}, nil
}

// onAgentBeforeSettle injects omp's completion reminder when the agent is about
// to settle with incomplete (pending or in_progress) todo items, up to defaultRemindersMax.
// Hook: sdk.EventAgentBeforeSettle ("agent_before_settle").
// upstream: packages/coding-agent/src/session/todo-tracker.ts:229-322
func (a *app) onAgentBeforeSettle(ctx sdk.Context, data map[string]any) (any, error) {
	key := sessionKey(ctx)
	a.mu.Lock()
	st := a.stateLocked(key)
	phases := ClonePhases(st.phases)
	if st.reminderCount >= defaultRemindersMax {
		a.mu.Unlock()
		return nil, nil
	}
	reminder, openCount := BuildStopReminder(phases, st.reminderCount+1, defaultRemindersMax)
	if openCount == 0 {
		st.reminderCount = 0
		a.mu.Unlock()
		return nil, nil
	}
	st.reminderCount++
	a.mu.Unlock()

	entry := map[string]any{
		"type":       "custom_message",
		"customType": "todo-reminder",
		"content":    reminder,
		"display":    false,
	}
	existing, _ := data["entries"].([]any)
	nextEntries := make([]any, 0, len(existing)+1)
	nextEntries = append(nextEntries, existing...)
	nextEntries = append(nextEntries, entry)
	return map[string]any{
		"entries":  nextEntries,
		"continue": true,
	}, nil
}

func (a *app) onAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	key := sessionKey(ctx)
	a.mu.Lock()
	a.stateLocked(key).reminderCount = 0
	a.mu.Unlock()
	return nil, nil
}

func (a *app) onSessionReplay(ctx sdk.Context, _ map[string]any) (any, error) {
	a.replayFromSession(ctx)
	return nil, nil
}

func (a *app) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	key := sessionKey(ctx)
	a.mu.Lock()
	delete(a.sessions, key)
	a.mu.Unlock()
	if ctx.HasUI() {
		_ = ctx.SetWidget(WidgetKey, nil)
	}
	return nil, nil
}

// Register wires omp's todo tool, /todo slash command, session replay hooks,
// HUD widget, and stop-reminder hook onto e.
func Register(e *sdk.Extension) {
	a := newApp()
	e.ToolWithPrepareArguments("todo", todoPrompt, toolSchema(), a.prepareTodoArguments, a.handleTool)
	e.Command("todo", "View or edit the session todo list", a.handleCommand)

	e.OnSessionStart(a.onSessionReplay)
	e.OnEvent(sdk.EventSessionCompact, a.onSessionReplay)
	e.OnEvent(sdk.EventSessionTree, a.onSessionReplay)
	e.OnSessionShutdown(a.onSessionShutdown)
	e.OnEvent(sdk.EventAgentStart, a.onAgentStart)
	e.OnEvent(sdk.EventAgentBeforeSettle, a.onAgentBeforeSettle)
}
