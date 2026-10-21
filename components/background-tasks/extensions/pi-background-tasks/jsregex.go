package pi_background_tasks

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/dlclark/regexp2"
)

// A JavaScript regular expression on a backtracking engine. The original compiles `/pattern/flags` notify patterns with
// `new RegExp` and runs them with a 25 ms deadline, so a pattern that backtracks catastrophically is disabled instead of
// freezing Pi. Go's regexp is linear-time (no deadline to hit, no backreferences or lookaround), so the port translates the
// pattern to the dialect of github.com/dlclark/regexp2, a backtracking engine with a match timeout, after parsing it with
// ECMAScript's grammar (Annex B leniencies included, for patterns without the u flag).
//
// A pattern the parser knows JavaScript rejects is a *jsSyntaxError: the original falls back to a substring match for it.
// A pattern the parser cannot be sure of (inline modifiers, duplicate group names, script properties) is an
// *jsUnsupportedError: the port refuses it, it never guesses.
//
// Without the u flag the text is matched as UTF-16 code units, as JavaScript does (an astral character is two atoms); with
// it, as code points.

type jsSyntaxError struct{ msg string }

func (e *jsSyntaxError) Error() string { return "Invalid regular expression: " + e.msg }

type jsUnsupportedError struct{ msg string }

func (e *jsUnsupportedError) Error() string { return e.msg }

// jsRegex is a compiled JavaScript regular expression.
type jsRegex struct {
	re      *regexp2.Regexp
	unicode bool
}

// toRunes is the text as the engine reads it: UTF-16 code units, or code points under the u flag.
func (r *jsRegex) toRunes(text string) []rune {
	if r.unicode {
		return []rune(text)
	}
	units := utf16.Encode([]rune(text))
	out := make([]rune, len(units))
	for i, u := range units {
		out[i] = rune(u)
	}
	return out
}

// test is regex.test(text) with lastIndex 0 under a deadline. timedOut reports that the deadline passed.
func (r *jsRegex) test(text string, deadline time.Duration) (matched, timedOut bool) {
	if deadline > 0 {
		r.re.MatchTimeout = deadline
	} else {
		r.re.MatchTimeout = regexp2.DefaultMatchTimeout
	}
	ok, err := r.re.MatchRunes(r.toRunes(text))
	if err != nil {
		return false, true
	}
	return ok, false
}

// compileJSRegex translates and compiles `new RegExp(source, flags)`.
func compileJSRegex(source, flags string) (*jsRegex, error) {
	var fi, fm, fs, fu, fy bool
	seen := map[rune]bool{}
	for _, f := range flags {
		if seen[f] || !strings.ContainsRune("gimsuy", f) {
			return nil, &jsSyntaxError{"Invalid flags"}
		}
		seen[f] = true
		switch f {
		case 'i':
			fi = true
		case 'm':
			fm = true
		case 's':
			fs = true
		case 'u':
			fu = true
		case 'y':
			fy = true
		}
	}
	p := &jsParser{u: fu, m: fm, s: fs}
	if fu {
		p.src = []rune(source)
	} else {
		for _, unit := range utf16.Encode([]rune(source)) {
			p.src = append(p.src, rune(unit))
		}
	}
	p.prescan()
	if err := p.checkNames(); err != nil {
		return nil, err
	}
	body, err := p.parseDisjunction()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.src) {
		return nil, &jsSyntaxError{"Unmatched ')'"}
	}
	for name := range p.refNames {
		if !p.names[name] {
			return nil, &jsSyntaxError{"Invalid named capture referenced"}
		}
	}
	pattern := body
	if fy {
		pattern = `\G(?:` + body + `)`
	}
	opts := regexp2.None
	if fi {
		opts |= regexp2.IgnoreCase
	}
	re, err := regexp2.Compile(pattern, opts)
	if err != nil {
		return nil, &jsUnsupportedError{"the translated pattern was rejected by the regular expression engine: " + err.Error()}
	}
	return &jsRegex{re: re, unicode: fu}, nil
}

type rng struct{ lo, hi rune }

type jsParser struct {
	src        []rune
	pos        int
	u, m, s    bool
	groupCount int
	hasNamed   bool
	names      map[string]bool
	refNames   map[string]bool
	dupName    bool
}

