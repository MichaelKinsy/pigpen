package websearch

import (
	"context"
	"encoding/base64"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// Twins of the extraction tests: fetch-routing, fetch-timeout, fetch-not-found-guidance,
// fetch-content-domain-policy, declared-web-links, fetch-modes and extract-cold-deadline.

func pubLookup(context.Context, string) ([]LookupAddress, error) {
	return []LookupAddress{{Address: "93.184.216.34", Family: 4}}, nil
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func errOf(r ExtractedContent) string {
	if r.Error == nil {
		return ""
	}
	return *r.Error
}

func pageOpts() ExtractOptions { return ExtractOptions{Lookup: pubLookup} }

func matchRE(t *testing.T, re, s string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Fatalf("%q does not match /%s/", s, re)
	}
}

func noMatchRE(t *testing.T, re, s string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(s) {
		t.Fatalf("%q unexpectedly matches /%s/", s, re)
	}
}

func extractEnv(t *testing.T, config string) {
	t.Helper()
	_, agentDir := isolate(t)
	if config != "" {
		writeConfig(t, agentDir, config)
	}
}

const jinaBody = "Markdown Content:\n# Routed\n\n"

var jinaFiller = strings.Repeat("Jina routed content. ", 12)

func blockedThenJina(t *testing.T, jinaFails bool) *fakeNet {
	return useNet(t, func(c netCall) netReply {
		if strings.HasPrefix(c.URL, "https://r.jina.ai/") && !jinaFails {
			return reply(200, jinaBody+jinaFiller)
		}
		return reply(403, "blocked")
	})
}

func wantCalls(t *testing.T, f *fakeNet, want ...string) {
	t.Helper()
	got := f.urls()
	if !eqStrings(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestUpstream_fetch_routing(t *testing.T) {
	const F = "fetch-routing"
	const page = "https://example.com/routed"
	const jina = "https://r.jina.ai/https://example.com/routed"

	tw(t, F, "fetchRouting.providers can put Jina first after explicit remote-hosted opt-in", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"providers":["jina","http"],"allowRemoteHostedProviders":true}}`)
		n := blockedThenJina(t, false)
		r := ExtractContent(bg(), page, pageOpts())
		wantCalls(t, n, page, jina)
		if r.Error != nil || r.Title != "Routed" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "remote hosted fetch providers are disabled by default", func(t *testing.T) {
		extractEnv(t, `{}`)
		n := blockedThenJina(t, false)
		r := ExtractContent(bg(), page, pageOpts())
		wantCalls(t, n, page)
		matchRE(t, `HTTP 403`, errOf(r))
	})
	tw(t, F, "fetchRouting without providers uses the default order when remote hosted providers are allowed", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true}}`)
		n := blockedThenJina(t, false)
		r := ExtractContent(bg(), page, pageOpts())
		wantCalls(t, n, page, jina)
		if r.Error != nil || r.Title != "Routed" {
			t.Fatalf("%+v", r)
		}
	})

	const hintText = "Enable the keyless Jina Reader fallback"
	const privacy = "target URLs are fetched through Jina's infrastructure"
	hintLine := func(t *testing.T, msg string) string {
		for _, l := range strings.Split(msg, "\n") {
			if strings.Contains(l, hintText) {
				return l
			}
		}
		t.Fatalf("no jina hint in %q", msg)
		return ""
	}
	tw(t, F, "blocked-page guidance enables Jina with only the remote-hosted opt-in when Jina is already routed", func(t *testing.T) {
		extractEnv(t, `{}`)
		blockedThenJina(t, false)
		hint := hintLine(t, errOf(ExtractContent(bg(), page, pageOpts())))
		matchRE(t, `set fetchRouting\.allowRemoteHostedProviders to true in .*web-search\.json`, hint)
		matchRE(t, `Jina is already in your fetch provider order`, hint)
		matchRE(t, `also allows the other hosted providers`, hint)
		if !strings.Contains(hint, privacy) {
			t.Fatal(hint)
		}
		noMatchRE(t, `"providers"|add "jina"`, hint)
	})
	tw(t, F, "blocked-page guidance asks to add Jina to a custom provider list without replacing it", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"providers":["http","firecrawl"]}}`)
		blockedThenJina(t, false)
		gated := hintLine(t, errOf(ExtractContent(bg(), page, pageOpts())))
		matchRE(t, `add "jina" to your existing fetchRouting\.providers and set fetchRouting\.allowRemoteHostedProviders to true`, gated)
		if !strings.Contains(gated, privacy) {
			t.Fatal(gated)
		}
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"fetchRouting":{"providers":["http","firecrawl"],"allowRemoteHostedProviders":true}}`)
		blockedThenJina(t, false)
		allowed := hintLine(t, errOf(ExtractContent(bg(), page, pageOpts())))
		matchRE(t, `add "jina" to your existing fetchRouting\.providers in `, allowed)
		noMatchRE(t, `allowRemoteHostedProviders`, allowed)
		if !strings.Contains(allowed, privacy) {
			t.Fatal(allowed)
		}
	})
	tw(t, F, "blocked-page guidance does not suggest enabling Jina after Jina was attempted", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"providers":["http","jina"],"allowRemoteHostedProviders":true}}`)
		n := blockedThenJina(t, true)
		r := errOf(ExtractContent(bg(), page, pageOpts()))
		if !sliceHas(n.urls(), jina) {
			t.Fatalf("jina not attempted: %q", n.urls())
		}
		matchRE(t, `HTTP 403`, r)
		matchRE(t, `Fallback options:`, r)
		if strings.Contains(r, hintText) {
			t.Fatal(r)
		}
	})

	typed := func(t *testing.T, config, contentType string) (*fakeNet, ExtractedContent) {
		extractEnv(t, config)
		n := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/typed":
				return netReply{Status: 200, Body: "typed", Header: hdr("content-type", contentType)}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(200, "Markdown Content:\n# Bypassed\n\n"+strings.Repeat("content ", 80))
			}
			return netReply{Err: http.ErrNotSupported}
		})
		return n, ExtractContent(bg(), "https://example.com/typed", pageOpts())
	}
	tw(t, F, "disabled image fetching does not fall through to hosted providers", func(t *testing.T) {
		n, r := typed(t, `{"image":{"enabled":false},"fetchRouting":{"providers":["http","jina"],"allowRemoteHostedProviders":true}}`, "image/png")
		wantCalls(t, n, "https://example.com/typed")
		matchRE(t, `Image fetching is disabled by image\.enabled`, errOf(r))
	})
	tw(t, F, "disabled PDF extraction does not fall through to hosted providers", func(t *testing.T) {
		n, r := typed(t, `{"pdf":{"enabled":false},"fetchRouting":{"providers":["http","jina"],"allowRemoteHostedProviders":true}}`, "application/pdf")
		wantCalls(t, n, "https://example.com/typed")
		matchRE(t, `PDF extraction is disabled by pdf\.enabled`, errOf(r))
	})
	tw(t, F, "malformed config returns a parse error without hosted fallback", func(t *testing.T) {
		n, r := typed(t, `{`, "image/png")
		wantCalls(t, n)
		matchRE(t, `Failed to parse .*web-search\.json`, errOf(r))
	})
	tw(t, F, "image attachment gate suppresses malformed config", func(t *testing.T) {
		extractEnv(t, `{`)
		if CanAttachImages() {
			t.Fatal("canAttach")
		}
		_, err := IsImageEnabled()
		wantErr(t, err, `Failed to parse .*web-search\.json`)
	})
	tw(t, F, "Ollama Web Fetch is disabled for remote URLs without hosted-provider opt-in", func(t *testing.T) {
		extractEnv(t, `{"ollamaApiKey":"test-key","fetchRouting":{"providers":["ollama","http"]}}`)
		n := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case page:
				return reply(403, "blocked")
			case "https://ollama.com/api/web_fetch":
				return reply(200, `{"title":"Ollama","content":"remote content"}`)
			}
			return netReply{Err: http.ErrNotSupported}
		})
		r := ExtractContent(bg(), page, pageOpts())
		wantCalls(t, n, page)
		matchRE(t, `HTTP 403`, errOf(r))
	})
	tw(t, F, "hosted providers cannot bypass redirect policy validation", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"providers":["jina"],"allowRemoteHostedProviders":true}}`)
		n := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/redirect":
				return redirectTo(302, "http://127.0.0.1/admin")
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(200, "Markdown Content:\n# Bypassed\n\n"+strings.Repeat("content ", 80))
			}
			return netReply{Err: http.ErrNotSupported}
		})
		r := ExtractContent(bg(), "https://example.com/redirect", pageOpts())
		wantCalls(t, n, "https://example.com/redirect")
		matchRE(t, `Blocked internal address`, errOf(r))
	})

	filler := "<p>" + strings.Repeat("Example.com needs to review the security of your connection before proceeding with this readable article text. ", 8) + "</p>"
	generic := `<!DOCTYPE html><html><head><title>Just a moment...</title></head><body><article><h1>Just a moment...</h1>` + filler + filler + `</article></body></html>`
	cloudflare := `<!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title></head><body><article><h1>Verifying you are human</h1>` + filler + filler + `</article><script>(function(){window._cf_chl_opt={cvId:'3',cType:'managed'};var a=document.createElement('script');a.src='/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1?ray=abc';document.head.appendChild(a);}());</script></body></html>`
	challenge := func(t *testing.T, config, body string, h http.Header, mode string) (*fakeNet, ExtractedContent) {
		extractEnv(t, config)
		if h.Get("Content-Type") == "" {
			h.Set("Content-Type", "text/html; charset=utf-8")
		}
		n := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/challenge":
				return netReply{Status: 200, Body: body, Header: h}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(200, jinaBody+jinaFiller)
			}
			return netReply{Err: http.ErrNotSupported}
		})
		o := pageOpts()
		o.Mode = mode
		return n, ExtractContent(bg(), "https://example.com/challenge", o)
	}
	fallback := `{"fetchRouting":{"providers":["http","jina"],"allowRemoteHostedProviders":true}}`
	const chURL, chJina = "https://example.com/challenge", "https://r.jina.ai/https://example.com/challenge"

	tw(t, F, "HTTP 200 with cf-mitigated: challenge falls back to configured providers", func(t *testing.T) {
		n, r := challenge(t, fallback, generic, hdr("cf-mitigated", "challenge"), "")
		wantCalls(t, n, chURL, chJina)
		if r.Error != nil || r.Title != "Routed" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "HTTP 200 cf-mitigated: challenge falls back even when the response is not labeled HTML", func(t *testing.T) {
		n, r := challenge(t, fallback, "Just a moment...", hdr("content-type", "text/plain", "cf-mitigated", "challenge"), "")
		wantCalls(t, n, chURL, chJina)
		if r.Error != nil || r.Title != "Routed" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "Cloudflare body markers in a non-HTML response are returned as content", func(t *testing.T) {
		n, r := challenge(t, fallback, cloudflare, hdr("content-type", "text/plain"), "")
		wantCalls(t, n, chURL)
		if r.Error != nil || r.Content != cloudflare {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "HTTP 200 Cloudflare challenge body signature falls back to configured providers", func(t *testing.T) {
		n, r := challenge(t, fallback, cloudflare, hdr(), "")
		wantCalls(t, n, chURL, chJina)
		if r.Error != nil || r.Title != "Routed" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "generic Just a moment text alone is not treated as a challenge", func(t *testing.T) {
		n, r := challenge(t, fallback, generic, hdr(), "")
		wantCalls(t, n, chURL)
		if r.Error != nil {
			t.Fatalf("%+v", r)
		}
		matchRE(t, `readable article text`, r.Content)
	})
	tw(t, F, "HTTP-only routing reports a Cloudflare challenge as an HTTP extraction failure", func(t *testing.T) {
		n, r := challenge(t, `{}`, cloudflare, hdr("cf-mitigated", "challenge"), "")
		wantCalls(t, n, chURL)
		matchRE(t, `^HTTP 200: Blocked by Cloudflare challenge page`, errOf(r))
		if r.Content != "" {
			t.Fatal(r.Content)
		}
	})
	tw(t, F, "raw mode returns Cloudflare challenge bodies verbatim without fallback", func(t *testing.T) {
		n, r := challenge(t, fallback, cloudflare, hdr("cf-mitigated", "challenge"), "raw")
		wantCalls(t, n, chURL)
		if r.Error != nil || r.Content != cloudflare {
			t.Fatalf("%+v", r)
		}
	})
}

func TestUpstream_fetch_timeout(t *testing.T) {
	const F = "fetch-timeout"
	resolve := func(t *testing.T, config string, explicit *int64) (int64, error) {
		extractEnv(t, config)
		return ResolveFetchTimeoutMs(explicit)
	}
	tw(t, F, "fetch.timeout defaults to the direct HTTP/Jina 30 second budget", func(t *testing.T) {
		for _, c := range []string{"", `{}`} {
			ms, err := resolve(t, c, nil)
			noErr(t, err)
			if ms != 30000 {
				t.Fatal(ms)
			}
		}
	})
	tw(t, F, "fetch.timeout accepts seconds and rounds fractional milliseconds up", func(t *testing.T) {
		for cfg, want := range map[string]int64{`{"fetch":{"timeout":2}}`: 2000, `{"fetch":{"timeout":1.2345}}`: 1235} {
			ms, err := resolve(t, cfg, nil)
			noErr(t, err)
			if ms != want {
				t.Fatalf("%s => %d", cfg, ms)
			}
		}
	})
	tw(t, F, "invalid fetch.timeout values fail closed with the config path", func(t *testing.T) {
		for _, v := range []string{"0", "-1", "null", `"2"`, "{}"} {
			_, err := resolve(t, `{"fetch":{"timeout":`+v+`}}`, nil)
			wantErr(t, err, `Invalid fetch\.timeout .*web-search\.json`)
		}
	})
	tw(t, F, "fetch.timeout rejects seconds that overflow safe millisecond conversion", func(t *testing.T) {
		ms, err := resolve(t, `{"fetch":{"timeout":2147483.647}}`, nil)
		noErr(t, err)
		if ms != 2147483647 {
			t.Fatal(ms)
		}
		for _, v := range []string{"2147483.648", "1e308", "9007199254740991"} {
			_, err := resolve(t, `{"fetch":{"timeout":`+v+`}}`, nil)
			wantErr(t, err, `Invalid fetch\.timeout .*web-search\.json`)
			wantErr(t, err, `finite safe integer`)
			wantErr(t, err, `2147483647`)
		}
	})
	tw(t, F, "malformed web-search.json fails closed with the config path", func(t *testing.T) {
		_, err := resolve(t, `{`, nil)
		wantErr(t, err, `Failed to parse .*web-search\.json`)
	})

	type seen struct{ http, jina []int64 }
	runJina := func(t *testing.T, timeout string, explicit *int64) (*fakeNet, *seen, ExtractedContent) {
		extractEnv(t, `{"fetch":{"timeout":`+timeout+`},"fetchRouting":{"providers":["jina"],"allowRemoteHostedProviders":true}}`)
		n := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/routed":
				return reply(403, "blocked")
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(200, jinaBody+jinaFiller)
			}
			return netReply{Err: http.ErrNotSupported}
		})
		s := &seen{}
		observeTimeout = func(kind string, ms int64) {
			if kind == "http" {
				s.http = append(s.http, ms)
			} else {
				s.jina = append(s.jina, ms)
			}
		}
		t.Cleanup(func() { observeTimeout = nil })
		o := pageOpts()
		o.TimeoutMs = explicit
		return n, s, ExtractContent(bg(), "https://example.com/routed", o)
	}
	tw(t, F, "Jina receives the resolved configured timeout budget", func(t *testing.T) {
		n, s, r := runJina(t, "1.25", nil)
		wantCalls(t, n, "https://example.com/routed", "https://r.jina.ai/https://example.com/routed")
		if !reflect.DeepEqual(s.jina, []int64{1250}) || !containsInt(s.http, 1250) || r.Error != nil {
			t.Fatalf("%+v %+v", s, r)
		}
	})
	tw(t, F, "explicit timeoutMs takes precedence over invalid fetch.timeout config", func(t *testing.T) {
		seven := int64(7)
		_, s, r := runJina(t, "0", &seven)
		if !reflect.DeepEqual(s.jina, []int64{7}) || r.Error != nil {
			t.Fatalf("%+v %+v", s, r)
		}
	})
	tw(t, F, "Jina does not swallow invalid timeout configuration", func(t *testing.T) {
		n, _, r := runJina(t, "0", nil)
		wantCalls(t, n)
		matchRE(t, `Invalid fetch\.timeout .*web-search\.json`, errOf(r))
	})
	tw(t, F, "positive sub-millisecond fetch.timeout values use a nonzero budget", func(t *testing.T) {
		_, s, r := runJina(t, "0.0005", nil)
		if !reflect.DeepEqual(s.jina, []int64{1}) || r.Error != nil {
			t.Fatalf("%+v %+v", s, r)
		}
	})
}

func containsInt(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestUpstream_fetch_not_found_guidance(t *testing.T) {
	const F = "fetch-not-found-guidance"
	stub := func(t *testing.T, status int) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply { return reply(status, "gone") })
	}
	names := RegisteredToolNames{WebSearch: "web_search", FetchContent: "fetch_content"}
	run := func(url string, n RegisteredToolNames) ExtractedContent {
		o := pageOpts()
		o.ToolNames = n
		return ExtractContent(bg(), url, o)
	}
	tw(t, F, "404 drops the provider checklist and points at the registered tools", func(t *testing.T) {
		stub(t, 404)
		r := run("https://example.com/missing-page", names)
		if r.Status == nil || *r.Status != 404 {
			t.Fatalf("%+v", r)
		}
		matchRE(t, `HTTP 404: Not Found`, errOf(r))
		noMatchRE(t, `Fallback options:`, errOf(r))
		matchRE(t, `web_search`, errOf(r))
		matchRE(t, `fetch_content`, errOf(r))
	})
	tw(t, F, "410 gets the same not-found guidance", func(t *testing.T) {
		stub(t, 410)
		r := run("https://example.com/retired", names)
		if r.Status == nil || *r.Status != 410 {
			t.Fatalf("%+v", r)
		}
		noMatchRE(t, `Fallback options:`, errOf(r))
		matchRE(t, `web_search`, errOf(r))
	})
	tw(t, F, "guidance uses the registered public tool names", func(t *testing.T) {
		stub(t, 404)
		r := run("https://example.com/missing-page", RegisteredToolNames{WebSearch: "webfinder", FetchContent: "pageget"})
		matchRE(t, `webfinder`, errOf(r))
		matchRE(t, `pageget`, errOf(r))
		noMatchRE(t, `web_search`, errOf(r))
	})
	tw(t, F, "guidance stays generic when the caller does not know the tool names", func(t *testing.T) {
		stub(t, 404)
		r := run("https://example.com/missing-page", RegisteredToolNames{})
		noMatchRE(t, `Fallback options:`, errOf(r))
		noMatchRE(t, `web_search`, errOf(r))
		matchRE(t, `Find the current URL, then retry the fetch`, errOf(r))
	})
	tw(t, F, "guidance omits the fetch tool when only search is registered", func(t *testing.T) {
		stub(t, 404)
		r := run("https://example.com/missing-page", RegisteredToolNames{WebSearch: "web_search"})
		matchRE(t, `web_search`, errOf(r))
		noMatchRE(t, `fetch_content`, errOf(r))
	})
	tw(t, F, "transient errors keep the fallback checklist", func(t *testing.T) {
		stub(t, 500)
		r := run("https://example.com/oops", names)
		if r.Status == nil || *r.Status != 500 {
			t.Fatalf("%+v", r)
		}
		matchRE(t, `Fallback options:`, errOf(r))
		matchRE(t, `Use web_search to find content about this topic`, errOf(r))
	})
	tw(t, F, "the checklist search bullet follows the registered search tool name", func(t *testing.T) {
		stub(t, 500)
		r := run("https://example.com/oops", RegisteredToolNames{WebSearch: "webfinder"})
		matchRE(t, `Use webfinder to find content about this topic`, errOf(r))
		noMatchRE(t, `web_search`, errOf(r))
	})
}

