package warden

import (
	"context"
	"testing"
	"time"
)

func doneJudge(claimsDone, claimsVerified float64, outcome string, applies float64) *recordingJudge {
	return &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 9, Answers: map[string]Answer{
			"claims_done":          noul(claimsDone),
			"claims_verified":      noul(claimsVerified),
			"verification_applies": noul(applies),
			"outcome":              {Type: "choice", Choice: outcome, Confidence: 0.8},
		}}, nil
	}}
}

func evalDone(task, final string, e *RunEvidence, j Judge) DoneVerdict {
	return EvaluateDone(context.Background(), task, final, e, DoneOptions{Config: DefaultConfig().Done, Judge: j, Timeout: time.Second})
}

func record(e *RunEvidence, outcome ToolOutcome, input map[string]any) {
	RecordOutcome(e, outcome, input, "bash")
}

func passed(checks []Check) []bool {
	out := []bool{}
	for _, c := range checks {
		out = append(out, c.Passed)
	}
	return out
}

// twin: tests/stuck-done.test.ts:292
func TestClassifyToolResultSeparatesReadsMutationsAndChecks(t *testing.T) {
	cases := []struct {
		tool   string
		input  map[string]any
		failed bool
		want   ToolOutcome
		note   string
	}{
		{"read", map[string]any{"path": "a"}, false, "read", ""},
		{"write", map[string]any{"path": "a", "content": ""}, false, "mutation", ""},
		{"edit", map[string]any{"path": "a", "edits": []any{}}, false, "mutation", ""},
		{"bash", cmd("git status"), false, "read", ""},
		{"bash", cmd("npm install ajv"), false, "unknown", "shell side effects are not code changes"},
		{"bash", cmd("rm -rf /tmp/demo"), false, "unknown", ""},
		{"bash", cmd("npm test"), false, "check-pass", ""},
		{"bash", cmd("npm test"), true, "check-fail", ""},
		{"bash", cmd("npx tsc --noEmit && npm run lint"), false, "check-pass", ""},
		{"bash", cmd("cargo test"), false, "check-pass", ""},
		{"bash", cmd("pytest -q"), true, "check-fail", ""},
		{"ctx_execute", map[string]any{"language": "shell", "code": "cd app && npm test 2>&1 | tail -5"}, false, "check-pass", "context-mode shell runs count"},
		{"ctx_execute", map[string]any{"language": "javascript", "code": "console.log(require('fs').readdirSync('.'))"}, false, "unknown", "non-shell code is neither read nor check"},
		{"ctx_batch_execute", map[string]any{"commands": []any{map[string]any{"label": "t", "command": "pytest -q"}, map[string]any{"label": "s", "command": "git status"}}}, true, "check-fail", ""},
		{"ctx_execute", map[string]any{"language": "shell", "code": "ls -la && git log -3"}, false, "read", ""},
		{"mcp_something", map[string]any{"query": "x"}, false, "unknown", ""},
	}
	for _, c := range cases {
		if got := ClassifyToolResult(c.tool, c.input, c.failed, ""); got != c.want {
			t.Errorf("%s %v failed=%v: got %q want %q %s", c.tool, c.input, c.failed, got, c.want, c.note)
		}
	}
	viaCtx := EmptyEvidence()
	RecordOutcome(viaCtx, "check-pass", map[string]any{"language": "shell", "code": "npm test"}, "ctx_execute")
	mustEqual(t, viaCtx.Checks, []Check{{Call: "npm test", Passed: true}}, "recordOutcome through ctx_execute")
}

