package autolearn

import (
	_ "embed"
	"encoding/json"
	"math"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/autolearn/controller.ts:1-153
// upstream: packages/coding-agent/src/autolearn/settings.ts:10-38
// upstream: packages/coding-agent/src/prompts/system/autolearn-guidance.md
// upstream: packages/coding-agent/src/prompts/system/autolearn-nudge-autocontinue.md

const captureCustomType = "autolearn-capture"

//go:embed prompts/autolearn-guidance.md
var autolearnGuidance string

//go:embed prompts/autolearn-nudge-autocontinue.md
var autolearnNudgeAutoContinue string

// Setting reads a raw setting value for ctx (e.g. `autolearn.minToolCalls`).
// Wired by wiring.go -> settings.Any. Defaults to nil (falls back to built-in defaults).
var Setting func(ctx sdk.Context, key string) any

// GoalActive reports whether ctx's session currently has Goal Mode active.
// Wired by wiring.go -> modes.Current(ctx) == modes.ModeGoal. Defaults to nil (false).
// upstream: packages/coding-agent/src/autolearn/controller.ts:78,123
var GoalActive func(ctx sdk.Context) bool

// CaptureTurn dispatches the synthetic post-stop capture prompt.
// Overridable in tests. Defaults to sending a hidden followUp message that triggers a turn.
// upstream: packages/coding-agent/src/autolearn/controller.ts:46,142
var CaptureTurn = defaultCaptureTurn

func defaultCaptureTurn(ctx sdk.Context, prompt string) error {
	return ctx.SendMessage(captureCustomType, prompt, false, sdk.SendMessageOptions{
		TriggerTurn: sdk.Bool(true),
		DeliverAs:   "followUp",
	})
}

// BuildInstructions returns the standing Auto-Learn guidance for the system prompt
// when `manage_skill` is enabled for ctx, or "" when disabled (so default sessions
// with `autolearn.enabled: false` have zero system prompt change).
// upstream: packages/coding-agent/src/autolearn/controller.ts:36-41
func BuildInstructions(ctx sdk.Context) string {
	if !IsEnabled(ctx) {
		return ""
	}
	return strings.TrimSpace(autolearnGuidance)
}

type sessionControllerState struct {
	toolCalls             int
	turnStartedInGoalMode bool
	captureActive         bool
	captureInFlight       bool
	capturePending        bool
	pendingCtx            sdk.Context
}

// AutoLearnController tracks per-session turn tool counts and goal-mode boundaries,
// optionally triggering a post-stop capture turn when `autolearn.enabled` and
// `autolearn.autoContinue` are enabled.
// upstream: packages/coding-agent/src/autolearn/controller.ts:49-153
type AutoLearnController struct {
	mu       sync.Mutex
	sessions map[string]*sessionControllerState
}

var defaultController = NewAutoLearnController()

// NewAutoLearnController creates a new per-session AutoLearnController.
func NewAutoLearnController() *AutoLearnController {
	return &AutoLearnController{
		sessions: make(map[string]*sessionControllerState),
	}
}

func (c *AutoLearnController) stateForLocked(sessionID string) *sessionControllerState {
	st, ok := c.sessions[sessionID]
	if !ok {
		st = &sessionControllerState{}
		c.sessions[sessionID] = st
	}
	return st
}

// CleanupSession removes all tracked state for sessionID.
func (c *AutoLearnController) CleanupSession(sessionID string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	delete(c.sessions, sessionID)
	c.mu.Unlock()
}

func sessionIDForContext(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

func minToolCallsFor(ctx sdk.Context) int {
	if Setting == nil {
		return DefaultMinToolCalls
	}
	raw := Setting(ctx, SettingMinToolCalls)
	switch v := raw.(type) {
	case int:
		if v >= 0 {
			return v
		}
	case int64:
		if v >= 0 {
			return int(v)
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n >= 0 {
			return int(n)
		}
	}
	return DefaultMinToolCalls
}

// OnAgentStart records whether the turn began in goal mode.
// upstream: packages/coding-agent/src/autolearn/controller.ts:76-80
func (c *AutoLearnController) OnAgentStart(ctx sdk.Context) {
	if IsSubagent != nil && IsSubagent(ctx) {
		return
	}
	sid := sessionIDForContext(ctx)
	if sid == "" {
		return
	}
	goalActive := GoalActive != nil && GoalActive(ctx)
	c.mu.Lock()
	st := c.stateForLocked(sid)
	st.turnStartedInGoalMode = goalActive
	c.mu.Unlock()
}

// OnToolResult increments the per-turn tool-call counter.
// upstream: packages/coding-agent/src/autolearn/controller.ts:81-84
func (c *AutoLearnController) OnToolResult(ctx sdk.Context) {
	if IsSubagent != nil && IsSubagent(ctx) {
		return
	}
	sid := sessionIDForContext(ctx)
	if sid == "" {
		return
	}
	c.mu.Lock()
	st := c.stateForLocked(sid)
	st.toolCalls++
	c.mu.Unlock()
}

// OnAgentEnd snapshots and resets per-turn counters, evaluates all upstream gates,
// and optionally starts a post-stop capture turn.
// upstream: packages/coding-agent/src/autolearn/controller.ts:90-152
func (c *AutoLearnController) OnAgentEnd(ctx sdk.Context, data map[string]any) {
	if IsSubagent != nil && IsSubagent(ctx) {
		return
	}
	sid := sessionIDForContext(ctx)
	if sid == "" {
		return
	}

	isCaptureMsg := isCaptureTurnMessages(data)
	c.mu.Lock()
	st := c.stateForLocked(sid)
	// Snapshot and reset every turn so below-threshold, disabled, and plan-mode
	// stops never accumulate into a later turn.
	toolCalls := st.toolCalls
	st.toolCalls = 0
	startedInGoalMode := st.turnStartedInGoalMode
	st.turnStartedInGoalMode = false
	wasCaptureActive := st.captureActive
	if isCaptureMsg {
		st.captureActive = true
	}
	c.mu.Unlock()

	// Never trigger a nested capture from a capture turn itself (including
	// auto-retry/overflow continuations whose agent_end lacks the custom message).
	if isCaptureMsg || wasCaptureActive {
		return
	}
	// Never nudge a turn that ended in an abort (ESC, cancel, etc.).
	if lastAssistantAborted(data) {
		return
	}
	// Re-read live settings at every stop.
	if Enabled == nil || !Enabled(ctx, SettingEnabled, false) {
		return
	}
	if toolCalls < minToolCallsFor(ctx) {
		return
	}
	// Never interrupt plan-mode review.
	if gate.PlanActive(ctx) {
		return
	}
	// Never divert a goal loop (started in goal mode or still in goal mode).
	if startedInGoalMode || (GoalActive != nil && GoalActive(ctx)) {
		return
	}
	// Auto-run a capture turn only when explicitly enabled.
	if !Enabled(ctx, SettingAutoContinue, false) {
		return
	}

	c.mu.Lock()
	st = c.stateForLocked(sid)
	if st.captureInFlight {
		st.capturePending = true
		st.pendingCtx = ctx
		c.mu.Unlock()
		return
	}
	st.captureInFlight = true
	st.captureActive = true
	c.mu.Unlock()

	// Invoke CaptureTurn synchronously inside agent_end so SendMessage(followUp)
	// lands in the host queue BEFORE post-run drain checks whether queued
	// messages exist (upstream: controller.ts:140-152).
	c.runCaptureLoop(sid, ctx)
}

// OnAgentSettled clears the per-session capture-active guard once a turn chain
// fully settles so the next primary user turn can trigger capture normally.
func (c *AutoLearnController) OnAgentSettled(ctx sdk.Context) {
	sid := sessionIDForContext(ctx)
	if sid == "" {
		return
	}
	c.mu.Lock()
	if st, ok := c.sessions[sid]; ok {
		st.captureActive = false
	}
	c.mu.Unlock()
}

func (c *AutoLearnController) runCaptureLoop(sid string, ctx sdk.Context) {
	nudge := strings.TrimSpace(autolearnNudgeAutoContinue)
	curCtx := ctx
	for {
		var err error
		if fn := CaptureTurn; fn != nil {
			err = fn(curCtx, nudge)
		}
		c.mu.Lock()
		st, ok := c.sessions[sid]
		if !ok {
			c.mu.Unlock()
			return
		}
		if err != nil {
			st.captureActive = false
		}
		if !st.capturePending {
			st.captureInFlight = false
			c.mu.Unlock()
			return
		}
		st.capturePending = false
		curCtx = st.pendingCtx
		var zero sdk.Context
		st.pendingCtx = zero
		c.mu.Unlock()
	}
}

func extractEventMessages(data map[string]any) []map[string]any {
	if data == nil {
		return nil
	}
	switch raw := data["messages"].(type) {
	case []map[string]any:
		return raw
	case []any:
		out := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func lastAssistantAborted(data map[string]any) bool {
	msgs := extractEventMessages(data)
	for i := len(msgs) - 1; i >= 0; i-- {
		role, _ := msgs[i]["role"].(string)
		if role != "assistant" {
			continue
		}
		stopReason, _ := msgs[i]["stopReason"].(string)
		return stopReason == "aborted"
	}
	return false
}

func isCaptureTurnMessages(data map[string]any) bool {
	for _, m := range extractEventMessages(data) {
		if ct, _ := m["customType"].(string); ct == captureCustomType {
			return true
		}
	}
	return false
}

func registerController(e *sdk.Extension) {
	lifecycle.OnEnd("autolearn", func(sessionID string) {
		defaultController.CleanupSession(sessionID)
	})
	e.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		defaultController.OnAgentStart(ctx)
		return nil, nil
	})
	e.OnEvent(sdk.EventToolResult, func(ctx sdk.Context, _ map[string]any) (any, error) {
		defaultController.OnToolResult(ctx)
		return nil, nil
	})
	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		defaultController.OnAgentEnd(ctx, data)
		return nil, nil
	})
	e.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
		defaultController.OnAgentSettled(ctx)
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDForContext(ctx); sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}
