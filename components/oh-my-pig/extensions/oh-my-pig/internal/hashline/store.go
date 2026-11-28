package hashline

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Session-scoped snapshots, clipboard registers, and no-op loop state.
// upstream: crates/edit/src/store.rs

const (
	// DefaultMaxPaths is the retained path count before LRU eviction.
	// upstream: crates/edit/src/store.rs:14
	DefaultMaxPaths = 256
	// DefaultMaxVersionsPerPath is the number of full-file versions retained per path.
	// upstream: crates/edit/src/store.rs:16
	DefaultMaxVersionsPerPath = 4
	// DefaultMaxTotalBytes is the global ceiling on retained snapshot allocations.
	// upstream: crates/edit/src/store.rs:20
	DefaultMaxTotalBytes = 64 * 1024 * 1024
	// MaxSnapshotFileBytes is the largest file size snapshotted from disk.
	// upstream: crates/edit/src/store.rs:23
	MaxSnapshotFileBytes = 4 * 1024 * 1024
	// MaxTaggedFileBytes is the largest file size given a tag.
	// upstream: crates/edit/src/store.rs:26
	MaxTaggedFileBytes = 64 * 1024 * 1024
	// NoopHardLimit is the consecutive identical no-op count before escalation.
	// upstream: crates/edit/src/store.rs:28
	NoopHardLimit uint32 = 3

	maxRetiredRanges = 64 * 1024
	carryMaxEdits    = 1024
)

// Snapshot is one full-file version observed at a point in time.
// SeenLines == nil means unrestricted (no seen-line provenance recorded yet or full-file).
// Non-nil empty map means authored (e.g. write): stays unrestricted even after partial display.
// Non-nil non-empty map means restricted to the displayed 1-indexed lines.
// upstream: crates/edit/src/store.rs:40
type Snapshot struct {
	Path      string
	Text      string
	Hash      string
	SeenLines map[int]bool
}

// Clipboard holds anonymous and named registers threaded through patch application.
// upstream: crates/edit/src/store.rs:55
type Clipboard struct {
	Lines           []string
	HasAnon         bool
	Named           map[string][]string
	PendingAnonCuts []string
}

// StartBatch creates a batch clipboard inheriting only named registers.
// upstream: crates/edit/src/store.rs:66
func (c *Clipboard) StartBatch() *Clipboard {
	if c == nil || len(c.Named) == 0 {
		return &Clipboard{}
	}
	named := make(map[string][]string, len(c.Named))
	for k, v := range c.Named {
		named[k] = slices.Clone(v)
	}
	return &Clipboard{Named: named}
}

// Fork returns a transactional deep copy of c.
// upstream: crates/edit/src/store.rs:71
func (c *Clipboard) Fork() *Clipboard {
	if c == nil {
		return &Clipboard{}
	}
	out := &Clipboard{
		Lines:           slices.Clone(c.Lines),
		HasAnon:         c.HasAnon,
		PendingAnonCuts: slices.Clone(c.PendingAnonCuts),
	}
	if c.Named != nil {
		out.Named = make(map[string][]string, len(c.Named))
		for k, v := range c.Named {
			out.Named[k] = slices.Clone(v)
		}
	}
	return out
}

// CommitFrom merges named registers from a completed transaction.
// upstream: crates/edit/src/store.rs:76
func (c *Clipboard) CommitFrom(fork *Clipboard) {
	if fork == nil || len(fork.Named) == 0 {
		return
	}
	if c.Named == nil {
		c.Named = make(map[string][]string, len(fork.Named))
	}
	for k, v := range fork.Named {
		c.Named[k] = slices.Clone(v)
	}
}

type storedSnapshot struct {
	snapshot Snapshot
	carried  []uint32
}

func (ss *storedSnapshot) cloneSnapshot() Snapshot {
	snap := ss.snapshot
	if snap.SeenLines != nil {
		snap.SeenLines = maps.Clone(snap.SeenLines)
	}
	return snap
}