func (p *jsParser) syntax(msg string) error { return &jsSyntaxError{msg} }

func (p *jsParser) peek() (rune, bool) {
	if p.pos >= len(p.src) {
		return 0, false
	}
	return p.src[p.pos], true
}

func (p *jsParser) at(s string) bool {
	r := []rune(s)
	if p.pos+len(r) > len(p.src) {
		return false
	}
	for i, c := range r {
		if p.src[p.pos+i] != c {
			return false
		}
	}
	return true
}

// prescan counts the capture groups and finds the named ones, which decide how \N and \k read.
func (p *jsParser) prescan() {
	p.names = map[string]bool{}
	p.refNames = map[string]bool{}
	inClass := false
	for i := 0; i < len(p.src); i++ {
		c := p.src[i]
		switch {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			if i+1 < len(p.src) && p.src[i+1] == '?' {
				if i+2 < len(p.src) && p.src[i+2] == '<' && i+3 < len(p.src) && p.src[i+3] != '=' && p.src[i+3] != '!' {
					p.groupCount++
					p.hasNamed = true
				}
			} else {
				p.groupCount++
			}
		}
	}
}

// checkNames refuses duplicate group names (valid across alternatives in current engines, which the port does not model).
func (p *jsParser) checkNames() error {
	seen := map[string]bool{}
	for i := 0; i+3 < len(p.src); i++ {
		if p.src[i] == '\\' {
			i++
			continue
		}
		if p.src[i] == '(' && p.src[i+1] == '?' && p.src[i+2] == '<' && p.src[i+3] != '=' && p.src[i+3] != '!' {
			j := i + 3
			for j < len(p.src) && p.src[j] != '>' {
				j++
			}
			name := string(p.src[i+3 : min(j, len(p.src))])
			if seen[name] {
				return &jsUnsupportedError{"a regular expression with a duplicate group name"}
			}
			seen[name] = true
		}
	}
	return nil
}

func (p *jsParser) parseDisjunction() (string, error) {
	var alts []string
	for {
		alt, err := p.parseAlternative()
		if err != nil {
			return "", err
		}
		alts = append(alts, alt)
		if c, ok := p.peek(); ok && c == '|' {
			p.pos++
			continue
		}
		break
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return strings.Join(alts, "|"), nil
}

func (p *jsParser) parseAlternative() (string, error) {
	var b strings.Builder
	for {
		c, ok := p.peek()
		if !ok || c == '|' || c == ')' {
			return b.String(), nil
		}
		term, err := p.parseTerm()
		if err != nil {
			return "", err
		}
		b.WriteString(term)
	}
}

const (
	lineTerms = `\u000A\u000D\u2028\u2029`
	wordAscii = `A-Za-z0-9_`
)

func (p *jsParser) maxRune() rune {
	if p.u {
		return 0x10FFFF
	}
	return 0xFFFF
}

func lit(r rune) string {
	switch {
	case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return string(r)
	case r <= 0xFFFF:
		return fmt.Sprintf(`\u%04X`, r)
	}
	return string(r)
}

func (p *jsParser) parseTerm() (string, error) {
	c, _ := p.peek()
	var atom string
	quantifiable := true
	switch c {
	case '^':
		p.pos++
		quantifiable = false
		if p.m {
			atom = `(?:^|(?<=[` + lineTerms + `]))`
		} else {
			atom = `^`
		}
	case '$':
		p.pos++
		quantifiable = false
		if p.m {
			atom = `(?=[` + lineTerms + `]|\z)`
		} else {
			atom = `\z`
		}
	case '(':
		a, q, err := p.parseGroup()
		if err != nil {
			return "", err
		}
		atom, quantifiable = a, q
	case '*', '+', '?':
		return "", p.syntax("Nothing to repeat")
	case '{':
		if p.u {
			return "", p.syntax("Nothing to repeat")
		}
		if _, _, _, ok := p.quantifierAt(); ok {
			return "", p.syntax("Nothing to repeat")
		}
		p.pos++
		atom = lit('{')
	case '}', ']':
		if p.u {
			return "", p.syntax("Lone quantifier brackets")
		}
		p.pos++
		atom = lit(c)
	case '.':
		p.pos++
		if p.s {
			atom = `[\s\S]`
		} else {
			atom = `[^` + lineTerms + `]`
		}
	case '[':
		a, err := p.parseClass()
		if err != nil {
			return "", err
		}
		atom = a
	case '\\':
		a, q, err := p.parseEscape()
		if err != nil {
			return "", err
		}
		atom, quantifiable = a, q
	default:
		p.pos++
		atom = lit(c)
	}
	q, ok, err := p.parseQuantifier()
	if err != nil {
		return "", err
	}
	if ok {
		if !quantifiable {
			return "", p.syntax("Nothing to repeat")
		}
		return atom + q, nil
	}
	return atom, nil
}

// quantifierAt reads `{n}`, `{n,}` or `{n,m}` at the current position without consuming it.
func (p *jsParser) quantifierAt() (lo, hi int, end int, ok bool) {
	i := p.pos
	if i >= len(p.src) || p.src[i] != '{' {
		return 0, 0, 0, false
	}
	i++
	readNum := func() (int, bool) {
		start := i
		for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
			i++
		}
		if i == start {
			return 0, false
		}
		n, err := strconv.Atoi(string(p.src[start:i]))
		if err != nil || n > 1<<30 {
			return 1 << 30, true
		}
		return n, true
	}
	lo, okLo := readNum()
	if !okLo {
		return 0, 0, 0, false
	}
	hi = lo
	if i < len(p.src) && p.src[i] == ',' {
		i++
		if n, okHi := readNum(); okHi {
			hi = n
		} else {
			hi = -1
		}
	}
	if i >= len(p.src) || p.src[i] != '}' {
		return 0, 0, 0, false
	}
	return lo, hi, i + 1, true
}

