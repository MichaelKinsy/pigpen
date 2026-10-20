package websearch

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// CookieAttempt is one browser profile the cookie reader tried.
type CookieAttempt struct{ Browser, Profile, Status string }

// GoogleAccountSource is the part of gemini-web.ts that /google-account reads. The cookie reader
// belongs to the authenticated-fetch slice, which installs its implementation with
// SetGoogleAccountSource; the default knows the opt-in and refuses what needs the reader.
type GoogleAccountSource interface {
	// CookieAccessAllowed is isBrowserCookieAccessAllowed.
	CookieAccessAllowed() (bool, error)
	// Available is isGeminiWebAvailable: the cookies, or nil when Gemini Web cannot be used.
	Available(ctx context.Context) (cookies any, err error)
	// Diagnostic and Attempts are getGeminiWebAvailabilityDiagnostic and its details.
	Diagnostic() string
	Attempts() []CookieAttempt
	// ActiveEmail is getActiveGoogleEmail: "" when it cannot be determined.
	ActiveEmail(ctx context.Context, cookies any) string
}

var googleAccountSource GoogleAccountSource = refusingGoogleAccount{}

// SetGoogleAccountSource installs the cookie reader and returns a restore function.
func SetGoogleAccountSource(s GoogleAccountSource) func() {
	prev := googleAccountSource
	if s == nil {
		s = refusingGoogleAccount{}
	}
	googleAccountSource = s
	return func() { googleAccountSource = prev }
}

// refusingGoogleAccount is the source of a build without the browser cookie reader.
type refusingGoogleAccount struct{}

func (refusingGoogleAccount) CookieAccessAllowed() (bool, error) {
	if os.Getenv("PI_ALLOW_BROWSER_COOKIES") == "1" || os.Getenv("FEYNMAN_ALLOW_BROWSER_COOKIES") == "1" {
		return true, nil
	}
	root, err := ReadConfigRoot()
	if err != nil {
		return false, err
	}
	return root["allowBrowserCookies"] == true, nil
}

func (refusingGoogleAccount) Available(context.Context) (any, error) {
	return nil, unsupported("Gemini Web (browser cookies)")
}
func (refusingGoogleAccount) Diagnostic() string                      { return "" }
func (refusingGoogleAccount) Attempts() []CookieAttempt               { return nil }
func (refusingGoogleAccount) ActiveEmail(context.Context, any) string { return "" }

// GoogleAccountCommand is the /google-account command.
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
		if u := asUnsupported(err); u != nil {
			r.sendFollowUp("google-account", textBlocks(u.Error()), map[string]any{"available": false, "cookieAccessAllowed": true, "error": u.Error()}, true)
			return nil
		}
		return err
	}
	if cookies == nil {
		diagnostic := src.Diagnostic()
		attempts := formatCookieAttempts(src.Attempts())
		text := "Gemini Web is unavailable. Sign into gemini.google.com in a supported Chromium-based browser."
		if diagnostic != "" {
			text = "Gemini Web is unavailable: " + diagnostic
			if attempts != "" {
				text += " Attempted browser profiles: " + attempts + "."
			}
		}
		details := map[string]any{"available": false, "cookieAccessAllowed": true}
		if diagnostic != "" {
			details["diagnostic"] = diagnostic
		}
		if list := src.Attempts(); len(list) > 0 {
			rows := make([]map[string]any, len(list))
			for i, a := range list {
				rows[i] = map[string]any{"browser": a.Browser, "profile": a.Profile, "status": a.Status}
			}
			details["cookieDiagnostic"] = map[string]any{"attempts": rows}
		}
		r.sendFollowUp("google-account", textBlocks(text), details, true)
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

func formatCookieAttempts(attempts []CookieAttempt) string {
	parts := make([]string, len(attempts))
	for i, a := range attempts {
		parts[i] = fmt.Sprintf("%s/%s (%s)", a.Browser, a.Profile, a.Status)
	}
	return strings.Join(parts, ", ")
}
