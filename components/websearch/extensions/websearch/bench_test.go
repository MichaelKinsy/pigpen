package websearch

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Extracting a fetched page: parse, pick the article, convert to Markdown. The fetch itself is the network.
//
//	go test -run xxx -bench . -benchmem
func benchPage(blocks int) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><title>Guide | Example</title></head><body><nav><a href="/">Home</a></nav><article><h1>Guide</h1>`)
	for i := 0; i < blocks; i++ {
		b.WriteString(`<p>` + strings.Repeat("This guide explains the <strong>important</strong> parts of the system in detail. ", 4) + `</p>`)
		b.WriteString(`<blockquote>A quoted line about the system.<br>And a second line.</blockquote>`)
		b.WriteString(`<pre><code class="language-go">go test ./... -run TestThing -count=1</code></pre>`)
		b.WriteString(`<ul><li>First step</li><li>Second <a href="https://example.com/x">linked</a> step</li></ul>`)
	}
	b.WriteString(`</article><footer>Copyright</footer></body></html>`)
	return b.String()
}

func BenchmarkReadableArticle(b *testing.B) {
	page := benchPage(40)
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		doc, err := html.Parse(strings.NewReader(page))
		if err != nil {
			b.Fatal(err)
		}
		if _, _, ok := readableArticle(doc); !ok {
			b.Fatal("no article")
		}
	}
}

// The conversion alone, on a parsed document.
func BenchmarkHTMLToMarkdown(b *testing.B) {
	doc, _ := html.Parse(strings.NewReader(benchPage(40)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = htmlToMarkdown(doc)
	}
}

// The SSRF guard runs on every fetch and on every redirect hop; DNS is stubbed so this is the guard itself.
func BenchmarkValidateRemoteURL(b *testing.B) {
	o := ValidationOptions{Lookup: func(context.Context, string) ([]LookupAddress, error) {
		return []LookupAddress{{Address: "93.184.216.34", Family: 4}, {Address: "2606:2800:220:1:248:1893:25c8:1946", Family: 6}}, nil
	}, AllowRanges: []string{"198.18.0.0/15", "10.1.0.0/16"}}
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ValidateRemoteURL(ctx, "https://example.com/docs/page?x=1", o); err != nil {
			b.Fatal(err)
		}
	}
}
