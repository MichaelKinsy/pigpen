package websearch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Twins of tool-activation, tool-activation-version and the tool-level cases of
// fetch-cache-storage.

// activationHost is a host with a mutable active tool set (pi.getAllTools / getActiveTools /
// setActiveTools).
type activationHost struct {
	fakeHost
	registered        []string
	active            []string
	unavailable       []string
	throwOnSet        bool
	dropOnReadback    string
	activateAfterInit bool
}

func (h *activationHost) AllToolNames() []string {
	var out []string
	for _, n := range h.registered {
		if !sliceHas(h.unavailable, n) {
			out = append(out, n)
		}
	}
	return out
}
func (h *activationHost) ActiveToolNames() []string { return append([]string(nil), h.active...) }
func (h *activationHost) SetActiveTools(names []string) error {
	if h.throwOnSet {
		return errors.New("set failed")
	}
	h.active = nil
	for _, n := range names {
		if n != h.dropOnReadback {
			h.active = append(h.active, n)
		}
	}
	return nil
}

var defaultToolNames = []string{"web_search", "source_check", "fetch_content", "get_search_content"}

type activationState struct {
	r      *Runtime
	h      *activationHost
	before []string
	result ToolOutput
}

type activationOpts struct {
	unavailable    []string
	throwOnSet     bool
	dropOnReadback string
	messages       []map[string]any
	events         []string
	activate       bool
	second         bool
}

func activationRun(t *testing.T, config string, o activationOpts) activationState {
	t.Helper()
	extractEnv(t, config)
	h := &activationHost{active: []string{"read", "foreign_tool"}, unavailable: o.unavailable, dropOnReadback: o.dropOnReadback}
	r, err := NewRuntime(h)
	noErr(t, err)
	for _, s := range r.Tools() {
		h.registered = append(h.registered, s.Name)
		if !sliceHas(o.unavailable, s.Name) {
			h.active = append(h.active, s.Name)
		}
	}
	h.throwOnSet = o.throwOnSet
	events := o.events
	if events == nil {
		events = []string{"session_start"}
	}
	for _, e := range events {
		if e == "before_agent_start" {
			r.BeforeAgentStart()
		} else {
			r.SelectFromSession(o.messages)
		}
	}
	st := activationState{r: r, h: h, before: h.ActiveToolNames()}
	if o.activate && r.Tool("web_enable") != nil {
		out, err := r.Tool("web_enable").Execute(bg(), map[string]any{}, nil)
		noErr(t, err)
		st.result = out
	}
	if o.second && r.Tool("web_enable") != nil {
		_, err := r.Tool("web_enable").Execute(bg(), map[string]any{}, nil)
		noErr(t, err)
	}
	return st
}

func (s activationState) filter(names []string) []string {
	var out []string
	for _, n := range s.before {
		if sliceHas(names, n) {
			out = append(out, n)
		}
	}
	return out
}

func registeredNames(r *Runtime) []string { return toolNames(r) }

func toolsAdded(names ...string) map[string]any {
	var list []any
	for _, n := range names {
		list = append(list, map[string]any{"name": n, "description": "", "parameters": map[string]any{"type": "object"}})
	}
	return map[string]any{"role": "system", "content": "", "toolsAdded": list, "timestamp": 1.0}
}

