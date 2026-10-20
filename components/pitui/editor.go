package tui

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/components/pitui/lazyregexp"

	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

var pasteMarkerRegex = lazyregexp.New(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`)
var pasteMarkerSingle = lazyregexp.New(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`)
var pasteCtrlCSIU = lazyregexp.New(`\x1b\[(\d+);5u`)

const attachmentAutocompleteDebounce = 20 * time.Millisecond

// SelectListTheme is pi-tui's SelectListTheme: the styling functions of the
// autocomplete popup. This port has no autocomplete, so the Editor stores but
// never calls them; they exist so callers pass the same theme pi-tui takes.
type SelectListTheme struct {
	SelectedPrefix func(string) string
	SelectedText   func(string) string
	Description    func(string) string
	ScrollInfo     func(string) string
	NoMatch        func(string) string
}

// EditorTheme is pi-tui's EditorTheme: the border colour function and the
// autocomplete SelectList theme.
type EditorTheme struct {
	BorderColor func(string) string
	SelectList  SelectListTheme
}

// Editor is a multi-line text input with undo/kill-ring support. Port of
// pi-tui's Editor without autocomplete.
type Editor struct {
	theme EditorTheme
	// Focused emits widthx.CursorMarker so the TUI can position the hardware cursor for IME candidate windows.
	Focused bool
	lines   []string
	cursor  [2]int // [logical line, UTF-16 column]
	// typed holds the line that typing at its end last produced. strings.Builder never rewrites bytes it has handed out, so the next key
	// typed at the end of that line extends the same buffer instead of copying the line.
	typed      strings.Builder
	jumpMode   string // "forward" | "backward" | ""
	history    []editorState
	killRing   KillRing
	lastAction string // "kill" | "yank" | "type-word" | ""

	preferredVisualCol   *int
	snappedFromCursorCol *int

	paddingX int

	// Submission history for Up/Down arrow navigation.
	// Distinct from the undo history (editorState stack above).
	//
	// inputHistory stores submitted texts, newest first (index 0 = most recent).
	// inputHistIdx is the current browse position: -1 = not browsing.
	// inputHistSaved holds the editor state captured when browsing starts,
	// restored when the user navigates back past index 0.
	inputHistory   []string
	inputHistIdx   int // -1 = live, >=0 = browsing
	inputHistSaved *editorHistoryDraft

	// Bracketed paste state: buffers content between \x1b[200~ and \x1b[201~.
	isInPaste   bool
	pasteBuffer string

	// Compact paste markers. When a paste is >10 lines or >1000 chars, the
	// editor inserts a marker like "[paste #N +K lines]" and stashes the
	// actual content keyed by N. GetExpandedText expands them back.
	pastes       map[int]string
	pasteCounter int

	// renderWidth is the last width passed to Render; used for visual-line movement.
	renderWidth int

	// wrapCache memoizes per-logical-line word wrapping (see wrappedChunks).
	wrapCacheWidth    int
	wrapCacheLines    []string
	wrapCacheChunks   [][]textChunk
	wrapCachePasteIDs map[int]bool
	// visualMap backs the last visual line map; its callers use a map only while they run.
	visualMap []editorVisualLine
	// layoutScratch and visualScratch are Render's per-frame working lists; nothing retains them past the frame.
	layoutScratch []layoutLine
	visualScratch []string

	// scrollOffset + maxVisibleLines: when the visual layout exceeds
	// maxVisibleLines the editor renders a window plus "up/down N more"
	// indicators in the borders. maxVisibleLines defaults to 5; pi-tui derives
	// it as max(5, floor(terminalRows * 0.3)) on every render, which
	// SetRowsProvider reproduces.
	scrollOffset    int
	maxVisibleLines int
	rowsProvider    func() int
	requestRender   func()
	// reported is the text last passed to OnChange (or present when
	// HandleInput began); HandleInput reports a differing final text once.
	reported string

	// Hooks and flags (pi-tui onSubmit / onChange / disableSubmit).
	OnSubmit      func(string)
	OnChange      func(string)
	DisableSubmit bool
}

type editorState struct {
	lines  []string
	cursor [2]int
	// pastes/pasteCounter travel with the snapshot so undo restores the
	// paste registry alongside the text (mirrors upstream EditorSnapshot).
	pastes       map[int]string
	pasteCounter int
}

type editorHistoryDraft struct {
	lines  []string
	cursor [2]int
}

type editorVisualLine struct {
	logicalLine int
	startCol    int
	length      int
}

type textChunk struct {
	text       string
	startIndex int
	endIndex   int
}

type layoutLine struct {
	text      string
	hasCursor bool
	cursorPos int
}

// NewEditor mirrors `new Editor(tui, theme)`; the tui argument only supplied
// terminal rows and requestRender, see SetRowsProvider / SetRequestRender.
func NewEditor(theme EditorTheme) *Editor {
	return &Editor{theme: theme, lines: []string{""}, inputHistIdx: -1, maxVisibleLines: 5, renderWidth: 80, wrapCacheWidth: -1}
}

// Invalidate is a no-op: the Editor keeps no cache that depends on external state (pi-tui Editor.invalidate).
func (e *Editor) Invalidate() {}

// SetRowsProvider makes Render derive the visible-line cap the way pi-tui
// does, max(5, floor(rows * 0.3)), from the terminal's row count.
func (e *Editor) SetRowsProvider(rows func() int) { e.rowsProvider = rows }

// SetRequestRender installs the callback pi-tui's Editor calls through
// tui.requestRender() when its padding changes.
func (e *Editor) SetRequestRender(fn func()) { e.requestRender = fn }

// SetMaxVisibleLines fixes the visible visual-line cap (clamped to >= 1) and
// disables the rows provider.
func (e *Editor) SetMaxVisibleLines(n int) {
	e.rowsProvider = nil
	e.maxVisibleLines = max(1, n)
}

// visibleLineLimit is the window height: pi-tui's max(5, floor(rows*0.3)) when
// a rows provider is installed, the fixed cap otherwise.
func (e *Editor) visibleLineLimit() int {
	if e.rowsProvider != nil {
		return max(5, e.rowsProvider()*3/10)
	}
	return e.maxVisibleLines
}

// MaxVisibleLines returns the current visible-line cap.
func (e *Editor) MaxVisibleLines() int { return e.maxVisibleLines }

// PaddingX returns the editor's horizontal content padding.
func (e *Editor) PaddingX() int { return e.paddingX }

// SetFocused records whether the editor holds TUI focus; only a focused editor emits the hardware-cursor marker.
func (e *Editor) SetFocused(focused bool) { e.Focused = focused }

// SetPaddingX changes the editor's horizontal content padding (negative clamps to 0) and requests a render when it changed.
func (e *Editor) SetPaddingX(padding int) {
	padding = max(0, padding)
	if e.paddingX == padding {
		return
	}
	e.paddingX = padding
	if e.requestRender != nil {
		e.requestRender()
	}
}

// GetText returns the editor content as a single string.
func (e *Editor) GetText() string { return strings.Join(e.lines, "\n") }

