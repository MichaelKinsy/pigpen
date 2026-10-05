// Package rpiv_todo is a Go port of @juicesharp/rpiv-todo (rpiv-mono packages/rpiv-todo 2.11.0): a todo
// tool for the model, the /todos command and a live overlay that survives reload and compaction.
package rpiv_todo

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// defaultPromptSnippet and defaultPromptGuidelines are the tool's built-in prompt copy. The strings are
// the model-facing text of the original and are compared by the guidance-in-request scenario.
// upstream: todo.ts:55-64.
var (
	defaultPromptSnippet    = "Manage a task list to track multi-step progress"
	defaultPromptGuidelines = []string{
		"Use `todo` for complex work with 3+ steps, when the user gives you a list of tasks, or immediately after receiving new instructions to capture requirements. Skip it for single trivial tasks and purely conversational requests.",
		"When starting a task from the todo list, mark it in_progress BEFORE beginning work. Mark it completed IMMEDIATELY when done — never batch completions. Exactly one task in_progress at a time.",
		"Never mark a task completed if tests are failing, the implementation is partial, or you hit unresolved errors — keep it in_progress and create a new task for the blocker instead.",
		"Task status is a 4-state machine: pending → in_progress → completed, plus deleted as a tombstone. Pass activeForm (present-continuous label, e.g. 'researching existing tool') when marking in_progress.",
		`To change a task's status, call update with the task id and the target status, e.g. {"action":"update","id":3,"status":"completed"} or {"action":"update","id":3,"status":"in_progress","activeForm":"writing tests"}. status is the field that changes the task; an update without a mutable field (status or another) is rejected.`,
		"Use blockedBy to express dependencies (A is blocked by B). On create, pass blockedBy as the initial set. On update, use addBlockedBy / removeBlockedBy (additive merge — do not resend the full array). Cycles are rejected.",
		"list hides tombstoned (deleted) tasks by default; pass includeDeleted:true to see them. Pass status to filter by a single status.",
		"Subject must be short and imperative (e.g. 'Research existing tool'); description is for long-form detail. activeForm is a present-continuous label shown while in_progress.",
	}
)

const toolDescription = "Manage a task list for tracking multi-step progress. Actions: create (new task), update (change status/fields/dependencies), list (all tasks, optionally filtered by status), get (single task details), delete (tombstone), clear (reset all). Status: pending → in_progress → completed, plus deleted tombstone. Use this to plan and track multi-step work like research, design, and implementation."

// str, num, strEnum build the property schemas of the TypeBox parameters.
func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func numList(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "description": description}
}

func enumOf(values ...string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// toolParameters is the tool's JSON Schema, the original's TypeBox schema: every description doubles as
// model-facing prompt copy, so field names and wording are pinned. upstream: tool/types.ts:90-141.
func toolParameters() sdk.Schema {
	status := str("Set this task's status (update): one of pending, in_progress, completed, deleted. When action is list, filters returned tasks by this status.")
	status["enum"] = enumOf(statusPending, statusInProgress, statusCompleted, statusDeleted)
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"action":          map[string]any{"type": "string", "enum": enumOf("create", "update", "list", "get", "delete", "clear")},
			"subject":         str("Task subject line (required for create)"),
			"description":     str("Long-form task description"),
			"activeForm":      str("Present-continuous spinner label shown while status is in_progress (e.g. 'writing tests')"),
			"status":          status,
			"blockedBy":       numList("Initial blockedBy ids (create only)"),
			"addBlockedBy":    numList("Task ids to add to blockedBy (update only, additive merge)"),
			"removeBlockedBy": numList("Task ids to remove from blockedBy (update only, additive merge)"),
			"owner":           str("Agent/owner assigned to this task"),
			"metadata": map[string]any{
				"type":              "object",
				"patternProperties": map[string]any{"^.*$": map[string]any{}},
				"description":       "Arbitrary metadata; pass null value for a key to delete that key on update",
			},
			"id":             map[string]any{"type": "number", "description": "Task id (required for update, get, delete)"},
			"includeDeleted": map[string]any{"type": "boolean", "description": "If true, list action returns deleted (tombstoned) tasks as well. Default: false."},
		},
		"required": []any{"action"},
	}
}

// app is the extension's state: the per-session store and the overlay.
type app struct {
	st *store

	mu     sync.Mutex        // guards the fields below
	ov     *overlay          // nil until a foreground session has visible tasks (the original loads it lazily)
	bound  bool              // a foreground session with a UI is attached (the original's uiCtx)
	rebind bool              // the overlay must attach to the UI again before its next update
	order  sequencer         // commits tool calls in the order they started
	held   map[string]func() // tool call id -> release of its place, until the host reports the call ended
}

