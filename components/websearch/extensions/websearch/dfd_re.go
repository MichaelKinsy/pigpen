package websearch

import (
	"strings"
	"sync"
)

// JavaScript regular expressions for Go's RE2. A pattern is written as the original writes it and translated:
// `\s` and `\S` are the whole of JavaScript's whitespace, `.` leaves out all four line terminators, `\uXXXX`
// is a code point, and `$` or `^` with the m flag also see \r, \u2028 and \u2029 only where the caller handles it
// (not translated). Patterns that need lookaround or backreferences are written by hand where they occur.

const dfdWS = `\t\n\x{0b}\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var jsReCache sync.Map

// jsRE is a JavaScript pattern with the flags i, m, s (g and y are the caller's business), compiled when it is first
// used (lazyre.go).
func jsRE(pattern, flags string) *lazyRE {
	key := flags + "/" + pattern
	if v, ok := jsReCache.Load(key); ok {
		return v.(*lazyRE)
	}
	v, _ := jsReCache.LoadOrStore(key, newLazyRE(pattern, flags, true))
	return v.(*lazyRE)
}

func jsTranslate(p, flags string) string {
	var b strings.Builder
	if strings.Contains(flags, "i") {
		b.WriteString("(?i)")
	}
	if strings.Contains(flags, "m") {
		b.WriteString("(?m)")
	}
	dotAll := strings.Contains(flags, "s")
	inClass := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\' && i+1 < len(p):
			i++
			e := p[i]
			switch e {
			case 's':
				if inClass {
					b.WriteString(dfdWS)
				} else {
					b.WriteString("[" + dfdWS + "]")
				}
			case 'S':
				if inClass {
					// only supported as the pair [\s\S]
					b.WriteString(`\S`)
				} else {
					b.WriteString("[^" + dfdWS + "]")
				}
			case 'u':
				if i+4 < len(p) {
					b.WriteString(`\x{` + p[i+1:i+5] + `}`)
					i += 4
				} else {
					b.WriteString(`\u`)
				}
			case '/':
				b.WriteByte('/')
			default:
				b.WriteByte('\\')
				b.WriteByte(e)
			}
		case c == '[' && !inClass:
			if strings.HasPrefix(p[i:], `[\s\S]`) {
				b.WriteString(`(?s:.)`)
				i += len(`[\s\S]`) - 1
				continue
			}
			inClass = true
			b.WriteByte('[')
		case c == ']' && inClass:
			inClass = false
			b.WriteByte(']')
		case c == '.' && !inClass:
			if dotAll {
				b.WriteString(`(?s:.)`)
			} else {
				b.WriteString(`[^\n\r\x{2028}\x{2029}]`)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// jsReplaceAll is s.replace(/re/g, repl) with a function replacement.
func jsReplaceAll(re *lazyRE, s string, repl func(m []string) string) string {
	return jsReplace(re, s, -1, repl)
}

// jsReplaceFirst is s.replace(/re/, repl).
func jsReplaceFirst(re *lazyRE, s string, repl func(m []string) string) string {
	return jsReplace(re, s, 1, repl)
}

func jsReplace(re *lazyRE, s string, n int, repl func(m []string) string) string {
	locs := re.FindAllStringSubmatchIndex(s, n)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		b.WriteString(s[last:loc[0]])
		m := make([]string, len(loc)/2)
		for i := range m {
			if loc[2*i] >= 0 {
				m[i] = s[loc[2*i]:loc[2*i+1]]
			}
		}
		b.WriteString(repl(m))
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// jsReplaceStr is s.replace(/re/g, str) for a replacement with no `$` pattern.
func jsReplaceStr(re *lazyRE, s, repl string) string {
	return re.ReplaceAllLiteralString(s, repl)
}

// jsSplitWS is s.split(/\s+/): note the empty strings a leading or trailing run yields.
func jsSplitWS(s string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		r, size := decodeRune(s[i:])
		if jsSpace(r) {
			j := i + size
			for j < len(s) {
				r2, size2 := decodeRune(s[j:])
				if !jsSpace(r2) {
					break
				}
				j += size2
			}
			out = append(out, s[start:i])
			start = j
			i = j
			continue
		}
		i += size
	}
	return append(out, s[start:])
}

// jsCollapseWS is s.replace(/\s+/g, ' ').
func jsCollapseWS(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if jsSpace(r) {
			if !in {
				b.WriteByte(' ')
			}
			in = true
			continue
		}
		in = false
		b.WriteRune(r)
	}
	return b.String()
}
