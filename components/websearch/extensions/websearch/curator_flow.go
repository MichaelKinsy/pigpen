package websearch

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// web_search with workflow "summary-review" (the curator) and "auto-summary" (index.ts:1862-2090,
// openCuratorBrowser at 1555-1727).

type cancelPartial struct {
	queries            []QueryResultData
	queryCount         int
	browserConnected   *bool
	lastHeartbeatAgeMs *int64
	curatorURL         string
	browserOpenError   string
}

// buildCurationCancelledReturn is buildCurationCancelledReturn: the result of a curation that
// ended without a submission, with the diagnostics the error renderer shows.
func buildCurationCancelledReturn(reason string, p cancelPartial) ToolOutput {
	message := fmt.Sprintf("Search curation cancelled (%s).", reason)
	details := map[string]any{
		"error":        message,
		"cancelled":    true,
		"cancelReason": reason,
	}
	if p.browserConnected != nil {
		details["browserConnected"] = *p.browserConnected
	}
	if p.lastHeartbeatAgeMs != nil {
		details["lastHeartbeatAgeMs"] = *p.lastHeartbeatAgeMs
	}
	if p.queryCount != 0 {
		details["queryCount"] = p.queryCount
	}
	if len(p.queries) > 0 {
		cancelled := make([]map[string]any, len(p.queries))
		for i, q := range p.queries {
			var provider, errValue any
			if q.Provider != "" {
				provider = q.Provider
			}
			if q.Error != nil {
				errValue = *q.Error
			}
			cancelled[i] = map[string]any{"query": q.Query, "provider": provider, "error": errValue, "resultCount": len(q.Results)}
		}
		details["cancelledQueries"] = cancelled
	}
	var extra []string
	if p.curatorURL != "" {
		extra = append(extra, "curator: "+p.curatorURL)
	}
	if p.browserOpenError != "" {
		extra = append(extra, "browser open error: "+p.browserOpenError)
	}
	if len(extra) > 0 {
		details["extraLines"] = extra
	}
	return textOutput(message, details)
}

func (r *Runtime) searchOptionsOf(pc *pendingCurate) SearchOptions {
	return SearchOptions{NumResults: pc.numResults, RecencyFilter: pc.recency, DomainFilter: pc.domainFilter, IncludeContent: pc.includeContent}
}

func (pc *pendingCurate) currentSearchProvider() ProviderSelection {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.searchProvider
}

var errCuratorInactive = errors.New("Curator session is no longer active.")

