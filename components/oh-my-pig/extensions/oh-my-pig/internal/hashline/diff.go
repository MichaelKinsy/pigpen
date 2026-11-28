package hashline

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// jsdiff-compatible Myers O(ND) diff and numbered diff / compact preview rendering.
// upstream: crates/diff/src/lib.rs, crates/edit/src/diff_string.rs

// DiffRun is a run of tokens sharing one edit classification, in forward order.
// upstream: crates/diff/src/lib.rs:15
type DiffRun struct {
	Count   uint32
	Added   bool
	Removed bool
}

const noComponent uint32 = math.MaxUint32

type componentNode struct {
	count   uint32
	prev    uint32
	refs    uint32
	added   bool
	removed bool
}

type componentArena struct {
	nodes    []componentNode
	freeHead uint32
}

func newComponentArena(capacity int) componentArena {
	return componentArena{
		nodes:    make([]componentNode, 0, capacity),
		freeHead: noComponent,
	}
}

func (a *componentArena) alloc(count, prev uint32, added, removed bool) uint32 {
	if a.freeHead == noComponent {
		idx := uint32(len(a.nodes))
		a.nodes = append(a.nodes, componentNode{count: count, prev: prev, refs: 1, added: added, removed: removed})
		return idx
	}
	idx := a.freeHead
	a.freeHead = a.nodes[idx].prev
	a.nodes[idx] = componentNode{count: count, prev: prev, refs: 1, added: added, removed: removed}
	return idx
}

func (a *componentArena) cloneRef(idx uint32) uint32 {
	if idx != noComponent {
		a.nodes[idx].refs++
	}
	return idx
}

func (a *componentArena) release(idx uint32) {
	for idx != noComponent {
		node := &a.nodes[idx]
		node.refs--
		if node.refs > 0 {
			break
		}
		prev := node.prev
		node.prev = a.freeHead
		a.freeHead = idx
		idx = prev
	}
}

type pathState struct {
	oldPos int
	last   uint32
	valid  bool
}

func extractCommon(arena *componentArena, path *pathState, newIDs, oldIDs []uint32, diagonal int) int {
	newLen := len(newIDs)
	oldLen := len(oldIDs)
	oldPos := path.oldPos
	newPos := oldPos - diagonal
	var common uint32
	for newPos+1 < newLen && oldPos+1 < oldLen && oldIDs[oldPos+1] == newIDs[newPos+1] {
		newPos++
		oldPos++
		common++
	}
	if common > 0 {
		path.last = arena.alloc(common, path.last, false, false)
	}
	path.oldPos = oldPos
	return newPos
}

func addToPath(arena *componentArena, path pathState, added, removed bool, oldPosInc int) pathState {
	lastIdx := path.last
	var newLast uint32
	if lastIdx == noComponent {
		newLast = arena.alloc(1, noComponent, added, removed)
	} else {
		last := arena.nodes[lastIdx]
		if last.added == added && last.removed == removed {
			if last.refs == 1 {
				arena.nodes[lastIdx].count = last.count + 1
				newLast = lastIdx
			} else {
				arena.nodes[lastIdx].refs--
				prev := arena.cloneRef(last.prev)
				newLast = arena.alloc(last.count+1, prev, added, removed)
			}
		} else {
			newLast = arena.alloc(1, lastIdx, added, removed)
		}
	}
	return pathState{oldPos: path.oldPos + oldPosInc, last: newLast, valid: true}
}

func buildDiffRuns(arena *componentArena, last uint32) []DiffRun {
	var runs []DiffRun
	cursor := last
	for cursor != noComponent {
		c := arena.nodes[cursor]
		runs = append(runs, DiffRun{Count: c.count, Added: c.added, Removed: c.removed})
		cursor = c.prev
	}
	slices.Reverse(runs)
	return runs
}

