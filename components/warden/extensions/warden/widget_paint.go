package warden

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The status stack above (or below) the editor, as extension.ts paint draws it. The SDK sends widget lines to the host as
// static rows; the width in force when they are drawn is the one rendered for, and a resize repaints (watchWidth).

// widgetLinesLocked returns the widget's rows for the current guards. w.mu must be held.
func (w *warden) widgetLinesLocked(ctx sdk.Context, cfg Config) []string {
	if !cfg.Widget.Enabled || len(w.widget) == 0 {
		return nil
	}
	theme := ThemeLike(ctx.UITheme())
	width := ctx.Width()
	entries := make([]WidgetEntry, len(w.widget))
	for i, wl := range w.widget {
		entries[i] = WidgetEntry{Guard: string(wl.guard), Line: wl.line}
	}
	// Live mode shows only the most recent guard, re-rendered with a sentence template.
	if cfg.Widget.BarMode == "live" {
		if all := w.trace.Entries(); len(all) > 0 {
			last := all[len(all)-1]
			if last.Tokens != nil {
				return liveSentence(last, theme, width)
			}
			entries = []WidgetEntry{{Guard: string(last.Guard), Line: last.Line}}
		} else {
			entries = entries[len(entries)-1:]
		}
	}
	return WidgetLines(entries, theme, width)
}

// liveSentence is the sentence form of the latest entry as rows: the verdict chip, the guard, then the sentence wrapped
// under its own column.
func liveSentence(entry TraceEntry, theme ThemeLike, width int) []string {
	template := PickSentenceTemplate(string(entry.Guard), Tokens(entry.Tokens))
	sentence := RenderTemplate(template, Tokens(entry.Tokens))
	chip, hasLevel := entry.Tokens["level"]
	if !hasLevel {
		chip = entry.Tokens["status"]
	}
	color := LevelColorOr(chip, "text")
	chipText, chipWidth := "", 0
	if chip != "" {
		chipText = theme.Bold(theme.Fg(color, strings.ToUpper(chip))) + "  "
		chipWidth = utf16Len(chip) + 2
	}
	head := chipText + theme.Fg("muted", string(entry.Guard)+" ")
	headWidth := chipWidth + utf16Len(string(entry.Guard)) + 1
	if width == 0 {
		return []string{head + sentence}
	}
	wrapped := widthx.WrapTextWithAnsi(sentence, max(10, width-headWidth))
	if len(wrapped) == 0 {
		return []string{head}
	}
	out := []string{head + wrapped[0]}
	for _, rest := range wrapped[1:] {
		out = append(out, strings.Repeat(" ", headWidth)+rest)
	}
	return out
}

// LevelColorOr is LEVEL_COLOR[level] ?? fallback.
func LevelColorOr(level, fallback string) string {
	if c, ok := LevelColor[level]; ok {
		return c
	}
	return fallback
}