func TestUpstream_fetch_content_domain_policy(t *testing.T) {
	const F = "fetch-content-domain-policy"
	article := `<!doctype html><html><head><title>Allowed</title></head><body><article><h1>Allowed</h1><p>` + strings.Repeat("Readable content. ", 60) + `</p></article></body></html>`
	run := func(t *testing.T, config string, urls []string, opts []ExtractOptions) (*fakeNet, []ExtractedContent) {
		extractEnv(t, config)
		n := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: article, Header: hdr("content-type", "text/html")}
		})
		var out []ExtractedContent
		for i, u := range urls {
			o := pageOpts()
			if i < len(opts) {
				o = opts[i]
				o.Lookup = pubLookup
			}
			out = append(out, ExtractContent(bg(), u, o))
		}
		return n, out
	}
	tw(t, F, "fetch_content enforces domain policy before target network requests", func(t *testing.T) {
		n, r := run(t, `{"fetchContent":{"domainPolicy":{"allow":["allowed.example"],"deny":["blocked.example"]}}}`,
			[]string{"https://blocked.example/article", "https://allowed.example/article"}, nil)
		wantCalls(t, n, "https://allowed.example/article")
		matchRE(t, `Blocked hostname by fetch_content domain policy`, errOf(r[0]))
		if r[1].Error != nil || r[1].Title != "Allowed" {
			t.Fatalf("%+v", r[1])
		}
	})
	tw(t, F, "fetch_content domain policy leaves local file inputs outside hostname checks", func(t *testing.T) {
		n, r := run(t, `{"fetchContent":{"domainPolicy":{"allow":["only.example"]}}}`, []string{"file:///tmp/not-a-web-source.txt"}, nil)
		wantCalls(t, n)
		noMatchRE(t, `fetch_content domain policy`, errOf(r[0]))
	})
	tw(t, F, "fetch_content domain policy blocks YouTube frame and timestamp branches before network helpers", func(t *testing.T) {
		yt := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
		n, r := run(t, `{"fetchContent":{"domainPolicy":{"deny":["youtube.com"]}}}`, []string{yt, yt},
			[]ExtractOptions{{Frames: 1}, {Timestamp: "1"}})
		wantCalls(t, n)
		matchRE(t, `Blocked hostname by fetch_content domain policy: www\.youtube\.com`, errOf(r[0]))
		matchRE(t, `Blocked hostname by fetch_content domain policy: www\.youtube\.com`, errOf(r[1]))
	})
	tw(t, F, "fetch_content rejects chunked oversized text responses while reading", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: strings.Repeat("A", 6<<20), Header: hdr("content-type", "text/html")}
		})
		r := ExtractContent(bg(), "https://allowed.example/article", pageOpts())
		matchRE(t, `Response too large \(5MB\)`, errOf(r))
	})
}

