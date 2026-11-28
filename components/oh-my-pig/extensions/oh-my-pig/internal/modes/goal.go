package modes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/goals/state.ts
// upstream: packages/coding-agent/src/goals/runtime.ts
// upstream: packages/coding-agent/src/goals/tools/goal-tool.ts
// upstream: packages/tui/src/tools/goal.ts
// upstream: packages/coding-agent/src/modes/interactive-mode.ts (goal mode)

const goalToolName = "goal"

// GoalStatus is the lifecycle state of a tracked goal.
// upstream: packages/tui/src/tools/goal.ts:13
type GoalStatus string

const (
	GoalStatusActive        GoalStatus = "active"
	GoalStatusPaused        GoalStatus = "paused"
	GoalStatusBudgetLimited GoalStatus = "budget-limited"
	GoalStatusComplete      GoalStatus = "complete"
	GoalStatusDropped       GoalStatus = "dropped"
)

// Goal mirrors omp's Goal struct read by goalToolRenderer.
// upstream: packages/tui/src/tools/goal.ts:16-25
type Goal struct {
	ID              string     `json:"id"`
	Objective       string     `json:"objective"`
	Status          GoalStatus `json:"status"`
	TokenBudget     *int       `json:"tokenBudget,omitempty"`
	TokensUsed      int        `json:"tokensUsed"`
	TimeUsedSeconds int        `json:"timeUsedSeconds"`
	CreatedAt       int64      `json:"createdAt"`
	UpdatedAt       int64      `json:"updatedAt"`
}

// GoalModeState tracks the session's active/exiting goal mode.
// upstream: packages/coding-agent/src/goals/state.ts:6-11
type GoalModeState struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"` // "active" | "exiting"
	Reason  string `json:"reason,omitempty"`
	Goal    Goal   `json:"goal"`
}

// GoalToolDetails matches omp's GoalToolDetails consumed by goalToolRenderer.
// upstream: packages/tui/src/tools/goal.ts:28-33
type GoalToolDetails struct {
	Op                     string  `json:"op"`
	Goal                   *Goal   `json:"goal"`
	RemainingTokens        *int    `json:"remainingTokens"`
	CompletionBudgetReport *string `json:"completionBudgetReport"`
}

// GoalTokenUsage tracks cumulative session tokens for goal delta accounting.
type GoalTokenUsage struct {
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
}

// GoalTodoTask is a single todo item projected into goal-mode `<todo_context>`.
// upstream: packages/coding-agent/src/session/agent-session.ts:8989-9019
type GoalTodoTask struct {
	Status  string
	Content string
}

// GoalTodoPhase is a named phase of todo items projected into goal-mode `<todo_context>`.
type GoalTodoPhase struct {
	Name  string
	Tasks []GoalTodoTask
}

// TodoContextSnapshot returns the current session's persisted todo phases when
// todo tracking is active (`todo.enabled` && `todo` tool active). Wired by the
// integrator (`wiring.go`) to `internal/todo.CurrentPhases`.
var TodoContextSnapshot func(ctx sdk.Context) (active bool, phases []GoalTodoPhase)

var goalIDSeq atomic.Uint64

func defaultNowMs() int64 { return time.Now().UnixMilli() }

func nextGoalID(nowMs int64) string {
	n := goalIDSeq.Add(1)
	return fmt.Sprintf("goal-%d-%d", nowMs, n)
}

func cloneGoal(g Goal) Goal {
	cp := g
	if g.TokenBudget != nil {
		v := *g.TokenBudget
		cp.TokenBudget = &v
	}
	return cp
}

func cloneGoalState(st *GoalModeState) *GoalModeState {
	if st == nil {
		return nil
	}
	cp := *st
	cp.Goal = cloneGoal(st.Goal)
	return &cp
}

func isGoalAccountingStatus(g Goal) bool {
	return g.Status == GoalStatusActive || g.Status == GoalStatusBudgetLimited
}

// GoalTokenDelta computes billable new tokens since baseline (input + cacheWrite + output;
// cacheRead excluded, clamped at 0 across resets).
// upstream: packages/coding-agent/src/goals/runtime.ts:66-78
func GoalTokenDelta(current, baseline GoalTokenUsage) int {
	delta := 0
	if d := current.Input - baseline.Input; d > 0 {
		delta += d
	}
	if d := current.CacheWrite - baseline.CacheWrite; d > 0 {
		delta += d
	}
	if d := current.Output - baseline.Output; d > 0 {
		delta += d
	}
	return delta
}

// RemainingGoalTokens returns remaining budget tokens, or nil when unbounded.
// upstream: packages/coding-agent/src/goals/runtime.ts:57-60
func RemainingGoalTokens(g *Goal) *int {
	if g == nil || g.TokenBudget == nil {
		return nil
	}
	rem := *g.TokenBudget - g.TokensUsed
	if rem < 0 {
		rem = 0
	}
	return &rem
}

