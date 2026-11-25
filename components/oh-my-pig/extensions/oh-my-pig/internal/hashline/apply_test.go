package hashline

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Tests ported from crates/edit/tests/hashline_apply.rs (apply, echo, clipboard, recovery).

func mkInsertAfter(line uint32, text string, opLine uint32) Edit {
	return Edit{Kind: EditInsert, Cursor: Cursor{Kind: CursorAfterAnchor, Anchor: Anchor{Line: line}}, Text: text, LineNum: opLine}
}

func mkInsertBefore(line uint32, text string, opLine uint32) Edit {
	return Edit{Kind: EditInsert, Cursor: Cursor{Kind: CursorBeforeAnchor, Anchor: Anchor{Line: line}}, Text: text, LineNum: opLine}
}

func mkDelete(line uint32, opLine uint32) Edit {
	return Edit{Kind: EditDelete, Anchor: Anchor{Line: line}, LineNum: opLine}
}

func mkReplacement(start, end uint32, body []string, opLine uint32) []Edit {
	edits := make([]Edit, 0, len(body)+int(end-start+1))
	for _, txt := range body {
		edits = append(edits, Edit{
			Kind:        EditInsert,
			Cursor:      Cursor{Kind: CursorBeforeAnchor, Anchor: Anchor{Line: start}},
			Text:        txt,
			LineNum:     opLine,
			Replacement: true,
		})
	}
	for ln := start; ln <= end; ln++ {
		edits = append(edits, mkDelete(ln, opLine))
	}
	return edits
}

func mustApply(t *testing.T, text string, edits []Edit, path string) ApplyResult {
	t.Helper()
	res, err := ApplyEdits(text, edits, ApplyOptions{Path: path, OnEmptyPaste: EmptyPasteThrow})
	if err != nil {
		t.Fatalf("unexpected apply error: %v", err)
	}
	return res
}

func mustReject(t *testing.T, text string, edits []Edit, path string) string {
	t.Helper()
	_, err := ApplyEdits(text, edits, ApplyOptions{Path: path, OnEmptyPaste: EmptyPasteThrow})
	if err == nil {
		t.Fatalf("expected error applying edits to %s", path)
	}
	return err.Error()
}

