package rpiv_web_tools

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Terminal text layout, a stand-in for pi-tui's utils and Text component (the Go SDK has none, and a port may not
// import PiG's tui package): ANSI-aware width, word wrapping that carries the active styling across a break, and
// the lines of `new Text(text, 0, 0).render(width)`, which is what the original's renderers return.
// render_differential_test.go compares the result with the original's own component at many widths.
// Grapheme clusters are approximated (a base rune with its marks, variation selectors, skin-tone modifiers, joined
// sequences and regional-indicator pairs); pi-tui uses Intl.Segmenter and a full Unicode width table.

// ansiAt returns the length of the escape sequence starting at s[i] (0 when none): a CSI sequence ending
// in a final byte, or an OSC sequence ending in BEL or ST.
func ansiAt(s string, i int) int {
	if s[i] != 0x1b || i+1 >= len(s) {
		return 0
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j - i + 1
			}
		}
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j - i + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j - i + 2
			}
		}
	}
	return 0
}

func runeWidth(r rune) int {
	if r < 0x300 {
		return 1
	}
	switch {
	case r == 0x200d || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || (r >= 0xfe00 && r <= 0xfe0f):
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe6f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f680 && r <= 0x1f6ff,
		r >= 0x1f900 && r <= 0x1f9ff, r >= 0x1fa70 && r <= 0x1faff, r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func isRegionalIndicator(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

// extendsCluster reports whether r attaches to the rune before it.
func extendsCluster(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Mc, r) ||
		(r >= 0xfe00 && r <= 0xfe0f) || (r >= 0xe0100 && r <= 0xe01ef) || (r >= 0x1f3fb && r <= 0x1f3ff) || r == 0x200c
}

// clusterEnd is the end of the unit a terminal draws in one place that starts at s[i]: a rune with the marks,
// variation selectors, modifiers and joined runes that attach to it, or a pair of regional indicators.
func clusterEnd(s string, i int) int {
	if s[i] < utf8.RuneSelf && (i+1 == len(s) || s[i+1] < utf8.RuneSelf) {
		return i + 1
	}
	first, n := utf8.DecodeRuneInString(s[i:])
	j := i + n
	if isRegionalIndicator(first) && j < len(s) {
		if r, n2 := utf8.DecodeRuneInString(s[j:]); isRegionalIndicator(r) {
			j += n2
		}
	}
	for j < len(s) {
		r, n2 := utf8.DecodeRuneInString(s[j:])
		if r == 0x200d {
			j += n2
			if j < len(s) {
				_, n3 := utf8.DecodeRuneInString(s[j:])
				j += n3
			}
			continue
		}
		if !extendsCluster(r) {
			break
		}
		j += n2
	}
	return j
}

// graphemeWidth is the cells a cluster takes: an emoji sequence is two wide, otherwise its first rune decides.
func graphemeWidth(g string) int {
	first, n := utf8.DecodeRuneInString(g)
	if n == len(g) {
		return runeWidth(first)
	}
	if strings.ContainsRune(g, 0xfe0f) || strings.ContainsRune(g, 0x200d) || isRegionalIndicator(first) {
		return 2
	}
	for _, r := range g[n:] {
		if r >= 0x1f3fb && r <= 0x1f3ff {
			return 2
		}
	}
	return runeWidth(first)
}

// visibleWidth is the terminal cell width of s, ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if n := ansiAt(s, i); n > 0 {
				i += n
				continue
			}
		}
		e := clusterEnd(s, i)
		w += graphemeWidth(s[i:e])
		i = e
	}
	return w
}

// ansiTracker follows the SGR attributes in force so a line break can close and reopen them. upstream: pi-tui
// utils.js AnsiCodeTracker (hyperlinks are not needed: the renderers write none).
type ansiTracker struct {
	bold, dim, italic, underline   bool
	blink, inverse, hidden, strike bool
	fg, bg                         string
}

func (t *ansiTracker) reset() { *t = ansiTracker{} }

