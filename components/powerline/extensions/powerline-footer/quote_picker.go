package powerline_footer

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The reply picker overlay. upstream: quote-reply.ts QuotePicker, formatTimestamp, printableInput, filterCandidates.
//
// The original runs inside Pi's TUI and borrows fuzzyFilter, matchesKey, decodeKittyPrintable, truncateToWidth and visibleWidth
// from @earendil-works/pi-tui. This file holds small stand-ins for the parts the picker uses, so that a swap to PiG's tui
// package (tui.FuzzyFilter, tui.MatchesKeyID, tui.DecodeKittyPrintable) is a change to this file only.

const maxVisibleItems = 8

// pickerTheme is the Theme methods the picker calls; sdk.UITheme has them.
type pickerTheme interface {
	Fg(token, text string) string
	Bold(text string) string
	Italic(text string) string
}

// formatTimestamp is date.toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"}), with the date in front when it is not
// today, in en-US (a Node without LANG, LC_ALL or LC_MESSAGES uses en-US; other locales are not reproduced).
func formatTimestamp(timestamp int64, now time.Time) string {
	date := time.UnixMilli(timestamp).In(time.Local)
	now = now.In(time.Local)
	clock := date.Format("03:04 PM")
	if date.Year() == now.Year() && date.Month() == now.Month() && date.Day() == now.Day() {
		return clock
	}
	return strconv.Itoa(int(date.Month())) + "/" + strconv.Itoa(date.Day()) + "/" + strconv.Itoa(date.Year()) + " " + clock
}

// fuzzyMatch and fuzzyFilter: pi-tui fuzzy.js. A lower score is a better match.

type fuzzyResult struct {
	matches bool
	score   float64
}

var (
	fuzzyAlphaNumeric = regexp.MustCompile(`^([a-z]+)([0-9]+)$`)
	fuzzyNumericAlpha = regexp.MustCompile(`^([0-9]+)([a-z]+)$`)
)

func fuzzyMatch(query, text string) fuzzyResult {
	queryLower := strings.ToLower(query)
	textLower := utf16.Encode([]rune(strings.ToLower(text)))

	matchQuery := func(normalized string) fuzzyResult {
		nq := utf16.Encode([]rune(normalized))
		if len(nq) == 0 {
			return fuzzyResult{true, 0}
		}
		if len(nq) > len(textLower) {
			return fuzzyResult{false, 0}
		}
		queryIndex, lastMatchIndex, consecutive := 0, -1, 0
		score := 0.0
		for queryIndex < len(nq) {
			i := -1
			for j := lastMatchIndex + 1; j < len(textLower); j++ {
				if textLower[j] == nq[queryIndex] {
					i = j
					break
				}
			}
			if i == -1 {
				break
			}
			isWordBoundary := i == 0 || strings.ContainsRune("-_./:", rune(textLower[i-1])) || isJSSpace(rune(textLower[i-1]))
			if lastMatchIndex == i-1 {
				consecutive++
				score -= float64(consecutive * 5)
			} else {
				consecutive = 0
				if lastMatchIndex >= 0 {
					score += float64((i - lastMatchIndex - 1) * 2)
				}
			}
			if isWordBoundary {
				score -= 10
			}
			score += float64(i) * 0.1
			lastMatchIndex = i
			queryIndex++
		}
		if queryIndex < len(nq) {
			return fuzzyResult{false, 0}
		}
		if string(utf16.Decode(nq)) == string(utf16.Decode(textLower)) {
			score -= 100
		}
		return fuzzyResult{true, score}
	}

	primary := matchQuery(queryLower)
	if primary.matches {
		return primary
	}
	swapped := ""
	if m := fuzzyAlphaNumeric.FindStringSubmatch(queryLower); m != nil {
		swapped = m[2] + m[1]
	} else if m := fuzzyNumericAlpha.FindStringSubmatch(queryLower); m != nil {
		swapped = m[2] + m[1]
	}
	if swapped == "" {
		return primary
	}
	sm := matchQuery(swapped)
	if !sm.matches {
		return primary
	}
	return fuzzyResult{true, sm.score + 5}
}

