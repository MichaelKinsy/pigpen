package modes

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:726-798
// upstream: packages/coding-agent/src/session/prewalk.ts
// upstream: packages/coding-agent/src/session/settings.ts:96-172
// upstream: packages/coding-agent/src/prompts/system/prewalk-plan.md
// upstream: packages/coding-agent/src/prompts/system/prewalk-continue.md
// upstream: packages/coding-agent/src/prompts/system/prewalk-checklist.md

const (
	prewalkDefaultFrom = "@default"
	prewalkDefaultInto = "@smol"

	prewalkPlanMessageType      = "prewalk-plan"
	prewalkContinueMessageType  = "prewalk-continue"
	prewalkChecklistMessageType = "prewalk-checklist"
)

type prewalkTarget struct {
	Selector string // original selector (e.g. "@smol" or "openai/gpt-5-mini")
	Provider string
	ModelID  string
	Thinking string
}

func (t prewalkTarget) qualified() string {
	if t.Provider != "" && t.ModelID != "" {
		return t.Provider + "/" + t.ModelID
	}
	if t.ModelID != "" {
		return t.ModelID
	}
	return t.Selector
}

type prewalkState struct {
	gen             uint64
	armed           bool
	target          prewalkTarget
	suppressed      bool
	handedOff       bool
	handedOffTo     string
	handedOffTool   string
	planInjected    bool
	continuePending bool
	todoSeen        bool
	pendingAction   string // gated implementation tool seen in current turn via onToolResult
}

// prewalkMutatingToolCommands lists wrapped bash tool commands that mutate the
// workspace (write approval tier in omp). Read-only commands (read, grep, glob,
// lsp, debug, ask, etc.) and exec-tier commands (bash, eval, task) do not arm
// the handoff (upstream: packages/coding-agent/src/session/prewalk.ts:50-77).
var prewalkMutatingToolCommands = map[string]bool{
	"edit":     true,
	"write":    true,
	"ast_edit": true,
}

func isPrewalkEnabledSetting(ctx sdk.Context) bool {
	return readModeSettingBool(ctx, "prewalk.enabled", false)
}

func prewalkFromSetting(ctx sdk.Context) string {
	if v := strings.TrimSpace(readModeSettingString(ctx, "prewalk.from")); v != "" {
		return v
	}
	return prewalkDefaultFrom
}

func prewalkIntoSetting(ctx sdk.Context) string {
	if v := strings.TrimSpace(readModeSettingString(ctx, "prewalk.into")); v != "" {
		return v
	}
	return prewalkDefaultInto
}

// resolvePrewalkSelector resolves a model/role selector (`@smol`, `@default`,
// `pi/<role>`, `provider/id`, with optional `:thinking` suffix) into a
// prewalkTarget.
func resolvePrewalkSelector(ctx sdk.Context, raw string) (prewalkTarget, error) {
	selector := strings.TrimSpace(raw)
	if selector == "" {
		return prewalkTarget{}, fmt.Errorf("Model selector is empty")
	}
	modelSpec, thinking := splitModelThinkingSuffix(selector)
	if isRoleSelector(modelSpec) {
		if ResolveRole == nil {
			return prewalkTarget{}, fmt.Errorf("Model %q not found", selector)
		}
		prov, id, roleThinking, ok := ResolveRole(ctx, selector)
		if !ok || id == "" {
			return prewalkTarget{}, fmt.Errorf("Model %q not found", selector)
		}
		if thinking == "" {
			thinking = roleThinking
		}
		return prewalkTarget{
			Selector: selector,
			Provider: prov,
			ModelID:  id,
			Thinking: thinking,
		}, nil
	}
	// L11: Validate non-role selectors at arm time: try ResolveRole if available,
	// otherwise require explicit `provider/model` format.
	if ResolveRole != nil {
		if prov, id, roleThinking, ok := ResolveRole(ctx, selector); ok && id != "" {
			if thinking == "" {
				thinking = roleThinking
			}
			return prewalkTarget{
				Selector: selector,
				Provider: prov,
				ModelID:  id,
				Thinking: thinking,
			}, nil
		}
	}
	slash := strings.IndexByte(modelSpec, '/')
	if slash <= 0 || slash >= len(modelSpec)-1 {
		return prewalkTarget{}, fmt.Errorf("Model %q not found (expected a role or provider/model selector)", selector)
	}
	return prewalkTarget{
		Selector: selector,
		Provider: modelSpec[:slash],
		ModelID:  modelSpec[slash+1:],
		Thinking: thinking,
	}, nil
}

