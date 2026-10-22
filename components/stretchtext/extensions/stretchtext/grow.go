package stretchtext

import (
	"math"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/pigpen/components/pitui/wordsegmenter"
)

// How a long form grows around its short form. A port of src/grow.ts of pi-stretchtext, itself a
// port of cc-stretch's hooks/grow.ts, with `breakables` from its hooks/layout.ts. Copyright (c) 2026
// Michal Pikosz and pro-vi, MIT License.
//
// A word here is a piece a row may break between (breakables), so Chinese grows a character at a
// time, with the punctuation that sticks to it. The short form's words, found in the long form in
// order, are anchors and never move; the words between them arrive from the anchors outward, so the
// sentence widens at every seam at once, to the left of an anchor as much as to its right. Words
// arrive whole: a terminal has no room to fade one in, and half a word reads as a typo.

type tokenKind uint8

const (
	tokWord tokenKind = iota
	tokSpace
	tokNested
)

type token struct {
	kind    tokenKind
	text    string
	stretch *Stretch
}

// atom is a word or a run of spaces of the long form (stretch nil), or a nested stretch.
type atom struct {
	text    string
	stretch *Stretch
}

func (a atom) isSpace() bool { return a.stretch == nil && isAllSpace(a.text) }

type piece struct {
	anchor bool
	atom   atom   // an anchor
	atoms  []atom // a run
}

type side uint8

const (
	sideBefore side = iota
	sideBetween
	sideAfter
)

type placed struct {
	token int
	word  string
}

var (
	// unspacedScripts are written without spaces, where a row may break between any two characters.
	unspacedTables = []*unicode.RangeTable{unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Bopomofo}
	// noStartExtra are the characters a row may not start with, besides \p{Pe} and \p{Pf}.
	noStartExtra = ",.:;!?，。、；：？！…·ぁぃぅぇぉっゃゅょゎゕゖァィゥェォッャュョヮヵヶーゝゞヽヾ々"
	// segmented scripts are written without spaces whose words only a dictionary finds.
	segmentedTables = []*unicode.RangeTable{unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar}
)

// hasSegmented is /[\p{Script=Thai}\p{Script=Lao}\p{Script=Khmer}\p{Script=Myanmar}]/u.test(text).
func hasSegmented(text string) bool {
	for _, r := range text {
		if unicode.In(r, segmentedTables...) {
			return true
		}
	}
	return false
}

func isUnspaced(r rune) bool {
	return unicode.In(r, unspacedTables...) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef)
}

func noStart(r rune) bool {
	return unicode.In(r, unicode.Pe, unicode.Pf) || strings.ContainsRune(noStartExtra, r)
}

func noEnd(r rune) bool { return unicode.In(r, unicode.Ps, unicode.Pi) }

// zeroWidth is /[\p{Mn}\p{Me}\p{Default_Ignorable_Code_Point}]/u: a mark drawn on the letter before
// it, or a character no terminal draws, such as a joiner.
func zeroWidth(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me) {
		return true
	}
	if unicode.Is(unicode.White_Space, r) || (r >= 0xfff9 && r <= 0xfffb) || (r >= 0x13430 && r <= 0x13440) {
		return false
	}
	switch r {
	case 0x600, 0x601, 0x602, 0x603, 0x604, 0x605, 0x6dd, 0x70f, 0x890, 0x891, 0x8e2, 0x110bd, 0x110cd:
		return false
	}
	return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Cf, r)
}

// Grow is the long form at progress: 0 shows only the anchors, 1 shows all of it. draw writes a
// nested stretch, which is never shown in part; paint marks each word the long form adds, so the
// reader can tell it from the words they already read.
func Grow(short string, long []Segment, progress float64, draw func(*Stretch) string, paint func(string) string) string {
	tokens := tokenize(long)
	pieces := piecesOf(tokens, anchorsOf(short, tokens))
	var out strings.Builder
	added := func(a atom) {
		switch {
		case a.stretch != nil:
			out.WriteString(draw(a.stretch))
		case hasNonSpace(a.text):
			out.WriteString(paint(a.text))
		default:
			out.WriteString(a.text)
		}
	}
	for i, p := range pieces {
		if p.anchor {
			if p.atom.stretch != nil {
				out.WriteString(draw(p.atom.stretch))
			} else {
				out.WriteString(p.atom.text)
			}
			continue
		}
		// Runs never touch, so a piece before or after a run is an anchor.
		for _, a := range reveal(p.atoms, sideOf(i > 0, i < len(pieces)-1), progress) {
			added(a)
		}
	}
	return out.String()
}

