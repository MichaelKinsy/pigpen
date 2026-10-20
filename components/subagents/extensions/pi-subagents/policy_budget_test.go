package pi_subagents

import (
	"math"
	"testing"
)

func budgetOf(t *testing.T, s string) resolvedToolBudget {
	t.Helper()
	b, msg := validateToolBudgetConfig(js(t, s), "toolBudget", 1)
	if msg != "" {
		t.Fatal(msg)
	}
	return *b
}

func TestToolBudget(t *testing.T) {
	tw(t, "tool-budget", "defaults block tools to read/search tools", func(t *testing.T) {
		b := budgetOf(t, `{"hard":5}`)
		equal(t, b, resolvedToolBudget{Hard: 5, Block: defaultToolBudgetBlock}, "budget")
	})
	tw(t, "tool-budget", "accepts soft and wildcard block", func(t *testing.T) {
		equal(t, budgetOf(t, `{"soft":2,"hard":4,"block":"*"}`), resolvedToolBudget{Hard: 4, Soft: 2, HasSoft: true, AllBlocked: true}, "budget")
	})
	tw(t, "tool-budget", "accepts a zero hard limit only when the internal minimum opts in", func(t *testing.T) {
		b, _ := validateToolBudgetConfig(js(t, `{"hard":0,"block":"*"}`), "toolBudget", 0)
		equal(t, *b, resolvedToolBudget{Hard: 0, AllBlocked: true}, "zero hard")
		_, msg := validateToolBudgetConfig(js(t, `{"soft":0,"hard":0,"block":"*"}`), "toolBudget", 0)
		equal(t, msg, "toolBudget.soft must be an integer >= 1 when provided.", "soft 0")
	})
	tw(t, "tool-budget", "rejects unsafe configs", func(t *testing.T) {
		for in, want := range map[string]string{
			`{"hard":0}`:              "toolBudget.hard must be an integer >= 1.",
			`{"soft":5,"hard":4}`:     "toolBudget.soft must be <= toolBudget.hard.",
			`{"hard":4,"block":[]}`:   "toolBudget.block must contain at least one tool name.",
			`{"hard":4,"block":[""]}`: "toolBudget.block must contain non-empty tool names.",
		} {
			_, msg := validateToolBudgetConfig(js(t, in), "toolBudget", 1)
			equal(t, msg, want, in)
		}
	})
	tw(t, "tool-budget", "tracks state and block decisions", func(t *testing.T) {
		b := resolvedToolBudget{Soft: 2, HasSoft: true, Hard: 3, Block: []string{"read"}}
		equal(t, initialToolBudgetState(b), toolBudgetState{resolvedToolBudget: b, Outcome: "within-budget"}, "initial")
		equal(t, toolBudgetStateFor(b, 2, "").Outcome, "soft-reached", "soft")
		equal(t, toolBudgetStateFor(b, 4, "read").Outcome, "hard-blocked", "hard")
		equal(t, shouldBlockToolForBudget(b, "read", 4), true, "read blocked")
		equal(t, shouldBlockToolForBudget(b, "write", 4), false, "write allowed")
		equal(t, shouldBlockToolForBudget(resolvedToolBudget{Hard: 0, AllBlocked: true}, "read", 1), true, "all blocked")
	})
	tw(t, "tool-budget", "recognizes only the exact runtime block message for this budget and tool", func(t *testing.T) {
		budget := resolvedToolBudget{Hard: 0, AllBlocked: true}
		exact := toolBudgetBlockedMessage(budget, "bash", 1)
		equal(t, isToolBudgetBlockedMessage(budget, exact, "bash"), true, "exact")
		two := resolvedToolBudget{Hard: 2, AllBlocked: true}
		equal(t, isToolBudgetBlockedMessage(two, toolBudgetBlockedMessage(two, "bash", 3), "bash"), true, "plural count past the hard limit")
		equal(t, isToolBudgetBlockedMessage(budget, toolBudgetBlockedMessage(resolvedToolBudget{Hard: 3, AllBlocked: true}, "bash", 4), "bash"), false, "different hard limit")
		equal(t, isToolBudgetBlockedMessage(budget, toolBudgetBlockedMessage(budget, "read", 1), "bash"), false, "different tool name")
		equal(t, isToolBudgetBlockedMessage(budget, "src/log.md:12:"+exact, "bash"), false, "phrase embedded in ordinary tool output")
		equal(t, isToolBudgetBlockedMessage(budget, exact+"\nnext line", "bash"), false, "trailing output lines")
		equal(t, isToolBudgetBlockedMessage(budget, exact, ""), false, "missing resolved tool name fails closed")
		equal(t, isToolBudgetBlockedMessage(budget, "Tool budget hard limit reached after 0 tool call (hard 0). The 'bash' tool is blocked so you can finalize from the context you already have.", "bash"), false, "blocked count must exceed the hard limit")
	})
	tw(t, "tool-budget", "formats user-facing budget messages", func(t *testing.T) {
		b := resolvedToolBudget{Soft: 2, HasSoft: true, Hard: 3, Block: []string{"read"}}
		polMatches(t, toolBudgetSoftNudge(b, 2), `soft limit reached after 2 tool calls`)
		polMatches(t, toolBudgetBlockedMessage(b, "read", 4), `'read' tool is blocked`)
	})
}

