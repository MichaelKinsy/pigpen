package websearch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Port of the summary generation of summary-review.ts: a model writes the final summary of
// search results, with a hard deadline and a deterministic fallback. The curator workflows that
// call it (summary-review, auto-summary) are not part of this file.

// SummaryGenerationDefaultDeadlineMs is SUMMARY_GENERATION_DEADLINE_MS (summary-review.ts:21).
const SummaryGenerationDefaultDeadlineMs = 30_000

// maxSummaryGenerationDeadlineMs caps the configured deadline (index.ts:258).
const maxSummaryGenerationDeadlineMs = 600_000

// preferredSummaryModels are tried after the configured model (summary-review.ts:11-18).
var preferredSummaryModels = []modelCandidate{
	{"anthropic", "claude-haiku-4-5"},
	{"openai-codex", "gpt-5.6-luna"},
	{"openai-codex", "gpt-5.6-terra"},
	{"google", "gemini-3.6-flash"},
	{"openai", "gpt-5-mini"},
	{"deepseek", "deepseek-v4-flash"},
}

// SummaryMeta describes how a summary was produced (summary-review.ts:23-31).
type SummaryMeta struct {
	Model          *string `json:"model"`
	DurationMs     int64   `json:"durationMs"`
	TokenEstimate  int     `json:"tokenEstimate"`
	FallbackUsed   bool    `json:"fallbackUsed"`
	FallbackReason string  `json:"fallbackReason,omitempty"`
	Phase          string  `json:"phase,omitempty"`
	Edited         bool    `json:"edited"`
}

// SummaryDraft is a summary and its metadata.
type SummaryDraft struct {
	Summary string
	Meta    SummaryMeta
}

// SummaryOptions are the optional arguments of GenerateSummaryDraft.
type SummaryOptions struct {
	// ModelOverride is `provider/model-id` with an optional `:thinking` suffix, tried before the preferred models.
	ModelOverride string
	// Feedback is the user's feedback to fold into the summary.
	Feedback string
	// Complete replaces the registry's completion (the direct path: it receives the resolved api key and headers).
	Complete CompleteFunc
	// DeadlineMs is the hard deadline; zero or less means the default.
	DeadlineMs int
}

// summaryClock is Date.now for the deadline; tests replace it to expire a deadline instantly.
var summaryClock = time.Now

// GetSummaryGenerationDeadlineMs reads summaryGenerationDeadlineMs of web-search.json (index.ts:449-455):
// a positive integer, capped at 600000; anything else is the 30000 default.
func GetSummaryGenerationDeadlineMs() int {
	return summaryGenerationDeadline(currentRoot()["summaryGenerationDeadlineMs"])
}

func summaryGenerationDeadline(value any) int {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n <= 0 {
		return SummaryGenerationDefaultDeadlineMs
	}
	return int(math.Min(n, maxSummaryGenerationDeadlineMs))
}

func estimateTokens(text string) int {
	trimmed := jsTrim(text)
	if trimmed == "" {
		return 0
	}
	return max(1, (jsLen(trimmed)+3)/4)
}

func summarizeQueryResult(result QueryResultData) string {
	var b strings.Builder
	b.Grow(queryResultSize(result))
	writeQueryResult(&b, result)
	return b.String()
}

// queryResultSize is a capacity for writeQueryResult: the text it writes, rounded up.
func queryResultSize(result QueryResultData) int {
	n := 96 + len(result.Query) + len(result.Provider) + len(result.Answer)
	if result.Error != nil {
		n += len(*result.Error)
	}
	for _, source := range result.Results {
		n += 16 + len(source.Title) + len(source.URL)
	}
	return n
}

// writeQueryResult writes the lines of summarizeQueryResult in summary-review.ts:40-62, joined by
// "\n", straight into b.
func writeQueryResult(b *strings.Builder, result QueryResultData) {
	if result.Error != nil && *result.Error != "" {
		b.WriteString("Query: ")
		b.WriteString(result.Query)
		b.WriteString("\nStatus: Error\nError: ")
		b.WriteString(*result.Error)
		return
	}
	provider := result.Provider
	if provider == "" {
		provider = "unknown"
	}
	answer := result.Answer
	if answer == "" {
		answer = "(no answer text returned)"
	}
	b.WriteString("Query: ")
	b.WriteString(result.Query)
	b.WriteString("\nProvider: ")
	b.WriteString(provider)
	b.WriteString("\nAnswer: ")
	b.WriteString(answer)
	if len(result.Results) == 0 {
		b.WriteString("\nSources: none")
		return
	}
	b.WriteString("\nSources:")
	for i, source := range result.Results {
		b.WriteByte('\n')
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(source.Title)
		b.WriteString(" \u2014 ")
		b.WriteString(source.URL)
	}
}

