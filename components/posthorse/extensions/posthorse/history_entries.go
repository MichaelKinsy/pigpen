package posthorse

// Port of src/history-entries.ts: entry flattening, window ancestry and history hits.
// Entries are rec values: either the host's own session entries or the normalized records
// parseSessionLine builds (session_files.go); both are read through the dyn.go helpers.

import "strings"

// isWindow is `isWindow` (history-entries.ts:23-28).
func isWindow(entry entryLike) bool {
	switch typ, _ := str(entry, "type"); typ {
	case "context_window":
		return true
	case "compaction":
		details, ok := recOf(entry["details"])
		if !ok {
			return false
		}
		posthorse, ok := num(details, "posthorse")
		return ok && posthorse == 1
	}
	return false
}

// windowHandoff is `windowHandoff` (history-entries.ts:29-31): a nil entry is undefined.
func windowHandoff(entry entryLike) (string, bool) {
	if entry == nil {
		return "", false
	}
	if typ, _ := str(entry, "type"); typ == "compaction" {
		return str(entry, "summary")
	}
	return str(entry, "handoff")
}

// contextStart is `contextStart` (history-entries.ts:33-48): the first active branch entry,
// including tails retained by Pi's own /compact. A nil boundary is undefined.
func contextStart(entries []entryLike) (start int, boundary entryLike) {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		typ, _ := str(entry, "type")
		if typ == "context_window" {
			return i + 1, entry
		}
		if typ == "compaction" {
			kept := -1
			// entries.findIndex(candidate => candidate.id === entry.firstKeptEntryId): strict
			// equality of the raw members, so an absent id (undefined) equals only an absent
			// firstKeptEntryId, and a null one equals only a null id.
			want, wantHas := entry["firstKeptEntryId"]
			for j, candidate := range entries {
				id, has := candidate["id"]
				if has == wantHas && jsStrictEqualScalar(id, want) {
					kept = j
					break
				}
			}
			if kept >= 0 {
				return kept, entry
			}
			return i + 1, entry
		}
	}
	return 0, nil
}

// jsStrictEqualScalar is `a === b` for decoded JSON scalars (absent members compare as nil, which
// the callers settle by comparing presence first); objects and arrays are never equal here.
func jsStrictEqualScalar(a, b any) bool {
	switch a := a.(type) {
	case nil:
		return b == nil
	case string:
		bs, ok := b.(string)
		return ok && a == bs
	case float64:
		bf, ok := b.(float64)
		return ok && a == bf
	case bool:
		bb, ok := b.(bool)
		return ok && a == bb
	}
	return false
}

// contextEdits is `contextEdits` (history-entries.ts:49-57): context_edit entries by targetId,
// the last of equal targets winning as in the Map constructor.
func contextEdits(entries []entryLike) map[string]entryLike {
	edits := make(map[string]entryLike)
	for _, entry := range entries {
		if typ, _ := str(entry, "type"); typ != "context_edit" {
			continue
		}
		if target, ok := str(entry, "targetId"); ok && target != "" {
			edits[target] = entry
		}
	}
	return edits
}