// CompletionBudgetReport formats the final budget line appended on `goal complete`.
// upstream: packages/coding-agent/src/goals/runtime.ts:96-106
func CompletionBudgetReport(g Goal) string {
	var parts []string
	if g.TokenBudget != nil {
		parts = append(parts, fmt.Sprintf("tokens used: %d of %d", g.TokensUsed, *g.TokenBudget))
	}
	if g.TimeUsedSeconds > 0 {
		parts = append(parts, fmt.Sprintf("time used: %d seconds", g.TimeUsedSeconds))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("Goal achieved. Report final budget usage to the user: %s.", strings.Join(parts, "; "))
}

// RenderGoalPrompt renders goal-mode-active.md, goal-continuation.md, or goal-budget-limit.md.
// upstream: packages/coding-agent/src/goals/runtime.ts:80-94
func RenderGoalPrompt(kind string, g Goal) string {
	var tpl string
	switch kind {
	case "active":
		tpl = goalModeActiveTemplate
	case "continuation":
		tpl = goalContinuationTemplate
	default:
		tpl = goalBudgetLimitTemplate
	}
	budgetVal := "none"
	remVal := "unbounded"
	if g.TokenBudget != nil {
		budgetVal = strconv.Itoa(*g.TokenBudget)
		rem := *g.TokenBudget - g.TokensUsed
		if rem < 0 {
			rem = 0
		}
		remVal = strconv.Itoa(rem)
	}
	return renderTemplate(tpl, templateData{
		Vars: map[string]string{
			"objective":       escapeXMLText(g.Objective),
			"tokensUsed":      strconv.Itoa(g.TokensUsed),
			"tokenBudget":     budgetVal,
			"remainingTokens": remVal,
			"timeUsedSeconds": strconv.Itoa(g.TimeUsedSeconds),
		},
	})
}

// sanitizeGoalTodoText escapes XML characters and control/newline characters
// for safe single-line interpolation inside `<todo_context>`.
// upstream: packages/coding-agent/src/session/agent-session.ts:8980-8987
func sanitizeGoalTodoText(text string) string {
	escaped := escapeXMLText(text)
	escaped = strings.ReplaceAll(escaped, "\r\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	var b strings.Builder
	b.Grow(len(escaped))
	for _, r := range escaped {
		if (r >= 0x00 && r <= 0x08) || r == 0x0b || r == 0x0c || (r >= 0x0e && r <= 0x1f) || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// RenderGoalTodoContext renders upstream `goal-todo-context.md` for non-empty
// phases, returning "" when there are no tasks to report.
// upstream: packages/coding-agent/src/session/agent-session.ts:8989-9019
func RenderGoalTodoContext(phases []GoalTodoPhase) string {
	var (
		total      int
		closed     int
		open       int
		phaseItems []templateData
	)
	for _, ph := range phases {
		if len(ph.Tasks) == 0 {
			continue
		}
		taskItems := make([]templateData, 0, len(ph.Tasks))
		for _, t := range ph.Tasks {
			total++
			if t.Status == "completed" || t.Status == "abandoned" {
				closed++
			} else {
				open++
			}
			taskItems = append(taskItems, templateData{
				Vars: map[string]string{
					"status":  sanitizeGoalTodoText(t.Status),
					"content": sanitizeGoalTodoText(t.Content),
				},
			})
		}
		phaseItems = append(phaseItems, templateData{
			Vars: map[string]string{
				"name": sanitizeGoalTodoText(ph.Name),
			},
			Items: map[string][]templateData{
				"tasks": taskItems,
			},
		})
	}
	if len(phaseItems) == 0 {
		return ""
	}
	return renderTemplate(goalTodoContextTemplate, templateData{
		Vars: map[string]string{
			"closed": strconv.Itoa(closed),
			"total":  strconv.Itoa(total),
			"open":   strconv.Itoa(open),
		},
		Items: map[string][]templateData{
			"phases": phaseItems,
		},
	})
}

// buildGoalModeContextMessage renders `goal-mode-active.md` and appends
// `<todo_context>` when `TodoContextSnapshot` reports active non-empty phases.
// upstream: packages/coding-agent/src/session/agent-session.ts:8952-9019
func buildGoalModeContextMessage(ctx sdk.Context, g Goal) string {
	content := RenderGoalPrompt("active", g)
	if TodoContextSnapshot == nil {
		return content
	}
	if !readModeSettingBool(ctx, "todo.enabled", true) {
		return content
	}
	active, phases := TodoContextSnapshot(ctx)
	if !active || len(phases) == 0 {
		return content
	}
	if todoCtx := RenderGoalTodoContext(phases); todoCtx != "" {
		return content + "\n" + todoCtx
	}
	return content
}

func renderGuidedGoalInterview(initial string) string {
	return renderTemplate(guidedGoalInterviewTemplate, templateData{
		Vars: map[string]string{
			"initial": strings.TrimSpace(initial),
		},
	})
}

func goalToolSchema() sdk.Schema {
	return sdk.Schema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"op"},
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "get", "complete", "pause", "resume", "drop"},
				"description": "goal operation",
			},
			"objective": map[string]any{
				"type":        "string",
				"description": "goal objective",
			},
			"token_budget": map[string]any{
				"type":        "integer",
				"description": "token budget",
			},
		},
	}
}

