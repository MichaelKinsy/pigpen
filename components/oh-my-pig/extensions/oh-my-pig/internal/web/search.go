package web

// Unified web_search tool execution, provider chain dispatch, and SDK registration.
// upstream: packages/coding-agent/src/web/search/index.ts

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
)

//go:embed web-search.md
var webSearchDescription string

//go:embed web-search-system.md
var webSearchSystemPrompt string

// Setting reads an omp setting by key for the active session (e.g.
// "models.offline", "providers.webKeyless", "providers.webSearchTimeoutSeconds",
// "exa.enabled", "searxng.endpoint", "searxng.token", "searxng.basicUsername",
// "searxng.basicPassword", "searxng.categories", "searxng.engines",
// "searxng.language", "searxng.safesearch").
// Nil falls back to omp's defaults. Assigned by the integrator to settings.Any.
var Setting func(ctx sdk.Context, key string) any

// httpClient is the single HTTP client used by all search providers and Fetch.
// Tests replace it (or its Transport) to route against httptest servers.
var httpClient = &http.Client{
	Timeout: DefaultWebSearchTimeoutSeconds * time.Second,
}

// KeylessProvider is an optional interface for providers that may run without
// a user-configured credential (e.g., Hacker News Algolia API, Keenable public).
// upstream: packages/coding-agent/src/web/search/providers/base.ts:111-125
type KeylessProvider interface {
	IsKeyless() bool
}

const (
	// maxFallbackReasonChars caps each provider failure reason in fallback notes and Details.
	// upstream: packages/coding-agent/src/web/search/index.ts:124
	maxFallbackReasonChars = 240
	// rateLimitCooldown is how long an engine that answered HTTP 429 is skipped by the automatic chain.
	// upstream: packages/coding-agent/src/web/search/index.ts:150
	rateLimitCooldown = 5 * time.Minute
)

var (
	rateLimitedMu    sync.Mutex
	rateLimitedUntil = map[string]time.Time{}
	wsCollapseRe     = regexp.MustCompile(`\s+`)
)

// resetRateLimitCooldowns clears the 429 cooldown map (for tests).
func resetRateLimitCooldowns() {
	rateLimitedMu.Lock()
	clear(rateLimitedUntil)
	rateLimitedMu.Unlock()
}

// sdkContextKey carries the active sdk.Context through a Go context.Context so
// settings lookups inside ExecuteSearch and provider methods see the session's
// settings store without changing the public ExecuteSearch signature.
type sdkContextKey struct{}

func withSDKContext(ctx context.Context, sctx sdk.Context) context.Context {
	return context.WithValue(ctx, sdkContextKey{}, sctx)
}

func sdkContextFrom(ctx context.Context) sdk.Context {
	if ctx == nil {
		return sdk.Context{}
	}
	sctx, _ := ctx.Value(sdkContextKey{}).(sdk.Context)
	return sctx
}

// readSetting queries the integrator-assigned Setting hook.
func readSetting(ctx context.Context, key string) (any, bool) {
	if Setting == nil {
		return nil, false
	}
	v := Setting(sdkContextFrom(ctx), key)
	if v == nil {
		return nil, false
	}
	return v, true
}

