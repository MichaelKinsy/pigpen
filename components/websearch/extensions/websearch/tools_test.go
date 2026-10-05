package websearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Twins of the tool-level tests: tool-registration-config, get-search-content,
// inline-content-config and fetch-answer-storage.

type hostEntry struct {
	Type string
	Data any
}

type hostMessage struct {
	Type, Content string
	Trigger       bool
}

// fakeHost records what the runtime asks the session to do (pi.appendEntry / pi.sendMessage).
type fakeHost struct {
	mu       sync.Mutex
	entries  []hostEntry
	messages []hostMessage
}

func (h *fakeHost) AppendEntry(t string, d any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, hostEntry{t, d})
}

func (h *fakeHost) SendMessage(t, c string, trigger bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, hostMessage{t, c, trigger})
}

func (h *fakeHost) entryCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

// newRuntime registers the tools against config (empty = no web-search.json).
func newRuntime(t *testing.T, config string) (*Runtime, *fakeHost) {
	t.Helper()
	extractEnv(t, config)
	ClearResults()
	t.Cleanup(ClearResults)
	h := &fakeHost{}
	r, err := NewRuntime(h)
	noErr(t, err)
	return r, h
}

func toolNames(r *Runtime) []string {
	var out []string
	for _, s := range r.Tools() {
		out = append(out, s.Name)
	}
	return out
}

func mustTool(t *testing.T, r *Runtime, name string) *ToolSpec {
	t.Helper()
	s := r.Tool(name)
	if s == nil {
		t.Fatalf("tool %s was not registered (have %v)", name, toolNames(r))
	}
	return s
}

func run(t *testing.T, s *ToolSpec, params map[string]any) ToolOutput {
	t.Helper()
	out, err := s.Execute(bg(), params, nil)
	noErr(t, err)
	return out
}

func dStr(o ToolOutput, k string) string { s, _ := o.Details[k].(string); return s }