func buildGoalToolResult(op string, g *Goal, includeCompletion bool) sdk.ToolResult {
	var rem *int
	var reportPtr *string
	if g != nil {
		rem = RemainingGoalTokens(g)
		if includeCompletion && g.Status == GoalStatusComplete {
			if rep := CompletionBudgetReport(*g); rep != "" {
				reportPtr = &rep
			}
		}
	}

	var text string
	if g != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "Goal: %s\nStatus: %s\nTokens: %d used", g.Objective, g.Status, g.TokensUsed)
		if g.TokenBudget != nil {
			fmt.Fprintf(&b, " / %d budget", *g.TokenBudget)
		}
		if rem != nil {
			fmt.Fprintf(&b, "\nRemaining tokens: %d", *rem)
		}
		if reportPtr != nil {
			fmt.Fprintf(&b, "\n\n%s", *reportPtr)
		}
		text = b.String()
	} else {
		text = "No active goal."
	}

	var goalCopy *Goal
	if g != nil {
		cp := cloneGoal(*g)
		goalCopy = &cp
	}
	return sdk.ToolResult{
		Content: text,
		Details: GoalToolDetails{
			Op:                     op,
			Goal:                   goalCopy,
			RemainingTokens:        rem,
			CompletionBudgetReport: reportPtr,
		},
	}
}

func parseOptionalIntParam(params map[string]any, key string) (*int, error) {
	raw, exists := params[key]
	if !exists || raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case float64:
		if v != float64(int(v)) || v <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer when provided", key)
		}
		n := int(v)
		return &n, nil
	case int:
		if v <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer when provided", key)
		}
		return &v, nil
	case json.Number:
		n, err := v.Int64()
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer when provided", key)
		}
		i := int(n)
		return &i, nil
	default:
		return nil, fmt.Errorf("%s must be a positive integer when provided", key)
	}
}

// handleGoalTool implements omp's hidden `goal` tool.
// upstream: packages/coding-agent/src/goals/tools/goal-tool.ts:78-135
func (m *manager) handleGoalTool(ctx sdk.Context, params map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, sdk.NewToolError("Goal mode is not active.")
	}
	op, _ := params["op"].(string)

	switch op {
	case "create":
		if !isGoalEnabledSetting(ctx) {
			return nil, sdk.NewToolError("Goal mode is disabled in settings (goal.enabled).")
		}
		obj, _ := params["objective"].(string)
		obj = strings.TrimSpace(obj)
		if obj == "" {
			return nil, sdk.NewToolError("objective is required when op=create")
		}
		budget, err := parseOptionalIntParam(params, "token_budget")
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		g, err := m.goalCreate(ctx, sid, obj, budget)
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		return buildGoalToolResult(op, &g, false), nil

	case "get":
		m.mu.Lock()
		st := m.stateLocked(sid)
		var g *Goal
		if st.goalState != nil {
			cp := cloneGoal(st.goalState.Goal)
			g = &cp
		}
		m.mu.Unlock()
		return buildGoalToolResult(op, g, false), nil

	case "pause":
		g := m.goalPause(ctx, sid)
		return buildGoalToolResult(op, g, false), nil

	case "resume":
		if !isGoalEnabledSetting(ctx) {
			return nil, sdk.NewToolError("Goal mode is disabled in settings (goal.enabled).")
		}
		g, err := m.goalResume(ctx, sid)
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		return buildGoalToolResult(op, &g, false), nil

	case "drop":
		g := m.goalDrop(ctx, sid)
		return buildGoalToolResult(op, g, false), nil

	case "complete":
		g, err := m.goalCompleteFromTool(ctx, sid)
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		return buildGoalToolResult(op, &g, true), nil

	default:
		return nil, sdk.NewToolError(fmt.Sprintf("unknown goal op %q", op))
	}
}

func (m *manager) readCurrentSessionUsage(ctx sdk.Context) GoalTokenUsage {
	branch, err := ctx.GetBranch()
	if err != nil {
		return GoalTokenUsage{}
	}
	var total GoalTokenUsage
	for _, entry := range branch {
		if entry.Usage == nil {
			continue
		}
		total.Input += entry.Usage.Input
		total.Output += entry.Usage.Output
		total.CacheRead += entry.Usage.CacheRead
		total.CacheWrite += entry.Usage.CacheWrite
	}
	return total
}

