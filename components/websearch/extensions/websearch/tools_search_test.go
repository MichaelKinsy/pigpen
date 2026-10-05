package websearch

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of the web_search tool tests. The upstream fixtures use OpenAI, XCrawl and AnySearch,
// which this port does not have; the behaviour under test (identifiers, query expansion,
// concurrency, output bounds) does not depend on the provider, so the adapted cases use Brave
// or Tavily and say so.

func braveBody(results ...[3]string) string {
	var items []map[string]string
	for _, r := range results {
		items = append(items, map[string]string{"title": r[0], "url": r[1], "description": r[2]})
	}
	b, _ := json.Marshal(map[string]any{"web": map[string]any{"results": items}})
	return string(b)
}

func tavilyBody(answer string, results ...map[string]string) string {
	b, _ := json.Marshal(map[string]any{"answer": answer, "results": results})
	return string(b)
}

func started(t *testing.T, config string) (*Runtime, *fakeHost) {
	t.Helper()
	r, h := newRuntime(t, config)
	r.SessionStarted(nil)
	t.Cleanup(func() { r.SessionShutdown(); r.WaitBackground() })
	return r, h
}

var responseIDRE = regexp.MustCompile(`responseId "([^"]+)"`)

func TestUpstream_web_search_response_id(t *testing.T) {
	const F = "web-search-response-id"
	const note = "provider tavily instead of openai (not ported); the identifier flow is provider-independent"
	type out struct {
		text       string
		details    map[string]any
		searchID   string
		retrieved  ToolOutput
		hasRetrive bool
	}
	scenario := func(t *testing.T, config string) out {
		r, _ := started(t, config)
		t.Setenv("TAVILY_API_KEY", "response-id-test-key")
		useNet(t, func(c netCall) netReply {
			if c.URL != "https://api.tavily.com/search" {
				return netReply{Err: fmt.Errorf("Unexpected fetch: %s", c.URL)}
			}
			return reply(200, tavilyBody("Search answer", map[string]string{"title": "Source", "url": "https://example.com/source", "content": "snippet"}))
		})
		res := run(t, mustTool(t, r, "web_search"), map[string]any{"query": "response id", "provider": "tavily", "workflow": "none"})
		o := out{text: res.Text(), details: res.Details, searchID: dStr(res, "searchId")}
		if get := r.Tool("get_search_content"); get != nil {
			o.hasRetrive = true
			m := responseIDRE.FindStringSubmatch(o.text)
			if m == nil {
				t.Fatalf("no responseId in %q", o.text)
			}
			o.retrieved = run(t, get, map[string]any{"responseId": m[1], "queryIndex": 0.0})
		}
		return o
	}
	tw(t, F, "web_search output tells the model the responseId that get_search_content accepts", func(t *testing.T) {
		adapted(t, note)
		o := scenario(t, `{"provider":"tavily"}`)
		if !o.hasRetrive {
			t.Fatal("no retrieval tool")
		}
		matchRE(t, `Full search results are stored as responseId "[a-z0-9]+"\. Use get_search_content\(\{ responseId: "[a-z0-9]+", queryIndex: 0, offset: 0, limit: 30000 \}\)`, o.text)
		matchRE(t, `Provider:\*\* tavily`, o.text)
		qp, _ := json.Marshal(o.details["queryProviders"])
		if string(qp) != `[{"providers":["tavily"],"query":"response id"}]` {
			t.Fatal(string(qp))
		}
		if o.details["truncated"] != false || o.details["omittedChars"] != 0 {
			t.Fatalf("%+v", o.details)
		}
		if responseIDRE.FindStringSubmatch(o.text)[1] != o.searchID {
			t.Fatal("printed id must be the stored searchId")
		}
		if o.retrieved.IsError || !regexp.MustCompile(`Search answer|example\.com/source`).MatchString(o.retrieved.Text()) {
			t.Fatal(o.retrieved.Text())
		}
	})
	tw(t, F, "web_search output honours a renamed get_search_content tool", func(t *testing.T) {
		adapted(t, note)
		o := scenario(t, `{"provider":"tavily","toolNames":{"getSearchContent":"grab_content"}}`)
		matchRE(t, `Use grab_content\(\{ responseId: "`, o.text)
		noMatchRE(t, `get_search_content\(`, o.text)
	})
	tw(t, F, "web_search output omits the retrieval hint when get_search_content is disabled", func(t *testing.T) {
		adapted(t, note)
		o := scenario(t, `{"provider":"tavily","tools":{"getSearchContent":{"enabled":false}}}`)
		if o.hasRetrive {
			t.Fatal("retrieval tool registered")
		}
		noMatchRE(t, `responseId`, o.text)
		if o.searchID == "" {
			t.Fatal("results are still stored in details")
		}
	})
}

