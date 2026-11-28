package modes

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Mode name constants returned by Current.
const (
	ModeNone       = ""
	ModePlan       = "plan"
	ModePlanPaused = "plan_paused"
	ModeGoal       = "goal"
	ModeGoalPaused = "goal_paused"
	ModeLoop       = "loop"
	ModeLoopPaused = "loop_paused"
)

// Custom session entry types persisted via ctx.AppendEntry so mode state survives
// session resume and tree navigation.
const (
	entryTypeModeChange    = "mode_change"
	entryTypeGoalCompleted = "goal-completed"
)

// Status bar keys pushed via ctx.SetStatus.
const (
	statusKeyMode = "omp:mode"
	statusKeyLoop = "omp:loop"
)

// ParentSession resolves whether a calling context belongs to a child (subagent)
// session and, if so, returns its parent's session ID. The integrator wires this
// from `internal/agents` so `gate.PlanActive(ctx)` and `modes.PlanActive(ctx)`
// walk up to the top-level ancestor session.
var ParentSession func(ctx sdk.Context) (parentSessionID string, isSubagent bool)

// VibeActive reports whether ctx's session currently has Vibe mode enabled.
// Assigned by the integrator (`modes.VibeActive = vibe.Active`) so `/plan`,
// `/goal`, `/guided-goal`, and `/clear` refuse entry while Vibe mode is active,
// matching omp (`interactive-mode.ts:6275, 6543, 7325, 7634, 7679`).
var VibeActive func(ctx sdk.Context) bool

// ResolveRole resolves a model role selector (e.g. "@smol", "@slow:high",
// "pi/plan", "*") to a provider, model ID, and optional thinking level.
// Wired by the integrator to `providerwire.ResolveRoleWithThinking`.
var ResolveRole func(ctx sdk.Context, selector string) (provider, id, thinking string, ok bool)

// SetForcedToolChoice arms a turn-scoped wire-level `tool_choice` override for
// sessionID. Wired by the integrator to `providerwire.SetForcedToolChoice`.
var SetForcedToolChoice func(sessionID, toolName string)

// ClearForcedToolChoice clears any pending wire-level `tool_choice` override
// for sessionID. Wired by the integrator to `providerwire.ClearForcedToolChoice`.
var ClearForcedToolChoice func(sessionID string)

// defaultStore is the single process-wide, session-ID-keyed store shared by
// every extension instance PiG creates (one per session / reload / replacement).
var (
	storeMu      sync.RWMutex
	defaultStore = newManager()
)

// Current returns the active mode for ctx's session: "plan", "plan_paused",
// "goal", "goal_paused", "loop", "loop_paused", or "" when no mode is active.
// Plan and goal take precedence over loop when both are active.
func Current(ctx sdk.Context) string {
	storeMu.RLock()
	m := defaultStore
	storeMu.RUnlock()
	return m.currentMode(ctx)
}

// PlanActive reports whether ctx's session, or the top-level ancestor session
// resolved via ParentSession, is currently in plan mode (active, not paused).
func PlanActive(ctx sdk.Context) bool {
	storeMu.RLock()
	m := defaultStore
	storeMu.RUnlock()
	return m.planActive(ctx)
}

// TurnBudgetFor returns the parsed `+Nk`/`+Nk!` turn budget for ctx's session
// (nil when none was specified on the latest user turn). Consumed by
// `internal/eval` for the `budget` helper and hard `agent()` ceiling.
func TurnBudgetFor(ctx sdk.Context) *TurnBudget {
	storeMu.RLock()
	m := defaultStore
	storeMu.RUnlock()
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.sessions[sid]
	if st == nil || st.turnBudget == nil {
		return nil
	}
	cp := *st.turnBudget
	return &cp
}

