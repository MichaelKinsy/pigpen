package modes

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

// Register wires omp's modes (plan, goal, loop, btw), magic keywords, hidden
// tools (`propose`, `goal`), non-conflicting slash commands, the `internal/gate`
// plan-mode policy, and `internal/lifecycle` cleanup onto e.
//
// PiG builds a fresh extension instance per session/reload/replacement, so
// Register runs many times in one process. All callbacks bind to the single
// package-level `defaultStore` keyed strictly by session ID; repeated Register
// calls are idempotent and never disturb live sessions.
// Must not panic or perform I/O at registration time.
func Register(e *sdk.Extension) {
	storeMu.RLock()
	m := defaultStore
	storeMu.RUnlock()
	registerWithManager(e, m)
}

func registerWithManager(e *sdk.Extension, m *manager) {
	// Install the package-level plan-mode gate policy and predicate (idempotent
	// across repeated Register calls; always routes to the shared store).
	gate.SetPolicy(m.planGatePolicy)
	gate.SetPlanActive(m.planActive)

	// Register lifecycle cleanup keyed by area name (replaces prior closure with
	// an identical closure over the shared manager, so every session ID is cleaned
	// up cleanly regardless of how many times Register ran).
	lifecycle.OnEnd("modes", m.endSession)

	// Special staged-action command `propose` (plan mode) and hidden top-level tool
	// `goal` (goal mode). Both register with DefaultActive=false.
	// `propose` uses ToolExposureCodemode and registers with toolcmd as a special
	// command (applies only while plan mode is active) so toolcmd owns its bash
	// mounting vs top-level activation and host ExecuteTool invokes it with full
	// validation, hooks, and gate enforcement.
	// upstream: packages/coding-agent/src/tools/index.ts:944-987
	defaultOff := false
	e.RegisterTool(intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:          proposeToolName,
		Label:         "Propose",
		Description:   "Submit the plan for approval by its <slug> (matching local://<slug>-plan.md). Valid only while plan mode is active.",
		Parameters:    proposeToolSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleProposeTool,
	})))
	toolcmd.RegisterSpecialCommand(proposeToolName, m.planActive)

	e.RegisterTool(intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:          goalToolName,
		Label:         "Goal",
		Description:   strings.TrimSpace(goalToolPrompt),
		Parameters:    goalToolSchema(),
		DefaultActive: &defaultOff,
		Execute:       m.handleGoalTool,
	})))

	// Slash commands (only names that do NOT collide with PiG's built-ins;
	// `/copy <arg>` and `/force:<tool>` are intercepted in EventInput).
	// Descriptions match omp v19.1.1 verbatim (deviations documented in UPSTREAM.md).
	e.RegisterCommand("plan", sdk.CommandOptions{
		Description: "Toggle plan mode (agent plans before executing)",
		Handler:     m.handlePlanCommand,
	})
	e.RegisterCommand("plan-review", sdk.CommandOptions{
		Description: "Re-open the plan review for the latest plan (plan mode only)",
		Handler:     m.handlePlanReviewCommand,
	})
	e.RegisterCommand("goal", sdk.CommandOptions{
		Description: "Toggle goal mode (persistent autonomous objective for this session)",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "set ", Label: "set", Description: "Set or replace the goal"},
			{Value: "show ", Label: "show", Description: "Show current goal details"},
			{Value: "pause ", Label: "pause", Description: "Pause the current goal"},
			{Value: "resume ", Label: "resume", Description: "Resume a paused goal"},
			{Value: "drop ", Label: "drop", Description: "Drop the current goal"},
			{Value: "budget ", Label: "budget", Description: "Adjust the token budget"},
		}),
		Handler: m.handleGoalCommand,
	})
	e.RegisterCommand("guided-goal", sdk.CommandOptions{
		Description: "Have the agent interview you in chat, then set up goal mode",
		Handler:     m.handleGuidedGoalCommand,
	})
	e.RegisterCommand("loop", sdk.CommandOptions{
		Description: "Toggle loop mode. While enabled, the next prompt you send re-submits after every yield. Bound it with a count/duration, or gate it with `--until '<cmd>'` / `--while '<cmd>'` — the command's exit status decides whether the next iteration runs. Escape suspends the ongoing loop; /loop again to disable.",
		Handler:     m.handleLoopCommand,
	})
	e.RegisterCommand("btw", sdk.CommandOptions{
		Description: "Ask a side question, or browse this session's BTW history",
		Handler:     m.handleBtwCommand,
	})
	e.RegisterCommand("context", sdk.CommandOptions{
		Description: "Browse context usage and detailed content",
		Handler:     m.handleContextCommand,
	})
	e.RegisterCommand("usage", sdk.CommandOptions{
		Description: "Show provider usage and limits",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "show ", Label: "show", Description: "Show provider usage and limits"},
			{Value: "reset ", Label: "reset", Description: "Spend a saved provider rate-limit reset"},
		}),
		Handler: m.handleUsageCommand,
	})
	e.RegisterCommand("handoff", sdk.CommandOptions{
		Description: "Compact in the background with a handoff summary (alias for /compact handoff)",
		Handler:     m.handleHandoffCommand,
	})
	e.RegisterCommand("branch", sdk.CommandOptions{
		Description: "Rewind to a previous message, keeping the old path as a branch",
		Handler:     m.handleBranchCommand,
	})
	e.RegisterCommand("rewind", sdk.CommandOptions{
		Description: "Rewind to a previous message, keeping the old path as a branch",
		Handler:     m.handleBranchCommand,
	})
	e.RegisterCommand("clear", sdk.CommandOptions{
		Description: "Clear the conversation context in place, keeping the session",
		Handler:     m.handleClearCommand,
	})
	e.RegisterCommand("tools", sdk.CommandOptions{
		Description: "Show tools currently visible to the agent",
		Handler:     m.handleToolsCommand,
	})
	e.RegisterCommand("effort", sdk.CommandOptions{
		Description: "Set reasoning effort (thinking level, intelligence) for this session; shift+tab cycles levels",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "off ", Label: "off", Description: "Disable extended thinking"},
			{Value: "minimal ", Label: "minimal", Description: "Minimal reasoning effort"},
			{Value: "low ", Label: "low", Description: "Low reasoning effort"},
			{Value: "medium ", Label: "medium", Description: "Medium reasoning effort"},
			{Value: "high ", Label: "high", Description: "High reasoning effort"},
			{Value: "xhigh ", Label: "xhigh", Description: "Extra-high reasoning effort"},
			{Value: "max ", Label: "max", Description: "Maximum reasoning effort"},
		}),
		Handler: m.handleEffortCommand,
	})
	e.RegisterCommand("switch", sdk.CommandOptions{
		Description: "Switch model for this session (same as alt+p); accepts provider/id and :level",
		Handler:     m.handleSwitchCommand,
	})
	e.RegisterCommand("rename", sdk.CommandOptions{
		Description: "Rename the current session (omit title to generate)",
		Handler:     m.handleRenameCommand,
	})
	e.RegisterCommand("dump", sdk.CommandOptions{
		Description: "Copy session transcript to clipboard",
		Handler:     m.handleDumpCommand,
	})
	e.RegisterCommand("jobs", sdk.CommandOptions{
		Description: "Show async background jobs status",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "full ", Label: "full", Description: "Show full, untruncated command lines"},
			{Value: "kill ", Label: "kill", Description: "Cancel a running background job"},
		}),
		Handler: m.handleJobsCommand,
	})
	e.RegisterCommand("queue", sdk.CommandOptions{
		Description: "Queue a message for after the agent yields",
		Handler:     m.handleQueueCommand,
	})
	e.RegisterCommand("force", sdk.CommandOptions{
		Description: "Force next turn to use a specific tool",
		Handler:     m.handleForceCommand,
	})
	e.RegisterCommand("exit", sdk.CommandOptions{
		Description: "Exit the application",
		Handler:     m.handleExitCommand,
	})
	e.RegisterCommand("q", sdk.CommandOptions{
		Description: "Quit the application",
		Handler:     m.handleExitCommand,
	})
	e.RegisterCommand("import-omp", sdk.CommandOptions{
		Description: "Import and switch to an omp session JSONL (defaults to latest for current cwd; --full keeps pre-compaction history)",
		Handler:     m.handleImportOmpCommand,
	})
	e.RegisterCommand("commit", sdk.CommandOptions{
		Description: "Commit only the changes this agent and its subagents made",
		Handler:     m.handleCommitCommand,
	})
	e.RegisterCommand("open", sdk.CommandOptions{
		Description: "Open the last link from the conversation in your browser (or pick one with /copy)",
		Handler:     m.handleOpenCommand,
	})
	e.RegisterCommand("skillful", sdk.CommandOptions{
		Description: "Toggle listing available skills in the system prompt (session only)",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "on ", Label: "on", Description: "List skills in the prompt for this session"},
			{Value: "off ", Label: "off", Description: "Omit the skills listing for this session"},
			{Value: "status ", Label: "status", Description: "Show skill listing status"},
		}),
		Handler: m.handleSkillfulCommand,
	})
	e.RegisterCommand("modelpreset", sdk.CommandOptions{
		Description: "Save and switch model presets (role models + thinking level)",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "list ", Label: "list", Description: "List saved presets"},
			{Value: "save ", Label: "save", Description: "Save the current role models and thinking level"},
			{Value: "switch ", Label: "switch", Description: "Apply a saved preset"},
			{Value: "delete ", Label: "delete", Description: "Delete a saved preset"},
		}),
		Handler: m.handleModelPresetCommand,
	})
	e.RegisterCommand("tan", sdk.CommandOptions{
		Description: "Run a full background agent on tangential work",
		Handler:     m.handleTanCommand,
	})
	e.RegisterCommand("cleanse", sdk.CommandOptions{
		Description: "Detect and fix project diagnostics with weighted parallel subagents",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "--all ", Label: "--all", Description: "Run every discovered checker without prompting"},
			{Value: "--tests ", Label: "--tests", Description: "Include test suites"},
			{Value: "cancel ", Label: "cancel", Description: "Cancel an active cleanse run"},
		}),
		Handler: m.handleCleanseCommand,
	})
	e.RegisterCommand("prewalk", sdk.CommandOptions{
		Description: "Arm, restart, or cancel a one-shot model handoff",
		GetArgumentCompletions: staticSubcommandCompletions([]sdk.AutocompleteItem{
			{Value: "restart ", Label: "restart", Description: "Return to the planning model and re-arm the handoff (default prewalk.from -> prewalk.into)"},
			{Value: "off ", Label: "off", Description: "Cancel this session's handoff without changing the active model"},
			{Value: "status ", Label: "status", Description: "Show current prewalk state and target model"},
		}),
		Handler: m.handlePrewalkCommand,
	})
	e.RegisterCommand("omfg", sdk.CommandOptions{
		Description: "Forge a TTSR rule from a complaint to stop a recurring behavior",
		Handler:     m.handleOmfgCommand,
	})

	// Event hooks
	e.OnSessionStart(m.onSessionStart)
	e.OnEvent(sdk.EventSessionTree, m.onSessionTree)
	// Intentionally NOT replaying on EventSessionCompact: compaction must keep
	// live goal/plan state intact (omp goals/runtime.ts + agent-session-goal-midrun-compaction.test.ts).
	e.OnSessionShutdown(m.onSessionShutdown)

	e.OnEvent(sdk.EventInput, m.onInput)
	e.OnEvent(sdk.EventBeforeAgentStart, m.onBeforeAgentStart)
	e.OnEvent(sdk.EventAgentStart, m.onAgentStart)
	e.OnToolResult(m.onToolResult)
	e.OnEvent(sdk.EventTurnEnd, m.onTurnEnd)
	e.OnEvent(sdk.EventAgentBeforeSettle, m.onAgentBeforeSettle)
	e.OnEvent(sdk.EventAgentEnd, m.onAgentEnd)
}