func TestUpstream_declared_web_links(t *testing.T) {
	const F = "declared-web-links"
	parse := func(t *testing.T, src string) *html.Node {
		doc, err := html.Parse(strings.NewReader(src))
		noErr(t, err)
		return doc
	}
	sp := func(s string) *string { return &s }

	tw(t, F, "discovers registered relations from Link headers and HTML declarations", func(t *testing.T) {
		doc := parse(t, `<!doctype html><html><head>
		<base href="/v2/">
		<link rel="stylesheet service-doc" href="docs" type="text/html">
		<link rel="alternate" href="/feed.xml">
	</head><body>
		<a rel="describedby" href="/schema">Schema</a>
		<a href="/developers">Developer careers</a>
		<a rel="service-desc" href="javascript:alert(1)">Unsafe</a>
	</body></html>`)
		links := DiscoverDeclaredWebLinks(doc, sp(`</catalog>; title="API, <catalog>"; rel="API-CATALOG"; type="application/linkset+json", `+
			`</schema>; rel="service-desc"; type="application/schema+json", `+
			`</metadata>; rel="service-meta"; optional, `+
			`</quoted>; title="x; rel=service-doc"; rel="alternate", `+
			`</anchored>; rel="service-doc"; anchor="/other", `+
			`</ignored>; rel="alternate"`), "https://example.com/root/start")
		want := []DeclaredWebLink{
			{URL: "https://example.com/catalog", Relations: []string{"api-catalog"}, Type: "application/linkset+json"},
			{URL: "https://example.com/schema", Relations: []string{"service-desc", "describedby"}, Type: "application/schema+json"},
			{URL: "https://example.com/metadata", Relations: []string{"service-meta"}},
			{URL: "https://example.com/v2/docs", Relations: []string{"service-doc"}, Type: "text/html"},
		}
		if !reflect.DeepEqual(links, want) {
			t.Fatalf("%+v", links)
		}
	})
	tw(t, F, "bounds declarations while preserving their relation annotations", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 25; i++ {
			b.WriteString(`<link rel="service-doc" href="/docs/` + itoa(i) + `">`)
		}
		links := DiscoverDeclaredWebLinks(parse(t, `<html><head>`+b.String()+`</head></html>`), nil, "https://example.com/")
		if len(links) != 20 {
			t.Fatal(len(links))
		}
		oversized := DiscoverDeclaredWebLinks(parse(t, "<html></html>"), sp(`<https://example.com/`+strings.Repeat("x", 4096)+`>; rel="service-doc"`), "https://example.com/")
		if len(oversized) != 0 {
			t.Fatal(oversized)
		}
		content := AppendDeclaredWebLinks("Existing: https://example.com/docs/0-extra", links[:2])
		matchRE(t, `<https://example\.com/docs/0>`, content)
		matchRE(t, `<https://example\.com/docs/1>`, content)
	})
	tw(t, F, "HTML extraction surfaces declared documentation links without broad URL heuristics", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true}}`)
		article := strings.Repeat("Readable article content remains the primary result. ", 20)
		type fixture struct{ html, link string }
		fixtures := map[string]fixture{
			"https://example.com/readable": {`<!doctype html><html><head><title>Readable</title></head><body><article><h1>Readable</h1><p>` + article + `</p></article></body></html>`,
				`</openapi.json>; rel="service-desc"; type="application/vnd.oai.openapi+json;version=3.1"`},
			"https://example.com/shell":    {`<!doctype html><html><head><title>API shell</title><link rel="service-doc" href="/docs"></head><body><div id="app"></div></body></html>`, ""},
			"https://example.com/fallback": {`<!doctype html><html><head><title>Rendered API</title></head><body><div id="app"></div></body></html>`, `</openapi.json>; rel="service-desc"`},
			"https://example.com/generic":  {`<!doctype html><html><head><title>Company</title></head><body><a href="/developers">Developer careers</a></body></html>`, ""},
		}
		n := useNet(t, func(c netCall) netReply {
			if c.URL == "https://r.jina.ai/https://example.com/fallback" {
				return reply(200, "Title: Rendered API\nMarkdown Content:\n# Rendered API\n\n"+article)
			}
			f, ok := fixtures[c.URL]
			if !ok {
				return reply(404, "not available")
			}
			h := hdr("content-type", "text/html; charset=utf-8")
			if f.link != "" {
				h.Set("link", f.link)
			}
			return netReply{Status: 200, Body: f.html, Header: h}
		})
		take := func() []string {
			u := n.urls()
			n.mu.Lock()
			n.calls = nil
			n.mu.Unlock()
			return u
		}
		readable := ExtractContent(bg(), "https://example.com/readable", pageOpts())
		rc := take()
		shell := ExtractContent(bg(), "https://example.com/shell", pageOpts())
		sc := take()
		fb := ExtractContent(bg(), "https://example.com/fallback", pageOpts())
		fc := take()
		gen := ExtractContent(bg(), "https://example.com/generic", pageOpts())

		if readable.Error != nil {
			t.Fatalf("%+v", readable)
		}
		matchRE(t, `Readable article content`, readable.Content)
		matchRE(t, `## Declared links`, readable.Content)
		matchRE(t, `service-desc`, readable.Content)
		matchRE(t, `https://example\.com/openapi\.json`, readable.Content)
		if !eqStrings(rc, []string{"https://example.com/readable"}) {
			t.Fatal(rc)
		}
		if shell.Error != nil {
			t.Fatalf("%+v", shell)
		}
		matchRE(t, `service-doc`, shell.Content)
		matchRE(t, `https://example\.com/docs`, shell.Content)
		if !eqStrings(sc, []string{"https://example.com/shell", "https://r.jina.ai/https://example.com/shell"}) {
			t.Fatal(sc)
		}
		if fb.Error != nil {
			t.Fatalf("%+v", fb)
		}
		matchRE(t, `Rendered API`, fb.Content)
		matchRE(t, `service-desc`, fb.Content)
		matchRE(t, `https://example\.com/openapi\.json`, fb.Content)
		if !eqStrings(fc, []string{"https://example.com/fallback", "https://r.jina.ai/https://example.com/fallback"}) {
			t.Fatal(fc)
		}
		noMatchRE(t, `https://example\.com/developers`, gen.Content)
		if gen.Error == nil {
			t.Fatalf("%+v", gen)
		}
	})
}