// prewalkTargetIsNoop reports whether the session is already on the requested
// target model and thinking level.
// upstream: packages/coding-agent/src/session/prewalk.ts:189-196
func prewalkTargetIsNoop(ctx sdk.Context, target prewalkTarget) bool {
	curModel := strings.TrimSpace(ctx.ModelQualified())
	curID := strings.TrimSpace(ctx.Model())
	curProv := strings.TrimSpace(ctx.ModelProvider())
	if mi, err := ctx.GetModelInfo(); err == nil && mi != nil && mi.ID != "" {
		curID = mi.ID
		curProv = mi.Provider
		if curProv != "" {
			curModel = curProv + "/" + curID
		} else {
			curModel = curID
		}
	}
	targetQual := target.qualified()
	sameModel := false
	if curModel != "" && targetQual != "" && strings.EqualFold(curModel, targetQual) {
		sameModel = true
	} else if target.Provider == "" && curID != "" && strings.EqualFold(curID, target.ModelID) {
		sameModel = true
	} else if target.Provider != "" && curProv != "" && strings.EqualFold(curProv, target.Provider) && strings.EqualFold(curID, target.ModelID) {
		sameModel = true
	}
	if !sameModel {
		return false
	}
	if target.Thinking == "" {
		return true
	}
	curThinking, _ := ctx.GetThinkingLevel()
	return strings.EqualFold(strings.TrimSpace(curThinking), strings.TrimSpace(target.Thinking))
}

// applyPrewalkModelSwitch switches the session to target via SetModelNamed and
// optional SetThinkingLevel.
func applyPrewalkModelSwitch(ctx sdk.Context, target prewalkTarget) (bool, error) {
	spec := target.qualified()
	if spec == "" {
		return false, fmt.Errorf("empty model target")
	}
	ok, err := ctx.SetModelNamed(spec)
	if err != nil || !ok {
		return false, err
	}
	if target.Thinking != "" {
		ctx.SetThinkingLevel(target.Thinking)
	}
	return true, nil
}

// prewalkImplementationTool inspects a completed tool result (from EventToolResult
// or EventTurnEnd) and returns the implementation tool name if it is a workspace-
// mutating action that arms the prewalk handoff, or "" otherwise.
// upstream: packages/coding-agent/src/session/prewalk.ts:67-77
func prewalkImplementationTool(toolName string, result map[string]any) string {
	switch toolName {
	case "edit", "write":
		if details, ok := result["details"].(map[string]any); ok && details != nil {
			if noFiles, _ := details["touchesNoFiles"].(bool); noFiles {
				return ""
			}
		}
		return toolName
	}
	// Check wrapped tool commands executed via bash.
	if toolcmd.TransportTools[toolName] {
		// First check raw map fields if tier/readOnlyHint are stamped (upstream shape).
		if details, ok := result["details"].(map[string]any); ok && details != nil {
			if rawList, ok := details["toolCommands"].([]any); ok {
				for _, item := range rawList {
					m, ok := item.(map[string]any)
					if !ok || m == nil {
						continue
					}
					mode, _ := m["mode"].(string)
					if mode != "execute" {
						continue
					}
					if ro, _ := m["readOnlyHint"].(bool); ro {
						continue
					}
					tool, _ := m["tool"].(string)
					if tool == "" {
						continue
					}
					if innerDetails, ok := m["details"].(map[string]any); ok && innerDetails != nil {
						if noFiles, _ := innerDetails["touchesNoFiles"].(bool); noFiles {
							continue
						}
					}
					if tier, hasTier := m["tier"].(string); hasTier && tier != "" {
						if tier == "write" {
							return tool
						}
						continue
					}
					if prewalkMutatingToolCommands[tool] {
						return tool
					}
				}
				return ""
			}
		}
		// Fallback to typed DispatchRecord slice.
		for _, d := range toolcmd.ToolCommandDispatches(toolName, result) {
			if d.Mode == "execute" && prewalkMutatingToolCommands[d.Tool] {
				return d.Tool
			}
		}
	}
	return ""
}

