package warden

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func eval(action ActionInput, opts EvaluateOptions) Verdict {
	if action.Cwd == "" {
		action.Cwd = cwd
	}
	return EvaluateAction(context.Background(), action, opts)
}

func cfg() ActionConfig { return DefaultConfig().Action }

func reasonsJoined(v Verdict) string { return strings.Join(v.Reasons, "; ") }

func indexOfReasonPrefix(v Verdict, prefix string) int {
	for i, r := range v.Reasons {
		if strings.HasPrefix(r, prefix) {
			return i
		}
	}
	return -1
}

// twin: tests/guard.test.ts:39 "should-proceed keeps the trace reason at the threshold and leaves higher scores alone"
func TestShouldProceedKeepsTheTraceReasonAtTheThreshold(t *testing.T) {
	for _, score := range []float64{0.6, 0.61} {
		result := actionAnswers(0.1, 0.1, "expected_step", nil)
		result.Answers["should_proceed"] = noul(score)
		judge := &recordingJudge{handler: func(Request) (Evaluation, error) { return result, nil }}
		v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": "example.ts", "content": "export {};"}, Task: "add a module"}, EvaluateOptions{Config: cfg(), Judge: judge})
		if v.Judgment == nil || v.Judgment.ShouldProceed == nil || *v.Judgment.ShouldProceed != score {
			t.Fatalf("score %v: %+v", score, v.Judgment)
		}
		atThreshold := score == 0.6
		if v.ShouldProceedTraceOnly != atThreshold || v.ShouldProceedSteer != atThreshold {
			t.Fatalf("score %v: traceOnly=%v steer=%v", score, v.ShouldProceedTraceOnly, v.ShouldProceedSteer)
		}
		if atThreshold && v.Reasons[v.ShouldProceedTraceOnlyReasonIndex] != "should-proceed 0.60 (trace-only until calibrated)" {
			t.Fatalf("reason %q", v.Reasons[v.ShouldProceedTraceOnlyReasonIndex])
		}
	}
}

// twin: tests/guard.test.ts:53 "off-task never holds: an unrelated change warns but is trace-only; a read-only command only warns"
func TestOffTaskNeverHolds(t *testing.T) {
	config := cfg()
	inspect := ActionInput{Tool: "bash", Input: map[string]any{"command": "cat package.json; node -e \"console.log(require('./package.json').version)\""}, Task: "Update the README image"}
	readOnly := eval(inspect, EvaluateOptions{Config: config, Judge: actionJudge(0.05, 0.91, "unrelated", f(0.05))})
	if readOnly.Level != LevelWarn {
		t.Fatalf("read-only level %s", readOnly.Level)
	}
	mustMatch(t, reasonsJoined(readOnly), `unrelated, but read-only`, "read-only reason")
	if readOnly.OffTaskSteer {
		t.Error("nothing changed, nothing to steer back from")
	}
	if !readOnly.OffTaskTraceOnly {
		t.Error("off-task is trace-only until AUC clears 0.51")
	}
	installInput := map[string]any{"command": "npm install left-pad"}
	changes := eval(ActionInput{Tool: "bash", Input: installInput, Task: inspect.Task}, EvaluateOptions{Config: config, Judge: actionJudge(0.2, 0.91, "unrelated", f(0.95))})
	if changes.Level != LevelWarn || !changes.OffTaskSteer || !changes.OffTaskTraceOnly {
		t.Fatalf("changes: %+v", changes)
	}
	mustMatch(t, reasonsJoined(changes), `off-task 0\.91 \(unrelated to the request; trace-only until AUC clears 0\.51\)`, "changes reason")
	if changes.OffTaskTraceOnlyReasonIndex != indexOfReasonPrefix(changes, "off-task 0.91") {
		t.Errorf("delivery metadata identifies only the generated diagnostic: %d", changes.OffTaskTraceOnlyReasonIndex)
	}
	mustMatch(t, OffTaskSteer(changes), `^pi-warden: this bash call looks unrelated to the user's request \(off-task 0\.91\)\. It ran\.`, "steer text")
	mustMatch(t, FormatVerdict(changes), `off task · warn$`, "widget line")
	unknown := eval(inspect, EvaluateOptions{Config: config, Judge: actionJudge(0.05, 0.91, "unrelated", nil)})
	if !unknown.OffTaskSteer || !unknown.OffTaskTraceOnly {
		t.Error("without a mutates answer the call is taken to change something")
	}
	write := eval(ActionInput{Tool: "write", Input: map[string]any{"path": "poem.txt", "content": "roses"}, Task: "Fix the login bug"}, EvaluateOptions{Config: config, Judge: actionJudge(0.05, 0.95, "unrelated", f(0.05))})
	if write.Level != LevelWarn || !write.OffTaskSteer || !write.OffTaskTraceOnly {
		t.Errorf("write and edit always change something, and still never hold for scope alone: %+v", write)
	}
	strict := config
	strict.OffTask = OffTaskThreshold{Warn: 0.6, Steer: 0.95}
	below := eval(ActionInput{Tool: "bash", Input: installInput, Task: inspect.Task}, EvaluateOptions{Config: strict, Judge: actionJudge(0.2, 0.91, "unrelated", f(0.95))})
	if !below.OffTaskSteer || below.Level != LevelWarn {
		t.Errorf("scope unrelated steers regardless of score threshold: %+v", below)
	}
	probe := actionJudge(0.05, 0.05, "expected_step", f(0.05))
	eval(inspect, EvaluateOptions{Config: config, Judge: probe})
	if !has(nth(t, probe, 0).Questions, "mutates") {
		t.Error("the question is part of the single action request")
	}
}

