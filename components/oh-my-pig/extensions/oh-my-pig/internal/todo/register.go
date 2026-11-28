package todo

import (
	_ "embed"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

//go:embed todo.md
var todoPrompt string

//go:embed eager-todo.md
var eagerTodoTemplate string

// Setting reads an omp setting by key (e.g. "todo.enabled", "todo.reminders",
// "todo.remindersMax", "todo.midRunNudges", "tasks.todoClearDelay").
// Nil falls back to omp's defaults. Assigned by the integrator.
var Setting func(ctx sdk.Context, key string) any

// SetForcedToolChoice arms a turn-scoped wire-level `tool_choice` override for
// sessionID. Wired by the integrator to `providerwire.SetForcedToolChoice`.
var SetForcedToolChoice func(sessionID, toolName string)

// ForcedToolChoice returns the currently armed wire-level forced tool name for
// sessionID, or "". Wired by the integrator to `providerwire.ForcedToolChoice`
// so `todo.eager` never clobbers a user's `/force:<tool>`.
var ForcedToolChoice func(sessionID string) string

// hudTimer is the minimal timer handle used by the auto-clear scheduler so
// tests can inject a deterministic clock without real wall-clock sleeps.
type hudTimer interface {
	Stop() bool
}

var (
	hudAfterFuncMu sync.RWMutex
	hudAfterFunc   func(d time.Duration, f func()) hudTimer = func(d time.Duration, f func()) hudTimer {
		return time.AfterFunc(d, f)
	}
)

func scheduleHudTimer(d time.Duration, f func()) hudTimer {
	hudAfterFuncMu.RLock()
	fn := hudAfterFunc
	hudAfterFuncMu.RUnlock()
	return fn(d, f)
}

const defaultTodoClearDelaySec = 60.0

func settingBool(ctx sdk.Context, key string, def bool) bool {
	if Setting == nil {
		return def
	}
	if v, ok := Setting(ctx, key).(bool); ok {
		return v
	}
	return def
}

func settingInt(ctx sdk.Context, key string, def int) int {
	if Setting == nil {
		return def
	}
	switch v := Setting(ctx, key).(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}
func settingFloat(ctx sdk.Context, key string, def float64) float64 {
	if Setting == nil {
		return def
	}
	switch v := Setting(ctx, key).(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return def
}

func settingString(ctx sdk.Context, key string, def string) string {
	if Setting == nil {
		return def
	}
	if v, ok := Setting(ctx, key).(string); ok && v != "" {
		return v
	}
	return def
}

// toolSchema matches omp's todoSchema in packages/coding-agent/src/tools/todo.ts:53-70.
func toolSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type": "string",
				"enum": []any{"init", "start", "done", "rm", "drop", "append", "view"},
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
					"required":             []any{"phase", "items"},
					"additionalProperties": false,
				},
			},
			"task": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"description": "verbatim task content; array targets several (not start)",
			},
			"phase": map[string]any{
				"type": "string",
			},
			"items": map[string]any{
				"type":        "array",
				"description": "tasks for flat init or append",
				"items":       map[string]any{"type": "string"},
			},
		},
		"required":             []any{"op"},
		"additionalProperties": false,
	}
}

const (
	midRunNudgeMutationThreshold = 12
	midRunNudgeMaxPerCycle       = 2
	midRunNudgeBackoffDoublings  = 4
)

var mutatingTools = map[string]bool{
	"bash":     true,
	"eval":     true,
	"edit":     true,
	"write":    true,
	"ast_edit": true,
}

type sessionState struct {
	phases                   []TodoPhase
	expanded                 bool
	hudHidden                bool
	pendingPersistDismissal  bool
	pendingPersistReveal     bool
	autoClearGen             uint64
	autoClearTimer           hudTimer
	reminderCount            int
	reminderAwaitingProgress bool
	mutationsSinceLastTouch  int
	midRunNudgeCount         int
}

// Package-level registry keyed strictly by session ID so every extension
// instance (created per session/reload/fork) and lifecycle.End(sessionID)
// (including child subagents with no session_shutdown event) reach the same
// state and only ever release the ending session.
var (
	storeMu  sync.Mutex
	sessions = map[string]*sessionState{}
	liveApps = map[string]*app{}
)

var (
	surfaceListenerMu sync.Mutex
	surfaceUnsubFn    func()
)