func (m *manager) goalCreate(ctx sdk.Context, sid, objective string, tokenBudget *int) (Goal, error) {
	now := m.now()
	usage := m.readCurrentSessionUsage(ctx)
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.planEnabled || st.planPaused {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("Exit plan mode first.")
	}
	if st.goalState != nil && st.goalState.Goal.Status != GoalStatusDropped && st.goalState.Goal.Status != GoalStatusComplete {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("cannot create a new goal because this session already has a goal")
	}
	g := Goal{
		ID:          nextGoalID(now),
		Objective:   objective,
		Status:      GoalStatusActive,
		TokenBudget: tokenBudget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	st.goalState = &GoalModeState{Enabled: true, Mode: "active", Goal: g}
	st.goalBudgetReportedFor = ""
	st.goalWallLastMs = now
	st.goalWallActiveID = g.ID
	st.goalTurnBaseline = usage
	st.goalHasTurnBaseline = true
	st.goalSuppressNext = false
	st.goalPrevActivity = ""
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	m.ensureGoalToolActive(ctx, sid)
	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "goal",
		"data": map[string]any{"goal": g},
	})
	return g, nil
}

func (m *manager) goalReplace(ctx sdk.Context, sid, objective string, tokenBudget *int) (Goal, error) {
	now := m.now()
	usage := m.readCurrentSessionUsage(ctx)
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil || !st.goalState.Enabled || !isGoalAccountingStatus(st.goalState.Goal) {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("cannot replace goal because no goal is active")
	}
	g := Goal{
		ID:          nextGoalID(now),
		Objective:   objective,
		Status:      GoalStatusActive,
		TokenBudget: tokenBudget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	st.goalState = &GoalModeState{Enabled: true, Mode: "active", Goal: g}
	st.goalBudgetReportedFor = ""
	st.goalWallLastMs = now
	st.goalWallActiveID = g.ID
	st.goalTurnBaseline = usage
	st.goalHasTurnBaseline = true
	st.goalSuppressNext = false
	st.goalPrevActivity = ""
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "goal",
		"data": map[string]any{"goal": g},
	})
	return g, nil
}

func (m *manager) goalPause(ctx sdk.Context, sid string) *Goal {
	m.flushGoalUsage(ctx, sid, false)
	now := m.now()
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		return nil
	}
	st.goalState.Enabled = false
	st.goalState.Mode = "active"
	st.goalState.Reason = ""
	if isGoalAccountingStatus(st.goalState.Goal) {
		st.goalState.Goal.Status = GoalStatusPaused
	}
	st.goalState.Goal.UpdatedAt = now
	st.goalWallActiveID = ""
	st.goalBudgetReportedFor = ""
	cp := cloneGoal(st.goalState.Goal)
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "goal_paused",
		"data": map[string]any{"goal": cp},
	})
	return &cp
}

func (m *manager) goalResume(ctx sdk.Context, sid string) (Goal, error) {
	now := m.now()
	usage := m.readCurrentSessionUsage(ctx)
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("No paused goal.")
	}
	if st.goalState.Goal.Status == GoalStatusComplete {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("Goal is already complete.")
	}
	st.goalState.Enabled = true
	st.goalState.Mode = "active"
	st.goalState.Reason = ""
	st.goalState.Goal.Status = GoalStatusActive
	st.goalState.Goal.UpdatedAt = now
	st.goalBudgetReportedFor = ""
	st.goalWallLastMs = now
	st.goalWallActiveID = st.goalState.Goal.ID
	st.goalTurnBaseline = usage
	st.goalHasTurnBaseline = true
	st.goalSuppressNext = false
	st.goalPrevActivity = ""
	cp := cloneGoal(st.goalState.Goal)
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	m.ensureGoalToolActive(ctx, sid)
	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "goal",
		"data": map[string]any{"goal": cp},
	})
	return cp, nil
}

func (m *manager) goalDrop(ctx sdk.Context, sid string) *Goal {
	m.flushGoalUsage(ctx, sid, false)
	now := m.now()
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		return nil
	}
	dropped := cloneGoal(st.goalState.Goal)
	dropped.Status = GoalStatusDropped
	dropped.UpdatedAt = now
	st.goalState = nil
	st.goalWallActiveID = ""
	st.goalBudgetReportedFor = ""
	st.goalSuppressNext = false
	st.goalPrevActivity = ""
	st.goalPendingContTurns = 0
	addedByMode := st.goalToolAddedByMode
	st.goalToolAddedByMode = false
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	if addedByMode {
		m.removeGoalTool(ctx)
	}
	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{"mode": "none"})
	return &dropped
}

func (m *manager) goalCompleteFromTool(ctx sdk.Context, sid string) (Goal, error) {
	m.flushGoalUsage(ctx, sid, false)
	now := m.now()
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("cannot complete goal because no goal is active")
	}
	if st.goalState.Goal.Status == GoalStatusComplete {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("goal is already complete")
	}
	if st.goalState.Goal.Status == GoalStatusDropped {
		m.mu.Unlock()
		return Goal{}, fmt.Errorf("cannot complete a dropped goal")
	}
	st.goalState.Enabled = false
	st.goalState.Mode = "exiting"
	st.goalState.Reason = "completed"
	st.goalState.Goal.Status = GoalStatusComplete
	st.goalState.Goal.UpdatedAt = now
	st.goalWallActiveID = ""
	st.goalBudgetReportedFor = ""
	cp := cloneGoal(st.goalState.Goal)
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "goal",
		"data": map[string]any{"goal": cp},
	})
	return cp, nil
}