func TestUpstream_tool_activation(t *testing.T) {
	const F = "tool-activation"
	withLoader := append(append([]string{}, defaultToolNames...), "web_enable")

	tw(t, F, "fresh sessions expose compact configured guidance and keep web tools registered but dormant", func(t *testing.T) {
		st := activationRun(t, `{}`, activationOpts{})
		if !reflect.DeepEqual(registeredNames(st.r), withLoader) || len(st.filter(defaultToolNames)) != 0 || !sliceHas(st.before, "web_enable") {
			t.Fatalf("%v %v", registeredNames(st.r), st.before)
		}
		loader := mustTool(t, st.r, "web_enable")
		matchRE(t, `(?i)next model request`, loader.Description)
		for _, capability := range []string{"search", "source checking", "content fetching", "stored-result retrieval"} {
			matchRE(t, `(?i)`+capability, loader.PromptSnippet)
		}
		if loader.ParametersJSON() != `{"type":"object","properties":{},"additionalProperties":false}` {
			t.Fatal(loader.ParametersJSON())
		}
	})
	tw(t, F, "activation enables every configured name once without removing unrelated tools", func(t *testing.T) {
		names := []string{"research_web", "verify_sources", "grab_content", "open_content"}
		st := activationRun(t, `{"toolNames":{"webSearch":"research_web","sourceCheck":"verify_sources","fetchContent":"grab_content","getSearchContent":"open_content"}}`,
			activationOpts{activate: true, second: true})
		if !reflect.DeepEqual(st.before, []string{"read", "foreign_tool", "web_enable"}) ||
			!reflect.DeepEqual(st.h.active, append([]string{"read", "foreign_tool", "web_enable"}, names...)) ||
			st.result.IsError || !reflect.DeepEqual(st.result.Details["enabled"], names) {
			t.Fatalf("%v %v %+v", st.before, st.h.active, st.result)
		}
	})
	tw(t, F, "disabled capabilities are neither registered nor advertised", func(t *testing.T) {
		st := activationRun(t, `{"tools":{"webSearch":{"enabled":false},"sourceCheck":{"enabled":false},"getSearchContent":{"enabled":false}}}`, activationOpts{})
		if !reflect.DeepEqual(registeredNames(st.r), []string{"fetch_content", "web_enable"}) {
			t.Fatal(registeredNames(st.r))
		}
		snippet := mustTool(t, st.r, "web_enable").PromptSnippet
		matchRE(t, `(?i)content fetching`, snippet)
		noMatchRE(t, `(?i)source checking|stored-result retrieval|web search`, snippet)
	})
	tw(t, F, "all-disabled configuration registers no loader", func(t *testing.T) {
		st := activationRun(t, `{"tools":{"webSearch":{"enabled":false},"sourceCheck":{"enabled":false},"fetchContent":{"enabled":false},"getSearchContent":{"enabled":false}}}`, activationOpts{})
		if len(registeredNames(st.r)) != 0 || !reflect.DeepEqual(st.before, []string{"read", "foreign_tool"}) {
			t.Fatalf("%v %v", registeredNames(st.r), st.before)
		}
	})
	tw(t, F, "eager activation config registers no loader and keeps web tools active", func(t *testing.T) {
		st := activationRun(t, `{"toolActivation":"eager"}`, activationOpts{})
		if !reflect.DeepEqual(registeredNames(st.r), defaultToolNames) || !reflect.DeepEqual(st.filter(defaultToolNames), defaultToolNames) {
			t.Fatalf("%v %v", registeredNames(st.r), st.before)
		}
	})
	tw(t, F, "excluded loader leaves permitted legacy tools active", func(t *testing.T) {
		st := activationRun(t, `{}`, activationOpts{unavailable: []string{"web_enable"}})
		if !reflect.DeepEqual(st.filter(defaultToolNames), defaultToolNames) || sliceHas(st.before, "web_enable") {
			t.Fatal(st.before)
		}
	})
	tw(t, F, "activation reports unavailable and failed readback without false success", func(t *testing.T) {
		un := activationRun(t, `{}`, activationOpts{activate: true, unavailable: []string{"source_check"}})
		if !un.result.IsError || !reflect.DeepEqual(un.result.Details["unavailable"], []string{"source_check"}) {
			t.Fatalf("%+v", un.result)
		}
		rb := activationRun(t, `{}`, activationOpts{activate: true, dropOnReadback: "fetch_content"})
		if !rb.result.IsError || !reflect.DeepEqual(rb.result.Details["missing"], []string{"fetch_content"}) || rb.result.Text() != "Tools still inactive after activation: fetch_content." {
			t.Fatalf("%+v", rb.result)
		}
		th := activationRun(t, `{}`, activationOpts{activate: true, throwOnSet: true})
		if !th.result.IsError || !strings.Contains(dStr(th.result, "error"), "set failed") {
			t.Fatalf("%+v", th.result)
		}
	})
	tw(t, F, "cold and warm native transcript selections survive start and tree lifecycle", func(t *testing.T) {
		cold := activationRun(t, `{}`, activationOpts{messages: []map[string]any{toolsAdded("web_enable")}})
		if len(cold.filter(defaultToolNames)) != 0 {
			t.Fatal(cold.before)
		}
		warmMessages := []map[string]any{toolsAdded("web_enable", "web_search")}
		warm := activationRun(t, `{}`, activationOpts{messages: warmMessages, events: []string{"session_tree"}})
		if !reflect.DeepEqual(warm.filter(defaultToolNames), []string{"web_search"}) {
			t.Fatal(warm.before)
		}
		reloaded := activationRun(t, `{}`, activationOpts{messages: warmMessages})
		if !reflect.DeepEqual(reloaded.filter(defaultToolNames), []string{"web_search"}) {
			t.Fatal(reloaded.before)
		}
	})
	tw(t, F, "legacy conversation without tool declarations preserves eager web tools", func(t *testing.T) {
		st := activationRun(t, `{}`, activationOpts{messages: []map[string]any{{"role": "user", "content": []any{map[string]any{"type": "text", "text": "old session"}}, "timestamp": 1.0}}})
		if !reflect.DeepEqual(st.filter(defaultToolNames), defaultToolNames) || !sliceHas(st.before, "web_enable") {
			t.Fatal(st.before)
		}
	})
	tw(t, F, "a resumed transcript that declared its tools without web_enable keeps that set", func(t *testing.T) {
		turn := map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Run: echo hello"}}, "timestamp": 2.0}
		events := []string{"session_start", "before_agent_start"}
		up := activationRun(t, `{}`, activationOpts{messages: []map[string]any{toolsAdded("read"), turn}, events: events})
		if sliceHas(up.before, "web_enable") || len(up.filter(defaultToolNames)) != 0 {
			t.Fatal(up.before)
		}
		rec := activationRun(t, `{}`, activationOpts{messages: []map[string]any{toolsAdded("read", "web_enable"), turn}, events: events})
		if !sliceHas(rec.before, "web_enable") {
			t.Fatal(rec.before)
		}
	})
	tw(t, F, "provider-facing cold and activated schemas stay within budget", func(t *testing.T) {
		size := func(st activationState, active []string) int {
			total := 0
			for _, s := range st.r.Tools() {
				if sliceHas(active, s.Name) {
					total += jsLen(`{"name":` + jsonNoEscape(s.Name) + `,"description":` + jsonNoEscape(s.Description) + `,"parameters":` + s.ParametersJSON() + `}`)
				}
			}
			return total
		}
		cold := activationRun(t, `{}`, activationOpts{})
		if n := size(cold, cold.before); n > 700 {
			t.Fatalf("cold schema is %d characters", n)
		}
		act := activationRun(t, `{}`, activationOpts{activate: true})
		if n := size(act, act.h.active); n > 11924 {
			t.Fatalf("activated schema is %d characters", n)
		}
	})
}

