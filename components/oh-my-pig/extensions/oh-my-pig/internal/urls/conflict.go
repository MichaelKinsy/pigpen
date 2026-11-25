package urls

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// upstream: packages/coding-agent/src/internal-urls/conflict-protocol.ts,
//           packages/coding-agent/src/tools/conflict-detect.ts,
//           packages/coding-agent/src/tools/conflict-uri.ts,
//           packages/tui/src/tools/conflict-detect.ts

const (
	conflictOursPrefix   = "<<<<<<<"
	conflictBasePrefix   = "|||||||"
	conflictSeparator    = "======="
	conflictTheirsPrefix = ">>>>>>>"
	conflictMaxEchoLines = 12
	conflictPreviewLimit = 8 // upstream: render-utils.ts PREVIEW_LIMITS.COLLAPSED_ITEMS
	conflictStrippedNote = "Note: auto-stripped hashline display prefixes from content before writing."
)

// ConflictBlock is a parsed git merge-conflict marker block.
// upstream: packages/tui/src/tools/conflict-detect.ts ConflictBlock.
type ConflictBlock struct {
	StartLine     int
	SeparatorLine int
	EndLine       int
	BaseLine      int // 0 when 2-way merge
	OursLabel     string
	BaseLabel     string
	TheirsLabel   string
	OursLines     []string
	BaseLines     []string // nil when 2-way merge
	TheirsLines   []string
}

// ConflictEntry is a ConflictBlock registered in a session's ConflictHistory.
type ConflictEntry struct {
	ConflictBlock
	ID           int
	AbsolutePath string
	DisplayPath  string
}

// ConflictHistory tracks merge-conflict blocks surfaced by `read` in a session.
// upstream: tools/conflict-detect.ts ConflictHistory.
type ConflictHistory struct {
	mu      sync.Mutex
	nextID  int
	entries map[int]ConflictEntry
	order   []int
}

// NewConflictHistory creates an empty ConflictHistory.
func NewConflictHistory() *ConflictHistory {
	return &ConflictHistory{
		nextID:  1,
		entries: map[int]ConflictEntry{},
	}
}

// Register records a conflict block and returns its session-stable ConflictEntry.
// Reuses an ID only for the same block at the same position.
func (h *ConflictHistory) Register(absPath, displayPath string, block ConflictBlock) ConflictEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range h.order {
		existing, ok := h.entries[id]
		if !ok {
			continue
		}
		if existing.AbsolutePath == absPath && existing.StartLine == block.StartLine {
			if !conflictRegionsEqual(existing.ConflictBlock, block) {
				delete(h.entries, id)
				continue
			}
			merged := ConflictEntry{
				ConflictBlock: block,
				ID:            id,
				AbsolutePath:  absPath,
				DisplayPath:   displayPath,
			}
			h.entries[id] = merged
			return merged
		}
	}
	id := h.nextID
	h.nextID++
	entry := ConflictEntry{
		ConflictBlock: block,
		ID:            id,
		AbsolutePath:  absPath,
		DisplayPath:   displayPath,
	}
	h.entries[id] = entry
	h.order = append(h.order, id)
	return entry
}

// Get returns the registered entry for id.
func (h *ConflictHistory) Get(id int) (ConflictEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.entries[id]
	return e, ok
}

// Entries returns all registered entries in insertion (ID) order.
func (h *ConflictHistory) Entries() []ConflictEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ConflictEntry, 0, len(h.entries))
	for _, id := range h.order {
		if e, ok := h.entries[id]; ok {
			out = append(out, e)
		}
	}
	return out
}

// Invalidate drops a single entry by ID.
func (h *ConflictHistory) Invalidate(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.entries, id)
}

// InvalidatePath drops every entry referencing absPath.
func (h *ConflictHistory) InvalidatePath(absPath string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, e := range h.entries {
		if e.AbsolutePath == absPath {
			delete(h.entries, id)
		}
	}
}

// ReconcilePath refreshes registered entries for absPath against live blocks.
func (h *ConflictHistory) ReconcilePath(absPath string, blocks []ConflictBlock) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var entries []ConflictEntry
	for _, id := range h.order {
		if e, ok := h.entries[id]; ok && e.AbsolutePath == absPath {
			entries = append(entries, e)
		}
	}
	used := make([]bool, len(blocks))
	matched := map[int]ConflictBlock{}
	// First pass: exact startLine + content match.
	for _, e := range entries {
		for i, b := range blocks {
			if used[i] {
				continue
			}
			if b.StartLine == e.StartLine && conflictRegionsEqual(e.ConflictBlock, b) {
				matched[e.ID] = b
				used[i] = true
				break
			}
		}
	}
	// Second pass: nearest content match (relocated by earlier edits).
	for _, e := range entries {
		if _, ok := matched[e.ID]; ok {
			continue
		}
		bestIdx := -1
		bestDist := int(^uint(0) >> 1)
		for i, b := range blocks {
			if used[i] {
				continue
			}
			d := b.StartLine - e.StartLine
			if d < 0 {
				d = -d
			}
			if d < bestDist && conflictRegionsEqual(e.ConflictBlock, b) {
				bestDist = d
				bestIdx = i
			}
		}
		if bestIdx >= 0 {
			matched[e.ID] = blocks[bestIdx]
			used[bestIdx] = true
		}
	}
	for _, e := range entries {
		if b, ok := matched[e.ID]; ok {
			e.ConflictBlock = b
			h.entries[e.ID] = e
		} else {
			delete(h.entries, e.ID)
		}
	}
}

