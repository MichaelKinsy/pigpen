package warden

import (
	"sort"
	"strings"
)

// Candidate-driven credential scanning. Go's regexp engine cannot skip ahead through a case-insensitive pattern or one
// that opens with `\b`, so a pattern like `(?i)(?:api[_-]?key|token|...)[a-z0-9_-]*\s*[=:]...` costs about a microsecond
// per input byte; V8 skips to the next candidate. These helpers find the candidate offsets with `strings.Index` (SIMD)
// and run the regexp, anchored, only there. The result equals the unanchored scan: a match must begin at a candidate,
// the scan is leftmost-first and resumes at the end of each match. `redact_fast_test.go` checks that against the plain
// regexp implementations on a corpus and on generated text.

// keyStarts are the words a credential-key alternative (`credentialKeys`) can start with, lower case.
var keyStarts = []string{"api", "access", "secret", "client", "private", "pass", "token", "credential"}

// asciiLower lower-cases ASCII letters only, so every byte offset of s is also an offset of the result.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; 'A' <= c && c <= 'Z' {
					b[j] = c + ('a' - 'A')
				}
			}
			return string(b)
		}
	}
	return s
}

// indexAll appends every offset of word in s, ascending, overlapping occurrences included.
func indexAll(s, word string, into []int) []int {
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], word)
		if i < 0 {
			break
		}
		into = append(into, from+i)
		from += i + 1
	}
	return into
}

// keyCandidates are the offsets in lower where a credential key can start, ascending and distinct.
func keyCandidates(lower string) []int {
	var out []int
	for _, w := range keyStarts {
		out = indexAll(lower, w, out)
	}
	if len(out) < 2 {
		return out
	}
	sort.Ints(out)
	n := 1
	for _, p := range out[1:] {
		if p != out[n-1] {
			out[n] = p
			n++
		}
	}
	return out[:n]
}

// urlStarts are the offsets where a `scheme://` can start: for each `://`, the first letter of the run of scheme
// characters before it (the leftmost start a `[a-z][a-z0-9+.-]*://` match can have), ascending and distinct.
func urlStarts(text string) []int {
	var out []int
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], "://")
		if i < 0 {
			break
		}
		c := from + i
		r := c
		for r > 0 {
			b := text[r-1]
			if ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9') || b == '+' || b == '.' || b == '-' {
				r--
				continue
			}
			break
		}
		for r < c && !(('a' <= text[r] && text[r] <= 'z') || ('A' <= text[r] && text[r] <= 'Z')) {
			r++
		}
		if r < c && (len(out) == 0 || out[len(out)-1] < r) {
			out = append(out, r)
		}
		from = c + 1
	}
	return out
}

// rewriteAt applies try at each candidate offset in ascending order. try reports the end of a match that begins at
// the offset and what replaces it; the scan resumes at that end, as a regexp's replace-all does.
func rewriteAt(text string, cands []int, try func(pos int) (end int, repl string, ok bool)) (string, bool) {
	var b strings.Builder
	last, changed := 0, false
	for _, p := range cands {
		if p < last {
			continue
		}
		end, repl, ok := try(p)
		if !ok {
			continue
		}
		b.WriteString(text[last:p])
		b.WriteString(repl)
		last, changed = end, true
	}
	if !changed {
		return text, false
	}
	b.WriteString(text[last:])
	return b.String(), true
}

// matchesAt calls each with the submatch indexes (offsets in text) of every match of re that begins at a candidate,
// leftmost-first and non-overlapping.
func matchesAt(text string, cands []int, re *lazyRegexp, each func(loc []int)) {
	last := 0
	for _, p := range cands {
		if p < last {
			continue
		}
		loc := re.FindStringSubmatchIndex(text[p:])
		if loc == nil {
			continue
		}
		for i := range loc {
			if loc[i] >= 0 {
				loc[i] += p
			}
		}
		each(loc)
		last = loc[1]
	}
}

// shapeSpans are the byte spans of every match of a token-shape regexp (one that opens with a literal, so Go can skip
// to it) whose first character is a word character, kept only where `\b` holds before it: the previous byte is not
// a word character. A match that fails the boundary is skipped and the scan resumes one byte later, as the regexp's
// own scan would.
func shapeSpans(re *lazyRegexp, text string, boundary bool) [][2]int {
	var spans [][2]int
	for pos := 0; pos < len(text); {
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		s, e := pos+loc[0], pos+loc[1]
		if boundary && s > 0 && isWordByte(text[s-1]) {
			pos = s + 1
			continue
		}
		spans = append(spans, [2]int{s, e})
		pos = e
	}
	return spans
}

