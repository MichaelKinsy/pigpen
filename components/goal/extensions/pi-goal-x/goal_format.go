package pi_goal_x

import (
	"strings"

	pitui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// The goal-format.ts exports the widget twins need (goal-enhancements): goalDetails, renderGoalResult and buildGoalHistoryBlock.

// goalDetails is `{ version: 3, goal: goal ? cloneGoal(goal) : null, ...(resultDetail ? { resultDetail } : {}) }`.
func goalDetails(goal *jsObject, resultDetail string) *jsObject {
	e := newObject()
	e.set("version", 3.0)
	if goal != nil {
		e.set("goal", cloneGoal(goal))
	} else {
		e.set("goal", nil)
	}
	if resultDetail != "" {
		e.set("resultDetail", resultDetail)
	}
	return e
}

// goalToolContent is one `{ type, text? }` item of a tool result; hasText is false for an item without a string text.
type goalToolContent struct {
	typ     string
	text    string
	hasText bool
}

// goalToolResult is the `result` argument of renderGoalResult; details is nil when undefined.
type goalToolResult struct {
	details *jsObject
	content []goalToolContent
}

// renderGoalResult renders a goal tool result; expanded is options?.expanded.
func renderGoalResult(result goalToolResult, expanded bool, theme goalTheme) *pitui.Text {
	firstText := ""
	for _, item := range result.content {
		if item.typ == "text" && item.hasText {
			firstText = item.text
			break
		}
	}
	details := result.details
	if details != nil {
		if rd, _ := details.str("resultDetail"); rd != "" && expanded {
			// E7: the collapsed heading stays byte-identical; the full reason is one keystroke away in the expanded detail view.
			return pitui.NewText(firstText+"\n"+rd, 0, 0, nil)
		}
	}
	if details == nil || !details.has("goal") {
		return pitui.NewText(firstText, 0, 0, nil)
	}
	for _, p := range []string{"Goal audit ", "Goal completion rejected", "Goal complete.", "Goal paused.", "Goal aborted.", "Goal confirmed and created."} {
		if strings.HasPrefix(firstText, p) {
			return pitui.NewText(firstText, 0, 0, nil)
		}
	}
	return pitui.NewText(theme.fg("accent", "Goal ")+theme.fg("muted", oneLineSummary(details.obj("goal"))), 0, 0, nil)
}

// buildGoalHistoryBlock is the E1 history block: the last audit verdict and the recent lifecycle events.
func buildGoalHistoryBlock(goal *jsObject, ledgerEvents []*jsObject) string {
	if goal == nil {
		return ""
	}
	lines := []string{}
	if audit := latestAuditorResultForGoal(ledgerEvents, gstr(goal, "id")); audit != nil {
		lines = append(lines, "Last audit: "+gstr(audit, "verdict")+" ("+utf16Prefix(gstr(audit, "at"), 10)+") — "+truncateText(gstr(audit, "report"), 90))
	}
	if recent := latestEventsForGoal(ledgerEvents, gstr(goal, "id"), 6); len(recent) > 0 {
		lines = append(lines, "Recent events:")
		for _, event := range recent {
			at := gstr(event, "at")
			lines = append(lines, "  "+jsSliceUTF16(at, 11, 19)+" "+gstr(event, "type"))
		}
	}
	return strings.Join(lines, "\n")
}

// jsSliceUTF16 is s.slice(from, to) by UTF-16 code units (from, to >= 0).
func jsSliceUTF16(s string, from, to int) string {
	pre := utf16Prefix(s, to)
	return strings.TrimPrefix(pre, utf16Prefix(pre, from))
}
