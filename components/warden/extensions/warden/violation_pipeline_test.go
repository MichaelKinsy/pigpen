package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scoreJudge is violation-pipeline.test.ts `fakeJudge`: a noul question scores `scores[id]` (should_proceed 1, else
// 0.01), a choice answers expected_step.
func scoreJudge(scores map[string]float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		answers := map[string]Answer{}
		for key, q := range r.Questions {
			switch q.Type {
			case "noul":
				v, ok := scores[key]
				if !ok {
					v = 0.01
					if key == "should_proceed" {
						v = 1
					}
				}
				answers[key] = noul(v)
			case "choice":
				answers[key] = Answer{Type: "choice", Choice: "expected_step", Confidence: 1}
			}
		}
		return Evaluation{Model: "offline-test", ElapsedMs: 1, Answers: answers}, nil
	}}
}

func levelCfg() ActionConfig {
	c := cfg()
	c.Floor = "level"
	return c
}

// twin: tests/violation-pipeline.test.ts:35 "authorization does not clear a destructive hold for an unrelated target or a noun"
func TestAuthorizationDoesNotClearADestructiveHold(t *testing.T) {
	for _, c := range [][2]string{
		{"delete scratch.txt", "rm -rf ../protected"},
		{"Adjust the display presets", "git reset --hard HEAD~1"},
		{"force push the feature branch", "git push --force origin main"},
		{"Explain how to force push: git push --force origin main", "git push --force origin main"},
		{"Do not, under any circumstances or at any point during this task, force push: git push --force origin main", "git push --force origin main"},
	} {
		action := ActionInput{Tool: "bash", Input: map[string]any{"command": c[1]}, Task: c[0]}
		if v := eval(action, EvaluateOptions{Config: cfg()}); v.Level != LevelConfirm {
			t.Fatalf("%q offline: %s", c, v.Level)
		}
		if v := eval(action, EvaluateOptions{Config: levelCfg(), Judge: scoreJudge(nil)}); v.Level != LevelConfirm {
			t.Fatalf("%q judged: %s", c, v.Level)
		}
	}
}

// twin: tests/violation-pipeline.test.ts:49 "non-rm bash violations fire in the pattern loop regardless of authorization"
func TestNonRmViolationsFireRegardlessOfAuthorization(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin feature"}, Task: "force push"}, EvaluateOptions{Config: cfg()})
	if v.Level != LevelConfirm {
		t.Fatal(v.Level)
	}
}

// twin: tests/violation-pipeline.test.ts:55 "file-scope authorization requires every complete path, not a shared basename or substring"
func TestFileScopeAuthorizationNeedsEveryCompletePath(t *testing.T) {
	cases := []struct {
		prompt string
		paths  []string
		want   bool
	}{
		{"delete reports", []string{"eval/reports/"}, false},
		{"delete eval/reports/", []string{"eval/reports/"}, true},
		{"delete eval/reports-old", []string{"eval/reports"}, false},
		{"delete a.txt", []string{"a.txt", "b.txt"}, false},
		{"delete a.txt and b.txt", []string{"a.txt", "b.txt"}, true},
		{"delete report.ts", []string{"report.js"}, false},
	}
	for _, c := range cases {
		if scopeMatches(c.prompt, ViolationScope{Paths: c.paths}) != c.want {
			t.Errorf("%q %v", c.prompt, c.paths)
		}
	}
	v := Violation{ID: "rm-rf", Severity: SeverityRisky, Source: SourcePattern, Description: "recursive removal", Scope: ViolationScope{Paths: []string{"tmp"}}}
	if !authorize("Explain how to remove tmp", v).Authorized {
		t.Error("target named in prompt")
	}
	if authorize("Remove tmp?", v).Authorized {
		t.Error("question punctuation prevents exact path match")
	}
	if authorize("Delete tmp, but do not remove anything", v).Authorized {
		t.Error("negation prevents authorization")
	}
}