// fuzzyFilter keeps the items every whitespace- or slash-separated token matches, best first (the sort is stable).
func fuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	if jsTrim(query) == "" {
		return items
	}
	var tokens []string
	for _, tok := range strings.FieldsFunc(jsTrim(query), func(r rune) bool { return isJSSpace(r) || r == '/' }) {
		tokens = append(tokens, tok)
	}
	if len(tokens) == 0 {
		return items
	}
	type scored struct {
		item  T
		total float64
	}
	var results []scored
	for _, item := range items {
		text := getText(item)
		total, all := 0.0, true
		for _, token := range tokens {
			m := fuzzyMatch(token, text)
			if !m.matches {
				all = false
				break
			}
			total += m.score
		}
		if all {
			results = append(results, scored{item, total})
		}
	}
	sort.SliceStable(results, func(a, b int) bool { return results[a].total < results[b].total })
	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}

func filterCandidates(candidates []quoteCandidate, query string) []quoteCandidate {
	trimmed := jsTrim(query)
	if trimmed == "" {
		return candidates
	}
	return fuzzyFilter(candidates, trimmed, func(c quoteCandidate) string { return c.SearchText })
}

// Keys. pi-tui matchesKey for the ids the picker uses, with the Kitty protocol treated as inactive (an extension cannot read
// that state): "\n" is Enter, as in a terminal without the protocol.

var (
	kittyCSIU      = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
	kittyArrow     = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$`)
	modifyOtherKey = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)
)

const (
	lockMask       = 64 + 128
	modShift       = 1
	modAlt         = 2
	modCtrl        = 4
	cpEscape       = 27
	cpEnter        = 13
	cpKpEnter      = 57414
	cpBackspace    = 127
	cpArrowUp      = -1
	cpArrowDown    = -2
	kittyAllowMods = modShift | lockMask
)

type kittyKey struct {
	codepoint, modifier int
	shifted, base       *int
}

func atoiPtr(s string) *int {
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

func parseKitty(data string) (kittyKey, bool) {
	if m := kittyCSIU.FindStringSubmatch(data); m != nil {
		cp, err := strconv.Atoi(m[1])
		if err != nil {
			return kittyKey{}, false
		}
		mod := 1
		if m[4] != "" {
			mod, _ = strconv.Atoi(m[4])
		}
		return kittyKey{codepoint: cp, modifier: mod - 1, shifted: atoiPtr(m[2]), base: atoiPtr(m[3])}, true
	}
	if m := kittyArrow.FindStringSubmatch(data); m != nil {
		mod, _ := strconv.Atoi(m[1])
		codes := map[string]int{"A": -1, "B": -2, "C": -3, "D": -4}
		return kittyKey{codepoint: codes[m[3]], modifier: mod - 1}, true
	}
	return kittyKey{}, false
}

// kittyFunctionalKey maps the keypad codepoints the picker's keys have an equivalent for (KITTY_FUNCTIONAL_KEY_EQUIVALENTS).
func kittyFunctionalKey(cp int) int {
	switch cp {
	case 57419:
		return cpArrowUp
	case 57420:
		return cpArrowDown
	}
	if cp >= 57399 && cp <= 57408 {
		return cp - 57399 + 48
	}
	return cp
}

func matchesKitty(data string, expectedCodepoint, expectedModifier int) bool {
	parsed, ok := parseKitty(data)
	if !ok {
		return false
	}
	actual := parsed.modifier &^ lockMask
	expected := expectedModifier &^ lockMask
	normalized := kittyFunctionalKey(parsed.codepoint)
	if actual == expected && normalized == kittyFunctionalKey(expectedCodepoint) {
		return true
	}
	if actual != expected {
		return false
	}
	if parsed.base != nil && *parsed.base == expectedCodepoint {
		isLatinLetter := normalized >= 97 && normalized <= 122
		if !isLatinLetter && !strings.ContainsRune("`-=[]\\;',./", rune(normalized)) {
			return true
		}
	}
	return false
}

