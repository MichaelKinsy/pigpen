package websearch

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twins of test/rsc-fallback. The RSC payload extractor is ported (rsc_extract.go, proven on the fixtures of
// testdata/html); the Defuddle fallback is ported (defuddle.go and dfd_*.go, proven on testdata/defuddle).

func rscPage(title, body, payload string) string {
	script := ""
	if payload != "" {
		enc, _ := json.Marshal(payload)
		script = `<script>self.__next_f.push([1,` + string(enc) + `])</script>`
	}
	return `<!doctype html><html><head><title>` + title + `</title></head><body>` + body + script + `</body></html>`
}

func TestUpstream_rsc_fallback(t *testing.T) {
	const F = "rsc-fallback"
	fetch := func(t *testing.T, page string, header map[string]string) ExtractedContent {
		t.Helper()
		extractEnv(t, "")
		h := hdr("content-type", "text/html")
		for k, v := range header {
			h.Set(k, v)
		}
		useNet(t, func(netCall) netReply { return netReply{Status: 200, Body: page, Header: h} })
		return ExtractContent(bg(), "https://example.com/rsc", pageOpts())
	}
	flight := func(tree any) string {
		b, _ := json.Marshal(tree)
		return "23:" + string(b) + "\n"
	}

	tw(t, F, "weak Readability output falls back to useful RSC content", func(t *testing.T) {
		article := strings.Repeat("RSC article content survives the loading shell. ", 20)
		page := rscPage("RSC article", "<article>Loading...</article>", flight([]any{"$", "article", nil, map[string]any{"children": []any{"$", "p", nil, map[string]any{"children": article}}}}))
		r := fetch(t, page, map[string]string{"link": `</openapi.json>; rel="service-desc"`})
		if r.Error != nil || r.Title != "RSC article" || !strings.Contains(r.Content, "RSC article content survives the loading shell") || !strings.Contains(r.Content, "https://example.com/openapi.json") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "null Readability output falls back to local Defuddle extraction", func(t *testing.T) {
		article := strings.Repeat("Useful local Defuddle fallback content with enough detail. ", 20)
		page := "<!doctype html><html><head><title>Defuddle article</title></head><body><aside><main>" + article + "</main></aside></body></html>"
		calls := 0
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			calls++
			return netReply{Status: 200, Body: page, Header: hdr("content-type", "text/html", "link", `</openapi.json>; rel="service-desc"`)}
		})
		r := ExtractContent(bg(), "https://example.com/defuddle-null", pageOpts())
		if calls != 1 {
			t.Fatalf("Defuddle attempted a hidden network request: %d calls", calls)
		}
		if r.Error != nil || r.Title != "Defuddle article" || !strings.Contains(r.Content, "Useful local Defuddle fallback content") || !strings.Contains(r.Content, "https://example.com/openapi.json") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "Defuddle fallback resolves relative canonical URLs against the fetched URL", func(t *testing.T) {
		article := strings.Repeat("Defuddle content with a relative canonical URL stays quiet. ", 20)
		page := `<!doctype html><html><head><title>Relative canonical</title><link rel="canonical" href="/relative-canonical"></head><body><aside><main>` + article + `</main></aside></body></html>`
		r := fetch(t, page, nil)
		if r.Error != nil || !strings.Contains(r.Content, "Defuddle content with a relative canonical URL stays quiet.") {
			t.Fatalf("%+v", r)
		}
		// The original asserts that console.warn stays silent; the port has no console, and the page is also a recorded
		// fixture (testdata/defuddle/port--rsc-fallback-relative-canonical) whose Markdown is the original's.
	})
	tw(t, F, "short Readability output falls back to useful Defuddle content", func(t *testing.T) {
		article := strings.Repeat("Useful Defuddle content after the short Readability article. ", 20)
		r := fetch(t, `<!doctype html><html><head><title>Short article fallback</title></head><body><article>Loading...</article><aside><main>`+article+`</main></aside></body></html>`, nil)
		if r.Error != nil || r.Title != "Short article fallback" || !strings.Contains(r.Content, "Useful Defuddle content after the short Readability article") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "Defuddle processing failures stay contained and do not return the fallback body", func(t *testing.T) {
		paragraph := "This is substantial article text that should be extracted as the page's main content. "
		page := `<!doctype html><html><head><title>Defuddle selector failure</title></head><body><nav><a href="/">Home</a></nav><template id="B:0"></template><div hidden id="S:a"><h1>Defuddle selector failure</h1><p>` + strings.Repeat(paragraph, 20) + `</p></div><template id="P:a"></template><footer><p>Copyright 2026 Example Inc. All rights reserved.</p></footer></body></html>`
		r := fetch(t, page, nil)
		if r.Error == nil || !strings.Contains(*r.Error, "Defuddle failed to process document: Unknown pseudo-class :a") || strings.Contains(r.Content, "Copyright 2026") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "short non-RSC pages remain incomplete", func(t *testing.T) {
		r := fetch(t, "<!doctype html><html><head><title>Short</title></head><body><article>Loading...</article></body></html>", nil)
		if r.Error == nil {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "short RSC payloads remain incomplete", func(t *testing.T) {
		r := fetch(t, rscPage("Short RSC", "", flight([]any{"$", "article", nil, map[string]any{"children": "Short RSC content"}})), nil)
		if r.Error == nil {
			t.Fatalf("%+v", r)
		}
	})
	tskip(t, F, "background fetch notification distinguishes full, partial, and failed content", "the upstream case greps the TypeScript source of index.ts for its notification strings; the Go extension has no such source (the notification text is covered by the background-fetch twins)")
}
