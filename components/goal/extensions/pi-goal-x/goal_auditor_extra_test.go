package pi_goal_x

import (
	"strings"
	"testing"
)

// Extra (non-twin) coverage of behavior the original's tests leave unpinned: progress derivation from session events,
// the tail helper, the prompt's task/budget/warm sections, advice validation and the armed follow-up marker.

func TestGoalAuditorProgressFromEvents(t *testing.T) {
	var seen []auditorProgress
	sess := &fakeAuditorSession{}
	sess.onPrompt = func(string) error {
		l := sess.listener
		l(auditorSessionEvent{typ: "agent_start"})
		l(auditorSessionEvent{typ: "tool_execution_start", toolName: "grep", args: jo(t, `{"pattern":"x"}`)})
		l(auditorSessionEvent{typ: "tool_execution_end"})
		l(auditorSessionEvent{typ: "tool_execution_start", toolName: "read", args: "plain"})
		l(auditorSessionEvent{typ: "message_update", streamType: "thinking_start"})
		l(auditorSessionEvent{typ: "message_update", streamType: "thinking_end"})
		l(auditorSessionEvent{typ: "message_update", message: &auditorMessage{role: "assistant", content: []auditorContentPart{{"text", new("a\n\nb\nc\nd\ne\nf\ng")}}}})
		l(auditorSessionEvent{typ: "message_end", message: &auditorMessage{role: "user", content: []auditorContentPart{{"text", new("ignored")}}}})
		l(auditorSessionEvent{typ: "message_end", message: &auditorMessage{role: "assistant", content: []auditorContentPart{{"text", new("Report\n<approved/>")}}}})
		return nil
	}
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{
		ctx: &fakeAuditorCtx{dir: t.TempDir(), mdl: &auditorModel{provider: "p", id: "m"}}, goal: auditorGoal(t, "x"), detailedSummary: "d",
		createSession: auditorSessionOf(sess), onProgress: func(p auditorProgress) { seen = append(seen, p) },
	})
	eq(t, res.approved, true, "approved")
	eq(t, res.output, "Report\n<approved/>", "output")
	eq(t, res.model, "p/m", "model label")
	eq(t, seen[0].label, "Starting audit...", "initial label")
	var grep, end, thinking, report *auditorProgress
	for i := range seen {
		p := &seen[i]
		switch {
		case p.currentTool == "grep" && grep == nil:
			grep = p
		case p.phase == "thinking" && thinking == nil:
			thinking = p
		case p.phase == "producing_report" && p.label == "Producing report..." && report == nil:
			report = p
		}
		if p.currentTool == "" && p.phase == "running" && p.label == "Searching content..." && end == nil {
			end = p
		}
	}
	eq(t, grep != nil && grep.phase == "tool_executing" && grep.label == "Searching content..." && *grep.percentage == 50 && grep.currentToolArgs == `{"pattern":"x"}`, true, "grep progress")
	eq(t, end != nil, true, "tool end clears the tool")
	eq(t, thinking != nil && thinking.label != "", true, "thinking")
	eq(t, report != nil && *report.percentage == 90, true, "report progress")
	last := seen[len(seen)-1]
	eq(t, last.phase == "done" && last.label == "Audit complete." && *last.percentage == 100, true, "done")
	// message_update streams the last 5 non-empty lines.
	found := false
	for _, p := range seen {
		if p.phase == "producing_report" && len(p.recentOutput) == 5 && p.recentOutput[0] == "c" && p.recentOutput[4] == "g" {
			found = true
		}
	}
	eq(t, found, true, "recent output tail")
	// percentage never decreases: read (25) after grep (50) keeps 50.
	for _, p := range seen {
		if p.currentTool == "read" {
			eq(t, *p.percentage, float64(50), "max percentage")
			break
		}
	}
}

func TestGoalAuditorHelpers(t *testing.T) {
	eq(t, recentNonEmptyLines("a\n\nb\n  \nc", 2), []string{"b", "c"}, "tail")
	eq(t, recentNonEmptyLines("", 3), []string(nil), "empty")
	eq(t, recentNonEmptyLines("x\ny", 5), []string{"x", "y"}, "all")
	for tool, label := range map[string]string{"read": "Inspecting files...", "grep": "Searching content...", "find": "Locating files...", "ls": "Listing directory...", "bash": "Running verification commands...", "zap": "Inspecting (zap)..."} {
		eq(t, labelForReadOnlyTool(tool), label, tool)
	}
	for tool, n := range map[string]int{"read": 25, "ls": 25, "find": 25, "grep": 50, "bash": 75, "zap": 25} {
		eq(t, estimateAuditProgress(tool), n, tool)
	}
	// provider-less model string resolution
	m1, m2 := &auditorModel{provider: "a", id: "x", name: "X"}, &auditorModel{provider: "b", id: "y"}
	reg := &fakeAuditorRegistry{available: []*auditorModel{m1, m2}, findFn: func(p, id string) *auditorModel {
		if p == "b" && id == "y" {
			return m2
		}
		return nil
	}}
	ctx := &fakeAuditorCtx{reg: reg}
	got, _ := resolveAuditorModel(ctx, goalSettings{model: "b/y"})
	eq(t, got == m2, true, "slash form")
	_, msg := resolveAuditorModel(ctx, goalSettings{model: "c/z"})
	eq(t, msg, "Configured auditor model not found: c/z", "slash missing")
	got, _ = resolveAuditorModel(ctx, goalSettings{model: "X"})
	eq(t, got == m1, true, "by name")
	_, msg = resolveAuditorModel(ctx, goalSettings{model: "nope"})
	eq(t, msg, "Configured auditor model is ambiguous or unavailable: nope", "ambiguous")
	_, msg = resolveAuditorModel(ctx, goalSettings{provider: "a", model: "q"})
	eq(t, msg, "Configured auditor model not found: a/q", "explicit missing")
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: &fakeAuditorCtx{dir: t.TempDir(), reg: reg}, goal: auditorGoal(t, "x")})
	eq(t, res.err, errNoAuditorSessionFactory.Error(), "no factory")
}

