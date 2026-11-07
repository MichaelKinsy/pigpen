package posthorse

// Port of src/ui/text.ts plus the pi-tui components the cards draw with
// (Text, Box). Components are `func(width int) []string` row builders: the
// original's components are pure functions of the width (their invalidate()
// bodies are empty, text.ts/cards.ts), and a render request here is one
// width.

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// outputColor is OutputColor (text.ts:7).
type outputColor = string

const (
	colorToolOutput outputColor = "toolOutput"
	colorMuted      outputColor = "muted"
	colorDim        outputColor = "dim"
	colorError      outputColor = "error"
	colorWarning    outputColor = "warning"
)

// rowView is a pi-tui Component reduced to its render(width).
type rowView func(width int) []string

// uiSpace reports the code points of ECMAScript WhiteSpace and LineTerminator,
// which `\s`, `trim` and `trimEnd` use.
func uiSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func uiTrimEnd(s string) string  { return strings.TrimRightFunc(s, uiSpace) }
func uiTrimBoth(s string) string { return strings.TrimFunc(s, uiSpace) }

// uiCollapseSpaces is `s.replace(/\s+/g, " ")`. Bytes that are not valid UTF-8 (the SDK's WTF-8 form of a lone
// surrogate, ED A0..BF xx) are copied unchanged: the regex leaves a lone UTF-16 unit alone (text.ts:9-12).
func uiCollapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	run := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size > 1 {
			if uiSpace(r) {
				if !run {
					b.WriteByte(' ')
				}
				run = true
				i += size
				continue
			}
		}
		run = false
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// cleanText is clean (text.ts:9-12): tool previews must not execute terminal
// controls or Unicode interlinear annotations.
func cleanText(value string) string {
	// Only ESC sequences (and C1 code points, UTF-8 0xC2 0x80-0x9F) start a
	// terminal sequence, so text without either skips the scan.
	needsStrip := false
	needsFilter := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == 0x1b || c == 0xc2:
			needsStrip, needsFilter = true, true
		case c < 0x20 && c != '\t' && c != '\n', c == 0x7f, c == 0xef:
			needsFilter = true
		}
		if needsStrip {
			break
		}
	}
	if needsStrip {
		value = tui.StripTerminalSequences(value)
	}
	if !needsFilter {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		// /[\x00-\x08\x0b-\x1f\x7f-\x9f\ufff9-\ufffb]/g; an invalid byte (a WTF-8 lone surrogate) is kept as it is.
		if r != utf8.RuneError || size > 1 {
			if r <= 0x08 || (r >= 0x0b && r <= 0x1f) || (r >= 0x7f && r <= 0x9f) || (r >= 0xfff9 && r <= 0xfffb) {
				i += size
				continue
			}
		}
		b.WriteString(value[i : i+size])
		i += size
	}
	return b.String()
}