func staticSubcommandCompletions(items []sdk.AutocompleteItem) sdk.ArgumentCompletionsFunc {
	return func(prefix string) ([]sdk.AutocompleteItem, error) {
		if strings.Contains(prefix, " ") {
			return nil, nil
		}
		lower := strings.ToLower(prefix)
		var out []sdk.AutocompleteItem
		for _, item := range items {
			if strings.HasPrefix(strings.ToLower(item.Label), lower) {
				out = append(out, item)
			}
		}
		return out, nil
	}
}

// onSessionStart captures the session-scoped context, replays persisted mode
// state from the active branch (cold resume pauses an active goal while keeping
// budget-limited goals enabled), and syncs mode-specific tools (`propose`, `goal`).
func (m *manager) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	st.sessionCtx = ctx
	st.hasSessionCtx = true
	m.mu.Unlock()

	m.replayFromBranch(ctx, sid, true)

	m.mu.Lock()
	st = m.stateLocked(sid)
	goalOn := st.goalState != nil
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	// Only `goal` is managed directly here (`propose` is owned by internal/toolcmd).
	if active, err := ctx.GetActiveTools(); err == nil && len(active) > 0 {
		next := append([]string(nil), active...)
		changed := false
		if !goalOn && containsString(next, goalToolName) {
			next = removeString(next, goalToolName)
			changed = true
		} else if goalOn && !containsString(next, goalToolName) {
			next = append(next, goalToolName)
			changed = true
		}
		if changed {
			ctx.SetActiveTools(next)
		}
	}

	// Auto-enter plan mode on fresh empty interactive sessions when plan.defaultOnStartup is true.
	m.maybeEnterPlanDefaultOnStartup(ctx, sid)
	// Auto-arm prewalk on session start when prewalk.enabled is true (default false).
	m.maybeArmConfiguredPrewalk(ctx, sid)
	return nil, nil
}

