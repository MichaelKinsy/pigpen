package hashline

import (
	"fmt"
	"slices"
	"strings"
)

// FormatMismatchMessage formats the model-facing snapshot tag mismatch error.
// upstream: crates/pi-edit/src/modes/hashline/mismatch.rs:75, messages.rs:51
func FormatMismatchMessage(d MismatchDetails) string {
	pathSuffix := ""
	if d.Path != "" {
		pathSuffix = " for " + d.Path
	}

	var lines []string
	if d.HashRecognized {
		lines = append(lines,
			fmt.Sprintf("Edit rejected%s: file changed between read and edit.", pathSuffix),
			fmt.Sprintf(
				"Section is bound to #%s, but the current file hashes to #%s. If a prior edit in this session modified this file, copy the [path#newhash] header from that edit's response; otherwise re-read the file with `read` to refresh the tag before retrying.",
				d.ExpectedFileHash, d.ActualFileHash,
			),
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("Edit rejected%s: hash #%s is not from this session.", pathSuffix, d.ExpectedFileHash),
		)
		for _, origin := range d.TagOriginPaths {
			lines = append(lines, fmt.Sprintf("Hash #%s was issued in this session for %s.", d.ExpectedFileHash, origin))
		}
		lines = append(lines, fmt.Sprintf(
			"The current file hashes to #%s. Re-read the file with `read` to copy a current [path#tag] header — never invent the tag and never reuse one from a prior session.",
			d.ActualFileHash,
		))
	}

	if len(d.AnchorLines) > 0 && len(d.FileLines) > 0 {
		anchorSet := make(map[int]bool, len(d.AnchorLines))
		displaySet := make(map[int]bool)
		for _, a := range d.AnchorLines {
			if a < 1 || a > len(d.FileLines) {
				continue
			}
			anchorSet[a] = true
			for ln := max(1, a-2); ln <= min(len(d.FileLines), a+2); ln++ {
				displaySet[ln] = true
			}
		}
		if len(displaySet) > 0 {
			sorted := make([]int, 0, len(displaySet))
			for ln := range displaySet {
				sorted = append(sorted, ln)
			}
			slices.Sort(sorted)
			var ctx []string
			prev := 0
			for _, ln := range sorted {
				if prev > 0 && ln > prev+1 {
					ctx = append(ctx, "...")
				}
				prev = ln
				marker := " "
				if anchorSet[ln] {
					marker = "*"
				}
				ctx = append(ctx, marker+FormatNumberedLine(ln, d.FileLines[ln-1]))
			}
			lines = append(lines, "")
			lines = append(lines, ctx...)
		}
	}
	return strings.Join(lines, "\n")
}

