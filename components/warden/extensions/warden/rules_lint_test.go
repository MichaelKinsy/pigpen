package warden

// Twins of tests/rules-lint.test.ts (the rules check behind /warden rules check).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// lintJudge records every request and answers from `answer`. `failIf` marks requests that fail: the upstream stub fails
// by arrival index, but the Go check sends its chunks concurrently, so a request is identified by what it carries.
func lintJudge(answer func(id string, q Question) Answer, failIf func(Request) bool) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		if failIf != nil && failIf(r) {
			return Evaluation{}, os.ErrInvalid
		}
		answers := map[string]Answer{}
		for id, q := range r.Questions {
			answers[id] = answer(id, q)
		}
		return Evaluation{Model: "jev-test", ElapsedMs: 5, Answers: answers}, nil
	}}
}

func judgedAnswer(pick string) Answer {
	probs := map[string]float64{"from_change_alone": 0.05, "needs_other_files": 0.05, "needs_task_or_history": 0.05, "too_vague": 0.05}
	probs[pick] = 0.8
	return Answer{Type: "choice", Choice: pick, Confidence: 0.9, Probabilities: probs}
}

func mechanicalAnswer(v float64) Answer { return Answer{Type: "noul", Noul: v} }

// healthyAnswer is a rule the judge finds fine on both questions: the answer follows the question type, as the real API does.
func healthyAnswer(_ string, q Question) Answer {
	if q.Type == "noul" {
		return mechanicalAnswer(0.05)
	}
	return judgedAnswer("from_change_alone")
}

func lintRuleSet(markdown string, sources ...string) *RuleSet {
	if len(sources) == 0 {
		sources = []string{"pi-warden.md"}
	}
	return &RuleSet{Sources: sources, Rules: parseRules(markdown)}
}

const twoRules = "# No console statements\nCode under `src/` must not call `console.log`.\n\n# No duplicate logic\nDo not duplicate logic that exists elsewhere in the codebase."

func lintOpts(set *RuleSet, judge Judge) RulesCheckOptions {
	return RulesCheckOptions{Set: set, Judge: judge, Timeout: 1000 * 1e6}
}

func manyRules(n int) string {
	var parts []string
	for i := 1; i <= n; i++ {
		parts = append(parts, "# Rule "+itoaInt(i)+"\nKeep rule number "+itoaInt(i)+" in mind.")
	}
	return strings.Join(parts, "\n\n")
}

// twin: tests/rules-lint.test.ts:42
func TestBuildRulesCheckRequestTwoQuestionsPerRule(t *testing.T) {
	set := lintRuleSet("# No console statements\npaths: src/**\nCode must not call `console.log`.\n\n# No duplicate logic\nDo not duplicate logic that exists elsewhere.")
	built := buildRulesCheckRequest(set.Rules)
	mustEqual(t, keysOf(built.Questions), []string{"judgeable_no-console-statements", "judgeable_no-duplicate-logic", "mechanical_no-console-statements", "mechanical_no-duplicate-logic"}, "questions")
	mustEqual(t, built.NotChecked, 0, "notChecked")
	mustEqual(t, built.State, map[string]any{
		"about": rulesQuestions().checkAbout,
		"rules": []any{
			map[string]any{"id": "no-console-statements", "name": "No console statements", "paths": []any{"src/**"}, "body": "Code must not call `console.log`."},
			map[string]any{"id": "no-duplicate-logic", "name": "No duplicate logic", "paths": []any{}, "body": "Do not duplicate logic that exists elsewhere."},
		},
	}, "state")
	judgeable := built.Questions["judgeable_no-console-statements"]
	mustEqual(t, judgeable.Type, "choice", "type")
	mustEqual(t, judgeable.CriteriaOrder, []string{"from_change_alone", "needs_other_files", "needs_task_or_history", "too_vague"}, "criteria")
	mustEqual(t, built.Questions["mechanical_no-console-statements"].Type, "noul", "mechanical type")
}

func withMechanical(id string, high string) func(string, Question) Answer {
	return func(qid string, q Question) Answer {
		if q.Type == "noul" {
			if qid == "mechanical_"+id {
				return mechanicalAnswer(0.93)
			}
			return mechanicalAnswer(0.05)
		}
		if qid == "judgeable_no-duplicate-logic" {
			return judgedAnswer(high)
		}
		return judgedAnswer("from_change_alone")
	}
}

// twin: tests/rules-lint.test.ts:60
func TestCheckRulesOneAnswerPerRule(t *testing.T) {
	judge := lintJudge(withMechanical("no-console-statements", "needs_other_files"), nil)
	result := checkRules(lintOpts(lintRuleSet(twoRules), judge))
	mustEqual(t, result.Source, "typesafe", "source")
	mustEqual(t, result.Checked, 2, "checked")
	mustEqual(t, result.Requests, 1, "requests")
	mustEqual(t, result.FailedRequests, 0, "failed")
	mustEqual(t, result.Unanswered, 0, "unanswered")
	mustEqual(t, result.Results[0], RuleCheck{ID: "no-console-statements", Name: "No console statements", Judgeability: "from_change_alone", JudgeabilityScore: 0.8, Mechanical: 0.93, NeedsAttention: true}, "first")
	mustEqual(t, result.Results[1].NeedsAttention, true, "second")
	ids := []string{}
	for _, r := range result.Attention {
		ids = append(ids, r.ID)
	}
	mustEqual(t, ids, []string{"no-console-statements", "no-duplicate-logic"}, "attention")
}