// SetText normalizes and replaces the document, resets paste/typing state and
// records a changed document for undo (pi-tui setText).
func (e *Editor) SetText(text string) {
	e.lastAction = ""
	e.exitHistoryBrowsing()
	normalized := normalizeEditorText(text)
	if e.GetText() != normalized {
		e.saveHistory()
	}
	e.pastes = nil
	e.pasteCounter = 0
	e.setTextInternal(normalized, false)
}

func (e *Editor) exitHistoryBrowsing() {
	e.inputHistIdx = -1
	e.inputHistSaved = nil
}

// GetExpandedText returns the editor content with any compact paste
// markers (`[paste #N +K lines]` / `[paste #N M chars]`) expanded back
// to their stored content. Mirrors upstream editor.ts::getExpandedText
// (.upstream/v0.69.0/packages/tui/src/components/editor.ts:929).
func (e *Editor) GetExpandedText() string {
	return e.expandPasteMarkers(e.GetText())
}

// expandPasteMarkers replaces every "[paste #N ...]" marker in text
// with the content stored under id N. Mirrors upstream editor.ts:
// expandPasteMarkers (line 915–921).
func (e *Editor) expandPasteMarkers(text string) string {
	if len(e.pastes) == 0 {
		return text
	}
	// pi-tui iterates the Map in insertion order, which is ascending id order.
	ids := make([]int, 0, len(e.pastes))
	for id := range e.pastes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := text
	for _, id := range ids {
		result = pasteMarkerRegexp(id).ReplaceAllLiteralString(result, e.pastes[id])
	}
	return result
}

// AddToHistory trims JavaScript whitespace and adds a submitted prompt for Up/Down navigation. It skips empty strings and consecutive duplicates and retains at most 100 entries.
func (e *Editor) AddToHistory(text string) {
	trimmed := widthx.JSTrim(text)
	if trimmed == "" {
		return
	}
	if len(e.inputHistory) > 0 && e.inputHistory[0] == trimmed {
		return // no consecutive duplicates
	}
	e.inputHistory = append([]string{trimmed}, e.inputHistory...)
	if len(e.inputHistory) > 100 {
		e.inputHistory = e.inputHistory[:100]
	}
}

// navigateHistory moves through input history.
// direction: -1 = older (Up), +1 = newer (Down).
// Mirrors upstream editor.ts::navigateHistory (line 369-390).
func (e *Editor) navigateHistory(direction int) {
	e.lastAction = ""
	if len(e.inputHistory) == 0 {
		return
	}
	newIdx := e.inputHistIdx - direction // Up(-1) increases idx, Down(+1) decreases
	if newIdx < -1 || newIdx >= len(e.inputHistory) {
		return
	}
	entering := e.inputHistIdx == -1 && newIdx >= 0
	if entering {
		e.saveHistory()
		// Capture current editor state before entering browse mode.
		e.inputHistSaved = &editorHistoryDraft{lines: append([]string(nil), e.lines...), cursor: e.cursor}
	}
	e.inputHistIdx = newIdx
	if e.inputHistIdx == -1 {
		if e.inputHistSaved == nil {
			e.setTextInternal("", false)
			return
		}
		e.lines = append([]string(nil), e.inputHistSaved.lines...)
		e.cursor = e.inputHistSaved.cursor
		e.inputHistSaved = nil
		e.preferredVisualCol = nil
		e.snappedFromCursorCol = nil
		e.scrollOffset = 0
		e.notifyChange()
		return
	}
	e.setTextInternal(e.inputHistory[e.inputHistIdx], direction == -1)
}

// notifyChange reports the current text to OnChange, as upstream's
// setTextInternal and draft restore call onChange.
func (e *Editor) notifyChange() {
	e.reported = e.GetText()
	if e.OnChange != nil {
		e.OnChange(e.reported)
	}
}

// setTextInternal sets text without touching the history browse state (pi-tui
// setTextInternal) and reports the change.
func (e *Editor) setTextInternal(text string, cursorAtStart bool) {
	e.lines = strings.Split(text, "\n")
	if len(e.lines) == 0 {
		e.lines = []string{""}
	}
	if cursorAtStart {
		e.cursor[0] = 0
		e.setCursorCol(0)
	} else {
		e.cursor[0] = len(e.lines) - 1
		e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
	}
	e.scrollOffset = 0
	e.notifyChange()
}

func (e *Editor) setCursorCol(col int) {
	e.cursor[1] = col
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
}

func isPasteMarker(segment string) bool {
	return len(segment) >= 10 && pasteMarkerSingle.MatchString(segment)
}

func (e *Editor) validPasteIDs() map[int]bool {
	ids := make(map[int]bool, len(e.pastes))
	for id := range e.pastes {
		ids[id] = true
	}
	return ids
}

func wordWrapLine(line string, maxWidth int, preSegmented []editorSegment) []textChunk {
	if line == "" || maxWidth <= 0 {
		return []textChunk{{text: "", startIndex: 0, endIndex: 0}}
	}
	length := jsstring.Length(line)
	if widthx.VisibleWidth(line) <= maxWidth {
		return []textChunk{{text: line, startIndex: 0, endIndex: length}}
	}
	segments := preSegmented
	if segments == nil {
		segments = editorSegments(graphemeSegmentData(line), line)
	}
	// Decode once: wrapping many chunks must not repeatedly scan the entire logical line.
	units := jsstring.ToUTF16(line)
	chunk := func(start, end int) textChunk {
		return textChunk{text: jsstring.FromUTF16(units[start:end]), startIndex: start, endIndex: end}
	}
	chunks := make([]textChunk, 0, len(segments))
	currentWidth, chunkStart := 0, 0
	wrapOppIndex, wrapOppWidth := -1, 0
	for i, seg := range segments {
		gWidth, charIndex := seg.Width, seg.Start
		isWs := !isPasteMarker(seg.Text) && isWhitespaceChar(seg.Text)
		if currentWidth+gWidth > maxWidth {
			if wrapOppIndex >= 0 && currentWidth-wrapOppWidth+gWidth <= maxWidth {
				chunks = append(chunks, chunk(chunkStart, wrapOppIndex))
				chunkStart = wrapOppIndex
				currentWidth -= wrapOppWidth
			} else if chunkStart < charIndex {
				chunks = append(chunks, chunk(chunkStart, charIndex))
				chunkStart, currentWidth = charIndex, 0
			}
			wrapOppIndex = -1
		}
		if gWidth > maxWidth {
			// pig divergence (D159): a single grapheme wider than maxWidth (a tab at width 2, a wide character at width 1) stays one chunk. Pi re-wraps it with the same input and exhausts the stack.
			subSegments := editorSegments(graphemeSegmentData(seg.Text), seg.Text)
			var subChunks []textChunk
			if len(subSegments) <= 1 {
				subChunks = []textChunk{{text: seg.Text, startIndex: 0, endIndex: jsstring.Length(seg.Text)}}
			} else {
				subChunks = wordWrapLine(seg.Text, maxWidth, subSegments)
			}
			for j := range len(subChunks) - 1 {
				sc := subChunks[j]
				chunks = append(chunks, textChunk{text: sc.text, startIndex: charIndex + sc.startIndex, endIndex: charIndex + sc.endIndex})
			}
			last := subChunks[len(subChunks)-1]
			chunkStart = charIndex + last.startIndex
			currentWidth = widthx.VisibleWidth(last.text)
			wrapOppIndex = -1
			continue
		}
		currentWidth += gWidth
		if i+1 < len(segments) {
			next := segments[i+1]
			if isWs && (isPasteMarker(next.Text) || !isWhitespaceChar(next.Text)) {
				wrapOppIndex, wrapOppWidth = next.Start, currentWidth
			} else if !isWs && !isWhitespaceChar(next.Text) {
				isCJK := !isPasteMarker(seg.Text) && widthx.IsCJKBreak(seg.Text)
				nextIsCJK := !isPasteMarker(next.Text) && widthx.IsCJKBreak(next.Text)
				if isCJK || nextIsCJK {
					wrapOppIndex, wrapOppWidth = next.Start, currentWidth
				}
			}
		}
	}
	return append(chunks, chunk(chunkStart, length))
}