func ensureSurfaceListener() {
	surfaceListenerMu.Lock()
	if surfaceUnsubFn != nil {
		surfaceListenerMu.Unlock()
		return
	}
	surfaceUnsubFn = surface.OnChange(handleSurfaceChange)
	surfaceListenerMu.Unlock()
}

// resetSurfaceListener re-subscribes after tests call surface.ResetForTests().
func resetSurfaceListener() {
	surfaceListenerMu.Lock()
	if surfaceUnsubFn != nil {
		surfaceUnsubFn()
		surfaceUnsubFn = nil
	}
	surfaceUnsubFn = surface.OnChange(handleSurfaceChange)
	surfaceListenerMu.Unlock()
}

func handleSurfaceChange(_ bool) {
	storeMu.Lock()
	apps := make([]*app, 0, len(liveApps))
	for _, a := range liveApps {
		apps = append(apps, a)
	}
	storeMu.Unlock()
	for _, a := range apps {
		a.refreshForSurfaceChange()
	}
}

func cancelAutoClearLocked(st *sessionState) {
	if st == nil {
		return
	}
	st.autoClearGen++
	if st.autoClearTimer != nil {
		st.autoClearTimer.Stop()
		st.autoClearTimer = nil
	}
}

func releaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	storeMu.Lock()
	if st := sessions[sessionID]; st != nil {
		cancelAutoClearLocked(st)
	}
	delete(sessions, sessionID)
	delete(liveApps, sessionID)
	storeMu.Unlock()
}

// CurrentPhases returns a deep copy of the active todo phases for ctx's
// session and whether todo is enabled for that session.
func CurrentPhases(ctx sdk.Context) (bool, []TodoPhase) {
	if !settingBool(ctx, "todo.enabled", true) {
		return false, nil
	}
	var activeTools []string
	func() {
		defer func() { _ = recover() }()
		if tools, err := ctx.GetActiveTools(); err == nil {
			activeTools = tools
		}
	}()
	if len(activeTools) > 0 {
		hasTodo := false
		for _, name := range activeTools {
			if name == "todo" {
				hasTodo = true
				break
			}
		}
		if !hasTodo {
			return false, nil
		}
	}
	var sid string
	func() {
		defer func() { _ = recover() }()
		if id, err := ctx.GetSessionID(); err == nil {
			sid = strings.TrimSpace(id)
		}
	}()
	if sid == "" {
		return true, nil
	}
	storeMu.Lock()
	st := sessions[sid]
	var phases []TodoPhase
	if st != nil && len(st.phases) > 0 {
		phases = ClonePhases(st.phases)
	}
	storeMu.Unlock()
	return true, phases
}
func stateLocked(key string) *sessionState {
	st, ok := sessions[key]
	if !ok {
		st = &sessionState{}
		sessions[key] = st
	}
	return st
}

type app struct {
	mu          sync.Mutex
	lastSessKey string
	lastCtx     sdk.Context
	hasCtx      bool
}

func newApp() *app {
	return &app{}
}
func sessionKey(ctx sdk.Context) string {
	if id, err := ctx.SessionManager().GetSessionID(); err == nil && id != "" {
		return id
	}
	if id, err := ctx.GetSessionID(); err == nil && id != "" {
		return id
	}
	return ""
}

func (a *app) rememberSession(key string) {
	if key == "" {
		return
	}
	a.mu.Lock()
	a.lastSessKey = key
	a.mu.Unlock()
}

func (a *app) effectiveSessionKey(ctx sdk.Context) string {
	if key := sessionKey(ctx); key != "" {
		a.rememberSession(key)
		return key
	}
	a.mu.Lock()
	key := a.lastSessKey
	a.mu.Unlock()
	return key
}

func (a *app) getPhases(ctx sdk.Context) []TodoPhase {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return nil
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	return ClonePhases(stateLocked(key).phases)
}

func (a *app) setPhases(ctx sdk.Context, phases []TodoPhase) {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return
	}
	storeMu.Lock()
	st := stateLocked(key)
	st.phases = ClonePhases(phases)
	// upstream: packages/coding-agent/src/session/todo-tracker.ts:127-134
	st.midRunNudgeCount = 0
	storeMu.Unlock()
}

