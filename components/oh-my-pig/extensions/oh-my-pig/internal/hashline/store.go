package hashline

import (
	"maps"
	"path/filepath"
	"slices"
	"sync"
)

const (
	// DefaultMaxPaths is the retained path count before LRU eviction.
	// upstream: crates/pi-edit/src/store.rs:15
	DefaultMaxPaths = 256
	// DefaultMaxVersionsPerPath is the number of full-file versions retained per path.
	// upstream: crates/pi-edit/src/store.rs:17
	DefaultMaxVersionsPerPath = 4
)

// Snapshot is one full-file version observed at a point in time.
type Snapshot struct {
	Path      string
	Text      string
	Hash      string
	SeenLines map[int]bool // nil means whole file was displayed
}

// Clipboard holds anonymous and named registers threaded through patch application.
// upstream: crates/pi-edit/src/store.rs:44
type Clipboard struct {
	Lines           []string
	HasAnon         bool
	Named           map[string][]string
	PendingAnonCuts []string
}

// StartBatch creates a batch clipboard inheriting only named registers.
// upstream: crates/pi-edit/src/store.rs:55
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

// CommitFrom merges named registers from a completed transaction.
// upstream: crates/pi-edit/src/store.rs:65
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

// Store retains session-scoped snapshots and clipboard registers.
// upstream: crates/pi-edit/src/store.rs
type Store struct {
	mu        sync.Mutex
	byPath    map[string][]Snapshot
	order     []string // oldest first
	clipboard Clipboard
}

// NewStore creates an empty session-scoped Store.
func NewStore() *Store {
	return &Store{
		byPath: make(map[string][]Snapshot),
	}
}

var defaultStore = NewStore()

// Default is the store the extension's tools share: read and edit record and check snapshots in it, and grep
// records the lines it displays, so a tag any of them printed is valid for edit. It lives as long as the
// process, where omp's lives as long as the session: a snapshot from before /new stays usable, which is harmless
// because an edit still requires the tag to match a retained snapshot of that file.
func Default() *Store { return defaultStore }

func canonicalPathKey(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// RecordSnapshot computes the hashline tag for normalizedText, stores it for absPath, and returns the tag.
func (s *Store) RecordSnapshot(absPath, normalizedText string) string {
	return s.recordSnapshotInternal(absPath, normalizedText, nil, true)
}

// RecordSnapshotWithSeenLines records a snapshot whose display only showed seenLines
// (for partial reads, comma lists, edit previews, or grep hits).
func (s *Store) RecordSnapshotWithSeenLines(absPath, normalizedText string, seenLines []int) string {
	return s.recordSnapshotInternal(absPath, normalizedText, seenLines, false)
}

// RecordSeenLines merges additional displayed 1-indexed lines into the retained snapshot
// for (absPath, hash). Exported so internal/search can record grep hit lines.
func (s *Store) RecordSeenLines(absPath, hash string, lines []int) {
	if len(lines) == 0 {
		return
	}
	key := canonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.byPath[key]
	for i := range versions {
		if versions[i].Hash == hash {
			if versions[i].SeenLines == nil {
				return // whole file already seen
			}
			for _, ln := range lines {
				if ln > 0 {
					versions[i].SeenLines[ln] = true
				}
			}
			return
		}
	}
}

// RecordSeenLinesFromBody parses displayed line numbers from a formatted body and merges
// them into the retained snapshot for (absPath, hash).
func (s *Store) RecordSeenLinesFromBody(absPath, hash, body string) {
	s.RecordSeenLines(absPath, hash, SeenLinesFromBody(body))
}

func (s *Store) recordSnapshotInternal(absPath, normalizedText string, seenLines []int, fullFile bool) string {
	tag := FileHash(normalizedText)
	key := canonicalPathKey(absPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	versions := s.byPath[key]
	var existingSeen map[int]bool
	hadExisting := false
	filtered := versions[:0]
	for _, v := range versions {
		if v.Hash == tag {
			hadExisting = true
			existingSeen = v.SeenLines
		} else {
			filtered = append(filtered, v)
		}
	}

	var mergedSeen map[int]bool
	if !fullFile {
		if hadExisting && existingSeen == nil {
			// Already seen in full; keep nil.
			mergedSeen = nil
		} else {
			mergedSeen = make(map[int]bool, len(existingSeen)+len(seenLines))
			for ln := range existingSeen {
				mergedSeen[ln] = true
			}
			for _, ln := range seenLines {
				if ln > 0 {
					mergedSeen[ln] = true
				}
			}
		}
	}

	filtered = append(filtered, Snapshot{Path: key, Text: normalizedText, Hash: tag, SeenLines: mergedSeen})
	if len(filtered) > DefaultMaxVersionsPerPath {
		filtered = filtered[len(filtered)-DefaultMaxVersionsPerPath:]
	}
	s.byPath[key] = filtered

	// Update LRU order.
	for i, p := range s.order {
		if p == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, key)
	if len(s.order) > DefaultMaxPaths {
		evict := s.order[0]
		s.order = s.order[1:]
		delete(s.byPath, evict)
	}
	return tag
}

// HasHashForPath reports whether hash was issued for absPath in this session.

// ByHash returns the retained snapshot matching absPath and hash, if any.
// upstream: crates/pi-edit/src/store.rs
func (s *Store) ByHash(absPath, hash string) (Snapshot, bool) {
	key := canonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.byPath[key] {
		if v.Hash == hash {
			return v, true
		}
	}
	return Snapshot{}, false
}

// Head returns the most recently recorded snapshot for absPath, if any.
func (s *Store) Head(absPath string) (Snapshot, bool) {
	key := canonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.byPath[key]
	if len(versions) == 0 {
		return Snapshot{}, false
	}
	return versions[len(versions)-1], true
}
func (s *Store) HasHashForPath(absPath, hash string) bool {
	key := canonicalPathKey(absPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.byPath[key] {
		if v.Hash == hash {
			return true
		}
	}
	return false
}

// TagOriginPaths returns sorted paths in this session that have been issued hash.
// upstream: crates/pi-edit/src/modes/hashline/mismatch.rs:24
func (s *Store) TagOriginPaths(hash string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var origins []string
	for p, versions := range s.byPath {
		for _, v := range versions {
			if v.Hash == hash {
				origins = append(origins, p)
				break
			}
		}
	}
	slices.Sort(origins)
	return origins
}

// StartBatchClipboard returns a transactional clipboard copy for one edit call.
func (s *Store) StartBatchClipboard() *Clipboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clipboard.StartBatch()
}

// CommitClipboard merges named registers from a successful edit call.
func (s *Store) CommitClipboard(batch *Clipboard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clipboard.CommitFrom(batch)
}

// NamedRegisters returns a snapshot of currently stored named registers (for diagnostics/tests).
func (s *Store) NamedRegisters() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.clipboard.Named)
}
