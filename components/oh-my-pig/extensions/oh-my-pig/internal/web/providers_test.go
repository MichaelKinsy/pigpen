package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// allProviderEnvKeys lists every environment variable read by ported providers so tests
// can clear all of them cleanly via t.Setenv.
var allProviderEnvKeys = []string{
	"PARALLEL_API_KEY",
	"EXA_API_KEY",
	"FIRECRAWL_API_KEY",
	"FIRECRAWL_BASE_URL",
	"FIRECRAWL_API_URL",
	"SEARXNG_ENDPOINT",
	"SEARXNG_TOKEN",
	"SEARXNG_BASIC_USERNAME",
	"SEARXNG_BASIC_PASSWORD",
	"PERPLEXITY_API_KEY",
	"PI_PERPLEXITY_API_MODEL",
	"TINYFISH_API_KEY",
	"JINA_API_KEY",
	"KAGI_API_KEY",
	"TAVILY_API_KEY",
	"BRAVE_API_KEY",
	"DEEPSEEK_API_KEY",
	"MOONSHOT_SEARCH_API_KEY",
	"KIMI_SEARCH_API_KEY",
	"MOONSHOT_SEARCH_BASE_URL",
	"KIMI_SEARCH_BASE_URL",
	"SYNTHETIC_API_KEY",
	"OLLAMA_CLOUD_API_KEY",
}

func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range allProviderEnvKeys {
		t.Setenv(k, "")
	}
}

// hostRewriteTransport routes every outbound request to a local httptest.Server while
// recording the original URL, headers, method, and body.
type recordedRequest struct {
	Method  string
	OrigURL string
	Headers http.Header
	Body    string
}

type hostRewriteTransport struct {
	target   *url.URL
	base     http.RoundTripper
	recorded []recordedRequest
}

func (rt *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	}
	rt.recorded = append(rt.recorded, recordedRequest{
		Method:  req.Method,
		OrigURL: req.URL.String(),
		Headers: req.Header.Clone(),
		Body:    string(bodyBytes),
	})
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = rt.target.Scheme
	cloned.URL.Host = rt.target.Host
	if len(bodyBytes) > 0 {
		cloned.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	}
	return rt.base.RoundTrip(cloned)
}

func withTestHTTPClient(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *hostRewriteTransport) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	rt := &hostRewriteTransport{target: target, base: srv.Client().Transport}
	prev := httpClient
	httpClient = &http.Client{Transport: rt, Timeout: 10 * time.Second}
	t.Cleanup(func() { httpClient = prev })
	return srv, rt
}

func TestRegisterAndToolDefinition(t *testing.T) {
	ext := sdk.New("test-web")
	Register(ext)

	def := webSearchToolDef()
	if def.Name != "web_search" || def.Label != "Web Search" {
		t.Fatalf("unexpected tool metadata: name=%q label=%q", def.Name, def.Label)
	}
	if !strings.Contains(def.Description, "Known URLs/programmatic data") {
		t.Fatalf("unexpected description: %q", def.Description)
	}
	props, _ := def.Parameters["properties"].(map[string]any)
	for _, field := range []string{"query", "recency", "limit", "max_tokens", "temperature", "num_search_results"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing field %q", field)
		}
	}
	reqFields, _ := def.Parameters["required"].([]any)
	if !reflect.DeepEqual(reqFields, []any{"query"}) {
		t.Errorf("required = %v, want [query]", reqFields)
	}
}

func TestNoProviderConfigured(t *testing.T) {
	// upstream: packages/coding-agent/src/web/search/index.ts:314-322
	clearProviderEnv(t)
	text, details := ExecuteSearch(context.Background(), SearchParams{Query: "golang"})
	if text != "Error: No web search model configured." {
		t.Fatalf("text = %q, want omp message", text)
	}
	if details.Response.Provider != "none" || details.Error != "No web search model configured." {
		t.Fatalf("unexpected details: %+v", details)
	}
}

