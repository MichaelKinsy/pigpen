package hashline

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Stale-tag anchor recovery.
// upstream: crates/edit/src/modes/hashline/recovery.rs

// RecoveryArgs holds inputs for stale-snapshot recovery.
// upstream: crates/edit/src/modes/hashline/recovery.rs:26
type RecoveryArgs struct {
	Path        string
	CurrentText string
	FileHash    string
	Edits       []Edit
	Clipboard   *Clipboard
}

// RecoveryResult holds a successfully recovered application result.
// upstream: crates/edit/src/modes/hashline/recovery.rs:41
type RecoveryResult struct {
	Text             string
	FirstChangedLine uint32
	Warnings         []string
}

func editAnchorsForRecovery(edit Edit) []Anchor {
	switch edit.Kind {
	case EditDelete, EditBlock:
		return []Anchor{edit.Anchor}
	case EditCut:
		out := make([]Anchor, 0, edit.Range.End.Line-edit.Range.Start.Line+1)
		for ln := edit.Range.Start.Line; ; ln++ {
			out = append(out, Anchor{Line: ln})
			if ln == edit.Range.End.Line {
				break
			}
		}
		return out
	case EditPaste:
		if edit.PasteAt.Kind == PasteTargetSpan {
			out := make([]Anchor, 0, edit.PasteAt.Range.End.Line-edit.PasteAt.Range.Start.Line+1)
			for ln := edit.PasteAt.Range.Start.Line; ; ln++ {
				out = append(out, Anchor{Line: ln})
				if ln == edit.PasteAt.Range.End.Line {
					break
				}
			}
			return out
		}
		if edit.PasteAt.Cursor.Kind == CursorBeforeAnchor || edit.PasteAt.Cursor.Kind == CursorAfterAnchor {
			return []Anchor{edit.PasteAt.Cursor.Anchor}
		}
	case EditInsert:
		if edit.Cursor.Kind == CursorBeforeAnchor || edit.Cursor.Kind == CursorAfterAnchor {
			return []Anchor{edit.Cursor.Anchor}
		}
	}
	return nil
}

// buildLineMap maps unchanged 1-indexed line numbers from previous to current.
// upstream: crates/edit/src/modes/hashline/recovery.rs:68
func buildLineMap(previous, current string) map[uint32]uint32 {
	m := make(map[uint32]uint32)
	var prevLine uint32 = 1
	var curLine uint32 = 1
	for _, run := range lineRunsStr(previous, current) {
		if run.Added {
			curLine += run.Count
		} else if run.Removed {
			prevLine += run.Count
		} else {
			for off := range run.Count {
				m[prevLine+off] = curLine + off
			}
			prevLine += run.Count
			curLine += run.Count
		}
	}
	return m
}

func duplicatedValues(lines []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(lines))
	dup := make(map[string]struct{})
	for _, line := range lines {
		if _, ok := seen[line]; ok {
			dup[line] = struct{}{}
		} else {
			seen[line] = struct{}{}
		}
	}
	return dup
}

type lineNeighbors struct {
	before    uint32
	hasBefore bool
	after     uint32
	hasAfter  bool
}

func anchorNeighbors(anchorLines map[uint32]struct{}, lineCount int) map[uint32]lineNeighbors {
	sorted := make([]uint32, 0, len(anchorLines))
	for ln := range anchorLines {
		sorted = append(sorted, ln)
	}
	slices.Sort(sorted)
	neighbors := make(map[uint32]lineNeighbors, len(sorted))
	i := 0
	for i < len(sorted) {
		j := i + 1
		for j < len(sorted) && sorted[j] == sorted[j-1]+1 {
			j++
		}
		start := sorted[i]
		end := sorted[j-1]
		var nb lineNeighbors
		if start > 1 {
			nb.before = start - 1
			nb.hasBefore = true
		}
		if int(end) < lineCount {
			nb.after = end + 1
			nb.hasAfter = true
		}
		for k := i; k < j; k++ {
			neighbors[sorted[k]] = nb
		}
		i = j
	}
	return neighbors
}

func validateRecoveryContext(previous, current string, lineMap map[uint32]uint32, edits []Edit) bool {
	prevLines := strings.Split(previous, "\n")
	curLines := strings.Split(current, "\n")
	anchors := make(map[uint32]struct{})
	for _, edit := range edits {
		for _, a := range editAnchorsForRecovery(edit) {
			anchors[a.Line] = struct{}{}
		}
	}
	prevDups := duplicatedValues(prevLines)
	curDups := duplicatedValues(curLines)
	for line, nb := range anchorNeighbors(anchors, len(prevLines)) {
		mapped, ok := lineMap[line]
		if !ok {
			return false
		}
		prevVal := ""
		if int(line-1) < len(prevLines) {
			prevVal = prevLines[line-1]
		}
		curVal := ""
		if int(mapped-1) < len(curLines) {
			curVal = curLines[mapped-1]
		}
		_, inPrevDup := prevDups[prevVal]
		_, inCurDup := curDups[curVal]
		if !inPrevDup && !inCurDup {
			offset := int64(mapped) - int64(line)
			afterMatches := false
			if nb.hasAfter {
				if v, ok := lineMap[nb.after]; ok && int64(v) == int64(nb.after)+offset {
					afterMatches = true
				}
			}
			beforeMatches := false
			if nb.hasBefore {
				if v, ok := lineMap[nb.before]; ok && int64(v) == int64(nb.before)+offset {
					beforeMatches = true
				}
			}
			if !afterMatches && !beforeMatches {
				return false
			}
		} else {
			checked := false
			if nb.hasBefore {
				checked = true
				v, ok := lineMap[nb.before]
				diff := line - nb.before
				if !ok || mapped < diff || v != mapped-diff {
					return false
				}
			}
			if nb.hasAfter {
				checked = true
				v, ok := lineMap[nb.after]
				if !ok || v != mapped+(nb.after-line) {
					return false
				}
			}
			if !checked {
				return false
			}
		}
	}
	return true
}