func (ss *storedSnapshot) retainedBytes() int {
	textBytes := len(ss.snapshot.Text) + 16
	seenBytes := 0
	if ss.snapshot.SeenLines != nil {
		seenBytes = 64 + len(ss.snapshot.SeenLines)*20
	}
	return len(ss.snapshot.Path) + len(ss.snapshot.Hash) + textBytes + seenBytes + len(ss.carried)*4
}

type pathHistory struct {
	versions []*storedSnapshot // newest first (index 0 is head)
	touched  uint64
}

func (ph *pathHistory) retainedBytes(pathLen int) int {
	total := pathLen + len(ph.versions)*64
	for _, v := range ph.versions {
		total += v.retainedBytes()
	}
	return total
}

type lineRangePair struct {
	start uint32
	end   uint32
}

type retiredProvenance struct {
	path     string
	textHash uint64
	seen     []lineRangePair
}

type noopEntry struct {
	payload uint64
	count   uint32
}

// Store retains session-scoped snapshots, clipboard registers, and no-op counters.
// upstream: crates/edit/src/store.rs:229
type Store struct {
	mu              sync.Mutex
	histories       map[string]*pathHistory
	retired         []retiredProvenance
	retiredRanges   int
	clipboard       Clipboard
	noop            map[string]noopEntry
	clock           uint64
	maxPaths        int
	maxVersions     int
	maxTotalBytes   int
	retainedBytes   int
	privateFallback bool
}

// NewStore creates an empty session-scoped Store with default limits.
// upstream: crates/edit/src/store.rs:235
func NewStore() *Store {
	return NewStoreWithLimits(DefaultMaxPaths, DefaultMaxVersionsPerPath, DefaultMaxTotalBytes)
}

// NewStoreWithLimits constructs a Store with explicit retention limits.
// upstream: crates/edit/src/store.rs:240
func NewStoreWithLimits(maxPaths, maxVersions, maxTotalBytes int) *Store {
	return &Store{
		histories:     make(map[string]*pathHistory),
		noop:          make(map[string]noopEntry),
		maxPaths:      maxPaths,
		maxVersions:   maxVersions,
		maxTotalBytes: maxTotalBytes,
	}
}

var defaultStore = NewStore()

// Default returns a process-wide fallback store.
// Deprecated: prefer ForSession(ctx) so read, grep, ast, lsp, and edit share
// one store per session with clean lifecycle teardown. Retained for tests and
// callers not yet migrated to ForSession.
func Default() *Store { return defaultStore }

// IsPrivateFallback reports whether s was created as an ephemeral fallback store
// for a Context with no session ID. Reads may use it, but anchored edits refuse
// rather than silently bypassing the session seen-line guard.
func (s *Store) IsPrivateFallback() bool {
	if s == nil {
		return false
	}
	return s.privateFallback
}

// CanonicalPathKey computes a snapshot key that stays identical before and after creation.
// It resolves symlinks on the deepest existing ancestor and re-appends the missing tail.
// upstream: crates/edit/src/path_policy.rs:297
func CanonicalPathKey(p string) string {
	if p == "" {
		return ""
	}
	abs := p
	if a, err := filepath.Abs(p); err == nil {
		abs = a
	}
	if real, ok := physicalPath(abs); ok {
		return real
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	parent := filepath.Dir(abs)
	name := filepath.Base(abs)
	if realParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(realParent, name)
	}
	return filepath.Clean(abs)
}

func physicalPath(p string) (string, bool) {
	existing := filepath.Clean(p)
	var tail []string
	for {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real, true
		}
		if _, err := os.Lstat(existing); err == nil {
			// Dangling symlink or unresolvable existing entry.
			return "", false
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", false
		}
		tail = append(tail, filepath.Base(existing))
		existing = parent
	}
}