// twin: tests/guard.test.ts:84
func TestMissingScopeContextIsNotItselfOffTaskEvidence(t *testing.T) {
	action := ActionInput{Tool: "write", Input: map[string]any{"path": "src/output.ts", "content": "export const output = 1;"}, Task: "Nice, the guard works :)"}
	unclear := eval(action, EvaluateOptions{Config: cfg(), Judge: actionJudge(0.1, 0.95, "unclear", nil)})
	if unclear.Level != LevelAllow || unclear.OffTaskSteer {
		t.Errorf("unclear: %+v", unclear)
	}
	unrelated := eval(action, EvaluateOptions{Config: cfg(), Judge: actionJudge(0.1, 0.95, "unrelated", nil)})
	if unrelated.Level != LevelWarn || !unrelated.OffTaskSteer || !unrelated.OffTaskTraceOnly {
		t.Errorf("unrelated: %+v", unrelated)
	}
	expected := eval(action, EvaluateOptions{Config: cfg(), Judge: actionJudge(0.1, 0.95, "expected_step", nil)})
	if expected.Level != LevelAllow || expected.OffTaskSteer {
		t.Errorf("scope expected_step vetoes off-task even with a high score: %+v", expected)
	}
	destructive := eval(action, EvaluateOptions{Config: cfg(), Judge: actionJudge(0.95, 0.95, "unclear", nil)})
	if destructive.Level != LevelConfirm {
		t.Errorf("missing context does not disable irreversible-action protection: %s", destructive.Level)
	}
}

// twin: tests/guard.test.ts:631
func TestEvaluateActionAllowsReadOnlyCommandsWithoutConsultingTheJudge(t *testing.T) {
	j := actionJudge(0.9, 0.9, "", nil)
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git status"}, Task: "push my branch"}, EvaluateOptions{Config: cfg(), Judge: j})
	if v.Level != LevelAllow || v.Source != "read-only" || len(j.calls()) != 0 {
		t.Errorf("%+v calls=%d", v, len(j.calls()))
	}
}

// twin: tests/guard.test.ts:639
func TestEvaluateActionEscalatesDestructivePatternsToConfirmEvenBeforeTheJudgeAnswers(t *testing.T) {
	j := actionJudge(0.1, 0.1, "", nil)
	config := cfg()
	config.Floor = "level"
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin main"}, Task: "push my branch"}, EvaluateOptions{Config: config, Judge: j})
	if v.Level != LevelConfirm {
		t.Fatalf("level %s", v.Level)
	}
	if _, ok := hitByID(v.Patterns, "git-force-push"); !ok {
		t.Error("no git-force-push hit")
	}
	if len(j.calls()) != 1 || v.Judgment == nil {
		t.Error("the judge still runs so the widget can show the off-task judgment")
	}
}

// twin: tests/guard.test.ts:648
func TestEvaluateActionInEvidenceModeFeedsBuiltInHitsToTheJudge(t *testing.T) {
	j := actionJudge(0.1, 0.1, "", nil)
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin main"}, Task: "push my branch"}, EvaluateOptions{Config: cfg(), Judge: j})
	if v.Level != LevelAllow {
		t.Errorf("built-in hit is evidence, not a level-setter: %s", v.Level)
	}
	if _, ok := hitByID(v.Patterns, "git-force-push"); !ok {
		t.Error("pattern still recorded for trace")
	}
	hits, ok := nth(t, j, 0).State["floor_hits"].(string)
	if !ok || !strings.Contains(hits, "git force push") {
		t.Errorf("floor_hits: %v", nth(t, j, 0).State["floor_hits"])
	}
}

// twin: tests/guard.test.ts:658
func TestEvaluateActionSendsNamedStateFieldsAndTheBaseQuestions(t *testing.T) {
	j := actionJudge(0.2, 0.1, "", nil)
	eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "Run the tests and fix failures"}, EvaluateOptions{Config: cfg(), Judge: j})
	request := nth(t, j, 0)
	ids := []string{}
	for id := range request.Questions {
		ids = append(ids, id)
	}
	mustEqual(t, sortedStrings(ids), []string{"irreversible", "mutates", "off_task", "scope", "should_proceed", "visible"}, "question ids")
	eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(cwd, "a.ts"), "content": "x"}, Task: "t"}, EvaluateOptions{Config: cfg(), Judge: j})
	if has(nth(t, j, 1).Questions, "visible") {
		t.Error("a write is never visible outside the working tree")
	}
	if request.Questions["irreversible"].Type != "noul" || request.Questions["scope"].Type != "choice" {
		t.Error("question types")
	}
	if request.State["task"] != "Run the tests and fix failures" {
		t.Errorf("task %v", request.State["task"])
	}
	mustEqual(t, request.State["action"], ActionSummary{Tool: "bash", Command: "npm test"}, "action")
}

