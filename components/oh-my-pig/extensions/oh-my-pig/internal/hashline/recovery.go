package hashline

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// RecoveryExternalWarning is emitted when an external write changed the file since the cached snapshot.
	// upstream: crates/pi-edit/src/modes/hashline/messages.rs:682
	RecoveryExternalWarning = "Recovered from a stale file hash using a previous read snapshot (file changed externally between read and edit)."
	// RecoverySessionChainWarning is emitted when an earlier in-session snapshot was used for recovery.
	// upstream: crates/pi-edit/src/modes/hashline/messages.rs:687
	RecoverySessionChainWarning = "Recovered from a stale file hash using an earlier in-session snapshot (a prior edit in this session advanced the hash)."
	// RecoveryLineRemapWarning is emitted when stale anchors shifted to new line numbers.
	// upstream: crates/pi-edit/src/modes/hashline/messages.rs:693
	RecoveryLineRemapWarning = "Recovered by remapping stale line anchors to unchanged current lines (file changed since the tagged read). Verify the diff matches your intent."
)

// TryRecover attempts to rebase ops anchored against a retained in-session snapshot
// onto currentText. Returns (result, true, nil) on recovery, or (_, false, nil) if
// the stale tag cannot be safely recovered.
// upstream: crates/pi-edit/src/modes/hashline/recovery.rs:367
func TryRecover(store *Store, absPath, displayPath, currentText, staleHash string, ops []Op, clipboard *Clipboard) (ApplyResult, bool, error) {
	if store == nil || len(ops) == 0 {
		return ApplyResult{}, false, nil
	}
	snap, ok := store.ByHash(absPath, staleHash)
	if !ok {
		return ApplyResult{}, false, nil
	}

	head, hasHead := store.Head(absPath)
	warning := RecoverySessionChainWarning
	if hasHead && head.Hash == snap.Hash {
		warning = RecoveryExternalWarning
	}

	prevLines := SplitAddressableFileLines(snap.Text)
	currLines := SplitAddressableFileLines(currentText)
	lineMap := buildLineMap(prevLines, currLines)

	// Remap all concrete line anchors in ops and verify a uniform offset + unchanged text.
	remapped := make([]Op, len(ops))
	offsetSet := false
	uniformOffset := 0

	mapOne := func(oldLine int) (int, bool) {
		newLine, ok := lineMap[oldLine]
		if !ok {
			return 0, false
		}
		// Reject if the target line's content changed or is not a safe unique match.
		if oldLine < 1 || oldLine > len(prevLines) || newLine < 1 || newLine > len(currLines) {
			return 0, false
		}
		if prevLines[oldLine-1] != currLines[newLine-1] {
			return 0, false
		}
		off := newLine - oldLine
		if !offsetSet {
			uniformOffset = off
			offsetSet = true
		} else if off != uniformOffset {
			return 0, false
		}
		return newLine, true
	}

	for i, op := range ops {
		r := op
		switch op.Kind {
		case OpPutEOF, OpPasteEOF:
			// Content-independent.
		case OpPutRange, OpCutRange, OpPasteRange:
			ns, ok := mapOne(op.Start)
			if !ok {
				return ApplyResult{}, false, nil
			}
			ne := ns
			for ln := op.Start + 1; ln <= op.End; ln++ {
				mapped, ok := mapOne(ln)
				if !ok || mapped != ne+1 {
					return ApplyResult{}, false, nil
				}
				ne = mapped
			}
			r.Start = ns
			r.End = ne
		case OpPutBlock, OpPutAfterBlock, OpCutBlock, OpPasteBlock, OpPasteAfterBlock:
			// Block ops require the anchor line AND the whole resolved block to be unchanged.
			ns, ok := mapOne(op.Start)
			if !ok {
				return ApplyResult{}, false, nil
			}
			oldSpan, okOld := ResolveBlock(prevLines, displayPath, op.Start)
			newSpan, okNew := ResolveBlock(currLines, displayPath, ns)
			if !okOld || !okNew || (oldSpan.End-oldSpan.Start) != (newSpan.End-newSpan.Start) {
				return ApplyResult{}, false, nil
			}
			for k := 0; k <= oldSpan.End-oldSpan.Start; k++ {
				if prevLines[oldSpan.Start-1+k] != currLines[newSpan.Start-1+k] {
					return ApplyResult{}, false, nil
				}
			}
			r.Start = ns
		case OpPutBefore, OpPutAfter, OpPasteBefore, OpPasteAfter:
			if op.Kind == OpPutBefore && op.Start == 1 {
				// BOF is content-independent.
			} else {
				ns, ok := mapOne(op.Start)
				if !ok {
					return ApplyResult{}, false, nil
				}
				r.Start = ns
			}
		}
		remapped[i] = r
	}

	if !offsetSet {
		return ApplyResult{}, false, nil
	}

	res, err := ApplyOps(currentText, displayPath, remapped, clipboard)
	if err != nil || !res.Changed {
		return ApplyResult{}, false, nil
	}

	w := warning
	if uniformOffset != 0 {
		w = RecoveryLineRemapWarning
	}
	res.Warnings = append([]string{w}, res.Warnings...)
	return res, true, nil
}