func TestUpstream_tool_activation_version(t *testing.T) {
	const F = "tool-activation-version"
	tskip(t, F, "activation ignores the imported Pi version and needs no Pi package on disk",
		"the Go SDK has no imported Pi package or version probe: the tool API is part of the SDK contract")
	tw(t, F, "a host-redirected warm session restores exactly the tools it recorded", func(t *testing.T) {
		messages := []map[string]any{toolsAdded("web_search"), {"role": "system", "content": "", "toolsRemoved": []any{map[string]any{"name": "source_check"}}, "timestamp": 2.0}}
		st := activationRun(t, `{}`, activationOpts{messages: messages})
		if !sliceHas(st.before, "web_search") || sliceHas(st.before, "fetch_content") {
			t.Fatal(st.before)
		}
	})
	tskip(t, F, "a host older than 0.86.0 falls back to eager web tools without crashing",
		"the Go SDK (PiG 0.3.0, Pi 0.87.1 API) always has the tool activation API; there is no older-host path")
}

func TestUpstream_tool_activation_sdk(t *testing.T) {
	const F = "tool-activation-sdk"
	tskip(t, F, "native Pi sends configured web schemas on the request immediately after activation",
		"needs the real Pi host; the pigeq scenario and the Binary run cover this against PiG")
	tskip(t, F, "native Pi resumes a session recorded without pi-web-access with its recorded tools",
		"needs the real Pi host; the pigeq scenario and the Binary run cover this against PiG")
}

// ---- fetch-cache-storage: the cases that go through the tools -------------------------------