func TestAuthorityPolicy(t *testing.T) {
	tw(t, "authority-policy", "uses conservative cleanup and spawn defaults without obstructing normal control", func(t *testing.T) {
		for action, want := range map[string]string{"discardWorktree": "confirm", "destructiveCleanup": "confirm", "spawnBudgetGrant": "confirm", "scheduleCreate": "auto", "stopRun": "auto", "steerRun": "auto"} {
			equal(t, resolveAuthorityDecision(action, nil), want, action)
		}
	})
	tw(t, "authority-policy", "confirms project panes that spawn an unchecked host and leaves plugin-gated inspectors automatic", func(t *testing.T) {
		equal(t, resolveAuthorityDecision("inspectorOpen", nil), "auto", "inspectorOpen")
		equal(t, resolveAuthorityDecision("projectOpen", nil), "confirm", "projectOpen")
		policy, err := validateAuthorityPolicy(js(t, `{"inspectorOpen":"forbid","projectOpen":"auto"}`), "config.authorityPolicy")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, resolveAuthorityDecision("inspectorOpen", policy), "forbid", "inspectorOpen policy")
		equal(t, resolveAuthorityDecision("projectOpen", policy), "auto", "projectOpen policy")
	})
	tw(t, "authority-policy", "accepts only fixed actions and decisions", func(t *testing.T) {
		policy, _ := validateAuthorityPolicy(js(t, `{"discardWorktree":"forbid","stopRun":"confirm"}`), "config.authorityPolicy")
		equal(t, resolveAuthorityDecision("discardWorktree", policy), "forbid", "discardWorktree")
		equal(t, resolveAuthorityDecision("stopRun", policy), "confirm", "stopRun")
		_, err := validateAuthorityPolicy(js(t, `{"mergePullRequest":"auto"}`), "config.authorityPolicy")
		if err == nil {
			t.Fatal("unknown action accepted")
		}
		polMatches(t, err.Error(), `unknown`)
		equal(t, err.Error(), "config.authorityPolicy.mergePullRequest is unknown; expected one of discardWorktree, destructiveCleanup, spawnBudgetGrant, scheduleCreate, stopRun, steerRun, inspectorOpen, projectOpen", "message")
		_, err = validateAuthorityPolicy(js(t, `{"discardWorktree":"sometimes"}`), "config.authorityPolicy")
		if err == nil {
			t.Fatal("bad decision accepted")
		}
		polMatches(t, err.Error(), `auto.*confirm.*forbid`)
	})
}

