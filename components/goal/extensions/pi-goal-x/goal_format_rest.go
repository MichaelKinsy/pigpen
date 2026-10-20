package pi_goal_x

import (
	"math"
	"regexp"
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// ported from goal-format.ts: everything not in format.go (summaries) and goal_format_network.go (error classification).

const (
	// STATE_ENTRY / FOCUS_ENTRY are stateEntryType / focusEntryType in core.go.
	goalEventEntry = "pi-goal-event"
	goalAuditEntry = "pi-goal-audit-event"
	completeStatus = "complete"
)

// goalProgressToolSet: tools that count as "real work" toward the active goal (GOAL_PROGRESS_TOOL_SET). A non-tool-use turn
// that ends without any of these does not queue the next autoContinue.
var goalProgressToolSet = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range goalProgressToolNames {
		m[n] = true
	}
	return m
}()

func usageLines(goal *jsObject) []string {
	tokens, seconds := usageOf(goal)
	return []string{
		"Time spent: " + formatDuration(seconds),
		"Tokens used: " + formatTokenValue(tokens),
	}
}

// goalEventDetails mirrors GoalEventDetails; absent optional fields are the zero value with their has* flag false.
type goalEventDetails struct {
	kind         string // "checkpoint" | "stale"
	goalId       string
	status       string // "" = undefined
	objective    string
	hasObjective bool
	timestamp    float64
	hasTimestamp bool
	// currentGoalId: undefined (hasCurrentGoalId false), null (hasCurrentGoalId true, currentGoalIdNull) or a string.
	currentGoalId     string
	hasCurrentGoalId  bool
	currentGoalIdNull bool
	// currentStatus: "" undefined; currentStatusNull = JS null.
	currentStatus     string
	currentStatusNull bool
	focus             string // "" | "goal" | "sisyphus"
}

func goalStatusValue(v any) string {
	if s, ok := v.(string); ok && (s == "active" || s == "paused" || s == "complete") {
		return s
	}
	return ""
}

func normalizeGoalEventDetails(value any) goalEventDetails {
	raw := asRecord(value)
	var d goalEventDetails
	get := func(k string) any {
		if raw == nil {
			return nil
		}
		return raw.vals[k]
	}
	// "checkpoint" is the only kind writers emit today; "stale" remains a legacy read.
	d.kind = "checkpoint"
	if get("kind") == any("stale") {
		d.kind = "stale"
	}
	d.goalId = "unknown"
	if s, ok := get("goalId").(string); ok {
		d.goalId = s
	}
	switch get("focus") {
	case any("sisyphus"):
		d.focus = "sisyphus"
	case any("goal"):
		d.focus = "goal"
	}
	d.status = goalStatusValue(get("status"))
	if raw != nil {
		if _, isNull := raw.vals["currentStatus"]; isNull && raw.vals["currentStatus"] == nil {
			d.currentStatusNull = true
		}
	}
	d.currentStatus = goalStatusValue(get("currentStatus"))
	if s, ok := get("objective").(string); ok {
		d.objective, d.hasObjective = s, true
	}
	if f, ok := get("timestamp").(float64); ok {
		d.timestamp, d.hasTimestamp = f, true
	}
	if raw != nil {
		if v, present := raw.vals["currentGoalId"]; present {
			if s, ok := v.(string); ok {
				d.currentGoalId, d.hasCurrentGoalId = s, true
			} else if v == nil {
				d.hasCurrentGoalId, d.currentGoalIdNull = true, true
			}
		}
	}
	return d
}

// GoalAuditEventDetails mirrors the TS interface.
type GoalAuditEventDetails struct {
	Phase   string // "started" | "approved" | "rejected" | "skipped"
	GoalId  string
	Auditor string
}