func (p *jsParser) parseQuantifier() (string, bool, error) {
	c, ok := p.peek()
	if !ok {
		return "", false, nil
	}
	var q string
	switch c {
	case '*', '+', '?':
		p.pos++
		q = string(c)
	case '{':
		lo, hi, end, valid := p.quantifierAt()
		if !valid {
			return "", false, nil
		}
		if lo >= 1<<30 || hi >= 1<<30 {
			return "", false, &jsUnsupportedError{"a regular expression repeat count above a billion"}
		}
		if hi >= 0 && hi < lo {
			return "", false, p.syntax("numbers out of order in {} quantifier")
		}
		q = string(p.src[p.pos:end])
		p.pos = end
	default:
		return "", false, nil
	}
	if n, ok := p.peek(); ok && n == '?' {
		p.pos++
		q += "?"
	}
	return q, true, nil
}

func (p *jsParser) parseGroup() (string, bool, error) {
	p.pos++ // (
	open := "("
	quantifiable := true
	switch {
	case p.at("?:"):
		p.pos += 2
		open = "(?:"
	case p.at("?="):
		p.pos += 2
		open = "(?="
		quantifiable = !p.u
	case p.at("?!"):
		p.pos += 2
		open = "(?!"
		quantifiable = !p.u
	case p.at("?<="):
		p.pos += 3
		open = "(?<="
		quantifiable = false
	case p.at("?<!"):
		p.pos += 3
		open = "(?<!"
		quantifiable = false
	case p.at("?<"):
		p.pos += 2
		start := p.pos
		for p.pos < len(p.src) && p.src[p.pos] != '>' {
			p.pos++
		}
		if p.pos >= len(p.src) {
			return "", false, p.syntax("Invalid capture group name")
		}
		name := string(p.src[start:p.pos])
		p.pos++
		if strings.ContainsRune(name, '\\') {
			return "", false, &jsUnsupportedError{"a regular expression group name with an escape"}
		}
		if !validGroupName(name) {
			return "", false, p.syntax("Invalid capture group name")
		}
		p.names[name] = true
		open = "(?<" + name + ">"
	case p.at("?"):
		// Inline modifiers (?i:...) exist in current engines; a malformed group is an error. The port cannot tell which, so it refuses.
		return "", false, &jsUnsupportedError{"a regular expression group that starts with (?"}
	}
	body, err := p.parseDisjunction()
	if err != nil {
		return "", false, err
	}
	if c, ok := p.peek(); !ok || c != ')' {
		return "", false, p.syntax("Unterminated group")
	}
	p.pos++
	return open + body + ")", quantifiable, nil
}