// twin: tests/violation-pipeline.test.ts:68 "violation questions use instance keys and real answers control escalation"
func TestViolationQuestionsUseInstanceKeys(t *testing.T) {
	judge := scoreJudge(map[string]float64{"violation_0": 0.99})
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf /tmp/test"}, Task: "delete test"}, EvaluateOptions{Config: cfg(), Judge: judge})
	if !has(judge.calls()[0].Questions, "violation_0") {
		t.Fatal("no violation_0 question")
	}
	if v.Extra["violation_0"] != 0.99 {
		t.Fatalf("%v", v.Extra)
	}
	if v.Level != LevelDeny {
		t.Fatalf("%s", v.Level)
	}
}

// twin: tests/violation-pipeline.test.ts:76 "duplicate violation IDs receive separate questions and scoped state"
func TestDuplicateViolationIDsGetSeparateQuestions(t *testing.T) {
	var vs []Violation
	for _, p := range []string{"one.txt", "two.txt"} {
		vs = append(vs, Violation{ID: "rm-rf", Severity: SeverityRisky, Source: SourcePattern, Description: "recursive removal", Scope: ViolationScope{Paths: []string{p}}})
	}
	request := BuildRequest(ActionSummary{Tool: "bash", Command: "fixture"}, "review", BuildExtras{Violations: vs})
	var keys []string
	for k := range request.Questions {
		if strings.HasPrefix(k, "violation_") {
			keys = append(keys, k)
		}
	}
	if len(keys) != 2 || !has(request.Questions, "violation_0") || !has(request.Questions, "violation_1") {
		t.Fatalf("%v", keys)
	}
	mustMatch(t, request.Questions["violation_0"].Instructions, `Violation #1`, "first")
	mustMatch(t, request.Questions["violation_1"].Instructions, `Violation #2`, "second")
}

// twin: tests/violation-pipeline.test.ts:85 "sensitive paths remain advisory and read-only suppression survives aggregation"
func TestSensitivePathsRemainAdvisory(t *testing.T) {
	read := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "cat .env"}, Task: "inspect the fixture"}, EvaluateOptions{Config: levelCfg(), Judge: scoreJudge(map[string]float64{"mutates": 0, "violation_0": 0.99})})
	if read.Level != LevelAllow {
		t.Fatalf("read: %s", read.Level)
	}
	write := eval(ActionInput{Tool: "write", Input: map[string]any{"path": ".env", "content": "REVIEW_FIXTURE=1"}, Task: "update the fixture"}, EvaluateOptions{Config: levelCfg(), Judge: scoreJudge(map[string]float64{"violation_0": 0.99})})
	if write.Level != LevelWarn {
		t.Fatalf("write: %s", write.Level)
	}
	if aggregateLevel([]EscalatedViolation{{Violation: Violation{ID: "sensitive-path", Severity: SeveritySensitive, Source: SourcePattern, Description: "credential file"}, EscalatedSeverity: SeveritySensitive}}) != LevelWarn {
		t.Fatal("aggregate")
	}
}

// twin: tests/violation-pipeline.test.ts:94 "sensitive path warns in evidence mode via deferral, not the floor"
func TestSensitivePathIsEvidenceWhenTheJudgeAnswers(t *testing.T) {
	write := eval(ActionInput{Tool: "write", Input: map[string]any{"path": ".env", "content": "REVIEW_FIXTURE=1"}, Task: "update the fixture"}, EvaluateOptions{Config: cfg(), Judge: scoreJudge(map[string]float64{"violation_0": 0.99})})
	if write.Level != LevelAllow {
		t.Fatalf("%s", write.Level)
	}
}

// twin: tests/violation-pipeline.test.ts:100 "older action sections receive the escalation default before making a decision"
// The default itself is applied by the config loader (completeConfig upstream); this pins the value it must apply and
// the decision it leads to.
func TestEscalationDefaultDecidesAtPointSixWarn(t *testing.T) {
	config := levelCfg()
	if config.EscalationThreshold != 0.85 {
		t.Fatalf("default %v", config.EscalationThreshold)
	}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf build"}, Task: "inspect output"}, EvaluateOptions{Config: config, Judge: scoreJudge(map[string]float64{"violation_0": 0.6})})
	if v.Level != LevelWarn {
		t.Fatalf("%s", v.Level)
	}
}

