package tintinweb_subagents

import (
	"strings"
	"testing"
)

// Twins of usage.test.ts (the accumulator and the pool), usage-reporting.test.ts, cost-display.test.ts (the texts)
// and the e2e usage test. The display readers of usage.test.ts are twinned with the UI lane's ui_usage.go.

func TestCostAccumulationTwins(t *testing.T) {
	tw(t, "usage", "sums cost across messages but keeps it out of the token total", func(t *testing.T) {
		var u lifetimeUsage
		addUsage(&u, lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, CacheRead: 900, Cost: 0.002})
		addUsage(&u, lifetimeUsage{Input: 200, Output: 80, CacheWrite: 20, CacheRead: 1800, Cost: 0.004})
		if d := getLifetimeCost(u) - 0.006; d > 1e-10 || d < -1e-10 {
			t.Fatalf("cost %v", getLifetimeCost(u))
		}
		// The display total takes neither the money nor the re-read prefix, though both are accumulated.
		eq(t, getLifetimeTotal(u), 460)
		eq(t, u.CacheRead, 2700)
	})
	tw(t, "usage", "leaves cost absent when nothing priced anything", func(t *testing.T) {
		// An unpriced model reports 0 per message; in Go, absent and zero are one value.
		var u lifetimeUsage
		addUsage(&u, lifetimeUsage{Input: 10, Output: 5, Cost: 0})
		eq(t, u.Cost, 0.0)
		eq(t, getLifetimeCost(u), 0.0)
	})
	tw(t, "usage", "reads a missing cost as 0", func(t *testing.T) {
		eq(t, getLifetimeCost(lifetimeUsage{}), 0.0)
		eq(t, getLifetimeCost(lifetimeUsage{Input: 1, Output: 1}), 0.0)
	})
}

func TestPendingUsagePoolTwins(t *testing.T) {
	tw(t, "usage", "drains what it accumulated as a complete pi Usage", func(t *testing.T) {
		var pool pendingUsagePool
		pool.add(lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, CacheRead: 900, Cost: 0.01})
		pool.add(lifetimeUsage{Input: 200, Output: 80, CacheWrite: 20, CacheRead: 1800, Cost: 0.02})
		got := pool.drain()
		eq(t, got.Input, 300)
		eq(t, got.Output, 130)
		eq(t, got.CacheRead, 2700) // summed, unlike the display total (#38)
		eq(t, got.CacheWrite, 30)
		eq(t, got.TotalTokens, 3160)
		eq(t, got.Cost.Input+got.Cost.Output+got.Cost.CacheRead+got.Cost.CacheWrite, 0.0)
		if d := got.Cost.Total - 0.03; d > 1e-10 || d < -1e-10 {
			t.Fatalf("total %v", got.Cost.Total)
		}
	})
	tw(t, "usage", "empties on drain, so no message is reported twice", func(t *testing.T) {
		var pool pendingUsagePool
		pool.add(lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.01})
		eq(t, pool.drain().TotalTokens, 160)
		eq(t, pool.drain() == nil, true)
	})
	tw(t, "usage", "handles an accumulator that never saw a cacheRead or a cost", func(t *testing.T) {
		var pool pendingUsagePool
		pool.add(lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10})
		eq(t, *pool.drain(), reportedUsage{Input: 100, Output: 50, CacheWrite: 10, TotalTokens: 160})
	})
	tw(t, "usage", "returns undefined when nothing has been added", func(t *testing.T) {
		var pool pendingUsagePool
		eq(t, pool.drain() == nil, true)
	})
	tw(t, "usage", "still reports tokens spent by a model with no pricing", func(t *testing.T) {
		var pool pendingUsagePool
		pool.add(lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0})
		got := pool.drain()
		eq(t, got.TotalTokens, 160)
		eq(t, got.Cost.Total, 0.0)
	})
	tw(t, "usage", "reports nothing for a message that spent nothing", func(t *testing.T) {
		var pool pendingUsagePool
		pool.add(lifetimeUsage{})
		eq(t, pool.drain() == nil, true)
	})
}

