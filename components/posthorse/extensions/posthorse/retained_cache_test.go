package posthorse

// Tests of the retained-count cache (reminders.go retainedCache): at every step of a session that grows, branches and
// is edited, the cached count equals the count read fresh from the projection (reminders.ts:263-288), and a step
// that must not read the projection does not.

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// roStaleReminders is the stale predicate of the generated cases, as a staleReminder.
var roStaleReminders staleReminder = roStaleFlagged

// retainedStep runs the cached count and the fresh count on the same session and checks they agree. It returns
// the number of projection reads the cached count made.
type retainedHarness struct {
	t     *testing.T
	f     *fixture
	call  func(func(sdk.Context))
	cache *retainedCache
}

func newRetainedHarness(t *testing.T, session *fakeSession) *retainedHarness {
	t.Helper()
	f, call := roWithCtx(t, withSession(session), withRealProjection())
	return &retainedHarness{t: t, f: f, call: call, cache: newRetainedCache()}
}

func (h *retainedHarness) count(name string, stale staleReminder, wantProjectionReads int) int {
	h.t.Helper()
	var cached, fresh int
	var reads int
	h.call(func(ctx sdk.Context) {
		before := h.f.Reads("sessionRead.buildSessionProjection")
		var err error
		if cached, err = h.cache.count(ctx.SessionManager(), stale); err != nil {
			h.t.Fatalf("%s: %v", name, err)
		}
		reads = h.f.Reads("sessionRead.buildSessionProjection") - before
		if fresh, err = newRetainedCache().count(ctx.SessionManager(), stale); err != nil {
			h.t.Fatalf("%s: %v", name, err)
		}
	})
	if cached != fresh {
		h.t.Fatalf("%s: cached count %d, fresh count %d", name, cached, fresh)
	}
	if reads != wantProjectionReads {
		h.t.Errorf("%s: %d projection reads, want %d", name, reads, wantProjectionReads)
	}
	return cached
}

func roRetainedSession() (*fakeSession, map[string]string) {
	s := fxMemorySession("")
	ids := map[string]string{}
	ids["u1"] = s.appendMessage(fxUser("first"))
	ids["a1"] = s.appendMessage(fxAssistant([]any{fxText("reply")}))
	ids["u2"] = s.appendMessage(fxUser("second"))
	ids["r"] = s.appendCustomMessageEntry("posthorse-reminder", "r", true, rec{"stale": true})
	ids["c1"] = s.appendCompaction("sum", ids["a1"], 100, rec{"posthorse": float64(1)})
	return s, ids
}

// A session that grows, is edited and compacted again: every step agrees with the fresh count.
func TestRetainedCacheAgreesWithTheProjection(t *testing.T) {
	s, ids := roRetainedSession()
	h := newRetainedHarness(t, s)
	// a1, u2 and the stale reminder are retained: two messages count.
	if got := h.count("first read", roStaleReminders, 1); got != 2 {
		t.Fatalf("retained count %d, want 2", got)
	}
	h.count("unchanged leaf", roStaleReminders, 0)
	s.appendMessage(fxUser("third"))
	h.count("one appended message", roStaleReminders, 0)
	s.appendMessage(fxAssistant([]any{fxText("more")}))
	s.appendCustomMessageEntry("intercom_message", "x", true, nil)
	s.appendLabelChange(ids["u1"], "label")
	s.appendThinkingLevelChange("high")
	h.count("several ordinary entries", roStaleReminders, 0)
	h.count("another stale rule", func(rec) bool { return false }, 0)
	h.count("every message stale", func(rec) bool { return true }, 0)

	// A context edit that removes a retained entry changes the count (a null replacement drops its messages).
	if _, err := s.appendContextEdit(ids["u2"], nil); err != nil {
		t.Fatal(err)
	}
	if got := h.count("a context_edit of a retained entry", roStaleReminders, 1); got != 1 {
		t.Errorf("after removing u2 the retained count is %d, want 1", got)
	}
	h.count("after the edit", roStaleReminders, 0)
	if _, err := s.appendContextEdit(ids["a1"], rec{"content": "edited"}); err != nil {
		t.Fatal(err)
	}
	h.count("a replacing context_edit", roStaleReminders, 1)

	// A new compaction starts another retained span.
	s.appendMessage(fxUser("fourth"))
	last := s.appendMessage(fxAssistant([]any{fxText("fifth")}))
	s.appendCompaction("sum 2", last, 100, rec{"posthorse": float64(1)})
	if got := h.count("a newer compaction", roStaleReminders, 1); got != 1 {
		t.Errorf("the newer span retains %d messages, want 1", got)
	}
	h.count("after the newer compaction", roStaleReminders, 0)

	// A native compaction that keeps nothing before it.
	s.appendCompaction("sum 3", "", 100, nil)
	if got := h.count("a compaction of itself", roStaleReminders, 1); got != 0 {
		t.Errorf("a compaction that keeps nothing retains %d messages, want 0", got)
	}
	h.count("and after it", roStaleReminders, 0)
}