// onSessionTree replays mode state from the active branch path (not all entries
// across abandoned branches), preserving active/budget-limited goal state on
// the selected branch.
func (m *manager) onSessionTree(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	m.replayFromBranch(ctx, sid, false)
	return nil, nil
}

// onSessionShutdown triggers lifecycle.End for ONLY the ending session ID.
// Never releases anything when sessionID cannot be resolved.
func (m *manager) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	if ctx.HasUI() {
		ctx.ClearStatus(statusKeyMode)
		ctx.ClearStatus(statusKeyLoop)
	}
	lifecycle.End(sid)
	return nil, nil
}

// replayFromBranch reconstructs plan/goal mode state from custom `mode_change`
// entries along the CURRENT branch only (root -> leaf), never from abandoned
// sibling branches.
//
// On cold resume (`coldResume == true`), matches omp `goals/runtime.ts:267-291`:
//   - active goal -> paused (`Enabled: false`, `Goal.Status = paused`) + persists `goal_paused`
//   - budget-limited goal -> stays `Enabled: true`, `Goal.Status = budget-limited`
//   - paused goal -> `Enabled: false`, `Goal.Status = paused`
//   - complete / dropped -> cleared (no active goal)
//
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:6173-6248
// upstream: packages/coding-agent/src/goals/runtime.ts:267-291
func (m *manager) replayFromBranch(ctx sdk.Context, sid string, coldResume bool) {
	rawEntries, err := ctx.GetEntries()
	if err != nil {
		return
	}
	branchEntries, err := ctx.GetBranch()
	if err != nil {
		return
	}
	branchIDs := make(map[string]bool, len(branchEntries))
	for _, be := range branchEntries {
		if be.ID != "" {
			branchIDs[be.ID] = true
		}
	}
	filterByBranch := len(branchIDs) > 0

	var (
		planEnabled       bool
		planPaused        bool
		planFilePath      string
		planHasEntered    bool
		planPrevModel     string
		planPrevThinking  string
		planReferencePath = defaultPlanFileURL
		goalState         *GoalModeState
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
		if filterByBranch && env.ID != "" && !branchIDs[env.ID] {
			continue
		}
		var mc struct {
			Mode string         `json:"mode"`
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(env.Data, &mc) != nil {
			continue
		}
		switch mc.Mode {
		case "plan":
			planEnabled = true
			planPaused = false
			planHasEntered = true
			goalState = nil
			if p, _ := mc.Data["planFilePath"].(string); p != "" {
				planFilePath = p
			} else {
				planFilePath = defaultPlanFileURL
			}
			if pm, _ := mc.Data["prePlanModelIdentity"].(string); pm != "" {
				planPrevModel = pm
			}
			if pt, _ := mc.Data["prePlanThinkingLevel"].(string); pt != "" {
				planPrevThinking = pt
			}
		case "plan_paused":
			planEnabled = false
			planPaused = true
			planHasEntered = true
			planPrevModel = ""
			planPrevThinking = ""
		case "goal", "goal_paused":
			planEnabled = false
			planPaused = false
			planFilePath = ""
			planPrevModel = ""
			planPrevThinking = ""
			if rawGoal, ok := mc.Data["goal"]; ok {
				b, _ := json.Marshal(rawGoal)
				var g Goal
				if json.Unmarshal(b, &g) == nil && g.ID != "" {
					switch g.Status {
					case GoalStatusComplete, GoalStatusDropped:
						goalState = nil
					case GoalStatusActive:
						if mc.Mode == "goal_paused" {
							g.Status = GoalStatusPaused
							goalState = &GoalModeState{Enabled: false, Mode: "active", Goal: g}
						} else {
							goalState = &GoalModeState{Enabled: true, Mode: "active", Goal: g}
						}
					case GoalStatusBudgetLimited:
						goalState = &GoalModeState{Enabled: mc.Mode == "goal", Mode: "active", Goal: g}
					case GoalStatusPaused:
						goalState = &GoalModeState{Enabled: false, Mode: "active", Goal: g}
					}
				}
			}
		case "none":
			planEnabled = false
			planPaused = false
			planFilePath = ""
			planPrevModel = ""
			planPrevThinking = ""
			goalState = nil
			if p, _ := mc.Data["approvedPlanFilePath"].(string); p != "" {
				planReferencePath = p
			}
		}
	}

	persistedPause := false
	var pausedGoal Goal
	if coldResume && goalState != nil && goalState.Goal.Status == GoalStatusActive {
		goalState.Enabled = false
		goalState.Goal.Status = GoalStatusPaused
		goalState.Goal.UpdatedAt = m.now()
		pausedGoal = cloneGoal(goalState.Goal)
		persistedPause = true
	}

	now := m.now()
	var restoreModel, restoreThinking string
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.planEnabled && !planEnabled && st.planPrevModel != "" {
		restoreModel = st.planPrevModel
		restoreThinking = st.planPrevThinking
	}
	st.planEnabled = planEnabled
	st.planPaused = planPaused
	st.planFilePath = planFilePath
	st.planHasEntered = planHasEntered
	st.planPrevModel = planPrevModel
	st.planPrevThinking = planPrevThinking
	st.planReferencePath = planReferencePath
	st.goalState = goalState
	if goalState != nil && goalState.Enabled && isGoalAccountingStatus(goalState.Goal) {
		st.goalWallActiveID = goalState.Goal.ID
		st.goalWallLastMs = now
	} else {
		st.goalWallActiveID = ""
	}
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	if persistedPause {
		_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
			"mode": "goal_paused",
			"data": map[string]any{"goal": pausedGoal},
		})
	}
	if restoreModel != "" {
		m.restorePlanPreviousModel(ctx, restoreModel, restoreThinking)
	}
}