// twin: tests/guard.test.ts:671
func TestEvaluateActionAppliesThresholdsFromConfig(t *testing.T) {
	config := cfg()
	run := func(irreversible, offTask float64) Verdict {
		return eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm run migrate"}, Task: "add a column"}, EvaluateOptions{Config: config, Judge: actionJudge(irreversible, offTask, "", nil)})
	}
	if v := run(0.55, 0.1); v.Level != LevelWarn {
		t.Errorf("0.55: %s", v.Level)
	}
	// The 0.5 to 0.9 band warns: the default confirm is 0.9, so a judge-only 0.8 does not hold.
	if v := run(0.8, 0.1); v.Level != LevelWarn {
		t.Errorf("0.8: %s", v.Level)
	}
	if v := run(0.92, 0.1); v.Level != LevelConfirm {
		t.Errorf("0.92: %s", v.Level)
	}
	if v := run(0.2, 0.2); v.Level != LevelAllow || v.Source != "typesafe" {
		t.Errorf("0.2: %s %s", v.Level, v.Source)
	}
}

// twin: tests/guard.test.ts:685
func TestEvaluateActionGatesOffTaskOnScope(t *testing.T) {
	config := cfg()
	write := func(offTask float64, scope string) Verdict {
		return eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(cwd, "notes.md"), "content": "x"}, Task: "fix login"}, EvaluateOptions{Config: config, Judge: actionJudge(0.1, offTask, scope, nil)})
	}
	side := write(0.7, "plausible_side_step")
	if side.Level != LevelWarn || side.OffTaskSteer || !side.OffTaskTraceOnly {
		t.Errorf("side: %+v", side)
	}
	unrelated := write(0.9, "unrelated")
	if unrelated.Level != LevelWarn || !unrelated.OffTaskSteer || !unrelated.OffTaskTraceOnly {
		t.Errorf("unrelated: %+v", unrelated)
	}
	if !hasReason(unrelated, `(?i)off-task`) {
		t.Error("no off-task reason")
	}
	expected := write(0.9, "expected_step")
	if expected.Level != LevelAllow || expected.OffTaskSteer || expected.OffTaskTraceOnly {
		t.Errorf("expected: %+v", expected)
	}
}

func hasReason(v Verdict, pattern string) bool {
	for _, r := range v.Reasons {
		if regexpMatch(pattern, r) {
			return true
		}
	}
	return false
}

// twin: tests/guard.test.ts:702
func TestEvaluateActionWithoutAJudgeRunsPatternChecksOnly(t *testing.T) {
	quiet := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "test"}, EvaluateOptions{Config: cfg()})
	if quiet.Level != LevelAllow || quiet.Source != "pattern" {
		t.Errorf("quiet: %+v", quiet)
	}
	risky := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf dist"}, Task: "test"}, EvaluateOptions{Config: cfg()})
	if risky.Level != LevelWarn {
		t.Errorf("risky: %s", risky.Level)
	}
	loud := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git reset --hard"}, Task: "test"}, EvaluateOptions{Config: cfg(), Git: func(string, ...string) (string, bool) { return " M x", true }})
	if loud.Level != LevelConfirm || loud.Judgment != nil {
		t.Errorf("loud: %+v", loud)
	}
}

// twin: tests/guard.test.ts:713
func TestEvaluateActionFailsOpenByDefaultAndFailsClosedWhenConfigured(t *testing.T) {
	open := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "test"}, EvaluateOptions{Config: cfg(), Judge: failingJudge("timeout")})
	if open.Level != LevelAllow || open.Source != "error" {
		t.Errorf("open: %+v", open)
	}
	mustMatch(t, open.Error, `synthetic timeout`, "error text")
	config := cfg()
	config.FailOpen = false
	closed := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "test"}, EvaluateOptions{Config: config, Judge: failingJudge("http")})
	if closed.Level != LevelConfirm || closed.Source != "error" {
		t.Errorf("closed: %+v", closed)
	}
}

// twin: tests/guard.test.ts:723 "evaluateAction warns on writes outside the project and confirms overwrites there"
func TestEvaluateActionWarnsOnWritesOutsideTheProjectAndConfirmsOverwritesThere(t *testing.T) {
	other := t.TempDir()
	fresh := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(other, "does-not-exist", "x.txt"), "content": "x"}, Task: "write a scratch file"}, EvaluateOptions{Config: cfg()})
	if fresh.Level != LevelWarn {
		t.Errorf("fresh: %s", fresh.Level)
	}
	target := filepath.Join(other, "overwrite-target")
	missing := eval(ActionInput{Tool: "write", Input: map[string]any{"path": target, "content": "x"}, Task: "x"}, EvaluateOptions{Config: cfg()})
	if missing.Level != LevelWarn {
		t.Errorf("a missing outside file is a warn, not a confirm: %s", missing.Level)
	}
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	clobber := eval(ActionInput{Tool: "write", Input: map[string]any{"path": target, "content": "x"}, Task: "x"}, EvaluateOptions{Config: cfg()})
	if clobber.Level != LevelConfirm {
		t.Errorf("clobber: %s", clobber.Level)
	}
}