// cardArgsOf is argsOf (text.ts:14-16).
func cardArgsOf(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// cardArgument is argument (text.ts:18-23). A
// JSON null is a present key holding null, which renders "…" like undefined.
func cardArgument(value any, name string) string {
	switch v := value.(type) {
	case string:
		return cleanText(v)
	case nil:
		return "…"
	default:
		return "[invalid " + name + "]"
	}
}

// cardTextOf is textOf of text.ts:25-39 (not message-text.ts's textOf): a
// string, or the text parts of a content array joined with newlines.
func cardTextOf(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := content.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	first := true
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok || m["type"] != "text" {
			continue
		}
		text, ok := m["text"].(string)
		if !ok {
			continue
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(text)
	}
	return b.String()
}

// cardTextOfBlocks is cardTextOf over the typed content blocks of a tool result.
func cardTextOfBlocks(content []map[string]any) string {
	var b strings.Builder
	first := true
	for _, part := range content {
		if part["type"] != "text" {
			continue
		}
		text, ok := part["text"].(string)
		if !ok {
			continue
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(text)
	}
	return b.String()
}

// piText is pi-tui Text(text, 0, 0).render(width) (text.js:render): wrapped
// rows, each padded with spaces to the width; blank text draws nothing.
func piText(text string, width int) []string {
	if text == "" || uiTrimBoth(text) == "" {
		return []string{}
	}
	normalized := text
	if strings.IndexByte(text, '\t') >= 0 {
		normalized = strings.ReplaceAll(text, "\t", "   ")
	}
	wrapped := tui.WrapTextWithAnsi(normalized, max(1, width))
	out := make([]string, len(wrapped))
	for i, line := range wrapped {
		if pad := width - tui.VisibleWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		out[i] = line
	}
	return out
}

// cardBlock is textBlock (text.ts:41-43) rendered at width; the name differs from the TS one
// because test fixtures declare a package-level textBlock.
func cardBlock(text string, theme sdk.UITheme, color outputColor) rowView {
	var styled string
	if len(text) > 0 {
		styled = theme.Fg(color, cleanText(text))
	}
	return func(width int) []string { return piText(styled, width) }
}

// cardLeadingRows is leadingRows (text.ts:45-81): the rows of `cardBlock(text)`
// wrapped only as far as a preview needs, so long text stays cheap: the first
// `count` rows are exact, and any further row means the full text has more.
func cardLeadingRows(text string, theme sdk.UITheme, color outputColor, count int) rowView {
	// One line past `count` already wraps to more rows when the text continues.
	head := cleanText(text)
	newlines := 0
	for i := 0; i < len(head); i++ {
		if head[i] == '\n' {
			if newlines == count {
				head = head[:i]
				break
			}
			newlines++
		}
	}
	return func(width int) []string {
		for limit := (count+1)*width + 1024; ; limit *= 2 {
			// The original cuts at the grapheme boundary containing UTF-16
			// unit `limit`; any boundary at or after it wraps the same rows
			// except the last, which callers never show (text.ts:64-65).
			end := safeCut(head, limit)
			var prefix string
			prefix = theme.Fg(color, head[:end])
			if len(text) == 0 {
				prefix = ""
			}
			rows := piText(prefix, width)
			if end == len(head) || len(rows) > count {
				return rows
			}
		}
	}
}

// safeCutScan bounds the scan for a cut point: text with no safe boundary within it (one huge grapheme cluster)
// is wrapped whole.
const safeCutScan = 4096

// markRune reports whether r may join the grapheme cluster of its neighbour: every Grapheme_Cluster_Break value that
// glues (Extend: Mn, Me, Other_Grapheme_Extend, ZWNJ, emoji modifiers, tag characters; ZWJ; SpacingMark: Mc,
// U+0E33, U+0EB3) and the format characters (Cf: Prepend such as U+0600, ZWJ, bidi and other controls). It is a
// superset of the true classes, so a boundary test built on it only ever answers "safe" for a real boundary.
func markRune(r rune) bool {
	if r < 0x300 {
		return r == 0xad
	}
	if r >= 0xd800 && r <= 0xdfff { // a lone surrogate may pair with its neighbour in UTF-16
		return true
	}
	switch {
	case r == 0x0e33 || r == 0x0eb3, r >= 0x1f3fb && r <= 0x1f3ff, r >= 0xe0020 && r <= 0xe007f:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc, unicode.Other_Grapheme_Extend, unicode.Join_Control, unicode.Cf)
}

// prependRune lists the Grapheme_Cluster_Break=Prepend characters outside Cf (Cf is in markRune).
func prependRune(r rune) bool {
	switch r {
	case 0x0d4e, 0x111c2, 0x111c3, 0x1193f, 0x11941, 0x11a3a, 0x11d46, 0x11f02:
		return true
	}
	return r >= 0x11a84 && r <= 0x11a89
}

func regionalIndicator(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

// hangulRune covers the conjoining Jamo blocks and the precomposed syllables (rules GB6-GB8).
func hangulRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x11ff) || (r >= 0xa960 && r <= 0xa97f) || (r >= 0xac00 && r <= 0xd7ff)
}

// boundaryBetween reports whether the UAX #29 extended grapheme cluster rules (15.1+) certainly break between a
// and b. Every rule that forbids a break is excluded explicitly: GB3 (CR LF: a is never CR here, clean removes it),
// GB6-8 (Hangul pairs), GB9 and GB9a (b is a mark), GB9b (a is Prepend), GB9c (a Linker, a Mark, so a is a mark),
// GB11 (a is ZWJ, a Cf), GB12 and GB13 (two regional indicators). All other pairs break (GB999). That is not every
// boundary, only boundaries that are certain.
func boundaryBetween(a, b rune) bool {
	if a == '\r' || markRune(a) || markRune(b) || prependRune(a) || (regionalIndicator(a) && regionalIndicator(b)) || (hangulRune(a) && hangulRune(b)) {
		return false
	}
	return true
}

// safeCut returns the byte offset of the first certain grapheme boundary at or after the UTF-16 offset limit of s,
// or len(s). The original cuts at the boundary containing the unit (text.ts:64-66); any boundary at or after it
// wraps the same first rows, because greedy wrapping of the prefix is that of the whole text up to the last
// token, which starts beyond the rows the callers show.
func safeCut(s string, limit int) int {
	units := 0
	i := 0
	prevAt := 0
	for i < len(s) && units < limit {
		size, n := decodeUnit(s, i)
		prevAt = i
		i += size
		units += n
	}
	if i >= len(s) {
		return len(s)
	}
	prev := runeAtUnit(s, prevAt)
	scanned := 0
	for i < len(s) && scanned < safeCutScan {
		size, _ := decodeUnit(s, i)
		r := runeAtUnit(s, i)
		if boundaryBetween(prev, r) {
			return i
		}
		prev = r
		i += size
		scanned++
	}
	return len(s)
}

// runeAtUnit is the code point at byte offset i; a lone surrogate (WTF-8) is itself, so it never joins a cut.
func runeAtUnit(s string, i int) rune {
	if isWTF8Surrogate(s, i) {
		return 0xd800
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}

// cardHint is hint (text.ts:83-88).
func cardHint(theme sdk.UITheme, width int, key string) string {
	label := "… details hidden"
	if len(key) > 0 {
		label = "… " + key + " to expand"
	}
	return tui.TruncateToWidth(theme.Fg(colorDim, label), width, tui.DefaultEllipsis, false)
}

// cardItoa formats an int for labels.
func cardItoa(n int) string { return strconv.Itoa(n) }