// twin: tests/violation-pipeline.test.ts:108 "unreadable rule candidates are skipped without crashing the hook"
func TestUnreadableRuleCandidatesAreSkipped(t *testing.T) {
	directory := filepath.Join(cwd, "bad-rules")
	if err := os.MkdirAll(filepath.Join(directory, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	harmless := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Cwd: directory, Task: "run tests"}, EvaluateOptions{Config: cfg(), Judge: scoreJudge(nil)})
	if harmless.Level != LevelAllow {
		t.Fatal(harmless.Level)
	}
	push := map[string]any{"command": "git push --force origin main"}
	if v := eval(ActionInput{Tool: "bash", Input: push, Cwd: directory, Task: "run tests"}, EvaluateOptions{Config: cfg(), Judge: scoreJudge(nil)}); v.Level != LevelAllow {
		t.Fatalf("evidence mode: a built-in hit does not set the level when the judge answers: %s", v.Level)
	}
	if v := eval(ActionInput{Tool: "bash", Input: push, Cwd: directory, Task: "run tests"}, EvaluateOptions{Config: levelCfg(), Judge: scoreJudge(nil)}); v.Level != LevelConfirm {
		t.Fatalf("level mode: %s", v.Level)
	}
}

const resetCmd = "git reset --hard HEAD~1"

// twin: tests/violation-pipeline.test.ts:136 "(a) evidence mode: destructive hit, judge present, Jev 0.18 → allow with evidence note"
func TestEvidenceModeAllowsALowIrreversibleScore(t *testing.T) {
	j := scoreJudge(map[string]float64{"irreversible": 0.18})
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": resetCmd}, Task: "undo last commit"}, EvaluateOptions{Config: cfg(), Judge: j})
	if v.Level != LevelAllow || !strings.Contains(reasonsJoined(v), "evidence") {
		t.Fatalf("%s %v", v.Level, v.Reasons)
	}
	if _, ok := j.calls()[0].State["floor_hits"].(string); !ok {
		t.Fatal("floor_hits missing")
	}
}

// twin: tests/violation-pipeline.test.ts:146 "(b) level mode: destructive hit, no judge → confirm as today"
func TestLevelModeConfirmsWithoutAJudge(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": resetCmd}, Task: "undo last commit"}, EvaluateOptions{Config: levelCfg()})
	if v.Level != LevelConfirm {
		t.Fatal(v.Level)
	}
}

// twin: tests/violation-pipeline.test.ts:152 "(c) evidence mode: destructive hit, judge present, Jev 0.92 → confirm via irreversible"
func TestEvidenceModeConfirmsAHighIrreversibleScore(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": resetCmd}, Task: "undo last commit"}, EvaluateOptions{Config: cfg(), Judge: scoreJudge(map[string]float64{"irreversible": 0.92})})
	if v.Level != LevelConfirm {
		t.Fatal(v.Level)
	}
	mustMatch(t, reasonsJoined(v), `irreversible 0\.92`, "reason")
}

// twin: tests/violation-pipeline.test.ts:160 "(d) user command rule severity destructive, judge present, Jev 0.1 → confirm (user rule wins)"
func TestAUserCommandRuleWinsOverALowScore(t *testing.T) {
	config := cfg()
	config.Floor = "evidence"
	config.CommandRules = []CommandRule{{ID: "my-deploy", Pattern: "deploy", Severity: "confirm"}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "deploy production"}, Task: "ship it"}, EvaluateOptions{Config: config, Judge: scoreJudge(map[string]float64{"irreversible": 0.1})})
	if v.Level != LevelConfirm || !strings.Contains(reasonsJoined(v), "my-deploy") {
		t.Fatalf("%s %v", v.Level, v.Reasons)
	}
}

// twin: tests/violation-pipeline.test.ts:172 "(e) evidence mode: outside-project existing write, judge present, Jev 0.2 → allow with evidence note"
func TestEvidenceModeAllowsAnOutsideProjectOverwrite(t *testing.T) {
	path := filepath.Join(os.TempDir(), "test-file.ts")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": path, "content": "x"}, Task: "write to tmp"}, EvaluateOptions{Config: cfg(), Judge: scoreJudge(map[string]float64{"irreversible": 0.2})})
	if v.Level != LevelAllow {
		t.Fatal(v.Level)
	}
	mustMatch(t, reasonsJoined(v), `outside project.*evidence`, "outside-project evidence note")
}