func (m *manager) goalSetBudget(ctx sdk.Context, sid string, newBudget *int) (*Goal, error) {
	if newBudget != nil && *newBudget <= 0 {
		return nil, fmt.Errorf("goal token_budget must be a positive integer when provided")
	}
	m.flushGoalUsage(ctx, sid, false)
	now := m.now()
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		return nil, nil
	}
	st.goalBudgetReportedFor = ""
	st.goalState.Goal.TokenBudget = newBudget
	st.goalState.Goal.UpdatedAt = now
	shouldSteer := false
	if newBudget != nil && st.goalState.Goal.TokensUsed >= *newBudget {
		if st.goalState.Goal.Status == GoalStatusActive {
			st.goalState.Goal.Status = GoalStatusBudgetLimited
			shouldSteer = true
		}
	} else if st.goalState.Goal.Status == GoalStatusBudgetLimited {
		st.goalState.Goal.Status = GoalStatusActive
		st.goalState.Enabled = true
		st.goalWallLastMs = now
		st.goalWallActiveID = st.goalState.Goal.ID
	}
	cp := cloneGoal(st.goalState.Goal)
	enabled := st.goalState.Enabled
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	modeVal := "goal_paused"
	if enabled {
		modeVal = "goal"
	}
	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": modeVal,
		"data": map[string]any{"goal": cp},
	})
	if shouldSteer {
		m.sendGoalBudgetLimitSteer(ctx, sid, cp)
	}
	return &cp, nil
}

// flushGoalUsage checks whether goal accounting is active BEFORE fetching the
// branch, matching omp `goals/runtime.ts:216-238` so sessions without an active
// goal never pay a branch walk on tool results or turn boundaries.
func (m *manager) flushGoalUsage(ctx sdk.Context, sid string, allowSteer bool) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	st := m.sessions[sid]
	active := st != nil && st.goalState != nil && st.goalState.Enabled && isGoalAccountingStatus(st.goalState.Goal)
	hasBaseline := active && st.goalHasTurnBaseline
	m.mu.Unlock()
	if !active {
		return
	}

	var usage GoalTokenUsage
	if hasBaseline {
		usage = m.readCurrentSessionUsage(ctx)
	}
	now := m.now()

	m.mu.Lock()
	st = m.sessions[sid]
	if st == nil || st.goalState == nil || !st.goalState.Enabled || !isGoalAccountingStatus(st.goalState.Goal) {
		m.mu.Unlock()
		return
	}
	tokenDelta := 0
	if hasBaseline && st.goalHasTurnBaseline {
		tokenDelta = GoalTokenDelta(usage, st.goalTurnBaseline)
		st.goalTurnBaseline = usage
	}
	wallSecs := 0
	if st.goalWallActiveID == st.goalState.Goal.ID && now > st.goalWallLastMs {
		wallSecs = int((now - st.goalWallLastMs) / 1000)
		if wallSecs > 0 {
			st.goalWallLastMs += int64(wallSecs) * 1000
		}
	}
	if tokenDelta <= 0 && wallSecs <= 0 {
		m.mu.Unlock()
		return
	}
	st.goalState.Goal.TokensUsed += tokenDelta
	st.goalState.Goal.TimeUsedSeconds += wallSecs
	st.goalState.Goal.UpdatedAt = now
	flipped := st.goalState.Goal.TokenBudget != nil &&
		st.goalState.Goal.TokensUsed >= *st.goalState.Goal.TokenBudget &&
		st.goalState.Goal.Status == GoalStatusActive
	if flipped {
		st.goalState.Goal.Status = GoalStatusBudgetLimited
	}
	if st.goalState.Goal.Status != GoalStatusBudgetLimited {
		st.goalBudgetReportedFor = ""
	}
	cp := cloneGoal(st.goalState.Goal)
	shouldSteer := allowSteer && flipped && st.goalBudgetReportedFor != cp.ID
	if shouldSteer {
		st.goalBudgetReportedFor = cp.ID
	}
	shouldPersist := tokenDelta > 0 || flipped
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	if shouldPersist {
		_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
			"mode": "goal",
			"data": map[string]any{"goal": cp},
		})
	}
	if shouldSteer {
		_ = ctx.SendMessage("goal-budget-limit", RenderGoalPrompt("budget-limit", cp), false, sdk.SendMessageOptions{
			DeliverAs: "steer",
		})
	}
}

