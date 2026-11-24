package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchHTMLToMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><title>Ignored Head</title><style>.x{color:red}</style><script>alert(1)</script></head>
<body>
<nav><a href="/home">Nav link dropped</a></nav>
<main>
  <h1>Main Heading</h1>
  <h2>Sub Heading</h2>
  <p>Intro with <strong>bold</strong>, <em>italic</em>, <code>inline()</code>, and <a href="/docs/guide">Guide Link</a>.</p>
  <ul>
    <li>First item</li>
    <li>Second item
      <ol>
        <li>Nested one</li>
        <li>Nested two</li>
      </ol>
    </li>
  </ul>
  <pre><code class="language-go">func main() {
	println("hi")
}</code></pre>
  <table>
    <thead><tr><th>Name</th><th>Value</th></tr></thead>
    <tbody><tr><td>Alpha</td><td>1</td></tr><tr><td>Beta</td><td>2</td></tr></tbody>
  </table>
  <blockquote><p>Quoted line</p></blockquote>
  <form><input type="text" value="dropped"/></form>
</main>
</body>
</html>`))
	}))
	defer srv.Close()

	prev := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prev }()

	got, err := Fetch(context.Background(), srv.URL+"/articles/intro", false)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	checks := []string{
		"URL: " + srv.URL + "/articles/intro",
		"Content-Type: text/html",
		"Method: native",
		"---\ntitle: Ignored Head\n---\n\n# Main Heading",
		"## Sub Heading",
		"**bold**",
		"*italic*",
		"`inline()`",
		"[Guide Link](/docs/guide)",
		"- First item",
		"- Second item",
		"  1. Nested one",
		"  2. Nested two",
		"```go\nfunc main() {",
		"| Name | Value |",
		"| --- | --- |",
		"| Alpha | 1 |",
		"| Beta | 2 |",
		"> Quoted line",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
	for _, dropped := range []string{"alert(1)", ".x{color:red}", "Nav link dropped"} {
		if strings.Contains(got, dropped) {
			t.Errorf("expected %q to be dropped from output:\n%s", dropped, got)
		}
	}

	// Raw mode returns original HTML verbatim.
	rawOut, err := Fetch(context.Background(), srv.URL+"/articles/intro", true)
	if err != nil {
		t.Fatalf("Fetch raw failed: %v", err)
	}
	if !strings.Contains(rawOut, "Method: raw") || !strings.Contains(rawOut, "<h1>Main Heading</h1>") {
		t.Fatalf("expected raw HTML output, got:\n%s", rawOut)
	}
}