// internStrings interns tokens as dense uint32 IDs under exact equality.
// upstream: crates/diff/src/lib.rs:273
func internStrings(oldTokens, newTokens []string) ([]uint32, []uint32) {
	ids := make(map[string]uint32, len(oldTokens)+len(newTokens))
	assign := func(tok string) uint32 {
		if id, ok := ids[tok]; ok {
			return id
		}
		id := uint32(len(ids))
		ids[tok] = id
		return id
	}
	oldIDs := make([]uint32, len(oldTokens))
	for i, t := range oldTokens {
		oldIDs[i] = assign(t)
	}
	newIDs := make([]uint32, len(newTokens))
	for i, t := range newTokens {
		newIDs[i] = assign(t)
	}
	return oldIDs, newIDs
}

// myersDiff computes jsdiff-compatible Myers O(ND) runs.
// upstream: crates/diff/src/lib.rs:178
func myersDiff(oldIDs, newIDs []uint32) []DiffRun {
	runs, _ := myersDiffWithin(oldIDs, newIDs, len(oldIDs)+len(newIDs))
	return runs
}

// myersDiffWithin computes Myers diff bounded to maxEdits; returns (nil, false) when exceeded.
// upstream: crates/diff/src/lib.rs:188
func myersDiffWithin(oldIDs, newIDs []uint32, maxEdits int) ([]DiffRun, bool) {
	oldLen := len(oldIDs)
	newLen := len(newIDs)
	maxEdit := min(maxEdits, oldLen+newLen)
	offset := maxEdit + 1
	best := make([]pathState, 2*maxEdit+3)
	capHint := oldLen + newLen
	if capHint < 16 {
		capHint = 16
	} else if capHint > 4096 {
		capHint = 4096
	}
	arena := newComponentArena(capHint)

	seed := pathState{oldPos: -1, last: noComponent, valid: true}
	seedNewPos := extractCommon(&arena, &seed, newIDs, oldIDs, 0)
	if seed.oldPos+1 >= oldLen && seedNewPos+1 >= newLen {
		return buildDiffRuns(&arena, seed.last), true
	}
	best[offset] = seed

	minDiag := math.MinInt
	maxDiag := math.MaxInt
	for editLength := 1; editLength <= maxEdit; editLength++ {
		diagStart := -editLength
		if minDiag > diagStart {
			diagStart = minDiag
		}
		diagEnd := editLength
		if maxDiag < diagEnd {
			diagEnd = maxDiag
		}
		for diagonal := diagStart; diagonal <= diagEnd; diagonal += 2 {
			idx := diagonal + offset
			removePath := best[idx-1]
			best[idx-1] = pathState{}
			addPath := best[idx+1]

			canAdd := false
			if addPath.valid {
				addNewPos := addPath.oldPos - diagonal
				canAdd = addNewPos >= 0 && addNewPos < newLen
			}
			canRemove := removePath.valid && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				if removePath.valid {
					arena.release(removePath.last)
				}
				if best[idx].valid {
					arena.release(best[idx].last)
					best[idx] = pathState{}
				}
				continue
			}

			var basePath pathState
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				if removePath.valid {
					arena.release(removePath.last)
				}
				cloned := pathState{oldPos: addPath.oldPos, last: arena.cloneRef(addPath.last), valid: true}
				basePath = addToPath(&arena, cloned, true, false, 0)
			} else {
				basePath = addToPath(&arena, removePath, false, true, 1)
			}

			newPos := extractCommon(&arena, &basePath, newIDs, oldIDs, diagonal)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return buildDiffRuns(&arena, basePath.last), true
			}
			if basePath.oldPos+1 >= oldLen {
				if diagonal-1 < maxDiag {
					maxDiag = diagonal - 1
				}
			}
			if newPos+1 >= newLen {
				if diagonal+1 > minDiag {
					minDiag = diagonal + 1
				}
			}
			if best[idx].valid {
				arena.release(best[idx].last)
			}
			best[idx] = basePath
		}
	}
	return nil, false
}

// lineTokensStr splits text into LF-inclusive line tokens matching jsdiff/omp_diff.
// upstream: crates/diff/src/lib.rs:338
func lineTokensStr(text string) []string {
	if text == "" {
		return nil
	}
	var tokens []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			tokens = append(tokens, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		tokens = append(tokens, text[start:])
	}
	return tokens
}