func (m *manager) sendGoalBudgetLimitSteer(ctx sdk.Context, sid string, g Goal) {
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalBudgetReportedFor == g.ID {
		m.mu.Unlock()
		return
	}
	st.goalBudgetReportedFor = g.ID
	m.mu.Unlock()

	_ = ctx.SendMessage("goal-budget-limit", RenderGoalPrompt("budget-limit", g), false, sdk.SendMessageOptions{
		DeliverAs: "steer",
	})
}

func (m *manager) ensureGoalToolActive(ctx sdk.Context, sid string) {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return
	}
	if containsString(active, goalToolName) {
		return
	}
	m.mu.Lock()
	m.stateLocked(sid).goalToolAddedByMode = true
	m.mu.Unlock()
	ctx.SetActiveTools(append(active, goalToolName))
}

func (m *manager) removeGoalTool(ctx sdk.Context) {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return
	}
	if containsString(active, goalToolName) {
		ctx.SetActiveTools(removeString(active, goalToolName))
	}
}

func (m *manager) finalizeCompletedGoal(ctx sdk.Context, sid string) {
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || st.goalState == nil || st.goalState.Mode != "exiting" {
		m.mu.Unlock()
		return
	}
	completed := cloneGoal(st.goalState.Goal)
	addedByMode := st.goalToolAddedByMode
	st.goalState = nil
	st.goalToolAddedByMode = false
	st.goalSuppressNext = false
	st.goalPrevActivity = ""
	st.goalPendingContTurns = 0
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	if addedByMode {
		m.removeGoalTool(ctx)
	}
	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{"mode": "none"})
	entryData := map[string]any{
		"objective":       completed.Objective,
		"tokensUsed":      completed.TokensUsed,
		"timeUsedSeconds": completed.TimeUsedSeconds,
	}
	if completed.TokenBudget != nil {
		entryData["tokenBudget"] = *completed.TokenBudget
	}
	_ = ctx.AppendEntry(entryTypeGoalCompleted, entryData)
	notifyInfo(ctx, "Goal mode completed.")
}

// GoalContinuationActivity hashes the tool calls and tool results of the latest
// turn so two identical/empty continuation turns stop auto-continuing.
// upstream: packages/coding-agent/src/goals/state.ts:54-70
func GoalContinuationActivity(branch []sdk.BranchEntry) string {
	start := 0
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Role == "user" || branch[i].Type == "custom_message" {
			start = i + 1
			break
		}
	}
	var parts []string
	hash := func(v any) {
		b, _ := json.Marshal(v)
		sum := sha256.Sum256(b)
		parts = append(parts, fmt.Sprintf("%d:%s", len(b), hex.EncodeToString(sum[:8])))
	}
	for _, entry := range branch[start:] {
		if entry.Role == "assistant" {
			for _, tc := range entry.ToolCalls {
				hash([]any{"call", tc.Name, tc.Args})
			}
		} else if entry.Role == "toolResult" {
			hash([]any{"result", entry.ToolName, entry.Content, entry.IsError})
		}
	}
	return strings.Join(parts, ":")
}

// ParseGoalSubcommand splits `/goal <subcommand> [rest]`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:684-697
func ParseGoalSubcommand(args string) (sub, rest string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}
	verb, tail := parseSubcommand(trimmed)
	switch verb {
	case "set", "show", "pause", "resume", "drop", "budget":
		return verb, tail
	default:
		return "", trimmed
	}
}

