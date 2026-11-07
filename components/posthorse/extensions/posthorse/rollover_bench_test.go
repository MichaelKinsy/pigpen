package posthorse

// Benchmarks of the hot paths of the rollover lane: the reminder lookup on a 20,000-entry branch (the
// algorithm over an in-memory by-id reader, and the whole turn_end through the fake host), the leaf
// certification, the `context` hook, and the retained-tail cut.

import (
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
)

// roMemorySession is a reminderSession over a map: GetEntry costs nothing, so the benchmark measures the
// scan itself (the TS reads are in-process too).
type roMemorySession struct {
	id      string
	entries map[string]rec
	leaf    string
}

func (s *roMemorySession) GetSessionID() (string, error)         { return s.id, nil }
func (s *roMemorySession) GetSessionFile() (*string, error)      { return nil, nil }
func (s *roMemorySession) GetLeafEntry() (map[string]any, error) { return s.entries[s.leaf], nil }
func (s *roMemorySession) GetBranch(from *string) ([]map[string]any, error) {
	var path []map[string]any
	for e := s.entries[*from]; e != nil; {
		path = append(path, e)
		parent, _ := e["parentId"].(string)
		e = s.entries[parent]
	}
	slices.Reverse(path)
	return path, nil
}
func (s *roMemorySession) GetEntry(id string) (map[string]any, error) {
	return s.entries[id], nil
}

// roLongSession is a branch of n custom entries, with a Posthorse window `windowDepth` entries below the leaf
// (windowDepth < 0: no window, so a cold lookup reaches the root).
func roLongSession(n, windowDepth int) *roMemorySession {
	s := &roMemorySession{id: "bench", entries: make(map[string]rec, n)}
	parent := any(nil)
	for i := range n {
		id := "e" + strconv.Itoa(i)
		entry := rec{"type": "custom", "id": id, "parentId": parent, "timestamp": "2026-01-01T00:00:00Z", "customType": "old", "data": rec{}}
		if windowDepth >= 0 && i == n-1-windowDepth {
			entry = rec{"type": "compaction", "id": id, "parentId": parent, "summary": "carry on", "firstKeptEntryId": id, "tokensBefore": float64(1), "details": rec{"posthorse": float64(1)}}
		}
		s.entries[id] = entry
		parent = id
		s.leaf = id
	}
	return s
}

