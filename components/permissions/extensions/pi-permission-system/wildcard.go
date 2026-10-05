package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Port of src/policy/wildcard-matcher.ts and src/path/expand-home.ts.

// MatchOptions is the Windows path folding applied to path-surface patterns: a case-insensitive match and `/` read as `\` on both
// the pattern and the value.
type MatchOptions struct{ CaseInsensitive, WindowsSeparators bool }

// homeDir is the home directory, replaceable in tests (the original mocks node:os homedir()).
var homeDir = func() string {
	h, _ := os.UserHomeDir()
	return h
}

var homePrefixes = []string{"~", "$HOME", "${HOME}"}

// splitHomePrefix returns what follows a leading ~, $HOME or ${HOME} when that prefix ends the value or is followed by a separator.
func splitHomePrefix(v string) (string, bool) {
	for _, p := range homePrefixes {
		if !strings.HasPrefix(v, p) {
			continue
		}
		rest := v[len(p):]
		if rest == "" || strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, `\`) {
			return rest, true
		}
	}
	return "", false
}

func expandHomePath(pattern string) string {
	rest, ok := splitHomePrefix(pattern)
	if !ok {
		return pattern
	}
	if rest == "" {
		return homeDir()
	}
	return filepath.Join(homeDir(), rest[1:])
}

type tokKind int

const (
	tokLit  tokKind = iota
	tokAny          // ?
	tokStar         // *
)

type tok struct {
	kind tokKind
	u    uint16
}

// Compiled is a pattern compiled once for repeated matching, with the state it stands for.
type Compiled[S any] struct {
	Pattern string
	State   S
	toks    []tok
	tail    bool // a trailing " *" whose space and arguments are optional
	opts    *MatchOptions
}

// Entry is a pattern and its state.
type Entry[S any] struct {
	Pattern string
	State   S
}

// PatternMatch is the result of a lookup.
type PatternMatch[S any] struct {
	State                       S
	MatchedPattern, MatchedName string
}

func foldSeparators(v string, o *MatchOptions) string {
	if o != nil && o.WindowsSeparators {
		return strings.ReplaceAll(v, "/", `\`)
	}
	return v
}

// CompilePattern compiles `pattern`. `*` matches any run (newlines included), `?` one UTF-16 unit, everything else is literal. A
// trailing " *" also matches the bare prefix, so "git *" matches "git".
func CompilePattern[S any](pattern string, state S, o *MatchOptions) Compiled[S] {
	expanded := foldSeparators(expandHomePath(pattern), o)
	c := Compiled[S]{Pattern: pattern, State: state, opts: o}
	if strings.HasSuffix(expanded, " *") {
		c.tail = true
		expanded = expanded[:len(expanded)-2]
	}
	for _, u := range utf16.Encode([]rune(expanded)) {
		switch u {
		case '*':
			c.toks = append(c.toks, tok{kind: tokStar})
		case '?':
			c.toks = append(c.toks, tok{kind: tokAny})
		default:
			c.toks = append(c.toks, tok{kind: tokLit, u: u})
		}
	}
	return c
}

// canonical is the JavaScript non-unicode case-insensitive canonicalization: upper-case, unless that gives another length or maps a
// non-ASCII character onto ASCII.
func canonical(u uint16) uint16 {
	up := unicode.ToUpper(rune(u))
	if up > 0xFFFF || (u >= 128 && up < 128) {
		return u
	}
	return uint16(up)
}

func (c Compiled[S]) glob(toks []tok, v []uint16) bool {
	ci := c.opts != nil && c.opts.CaseInsensitive
	same := func(a, b uint16) bool {
		if ci {
			return canonical(a) == canonical(b)
		}
		return a == b
	}
	p, i, star, mark := 0, 0, -1, 0
	for i < len(v) {
		switch {
		case p < len(toks) && toks[p].kind == tokStar:
			star, mark = p, i
			p++
		case p < len(toks) && (toks[p].kind == tokAny || same(toks[p].u, v[i])):
			p++
			i++
		case star >= 0:
			mark++
			i, p = mark, star+1
		default:
			return false
		}
	}
	for p < len(toks) && toks[p].kind == tokStar {
		p++
	}
	return p == len(toks)
}

// Matches reports whether the value matches, folding it the way the pattern was folded.
func (c Compiled[S]) Matches(value string) bool {
	v := utf16.Encode([]rune(foldSeparators(value, c.opts)))
	if c.glob(c.toks, v) {
		return true
	}
	if c.tail {
		with := append(append([]tok{}, c.toks...), tok{kind: tokLit, u: ' '}, tok{kind: tokStar})
		return c.glob(with, v)
	}
	return false
}

// CompilePatternEntries compiles each entry, in order.
func CompilePatternEntries[S any](entries []Entry[S]) []Compiled[S] {
	out := make([]Compiled[S], 0, len(entries))
	for _, e := range entries {
		out = append(out, CompilePattern(e.Pattern, e.State, nil))
	}
	return out
}

// FindCompiledMatch returns the last pattern that matches (last match wins).
func FindCompiledMatch[S any](patterns []Compiled[S], name string) *PatternMatch[S] {
	for i := len(patterns) - 1; i >= 0; i-- {
		if patterns[i].Matches(name) {
			return &PatternMatch[S]{State: patterns[i].State, MatchedPattern: patterns[i].Pattern, MatchedName: name}
		}
	}
	return nil
}

// FindCompiledMatchForNames tries the trimmed, non-empty names in order and returns the first one that matches any pattern.
func FindCompiledMatchForNames[S any](patterns []Compiled[S], names []string) *PatternMatch[S] {
	for _, n := range names {
		n = jsTrim(n)
		if n == "" {
			continue
		}
		if m := FindCompiledMatch(patterns, n); m != nil {
			return m
		}
	}
	return nil
}

// WildcardMatch is CompilePattern(pattern).Matches(value).
func WildcardMatch(pattern, value string, o *MatchOptions) bool {
	return CompilePattern(pattern, struct{}{}, o).Matches(value)
}