// flattenMessage is `flattenMessage` (history-entries.ts:58-76).
func flattenMessage(message messageLike) string {
	role, hasRole := str(message, "role")
	if role == "bashExecution" {
		if excluded, _ := boolean(message, "excludeFromContext"); excluded {
			return "[bashExecution] (excluded from model context by Pi)"
		}
		command, _ := str(message, "command")
		output, _ := str(message, "output")
		return "[bashExecution] $ " + command + "\n" + output
	}
	identity := ""
	if role == "toolResult" {
		identity = "Tool: " + toolIdentity(optStr(message, "toolName"), optStr(message, "namespace"))
		if id, ok := str(message, "toolCallId"); ok {
			identity += "\nCall ID: " + id
		}
	}
	if !hasRole {
		role = "message"
	}
	parts := [4]string{
		textOf(message),
		assistantFailure(message),
		imageSummary(imagesOf(message["content"])),
		identity,
	}
	// "[role] " and the non-empty parts joined by a line break, built in one allocation.
	size, count := len(role)+3, 0
	for _, part := range parts {
		if part != "" {
			size += len(part)
			count++
		}
	}
	if count > 1 {
		size += count - 1
	}
	var b strings.Builder
	b.Grow(size)
	b.WriteByte('[')
	b.WriteString(role)
	b.WriteString("] ")
	first := true
	for _, part := range parts {
		if part == "" {
			continue
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(part)
	}
	return b.String()
}

// flattenEntry is `flattenEntry` (history-entries.ts:77-97); false is undefined.
func flattenEntry(entry entryLike) (string, bool) {
	typ, _ := str(entry, "type")
	switch typ {
	case "message":
		message, _ := recOf(entry["message"])
		return flattenMessage(message), true
	case "compaction", "branch_summary":
		summary, _ := str(entry, "summary")
		return "[" + typ + "] " + summary, true
	case "context_window":
		handoff, ok := str(entry, "handoff")
		if !ok || handoff == "" {
			return "[context_window] No handoff", true
		}
		return "[context_window] Handoff: " + handoff, true
	case "custom_message":
		customType, ok := str(entry, "customType")
		if !ok {
			customType = "unknown"
		}
		return "[custom:" + customType + "] " + contentText(entry["content"]), true
	case "context_edit":
		replacement, ok := recOf(entry["replacement"])
		if !ok {
			return "", false
		}
		return "[context_edit] " + contentText(replacement["content"]), true
	}
	return "", false
}

// contentText is `contentText` (history-entries.ts:98-102).
func contentText(content any) string {
	text := textOf(rec{"content": content})
	summary := imageSummary(imagesOf(content))
	switch {
	case text == "":
		return summary
	case summary == "":
		return text
	}
	return text + "\n" + summary
}

// ownAssistantText is `ownAssistantText` (history-entries.ts:103-126): the assistant's own text
// before each recovery call, then after the last one.
func ownAssistantText(message messageLike, images []imageLike) []string {
	var originals []string
	current := ""
	parts, _ := arrOf(message["content"])
	for _, part := range parts {
		if isRecoveryCall(part) {
			originals = append(originals, current)
			current = ""
			continue
		}
		partText := textOf(rec{"content": []any{part}})
		if partText != "" {
			if current == "" {
				current = partText
			} else {
				current = current + "\n" + partText
			}
		}
	}
	last := make([]string, 0, 3)
	for _, part := range [3]string{current, assistantFailure(message), imageSummary(images)} {
		if part != "" {
			last = append(last, part)
		}
	}
	originals = append(originals, strings.Join(last, "\n"))
	if originals[0] != "" {
		originals[0] = "[assistant] " + originals[0]
	}
	return originals
}

// echoPriority is `echoPriority` (history-entries.ts:127-143): 1 ranks recovery echoes after
// original content.
func echoPriority(entry entryLike) int {
	typ, _ := str(entry, "type")
	switch typ {
	case "context_window", "compaction", "branch_summary":
		return 1
	case "custom_message":
		if isReminderType(entry["customType"]) {
			return 1
		}
	case "message":
		message, _ := recOf(entry["message"])
		if role, _ := str(message, "role"); role == "toolResult" && isRecoveryTool(message["toolName"]) {
			return 1
		}
	}
	return 0
}

// originalMatch is `originalMatch` (history-entries.ts:144-161). hasText is false when text is
// undefined.
func originalMatch(item windowedEntry, query string) (text string, hasText, mixed bool) {
	typ, _ := str(item.entry, "type")
	message, _ := recOf(item.entry["message"])
	if role, _ := str(message, "role"); typ != "message" || role != "assistant" {
		return "", false, false
	}
	content, _ := arrOf(message["content"])
	recovery := false
	for _, part := range content {
		if isRecoveryCall(part) {
			recovery = true
			break
		}
	}
	if !recovery {
		return "", false, false
	}
	images := item.images
	if images == nil {
		images = imagesOf(message["content"])
	}
	originals := ownAssistantText(message, images)
	for _, part := range originals {
		if historyIncludes(part, query) {
			if part == "" {
				return "", false, true
			}
			if part == originals[0] {
				return part, true, true
			}
			return "[assistant] " + part, true, true
		}
	}
	return "", false, true
}

// hitQuery is the lowercase query of one search with what is decided once for it: ASCII queries
// are searched with a prebuilt folding pattern.
type hitQuery struct {
	text    string
	pattern *foldPattern // nil unless the query is ASCII
}

func newHitQuery(query string) *hitQuery {
	q := &hitQuery{text: query}
	if query != "" && isASCII(query) {
		q.pattern = newFoldPattern(query)
	}
	return q
}

// index is `text.toLowerCase().indexOf(query)` (see historyIndex).
func (q *hitQuery) index(text string) int {
	if q.pattern != nil && isASCII(text) {
		return q.pattern.index(text)
	}
	return historyIndex(text, q.text)
}

// hitCandidate is a match of historyHit whose text is not built yet: archive scans keep only the
// newest few hits per file, so the header and excerpt are made for those alone. It owns its
// strings (the entry it came from may alias a read buffer).
type hitCandidate struct {
	id        string
	timestamp string
	windowID  string
	text      string // the text the excerpt is cut from: the entry's text, or the matching original part
	priority  int
}

// matchHit is the decision half of `historyHit` (history-entries.ts:162-186): whether the entry is a
// hit, which text it matches and its rank. false is undefined.
func matchHit(item windowedEntry, q *hitQuery) (hitCandidate, bool) {
	entry := item.entry
	id, _ := str(entry, "id")
	if id == "" || q.index(item.text) < 0 {
		return hitCandidate{}, false
	}
	text, hasText, mixed := originalMatch(item, q.text)
	if !hasText {
		text = item.text
	}
	priority := echoPriority(entry)
	if mixed {
		if hasText {
			priority = 0
		} else {
			priority = 1
		}
	}
	timestamp, _ := str(entry, "timestamp")
	windowID := item.windowId
	if windowID != "initial" {
		windowID = strings.Clone(windowID)
	}
	return hitCandidate{id: strings.Clone(id), timestamp: strings.Clone(timestamp), windowID: windowID, text: text, priority: priority}, true
}

// qualifiedID is the id search prints: `id@shortKey(source)`; key is shortKey(source) ("" for the
// active branch, which prints the bare id).
func (c hitCandidate) qualifiedID(key string) string {
	if key == "" {
		return c.id
	}
	return c.id + "@" + key
}

// hit is the construction half of `historyHit`: header, excerpt and header length. key is
// shortKey(source), computed once per file by the caller.
func (c hitCandidate) hit(query *hitQuery, source, key string) historyHit {
	qualified := c.qualifiedID(key)
	header := c.timestamp + sourceTag(source) + " [window " + c.windowID + "] [" + qualified + "] "
	return historyHit{
		id:           qualified,
		priority:     c.priority,
		text:         header + excerptAround(c.text, query.index(c.text), 100, 400),
		headerLength: u16len(header),
	}
}

// newHistoryHit is the function `historyHit` of history-entries.ts:162-186 (renamed: the Go type
// historyHit is the TS HistoryHit); false is undefined.
func newHistoryHit(item windowedEntry, query, source string) (historyHit, bool) {
	q := newHitQuery(query)
	candidate, ok := matchHit(item, q)
	if !ok {
		return historyHit{}, false
	}
	key := ""
	if source != "" {
		key = shortKey(source)
	}
	return candidate.hit(q, source, key), true
}

// entryContent is `entryContent` (history-entries.ts:187-198).
func entryContent(entry entryLike) any {
	typ, _ := str(entry, "type")
	switch typ {
	case "context_edit":
		replacement, _ := recOf(entry["replacement"])
		return replacement["content"]
	case "message":
		message, _ := recOf(entry["message"])
		return message["content"]
	}
	return entry["content"]
}

// toWindowedEntry is `toWindowedEntry` (history-entries.ts:199-214); false is undefined.
func toWindowedEntry(entry entryLike, inheritedWindow string) (windowedEntry, bool) {
	item, ok := windowedEntryOf(entry, inheritedWindow)
	if ok {
		item.images = imagesOf(entryContent(entry))
	}
	return item, ok
}

// windowedEntryOf is toWindowedEntry without the images, which only a hit on an assistant
// message with recovery calls (originalMatch) and a read need: those read them from the entry.
func windowedEntryOf(entry entryLike, inheritedWindow string) (windowedEntry, bool) {
	id, _ := str(entry, "id")
	if id == "" {
		return windowedEntry{}, false
	}
	text, ok := flattenEntry(entry)
	if !ok || text == "" {
		return windowedEntry{}, false
	}
	windowID := inheritedWindow
	if isWindow(entry) {
		windowID = id
	}
	return windowedEntry{entry: entry, windowId: windowID, text: text}, true
}

// windowTracker is the state of `windowProjection()` (history-entries.ts:215-226): window
// ancestry is metadata-only; extracting text and images is a separate operation.
type windowTracker struct {
	windows map[string]string
}

// windowProjection is `windowProjection` (history-entries.ts:216).
func windowProjection() *windowTracker {
	return &windowTracker{windows: make(map[string]string)}
}

// project is the returned `(entry) => windowId` of windowProjection for a rec entry.
func (w *windowTracker) project(entry entryLike) string {
	id, hasID := str(entry, "id")
	parent, _ := str(entry, "parentId")
	// `isWindow(entry) ? (entry.id ?? inherited) : inherited`: a window without an id inherits.
	return w.track(id, parent, hasID && isWindow(entry))
}

// track is project over the three facts it reads; id and parent are "" when undefined (an
// undefined parentId reads `?? ""`, and ids are only stored when non-empty). The id is cloned
// when stored, so a short id never retains the line it was sliced from.
func (w *windowTracker) track(id, parentID string, isWin bool) string {
	inherited, ok := w.windows[parentID]
	if !ok {
		inherited = "initial"
	}
	windowID := inherited
	if isWin {
		// `entry.id ?? inherited`: an empty (but present) id is kept; a missing one inherits.
		windowID = id
		if windowID == "initial" {
			windowID = "initial" // the constant, not a string that may alias a read buffer
		}
	}
	// An entry in the initial window is the lookup default (`?? "initial"`), so it needs no map
	// entry: archives without windows keep the map empty however many entries they hold.
	if id != "" {
		if windowID != "initial" {
			key := strings.Clone(id)
			if isWin {
				windowID = key // the window's id is the window name: one owned copy for both
			}
			w.windows[key] = windowID
		} else if len(w.windows) > 0 {
			delete(w.windows, id) // a repeated id replaces its earlier window
		}
	}
	return windowID
}

// reverseWindowEntries is `reverseWindowEntries` (history-entries.ts:227-238): newest first,
// yielding until yield returns false.
func reverseWindowEntries(entries []entryLike, yield func(windowedEntry) bool) {
	// Preserve parent-relative window labels without normalizing the whole branch.
	project := windowProjection()
	windowIDs := make([]string, len(entries))
	for i, entry := range entries {
		windowIDs[i] = project.project(entry)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if item, ok := toWindowedEntry(entries[i], windowIDs[i]); ok && !yield(item) {
			return
		}
	}
}

// seenText is `seenText` (history-entries.ts:239-251): what the model saw of a message,
// lowercased.
func seenText(message messageLike) string {
	parts := make([]string, 0, 6)
	for _, part := range [3]string{
		textOf(message),
		assistantFailure(message),
		imageSummary(imagesOf(message["content"])),
	} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	for _, key := range [3]string{"summary", "command", "output"} {
		if value, ok := str(message, key); ok && value != "" {
			parts = append(parts, value)
		}
	}
	return jsToLowerCase(strings.Join(parts, "\n"))
}

// historyIncludes is `text.toLowerCase().includes(query)` for an already lowercase query.
func historyIncludes(text, query string) bool {
	return historyIndex(text, query) >= 0
}

// historyIndex is `text.toLowerCase().indexOf(query)` for an already lowercase query, in UTF-16
// units of the lowercased text. ASCII text and queries search without lowering a copy: ASCII
// case folding only maps ASCII letters, so the index is the byte offset.
func historyIndex(text, query string) int {
	if query == "" {
		return 0
	}
	if isASCII(query) && isASCII(text) {
		return foldIndexString(text, query)
	}
	return u16index(jsToLowerCase(text), query, 0)
}
