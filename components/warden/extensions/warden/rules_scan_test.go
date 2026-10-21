package warden

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The hand matchers of rules_scan.go against the expressions they stand for, over generated lines: hash runs of every
// length, every mix of spaces, tabs, form feeds and Unicode spaces before, inside and after a title, trailing hashes,
// colons in every place, keywords in every case with their near misses, and lines that are not plain ASCII.

func genScanLine(r *rand.Rand) string {
	pieces := []string{"#", "#", "##", "###", "######", "#######", " ", " ", "  ", "\t", "\f", "\v", "\u00a0", "\u3000", "\u2028", "\u2029", "\ufeff", "a", "b", "Title", "word", "é", "日", "\xff", "-", "_", "`", "x y", "*", "~"}
	var b strings.Builder
	for range r.IntN(10) {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.String()
}

func TestHeadingMatchAgreesWithTheExpression(t *testing.T) {
	r := rand.New(rand.NewPCG(51, 51))
	heading, ascii := 0, 0
	for i := range 600000 {
		line := genScanLine(r)
		if r.IntN(2) == 0 {
			line = "#" + strings.Repeat("#", r.IntN(7)) + " " + line
		}
		got, ok := headingMatch(line)
		want := reHeading.FindStringSubmatch(line)
		if ok != (want != nil) || ok && (got[0] != want[0] || got[1] != want[1] || got[2] != want[2]) {
			t.Fatalf("case %d %q: got %q %v, want %q", i, line, got, ok, want)
		}
		if ok {
			heading++
			if plainLine(line) {
				ascii++
			}
		}
	}
	if heading < 50000 || ascii < 30000 {
		t.Fatalf("generator too narrow: %d headings, %d plain", heading, ascii)
	}
	for _, line := range []string{"", "#", "# ", "#  ", "#   ", "# a", "#  a ", "#\t", "# a #", "# a ##  ", "# #", "# ##", "## ## ##", "####### a", "###### a", "#a", "# \u00a0", "#\u3000", "# a\u2028", "# \u2028 a"} {
		got, ok := headingMatch(line)
		want := reHeading.FindStringSubmatch(line)
		if ok != (want != nil) || ok && (got[1] != want[1] || got[2] != want[2]) {
			t.Errorf("%q: got %q %v, want %q", line, got, ok, want)
		}
	}
}

func TestHeaderMatchAgreesWithTheExpressions(t *testing.T) {
	specs := []*headerSpec{headerPaths, headerThreshold, headerSeverity, headerSource, headerWhen}
	words := []string{"paths", "path", "Paths", "PATH", "pathss", "pat", "applies to", "Applies To", "applies  to", "appliesto", "files", "file", "File", "FILES", "filess", "threshold", "Threshold", "THRESHOLD", "thresholds", "severity", "Severity", "sev", "source", "Source", "SOURCE", "sources", "when", "When", "WHEN", "whenever", "ſource", "\u212a", "paths\u00a0"}
	spaces := []string{"", " ", "  ", "\t", " \t", "\f", "\v", "\u00a0", "\u3000", "\u2028"}
	values := []string{"", "a", "src/**/*.ts", "`a.go`, b.go", " x", "x ", " x ", "0.5", "high", "a.md:3", "turn", ":", "a:b", "é", "x\u00a0", "# t", "a  b", "\xff"}
	r := rand.New(rand.NewPCG(52, 52))
	matched := map[int]int{}
	for i := range 600000 {
		spec := r.IntN(len(specs))
		var b strings.Builder
		b.WriteString(spaces[r.IntN(len(spaces))])
		b.WriteString(words[r.IntN(len(words))])
		b.WriteString(spaces[r.IntN(len(spaces))])
		if r.IntN(10) != 0 {
			b.WriteString(":")
		}
		b.WriteString(spaces[r.IntN(len(spaces))])
		b.WriteString(values[r.IntN(len(values))])
		b.WriteString(spaces[r.IntN(len(spaces))])
		line := b.String()
		if r.IntN(8) == 0 {
			line = genScanLine(r) + line
		}
		got, ok := headerMatch(specs[spec], line)
		want := specs[spec].re.FindStringSubmatch(line)
		if ok != (want != nil) || ok && (got[0] != want[0] || got[1] != want[1]) {
			t.Fatalf("case %d spec %d %q: got %q %v, want %q", i, spec, line, got, ok, want)
		}
		if ok {
			matched[spec]++
		}
	}
	for spec := range specs {
		if matched[spec] < 3000 {
			t.Fatalf("generator too narrow for spec %d: %d matches", spec, matched[spec])
		}
	}
}

func TestSlugAgreesWithTheReference(t *testing.T) {
	r := rand.New(rand.NewPCG(53, 53))
	pieces := []string{"Use", "named", " ", "  ", "-", "--", "_", "/", "!", "A", "z", "9", "0", "Rule", ".", "é", "İ", "日本", "\u00a0", "ß", "a", "\n", "\t", strings.Repeat("longword", 5)}
	for i := range 200000 {
		name := genText(r, pieces, 12)
		usedGot, usedWant := map[string]bool{}, map[string]bool{}
		if r.IntN(3) == 0 {
			usedGot["rule"], usedWant["rule"] = true, true
		}
		if got, want := slug(name, usedGot), slugRef(name, usedWant); got != want || !reflect.DeepEqual(usedGot, usedWant) {
			t.Fatalf("case %d %q: got %q want %q", i, name, got, want)
		}
	}
}
