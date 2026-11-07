package posthorse

// Layer-1 twins of the native-host history cases of test/native-host.ts that count work instead of
// asserting a result (the original counts JSON.stringify calls and content accessors): the
// invocation-time visibility snapshot across archive I/O (native-host.ts:1257), and bounds on the
// bytes allocated while searching and reading (native-host.ts:1143, 1225, 1353, 1382). The bounds are
// relative to the cost of flattening the entries involved once, so they fail when an entry's
// content is rendered again or when the scan extracts entries it does not need.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allocated is the bytes the Go heap handed out while fn ran (cumulative, so GC does not matter).
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestHistoryAllSearchKeepsInvocationTimeContextVisibility(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the archive I/O gate is a shell script over named pipes")
	}
	dir := t.TempDir()
	sess, err := fxPersistedSession(hlWD(t), dir)
	if err != nil {
		t.Fatal(err)
	}
	id := sess.appendMessage(rec{"role": "user", "content": "SNAPSHOT_NEEDLE original.", "timestamp": float64(1)})
	sess.appendMessage(fxAssistant("Acknowledged."))

	// The prefilter runs before the archive is read, after the visibility snapshot of an all-session
	// search is taken: a fake rg that stops there stands for "archive I/O is pending".
	bin := t.TempDir()
	started, release, gate := filepath.Join(bin, "started"), filepath.Join(bin, "release"), filepath.Join(bin, "gate")
	for _, fifo := range []string{started, release} {
		if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
			t.Skipf("no mkfifo: %v %s", err, out)
		}
	}
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	hlFakeRG(t, bin, `if [ -e "`+gate+`" ]; then echo go > "`+started+`"; read x < "`+release+`"; fi; exit 2`)
	t.Setenv("PATH", bin)

	f := hlFixture(t, withSession(sess), withSessionDir(dir), withRealProjection())
	run := f.StartRun("history", rec{"op": "search", "query": "SNAPSHOT_NEEDLE", "all": true})
	if _, err := os.ReadFile(started); err != nil { // blocks until rg is running
		t.Fatal(err)
	}
	if _, err := sess.appendContextEdit(id, nil); err != nil {
		t.Fatal(err)
	}
	gateRelease, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = gateRelease.WriteString("go\n")
	gateRelease.Close()
	initial, failure := run.Wait()
	if failure != "" {
		t.Fatalf("search failed: %s", failure)
	}
	hlMustMatch(t, `No history matches`, initial.Text())
	if d := hlDetails(t, initial); d["kind"] != "history-search" || d["skipped"] != float64(1) {
		t.Errorf("the entry was in context when the search began: details %v", d)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	updated := f.Run("history", rec{"op": "search", "query": "SNAPSHOT_NEEDLE", "all": true})
	hlMustMatch(t, `\[user\] SNAPSHOT_NEEDLE original\.`, updated.Text())
	if skipped, _ := hlDetails(t, updated)["skipped"].(float64); skipped != 0 {
		t.Errorf("after the edit the entry is out of context: skipped %v", skipped)
	}
}

// toolCallEntry is an assistant message that calls an unrelated tool with a large argument object.
func toolCallEntry(id, parent string, args rec) rec {
	return rec{"type": "message", "id": id, "parentId": parent, "timestamp": id, "message": rec{
		"role": "assistant", "stopReason": "toolUse",
		"content": []any{rec{"type": "toolCall", "id": "call-" + id, "name": "unrelated", "arguments": args}},
	}}
}

func branchSource(entries []rec, projected []projectedEntry) historySource {
	branch := make([]entryLike, len(entries))
	for i, entry := range entries {
		branch[i] = entry
	}
	return historySource{
		branch:     func() ([]entryLike, error) { return branch, nil },
		projection: func() ([]projectedEntry, error) { return projected, nil },
	}
}

