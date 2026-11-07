package posthorse

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Port of src/recovery-note-touches.ts.

// scannedNoteEntry is ScannedNoteEntry (recovery-note-touches.ts:12).
type scannedNoteEntry struct {
	entry      rec
	entryId    string
	inRetained bool
}

// noteTarget is NoteTarget (recovery-note-touches.ts:18); "" = undefined.
type noteTarget struct {
	notePath       string
	absPath        string
	referenceScope string
}

// noteTouch is NoteTouch (recovery-note-touches.ts:23); "" = undefined for
// absPath, referenceScope and receiptId. kind is one of "full-write",
// "partial-write", "read", "clear", "observed-write".
type noteTouch struct {
	notePath       string
	absPath        string
	referenceScope string
	entryId        string
	receiptId      string
	owner          *noteOwner
	kind           string
	inRetained     bool
}

// toolResultInfo is ToolResultInfo (recovery-note-touches.ts:31). The text is
// derived lazily: it is only needed for notes results and path inference.
type toolResultInfo struct {
	entryID    string
	sourceID   string
	inRetained bool
	isError    bool
	toolName   string
	message    rec
	details    any
	text       string
	hasText    bool
}

func (r *toolResultInfo) resultText() string {
	if !r.hasText {
		r.text = textOf(r.message)
		r.hasText = true
	}
	return r.text
}

type touchContext struct {
	entryID    string
	sourceID   string
	inRetained bool
	knownDirs  []string // sorted once by length, longest first (note-scopes.ts:257)
}

var (
	leadingDotSlashes = regexp.MustCompile(`^(?:\./)+`)
	leadingSlashes    = regexp.MustCompile(`^/+`)
	trailingSlashes   = regexp.MustCompile(`/+$`)
	windowsDrivePath  = regexp.MustCompile(`^[A-Za-z]:/`)
	piNotesPrefix     = regexp.MustCompile(`^(?:\./)?\.pi/notes/`)
	// JS `.` excludes \n, \r, \u2028 and \u2029 and `$` without the m flag is end of input.
	packageNotesPath = regexp.MustCompile(`/\.pi/package-notes/[0-9a-f]{64}/notes/([^\n\r\x{2028}\x{2029}]+)$`)
	legacyNotesPath  = regexp.MustCompile(`/\.pi/(?:agent/)?notes/[^/]+/[^/]+/[^/]+/([^\n\r\x{2028}\x{2029}]+)$`)
)

// normalizeRelPath: recovery-note-touches.ts:47.
func normalizeRelPath(raw string) string {
	cleaned := strings.ReplaceAll(jsTrim(raw), "\\", "/")
	cleaned = leadingDotSlashes.ReplaceAllString(cleaned, "")
	cleaned = leadingSlashes.ReplaceAllString(cleaned, "")
	invalid := cleaned == "" || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") ||
		strings.HasSuffix(cleaned, "/")
	if invalid {
		return ""
	}
	return cleaned
}

// matchMarkerPath: recovery-note-touches.ts:63.
func matchMarkerPath(slash string) (string, bool) {
	for _, marker := range [...]string{"/.pi/notes/", "/posthorse-notes/"} {
		if idx := strings.LastIndex(slash, marker); idx >= 0 {
			return slash[idx+len(marker):], true
		}
	}
	if strings.HasPrefix(slash, ".pi/notes/") || strings.HasPrefix(slash, "./.pi/notes/") {
		return piNotesPrefix.ReplaceAllString(slash, ""), true
	}
	if m := packageNotesPath.FindStringSubmatch(slash); m != nil {
		return m[1], true
	}
	if m := legacyNotesPath.FindStringSubmatch(slash); m != nil {
		return m[1], true
	}
	return "", false
}

// unscopedNoteTarget: recovery-note-touches.ts:80.
func unscopedNoteTarget(trimmed, slash string) (noteTarget, bool) {
	if strings.HasPrefix(slash, "/") || windowsDrivePath.MatchString(slash) {
		base := normalizeRelPath(slash[strings.LastIndex(slash, "/")+1:])
		if base == "" {
			return noteTarget{}, false
		}
		return noteTarget{notePath: base, absPath: trimmed}, true
	}
	rel := normalizeRelPath(slash)
	if rel == "" {
		return noteTarget{}, false
	}
	return noteTarget{notePath: rel}, true
}