func TestSpawnBudget(t *testing.T) {
	none := envOf(nil)
	capped := extensionConfig{MaxSubagentSpawnsPerSession: 4.0}
	state := func() *subagentState { return &subagentState{CurrentSessionID: "session-a"} }
	tw(t, "spawn-budget", "keeps unset and zero caps unlimited without cumulative accounting", func(t *testing.T) {
		for _, config := range []extensionConfig{{}, {MaxSubagentSpawnsPerSession: 0.0}} {
			s := state()
			snap, errMsg := reserveSpawnBudget(s, config, "session-a", 50, none)
			equal(t, errMsg, "", "error")
			equal(t, snap, spawnBudgetSnapshot{GrantHistory: []grantRecord{}}, "snapshot")
			equal(t, formatSpawnBudget(snap), "Spawn budget: unlimited", "format")
		}
	})
	tw(t, "spawn-budget", "preflights declared capacity without reserving partial work", func(t *testing.T) {
		s := state()
		s.Spawns = &spawnCounters{SessionID: "session-a", Count: 2}
		_, errMsg := preflightSpawnBudget(s, capped, "session-a", 3, none)
		polMatches(t, errMsg, `2/4 used, 3 requested\).*2 remaining`)
		equal(t, s.Spawns.Count, 2, "count")
	})
	tw(t, "spawn-budget", "reserves cumulative declared capacity", func(t *testing.T) {
		s := state()
		first, errMsg := reserveSpawnBudget(s, capped, "session-a", 3, none)
		equal(t, errMsg, "", "first error")
		equal(t, first.Used, 3, "used")
		equal(t, *first.Remaining, 1, "remaining")
		_, errMsg = reserveSpawnBudget(s, capped, "session-a", 2, none)
		polMatches(t, errMsg, `3/4 used, 2 requested\).*1 remaining`)
		equal(t, s.Spawns.Count, 3, "count")
	})
	tw(t, "spawn-budget", "grants at most the original configured limit and keeps bounded audit records", func(t *testing.T) {
		s := state()
		s.Spawns = &spawnCounters{SessionID: "session-a", Count: 4}
		first, errMsg := grantSpawnBudget(s, capped, "session-a", 3.0, 100, none)
		equal(t, errMsg, "", "first")
		equal(t, *first.Limit, 7, "limit")
		equal(t, *first.Remaining, 3, "remaining")
		equal(t, *first.GrantRemaining, 1, "grantRemaining")
		equal(t, first.GrantHistory, []grantRecord{{SessionID: "session-a", Amount: 3, GrantedAt: 100, PreviousLimit: 4, Limit: 7}}, "history")
		rejected, errMsg := grantSpawnBudget(s, capped, "session-a", 2.0, 200, none)
		polMatches(t, errMsg, `2 requested but only 1`)
		equal(t, *rejected.Limit, 7, "limit after rejection")
		final, errMsg := grantSpawnBudget(s, capped, "session-a", 1.0, 300, none)
		equal(t, errMsg, "", "final")
		equal(t, *final.Limit, 8, "final limit")
		equal(t, final.Granted, 4, "granted")
		equal(t, *final.GrantRemaining, 0, "grantRemaining")
	})
	tw(t, "spawn-budget", "bounds grant history while preserving aggregate granted capacity", func(t *testing.T) {
		s := state()
		config := extensionConfig{MaxSubagentSpawnsPerSession: 100.0}
		for i := 0; i < 25; i++ {
			if _, errMsg := grantSpawnBudget(s, config, "session-a", 1.0, int64(i), none); errMsg != "" {
				t.Fatal(errMsg)
			}
		}
		snap := getSpawnBudgetSnapshot(s, config, "session-a", none)
		equal(t, snap.Granted, 25, "granted")
		equal(t, *snap.Limit, 125, "limit")
		equal(t, len(snap.GrantHistory), 20, "history length")
		equal(t, snap.GrantHistory[0].GrantedAt, int64(5), "oldest kept")
	})
	tw(t, "spawn-budget", "rejects meaningless or invalid grants", func(t *testing.T) {
		s := state()
		_, m := grantSpawnBudget(s, extensionConfig{}, "session-a", 1.0, 0, none)
		polMatches(t, m, `no configured spawn cap`)
		_, m = grantSpawnBudget(s, capped, "session-a", 0.0, 0, none)
		polMatches(t, m, `positive integer`)
		_, m = grantSpawnBudget(s, capped, "session-a", 1.5, 0, none)
		polMatches(t, m, `positive integer`)
	})
	tw(t, "spawn-budget", "resets usage and grants only when the logical session changes", func(t *testing.T) {
		s := state()
		reserveSpawnBudget(s, capped, "session-a", 2, none)
		grantSpawnBudget(s, capped, "session-a", 2.0, 100, none)
		same := getSpawnBudgetSnapshot(s, capped, "session-a", none)
		equal(t, same.Used, 2, "used")
		equal(t, same.Granted, 2, "granted")
		other := getSpawnBudgetSnapshot(s, capped, "session-b", none)
		equal(t, other.Used, 0, "new used")
		equal(t, other.Granted, 0, "new granted")
		equal(t, *other.Limit, 4, "new limit")
	})
}

