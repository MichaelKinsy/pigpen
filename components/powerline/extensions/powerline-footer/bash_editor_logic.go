package powerline_footer

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// The editor-independent parts of the bash mode editor: input classification, dropped file paths, the ghost suggestion
// scheduler, accepting and drawing a ghost, shell history browsing and the fast ASCII line edits. upstream:
// bash-mode/editor.ts. The BashModeEditor itself subclasses Pi's CustomEditor and is installed through
// ctx.ui.setEditorComponent, which the Go SDK does not offer (Context.SetEditorComponent returns ErrUnsupportedSubprocessUI), so
// the editor is not ported; these are the pieces it is built from, ready for it. Columns count UTF-16 code units as in pi-tui.

const (
	ghostUpdateDebounce = 50 * time.Millisecond // GHOST_UPDATE_DEBOUNCE_MS
	// fastASCIILineColumnThreshold is FAST_ASCII_LINE_COLUMN_THRESHOLD: lines shorter than this take Pi's own editing path.
	fastASCIILineColumnThreshold = 1200
)

// isPrintableInput upstream: editor.ts:41: one UTF-16 unit that is printable (not a control, DEL or C1, not a lone surrogate),
// or one surrogate pair.
func isPrintableInput(data string) bool {
	units := utf16.Encode([]rune(data))
	switch len(units) {
	case 1:
		code := units[0]
		return code >= 0x20 && (code < 0x7f || code > 0x9f) && (code < 0xd800 || code > 0xdfff)
	case 2:
		return units[0] >= 0xd800 && units[0] <= 0xdbff && units[1] >= 0xdc00 && units[1] <= 0xdfff
	}
	return false
}

// isCommandUndoShortcut upstream: editor.ts:52: Command+Z as the kitty and xterm modifyOtherKeys encodings send it.
func isCommandUndoShortcut(data string) bool {
	switch data {
	case "\x1b[122;9u", "\x1b[122;9:1u", "\x1b[122;9:2u", "\x1b[27;9;122~":
		return true
	}
	return false
}

// bracketedPasteContent upstream: editor.ts:59: the text of an input that is exactly one complete bracketed paste.
func bracketedPasteContent(data string) (string, bool) {
	const startMarker, endMarker = "\x1b[200~", "\x1b[201~"
	if !strings.HasPrefix(data, startMarker) {
		return "", false
	}
	end := strings.Index(data[len(startMarker):], endMarker)
	if end == -1 {
		return "", false
	}
	end += len(startMarker)
	if end+len(endMarker) != len(data) {
		return "", false
	}
	return data[len(startMarker):end], true
}

// fileURLToPath is node:url fileURLToPath on POSIX for a "file://" string: the host must be empty or localhost, dot segments are
// resolved as the WHATWG URL parser does, the path may not hide an encoded "/" and must percent-decode to valid UTF-8. Query and
// fragment are ignored.
func fileURLToPath(raw string) (string, bool) {
	rest, ok := strings.CutPrefix(raw, "file://")
	if !ok {
		return "", false
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.ReplaceAll(rest, "\\", "/")
	host, pathname := rest, ""
	if i := strings.Index(rest, "/"); i >= 0 {
		host, pathname = rest[:i], rest[i:]
	}
	if host != "" && !strings.EqualFold(host, "localhost") {
		return "", false
	}
	var segments []string
	parts := strings.Split(pathname, "/")
	for i, part := range parts[1:] {
		last := i == len(parts)-2
		switch strings.ToLower(part) {
		case ".", "%2e":
			if last {
				segments = append(segments, "")
			}
		case "..", ".%2e", "%2e.", "%2e%2e":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
			if last {
				segments = append(segments, "")
			}
		default:
			segments = append(segments, part)
		}
	}
	pathname = "/" + strings.Join(segments, "/")
	if strings.Contains(strings.ToLower(pathname), "%2f") {
		return "", false
	}
	decoded, err := url.PathUnescape(pathname)
	if err != nil || !utf8.ValidString(decoded) {
		return "", false
	}
	return decoded, true
}

// decodeFileUriList upstream: editor.ts:71: a list of file:// URIs (Finder drops) as space-separated paths.
func decodeFileUriList(text string) (string, bool) {
	var entries []string
	for _, field := range strings.FieldsFunc(text, isJSSpace) {
		if field != "" && !strings.HasPrefix(field, "#") {
			entries = append(entries, field)
		}
	}
	if len(entries) == 0 {
		return "", false
	}
	paths := make([]string, len(entries))
	for i, entry := range entries {
		if !strings.HasPrefix(entry, "file://") {
			return "", false
		}
		p, ok := fileURLToPath(entry)
		if !ok {
			return "", false
		}
		paths[i] = p
	}
	return strings.Join(paths, " "), true
}

var (
	droppedControl = regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]")
	droppedPathRe  = regexp.MustCompile(`^(?:/|~/|\.\.?/)`)
)