func (a *app) setExpanded(ctx sdk.Context, expanded bool) {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return
	}
	var snapshotPhases []TodoPhase
	storeMu.Lock()
	st := stateLocked(key)
	st.expanded = expanded
	if expanded {
		// upstream: packages/coding-agent/src/modes/interactive-mode.ts:10183-10221
		cancelAutoClearLocked(st)
		st.hudHidden = false
		st.pendingPersistDismissal = false
		snapshotPhases = ClonePhases(st.phases)
	}
	storeMu.Unlock()
	if expanded && len(snapshotPhases) > 0 {
		persisted := false
		if branch, err := ctx.SessionManager().GetBranch(nil); err == nil {
			if data, ok := CreateTodoHudStateData(branch, snapshotPhases, HudVisibilityRevealed); ok {
				_ = ctx.AppendEntry(TodoHudStateCustomType, data)
				persisted = true
			}
		}
		if !persisted {
			storeMu.Lock()
			if st := sessions[key]; st != nil && TodoPhasesFingerprint(st.phases) == TodoPhasesFingerprint(snapshotPhases) {
				st.pendingPersistReveal = true
			}
			storeMu.Unlock()
		}
	}
}

func (a *app) isExpanded(ctx sdk.Context) bool {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return false
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	return stateLocked(key).expanded
}

func (a *app) isHudHidden(ctx sdk.Context) bool {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return false
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	return stateLocked(key).hudHidden
}

func allTasksClosed(phases []TodoPhase) bool {
	hasTask := false
	for _, p := range phases {
		for _, t := range p.Tasks {
			hasTask = true
			if !IsClosedTodo(t) {
				return false
			}
		}
	}
	return hasTask
}

// syncHudState mirrors InteractiveMode.#syncTodoHudState
// (packages/coding-agent/src/modes/interactive-mode.ts:5351-5390):
// restores persisted "dismissed"/"revealed" visibility for the current snapshot,
// cancels any prior timer, and schedules auto-clear when every task is closed
// and no visibility choice is persisted yet.
func (a *app) syncHudState(ctx sdk.Context, branch []map[string]any) {
	a.bindCtx(ctx)
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return
	}
	if branch == nil {
		if b, err := ctx.SessionManager().GetBranch(nil); err == nil {
			branch = b
		}
	}

	var sessFile string
	if sf, err := ctx.GetSessionFile(); err == nil && sf != nil {
		sessFile = *sf
	}
	delaySec := settingFloat(ctx, "tasks.todoClearDelay", defaultTodoClearDelaySec)

	storeMu.Lock()
	st := stateLocked(key)
	cancelAutoClearLocked(st)
	st.pendingPersistDismissal = false
	st.pendingPersistReveal = false
	phases := ClonePhases(st.phases)
	persisted, hasPersisted := GetTodoHudVisibility(branch, phases)
	st.hudHidden = hasPersisted && persisted == HudVisibilityDismissed
	if hasPersisted || !allTasksClosed(phases) {
		storeMu.Unlock()
		return
	}
	if math.IsNaN(delaySec) || math.IsInf(delaySec, 0) || delaySec < 0 {
		storeMu.Unlock()
		return
	}
	gen := st.autoClearGen
	snapshotKey := TodoPhasesFingerprint(phases)
	delay := time.Duration(delaySec * float64(time.Second))
	st.autoClearTimer = scheduleHudTimer(delay, func() {
		a.fireAutoClear(key, gen, snapshotKey, sessFile)
	})
	storeMu.Unlock()
}

