package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Port of src/runs/shared/subagent-control.ts (the activity states, control events and their notices) and of
// nextLongRunningTrigger of src/runs/shared/long-running-guard.ts. The resolved control config is workflow_launch.go's
// wfControlConfig (resolveControlConfig).

// controlEvent is the original's ControlEvent: a numeric or string field that is unset is its zero value or nil.
type controlEvent struct {
	Type                  string            `json:"type"`
	From                  string            `json:"from,omitempty"`
	To                    string            `json:"to"`
	TS                    int64             `json:"ts"`
	RunID                 string            `json:"runId"`
	Agent                 string            `json:"agent"`
	Index                 *int              `json:"index,omitempty"`
	NestedRunID           string            `json:"nestedRunId,omitempty"`
	NestingPath           []nestedPathEntry `json:"nestingPath,omitempty"`
	Message               string            `json:"message"`
	Reason                string            `json:"reason,omitempty"`
	Turns                 *int64            `json:"turns,omitempty"`
	Tokens                *int64            `json:"tokens,omitempty"`
	ToolCount             *int64            `json:"toolCount,omitempty"`
	CurrentTool           string            `json:"currentTool,omitempty"`
	ToolCallID            string            `json:"toolCallId,omitempty"`
	CurrentToolDurationMs *int64            `json:"currentToolDurationMs,omitempty"`
	CurrentPath           string            `json:"currentPath,omitempty"`
	ElapsedMs             *int64            `json:"elapsedMs,omitempty"`
	RecentFailureSummary  string            `json:"recentFailureSummary,omitempty"`
	WorkflowKey           string            `json:"workflowKey,omitempty"`
	Phase                 string            `json:"phase,omitempty"`
	Label                 string            `json:"label,omitempty"`
	TaskPreview           string            `json:"taskPreview,omitempty"`
}

var controlEventTypes = []string{"active_long_running", "needs_attention"}

func resolvedBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func floatOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

// scaledNeedsAttentionAfterMs scales the default idle threshold by thinking level; an explicit threshold (or a config recovered
// without the explicitness flag) is fixed.
func scaledNeedsAttentionAfterMs(c wfControlConfig, thinking string) float64 {
	needs := floatOr(c.NeedsAttentionAfterMs, 60000)
	if c.NeedsAttentionAfterMsIsExplicit == nil || *c.NeedsAttentionAfterMsIsExplicit {
		return needs
	}
	switch thinking {
	case "medium":
		return needs * 2
	case "high":
		return needs * 5
	case "xhigh", "max":
		return needs * 10
	}
	return needs
}

// activityInput is the input of deriveActivityState; LastActivityAt nil falls back to StartedAt, Now 0 to the clock.
type activityInput struct {
	Config         wfControlConfig
	StartedAt      int64
	LastActivityAt *int64
	TurnCount      *int
	CurrentTool    string
	Thinking       string
	Now            int64
}

// deriveActivityState is "needs_attention" once a run with turns and no open tool was idle past the threshold; "" otherwise.
func deriveActivityState(in activityInput) string {
	if !resolvedBool(in.Config.Enabled, true) || in.CurrentTool != "" || (in.TurnCount == nil || *in.TurnCount == 0) {
		return ""
	}
	now := in.Now
	if now == 0 {
		now = nowMs()
	}
	last := in.StartedAt
	if in.LastActivityAt != nil {
		last = *in.LastActivityAt
	}
	age := math.Max(0, float64(now-last))
	if age > scaledNeedsAttentionAfterMs(in.Config, in.Thinking) {
		return "needs_attention"
	}
	return ""
}

// shouldEmitOpenToolAttention is true once a non-exempt tool has been open for the active notice threshold.
func shouldEmitOpenToolAttention(c wfControlConfig, currentTool string, currentToolStartedAt *int64, now int64) bool {
	if !resolvedBool(c.Enabled, true) || currentTool == "" || currentToolStartedAt == nil {
		return false
	}
	if isToolTimeoutExempt(currentTool) {
		return false
	}
	if now == 0 {
		now = nowMs()
	}
	return math.Max(0, float64(now-*currentToolStartedAt)) >= floatOr(c.ActiveNoticeAfterMs, 240000)
}

// controlEventInput is the input of buildControlEvent (the original's object parameter).
type controlEventInput struct {
	Type                  string
	From                  string
	To                    string
	RunID                 string
	Agent                 string
	Index                 *int
	TS                    int64
	LastActivityAt        *int64
	Message               string
	Reason                string
	Turns                 *int64
	Tokens                *int64
	ToolCount             *int64
	CurrentTool           string
	ToolCallID            string
	CurrentToolDurationMs *int64
	CurrentPath           string
	ElapsedMs             *int64
	RecentFailureSummary  string
	WorkflowKey           string
	Phase                 string
	Label                 string
	TaskPreview           string
}