// openCuratorBrowser starts the curator server of a pending call, replays the results gathered so
// far into it and opens the page (openCuratorBrowser). searchesComplete says whether the initial
// searches are finished.
func (r *Runtime) openCuratorBrowser(callID string, pc *pendingCurate, searchesComplete bool) {
	s := &r.cur
	if s.pendingFor(callID) != pc {
		return
	}
	var handle *CuratorServerHandle
	progress := 0.5
	if searchesComplete {
		progress = 1
	}
	sendFallback := func(message string) {
		if handle == nil {
			return
		}
		pc.mu.Lock()
		openErr := pc.browserOpenError
		pc.mu.Unlock()
		details := map[string]any{"phase": "curator-fallback", "progress": progress, "curatorUrl": handle.URL, "timeoutSeconds": pc.timeoutSeconds, "shortcut": r.cfg.CurateShortcut}
		if openErr != "" {
			details["browserOpenError"] = openErr
		}
		pc.update(message+"\nOpen manually: "+handle.URL, details)
	}

	err := func() error {
		pc.mu.Lock()
		pc.phase = "curating"
		pc.mu.Unlock()
		searchCtx, searchAbort := context.WithCancel(pc.ctx)
		defer func() {
			// searchAbort is released with the session: submit and cancel cancel it, and
			// closing the curator ends the rest.
			_ = searchAbort
		}()
		sessionToken := randomUUID()
		var err error
		handle, err = StartCuratorServer(CuratorServerOptions{
			Queries:                    pc.queryList,
			InitialResultIndexCapacity: new(curatorResultIndexCapacity(len(pc.queryList))),
			SessionToken:               sessionToken,
			Timeout:                    float64(pc.timeoutSeconds),
			AvailableProviders:         pc.available,
			DefaultProvider:            pc.defaultProvider,
			SearchProvider:             searchProviderOfSelection(pc.currentSearchProvider()),
			SummaryModels:              pc.summaryModels,
			DefaultSummaryModel:        pc.defaultSummaryModel,
			Network:                    ResolveCuratorNetworkConfig(),
		}, r.curatedServerCallbacks(callID, pc, searchCtx, searchAbort))
		if err != nil {
			searchAbort()
			return err
		}
		if s.pendingFor(callID) != pc {
			handle.Close()
			searchAbort()
			return nil
		}
		s.setActive(callID, handle)
		pc.mu.Lock()
		pc.curatorURL = handle.URL
		slots := map[int]int{}
		for k, v := range pc.resultSlots {
			slots[k] = v
		}
		pc.mu.Unlock()

		for _, item := range pc.searchResults.inOrder() {
			var slot *int
			if v, ok := slots[item.index]; ok {
				slot = new(v)
			}
			data := item.data
			if data.Error != nil {
				handle.PushError(item.index, *data.Error, data.Provider, data.Query, slot)
				continue
			}
			sources := make([]CuratorSource, len(data.Results))
			for i, src := range data.Results {
				snippet := src.Snippet
				sources[i] = CuratorSource{Title: src.Title, URL: src.URL, Snippet: &snippet, Domain: extractDomain(src.URL)}
			}
			provider := data.Provider
			if provider == "" {
				provider = pc.defaultProvider
			}
			handle.PushResult(item.index, CuratorPush{CuratorSearchEntry: CuratorSearchEntry{Answer: data.Answer, Results: sources, Provider: provider}, Query: data.Query, SlotIndex: slot})
		}
		if searchesComplete {
			handle.SearchesDone()
		}
		text := "Searches streaming to browser..."
		if searchesComplete {
			text = "Waiting for summary approval in browser..."
		}
		pc.update(text, map[string]any{"phase": "curating", "progress": progress, "curatorUrl": handle.URL, "timeoutSeconds": pc.timeoutSeconds, "shortcut": r.cfg.CurateShortcut})

		root, err := ReadConfigRoot()
		if err != nil {
			return err
		}
		if !ShouldAutoOpenCuratorBrowser(root) {
			sendFallback("Search curator is running. Open the curator URL manually.")
			return nil
		}
		return r.openInBrowser(handle.URL)
	}()
	if err == nil {
		return
	}
	if handle != nil && s.activeFor(callID) == handle && s.pendingFor(callID) == pc {
		pc.mu.Lock()
		pc.browserOpenError = err.Error()
		pc.mu.Unlock()
		sendFallback("Search curator is running, but the browser did not open automatically.")
	} else if s.pendingFor(callID) == pc || (handle != nil && s.activeFor(callID) == handle) {
		r.CloseCurator(callID)
	}
}

// searchProviderOfSelection is toCuratorProvider(...) ?? "auto".
func searchProviderOfSelection(sel ProviderSelection) string {
	if p := curatorProvider(sel); p != "" {
		return p
	}
	return "auto"
}

// curatedServerCallbacks are the hooks of the curator server of a curated web_search call.
func (r *Runtime) curatedServerCallbacks(callID string, pc *pendingCurate, searchCtx context.Context, searchAbort context.CancelFunc) CuratorServerCallbacks {
	s := &r.cur
	active := func() bool { return s.pendingFor(callID) == pc }
	return CuratorServerCallbacks{
		OnSummarize: func(ctx context.Context, selected []int, model, feedback string) (SummaryDraft, error) {
			ctx, err := ScopeProxy(ctx, pc.proxy)
			if err != nil {
				return SummaryDraft{}, err
			}
			if !active() {
				return SummaryDraft{}, errCuratorInactive
			}
			pc.update("Generating summary draft...", map[string]any{"phase": "generating-summary", "progress": 0.9, "curatorUrl": pc.curatorURLNow(), "timeoutSeconds": pc.timeoutSeconds, "shortcut": r.cfg.CurateShortcut})
			draft, err := generateSummaryForSelectedIndices(ctx, selected, pc.searchResults.snapshot(), pc.sc, model, feedback)
			if err != nil {
				return SummaryDraft{}, err
			}
			if !active() {
				return SummaryDraft{}, errCuratorInactive
			}
			pc.update("Summary draft ready — waiting for approval...", map[string]any{"phase": "waiting-for-approval", "progress": 1, "curatorUrl": pc.curatorURLNow(), "timeoutSeconds": pc.timeoutSeconds, "shortcut": r.cfg.CurateShortcut})
			return draft, nil
		},
		OnSubmit: func(p CuratorSubmitPayload) {
			if !active() {
				return
			}
			if p.AutoApproveRemainingSearches {
				r.cur.run.ApproveRemainingSearches()
			}
			searchAbort()
			snap := pc.searchResults.snapshot()
			var results []QueryResultData
			var urls []string
			if len(p.SelectedQueryIndices) > 0 {
				results, urls = filterByQueryIndices(p.SelectedQueryIndices, snap)
			} else {
				results, urls = collectAllResultsAndURLs(snap)
			}
			base := searchReturn{
				queryList: queriesOf(results), results: results, urls: urls, includeContent: pc.includeContent,
				inline: pc.inlineFor(urls), curated: true, curatedFrom: len(snap), proxy: pc.proxy,
			}
			if !p.RawResults {
				summary, meta := resolveSummaryForSubmit(p.SelectedQueryIndices, p.Summary, p.SummaryMeta, snap)
				base.workflow, base.approvedSummary, base.summaryMeta = pc.workflow, summary, &meta
			}
			pc.finish(r.buildSearchReturn(base))
			r.CloseCurator(callID)
		},
		OnCancel: func(reason string) {
			if !active() {
				return
			}
			searchAbort()
			snap := pc.searchResults.snapshot()
			if reason == "timeout" {
				summary, meta := resolveSummaryForSubmit(nil, "", nil, snap)
				results, urls := collectAllResultsAndURLs(snap)
				pc.finish(r.buildSearchReturn(searchReturn{
					queryList: queriesOf(results), results: results, urls: urls, includeContent: pc.includeContent,
					inline: pc.inlineFor(urls), curated: true, curatedFrom: len(snap), workflow: pc.workflow,
					approvedSummary: summary, summaryMeta: &meta, proxy: pc.proxy,
				}))
			} else {
				pc.finish(buildCurationCancelledReturn(reason, r.cancelPartialOf(callID, pc)))
			}
			r.CloseCurator(callID)
		},
		OnProviderChange: func(provider string) {
			if !active() {
				return
			}
			sel, err := NormalizeSearchProviderSelection(provider, "provider")
			if err != nil || sel.IsList() || sel.Name == "auto" {
				return
			}
			pc.mu.Lock()
			pc.defaultProvider = sel.Name
			pc.searchProvider = sel
			pc.mu.Unlock()
			_ = SaveConfigValue("provider", sel.Name) // the original logs a failure and goes on
		},
		OnAddSearch: func(query string, provider *string) ([]CuratorSearchEntry, error) {
			ctx, err := ScopeProxy(searchCtx, pc.proxy)
			if err != nil {
				return nil, err
			}
			if !active() {
				return nil, errCuratorInactive
			}
			requested, err := resolveCuratorSearchProvider(provider, pc.currentSearchProvider())
			if err != nil {
				return nil, err
			}
			resp, err := Search(ctx, query, FullSearchOptions{SearchOptions: r.searchOptionsOf(pc), Provider: requested})
			if err != nil {
				return nil, err
			}
			if !active() {
				return nil, errCuratorInactive
			}
			pc.addInline(resp.InlineContent)
			return toCuratorSearchEntries(resp), nil
		},
		OnAddSearchResults: func(entries []IndexedCuratorSearchEntry) {
			if !active() {
				return
			}
			for _, e := range entries {
				pc.searchResults.set(e.QueryIndex, indexedCuratorEntryToQueryResult(e))
			}
		},
		OnRewriteQuery: func(ctx context.Context, query string) (string, error) {
			ctx, err := ScopeProxy(ctx, pc.proxy)
			if err != nil {
				return "", err
			}
			if !active() {
				return "", errCuratorInactive
			}
			if pc.sc == nil {
				return "", errors.New("Query rewrite requires an active extension context")
			}
			return RewriteSearchQuery(ctx, query, pc.sc)
		},
	}
}