// BuildSummaryPrompt is the prompt of the summary model (summary-review.ts:66-100): the sections
// joined by "\n", written into one buffer.
func BuildSummaryPrompt(results []QueryResultData, feedback string) string {
	size := 640 + len(feedback)
	for _, result := range results {
		size += 24 + queryResultSize(result)
	}
	var b strings.Builder
	b.Grow(size)
	first := true
	section := func(parts ...string) {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		for _, part := range parts {
			b.WriteString(part)
		}
	}
	section("You are writing the final web search summary for a coding assistant.")
	section("Write a concise, factual summary using only the provided search results.")
	section("Requirements:")
	section("- Keep it readable and skimmable.")
	section("- Include key findings and caveats.")
	section("- Do not invent sources or claims.")
	section("- If evidence is weak or conflicting, say so explicitly.")
	section("- End with a short \"Sources\" section listing the most relevant URLs.")
	if feedback != "" {
		section("- Incorporate the user feedback provided below into the summary.")
	}
	section("")
	section("<search_results>")
	for i, result := range results {
		section("\n[Result ", strconv.Itoa(i+1), "]")
		section()
		writeQueryResult(&b, result)
	}
	section("\n</search_results>")
	if feedback != "" {
		section("")
		section("<user_feedback>")
		section(feedback)
		section("</user_feedback>")
	}
	return b.String()
}

var sourceMarkerPattern = regexp.MustCompile(`(?i)\bSources?\s*:`)

func buildDeterministicAnswerPreview(answer string) string {
	text := jsTrim(collapseSpaces(answer))
	if text == "" {
		return ""
	}
	if loc := sourceMarkerPattern.FindStringIndex(text); loc != nil {
		text = jsTrim(text[:loc[0]])
	}
	if text == "" {
		return ""
	}
	if jsLen(text) > 240 {
		return jsSlice(text, 0, 237) + "..."
	}
	return text
}

func buildDeterministicSummaryLines(results []QueryResultData) []string {
	if len(results) == 0 {
		return []string{"No completed search results were available when the curator session finished.", "", "Sources", "- None"}
	}
	lines := []string{"Summary based on the currently selected search results.", ""}
	var sourceURLs []string
	seen := map[string]bool{}
	successful, failed := 0, 0
	for _, result := range results {
		if result.Error != nil && *result.Error != "" {
			failed++
			lines = append(lines, fmt.Sprintf("- %s: failed (%s)", result.Query, *result.Error))
			continue
		}
		successful++
		if preview := buildDeterministicAnswerPreview(result.Answer); preview != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", result.Query, preview))
		} else {
			plural := "s"
			if len(result.Results) == 1 {
				plural = ""
			}
			lines = append(lines, fmt.Sprintf("- %s: returned %d source%s without answer text.", result.Query, len(result.Results), plural))
		}
		for _, source := range result.Results {
			if !seen[source.URL] {
				seen[source.URL] = true
				sourceURLs = append(sourceURLs, source.URL)
			}
		}
	}
	lines = append(lines, "", fmt.Sprintf("Completed queries: %d", len(results)), fmt.Sprintf("Successful: %d", successful), fmt.Sprintf("Failed: %d", failed), "", "Sources")
	if len(sourceURLs) == 0 {
		return append(lines, "- None")
	}
	for _, url := range sourceURLs[:min(len(sourceURLs), 12)] {
		lines = append(lines, "- "+url)
	}
	if len(sourceURLs) > 12 {
		lines = append(lines, fmt.Sprintf("- ... and %d more", len(sourceURLs)-12))
	}
	return lines
}