// CollectAnchorLines returns sorted unique 1-indexed anchor lines referenced by ops.
func CollectAnchorLines(ops []Op) []int {
	seen := make(map[int]bool)
	var out []int
	add := func(n int) {
		if n > 0 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, op := range ops {
		add(op.Start)
		add(op.End)
	}
	slices.Sort(out)
	return out
}

// concreteEdit is a resolved low-level edit against original 1-indexed line numbers.
type concreteEdit struct {
	order   int
	start   int // 1-indexed inclusive start of deleted range (or anchor for pure insert)
	end     int // 1-indexed inclusive end of deleted range; 0 for pure insert
	insert  int // 0-indexed position in original lines where payload is inserted
	payload []string
}

// ApplyOps applies a section's parsed operations to LF-normalized text using clipboard.
// upstream: crates/pi-edit/src/modes/hashline/apply.rs, clipboard.rs, block.rs
func ApplyOps(normalizedText, displayPath string, ops []Op, clipboard *Clipboard) (ApplyResult, error) {
	if len(ops) == 0 {
		return ApplyResult{Content: normalizedText, Changed: false}, nil
	}

	hasTrailingNL := strings.HasSuffix(normalizedText, "\n")
	var origLines []string
	if normalizedText != "" {
		origLines = SplitAddressableFileLines(normalizedText)
	}

	if clipboard == nil {
		clipboard = &Clipboard{}
	}

	// Pass 1: resolve block ops and clipboard ops in authored order.
	var resolved []concreteEdit
	var blockResolutions []BlockResolution
	for idx, op := range ops {
		switch op.Kind {
		case OpPutRange:
			if err := checkBounds(op.Start, op.End, len(origLines)); err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{
				order:   idx,
				start:   op.Start,
				end:     op.End,
				insert:  op.Start - 1,
				payload: op.Payload,
			})

		case OpPutBlock:
			span, ok := ResolveBlock(origLines, displayPath, op.Start)
			if !ok {
				return ApplyResult{}, fmt.Errorf("line %d: could not resolve multi-line block at line %d", op.LineNum, op.Start)
			}
			blockResolutions = append(blockResolutions, BlockResolution{
				Kind:       op.Kind,
				AnchorLine: op.Start,
				Start:      span.Start,
				End:        span.End,
			})
			resolved = append(resolved, concreteEdit{
				order:   idx,
				start:   span.Start,
				end:     span.End,
				insert:  span.Start - 1,
				payload: op.Payload,
			})

		case OpPutBefore:
			if op.Start != 1 {
				if err := checkBounds(op.Start, op.Start, len(origLines)); err != nil {
					return ApplyResult{}, err
				}
			}
			pos := op.Start - 1
			if pos > len(origLines) {
				pos = len(origLines)
			}
			resolved = append(resolved, concreteEdit{
				order:   idx,
				insert:  pos,
				payload: op.Payload,
			})

		case OpPutAfter:
			if err := checkBounds(op.Start, op.Start, len(origLines)); err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{
				order:   idx,
				insert:  op.Start,
				payload: op.Payload,
			})

		case OpPutEOF:
			resolved = append(resolved, concreteEdit{
				order:   idx,
				insert:  len(origLines),
				payload: op.Payload,
			})

		case OpPutAfterBlock:
			span, ok := ResolveBlock(origLines, displayPath, op.Start)
			if !ok {
				return ApplyResult{}, fmt.Errorf("line %d: could not resolve multi-line block at line %d", op.LineNum, op.Start)
			}
			blockResolutions = append(blockResolutions, BlockResolution{
				Kind:       op.Kind,
				AnchorLine: op.Start,
				Start:      span.Start,
				End:        span.End,
			})
			resolved = append(resolved, concreteEdit{
				order:   idx,
				insert:  span.End,
				payload: op.Payload,
			})

		case OpCutRange:
			if err := checkBounds(op.Start, op.End, len(origLines)); err != nil {
				return ApplyResult{}, err
			}
			captured := slices.Clone(origLines[op.Start-1 : op.End])
			writeClipboard(clipboard, op.Register, captured, fmt.Sprintf("CUT %d.=%d", op.Start, op.End))
			resolved = append(resolved, concreteEdit{
				order:  idx,
				start:  op.Start,
				end:    op.End,
				insert: op.Start - 1,
			})

		case OpCutBlock:
			span, ok := ResolveBlock(origLines, displayPath, op.Start)
			if !ok {
				return ApplyResult{}, fmt.Errorf("line %d: could not resolve multi-line block at line %d", op.LineNum, op.Start)
			}
			blockResolutions = append(blockResolutions, BlockResolution{
				Kind:       op.Kind,
				AnchorLine: op.Start,
				Start:      span.Start,
				End:        span.End,
			})
			captured := slices.Clone(origLines[span.Start-1 : span.End])
			writeClipboard(clipboard, op.Register, captured, fmt.Sprintf("CUT %d*", op.Start))
			resolved = append(resolved, concreteEdit{
				order:  idx,
				start:  span.Start,
				end:    span.End,
				insert: span.Start - 1,
			})

		case OpPasteBefore:
			if op.Start != 1 {
				if err := checkBounds(op.Start, op.Start, len(origLines)); err != nil {
					return ApplyResult{}, err
				}
			}
			lines, err := readClipboard(clipboard, op.Register, op.LineNum)
			if err != nil {
				return ApplyResult{}, err
			}
			pos := op.Start - 1
			if pos > len(origLines) {
				pos = len(origLines)
			}
			resolved = append(resolved, concreteEdit{order: idx, insert: pos, payload: lines})

		case OpPasteAfter:
			if err := checkBounds(op.Start, op.Start, len(origLines)); err != nil {
				return ApplyResult{}, err
			}
			lines, err := readClipboard(clipboard, op.Register, op.LineNum)
			if err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{order: idx, insert: op.Start, payload: lines})

		case OpPasteEOF:
			lines, err := readClipboard(clipboard, op.Register, op.LineNum)
			if err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{order: idx, insert: len(origLines), payload: lines})

		case OpPasteAfterBlock:
			span, ok := ResolveBlock(origLines, displayPath, op.Start)
			if !ok {
				return ApplyResult{}, fmt.Errorf("line %d: could not resolve multi-line block at line %d", op.LineNum, op.Start)
			}
			blockResolutions = append(blockResolutions, BlockResolution{
				Kind:       op.Kind,
				AnchorLine: op.Start,
				Start:      span.Start,
				End:        span.End,
			})
			lines, err := readClipboard(clipboard, op.Register, op.LineNum)
			if err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{order: idx, insert: span.End, payload: lines})

		case OpPasteRange:
			if err := checkBounds(op.Start, op.End, len(origLines)); err != nil {
				return ApplyResult{}, err
			}
			lines, err := readClipboard(clipboard, op.Register, op.LineNum)
			if err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{
				order:   idx,
				start:   op.Start,
				end:     op.End,
				insert:  op.Start - 1,
				payload: lines,
			})

		case OpPasteBlock:
			span, ok := ResolveBlock(origLines, displayPath, op.Start)
			if !ok {
				return ApplyResult{}, fmt.Errorf("line %d: could not resolve multi-line block at line %d", op.LineNum, op.Start)
			}
			blockResolutions = append(blockResolutions, BlockResolution{
				Kind:       op.Kind,
				AnchorLine: op.Start,
				Start:      span.Start,
				End:        span.End,
			})
			lines, err := readClipboard(clipboard, op.Register, op.LineNum)
			if err != nil {
				return ApplyResult{}, err
			}
			resolved = append(resolved, concreteEdit{
				order:   idx,
				start:   span.Start,
				end:     span.End,
				insert:  span.Start - 1,
				payload: lines,
			})
		}
	}

	// Sort edits by position (ascending insert index, then authored order).
	slices.SortStableFunc(resolved, func(a, b concreteEdit) int {
		if a.insert != b.insert {
			return a.insert - b.insert
		}
		return a.order - b.order
	})

	// Check for overlapping deleted ranges.
	lastDeletedEnd := 0
	for _, e := range resolved {
		if e.end > 0 {
			if e.start <= lastDeletedEnd {
				return ApplyResult{}, fmt.Errorf("overlapping edits at line %d", e.start)
			}
			lastDeletedEnd = e.end
		}
	}

	// Build new lines and record 1-indexed changed windows in newLines.
	var newLines []string
	var changedWindows [][2]int
	firstChanged := 0
	cursor := 0 // 0-indexed into origLines
	for _, e := range resolved {
		if e.insert > cursor {
			newLines = append(newLines, origLines[cursor:e.insert]...)
			cursor = e.insert
		}
		winStart := len(newLines) + 1
		if len(e.payload) > 0 {
			newLines = append(newLines, e.payload...)
		}
		winEnd := len(newLines)
		if firstChanged == 0 {
			firstChanged = max(1, winStart)
		}
		changedWindows = append(changedWindows, [2]int{winStart, winEnd})
		if e.end > 0 {
			cursor = e.end // skip origLines[e.start-1 : e.end]
		}
	}
	if cursor < len(origLines) {
		newLines = append(newLines, origLines[cursor:]...)
	}

	var newText string
	if len(newLines) > 0 {
		newText = strings.Join(newLines, "\n")
		if hasTrailingNL {
			newText += "\n"
		}
	}

	changed := newText != normalizedText
	preview := ""
	if changed {
		preview = buildNewFilePreview(newLines, changedWindows)
	}

	return ApplyResult{
		Content:          newText,
		Changed:          changed,
		FirstChangedLine: firstChanged,
		Preview:          preview,
		BlockResolutions: blockResolutions,
	}, nil
}

