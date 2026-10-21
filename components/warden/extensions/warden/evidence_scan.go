package warden

import (
	"strings"
	"unicode/utf8"
)

// Hand-written replacements for the regular expressions that dominated the cost of parsing a run's output
// (evidence.go). Go's regexp engine cannot skip to the next line start for an expression that begins with `(?m)^`
// and has no literal prefix: it tries every byte offset, where V8's irregexp compiles a scan. Each function here is
// exactly one expression (named in its comment) and is checked against it, over adversarial inputs, by
// evidence_scan_test.go, which keeps the expressions as the reference. Go's `\s` is [\t\n\f\r ] (no \v), `\S` is
// everything else (any byte of a multi-byte character is `\S`), `\d` is [0-9], `.` is anything but "\n" and `$` of
// a (?m) expression is the end of a line: before "\n", or the end of the text.

// reSpace is Go's `\s` for one byte.
func reSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

func digitByte(c byte) bool { return c >= '0' && c <= '9' }

// eachLine calls f with every line of text (split at "\n") and its start offset until f returns false.
func eachLine(text string, f func(line string, start int, prevBlank bool) bool) {
	start := 0
	prevBlank := false
	for start <= len(text) {
		end := strings.IndexByte(text[start:], '\n')
		var line string
		if end < 0 {
			line = text[start:]
			end = len(text)
		} else {
			end += start
			line = text[start:end]
		}
		if !f(line, start, prevBlank) {
			return
		}
		prevBlank = isBlankLine(line)
		start = end + 1
	}
}

// isBlankLine: the line is whitespace only (`\s*` from its start reaches the "\n").
func isBlankLine(line string) bool {
	for i := range len(line) {
		if !reSpace(line[i]) {
			return false
		}
	}
	return true
}