// fgRun runs one foreground Agent call whose child spends u (when non-nil) and answers "done".
func fgRun(t *testing.T, r *rig, u *lifetimeUsage, callID string) (obj, string) {
	t.Helper()
	n := r.fleet.count()
	type out struct {
		res  obj
		fail string
	}
	got := make(chan out, 1)
	go func() {
		res, fail := r.toolRaw("Agent", obj{"prompt": "go", "description": "spend", "subagent_type": "general-purpose", "run_in_background": false}, callID)
		got <- out{res, fail}
	}()
	c := r.fleet.wait(t, n+1)[n]
	if u != nil {
		c.spend(*u)
	}
	c.finishWith(&childResult{Text: "done", ToolUses: 1})
	o := <-got
	if o.fail != "" {
		t.Fatalf("Agent failed: %s", o.fail)
	}
	return o.res, o.res["content"].(string)
}

func usageRig(t *testing.T, settings string) *rig {
	return startObsRig(t, obsWorkspace(t, settings, ""))
}

func wantUsage(input, output, cacheRead, cacheWrite, total int, cost float64) obj {
	return obj{"input": float64(input), "output": float64(output), "cacheRead": float64(cacheRead), "cacheWrite": float64(cacheWrite),
		"totalTokens": float64(total), "cost": obj{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0, "total": cost}}
}

func TestUsageReportingTwins(t *testing.T) {
	tw(t, "usage-reporting", "attaches a complete pi Usage to the tool result", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		res, _ := fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, CacheRead: 900, Cost: 0.0123}, "call-1")
		eq(t, res["usage"], any(wantUsage(100, 50, 900, 10, 1060, 0.0123)))
	})
	tw(t, "usage-reporting", "reports each message's spend exactly once", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.0123}, "call-1")
		second, _ := fgRun(t, r, nil, "call-2")
		_, has := second["usage"]
		eq(t, has, false)
	})
	tw(t, "usage-reporting", "carries what a later run spends on the later result", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.01}, "call-1")
		second, _ := fgRun(t, r, &lifetimeUsage{Input: 7, Output: 3, Cost: 0.002}, "call-2")
		u := second["usage"].(map[string]any)
		eq(t, u["totalTokens"], 10.0)
		eq(t, u["cost"].(map[string]any)["total"], 0.002)
	})
	tw(t, "usage-reporting", "attaches nothing when the setting is off", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": false}`)
		res, text := fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.0123}, "call-1")
		_, has := res["usage"]
		eq(t, has, false)
		eq(t, strings.Contains(text, "Agent completed"), true) // the setting must not change what the orchestrator reads
	})
	tw(t, "usage-reporting", "defaults to off", func(t *testing.T) {
		r := usageRig(t, "")
		res, _ := fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.0123}, "call-1")
		_, has := res["usage"]
		eq(t, has, false)
	})
	tw(t, "usage-reporting", "attaches nothing to a call with no tool-call id, and loses none of it", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		cloned, _ := fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.0123}, "")
		_, has := cloned["usage"]
		eq(t, has, false)
		real, _ := fgRun(t, r, nil, "call-2") // it was not dropped: the next real result carries it
		u := real["usage"].(map[string]any)
		eq(t, u["cost"].(map[string]any)["total"], 0.0123)
		eq(t, u["totalTokens"], 160.0)
	})
	tw(t, "usage-reporting", "attaches nothing when a run produced no usage at all", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		res, _ := fgRun(t, r, nil, "call-1")
		_, has := res["usage"]
		eq(t, has, false)
	})
	tw(t, "usage-reporting", "reports an unpriced model's tokens with a zero cost rather than dropping them", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		res, _ := fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0}, "call-1")
		u := res["usage"].(map[string]any)
		eq(t, u["totalTokens"], 160.0)
		eq(t, u["cost"].(map[string]any)["total"], 0.0)
	})
	tskip(t, "usage-reporting", "reports what a background resume spends, on the next call", "resume of a finished agent is refused by this port (resume parameter); its spend path is the sessions lane's")
	tskip(t, "usage-reporting", "counts a nested child's spend once, on the top-level agent's report", "nested delegation is refused by this port (allowed_subagents); the double-booking it guards against has no Go form yet")
	tskip(t, "usage-reporting", "reports what a resume spends, not only the first run", "resume of a finished agent is refused by this port (resume parameter); its spend path is the sessions lane's")

	completedPayload := func(t *testing.T, settings string, u *lifetimeUsage) obj {
		r := usageRig(t, settings)
		completed := r.collect("subagents:completed")
		fgRun(t, r, u, "call-1")
		return completed.wait(t, 1)[0]
	}
	tw(t, "usage-reporting", "carries the run's spend as a pi Usage", func(t *testing.T) {
		p := completedPayload(t, "", &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, CacheRead: 900, Cost: 0.0123})
		eq(t, p["usage"], any(wantUsage(100, 50, 900, 10, 1060, 0.0123)))
	})
	tw(t, "usage-reporting", "carries it regardless of either setting", func(t *testing.T) {
		p := completedPayload(t, `{"reportUsage": false, "showCost": false}`, &lifetimeUsage{Input: 100, Output: 50, Cost: 0.01})
		eq(t, p["usage"].(map[string]any)["cost"].(map[string]any)["total"], 0.01)
	})
	tw(t, "usage-reporting", "keeps `tokens` as the display total it has always been", func(t *testing.T) {
		p := completedPayload(t, "", &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, CacheRead: 900, Cost: 0.0123})
		eq(t, p["tokens"], any(obj{"input": 100.0, "output": 50.0, "total": 160.0}))
	})
	tw(t, "usage-reporting", "omits usage entirely when nothing was spent", func(t *testing.T) {
		p := completedPayload(t, "", nil)
		_, hasUsage := p["usage"]
		_, hasTokens := p["tokens"]
		eq(t, hasUsage || hasTokens, false)
	})
	tw(t, "usage-reporting", "reports an unpriced model's tokens with a zero cost", func(t *testing.T) {
		p := completedPayload(t, "", &lifetimeUsage{Input: 100, Output: 50, Cost: 0})
		u := p["usage"].(map[string]any)
		eq(t, u["totalTokens"], 150.0)
		eq(t, u["cost"].(map[string]any)["total"], 0.0)
	})
	tw(t, "usage-reporting", "reports spend through get_subagent_result too", func(t *testing.T) {
		r := usageRig(t, `{"reportUsage": true}`)
		fgRun(t, r, &lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, Cost: 0.0123}, "") // accumulates, nothing attached
		res, fail := r.toolRaw("get_subagent_result", obj{"agent_id": "nope"}, "call-2")
		eq(t, fail, "")
		eq(t, res["usage"].(map[string]any)["cost"].(map[string]any)["total"], 0.0123)
	})
}

