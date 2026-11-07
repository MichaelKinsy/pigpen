package posthorse

// Theme tokens of the cards: every row carries the color the original gives it (cards.ts, ui.ts), checked on the raw
// escape sequences the host palette of ui_testhost_test.go produces.

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

const (
	uiOut   = "\x1b[38;5;250m" // toolOutput
	uiMuted = "\x1b[38;5;244m"
	uiDim   = "\x1b[38;5;240m"
	uiErr   = "\x1b[38;5;196m"
	uiWarn  = "\x1b[38;5;214m"
	uiTitle = "\x1b[38;5;33m"
	uiLabel = "\x1b[38;5;99m"
	uiBg    = "\x1b[48;5;53m"
	uiBold  = "\x1b[1m"
)

// uiRowWith returns the raw row whose visible text is text.
func uiRowWith(t *testing.T, rows []string, text string) string {
	t.Helper()
	for _, r := range rows {
		if strings.TrimSpace(tui.StripTerminalSequences(r)) == text {
			return r
		}
	}
	t.Fatalf("no row %q in %q", text, uiPlain(rows))
	return ""
}

func uiColored(t *testing.T, rows []string, text string, want ...string) {
	t.Helper()
	row := uiRowWith(t, rows, text)
	for _, w := range want {
		if !strings.Contains(row, w) {
			t.Errorf("row %q lacks %q: %q", text, w, row)
		}
	}
}

func uiNotColored(t *testing.T, rows []string, text string, not ...string) {
	t.Helper()
	row := uiRowWith(t, rows, text)
	for _, n := range not {
		if strings.Contains(row, n) {
			t.Errorf("row %q must not carry %q: %q", text, n, row)
		}
	}
}

func uiContextDetails(rollover string, tokens any, percent any) map[string]any {
	return map[string]any{"kind": "context", "usage": map[string]any{"tokens": tokens, "contextWindow": 8192.0, "percent": percent}, "rollover": rollover}
}