// lineDigitsAfter is the first group of FindStringSubmatch of `(?m)^<prefix>(\d+)$` for a literal prefix without
// regexp metacharacters (reTapFail, reTapTests).
func lineDigitsAfter(text, prefix string) string {
	for pos := 0; pos < len(text); {
		i := strings.Index(text[pos:], prefix)
		if i < 0 {
			return ""
		}
		i += pos
		pos = i + 1
		if i > 0 && text[i-1] != '\n' {
			continue
		}
		rest := text[i+len(prefix):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		if rest != "" && allDigits(rest) {
			return rest
		}
	}
	return ""
}

func allDigits(s string) bool {
	for i := range len(s) {
		if !digitByte(s[i]) {
			return false
		}
	}
	return true
}

// goFailName is the group of `(?m)^\s*--- FAIL: (\S+)` on one line.
func goFailName(line string) (string, bool) {
	i := 0
	for i < len(line) && reSpace(line[i]) {
		i++
	}
	const lit = "--- FAIL: "
	if !strings.HasPrefix(line[i:], lit) {
		return "", false
	}
	i += len(lit)
	j := i
	for j < len(line) && !reSpace(line[j]) {
		j++
	}
	if j == i {
		return "", false
	}
	return line[i:j], true
}

// goBuildError is the groups of `(?m)^(\S+\.go):(\d+):(\d+): (.+)$` on one line: file, line, column and message.
func goBuildError(line string) (file, ln, col, msg string, ok bool) {
	run := 0
	for run < len(line) && !reSpace(line[run]) {
		run++
	}
	// The run must end `.go:<digits>:<digits>:` and be followed by one space and at least one more character.
	if run == 0 || run+1 >= len(line) || line[run] != ' ' || line[run-1] != ':' {
		return "", "", "", "", false
	}
	r := line[:run-1]
	d2 := len(r)
	for d2 > 0 && digitByte(r[d2-1]) {
		d2--
	}
	if d2 == len(r) || d2 == 0 || r[d2-1] != ':' {
		return "", "", "", "", false
	}
	r2 := r[:d2-1]
	d1 := len(r2)
	for d1 > 0 && digitByte(r2[d1-1]) {
		d1--
	}
	if d1 == len(r2) || d1 == 0 || r2[d1-1] != ':' {
		return "", "", "", "", false
	}
	g := r2[:d1-1]
	if len(g) < 4 || !strings.HasSuffix(g, ".go") {
		return "", "", "", "", false
	}
	return g, r2[d1:], r[d2:], line[run+1:], true
}

// goTestDetail is the groups of `(?m)^\s+(\S+_test\.go:\d+): (.+)$` for one line: the location and the message.
// prevBlank is whether the line before is whitespace only, because `\s+` can start on that line and run through
// its "\n" into this one.
func goTestDetail(line string, prevBlank bool) (loc, msg string, ok bool) {
	i := 0
	for i < len(line) && reSpace(line[i]) {
		i++
	}
	if i == 0 && !prevBlank {
		return "", "", false
	}
	run := i
	for run < len(line) && !reSpace(line[run]) {
		run++
	}
	if run == i || run+1 >= len(line) || line[run] != ' ' || line[run-1] != ':' {
		return "", "", false
	}
	r := line[i : run-1]
	d := len(r)
	for d > 0 && digitByte(r[d-1]) {
		d--
	}
	if d == len(r) || d == 0 || r[d-1] != ':' {
		return "", "", false
	}
	g := r[:d-1]
	const lit = "_test.go"
	if len(g) < len(lit)+1 || !strings.HasSuffix(g, lit) {
		return "", "", false
	}
	return r, line[run+1:], true
}

// goFailPackage is the group of `(?m)^FAIL\t(\S+)(?:\t| \[)` on one line.
func goFailPackage(line string) (string, bool) {
	const lit = "FAIL\t"
	if !strings.HasPrefix(line, lit) {
		return "", false
	}
	j := len(lit)
	for j < len(line) && !reSpace(line[j]) {
		j++
	}
	if j == len(lit) || j >= len(line) {
		return "", false
	}
	if line[j] == '\t' || (line[j] == ' ' && j+1 < len(line) && line[j+1] == '[') {
		return line[len(lit):j], true
	}
	return "", false
}

// asciiExitCode is the digits of the last match of `(?im)(?:exited with code|exit code:?|exit status|Error)\s+(\d+)\s*$`
// in an ASCII text (reEvExitCode), or "" for none. Matches cannot overlap: the expression's letters only occur in
// its keywords.
func asciiExitCode(text string) string {
	last := ""
	for i := 0; i < len(text); i++ {
		if text[i]|0x20 != 'e' {
			continue
		}
		j := exitKeywordEnd(text, i)
		if j < 0 {
			continue
		}
		k := j
		for k < len(text) && reSpace(text[k]) {
			k++
		}
		if k == j {
			continue
		}
		d := k
		for d < len(text) && digitByte(text[d]) {
			d++
		}
		if d == k {
			continue
		}
		// `\s*$`: the longest whitespace run from d that ends at a line end, or the end of the text.
		w := d
		for w < len(text) && reSpace(text[w]) {
			w++
		}
		end := -1
		if w == len(text) {
			end = w
		} else if nl := strings.LastIndexByte(text[d:w], '\n'); nl >= 0 {
			end = d + nl
		}
		if end < 0 {
			continue
		}
		last = text[k:d]
		i = end - 1
	}
	return last
}

// exitKeywordEnd is where the keyword alternation of reEvExitCode ends when it matches at i, or -1.
func exitKeywordEnd(text string, i int) int {
	if foldPrefix(text[i:], "exited with code") {
		return i + len("exited with code")
	}
	if foldPrefix(text[i:], "exit code") {
		j := i + len("exit code")
		if j < len(text) && text[j] == ':' {
			j++
		}
		return j
	}
	if foldPrefix(text[i:], "exit status") {
		return i + len("exit status")
	}
	if foldPrefix(text[i:], "error") {
		return i + len("error")
	}
	return -1
}

// foldPrefix is strings.HasPrefix for an ASCII lower-case lit, ignoring the case of an ASCII text.
func foldPrefix(s, lit string) bool {
	if len(s) < len(lit) {
		return false
	}
	for k := range len(lit) {
		c := s[k]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lit[k] {
			return false
		}
	}
	return true
}

// collapseSpaceRuns is reJSSpace.ReplaceAllString(s, " "): every run of JavaScript whitespace becomes one space.
// A string that has none, or only single spaces, is returned as it is, and invalid UTF-8 is kept byte for byte
// (the expression does not match it).
func collapseSpaceRuns(s string) string {
	var out []byte
	copied := 0
	for i := 0; i < len(s); {
		r, size := rune(s[i]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(s[i:])
		}
		if !isJSSpace(r) {
			i += size
			continue
		}
		start := i
		for i += size; i < len(s); i += size {
			r, size = rune(s[i]), 1
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(s[i:])
			}
			if !isJSSpace(r) {
				break
			}
		}
		if i-start == 1 && s[start] == ' ' {
			continue
		}
		if out == nil {
			out = make([]byte, 0, len(s))
		}
		out = append(out, s[copied:start]...)
		out = append(out, ' ')
		copied = i
	}
	if out == nil {
		return s
	}
	return string(append(out, s[copied:]...))
}