func buildControlEvent(in controlEventInput) controlEvent {
	ts := in.TS
	if ts == 0 {
		ts = nowMs()
	}
	typ := in.Type
	if typ == "" {
		if in.To == "active_long_running" {
			typ = "active_long_running"
		} else {
			typ = "needs_attention"
		}
	}
	elapsed := in.ElapsedMs
	if elapsed == nil && in.LastActivityAt != nil && *in.LastActivityAt != 0 {
		elapsed = new(max(0, ts-*in.LastActivityAt))
	}
	message := in.Message
	if message == "" {
		switch {
		case typ == "active_long_running":
			message = in.Agent + " is still active but long-running"
		case elapsed != nil:
			message = fmt.Sprintf("%s needs attention (no observed activity for %ds)", in.Agent, *elapsed/1000)
		default:
			message = in.Agent + " needs attention"
		}
	}
	reason := in.Reason
	if reason == "" {
		if typ == "active_long_running" {
			reason = "active_long_running"
		} else {
			reason = "idle"
		}
	}
	e := controlEvent{Type: typ, From: in.From, To: in.To, TS: ts, RunID: in.RunID, Agent: in.Agent, Index: in.Index, Message: message, Reason: reason,
		Turns: in.Turns, Tokens: in.Tokens, ToolCount: in.ToolCount, CurrentTool: in.CurrentTool, ToolCallID: in.ToolCallID,
		CurrentToolDurationMs: in.CurrentToolDurationMs, CurrentPath: in.CurrentPath, ElapsedMs: elapsed,
		RecentFailureSummary: in.RecentFailureSummary, WorkflowKey: in.WorkflowKey, Phase: in.Phase, Label: in.Label}
	if in.TaskPreview != "" {
		e.TaskPreview = previewDisplayText(in.TaskPreview, 160)
	}
	return e
}

func shouldNotifyControlEvent(c wfControlConfig, e controlEvent) bool {
	if !resolvedBool(c.Enabled, true) || c.NotifyOn == nil {
		return false
	}
	for _, t := range *c.NotifyOn {
		if t == e.Type {
			return true
		}
	}
	return false
}

// controlNotificationKey identifies a notice: the child, the attention type and reason, and a hash of the nudge it carries.
func controlNotificationKey(e controlEvent, childIntercomTarget string) string {
	child := childIntercomTarget
	if child == "" {
		if e.Index != nil {
			child = e.RunID + ":" + strconv.Itoa(*e.Index)
		} else {
			child = e.RunID
		}
	}
	sum := sha256.Sum256([]byte(formatControlNudge(e)))
	reason := e.Reason
	if reason == "" {
		reason = "idle"
	}
	return child + ":" + e.Type + ":" + reason + ":" + hex.EncodeToString(sum[:])[:8]
}

// claimControlNotification is true once per notice key (and only for an event the config notifies on).
func claimControlNotification(c wfControlConfig, e controlEvent, seen map[string]bool, childIntercomTarget string) bool {
	if !shouldNotifyControlEvent(c, e) {
		return false
	}
	key := controlNotificationKey(e, childIntercomTarget)
	if seen[key] {
		return false
	}
	seen[key] = true
	return true
}

func formatLongRunningFacts(e controlEvent) string {
	var facts []string
	if e.ElapsedMs != nil {
		facts = append(facts, fmt.Sprintf("elapsed %ds", max(0, *e.ElapsedMs)/1000))
	}
	if e.Turns != nil {
		facts = append(facts, fmt.Sprintf("%d turns", *e.Turns))
	}
	if e.Tokens != nil {
		facts = append(facts, fmt.Sprintf("%d tokens", *e.Tokens))
	}
	if e.ToolCount != nil {
		facts = append(facts, fmt.Sprintf("%d tools", *e.ToolCount))
	}
	if e.CurrentTool != "" {
		tool := "tool " + e.CurrentTool
		if e.CurrentToolDurationMs != nil {
			tool += fmt.Sprintf(" %ds", max(0, *e.CurrentToolDurationMs)/1000)
		}
		facts = append(facts, tool)
	}
	if e.CurrentPath != "" {
		facts = append(facts, "path "+e.CurrentPath)
	}
	return strings.Join(facts, " | ")
}