// lineRunsStr computes Myers runs over LF-inclusive line tokens.
// upstream: crates/diff/src/lib.rs:362
func lineRunsStr(oldText, newText string) []DiffRun {
	oldToks := lineTokensStr(oldText)
	newToks := lineTokensStr(newText)
	oldIDs, newIDs := internStrings(oldToks, newToks)
	return myersDiff(oldIDs, newIDs)
}

// DiffOutput is the rendered numbered diff plus 1-indexed first changed line.
// upstream: crates/edit/src/diff_string.rs:42
type DiffOutput struct {
	Diff             string
	FirstChangedLine uint32 // 0 = none
}

type diffPrefix int

const (
	diffPrefixAdded diffPrefix = iota
	diffPrefixRemoved
	diffPrefixContext
)

type parsedNumberedDiffRow struct {
	prefix     diffPrefix
	lineNumber uint32
}

func formatNumberedDiffLine(prefix byte, lineNumber uint32, content string) string {
	return fmt.Sprintf("%c%d|%s", prefix, lineNumber, content)
}

func parseNumberedDiffRow(row string) (parsedNumberedDiffRow, bool) {
	if len(row) == 0 {
		return parsedNumberedDiffRow{}, false
	}
	var p diffPrefix
	switch row[0] {
	case '+':
		p = diffPrefixAdded
	case '-':
		p = diffPrefixRemoved
	case ' ':
		p = diffPrefixContext
	default:
		return parsedNumberedDiffRow{}, false
	}
	body := row[1:]
	sep := strings.IndexByte(body, '|')
	if sep < 0 {
		return parsedNumberedDiffRow{}, false
	}
	num, err := strconv.ParseUint(body[:sep], 10, 32)
	if err != nil {
		return parsedNumberedDiffRow{}, false
	}
	return parsedNumberedDiffRow{prefix: p, lineNumber: uint32(num)}, true
}

func isDiffChangeRow(row string) bool {
	return strings.HasPrefix(row, "+") || strings.HasPrefix(row, "-")
}

func parseSourceRowLineNumber(row string) (uint32, bool) {
	p, ok := parseNumberedDiffRow(row)
	if !ok || p.prefix == diffPrefixAdded {
		return 0, false
	}
	return p.lineNumber, true
}

func normalizeDiffGapRows(rows []string) []string {
	writeIdx := 0
	for readIdx := 0; readIdx < len(rows); readIdx++ {
		if rows[readIdx] != "" {
			rows[writeIdx] = rows[readIdx]
			writeIdx++
			continue
		}
		if writeIdx == 0 || rows[writeIdx-1] == "" {
			continue
		}
		var before uint32
		hasBefore := false
		for i := writeIdx - 1; i >= 0; i-- {
			if n, ok := parseSourceRowLineNumber(rows[i]); ok {
				before = n
				hasBefore = true
				break
			}
		}
		var after uint32
		hasAfter := false
		for i := readIdx + 1; i < len(rows); i++ {
			if rows[i] == "" {
				continue
			}
			if n, ok := parseSourceRowLineNumber(rows[i]); ok {
				after = n
				hasAfter = true
				break
			}
		}
		if hasBefore && hasAfter && after > before+1 {
			rows[writeIdx] = ""
			writeIdx++
		}
	}
	return rows[:writeIdx]
}

func adjustedContextInsertIndex(rows []string, index int) int {
	start := index
	for start > 0 && isDiffChangeRow(rows[start-1]) {
		start--
	}
	end := index
	for end < len(rows) && isDiffChangeRow(rows[end]) {
		end++
	}
	if index > start && index < end {
		return end
	}
	return index
}

type numberedTextLine struct {
	line uint32
	text string
}

