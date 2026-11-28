package hashline

import (
	"fmt"
	"strings"
	"testing"
)

// Tests ported from crates/edit/src/text.rs, crates/edit/src/modes/hashline/messages.rs,
// crates/edit/src/store.rs, and crates/edit/tests/hashline_parse.rs.

// upstream: crates/edit/src/text.rs: "line_ending_detection_prefers_first_terminator"
func TestLineEndingDetectionPrefersFirstTerminator(t *testing.T) {
	if DetectLineEnding("a\r\nb\n") != LineEndingCRLF {
		t.Fatal("expected CRLF")
	}
	if DetectLineEnding("a\nb\r\n") != LineEndingLF {
		t.Fatal("expected LF")
	}
	if DetectLineEnding("abc") != LineEndingLF {
		t.Fatal("expected LF")
	}
}

// upstream: crates/edit/src/text.rs: "lf_normalization_round_trips"
func TestLFNormalizationRoundTrips(t *testing.T) {
	// Lone \r stays content; only \r\n normalizes to \n.
	if got := NormalizeToLF("a\r\nb\rc\n"); got != "a\nb\rc\n" {
		t.Fatalf("NormalizeToLF = %q, want %q", got, "a\nb\rc\n")
	}
	if got := NormalizeToLF("\r\r\n界\r"); got != "\r\n界\r" {
		t.Fatalf("NormalizeToLF = %q, want %q", got, "\r\n界\r")
	}
	if got := RestoreLineEndings("a\nb", LineEndingCRLF); got != "a\r\nb" {
		t.Fatalf("RestoreLineEndings = %q, want %q", got, "a\r\nb")
	}
	bom, rest := StripBOM("\uFEFFx")
	if bom != BOM || rest != "x" {
		t.Fatalf("StripBOM = (%q, %q)", bom, rest)
	}
}