func validGroupName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '$' || r == '_' || unicode.IsLetter(r):
		case i > 0 && (unicode.IsDigit(r) || r == 0x200c || r == 0x200d || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r)):
		default:
			return false
		}
	}
	return true
}

func isHex(r rune) bool { return digitValue(r) >= 0 && digitValue(r) < 16 }

func (p *jsParser) hexAt(n int) (rune, bool) {
	if p.pos+n > len(p.src) {
		return 0, false
	}
	v := rune(0)
	for i := range n {
		c := p.src[p.pos+i]
		if !isHex(c) {
			return 0, false
		}
		v = v*16 + rune(digitValue(c))
	}
	return v, true
}

// escapeSets are the ranges of \d, \s and \w (the class escapes), over the code units of the text.
var (
	digitRanges = []rng{{'0', '9'}}
	wordRanges  = []rng{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
	spaceRanges = []rng{{'\t', '\r'}, {' ', ' '}, {0xa0, 0xa0}, {0x1680, 0x1680}, {0x2000, 0x200a}, {0x2028, 0x2029}, {0x202f, 0x202f}, {0x205f, 0x205f}, {0x3000, 0x3000}, {0xfeff, 0xfeff}}
)

func complementRanges(in []rng, maxRune rune) []rng {
	sorted := append([]rng{}, in...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].lo < sorted[j].lo })
	var out []rng
	next := rune(0)
	for _, r := range sorted {
		if r.lo > next {
			out = append(out, rng{next, r.lo - 1})
		}
		next = r.hi + 1
	}
	if next <= maxRune {
		out = append(out, rng{next, maxRune})
	}
	return out
}

func emitRanges(rs []rng) string {
	var b strings.Builder
	for _, r := range rs {
		if r.lo == r.hi {
			b.WriteString(lit(r.lo))
		} else {
			b.WriteString(lit(r.lo) + "-" + lit(r.hi))
		}
	}
	return b.String()
}

// classEscape is the ranges of \d \D \s \S \w \W, ok false for any other letter.
func (p *jsParser) classEscape(c rune) ([]rng, bool) {
	switch c {
	case 'd':
		return digitRanges, true
	case 'D':
		return complementRanges(digitRanges, p.maxRune()), true
	case 's':
		return spaceRanges, true
	case 'S':
		return complementRanges(spaceRanges, p.maxRune()), true
	case 'w':
		return wordRanges, true
	case 'W':
		return complementRanges(wordRanges, p.maxRune()), true
	}
	return nil, false
}

var generalCategories = map[string]bool{}

func init() {
	for _, c := range strings.Fields("L Lu Ll Lt Lm Lo M Mn Mc Me N Nd Nl No P Pc Pd Ps Pe Pi Pf Po S Sm Sc Sk So Z Zs Zl Zp C Cc Cf Cs Co Cn") {
		generalCategories[c] = true
	}
}

// parseProperty reads `{Name}` after \p or \P (u flag) into the engine's \p{Name}.
func (p *jsParser) parseProperty(negate bool) (string, error) {
	if c, ok := p.peek(); !ok || c != '{' {
		return "", p.syntax("Invalid property name")
	}
	end := p.pos + 1
	for end < len(p.src) && p.src[end] != '}' {
		end++
	}
	if end >= len(p.src) {
		return "", p.syntax("Invalid property name")
	}
	name := string(p.src[p.pos+1 : end])
	p.pos = end + 1
	name = strings.TrimPrefix(strings.TrimPrefix(name, "General_Category="), "gc=")
	if !generalCategories[name] {
		return "", &jsUnsupportedError{"the regular expression property escape \\p{" + name + "}"}
	}
	if negate {
		return `\P{` + name + `}`, nil
	}
	return `\p{` + name + `}`, nil
}