// buildLineMap aligns identical lines between prev and curr using LCS dynamic programming
// (with common prefix/suffix trimming for speed), returning 1-indexed oldLine -> newLine.
func buildLineMap(prev, curr []string) map[int]int {
	m := make(map[int]int)
	// Common prefix.
	prefix := 0
	for prefix < len(prev) && prefix < len(curr) && prev[prefix] == curr[prefix] {
		m[prefix+1] = prefix + 1
		prefix++
	}
	// Common suffix.
	suffix := 0
	for suffix < len(prev)-prefix && suffix < len(curr)-prefix &&
		prev[len(prev)-1-suffix] == curr[len(curr)-1-suffix] {
		m[len(prev)-suffix] = len(curr) - suffix
		suffix++
	}

	pMid := prev[prefix : len(prev)-suffix]
	cMid := curr[prefix : len(curr)-suffix]
	if len(pMid) == 0 || len(cMid) == 0 {
		return m
	}

	// Standard LCS table on the middle slice (bounded for safety).
	if len(pMid)*len(cMid) > 2_000_000 {
		return m
	}
	rows := len(pMid) + 1
	cols := len(cMid) + 1
	dp := make([]int, rows*cols)
	for i := len(pMid) - 1; i >= 0; i-- {
		for j := len(cMid) - 1; j >= 0; j-- {
			if pMid[i] == cMid[j] {
				dp[i*cols+j] = dp[(i+1)*cols+(j+1)] + 1
			} else if dp[(i+1)*cols+j] >= dp[i*cols+(j+1)] {
				dp[i*cols+j] = dp[(i+1)*cols+j]
			} else {
				dp[i*cols+j] = dp[i*cols+(j+1)]
			}
		}
	}
	i, j := 0, 0
	for i < len(pMid) && j < len(cMid) {
		if pMid[i] == cMid[j] {
			m[prefix+i+1] = prefix + j + 1
			i++
			j++
		} else if dp[(i+1)*cols+j] >= dp[i*cols+(j+1)] {
			i++
		} else {
			j++
		}
	}
	return m
}

// FormatBlockResolution formats a resolved block op line as omp prints it.
// upstream: crates/pi-edit/src/modes/hashline/patcher.rs:502
func FormatBlockResolution(r BlockResolution) string {
	var tmpl, suffix string
	switch r.Kind {
	case OpPutBlock, OpPasteBlock:
		tmpl = "PUT N*:"
	case OpPutAfterBlock:
		tmpl = "PUT >N*:"
		suffix = "; body lands after line "
	case OpCutBlock:
		tmpl = "CUT N*"
	case OpPasteAfterBlock:
		tmpl = "PUT >N*"
		suffix = "; clipboard lands after line "
	default:
		tmpl = "PUT N*:"
	}
	op := strings.Replace(tmpl, "N", fmt.Sprintf("%d", r.AnchorLine), 1)
	count := r.End - r.Start + 1
	span := fmt.Sprintf("lines %d-%d", r.Start, r.End)
	if r.Start == r.End {
		span = fmt.Sprintf("line %d", r.Start)
	}
	plural := "s"
	if count == 1 {
		plural = ""
	}
	if suffix != "" {
		suffix = fmt.Sprintf("%s%d", suffix, r.End)
	}
	return fmt.Sprintf("%s → resolved %s (%d line%s)%s", op, span, count, plural, suffix)
}

// buildNewFilePreview renders the new file's lines around each changed window
// as `N:text` rows joined by `…`, including off-window enclosing block boundaries.
func buildNewFilePreview(newLines []string, changedWindows [][2]int) string {
	if len(newLines) == 0 || len(changedWindows) == 0 {
		return ""
	}
	visible := make(map[int]bool)
	for _, w := range changedWindows {
		lo := max(1, w[0]-2)
		hi := min(len(newLines), w[1]+2)
		// If a window is a pure insertion whose first line is blank (e.g. PUT >23: starting with `+`),
		// 1 line before the insertion is the anchor line itself.
		if w[0] > 1 && w[1] >= w[0] && newLines[w[0]-1] == "" {
			lo = max(1, w[0]-1)
		}
		if w[0] > 2 && newLines[w[0]-2] == "" && newLines[w[0]-3] == "" {
			lo = max(lo, w[0]-1)
		}
		// If the window is a pure deletion (w[1] < w[0]), show 2 lines before and 4 lines after
		// (matching omp's diff_string 4-line context around deletions).
		if w[1] < w[0] {
			lo = max(1, w[0]-2)
			hi = min(len(newLines), w[0]+4)
		}
		for ln := lo; ln <= hi; ln++ {
			visible[ln] = true
		}
	}
	// If every line is visible, render directly without block-context scanning.
	if len(visible) >= len(newLines) {
		var out []string
		for i, line := range newLines {
			out = append(out, FormatNumberedLine(i+1, line))
		}
		return strings.Join(out, "\n")
	}
	return RenderLinesWithBlockContext(newLines, visible)
}

// Suppress unused import if slices is only used in future extensions.
var _ = slices.Sort[[]int]