func TestToolTimeout(t *testing.T) {
	resolve := func(in toolTimeoutInput) [3]any {
		ms, set, e := resolveToolTimeoutMs(in)
		return [3]any{ms, set, e}
	}
	s := func(v string) *string { return &v }
	tw(t, "tool-timeout", "has no configured hard timeout when nothing is configured anywhere", func(t *testing.T) {
		equal(t, resolve(toolTimeoutInput{}), [3]any{0, false, ""}, "unset")
	})
	tw(t, "tool-timeout", "honors precedence: per-call > agent frontmatter > config > env", func(t *testing.T) {
		in := toolTimeoutInput{CallValue: 1000.0, AgentValue: 2000.0, ConfigValue: 3000.0, EnvValue: s("4000")}
		equal(t, resolve(in), [3]any{1000, true, ""}, "call")
		in.CallValue = nil
		equal(t, resolve(in), [3]any{2000, true, ""}, "agent")
		in.AgentValue = nil
		equal(t, resolve(in), [3]any{3000, true, ""}, "config")
		in.ConfigValue = nil
		equal(t, resolve(in), [3]any{4000, true, ""}, "env")
	})
	tw(t, "tool-timeout", "accepts the env value as a plain integer string", func(t *testing.T) {
		equal(t, resolve(toolTimeoutInput{EnvValue: s("60000")}), [3]any{60000, true, ""}, "env")
	})
	tw(t, "tool-timeout", "rejects non-positive, non-integer, and oversized values with an error", func(t *testing.T) {
		for _, bad := range []any{0.0, -1.0, 1.5, "abc", mathNaN()} {
			_, _, e := resolveToolTimeoutMs(toolTimeoutInput{CallValue: bad})
			if e == "" {
				t.Errorf("no error for %v", bad)
			}
			polMatches(t, e, `positive integer`)
		}
		_, _, e := resolveToolTimeoutMs(toolTimeoutInput{CallValue: 2147483648.0})
		polMatches(t, e, `no larger than`)
	})
	tw(t, "tool-timeout", "ignores an empty env string (treated as unset)", func(t *testing.T) {
		equal(t, resolve(toolTimeoutInput{EnvValue: s("  ")}), [3]any{0, false, ""}, "blank env")
	})
	tw(t, "tool-timeout", "uses a default hard timeout only for known-fast tools", func(t *testing.T) {
		for _, tool := range []string{"read", "grep", "find", "ls", "edit", "write", "structured_output"} {
			d, ok := defaultToolTimeoutMs(tool)
			equal(t, [2]any{d, ok}, [2]any{defaultFastToolTimeoutMs, true}, tool)
			e, ok := effectiveToolTimeoutMs(tool, nil)
			equal(t, [2]any{e, ok}, [2]any{defaultFastToolTimeoutMs, true}, tool+" effective")
		}
		_, ok := defaultToolTimeoutMs("bash")
		equal(t, ok, false, "bash default")
		_, ok = effectiveToolTimeoutMs("bash", nil)
		equal(t, ok, false, "bash effective")
		equal(t, defaultFastToolTimeoutTools, []string{"read", "grep", "find", "ls", "edit", "write", "structured_output"}, "tools")
	})
	tw(t, "tool-timeout", "lets an explicit configured timeout win for non-exempt tools", func(t *testing.T) {
		n := 1234
		for _, tool := range []string{"bash", "read"} {
			e, ok := effectiveToolTimeoutMs(tool, &n)
			equal(t, [2]any{e, ok}, [2]any{1234, true}, tool)
		}
	})
	tw(t, "tool-timeout", "never applies a tool timeout to blocking coordination tools", func(t *testing.T) {
		n := 1234
		for _, tool := range []string{"contact_supervisor", "intercom", "bg_wait"} {
			equal(t, isToolTimeoutExempt(tool), true, tool+" exempt")
			_, ok := effectiveToolTimeoutMs(tool, &n)
			equal(t, ok, false, tool+" with config")
			_, ok = effectiveToolTimeoutMs(tool, nil)
			equal(t, ok, false, tool+" without")
		}
		equal(t, isToolTimeoutExempt("bash"), false, "bash")
		equal(t, isToolTimeoutExempt(""), false, "undefined")
	})
	tw(t, "tool-timeout", "formats timeout errors consistently", func(t *testing.T) {
		equal(t, formatToolTimeoutMessage("bash", 1000), "Tool 'bash' exceeded its timeout of 1000ms.", "message")
	})
	tw(t, "tool-timeout", "uses toolCallId when present and falls back to an anonymous sequence", func(t *testing.T) {
		equal(t, toolTimeoutCallKey("call-1", "read", 9), "id:call-1", "id")
		equal(t, toolTimeoutCallKey(nil, "read", 9), "anon:read:9", "anon")
	})
	tw(t, "tool-timeout", "reads PI_SUBAGENT_TOOL_TIMEOUT_MS from the process env", func(t *testing.T) {
		equal(t, toolTimeoutEnv, "PI_SUBAGENT_TOOL_TIMEOUT_MS", "name")
		equal(t, *toolTimeoutFromEnv(envOf(map[string]string{"PI_SUBAGENT_TOOL_TIMEOUT_MS": "123"})), "123", "set")
		equal(t, toolTimeoutFromEnv(envOf(nil)) == nil, true, "unset")
	})
}

func mathNaN() float64 { return math.NaN() }