func checkBounds(start, end, total int) error {
	if start < 1 || start > total {
		return fmt.Errorf("Line %d does not exist (file has %d lines)", start, total)
	}
	if end < 1 || end > total {
		return fmt.Errorf("Line %d does not exist (file has %d lines)", end, total)
	}
	return nil
}

func writeClipboard(c *Clipboard, reg string, lines []string, desc string) {
	if reg != "" {
		if c.Named == nil {
			c.Named = make(map[string][]string)
		}
		c.Named[reg] = lines
		return
	}
	c.Lines = lines
	c.HasAnon = true
	c.PendingAnonCuts = append(c.PendingAnonCuts, desc)
}

func readClipboard(c *Clipboard, reg string, lineNum int) ([]string, error) {
	if reg != "" {
		if lines, ok := c.Named[reg]; ok {
			return slices.Clone(lines), nil
		}
		return nil, fmt.Errorf("line %d: clipboard register @%s is empty", lineNum, reg)
	}
	if len(c.PendingAnonCuts) > 1 {
		return nil, fmt.Errorf("line %d: ambiguous anonymous paste after multiple CUTs (%s); use named @registers", lineNum, strings.Join(c.PendingAnonCuts, ", "))
	}
	if !c.HasAnon {
		return nil, fmt.Errorf("line %d: nothing to paste (no prior CUT in this patch)", lineNum)
	}
	c.PendingAnonCuts = nil
	return slices.Clone(c.Lines), nil
}