// parseEscape is an atom escape outside a class. It reports whether the atom can take a quantifier.
func (p *jsParser) parseEscape() (string, bool, error) {
	p.pos++ // backslash
	c, ok := p.peek()
	if !ok {
		return "", false, p.syntax("\\ at end of pattern")
	}
	if rs, isClass := p.classEscape(c); isClass {
		p.pos++
		return "[" + emitRanges(rs) + "]", true, nil
	}
	switch c {
	case 'b':
		p.pos++
		return `(?:(?<=[` + wordAscii + `])(?![` + wordAscii + `])|(?<![` + wordAscii + `])(?=[` + wordAscii + `]))`, false, nil
	case 'B':
		p.pos++
		return `(?:(?<=[` + wordAscii + `])(?=[` + wordAscii + `])|(?<![` + wordAscii + `])(?![` + wordAscii + `]))`, false, nil
	case 'p', 'P':
		if p.u {
			p.pos++
			s, err := p.parseProperty(c == 'P')
			return s, true, err
		}
	case 'k':
		if p.hasNamed || p.u {
			p.pos++
			if n, ok := p.peek(); !ok || n != '<' {
				return "", false, p.syntax("Invalid named reference")
			}
			end := p.pos + 1
			for end < len(p.src) && p.src[end] != '>' {
				end++
			}
			if end >= len(p.src) {
				return "", false, p.syntax("Invalid named reference")
			}
			name := string(p.src[p.pos+1 : end])
			if !validGroupName(name) {
				return "", false, p.syntax("Invalid named reference")
			}
			p.pos = end + 1
			p.refNames[name] = true
			return `\k<` + name + `>`, true, nil
		}
	}
	if c >= '1' && c <= '9' {
		start := p.pos
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		n, err := strconv.Atoi(string(p.src[start:p.pos]))
		if err == nil && n <= p.groupCount {
			if p.hasNamed {
				return "", false, &jsUnsupportedError{"a numbered backreference in a regular expression with named groups"}
			}
			return fmt.Sprintf(`\k<%d>`, n), true, nil
		}
		p.pos = start
		if p.u {
			return "", false, p.syntax("Invalid escape")
		}
		if c >= '8' {
			p.pos++
			return lit(c), true, nil
		}
		return lit(p.legacyOctal()), true, nil
	}
	r, err := p.characterEscape(false)
	if err != nil {
		return "", false, err
	}
	return r, true, nil
}

// legacyOctal reads up to three octal digits (value at most 0377), as Annex B does without the u flag.
func (p *jsParser) legacyOctal() rune {
	v := rune(0)
	n := 0
	for p.pos < len(p.src) && n < 3 && p.src[p.pos] >= '0' && p.src[p.pos] <= '7' {
		next := v*8 + (p.src[p.pos] - '0')
		if next > 0o377 {
			break
		}
		v = next
		p.pos++
		n++
	}
	return v
}

// characterEscape reads a one-character escape (f n r t v, \0, \cX, \xHH, \uHHHH, an identity escape) at p.pos, the char after the backslash.
func (p *jsParser) characterEscape(inClass bool) (string, error) {
	r, err := p.characterEscapeRune(inClass)
	if err != nil {
		return "", err
	}
	return lit(r), nil
}

func (p *jsParser) characterEscapeRune(inClass bool) (rune, error) {
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'v':
		return '\v', nil
	case '0':
		if n, ok := p.peek(); ok && n >= '0' && n <= '9' {
			if p.u {
				return 0, p.syntax("Invalid decimal escape")
			}
			p.pos--
			return p.legacyOctal(), nil
		}
		return 0, nil
	case 'c':
		if n, ok := p.peek(); ok && (n >= 'a' && n <= 'z' || n >= 'A' && n <= 'Z') {
			p.pos++
			return n % 32, nil
		}
		if p.u {
			return 0, p.syntax("Invalid unicode escape")
		}
		p.pos-- // the backslash stands for itself and the c is read again
		return '\\', nil
	case 'x':
		if v, ok := p.hexAt(2); ok {
			p.pos += 2
			return v, nil
		}
		if p.u {
			return 0, p.syntax("Invalid escape")
		}
		return 'x', nil
	case 'u':
		return p.unicodeEscape()
	case '-':
		if p.u && !inClass {
			return 0, p.syntax("Invalid escape")
		}
		return '-', nil
	}
	if p.u {
		if strings.ContainsRune("^$\\.*+?()[]{}|/", c) {
			return c, nil
		}
		return 0, p.syntax("Invalid escape")
	}
	if c >= '1' && c <= '7' && inClass {
		p.pos--
		return p.legacyOctal(), nil
	}
	return c, nil
}

