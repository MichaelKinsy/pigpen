package warden

import "strings"

// Hand matchers for the line expressions of the rules markdown (rules.go). A line of printable ASCII with no line
// break (the case for nearly every line of a rules file) is matched by the loops below; any other line goes to the
// regular expression itself, so the Unicode spaces and case folds of the expressions stay the engine's. The loops give
// the captures the engine gives, including its backtracking over the spaces before a lazy capture:
// rules_scan_test.go compares them with the expressions over generated lines.

// plainLine is true when s has no byte of 0x80 or above and no \n or \r.
func plainLine(s string) bool {
	for i := range len(s) {
		if c := s[i]; c >= 0x80 || c == '\n' || c == '\r' {
			return false
		}
	}
	return true
}

// isHSpace is the ASCII part of jsSpaceClass that a plain line can hold: Go's \s without the line breaks.
func isHSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// trailTail is the start of the longest suffix of s made of spaces, then `#`s when hashes is set, then spaces.
func trailTail(s string, hashes bool) int {
	t := len(s)
	for t > 0 && isHSpace(s[t-1]) {
		t--
	}
	if hashes {
		for t > 0 && s[t-1] == '#' {
			t--
		}
		for t > 0 && isHSpace(s[t-1]) {
			t--
		}
	}
	return t
}

// lazyCapture is the capture of `S*(D+?)<tail>$` entered after the spaces at s[from:], where the tail is the one
// trailTail measures. The spaces are taken greedily and given back one at a time until the capture holds a character.
func lazyCapture(s string, from int, tail int) (string, bool) {
	spaces := 0
	for from+spaces < len(s) && isHSpace(s[from+spaces]) {
		spaces++
	}
	for k := spaces; k >= 0; k-- {
		start := from + k
		if start < len(s) {
			return s[start:max(start+1, tail)], true
		}
	}
	return "", false
}

// headingMatch is reHeading.FindStringSubmatch as (the line, the hashes, the title).
func headingMatch(text string) ([3]string, bool) {
	if text == "" || text[0] != '#' {
		return [3]string{}, false
	}
	if !plainLine(text) {
		m := reHeading.FindStringSubmatch(text)
		if m == nil {
			return [3]string{}, false
		}
		return [3]string{m[0], m[1], m[2]}, true
	}
	h := 0
	for h < len(text) && text[h] == '#' {
		h++
	}
	// `#{1,6}` must be followed by a space: with more hashes, giving some back leaves a `#` where a space must be.
	if h > 6 || h == len(text) || !isHSpace(text[h]) {
		return [3]string{}, false
	}
	// The spaces after the hashes are `[S]+`: at least one stays out of the title.
	spaces := 0
	for h+spaces < len(text) && isHSpace(text[h+spaces]) {
		spaces++
	}
	tail := trailTail(text, true)
	for k := spaces; k >= 1; k-- {
		start := h + k
		if start < len(text) {
			return [3]string{text, text[:h], text[start:max(start+1, tail)]}, true
		}
	}
	return [3]string{}, false
}

// headerWord is a keyword of a header line, with `s` optional after it for `path(s)` and `file(s)`.
type headerWord struct {
	text string
	optS bool
}

// headerSpec is a header-line expression `(?i)^S*(?:words)S*:S*(D+?)S*$` and its words.
type headerSpec struct {
	re    *lazyRegexp
	words []headerWord
}

func (h *headerSpec) lineAfterKeyword(line string) (int, bool) {
	i := 0
	for i < len(line) && isHSpace(line[i]) {
		i++
	}
	for _, w := range h.words {
		if len(line)-i < len(w.text) || !strings.EqualFold(line[i:i+len(w.text)], w.text) {
			continue
		}
		j := i + len(w.text)
		if w.optS && j < len(line) && (line[j] == 's' || line[j] == 'S') {
			j++
		}
		for j < len(line) && isHSpace(line[j]) {
			j++
		}
		if j < len(line) && line[j] == ':' {
			return j + 1, true
		}
		return 0, false
	}
	return 0, false
}

// headerMatch is the header expression's FindStringSubmatch as (the line, the value): none of them can match a line
// without a colon.
func headerMatch(h *headerSpec, line string) ([2]string, bool) {
	if strings.IndexByte(line, ':') < 0 {
		return [2]string{}, false
	}
	if !plainLine(line) {
		m := h.re.FindStringSubmatch(line)
		if m == nil {
			return [2]string{}, false
		}
		return [2]string{m[0], m[1]}, true
	}
	from, ok := h.lineAfterKeyword(line)
	if !ok {
		return [2]string{}, false
	}
	value, ok := lazyCapture(line, from, trailTail(line, false))
	if !ok {
		return [2]string{}, false
	}
	return [2]string{line, value}, true
}

var (
	headerPaths     = &headerSpec{rePathsLine, []headerWord{{"path", true}, {"applies to", false}, {"file", true}}}
	headerThreshold = &headerSpec{reThresholdLn, []headerWord{{"threshold", false}}}
	headerSeverity  = &headerSpec{reSeverityLine, []headerWord{{"severity", false}}}
	headerSource    = &headerSpec{reSourceRef, []headerWord{{"source", false}}}
	headerWhen      = &headerSpec{reWhenLine, []headerWord{{"when", false}}}
)

// slugText is the lower-cased text with each run of characters outside a-z and 0-9 as one `-`, trimmed of dashes: for a
// plain ASCII name, the replacement of reNonSlug on its lower case, in one pass.
func slugText(name string) (string, bool) {
	for i := range len(name) {
		if name[i] >= 0x80 {
			return "", false
		}
	}
	out := make([]byte, 0, len(name))
	dash := false
	for i := range len(name) {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			out = append(out, c)
			dash = false
		} else if !dash {
			out = append(out, '-')
			dash = true
		}
	}
	return strings.Trim(string(out), "-"), true
}

// splitPathList is rePathSep.Split(s, -1): the pieces of s between runs of commas and spaces, the empty ends included.
func splitPathList(s string) []string {
	if !plainLine(s) {
		return rePathSep.Split(s, -1)
	}
	isSep := func(c byte) bool { return c == ',' || isHSpace(c) }
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(s); {
		if !isSep(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && isSep(s[j]) {
			j++
		}
		out = append(out, s[start:i])
		start, i = j, j
	}
	return append(out, s[start:])
}
