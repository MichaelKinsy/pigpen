package pi_permission_system

import (
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Port of pi-tui's Input component (packages/tui/src/components/input.ts, Pi 1402973a): the single-line text input with horizontal
// scrolling the denial-reason step is built on. pitui carries no Input (only the multi-line Editor), so it is ported here over
// pitui's own kill ring, key matcher, word navigation and width helpers. Cursor positions count UTF-16 units, as in the original;
// mouse handling is not ported (the permission dialog never routes a mouse event to it).

type lineInputState struct {
	value  string
	cursor int
}

// lineInputAction is the original's lastAction: "" (null), "kill", "yank" or "type-word".
type lineInputAction string

const (
	actionKill     lineInputAction = "kill"
	actionYank     lineInputAction = "yank"
	actionTypeWord lineInputAction = "type-word"
)

// LineInput is the single-line text input. The zero value is not usable: build one with NewLineInput.
type LineInput struct {
	value  string
	cursor int // in UTF-16 units
	prompt string

	placeholder string
	// OnSubmit and OnEscape are the original's onSubmit and onEscape callbacks.
	OnSubmit func(value string)
	OnEscape func()
	// Focused emits the zero-width cursor marker that positions the hardware cursor for IME composition.
	Focused bool

	pasteBuffer string
	isInPaste   bool
	killRing    tui.KillRing
	lastAction  lineInputAction
	undoStack   []lineInputState
	keys        tui.Keybindings
}

// NewLineInput is `new Input()`: the prompt is "> ". The original reads pi-tui's global keybindings manager on every key; nil keys do
// the same here (tui.GetKeybindings, which a host adapter sets with the user's overrides), and a non-nil keys replaces it.
func NewLineInput(keys tui.Keybindings) *LineInput {
	return &LineInput{prompt: "> ", keys: keys}
}

// Value is getValue.
func (in *LineInput) Value() string { return in.value }

// SetValue is setValue.
func (in *LineInput) SetValue(value string) {
	in.value = value
	if n := jsstring.Length(value); in.cursor > n {
		in.cursor = n
	}
}

func (in *LineInput) length() int { return jsstring.Length(in.value) }

func (in *LineInput) head() string { return jsstring.Slice(in.value, 0, in.cursor) }

func (in *LineInput) tail() string { return jsstring.Slice(in.value, in.cursor) }

func (in *LineInput) matches(data string, id tui.TUIKeybinding) bool {
	if in.keys == nil {
		return tui.GetKeybindings().Matches(data, string(id))
	}
	return in.keys.Matches(data, string(id))
}

// lastGraphemeLength is the UTF-16 length of the last grapheme of s, or 1 for the empty string.
func lastGraphemeLength(s string) int {
	last := ""
	for rest := s; rest != ""; {
		cluster, next := widthx.FirstGrapheme(rest)
		if cluster == "" {
			break
		}
		last, rest = cluster, next
	}
	if last == "" {
		return 1
	}
	return jsstring.Length(last)
}

// firstGraphemeLength is the UTF-16 length of the first grapheme of s, or 1 for the empty string.
func firstGraphemeLength(s string) int {
	cluster, _ := widthx.FirstGrapheme(s)
	if cluster == "" {
		return 1
	}
	return jsstring.Length(cluster)
}

// HandleInput is handleInput.
func (in *LineInput) HandleInput(data string) {
	// Bracketed paste mode: start of paste is ESC[200~, end is ESC[201~.
	if strings.Contains(data, "\x1b[200~") {
		in.isInPaste = true
		in.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if in.isInPaste {
		in.pasteBuffer += data
		if endIndex := strings.Index(in.pasteBuffer, "\x1b[201~"); endIndex != -1 {
			pasteContent := in.pasteBuffer[:endIndex]
			in.handlePaste(pasteContent)
			in.isInPaste = false
			remaining := in.pasteBuffer[endIndex+len("\x1b[201~"):]
			in.pasteBuffer = ""
			if remaining != "" {
				in.HandleInput(remaining)
			}
		}
		return
	}

	switch {
	case in.matches(data, tui.KBSelectCancel):
		if in.OnEscape != nil {
			in.OnEscape()
		}
		return
	case in.matches(data, tui.KBEditorUndo):
		in.undo()
		return
	case in.matches(data, tui.KBInputSubmit) || data == "\n":
		if in.OnSubmit != nil {
			in.OnSubmit(in.value)
		}
		return
	case in.matches(data, tui.KBEditorDeleteCharBack):
		in.handleBackspace()
		return
	case in.matches(data, tui.KBEditorDeleteCharForward):
		in.handleForwardDelete()
		return
	case in.matches(data, tui.KBEditorDeleteWordBack):
		in.deleteWordBackwards()
		return
	case in.matches(data, tui.KBEditorDeleteWordForward):
		in.deleteWordForward()
		return
	case in.matches(data, tui.KBEditorDeleteToLineStart):
		in.deleteToLineStart()
		return
	case in.matches(data, tui.KBEditorDeleteToLineEnd):
		in.deleteToLineEnd()
		return
	case in.matches(data, tui.KBEditorYank):
		in.yank()
		return
	case in.matches(data, tui.KBEditorYankPop):
		in.yankPop()
		return
	case in.matches(data, tui.KBEditorCursorLeft):
		in.lastAction = ""
		if in.cursor > 0 {
			in.cursor -= lastGraphemeLength(in.head())
		}
		return
	case in.matches(data, tui.KBEditorCursorRight):
		in.lastAction = ""
		if in.cursor < in.length() {
			in.cursor += firstGraphemeLength(in.tail())
		}
		return
	case in.matches(data, tui.KBEditorCursorLineStart):
		in.lastAction = ""
		in.cursor = 0
		return
	case in.matches(data, tui.KBEditorCursorLineEnd):
		in.lastAction = ""
		in.cursor = in.length()
		return
	case in.matches(data, tui.KBEditorCursorWordLeft):
		in.moveWordBackwards()
		return
	case in.matches(data, tui.KBEditorCursorWordRight):
		in.moveWordForwards()
		return
	}

	// Kitty CSI-u printable character (e.g. ESC[97u for 'a'), decoded before the control-character check since CSI-u sequences
	// contain ESC, which would be rejected.
	if printable, ok := tui.DecodeKittyPrintable(data); ok {
		in.insertCharacter(printable)
		return
	}

	// Regular character input: printable characters including Unicode, but not control characters (C0, DEL, C1).
	for _, r := range data {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return
		}
	}
	in.insertCharacter(data)
}

func isWhitespaceText(s string) bool { return strings.ContainsFunc(s, widthx.IsJSSpace) }

func (in *LineInput) insertCharacter(char string) {
	// Undo coalescing: consecutive word characters coalesce into one undo unit.
	if isWhitespaceText(char) || in.lastAction != actionTypeWord {
		in.pushUndo()
	}
	in.lastAction = actionTypeWord
	in.value = jsstring.Slice(in.value, 0, in.cursor) + char + jsstring.Slice(in.value, in.cursor)
	in.cursor += jsstring.Length(char)
}

func (in *LineInput) handleBackspace() {
	in.lastAction = ""
	if in.cursor > 0 {
		in.pushUndo()
		n := lastGraphemeLength(in.head())
		in.value = jsstring.Slice(in.value, 0, in.cursor-n) + jsstring.Slice(in.value, in.cursor)
		in.cursor -= n
	}
}

func (in *LineInput) handleForwardDelete() {
	in.lastAction = ""
	if in.cursor < in.length() {
		in.pushUndo()
		n := firstGraphemeLength(in.tail())
		in.value = jsstring.Slice(in.value, 0, in.cursor) + jsstring.Slice(in.value, in.cursor+n)
	}
}

func (in *LineInput) deleteToLineStart() {
	if in.cursor == 0 {
		return
	}
	in.pushUndo()
	deleted := in.head()
	in.killRing.Push(deleted, true, in.lastAction == actionKill)
	in.lastAction = actionKill
	in.value = in.tail()
	in.cursor = 0
}

func (in *LineInput) deleteToLineEnd() {
	if in.cursor >= in.length() {
		return
	}
	in.pushUndo()
	deleted := in.tail()
	in.killRing.Push(deleted, false, in.lastAction == actionKill)
	in.lastAction = actionKill
	in.value = in.head()
}

func (in *LineInput) deleteWordBackwards() {
	if in.cursor == 0 {
		return
	}
	// lastAction is read before the cursor movement, which resets it.
	wasKill := in.lastAction == actionKill
	in.pushUndo()
	oldCursor := in.cursor
	in.moveWordBackwards()
	deleteFrom := in.cursor
	in.cursor = oldCursor
	deleted := jsstring.Slice(in.value, deleteFrom, in.cursor)
	in.killRing.Push(deleted, true, wasKill)
	in.lastAction = actionKill
	in.value = jsstring.Slice(in.value, 0, deleteFrom) + jsstring.Slice(in.value, in.cursor)
	in.cursor = deleteFrom
}

func (in *LineInput) deleteWordForward() {
	if in.cursor >= in.length() {
		return
	}
	wasKill := in.lastAction == actionKill
	in.pushUndo()
	oldCursor := in.cursor
	in.moveWordForwards()
	deleteTo := in.cursor
	in.cursor = oldCursor
	deleted := jsstring.Slice(in.value, in.cursor, deleteTo)
	in.killRing.Push(deleted, false, wasKill)
	in.lastAction = actionKill
	in.value = jsstring.Slice(in.value, 0, in.cursor) + jsstring.Slice(in.value, deleteTo)
}

func (in *LineInput) yank() {
	text := in.killRing.Peek()
	if text == "" {
		return
	}
	in.pushUndo()
	in.value = in.head() + text + in.tail()
	in.cursor += jsstring.Length(text)
	in.lastAction = actionYank
}

func (in *LineInput) yankPop() {
	if in.lastAction != actionYank || in.killRing.Length() <= 1 {
		return
	}
	in.pushUndo()
	// Delete the previously yanked text (still at the end of the ring before rotation).
	prev := in.killRing.Peek()
	in.value = jsstring.Slice(in.value, 0, in.cursor-jsstring.Length(prev)) + in.tail()
	in.cursor -= jsstring.Length(prev)
	in.killRing.Rotate()
	text := in.killRing.Peek()
	in.value = in.head() + text + in.tail()
	in.cursor += jsstring.Length(text)
	in.lastAction = actionYank
}

func (in *LineInput) pushUndo() {
	in.undoStack = append(in.undoStack, lineInputState{value: in.value, cursor: in.cursor})
}

func (in *LineInput) undo() {
	if len(in.undoStack) == 0 {
		return
	}
	snapshot := in.undoStack[len(in.undoStack)-1]
	in.undoStack = in.undoStack[:len(in.undoStack)-1]
	in.value, in.cursor = snapshot.value, snapshot.cursor
	in.lastAction = ""
}

func (in *LineInput) moveWordBackwards() {
	if in.cursor == 0 {
		return
	}
	in.lastAction = ""
	in.cursor = tui.FindWordBackward(in.value, in.cursor)
}

func (in *LineInput) moveWordForwards() {
	if in.cursor >= in.length() {
		return
	}
	in.lastAction = ""
	in.cursor = tui.FindWordForward(in.value, in.cursor)
}

func (in *LineInput) handlePaste(pasted string) {
	in.lastAction = ""
	in.pushUndo()
	// Newlines and carriage returns are removed, tabs become four spaces.
	clean := strings.ReplaceAll(pasted, "\r\n", "")
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.ReplaceAll(clean, "\n", "")
	clean = strings.ReplaceAll(clean, "\t", "    ")
	in.value = in.head() + clean + in.tail()
	in.cursor += jsstring.Length(clean)
}

// Render is render: exactly one row, whatever the value's length, scrolled horizontally around the cursor.
func (in *LineInput) Render(width int) []string {
	availableWidth := width - widthx.VisibleWidth(in.prompt)
	if availableWidth <= 0 {
		return []string{widthx.TruncateToWidth(in.prompt, width, "", false)}
	}

	if in.length() == 0 && in.placeholder != "" {
		placeholder := widthx.TruncateToWidth(in.placeholder, availableWidth, "", false)
		atCursor, _ := widthx.FirstGrapheme(placeholder)
		if atCursor == "" {
			atCursor = " "
		}
		afterCursor := jsstring.Slice(placeholder, jsstring.Length(atCursor))
		marker := ""
		if in.Focused {
			marker = widthx.CursorMarker
		}
		textWithCursor := marker + widthx.RenderFakeCursor(atCursor) + afterCursor
		padding := strings.Repeat(" ", max(0, availableWidth-widthx.VisibleWidth(textWithCursor)))
		return []string{in.prompt + textWithCursor + padding}
	}

	visibleText := ""
	cursorDisplay := in.cursor
	totalWidth := widthx.VisibleWidth(in.value)

	if totalWidth < availableWidth {
		// Everything fits (leave room for the cursor at the end).
		visibleText = in.value
	} else {
		// Horizontal scrolling; one column is reserved for the cursor when it is at the end.
		scrollWidth := availableWidth
		if in.cursor == in.length() {
			scrollWidth = availableWidth - 1
		}
		cursorCol := widthx.VisibleWidth(in.head())
		if scrollWidth > 0 {
			halfWidth := scrollWidth / 2
			startCol := 0
			switch {
			case cursorCol < halfWidth:
				startCol = 0
			case cursorCol > totalWidth-halfWidth:
				startCol = max(0, totalWidth-scrollWidth)
			default:
				startCol = max(0, cursorCol-halfWidth)
			}
			visibleText = widthx.SliceByColumn(in.value, startCol, scrollWidth, true)
			beforeCursor := widthx.SliceByColumn(in.value, startCol, max(0, cursorCol-startCol), true)
			cursorDisplay = jsstring.Length(beforeCursor)
		} else {
			visibleText = ""
			cursorDisplay = 0
		}
	}

	atCursor, _ := widthx.FirstGrapheme(jsstring.Slice(visibleText, cursorDisplay))
	if atCursor == "" {
		atCursor = " " // the character at the cursor, or a space at the end
	}
	beforeCursor := jsstring.Slice(visibleText, 0, cursorDisplay)
	afterCursor := jsstring.Slice(visibleText, cursorDisplay+jsstring.Length(atCursor))
	// Hardware cursor marker (zero-width, before the fake cursor, for IME positioning).
	marker := ""
	if in.Focused {
		marker = widthx.CursorMarker
	}
	textWithCursor := beforeCursor + marker + widthx.RenderFakeCursor(atCursor) + afterCursor
	padding := strings.Repeat(" ", max(0, availableWidth-widthx.VisibleWidth(textWithCursor)))
	return []string{in.prompt + textWithCursor + padding}
}