// ConflictWriteHooks are optional callbacks installed by the files area so
// `conflict://` writes can record hashline snapshots and file-change events
// without an import cycle between `internal/urls` and `internal/files`.
// upstream: tools/conflict-uri.ts:156-167.
type ConflictWriteHooks struct {
	// StripContent overrides built-in hashline prefix stripping when non-nil.
	StripContent func(c Context, content string) (cleaned string, stripped bool)
	// OnFileWritten is called after a conflict file is written; returns an
	// optional `[displayPath#TAG]` snapshot header to prepend to the result.
	OnFileWritten func(c Context, absPath, before, after string) (snapshotHeader string)
}

var (
	conflictHooksMu    sync.RWMutex
	conflictWriteHooks ConflictWriteHooks
)

// SetConflictWriteHooks installs callbacks for hashline stripping, snapshot
// header generation, and file-change recording on conflict:// writes.
func SetConflictWriteHooks(hooks ConflictWriteHooks) {
	conflictHooksMu.Lock()
	conflictWriteHooks = hooks
	conflictHooksMu.Unlock()
}

// SessionConflictHistory returns the ConflictHistory for the session in c, creating it on first use.
// If c carries a live sdk.Context whose session ID cannot be resolved, returns an ephemeral
// unshared history so sessions never collide on a default key.
func SessionConflictHistory(c Context) *ConflictHistory {
	key, ok := resolveSessionKey(c)
	if !ok {
		return NewConflictHistory()
	}
	sessionStateMu.Lock()
	defer sessionStateMu.Unlock()
	st := getOrCreateSessionStateLocked(key)
	if st.conflicts == nil {
		st.conflicts = NewConflictHistory()
	}
	return st.conflicts
}

// ResetConflictsForTests clears all session conflict histories and write hooks.
func ResetConflictsForTests() {
	sessionStateMu.Lock()
	for _, st := range sessionStates {
		st.conflicts = nil
	}
	sessionStateMu.Unlock()
	conflictHooksMu.Lock()
	conflictWriteHooks = ConflictWriteHooks{}
	conflictHooksMu.Unlock()
}

// ScanConflictLines scans file lines starting at 1-indexed firstLineNumber for completed conflict blocks.
// upstream: tools/conflict-detect.ts scanConflictLines.
func ScanConflictLines(lines []string, firstLineNumber int) []ConflictBlock {
	var blocks []ConflictBlock
	phase := "idle"
	var partial *ConflictBlock

	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		ln := firstLineNumber + i

		if label, ok := matchConflictMarker(line, conflictOursPrefix); ok {
			partial = &ConflictBlock{StartLine: ln, OursLabel: label}
			phase = "ours"
			continue
		}
		if phase == "idle" || partial == nil {
			continue
		}
		if label, ok := matchConflictMarker(line, conflictBasePrefix); ok {
			if phase != "ours" {
				partial = nil
				phase = "idle"
				continue
			}
			partial.BaseLine = ln
			partial.BaseLabel = label
			partial.BaseLines = []string{}
			phase = "base"
			continue
		}
		if line == conflictSeparator {
			if phase == "ours" || phase == "base" {
				partial.SeparatorLine = ln
				partial.TheirsLines = []string{}
				phase = "theirs"
			} else {
				partial = nil
				phase = "idle"
			}
			continue
		}
		if label, ok := matchConflictMarker(line, conflictTheirsPrefix); ok {
			if phase == "theirs" && partial.SeparatorLine != 0 {
				partial.EndLine = ln
				partial.TheirsLabel = label
				blocks = append(blocks, *partial)
			}
			partial = nil
			phase = "idle"
			continue
		}
		switch phase {
		case "ours":
			partial.OursLines = append(partial.OursLines, line)
		case "base":
			partial.BaseLines = append(partial.BaseLines, line)
		case "theirs":
			partial.TheirsLines = append(partial.TheirsLines, line)
		}
	}
	return blocks
}

func matchConflictMarker(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	if len(line) == len(prefix) {
		return "", true
	}
	if line[len(prefix)] != ' ' {
		return "", false
	}
	return line[len(prefix)+1:], true
}

func buildRecordedRegion(b ConflictBlock) []string {
	var out []string
	if b.OursLabel != "" {
		out = append(out, conflictOursPrefix+" "+b.OursLabel)
	} else {
		out = append(out, conflictOursPrefix)
	}
	out = append(out, b.OursLines...)
	if b.BaseLines != nil {
		if b.BaseLabel != "" {
			out = append(out, conflictBasePrefix+" "+b.BaseLabel)
		} else {
			out = append(out, conflictBasePrefix)
		}
		out = append(out, b.BaseLines...)
	}
	out = append(out, conflictSeparator)
	out = append(out, b.TheirsLines...)
	if b.TheirsLabel != "" {
		out = append(out, conflictTheirsPrefix+" "+b.TheirsLabel)
	} else {
		out = append(out, conflictTheirsPrefix)
	}
	return out
}

func conflictRegionsEqual(a, b ConflictBlock) bool {
	ra := buildRecordedRegion(a)
	rb := buildRecordedRegion(b)
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			return false
		}
	}
	return true
}

type parsedConflictURI struct {
	wildcard bool
	id       int
	scope    string // "", "ours", "theirs", "base"
}