// droppedPathTextFromInput upstream: editor.ts:90: the path text a terminal drop or paste stands for: file:// URIs decoded, or a
// single absolute, home- or dot-relative path as typed.
func droppedPathTextFromInput(data string) (string, bool) {
	text := data
	if paste, ok := bracketedPasteContent(data); ok {
		text = paste
	}
	if list, ok := decodeFileUriList(text); ok {
		return list, true
	}
	trimmed := strings.Trim(text, "\r\n")
	if utf16Len(trimmed) <= 1 || droppedControl.MatchString(trimmed) {
		return "", false
	}
	if droppedPathRe.MatchString(trimmed) && !strings.ContainsAny(trimmed, "\r\n") {
		return trimmed, true
	}
	return "", false
}

// isOneOffBashCommandLine: the editor's first line starts a "!cmd" or "!!cmd" command. upstream: editor.ts isOneOffBashCommandContext.
func isOneOffBashCommandLine(firstLine string) bool { return strings.HasPrefix(firstLine, "!") }

// acceptGhostSuggestion upstream: editor.ts acceptGhostSuggestion: the text the editor takes when the ghost is accepted. The
// ghost must extend the text, the text be one line and the cursor sit at its end.
func acceptGhostSuggestion(ghost *ghostSuggestion, text string, cursorLine, cursorCol int) (string, bool) {
	if ghost == nil || strings.Contains(text, "\n") {
		return "", false
	}
	if cursorLine != 0 || cursorCol != utf16Len(text) {
		return "", false
	}
	if !strings.HasPrefix(ghost.Value, text) || ghost.Value == text {
		return "", false
	}
	return ghost.Value, true
}

// renderGhostLine upstream: editor.ts render(): draws the ghost suffix in grey behind a block cursor on the editor's content
// row (row 1 of the framed editor), padded to the width. Any condition that does not hold returns the rows unchanged.
func renderGhostLine(lines []string, width int, text string, ghost *ghostSuggestion, cursorLine, cursorCol int, shellContext bool) []string {
	if !shellContext || ghost == nil || strings.Contains(text, "\n") {
		return lines
	}
	if cursorLine != 0 || cursorCol != utf16Len(text) {
		return lines
	}
	if !strings.HasPrefix(ghost.Value, text) || ghost.Value == text {
		return lines
	}
	if len(lines) < 3 {
		return lines
	}
	suffix := ghost.Value[len(text):]
	const cursorBlock = "\x1b[7m \x1b[0m"
	availableWidth := max(0, width-visibleWidth(text)-1)
	if availableWidth == 0 {
		return lines
	}
	shownSuffix := truncateToWidth(suffix, availableWidth, "")
	if shownSuffix == "" {
		return lines
	}
	if pad := availableWidth - visibleWidth(shownSuffix); pad > 0 { // pi-tui's truncateToWidth(..., pad = true)
		shownSuffix += strings.Repeat(" ", pad)
	}
	padding := strings.Repeat(" ", max(0, width-visibleWidth(text)-1-visibleWidth(shownSuffix)))
	out := append([]string{}, lines...)
	out[1] = text + cursorBlock + "\x1b[38;5;244m" + shownSuffix + "\x1b[0m" + padding
	return out
}

// shellHistoryBrowser is the editor's browsing state over earlier shell commands (up and down in bash mode). upstream:
// editor.ts navigateShellHistory and resetShellHistoryBrowse.
type shellHistoryBrowser struct {
	index int
	items []string
	draft string
}

func newShellHistoryBrowser() *shellHistoryBrowser {
	return &shellHistoryBrowser{index: -1, items: []string{}}
}

func (b *shellHistoryBrowser) reset() { b.index, b.items, b.draft = -1, []string{}, "" }

// shellHistoryStep is what one up or down press asks of the editor.
type shellHistoryStep struct {
	Notify        string  // a message for onNotify(msg, "info"); empty for none
	SetText       *string // new editor text, nil to keep it
	ClearGhost    bool
	ScheduleGhost bool
}

// navigate moves through the matching history; direction -1 goes to older commands, 1 back to newer ones and finally to the
// draft that was being typed. expandedText is the editor's expanded text, getEntries the history source for a prefix.
func (b *shellHistoryBrowser) navigate(direction int, expandedText string, getEntries func(prefix string) []string) shellHistoryStep {
	prefix := b.draft
	if prefix == "" {
		prefix = expandedText
	}
	if b.index == -1 {
		b.draft = prefix
		b.items = getEntries(prefix)
	}
	if len(b.items) == 0 {
		return shellHistoryStep{Notify: "No shell history matches"}
	}
	itemOrDraft := func() *string {
		if b.index >= 0 && b.index < len(b.items) {
			return &b.items[b.index]
		}
		d := b.draft
		return &d
	}
	if direction < 0 {
		b.index = min(len(b.items)-1, b.index+1)
		return shellHistoryStep{SetText: itemOrDraft(), ClearGhost: true}
	}
	b.index--
	if b.index < 0 {
		b.index = -1
		d := b.draft
		return shellHistoryStep{SetText: &d, ScheduleGhost: true}
	}
	return shellHistoryStep{SetText: itemOrDraft(), ClearGhost: true}
}