func TestCostDisplayTwins(t *testing.T) {
	const cost = 0.0123
	fg := func(t *testing.T, settings string, c float64) string {
		r := usageRig(t, settings)
		_, text := fgRun(t, r, &lifetimeUsage{Input: 1000, Output: 200, Cost: c}, "call-1")
		return text
	}
	tw(t, "cost-display", "names the cost in the stats it already reports", func(t *testing.T) {
		text := fg(t, `{"showCost": true}`, cost)
		eq(t, strings.Contains(text, "~$0.0123"), true)
		eq(t, strings.Contains(text, "1.2k token, ~$0.0123"), true) // comma-joined with the rest: the separator belongs to the surface
	})
	tw(t, "cost-display", "says nothing when the setting is off", func(t *testing.T) {
		eq(t, strings.Contains(fg(t, `{"showCost": false}`, cost), "$"), false)
	})
	tw(t, "cost-display", "says nothing for a model with no pricing data", func(t *testing.T) {
		text := fg(t, `{"showCost": true}`, 0)
		eq(t, strings.Contains(text, "1.2k token"), true) // tokens are still exact
		eq(t, strings.Contains(text, "$"), false)
	})
	result := func(t *testing.T, c float64) string {
		r := usageRig(t, `{"showCost": true}`)
		fgRun(t, r, &lifetimeUsage{Input: 1000, Output: 200, Cost: c}, "call-1")
		return r.must("get_subagent_result", obj{"agent_id": "general-purpose"})
	}
	tw(t, "cost-display", "reports the cost as its own labelled field", func(t *testing.T) {
		eq(t, strings.Contains(result(t, cost), "Cost: ~$0.0123"), true)
	})
	tw(t, "cost-display", "omits the field entirely when unpriced", func(t *testing.T) {
		eq(t, strings.Contains(result(t, 0), "Cost:"), false)
	})
	notification := func(t *testing.T, settings string, c float64) string {
		r := usageRig(t, settings)
		n := r.fleet.count()
		r.must("Agent", obj{"prompt": "go", "description": "spend", "subagent_type": "general-purpose", "run_in_background": true})
		child := r.fleet.wait(t, n+1)[n]
		child.spend(lifetimeUsage{Input: 1000, Output: 200, Cost: c})
		child.finishWith(&childResult{Text: "done"})
		for i := 0; i < 400 && len(r.messages()) == 0; i++ {
			sleepMs(10)
		}
		msgs := r.messages()
		if len(msgs) == 0 {
			t.Fatal("no notification")
		}
		return msgs[len(msgs)-1]["message"].(map[string]any)["content"].(string)
	}
	tw(t, "cost-display", "includes the cost in the usage block when enabled", func(t *testing.T) {
		eq(t, strings.Contains(notification(t, `{"showCost": true, "defaultJoinMode": "async"}`, cost), "<estimated_cost_usd>0.0123</estimated_cost_usd>"), true)
	})
	tw(t, "cost-display", "omits it when disabled — this is LLM context, not a display", func(t *testing.T) {
		text := notification(t, `{"showCost": false, "defaultJoinMode": "async"}`, cost)
		eq(t, strings.Contains(text, "<total_tokens>"), true)
		eq(t, strings.Contains(text, "estimated_cost_usd"), false)
	})
	tw(t, "cost-display", "omits it for a model with no pricing data", func(t *testing.T) {
		eq(t, strings.Contains(notification(t, `{"showCost": true, "defaultJoinMode": "async"}`, 0), "estimated_cost_usd"), false)
	})
	tskip(t, "cost-display", "totals a group, so nobody adds four figures by hand", "the group total is drawn by the notification renderer (a TUI component), twinned by the UI lane")
	tskip(t, "cost-display", "does not total a single agent — the line above already says it", "the single-agent line is drawn by the notification renderer (a TUI component), twinned by the UI lane")
	tskip(t, "cost-display", "shows no total, and no per-agent cost, when unpriced", "the unpriced rows are drawn by the notification renderer (a TUI component), twinned by the UI lane")
	tskip(t, "cost-display", "shows nothing when the setting is off", "the showCost gate of the drawn notification belongs to the renderer (a TUI component), twinned by the UI lane")
}