var (
	conflictURIRE         = regexp.MustCompile(`^conflict://(.+)$`)
	prefixedConflictURIRE = regexp.MustCompile(`^(.+):(conflict://.+)$`)
	digitsOnlyRE          = regexp.MustCompile(`^\d+$`)
	bulkDirectiveRE       = regexp.MustCompile(`^(?:\d+:)?#?(\d+)\s*[:=]\s*(@ours|@theirs|@base|@both)$`)
	bulkDirectiveHeadRE   = regexp.MustCompile(`^(?:\d+:)?#?(\d+)\s*[:=]`)
	looseHashlineHeaderRE = regexp.MustCompile(`^\s*\[[^#\r\n]+#[^ \t\r\n]*\]\s*$`)
)

// RecoverConflictURIPrefix strips an erroneous `<file>:` prefix from
// `<file>:conflict://...` and returns an explanatory note for the model.
// Non-matching inputs pass through unchanged with an empty note.
// upstream: tools/conflict-detect.ts recoverConflictUriPrefix.
func RecoverConflictURIPrefix(raw string) (pathOut, note string) {
	m := prefixedConflictURIRE.FindStringSubmatch(raw)
	if m == nil {
		return raw, ""
	}
	prefix, u := m[1], m[2]
	return u, fmt.Sprintf("Note: stripped erroneous '%s:' prefix from path; conflict URIs are global (use `%s`, not `<file>:%s`).", prefix, u, u)
}

func stripConflictHashlinePrefixes(c Context, content string) (string, bool) {
	conflictHooksMu.RLock()
	hook := conflictWriteHooks.StripContent
	conflictHooksMu.RUnlock()
	if hook != nil {
		return hook(c, content)
	}
	return defaultStripHashlinePrefixes(content)
}

func joinStrippedHashlines(lines []string, orig string) (string, bool) {
	cleaned := hashline.StripHashlinePrefixes(lines)
	if len(cleaned) == len(lines) {
		same := true
		for i := range lines {
			if cleaned[i] != lines[i] {
				same = false
				break
			}
		}
		if same {
			return "", false
		}
	}
	cleanedText := strings.Join(cleaned, "\n")
	if cleanedText == orig {
		return "", false
	}
	return cleanedText, true
}

// defaultStripHashlinePrefixes mirrors omp's stripWriteContentWithPotentialLooseHeader:
// delegates to hashline.StripHashlinePrefixes (which strips only when every content row
// is prefixed and line numbers count up by one), and falls back to removing a single
// loose `[path#tag]` header ONLY if the remaining lines then strip.
// upstream: tools/write-content.ts:71-99.
func defaultStripHashlinePrefixes(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	if cleaned, ok := joinStrippedHashlines(lines, content); ok {
		return cleaned, true
	}
	headerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 || !looseHashlineHeaderRE.MatchString(lines[headerIdx]) {
		return content, false
	}
	withoutHeader := make([]string, 0, len(lines)-1)
	withoutHeader = append(withoutHeader, lines[:headerIdx]...)
	withoutHeader = append(withoutHeader, lines[headerIdx+1:]...)
	if cleaned, ok := joinStrippedHashlines(withoutHeader, strings.Join(withoutHeader, "\n")); ok {
		return cleaned, true
	}
	return content, false
}

func parseConflictURI(raw string) (*parsedConflictURI, error) {
	m := conflictURIRE.FindStringSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("Invalid conflict URI '%s': must be 'conflict://<N>', 'conflict://<N>/<scope>', or 'conflict://*'.", raw)
	}
	tail := m[1]
	idPart, scopePart, hasSlash := strings.Cut(tail, "/")

	if idPart == "*" {
		if hasSlash {
			return nil, fmt.Errorf("Invalid conflict URI '%s': wildcard 'conflict://*' does not accept a scope segment. Drop '/%s' or use a numeric id.", raw, scopePart)
		}
		return &parsedConflictURI{wildcard: true}, nil
	}
	if !digitsOnlyRE.MatchString(idPart) {
		return nil, fmt.Errorf("Invalid conflict URI '%s': must be 'conflict://<N>', 'conflict://<N>/<scope>', or 'conflict://*' where N is a positive integer surfaced by a prior `read`.", raw)
	}
	id, err := strconv.Atoi(idPart)
	if err != nil || id < 1 {
		return nil, fmt.Errorf("Invalid conflict URI '%s': id must be ≥ 1.", raw)
	}
	scope := ""
	if hasSlash {
		switch scopePart {
		case "ours", "theirs", "base":
			scope = scopePart
		default:
			return nil, fmt.Errorf("Invalid conflict URI '%s': scope must be one of 'ours', 'theirs', 'base', or omitted (e.g. 'conflict://%d/theirs').", raw, id)
		}
	}
	return &parsedConflictURI{id: id, scope: scope}, nil
}

func renderConflictRegion(entry ConflictEntry, scope string) ([]string, int, error) {
	switch scope {
	case "ours":
		return append([]string(nil), entry.OursLines...), entry.StartLine + 1, nil
	case "theirs":
		return append([]string(nil), entry.TheirsLines...), entry.SeparatorLine + 1, nil
	case "base":
		if entry.BaseLines == nil || entry.BaseLine == 0 {
			return nil, 0, fmt.Errorf("Conflict #%d has no base section (2-way merge). 'conflict://%d/base' is only valid for diff3 conflicts.", entry.ID, entry.ID)
		}
		return append([]string(nil), entry.BaseLines...), entry.BaseLine + 1, nil
	default:
		return buildRecordedRegion(entry.ConflictBlock), entry.StartLine, nil
	}
}