func insertBracketContextRows(rows []string, contextLines []numberedTextLine) []string {
	for _, item := range contextLines {
		row := formatNumberedDiffLine(' ', item.line, item.text)
		if slices.Contains(rows, row) {
			continue
		}
		insertIndex := len(rows)
		var prevSource, nextSource uint32
		hasPrev, hasNext := false, false
		for idx, cand := range rows {
			p, ok := parseNumberedDiffRow(cand)
			if !ok || p.prefix == diffPrefixAdded {
				continue
			}
			if p.lineNumber < item.line {
				prevSource = p.lineNumber
				hasPrev = true
				continue
			}
			nextSource = p.lineNumber
			hasNext = true
			insertIndex = idx
			break
		}
		var chunk []string
		if hasPrev && item.line > prevSource+1 {
			chunk = append(chunk, "")
		}
		chunk = append(chunk, row)
		if hasNext && nextSource > item.line+1 {
			chunk = append(chunk, "")
		}
		insertIndex = adjustedContextInsertIndex(rows, insertIndex)
		rows = slices.Insert(rows, insertIndex, chunk...)
	}
	return rows
}

func addMatchingBracketContextRows(rows []string, oldText, newText string, streaming bool, path string) []string {
	if !streaming {
		oldLines := strings.Split(oldText, "\n")
		newLines := strings.Split(newText, "\n")
		oldVis := make(map[int]bool)
		newVis := make(map[int]bool)
		type changeEntry struct {
			newPos int64
			delta  int64
		}
		var changes []changeEntry
		var offset int64
		for _, row := range rows {
			p, ok := parseNumberedDiffRow(row)
			if !ok {
				continue
			}
			switch p.prefix {
			case diffPrefixRemoved:
				oldVis[int(p.lineNumber)] = true
				changes = append(changes, changeEntry{int64(p.lineNumber) + offset, -1})
				offset--
			case diffPrefixAdded:
				newVis[int(p.lineNumber)] = true
				changes = append(changes, changeEntry{int64(p.lineNumber), 1})
				offset++
			case diffPrefixContext:
				oldVis[int(p.lineNumber)] = true
				shifted := int64(p.lineNumber) + offset
				if shifted >= 1 {
					newVis[int(shifted)] = true
				}
			}
		}
		toOldLine := func(newLine uint32) (uint32, bool) {
			nl := int64(newLine)
			var shift int64
			for _, ch := range changes {
				if ch.newPos <= nl {
					shift += ch.delta
				}
			}
			v := nl - shift
			if v < 1 {
				return 0, false
			}
			return uint32(v), true
		}
		ctxMap := make(map[uint32]string)
		for ln := range FindBlockContextLinesForPath(path, oldLines, oldVis) {
			if ln >= 1 && ln <= len(oldLines) {
				ctxMap[uint32(ln)] = oldLines[ln-1]
			}
		}
		for ln := range FindBlockContextLinesForPath(path, newLines, newVis) {
			if ln >= 1 && ln <= len(newLines) {
				if oldLn, ok := toOldLine(uint32(ln)); ok {
					if _, exists := ctxMap[oldLn]; !exists {
						ctxMap[oldLn] = newLines[ln-1]
					}
				}
			}
		}
		if len(ctxMap) > 0 {
			ordered := make([]numberedTextLine, 0, len(ctxMap))
			for ln, txt := range ctxMap {
				ordered = append(ordered, numberedTextLine{line: ln, text: txt})
			}
			slices.SortFunc(ordered, func(a, b numberedTextLine) int {
				if a.line < b.line {
					return -1
				}
				if a.line > b.line {
					return 1
				}
				return 0
			})
			rows = insertBracketContextRows(rows, ordered)
		}
	}
	return normalizeDiffGapRows(rows)
}

// GenerateDiffString computes omp's numbered diff (+N|text, -N|text,  N|text)
// with 2 lines of context and enclosing bracket context rows.
// upstream: crates/edit/src/diff_string.rs:265
func GenerateDiffString(oldText, newText string, contextLines int, streaming bool) DiffOutput {
	return GenerateDiffStringForPath(oldText, newText, contextLines, streaming, "")
}