func matchesModifyOther(data string, codepoint, modifier int) bool {
	m := modifyOtherKey.FindStringSubmatch(data)
	if m == nil {
		return false
	}
	mod, _ := strconv.Atoi(m[1])
	cp, _ := strconv.Atoi(m[2])
	return cp == codepoint && mod-1 == modifier
}

// matchesPickerKey is matchesKey(data, id) for the ids the picker handles.
func matchesPickerKey(data, id string) bool {
	switch id {
	case "up":
		return data == "\x1b[A" || data == "\x1bOA" || matchesKitty(data, cpArrowUp, 0)
	case "down":
		return data == "\x1b[B" || data == "\x1bOB" || matchesKitty(data, cpArrowDown, 0)
	case "enter":
		return data == "\r" || data == "\n" || data == "\x1bOM" || matchesKitty(data, cpEnter, 0) || matchesKitty(data, cpKpEnter, 0)
	case "escape":
		return data == "\x1b" || matchesKitty(data, cpEscape, 0) || matchesModifyOther(data, cpEscape, 0)
	case "backspace":
		return data == "\x7f" || data == "\x08" || matchesKitty(data, cpBackspace, 0) || matchesModifyOther(data, cpBackspace, 0)
	case "ctrl+c":
		return data == "\x03" || matchesKitty(data, 'c', modCtrl) || matchesModifyOther(data, 'c', modCtrl)
	case "ctrl+u":
		return data == "\x15" || matchesKitty(data, 'u', modCtrl) || matchesModifyOther(data, 'u', modCtrl)
	}
	return false
}

// printableInput is the text a key chunk types: a printable Kitty sequence, or the chunk itself when it has no control character.
func printableInput(data string) (string, bool) {
	if m := kittyCSIU.FindStringSubmatch(data); m != nil {
		if r, ok := decodeKittyPrintable(m); ok {
			return r, true
		}
	}
	if data == "" {
		return "", false
	}
	for _, r := range data {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return "", false
		}
	}
	return data, true
}

func decodeKittyPrintable(m []string) (string, bool) {
	codepoint, err := strconv.Atoi(m[1])
	if err != nil {
		return "", false
	}
	shifted := atoiPtr(m[2])
	modValue := 1
	if m[4] != "" {
		modValue, _ = strconv.Atoi(m[4])
	}
	modifier := modValue - 1
	if modifier&^kittyAllowMods != 0 || modifier&(modAlt|modCtrl) != 0 {
		return "", false
	}
	effective := codepoint
	if modifier&modShift != 0 && shifted != nil {
		effective = *shifted
	}
	effective = kittyFunctionalKey(effective)
	if effective < 32 || effective > 0x10ffff || (effective >= 0xd800 && effective <= 0xdfff) {
		return "", false
	}
	return string(rune(effective)), true
}

// QuotePicker. upstream: quote-reply.ts class QuotePicker.

type quotePicker struct {
	mu           sync.Mutex
	query        string
	selectedIdx  int
	candidates   []quoteCandidate
	theme        pickerTheme
	now          func() time.Time
	onSelect     func(quoteCandidate)
	onCancel     func()
	lastSelected *quoteCandidate
}

func newQuotePicker(candidates []quoteCandidate, theme pickerTheme, now func() time.Time, onSelect func(quoteCandidate), onCancel func()) *quotePicker {
	return &quotePicker{candidates: candidates, theme: theme, now: now, onSelect: onSelect, onCancel: onCancel}
}

