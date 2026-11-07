package posthorse

// Twins of test/renderers.test.ts (lane E). The Pi components of the original
// (ToolExecutionComponent, CustomMessageComponent) are host code: uiCard and
// uiMessage in ui_testhost_test.go compose the rows the Go renderers return
// the way those shells do, so the assertions are the original's, on the
// rendered rows.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func uiMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("expected %q to match %s", text, pattern)
	}
}

func uiNoMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("expected %q not to match %s", text, pattern)
	}
}

func TestTwinRenderersUi(t *testing.T) {
	// renderers.test.ts:155
	tw(t, "renderers", "all four native cards identify pending calls and tolerate malformed streamed arguments", func(t *testing.T) {
		u := newUIHost(t)
		for _, tc := range []struct {
			name     string
			args     map[string]any
			expected string
		}{
			{"notes", map[string]any{"op": "read", "path": "plan.md"}, `Notes.*read.*plan\.md`},
			{"history", map[string]any{"op": "search", "query": "relay", "all": true}, `History.*search.*relay`},
			{"get_context_remaining", map[string]any{}, `Context`},
			{"new_context", map[string]any{"handoff": "next station"}, `New context`},
		} {
			cards := toolCards(tc.name)
			if cards.Call == nil || cards.Result == nil {
				t.Fatalf("%s needs its native call and result renderers", tc.name)
			}
			if cards.Shell == "self" {
				t.Fatal("Pi owns the card shell and expansion")
			}
			c := u.card(tc.name, tc.args)
			c.markExecutionStarted()
			uiMatch(t, tc.expected, uiText(t, c, 80))
			uiMatch(t, `(?i)running`, uiText(t, c, 80))
			for _, malformed := range []map[string]any{
				nil,
				nil,
				{"op": float64(3), "path": map[string]any{}},
				{"query": []any{"bad"}, "id": false},
			} {
				c.updateArgs(malformed)
				if rows := len(uiLines(t, c, 24)); rows > 6 {
					t.Fatalf("%s: %d rows for %v", tc.name, rows, malformed)
				}
			}
		}
	})

	// renderers.test.ts:200
	tw(t, "renderers", "native cards cap actual wrapped rows and expose complete legacy text by click or global expansion", func(t *testing.T) {
		body := "START " + strings.Repeat("界 👩🏽‍💻 é ", 900) + " END"
		u := newUIHost(t)
		c := u.card("notes", map[string]any{"op": "read", "path": strings.Repeat("長", 100) + ".md"})
		c.updateResult(uiResult(body, nil), false, false)
		for _, width := range []int{24, 40, 80, 196} {
			collapsed := uiText(t, c, width)
			if rows := len(uiLines(t, c, width)); rows > 10 {
				t.Fatalf("collapsed height is bounded at %d: %d", width, rows)
			}
			uiMatch(t, `START`, collapsed)
			uiNoMatch(t, ` END`, collapsed)
		}
		// The left click is Pi's shell (createResultRegion); the tmux scenario proves it on both hosts.
		if !c.click() {
			t.Fatal("a click on a card with a result is handled")
		}
		uiMatch(t, ` END`, uiText(t, c, 80))
		c.setExpanded(false)
		uiNoMatch(t, ` END`, uiText(t, c, 80))
		c.setExpanded(true)
		uiMatch(t, ` END`, uiText(t, c, 80))
	})

	// renderers.test.ts:226
	tw(t, "renderers", "collapsed previews show exactly the first rows of the expanded text", func(t *testing.T) {
		u := newUIHost(t)
		bodies := []string{
			"START " + strings.Repeat("👩🏽‍💻", 800) + " END",
			strings.Repeat("👨‍👩‍👧‍👦 ", 500),
			strings.Repeat("e"+strings.Repeat("\u0301", 30)+" ", 200),
			"界 👩🏽‍💻 é " + strings.Repeat("長", 3000),
			strings.Repeat("x", 6000),
			"one\ntwo\nthree\n" + strings.Repeat("four ", 1500),
			strings.Repeat("a\tb ", 1500),
			"one\ntwo\nthree",
		}
		note := u.card("notes", map[string]any{"op": "read", "path": "a.md"})
		for _, body := range bodies {
			note.updateResult(uiResult(body, nil), false, false)
			reminder := u.message("posthorse-reminder", body, 0)
			for _, subject := range []struct {
				component  uiComponent
				alwaysHint bool
			}{{note, false}, {reminder, true}} {
				for _, width := range []int{24, 40, 80, 196} {
					subject.component.setExpanded(true)
					expanded := uiLines(t, subject.component, width)
					subject.component.setExpanded(false)
					collapsed := uiLines(t, subject.component, width)
					start := 0
					for i, row := range collapsed {
						if strings.Contains(row, "▸") {
							start = i + 1
							break
						}
					}
					hinted := len(collapsed) >= 2 && strings.Contains(collapsed[len(collapsed)-2], "…")
					full := expanded[start : len(expanded)-1]
					hintEnd := len(collapsed) - 1
					if hinted {
						hintEnd = len(collapsed) - 2
					}
					label := u16slice(body, 0, 12) + " at " + strconv.Itoa(width) + " columns"
					got := collapsed[start:hintEnd]
					want := full[:min(3, len(full))]
					if strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Fatalf("%s: collapsed rows\n%q\nwant the first rows of the expanded text\n%q", label, got, want)
					}
					if hinted != (subject.alwaysHint || len(full) > 3) {
						t.Fatalf("%s: hinted=%v, expanded rows %d", label, hinted, len(full))
					}
				}
			}
		}
	})

	// renderers.test.ts:350
	tw(t, "renderers", "empty notes, no matches, actual errors, and submitted content stay distinct", func(t *testing.T) {
		u := newUIHost(t)
		empty := u.card("notes", map[string]any{"op": "read", "path": "empty.md"})
		empty.updateResult(uiResult("", nil), false, false)
		uiMatch(t, `Empty note`, uiText(t, empty, 80))
		failed := u.card("notes", map[string]any{"op": "read", "path": "missing.md"})
		failed.updateResult(uiResult("No note at missing.md. Use list.", nil), true, false)
		uiMatch(t, `(?i)error`, uiText(t, failed, 80))
		uiMatch(t, `No note at missing\.md`, uiText(t, failed, 80))
		noMatches := u.card("history", map[string]any{"op": "search", "query": "nothing"})
		noMatches.updateResult(uiResult(`No history matches "nothing".`, nil), false, false)
		uiMatch(t, `No history matches`, uiText(t, noMatches, 80))
		write := u.card("notes", map[string]any{"op": "write", "path": "draft.md", "content": "SUBMITTED TEXT"})
		write.updateResult(uiResult("Wrote .pi/notes/draft.md", nil), false, false)
		write.setExpanded(true)
		uiMatch(t, `SUBMITTED TEXT`, uiText(t, write, 80))
	})

	// renderers.test.ts:495
	tw(t, "renderers", "native rendering preserves theme changes and leaves drag selection to Pi", func(t *testing.T) {
		u := newUIHost(t)
		c := u.card("notes", map[string]any{"op": "read", "path": "theme.md"})
		c.updateResult(uiResult("A readable note", nil), false, false)
		noteRow := func(rows []string) string {
			for _, row := range rows {
				if strings.Contains(row, "A readable note") {
					return row
				}
			}
			t.Fatal("no row carries the note")
			return ""
		}
		before := c.render(80)
		// initTheme("light"), then component.invalidate(): the host pushes the theme and draws the card again.
		u.setTheme(uiLight)
		after := c.render(80)
		if noteRow(after) == noteRow(before) {
			t.Fatal("result colors update, not just Pi's shell or title")
		}
		stripped := func(rows []string) string { return tui.StripTerminalSequences(strings.Join(rows, "\n")) }
		if stripped(after) != stripped(before) {
			t.Fatalf("a theme change leaves the text\n%q\n%q", after, before)
		}
		// The drag event of the original is delivered to Pi's shell, which leaves selection to the terminal:
		// there is no renderer involvement to test here (tmux scenario "drag selection").
	})

	// renderers.test.ts:520
	tw(t, "renderers", "partial and error results cannot inherit a success summary or terminal controls", func(t *testing.T) {
		controls := "\x1b[2J\x1b]52;c;c2VjcmV0\x07\x1b_hidden\x1b\\\x00\r"
		u := newUIHost(t)
		c := u.card("notes", map[string]any{"op": "write", "path": "draft" + controls + ".md", "content": "saved" + controls})
		c.updateResult(uiResult("still running"+controls, map[string]any{"kind": "note-write"}), false, true)
		uiMatch(t, `still running`, uiText(t, c, 80))
		uiNoMatch(t, `Saved note`, uiText(t, c, 80))
		c.updateResult(uiResult("ERROR HEAD "+strings.Repeat("problem ", 100)+" ERROR TAIL"+controls, map[string]any{"kind": "note-write"}), true, false)
		uiMatch(t, `ERROR HEAD`, uiText(t, c, 80))
		uiNoMatch(t, `Saved note`, uiText(t, c, 80))
		c.setExpanded(true)
		uiMatch(t, `ERROR TAIL`, uiText(t, c, 80))
		// Sanitization must reject terminal erase/clipboard controls and C0 bytes from results.
		uiNoMatch(t, "\x1b\\[2J|\x1b\\]52|hidden|\x00|\r", strings.Join(c.render(80), "\n"))
	})

	// renderers.test.ts:612
	tw(t, "renderers", "legacy history and malformed display spans fall back to the complete returned text", func(t *testing.T) {
		raw := "archive.jsonl 2026-09-06T17:43:46Z [window old] [owner-1] [user] Relay station is ready.\nSECOND LINE"
		u := newUIHost(t)
		for _, details := range []any{
			nil,
			map[string]any{"kind": "history-search", "entries": []any{map[string]any{"headerLength": float64(99999), "length": float64(3)}}},
			map[string]any{"kind": "history-search", "entries": []any{nil}},
			map[string]any{"kind": "context", "usage": map[string]any{"tokens": "not-number", "contextWindow": float64(100), "percent": nil}},
		} {
			c := u.card("history", map[string]any{"op": "search", "query": "Relay station"})
			c.updateResult(uiResult(raw, details), false, false)
			uiMatch(t, `Relay station`, uiText(t, c, 40))
			if rows := len(uiLines(t, c, 40)); rows > 10 {
				t.Fatalf("%d rows", rows)
			}
			c.setExpanded(true)
			uiNoMatch(t, `NaN|tokens to rollover`, uiText(t, c, 80))
			for _, line := range strings.Split(raw, "\n") {
				if !strings.Contains(uiText(t, c, 196), line) {
					t.Fatalf("expanded text lacks %q", line)
				}
			}
		}
		blankBlocks := u.card("history", map[string]any{"op": "read", "id": "spaced"})
		blankBlocks.updateResult(map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "FIRST"},
			map[string]any{"type": "text", "text": ""},
			map[string]any{"type": "text", "text": "LAST"},
		}}, false, false)
		blankBlocks.setExpanded(true)
		uiMatch(t, `FIRST[ \t]*\n[ \t]*\n[ \t]*LAST`, uiText(t, blankBlocks, 80))
	})

	// renderers.test.ts:683
	tw(t, "renderers", "owned reminders are compact, expandable, and sanitize terminal control content", func(t *testing.T) {
		u := newUIHost(t)
		content := "Checkpoint now\n" + strings.Repeat("remember\n", 50) + "LAST\x1b[2J\x1b]52;c;c2VjcmV0\x07\x00"
		m := u.message("posthorse-reminder", content, 2)
		if rows := len(uiLines(t, m, 24)); rows > 10 {
			t.Fatalf("%d rows at 24 columns", rows)
		}
		m.setExpanded(true)
		uiMatch(t, `LAST`, uiText(t, m, 80))
		// Reminder rendering must reject terminal erase/clipboard controls and C0 bytes.
		uiNoMatch(t, "\x1b\\[2J|\x1b\\]52|\x00", strings.Join(m.render(80), "\n"))
		blank := u.message("posthorse-reminder", []any{
			map[string]any{"type": "text", "text": "FIRST"},
			map[string]any{"type": "text", "text": ""},
			map[string]any{"type": "text", "text": "LAST"},
		}, 0)
		blank.setExpanded(true)
		uiMatch(t, `FIRST[ \t]*\n[ \t]*\n[ \t]*LAST`, uiText(t, blank, 80))
	})
}
