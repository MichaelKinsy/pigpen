package hashline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Additional tests covering:
// - packages/coding-agent/test/edit-tool-details.test.ts (move permissions, pruned snapshots, details)
// - crates/edit/src/modes/hashline/patcher.rs (remaining lifecycle & rescue cases)
// - crates/edit/src/store.rs (remaining eviction & unicode budget tests)

// upstream: crates/edit/src/store.rs: "rewrite_past_the_diff_bound_carries_only_the_unchanged_head_and_tail"
func TestStoreRewritePastDiffBoundCarriesHeadAndTail(t *testing.T) {
	store := NewStore()
	count := 3 * carryMaxEdits
	origRows := make([]string, count)
	rewrittenRows := make([]string, count)
	all := make([]uint32, count)
	for i := 1; i <= count; i++ {
		origRows[i-1] = "line " + itoa(i)
		all[i-1] = uint32(i)
		if i > 100 && i <= count-100 && i%2 == 0 {
			rewrittenRows[i-1] = itoa(i) + "!"
		} else {
			rewrittenRows[i-1] = origRows[i-1]
		}
	}
	store.Record("f.txt", strings.Join(origRows, "\n"), all, true)
	store.Record("f.txt", strings.Join(rewrittenRows, "\n"), []uint32{0}, true)
	head, _ := store.Head("f.txt")
	if !head.SeenLines[0] || !head.SeenLines[1] || !head.SeenLines[101] || head.SeenLines[102] {
		t.Fatalf("head prefix unexpected: 1=%v 101=%v 102=%v", head.SeenLines[1], head.SeenLines[101], head.SeenLines[102])
	}
	if !head.SeenLines[count-99] || !head.SeenLines[count] {
		t.Fatalf("head tail unexpected: %d=%v %d=%v", count-99, head.SeenLines[count-99], count, head.SeenLines[count])
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// upstream: crates/edit/src/store.rs: "zero_limits_reject_snapshots_and_clear_restores_default_limits"
func TestStoreZeroLimitsAndClearRestoresDefaults(t *testing.T) {
	for _, lim := range [][3]int{{0, 2, 10}, {10, 0, 1}, {10, 2, 0}} {
		store := NewStoreWithLimits(lim[0], lim[1], lim[2])
		store.Record("empty", "", nil, false)
		if _, ok := store.Head("empty"); ok {
			t.Fatalf("lim=%v: expected empty rejected", lim)
		}
		store.Relocate("empty", "moved")
		store.Clear()
		store.Record("after-clear", "😀", nil, false)
		head, ok := store.Head("after-clear")
		if !ok || head.Text != "😀" {
			t.Fatalf("lim=%v: after clear head=%+v ok=%v", lim, head, ok)
		}
	}
}

// upstream: crates/edit/src/store.rs: "invalidation_and_eviction_release_their_budget"
func TestStoreInvalidationReleasesBudget(t *testing.T) {
	// Tight byte budget: fits two ~1200-2400 byte entries, evicting the oldest when exceeded,
	// and invalidating an entry releases its budget so a replacement fits alongside `last`.
	store := NewStoreWithLimits(10, 2, 4200)
	store.Record("old", strings.Repeat("😀", 600), nil, false)
	store.Record("next", strings.Repeat("é", 1200), nil, false)
	if _, ok := store.Head("old"); ok {
		t.Fatal("expected old evicted by byte budget")
	}
	store.Record("last", strings.Repeat("x", 1000), nil, false)
	if _, ok := store.Head("next"); !ok {
		t.Fatal("expected next retained")
	}
	if _, ok := store.Head("last"); !ok {
		t.Fatal("expected last retained")
	}
	store.Invalidate("next")
	store.Invalidate("next") // absent history releases nothing
	store.Record("replacement", strings.Repeat("😀", 600), nil, false)
	if _, ok := store.Head("last"); !ok {
		t.Fatal("expected last retained after invalidation freed budget")
	}
	if _, ok := store.Head("replacement"); !ok {
		t.Fatal("expected replacement retained after invalidation freed budget")
	}
}

// upstream: crates/edit/src/modes/hashline/patcher.rs: "insert_after_shifted_line_stays_rejected" & "cut_on_shifted_line_stays_rejected"
func TestShiftedInsertCutAndRestoredRowStayRejected(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		b.WriteString("let v00")
		if i < 10 {
			b.WriteByte('0')
		}
		b.WriteString(itoa(i))
		b.WriteString(" = ")
		b.WriteString(itoa(i))
		b.WriteString(";\n")
	}
	src := b.String()

	for _, op := range []string{"PUT >18:\n+// later insert\n", "CUT 20.=20\n"} {
		w := newTestWorkspace(t)
		w.opts.EnforceSeenLines = true
		w.writeFile("c.ts", src)
		tag0 := w.snapshot("c.ts", src, allU32(30))
		if _, _, err := w.apply("[c.ts#" + tag0 + "]\nPUT >5:\n+// ins a\n+// ins b\n"); err != nil {
			t.Fatal(err)
		}
		head, _ := w.store.Head(filepath.Join(w.dir, "c.ts"))
		if _, _, err := w.apply("[c.ts#" + head.Hash + "]\n" + op); err == nil || !strings.Contains(err.Error(), "never displayed") {
			t.Fatalf("%q: expected unseen error, got %v", op, err)
		}
	}

	// Restored rows after insert + top cut: ops without payload stay rejected.
	for _, op := range []string{"CUT 22", "PUT <22:\n+// before", "PUT >22:\n+// after"} {
		w := newTestWorkspace(t)
		w.opts.EnforceSeenLines = true
		w.writeFile("r.ts", src)
		tag0 := w.snapshot("r.ts", src, allU32(30))
		if _, _, err := w.apply("[r.ts#" + tag0 + "]\nPUT >5:\n+// ins a\n+// ins b\n"); err != nil {
			t.Fatal(err)
		}
		h1, _ := w.store.Head(filepath.Join(w.dir, "r.ts"))
		if _, _, err := w.apply("[r.ts#" + h1.Hash + "]\nCUT 1.=2\n"); err != nil {
			t.Fatal(err)
		}
		h2, _ := w.store.Head(filepath.Join(w.dir, "r.ts"))
		if _, _, err := w.apply("[r.ts#" + h2.Hash + "]\n" + op + "\n"); err == nil || !strings.Contains(err.Error(), "never displayed") {
			t.Fatalf("restored %q: expected unseen error, got %v", op, err)
		}
	}
}

// upstream: crates/edit/src/modes/hashline/patcher.rs: "stale_range_after_upward_shift_stays_rejected" & "faithful_range_after_upward_shift_applies"
func TestUpwardShiftRangeRescue(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		b.WriteString("let v00")
		if i < 10 {
			b.WriteByte('0')
		}
		b.WriteString(itoa(i))
		b.WriteString(" = ")
		b.WriteString(itoa(i))
		b.WriteString(";\n")
	}
	src := b.String()

	w1 := newTestWorkspace(t)
	w1.opts.EnforceSeenLines = true
	w1.writeFile("i.ts", src)
	t0 := w1.snapshot("i.ts", src, allU32(30))
	if _, _, err := w1.apply("[i.ts#" + t0 + "]\nCUT 3.=4\n"); err != nil {
		t.Fatal(err)
	}
	h1, _ := w1.store.Head(filepath.Join(w1.dir, "i.ts"))
	if _, _, err := w1.apply("[i.ts#" + h1.Hash + "]\nPUT 18.=21:\n+let v0018 = 180;\n+let v0019 = 19;\n+let v0020 = 20;\n+let v0021 = 210;\n"); err == nil || !strings.Contains(err.Error(), "never displayed") {
		t.Fatalf("stale upward shift should reject, got %v", err)
	}

	w2 := newTestWorkspace(t)
	w2.opts.EnforceSeenLines = true
	w2.writeFile("j.ts", src)
	t0b := w2.snapshot("j.ts", src, allU32(30))
	if _, _, err := w2.apply("[j.ts#" + t0b + "]\nCUT 3.=4\n"); err != nil {
		t.Fatal(err)
	}
	h2, _ := w2.store.Head(filepath.Join(w2.dir, "j.ts"))
	if _, _, err := w2.apply("[j.ts#" + h2.Hash + "]\nPUT 16.=19:\n+let v0018 = 18; // a\n+let v0019 = 19;\n+let v0020 = 20;\n+let v0021 = 21; // b\n"); err != nil {
		t.Fatalf("faithful upward shift should apply: %v", err)
	}
}