// formatControlNudge is the bounded steering text a control notice suggests.
func formatControlNudge(e controlEvent) string {
	scope := firstNonEmpty(e.Label, e.Phase, e.WorkflowKey, e.TaskPreview)
	forScope := ""
	if scope != "" {
		forScope = " for " + scope
	}
	if e.RecentFailureSummary != "" {
		return previewDisplayText("Resolve the recent failure"+forScope+": "+e.RecentFailureSummary+". Report the smallest next step or ask for a decision.", 160)
	}
	if e.CurrentTool != "" || e.CurrentPath != "" {
		var parts []string
		if e.CurrentTool != "" {
			parts = append(parts, "tool "+e.CurrentTool)
		}
		if e.CurrentPath != "" {
			parts = append(parts, "path "+e.CurrentPath)
		}
		return previewDisplayText("Check "+strings.Join(parts, " at ")+forScope+". Report the smallest next step or ask for a decision.", 160)
	}
	if scope != "" {
		return previewDisplayText("Continue "+scope+". Report the smallest next step or ask for a decision.", 160)
	}
	return "What are you blocked on? Reply with the smallest next step or ask for a decision."
}

func jsonQuote(s string) string {
	b, _ := marshalNoEscape(s)
	return b
}

// marshalNoEscape is JSON.stringify of a string.
func marshalNoEscape(v any) (string, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

func formatControlNoticeMessage(e controlEvent, childIntercomTarget string) string {
	runTarget := e.RunID
	nudge := jsonQuote(formatControlNudge(e))
	idx := ""
	runStep := ""
	if e.Index != nil {
		idx = fmt.Sprintf("index: %d, ", *e.Index)
		runStep = fmt.Sprintf(" step %d", *e.Index+1)
	}
	steer := fmt.Sprintf(`subagent({ action: "steer", id: "%s", %smessage: %s })`, runTarget, idx, nudge)
	nestedResume := fmt.Sprintf(`subagent({ action: "resume", id: "%s", message: %s })`, runTarget, nudge)
	facts := formatLongRunningFacts(e)
	var lines []string
	add := func(l string) {
		if l != "" {
			lines = append(lines, l)
		}
	}
	tail := func() {
		if childIntercomTarget != "" {
			add("Direct intercom target: " + childIntercomTarget)
		}
		add(fmt.Sprintf(`Status: subagent({ action: "status", id: "%s" })`, runTarget))
		add(fmt.Sprintf(`Interrupt: subagent({ action: "interrupt", id: "%s" })`, runTarget))
	}
	if e.Type == "active_long_running" {
		add("Subagent active but long-running: " + e.Agent)
		add("Run: " + runTarget + runStep)
		add("Signal: " + e.Message)
		if facts != "" {
			add("Facts: " + facts)
		}
		add("Hint: Inspect status first. Use steer for a top-level live async child, routed resume for a live nested child, or resume to revive a paused/completed/failed child.")
		add("Top-level live async nudge: " + steer)
		add("Routed live nested nudge: " + nestedResume)
		tail()
		return strings.Join(lines, "\n")
	}
	add("Subagent needs attention: " + e.Agent)
	add("Run: " + runTarget + runStep)
	add("Signal: " + e.Message)
	if facts != "" {
		add("Facts: " + facts)
	}
	if e.RecentFailureSummary != "" {
		add("Recent failures: " + e.RecentFailureSummary)
	}
	if e.Reason == "supervisor_request" {
		add("Supervisor request: reply to the pending request. If subagent_supervisor pending is empty, check intercom pending because an external intercom tool may own the request.")
	}
	add("Hint: Inspect status first unless the run is clearly blocked. Use steer for a top-level live async child, routed resume for a live nested child, or resume to revive a paused/completed/failed child.")
	add("Top-level live async nudge: " + steer)
	add("Routed live nested nudge: " + nestedResume)
	tail()
	return strings.Join(lines, "\n")
}

func formatControlIntercomMessage(e controlEvent, childIntercomTarget string) string {
	label, body := "subagent needs attention", e.Agent+" needs attention in run "+e.RunID+"."
	if e.Type == "active_long_running" {
		label, body = "subagent active but long-running", e.Agent+" is still active but long-running in run "+e.RunID+"."
	}
	return strings.Join([]string{label, "", body, "", formatControlNoticeMessage(e, childIntercomTarget)}, "\n")
}

// longRunningMetrics is the input of nextLongRunningTrigger.
type longRunningMetrics struct{ StartedAt, Now, Turns, Tokens int64 }

// nextLongRunningTrigger is the reason an active run earns a long-running notice: its elapsed time first, then the opt-in turn and
// token thresholds; "" for none.
func nextLongRunningTrigger(c wfControlConfig, m longRunningMetrics) string {
	if float64(m.Now-m.StartedAt) >= floatOr(c.ActiveNoticeAfterMs, 240000) {
		return "time_threshold"
	}
	if c.ActiveNoticeAfterTurns != nil && float64(m.Turns) >= *c.ActiveNoticeAfterTurns {
		return "turn_threshold"
	}
	if c.ActiveNoticeAfterTokens != nil && float64(m.Tokens) >= *c.ActiveNoticeAfterTokens {
		return "token_threshold"
	}
	return ""
}