func TestProviderSelectionOrderAndFallback(t *testing.T) {
	// upstream: packages/catalog/src/compat/rules/runtime/behavior.kdl:467-479
	clearProviderEnv(t)
	t.Setenv("EXA_API_KEY", "exa-key")
	t.Setenv("BRAVE_API_KEY", "brave-key")

	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"requestId":"exa-1","results":[{"title":"Exa Result","url":"https://example.com/exa","summary":"Exa summary"}]}`))
	})

	text, details := ExecuteSearch(context.Background(), SearchParams{Query: "go generics"})
	if details.Response.Provider != "exa" {
		t.Fatalf("expected exa ahead of brave, got %q", details.Response.Provider)
	}
	if !strings.Contains(text, "Exa Result") || len(rt.recorded) != 1 {
		t.Fatalf("unexpected text=%q calls=%d", text, len(rt.recorded))
	}

	// Fallback when first provider returns HTTP 500.
	t.Run("falls back to next configured provider on HTTP error", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("PARALLEL_API_KEY", "p-key")
		t.Setenv("TAVILY_API_KEY", "t-key")

		_, rt2 := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") == "p-key" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("parallel down"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answer":"Tavily fallback","results":[{"title":"T","url":"https://tavily.test/1","content":"snippet"}]}`))
		})

		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "fallback query"})
		if details.Response.Provider != "tavily" {
			t.Fatalf("expected fallback to tavily, got %q (text=%q)", details.Response.Provider, text)
		}
		if len(rt2.recorded) != 2 {
			t.Fatalf("expected 2 requests, got %d", len(rt2.recorded))
		}
	})

	t.Run("combines errors when all configured providers fail", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("PARALLEL_API_KEY", "p-key")
		t.Setenv("BRAVE_API_KEY", "b-key")

		withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("bad key"))
		})

		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "fail all"})
		if !strings.HasPrefix(text, "Error: All web search providers failed:") {
			t.Fatalf("unexpected error text: %q", text)
		}
		if !strings.Contains(text, "parallel:") || !strings.Contains(text, "brave:") {
			t.Fatalf("expected both provider errors in %q", text)
		}
		if details.Error == "" {
			t.Fatalf("expected error in details")
		}
	})
}

func TestCentralDirectivePipeline(t *testing.T) {
	// upstream: test/web/search/query-pipeline.test.ts: "passes the parsed query to the role-selected provider and post-filters ignored constraints"
	t.Run("post-filters ignored constraints", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("SYNTHETIC_API_KEY", "s-key")
		withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[
				{"title":"Docs page","url":"https://docs.example.com/guide","text":"guide"},
				{"title":"Blog post","url":"https://blog.other.com/post","text":"blog"}
			]}`))
		})
		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "guide site:docs.example.com"})
		if len(details.Response.Sources) != 1 || details.Response.Sources[0].URL != "https://docs.example.com/guide" {
			t.Fatalf("unexpected filtered sources: %+v", details.Response.Sources)
		}
		if strings.Contains(text, "blog.other.com") {
			t.Fatalf("filtered-out source appeared in output: %q", text)
		}
	})

	// upstream: test/web/search/query-pipeline.test.ts: "relaxes a constraint that matches nothing and leads the LLM text with a note"
	t.Run("relaxes constraint that matches nothing and prepends Note", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("SYNTHETIC_API_KEY", "s-key")
		withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"Docs page","url":"https://docs.example.com/guide"}]}`))
		})
		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "guide site:missing.example"})
		if len(details.Response.Sources) != 1 {
			t.Fatalf("expected relaxed results, got %d", len(details.Response.Sources))
		}
		if !strings.HasPrefix(text, "Note: no results matched `site:missing.example`; the constraint was relaxed") {
			t.Fatalf("expected relaxation note, got %q", text)
		}
	})
}