// upstream: crates/edit/src/modes/hashline/patcher.rs: "unseen_anchor_without_shift_history_stays_rejected"
func TestUnseenAnchorWithoutShiftHistoryStaysRejected(t *testing.T) {
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	src := numberedLines(30)
	w.writeFile("e.ts", src)
	t0 := w.snapshot("e.ts", src, allU32(10))
	if _, _, err := w.apply("[e.ts#" + t0 + "]\nPUT 25.=25:\n+line 25 // edited\n"); err == nil || !strings.Contains(err.Error(), "never displayed") {
		t.Fatalf("unseen without shift should reject, got %v", err)
	}
}

// upstream: crates/edit/src/session.rs: "snapshot_pruning_uses_combined_budget"
func TestPruneSnapshotsUsesCombinedBudget(t *testing.T) {
	big := strings.Repeat("x", MaxEditSnapshotTextChars)
	o, ho, n, hn, pruned := PruneSnapshots(big, true, "", false)
	if !ho || hn || pruned || o != big {
		t.Fatalf("exact budget should not prune: ho=%v hn=%v pruned=%v", ho, hn, pruned)
	}
	_, ho, _, hn, pruned = PruneSnapshots(big, true, "y", true)
	if ho || hn || !pruned || n != "" {
		t.Fatalf("over budget should prune both: ho=%v hn=%v pruned=%v", ho, hn, pruned)
	}
}