func TestFetchFixtureExactParity(t *testing.T) {
	// Exact-text comparison against omp's `read` on a local HTML fixture.
	// upstream: crates/pi-natives/src/html.rs (html-to-markdown-rs 3.12.4 Tier2),
	// packages/coding-agent/src/tools/fetch.ts
	const page = `<!doctype html><html><head><title>Fixture page</title></head><body>` +
		`<h1>Fixture page</h1>` +
		`<p>A <a href="/other.html">link</a> and <strong>bold</strong> text.</p>` +
		`<ul><li>one</li><li>two <code>inline</code></li></ul>` +
		`<pre><code class="language-go">func main() {}` + "\n" + `</code></pre>` +
		`<table><thead><tr><th>k</th><th>v</th></tr></thead><tbody><tr><td>a</td><td>1</td></tr></tbody></table>` +
		`</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	prev := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prev }()

	got, err := Fetch(context.Background(), srv.URL+"/fixture.html", false)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	want := "URL: " + srv.URL + "/fixture.html\n" +
		"Content-Type: text/html\n" +
		"Method: native\n" +
		"\n---\n\n" +
		"---\n" +
		"title: Fixture page\n" +
		"---\n\n" +
		"# Fixture page\n\n" +
		"A [link](/other.html) and **bold** text.\n\n" +
		"- one\n" +
		"- two `inline`\n\n" +
		"```go\n" +
		"func main() {}\n" +
		"```\n\n" +
		"| k | v |\n" +
		"| --- | --- |\n" +
		"| a | 1 |"

	if got != want {
		t.Fatalf("Fetch exact output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestHTMLToMarkdownFrontmatterAndHrefs(t *testing.T) {
	// upstream: html-to-markdown-rs 3.12.4 converter/main_helpers.rs:extract_head_metadata
	t.Run("emits sorted head metadata keys and keeps relative hrefs verbatim", func(t *testing.T) {
		doc := `<!doctype html><html><head>` +
			`<title>  Sample Title  </title>` +
			`<meta name="description" content="Page summary">` +
			`<meta property="og:type" content="article">` +
			`<link rel="canonical" href="https://example.com/canonical">` +
			`<base href="https://example.com/base/">` +
			`</head><body>` +
			`<p>See <a href="rel/page.html">relative</a>, <a href="/root">root</a>, <img src="/img.png" alt="pic"></p>` +
			`</body></html>`

		got := HTMLToMarkdown(doc, "http://127.0.0.1:18731/dir/index.html")
		want := "---\n" +
			"base: https://example.com/base/\n" +
			"canonical: https://example.com/canonical\n" +
			"meta-description: Page summary\n" +
			"meta-og:type: article\n" +
			"title: Sample Title\n" +
			"---\n\n" +
			"See [relative](rel/page.html), [root](/root), ![pic](/img.png)"
		if got != want {
			t.Fatalf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("omits frontmatter when head is absent or has no metadata keys", func(t *testing.T) {
		noHead := `<h1>Hello</h1><p>World</p>`
		if got := HTMLToMarkdown(noHead, "https://example.com"); got != "# Hello\n\nWorld" {
			t.Fatalf("no-head got:\n%s", got)
		}
		emptyHead := `<!doctype html><html><head><title>   </title><meta charset="utf-8"><link rel="stylesheet" href="/a.css"></head><body><h1>Hello</h1></body></html>`
		if got := HTMLToMarkdown(emptyHead, "https://example.com"); got != "# Hello" {
			t.Fatalf("empty-head got:\n%s", got)
		}
	})
}

func TestFetchDataURIImagesDropped(t *testing.T) {
	// upstream: test/tools/fetch-data-uri-images.test.ts: "drops base64 payloads from inline <svg> and data: <img> while keeping text and alt"
	const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	htmlDoc := `<!doctype html><html><body><article>
<a href="/blog/"><svg viewBox="0 0 32 32"><path d="M0 0h32v32H0z"/></svg>All posts</a>
<h1>Launch post</h1>
<img src="data:image/png;base64,` + pngB64 + `" alt="Benchmark chart">
<img src="DATA:image/png;base64,` + pngB64 + `" alt="Upper chart" title="caption">
<p>Real article text that must remain intact.</p>
</article></body></html>`

	md := HTMLToMarkdown(htmlDoc, "https://example.com/post")
	if strings.Contains(strings.ToLower(md), "data:") || strings.Contains(md, pngB64) {
		t.Fatalf("expected data: URI dropped, got:\n%s", md)
	}
	if !strings.Contains(md, "![Benchmark chart]") || !strings.Contains(md, "![Upper chart]") || !strings.Contains(md, "All posts") {
		t.Fatalf("expected alt text and link text preserved, got:\n%s", md)
	}
}

func TestFetchPlainTextAndJSONAndRawMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("Hello plain world\n\n\n\nLine two"))
		case "/data.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"alpha":1,"beta":[2,3]}`))
		}
	}))
	defer srv.Close()

	prev := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prev }()

	plain, err := Fetch(context.Background(), srv.URL+"/plain", false)
	if err != nil {
		t.Fatalf("plain fetch failed: %v", err)
	}
	if !strings.Contains(plain, "Method: text") || !strings.Contains(plain, "Hello plain world\n\nLine two") {
		t.Fatalf("unexpected plain output:\n%s", plain)
	}

	// upstream: test/tools/fetch-raw-mode.test.ts: "still pretty-prints JSON when :raw is absent (existing behavior)"
	prettyJSON, err := Fetch(context.Background(), srv.URL+"/data.json", false)
	if err != nil {
		t.Fatalf("json fetch failed: %v", err)
	}
	if !strings.Contains(prettyJSON, "Method: json") || !strings.Contains(prettyJSON, "\"alpha\": 1") {
		t.Fatalf("expected pretty-printed JSON, got:\n%s", prettyJSON)
	}

	// upstream: test/tools/fetch-raw-mode.test.ts: "returns the raw JSON body when :raw is set"
	rawJSON, err := Fetch(context.Background(), srv.URL+"/data.json", true)
	if err != nil {
		t.Fatalf("raw json fetch failed: %v", err)
	}
	if !strings.Contains(rawJSON, "Method: raw") || !strings.Contains(rawJSON, `{"alpha":1,"beta":[2,3]}`) {
		t.Fatalf("expected raw JSON, got:\n%s", rawJSON)
	}
}

func TestFetchRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Landed at new location"))
	}))
	defer srv.Close()

	prev := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prev }()

	got, err := Fetch(context.Background(), srv.URL+"/old", false)
	if err != nil {
		t.Fatalf("redirect fetch failed: %v", err)
	}
	if !strings.Contains(got, "URL: "+srv.URL+"/new\n") || !strings.Contains(got, "Landed at new location") {
		t.Fatalf("expected final URL after redirect, got:\n%s", got)
	}
}

func TestFetchOversizedBody(t *testing.T) {
	big := strings.Repeat("a", 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	prevClient := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prevClient }()

	prevMax := fetchMaxBytes
	fetchMaxBytes = 512
	defer func() { fetchMaxBytes = prevMax }()

	got, err := Fetch(context.Background(), srv.URL+"/big", false)
	if err != nil {
		t.Fatalf("oversized fetch failed: %v", err)
	}
	if !strings.Contains(got, "Response body exceeded 512 B and was cut mid-stream; content is incomplete") {
		t.Fatalf("expected truncation note, got:\n%s", got)
	}
	sep := "\n---\n\n"
	idx := strings.Index(got, sep)
	if idx < 0 {
		t.Fatalf("missing separator in output")
	}
	body := got[idx+len(sep):]
	if len(body) != 512 {
		t.Fatalf("body length = %d, want 512", len(body))
	}
}

func TestFetchNonTextContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4\x00binarydata"))
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
		case "/sniff-binary":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("abc\x00def"))
		}
	}))
	defer srv.Close()

	prev := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prev }()

	for _, tc := range []struct {
		path string
		mime string
	}{
		{"/file.pdf", "application/pdf"},
		{"/image.png", "image/png"},
		{"/sniff-binary", "text/plain"},
	} {
		got, err := Fetch(context.Background(), srv.URL+tc.path, false)
		if err != nil {
			t.Fatalf("Fetch(%s) error: %v", tc.path, err)
		}
		if !strings.Contains(got, "Method: binary") || !strings.Contains(got, "[Binary content: "+tc.mime) {
			t.Fatalf("Fetch(%s) expected binary notice, got:\n%s", tc.path, got)
		}
	}
}