func mapLineOffset(lineMap map[uint32]uint32, line uint32, offsets *[]int64) (uint32, bool) {
	mapped, ok := lineMap[line]
	if !ok {
		return 0, false
	}
	*offsets = append(*offsets, int64(mapped)-int64(line))
	return mapped, true
}

func remapEdits(previous, current string, edits []Edit) ([]Edit, int64, bool) {
	lineMap := buildLineMap(previous, current)
	if !validateRecoveryContext(previous, current, lineMap, edits) {
		return nil, 0, false
	}
	var offsets []int64
	remapped := make([]Edit, 0, len(edits))
	for _, edit := range edits {
		cp := edit
		switch edit.Kind {
		case EditDelete:
			ln, ok := mapLineOffset(lineMap, edit.Anchor.Line, &offsets)
			if !ok {
				return nil, 0, false
			}
			cp.Anchor.Line = ln
		case EditBlock:
			ln, ok := mapLineOffset(lineMap, edit.Anchor.Line, &offsets)
			if !ok {
				return nil, 0, false
			}
			cp.Anchor.Line = ln
		case EditCut:
			start, ok := mapLineOffset(lineMap, edit.Range.Start.Line, &offsets)
			if !ok {
				return nil, 0, false
			}
			end := start
			if edit.Range.Start.Line < edit.Range.End.Line {
				for ln := edit.Range.Start.Line + 1; ; ln++ {
					end, ok = mapLineOffset(lineMap, ln, &offsets)
					if !ok {
						return nil, 0, false
					}
					if ln == edit.Range.End.Line {
						break
					}
				}
			}
			cp.Range = ParsedRange{Start: Anchor{Line: start}, End: Anchor{Line: end}}
		case EditPaste:
			if edit.BlockStart > 0 {
				bs, ok := mapLineOffset(lineMap, edit.BlockStart, &offsets)
				if !ok {
					return nil, 0, false
				}
				cp.BlockStart = bs
			}
			if edit.PasteAt.Kind == PasteTargetSpan {
				start, ok := mapLineOffset(lineMap, edit.PasteAt.Range.Start.Line, &offsets)
				if !ok {
					return nil, 0, false
				}
				end := start
				if edit.PasteAt.Range.Start.Line < edit.PasteAt.Range.End.Line {
					for ln := edit.PasteAt.Range.Start.Line + 1; ; ln++ {
						end, ok = mapLineOffset(lineMap, ln, &offsets)
						if !ok {
							return nil, 0, false
						}
						if ln == edit.PasteAt.Range.End.Line {
							break
						}
					}
				}
				cp.PasteAt.Range = ParsedRange{Start: Anchor{Line: start}, End: Anchor{Line: end}}
			} else if edit.PasteAt.Cursor.Kind == CursorBeforeAnchor || edit.PasteAt.Cursor.Kind == CursorAfterAnchor {
				ln, ok := mapLineOffset(lineMap, edit.PasteAt.Cursor.Anchor.Line, &offsets)
				if !ok {
					return nil, 0, false
				}
				cp.PasteAt.Cursor.Anchor.Line = ln
			}
		case EditInsert:
			if edit.BlockStart > 0 {
				bs, ok := mapLineOffset(lineMap, edit.BlockStart, &offsets)
				if !ok {
					return nil, 0, false
				}
				cp.BlockStart = bs
			}
			if edit.Cursor.Kind == CursorBeforeAnchor || edit.Cursor.Kind == CursorAfterAnchor {
				ln, ok := mapLineOffset(lineMap, edit.Cursor.Anchor.Line, &offsets)
				if !ok {
					return nil, 0, false
				}
				cp.Cursor.Anchor.Line = ln
			}
		}
		remapped = append(remapped, cp)
	}
	if len(offsets) == 0 {
		return nil, 0, false
	}
	first := offsets[0]
	for _, o := range offsets[1:] {
		if o != first {
			return nil, 0, false
		}
	}
	return remapped, first, true
}

const (
	authoredLinesFloor  = 0.5
	authoredLinesMargin = 0.3
	keptShareBudget     = 1 << 23
	keptShareMinChars   = 8
)

func identifiesLine(line string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(line)) >= keptShareMinChars
}