func replaceSpans(text string, spans [][2]int, repl string) string {
	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, s := range spans {
		b.WriteString(text[last:s[0]])
		b.WriteString(repl)
		last = s[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// boundaryCandidates keeps the offsets where `\b` holds before a word character: the previous byte is not a word
// character.
func boundaryCandidates(text string, cands []int) []int {
	var out []int
	for _, p := range cands {
		if p == 0 || !isWordByte(text[p-1]) {
			out = append(out, p)
		}
	}
	return out
}

// sepWord reports whether r starts with an optional `_` or `-` and then word (`[_-]?word`).
func sepWord(r, word string) bool {
	if strings.HasPrefix(r, word) {
		return true
	}
	return len(r) > 0 && (r[0] == '_' || r[0] == '-') && strings.HasPrefix(r[1:], word)
}

// keyAltAt reports whether one of the credential-key alternatives (`credentialKeys`) matches at offset p of lower,
// the ASCII-lowered text. Case folding is ASCII only, as JavaScript's `i` flag does for a non-unicode pattern.
func keyAltAt(lower string, p int) bool {
	r := lower[p:]
	switch r[0] {
	case 'a':
		return (strings.HasPrefix(r, "api") && sepWord(r[3:], "key")) || (strings.HasPrefix(r, "access") && sepWord(r[6:], "key"))
	case 's':
		return strings.HasPrefix(r, "secret")
	case 'c':
		return (strings.HasPrefix(r, "client") && sepWord(r[6:], "secret")) || strings.HasPrefix(r, "credential")
	case 'p':
		return (strings.HasPrefix(r, "private") && sepWord(r[7:], "key")) ||
			(strings.HasPrefix(r, "passw") && (strings.HasPrefix(r[5:], "ord") || strings.HasPrefix(r[5:], "d"))) ||
			strings.HasPrefix(r, "passphrase")
	case 't':
		return strings.HasPrefix(r, "token")
	}
	return false
}

// isGoSpace is RE2's `\s`: [\t\n\f\r ].
func isGoSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// assignHeadAt matches `(?i)(?:KEYS)[a-z0-9_-]*\s*[=:]\s*` at offset p of lower, the ASCII-lowered text: a credential
// key, the rest of the identifier, and the `=` or `:` with the space around it. It returns the offset after the
// trailing space. The identifier run is the maximal run of `[a-z0-9_-]` from p (every keyword is inside it), so
// there is nothing to backtrack into.
func assignHeadAt(lower string, p int) (valueStart int, ok bool) {
	if !keyAltAt(lower, p) {
		return 0, false
	}
	j := p
	for j < len(lower) {
		c := lower[j]
		if ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') || c == '_' || c == '-' {
			j++
			continue
		}
		break
	}
	for j < len(lower) && isGoSpace(lower[j]) {
		j++
	}
	if j >= len(lower) || (lower[j] != '=' && lower[j] != ':') {
		return 0, false
	}
	j++
	for j < len(lower) && isGoSpace(lower[j]) {
		j++
	}
	return j, true
}

// quotedValueAt matches a credential key, `=` or `:`, and a value quoted with quote, at offset p: the value holds no
// quote of either kind and no newline. It returns the end of the head (before the opening quote) and of the match.
func quotedValueAt(text, lower string, p int, quote byte) (headEnd, end int, ok bool) {
	j, ok := assignHeadAt(lower, p)
	if !ok || j >= len(text) || text[j] != quote {
		return 0, 0, false
	}
	k := j + 1
	for k < len(text) && text[k] != '"' && text[k] != '\'' && text[k] != '\n' {
		k++
	}
	if k >= len(text) || text[k] != quote {
		return 0, 0, false
	}
	return j, k + 1, true
}

// assignValueStart is the offset where the value of an unquoted assignment at p begins: after the head and an
// optional opening quote.
func assignValueStart(text, lower string, p int) (int, bool) {
	j, ok := assignHeadAt(lower, p)
	if !ok {
		return 0, false
	}
	if j < len(text) && (text[j] == '"' || text[j] == '\'') {
		j++
	}
	return j, true
}
