package websearch

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
)

// web_search and source_check (index.ts:1829-2524), workflow "none" only.

// ResolveRequestedProvider is resolveRequestedProvider(): an explicit provider wins, else the
// configured one, else auto. A provider outside webSearch.allowedProviders fails.
func ResolveRequestedProvider(requested any) (ProviderSelection, error) {
	if requested != nil {
		sel, err := NormalizeSearchProviderSelection(requested, "provider")
		if err != nil {
			return sel, err
		}
		if !(sel.Name == "auto" && !sel.IsList()) {
			allowed, err := AllowedSearchProviders()
			if err != nil {
				return sel, err
			}
			if err := AssertProviderSelectionAllowed(sel, "Requested provider", allowed); err != nil {
				return sel, err
			}
			return sel, nil
		}
	}
	root, err := ReadConfigRoot()
	if err != nil {
		return Auto, err
	}
	path := ConfigPath()
	raw := root["searchProvider"]
	if raw == nil {
		raw = root["provider"]
	}
	sel := Auto
	if raw != nil {
		if sel, err = NormalizeSearchProviderSelection(raw, "provider in "+path); err != nil {
			return sel, err
		}
	}
	allowed, err := AllowedSearchProviders()
	if err != nil {
		return sel, err
	}
	if err := AssertProviderSelectionAllowed(sel, "configured provider in "+path, allowed); err != nil {
		return sel, err
	}
	return sel, nil
}

func curatorProvider(sel ProviderSelection) string {
	switch {
	case sel.IsList():
		return "all"
	case sel.Name == "auto":
		return ""
	}
	return sel.Name
}

func searchOptionsFrom(params map[string]any, recency string) SearchOptions {
	o := SearchOptions{RecencyFilter: recency, DomainFilter: paramStrings(params["domainFilter"])}
	if f, ok := params["numResults"].(float64); ok {
		o.NumResults = &f
	}
	if b, ok := params["includeContent"].(bool); ok {
		o.IncludeContent = b
	}
	return o
}

type queryResponse struct {
	result QueryResultData
	inline []ExtractedContent
	abort  error
}

// abortError keeps the cause but always says "Aborted", as the original's AbortError does.
func abortError(cause error) error {
	if cause == nil || strings.Contains(strings.ToLower(cause.Error()), "abort") {
		return cause
	}
	return fmt.Errorf("Aborted: %w", cause)
}

func isAborted(ctx context.Context, err error) bool { return ctx.Err() != nil || isAbortError(err) }