func TestUpstream_web_search_query_expand(t *testing.T) {
	const F = "web-search-query-expand"
	const note = "provider brave instead of xcrawl (not ported); query expansion is provider-independent"
	search := func(t *testing.T, params map[string]any) (ToolOutput, []string) {
		r, _ := started(t, `{}`)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		var mu sync.Mutex
		var queries []string
		useNet(t, func(c netCall) netReply {
			mu.Lock()
			queries = append(queries, query(c.URL, "q"))
			mu.Unlock()
			return reply(200, braveBody([3]string{"R1", "https://example.com/1", "s1"}, [3]string{"R2", "https://example.com/2", "s2"}))
		})
		params["provider"], params["workflow"], params["numResults"] = "brave", "none", 2.0
		res := run(t, mustTool(t, r, "web_search"), params)
		mu.Lock()
		defer mu.Unlock()
		return res, append([]string(nil), queries...)
	}
	sorted := func(q []string) []string { c := append([]string(nil), q...); sortStrings(c); return c }
	tw(t, F, "web_search expands a JSON-array string in query into separate searches", func(t *testing.T) {
		adapted(t, note)
		_, q := search(t, map[string]any{"query": `["AGP version", "Compose BOM", "CameraX"]`})
		if !reflect.DeepEqual(sorted(q), sorted([]string{"AGP version", "Compose BOM", "CameraX"})) {
			t.Fatal(q)
		}
	})
	tw(t, F, "web_search keeps mixed JSON arrays in query as a single literal query", func(t *testing.T) {
		adapted(t, note)
		_, q := search(t, map[string]any{"query": `["AGP version", 5]`})
		if !reflect.DeepEqual(q, []string{`["AGP version", 5]`}) {
			t.Fatal(q)
		}
	})
	tw(t, F, "web_search does not reinterpret already-structured queries entries", func(t *testing.T) {
		adapted(t, note)
		_, q := search(t, map[string]any{"queries": []any{`["AGP version", "Compose BOM"]`}})
		if !reflect.DeepEqual(q, []string{`["AGP version", "Compose BOM"]`}) {
			t.Fatal(q)
		}
	})
	tw(t, F, "web_search reports the existing no-query error for empty JSON-array strings", func(t *testing.T) {
		adapted(t, note)
		for _, query := range []string{"[]", `["   "]`} {
			res, q := search(t, map[string]any{"query": query})
			if len(q) != 0 || dStr(res, "error") != "No query provided" || res.Text() != "Error: No query provided. Use 'query' or 'queries' parameter." {
				t.Fatalf("%q %+v", q, res)
			}
		}
	})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestUpstream_web_search_concurrency(t *testing.T) {
	tw(t, "web-search-concurrency", "web_search bounds batch concurrency and preserves query order", func(t *testing.T) {
		adapted(t, "provider brave instead of xcrawl (not ported); the summary-review half runs the curator, a named gap")
		r, _ := started(t, `{}`)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		delays := map[string]time.Duration{"q1": 90, "q2": 70, "q3": 50, "q4": 30, "q5": 10}
		var active, maxActive int32
		var mu sync.Mutex
		var startedQ, completedQ []string
		useNet(t, func(c netCall) netReply {
			q := query(c.URL, "q")
			mu.Lock()
			startedQ = append(startedQ, q)
			mu.Unlock()
			n := atomic.AddInt32(&active, 1)
			for {
				m := atomic.LoadInt32(&maxActive)
				if n <= m || atomic.CompareAndSwapInt32(&maxActive, m, n) {
					break
				}
			}
			time.Sleep(delays[q] * time.Millisecond)
			atomic.AddInt32(&active, -1)
			mu.Lock()
			completedQ = append(completedQ, q)
			mu.Unlock()
			return reply(200, braveBody([3]string{q, "https://example.com/" + q, q}))
		})
		var updates []map[string]any
		var um sync.Mutex
		res, err := mustTool(t, r, "web_search").Execute(bg(), map[string]any{"queries": []any{"q1", "q2", "q3", "q4", "q5"}, "provider": "brave", "workflow": "none"},
			func(o ToolOutput) { um.Lock(); updates = append(updates, o.Details); um.Unlock() })
		noErr(t, err)
		if atomic.LoadInt32(&maxActive) != 3 {
			t.Fatalf("maxActive = %d", maxActive)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(startedQ) != 5 || reflect.DeepEqual(completedQ, startedQ) {
			t.Fatalf("started %v completed %v", startedQ, completedQ)
		}
		previous := -1
		for _, q := range []string{"q1", "q2", "q3", "q4", "q5"} {
			pos := strings.Index(res.Text(), `## Query: "`+q+`"`)
			if pos <= previous {
				t.Fatalf("%s returned out of order", q)
			}
			previous = pos
		}
		um.Lock()
		last := updates[len(updates)-1]["progress"]
		um.Unlock()
		if last != 1.0 {
			t.Fatalf("last progress %v", last)
		}
		matchRE(t, `concurrently \(up to three at a time\)`, prop(t, mustTool(t, r, "web_search"), "queries")["description"].(string))
	})
}

func TestUpstream_web_search_answer_render(t *testing.T) {
	tw(t, "web-search-answer-render", "web_search preserves OpenAI answers even when no sources are returned", func(t *testing.T) {
		adapted(t, "provider tavily instead of openai (not ported)")
		r, _ := started(t, `{}`)
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		useNet(t, func(netCall) netReply { return reply(200, tavilyBody("Direct answer without citations.")) })
		res := run(t, mustTool(t, r, "web_search"), map[string]any{"query": "answer only", "provider": "tavily", "workflow": "none"})
		matchRE(t, `Direct answer without citations\.`, res.Text())
		matchRE(t, `No sources returned\.`, res.Text())
		noMatchRE(t, `No results found`, res.Text())
		if res.Details["successfulQueries"] != 1 || res.Details["totalResults"] != 0 {
			t.Fatalf("%+v", res.Details)
		}
	})
}

func TestUpstream_web_search_output_boundary(t *testing.T) {
	const F = "web-search-output-boundary"
	const note = "provider tavily (answer text passes through) instead of openai/anysearch (not ported)"
	const defaultCap, largeAnswer = 30000, 116000
	type scenario struct {
		text, page1Text, page2Text, foundText string
		details, page1, page2, found          map[string]any
		requestCount                          int
		storedAnswers                         []int
		storedLate                            bool
		hasRetrieve                           bool
	}
	bigAnswer := strings.Repeat("A", largeAnswer-11) + "LATE_ANSWER"
	run1 := func(t *testing.T, config string) scenario {
		r, h := started(t, config)
		t.Setenv("TAVILY_API_KEY", "boundary-test-key")
		var count int32
		useNet(t, func(c netCall) netReply {
			if c.URL != "https://api.tavily.com/search" {
				return netReply{Err: fmt.Errorf("Unexpected fetch: %s", c.URL)}
			}
			n := atomic.AddInt32(&count, 1)
			return reply(200, tavilyBody(bigAnswer, map[string]string{"title": fmt.Sprintf("Source %d", n), "url": fmt.Sprintf("https://example.com/source-%d", n), "content": "c"}))
		})
		res := run(t, mustTool(t, r, "web_search"), map[string]any{"queries": []any{"first", "second"}, "provider": "tavily"})
		s := scenario{text: res.Text(), details: res.Details, requestCount: int(atomic.LoadInt32(&count))}
		h.mu.Lock()
		for _, e := range h.entries {
			if d, ok := e.Data.(*StoredSearchData); ok && d.Type == "search" {
				s.storedAnswers, s.storedLate = nil, true
				for _, q := range d.Queries {
					s.storedAnswers = append(s.storedAnswers, jsLen(q.Answer))
					s.storedLate = s.storedLate && strings.HasSuffix(q.Answer, "LATE_ANSWER")
				}
			}
		}
		h.mu.Unlock()
		if get := r.Tool("get_search_content"); get != nil {
			s.hasRetrieve = true
			id := dStr(res, "searchId")
			limit := prop(t, get, "limit")["maximum"].(float64)
			p1 := run(t, get, map[string]any{"responseId": id, "queryIndex": 0.0, "limit": limit})
			p2 := run(t, get, map[string]any{"responseId": id, "queryIndex": 0.0, "offset": float64(dInt(t, p1, "nextOffset")), "limit": limit})
			f := run(t, get, map[string]any{"responseId": id, "queryIndex": 0.0, "findText": "LATE_ANSWER", "findMode": "exact"})
			s.page1, s.page1Text, s.page2, s.page2Text, s.found, s.foundText = p1.Details, p1.Text(), p2.Details, p2.Text(), f.Details, f.Text()
		}
		return s
	}
	num := func(m map[string]any, k string) int {
		switch v := m[k].(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
		return -1
	}
	var cached *scenario
	defaultScenario := func(t *testing.T) scenario {
		if cached == nil {
			s := run1(t, `{"provider":"tavily"}`)
			cached = &s
		}
		return *cached
	}

	tw(t, F, "default raw multi-query output is bounded, attributed, and stored without mutation", func(t *testing.T) {
		adapted(t, note)
		out := defaultScenario(t)
		if out.requestCount != 2 || jsLen(out.text) != defaultCap {
			t.Fatalf("%d %d", out.requestCount, jsLen(out.text))
		}
		matchRE(t, `Output truncated`, out.text)
		matchRE(t, `responseId "[a-z0-9]+"`, out.text)
		matchRE(t, `get_search_content\(\{ responseId: "[a-z0-9]+", queryIndex: 0, offset: 0, limit: 30000 \}\)`, out.text)
		matchRE(t, `Providers used:\*\* Query 1: tavily; Query 2: tavily`, out.text)
		if out.details["truncated"] != true {
			t.Fatal("not truncated")
		}
		label := "\n\n---\n[Output truncated.]"
		retained := strings.Index(out.text, label)
		if retained <= 0 || num(out.details, "returnedChars") != retained || len(out.text[retained+len(label):]) == 0 {
			t.Fatalf("%d %+v", retained, out.details)
		}
		if num(out.details, "originalChars")-num(out.details, "returnedChars") != num(out.details, "omittedChars") {
			t.Fatalf("%+v", out.details)
		}
		if !reflect.DeepEqual(out.storedAnswers, []int{largeAnswer, largeAnswer}) || !out.storedLate {
			t.Fatalf("%v %v", out.storedAnswers, out.storedLate)
		}
	})
	tw(t, F, "stored search answers support bounded continuation and findText", func(t *testing.T) {
		adapted(t, note)
		out := defaultScenario(t)
		if num(out.page1, "offset") != 0 || num(out.page1, "returnedChars") >= defaultCap || num(out.page1, "nextOffset") != num(out.page1, "returnedChars") ||
			jsLen(out.page1Text) > defaultCap || out.page1["truncated"] != true {
			t.Fatalf("%+v", out.page1)
		}
		if num(out.page2, "offset") != num(out.page1, "nextOffset") || num(out.page2, "returnedChars") >= defaultCap ||
			num(out.page2, "nextOffset") != num(out.page2, "offset")+num(out.page2, "returnedChars") || jsLen(out.page2Text) > defaultCap ||
			num(out.page1, "contentLength") != num(out.page2, "contentLength") {
			t.Fatalf("%+v", out.page2)
		}
		noMatchRE(t, `LATE_ANSWER`, out.page1Text)
		matchRE(t, `LATE_ANSWER`, out.foundText)
		if num(out.found, "contentLength") != num(out.page1, "contentLength") {
			t.Fatal(out.found)
		}
	})
	tw(t, F, "truncated output discloses disabled retrieval while remaining within the cap", func(t *testing.T) {
		adapted(t, note)
		out := run1(t, `{"provider":"tavily","tools":{"getSearchContent":{"enabled":false}}}`)
		if jsLen(out.text) != defaultCap || out.hasRetrieve {
			t.Fatal(jsLen(out.text))
		}
		matchRE(t, `Output truncated`, out.text)
		matchRE(t, `responseId "[a-z0-9]+"`, out.text)
		matchRE(t, `Enable get_search_content to retrieve the full stored results`, out.text)
	})
	tw(t, F, "configured inline limit bounds the complete raw presentation", func(t *testing.T) {
		adapted(t, note)
		out := run1(t, `{"provider":"tavily","maxInlineContentChars":12000}`)
		if jsLen(out.text) != 12000 || num(out.details, "returnedChars") >= 12000 || num(out.page1, "returnedChars") >= 12000 {
			t.Fatalf("%d %+v", jsLen(out.text), out.details)
		}
		matchRE(t, `limit: 12000`, out.text)
	})
	tw(t, F, "below-minimum inline limit falls back to the default and exact minimum remains complete", func(t *testing.T) {
		adapted(t, note)
		below := run1(t, `{"provider":"tavily","maxInlineContentChars":999}`)
		if jsLen(below.text) != defaultCap {
			t.Fatal(jsLen(below.text))
		}
		matchRE(t, `limit: 30000`, below.text)
		exact := run1(t, `{"provider":"tavily","maxInlineContentChars":1000}`)
		if jsLen(exact.text) != 1000 {
			t.Fatal(jsLen(exact.text))
		}
		matchRE(t, `Output truncated`, exact.text)
		matchRE(t, `responseId "[a-z0-9]+"`, exact.text)
		matchRE(t, `limit: 1000`, exact.text)
	})

	includeContent := func(t *testing.T, mode string) ToolOutput {
		r, _ := started(t, `{"provider":"tavily","maxInlineContentChars":1000}`)
		t.Setenv("TAVILY_API_KEY", "boundary-test-key")
		answer := strings.Repeat("A", largeAnswer)
		useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://api.tavily.com/search" && mode == "inline":
				return reply(200, tavilyBody(answer, map[string]string{"title": "Inline", "url": "https://example.com/inline", "content": "snippet", "raw_content": "full inline page"}))
			case c.URL == "https://api.tavily.com/search":
				return reply(200, tavilyBody(answer, map[string]string{"title": "Background", "url": "https://example.com/background", "content": "snippet"}))
			case c.URL == "https://example.com/background":
				return netReply{Status: 200, Body: "<main>background page</main>", Header: hdr("content-type", "text/html")}
			}
			return netReply{Err: fmt.Errorf("Unexpected fetch: %s", c.URL)}
		})
		return run(t, mustTool(t, r, "web_search"), map[string]any{"query": "content guidance", "includeContent": true})
	}
	tw(t, F, "truncated inline-ready search retains fetch and search retrieval guidance", func(t *testing.T) {
		adapted(t, note)
		out := includeContent(t, "inline")
		fid, sid := dStr(out, "fetchId"), dStr(out, "searchId")
		if jsLen(out.Text()) != 1000 || dInt(t, out, "originalChars") >= largeAnswer+1000 {
			t.Fatalf("%d %+v", jsLen(out.Text()), out.Details)
		}
		matchRE(t, fmt.Sprintf(`Full content for 1 sources is ready as responseId "%s"`, fid), out.Text())
		matchRE(t, fmt.Sprintf(`get_search_content\(\{ responseId: "%s", urlIndex: 0, offset: 0, limit: 1000 \}\)`, fid), out.Text())
		matchRE(t, fmt.Sprintf(`Full search results are stored as responseId "%s"`, sid), out.Text())
		matchRE(t, fmt.Sprintf(`get_search_content\(\{ responseId: "%s", queryIndex: 0, offset: 0, limit: 1000 \}\)`, sid), out.Text())
	})
	tw(t, F, "truncated background-fetch search retains state, fetchId, and search retrieval guidance", func(t *testing.T) {
		adapted(t, note)
		out := includeContent(t, "background")
		fid, sid := dStr(out, "fetchId"), dStr(out, "searchId")
		if jsLen(out.Text()) != 1000 {
			t.Fatal(jsLen(out.Text()))
		}
		matchRE(t, fmt.Sprintf(`Content fetching in background as responseId "%s"`, fid), out.Text())
		matchRE(t, `Will notify when ready`, out.Text())
		matchRE(t, fmt.Sprintf(`Full search results are stored as responseId "%s"`, sid), out.Text())
		matchRE(t, fmt.Sprintf(`get_search_content\(\{ responseId: "%s", queryIndex: 0, offset: 0, limit: 1000 \}\)`, sid), out.Text())
	})
}