func settingBool(ctx context.Context, key string, def bool) bool {
	v, ok := readSetting(ctx, key)
	if !ok {
		return def
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func settingFloat(ctx context.Context, key string) (float64, bool) {
	v, ok := readSetting(ctx, key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func settingString(ctx context.Context, key string) (string, bool) {
	v, ok := readSetting(ctx, key)
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

// truthyEnvFlag mirrors PiG's coding/cli/main.go:1891-1896 (and upstream main.ts):
// "1", "true", or "yes", case-insensitively.
func truthyEnvFlag(v string) bool {
	lower := strings.ToLower(v)
	return v == "1" || lower == "true" || lower == "yes"
}

// isOffline reports whether offline mode is active via PiG's process env
// (PI_OFFLINE / PIG_OFFLINE; PiG sets PI_OFFLINE=1 in coding/cli/main.go:1875)
// or omp's `models.offline` setting.
// upstream: packages/coding-agent/src/web/search/index.ts:330-341,510
func isOffline(ctx context.Context) bool {
	if truthyEnvFlag(os.Getenv("PI_OFFLINE")) || truthyEnvFlag(strings.TrimSpace(os.Getenv("PIG_OFFLINE"))) {
		return true
	}
	return settingBool(ctx, "models.offline", false)
}

// webKeylessEnabled returns whether keyless third-party web services are allowed.
// Mirrors omp's `providers.webKeyless` setting (default true; no env var in omp).
// upstream: packages/coding-agent/src/session/settings.ts:1522-1533
func webKeylessEnabled(ctx context.Context) bool {
	return settingBool(ctx, "providers.webKeyless", true)
}

// configuredSearchTimeout returns the timeout from `providers.webSearchTimeoutSeconds`,
// clamped to MaxWebSearchTimeoutSeconds, or the default 60s when unset/invalid.
// upstream: packages/coding-agent/src/web/search/index.ts:307-315
func configuredSearchTimeout(ctx context.Context) time.Duration {
	if secs, ok := settingFloat(ctx, "providers.webSearchTimeoutSeconds"); ok && secs > 0 {
		if secs > MaxWebSearchTimeoutSeconds {
			secs = MaxWebSearchTimeoutSeconds
		}
		return time.Duration(math.Ceil(secs*1000)) * time.Millisecond
	}
	return DefaultWebSearchTimeoutSeconds * time.Second
}

func isProviderKeyless(p SearchProvider) bool {
	if kp, ok := p.(KeylessProvider); ok {
		return kp.IsKeyless()
	}
	return false
}

func isRateLimited(id string) bool {
	rateLimitedMu.Lock()
	until, ok := rateLimitedUntil[id]
	if ok && !nowFunc().Before(until) {
		delete(rateLimitedUntil, id)
		ok = false
	}
	rateLimitedMu.Unlock()
	return ok
}

func markRateLimited(id string) {
	rateLimitedMu.Lock()
	rateLimitedUntil[id] = nowFunc().Add(rateLimitCooldown)
	rateLimitedMu.Unlock()
}

// targetsHackerNews reports whether every site: in a query is exactly news.ycombinator.com.
// upstream: packages/coding-agent/src/web/search/index.ts:295-296
func targetsHackerNews(q StructuredQuery) bool {
	if len(q.Sites) == 0 {
		return false
	}
	for _, s := range q.Sites {
		host := s
		if idx := strings.IndexByte(s, '/'); idx >= 0 {
			host = s[:idx]
		}
		if host != "news.ycombinator.com" {
			return false
		}
	}
	return true
}

// buildFailureDetails converts provider failures into SearchFailureDetail records.
// upstream: packages/coding-agent/src/web/search/index.ts:127-137
func buildFailureDetails(failures []providerFailure) []SearchFailureDetail {
	if len(failures) == 0 {
		return nil
	}
	out := make([]SearchFailureDetail, 0, len(failures))
	for _, f := range failures {
		reason := formatSearchProviderFailure(f.err, f.id, f.label)
		reason = strings.TrimSuffix(strings.TrimSpace(wsCollapseRe.ReplaceAllString(reason, " ")), ".")
		detail := SearchFailureDetail{
			Provider: f.id,
			Label:    f.label,
			Reason:   truncateText(reason, maxFallbackReasonChars),
		}
		var spe *SearchProviderError
		if errors.As(f.err, &spe) && spe.Status != 0 {
			detail.Status = spe.Status
		}
		out = append(out, detail)
	}
	return out
}

// fallbackNotes builds the note naming candidates that failed before servedLabel answered.
// upstream: packages/coding-agent/src/web/search/index.ts:144-147
func fallbackNotes(failed []SearchFailureDetail, servedLabel string) []string {
	if len(failed) == 0 {
		return nil
	}
	parts := make([]string, len(failed))
	for i, f := range failed {
		parts[i] = fmt.Sprintf("%s failed (%s)", f.Label, f.Reason)
	}
	return []string{fmt.Sprintf("answered by %s after %s", servedLabel, strings.Join(parts, " and "))}
}

// ExecuteSearch runs the web search pipeline over the configured provider chain.
// upstream: packages/coding-agent/src/web/search/index.ts:266-538
func ExecuteSearch(ctx context.Context, params SearchParams) (string, SearchResultDetails) {
	return executeSearchWithProviders(ctx, httpClient, defaultProviders, params)
}

func executeSearchWithProviders(
	ctx context.Context,
	client *http.Client,
	providers []SearchProvider,
	params SearchParams,
) (string, SearchResultDetails) {
	parsed := ParseSearchQuery(params.Query)
	params.ParsedQuery = &parsed
	if params.SystemPrompt == "" {
		params.SystemPrompt = strings.TrimSpace(webSearchSystemPrompt)
	}
	if params.Timeout <= 0 {
		params.Timeout = configuredSearchTimeout(ctx)
	} else if maxD := MaxWebSearchTimeoutSeconds * time.Second; params.Timeout > maxD {
		params.Timeout = maxD
	}

	// Route site:news.ycombinator.com queries to Hacker News first.
	// upstream: packages/coding-agent/src/web/search/providers/hackernews.ts:6-7
	ordered := providers
	if targetsHackerNews(parsed) {
		var hn, rest []SearchProvider
		for _, p := range providers {
			if p.ID() == "hackernews" {
				hn = append(hn, p)
			} else {
				rest = append(rest, p)
			}
		}
		if len(hn) == 0 {
			hn = []SearchProvider{hackerNewsProvider{}}
		}
		ordered = append(hn, rest...)
	}

	offline := isOffline(ctx)
	keylessAllowed := webKeylessEnabled(ctx)
	scope := ScopeConstraints(parsed)

	type scopeMiss struct {
		id    string
		label string
	}
	var scopeMisses []scopeMiss
	var failures []providerFailure
	availableCount := 0
	keylessSkipped := false
	var lastLabel string
	failedProvider := "none"

	for _, prov := range ordered {
		// All ported providers are external network search backends (modelKind === "search"
		// in omp, never the active session's model provider), so under offline every
		// candidate is skipped before touching the network.
		// upstream: packages/coding-agent/src/web/search/index.ts:330-341
		if offline {
			continue
		}
		if isRateLimited(prov.ID()) {
			minutes := int(rateLimitCooldown / time.Minute)
			failedProvider = prov.ID()
			failures = append(failures, providerFailure{
				id:    prov.ID(),
				label: prov.Label(),
				err:   fmt.Errorf("skipped: rate-limited (429) within the last %d minutes", minutes),
			})
			continue
		}
		if !keylessAllowed && isProviderKeyless(prov) {
			keylessSkipped = true
			continue
		}
		if prov.ID() == "exa" && !settingBool(ctx, "exa.enabled", true) {
			continue
		}
		if avail, ok := prov.(ctxAvailableProvider); ok {
			if !avail.IsAvailableCtx(ctx) {
				continue
			}
		} else if !prov.IsAvailable() {
			continue
		}
		availableCount++
		lastLabel = prov.Label()

		response, err := prov.Search(ctx, client, params)
		if err != nil {
			if ctx.Err() != nil {
				msg := ctx.Err().Error()
				return "Error: " + msg, SearchResultDetails{
					Response: SearchResponse{Provider: prov.ID(), Sources: []SearchSource{}},
					Error:    msg,
				}
			}
			failedProvider = prov.ID()
			failures = append(failures, providerFailure{id: prov.ID(), label: prov.Label(), err: err})
			var spe *SearchProviderError
			if errors.As(err, &spe) && spe.Status == http.StatusTooManyRequests {
				markRateLimited(prov.ID())
			}
			continue
		}

		// Central directive post-filter matching omp v19.1.1.
		// upstream: packages/coding-agent/src/web/search/index.ts:434-462
		finalResponse := response
		if finalResponse.Sources == nil {
			finalResponse.Sources = []SearchSource{}
		}
		var constraintNotes []string
		if parsed.HasConstraints && (len(response.Sources) > 0 || len(response.Citations) > 0) {
			filtered := ApplyQueryConstraints(response.Sources, parsed)
			if len(filtered.Sources) != len(response.Sources) {
				if filtered.Sources == nil {
					finalResponse.Sources = []SearchSource{}
				} else {
					finalResponse.Sources = filtered.Sources
				}
			}
			for _, label := range filtered.Dropped {
				constraintNotes = append(constraintNotes, fmt.Sprintf("no results matched `%s`; the constraint was relaxed", label))
			}

			var filteredCitations []SearchCitation
			if scope != nil && len(response.Citations) > 0 {
				for _, c := range response.Citations {
					if scope.Matches(SearchSource{Title: c.Title, URL: c.URL}) {
						filteredCitations = append(filteredCitations, c)
					}
				}
				finalResponse.Citations = filteredCitations
			} else {
				filteredCitations = response.Citations
			}
			if scope != nil {
				if len(filtered.Sources) == 0 && len(filteredCitations) == 0 {
					scopeMisses = append(scopeMisses, scopeMiss{id: prov.ID(), label: prov.Label()})
					continue
				}
				offScope := 0
				for _, src := range response.Sources {
					if !scope.Matches(src) {
						offScope++
					}
				}
				offScope += len(response.Citations) - len(filteredCitations)
				if strings.TrimSpace(response.Answer) != "" && offScope > 0 {
					constraintNotes = append(constraintNotes, fmt.Sprintf(
						"removed %s outside `%s`; the answer text is unfiltered and may draw on them",
						formatCount("source", offScope), scope.Label,
					))
				}
			}
		}

		if !hasRenderableSearchContent(finalResponse) {
			err := &SearchProviderError{
				Provider: prov.ID(),
				Message:  fmt.Sprintf("%s returned no renderable search content.", prov.Label()),
				Status:   204,
			}
			failedProvider = prov.ID()
			failures = append(failures, providerFailure{id: prov.ID(), label: prov.Label(), err: err})
			continue
		}

		failed := buildFailureDetails(failures)
		notes := append(fallbackNotes(failed, prov.Label()), constraintNotes...)
		text := formatForLLM(finalResponse, notes)
		details := SearchResultDetails{Response: finalResponse}
		if len(failed) > 0 {
			details.Failures = failed
		}
		return text, details
	}

	if scope != nil && len(scopeMisses) > 0 {
		labels := make([]string, len(scopeMisses))
		for i, m := range scopeMisses {
			labels[i] = m.label
		}
		searched := strings.Join(labels, ", ")
		notes := fallbackNotes(buildFailureDetails(failures), searched)
		lines := make([]string, 0, len(notes)+1)
		for _, note := range notes {
			lines = append(lines, "Note: "+note)
		}
		lines = append(lines, fmt.Sprintf("No results from %s matched `%s`.", searched, scope.Label))
		return strings.Join(lines, "\n"), SearchResultDetails{
			Response: SearchResponse{Provider: scopeMisses[0].id, Sources: []SearchSource{}},
		}
	}

	if availableCount == 0 && len(failures) == 0 {
		// upstream: packages/coding-agent/src/web/search/index.ts:509-521
		msg := "No web search model configured."
		if offline {
			msg = "No web search backend is available on the active model's provider under --offline."
		} else if keylessSkipped {
			msg = "No configured web search service is available, and providers.webKeyless is off. Configure a search credential or turn providers.webKeyless on."
		}
		return "Error: " + msg, SearchResultDetails{
			Response: SearchResponse{Provider: "none", Sources: []SearchSource{}},
			Error:    msg,
		}
	}

	var msg string
	if len(failures) > 1 {
		msg = "All web search providers failed: " + formatSearchProviderFailures(failures)
	} else if len(failures) == 1 {
		f := failures[0]
		msg = formatSearchProviderFailure(f.err, f.id, f.label)
	} else {
		if lastLabel == "" {
			lastLabel = "web search provider"
		}
		msg = "Unknown error from " + lastLabel
	}

	return "Error: " + msg, SearchResultDetails{
		Response: SearchResponse{Provider: failedProvider, Sources: []SearchSource{}},
		Error:    msg,
		Failures: buildFailureDetails(failures),
	}
}

// webSearchSchema mirrors omp's webSearchSchema verbatim.
// upstream: packages/coding-agent/src/web/search/index.ts:44-51
func webSearchSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"recency": map[string]any{
				"type": "string",
				"enum": []any{"day", "week", "month", "year"},
			},
			"limit":              map[string]any{"type": "number"},
			"max_tokens":         map[string]any{"type": "number"},
			"temperature":        map[string]any{"type": "number"},
			"num_search_results": map[string]any{"type": "number"},
		},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
}

func readIntParam(params map[string]any, key string) int {
	v, ok := params[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func readFloatParam(params map[string]any, key string) *float64 {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		f := n
		return &f
	case int:
		f := float64(n)
		return &f
	}
	return nil
}

func webSearchToolDef() sdk.ToolDefinition {
	readOnly := true
	openWorld := true
	return sdk.ToolDefinition{
		Name:          "web_search",
		Label:         "Web Search",
		Description:   strings.TrimSpace(webSearchDescription),
		PromptSnippet: "Search the web for up-to-date information",
		Parameters:    webSearchSchema(),
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  &readOnly,
			OpenWorldHint: &openWorld,
		},
		Execute: handleWebSearch,
	}
}

func handleWebSearch(ctx sdk.Context, params map[string]any) (any, error) {
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}
	recency, _ := params["recency"].(string)
	switch recency {
	case "", "day", "week", "month", "year":
	default:
		return nil, fmt.Errorf("invalid recency %q: must be day, week, month, or year", recency)
	}

	goCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-goCtx.Done():
			}
		}()
	}

	text, details := ExecuteSearch(withSDKContext(goCtx, ctx), SearchParams{
		Query:            query,
		Recency:          recency,
		Limit:            readIntParam(params, "limit"),
		MaxOutputTokens:  readIntParam(params, "max_tokens"),
		Temperature:      readFloatParam(params, "temperature"),
		NumSearchResults: readIntParam(params, "num_search_results"),
	})
	if goCtx.Err() != nil {
		return nil, goCtx.Err()
	}
	return sdk.ToolResult{
		Content: text,
		Details: details,
	}, nil
}

// Register installs the web_search tool on e without performing any I/O.
func Register(e *sdk.Extension) {
	def := schemaorder.Apply(webSearchToolDef())
	e.RegisterTool(intent.Tool(def))
	intent.RegisterSync(e, def)
}