// twin: tests/rules-lint.test.ts:77
func TestCheckRulesHealthyRuleAndTheMechanicalCutoff(t *testing.T) {
	judge := lintJudge(func(_ string, q Question) Answer {
		if q.Type == "noul" {
			return mechanicalAnswer(mechanicalCutoff - 0.01)
		}
		return judgedAnswer("from_change_alone")
	}, nil)
	result := checkRules(lintOpts(lintRuleSet(twoRules), judge))
	mustEqual(t, mechanicalCutoff, 0.7, "the house cutoff")
	mustEqual(t, len(result.Attention), 0, "attention")
	mustEqual(t, result.Checked, 2, "checked")
}

// twin: tests/rules-lint.test.ts:85
func TestFormatRulesCheckOneLinePerRuleThenTheSummary(t *testing.T) {
	judge := lintJudge(withMechanical("no-console-statements", "needs_other_files"), nil)
	lines := strings.Split(formatRulesCheck(checkRules(lintOpts(lintRuleSet(twoRules), judge))), "\n")
	mustEqual(t, lines[0], "Rules check: pi-warden.md — 2 rules checked in 1 request.", "header")
	mustEqual(t, lines[1], "- No console statements (no-console-statements): a linter could enforce it exactly (0.93). Fix: move it to your linter.", "first")
	mustEqual(t, lines[2], "- No duplicate logic (no-duplicate-logic): needs another file to judge (0.80). Fix: split it so the changed file alone shows the violation, or leave it to review.", "second")
	mustEqual(t, lines[3], "0 fine, 2 need attention.", "summary")
}

// twin: tests/rules-lint.test.ts:98
func TestFormatRulesCheckMechanicalAndNonJudgeableNameBothReasons(t *testing.T) {
	judge := lintJudge(func(id string, q Question) Answer {
		if q.Type == "noul" {
			return mechanicalAnswer(0.9)
		}
		if id == "judgeable_no-duplicate-logic" {
			return judgedAnswer("too_vague")
		}
		return judgedAnswer("from_change_alone")
	}, nil)
	text := formatRulesCheck(checkRules(lintOpts(lintRuleSet(twoRules), judge)))
	mustMatch(t, text, `- No duplicate logic \(no-duplicate-logic\): too vague to judge twice \(0\.80\); a linter could enforce it exactly \(0\.90\)\. Fix: move it to your linter\.`, "both reasons")
}

// twin: tests/rules-lint.test.ts:104
func TestCheckRulesNoJudgeNoRequest(t *testing.T) {
	judge := lintJudge(func(string, Question) Answer { return judgedAnswer("from_change_alone") }, nil)
	result := checkRules(lintOpts(lintRuleSet(twoRules), nil))
	mustEqual(t, result.Source, "skipped", "source")
	mustEqual(t, result.Requests, 0, "requests")
	mustEqual(t, len(judge.calls()), 0, "judge calls")
	mustMatch(t, formatRulesCheck(result), `sent nothing`, "nothing sent")
	mustMatch(t, formatRulesCheck(result), `/warden rules shows the rule set locally`, "local view")
}

// twin: tests/rules-lint.test.ts:114
func TestCheckRulesNothingToCheckIsItsOwnSource(t *testing.T) {
	judge := lintJudge(func(string, Question) Answer { return judgedAnswer("from_change_alone") }, nil)
	none := checkRules(lintOpts(nil, judge))
	mustEqual(t, none.Source, "none", "none")
	mustMatch(t, formatRulesCheck(none), `No rules file detected`, "none text")
	prose := checkRules(lintOpts(&RuleSet{Sources: []string{"NOTES.md"}, ProseOnly: true}, judge))
	mustEqual(t, prose.Source, "prose", "prose")
	mustMatch(t, formatRulesCheck(prose), `no rule-shaped sections`, "prose text")
	aggregate := checkRules(lintOpts(&RuleSet{Sources: []string{"AGENTS.md"}, Aggregate: "Some prose.", HasAggregate: true}, judge))
	mustEqual(t, aggregate.Source, "aggregate", "aggregate")
	mustMatch(t, formatRulesCheck(aggregate), `No separate rules to check: AGENTS\.md has no rule headings, so the guard judges it as one document\.`, "aggregate text")
	mustEqual(t, len(judge.calls()), 0, "an aggregate, prose-only, or missing rule set sends nothing")
}