func (r *Runtime) webSearch(ctx context.Context, params map[string]any, update func(ToolOutput)) (ToolOutput, error) {
	var proxy *string
	if p, ok := params["proxy"].(string); ok {
		proxy = &p
	}
	ctx, err := ScopeProxy(ctx, proxy)
	if err != nil {
		return ToolOutput{}, err
	}
	var rawQueries []any
	if qs, ok := params["queries"].([]any); ok {
		rawQueries = qs
	} else if q, ok := params["query"].(string); ok {
		for _, e := range expandQueryString(q) {
			rawQueries = append(rawQueries, e)
		}
	}
	queries := normalizeQueryList(rawQueries)
	var workflowInput any = params["workflow"]
	if workflowInput == nil {
		workflowInput = r.cfg.Root["workflow"]
	}
	workflow := resolveWebSearchWorkflow(workflowInput, r.hasUI())
	recency := normalizeRecency(params["recencyFilter"])

	if len(queries) == 0 {
		return textOutput("Error: No query provided. Use 'query' or 'queries' parameter.", map[string]any{"error": "No query provided"}), nil
	}
	if workflow != "none" {
		msg := fmt.Sprintf(`workflow "%s" (the search curator and generated summaries) is not available in this Go port yet; use workflow "none"`, workflow)
		return errorOutput(msg, msg, nil), nil
	}

	provider, err := ResolveRequestedProvider(params["provider"])
	if err != nil {
		return ToolOutput{}, err
	}
	opts := searchOptionsFrom(params, recency)
	var (
		progressMu sync.Mutex
		completed  int
	)
	report := func(text string, phase string, progress float64, query string) {
		if update != nil {
			update(textOutput(text, map[string]any{"phase": phase, "progress": progress, "currentQuery": query}))
		}
	}
	responses := runSearchQueries(queries, func(query string, _ int) queryResponse {
		if ctx.Err() != nil {
			return queryResponse{abort: ctx.Err()}
		}
		func() {
			// Unlocked by defer: runSearchQueries recovers a panic in this query, and a lock left
			// held would block the sibling queries forever.
			progressMu.Lock()
			defer progressMu.Unlock()
			report(fmt.Sprintf(`Searching "%s" (%d/%d complete)...`, query, completed, len(queries)), "search", float64(completed)/float64(len(queries)), query)
		}()
		resp, err := Search(ctx, query, FullSearchOptions{SearchOptions: opts, Provider: provider})
		defer func() {
			// Reported under the lock so updates reach the host in order.
			progressMu.Lock()
			defer progressMu.Unlock()
			completed++
			if ctx.Err() == nil {
				report(fmt.Sprintf("Completed %d/%d searches.", completed, len(queries)), "search", float64(completed)/float64(len(queries)), query)
			}
		}()
		if err != nil {
			if isAborted(ctx, err) {
				return queryResponse{abort: err}
			}
			msg := err.Error()
			return queryResponse{result: QueryResultData{Query: query, Results: []SearchResult{}, Error: &msg, Provider: curatorProvider(provider)}}
		}
		providers := []string{resp.Provider}
		if len(resp.ProviderResponses) > 0 {
			providers = nil
			for _, pr := range resp.ProviderResponses {
				providers = append(providers, pr.Provider)
			}
		}
		results := resp.Results
		if results == nil {
			results = []SearchResult{}
		}
		return queryResponse{result: QueryResultData{Query: query, Answer: resp.Answer, Results: results, Provider: resp.Provider, Providers: providers}, inline: resp.InlineContent}
	}, func(query string, err error) queryResponse {
		msg := err.Error()
		return queryResponse{result: QueryResultData{Query: query, Results: []SearchResult{}, Error: &msg, Provider: curatorProvider(provider)}}
	})
	var results []QueryResultData
	var urls []string
	var inline []ExtractedContent
	for _, resp := range responses {
		if resp.abort != nil {
			return ToolOutput{}, abortError(resp.abort)
		}
		results = append(results, resp.result)
		for _, res := range resp.result.Results {
			if !sliceHas(urls, res.URL) {
				urls = append(urls, res.URL)
			}
		}
		inline = append(inline, resp.inline...)
	}
	var proxyStr string
	if proxy != nil {
		proxyStr = *proxy
	}
	return r.buildSearchReturn(searchReturn{queryList: queries, results: results, urls: urls, includeContent: opts.IncludeContent, inline: inline, proxy: proxy, proxyStr: proxyStr}), nil
}

func resolveWebSearchWorkflow(input any, hasUI bool) string {
	s, _ := input.(string)
	n := strings.ToLower(jsTrim(s))
	switch {
	case n == "auto-summary":
		return n
	case !hasUI:
		return "none"
	case n == "none" || n == "summary-review":
		return n
	}
	return "none"
}

type searchReturn struct {
	queryList      []string
	results        []QueryResultData
	urls           []string
	includeContent bool
	inline         []ExtractedContent
	proxy          *string
	proxyStr       string
}

func formatSearchSummary(results []SearchResult, answer string) string {
	if len(results) == 0 {
		if answer != "" {
			return answer + "\n\n---\n\n**Sources:**\nNo sources returned."
		}
		return "No results found."
	}
	out := ""
	if answer != "" {
		out = answer + "\n\n---\n\n**Sources:**\n"
	}
	parts := make([]string, len(results))
	for i, r := range results {
		parts[i] = fmt.Sprintf("%d. %s\n   %s", i+1, r.Title, r.URL)
	}
	return out + strings.Join(parts, "\n\n")
}

func hasFullInlineCoverage(urls []string, inline []ExtractedContent) bool {
	if len(inline) == 0 {
		return false
	}
	covered := map[string]bool{}
	for _, c := range inline {
		covered[c.URL] = true
	}
	for _, u := range urls {
		if !covered[u] {
			return false
		}
	}
	return true
}

type presentation struct {
	text                                       string
	truncated                                  bool
	originalChars, returnedChars, omittedChars int
}

func boundSearchPresentation(text, guidance, truncationGuidance string, maxChars int) presentation {
	full := text + guidance
	if jsLen(full) <= maxChars {
		n := jsLen(text)
		return presentation{text: full, originalChars: n, returnedChars: n}
	}
	marker := "\n\n---\n[Output truncated.]" + truncationGuidance
	prefix := maxChars - jsLen(marker)
	bounded := jsSlice(text, 0, prefix) + marker
	n := jsLen(text)
	return presentation{text: bounded, truncated: true, originalChars: n, returnedChars: prefix, omittedChars: n - prefix}
}