// Fork creates an independent copy of the current store state for dry-run staging.
// upstream: crates/edit/src/store.rs:250
func (s *Store) Fork() *Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &Store{
		histories:       make(map[string]*pathHistory, len(s.histories)),
		retired:         slices.Clone(s.retired),
		retiredRanges:   s.retiredRanges,
		clipboard:       *s.clipboard.Fork(),
		noop:            maps.Clone(s.noop),
		clock:           s.clock,
		maxPaths:        s.maxPaths,
		maxVersions:     s.maxVersions,
		maxTotalBytes:   s.maxTotalBytes,
		retainedBytes:   s.retainedBytes,
		privateFallback: s.privateFallback,
	}
	for k, h := range s.histories {
		nh := &pathHistory{
			versions: make([]*storedSnapshot, len(h.versions)),
			touched:  h.touched,
		}
		for i, v := range h.versions {
			nh.versions[i] = &storedSnapshot{
				snapshot: v.cloneSnapshot(),
				carried:  slices.Clone(v.carried),
			}
		}
		out.histories[k] = nh
	}
	return out
}

// RecordSnapshot records authored full text (from `write`) under absPath and returns its tag.
// It marks the version authored (unrestricted empty SeenLines map) so a later partial display
// does not narrow it. Reads MUST use RecordRead / RecordReadLines instead.
// Exported for internal/files, internal/ast, internal/search.
func (s *Store) RecordSnapshot(absPath, normalizedText string) string {
	return s.Record(absPath, normalizedText, []uint32{}, true)
}

// RecordRead records a read of absPath whose displayed (non-column-cut) lines are seenLines.
// Unlike RecordSnapshot (which marks authored content unrestricted), RecordRead ALWAYS records
// a restricted seen set:
//   - Passing a non-empty seenLines records those exact 1-based line numbers (e.g. 1..N for a
//     whole-file read, without the phantom trailing-LF row).
//   - Passing an empty/nil seenLines (e.g. every displayed row was column-cut, or an empty file)
//     records sentinel line 0 (`NO_FULL_LINE`) so the version remains restricted.
//
// upstream: packages/coding-agent/src/edit/store.ts:21-50
func (s *Store) RecordRead(absPath, normalizedText string, seenLines []uint32) string {
	if len(seenLines) == 0 {
		return s.Record(absPath, normalizedText, []uint32{0}, true)
	}
	return s.Record(absPath, normalizedText, seenLines, true)
}

// RecordReadLines is the []int convenience wrapper around RecordRead.
// Empty or all-negative slices record sentinel line 0 (`NO_FULL_LINE`) so the version stays restricted.
// Exported for internal/files, internal/search, internal/ast.
func (s *Store) RecordReadLines(absPath, normalizedText string, seenLines []int) string {
	u32 := make([]uint32, 0, len(seenLines))
	for _, ln := range seenLines {
		if ln >= 0 {
			u32 = append(u32, uint32(ln))
		}
	}
	return s.RecordRead(absPath, normalizedText, u32)
}

// RecordReadWholeFile records a whole-file read of absPath with all addressable lines (1..N,
// excluding the trailing-LF phantom row) marked as displayed. For an empty file, omp prints
// the `1:` row and accepts `PUT 1.=1:`, so line 1 is recorded as displayed.
// Exported for internal/files, internal/search, internal/ast.
func (s *Store) RecordReadWholeFile(absPath, normalizedText string) string {
	if normalizedText == "" {
		return s.RecordRead(absPath, normalizedText, []uint32{1})
	}
	n := len(SplitAddressableFileLines(normalizedText))
	lines := make([]uint32, n)
	for i := range n {
		lines[i] = uint32(i + 1)
	}
	return s.RecordRead(absPath, normalizedText, lines)
}