// GenerateDiffStringForPath computes omp's numbered diff with enclosing-block context
// rows aware of path's language (Python/YAML/Markdown indentation/heading boundaries + brackets).
// upstream: crates/edit/src/diff_string.rs:265
func GenerateDiffStringForPath(oldText, newText string, contextLines int, streaming bool, path string) DiffOutput {
	if contextLines <= 0 {
		contextLines = 2
	}
	oldTokens := lineTokensStr(oldText)
	newTokens := lineTokensStr(newText)
	oldIDs, newIDs := internStrings(oldTokens, newTokens)
	runs := myersDiff(oldIDs, newIDs)

	oldRows := make([]string, len(oldTokens))
	for i, t := range oldTokens {
		oldRows[i] = strings.TrimSuffix(t, "\n")
	}
	newRows := make([]string, len(newTokens))
	for i, t := range newTokens {
		newRows[i] = strings.TrimSuffix(t, "\n")
	}

	var output []string
	var oldLineNum uint32 = 1
	var newLineNum uint32 = 1
	lastWasChange := false
	var firstChangedLine uint32
	oldPos, newPos := 0, 0

	for idx, run := range runs {
		count := int(run.Count)
		var raw []string
		if run.Removed {
			raw = oldRows[oldPos : oldPos+count]
			oldPos += count
		} else {
			raw = newRows[newPos : newPos+count]
			newPos += count
			if !run.Added {
				oldPos += count
			}
		}
		if run.Added || run.Removed {
			if firstChangedLine == 0 {
				firstChangedLine = newLineNum
			}
			for _, line := range raw {
				if run.Added {
					output = append(output, formatNumberedDiffLine('+', newLineNum, line))
					newLineNum++
				} else {
					output = append(output, formatNumberedDiffLine('-', oldLineNum, line))
					oldLineNum++
				}
			}
			lastWasChange = true
		} else {
			nextIsChange := idx+1 < len(runs) && (runs[idx+1].Added || runs[idx+1].Removed)
			if lastWasChange || nextIsChange {
				leadingSkip := 0
				middleSkip := 0
				trailingSkip := 0
				var linesToShow []string
				if lastWasChange && nextIsChange {
					if len(raw) > contextLines*2 {
						middleSkip = len(raw) - contextLines*2
						linesToShow = make([]string, 0, contextLines*2)
						linesToShow = append(linesToShow, raw[:contextLines]...)
						linesToShow = append(linesToShow, raw[len(raw)-contextLines:]...)
					} else {
						linesToShow = raw
					}
				} else if nextIsChange {
					if len(raw) > contextLines {
						leadingSkip = len(raw) - contextLines
					}
					linesToShow = raw[leadingSkip:]
				} else {
					if len(raw) > contextLines {
						trailingSkip = len(raw) - contextLines
						linesToShow = raw[:contextLines]
					} else {
						linesToShow = raw
					}
				}
				oldLineNum += uint32(leadingSkip)
				newLineNum += uint32(leadingSkip)
				firstChunkLen := len(linesToShow)
				if middleSkip > 0 {
					firstChunkLen = contextLines
				}
				for _, line := range linesToShow[:firstChunkLen] {
					output = append(output, formatNumberedDiffLine(' ', oldLineNum, line))
					oldLineNum++
					newLineNum++
				}
				if middleSkip > 0 {
					oldLineNum += uint32(middleSkip)
					newLineNum += uint32(middleSkip)
					for _, line := range linesToShow[firstChunkLen:] {
						output = append(output, formatNumberedDiffLine(' ', oldLineNum, line))
						oldLineNum++
						newLineNum++
					}
				}
				oldLineNum += uint32(trailingSkip)
				newLineNum += uint32(trailingSkip)
			} else {
				oldLineNum += uint32(len(raw))
				newLineNum += uint32(len(raw))
			}
			lastWasChange = false
		}
	}

	output = addMatchingBracketContextRows(output, oldText, newText, streaming, path)
	return DiffOutput{Diff: strings.Join(output, "\n"), FirstChangedLine: firstChangedLine}
}

const (
	defaultAddedRunContextLines = 2
	// ResponseAddedRunContextLines matches crates/edit/src/session.rs:134.
	ResponseAddedRunContextLines = 20
	previewElisionMarker         = "…"
)