func (p *jsParser) unicodeEscape() (rune, error) {
	if p.u {
		if c, ok := p.peek(); ok && c == '{' {
			end := p.pos + 1
			for end < len(p.src) && p.src[end] != '}' {
				end++
			}
			if end >= len(p.src) || end == p.pos+1 {
				return 0, p.syntax("Invalid Unicode escape")
			}
			v, err := strconv.ParseUint(string(p.src[p.pos+1:end]), 16, 32)
			if err != nil || v > 0x10FFFF {
				return 0, p.syntax("Invalid Unicode escape")
			}
			p.pos = end + 1
			return rune(v), nil
		}
	}
	v, ok := p.hexAt(4)
	if !ok {
		if p.u {
			return 0, p.syntax("Invalid Unicode escape")
		}
		return 'u', nil
	}
	p.pos += 4
	if p.u && v >= 0xD800 && v <= 0xDBFF && p.at(`\u`) {
		save := p.pos
		p.pos += 2
		if lo, ok := p.hexAt(4); ok && lo >= 0xDC00 && lo <= 0xDFFF {
			p.pos += 4
			return utf16.DecodeRune(v, lo), nil
		}
		p.pos = save
	}
	return v, nil
}

// parseClass reads `[...]`.
func (p *jsParser) parseClass() (string, error) {
	p.pos++ // [
	negate := false
	if c, ok := p.peek(); ok && c == '^' {
		negate = true
		p.pos++
	}
	var items []rng
	for {
		c, ok := p.peek()
		if !ok {
			return "", p.syntax("Unterminated character class")
		}
		if c == ']' {
			p.pos++
			break
		}
		lo, loSet, err := p.classAtom()
		if err != nil {
			return "", err
		}
		if n, ok := p.peek(); ok && n == '-' && p.pos+1 < len(p.src) && p.src[p.pos+1] != ']' {
			// A range, unless an end is a class escape.
			save := p.pos
			p.pos++
			hi, hiSet, err := p.classAtom()
			if err != nil {
				return "", err
			}
			if loSet != nil || hiSet != nil {
				if p.u {
					return "", p.syntax("Invalid character class")
				}
				_ = save
				items = appendAtom(items, lo, loSet)
				items = append(items, rng{'-', '-'})
				items = appendAtom(items, hi, hiSet)
				continue
			}
			if lo > hi {
				return "", p.syntax("Range out of order in character class")
			}
			items = append(items, rng{lo, hi})
			continue
		}
		items = appendAtom(items, lo, loSet)
	}
	switch {
	case len(items) == 0 && !negate:
		return `(?!)`, nil
	case len(items) == 0:
		return `[\s\S]`, nil
	}
	prefix := "["
	if negate {
		prefix = "[^"
	}
	return prefix + emitRanges(items) + "]", nil
}

func appendAtom(items []rng, r rune, set []rng) []rng {
	if set != nil {
		return append(items, set...)
	}
	return append(items, rng{r, r})
}

// classAtom is one character of a class, or a set for a class escape or property.
func (p *jsParser) classAtom() (rune, []rng, error) {
	c := p.src[p.pos]
	if c != '\\' {
		p.pos++
		return c, nil, nil
	}
	p.pos++
	e, ok := p.peek()
	if !ok {
		return 0, nil, p.syntax("\\ at end of pattern")
	}
	if rs, isClass := p.classEscape(e); isClass {
		p.pos++
		return 0, rs, nil
	}
	switch e {
	case 'b':
		p.pos++
		return '\b', nil, nil
	case 'p', 'P':
		if p.u {
			return 0, nil, &jsUnsupportedError{"a property escape inside a regular expression character class"}
		}
	case 'k':
		// Inside a class \k is an identity escape without the u flag.
		if p.u {
			return 0, nil, p.syntax("Invalid escape")
		}
	}
	if e >= '1' && e <= '9' && p.u {
		return 0, nil, p.syntax("Invalid class escape")
	}
	if e == '8' || e == '9' {
		p.pos++
		return e, nil, nil
	}
	r, err := p.characterEscapeRune(true)
	return r, nil, err
}