// Navigation and other sessions: a leaf that is not a descendant of the cached one is a miss, with or without the reset.
func TestRetainedCacheLeavesTheCachedBranch(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := map[bool]string{false: "without the event", true: "with session_tree"}[reset]
		t.Run(name, func(t *testing.T) {
			s, ids := roRetainedSession()
			h := newRetainedHarness(t, s)
			h.count("first read", roStaleReminders, 1)
			s.appendMessage(fxUser("third"))
			h.count("a child of the cached leaf", roStaleReminders, 0)

			// Back to before the compaction: the span is gone.
			s.branch(ids["u2"])
			if reset {
				h.cache.reset()
			}
			if got := h.count("navigated before the compaction", roStaleReminders, 1); got != 0 {
				t.Errorf("before the compaction nothing is retained, got %d", got)
			}
			// Forward to the compaction on a new branch: a child of the cached (older) leaf misses nothing but is read once.
			s.branch(ids["c1"])
			if reset {
				h.cache.reset()
			}
			h.count("navigated back to the compaction", roStaleReminders, 1)
			// A sibling branch off the compaction.
			s.appendMessage(fxUser("on the new branch"))
			h.count("a child on the new branch", roStaleReminders, 0)
		})
	}
	t.Run("a different session", func(t *testing.T) {
		s, _ := roRetainedSession()
		h := newRetainedHarness(t, s)
		h.count("first read", roStaleReminders, 1)
		other := fxMemorySession("")
		other.appendMessage(fxUser("another session"))
		kept := other.appendMessage(fxAssistant([]any{fxText("kept")}))
		other.appendCompaction("sum", kept, 100, rec{"posthorse": float64(1)})
		h.f.Set(withSession(other))
		h.count("the resumed session", roStaleReminders, 1)
		h.count("and again", roStaleReminders, 0)
	})
	t.Run("an empty session is never cached", func(t *testing.T) {
		h := newRetainedHarness(t, fxMemorySession(""))
		h.count("empty", roStaleReminders, 1)
		h.count("empty again", roStaleReminders, 1)
	})
}

// A long run of appended entries is certified up to retainedCertifySteps and read again beyond it.
func TestRetainedCacheWalkIsBounded(t *testing.T) {
	s, _ := roRetainedSession()
	h := newRetainedHarness(t, s)
	h.count("first read", roStaleReminders, 1)
	for range retainedCertifySteps {
		s.appendMessage(fxUser("filler"))
	}
	h.count("a walk of the bound", roStaleReminders, 0)
	for range retainedCertifySteps + 2 {
		s.appendMessage(fxUser("filler"))
	}
	h.count("a walk beyond the bound", roStaleReminders, 1)
	h.count("certified again", roStaleReminders, 0)
}

// A reset between the lookup's start and its store forgets the result: the cache holds nothing for the dropped branch.
func TestRetainedCacheResetDuringACountStoresNothing(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	source := &roRetainedSource{leaf: rec{"id": "L", "type": "message"}}
	source.beforeProjection = func() { close(reached); <-release }
	cache := newRetainedCache()
	done := make(chan struct{})
	go func() { _, _ = cache.count(source, nil); close(done) }()
	<-reached
	cache.reset()
	close(release)
	<-done
	cache.mu.Lock()
	valid := cache.valid
	cache.mu.Unlock()
	if valid {
		t.Errorf("a count that overlapped a reset stored its result")
	}
}

// roRetainedSource is an in-memory retainedSource.
type roRetainedSource struct {
	leaf             rec
	entries          map[string]rec
	projection       sdk.SessionProjection
	projections      int
	reads            int
	beforeProjection func()
	failLeaf         error
	failEntry        error
}

