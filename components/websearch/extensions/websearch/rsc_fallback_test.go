package websearch

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twins of test/rsc-fallback. The RSC payload extractor is ported (rsc_extract.go, proven on the fixtures of
// testdata/html); the Defuddle fallback is not (defuddle.go), so the cases that need its content are named skips.

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
	tskip(t, F, "null Readability output falls back to local Defuddle extraction", "the Defuddle fallback is not ported (defuddle.go: no Go port tracks Defuddle 0.19.3 and its pipeline is several thousand lines); the page is reported with the original's verdict and the refusal (TestDefuddleFallbackIsRefused)")
	tskip(t, F, "Defuddle fallback resolves relative canonical URLs against the fetched URL", "the Defuddle fallback is not ported (defuddle.go); the case observes Defuddle's own console warnings")
	tskip(t, F, "short Readability output falls back to useful Defuddle content", "the Defuddle fallback is not ported (defuddle.go); the page is reported with the original's verdict and the refusal")
	tskip(t, F, "Defuddle processing failures stay contained and do not return the fallback body", "the Defuddle fallback is not ported (defuddle.go): the failure is css-select's own \"Unknown pseudo-class :a\" from Defuddle's hidden-content retry")
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

// Until the Defuddle fallback is ported, a page the original would hand to it is reported with the original's verdict
// followed by the refusal: never a silent skip.
func TestDefuddleFallbackIsRefused(t *testing.T) {
	extractEnv(t, "")
	useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Body: "<!doctype html><html><head><title>App</title></head><body><div id=\"root\"></div></body></html>", Header: hdr("content-type", "text/html")}
	})
	r := ExtractContent(bg(), "https://example.com/app", pageOpts())
	msg := errOf(r)
	if !strings.HasPrefix(msg, "Could not extract readable content from HTML structure") || !strings.Contains(msg, "the Defuddle fallback extractor") {
		t.Fatalf("%q", msg)
	}
	if strings.Contains(msg, "RSC payload") {
		t.Fatalf("the RSC extractor is ported: %q", msg)
	}
}