// upstream: crates/edit/src/store.rs: "file_hash_matches_typescript"
func TestFileHashMatchesTypeScript(t *testing.T) {
	cases := [][2]string{
		{"a \n b\t\r\nc", "80BA"},
		{"hello\n", "5BF9"},
		{"", "5D05"},
	}
	for _, tc := range cases {
		if got := FileHash(tc[0]); got != tc[1] {
			t.Fatalf("FileHash(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

// upstream: crates/edit/src/store.rs: "file_hash_streams_the_normalized_text"
func TestFileHashStreamsNormalizedText(t *testing.T) {
	refHash := func(text string) string {
		var b strings.Builder
		start := 0
		for start < len(text) {
			nl := strings.IndexByte(text[start:], '\n')
			var seg, line, suffix string
			if nl >= 0 {
				seg = text[start : start+nl+1]
				line = text[start : start+nl]
				suffix = "\n"
			} else {
				seg = text[start:]
				line = seg
			}
			b.WriteString(strings.TrimRight(line, " \t\r"))
			b.WriteString(suffix)
			start += len(seg)
		}
		h := newXXH32(0)
		h.update([]byte(b.String()))
		return fmt.Sprintf("%04X", h.digest()&0xffff)
	}
	longLine := strings.Repeat("x", 100)
	var rows []string
	suffixes := []string{"", " ", "\t", "\r", " \r"}
	for n := range 200 {
		rows = append(rows, fmt.Sprintf("line %d%s", n, suffixes[n%5]))
	}
	cases := []string{
		"",
		"\n",
		"\r\n",
		" \t\r",
		"no final newline",
		"no final newline with trailing space \t",
		"crlf\r\nlines\r\nend\r\n",
		"crlf without final\r\nnewline\r",
		"trailing  \nwhitespace\t\t\n  only indent kept\n   \n\t\n",
		"lone\rcarriage\r\rreturns \r\r\n",
		"mixed 😀 \t\r\nünïcödé  \n中文\r",
		fmt.Sprintf("%s \n%s\r\n%s\n%s\t", longLine, longLine, longLine, longLine),
		strings.Join(rows, "\n"),
	}
	for _, text := range cases {
		if got, want := FileHash(text), refHash(text); got != want {
			t.Fatalf("FileHash(%q) = %q, want %q", text, got, want)
		}
	}
}

// upstream: crates/edit/src/modes/hashline/messages.rs: "test_format_anchored_context"
func TestFormatAnchoredContext(t *testing.T) {
	lines := []string{
		"alpha", "bravo", "charlie", "delta", "echo",
		"foxtrot", "golf", "hotel", "india", "juliet",
	}
	got := FormatAnchoredContext([]uint32{2, 8}, lines)
	want := []string{
		" 1:alpha",
		"*2:bravo",
		" 3:charlie",
		" 4:delta",
		"...",
		" 6:foxtrot",
		" 7:golf",
		"*8:hotel",
		" 9:india",
		" 10:juliet",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if oor := FormatAnchoredContext([]uint32{999}, lines); len(oor) != 0 {
		t.Fatalf("expected empty for OOR anchor, got %v", oor)
	}
}

// upstream: crates/edit/src/modes/hashline/messages.rs: "test_invalid_absolute_range_message"
func TestInvalidAbsoluteRangeMessage(t *testing.T) {
	msg := InvalidAbsoluteRangeMessage(12, 5, 3, AbsoluteRangeReplace, &BlockSpan{Start: 5, End: 10}, "", false)
	want := "line 12: Invalid absolute range: start 5, end 3. The value after `.=` is an absolute source line, not a line count or replacement length. For one line use `PUT 5:`. For 3 lines starting at 5, use `PUT 5.=7:`. The syntactic block beginning at 5 ends at 10, so `PUT 5*:` is also valid."
	if msg != want {
		t.Fatalf("got:\n%s\nwant:\n%s", msg, want)
	}
	msgCut := InvalidAbsoluteRangeMessage(10, 4, 1, AbsoluteRangeCut, nil, "clip", true)
	wantCut := "line 10: Invalid absolute range: start 4, end 1. The value after `.=` is an absolute source line, not a line count or replacement length. For one line use `CUT 4 @clip`. For 1 lines starting at 4, use `CUT 4.=4 @clip`."
	if msgCut != wantCut {
		t.Fatalf("got:\n%s\nwant:\n%s", msgCut, wantCut)
	}
}

// upstream: crates/edit/src/modes/hashline/messages.rs: "test_empty_register_paste_warning"
func TestEmptyRegisterPasteWarning(t *testing.T) {
	if got, want := EmptyRegisterPasteWarning("temp", nil), "`@temp` was empty — no `CUT … @temp` precedes this op in this call and no persisted register has that name — so nothing was pasted."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := EmptyRegisterPasteWarning("temp", []string{"foo", "bar"}), "`@temp` was empty — no `CUT … @temp` precedes this op in this call and no persisted register has that name — so nothing was pasted. Available registers: `@foo`, `@bar`."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// upstream: crates/edit/src/modes/hashline/messages.rs: "test_unseen_lines_message"
func TestUnseenLinesMessage(t *testing.T) {
	msgEmpty := UnseenLinesMessage("src/foo.ts", []uint32{5, 6, 7, 10}, "AB12", UnseenLinesReveal{})
	wantEmpty := "This edit anchors to lines 5-7, 10 of src/foo.ts that [src/foo.ts#AB12] never displayed (it showed a partial range, a search hit, or a folded summary). Re-read them in full first with a ranged read like `src/foo.ts:5-7,10` — it skips summarization and mints a fresh tag (a plain re-read just re-folds them) — then re-issue the edit."
	if msgEmpty != wantEmpty {
		t.Fatalf("got:\n%s\nwant:\n%s", msgEmpty, wantEmpty)
	}
	msgReveal := UnseenLinesMessage("src/foo.ts", []uint32{5}, "AB12", UnseenLinesReveal{
		Lines: []RevealedLine{{Line: 5, Text: "let x = 1;"}},
	})
	wantReveal := "This edit anchors to lines 5 of src/foo.ts that [src/foo.ts#AB12] never displayed (it showed a partial range, a search hit, or a folded summary). Actual file content at those lines:\n  5:let x = 1;\nVerify the content matches what you intend to touch, then re-issue the edit with the same [path#tag] header — a straight retry now succeeds without a re-read. If the content does NOT match, fix your line numbers."
	if msgReveal != wantReveal {
		t.Fatalf("got:\n%s\nwant:\n%s", msgReveal, wantReveal)
	}
	msgTrunc := UnseenLinesMessage("src/foo.ts", []uint32{5, 6}, "AB12", UnseenLinesReveal{
		Lines:     []RevealedLine{{Line: 5, Text: "let x = 1;"}},
		Truncated: true,
	})
	wantTrunc := "This edit anchors to lines 5-6 of src/foo.ts that [src/foo.ts#AB12] never displayed (it showed a partial range, a search hit, or a folded summary). Preview of the actual file content at the first 1 unseen line(s):\n  5:let x = 1;\nThe range exceeds the inline preview cap — re-read the remainder with `src/foo.ts:5-6` before re-issuing the edit."
	if msgTrunc != wantTrunc {
		t.Fatalf("got:\n%s\nwant:\n%s", msgTrunc, wantTrunc)
	}
}

// upstream: crates/edit/src/modes/hashline/messages.rs: "test_block_single_line_message"
func TestBlockSingleLineMessage(t *testing.T) {
	msg := BlockSingleLineMessage(15, BlockOpReplace, nil)
	want := "`PUT 15*:` resolved a single-line block — line 15 is a bare statement, not the opening line of a multi-line construct. For only this statement use `PUT 15:`."
	if msg != want {
		t.Fatalf("got %q, want %q", msg, want)
	}
	msgEnc := BlockSingleLineMessage(15, BlockOpInsertAfter, &BlockSpan{Start: 10, End: 20})
	wantEnc := "`PUT >15*:` resolved a single-line block — line 15 is a bare statement, not the opening line of a multi-line construct. For only this statement use `PUT >15:`. The nearest enclosing multi-line block begins at line 10 and ends at line 20; use `PUT >10*:` to target it."
	if msgEnc != wantEnc {
		t.Fatalf("got %q, want %q", msgEnc, wantEnc)
	}
}

// upstream: crates/edit/src/modes/hashline/messages.rs: "test_json_quote"
func TestJSONQuote(t *testing.T) {
	if got, want := JSONQuote("hello \"world\"\n\t\\"), `"hello \"world\"\n\t\\"`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