// renderGoalEvent renders a goal checkpoint message; details is the message's details value.
func renderGoalEvent(details any, expanded bool, theme goalTheme) *tui.Text {
	d := normalizeGoalEventDetails(details)
	label := "checkpoint"
	if d.kind == "stale" {
		label = "stale checkpoint"
	}
	if !expanded {
		return tui.NewText(theme.fg("customMessageLabel", "Goal ")+theme.fg("customMessageText", label), 0, 0, nil)
	}
	status := "unknown"
	if d.status == "active" {
		status = "running"
	} else if d.status != "" {
		status = d.status
	}
	lines := []string{"Status: " + status}
	if d.hasObjective && d.objective != "" {
		lines = append(lines, "Objective: "+d.objective)
	}
	lines = append(lines, "Goal id: "+d.goalId)
	if (d.hasCurrentGoalId && !d.currentGoalIdNull && d.currentGoalId != "") || d.currentStatus != "" {
		current := "none"
		if d.hasCurrentGoalId && !d.currentGoalIdNull { // `currentGoalId ?? "none"`: an empty id is kept
			current = d.currentGoalId
		}
		if d.currentStatus != "" {
			current += " (" + d.currentStatus + ")"
		}
		lines = append(lines, "Current: "+current)
	}
	return tui.NewText(
		theme.fg("customMessageLabel", "Goal "+label)+"\n"+theme.fg("customMessageText", strings.Join(lines, "\n")), 0, 0, nil)
}

// renderGoalAuditEvent renders a goal audit message; content is the message content (a string or anything else), phase
// the details.phase ("" = undefined).
func renderGoalAuditEvent(content any, phase string, theme goalTheme) *tui.Text {
	label := "started"
	switch phase {
	case "approved", "rejected", "skipped":
		label = phase
	}
	text, ok := content.(string)
	if !ok {
		text = "Goal audit " + label + "."
	}
	return tui.NewText(theme.fg("customMessageLabel", "Goal audit "+label)+"\n"+theme.fg("customMessageText", text), 0, 0, nil)
}

var (
	// `[^"]` and `[^>]` also match line terminators, as in JavaScript.
	v2SelfClosingContinuationRE = regexp.MustCompile(`^<pi_goal_continuation` + jsS + `+goal_id="([^"]+)"[^>]*v="2"` + jsS + `*/?>`)
	legacyContinuationRE        = regexp.MustCompile(`^<pi_goal_continuation` + jsS + `+goal_id="([^"]+)"`)
	legacyBracketCheckpointRE   = regexp.MustCompile(`^\[(?:GOAL CHECKPOINT|GOAL CONTINUATION|GOAL STALE) goalId=([^\]]+)\]`)
)

// Issue #30 v2 markers are self-closing and carry an explicit version: `<pi_goal_continuation goal_id="..." kind="checkpoint" v="2"/>`.
func matchV2SelfClosingContinuation(text string) (string, bool) {
	if m := v2SelfClosingContinuationRE.FindStringSubmatch(text); m != nil {
		return m[1], true
	}
	return "", false
}

func matchLegacyContinuation(text string) (string, bool) {
	if m := legacyContinuationRE.FindStringSubmatch(text); m != nil {
		return m[1], true
	}
	return "", false
}

func matchLegacyBracketCheckpoint(text string) (string, bool) {
	// `[^\]\s]+` with JavaScript whitespace: a captured space means the JS match fails.
	if m := legacyBracketCheckpointRE.FindStringSubmatch(text); m != nil && !strings.ContainsFunc(m[1], isJSSpace) {
		return m[1], true
	}
	return "", false
}

func extractGoalIdFromInjectedMessage(text string) (string, bool) {
	if id, ok := matchV2SelfClosingContinuation(text); ok {
		return id, true
	}
	if id, ok := matchLegacyContinuation(text); ok {
		return id, true
	}
	return matchLegacyBracketCheckpoint(text)
}

// goalEventMessageId is goalEventMessageId(message): the goal id of a pi-goal-event custom message, from details.goalId or
// the injected content marker.
func goalEventMessageId(message any) (string, bool) {
	if custom, _ := sessionMessageField(message, "customType"); custom != any(goalEventEntry) {
		return "", false
	}
	details, _ := sessionMessageField(message, "details")
	if d := asRecord(details); d != nil {
		if id, ok := d.str("goalId"); ok && id != "" {
			return id, true
		}
	}
	if content, _ := sessionMessageField(message, "content"); content != nil {
		if s, ok := content.(string); ok {
			return extractGoalIdFromInjectedMessage(s)
		}
	}
	return "", false
}

