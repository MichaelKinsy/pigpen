package websearch

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// The search curator workflows of index.ts (web_search with workflow "summary-review" and
// "auto-summary", the /websearch command): a local page where the user reviews results and the
// generated summary before they reach the model. This file holds the shared state and helpers;
// curator_flow.go runs the web_search workflow, curator_commands.go the commands.

// toolCallKey carries the tool call id of the running tool (ctx.toolCallId upstream: the key of
// the per-call curator state).
type toolCallKey struct{}

// WithToolCallID attaches the id of the running tool call to ctx.
func WithToolCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolCallKey{}, id)
}

var fallbackCallSeq struct {
	sync.Mutex
	n int
}

// ToolCallIDFrom returns the id attached by WithToolCallID; a call without one (a test) gets a
// fresh unique id so parallel calls never share curator state.
func ToolCallIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(toolCallKey{}).(string); ok && id != "" {
		return id
	}
	fallbackCallSeq.Lock()
	defer fallbackCallSeq.Unlock()
	fallbackCallSeq.n++
	return fmt.Sprintf("call-%d", fallbackCallSeq.n)
}

// CuratorHost is what the curator flows need from the running session besides the model context:
// notifications, follow-up messages with details, and starting the browser. The SDK adapter
// implements it; a Host without it runs the workflows silently.
type CuratorHost interface {
	Notify(message, level string)
	// SendCustomMessage is pi.sendMessage with block content and details.
	SendCustomMessage(customType string, content []map[string]any, details map[string]any, triggerTurn bool, deliverAs string)
	// Exec is pi.exec: it runs the program and waits.
	Exec(command string, args []string) (code int, stderr string, err error)
}

func (r *Runtime) curatorHost() CuratorHost {
	h, _ := r.host.(CuratorHost)
	return h
}

func (r *Runtime) notify(message, level string) {
	if h := r.curatorHost(); h != nil {
		h.Notify(message, level)
	}
}

// resultSet is a Map<number, QueryResultData>: lookups by result index and iteration in the order
// the keys were first set.
type resultSet struct {
	mu    sync.Mutex
	m     map[int]QueryResultData
	order []int
}

func newResultSet() *resultSet { return &resultSet{m: map[int]QueryResultData{}} }

func (s *resultSet) set(i int, d QueryResultData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, present := s.m[i]; !present {
		s.order = append(s.order, i)
	}
	s.m[i] = d
}

func (s *resultSet) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// snapshot is a copy of the map for the pure helpers.
func (s *resultSet) snapshot() map[int]QueryResultData {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]QueryResultData, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

type indexedResult struct {
	index int
	data  QueryResultData
}

// inOrder lists the entries in insertion order.
func (s *resultSet) inOrder() []indexedResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]indexedResult, len(s.order))
	for i, k := range s.order {
		out[i] = indexedResult{k, s.m[k]}
	}
	return out
}

// pendingCurate is PendingCurate: one curated web_search call.
type pendingCurate struct {
	mu sync.Mutex

	phase               string // "searching" or "curating"
	workflow            string
	sc                  SideCallContext
	searchResults       *resultSet
	resultSlots         map[int]int
	allInline           []ExtractedContent
	queryList           []string
	includeContent      bool
	numResults          *float64
	recency             string
	domainFilter        []string
	available           ProviderAvailability
	defaultProvider     string
	searchProvider      ProviderSelection
	summaryModels       []SummaryModelChoice
	defaultSummaryModel *string
	timeoutSeconds      int
	proxy               *string
	curatorURL          string
	onUpdate            func(ToolOutput)
	ctx                 context.Context
	abortSearches       func()
	finish              func(ToolOutput)
	cancel              func(reason string)
	browserDone         chan struct{}
	browserOpenError    string
}

func (pc *pendingCurate) update(text string, details map[string]any) {
	if pc.onUpdate != nil {
		pc.onUpdate(textOutput(text, details))
	}
}

