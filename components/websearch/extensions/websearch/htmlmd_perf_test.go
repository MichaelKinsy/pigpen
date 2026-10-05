package websearch

import (
	"math/rand"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The conversion keeps its original definitions here as oracles: mdEscape was thirteen regexp passes and mdChildren
// joined by copying the whole output every time (quadratic in the number of siblings). The fast versions must give
// the same text for any input.

var referenceEscapes = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`\\`), `\\`},
	{regexp.MustCompile(`\*`), `\*`},
	{regexp.MustCompile(`(?m)^-`), `\-`},
	{regexp.MustCompile(`(?m)^\+ `), `\+ `},
	{regexp.MustCompile(`(?m)^(=+)`), `\$1`},
	{regexp.MustCompile(`(?m)^(#{1,6}) `), `\$1 `},
	{regexp.MustCompile("`"), "\\`"},
	{regexp.MustCompile(`(?m)^~~~`), `\~~~`},
	{regexp.MustCompile(`\[`), `\[`},
	{regexp.MustCompile(`\]`), `\]`},
	{regexp.MustCompile(`(?m)^>`), `\>`},
	{regexp.MustCompile(`_`), `\_`},
	{regexp.MustCompile(`(?m)^(\d+)\. `), `$1\. `},
}

func referenceMdEscape(s string) string {
	for _, e := range referenceEscapes {
		s = e.re.ReplaceAllString(s, e.rep)
	}
	return s
}

func referenceMdJoin(a, b string) string {
	if strings.HasPrefix(b, "\n") {
		a = strings.TrimRight(a, " ")
	}
	s1, s2 := strings.TrimRight(a, "\n"), strings.TrimLeft(b, "\n")
	nls := len(a) - len(s1)
	if l := len(b) - len(s2); l > nls {
		nls = l
	}
	if nls > 2 {
		nls = 2
	}
	return s1 + "\n\n"[:nls] + s2
}

func TestMdEscapeMatchesTheRegexpDefinition(t *testing.T) {
	fixed := []string{"", "plain", `a\b`, "* item", "- item", "+ item", "+item", "== h", "=", "# h", "###### h", "####### h", "#nospace", "`code`", "~~~", "~~~~", "~~",
		"[a](b)", "a_b_c", "> q", ">q", "1. one", "12. twelve", "1.one", "1 . x", "a\n- b\n+ c\n1. d\n> e", "\n\n- x", "-\n-", "\\-", "\\\\", "*-", "_>", "x\r\n- y", "ünï - çode", "```\n~~~\n```"}
	for _, s := range fixed {
		if got, want := mdEscape(s), referenceMdEscape(s); got != want {
			t.Errorf("mdEscape(%q) = %q, want %q", s, got, want)
		}
	}
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", " ", "\n", "\r", "-", "+", "=", "#", "~", ">", "`", "*", "_", "[", "]", "\\", "1", "2", ".", "é", "\t"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(14); n > 0; n-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		s := b.String()
		if got, want := mdEscape(s), referenceMdEscape(s); got != want {
			t.Fatalf("mdEscape(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestMdBufJoinMatchesMdJoin(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	pieces := []string{"", " ", "  ", "\n", "\n\n", "\n\n\n", "a", "b ", " c", "\n\nx\n\n", "  \n", "x\n", "y \n ", "\n z", "tail  "}
	for i := 0; i < 20000; i++ {
		var buf mdBuf
		want := ""
		for n := rng.Intn(9); n > 0; n-- {
			p := pieces[rng.Intn(len(pieces))]
			if p == "" {
				continue
			}
			if buf.empty() != (want == "") || buf.endsWithSpaceOrNewline() != (want != "" && (strings.HasSuffix(want, "\n") || strings.HasSuffix(want, " "))) {
				t.Fatalf("buffer state %q disagrees with %q", buf.String(), want)
			}
			buf.join(p)
			want = referenceMdJoin(want, p)
			if buf.String() != want {
				t.Fatalf("after %q: got %q, want %q", p, buf.String(), want)
			}
		}
	}
}

// A page with thousands of sibling blocks must convert in time linear in its size: the join used to copy the whole
// accumulated output for every sibling.
func TestHTMLToMarkdownDoesNotCopyTheOutputPerSibling(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(benchPage(60)))
	small := testing.AllocsPerRun(1, func() { _ = htmlToMarkdown(doc) })
	smallBytes := allocBytes(func() { _ = htmlToMarkdown(doc) })
	doc2, _ := html.Parse(strings.NewReader(benchPage(240)))
	bigBytes := allocBytes(func() { _ = htmlToMarkdown(doc2) })
	_ = small
	// Four times the content: linear growth is about 4x, quadratic growth about 16x.
	if ratio := float64(bigBytes) / float64(smallBytes); ratio > 7 {
		t.Fatalf("converting 4x the blocks allocated %.1fx the bytes (%d -> %d): the output is being copied per sibling", ratio, smallBytes, bigBytes)
	}
}

func allocBytes(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A blockquote and a code block each used to compile a regexp for themselves.
func TestBlockquoteAndPreDoNotCompileRegexps(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(`<body><blockquote>a quote<br>second line</blockquote><pre><code class="language-go">x := 1</code></pre></body>`))
	var quote, pre *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if isElement(n, "blockquote") {
			quote = n
		}
		if isElement(n, "pre") {
			pre = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if got := mdElement(quote, false); !strings.Contains(got, "> a quote") {
		t.Fatalf("blockquote: %q", got)
	}
	if got := mdElement(pre, false); !strings.Contains(got, "```go") {
		t.Fatalf("pre: %q", got)
	}
	if raceEnabled {
		return // allocation counts are not meaningful under the race detector
	}
	if allocs := testing.AllocsPerRun(50, func() { _ = mdElement(quote, false) }); allocs > 20 {
		t.Errorf("a blockquote allocated %.0f times; its regexp must be compiled once", allocs)
	}
	if allocs := testing.AllocsPerRun(50, func() { _ = mdElement(pre, false) }); allocs > 8 {
		t.Errorf("a code block allocated %.0f times; its regexp must be compiled once", allocs)
	}
}
