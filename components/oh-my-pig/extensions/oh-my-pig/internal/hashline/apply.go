package hashline

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Edit application, boundary-echo checks, and line rendering.
// Body rows are applied verbatim; boundary/echo checks never silently rewrite a patch.
// upstream: crates/edit/src/modes/hashline/apply.rs

var (
	// structuralCloserRe matches a line containing only closing delimiters and optional separator.
	// upstream: crates/edit/src/modes/hashline/apply.rs:36
	structuralCloserRe = regexp.MustCompile(`^\s*[)\]}]+[;,]?\s*$`)
)

// ApplyOptions configures ApplyEdits.
// upstream: crates/edit/src/modes/hashline/apply.rs:58
type ApplyOptions struct {
	Clipboard    *Clipboard
	Path         string
	OnEmptyPaste EmptyPaste
}

func editDirectAnchors(edit Edit) []Anchor {
	switch edit.Kind {
	case EditDelete:
		return []Anchor{edit.Anchor}
	case EditInsert:
		if edit.Cursor.Kind == CursorBeforeAnchor || edit.Cursor.Kind == CursorAfterAnchor {
			return []Anchor{edit.Cursor.Anchor}
		}
	}
	return nil
}

func phantomLine(lines []string) (uint32, bool) {
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return uint32(len(lines)), true
	}
	return 0, false
}

func outOfRangeError(line uint32, count int) *EditError {
	return RejectedError(
		RejectionOutOfRange,
		fmt.Sprintf("Line %d does not exist (file has %d lines)", line, count),
	)
}

func validateBounds(edits []Edit, lines []string) *EditError {
	for _, edit := range edits {
		for _, anchor := range editDirectAnchors(edit) {
			if anchor.Line < 1 || int(anchor.Line) > len(lines) {
				return outOfRangeError(anchor.Line, len(lines))
			}
		}
	}
	return nil
}

// replacementGroup represents one PUT N.=M: as lowered by the parser.
// upstream: crates/edit/src/modes/hashline/apply.rs:116
type replacementGroup struct {
	insertIndices []int
	deleteIndices []int
	payload       []string
	start         uint32
	end           uint32
}

func findReplacementGroup(edits []Edit, start int) (replacementGroup, bool) {
	if start >= len(edits) {
		return replacementGroup{}, false
	}
	first := edits[start]
	if first.Kind != EditInsert || first.Cursor.Kind != CursorBeforeAnchor || !first.Replacement {
		return replacementGroup{}, false
	}
	anchorLine := first.Cursor.Anchor.Line
	opLine := first.LineNum
	var insertIndices []int
	var payload []string
	i := start
	for i < len(edits) {
		e := edits[i]
		if e.Kind != EditInsert || e.Cursor.Kind != CursorBeforeAnchor || !e.Replacement {
			break
		}
		if e.Cursor.Anchor.Line != anchorLine || e.LineNum != opLine {
			break
		}
		insertIndices = append(insertIndices, i)
		payload = append(payload, e.Text)
		i++
	}
	var deleteIndices []int
	expected := anchorLine
	for i < len(edits) {
		e := edits[i]
		if e.Kind != EditDelete || e.Anchor.Line != expected || e.LineNum != opLine {
			break
		}
		deleteIndices = append(deleteIndices, i)
		expected++
		i++
	}
	if len(deleteIndices) == 0 {
		return replacementGroup{}, false
	}
	return replacementGroup{
		insertIndices: insertIndices,
		deleteIndices: deleteIndices,
		payload:       payload,
		start:         anchorLine,
		end:           expected - 1,
	}, true
}

func hasContent(text string) bool {
	for i := range len(text) {
		b := text[i]
		if !((b >= 9 && b <= 13) || b == 32) {
			return true
		}
	}
	return false
}

func sourceDeleted(edits []Edit, line uint32) bool {
	for _, edit := range edits {
		if edit.Kind == EditDelete && edit.Anchor.Line == line {
			return true
		}
	}
	return false
}

func duplicateLeading(group replacementGroup, edits []Edit, lines []string) int {
	if group.start > 1 && sourceDeleted(edits, group.start-1) {
		return 0
	}
	return restatedAbove(group.payload, group.start, lines)
}

func duplicateTrailing(group replacementGroup, edits []Edit, lines []string) int {
	if sourceDeleted(edits, group.end+1) {
		return 0
	}
	return restatedBelow(group.payload, group.end, lines)
}

func restatedAbove(rows []string, start uint32, lines []string) int {
	maxCount := min(len(rows), int(start-1))
	for count := maxCount; count >= 1; count-- {
		sourceStart := int(start) - 1 - count
		cand := rows[:count]
		match := true
		anyContent := false
		for i := range count {
			if cand[i] != lines[sourceStart+i] {
				match = false
				break
			}
			if hasContent(cand[i]) {
				anyContent = true
			}
		}
		if match && anyContent {
			return count
		}
	}
	return 0
}

func restatedBelow(rows []string, end uint32, lines []string) int {
	avail := len(lines) - int(end)
	if avail <= 0 {
		return 0
	}
	maxCount := min(len(rows), avail)
	for count := maxCount; count >= 1; count-- {
		payload := rows[len(rows)-count:]
		source := lines[int(end) : int(end)+count]
		match := true
		anyContent := false
		for i := range count {
			if payload[i] != source[i] {
				match = false
				break
			}
			if hasContent(payload[i]) {
				anyContent = true
			}
		}
		if match && anyContent {
			return count
		}
	}
	return 0
}

func restatedEdges(rows []string, lines []string, start, end uint32) (int, int) {
	landsOn := func(run []string, at int) bool {
		if at < 0 {
			return false
		}
		for i, row := range run {
			if at+i >= len(lines) {
				break
			}
			if row != lines[at+i] {
				return false
			}
		}
		return true
	}
	leading := restatedAbove(rows, start, lines)
	trailing := restatedBelow(rows, end, lines)
	if leading > 0 && landsOn(rows[:leading], int(start-1)) {
		leading = 0
	}
	if trailing > 0 {
		at := int(end) - trailing
		if at < 0 {
			at = 0
		}
		if landsOn(rows[len(rows)-trailing:], at) {
			trailing = 0
		}
	}
	return leading, trailing
}

// annotationEcho detects single-line attribute/decorator echoes without tree-sitter.
// upstream: crates/edit/src/modes/hashline/apply.rs:273
func annotationEcho(lines []string, path string, first, last uint32) bool {
	if path == "" || first < 1 || int(last) > len(lines) {
		return false
	}
	lang := detectLang(path)
	for ln := first; ; ln++ {
		t := strings.TrimSpace(lines[ln-1])
		switch lang {
		case langRust:
			if !((strings.HasPrefix(t, "#[") || strings.HasPrefix(t, "#![")) && strings.HasSuffix(t, "]")) {
				return false
			}
		case langPython, langJS, langJSX, langC, langCSS:
			if !strings.HasPrefix(t, "@") {
				return false
			}
		default:
			return false
		}
		if ln == last {
			break
		}
	}
	return true
}

type echoedKind int

const (
	echoedLeading echoedKind = iota
	echoedTrailing
	echoedBoth
)

type echoAmbiguity struct {
	start    uint32
	end      uint32
	kind     echoedKind
	above    uint32
	below    uint32
	leftOut  [2]uint32
	repeated bool
}

func shortEcho(group replacementGroup, dropJ, dropK int) (echoAmbiguity, bool) {
	rangeLen := len(group.deleteIndices)
	between := len(group.payload) - (dropJ + dropK)
	hasBetween := len(group.payload) >= dropJ+dropK
	oneSidedShort := (dropJ == 0) != (dropK == 0) && (!hasBetween || between < rangeLen)
	twoSidedShort := dropJ > 0 && dropK > 0 && rangeLen > 1 && hasBetween && between < rangeLen
	if !oneSidedShort && !twoSidedShort {
		return echoAmbiguity{}, false
	}
	var kept uint32
	if hasBetween {
		kept = uint32(between)
	}
	var amb echoAmbiguity
	amb.start = group.start
	amb.end = group.end
	switch {
	case twoSidedShort:
		amb.kind = echoedBoth
		amb.above = uint32(dropJ)
		amb.below = uint32(dropK)
		amb.leftOut = [2]uint32{group.start + kept, group.end}
	case dropJ > 0:
		amb.kind = echoedLeading
		amb.above = uint32(dropJ)
		amb.leftOut = [2]uint32{group.start + kept, group.end}
	default:
		amb.kind = echoedTrailing
		amb.below = uint32(dropK)
		amb.leftOut = [2]uint32{group.start, group.end - kept}
	}
	return amb, true
}