func TestUsage(t *testing.T) {
	live := usage{Input: 20, Output: 10, CacheRead: 3, CacheWrite: 4, Cost: 2, Turns: 9}
	assistant := func(usageJSON string, ts ...int) any {
		o := newObject()
		o.set("role", "assistant")
		o.set("content", []any{})
		o.set("usage", js(t, usageJSON))
		if len(ts) > 0 {
			o.set("timestamp", float64(ts[0]))
		}
		return o
	}
	user := func() any { o := newObject(); o.set("role", "user"); return o }
	tw(t, "usage-reconciliation", "applies the public field-completeness and attempt-baseline contract", func(t *testing.T) {
		r := reconcileAttemptUsage
		equal(t, r(live, []any{user()}, 0), live, "no assistant")
		equal(t, r(live, []any{assistant(`{"input":999,"output":999,"cacheRead":999,"cacheWrite":999,"cost":999}`), user(), assistant(`{"input":2,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.2}}`)}, 2), usage{2, 1, 0, 0, 0.2, 1}, "baseline")
		equal(t, r(live, []any{assistant(`{"input":2,"output":1,"cacheRead":3,"cacheWrite":4,"cost":0.25}`), assistant(`{"input":4,"output":3,"cacheRead":2,"cacheWrite":1,"cost":{"total":0.5}}`)}, 0), usage{6, 4, 5, 5, 0.75, 2}, "sum")
		equal(t, r(live, []any{assistant(`{"input":-1,"inputTokens":5,"output":null,"outputTokens":2,"cacheRead":0,"cacheWriteTokens":1,"cost":{"total":0}}`)}, 0), usage{5, 2, 0, 1, 0, 1}, "aliases and invalid numbers")
		equal(t, r(usage{Turns: 4}, []any{assistant(`{"inputTokens":5,"outputTokens":2,"cacheReadTokens":1,"cacheWriteTokens":1,"cost":0.25}`)}, 0), usage{5, 2, 1, 1, 0.25, 1}, "zero live")
		equal(t, r(live, []any{assistant(`{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":0}`)}, 0), usage{0, 0, 0, 0, 0, 1}, "zeros")
		equal(t, r(live, []any{assistant(`{"input":1,"output":1,"cacheRead":1,"cacheWrite":1,"cost":1}`), assistant(`{"input":2,"output":"bad","cacheRead":-1,"cacheWrite":2,"cost":{}}`)}, 0), usage{3, 10, 3, 3, 2, 2}, "incomplete fields keep live")
		same := `{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"cost":5}`
		equal(t, r(live, []any{assistant(same, 10), assistant(same, 10), assistant(same, 11)}, 0), usage{2, 4, 6, 8, 10, 2}, "adjacent duplicates")
		equal(t, r(live, []any{assistant(`{"input":1}`, 10), user(), assistant(`{"input":1}`, 10)}, 0).Turns, 2, "only adjacent duplicate projections collapse")
		equal(t, r(live, []any{assistant(`{"input":1}`), assistant(`{"input":1}`)}, 0).Turns, 2, "messages without timestamp identity remain distinct")
		equal(t, r(live, []any{assistant(`{"input":1}`, 10)}, 2), live, "contracted message lists retain live evidence")
	})
}

func TestTotalCost(t *testing.T) {
	r := func(u usage) singleResultUsage { return singleResultUsage{Usage: u} }
	a, b := usage{10, 5, 1, 2, 0.01, 1}, usage{20, 7, 3, 4, 0.03, 2}
	tw(t, "total-cost", "aggregates full child usage", func(t *testing.T) {
		equal(t, sumResultsUsage([]singleResultUsage{r(a), r(b)}), usage{30, 12, 4, 6, 0.04, 3}, "usage")
	})
	tw(t, "total-cost", "aggregates input tokens, output tokens, and cost", func(t *testing.T) {
		equal(t, sumResultsCost([]singleResultUsage{r(a), r(b)}), totalCost{30, 12, 0.04}, "cost")
	})
	tw(t, "total-cost", "returns zero totals when all aggregated fields are zero", func(t *testing.T) {
		equal(t, sumResultsCost([]singleResultUsage{r(usage{CacheRead: 10, CacheWrite: 5, Turns: 2})}), totalCost{}, "zero")
	})
	tw(t, "total-cost", "includes attached nested subagent costs", func(t *testing.T) {
		parent := r(usage{10, 5, 0, 0, 0.01, 1})
		parent.Children = []nestedRun{{TotalCost: &totalCost{20, 7, 0.03}}}
		equal(t, sumResultsCost([]singleResultUsage{parent}), totalCost{30, 12, 0.04}, "nested")
	})
}
