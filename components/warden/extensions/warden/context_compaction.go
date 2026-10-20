package warden

import (
	"fmt"
	"math"
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Relevance compaction (opt-in): the judge picks what of the discarded span stays word for word, in place of the
// host's own summary (extension.ts 2204-2256). Returning nothing lets the host's summary run, so every failure path
// returns nothing; it never cancels a compaction.

const packageName = "pi-warden"

func asString(v any) string { s, _ := v.(string); return s }

func stringList(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// spanMessageOf reads one host message (the flat, role-discriminated wire form).
func spanMessageOf(v any) (SpanMessage, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return SpanMessage{}, false
	}
	msg := SpanMessage{Role: asString(m["role"]), Content: m["content"], ToolCallID: asString(m["toolCallId"]), ToolName: asString(m["toolName"]),
		CustomType: asString(m["customType"]), Command: asString(m["command"]), Output: asString(m["output"]), Summary: asString(m["summary"])}
	msg.IsError, _ = m["isError"].(bool)
	msg.ExcludeFromContext, _ = m["excludeFromContext"].(bool)
	if code, ok := m["exitCode"].(float64); ok {
		msg.ExitCode = &code
	}
	return msg, true
}

func (w *warden) compactionFallback(ctx sdk.Context, cfg Config, reason FallbackReason, details []string, stats *CompactionStats) {
	st := &w.context
	st.mu.Lock()
	st.compactions.Fallback(reason)
	if stats != nil {
		last := *stats
		st.compactions.Last = &last
	}
	st.mu.Unlock()
	w.record(ctx, cfg, GuardContext, "warden · context · relevance compaction · Pi's summary ran ("+string(reason)+")", details, nil)
}

// requestsLeft is the requests left of the session budget the guards share, read from the shared counter.
func (w *warden) requestsLeft(cfg Config) int {
	if cfg.MaxRequests <= 0 {
		return math.MaxInt32
	}
	return cfg.MaxRequests - w.budget.Used()
}

func (w *warden) onSessionBeforeCompact(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Compaction.Enabled {
		return nil, nil
	}
	st := &w.context
	st.mu.Lock()
	st.compactions.Runs++
	st.mu.Unlock()
	if provider := ctx.ModelProvider(); provider != "" && slices.Contains(cfg.Compaction.SkipProviders, provider) {
		w.compactionFallback(ctx, cfg, FallbackReasonSkipped, []string{"provider " + provider + " is in compaction.skipProviders"}, nil)
		return nil, nil
	}
	judge := w.judgeFor(ctx, cfg)
	if judge == nil {
		w.compactionFallback(ctx, cfg, FallbackReasonJudgmentOff, []string{"no judge (consent, key, budget, or cooldown); Pi's summary runs"}, nil)
		return nil, nil
	}
	preparation, _ := data["preparation"].(map[string]any)
	var span []SpanMessage
	add := func(list any) {
		if items, ok := list.([]any); ok {
			for _, item := range items {
				if msg, ok := spanMessageOf(item); ok {
					span = append(span, msg)
				}
			}
		}
	}
	add(preparation["messagesToSummarize"])
	if split, _ := preparation["isSplitTurn"].(bool); split {
		add(preparation["turnPrefixMessages"])
	}
	input := RelevanceInput{Messages: span, PreviousSummary: asString(preparation["previousSummary"]), Focus: asString(data["customInstructions"])}
	if ops, ok := preparation["fileOps"].(map[string]any); ok {
		input.FileOps = &FileOps{Read: stringList(ops["read"]), Written: stringList(ops["written"]), Edited: stringList(ops["edited"])}
	}
	input.Task, _ = w.latestUserPrompt(ctx)
	input.Spine = w.outputSpine(ctx)
	gctx, cancel := abortContext(ctx)
	defer cancel()
	result := RelevanceCompaction(gctx, input, RelevanceOptions{
		Judge:  judge,
		Config: RelevanceConfig{KeepThreshold: cfg.Compaction.KeepThreshold, MaxSummaryTokens: cfg.Compaction.MaxSummaryTokens, TimeoutMs: cfg.Compaction.TimeoutMs, MaxRequests: cfg.Compaction.MaxRequests},
		// The client carries the global per-request timeout; compaction.timeoutMs bounds the whole compaction on top of it.
		RequestTimeoutMs: cfg.TimeoutMs,
		// Read from the shared counter, so requests every guard started count against the reserve.
		RequestsLeft: func() int { return w.requestsLeft(cfg) },
		SavedPathFor: func(text string) (string, bool) { return st.ledger.StoredPathIn(text) },
	})
	stats := result.Stats
	counts := fmt.Sprintf("%d scored units, %d %s, %d input tokens, %d ms", stats.Candidates, stats.Requests, pluralWord(stats.Requests, "request"), stats.InputTokens, stats.ElapsedMs)
	if !result.OK {
		// A budget stop never turns judgments off: the guards keep what is left of the budget.
		switch result.Reason {
		case FallbackReasonBudget:
			if ctx.HasUI() {
				detail := result.Detail
				if detail == "" {
					detail = "budget reached"
				}
				ctx.Notify("warden: relevance compaction stopped to spare the request budget ("+detail+"); Pi's summary runs.", "warning")
			}
		case FallbackReasonJudgeError:
			detail := result.Detail
			if detail == "" {
				detail = "TypeSafe error"
			}
			w.noteError(ctx, "Relevance compaction failed ("+detail+"); Pi's summary runs.", "")
		}
		line := counts + "; " + string(result.Reason)
		if result.Detail != "" {
			line += ": " + result.Detail
		}
		w.compactionFallback(ctx, cfg, result.Reason, []string{line}, &stats)
		return nil, nil
	}
	// The host checks the signal only after the hook returns; an aborted compaction is not replaced.
	if ctx.Err() != nil {
		w.compactionFallback(ctx, cfg, FallbackReasonAborted, []string{counts}, &stats)
		return nil, nil
	}
	st.mu.Lock()
	st.compactions.Replaced++
	last := stats
	st.compactions.Last = &last
	st.mu.Unlock()
	tokensBefore, _ := preparation["tokensBefore"].(float64)
	w.record(ctx, cfg, GuardContext, fmt.Sprintf("warden · context · relevance compaction · kept %d of %d", stats.Kept, stats.Candidates), []string{
		fmt.Sprintf("%s; threshold %s; summary ~%d tokens (Pi counted %s before)", counts, relNumber(stats.Threshold), stats.SummaryTokens, relNumber(tokensBefore)),
	}, nil)
	read := result.Files.ReadFiles
	if read == nil {
		read = []string{}
	}
	modified := result.Files.ModifiedFiles
	if modified == nil {
		modified = []string{}
	}
	return map[string]any{"compaction": map[string]any{
		"summary":          result.Summary,
		"firstKeptEntryId": asString(preparation["firstKeptEntryId"]),
		"tokensBefore":     tokensBefore,
		"details":          map[string]any{"source": packageName + " relevance compaction", "kept": stats.Kept, "candidates": stats.Candidates, "readFiles": read, "modifiedFiles": modified},
	}}, nil
}