// isEditorEmpty returns true when the editor contains only a single empty line.
func (e *Editor) isEditorEmpty() bool {
	return len(e.lines) == 1 && e.lines[0] == ""
}

func (e *Editor) saveHistory() {
	state := editorState{
		lines:        make([]string, len(e.lines)),
		cursor:       e.cursor,
		pastes:       clonePastes(e.pastes),
		pasteCounter: e.pasteCounter,
	}
	copy(state.lines, e.lines)
	e.history = append(e.history, state)
}

// clonePastes returns a shallow copy of a paste registry so an undo snapshot
// is not mutated by later edits. Returns nil for an empty/nil registry.
func clonePastes(m map[int]string) map[int]string {
	if len(m) == 0 {
		return nil
	}
	c := make(map[int]string, len(m))
	maps.Copy(c, m)
	return c
}

// wrappedChunks returns the word-wrapped textChunk list for every logical line
// at the given width, memoizing per line. A line whose content is unchanged
// from the previous call at the same width and with the same valid paste IDs reuses its cached chunks instead of
// re-running wordWrapLine (grapheme segmentation + wrapping). Output is
// identical to calling e.wrapLine(line, width) for each
// line; the reuse is invisible. The comparison is by content, so SetText
// rebuilding the buffer still reuses chunks for lines whose text is unchanged.
func (e *Editor) wrappedChunks(width int) [][]textChunk {
	if width < 1 {
		width = 1
	}
	reuse := e.wrapCacheWidth == width && len(e.wrapCachePasteIDs) == len(e.pastes)
	if reuse {
		for id := range e.wrapCachePasteIDs {
			if _, ok := e.pastes[id]; !ok {
				reuse = false
				break
			}
		}
	}
	if !reuse || len(e.wrapCacheLines) != len(e.lines) {
		lines := make([]string, len(e.lines))
		chunks := make([][]textChunk, len(e.lines))
		for i, line := range e.lines {
			lines[i] = line
			if reuse && i < len(e.wrapCacheLines) && e.wrapCacheLines[i] == line {
				chunks[i] = e.wrapCacheChunks[i]
				continue
			}
			chunks[i] = e.wrapLine(line, width)
		}
		e.wrapCacheLines, e.wrapCacheChunks = lines, chunks
		e.wrapCacheWidth = width
		if !reuse {
			e.wrapCachePasteIDs = e.validPasteIDs()
		}
		return e.wrapCacheChunks
	}
	for i, line := range e.lines {
		if e.wrapCacheLines[i] != line {
			e.wrapCacheLines[i] = line
			e.wrapCacheChunks[i] = e.wrapLine(line, width)
		}
	}
	return e.wrapCacheChunks
}

// wrapLine is wordWrapLine(line, width, e.segmentLine(line)). A line that fits is one chunk, so it is not segmented.
func (e *Editor) wrapLine(line string, width int) []textChunk {
	if line == "" || width <= 0 || widthx.VisibleWidth(line) <= width {
		return wordWrapLine(line, width, nil)
	}
	return wordWrapLine(line, width, e.segmentLine(line))
}

// layoutText walks logical lines through the word-wrap cache and applies the
// cursor decoration to the appropriate visual chunk, returning the flat list
// of visual lines that Render emits between the top and bottom borders, before
// scroll-windowing. Mirrors upstream editor.ts::layoutText (line 823) folded
// with the cursor-splice loop in render (line 467-510).
func (e *Editor) layoutText(contentWidth int) []layoutLine {
	if len(e.lines) == 0 || (len(e.lines) == 1 && e.lines[0] == "") {
		return []layoutLine{{text: "", hasCursor: true, cursorPos: 0}}
	}

	// wordWrapLine returns a single chunk (startIndex 0) for a line that fits,
	// so the chunk loop below reproduces the former short-line fast path
	// exactly while letting wrappedChunks memoize the expensive wrapping.
	layoutLines := e.layoutScratch[:0]
	defer func() { e.layoutScratch = layoutLines[:0] }()
	for i, chunks := range e.wrappedChunks(contentWidth) {
		isCurrentLine := i == e.cursor[0]
		for ci, chunk := range chunks {
			cursorPos := e.cursor[1]
			isLastChunk := ci == len(chunks)-1
			hasCursorInChunk := false
			adjustedCursorPos := 0
			if isCurrentLine {
				if isLastChunk {
					hasCursorInChunk = cursorPos >= chunk.startIndex
					adjustedCursorPos = cursorPos - chunk.startIndex
				} else {
					hasCursorInChunk = cursorPos >= chunk.startIndex && cursorPos < chunk.endIndex
					if hasCursorInChunk {
						adjustedCursorPos = min(cursorPos-chunk.startIndex, jsstring.Length(chunk.text))
					}
				}
			}
			layoutLines = append(layoutLines, layoutLine{text: chunk.text, hasCursor: hasCursorInChunk, cursorPos: adjustedCursorPos})
		}
	}

	return layoutLines
}

func (e *Editor) buildVisualLineMap(width int) []editorVisualLine {
	if width < 1 {
		width = 1
	}
	if len(e.lines) == 0 {
		return []editorVisualLine{{logicalLine: 0, startCol: 0, length: 0}}
	}
	chunked := e.wrappedChunks(width)
	count := 0
	for _, chunks := range chunked {
		count += len(chunks)
	}
	if count == 0 {
		return []editorVisualLine{{logicalLine: 0, startCol: 0, length: 0}}
	}
	out := e.visualMap[:0]
	if cap(out) < count {
		out = make([]editorVisualLine, 0, count)
	}
	for i, chunks := range chunked {
		for _, chunk := range chunks {
			out = append(out, editorVisualLine{logicalLine: i, startCol: chunk.startIndex, length: jsstring.Length(chunk.text)})
		}
	}
	e.visualMap = out
	return out
}

func (e *Editor) findVisualLineAt(visual []editorVisualLine, line, col int) int {
	for i, vl := range visual {
		if vl.logicalLine != line {
			continue
		}
		offset := col - vl.startCol
		isLastSegment := i == len(visual)-1 || visual[i+1].logicalLine != vl.logicalLine
		if offset >= 0 && (offset < vl.length || (isLastSegment && offset == vl.length)) {
			return i
		}
	}
	return max(0, len(visual)-1)
}

func (e *Editor) findCurrentVisualLine(visual []editorVisualLine) int {
	return e.findVisualLineAt(visual, e.cursor[0], e.cursor[1])
}