func TestFormatCost(t *testing.T) {
	for in, want := range map[float64]string{0: "", -1: "", 0.00001: "<$0.0001", 0.0001: "~$0.0001", 0.0042: "~$0.0042", 0.0123: "~$0.0123", 0.5: "~$0.50", 0.12: "~$0.12", 0.12345: "~$0.1235", 1: "~$1.00", 12.3456: "~$12.35"} {
		eq(t, formatCost(in), want)
	}
}

func TestUsageReachesSessionStatsTwins(t *testing.T) {
	tskip(t, "e2e/usage-reaches-session-stats.e2e", "pi adds our reported tokens and cost to getSessionStats()", "the host's own accounting (Pi folds toolResult.usage into getSessionStats): not extension code; the extension's half, the `usage` object on the tool result, is twinned in usage-reporting")
	tskip(t, "e2e/usage-reaches-session-stats.e2e", "leaves the context-window percentage alone", "the host's own context-window projection, not extension code")
	tskip(t, "e2e/usage-reaches-session-stats.e2e", "counts nothing for a tool result that carries no usage", "the host's own accounting, not extension code; that a result carries no `usage` is twinned in usage-reporting")
}

// The display readers of usage.test.ts read an in-process session (getSessionStats), which a child process has no
// Go form of. They are twinned where the readers live, in the ui-core slice (ui_usage.go), which stacks after this one.
func TestUsageDisplayReadersAreTheUICoreSlices(t *testing.T) {
	tskip(t, "usage", "uses billed-token semantics (input + output + cacheWrite), not inflated total", "getSessionTokens is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
	tskip(t, "usage", "returns 0 when session is undefined or stats throw", "the no-session and throwing-stats paths of getSessionTokens is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
	tskip(t, "usage", "returns null when contextUsage is unavailable", "getSessionContextPercent with no contextUsage is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
	tskip(t, "usage", "returns null when percent is null (post-compaction)", "getSessionContextPercent with a null percent is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
	tskip(t, "usage", "returns the upstream percent when available", "getSessionContextPercent with a percent is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
	tskip(t, "usage", "sums components and handles undefined", "getLifetimeTotal over absent components is twinned in the ui-core slice, with ui_usage.go's lifetimeUsage")
	tskip(t, "usage", "agrees with getSessionTokens pre-compaction, diverges after", "the comparison with getSessionTokens is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
	tskip(t, "usage", "stays monotone across simulated compaction when fed via addUsage-style accumulation", "the compaction simulation over getSessionTokens is twinned in the ui-core slice, with ui_usage.go's sessionLike reader")
}