var onePixelPNG = func() string {
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	return string(b)
}()

func TestUpstream_fetch_modes(t *testing.T) {
	const F = "fetch-modes"
	tw(t, F, "local HTTP fetch sends the compatible User-Agent", func(t *testing.T) {
		extractEnv(t, "")
		n := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "body", Header: hdr("content-type", "text/plain")}
		})
		o := pageOpts()
		o.Mode = "raw"
		ExtractContent(bg(), "https://example.com/article", o)
		if got := n.calls[0].Header.Get("User-Agent"); got != "OpenAI File Downloader, XaiImageApiFetch/1.0" {
			t.Fatal(got)
		}
	})
	tw(t, F, "raw mode returns textual non-2xx bodies but rejects images", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(c netCall) netReply {
			if strings.HasSuffix(c.URL, ".png") {
				return netReply{Status: 200, Body: onePixelPNG, Header: hdr("content-type", "image/png")}
			}
			return netReply{Status: 404, Body: `{"error":"missing"}`, Header: hdr("content-type", "application/json; charset=utf-8")}
		})
		o := pageOpts()
		o.Mode = "raw"
		text := ExtractContent(bg(), "https://example.com/missing", o)
		if text.Error != nil || text.Status == nil || *text.Status != 404 || text.Content != `{"error":"missing"}` {
			t.Fatalf("%+v", text)
		}
		img := ExtractContent(bg(), "https://example.com/pixel.png", o)
		matchRE(t, `Unsupported content type in raw mode: image/png`, errOf(img))
		if img.Thumbnail != nil {
			t.Fatal("thumbnail")
		}
	})
	tw(t, F, "raw mode keeps data URIs in the exact HTTP body", func(t *testing.T) {
		extractEnv(t, "")
		body := "exact data:text/plain,hello%20world body"
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: body, Header: hdr("content-type", "text/plain")}
		})
		o := pageOpts()
		o.Mode = "raw"
		r := FetchAllContent(bg(), []string{"https://example.com/data"}, o)
		if r[0].Content != body {
			t.Fatal(r[0].Content)
		}
	})
	tw(t, F, "readable mode returns supported image content", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: onePixelPNG, Header: hdr("content-type", "image/png")}
		})
		r := ExtractContent(bg(), "https://example.com/pixel.png", pageOpts())
		if r.Error != nil || r.MimeType != "image/png" || r.Thumbnail == nil || r.Thumbnail.MimeType != "image/png" {
			t.Fatalf("%+v", r)
		}
		matchRE(t, `Image fetched \(1×1, image/png\)`, r.Content)
	})
}