// upstream: crates/edit/tests/hashline_patcher.rs: "mismatch_reply_with_no_rows_leaves_every_line_unseen"
func TestMismatchReplyWithNoRowsLeavesEveryLineUnseen(t *testing.T) {
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	src := numberedLines(20)
	w.writeFile("a.txt", src)
	_, _, err := w.apply("[a.txt#0000]\nPUT 25.=25:\n+X\n")
	tag := extractMismatchTag(t, err)
	if strings.Contains(err.Error(), ":line ") {
		t.Fatalf("expected no context rows: %s", err.Error())
	}
	if _, _, err := w.apply("[a.txt#" + tag + "]\nPUT 10.=10:\n+Y\n"); err == nil || !strings.Contains(err.Error(), "never displayed") {
		t.Fatalf("line 10 should remain unseen: %v", err)
	}
}

// upstream: crates/edit/tests/hashline_patcher.rs: "each_mismatch_reply_adds_the_rows_it_displayed"
func TestEachMismatchReplyAddsDisplayedRows(t *testing.T) {
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	src := numberedLines(30)
	w.writeFile("a.txt", src)
	_, _, _ = w.apply("[a.txt#0000]\nPUT 5.=5:\n+X\n")
	_, _, err := w.apply("[a.txt#0000]\nPUT 20.=20:\n+X\n")
	tag := extractMismatchTag(t, err)
	if _, _, err := w.apply("[a.txt#" + tag + "]\nPUT 12.=12:\n+Y\n"); err == nil {
		t.Fatal("line 12 was never displayed")
	}
	if _, _, err := w.apply("[a.txt#" + tag + "]\nPUT 20.=20:\n+Y\n"); err != nil {
		t.Fatalf("line 20 was displayed in second reply: %v", err)
	}
}

// upstream: crates/edit/src/modes/hashline/patcher.rs: "stale_tag_edit_of_a_cut_line_requires_full_read" & "shifted_stale_tag_cut_line_reads_its_current_number"
func TestStaleTagWideLineReadsCurrentNumber(t *testing.T) {
	wide := "note: " + strings.Repeat("abcdefghij", 80) + " END-OF-LINE-TAIL"
	orig := wide + "\ntwo\nthree\nfour\nfive\nsix\n"
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("cut.txt", orig)
	tag0 := w.snapshot("cut.txt", orig, []uint32{0, 2, 3, 4, 5, 6})
	if _, _, err := w.apply("[cut.txt#" + tag0 + "]\nPUT <1:\n+head a\n+head b\n"); err != nil {
		t.Fatal(err)
	}
	head, _ := w.store.Head(filepath.Join(w.dir, "cut.txt"))
	_, _, err := w.apply("[cut.txt#" + tag0 + "]\nPUT 1.=1:\n+NOTE: " + wide[6:768] + "\n")
	if err == nil || !strings.Contains(err.Error(), "cut.txt:3:raw") || !strings.Contains(err.Error(), "#"+head.Hash+"]") {
		t.Fatalf("expected cut.txt:3:raw under #%s, got %v", head.Hash, err)
	}
}