// resolveNoteTarget: recovery-note-touches.ts:88.
func resolveNoteTarget(rawPath string, hasPath bool, knownDirs []string /* sorted */, fromNotesTool bool) (noteTarget, bool) {
	if !hasPath {
		return noteTarget{}, false
	}
	trimmed := jsTrim(rawPath)
	slash := strings.ReplaceAll(trimmed, "\\", "/")
	matched, found := "", false
	if _, rel, ok := matchSortedNoteDirectory(slash, knownDirs); ok {
		matched, found = rel, true
	} else {
		matched, found = matchMarkerPath(slash)
	}
	if found {
		rel := ""
		if !strings.HasPrefix(matched, ".forks/") {
			rel = normalizeRelPath(matched)
		}
		if rel == "" {
			return noteTarget{}, false
		}
		return noteTarget{notePath: rel, absPath: trimmed}, true
	}
	if fromNotesTool {
		return unscopedNoteTarget(trimmed, slash)
	}
	return noteTarget{}, false
}

// isJSLineBreak: JS line terminators, which `^`/`$` honour under the m flag.
func isJSLineBreak(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// lineAfterPrefix is /^PREFIX([^\r\n]+)$/mu.exec(text)?.[1]: the first line
// start (after \n, \r, \u2028 or \u2029) carrying the prefix and a non-empty rest.
func lineAfterPrefix(text, prefix string) (string, bool) {
	for pos := 0; pos <= len(text); {
		if strings.HasPrefix(text[pos:], prefix) {
			rest := text[pos+len(prefix):]
			end := strings.IndexAny(rest, "\r\n")
			if end < 0 {
				end = len(rest)
			}
			if end > 0 {
				return rest[:end], true
			}
		}
		// advance to the next line start
		next := strings.IndexFunc(text[pos:], isJSLineBreak)
		if next < 0 {
			return "", false
		}
		_, size := utf8.DecodeRuneInString(text[pos+next:])
		pos += next + size
	}
	return "", false
}

type notesResultTarget struct {
	kind    string
	rawPath string
}

// extractNotesResultTarget: recovery-note-touches.ts:106.
func extractNotesResultTarget(text string) (notesResultTarget, bool) {
	if raw, ok := lineAfterPrefix(text, "Wrote "); ok {
		return notesResultTarget{"full-write", raw}, true
	}
	if raw, ok := lineAfterPrefix(text, "Appended to "); ok {
		return notesResultTarget{"partial-write", raw}, true
	}
	if raw, ok := lineAfterPrefix(text, "File: "); ok {
		return notesResultTarget{"read", raw}, true
	}
	return notesResultTarget{}, false
}

// resultMap is the insertion-ordered Map of recovery-note-touches.ts:123: a
// re-set key keeps its first position and takes the new value.
type resultMap struct {
	order []string
	byID  map[string]*toolResultInfo
}

// collectResultMap: recovery-note-touches.ts:123.
func collectResultMap(entries []scannedNoteEntry) *resultMap {
	results := &resultMap{byID: make(map[string]*toolResultInfo)}
	for i := range entries {
		item := &entries[i]
		msg, hasMsg := recOf(item.entry["message"])
		callID, _ := optionalString(msg["toolCallId"])
		if t, _ := str(item.entry, "type"); t == "message" && hasMsg && messageRole(msg) == "toolResult" && callID != "" {
			sourceID := item.entryId
			if v, ok := str(item.entry, "id"); ok {
				sourceID = v
			}
			toolName, _ := str(msg, "toolName")
			if _, seen := results.byID[callID]; !seen {
				results.order = append(results.order, callID)
			}
			results.byID[callID] = &toolResultInfo{
				entryID:    item.entryId,
				sourceID:   sourceID,
				inRetained: item.inRetained,
				isError:    msg["isError"] == true,
				toolName:   bareToolName(toolName),
				message:    msg,
				details:    msg["details"],
			}
		}
	}
	return results
}

// inferDirFromCall: recovery-note-touches.ts:150.
func inferDirFromCall(call toolCallLike, results *resultMap) (string, bool) {
	if call.id == nil {
		return "", false
	}
	res := results.byID[*call.id]
	if res == nil || res.isError {
		return "", false
	}
	callPath := ""
	if args, ok := recOf(call.arguments); ok {
		if raw, ok := optionalString(args["path"]); ok {
			callPath = normalizeRelPath(raw)
		}
	}
	if callPath == "" {
		return "", false
	}
	parsed, ok := extractNotesResultTarget(res.resultText())
	if !ok {
		return "", false
	}
	slash := strings.ReplaceAll(jsTrim(parsed.rawPath), "\\", "/")
	suffix := "/" + callPath
	if strings.HasSuffix(slash, suffix) && len(slash) > len(suffix) {
		return slash[:len(slash)-len(suffix)], true
	}
	return "", false
}

// assistantCallsOf: recovery-note-touches.ts:172.
func assistantCallsOf(entry rec) []toolCallLike {
	if t, _ := str(entry, "type"); t != "message" {
		return nil
	}
	message, ok := recOf(entry["message"])
	if !ok || messageRole(message) != "assistant" {
		return nil
	}
	return toolCalls(message["content"])
}

// orderedSet is a JS Set<string> that remembers insertion order.
type orderedSet struct {
	items []string
	seen  map[string]struct{}
}

func (s *orderedSet) add(v string) {
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	if _, ok := s.seen[v]; ok {
		return
	}
	s.seen[v] = struct{}{}
	s.items = append(s.items, v)
}

// learnNotesDirs: recovery-note-touches.ts:178.
func learnNotesDirs(entries []scannedNoteEntry, results *resultMap, dirs *orderedSet) {
	for _, id := range results.order {
		info := results.byID[id]
		if info.isError || info.toolName != "notes" {
			continue
		}
		if match, ok := lineAfterPrefix(info.resultText(), "Notes directory: "); ok {
			dirs.add(trailingSlashes.ReplaceAllString(strings.ReplaceAll(jsTrim(match), "\\", "/"), ""))
		}
	}
	for i := range entries {
		for _, call := range assistantCallsOf(entries[i].entry) {
			if inferred, ok := inferDirFromCall(call, results); ok {
				dirs.add(inferred)
			}
		}
	}
}

// fileToolTouches: recovery-note-touches.ts:203.
func fileToolTouches(tool string, args rec, result noteResult, context *touchContext) []noteTouch {
	var out []noteTouch
	for _, operation := range verifiedNoteOperations(tool, args, result) {
		target, ok := resolveNoteTarget(operation.path, true, context.knownDirs, false)
		if !ok {
			continue
		}
		kind := noteOperationKind(operation.action)
		if tool == "apply_patch" && operation.action != "delete" {
			kind = "partial-write"
		}
		out = append(out, noteTouch{
			notePath: target.notePath, absPath: target.absPath, referenceScope: target.referenceScope,
			kind: kind, entryId: context.entryID, inRetained: context.inRetained,
		})
	}
	return out
}

// notesCallKind: recovery-note-touches.ts:227; "" = undefined.
func notesCallKind(args rec) string {
	switch args["op"] {
	case "write":
		if args["content"] == "" {
			return "clear"
		}
		return "full-write"
	case "append":
		return "partial-write"
	case "read":
		return "read"
	}
	return ""
}

// notesToolTouches: recovery-note-touches.ts:247.
func notesToolTouches(args rec, resultText string, hasResultText bool, context *touchContext) []noteTouch {
	var parsed notesResultTarget
	hasParsed := false
	if hasResultText {
		parsed, hasParsed = extractNotesResultTarget(resultText)
	}
	kind := notesCallKind(args)
	if kind == "" && hasParsed {
		kind = parsed.kind
	}
	path, hasPath := optionalString(args["path"])
	scopedPath := path
	if scope, ok := args["scope"].(string); ok && hasPath {
		scopedPath = scope + "/" + path
	}
	callTarget, hasCall := resolveNoteTarget(scopedPath, hasPath, context.knownDirs, true)
	resTarget, hasRes := resolveNoteTarget(parsed.rawPath, hasParsed, context.knownDirs, true)
	var merged noteTarget
	switch {
	case hasCall:
		merged.notePath = callTarget.notePath
	case hasRes:
		merged.notePath = resTarget.notePath
	default:
		return nil
	}
	switch {
	case hasRes && resTarget.absPath != "":
		merged.absPath = resTarget.absPath
	case hasCall:
		merged.absPath = callTarget.absPath
	}
	if kind == "" {
		return nil
	}
	return []noteTouch{{
		notePath: merged.notePath, absPath: merged.absPath,
		kind: kind, entryId: context.entryID, inRetained: context.inRetained,
	}}
}

// extractNestedCalls: recovery-note-touches.ts:265.
func extractNestedCalls(message rec) []any {
	if nested, ok := recOf(message["nestedCalls"]); ok {
		if calls, ok := arrOf(nested["calls"]); ok {
			return calls
		}
	}
	details, ok := recOf(message["details"])
	if !ok {
		return nil
	}
	if calls, ok := arrOf(details["calls"]); ok {
		return calls
	}
	if nested, ok := recOf(details["nestedCalls"]); ok {
		if calls, ok := arrOf(nested["calls"]); ok {
			return calls
		}
	}
	return nil
}

// nestedArgs: recovery-note-touches.ts:281.
func nestedArgs(raw rec) rec {
	for _, key := range [...]string{"arguments", "args", "input"} {
		if args, ok := recOf(raw[key]); ok {
			return args
		}
	}
	return rec{}
}

// touchesFromNested: recovery-note-touches.ts:291.
func touchesFromNested(message rec, context *touchContext) []noteTouch {
	var out []noteTouch
	for _, item := range extractNestedCalls(message) {
		raw, ok := recOf(item)
		if !ok || raw["isError"] == true || raw["status"] == "error" || raw["status"] == "unfinished" {
			continue
		}
		if e, ok := raw["error"].(string); ok && e != "" {
			continue
		}
		nameValue, ok := optionalString(raw["name"])
		if !ok {
			nameValue, _ = optionalString(raw["toolName"])
		}
		name := bareToolName(nameValue)
		args := nestedArgs(raw)
		var touches []noteTouch
		if name == "notes" {
			text, hasText := optionalString(raw["result"])
			touches = notesToolTouches(args, text, hasText, context)
		} else {
			touches = fileToolTouches(name, args, noteResult{details: raw["result"], isError: false}, context)
		}
		for _, touch := range touches {
			if touch.kind != "read" && touch.kind != "clear" {
				touch.kind = "observed-write"
			}
			touch.inRetained = false
			touch.receiptId = context.sourceID
			out = append(out, touch)
		}
	}
	return out
}

// touchesFromAssistantCall: recovery-note-touches.ts:319.
func touchesFromAssistantCall(call toolCallLike, results *resultMap, context *touchContext) (consumed string, hasConsumed bool, touches []noteTouch) {
	var res *toolResultInfo
	if call.id != nil {
		res = results.byID[*call.id]
	}
	if call.id != nil && res != nil {
		consumed, hasConsumed = *call.id, true
	}
	callName := ""
	if call.name != nil {
		callName = *call.name
	}
	if res == nil || (res.isError && bareToolName(callName) == "notes") {
		return consumed, hasConsumed, nil
	}
	tool := bareToolName(callName)
	args, ok := recOf(call.arguments)
	if !ok {
		args = rec{}
	}
	if tool == "notes" {
		touches = notesToolTouches(args, res.resultText(), true, context)
	} else {
		touches = fileToolTouches(tool, args, noteResult{details: res.details, isError: res.isError}, context)
	}
	for i := range touches {
		touches[i].receiptId = res.sourceID
	}
	return consumed, hasConsumed, touches
}

// touchesFromToolResult: recovery-note-touches.ts:342.
func touchesFromToolResult(msg rec, handled map[string]struct{}, context *touchContext) []noteTouch {
	if messageRole(msg) != "toolResult" || msg["isError"] == true {
		return nil
	}
	callID, _ := optionalString(msg["toolCallId"])
	var direct []noteTouch
	if _, done := handled[callID]; !done {
		if toolName, _ := str(msg, "toolName"); bareToolName(toolName) == "notes" {
			direct = notesToolTouches(rec{}, textOf(msg), true, context)
		}
	}
	return append(direct, touchesFromNested(msg, context)...)
}

// collectTouches: recovery-note-touches.ts:358. defaultDir "" = undefined.
func collectTouches(entries []scannedNoteEntry, directories []string) ([]noteTouch, string) {
	results := collectResultMap(entries)
	known := &orderedSet{}
	for _, d := range directories {
		known.add(d)
	}
	learnNotesDirs(entries, results, known)
	sortedDirs := sortNoteDirectories(known.items)
	handled := make(map[string]struct{})
	var touches []noteTouch
	for i := range entries {
		item := &entries[i]
		msg, hasMsg := recOf(item.entry["message"])
		sourceID := item.entryId
		if v, ok := str(item.entry, "id"); ok {
			sourceID = v
		}
		context := &touchContext{entryID: item.entryId, sourceID: sourceID, inRetained: item.inRetained, knownDirs: sortedDirs}
		for _, call := range assistantCallsOf(item.entry) {
			id, has, out := touchesFromAssistantCall(call, results, context)
			if has {
				handled[id] = struct{}{}
			}
			touches = append(touches, out...)
		}
		if t, _ := str(item.entry, "type"); t == "message" && hasMsg {
			touches = append(touches, touchesFromToolResult(msg, handled, context)...)
		}
	}
	defaultDir := ""
	if len(known.items) == 1 {
		defaultDir = known.items[0]
	}
	return touches, defaultDir
}