// onInput intercepts `/copy <arg>` and `/force:<tool>`, captures loop prompts,
// parses `+Nk` turn budgets (recording only, no invented notice), applies
// `chillax` thinking reduction on per-message-effort models, and injects
// omp's hidden magic-keyword `<system-notice>` messages.
func (m *manager) onInput(ctx sdk.Context, data map[string]any) (any, error) {
	text, _ := data["text"].(string)
	source, _ := data["source"].(string)
	trimmed := strings.TrimSpace(text)

	// Intercept `/copy <arg>` (PiG's built-in `/copy` only matches bare `/copy`).
	if strings.HasPrefix(trimmed, "/copy ") {
		if err := m.handleCopyWithArgs(ctx, trimmed[len("/copy "):]); err != nil {
			return nil, err
		}
		return map[string]any{"action": "handled"}, nil
	}
	// Intercept `/force:<tool> [prompt]` colon syntax.
	if strings.HasPrefix(trimmed, "/force:") {
		if err := m.handleForceCommand(ctx, trimmed[len("/force:"):]); err != nil {
			return nil, err
		}
		return map[string]any{"action": "handled"}, nil
	}
	// Intercept `/export --copy|copy|clipboard` and `/export --themes [path]`
	// while leaving bare `/export` and `/export <path>` to PiG's built-in handler.
	if strings.HasPrefix(trimmed, "/export ") {
		exportArgs := trimmed[len("/export "):]
		if shouldInterceptExport(exportArgs) {
			if err := m.handleExportIntercept(ctx, exportArgs); err != nil {
				return nil, err
			}
			return map[string]any{"action": "handled"}, nil
		}
	}

	if source == "extension" {
		return map[string]any{"action": "continue"}, nil
	}

	if sid := strictSessionID(ctx); sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		if st.loopEnabled && trimmed != "" && !strings.HasPrefix(trimmed, "/") {
			// Manual submit supersedes any pending condition evaluation.
			if st.loopCondCancel != nil {
				st.loopCondCancel()
				st.loopCondCancel = nil
			}
			st.loopPrompt = text
			st.loopPaused = false
			m.syncStatus(ctx, st)
		}
		// Genuine user input resets goal stall suppression and plan convergence cap.
		st.goalSuppressNext = false
		st.goalPrevActivity = ""
		st.planReminderCount = 0
		st.planAwaitingProgress = false
		// Record +Nk / +Nk! turn budget for `TurnBudgetFor(ctx)` (no model-facing text;
		// upstream agent-session.ts:9091-9092).
		tb := ParseTurnBudget(text)
		st.turnBudget = tb
		st.turnBudgetHasBaseline = false
		st.turnBudgetBaselineOut = 0
		m.mu.Unlock()
		if tb != nil {
			baseOut := m.readCurrentSessionUsage(ctx).Output
			m.mu.Lock()
			if st2 := m.sessions[sid]; st2 != nil {
				st2.turnBudgetHasBaseline = true
				st2.turnBudgetBaselineOut = baseOut
			}
			m.mu.Unlock()
		}
	}

	// `chillax`: lower pinned thinking level on models supporting per-message effort.
	// `ultrathink`: notice-only here because PiG has no `auto` thinking mode
	// (upstream model-controls.ts:671-675 never mutates a pinned level for ultrathink).
	kwSettings := resolveMagicKeywordSettings(ctx)
	if mi, err := ctx.GetModelInfo(); err == nil && mi != nil && mi.Reasoning {
		if regModel, err := ctx.ModelRegistry().Find(mi.Provider, mi.ID); err == nil && regModel != nil {
			curLevel, _ := ctx.GetThinkingLevel()
			supported := sdk.GetSupportedThinkingLevels(regModel)
			if nextLevel, ok := resolveChillaxThinkingChange(text, curLevel, regModel, supported, kwSettings); ok {
				ctx.SetThinkingLevel(nextLevel)
			}
		}
	}

	activeTools, _ := ctx.GetActiveTools()
	kwCtx := DefaultMagicKeywordContext(activeTools)
	notices := BuildMagicKeywordNotices(text, kwCtx, kwSettings)
	if len(notices) == 0 {
		return map[string]any{"action": "continue"}, nil
	}

	// Inject each triggered keyword notice as a hidden custom message
	// (display: false, triggerTurn: false).
	// upstream: packages/coding-agent/src/session/agent-session.ts:9111-9118
	for _, n := range notices {
		_ = ctx.SendMessage(n.CustomType, n.Content, false, sdk.SendMessageOptions{
			TriggerTurn: sdk.Bool(false),
		})
	}
	return map[string]any{"action": "continue"}, nil
}