func TestUiColorTokens(t *testing.T) {
	u := newUIHost(t)
	render := func(c *uiCard, width int) []string { return c.render(width) }

	// call card: toolTitle and bold, error in the error token
	c := u.card("notes", map[string]any{"op": "read", "path": "a.md"})
	c.updateResult(uiResult("hello", nil), false, false)
	rows := render(c, 80)
	uiColored(t, rows, "▸ Notes · read · a.md", uiTitle, uiBold)
	uiNotColored(t, rows, "▸ Notes · read · a.md", uiErr)
	c.setExpanded(true)
	uiColored(t, render(c, 80), "▾ Notes · read · a.md", uiTitle, uiBold)
	failed := u.card("notes", map[string]any{"op": "read", "path": "a.md"})
	failed.updateResult(uiResult("boom", nil), true, false)
	rows = render(failed, 80)
	uiColored(t, rows, "▸ Notes · read · error · a.md", uiErr, uiBold)
	uiNotColored(t, rows, "▸ Notes · read · error · a.md", uiTitle)
	uiColored(t, rows, "boom", uiErr)
	uiNotColored(t, rows, "boom", uiOut)

	// result: summary and preview in toolOutput, the hint dim
	note := u.card("notes", map[string]any{"op": "read", "path": "a.md"})
	note.updateResult(uiResult("File: /x\nhello", map[string]any{"kind": "note-read", "headerLength": 9.0, "offset": 0.0, "end": 5.0, "total": 5.0}), false, false)
	rows = render(note, 80)
	uiColored(t, rows, "5 chars · complete", uiOut)
	uiColored(t, rows, "hello", uiOut)
	uiColored(t, rows, "… ctrl+o to expand", uiDim)
	uiNotColored(t, rows, "… ctrl+o to expand", uiOut, uiMuted)
	note.setExpanded(true)
	rows = render(note, 80)
	if got := uiPlain(rows); len(got) != 6 { // spacer, padding, call, two body rows, padding: no row for the empty attachment/footer/submission
		t.Errorf("expanded note card rows: %q", got)
	}
	uiColored(t, rows, "File: /x", uiOut)

	// partial results are plain output, whatever their display says
	partial := u.card("get_context_remaining", map[string]any{})
	partial.updateResult(uiResult("so far", uiContextDetails("unsupported", 1192.0, 14.5)), false, true)
	rows = render(partial, 80)
	uiColored(t, rows, "so far", uiOut)
	uiNotColored(t, rows, "so far", uiWarn)

	// context: warning only for unsupported with known tokens
	for _, tc := range []struct {
		name    string
		details map[string]any
		row     string
		warn    bool
	}{
		{"unsupported", uiContextDetails("unsupported", 1192.0, 14.5), "Automatic rollover unsupported", true},
		{"disabled", uiContextDetails("disabled", 1192.0, 14.5), "Automatic rollover disabled", false},
		{"enabled", uiContextDetails("enabled", 1192.0, 14.5), "Automatic rollover enabled", false},
		{"unknown tokens", uiContextDetails("unsupported", nil, nil), "Context usage unknown", false},
	} {
		ctx := u.card("get_context_remaining", map[string]any{})
		ctx.updateResult(uiResult("t", tc.details), false, false)
		rows = render(ctx, 80)
		if tc.warn {
			uiColored(t, rows, tc.row, uiWarn)
			uiNotColored(t, rows, tc.row, uiOut)
		} else {
			uiColored(t, rows, tc.row, uiOut)
			uiNotColored(t, rows, tc.row, uiWarn)
		}
		if tc.name == "unsupported" {
			uiColored(t, rows, "≈1,192 / 8,192 used (≈15%)", uiWarn)
		}
	}
	ctx := u.card("get_context_remaining", map[string]any{})
	ctx.updateResult(uiResult("t", uiContextDetails("unsupported", 1192.0, 14.5)), false, false)
	ctx.setExpanded(true)
	uiColored(t, render(ctx, 80), "t", uiWarn)

	// attachments are muted
	pic := u.card("notes", map[string]any{"op": "read", "path": "a.md"})
	pic.updateResult(map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello"}, map[string]any{"type": "image", "data": "x", "mimeType": "image/png"}}}, false, false)
	rows = render(pic, 80)
	uiColored(t, rows, "1 image attached", uiMuted)
	pic.setExpanded(true)
	uiColored(t, render(pic, 80), "1 image attached", uiMuted)

	// expanded call details are muted
	hist := u.card("history", map[string]any{"op": "search", "query": "q", "all": true})
	hist.setExpanded(true)
	hist.updateResult(uiResult("H1 body one\nfoot", map[string]any{"kind": "history-search", "entries": []any{map[string]any{"headerLength": 3.0, "length": 11.0}}, "footerLength": 5.0, "more": true, "skipped": 0.0}), false, false)
	rows = render(hist, 80)
	uiColored(t, rows, "Scope: all project sessions", uiMuted)
	uiColored(t, rows, "body one", uiOut)
	uiColored(t, rows, "H1", uiDim)
	uiColored(t, rows, "foot", uiMuted)
	hist.setExpanded(false)
	rows = render(hist, 80)
	uiColored(t, rows, "1 matches · all sessions", uiOut)
	uiColored(t, rows, "body one", uiOut)

	// submitted content is output text
	write := u.card("notes", map[string]any{"op": "write", "path": "a.md", "content": "hi"})
	write.setExpanded(true)
	write.updateResult(uiResult("Wrote p", map[string]any{"kind": "note-write"}), false, false)
	rows = render(write, 80)
	uiColored(t, rows, "Submitted content:", uiOut)
	uiColored(t, rows, "hi", uiOut)
}

func TestUiMessageColors(t *testing.T) {
	u := newUIHost(t)
	u.state(map[string]any{"keybindings": map[string]any{"definitions": map[string]any{"app.tools.expand": map[string]any{"defaultKeys": "ctrl+o"}}, "userBindings": map[string]any{}}})
	reminder := u.message("posthorse-reminder", "Checkpoint now\nl2\nl3\nl4", 1)
	rows := reminder.render(60)
	uiColored(t, rows, "▸ Checkpoint reminder", uiWarn, uiBold)
	uiColored(t, rows, "Checkpoint now", uiOut)
	uiColored(t, rows, "… ctrl+o to expand", uiDim)
	for i, r := range rows[1:] { // the spacer row above the box is the host's
		if !strings.Contains(r, uiBg) {
			t.Errorf("row %d has no customMessageBg: %q", i, r)
		}
	}
	reminder.setExpanded(true)
	rows = reminder.render(60)
	uiColored(t, rows, "▾ Checkpoint reminder", uiWarn, uiBold)
	uiColored(t, rows, "l4", uiOut)

	label := "Context window window-2 starts here.\n\nHandoff from the previous window:\n"
	window := u.message("context-window", label+"H1\nH2\nH3", 1)
	rows = window.render(60)
	uiColored(t, rows, "▸ Context window started", uiLabel, uiBold)
	uiNotColored(t, rows, "▸ Context window started", uiWarn)
	uiColored(t, rows, "Earlier conversation remains in history.", uiMuted)
	uiColored(t, rows, "H1", uiOut)
	uiColored(t, rows, "… ctrl+o to expand", uiDim)
	window.setExpanded(true)
	rows = window.render(60)
	uiColored(t, rows, "H3", uiOut)

	// no handoff label: no preview rows (spacer, padding, heading, history text, hint, padding)
	bare := u.message("context-window", "Context window w starts here.", 1)
	if got := uiPlain(bare.render(60)); len(got) != 6 {
		t.Errorf("window without a handoff: %q", got)
	}
}
