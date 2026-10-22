package stretchtext

import (
	"strings"
)

// How a reply marks its stretches. `{{short|long}}` is a stretch: the reader sees `short`, and opening
// it puts `long` in its place; `long` may hold further stretches. Code spans and fences are copied as
// written, so braces in code never open a stretch. A port of src/parse.ts of pi-stretchtext, itself a
// port of cc-stretch's hooks/parse.ts. Copyright (c) 2026 Michal Pikosz and pro-vi, MIT License.

// Segment is a run of text or a Stretch.
type Segment struct {
	Text    string
	Stretch *Stretch
}

// Stretch is one `{{short|long}}`.
type Stretch struct {
	// Key names the stretch by its trimmed short form, each line break in it written ↵, and its
	// parents' (`cache › key`), so a stretch added elsewhere in the reply leaves every other key as it
	// was. A short form repeated among siblings takes `~2`, `~3`. "" when no `|` was written.
	Key   string
	Short string
	// Long is the long form, valid when HasLong. False when no `|` was written: then there is nothing
	// to open, and Short is plain text.
	Long    []Segment
	HasLong bool
	// Closed is false while the reply streams and the closing braces have not arrived.
	Closed bool
}

type markKind uint8

const (
	markText  markKind = iota
	markOpen           // {{
	markBar            // |
	markClose          // }}
)

// lexeme is a mark, or text up to the next one, code copied as written included.
type lexeme struct {
	kind markKind
	text string
}

const keySeparator = " › "

// Parse splits a reply into text and stretches.
func Parse(text string) []Segment {
	segments, _, _ := readLong(lex(text), 0, nil)
	return segments
}

// Stretches lists every stretch, outermost first, in the order the reply writes them.
func Stretches(segments []Segment) []*Stretch {
	var out []*Stretch
	var walk func([]Segment)
	walk = func(segments []Segment) {
		for _, segment := range segments {
			if segment.Stretch != nil {
				out = append(out, segment.Stretch)
				walk(segment.Stretch.Long)
			}
		}
	}
	walk(segments)
	return out
}

// ParentOf is the key of the stretch holding the one keyed key, ok false when no stretch holds it.
func ParentOf(key string) (string, bool) {
	at := strings.LastIndex(key, keySeparator)
	if at == -1 {
		return "", false
	}
	return key[:at], true
}

func (s *Stretch) isOpenable() bool { return s.Closed && s.HasLong }

// IsLinkable reports whether a stretch is drawn as a link: openable, with its short form on one
// line, as a link cannot span lines.
func (s *Stretch) IsLinkable() bool { return s.isOpenable() && !strings.Contains(s.Short, "\n") }

// DrawnAs is how the terminal draws a stretch.
type DrawnAs uint8

const (
	// DrawnText is plain text: there is nothing to open.
	DrawnText DrawnAs = iota
	// DrawnOpen is already open: the short form spans lines.
	DrawnOpen
	// DrawnLink is a link.
	DrawnLink
)

// DrawnAs reports how the terminal draws the stretch.
func (s *Stretch) DrawnAs() DrawnAs {
	if !s.isOpenable() {
		return DrawnText
	}
	if s.IsLinkable() {
		return DrawnLink
	}
	return DrawnOpen
}

// readLong reads the segments from lexeme at on, to the `}}` closing the stretch keyed parent, or to
// the end for the reply itself, which no `}}` closes (parent nil). A `|` here is text.
func readLong(lexemes []lexeme, at int, parent *string) (segments []Segment, closed bool, next int) {
	var siblings map[string]int
	var pending strings.Builder
	flush := func() {
		if pending.Len() > 0 {
			segments = append(segments, Segment{Text: pending.String()})
			pending.Reset()
		}
	}
	for i := at; i < len(lexemes); {
		lx := lexemes[i]
		if lx.kind == markClose && parent != nil {
			flush()
			return segments, true, i + 1
		}
		if lx.kind == markOpen {
			flush()
			if siblings == nil {
				siblings = map[string]int{}
			}
			stretch, stop := readStretch(lexemes, i+1, parent, siblings)
			segments = append(segments, Segment{Stretch: stretch})
			i = stop
			continue
		}
		pending.WriteString(textOf(lx))
		i++
	}
	flush()
	return segments, false, len(lexemes)
}

