package warden

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The line scanner, the heading and header prefilters of rules.go against the code they replace
// (rules_parse_ref_test.go), over generated markdown: every line break, fences of both kinds and lengths with up to
// four leading spaces of each Unicode kind, headings of each level with trailing hashes and odd spacing, header lines
// in every spelling and order, repeated and bad values, and noise.

var mdGenLines = []string{
	"# Rule one", "## Rule two", "### Deep", "####### too deep", "#nospace", "# ", "#", "#\tTabbed", "# Trailing ##", "# Trailing # x", "#  spaced  ", " # indented", "\u00a0# nbsp", "#\u3000wide", "# \ufeff", "## Title ##  ", "#\u2028x",
	"```", "```ts", "````", "``", "~~~", "~~~~", "~~", "~~~ts", " ```", "  ```", "   ```", "    ```", "\u00a0```", "\u3000\u3000~~~", "\ufeff```", "\v```", "\f```", "x ```", "a~~~", "```` more", "```~~~", "~~~```",
	"paths: src/**/*.ts", "path: `a.go`, b.go", "Paths:", "files : x y", "applies to: docs/*.md", "PATHS: a", "paths src", "paths: a\tb", "threshold: 0.5", "threshold: 2", "threshold:", "Threshold: 0x1", "threshold: `0.3`", "severity: high", "Severity: LOW", "severity: urgent", "severity:",
	"source: AGENTS.md:12", "source: x:0", "source: `a:3`", "source: nope", "when: turn", "when: edit", "When: sometimes", "when:", "\u00a0paths:\u00a0a", "paths\u3000:\u3000b", ":", "a: b", "note: this is text", "http://example.com",
	"The code must be short.", "Never log secrets.", "- bullet one", "1. numbered", "* star", "text with ` tick", "text with ~ tilde", "", "", "   ", "\t", "é", "日本語", "\xff", "# 🙂 emoji heading",
}

var mdGenBreaks = []string{"\n", "\n", "\n", "\r\n", "\r", "\n\n", "\r\n\r\n", "\n\r", "\r\r", "\n"}

func genMarkdown(r *rand.Rand) string {
	var b strings.Builder
	n := r.IntN(30)
	for range n {
		b.WriteString(mdGenLines[r.IntN(len(mdGenLines))])
		b.WriteString(mdGenBreaks[r.IntN(len(mdGenBreaks))])
	}
	if r.IntN(2) == 0 {
		b.WriteString(mdGenLines[r.IntN(len(mdGenLines))])
	}
	return b.String()
}

// genRuleDoc is a document of rule sections whose headers come in a random order, some repeated.
func genRuleDoc(r *rand.Rand) string {
	headers := []string{"paths: src/**/*.ts", "threshold: 0.4", "severity: high", "source: a.md:3", "when: turn", "paths: x", "threshold: bad", "severity: zzz", "source: bad", "when: never", "", "   "}
	var b strings.Builder
	b.WriteString(mdGenLines[r.IntN(len(mdGenLines))] + "\n")
	for range 1 + r.IntN(5) {
		b.WriteString("## Rule " + string(rune('a'+r.IntN(26))) + "\n")
		for range r.IntN(7) {
			b.WriteString(headers[r.IntN(len(headers))] + mdGenBreaks[r.IntN(len(mdGenBreaks))])
		}
		for range r.IntN(5) {
			b.WriteString(mdGenLines[r.IntN(len(mdGenLines))] + mdGenBreaks[r.IntN(len(mdGenBreaks))])
		}
	}
	return b.String()
}

func TestRulesParsingAgreesWithTheReference(t *testing.T) {
	r := rand.New(rand.NewPCG(21, 21))
	withRules, fenced, shaped := 0, 0, 0
	for i := range 150000 {
		var doc string
		switch i % 3 {
		case 0:
			doc = genMarkdown(r)
		case 1:
			doc = genRuleDoc(r)
		default:
			doc = genMarkdown(r) + genRuleDoc(r) + genMarkdown(r)
		}
		got, want := taggedLines(doc), taggedLinesRef(doc)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d %q: taggedLines\n got  %+v\n want %+v", i, doc, got, want)
		}
		if gl, wl := ruleLevel(got), ruleLevelRef(want); gl != wl {
			t.Fatalf("case %d %q: ruleLevel %d, want %d", i, doc, gl, wl)
		}
		gr, wr := parseRules(doc), parseRulesRef(doc)
		if !reflect.DeepEqual(gr, wr) {
			t.Fatalf("case %d %q: parseRules\n got  %+v\n want %+v", i, doc, gr, wr)
		}
		if gs, ws := isRuleShaped(doc), isRuleShapedRef(doc); gs != ws {
			t.Fatalf("case %d %q: isRuleShaped %v, want %v", i, doc, gs, ws)
		}
		if len(wr) > 0 {
			withRules++
		}
		for _, l := range want {
			if l.fenced {
				fenced++
				break
			}
		}
		if isRuleShapedRef(doc) {
			shaped++
		}
	}
	if withRules < 30000 || fenced < 20000 || shaped < 5000 {
		t.Fatalf("generator too narrow: %d with rules, %d fenced, %d rule-shaped", withRules, fenced, shaped)
	}
	for _, doc := range []string{"", "\n", "\r\n", "\r", "a", "a\n", "a\r\n", "\n\n", "# x", "# x\n"} {
		if got, want := taggedLines(doc), taggedLinesRef(doc); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %+v, want %+v", doc, got, want)
		}
	}
}

// condense reads headings through the same prefilter.
func TestCondenseKeepsItsHeadings(t *testing.T) {
	r := rand.New(rand.NewPCG(22, 22))
	for range 3000 {
		doc := genMarkdown(r) + strings.Repeat("filler line of text\n", r.IntN(60))
		got := condense(doc, 400)
		want := condenseRef(doc, 400)
		if got != want {
			t.Fatalf("%q:\n got  %q\n want %q", doc, got, want)
		}
	}
}
