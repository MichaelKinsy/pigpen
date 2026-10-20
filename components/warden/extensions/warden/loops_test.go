package warden

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Twins of tests/loops.test.ts (pi-warden a12b2703). The compaction-appendix and recall cases at the end of that file
// are twinned in compact_test.go with the rest of the appendix.

const loopsNow = int64(1768910400000) // 2026-01-20T12:00:00.000Z

func applyLoop(store LoopStore, in map[string]any) loopReply { return applyLoopAction(store, in, loopsNow) }

func addedLoops(t *testing.T, texts ...string) LoopStore {
	t.Helper()
	store := emptyLoopStore()
	for _, text := range texts {
		r := applyLoop(store, map[string]any{"action": "add", "text": text})
		if r.Store == nil {
			t.Fatalf("add %q: %s", text, r.Reply)
		}
		store = *r.Store
	}
	return store
}

func openIDs(store LoopStore) []int {
	var ids []int
	for _, l := range openLoops(store) {
		ids = append(ids, l.ID)
	}
	return ids
}

// twin: tests/loops.test.ts:17
func TestLoopsAddDoneDropList(t *testing.T) {
	first := applyLoop(emptyLoopStore(), map[string]any{"action": "add", "text": "Bump the version", "when": "after CI passes"})
	if first.Reply != "added #1: Bump the version (when: after CI passes)" {
		t.Fatalf("reply %q", first.Reply)
	}
	second := applyLoop(*first.Store, map[string]any{"action": "add", "text": "Rerun the flaky upload test"})
	third := applyLoop(*second.Store, map[string]any{"action": "add", "text": "Reply to the review comment"})
	want := strings.Join([]string{"Open loops:", "- #1 Bump the version (when: after CI passes)", "- #2 Rerun the flaky upload test", "- #3 Reply to the review comment"}, "\n")
	if got := applyLoop(*third.Store, map[string]any{"action": "list"}).Reply; got != want {
		t.Fatalf("list %q", got)
	}
	done := applyLoop(*third.Store, map[string]any{"action": "done", "id": float64(2)})
	if done.Reply != "done #2: Rerun the flaky upload test" {
		t.Fatalf("done %q", done.Reply)
	}
	if got := applyLoop(*done.Store, map[string]any{"action": "done", "id": float64(2)}).Reply; got != "#2 is already done" {
		t.Fatalf("again %q", got)
	}
	if got := applyLoop(*done.Store, map[string]any{"action": "drop", "id": float64(3)}).Reply; got != "not dropped: say why #3 is no longer needed" {
		t.Fatalf("drop without reason %q", got)
	}
	dropped := applyLoop(*done.Store, map[string]any{"action": "drop", "id": "#3", "reason": "the reviewer withdrew it"})
	if dropped.Reply != "dropped #3: Reply to the review comment" {
		t.Fatalf("drop %q", dropped.Reply)
	}
	if ids := openIDs(*dropped.Store); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("open %v", ids)
	}
	if got := applyLoop(*dropped.Store, map[string]any{"action": "list"}).Reply; got != "Open loops:\n- #1 Bump the version (when: after CI passes)\n(2 closed)" {
		t.Fatalf("list after %q", got)
	}
	if got := applyLoop(*dropped.Store, map[string]any{"action": "done", "id": float64(9)}).Reply; got != "no loop #9 in this session" {
		t.Fatalf("missing %q", got)
	}
	if got := formatLoopsForUser(*dropped.Store); !strings.Contains(got, "Closed (2):\n- #2 Rerun the flaky upload test: done\n- #3 Reply to the review comment: dropped, the reviewer withdrew it") {
		t.Fatalf("for user %q", got)
	}
	if got := applyLoop(emptyLoopStore(), map[string]any{"action": "list"}).Reply; got != "No open loops." {
		t.Fatalf("empty list %q", got)
	}
}

// twin: tests/loops.test.ts:38
func TestLoopsAddRefusesEmptyOrOverLongAndUnknownActionChangesNothing(t *testing.T) {
	r := applyLoop(emptyLoopStore(), map[string]any{"action": "add", "text": "  "})
	if r.Reply != "not added: give the loop's text" || r.Store != nil {
		t.Fatalf("blank %+v", r)
	}
	if r := applyLoop(emptyLoopStore(), map[string]any{"action": "add", "text": strings.Repeat("x", loopChars+1)}); !strings.HasPrefix(r.Reply, "not added: the text and the condition are at most 160") {
		t.Fatalf("long %q", r.Reply)
	}
	if r := applyLoop(emptyLoopStore(), map[string]any{"action": "remember"}); r.Store != nil || r.Reply != "unknown action; use one of add, done, drop, list" {
		t.Fatalf("unknown %+v", r)
	}
	r = applyLoop(emptyLoopStore(), map[string]any{"action": "add", "text": "Rotate the token=abcd1234abcd1234abcd1234 later"})
	if strings.Contains(r.Store.Loops[0].Text, "abcd1234abcd1234abcd1234") {
		t.Fatalf("not redacted: %q", r.Store.Loops[0].Text)
	}
}

