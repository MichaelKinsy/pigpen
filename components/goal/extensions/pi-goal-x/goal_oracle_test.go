package pi_goal_x

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func oracleGoal() *jsObject {
	return createGoal("Ship the risky migration.", true, false, false, ms(2026, 8, 23, 9, 0, 0))
}

func oracleFakeCtx() *fakeAuditorCtx {
	return &fakeAuditorCtx{dir: "/tmp/goal-oracle", reg: &fakeAuditorRegistry{findFn: func(string, string) *auditorModel {
		return &auditorModel{provider: "anthropic", id: "claude-strong"}
	}}}
}

func oracleSettings(provider, model string) resolvedGoalOracleSettings {
	return resolvedGoalOracleSettings{enabled: true, provider: provider, model: model, maxFailedAttemptsPerBlocker: 2}
}

const actionableAdviceJSON = `{"diagnosis":"The migration fails because the schema lock is held by a stale connection pool.","alternatives":[{"title":"Drain the pool before migrating","rationale":"The lock clears once connections close; then ALTER TABLE succeeds.","steps":["Stop the app server","Kill lingering connections","Re-run the migration"],"expectedEvidence":["migration output shows OK"]}],"recommendedIndex":0,"unresolvedQuestions":[],"disposition":"actionable"}`

func findSubmitTool(o auditorSessionOptions) *goalOracleTool {
	for _, tl := range o.customTools {
		if tl.name == "submit_goal_oracle_advice" {
			return tl
		}
	}
	return nil
}

func oracleEvents(t *testing.T, lines ...string) []*jsObject {
	var out []*jsObject
	for _, l := range lines {
		out = append(out, jo(t, l))
	}
	return out
}