func BenchmarkReminderLookupCold20000NoWindow(b *testing.B) {
	session := roLongSession(20_000, -1)
	lookup := createReminderLookup()
	b.ReportAllocs()
	for b.Loop() {
		lookup.reset()
		if _, err := lookup.read(session); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReminderLookupCold20000WindowNearLeaf(b *testing.B) {
	session := roLongSession(20_000, 3)
	lookup := createReminderLookup()
	b.ReportAllocs()
	for b.Loop() {
		lookup.reset()
		if _, err := lookup.read(session); err != nil {
			b.Fatal(err)
		}
	}
}

// The steady state: one by-id certification of an unchanged leaf.
func BenchmarkReminderLookupCertified20000(b *testing.B) {
	session := roLongSession(20_000, -1)
	lookup := createReminderLookup()
	if _, err := lookup.read(session); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := lookup.read(session); err != nil {
			b.Fatal(err)
		}
	}
}

// benchMessages is n context messages; with reminders, every tenth is a stale reminder and the first is a window marker.
func benchMessages(n int, reminders bool) []any {
	messages := make([]any, 0, n+1)
	if reminders {
		messages = append(messages, roMessage("context-window", "new", rec{"windowId": "new"}))
	}
	for i := range n {
		switch {
		case reminders && i%10 == 0:
			messages = append(messages, roMessage("posthorse-reminder", "stale", roDetails("old", 100_000, 16_384)))
		case i%2 == 0:
			messages = append(messages, rec{"role": "user", "content": "hello " + strconv.Itoa(i)})
		default:
			messages = append(messages, rec{"role": "assistant", "content": []any{rec{"type": "text", "text": "world " + strconv.Itoa(i)}}})
		}
	}
	return messages
}

// The reminder-free context hook: the scan that decides to leave the messages alone.
func BenchmarkContextHookNoReminder1000(b *testing.B) {
	messages := benchMessages(1000, false)
	b.ReportAllocs()
	for b.Loop() {
		present, marker := scanReminderMessages(messages)
		if present || marker != nil {
			b.Fatal("unexpected reminder")
		}
		if filtered, changed := dropStale(messages, nil); changed || len(filtered) != len(messages) {
			b.Fatal("unexpected filtering")
		}
	}
}

// The context hook with stale reminders: scan, predicate and filtered copy.
func BenchmarkContextHookStaleReminders1000(b *testing.B) {
	messages := benchMessages(1000, true)
	fingerprint := reminderFingerprint{windowId: "new", contextWindow: new(100_000), reserveTokens: new(16_384)}
	stale := func(message rec) bool {
		role, _ := str(message, "role")
		return role == "custom" && isReminderType(message["customType"]) && !reminderMatches(message["details"], fingerprint)
	}
	b.ReportAllocs()
	for b.Loop() {
		present, marker := scanReminderMessages(messages)
		if !present || marker == nil {
			b.Fatal("reminder not found")
		}
		if filtered, changed := dropStale(messages, stale); !changed || len(filtered) != 901 {
			b.Fatalf("filtered %d", len(filtered))
		}
	}
}

// TestRolloverWireCosts runs whole hooks through the fake host (JSON framing and decoding included) a fixed number of times and
// logs what they cost (an upper bound: it includes the fixture's own work). It is a test because the fixture needs a *testing.T; the sessions are
// ten times smaller than the benchmarks' so that it stays fast under the race detector (the fixture answers a request within 10 s).
func TestRolloverWireCosts(t *testing.T) {
	const runs = 2
	report := func(name string, run func()) {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		for range runs {
			run()
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		t.Logf("%-40s %10d ns/op %10d B/op %8d allocs/op (N=%d)", name, elapsed.Nanoseconds()/runs, (after.TotalAlloc-before.TotalAlloc)/runs, (after.Mallocs-before.Mallocs)/runs, runs)
	}
	manager := fxMemorySession("")
	for range 2_000 {
		manager.appendCustomEntry("old", rec{})
	}
	f := newFixture(t, withUsage(100_000, 76_000), withSession(manager), withEmptyProjection())
	report("turn_end cold lookup, 2,000 entries", func() {
		f.FireAll("session_tree", rec{})
		f.TurnEnd()
	})
	report("turn_end certified leaf", func() { f.TurnEnd() })
	// A window opened by a retained-tail rollover over 2,000 earlier entries: every context event.
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	tail := fxMemorySession("")
	for range 1_000 {
		tail.appendMessage(fxUser("earlier"))
		tail.appendMessage(fxAssistant([]any{fxText("earlier reply")}))
	}
	kept := tail.appendMessage(fxUser("kept ask"))
	tail.appendMessage(fxAssistant([]any{fxText("kept reply")}))
	tail.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
	h := newFixture(t, withSession(tail), withRealProjection())
	boundary := rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)}
	report("context, retained tail over 2,000 entries", func() {
		h.ContextEvent(boundary, fxUser("kept ask"), fxAssistant([]any{fxText("kept reply")}), fxUser("after"))
	})
	plain, stale := benchMessages(1000, false), benchMessages(1000, true)
	g := newFixture(t)
	toRecs := func(list []any) []rec {
		out := make([]rec, len(list))
		for i, m := range list {
			out[i] = m.(rec)
		}
		return out
	}
	plainMessages, staleMessages := toRecs(plain), toRecs(stale)
	report("context, 1000 messages, no reminder", func() { g.ContextEvent(plainMessages...) })
	report("context, 1000 messages, 100 stale", func() { g.ContextEvent(staleMessages...) })
}

// BenchmarkRetainedTailCut is retention.go's projectedCut over n projected entries.
func BenchmarkRetainedTailCut(b *testing.B) {
	const n = 5000
	entries := make([]projectedEntry, n)
	for i := range n {
		message := rec{"role": "user", "content": "message number " + strconv.Itoa(i) + " with some words in it"}
		if i%2 == 1 {
			message = rec{"role": "assistant", "content": []any{rec{"type": "text", "text": "reply " + strconv.Itoa(i)}}}
		}
		entries[i] = projectedEntry{sourceEntry: rec{"type": "message", "id": "m" + strconv.Itoa(i)}, messages: []rec{message}}
	}
	b.ReportAllocs()
	for b.Loop() {
		if start := projectedCut(entries, 20_000); start < 0 {
			b.Fatal("no cut")
		}
	}
}
