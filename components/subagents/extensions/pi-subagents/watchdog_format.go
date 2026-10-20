package pi_subagents

import (
	"strings"
)

// Warning content and rendering text (watchdog/warning-format.ts, watchdog/render.ts).

func wdEscapeXMLText(v string) string {
	v = strings.ReplaceAll(v, "&", "&amp;")
	v = strings.ReplaceAll(v, "<", "&lt;")
	return strings.ReplaceAll(v, ">", "&gt;")
}

func wdEscapeXMLAttribute(v string) string {
	return strings.ReplaceAll(wdEscapeXMLText(v), `"`, "&quot;")
}

// wdMessageOptions is the options of createWatchdogWarningMessage: Display nil is true; Details are the fields that override
// the warning's own (a field left empty does not override).
type wdMessageOptions struct {
	Display *bool
	Details wdWarning
}

// mergeWatchdogExtras is `{...warning, ...extras}` where an empty field of extras is undefined.
func mergeWatchdogExtras(w, extras wdWarning) wdWarning {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&w.Severity, extras.Severity)
	set(&w.Importance, extras.Importance)
	set(&w.Summary, extras.Summary)
	set(&w.Evidence, extras.Evidence)
	set(&w.RecommendedAction, extras.RecommendedAction)
	set(&w.Category, extras.Category)
	set(&w.Source, extras.Source)
	set(&w.Agent, extras.Agent)
	set(&w.RunID, extras.RunID)
	set(&w.State, extras.State)
	set(&w.Identity, extras.Identity)
	set(&w.DisplayedAt, extras.DisplayedAt)
	set(&w.Error, extras.Error)
	if extras.Stale != nil {
		w.Stale = extras.Stale
	}
	if extras.StalemateRepeats != nil {
		w.StalemateRepeats = extras.StalemateRepeats
	}
	return w
}

func normalizeWatchdogWarningDetails(w wdWarning, extras ...wdWarning) wdWarning {
	var ex wdWarning
	if len(extras) > 0 {
		ex = extras[0]
	}
	category := w.Category
	if category == "" {
		category = ex.Category
	}
	if category == "" {
		category = "other"
	}
	source := w.Source
	if source == "" {
		source = ex.Source
	}
	if source == "" {
		source = "main"
	}
	w.Category, w.Source = category, source
	return mergeWatchdogExtras(w, ex)
}

func formatWatchdogWarningContent(w wdWarning) string {
	d := normalizeWatchdogWarningDetails(w)
	lines := []string{
		`<subagent_watchdog severity="` + wdEscapeXMLAttribute(d.Severity) + `" importance="` + wdEscapeXMLAttribute(d.Importance) +
			`" category="` + wdEscapeXMLAttribute(d.Category) + `" source="` + wdEscapeXMLAttribute(d.Source) + `" guidance="weigh, don't blindly obey">`,
		"<summary>" + wdEscapeXMLText(d.Summary) + "</summary>",
		"<evidence>" + wdEscapeXMLText(d.Evidence) + "</evidence>",
		"<recommended_action>" + wdEscapeXMLText(d.RecommendedAction) + "</recommended_action>",
	}
	tag := func(name, value string) {
		if value != "" {
			lines = append(lines, "<"+name+">"+wdEscapeXMLText(value)+"</"+name+">")
		}
	}
	tag("agent", d.Agent)
	tag("run_id", d.RunID)
	tag("state", d.State)
	if d.Stale != nil {
		if *d.Stale {
			tag("stale", "true")
		} else {
			tag("stale", "false")
		}
	}
	if d.Severity == "blocker" {
		lines = append(lines, "<blocker_guidance>If this warning changes the outcome, produce a new self-contained final answer after addressing it.</blocker_guidance>")
	}
	lines = append(lines, "</subagent_watchdog>")
	return strings.Join(lines, "\n")
}

func createWatchdogWarningMessage(w wdWarning, opts wdMessageOptions) watchdogWarningMessage {
	d := normalizeWatchdogWarningDetails(w, opts.Details)
	display := true
	if opts.Display != nil {
		display = *opts.Display
	}
	return watchdogWarningMessage{CustomType: subagentWatchdogWarningType, Content: formatWatchdogWarningContent(d), Display: display, Details: d}
}

func wdTitleCase(value string) string {
	parts := strings.Split(value, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func wdStateLabels(w wdWarning) []string {
	var labels []string
	if w.State == "displayed" {
		labels = append(labels, "displayed")
	}
	if (w.Stale != nil && *w.Stale) || w.State == "stale" {
		labels = append(labels, "stale")
	}
	if w.State == "failed" {
		labels = append(labels, "failed review")
	}
	if w.State == "stalemate" {
		labels = append(labels, "stalemate")
	}
	return labels
}

func formatWatchdogWarningRenderText(w wdWarning) string {
	labels := wdStateLabels(w)
	subject := "Concern"
	if w.Severity == "blocker" {
		subject = "Blocker"
	}
	label := ""
	if len(labels) > 0 {
		label = " (" + strings.Join(labels, ", ") + ")"
	}
	meta := "Importance: " + wdTitleCase(w.Importance) + " · Category: " + wdTitleCase(w.Category) + " · Source: " + w.Source
	if w.Agent != "" {
		meta += " · Agent: " + w.Agent
	}
	if w.RunID != "" {
		meta += " · Run: " + w.RunID
	}
	lines := []string{"Subagent watchdog " + subject + label + ": " + w.Summary, "Evidence: " + w.Evidence, "Recommended action: " + w.RecommendedAction, meta}
	if w.State == "failed" && w.Error != "" {
		lines = append(lines, "Failure: "+w.Error)
	}
	if w.State == "stalemate" && w.StalemateRepeats != nil {
		plural := "s"
		if *w.StalemateRepeats == 1 {
			plural = ""
		}
		lines = append(lines, "Same warning "+itoa(*w.StalemateRepeats)+" time"+plural+" in a row; the watchdog stopped continuing the run.")
	}
	if (w.Stale != nil && *w.Stale) || w.State == "stale" {
		lines = append(lines, "This warning arrived after the watchdog catch-up timeout.")
	}
	return strings.Join(lines, "\n")
}

// renderWatchdogWarning is the transcript component as lines: the first line, then (expanded) a blank row and every other line,
// or (collapsed) the evidence line under a connector.
func renderWatchdogWarning(w wdWarning, expanded bool) []string {
	lines := strings.Split(formatWatchdogWarningRenderText(w), "\n")
	out := []string{lines[0]}
	if expanded {
		out = append(out, "")
		out = append(out, lines[1:]...)
	} else if len(lines) > 1 && lines[1] != "" {
		out = append(out, "  ⎿  "+lines[1])
	}
	return out
}