func (s *roRetainedSource) GetLeafEntry() (map[string]any, error) {
	s.reads++
	return s.leaf, s.failLeaf
}
func (s *roRetainedSource) GetEntry(id string) (map[string]any, error) {
	s.reads++
	return s.entries[id], s.failEntry
}
func (s *roRetainedSource) BuildSessionProjection() (sdk.SessionProjection, error) {
	s.projections++
	if s.beforeProjection != nil {
		s.beforeProjection()
	}
	return s.projection, nil
}

// roRetainedWorld is a session of n entries before a compaction that retains kept messages, and a source over it.
func roRetainedWorld(n, kept int) (*roRetainedSource, func(appended int) rec) {
	s := &roRetainedSource{entries: map[string]rec{}}
	compaction := rec{"type": "compaction", "id": "C", "parentId": "p" + strconv.Itoa(n-1), "summary": "s", "firstKeptEntryId": "p" + strconv.Itoa(n-kept)}
	s.projection.Entries = []sdk.ProjectedSessionEntry{{SourceEntry: compaction, Messages: []map[string]any{{"role": "compactionSummary", "summary": "s"}}}}
	for i := n - kept; i < n; i++ {
		id := "p" + strconv.Itoa(i)
		parent := "p" + strconv.Itoa(i-1)
		s.projection.Entries = append(s.projection.Entries, sdk.ProjectedSessionEntry{
			SourceEntry: rec{"type": "message", "id": id, "parentId": parent},
			Messages:    []map[string]any{{"role": "user", "content": "kept"}},
		})
	}
	s.entries["C"] = compaction
	s.leaf = compaction
	// grow appends entries below the compaction and returns the new leaf.
	grow := func(appended int) rec {
		parent := "C"
		var entry rec
		for i := range appended {
			id := "n" + strconv.Itoa(i)
			entry = rec{"type": "message", "id": id, "parentId": parent, "message": rec{"role": "user", "content": "x"}}
			s.entries[id] = entry
			parent = id
		}
		return entry
	}
	return s, grow
}

// BenchmarkRetainedCountHit is the per-event cost of the retained count between compactions: a leaf read, the
// walk over the entries appended since, and the count over the cached messages.
func BenchmarkRetainedCountHit(b *testing.B) {
	for _, appended := range []int{0, 1, 4} {
		b.Run("appended="+strconv.Itoa(appended), func(b *testing.B) {
			source, grow := roRetainedWorld(20_000, 200)
			cache := newRetainedCache()
			if _, err := cache.count(source, roStaleReminders); err != nil {
				b.Fatal(err)
			}
			if appended > 0 {
				source.leaf = grow(appended)
			}
			b.ReportAllocs()
			for b.Loop() {
				// Re-certify from the cached leaf each time: the cache is reset to the compaction leaf.
				cache.mu.Lock()
				cache.leaf = source.entries["C"]
				cache.mu.Unlock()
				if got, err := cache.count(source, roStaleReminders); err != nil || got != 200 {
					b.Fatalf("count %d, %v", got, err)
				}
			}
		})
	}
}

// BenchmarkRetainedCountMiss is the cost the cache saves per event once decoded: counting a retained span of 200
// messages from a projection that is already in memory (the wire read and its decode come on top).
func BenchmarkRetainedCountMiss(b *testing.B) {
	source, _ := roRetainedWorld(20_000, 200)
	b.ReportAllocs()
	for b.Loop() {
		cache := newRetainedCache()
		if got, err := cache.count(source, roStaleReminders); err != nil || got != 200 {
			b.Fatalf("count %d, %v", got, err)
		}
	}
}

// The certification walk reads exactly the entries appended since the cached leaf, minus the one whose parent is it.
func TestRetainedCacheWalkReadsOnlyTheNewEntries(t *testing.T) {
	s, _ := roRetainedSession()
	h := newRetainedHarness(t, s)
	entries := func() int { return h.f.Reads("sessionRead.getEntry") }
	h.count("first read", roStaleReminders, 1)
	roEqual(t, entries(), 0, "a miss reads no entries by id")
	s.appendMessage(fxUser("one"))
	h.count("a child of the cached leaf", roStaleReminders, 0)
	roEqual(t, entries(), 0, "a child of the cached leaf needs no by-id read")
	s.appendMessage(fxUser("two"))
	s.appendMessage(fxUser("three"))
	s.appendMessage(fxUser("four"))
	h.count("three appended entries", roStaleReminders, 0)
	roEqual(t, entries(), 2, "three appended entries cost two by-id reads")
	h.count("the same leaf again", roStaleReminders, 0)
	roEqual(t, entries(), 2, "the certified leaf became the cached one: no walk")
}