func (r *Runtime) buildSearchReturn(o searchReturn) ToolOutput {
	successful, totalResults := 0, 0
	for _, res := range o.results {
		if res.Error == nil {
			successful++
		}
		totalResults += len(res.Results)
	}
	maxChars := MaxInlineContentChars(r.cfg.Root)
	var out strings.Builder
	names := make([]string, len(o.results))
	for i, res := range o.results {
		providers := res.Providers
		if providers == nil && res.Provider != "" {
			providers = []string{res.Provider}
		}
		names[i] = strings.Join(providers, ", ")
		if names[i] == "" {
			names[i] = "unknown"
		}
	}
	if len(o.results) == 1 {
		fmt.Fprintf(&out, "**Provider:** %s\n\n", names[0])
	} else {
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = fmt.Sprintf("Query %d: %s", i+1, n)
		}
		fmt.Fprintf(&out, "**Providers used:** %s\n\n", strings.Join(parts, "; "))
	}
	for _, res := range o.results {
		if len(o.queryList) > 1 {
			fmt.Fprintf(&out, "## Query: \"%s\"\n\n", res.Query)
		}
		if res.Error != nil {
			out.WriteString("Error: " + *res.Error + "\n\n")
		} else {
			out.WriteString(formatSearchSummary(res.Results, res.Answer) + "\n\n")
		}
	}

	inlineReady := hasFullInlineCoverage(o.urls, o.inline)
	var fetchID string
	if inlineReady {
		fetchID = GenerateID()
		data := &StoredSearchData{ID: fetchID, Type: "fetch", Timestamp: nowMs(), URLs: o.inline}
		r.host.AppendEntry("web-search-results", StoreFetchedContentResult(fetchID, data))
	} else if o.includeContent {
		fetchID = r.startBackgroundFetch(o.urls, o.proxy)
	}

	searchID := GenerateID()
	stored := &StoredSearchData{ID: searchID, Type: "search", Timestamp: nowMs(), Queries: o.results}
	StoreResult(searchID, stored)
	r.host.AppendEntry("web-search-results", stored)

	background := fetchID != "" && !inlineReady
	unbounded := jsTrim(out.String())
	guidance := func(forTruncation bool) string {
		v := ""
		if inlineReady && fetchID != "" {
			v += fmt.Sprintf("\n---\nFull content for %d sources is ready as responseId \"%s\". ", len(o.inline), fetchID)
			switch {
			case r.cfg.GetSearchContent:
				v += fmt.Sprintf("Use %s({ responseId: \"%s\", urlIndex: 0, offset: 0, limit: %d }) to retrieve the first bounded page.", r.cfg.Names.GetSearchContent, fetchID, maxChars)
			case forTruncation:
				v += fmt.Sprintf("Enable %s to retrieve the full stored content.", r.cfg.Names.GetSearchContent)
			}
		} else if background {
			v += fmt.Sprintf("\n---\nContent fetching in background as responseId \"%s\". Will notify when ready.", fetchID)
		}
		if r.cfg.GetSearchContent || forTruncation {
			v += fmt.Sprintf("\n---\nFull search results are stored as responseId \"%s\". ", searchID)
			if r.cfg.GetSearchContent {
				more := ""
				if len(o.results) > 1 {
					more = fmt.Sprintf("; repeat with queryIndex 1 through %d", len(o.results)-1)
				}
				v += fmt.Sprintf("Use %s({ responseId: \"%s\", queryIndex: 0, offset: 0, limit: %d }) to retrieve the first bounded page%s.", r.cfg.Names.GetSearchContent, searchID, maxChars, more)
			} else {
				v += fmt.Sprintf("Enable %s to retrieve the full stored results.", r.cfg.Names.GetSearchContent)
			}
		}
		return v
	}
	p := boundSearchPresentation(unbounded, guidance(false), guidance(true), maxChars)

	queryProviders := make([]map[string]any, len(o.results))
	for i, res := range o.results {
		providers := res.Providers
		if providers == nil && res.Provider != "" {
			providers = []string{res.Provider}
		}
		if providers == nil {
			providers = []string{}
		}
		queryProviders[i] = map[string]any{"query": res.Query, "providers": providers}
	}
	details := map[string]any{
		"queries": o.queryList, "queryCount": len(o.queryList), "successfulQueries": successful, "totalResults": totalResults,
		"includeContent": o.includeContent, "fetchId": nil, "searchId": searchID, "queryProviders": queryProviders,
		"truncated": p.truncated, "originalChars": p.originalChars, "returnedChars": p.returnedChars, "omittedChars": p.omittedChars,
	}
	if fetchID != "" {
		details["fetchId"] = fetchID
	}
	if background {
		details["fetchUrls"] = o.urls
	}
	return textOutput(p.text, details)
}