// twin: tests/loops.test.ts:45
func TestLoopsOpenListIsCappedAtEightItemsAndSixHundredCharacters(t *testing.T) {
	var texts []string
	for i := 1; i <= 12; i++ {
		texts = append(texts, "Follow up on item "+itoaInt(i))
	}
	text := formatOpenLoops(addedLoops(t, texts...).Loops)
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- #") {
			n++
		}
	}
	if n != loopsShown || !strings.HasSuffix(text, "- … and 4 more (warden_loops list)") {
		t.Fatalf("shown %d: %q", n, text)
	}
	var long []string
	for i := 0; i < 8; i++ {
		long = append(long, strings.Repeat("Check the migration notes carefully ", 4)+itoaInt(i))
	}
	capped := formatOpenLoops(addedLoops(t, long...).Loops)
	if utf16Len(capped) > loopsChars || !strings.HasSuffix(capped, "more (warden_loops list)") {
		t.Fatalf("capped (%d): %q", utf16Len(capped), capped)
	}
	if formatOpenLoops(nil) != "" {
		t.Fatal("empty list is not empty")
	}
}

// twin: tests/loops.test.ts:57
func TestLoopsFingerprintChangesOnlyWhenTheOpenListChanges(t *testing.T) {
	store := addedLoops(t, "Bump the version", "Rerun the flaky test")
	clone := LoopStore{Next: store.Next, Loops: append([]Loop(nil), store.Loops...)}
	if loopsFingerprint(store.Loops) != loopsFingerprint(clone.Loops) {
		t.Fatal("a copy has another fingerprint")
	}
	closed := applyLoop(store, map[string]any{"action": "done", "id": float64(1)}).Store
	if loopsFingerprint(store.Loops) == loopsFingerprint(closed.Loops) {
		t.Fatal("closing did not change the fingerprint")
	}
	more := applyLoop(store, map[string]any{"action": "add", "text": "Tag the release"}).Store
	if loopsFingerprint(store.Loops) == loopsFingerprint(more.Loops) {
		t.Fatal("adding did not change the fingerprint")
	}
}

// twin: tests/loops.test.ts:64
func TestLoopsIsolationOneFilePerSessionAndProjectAndCorruptFileFailsClosed(t *testing.T) {
	data := filepath.Join(t.TempDir(), "pigpen-warden")
	a := loopsPath("/work/app", "session-a", data)
	if filepath.Dir(filepath.Dir(a)) != filepath.Join(data, "loops") {
		t.Fatalf("path %s", a)
	}
	if a == loopsPath("/work/app", "session-b", data) {
		t.Fatal("another session shares the file")
	}
	if a == loopsPath("/work/other", "session-a", data) {
		t.Fatal("another project shares the file")
	}
	if filepath.Dir(loopsPath("/work/app", "../../escape", data)) != filepath.Dir(a) {
		t.Fatal("a session id left the project folder")
	}
	store := addedLoops(t, "Bump the version")
	if err := writeLoops(a, store); err != nil {
		t.Fatal(err)
	}
	got, err := readLoops(a)
	if err != nil || len(got.Loops) != 1 || got.Loops[0].Text != "Bump the version" || got.Next != 2 {
		t.Fatalf("read %+v %v", got, err)
	}
	for _, other := range []string{loopsPath("/work/app", "session-b", data), loopsPath("/work/other", "session-a", data)} {
		if s, err := readLoops(other); err != nil || len(s.Loops) != 0 || s.Next != 1 {
			t.Fatalf("read %s: %+v %v", other, s, err)
		}
	}
	raw, _ := os.ReadFile(a)
	if !strings.Contains(string(raw), `"text": "Bump the version"`) {
		t.Fatalf("file %s", raw)
	}
	if err := os.WriteFile(a, []byte("{ torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLoops(a); err == nil {
		t.Fatal("a torn file read as an empty store")
	}
}

// twin: tests/loops.test.ts:88
func TestLoopsConcurrentOperationsNeitherThrowNorLoseAnUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loops.json")
	if _, err := updateLoops(path, func(s LoopStore) loopReply { return applyLoopAction(s, map[string]any{"action": "add", "text": "Bump the version"}, loopsNow) }); err != nil {
		t.Fatal(err)
	}
	replies := make([]string, 2)
	var wg sync.WaitGroup
	changes := []map[string]any{{"action": "add", "text": "Rerun the flaky test"}, {"action": "drop", "id": float64(1), "reason": "the reviewer withdrew it"}}
	for i := range changes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := updateLoops(path, func(s LoopStore) loopReply { return applyLoopAction(s, changes[i], loopsNow) })
			if err != nil {
				t.Error(err)
			}
			replies[i] = r
		}()
	}
	wg.Wait()
	// The two calls ran in turn, in either order: each saw the other's result or its own.
	seen := map[string]bool{replies[0]: true, replies[1]: true}
	if !seen["added #2: Rerun the flaky test"] || !seen["dropped #1: Bump the version"] {
		t.Fatalf("replies %q", replies)
	}
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := updateLoops(path, func(s LoopStore) loopReply {
				return applyLoopAction(s, map[string]any{"action": "add", "text": "Follow up on item " + itoaInt(i+1)}, loopsNow)
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	store, err := readLoops(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Loops) != 22 || store.Next != 23 {
		t.Fatalf("loops %d next %d", len(store.Loops), store.Next)
	}
	seenIDs := map[int]bool{}
	dropped := 0
	for _, l := range store.Loops {
		seenIDs[l.ID] = true
		if l.Status == "dropped" {
			dropped++
			if l.ID != 1 {
				t.Fatalf("dropped #%d", l.ID)
			}
		}
	}
	for id := 1; id <= 22; id++ {
		if !seenIDs[id] {
			t.Fatalf("no loop #%d", id)
		}
	}
	if dropped != 1 {
		t.Fatalf("dropped %d", dropped)
	}
}