func TestGoalAuditorPromptSections(t *testing.T) {
	g := auditorGoal(t, "Obj")
	g.set("tokenBudget", float64(5000))
	g.obj("usage").set("tokensUsed", float64(1200))
	g.set("taskList", jo(t, `{"blockCompletion":true,"proposedAt":"p","tasks":[{"id":"t1","title":"A & B","status":"complete","subtasks":[{"id":"t1a","title":"sub","status":"skipped"}]},{"id":"t2","title":"C","status":"pending"}]}`))
	g.set("verificationContract", " Prove it ")
	prompt := buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g, warmContext: " ctx "})
	for _, s := range []string{
		"Goal id: g1\nStatus: running\nMode: regular\nLifetime spending cap: 5000 tokens (1200 cumulatively used; not context occupancy)",
		"Tasks: 1/3 complete, 1 skipped | TASK GATE: pending tasks block completion\n[x] t1: A &amp; B\n  [~] t1a: sub\n[ ] t2: C",
		"<task_state>", "<verification_contract>\nProve it\n</verification_contract>", "<warm_context>\nctx\n</warm_context>",
	} {
		eq(t, strings.Contains(prompt, s), true, s)
	}
	off := buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g, settings: &goalSettings{disableTasks: true, disableContracts: true}})
	eq(t, strings.Contains(off, "<task_state>") || strings.Contains(off, "<verification_contract>") || strings.Contains(off, "3. Verify"), false, "disabled sections")
	g.set("sisyphus", true)
	eq(t, strings.Contains(buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g}), "Mode: sisyphus"), true, "sisyphus")
}

func TestGoalOracleAdviceAndMarker(t *testing.T) {
	good := jo(t, actionableAdviceJSON)
	adv, msg := validateAdvice(good)
	eq(t, msg, "", "valid")
	eq(t, adv.alternatives[0].steps[1], "Kill lingering connections", "steps")
	for body, want := range map[string]string{
		`{"diagnosis":" "}`:                   "diagnosis required",
		`{"diagnosis":"d","alternatives":[]}`: "1-4 alternatives required",
		`{"diagnosis":"d","alternatives":[{"title":1,"rationale":"r","steps":["s"]}]}`:                                          "alternative title/rationale required",
		`{"diagnosis":"d","alternatives":[{"title":"t","rationale":"r","steps":[]}]}`:                                           "steps must be non-empty strings",
		`{"diagnosis":"d","alternatives":[{"title":"t","rationale":"r","steps":["s"]}],"recommendedIndex":1}`:                   "recommendedIndex out of range",
		`{"diagnosis":"d","alternatives":[{"title":"t","rationale":"r","steps":["s"]}],"recommendedIndex":0,"disposition":"x"}`: "disposition must be actionable|needs_human|insufficient_context",
	} {
		_, got := validateAdvice(jo(t, body))
		eq(t, got, want, body)
	}
	_, got := validateAdvice(nil)
	eq(t, got, "advice must be an object", "nil")

	id := armOracleAdvice("g-arm", "fp", *adv)
	eq(t, id, "fp-Drain_the_pool_before_mi", "adviceId")
	eq(t, hasPendingOracleAdviceForFocusedGoal("g-arm"), true, "pending")
	armed, _ := consumeOracleFollowupMarker("g-arm")
	eq(t, hasPendingOracleAdviceForFocusedGoal("g-arm"), false, "consumed")
	eq(t, armed.text, "Oracle suggested an alternative path:\nDrain the pool before migrating — The lock clears once connections close; then ALTER TABLE succeeds.\nSteps:\n  1. Stop the app server\n  2. Kill lingering connections\n  3. Re-run the migration\nExpected evidence: migration output shows OK\nAttempt this path before reporting blocked again.", "armed text")
	eq(t, strings.HasSuffix(renderOracleAdviceReminder(armed), "(The same blocker fingerprint already received Oracle advice; no new consultation will run until you attempt it.)"), true, "reminder")
	eq(t, renderActionableOracleAdvice(*adv), "Oracle diagnosis: The migration fails because the schema lock is held by a stale connection pool.\nRecommended alternative: Drain the pool before migrating — The lock clears once connections close; then ALTER TABLE succeeds.\n  1. Stop the app server\n  2. Kill lingering connections\n  3. Re-run the migration\nTry this path before calling update_goal({status:\"blocked\"}) again.", "actionable text")

	g := oracleGoal()
	p := buildBlockerOraclePrompt(g, "a<b", []string{"tried x"}, "  evidence ")
	eq(t, p, "You are a read-only blocker adviser.\n\nFind concrete alternative paths. Do not mutate files.\n\nDo not mark the goal complete or blocked.\n\nReturn advice only through submit_goal_oracle_advice.\n\n<objective>\n\nShip the risky migration.\n\n</objective>\n\n<blocker_reason>\na&lt;b\n</blocker_reason>\n\n<attempted_actions>\n- tried x\n</attempted_actions>\n\n<recent_evidence>\n  evidence \n</recent_evidence>\n\nInspect relevant files with read-only tools before advising when needed.", "prompt")
}
