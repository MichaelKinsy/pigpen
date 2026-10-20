package websearch

import (
	"strconv"
	"strings"
)

// A small backtracking engine for the JavaScript regular expressions of mhchem (dist/temml.cjs): literals, classes, the
// escapes \s \S \d \D \w \W, groups, alternation, greedy quantifiers and the lookaheads (?=...) and (?!...), none of
// which RE2 offers together. It works on runes, so a character outside the BMP counts as one (the original counts
// UTF-16 units; see port/defuddle-math-notes.md).

type jsReNode func(s *jsReState, i int, k func(int) bool) bool

type jsReState struct {
	in   []rune
	caps []int
}

type jsRegex struct {
	root jsReNode
	ncap int
}

type jsReParser struct {
	src  []rune
	pos  int
	ncap int
}

func tmCompileRe(src string) *jsRegex {
	p := &jsReParser{src: []rune(src)}
	root := p.parseAlt()
	if p.pos != len(p.src) {
		panic("jsre: unexpected " + string(p.src[p.pos:]) + " in " + src)
	}
	return &jsRegex{root: root, ncap: p.ncap}
}

// exec is RegExp.prototype.exec without the g flag: the capture strings (index 0 is the whole match; ok flags the
// groups that took part) of the first match at or after from.
func (re *jsRegex) exec(in []rune) (caps []string, ok []bool, end int, matched bool) {
	for start := 0; start <= len(in); start++ {
		s := &jsReState{in: in, caps: make([]int, 2*(re.ncap+1))}
		for i := range s.caps {
			s.caps[i] = -1
		}
		var endPos int
		if re.root(s, start, func(e int) bool { endPos = e; return true }) {
			caps = make([]string, re.ncap+1)
			ok = make([]bool, re.ncap+1)
			caps[0], ok[0] = string(in[start:endPos]), true
			for g := 1; g <= re.ncap; g++ {
				if s.caps[2*g] >= 0 && s.caps[2*g+1] >= 0 {
					caps[g], ok[g] = string(in[s.caps[2*g]:s.caps[2*g+1]]), true
				}
			}
			return caps, ok, endPos, true
		}
	}
	return nil, nil, 0, false
}

func (p *jsReParser) more() bool { return p.pos < len(p.src) }
func (p *jsReParser) peek() rune {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return -1
}

func (p *jsReParser) parseAlt() jsReNode {
	alts := []jsReNode{p.parseSeq()}
	for p.peek() == '|' {
		p.pos++
		alts = append(alts, p.parseSeq())
	}
	if len(alts) == 1 {
		return alts[0]
	}
	return func(s *jsReState, i int, k func(int) bool) bool {
		for _, a := range alts {
			if a(s, i, k) {
				return true
			}
		}
		return false
	}
}

func (p *jsReParser) parseSeq() jsReNode {
	var items []jsReNode
	for p.more() && p.peek() != '|' && p.peek() != ')' {
		items = append(items, p.parseQuantified())
	}
	return func(s *jsReState, i int, k func(int) bool) bool {
		var run func(idx, pos int) bool
		run = func(idx, pos int) bool {
			if idx == len(items) {
				return k(pos)
			}
			return items[idx](s, pos, func(n int) bool { return run(idx+1, n) })
		}
		return run(0, i)
	}
}

func (p *jsReParser) parseQuantified() jsReNode {
	atom := p.parseAtom()
	min, max := 1, 1
	switch p.peek() {
	case '*':
		min, max = 0, -1
		p.pos++
	case '+':
		min, max = 1, -1
		p.pos++
	case '?':
		min, max = 0, 1
		p.pos++
	case '{':
		end := p.pos + 1
		for end < len(p.src) && p.src[end] != '}' {
			end++
		}
		body := string(p.src[p.pos+1 : end])
		parts := strings.Split(body, ",")
		min, _ = strconv.Atoi(parts[0])
		if len(parts) == 1 {
			max = min
		} else if parts[1] == "" {
			max = -1
		} else {
			max, _ = strconv.Atoi(parts[1])
		}
		p.pos = end + 1
	default:
		return atom
	}
	return func(s *jsReState, i int, k func(int) bool) bool {
		var try func(count, pos int) bool
		try = func(count, pos int) bool {
			if max == -1 || count < max {
				if atom(s, pos, func(n int) bool {
					if n == pos && count >= min {
						return false
					}
					return try(count+1, n)
				}) {
					return true
				}
			}
			if count >= min {
				return k(pos)
			}
			return false
		}
		return try(0, i)
	}
}

func tmIsRegexSpace(r rune) bool { return jsSpace(r) }