// twin: tests/guard.test.ts:837 "evaluateAction: a prompt never authorizes a recursive rm of a root-like target"
func TestAPromptNeverAuthorizesARecursiveRmOfARootLikeTarget(t *testing.T) {
	held := [][2]string{
		{"Clean up build/ please.", "rm -rf /"}, {"Clean up build/ please.", "rm -rf ."}, {"Clean up build/ please.", "rm -rf ./"}, {"fix the failing test", "rm -rf /"},
		{"delete the .. directory", "rm -rf .."}, {"remove ~ caches", "rm -rf ~"}, {"delete * now", "rm -rf *"}, {"remove $HOME tmp", "rm -rf $HOME"},
		{"delete everything under / and .", "rm -rf / ."},
	}
	for _, c := range held {
		v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": c[1]}, Task: c[0]}, EvaluateOptions{Config: cfg()})
		if v.Level != LevelConfirm {
			t.Errorf("%s | %s: %s", c[0], c[1], v.Level)
		}
	}
	for _, c := range [][2]string{{"Clean up build/ please.", "rm -rf ./*"}, {"delete build/x", "rm -rf x"}, {"delete old/build", "rm -rf build"}, {"delete build.gradle", "rm -rf build"}} {
		v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": c[1]}, Task: c[0]}, EvaluateOptions{Config: cfg()})
		if v.Level == LevelAllow {
			t.Errorf("%s | %s: allowed", c[0], c[1])
		}
	}
	for _, c := range [][2]string{{"delete build/", "rm -rf build/"}, {"delete build", "rm -rf build/"}, {"Please remove `dist`.", "rm -rf dist"}, {"clean up build/, then rebuild", "rm -rf build"}} {
		v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": c[1]}, Task: c[0]}, EvaluateOptions{Config: cfg()})
		if v.Level != LevelAllow {
			t.Errorf("%s | %s: %s", c[0], c[1], v.Level)
		}
	}
}

// twin: tests/guard.test.ts:876
func TestEvaluateActionSkipsToolsThatAreNotGuarded(t *testing.T) {
	j := actionJudge(0.9, 0.9, "", nil)
	v := eval(ActionInput{Tool: "read", Input: map[string]any{"path": "/etc/passwd"}, Task: "x"}, EvaluateOptions{Config: cfg(), Judge: j})
	if v.Level != LevelAllow || v.Source != "skipped" || len(j.calls()) != 0 {
		t.Errorf("%+v", v)
	}
}

// withApproval is guard.test.ts `withSlop(irreversible, offTask, {}, approved)` without the slop questions (slop guard is out of scope).
func withApproval(irreversible, offTask float64, approved float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		e := actionAnswers(irreversible, offTask, "expected_step", nil)
		if has(r.Questions, "approved") {
			e.Answers["approved"] = noul(approved)
		}
		return e, nil
	}}
}

// twin: tests/guard.test.ts:934 "a retry after a hold asks Jev about approval; an approving reply lets the call through"
func TestARetryAfterAHoldAsksJevAboutApproval(t *testing.T) {
	config := cfg()
	first := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force"}, Task: "push my branch"}, EvaluateOptions{Config: config, Judge: withApproval(0.9, 0.2, 0)})
	if first.Level != LevelConfirm || first.Judgment.Approved != nil {
		t.Fatalf("first: %+v", first)
	}
	declined := withApproval(0.9, 0.2, 0.1)
	retry := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force"}, Task: "no, just push normally"}, EvaluateOptions{Config: config, Judge: declined, RetryAfterHold: true})
	if !has(nth(t, declined, 0).Questions, "approved") {
		t.Error("the approval question was not asked")
	}
	if retry.Level != LevelConfirm || retry.ApprovedByUser {
		t.Errorf("retry: %+v", retry)
	}
	approved := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force"}, Task: "yes, force push it, I own that branch"}, EvaluateOptions{Config: config, Judge: withApproval(0.9, 0.2, 0.95), RetryAfterHold: true})
	if approved.Level != LevelAllow || !approved.ApprovedByUser {
		t.Fatalf("approved: %+v", approved)
	}
	mustMatch(t, approved.Reasons[0], `user approved in the latest message \(0\.95\)`, "reason")
	if approved.Judgment.Approved == nil || *approved.Judgment.Approved != 0.95 {
		t.Errorf("approved score %v", deref(approved.Judgment.Approved))
	}
}

func withIntent(mismatch float64, mutates float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		e := actionAnswers(0.1, 0.1, "expected_step", f(mutates))
		if has(r.Questions, "intent_mismatch") {
			e.Answers["intent_mismatch"] = noul(mismatch)
		}
		return e, nil
	}}
}