func (e *Editor) isOnFirstVisualLine() bool {
	visual := e.buildVisualLineMap(e.renderWidth)
	return e.findCurrentVisualLine(visual) == 0
}

func (e *Editor) isOnLastVisualLine() bool {
	visual := e.buildVisualLineMap(e.renderWidth)
	return e.findCurrentVisualLine(visual) == len(visual)-1
}

func createScrollBorder(arrow string, n, width int) string {
	width = max(0, width)
	label := fmt.Sprintf(" %s %d more ", arrow, n)
	labelWidth := widthx.VisibleWidth(label)
	if labelWidth+2 <= width {
		left := (width - labelWidth) / 2
		return strings.Repeat("─", left) + label + strings.Repeat("─", width-left-labelWidth)
	}
	indicator := fmt.Sprintf("─── %s %d more ", arrow, n)
	remaining := width - widthx.VisibleWidth(indicator)
	if remaining >= 0 {
		return indicator + strings.Repeat("─", remaining)
	}
	ellipsis := "..."[:min(3, width)]
	return widthx.SliceByColumn(indicator, 0, width-len(ellipsis), true) + ellipsis
}

func (e *Editor) renderTopBorder(width, hidden int) string {
	if hidden > 0 {
		return e.theme.BorderColor(createScrollBorder("↑", hidden, width))
	}
	return e.theme.BorderColor(strings.Repeat("─", width))
}

func (e *Editor) renderBottomBorder(width, hidden int) string {
	if hidden > 0 {
		return e.theme.BorderColor(createScrollBorder("↓", hidden, width))
	}
	return e.theme.BorderColor(strings.Repeat("─", width))
}

// Render mirrors pi-tui Editor.render: a border line, the visible window of
// the word-wrapped text with the cursor decorated, and a border line.
func (e *Editor) Render(width int) []string {
	maxPadding := max(0, (width-1)/2)
	paddingX := min(e.paddingX, maxPadding)
	contentWidth := max(1, width-paddingX*2)
	// With padding the cursor can overflow into it, without padding one
	// column is reserved for the cursor.
	layoutWidth := contentWidth
	if paddingX == 0 {
		layoutWidth = max(1, contentWidth-1)
	}
	e.renderWidth = layoutWidth
	layoutLines := e.layoutText(layoutWidth)

	maxVisibleLines := e.visibleLineLimit()

	cursorLineIndex := 0
	for i, line := range layoutLines {
		if line.hasCursor {
			cursorLineIndex = i
			break
		}
	}
	if cursorLineIndex < e.scrollOffset {
		e.scrollOffset = cursorLineIndex
	} else if cursorLineIndex >= e.scrollOffset+maxVisibleLines {
		e.scrollOffset = cursorLineIndex - maxVisibleLines + 1
	}
	maxScrollOffset := max(0, len(layoutLines)-maxVisibleLines)
	e.scrollOffset = max(0, min(e.scrollOffset, maxScrollOffset))

	visible := layoutLines[e.scrollOffset:min(e.scrollOffset+maxVisibleLines, len(layoutLines))]

	leftPadding := strings.Repeat(" ", paddingX)
	rightPadding := leftPadding
	result := []string{e.renderTopBorder(width, e.scrollOffset)}
	// Hardware cursor marker (zero-width) so the TUI can position the terminal
	// cursor for IME candidate-window placement.
	emitCursorMarker := e.Focused
	for _, layoutLine := range visible {
		displayText := layoutLine.text
		lineVisibleWidth := widthx.VisibleWidth(layoutLine.text)
		cursorInPadding := false
		if layoutLine.hasCursor {
			before := jsstring.Slice(displayText, 0, layoutLine.cursorPos)
			after := jsstring.Slice(displayText, layoutLine.cursorPos, jsstring.Length(displayText))
			marker := ""
			if emitCursorMarker {
				marker = widthx.CursorMarker
			}
			if after != "" {
				// The cursor sits on a grapheme: replace it with the highlighted version.
				firstGrapheme := ""
				if segs := e.segmentLine(after); len(segs) > 0 {
					firstGrapheme = segs[0].Text
				}
				restAfter := jsstring.Slice(after, jsstring.Length(firstGrapheme), jsstring.Length(after))
				displayText = before + marker + RenderFakeCursor(firstGrapheme) + restAfter
			} else {
				displayText = before + marker + RenderFakeCursor(" ")
				lineVisibleWidth++
				if lineVisibleWidth > contentWidth && paddingX > 0 {
					cursorInPadding = true
				}
			}
		}
		padding := strings.Repeat(" ", max(0, contentWidth-lineVisibleWidth))
		lineRightPadding := rightPadding
		if cursorInPadding {
			lineRightPadding = rightPadding[1:]
		}
		result = append(result, leftPadding+displayText+padding+lineRightPadding)
	}
	linesBelow := len(layoutLines) - (e.scrollOffset + len(visible))
	result = append(result, e.renderBottomBorder(width, linesBelow))
	return result
}

// pasteMarkerRegexp returns the regexp matching the marker
// "[paste #<id>]" plus the optional " +K lines" / " M chars" suffix
// produced by handlePasteFlush. Mirrors upstream's literal regex in
// expandPasteMarkers.
func pasteMarkerRegexp(id int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`\[paste #%d( (\+\d+ lines|\d+ chars))?\]`, id))
}

// handlePasteFlush inserts a buffered bracketed paste. Large pastes
// (>10 lines or >1000 chars) are replaced with a compact marker and
// stashed in e.pastes for later expansion via GetExpandedText.
// Mirrors upstream editor.ts::handlePaste (line 1084).
func (e *Editor) handlePasteFlush(buf string) {
	if buf == "" {
		return
	}
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	e.lastAction = ""
	e.saveHistory()
	// Some terminals re-encode control bytes inside bracketed paste as
	// CSI-u Ctrl+<letter> sequences (ESC [ <codepoint> ; 5 u). Decode
	// those back to their literal byte before normalization so Ctrl+J
	// becomes a real newline rather than leaking "[106;5u" text.
	decoded := pasteCtrlCSIU.ReplaceAllStringFunc(buf, func(seq string) string {
		match := pasteCtrlCSIU.FindStringSubmatch(seq)
		if len(match) != 2 {
			return seq
		}
		cp, err := strconv.Atoi(match[1])
		if err != nil {
			return seq
		}
		switch {
		case cp >= 97 && cp <= 122:
			return string(rune(cp - 96))
		case cp >= 65 && cp <= 90:
			return string(rune(cp - 64))
		default:
			return seq
		}
	})
	// Normalize line endings and tabs the same way upstream
	// normalizeText does (\r\n / \r → \n, tab → 4 spaces).
	clean := normalizeEditorText(decoded)
	// Filter JavaScript code units so lone surrogates survive paste cleaning.
	units := jsstring.ToUTF16(clean)
	filtered := units[:0]
	for _, unit := range units {
		if unit == '\n' || unit >= 32 {
			filtered = append(filtered, unit)
		}
	}
	text := jsstring.FromUTF16(filtered)
	if text == "" {
		return
	}
	if strings.ContainsRune("/~.", rune(text[0])) && e.cursor[0] >= 0 && e.cursor[0] < len(e.lines) {
		line := e.lines[e.cursor[0]]
		cursor := min(max(e.cursor[1], 0), jsstring.Length(line))
		if cursor > 0 {
			before, _ := jsstring.DecodeRuneInString(jsstring.Slice(line, cursor-1, cursor))
			if isJSWordChar(before) {
				text = " " + text
			}
		}
	}
	lineCount := strings.Count(text, "\n") + 1
	charCount := jsStringLength(text)
	if lineCount > 10 || charCount > 1000 {
		if e.pastes == nil {
			e.pastes = make(map[int]string)
		}
		e.pasteCounter++
		id := e.pasteCounter
		e.pastes[id] = text
		var marker string
		if lineCount > 10 {
			marker = fmt.Sprintf("[paste #%d +%d lines]", id, lineCount)
		} else {
			marker = fmt.Sprintf("[paste #%d %d chars]", id, charCount)
		}
		e.insert(marker)
		e.notifyChange()
		return
	}
	e.insert(text)
	e.notifyChange()
}

