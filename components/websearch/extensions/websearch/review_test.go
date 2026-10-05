package websearch

// Tests added by the adversarial review (rev-pigpen-websearch). They are not upstream twins:
// they pin the security hardening the port claims beyond the original (dial-time address check,
// NAT64/6to4/multicast blocking, body caps) and two review fixes (image pixel budget, guidance
// that names only what this port can do).

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// pngHeaderOnly is a PNG that declares w×h 8-bit grey pixels but carries only a few rows of
// data: a few kilobytes on the wire that ask the decoder for w*h bytes of memory.
func pngHeaderOnly(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(typ string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		c := crc32.NewIEEE()
		c.Write([]byte(typ))
		c.Write(data)
		_ = binary.Write(&b, binary.BigEndian, c.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 0 // greyscale
	chunk("IHDR", ihdr)
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
	row := make([]byte, 1+int(w))
	for i := 0; i < 4; i++ {
		_, _ = zw.Write(row)
	}
	_ = zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return b.Bytes()
}

func TestReviewImagePixelBudget(t *testing.T) {
	t.Run("an image whose declared size exceeds the pixel budget is refused before decoding", func(t *testing.T) {
		data := pngHeaderOnly(9000, 9000)
		img, err := resizeImage(data, "image/png", 2000, 2000)
		if img != nil || err == nil {
			t.Fatalf("img=%v err=%v", img != nil, err)
		}
		matchRE(t, `^Image too large to process \(9000×9000`, err.Error())
	})
	t.Run("fetch_content reports the refused image instead of decoding it", func(t *testing.T) {
		extractEnv(t, "")
		data := pngHeaderOnly(9000, 9000)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: string(data), Header: hdr("content-type", "image/png")}
		})
		matchRE(t, `^Image too large to process`, errOf(ExtractContent(bg(), "https://example.com/bomb.png", pageOpts())))
	})
	t.Run("an image inside the budget but over 2000px is still resized", func(t *testing.T) {
		data := pngHeaderOnly(2400, 10)
		// Truncated pixel data: decode fails, which is "cannot decode", not the budget error.
		img, err := resizeImage(data, "image/png", 2000, 2000)
		if img != nil || err != nil {
			t.Fatalf("img=%v err=%v", img != nil, err)
		}
	})
}

// unportedGuidance matches options the original offers that this port cannot act on.
const unportedGuidance = `firecrawl|crawl4ai|tinyfish|TINYFISH|search1api|SEARCH1API|querit|QUERIT|kagiApiKey|KAGI_API_KEY|ollama|OLLAMA|parallel|PARALLEL|brightdata|BRIGHTDATA|GEMINI_API_KEY|gemini\.google\.com|Chrom|/login|Codex|openaiApiKey|OPENAI_API_KEY|searxng|SEARXNG|bocha|BOCHA|cloudflare|CLOUDFLARE|anysearch|xcrawl|serpapi|serper|serply|valyu|You\.com|Mistral|Grok`