func (pc *pendingCurate) curatorURLNow() string {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.curatorURL
}

func (pc *pendingCurate) addInline(in []ExtractedContent) {
	if len(in) == 0 {
		return
	}
	pc.mu.Lock()
	pc.allInline = append(pc.allInline, in...)
	pc.mu.Unlock()
}

// inlineFor is the inline content of the given URLs, nil when there is none.
func (pc *pendingCurate) inlineFor(urls []string) []ExtractedContent {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	var out []ExtractedContent
	for _, c := range pc.allInline {
		if sliceHas(urls, c.URL) {
			out = append(out, c)
		}
	}
	return out
}

func queriesOf(results []QueryResultData) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Query
	}
	return out
}

// cancelPartialOf is the partial state a cancelled curation reports.
func (r *Runtime) cancelPartialOf(callID string, pc *pendingCurate) cancelPartial {
	p := cancelPartial{queries: resultsOf(pc.searchResults.inOrderSorted()), queryCount: len(pc.queryList)}
	if h := r.cur.activeFor(callID); h != nil {
		st := h.ConnectionState()
		p.browserConnected, p.lastHeartbeatAgeMs = &st.BrowserConnected, &st.LastHeartbeatAgeMs
	}
	pc.mu.Lock()
	p.curatorURL, p.browserOpenError = pc.curatorURL, pc.browserOpenError
	pc.mu.Unlock()
	return p
}

func resultsOf(in []indexedResult) []QueryResultData {
	out := make([]QueryResultData, len(in))
	for i, e := range in {
		out[i] = e.data
	}
	return out
}

// inOrderSorted lists the entries by result index (orderedSearchResults).
func (s *resultSet) inOrderSorted() []indexedResult {
	items := s.inOrder()
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].index < items[j-1].index; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return items
}

