package websearch

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The oracle fixture: testdata/provider-requests.json (kept inside the extension tree so mutation
// runs, which copy only the tree, replay it too) is written by port/record-provider-requests.mjs
// from the ORIGINAL provider modules (what they sent to a recording fetch and what they returned
// or threw for the same inputs). Every case is replayed here against the port with the same canned
// responses; requests (method, URL, headers, body) and outcomes must be identical.
//
// Compared loosely, on purpose: the `redirect` mode of a request (a Node fetch option with no Go
// counterpart: the Go transport never follows redirects on its own), and the text a JSON parser
// puts after "invalid JSON:" (the parser message of Node and Go differ).

type fixtureRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
}

type fixtureResponse struct {
	Status  int               `json:"status"`
	JSON    any               `json:"json"`
	Body    *string           `json:"body"`
	Headers map[string]string `json:"headers"`
	Throw   string            `json:"throw"`
	Message string            `json:"message"`
	// BodyThrow makes reading the body fail after the headers arrived: "timeout" blocks until the
	// request's own deadline (providerTimeoutOverride makes it short), "abort" cancels the caller
	// while the body is read, "terminated" cuts the connection.
	BodyThrow string `json:"bodyThrow"`
}

type fixtureCase struct {
	ID         string            `json:"id"`
	Provider   string            `json:"provider"`
	Fn         string            `json:"fn"`
	Config     any               `json:"config"`
	ConfigRaw  *string           `json:"configRaw"`
	Env        map[string]string `json:"env"`
	Query      string            `json:"query"`
	Options    map[string]any    `json:"options"`
	Responses  []fixtureResponse `json:"responses"`
	Baizhi     string            `json:"baizhi"`
	PreAborted bool              `json:"preAborted"`
	// CallerAbortsBody: the caller's context is cancelled while the body is read (bodyThrow "abort").
	CallerAbortsBody bool             `json:"callerAbortsBody"`
	Now              string           `json:"now"`
	Unordered        bool             `json:"unordered"`
	Requests         []fixtureRequest `json:"requests"`
	Activity         []oracleActivity `json:"activity"`
	Outcome          struct {
		Result any    `json:"result"`
		Error  string `json:"error"`
	} `json:"outcome"`
	hasError bool
}

var (
	fixtureOnce  []fixtureCase
	fixtureIndex map[string]*fixtureCase
)

func loadFixtures(t testing.TB) map[string]*fixtureCase {
	t.Helper()
	if fixtureIndex != nil {
		return fixtureIndex
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "provider-requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixtureOnce); err != nil {
		t.Fatal(err)
	}
	// An outcome with an "error" key is a failure even when the message is empty.
	var shape []struct {
		Outcome map[string]json.RawMessage `json:"outcome"`
	}
	_ = json.Unmarshal(raw, &shape)
	fixtureIndex = map[string]*fixtureCase{}
	for i := range fixtureOnce {
		_, fixtureOnce[i].hasError = shape[i].Outcome["error"]
		fixtureIndex[fixtureOnce[i].ID] = &fixtureOnce[i]
	}
	return fixtureIndex
}

// fixtureDoer answers the canned responses of one case and records every request, over both
// transports a provider can use (API calls and the SSRF-guarded page fetch).
type fixtureDoer struct {
	c        *fixtureCase
	requests []fixtureRequest
	n        int
	// brave, when set, answers the Brave API (routing tests that fall through to it); cancel
	// is the caller's cancellation, fired by the "abort" Baizhi mode.
	brave  string
	cancel context.CancelFunc
	mu     sync.Mutex
}

type brokenBody struct{ err error }

func (b brokenBody) Read([]byte) (int, error) { return 0, b.err }
func (b brokenBody) Close() error             { return nil }