// startBackgroundFetch fetches result pages after the search returned and reports through a
// session message when they are stored. It belongs to the session: a session change or shutdown
// cancels it and its outcome is dropped.
func (r *Runtime) startBackgroundFetch(urls []string, proxy *string) string {
	if len(urls) == 0 {
		return ""
	}
	id := GenerateID()
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.pending[id] = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer cancel()
		// This goroutine outlives the tool call, so the SDK's request recover does not cover it,
		// and it handles fetched content beyond FetchAllContent's per-URL boundary (data: URI
		// sanitizing, storing, reporting). A panic is reported like a failed fetch.
		defer recoverInto("Fetch", func(err error) {
			r.mu.Lock()
			delete(r.pending, id)
			active := r.sessionActive
			r.mu.Unlock()
			if active {
				defer func() { _ = recover() }() // the host call may be what panicked
				r.host.SendMessage("web-search-error", fmt.Sprintf("Content fetch failed [%s]: %s", id, err.Error()), false)
			}
		})
		fetched, err := r.fetchInBackground(ctx, urls, proxy)
		r.mu.Lock()
		_, still := r.pending[id]
		active := r.sessionActive
		delete(r.pending, id)
		r.mu.Unlock()
		if !active || !still {
			return
		}
		if err != nil {
			if !isAbortError(err) {
				r.host.SendMessage("web-search-error", fmt.Sprintf("Content fetch failed [%s]: %s", id, err.Error()), false)
			}
			return
		}
		data := &StoredSearchData{ID: id, Type: "fetch", Timestamp: nowMs(), URLs: stripThumbnails(fetched)}
		r.host.AppendEntry("web-search-results", StoreFetchedContentResult(id, data))
		ok := 0
		for _, f := range fetched {
			if f.Error == nil {
				ok++
			}
		}
		availability := "No page content was fetched. Stored fetch diagnostics are available."
		switch {
		case ok == len(fetched):
			availability = "Full page content now available."
		case ok > 0:
			availability = "Partial page content now available."
		}
		r.host.SendMessage("web-search-content-ready", fmt.Sprintf("Content fetched for %d/%d URLs [%s]. %s", ok, len(fetched), id, availability), true)
	}()
	return id
}

func (r *Runtime) fetchInBackground(ctx context.Context, urls []string, proxy *string) ([]ExtractedContent, error) {
	ctx, err := ScopeProxy(ctx, proxy)
	if err != nil {
		return nil, err
	}
	fetched := FetchAllContent(ctx, urls, ExtractOptions{ToolNames: r.registeredNames()})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return fetched, nil
}

// SessionStarted is the session_start/session_tree handler: pending fetches of the previous
// session are cancelled and the retained results are rebuilt from the session entries.
func (r *Runtime) SessionStarted(entries []CustomEntry) {
	r.abortPending()
	r.mu.Lock()
	r.sessionActive = true
	r.mu.Unlock()
	RestoreFromEntries(entries)
}

// SessionShutdown is the session_shutdown handler.
func (r *Runtime) SessionShutdown() {
	r.mu.Lock()
	r.sessionActive = false
	r.mu.Unlock()
	r.abortPending()
	ClearResults()
}

// WaitBackground blocks until background fetches finished (tests, shutdown).
func (r *Runtime) WaitBackground() { r.wg.Wait() }

func (r *Runtime) abortPending() {
	r.mu.Lock()
	for id, cancel := range r.pending {
		cancel()
		delete(r.pending, id)
	}
	r.mu.Unlock()
}