func TestUpstream_fetch_render_call(t *testing.T) {
	const F = "fetch-render-call"
	call := func(t *testing.T, tool string, args map[string]any) []string {
		r, _ := newRuntime(t, "")
		lines := r.RenderCall(tool, args)
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return lines
	}
	tw(t, F, "fetch_content renderCall falls back to url when urls is empty", func(t *testing.T) {
		got := call(t, "fetch_content", map[string]any{"url": "https://example.com/docs", "urls": []any{}, "frames": 1.0, "prompt": "", "model": ""})
		if !reflect.DeepEqual(got, []string{"fetch https://example.com/docs"}) {
			t.Fatal(got)
		}
	})
	tw(t, F, "fetch_content renderCall tolerates invalid normalized parameters", func(t *testing.T) {
		for _, args := range []map[string]any{{"auth": 1.0}, {"mode": "invalid"}, {"proxy": nil}} {
			if got := call(t, "fetch_content", args); !reflect.DeepEqual(got, []string{"fetch (invalid parameters)"}) {
				t.Fatalf("%v: %v", args, got)
			}
		}
	})
	tw(t, F, "search queries and fetch URLs remain complete in wide tool-call labels", func(t *testing.T) {
		q := "Google Cloud API key best practices restrict HTTP referrers and APIs"
		u := "https://developers.google.com/maps/api-security-best-practices"
		cases := []struct {
			tool string
			args map[string]any
			want []string
		}{
			{"web_search", map[string]any{"query": q}, []string{`search "` + q + `"`}},
			{"fetch_content", map[string]any{"url": u}, []string{"fetch " + u}},
			{"web_search", map[string]any{"queries": []any{q, q}}, []string{"search 2 queries", `  "` + q + `"`, `  "` + q + `"`}},
			{"fetch_content", map[string]any{"urls": []any{u, u + "?ref=docs"}}, []string{"fetch 2 URLs", "  " + u, "  " + u + "?ref=docs"}},
		}
		for _, c := range cases {
			if got := call(t, c.tool, c.args); !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s: %q, want %q", c.tool, got, c.want)
			}
		}
	})
}