// curatorState is the module-level state of index.ts: the pending curated calls and the running
// curator servers, keyed by tool call id (the /websearch command uses "cmd:<token>").
type curatorState struct {
	mu      sync.Mutex
	pending map[string]*pendingCurate
	// pendingOrder lists the pending call ids in insertion order (a Map's iteration order).
	pendingOrder []string
	active       map[string]*CuratorServerHandle
	run          CuratorRunState

	widgetVisible bool
	widgetUnsub   func()
}

func (s *curatorState) pendingFor(id string) *pendingCurate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[id]
}

func (s *curatorState) activeFor(id string) *CuratorServerHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[id]
}

func (s *curatorState) setPending(id string, pc *pendingCurate) {
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]*pendingCurate{}
	}
	if _, present := s.pending[id]; !present {
		s.pendingOrder = append(s.pendingOrder, id)
	}
	s.pending[id] = pc
	s.mu.Unlock()
}

func (s *curatorState) deletePending(id string) {
	s.mu.Lock()
	s.deletePendingLocked(id)
	s.mu.Unlock()
}

func (s *curatorState) deletePendingLocked(id string) {
	delete(s.pending, id)
	for i, v := range s.pendingOrder {
		if v == id {
			s.pendingOrder = append(s.pendingOrder[:i], s.pendingOrder[i+1:]...)
			break
		}
	}
}

// lastPending is the most recently added pending call.
func (s *curatorState) lastPending() (string, *pendingCurate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingOrder) == 0 {
		return "", nil
	}
	id := s.pendingOrder[len(s.pendingOrder)-1]
	return id, s.pending[id]
}

func (s *curatorState) setActive(id string, h *CuratorServerHandle) {
	s.mu.Lock()
	if s.active == nil {
		s.active = map[string]*CuratorServerHandle{}
	}
	s.active[id] = h
	s.mu.Unlock()
}

// CuratorRun is the per-prompt approval state (registerCuratorRunLifecycle's CuratorRunState).
func (r *Runtime) CuratorRun() *CuratorRunState { return &r.cur.run }

// CloseCurator is closeCurator(callId): the call's pending state is cancelled as stale and its
// server closed. An empty id closes every curator.
func (r *Runtime) CloseCurator(callID string) {
	s := &r.cur
	if callID != "" {
		s.mu.Lock()
		pc := s.pending[callID]
		s.mu.Unlock()
		if pc != nil {
			pc.cancel("stale")
		}
		s.mu.Lock()
		s.deletePendingLocked(callID)
		handle := s.active[callID]
		delete(s.active, callID)
		s.mu.Unlock()
		if handle != nil {
			handle.Close()
		}
		return
	}
	s.mu.Lock()
	pending := make([]*pendingCurate, 0, len(s.pending))
	for _, pc := range s.pending {
		pending = append(pending, pc)
	}
	s.pending, s.pendingOrder = map[string]*pendingCurate{}, nil
	handles := make([]*CuratorServerHandle, 0, len(s.active))
	for _, h := range s.active {
		handles = append(handles, h)
	}
	s.active = map[string]*CuratorServerHandle{}
	s.mu.Unlock()
	for _, pc := range pending {
		pc.cancel("stale")
	}
	for _, h := range handles {
		h.Close()
	}
}

// ---- helpers ------------------------------------------------------------------------------------

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func extractDomain(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host
}