func jsStringLength(text string) int { return jsstring.Length(text) }

func isJSWordChar(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

// HandleInput processes one chunk of raw terminal input. The dispatch order is
// pi-tui Editor.handleInput's.
func (e *Editor) HandleInput(data string) {
	kb := GetTUIKeybindings()
	beforeText := e.GetText()
	e.reported = beforeText
	defer func() {
		if text := e.GetText(); e.OnChange != nil && text != e.reported {
			e.reported = text
			e.OnChange(text)
		}
	}()

	// Character jump mode (awaiting the next character to jump to).
	if e.jumpMode != "" {
		// Cancel if the hotkey is pressed again.
		if kb.Matches(data, KBEditorJumpForward) || kb.Matches(data, KBEditorJumpBackward) {
			e.jumpMode = ""
			return
		}
		printable, ok := DecodePrintableKey(data)
		if !ok && len(data) > 0 && data[0] >= 32 {
			printable, ok = data, true
		}
		if ok {
			direction := e.jumpMode
			e.jumpMode = ""
			e.jumpToChar(printable, direction)
			return
		}
		// Control character: cancel and fall through to normal handling.
		e.jumpMode = ""
	}

	// Bracketed paste mode.
	if strings.Contains(data, "\x1b[200~") {
		e.isInPaste = true
		e.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if e.isInPaste {
		e.pasteBuffer += data
		if endIndex := strings.Index(e.pasteBuffer, "\x1b[201~"); endIndex >= 0 {
			pasteContent := e.pasteBuffer[:endIndex]
			if pasteContent != "" {
				e.handlePasteFlush(pasteContent)
			}
			e.isInPaste = false
			remaining := e.pasteBuffer[endIndex+len("\x1b[201~"):]
			e.pasteBuffer = ""
			if remaining != "" {
				e.HandleInput(remaining)
			}
		}
		return
	}

	// Ctrl+C: let the parent handle (exit/clear).
	if kb.Matches(data, KBInputCopy) {
		return
	}
	if kb.Matches(data, KBEditorUndo) {
		e.lastAction = ""
		e.undo()
		return
	}
	// Tab triggers completion in pi-tui; with no autocomplete provider it is a no-op.
	if kb.Matches(data, KBInputTab) {
		return
	}

	// Deletion actions.
	switch {
	case kb.Matches(data, KBEditorDeleteToLineEnd):
		e.deleteToLineEnd()
		return
	case kb.Matches(data, KBEditorDeleteToLineStart):
		e.deleteToLineStart()
		return
	case kb.Matches(data, KBEditorDeleteWordBack):
		e.deleteWordBackward()
		return
	case kb.Matches(data, KBEditorDeleteWordForward):
		e.deleteWordForward()
		return
	case kb.Matches(data, KBEditorDeleteCharBack) || MatchesKey(data, "shift+backspace"):
		e.backspace()
		return
	case kb.Matches(data, KBEditorDeleteCharForward) || MatchesKey(data, "shift+delete"):
		e.forwardDelete()
		return
	}

	// Kill ring actions.
	if kb.Matches(data, KBEditorYank) {
		e.yank()
		return
	}
	if kb.Matches(data, KBEditorYankPop) {
		e.yankPop()
		return
	}

	// Dedicated history actions always browse entries instead of moving the cursor.
	if kb.Matches(data, KBEditorHistoryPrevious) {
		e.navigateHistory(-1)
		return
	}
	if kb.Matches(data, KBEditorHistoryNext) {
		e.navigateHistory(1)
		return
	}

	// Cursor movement actions.
	switch {
	case kb.Matches(data, KBEditorCursorLineStart):
		e.moveToLineStart()
		return
	case kb.Matches(data, KBEditorCursorLineEnd):
		e.moveToLineEnd()
		return
	case kb.Matches(data, KBEditorCursorWordLeft):
		e.cursorWordBackward()
		return
	case kb.Matches(data, KBEditorCursorWordRight):
		e.cursorWordForward()
		return
	}

	// New line.
	if matchesEditorNewLine(data, kb) {
		if e.shouldSubmitOnBackslashEnter(data, kb) {
			e.backspace()
			e.submitValue()
			return
		}
		e.insertNewline()
		return
	}

	// Submit (Enter).
	if kb.Matches(data, KBInputSubmit) {
		if e.DisableSubmit {
			return
		}
		// Workaround for terminals without Shift+Enter support: a backslash
		// before the cursor is replaced by a newline instead of submitting.
		line := e.lines[e.cursor[0]]
		if e.cursor[1] > 0 && jsstring.Slice(line, e.cursor[1]-1, e.cursor[1]) == "\\" {
			e.backspace()
			e.insertNewline()
			return
		}
		e.submitValue()
		return
	}

	// Arrow key navigation (with history support).
	switch {
	case kb.Matches(data, KBEditorCursorUp):
		switch {
		case e.isOnFirstVisualLine() && (e.isEditorEmpty() || e.inputHistIdx > -1 || e.cursor[1] == 0):
			e.navigateHistory(-1)
		case e.isOnFirstVisualLine():
			e.moveToLineStart() // already at top: jump to start of line
		default:
			e.moveCursor(-1, 0)
		}
		return
	case kb.Matches(data, KBEditorCursorDown):
		switch {
		case e.inputHistIdx > -1 && e.isOnLastVisualLine():
			e.navigateHistory(1)
		case e.isOnLastVisualLine():
			e.moveToLineEnd() // already at bottom: jump to end of line
		default:
			e.moveCursor(1, 0)
		}
		return
	case kb.Matches(data, KBEditorCursorRight):
		e.moveCursor(0, 1)
		return
	case kb.Matches(data, KBEditorCursorLeft):
		e.moveCursor(0, -1)
		return
	case kb.Matches(data, KBEditorPageUp):
		e.pageScroll(-1)
		return
	case kb.Matches(data, KBEditorPageDown):
		e.pageScroll(1)
		return
	case kb.Matches(data, KBEditorJumpForward):
		e.jumpMode = "forward"
		return
	case kb.Matches(data, KBEditorJumpBackward):
		e.jumpMode = "backward"
		return
	}

	// Shift+Space inserts a regular space.
	if MatchesKey(data, "shift+space") {
		e.insertCharacter(" ")
		return
	}
	if printable, ok := DecodePrintableKey(data); ok {
		e.insertCharacter(printable)
		return
	}
	// Regular characters.
	if len(data) > 0 && data[0] >= 32 {
		e.insertCharacter(data)
	}
}

func (e *Editor) moveToLineStart() {
	e.lastAction = ""
	e.setCursorCol(0)
}

func (e *Editor) moveToLineEnd() {
	e.lastAction = ""
	e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
}

func (e *Editor) yank() {
	if e.killRing.Len() == 0 {
		return
	}
	e.saveHistory()
	e.insert(e.killRing.Peek())
	e.lastAction = "yank"
}

func (e *Editor) yankPop() {
	if e.lastAction != "yank" || e.killRing.Len() <= 1 {
		return
	}
	e.saveHistory()
	e.deleteYankedText()
	e.killRing.Rotate()
	e.insert(e.killRing.Peek())
	e.lastAction = "yank"
}

func matchesEditorNewLine(data string, kb Keybindings) bool {
	return kb.Matches(data, KBInputNewLine) ||
		len(data) > 1 && data[0] == '\n' ||
		data == "\x1b\r" ||
		data == "\x1b[13;2~" ||
		len(data) > 1 && strings.Contains(data, "\x1b") && strings.Contains(data, "\r") ||
		data == "\n"
}

func (e *Editor) shouldSubmitOnBackslashEnter(data string, kb *TUIKeybindingsManager) bool {
	if e.DisableSubmit || !MatchesKey(data, "enter") {
		return false
	}
	submitKeys := kb.GetKeys(KBInputSubmit)
	hasShiftEnter := slices.Contains(submitKeys, "shift+enter") || slices.Contains(submitKeys, "shift+return")
	if !hasShiftEnter {
		return false
	}
	line := e.lines[e.cursor[0]]
	return e.cursor[1] > 0 && jsstring.Slice(line, e.cursor[1]-1, e.cursor[1]) == "\\"
}

func (e *Editor) submitValue() {
	result := widthx.JSTrim(e.GetExpandedText())
	e.lines = []string{""}
	e.cursor = [2]int{0, 0}
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
	e.pastes = nil
	e.pasteCounter = 0
	e.exitHistoryBrowsing()
	e.scrollOffset = 0
	e.history = nil
	e.lastAction = ""
	e.notifyChange()
	if e.OnSubmit != nil {
		e.OnSubmit(result)
	}
}

func (e *Editor) insertCharacter(char string) {
	defer e.notifyChange()
	if isWhitespaceChar(char) || e.lastAction != "type-word" {
		e.saveHistory()
	}
	e.lastAction = "type-word"
	e.insert(char)
}

func normalizeEditorText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return jsstring.Canonical(strings.ReplaceAll(text, "\t", "    "))
}

func (e *Editor) insert(s string) {
	if s == "" {
		return
	}
	// Handle multi-line inserts (e.g. from paste or programmatic API).
	if strings.Contains(s, "\n") {
		e.insertMultiLine(s)
		return
	}
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	e.lines[l] = e.spliceTyped(line, c, s)
	e.setCursorCol(c + jsstring.Length(s))
	e.inputHistIdx = -1 // typing exits history-browse mode
	e.inputHistSaved = nil
	e.Invalidate()
}

// spliceTyped is jsstring.Splice(line, c, c, s). At the end of a valid UTF-8 line the result is line+s, built in e.typed so that a run of keys
// typed at the end of one line copies the line once rather than once per key.
func (e *Editor) spliceTyped(line string, c int, s string) string {
	if line == "" {
		return s
	}
	if !utf8.ValidString(line) || !utf8.ValidString(s) || jsstring.Length(line) != c {
		return jsstring.Splice(line, c, c, s)
	}
	if e.typed.Len() != len(line) || e.typed.String() != line {
		e.typed = strings.Builder{}
		e.typed.Grow(2*(len(line)+len(s)) + 16)
		e.typed.WriteString(line)
	}
	e.typed.WriteString(s)
	return e.typed.String()
}

// insertMultiLine inserts text that may contain newlines.
func (e *Editor) insertMultiLine(s string) {
	parts := strings.Split(s, "\n")
	l, c := e.cursor[0], e.cursor[1]
	line := e.lines[l]
	before, after := jsstring.Slice(line, 0, c), jsstring.Slice(line, c)
	lastCol := jsstring.Length(parts[len(parts)-1])
	parts[0] = jsstring.Canonical(before + parts[0])
	parts[len(parts)-1] = jsstring.Canonical(parts[len(parts)-1] + after)
	e.lines = slices.Concat(e.lines[:l], parts, e.lines[l+1:])
	e.cursor[0] = l + len(parts) - 1
	e.setCursorCol(lastCol)
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	e.Invalidate()
}

// InsertTextAtCursor inserts normalized single- or multi-line text as one undoable edit.
func (e *Editor) InsertTextAtCursor(text string) {
	if text == "" {
		return
	}
	e.saveHistory()
	e.lastAction = ""
	e.insert(normalizeEditorText(text))
	e.notifyChange()
}

func (e *Editor) insertNewline() {
	defer e.notifyChange()
	e.saveHistory()
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	before := jsstring.Slice(line, 0, c)
	after := jsstring.Slice(line, c)
	e.lines = append(e.lines[:l+1], append([]string{after}, e.lines[l+1:]...)...)
	e.lines[l] = before
	e.cursor[0]++
	e.setCursorCol(0)
	e.Invalidate()
}

func (e *Editor) backspace() {
	defer e.notifyChange()
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	if c > 0 || l > 0 {
		e.saveHistory()
	}
	if c > 0 {
		line := e.lines[l]
		// If the segment immediately before the cursor is a paste marker,
		// delete the whole marker atomically and compact the paste registry,
		// mirroring upstream editor.ts backspace. Otherwise delete one grapheme.
		last, ok := e.lastSegment(jsstring.Slice(line, 0, c))
		if ok && isPasteMarker(last.Text) {
			marker := last
			e.lines[l] = jsstring.Splice(line, marker.Start, c, "")
			e.cursor[1] = marker.Start
			if m := pasteMarkerSingle.FindStringSubmatch(marker.Text); m != nil {
				if targetID, err := strconv.Atoi(m[1]); err == nil {
					e.removePasteFromRegistry(targetID)
				}
			}
		} else {
			start := 0
			if ok {
				start = last.Start
			}
			e.lines[l] = jsstring.Splice(line, start, c, "")
			e.cursor[1] = start
		}
	} else if l > 0 {
		prev := e.lines[l-1]
		e.cursor[1] = jsstring.Length(prev)
		e.lines[l-1] = jsstring.Canonical(prev + e.lines[l])
		e.lines = append(e.lines[:l], e.lines[l+1:]...)
		e.cursor[0]--
	}
	if c > 0 || l > 0 {
		e.setCursorCol(e.cursor[1])
	}
	e.Invalidate()
}

// removePasteFromRegistry deletes targetID from the paste registry, shifts
// every higher id down by one, and renumbers the surviving [paste #N] markers
// in the buffer text so ids stay contiguous. Mirrors upstream editor.ts
// backspace registry compaction (deleting [paste #1] renumbers [paste #2] to
// [paste #1], and so on, independent of marker order in the text).
func (e *Editor) removePasteFromRegistry(targetID int) {
	if e.pastes != nil {
		delete(e.pastes, targetID)
		higher := make([]int, 0, len(e.pastes))
		for id := range e.pastes {
			if id > targetID {
				higher = append(higher, id)
			}
		}
		slices.Sort(higher)
		for _, id := range higher {
			e.pastes[id-1] = e.pastes[id]
			delete(e.pastes, id)
		}
	}
	e.pasteCounter--
	for i, ln := range e.lines {
		e.lines[i] = pasteMarkerRegex.ReplaceAllStringFunc(ln, func(marker string) string {
			m := pasteMarkerRegex.FindStringSubmatch(marker)
			if m == nil {
				return marker
			}
			id, err := strconv.Atoi(m[1])
			if err != nil || id <= targetID {
				return marker
			}
			return "[paste #" + strconv.Itoa(id-1) + m[2] + "]"
		})
	}
}

// forwardDelete deletes the character at the cursor (Delete key / Ctrl+D).
// Mirrors upstream editor.ts::forwardDelete.
func (e *Editor) forwardDelete() {
	defer e.notifyChange()
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	if c < jsstring.Length(line) || l < len(e.lines)-1 {
		e.saveHistory()
	}
	if c < jsstring.Length(line) {
		end := e.nextSegmentEnd(line, c)
		e.lines[l] = jsstring.Splice(line, c, end, "")
	} else if l < len(e.lines)-1 {
		// Join with next line.
		e.lines[l] = jsstring.Canonical(line + e.lines[l+1])
		e.lines = append(e.lines[:l+1], e.lines[l+2:]...)
	}
	e.Invalidate()
}

func (e *Editor) moveToVisualLine(visual []editorVisualLine, currentVisualLine, targetVisualLine int) {
	if currentVisualLine < 0 || currentVisualLine >= len(visual) || targetVisualLine < 0 || targetVisualLine >= len(visual) {
		return
	}
	currentVL := visual[currentVisualLine]
	targetVL := visual[targetVisualLine]
	currentVisualCol := e.cursor[1] - currentVL.startCol
	if e.snappedFromCursorCol != nil {
		vlIndex := e.findVisualLineAt(visual, currentVL.logicalLine, *e.snappedFromCursorCol)
		currentVisualCol = *e.snappedFromCursorCol - visual[vlIndex].startCol
	}

	isLastSourceSegment := currentVisualLine == len(visual)-1 || visual[currentVisualLine+1].logicalLine != currentVL.logicalLine
	sourceMaxVisualCol := currentVL.length
	if !isLastSourceSegment {
		sourceMaxVisualCol = max(0, currentVL.length-1)
	}

	isLastTargetSegment := targetVisualLine == len(visual)-1 || visual[targetVisualLine+1].logicalLine != targetVL.logicalLine
	targetMaxVisualCol := targetVL.length
	if !isLastTargetSegment {
		targetMaxVisualCol = max(0, targetVL.length-1)
	}

	moveToVisualCol := e.computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol)
	e.cursor[0] = targetVL.logicalLine
	targetCol := targetVL.startCol + moveToVisualCol
	e.cursor[1] = min(targetCol, jsstring.Length(e.lines[targetVL.logicalLine]))
	for _, segment := range e.segmentLine(e.lines[targetVL.logicalLine]) {
		if segment.Start > e.cursor[1] {
			break
		}
		if segment.End-segment.Start <= 1 {
			continue
		}
		if e.cursor[1] < segment.End {
			if segment.Start < targetVL.startCol && targetVisualLine > currentVisualLine {
				next := targetVisualLine + 1
				for next < len(visual) && visual[next].logicalLine == targetVL.logicalLine && visual[next].startCol < segment.End {
					next++
				}
				if next < len(visual) {
					e.moveToVisualLine(visual, currentVisualLine, next)
					return
				}
			}
			e.snappedFromCursorCol = new(e.cursor[1])
			e.cursor[1] = segment.Start
			e.Invalidate()
			return
		}
	}
	e.snappedFromCursorCol = nil
	e.Invalidate()
}

func (e *Editor) computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol int) int {
	hasPreferred := e.preferredVisualCol != nil
	cursorInMiddle := currentVisualCol < sourceMaxVisualCol
	targetTooShort := targetMaxVisualCol < currentVisualCol

	if !hasPreferred || cursorInMiddle {
		if targetTooShort {
			e.preferredVisualCol = new(currentVisualCol)
			return targetMaxVisualCol
		}
		e.preferredVisualCol = nil
		return currentVisualCol
	}

	targetCantFitPreferred := targetMaxVisualCol < *e.preferredVisualCol
	if targetTooShort || targetCantFitPreferred {
		return targetMaxVisualCol
	}

	result := *e.preferredVisualCol
	e.preferredVisualCol = nil
	return result
}

func (e *Editor) moveCursor(deltaLine, deltaCol int) {
	e.lastAction = ""
	visual := e.buildVisualLineMap(e.renderWidth)
	currentVisualLine := e.findCurrentVisualLine(visual)

	if deltaLine != 0 {
		targetVisualLine := currentVisualLine + deltaLine
		if targetVisualLine >= 0 && targetVisualLine < len(visual) {
			e.moveToVisualLine(visual, currentVisualLine, targetVisualLine)
		}
	}

	if deltaCol == 0 {
		return
	}
	currentLine := e.lines[e.cursor[0]]
	if deltaCol > 0 {
		switch {
		case e.cursor[1] < jsstring.Length(currentLine):
			e.setCursorCol(e.nextSegmentEnd(currentLine, e.cursor[1]))
		case e.cursor[0] < len(e.lines)-1:
			e.cursor[0]++
			e.setCursorCol(0)
		case currentVisualLine >= 0 && currentVisualLine < len(visual):
			currentVL := visual[currentVisualLine]
			e.preferredVisualCol = new(e.cursor[1] - currentVL.startCol)
		}
	} else {
		switch {
		case e.cursor[1] > 0:
			e.setCursorCol(e.previousSegmentStart(currentLine, e.cursor[1]))
		case e.cursor[0] > 0:
			e.cursor[0]--
			e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
		}
	}
	e.Invalidate()
}

