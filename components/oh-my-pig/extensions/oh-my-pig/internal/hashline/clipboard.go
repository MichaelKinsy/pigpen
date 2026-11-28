package hashline

import (
	"fmt"
	"slices"
	"strconv"
)

// Clipboard edit resolution.
// upstream: crates/edit/src/modes/hashline/clipboard.rs

// EmptyPaste controls behavior when an anonymous or named paste has no available cut.
// upstream: crates/edit/src/modes/hashline/apply.rs:50
type EmptyPaste int

const (
	EmptyPasteThrow EmptyPaste = iota
	EmptyPasteDrop
)

func describeCut(edit Edit) string {
	var span string
	if edit.Range.Start.Line == edit.Range.End.Line {
		span = strconv.FormatUint(uint64(edit.Range.Start.Line), 10)
	} else {
		span = fmt.Sprintf("%d.=%d", edit.Range.Start.Line, edit.Range.End.Line)
	}
	if edit.HasRegister {
		return fmt.Sprintf("CUT %s @%s", span, edit.Register)
	}
	return "CUT " + span
}

// HasClipboardEdit reports whether an edit stream reads or writes a clipboard register.
// upstream: crates/edit/src/modes/hashline/clipboard.rs:34
func HasClipboardEdit(edits []Edit) bool {
	for _, edit := range edits {
		switch edit.Kind {
		case EditCut, EditPaste:
			return true
		case EditBlock:
			if edit.BlockMode == BlockModeCut || edit.BlockMode == BlockModePasteAfter || edit.HasRegister {
				return true
			}
		}
	}
	return false
}

func knownRegisters(clipboard *Clipboard) []string {
	if clipboard == nil || len(clipboard.Named) == 0 {
		return nil
	}
	known := make([]string, 0, len(clipboard.Named))
	for k := range clipboard.Named {
		known = append(known, k)
	}
	slices.Sort(known)
	return known
}

func readRegister(
	reg string,
	hasReg bool,
	span bool,
	clipboard *Clipboard,
	lineNum uint32,
	onEmpty EmptyPaste,
	onWarning func(string),
) ([]string, bool, *EditError) {
	if hasReg {
		if clipboard.Named != nil {
			if lines, ok := clipboard.Named[reg]; ok {
				return slices.Clone(lines), true, nil
			}
		}
		if onEmpty == EmptyPasteDrop {
			return nil, false, nil
		}
		known := knownRegisters(clipboard)
		if span {
			return nil, false, ApplyError(fmt.Sprintf("line %d: %s", lineNum, EmptyRegisterSpanPasteMessage(reg, known)))
		}
		if onWarning != nil {
			onWarning(fmt.Sprintf("line %d: %s", lineNum, EmptyRegisterPasteWarning(reg, known)))
		}
		return []string{}, true, nil
	}

	if len(clipboard.PendingAnonCuts) > 1 {
		if onEmpty == EmptyPasteDrop {
			return nil, false, nil
		}
		return nil, false, ApplyError(fmt.Sprintf("line %d: %s", lineNum, AmbiguousAnonymousPasteMessage(clipboard.PendingAnonCuts)))
	}
	if !clipboard.HasAnon {
		if onEmpty == EmptyPasteDrop {
			return nil, false, nil
		}
		return nil, false, ApplyError(fmt.Sprintf("line %d: %s", lineNum, EmptyPasteMessage))
	}
	lines := slices.Clone(clipboard.Lines)
	clipboard.PendingAnonCuts = nil
	return lines, true, nil
}

func writeRegister(edit Edit, fileLines []string, clipboard *Clipboard) *EditError {
	if edit.Range.Start.Line == 0 || int(edit.Range.End.Line) > len(fileLines) {
		return RejectedError(
			RejectionOutOfRange,
			fmt.Sprintf("line %d: `%s` is out of range (file has %d lines).", edit.LineNum, describeCut(edit), len(fileLines)),
		)
	}
	captured := slices.Clone(fileLines[edit.Range.Start.Line-1 : edit.Range.End.Line])
	if edit.HasRegister {
		if clipboard.Named == nil {
			clipboard.Named = make(map[string][]string)
		}
		clipboard.Named[edit.Register] = captured
	} else {
		clipboard.Lines = captured
		clipboard.HasAnon = true
		clipboard.PendingAnonCuts = append(clipboard.PendingAnonCuts, describeCut(edit))
	}
	return nil
}