// twin: tests/rules-lint.test.ts:128
func TestCheckRulesRuleTextIsRedactedBeforeItIsSent(t *testing.T) {
	set := lintRuleSet("# No hardcoded secrets\nA rule body may quote `api_key = \"42a7b1f9c3d5e6\"` and a token `ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789`.\n\n# Plain rule\nA rule with nothing to hide.")
	judge := lintJudge(func(string, Question) Answer { return judgedAnswer("from_change_alone") }, nil)
	checkRules(lintOpts(set, judge))
	var sent strings.Builder
	for _, r := range judge.calls() {
		b, _ := json.Marshal(r.State)
		sent.Write(b)
	}
	mustNotContain(t, sent.String(), "42a7b1f9c3d5e6", "the assigned value never leaves the machine")
	mustNotContain(t, sent.String(), "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "the token never leaves the machine")
	if !strings.Contains(sent.String(), "[redacted]") {
		t.Fatal("the rule is sent with its credential masked")
	}
}

// twin: tests/rules-lint.test.ts:138
func TestCheckRulesChunksALargeRuleSet(t *testing.T) {
	judge := lintJudge(healthyAnswer, nil)
	result := checkRules(lintOpts(lintRuleSet(manyRules(20)), judge))
	mustEqual(t, result.Requests, 2, "40 questions split into chunks of at most 32")
	calls := judge.calls()
	mustEqual(t, len(calls), 2, "judge requests")
	// The upstream stub reads the first request; the Go chunks are concurrent, so the sizes are compared as a set.
	sizes := []int{len(calls[0].Questions), len(calls[1].Questions)}
	if !(sizes[0] == 32 && sizes[1] == 8) && !(sizes[0] == 8 && sizes[1] == 32) {
		t.Fatalf("chunk sizes %v", sizes)
	}
	mustEqual(t, result.Checked, 20, "checked")
	mustEqual(t, result.NotChecked, 0, "notChecked")
}

// twin: tests/rules-lint.test.ts:149
func TestCheckRulesStatePastTheByteBudgetDropsTheTail(t *testing.T) {
	judge := lintJudge(healthyAnswer, nil)
	body := strings.Repeat("x", 600)
	var parts []string
	for i := 1; i <= 140; i++ {
		parts = append(parts, "# Rule "+itoaInt(i)+"\n"+body)
	}
	result := checkRules(lintOpts(lintRuleSet(strings.Join(parts, "\n\n")), judge))
	if result.NotChecked <= 0 {
		t.Fatal("the tail is left out")
	}
	mustEqual(t, result.Checked+result.NotChecked, 140, "every rule is checked or left out")
	mustMatch(t, formatRulesCheck(result), `left out \(the request would be too large\)`, "report")
}

// twin: tests/rules-lint.test.ts:159
func TestCheckRulesAFailedRequestIsReportedNeverThrown(t *testing.T) {
	// The upstream stub fails the second request; the Go check sends its chunks concurrently, so the tail chunk (the
	// second of the two) is the one identified by its size.
	partialJudge := lintJudge(healthyAnswer, func(r Request) bool { return len(r.Questions) < maxQuestionsPerRequest })
	half := checkRules(lintOpts(lintRuleSet(manyRules(20)), partialJudge))
	mustEqual(t, half.Source, "typesafe", "source")
	mustEqual(t, half.FailedRequests, 1, "failed")
	mustEqual(t, half.Unanswered, 4, "unanswered")
	mustMatch(t, formatRulesCheck(half), `1 request failed: TypeSafe request failed\.`, "report")
	all := checkRules(lintOpts(lintRuleSet(twoRules), lintJudge(healthyAnswer, func(Request) bool { return true })))
	mustEqual(t, all.Source, "error", "error")
	mustMatch(t, formatRulesCheck(all), `^Rules check failed after 1 request: TypeSafe request failed\.`, "error report")
}

// twin: tests/rules-lint.test.ts:172
func TestCheckRulesAnAnswerOutsideTheFourReasonsIsTheMostCautiousOne(t *testing.T) {
	judge := lintJudge(func(id string, q Question) Answer {
		if q.Type == "noul" {
			return mechanicalAnswer(0.05)
		}
		if id == "judgeable_no-console-statements" {
			return judgedAnswer("whatever")
		}
		return judgedAnswer("from_change_alone")
	}, nil)
	result := checkRules(lintOpts(lintRuleSet(twoRules), judge))
	var unknown RuleCheck
	for _, r := range result.Results {
		if r.ID == "no-console-statements" {
			unknown = r
		}
	}
	mustEqual(t, unknown.Judgeability, "too_vague", "judgeability")
	mustEqual(t, unknown.NeedsAttention, true, "needs attention")
}

// twin: tests/rules-lint.test.ts:180
func TestCheckRulesAsksAboutTheRuleSetAsTheGuardResolvedIt(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), "# Explicit return types\npaths: src/**/*.ts\nEvery exported function declares its return type.")
	judge := lintJudge(func(string, Question) Answer { return judgedAnswer("from_change_alone") }, nil)
	result := checkRules(lintOpts(NewRuleStore().Load(cwd, rulesConfig()), judge))
	mustEqual(t, result.Sources[0], "pi-warden.md", "source")
	mustEqual(t, nth(t, judge, 0).State["rules"], []any{map[string]any{"id": "explicit-return-types", "name": "Explicit return types", "paths": []any{"src/**/*.ts"}, "body": "Every exported function declares its return type."}}, "state")
}

var _ = regexp.MustCompile