// A reset forgets the cached span even when the leaf has not moved.
func TestRetainedCacheResetForcesARead(t *testing.T) {
	s, _ := roRetainedSession()
	h := newRetainedHarness(t, s)
	h.count("first read", roStaleReminders, 1)
	h.count("unchanged", roStaleReminders, 0)
	h.cache.reset()
	h.count("after the reset", roStaleReminders, 1)
	h.count("cached again", roStaleReminders, 0)
}

// session_start and session_tree reset the cache of the registered extension: the next `context` event reads the projection.
func TestRetainedCacheIsResetByTheSessionEvents(t *testing.T) {
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	for _, event := range []string{"session_start", "session_tree"} {
		t.Run(event, func(t *testing.T) {
			s, _ := roRetainedSession()
			f := newFixture(t, withSession(s), withRealProjection())
			boundary := rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)}
			fire := func() {
				f.ContextEvent(boundary, fxAssistant([]any{fxText("reply")}), fxUser("second"), fxUser("after"))
			}
			fire()
			roEqual(t, f.Reads("sessionRead.buildSessionProjection"), 1, "the first event reads the projection")
			fire()
			roEqual(t, f.Reads("sessionRead.buildSessionProjection"), 1, "the second is served from the cache")
			f.FireAll(event, rec{})
			fire()
			roEqual(t, f.Reads("sessionRead.buildSessionProjection"), 2, "the event reset the cache")
		})
	}
}

// Failures of the certification inputs are propagated, and a missing parent is a miss, not a certificate.
func TestRetainedCacheCertificationFailuresAndGaps(t *testing.T) {
	cached := rec{"type": "message", "id": "cached"}
	prime := func(source *roRetainedSource) *retainedCache {
		source.leaf = cached
		cache := newRetainedCache()
		if _, err := cache.count(source, nil); err != nil {
			t.Fatal(err)
		}
		if source.projections != 1 {
			t.Fatalf("priming reads the projection once, got %d", source.projections)
		}
		return cache
	}
	t.Run("a dangling parent is a miss", func(t *testing.T) {
		source := &roRetainedSource{entries: map[string]rec{}}
		cache := prime(source)
		source.leaf = rec{"type": "message", "id": "child", "parentId": "ghost"}
		if _, err := cache.count(source, nil); err != nil || source.projections != 2 {
			t.Errorf("a leaf whose ancestry is cut must be read again: %d projection reads (%v)", source.projections, err)
		}
	})
	t.Run("a failing entry read is propagated", func(t *testing.T) {
		source := &roRetainedSource{entries: map[string]rec{"mid": {"type": "message", "id": "mid", "parentId": "cached"}}}
		cache := prime(source)
		source.leaf = rec{"type": "message", "id": "top", "parentId": "mid"}
		source.failEntry = errors.New("entry read failed")
		if _, err := cache.count(source, nil); err == nil || !strings.Contains(err.Error(), "entry read failed") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("a failing leaf read is propagated", func(t *testing.T) {
		source := &roRetainedSource{entries: map[string]rec{}}
		cache := prime(source)
		source.failLeaf = errors.New("leaf read failed")
		if _, err := cache.count(source, nil); err == nil || !strings.Contains(err.Error(), "leaf read failed") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("a failing projection read is propagated", func(t *testing.T) {
		source := &roRetainedSource{leaf: cached}
		source.beforeProjection = func() {}
		cache := newRetainedCache()
		source.failLeaf = nil
		failing := &roFailingProjection{roRetainedSource: source}
		if _, err := cache.count(failing, nil); err == nil || !strings.Contains(err.Error(), "projection read failed") {
			t.Errorf("err %v", err)
		}
	})
}

// roFailingProjection fails the projection read.
type roFailingProjection struct{ *roRetainedSource }

func (roFailingProjection) BuildSessionProjection() (sdk.SessionProjection, error) {
	return sdk.SessionProjection{}, errors.New("projection read failed")
}