func TestProviderParallel(t *testing.T) {
	// upstream: test/tools/web-search-parallel.test.ts: "maps Parallel search responses into SearchResponse"
	clearProviderEnv(t)
	t.Setenv("PARALLEL_API_KEY", "test-parallel-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"search_id":"search-parallel-2",
			"results":[{"title":"Alpha","url":"https://alpha.example","publish_date":"2024-12-24","excerpts":["First excerpt","Second excerpt"]}]
		}`))
	})

	text, details := ExecuteSearch(context.Background(), SearchParams{Query: "alpha site:alpha.example", Recency: "week"})
	if details.Response.Provider != "parallel" || details.Response.RequestID != "search-parallel-2" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if len(details.Response.Sources) != 1 || details.Response.Sources[0].Snippet != "First excerpt\n\nSecond excerpt" {
		t.Fatalf("unexpected sources: %+v", details.Response.Sources)
	}
	if !strings.Contains(text, "[1] Alpha") {
		t.Fatalf("unexpected formatted text: %q", text)
	}
	if rt.recorded[0].Headers.Get("x-api-key") != "test-parallel-key" || rt.recorded[0].Headers.Get("parallel-beta") != parallelBetaHeader {
		t.Fatalf("unexpected headers: %+v", rt.recorded[0].Headers)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rt.recorded[0].Body), &body); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}
	if body["objective"] != "alpha" {
		t.Fatalf("objective = %v, want alpha", body["objective"])
	}
	sp, _ := body["source_policy"].(map[string]any)
	if sp == nil || sp["after_date"] == "" {
		t.Fatalf("expected source_policy with include_domains and after_date, got %+v", sp)
	}
}

func TestProviderExa(t *testing.T) {
	// upstream: test/tools/web-search-exa.test.ts: "synthesizes answer from multiple summaries joined by double newlines"
	clearProviderEnv(t)
	t.Setenv("EXA_API_KEY", "test-exa-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"requestId":"exa-req-1",
			"results":[
				{"title":"A","url":"https://example.com/a","summary":"Summary A","publishedDate":"2025-01-01"},
				{"title":"B","url":"https://example.com/b","summary":"Summary B"}
			]
		}`))
	})

	text, details := ExecuteSearch(context.Background(), SearchParams{Query: "test query site:example.com after:2024-01-01", Limit: 2})
	if details.Response.Provider != "exa" || details.Response.Answer != "**A**: Summary A\n\n**B**: Summary B" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if !strings.Contains(text, "## Sources") || !strings.Contains(text, "2 sources") {
		t.Fatalf("unexpected formatted text: %q", text)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &body)
	if body["startPublishedDate"] != "2024-01-01" {
		t.Fatalf("expected startPublishedDate, got %+v", body)
	}
}

func TestProviderFirecrawl(t *testing.T) {
	// upstream: test/tools/web-search-firecrawl.test.ts: "sends the Firecrawl POST request and maps web results"
	// upstream: test/tools/web-search-firecrawl.test.ts: "maps before:/after: to a cdr tbs and strips dates from the operator query"
	clearProviderEnv(t)
	t.Setenv("FIRECRAWL_API_KEY", "test-fc-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"fc-123",
			"data":{"web":[
				{"title":"One","url":"https://github.com/oven-sh/bun","description":"Desc one","markdown":"Ignored"},
				{"title":"Two","url":"https://github.com/oven-sh/two","markdown":"Markdown fallback"}
			]}
		}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{
		Query:   "bun runtime site:github.com/oven-sh after:2024-01-01 before:2024-06-30",
		Recency: "month",
		Limit:   2,
	})
	if details.Response.Provider != "firecrawl" || details.Response.RequestID != "fc-123" || details.Response.AuthMode != "api_key" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if len(details.Response.Sources) != 2 || details.Response.Sources[0].Snippet != "Desc one" || details.Response.Sources[1].Snippet != "Markdown fallback" {
		t.Fatalf("unexpected sources: %+v", details.Response.Sources)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &body)
	if body["tbs"] != "cdr:1,cd_min:01/01/2024,cd_max:06/30/2024" {
		t.Fatalf("tbs = %v", body["tbs"])
	}
	if body["query"] != "bun runtime site:github.com/oven-sh" {
		t.Fatalf("query = %v", body["query"])
	}
}