func (a echoAmbiguity) refusal() *EditError {
	switch a.kind {
	case echoedBoth:
		return RejectedError(RejectionAmbiguousBoundary, TwoSidedBoundaryEchoMessage(a.start, a.end, a.above, a.below, a.repeated, a.leftOut[0], a.leftOut[1]))
	case echoedLeading:
		if a.repeated {
			return RejectedError(RejectionAmbiguousBoundary, RepeatedBoundaryEchoMessage(a.start, a.end, BoundaryLeading, a.above, a.leftOut[0], a.leftOut[1]))
		}
		return RejectedError(RejectionAmbiguousBoundary, AmbiguousBoundaryEchoMessage(a.start, a.end, BoundaryLeading, a.above, a.leftOut[0], a.leftOut[1]))
	default:
		if a.repeated {
			return RejectedError(RejectionAmbiguousBoundary, RepeatedBoundaryEchoMessage(a.start, a.end, BoundaryTrailing, a.below, a.leftOut[0], a.leftOut[1]))
		}
		return RejectedError(RejectionAmbiguousBoundary, AmbiguousBoundaryEchoMessage(a.start, a.end, BoundaryTrailing, a.below, a.leftOut[0], a.leftOut[1]))
	}
}

func dropIsNoop(group replacementGroup, lines []string, dropL, dropT int) bool {
	kept := group.payload[dropL : len(group.payload)-dropT]
	rng := lines[group.start-1 : group.end]
	if len(kept) != len(rng) {
		return false
	}
	for i := range kept {
		if kept[i] != rng[i] {
			return false
		}
	}
	return true
}

// classifyEchoes matches crates/edit/src/modes/hashline/apply.rs:396.
func classifyEchoes(edits []Edit, lines []string, path string, authoredParses bool) ([]echoAmbiguity, []uint32) {
	var ambiguities []echoAmbiguity
	var accepted []uint32
	i := 0
	for i < len(edits) {
		group, ok := findReplacementGroup(edits, i)
		if !ok {
			i++
			continue
		}
		i = group.deleteIndices[len(group.deleteIndices)-1] + 1
		leading := duplicateLeading(group, edits, lines)
		trailing := duplicateTrailing(group, edits, lines)
		rangeLen := len(group.deleteIndices)
		dropL, dropT := 0, 0
		if leading > 0 && trailing > 0 {
			if len(group.payload) >= leading+trailing && len(group.payload)-(leading+trailing) == rangeLen {
				dropL = leading
				dropT = trailing
			} else if amb, ok := shortEcho(group, leading, trailing); ok {
				ambiguities = append(ambiguities, amb)
			}
		} else if leading > 0 && (rangeLen > 1 || annotationEcho(lines, path, group.start-uint32(leading), group.start-1)) {
			if len(group.payload)-leading >= rangeLen {
				dropL = leading
			} else if amb, ok := shortEcho(group, leading, 0); ok {
				ambiguities = append(ambiguities, amb)
			}
		} else if trailing > 0 && (rangeLen > 1 || annotationEcho(lines, path, group.end+1, group.end+uint32(trailing))) {
			if len(group.payload)-trailing >= rangeLen {
				dropT = trailing
			} else if amb, ok := shortEcho(group, 0, trailing); ok {
				ambiguities = append(ambiguities, amb)
			}
		}
		if dropL == 0 && dropT == 0 {
			continue
		}
		if dropIsNoop(group, lines, dropL, dropT) {
			accepted = append(accepted, group.start)
			continue
		}
		_ = authoredParses
		// Check for repeated echo exposed after setting one copy aside.
		rest := group
		rest.payload = slices.Clone(group.payload[dropL : len(group.payload)-dropT])
		j, k := restatedEdges(rest.payload, lines, group.start, group.end)
		if group.start > 1 && sourceDeleted(edits, group.start-1) {
			j = 0
		}
		if sourceDeleted(edits, group.end+1) {
			k = 0
		}
		if rangeLen > 1 {
			if amb, ok := shortEcho(rest, j, k); ok {
				amb.repeated = true
				ambiguities = append(ambiguities, amb)
			}
		}
	}
	return ambiguities, accepted
}