// maybeArmConfiguredPrewalk arms prewalk at session start when `prewalk.enabled`
// is true (default false), not suppressed, and not already on the target model.
// Default-session safety: when `prewalk.enabled == false`, returns immediately
// with zero host calls.
func (m *manager) maybeArmConfiguredPrewalk(ctx sdk.Context, sid string) {
	if isSubagentSession(ctx) || !isPrewalkEnabledSetting(ctx) {
		return
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.prewalk.armed || st.prewalk.suppressed || st.prewalk.handedOff || st.planEnabled {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	into := prewalkIntoSetting(ctx)
	target, err := resolvePrewalkSelector(ctx, into)
	if err != nil {
		return
	}
	if prewalkTargetIsNoop(ctx, target) {
		return
	}

	m.mu.Lock()
	st = m.stateLocked(sid)
	if !st.prewalk.armed && !st.prewalk.suppressed && !st.prewalk.handedOff && !st.planEnabled {
		nextGen := st.prewalk.gen + 1
		st.prewalk = prewalkState{
			gen:             nextGen,
			armed:           true,
			target:          target,
			planInjected:    false,
			continuePending: false,
			todoSeen:        false,
		}
	}
	m.mu.Unlock()
}

// handlePrewalkCommand implements `/prewalk [into] | restart [from] [into] | off | status`.
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:726-798
func (m *manager) handlePrewalkCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyError(ctx, "Cannot use /prewalk without an active session ID.")
		return nil
	}

	fields := strings.Fields(strings.TrimSpace(args))
	verb := ""
	var rest []string
	if len(fields) > 0 {
		verb = strings.ToLower(fields[0])
		rest = fields[1:]
	}

	switch verb {
	case "status":
		if len(rest) > 0 {
			notifyWarn(ctx, "Usage: /prewalk [into] | restart [from] [into] | off | status")
			return nil
		}
		m.mu.Lock()
		pw := m.stateLocked(sid).prewalk
		m.mu.Unlock()
		switch {
		case pw.armed:
			todoNote := "todo-gated"
			if pw.todoSeen {
				todoNote = "todo seen, waiting for first edit/write"
			}
			notifyInfo(ctx, fmt.Sprintf("Prewalk: armed for %s (%s).", pw.target.qualified(), todoNote))
		case pw.handedOff:
			notifyInfo(ctx, fmt.Sprintf("Prewalk: handed off to %s (after %s).", pw.handedOffTo, pw.handedOffTool))
		case pw.suppressed:
			notifyInfo(ctx, "Prewalk: off (suppressed for this session).")
		default:
			notifyInfo(ctx, fmt.Sprintf("Prewalk: off (default target %s).", prewalkIntoSetting(ctx)))
		}
		return nil

	case "off":
		if len(rest) > 0 {
			notifyWarn(ctx, "Usage: /prewalk [into] | restart [from] [into] | off | status")
			return nil
		}
		m.mu.Lock()
		st := m.stateLocked(sid)
		wasArmed := st.prewalk.armed
		nextGen := st.prewalk.gen + 1
		st.prewalk = prewalkState{gen: nextGen, suppressed: true}
		m.mu.Unlock()
		if wasArmed {
			notifyInfo(ctx, "Prewalk off: canceled this session's pending handoff; keeping the active model.")
		} else {
			notifyInfo(ctx, "Prewalk already off for this session; keeping the active model.")
		}
		return nil

	case "restart":
		if len(rest) > 2 {
			notifyWarn(ctx, "Usage: /prewalk [into] | restart [from] [into] | off | status")
			return nil
		}
		fromSel := prewalkFromSetting(ctx)
		intoSel := prewalkIntoSetting(ctx)
		if len(rest) >= 1 {
			fromSel = rest[0]
		}
		if len(rest) >= 2 {
			intoSel = rest[1]
		}
		source, err := resolvePrewalkSelector(ctx, fromSel)
		if err != nil {
			notifyWarn(ctx, err.Error())
			return nil
		}
		target, err := resolvePrewalkSelector(ctx, intoSel)
		if err != nil {
			notifyWarn(ctx, err.Error())
			return nil
		}
		switched, err := applyPrewalkModelSwitch(ctx, source)
		if err != nil {
			notifyError(ctx, fmt.Sprintf("Prewalk restart failed to switch to %s: %v", source.qualified(), err))
			return nil
		}
		if !switched {
			notifyError(ctx, fmt.Sprintf("Model %q not found", source.qualified()))
			return nil
		}

		sourceLabel := fmt.Sprintf("%s (%s)", fromSel, source.qualified())
		targetLabel := fmt.Sprintf("%s (%s)", intoSel, target.qualified())

		if prewalkTargetIsNoop(ctx, target) {
			m.mu.Lock()
			st := m.stateLocked(sid)
			nextGen := st.prewalk.gen + 1
			st.prewalk = prewalkState{gen: nextGen}
			m.mu.Unlock()
			notifyInfo(ctx, fmt.Sprintf("Prewalk reset: using %s; %s resolves to the same model and thinking level, so no handoff was armed.", sourceLabel, intoSel))
			return nil
		}

		m.mu.Lock()
		st := m.stateLocked(sid)
		nextGen := st.prewalk.gen + 1
		st.prewalk = prewalkState{
			gen:             nextGen,
			armed:           true,
			target:          target,
			planInjected:    false,
			continuePending: false,
			todoSeen:        false,
		}
		m.mu.Unlock()
		notifyInfo(ctx, fmt.Sprintf("Prewalk restarted: using %s for planning, then switching to %s at the next edit/write (todo-gated).", sourceLabel, targetLabel))
		return nil
	}

	// Default: `/prewalk [into]`
	if len(fields) > 1 {
		notifyWarn(ctx, "Usage: /prewalk [into] | restart [from] [into] | off | status")
		return nil
	}
	intoSel := prewalkIntoSetting(ctx)
	if len(fields) == 1 {
		intoSel = fields[0]
	}
	target, err := resolvePrewalkSelector(ctx, intoSel)
	if err != nil {
		notifyWarn(ctx, err.Error())
		return nil
	}

	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.prewalk.armed {
		existing := st.prewalk.target.qualified()
		m.mu.Unlock()
		notifyInfo(ctx, fmt.Sprintf("Prewalk: already armed for %s, waiting for the first edit/write.", existing))
		return nil
	}
	m.mu.Unlock()

	if prewalkTargetIsNoop(ctx, target) {
		notifyInfo(ctx, fmt.Sprintf("Prewalk: target %s already matches the active model and thinking level; nothing to switch.", target.qualified()))
		return nil
	}

	m.mu.Lock()
	st = m.stateLocked(sid)
	nextGen := st.prewalk.gen + 1
	st.prewalk = prewalkState{
		gen:             nextGen,
		armed:           true,
		target:          target,
		planInjected:    false,
		continuePending: false,
		todoSeen:        false,
	}
	m.mu.Unlock()
	notifyInfo(ctx, fmt.Sprintf("Prewalk on: switching to %s at the next edit/write (todo-gated).", target.qualified()))
	return nil
}