// twin: tests/guard.test.ts:988 "the agent's plan travels with the request and is judged for intent mismatch; an empty plan asks nothing"
func TestThePlanTravelsWithTheRequestAndIsJudgedForIntentMismatch(t *testing.T) {
	config := DefaultConfig()
	secretPlan := "Now I will remove the build directory. TOKEN=sk-synthetic-0123456789abcdef"
	summary := DescribeAction("bash", map[string]any{"command": "rm -rf build"}, cwd)
	request := BuildRequest(summary, "clean the build", BuildExtras{Plan: secretPlan})
	mustMatch(t, request.State["plan"].(string), `^Now I will remove the build directory\. TOKEN=\[redacted\]`, "plan redacted")
	if !has(request.Questions, "intent_mismatch") {
		t.Error("no intent_mismatch question")
	}
	if _, ok := BuildRequest(summary, "t", BuildExtras{Plan: "  \n"}).State["plan"]; ok {
		t.Error("blank plan: no field")
	}
	if has(BuildRequest(summary, "t", BuildExtras{}).Questions, "intent_mismatch") {
		t.Error("no plan: no question")
	}
	long := BuildRequest(DescribeAction("bash", map[string]any{"command": "ls"}, cwd), "t", BuildExtras{Plan: strings.Repeat("p", 900)})
	p := long.State["plan"].(string)
	if len(p) >= 560 || !strings.HasSuffix(p, "more chars]") {
		t.Errorf("plans are bounded: %d", len(p))
	}

	call := ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf build"}, Task: "clean the build", Plan: "Let me first list what is in build/ before removing anything."}
	drift := eval(call, EvaluateOptions{Config: config.Action, Judge: withIntent(0.9, 0.9)})
	if drift.Level != LevelWarn || !drift.IntentMismatch || drift.Judgment.IntentMismatch == nil || *drift.Judgment.IntentMismatch != 0.9 {
		t.Fatalf("drift: %+v", drift)
	}
	mustMatch(t, reasonsJoined(drift), `intent mismatch 0\.90 \(the call differs from the agent's stated plan; trace-only\)`, "drift reason")
	if !drift.IntentTraceOnly {
		t.Error("the default keeps every mismatch in the trace only")
	}
	if drift.Plan != "Let me first list what is in build/ before removing anything." {
		t.Errorf("plan %q", drift.Plan)
	}
	mustMatch(t, FormatVerdict(drift), `off plan · warn$`, "widget")
	mustMatch(t, IntentSteer(drift), `^pi-warden: this bash call does something different from what you said you were about to do \(intent mismatch 0\.90\)\. It ran\.`, "steer")
	mustMatch(t, IntentSteer(drift), `at most one short sentence`, "the steer bounds the demanded reply")

	none := config.Action
	none.IntentTraceOnly = "none"
	told := eval(call, EvaluateOptions{Config: none, Judge: withIntent(0.9, 0.9)})
	if !told.IntentMismatch || told.IntentTraceOnly {
		t.Errorf("none restores the steer for every mismatch: %+v", told)
	}
	mustMatch(t, reasonsJoined(told), `intent mismatch 0\.90 \(the call differs from the agent's stated plan\)`, "told reason")

	readOnly := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm run check:manifest"}, Task: "clean the build", Plan: "I will delete build/ now."}, EvaluateOptions{Config: config.Action, Judge: withIntent(0.9, 0.05)})
	if readOnly.Level != LevelAllow || readOnly.IntentMismatch || readOnly.Judgment.IntentMismatch == nil || *readOnly.Judgment.IntentMismatch != 0.9 {
		t.Errorf("a call that changes nothing is never warned about for drifting from the plan: %+v", readOnly)
	}
	inStep := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm run clean"}, Task: "clean the build", Plan: "Running the clean script now."}, EvaluateOptions{Config: config.Action, Judge: withIntent(0.05, 0.9)})
	if inStep.Level != LevelAllow || inStep.IntentMismatch {
		t.Errorf("inStep: %+v", inStep)
	}
	high := config.Action
	high.IntentMismatch = 0.95
	below := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm run clean"}, Task: "clean the build", Plan: "Let me look at build/ first."}, EvaluateOptions{Config: high, Judge: withIntent(0.9, 0.9)})
	if below.Level != LevelAllow || below.IntentMismatch {
		t.Errorf("the threshold is configurable: %+v", below)
	}
	offline := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf build"}, Task: "clean the build", Plan: "Removing build/."}, EvaluateOptions{Config: config.Action})
	if offline.Plan != "Removing build/." || offline.Judgment != nil {
		t.Errorf("pattern-only verdicts keep the plan for the trace: %+v", offline)
	}
}

func withVisible(mismatch, visible float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		e := actionAnswers(0.1, 0.1, "expected_step", f(0.9))
		if has(r.Questions, "intent_mismatch") {
			e.Answers["intent_mismatch"] = noul(mismatch)
		}
		if has(r.Questions, "visible") {
			e.Answers["visible"] = noul(visible)
		}
		return e, nil
	}}
}

