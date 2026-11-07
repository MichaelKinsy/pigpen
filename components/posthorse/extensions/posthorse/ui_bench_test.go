package posthorse

// Benchmarks of the cards on the page sizes the tools produce (a note page is at most 40,000 characters,
// maxPageChars). The theme is the zero UITheme (modifiers only), which leaves the per-style ANSI wrapping out; the
// wrapping adds a constant number of small allocations per styled string.

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func benchNoteFacts(expanded bool) *cardResultFacts {
	body := "START " + strings.Repeat("界 👩🏽‍💻 é route details ", 1800)
	body = u16slice(body, 0, 40_000-30)
	header := "File: /work/.pi/notes/ledger.md\n"
	return &cardResultFacts{
		name: "notes", raw: header + body, args: map[string]any{"op": "read", "path": "ledger.md"},
		display:  displayNoteRead(u16len(header), 0, u16len(body), 56_186),
		expanded: expanded, expandKey: "ctrl+o",
	}
}

func benchHistoryFacts(expanded bool) (*cardResultFacts, int) {
	var entries []string
	var spans []historySpan
	for i := range 40 {
		header := "2026-09-06T17:43:46Z [window w1] [entry-" + itoaBench(i) + "] [user] "
		part := header + "Relay station " + itoaBench(i) + ": " + strings.Repeat("route details ", 36)
		entries = append(entries, part)
		spans = append(spans, historySpan{headerLength: float64(u16len(header)), length: float64(u16len(part))})
	}
	raw := strings.Join(entries, "\n")
	return &cardResultFacts{
		name: "history", raw: raw, args: map[string]any{"op": "search", "query": "Relay", "all": true},
		display:  displayHistorySearch(spans, 0, false, 0),
		expanded: expanded, expandKey: "ctrl+o",
	}, u16len(raw)
}

func itoaBench(n int) string { return cardItoa(n) }

func BenchmarkCardNotePage40k(b *testing.B) {
	theme := sdk.UITheme{}
	for _, expanded := range []bool{false, true} {
		name := "collapsed"
		if expanded {
			name = "expanded"
		}
		facts := benchNoteFacts(expanded)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if rows := cardResultComponent(facts, theme, 80); len(rows) == 0 {
					b.Fatal("no rows")
				}
			}
		})
	}
}

func BenchmarkCardHistoryPage(b *testing.B) {
	theme := sdk.UITheme{}
	for _, expanded := range []bool{false, true} {
		name := "collapsed"
		if expanded {
			name = "expanded"
		}
		facts, _ := benchHistoryFacts(expanded)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if rows := cardResultComponent(facts, theme, 80); len(rows) == 0 {
					b.Fatal("no rows")
				}
			}
		})
	}
}

func BenchmarkCardMessage40k(b *testing.B) {
	theme := sdk.UITheme{}
	body := "Checkpoint now\n" + strings.Repeat("remember this route "+"界 👩🏽‍💻 é ", 1800)
	for _, expanded := range []bool{false, true} {
		name := "collapsed"
		if expanded {
			name = "expanded"
		}
		message := map[string]any{"customType": "posthorse-reminder", "content": body}
		options := sdk.MessageRenderOptions{Expanded: expanded, OutputPad: 1}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if rows := cardMessageRows(theme, message, options, 80); len(rows) == 0 {
					b.Fatal("no rows")
				}
			}
		})
	}
}

func BenchmarkDisplayOf(b *testing.B) {
	details := map[string]any{
		"kind": "history-search", "footerLength": float64(0), "more": false, "skipped": float64(0),
		"entries": []any{map[string]any{"headerLength": float64(60), "length": float64(600)}, map[string]any{"headerLength": float64(60), "length": float64(600)}},
	}
	b.ReportAllocs()
	for range b.N {
		if displayOf(details) == nil {
			b.Fatal("invalid")
		}
	}
}