// upstream: crates/edit/tests/hashline_patcher.rs: "head_insert_still_applies_across_a_stale_tag"
func TestHeadInsertWithMoveAppliesAcrossStaleTag(t *testing.T) {
	w := newTestWorkspace(t)
	w.writeFile("a.txt", "current\n")
	tag := w.snapshot("a.txt", "old\n", nil)
	out, _, err := w.apply("[a.txt#" + tag + "]\nPUT <1:\n+head\nMV b.txt\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.readFile("a.txt"); ok {
		t.Fatal("a.txt should have moved")
	}
	if got, _ := w.readFile("b.txt"); got != "head\ncurrent\n" {
		t.Fatalf("b.txt=%q", got)
	}
	if !strings.Contains(out, "despite a stale snapshot tag") {
		t.Fatalf("out=%s", out)
	}
}

// upstream: crates/edit/tests/hashline_parity.rs: "block_replace_cases_resolve_syntax_end_to_end"
func TestBlockReplaceAndCutEndToEnd(t *testing.T) {
	const src = "function x() {\n  if (y) {\n  }\n}\n"
	w := newTestWorkspace(t)
	w.writeFile("x.ts", src)
	tag := w.snapshot("x.ts", src, nil)
	out, _, err := w.apply("[x.ts#" + tag + "]\nPUT 1*:\n+function x() {\n+  return 42;\n+}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PUT 1*: → resolved lines 1-4 (4 lines)") {
		t.Fatalf("missing block resolution header:\n%s", out)
	}
	if got, _ := w.readFile("x.ts"); got != "function x() {\n  return 42;\n}\n" {
		t.Fatalf("got %q", got)
	}

	w2 := newTestWorkspace(t)
	w2.writeFile("x.ts", src)
	tag2 := w2.snapshot("x.ts", src, nil)
	out2, _, err := w2.apply("[x.ts#" + tag2 + "]\nCUT 2*")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "CUT 2* → resolved lines 2-3 (2 lines)") {
		t.Fatalf("out2=%s", out2)
	}
	if got, _ := w2.readFile("x.ts"); got != "function x() {\n}\n" {
		t.Fatalf("got %q", got)
	}
}

// Verify REM on an empty directory is rejected cleanly (review bug #34).
func TestRemOnDirectoryIsRejected(t *testing.T) {
	w := newTestWorkspace(t)
	dirPath := filepath.Join(w.dir, "empty-dir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.apply("[empty-dir]\nREM\n"); err == nil {
		t.Fatal("expected error deleting directory")
	}
	if _, err := os.Stat(dirPath); err != nil {
		t.Fatalf("empty-dir must still exist: %v", err)
	}
}

// Verify undecodable (invalid UTF-8) file refuses edits but allows tagged REM.
func TestInvalidUtf8RefusesEditAllowsTaggedRem(t *testing.T) {
	w := newTestWorkspace(t)
	p := filepath.Join(w.dir, "bin.dat")
	if err := os.WriteFile(p, []byte{0xff, 0xfe, 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := w.apply("[bin.dat#ABCD]\nPUT 1.=1:\n+text\n")
	if err == nil || !err.IsInvalidUtf8() || err.Kind() != "invalid_utf8" {
		t.Fatalf("expected invalid_utf8 error, got %v", err)
	}
	if _, _, err := w.apply("[bin.dat#ABCD]\nREM\n"); err != nil {
		t.Fatalf("tagged REM on binary file should succeed: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("bin.dat should be deleted")
	}
	_ = runtime.GOOS
}