func assistantStopReason(message any, reason string) bool {
	raw := asRecord(message)
	if raw == nil {
		return false
	}
	role, _ := raw.str("role")
	stop, _ := raw.str("stopReason")
	return role == "assistant" && stop == reason
}

func isAbortedAssistantMessage(message any) bool { return assistantStopReason(message, "aborted") }

func isToolUseAssistantMessage(message any) bool { return assistantStopReason(message, "toolUse") }

func hasAbortedAssistantMessage(messages []any) bool {
	for _, m := range messages {
		if isAbortedAssistantMessage(m) {
			return true
		}
	}
	return false
}

func hasErrorAssistantMessage(messages []any) bool {
	for _, m := range messages {
		if isErrorAssistantMessage(m) {
			return true
		}
	}
	return false
}

// usageChannelTokens: a finite number truncated toward zero and floored at 0; anything else is 0.
func usageChannelTokens(value any) float64 {
	f, ok := value.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Max(0, math.Trunc(f))
}

func assistantTurnTokens(message any) float64 {
	raw := asRecord(message)
	if raw == nil {
		return 0
	}
	if role, _ := raw.str("role"); role != "assistant" {
		return 0
	}
	usage := asRecord(raw.vals["usage"])
	if usage == nil {
		return 0
	}
	return usageChannelTokens(usage.vals["input"]) + usageChannelTokens(usage.vals["output"])
}

var leadingEchoRE = regexp.MustCompile(`^` + jsS + `*echo\b`)

func isMeaningfulProgressToolCall(toolName string, args any) bool {
	if !goalProgressToolSet[toolName] {
		return false
	}
	if toolName == "read" {
		if rec := asRecord(args); rec != nil {
			if path, ok := rec.str("path"); ok && (path == ".pi/goals" || strings.HasPrefix(path, ".pi/goals/")) {
				return false
			}
		}
	}
	if toolName == "bash" {
		if rec := asRecord(args); rec != nil {
			if command, ok := rec.str("command"); ok && leadingEchoRE.MatchString(command) {
				return false
			}
		}
	}
	return true
}

// buildGoalTaskDetailBlock is F1: counts, next pending with contracts, recent completions with evidence.
func buildGoalTaskDetailBlock(goal *jsObject) string {
	taskList := goal.obj("taskList")
	if taskList == nil {
		return ""
	}
	tasks, _ := taskList.vals["tasks"].([]any)
	if len(tasks) == 0 {
		return ""
	}
	counts := countTaskSubtree(tasks)
	header := jsNumber(float64(counts.complete)) + "/" + jsNumber(float64(counts.total)) + " tasks complete"
	if counts.skipped > 0 {
		header += " (" + jsNumber(float64(counts.skipped)) + " skipped)"
	}
	type pendingEntry struct {
		task  *jsObject
		depth int
	}
	var pending []pendingEntry
	var recent []*jsObject
	var walk func(list []any, depth int)
	walk = func(list []any, depth int) {
		for _, x := range list {
			t := asRecord(x)
			if t == nil {
				continue
			}
			status := gstr(t, "status")
			if status == "pending" {
				pending = append(pending, pendingEntry{t, depth})
			}
			if status == "complete" && len(recent) < 2 {
				recent = append(recent, t)
			}
			if subs, ok := t.vals["subtasks"].([]any); ok && len(subs) > 0 {
				walk(subs, depth+1)
			}
		}
	}
	walk(tasks, 0)
	lines := []string{"Tasks: " + header}
	for i, entry := range pending {
		if i >= 3 {
			break
		}
		lines = append(lines, strings.Repeat("  ", entry.depth)+"[ ] "+gstr(entry.task, "id")+": "+gstr(entry.task, "title"))
		if c := gstr(entry.task, "verificationContract"); c != "" {
			lines = append(lines, strings.Repeat("  ", entry.depth+1)+"contract: "+c)
		}
	}
	if len(pending) > 3 {
		lines = append(lines, "(+"+jsNumber(float64(len(pending)-3))+" more pending — expand the dashboard with Ctrl+Shift+T)")
	}
	for _, t := range recent {
		line := "[x] " + gstr(t, "id") + ": " + gstr(t, "title")
		if ev := gstr(t, "evidence"); ev != "" {
			line += " — " + ev
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