// twin: tests/guard.test.ts:1045 "a visible action (commit, push, merge, launch) needs less plan mismatch to be steered than a file edit"
func TestAVisibleActionNeedsLessPlanMismatchToBeSteered(t *testing.T) {
	defaults := cfg()
	config := defaults
	config.IntentTraceOnly = "invisible"
	const plan = "I will run the tests once more before touching the PR."
	call := ActionInput{Tool: "bash", Input: map[string]any{"command": "gh pr ready 12 && git push origin feature"}, Task: "get the PR ready", Plan: plan}
	drift := eval(call, EvaluateOptions{Config: config, Judge: withVisible(0.83, 0.96)})
	if !drift.IntentMismatch {
		t.Fatal("0.83 is under the 0.9 default, but the action is visible")
	}
	if drift.Judgment.Visible == nil || *drift.Judgment.Visible != 0.96 {
		t.Errorf("visible %v", deref(drift.Judgment.Visible))
	}
	mustMatch(t, reasonsJoined(drift), `intent mismatch 0\.83 on a visible action \(0\.96; a commit, push, merge, publish, or launch the plan did not describe\)`, "visible reason")
	mustMatch(t, IntentSteer(drift), `and its effect is visible outside the working tree`, "steer")
	mustMatch(t, FormatVerdict(drift), `off plan · warn$`, "widget")
	if drift.IntentTraceOnly {
		t.Error(`under "invisible" a push and a pull request keep the steer`)
	}
	install := ActionInput{Tool: "bash", Input: map[string]any{"command": "npm install left-pad"}, Task: "get the PR ready", Plan: plan}
	judgedVisible := eval(install, EvaluateOptions{Config: config, Judge: withVisible(0.91, 0.85)})
	if !judgedVisible.IntentMismatch || judgedVisible.IntentTraceOnly {
		t.Errorf("not visible by code, but the judge scores it visible: the steer stays: %+v", judgedVisible)
	}
	judgedLocal := eval(install, EvaluateOptions{Config: config, Judge: withVisible(0.91, 0.5)})
	if !judgedLocal.IntentTraceOnly {
		t.Error("neither code nor judge finds a visible effect: trace-only")
	}
	traced := eval(call, EvaluateOptions{Config: defaults, Judge: withVisible(0.83, 0.96)})
	if !traced.IntentTraceOnly {
		t.Error("the default keeps even a visible mismatch in the trace only")
	}
	if traced.IntentTraceOnlyReasonIndex != indexOfReasonPrefix(traced, "intent mismatch") {
		t.Errorf("trace-only reason index %d", traced.IntentTraceOnlyReasonIndex)
	}
	mustMatch(t, reasonsJoined(traced), `the plan did not describe; trace-only\)`, "traced reason")
	quiet := eval(call, EvaluateOptions{Config: config, Judge: withVisible(0.83, 0.2)})
	if quiet.IntentMismatch || quiet.Level != LevelAllow {
		t.Errorf("the same mismatch on an action nobody else sees is below the bar: %+v", quiet)
	}
	low := eval(call, EvaluateOptions{Config: config, Judge: withVisible(0.7, 0.96)})
	if low.IntentMismatch {
		t.Error("visible alone is not a reason: 0.7 is under visibleMismatch")
	}
	tunedConfig := config
	tunedConfig.VisibleMismatch = 0.6
	if tuned := eval(call, EvaluateOptions{Config: tunedConfig, Judge: withVisible(0.7, 0.96)}); !tuned.IntentMismatch {
		t.Error("tuned visibleMismatch")
	}
	write := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(cwd, "a.ts"), "content": "x"}, Task: "t", Plan: "reading first"}, EvaluateOptions{Config: config, Judge: withVisible(0.83, 0.99)})
	if write.Judgment.Visible != nil || write.IntentMismatch {
		t.Errorf("writes are never asked: %+v", write)
	}
}

// twin: tests/guard.test.ts:1087
func TestTextApprovesIsAConservativeOfflineStandIn(t *testing.T) {
	for _, s := range []string{"yes", "Yes, go ahead", "do it", "ok proceed", "approved"} {
		if !TextApproves(s) {
			t.Errorf("want approves: %q", s)
		}
	}
	for _, s := range []string{"no", "don't do that", "yes but not like that, use git revert instead", "what does it do?", ""} {
		if TextApproves(s) {
			t.Errorf("want not approves: %q", s)
		}
	}
}

// twin: tests/guard.test.ts:1092
func TestSteerReasonExplainsTheHoldAndTheTwoAcceptableMoves(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin main"}, Task: "push"}, EvaluateOptions{Config: cfg(), Judge: actionJudge(0.9, 0.1, "", nil)})
	text := SteerReason(v, true)
	for _, p := range []string{`held this bash call`, `git force push`, `irreversible 0\.90`, `Do not retry it unchanged`, `tell the user`, `retry the same call and pi-warden will let it through`} {
		mustMatch(t, text, p, "steer reason")
	}
	mustNotContain(t, text, "origin main", "the command is not echoed")
	mustMatch(t, SteerReason(v, false), `once the user has replied with approval`, "canApprove=false")
}