// RecordSnapshotWithSeenLines records a read snapshot whose display showed only seenLines.
// When seenLines is empty (e.g. every displayed row was column-cut), records sentinel line 0
// (`NO_FULL_LINE`) so the version stays restricted rather than becoming authored/unrestricted.
// Exported for internal/files, internal/search, internal/ast.
func (s *Store) RecordSnapshotWithSeenLines(absPath, normalizedText string, seenLines []int) string {
	return s.RecordReadLines(absPath, normalizedText, seenLines)
}

// Record records normalized text under absPath and returns its tag.
// When hasSeenLines is false, no seen-line provenance is attached yet (unrestricted,
// carried lines held back until restricted). When hasSeenLines is true and seenLines
// is empty, the version is authored (stays unrestricted even after partial display).
// upstream: crates/edit/src/store.rs:260
func (s *Store) Record(absPath, normalizedText string, seenLines []uint32, hasSeenLines bool) string {
	return s.recordVersion(CanonicalPathKey(absPath), normalizedText, seenLines, hasSeenLines, "", false)
}

// RecordEdit records after (which an edit wrote over before) and returns its tag.
// Lines displayed from before carry where they keep their number and content.
// upstream: crates/edit/src/store.rs:270
func (s *Store) RecordEdit(absPath, before, after string) string {
	return s.recordVersion(CanonicalPathKey(absPath), after, nil, false, before, true)
}

func (s *Store) recordVersion(key, text string, seenLines []uint32, hasSeenLines bool, parent string, hasParent bool) string {
	hash := FileHash(text)
	carried := s.carriedFromParent(key, text, parent, hasParent)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clock++
	touched := s.clock
	restored, hasRestored := takeRetired(s, key, text)

	hist, existed := s.histories[key]
	prevBytes := 0
	if existed {
		prevBytes = hist.retainedBytes(len(key))
	} else {
		hist = &pathHistory{touched: touched}
		s.histories[key] = hist
	}
	hist.touched = touched

	var stored *storedSnapshot
	foundIdx := -1
	for i, v := range hist.versions {
		if v.snapshot.Hash == hash && v.snapshot.Text == text {
			foundIdx = i
			break
		}
	}
	if foundIdx >= 0 {
		v := hist.versions[foundIdx]
		copy(hist.versions[1:foundIdx+1], hist.versions[0:foundIdx])
		hist.versions[0] = v
		stored = v
	} else if s.maxVersions > 0 {
		v := &storedSnapshot{
			snapshot: Snapshot{
				Path: key,
				Text: text,
				Hash: hash,
			},
		}
		hist.versions = append([]*storedSnapshot{v}, hist.versions...)
		if len(hist.versions) > s.maxVersions {
			hist.versions = hist.versions[:s.maxVersions]
		}
		stored = v
	}

	if stored != nil {
		if hasRestored {
			mergeSeen(stored, restored, true)
		}
		if hasSeenLines {
			mergeSeen(stored, seenLines, true)
		}
		if hasParent && len(carried) > 0 {
			mergeSeen(stored, carried, true)
		} else {
			carryInto(stored, carried)
		}
	}

	curBytes := hist.retainedBytes(len(key))
	s.retainedBytes = s.retainedBytes - prevBytes + curBytes
	evictStore(s)
	return hash
}

func (s *Store) carriedFromParent(key, text, parent string, hasParent bool) []uint32 {
	var headText string
	var hasHead bool
	var parentSeen map[int]bool
	var hasParentSeen bool

	s.mu.Lock()
	hist := s.histories[key]
	if hist != nil {
		if hasParent {
			for _, v := range hist.versions {
				if v.snapshot.Text == parent {
					if v.snapshot.SeenLines != nil {
						parentSeen = maps.Clone(v.snapshot.SeenLines)
						hasParentSeen = true
					}
					break
				}
			}
		} else if len(hist.versions) > 0 {
			headText = hist.versions[0].snapshot.Text
			hasHead = true
			if hist.versions[0].snapshot.SeenLines != nil {
				parentSeen = maps.Clone(hist.versions[0].snapshot.SeenLines)
				hasParentSeen = true
			}
		}
	}
	s.mu.Unlock()

	var parentText string
	if hasParent {
		parentText = parent
	} else if hasHead {
		parentText = headText
	} else {
		return nil
	}
	if parentText == text {
		return nil
	}
	if !hasParentSeen {
		parentSeen = nil
	}
	return carriedSeenLines(parentText, text, parentSeen)
}