func TestReviewGuidanceNamesOnlyPortedOptions(t *testing.T) {
	t.Run("the fetch fallback checklist offers only what this port can do", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply { return reply(500, "oops") })
		msg := errOf(ExtractContent(bg(), "https://example.com/oops", ExtractOptions{Lookup: pubLookup, ToolNames: RegisteredToolNames{WebSearch: "web_search", FetchContent: "fetch_content"}}))
		matchRE(t, `Fallback options:`, msg)
		matchRE(t, `Enable the keyless Jina Reader fallback`, msg)
		matchRE(t, `Use web_search to find content about this topic`, msg)
		noMatchRE(t, unportedGuidance, msg)
	})
	t.Run("the checklist says so when no fallback is available", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"providers":["http","jina"],"allowRemoteHostedProviders":true}}`)
		useNet(t, func(netCall) netReply { return reply(500, "oops") })
		msg := errOf(ExtractContent(bg(), "https://example.com/oops", pageOpts()))
		matchRE(t, `Fallback options:\n  • none in this port`, msg)
		noMatchRE(t, unportedGuidance, msg)
	})
	t.Run("the no-provider error names only ported providers", func(t *testing.T) {
		providerEnv(t, `{"webSearch":{"allowedProviders":["duckduckgo"]}}`, nil)
		useNet(t, func(netCall) netReply { return netReply{Status: 500} })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `^No search provider available`)
		for _, want := range []string{"braveApiKey", "tavilyApiKey", "serpdiveApiKey", "perplexityApiKey", "exaApiKey", "BRAVE_API_KEY", "EXA_API_KEY", `"duckduckgo"`} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%q missing from %q", want, err.Error())
			}
		}
		noMatchRE(t, strings.ReplaceAll(unportedGuidance, `kagiApiKey|KAGI_API_KEY|`, ``), err.Error())
	})
}

func TestReviewDialTimeAddressCheck(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("internal"))
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	t.Run("a connection that lands on loopback is refused even after validation passed", func(t *testing.T) {
		// guardedFetch is the connect step: validation (by name) already passed, and the name now
		// resolves to loopback (DNS rebinding). The dialer must refuse the address.
		_, err := guardedFetch(context.Background(), target, RequestInit{Method: "GET"}, ValidationOptions{})
		if err == nil {
			t.Fatal("loopback connection was allowed")
		}
		matchRE(t, `Blocked internal address for 127\.0\.0\.1: 127\.0\.0\.1`, err.Error())
		if hits.Load() != 0 {
			t.Fatal("the request reached the server")
		}
	})
	t.Run("allowRanges also applies at dial time", func(t *testing.T) {
		resp, err := guardedFetch(context.Background(), target, RequestInit{Method: "GET"}, ValidationOptions{AllowRanges: []string{"127.0.0.1/32"}})
		noErr(t, err)
		_ = resp.Body.Close()
		if hits.Load() != 1 {
			t.Fatal(hits.Load())
		}
	})
	t.Run("the environment proxy is not used unless ssrf.trustEnvProxy is set", func(t *testing.T) {
		// With trustEnvProxy off the dial check must stay in force: the environment proxy
		// (which would resolve the target itself) is ignored, so the loopback dial is refused.
		t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
		t.Setenv("http_proxy", "http://proxy.invalid:3128")
		_, err := guardedFetch(context.Background(), target, RequestInit{Method: "GET"}, ValidationOptions{})
		if err == nil {
			t.Fatal("allowed")
		}
		matchRE(t, `Blocked internal address`, err.Error())
	})
}

func TestReviewHardenedRanges(t *testing.T) {
	blocked := []string{
		"http://[64:ff9b::7f00:1]/",    // NAT64 of 127.0.0.1
		"http://[64:ff9b::a9fe:a9fe]/", // NAT64 of 169.254.169.254
		"http://[2002:a00:1::1]/",      // 6to4 of 10.0.0.1
		"http://[2002:c0a8:101::1]/",   // 6to4 of 192.168.1.1
		"http://[ff02::1]/",            // multicast
		"http://[::ffff:10.0.0.1]/",    // IPv4-mapped private
		"http://100.64.0.1/",           // CGNAT
		"http://100.127.255.254/",
		"http://198.18.0.1/", // benchmarking / fake-IP
		"http://224.0.0.1/",
		"http://0.0.0.0/",
		"http://a.localhost/",
		"http://LOCALHOST./",
	}
	for _, raw := range blocked {
		if _, err := ValidateRemoteURL(bg(), raw, ValidationOptions{Lookup: pubLookup}); err == nil {
			t.Errorf("%s was allowed", raw)
		}
	}
	allowed := []string{
		"http://[64:ff9b::808:808]/", // NAT64 of 8.8.8.8
		"http://[2002:808:808::1]/",  // 6to4 of 8.8.8.8
		"http://100.128.0.1/",
		"http://[2606:4700::1111]/",
	}
	for _, raw := range allowed {
		if _, err := ValidateRemoteURL(bg(), raw, ValidationOptions{Lookup: pubLookup}); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestReviewBodyCaps(t *testing.T) {
	t.Run("a body without Content-Length is cut off at the page cap", func(t *testing.T) {
		extractEnv(t, "")
		big := strings.Repeat("a", pageMaxBytes+1)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: big, Header: hdr("content-type", "text/plain")}
		})
		matchRE(t, `^Response too large \(5MB\)`, errOf(ExtractContent(bg(), "https://example.com/stream", pageOpts())))
	})
	t.Run("raw mode applies the same cap", func(t *testing.T) {
		extractEnv(t, "")
		big := strings.Repeat("a", pageMaxBytes+1)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: big, Header: hdr("content-type", "text/plain")}
		})
		o := pageOpts()
		o.Mode = "raw"
		matchRE(t, `^Response too large \(5MB\)`, errOf(ExtractContent(bg(), "https://example.com/stream", o)))
	})
	t.Run("provider API bodies are bounded", func(t *testing.T) {
		resp := &http.Response{Body: nopCloser{bytes.NewReader(bytes.Repeat([]byte("x"), maxProviderBody+10))}}
		body, err := readBody(resp)
		noErr(t, err)
		if len(body) != maxProviderBody {
			t.Fatal(len(body))
		}
	})
}

func TestReviewAPIRedirectsStayHTTP(t *testing.T) {
	isolate(t)
	useNet(t, func(c netCall) netReply { return redirectTo(302, "file:///etc/passwd") })
	_, err := FetchWithCredentialRedirects(bg(), "https://api.example.com/x", RequestInit{Method: "GET"}, nil)
	wantErr(t, err, `API redirect from https://api\.example\.com must use HTTP\(S\)`)
}