// readStretch reads the stretch whose `{{` stands before lexeme at. Its short form reads to a `|` or
// `}}`; a `{{` there is text.
func readStretch(lexemes []lexeme, at int, parent *string, siblings map[string]int) (*Stretch, int) {
	var short strings.Builder
	for i := at; i < len(lexemes); i++ {
		lx := lexemes[i]
		switch lx.kind {
		case markClose:
			return &Stretch{Short: short.String(), Closed: true}, i + 1
		case markBar:
			text := short.String()
			key := keyOf(text, parent, siblings)
			long, closed, stop := readLong(lexemes, i+1, &key)
			return &Stretch{Key: key, Short: text, Long: long, HasLong: true, Closed: closed}, stop
		}
		short.WriteString(textOf(lx))
	}
	return &Stretch{Short: short.String()}, len(lexemes)
}

func textOf(lx lexeme) string {
	switch lx.kind {
	case markOpen:
		return "{{"
	case markBar:
		return "|"
	case markClose:
		return "}}"
	}
	return lx.text
}

func keyOf(short string, parent *string, siblings map[string]int) string {
	// A line break in a key would split the link's address across rows; ↵ keeps the key apart from
	// the same words on one line, which a space would not.
	name := strings.ReplaceAll(trim(short), "\n", "↵")
	siblings[name]++
	own := name
	if seen := siblings[name]; seen != 1 {
		own = name + "~" + itoa(seen)
	}
	if parent != nil {
		return *parent + keySeparator + own
	}
	return own
}

func itoa(n int) string {
	var buf [20]byte
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(buf[i:])
}

func lex(text string) []lexeme {
	var lexemes []lexeme
	appendText := func(s string) {
		if n := len(lexemes); n > 0 && lexemes[n-1].kind == markText {
			lexemes[n-1].text += s
		} else {
			lexemes = append(lexemes, lexeme{kind: markText, text: s})
		}
	}
	for at := 0; at < len(text); {
		if stop, ok := codeEnd(text, at); ok {
			appendText(text[at:stop])
			at = stop
			continue
		}
		switch {
		case strings.HasPrefix(text[at:], "{{"):
			lexemes = append(lexemes, lexeme{kind: markOpen})
			at += 2
		case text[at] == '|':
			lexemes = append(lexemes, lexeme{kind: markBar})
			at++
		case strings.HasPrefix(text[at:], "}}"):
			lexemes = append(lexemes, lexeme{kind: markClose})
			at += 2
		default:
			// One byte: every mark is ASCII, so a multi-byte character is copied whole by its bytes.
			appendText(text[at : at+1])
			at++
		}
	}
	return lexemes
}

// codeEnd is where code starting at at ends: a fence opening a line, or a code span.
func codeEnd(text string, at int) (int, bool) {
	if at == 0 || text[at-1] == '\n' {
		if stop, ok := fenceEnd(text, at); ok {
			return stop, true
		}
	}
	if text[at] == '`' {
		return codeSpanEnd(text, at), true
	}
	return 0, false
}

// codeSpanEnd is where the code span at at stops: past the next run of as many backticks, or at the
// text's end.
func codeSpanEnd(text string, at int) int {
	n := 1
	for at+n < len(text) && text[at+n] == '`' {
		n++
	}
	run := text[at : at+n]
	end := strings.Index(text[at+n:], run)
	if end == -1 {
		return len(text)
	}
	return at + n + end + n
}

func lineEnd(text string, from int) int {
	end := strings.IndexByte(text[from:], '\n')
	if end == -1 {
		return len(text)
	}
	return from + end
}

// fenceEnd is where a fenced code block opening at start ends (past its closing line), ok false when
// no fence opens there.
func fenceEnd(text string, start int) (int, bool) {
	opening := lineEnd(text, start)
	fence, ok := openingFence(text[start:opening])
	if !ok {
		return 0, false
	}
	for at := opening + 1; at < len(text); at = lineEnd(text, at) + 1 {
		end := lineEnd(text, at)
		if closesFence(text[at:end], fence) {
			return min(end+1, len(text)), true
		}
	}
	return len(text), true
}

// openingFence is the marker line opens a code fence with, such as ``` or ~~~: /^[ \t]*(`{3,}|~{3,})/.
func openingFence(line string) (string, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return "", false
	}
	char := line[i]
	// The regex takes the longest run: `{3,} is greedy and nothing follows it.
	j := i
	for j < len(line) && line[j] == char {
		j++
	}
	if j-i < 3 {
		return "", false
	}
	return line[i:j], true
}

// closesFence reports whether line closes the code fence fence opened: trimmed, it is only the
// fence's character, repeated at least as often.
func closesFence(line, fence string) bool {
	trimmed := trim(line)
	if !strings.HasPrefix(trimmed, fence) {
		return false
	}
	return strings.Count(trimmed, fence[:1]) == len(trimmed)
}