// toCuratorSearchEntries is toCuratorSearchEntries: one entry per provider of an aggregate answer,
// then one per provider failure.
func toCuratorSearchEntries(resp *AttributedSearchResponse) []CuratorSearchEntry {
	responses := []SearchResponse{resp.SearchResponse}
	providers := []string{resp.Provider}
	if resp.Provider == "all" && len(resp.ProviderResponses) > 0 {
		responses, providers = nil, nil
		for _, pr := range resp.ProviderResponses {
			responses = append(responses, pr.SearchResponse)
			providers = append(providers, pr.Provider)
		}
	}
	entries := make([]CuratorSearchEntry, 0, len(responses)+len(resp.ProviderErrors))
	for i, res := range responses {
		sources := make([]CuratorSource, len(res.Results))
		for j, s := range res.Results {
			snippet := s.Snippet
			sources[j] = CuratorSource{Title: s.Title, URL: s.URL, Snippet: &snippet, Domain: extractDomain(s.URL)}
		}
		entries = append(entries, CuratorSearchEntry{Answer: res.Answer, Results: sources, Provider: providers[i]})
	}
	for _, failure := range resp.ProviderErrors {
		entries = append(entries, CuratorSearchEntry{Results: []CuratorSource{}, Provider: failure.Provider, Error: failure.Error})
	}
	return entries
}

// indexedCuratorEntryToQueryResult is indexedCuratorEntryToQueryResult.
func indexedCuratorEntryToQueryResult(e IndexedCuratorSearchEntry) QueryResultData {
	results := make([]SearchResult, len(e.Results))
	for i, s := range e.Results {
		results[i] = SearchResult{Title: s.Title, URL: s.URL}
		if s.Snippet != nil {
			results[i].Snippet = *s.Snippet
		}
	}
	data := QueryResultData{Query: e.Query, Answer: e.Answer, Results: results, Provider: e.Provider}
	if e.Error != "" {
		msg := e.Error
		data.Error = &msg
	}
	return data
}

// curatorResultIndex keeps primary query indexes stable for curator slots and interleaves the
// additional provider entries deterministically (curatorResultIndex).
func curatorResultIndex(queryIndex, entryIndex, queryCount int) int {
	return entryIndex*queryCount + queryIndex
}

func curatorResultIndexCapacity(queryCount int) int { return queryCount * len(ResolvedProviders) }

// resolveCuratorSearchProvider is resolveCuratorSearchProvider: the browser's provider wins unless
// it is auto, or "all" while the current selection is a provider list.
func resolveCuratorSearchProvider(requested *string, current ProviderSelection) (ProviderSelection, error) {
	if requested == nil {
		return current, nil
	}
	normalized, err := NormalizeSearchProviderSelection(*requested, "provider")
	if err != nil {
		return current, err
	}
	if normalized.Name == "auto" && !normalized.IsList() {
		return current, nil
	}
	if normalized.Name == "all" && !normalized.IsList() && current.IsList() {
		return current, nil
	}
	return normalized, nil
}

// ---- provider availability and default ------------------------------------------------------------------

// curatorAvailabilityOrder is the key order of getProviderAvailability's object.
var curatorAvailabilityOrder = []string{
	"openai", "brave", "parallel", "parallel-mcp", "tinyfish", "search1api", "searchinfinity", "querit", "tavily", "you",
	"firecrawl", "jina", "serpdive", "kagi", "bocha", "ollama", "searxng", "duckduckgo", "perplexity", "exa", "gemini",
	"kimi", "anysearch", "xcrawl", "xai", "mistral", "brightdata", "serpbase", "serpapi", "serper", "serply", "baizhi", "valyu",
}

// GetProviderAvailability is getProviderAvailability: a provider the allowlist excludes is never
// available; "all" is available when any provider that "all" searches is.
func GetProviderAvailability() (ProviderAvailability, error) {
	allowed, err := AllowedSearchProviders()
	if err != nil {
		return ProviderAvailability{}, err
	}
	flags := map[string]bool{}
	for _, p := range curatorAvailabilityOrder {
		flags[p] = sliceHas(allowed, p) && isProviderAvailable(p)
	}
	all := false
	for _, p := range AllSearchProviders {
		if flags[p] {
			all = true
			break
		}
	}
	var out ProviderAvailability
	out.Set("all", all)
	for _, p := range curatorAvailabilityOrder {
		out.Set(p, flags[p])
	}
	return out, nil
}