// Summaries of test runners in a script's output (done.ts checkSummary) classify an otherwise opaque command.
func TestClassifyToolResultReadsARunnersSummaryFromScriptOutput(t *testing.T) {
	script := map[string]any{"language": "javascript", "code": "run()"}
	for _, c := range []struct {
		out  string
		want ToolOutcome
	}{
		{"ℹ tests 4\nℹ pass 4\nℹ fail 0\n", "check-pass"},
		{"ℹ tests 4\nℹ pass 3\nℹ fail 1\n", "check-fail"},
		{"Tests:       1 failed, 3 passed, 4 total\n", "check-fail"},
		{"Tests:       4 passed, 4 total\n", "check-pass"},
		{"=========== 2 failed, 1 passed in 0.31s ===========\n", "check-fail"},
		{"test result: ok. 3 passed; 0 failed\n", "check-pass"},
		{"FAIL\tgithub.com/x/y\t0.012s\n", "check-fail"},
		{"ok  \tgithub.com/x/y\t0.012s\n", "check-pass"},
		{"src/a.ts(1,1): error TS2304: Cannot find name 'x'.\n", "check-fail"},
		{"nothing to see\n", "unknown"},
	} {
		if got := ClassifyToolResult("ctx_execute", script, false, c.out); got != c.want {
			t.Errorf("%q: got %q want %q", c.out, got, c.want)
		}
	}
	if got := ClassifyToolResult("ctx_execute", script, true, "ℹ tests 4\nℹ pass 4\nℹ fail 0\n"); got != "check-fail" {
		t.Errorf("a failed call with a passing summary is a failed check: %q", got)
	}
}

// twin: tests/stuck-done.test.ts:314
func TestEvidenceGatesTheDoneCheck(t *testing.T) {
	e := EmptyEvidence()
	if NeedsDoneCheck(e) {
		t.Error("no changes, nothing to verify")
	}
	record(e, "mutation", map[string]any{"path": "a.ts"})
	if !NeedsDoneCheck(e) {
		t.Error("a change needs a check")
	}
	record(e, "check-fail", cmd("npm test"))
	if !NeedsDoneCheck(e) {
		t.Error("a failed check is not verification")
	}
	record(e, "check-pass", cmd("npm test"))
	if NeedsDoneCheck(e) {
		t.Error("a pass verifies")
	}
	mustEqual(t, passed(e.Checks), []bool{false, true}, "checks")
}

// twin: tests/stuck-done.test.ts:326
func TestOnlyChecksThatRanAfterTheLatestChangeVerifyIt(t *testing.T) {
	e := EmptyEvidence()
	record(e, "mutation", map[string]any{"path": "a.ts"})
	if !NeedsDoneCheck(e) {
		t.Error("1")
	}
	record(e, "check-pass", cmd("npm test"))
	if NeedsDoneCheck(e) {
		t.Error("2")
	}
	record(e, "mutation", map[string]any{"path": "b.ts"})
	if !NeedsDoneCheck(e) {
		t.Error("the pass predates the second change")
	}
	mustEqual(t, passed(e.Checks), []bool{true}, "history is kept")
	mustEqual(t, FreshChecks(e), []Check{}, "no check has run on the new change")

	record(e, "check-fail", cmd("npm test"))
	if !NeedsDoneCheck(e) {
		t.Error("3")
	}
	mustEqual(t, passed(FreshChecks(e)), []bool{false}, "fresh checks")
	record(e, "check-pass", cmd("npm test -- --fix"))
	if NeedsDoneCheck(e) {
		t.Error("a pass after the latest change verifies it")
	}

	many := EmptyEvidence()
	record(many, "mutation", map[string]any{})
	record(many, "mutation", map[string]any{})
	record(many, "check-pass", cmd("npm test"))
	if NeedsDoneCheck(many) {
		t.Error("several changes before one passing check are covered")
	}
	later := EmptyEvidence()
	record(later, "check-pass", cmd("npm test"))
	if NeedsDoneCheck(later) || len(FreshChecks(later)) != 1 {
		t.Error("checks with no change are not verification work; without a change every check is current")
	}
}