// BuildDeterministicSummary summarizes the results without a model (summary-review.ts:175-193).
func BuildDeterministicSummary(results []QueryResultData) SummaryDraft {
	summary := jsTrim(strings.Join(buildDeterministicSummaryLines(results), "\n"))
	if summary == "" {
		summary = "No completed search results were available when the curator session finished.\n\nSources\n- None"
	}
	return SummaryDraft{Summary: summary, Meta: SummaryMeta{
		TokenEstimate: estimateTokens(summary), FallbackUsed: true,
		FallbackReason: "deterministic-submit-fallback", Phase: "deterministic-fallback",
	}}
}

func buildFallbackSummary(results []QueryResultData, reason string, durationMs int64) SummaryDraft {
	draft := BuildDeterministicSummary(results)
	draft.Meta.DurationMs = durationMs
	draft.Meta.FallbackReason = reason
	return draft
}

type summarySelector struct {
	provider, id, thinkingLevel string
}

func parseSummaryModelSelector(value string) (summarySelector, error) {
	rest, level := SplitThinkingSuffix(value)
	slash := strings.Index(rest, "/")
	if slash <= 0 || slash >= len(rest)-1 {
		return summarySelector{}, fmt.Errorf("Invalid summary model: %s. Use provider/model-id.", value)
	}
	return summarySelector{provider: rest[:slash], id: rest[slash+1:], thinkingLevel: level}, nil
}

var extendedThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// supportedThinkingLevels is pi-ai's getSupportedThinkingLevels (models.ts:1230-1239): a model
// without reasoning supports "off" only; a null map entry removes a level, and "xhigh" and "max"
// need an entry.
func supportedThinkingLevels(model map[string]any) []string {
	if reasoning, _ := model["reasoning"].(bool); !reasoning {
		return []string{"off"}
	}
	levelMap, _ := model["thinkingLevelMap"].(map[string]any)
	var levels []string
	for _, level := range extendedThinkingLevels {
		mapped, present := levelMap[level]
		if present && mapped == nil {
			continue
		}
		if (level == "xhigh" || level == "max") && !present {
			continue
		}
		levels = append(levels, level)
	}
	return levels
}

func indexOfString(list []string, value string) int {
	for i, item := range list {
		if item == value {
			return i
		}
	}
	return -1
}

// clampThinkingLevel is pi-ai's clampThinkingLevel (models.ts:1241-1260): the requested level, else
// the nearest supported one, searching upward first.
func clampThinkingLevel(model map[string]any, level string) string {
	available := supportedThinkingLevels(model)
	if indexOfString(available, level) >= 0 {
		return level
	}
	first := "off"
	if len(available) > 0 {
		first = available[0]
	}
	requested := indexOfString(extendedThinkingLevels, level)
	if requested < 0 {
		return first
	}
	for i := requested; i < len(extendedThinkingLevels); i++ {
		if indexOfString(available, extendedThinkingLevels[i]) >= 0 {
			return extendedThinkingLevels[i]
		}
	}
	for i := requested - 1; i >= 0; i-- {
		if indexOfString(available, extendedThinkingLevels[i]) >= 0 {
			return extendedThinkingLevels[i]
		}
	}
	return first
}

type summaryCandidate struct {
	model         map[string]any
	apiKey        string
	headers       map[string]any
	thinkingLevel string
}

// resolveSummaryModelCandidates lists the models the summary may use, in order (summary-review.ts:214-248).
func resolveSummaryModelCandidates(sc SideCallContext, modelOverride string) ([]summaryCandidate, []string, error) {
	var specs []summarySelector
	if override := jsTrim(modelOverride); override != "" {
		selector, err := parseSummaryModelSelector(override)
		if err != nil {
			return nil, nil, err
		}
		specs = append(specs, selector)
	}
	for _, preferred := range preferredSummaryModels {
		specs = append(specs, summarySelector{provider: preferred.provider, id: preferred.id})
	}
	registry := sc.Registry()
	var candidates []summaryCandidate
	var problems []string
	seen := map[string]bool{}
	for _, spec := range specs {
		value := spec.provider + "/" + spec.id
		if seen[value] {
			continue
		}
		seen[value] = true
		model, err := FindModelWithProviderRouting(registry, spec.provider, spec.id)
		if err != nil {
			return nil, nil, err
		}
		if model == nil {
			problems = append(problems, "Summary model not found: "+value)
			continue
		}
		scoped, err := sc.ScopedModels()
		if err != nil {
			return nil, nil, err
		}
		if !IsModelInScope(model, scoped) {
			problems = append(problems, "Summary model is not enabled: "+value)
			continue
		}
		auth, err := registry.GetApiKeyAndHeaders(model)
		if err != nil {
			return nil, nil, err
		}
		if ok, _ := auth["ok"].(bool); !ok {
			problems = append(problems, "No API key available for summary model "+value)
			continue
		}
		candidate := summaryCandidate{model: model, thinkingLevel: spec.thinkingLevel}
		candidate.apiKey, _ = auth["apiKey"].(string)
		candidate.headers, _ = auth["headers"].(map[string]any)
		candidates = append(candidates, candidate)
	}
	return candidates, problems, nil
}

