package warden

import (
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The test file name sorts last, so by the time this runs the suite has compiled most of the package's
// expressions; lazyObserve collects them, and the registry's are compiled here.
var (
	observedMu sync.Mutex
	observed   []*lazyRegexp
)

func init() {
	lazyObserve = func(l *lazyRegexp) {
		observedMu.Lock()
		observed = append(observed, l)
		observedMu.Unlock()
	}
}

func gateLits(g *gate) []string {
	var out []string
	if g == nil {
		return out
	}
	for _, s := range g.sets {
		out = append(out, s.lits...)
	}
	return out
}

func TestBuildGateFindsTheLiteralsAnExpressionCannotMatchWithout(t *testing.T) {
	for expr, want := range map[string][]string{
		`(?i)\bdrop\s+(?:table|database|schema)\b`:                     {"drop", "database", "schema", "table"},
		`\bgit\s+push\b[^\n;&|]*--force-with-lease`:                    {"--force-with-lease", "push"},
		`(?i)\becho\b`:                                                 {"echo"},
		`\bfind\b[^\n;&|]*(?:-delete\b|-exec\w*\s+rm\b)`:               {"delete", "exec", "find"},
		`^\s*wc\b`:                                                     {"wc"},
		`[a-z]+`:                                                       nil,
		`a|b`:                                                          nil,
		`(?:foo|ba)r`:                                                  {"foo", "ba"},
		`x?abc*`:                                                       {"ab"},
	} {
		got := gateLits(buildGate(expr))
		sortStrings(got)
		w := append([]string(nil), want...)
		sortStrings(w)
		if !reflect.DeepEqual(got, w) && (len(got) > 0 || len(w) > 0) {
			t.Errorf("%s: gate %q want %q", expr, got, w)
		}
	}
	// A folded literal is compared on ASCII text only: the Kelvin sign and the long s fold to k and s.
	g := buildGate(`(?i)\bkey=`)
	if !g.may("KEY=") || !g.may("kEy=") || g.may("ke=") || !g.may("\u212aEY=") {
		t.Error("folded gate")
	}
	if g := buildGate(`\bkey=`); g.may("KEY=") || !g.may("key=") {
		t.Error("exact gate")
	}
}

func TestEveryLazyExpressionAgreesWithItsGate(t *testing.T) {
	for _, l := range lazyExpressions() {
		l.get()
	}
	observedMu.Lock()
	list := append([]*lazyRegexp(nil), observed...)
	observedMu.Unlock()
	r := rand.New(rand.NewPCG(9, 9))
	noise := []string{" ", " ", "\n", "a", "x", "1", "-", ":", "/", ".", "é", "\u212a", "ſ", "\t", "'", "\"", "$", "=", "_", "~"}
	word := regexp.MustCompile(`[A-Za-z0-9_./=:~-]{2,}`)
	gated := 0
	checked, seen := 0, map[string]bool{}
	for _, l := range list {
		if seen[l.expr] {
			continue
		}
		seen[l.expr] = true
		checked++
		re, g := l.re, l.gate
		if g != nil {
			gated++
		}
		vocab := append([]string(nil), noise...)
		for _, w := range word.FindAllString(l.expr, -1) {
			vocab = append(vocab, w, strings.ToUpper(w), w[1:], w[:len(w)-1])
		}
		for _, lit := range gateLits(g) {
			vocab = append(vocab, lit, strings.ToUpper(lit), lit[1:], lit[:len(lit)-1])
		}
		for range 150 {
			text := genText(r, vocab, 8)
			if !g.may(text) && re.MatchString(text) {
				t.Fatalf("%s: the gate rejected %q, which the expression matches", l.expr, text)
			}
			if l.MatchString(text) != re.MatchString(text) ||
				l.FindString(text) != re.FindString(text) ||
				!reflect.DeepEqual(l.FindStringIndex(text), re.FindStringIndex(text)) ||
				!reflect.DeepEqual(l.FindStringSubmatch(text), re.FindStringSubmatch(text)) ||
				!reflect.DeepEqual(l.FindStringSubmatchIndex(text), re.FindStringSubmatchIndex(text)) ||
				!reflect.DeepEqual(l.FindAllString(text, -1), re.FindAllString(text, -1)) ||
				!reflect.DeepEqual(l.FindAllStringIndex(text, -1), re.FindAllStringIndex(text, -1)) ||
				!reflect.DeepEqual(l.FindAllStringSubmatch(text, -1), re.FindAllStringSubmatch(text, -1)) ||
				l.ReplaceAllString(text, "<$0>") != re.ReplaceAllString(text, "<$0>") ||
				l.ReplaceAllLiteralString(text, "#") != re.ReplaceAllLiteralString(text, "#") ||
				l.ReplaceAllStringFunc(text, strings.ToUpper) != re.ReplaceAllStringFunc(text, strings.ToUpper) {
				t.Fatalf("%s: a method differs from the engine's on %q", l.expr, text)
			}
		}
	}
	t.Logf("%d distinct expressions seen, %d gated", checked, gated)
	if checked < 100 || gated < checked/4 {
		t.Errorf("only %d of %d distinct expressions seen are gated: the observer or the analysis is broken", gated, checked)
	}
}