// onBeforeAgentStart injects active plan-mode / plan-reference / goal-mode /
// forced-tool context as hidden custom messages for the upcoming turn.
// `/force` is never dropped by plan or goal mode: when both a mode context and
// a forced-tool notice exist, the forced-tool notice is delivered via a hidden
// `SendMessage` while the mode context returns in the hook result.
func (m *manager) onBeforeAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	planOn := st.planEnabled
	planRefPath := st.planReferencePath
	needPlanRef := !planOn && !st.planReferenceSent && planRefPath != "" && planRefPath != defaultPlanFileURL
	goalState := cloneGoalState(st.goalState)
	forced := st.forcedTool
	st.forcedTool = ""
	var planPrompt string
	if planOn {
		planPrompt = m.buildPlanModeActivePrompt(ctx, st)
	}
	if needPlanRef {
		st.planReferenceSent = true
	}
	m.mu.Unlock()

	var primaryType, primaryContent string
	switch {
	case planOn && planPrompt != "":
		primaryType = "plan-mode-context"
		primaryContent = planPrompt
	case goalState != nil && goalState.Enabled && goalState.Goal.Status == GoalStatusActive:
		primaryType = "goal-mode-context"
		primaryContent = buildGoalModeContextMessage(ctx, goalState.Goal)
	case needPlanRef:
		if content, ok, err := readPlanFile(ctx, planRefPath); err == nil && ok && strings.TrimSpace(content) != "" {
			primaryType = "plan-mode-reference"
			primaryContent = renderPlanReferencePrompt(planRefPath, content)
		}
	}

	if prewalkPlan := m.takePrewalkPlanContext(sid); prewalkPlan != "" {
		if primaryContent == "" {
			primaryType = prewalkPlanMessageType
			primaryContent = prewalkPlan
		} else {
			_ = ctx.SendMessage(prewalkPlanMessageType, prewalkPlan, false, sdk.SendMessageOptions{
				TriggerTurn: sdk.Bool(false),
			})
		}
	}

	if forced != "" {
		forcedContent := fmt.Sprintf("<system-notice>You MUST invoke the `%s` tool on this turn.</system-notice>", forced)
		if primaryContent == "" {
			primaryType = "forced-tool-notice"
			primaryContent = forcedContent
		} else {
			_ = ctx.SendMessage("forced-tool-notice", forcedContent, false, sdk.SendMessageOptions{
				TriggerTurn: sdk.Bool(false),
			})
		}
	}

	if primaryContent == "" {
		return nil, nil
	}
	return map[string]any{
		"message": map[string]any{
			"customType": primaryType,
			"content":    primaryContent,
			"display":    false,
		},
	}, nil
}