// LengthOf is how many words and nested stretches a growth adds, to pace it.
func LengthOf(long []Segment) int {
	n := 0
	for _, t := range tokenize(long) {
		if t.kind != tokSpace {
			n++
		}
	}
	return n
}

func piecesOf(tokens []token, anchors map[int]struct{}) []piece {
	var pieces []piece
	for i, t := range tokens {
		a := atom{text: t.text, stretch: t.stretch}
		_, isAnchor := anchors[i]
		if isAnchor && t.kind != tokSpace {
			pieces = append(pieces, piece{anchor: true, atom: a})
		} else if n := len(pieces); n > 0 && !pieces[n-1].anchor {
			pieces[n-1].atoms = append(pieces[n-1].atoms, a)
		} else {
			pieces = append(pieces, piece{atoms: []atom{a}})
		}
	}
	return pieces
}

func sideOf(hasAnchorBefore, hasAnchorAfter bool) side {
	switch {
	case hasAnchorBefore && hasAnchorAfter:
		return sideBetween
	case hasAnchorAfter:
		return sideBefore
	}
	return sideAfter
}

func isSpaceAt(run []atom, i int) bool { return i >= 0 && i < len(run) && run[i].isSpace() }

// reveal is the atoms of run shown at progress, arriving from the anchors on side of it.
func reveal(run []atom, s side, progress float64) []atom {
	// Two anchors stay a space apart from the start, as they stood in the short form.
	if s == sideBetween && len(run) > 1 && run[0].isSpace() && run[len(run)-1].isSpace() {
		middle := reveal(run[1:len(run)-1], s, progress)
		if len(middle) == 0 {
			return []atom{run[0]}
		}
		out := make([]atom, 0, len(middle)+2)
		out = append(out, run[0])
		out = append(out, middle...)
		return append(out, run[len(run)-1])
	}
	if s == sideBetween && len(run) > 0 && run[0].isSpace() {
		rest := reveal(run[1:], s, progress)
		return append([]atom{run[0]}, rest...)
	}

	count := int(math.Floor(float64(len(run))*math.Min(1, math.Max(0, progress)) + 0.5))
	switch s {
	case sideAfter:
		return run[:count]
	case sideBefore:
		return run[len(run)-count:]
	}

	left := run[:(count+1)/2]
	right := run[len(run)-count/2:]
	if len(left)+len(right) == len(run) {
		return concat(left, right)
	}
	// The two halves meet where words are still missing: exactly one space between them. The space
	// is added only where the long form has one beside the gap; a gap between Chinese characters
	// joins without one, as the finished text reads.
	isGapSpaced := isSpaceAt(run, len(left)) || isSpaceAt(run, len(run)-len(right)-1)
	leftEndsSpace := len(left) > 0 && left[len(left)-1].isSpace()
	rightStartsSpace := len(right) > 0 && right[0].isSpace()
	if leftEndsSpace && rightStartsSpace {
		return concat(left, right[1:])
	}
	if !leftEndsSpace && !rightStartsSpace && len(left) > 0 && len(right) > 0 && isGapSpaced {
		out := make([]atom, 0, len(left)+len(right)+1)
		out = append(out, left...)
		out = append(out, atom{text: " "})
		return append(out, right...)
	}
	return concat(left, right)
}