type curatorDefaultOptions struct {
	numResults *float64
	recency    string
}

func shouldUseOpenAICodexDefault(model map[string]any) bool {
	provider, _ := model["provider"].(string)
	return provider == "openai-codex"
}

func shouldPreferOpenAI(o curatorDefaultOptions, preferCodex bool) bool {
	if o.recency != "" {
		return false
	}
	if o.numResults != nil && !math.IsNaN(*o.numResults) && !math.IsInf(*o.numResults, 0) && math.Floor(*o.numResults) != 5 {
		return false
	}
	return preferCodex
}

func firstAvailableProvider(avail ProviderAvailability, preferOpenAI bool, fallback string) string {
	if avail.Get("searxng") {
		return "searxng"
	}
	if preferOpenAI && avail.Get("openai") {
		return "openai"
	}
	if avail.Get("exa") {
		return "exa"
	}
	for _, p := range AllSearchProviders {
		if p == "ollama" && avail.Get("bocha") {
			return "bocha"
		}
		if avail.Get(p) {
			return p
		}
	}
	if allowed, err := AllowedSearchProviders(); err == nil && sliceHas(allowed, fallback) {
		return fallback
	}
	return "auto"
}

// ResolveCuratorDefaultProvider is resolveCuratorDefaultProvider: the provider the curator page
// starts on for the selection, the availability, the current model and the search options.
func ResolveCuratorDefaultProvider(provider ProviderSelection, avail ProviderAvailability, model map[string]any, numResults *float64, recency string) string {
	return resolveCuratorProvider(provider, avail, curatorDefaultOptions{numResults, recency}, shouldUseOpenAICodexDefault(model), model)
}

func resolveCuratorProvider(provider ProviderSelection, avail ProviderAvailability, o curatorDefaultOptions, preferCodex bool, model map[string]any) string {
	if provider.IsList() {
		return "all"
	}
	preferOpenAI := shouldPreferOpenAI(o, preferCodex)
	switch {
	case provider.Name == "auto":
		cfg, err := loadSearchConfig()
		if err == nil && cfg.routing != nil {
			eligible := func(candidate string) bool {
				return !(candidate == "openai" && cfg.routing.UseCurrentModel != nil && *cfg.routing.UseCurrentModel && !modelHostedSearchEligible(model))
			}
			for _, candidate := range cfg.routing.Providers {
				if !eligible(candidate) {
					continue
				}
				if avail.Get(candidate) {
					return candidate
				}
			}
			for _, candidate := range cfg.routing.Providers {
				if eligible(candidate) {
					return candidate
				}
			}
			return cfg.routing.Providers[0]
		}
		return firstAvailableProvider(avail, preferOpenAI, "exa")
	case provider.Name == "all":
		if avail.Get("all") {
			return "all"
		}
		return firstAvailableProvider(avail, preferOpenAI, "exa")
	case sliceHas(AllSearchProviders, provider.Name) && !avail.Get(provider.Name):
		prefer := preferOpenAI
		if provider.Name == "openai" {
			prefer = false
		}
		return firstAvailableProvider(avail, prefer, provider.Name)
	}
	return provider.Name
}

// curatorBootstrap is CuratorBootstrap.
type curatorBootstrap struct {
	available       ProviderAvailability
	defaultProvider string
	timeoutSeconds  int
}

// loadCuratorBootstrap is loadCuratorBootstrap for an already resolved provider selection:
// availability, the page's default provider and the idle timeout.
func loadCuratorBootstrap(provider ProviderSelection, model map[string]any, o curatorDefaultOptions) (curatorBootstrap, error) {
	avail, err := GetProviderAvailability()
	if err != nil {
		return curatorBootstrap{}, err
	}
	if provider.IsList() {
		avail.Set("all", true)
	}
	timeout, err := CuratorTimeoutSeconds()
	if err != nil {
		return curatorBootstrap{}, err
	}
	return curatorBootstrap{
		available:       avail,
		defaultProvider: resolveCuratorProvider(provider, avail, o, shouldUseOpenAICodexDefault(model), model),
		timeoutSeconds:  timeout,
	}, nil
}