func TestProviderSearXNG(t *testing.T) {
	// upstream: test/tools/web-search-searxng.test.ts: "sends RFC 7617 Basic auth when username and password are configured"
	// upstream: test/tools/web-search-searxng.test.ts: "demotes engine-hostile operators while keeping bare-domain site: filters"
	clearProviderEnv(t)
	t.Setenv("SEARXNG_ENDPOINT", "https://searx.example.org/")
	t.Setenv("SEARXNG_BASIC_USERNAME", "alice")
	t.Setenv("SEARXNG_BASIC_PASSWORD", "s3cret")

	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results":[{"title":"SearXNG","url":"https://github.com/r","content":"Metasearch result"}],
			"answers":["  Forty-two  ",{"answer":"Legacy answer"},{"translations":[{"text":"Hallo"},{"text":"Guten Tag"}]}],
			"suggestions":["related search"]
		}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{
		Query:   "!!g site:github.com/can1357/oh-my-pi inurl:releases site:github.com lang:de 17.1.1",
		Recency: "week",
		Limit:   1,
	})
	if details.Response.Provider != "searxng" {
		t.Fatalf("provider = %q", details.Response.Provider)
	}
	if details.Response.Answer != "Forty-two\n\nLegacy answer\n\nHallo\nGuten Tag" {
		t.Fatalf("answer = %q", details.Response.Answer)
	}
	if !reflect.DeepEqual(details.Response.RelatedQuestions, []string{"related search"}) {
		t.Fatalf("related = %v", details.Response.RelatedQuestions)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if got := rt.recorded[0].Headers.Get("Authorization"); got != wantAuth {
		t.Fatalf("Authorization = %q, want %q", got, wantAuth)
	}
	u, _ := url.Parse(rt.recorded[0].OrigURL)
	if u.Query().Get("time_range") != "month" || u.Query().Get("language") != "de" || strings.Contains(u.Query().Get("q"), "!!g") {
		t.Fatalf("unexpected query params: %v", u.Query())
	}
}

