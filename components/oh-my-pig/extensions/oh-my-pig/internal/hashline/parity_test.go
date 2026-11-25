package hashline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Additional upstream tests ported from:
// - crates/edit/src/modes/hashline/patcher.rs lifecycle tests
// - crates/edit/tests/hashline_patcher.rs
// - crates/edit/tests/hashline_parity.rs
// (The 26 patcher.json and parity_*.json fixture suites are loaded directly in fixture_json_test.go.)

// upstream: crates/edit/src/modes/hashline/patcher.rs: "wide_unseen_line_requires_full_read" & "resumed_wide_anchor_requires_full_read"
func TestWideUnseenAndResumedWideAnchorsRequireRawRead(t *testing.T) {
	wide := "note: " + strings.Repeat("abcdefghij", 80) + " END-OF-LINE-TAIL"
	orig := wide + "\nshort\n"

	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("wide.txt", orig)
	tag := w.snapshot("wide.txt", orig, []uint32{2})
	patch := fmt.Sprintf("[wide.txt#%s]\nPUT 1.=1:\n+NOTE: %s\n", tag, wide[6:768])
	for range 2 {
		_, _, err := w.apply(patch)
		if err == nil || !strings.Contains(err.Error(), "wide.txt:1:raw") {
			t.Fatalf("expected :1:raw error, got %v", err)
		}
	}

	// Resumed (fresh empty store) with wide line: rejected until recorded as seen.
	w2 := newTestWorkspace(t)
	w2.opts.EnforceSeenLines = true
	orig2 := "first\n" + wide + "\n"
	w2.writeFile("resumed.txt", orig2)
	tag2 := FileHash(orig2)
	_, _, err := w2.apply(fmt.Sprintf("[resumed.txt#%s]\nPUT 2.=2:\n+NOTE: %s\n", tag2, wide[6:512]))
	if err == nil || !strings.Contains(err.Error(), "resumed.txt:2:raw") {
		t.Fatalf("expected :2:raw error on resumed wide line, got %v", err)
	}
	// Short line in same resumed file still applies.
	if _, _, err := w2.apply(fmt.Sprintf("[resumed.txt#%s]\nPUT 1.=1:\n+FIRST\n", tag2)); err != nil {
		t.Fatalf("short line in resumed file should apply: %v", err)
	}
}

// upstream: crates/edit/src/modes/hashline/patcher.rs: "resumed_block_over_wide_line_requires_full_read"
func TestResumedBlockOverWideLineRequiresFullRead(t *testing.T) {
	wide := "note: " + strings.Repeat("abcdefghij", 80) + " END-OF-LINE-TAIL"
	orig := fmt.Sprintf("function f() {\n\tconst x = \"%s\";\n\treturn x;\n}\n", wide)
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("block.ts", orig)
	tag := FileHash(orig)
	_, _, err := w.apply(fmt.Sprintf("[block.ts#%s]\nCUT 1*\n", tag))
	if err == nil || !strings.Contains(err.Error(), "block.ts:2:raw") {
		t.Fatalf("expected block.ts:2:raw error, got %v", err)
	}
}

// upstream: crates/edit/src/modes/hashline/patcher.rs: "shifted_stale_tag_over_reveal_cap_reads_current_numbers"
func TestShiftedStaleTagOverRevealCapNamesCurrentNumbers(t *testing.T) {
	src := numberedLines(60)
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("long.ts", src)
	tag0 := w.snapshot("long.ts", src, []uint32{1, 2, 3, 4, 5})
	if _, _, err := w.apply(fmt.Sprintf("[long.ts#%s]\nPUT >3:\n+// ins a\n+// ins b\n", tag0)); err != nil {
		t.Fatal(err)
	}
	head, _ := w.store.Head(filepath.Join(w.dir, "long.ts"))
	_, _, err := w.apply(fmt.Sprintf("[long.ts#%s]\nCUT 10.=55\n", tag0))
	if err == nil || !strings.Contains(err.Error(), "`long.ts:12-57`") || !strings.Contains(err.Error(), "#"+head.Hash+"]") {
		t.Fatalf("expected live numbers 12-57 under #%s, got %v", head.Hash, err)
	}
}

