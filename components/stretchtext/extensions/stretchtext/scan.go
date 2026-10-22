package stretchtext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The original finds words and code or math spans with one JavaScript regular expression each. Go's
// regexp has no back-references, no lazy quantifier over a group and a different \s, so the two
// matchers below are written by hand, alternative by alternative, in the order the expression tries
// them:
//
//	SPAN = (`+)[^]*?\1 | \\\(.*?\\\) | \\\[.*?\\\] | \$\$.*?\$\$ | \$(?!\s).*?\$
//	WORD_OR_SPACES = /(?:SPAN|\S)+|\s+/g

// lazyUntil is `.*?delim` from index from: the index just past the nearest delim, ok false when a
// line terminator (which `.` does not match) comes first or there is none.
func lazyUntil(s string, from int, delim string) (int, bool) {
	for i := from; i < len(s); {
		if strings.HasPrefix(s[i:], delim) {
			return i + len(delim), true
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if isLineTerminator(r) {
			return 0, false
		}
		i += size
	}
	return 0, false
}

// spanAt is the end of the span starting at i, -1 when none starts there.
func spanAt(s string, i int) int {
	switch s[i] {
	case '`':
		n := 1
		for i+n < len(s) && s[i+n] == '`' {
			n++
		}
		// (`+) is greedy: a run with no closing run of its own length backtracks to a shorter one.
		for k := n; k >= 1; k-- {
			if at := strings.Index(s[i+k:], backticks(k)); at != -1 {
				return i + k + at + k
			}
		}
	case '\\':
		if i+1 < len(s) {
			switch s[i+1] {
			case '(':
				if end, ok := lazyUntil(s, i+2, `\)`); ok {
					return end
				}
			case '[':
				if end, ok := lazyUntil(s, i+2, `\]`); ok {
					return end
				}
			}
		}
	case '$':
		if strings.HasPrefix(s[i:], "$$") {
			if end, ok := lazyUntil(s, i+2, "$$"); ok {
				return end
			}
		}
		if i+1 < len(s) {
			if r, _ := utf8.DecodeRuneInString(s[i+1:]); isSpace(r) {
				return -1
			}
		}
		if end, ok := lazyUntil(s, i+1, "$"); ok {
			return end
		}
	}
	return -1
}

func backticks(n int) string {
	const run = "````````````````"
	if n <= len(run) {
		return run[:n]
	}
	return strings.Repeat("`", n)
}

// hasSpan is HAS_SPAN.test(word): a span starts anywhere in it.
func hasSpan(word string) bool {
	for i := 0; i < len(word); i++ {
		switch word[i] {
		case '`', '\\', '$':
			if spanAt(word, i) >= 0 {
				return true
			}
		}
	}
	return false
}

// wordsAndSpaces is text.match(WORD_OR_SPACES): the words, spans in them whole even across spaces,
// and the runs of spaces, in order.
func wordsAndSpaces(text string) []string {
	var out []string
	for p := 0; p < len(text); {
		r, size := utf8.DecodeRuneInString(text[p:])
		j := p
		if isSpace(r) {
			j += size
			for j < len(text) {
				r, size = utf8.DecodeRuneInString(text[j:])
				if !isSpace(r) {
					break
				}
				j += size
			}
		} else {
			for j < len(text) {
				if end := spanAt(text, j); end >= 0 {
					j = end
					continue
				}
				r, size = utf8.DecodeRuneInString(text[j:])
				if isSpace(r) {
					break
				}
				j += size
			}
		}
		out = append(out, text[p:j])
		p = j
	}
	return out
}

// bare is a word without its surrounding punctuation or markdown marks, lowercased, so `cache.`
// meets `**cache**`: /^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu then toLowerCase.
func bare(word string) string {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }
	start := 0
	for start < len(word) {
		r, size := utf8.DecodeRuneInString(word[start:])
		if isWord(r) {
			break
		}
		start += size
	}
	end := len(word)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(word[:end])
		if isWord(r) {
			break
		}
		end -= size
	}
	return lower(word[start:end])
}