// CompactDiffPreview is the current-file numbered preview built from a numbered diff.
// upstream: crates/edit/src/diff_string.rs:638
type CompactDiffPreview struct {
	Preview      string
	AddedLines   int
	RemovedLines int
}

func appendPreviewLine(output *[]string, line string) {
	normalized := line
	if line == "" || line == "..." || line == previewElisionMarker || line == "+…" {
		normalized = previewElisionMarker
	}
	if normalized == previewElisionMarker && (len(*output) == 0 || (*output)[len(*output)-1] == previewElisionMarker) {
		return
	}
	*output = append(*output, normalized)
}

func appendAddedRun(output *[]string, run []string, edgeLines int) {
	if len(run) == 0 {
		return
	}
	threshold := edgeLines*2 + 1
	if len(run) <= threshold {
		for _, line := range run {
			appendPreviewLine(output, line)
		}
		return
	}
	for _, line := range run[:edgeLines] {
		appendPreviewLine(output, line)
	}
	appendPreviewLine(output, previewElisionMarker)
	for _, line := range run[len(run)-edgeLines:] {
		appendPreviewLine(output, line)
	}
}

func parseIntegerPrefix(value string) (int64, bool) {
	value = strings.TrimLeft(value, " \t")
	neg := false
	if strings.HasPrefix(value, "-") {
		neg = true
		value = value[1:]
	} else if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	digits := 0
	for digits < len(value) && value[digits] >= '0' && value[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(value[:digits], 10, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

// BuildCompactDiffPreview builds a compact current-file preview from numbered diff rows.
// upstream: crates/edit/src/diff_string.rs:733
func BuildCompactDiffPreview(diff string, maxAddedRunContext int) CompactDiffPreview {
	if diff == "" {
		return CompactDiffPreview{}
	}
	if maxAddedRunContext < 1 {
		maxAddedRunContext = defaultAddedRunContextLines
	}
	lines := strings.Split(diff, "\n")
	addedLines := 0
	removedLines := 0
	var formatted []string
	var addedRun []string

	for _, line := range lines {
		var kind diffPrefix
		var body string
		ok := false
		if len(line) > 0 {
			switch line[0] {
			case '+':
				kind, body, ok = diffPrefixAdded, line[1:], true
			case '-':
				kind, body, ok = diffPrefixRemoved, line[1:], true
			case ' ':
				kind, body, ok = diffPrefixContext, line[1:], true
			}
		}
		var lineNumber int64
		var content string
		if ok {
			sep := strings.IndexByte(body, '|')
			if sep >= 0 {
				if n, parsed := parseIntegerPrefix(body[:sep]); parsed {
					lineNumber = n
					content = body[sep+1:]
				} else {
					ok = false
				}
			} else {
				ok = false
			}
		}
		if !ok {
			appendAddedRun(&formatted, addedRun, maxAddedRunContext)
			addedRun = addedRun[:0]
			appendPreviewLine(&formatted, line)
			continue
		}
		switch kind {
		case diffPrefixAdded:
			addedLines++
			addedRun = append(addedRun, fmt.Sprintf("%d:%s", lineNumber, content))
		case diffPrefixRemoved:
			appendAddedRun(&formatted, addedRun, maxAddedRunContext)
			addedRun = addedRun[:0]
			removedLines++
		case diffPrefixContext:
			appendAddedRun(&formatted, addedRun, maxAddedRunContext)
			addedRun = addedRun[:0]
			newLineNum := lineNumber + int64(addedLines) - int64(removedLines)
			appendPreviewLine(&formatted, fmt.Sprintf("%d:%s", newLineNum, content))
		}
	}
	appendAddedRun(&formatted, addedRun, maxAddedRunContext)
	for len(formatted) > 0 && formatted[len(formatted)-1] == previewElisionMarker {
		formatted = formatted[:len(formatted)-1]
	}
	return CompactDiffPreview{
		Preview:      strings.Join(formatted, "\n"),
		AddedLines:   addedLines,
		RemovedLines: removedLines,
	}
}
