package tui

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// streamingCorpusDocuments joins the marked corpus cases into documents of a few cases each, so blocks of different
// kinds follow each other, and adds long paragraphs that wrap.
func streamingCorpusDocuments(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/markdown-marked-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	var documents []string
	for start := 0; start < len(corpus); {
		end := min(len(corpus), start+3+rng.IntN(6))
		parts := make([]string, 0, end-start)
		for _, c := range corpus[start:end] {
			parts = append(parts, c.Source)
		}
		documents = append(documents, strings.Join(parts, []string{"\n\n", "\n", ""}[rng.IntN(3)]))
		start = end
	}
	paragraph := "Some **bold** text with `code`, a [link](http://x.y), _emphasis_ and more words that fill the row. "
	documents = append(documents,
		strings.Repeat(paragraph, 6)+"\n\n"+strings.Repeat(paragraph, 4)+"\n\n- item "+paragraph+"\n- item two\n\n> quoted "+paragraph,
		"# Title\n\n"+strings.Repeat(paragraph+"\n\n", 5)+"```go\nfunc f() {}\n```\n\n| a | b |\n|---|---|\n| "+paragraph+" | x |\n",
		// marked's inline lexer state carries from one paragraph to the next: an open <a> suppresses URL autolinks, raw
		// HTML blocks keep their text, and a definition after a reference makes the document lex again.
		"before http://a.b <a href=\"u\">open http://c.d\n\nstill http://e.f inside www.g.h\n\nclose</a> after http://i.j\n\nwww.k.l done",
		"see [ref] and [x] then http://a.b\n\nplain text www.c.d\n\n[ref]: http://r.s\n\nmore [ref] and [x]\n\n[x]: http://x.y\n\nlast [x] www.e.f",
		"http://a.b and www.c.d\n\nx <a href=\"u\">open\n\nhttp://a.b and www.c.d\n\nclose</a>\n\nhttp://a.b and www.c.d",
		"<pre>raw http://a.b\n\nhttp://c.d</pre>\n\nafter http://e.f <a>x</a> http://g.h\n\n<a href=\"u\">http://i.j</a> www.k.l",
	)
	return documents
}

// A Markdown component that streams a document renders every prefix exactly as a new component given that prefix does,
// at a width that changes during the stream. The streamed component reuses what it computed for earlier prefixes; Pi's
// Markdown recomputes everything (markdown.ts render), so a fresh component that reuses nothing is the reference.
func TestStreamedMarkdownEqualsAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	rng := rand.New(rand.NewPCG(3, 4))
	// One component streams every document, so it also renders texts that do not extend the one it rendered before.
	streamed := testMDWithOptions("", 1, 0, nil, nil, nil)
	reference := opaqueMarkdownTheme()
	for index, document := range streamingCorpusDocuments(t) {
		width := 8 + rng.IntN(70)
		switchAt := rng.IntN(len(document) + 1)
		// Every byte of documents up to 150 bytes; random steps averaging about 150 prefixes in longer ones (adversarial
		// delimiter runs make one render of those slow).
		maxStep := max(1, 2*len(document)/150)
		for end := 0; end <= len(document); end += 1 + rng.IntN(maxStep) {
			if end >= switchAt && switchAt >= 0 {
				width = 8 + rng.IntN(70)
				switchAt = -1
			}
			prefix := document[:end]
			streamed.SetText(prefix)
			got := streamed.Render(width)
			want := testMDWithOptions(prefix, 1, 0, reference, nil, nil).Render(width)
			if !slices.Equal(got, want) {
				t.Fatalf("document %d, prefix of %d bytes at width %d: %q\n got %q\nwant %q", index, end, width, prefix, got, want)
			}
		}
	}
}