// process applies one escape sequence.
func (t *ansiTracker) process(code string) {
	if !strings.HasSuffix(code, "m") {
		return
	}
	// The first /\x1b\[([\d;]*)m/ in the code.
	params, found := "", false
	for i := 0; i+2 < len(code) && !found; i++ {
		if code[i] != 0x1b || code[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(code) && (code[j] == ';' || code[j] >= '0' && code[j] <= '9') {
			j++
		}
		if j < len(code) && code[j] == 'm' {
			params, found = code[i+2:j], true
		}
	}
	if !found {
		return
	}
	if params == "" || params == "0" {
		t.reset()
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); {
		c, err := strconv.Atoi(parts[i])
		if err != nil {
			i++
			continue
		}
		if (c == 38 || c == 48) && i+1 < len(parts) {
			color, used := "", 0
			if parts[i+1] == "5" && i+2 < len(parts) {
				color, used = strings.Join(parts[i:i+3], ";"), 3
			} else if parts[i+1] == "2" && i+4 < len(parts) {
				color, used = strings.Join(parts[i:i+5], ";"), 5
			}
			if used > 0 {
				if c == 38 {
					t.fg = color
				} else {
					t.bg = color
				}
				i += used
				continue
			}
		}
		switch {
		case c == 0:
			t.reset()
		case c == 1:
			t.bold = true
		case c == 2:
			t.dim = true
		case c == 3:
			t.italic = true
		case c == 4:
			t.underline = true
		case c == 5:
			t.blink = true
		case c == 7:
			t.inverse = true
		case c == 8:
			t.hidden = true
		case c == 9:
			t.strike = true
		case c == 21:
			t.bold = false
		case c == 22:
			t.bold, t.dim = false, false
		case c == 23:
			t.italic = false
		case c == 24:
			t.underline = false
		case c == 25:
			t.blink = false
		case c == 27:
			t.inverse = false
		case c == 28:
			t.hidden = false
		case c == 29:
			t.strike = false
		case c == 39:
			t.fg = ""
		case c == 49:
			t.bg = ""
		case c >= 30 && c <= 37 || c >= 90 && c <= 97:
			t.fg = strconv.Itoa(c)
		case c >= 40 && c <= 47 || c >= 100 && c <= 107:
			t.bg = strconv.Itoa(c)
		}
		i++
	}
}

// update processes every escape sequence in text.
func (t *ansiTracker) update(text string) {
	for i := 0; i < len(text); {
		if n := ansiAt(text, i); n > 0 {
			t.process(text[i : i+n])
			i += n
			continue
		}
		i++
	}
}

// active is the sequence that restores the styling in force, "" when there is none.
func (t *ansiTracker) active() string {
	var buf [64]byte
	b := append(buf[:0], 0x1b, '[')
	n := len(b)
	for _, a := range [...]struct {
		on   bool
		code byte
	}{{t.bold, '1'}, {t.dim, '2'}, {t.italic, '3'}, {t.underline, '4'}, {t.blink, '5'}, {t.inverse, '7'}, {t.hidden, '8'}, {t.strike, '9'}} {
		if a.on {
			if len(b) > n {
				b = append(b, ';')
			}
			b = append(b, a.code)
		}
	}
	for _, c := range [...]string{t.fg, t.bg} {
		if c != "" {
			if len(b) > n {
				b = append(b, ';')
			}
			b = append(b, c...)
		}
	}
	if len(b) == n {
		return ""
	}
	return string(append(b, 'm'))
}

// lineEndReset is what a line gets at its end: underline is the only attribute that would show in the padding.
func (t *ansiTracker) lineEndReset() string {
	if t.underline {
		return "\x1b[24m"
	}
	return ""
}

// isCJKBreak reports whether a cluster is a Han, Hiragana, Katakana, Hangul or Bopomofo character, which may start
// a new line anywhere.
func isCJKBreak(g string) bool {
	for _, r := range g {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Bopomofo) {
			return true
		}
	}
	return false
}

// splitTokens splits a line into runs of spaces and of other characters (each CJK character alone), keeping every
// escape sequence with the visible character after it (the sequences before the end of the line go with the last
// token). Every token is a slice of text. upstream: pi-tui utils.js splitIntoTokensWithAnsi.
func splitTokens(text string) []string {
	tokens := make([]string, 0, len(text)/6+2)
	start, end, kind := -1, 0, 0 // the token being built: its bounds and what it is made of (1 spaces, 2 other)
	lastStart := 0               // where the last emitted token starts
	flush := func() {
		if start >= 0 {
			tokens = append(tokens, text[start:end])
			lastStart = start
			start, kind = -1, 0
		}
	}
	pending := -1 // where the escape sequences waiting for the next visible character begin
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			if n := ansiAt(text, i); n > 0 {
				if pending < 0 {
					pending = i
				}
				i += n
				continue
			}
		}
		e := clusterEnd(text, i)
		seg := text[i:e]
		isSpace := seg == " "
		from := i
		if pending >= 0 {
			from = pending
			pending = -1
		}
		if !isSpace && isCJKBreak(seg) {
			flush()
			tokens = append(tokens, text[from:e])
			lastStart = from
			i = e
			continue
		}
		k := 2
		if isSpace {
			k = 1
		}
		if start >= 0 && kind != k {
			flush()
		}
		if start < 0 {
			start = from
		}
		end, kind = e, k
		i = e
	}
	if pending >= 0 {
		switch {
		case start >= 0:
			end = len(text)
		case len(tokens) > 0:
			tokens[len(tokens)-1] = text[lastStart:]
		default:
			start, end = pending, len(text)
		}
	}
	flush()
	return tokens
}