// twin: tests/guard.test.ts:1105 "a repeated steer collapses to the one-line notice; a changed notice does not"
func TestARepeatedSteerCollapsesToTheOneLineNotice(t *testing.T) {
	w := NewSteerRepeatWindow()
	first := "pi-warden: this ctx_execute call does something different (intent mismatch 0.80). It ran."
	rescored := "pi-warden: this ctx_execute call does something different (intent mismatch 0.89). It ran."
	if w.Seen(first) {
		t.Error("the first copy is delivered in full")
	}
	if !w.Seen(rescored) {
		t.Error("only the score changed: same notice")
	}
	if w.Seen("pi-warden: the content just written to src/a.ts violates a rule") {
		t.Error("a different notice is delivered in full")
	}
	mustMatch(t, SteerFingerprint(rescored), `^pi-warden: this ctx_execute call does something different \(intent mismatch #\)\. It ran\.$`, "fingerprint")
	w.Reset()
	if w.Seen(first) {
		t.Error("a reset window delivers the full notice again")
	}
}

// twin: tests/guard.test.ts:1117 "context-mode and powershell tools are guarded through their command fields"
func TestContextModeAndPowershellToolsAreGuardedThroughTheirCommandFields(t *testing.T) {
	config := cfg()
	j := actionJudge(0.1, 0.1, "", nil)
	readOnly := eval(ActionInput{Tool: "ctx_execute", Input: map[string]any{"language": "shell", "code": "cd ~/app && git status && ls src"}, Task: "look around"}, EvaluateOptions{Config: config, Judge: j})
	if readOnly.Source != "read-only" || len(j.calls()) != 0 {
		t.Errorf("readOnly: %+v", readOnly)
	}
	destructive := eval(ActionInput{Tool: "ctx_execute", Input: map[string]any{"language": "shell", "code": "cd ~/app && git push --force origin main"}, Task: "push"}, EvaluateOptions{Config: config})
	if destructive.Level != LevelConfirm {
		t.Errorf("destructive: %s", destructive.Level)
	}
	if _, ok := hitByID(destructive.Patterns, "git-force-push"); !ok {
		t.Error("no git-force-push")
	}
	mustMatch(t, destructive.Summary.Command, `git push --force`, "command in summary")
	batch := eval(ActionInput{Tool: "ctx_batch_execute", Input: map[string]any{"commands": []any{map[string]any{"label": "status", "command": "git status"}, map[string]any{"label": "nuke", "command": "rm -rf /tmp/x"}}, "queries": []any{"q"}}, Task: "fix the bug"}, EvaluateOptions{Config: config})
	if batch.Level != LevelConfirm {
		t.Errorf("batch: %s", batch.Level)
	}
	if _, ok := hitByID(batch.Patterns, "rm-recursive-dangerous-target"); !ok {
		t.Error("batch: no rm hit")
	}
	js := eval(ActionInput{Tool: "ctx_execute", Input: map[string]any{"language": "javascript", "code": "const fs = require('fs'); console.log(fs.readdirSync('.').length)"}, Task: "count files"}, EvaluateOptions{Config: config, Judge: j})
	if js.Source != "typesafe" || len(j.calls()) != 1 {
		t.Errorf("non-shell code is judged, not shortcut as read-only: %+v", js)
	}
	mustMatch(t, js.Summary.Command, `readdirSync`, "js command")
	file := eval(ActionInput{Tool: "ctx_execute_file", Input: map[string]any{"path": "/etc/hosts", "language": "javascript", "code": "console.log(FILE_CONTENT.length)"}, Task: "size"}, EvaluateOptions{Config: config, Judge: j})
	if file.Summary.Path != "" || file.Level != LevelAllow {
		t.Errorf("ctx_execute_file reads its path; it is not a write target: %+v", file)
	}
	ps := eval(ActionInput{Tool: "powershell", Input: map[string]any{"command": `Remove-Item -Recurse -Force C:\\tmp\\x; git reset --hard`}, Task: "x"}, EvaluateOptions{Config: config, Git: func(string, ...string) (string, bool) { return " M x", true }})
	if ps.Level != LevelConfirm {
		t.Errorf("ps: %s", ps.Level)
	}
	custom := config
	custom.Tools = append(append([]string{}, config.Tools...), "mcp_something")
	unknown := eval(ActionInput{Tool: "mcp_something", Input: map[string]any{"query": "x"}, Task: "x"}, EvaluateOptions{Config: custom, Judge: j})
	if unknown.Source != "typesafe" {
		t.Errorf("unknown: %+v", unknown)
	}
	mustMatch(t, unknown.Summary.Input, `query`, "input summary")
}

// twin: tests/guard.test.ts:1158 "commandRules: severity ladder interacts with built-ins via higher()"
func TestCommandRulesSeverityLadderInteractsWithBuiltIns(t *testing.T) {
	warnRule := []CommandRule{{ID: "git-push-any", Pattern: `\bgit\s+push\b`, Severity: "warn"}}
	config := cfg()
	config.CommandRules = warnRule
	warn := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push origin feature"}, Task: "fix the bug"}, EvaluateOptions{Config: config})
	if warn.Level != LevelWarn {
		t.Fatalf("warn: %s", warn.Level)
	}
	if _, ok := hitByID(warn.Patterns, "git-push-any"); !ok {
		t.Error("user rule missing")
	}
	confirmConfig := cfg()
	confirmConfig.CommandRules = []CommandRule{{ID: "kubectl-delete", Pattern: `\bkubectl\s+delete\b`, Severity: "confirm"}}
	held := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "kubectl delete pod foo"}, Task: "cleanup"}, EvaluateOptions{Config: confirmConfig})
	if held.Level != LevelConfirm {
		t.Errorf("held: %s", held.Level)
	}
	stacked := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin main"}, Task: "push"}, EvaluateOptions{Config: config})
	if stacked.Level != LevelConfirm {
		t.Errorf("built-in destructive rule raises above the user's warn: %s", stacked.Level)
	}
	if _, ok := hitByID(stacked.Patterns, "git-force-push"); !ok {
		t.Error("built-in missing")
	}
	if _, ok := hitByID(stacked.Patterns, "git-push-any"); !ok {
		t.Error("user rule missing when stacked")
	}
}