func concat(a, b []atom) []atom {
	out := make([]atom, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

func tokenize(long []Segment) []token {
	var tokens []token
	for _, part := range long {
		if part.Stretch != nil {
			tokens = append(tokens, token{kind: tokNested, stretch: part.Stretch})
			continue
		}
		for _, text := range wordsAndSpaces(part.Text) {
			if isAllSpace(text) {
				tokens = append(tokens, token{kind: tokSpace, text: text})
				continue
			}
			for _, word := range piecesOfWord(text) {
				tokens = append(tokens, token{kind: tokWord, text: word})
			}
		}
	}
	return tokens
}

// piecesOfWord is a word cut where a row may break, or whole when it holds code or math.
func piecesOfWord(word string) []string {
	if hasSpan(word) {
		return []string{word}
	}
	return breakables(word)
}

// breakables cuts one part of a line split at spaces into the pieces a row may break between and
// growth adds one at a time; a run of spaces stays whole.
func breakables(text string) []string {
	// Nothing below Thai breaks, so a Latin word or a run of spaces comes back whole without a look at
	// each character.
	needs := false
	for _, r := range text {
		if r > 0xdff {
			needs = true
			break
		}
	}
	if !needs {
		return []string{text}
	}
	var pieces []string
	// Thai, Lao, Khmer and Myanmar words are found by a dictionary: the start of each segment of the
	// word segmenter (the original's Intl.Segmenter, ICU's dictionaries) is a place a row may break.
	var wordStarts map[int]struct{}
	if hasSegmented(text) {
		wordStarts = map[int]struct{}{}
		for segment := range wordsegmenter.Segments(text) {
			wordStarts[segment.Index] = struct{}{}
		}
	}
	start := 0
	var last rune
	haveLast := false
	isAfterUnspaced := false
	at := 0 // the UTF-16 index of char, as the segments count
	for i, char := range text {
		unspaced := isUnspaced(char)
		_, atWordStart := wordStarts[at]
		isWordStart := atWordStart && (unicode.In(char, segmentedTables...) || (haveLast && unicode.In(last, segmentedTables...)))
		mayBreak := unspaced || isAfterUnspaced || isWordStart
		if i > start && mayBreak && !noStart(char) && !(haveLast && noEnd(last)) && !zeroWidth(char) {
			pieces = append(pieces, text[start:i])
			start = i
		}
		last, haveLast = char, true
		isAfterUnspaced = unspaced || (isAfterUnspaced && noStart(char))
		at++
		if char >= 0x10000 {
			at++
		}
	}
	if start < len(text) {
		pieces = append(pieces, text[start:])
	}
	return pieces
}

// anchorsOf is the token indexes of the long form that carry the short form's words, as many as can
// be kept in order. A nested stretch shows its own short form, so its words count where it stands.
func anchorsOf(short string, tokens []token) map[int]struct{} {
	var want []string
	for _, w := range wordsIn(short) {
		if b := bare(w); b != "" {
			want = append(want, b)
		}
	}
	var have []placed
	for i, t := range tokens {
		for _, w := range wordsOf(t) {
			if b := bare(w); b != "" {
				have = append(have, placed{token: i, word: b})
			}
		}
	}
	anchors := map[int]struct{}{}
	found, ok := inOrder(want, have)
	if !ok {
		found = longestCommon(want, have)
	}
	for _, i := range found {
		anchors[i] = struct{}{}
	}

	// Punctuation standing on its own right after an anchor (`{{...}}.`) belongs to that anchor.
	last := -1
	for i, t := range tokens {
		if t.kind == tokSpace {
			continue
		}
		if _, ok := anchors[last]; t.kind == tokWord && bare(t.text) == "" && ok {
			anchors[i] = struct{}{}
		}
		last = i
	}
	return anchors
}

func wordsIn(text string) []string {
	var out []string
	for _, w := range wordsAndSpaces(text) {
		if !isAllSpace(w) {
			out = append(out, piecesOfWord(w)...)
		}
	}
	return out
}

func wordsOf(t token) []string {
	switch t.kind {
	case tokWord:
		return []string{t.text}
	case tokNested:
		return wordsIn(t.stretch.Short)
	}
	return nil
}

// inOrder is the tokens carrying the earliest match of each wanted word, ok false when some word
// finds no place. The guide asks the long form to keep every word of the short form, in order, and
// when it does, these are already the longest common subsequence: a Chinese long form, with a word
// per character, never pays for the table longestCommon builds.
func inOrder(want []string, have []placed) ([]int, bool) {
	found := make([]int, 0, len(want))
	for _, h := range have {
		if len(found) == len(want) {
			break
		}
		if h.word == want[len(found)] {
			found = append(found, h.token)
		}
	}
	return found, len(found) == len(want)
}

// longestCommon is the tokens carrying the most wanted words that can be kept in order, a longest
// common subsequence.
func longestCommon(want []string, have []placed) []int {
	// lengths[i][j]: the longest common run of want[i..] and have[j..].
	width := len(have) + 1
	lengths := make([]int, (len(want)+1)*width)
	for i := len(want) - 1; i >= 0; i-- {
		for j := len(have) - 1; j >= 0; j-- {
			if want[i] == have[j].word {
				lengths[i*width+j] = lengths[(i+1)*width+j+1] + 1
			} else {
				lengths[i*width+j] = max(lengths[(i+1)*width+j], lengths[i*width+j+1])
			}
		}
	}
	var found []int
	for i, j := 0, 0; i < len(want) && j < len(have); {
		switch {
		case want[i] == have[j].word:
			found = append(found, have[j].token)
			i++
			j++
		case lengths[(i+1)*width+j] >= lengths[i*width+j+1]:
			i++
		default:
			j++
		}
	}
	return found
}
