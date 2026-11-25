package hashline

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// Tests ported from crates/edit/tests/hashline_parse.rs and crates/edit/src/modes/hashline/mod.rs.

// upstream: crates/edit/tests/hashline_parse.rs: "formats_hashline_v4_rows_and_headers"
func TestFormatsHashlineV4RowsAndHeaders(t *testing.T) {
	if got := FormatHeader("src/a.ts", "1A2B"); got != "[src/a.ts#1A2B]" {
		t.Fatalf("got %q", got)
	}
	if got := FormatReplaceHeader(5, 9); got != "PUT 5.=9:" {
		t.Fatalf("got %q", got)
	}
	if got := FormatCutHeader(5, 9); got != "CUT 5.=9" {
		t.Fatalf("got %q", got)
	}
	if got := FormatNumberedLine(7, "hello"); got != "7:hello" {
		t.Fatalf("got %q", got)
	}
	if got := FormatNumberedLines("a\nb\n", 4); got != "4:a\n5:b\n6:" {
		t.Fatalf("got %q", got)
	}
	if got := SplitAddressableFileLines("a\nb\n"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := SplitAddressableFileLines(""); !slices.Equal(got, []string{""}) {
		t.Fatalf("got %v", got)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "numbered_empty_text_keeps_its_display_row"
func TestNumberedEmptyTextKeepsDisplayRow(t *testing.T) {
	if got := FormatNumberedLinesU32("", 0); got != "0:" {
		t.Fatalf("got %q", got)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "numbered_rows_preserve_payload_and_saturate_at_u32_max"
func TestNumberedRowsPreservePayloadAndSaturateAtU32Max(t *testing.T) {
	got := FormatNumberedLinesU32("a\r\n\n\u03bb\x00\n", math.MaxUint32-1)
	want := "4294967294:a\r\n4294967295:\n4294967295:\u03bb\x00\n4294967295:"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "tokenizer_recognizes_headers_ops_payloads_and_envelope"
func TestTokenizerRecognizesHeadersOpsPayloadsAndEnvelope(t *testing.T) {
	tok := NewTokenizer()
	tokens, err := tok.TokenizeAll("*** Begin Patch\n[a b.ts#1a2b]\nPUT 2.=3:\n+x\nCUT 5*\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 6 {
		t.Fatalf("len=%d", len(tokens))
	}
	if tokens[0].Kind != TokenEnvelopeBegin || tokens[0].LineNum != 1 {
		t.Fatalf("tok0=%+v", tokens[0])
	}
	if tokens[1].Kind != TokenHeader || tokens[1].Path != "a b.ts" || tokens[1].FileHash != "1A2B" {
		t.Fatalf("tok1=%+v", tokens[1])
	}
	if tokens[2].Kind != TokenOpBlock || tokens[2].Target.Kind != TargetReplace || !tokens[2].HadColon {
		t.Fatalf("tok2=%+v", tokens[2])
	}
	if tokens[3].Kind != TokenPayloadLiteral || tokens[3].Text != "x" {
		t.Fatalf("tok3=%+v", tokens[3])
	}
	if tokens[4].Kind != TokenOpBlock || tokens[4].Target.Kind != TargetCutBlock {
		t.Fatalf("tok4=%+v", tokens[4])
	}
	if tokens[5].Kind != TokenEnvelopeEnd {
		t.Fatalf("tok5=%+v", tokens[5])
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "tokenizer_streams_crlf_and_final_lines"
func TestTokenizerStreamsCRLFAndFinalLines(t *testing.T) {
	tok := NewTokenizer()
	t1, err := tok.Feed("PUT 2:\r")
	if err != nil || len(t1) != 0 {
		t.Fatalf("t1=%v err=%v", t1, err)
	}
	t2, err := tok.Feed("\n+x")
	if err != nil || len(t2) != 1 || t2[0].Kind != TokenOpBlock || t2[0].LineNum != 1 {
		t.Fatalf("t2=%+v err=%v", t2, err)
	}
	t3 := tok.End()
	if len(t3) != 1 || t3[0].Kind != TokenPayloadLiteral || t3[0].LineNum != 2 || t3[0].Text != "x" {
		t.Fatalf("t3=%+v", t3)
	}
	if got := SplitHashlineLines("a\r\nb\n"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "parses_lids_and_rejects_unsafe_values"
func TestParsesLidsAndRejectsUnsafeValues(t *testing.T) {
	a, err := ParseLid(" 42 ", 3)
	if err != nil || a.Line != 42 {
		t.Fatalf("got %+v err=%v", a, err)
	}
	_, err = ParseLid("0", 3)
	if err == nil || !strings.Contains(err.Error(), "line 3: expected a line number") {
		t.Fatalf("unexpected err: %v", err)
	}
	if NewTokenizer().IsOp("PUT 9007199254740992.=9007199254740992:") {
		t.Fatal("should reject unsafe huge number")
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "parses_literal_replace_in_textual_order"
func TestParsesLiteralReplaceInTextualOrder(t *testing.T) {
	parsed, err := ParsePatch("PUT 2.=3:\n+B\n+C")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Edits) != 4 {
		t.Fatalf("len=%d", len(parsed.Edits))
	}
	if parsed.Edits[0].Kind != EditInsert || parsed.Edits[0].Cursor.Anchor.Line != 2 || parsed.Edits[0].Text != "B" || !parsed.Edits[0].Replacement {
		t.Fatalf("e0=%+v", parsed.Edits[0])
	}
	if parsed.Edits[1].Kind != EditInsert || parsed.Edits[1].Text != "C" {
		t.Fatalf("e1=%+v", parsed.Edits[1])
	}
	if parsed.Edits[2].Kind != EditDelete || parsed.Edits[2].Anchor.Line != 2 {
		t.Fatalf("e2=%+v", parsed.Edits[2])
	}
	if parsed.Edits[3].Kind != EditDelete || parsed.Edits[3].Anchor.Line != 3 {
		t.Fatalf("e3=%+v", parsed.Edits[3])
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "accepts_lenient_range_separators_and_dangling_ranges"
func TestAcceptsLenientRangeSeparatorsAndDanglingRanges(t *testing.T) {
	for _, hdr := range []string{"PUT 2-3:", "PUT 2=3:", "PUT 2..3:", "PUT 2…3:", "PUT 2 3:"} {
		parsed, err := ParsePatch(hdr + "\n+X")
		if err != nil {
			t.Fatalf("%s: %v", hdr, err)
		}
		dels := 0
		for _, e := range parsed.Edits {
			if e.Kind == EditDelete {
				dels++
			}
		}
		if dels != 2 {
			t.Fatalf("%s: deletes=%d, want 2", hdr, dels)
		}
	}
	for _, hdr := range []string{"PUT 2.=:", "PUT 2-:"} {
		parsed, err := ParsePatch(hdr + "\n+X")
		if err != nil || len(parsed.Edits) != 2 {
			t.Fatalf("%s: err=%v edits=%d", hdr, err, len(parsed.Edits))
		}
	}
	if _, err := ParsePatch("PUT 2.= junk:\n+X"); err == nil {
		t.Fatal("expected error for junk after separator")
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "parses_all_gap_and_block_targets"
func TestParsesAllGapAndBlockTargets(t *testing.T) {
	parsed, err := ParsePatch("PUT <2:\n+a\nPUT >3:\n+b\nPUT <1:\n+c\nPUT >$:\n+d\nPUT 4*:\n+e\nPUT >5*:\n+f")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Edits) != 6 {
		t.Fatalf("len=%d", len(parsed.Edits))
	}
	if parsed.Edits[0].Cursor.Kind != CursorBeforeAnchor || parsed.Edits[0].Cursor.Anchor.Line != 2 {
		t.Fatalf("e0=%+v", parsed.Edits[0])
	}
	if parsed.Edits[1].Cursor.Kind != CursorAfterAnchor || parsed.Edits[1].Cursor.Anchor.Line != 3 {
		t.Fatalf("e1=%+v", parsed.Edits[1])
	}
	if parsed.Edits[2].Cursor.Kind != CursorBof {
		t.Fatalf("e2=%+v", parsed.Edits[2])
	}
	if parsed.Edits[3].Cursor.Kind != CursorEof {
		t.Fatalf("e3=%+v", parsed.Edits[3])
	}
	if parsed.Edits[4].Kind != EditBlock || parsed.Edits[4].Anchor.Line != 4 || parsed.Edits[4].BlockMode != BlockModeReplace {
		t.Fatalf("e4=%+v", parsed.Edits[4])
	}
	if parsed.Edits[5].Kind != EditBlock || parsed.Edits[5].Anchor.Line != 5 || parsed.Edits[5].BlockMode != BlockModeInsertAfter {
		t.Fatalf("e5=%+v", parsed.Edits[5])
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "parses_cut_clipboard_and_register_pastes"
func TestParsesCutClipboardAndRegisterPastes(t *testing.T) {
	parsed, err := ParsePatch("CUT 2.=3 @hold\nPUT <1 @hold\nPUT >$\nPUT 4.=5 @hold")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Edits[0].Kind != EditCut || parsed.Edits[0].Register != "hold" {
		t.Fatalf("e0=%+v", parsed.Edits[0])
	}
	dels := 0
	for _, e := range parsed.Edits {
		if e.Kind == EditDelete {
			dels++
		}
	}
	if dels != 2 {
		t.Fatalf("dels=%d", dels)
	}
	if parsed.Edits[3].Kind != EditPaste || parsed.Edits[3].PasteAt.Cursor.Kind != CursorBof || parsed.Edits[3].Register != "hold" {
		t.Fatalf("e3=%+v", parsed.Edits[3])
	}
	if parsed.Edits[4].Kind != EditPaste || parsed.Edits[4].PasteAt.Cursor.Kind != CursorEof || parsed.Edits[4].HasRegister {
		t.Fatalf("e4=%+v", parsed.Edits[4])
	}
	if parsed.Edits[5].Kind != EditPaste || parsed.Edits[5].PasteAt.Kind != PasteTargetSpan || parsed.Edits[5].PasteAt.Range.Start.Line != 4 || parsed.Edits[5].PasteAt.Range.End.Line != 5 {
		t.Fatalf("e5=%+v", parsed.Edits[5])
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "rejects_body_rows_for_bodyless_ops"
func TestRejectsBodyRowsForBodylessOps(t *testing.T) {
	cases := [][2]string{
		{"CUT 2\n+replacement", "takes no body rows"},
		{"PUT >2 @name:", "never takes `:`"},
		{"PUT <2\n+X", "without `:` is clipboard-backed"},
	}
	for _, tc := range cases {
		_, err := ParsePatch(tc[0])
		if err == nil || !strings.Contains(err.Error(), tc[1]) {
			t.Fatalf("%q: err=%v, want %q", tc[0], err, tc[1])
		}
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "empty_put_deletes_but_empty_insert_is_rejected"
func TestEmptyPutDeletesButEmptyInsertIsRejected(t *testing.T) {
	parsed, err := ParsePatch("PUT 2.=3:")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Edits) != 2 || !slices.ContainsFunc(parsed.Warnings, func(w string) bool { return strings.Contains(w, "empty `PUT` body as deletion") }) {
		t.Fatalf("parsed=%+v", parsed)
	}
	_, pErr := ParsePatch("PUT >$:")
	if pErr == nil || !strings.Contains(pErr.Error(), "promises body rows") {
		t.Fatalf("err=%v", pErr)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "reports_invalid_absolute_ranges_and_limits"
func TestReportsInvalidAbsoluteRangesAndLimits(t *testing.T) {
	_, err := ParsePatch("CUT 5.=2")
	if err == nil || err.InvalidRange == nil {
		t.Fatalf("expected InvalidRange, got %v", err)
	}
	if err.InvalidRange.Op != AbsoluteRangeCut {
		t.Fatalf("op=%v", err.InvalidRange.Op)
	}
	msg := err.InvalidRange.Message()
	if !strings.Contains(msg, "For one line use `CUT 5`") || !strings.Contains(msg, "For 2 lines starting at 5, use `CUT 5.=6`") {
		t.Fatalf("msg=%q", msg)
	}
	_, err2 := ParsePatch("PUT 1.=100001:\n+x")
	if err2 == nil || !strings.Contains(err2.Error(), "maximum is 100000") {
		t.Fatalf("err2=%v", err2)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "recovers_bare_snapshot_rows_and_rejects_duplicates"
func TestRecoversBareSnapshotRowsAndRejectsDuplicates(t *testing.T) {
	parsed, err := ParsePatch("2:B\n4|D")
	if err != nil || len(parsed.Edits) != 4 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
	if !slices.ContainsFunc(parsed.Warnings, func(w string) bool { return strings.Contains(w, "snapshot row") }) {
		t.Fatalf("warnings=%v", parsed.Warnings)
	}
	_, err2 := ParsePatch("2:B\n4:first\n4:second")
	if err2 == nil || !strings.Contains(err2.Error(), "name line 4") || !strings.Contains(err2.Error(), "keep only the last row") {
		t.Fatalf("err2=%v", err2)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "bare_body_prefix_stripping_is_uniform_and_single_pass"
func TestBareBodyPrefixStrippingIsUniformAndSinglePass(t *testing.T) {
	collectInserts := func(p Parsed) []string {
		var out []string
		for _, e := range p.Edits {
			if e.Kind == EditInsert {
				out = append(out, e.Text)
			}
		}
		return out
	}
	p1, err := ParsePatch("PUT 2.=3:\n2:foo\n3:bar")
	if err != nil || !slices.Equal(collectInserts(p1), []string{"foo", "bar"}) {
		t.Fatalf("p1=%v err=%v", collectInserts(p1), err)
	}
	p2, err := ParsePatch("PUT 2.=3:\n3:keep\nplain")
	if err != nil || !slices.Equal(collectInserts(p2), []string{"3:keep", "plain"}) {
		t.Fatalf("p2=%v err=%v", collectInserts(p2), err)
	}
	p3, err := ParsePatch("PUT 2:\n2:42:hello")
	if err != nil || !slices.Equal(collectInserts(p3), []string{"42:hello"}) {
		t.Fatalf("p3=%v err=%v", collectInserts(p3), err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "preserves_numeric_keyed_bodies"
func TestPreservesNumericKeyedBodies(t *testing.T) {
	p, err := ParsePatch("PUT 2.=3:\n1: \"one\",\n2: \"two\",")
	if err != nil {
		t.Fatal(err)
	}
	if p.Edits[0].Text != "1: \"one\"," || p.Edits[1].Text != "2: \"two\"," {
		t.Fatalf("edits=%+v", p.Edits)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "handles_minus_rows_and_markdown_bullets"
func TestHandlesMinusRowsAndMarkdownBullets(t *testing.T) {
	if _, err := ParsePatch("PUT 2:\n-old"); err == nil || !strings.Contains(err.Error(), "`-` rows are not valid") {
		t.Fatalf("err=%v", err)
	}
	p1, err := ParsePatch("PUT 2:\n- item\n  - nested")
	if err != nil || !slices.ContainsFunc(p1.Warnings, func(w string) bool { return strings.Contains(w, "bullet row") }) {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := ParsePatch("PUT 2:\n-old\n+new")
	if err != nil || !slices.ContainsFunc(p2.Warnings, func(w string) bool { return strings.Contains(w, "Ignored unified-diff `-old`") }) || p2.Edits[0].Text != "new" {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "warns_when_literal_payload_is_an_op"
func TestWarnsWhenLiteralPayloadIsAnOp(t *testing.T) {
	p, err := ParsePatch("PUT >1:\n+inserted();\n+CUT 2.=3")
	if err != nil || !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "itself a valid hunk header") }) {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "rejects_contaminated_patch_syntax"
func TestRejectsContaminatedPatchSyntax(t *testing.T) {
	cases := [][2]string{
		{"*** Update File: a.ts\nPUT 2:\n+X", "apply_patch sentinel"},
		{"@@ -1,3 +1,3 @@\nPUT 2:\n+X", "unified-diff hunk header"},
		{"2\n+B", "hunk headers need a verb"},
		{"2 3\n+X", "Hunk headers need a verb"},
	}
	for _, tc := range cases {
		_, err := ParsePatch(tc[0])
		if err == nil || !strings.Contains(err.Error(), tc[1]) {
			t.Fatalf("%q: err=%v want %q", tc[0], err, tc[1])
		}
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "recovers_bare_range_header_as_implicit_put"
func TestRecoversBareRangeHeaderAsImplicitPut(t *testing.T) {
	p, err := ParsePatch("2.=3:\n+X")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "bare `N.=M:` header") }) {
		t.Fatalf("warnings=%v", p.Warnings)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "ignores_copied_read_elisions"
func TestIgnoresCopiedReadElisions(t *testing.T) {
	p, err := ParsePatch("1:a\n2-3: omitted() { … }\n4:d\n[…2ln elided; re-read needed ranges with a.ts:2-3]")
	if err != nil {
		t.Fatal(err)
	}
	inserts := 0
	for _, e := range p.Edits {
		if e.Kind == EditInsert {
			inserts++
		}
	}
	if inserts != 2 || !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "Ignored copied read-output elision") }) {
		t.Fatalf("inserts=%d warnings=%v", inserts, p.Warnings)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "accepts_harmless_cut_colon_with_warning"
func TestAcceptsHarmlessCutColonWithWarning(t *testing.T) {
	p, err := ParsePatch("CUT 2.=3:")
	if err != nil || !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "Ignored a trailing `:`") }) {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "cut_supersedes_placeholder_put_on_exact_range"
func TestCutSupersedesPlaceholderPutOnExactRange(t *testing.T) {
	p, err := ParsePatch("PUT 2.=3:\n+// moved block removed\nCUT 2.=3 @block")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range p.Edits {
		if e.LineNum == 1 {
			t.Fatalf("line 1 edit should have been dropped: %+v", e)
		}
	}
	if !slices.ContainsFunc(p.Edits, func(e Edit) bool { return e.Kind == EditCut && e.Register == "block" }) {
		t.Fatalf("missing CUT @block: %+v", p.Edits)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "preserves_explicit_and_interior_blank_payload_rows"
func TestPreservesExplicitAndInteriorBlankPayloadRows(t *testing.T) {
	extract := func(p Parsed) []string {
		var out []string
		for _, e := range p.Edits {
			if e.Kind == EditInsert {
				out = append(out, e.Text)
			}
		}
		return out
	}
	p1, err := ParsePatch("PUT 2:\n+\n+\nPUT 4:\n+D")
	if err != nil || !slices.Equal(extract(p1), []string{"", "", "D"}) {
		t.Fatalf("p1=%v err=%v", extract(p1), err)
	}
	p2, err := ParsePatch("PUT 2:\nfirst\n\nsecond")
	if err != nil || !slices.Equal(extract(p2), []string{"first", "", "second"}) {
		t.Fatalf("p2=%v err=%v", extract(p2), err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "drops_trailing_bare_blank_before_next_hunk"
func TestDropsTrailingBareBlankBeforeNextHunk(t *testing.T) {
	p, err := ParsePatch("PUT 2:\nfoo\n\nPUT 4:\nbaz")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range p.Edits {
		if e.Kind == EditInsert {
			out = append(out, e.Text)
		}
	}
	if !slices.Equal(out, []string{"foo", "baz"}) {
		t.Fatalf("got %v", out)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "abort_terminates_parser_without_warning"
func TestAbortTerminatesParserWithoutWarning(t *testing.T) {
	p, err := ParsePatch("PUT >1:\n+HELLO\n*** Abort\nPUT >99:\n+never")
	if err != nil || len(p.Edits) != 1 || p.Edits[0].Text != "HELLO" || len(p.Warnings) != 0 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "abort_stops_input_before_later_sections"
func TestAbortStopsInputBeforeLaterSections(t *testing.T) {
	patch, err := ParsePatchInput("[a.ts]\nPUT >1:\n+a\n*** Abort\n[b.ts]\nPUT >1:\n+never", SplitOptions{})
	if err != nil || len(patch.Sections) != 1 || patch.Sections[0].Path != "a.ts" || strings.Contains(patch.Sections[0].Diff, "never") {
		t.Fatalf("patch=%+v err=%v", patch, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "removed_del_and_copy_headers_are_orphan_rows"
func TestRemovedDelAndCopyHeadersAreOrphanRows(t *testing.T) {
	for _, hdr := range []string{"DEL 2", "DEL.BLK 2", "COPY 2", "COPY.BLK 2"} {
		_, err := ParsePatch(hdr)
		if err == nil || !strings.Contains(err.Error(), "payload line has no preceding hunk header") {
			t.Fatalf("%q: err=%v", hdr, err)
		}
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "detects_and_coalesces_overlapping_hunks"
func TestDetectsAndCoalescesOverlappingHunks(t *testing.T) {
	_, err := ParsePatch("PUT 2.=4:\n+A\nPUT 3.=5:\n+B")
	if err == nil || !strings.Contains(err.Error(), "anchor line 3 is already targeted by another hunk on line 1") {
		t.Fatalf("err=%v", err)
	}
	p, err := ParsePatch("2:b\nPUT 2:\n+B")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "kept only the last") }) {
		t.Fatalf("warnings=%v", p.Warnings)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "streaming_drops_incomplete_empty_body_but_keeps_bodyless_ops"
func TestStreamingDropsIncompleteEmptyBodyButKeepsBodylessOps(t *testing.T) {
	p1, err := ParsePatchStreaming("PUT 2.=")
	if err != nil || len(p1.Edits) != 0 {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := ParsePatchStreaming("CUT 1\nPUT >$")
	if err != nil || len(p2.Edits) != 3 || p2.Edits[0].Kind != EditCut || p2.Edits[1].Kind != EditDelete || p2.Edits[2].Kind != EditPaste {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "parses_file_ops_and_rejects_invalid_combinations"
func TestParsesFileOpsAndRejectsInvalidCombinations(t *testing.T) {
	p1, err := ParsePatch("REM")
	if err != nil || p1.FileOp == nil || p1.FileOp.Kind != FileOpDelete {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := ParsePatch("MV next.ts")
	if err != nil || p2.FileOp == nil || p2.FileOp.Kind != FileOpMove || p2.FileOp.Dest != "next.ts" {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
	p3, err := ParsePatch("MV 'q r.sh'")
	if err != nil || p3.FileOp == nil || p3.FileOp.Dest != "q r.sh" {
		t.Fatalf("p3=%+v err=%v", p3, err)
	}
	if _, err := ParsePatch("PUT 1:\n+x\nREM"); err == nil || !strings.Contains(err.Error(), "REM") {
		t.Fatalf("expected REM error, got %v", err)
	}
	if _, err := ParsePatch("REM\nMV b.ts"); err == nil || !strings.Contains(err.Error(), "only one file-level op") {
		t.Fatalf("expected duplicate file-op error, got %v", err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "rejects_header_shaped_bare_rows_instead_of_writing_them"
func TestRejectsHeaderShapedBareRowsInsteadOfWritingThem(t *testing.T) {
	cases := [][2]string{
		{"PUT 2.=3:\n+a\nPUT 789*=821:\n+b", `line 3: "PUT 789*=821:"`},
		{"PUT >2:\nnew\n  [docs/b.md#2C2B]\nmore", `line 3: "  [docs/b.md#2C2B]"`},
	}
	for _, tc := range cases {
		_, err := ParsePatch(tc[0])
		if err == nil || !strings.HasPrefix(err.Error(), tc[1]) || !strings.Contains(err.Error(), "prefix it with `+`") {
			t.Fatalf("%q: err=%v", tc[0], err)
		}
	}
	p, err := ParsePatch("PUT >2:\nnew\nrow")
	if err != nil || len(p.Edits) != 2 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_splits_sections_and_normalizes_tags"
func TestInputSplitsSectionsAndNormalizesTags(t *testing.T) {
	patch, err := ParsePatchInput("\n*** Begin Patch\n[a.ts#1a2b]\nPUT 2:\n+B\n[b.ts]\nCUT 3\n*** End Patch\n[c.ts]\nCUT 1", SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.Sections) != 2 || patch.Sections[0].Path != "a.ts" || patch.Sections[0].FileHash != "1A2B" || patch.Sections[0].Diff != "PUT 2:\n+B" || patch.Sections[1].Path != "b.ts" {
		t.Fatalf("patch=%+v", patch)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_recovers_apply_patch_header_noise_and_spaces"
func TestInputRecoversApplyPatchHeaderNoiseAndSpaces(t *testing.T) {
	patch, err := ParsePatchInput("[*** Update File: dir with spaces/a.ts#1A2B]\nPUT 1:\n+x", SplitOptions{})
	if err != nil || patch.Sections[0].Path != "dir with spaces/a.ts" || patch.Sections[0].FileHash != "1A2B" {
		t.Fatalf("patch=%+v err=%v", patch, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_accepts_hash_in_tagged_paths_only"
func TestInputAcceptsHashInTaggedPathsOnly(t *testing.T) {
	for _, tc := range [][2]string{
		{"[conf.yaml##hostname.home#1a2b]", "conf.yaml##hostname.home"},
		{"[*** Update File: conf.yaml##os.Linux#1A2B]", "conf.yaml##os.Linux"},
	} {
		patch, err := ParsePatchInput(tc[0]+"\nPUT 1:\n+x", SplitOptions{})
		if err != nil || patch.Sections[0].Path != tc[1] || patch.Sections[0].FileHash != "1A2B" {
			t.Fatalf("%s: patch=%+v err=%v", tc[0], patch, err)
		}
	}
	for _, hdr := range []string{"[conf.yaml##hostname.home]", "[conf.yaml##hostname]"} {
		_, err := ParsePatchInput(hdr+"\nPUT 1:\n+x", SplitOptions{})
		if err == nil || !strings.Contains(err.Error(), "Input header must be [PATH#TAG] with") {
			t.Fatalf("%s: err=%v", hdr, err)
		}
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_recovers_headers_nested_in_apply_patch_envelope_markers"
func TestInputRecoversHeadersNestedInApplyPatchEnvelopeMarkers(t *testing.T) {
	p1, err := ParsePatchInput("[*** Begin Patch] [migrations.ts#5275]\nCUT 33.=38", SplitOptions{})
	if err != nil || len(p1.Sections) != 1 || p1.Sections[0].Path != "migrations.ts" || p1.Sections[0].FileHash != "5275" {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := ParsePatchInput("[*** Begin Patch]\n[a.ts#1a2b]\nPUT 2.=2:\n+B\n[*** End Patch]\n[b.ts#3c4d]\nCUT 1", SplitOptions{})
	if err != nil || len(p2.Sections) != 1 || p2.Sections[0].Path != "a.ts" {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_rejects_multi_group_headers_instead_of_inventing_a_path"
func TestInputRejectsMultiGroupHeaders(t *testing.T) {
	for _, hdr := range []string{"[Foo] [bar.ts#1A2B]", "[a.ts] [b.ts#1A2B]", "[x] [y]"} {
		_, err := ParsePatchInput(hdr+"\nCUT 1", SplitOptions{})
		if err == nil || !strings.Contains(err.Error(), "Input header must be") {
			t.Fatalf("%s: err=%v", hdr, err)
		}
	}
	p1, err := ParsePatchInput("[app/[slug]/page.tsx#1A2B]\nCUT 1", SplitOptions{})
	if err != nil || p1.Sections[0].Path != "app/[slug]/page.tsx" || p1.Sections[0].FileHash != "1A2B" {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := ParsePatchInput("[app/[slug]/page.tsx]\nCUT 1", SplitOptions{})
	if err != nil || p2.Sections[0].Path != "app/[slug]/page.tsx" {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_rejects_malformed_tags_and_missing_headers"
func TestInputRejectsMalformedTagsAndMissingHeaders(t *testing.T) {
	for _, hdr := range []string{"[a.ts#1A2B copied]", "[a.ts#1A2B:812]", "[a.ts#1A2B copied from read]"} {
		_, err := ParsePatchInput(hdr+"\nPUT 1:\n+x", SplitOptions{})
		if err == nil || !strings.Contains(err.Error(), "Input header must be") {
			t.Fatalf("%s: err=%v", hdr, err)
		}
	}
	for _, hdr := range []string{"[a.ts#1A2]", "[a.ts#1A2G]", "[a.ts#1A2B5]", "[a.ts#AUTO]", "[a.ts#????]", "[a.ts#]"} {
		p, err := ParsePatchInput(hdr+"\nPUT 1:\n+x", SplitOptions{})
		if err != nil || p.Sections[0].Path != "a.ts" || p.Sections[0].HasHash {
			t.Fatalf("%s: p=%+v err=%v", hdr, p, err)
		}
	}
	_, err := ParsePatchInput("CUT 38.=40", SplitOptions{})
	if err == nil || !strings.Contains(err.Error(), `input must begin with "[PATH#HASH]"`) || !strings.Contains(err.Error(), "[src/foo.ts#1A2B]") {
		t.Fatalf("err=%v", err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_supports_fallback_path_and_absolute_paths_in_cwd"
func TestInputSupportsFallbackPathAndAbsolutePathsInCwd(t *testing.T) {
	p1, err := ParsePatchInput("PUT <1:\n+x", SplitOptions{Path: "a.ts"})
	if err != nil || p1.Sections[0].Path != "a.ts" {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := ParsePatchInput("[/tmp/work/src/a.ts]\nPUT <1:\n+x", SplitOptions{Cwd: "/tmp/work"})
	if err != nil || p2.Sections[0].Path != "src/a.ts" {
		t.Fatalf("p2=%+v err=%v", p2, err)
	}
	if _, err := ParsePatchInput("plain text", SplitOptions{Path: "a.ts"}); err == nil {
		t.Fatal("expected error for plain text")
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "input_merges_same_path_sections_and_conflicting_tags_fail"
func TestInputMergesSamePathSectionsAndConflictingTagsFail(t *testing.T) {
	patch, err := ParsePatchInput("[a.ts#1A2B]\nCUT 1\n[b.ts]\nCUT 2\n[a.ts#1A2B]\nCUT 3", SplitOptions{})
	if err != nil || len(patch.Sections) != 2 || patch.Sections[0].Diff != "CUT 1\nCUT 3" {
		t.Fatalf("patch=%+v err=%v", patch, err)
	}
	if _, pErr := patch.Sections[0].Parse(); pErr == nil || !strings.Contains(pErr.Error(), "cannot be used in a file whose sections are interleaved") {
		t.Fatalf("pErr=%v", pErr)
	}
	if _, err := ParsePatchInput("[a.ts#1A2B]\nCUT 1\n[a.ts#3C4D]\nCUT 2", SplitOptions{}); err == nil || !strings.Contains(err.Error(), "Conflicting hashline snapshot tags") {
		t.Fatalf("err=%v", err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "section_reports_anchor_scope_and_sorted_lines"
func TestSectionReportsAnchorScopeAndSortedLines(t *testing.T) {
	patch, err := ParsePatchInput("[a.ts]\nCUT 5.=6\nPUT >2:\n+x", SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := patch.Sections[0].HasAnchorScopedEdit()
	if err != nil || !scoped {
		t.Fatalf("scoped=%v err=%v", scoped, err)
	}
	lines, err := patch.Sections[0].CollectAnchorLines()
	if err != nil || !slices.Equal(lines, []uint32{2, 5, 6}) {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "prefix_helpers_strip_read_and_diff_shapes"
func TestPrefixHelpersStripReadAndDiffShapes(t *testing.T) {
	if got := StripOneLeadingHashlinePrefix(" >>> + 42:hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := StripHashlinePrefixes([]string{"[a.ts#1A2B]", "1:one", "2:two"}); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("got %v", got)
	}
	if got := StripNewLinePrefixes([]string{"+one", "+two"}); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("got %v", got)
	}
	if got := HashlineParseText("1:one\r\n2:two\n"); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("got %v", got)
	}
	for _, meta := range []string{
		"[Showing lines 1-2 of 8. Use :3 to continue]",
		"2-4: omitted …",
		"...",
	} {
		if !IsReadMetadataLine(meta) {
			t.Fatalf("expected metadata: %q", meta)
		}
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "read_truncation_notice_covers_emitted_shapes"
func TestReadTruncationNoticeCoversEmittedShapes(t *testing.T) {
	notices := []string{
		"[Showing lines 1-20 of 60 (50.0KB limit). Use :21 to continue]",
		"[Showing last 50.0KB across lines 4-8 of 8; line 4 is partial]",
		"[40 more lines in notebook. Use :21 to continue]",
		"[More lines in file (1.2MB total; not scanned to EOF). Use :21 to continue]",
		"[...30ln elided; re-read needed ranges, e.g. a.ts:5-16,40-80]",
		"[Line 1 is 60.0KB, exceeds 50.0KB limit. Hashline output requires full lines; cannot emit an editable numbered preview for a truncated line.]",
	}
	for _, n := range notices {
		if !IsReadTruncationNotice(n) {
			t.Fatalf("not recognized: %q", n)
		}
	}
	if IsReadTruncationNotice("[Showing files 1-20 of 60. Use skip=20 for the next page]") {
		t.Fatal("directory notice should not match")
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "write_prefix_strip_keeps_numbers_that_do_not_count_up_by_one"
func TestWritePrefixStripKeepsNonSequentialNumbers(t *testing.T) {
	for _, text := range []string{
		"100000000: 30 00 00 54 bc.eq 0x100000004\n100000004: c0 03 5f d6 ret\n",
		"00:00 welcome back\n00:02 to the series\n00:03 today we\n",
		"20:56:07 read a\n20:56:07 read b\n",
	} {
		rows := strings.Split(text, "\n")
		if got := StripHashlinePrefixes(rows); !slices.Equal(got, rows) {
			t.Fatalf("%q: stripped unexpectedly to %v", text, got)
		}
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "write_prefix_strip_still_removes_read_and_search_numbering"
func TestWritePrefixStripRemovesReadAndSearchNumbering(t *testing.T) {
	read := "[a.ts#1A2B]\n1:one\n2:two\n\n…\n\n9:nine\n10:ten\n"
	if got := StripHashlinePrefixes(strings.Split(read, "\n")); !slices.Equal(got, []string{"one", "two", "", "", "nine", "ten", ""}) {
		t.Fatalf("read got %v", got)
	}
	search := "[a.ts#1A2B]\n 4:ctx\n*5:hit\n...\n*9:hit\n[b.ts#3C4D]\n*2:hit\n"
	if got := StripHashlinePrefixes(strings.Split(search, "\n")); !slices.Equal(got, []string{"ctx", "hit", "hit", "hit", ""}) {
		t.Fatalf("search got %v", got)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "mismatch_messages_distinguish_stale_and_unrecognized_hashes"
func TestMismatchMessagesDistinguishStaleAndUnrecognizedHashes(t *testing.T) {
	stale := MismatchDetails{
		Path:             "a.ts",
		ExpectedFileHash: "1A2B",
		ActualFileHash:   "3C4D",
		FileLines:        []string{"one", "two", "three"},
		AnchorLines:      []uint32{2},
		HashRecognized:   true,
	}
	msg := FormatMismatchMessage(stale)
	if !strings.Contains(msg, "Edit rejected for a.ts: file changed between read and edit.") || !strings.Contains(msg, "*2:two") {
		t.Fatalf("stale=%s", msg)
	}
	unknown := stale
	unknown.HashRecognized = false
	msg = FormatMismatchMessage(unknown)
	if !strings.Contains(msg, "hash #1A2B is not from this session") || !strings.Contains(msg, "#3C4D") {
		t.Fatalf("unknown=%s", msg)
	}
	other := unknown
	other.TagOriginPaths = []string{"/build/x/wt/crates/a/mcp.rs"}
	msg = FormatMismatchMessage(other)
	if !strings.Contains(msg, "Hash #1A2B was issued in this session for /build/x/wt/crates/a/mcp.rs.") {
		t.Fatalf("other=%s", msg)
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "unknown_tag_rejection_offers_resend_only_for_shown_rows_of_this_file"
func TestUnknownTagRejectionOffersResendOnlyForShownRows(t *testing.T) {
	check := func(anchors []uint32, origins []string) bool {
		return strings.Contains(FormatMismatchMessage(MismatchDetails{
			Path:             "a.ts",
			ExpectedFileHash: "7E21",
			ActualFileHash:   "3C4D",
			FileLines:        []string{"one", "two"},
			AnchorLines:      anchors,
			TagOriginPaths:   origins,
		}), "resend the edit under #3C4D")
	}
	if !check([]uint32{1}, nil) {
		t.Fatal("should offer resend")
	}
	if check(nil, nil) {
		t.Fatal("should not offer resend without rows")
	}
	if check([]uint32{1}, []string{"/w/b.ts"}) {
		t.Fatal("should not offer resend for other-file tag")
	}
}

// upstream: crates/edit/tests/hashline_parse.rs: "recognizes_operations_and_emits_canonical_labels"
func TestRecognizesOperationsAndEmitsCanonicalLabels(t *testing.T) {
	if !ContainsRecognizableHashlineOperations("partial\nPUT 2:\n") {
		t.Fatal("expected true")
	}
	if ContainsRecognizableHashlineOperations("plain text") {
		t.Fatal("expected false")
	}
	want := []string{"PUT N.=N:", "PUT N.=N (invalid)", "PUT >N @reg", "CUT N.=M: (invalid)", "REM", "MV"}
	if got := OpLabels("PUT 2:\nPUT 2\nPUT >3 @x\nCUT 2.=3:\nREM\nMV x"); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// upstream: crates/edit/src/modes/hashline/mod.rs: "inspect_extracts_paths_and_added_lines_from_partial_input"
func TestInspectExtractsPathsAndAddedLines(t *testing.T) {
	inspected := InspectInput("[*** Update File: src/a.ts#ABCD]\nPUT 1.=1:\n+new\n[src/b.ts]\nCUT 2.=2")
	if !slices.Equal(inspected.Paths, []string{"src/a.ts", "src/b.ts"}) {
		t.Fatalf("paths=%v", inspected.Paths)
	}
	if len(inspected.Entries) != 1 || inspected.Entries[0].Path != "src/a.ts" || inspected.Entries[0].Digest != "new" {
		t.Fatalf("entries=%+v", inspected.Entries)
	}
}

// upstream: crates/edit/src/store.rs: "snapshots_deduplicate_promote_and_union_seen_lines"
func TestStoreSnapshotsDeduplicatePromoteAndUnionSeenLines(t *testing.T) {
	store := NewStore()
	first := store.Record("a.ts", "one", []uint32{1}, true)
	store.Record("a.ts", "two", []uint32{2}, true)
	head, _ := store.Head("a.ts")
	if head.Text != "two" {
		t.Fatalf("head=%q", head.Text)
	}
	if got := store.Record("a.ts", "one", []uint32{3}, true); got != first {
		t.Fatalf("tag=%q want %q", got, first)
	}
	head, _ = store.Head("a.ts")
	if head.Text != "one" || !head.SeenLines[1] || !head.SeenLines[3] || len(head.SeenLines) != 2 {
		t.Fatalf("head=%+v", head)
	}
}

// upstream: crates/edit/src/store.rs: "outside_change_carries_only_unshifted_unchanged_seen_lines"
func TestStoreOutsideChangeCarriesOnlyUnshiftedUnchangedSeenLines(t *testing.T) {
	mkLines := func(count int, edit func(int) (string, bool)) string {
		rows := make([]string, count)
		for i := 1; i <= count; i++ {
			if s, ok := edit(i); ok {
				rows[i-1] = s
			} else {
				rows[i-1] = fmt.Sprintf("line %d", i)
			}
		}
		return strings.Join(rows, "\n")
	}
	all60 := make([]uint32, 60)
	for i := range 60 {
		all60[i] = uint32(i + 1)
	}
	partial := []uint32{47, 48, 49, 50, 51, 52, 53, 54, 55}
	readAfter := func(changed string) map[int]bool {
		store := NewStore()
		store.Record("f.txt", mkLines(60, func(int) (string, bool) { return "", false }), all60, true)
		store.Record("f.txt", changed, partial, true)
		head, _ := store.Head("f.txt")
		return head.SeenLines
	}
	s1 := readAfter(mkLines(60, func(n int) (string, bool) { return "line FIFTY", n == 50 }))
	if !s1[5] || !s1[60] {
		t.Fatalf("unchanged lines should carry: %v", s1)
	}
	s2 := readAfter("new top\n" + mkLines(60, func(int) (string, bool) { return "", false }))
	if s2[5] || !s2[50] {
		t.Fatalf("shifted line 5 should not carry, partial 50 should: %v", s2)
	}
	s3 := readAfter(mkLines(60, func(n int) (string, bool) { return "line FIVE", n == 5 }))
	if s3[5] {
		t.Fatalf("rewritten line 5 should not carry: %v", s3)
	}
}

// upstream: crates/edit/src/store.rs: "version_without_provenance_holds_carried_lines_until_it_is_restricted"
func TestStoreVersionWithoutProvenanceHoldsCarriedLines(t *testing.T) {
	store := NewStore()
	base := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10"
	changed := strings.Replace(base, "line 9", "line NINE", 1)
	store.Record("f.txt", base, []uint32{5}, true)
	tag := store.Record("f.txt", changed, nil, false)
	head, _ := store.Head("f.txt")
	if head.SeenLines != nil {
		t.Fatalf("expected unrestricted head, got %v", head.SeenLines)
	}
	store.RecordSeenLinesU32("f.txt", tag, []uint32{9})
	head, _ = store.Head("f.txt")
	if !head.SeenLines[5] || !head.SeenLines[9] || len(head.SeenLines) != 2 {
		t.Fatalf("got %v, want {5, 9}", head.SeenLines)
	}
}

// upstream: crates/edit/src/store.rs: "rereading_an_evicted_version_restores_what_earlier_reads_displayed"
func TestStoreRereadingEvictedVersionRestoresProvenance(t *testing.T) {
	store := NewStoreWithLimits(1, 4, DefaultMaxTotalBytes)
	var rows []string
	for i := 1; i <= 100; i++ {
		rows = append(rows, fmt.Sprintf("line %d", i))
	}
	text := strings.Join(rows, "\n")
	seen60 := make([]uint32, 60)
	for i := range 60 {
		seen60[i] = uint32(i + 1)
	}
	store.Record("read.txt", text, seen60, true)
	store.Record("other.txt", "other", []uint32{1}, true)
	if _, ok := store.Head("read.txt"); ok {
		t.Fatal("expected read.txt to be evicted")
	}
	store.Record("read.txt", text, []uint32{90}, true)
	head, _ := store.Head("read.txt")
	if !head.SeenLines[1] || !head.SeenLines[60] || !head.SeenLines[90] || head.SeenLines[61] {
		t.Fatalf("restored seen wrong: %v", head.SeenLines)
	}
}

// upstream: crates/edit/src/store.rs: "authored_version_stays_unrestricted_after_a_partial_display"
func TestStoreAuthoredVersionStaysUnrestricted(t *testing.T) {
	store := NewStore()
	store.Record("f.txt", "a\nb\n", []uint32{1}, true)
	tag := store.Record("f.txt", "a\nB\n", []uint32{}, true)
	store.RecordSeenLinesU32("f.txt", tag, []uint32{2})
	store.Record("f.txt", "a\nB\n", []uint32{1}, true)
	head, _ := store.Head("f.txt")
	if head.SeenLines == nil || len(head.SeenLines) != 0 {
		t.Fatalf("expected empty non-nil authored map, got %v", head.SeenLines)
	}
}

// upstream: crates/edit/src/store.rs: "version_and_lru_limits_are_enforced"
func TestStoreVersionAndLRULimits(t *testing.T) {
	store := NewStoreWithLimits(2, 2, DefaultMaxTotalBytes)
	for _, txt := range []string{"one", "two", "three"} {
		store.Record("a", txt, nil, false)
	}
	if _, ok := store.ByContent("a", "one"); ok {
		t.Fatal("expected one evicted by maxVersions=2")
	}
	store.Record("b", "b", nil, false)
	_, _ = store.Head("a")
	store.Record("c", "c", nil, false)
	if _, ok := store.Head("b"); ok {
		t.Fatal("expected b evicted by LRU")
	}
	if _, ok := store.Head("a"); !ok {
		t.Fatal("expected a retained")
	}
}

// upstream: crates/edit/src/store.rs: "relocation_merges_and_rewrites_paths"
func TestStoreRelocationMergesAndRewritesPaths(t *testing.T) {
	store := NewStore()
	shared := store.Record("from", "same", []uint32{1}, true)
	store.Record("to", "older", nil, false)
	store.Relocate("from", "to")
	if _, ok := store.Head("from"); ok {
		t.Fatal("expected from removed")
	}
	snap, ok := store.ByHash("to", shared)
	if !ok || snap.Path != CanonicalPathKey("to") {
		t.Fatalf("snap=%+v ok=%v", snap, ok)
	}
}

// upstream: crates/edit/src/store.rs: "parses_seen_line_boundaries_only"
func TestStoreParsesSeenLineBoundariesOnly(t *testing.T) {
	got := SeenLinesFromBodyU32("1:x\n*20-30:{ … }\n nope\n 7:y")
	if !slices.Equal(got, []uint32{1, 20, 30, 7}) {
		t.Fatalf("got %v", got)
	}
}

// upstream: crates/edit/src/store.rs: "clipboard_batch_fork_and_commit_preserve_named_only"
func TestStoreClipboardBatchForkAndCommit(t *testing.T) {
	store := NewStore()
	initial := &Clipboard{
		Named:   map[string][]string{"a": {"one"}},
		Lines:   []string{"anonymous"},
		HasAnon: true,
	}
	store.CommitClipboard(initial)
	batch := store.StartBatchClipboard()
	if batch.HasAnon {
		t.Fatal("batch should not inherit anon")
	}
	fork := batch.Fork()
	fork.Named["b"] = []string{"two"}
	store.CommitClipboard(fork)
	batch2 := store.StartBatchClipboard()
	if len(batch2.Named) != 2 {
		t.Fatalf("named=%v", batch2.Named)
	}
}

// upstream: crates/edit/src/store.rs: "noop_counter_resets_for_new_payload_and_commit"
func TestStoreNoopCounter(t *testing.T) {
	store := NewStore()
	h := PayloadHash("edit")
	if c, esc := store.RecordNoop("a", h); c != 1 || esc {
		t.Fatalf("1: %d %v", c, esc)
	}
	if c, esc := store.RecordNoop("a", h); c != 2 || esc {
		t.Fatalf("2: %d %v", c, esc)
	}
	if c, esc := store.RecordNoop("a", h); c != 3 || !esc {
		t.Fatalf("3: %d %v", c, esc)
	}
	if c, esc := store.RecordNoop("a", PayloadHash("different")); c != 1 || esc {
		t.Fatalf("diff: %d %v", c, esc)
	}
	store.ResetNoop("a")
	if c, esc := store.RecordNoop("a", h); c != 1 || esc {
		t.Fatalf("reset: %d %v", c, esc)
	}
}
