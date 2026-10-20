package warden

import (
	"fmt"
	"strings"
	"sync"
)

// Session accounting for the context saver (saver.ts): how often large outputs appear, how many were compressed,
// dropped as duplicates or cut as repeated runs, how much was removed, how many turns that removal was spared from
// (token-turns), and how often the agent went back for the full text. Deterministic; no requests.

// CompressionCounts is a kind of compression's count and recalls.
type CompressionCounts struct {
	Count       int `json:"count"`
	Recalls     int `json:"recalls"`
	RecallsFull int `json:"recallsFull"`
}

// FilterCounts is what the context filter spent, on top of its CompressionCounts.
type FilterCounts struct {
	CompressionCounts
	// KeptChars is the characters of the original output the filtered outputs kept.
	KeptChars int `json:"keptChars"`
	// Requests and Ms are the requests and wall-clock time the filter spent, fallbacks included.
	Requests int `json:"requests"`
	Ms       int `json:"ms"`
	// Fallbacks counts outputs that got the excerpt instead, by reason.
	Fallbacks map[string]int `json:"fallbacks"`
	// Order lists the fallback reasons in the order each first occurred (the status line prints them so).
	Order []string `json:"-"`
}

// ContextLedgerSnapshot is the ledger's counters.
type ContextLedgerSnapshot struct {
	// Large: single-text-block outputs at or above tailMinChars, the candidates for compression.
	Large      int `json:"large"`
	Compressed int `json:"compressed"`
	// Duplicates: results replaced by a duplicate note.
	Duplicates int `json:"duplicates"`
	// Repeats: results and messages whose repeated runs were replaced by pointers.
	Repeats    int `json:"repeats"`
	BytesSaved int `json:"bytesSaved"`
	// Turns completed since the session started.
	Turns int `json:"turns"`
	// TokenTurnsSaved is the sum over turns of the bytes no longer in context on that turn, in rough tokens (bytes / 4).
	TokenTurnsSaved int               `json:"tokenTurnsSaved"`
	Recalls         int               `json:"recalls"`
	RecallsFull     int               `json:"recallsFull"`
	Excerpt         CompressionCounts `json:"excerpt"`
	Filter          FilterCounts      `json:"filter"`
}

// RecallKind is the kind of access to a stored output.
type RecallKind string

const (
	RecallFull   RecallKind = "full"
	RecallScoped RecallKind = "scoped"
)

// CompressionKind says which excerpt replaced the output; a parser excerpt and a duplicate or repeat are neither
// "excerpt" nor "filtered".
type CompressionKind string

const (
	KindNone     CompressionKind = ""
	KindExcerpt  CompressionKind = "excerpt"
	KindFiltered CompressionKind = "filtered"
	KindParser   CompressionKind = "parser"
)

// mentions: serialized tool input escapes backslashes, so a Windows path must also match in its JSON form.
func mentions(text, path string) bool {
	if strings.Contains(text, path) {
		return true
	}
	quoted := jsStringify(path)
	return strings.Contains(text, quoted[1:len(quoted)-1])
}

type storedState struct {
	recalled bool
	restored bool
	saved    int
	tool     string
	bytes    int
	hasBytes bool
	kind     CompressionKind
}

// StoredSource is what Record and Repeat know about the output they stored.
type StoredSource struct {
	Tool      string
	Bytes     int
	Kind      CompressionKind
	KeptChars int
}

type seenEntry struct {
	tool string
	path string
}

// ContextLedger is the saver's session accounting.
type ContextLedger struct {
	// mu guards every field: the tool_result, tool_call and message handlers and the recall tool read and write one ledger.
	mu                                                 sync.Mutex
	large, compressed, duplicates, repeats, bytesSaved int
	// bytesAbsent is bytesSaved minus the savings of outputs a whole-file recall put back into context.
	bytesAbsent            int
	turns, tokenTurnsSaved int
	recalls, recallsFull   int
	excerpt                CompressionCounts
	filter                 FilterCounts
	stored                 map[string]*storedState
	storedOrder            []string
	// seen: every sizeable text result seen this session, by content key, with the tool that produced it and its stored copy if any.
	seen      map[string]seenEntry
	seenOrder []string
}

// NewContextLedger returns an empty ledger.
func NewContextLedger() *ContextLedger {
	l := &ContextLedger{}
	l.Reset()
	return l
}

// Candidate counts a large output.
func (l *ContextLedger) Candidate() {
	l.mu.Lock()
	l.large++
	l.mu.Unlock()
}

