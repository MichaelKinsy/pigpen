package pigmodeltweaks

import (
	"fmt"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The two focused components the original opens with ctx.ui.custom: the
// multi-select of pt-model-guard.ts (showModelPicker) and the searchable select
// of pt-openrouter-lock-provider.ts (showLockPicker). Both are sdk.RemoteComponents,
// so the lines are drawn here and the host routes raw keystrokes to HandleInput.
//
// The original colors rows with the host theme (accent, success, warning, dim).
// A Go component has no theme handle (ctx.ui.theme is not in the SDK), so the
// rows are uncolored; the text, layout and keys are the original's.

// maxPickerRows is showModelPicker's `Math.min(items.length, 20)` ceiling.
const maxPickerRows = 20

// pickerItem is one row of the multi-select.
type pickerItem struct {
	value     string
	checked   bool
	available bool
}

// guardPicker is showModelPicker's component.
type guardPicker struct {
	title      string
	items      []pickerItem
	cursor     int
	scroll     int
	search     string
	maxVisible int
}

// newGuardPicker builds the rows the way showModelPicker does: every model is
// available (the original lists ctx.modelRegistry.getAvailable()), checked ones
// come first, then by locale compare of `provider/id`.
func newGuardPicker(title string, models []modelRef, checked []string) *guardPicker {
	items := make([]pickerItem, 0, len(models))
	for _, model := range models {
		value := model.String()
		items = append(items, pickerItem{value: value, checked: slices.Contains(checked, value), available: true})
	}
	slices.SortStableFunc(items, func(a, b pickerItem) int {
		if a.checked != b.checked {
			if a.checked {
				return -1
			}
			return 1
		}
		if a.available != b.available {
			if a.available {
				return -1
			}
			return 1
		}
		return localeCompare(a.value, b.value)
	})
	return &guardPicker{title: title, items: items, maxVisible: min(len(items), maxPickerRows)}
}

// pickerValues lists the row values in display order.
func pickerValues(p *guardPicker) []string {
	values := make([]string, 0, len(p.items))
	for _, item := range p.items {
		values = append(values, item.value)
	}
	return values
}

// filtered is getFilteredIndices: the rows whose value contains the search,
// case-insensitively.
func (p *guardPicker) filtered() []int {
	indices := make([]int, 0, len(p.items))
	term := strings.ToLower(p.search)
	for index, item := range p.items {
		if term == "" || strings.Contains(strings.ToLower(item.value), term) {
			indices = append(indices, index)
		}
	}
	return indices
}

func (p *guardPicker) checkedCount() int {
	count := 0
	for _, item := range p.items {
		if item.checked {
			count++
		}
	}
	return count
}

// Render draws the frame of showModelPicker's render().
func (p *guardPicker) Render(width int) []string {
	rw := max(1, width)
	filtered := p.filtered()
	border := strings.Repeat("─", rw)
	lines := []string{
		border,
		" " + p.title,
		" 🔍 " + p.search + "_",
		fmt.Sprintf(" %d/%d selected, %d shown", p.checkedCount(), len(p.items), len(filtered)),
		"",
	}
	for i := 0; i < min(len(filtered), p.maxVisible); i++ {
		global := p.scroll + i
		if global >= len(filtered) {
			break
		}
		item := p.items[filtered[global]]
		box := "☐"
		if item.checked {
			box = "☑"
		}
		label := box + " " + item.value
		if !item.available {
			label += " ⚠ no key"
		}
		for j, wrapped := range wrapPlain(label, max(1, rw-2)) {
			prefix := "  "
			if j == 0 && global == p.cursor {
				prefix = "> "
			}
			lines = append(lines, prefix+wrapped)
		}
	}
	if len(filtered) > p.maxVisible {
		lines = append(lines, fmt.Sprintf(" %d-%d of %d", p.scroll+1, min(p.scroll+p.maxVisible, len(filtered)), len(filtered)))
	}
	lines = append(lines, "", "  Type to search • Space toggle • a=all • n=none • Enter confirm • Esc cancel", border)
	return lines
}

// HandleInput is showModelPicker's handleInput, in its order of tests.
func (p *guardPicker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	decoded, typed := keyOf(data)
	switch decoded {
	case keyUp:
		maxIndex := len(p.filtered()) - 1
		if maxIndex < 0 {
			return sdk.RemoteComponentResult{}, nil
		}
		p.cursor = max(0, min(p.cursor, maxIndex)-1)
		if p.cursor < p.scroll {
			p.scroll = p.cursor
		}
	case keyDown:
		maxIndex := len(p.filtered()) - 1
		if maxIndex < 0 {
			return sdk.RemoteComponentResult{}, nil
		}
		p.cursor = min(maxIndex, max(0, p.cursor)+1)
		if p.cursor >= p.scroll+p.maxVisible {
			p.scroll = p.cursor - p.maxVisible + 1
		}
	case keyEnter:
		selected := []string{}
		for _, item := range p.items {
			if item.checked {
				selected = append(selected, item.value)
			}
		}
		return sdk.RemoteComponentResult{Done: true, Value: selected}, nil
	case keyEscape:
		if p.search != "" {
			p.resetSearch("")
			return sdk.RemoteComponentResult{}, nil
		}
		return sdk.RemoteComponentResult{Done: true, Value: nil}, nil
	case keyBackspace:
		if p.search != "" {
			p.resetSearch(trimLastRune(p.search))
		}
	case keySpace:
		filtered := p.filtered()
		if p.cursor < len(filtered) {
			item := &p.items[filtered[p.cursor]]
			item.checked = !item.checked
		}
		if p.cursor < len(filtered)-1 {
			p.cursor++
			if p.cursor >= p.scroll+p.maxVisible {
				p.scroll = p.cursor - p.maxVisible + 1
			}
		}
	case keyRune:
		// `a` and `n` select or clear the filtered rows, but only while there
		// is no search, so they stay typeable in one.
		if p.search == "" && (typed == 'a' || typed == 'n') {
			for _, index := range p.filtered() {
				p.items[index].checked = typed == 'a'
			}
			return sdk.RemoteComponentResult{}, nil
		}
		if typed >= ' ' && typed < 0x7f {
			p.resetSearch(p.search + string(typed))
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

func (p *guardPicker) resetSearch(search string) {
	p.search, p.cursor, p.scroll = search, 0, 0
}

func trimLastRune(text string) string {
	runes := []rune(text)
	return string(runes[:len(runes)-1])
}

// localeCompare orders strings the way Node's String.prototype.localeCompare
// does for the ASCII an id is made of: punctuation, then digits, then letters
// without regard to case; a lowercase letter sorts before its uppercase twin
// only when everything else is equal. Other characters follow, by code point.
func localeCompare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < min(len(ra), len(rb)); i++ {
		if wa, wb := collationWeight(ra[i]), collationWeight(rb[i]); wa != wb {
			if wa < wb {
				return -1
			}
			return 1
		}
	}
	if len(ra) != len(rb) {
		if len(ra) < len(rb) {
			return -1
		}
		return 1
	}
	for i := range ra {
		if ra[i] == rb[i] {
			continue
		}
		// Same letter, different case: lowercase first.
		if isLower(ra[i]) {
			return -1
		}
		return 1
	}
	return 0
}

// punctuationOrder is ICU's root-collation order of the ASCII punctuation.
const punctuationOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

func collationWeight(r rune) int {
	if index := strings.IndexRune(punctuationOrder, r); index >= 0 {
		return index
	}
	switch {
	case r >= '0' && r <= '9':
		return 100 + int(r-'0')
	case r >= 'a' && r <= 'z':
		return 200 + int(r-'a')
	case r >= 'A' && r <= 'Z':
		return 200 + int(r-'A')
	}
	return 1000 + int(r)
}

func isLower(r rune) bool { return r >= 'a' && r <= 'z' }

// wrapPlain is pi-tui's wrapTextWithAnsi for text without escape sequences:
// words are kept whole, a word wider than the line is broken at the width, and
// trailing blanks are dropped.
func wrapPlain(text string, width int) []string {
	if text == "" || textWidth(text) <= width {
		return []string{text}
	}
	var wrapped []string
	line, lineWidth := "", 0
	for _, token := range splitWords(text) {
		tokenWidth := textWidth(token)
		isSpace := strings.TrimSpace(token) == ""
		if tokenWidth > width && !isSpace {
			if line != "" {
				wrapped = append(wrapped, line)
				line, lineWidth = "", 0
			}
			broken := breakWord(token, width)
			wrapped = append(wrapped, broken[:len(broken)-1]...)
			line = broken[len(broken)-1]
			lineWidth = textWidth(line)
			continue
		}
		if lineWidth+tokenWidth > width && lineWidth > 0 {
			wrapped = append(wrapped, strings.TrimRight(line, " "))
			if isSpace {
				line, lineWidth = "", 0
			} else {
				line, lineWidth = token, tokenWidth
			}
			continue
		}
		line += token
		lineWidth += tokenWidth
	}
	if line != "" {
		wrapped = append(wrapped, line)
	}
	for i := range wrapped {
		wrapped[i] = strings.TrimRight(wrapped[i], " ")
	}
	return wrapped
}

// splitWords splits text into alternating runs of blanks and non-blanks.
func splitWords(text string) []string {
	var tokens []string
	var current strings.Builder
	space := false
	for index, r := range text {
		isSpace := r == ' ' || r == '\t'
		if index > 0 && isSpace != space {
			tokens = append(tokens, current.String())
			current.Reset()
		}
		space = isSpace
		current.WriteRune(r)
	}
	return append(tokens, current.String())
}

func breakWord(word string, width int) []string {
	var lines []string
	var line strings.Builder
	used := 0
	for _, r := range word {
		w := runeWidth(r)
		if used+w > width && used > 0 {
			lines = append(lines, line.String())
			line.Reset()
			used = 0
		}
		line.WriteRune(r)
		used += w
	}
	return append(lines, line.String())
}

func textWidth(text string) int {
	total := 0
	for _, r := range text {
		total += runeWidth(r)
	}
	return total
}

func runeWidth(r rune) int {
	if isWide(r) {
		return 2
	}
	return 1
}

// isWide reports whether a rune occupies two terminal columns: the East Asian
// Wide and Fullwidth blocks plus emoji, which is what a model list or a search
// term can realistically contain.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF && r != 0x303F, // CJK
		r >= 0xAC00 && r <= 0xD7A3,                // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,                // CJK compatibility
		r >= 0xFE30 && r <= 0xFE6F,                // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60,                // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF:
		return true
	}
	return false
}

// lockItem is one row of the lock picker: a model id and its description.
type lockItem struct {
	id          string
	description string
}

// lockPicker is showLockPicker's component: a SelectList (visible rows
// min(items, 15), prefix filter, wrapping arrows) under a search line.
type lockPicker struct {
	items    []lockItem
	filtered []lockItem
	selected int
	search   string
	rows     int
}

func newLockPicker(items []lockItem) *lockPicker {
	return &lockPicker{items: items, filtered: items, rows: min(len(items), 15)}
}

func (p *lockPicker) setFilter(search string) {
	p.search = search
	p.filtered = p.filtered[:0:0]
	for _, item := range p.items {
		if strings.HasPrefix(strings.ToLower(item.id), strings.ToLower(search)) {
			p.filtered = append(p.filtered, item)
		}
	}
	p.selected = 0
}

// Render is Container.render of showLockPicker: border, title, search line,
// the SelectList, hint, border.
func (p *lockPicker) Render(width int) []string {
	border := strings.Repeat("─", max(1, width))
	search := "  type to search"
	if p.search != "" {
		search = "  search: " + p.search
	}
	lines := []string{border, " OpenRouter Provider Lock ", search}
	lines = append(lines, p.renderList(width)...)
	lines = append(lines, " ↑↓ navigate • enter select • esc cancel ", border)
	return lines
}

// renderList is SelectList.render with the default layout.
func (p *lockPicker) renderList(width int) []string {
	if len(p.filtered) == 0 {
		return []string{"  No matching commands"}
	}
	// The SelectList's primary column: widest id plus the gap, clamped to the
	// default minimum and maximum, which are both 32.
	const column = 32
	start := max(0, min(p.selected-p.rows/2, len(p.filtered)-p.rows))
	end := min(start+p.rows, len(p.filtered))
	var lines []string
	for index := start; index < end; index++ {
		lines = append(lines, p.renderItem(p.filtered[index], index == p.selected, width, column))
	}
	if start > 0 || end < len(p.filtered) {
		lines = append(lines, truncateWidth(fmt.Sprintf("  (%d/%d)", p.selected+1, len(p.filtered)), width-2))
	}
	return lines
}

func (p *lockPicker) renderItem(item lockItem, selected bool, width, column int) string {
	prefix := "  "
	if selected {
		prefix = "→ "
	}
	description := strings.Join(strings.Fields(item.description), " ")
	if description != "" && width > 40 {
		primary := max(1, min(column, width-2-4))
		value := truncateWidth(item.id, max(1, primary-2))
		spacing := strings.Repeat(" ", max(1, primary-textWidth(value)))
		remaining := width - (2 + textWidth(value) + len(spacing)) - 2
		if remaining > 10 {
			return prefix + value + spacing + truncateWidth(description, remaining)
		}
	}
	return prefix + truncateWidth(item.id, width-2-2)
}

// truncateWidth cuts text to width columns.
func truncateWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	used := 0
	for index, r := range text {
		w := runeWidth(r)
		if used+w > width {
			return text[:index]
		}
		used += w
	}
	return text
}

// HandleInput is the lock picker's handleInput: Backspace and Escape edit the
// search, any printable character (a space too) extends it, and everything else
// goes to the SelectList (Up and Down wrap, Enter selects, Escape cancels).
func (p *lockPicker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	decoded, typed := keyOf(data)
	switch {
	case decoded == keyBackspace:
		if p.search != "" {
			p.setFilter(trimLastRune(p.search))
		}
	case decoded == keyEscape && p.search != "":
		p.setFilter("")
	case decoded == keySpace || decoded == keyRune && typed >= ' ' && typed < 0x7f:
		if decoded == keySpace {
			typed = ' '
		}
		p.setFilter(p.search + string(typed))
	case decoded == keyUp && len(p.filtered) > 0:
		p.selected = (p.selected - 1 + len(p.filtered)) % len(p.filtered)
	case decoded == keyDown && len(p.filtered) > 0:
		p.selected = (p.selected + 1) % len(p.filtered)
	case decoded == keyEnter:
		if p.selected < len(p.filtered) {
			return sdk.RemoteComponentResult{Done: true, Value: p.filtered[p.selected].id}, nil
		}
	case decoded == keyEscape:
		return sdk.RemoteComponentResult{Done: true, Value: nil}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}