// RecordSeenLines merges additional displayed 1-indexed lines into the retained snapshot.
// Exported for internal/search and internal/files.
// upstream: crates/edit/src/store.rs:393
func (s *Store) RecordSeenLines(absPath, hash string, lines []int) {
	if len(lines) == 0 {
		return
	}
	u32 := make([]uint32, 0, len(lines))
	for _, ln := range lines {
		if ln >= 0 {
			u32 = append(u32, uint32(ln))
		}
	}
	s.RecordSeenLinesU32(absPath, hash, u32)
}

// RecordSeenLinesU32 unions displayed uint32 lines into the most recent version matching hash.
// upstream: crates/edit/src/store.rs:393
func (s *Store) RecordSeenLinesU32(absPath, hash string, lines []uint32) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	touchStore(s, key)
	hist := s.histories[key]
	if hist == nil {
		return
	}
	for _, v := range hist.versions {
		if v.snapshot.Hash == hash {
			prevBytes := v.retainedBytes()
			mergeSeen(v, lines, true)
			curBytes := v.retainedBytes()
			s.retainedBytes = s.retainedBytes - prevBytes + curBytes
			evictStore(s)
			return
		}
	}
}

// RecordSeenLinesFromBody parses displayed line numbers from a formatted body and merges them.
// Exported for internal/ast and internal/search.
func (s *Store) RecordSeenLinesFromBody(absPath, hash, body string) {
	s.RecordSeenLinesU32(absPath, hash, SeenLinesFromBodyU32(body))
}

// Head returns the most recently recorded snapshot for absPath, refreshing recency.
// upstream: crates/edit/src/store.rs:410
func (s *Store) Head(absPath string) (Snapshot, bool) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	touchStore(s, key)
	hist := s.histories[key]
	if hist == nil || len(hist.versions) == 0 {
		return Snapshot{}, false
	}
	return hist.versions[0].cloneSnapshot(), true
}

// Versions returns every retained snapshot for absPath, newest first.
// upstream: crates/edit/src/store.rs:434
func (s *Store) Versions(absPath string) []Snapshot {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	touchStore(s, key)
	hist := s.histories[key]
	if hist == nil {
		return nil
	}
	out := make([]Snapshot, len(hist.versions))
	for i, v := range hist.versions {
		out[i] = v.cloneSnapshot()
	}
	return out
}

// ByHash returns the most recent retained snapshot matching absPath and hash.
// upstream: crates/edit/src/store.rs:447
func (s *Store) ByHash(absPath, hash string) (Snapshot, bool) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	touchStore(s, key)
	hist := s.histories[key]
	if hist == nil {
		return Snapshot{}, false
	}
	for _, v := range hist.versions {
		if v.snapshot.Hash == hash {
			return v.cloneSnapshot(), true
		}
	}
	return Snapshot{}, false
}

// HasHashForPath reports whether hash was issued for absPath in this session.
// upstream: crates/edit/src/store.rs:461
func (s *Store) HasHashForPath(absPath, hash string) bool {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	touchStore(s, key)
	hist := s.histories[key]
	if hist == nil {
		return false
	}
	for _, v := range hist.versions {
		if v.snapshot.Hash == hash {
			return true
		}
	}
	return false
}

