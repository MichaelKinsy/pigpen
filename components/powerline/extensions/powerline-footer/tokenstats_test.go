package powerline_footer

import (
	"math"
	"testing"
)

type m = map[string]any

func makeUsage(input, output, cacheRead, cacheWrite float64) m {
	return m{"input": input, "output": output, "cacheRead": cacheRead, "cacheWrite": cacheWrite,
		"cost": m{"total": (input + output + cacheRead + cacheWrite) * 0.001}}
}

func assistantEvent(usage m, stopReason string) m {
	return m{"type": "message", "message": m{"role": "assistant", "content": []any{m{"type": "text", "text": "hi"}}, "usage": usage, "stopReason": stopReason}}
}

func userEvent() m {
	return m{"type": "message", "message": m{"role": "user", "content": []any{m{"type": "text", "text": "hello"}}}}
}

func subagentToolResultEvent(costs ...float64) m {
	results := []any{}
	for _, c := range costs {
		results = append(results, m{"usage": m{"cost": c}})
	}
	return m{"type": "message", "message": m{"role": "toolResult", "toolName": "subagent", "details": m{"results": results}}}
}

func subagentSlashResultEvent(costs ...float64) m {
	results := []any{}
	for _, c := range costs {
		results = append(results, m{"usage": m{"cost": c}})
	}
	return m{"type": "custom_message", "customType": "subagent-slash-result", "details": m{"result": m{"details": m{"results": results}}}}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

type fakeBranch struct {
	leaf   string
	branch []map[string]any
	reads  int
}

func (f *fakeBranch) GetLeafID() (*string, error) { return &f.leaf, nil }
func (f *fakeBranch) GetBranch(*string) ([]map[string]any, error) {
	f.reads++
	return f.branch, nil
}

func TestTokenStats(t *testing.T) {
	tw(t, "token-stats", "session branch cache reuses a leaf path and refreshes when the leaf changes", func(t *testing.T) {
		cache := &sessionBranchCache{}
		p := &fakeBranch{leaf: "leaf-1", branch: []map[string]any{{"id": "leaf-1", "value": 1}}}
		eq(t, len(cache.get(nil)), 0, "no provider")
		first := cache.get(p)
		p.branch[0]["value"] = 2
		second := cache.get(p)
		eq(t, len(first) == 1 && len(second) == 1 && &second[0] == &first[0], true, "same cached path")
		got := cache.get(p)
		eq(t, len(got) == 1 && got[0]["value"] == 2, true, "shared entries")
		eq(t, p.reads, 1, "reads")
		p.leaf = "leaf-2"
		p.branch = append(p.branch, map[string]any{"id": "leaf-2"})
		eq(t, len(cache.get(p)), 2, "refreshed")
		eq(t, p.reads, 2, "reads after leaf change")
		cache.reset()
		cache.get(p)
		eq(t, p.reads, 3, "reads after reset")
	})
	tw(t, "token-stats", "computeSessionTokenStats aggregates usage and tracks thinking level", func(t *testing.T) {
		events := []map[string]any{
			userEvent(),
			{"type": "thinking_level_change", "thinkingLevel": "high"},
			assistantEvent(makeUsage(10, 5, 0, 0), "stop"),
			assistantEvent(makeUsage(20, 7, 3, 2), "error"),   // skipped: error
			assistantEvent(makeUsage(30, 9, 4, 1), "aborted"), // skipped: aborted
			assistantEvent(makeUsage(40, 11, 6, 2), "stop"),
		}
		st := computeSessionTokenStats(events)
		eq(t, st.Input, 50.0, "input")
		eq(t, st.Output, 16.0, "output")
		eq(t, st.CacheRead, 6.0, "cacheRead")
		eq(t, st.CacheWrite, 2.0, "cacheWrite")
		eq(t, near(st.Cost, 0.074), true, "cost")
		eq(t, st.LastAssistant, events[5]["message"], "lastAssistant")
		eq(t, st.ThinkingLevelFromSession, s("high"), "thinking level")
	})
	tw(t, "token-stats", "computeSessionTokenStats sums subagent child run cost from tool results and slash results", func(t *testing.T) {
		st := computeSessionTokenStats([]map[string]any{userEvent(), assistantEvent(makeUsage(10, 5, 0, 0), "stop"), subagentToolResultEvent(0.12, 0.34), subagentSlashResultEvent(0.5)})
		eq(t, near(st.SubagentCost, 0.96), true, "subagent cost")
	})
	tw(t, "token-stats", "computeSessionTokenStats ignores assistant messages without tokens for lastAssistant", func(t *testing.T) {
		events := []map[string]any{assistantEvent(makeUsage(10, 5, 0, 0), "stop"), assistantEvent(makeUsage(0, 0, 0, 0), "stop")}
		eq(t, computeSessionTokenStats(events).LastAssistant, events[0]["message"], "lastAssistant")
	})
	tskip(t, "token-stats", "cache reuses stats object while session is unchanged", "asserts the returned stats object is the same reference (===); Go returns a value, and the port recomputes from a fresh copy of the branch for each context, so identity has no counterpart")
	tskip(t, "token-stats", "cache recomputes when events are appended", "the cache exists to skip rescans of a shared in-memory event array; the Go port reads a fresh decoded branch per context and always aggregates it (appending is covered by computeSessionTokenStats totals)")
	tskip(t, "token-stats", "cache recomputes when the last event is updated in place (streaming)", "needs the host to mutate a shared event object in place; the SDK hands the extension decoded copies, so an in-place update cannot happen")
	tskip(t, "token-stats", "cache recomputes when the last subagent result cost is updated in place", "needs in-place mutation of a shared event object, which decoded copies from the host never have; the subagent cost sum itself is covered by the computeSessionTokenStats subagent twin")
	tskip(t, "token-stats", "cache recomputes when the last event turns into an error in place", "needs in-place mutation of a shared event object; the error and aborted exclusion is covered by the computeSessionTokenStats aggregate twin")
	tskip(t, "token-stats", "cache recomputes when the last event is replaced with a same-sized list", "asserts the cache distinguishes event objects by identity; the Go port has no cache keyed on identity because every read is a fresh decoded copy")
	tskip(t, "token-stats", "cache does not rescan for in-place changes to non-trailing events", "documents the original cache's correctness boundary (historical events are not re-read); the Go port keeps no cache, so there is no boundary to document")
	tskip(t, "token-stats", "reset forces recomputation", "the stats cache is not ported (see the identity twins); the branch cache's reset is covered by the session branch cache twin")
	tskip(t, "token-stats", "index.ts wires the token stats cache into buildSegmentContext", "asserts the TypeScript source text of index.ts with regular expressions; Go has no source text to match")
}