// twin: tests/stuck-done.test.ts:355
func TestEvaluateDoneJudgesTheMessageAgainstChecksThatCoverTheLatestChange(t *testing.T) {
	e := EmptyEvidence()
	record(e, "mutation", map[string]any{})
	record(e, "check-pass", cmd("npm test"))
	record(e, "mutation", map[string]any{})
	v := evalDone("fix the parser bug", "Fixed the parser bug.", e, doneJudge(0.9, 0.1, "complete", 0.9))
	if !v.Unverified {
		t.Fatal("the passing run predates the second change")
	}
	mustMatch(t, v.Reasons[0], `after 2 file changes with no test, build, or lint run since the last change`, "reason")
	mustMatch(t, DoneNudge(v), `Run the project's tests, build, or lint`, "nudge")
	mustMatch(t, FormatDone(v), `2 changes · 0/0 checks passed .* unverified$`, "widget")
	mustEqual(t, BuildDoneRequest("fix it", "Done.", e).State["run"], map[string]any{"file_changes": 2, "checks_run": []string{}}, "Jev is shown only the checks that cover the current code")

	record(e, "check-fail", cmd("npm test"))
	afterFailure := evalDone("fix it", "Done and all tests pass.", e, doneJudge(0.9, 0.9, "complete", 0.9))
	if !afterFailure.Unverified || afterFailure.FalseClaim {
		t.Errorf("a fresh failing check is not 'none ran': %+v", afterFailure)
	}
	mustMatch(t, afterFailure.Reasons[0], `1 failed check and no passing one`, "reason")
	mustMatch(t, DoneNudge(afterFailure), `The last check that ran failed: npm test`, "nudge")

	honest := EmptyEvidence()
	record(honest, "mutation", map[string]any{})
	record(honest, "check-pass", cmd("npm test"))
	record(honest, "mutation", map[string]any{})
	stale := evalDone("fix it", "Tests passed before my last edit; I did not rerun them.", honest, doneJudge(0.95, 0.9, "complete", 0.9))
	if !stale.Unverified || stale.FalseClaim || len(stale.Reasons) != 1 {
		t.Errorf("stale evidence is unverified, not a false claim: %+v", stale)
	}
	mustMatch(t, stale.Reasons[0], `no test, build, or lint run since the last change`, "reason")
}

// twin: tests/stuck-done.test.ts:386
func TestFinalAssistantTextTakesTheLastAssistantMessageOnlyWhenItStoppedNormallyWithText(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "fix it"},
		{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Done, all fixed."}}, "stopReason": "stop"},
		{"role": "toolResult", "content": []any{}},
	}
	if got, ok := FinalAssistantText(msgs); !ok || got != "Done, all fixed." {
		t.Errorf("got %q %v", got, ok)
	}
	if _, ok := FinalAssistantText([]map[string]any{{"role": "assistant", "content": []any{map[string]any{"type": "toolCall"}}, "stopReason": "toolUse"}}); ok {
		t.Error("toolUse")
	}
	if _, ok := FinalAssistantText([]map[string]any{{"role": "assistant", "content": "aborted", "stopReason": "aborted"}}); ok {
		t.Error("aborted")
	}
	if _, ok := FinalAssistantText([]map[string]any{{"role": "user", "content": "hi"}}); ok {
		t.Error("user only")
	}
	if got, ok := FinalAssistantText([]map[string]any{{"role": "assistant", "content": "plain string"}}); !ok || got != "plain string" {
		t.Errorf("plain string: %q", got)
	}
}

// twin: tests/stuck-done.test.ts:399
func TestEvaluateDoneFlagsUnverifiedCompletionClaimsAndFalseVerificationClaims(t *testing.T) {
	e := EmptyEvidence()
	record(e, "mutation", map[string]any{})
	record(e, "mutation", map[string]any{})

	unverified := evalDone("fix the parser bug", "Fixed the parser bug in src/parser.ts.", e, doneJudge(0.9, 0.1, "complete", 0.9))
	if !unverified.Unverified || unverified.FalseClaim {
		t.Fatalf("unverified: %+v", unverified)
	}
	mustMatch(t, unverified.Reasons[0], `reports completion \(0\.90\) after 2 file changes with no test, build, or lint run`, "reason")
	mustMatch(t, DoneNudge(unverified), `Run the project's tests, build, or lint`, "nudge")
	mustMatch(t, FormatDone(unverified), `2 changes · 0/0 checks passed .* unverified$`, "widget")

	lie := evalDone("fix it", "Fixed and all tests pass.", e, doneJudge(0.95, 0.9, "complete", 0.9))
	if !lie.FalseClaim {
		t.Fatal("false claim")
	}
	mustMatch(t, lie.Reasons[1], `claims checks passed \(0\.90\) but none ran`, "reason")
	mustMatch(t, FormatDone(lie), `false claim$`, "widget")

	if blocked := evalDone("fix it", "I could not reproduce it; which Node version do you use?", e, doneJudge(0.8, 0.0, "blocked", 0.9)); blocked.Unverified {
		t.Error("a blocker or question is not a completion claim")
	}
	if partial := evalDone("fix it", "Changed the regex; still need to handle the empty case.", e, doneJudge(0.3, 0.0, "partial", 0.9)); partial.Unverified {
		t.Error("partial")
	}
	if prose := evalDone("rewrite the README intro", "Rewrote the intro.", e, doneJudge(0.95, 0.0, "complete", 0.1)); prose.Unverified {
		t.Error("checks do not apply to prose work")
	}

	failedCheck := EmptyEvidence()
	record(failedCheck, "mutation", map[string]any{})
	record(failedCheck, "check-fail", cmd("npm test"))
	afterFailure := evalDone("fix it", "Done.", failedCheck, doneJudge(0.9, 0.1, "complete", 0.9))
	mustMatch(t, afterFailure.Reasons[0], `1 failed check and no passing one`, "reason")
	mustMatch(t, DoneNudge(afterFailure), `The last check that ran failed: npm test`, "nudge")

	errored := evalDone("fix it", "Done.", e, failingJudge("timeout"))
	if errored.Unverified {
		t.Error("errored")
	}
	mustMatch(t, errored.Error, `synthetic timeout`, "error text")

	request := BuildDoneRequest("fix it", "Done. TOKEN=sk-live-0123456789abcdef", e)
	mustNotContain(t, request.State["final_message"].(string), "sk-live", "final message redacted")
	mustEqual(t, request.State["run"], map[string]any{"file_changes": 2, "checks_run": []string{}}, "run")
}

