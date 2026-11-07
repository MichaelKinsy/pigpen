package posthorse

// Port of src/history-search.ts: history search over the active branch or every session of the
// project, with ranking, lookahead and cursors. Nothing here holds a whole archive: each archive
// keeps only its newest limit+2 hits per priority.

import (
	"context"
	"encoding/base64"
	"math"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// historyParams is the TS HistoryParams (history-search.ts:20-29). Strings carry their presence
// where undefined differs from "" (query, id); limit, offset and imageOffset nil are undefined;
// an absent cursor is "".
type historyParams struct {
	op          string // "search" or "read"
	query       string
	hasQuery    bool
	id          string
	hasID       bool
	all         bool // `all === true`
	limit       *int
	cursor      string
	offset      *float64 // a JS number: TypeBox Integer accepts values past int64
	imageOffset *float64
}

// historySource is the TS HistorySource (history-search.ts:30-36); currentFile "" is undefined.
type historySource struct {
	dir         string
	cwd         string
	currentFile string
	branch      func() ([]entryLike, error)
	projection  func() ([]projectedEntry, error)
}

// historyCursor is the TS HistoryCursor tuple [id, priority, offset, key] (history-search.ts:37).
type historyCursor struct {
	id       string
	priority int
	offset   int
	key      string
}

// historyMatchSet is the TS HistoryMatches (history-search.ts:38-44); cursor nil is undefined.
type historyMatchSet struct {
	hits    []historyHit
	skipped int
	cursor  *historyCursor
	key     string
	limit   int
}

// decodeBase64URL is `Buffer.from(encoded, "base64url")`: both alphabets, characters outside
// them skipped, decoding ends at padding, and a lone trailing character is dropped.
func decodeBase64URL(encoded string) []byte {
	out := make([]byte, 0, len(encoded)*3/4+3)
	var acc uint32
	bits := 0
	for i := 0; i < len(encoded); i++ {
		c := encoded[i]
		var v uint32
		switch {
		case c >= 'A' && c <= 'Z':
			v = uint32(c - 'A')
		case c >= 'a' && c <= 'z':
			v = uint32(c-'a') + 26
		case c >= '0' && c <= '9':
			v = uint32(c-'0') + 52
		case c == '-' || c == '+':
			v = 62
		case c == '_' || c == '/':
			v = 63
		case c == '=':
			i = len(encoded)
			continue
		default:
			continue
		}
		acc = acc<<6 | v
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	return out
}

// cursorOffset is `cursorOffset` (history-search.ts:45-55); ok is false for the thrown error.
func cursorOffset(value any, storedKey any, key string) (int, bool) {
	offset, isNumber := value.(float64)
	stored, isString := storedKey.(string)
	if !isNumber || offset != math.Trunc(offset) || math.Abs(offset) > maxSafeInteger || offset < 0 || !isString || stored != key {
		return 0, false
	}
	return int(offset), true
}

// decodeCursor is `decodeCursor` (history-search.ts:56-70); ok is false for any thrown error.
// An absent or empty cursor is no cursor.
func decodeCursor(encoded, key string) (cursor *historyCursor, ok bool) {
	if encoded == "" {
		return nil, true
	}
	var value any
	if json.Unmarshal([]byte(jsFromBytes(decodeBase64URL(encoded))), &value) != nil {
		return nil, false
	}
	parts, isArray := arrOf(value)
	if !isArray || len(parts) != 4 {
		return nil, false
	}
	id, isString := parts[0].(string)
	priority, isNumber := parts[1].(float64)
	if !isString || id == "" || !isNumber || (priority != 0 && priority != 1) {
		return nil, false
	}
	offset, ok := cursorOffset(parts[2], parts[3], key)
	if !ok {
		return nil, false
	}
	return &historyCursor{id: id, priority: int(priority), offset: offset, key: key}, true
}

// entryMessage is `entryMessage` (history-search.ts:71-84).
func entryMessage(entry entryLike) messageLike {
	typ, _ := str(entry, "type")
	switch typ {
	case "message":
		message, _ := recOf(entry["message"])
		if message == nil {
			message = rec{}
		}
		return message
	case "context_edit":
		replacement, _ := recOf(entry["replacement"])
		return rec{"content": replacement["content"]}
	case "context_window":
		return rec{"content": entry["handoff"]}
	}
	return rec{"content": entry["content"], "summary": entry["summary"]}
}

// visibleEntry is one projected entry of contextVisibility: its messages, and their seen text
// once computed.
type visibleEntry struct {
	messages []messageLike
	text     string
	computed bool
}

// contextVisibility is `contextVisibility` (history-search.ts:85-110): whether an entry is still
// in the model's active context. Pi owns edits and compaction; only matching candidates need
// their projected text.
func contextVisibility(project func() ([]projectedEntry, error)) func(entry entryLike) (bool, error) {
	var visible map[string]*visibleEntry
	return func(entry entryLike) (bool, error) {
		if typ, _ := str(entry, "type"); typ == "custom_message" && isReminderType(entry["customType"]) {
			return false, nil
		}
		if visible == nil {
			projected, err := project()
			if err != nil {
				return false, err
			}
			visible = make(map[string]*visibleEntry, len(projected))
			for _, item := range projected {
				if id, _ := str(item.sourceEntry, "id"); id != "" && len(item.messages) > 0 {
					visible[id] = &visibleEntry{messages: item.messages}
				}
			}
		}
		var id string
		if typ, _ := str(entry, "type"); typ == "context_edit" {
			id, _ = str(entry, "targetId")
		} else {
			id, _ = str(entry, "id")
		}
		seen, ok := visible[id]
		if !ok {
			return false, nil
		}
		if !seen.computed {
			texts := make([]string, len(seen.messages))
			for i, message := range seen.messages {
				texts[i] = seenText(message)
			}
			seen.text, seen.computed = strings.Join(texts, "\n"), true
		}
		return strings.Contains(seen.text, seenText(entryMessage(entry))), nil
	}
}

// searchAccumulator is `SearchAccumulator` (history-search.ts:121-173): it owns ranking, lookahead
// and one cursor's progress; it never holds the whole archive.
type searchAccumulator struct {
	groups [2][]historyHit
	limit  int
	// seekingAnchor is progress.kind === "seeking"; cursor is progress.cursor.
	seekingAnchor bool
	cursor        *historyCursor
	fail          func(message string) error
}

func newSearchAccumulator(limit int, cursor *historyCursor, fail func(string) error) *searchAccumulator {
	return &searchAccumulator{limit: limit, seekingAnchor: cursor != nil, cursor: cursor, fail: fail}
}

func (a *searchAccumulator) add(hit historyHit) error {
	cursor := a.cursor
	if cursor != nil && hit.priority < cursor.priority {
		return nil
	}
	if a.seekingAnchor && hit.priority == cursor.priority {
		if hit.id != cursor.id {
			return nil
		}
		total := u16len(hit.text)
		if cursor.offset > total {
			return a.fail("History cursor is past the entry; restart the search without it.")
		}
		a.seekingAnchor = false
		if cursor.offset == total {
			return nil
		}
	}
	if len(a.groups[hit.priority]) <= a.limit {
		a.groups[hit.priority] = append(a.groups[hit.priority], hit)
	}
	return nil
}

func (a *searchAccumulator) seeking(priority int) bool {
	return a.seekingAnchor && priority == a.cursor.priority
}

func (a *searchAccumulator) enough() bool { return len(a.groups[0]) > a.limit }

func (a *searchAccumulator) results() ([]historyHit, error) {
	if a.seekingAnchor {
		return nil, a.fail("History cursor entry no longer matches; restart the search without it.")
	}
	return append(append(make([]historyHit, 0, len(a.groups[0])+len(a.groups[1])), a.groups[0]...), a.groups[1]...), nil
}

// archiveCollector is `archiveCollector` (history-search.ts:174-203): the newest limit+2 hits of
// one archive per priority, and whether the cursor's anchor entry is among them. The hits are
// candidates: their text is built when the archive is finished, for the kept ones only.
type archiveCollector struct {
	acc        *searchAccumulator
	query      *hitQuery
	source     string // the file relative to the session dir
	key        string // shortKey(source)
	recent     [2][]hitCandidate
	anchorHere bool
}

// isAnchor reports whether the candidate is the entry the cursor names (`hit.id === cursor.id`
// without building the qualified id).
func (c *archiveCollector) isAnchor(hit hitCandidate) bool {
	rest, ok := strings.CutPrefix(c.acc.cursor.id, hit.id)
	return ok && len(rest) == 1+len(c.key) && rest[0] == '@' && rest[1:] == c.key
}

func (c *archiveCollector) add(hit hitCandidate) {
	seeking := c.acc.seeking(hit.priority)
	if seeking && c.anchorHere {
		return
	}
	if seeking && c.isAnchor(hit) {
		c.anchorHere = true
	}
	group := append(c.recent[hit.priority], hit)
	if len(group) > c.acc.limit+2 {
		copy(group, group[1:])
		group[len(group)-1] = hitCandidate{}
		group = group[:len(group)-1]
	}
	c.recent[hit.priority] = group
}

func (c *archiveCollector) finish() [2][]historyHit {
	cursor := c.acc.cursor
	if cursor != nil && c.acc.seeking(cursor.priority) && !c.anchorHere {
		c.recent[cursor.priority] = nil
	}
	var kept [2][]historyHit
	for priority, group := range c.recent {
		kept[priority] = make([]historyHit, len(group))
		for i, candidate := range group {
			kept[priority][i] = candidate.hit(c.query, c.source, c.key)
		}
	}
	return kept
}

// scanArchive is `scanArchive` (history-search.ts:204-234).
func scanArchive(ctx context.Context, file string, source string, query *hitQuery, wanted *lineMatcher, inContext func(entryLike) (bool, error), acc *searchAccumulator) ([2][]historyHit, int, error) {
	collector := &archiveCollector{acc: acc, query: query, source: source, key: shortKey(source)}
	skipped := 0
	for item, err := range sessionWindowEntries(ctx, file, nil, wanted, false) {
		if err != nil {
			return [2][]historyHit{}, 0, err
		}
		hit, ok := matchHit(item, query)
		if !ok {
			continue
		}
		visible, err := inContext(item.entry)
		if err != nil {
			return [2][]historyHit{}, 0, err
		}
		if visible {
			skipped++
		} else {
			collector.add(hit)
		}
	}
	return collector.finish(), skipped, nil
}

// archivedMatches is `archivedMatches` (history-search.ts:235-281): every session file of the
// project, newest modified first, stopping once the first page's originals are found.
func archivedMatches(ctx context.Context, source historySource, query string, inContext func(entryLike) (bool, error), acc *searchAccumulator) (int, error) {
	needles, filtered := prefilterNeedles(query)
	var wanted *lineMatcher
	if filtered {
		wanted = needleLines(needles)
	} else {
		needles = nil
	}
	current := ""
	if source.currentFile != "" {
		current = absPath(source.currentFile)
	}
	skipped := 0
	hq := newHitQuery(query)
	scope := sessionScope{dir: source.dir, cwd: source.cwd, currentFile: source.currentFile, needles: needles}
	for file, err := range scopedSessionFiles(ctx, scope) {
		if err != nil {
			return 0, err
		}
		inCurrent := current != "" && absPath(file) == current
		rel, _ := filepath.Rel(source.dir, file)
		recent, fileSkipped, err := scanArchive(ctx, file, rel, hq, wanted, func(entry entryLike) (bool, error) {
			if !inCurrent {
				return false, nil
			}
			return inContext(entry)
		}, acc)
		if err != nil {
			return 0, err
		}
		skipped += fileSkipped
		for _, group := range recent {
			for i := len(group) - 1; i >= 0; i-- {
				if err := acc.add(group[i]); err != nil {
					return 0, err
				}
			}
		}
		if acc.enough() {
			break
		}
	}
	return skipped, nil
}

// absPath is node:path `resolve` for one argument.
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// branchMatches is `branchMatches` (history-search.ts:282-304).
func branchMatches(source historySource, query string, inContext func(entryLike) (bool, error), acc *searchAccumulator) (int, error) {
	branch, err := source.branch()
	if err != nil {
		return 0, err
	}
	skipped := 0
	var failure error
	hq := newHitQuery(query)
	reverseWindowEntries(branch, func(item windowedEntry) bool {
		candidate, ok := matchHit(item, hq)
		if !ok {
			return true
		}
		hit := candidate.hit(hq, "", "")
		visible, err := inContext(item.entry)
		if err != nil {
			failure = err
			return false
		}
		if visible {
			skipped++
			return true
		}
		if failure = acc.add(hit); failure != nil {
			return false
		}
		return !acc.enough()
	})
	return skipped, failure
}

// historyMatches is `historyMatches` (history-search.ts:305-329). fail builds the thrown error
// (pages.error): it is called where the original calls `fail(message)`.
func historyMatches(ctx context.Context, source historySource, params historyParams, fail func(message string) error) (historyMatchSet, error) {
	queryText, err := requireValue(params.query, params.hasQuery, "query", params.op)
	if err != nil {
		return historyMatchSet{}, err
	}
	query := jsToLowerCase(queryText)
	limit := 10
	if params.limit != nil {
		limit = *params.limit
	}
	encodedKey, err := jsStringify([]any{query, params.all})
	if err != nil {
		return historyMatchSet{}, err
	}
	key := shortKey(encodedKey)
	cursor, ok := decodeCursor(params.cursor, key)
	if !ok {
		return historyMatchSet{}, fail("Invalid history cursor or changed query/scope; restart the search without it.")
	}
	acc := newSearchAccumulator(limit, cursor, fail)
	// Archive scans await I/O; keep their visibility snapshot at invocation time.
	var projected []projectedEntry
	if params.all {
		if projected, err = source.projection(); err != nil {
			return historyMatchSet{}, err
		}
	}
	inContext := contextVisibility(func() ([]projectedEntry, error) {
		if params.all {
			return projected, nil
		}
		return source.projection()
	})
	var skipped int
	if params.all {
		skipped, err = archivedMatches(ctx, source, query, inContext, acc)
	} else {
		skipped, err = branchMatches(source, query, inContext, acc)
	}
	if err != nil {
		return historyMatchSet{}, err
	}
	hits, err := acc.results()
	if err != nil {
		return historyMatchSet{}, err
	}
	return historyMatchSet{hits: hits, skipped: skipped, cursor: cursor, key: key, limit: limit}, nil
}

// encodeCursor is `Buffer.from(...).toString("base64url")`.
func encodeCursor(text string) string {
	return base64.RawURLEncoding.EncodeToString(jsUTF8Bytes(text))
}