func TestProviderPerplexity(t *testing.T) {
	// upstream: test/web/search/perplexity.test.ts: "maps site:/-site:/after: directives onto native filters and strips them from the query"
	// upstream: test/web/search/perplexity.test.ts: "parses related_questions into relatedQuestions, preserving order and dropping blanks"
	clearProviderEnv(t)
	t.Setenv("PERPLEXITY_API_KEY", "pplx-key")

	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"pplx-1",
			"model":"sonar-pro",
			"choices":[{"message":{"content":"QUIC uses UDP."}}],
			"citations":["https://docs.rs/quinn"],
			"search_results":[{"title":"Quinn docs","url":"https://docs.rs/quinn","snippet":"QUIC in Rust","date":"2025-01-10"}],
			"related_questions":["How does QUIC handle loss?","   ","What is 0-RTT?"],
			"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}
		}`))
	})

	text, details := ExecuteSearch(context.Background(), SearchParams{
		Query: "rust site:docs.rs -site:reddit.com after:2024-06-01",
	})
	if details.Response.Provider != "perplexity" || details.Response.Answer != "QUIC uses UDP." {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if !reflect.DeepEqual(details.Response.RelatedQuestions, []string{"How does QUIC handle loss?", "What is 0-RTT?"}) {
		t.Fatalf("related = %v", details.Response.RelatedQuestions)
	}
	if len(details.Response.Citations) != 1 || details.Response.Usage == nil || details.Response.Usage.TotalTokens != 30 {
		t.Fatalf("unexpected citations/usage: %+v", details.Response)
	}
	if !strings.Contains(text, "## Citations") || !strings.Contains(text, "## Related") {
		t.Fatalf("unexpected formatted text: %q", text)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &body)
	if body["search_after_date_filter"] != "6/1/2024" {
		t.Fatalf("search_after_date_filter = %v", body["search_after_date_filter"])
	}
	if !reflect.DeepEqual(body["search_domain_filter"], []any{"docs.rs", "-reddit.com"}) {
		t.Fatalf("search_domain_filter = %v", body["search_domain_filter"])
	}
}

func TestProviderTinyFish(t *testing.T) {
	// upstream: test/tools/web-search-tinyfish.test.ts: "rewrites directive queries with supported operators only"
	// upstream: test/tools/web-search-tinyfish.test.ts: "maps a lang: locale directive onto location and language"
	clearProviderEnv(t)
	t.Setenv("TINYFISH_API_KEY", "tf-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"TinyFish Hit","url":"https://github.com/a.pdf","snippet":"  clean   snippet ","site_name":"GitHub"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{
		Query:   `"error handling" rust site:github.com -site:gitlab.com filetype:pdf lang:it-it`,
		Recency: "day",
	})
	if details.Response.Provider != "tinyfish" || len(details.Response.Sources) != 1 {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if details.Response.Sources[0].Snippet != "clean snippet" || details.Response.Sources[0].Author != "GitHub" {
		t.Fatalf("unexpected source: %+v", details.Response.Sources[0])
	}
	u, _ := url.Parse(rt.recorded[0].OrigURL)
	q := u.Query()
	if q.Get("query") != `"error handling" rust filetype:pdf` ||
		q.Get("include_domains") != "github.com" ||
		q.Get("exclude_domains") != "gitlab.com" ||
		q.Get("location") != "IT" ||
		q.Get("language") != "it" ||
		q.Get("recency_minutes") != "1440" {
		t.Fatalf("unexpected params: %v", q)
	}
	if rt.recorded[0].Headers.Get("X-API-Key") != "tf-key" {
		t.Fatalf("missing X-API-Key")
	}
}

func TestProviderJina(t *testing.T) {
	// upstream: packages/coding-agent/src/web/search/providers/jina.ts
	clearProviderEnv(t)
	t.Setenv("JINA_API_KEY", "jina-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":[{"title":"Jina Doc","url":"https://go.dev/doc","description":"Go docs"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{Query: "slices site:go.dev", Limit: 3})
	if details.Response.Provider != "jina" || len(details.Response.Sources) != 1 || details.Response.Sources[0].Title != "Jina Doc" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if rt.recorded[0].Headers.Get("X-Site") != "go.dev" || rt.recorded[0].Headers.Get("Authorization") != "Bearer jina-key" {
		t.Fatalf("unexpected headers: %+v", rt.recorded[0].Headers)
	}
}

func TestProviderKagi(t *testing.T) {
	// upstream: test/tools/web-search-kagi.test.ts: "parses categorized response with search + video + news + related_search"
	// upstream: test/tools/web-search-kagi.test.ts: "maps auth failures to a compact provider-tagged error"
	clearProviderEnv(t)
	t.Setenv("KAGI_API_KEY", "kagi-key")
	withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"meta":{"trace":"req-kagi-1"},
			"data":{
				"search":[{"url":"https://example.com/a","title":"Article","snippet":"Snippet A","time":"2025-06-01T00:00:00Z"}],
				"video":[{"url":"https://example.com/v","title":"Video","snippet":"Video desc"}],
				"news":[{"url":"https://example.com/n","title":"News","snippet":"News desc"}],
				"related_search":[{"title":"Related 1"}],
				"direct_answer":[{"snippet":"Direct Kagi answer"}]
			}
		}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{Query: "kagi search", Recency: "day"})
	if details.Response.Provider != "kagi" || details.Response.RequestID != "req-kagi-1" || details.Response.Answer != "Direct Kagi answer" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if len(details.Response.Sources) != 3 || details.Response.Sources[1].Title != "[Video] Video" || details.Response.Sources[2].Title != "[News] News" {
		t.Fatalf("unexpected sources: %+v", details.Response.Sources)
	}
	if !reflect.DeepEqual(details.Response.RelatedQuestions, []string{"Related 1"}) {
		t.Fatalf("related = %v", details.Response.RelatedQuestions)
	}

	// Auth error mapping
	withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":[{"code":401,"message":"Invalid API key"}]}`))
	})
	text, _ := ExecuteSearch(context.Background(), SearchParams{Query: "kagi auth fail"})
	if text != "Error: Kagi authorization failed (401). Check API key or base URL." {
		t.Fatalf("unexpected auth error: %q", text)
	}
}

func TestProviderTavily(t *testing.T) {
	// upstream: test/tools/web-search-tavily.test.ts: "maps Tavily responses into SearchResponse and forwards recency filters"
	// upstream: test/tools/web-search-tavily.test.ts: "retries recency-filtered empty responses without time_range"
	clearProviderEnv(t)
	t.Setenv("TAVILY_API_KEY", "tavily-key")
	calls := 0
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"answer":"","request_id":"empty","results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"answer":"Fallback Tavily answer","request_id":"req-2","results":[{"title":"Release","url":"https://example.com/r","content":"Notes"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{Query: "omp release notes", Recency: "month", NumSearchResults: 5})
	if calls != 2 || details.Response.Provider != "tavily" || details.Response.Answer != "Fallback Tavily answer" {
		t.Fatalf("expected retry on empty recency response, calls=%d resp=%+v", calls, details.Response)
	}
	var firstBody, secondBody map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &firstBody)
	_ = json.Unmarshal([]byte(rt.recorded[1].Body), &secondBody)
	if firstBody["time_range"] != "month" {
		t.Fatalf("first call missing time_range: %+v", firstBody)
	}
	if _, has := secondBody["time_range"]; has {
		t.Fatalf("second call should omit time_range: %+v", secondBody)
	}
}

func TestProviderBrave(t *testing.T) {
	// upstream: packages/coding-agent/src/web/search/providers/brave.ts
	clearProviderEnv(t)
	t.Setenv("BRAVE_API_KEY", "brave-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "brave-req-7")
		_, _ = w.Write([]byte(`{"web":{"results":[
			{"title":"<b>Brave</b> Result","url":"https://brave.com/search","description":"Main <em>desc</em>","extra_snippets":["Extra 1","Main desc"],"age":"2025-05-01"}
		]}}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{
		Query:   "privacy after:2024-01-01 before:2025-12-31",
		Recency: "day",
		Limit:   5,
	})
	if details.Response.Provider != "brave" || details.Response.RequestID != "brave-req-7" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if len(details.Response.Sources) != 1 || details.Response.Sources[0].Title != "Brave Result" || details.Response.Sources[0].Snippet != "Main desc\nExtra 1" {
		t.Fatalf("unexpected source: %+v", details.Response.Sources)
	}
	u, _ := url.Parse(rt.recorded[0].OrigURL)
	if u.Query().Get("freshness") != "2024-01-01to2025-12-31" || rt.recorded[0].Headers.Get("X-Subscription-Token") != "brave-key" {
		t.Fatalf("unexpected request: url=%v headers=%v", u.Query(), rt.recorded[0].Headers)
	}
}

func TestProviderDeepSeek(t *testing.T) {
	// upstream: packages/coding-agent/src/web/search/providers/deepseek.ts
	clearProviderEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "ds-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"DS Hit","url":"https://deepseek.com/docs","summary":"Long summary","snippet":"Short","date":"2025-02-02"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{Query: "v3 site:deepseek.com lang:en-us"})
	if details.Response.Provider != "deepseek" || len(details.Response.Sources) != 1 || details.Response.Sources[0].Snippet != "Long summary" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &body)
	if body["query"] != "v3 site:deepseek.com lang:en" {
		t.Fatalf("query = %v", body["query"])
	}
}

func TestProviderKimi(t *testing.T) {
	// upstream: test/tools/web-search-kimi.test.ts: "rebuilds directive queries with Bing-style operators and drops date directives"
	clearProviderEnv(t)
	t.Setenv("KIMI_SEARCH_API_KEY", "kimi-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-msh-request-id", "kimi-req-9")
		_, _ = w.Write([]byte(`{"search_results":[{"title":"Kimi Hit","url":"https://github.com/kimi","snippet":"Kimi snippet","site_name":"GitHub"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{
		Query: `site:github.com intitle:changelog filetype:md after:2024-01-01 "bun runtime" -deprecated`,
	})
	if details.Response.Provider != "kimi" || details.Response.RequestID != "kimi-req-9" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &body)
	sent, _ := body["text_query"].(string)
	for _, want := range []string{"site:github.com", "intitle:changelog", "filetype:md", `"bun runtime"`, "-deprecated"} {
		if !strings.Contains(sent, want) {
			t.Errorf("expected %q in %q", want, sent)
		}
	}
	if strings.Contains(sent, "after:") {
		t.Errorf("date directive should be stripped: %q", sent)
	}
}

func TestProviderSynthetic(t *testing.T) {
	// upstream: packages/coding-agent/src/web/search/providers/synthetic.ts
	clearProviderEnv(t)
	t.Setenv("SYNTHETIC_API_KEY", "syn-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://synthetic.new/1","title":"Syn 1","text":"Body text","published":"2025-03-03"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{Query: "zero retention"})
	if details.Response.Provider != "synthetic" || len(details.Response.Sources) != 1 || details.Response.Sources[0].Snippet != "Body text" {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	if rt.recorded[0].Headers.Get("Authorization") != "Bearer syn-key" {
		t.Fatalf("missing bearer token")
	}
}

func TestProviderOllama(t *testing.T) {
	// upstream: test/web/search/ollama.test.ts: "sends query and max_results in the request body"
	// upstream: test/web/search/ollama.test.ts: "clamps max_results to the documented maximum of 10"
	clearProviderEnv(t)
	t.Setenv("OLLAMA_CLOUD_API_KEY", "ollama-key")
	_, rt := withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Ollama Docs","url":"https://ollama.com/docs","content":"Run models locally"}]}`))
	})

	_, details := ExecuteSearch(context.Background(), SearchParams{Query: "rust async", NumSearchResults: 99})
	if details.Response.Provider != "ollama" || len(details.Response.Sources) != 1 {
		t.Fatalf("unexpected response: %+v", details.Response)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rt.recorded[0].Body), &body)
	if body["max_results"] != float64(10) {
		t.Fatalf("max_results = %v, want 10", body["max_results"])
	}
}

func TestProviderHTTPErrorAndClassification(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("BRAVE_API_KEY", "brave-key")

	t.Run("HTTP 500 surfaces provider error message", func(t *testing.T) {
		withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream exploded"))
		})
		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "boom"})
		if !strings.Contains(text, "Brave API error (500): upstream exploded") {
			t.Fatalf("unexpected error text: %q", text)
		}
		if details.Response.Provider != "brave" {
			t.Fatalf("provider = %q", details.Response.Provider)
		}
	})

	t.Run("credit exhaustion body is classified cleanly", func(t *testing.T) {
		withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"credits exhausted for this billing period"}`))
		})
		text, _ := ExecuteSearch(context.Background(), SearchParams{Query: "quota"})
		if text != "Error: brave: credits exhausted" {
			t.Fatalf("text = %q", text)
		}
	})

	t.Run("context cancellation surfaces immediately without falling through", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		withTestHTTPClient(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("HTTP handler should not run on cancelled context")
		})
		text, _ := ExecuteSearch(ctx, SearchParams{Query: "cancelled"})
		if !strings.Contains(text, context.Canceled.Error()) {
			t.Fatalf("expected context canceled error, got %q", text)
		}
	})
}

func TestReadLimitedText(t *testing.T) {
	// upstream: test/web/search/read-limited-text.test.ts: "decodes a body in the charset its Content-Type declares"
	resp1 := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/plain; charset=windows-1252"}},
		Body:   io.NopCloser(strings.NewReader(string([]byte{0x63, 0x61, 0x66, 0xe9}))),
	}
	if got, err := readLimitedText(resp1, "brave", 1024, false); err != nil || got != "café" {
		t.Fatalf("got (%q, %v), want (café, nil)", got, err)
	}

	// upstream: test/web/search/read-limited-text.test.ts: "drops a UTF-8 byte-order mark so JSON bodies still parse"
	bomBody := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"ok":true}`)...)
	resp2 := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(string(bomBody))),
	}
	got2, err := readLimitedText(resp2, "brave", 1024, false)
	if err != nil || got2 != `{"ok":true}` {
		t.Fatalf("BOM strip failed: (%q, %v)", got2, err)
	}

	// Exceeding limit without allowTruncate errors with SearchProviderError.
	resp3 := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader("0123456789")),
	}
	_, err = readLimitedText(resp3, "brave", 5, false)
	var spErr *SearchProviderError
	if !errors.As(err, &spErr) {
		t.Fatalf("expected SearchProviderError, got %v", err)
	}
}