// sequencer runs steps in the order their tickets were taken. Pi starts the calls of a parallel batch in
// source order and each reaches the synchronous reducer at once, so the commits happen in source order; here
// the handler first asks the host for the session id, and without a ticket the commits could be reordered
// (an update before the create it follows). The SDK starts the handlers in source order, so the ticket is
// taken as the handler's first statement. That order has the residual window the SDK documents
// (tool_start_order.go): a handler thread preempted between its start and its first statement can still be
// overtaken, so the order is not guaranteed under preemption (PORT.md G4). A place is also held until the host reports the call's
// tool_execution_end, so the ends reach the client in start order as they do for Pi's synchronous reducer
// (a call that returned early would otherwise overtake the response of the one before it).
type sequencer struct {
	mu   sync.Mutex
	tail chan struct{}
}

// turn takes the next place. wait returns when every earlier place was released; release hands the place on.
func (q *sequencer) turn() (wait func(), release func()) {
	done := make(chan struct{})
	q.mu.Lock()
	prev := q.tail
	q.tail = done
	q.mu.Unlock()
	var once sync.Once
	wait = func() {
		if prev != nil {
			<-prev
		}
	}
	release = func() { wait(); once.Do(func() { close(done) }) }
	return wait, release
}

func newApp() *app { return &app{st: newStore(), held: map[string]func(){}} }

// hold keeps a call's place until its end event (or the end of the agent run) is seen.
func (a *app) hold(callID string, release func()) {
	a.mu.Lock()
	a.held[callID] = release
	a.mu.Unlock()
}

// releaseCall hands on the place of one call ("" releases every held place).
func (a *app) releaseCall(callID string) {
	a.mu.Lock()
	var fns []func()
	for id, fn := range a.held {
		if callID == "" || id == callID {
			fns = append(fns, fn)
			delete(a.held, id)
		}
	}
	a.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// ctxSink pushes overlay rows to the host's widget above the editor.
type ctxSink struct{ ctx sdk.Context }

func (s ctxSink) SetWidgetLines(key string, lines []string) error {
	if lines == nil {
		return s.ctx.SetWidget(key, nil)
	}
	return s.ctx.SetWidget(key, lines, sdk.WidgetOptions{"placement": "aboveEditor"})
}

// rpcSink models Pi's RPC mode, where a component widget is not rendered: its registration is dropped, and
// only a clear reaches the client. upstream: Pi rpc-mode.js (setWidget accepts string[] content only).
type rpcSink struct{ ctxSink }

func (s rpcSink) SetWidgetLines(key string, lines []string) error {
	if lines != nil {
		return nil
	}
	return s.ctxSink.SetWidgetLines(key, nil)
}

// sinkFor is where this host mode shows widgets.
func sinkFor(ctx sdk.Context) widgetSink {
	if ctx.Mode() == "rpc" {
		return rpcSink{ctxSink{ctx}}
	}
	return ctxSink{ctx}
}

// envFor is the host state one render needs: the live theme, the width and the tool-output expansion mode
// (a host that does not report it reads as collapsed, like a host predating getToolsExpanded()).
func envFor(ctx sdk.Context) viewEnv {
	expanded, err := ctx.GetToolsExpanded()
	width := ctx.Width()
	if width <= 0 {
		width = 80
	}
	return viewEnv{sink: sinkFor(ctx), theme: ctx.UITheme(), width: width, toolsExpanded: err == nil && expanded}
}

// isStaleCtxError matches the phrase the host raises from an invalidated context after a session
// replacement or reload; genuine replay bugs still propagate. upstream: index.ts:131-133.
func isStaleCtxError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "stale after session replacement")
}

// guidanceFromConfig reads the user's prompt overrides. upstream: todo.ts:67.
func guidanceFromConfig() guidanceFields { return validateGuidanceFields(loadConfig().Guidance) }

// toolDefinition is the registration of the `todo` tool. upstream: todo.ts:66-110.
func (a *app) toolDefinition(g guidanceFields) sdk.ToolDefinition {
	snippet, guidelines := defaultPromptSnippet, defaultPromptGuidelines
	if g.PromptSnippet != "" {
		snippet = g.PromptSnippet
	}
	if g.PromptGuidelines != nil {
		guidelines = g.PromptGuidelines
	}
	return sdk.ToolDefinition{
		Name:             toolName,
		Label:            toolLabel,
		Description:      toolDescription,
		PromptSnippet:    snippet,
		PromptGuidelines: guidelines,
		Parameters:       toolParameters(),
		Execute:          a.execute,
		// renderCall reflects the FOREGROUND slot, not the calling session's: the render context carries no
		// session identity. A child call whose task lives only in the child's slot falls back to `#<id>`,
		// which is intentional (ids restart at 1 per session, so searching siblings could show the wrong
		// subject). upstream: todo.ts:80-91.
		RenderCall: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			return wrapText(renderTodoCall(args, ctx.UITheme(), a.st.getRenderState()), width), nil
		},
		RenderResult: func(ctx sdk.Context, result sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
			return wrapText(renderTodoResult(detailsFromJSON(result.Details), ctx.UITheme()), width), nil
		},
	}
}