// curatedWebSearch runs web_search with workflow "summary-review": the searches stream into the
// curator page while the call waits for the user to submit, or for the session to time out.
func (r *Runtime) curatedWebSearch(ctx context.Context, params map[string]any, update func(ToolOutput), queries []string, recency string) (ToolOutput, error) {
	s := &r.cur
	callID := ToolCallIDFrom(ctx)
	r.CloseCurator(callID)

	opts := searchOptionsFrom(params, recency)
	requested, err := ResolveRequestedProvider(params["provider"])
	if err != nil {
		return ToolOutput{}, err
	}
	sc := SideCallContextFrom(ctx)
	var model map[string]any
	if sc != nil {
		model = sc.Model()
	}
	bootstrap, err := loadCuratorBootstrap(requested, model, curatorDefaultOptions{opts.NumResults, recency})
	if err != nil {
		return ToolOutput{}, err
	}
	var choices []SummaryModelChoice
	var defaultModel *string
	if sc != nil {
		if choices, defaultModel, err = loadSummaryModelChoices(sc); err != nil {
			return ToolOutput{}, err
		}
	}
	var proxy *string
	if p, ok := params["proxy"].(string); ok {
		proxy = &p
	}
	ctx, err = ScopeProxy(ctx, proxy)
	if err != nil {
		return ToolOutput{}, err
	}

	searchCtx, searchAbort := context.WithCancel(ctx)
	defer searchAbort()
	result := make(chan ToolOutput, 1)
	var (
		cancelledMu sync.Mutex
		cancelled   bool
	)
	isCancelled := func() bool {
		cancelledMu.Lock()
		defer cancelledMu.Unlock()
		return cancelled
	}
	pc := &pendingCurate{
		phase: "searching", workflow: "summary-review", sc: sc, searchResults: newResultSet(), resultSlots: map[int]int{},
		queryList: queries, includeContent: opts.IncludeContent, numResults: opts.NumResults, recency: recency,
		domainFilter: opts.DomainFilter, available: bootstrap.available, defaultProvider: bootstrap.defaultProvider,
		searchProvider: requested, summaryModels: choices, defaultSummaryModel: defaultModel,
		timeoutSeconds: bootstrap.timeoutSeconds, proxy: proxy, onUpdate: update, ctx: ctx,
		abortSearches: searchAbort, browserDone: make(chan struct{}),
	}
	pc.finish = func(v ToolOutput) {
		cancelledMu.Lock()
		if cancelled {
			cancelledMu.Unlock()
			return
		}
		cancelled = true
		cancelledMu.Unlock()
		pc.abortSearches()
		s.deletePending(callID)
		result <- v
	}
	pc.cancel = func(reason string) {
		if isCancelled() {
			return
		}
		pc.finish(buildCurationCancelledReturn(reason, r.cancelPartialOf(callID, pc)))
	}

	s.setPending(callID, pc)
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			r.CloseCurator(callID)
		case <-stopWatch:
		}
	}()
	firstOpen := pc.browserDone
	go func() {
		defer close(firstOpen)
		r.openCuratorBrowser(callID, pc, false)
	}()

	var progressMu sync.Mutex
	completed := 0
	report := func(text string, query string) {
		pc.update(text, map[string]any{"phase": "searching", "progress": float64(completed) / float64(len(queries)), "currentQuery": query})
	}
	stopped := func() bool { return ctx.Err() != nil || isCancelled() || searchCtx.Err() != nil }
	runSearchQueries(queries, func(query string, qi int) struct{} {
		if stopped() {
			return struct{}{}
		}
		progressMu.Lock()
		report(fmt.Sprintf(`Searching "%s" (%d/%d complete)...`, query, completed, len(queries)), query)
		progressMu.Unlock()
		requestedNow := pc.currentSearchProvider()
		resp, err := Search(searchCtx, query, FullSearchOptions{SearchOptions: opts, Provider: requestedNow})
		defer func() {
			progressMu.Lock()
			completed++
			if !stopped() {
				report(fmt.Sprintf("Completed %d/%d searches.", completed, len(queries)), query)
			}
			progressMu.Unlock()
		}()
		if stopped() {
			return struct{}{}
		}
		if err != nil {
			message := err.Error()
			failed := curatorProvider(requestedNow)
			pc.searchResults.set(qi, QueryResultData{Query: query, Results: []SearchResult{}, Error: &message, Provider: failed})
			pc.mu.Lock()
			pc.resultSlots[qi] = qi
			pc.mu.Unlock()
			if h := s.activeFor(callID); h != nil {
				h.PushError(qi, message, failed, query, new(qi))
			}
			return struct{}{}
		}
		pc.addInline(resp.InlineContent)
		entries := toCuratorSearchEntries(resp)
		h := s.activeFor(callID)
		for entryIndex, entry := range entries {
			index := curatorResultIndex(qi, entryIndex, len(queries))
			indexed := IndexedCuratorSearchEntry{CuratorSearchEntry: entry, QueryIndex: index, Query: query}
			pc.searchResults.set(index, indexedCuratorEntryToQueryResult(indexed))
			pc.mu.Lock()
			pc.resultSlots[index] = qi
			pc.mu.Unlock()
			if h != nil {
				if entry.Error != "" {
					h.PushError(index, entry.Error, entry.Provider, query, new(qi))
				} else {
					h.PushResult(index, CuratorPush{CuratorSearchEntry: entry, Query: query, SlotIndex: new(qi)})
				}
			}
		}
		return struct{}{}
	}, func(query string, err error) struct{} { return struct{}{} })

	if stopped() {
		pc.cancel("stale")
		return <-result, nil
	}
	pc.mu.Lock()
	browserDone := pc.browserDone
	pc.mu.Unlock()
	<-browserDone
	if h := s.activeFor(callID); h != nil && !isCancelled() {
		h.SearchesDone()
		pc.mu.Lock()
		openErr, curatorURL := pc.browserOpenError, pc.curatorURL
		pc.mu.Unlock()
		if openErr != "" {
			pc.update("All searches complete. Open the curator manually: "+curatorURL, map[string]any{
				"phase": "curator-fallback", "progress": 1, "curatorUrl": curatorURL, "timeoutSeconds": pc.timeoutSeconds,
				"shortcut": r.cfg.CurateShortcut, "browserOpenError": openErr,
			})
		} else {
			pc.update("All searches complete — waiting for summary approval in browser...", map[string]any{
				"phase": "curating", "progress": 1, "curatorUrl": curatorURL, "timeoutSeconds": pc.timeoutSeconds, "shortcut": r.cfg.CurateShortcut,
			})
		}
	}
	return <-result, nil
}