// twin: tests/violation-pipeline.test.ts:180 "(f) floor:level restores (a) to confirm"
func TestFloorLevelRestoresConfirm(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": resetCmd}, Task: "undo last commit"}, EvaluateOptions{Config: levelCfg(), Judge: scoreJudge(map[string]float64{"irreversible": 0.18})})
	if v.Level != LevelConfirm {
		t.Fatal(v.Level)
	}
}

// twin: tests/violation-pipeline.test.ts:187 "(h) judge failure in evidence mode falls back to floor for built-in hits"
func TestJudgeFailureInEvidenceModeFallsBackToTheFloor(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin main"}, Task: "push"}, EvaluateOptions{Config: cfg(), Judge: failingJudge("timeout")})
	if v.Level != LevelConfirm {
		t.Fatal(v.Level)
	}
	mustMatch(t, reasonsJoined(v), `built-in patterns decide`, "reason")
}

// twin: tests/violation-pipeline.test.ts:201 "(i) built-in ID coverage: every SHELL_RULES, rm classifier, and sensitive-path id is in BUILT_IN_IDS"
func TestEveryBuiltInIDIsBuiltIn(t *testing.T) {
	var ids []string
	for _, r := range shellRules {
		ids = append(ids, r.ID)
	}
	ids = append(ids, "rm-recursive", "rm-rf", "rm-recursive-dangerous-target", "sensitive-path")
	for _, id := range ids {
		if !builtInIDs[id] {
			t.Errorf("%s must be built in", id)
		}
	}
	// The ids the scratch, SQL-target and path-rule passes add are exemptable too.
	for _, id := range []string{"rm-session-scratch", "sql-delete-local", "sql-hosted", "sql-hosted-write"} {
		if !builtInIDs[id] {
			t.Errorf("%s must be built in", id)
		}
	}
}

// twin: tests/violation-pipeline.test.ts:210 "(j) deferred sensitive hit warns on judge failure in evidence mode"
func TestDeferredSensitiveHitWarnsOnJudgeFailure(t *testing.T) {
	bash := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "cat .env"}, Task: "inspect env"}, EvaluateOptions{Config: cfg(), Judge: failingJudge("timeout")})
	if bash.Level != LevelWarn {
		t.Fatalf("bash %s", bash.Level)
	}
	mustMatch(t, reasonsJoined(bash), `built-in patterns decide`, "reason")
	write := eval(ActionInput{Tool: "write", Input: map[string]any{"path": ".env", "content": "x"}, Task: "inspect env"}, EvaluateOptions{Config: cfg(), Judge: failingJudge("timeout")})
	if write.Level != LevelWarn {
		t.Fatalf("write %s", write.Level)
	}
}

// The violation question text is pinned to the original's (testdata/violation_questions.json is dumped from it by
// port/gen-questions.ts); so is the regret locator.
func TestViolationAndRegretQuestionTextsEqualTheOriginals(t *testing.T) {
	data, err := os.ReadFile("testdata/violation_questions.json")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Violations map[string]rawQuestion `json:"violations"`
		Regret     map[string]rawQuestion `json:"regret"`
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	vs := []Violation{
		{ID: "rm-rf", Severity: SeverityRisky, Source: SourcePattern, Description: "recursive removal"},
		{ID: "rm-rf", Severity: SeverityDestructive, Source: SourceRulesGuard, Description: "no console", MatchedRule: "No console.log"},
	}
	got := violationJudgmentQuestions(vs)
	for key, raw := range want.Violations {
		mustEqual(t, got[key], raw.build(), key)
	}
	regret := regretQuestions([]PreviousAction{{ID: "a", Tool: "bash", Command: "rm x"}, {ID: "b", Tool: "write", Path: "p.txt"}, {ID: "c", Tool: "edit"}})
	for key, raw := range want.Regret {
		mustEqual(t, regret[key], raw.build(), key)
	}
}
