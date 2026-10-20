package websearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refusal is the exact message of an unported feature, written out in full (not built from the
// constants of refusal.go) so that a change of wording or of the install advice fails here.
func refusal(feature string) string {
	return "@pi-in-go/pigpen-websearch (Go port) does not support " + feature + " yet. Install the original extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG."
}

func readJSONFile(t *testing.T, rel string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("the Package files are not beside this module (a mutation run copies the module alone); the repository run checks them")
		}
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The message names this Package and the original pinned in provenance.json; both must follow
// package.json and the vendored original.
func TestRefusalNamesPackageAndOriginal(t *testing.T) {
	pkg := readJSONFile(t, "../../package.json")
	orig := readJSONFile(t, "../../port/oracle/package.json")
	prov := readJSONFile(t, "../../provenance.json")
	upstream := prov["upstreams"].([]any)[0].(map[string]any)
	if upstream["name"] != orig["name"] {
		t.Fatalf("provenance names %v, the vendored original is %v", upstream["name"], orig["name"])
	}
	want := pkg["name"].(string) + " (Go port) does not support x yet. Install the original extension, npm:" + orig["name"].(string) + "@" + orig["version"].(string) + ", which runs unchanged in PiG."
	if got := unsupported("x").Error(); got != want {
		t.Fatalf("refusal = %q, want %q", got, want)
	}
	if got := refusal("x"); got != want {
		t.Fatalf("test helper = %q, want %q", got, want)
	}
}

func execTool(t *testing.T, r *Runtime, name string, params map[string]any) (ToolOutput, error) {
	t.Helper()
	return mustTool(t, r, name).Execute(bg(), params, nil)
}

func wantRefusal(t *testing.T, err error, feature string) {
	t.Helper()
	if want := refusal(feature); err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// providerRuntime isolates the configuration (which clears the provider variables), writes the
// config, then sets the environment of the case.
func providerRuntime(t *testing.T, cfg string, env map[string]string) *Runtime {
	t.Helper()
	r, _ := newRuntime(t, cfg)
	for k, v := range env {
		t.Setenv(k, v)
	}
	return r
}

func TestRefusalSearchProviders(t *testing.T) {
	t.Run("an explicit unported provider", func(t *testing.T) {
		r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "serpbase"})
		wantRefusal(t, err, "SerpBase search provider")
		wantCalls(t, n)
	})
	t.Run("a provider list with an unported provider contacts none", func(t *testing.T) {
		r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": []any{"tavily", "serper"}})
		wantRefusal(t, err, "Serper search provider")
		wantCalls(t, n)
	})
	t.Run("provider all refuses when an unported provider is configured", func(t *testing.T) {
		r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k", "TINYFISH_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "all"})
		wantRefusal(t, err, "TinyFish search provider")
		wantCalls(t, n)
	})
	t.Run("auto refuses at a configured unported provider instead of falling through", func(t *testing.T) {
		// Keyless Exa comes first in the auto order, so the allowlist leaves it out.
		r := providerRuntime(t, `{"webSearch":{"allowedProviders":["tinyfish","tavily"]}}`, map[string]string{"TAVILY_API_KEY": "k", "TINYFISH_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x"})
		wantRefusal(t, err, "TinyFish search provider")
		wantCalls(t, n)
	})
	t.Run("a configured provider that is unported refuses", func(t *testing.T) {
		r := providerRuntime(t, `{"provider":"serper"}`, map[string]string{"TAVILY_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x"})
		wantRefusal(t, err, "Serper search provider")
		wantCalls(t, n)
	})
	t.Run("searchRouting refuses at an available unported provider", func(t *testing.T) {
		cfg := `{"searchRouting":{"providers":["tinyfish","tavily"],"fallbackOn":["quota","unsupported"]}}`
		r := providerRuntime(t, cfg, map[string]string{"TAVILY_API_KEY": "k", "TINYFISH_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x"})
		wantRefusal(t, err, "TinyFish search provider")
		wantCalls(t, n)
	})
	t.Run("source_check refuses too", func(t *testing.T) {
		r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "source_check", map[string]any{"claim": "x", "provider": "serpbase"})
		wantRefusal(t, err, "SerpBase search provider")
		wantCalls(t, n)
	})
	t.Run("a ported provider is not refused", func(t *testing.T) {
		r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
		searchProviderNet(t)
		if _, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "tavily"}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRefusalSearchWorkflows(t *testing.T) {
	for _, workflow := range []string{"summary-review", "auto-summary"} {
		workflow := workflow
		t.Run("workflow parameter "+workflow, func(t *testing.T) {
			r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
			n := searchProviderNet(t)
			_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "tavily", "workflow": workflow})
			wantRefusal(t, err, `web_search workflow "`+workflow+`" (the search curator and generated summaries)`)
			wantCalls(t, n)
		})
	}
	t.Run("configured workflow auto-summary", func(t *testing.T) {
		cfg := `{"workflow":"auto-summary"}`
		r := providerRuntime(t, cfg, map[string]string{"TAVILY_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "tavily"})
		wantRefusal(t, err, `web_search workflow "auto-summary" (the search curator and generated summaries)`)
		wantCalls(t, n)
	})
}

func TestRefusalFetchAnswerMode(t *testing.T) {
	r, _ := newRuntime(t, "")
	n := useNet(t, func(netCall) netReply { return reply(200, "x") })
	_, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page", "mode": "answer", "prompt": "What does it say?"})
	wantRefusal(t, err, "fetch_content mode answer (answering from a page needs a model call)")
	wantCalls(t, n)
}

func TestRefusalFetchAuth(t *testing.T) {
	r, _ := newRuntime(t, "")
	n := useNet(t, func(netCall) netReply { return reply(200, "x") })
	for _, auth := range []any{true, "work"} {
		_, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page", "auth": auth})
		wantRefusal(t, err, "authenticated fetch (auth profiles read local browser cookies)")
	}
	wantCalls(t, n)
}

func TestRefusalFetchSpecialisedExtractors(t *testing.T) {
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(video, []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		params  map[string]any
		feature string
	}{
		{"YouTube", map[string]any{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}, "YouTube video extraction"},
		{"YouTube with a prompt", map[string]any{"url": "https://youtu.be/dQw4w9WgXcQ", "prompt": "summarise"}, "YouTube video extraction"},
		{"YouTube frames", map[string]any{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "frames": 3.0}, "video frame and timestamp extraction"},
		{"YouTube timestamp", map[string]any{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "timestamp": "1:30"}, "video frame and timestamp extraction"},
		{"local video", map[string]any{"url": video}, "local video file analysis"},
		{"local video frames", map[string]any{"url": video, "frames": 2.0}, "video frame and timestamp extraction"},
		{"GitHub repository", map[string]any{"url": "https://github.com/octocat/Hello-World"}, "GitHub repository extraction (clone)"},
		{"GitHub tree", map[string]any{"url": "https://github.com/octocat/Hello-World/tree/main/docs", "forceClone": true}, "GitHub repository extraction (clone)"},
		{"GitHub blob", map[string]any{"url": "https://github.com/octocat/Hello-World/blob/main/README"}, "GitHub repository extraction (clone)"},
		{"GitHub issue", map[string]any{"url": "https://github.com/octocat/Hello-World/issues/12"}, "GitHub issue and pull request extraction"},
		{"GitHub pull request", map[string]any{"url": "https://github.com/octocat/Hello-World/pull/7/files"}, "GitHub issue and pull request extraction"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			r, _ := newRuntime(t, "")
			n := useNet(t, func(netCall) netReply { return reply(200, "x") })
			_, err := execTool(t, r, "fetch_content", c.params)
			wantRefusal(t, err, c.feature)
			wantCalls(t, n)
		})
	}
	t.Run("a batch with one such URL is refused whole", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		useNet(t, func(netCall) netReply { return reply(200, "x") })
		_, err := execTool(t, r, "fetch_content", map[string]any{"urls": []any{"https://93.184.216.34/a", "https://youtu.be/dQw4w9WgXcQ"}})
		wantRefusal(t, err, "YouTube video extraction")
	})
	t.Run("raw mode reads the page as is", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Header: hdr("content-type", "text/plain"), Body: "raw body"}
		})
		if _, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://github.com/octocat/Hello-World", "mode": "raw"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a disabled extractor leaves its URLs to the plain fetch", func(t *testing.T) {
		cfg := `{"youtube":{"enabled":false},"githubClone":{"enabled":false},"githubPrIssue":{"enabled":false},"video":{"enabled":false}}`
		for _, u := range []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://github.com/octocat/Hello-World", "https://github.com/octocat/Hello-World/issues/12", video} {
			extractEnv(t, cfg)
			feature, err := unportedFetchFeature(u, ExtractOptions{})
			if err != nil || feature != "" {
				t.Fatalf("%s: feature %q, err %v", u, feature, err)
			}
		}
	})
	t.Run("a page that is not one of them is not refused", func(t *testing.T) {
		extractEnv(t, "")
		for _, u := range []string{"https://www.youtube.com/playlist?list=PL1", "https://github.com/octocat", "https://github.com/octocat/Hello-World/issues", "https://github.com/octocat/Hello-World/pulls", "https://example.com/a.mp4", "https://example.com/"} {
			feature, err := unportedFetchFeature(u, ExtractOptions{})
			if err != nil || feature != "" {
				t.Fatalf("%s: feature %q, err %v", u, feature, err)
			}
		}
	})
	t.Run("frames on an ordinary page keep the original's message", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply { return reply(200, "x") })
		o := pageOpts()
		o.Frames = 2
		if got := errOf(ExtractContent(bg(), "https://example.com/", o)); got != "Frame extraction only works with YouTube and local video files" {
			t.Fatal(got)
		}
		o = pageOpts()
		o.Timestamp = "1:00"
		if got := errOf(ExtractContent(bg(), "https://example.com/", o)); got != "Timestamp extraction only works with YouTube and local video files" {
			t.Fatal(got)
		}
	})
}

