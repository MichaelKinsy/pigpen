package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// The commands of index.ts:3265-3617: /websearch (open the curator for ad-hoc searches), /curator
// (toggle the workflow) and /google-account (the Gemini Web account). /search is command.go.

func textBlocks(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text}}
}

// sendFollowUp is sendMessage({customType, content, display: true, details}, {triggerTurn: true,
// deliverAs: "followUp"}).
func (r *Runtime) sendFollowUp(customType string, content []map[string]any, details map[string]any, triggerTurn bool) {
	if h := r.curatorHost(); h != nil {
		h.SendCustomMessage(customType, content, details, triggerTurn, "followUp")
	}
}

func (r *Runtime) followUpFromReturn(out ToolOutput) {
	var blocks []map[string]any
	for _, b := range out.Content {
		if b.Type == "text" {
			blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
		}
	}
	r.sendFollowUp("web-search-results", blocks, out.Details, true)
}

// WebsearchCommand is the /websearch command: it starts a curator, opens the page and streams the
// comma-separated queries of args into it. The command returns once the curator runs; the user's
// submission arrives later as a follow-up message. sc is the command's model context.
func (r *Runtime) WebsearchCommand(sc SideCallContext, args string) error {
	s := &r.cur
	sessionToken := randomUUID()
	callID := "cmd:" + sessionToken
	r.CloseCurator(callID)

	var queries []string
	if raw := jsTrim(args); raw != "" {
		parts := strings.Split(raw, ",")
		list := make([]any, len(parts))
		for i, p := range parts {
			list[i] = p
		}
		queries = normalizeQueryList(list)
	}

	var model map[string]any
	if sc != nil {
		model = sc.Model()
	}
	requested, err := ResolveRequestedProvider(nil)
	var bootstrap curatorBootstrap
	if err == nil {
		bootstrap, err = loadCuratorBootstrap(requested, model, curatorDefaultOptions{})
	}
	if err != nil {
		r.notify("Failed to load web search config: "+err.Error(), "error")
		return nil
	}
	initialProvider := bootstrap.defaultProvider
	root, err := ReadConfigRoot()
	if err != nil {
		return err
	}
	rawSel := Auto
	if v := firstNonNil(root["searchProvider"], root["provider"]); v != nil {
		if rawSel, err = NormalizeSearchProviderSelection(v, "provider in "+ConfigPath()); err != nil {
			return err
		}
	}
	currentSearchProvider := rawSel
	if !rawSel.IsList() && rawSel.Name != "auto" {
		currentSearchProvider = ProviderSelection{Name: initialProvider}
	}
	var choices []SummaryModelChoice
	var defaultModel *string
	if sc != nil {
		if choices, defaultModel, err = loadSummaryModelChoices(sc); err != nil {
			return err
		}
	}

	r.notify("Opening web search curator...", "info")

	collected := newResultSet()
	// The searches and the curator outlive the command's request: they run on a context of their own.
	baseCtx := WithSideCallContext(context.Background(), sc)
	searchCtx, searchAbort := context.WithCancel(baseCtx)
	var (
		stateMu sync.Mutex
		aborted bool
		handle  *CuratorServerHandle
	)
	setAborted := func() {
		stateMu.Lock()
		aborted = true
		stateMu.Unlock()
		searchAbort()
	}
	isAborted := func() bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		return aborted
	}
	isActive := func() bool {
		stateMu.Lock()
		h := handle
		stateMu.Unlock()
		return h != nil && s.activeFor(callID) == h
	}
	// handleSet reports a started handle for the "commandHandle && !isCommandActive()" checks.
	inactive := func() bool {
		stateMu.Lock()
		h := handle
		stateMu.Unlock()
		return h != nil && s.activeFor(callID) != h
	}
	var provMu sync.Mutex
	currentProvider := func() ProviderSelection {
		provMu.Lock()
		defer provMu.Unlock()
		return currentSearchProvider
	}

	callbacks := CuratorServerCallbacks{
		OnSummarize: func(ctx context.Context, selected []int, model, feedback string) (SummaryDraft, error) {
			if inactive() {
				return SummaryDraft{}, errCuratorInactive
			}
			return generateSummaryForSelectedIndices(ctx, selected, collected.snapshot(), sc, model, feedback)
		},
		OnSubmit: func(p CuratorSubmitPayload) {
			if inactive() {
				return
			}
			setAborted()
			snap := collected.snapshot()
			var results []QueryResultData
			var urls []string
			if len(p.SelectedQueryIndices) > 0 {
				results, urls = filterByQueryIndices(p.SelectedQueryIndices, snap)
			} else {
				results, urls = collectAllResultsAndURLs(snap)
			}
			base := searchReturn{queryList: queriesOf(results), results: results, urls: urls, curated: true, curatedFrom: len(snap)}
			if !p.RawResults {
				summary, meta := resolveSummaryForSubmit(p.SelectedQueryIndices, p.Summary, p.SummaryMeta, snap)
				base.workflow, base.approvedSummary, base.summaryMeta = "summary-review", summary, &meta
			}
			r.followUpFromReturn(r.buildSearchReturn(base))
			r.CloseCurator(callID)
		},
		OnCancel: func(reason string) {
			if inactive() {
				return
			}
			setAborted()
			if reason == "timeout" {
				snap := collected.snapshot()
				results, urls := collectAllResultsAndURLs(snap)
				summary, meta := resolveSummaryForSubmit(nil, "", nil, snap)
				r.followUpFromReturn(r.buildSearchReturn(searchReturn{
					queryList: queriesOf(results), results: results, urls: urls, curated: true, curatedFrom: len(snap),
					workflow: "summary-review", approvedSummary: summary, summaryMeta: &meta,
				}))
			}
			r.CloseCurator(callID)
		},
		OnProviderChange: func(provider string) {
			if inactive() {
				return
			}
			sel, err := NormalizeSearchProviderSelection(provider, "provider")
			if err != nil || sel.IsList() || sel.Name == "auto" {
				return
			}
			provMu.Lock()
			currentSearchProvider = sel
			provMu.Unlock()
			_ = SaveConfigValue("provider", sel.Name)
		},
		OnAddSearch: func(query string, provider *string) ([]CuratorSearchEntry, error) {
			ctx, err := ScopeProxy(searchCtx, nil)
			if err != nil {
				return nil, err
			}
			if inactive() {
				return nil, errCuratorInactive
			}
			requested, err := resolveCuratorSearchProvider(provider, currentProvider())
			if err != nil {
				return nil, err
			}
			resp, err := Search(ctx, query, FullSearchOptions{Provider: requested})
			if err != nil {
				return nil, err
			}
			if inactive() {
				return nil, errCuratorInactive
			}
			return toCuratorSearchEntries(resp), nil
		},
		OnAddSearchResults: func(entries []IndexedCuratorSearchEntry) {
			if inactive() {
				return
			}
			for _, e := range entries {
				collected.set(e.QueryIndex, indexedCuratorEntryToQueryResult(e))
			}
		},
		OnRewriteQuery: func(ctx context.Context, query string) (string, error) {
			if inactive() {
				return "", errCuratorInactive
			}
			if sc == nil {
				return "", errors.New("Query rewrite requires an active extension context")
			}
			return RewriteSearchQuery(ctx, query, sc)
		},
	}

	h, err := StartCuratorServer(CuratorServerOptions{
		Queries:                    queries,
		InitialResultIndexCapacity: new(curatorResultIndexCapacity(len(queries))),
		SessionToken:               sessionToken,
		Timeout:                    float64(bootstrap.timeoutSeconds),
		AvailableProviders:         bootstrap.available,
		DefaultProvider:            initialProvider,
		SearchProvider:             searchProviderOfSelection(currentProvider()),
		SummaryModels:              choices,
		DefaultSummaryModel:        defaultModel,
		Network:                    ResolveCuratorNetworkConfig(),
	}, callbacks)
	if err != nil {
		r.CloseCurator(callID)
		r.notify("Failed to open curator: "+err.Error(), "error")
		return nil
	}
	stateMu.Lock()
	handle = h
	stateMu.Unlock()
	s.setActive(callID, h)

	if err := r.openCommandCurator(h); err != nil {
		r.CloseCurator(callID)
		r.notify("Failed to open curator: "+err.Error(), "error")
		return nil
	}

	if len(queries) == 0 {
		if isActive() {
			h.SearchesDone()
		}
		return nil
	}
	go func() {
		defer func() { _ = recover() }()
		runSearchQueries(queries, func(query string, qi int) struct{} {
			if isAborted() || !isActive() {
				return struct{}{}
			}
			requested := currentProvider()
			ctx, err := ScopeProxy(searchCtx, nil)
			var resp *AttributedSearchResponse
			if err == nil {
				resp, err = Search(ctx, query, FullSearchOptions{Provider: requested})
			}
			if isAborted() || !isActive() {
				return struct{}{}
			}
			if err != nil {
				message := err.Error()
				failed := curatorProvider(requested)
				h.PushError(qi, message, failed, query, new(qi))
				collected.set(qi, QueryResultData{Query: query, Results: []SearchResult{}, Error: &message, Provider: failed})
				return struct{}{}
			}
			for entryIndex, entry := range toCuratorSearchEntries(resp) {
				index := curatorResultIndex(qi, entryIndex, len(queries))
				collected.set(index, indexedCuratorEntryToQueryResult(IndexedCuratorSearchEntry{CuratorSearchEntry: entry, QueryIndex: index, Query: query}))
				if entry.Error != "" {
					h.PushError(index, entry.Error, entry.Provider, query, new(qi))
				} else {
					h.PushResult(index, CuratorPush{CuratorSearchEntry: entry, Query: query, SlotIndex: new(qi)})
				}
			}
			return struct{}{}
		}, func(string, error) struct{} { return struct{}{} })
		if !isAborted() && isActive() {
			h.SearchesDone()
		}
	}()
	return nil
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// openCommandCurator opens the page of the /websearch curator, or tells the user the URL when the
// configuration says not to open a browser. A browser that fails to open is reported and the
// curator keeps running.
func (r *Runtime) openCommandCurator(h *CuratorServerHandle) error {
	root, err := ReadConfigRoot()
	if err != nil {
		return err
	}
	if !ShouldAutoOpenCuratorBrowser(root) {
		r.notify("Search curator is running. Open manually: "+h.URL, "info")
		return nil
	}
	if err := r.openInBrowser(h.URL); err != nil {
		r.notify("Search curator is running, but the browser did not open automatically. Open manually: "+h.URL, "info")
	}
	return nil
}

