package tintinweb_subagents

import (
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// steerInput is the viewer's steering composer: pi-tui's Input (components/input.ts), a single-line text input with
// horizontal scrolling, bracketed paste, Kitty CSI-u printable keys, undo, a kill ring and grapheme-aware movement.
// pitui does not carry Input yet, so this is the port, built on pitui's keybindings, KillRing, word navigation, Kitty
// decoder and width helpers. Offsets are UTF-16 units, as in the original (`value.length`, `value.slice`).
// upstream: pi-tui src/components/input.ts (mouse handling omitted: an overlay composer has no pointer path in PiG).
type steerInput struct {
	value  string
	cursor int // UTF-16 units into value
	prompt string
	// focused: the host sets it while the input owns the keyboard; it emits the hardware cursor marker.
	focused             bool
	renderedStartColumn int
	onSubmit            func(value string)
	onEscape            func()

	pasteBuffer string
	inPaste     bool

	killRing   tui.KillRing
	lastAction string // "kill", "yank", "type-word" or ""
	undoStack  []inputState
}

type inputState struct {
	value  string
	cursor int
}

// newSteerInput is `new Input()` with `focused = true`, as the viewer opens it.
func newSteerInput() *steerInput { return &steerInput{prompt: "> ", focused: true} }

func u16(s string) int { return jsstring.Length(s) }

// slice16 is s.slice(start, end) in UTF-16 units.
func slice16(s string, start int, end ...int) string { return jsstring.Slice(s, start, end...) }

func isWhitespaceChar(s string) bool { return strings.ContainsFunc(s, widthx.IsJSSpace) }

// lastGraphemeLen is the UTF-16 length of the last grapheme of s (1 for an empty s, as the original's fallback).
func lastGraphemeLen(s string) int {
	if s == "" {
		return 1
	}
	last := ""
	for rest := s; rest != ""; {
		last, rest = widthx.FirstGrapheme(rest)
	}
	return u16(last)
}

// firstGraphemeLen is the UTF-16 length of the first grapheme of s (1 for an empty s).
func firstGraphemeLen(s string) int {
	if s == "" {
		return 1
	}
	g, _ := widthx.FirstGrapheme(s)
	return u16(g)
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// handleInput handles one chunk of terminal input.
func (in *steerInput) handleInput(data string) {
	// Bracketed paste: \x1b[200~ ... \x1b[201~, possibly split across chunks.
	if strings.Contains(data, pasteStart) {
		in.inPaste = true
		in.pasteBuffer = ""
		data = strings.Replace(data, pasteStart, "", 1)
	}
	if in.inPaste {
		in.pasteBuffer += data
		if end := strings.Index(in.pasteBuffer, pasteEnd); end != -1 {
			in.handlePaste(in.pasteBuffer[:end])
			in.inPaste = false
			remaining := in.pasteBuffer[end+len(pasteEnd):]
			in.pasteBuffer = ""
			if remaining != "" {
				in.handleInput(remaining)
			}
		}
		return
	}

	kb := tui.GetTUIKeybindings()
	switch {
	case kb.Matches(data, tui.KBSelectCancel):
		if in.onEscape != nil {
			in.onEscape()
		}
		return
	case kb.Matches(data, tui.KBEditorUndo):
		in.undo()
		return
	case kb.Matches(data, tui.KBInputSubmit) || data == "\n":
		if in.onSubmit != nil {
			in.onSubmit(in.value)
		}
		return
	case kb.Matches(data, tui.KBEditorDeleteCharBack):
		in.handleBackspace()
		return
	case kb.Matches(data, tui.KBEditorDeleteCharForward):
		in.handleForwardDelete()
		return
	case kb.Matches(data, tui.KBEditorDeleteWordBack):
		in.deleteWordBackwards()
		return
	case kb.Matches(data, tui.KBEditorDeleteWordForward):
		in.deleteWordForward()
		return
	case kb.Matches(data, tui.KBEditorDeleteToLineStart):
		in.deleteToLineStart()
		return
	case kb.Matches(data, tui.KBEditorDeleteToLineEnd):
		in.deleteToLineEnd()
		return
	case kb.Matches(data, tui.KBEditorYank):
		in.yank()
		return
	case kb.Matches(data, tui.KBEditorYankPop):
		in.yankPop()
		return
	case kb.Matches(data, tui.KBEditorCursorLeft):
		in.lastAction = ""
		if in.cursor > 0 {
			in.cursor -= lastGraphemeLen(slice16(in.value, 0, in.cursor))
		}
		return
	case kb.Matches(data, tui.KBEditorCursorRight):
		in.lastAction = ""
		if in.cursor < u16(in.value) {
			in.cursor += firstGraphemeLen(slice16(in.value, in.cursor))
		}
		return
	case kb.Matches(data, tui.KBEditorCursorLineStart):
		in.lastAction = ""
		in.cursor = 0
		return
	case kb.Matches(data, tui.KBEditorCursorLineEnd):
		in.lastAction = ""
		in.cursor = u16(in.value)
		return
	case kb.Matches(data, tui.KBEditorCursorWordLeft):
		in.moveWordBackwards()
		return
	case kb.Matches(data, tui.KBEditorCursorWordRight):
		in.moveWordForwards()
		return
	}

	// Kitty CSI-u printable character (\x1b[97u for 'a'): decoded before the control-character check, whose rule would
	// reject the ESC.
	if printable, ok := tui.DecodeKittyPrintable(data); ok {
		in.insertCharacter(printable)
		return
	}

	// Regular input: printable characters including Unicode; control characters (C0, DEL and C1) are rejected.
	for _, r := range data {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return
		}
	}
	in.insertCharacter(data)
}

func (in *steerInput) insertCharacter(char string) {
	// Undo coalescing: consecutive word characters are one undo unit.
	if isWhitespaceChar(char) || in.lastAction != "type-word" {
		in.pushUndo()
	}
	in.lastAction = "type-word"
	in.value = slice16(in.value, 0, in.cursor) + char + slice16(in.value, in.cursor)
	in.cursor += u16(char)
}

func (in *steerInput) handleBackspace() {
	in.lastAction = ""
	if in.cursor > 0 {
		in.pushUndo()
		n := lastGraphemeLen(slice16(in.value, 0, in.cursor))
		in.value = slice16(in.value, 0, in.cursor-n) + slice16(in.value, in.cursor)
		in.cursor -= n
	}
}

func (in *steerInput) handleForwardDelete() {
	in.lastAction = ""
	if in.cursor < u16(in.value) {
		in.pushUndo()
		n := firstGraphemeLen(slice16(in.value, in.cursor))
		in.value = slice16(in.value, 0, in.cursor) + slice16(in.value, in.cursor+n)
	}
}

func (in *steerInput) deleteToLineStart() {
	if in.cursor == 0 {
		return
	}
	in.pushUndo()
	in.killRing.Push(slice16(in.value, 0, in.cursor), true, in.lastAction == "kill")
	in.lastAction = "kill"
	in.value = slice16(in.value, in.cursor)
	in.cursor = 0
}

func (in *steerInput) deleteToLineEnd() {
	if in.cursor >= u16(in.value) {
		return
	}
	in.pushUndo()
	in.killRing.Push(slice16(in.value, in.cursor), false, in.lastAction == "kill")
	in.lastAction = "kill"
	in.value = slice16(in.value, 0, in.cursor)
}

func (in *steerInput) deleteWordBackwards() {
	if in.cursor == 0 {
		return
	}
	// lastAction is read before the cursor moves (moveWordBackwards resets it).
	wasKill := in.lastAction == "kill"
	in.pushUndo()
	oldCursor := in.cursor
	in.moveWordBackwards()
	deleteFrom := in.cursor
	in.cursor = oldCursor
	in.killRing.Push(slice16(in.value, deleteFrom, in.cursor), true, wasKill)
	in.lastAction = "kill"
	in.value = slice16(in.value, 0, deleteFrom) + slice16(in.value, in.cursor)
	in.cursor = deleteFrom
}

func (in *steerInput) deleteWordForward() {
	if in.cursor >= u16(in.value) {
		return
	}
	wasKill := in.lastAction == "kill"
	in.pushUndo()
	oldCursor := in.cursor
	in.moveWordForwards()
	deleteTo := in.cursor
	in.cursor = oldCursor
	in.killRing.Push(slice16(in.value, in.cursor, deleteTo), false, wasKill)
	in.lastAction = "kill"
	in.value = slice16(in.value, 0, in.cursor) + slice16(in.value, deleteTo)
}

func (in *steerInput) yank() {
	text := in.killRing.Peek()
	if text == "" {
		return
	}
	in.pushUndo()
	in.value = slice16(in.value, 0, in.cursor) + text + slice16(in.value, in.cursor)
	in.cursor += u16(text)
	in.lastAction = "yank"
}

func (in *steerInput) yankPop() {
	if in.lastAction != "yank" || in.killRing.Len() <= 1 {
		return
	}
	in.pushUndo()
	// Delete the text just yanked (still the last entry before the rotation).
	prev := in.killRing.Peek()
	in.value = slice16(in.value, 0, in.cursor-u16(prev)) + slice16(in.value, in.cursor)
	in.cursor -= u16(prev)
	in.killRing.Rotate()
	text := in.killRing.Peek()
	in.value = slice16(in.value, 0, in.cursor) + text + slice16(in.value, in.cursor)
	in.cursor += u16(text)
	in.lastAction = "yank"
}

func (in *steerInput) pushUndo() { in.undoStack = append(in.undoStack, inputState{in.value, in.cursor}) }

func (in *steerInput) undo() {
	if len(in.undoStack) == 0 {
		return
	}
	s := in.undoStack[len(in.undoStack)-1]
	in.undoStack = in.undoStack[:len(in.undoStack)-1]
	in.value, in.cursor = s.value, s.cursor
	in.lastAction = ""
}

func (in *steerInput) moveWordBackwards() {
	if in.cursor == 0 {
		return
	}
	in.lastAction = ""
	in.cursor = tui.FindWordBackward(in.value, in.cursor)
}

func (in *steerInput) moveWordForwards() {
	if in.cursor >= u16(in.value) {
		return
	}
	in.lastAction = ""
	in.cursor = tui.FindWordForward(in.value, in.cursor)
}

// handlePaste inserts pasted text with its line breaks removed and tabs widened to four spaces.
func (in *steerInput) handlePaste(pasted string) {
	in.lastAction = ""
	in.pushUndo()
	clean := strings.NewReplacer("\r\n", "", "\r", "", "\n", "", "\t", "    ").Replace(pasted)
	in.value = slice16(in.value, 0, in.cursor) + clean + slice16(in.value, in.cursor)
	in.cursor += u16(clean)
}

// render is the prompt and the visible window of the value, with the fake cursor (and, focused, the hardware cursor
// marker) at the cursor. (The original's placeholder is not ported: the viewer never sets one.)
func (in *steerInput) render(width int) string {
	availableWidth := width - widthx.VisibleWidth(in.prompt)
	if availableWidth <= 0 {
		return widthx.TruncateToWidth(in.prompt, width, "", false)
	}
	marker := ""
	if in.focused {
		marker = tui.CursorMarker
	}
	visibleText := ""
	cursorDisplay := in.cursor
	in.renderedStartColumn = 0
	totalWidth := widthx.VisibleWidth(in.value)
	if totalWidth < availableWidth {
		visibleText = in.value // everything fits, with room for the cursor at the end
	} else {
		// Horizontal scrolling; one column stays free for the cursor at the end.
		scrollWidth := availableWidth
		if in.cursor == u16(in.value) {
			scrollWidth--
		}
		cursorCol := widthx.VisibleWidth(slice16(in.value, 0, in.cursor))
		if scrollWidth > 0 {
			half := scrollWidth / 2
			var startCol int
			switch {
			case cursorCol < half:
				startCol = 0
			case cursorCol > totalWidth-half:
				startCol = max(0, totalWidth-scrollWidth)
			default:
				startCol = max(0, cursorCol-half)
			}
			in.renderedStartColumn = startCol
			visibleText = widthx.SliceByColumn(in.value, startCol, scrollWidth, true)
			cursorDisplay = u16(widthx.SliceByColumn(in.value, startCol, max(0, cursorCol-startCol), true))
		} else {
			visibleText, cursorDisplay = "", 0
		}
	}

	atCursor := " "
	if rest := slice16(visibleText, cursorDisplay); rest != "" {
		atCursor, _ = widthx.FirstGrapheme(rest)
	}
	before := slice16(visibleText, 0, cursorDisplay)
	after := slice16(visibleText, cursorDisplay+u16(atCursor))
	text := before + marker + tui.RenderFakeCursor(atCursor) + after
	return in.prompt + text + strings.Repeat(" ", max(0, availableWidth-widthx.VisibleWidth(text)))
}