// detailsFromJSON reads the details a result carries back into the snapshot shape; nil when absent.
func detailsFromJSON(v any) *taskDetails {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	d := &taskDetails{}
	d.Action, _ = m["action"].(string)
	d.Params, _ = m["params"].(map[string]any)
	if raw, ok := m["tasks"].([]any); ok {
		for _, r := range raw {
			if tm, ok := r.(map[string]any); ok {
				d.Tasks = append(d.Tasks, taskFromJSON(tm))
			}
		}
	}
	return d
}

// execute applies one tool call to the calling session's slot. upstream: todo.ts:73-79.
func (a *app) execute(ctx sdk.Context, p map[string]any) (any, error) {
	wait, release := a.order.turn() // the first statement: the SDK starts handlers in source order
	handed := false
	defer func() {
		if !handed {
			release()
		}
	}()
	id, err := sid(ctx.SessionManager())
	if err != nil {
		return nil, err
	}
	action, _ := p["action"].(string)
	wait()
	r := applyTaskMutation(a.st.getState(id), action, p)
	a.st.commitState(id, r.State)
	env := buildToolResult(action, p, r.State, r.Op)
	details, err := marshalDetails(env.Details)
	if err != nil {
		return nil, err
	}
	if callID := ctx.ToolCallID(); callID != "" {
		a.hold(callID, release)
		handed = true
	}
	return sdk.ToolResult{Content: env.Text, Details: json.RawMessage(details)}, nil
}

// commandHandler is the /todos command: the list grouped by status. upstream: todo.ts:119-158.
func (a *app) commandHandler(ctx sdk.Context, _ string) error {
	if !ctx.HasUI() {
		ctx.Notify(tr("command.requires_interactive", errRequiresInteractive), "error")
		return nil
	}
	id, err := sid(ctx.SessionManager())
	if err != nil {
		return err
	}
	state := a.st.getState(id)
	if len(selectVisibleTasks(state)) == 0 {
		ctx.Notify(tr("command.no_todos", msgNoTodos), "info")
		return nil
	}
	groups, counts := selectTasksByStatus(state), selectTodoCounts(state)
	var header []string
	if counts.Completed > 0 {
		header = append(header, fmt.Sprintf("%d/%d %s", counts.Completed, counts.Total, formatStatusLabel(statusCompleted)))
	}
	if counts.InProgress > 0 {
		header = append(header, fmt.Sprintf("%d %s", counts.InProgress, formatStatusLabel(statusInProgress)))
	}
	if counts.Pending > 0 {
		header = append(header, fmt.Sprintf("%d %s", counts.Pending, formatStatusLabel(statusPending)))
	}
	lines := []string{strings.Join(header, " · ")}
	section := func(tasks []task, titleKey, title, glyph string) {
		if len(tasks) == 0 {
			return
		}
		lines = append(lines, tr(titleKey, title))
		for _, t := range tasks {
			lines = append(lines, formatCommandTaskLine(t, glyph))
		}
	}
	section(groups.Pending, "command.section.pending", "── Pending ──", "○")
	section(groups.InProgress, "command.section.in_progress", "── In Progress ──", "◐")
	section(groups.Completed, "command.section.completed", "── Completed ──", "✓")
	ctx.Notify(strings.Join(lines, "\n"), "info")
	return nil
}

// updateOverlay refreshes the overlay, constructing it only when a foreground session has visible tasks.
// upstream: index.ts:104-118 (updateTodoOverlay).
func (a *app) updateOverlay(ctx sdk.Context, resetCompletedDisplayState bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hasVisible := len(selectVisibleTasks(a.st.getRenderState())) > 0
	if !a.bound || (a.ov == nil && !hasVisible) {
		return
	}
	if a.ov == nil {
		a.ov = newOverlay(a.st, hostWidgetRows-1)
		a.rebind = true
	}
	if a.rebind {
		a.ov.bind()
		a.rebind = false
	}
	if resetCompletedDisplayState {
		a.ov.resetCompletedDisplayState()
	}
	a.ov.update(envFor(ctx))
}

// replay re-keys a session's slot from its branch. A stale ctx keeps the current state (the replacement
// session's session_start replays it); other errors are real replay bugs and propagate.
func (a *app) replay(ctx sdk.Context) (id string, err error) {
	id, err = sid(ctx.SessionManager())
	if err == nil {
		var branch []map[string]any
		if branch, err = ctx.SessionManager().GetBranch(nil); err == nil {
			a.st.replaceState(id, replayFromBranch(branch))
		}
	}
	return id, err
}