// summaryRun is one generation's deadline and cancellation state.
type summaryRun struct {
	caller   context.Context
	started  time.Time
	deadline time.Duration
	// completion is the context of the model call: cancelled by the caller or by the deadline (`options.signal`).
	completion context.Context
	expire     context.CancelFunc
	expired    atomic.Bool
	expiredCh  chan struct{}
}

var errSummaryDeadline = errors.New("summary generation deadline")

// check is checkSummaryDeadline: it reports caller cancellation first, then an expired deadline
// even when the timer has not run yet.
func (r *summaryRun) check() error {
	if r.caller.Err() != nil {
		return errAborted
	}
	if r.expired.Load() || summaryClock().Sub(r.started) >= r.deadline {
		r.fire()
		return errSummaryDeadline
	}
	return nil
}

func (r *summaryRun) fire() {
	if r.expired.CompareAndSwap(false, true) {
		close(r.expiredCh)
		r.expire()
	}
}

// raceSummaryOperation waits for operation, the deadline or the caller's cancellation, whichever
// comes first. A provider may ignore its context and never return, so the operation runs apart
// and its late outcome is dropped.
func raceSummaryOperation[T any](r *summaryRun, operation func() (T, error)) (T, error) {
	var zero T
	if err := r.check(); err != nil {
		return zero, err
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := operation()
		done <- outcome{value, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			return zero, o.err
		}
		if err := r.check(); err != nil {
			return zero, err
		}
		return o.value, nil
	case <-r.expiredCh:
		return zero, errSummaryDeadline
	case <-r.caller.Done():
		return zero, errAborted
	}
}

func contentPartText(part any) string {
	object, _ := part.(map[string]any)
	if text, ok := object["text"].(string); ok {
		return text
	}
	if refusal, ok := object["refusal"].(string); ok {
		return refusal
	}
	return ""
}

func contentPartType(part any) string {
	object, _ := part.(map[string]any)
	if kind, ok := object["type"].(string); ok {
		return kind
	}
	return "unknown"
}

// GenerateSummaryDraft writes the summary of results with the first usable model (summary-review.ts:287-445).
// It never runs past the deadline: a model that is slow, unavailable or silent gives the deterministic
// summary with the reason. The caller's cancellation is an "Aborted" error.
func GenerateSummaryDraft(ctx context.Context, results []QueryResultData, sc SideCallContext, opts SummaryOptions) (SummaryDraft, error) {
	if sc == nil || sc.Registry() == nil {
		return SummaryDraft{}, errors.New("Summary generation context unavailable")
	}
	if ctx.Err() != nil {
		return SummaryDraft{}, errAborted
	}
	deadline := opts.DeadlineMs
	if deadline <= 0 {
		deadline = SummaryGenerationDefaultDeadlineMs
	}
	completion, cancel := context.WithCancel(ctx)
	defer cancel()
	run := &summaryRun{caller: ctx, started: summaryClock(), deadline: time.Duration(deadline) * time.Millisecond, completion: completion, expire: cancel, expiredCh: make(chan struct{})}
	timer := time.AfterFunc(run.deadline, run.fire)
	defer timer.Stop()

	draft, err := run.generate(results, sc, opts)
	if ctx.Err() != nil {
		return SummaryDraft{}, errAborted
	}
	if errors.Is(err, errSummaryDeadline) {
		return buildFallbackSummary(results, "summary-generation-timeout", summaryClock().Sub(run.started).Milliseconds()), nil
	}
	return draft, err
}