func searchBranch(t *testing.T, source historySource, params historyParams) historyMatchSet {
	t.Helper()
	matches, err := historyMatches(context.Background(), source, params, func(message string) error { return fmt.Errorf("%s", message) })
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// native-host.ts:1143: a search renders each unrelated tool call's arguments at most once per page.
func TestHistoryBranchSearchRendersUnrelatedArgumentsOnce(t *testing.T) {
	var entries []rec
	parent := ""
	add := func(entry rec) {
		if parent != "" {
			entry["parentId"] = parent
		} else {
			entry["parentId"] = nil
		}
		parent, _ = str(entry, "id")
		entries = append(entries, entry)
	}
	for i := range 5 {
		add(hlMsg(fmt.Sprintf("old%d", i), nil, fmt.Sprint(i), hlUser(fmt.Sprintf("PROJECTION_NEEDLE old:%d.", i))))
	}
	add(rec{"type": "context_window", "id": "window", "timestamp": "w", "handoff": "Earlier entries archived."})
	var unrelated []rec
	for i := range 64 {
		entry := toolCallEntry(fmt.Sprintf("call%02d", i), "", rec{"historyProjectionProbe": true, "payload": strings.Repeat("x", 65_536), "index": float64(i)})
		unrelated = append(unrelated, entry)
		add(entry)
	}
	visible := hlMsg("visible", nil, "100", hlUser("PROJECTION_NEEDLE visible."))
	add(visible)
	projected := []projectedEntry{{sourceEntry: visible, messages: []rec{{"role": "user", "content": "PROJECTION_NEEDLE visible."}}}}
	source := branchSource(entries, projected)

	oncePerEntry := allocated(func() {
		for _, entry := range unrelated {
			flattenEntry(entry)
		}
	})
	bound := oncePerEntry + oncePerEntry/2 + 512<<10
	first := searchBranchAlloc(t, source, historyParams{op: "search", query: "PROJECTION_NEEDLE", hasQuery: true, limit: hlInt(2)}, bound, "first page")
	if len(first.hits) < 2 || !strings.Contains(first.hits[0].id, "old4") || !strings.Contains(first.hits[1].id, "old3") || first.skipped != 1 {
		t.Fatalf("first page = %+v", first)
	}
	second := searchBranchAlloc(t, source, historyParams{op: "search", query: "PROJECTION_NEEDLE", hasQuery: true, limit: hlInt(2), cursor: cursorOf(t, first, 1)}, bound, "second page")
	if len(second.hits) < 1 || !strings.Contains(second.hits[0].id, "old2") || second.skipped != 1 {
		t.Fatalf("second page = %+v", second)
	}
	searchBranchAlloc(t, source, historyParams{op: "search", query: "NO_MATCH_102", hasQuery: true}, bound, "no-match search")
}

func cursorOf(t *testing.T, matches historyMatchSet, hitIndex int) string {
	t.Helper()
	hit := matches.hits[hitIndex]
	cursor, err := cursorFor(matches, hit, u16len(hit.text))
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func searchBranchAlloc(t *testing.T, source historySource, params historyParams, bound uint64, what string) (matches historyMatchSet) {
	t.Helper()
	if params.limit == nil {
		params.limit = hlInt(10)
	}
	used := allocated(func() { matches = searchBranch(t, source, params) })
	t.Logf("%s allocated %d KiB (bound %d KiB)", what, used>>10, bound>>10)
	if used > bound {
		t.Errorf("%s allocated %d KiB, bound %d KiB: an entry's content was rendered more than once", what, used>>10, bound>>10)
	}
	return matches
}

// native-host.ts:1225: edits that share one target are rendered a bounded number of times.
func TestHistorySearchRendersASharedEditTargetOnlyOnce(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all=%v", all), func(t *testing.T) {
			target := hlMsg("target", nil, "1", rec{"role": "assistant", "content": "Before edits."})
			entries := []rec{target}
			var edits []rec
			var last any
			parent := "target"
			for i := range 8 {
				args := rec{"value": "MATCH_EDIT_ARGUMENT", "payload": strings.Repeat("x", 4096)}
				last = []any{rec{"type": "toolCall", "id": "same-call", "name": "unrelated", "arguments": args}}
				edit := rec{"type": "context_edit", "id": fmt.Sprintf("edit%d", i), "parentId": parent, "timestamp": "e", "targetId": "target", "replacement": rec{"content": last}}
				parent = fmt.Sprintf("edit%d", i)
				edits = append(edits, edit)
				entries = append(entries, edit)
			}
			// Pi projects the target with the content of its last edit.
			projected := []projectedEntry{{sourceEntry: target, messages: []rec{{"role": "assistant", "content": last}}}}
			oncePerEdit := allocated(func() {
				for _, edit := range edits {
					flattenEntry(edit)
				}
			})
			bound := 3*oncePerEdit + 256<<10
			params := historyParams{op: "search", query: "MATCH_EDIT_ARGUMENT", hasQuery: true, limit: hlInt(10)}
			var matches historyMatchSet
			var used uint64
			if all {
				dir := t.TempDir()
				file := filepath.Join(dir, "current.jsonl")
				hlArchive(t, file, hlLines(append([]rec{target}, edits...)...))
				source := hlSource(t, dir, file)
				source.projection = func() ([]projectedEntry, error) { return projected, nil }
				params.all = true
				bound += 1 << 20 // the archive scan's read buffer
				used = allocated(func() { matches = searchBranch(t, source, params) })
			} else {
				used = allocated(func() { matches = searchBranch(t, branchSource(entries, projected), params) })
			}
			t.Logf("allocated %d KiB (once per edit %d KiB)", used>>10, oncePerEdit>>10)
			if len(matches.hits) != 0 || matches.skipped != 8 {
				t.Errorf("every edit is in context: hits %d skipped %d", len(matches.hits), matches.skipped)
			}
			if used > bound {
				t.Errorf("allocated %d KiB, bound %d KiB", used>>10, bound>>10)
			}
		})
	}
}