// twin: tests/stuck-done.test.ts:496
func TestIsUIFileReadsBraceAlternativesAndExclusions(t *testing.T) {
	globs := DefaultConfig().Done.UIFiles
	for _, p := range []string{"web/app.css", "/repo/src/App.tsx", "src/pages/index.astro", "lib/widgets/chip.dart", "site/web/app.js", "public/js/menu.js"} {
		if !IsUIFile(p, globs) {
			t.Errorf("want UI: %s", p)
		}
	}
	for _, p := range []string{"src/parser.ts", "src/server.js", "README.md", "src/App.test.tsx", "src/row.dom.test.tsx", "test/widgets/chip_test.dart", "tests/web/app.js"} {
		if IsUIFile(p, globs) {
			t.Errorf("want not UI: %s", p)
		}
	}
}

// twin: tests/stuck-done.test.ts:502
func TestIsVisualCheckCountsBrowserDeviceScreenshotAndImageReadsNotMentionsOfThem(t *testing.T) {
	visual := DefaultConfig().Done.VisualTools
	shows := func(tool string, input map[string]any, failed ...bool) bool {
		return IsVisualCheck(tool, input, len(failed) > 0 && failed[0], visual)
	}
	yes := []struct {
		tool  string
		input map[string]any
	}{
		{"bash", cmd("agent-browser open http://localhost:3000")},
		{"bash", cmd("cd web && PORT=3000 npx playwright test")},
		{"bash", cmd("cd app && fvm flutter test integration_test/app_test.dart")},
		{"bash", cmd("flutter test test/goldens/header_golden_test.dart")},
		{"bash", cmd("flutter test --update-goldens")},
		{"bash", cmd("xcrun simctl io booted screenshot /tmp/s.png")},
		{"bash", cmd("idb screenshot /tmp/s.png")},
		{"bash", cmd("idb ui tap 10 20")},
		{"bash", cmd("npx playwright test --screenshot=on")},
		{"bash", cmd("chrome --headless --screenshot=/tmp/s.png http://localhost")},
		{"bash", cmd("chromium --headless --screenshot http://localhost")},
		{"bash", cmd("google-chrome --headless=new --screenshot=/tmp/s.png http://localhost")},
		{"ctx_execute", map[string]any{"language": "shell", "code": "agent-browser snapshot -i"}},
		{"read", map[string]any{"path": "/tmp/shot.PNG"}},
		{"mcp__chrome_devtools", map[string]any{"tool": "take_screenshot"}},
		{"mcp", map[string]any{"tool": "navigate_page", "args": map[string]any{}}},
		{"take_snapshot", map[string]any{}},
	}
	for _, c := range yes {
		if !shows(c.tool, c.input) {
			t.Errorf("want visual: %s %v", c.tool, c.input)
		}
	}
	if shows("bash", cmd("agent-browser open http://localhost:3000"), true) {
		t.Error("a failed call showed nothing")
	}
	no := []struct {
		tool  string
		input map[string]any
		note  string
	}{
		{"bash", cmd(`gh pr create --title x --body "see the screenshot"`), "a PR body is data"},
		{"bash", cmd("git add web/screenshots/header.png"), "a screenshots path is not a screenshot"},
		{"bash", cmd("which chromium; ls ~/.cache/ms-playwright"), ""},
		{"bash", cmd("npm test"), ""},
		{"bash", cmd("grep -rn screenshot src"), "a command word counts only after a visual head"},
		{"bash", cmd("chromium --version"), "a browser binary without a screenshot flag shows nothing"},
		{"bash", cmd("google-chrome --headless http://localhost"), ""},
		{"bash", cmd("idb list-targets"), "idb shows the UI only through screenshot or ui"},
		{"bash", cmd("flutter test test/unit/x_test.dart"), "a unit test shows no UI"},
		{"bash", cmd("cd app && fvm flutter test test/widget_test.dart"), "a widget test is not a golden test"},
		{"read", map[string]any{"path": "web/app.css"}, ""},
		{"mcp__linear", map[string]any{"tool": "list_issues"}, ""},
	}
	for _, c := range no {
		if shows(c.tool, c.input) {
			t.Errorf("want not visual: %s %v (%s)", c.tool, c.input, c.note)
		}
	}
}

