package websearch

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// utils.ts installGlobalProxyFetch wraps the global fetch, so every request a proxied call makes
// (search providers, hosted fetch providers, Gemini API, thumbnails, redirect probes) runs curl.
// The port sends those requests through guardedFetch, doAPI and apiClientFor; this checks the entry
// points of the slices that were written before the curl transport (stack integration): each one
// runs curl through the scoped proxy and none of them sends a request over net/http.
func TestEveryOutboundPathOfAProxiedScopeRunsCurl(t *testing.T) {
	const proxy = "http://call-proxy.example:8080/"
	apiKey := "synthetic-key"
	cases := []struct {
		name string
		url  string
		run  func(ctx context.Context)
	}{
		{"Gemini API request", "https://generativelanguage.googleapis.com/v1beta/files/abc", func(ctx context.Context) {
			resp, err := FetchGeminiAPI(ctx, "https://generativelanguage.googleapis.com/v1beta/files/abc", RequestInit{}, &apiKey)
			if err == nil {
				resp.Body.Close()
			}
		}},
		{"Gemini grounding redirect probe", "https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc", func(ctx context.Context) {
			resolveGeminiRedirect(ctx, "https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc")
		}},
		{"YouTube thumbnail", "https://img.youtube.com/vi/dQw4w9WgXcQ/hqdefault.jpg", func(ctx context.Context) {
			fetchYouTubeThumbnail(ctx, "dQw4w9WgXcQ")
		}},
		{"TinyFish search", "https://api.search.tinyfish.ai", func(ctx context.Context) {
			_, _ = SearchWithTinyFish(ctx, "q", SearchOptions{})
		}},
		{"Perplexity search", "https://api.perplexity.ai/chat/completions", func(ctx context.Context) {
			_, _ = SearchWithPerplexity(ctx, "q", SearchOptions{})
		}},
		{"Firecrawl search", "https://api.firecrawl.dev", func(ctx context.Context) {
			_, _ = SearchWithFirecrawl(ctx, "q", SearchOptions{})
		}},
		{"page fetch with credential redirects", "https://93.184.216.34/page", func(ctx context.Context) {
			resp, err := FetchWithCredentialRedirects(ctx, "https://93.184.216.34/page", RequestInit{}, nil)
			if err == nil {
				resp.Body.Close()
			}
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			extractEnv(t, `{"tinyfishApiKey":"k","perplexityApiKey":"k","firecrawlApiKey":"k","geminiApiKey":"k"}`)
			n := useNet(t, func(netCall) netReply { return reply(200, "{}") })
			f := useCurl(t, map[string]curlRoute{})
			c.run(WithProxy(bg(), proxy))
			if len(n.urls()) != 0 {
				t.Fatalf("a request went over net/http: %v", n.urls())
			}
			found := false
			for _, call := range f.snapshot() {
				if valueAfter(call.Args, "-x") == proxy && len(call.Args) > 0 && strings.HasPrefix(call.Args[len(call.Args)-1], c.url) {
					found = true
				}
			}
			if !found {
				t.Fatalf("curl was not run for %s through %s: %+v", c.url, proxy, f.snapshot())
			}
		})
	}
	t.Run("an unproxied scope sends the same request over net/http", func(t *testing.T) {
		extractEnv(t, `{"geminiApiKey":"k"}`)
		n := useNet(t, func(netCall) netReply { return reply(200, "{}") })
		f := useCurl(t, map[string]curlRoute{})
		resp, err := FetchGeminiAPI(bg(), "https://generativelanguage.googleapis.com/v1beta/files/abc", RequestInit{Method: http.MethodGet}, &apiKey)
		noErr(t, err)
		resp.Body.Close()
		if len(n.urls()) != 1 || len(f.snapshot()) != 0 {
			t.Fatalf("net %v, curl %d", n.urls(), len(f.snapshot()))
		}
	})
}