// TurnBudgetSnapshot returns the active turn budget (`total`, `spent` output
// tokens since the user turn began, and `hard`). `spent` measures assistant
// output tokens produced since the turn-start baseline rather than context size
// (upstream: session-manager.ts:3332-3346, budget-bridge.ts:33-48).
func TurnBudgetSnapshot(ctx sdk.Context) (total *int, spent int, hard bool) {
	storeMu.RLock()
	m := defaultStore
	storeMu.RUnlock()
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, 0, false
	}
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil {
		m.mu.Unlock()
		return nil, 0, false
	}
	var tb *TurnBudget
	if st.turnBudget != nil {
		cp := *st.turnBudget
		tb = &cp
	}
	hasBase := st.turnBudgetHasBaseline
	baseOut := st.turnBudgetBaselineOut
	var goalTotal *int
	goalSpent := 0
	if st.goalState != nil && st.goalState.Enabled && st.goalState.Goal.TokenBudget != nil {
		gb := *st.goalState.Goal.TokenBudget
		goalTotal = &gb
		goalSpent = st.goalState.Goal.TokensUsed
	}
	m.mu.Unlock()

	if tb != nil {
		t := tb.Total
		total = &t
		hard = tb.Hard
		if hasBase {
			cur := m.readCurrentSessionUsage(ctx)
			if delta := cur.Output - baseOut; delta > 0 {
				spent = delta
			}
		}
		return total, spent, hard
	}
	if goalTotal != nil {
		return goalTotal, goalSpent, false
	}
	return nil, 0, false
}

// manager owns all per-session state keyed strictly by session ID.
type manager struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	now      func() int64  // unix milliseconds; overridable in tests
	loopWait time.Duration // delay before loop resubmit (800ms in omp; configurable in tests)
}

func newManager() *manager {
	return &manager{
		sessions: map[string]*sessionState{},
		now:      defaultNowMs,
		loopWait: defaultLoopResubmitDelay,
	}
}

type sessionState struct {
	// sessionCtx is the context captured at session_start for work that outlives
	// a single request handler (e.g. deferred loop timer, compaction callbacks).
	sessionCtx    sdk.Context
	hasSessionCtx bool

	// Plan mode state
	planEnabled          bool
	planPaused           bool
	planFilePath         string
	planReferencePath    string
	planReferenceSent    bool
	planHasEntered       bool
	planPrevTools        []string
	planPrevModel        string
	planPrevThinking     string
	planProposeUsedTurn  bool
	planAskUsedTurn      bool
	planReminderCount    int
	planAwaitingProgress bool
	pendingPlanReview    *PlanApprovalDetails

	// Goal mode state
	goalState             *GoalModeState
	goalToolAddedByMode   bool
	goalTurnBaseline      GoalTokenUsage
	goalHasTurnBaseline   bool
	goalWallLastMs        int64
	goalWallActiveID      string
	goalBudgetReportedFor string
	goalSuppressNext      bool
	goalPrevActivity      string
	goalPendingContTurns  int

	// Loop mode state
	loopEnabled      bool
	loopPaused       bool
	loopPrompt       string
	loopLimit        *LoopLimitRuntime
	loopCondition    *LoopConditionConfig
	loopCondCancel   context.CancelFunc
	loopResubmitStop chan struct{}

	// Turn budget (+Nk / +Nk!) and baseline assistant output tokens at turn start.
	turnBudget            *TurnBudget
	turnBudgetHasBaseline bool
	turnBudgetBaselineOut int
	// /force:<tool> next-turn notice
	forcedTool string
	// /cleanse single-active-run guard
	cleanseActive bool
	cleanseCancel func()
}

// strictSessionID returns the session ID from ctx, or "" when it cannot be
// resolved. An empty session ID is never mapped to a shared "default" or cwd
// bucket so unrelated sessions can never collide or release each other's state.
func strictSessionID(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

func (m *manager) stateLocked(sid string) *sessionState {
	st, ok := m.sessions[sid]
	if !ok {
		st = &sessionState{
			planReferencePath: defaultPlanFileURL,
		}
		m.sessions[sid] = st
	}
	return st
}

func (m *manager) endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	st := m.sessions[sessionID]
	if st == nil {
		m.mu.Unlock()
		return
	}
	cancel := st.loopCondCancel
	stopCh := st.loopResubmitStop
	cleanseCancel := st.cleanseCancel
	st.loopCondCancel = nil
	st.loopResubmitStop = nil
	st.cleanseActive = false
	st.cleanseCancel = nil
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	if ClearForcedToolChoice != nil {
		ClearForcedToolChoice(sessionID)
	}
	if cancel != nil {
		cancel()
	}
	if cleanseCancel != nil {
		cleanseCancel()
	}
	if stopCh != nil {
		close(stopCh)
	}
}