// CuratorCommand is the /curator command: on, off or a workflow name sets the configured
// workflow; no argument toggles between none and summary-review.
func (r *Runtime) CuratorCommand(args string) {
	arg := strings.ToLower(jsTrim(args))
	var workflow string
	switch arg {
	case "":
		if ResolveWebSearchWorkflow(currentRoot()["workflow"], true) == "none" {
			workflow = "summary-review"
		} else {
			workflow = "none"
		}
	case "on":
		workflow = "summary-review"
	case "off":
		workflow = "none"
	case "none", "summary-review", "auto-summary":
		workflow = arg
	default:
		r.notify(fmt.Sprintf("Unknown option: %s. Use on, off, summary-review, or auto-summary.", arg), "error")
		return
	}
	if err := SaveConfigValue("workflow", workflow); err != nil {
		r.notify("Failed to save config: "+err.Error(), "error")
		return
	}
	name := r.cfg.Names.WebSearch
	label := "Curator enabled — " + name + " will open curator and auto-generate a summary draft"
	switch workflow {
	case "none":
		label = "Curator disabled — " + name + " will return raw results"
	case "auto-summary":
		label = "Auto-summary enabled — " + name + " will generate a summary without opening the curator"
	}
	r.sendFollowUp("curator-config", textBlocks(label), map[string]any{"workflow": workflow}, false)
}