// reconcileFileOnDisk streams absPath with bounded line memory to find blocks
// matching registered entries for absPath and reconciles them in h.
// upstream: tools/conflict-detect.ts findRegisteredBlocks, reconcileRegisteredConflicts.
func reconcileFileOnDisk(h *ConflictHistory, absPath string) error {
	var entries []ConflictEntry
	for _, e := range h.Entries() {
		if e.AbsolutePath == absPath {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	blocks, err := findRegisteredBlocksStreaming(absPath, entries)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isNotDirErr(err) {
			h.InvalidatePath(absPath)
			return nil
		}
		return err
	}
	h.ReconcilePath(absPath, blocks)
	return nil
}

func findRegisteredBlocksStreaming(absPath string, entries []ConflictEntry) ([]ConflictBlock, error) {
	regions := map[string]bool{}
	unconfirmed := map[int]string{}
	maxLines := 0
	maxLineBytes := 0
	for _, e := range entries {
		region := buildRecordedRegion(e.ConflictBlock)
		key := strings.Join(region, "\n")
		regions[key] = true
		unconfirmed[e.StartLine] = key
		if len(region) > maxLines {
			maxLines = len(region)
		}
		for _, line := range region {
			if len(line) > maxLineBytes {
				maxLineBytes = len(line)
			}
		}
	}
	maxLineBytes++ // trailing CR

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var found []ConflictBlock
	var candidate []string
	candidateStart := 0
	feed := func(line string, ok bool, lineNumber int) bool {
		if ok {
			line = strings.TrimSuffix(line, "\r")
		}
		if ok {
			if _, isOurs := matchConflictMarker(line, conflictOursPrefix); isOurs {
				candidate = []string{line}
				candidateStart = lineNumber
				return false
			}
		}
		if len(candidate) == 0 {
			return false
		}
		if !ok || len(candidate) == maxLines {
			candidate = nil
			return false
		}
		candidate = append(candidate, line)
		if _, isTheirs := matchConflictMarker(line, conflictTheirsPrefix); !isTheirs {
			return false
		}
		blocks := ScanConflictLines(candidate, candidateStart)
		candidate = nil
		if len(blocks) == 0 {
			return false
		}
		block := blocks[0]
		key := strings.Join(buildRecordedRegion(block), "\n")
		if !regions[key] {
			return false
		}
		found = append(found, block)
		if unconfirmed[block.StartLine] == key {
			delete(unconfirmed, block.StartLine)
		}
		return len(unconfirmed) == 0
	}

	reader := bufio.NewReaderSize(f, 32*1024)
	lineNumber := 1
	lineLimit := maxLineBytes + 3 // budget UTF-8 BOM on line 1
	for {
		var buf []byte
		overLimit := false
		var readErr error
		for {
			frag, err := reader.ReadSlice('\n')
			if err == bufio.ErrBufferFull {
				if !overLimit {
					if len(buf)+len(frag) > lineLimit {
						overLimit = true
						buf = nil
					} else {
						buf = append(buf, frag...)
					}
				}
				continue
			}
			if !overLimit {
				trimmed := frag
				if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\n' {
					trimmed = trimmed[:len(trimmed)-1]
				}
				if len(buf)+len(trimmed) > lineLimit {
					overLimit = true
					buf = nil
				} else {
					buf = append(buf, trimmed...)
				}
			}
			readErr = err
			break
		}
		if readErr == io.EOF && len(buf) == 0 && !overLimit {
			break
		}
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		lineStr := ""
		if !overLimit {
			lineStr = string(buf)
			if lineNumber == 1 {
				lineStr = strings.TrimPrefix(lineStr, "\xef\xbb\xbf")
			}
		}
		if feed(lineStr, !overLimit, lineNumber) {
			return found, nil
		}
		lineNumber++
		lineLimit = maxLineBytes
		if readErr == io.EOF {
			break
		}
	}
	return found, nil
}

type conflictReplacement struct {
	lines         []string
	typedLeading  int
	typedTrailing int
}

func expandConflictTokens(content string, entry ConflictEntry) (conflictReplacement, error) {
	trimmed := content
	if strings.HasSuffix(trimmed, "\r\n") {
		trimmed = trimmed[:len(trimmed)-2]
	} else if strings.HasSuffix(trimmed, "\n") {
		trimmed = trimmed[:len(trimmed)-1]
	}

	var out []string
	firstExpanded := -1
	lastExpandedEnd := 0
	pushSection := func(sec []string) {
		if len(sec) == 0 {
			return
		}
		if firstExpanded < 0 {
			firstExpanded = len(out)
		}
		out = append(out, sec...)
		lastExpandedEnd = len(out)
	}

	for _, rawLine := range strings.Split(trimmed, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		switch line {
		case "@ours":
			pushSection(entry.OursLines)
		case "@theirs":
			pushSection(entry.TheirsLines)
		case "@base":
			if entry.BaseLines == nil {
				return conflictReplacement{}, fmt.Errorf("Conflict #%d has no base section (2-way merge). `@base` is only valid for diff3 conflicts.", entry.ID)
			}
			pushSection(entry.BaseLines)
		case "@both":
			pushSection(entry.OursLines)
			pushSection(entry.TheirsLines)
		default:
			out = append(out, line)
		}
	}
	typedLeading := len(out)
	if firstExpanded >= 0 {
		typedLeading = firstExpanded
	}
	return conflictReplacement{
		lines:         out,
		typedLeading:  typedLeading,
		typedTrailing: len(out) - lastExpandedEnd,
	}, nil
}