func (p *jsReParser) parseAtom() jsReNode {
	c := p.src[p.pos]
	p.pos++
	switch c {
	case '(':
		if p.peek() == '?' {
			p.pos++
			kind := p.src[p.pos]
			p.pos++
			inner := p.parseAlt()
			p.pos++ // )
			switch kind {
			case ':':
				return inner
			case '=', '!':
				neg := kind == '!'
				return func(s *jsReState, i int, k func(int) bool) bool {
					saved := append([]int(nil), s.caps...)
					m := inner(s, i, func(int) bool { return true })
					if neg {
						copy(s.caps, saved)
						return !m && k(i)
					}
					return m && k(i)
				}
			}
			panic("jsre: unsupported group")
		}
		p.ncap++
		idx := p.ncap
		inner := p.parseAlt()
		p.pos++ // )
		return func(s *jsReState, i int, k func(int) bool) bool {
			oldS, oldE := s.caps[2*idx], s.caps[2*idx+1]
			if inner(s, i, func(n int) bool {
				ps, pe := s.caps[2*idx], s.caps[2*idx+1]
				s.caps[2*idx], s.caps[2*idx+1] = i, n
				if k(n) {
					return true
				}
				s.caps[2*idx], s.caps[2*idx+1] = ps, pe
				return false
			}) {
				return true
			}
			s.caps[2*idx], s.caps[2*idx+1] = oldS, oldE
			return false
		}
	case '^':
		return func(s *jsReState, i int, k func(int) bool) bool { return i == 0 && k(i) }
	case '$':
		return func(s *jsReState, i int, k func(int) bool) bool { return i == len(s.in) && k(i) }
	case '.':
		return func(s *jsReState, i int, k func(int) bool) bool {
			if i < len(s.in) {
				r := s.in[i]
				if r != '\n' && r != '\r' && r != 0x2028 && r != 0x2029 {
					return k(i + 1)
				}
			}
			return false
		}
	case '[':
		return p.parseClass()
	case '\\':
		set := p.parseEscape(false)
		return func(s *jsReState, i int, k func(int) bool) bool {
			return i < len(s.in) && set(s.in[i]) && k(i+1)
		}
	}
	lit := c
	return func(s *jsReState, i int, k func(int) bool) bool { return i < len(s.in) && s.in[i] == lit && k(i+1) }
}

// parseEscape reads the escape after a backslash as a rune predicate.
func (p *jsReParser) parseEscape(inClass bool) func(rune) bool {
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 's':
		return tmIsRegexSpace
	case 'S':
		return func(r rune) bool { return !tmIsRegexSpace(r) }
	case 'd':
		return func(r rune) bool { return r >= '0' && r <= '9' }
	case 'D':
		return func(r rune) bool { return !(r >= '0' && r <= '9') }
	case 'w':
		return func(r rune) bool {
			return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		}
	case 'W':
		return func(r rune) bool {
			return !(r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'))
		}
	case 'n':
		return func(r rune) bool { return r == '\n' }
	case 'u':
		n, _ := strconv.ParseInt(string(p.src[p.pos:p.pos+4]), 16, 32)
		p.pos += 4
		return func(r rune) bool { return r == rune(n) }
	}
	return func(r rune) bool { return r == c }
}

func (p *jsReParser) parseClass() jsReNode {
	neg := false
	if p.peek() == '^' {
		neg = true
		p.pos++
	}
	var preds []func(rune) bool
	for p.src[p.pos] != ']' {
		var lo rune
		var pred func(rune) bool
		if p.src[p.pos] == '\\' {
			p.pos++
			save := p.pos
			pred = p.parseEscape(true)
			// a single-character escape can start a range
			if p.pos-save == 1 && !strings.ContainsRune("sSdDwW", p.src[save]) {
				lo = p.src[save]
				if p.src[save] == 'n' {
					lo = '\n'
				}
			} else if p.src[save] == 'u' {
				n, _ := strconv.ParseInt(string(p.src[save+1:save+5]), 16, 32)
				lo = rune(n)
			} else {
				preds = append(preds, pred)
				continue
			}
		} else {
			lo = p.src[p.pos]
			p.pos++
		}
		if p.peek() == '-' && p.pos+1 < len(p.src) && p.src[p.pos+1] != ']' {
			p.pos++
			var hi rune
			if p.src[p.pos] == '\\' {
				p.pos++
				if p.src[p.pos] == 'u' {
					n, _ := strconv.ParseInt(string(p.src[p.pos+1:p.pos+5]), 16, 32)
					hi = rune(n)
					p.pos += 5
				} else {
					hi = p.src[p.pos]
					p.pos++
				}
			} else {
				hi = p.src[p.pos]
				p.pos++
			}
			l, h := lo, hi
			preds = append(preds, func(r rune) bool { return r >= l && r <= h })
		} else {
			l := lo
			preds = append(preds, func(r rune) bool { return r == l })
		}
	}
	p.pos++ // ]
	return func(s *jsReState, i int, k func(int) bool) bool {
		if i >= len(s.in) {
			return false
		}
		in := false
		for _, f := range preds {
			if f(s.in[i]) {
				in = true
				break
			}
		}
		return in != neg && k(i+1)
	}
}