// stalledBody is a body that never delivers a byte: a read returns when the request's context ends,
// with the context's error, as net/http's body does once a deadline or a cancellation fires.
// cancel, when set, is the caller's cancellation, fired on the first read.
type stalledBody struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (b stalledBody) Read([]byte) (int, error) {
	if b.cancel != nil {
		b.cancel()
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b stalledBody) Close() error { return nil }

func (d *fixtureDoer) serve(ctx context.Context, method, rawURL string, h http.Header, body []byte) (*http.Response, error) {
	hdr := map[string]string{}
	for k, v := range h {
		hdr[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	var parsed any
	if len(body) > 0 {
		if json.Unmarshal(body, &parsed) != nil {
			parsed = string(body)
		}
	}
	if method == "" {
		method = "GET"
	}
	d.mu.Lock()
	d.requests = append(d.requests, fixtureRequest{Method: method, URL: rawURL, Headers: hdr, Body: parsed})
	n := d.n
	d.n++
	d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.c.Baizhi != "" {
		return d.baizhi(method, hdr, body)
	}
	spec := d.c.Responses[min(n, len(d.c.Responses)-1)]
	switch spec.Throw {
	case "timeout":
		return nil, context.DeadlineExceeded
	case "abort":
		return nil, context.Canceled
	case "network":
		return nil, errors.New("fetch failed")
	case "plain":
		return nil, errors.New(spec.Message)
	}
	resp := &http.Response{StatusCode: spec.Status, Header: http.Header{}}
	for k, v := range spec.Headers {
		resp.Header.Set(k, v)
	}
	switch {
	case spec.BodyThrow == "timeout":
		resp.StatusCode = cmp.Or(spec.Status, 200)
		resp.Body = stalledBody{ctx: ctx}
	case spec.BodyThrow == "abort":
		resp.StatusCode = 200
		resp.Body = stalledBody{ctx: ctx, cancel: d.cancel}
	case spec.BodyThrow == "terminated":
		resp.StatusCode = 200
		resp.Body = brokenBody{io.ErrUnexpectedEOF}
	case spec.JSON != nil:
		b, _ := json.Marshal(spec.JSON)
		resp.Header.Set("Content-Type", "application/json")
		resp.Body = io.NopCloser(strings.NewReader(string(b)))
	case spec.Body != nil:
		resp.Body = io.NopCloser(strings.NewReader(*spec.Body))
	default:
		resp.Body = io.NopCloser(strings.NewReader(""))
	}
	return resp, nil
}

func (d *fixtureDoer) Do(r *http.Request) (*http.Response, error) {
	if d.brave != "" && r.URL.Hostname() == "api.search.brave.com" {
		d.mu.Lock()
		d.requests = append(d.requests, fixtureRequest{Method: r.Method, URL: r.URL.String()})
		d.mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(d.brave)), Request: r}, nil
	}
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	resp, err := d.serve(r.Context(), r.Method, r.URL.String(), r.Header, body)
	if resp != nil {
		resp.Request = r
	}
	return resp, err
}

func (d *fixtureDoer) page(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
	return d.serve(ctx, init.Method, u.String(), init.Header, init.Body)
}

// baizhi plays the MCP server of the upstream test (baizhi-provider.test.mjs).
func (d *fixtureDoer) baizhi(method string, h map[string]string, body []byte) (*http.Response, error) {
	mode := d.c.Baizhi
	key := "synthetic-baizhi-credential"
	if v, ok := d.c.Env["SYNTHETIC_BAIZHI_NAMED"]; ok {
		key = v
	} else if v, ok := d.c.Env["BAIZHI_API_KEY"]; ok {
		key = v
	}
	resp := func(status int, ctype string, text string, extra map[string]string) (*http.Response, error) {
		r := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(text))}
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		for k, v := range extra {
			r.Header.Set(k, v)
		}
		return r, nil
	}
	if mode == "network" {
		return nil, errors.New("fetch failed")
	}
	if mode == "abort" {
		d.cancel()
		return nil, errors.New("private abort " + key)
	}
	auth := h["authorization"] == "Bearer "+key
	if mode == "http401" || (!auth && mode != "json") {
		return resp(401, "", "bad key "+key, nil)
	}
	switch method {
	case "GET":
		return resp(405, "", "", nil)
	case "DELETE":
		return resp(200, "", "", nil)
	}
	switch mode {
	case "redirect":
		return resp(307, "", "", map[string]string{"Location": "https://untrusted.example/mcp"})
	}
	if status, ok := strings.CutPrefix(mode, "http"); ok {
		code, _ := strconv.Atoi(status)
		return resp(code, "", "private body "+key, nil)
	}
	var rpc struct {
		Method string          `json:"method"`
		ID     *int            `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &rpc)
	jsonType := "application/json"
	if rpc.Method == "initialize" {
		if mode == "invalid-json" {
			return resp(200, jsonType, "not json", nil)
		}
		return resp(200, jsonType, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"synthetic-only","version":"1.0.0"}}}`, *rpc.ID), map[string]string{"Mcp-Session-Id": "synthetic-session"})
	}
	if rpc.ID == nil {
		return resp(202, "", "", nil)
	}
	payload := `{"status":"success","results":[{"unknown_service_field":"opaque search result; https://example.com/reference"}]}`
	quoted, _ := json.Marshal(payload)
	var result string
	switch mode {
	case "empty":
		result = `{"content":[]}`
	case "tool-error":
		result = fmt.Sprintf(`{"content":[{"type":"text","text":"secret %s"}],"isError":true}`, key)
	case "text":
		result = fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`, quoted)
	case "redact":
		result = fmt.Sprintf(`{"content":[{"type":"text","text":%q}],"structuredContent":{"echoed":%q}}`, key, key)
	default:
		result = fmt.Sprintf(`{"content":[],"structuredContent":%s}`, payload)
	}
	message := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *rpc.ID, result)
	if mode == "sse" {
		return resp(200, "text/event-stream", "event: message\ndata: "+message+"\n\n", nil)
	}
	return resp(200, jsonType, message, nil)
}

var (
	parseTailRE  = regexp.MustCompile(`(invalid JSON|not valid JSON): ([\s\S]*)$`)
	configTailRE = regexp.MustCompile(`(Failed to parse <CONFIG>): [\s\S]*$`)
)

// bodyReadTexts are what a body that could not be read at all says after "invalid JSON:" (the
// deadline, the caller's abort, a dropped connection); every other text there is a parser's.
var bodyReadTexts = map[string]bool{"The operation was aborted due to timeout": true, "This operation was aborted": true, "terminated": true}

func normalizeFixtureText(s string) string {
	s = strings.ReplaceAll(s, ConfigPath(), "<CONFIG>")
	s = parseTailRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := parseTailRE.FindStringSubmatch(m)
		if bodyReadTexts[sub[2]] {
			return m
		}
		return sub[1]
	})
	return configTailRE.ReplaceAllString(s, "$1")
}

func fixtureSearchOptions(t *testing.T, o map[string]any) SearchOptions {
	var so SearchOptions
	for k, v := range o {
		switch k {
		case "numResults":
			f := v.(float64)
			so.NumResults = &f
		case "recencyFilter":
			so.RecencyFilter = v.(string)
		case "includeContent":
			so.IncludeContent = v.(bool)
		case "domainFilter":
			for _, e := range v.([]any) {
				so.DomainFilter = append(so.DomainFilter, e.(string))
			}
		default:
			t.Fatalf("unknown option %s", k)
		}
	}
	return so
}

var fixtureFns = map[string]func(context.Context, string, SearchOptions) (*SearchResponse, error){
	"searchWithSerper":     SearchWithSerper,
	"searchWithSerpApi":    SearchWithSerpAPI,
	"searchWithSerpBase":   SearchWithSerpBase,
	"searchWithSerply":     SearchWithSerply,
	"searchWithValyu":      SearchWithValyu,
	"searchWithBocha":      SearchWithBocha,
	"searchWithBaizhi":     SearchWithBaizhi,
	"searchWithYou":        SearchWithYou,
	"searchWithAnySearch":  SearchWithAnySearch,
	"searchWithXCrawl":     SearchWithXCrawl,
	"searchWithJina":       SearchWithJina,
	"searchWithSearXNG":    SearchWithSearXNG,
	"searchWithBrave":      SearchWithBrave,
	"searchWithPerplexity": SearchWithPerplexity,
	"searchWithSerpdive":   SearchWithSerpdive,
	"searchWithDuckDuckGo": SearchWithDuckDuckGo,
	"searchWithTavily":     SearchWithTavily,
	"searchWithExa":        SearchWithExa,
	"searchWithKagi":       SearchWithKagi,
	"searchWithOllama":     SearchWithOllama,
}

// runFixture replays one case against the port and returns what it sent and what it produced.
func runFixture(t *testing.T, c *fixtureCase) (*fixtureDoer, *SearchResponse, error) {
	t.Helper()
	_, agentDir := isolate(t)
	unsetenv(t, "SERPAPI_KEY", "YDC_API_KEY", "XCRAWL_API_KEY", "SYNTHETIC_BAIZHI_NAMED", "EXA_API_KEY", "BRAVE_API_KEY", "PERPLEXITY_API_KEY", "SERPDIVE_API_KEY", "TAVILY_API_KEY", "KAGI_API_KEY", "OLLAMA_API_KEY")
	if c.ConfigRaw != nil {
		writeConfig(t, agentDir, *c.ConfigRaw)
	} else if c.Config != nil {
		b, _ := json.Marshal(c.Config)
		writeConfig(t, agentDir, string(b))
	}
	for k, v := range c.Env {
		t.Setenv(k, v)
	}
	if c.Now != "" {
		now, err := time.Parse(time.RFC3339Nano, c.Now)
		if err != nil {
			t.Fatal(err)
		}
		prev := restNow
		restNow = func() time.Time { return now }
		t.Cleanup(func() { restNow = prev })
	}
	d := &fixtureDoer{c: c}
	t.Cleanup(SetHTTP(d))
	t.Cleanup(SetPageFetch(d.page))
	ctx := context.Background()
	if c.PreAborted {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	if c.CallerAbortsBody {
		ctx, d.cancel = context.WithCancel(ctx)
		defer d.cancel()
	}
	for _, spec := range c.Responses {
		if spec.BodyThrow == "timeout" {
			prev := providerTimeoutOverride
			providerTimeoutOverride = 30 * time.Millisecond
			defer func() { providerTimeoutOverride = prev }()
			break
		}
	}
	Activity.Clear()
	res, err := fixtureFns[c.Fn](ctx, c.Query, fixtureSearchOptions(t, c.Options))
	return d, res, err
}

func canonicalRequests(reqs []fixtureRequest, unordered bool) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		// Accept-Encoding: the original names gzip itself (brave.ts); the Go transport negotiates it and
		// decodes the body, so a request that names it would receive undecoded bytes.
		if _, ok := r.Headers["accept-encoding"]; ok {
			h := map[string]string{}
			for k, v := range r.Headers {
				if k != "accept-encoding" {
					h[k] = v
				}
			}
			r.Headers = h
		}
		b, _ := json.Marshal(r)
		out[i] = string(b)
	}
	if unordered {
		sort.Strings(out)
	}
	return out
}

// checkFixture requires the port to match the oracle for one case.
func checkFixture(t *testing.T, c *fixtureCase) {
	t.Helper()
	d, res, err := runFixture(t, c)
	// Requests: through JSON so numbers and nesting compare as the fixture holds them.
	var got []fixtureRequest
	b, _ := json.Marshal(d.requests)
	_ = json.Unmarshal(b, &got)
	want, have := canonicalRequests(c.Requests, c.Unordered), canonicalRequests(got, c.Unordered)
	if !reflect.DeepEqual(want, have) {
		t.Fatalf("%s: requests differ\noriginal:\n  %s\nport:\n  %s", c.ID, strings.Join(want, "\n  "), strings.Join(have, "\n  "))
	}
	requireActivity(t, c.ID, c.Activity)
	if c.hasError {
		if err == nil {
			t.Fatalf("%s: the original failed with %q, the port returned a result", c.ID, c.Outcome.Error)
		}
		if got := normalizeFixtureText(err.Error()); got != c.Outcome.Error {
			t.Fatalf("%s: error\noriginal: %q\nport:     %q", c.ID, c.Outcome.Error, got)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: the original returned a result, the port failed: %v", c.ID, err)
	}
	var gotResult any
	rb, _ := json.Marshal(res)
	_ = json.Unmarshal(rb, &gotResult)
	if !reflect.DeepEqual(gotResult, c.Outcome.Result) {
		wb, _ := json.MarshalIndent(c.Outcome.Result, "", " ")
		gb, _ := json.MarshalIndent(gotResult, "", " ")
		t.Fatalf("%s: result differs\noriginal: %s\nport:     %s", c.ID, wb, gb)
	}
}

// fixtureTw runs the named fixture cases as one twin.
func fixtureCases(t *testing.T, ids ...string) {
	t.Helper()
	idx := loadFixtures(t)
	for _, id := range ids {
		c := idx[id]
		if c == nil {
			t.Fatalf("no fixture case %q", id)
		}
		t.Run(id, func(t *testing.T) { checkFixture(t, c) })
	}
}

// TestOracleProviderFixtures replays every recorded case of the original providers.
func TestOracleProviderFixtures(t *testing.T) {
	for _, c := range loadFixtures(t) {
		c := c
		t.Run(c.ID, func(t *testing.T) { checkFixture(t, c) })
	}
}