// recordPrewalkToolResult tracks `todo` completion and workspace-mutating tool
// calls while prewalk is armed. Returns immediately with zero allocations when
// prewalk is not armed.
func (m *manager) recordPrewalkToolResult(sid, toolName string, isErr bool, data map[string]any) {
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || !st.prewalk.armed {
		m.mu.Unlock()
		return
	}
	if toolName == "todo" && !isErr {
		st.prewalk.todoSeen = true
	}
	if !isErr && st.prewalk.pendingAction == "" {
		if action := prewalkImplementationTool(toolName, data); action != "" {
			st.prewalk.pendingAction = action
		}
	}
	m.mu.Unlock()
}

// takePrewalkPlanContext returns the hidden `prewalk-plan.md` context once when
// prewalk is armed and has not yet injected its plan nudge.
func (m *manager) takePrewalkPlanContext(sid string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.sessions[sid]
	if st == nil || !st.prewalk.armed || st.prewalk.planInjected {
		return ""
	}
	st.prewalk.planInjected = true
	st.prewalk.continuePending = true
	return strings.TrimSpace(prewalkPlanPrompt)
}

// onTurnEnd advances an armed prewalk handoff at a completed assistant turn
// boundary (`EventTurnEnd`). Default-session safety: when prewalk is not armed,
// returns immediately with zero host calls.
// upstream: packages/coding-agent/src/session/prewalk.ts:350-473
func (m *manager) onTurnEnd(ctx sdk.Context, data map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}

	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || !st.prewalk.armed {
		m.mu.Unlock()
		return nil, nil
	}
	m.mu.Unlock()

	// Verify this turn belongs to an assistant message (if provided).
	if rawMsg, ok := data["message"].(map[string]any); ok && rawMsg != nil {
		if role, _ := rawMsg["role"].(string); role != "" && role != "assistant" {
			m.mu.Lock()
			if st := m.sessions[sid]; st != nil {
				st.prewalk.pendingAction = ""
			}
			m.mu.Unlock()
			return nil, nil
		}
		if stopReason, _ := rawMsg["stopReason"].(string); stopReason == "aborted" || stopReason == "error" {
			m.mu.Lock()
			if st := m.sessions[sid]; st != nil {
				st.prewalk.pendingAction = ""
			}
			m.mu.Unlock()
			return nil, nil
		}
	}
	// Inspect toolResults carried on EventTurnEnd (in addition to onToolResult).
	rawResults, _ := data["toolResults"].([]any)
	turnSawTodo := false
	turnAction := ""
	for _, item := range rawResults {
		tr, ok := item.(map[string]any)
		if !ok || tr == nil {
			continue
		}
		tn, _ := tr["toolName"].(string)
		isErr, _ := tr["isError"].(bool)
		if tn == "todo" && !isErr {
			turnSawTodo = true
		}
		if !isErr && turnAction == "" {
			if action := prewalkImplementationTool(tn, tr); action != "" {
				turnAction = action
			}
		}
	}

	m.mu.Lock()
	st = m.sessions[sid]
	if st == nil || !st.prewalk.armed {
		m.mu.Unlock()
		return nil, nil
	}
	if turnSawTodo {
		st.prewalk.todoSeen = true
	}
	if turnAction == "" {
		turnAction = st.prewalk.pendingAction
	}
	st.prewalk.pendingAction = ""
	gen := st.prewalk.gen
	target := st.prewalk.target
	todoSeen := st.prewalk.todoSeen
	planInjected := st.prewalk.planInjected
	continuePending := st.prewalk.continuePending
	m.mu.Unlock()

	if prewalkTargetIsNoop(ctx, target) {
		m.mu.Lock()
		if st = m.sessions[sid]; st != nil && st.prewalk.armed && st.prewalk.gen == gen {
			st.prewalk = prewalkState{gen: gen + 1}
		}
		m.mu.Unlock()
		notifyInfo(ctx, fmt.Sprintf("Prewalk: target %s already matches the active model and thinking level; nothing to switch.", target.qualified()))
		return nil, nil
	}

	// Gate implementation handoff on `todo` having been called (unless `todo` is
	// not an active tool).
	todoGateOpen := todoSeen
	if !todoGateOpen {
		if activeTools, err := ctx.GetActiveTools(); err == nil && len(activeTools) > 0 && !containsString(activeTools, "todo") {
			todoGateOpen = true
		}
	}

	actionTool := ""
	if todoGateOpen {
		actionTool = turnAction
	}

	if actionTool == "" {
		hasToolResults := len(rawResults) > 0
		m.mu.Lock()
		st = m.sessions[sid]
		if st == nil || !st.prewalk.armed {
			m.mu.Unlock()
			return nil, nil
		}
		injectPlan := false
		injectContinue := false
		if !planInjected {
			st.prewalk.planInjected = true
			st.prewalk.continuePending = true
			injectPlan = true
		} else if hasToolResults {
			st.prewalk.continuePending = true
		} else if continuePending {
			st.prewalk.continuePending = false
			injectContinue = true
		}
		m.mu.Unlock()

		if injectPlan {
			_ = ctx.SendMessage(prewalkPlanMessageType, strings.TrimSpace(prewalkPlanPrompt), false, sdk.SendMessageOptions{
				DeliverAs:   "steer",
				TriggerTurn: sdk.Bool(false),
			})
			notifyInfo(ctx, "Prewalk: injected deep-plan nudge.")
		} else if injectContinue {
			_ = ctx.SendMessage(prewalkContinueMessageType, strings.TrimSpace(prewalkContinuePrompt), false, sdk.SendMessageOptions{
				DeliverAs:   "steer",
				TriggerTurn: sdk.Bool(false),
			})
		}
		return nil, nil
	}

	// Trigger one-shot model handoff.
	switched, err := applyPrewalkModelSwitch(ctx, target)
	m.mu.Lock()
	if st = m.sessions[sid]; st != nil && st.prewalk.armed && st.prewalk.gen == gen {
		if switched && err == nil {
			st.prewalk = prewalkState{
				gen:           gen + 1,
				armed:         false,
				handedOff:     true,
				handedOffTo:   target.qualified(),
				handedOffTool: actionTool,
			}
		} else {
			st.prewalk = prewalkState{gen: gen + 1}
		}
	}
	m.mu.Unlock()
	if err != nil {
		notifyWarn(ctx, fmt.Sprintf("Prewalk: failed to switch to %s: %v", target.qualified(), err))
		return nil, nil
	}
	if !switched {
		notifyWarn(ctx, fmt.Sprintf("Prewalk: could not switch to %s.", target.qualified()))
		return nil, nil
	}

	notifyInfo(ctx, fmt.Sprintf("Prewalk: switched to %s after first %s call.", target.qualified(), actionTool))
	_ = ctx.SendMessage(prewalkChecklistMessageType, strings.TrimSpace(prewalkChecklistPrompt), false, sdk.SendMessageOptions{
		DeliverAs:   "steer",
		TriggerTurn: sdk.Bool(false),
	})
	return nil, nil
}