func (r *summaryRun) generate(results []QueryResultData, sc SideCallContext, opts SummaryOptions) (SummaryDraft, error) {
	prompt := BuildSummaryPrompt(results, opts.Feedback)
	type resolved struct {
		candidates []summaryCandidate
		problems   []string
	}
	found, err := raceSummaryOperation(r, func() (resolved, error) {
		candidates, problems, err := resolveSummaryModelCandidates(sc, opts.ModelOverride)
		return resolved{candidates, problems}, err
	})
	if err != nil {
		if cerr := r.check(); cerr != nil {
			return SummaryDraft{}, cerr
		}
		return buildFallbackSummary(results, "summary-model-settings-error: "+err.Error(), summaryClock().Sub(r.started).Milliseconds()), nil
	}

	lastError := ""
	if len(found.problems) > 0 {
		lastError = found.problems[len(found.problems)-1]
	}
	complete := opts.Complete
	custom := complete != nil
	if !custom {
		complete = sc.Registry().Complete
	}
	for _, candidate := range found.candidates {
		startedAt := summaryClock()
		draft, err := r.tryCandidate(prompt, candidate, sc, complete, custom, startedAt)
		if err == nil {
			return draft, nil
		}
		if cerr := r.check(); cerr != nil {
			return SummaryDraft{}, cerr
		}
		if isAbortError(err) {
			return SummaryDraft{}, err
		}
		lastError = err.Error()
	}
	reason := "summary-model-unavailable"
	if lastError != "" {
		reason += ": " + lastError
	}
	return buildFallbackSummary(results, reason, summaryClock().Sub(r.started).Milliseconds()), nil
}

func (r *summaryRun) tryCandidate(prompt string, c summaryCandidate, sc SideCallContext, complete CompleteFunc, custom bool, startedAt time.Time) (SummaryDraft, error) {
	sessionHeaders := OpenCodeSessionHeaders(c.model, sc.SessionID())
	request := map[string]any{"messages": []any{userMessage(prompt)}}
	requested := ""
	if c.thinkingLevel != "" {
		if err := r.check(); err != nil {
			return SummaryDraft{}, err
		}
		requested = clampThinkingLevel(c.model, c.thinkingLevel)
	}
	options := CompleteOptions{Reasoning: requested}
	if requested != "" && requested != "off" {
		options.ReasoningEffort = requested
	}
	switch {
	case custom:
		options.APIKey = c.apiKey
		options.Headers = c.headers
		if sessionHeaders != nil {
			options.Headers = make(map[string]any, len(c.headers)+len(sessionHeaders))
			for name, value := range c.headers {
				options.Headers[name] = value
			}
			for name, value := range sessionHeaders {
				options.Headers[name] = value
			}
		}
	case sessionHeaders != nil:
		options.TransformHeaders = mergeHeaders(sessionHeaders)
	}
	if err := r.check(); err != nil {
		return SummaryDraft{}, err
	}
	response, err := raceSummaryOperation(r, func() (map[string]any, error) {
		return complete(r.completion, c.model, request, options)
	})
	if err != nil {
		return SummaryDraft{}, err
	}
	if response["stopReason"] == "aborted" {
		return SummaryDraft{}, errAborted
	}
	parts, _ := response["content"].([]any)
	var texts []string
	for _, part := range parts {
		if text := contentPartText(part); jsTrim(text) != "" {
			texts = append(texts, text)
		}
	}
	summary := jsTrim(strings.Join(texts, "\n"))
	if summary == "" {
		kinds := make([]string, len(parts))
		for i, part := range parts {
			kinds[i] = contentPartType(part)
		}
		label := "none"
		if len(kinds) > 0 {
			label = strings.Join(kinds, ", ")
		}
		return SummaryDraft{}, fmt.Errorf("Summary model returned empty response (content parts: %s)", label)
	}
	return SummaryDraft{Summary: summary, Meta: SummaryMeta{
		Model: new(modelLabel(c.model)), DurationMs: max(0, summaryClock().Sub(startedAt).Milliseconds()),
		TokenEstimate: estimateTokens(summary), Phase: "summary-model",
	}}, nil
}
