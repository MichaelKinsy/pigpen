package pi_goal_x

import "strings"

// ported from goal-widget.ts: the pure helpers not already in goal_widget_keys.go (isKittyRepeatOrRelease, isDebugEnabled). Stateful parts (left to the SDK integration step): toggleTaskViaService
// (goal service + UI dialogs), syncTerminalInputPause (terminal input subscription, goalModalDepth, settings, pause/audit
// abort), the debugGoalCounter/debugMockAuditTimer module state and openDebugProposal / the debug key handlers.

func formatModeLabelDebug(sisyphus bool) string {
	if sisyphus {
		return "Sisyphus (prompt/criteria style)"
	}
	return "Normal goal"
}

func formatPrefixedLinesDebug(content string) []string {
	lines := []string{}
	for _, raw := range strings.Split(content, "\n") {
		trimmed := jsTrim(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "│") {
			lines = append(lines, raw)
		} else {
			lines = append(lines, "│   "+raw)
		}
	}
	return lines
}

func formatSectionDebug(title, content string) []string {
	return append([]string{"", "─── " + title + " ───", ""}, formatPrefixedLinesDebug(content)...)
}

// renderDebugTaskLines renders task lines for the debug proposal dialog.
func renderDebugTaskLines(tasks []any, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	lines := []string{}
	for _, x := range tasks {
		t := asRecord(x)
		if t == nil {
			continue
		}
		marker := "[ ]"
		switch gstr(t, "status") {
		case "complete":
			marker = "[x]"
		case "skipped":
			marker = "[~]"
		}
		lw := ""
		if gflag(t, "lightweightSubtasks") {
			lw = " (lightweight)"
		}
		lines = append(lines, prefix+marker+" "+gstr(t, "id")+": "+gstr(t, "title")+lw)
		if subs, ok := t.vals["subtasks"].([]any); ok && len(subs) > 0 {
			lines = append(lines, renderDebugTaskLines(subs, indent+1)...)
		}
	}
	return lines
}