func (a *app) fireAutoClear(sessionID string, gen uint64, snapshotKey string, expectedFile string) {
	a.mu.Lock()
	ctx := a.lastCtx
	hasCtx := a.hasCtx
	a.mu.Unlock()
	if !hasCtx {
		return
	}
	storeMu.Lock()
	st, ok := sessions[sessionID]
	if !ok || st.autoClearGen != gen {
		storeMu.Unlock()
		return
	}
	st.autoClearTimer = nil
	phases := ClonePhases(st.phases)
	if TodoPhasesFingerprint(phases) != snapshotKey || !allTasksClosed(phases) {
		storeMu.Unlock()
		return
	}
	storeMu.Unlock()

	// Verify session ID and session file still match before persisting dismissal
	// (upstream: interactive-mode.ts:5365-5370).
	if curID := sessionKey(ctx); curID != "" && curID != sessionID {
		return
	}
	if sf, err := ctx.GetSessionFile(); err == nil {
		curFile := ""
		if sf != nil {
			curFile = *sf
		}
		if curFile != expectedFile {
			return
		}
	}

	persisted := false
	if branch, err := ctx.SessionManager().GetBranch(nil); err == nil {
		if data, ok := CreateTodoHudStateData(branch, phases, HudVisibilityDismissed); ok {
			_ = ctx.AppendEntry(TodoHudStateCustomType, data)
			persisted = true
		}
	}

	// Re-check generation and fingerprint after host round-trips so a newer todo
	// update that landed concurrently is never hidden.
	storeMu.Lock()
	st, ok = sessions[sessionID]
	if !ok || st.autoClearGen != gen || TodoPhasesFingerprint(st.phases) != snapshotKey {
		storeMu.Unlock()
		return
	}
	st.hudHidden = true
	st.pendingPersistDismissal = !persisted
	storeMu.Unlock()

	a.refreshWidget(ctx)
}

// flushPendingHudState persists a deferred "dismissed" or "revealed" entry once
// the toolResult message that produced the snapshot has been appended to the
// session branch (e.g. on EventTurnEnd when delay == 0 fired inside Execute).
func (a *app) flushPendingHudState(ctx sdk.Context) {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return
	}
	storeMu.Lock()
	st := sessions[key]
	if st == nil || (!st.pendingPersistDismissal && !st.pendingPersistReveal) {
		storeMu.Unlock()
		return
	}
	vis := HudVisibilityDismissed
	if st.pendingPersistReveal {
		vis = HudVisibilityRevealed
	}
	phases := ClonePhases(st.phases)
	gen := st.autoClearGen
	storeMu.Unlock()

	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return
	}
	data, ok := CreateTodoHudStateData(branch, phases, vis)
	if !ok {
		return
	}
	_ = ctx.AppendEntry(TodoHudStateCustomType, data)
	storeMu.Lock()
	if cur := sessions[key]; cur != nil && cur.autoClearGen == gen {
		cur.pendingPersistDismissal = false
		cur.pendingPersistReveal = false
	}
	storeMu.Unlock()
}

func (a *app) bindCtx(ctx sdk.Context) {
	key := a.effectiveSessionKey(ctx)
	a.mu.Lock()
	a.lastCtx = ctx
	a.hasCtx = true
	a.mu.Unlock()
	if key != "" {
		storeMu.Lock()
		liveApps[key] = a
		storeMu.Unlock()
	}
}

func (a *app) refreshForSurfaceChange() {
	a.mu.Lock()
	ctx := a.lastCtx
	hasCtx := a.hasCtx
	a.mu.Unlock()
	if !hasCtx {
		return
	}
	a.refreshWidget(ctx)
}

func (a *app) refreshWidget(ctx sdk.Context) {
	a.bindCtx(ctx)
	if !ctx.HasUI() {
		return
	}
	if a.isHudHidden(ctx) {
		_ = ctx.SetWidget(WidgetKey, nil)
		return
	}
	phases := a.getPhases(ctx)
	expanded := a.isExpanded(ctx)
	if surface.Native() {
		view, ok := BuildNativeWidgetView(phases)
		if !ok {
			_ = ctx.SetWidget(WidgetKey, nil)
			return
		}
		_ = ctx.SetWidget(WidgetKey, view)
		return
	}
	if len(buildWidgetEntries(phases, expanded)) == 0 {
		_ = ctx.SetWidget(WidgetKey, nil)
		return
	}
	// Use SetWidgetRenderer so PiG does not cap expanded lists at 10 rows and
	// every row wraps/clamps to the terminal width passed by PiG. Read the live
	// host UITheme on every render so theme switches repaint immediately.
	snapshotPhases := ClonePhases(phases)
	_ = ctx.SetWidgetRenderer(WidgetKey, func(width int) []string {
		return RenderStyledWidgetLinesForWidth(ctx.UITheme(), snapshotPhases, expanded, width)
	})
}

func (a *app) replayFromSession(ctx sdk.Context) {
	a.effectiveSessionKey(ctx)
	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return
	}
	phases := GetLatestTodoPhasesFromEntries(branch)
	a.setPhases(ctx, phases)
	a.syncHudState(ctx, branch)
	a.refreshWidget(ctx)
}