func (m *manager) currentMode(ctx sdk.Context) string {
	sid := strictSessionID(ctx)
	if sid == "" {
		return ModeNone
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.sessions[sid]
	if st == nil {
		return ModeNone
	}
	switch {
	case st.planEnabled:
		return ModePlan
	case st.goalState != nil && (st.goalState.Enabled || st.goalState.Mode == "exiting"):
		return ModeGoal
	case st.planPaused:
		return ModePlanPaused
	case st.goalState != nil && st.goalState.Goal.Status == GoalStatusPaused:
		return ModeGoalPaused
	case st.loopEnabled && st.loopPaused:
		return ModeLoopPaused
	case st.loopEnabled:
		return ModeLoop
	default:
		return ModeNone
	}
}

// planActive checks the calling session and, if ParentSession identifies it as
// a subagent, its top-level ancestor session (`internal/agents.ParentSession`
// walks the full child->parent chain via `ResolveTopParent`).
func (m *manager) planActive(ctx sdk.Context) bool {
	sid := strictSessionID(ctx)
	m.mu.Lock()
	if sid != "" {
		if st := m.sessions[sid]; st != nil && st.planEnabled {
			m.mu.Unlock()
			return true
		}
	}
	m.mu.Unlock()

	if ParentSession != nil {
		if parentID, isSub := ParentSession(ctx); isSub && parentID != "" && parentID != sid {
			m.mu.Lock()
			st := m.sessions[parentID]
			active := st != nil && st.planEnabled
			m.mu.Unlock()
			return active
		}
	}
	return false
}

// anyPlanActive reports whether any live session currently has plan mode
// enabled. Used only to fail closed in `planGatePolicy` when the host fails to
// report a session ID.
func (m *manager) anyPlanActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.sessions {
		if st != nil && st.planEnabled {
			return true
		}
	}
	return false
}

func (m *manager) syncStatus(ctx sdk.Context, st *sessionState) {
	if !ctx.HasUI() {
		return
	}
	switch {
	case st.planEnabled:
		ctx.SetStatus(statusKeyMode, "PLAN")
	case st.planPaused:
		ctx.SetStatus(statusKeyMode, "PLAN (paused)")
	case st.goalState != nil && st.goalState.Enabled:
		label := "GOAL"
		if st.goalState.Goal.TokenBudget != nil {
			label = fmt.Sprintf("GOAL %s/%s", formatCommaInt(st.goalState.Goal.TokensUsed), formatCommaInt(*st.goalState.Goal.TokenBudget))
		}
		ctx.SetStatus(statusKeyMode, label)
	case st.goalState != nil && st.goalState.Goal.Status == GoalStatusPaused:
		ctx.SetStatus(statusKeyMode, "GOAL (paused)")
	default:
		ctx.ClearStatus(statusKeyMode)
	}
	if st.loopEnabled {
		var parts []string
		if st.loopPaused {
			parts = append(parts, "LOOP (paused)")
		} else {
			parts = append(parts, "LOOP")
		}
		if st.loopLimit != nil {
			parts = append(parts, DescribeLoopLimitRuntime(*st.loopLimit))
		}
		if st.loopCondition != nil {
			parts = append(parts, DescribeLoopCondition(*st.loopCondition))
		}
		ctx.SetStatus(statusKeyLoop, strings.Join(parts, " · "))
	} else {
		ctx.ClearStatus(statusKeyLoop)
	}
}

func notifyInfo(ctx sdk.Context, msg string) {
	ctx.Notify(msg, "info")
}

func notifyWarn(ctx sdk.Context, msg string) {
	ctx.Notify(msg, "warning")
}

func notifyError(ctx sdk.Context, msg string) {
	ctx.Notify(msg, "error")
}

func formatCommaInt(n int) string {
	if n < 0 {
		return "-" + formatCommaInt(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func parseSubcommand(args string) (verb, rest string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.IndexAny(trimmed, " \t\r\n")
	if idx < 0 {
		return strings.ToLower(trimmed), ""
	}
	return strings.ToLower(trimmed[:idx]), strings.TrimSpace(trimmed[idx+1:])
}
