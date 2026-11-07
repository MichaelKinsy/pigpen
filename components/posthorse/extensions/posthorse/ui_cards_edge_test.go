package posthorse

// Edge states of the cards: error and partial results with display facts, host flags that differ between the call and result
// context, the legacy history preview, and values at the limits of the formatting (display.ts, cards.ts).

import (
	"math"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestUiResultCardEdges(t *testing.T) {
	th := sdk.UITheme{}
	f := func(v float64) *float64 { return &v }
	note := displayNoteRead(9, 0, 5, 5)
	for _, tc := range []struct {
		name      string
		f         *cardResultFacts
		collapsed []string
		expanded  []string
	}{
		// an error never sheds a note header: the text is shown whole
		{"note error", &cardResultFacts{name: "notes", raw: "File: /x\nerr", args: map[string]any{"op": "read"}, display: note, isError: true, expandKey: "ctrl+o"},
			[]string{"File: /x", "err"}, []string{"File: /x", "err"}},
		// an error does not strip the legacy history prefix
		{"history error", &cardResultFacts{name: "history", raw: "arch [window w] [id] [user] bad", args: map[string]any{"op": "read"}, isError: true, expandKey: "ctrl+o"},
			[]string{"arch [window w] [id] [user] bad"}, []string{"arch [window w] [id] [user] bad"}},
		// a legacy write result with no display facts still says there is more to see: the submitted content
		{"legacy write", &cardResultFacts{name: "notes", raw: "Wrote p", args: map[string]any{"op": "write", "content": "hi"}, expandKey: "ctrl+o"},
			[]string{"Wrote p", "… ctrl+o to expand"}, []string{"Wrote p", "", "Submitted content:", "hi"}},
		// a partial result is its text, whatever sections its facts could give
		{"partial history", &cardResultFacts{name: "history", raw: "H1 body one\nfoot", args: map[string]any{"op": "search"}, isPartial: true, expandKey: "ctrl+o",
			display: &posthorseDisplay{kind: "history-search", entries: []historySpan{{3, 11}}, footerLength: f(5), more: new(bool), skipped: f(0)}},
			[]string{"H1 body one", "foot"}, []string{"H1 body one", "foot"}},
		// a summary of many rows keeps five
		{"tall summary", &cardResultFacts{name: "get_context_remaining", raw: "t", args: map[string]any{}, expandKey: "ctrl+o",
			display: &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1000), usageWindow: 100000, usagePercent: f(1), rollover: "enabled", rolloverAt: f(91000)}},
			[]string{"≈90,000 tokens", "to rollover", "≈99,000 tokens", "to configured", "context limit", "… ctrl+o to..."}, []string{"t"}},
		// hidden images
		{"hidden image", &cardResultFacts{name: "notes", raw: "hello", args: map[string]any{"op": "read"}, imageCount: 1, expandKey: "ctrl+o"},
			[]string{"hello", "1 image attached (display hidden)"}, []string{"hello", "1 image attached (display hidden)"}},
	} {
		c := *tc.f
		width := 40
		if tc.name == "tall summary" {
			width = 14
		}
		uiSame(t, tc.name+" collapsed", uiPlain(cardResultComponent(&c, th, width)), tc.collapsed)
		c.expanded = true
		uiSame(t, tc.name+" expanded", uiPlain(cardResultComponent(&c, th, width)), tc.expanded)
	}
}

// Expanded history sections never carry terminal controls.
func TestUiExpandedSectionsAreSanitised(t *testing.T) {
	raw := "H\x1b[2J1 body\x00 one\nfoot"
	d := displayHistorySearch([]historySpan{{7, 16}}, 5, false, 0)
	rows := uiPlain(cardResultComponent(&cardResultFacts{name: "history", raw: raw, args: map[string]any{"op": "search"}, display: d, expanded: true, expandKey: "ctrl+o"}, sdk.UITheme{}, 40))
	uiSame(t, "sections", rows, []string{"body one", "H1", "", "foot"})
	for _, r := range cardResultComponent(&cardResultFacts{name: "history", raw: raw, args: map[string]any{"op": "search"}, display: d, expanded: true}, sdk.UITheme{}, 40) {
		if strings.ContainsAny(r, "\x00") || strings.Contains(r, "[2J") {
			t.Errorf("controls reach the card: %q", r)
		}
	}
}

// historySections splits a pair that a span boundary falls into, at the end of a span as at its header (display.ts:183-189).
func TestUiHistorySectionsSplitAtTheEnd(t *testing.T) {
	d := displayHistorySearch([]historySpan{{1, 2}, {0, 1}}, 0, false, 0)
	got := historySections("H😀b", d)
	if len(got) != 2 || got[0].header != "H" || got[0].body != "\xed\xa0\xbd" || got[1].header != "" || got[1].body != "b" {
		t.Errorf("sections %q", got)
	}
}