// GoogleAccountSource is the part of gemini-web.ts that /google-account reads. The command reads the
// real cookie reader of the authenticated-fetch slice (cookieGoogleAccount); SetGoogleAccountSource
// replaces it for the tests.
type GoogleAccountSource interface {
	// CookieAccessAllowed is isBrowserCookieAccessAllowed.
	CookieAccessAllowed() (bool, error)
	// Available is isGeminiWebAvailable: the cookies, or nil when Gemini Web cannot be used.
	Available(ctx context.Context) (cookies any, err error)
	// Diagnostic and DiagnosticDetails are getGeminiWebAvailabilityDiagnostic and ...Details.
	Diagnostic() string
	DiagnosticDetails() *BrowserCookieDiagnosticDetails
	// ActiveEmail is getActiveGoogleEmail: "" when it cannot be determined.
	ActiveEmail(ctx context.Context, cookies any) string
}

var googleAccountSource GoogleAccountSource = cookieGoogleAccount{}

// SetGoogleAccountSource replaces the cookie reader and returns a restore function; nil restores the real one.
func SetGoogleAccountSource(s GoogleAccountSource) func() {
	prev := googleAccountSource
	if s == nil {
		s = cookieGoogleAccount{}
	}
	googleAccountSource = s
	return func() { googleAccountSource = prev }
}