func (m *manager) onAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	now := m.now()

	m.mu.Lock()
	st := m.stateLocked(sid)
	st.planProposeUsedTurn = false
	st.planAskUsedTurn = false
	st.planAwaitingProgress = false
	goalAccounting := st.goalState != nil && st.goalState.Enabled && isGoalAccountingStatus(st.goalState.Goal)
	if goalAccounting {
		if st.goalWallActiveID != st.goalState.Goal.ID {
			st.goalWallActiveID = st.goalState.Goal.ID
			st.goalWallLastMs = now
		}
	} else {
		st.goalHasTurnBaseline = false
	}
	m.mu.Unlock()

	// Only walk the branch for a usage baseline when goal accounting is active.
	if goalAccounting {
		usage := m.readCurrentSessionUsage(ctx)
		m.mu.Lock()
		if st = m.sessions[sid]; st != nil && st.goalState != nil && st.goalState.Enabled {
			st.goalTurnBaseline = usage
			st.goalHasTurnBaseline = true
		}
		m.mu.Unlock()
	}
	return nil, nil
}

func (m *manager) onToolResult(ctx sdk.Context, data map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	toolName, _ := data["toolName"].(string)
	isErr, _ := data["isError"].(bool)

	if toolName == "ask" && !isErr {
		m.mu.Lock()
		if st := m.sessions[sid]; st != nil {
			st.planAskUsedTurn = true
			st.planReminderCount = 0
			st.planAwaitingProgress = false
		}
		m.mu.Unlock()
	}

	m.recordPrewalkToolResult(sid, toolName, isErr, data)

	if toolName == goalToolName {
		m.flushGoalUsage(ctx, sid, false)
	} else {
		m.flushGoalUsage(ctx, sid, true)
	}
	return nil, nil
}

