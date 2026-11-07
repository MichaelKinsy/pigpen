package posthorse

// Tests of the reminder lookup and the retained-tail count (reminders.go): the retained span read from
// the projection alone (review R5) against the full branch walk of reminders.ts and against Pi 1.5.0, the
// leaf read (R6), the guarded cache (R8) and the hybrid ancestry walk (R7).

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// roOldRetainedEntryIDSet is reminders.ts:204-221 as the port had it before R5: the ids of the branch entries
// from the newest compaction's firstKeptEntryId up to it. Kept as the oracle of the differential tests.
func roOldRetainedEntryIDSet(branch []rec) map[string]struct{} {
	compactionIdx := -1
	for i := len(branch) - 1; i >= 0; i-- {
		if entryType(branch[i]) == "compaction" {
			compactionIdx = i
			break
		}
	}
	if compactionIdx <= 0 {
		return nil
	}
	firstKept, ok := str(branch[compactionIdx], "firstKeptEntryId")
	if !ok || firstKept == "" {
		return nil
	}
	firstKeptIdx := -1
	for i, entry := range branch {
		if id, ok := str(entry, "id"); ok && id == firstKept {
			firstKeptIdx = i
			break
		}
	}
	if firstKeptIdx < 0 || firstKeptIdx >= compactionIdx {
		return nil
	}
	ids := map[string]struct{}{}
	for _, entry := range branch[firstKeptIdx:compactionIdx] {
		if id, ok := str(entry, "id"); ok && id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids
}

// roOldCount is reminders.ts:223-244 and 263-288 over the full branch and the projection.
func roOldCount(branch []rec, projected []sdk.ProjectedSessionEntry, stale staleReminder) int {
	retained := roOldRetainedEntryIDSet(branch)
	if len(retained) == 0 {
		return 0
	}
	count := 0
	for _, item := range projected {
		id, _ := str(item.SourceEntry, "id")
		if _, ok := retained[id]; !ok {
			continue
		}
		for _, message := range item.Messages {
			if role, _ := str(message, "role"); role != "system" && (stale == nil || !stale(message)) {
				count++
			}
		}
	}
	return count
}

// roStaleFlagged is the stale predicate of the generated cases: a reminder whose details carry stale:true.
func roStaleFlagged(message rec) bool {
	role, _ := str(message, "role")
	details, _ := message["details"].(map[string]any)
	return role == "custom" && message["customType"] == "posthorse-reminder" && details["stale"] == true
}

func TestRetainedProjectionCountMatchesPi(t *testing.T) {
	raw, err := os.ReadFile("testdata/retained_count_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Branch     []rec                       `json:"branch"`
		Projection []sdk.ProjectedSessionEntry `json:"projection"`
		Count      int                         `json:"count"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	nonzero := 0
	for i, c := range cases {
		got := countRetainedFromProjected(retainedProjection(c.Projection), roStaleFlagged)
		if got != c.Count {
			t.Errorf("case %d: projection count %d, Pi 1.5.0 countRetainedMessages %d", i, got, c.Count)
		}
		if old := roOldCount(c.Branch, c.Projection, roStaleFlagged); old != c.Count {
			t.Errorf("case %d: the branch-walk count of the old port %d, Pi 1.5.0 %d", i, old, c.Count)
		}
		if c.Count > 0 {
			nonzero++
		}
	}
	if nonzero < 20 {
		t.Errorf("the cases must cover retained tails, only %d have one", nonzero)
	}
}

// roProjectionOf is a fakeSession's projection as the SDK decodes it.
func roProjectionOf(t *testing.T, s *fakeSession) []sdk.ProjectedSessionEntry {
	t.Helper()
	raw, err := json.Marshal(s.buildSessionProjection()["entries"])
	if err != nil {
		t.Fatal(err)
	}
	var entries []sdk.ProjectedSessionEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestRetainedProjectionCountMatchesBranchWalk compares the projection-only count with the old full-walk
// count on seeded random sessions of the fixture (compactions at the root, of themselves, of missing ids,
// several of them, system messages, labels, branch navigation and summaries, edits).
func TestRetainedProjectionCountMatchesBranchWalk(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	nonzero := 0
	for n := range 400 {
		s := fxMemorySession("")
		var ids []string
		pick := func() string {
			if len(ids) == 0 {
				return ""
			}
			return ids[rng.IntN(len(ids))]
		}
		for step := range 3 + rng.IntN(25) {
			var id string
			switch rng.IntN(14) {
			case 0, 1:
				id = s.appendMessage(fxUser(fmt.Sprintf("u%d", step)))
			case 2, 3:
				id = s.appendMessage(fxAssistant([]any{fxText(fmt.Sprintf("a%d", step))}))
			case 4:
				id = s.appendMessage(rec{"role": "system", "content": "sys", "timestamp": float64(1)})
			case 5:
				id = s.appendCustomMessageEntry("posthorse-reminder", "r", true, rec{"stale": rng.IntN(2) == 0})
			case 6:
				id = s.appendCustomMessageEntry("todo", "c", rng.IntN(2) == 0, nil)
			case 7:
				if len(ids) > 0 {
					id = s.appendLabelChange(pick(), "l")
				}
			case 8, 9:
				first := pick()
				if rng.IntN(5) == 0 {
					first = ""
				}
				var details rec
				if rng.IntN(2) == 0 {
					details = rec{"posthorse": float64(1)}
				}
				id = s.appendCompaction("sum", first, 10, details)
			case 10:
				if len(ids) > 0 {
					s.branch(pick())
				}
			case 11:
				if len(ids) > 0 {
					id = s.branchWithSummary(pick(), "bs")
				}
			case 12:
				id = s.appendThinkingLevelChange("high")
			default:
				if len(ids) > 0 && rng.IntN(2) == 0 {
					id, _ = s.appendContextEdit(pick(), rec{"content": "edited"})
				} else {
					id = s.appendCustomEntry("other", rec{})
				}
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		projection := roProjectionOf(t, s)
		branch := s.getBranch(nil)
		want := roOldCount(branch, projection, roStaleFlagged)
		got := countRetainedFromProjected(retainedProjection(projection), roStaleFlagged)
		if got != want {
			t.Fatalf("session %d: projection count %d, full-walk count %d\nbranch: %v", n, got, want, branch)
		}
		if want > 0 {
			nonzero++
		}
	}
	if nonzero < 40 {
		t.Errorf("the shapes must cover retained tails, only %d have one", nonzero)
	}
}

// TestRetainedTailContextNeedsNoBranchRead is the cost of R5: a window opened by a retained-tail rollover
// had every `context` event read the whole branch (317 ms with 20,000 earlier entries over the wire).
// The count now needs the projection only: no getBranch, one projection read per event.
func TestRetainedTailContextNeedsNoBranchRead(t *testing.T) {
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	session := fxMemorySession("")
	for range 10_000 {
		session.appendMessage(fxUser("earlier"))
		session.appendMessage(fxAssistant([]any{fxText("earlier reply")}))
	}
	kept := session.appendMessage(fxUser("kept ask"))
	session.appendMessage(fxAssistant([]any{fxText("kept reply")}))
	session.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
	f := newFixture(t, withSession(session), withRealProjection(),
		withHostError("sessionRead.getBranch", "a retained-tail context must not read the branch"))
	boundary := rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)}
	user, assistant := fxUser("kept ask"), fxAssistant([]any{fxText("kept reply")})
	const events = 3
	for range events {
		got := f.ContextEvent(boundary, user, assistant, fxUser("after"))
		roSameJSON(t, got["messages"], []rec{user, assistant, boundary, fxUser("after")}, "retained tail moved ahead of the record")
	}
	roEqual(t, roGetBranchReads(f), 0, "no branch read")
	roEqual(t, f.Reads("sessionRead.buildSessionProjection"), 1, "one projection read, then the cache serves the events")
	roEqual(t, f.Reads("sessionRead.getLeafEntry"), events, "one leaf read per context event")
}

// TestReminderReadUsesLeafEntry is R6: the leaf is one getLeafEntry read, not getLeafId plus getEntry.
func TestReminderReadUsesLeafEntry(t *testing.T) {
	session := fxIndexedSession(rec{"type": "context_window", "id": "window"}, rec{"type": "custom", "id": "x"})
	f := newFixture(t, withUsage(100_000, 76_000), withSession(session), withEmptyProjection())
	f.TurnEnd()
	f.ResetReads()
	f.TurnEnd()
	roEqual(t, f.Reads("sessionRead.getLeafEntry"), 1, "one leaf read per certified lookup")
	roEqual(t, f.Reads("sessionRead.getEntry"), 0, "no by-id read for an unchanged leaf")
	roEqual(t, f.Reads("sessionRead.getLeafId"), 0, "no separate leaf id read")
}

// roSession is an in-memory reminderSession that counts its reads and lets a test hook each one.
type roSession struct {
	*roMemorySession
	entries    atomic.Int64
	branches   atomic.Int64
	leafReads  atomic.Int64
	beforeRead func()
}

func (s *roSession) GetEntry(id string) (map[string]any, error) {
	s.entries.Add(1)
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return s.roMemorySession.GetEntry(id)
}

func (s *roSession) GetLeafEntry() (map[string]any, error) {
	s.leafReads.Add(1)
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return s.roMemorySession.GetLeafEntry()
}

func (s *roSession) GetBranch(from *string) ([]map[string]any, error) {
	s.branches.Add(1)
	return s.roMemorySession.GetBranch(from)
}

// TestReminderLookupResetDuringReadStoresNothing is R8: a lookup that overlaps a reset returns its result to
// its caller but must not cache a state computed for the branch the reset dropped.
func TestReminderLookupResetDuringReadStoresNothing(t *testing.T) {
	session := &roSession{roMemorySession: roLongSession(6, -1)}
	lookup := createReminderLookup()
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	session.beforeRead = func() {
		once.Do(func() { close(reached) })
		<-release
	}
	done := make(chan error, 1)
	go func() { _, err := lookup.read(session); done <- err }()
	<-reached
	lookup.reset()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	lookup.mu.Lock()
	stored := lookup.state
	lookup.mu.Unlock()
	if stored != nil {
		t.Errorf("a lookup that began before a reset stored its state: %+v", stored)
	}
	// Without a reset in between the result is stored and certified.
	session.beforeRead = nil
	if _, err := lookup.read(session); err != nil {
		t.Fatal(err)
	}
	session.entries.Store(0)
	if _, err := lookup.read(session); err != nil || session.entries.Load() != 0 {
		t.Errorf("an unchanged leaf must be certified from the cache (%d by-id reads, %v)", session.entries.Load(), err)
	}
}

// TestReminderLookupConcurrentReadsAndResets runs under -race: lookups, resets and a moving leaf at once.
func TestReminderLookupConcurrentReadsAndResets(t *testing.T) {
	long := roLongSession(300, 100)
	session := &roSession{roMemorySession: long}
	lookup := createReminderLookup()
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				if g%2 == 0 {
					lookup.reset()
					continue
				}
				state, err := lookup.read(session)
				if err != nil || state.windowId != "e199" {
					t.Errorf("read %d: window %q, %v", i, state.windowId, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// roShapedSession builds a branch of n entries from a seeded recipe: custom entries, reminders, native and
// Posthorse compactions (with firstKeptEntryId of an earlier entry, of itself, null or missing), windows.
func roShapedSession(seed int64, n int) *roMemorySession {
	rng := rand.New(rand.NewPCG(uint64(seed), 1))
	s := &roMemorySession{id: "shaped", entries: map[string]rec{}}
	var parent any
	for i := range n {
		id := fmt.Sprintf("e%d", i)
		entry := rec{"id": id, "parentId": parent, "type": "custom", "customType": "old"}
		earlier := fmt.Sprintf("e%d", rng.IntN(i+1))
		switch rng.IntN(12) {
		case 0, 1, 2:
			entry["type"], entry["customType"], entry["details"] = "custom_message", "posthorse-reminder", rec{"windowId": "w"}
		case 3:
			entry["type"], entry["summary"], entry["firstKeptEntryId"] = "compaction", "s", earlier
		case 4:
			entry["type"], entry["summary"], entry["firstKeptEntryId"] = "compaction", "s", id
		case 5:
			entry["type"], entry["summary"], entry["firstKeptEntryId"] = "compaction", "s", nil
			entry["details"] = rec{"posthorse": float64(1)}
		case 6:
			entry["type"], entry["summary"] = "compaction", "s"
			entry["details"] = rec{"posthorse": float64(1)}
		case 7:
			entry["type"], entry["handoff"] = "context_window", "h"
		}
		s.entries[id] = entry
		parent = id
		s.leaf = id
	}
	return s
}

// TestHybridAncestryWalkMatchesByIDWalk is R7: after K by-id reads the walk fetches the rest of the ancestry
// with one GetBranch, with the same windowId and reminders as the by-id walk to the root, on every shape,
// with and without a certified previous leaf; and it reads at most K entries by id plus one branch.
func TestHybridAncestryWalkMatchesByIDWalk(t *testing.T) {
	if reminderBranchAfter != 0 {
		t.Errorf("the hybrid walk is off by default (reminderBranchAfter = %d)", reminderBranchAfter)
	}
	for seed := range int64(300) {
		n := 1 + int(seed%40)
		full := roShapedSession(seed, n)
		leaf := full.entries[full.leaf]
		byID := &roSession{roMemorySession: full}
		wantWindow, wantReminders, err := scanAncestry(byID, leaf, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []int{1, 2, 3, 5, 8, 100} {
			hybrid := &roSession{roMemorySession: full}
			window, reminders, err := scanAncestry(hybrid, leaf, nil, k)
			if err != nil {
				t.Fatal(err)
			}
			if window != wantWindow || !reflect.DeepEqual(reminders, wantReminders) {
				t.Fatalf("seed %d, K=%d: hybrid (%q, %d reminders), by-id (%q, %d reminders)", seed, k, window, len(reminders), wantWindow, len(wantReminders))
			}
			if hybrid.entries.Load() > int64(k) || hybrid.branches.Load() > 1 {
				t.Fatalf("seed %d, K=%d: %d by-id reads and %d branch reads", seed, k, hybrid.entries.Load(), hybrid.branches.Load())
			}
		}
		// A certified previous leaf (an append to a cached branch) ends the walk the same way.
		if n > 4 {
			prevLeaf := full.entries[fmt.Sprintf("e%d", n-3)]
			previous := &reminderState{leaf: prevLeaf, windowId: "cached", reminders: []rec{{"id": "cached-reminder"}}}
			wantWindow, wantReminders, _ := scanAncestry(&roSession{roMemorySession: full}, leaf, previous, 0)
			for _, k := range []int{1, 2, 100} {
				window, reminders, err := scanAncestry(&roSession{roMemorySession: full}, leaf, previous, k)
				if err != nil || window != wantWindow || !reflect.DeepEqual(reminders, wantReminders) {
					t.Fatalf("seed %d, K=%d with a certified leaf: hybrid (%q, %v), by-id (%q, %v) (%v)", seed, k, window, reminders, wantWindow, wantReminders, err)
				}
			}
		}
	}
}

// TestHybridAncestryWalkStopsAtADanglingParent: a missing parent ends the walk in both modes.
func TestHybridAncestryWalkStopsAtADanglingParent(t *testing.T) {
	s := roLongSession(10, -1)
	delete(s.entries, "e4")
	leaf := s.entries[s.leaf]
	want, wantReminders, _ := scanAncestry(&roSession{roMemorySession: s}, leaf, nil, 0)
	got, gotReminders, err := scanAncestry(&roSession{roMemorySession: s}, leaf, nil, 2)
	if err != nil || got != want || !reflect.DeepEqual(gotReminders, wantReminders) {
		t.Errorf("hybrid (%q, %v), by-id (%q, %v) (%v)", got, gotReminders, want, wantReminders, err)
	}
}
