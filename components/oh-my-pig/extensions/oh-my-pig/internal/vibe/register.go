package vibe

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/modes"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
)

// upstream:
//   - packages/coding-agent/src/slash-commands/builtin-modes.ts:316-332
//   - packages/coding-agent/src/modes/interactive-mode.ts:5806-5810, 5883-5906, 6115-6134, 7391-7604
//   - packages/coding-agent/src/session/agent-session.ts:8618-8653, 8966-8978

const (
	entryTypeModeChange = "mode_change"
	statusKeyVibe       = "omp-vibe"
)

// Hooks that integrators or tests may override for cross-area integration.
var (
	// ActiveModeCheck returns the active conflicting mode ("plan", "plan_paused",
	// "goal", "goal_paused", or "") for ctx. Defaults to `modes.Current`.
	ActiveModeCheck = modes.Current

	// DefaultSubagentRunner optionally overrides defaultRunSubagent (tests only).
	DefaultSubagentRunner SubagentRunner

	// DefaultFollowUpRunner optionally overrides defaultRunFollowUpTurn (tests only).
	DefaultFollowUpRunner FollowUpRunner

	// DefaultSteerWorker optionally overrides defaultSteerWorker (tests only).
	DefaultSteerWorker SteerFunc
)

type sessionModeState struct {
	enabled       bool
	previousTools []string
	ownerScope    OwnerScope
	sessionCtx    sdk.Context
	hasSessionCtx bool
}

type controller struct {
	mu       sync.Mutex
	reg      *SessionRegistry
	sessions map[string]*sessionModeState
}

func newController(reg *SessionRegistry) *controller {
	return &controller{
		reg:      reg,
		sessions: make(map[string]*sessionModeState),
	}
}

var (
	defaultCtrlMu sync.RWMutex
	defaultCtrl   = newController(Global())
)

// Active reports whether Vibe mode is currently enabled for ctx's session.
func Active(ctx sdk.Context) bool {
	sid := strictSessionID(ctx)
	if sid == "" {
		return false
	}
	defaultCtrlMu.RLock()
	c := defaultCtrl
	defaultCtrlMu.RUnlock()
	return c.isEnabled(sid)
}

// StateFor returns the ModeState for ctx's session, or nil when inactive.
func StateFor(ctx sdk.Context) *ModeState {
	if !Active(ctx) {
		return nil
	}
	return &ModeState{Enabled: true}
}

// ResetForTests clears package-level Vibe mode state and resets the global
// SessionRegistry. Intended for tests only.
func ResetForTests() {
	ResetGlobalForTests()
	defaultCtrlMu.Lock()
	defaultCtrl = newController(Global())
	defaultCtrlMu.Unlock()
	DefaultSubagentRunner = nil
	DefaultFollowUpRunner = nil
	DefaultSteerWorker = nil
	ActiveModeCheck = modes.Current
}

// Register wires omp's Vibe mode onto e:
//   - `/vibe [prompt]` slash command
//   - `session_start` / `session_tree` branch replay & rehydration
//   - `before_agent_start` hidden `<vibe-mode>` director directive (only while active)
//   - `context` hook stripping stale persisted `vibe-mode-context` messages when inactive
//   - `lifecycle.OnEnd("vibe", ...)` releasing only the ending session
//
// Zero tools are registered at startup so a default session (where `/vibe` is
// inactive) advertises ZERO new tools and changes ZERO default prompt lines.
// The five Vibe tools (`vibe_spawn`, `vibe_send`, `vibe_wait`, `vibe_kill`,
// `vibe_list`) are installed dynamically via `ctx.RegisterTool` on `/vibe` entry
// and deactivated (restoring the exact pre-vibe active toolset) on exit.
func Register(e *sdk.Extension) {
	if e == nil {
		return
	}
	defaultCtrlMu.RLock()
	c := defaultCtrl
	defaultCtrlMu.RUnlock()
	registerWithController(e, c)
}

func registerWithController(e *sdk.Extension, c *controller) {
	jobs.EnsureDeliveryListener()
	lifecycle.OnEnd("vibe", c.endSession)

	// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:316-332
	e.RegisterCommand("vibe", sdk.CommandOptions{
		Description: "Toggle vibe mode (direct persistent fast/good worker sessions; read-only toolset)",
		Handler:     c.handleVibeCommand,
	})

	e.OnSessionStart(c.onSessionStart)
	e.OnEvent(sdk.EventSessionTree, c.onSessionTree)
	e.OnEvent(sdk.EventBeforeAgentStart, c.onBeforeAgentStart)
	e.OnEvent(sdk.EventContext, c.onContext)
	e.OnSessionShutdown(c.onSessionShutdown)
}