// echoOutcome matches crates/edit/src/modes/hashline/apply.rs:1344.
func echoOutcome(edits []Edit, lines []string, path string, literal []uint32) *EditError {
	i := 0
	for i < len(edits) {
		group, ok := findReplacementGroup(edits, i)
		if !ok {
			i++
			continue
		}
		i = group.deleteIndices[len(group.deleteIndices)-1] + 1
		if slices.Contains(literal, group.start) {
			continue
		}
		rangeLen := len(group.deleteIndices)
		leading := duplicateLeading(group, edits, lines)
		trailing := duplicateTrailing(group, edits, lines)
		var isEcho bool
		if leading > 0 && trailing > 0 {
			isEcho = rangeLen > 1 || (len(group.payload) >= leading+trailing && len(group.payload)-(leading+trailing) == rangeLen)
		} else if leading > 0 {
			isEcho = rangeLen > 1 || annotationEcho(lines, path, group.start-uint32(leading), group.start-1)
		} else if trailing > 0 {
			isEcho = rangeLen > 1 || annotationEcho(lines, path, group.end+1, group.end+uint32(trailing))
		}
		if !isEcho {
			continue
		}
		above, below := restatedEdges(group.payload, lines, group.start, group.end)
		if leading == 0 {
			above = 0
		}
		if trailing == 0 {
			below = 0
		}
		supplied := len(group.payload) - (above + below)
		if supplied < 0 {
			supplied = 0
		}
		if above == 0 && below == 0 && supplied >= rangeLen {
			continue
		}
		var aboveSpan, belowSpan, leftOutSpan *[2]uint32
		if above > 0 {
			s := [2]uint32{group.start - uint32(above), group.start - 1}
			aboveSpan = &s
		}
		if below > 0 {
			s := [2]uint32{group.end + 1, group.end + uint32(below)}
			belowSpan = &s
		}
		if supplied < rangeLen {
			s := [2]uint32{group.start + uint32(supplied), group.end}
			leftOutSpan = &s
		}
		return RejectedError(
			RejectionAmbiguousBoundary,
			BoundaryEchoOutcomeMessage(group.start, group.end, aboveSpan, belowSpan, leftOutSpan),
		)
	}
	return nil
}

type renderSlot int

const (
	slotBefore renderSlot = iota
	slotReplacement
	slotAfter
	slotDelete
)

type anchoredEntry struct {
	line uint32
	slot renderSlot
	text string
}