// handleGoalCommand implements `/goal [set|show|pause|resume|drop|budget | <objective>]`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7626-7954
func (m *manager) handleGoalCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyError(ctx, "Cannot use goal mode without an active session ID.")
		return nil
	}
	if VibeActive != nil && VibeActive(ctx) {
		notifyWarn(ctx, "Exit vibe mode first.")
		return nil
	}
	if !isGoalEnabledSetting(ctx) {
		notifyWarn(ctx, "Goal mode is disabled. Enable it in settings (goal.enabled).")
		return nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.planEnabled || st.planPaused {
		paused := st.planPaused
		m.mu.Unlock()
		if paused {
			notifyWarn(ctx, "Plan mode is paused — run /plan again to fully exit.")
		} else {
			notifyWarn(ctx, "Exit plan mode first.")
		}
		return nil
	}
	goalEnabled := st.goalState != nil && st.goalState.Enabled
	goalPaused := st.goalState != nil && !st.goalState.Enabled && st.goalState.Goal.Status == GoalStatusPaused
	hasGoal := st.goalState != nil && st.goalState.Goal.Status != GoalStatusDropped && st.goalState.Goal.Status != GoalStatusComplete
	m.mu.Unlock()

	sub, rest := ParseGoalSubcommand(args)
	switch sub {
	case "set":
		return m.handleGoalSetSubcommand(ctx, sid, rest, goalEnabled, goalPaused)
	case "show":
		m.showGoalDetails(ctx, sid)
		return nil
	case "pause":
		if !goalEnabled {
			notifyWarn(ctx, "No active goal to pause.")
			return nil
		}
		m.goalPause(ctx, sid)
		notifyInfo(ctx, "Goal mode paused.")
		return nil
	case "resume":
		if !goalPaused {
			notifyWarn(ctx, "No paused goal to resume.")
			return nil
		}
		if _, err := m.goalResume(ctx, sid); err != nil {
			notifyError(ctx, err.Error())
			return nil
		}
		notifyInfo(ctx, "Goal mode resumed.")
		return nil
	case "drop":
		return m.confirmAndDropGoal(ctx, sid, hasGoal)
	case "budget":
		if !goalEnabled {
			if goalPaused {
				notifyWarn(ctx, "Resume the goal before adjusting the budget.")
			} else {
				notifyWarn(ctx, "No active goal.")
			}
			return nil
		}
		if rest == "" {
			return m.promptGoalBudgetEdit(ctx, sid)
		}
		return m.applyGoalBudgetArg(ctx, sid, rest)
	}

	if goalEnabled {
		if rest != "" {
			notifyInfo(ctx, "Goal mode is already active. Use /goal to manage it, or /goal drop to start over.")
			return nil
		}
		return m.openGoalMenu(ctx, sid, true)
	}
	if goalPaused {
		if rest != "" {
			notifyWarn(ctx, "Resume the current goal first, or drop it before setting a new objective.")
			return nil
		}
		return m.openGoalMenu(ctx, sid, false)
	}
	if rest != "" {
		return m.startGoalObjective(ctx, sid, rest)
	}
	if !ctx.HasUI() {
		notifyWarn(ctx, "Usage: /goal <objective> | set | show | pause | resume | drop | budget <N|off>")
		return nil
	}
	val, ok, err := ctx.Editor("Goal objective", "")
	if err != nil || !ok {
		return err
	}
	obj, _ := val.(string)
	obj = strings.TrimSpace(obj)
	if obj == "" {
		return nil
	}
	return m.startGoalObjective(ctx, sid, obj)
}

func (m *manager) handleGoalSetSubcommand(ctx sdk.Context, sid, rest string, goalEnabled, goalPaused bool) error {
	if !goalEnabled && goalPaused {
		notifyWarn(ctx, "Resume the current goal first, or drop it before setting a new objective.")
		return nil
	}
	obj := strings.TrimSpace(rest)
	if obj == "" {
		if !ctx.HasUI() {
			notifyWarn(ctx, "Usage: /goal set <objective>")
			return nil
		}
		val, ok, err := ctx.Editor("Goal objective", "")
		if err != nil || !ok {
			return err
		}
		obj, _ = val.(string)
		obj = strings.TrimSpace(obj)
		if obj == "" {
			return nil
		}
	}
	if goalEnabled {
		if _, err := m.goalReplace(ctx, sid, obj, nil); err != nil {
			notifyError(ctx, err.Error())
			return nil
		}
		notifyInfo(ctx, "Goal updated.")
		return sendPromptOrSteer(ctx, obj)
	}
	return m.startGoalObjective(ctx, sid, obj)
}

func (m *manager) startGoalObjective(ctx sdk.Context, sid, objective string) error {
	if _, err := m.goalCreate(ctx, sid, objective, nil); err != nil {
		notifyError(ctx, err.Error())
		return nil
	}
	notifyInfo(ctx, "Goal mode enabled.")
	return sendPromptOrSteer(ctx, objective)
}

func (m *manager) openGoalMenu(ctx sdk.Context, sid string, active bool) error {
	if !ctx.HasUI() {
		m.showGoalDetails(ctx, sid)
		return nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		return nil
	}
	g := cloneGoal(st.goalState.Goal)
	m.mu.Unlock()

	summary := g.Objective
	if len([]rune(summary)) > 48 {
		summary = string([]rune(summary)[:47]) + "…"
	}
	title := fmt.Sprintf("Goal: %s (%s)", summary, g.Status)
	items := []string{"Show details", "Adjust budget…", "Pause", "Drop"}
	if !active {
		title = fmt.Sprintf("Goal paused: %s", summary)
		items = []string{"Resume", "Show details", "Adjust budget…", "Drop"}
	}
	choiceVal, ok, err := ctx.Select(title, items)
	if err != nil || !ok {
		return err
	}
	switch choice, _ := choiceVal.(string); choice {
	case "Show details":
		m.showGoalDetails(ctx, sid)
	case "Adjust budget…":
		return m.promptGoalBudgetEdit(ctx, sid)
	case "Pause":
		m.goalPause(ctx, sid)
		notifyInfo(ctx, "Goal mode paused.")
	case "Resume":
		if _, err := m.goalResume(ctx, sid); err != nil {
			notifyError(ctx, err.Error())
			return nil
		}
		notifyInfo(ctx, "Goal mode resumed.")
	case "Drop":
		return m.confirmAndDropGoal(ctx, sid, true)
	}
	return nil
}