func TestFetchHTTPStatusAndFeedsAndAlternates(t *testing.T) {
	const filler = "Page content that a reader must not silently replace with a site-wide alternate. Page content that a reader must not silently replace with a site-wide alternate."
	const rss = `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Fluxer blog</title><item><title>Mobile clients &amp; v2</title><link>https://example.test/rss-item</link><description><![CDATA[<p>Open source <b>mobile</b> apps.</p>]]></description><pubDate>Mon, 15 Jun 2026 12:00:00 +0000</pubDate></item></channel></rss>`
	const atom = `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Atom title</title><entry><title>First entry</title><link href="https://example.test/atom-entry"/><updated>2026-06-15T12:00:00Z</updated><summary>Entry summary</summary></entry><entry><title>Second entry</title><content>Entry content</content></entry></feed>`

	var attempts429 int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/204":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNoContent)
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/429":
			attempts429++
			if attempts429 == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Recovered after 429"))
		case "/rss":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(rss))
		case "/atom":
			w.Header().Set("Content-Type", "application/atom+xml")
			_, _ = w.Write([]byte(atom))
		case "/guide/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><html><head><link rel="alternate" type="text/markdown" href="/guide/index.txt"></head><body><h1>HTML</h1></body></html>`))
		case "/guide/index.txt":
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("# GUIDE_MARKDOWN\n\n" + filler))
		case "/pricing/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><html><head><link rel="alternate" type="text/markdown" href="/llms.txt"></head><body><h1>PRICING_BODY</h1><p>` + filler + `</p></body></html>`))
		case "/llms.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("# SITE_WIDE\n\n" + filler))
		}
	}))
	defer srv.Close()

	prevClient := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = prevClient }()

	prevSleep := fetchRetrySleep
	fetchRetrySleep = func(context.Context, time.Duration) error { return nil }
	defer func() { fetchRetrySleep = prevSleep }()

	// upstream: test/tools/fetch-http-status.test.ts: "reports a bodyless 204 as a successful empty read, not a failed fetch"
	got204, err := Fetch(context.Background(), srv.URL+"/204", false)
	if err != nil || !strings.Contains(got204, "HTTP 204 No Content") || !strings.Contains(got204, "Method: no-content") {
		t.Fatalf("204 mismatch: out=%q err=%v", got204, err)
	}

	// upstream: test/tools/fetch-http-status.test.ts: "reports a bodyless 503 as a failed fetch"
	if _, err := Fetch(context.Background(), srv.URL+"/503", false); err == nil || !strings.Contains(err.Error(), "Failed to fetch URL (HTTP 503)") {
		t.Fatalf("503 mismatch: err=%v", err)
	}

	// 429 retry
	got429, err := Fetch(context.Background(), srv.URL+"/429", false)
	if err != nil || !strings.Contains(got429, "Recovered after 429") || attempts429 != 2 {
		t.Fatalf("429 retry mismatch: attempts=%d out=%q err=%v", attempts429, got429, err)
	}

	// upstream: test/tools/fetch-feed.test.ts: "renders RSS channel title, item title, date, description text, and <link> body"
	gotRSS, err := Fetch(context.Background(), srv.URL+"/rss", false)
	if err != nil || !strings.Contains(gotRSS, "# Fluxer blog\n") || !strings.Contains(gotRSS, "## Mobile clients & v2\n*Mon, 15 Jun 2026 12:00:00 +0000*\n\nOpen source mobile apps.\n") || !strings.Contains(gotRSS, "[Read more](https://example.test/rss-item)") {
		t.Fatalf("RSS mismatch: err=%v out:\n%s", err, gotRSS)
	}

	// upstream: test/tools/fetch-feed.test.ts: "renders Atom feed title, entry dates, and summary with content fallback"
	gotAtom, err := Fetch(context.Background(), srv.URL+"/atom", false)
	if err != nil || !strings.Contains(gotAtom, "# Atom title\n") || !strings.Contains(gotAtom, "## First entry\n*2026-06-15T12:00:00Z*\n\nEntry summary\n\n[Read more](https://example.test/atom-entry)") || !strings.Contains(gotAtom, "## Second entry\nEntry content\n") {
		t.Fatalf("Atom mismatch: err=%v out:\n%s", err, gotAtom)
	}

	// upstream: test/tools/fetch-alternates.test.ts: "uses a page-specific markdown alternate regardless of filename extension"
	gotGuide, err := Fetch(context.Background(), srv.URL+"/guide/", false)
	if err != nil || !strings.Contains(gotGuide, "Method: alternate-markdown") || !strings.Contains(gotGuide, "GUIDE_MARKDOWN") {
		t.Fatalf("guide alternate mismatch: err=%v out:\n%s", err, gotGuide)
	}

	// upstream: test/tools/fetch-alternates.test.ts: "keeps a pricing page instead of substituting site-wide llms.txt"
	gotPricing, err := Fetch(context.Background(), srv.URL+"/pricing/", false)
	if err != nil || !strings.Contains(gotPricing, "Method: native") || !strings.Contains(gotPricing, "PRICING_BODY") || strings.Contains(gotPricing, "SITE_WIDE") {
		t.Fatalf("pricing alternate mismatch: err=%v out:\n%s", err, gotPricing)
	}
}