// onAgentBeforeSettle handles:
//  1. Plan-mode convergence reminder (up to PLAN_MODE_REMINDER_MAX = 3) when a
//     completed turn ends without calling `ask` or `propose`.
//  2. Goal-mode autonomous continuation when an active goal is open and making
//     progress.
//
// Skips both when `outcome != "completed"` (error / aborted), matching omp
// `agent-session.ts:13757-13759`.
func (m *manager) onAgentBeforeSettle(ctx sdk.Context, data map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	m.flushGoalUsage(ctx, sid, false)

	// Respect boundary outcome: never inject reminders or continuations after
	// an aborted or errored turn.
	if outcome, _ := data["outcome"].(string); outcome != "" && outcome != "completed" {
		return nil, nil
	}

	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil {
		m.mu.Unlock()
		return nil, nil
	}
	planOn := st.planEnabled
	planAsked := st.planAskUsedTurn
	planProposed := st.planProposeUsedTurn
	planAwaiting := st.planAwaitingProgress
	planRemCount := st.planReminderCount
	loopOn := st.loopEnabled
	goalState := cloneGoalState(st.goalState)
	suppressGoal := st.goalSuppressNext
	pendingCont := st.goalPendingContTurns
	m.mu.Unlock()

	// 1. Plan mode convergence: remind up to PLAN_MODE_REMINDER_MAX (3) times
	// when the turn ended without `ask` or `propose`.
	if planOn && !planAsked && !planProposed && !planAwaiting && planRemCount < planReminderMax {
		m.mu.Lock()
		if st = m.sessions[sid]; st != nil {
			st.planReminderCount++
			st.planAwaitingProgress = true
		}
		m.mu.Unlock()
		return appendBoundaryContinue(data, "plan-mode-reminder", renderPlanDecisionReminder(ctx)), nil
	}

	// 2. Goal mode auto-continuation (suppressed while loop or plan is active).
	if !planOn && !loopOn && goalState != nil && goalState.Enabled && goalState.Goal.Status == GoalStatusActive {
		if pendingCont > 0 {
			branch, _ := ctx.GetBranch()
			activity := GoalContinuationActivity(branch)
			m.mu.Lock()
			if st = m.sessions[sid]; st != nil {
				st.goalPendingContTurns--
				st.goalSuppressNext = activity == "" || activity == st.goalPrevActivity
				st.goalPrevActivity = activity
				suppressGoal = st.goalSuppressNext
			}
			m.mu.Unlock()
		}
		if !suppressGoal {
			m.mu.Lock()
			if st = m.sessions[sid]; st != nil {
				st.goalPendingContTurns++
			}
			m.mu.Unlock()
			return appendBoundaryContinue(data, "goal-continuation", RenderGoalPrompt("continuation", goalState.Goal)), nil
		}
	}

	return nil, nil
}

