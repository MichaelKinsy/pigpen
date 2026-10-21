package warden

import (
	"math/bits"
	"strings"
)

// A glob (`**/` any directories, `**` anything, `*` within a segment, `?` one character) compiles, in done.go, to the
// expression `^(?:.*/)?BODY$`. Matching a short path against it costs the regexp engine about a microsecond (a pooled
// backtracker and a bitmap for the text); the same match is a walk of at most 63 states over the bytes. The walk is
// exact where `.` and `[^/]` mean one byte, that is for text without "\n" or a non-ASCII byte, so a path with either
// goes to the expression.
const (
	globByte       = iota // the byte ch
	globAnyInSeg          // `[^/]`: any byte but "/"
	globStar              // `[^/]*`
	globDoubleStar        // `.*`
	globSplit             // the entry of `(?:.*/)?`: either the group is empty (three states on) or it starts
)

type globToken struct {
	kind uint8
	ch   byte
}

type globMatcher struct{ tokens []globToken }

// newGlobMatcher is the matcher of globBody's reading of pattern, or nil when it needs more than 63 states.
func newGlobMatcher(pattern string) *globMatcher {
	// `(?:.*/)?` is three states: the split, the `.*` loop and the `/`.
	dirs := []globToken{{kind: globSplit}, {kind: globDoubleStar}, {kind: globByte, ch: '/'}}
	tokens := append([]globToken(nil), dirs...)
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			tokens = append(tokens, dirs...)
			i += 3
		case strings.HasPrefix(pattern[i:], "**"):
			tokens = append(tokens, globToken{kind: globDoubleStar})
			i += 2
		case pattern[i] == '*':
			tokens = append(tokens, globToken{kind: globStar})
			i++
		case pattern[i] == '?':
			tokens = append(tokens, globToken{kind: globAnyInSeg})
			i++
		default:
			tokens = append(tokens, globToken{kind: globByte, ch: pattern[i]})
			i++
		}
	}
	if len(tokens) > 63 {
		return nil
	}
	return &globMatcher{tokens: tokens}
}

// plainPath is the text the matcher is exact for: ASCII without a newline.
func plainPath(s string) bool {
	for i := range len(s) {
		if c := s[i]; c >= 0x80 || c == '\n' {
			return false
		}
	}
	return true
}

// closure adds the states reachable without reading a byte: the end of a star, `.*` or `(?:.*/)?`.
func (g *globMatcher) closure(mask uint64) uint64 {
	for st, tok := range g.tokens {
		if mask>>st&1 == 0 {
			continue
		}
		switch tok.kind {
		case globStar, globDoubleStar:
			mask |= 1 << (st + 1)
		case globSplit:
			mask |= 1<<(st+1) | 1<<(st+3)
		}
	}
	return mask
}

// match is the expression's MatchString for a plainPath text.
func (g *globMatcher) match(s string) bool {
	end := len(g.tokens)
	mask := g.closure(1)
	for i := range len(s) {
		c := s[i]
		var next uint64
		for m := mask; m != 0; m &= m - 1 {
			st := bits.TrailingZeros64(m)
			if st == end {
				continue
			}
			switch tok := g.tokens[st]; tok.kind {
			case globByte:
				if c == tok.ch {
					next |= 1 << (st + 1)
				}
			case globAnyInSeg:
				if c != '/' {
					next |= 1 << (st + 1)
				}
			case globStar:
				if c != '/' {
					next |= 1 << st
				}
			case globDoubleStar:
				next |= 1 << st
			}
		}
		if mask = g.closure(next); mask == 0 {
			return false
		}
	}
	return mask>>end&1 != 0
}