func strictSessionID(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	if id, err := ctx.GetSessionID(); err == nil {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	if id, err := ctx.SessionManager().GetSessionID(); err == nil {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (c *controller) stateLocked(sid string) *sessionModeState {
	st := c.sessions[sid]
	if st == nil {
		st = &sessionModeState{}
		c.sessions[sid] = st
	}
	return st
}

func (c *controller) isEnabled(sid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.sessions[sid]
	return st != nil && st.enabled
}

func (c *controller) requireActive(ctx sdk.Context) error {
	sid := strictSessionID(ctx)
	if sid == "" || !c.isEnabled(sid) {
		return sdk.NewToolError("Vibe mode is not active. Run /vibe to enter vibe mode first.")
	}
	return nil
}

func (c *controller) endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	delete(c.sessions, sessionID)
	c.mu.Unlock()
	c.reg.EndSession(sessionID)
}

func (c *controller) parentSession(ctx sdk.Context) *ParentSession {
	sid := strictSessionID(ctx)
	var sessionFile string
	if sf, err := ctx.SessionManager().GetSessionFile(); err == nil && sf != nil {
		sessionFile = *sf
	} else if sf, err := ctx.GetSessionFile(); err == nil && sf != nil {
		sessionFile = *sf
	}
	agentID, _ := agents.AgentIdentityFor(ctx)
	if agentID == "" {
		agentID = agents.MainAgentID
	}

	// Prefer the long-lived session_start context for background spawns so
	// child sessions outlive the originating tool call's request cancellation.
	longCtx := ctx
	c.mu.Lock()
	if st := c.sessions[sid]; st != nil && st.hasSessionCtx && st.sessionCtx.Err() == nil {
		longCtx = st.sessionCtx
	}
	c.mu.Unlock()

	return &ParentSession{
		SDK:            longCtx,
		OwnerID:        agentID,
		SessionID:      sid,
		SessionFile:    sessionFile,
		Cwd:            ctx.Cwd(),
		ActiveModel:    strings.TrimSpace(ctx.ModelQualified()),
		AppendEntry:    ctx.AppendEntry,
		GetEntries:     ctx.GetEntries,
		GetBranch:      ctx.GetBranch,
		SettingsStore:  settings.StoreFor(ctx),
		AgentRegistry:  agents.DefaultRuntime().Registry,
		AgentsRuntime:  agents.DefaultRuntime(),
		SubagentRunner: DefaultSubagentRunner,
		FollowUpRunner: DefaultFollowUpRunner,
		SteerWorker:    DefaultSteerWorker,
	}
}

func (c *controller) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	jobs.BindSession(sid, ctx)
	c.mu.Lock()
	st := c.stateLocked(sid)
	st.sessionCtx = ctx
	st.hasSessionCtx = true
	c.mu.Unlock()

	// Replay branch to see if a resumed session was left in vibe mode.
	c.reconcileFromBranch(ctx, sid)
	return nil, nil
}

func (c *controller) onSessionTree(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	c.reconcileFromBranch(ctx, sid)
	return nil, nil
}

func (c *controller) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	if ctx.HasUI() {
		ctx.ClearStatus(statusKeyVibe)
	}
	lifecycle.End(sid)
	return nil, nil
}