func (m *manager) showGoalDetails(ctx sdk.Context, sid string) {
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState == nil {
		m.mu.Unlock()
		notifyInfo(ctx, "No goal set.")
		return
	}
	g := cloneGoal(st.goalState.Goal)
	enabled := st.goalState.Enabled
	m.mu.Unlock()

	used := formatCommaInt(g.TokensUsed)
	budgetLine := used + " (no budget)"
	if g.TokenBudget != nil {
		left := *g.TokenBudget - g.TokensUsed
		if left < 0 {
			left = 0
		}
		budgetLine = fmt.Sprintf("%s / %s (%s left)", used, formatCommaInt(*g.TokenBudget), formatCommaInt(left))
	}
	pausedSuffix := ""
	if !enabled {
		pausedSuffix = " (paused)"
	}
	lines := []string{
		fmt.Sprintf("Objective: %s", g.Objective),
		fmt.Sprintf("Status: %s%s", g.Status, pausedSuffix),
		fmt.Sprintf("Tokens: %s", budgetLine),
		fmt.Sprintf("Time spent: %s", formatCoarseDurationMs(int64(g.TimeUsedSeconds)*1000)),
	}
	notifyInfo(ctx, strings.Join(lines, "\n"))
}

func (m *manager) promptGoalBudgetEdit(ctx sdk.Context, sid string) error {
	if !ctx.HasUI() {
		notifyWarn(ctx, "Usage: /goal budget <N|off>")
		return nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	prefill := ""
	if st.goalState != nil && st.goalState.Goal.TokenBudget != nil {
		prefill = strconv.Itoa(*st.goalState.Goal.TokenBudget)
	}
	m.mu.Unlock()

	val, ok, err := ctx.Input("Goal budget (number, `off`, or empty to cancel)", prefill)
	if err != nil || !ok {
		return err
	}
	raw, _ := val.(string)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return m.applyGoalBudgetArg(ctx, sid, raw)
}

func (m *manager) applyGoalBudgetArg(ctx sdk.Context, sid, raw string) error {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	var next *int
	if trimmed != "off" {
		n, err := strconv.Atoi(trimmed)
		if err != nil || n <= 0 {
			notifyError(ctx, "Goal budget must be a positive integer or `off`.")
			return nil
		}
		next = &n
	}
	if _, err := m.goalSetBudget(ctx, sid, next); err != nil {
		notifyError(ctx, err.Error())
		return nil
	}
	if next == nil {
		notifyInfo(ctx, "Goal budget cleared.")
	} else {
		notifyInfo(ctx, fmt.Sprintf("Goal budget set to %d.", *next))
	}
	return nil
}

func (m *manager) confirmAndDropGoal(ctx sdk.Context, sid string, hasGoal bool) error {
	if !hasGoal {
		notifyWarn(ctx, "No goal to drop.")
		return nil
	}
	if ctx.HasUI() {
		confirmed, err := ctx.Confirm("Drop goal?", "This removes the goal record. Accumulated usage stays in the session log.")
		if err != nil {
			return err
		}
		if b, ok := confirmed.(bool); !ok || !b {
			return nil
		}
	}
	m.goalDrop(ctx, sid)
	notifyInfo(ctx, "Goal dropped.")
	return nil
}

// handleGuidedGoalCommand implements `/guided-goal [rough objective]`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7670-7731
func (m *manager) handleGuidedGoalCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyError(ctx, "Cannot use guided-goal mode without an active session ID.")
		return nil
	}
	if VibeActive != nil && VibeActive(ctx) {
		notifyWarn(ctx, "Exit vibe mode first.")
		return nil
	}
	if !isGoalEnabledSetting(ctx) {
		notifyWarn(ctx, "Goal mode is disabled. Enable it in settings (goal.enabled).")
		return nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.planEnabled || st.planPaused {
		paused := st.planPaused
		m.mu.Unlock()
		if paused {
			notifyWarn(ctx, "Plan mode is paused — run /plan again to fully exit.")
		} else {
			notifyWarn(ctx, "Exit plan mode first.")
		}
		return nil
	}
	if st.goalState != nil && st.goalState.Enabled {
		m.mu.Unlock()
		notifyInfo(ctx, "Goal mode is already active. Use /goal to manage it, or /goal drop to start over.")
		return nil
	}
	if st.goalState != nil && st.goalState.Goal.Status == GoalStatusPaused {
		m.mu.Unlock()
		notifyWarn(ctx, "Resume the current goal first, or drop it before setting a new objective.")
		return nil
	}
	m.mu.Unlock()

	m.ensureGoalToolActive(ctx, sid)
	kickoff := renderGuidedGoalInterview(args)
	return ctx.SendMessage("guided-goal-interview", kickoff, false, sdk.SendMessageOptions{
		TriggerTurn: sdk.Bool(true),
		DeliverAs:   "followUp",
	})
}

func formatCoarseDurationMs(ms int64) string {
	secs := (ms + 500) / 1000
	if secs < 0 {
		secs = 0
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := (secs + 30) / 60
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	hours := (mins + 30) / 60
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	days := (hours + 12) / 24
	return fmt.Sprintf("%dd", days)
}