func (e *Editor) pageScroll(direction int) {
	e.lastAction = ""
	visual := e.buildVisualLineMap(e.renderWidth)
	currentVisualLine := e.findCurrentVisualLine(visual)
	pageSize := max(5, e.visibleLineLimit())
	targetVisualLine := currentVisualLine + direction*pageSize
	targetVisualLine = max(0, min(len(visual)-1, targetVisualLine))
	e.moveToVisualLine(visual, currentVisualLine, targetVisualLine)
}

func (e *Editor) jumpToChar(char, direction string) {
	e.lastAction = ""
	forward := direction == "forward"
	end, step := -1, -1
	if forward {
		end, step = len(e.lines), 1
	}
	for row := e.cursor[0]; row != end; row += step {
		line := e.lines[row]
		from := jsstring.Length(line)
		if forward {
			from = 0
		}
		if row == e.cursor[0] {
			from = e.cursor[1] - 1
			if forward {
				from = e.cursor[1] + 1
			}
		}
		var index int
		if forward {
			index = jsstring.IndexOf(line, char, from)
		} else {
			index = jsstring.LastIndexOf(line, char, from)
		}
		if index >= 0 {
			e.cursor[0] = row
			e.setCursorCol(index)
			e.Invalidate()
			return
		}
	}
}