func appendBoundaryContinue(data map[string]any, customType, content string) map[string]any {
	entry := map[string]any{
		"type":       "custom_message",
		"customType": customType,
		"content":    content,
		"display":    false,
	}
	existing, _ := data["entries"].([]any)
	nextEntries := make([]any, 0, len(existing)+1)
	nextEntries = append(nextEntries, existing...)
	nextEntries = append(nextEntries, entry)
	return map[string]any{
		"entries":  nextEntries,
		"continue": true,
	}
}

// lastAssistantStopReason inspects `data["messages"]` from PiG's `agent_end`
// event (`{type, messages}` per `coding/extension/events.go:518-521`) and
// returns the `stopReason` of the last assistant message ("stop", "aborted",
// "error", "length", "toolUse", or "").
func lastAssistantStopReason(data map[string]any) string {
	rawMsgs, ok := data["messages"].([]any)
	if !ok {
		return ""
	}
	for i := len(rawMsgs) - 1; i >= 0; i-- {
		msg, ok := rawMsgs[i].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		if reason, _ := msg["stopReason"].(string); reason != "" {
			return reason
		}
	}
	return ""
}

// onAgentEnd handles post-turn transitions:
//   - Opening the plan review dialog after `propose` succeeded (in non-command
//     context, offering context-preserving options + hint for `/plan-review`)
//   - Finalizing a completed goal (`mode == "exiting"`)
//   - Pausing an active goal on `stopReason == "aborted"` (Esc interrupt)
//   - Pausing an armed `/loop` on `stopReason == "aborted"` OR `"error"`, else
//     running the next `/loop` iteration after omp's 800 ms grace delay
//
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:3151-3158
// upstream: packages/coding-agent/src/goals/runtime.ts:244-265
func (m *manager) onAgentEnd(ctx sdk.Context, data map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	stopReason := lastAssistantStopReason(data)
	aborted := stopReason == "aborted"
	errored := stopReason == "error"

	m.flushGoalUsage(ctx, sid, false)

	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil {
		m.mu.Unlock()
		return nil, nil
	}
	pendingReview := st.pendingPlanReview
	st.pendingPlanReview = nil
	goalExiting := st.goalState != nil && st.goalState.Mode == "exiting"
	goalActive := st.goalState != nil && st.goalState.Enabled && st.goalState.Goal.Status == GoalStatusActive
	if st.cleanseActive && st.cleanseCancel == nil {
		st.cleanseActive = false
	}
	m.mu.Unlock()

	if pendingReview != nil {
		if err := m.runPlanReviewDialog(ctx, sid, *pendingReview, false); err != nil {
			notifyError(ctx, fmt.Sprintf("Plan review failed: %v", err))
		}
		return nil, nil
	}

	if goalExiting {
		m.finalizeCompletedGoal(ctx, sid)
		return nil, nil
	}
	if aborted || errored {
		m.pauseLoop(ctx, sid)
		if aborted && goalActive {
			m.goalPause(ctx, sid)
			notifyInfo(ctx, "Goal mode paused.")
		}
		return nil, nil
	}
	if nextPrompt := m.maybeRunLoopIteration(ctx, sid); nextPrompt != "" {
		_ = ctx.SendUserMessage(nextPrompt, "followUp")
	}

	return nil, nil
}