func (p *quotePicker) border(text string) string { return p.theme.Fg("borderMuted", text) }
func (p *quotePicker) emptyRow(innerWidth int) string {
	return p.border("│") + strings.Repeat(" ", innerWidth) + p.border("│")
}
func (p *quotePicker) divider(innerWidth int) string {
	return p.border("├" + strings.Repeat("─", innerWidth) + "┤")
}
func (p *quotePicker) bottomBorder(innerWidth int) string {
	return p.border("╰" + strings.Repeat("─", innerWidth) + "╯")
}

// truncatePad is truncateToWidth(text, width, "…", true): cut to width cells, then pad with spaces up to width.
func truncatePad(text string, width int) string {
	out := truncateToWidth(text, width, "…")
	if pad := width - visibleWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

func (p *quotePicker) row(innerWidth int, content string) string {
	return p.border("│") + truncatePad(" "+content, innerWidth) + p.border("│")
}

func (p *quotePicker) topBorder(innerWidth int, title string) string {
	borderLength := max(0, innerWidth-visibleWidth(title))
	left := borderLength / 2
	return p.border("╭"+strings.Repeat("─", left)) + p.theme.Fg("dim", title) + p.border(strings.Repeat("─", borderLength-left)+"╮")
}

func (p *quotePicker) searchLine() string {
	cursor := p.theme.Fg("accent", "│")
	icon := p.theme.Fg("dim", "◎")
	query := cursor + p.theme.Fg("dim", p.theme.Italic("type to filter messages..."))
	if p.query != "" {
		query = p.query + cursor
	}
	return icon + "  " + query
}

func (p *quotePicker) renderCandidate(innerWidth int, c quoteCandidate, selected bool) string {
	prefix := p.theme.Fg("borderMuted", "·")
	if selected {
		prefix = p.theme.Fg("accent", "▸")
	}
	roleToken := "success"
	if c.Role == "assistant" {
		roleToken = "accent"
	}
	role := p.theme.Fg(roleToken, c.Role)
	meta := p.theme.Fg("dim", c.ID+"  "+formatTimestamp(c.TimestampMs, p.now()))
	shownRole := role
	if selected {
		shownRole = p.theme.Bold(role)
	}
	head := prefix + " " + shownRole + " " + meta
	maxExcerptWidth := max(0, innerWidth-visibleWidth(head)-8)
	preview, separator := "", ""
	if maxExcerptWidth > 18 {
		preview = p.theme.Fg("muted", excerpt(c.Text, maxExcerptWidth))
		separator = "  " + p.theme.Fg("borderMuted", "—") + "  "
	}
	if selected {
		return p.theme.Fg("accent", head+separator+preview)
	}
	return head + separator + preview
}

func (p *quotePicker) scrollIndicator(total int) string {
	filled := max(1, int(math.Floor(float64(p.selectedIdx+1)/float64(total)*10+0.5)))
	dots := make([]string, 10)
	for i := range dots {
		if i < filled {
			dots[i] = p.theme.Fg("accent", "●")
		} else {
			dots[i] = p.theme.Fg("dim", "○")
		}
	}
	return strings.Join(dots, " ") + "  " + p.theme.Fg("dim", strconv.Itoa(p.selectedIdx+1)+"/"+strconv.Itoa(total))
}

func (p *quotePicker) help() string {
	i := p.theme.Italic
	return p.theme.Fg("dim", i("↑↓")+" navigate  "+i("enter")+" quote  "+i("esc")+" cancel  "+i("⌫")+" edit")
}

// Render draws the picker at width.
func (p *quotePicker) Render(width int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	innerWidth := max(0, width-2)
	filtered := filterCandidates(p.candidates, p.query)
	if len(filtered) == 0 {
		p.selectedIdx = 0
	} else {
		p.selectedIdx = min(p.selectedIdx, len(filtered)-1)
	}
	lines := []string{p.topBorder(innerWidth, " Reply "), p.emptyRow(innerWidth), p.row(innerWidth, p.searchLine()), p.emptyRow(innerWidth), p.divider(innerWidth)}

	if len(filtered) == 0 {
		lines = append(lines, p.emptyRow(innerWidth), p.row(innerWidth, p.theme.Fg("warning", p.theme.Italic("No matching messages"))), p.emptyRow(innerWidth))
	} else {
		lines = append(lines, p.emptyRow(innerWidth))
		start := max(0, min(p.selectedIdx-maxVisibleItems/2, len(filtered)-maxVisibleItems))
		end := min(len(filtered), start+maxVisibleItems)
		for offset, c := range filtered[start:end] {
			lines = append(lines, p.row(innerWidth, p.renderCandidate(innerWidth, c, start+offset == p.selectedIdx)))
		}
		lines = append(lines, p.emptyRow(innerWidth))
		if len(filtered) > maxVisibleItems {
			lines = append(lines, p.row(innerWidth, p.scrollIndicator(len(filtered))), p.emptyRow(innerWidth))
		}
	}

	return append(lines, p.divider(innerWidth), p.emptyRow(innerWidth), p.row(innerWidth, p.help()), p.bottomBorder(innerWidth))
}

// HandleInput applies one key chunk; selected is set when Enter chose a candidate, cancelled when the picker was dismissed.
func (p *quotePicker) HandleInput(data string) (selected *quoteCandidate, cancelled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	filtered := filterCandidates(p.candidates, p.query)
	switch {
	case matchesPickerKey(data, "up"):
		if len(filtered) == 0 {
			p.selectedIdx = 0
		} else {
			p.selectedIdx = (p.selectedIdx - 1 + len(filtered)) % len(filtered)
		}
	case matchesPickerKey(data, "down"):
		if len(filtered) == 0 {
			p.selectedIdx = 0
		} else {
			p.selectedIdx = (p.selectedIdx + 1) % len(filtered)
		}
	case matchesPickerKey(data, "enter"):
		if p.selectedIdx >= 0 && p.selectedIdx < len(filtered) {
			c := filtered[p.selectedIdx]
			return &c, false
		}
	case matchesPickerKey(data, "escape") || matchesPickerKey(data, "ctrl+c"):
		return nil, true
	case matchesPickerKey(data, "backspace"):
		runes := []rune(p.query)
		if len(runes) > 0 {
			runes = runes[:len(runes)-1]
		}
		p.query = string(runes)
		p.selectedIdx = 0
	case matchesPickerKey(data, "ctrl+u"):
		p.query = ""
		p.selectedIdx = 0
	default:
		if printable, ok := printableInput(data); ok {
			p.query += printable
			p.selectedIdx = 0
		}
	}
	return nil, false
}

// quotePickerComponent adapts the picker to the SDK's remote component. The original's finish() calls done(candidate) and then
// overlayHandle.hide(); Pi's own close() already hides an overlay when done runs (interactive-mode.js showExtensionCustom), so
// the component result alone closes it here and there is no onHandle to call.
type quotePickerComponent struct {
	picker   *quotePicker
	finished bool
}

func (c *quotePickerComponent) Render(width int) []string { return c.picker.Render(width) }

func (c *quotePickerComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if c.finished {
		return sdk.RemoteComponentResult{}, nil
	}
	selected, cancelled := c.picker.HandleInput(data)
	switch {
	case selected != nil:
		c.finished = true
		return sdk.RemoteComponentResult{Done: true, Value: selected.ID}, nil
	case cancelled:
		c.finished = true
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// quotePickerOverlayOptions is { overlay: true, overlayOptions: { width: "85%", minWidth: 72, maxHeight: "80%" } }.
func quotePickerOverlayOptions() sdk.RemoteOverlayOptions {
	return sdk.RemoteOverlayOptions{Overlay: true, OverlayOptions: &sdk.OverlayOptions{Width: sdk.OverlayPercent(85), MinWidth: 72, MaxHeight: sdk.OverlayPercent(80)}}
}
