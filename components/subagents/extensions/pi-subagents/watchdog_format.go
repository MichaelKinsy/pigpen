package pi_subagents


// Warning content and rendering text (watchdog/warning-format.ts, watchdog/render.ts).

func wdEscapeXMLText(v string) string { panic("not implemented") }

func wdEscapeXMLAttribute(v string) string { panic("not implemented") }

// wdMessageOptions is the options of createWatchdogWarningMessage: Display nil is true; Details are the fields that override
// the warning's own (a field left empty does not override).
type wdMessageOptions struct {
	Display *bool
	Details wdWarning
}

// mergeWatchdogExtras is `{...warning, ...extras}` where an empty field of extras is undefined.
func mergeWatchdogExtras(w, extras wdWarning) wdWarning { panic("not implemented") }

func normalizeWatchdogWarningDetails(w wdWarning, extras ...wdWarning) wdWarning {
	panic("not implemented")
}

func formatWatchdogWarningContent(w wdWarning) string { panic("not implemented") }

func createWatchdogWarningMessage(w wdWarning, opts wdMessageOptions) watchdogWarningMessage {
	panic("not implemented")
}

func wdTitleCase(value string) string { panic("not implemented") }

func wdStateLabels(w wdWarning) []string { panic("not implemented") }

func formatWatchdogWarningRenderText(w wdWarning) string { panic("not implemented") }

// renderWatchdogWarning is the transcript component as lines: the first line, then (expanded) a blank row and every other line,
// or (collapsed) the evidence line under a connector.
func renderWatchdogWarning(w wdWarning, expanded bool) []string { panic("not implemented") }