// bigBranch is the original's countedArchive: n user messages with a large body, a window in the middle.
func bigBranch(n int) []rec {
	entries := make([]rec, 0, n)
	body := strings.Repeat("b", 8_000)
	parent := any(nil)
	for i := range n {
		id := fmt.Sprintf("old%04d", i)
		entries = append(entries, rec{"type": "message", "id": id, "parentId": parent, "timestamp": fmt.Sprint(i),
			"message": rec{"role": "user", "content": fmt.Sprintf("HIST_NEEDLE old:%d. %s", i, body)}})
		parent = id
	}
	return entries
}

// native-host.ts:1353: a first page and its lookahead extract about page-size entries of a large archive.
func TestHistorySearchExtractsOnlyEarlyPageHits(t *testing.T) {
	const n = 4096
	source := branchSource(bigBranch(n), nil)
	perEntry := allocated(func() {
		entry := rec{"type": "message", "id": "x", "message": rec{"role": "user", "content": "HIST_NEEDLE old:1. " + strings.Repeat("b", 8_000)}}
		flattenEntry(entry)
	})
	// A page of ten, the lookahead and the entries the first page skips (here none) extract ~11 entries;
	// the whole archive would take n times a single entry.
	bound := 40*perEntry + 1<<20
	first := searchBranchAlloc(t, source, historyParams{op: "search", query: "HIST_NEEDLE", hasQuery: true, limit: hlInt(10)}, bound, "first page")
	if len(first.hits) != 11 || !strings.HasPrefix(first.hits[0].id, "old4095") {
		ids := make([]string, len(first.hits))
		for i, h := range first.hits {
			ids[i] = h.id
		}
		t.Fatalf("first page hits %v", ids)
	}
	if limit := uint64(n) * perEntry / 4; bound >= limit {
		t.Fatalf("the bound (%d KiB) must stay far below extracting every entry (%d KiB)", bound>>10, limit>>10)
	}
	second := searchBranchAlloc(t, source, historyParams{op: "search", query: "HIST_NEEDLE", hasQuery: true, limit: hlInt(10), cursor: cursorOf(t, first, 9)}, bound, "second page")
	if len(second.hits) == 0 || !strings.HasPrefix(second.hits[0].id, "old4085") {
		t.Fatalf("second page = %+v", second.hits)
	}
	oldest := searchBranchAlloc(t, source, historyParams{op: "search", query: "old:0.", hasQuery: true, limit: hlInt(10)}, uint64(n)*perEntry*2, "oldest entry")
	if len(oldest.hits) != 1 || !strings.HasPrefix(oldest.hits[0].id, "old0000") {
		t.Errorf("oldest = %+v", oldest.hits)
	}
}

// native-host.ts:1382: a read extracts the selected entry only.
func TestHistoryReadExtractsOnlyTheSelectedEntry(t *testing.T) {
	const n = 4096
	source := branchSource(bigBranch(n), nil)
	perEntry := allocated(func() {
		flattenEntry(rec{"type": "message", "id": "x", "message": rec{"role": "user", "content": "HIST_NEEDLE old:1. " + strings.Repeat("b", 8_000)}})
	})
	for _, id := range []string{"old4095", "old0000"} {
		var located locatedEntry
		used := allocated(func() {
			var err error
			if located, err = findEntry(context.Background(), source, id); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("read %s allocated %d KiB (one entry %d KiB)", id, used>>10, perEntry>>10)
		if bound := 8*perEntry + 1<<20; used > bound {
			t.Errorf("read %s allocated %d KiB, bound %d KiB: unselected entries were extracted", id, used>>10, bound>>10)
		}
		if located.item.windowId != "initial" || !strings.HasPrefix(located.item.text, "[user] HIST_NEEDLE old:") {
			t.Errorf("located %+v", located.item.windowId)
		}
	}
}
