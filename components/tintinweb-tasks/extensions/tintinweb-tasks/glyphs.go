package tintinweb_tasks

import (
	"regexp"
	"slices"
)

// The glyphs the task widget and the /tasks menu are drawn with. upstream: task-glyphs.ts. Glyphs are pure
// data: a hand-edited config must never break the widget, so anything unrecognised falls back to the default.

// taskGlyphs is a fully resolved glyph set.
type taskGlyphs struct {
	Completed, InProgress, Pending string
	Spinner                        []string
	CompletedSummary, Header       string
	Overflow, Blocked              string
	InputTokens, OutputTokens      string
	StatsSeparator                 string
	TrailingEllipsis, Truncation   string
}

// builtinGlyphs is every glyph's built-in default; completedSummary inherits `completed`. upstream: task-glyphs.ts:46-59.
var builtinGlyphs = taskGlyphs{
	Completed: "✔", InProgress: "◼", Pending: "◻",
	Spinner: []string{"✳", "✴", "✵", "✶", "✷", "✸", "✹", "✺", "✻", "✼", "✽"},
	Header:  "●", Overflow: "…", Blocked: "›", InputTokens: "↑", OutputTokens: "↓",
	StatsSeparator: "·", TrailingEllipsis: "…", Truncation: "...",
}

// unsafeGlyph matches control characters (they would break the one-line-per-entry contract or steer the
// terminal) and bidi overrides. upstream: task-glyphs.ts:67.
var unsafeGlyph = regexp.MustCompile(`[\p{Cc}\x{200E}\x{200F}\x{202A}-\x{202E}\x{2066}-\x{2069}]`)

// isGlyph is any non-empty string without an unsafe character. upstream: task-glyphs.ts:71-72.
func isGlyph(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || s == "" || unsafeGlyph.MatchString(s) {
		return "", false
	}
	return s, true
}

// resolveTaskGlyphs resolves configured glyphs (a decoded JSON object, or nil) against the defaults. Each
// glyph falls back on its own; the spinner falls back as a whole. upstream: task-glyphs.ts:97-118.
func resolveTaskGlyphs(configured any) taskGlyphs {
	cfg, _ := configured.(map[string]any)
	glyph := func(key, fallback string) string {
		if s, ok := isGlyph(cfg[key]); ok {
			return s
		}
		return fallback
	}
	g := taskGlyphs{
		Completed:        glyph("completed", builtinGlyphs.Completed),
		InProgress:       glyph("inProgress", builtinGlyphs.InProgress),
		Pending:          glyph("pending", builtinGlyphs.Pending),
		Spinner:          builtinGlyphs.Spinner,
		Header:           glyph("header", builtinGlyphs.Header),
		Overflow:         glyph("overflow", builtinGlyphs.Overflow),
		Blocked:          glyph("blocked", builtinGlyphs.Blocked),
		InputTokens:      glyph("inputTokens", builtinGlyphs.InputTokens),
		OutputTokens:     glyph("outputTokens", builtinGlyphs.OutputTokens),
		StatsSeparator:   glyph("statsSeparator", builtinGlyphs.StatsSeparator),
		TrailingEllipsis: glyph("trailingEllipsis", builtinGlyphs.TrailingEllipsis),
		Truncation:       glyph("truncation", builtinGlyphs.Truncation),
	}
	// completedSummary falls back to the resolved `completed`, not to a literal of its own.
	g.CompletedSummary = glyph("completedSummary", g.Completed)
	if frames, ok := cfg["spinner"].([]any); ok && len(frames) > 0 {
		var spinner []string
		for _, f := range frames {
			s, ok := isGlyph(f)
			if !ok {
				spinner = nil
				break
			}
			spinner = append(spinner, s)
		}
		if spinner != nil {
			g.Spinner = slices.Clone(spinner)
		}
	}
	return g
}