func TestUiNumberLimits(t *testing.T) {
	if got := n(math.Copysign(0, -1)); got != "-0" {
		t.Errorf("n(-0) = %q", got)
	}
	// a percent that rounds to -0 prints 0: `${Math.round(x)}` of -0 is "0"
	tokens, percent := 5.0, -0.4
	d := &posthorseDisplay{kind: "context", usage: true, usageTokens: &tokens, usageWindow: 10, usagePercent: &percent, rollover: "disabled"}
	if got := contextSummary(d); !strings.HasSuffix(got, "≈5 / 10 used (≈0%)") {
		t.Errorf("negative percent: %q", got)
	}
	if got := uiIntegerString(math.Copysign(0, -1)); got != "0" {
		t.Errorf("uiIntegerString(-0) = %q", got)
	}
}

// The tool renderers read the expansion and partial flags from the result options, the other flags from the context.
func TestUiRendererFlagsComeFromOptionsAndContext(t *testing.T) {
	u := newUIHost(t)
	result := uiResult("File: /x\nhello", map[string]any{"kind": "note-read", "headerLength": 9.0, "offset": 0.0, "end": 5.0, "total": 5.0})
	render := func(options, context map[string]any) []string {
		ctxMap := map[string]any{"toolCallId": "t", "executionStarted": true, "argsComplete": true, "showImages": true, "outputPad": 1}
		for k, v := range context {
			ctxMap[k] = v
		}
		rows, failure := u.h.RenderTool("notes", ToolRender{Card: "flags", Phase: "result", Args: map[string]any{"op": "read", "path": "a.md"}, Result: result, Options: options, Context: ctxMap, Width: 60})
		if failure != "" {
			t.Fatal(failure)
		}
		return uiPlain(rows)
	}
	// options say expanded and final, the context says collapsed and partial: the options win
	uiSame(t, "options", render(map[string]any{"expanded": true, "isPartial": false}, map[string]any{"expanded": false, "isPartial": true}), []string{"File: /x", "hello"})
	// options say collapsed and partial: a partial result is plain text with the hint
	uiSame(t, "partial", render(map[string]any{"expanded": false, "isPartial": true}, map[string]any{"expanded": true, "isPartial": false}), []string{"hello", "… ctrl+o to expand"})
	uiSame(t, "collapsed", render(map[string]any{"expanded": false, "isPartial": false}, map[string]any{"expanded": true, "isPartial": true}), []string{"5 chars · complete", "hello", "… ctrl+o to expand"})
	// the context's showImages and isError
	pic := map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello"}, map[string]any{"type": "image", "data": "x", "mimeType": "image/png"}}}
	rows, _ := u.h.RenderTool("notes", ToolRender{Card: "img", Phase: "result", Args: map[string]any{"op": "read", "path": "a.md"}, Result: pic,
		Options: map[string]any{}, Context: map[string]any{"showImages": false}, Width: 60})
	uiSame(t, "hidden", uiPlain(rows), []string{"hello", "1 image attached (display hidden)"})
	rows, _ = u.h.RenderTool("notes", ToolRender{Card: "img2", Phase: "result", Args: map[string]any{"op": "read", "path": "a.md"}, Result: pic,
		Options: map[string]any{}, Context: map[string]any{"showImages": true}, Width: 60})
	uiSame(t, "shown", uiPlain(rows), []string{"hello", "1 image attached"})
	rows, _ = u.h.RenderTool("notes", ToolRender{Card: "err", Phase: "result", Args: map[string]any{"op": "read", "path": "a.md"}, Result: uiResult("boom", nil),
		Options: map[string]any{}, Context: map[string]any{"isError": true}, Width: 60})
	uiSame(t, "error", uiPlain(rows), []string{"boom"})
	callRows, _ := u.h.RenderTool("notes", ToolRender{Card: "call", Phase: "call", Args: map[string]any{"op": "read", "path": "a.md"},
		Context: map[string]any{"isPartial": true, "executionStarted": true, "expanded": false}, Width: 60})
	uiSame(t, "call context", uiPlain(callRows), []string{"… Notes · read · running · a.md"})
}

func TestUiApplyToolCardsLeavesTheDefaultShell(t *testing.T) {
	for _, name := range []string{"notes", "history", "get_context_remaining", "new_context"} {
		def := sdk.ToolDefinition{Name: name}
		applyToolCards(&def, name)
		if def.RenderShell == sdk.ToolRenderShellSelf || def.RenderCall == nil || def.RenderResult == nil {
			t.Errorf("%s: shell %q call %v result %v", name, def.RenderShell, def.RenderCall != nil, def.RenderResult != nil)
		}
	}
}

// A collapsed history search shows each match on one row: its whitespace runs become single blanks.
func TestUiSearchPreviewsAreOneRowEach(t *testing.T) {
	raw := "H1 a\n  b\tc\nH2 d"
	d := displayHistorySearch([]historySpan{{3, 10}, {3, 4}}, 0, false, 0)
	rows := uiPlain(cardResultComponent(&cardResultFacts{name: "history", raw: raw, args: map[string]any{"op": "search"}, display: d, expandKey: "ctrl+o"}, sdk.UITheme{}, 40))
	uiSame(t, "previews", rows, []string{"2 matches · current branch", "a b c", "d", "… ctrl+o to expand"})
}
