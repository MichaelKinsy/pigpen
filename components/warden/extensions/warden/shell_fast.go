package warden

import (
	"strings"
	"unicode/utf8"
)

// Hand-written forms of the small expressions the shell reader runs on every segment of every tool call
// (reSplitShell, reAssignment, reLeadingPath, `s.split(/\s+/)`). Go's regexp engine spends a pooled backtracker
// setup (about 150 ns) on each call however short the text; these are one pass, with no allocation beyond the
// result. shell_fast_test.go compares each with its expression over generated text.

// splitShell is the trimmed, non-empty pieces of command between `\n`, `;`, `&&`, `||`, `|` and `&`
// (reSplitShell.Split followed by the trim and the empty filter).
func splitShell(command string) []string {
	var out []string
	start := 0
	piece := func(end int) {
		if p := jsTrim(command[start:end]); p != "" {
			out = append(out, p)
		}
	}
	for i := 0; i < len(command); i++ {
		switch command[i] {
		case '\n', ';':
			piece(i)
			start = i + 1
		case '&', '|':
			piece(i)
			if i+1 < len(command) && command[i+1] == command[i] {
				i++
			}
			start = i + 1
		}
	}
	piece(len(command))
	return out
}

// isAssignment is `^[A-Za-z_][A-Za-z0-9_]*=` (reAssignment): a word that starts a `NAME=value` assignment.
func isAssignment(word string) bool {
	if word == "" || !(word[0] == '_' || word[0] >= 'a' && word[0] <= 'z' || word[0] >= 'A' && word[0] <= 'Z') {
		return false
	}
	for i := 1; i < len(word); i++ {
		c := word[i]
		if c == '=' {
			return true
		}
		if !isWordByte(c) {
			return false
		}
	}
	return false
}

// stripPathPrefix is `^.*/` removed (reLeadingPath): everything up to the last `/` of the first line.
func stripPathPrefix(s string) string {
	line := s
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		line = s[:nl]
	}
	if slash := strings.LastIndexByte(line, '/'); slash >= 0 {
		return s[slash+1:]
	}
	return s
}

// nextJSWord is the next run of non-whitespace (JavaScript's `\s`) at or after from, as a [start, end) range; both are
// len(s) when none is left. A byte that is not valid UTF-8 is a word byte (the expression does not match it).
func nextJSWord(s string, from int) (start, end int) {
	i := from
	for i < len(s) {
		c := s[i]
		if c < utf8.RuneSelf {
			if !isJSSpace(rune(c)) {
				break
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			break
		}
		i += size
	}
	start = i
	for i < len(s) {
		c := s[i]
		if c < utf8.RuneSelf {
			if isJSSpace(rune(c)) {
				break
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if isJSSpace(r) {
			break
		}
		i += size
	}
	return start, i
}

// headOf is the command word of a segment after assignments and wrappers, with any directory removed.
func headOf(segment string) (string, bool) {
	s := jsTrim(segment)
	if !utf8.ValidString(s) {
		return headOfTokens(jsSplitWhitespace(s))
	}
	for pos := 0; ; {
		start, end := nextJSWord(s, pos)
		if start == end {
			return "", false
		}
		if word := s[start:end]; !isAssignment(word) && !wrappers[word] {
			return stripPathPrefix(word), true
		}
		pos = end
	}
}

// headOfTokens is headOf over the words of a segment, split the way JavaScript's `split(/\s+/)` does (which turns a byte
// that is not valid UTF-8 into U+FFFD).
func headOfTokens(tokens []string) (string, bool) {
	i := 0
	for i < len(tokens) && (isAssignment(tokens[i]) || wrappers[tokens[i]]) {
		i++
	}
	if i >= len(tokens) || tokens[i] == "" {
		return "", false
	}
	return stripPathPrefix(tokens[i]), true
}
