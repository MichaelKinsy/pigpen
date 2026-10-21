package warden

import (
	"regexp"
	"regexp/syntax"
	"strings"
)

// A gate is a necessary condition of a match that costs a substring search: Go's regexp engine tries an expression
// at every byte offset of the text (it only skips ahead for a literal prefix), where V8 and RE2's own prefilter
// reject a text that lacks the expression's literals at once. A lazyRegexp builds its gate from the parsed
// expression when it first compiles it, and a text that fails the gate goes no further: the answer is the one the
// expression would give, "no match", which every method below returns as the engine does.
//
// The gate holds up to two required sets. A set is a list of ASCII literals of which at least one occurs in every
// text the expression matches: the literal of a concatenation, or the union of an alternation's branches. A set is
// "folded" when it comes from a case-insensitive literal; it is then compared ASCII case-insensitively and only on
// ASCII text, because a folded 'k' or 's' also matches the Kelvin sign and the long s.
type gateSet struct {
	lits []string // lower case when fold
	fold bool
}

type gate struct{ sets []gateSet }

const (
	gateMinLen  = 2  // a one-byte literal costs a scan and rejects little
	gateMaxLits = 32 // an alternation with more branches is not worth the scans
)

// buildGate is the gate of an expression, nil when the expression gives none.
func buildGate(expr string) *gate {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return nil
	}
	sets := bestSets(gateSets(re), 2)
	if len(sets) == 0 {
		return nil
	}
	return &gate{sets: sets}
}

func (s gateSet) minLen() int {
	n := len(s.lits[0])
	for _, l := range s.lits[1:] {
		n = min(n, len(l))
	}
	return n
}

// bestSets keeps the n sets that reject the most: the longest shortest literal, then the fewest literals.
func bestSets(sets []gateSet, n int) []gateSet {
	var good []gateSet
	for _, s := range sets {
		if s.minLen() >= gateMinLen {
			good = append(good, s)
		}
	}
	for i := 1; i < len(good); i++ {
		for j := i; j > 0 && better(good[j], good[j-1]); j-- {
			good[j], good[j-1] = good[j-1], good[j]
		}
	}
	if len(good) > n {
		good = good[:n]
	}
	return good
}

func better(a, b gateSet) bool {
	if a.minLen() != b.minLen() {
		return a.minLen() > b.minLen()
	}
	return len(a.lits) < len(b.lits)
}

// gateSets lists the sets that every match of re must satisfy (all of them, not one).
func gateSets(re *syntax.Regexp) []gateSet {
	switch re.Op {
	case syntax.OpLiteral:
		fold := re.Flags&syntax.FoldCase != 0
		b := make([]byte, 0, len(re.Rune))
		for _, r := range re.Rune {
			if r >= 0x80 {
				return nil
			}
			if fold && r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			b = append(b, byte(r))
		}
		if len(b) == 0 {
			return nil
		}
		return []gateSet{{lits: []string{string(b)}, fold: fold}}
	case syntax.OpCapture, syntax.OpPlus:
		return gateSets(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return gateSets(re.Sub[0])
		}
	case syntax.OpConcat:
		var out []gateSet
		for _, sub := range re.Sub {
			out = append(out, gateSets(sub)...)
		}
		return out
	case syntax.OpAlternate:
		var union gateSet
		for _, sub := range re.Sub {
			best := bestSets(gateSets(sub), 1)
			if len(best) == 0 {
				return nil
			}
			union.fold = union.fold || best[0].fold
			union.lits = append(union.lits, best[0].lits...)
			if len(union.lits) > gateMaxLits {
				return nil
			}
		}
		if union.fold {
			for i, l := range union.lits {
				union.lits[i] = strings.ToLower(l)
			}
		}
		return []gateSet{union}
	}
	return nil
}

// may reports whether s can match: false is final.
func (g *gate) may(s string) bool {
	if g == nil {
		return true
	}
	ascii, known := false, false
	for _, set := range g.sets {
		if set.fold {
			if !known {
				ascii, known = isASCII(s), true
			}
			if !ascii {
				continue
			}
		}
		found := false
		for _, lit := range set.lits {
			if set.fold && indexFold(s, lit) >= 0 || !set.fold && strings.Contains(s, lit) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// indexFold is the index of lit (lower case ASCII) in the ASCII text s, ignoring the case of letters, or -1.
func indexFold(s, lit string) int {
	first := lit[0]
	for i := 0; i+len(lit) <= len(s); i++ {
		if c := s[i]; c != first && c != first-'a'+'A' {
			continue
		}
		if foldPrefix(s[i:], lit) {
			return i
		}
	}
	return -1
}

// gatedRegexp is a compiled expression that carries its gate: the compiled-and-cached expressions built from
// configuration (globs, user rules, arming commands) are not lazyRegexps but need the same early rejection.
type gatedRegexp struct {
	*regexp.Regexp
	gate *gate
}

func compileGated(expr string) (*gatedRegexp, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return &gatedRegexp{Regexp: re, gate: buildGate(expr)}, nil
}

// MatchString is regexp.Regexp.MatchString after the gate.
func (g *gatedRegexp) MatchString(s string) bool { return g.gate.may(s) && g.Regexp.MatchString(s) }