func TestRefusalFetchPDF(t *testing.T) {
	r, _ := newRuntime(t, "")
	useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Header: hdr("content-type", "application/pdf"), Body: "%PDF-1.4\n"}
	})
	_, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/doc.pdf"})
	wantRefusal(t, err, "PDF text extraction")
}

func TestRefusalFetchHostedProviders(t *testing.T) {
	t.Run("a configured self-hosted provider the original would call", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		t.Setenv("FIRECRAWL_BASE_URL", "https://firecrawl.invalid")
		n := useNet(t, func(netCall) netReply { return reply(500, "boom") })
		_, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page"})
		wantRefusal(t, err, "Firecrawl fetch provider")
		wantCalls(t, n, "https://93.184.216.34/page")
	})
	t.Run("an order that lists a provider with a key", func(t *testing.T) {
		cfg := `{"fetchRouting":{"providers":["http","tinyfish"],"allowRemoteHostedProviders":true}}`
		r, _ := newRuntime(t, cfg)
		t.Setenv("TINYFISH_API_KEY", "k")
		useNet(t, func(netCall) netReply { return reply(500, "boom") })
		_, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page"})
		wantRefusal(t, err, "TinyFish fetch provider")
	})
	t.Run("a provider that is not configured is skipped as in the original", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		useNet(t, func(netCall) netReply { return reply(500, "boom") })
		out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page"})
		if err != nil {
			t.Fatal(err)
		}
		if out.IsError || strings.Contains(out.Text(), "(Go port)") {
			t.Fatal(out.Text())
		}
	})
}

func TestRefusalFetchFallbackExtractors(t *testing.T) {
	r, _ := newRuntime(t, "")
	useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: `<html><head><title>t</title></head><body><div id="root"></div><script>self.__next_f.push([1,"x"])</script></body></html>`}
	})
	_, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/app"})
	wantRefusal(t, err, "the RSC payload and Defuddle fallback extractors")
}