// The documented privacy default: with nothing configured, `auto` sends the query to the keyless
// Exa MCP (as the original does), and to nothing else.
func TestReviewUnconfiguredAutoUsesKeylessExaMCP(t *testing.T) {
	providerEnv(t, "", nil)
	n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("fetch failed")} })
	_, _ = Search(bg(), "q", FullSearchOptions{Provider: Auto})
	urls := n.urls()
	if len(urls) == 0 {
		t.Fatal("no request")
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, exaMCPURL) {
			t.Fatalf("unexpected request to %s (all: %v)", u, urls)
		}
	}
}

// The search half of upstream "Kagi search maps v1 results and Extract maps markdown" (the title is
// a named skip because Kagi Extract is not ported). Same fixture and assertions as upstream,
// without the extract call.
func TestKagiSearchHalfOfUpstreamCase(t *testing.T) {
	providerEnv(t, `{"kagiApiKey":"kagi-test-key"}`, nil)
	n := useNet(t, func(c netCall) netReply {
		if c.URL == "https://kagi.com/api/v1/search" {
			return reply(200, `{"data":{"search":[{"title":"Kagi result","url":"https://example.com/kagi","snippet":"Kagi snippet"}]}}`)
		}
		return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
	})
	search, err := SearchWithKagi(bg(), "premium search", SearchOptions{NumResults: nf(3), IncludeContent: true})
	noErr(t, err)
	if len(n.calls) != 1 {
		t.Fatal(n.urls())
	}
	c := n.calls[0]
	if c.URL != "https://kagi.com/api/v1/search" || c.Method != "POST" || c.Header.Get("Authorization") != "Bearer kagi-test-key" {
		t.Fatalf("%+v", c)
	}
	if c.Body != `{"limit":3,"query":"premium search"}` {
		t.Fatal(c.Body)
	}
	want := []SearchResult{{Title: "Kagi result", URL: "https://example.com/kagi", Snippet: "Kagi snippet"}}
	if len(search.Results) != 1 || search.Results[0] != want[0] {
		t.Fatalf("%+v", search.Results)
	}
}
