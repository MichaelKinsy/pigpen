package commit

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/commit/selective/preview.ts

const (
	wideLine    = 240
	wordContext = 40
	maxWordWork = 1 << 24
)

var (
	wordMarks = [4]string{"[-", "-]", "{+", "+}"}
	hunkRE    = regexp.MustCompile(`^@@ -\d+,(\d+) \+\d+,(\d+) @@$`)
)

// utf16Len returns the UTF-16 code unit length of s (matching JS string.length).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isWordRune classifies runes for UAX#29-like word segmentation matching
// Intl.Segmenter("en", { granularity: "word" }) on prose, identifiers, and CJK.
func isWordRune(r rune) bool {
	if r == '_' {
		return true
	}
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

func isHanRune(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func isHiraganaRune(r rune) bool {
	return unicode.Is(unicode.Hiragana, r)
}

func isKatakanaRune(r rune) bool {
	return unicode.Is(unicode.Katakana, r)
}

// splitWords segments line into words, whitespace runs, and punctuation/symbols
// with byte start offsets (matching Intl.Segmenter("en", { granularity: "word" })).
// upstream: preview.ts:29
func splitWords(line string) (words []string, starts []int) {
	i := 0
	n := len(line)
	for i < n {
		start := i
		r, size := utf8.DecodeRuneInString(line[i:])
		switch {
		case r == ' ' || r == '\t' || r == '\f' || r == '\v' || unicode.Is(unicode.Zs, r):
			i += size
			for i < n {
				nr, nsize := utf8.DecodeRuneInString(line[i:])
				if nr == ' ' || nr == '\t' || nr == '\f' || nr == '\v' || unicode.Is(unicode.Zs, nr) {
					i += nsize
				} else {
					break
				}
			}
		case isWordRune(r):
			i += size
			for i < n {
				nr, nsize := utf8.DecodeRuneInString(line[i:])
				if isWordRune(nr) {
					i += nsize
					continue
				}
				// MidNumLet / MidLetter (apostrophe/dot) between letters/digits as in UAX #29.
				if (nr == '\'' || nr == '\u2019' || nr == '.') && i+nsize < n {
					after, _ := utf8.DecodeRuneInString(line[i+nsize:])
					if (nr == '.' && unicode.IsDigit(r) && unicode.IsDigit(after)) ||
						((nr == '\'' || nr == '\u2019') && unicode.IsLetter(r) && unicode.IsLetter(after)) {
						i += nsize
						r = nr
						continue
					}
				}
				break
			}
		case isHanRune(r):
			i += size
			for i < n {
				nr, nsize := utf8.DecodeRuneInString(line[i:])
				if isHanRune(nr) {
					i += nsize
				} else {
					break
				}
			}
		case isHiraganaRune(r):
			i += size
			for i < n {
				nr, nsize := utf8.DecodeRuneInString(line[i:])
				if isHiraganaRune(nr) {
					i += nsize
				} else {
					break
				}
			}
		case isKatakanaRune(r):
			i += size
			for i < n {
				nr, nsize := utf8.DecodeRuneInString(line[i:])
				if isKatakanaRune(nr) {
					i += nsize
				} else {
					break
				}
			}
		default:
			i += size
		}
		words = append(words, line[start:i])
		starts = append(starts, start)
	}
	starts = append(starts, n)
	return words, starts
}

func markedLength(removed, added string) int {
	total := 0
	if removed != "" {
		total += utf16Len(removed) + 4
	}
	if added != "" {
		total += utf16Len(added) + 4
	}
	return total
}

// cutKept trims unchanged text to its first left and last right UTF-16 code units,
// never splitting a surrogate pair, with "…" standing for the rest.
// upstream: preview.ts:46
func cutKept(text string, left, right int) string {
	units := utf16.Encode([]rune(text))
	head := left
	tail := len(units) - right
	if tail-head < 2 {
		return text
	}
	if head > 0 && (units[head-1]&0xfc00) == 0xd800 {
		head--
	}
	if tail < len(units) && (units[tail]&0xfc00) == 0xdc00 {
		tail++
	}
	if head < 0 {
		head = 0
	}
	if tail > len(units) {
		tail = len(units)
	}
	return string(utf16.Decode(units[:head])) + "…" + string(utf16.Decode(units[tail:]))
}

func wordRow(before, after string, columns int, work *int) (string, bool) {
	oldWords, oldStarts := splitWords(before)
	newWords, newStarts := splitWords(after)

	shorter := min(len(oldWords), len(newWords))
	head := 0
	for head < shorter && oldWords[head] == newWords[head] {
		head++
	}
	tail := 0
	for tail < shorter-head && oldWords[len(oldWords)-1-tail] == newWords[len(newWords)-1-tail] {
		tail++
	}
	oldMiddle := oldWords[head : len(oldWords)-tail]
	newMiddle := newWords[head : len(newWords)-tail]
	worst := len(oldMiddle) * len(newMiddle)
	if worst > *work {
		return "", false
	}
	regions := ChangedRegions(oldMiddle, newMiddle)
	changed := 0
	for _, r := range regions {
		changed += r.I2 - r.I1 + r.J2 - r.J1
	}
	cost := (len(oldMiddle) + len(newMiddle)) * changed
	if worst < cost {
		cost = worst
	}
	*work -= cost

	var kept []string
	var changes [][2]string
	cursor := 0
	for _, r := range regions {
		oldStart := oldStarts[head+r.I1]
		gap := before[cursor:oldStart]
		removed := before[oldStart:oldStarts[head+r.I2]]
		added := after[newStarts[head+r.J1]:newStarts[head+r.J2]]
		cursor = oldStart + len(removed)
		if len(changes) > 0 {
			last := &changes[len(changes)-1]
			if markedLength(last[0]+gap+removed, last[1]+gap+added) <=
				markedLength(last[0], last[1])+utf16Len(gap)+markedLength(removed, added) {
				last[0] += gap + removed
				last[1] += gap + added
				continue
			}
		}
		kept = append(kept, gap)
		changes = append(changes, [2]string{removed, added})
	}
	kept = append(kept, before[cursor:])

	longer := max(utf16Len(before), utf16Len(after))
	marked := 1
	for _, ch := range changes {
		marked += markedLength(ch[0], ch[1])
	}
	if len(changes) == 0 || marked > longer {
		return "", false
	}
	removals, additions := 0, 0
	for _, ch := range changes {
		if ch[0] != "" {
			removals++
		}
		if ch[1] != "" {
			additions++
		}
	}
	marks := [4]int{removals, removals, additions, additions}

	for context := wordContext; ; context >>= 1 {
		var row strings.Builder
		row.WriteByte('!')
		for n, ch := range changes {
			leftCtx := 0
			if n > 0 {
				leftCtx = context
			}
			row.WriteString(cutKept(kept[n], leftCtx, context))
			if ch[0] != "" {
				row.WriteString("[-")
				row.WriteString(ch[0])
				row.WriteString("-]")
			}
			if ch[1] != "" {
				row.WriteString("{+")
				row.WriteString(ch[1])
				row.WriteString("+}")
			}
		}
		row.WriteString(cutKept(kept[len(changes)], context, 0))
		s := row.String()
		if utf16Len(s) <= longer && (columns <= 0 || len(s) <= columns) {
			okMarks := true
			for n, mark := range wordMarks {
				if strings.Count(s, mark) != marks[n] {
					okMarks = false
					break
				}
			}
			if okMarks {
				return s, true
			}
		}
		if context == 0 {
			return "", false
		}
	}
}

// FormatPatchPreview formats a unified patch (as FormatTextPatch emits) for display
// with a column cap in bytes (0 = unlimited): long lines edited in place print as a
// single `!` word-diff row where that reads better than two lines.
// Returns patch unchanged when no line folds.
// upstream: preview.ts:144
func FormatPatchPreview(patch string, columns int) string {
	lines := strings.Split(patch, "\n")
	view := make([]string, 0, len(lines))
	work := maxWordWork
	folded := false
	index := 0
	for index < len(lines) {
		m := hunkRE.FindStringSubmatch(lines[index])
		view = append(view, lines[index])
		index++
		if m == nil {
			continue
		}
		oldLeft, _ := strconv.Atoi(m[1])
		newLeft, _ := strconv.Atoi(m[2])
		for oldLeft > 0 || newLeft > 0 {
			start := index
			if index < len(lines) && strings.HasPrefix(lines[index], " ") {
				index++
				oldLeft--
				newLeft--
				if index < len(lines) && lines[index] == noEOLMarker {
					index++
				}
				view = append(view, lines[start:index]...)
				continue
			}
			for oldLeft > 0 && index < len(lines) && strings.HasPrefix(lines[index], "-") {
				index++
				oldLeft--
			}
			removedEnd := index
			oldNoEol := index < len(lines) && lines[index] == noEOLMarker
			if oldNoEol {
				index++
			}
			addedStart := index
			for newLeft > 0 && index < len(lines) && strings.HasPrefix(lines[index], "+") {
				index++
				newLeft--
			}
			addedEnd := index
			newNoEol := index < len(lines) && lines[index] == noEOLMarker
			if newNoEol {
				index++
			}
			if index == start {
				return patch
			}
			count := 0
			if removedEnd-start == addedEnd-addedStart {
				count = removedEnd - start
			}
			shown := 0
			for n := range count {
				if utf16Len(lines[start+n]) <= wideLine+1 && utf16Len(lines[addedStart+n]) <= wideLine+1 {
					continue
				}
				unterminated := n == count-1 && oldNoEol
				if unterminated != (n == count-1 && newNoEol) {
					continue
				}
				row, ok := wordRow(lines[start+n][1:], lines[addedStart+n][1:], columns, &work)
				if !ok {
					continue
				}
				for m := shown; m < n; m++ {
					view = append(view, lines[start+m])
				}
				for m := shown; m < n; m++ {
					view = append(view, lines[addedStart+m])
				}
				view = append(view, row)
				if unterminated {
					view = append(view, noEOLMarker)
				}
				shown = n + 1
				folded = true
			}
			if shown == 0 {
				view = append(view, lines[start:index]...)
			} else if shown < count {
				for m := shown; m < count; m++ {
					view = append(view, lines[start+m])
				}
				if oldNoEol {
					view = append(view, noEOLMarker)
				}
				for m := shown; m < count; m++ {
					view = append(view, lines[addedStart+m])
				}
				if newNoEol {
					view = append(view, noEOLMarker)
				}
			}
		}
	}
	if !folded {
		return patch
	}
	return strings.Join(view, "\n")
}