// ---- summaries ----------------------------------------------------------------------------------------

func modelValue(model map[string]any) string {
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	return provider + "/" + id
}

// loadSummaryModelChoices is loadSummaryModelChoices: the models of the summary dropdown (the
// available models inside the scope, then the current model) and the default one (the configured
// summaryModel when it is available, else the first preferred model that is).
func loadSummaryModelChoices(sc SideCallContext) ([]SummaryModelChoice, *string, error) {
	choices := []SummaryModelChoice{}
	seen := map[string]bool{}
	available := map[string]bool{}
	add := func(value string) {
		if !seen[value] {
			seen[value] = true
			choices = append(choices, SummaryModelChoice{Value: value, Label: value})
		}
	}
	registry := sc.Registry()
	models, err := registry.GetAvailable()
	if err != nil {
		return nil, nil, err
	}
	scoped, err := sc.ScopedModels()
	if err != nil {
		return nil, nil, err
	}
	for _, m := range models {
		if !IsModelInScope(m, scoped) {
			continue
		}
		available[modelValue(m)] = true
		add(modelValue(m))
	}
	if current := sc.Model(); current != nil {
		if v := modelValue(current); !seen[v] && IsModelInScope(current, scoped) {
			add(v)
		}
	}
	root, err := ReadConfigRoot()
	if err != nil {
		return nil, nil, err
	}
	configured := ""
	if s, ok := root["summaryModel"].(string); ok {
		configured = jsTrim(s)
	}
	resolveAvailable := func(selector string) (string, bool, error) {
		value, _ := SplitThinkingSuffix(selector)
		slash := strings.Index(value, "/")
		if slash <= 0 || slash >= len(value)-1 {
			return "", false, nil
		}
		model, err := FindModelWithProviderRouting(registry, value[:slash], value[slash+1:])
		if err != nil {
			return "", false, err
		}
		if model == nil || !available[modelValue(model)] {
			return "", false, nil
		}
		if selector != modelValue(model) && !seen[selector] {
			seen[selector] = true
			choices = append(choices, SummaryModelChoice{Value: selector, Label: selector})
		}
		return selector, true, nil
	}
	var def *string
	if configured != "" {
		if available[configured] {
			def = &configured
		} else if v, ok, err := resolveAvailable(configured); err != nil {
			return nil, nil, err
		} else if ok {
			def = &v
		}
	}
	if def == nil {
		for _, p := range preferredSummaryModels {
			model, err := FindModelWithProviderRouting(registry, p.provider, p.id)
			if err != nil {
				return nil, nil, err
			}
			if model == nil {
				continue
			}
			if v := modelValue(model); available[v] {
				def = &v
				break
			}
		}
	}
	return choices, def, nil
}

// generateSummaryForSelectedIndices is generateSummaryForSelectedIndices: a summary of the
// selected results; an empty model answer falls back to the deterministic summary.
func generateSummaryForSelectedIndices(ctx context.Context, selected []int, results map[int]QueryResultData, sc SideCallContext, model, feedback string) (SummaryDraft, error) {
	var picked []QueryResultData
	for _, qi := range selected {
		if res, ok := results[qi]; ok {
			picked = append(picked, res)
		}
	}
	if len(picked) == 0 {
		return SummaryDraft{}, errors.New("No selected results available for summary generation")
	}
	if sc == nil {
		return SummaryDraft{}, errors.New("Summary generation requires an active extension context")
	}
	draft, err := GenerateSummaryDraft(ctx, picked, sc, SummaryOptions{ModelOverride: model, Feedback: feedback, DeadlineMs: GetSummaryGenerationDeadlineMs()})
	if err != nil {
		if !strings.Contains(err.Error(), "Summary model returned empty response") {
			return SummaryDraft{}, err
		}
		det := BuildDeterministicSummary(picked)
		det.Meta.FallbackReason = "summary-model-empty-response"
		return det, nil
	}
	return draft, nil
}