func (l *ContextLedger) store(path string, state storedState) {
	if _, ok := l.stored[path]; !ok {
		l.storedOrder = append(l.storedOrder, path)
	}
	l.stored[path] = &state
}

// Record counts a compression. `source` is optional (nil for callers that do not know the tool and size).
func (l *ContextLedger) Record(path string, bytesSaved int, source *StoredSource) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.compressed++
	l.bytesSaved += bytesSaved
	l.bytesAbsent += bytesSaved
	state := storedState{saved: bytesSaved}
	if source != nil {
		switch source.Kind {
		case KindExcerpt:
			l.excerpt.Count++
		case KindFiltered:
			l.filter.Count++
			l.filter.KeptChars += source.KeptChars
		}
		state.tool, state.bytes, state.hasBytes, state.kind = source.Tool, source.Bytes, true, source.Kind
	}
	l.store(path, state)
}

// FilterSpent is what one filter attempt cost, and why it fell back to the excerpt when it did.
func (l *ContextLedger) FilterSpent(requests, ms int, fallback string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filter.Requests += requests
	l.filter.Ms += ms
	if fallback != "" {
		if l.filter.Fallbacks[fallback] == 0 {
			l.filter.Order = append(l.filter.Order, fallback)
		}
		l.filter.Fallbacks[fallback]++
	}
}

// Remember keeps a result's identity so a later identical result can be dropped. `path` is set when a full copy exists.
func (l *ContextLedger) Remember(key, tool, path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.seen[key]; ok && existing.path != "" && path == "" {
		return
	}
	if _, ok := l.seen[key]; !ok {
		l.seenOrder = append(l.seenOrder, key)
	}
	l.seen[key] = seenEntry{tool: tool, path: path}
}

// DuplicateOf is the earlier result with this content key.
func (l *ContextLedger) DuplicateOf(key string) (tool, path string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.seen[key]
	return e.tool, e.path, ok
}

// Duplicate counts a result replaced by a duplicate note.
func (l *ContextLedger) Duplicate(bytesSaved int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.duplicates++
	l.bytesSaved += bytesSaved
	l.bytesAbsent += bytesSaved
}

// Repeat counts repeated runs of one text replaced by pointer lines; `path` holds the full text, so reading it back is a recall.
func (l *ContextLedger) Repeat(path string, bytesSaved int, tool string, bytes int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.repeats++
	l.bytesSaved += bytesSaved
	l.bytesAbsent += bytesSaved
	l.store(path, storedState{saved: bytesSaved, tool: tool, bytes: bytes, hasBytes: true})
}

// TurnEnd: one LLM turn finished; everything removed so far was absent from this turn's prompt.
func (l *ContextLedger) TurnEnd() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.turns++
	l.tokenTurnsSaved += jsRound(float64(l.bytesAbsent) / 4)
}

// StoredPathIn is the stored path that `text` (a path, command, or serialized input) mentions, if any.
func (l *ContextLedger) StoredPathIn(text string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, path := range l.storedOrder {
		if mentions(text, path) {
			return path, true
		}
	}
	for _, key := range l.seenOrder {
		if e := l.seen[key]; e.path != "" && mentions(text, e.path) {
			return e.path, true
		}
	}
	return "", false
}

// NoteAccess counts the first access to a compressed output; later accesses and duplicate-only copies are not new
// recalls. A whole-file access, first or not, puts the output back into context, so its savings stop counting
// toward token-turns.
func (l *ContextLedger) NoteAccess(text string, kind RecallKind) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, path := range l.storedOrder {
		if !mentions(text, path) {
			continue
		}
		state := l.stored[path]
		if !state.recalled {
			state.recalled = true
			l.recalls++
			if kind == RecallFull {
				l.recallsFull++
			}
			var counts *CompressionCounts
			switch state.kind {
			case KindExcerpt:
				counts = &l.excerpt
			case KindFiltered:
				counts = &l.filter.CompressionCounts
			}
			if counts != nil {
				counts.Recalls++
				if kind == RecallFull {
					counts.RecallsFull++
				}
			}
		}
		if kind == RecallFull && !state.restored {
			state.restored = true
			l.bytesAbsent -= state.saved
		}
		return path, true
	}
	return "", false
}

