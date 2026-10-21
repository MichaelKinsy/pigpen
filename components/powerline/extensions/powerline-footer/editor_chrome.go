package powerline_footer

import (
	"regexp"
	"strings"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The editor chrome and the fast editor renderer. upstream: index.ts 913-1085 (renderFastPowerlineEditor, colorEditorBorder) and
// the editor.render override of setupCustomEditor (3421-3497).
//
// Two differences from the original, both from what the SDK's editor exposes. The original reads Pi's Editor object directly
// (state.lines, borderColor, isInPaste, jumpMode, autocompleteState, tui.terminal.rows, focused); a Go extension reaches the
// default editor only through sdk.Editor, so the fast renderer reads GetLines, GetCursor and IsShowingAutocomplete, assumes a
// 24-row terminal and a focused editor, and cannot see a paste or a jump in progress or set the editor's lastWidth. The border
// colour is the colour of the border row the default editor drew (its borderColor function), read from the row's leading
// escape sequences, and the powerline separator grey before the first row has been seen.

const (
	fastEditorRenderLineThreshold   = 80
	fastEditorRenderColumnThreshold = 1200
	// fastEditorTerminalRows stands for tui.terminal.rows, which the SDK does not report.
	fastEditorTerminalRows = 24
	// cursorMarker is pi-tui's CURSOR_MARKER, the zero-width APC sequence a focused editor puts where its cursor is.
	cursorMarker = "\x1b_pi:c\x07"
	// promptColor is ansi.getFgAnsi(200, 200, 200).
	promptColor = "\x1b[38;2;200;200;200m"
)

var (
	sgrRE          = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	borderRowRE    = regexp.MustCompile(`^─{3,}`)
	fastTextRE     = regexp.MustCompile(`^[\x20-\x7E]*$`)
	leadingSGRRunE = regexp.MustCompile(`^(?:\x1b\[[0-9;]*m)+`)
)

// editorBase is what the powerline editor needs of the host's default editor; *sdk.Editor implements it.
type editorBase interface {
	HandleInput(data string) error
	Render(width int) ([]string, error)
	GetLines() ([]string, error)
	GetCursor() (sdk.EditorCursor, error)
	IsShowingAutocomplete() (bool, error)
}

// jsLen is String.length: the UTF-16 length.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// jsSlice is String.prototype.slice for non-negative bounds: the UTF-16 range [from, to) of s.
func jsSlice(s string, from, to int) string {
	if from == 0 && to >= len(s) && isASCII(s) {
		return s
	}
	units := utf16.Encode([]rune(s))
	from = max(0, min(from, len(units)))
	to = max(from, min(to, len(units)))
	return string(utf16.Decode(units[from:to]))
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

type fastEditorState struct {
	lines                 []string
	cursorLine, cursorCol int
}

type fastEditorVisualLine struct {
	text      string
	cursorCol int // -1 when the cursor is not on this row
}

// readFastEditorState clamps the cursor into the text, as readFastEditorState does.
func readFastEditorState(lines []string, cursor sdk.EditorCursor) (fastEditorState, bool) {
	if len(lines) == 0 {
		return fastEditorState{}, false
	}
	cursorLine := max(0, min(len(lines)-1, cursor.Line))
	cursorCol := max(0, min(jsLen(lines[cursorLine]), cursor.Col))
	return fastEditorState{lines: lines, cursorLine: cursorLine, cursorCol: cursorCol}, true
}

func fastChunkCount(line string, width int) int {
	n := jsLen(line)
	return max(1, (max(1, n)+width-1)/width)
}

func fastChunk(line string, width, chunkIndex int) (text string, startCol int) {
	startCol = chunkIndex * width
	return jsSlice(line, startCol, startCol+width), startCol
}

func pushTrailingChunks(target []fastEditorVisualLine, line string, width, maxCount int) []fastEditorVisualLine {
	count := fastChunkCount(line, width)
	for index := max(0, count-maxCount); index < count; index++ {
		text, _ := fastChunk(line, width, index)
		target = append(target, fastEditorVisualLine{text: text, cursorCol: -1})
	}
	return target
}

type fastViewport struct {
	lines               []fastEditorVisualLine
	hasBefore, hasAfter bool
}

func collectFastEditorVisualLines(state fastEditorState, layoutWidth, maxVisibleLines int) fastViewport {
	cursorText := state.lines[state.cursorLine]
	cursorChunkIndex := state.cursorCol / layoutWidth
	cursorChunkCount := max(fastChunkCount(cursorText, layoutWidth), cursorChunkIndex+1)
	firstCursorChunk := max(0, cursorChunkIndex-maxVisibleLines+1)

	var visual []fastEditorVisualLine
	for lineIndex := state.cursorLine - 1; lineIndex >= 0 && len(visual) < maxVisibleLines-1; lineIndex-- {
		chunks := pushTrailingChunks(nil, state.lines[lineIndex], layoutWidth, maxVisibleLines-1-len(visual))
		visual = append(chunks, visual...)
	}
	for chunkIndex := firstCursorChunk; chunkIndex < cursorChunkIndex && len(visual) < maxVisibleLines-1; chunkIndex++ {
		text, _ := fastChunk(cursorText, layoutWidth, chunkIndex)
		visual = append(visual, fastEditorVisualLine{text: text, cursorCol: -1})
	}
	text, startCol := fastChunk(cursorText, layoutWidth, cursorChunkIndex)
	visual = append(visual, fastEditorVisualLine{text: text, cursorCol: state.cursorCol - startCol})
	for chunkIndex := cursorChunkIndex + 1; chunkIndex < cursorChunkCount && len(visual) < maxVisibleLines; chunkIndex++ {
		text, _ := fastChunk(cursorText, layoutWidth, chunkIndex)
		visual = append(visual, fastEditorVisualLine{text: text, cursorCol: -1})
	}
	for lineIndex := state.cursorLine + 1; lineIndex < len(state.lines) && len(visual) < maxVisibleLines; lineIndex++ {
		line := state.lines[lineIndex]
		count := fastChunkCount(line, layoutWidth)
		for chunkIndex := 0; chunkIndex < count && len(visual) < maxVisibleLines; chunkIndex++ {
			text, _ := fastChunk(line, layoutWidth, chunkIndex)
			visual = append(visual, fastEditorVisualLine{text: text, cursorCol: -1})
		}
	}
	if len(visual) > maxVisibleLines {
		visual = visual[len(visual)-maxVisibleLines:]
	}
	return fastViewport{
		lines:     visual,
		hasBefore: state.cursorLine > 0 || firstCursorChunk > 0,
		hasAfter:  state.cursorLine < len(state.lines)-1 || cursorChunkIndex < cursorChunkCount-1,
	}
}

// renderFastCursorLine draws the cursor row of ASCII text: the cell under the cursor in reverse video.
func renderFastCursorLine(line string, cursorCol int, focused bool) string {
	before := line[:min(cursorCol, len(line))]
	marker := ""
	if focused {
		marker = cursorMarker
	}
	if cursorCol < len(line) {
		return before + marker + "\x1b[7m" + line[cursorCol:cursorCol+1] + ansiReset + line[cursorCol+1:]
	}
	return before + marker + "\x1b[7m " + ansiReset
}

func padToWidth(line string, width int) string {
	return line + strings.Repeat(" ", max(0, width-visibleWidth(line)))
}

// editorBorderColor remembers how the default editor colours its border, from the leading escape sequences of a border row it
// drew; the zero value is colorEditorBorder's fallback, the powerline separator grey.
type editorBorderColor struct{ prefix string }

func (c editorBorderColor) apply(text string) string {
	if c.prefix == "" {
		return separatorAnsi + text + ansiReset
	}
	return c.prefix + text + ansiReset
}

// learn reads the colour from the default editor's top border row.
func (c *editorBorderColor) learn(row string) {
	if prefix := leadingSGRRunE.FindString(row); prefix != "" && strings.Contains(sgrRE.ReplaceAllString(row, ""), "─") {
		c.prefix = prefix
	}
}

// renderFastPowerlineEditor is renderFastPowerlineEditor: a cheap draw of a very large draft, or nil when the normal render is
// needed (a short draft, text that is not plain ASCII, autocomplete open, a narrow terminal, bash completions on).
func renderFastPowerlineEditor(ed editorBase, border editorBorderColor, width int, bashModeActive, completionsEnabled bool) ([]string, error) {
	if width < 10 || completionsEnabled {
		return nil, nil
	}
	lines, err := ed.GetLines()
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	if len(lines) < fastEditorRenderLineThreshold {
		long := false
		for _, line := range lines {
			if len(line) >= fastEditorRenderColumnThreshold { // bytes bound UTF-16 units from above, so a short byte length is short
				long = true
				break
			}
		}
		if !long {
			return nil, nil
		}
	}
	showing, err := ed.IsShowingAutocomplete()
	if err != nil || showing {
		return nil, err
	}
	cursor, err := ed.GetCursor()
	if err != nil {
		return nil, err
	}
	state, ok := readFastEditorState(lines, cursor)
	if !ok {
		return nil, nil
	}
	if len(state.lines) < fastEditorRenderLineThreshold && jsLen(state.lines[state.cursorLine]) < fastEditorRenderColumnThreshold {
		return nil, nil
	}

	maxVisibleLines := max(5, fastEditorTerminalRows*3/10)
	innerWidth := max(1, width-3)
	layoutWidth := max(1, innerWidth-1)
	viewport := collectFastEditorVisualLines(state, layoutWidth, maxVisibleLines)
	for _, line := range viewport.lines {
		if !fastTextRE.MatchString(line.text) {
			return nil, nil
		}
	}

	edge := func(marker string) string {
		text := strings.Repeat("─", width-2)
		if marker != "─" {
			text = marker + strings.Repeat("─", max(0, width-3))
		}
		return " " + border.apply(text)
	}
	glyph := ">"
	if bashModeActive {
		glyph = "$"
	}
	promptPrefix := " " + promptColor + glyph + ansiReset + " "
	const contPrefix = "   "

	topMark, bottomMark := "─", "─"
	if viewport.hasBefore {
		topMark = "↑"
	}
	if viewport.hasAfter {
		bottomMark = "↓"
	}
	out := []string{edge(topMark)}
	for i, visual := range viewport.lines {
		content := visual.text
		if visual.cursorCol >= 0 {
			content = renderFastCursorLine(visual.text, visual.cursorCol, true)
		}
		prefix := contPrefix
		if i == 0 {
			prefix = promptPrefix
		}
		out = append(out, prefix+padToWidth(content, innerWidth))
	}
	return append(out, edge(bottomMark)), nil
}

// renderPowerlineEditor is the editor.render override: the fast render when it applies, otherwise the default editor's rows
// laid out at width-3 with the prompt glyph in the left margin and powerline borders.
func renderPowerlineEditor(ed editorBase, border *editorBorderColor, width int, bashModeActive, completionsEnabled bool) ([]string, error) {
	return renderPowerlineEditorWith(ed, border, width, bashModeActive, completionsEnabled, editorRenderOptions{chrome: true, fast: true})
}

// editorRenderOptions are the profiler's A/B switches (editorChrome, fastRender) and the profiler itself, nil when profiling is off.
type editorRenderOptions struct {
	chrome, fast bool
	perf         *editorPerfProfiler
}

func renderPowerlineEditorWith(ed editorBase, border *editorBorderColor, width int, bashModeActive, completionsEnabled bool, o editorRenderOptions) ([]string, error) {
	if !o.chrome {
		return ed.Render(width)
	}
	if o.fast {
		var fast []string
		var err error
		if o.perf != nil {
			fast, err = measureEditorPerf2(o.perf, "editor.render.fast-probe", func() ([]string, error) {
				return renderFastPowerlineEditor(ed, *border, width, bashModeActive, completionsEnabled)
			})
		} else {
			fast, err = renderFastPowerlineEditor(ed, *border, width, bashModeActive, completionsEnabled)
		}
		if err != nil || fast != nil {
			if fast != nil && o.perf != nil {
				o.perf.count("editor.render.fast-hit")
			}
			return fast, err
		}
	}
	if width < 10 {
		return ed.Render(width)
	}
	glyph := ">"
	if bashModeActive {
		glyph = "$"
	}
	promptPrefix := " " + promptColor + glyph + ansiReset + " "
	const contPrefix = "   "
	contentWidth := max(1, width-3)
	lines, err := ed.Render(contentWidth)
	if err != nil || len(lines) == 0 {
		return lines, err
	}
	border.learn(lines[0])

	bottom := len(lines) - 1
	for i := len(lines) - 1; i >= 1; i-- {
		if stripped := sgrRE.ReplaceAllString(lines[i], ""); stripped != "" && borderRowRE.MatchString(stripped) {
			bottom = i
			break
		}
	}
	rule := " " + border.apply(strings.Repeat("─", width-2))
	out := []string{rule}
	for i := 1; i < bottom; i++ {
		prefix := contPrefix
		if i == 1 {
			prefix = promptPrefix
		}
		out = append(out, prefix+lines[i])
	}
	if bottom == 1 {
		out = append(out, promptPrefix+strings.Repeat(" ", contentWidth))
	}
	out = append(out, rule)
	return append(out, lines[bottom+1:]...), nil
}

// measureEditorPerf2 is measureEditorPerf for an operation that answers rows and an error.
func measureEditorPerf2(p *editorPerfProfiler, name string, operation func() ([]string, error)) ([]string, error) {
	type result struct {
		lines []string
		err   error
	}
	r := measureEditorPerf(p, name, func() result {
		lines, err := operation()
		return result{lines, err}
	})
	return r.lines, r.err
}