// ByContent returns the version with exact text and refreshes path recency.
// upstream: crates/edit/src/store.rs:471
func (s *Store) ByContent(absPath, text string) (Snapshot, bool) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	touchStore(s, key)
	hist := s.histories[key]
	if hist == nil {
		return Snapshot{}, false
	}
	for _, v := range hist.versions {
		if v.snapshot.Text == text {
			return v.cloneSnapshot(), true
		}
	}
	return Snapshot{}, false
}

// TagOriginPaths returns sorted paths in this session that have been issued hash.
// upstream: crates/edit/src/store.rs:484
func (s *Store) TagOriginPaths(hash string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var origins []string
	for p, hist := range s.histories {
		for _, v := range hist.versions {
			if v.snapshot.Hash == hash {
				origins = append(origins, p)
				break
			}
		}
	}
	slices.Sort(origins)
	return origins
}

// Invalidate removes one path's history and retired provenance.
// upstream: crates/edit/src/store.rs:496
func (s *Store) Invalidate(absPath string) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	if hist, ok := s.histories[key]; ok {
		s.retainedBytes -= hist.retainedBytes(len(key))
		delete(s.histories, key)
	}
	kept := s.retired[:0]
	for _, r := range s.retired {
		if r.path == key {
			s.retiredRanges -= len(r.seen)
		} else {
			kept = append(kept, r)
		}
	}
	s.retired = kept
}

// Relocate moves source history and provenance to a destination path.
// upstream: crates/edit/src/store.rs:513
func (s *Store) Relocate(fromAbs, toAbs string) {
	fromKey := CanonicalPathKey(fromAbs)
	toKey := CanonicalPathKey(toAbs)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock++
	touched := s.clock
	source, ok := s.histories[fromKey]
	if !ok {
		return
	}
	delete(s.histories, fromKey)
	s.retainedBytes -= source.retainedBytes(len(fromKey))
	for _, v := range source.versions {
		v.snapshot.Path = toKey
	}
	merged := source.versions
	if dest, ok := s.histories[toKey]; ok {
		delete(s.histories, toKey)
		s.retainedBytes -= dest.retainedBytes(len(toKey))
		merged = append(merged, dest.versions...)
	}
	seenHashes := make(map[string]struct{}, len(merged))
	deduped := merged[:0]
	for _, v := range merged {
		if _, dup := seenHashes[v.snapshot.Hash]; !dup {
			seenHashes[v.snapshot.Hash] = struct{}{}
			deduped = append(deduped, v)
		}
	}
	if len(deduped) > s.maxVersions {
		deduped = deduped[:s.maxVersions]
	}
	hist := &pathHistory{versions: deduped, touched: touched}
	s.retainedBytes += hist.retainedBytes(len(toKey))
	s.histories[toKey] = hist
	evictStore(s)
}

// Clear removes all snapshots, clipboard state, and no-op counters, restoring default limits.
// upstream: crates/edit/src/store.rs:544
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.histories = make(map[string]*pathHistory)
	s.retired = nil
	s.retiredRanges = 0
	s.clipboard = Clipboard{}
	s.noop = make(map[string]noopEntry)
	s.clock = 0
	s.maxPaths = DefaultMaxPaths
	s.maxVersions = DefaultMaxVersionsPerPath
	s.maxTotalBytes = DefaultMaxTotalBytes
	s.retainedBytes = 0
}

// StartBatchClipboard returns a transactional clipboard copy for one edit call.
// upstream: crates/edit/src/store.rs:549
func (s *Store) StartBatchClipboard() *Clipboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clipboard.StartBatch()
}

// CommitClipboard merges named registers from a successful edit call.
// upstream: crates/edit/src/store.rs:554
func (s *Store) CommitClipboard(batch *Clipboard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clipboard.CommitFrom(batch)
}

// NamedRegisters returns a snapshot of currently stored named registers.
func (s *Store) NamedRegisters() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.clipboard.Named)
}