// upstream: crates/edit/tests/hashline_apply.rs: "inserts_before_after_head_and_tail"
func TestApplyInsertsBeforeAfterHeadAndTail(t *testing.T) {
	edits := []Edit{
		{Kind: EditInsert, Cursor: Cursor{Kind: CursorBof}, Text: "head", LineNum: 1},
		mkInsertBefore(2, "before", 2),
		mkInsertAfter(2, "after", 3),
		{Kind: EditInsert, Cursor: Cursor{Kind: CursorEof}, Text: "tail", LineNum: 4},
	}
	got := mustApply(t, "one\ntwo\n", edits, "").Text
	if got != "head\none\nbefore\ntwo\nafter\ntail\n" {
		t.Fatalf("got %q", got)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "rejects_a_replacement_of_the_row_after_the_trailing_newline"
func TestApplyRejectsReplacementOfPhantomLine(t *testing.T) {
	err := mustReject(t, "one\ntwo\n", mkReplacement(3, 3, []string{"three"}, 1), "")
	if err != "Line 3 does not exist (file has 2 lines)" {
		t.Fatalf("got %q", err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "rejects_an_out_of_bounds_anchor"
func TestApplyRejectsOutOfBoundsAnchor(t *testing.T) {
	err := mustReject(t, "one\ntwo", []Edit{mkDelete(3, 1)}, "")
	if err != "Line 3 does not exist (file has 2 lines)" {
		t.Fatalf("got %q", err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "applies_an_unindented_replacement_body_verbatim"
func TestApplyUnindentedReplacementVerbatim(t *testing.T) {
	file := "fn f() {\n\tlet a = 1;\n\tlet b = 2;\n\tlet c = 3;\n}"
	res := mustApply(t, file, mkReplacement(2, 4, []string{"let a = 1;", "let b = 20;", "let c = 3;"}, 1), "x.rs")
	if res.Text != "fn f() {\nlet a = 1;\nlet b = 20;\nlet c = 3;\n}" || len(res.Warnings) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "preserves_intentional_indentation_only_replacements"
func TestApplyPreservesIntentionalIndentationOnlyReplacements(t *testing.T) {
	res := mustApply(t, "    first();\n    second();", mkReplacement(1, 2, []string{"first();", "second();"}, 1), "x.ts")
	if res.Text != "first();\nsecond();" || len(res.Warnings) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "rejects_a_body_that_restates_both_neighbours_of_its_range"
func TestApplyRejectsBodyRestatingBothNeighbours(t *testing.T) {
	file := "function f() {\n  keepA();\n  old1();\n  old2();\n  keepB();\n}"
	err := mustReject(t, file, mkReplacement(3, 4, []string{"  keepA();", "  new1();", "  new2();", "  keepB();"}, 1), "x.ts")
	if !strings.Contains(err, "write line 2 (just above the range) a second time") || !strings.Contains(err, "write line 5 (just below the range) a second time") {
		t.Fatalf("err=%s", err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "keeps_a_row_that_restates_a_neighbour_another_hunk_deletes"
func TestApplyKeepsRowRestatingNeighbourDeletedByOtherHunk(t *testing.T) {
	file := "async fn f() {\n\tlet x = call(\n\t\ta)\n\t\t.await;\n\tdone();\n}\n"
	edits := append(mkReplacement(2, 3, []string{"\tlet x = other(", "\t\tb)", "\t\t.await;"}, 1), mkDelete(4, 2))
	res := mustApply(t, file, edits, "x.rs")
	if res.Text != "async fn f() {\n\tlet x = other(\n\t\tb)\n\t\t.await;\n\tdone();\n}\n" {
		t.Fatalf("got %q", res.Text)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "keeps_a_boundary_repeat_when_dropping_it_would_be_a_no_op"
func TestApplyKeepsBoundaryRepeatWhenDropIsNoop(t *testing.T) {
	text := "alpha\nbeta\ngamma\nomega"
	cases := []struct {
		path, file string
		start, end uint32
		body       []string
		want       string
	}{
		{"x.txt", text, 2, 3, []string{"alpha", "beta", "gamma"}, "alpha\nalpha\nbeta\ngamma\nomega"},
		{"x.txt", text, 2, 3, []string{"beta", "gamma", "omega"}, "alpha\nbeta\ngamma\nomega\nomega"},
		{"x.txt", text, 2, 3, []string{"alpha", "beta", "gamma", "omega"}, "alpha\nalpha\nbeta\ngamma\nomega\nomega"},
		{"t.rs", "#[test]\nfn a() {}\n", 2, 2, []string{"#[test]", "fn a() {}"}, "#[test]\n#[test]\nfn a() {}\n"},
		{"f.ts", "function f() {\n  a();\n  b();\n}\n", 2, 3, []string{"function f() {", "  a();", "  b();"}, "function f() {\nfunction f() {\n  a();\n  b();\n}\n"},
	}
	for _, tc := range cases {
		res := mustApply(t, tc.file, mkReplacement(tc.start, tc.end, tc.body, 1), tc.path)
		if res.Text != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.path, res.Text, tc.want)
		}
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "rejects_a_leading_keeper_echo_when_the_payload_cannot_fill_the_widened_range"
func TestApplyRejectsLeadingKeeperEchoShortBody(t *testing.T) {
	file := "{\n    auto* handle = payloadFor<PyThreadHandle>(self);\n    if (!handle)\n        return threadError(globalObject, \"thread not started\");\n    handle.setDone();\n}"
	err := mustReject(t, file, mkReplacement(3, 4, []string{
		"    auto* handle = payloadFor<PyThreadHandle>(self);",
		"    if (!handle || !handle.isStarted())",
	}, 1), "x.cpp")
	if !strings.Contains(err, "body opens by restating") {
		t.Fatalf("err=%s", err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "refuses_a_too_short_leading_echo_the_same_way_with_or_without_a_parser"
func TestApplyRefusesTooShortLeadingEchoWithOrWithoutParser(t *testing.T) {
	file := "import time\n\ndef run(a=None, b=None):\n    \"\"\"Time each phase: reap, collect,\n    grade, so the part outside the cap is\n    measured, not assumed.\"\"\"\n    rs, wd = a or 1, b or 2\n    t0 = time.monotonic()\n    return rs, wd, t0\n"
	edits := mkReplacement(5, 8, []string{
		"    \"\"\"Time each phase: reap, collect,",
		"    collect_objects, grade, so the part outside the cap is",
		"    measured, not assumed.\"\"\"",
	}, 1)
	for _, p := range []string{"server.py", "server.txt"} {
		err := mustReject(t, file, edits, p)
		if !strings.Contains(err, "body opens by restating the 1 line(s) just above the range") || !strings.Contains(err, "the body leaves out lines 7-8") {
			t.Fatalf("%s: err=%s", p, err)
		}
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "refuses_a_too_short_echo_a_dropped_copy_exposed"
func TestApplyRefusesRepeatedBoundaryEcho(t *testing.T) {
	one := "const z = 0;\nfunction f() {\n\told();\n}\nafter();\n"
	two := "const z = 0;\nfunction f() {\n\told();\n\tmore();\n}\nafter();\n"
	cases := []struct {
		file       string
		start, end uint32
		body       []string
		restates   string
		leftOut    string
	}{
		{one, 3, 4, []string{"function f() {", "function f() {", "}"}, "opens by restating the 1 line(s) just above the range more than once", "it leaves out line 4,"},
		{one, 2, 3, []string{"function f() {", "}", "}"}, "ends by restating the 1 line(s) just below the range more than once", "it leaves out line 2,"},
		{two, 3, 4, []string{"function f() {", "function f() {", "\tfresh();", "}"}, "opens by restating the 1 line(s) just above the range more than once", "it leaves out line 4,"},
	}
	for _, tc := range cases {
		err := mustReject(t, tc.file, mkReplacement(tc.start, tc.end, tc.body, 1), "f.ts")
		if !strings.Contains(err, tc.restates) || !strings.Contains(err, tc.leftOut) {
			t.Fatalf("err=%s", err)
		}
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "refuses_an_echo_when_both_readings_parse"
func TestApplyRefusesEchoWhenBothReadingsParse(t *testing.T) {
	cases := []struct {
		path, file string
		start, end uint32
		body       []string
		repeated   string
	}{
		{"s.ts", "switch (x) {\n\tcase 1:\n\t\ta();\n\t\tbreak;\n\tdefault:\n\t\tb();\n}\n", 5, 6, []string{"\t\tbreak;", "\tcase 2:", "\t\tc();", "\t\tbreak;", "\tdefault:", "\t\tb();"}, "write line 4 (just above the range) a second time"},
		{"c.py", "class C:\n    @property\n    def a(self):\n        return 1\n", 3, 4, []string{"    @property", "    def a(self):", "        return 2", "", "    @property", "    def b(self):", "        return 3"}, "write line 2 (just above the range) a second time"},
		{"t.rs", "#[test]\nfn a() {}\n", 2, 2, []string{"#[test]", "fn a() { x(); }", "", "#[test]", "fn b() {}"}, "write line 1 (just above the range) a second time"},
		{"x.rs", "/// Old summary.\n#[napi]\npub fn f() {}", 1, 1, []string{"/// New summary.", "#[napi]"}, "write line 2 (just below the range) a second time"},
	}
	for _, tc := range cases {
		err := mustReject(t, tc.file, mkReplacement(tc.start, tc.end, tc.body, 1), tc.path)
		if !strings.Contains(err, tc.repeated) {
			t.Fatalf("%s: err=%s", tc.path, err)
		}
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "keeps_a_trailing_statement_echo_literal_on_a_single_line_range"
func TestApplyKeepsTrailingStatementEchoOnSingleLine(t *testing.T) {
	res := mustApply(t, "foo();\nold();\nbar();", mkReplacement(2, 2, []string{"new();", "bar();"}, 1), "x.ts")
	if res.Text != "foo();\nnew();\nbar();\nbar();" {
		t.Fatalf("got %q", res.Text)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "keeps_a_shallower_gap_insert_where_it_was_written"
func TestApplyKeepsShallowerGapInsertWhereWritten(t *testing.T) {
	file := "function f() {\n    if (x) {\n        a();\n    }\n    b();\n}\n"
	res := mustApply(t, file, []Edit{mkInsertAfter(3, "    c();", 1)}, "x.ts")
	if res.Text != "function f() {\n    if (x) {\n        a();\n    c();\n    }\n    b();\n}\n" {
		t.Fatalf("got %q", res.Text)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "clipboard_cut_then_gap_paste_moves_lines"
func TestClipboardCutThenGapPasteMovesLines(t *testing.T) {
	edits := []Edit{
		{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 2}, End: Anchor{Line: 3}}, LineNum: 1},
		mkDelete(2, 1),
		mkDelete(3, 1),
		{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorAfterAnchor, Anchor: Anchor{Line: 4}}}, LineNum: 2},
	}
	cb := &Clipboard{}
	res, err := ApplyEdits("a\nb\nc\nd", edits, ApplyOptions{Clipboard: cb, OnEmptyPaste: EmptyPasteThrow})
	if err != nil || res.Text != "a\nd\nb\nc" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "repeated_anonymous_pastes_do_not_consume_the_clipboard"
func TestRepeatedAnonymousPastesDoNotConsumeClipboard(t *testing.T) {
	edits := []Edit{
		{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 2}, End: Anchor{Line: 2}}, LineNum: 1},
		mkDelete(2, 1),
		{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorBof}}, LineNum: 2},
		{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorEof}}, LineNum: 3},
	}
	cb := &Clipboard{}
	res, err := ApplyEdits("a\nb\nc", edits, ApplyOptions{Clipboard: cb, OnEmptyPaste: EmptyPasteThrow})
	if err != nil || res.Text != "b\na\nc\nb" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "named_registers_swap_two_regions"
func TestNamedRegistersSwapTwoRegions(t *testing.T) {
	edits := []Edit{
		{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 1}, End: Anchor{Line: 2}}, Register: "a", HasRegister: true, LineNum: 1},
		mkDelete(1, 1), mkDelete(2, 1),
		{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 3}, End: Anchor{Line: 4}}, Register: "b", HasRegister: true, LineNum: 2},
		mkDelete(3, 2), mkDelete(4, 2),
		{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorBof}}, Register: "b", HasRegister: true, LineNum: 3},
		{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorEof}}, Register: "a", HasRegister: true, LineNum: 4},
	}
	cb := &Clipboard{}
	res, err := ApplyEdits("a1\na2\nb1\nb2", edits, ApplyOptions{Clipboard: cb, OnEmptyPaste: EmptyPasteThrow})
	if err != nil || res.Text != "b1\nb2\na1\na2" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "named_register_gap_paste_warns_and_does_nothing_when_empty"
func TestNamedRegisterGapPasteWarnsWhenEmpty(t *testing.T) {
	paste := Edit{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorAfterAnchor, Anchor: Anchor{Line: 1}}}, Register: "missing", HasRegister: true, LineNum: 4}
	cb := &Clipboard{}
	var warnings []string
	resolved, err := ResolveClipboardEdits([]Edit{paste}, []string{"a"}, cb, EmptyPasteThrow, func(w string) { warnings = append(warnings, w) })
	if err != nil || len(resolved) != 0 || len(warnings) != 1 || !strings.HasPrefix(warnings[0], "line 4: `@missing` was empty") {
		t.Fatalf("resolved=%v warnings=%v err=%v", resolved, warnings, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "empty_named_span_paste_is_rejected"
func TestEmptyNamedSpanPasteIsRejected(t *testing.T) {
	paste := Edit{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetSpan, Range: ParsedRange{Start: Anchor{Line: 1}, End: Anchor{Line: 1}}}, Register: "missing", HasRegister: true, LineNum: 4}
	_, err := ResolveClipboardEdits([]Edit{paste}, []string{"a"}, &Clipboard{}, EmptyPasteThrow, nil)
	if err == nil || !strings.Contains(err.Error(), "pasting it over a range would delete those lines") {
		t.Fatalf("err=%v", err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "ambiguous_anonymous_paste_is_rejected"
func TestAmbiguousAnonymousPasteIsRejected(t *testing.T) {
	edits := []Edit{
		{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 1}, End: Anchor{Line: 1}}, LineNum: 1},
		{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 2}, End: Anchor{Line: 2}}, LineNum: 2},
		{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorEof}}, LineNum: 3},
	}
	err := ValidateClipboardSequence(edits, &Clipboard{})
	if err == nil || !strings.Contains(err.Error(), "2 unlabeled `CUT`s are pending") {
		t.Fatalf("err=%v", err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "empty_paste_drop_removes_the_incomplete_preview_op"
func TestEmptyPasteDropRemovesIncompleteOp(t *testing.T) {
	paste := Edit{Kind: EditPaste, PasteAt: PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorEof}}, LineNum: 1}
	resolved, err := ResolveClipboardEdits([]Edit{paste}, []string{"a"}, &Clipboard{}, EmptyPasteDrop, nil)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
}

// --- Recovery tests from crates/edit/tests/hashline_apply.rs ---

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_uniform_line_shift"
func TestRecoveryRemapsUniformLineShift(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery.rs"
	prev := "fn f() {\n\told();\n}\n"
	tag := store.Record(path, prev, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "// header\nfn f() {\n\told();\n}\n",
		FileHash:    tag,
		Edits:       mkReplacement(2, 2, []string{"\tnew();"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "// header\nfn f() {\n\tnew();\n}\n" || !strings.Contains(rec.Warnings[0], "remapping stale line anchors") {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

const (
	errandsText        = "start the engine\ncheck the oil\ndrive home\n"
	shiftedErrandsText = "header one\nheader two\nstart the engine\ncheck the oil\ndrive home\n"
)

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_refuses_a_remap_of_a_body_written_for_the_current_line"
func TestRecoveryRefusesRemapWrittenForCurrentLine(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-current-line.txt"
	tag := store.Record(path, errandsText, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: shiftedErrandsText,
		FileHash:    tag,
		Edits:       mkReplacement(3, 3, []string{"start the engine twice"}, 1),
	})
	if err != nil || rec != nil {
		t.Fatalf("expected nil recovery, got %+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_body_written_for_the_tagged_line"
func TestRecoveryRemapsBodyWrittenForTaggedLine(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-tagged-line.txt"
	tag := store.Record(path, errandsText, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: shiftedErrandsText,
		FileHash:    tag,
		Edits:       mkReplacement(3, 3, []string{"drive home slowly"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "header one\nheader two\nstart the engine\ncheck the oil\ndrive home slowly\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_budgets_the_body_check_in_chars_not_bytes"
func TestRecoveryBudgetsBodyCheckInCharsNotBytes(t *testing.T) {
	taggedRows := make([]string, 40)
	curRows := make([]string, 40)
	body := make([]string, 40)
	for i := range 40 {
		taggedRows[i] = fmt.Sprintf("%s%03d", strings.Repeat("タ", 37), i)
		curRows[i] = fmt.Sprintf("%s%03d", strings.Repeat("中", 37), i)
		body[i] = curRows[i] + "！"
	}
	tagged := strings.Join(taggedRows, "\n") + "\nend\n"
	current := strings.Join(curRows, "\n") + "\n" + tagged
	store := NewStore()
	path := "/tmp/recovery-budget-chars.txt"
	tag := store.Record(path, tagged, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: current,
		FileHash:    tag,
		Edits:       mkReplacement(1, 40, body, 1),
	})
	if err != nil || rec != nil {
		t.Fatalf("expected refusal, got %+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_shifted_range_that_overlaps_its_kept_line"
func TestRecoveryRemapsShiftedRangeOverlappingKeptLine(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-overlap.txt"
	tag := store.Record(path, "intro text\n\nalpha line\nobsolete line\nend text\n", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "header\nintro text\n\nalpha line\nobsolete line\nend text\n",
		FileHash:    tag,
		Edits:       mkReplacement(3, 4, []string{"alpha line 2"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "header\nintro text\n\nalpha line 2\nend text\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_filled_blank_line_next_to_a_similar_line"
func TestRecoveryRemapsFilledBlankLine(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-blank-fill.txt"
	tag := store.Record(path, "first line here\n\nthird line here\n", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "a header\nfirst line here\n\nthird line here\n",
		FileHash:    tag,
		Edits:       mkReplacement(2, 2, []string{"first line here too"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "a header\nfirst line here\nfirst line here too\nthird line here\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_short_body_next_to_a_short_line"
func TestRecoveryRemapsShortBodyNextToShortLine(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-short.txt"
	tag := store.Record(path, "header\nx\nlong legacy line\ntail\n", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "prefix\nheader\nx\nlong legacy line\ntail\n",
		FileHash:    tag,
		Edits:       mkReplacement(3, 3, []string{"x2"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "prefix\nheader\nx\nx2\ntail\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_rewrite_loosely_like_the_neighboring_line"
func TestRecoveryRemapsRewriteLooselyLikeNeighbor(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-rewrite.rs"
	prev := "fn f() -> u32 {\n\tlet computed = 1;\n\tpanic!(\"not implemented\");\n}\n"
	tag := store.Record(path, prev, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "// header\n" + prev,
		FileHash:    tag,
		Edits:       mkReplacement(3, 3, []string{"\treturn computed;"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "// header\nfn f() -> u32 {\n\tlet computed = 1;\n\treturn computed;\n}\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_publishes_the_registers_of_an_accepted_remap"
func TestRecoveryPublishesRegistersOfAcceptedRemap(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-accepted-cut.txt"
	tag := store.Record(path, "a\nb\nc\n", nil, false)
	cb := &Clipboard{}
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "h\na\nb\nc\n",
		FileHash:    tag,
		Edits: []Edit{
			{Kind: EditCut, Range: ParsedRange{Start: Anchor{Line: 2}, End: Anchor{Line: 2}}, Register: "reg", HasRegister: true, LineNum: 1},
			mkDelete(2, 1),
		},
		Clipboard: cb,
	})
	if err != nil || rec == nil || rec.Text != "h\na\nc\n" || !slices.Equal(cb.Named["reg"], []string{"b"}) {
		t.Fatalf("rec=%+v cb=%+v err=%v", rec, cb, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_rejects_a_changed_anchor"
func TestRecoveryRejectsChangedAnchor(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-changed.rs"
	tag := store.Record(path, "a\nold\nc", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "a\nchanged\nc",
		FileHash:    tag,
		Edits:       mkReplacement(2, 2, []string{"new"}, 1),
	})
	if err != nil || rec != nil {
		t.Fatalf("expected nil, got %+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_replays_unchanged_anchor_onto_current_session_text"
func TestRecoveryReplaysUnchangedAnchorOntoSessionText(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-session.ts"
	tag := store.Record(path, "L1\nL2\nL3\nL4\nL5\nL6\n", nil, false)
	current := "L1\nL2\nL3\nL4\nL5-CHANGED\nL6\n"
	store.Record(path, current, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: current,
		FileHash:    tag,
		Edits:       mkReplacement(3, 3, []string{"L3-MODEL"}, 1),
	})
	if err != nil || rec == nil || !strings.Contains(rec.Text, "L3-MODEL") || !strings.Contains(rec.Text, "L5-CHANGED") || !strings.Contains(rec.Warnings[0], "earlier in-session snapshot") {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_after_a_prior_deletion"
func TestRecoveryRemapsAfterPriorDeletion(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-deletion.ts"
	tag := store.Record(path, "L1\nL2\nL3\nL4\nL5\nL6\n", nil, false)
	current := "L1\nL3\nL4\nL5\nL6\n"
	store.Record(path, current, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: current,
		FileHash:    tag,
		Edits:       mkReplacement(5, 5, []string{"L5-MODEL"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "L1\nL3\nL4\nL5-MODEL\nL6\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_refuses_an_isolated_unique_line_without_neighbor_offset"
func TestRecoveryRefusesIsolatedUniqueLine(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-isolated.ts"
	tag := store.Record(path, "L1\nL2\nL3\nL4\nT\nL6\n", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "X\nL1\nL2\nL3\nL4\nBEFORE\nT\nAFTER\nL6\n",
		FileHash:    tag,
		Edits:       mkReplacement(5, 5, []string{"MODEL"}, 1),
	})
	if err != nil || rec != nil {
		t.Fatalf("expected nil, got %+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_duplicate_range_when_context_matches"
func TestRecoveryRemapsDuplicateRangeWhenContextMatches(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-dup-valid.ts"
	tag := store.Record(path, "alpha\nDUP\nbeta\nDUP\nomega\n", nil, false)
	current := "alpha\nINSERTED\nDUP\nbeta\nDUP\nomega\n"
	store.Record(path, current, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: current,
		FileHash:    tag,
		Edits:       mkReplacement(3, 4, []string{"B-MODEL", "MODEL"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "alpha\nINSERTED\nDUP\nB-MODEL\nMODEL\nomega\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_rejects_ambiguous_duplicate_anchor_context"
func TestRecoveryRejectsAmbiguousDuplicateAnchorContext(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-duplicate.rs"
	tag := store.Record(path, "start\nDUP\nmid\nDUP\ntail", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "start\nmid\nDUP\nCHANGED\ntail",
		FileHash:    tag,
		Edits:       mkReplacement(4, 4, []string{"MODEL"}, 1),
	})
	if err != nil || rec != nil {
		t.Fatalf("expected nil, got %+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_uses_the_most_recent_snapshot_when_tags_collide"
func TestRecoveryUsesMostRecentSnapshotOnTagCollision(t *testing.T) {
	tags := make(map[string]string)
	var older, newer, tag string
	for n := range 100000 {
		txt := fmt.Sprintf("shared head\nunique payload %d\nshared tail\n", n)
		h := FileHash(txt)
		if prev, ok := tags[h]; ok {
			older, newer, tag = prev, txt, h
			break
		}
		tags[h] = txt
	}
	if tag == "" {
		t.Fatal("failed to find collision")
	}
	store := NewStore()
	path := "/tmp/recovery-collision.ts"
	store.Record(path, older, nil, false)
	store.Record(path, newer, nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: newer + "drifted trailer\n",
		FileHash:    tag,
		Edits:       mkReplacement(2, 2, []string{"model payload"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "shared head\nmodel payload\nshared tail\ndrifted trailer\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "recovery_remaps_a_shift_inside_the_same_construct"
func TestRecoveryRemapsShiftInsideSameConstruct(t *testing.T) {
	store := NewStore()
	path := "/tmp/recovery-same-construct.py"
	tag := store.Record(path, "def f():\n    old()\n", nil, false)
	rec, err := TryRecover(store, RecoveryArgs{
		Path:        path,
		CurrentText: "import os\n\ndef f():\n    old()\n",
		FileHash:    tag,
		Edits:       mkReplacement(2, 2, []string{"    new()"}, 1),
	})
	if err != nil || rec == nil || rec.Text != "import os\n\ndef f():\n    new()\n" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
}