func (a *app) replayAndRefresh(ctx sdk.Context, _ map[string]any) (any, error) {
	id, err := a.replay(ctx)
	foreground := false
	if err != nil {
		if !isStaleCtxError(err) {
			return nil, err
		}
	} else {
		foreground = id == a.st.getActiveRenderSession()
	}
	if foreground {
		a.updateOverlay(ctx, true)
	}
	return nil, nil
}

func (a *app) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	id, err := a.replay(ctx)
	if err != nil {
		if isStaleCtxError(err) {
			return nil, nil
		}
		return nil, err
	}
	if !ctx.HasUI() {
		return nil, nil
	}
	// The first UI-bearing session_start claims the foreground, without loading the overlay.
	if a.st.getActiveRenderSession() == "" {
		a.st.setActiveRenderSession(id)
	}
	// Only the foreground re-binds and refreshes the shared overlay; a child (distinct sid) is skipped.
	if id != a.st.getActiveRenderSession() {
		return nil, nil
	}
	a.mu.Lock()
	a.bound, a.rebind = true, true
	a.mu.Unlock()
	a.updateOverlay(ctx, true)
	return nil, nil
}

func (a *app) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	id, err := sid(ctx.SessionManager())
	if err != nil {
		if !isStaleCtxError(err) {
			return nil, err
		}
		id = ""
	}
	// The shutting-down session's own slot is always evicted.
	a.st.evictSession(id)
	// Teardown is sid-gated: a child shutdown must not dispose the foreground's overlay. An unknown or stale
	// sid is treated as foreground, the safe pre-isolation default.
	if id != "" && id != a.st.getActiveRenderSession() {
		return nil, nil
	}
	a.mu.Lock()
	ov := a.ov
	a.ov, a.bound, a.rebind = nil, false, false
	a.mu.Unlock()
	// The pointer clear and the overlay drop run whether or not the widget clear fails, so a dead UI does
	// not leave the foreground pointing at an evicted slot.
	var derr error
	if ov != nil {
		derr = ov.dispose(sinkFor(ctx))
	}
	a.st.clearActiveRenderSession()
	return nil, derr
}

// onToolExecutionEnd refreshes the overlay after a successful todo call. It reads the store at render
// time and never replays the branch (the branch is stale: message_end runs after tool_execution_end). The
// tool succeeded, so a refused widget push only costs this refresh. upstream: index.ts:227-243.
func (a *app) onToolExecutionEnd(ctx sdk.Context, data map[string]any) (any, error) {
	if callID, _ := data["toolCallId"].(string); callID != "" {
		a.releaseCall(callID)
	}
	if name, _ := data["toolName"].(string); name != toolName {
		return nil, nil
	}
	if isErr, _ := data["isError"].(bool); isErr {
		return nil, nil
	}
	a.updateOverlay(ctx, false)
	return nil, nil
}

func (a *app) onAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	ov := a.ov
	a.mu.Unlock()
	if ov != nil {
		ov.hideCompletedTasksFromPreviousTurn(envFor(ctx))
	}
	return nil, nil
}

// collapseHandler toggles the overlay: a no-op headless, before the overlay exists, or while it is not
// registered (auto-hidden on an empty list). upstream: index.ts:179-184.
func (a *app) collapseHandler(ctx sdk.Context) error {
	a.mu.Lock()
	ov := a.ov
	a.mu.Unlock()
	if !ctx.HasUI() || ov == nil || !ov.isRegistered() {
		return nil
	}
	ov.toggleCollapse(envFor(ctx))
	return nil
}

// Extension returns the todo extension.
func Extension() *sdk.Extension {
	e := sdk.New("rpiv-todo")
	a := newApp()
	e.RegisterTool(a.toolDefinition(guidanceFromConfig()))
	e.Command(commandName, "Show all todos on the current branch, grouped by status", a.commandHandler)

	// The collapse key is resolved once at factory scope (a config change needs a reload to re-bind) and
	// the binding is skipped entirely when collapseKey is "off".
	if key := resolveCollapseKey(); key != collapseKeyOff {
		e.Shortcut(key, "Collapse or expand the todo overlay", a.collapseHandler)
	}

	e.OnSessionStart(a.onSessionStart)
	e.OnEvent(sdk.EventSessionCompact, a.replayAndRefresh)
	e.OnEvent(sdk.EventSessionTree, a.replayAndRefresh)
	e.OnSessionShutdown(a.onSessionShutdown)
	e.OnEvent(sdk.EventToolExecutionEnd, a.onToolExecutionEnd)
	e.OnEvent(sdk.EventAgentStart, a.onAgentStart)
	// Every call has ended when the run does: hand on any place whose end event was not seen.
	e.OnEvent(sdk.EventAgentEnd, func(sdk.Context, map[string]any) (any, error) {
		a.releaseCall("")
		return nil, nil
	})
	return e
}