// RecordNoop records an identical no-op and returns its consecutive count and escalation state.
// upstream: crates/edit/src/store.rs:560
func (s *Store) RecordNoop(absPath string, payload uint64) (uint32, bool) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	var count uint32 = 1
	if entry, ok := s.noop[key]; ok && entry.payload == payload {
		count = entry.count + 1
	}
	s.noop[key] = noopEntry{payload: payload, count: count}
	return count, count >= NoopHardLimit
}

// ResetNoop clears one path's no-op counter.
// upstream: crates/edit/src/store.rs:572
func (s *Store) ResetNoop(absPath string) {
	key := CanonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.noop, key)
}

// mergeSeen unions displayed lines into a stored snapshot.
// An empty non-nil SeenLines map is authored content and stays unrestricted.
// upstream: crates/edit/src/store.rs:583
func mergeSeen(stored *storedSnapshot, lines []uint32, hasLines bool) {
	if !hasLines {
		return
	}
	if stored.snapshot.SeenLines != nil && len(stored.snapshot.SeenLines) == 0 {
		return
	}
	if stored.snapshot.SeenLines == nil {
		stored.snapshot.SeenLines = make(map[int]bool, len(lines))
	}
	for _, ln := range lines {
		stored.snapshot.SeenLines[int(ln)] = true
	}
	if len(stored.snapshot.SeenLines) > 0 && len(stored.carried) > 0 {
		for _, ln := range stored.carried {
			stored.snapshot.SeenLines[int(ln)] = true
		}
		stored.carried = nil
	}
}

// carryInto credits carried lines to a version.
// upstream: crates/edit/src/store.rs:604
func carryInto(stored *storedSnapshot, carried []uint32) {
	if len(carried) == 0 {
		return
	}
	if stored.snapshot.SeenLines != nil {
		if len(stored.snapshot.SeenLines) == 0 {
			// Authored content stays unrestricted.
			return
		}
		for _, ln := range carried {
			stored.snapshot.SeenLines[int(ln)] = true
		}
		return
	}
	stored.carried = append(stored.carried, carried...)
	slices.Sort(stored.carried)
	stored.carried = slices.Compact(stored.carried)
}

// carriedSeenLines computes parent lines keeping both number and content in child.
// upstream: crates/edit/src/store.rs:624
func carriedSeenLines(parent, child string, seen map[int]bool) []uint32 {
	restrict := len(seen) > 0
	oldLines := strings.Split(parent, "\n")
	newLines := strings.Split(child, "\n")
	oldIDs, newIDs := internStrings(oldLines, newLines)

	type span struct{ start, end uint32 } // half-open [start, end)
	var unshifted []span
	if runs, ok := myersDiffWithin(oldIDs, newIDs, carryMaxEdits); ok {
		var oldLine, newLine uint32 = 1, 1
		for _, run := range runs {
			if run.Added {
				newLine += run.Count
			} else if run.Removed {
				oldLine += run.Count
			} else {
				if oldLine == newLine {
					unshifted = append(unshifted, span{oldLine, oldLine + run.Count})
				}
				oldLine += run.Count
				newLine += run.Count
			}
		}
	} else {
		prefix := 0
		limit := min(len(oldIDs), len(newIDs))
		for prefix < limit && oldIDs[prefix] == newIDs[prefix] {
			prefix++
		}
		unshifted = append(unshifted, span{1, uint32(prefix) + 1})
		if len(oldIDs) == len(newIDs) {
			n := len(oldIDs)
			suffix := 0
			for suffix < n-prefix && oldIDs[n-1-suffix] == newIDs[n-1-suffix] {
				suffix++
			}
			unshifted = append(unshifted, span{uint32(n-suffix) + 1, uint32(n) + 1})
		}
	}

	var carried []uint32
	for _, sp := range unshifted {
		for ln := sp.start; ln < sp.end; ln++ {
			if !restrict || seen[int(ln)] {
				carried = append(carried, ln)
			}
		}
	}
	return carried
}