// reconcileFromBranch replays persisted `mode_change` entries on the active
// branch and rehydrates persisted Vibe worker records when resuming a vibe session.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:6155-6208
func (c *controller) reconcileFromBranch(ctx sdk.Context, sid string) {
	rawEntries, err := ctx.GetEntries()
	if err != nil {
		return
	}
	var branchIDs map[string]bool
	if branch, err := ctx.GetBranch(); err == nil && len(branch) > 0 {
		branchIDs = make(map[string]bool, len(branch))
		for _, be := range branch {
			if be.ID != "" {
				branchIDs[be.ID] = true
			}
		}
	}

	var (
		latestMode    string
		persistedPrev []string
	)
	for _, raw := range rawEntries {
		var env struct {
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Type != "custom" || env.CustomType != entryTypeModeChange {
			continue
		}
		if branchIDs != nil && env.ID != "" && !branchIDs[env.ID] {
			continue
		}
		var mc struct {
			Mode string         `json:"mode"`
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(env.Data, &mc) != nil {
			continue
		}
		latestMode = mc.Mode
		if mc.Mode == "vibe" {
			persistedPrev = extractStringSliceParam(mc.Data["previousTools"])
		} else {
			persistedPrev = nil
		}
	}

	parent := c.parentSession(ctx)
	_, _ = c.reg.Rehydrate(parent)

	if latestMode == "vibe" {
		c.mu.Lock()
		st := c.sessions[sid]
		wasEnabled := st != nil && st.enabled
		var prevScope OwnerScope
		if st != nil {
			prevScope = st.ownerScope
		}
		c.mu.Unlock()

		newScope, scopeErr := c.reg.OwnerScope(parent)
		sameScope := wasEnabled && scopeErr == nil && prevScope.matches(newScope)
		if sameScope {
			return
		}
		if wasEnabled {
			// Switching from another Vibe scope (or a re-anchored branch) lost
			// the live pre-vibe toolset to the reduced Vibe set; only in this
			// case restore the target's persisted `previousTools` snapshot
			// (upstream: interactive-mode.ts:6150-6157, 6199-6204).
			c.reg.SuspendScope(prevScope)
			c.mu.Lock()
			st := c.stateLocked(sid)
			st.enabled = false
			st.ownerScope = OwnerScope{}
			c.mu.Unlock()
			_ = c.enterVibeMode(ctx, sid, false, persistedPrev)
		} else {
			// Cold resume or switch from a non-vibe session: snapshot the
			// freshly built live toolset rather than a stale persisted list.
			_ = c.enterVibeMode(ctx, sid, false, nil)
		}
		return
	}

	// Branch is not in vibe mode: if vibe was previously enabled, deactivate
	// transient vibe tools without clobbering the branch's active set.
	c.mu.Lock()
	st := c.sessions[sid]
	wasEnabled := st != nil && st.enabled
	var oldScope OwnerScope
	if st != nil {
		oldScope = st.ownerScope
		st.enabled = false
		st.previousTools = nil
		st.ownerScope = OwnerScope{}
	}
	c.mu.Unlock()
	if wasEnabled {
		c.reg.SuspendScope(oldScope)
		if active, err := ctx.GetActiveTools(); err == nil {
			filtered := removeVibeTools(active)
			ctx.SetActiveTools(filtered)
		}
		if ctx.HasUI() {
			ctx.ClearStatus(statusKeyVibe)
		}
	}
}

// onBeforeAgentStart injects the hidden `<vibe-mode>` directive ONLY when Vibe
// mode is active for ctx's session. Returns (nil, nil) when inactive so default
// sessions see ZERO prompt changes.
// upstream: packages/coding-agent/src/session/agent-session.ts:8966-8978, 10074-10076
func (c *controller) onBeforeAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" || !c.isEnabled(sid) {
		return nil, nil
	}
	active, _ := ctx.GetActiveTools()
	todoAvailable := slices.Contains(active, "todo")
	content := RenderModeActivePrompt(todoAvailable)
	return map[string]any{
		"message": map[string]any{
			"customType": ModeContextMessageType,
			"content":    content,
			"display":    false,
		},
	}, nil
}

// onContext strips any persisted `vibe-mode-context` messages when Vibe mode
// is inactive, matching upstream `setVibeModeState(undefined)` and
// `convertToLlm` (`agent-session.ts:8622-8645`, `session-context.ts:492`).
func (c *controller) onContext(ctx sdk.Context, data map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" || c.isEnabled(sid) {
		return nil, nil
	}
	msgs, ok := data["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return nil, nil
	}
	filtered := make([]any, 0, len(msgs))
	removed := false
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if ok && m["role"] == "custom" && m["customType"] == ModeContextMessageType {
			removed = true
			continue
		}
		filtered = append(filtered, raw)
	}
	if !removed {
		return nil, nil
	}
	return map[string]any{"messages": filtered}, nil
}

// handleVibeCommand implements `/vibe [prompt]`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7391-7470
func (c *controller) handleVibeCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		ctx.Notify("Cannot toggle vibe mode without an active session ID.", "error")
		return nil
	}
	if c.isEnabled(sid) {
		return c.exitVibeMode(ctx, sid)
	}

	if ActiveModeCheck != nil {
		switch ActiveModeCheck(ctx) {
		case modes.ModePlan:
			ctx.Notify("Exit plan mode first.", "warning")
			return nil
		case modes.ModePlanPaused:
			ctx.Notify("Plan mode is paused — run /plan again to fully exit.", "warning")
			return nil
		case modes.ModeGoal, modes.ModeGoalPaused:
			ctx.Notify("Exit goal mode first.", "warning")
			return nil
		}
	}

	if err := c.enterVibeMode(ctx, sid, true, nil); err != nil {
		ctx.Notify(fmt.Sprintf("Failed to enter vibe mode: %v", err), "error")
		return err
	}

	initialPrompt := strings.TrimSpace(args)
	if initialPrompt != "" {
		deliverAs := ""
		if idle, err := ctx.IsIdle(); err == nil && !idle {
			deliverAs = "steer"
		}
		if err := ctx.SendUserMessage(initialPrompt, deliverAs); err != nil {
			ctx.Notify(fmt.Sprintf("Failed to send vibe prompt: %v", err), "error")
			return err
		}
	}
	return nil
}