// completeSummaryMeta is index.ts's normalizeSummaryMeta: the metadata of an approved summary with
// every field filled in.
func completeSummaryMeta(meta *SummaryMeta, summary string) SummaryMeta {
	text := jsTrim(summary)
	estimate := 0
	if text != "" {
		estimate = max(1, (jsLen(text)+3)/4)
	}
	if meta == nil {
		return SummaryMeta{TokenEstimate: estimate}
	}
	out := *meta
	if out.DurationMs < 0 {
		out.DurationMs = 0
	}
	if out.TokenEstimate < 0 {
		out.TokenEstimate = estimate
	}
	return out
}

// resolveSummaryForSubmit is resolveSummaryForSubmit: the submitted summary, or the deterministic
// one over the selected (else all) results.
func resolveSummaryForSubmit(selected []int, summary string, meta *SummaryMeta, results map[int]QueryResultData) (string, SummaryMeta) {
	if submitted := jsTrim(summary); submitted != "" {
		return submitted, completeSummaryMeta(meta, submitted)
	}
	picked, _ := filterByQueryIndices(selected, results)
	if len(picked) == 0 {
		picked = orderedSearchResults(results)
	}
	det := BuildDeterministicSummary(picked)
	return det.Summary, det.Meta
}

func filterByQueryIndices(selected []int, results map[int]QueryResultData) ([]QueryResultData, []string) {
	var filtered []QueryResultData
	var urls []string
	for _, qi := range selected {
		r, ok := results[qi]
		if !ok {
			continue
		}
		filtered = append(filtered, r)
		for _, res := range r.Results {
			if !sliceHas(urls, res.URL) {
				urls = append(urls, res.URL)
			}
		}
	}
	return filtered, urls
}

func orderedSearchResults(results map[int]QueryResultData) []QueryResultData {
	keys := make([]int, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]QueryResultData, len(keys))
	for i, k := range keys {
		out[i] = results[k]
	}
	return out
}

func collectAllResultsAndURLs(results map[int]QueryResultData) ([]QueryResultData, []string) {
	ordered := orderedSearchResults(results)
	var urls []string
	for _, r := range ordered {
		for _, s := range r.Results {
			if !sliceHas(urls, s.URL) {
				urls = append(urls, s.URL)
			}
		}
	}
	return ordered, urls
}

// ---- opening the browser ---------------------------------------------------------------------------------

const xdgOpenGrace = 100 * time.Millisecond

// openInBrowser is openInBrowser: macOS and Windows go through pi.exec (`open`, `cmd /c start`);
// every other system starts `xdg-open` detached, waits up to 100 ms for an immediate failure and
// then lets it run on.
func (r *Runtime) openInBrowser(target string) error {
	if r.openBrowser != nil {
		return r.openBrowser(target)
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		h := r.curatorHost()
		if h == nil {
			return errors.New("no host to start the browser with")
		}
		var code int
		var stderr string
		var err error
		if runtime.GOOS == "darwin" {
			code, stderr, err = h.Exec("open", []string{target})
		} else {
			code, stderr, err = h.Exec("cmd", []string{"/c", "start", "", target})
		}
		if err != nil {
			return err
		}
		if code != 0 {
			if stderr != "" {
				return errors.New(stderr)
			}
			return fmt.Errorf("Failed to open browser (exit code %d)", code)
		}
		return nil
	}
	cmd := exec.Command("xdg-open", target)
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("spawn xdg-open ENOENT")
		}
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timer := time.NewTimer(xdgOpenGrace)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case err := <-exited:
		if err == nil {
			return nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("Failed to open browser (exit code %d)", ee.ExitCode())
		}
		return fmt.Errorf("Failed to open browser (exit code unknown)")
	}
}
