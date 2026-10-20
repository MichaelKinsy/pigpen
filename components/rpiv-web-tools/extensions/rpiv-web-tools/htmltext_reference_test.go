package rpiv_web_tools

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

// The regular expressions the scans of htmltext.go replace, as the first port had them (and as the original has
// them in fetch-helpers.ts:75-82). The scans must answer what they answer on any page, so this compares the two on
// generated pages made of the fragments where the expressions differ most: unclosed blocks, nested tags, stray
// angle brackets, case, whitespace runs. Fragments avoid U+017F and U+212A, which Go's (?i) folds onto s and k and
// JavaScript's does not (the scans follow JavaScript; the differential fixture holds those cases).
var (
	refScriptBlockRe   = regexp.MustCompile(`(?is)<script.*?</script>`)
	refStyleBlockRe    = regexp.MustCompile(`(?is)<style.*?</style>`)
	refNoscriptBlockRe = regexp.MustCompile(`(?is)<noscript.*?</noscript>`)
	refBlockCloserRe   = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|br|blockquote|pre|section|article|header|footer|nav|details|summary)>`)
	refSelfClosingBrRe = regexp.MustCompile(`(?i)<br\s*/?>`)
	refAnyTagRe        = regexp.MustCompile(`<[^>]+>`)
	refTitleTagRe      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	refHRunRe          = regexp.MustCompile(`[ \t]+`)
	refBlankRunRe      = regexp.MustCompile(`\n{3,}`)
)

func refHTMLToText(html string) string {
	text := refScriptBlockRe.ReplaceAllString(html, "")
	text = refStyleBlockRe.ReplaceAllString(text, "")
	text = refNoscriptBlockRe.ReplaceAllString(text, "")
	text = refBlockCloserRe.ReplaceAllString(text, "\n")
	text = refSelfClosingBrRe.ReplaceAllString(text, "\n")
	text = refAnyTagRe.ReplaceAllString(text, " ")
	text = decodeHTMLEntities(text)
	text = refHRunRe.ReplaceAllString(text, " ")
	text = refBlankRunRe.ReplaceAllString(text, "\n\n")
	return jsTrim(text)
}

func refExtractTitle(html string) string {
	m := refTitleTagRe.FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	return jsTrim(refAnyTagRe.ReplaceAllString(m[1], ""))
}

var htmlFragments = []string{
	"<script>", "</script>", "<SCRIPT type=x>", "</Script>", "<scripts>", "<style>", "</style>", "<STYLE>", "<noscript>", "</noscript>",
	"<title>", "</title>", "<TITLE lang=en>", "<title", "<titles>x</titles>", "<p>", "</p>", "</P>", "</p >", "</pre>", "</preX>", "</h1>", "</h7>",
	"</div>", "</li>", "</br>", "<br>", "<BR/>", "<br />", "<br\t/>", "<br\n>", "<brx>", "<br/ >", "</blockquote>", "</summary>", "</nav>",
	"<a href=\"x\">", "</a>", "<>", "<<", "<<a>", "<", ">", "a<b", "<a <b>", "<!-- c -->", "<img src=y>",
	"&amp;", "&lt;b&gt;", "&#65;", "&#8364;", "&#39;", "&nbsp;", "&unknown;", "&#;",
	" ", "  ", "\t", " \t ", "\n", "\n\n", "\n\n\n", "\n\n\n\n\n", "\r\n",
	"text", "word", "caf\u00e9", "\u4e2d\u6587", "\U0001F600", "\xff", "x",
}

func randomHTML(r *rand.Rand) string {
	var b strings.Builder
	for range 1 + r.IntN(40) {
		b.WriteString(htmlFragments[r.IntN(len(htmlFragments))])
	}
	return b.String()
}

func TestHTMLScansMatchTheExpressions(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 20000 {
		in := randomHTML(r)
		if got, want := htmlToText(in), refHTMLToText(in); got != want {
			t.Fatalf("case %d: htmlToText(%q)\n got  %q\n want %q", i, in, got, want)
		}
		if got, want := extractTitle(in), refExtractTitle(in); got != want {
			t.Fatalf("case %d: extractTitle(%q) = %q, want %q", i, in, got, want)
		}
	}
}

func BenchmarkHTMLToText(b *testing.B) {
	page := "<html><head><title>Bench</title></head><body>" + strings.Repeat("<p>Hello &amp; welcome to <b>the</b> page</p>\n", 450) + "</body></html>"
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			htmlToText(page)
		}
	})
	b.Run("expressions", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			refHTMLToText(page)
		}
	})
}