// ResolveClipboardEdits lowers cut/paste operations into inserts and deletes against fileLines.
// upstream: crates/edit/src/modes/hashline/clipboard.rs:146
func ResolveClipboardEdits(
	edits []Edit,
	fileLines []string,
	clipboard *Clipboard,
	onEmpty EmptyPaste,
	onWarning func(string),
) ([]Edit, *EditError) {
	if !HasClipboardEdit(edits) {
		return edits, nil
	}
	var resolved []Edit
	var synthIndex uint32
	for _, edit := range edits {
		switch edit.Kind {
		case EditCut:
			if err := writeRegister(edit, fileLines, clipboard); err != nil {
				return nil, err
			}
		case EditPaste:
			isSpan := edit.PasteAt.Kind == PasteTargetSpan
			lines, ok, err := readRegister(edit.Register, edit.HasRegister, isSpan, clipboard, edit.LineNum, onEmpty, onWarning)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if !isSpan {
				for _, text := range lines {
					resolved = append(resolved, Edit{
						Kind:        EditInsert,
						Cursor:      edit.PasteAt.Cursor,
						Text:        text,
						LineNum:     edit.LineNum,
						Index:       synthIndex,
						Replacement: false,
						BlockStart:  edit.BlockStart,
					})
					synthIndex++
				}
			} else {
				rng := edit.PasteAt.Range
				if rng.Start.Line == 0 || int(rng.End.Line) > len(fileLines) {
					regSuffix := ""
					if edit.HasRegister {
						regSuffix = " @" + edit.Register
					}
					return nil, RejectedError(
						RejectionOutOfRange,
						fmt.Sprintf(
							"line %d: `PUT %d.=%d%s` is out of range (file has %d lines).",
							edit.LineNum, rng.Start.Line, rng.End.Line, regSuffix, len(fileLines),
						),
					)
				}
				cursor := Cursor{Kind: CursorBeforeAnchor, Anchor: Anchor{Line: rng.Start.Line}}
				for _, text := range lines {
					resolved = append(resolved, Edit{
						Kind:        EditInsert,
						Cursor:      cursor,
						Text:        text,
						LineNum:     edit.LineNum,
						Index:       synthIndex,
						Replacement: true,
					})
					synthIndex++
				}
				for ln := rng.Start.Line; ; ln++ {
					resolved = append(resolved, Edit{
						Kind:    EditDelete,
						Anchor:  Anchor{Line: ln},
						LineNum: edit.LineNum,
						Index:   synthIndex,
					})
					synthIndex++
					if ln == rng.End.Line {
						break
					}
				}
			}
		default:
			resolved = append(resolved, edit)
		}
	}
	return resolved, nil
}

// ValidateClipboardSequence validates anonymous clipboard sequencing without mutating clipboard.
// upstream: crates/edit/src/modes/hashline/clipboard.rs:235
func ValidateClipboardSequence(edits []Edit, clipboard *Clipboard) *EditError {
	fork := clipboard.Fork()
	for _, edit := range edits {
		switch edit.Kind {
		case EditCut:
			if edit.HasRegister {
				if fork.Named == nil {
					fork.Named = make(map[string][]string)
				}
				fork.Named[edit.Register] = nil
			} else {
				fork.Lines = nil
				fork.HasAnon = true
				fork.PendingAnonCuts = append(fork.PendingAnonCuts, describeCut(edit))
			}
		case EditPaste:
			_, _, err := readRegister(
				edit.Register,
				edit.HasRegister,
				edit.PasteAt.Kind == PasteTargetSpan,
				fork,
				edit.LineNum,
				EmptyPasteThrow,
				nil,
			)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
