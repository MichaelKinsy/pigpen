package rpiv_todo

import "strconv"

// themer is the part of the host theme the views use (sdk.UITheme satisfies it).
type themer interface {
	Fg(token, text string) string
	Bold(text string) string
	Strikethrough(text string) string
}

// i18nNamespace is the namespace the original registers its locale strings under.
// upstream: state/i18n-bridge.ts:23.
const i18nNamespace = "@juicesharp/rpiv-todo"

// tr is the original's `t(key, fallback)`. Without @juicesharp/rpiv-i18n the original returns the inline
// English literal at every call site, and so does this port (locales are an open exclusion, PORT.md E1).
func tr(_, fallback string) string { return fallback }

// formatStatusLabel is the status word every view shares. upstream: state/i18n-bridge.ts:55-64.
func formatStatusLabel(status string) string {
	switch status {
	case statusPending:
		return tr("status.pending", "pending")
	case statusInProgress:
		return tr("status.in_progress", "in progress")
	case statusCompleted:
		return tr("status.completed", "completed")
	case statusDeleted:
		return tr("status.deleted", "deleted")
	}
	return status
}

// statusGlyph and statusColor present the renderResult status echo; deleted uses muted so a successful
// delete differs from the error branch. upstream: view/format.ts:15-34.
var (
	statusGlyph = map[string]string{statusPending: "○", statusInProgress: "◐", statusCompleted: "●", statusDeleted: "⊘"}
	statusColor = map[string]string{statusPending: "dim", statusInProgress: "warning", statusCompleted: "success", statusDeleted: "muted"}
	actionGlyph = map[string]string{"create": "+", "update": "→", "delete": "×", "get": "›", "list": "☰", "clear": "∅"}
)

// overlayStatusGlyph is the glyph of the overlay's per-task row: completed is ✓ and deleted ✗ here.
// upstream: view/format.ts:45-56.
func overlayStatusGlyph(status string, theme themer) string {
	switch status {
	case statusPending:
		return theme.Fg("dim", "○")
	case statusInProgress:
		return theme.Fg("warning", "◐")
	case statusCompleted:
		return theme.Fg("success", "✓")
	case statusDeleted:
		return theme.Fg("error", "✗")
	}
	return ""
}

// formatOverlayTaskLine is one task row of the overlay: the subject colour reflects the state, ids and
// supporting metadata stay quiet. upstream: view/format.ts:62-79.
func formatOverlayTaskLine(t task, theme themer, showID bool) string {
	glyph := overlayStatusGlyph(t.Status, theme)
	color := "text"
	switch t.Status {
	case statusInProgress:
		color = "accent"
	case statusCompleted, statusDeleted:
		color = "muted"
	}
	subject := theme.Fg(color, sanitizeTerminalText(t.Subject))
	if t.Status == statusCompleted || t.Status == statusDeleted {
		subject = theme.Strikethrough(subject)
	}
	line := glyph
	if showID {
		line += " " + theme.Fg("dim", "#"+strconv.Itoa(t.ID))
	}
	line += " " + subject
	if t.Status == statusInProgress && t.ActiveForm != nil && *t.ActiveForm != "" {
		line += " " + theme.Fg("muted", "("+sanitizeTerminalText(*t.ActiveForm)+")")
	}
	if len(t.BlockedBy) > 0 {
		line += " " + theme.Fg("muted", "⛓ "+idList(t.BlockedBy, ","))
	}
	return line
}

// formatCommandTaskLine is one task line of /todos (no colour, indented bullet).
// upstream: view/format.ts:86-90.
func formatCommandTaskLine(t task, glyph string) string {
	form := ""
	if t.Status == statusInProgress && t.ActiveForm != nil && *t.ActiveForm != "" {
		form = " (" + sanitizeTerminalText(*t.ActiveForm) + ")"
	}
	block := ""
	if len(t.BlockedBy) > 0 {
		block = "    ⛓ " + idList(t.BlockedBy, ",")
	}
	return "  " + glyph + " #" + strconv.Itoa(t.ID) + " " + sanitizeTerminalText(t.Subject) + form + block
}

// renderTodoCall is the tool call's summary line: the action glyph and what it acts on.
// upstream: view/format.ts:101-120.
func renderTodoCall(args params, theme themer, state taskState) string {
	action, _ := text(args, "action")
	glyph, ok := actionGlyph[action]
	if !ok {
		glyph = action
	}
	out := theme.Fg("toolTitle", theme.Bold("todo ")) + theme.Fg("muted", glyph)
	switch {
	case action == "create" && args["subject"] != nil && args["subject"] != "":
		subject, _ := text(args, "subject")
		out += " " + theme.Fg("dim", sanitizeTerminalText(subject))
	case (action == "update" || action == "get" || action == "delete") && args["id"] != nil:
		id, _ := number(args["id"])
		subject, _ := selectTaskSubjectByID(state, id)
		label := "#" + jsNumber(id)
		if subject != "" {
			label = sanitizeTerminalText(subject)
		}
		out += " " + theme.Fg("accent", label)
	case action == "list" && args["status"] != nil && args["status"] != "":
		status, _ := text(args, "status")
		out += " " + theme.Fg("muted", formatStatusLabel(status))
	}
	return out
}

// renderTodoResult is the result's status echo: only create, update and delete advertise a status; the
// other actions fall back to a plain ✓. upstream: view/format.ts:129-164.
func renderTodoResult(details *taskDetails, theme themer) string {
	status := ""
	if details != nil {
		last := func() string {
			if n := len(details.Tasks); n > 0 {
				return details.Tasks[n-1].Status
			}
			return ""
		}
		byID := func() string {
			if id, ok := number(details.Params["id"]); ok {
				if i := findByNumber(details.Tasks, id); i != -1 {
					return details.Tasks[i].Status
				}
			}
			return ""
		}
		switch details.Action {
		case "create":
			status = last()
		case "update":
			if s, ok := text(details.Params, "status"); ok && s != "" {
				status = s
			} else {
				status = byID()
			}
		case "delete":
			status = byID()
		}
	}
	if status != "" {
		return theme.Fg(statusColor[status], statusGlyph[status]+" "+formatStatusLabel(status))
	}
	return theme.Fg("success", "✓")
}