// ghostUpdater is the editor's ghost suggestion: updates are debounced, a newer update or a clear cancels the one in flight,
// and a result that arrives after that is dropped. upstream: editor.ts scheduleGhostUpdate and clearGhostSuggestion.
type ghostUpdater struct {
	debounce time.Duration
	resolve  func(ctx context.Context, text string) (*ghostSuggestion, error)
	onChange func() // the editor asks for a render

	mu     sync.Mutex
	ghost  *ghostSuggestion
	cancel context.CancelFunc
	timer  *time.Timer
	token  int
}

func newGhostUpdater(resolve func(ctx context.Context, text string) (*ghostSuggestion, error), onChange func()) *ghostUpdater {
	return &ghostUpdater{debounce: ghostUpdateDebounce, resolve: resolve, onChange: onChange}
}

// current is the ghost to show, nil for none.
func (g *ghostUpdater) current() *ghostSuggestion {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ghost
}

// clear drops the ghost and cancels anything pending.
func (g *ghostUpdater) clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clearLocked()
}

func (g *ghostUpdater) clearLocked() {
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.token++
	g.ghost = nil
}

// schedule resolves a ghost for text after the debounce. With completions disabled it clears instead.
func (g *ghostUpdater) schedule(completionsEnabled bool, text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !completionsEnabled {
		g.clearLocked()
		return
	}
	g.token++
	token := g.token
	if g.timer != nil {
		g.timer.Stop()
	}
	if g.cancel != nil {
		g.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	g.timer = time.AfterFunc(g.debounce, func() {
		g.mu.Lock()
		if g.token != token || ctx.Err() != nil {
			g.mu.Unlock()
			return
		}
		g.timer = nil
		g.mu.Unlock()

		ghost, err := g.resolve(ctx, text)
		g.mu.Lock()
		if ctx.Err() != nil || g.token != token {
			g.mu.Unlock()
			return
		}
		if err != nil {
			g.mu.Unlock()
			bashDebug("[powerline-footer] Failed to resolve bash ghost suggestion: %v", err)
			return
		}
		g.ghost = ghost
		g.mu.Unlock()
		if g.onChange != nil {
			g.onChange()
		}
	})
}

// isPlainASCIIAt: the UTF-16 unit at index is a printable ASCII character.
func isPlainASCIIAt(units []uint16, index int) bool {
	return index >= 0 && index < len(units) && units[index] >= 0x20 && units[index] <= 0x7e
}

func isPlainASCIIOrEdge(units []uint16, index int) bool {
	return index < 0 || index >= len(units) || isPlainASCIIAt(units, index)
}

// isPlainASCIICursorMove: the character at index and both neighbours are plain ASCII (or the line edge), so moving the cursor
// over it cannot split a grapheme. upstream: editor.ts isPlainAsciiCursorMove.
func isPlainASCIICursorMove(units []uint16, index int) bool {
	return isPlainASCIIAt(units, index) && isPlainASCIIOrEdge(units, index-1) && isPlainASCIIOrEdge(units, index+1)
}

// fastASCIIBackspace upstream: editor.ts tryFastAsciiBackspace (the text part): on a long single line with the cursor at its
// end behind two plain ASCII characters, the line without its last character.
func fastASCIIBackspace(line string, cursorCol int) (string, bool) {
	units := utf16.Encode([]rune(line))
	if cursorCol < fastASCIILineColumnThreshold || cursorCol != len(units) {
		return "", false
	}
	if !isPlainASCIIAt(units, cursorCol-2) || !isPlainASCIIAt(units, cursorCol-1) {
		return "", false
	}
	return string(utf16.Decode(units[:len(units)-1])), true
}

// fastASCIIForwardDelete upstream: editor.ts tryFastAsciiForwardDelete (the text part): the line without the plain ASCII
// character under the cursor, on a long line.
func fastASCIIForwardDelete(line string, cursorCol int) (string, bool) {
	units := utf16.Encode([]rune(line))
	if cursorCol >= len(units) || len(units) < fastASCIILineColumnThreshold {
		return "", false
	}
	if !isPlainASCIICursorMove(units, cursorCol) {
		return "", false
	}
	return string(utf16.Decode(append(append([]uint16{}, units[:cursorCol]...), units[cursorCol+1:]...))), true
}

// fastASCIIHorizontalMove upstream: editor.ts tryFastAsciiHorizontalMove (the position part): the cursor column after moving one
// plain ASCII character in direction (-1 or 1) on a long line, and whether the fast path applies.
func fastASCIIHorizontalMove(line string, cursorCol, direction int) (int, bool) {
	units := utf16.Encode([]rune(line))
	if len(units) < fastASCIILineColumnThreshold {
		return 0, false
	}
	if direction < 0 {
		if cursorCol <= 0 || !isPlainASCIICursorMove(units, cursorCol-1) {
			return 0, false
		}
	} else if cursorCol >= len(units) || !isPlainASCIICursorMove(units, cursorCol) {
		return 0, false
	}
	return cursorCol + direction, true
}