func TestUpstream_fetch_cache_storage_tools(t *testing.T) {
	const F = "fetch-cache-storage"
	setup := func(t *testing.T, config string) (*Runtime, *fakeHost) {
		r, h := newRuntime(t, config)
		ResetCaches()
		return r, h
	}
	fetchEntry := func(t *testing.T, h *fakeHost) *StoredSearchData {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, e := range h.entries {
			if d, ok := e.Data.(*StoredSearchData); ok && e.Type == "web-search-results" {
				return d
			}
		}
		t.Fatal("no web-search-results entry")
		return nil
	}
	restore := func(t *testing.T, d *StoredSearchData) {
		ClearResults()
		raw, err := jsonMarshal(d)
		noErr(t, err)
		RestoreFromEntries([]CustomEntry{{CustomType: "web-search-results", Data: raw}})
	}
	tw(t, F, "fetch_content stores full content in cache and writes a bounded session entry", func(t *testing.T) {
		r, h := setup(t, "")
		content := strings.Repeat("Cached page content. ", 4000)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: content, Header: hdr("content-type", "text/plain")}
		})
		result := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page"})
		entry := fetchEntry(t, h)
		if entry.Type != "fetch" || entry.URLs != nil || entry.FetchCache == nil || entry.FetchCache.Key == "" ||
			len(entry.URLMetadata) == 0 || entry.URLMetadata[0].ContentLength != jsLen(content) {
			t.Fatalf("%+v", entry)
		}
		serialized, _ := jsonMarshal(entry)
		if len(serialized) >= 5000 || strings.Contains(string(serialized), "Cached page content. Cached page content.") {
			t.Fatalf("session entry was %d chars", len(serialized))
		}
		restore(t, entry)
		got := run(t, mustTool(t, r, "get_search_content"), map[string]any{"responseId": dStr(result, "responseId"), "urlIndex": 0.0, "offset": float64(jsLen(content) - 21), "limit": 21.0})
		if dInt(t, got, "returnedChars") != 21 {
			t.Fatalf("%+v", got.Details)
		}
		matchRE(t, `Cached page content\.`, got.Text())
	})
	tw(t, F, "loaded cached fetched content expires after the result lifetime", func(t *testing.T) {
		r, h := setup(t, "")
		startedAt := nowMs()
		withClock(t, startedAt)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "expiring cache-backed content", Header: hdr("content-type", "text/plain")}
		})
		result := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/expiring-cache"})
		entry := fetchEntry(t, h)
		if entry.FetchCache == nil {
			t.Fatal("no cache ref")
		}
		restore(t, entry)
		get := mustTool(t, r, "get_search_content")
		loaded := run(t, get, map[string]any{"responseId": dStr(result, "responseId"), "urlIndex": 0.0})
		matchRE(t, `expiring cache-backed content`, loaded.Text())
		withClock(t, startedAt+60*60*1000)
		expired := run(t, get, map[string]any{"responseId": dStr(result, "responseId"), "urlIndex": 0.0})
		if dStr(expired, "error") != "Cached fetched content is missing or expired" {
			t.Fatalf("%+v", expired.Details)
		}
		matchRE(t, `Cached fetched content is missing or expired`, expired.Text())
		noMatchRE(t, `expiring cache-backed content`, expired.Text())
	})
	tw(t, F, "missing cache files return an actionable fetched-content error", func(t *testing.T) {
		r, h := setup(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "cache-backed content", Header: hdr("content-type", "text/plain")}
		})
		result := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/missing-cache"})
		entry := fetchEntry(t, h)
		if entry.FetchCache == nil {
			t.Fatal("no cache ref")
		}
		noErr(t, os.Remove(filepath.Join(FetchCacheDir(), entry.FetchCache.Key)))
		restore(t, entry)
		missing := run(t, mustTool(t, r, "get_search_content"), map[string]any{"responseId": dStr(result, "responseId"), "urlIndex": 0.0})
		if dStr(missing, "error") != "Cached fetched content is missing or expired" {
			t.Fatalf("%+v", missing.Details)
		}
		matchRE(t, `Cached fetched content is missing or expired`, missing.Text())
	})
}

func TestLegacyInlineEntryThroughTool(t *testing.T) {
	r, _ := newRuntime(t, "")
	ClearResults()
	RestoreFromEntries([]CustomEntry{{CustomType: "web-search-results", Data: []byte(`{"id":"legacy-fetch","type":"fetch","timestamp":` + itoa64(nowMs()) + `,"urls":[{"url":"https://example.com/legacy","title":"Legacy","content":"legacy inline content","error":null}]}`)}})
	out := run(t, mustTool(t, r, "get_search_content"), map[string]any{"responseId": "legacy-fetch", "urlIndex": 0.0})
	matchRE(t, `legacy inline content`, out.Text())
	if dInt(t, out, "contentLength") != len("legacy inline content") {
		t.Fatalf("%+v", out.Details)
	}
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }
