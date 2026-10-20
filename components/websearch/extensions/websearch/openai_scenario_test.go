package websearch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The scenario runner of the OpenAI twins: runScenario of test/openai-alpha-search.test.mjs, the
// `runSearch` of openai-search-opencode-headers.test.mjs and the inline child of
// openai-origin.test.mjs, with `globalThis.fetch` replaced by a doer that sees the request
// context (hangs and aborts are real context cancellations here).

// absent marks a config key that is left out (the upstream `undefined`).
type absent struct{}

type oaScenario struct {
	// baseConfig replaces the default alpha config; config is merged over it.
	baseConfig map[string]any
	config     map[string]any

	payload any
	raw     *string
	status  int

	registry, current, standalone bool
	model                         map[string]any
	missingBaseURL                bool
	models                        []map[string]any
	authByProvider                map[string]map[string]any
	key                           *string
	headers                       map[string]any
	authBaseURL                   string
	sessionID                     string

	options SearchOptions

	route, tool, availability bool
	calls                     int
	nextBaseURL               string

	networkError   bool
	hang, bodyHang string
	preAbort       bool

	redirectURL    string
	redirectStatus int
}

type oaRequest struct {
	URL    string
	Method string
	Header http.Header
	Body   map[string]any
}

type oaOut struct {
	requests      []oaRequest
	authProviders []string
	result        any
	err           error
	kind          string
	// tool run
	text, retrieved string
	searchID        string
	isError         bool
	stored          [][]SearchResult
}

func strp(s string) *string { return &s }

var (
	oaSource = func(u, title string, snippet any) map[string]any {
		return map[string]any{"type": "text_result", "url": u, "title": title, "snippet": snippet}
	}
	alphaPayload  = map[string]any{"output": "Plain search output", "results": []any{oaSource("https://example.com/a", "Source", "Evidence")}}
	legacyPayload = map[string]any{"output": []any{
		map[string]any{"type": "web_search_call"},
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Responses answer"}}},
	}}
	searchRoute = `{"providers":["openai","tavily"],"fallbackOn":["unsupported","transient","quota","network","invalid-response"]}`
)

func oaConfigJSON(sc *oaScenario) string {
	cfg := map[string]any{"openaiApiKey": "alpha-test-key", "openaiUseAlphaSearch": true}
	if sc.baseConfig != nil {
		cfg = map[string]any{}
		for k, v := range sc.baseConfig {
			cfg[k] = v
		}
	}
	for k, v := range sc.config {
		if _, drop := v.(absent); drop {
			delete(cfg, k)
		} else {
			cfg[k] = v
		}
	}
	return string(mustJSON(cfg))
}

// blockingBody is a body that never delivers data: a read returns when the request is done.
type blockingBody struct {
	ctx      context.Context
	onStart  func()
	once     sync.Once
	finished bool
}

func (b *blockingBody) Read([]byte) (int, error) {
	b.once.Do(func() {
		if b.onStart != nil {
			b.onStart()
		}
	})
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *blockingBody) Close() error { return nil }

func runOpenAI(t *testing.T, sc oaScenario) oaOut {
	t.Helper()
	var out oaOut
	config := oaConfigJSON(&sc)
	var rt *Runtime
	if sc.tool {
		rt, _ = started(t, config)
	} else {
		_, dir := isolate(t)
		writeConfig(t, dir, config)
	}

	base := context.WithValue(bg(), struct{ k string }{"scenario"}, true)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	if sc.preAbort {
		cancel()
	}
	if sc.hang == "timeout" || sc.bodyHang == "timeout" {
		prev := openAISearchTimeout
		openAISearchTimeout = 20 * time.Millisecond
		t.Cleanup(func() { openAISearchTimeout = prev })
	}

	mdl := map[string]any{"id": "gpt-5.6-luna", "provider": "openai", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"}
	for k, v := range sc.model {
		mdl[k] = v
	}
	if sc.missingBaseURL {
		delete(mdl, "baseUrl")
	}
	var ec *fakeExt
	if (sc.registry || sc.current) && !sc.standalone {
		models := sc.models
		if models == nil {
			models = []map[string]any{mdl}
		}
		reg := &fakeRegistry{models: models}
		reg.auth = func(m map[string]any) (map[string]any, error) {
			p := jsString(m["provider"])
			if a, ok := sc.authByProvider[p]; ok {
				return a, nil
			}
			key := "registry-test-key"
			if sc.key != nil {
				key = *sc.key
			}
			headers := sc.headers
			if headers == nil {
				headers = map[string]any{}
			}
			auth := map[string]any{"ok": true, "apiKey": key, "headers": headers}
			if sc.authBaseURL != "" {
				auth["baseUrl"] = sc.authBaseURL
			}
			return auth, nil
		}
		ec = &fakeExt{model: mdl, reg: reg, session: sc.sessionID}
	}
	if ec != nil {
		ctx = WithExtensionContext(ctx, ec)
	}

	var mu sync.Mutex
	t.Cleanup(SetHTTP(doerFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		req := oaRequest{URL: r.URL.String(), Method: r.Method, Header: r.Header.Clone()}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &req.Body)
		}
		mu.Lock()
		out.requests = append(out.requests, req)
		n := len(out.requests)
		mu.Unlock()
		respond := func(status int, body string, h http.Header) *http.Response {
			if h == nil {
				h = http.Header{"Content-Type": {"application/json"}}
			}
			return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}
		}
		switch {
		case strings.Contains(req.URL, "api.tavily.com"):
			return respond(200, `{"results":[{"title":"Fallback","url":"https://fallback.example/a","content":"Fallback evidence"}]}`, nil), nil
		case sc.hang != "":
			if sc.hang == "abort" {
				go func() { time.Sleep(100 * time.Millisecond); cancel() }()
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		case sc.networkError:
			return nil, errors.New("fetch failed")
		case sc.bodyHang != "":
			body := &blockingBody{ctx: r.Context()}
			if sc.bodyHang == "abort" {
				body.onStart = func() { go func() { time.Sleep(20 * time.Millisecond); cancel() }() }
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body, Request: r}, nil
		case sc.redirectURL != "" && n == 1:
			return respond(sc.redirectStatus, "", http.Header{"Location": {sc.redirectURL}}), nil
		}
		status := sc.status
		if status == 0 {
			status = 200
		}
		payload := sc.payload
		if payload == nil {
			payload = alphaPayload
		}
		body := string(mustJSON(payload))
		if sc.raw != nil {
			body = *sc.raw
		}
		return respond(status, body, nil), nil
	})))

	opts := sc.options
	switch {
	case sc.tool:
		params := map[string]any{"queries": []any{"test query", "second query"}, "provider": "openai", "workflow": "none"}
		if opts.NumResults != nil {
			params["numResults"] = *opts.NumResults
		}
		res, err := mustTool(t, rt, "web_search").Execute(ctx, params, nil)
		if err != nil {
			out.err = err
			return out
		}
		out.text, out.searchID, out.isError = res.Text(), dStr(res, "searchId"), res.IsError
		got, err := mustTool(t, rt, "get_search_content").Execute(ctx, map[string]any{"responseId": out.searchID, "queryIndex": 1.0}, nil)
		if err != nil {
			out.err = err
			return out
		}
		out.retrieved = got.Text()
		out.isError = out.isError || got.IsError
		if stored := GetResult(out.searchID); stored != nil {
			for _, q := range stored.Queries {
				out.stored = append(out.stored, q.Results)
			}
		}
	case sc.route:
		r, err := Search(ctx, "test query", FullSearchOptions{SearchOptions: opts})
		out.result, out.err = r, err
	case sc.availability:
		var ext ExtensionContext
		if ec != nil {
			ext = ec
		}
		out.result, out.err = IsOpenAISearchAvailable(ext)
	default:
		calls := sc.calls
		if calls == 0 {
			calls = 1
		}
		for i := 0; i < calls; i++ {
			if i > 0 && sc.nextBaseURL != "" {
				mdl["baseUrl"] = sc.nextBaseURL
			}
			var r *SearchResponse
			var err error
			if sc.current {
				r, err = SearchWithCurrentModelOpenAI(ctx, "test query", opts)
			} else {
				r, err = SearchWithOpenAI(ctx, "test query", opts)
			}
			out.result, out.err = r, err
			if err != nil {
				break
			}
		}
	}
	if ec != nil {
		out.authProviders = nil
		for _, s := range ec.reg.selected {
			out.authProviders = append(out.authProviders, s[:strings.Index(s, "/")])
		}
	}
	var pe *SearchProviderError
	if errors.As(out.err, &pe) {
		out.kind = pe.Kind
	}
	return out
}

func (o oaOut) searchResponse(t *testing.T) *SearchResponse {
	t.Helper()
	switch r := o.result.(type) {
	case *SearchResponse:
		return r
	case *AttributedSearchResponse:
		return &r.SearchResponse
	}
	t.Fatalf("result %T, err %v", o.result, o.err)
	return nil
}

func (o oaOut) urls() []string {
	var out []string
	for _, r := range o.requests {
		out = append(out, r.URL)
	}
	return out
}

func (o oaOut) mustOK(t *testing.T) {
	t.Helper()
	if o.err != nil {
		t.Fatalf("unexpected error: %v", o.err)
	}
}

func (o oaOut) wantNoRequests(t *testing.T) {
	t.Helper()
	if len(o.requests) != 0 {
		t.Fatalf("requests were sent: %v", o.urls())
	}
}

// codexToken is the upstream key: a JWT whose payload names a ChatGPT account.
func codexToken() string {
	payload, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "test-account"}})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func headerOf(r oaRequest, name string) string { return r.Header.Get(name) }

func hasHeader(r oaRequest, name string) bool {
	_, ok := r.Header[http.CanonicalHeaderKey(name)]
	return ok
}