// enterVibeMode installs the 5 ephemeral Vibe tools, switches the active toolset
// to `read` + optional `todo` + the 5 Vibe tools, records the pre-vibe snapshot,
// and updates status/journal.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7508-7570
func (c *controller) enterVibeMode(ctx sdk.Context, sid string, persistModeChange bool, previousToolsOverride []string) error {
	parent := c.parentSession(ctx)
	scope, err := c.reg.OwnerScope(parent)
	if err != nil {
		return err
	}
	c.reg.ActivateScope(scope)

	prevTools := previousToolsOverride
	if prevTools == nil {
		active, err := ctx.GetActiveTools()
		if err == nil {
			prevTools = append([]string(nil), active...)
		} else {
			prevTools = []string{}
		}
	}

	// Register the 5 Vibe tools lazily via ctx.RegisterTool so default sessions
	// never advertise them.
	for _, def := range c.buildToolDefinitions(ctx) {
		ctx.RegisterTool(def)
	}

	// Build active toolset: `read`, optional built-in `todo`, + 5 vibe tools.
	vibeTools := []string{"read"}
	if hasToolNamed(ctx, "todo") {
		vibeTools = append(vibeTools, "todo")
	}
	vibeTools = append(vibeTools, ToolNames...)
	ctx.SetActiveTools(vibeTools)

	c.mu.Lock()
	st := c.stateLocked(sid)
	st.enabled = true
	st.previousTools = append([]string(nil), prevTools...)
	st.ownerScope = scope
	c.mu.Unlock()

	if idle, err := ctx.IsIdle(); err == nil && !idle {
		todoAvailable := slices.Contains(vibeTools, "todo")
		_ = ctx.SendMessage(ModeContextMessageType, RenderModeActivePrompt(todoAvailable), false, sdk.SendMessageOptions{
			TriggerTurn: sdk.Bool(false),
			DeliverAs:   "steer",
		})
	}

	if ctx.HasUI() {
		ctx.SetStatus(statusKeyVibe, "VIBE")
	}
	if persistModeChange {
		_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
			"mode": "vibe",
			"data": map[string]any{
				"previousTools": prevTools,
			},
		})
	}
	ctx.Notify("Vibe mode enabled. You direct fast/good worker sessions; toolset is read + optional parent Todo + vibe tools.", "info")
	return nil
}

// exitVibeMode aborts any streaming director turn, terminates all live workers,
// restores the exact pre-vibe active toolset (including an empty set), persists
// `mode_change: none`, and clears status.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7572-7604
func (c *controller) exitVibeMode(ctx sdk.Context, sid string) error {
	c.mu.Lock()
	st := c.sessions[sid]
	if st == nil || !st.enabled {
		c.mu.Unlock()
		return nil
	}
	prevTools := append([]string(nil), st.previousTools...)
	scope := st.ownerScope
	c.mu.Unlock()

	if idle, err := ctx.IsIdle(); err == nil && !idle {
		ctx.Abort()
	}

	parent := c.parentSession(ctx)
	killed, killErr := c.reg.KillAll(parent, &scope)
	if killErr != nil {
		return killErr
	}

	if err := ctx.AppendEntry(entryTypeModeChange, map[string]any{"mode": "none"}); err != nil {
		return err
	}

	restored := removeVibeTools(prevTools)
	if restored == nil {
		restored = []string{}
	}
	ctx.SetActiveTools(restored)

	c.mu.Lock()
	if st := c.sessions[sid]; st != nil {
		st.enabled = false
		st.previousTools = nil
		st.ownerScope = OwnerScope{}
	}
	c.mu.Unlock()

	if ctx.HasUI() {
		ctx.ClearStatus(statusKeyVibe)
	}
	if killed > 0 {
		plural := "s"
		if killed == 1 {
			plural = ""
		}
		ctx.Notify(fmt.Sprintf("Vibe mode disabled. Killed %d worker session%s.", killed, plural), "info")
	} else {
		ctx.Notify("Vibe mode disabled.", "info")
	}
	return nil
}

func hasToolNamed(ctx sdk.Context, name string) bool {
	all, err := ctx.GetAllTools()
	if err != nil {
		return false
	}
	for _, ti := range all {
		if ti.Name == name {
			return true
		}
	}
	return false
}

func removeVibeTools(list []string) []string {
	out := make([]string, 0, len(list))
	for _, name := range list {
		if slices.Contains(ToolNames, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}