// renderEdits applies concrete edits to original lines in one ascending pass.
// upstream: crates/edit/src/modes/hashline/apply.rs:1130
func renderEdits(original []string, edits []Edit) (string, uint32) {
	var first uint32
	var bof []string
	var eof []string
	var anchored []anchoredEntry

	for _, edit := range edits {
		switch edit.Kind {
		case EditInsert:
			switch edit.Cursor.Kind {
			case CursorBof:
				bof = append(bof, edit.Text)
			case CursorEof:
				eof = append(eof, edit.Text)
			case CursorAfterAnchor:
				anchored = append(anchored, anchoredEntry{line: edit.Cursor.Anchor.Line, slot: slotAfter, text: edit.Text})
			case CursorBeforeAnchor:
				slot := slotBefore
				if edit.Replacement {
					slot = slotReplacement
				}
				anchored = append(anchored, anchoredEntry{line: edit.Cursor.Anchor.Line, slot: slot, text: edit.Text})
			}
		case EditDelete:
			anchored = append(anchored, anchoredEntry{line: edit.Anchor.Line, slot: slotDelete})
		}
	}

	// Stable sort by line number so edits on one line preserve op order.
	slices.SortStableFunc(anchored, func(a, b anchoredEntry) int {
		if a.line < b.line {
			return -1
		}
		if a.line > b.line {
			return 1
		}
		return 0
	})

	lines := make([]string, 0, len(original)+len(anchored)+len(bof)+len(eof))
	copied := 0
	i := 0
	for i < len(anchored) {
		line := anchored[i].line
		j := i + 1
		for j < len(anchored) && anchored[j].line == line {
			j++
		}
		group := anchored[i:j]
		i = j

		idx := int(line - 1)
		end := min(idx, len(original))
		if copied < end {
			lines = append(lines, original[copied:end]...)
		}
		for _, entry := range group {
			if entry.slot == slotBefore {
				lines = append(lines, entry.text)
			}
		}
		for _, entry := range group {
			if entry.slot == slotReplacement {
				lines = append(lines, entry.text)
			}
		}
		deleted := false
		for _, entry := range group {
			if entry.slot == slotDelete {
				deleted = true
				break
			}
		}
		if !deleted {
			if idx < len(original) {
				lines = append(lines, original[idx])
			} else {
				lines = append(lines, "")
			}
		}
		for _, entry := range group {
			if entry.slot == slotAfter {
				lines = append(lines, entry.text)
			}
		}
		copied = idx + 1
		if first == 0 {
			first = line
		}
	}
	if copied < len(original) {
		lines = append(lines, original[copied:]...)
	}

	if len(bof) > 0 {
		if len(lines) == 1 && lines[0] == "" {
			lines = bof
		} else {
			lines = slices.Insert(lines, 0, bof...)
		}
		first = 1
	}

	if len(eof) > 0 {
		var at uint32
		if len(lines) == 1 && lines[0] == "" {
			lines = eof
			at = 1
		} else {
			idx := len(lines)
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				idx = len(lines) - 1
			}
			lines = slices.Insert(lines, idx, eof...)
			at = uint32(idx + 1)
		}
		if first == 0 || at < first {
			first = at
		}
	}

	return strings.Join(lines, "\n"), first
}

// ApplyEdits applies parsed edits to LF-normalized text.
// upstream: crates/edit/src/modes/hashline/apply.rs:1245
func ApplyEdits(text string, edits []Edit, options ApplyOptions) (ApplyResult, *EditError) {
	if len(edits) == 0 {
		return ApplyResult{Text: text}, nil
	}
	lines := strings.Split(text, "\n")
	clipboard := options.Clipboard
	if clipboard == nil {
		clipboard = &Clipboard{}
	}
	var clipboardWarnings []string
	concrete, err := ResolveClipboardEdits(edits, lines, clipboard, options.OnEmptyPaste, func(w string) {
		clipboardWarnings = append(clipboardWarnings, w)
	})
	if err != nil {
		return ApplyResult{}, err
	}
	target := make([]Edit, 0, len(concrete))
	for _, edit := range concrete {
		switch edit.Kind {
		case EditBlock:
			return ApplyResult{}, ApplyError(UnresolvedBlockInternal)
		case EditCut, EditPaste:
			return ApplyResult{}, ApplyError(UnresolvedClipboardInternal)
		default:
			target = append(target, edit)
		}
	}
	if phantom, hasPhantom := phantomLine(lines); hasPhantom {
		for _, edit := range target {
			if edit.Kind == EditInsert && edit.Cursor.Kind == CursorBeforeAnchor && edit.Replacement && edit.Cursor.Anchor.Line == phantom {
				return ApplyResult{}, outOfRangeError(phantom, len(lines)-1)
			}
		}
		kept := target[:0]
		for _, edit := range target {
			if edit.Kind == EditDelete && edit.Anchor.Line == phantom {
				continue
			}
			kept = append(kept, edit)
		}
		target = kept
	}
	for i := range target {
		target[i].Index = uint32(i)
	}
	if err := validateBounds(target, lines); err != nil {
		return ApplyResult{}, err
	}
	result, firstChangedLine := renderEdits(lines, target)
	authoredParses := parsesCleanly(options.Path, result)
	ambiguities, literal := classifyEchoes(target, lines, options.Path, authoredParses)
	if len(ambiguities) > 0 {
		return ApplyResult{}, ambiguities[0].refusal()
	}
	if err := echoOutcome(target, lines, options.Path, literal); err != nil {
		return ApplyResult{}, err
	}
	_ = hasSyntaxOracle(options.Path)
	return ApplyResult{
		Text:             result,
		FirstChangedLine: firstChangedLine,
		Warnings:         clipboardWarnings,
	}, nil
}