func TestUpstream_extract_cold_deadline(t *testing.T) {
	const F = "extract-cold-deadline"
	articleHTML := `<html><head><title>Article</title></head><body><article><h1>Article</h1><p>` + strings.Repeat("A useful article with enough readable text for successful extraction. ", 30) + `</p></article></body></html>`
	tw(t, F, "direct extraction rejects late success: ${scenario}", func(t *testing.T) {
		for _, scenario := range []string{"import-timer", "import-elapsed", "html-processing", "image-processing", "caller-abort"} {
			t.Run(scenario, func(t *testing.T) {
				extractEnv(t, `{"fetch":{"timeout":0.1},"fetchRouting":{"providers":["http"]}}`)
				image := scenario == "image-processing" || scenario == "caller-abort"
				useNet(t, func(netCall) netReply {
					if image {
						return netReply{Status: 200, Body: onePixelPNG, Header: hdr("content-type", "image/png")}
					}
					return netReply{Status: 200, Body: articleHTML, Header: hdr("content-type", "text/html")}
				})
				var clock int64
				nowMs = func() int64 { return clock }
				ctx, cancel := context.WithCancel(bg())
				defer cancel()
				extractHook = func(stage string) {
					switch {
					case stage == "before-parse" && scenario == "import-timer":
						time.Sleep(160 * time.Millisecond)
					case stage == "before-parse" && scenario == "import-elapsed":
						clock = 101
					case stage == "processing" && (scenario == "html-processing" || scenario == "image-processing" || scenario == "caller-abort"):
						clock = 101
						if scenario == "caller-abort" {
							cancel()
						}
					}
				}
				t.Cleanup(func() { extractHook = func(string) {}; ResetCaches() })
				r := ExtractContent(ctx, "https://example.com/article", pageOpts())
				if scenario == "caller-abort" {
					if errOf(r) != "Aborted" {
						t.Fatalf("%+v", r)
					}
				} else if !strings.HasPrefix(errOf(r), "The operation was aborted.") {
					t.Fatalf("%+v", r)
				}
				if r.Content != "" || r.Thumbnail != nil {
					t.Fatalf("%+v", r)
				}
			})
		}
	})
}