func touchStore(s *Store, key string) {
	if hist, ok := s.histories[key]; ok {
		s.clock++
		hist.touched = s.clock
	}
}

func evictStore(s *Store) {
	for len(s.histories) > s.maxPaths || s.retainedBytes > s.maxTotalBytes {
		var oldestKey string
		var oldestTouched uint64
		first := true
		for k, h := range s.histories {
			if first || h.touched < oldestTouched {
				oldestKey = k
				oldestTouched = h.touched
				first = false
			}
		}
		if first {
			break
		}
		hist := s.histories[oldestKey]
		delete(s.histories, oldestKey)
		s.retainedBytes -= hist.retainedBytes(len(oldestKey))
		retireHistory(s, oldestKey, hist)
	}
}

func retireHistory(s *Store, key string, hist *pathHistory) {
	for _, v := range hist.versions {
		if len(v.snapshot.SeenLines) == 0 {
			continue
		}
		sorted := make([]uint32, 0, len(v.snapshot.SeenLines))
		for ln := range v.snapshot.SeenLines {
			if ln >= 0 {
				sorted = append(sorted, uint32(ln))
			}
		}
		slices.Sort(sorted)
		var ranges []lineRangePair
		for _, ln := range sorted {
			if len(ranges) > 0 && ranges[len(ranges)-1].end+1 == ln {
				ranges[len(ranges)-1].end = ln
			} else {
				ranges = append(ranges, lineRangePair{start: ln, end: ln})
			}
		}
		if len(ranges) > maxRetiredRanges {
			continue
		}
		s.retiredRanges += len(ranges)
		s.retired = append(s.retired, retiredProvenance{
			path:     key,
			textHash: xxh64Sum([]byte(v.snapshot.Text), 0),
			seen:     ranges,
		})
	}
	for s.retiredRanges > maxRetiredRanges && len(s.retired) > 0 {
		oldest := s.retired[0]
		s.retired = s.retired[1:]
		s.retiredRanges -= len(oldest.seen)
	}
}

func takeRetired(s *Store, key, text string) ([]uint32, bool) {
	hasPath := false
	for _, r := range s.retired {
		if r.path == key {
			hasPath = true
			break
		}
	}
	if !hasPath {
		return nil, false
	}
	textHash := xxh64Sum([]byte(text), 0)
	for i, r := range s.retired {
		if r.path == key && r.textHash == textHash {
			s.retired = slices.Delete(s.retired, i, i+1)
			s.retiredRanges -= len(r.seen)
			var out []uint32
			for _, pair := range r.seen {
				for ln := pair.start; ; ln++ {
					out = append(out, ln)
					if ln == pair.end {
						break
					}
				}
			}
			return out, true
		}
	}
	return nil, false
}

var seenLinePrefixRe = regexp.MustCompile(`^[ *]?(\d+)(?:-(\d+))?:`)

// SeenLinesFromBodyU32 parses displayed boundary line numbers from a hashline-formatted body.
// upstream: crates/edit/src/store.rs:114
func SeenLinesFromBodyU32(body string) []uint32 {
	var seen []uint32
	for row := range strings.SplitSeq(body, "\n") {
		m := seenLinePrefixRe.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		if n, err := strconv.ParseUint(m[1], 10, 32); err == nil {
			seen = append(seen, uint32(n))
		}
		if m[2] != "" {
			if end, err := strconv.ParseUint(m[2], 10, 32); err == nil {
				seen = append(seen, uint32(end))
			}
		}
	}
	return seen
}

// SeenLinesFromBody parses 1-indexed boundary line numbers from a formatted body.
// Exported for internal/files, internal/ast, internal/search.
func SeenLinesFromBody(body string) []int {
	u32 := SeenLinesFromBodyU32(body)
	out := make([]int, len(u32))
	for i, v := range u32 {
		out[i] = int(v)
	}
	return out
}