func keptShare(window, body []string, verbatim map[string]struct{}, budget *int) (float64, bool) {
	if len(window) == 0 {
		return 0, false
	}
	windowChars := 0
	for _, line := range window {
		windowChars += utf8.RuneCountInString(strings.TrimSpace(line))
	}
	bodyChars := 0
	for _, row := range body {
		bodyChars += utf8.RuneCountInString(row)
	}
	// Saturating multiply check against budget.
	if windowChars > 0 && bodyChars > *budget/windowChars {
		return 0, false
	}
	cost := windowChars * bodyChars
	if cost > *budget {
		return 0, false
	}
	*budget -= cost
	var total float64
	for _, line := range window {
		t := strings.TrimSpace(line)
		if !identifiesLine(t) {
			continue
		}
		if _, ok := verbatim[t]; ok {
			total += 1.0
			continue
		}
		best := 0.0
		for _, row := range body {
			if s := similarity(t, row); s > best {
				best = s
			}
		}
		total += best
	}
	return total / float64(len(window)), true
}

// aimedAtAuthoredLines checks whether a remapped replacement reads as written for the
// current lines at its authored numbers rather than the remapped target.
// upstream: crates/edit/src/modes/hashline/recovery.rs:437
func aimedAtAuthoredLines(current string, authored []Edit, offset int64) bool {
	type grp struct {
		payload []string
		start   uint32
		end     uint32
	}
	var groups []grp
	start := 0
	for start < len(authored) {
		group, ok := findReplacementGroup(authored, start)
		if !ok {
			start++
			continue
		}
		start = group.deleteIndices[len(group.deleteIndices)-1] + 1
		hasNonBlank := false
		for _, row := range group.payload {
			if strings.TrimSpace(row) != "" {
				hasNonBlank = true
				break
			}
		}
		if hasNonBlank {
			groups = append(groups, grp{payload: group.payload, start: group.start, end: group.end})
		}
	}
	if len(groups) == 0 {
		return false
	}
	lines := strings.Split(current, "\n")
	getWindow := func(s, e uint32, shift int64) []string {
		out := make([]string, 0, e-s+1)
		for ln := s; ; ln++ {
			idx := int64(ln) + shift - 1
			if idx >= 0 && int(idx) < len(lines) {
				out = append(out, lines[idx])
			} else {
				out = append(out, "")
			}
			if ln == e {
				break
			}
		}
		return out
	}
	budget := keptShareBudget
	for _, g := range groups {
		var body []string
		verbatim := make(map[string]struct{})
		for _, row := range g.payload {
			t := strings.TrimSpace(row)
			if t != "" {
				body = append(body, t)
				verbatim[t] = struct{}{}
			}
		}
		target := getWindow(g.start, g.end, offset)
		hasIdent := false
		for _, line := range target {
			if identifiesLine(line) {
				hasIdent = true
				break
			}
		}
		if !hasIdent {
			continue
		}
		kept, ok := keptShare(getWindow(g.start, g.end, 0), body, verbatim, &budget)
		if !ok {
			return false
		}
		if kept < authoredLinesFloor {
			continue
		}
		targetKept, ok := keptShare(target, body, verbatim, &budget)
		if !ok {
			return false
		}
		if kept > targetKept+authoredLinesMargin {
			return true
		}
	}
	return false
}

// TryRecover attempts to rebase stale anchors from a retained snapshot onto current text.
// upstream: crates/edit/src/modes/hashline/recovery.rs:502
func TryRecover(store *Store, args RecoveryArgs) (*RecoveryResult, *EditError) {
	if store == nil {
		return nil, nil
	}
	snapshot, ok := store.ByHash(args.Path, args.FileHash)
	if !ok {
		return nil, nil
	}
	warning := RecoverySessionChainWarning
	if head, ok := store.Head(args.Path); ok && head.Text == snapshot.Text {
		warning = RecoveryExternalWarning
	}
	remapped, offset, ok := remapEdits(snapshot.Text, args.CurrentText, args.Edits)
	if !ok {
		return nil, nil
	}
	if offset != 0 && aimedAtAuthoredLines(args.CurrentText, args.Edits, offset) {
		return nil, nil
	}
	var fork *Clipboard
	if args.Clipboard != nil && HasClipboardEdit(remapped) {
		fork = args.Clipboard.Fork()
	}
	applied, err := ApplyEdits(args.CurrentText, remapped, ApplyOptions{
		Clipboard:    fork,
		Path:         args.Path,
		OnEmptyPaste: EmptyPasteThrow,
	})
	if err != nil {
		return nil, nil
	}
	if applied.Text == args.CurrentText {
		return nil, nil
	}
	if offset != 0 && parsesCleanly(args.Path, args.CurrentText) && !parsesCleanly(args.Path, applied.Text) {
		return nil, nil
	}
	if args.Clipboard != nil && fork != nil {
		*args.Clipboard = *fork
	}
	banner := warning
	if offset != 0 {
		banner = RecoveryLineRemapWarning
	}
	warnings := append([]string{banner}, applied.Warnings...)
	return &RecoveryResult{
		Text:             applied.Text,
		FirstChangedLine: applied.FirstChangedLine,
		Warnings:         warnings,
	}, nil
}