// breakLongWord cuts a token wider than the line into cluster-sized pieces, carrying the styling across each cut.
func breakLongWord(word string, width int, t *ansiTracker) []string {
	var lines []string
	var line strings.Builder
	line.WriteString(t.active())
	lineWidth := 0
	for i := 0; i < len(word); {
		if word[i] == 0x1b {
			if n := ansiAt(word, i); n > 0 {
				line.WriteString(word[i : i+n])
				t.process(word[i : i+n])
				i += n
				continue
			}
		}
		e := clusterEnd(word, i)
		g := word[i:e]
		gw := graphemeWidth(g)
		if lineWidth+gw > width {
			line.WriteString(t.lineEndReset())
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(t.active())
			lineWidth = 0
		}
		line.WriteString(g)
		lineWidth += gw
		i = e
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if visibleWidth(line) <= width {
		return []string{line}
	}
	var wrapped []string
	t := &ansiTracker{}
	var current strings.Builder
	current.Grow(width + 32)
	currentWidth := 0
	for _, token := range splitTokens(line) {
		tokenWidth := visibleWidth(token)
		isSpace := jsTrim(token) == ""
		if tokenWidth > width && !isSpace {
			if current.Len() > 0 {
				wrapped = append(wrapped, current.String()+t.lineEndReset())
			}
			broken := breakLongWord(token, width, t)
			wrapped = append(wrapped, broken[:len(broken)-1]...)
			current.Reset()
			current.Grow(width + 32)
			current.WriteString(broken[len(broken)-1])
			currentWidth = visibleWidth(broken[len(broken)-1])
			continue
		}
		if currentWidth+tokenWidth > width && currentWidth > 0 {
			wrapped = append(wrapped, jsTrimEnd(current.String())+t.lineEndReset())
			current.Reset()
			current.Grow(width + 32)
			current.WriteString(t.active())
			currentWidth = 0
			if !isSpace {
				current.WriteString(token)
				currentWidth = tokenWidth
			}
		} else {
			current.WriteString(token)
			currentWidth += tokenWidth
		}
		t.update(token)
	}
	if current.Len() > 0 {
		wrapped = append(wrapped, current.String())
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	for i := range wrapped {
		wrapped[i] = jsTrimEnd(wrapped[i])
	}
	return wrapped
}

// wrapTextWithAnsi wraps text to width cells, breaking at spaces (a longer word at clusters) and carrying the active
// styling onto each new line. upstream: pi-tui utils.js wrapTextWithAnsi.
func wrapTextWithAnsi(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out []string
	t := &ansiTracker{}
	for _, line := range strings.Split(text, "\n") {
		prefix := ""
		if len(out) > 0 {
			prefix = t.active()
		}
		out = append(out, wrapSingleLine(prefix+line, width)...)
		t.update(line)
	}
	return out
}

// textLines is what pi-tui's `new Text(text, 0, 0).render(width)` returns, which is what the original's renderers
// return: no lines for blank text, otherwise tabs as three spaces, wrapped, every line padded with spaces to width.
// upstream: pi-tui components/text.js.
func textLines(text string, width int) []string {
	if jsTrim(text) == "" {
		return []string{}
	}
	lines := wrapTextWithAnsi(strings.ReplaceAll(text, "\t", "   "), max(1, width))
	for i, l := range lines {
		if pad := width - visibleWidth(l); pad > 0 {
			if pad <= len(blanks) {
				lines[i] = l + blanks[:pad]
			} else {
				lines[i] = l + strings.Repeat(" ", pad)
			}
		}
	}
	return lines
}

// blanks is the padding of a line, sliced instead of built for any width a terminal has.
const blanks = "                                                                                                                                                                                                                                                                "