func (r *Runtime) sourceCheck(ctx context.Context, params map[string]any, _ func(ToolOutput)) (ToolOutput, error) {
	var proxy *string
	if p, ok := params["proxy"].(string); ok {
		proxy = &p
	}
	ctx, err := ScopeProxy(ctx, proxy)
	if err != nil {
		return ToolOutput{}, err
	}
	claim := ""
	if c, ok := params["claim"].(string); ok {
		claim = jsTrim(c)
	}
	if claim == "" {
		return textOutput("Error: 'claim' is required.", map[string]any{"error": "Missing claim"}), nil
	}
	var requested []string
	for _, q := range paramStrings(params["queries"]) {
		if t := jsTrim(q); t != "" {
			requested = append(requested, t)
		}
	}
	queries := requested
	if len(queries) == 0 {
		queries = []string{claim}
	}
	if len(queries) > 8 {
		queries = queries[:8]
	}
	numResults := 5.0
	if f, ok := params["numResults"].(float64); ok && f == f {
		numResults = min(20, max(1, math.Floor(f)))
	}
	var domainFilter []string
	if _, ok := params["domainFilter"].([]any); ok {
		domainFilter = paramStrings(params["domainFilter"])
	}
	recency := normalizeRecency(params["recencyFilter"])

	byURL := map[string]bool{}
	var results []RankedSearchResult
	var summaries []string
	var errs []ArtifactError
	provider := ""
	var unique []SearchResult
	for _, query := range queries {
		if ctx.Err() != nil {
			break
		}
		sel, err := ResolveRequestedProvider(params["provider"])
		if err != nil {
			errs = append(errs, ArtifactError{Query: query, Error: err.Error()})
			continue
		}
		resp, err := Search(ctx, query, FullSearchOptions{SearchOptions: SearchOptions{NumResults: &numResults, RecencyFilter: recency, DomainFilter: domainFilter}, Provider: sel})
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			if isAbortError(err) {
				break
			}
			errs = append(errs, ArtifactError{Query: query, Error: err.Error()})
			continue
		}
		if provider == "" {
			provider = resp.Provider
		}
		if resp.Answer != "" {
			summaries = append(summaries, query+": "+resp.Answer)
		}
		for _, res := range resp.Results {
			if !byURL[res.URL] {
				byURL[res.URL] = true
				unique = append(unique, res)
			}
		}
	}
	if len(unique) > 20 {
		unique = unique[:20]
	}
	for i, res := range unique {
		results = append(results, RankedSearchResult{SearchResult: res, Rank: i + 1})
	}
	var fetched []ExtractedContent
	if fc, _ := params["fetchContent"].(bool); fc && len(results) > 0 {
		var fetchURLs []string
		for _, res := range results[:min(5, len(results))] {
			fetchURLs = append(fetchURLs, res.URL)
		}
		fetched = FetchAllContent(ctx, fetchURLs, ExtractOptions{ToolNames: r.registeredNames()})
		if ctx.Err() != nil {
			return ToolOutput{}, abortError(ctx.Err())
		}
	}
	in := BuildArtifactInput{Query: claim, Provider: provider, Results: results, Fetched: fetched, Recency: recency, DomainFilter: domainFilter}
	if len(summaries) > 0 {
		s := strings.Join(summaries, "\n\n")
		in.Summary = &s
	}
	artifact := WithClaimAssessment(BuildResearchArtifact(in), []string{claim})
	if len(errs) > 0 {
		artifact.Errors = errs
	}
	if err := StoreResearchArtifact(&artifact); err != nil {
		return ToolOutput{}, err
	}
	r.host.AppendEntry("web-search-results", map[string]any{"id": artifact.ID, "type": "research", "timestamp": artifact.Timestamp, "artifact": artifact})
	getTool := ""
	if r.cfg.GetSearchContent {
		getTool = r.cfg.Names.GetSearchContent
	}
	return textOutput(formatSourceCheckResult(artifact, getTool), map[string]any{"responseId": artifact.ID, "artifact": artifact, "sourceCount": len(artifact.Sources), "passageCount": len(artifact.Passages)}), nil
}

func formatSourceCheckResult(a ResearchArtifact, getSearchContentTool string) string {
	lines := []string{"# Source check: " + a.Query, ""}
	if len(a.Claims) > 0 {
		c := a.Claims[0]
		lines = append(lines, fmt.Sprintf("**Status:** %s (confidence %.2f)", c.Status, c.Confidence), "**Rationale:** "+c.Rationale)
		if len(c.SupportingPassages) > 0 {
			lines = append(lines, "**Supporting passages:** "+strings.Join(c.SupportingPassages, ", "))
		}
		if len(c.ContradictingPassages) > 0 {
			lines = append(lines, "**Contradicting passages:** "+strings.Join(c.ContradictingPassages, ", "))
		}
		lines = append(lines, "")
	}
	if len(a.Sources) > 0 {
		lines = append(lines, "## Sources")
		for _, s := range a.Sources {
			lines = append(lines, fmt.Sprintf("%d. [%s] %s\n   %s", s.Rank, s.Quality, s.Title, s.URL))
		}
		lines = append(lines, "")
	}
	if len(a.Errors) > 0 {
		parts := make([]string, len(a.Errors))
		for i, e := range a.Errors {
			parts[i] = e.Query + ": " + e.Error
		}
		lines = append(lines, "Search errors: "+strings.Join(parts, "; "))
	}
	if getSearchContentTool != "" {
		lines = append(lines, fmt.Sprintf("Artifact responseId: %s (retrievable via %s).", a.ID, getSearchContentTool))
	} else {
		lines = append(lines, fmt.Sprintf("Artifact responseId: %s. Content retrieval is not registered.", a.ID))
	}
	return strings.Join(lines, "\n")
}