// Snapshot is a copy of the counters.
func (l *ContextLedger) Snapshot() ContextLedgerSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := ContextLedgerSnapshot{Large: l.large, Compressed: l.compressed, Duplicates: l.duplicates, Repeats: l.repeats, BytesSaved: l.bytesSaved,
		Turns: l.turns, TokenTurnsSaved: l.tokenTurnsSaved, Recalls: l.recalls, RecallsFull: l.recallsFull, Excerpt: l.excerpt, Filter: l.filter}
	s.Filter.Fallbacks = make(map[string]int, len(l.filter.Fallbacks))
	for k, v := range l.filter.Fallbacks {
		s.Filter.Fallbacks[k] = v
	}
	s.Filter.Order = append([]string(nil), l.filter.Order...)
	return s
}

// StoredPaths are the paths of the stored copies, for cleanup at session start. Local only: paths never leave the machine.
func (l *ContextLedger) StoredPaths() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.storedOrder...)
}

// storedOutputs are the stored full outputs, oldest first, with the tool that produced each and its size when
// recorded (the outputLedger the compaction snapshot and warden_recall read). Local only, like StoredPaths.
func (l *ContextLedger) storedOutputs() []SavedOutput {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SavedOutput, 0, len(l.storedOrder))
	for _, path := range l.storedOrder {
		s := l.stored[path]
		out = append(out, SavedOutput{Tool: s.tool, Path: path, Bytes: s.bytes, HasBytes: s.hasBytes})
	}
	return out
}

// Reset clears every counter and memory.
func (l *ContextLedger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.large, l.compressed, l.duplicates, l.repeats, l.bytesSaved, l.bytesAbsent = 0, 0, 0, 0, 0, 0
	l.turns, l.tokenTurnsSaved, l.recalls, l.recallsFull = 0, 0, 0, 0
	l.excerpt = CompressionCounts{}
	l.filter = FilterCounts{Fallbacks: map[string]int{}}
	l.stored = map[string]*storedState{}
	l.storedOrder = nil
	l.seen = map[string]seenEntry{}
	l.seenOrder = nil
}

func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// FormatLedger is the line for /warden status. It says when there is nothing to report instead of printing zeros.
func FormatLedger(s ContextLedgerSnapshot) string {
	if s.Large == 0 && s.Duplicates == 0 && s.Repeats == 0 {
		return "Context saver: no tool output large enough to consider this session."
	}
	kb := toFixed(float64(s.BytesSaved)/1024, 1)
	stored := s.Compressed + s.Repeats
	recallRate := 0
	if stored > 0 {
		recallRate = jsRound(float64(s.Recalls) / float64(stored) * 100)
	}
	scoped := s.Recalls - s.RecallsFull
	repeats := ""
	if s.Repeats > 0 {
		repeats = fmt.Sprintf("%d %s cut, ", s.Repeats, pluralWord(s.Repeats, "repeat"))
	}
	return fmt.Sprintf("Context saver: %d large outputs, %d compressed, %d %s dropped, %s%s KB removed (~%d tokens), ~%d token-turns spared over %d turns, %d %s of the full output (%d%%; %d whole-file, %d scoped).",
		s.Large, s.Compressed, s.Duplicates, pluralWord(s.Duplicates, "duplicate"), repeats, kb, jsRound(float64(s.BytesSaved)/4), s.TokenTurnsSaved, s.Turns,
		s.Recalls, pluralWord(s.Recalls, "recall"), recallRate, s.RecallsFull, scoped)
}

// FormatFilterLedger is the line for /warden status while the context filter is on or has run: filtered outputs
// beside excerpt outputs, for the trial.
func FormatFilterLedger(s ContextLedgerSnapshot) string {
	rate := func(c CompressionCounts) string {
		if c.Count == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%d%%", jsRound(float64(c.Recalls)/float64(c.Count)*100))
	}
	parts := make([]string, 0, len(s.Filter.Order))
	for _, r := range s.Filter.Order {
		parts = append(parts, fmt.Sprintf("%s %d", r, s.Filter.Fallbacks[r]))
	}
	fallbacks := strings.Join(parts, ", ")
	if fallbacks == "" {
		fallbacks = "none"
	}
	f, e := s.Filter, s.Excerpt
	return fmt.Sprintf("Context filter (beta): %d filtered (%d characters kept), %d recalled (%s; %d whole-file, %d scoped); %d excerpts, %d recalled (%s; %d whole-file, %d scoped); %d %s, %d ms; fallbacks: %s.",
		f.Count, f.KeptChars, f.Recalls, rate(f.CompressionCounts), f.RecallsFull, f.Recalls-f.RecallsFull,
		e.Count, e.Recalls, rate(e), e.RecallsFull, e.Recalls-e.RecallsFull, f.Requests, pluralWord(f.Requests, "request"), f.Ms, fallbacks)
}