// upstream: crates/edit/tests/hashline_parity.rs: "edit_results_carry_prior_provenance_past_line_neutral_hunks"
func TestEditResultsCarryPriorProvenancePastLineNeutralHunks(t *testing.T) {
	src := numberedLines(40)
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("a.txt", src)
	tag := w.snapshot("a.txt", src, allU32(40))
	if _, _, err := w.apply(fmt.Sprintf("[a.txt#%s]\nPUT >10:\n+new\nCUT 20.=20", tag)); err != nil {
		t.Fatal(err)
	}
	after, _ := w.readFile("a.txt")
	tag2 := FileHash(after)
	if _, _, err := w.apply(fmt.Sprintf("[a.txt#%s]\nPUT 16.=16:\n+LINE16", tag2)); err == nil || !strings.Contains(err.Error(), "lines 16") {
		t.Fatalf("line 16 shifted, should reject: %v", err)
	}
	if _, _, err := w.apply(fmt.Sprintf("[a.txt#%s]\nPUT 35.=35:\n+LINE35", tag2)); err != nil {
		t.Fatalf("line 35 kept number and content past line-neutral hunk: %v", err)
	}
}

// upstream: crates/edit/tests/hashline_parity.rs: "coding_agent_hashline_executor_cases_run_through_session" (sandbox tag recovery)
func TestSandboxTagPathRecoveryRebindsWithinSandboxOnly(t *testing.T) {
	sandbox := t.TempDir()
	w := newTestWorkspace(t)
	w.roots = []string{sandbox}
	artifact := filepath.Join(sandbox, "plan.md")
	src := "# Plan\n\n- old\n"
	if err := os.WriteFile(artifact, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	tag := w.store.Record(artifact, src, nil, false)
	out, _, err := w.apply(fmt.Sprintf("[plan.md#%s]\nPUT 3.=3:\n+- new\n", tag))
	if err != nil {
		t.Fatalf("sandbox recovery failed: %v", err)
	}
	if !strings.Contains(out, "does not exist") {
		t.Fatalf("expected recovery warning in:\n%s", out)
	}
	b, _ := os.ReadFile(artifact)
	if string(b) != "# Plan\n\n- new\n" {
		t.Fatalf("artifact=%q", string(b))
	}
}

// upstream: crates/edit/tests/hashline_patcher.rs: "refuses_a_short_body_that_restates_both_neighbours_and_applies_the_resend"
func TestRefusesShortBodyRestatingBothNeighboursAndAppliesResend(t *testing.T) {
	const probe = "class Probe {\n\talive = true;\n\n\tconstructor() {}\n\n\tisAlive(): boolean {\n\t\treturn this.alive;\n\t}\n}\n"
	w := newTestWorkspace(t)
	w.writeFile("probe.ts", probe)
	tag := w.snapshot("probe.ts", probe, nil)
	_, _, err := w.apply(fmt.Sprintf("[probe.ts#%s]\nPUT 3.=5:\n+\talive = true;\n+\n+\tisAlive(): boolean {\n", tag))
	if err == nil || !strings.Contains(err.Error(), "`PUT 3.=5:` rejected: the body restates both") || !strings.Contains(err.Error(), "they leave out lines 4-5") {
		t.Fatalf("err=%v", err)
	}
	if got, _ := w.readFile("probe.ts"); got != probe {
		t.Fatalf("probe.ts modified on refusal: %q", got)
	}
	if _, _, err := w.apply(fmt.Sprintf("[probe.ts#%s]\nPUT 3.=5:\n+\n", tag)); err != nil {
		t.Fatalf("resend failed: %v", err)
	}
}

// upstream: crates/edit/tests/hashline_patcher.rs: "streaming_preview_keeps_completed_section_when_trailing_header_arrives"
func TestPreviewPatchStreamingAndFinal(t *testing.T) {
	w := newTestWorkspace(t)
	w.writeFile("a.txt", "a\n")
	w.writeFile("b.txt", "b\n")
	ta := w.snapshot("a.txt", "a\n", nil)
	tb := w.snapshot("b.txt", "b\n", nil)
	files := &DiskFileSource{WorkDir: w.dir}

	patch, err := ParsePatchInput(fmt.Sprintf("[a.txt#%s]\nPUT 1.=1:\n+A\n[b.txt#%s]", ta, tb), SplitOptions{Cwd: w.dir})
	if err != nil {
		t.Fatal(err)
	}
	prev := PreviewPatch(patch, true, files, w.store)
	if len(prev) != 1 || prev[0].Display != "a.txt" || !strings.Contains(prev[0].Diff, "+1|A") {
		t.Fatalf("streaming preview=%+v", prev)
	}

	// Completed preview surfaces unknown tag error.
	p2, err := ParsePatchInput("[a.txt#FFFF]\nPUT 1.=1:\n+A\n", SplitOptions{Cwd: w.dir})
	if err != nil {
		t.Fatal(err)
	}
	prev2 := PreviewPatch(p2, false, files, w.store)
	if len(prev2) != 1 || !strings.Contains(prev2[0].Error, "not from this session") {
		t.Fatalf("final preview=%+v", prev2)
	}
}

// upstream: crates/edit/tests/hashline_patcher.rs: "multi_file_reveal_carries_a_later_tag_rejection" & "multi_file_reveal_omits_a_paste_of_a_rejected_cut"
func TestMultiFileRevealLaterRejectionAndOmittedPaste(t *testing.T) {
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("one.ts", numberedLines(30))
	w.writeFile("two.ts", strings.ReplaceAll(numberedLines(30), "line ", "row "))
	one := w.snapshot("one.ts", numberedLines(30), allU32(10))
	_ = w.snapshot("two.ts", strings.ReplaceAll(numberedLines(30), "line ", "row "), allU32(10))

	_, _, err := w.apply(fmt.Sprintf("[one.ts#%s]\nPUT 20.=20:\n+X\n[two.ts#BAD0]\nPUT 5.=5:\n+Y\n", one))
	if err == nil || !strings.Contains(err.Error(), "  20:line 20") || !strings.Contains(err.Error(), "[two.ts]: ") {
		t.Fatalf("err=%v", err)
	}

	// Rejected cut in one.ts suppresses bogus empty-paste error in two.ts; resend succeeds.
	w2 := newTestWorkspace(t)
	w2.opts.EnforceSeenLines = true
	w2.writeFile("one.ts", numberedLines(30))
	w2.writeFile("two.ts", strings.ReplaceAll(numberedLines(30), "line ", "row "))
	one2 := w2.snapshot("one.ts", numberedLines(30), allU32(10))
	two2 := w2.snapshot("two.ts", strings.ReplaceAll(numberedLines(30), "line ", "row "), allU32(10))
	crossPatch := fmt.Sprintf("[one.ts#%s]\nCUT 20.=20\n[two.ts#%s]\nPUT <1\n", one2, two2)
	_, _, err = w2.apply(crossPatch)
	if err == nil || !strings.Contains(err.Error(), "  20:line 20") || strings.Contains(err.Error(), EmptyPasteMessage) {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := w2.apply(crossPatch); err != nil {
		t.Fatalf("resend failed: %v", err)
	}
	gotTwo, _ := w2.readFile("two.ts")
	if !strings.HasPrefix(gotTwo, "line 20\nrow 1\n") {
		t.Fatalf("two.ts=%q", gotTwo)
	}
}

// Cross-call named clipboard register persistence across separate edit calls.
func TestCrossCallNamedRegistersPersist(t *testing.T) {
	w := newTestWorkspace(t)
	w.writeFile("a.txt", "one\ntwo\nthree\n")
	w.writeFile("b.txt", "alpha\nbeta\n")
	ta := w.snapshot("a.txt", "one\ntwo\nthree\n", nil)
	tb := w.snapshot("b.txt", "alpha\nbeta\n", nil)

	if _, _, err := w.apply(fmt.Sprintf("[a.txt#%s]\nCUT 2.=2 @saved\n", ta)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.apply(fmt.Sprintf("[b.txt#%s]\nPUT >1 @saved\n", tb)); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.readFile("b.txt"); got != "alpha\ntwo\nbeta\n" {
		t.Fatalf("b.txt=%q", got)
	}
}

// upstream: crates/edit/src/text.rs: lone \r preserved in file edits
func TestLoneCarriageReturnPreservedAcrossEdit(t *testing.T) {
	w := newTestWorkspace(t)
	orig := "10%\rprogress 100%\ndone\n"
	w.writeFile("log.txt", orig)
	tag := w.snapshot("log.txt", orig, nil)
	if _, _, err := w.apply(fmt.Sprintf("[log.txt#%s]\nPUT >$:\n+tail\n", tag)); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.readFile("log.txt"); got != "10%\rprogress 100%\ndone\ntail\n" {
		t.Fatalf("lone CR corrupted: %q", got)
	}
}