// cookieGoogleAccount reads the browser cookies of gemini-web.ts and gemini-web-config.ts.
type cookieGoogleAccount struct{}

func (cookieGoogleAccount) CookieAccessAllowed() (bool, error) { return IsBrowserCookieAccessAllowed() }

func (cookieGoogleAccount) Available(ctx context.Context) (any, error) {
	cookies, err := IsGeminiWebAvailable(ctx, "")
	if cookies == nil {
		return nil, err
	}
	return cookies, err
}

func (cookieGoogleAccount) Diagnostic() string { return geminiWebAvailabilityDiagnostic() }
func (cookieGoogleAccount) DiagnosticDetails() *BrowserCookieDiagnosticDetails {
	return GeminiWebAvailabilityDiagnosticDetails()
}

func (cookieGoogleAccount) ActiveEmail(ctx context.Context, cookies any) string {
	m, _ := cookies.(CookieMap)
	return ActiveGoogleEmail(ctx, m)
}

// GoogleAccountCommand is the /google-account command (index.ts:3570-3615).
func (r *Runtime) GoogleAccountCommand(ctx context.Context) error {
	src := googleAccountSource
	allowed, err := src.CookieAccessAllowed()
	if err != nil {
		return err
	}
	if !allowed {
		text := fmt.Sprintf("Gemini Web browser cookie access is disabled. Set allowBrowserCookies: true in %s to enable it.", ConfigPath())
		r.sendFollowUp("google-account", textBlocks(text), map[string]any{"available": false, "cookieAccessAllowed": false}, true)
		return nil
	}
	cookies, err := src.Available(ctx)
	if err != nil {
		return err
	}
	if cookies == nil {
		diagnostic := src.Diagnostic()
		details := src.DiagnosticDetails()
		var attempts []BrowserCookieAttempt
		if details != nil {
			attempts = details.Attempts
		}
		attempted := formatCookieAttempts(attempts)
		text := "Gemini Web is unavailable. Sign into gemini.google.com in a supported Chromium-based browser."
		if diagnostic != "" {
			text = "Gemini Web is unavailable: " + diagnostic
			if attempted != "" {
				text += " Attempted browser profiles: " + attempted + "."
			}
		}
		var diagnosticValue, detailsValue any
		if diagnostic != "" {
			diagnosticValue = diagnostic
		}
		if details != nil {
			detailsValue = details
		}
		r.sendFollowUp("google-account", textBlocks(text),
			map[string]any{"available": false, "cookieAccessAllowed": true, "diagnostic": diagnosticValue, "cookieDiagnostic": detailsValue}, true)
		return nil
	}
	email := src.ActiveEmail(ctx, cookies)
	text := "Gemini Web is available, but the active Google account could not be determined."
	var emailValue any
	if email != "" {
		text = "Active Google account: " + email
		emailValue = email
	}
	r.sendFollowUp("google-account", textBlocks(text), map[string]any{"available": true, "email": emailValue}, true)
	return nil
}

func formatCookieAttempts(attempts []BrowserCookieAttempt) string {
	parts := make([]string, len(attempts))
	for i, a := range attempts {
		parts[i] = fmt.Sprintf("%s/%s (%s)", a.Browser, a.Profile, a.Status)
	}
	return strings.Join(parts, ", ")
}