// dNum reads a numeric detail; the second result is false when the key is missing or null.
func dNum(o ToolOutput, k string) (float64, bool) {
	switch v := o.Details[k].(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

func dInt(t *testing.T, o ToolOutput, k string) int {
	t.Helper()
	v, ok := dNum(o, k)
	if !ok {
		t.Fatalf("details.%s missing in %+v", k, o.Details)
	}
	return int(v)
}

// checkSchema is typebox's Value.Check for the schema keywords the tools use.
func checkSchema(schema map[string]any, v any) bool {
	if any1, ok := schema["anyOf"].([]any); ok {
		for _, s := range any1 {
			if checkSchema(s.(map[string]any), v) {
				return true
			}
		}
		return false
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if e == v {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	switch schema["type"] {
	case "string":
		s, ok := v.(string)
		if !ok {
			return false
		}
		if m, ok := schema["minLength"].(float64); ok && float64(jsLen(s)) < m {
			return false
		}
		if m, ok := schema["maxLength"].(float64); ok && float64(jsLen(s)) > m {
			return false
		}
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer", "number":
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
		if schema["type"] == "integer" && f != math.Trunc(f) {
			return false
		}
		if m, ok := schema["minimum"].(float64); ok && f < m {
			return false
		}
		if m, ok := schema["maximum"].(float64); ok && f > m {
			return false
		}
	case "array":
		list, ok := v.([]any)
		if !ok {
			return false
		}
		if m, ok := schema["minItems"].(float64); ok && float64(len(list)) < m {
			return false
		}
		if m, ok := schema["maxItems"].(float64); ok && float64(len(list)) > m {
			return false
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for _, e := range list {
				if !checkSchema(items, e) {
					return false
				}
			}
		}
	}
	return true
}

func props(t *testing.T, s *ToolSpec) map[string]any {
	t.Helper()
	p, _ := s.ParametersMap()["properties"].(map[string]any)
	if p == nil {
		t.Fatalf("%s has no properties", s.Name)
	}
	return p
}

func prop(t *testing.T, s *ToolSpec, name string) map[string]any {
	t.Helper()
	m, _ := props(t, s)[name].(map[string]any)
	if m == nil {
		t.Fatalf("%s has no property %s", s.Name, name)
	}
	return m
}

func TestUpstream_tool_registration_config(t *testing.T) {
	const F = "tool-registration-config"
	names := func(t *testing.T, config string) []string {
		r, _ := newRuntime(t, config)
		return toolNames(r)
	}
	regErr := func(t *testing.T, config string) error {
		extractEnv(t, config)
		_, err := NewRuntime(&fakeHost{})
		if err == nil {
			t.Fatal("expected a registration error")
		}
		return err
	}
	all := []string{"web_search", "source_check", "fetch_content", "get_search_content", "web_enable"}

	tw(t, F, "malformed config falls back during extension registration", func(t *testing.T) {
		r, _ := newRuntime(t, `{`)
		if !reflect.DeepEqual(toolNames(r), all) {
			t.Fatal(toolNames(r))
		}
		if !strings.Contains(r.Warning(), "Failed to parse") {
			t.Fatal(r.Warning())
		}
	})
	tw(t, F, "default public execution tool definitions retain their compatibility hashes", func(t *testing.T) {
		r, _ := newRuntime(t, `{}`)
		want := map[string]string{
			"web_search":         "95cd4927bf650e40ac05dce1a348fa70bb4d6f99b10bf7ba6f70a54f0114a81c",
			"source_check":       "0097da0793440b4af40fdde4d6e3efa256e0fb8d4cb52ced3da7958ead268581",
			"fetch_content":      "0082465bae0f184988fd37fe152cad9c7a236e410747ba6770013895a28978d4",
			"get_search_content": "e1c7597fc085a811c0c6fcde365a96571c275c70b93a48206a38be06a7a45a5f",
		}
		for name, hash := range want {
			s := mustTool(t, r, name)
			text := `{"name":` + jsonNoEscape(s.Name) + `,"description":` + jsonNoEscape(s.Description) + `,"parameters":` + s.ParametersJSON() + `}`
			sum := sha256.Sum256([]byte(text))
			if got := hex.EncodeToString(sum[:]); got != hash {
				t.Errorf("%s: %s, want %s", name, got, hash)
			}
		}
	})
	tw(t, F, "search tools constrain numResults to integer values from 1 through 20", func(t *testing.T) {
		r, _ := newRuntime(t, `{}`)
		for _, name := range []string{"web_search", "source_check"} {
			schema := prop(t, mustTool(t, r, name), "numResults")
			if schema["type"] != "integer" || schema["minimum"] != 1.0 || schema["maximum"] != 20.0 {
				t.Fatalf("%s: %v", name, schema)
			}
			for _, v := range []float64{0, -1, 1.5, 21, math.NaN(), math.Inf(1)} {
				if checkSchema(schema, v) {
					t.Errorf("%s accepts %v", name, v)
				}
			}
			for _, v := range []float64{1, 5, 20} {
				if !checkSchema(schema, v) {
					t.Errorf("%s rejects %v", name, v)
				}
			}
		}
	})
	tw(t, F, "tool registration gates support legacy and per-tool config", func(t *testing.T) {
		if got := names(t, `{"webSearch":{"enabled":false}}`); !reflect.DeepEqual(got, []string{"fetch_content", "get_search_content", "web_enable"}) {
			t.Fatal(got)
		}
		got := names(t, `{"webSearch":{"enabled":false},"tools":{"webSearch":{"enabled":true},"sourceCheck":{"enabled":true},"fetchContent":{"enabled":false}}}`)
		if !reflect.DeepEqual(got, []string{"web_search", "source_check", "get_search_content", "web_enable"}) {
			t.Fatal(got)
		}
		got = names(t, `{"tools":{"sourceCheck":{"enabled":false},"getSearchContent":{"enabled":false}}}`)
		if !reflect.DeepEqual(got, []string{"web_search", "fetch_content", "web_enable"}) {
			t.Fatal(got)
		}
	})
	tskip(t, F, "command registration gates default to enabled",
		"websearch, curator and google-account are the curator UI and Gemini account commands (deferred, docs/PORT.md); TestCommandRegistrationGates pins the ported commands")
	tw(t, F, "fetch_content schema exposes auth profile opt-in", func(t *testing.T) {
		r, _ := newRuntime(t, `{}`)
		schema := prop(t, mustTool(t, r, "fetch_content"), "auth")
		var types []any
		for _, o := range schema["anyOf"].([]any) {
			types = append(types, o.(map[string]any)["type"])
		}
		if !reflect.DeepEqual(types, []any{"string", "boolean"}) {
			t.Fatal(types)
		}
	})
	tw(t, F, "registered tools do not advertise disabled get_search_content", func(t *testing.T) {
		r, _ := newRuntime(t, `{"tools":{"getSearchContent":{"enabled":false}}}`)
		s := mustTool(t, r, "fetch_content")
		matchRE(t, `retrieval tool is not registered`, s.Description)
		noMatchRE(t, `get_search_content`, s.Description)
	})
	tskip(t, F, "web activity shortcut renders through the supported string-array API",
		"the activity monitor widget and its shortcut are deferred (docs/PORT.md)")
	tw(t, F, "tool names can be configured without changing defaults", func(t *testing.T) {
		if got := names(t, `{}`); !reflect.DeepEqual(got, all) {
			t.Fatal(got)
		}
		got := names(t, `{"toolNames":{"webSearch":"research_web","sourceCheck":"verify_sources","fetchContent":"grab_content","getSearchContent":"open_content"}}`)
		if !reflect.DeepEqual(got, []string{"research_web", "verify_sources", "grab_content", "open_content", "web_enable"}) {
			t.Fatal(got)
		}
	})
	tw(t, F, "tool config rejects invalid, duplicate, or reserved names and unknown toolActivation", func(t *testing.T) {
		matchRE(t, `toolNames\.webSearch`, regErr(t, `{"toolNames":{"webSearch":"1bad"}}`).Error())
		matchRE(t, `duplicates`, regErr(t, `{"toolNames":{"webSearch":"same_name","fetchContent":"same_name"}}`).Error())
		matchRE(t, `toolActivation.*"dynamic" or "eager"`, regErr(t, `{"toolActivation":"lazy"}`).Error())
		// Upstream matches /web_enable.*reserved/i against the child's stderr, where the thrown source
		// line puts "web_enable" first; the message itself reads "reserved loader name web_enable".
		matchRE(t, `(?i)reserved loader name web_enable`, regErr(t, `{"toolNames":{"webSearch":"web_enable"}}`).Error())
	})
	tw(t, F, "webSearch.enabled false registers only fetch tools and ignores disabled-name duplicates", func(t *testing.T) {
		got := names(t, `{"webSearch":{"enabled":false},"toolNames":{"webSearch":"content_only","sourceCheck":"content_only","fetchContent":"grab_content","getSearchContent":"open_content"}}`)
		if !reflect.DeepEqual(got, []string{"grab_content", "open_content", "web_enable"}) {
			t.Fatal(got)
		}
		matchRE(t, `duplicates`, regErr(t, `{"webSearch":{"enabled":false},"toolNames":{"fetchContent":"same_name","getSearchContent":"same_name"}}`).Error())
	})
}

func jsonNoEscape(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

func TestCommandRegistrationGates(t *testing.T) {
	r, _ := newRuntime(t, `{}`)
	if !reflect.DeepEqual(r.Commands(), []string{"search"}) {
		t.Fatal(r.Commands())
	}
	r, _ = newRuntime(t, `{"commands":{"search":{"enabled":false}}}`)
	if len(r.Commands()) != 0 {
		t.Fatal(r.Commands())
	}
}

func storeFetched(id string, content string) {
	StoreResult(id, &StoredSearchData{ID: id, Type: "fetch", Timestamp: nowMs(), URLs: []ExtractedContent{{URL: "https://example.com/large", Title: "Large Page", Content: content}}})
}

func TestUpstream_get_search_content(t *testing.T) {
	const F = "get-search-content"
	tool := func(t *testing.T) *ToolSpec {
		r, _ := newRuntime(t, "")
		return mustTool(t, r, "get_search_content")
	}
	storeSearch := func() {
		StoreResult("search-result", &StoredSearchData{ID: "search-result", Type: "search", Timestamp: nowMs(), Queries: []QueryResultData{{
			Query: "CotEditor scripts", Results: []SearchResult{
				{Title: "ScriptManager.swift", URL: "https://example.com/script-manager", Snippet: "UNIX script support"},
				{Title: "UNIX script", URL: "https://example.com/unix-script", Snippet: "Script support"},
				{Title: "ScriptMenu", URL: "https://example.com/script-menu", Snippet: "Menu integration"},
			}}}})
	}

	tw(t, F, "get_search_content schemas constrain numeric parameters", func(t *testing.T) {
		s := tool(t)
		for _, name := range []string{"queryIndex", "urlIndex", "offset"} {
			p := prop(t, s, name)
			if p["type"] != "integer" || p["minimum"] != 0.0 {
				t.Fatalf("%s: %v", name, p)
			}
			if _, has := p["maximum"]; has {
				t.Fatalf("%s has a maximum", name)
			}
			for _, v := range []float64{-1, 1.5, math.NaN(), math.Inf(1)} {
				if checkSchema(p, v) {
					t.Errorf("%s accepts %v", name, v)
				}
			}
		}
		limit := prop(t, s, "limit")
		if limit["type"] != "integer" || limit["minimum"] != 1.0 || limit["maximum"] != 30000.0 {
			t.Fatal(limit)
		}
		for _, v := range []float64{0, 1.5, 30001, math.NaN(), math.Inf(1)} {
			if checkSchema(limit, v) {
				t.Errorf("limit accepts %v", v)
			}
		}
		for _, v := range []float64{1, 30000} {
			if !checkSchema(limit, v) {
				t.Errorf("limit rejects %v", v)
			}
		}
	})
	tw(t, F, "get_search_content returns a bounded first slice for large fetched content", func(t *testing.T) {
		s := tool(t)
		storeFetched("large-fetch", strings.Repeat("A", 30000)+"TAIL")
		out := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0})
		if dInt(t, out, "contentLength") != 30004 || dInt(t, out, "offset") != 0 || dInt(t, out, "returnedChars") != 30000 || dInt(t, out, "nextOffset") != 30000 || out.Details["truncated"] != true {
			t.Fatalf("%+v", out.Details)
		}
		matchRE(t, `Showing chars 0-30000 of 30004`, out.Text())
		matchRE(t, `offset: 30000`, out.Text())
		noMatchRE(t, `TAIL`, out.Text())
	})
	tw(t, F, "get_search_content returns requested fetched content slices", func(t *testing.T) {
		s := tool(t)
		storeFetched("large-fetch", strings.Repeat("A", 30000)+"BCDEFGHIJ")
		out := run(t, s, map[string]any{"responseId": "large-fetch", "url": "https://example.com/large", "offset": 30000.0, "limit": 5.0})
		if dInt(t, out, "offset") != 30000 || dInt(t, out, "limit") != 5 || dInt(t, out, "returnedChars") != 5 || dInt(t, out, "nextOffset") != 30005 {
			t.Fatalf("%+v", out.Details)
		}
		matchRE(t, `BCDEF`, out.Text())
		noMatchRE(t, `GHIJ`, out.Text())
		matchRE(t, `urlIndex: 0, offset: 30005, limit: 5`, out.Text())
	})
	tw(t, F, "get_search_content rejects unsafe fetched content ranges", func(t *testing.T) {
		s := tool(t)
		storeFetched("large-fetch", "short content")
		tooLarge := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0, "limit": 30001.0})
		if dStr(tooLarge, "error") != "Invalid limit" {
			t.Fatalf("%+v", tooLarge.Details)
		}
		matchRE(t, `received 30001`, tooLarge.Text())
		matchRE(t, `limit must be an integer from 1 to 30000`, tooLarge.Text())
		badOffset := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0, "offset": 1.5})
		if dStr(badOffset, "error") != "Invalid offset" {
			t.Fatalf("%+v", badOffset.Details)
		}
		matchRE(t, `received 1\.5`, badOffset.Text())
		matchRE(t, `Use 0 or a larger integer`, badOffset.Text())
		oor := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0, "offset": 99.0})
		if dStr(oor, "error") != "Offset out of range" {
			t.Fatalf("%+v", oor.Details)
		}
		matchRE(t, `Received offset 99`, oor.Text())
		matchRE(t, `valid range is 0-13`, oor.Text())
		missing := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0, "findMode": "fuzzy"})
		if dStr(missing, "error") != "findMode requires findText" {
			t.Fatalf("%+v", missing.Details)
		}
		matchRE(t, `findMode "fuzzy" requires findText`, missing.Text())

		art := BuildResearchArtifact(BuildArtifactInput{Query: "stored claim",
			Results: []RankedSearchResult{{SearchResult: SearchResult{URL: "https://example.com/research", Title: "Research source", Snippet: "The bridge defaults match this research passage."}, Rank: 1}},
			Summary: ptr("Unique bridge research summary marker.")})
		art.ID = "stored-research"
		noErr(t, StoreResearchArtifact(&art))
		rl := run(t, s, map[string]any{"responseId": "stored-research", "limit": 30001.0})
		if dStr(rl, "error") != "Invalid limit" {
			t.Fatalf("%+v", rl.Details)
		}
		matchRE(t, `received 30001`, rl.Text())
		matchRE(t, `limit must be an integer from 1 to 30000`, rl.Text())
		ro := run(t, s, map[string]any{"responseId": "stored-research", "offset": 99999.0})
		if dStr(ro, "error") != "Offset out of range" {
			t.Fatalf("%+v", ro.Details)
		}
		matchRE(t, `responseId "stored-research"`, ro.Text())
		matchRE(t, `valid range is 0-`, ro.Text())
		rf := run(t, s, map[string]any{"responseId": "stored-research", "offset": 0.0, "limit": 10000.0, "findText": "Unique bridge research summary marker"})
		if dStr(rf, "type") != "research" || dStr(rf, "findMode") != "case-insensitive" || dInt(t, rf, "matchCount") != 1 {
			t.Fatalf("%+v", rf.Details)
		}
		matchRE(t, `^Text matches \(case-insensitive\)`, rf.Text())
		matchRE(t, `Unique bridge research summary marker`, rf.Text())
	})
	tw(t, F, "get_search_content normalizes bridge defaults for search matches", func(t *testing.T) {
		s := tool(t)
		storeSearch()
		out := run(t, s, map[string]any{"responseId": "search-result", "query": "", "queryIndex": 0.0, "url": "", "urlIndex": 0.0, "offset": 0.0, "limit": 10000.0,
			"findText": []any{"ScriptManager.swift", "UNIX script", "ScriptMenu"}, "findMode": "case-insensitive"})
		if dStr(out, "findMode") != "case-insensitive" || dInt(t, out, "matchCount") != 4 {
			t.Fatalf("%+v", out.Details)
		}
		matchRE(t, `ScriptManager\.swift`, out.Text())
		matchRE(t, `ScriptMenu`, out.Text())
	})
	tw(t, F, "get_search_content pages complete search data and validates search ranges", func(t *testing.T) {
		s := tool(t)
		late := "LATE_SNIPPET_" + strings.Repeat("S", 116000)
		StoreResult("oversized-search", &StoredSearchData{ID: "oversized-search", Type: "search", Timestamp: nowMs(), Queries: []QueryResultData{{
			Query: "oversized stored search", Answer: "answer " + strings.Repeat("A", 40000) + " OMITTED_ANSWER",
			Results:  []SearchResult{{Title: "First", URL: "https://example.com/first", Snippet: "first snippet"}, {Title: "Late", URL: "https://example.com/late", Snippet: late}},
			Provider: "fixture-provider"}}})
		first := run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "limit": 30000.0})
		rc := dInt(t, first, "returnedChars")
		if rc >= 30000 || dInt(t, first, "nextOffset") != rc || first.Details["truncated"] != true || jsLen(first.Text()) > 30000 || dInt(t, first, "contentLength") <= 156000 {
			t.Fatalf("%+v", first.Details)
		}
		matchRE(t, `Provider:\*\* fixture-provider`, first.Text())
		noMatchRE(t, `LATE_SNIPPET`, first.Text())

		lateOut := run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "findText": []any{"OMITTED_ANSWER", "LATE_SNIPPET"}, "findMode": "exact"})
		if dInt(t, lateOut, "matchCount") != 2 {
			t.Fatalf("%+v", lateOut.Details)
		}
		matchRE(t, `OMITTED_ANSWER`, lateOut.Text())
		matchRE(t, `LATE_SNIPPET`, lateOut.Text())

		one := run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "limit": 1.0})
		if dInt(t, one, "returnedChars") != 1 || dInt(t, one, "nextOffset") != 1 || jsLen(one.Text()) > 30000 {
			t.Fatalf("%+v", one.Details)
		}
		matchRE(t, `offset: 1, limit: 1`, one.Text())

		eof := run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "offset": float64(dInt(t, first, "contentLength")), "limit": 1.0})
		if eof.Text() != "" || dInt(t, eof, "returnedChars") != 0 || eof.Details["nextOffset"] != nil || eof.Details["truncated"] != false {
			t.Fatalf("%q %+v", eof.Text(), eof.Details)
		}
		if got := dStr(run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "limit": 30001.0}), "error"); got != "Invalid limit" {
			t.Fatal(got)
		}
		if got := dStr(run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "offset": -1.0}), "error"); got != "Invalid offset" {
			t.Fatal(got)
		}
		if got := dStr(run(t, s, map[string]any{"responseId": "oversized-search", "queryIndex": 0.0, "offset": float64(dInt(t, first, "contentLength") + 1)}), "error"); got != "Offset out of range" {
			t.Fatal(got)
		}
	})
	tw(t, F, "get_search_content returns small fetched content without continuation noise", func(t *testing.T) {
		s := tool(t)
		storeFetched("large-fetch", "small content")
		out := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0})
		if dInt(t, out, "returnedChars") != len("small content") || out.Details["nextOffset"] != nil {
			t.Fatalf("%+v", out.Details)
		}
		matchRE(t, `small content`, out.Text())
		noMatchRE(t, `Showing chars`, out.Text())
	})
	tw(t, F, "get_search_content finds bounded passages in stored fetched content", func(t *testing.T) {
		s := tool(t)
		storeFetched("large-fetch", "prefix "+strings.Repeat("A", 2000)+" Installation requires Node 22. "+strings.Repeat("B", 2000)+" suffix")
		out := run(t, s, map[string]any{"responseId": "large-fetch", "query": "", "queryIndex": 0.0, "url": "", "urlIndex": 0.0, "offset": 0.0, "limit": 10000.0, "findText": "installation"})
		if dInt(t, out, "matchCount") != 1 || dStr(out, "findMode") != "case-insensitive" || jsLen(out.Text()) >= 1000 {
			t.Fatalf("%+v", out.Details)
		}
		matchRE(t, `Installation requires Node 22`, out.Text())
	})
	tw(t, F, "get_search_content represents every matching maximum-length query under overflow", func(t *testing.T) {
		s := tool(t)
		sequence := strings.Repeat("a", 499) + "0123456789"
		queries := make([]string, 10)
		anyQueries := make([]any, 10)
		for i := range queries {
			queries[i] = sequence[i : i+500]
			anyQueries[i] = queries[i]
		}
		gap := strings.Repeat("Z", 1000)
		occurrence := func(q string) string { return strings.Repeat("x", 500) + q + strings.Repeat("x", 500) }
		var parts []string
		for _, q := range queries[:9] {
			parts = append(parts, occurrence(q))
		}
		parts = append(parts, sequence, occurrence(queries[0]), occurrence(queries[1]), occurrence(queries[9]))
		storeFetched("large-fetch", strings.Join(parts, gap))
		if !checkSchema(prop(t, s, "findText"), anyQueries) {
			t.Fatal("schema rejects ten 500-char queries")
		}
		out := run(t, s, map[string]any{"responseId": "large-fetch", "urlIndex": 0.0, "findText": anyQueries, "findMode": "exact"})
		excerpts := strings.Join(strings.Split(out.Text(), "\n\n")[1:], "\n\n")
		mc, rm := dInt(t, out, "matchCount"), dInt(t, out, "returnedMatches")
		if mc != 22 || rm > mc {
			t.Fatalf("%d %d", mc, rm)
		}
		var snippets strings.Builder
		numbered := regexpMust(`^\d+\. `)
		for _, section := range strings.Split(excerpts, "\n\n") {
			if numbered.MatchString(section) {
				snippets.WriteString(strings.Join(strings.Split(section, "\n")[1:], "\n"))
			}
		}
		for i, q := range queries {
			if !strings.Contains(excerpts, "Q"+strconv.Itoa(i+1)+` = "`+q+`"`) {
				t.Errorf("missing legend entry %d", i+1)
			}
			if !strings.Contains(snippets.String(), q) {
				t.Errorf("missing representative occurrence %d", i+1)
			}
		}
		if rm < mc {
			matchRE(t, `Showing \d+ of 22 matches\.`, excerpts)
		}
		if jsLen(excerpts) > 20000 {
			t.Fatal(jsLen(excerpts))
		}
	})
}

