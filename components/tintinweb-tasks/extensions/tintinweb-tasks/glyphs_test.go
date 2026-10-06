package tintinweb_tasks

import "testing"

var defaultGlyphs = taskGlyphs{
	Completed: "✔", InProgress: "◼", Pending: "◻",
	Spinner:          []string{"✳", "✴", "✵", "✶", "✷", "✸", "✹", "✺", "✻", "✼", "✽"},
	CompletedSummary: "✔", Header: "●", Overflow: "…", Blocked: "›", InputTokens: "↑", OutputTokens: "↓",
	StatsSeparator: "·", TrailingEllipsis: "…", Truncation: "...",
}

func TestTaskGlyphs(t *testing.T) {
	const f = "task-glyphs"
	tw(t, f, "returns the built-in glyphs when nothing is configured", func(t *testing.T) {
		eq(t, resolveTaskGlyphs(nil), defaultGlyphs)
	})
	tw(t, f, "returns the built-in glyphs for an empty glyph object", func(t *testing.T) {
		eq(t, resolveTaskGlyphs(obj{}), resolveTaskGlyphs(nil))
	})
	tw(t, f, "applies every configured glyph", func(t *testing.T) {
		configured := obj{"completed": "[x]", "inProgress": "[>]", "pending": "[ ]", "spinner": arr{"|", "/", "-", "\\"},
			"completedSummary": "[=]", "header": "*", "overflow": "~", "blocked": ">", "inputTokens": "in",
			"outputTokens": "out", "statsSeparator": "|", "trailingEllipsis": "~~", "truncation": ">>"}
		eq(t, resolveTaskGlyphs(configured), taskGlyphs{Completed: "[x]", InProgress: "[>]", Pending: "[ ]",
			Spinner: []string{"|", "/", "-", "\\"}, CompletedSummary: "[=]", Header: "*", Overflow: "~", Blocked: ">",
			InputTokens: "in", OutputTokens: "out", StatsSeparator: "|", TrailingEllipsis: "~~", Truncation: ">>"})
	})
	tw(t, f, "keeps the default for glyphs that are not configured", func(t *testing.T) {
		g := resolveTaskGlyphs(obj{"pending": "[ ]"})
		eq(t, g.Pending, "[ ]")
		eq(t, g.Completed, defaultGlyphs.Completed)
		eq(t, g.Header, defaultGlyphs.Header)
		eq(t, g.Spinner, defaultGlyphs.Spinner)
	})
	tw(t, f, "falls back per glyph when a value is not a non-empty string", func(t *testing.T) {
		g := resolveTaskGlyphs(obj{"completed": float64(42), "inProgress": "", "blocked": nil, "pending": "[ ]"})
		eq(t, g.Completed, defaultGlyphs.Completed)
		eq(t, g.InProgress, defaultGlyphs.InProgress)
		eq(t, g.Blocked, defaultGlyphs.Blocked)
		eq(t, g.Pending, "[ ]")
	})
	tw(t, f, "falls back per glyph for a control character or a bidi override", func(t *testing.T) {
		// A newline would break the widget's one-line-per-entry contract, an OSC sequence
		// would retitle the terminal, and an RLO reorders the line around the glyph.
		g := resolveTaskGlyphs(obj{"completed": "X\n", "inProgress": "\x1b]0;pwned\x07", "header": "\x7f",
			"overflow": "\u202eabc", "pending": "[ ]"})
		eq(t, g.Completed, defaultGlyphs.Completed)
		eq(t, g.InProgress, defaultGlyphs.InProgress)
		eq(t, g.Header, defaultGlyphs.Header)
		eq(t, g.Overflow, defaultGlyphs.Overflow)
		eq(t, g.Pending, "[ ]")
	})
	tw(t, f, "accepts a single space, which renders as spacing only", func(t *testing.T) {
		eq(t, resolveTaskGlyphs(obj{"blocked": " "}).Blocked, " ")
	})
	tw(t, f, "accepts glyphs that are more than one character", func(t *testing.T) {
		// A Nerd Font glyph with the trailing space its width needs, and an emoji whose
		// variation selector must survive the control-character check.
		g := resolveTaskGlyphs(obj{"completed": "[x]", "inProgress": "⣾⣾", "pending": "🌑\uFE0F", "blocked": "\uE0A0 "})
		eq(t, g.Completed, "[x]")
		eq(t, g.InProgress, "⣾⣾")
		eq(t, g.Pending, "🌑\uFE0F")
		eq(t, g.Blocked, "\uE0A0 ")
	})
	tw(t, f, "follows `completed` when only `completed` is set", func(t *testing.T) {
		eq(t, resolveTaskGlyphs(obj{"completed": "[x]"}).CompletedSummary, "[x]")
	})
	tw(t, f, "wins over `completed` when set explicitly", func(t *testing.T) {
		g := resolveTaskGlyphs(obj{"completed": "[x]", "completedSummary": "[=]"})
		eq(t, g.Completed, "[x]")
		eq(t, g.CompletedSummary, "[=]")
	})
	tw(t, f, "falls back through `completed` to the default when both are unusable", func(t *testing.T) {
		eq(t, resolveTaskGlyphs(obj{"completed": "", "completedSummary": ""}).CompletedSummary, "✔")
	})
	tw(t, f, "accepts frames that are more than one glyph", func(t *testing.T) {
		spinner := []string{"⣾⣾", "🌑\uFE0F", "..", "a\u0301"}
		eq(t, resolveTaskGlyphs(obj{"spinner": arr{"⣾⣾", "🌑\uFE0F", "..", "a\u0301"}}).Spinner, spinner)
	})
	tw(t, f, "falls back as a whole when the configured sequence is unusable", func(t *testing.T) {
		rejected := []any{arr{}, "✳✴", nil, arr{"✳", float64(7)}, arr{"✳", ""}, arr{"✳", "✴\x1b[2J"}, obj{}}
		for _, spinner := range rejected {
			eq(t, resolveTaskGlyphs(obj{"spinner": spinner}).Spinner, defaultGlyphs.Spinner)
		}
	})
}