// Extras beyond the upstream suite: behaviour the Go extraction pins itself.

func TestExtractHeadingTitle(t *testing.T) {
	for in, want := range map[string]string{"# Title\nbody": "Title", "text\n## **Bold** head": "Bold head", "### deep": ""} {
		got, ok := ExtractHeadingTitle(in)
		if (got != "") != ok || got != want {
			t.Fatalf("%q => %q,%v", in, got, ok)
		}
	}
}

func TestExtractReadableMarkdown(t *testing.T) {
	extractEnv(t, "")
	page := `<!doctype html><html><head><title>Guide | Example</title><style>.x{}</style></head><body>
<nav><a href="/home">Home</a></nav>
<article><h1>Guide</h1><p>` + strings.Repeat("This guide explains the <strong>important</strong> parts of the system in detail. ", 8) + `</p>
<h2>Steps</h2><ul><li>First step</li><li>Second <a href="https://example.com/x">linked</a> step</li></ul>
<pre><code>go test ./...</code></pre><script>alert(1)</script></article><footer>Copyright</footer></body></html>`
	useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Body: page, Header: hdr("content-type", "text/html")}
	})
	r := ExtractContent(bg(), "https://example.com/guide", pageOpts())
	if r.Error != nil {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"# Guide", "## Steps", "*   First step", "[linked](https://example.com/x)", "**important**", "```\ngo test ./...\n```"} {
		if !strings.Contains(r.Content, want) {
			t.Errorf("missing %q in\n%s", want, r.Content)
		}
	}
	for _, bad := range []string{"alert(1)", "Copyright", ".x{}"} {
		if strings.Contains(r.Content, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}

func TestExtractGuards(t *testing.T) {
	t.Run("declared size over the cap is refused before reading", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "x", Header: hdr("content-type", "text/plain", "content-length", "9000000")}
		})
		matchRE(t, `Response too large \(9MB\)`, errOf(ExtractContent(bg(), "https://example.com/big", pageOpts())))
	})
	t.Run("binary content types are unsupported", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "PK", Header: hdr("content-type", "application/zip")}
		})
		matchRE(t, `^Unsupported content type: application/zip`, errOf(ExtractContent(bg(), "https://example.com/a.zip", pageOpts())))
	})
	t.Run("plain text keeps its body and heading title", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "# Notes\nhello", Header: hdr("content-type", "text/plain")}
		})
		r := ExtractContent(bg(), "https://example.com/notes.txt", pageOpts())
		if r.Error != nil || r.Title != "Notes" || r.Content != "# Notes\nhello" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("a cancelled context is reported as Aborted without network", func(t *testing.T) {
		extractEnv(t, "")
		n := useNet(t, func(netCall) netReply { return reply(200, "x") })
		ctx, cancel := context.WithCancel(bg())
		cancel()
		if got := errOf(ExtractContent(ctx, "https://example.com/", pageOpts())); got != "Aborted" || n.count() != 0 {
			t.Fatal(got)
		}
	})
	t.Run("private addresses are blocked before any request", func(t *testing.T) {
		extractEnv(t, "")
		n := useNet(t, func(netCall) netReply { return reply(200, "x") })
		matchRE(t, `Blocked internal address`, errOf(ExtractContent(bg(), "http://127.0.0.1:8080/x", ExtractOptions{})))
		if n.count() != 0 {
			t.Fatal("request sent")
		}
	})
	t.Run("frame and timestamp extraction of remote videos is a named gap", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply { return reply(200, "x") })
		o := pageOpts()
		o.Frames = 3
		matchRE(t, `not available in this Go port yet`, errOf(ExtractContent(bg(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ", o)))
	})
}

func itoa(i int) string { return strconv.Itoa(i) }