func spliceConflict(originalText string, entry ConflictEntry, repl conflictReplacement) (text string, trimmedLeading, trimmedTrailing int, err error) {
	lines := strings.Split(originalText, "\n")
	expected := buildRecordedRegion(entry.ConflictBlock)
	startIdx, endIdx, found := locateConflictRegion(lines, expected, entry.StartLine-1)
	if !found {
		return "", 0, 0, fmt.Errorf("Conflict #%d no longer present in '%s': the recorded marker block can't be located. The file changed since the conflict was registered — re-read it to re-register conflicts.", entry.ID, entry.DisplayPath)
	}

	spliced, lead, trail := trimConflictBoundaryEcho(repl, lines, startIdx, endIdx, entry.ConflictBlock)
	hasFollowing := endIdx+1 < len(lines)
	if strings.HasSuffix(lines[startIdx], "\r") {
		crlf := make([]string, len(spliced))
		for i, l := range spliced {
			if i < len(spliced)-1 || hasFollowing {
				crlf[i] = l + "\r"
			} else {
				crlf[i] = l
			}
		}
		spliced = crlf
	}
	before := append([]string(nil), lines[:startIdx]...)
	if len(spliced) == 0 && !hasFollowing && len(before) > 0 {
		before[len(before)-1] = strings.TrimSuffix(before[len(before)-1], "\r")
	}
	next := make([]string, 0, len(before)+len(spliced)+len(lines)-(endIdx+1))
	next = append(next, before...)
	next = append(next, spliced...)
	next = append(next, lines[endIdx+1:]...)
	return strings.Join(next, "\n"), lead, trail, nil
}