// A streamed render wraps only the overflowing lines that changed since the previous render.
func TestStreamedMarkdownWrapsOnlyChangedLines(t *testing.T) {
	mdCaseSetup(t)
	wraps := 0
	previous := wrapTextWithAnsi
	t.Cleanup(func() { wrapTextWithAnsi = previous })
	wrapTextWithAnsi = func(text string, width int) []string {
		wraps++
		return previous(text, width)
	}
	paragraph := strings.Repeat("A paragraph wider than the component wraps. ", 4)
	text := strings.Repeat(paragraph+"\n\n", 30) + paragraph
	streamed := testMDWithOptions(text, 0, 0, mdUpstreamTheme(), nil, nil)
	streamed.Render(60)
	if wraps != 31 {
		t.Fatalf("first render wrapped %d lines, want 31", wraps)
	}
	wraps = 0
	streamed.SetText(text + "more")
	streamed.Render(60)
	if wraps != 1 {
		t.Fatalf("streamed render wrapped %d lines, want 1 (the changed paragraph)", wraps)
	}
	wraps = 0
	streamed.Render(50)
	if wraps != 31 {
		t.Fatalf("render at a new width wrapped %d lines, want 31", wraps)
	}
	// A text that ends in a blank line renders a last space row after the lines it wraps; that row keeps their wraps too.
	for _, theme := range []*MarkdownTheme{mdUpstreamTheme(), nil} {
		streamed := testMDWithOptions(text+"\n\n", 0, 0, theme, nil, nil)
		streamed.Render(60)
		wraps = 0
		streamed.SetText(text + "\n\nmore")
		streamed.Render(60)
		if wraps != 0 {
			t.Fatalf("theme %v: the render after a trailing blank line wrapped %d lines, want 0", theme != nil, wraps)
		}
	}
}

// opaqueMarkdownTheme renders as GetMarkdownTheme does through a style function that is not one of tui's own, so a
// component with it reuses no inline rendering.
func opaqueMarkdownTheme() *MarkdownTheme {
	theme := GetMarkdownTheme()
	bold := theme.Bold
	theme.Bold = func(s string) string { return bold(s) }
	return &theme
}

// Streaming a list or a quote draws what a new Markdown given the same text draws, for owners with a default colour, an
// italic default and a text style, and with a reference link defined after its use.
func TestStreamedListAndQuoteEqualAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	docs := []string{
		"- one **a**\n- two `b` [ref]\n  - nested *c*\n- three\n\n[ref]: http://x.y\n",
		"1. one\n2. two **b**\n3. three\n\n   para in three\n4. four\n",
		"> quote **a**\n>\n> - item *b*\n> - item `c`\n\nafter\n",
		"- > quoted in item **a**\n- plain\n  > another [r]\n\n[r]: http://z\n",
		"- [ ] task **a**\n- [x] done\n",
		"- same **text** here\n\n> same **text** here\n\n> > same **text** here\n\nsame **text** here\n",
	}
	owners := map[string]func(*Markdown){
		"plain":   func(*Markdown) {},
		"colour":  func(m *Markdown) { m.SetDefaultColor("\x1b[38;5;99m") },
		"italics": func(m *Markdown) { m.defaultItalic = true },
	}
	for ownerName, configure := range owners {
		for d, doc := range docs {
			streamed := testMDWithOptions("", 0, 0, nil, nil, nil)
			configure(streamed)
			for end := 0; end <= len(doc); end++ {
				for end < len(doc) && !utf8.RuneStart(doc[end]) {
					end++
				}
				streamed.SetText(doc[:end])
				fresh := testMDWithOptions(doc[:end], 0, 0, nil, nil, nil)
				configure(fresh)
				got, want := streamed.Render(40), fresh.Render(40)
				if !slices.Equal(got, want) {
					t.Fatalf("%s doc %d at %d bytes:\n got %q\nwant %q", ownerName, d, end, got, want)
				}
			}
		}
	}
}

// A quote's inline text is styled by the theme's Quote, which the memo's context does not hold, so a changed Quote renders
// the unchanged quote text again.
func TestStreamedQuoteFollowsTheThemeQuote(t *testing.T) {
	mdCaseSetup(t)
	generation := "1"
	theme := GetMarkdownTheme()
	theme.Quote = func(s string) string { return "<" + generation + ">" + s }
	streamed := testMDWithOptions("", 0, 0, &theme, nil, nil)
	text := "> quoted **bold** text\n\nplain"
	for _, step := range []string{"1", "2", "3"} {
		generation = step
		text += " more"
		streamed.SetText(text)
		got := streamed.Render(60)
		want := testMDWithOptions(text, 0, 0, &theme, nil, nil).Render(60)
		if !slices.Equal(got, want) {
			t.Fatalf("generation %s: got %q, want %q", step, got, want)
		}
	}
}