func TestUpstream_inline_content_config(t *testing.T) {
	const F = "inline-content-config"
	scenario := func(t *testing.T, max any) map[string]any {
		config := ""
		if max != nil {
			config = `{"maxInlineContentChars":` + strconv.Itoa(max.(int)) + `}`
		}
		r, _ := newRuntime(t, config)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: strings.Repeat("A", 40000) + "TAIL", Header: hdr("content-type", "text/plain")}
		})
		fetch, content := mustTool(t, r, "fetch_content"), mustTool(t, r, "get_search_content")
		fetched := run(t, fetch, map[string]any{"url": "https://93.184.216.34/page"})
		id := dStr(fetched, "responseId")
		retrieved := run(t, content, map[string]any{"responseId": id, "urlIndex": 0.0})
		tail := run(t, content, map[string]any{"responseId": id, "urlIndex": 0.0, "offset": 40000.0, "limit": 4.0})
		rejectLimit := 30001.0
		if max != nil {
			rejectLimit = float64(max.(int) + 1)
		}
		rejected := run(t, content, map[string]any{"responseId": id, "urlIndex": 0.0, "limit": rejectLimit})
		var text string
		for _, b := range fetched.Content {
			if b.Type == "text" {
				text = b.Text
			}
		}
		rej, _ := dNum(rejected, "maxLimit")
		return map[string]any{
			"schemaMax":           prop(t, content, "limit")["maximum"],
			"fetchTruncated":      fetched.Details["truncated"],
			"fetchEndOffset":      float64(strings.Index(text, "\n\n---")),
			"retrievedChars":      float64(dInt(t, retrieved, "returnedChars")),
			"retrievedNextOffset": float64(dInt(t, retrieved, "nextOffset")),
			"tail":                strings.Contains(tail.Text(), "TAIL"),
			"rejectedMax":         rej,
		}
	}
	want := func(n float64) map[string]any {
		return map[string]any{"schemaMax": n, "fetchTruncated": true, "fetchEndOffset": n, "retrievedChars": n, "retrievedNextOffset": n, "tail": true, "rejectedMax": n}
	}
	tw(t, F, "inline content defaults to 30,000 characters", func(t *testing.T) {
		if got := scenario(t, nil); !reflect.DeepEqual(got, want(30000)) {
			t.Fatal(got)
		}
	})
	tw(t, F, "maxInlineContentChars applies to direct and stored content slices", func(t *testing.T) {
		if got := scenario(t, 40000); !reflect.DeepEqual(got, want(40000)) {
			t.Fatal(got)
		}
	})
	tw(t, F, "stored content schema and execution keep one registered limit", func(t *testing.T) {
		r, _ := newRuntime(t, `{"maxInlineContentChars":40000}`)
		writeConfig(t, ConfigDir(), `{"maxInlineContentChars":20000}`)
		storeFetched("stored", strings.Repeat("A", 50000))
		content := mustTool(t, r, "get_search_content")
		rejected := run(t, content, map[string]any{"responseId": "stored", "urlIndex": 0.0, "limit": 40001.0})
		rej, _ := dNum(rejected, "maxLimit")
		if prop(t, content, "limit")["maximum"] != 40000.0 || rej != 40000 {
			t.Fatalf("%v %v", prop(t, content, "limit")["maximum"], rej)
		}
	})
}

func TestUpstream_fetch_answer_storage(t *testing.T) {
	tw(t, "fetch-answer-storage", "answer mode stores original fetched content instead of its answer presentation", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		original := strings.Repeat("Original page content. ", 60)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Header: hdr("content-type", "text/html"),
				Body: `<!doctype html><html><head><title>Stored Page</title></head><body><article><h1>Stored Page</h1><p>` + original + `</p></article></body></html>`}
		})
		out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page", "mode": "answer", "prompt": "What does it say?"})
		matchRE(t, `Page answer failed`, dStr(out, "error"))
		stored := GetResult(dStr(out, "responseId"))
		if stored == nil || stored.Type != "fetch" || !strings.Contains(stored.URLs[0].Content, "Original page content") || strings.Contains(stored.URLs[0].Content, "Page answer failed") {
			t.Fatalf("%+v", stored)
		}
	})
}

var _ = context.Background

func regexpMust(re string) *regexp.Regexp { return regexp.MustCompile(re) }