// prepareTodoArguments mirrors omp's `lenientArgValidation` + `resolveTodoParams`
// before PiG's host schema validator runs: validates `op` and all fields with
// omp's exact `Invalid todo arguments: ...` text, repairing an omitted `op` for
// unambiguous payloads.
// upstream: packages/coding-agent/src/tools/todo.ts:481-509,696
func (a *app) prepareTodoArguments(params map[string]any) (map[string]any, error) {
	a.mu.Lock()
	key := a.lastSessKey
	a.mu.Unlock()
	hasExisting := false
	if key != "" {
		storeMu.Lock()
		if st, ok := sessions[key]; ok && len(st.phases) > 0 {
			hasExisting = true
		}
		storeMu.Unlock()
	}
	resolved, errText := ResolveTodoParams(params, hasExisting)
	if errText != "" {
		return nil, fmt.Errorf("%s", errText)
	}
	if v, hasOp := params["op"]; hasOp && v != nil {
		return params, nil
	}
	cloned := make(map[string]any, len(params)+1)
	for k, v := range params {
		cloned[k] = v
	}
	cloned["op"] = string(resolved.Op)
	return cloned, nil
}

func (a *app) handleTool(ctx sdk.Context, params map[string]any) (any, error) {
	storage := "memory"
	if file, err := ctx.GetSessionFile(); err == nil && file != nil && *file != "" {
		storage = "session"
	}

	key := a.effectiveSessionKey(ctx)
	storeMu.Lock()
	var st *sessionState
	var previousPhases []TodoPhase
	if key != "" {
		st = stateLocked(key)
		previousPhases = ClonePhases(st.phases)
	}

	resolved, errText := ResolveTodoParams(params, len(previousPhases) > 0)
	if errText != "" {
		storeMu.Unlock()
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
		if st != nil {
			st.phases = ClonePhases(updated)
			st.midRunNudgeCount = 0
			st.mutationsSinceLastTouch = 0
		}
	}
	storeMu.Unlock()

	if !readOnly && !failed {
		a.syncHudState(ctx, nil)
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

// lastAssistantFromBoundary extracts the latest assistant message's text,
// stopReason, and whether it contains toolCall blocks from agent_before_settle
// context preview (falling back to session branch).
func lastAssistantFromBoundary(ctx sdk.Context, data map[string]any) (text string, hasToolCalls bool, stopReason string) {
	inspectMsg := func(msg map[string]any) (string, bool, string, bool) {
		if msg == nil {
			return "", false, "", false
		}
		role, _ := msg["role"].(string)
		if role != "assistant" {
			return "", false, "", false
		}
		sr, _ := msg["stopReason"].(string)
		if sr == "" {
			sr, _ = msg["stop_reason"].(string)
		}
		var parts []string
		toolCalls := false
		switch content := msg["content"].(type) {
		case string:
			parts = append(parts, content)
		case []any:
			for _, raw := range content {
				blk, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				t, _ := blk["type"].(string)
				switch t {
				case "text":
					if s, ok := blk["text"].(string); ok {
						parts = append(parts, s)
					}
				case "toolCall", "tool_call", "tool_use":
					toolCalls = true
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n")), toolCalls, sr, true
	}

	if ctxMap, ok := data["context"].(map[string]any); ok {
		if msgs, ok := ctxMap["llmMessages"].([]any); ok {
			for i := len(msgs) - 1; i >= 0; i-- {
				if m, ok := msgs[i].(map[string]any); ok {
					if txt, tc, sr, found := inspectMsg(m); found {
						return txt, tc, sr
					}
				}
			}
		}
	}
	if branch, err := ctx.SessionManager().GetBranch(nil); err == nil {
		for i := len(branch) - 1; i >= 0; i-- {
			entry := branch[i]
			if t, _ := entry["type"].(string); t != "message" {
				continue
			}
			if m, ok := entry["message"].(map[string]any); ok {
				if txt, tc, sr, found := inspectMsg(m); found {
					return txt, tc, sr
				}
			}
		}
	}
	return "", false, ""
}

func hasWakeSources(ctx sdk.Context, key string) bool {
	if pending, err := ctx.HasPendingMessages(); err == nil && pending {
		return true
	}
	if key != "" {
		for _, j := range jobs.List(key) {
			select {
			case <-j.Done():
			default:
				return true
			}
		}
	}
	return false
}

// onAgentBeforeSettle injects omp's completion reminder when the agent is about
// to settle with incomplete (pending or in_progress) todo items, up to todo.remindersMax.
// Hook: sdk.EventAgentBeforeSettle ("agent_before_settle").
// upstream: packages/coding-agent/src/session/todo-tracker.ts:252-345,
// packages/coding-agent/src/session/agent-session.ts:5637-5652,
// packages/coding-agent/src/modes/print-mode.ts:195
func (a *app) onAgentBeforeSettle(ctx sdk.Context, data map[string]any) (any, error) {
	// Never remind in print or json mode (upstream: modes/print-mode.ts:195).
	if mode := ctx.Mode(); mode == "print" || mode == "json" {
		return nil, nil
	}
	// Never remind in plan mode (upstream: session/todo-tracker.ts:254).
	if gate.PlanActive(ctx) {
		return nil, nil
	}
	// Skip error/aborted outcomes (upstream: session/agent-session.ts:5637-5652).
	if outcome, _ := data["outcome"].(string); outcome != "" && outcome != "completed" {
		return nil, nil
	}
	lastText, hasToolCalls, stopReason := lastAssistantFromBoundary(ctx, data)
	if hasToolCalls || stopReason == "error" || stopReason == "aborted" {
		return nil, nil
	}

	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return nil, nil
	}
	remindersEnabled := settingBool(ctx, "todo.reminders", true)
	todoEnabled := settingBool(ctx, "todo.enabled", true)
	remindersMax := settingInt(ctx, "todo.remindersMax", defaultRemindersMax)

	activeTools, activeErr := ctx.GetActiveTools()
	todoActive := activeErr != nil || slices.Contains(activeTools, "todo")

	storeMu.Lock()
	st := stateLocked(key)
	// upstream: packages/coding-agent/src/session/todo-tracker.ts:255-260
	if st.reminderAwaitingProgress {
		storeMu.Unlock()
		return nil, nil
	}
	// upstream: packages/coding-agent/src/session/todo-tracker.ts:261-268
	if !remindersEnabled || !todoEnabled || !todoActive {
		st.reminderCount = 0
		st.reminderAwaitingProgress = false
		storeMu.Unlock()
		return nil, nil
	}
	if st.reminderCount >= remindersMax {
		storeMu.Unlock()
		return nil, nil
	}
	phases := ClonePhases(st.phases)
	if len(phases) == 0 {
		st.reminderCount = 0
		st.reminderAwaitingProgress = false
		storeMu.Unlock()
		return nil, nil
	}
	reminder, openCount := BuildStopReminder(phases, st.reminderCount+1, remindersMax)
	if openCount == 0 {
		st.reminderCount = 0
		st.reminderAwaitingProgress = false
		storeMu.Unlock()
		return nil, nil
	}
	if IsAwaitingUserAnswer(lastText) {
		storeMu.Unlock()
		return nil, nil
	}
	storeMu.Unlock()

	if hasWakeSources(ctx, key) {
		return nil, nil
	}

	storeMu.Lock()
	st = stateLocked(key)
	if st.reminderAwaitingProgress || st.reminderCount >= remindersMax {
		storeMu.Unlock()
		return nil, nil
	}
	st.reminderCount++
	reminder, openCount = BuildStopReminder(ClonePhases(st.phases), st.reminderCount, remindersMax)
	if openCount == 0 {
		st.reminderCount = 0
		st.reminderAwaitingProgress = false
		storeMu.Unlock()
		return nil, nil
	}
	st.mutationsSinceLastTouch = 0
	st.reminderAwaitingProgress = true
	storeMu.Unlock()

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

// renderEagerTodoPrelude renders omp's embedded `eager-todo.md` template for
// `forced` (`todo.eager == "always"`) vs non-forced (`todo.eager == "preferred"`).
// upstream: packages/coding-agent/src/prompts/system/eager-todo.md
// upstream: packages/coding-agent/src/session/todo-tracker.ts:200-207
func renderEagerTodoPrelude(forced bool) string {
	tpl := strings.ReplaceAll(eagerTodoTemplate, "\r\n", "\n")
	const (
		ifOpen  = "{{#if forced}}\n"
		ifElse  = "{{else}}\n"
		ifClose = "{{/if}}\n"
	)
	if start := strings.Index(tpl, ifOpen); start >= 0 {
		rest := tpl[start+len(ifOpen):]
		if elseIdx := strings.Index(rest, ifElse); elseIdx >= 0 {
			afterElse := rest[elseIdx+len(ifElse):]
			if closeIdx := strings.Index(afterElse, ifClose); closeIdx >= 0 {
				branch := afterElse[:closeIdx]
				if forced {
					branch = rest[:elseIdx]
				}
				tpl = tpl[:start] + branch + afterElse[closeIdx+len(ifClose):]
			}
		}
	}
	tpl = strings.ReplaceAll(tpl, "{{toolRefs.todo}}", "todo")
	return strings.TrimRight(tpl, "\n")
}

// hasPriorUserMessage reports whether the active session branch already contains
// a prior user message.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:191
func hasPriorUserMessage(ctx sdk.Context) bool {
	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return false
	}
	for _, entry := range branch {
		if t, _ := entry["type"].(string); t != "message" {
			continue
		}
		if msg, ok := entry["message"].(map[string]any); ok {
			if role, _ := msg["role"].(string); role == "user" {
				return true
			}
		}
	}
	return false
}

// onBeforeAgentStart resets per-user-prompt reminder and mid-run nudge budgets,
// and when `todo.eager` is `"preferred"` or `"always"`, injects omp's first-turn
// eager todo prelude (and arms forced wire `tool_choice: todo` for `"always"`).
// Must NOT reset on agent_start because PiG emits agent_start on every continuation run.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:147-153,180-219
func (a *app) onBeforeAgentStart(ctx sdk.Context, data map[string]any) (any, error) {
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return nil, nil
	}
	storeMu.Lock()
	st := stateLocked(key)
	st.reminderCount = 0
	st.reminderAwaitingProgress = false
	st.mutationsSinceLastTouch = 0
	st.midRunNudgeCount = 0
	hasPhases := len(st.phases) > 0
	storeMu.Unlock()

	// Default ("default") is a strict no-op: zero change to default sessions.
	mode := settingString(ctx, "todo.eager", "default")
	if mode != "preferred" && mode != "always" {
		return nil, nil
	}
	if !settingBool(ctx, "todo.enabled", true) || gate.PlanActive(ctx) || hasPhases {
		return nil, nil
	}
	activeTools, activeErr := ctx.GetActiveTools()
	if activeErr == nil && !slices.Contains(activeTools, "todo") {
		return nil, nil
	}
	if ForcedToolChoice != nil && ForcedToolChoice(key) != "" {
		return nil, nil
	}
	promptText, hasPrompt := data["prompt"].(string)
	if hasPrompt {
		trimmed := strings.TrimRight(promptText, " \t\r\n")
		if strings.HasSuffix(trimmed, "?") || strings.HasSuffix(trimmed, "!") {
			return nil, nil
		}
		if hasPriorUserMessage(ctx) {
			return nil, nil
		}
	} else if hasPriorUserMessage(ctx) {
		return nil, nil
	}

	forced := mode == "always"
	rendered := renderEagerTodoPrelude(forced)
	if forced && hasPrompt && SetForcedToolChoice != nil {
		SetForcedToolChoice(key, "todo")
	}
	return map[string]any{
		"message": map[string]any{
			"customType": "eager-todo-prelude",
			"content":    rendered,
			"display":    false,
		},
	}, nil
}

// onToolResult records tool progress so subsequent stops can re-escalate reminders
// and counts mutating tool results for mid-run nudges.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:156-163
func (a *app) onToolResult(ctx sdk.Context, data map[string]any) (any, error) {
	toolName, _ := data["toolName"].(string)
	isErr, _ := data["isError"].(bool)
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return nil, nil
	}
	storeMu.Lock()
	st := stateLocked(key)
	if toolName == "todo" {
		st.mutationsSinceLastTouch = 0
	} else if !isErr && mutatingTools[toolName] {
		st.mutationsSinceLastTouch++
	}
	st.reminderAwaitingProgress = false
	storeMu.Unlock()
	return nil, nil
}

// onTurnEnd injects a mid-run reconciliation nudge after threshold mutating tool results.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:348-377
func (a *app) onTurnEnd(ctx sdk.Context, data map[string]any) (any, error) {
	a.flushPendingHudState(ctx)
	// Mid-run nudge only fires between turns that ran tools, never on a final
	// text-only stop (upstream: session/agent-session.ts:2754).
	toolResults, _ := data["toolResults"].([]any)
	if len(toolResults) == 0 {
		return nil, nil
	}
	mode := settingString(ctx, "todo.midRunNudges", "capped")
	if mode == "off" {
		return nil, nil
	}
	if !settingBool(ctx, "todo.enabled", true) || !settingBool(ctx, "todo.reminders", true) {
		return nil, nil
	}
	if gate.PlanActive(ctx) {
		return nil, nil
	}
	activeTools, activeErr := ctx.GetActiveTools()
	if activeErr == nil && !slices.Contains(activeTools, "todo") {
		return nil, nil
	}
	key := a.effectiveSessionKey(ctx)
	if key == "" {
		return nil, nil
	}
	storeMu.Lock()
	st := stateLocked(key)
	if mode == "capped" && st.midRunNudgeCount >= midRunNudgeMaxPerCycle {
		storeMu.Unlock()
		return nil, nil
	}
	threshold := midRunNudgeMutationThreshold
	if mode == "backoff" {
		shift := min(st.midRunNudgeCount, midRunNudgeBackoffDoublings)
		threshold = midRunNudgeMutationThreshold * (1 << shift)
	}
	if st.mutationsSinceLastTouch < threshold {
		storeMu.Unlock()
		return nil, nil
	}
	openCount := 0
	for _, p := range st.phases {
		for _, t := range p.Tasks {
			if t.Status == StatusPending || t.Status == StatusInProgress {
				openCount++
			}
		}
	}
	if openCount == 0 {
		storeMu.Unlock()
		return nil, nil
	}
	st.mutationsSinceLastTouch = 0
	st.midRunNudgeCount++
	storeMu.Unlock()

	plural := ""
	if openCount != 1 {
		plural = "s"
	}
	nudge := fmt.Sprintf("<system-reminder>\n%d todo item%s still open. If you finished a task since last `todo` update, mark it done now so progress stays visible; otherwise keep working.\n</system-reminder>", openCount, plural)
	entry := map[string]any{
		"type":       "custom_message",
		"customType": "mid-run-todo-nudge",
		"content":    nudge,
		"display":    false,
	}
	existing, _ := data["entries"].([]any)
	nextEntries := make([]any, 0, len(existing)+1)
	nextEntries = append(nextEntries, existing...)
	nextEntries = append(nextEntries, entry)
	return map[string]any{"entries": nextEntries}, nil
}

func (a *app) onSessionReplay(ctx sdk.Context, _ map[string]any) (any, error) {
	a.replayFromSession(ctx)
	return nil, nil
}

func (a *app) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	a.hasCtx = false
	a.mu.Unlock()
	if key := a.effectiveSessionKey(ctx); key != "" {
		lifecycle.End(key)
	}
	if ctx.HasUI() {
		_ = ctx.SetWidget(WidgetKey, nil)
	}
	return nil, nil
}

// Register wires omp's todo tool, /todo slash command, session replay hooks,
// HUD widget, mid-run nudge, lifecycle cleanup, and stop-reminder hook onto e.
func Register(e *sdk.Extension) {
	ensureSurfaceListener()
	lifecycle.OnEnd("todo", releaseSession)
	a := newApp()
	todoDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:             "todo",
		Label:            "Todo",
		Description:      strings.TrimRight(todoPrompt, "\r\n"),
		Parameters:       toolSchema(),
		ExecutionMode:    "sequential",
		PrepareArguments: a.prepareTodoArguments,
		Execute:          a.handleTool,
	})
	e.RegisterTool(intent.Tool(todoDef))
	intent.RegisterSync(e, todoDef)
	e.Command("todo", "View or edit the session todo list", a.handleCommand)

	e.OnSessionStart(a.onSessionReplay)
	e.OnEvent(sdk.EventSessionCompact, a.onSessionReplay)
	e.OnEvent(sdk.EventSessionTree, a.onSessionReplay)
	e.OnSessionShutdown(a.onSessionShutdown)
	e.OnEvent(sdk.EventBeforeAgentStart, a.onBeforeAgentStart)
	e.OnEvent(sdk.EventToolResult, a.onToolResult)
	e.OnEvent(sdk.EventTurnEnd, a.onTurnEnd)
	e.OnEvent(sdk.EventAgentBeforeSettle, a.onAgentBeforeSettle)
}