// twin: tests/guard.test.ts:1173
func TestCommandDenyRulesDenyBlocksWithNoDialogNoJudge(t *testing.T) {
	config := cfg()
	config.CommandDenyRules = []CommandRule{{ID: "never-talos-reset", Pattern: `\btalosctl\s+reset\b`, Severity: "deny"}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "talosctl reset --nodes talos1"}, Task: "reset"}, EvaluateOptions{Config: config, Judge: actionJudge(0.05, 0.05, "", nil)})
	if v.Level != LevelDeny || v.Source != "pattern" || v.Judgment != nil {
		t.Errorf("deny skips the judge entirely: %+v", v)
	}
	if _, ok := hitByID(v.Patterns, "never-talos-reset"); !ok {
		t.Error("deny rule missing")
	}
}

// twin: tests/guard.test.ts:1214 and :1221
func TestCommandRulesConfirmDefaultsToDialogForUserRules(t *testing.T) {
	config := cfg()
	config.CommandRules = []CommandRule{{ID: "flux-suspend", Pattern: `\bflux\s+suspend\b`, Severity: "confirm", Action: "dialog"}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "flux suspend kustomization apps"}, Task: "pause"}, EvaluateOptions{Config: config, Judge: actionJudge(0.1, 0.1, "", nil)})
	if v.Level != LevelConfirm || !hasHit(v.Patterns, func(h PatternHit) bool { return h.Action == "dialog" }) {
		t.Errorf("dialog action: %+v", v)
	}
	bare := cfg()
	bare.CommandRules = []CommandRule{{ID: "helm-uninstall", Pattern: `\bhelm\s+(?:uninstall|delete)\b`, Severity: "confirm"}}
	w := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "helm uninstall my-release"}, Task: "remove"}, EvaluateOptions{Config: bare})
	if w.Level != LevelConfirm || w.Patterns[0].Action != "" {
		t.Errorf("action is unset; the extension checks default dialog behavior: %+v", w.Patterns)
	}
}

// twin: tests/guard.test.ts:1408, :1415, :1424 (the task spine in the request)
func TestBuildRequestCarriesTheTaskSpine(t *testing.T) {
	summary := DescribeAction("bash", map[string]any{"command": "npm test"}, cwd)
	spine := &TaskSpine{Goal: "add a rate limiter", Task: "now the tests", History: []string{"wire it into the app", "run the suite"}}
	request := BuildRequest(summary, "now the tests", BuildExtras{Spine: spine})
	mustEqual(t, request.State["spine"], map[string]any{"goal": "add a rate limiter", "task_history": []string{"wire it into the app", "run the suite"}}, "spine field")
	if request.State["task"] != "now the tests" {
		t.Error("task stays the latest user turn, the approval evidence")
	}
	if _, ok := BuildRequest(DescribeAction("bash", map[string]any{"command": "ls"}, cwd), "t", BuildExtras{}).State["spine"]; ok {
		t.Error("no spine, no field")
	}
	redacted := BuildRequest(DescribeAction("bash", map[string]any{"command": "ls"}, cwd), "t", BuildExtras{Spine: &TaskSpine{Goal: "use TOKEN=supersecretvalue1 to log in", Task: "t"}})
	goal := redacted.State["spine"].(map[string]any)["goal"].(string)
	mustNotContain(t, goal, "supersecretvalue1", "goal leaves redacted")

	hist := make([]string, 50)
	for i := range hist {
		hist[i] = (strings.Repeat("h", 2000))
		hist[i] = itoa(i) + hist[i][len(itoa(i)):]
	}
	capped := BuildRequest(DescribeAction("bash", map[string]any{"command": "ls"}, cwd), "t", BuildExtras{Spine: &TaskSpine{Goal: strings.Repeat("g", 5000), Task: "t", History: hist}})
	state := capped.State["spine"].(map[string]any)
	th := state["task_history"].([]string)
	if len(th) != 4 {
		t.Fatalf("history count capped at SPINE_HISTORY_TURNS: %d", len(th))
	}
	if !strings.HasPrefix(th[0], "0") {
		t.Error("the newest entries are kept")
	}
	for _, turn := range th {
		if !strings.HasSuffix(turn, "… [1250 more chars]") {
			t.Errorf("each entry truncated at SPINE_HISTORY_LIMIT: %q", turn[len(turn)-30:])
		}
	}
	if !strings.HasSuffix(state["goal"].(string), "… [3800 more chars]") {
		t.Error("goal truncated at SPINE_GOAL_LIMIT")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