// twin: tests/stuck-done.test.ts:537
func TestRecordUIKeepsTheLastUIChangeUntilAVisualCheckFollowsIt(t *testing.T) {
	globs := DefaultConfig().Done.UIFiles
	e := EmptyEvidence()
	RecordUI(e, nil, true, globs)
	if e.UnseenUI != "" {
		t.Error("nothing to see yet")
	}
	RecordUI(e, []string{"web/app.css"}, false, globs)
	RecordUI(e, []string{"src/parser.ts"}, false, globs)
	if e.UnseenUI != "web/app.css" {
		t.Errorf("a non-UI change does not clear it: %q", e.UnseenUI)
	}
	if !NeedsDoneCheck(e) {
		t.Error("no mutation counted, yet the UI change needs proof")
	}
	RecordUI(e, nil, true, globs)
	if e.UnseenUI != "" || NeedsDoneCheck(e) {
		t.Error("a visual check clears it")
	}
}

// twin: tests/stuck-done.test.ts:551
func TestEvaluateDoneAUIChangeWithNoVisualCheckIsUnverifiedEvenWhenTestsPass(t *testing.T) {
	config := DefaultConfig().Done
	e := EmptyEvidence()
	record(e, "mutation", map[string]any{})
	RecordUI(e, []string{"web/app.css"}, false, config.UIFiles)
	record(e, "check-pass", cmd("npm test"))
	if !NeedsDoneCheck(e) {
		t.Fatal("needs done check")
	}
	v := evalDone("restyle the header", "Done, tests pass.", e, doneJudge(0.9, 0.9, "complete", 0.1))
	if !v.Unverified || v.FalseClaim || v.UnseenUI != "web/app.css" {
		t.Fatalf("tests do not apply to a style change, the visual check does: %+v", v)
	}
	want := "pi-warden: reports completion (0.90) after a UI change with no browser, screenshot, or device check since. You changed `web/app.css` but did not look at the result. Open it in a browser or take a screenshot before calling it done, or say it is unverified."
	if got := DoneNudge(v); got != want {
		t.Errorf("nudge %q", got)
	}
	if blocked := evalDone("restyle the header", "Should the header be blue?", e, doneJudge(0.9, 0.1, "blocked", 0.9)); blocked.Unverified {
		t.Error("a question to the user is not a claim")
	}
	both := EmptyEvidence()
	record(both, "mutation", map[string]any{})
	RecordUI(both, []string{"web/app.css"}, false, config.UIFiles)
	neither := evalDone("restyle the header", "Done.", both, doneJudge(0.9, 0.1, "complete", 0.9))
	mustEqual(t, neither.Reasons, []string{"reports completion (0.90) after 1 file change with no test, build, or lint run since the last change", "no browser, screenshot, or device check since the last UI change"}, "reasons")
	mustMatch(t, DoneNudge(neither), `Run the project's tests.*say so explicitly instead of presenting the work as done\. You changed `+"`web/app\\.css`", "combined nudge")
}