// Bits of noiseFlags: each is a necessary condition for the expression of the same step in failureNoise, so a
// string without the bit is not changed by that step.
const (
	noiseTmp = 1 << iota
	noisePytest
	noiseDotTmp
	noiseDate
	noiseTime
	noiseRun
	noiseDuration
	noiseHex
	noiseParenNumber
	noiseFiveDigits
	noiseColonNumber
	noiseParenPair
)

// noiseFlags is one pass over s that sets the necessary conditions of the twelve normalising expressions.
func noiseFlags(s string) (flags uint32) {
	colonPairs, comma, dashOrSlash := 0, false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case digitByte(c):
			j := i + 1
			for j < len(s) && digitByte(s[j]) {
				j++
			}
			if j-i >= 5 {
				flags |= noiseFiveDigits
			}
			if durationFollows(s, j) {
				flags |= noiseDuration
			}
			if s[j-1] == '0' && j < len(s) && s[j] == 'x' {
				flags |= noiseHex
			}
			if i > 0 && s[i-1] == ':' {
				flags |= noiseColonNumber
				if i >= 2 && digitByte(s[i-2]) {
					colonPairs++
				}
			}
			if i > 0 && s[i-1] == '(' {
				flags |= noiseParenNumber
			}
			i = j - 1
		case c == '/':
			dashOrSlash = true
			if strings.HasPrefix(s[i:], "/tmp/") || strings.HasPrefix(s[i:], "/var/folders/") {
				flags |= noiseTmp
			}
		case c == '-':
			dashOrSlash = true
			if i >= 6 && strings.HasPrefix(s[i-6:], "pytest-") {
				flags |= noisePytest
			}
		case c == '.':
			if strings.HasPrefix(s[i:], ".tmp") {
				flags |= noiseDotTmp
			}
		case c == ',':
			comma = true
		case c == 'r':
			if strings.HasPrefix(s[i:], "run=") {
				flags |= noiseRun
			}
		}
	}
	if colonPairs >= 1 && dashOrSlash {
		flags |= noiseDate
	}
	if colonPairs >= 2 {
		flags |= noiseTime
	}
	if comma && flags&noiseParenNumber != 0 {
		flags |= noiseParenPair
	}
	return flags
}

// durationFollows is whether a digit run that ends at j can start `\d+(?:\.\d+)?\s*(?:ms|s|m|h|µs|us|ns)\b`: an
// optional fraction, optional whitespace, then the first letter of a unit.
func durationFollows(s string, j int) bool {
	if j+1 < len(s) && s[j] == '.' && digitByte(s[j+1]) {
		j += 2
		for j < len(s) && digitByte(s[j]) {
			j++
		}
	}
	for j < len(s) && reSpace(s[j]) {
		j++
	}
	if j >= len(s) {
		return false
	}
	switch s[j] {
	case 'm', 's', 'h', 'u', 'n':
		return true
	case 0xc2:
		return j+1 < len(s) && s[j+1] == 0xb5
	}
	return false
}