func (e *Editor) undo() {
	// Undo leaves history browsing, as upstream undo calls exitHistoryBrowsing.
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	if len(e.history) == 0 {
		return
	}
	last := len(e.history) - 1
	state := e.history[last]
	e.history[last] = editorState{}
	e.history = e.history[:last]
	e.lines = state.lines
	e.cursor = state.cursor
	e.pastes = state.pastes
	e.pasteCounter = state.pasteCounter
	e.lastAction = ""
	e.preferredVisualCol = nil
	e.Invalidate()
	e.notifyChange()
}

func (e *Editor) cursorWordBackward() {
	e.lastAction = ""
	l, c := e.cursor[0], e.cursor[1]
	if c == 0 {
		if l > 0 {
			e.cursor[0]--
			e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
			e.Invalidate()
		}
		return
	}
	e.setCursorCol(e.prevWordStart(e.lines[l], c))
	e.Invalidate()
}

func (e *Editor) cursorWordForward() {
	e.lastAction = ""
	l, c := e.cursor[0], e.cursor[1]
	if c >= jsstring.Length(e.lines[l]) {
		if l < len(e.lines)-1 {
			e.cursor[0]++
			e.setCursorCol(0)
			e.Invalidate()
		}
		return
	}
	e.setCursorCol(e.nextWordEnd(e.lines[l], c))
	e.Invalidate()
}

func (e *Editor) deleteWordBackward() {
	defer e.notifyChange()
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	if c == 0 {
		if l == 0 {
			return
		}
		e.saveHistory()
		wasKill := e.lastAction == "kill"
		e.killRing.Push("\n", true, wasKill)
		prev := e.lines[l-1]
		e.cursor[0] = l - 1
		e.setCursorCol(jsstring.Length(prev))
		e.lines[l-1] = jsstring.Canonical(prev + e.lines[l])
		e.lines = append(e.lines[:l], e.lines[l+1:]...)
		e.lastAction = "kill"
		e.Invalidate()
		return
	}
	e.saveHistory()
	line := e.lines[l]
	start := e.prevWordStart(line, c)
	killed := jsstring.Slice(line, start, c)
	wasKill := e.lastAction == "kill"
	e.killRing.Push(killed, true, wasKill)
	e.lastAction = "kill"
	e.lines[l] = jsstring.Splice(line, start, c, "")
	e.setCursorCol(start)
	e.Invalidate()
}

func (e *Editor) deleteWordForward() {
	defer e.notifyChange()
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	if c >= jsstring.Length(line) {
		if l >= len(e.lines)-1 {
			return
		}
		e.saveHistory()
		wasKill := e.lastAction == "kill"
		e.killRing.Push("\n", false, wasKill)
		e.lines[l] = jsstring.Canonical(line + e.lines[l+1])
		e.lines = append(e.lines[:l+1], e.lines[l+2:]...)
		e.lastAction = "kill"
		e.Invalidate()
		return
	}
	e.saveHistory()
	end := e.nextWordEnd(line, c)
	e.setCursorCol(c)
	killed := jsstring.Slice(line, c, end)
	wasKill := e.lastAction == "kill"
	e.killRing.Push(killed, false, wasKill)
	e.lastAction = "kill"
	e.lines[l] = jsstring.Splice(line, c, end, "")
	e.Invalidate()
}

// deleteToLineEnd kills from cursor to end of the current line.
// If cursor is already at end-of-line and this is not the last line,
// kills the newline (joining with next line).
// Mirrors upstream editor.ts::deleteToEndOfLine.
func (e *Editor) deleteToLineEnd() {
	defer e.notifyChange()
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	wasKill := e.lastAction == "kill"
	if c < jsstring.Length(e.lines[l]) {
		e.saveHistory()
		killed := jsstring.Slice(e.lines[l], c)
		e.killRing.Push(killed, false, wasKill)
		e.lines[l] = jsstring.Slice(e.lines[l], 0, c)
		e.lastAction = "kill"
		e.Invalidate()
	} else if l < len(e.lines)-1 {
		e.saveHistory()
		e.killRing.Push("\n", false, wasKill)
		e.lines[l] = jsstring.Canonical(e.lines[l] + e.lines[l+1])
		e.lines = append(e.lines[:l+1], e.lines[l+2:]...)
		e.lastAction = "kill"
		e.Invalidate()
	}
}

// deleteToLineStart kills from line start to cursor.
// If cursor is at col 0 and not on first line, kills the newline.
// Mirrors upstream editor.ts::deleteToStartOfLine.
func (e *Editor) deleteToLineStart() {
	defer e.notifyChange()
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	wasKill := e.lastAction == "kill"
	if c > 0 {
		e.saveHistory()
		killed := jsstring.Slice(e.lines[l], 0, c)
		e.killRing.Push(killed, true, wasKill)
		e.lines[l] = jsstring.Slice(e.lines[l], c)
		e.setCursorCol(0)
		e.lastAction = "kill"
		e.Invalidate()
	} else if l > 0 {
		e.saveHistory()
		e.killRing.Push("\n", true, wasKill)
		prev := e.lines[l-1]
		e.setCursorCol(jsstring.Length(prev))
		e.lines[l-1] = jsstring.Canonical(prev + e.lines[l])
		e.lines = append(e.lines[:l], e.lines[l+1:]...)
		e.cursor[0]--
		e.lastAction = "kill"
		e.Invalidate()
	}
}

// deleteYankedText removes the most recent ring entry immediately before the cursor, including its line breaks.
func (e *Editor) deleteYankedText() {
	text := e.killRing.Peek()
	if text == "" {
		return
	}
	defer e.notifyChange()
	parts := strings.Split(text, "\n")
	l, c := e.cursor[0], e.cursor[1]
	startLine := l - len(parts) + 1
	startCol := c - jsstring.Length(text)
	if len(parts) > 1 {
		startCol = jsstring.Length(e.lines[startLine]) - jsstring.Length(parts[0])
	}
	before := jsstring.Slice(e.lines[startLine], 0, startCol)
	after := jsstring.Slice(e.lines[l], c)
	e.lines = slices.Concat(e.lines[:startLine], []string{jsstring.Canonical(before + after)}, e.lines[l+1:])
	e.cursor[0] = startLine
	e.setCursorCol(startCol)
	e.Invalidate()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// wrapText wraps s to lines of at most width runes.
// wrapText wraps s to lines of at most `width` visible columns, preserving
// active ANSI/OSC 8 hyperlink state across line breaks.
//
// Delegates to widthx.WrapTextWithAnsi which mirrors upstream
// `wrapTextWithAnsi` (utils.ts:596). The previous in-place implementation
// used `len(stripANSI(line))` for width measurement which was wrong for
// CJK/emoji and could not break overlong tokens.
func wrapText(s string, width int) []string {
	return widthx.WrapTextWithAnsi(s, width)
}

// WrapText wraps text at width, preserving ANSI escape codes across
// line breaks. Exported for use by tool renderers.
func WrapText(s string, width int) []string {
	return widthx.WrapTextWithAnsi(s, width)
}