func TestGoalOracle(t *testing.T) {
	tw(t, "goal-oracle", "changes with reason/task state but not usage/revision/timestamps", func(t *testing.T) {
		g := oracleGoal()
		fp1 := buildBlockerFingerprint(g, "Tests fail with ECONNREFUSED")
		fp2 := buildBlockerFingerprint(g, "tests  fail  with\nECONNREFUSED ")
		eq(t, fp1, fp2, "whitespace/case normalization")
		g2 := oracleGoal()
		g2.set("id", gstr(g, "id"))
		g2.obj("usage").set("tokensUsed", float64(999999))
		g2.set("revision", float64(42))
		eq(t, buildBlockerFingerprint(g2, "Tests fail with ECONNREFUSED"), fp1, "usage/revision do not change the fingerprint")
		g2.set("taskList", jo(t, `{"tasks":[{"id":"t1","title":"New task","status":"pending"}],"blockCompletion":false,"proposedAt":""}`))
		eq(t, buildBlockerFingerprint(g2, "Tests fail with ECONNREFUSED") != fp1, true, "task state changes the fingerprint")
		eq(t, buildBlockerFingerprint(g, "Different error entirely") != fp1, true, "different reason")
		eq(t, len(fp1), 24, "fingerprint length")
	})
	tw(t, "goal-oracle", "refuses missing and provider-only configuration", func(t *testing.T) {
		ctx := oracleFakeCtx()
		m, msg := resolveOracleModel(ctx, oracleSettings("", ""))
		eq(t, m == nil, true, "missing")
		eq(t, regexp.MustCompile(`not fully configured`).MatchString(msg), true, msg)
		m, _ = resolveOracleModel(ctx, oracleSettings("anthropic", ""))
		eq(t, m == nil, true, "provider only")
		m, msg = resolveOracleModel(ctx, oracleSettings("anthropic", "claude-strong"))
		eq(t, m != nil && msg == "", true, "ok")
	})
	tw(t, "goal-oracle", "shares the parent runtime, isolates resources, and aborts/disposes the child", func(t *testing.T) {
		runtime := &struct{ id string }{"metered-parent"}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		disposed, aborted := 0, 0
		var captured auditorSessionOptions
		reg := &fakeAuditorRegistry{rt: runtime, findFn: func(string, string) *auditorModel { return &auditorModel{provider: "fixture", id: "fixture"} }}
		result := runBlockerOracle(runBlockerOracleArgs{
			ctx:  &fakeAuditorCtx{dir: "/tmp/goal-oracle", reg: reg},
			goal: oracleGoal(), reason: "stuck", signal: ctx,
			settings: oracleSettings("fixture", "fixture"),
			createSession: auditorSessionFactoryFunc(func(o auditorSessionOptions) (auditorSession, error) {
				captured = o
				s := &fakeAuditorSession{
					onPrompt:  func(string) error { cancel(); return nil },
					onAbort:   func() { aborted++ },
					onDispose: func() { disposed++ },
				}
				return s, nil
			}),
		})
		eq(t, captured.modelRuntime == any(runtime), true, "modelRuntime")
		eq(t, captured.inMemorySessionCwd != "", true, "sessionManager")
		eq(t, captured.compactionEnabled, false, "settingsManager compaction off")
		eq(t, regexp.MustCompile(`blocker adviser`).MatchString(captured.resourceLoader.getSystemPrompt()), true, "system prompt")
		eq(t, len(captured.resourceLoader.getExtensions()), 0, "no extensions")
		eq(t, result, oracleRunResult{errorCode: "aborted", message: "Oracle consultation aborted."}, "result")
		eq(t, aborted, 1, "aborted")
		eq(t, disposed, 1, "disposed")
	})
	tw(t, "goal-oracle", "exposes ONLY read-only tools plus the submit tool; no bash/write/edit", func(t *testing.T) {
		var capturedTools []string
		run := runBlockerOracle(runBlockerOracleArgs{
			ctx: oracleFakeCtx(), goal: oracleGoal(), reason: "stuck",
			settings: oracleSettings("anthropic", "claude-strong"),
			createSession: auditorSessionFactoryFunc(func(o auditorSessionOptions) (auditorSession, error) {
				capturedTools = o.tools
				submit := findSubmitTool(o)
				submit.execute("id", jo(t, actionableAdviceJSON))
				return &fakeAuditorSession{}, nil
			}),
		})
		eq(t, capturedTools, []string{"read", "grep", "find", "ls", "submit_goal_oracle_advice"}, "read-only profile enforced")
		eq(t, run.ok, true, "ok")
		if run.ok {
			eq(t, run.advice.disposition, "actionable", "disposition")
		}
	})
	tw(t, "goal-oracle", "reports invalid structured output instead of accepting prose", func(t *testing.T) {
		run := runBlockerOracle(runBlockerOracleArgs{
			ctx: oracleFakeCtx(), goal: oracleGoal(), reason: "stuck",
			settings: oracleSettings("anthropic", "claude-strong"),
			createSession: auditorSessionFactoryFunc(func(o auditorSessionOptions) (auditorSession, error) {
				findSubmitTool(o).execute("id", jo(t, `{"diagnosis":"just prose, no alternatives"}`))
				return &fakeAuditorSession{}, nil
			}),
		})
		eq(t, run.ok, false, "ok")
		eq(t, run.errorCode, "invalid_output", "errorCode")
	})
	tw(t, "goal-oracle", "rebuilds result/failure/follow-up state for one fingerprint only", func(t *testing.T) {
		g := oracleGoal()
		fp := buildBlockerFingerprint(g, "blocked on A")
		id := gstr(g, "id")
		events := oracleEvents(t,
			`{"type":"oracle_started","goalId":"`+id+`","fingerprint":"`+fp+`","provider":"p","model":"m","reason":"blocked on A","at":"t1"}`,
			`{"type":"oracle_result","goalId":"`+id+`","fingerprint":"`+fp+`","adviceId":"adv-1","disposition":"actionable","summary":"try X","at":"t2"}`,
			`{"type":"oracle_followup_attempted","goalId":"`+id+`","fingerprint":"`+fp+`","adviceId":"adv-1","firstToolName":"bash","at":"t3"}`,
			`{"type":"oracle_started","goalId":"`+id+`","fingerprint":"other-fp","provider":"p","model":"m","reason":"other","at":"t4"}`,
			`{"type":"oracle_result","goalId":"`+id+`","fingerprint":"other-fp","adviceId":"adv-2","disposition":"needs_human","summary":"human needed","at":"t5"}`,
		)
		state := oracleStateForFingerprint(events, id, fp)
		eq(t, state.result.adviceID, "adv-1", "adviceId")
		eq(t, state.followupAttempted, true, "followup")
		eq(t, state.failedAttempts, 0, "failed")
	})
	tw(t, "goal-oracle", "demonstrates the full actionable journey", func(t *testing.T) {
		dir := t.TempDir()
		g := createGoal("Journey: actionable advice.", true, false, false, ms(2026, 8, 23, 9, 0, 0))
		g.set("id", "journey-actionable")
		fp := buildBlockerFingerprint(g, "Migration fails")
		started := `{"type":"oracle_started","goalId":"journey-actionable","fingerprint":"` + fp + `","provider":"p","model":"m","reason":"Migration fails","at":"t1"}`
		result := `{"type":"oracle_result","goalId":"journey-actionable","fingerprint":"` + fp + `","adviceId":"adv-A","disposition":"actionable","summary":"drain pool first","at":"t2"}`
		events1 := oracleEvents(t, started, result)
		state := oracleStateForFingerprint(events1, gstr(g, "id"), fp)
		eq(t, state.result.disposition, "actionable", "disposition")
		eq(t, state.followupAttempted, false, "followup")
		consults := 0
		for _, e := range events1 {
			if gstr(e, "type") == "oracle_started" && gstr(e, "fingerprint") == fp {
				consults++
			}
		}
		eq(t, consults, 1, "no second consult for the same fingerprint")
		followup := `{"type":"oracle_followup_attempted","goalId":"journey-actionable","fingerprint":"` + fp + `","adviceId":"adv-A","firstToolName":"bash","at":"t3"}`
		events2 := append(append([]*jsObject{}, events1...), jo(t, followup))
		eq(t, oracleStateForFingerprint(events2, gstr(g, "id"), fp).followupAttempted, true, "followup attempted")
		blocked := `{"type":"goal_blocked","goalId":"journey-actionable","reason":"Migration fails even after draining the pool","source":"agent","at":"t4"}`
		path := filepath.Join(dir, ".pi", "goals", "goal_events.jsonl")
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(strings.Join([]string{started, result, followup, blocked}, "\n")+"\n"), 0o644)
		data, _ := os.ReadFile(path)
		eq(t, strings.Contains(string(data), `"type":"goal_blocked"`), true, "blocked persisted")
		eq(t, strings.Contains(string(data), `"type":"oracle_followup_attempted"`), true, "followup persisted")
	})
	tw(t, "goal-oracle", "demonstrates the needs-human journey", func(t *testing.T) {
		g := createGoal("Journey: needs human.", true, false, false, ms(2026, 8, 23, 9, 30, 0))
		g.set("id", "journey-human")
		fp := buildBlockerFingerprint(g, "Need prod credentials I cannot access")
		events := oracleEvents(t,
			`{"type":"oracle_started","goalId":"journey-human","fingerprint":"`+fp+`","provider":"p","model":"m","reason":"Need prod credentials I cannot access","at":"t1"}`,
			`{"type":"oracle_result","goalId":"journey-human","fingerprint":"`+fp+`","adviceId":"adv-H","disposition":"needs_human","summary":"Requires credentials only the user can provide.","at":"t2"}`,
		)
		eq(t, oracleStateForFingerprint(events, "journey-human", fp).result.disposition, "needs_human", "disposition")
	})
}