func locateConflictRegion(lines, expected []string, preferredIdx int) (startIdx, endIdx int, ok bool) {
	if len(expected) == 0 || len(expected) > len(lines) {
		return 0, 0, false
	}
	matchesAt := func(idx int) bool {
		if idx < 0 || idx+len(expected) > len(lines) {
			return false
		}
		for i := range expected {
			if strings.TrimSuffix(lines[idx+i], "\r") != expected[i] {
				return false
			}
		}
		return true
	}
	if preferredIdx >= 0 && matchesAt(preferredIdx) {
		return preferredIdx, preferredIdx + len(expected) - 1, true
	}
	best := -1
	bestDist := int(^uint(0) >> 1)
	for i := 0; i <= len(lines)-len(expected); i++ {
		if !matchesAt(i) {
			continue
		}
		d := i - preferredIdx
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best = i
			bestDist = d
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, best + len(expected) - 1, true
}

func delimiterBalance(lines []string) int {
	bal := 0
	for _, l := range lines {
		for i := 0; i < len(l); i++ {
			switch l[i] {
			case '{', '(', '[':
				bal++
			case '}', ')', ']':
				bal--
			}
		}
	}
	return bal
}

func trimConflictBoundaryEcho(repl conflictReplacement, fileLines []string, startIdx, endIdx int, block ConflictBlock) ([]string, int, int) {
	oursBal := delimiterBalance(block.OursLines)
	hasExpected := oursBal == delimiterBalance(block.TheirsLines)
	singleJustified := func(lines, without []string) bool {
		return hasExpected && delimiterBalance(lines) != oursBal && delimiterBalance(without) == oursBal
	}

	lines := append([]string(nil), repl.lines...)
	trailing := 0
	var after []string
	for i := endIdx + 1; i < len(fileLines) && len(after) < conflictMaxEchoLines; i++ {
		after = append(after, strings.TrimSuffix(fileLines[i], "\r"))
	}
	maxK := minInt(len(after), minInt(len(lines)-1, repl.typedTrailing))
	for k := maxK; k >= 1; k-- {
		match := true
		for i := 0; i < k; i++ {
			if lines[len(lines)-k+i] != after[i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if k >= 2 || singleJustified(lines, lines[:len(lines)-1]) {
			trailing = k
			lines = lines[:len(lines)-k]
		}
		break
	}

	leading := 0
	var before []string
	for i := startIdx - 1; i >= 0 && len(before) < conflictMaxEchoLines; i-- {
		before = append([]string{strings.TrimSuffix(fileLines[i], "\r")}, before...)
	}
	maxK = minInt(len(before), minInt(len(lines)-1, repl.typedLeading))
	for k := maxK; k >= 1; k-- {
		match := true
		for i := 0; i < k; i++ {
			if lines[i] != before[len(before)-k+i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if k >= 2 || singleJustified(lines, lines[1:]) {
			leading = k
			lines = lines[k:]
		}
		break
	}
	return lines, leading, trailing
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseBulkDirectives(content string, registeredIDs map[int]bool) (map[int]string, error) {
	result := map[int]string{}
	var stray []string
	sawDirective := false

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		m := bulkDirectiveRE.FindStringSubmatch(line)
		if m == nil {
			stray = append(stray, line)
			continue
		}
		sawDirective = true
		id, _ := strconv.Atoi(m[1])
		if _, dup := result[id]; dup {
			return nil, fmt.Errorf("Bulk directive lists conflict #%d twice — each id may appear once.", id)
		}
		result[id] = m[2]
	}

	if !sawDirective {
		if len(stray) == 0 {
			return nil, nil
		}
		heads := make([]int, len(stray))
		regCount := 0
		uniqueReg := map[int]bool{}
		allKnownOrNone := true
		for i, line := range stray {
			if m := bulkDirectiveHeadRE.FindStringSubmatch(line); m != nil {
				id, _ := strconv.Atoi(m[1])
				heads[i] = id
				if registeredIDs[id] {
					regCount++
					uniqueReg[id] = true
				} else {
					allKnownOrNone = false
				}
			}
		}
		literalPerID := heads[0] != 0 && registeredIDs[heads[0]] && allKnownOrNone && (regCount == len(stray) || len(uniqueReg) >= 2)
		if !literalPerID {
			return nil, nil
		}
	}

	if len(stray) > 0 {
		sample := truncateDirectiveLine(stray[0])
		tokenHint := ""
		if bulkDirectiveHeadRE.MatchString(sample) {
			tokenHint = "Per-id bulk only accepts the tokens @ours/@theirs/@base/@both — one side per id, single line. "
		}
		return nil, fmt.Errorf(
			"Malformed `conflict://*` per-id block: %d line(s) are not `<id>: @side` directives (first: `%s`). %sLiteral or multi-line replacement content isn't supported in a per-id block — resolve those blocks with individual `write({ path: \"conflict://<N>\", content })` calls (you can issue several at once). For a pure pick-a-side pass, make every non-empty line `<id>: @ours` (or @theirs/@base/@both).",
			len(stray), sample, tokenHint,
		)
	}
	return result, nil
}

func truncateDirectiveLine(line string) string {
	runes := []rune(line)
	if len(runes) > 60 {
		return string(runes[:57]) + "…"
	}
	return line
}

// resolveBulkDirectives prefers pre-strip raw content and falls back to
// hashline-stripped content, mirroring upstream conflict-uri.ts:281-301.
func resolveBulkDirectives(raw, stripped string, registeredIDs map[int]bool) (map[int]string, error) {
	if raw == stripped {
		return parseBulkDirectives(raw, registeredIDs)
	}
	rawRes, rawErr := parseBulkDirectives(raw, registeredIDs)
	if rawErr != nil {
		if fb, fbErr := parseBulkDirectives(stripped, registeredIDs); fbErr == nil && fb != nil {
			return fb, nil
		}
		return nil, rawErr
	}
	if rawRes != nil {
		return rawRes, nil
	}
	return parseBulkDirectives(stripped, registeredIDs)
}

// ConflictHandler serves `conflict://<N>[/<ours|theirs|base>]` and `conflict://*`.
// upstream: conflict-protocol.ts ConflictProtocolHandler.
type ConflictHandler struct{}

func (ConflictHandler) Scheme() string { return "conflict" }

func (ConflictHandler) Spec() Spec {
	return Spec{
		Backing:   BackingVirtual,
		Selectors: SelectorsLines,
		Immutable: false,
		Write: &WritePolicy{
			Via:     "handler",
			Payload: "verbatim",
			Scope:   "workspace",
		},
	}
}

func (ConflictHandler) Resolve(c Context, u URL) (*Resource, error) {
	target, err := parseConflictURI(u.RawHref)
	if err != nil {
		return nil, err
	}
	if target.wildcard {
		return nil, errors.New("Reading `conflict://*` is not supported — wildcards are write-only. Use the `<path>:conflicts` read selector for the full list of conflicts in a file, or read `conflict://<N>` to inspect a single block.")
	}
	if _, ok := resolveSessionKey(c); !ok {
		return nil, errors.New("conflict:// requires a calling session.")
	}
	h := SessionConflictHistory(c)
	entry, err := requireConflictEntry(h, c.Cwd, target.id)
	if err != nil {
		return nil, err
	}
	lines, startLine, err := renderConflictRegion(entry, target.scope)
	if err != nil {
		return nil, err
	}
	return &Resource{
		URL:             u.Href,
		Content:         strings.Join(lines, "\n"),
		ContentType:     "text/plain",
		SourcePath:      entry.AbsolutePath,
		SourceStartLine: startLine,
	}, nil
}

func (ConflictHandler) Write(c Context, u URL, content string) (*WriteResult, error) {
	target, err := parseConflictURI(u.RawHref)
	if err != nil {
		return nil, err
	}
	if target.scope != "" {
		return nil, fmt.Errorf(
			"Conflict URI scope '/%s' is read-only — read `conflict://%d/%s` to inspect that side. To write, drop the scope (`conflict://%d`) and put the chosen content (or shorthand like `@%s`) in `content`.",
			target.scope, target.id, target.scope, target.id, target.scope,
		)
	}
	if _, ok := resolveSessionKey(c); !ok {
		return nil, errors.New("conflict:// requires a calling session.")
	}
	cleanContent, stripped := stripConflictHashlinePrefixes(c, content)
	h := SessionConflictHistory(c)
	if target.wildcard {
		return resolveAllConflicts(c, h, cleanContent, stripped, content)
	}
	entry, err := requireConflictEntry(h, c.Cwd, target.id)
	if err != nil {
		return nil, err
	}
	return resolveSingleConflict(c, h, entry, cleanContent, stripped)
}

func requireConflictEntry(h *ConflictHistory, cwd string, id int) (ConflictEntry, error) {
	if reg, ok := h.Get(id); ok {
		if err := reconcileFileOnDisk(h, reg.AbsolutePath); err != nil {
			return ConflictEntry{}, err
		}
	}
	if entry, ok := h.Get(id); ok {
		return entry, nil
	}
	byPath := map[string][]int{}
	var paths []string
	for _, e := range h.Entries() {
		if _, seen := byPath[e.AbsolutePath]; !seen {
			paths = append(paths, e.AbsolutePath)
		}
		byPath[e.AbsolutePath] = append(byPath[e.AbsolutePath], e.ID)
	}
	known := "No conflicts are registered."
	if len(paths) > 0 {
		more := func(hidden int, noun string) string {
			if hidden > 0 {
				return fmt.Sprintf(" (+%d more%s)", hidden, noun)
			}
			return ""
		}
		shownPaths := paths
		if len(shownPaths) > conflictPreviewLimit {
			shownPaths = shownPaths[:conflictPreviewLimit]
		}
		parts := make([]string, 0, len(shownPaths))
		for _, p := range shownPaths {
			rel := formatConflictDisplayPath(p, cwd)
			ids := byPath[p]
			shownIDs := ids
			if len(shownIDs) > conflictPreviewLimit {
				shownIDs = shownIDs[:conflictPreviewLimit]
			}
			idStrs := make([]string, len(shownIDs))
			for i, cid := range shownIDs {
				idStrs[i] = fmt.Sprintf("#%d", cid)
			}
			parts = append(parts, fmt.Sprintf("%s%s in %s", strings.Join(idStrs, ", "), more(len(ids)-conflictPreviewLimit, ""), rel))
		}
		known = "Registered: " + strings.Join(parts, "; ") + more(len(paths)-conflictPreviewLimit, " files") + "."
	}
	return ConflictEntry{}, fmt.Errorf(
		"Conflict #%d not found: it was resolved, became stale, or no `read` has shown its block yet (a ranged read registers only the blocks it shows, so unseen blocks' ids can't be guessed). Read `<path>:conflicts` on the file to list and register its blocks. %s",
		id, known,
	)
}

func formatConflictDisplayPath(absPath, cwd string) string {
	if cwd != "" {
		if r, err := filepath.Rel(cwd, absPath); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(absPath)
}

func resolveSingleConflict(c Context, h *ConflictHistory, entry ConflictEntry, replacement string, stripped bool) (*WriteResult, error) {
	rawBytes, err := os.ReadFile(entry.AbsolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Conflict #%d target '%s' no longer exists.", entry.ID, entry.DisplayPath)
		}
		return nil, err
	}
	expanded, err := expandConflictTokens(replacement, entry)
	if err != nil {
		return nil, err
	}
	bom := ""
	origText := string(rawBytes)
	if strings.HasPrefix(origText, "\xef\xbb\xbf") {
		bom = "\xef\xbb\xbf"
		origText = origText[3:]
	}
	newText, lead, trail, err := spliceConflict(origText, entry, expanded)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(entry.AbsolutePath, []byte(bom+newText), 0o644); err != nil {
		return nil, err
	}
	h.Invalidate(entry.ID)
	h.ReconcilePath(entry.AbsolutePath, ScanConflictLines(strings.Split(newText, "\n"), 1))

	var header string
	conflictHooksMu.RLock()
	onWritten := conflictWriteHooks.OnFileWritten
	conflictHooksMu.RUnlock()
	if onWritten != nil {
		header = onWritten(c, entry.AbsolutePath, bom+origText, bom+newText)
	}

	lineRange := fmt.Sprintf("lines %d\u2013%d", entry.StartLine, entry.EndLine)
	if entry.StartLine == entry.EndLine {
		lineRange = fmt.Sprintf("line %d", entry.StartLine)
	}
	summary := fmt.Sprintf("Resolved conflict #%d at %s in %s.", entry.ID, lineRange, entry.DisplayPath)
	resultText := summary
	if header != "" {
		resultText = header + "\n" + summary
	}
	if stripped {
		resultText += "\n" + conflictStrippedNote
	}
	if echoTrimmed := lead + trail; echoTrimmed > 0 {
		resultText += fmt.Sprintf("\nNote: dropped %d content line(s) that duplicated the code adjacent to the conflict region — writes replace only the marker block; surrounding lines stay in place.", echoTrimmed)
	}
	return &WriteResult{
		Text:    resultText,
		Details: map[string]any{"resolvedPath": entry.AbsolutePath},
	}, nil
}

func resolveAllConflicts(c Context, h *ConflictHistory, replacementContent string, stripped bool, rawContent string) (*WriteResult, error) {
	// Reconcile every registered file.
	seenFiles := map[string]bool{}
	for _, e := range h.Entries() {
		if !seenFiles[e.AbsolutePath] {
			seenFiles[e.AbsolutePath] = true
			if err := reconcileFileOnDisk(h, e.AbsolutePath); err != nil {
				return nil, err
			}
		}
	}
	allEntries := h.Entries()
	if len(allEntries) == 0 {
		return nil, errors.New("`conflict://*` has nothing to resolve — no conflicts are currently registered. Re-read the file(s) with conflicts first.")
	}

	known := map[int]bool{}
	for _, e := range allEntries {
		known[e.ID] = true
	}
	directives, err := resolveBulkDirectives(rawContent, replacementContent, known)
	if err != nil {
		return nil, err
	}
	if directives != nil {
		var unknown []int
		for id := range directives {
			if !known[id] {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			sort.Ints(unknown)
			uStrs := make([]string, len(unknown))
			for i, id := range unknown {
				uStrs[i] = fmt.Sprintf("#%d", id)
			}
			kStrs := make([]string, len(allEntries))
			for i, e := range allEntries {
				kStrs[i] = fmt.Sprintf("#%d", e.ID)
			}
			return nil, fmt.Errorf("Bulk directive references unknown conflict id(s) %s. Currently registered: %s.", strings.Join(uStrs, ", "), strings.Join(kStrs, ", "))
		}
	}

	var selected []ConflictEntry
	for _, e := range allEntries {
		if directives == nil {
			selected = append(selected, e)
		} else if _, ok := directives[e.ID]; ok {
			selected = append(selected, e)
		}
	}

	byFile := map[string][]ConflictEntry{}
	var fileOrder []string
	for _, e := range selected {
		if _, ok := byFile[e.AbsolutePath]; !ok {
			fileOrder = append(fileOrder, e.AbsolutePath)
		}
		byFile[e.AbsolutePath] = append(byFile[e.AbsolutePath], e)
	}

	conflictHooksMu.RLock()
	onWritten := conflictWriteHooks.OnFileWritten
	conflictHooksMu.RUnlock()

	type fileOutcome struct {
		displayPath string
		count       int
		header      string
		errMsg      string
	}
	var succeeded, failed []fileOutcome
	totalResolved := 0
	totalEchoTrimmed := 0

	for _, absPath := range fileOrder {
		entries := byFile[absPath]
		sample := entries[0]
		rawBytes, err := os.ReadFile(absPath)
		if err != nil {
			msg := err.Error()
			if errors.Is(err, os.ErrNotExist) {
				msg = "file no longer exists"
			}
			failed = append(failed, fileOutcome{displayPath: sample.DisplayPath, count: len(entries), errMsg: msg})
			continue
		}
		// Apply bottom-up by startLine so earlier line indices remain valid.
		sort.Slice(entries, func(i, j int) bool { return entries[i].StartLine > entries[j].StartLine })

		bom := ""
		text := string(rawBytes)
		if strings.HasPrefix(text, "\xef\xbb\xbf") {
			bom = "\xef\xbb\xbf"
			text = text[3:]
		}
		original := text
		var resolved []ConflictEntry
		var failMsg string
		for _, e := range entries {
			cForEntry := replacementContent
			if directives != nil {
				cForEntry = directives[e.ID]
			}
			expanded, err := expandConflictTokens(cForEntry, e)
			if err != nil {
				failMsg = err.Error()
				break
			}
			next, lead, trail, err := spliceConflict(text, e, expanded)
			if err != nil {
				failMsg = err.Error()
				break
			}
			text = next
			totalEchoTrimmed += lead + trail
			resolved = append(resolved, e)
		}
		if failMsg != "" {
			failed = append(failed, fileOutcome{displayPath: sample.DisplayPath, count: len(entries), errMsg: failMsg})
			continue
		}
		if err := os.WriteFile(absPath, []byte(bom+text), 0o644); err != nil {
			failed = append(failed, fileOutcome{displayPath: sample.DisplayPath, count: len(entries), errMsg: err.Error()})
			continue
		}
		for _, e := range resolved {
			h.Invalidate(e.ID)
		}
		h.ReconcilePath(absPath, ScanConflictLines(strings.Split(text, "\n"), 1))
		header := ""
		if onWritten != nil {
			header = onWritten(c, absPath, bom+original, bom+text)
		}
		succeeded = append(succeeded, fileOutcome{displayPath: sample.DisplayPath, count: len(resolved), header: header})
		totalResolved += len(resolved)
	}

	plural := func(n int, sg, pl string) string {
		if n == 1 {
			return sg
		}
		return pl
	}
	var summary []string
	if len(succeeded) > 0 {
		summary = append(summary, fmt.Sprintf("Resolved %d %s across %d %s:", totalResolved, plural(totalResolved, "conflict", "conflicts"), len(succeeded), plural(len(succeeded), "file", "files")))
		for _, f := range succeeded {
			summary = append(summary, fmt.Sprintf("  %s: %d %s", f.displayPath, f.count, plural(f.count, "conflict", "conflicts")))
		}
	}
	if directives != nil {
		var remaining []string
		for _, e := range h.Entries() {
			if _, ok := directives[e.ID]; !ok {
				remaining = append(remaining, fmt.Sprintf("#%d", e.ID))
			}
		}
		if len(remaining) > 0 {
			summary = append(summary, fmt.Sprintf("Directive mode: %d unlisted %s still registered (%s).", len(remaining), plural(len(remaining), "conflict", "conflicts"), strings.Join(remaining, ", ")))
		}
	}
	if totalEchoTrimmed > 0 {
		summary = append(summary, fmt.Sprintf("Note: dropped %d content line(s) that duplicated code adjacent to conflict regions — writes replace only the marker block; surrounding lines stay in place.", totalEchoTrimmed))
	}
	if len(failed) > 0 {
		summary = append(summary, fmt.Sprintf("Failed to resolve %d %s — registered entries left intact for retry:", len(failed), plural(len(failed), "file", "files")))
		for _, f := range failed {
			summary = append(summary, fmt.Sprintf("  %s: %d %s (%s)", f.displayPath, f.count, plural(f.count, "conflict", "conflicts"), f.errMsg))
		}
	}
	var headerLines []string
	for _, f := range succeeded {
		if f.header != "" {
			headerLines = append(headerLines, f.header)
		}
	}
	if len(headerLines) > 0 {
		summary = append(summary, "Snapshots:")
		for _, hl := range headerLines {
			summary = append(summary, "  "+hl)
		}
	}
	if stripped && directives == nil {
		summary = append(summary, conflictStrippedNote)
	}
	resultText := strings.Join(summary, "\n")
	if len(failed) > 0 && len(succeeded) == 0 {
		return nil, errors.New(resultText)
	}
	return &WriteResult{
		Text:    resultText,
		Details: map[string]any{},
		IsError: len(failed) > 0,
	}, nil
}
