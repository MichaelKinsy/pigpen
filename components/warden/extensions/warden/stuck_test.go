package warden

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

func stuckJudge(sameStrategy, approachChange, progress float64) *recordingJudge {
	return &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 9, Answers: map[string]Answer{
			"same_strategy":   noul(sameStrategy),
			"approach_change": {Type: "score", Score: approachChange, Confidence: 0.7},
			"progress":        noul(progress),
		}}, nil
	}}
}

func stuckConfig() StuckConfig { return DefaultConfig().Stuck }

func evalStuck(w *AttemptWindow, task string, config StuckConfig, j Judge) StuckVerdict {
	return EvaluateStuck(context.Background(), w, task, StuckOptions{Config: config, Judge: j, Timeout: time.Second})
}

func push(w *AttemptWindow, tool string, input map[string]any, out string, failed bool) {
	w.Push(MakeAttempt(tool, input, text(out), failed))
}

func cmd(c string) map[string]any { return map[string]any{"command": c} }

// twin: tests/stuck-done.test.ts:50
func TestMakeAttemptKeysTheExactCallRedactsAndKeepsTheOutputTail(t *testing.T) {
	long := strings.Repeat("x", 1000) + "Error: ENOENT no such file TOKEN=sk-live-0123456789abcdef"
	a := MakeAttempt("bash", cmd("npm test"), text(long), true)
	if a.Tool != "bash" || a.Call != "npm test" {
		t.Fatalf("%+v", a)
	}
	if len([]rune(a.Output)) > 440 {
		t.Errorf("output %d", len([]rune(a.Output)))
	}
	mustMatch(t, a.Output, `ENOENT`, "tail keeps the error")
	mustNotContain(t, a.Output, "sk-live", "redacted")
	if MakeAttempt("bash", cmd("npm test"), nil, true).Key != a.Key {
		t.Error("same call, same key")
	}
	if MakeAttempt("bash", cmd("npm test -- --watch"), nil, true).Key == a.Key {
		t.Error("different call, different key")
	}
	if got := MakeAttempt("edit", map[string]any{"path": "src/a.ts", "edits": []any{}}, nil, false).Call; got != "edit src/a.ts" {
		t.Errorf("call %q", got)
	}
	if !ResultFailed(false, map[string]any{"exitCode": float64(1)}, nil) || ResultFailed(false, map[string]any{"exitCode": float64(0)}, nil) || !ResultFailed(true, nil, nil) {
		t.Error("resultFailed by exit code / isError")
	}
	if !ResultFailed(false, nil, text("```shell\nnpm test\n```\n\n1 failing\n\nCommand exited with code 1")) {
		t.Error("context-mode reports the exit code inline")
	}
	if ResultFailed(false, nil, text("30 passing\n")) {
		t.Error("30 passing is not a failure")
	}
	batch := MakeAttempt("ctx_batch_execute", map[string]any{"commands": []any{map[string]any{"label": "a", "command": "npm test"}, map[string]any{"label": "b", "command": "git diff"}}}, nil, false)
	if batch.Call != "npm test\ngit diff" {
		t.Errorf("batch call %q", batch.Call)
	}
}

// twin: tests/stuck-done.test.ts:69
func TestAttemptWindowTrimsCountsFailuresAndExactRepeatsAndHonoursTheCooldown(t *testing.T) {
	w := NewAttemptWindow(3)
	for i := 0; i < 5; i++ {
		push(w, "bash", cmd("cmd "+itoa(i)), "boom", i%2 == 0)
	}
	if len(w.Attempts) != 3 || w.Failures() != 2 || w.ExactRepeats() != 1 {
		t.Fatalf("window: %d attempts, %d failures, %d repeats", len(w.Attempts), w.Failures(), w.ExactRepeats())
	}
	if w.ShouldJudge(stuckConfig()) {
		t.Error("2 failures < minFailures 3")
	}

	repeats := NewAttemptWindow(12)
	for i := 0; i < 3; i++ {
		push(repeats, "bash", cmd("npm test"), "fail", true)
	}
	if repeats.ExactRepeats() != 3 || !repeats.ShouldJudge(stuckConfig()) {
		t.Fatalf("repeats %d judge %v", repeats.ExactRepeats(), repeats.ShouldJudge(stuckConfig()))
	}
	repeats.MarkJudged()
	push(repeats, "bash", cmd("npm test"), "fail", true)
	if repeats.ShouldJudge(stuckConfig()) {
		t.Error("cool-down: 1 result since the last check")
	}
	push(repeats, "bash", cmd("npm test"), "fail", true)
	push(repeats, "bash", cmd("npm test"), "fail", true)
	if !repeats.ShouldJudge(stuckConfig()) {
		t.Error("cool-down over")
	}
	push(repeats, "bash", cmd("ls"), "ok", false)
	if repeats.ShouldJudge(stuckConfig()) {
		t.Error("latest result succeeded")
	}

	polling := NewAttemptWindow(12)
	poll := cmd("sleep 5 && gh pr checks 2673")
	for i := 0; i < 2; i++ {
		push(polling, "bash", poll, "all checks passed", false)
	}
	if polling.SuccessRepeats() != 2 || polling.ShouldJudge(stuckConfig()) {
		t.Error("2 successful repeats < minFailures 3")
	}
	push(polling, "bash", poll, "all checks passed", false)
	if polling.SuccessRepeats() != 3 || !polling.ShouldJudge(stuckConfig()) {
		t.Error("a successful poll that keeps printing the same answer is judged")
	}
	push(polling, "bash", poll, "3 of 4 checks passed", false)
	if polling.SuccessRepeats() != 1 {
		t.Error("a changed answer is progress, not a repeat")
	}
	repeats.Reset()
	if len(repeats.Attempts) != 0 {
		t.Error("reset")
	}
}

// twin: tests/stuck-done.test.ts:104
func TestEvaluateStuckDecidesExactRepeatsInCodeAndAsksJevOtherwise(t *testing.T) {
	w := NewAttemptWindow(12)
	for i := 0; i < 3; i++ {
		push(w, "bash", cmd("npm test"), "1 failing", true)
	}
	j := stuckJudge(0.9, 0, 0.1)
	repeat := evalStuck(w, "fix tests", stuckConfig(), j)
	if !repeat.Stuck || repeat.Source != "repeat" || len(j.calls()) != 0 {
		t.Fatalf("repeat: %+v calls=%d", repeat, len(j.calls()))
	}
	mustMatch(t, StuckNudge(repeat), `failed 3 times with the same output`, "nudge")

	progressing := NewAttemptWindow(12)
	push(progressing, "bash", cmd("npm test"), "3 failing (412 ms)", true)
	push(progressing, "bash", cmd("npm test"), "2 failing (398 ms)", true)
	push(progressing, "bash", cmd("npm test"), "1 failing (401 ms)", true)
	if progressing.ExactRepeats() != 1 {
		t.Error("same command, different output: the normal fix-and-rerun loop")
	}
	timing := NewAttemptWindow(12)
	push(timing, "bash", cmd("npm test"), "1 failing (412 ms)", true)
	push(timing, "bash", cmd("npm test"), "1 failing (398 ms)", true)
	if timing.ExactRepeats() != 2 {
		t.Error("only digits differ: still the same output")
	}
	mustMatch(t, FormatStuck(repeat), `exact repeat · stuck$`, "widget")

	varied := NewAttemptWindow(12)
	push(varied, "bash", cmd("npm test"), "1 failing", true)
	push(varied, "bash", cmd("npm test -- --verbose"), "1 failing", true)
	push(varied, "bash", cmd("npx jest tests/a.test.ts"), "1 failing", true)
	stuck := evalStuck(varied, "fix tests", stuckConfig(), stuckJudge(0.85, 1, 0.1))
	if !stuck.Stuck || stuck.Source != "typesafe" {
		t.Fatalf("stuck: %+v", stuck)
	}
	mustMatch(t, stuck.Reasons[0], `3 failures with the same strategy \(0\.85\)`, "reason")
	mustMatch(t, StuckNudge(stuck), `new hypothesis`, "nudge")

	fine := evalStuck(varied, "fix tests", stuckConfig(), stuckJudge(0.2, 2, 0.8))
	if fine.Stuck || len(fine.Reasons) != 0 {
		t.Errorf("fine: %+v", fine)
	}
	offline := evalStuck(varied, "fix tests", stuckConfig(), nil)
	if offline.Stuck || offline.Source != "repeat" {
		t.Errorf("offline: %+v", offline)
	}

	pollWindow := NewAttemptWindow(12)
	for i := 0; i < 3; i++ {
		push(pollWindow, "bash", cmd("sleep 5 && gh pr checks 2673"), "all checks passed", false)
	}
	pollJudge := stuckJudge(0, 0, 0)
	pv := evalStuck(pollWindow, "watch the PR", stuckConfig(), pollJudge)
	if !pv.Stuck || pv.Source != "repeat" || !pv.SuccessRepeat || len(pollJudge.calls()) != 0 {
		t.Fatalf("the same call succeeding with the same output is a repeat, decided in code: %+v", pv)
	}
	mustMatch(t, pv.Reasons[0], `succeeded 3 times with the same output`, "reason")
	mustMatch(t, StuckNudge(pv), `Stop re-running it`, "nudge says to use the answer")
	mustMatch(t, FormatStuck(pv), "successful repeat \u00b7 stuck$", "widget")

	errored := evalStuck(varied, "fix tests", stuckConfig(), failingJudge("timeout"))
	if errored.Stuck || errored.Source != "error" {
		t.Errorf("errored: %+v", errored)
	}
	mustMatch(t, errored.Error, `synthetic timeout`, "error text")
}

// twin: tests/stuck-done.test.ts:161
func TestChurnCountDetectsRepeatedCallsToTheSameTargetWithChangingOutput(t *testing.T) {
	w := NewAttemptWindow(12)
	poll := cmd("gh pr checks 2673")
	push(w, "bash", poll, "1 of 4 checks passed", false)
	push(w, "bash", poll, "2 of 4 checks passed", false)
	push(w, "bash", poll, "3 of 4 checks passed", false)
	if w.ChurnCount() != 3 || w.ExactRepeats() != 0 || w.SuccessRepeats() != 1 {
		t.Fatalf("churn %d exact %d success %d", w.ChurnCount(), w.ExactRepeats(), w.SuccessRepeats())
	}
	churn := stuckConfig()
	churn.ChurnThreshold = 3
	if !w.ShouldJudge(churn) {
		t.Error("churn threshold met triggers judgment")
	}
	push(w, "bash", poll, "3 of 4 checks passed", false)
	if w.ChurnCount() != 4 || w.SuccessRepeats() != 2 {
		t.Errorf("churn %d success %d", w.ChurnCount(), w.SuccessRepeats())
	}
	push(w, "bash", cmd("ls"), "ok", false)
	if w.ChurnCount() != 1 {
		t.Error("different target resets churn")
	}
}

// twin: tests/stuck-done.test.ts:182
func TestEvaluateStuckReturnsAChurnVerdictWhenTheSameTargetIsCalledEnoughTimes(t *testing.T) {
	w := NewAttemptWindow(12)
	for i := 0; i < 5; i++ {
		push(w, "bash", cmd("gh pr checks 2673"), itoa(i+1)+" of 4 checks passed", false)
	}
	config := stuckConfig()
	config.ChurnThreshold = 5
	v := evalStuck(w, "watch the PR", config, nil)
	if !v.Stuck || v.Source != "repeat" || !v.Churn {
		t.Fatalf("%+v", v)
	}
	mustMatch(t, v.Reasons[0], `5 times with changing output`, "reason")
	mustMatch(t, StuckNudge(v), `keeps changing but the target stays the same`, "nudge")
	mustMatch(t, FormatStuck(v), "churn \u00b7 stuck$", "widget")
}

// twin: tests/stuck-done.test.ts:196
func TestQuickRepeatFiresOnThe2ndIdenticalFailureWithNothingChangedBetweenOncePerKey(t *testing.T) {
	config := stuckConfig()
	w := NewAttemptWindow(config.Window)
	missing := "ENOENT: no such file or directory, access '/tmp/shot.png'"
	read := map[string]any{"path": "/tmp/shot.png"}
	push(w, "read", read, missing, true)
	if w.QuickRepeat() != nil {
		t.Error("a first call is not a repeat")
	}
	push(w, "bash", cmd("ls /tmp"), "a.txt", false)
	push(w, "read", read, missing, true)
	repeat := w.QuickRepeat()
	if repeat == nil || repeat.CallsAgo != 2 {
		t.Fatalf("repeat %+v", repeat)
	}
	want := "pi-warden: you already ran `read /tmp/shot.png`; it failed the same way: ENOENT: no such file or directory, access '/tmp/shot.png'. Change something before running it again."
	if got := QuickRepeatNudge(*repeat); got != want {
		t.Errorf("nudge %q", got)
	}
	push(w, "read", read, missing, true)
	if w.QuickRepeat() != nil {
		t.Error("fires once per key")
	}
	if !w.ShouldJudge(config) {
		t.Error("the 3rd identical failure still reaches the stuck check")
	}
	v := evalStuck(w, "look at the screenshot", config, nil)
	if !v.Stuck {
		t.Fatal("not stuck")
	}
	mustEqual(t, v.Reasons, []string{"the same call failed 3 times with the same output"}, "reasons")
	w.Reset()
	push(w, "read", read, missing, true)
	push(w, "read", read, missing, true)
	if w.QuickRepeat() == nil {
		t.Error("a reset window may fire again")
	}
}

// twin: tests/stuck-done.test.ts:220
func TestQuickRepeatStaysQuietAfterAChangeOnAChangedErrorAndForPolling(t *testing.T) {
	w := NewAttemptWindow(12)
	push(w, "bash", cmd("npm test"), "1 failing", true)
	push(w, "edit", map[string]any{"path": "src/a.ts", "oldText": "a", "newText": "b"}, "ok", false)
	push(w, "bash", cmd("npm test"), "1 failing", true)
	if w.QuickRepeat() != nil {
		t.Error("an edit between the calls resets the check")
	}
	push(w, "bash", cmd("npx prettier --write src"), "done", false)
	push(w, "bash", cmd("npm test"), "1 failing", true)
	if w.QuickRepeat() != nil {
		t.Error("a command that is not read-only may have changed state")
	}
	push(w, "edit", map[string]any{"path": "src/a.ts", "oldText": "x", "newText": "y"}, "oldText not found", true)
	push(w, "bash", cmd("npm test"), "1 failing", true)
	if w.QuickRepeat() != nil {
		t.Error("a failed edit is not provably read-only either")
	}
	push(w, "bash", cmd("npm test"), "2 failing", true)
	if w.QuickRepeat() != nil {
		t.Error("a changed error is progress")
	}
	push(w, "grep", map[string]any{"pattern": "parse", "path": "src"}, "src/a.ts:1: parse", false)
	push(w, "ls", map[string]any{"path": "src"}, "a.ts", false)
	push(w, "bash", cmd("npm test"), "2 failing", true)
	if w.QuickRepeat() == nil {
		t.Error("built-in read tools change nothing, so the same failure repeats")
	}

	w.Reset()
	push(w, "read", map[string]any{"path": "src/a.ts"}, "const a = 1;", false)
	push(w, "mcp", map[string]any{"tool": "chrome_devtools_navigate", "args": map[string]any{"url": "http://localhost:3000"}}, "ok", false)
	push(w, "read", map[string]any{"path": "src/a.ts"}, "const a = 1;", false)
	if w.QuickRepeat() != nil {
		t.Error("an MCP call may have changed state")
	}
	push(w, "ctx_execute", map[string]any{"language": "javascript", "code": "console.log(1)"}, "1", false)
	push(w, "read", map[string]any{"path": "src/a.ts"}, "const a = 1;", false)
	if w.QuickRepeat() != nil {
		t.Error("a script call may have changed state")
	}

	w.Reset()
	push(w, "bash", cmd("sleep 5"), "", false)
	push(w, "bash", cmd("sleep 5"), "", false)
	if w.QuickRepeat() != nil {
		t.Error("sleep is waiting, not a repeat")
	}
	push(w, "bash", cmd("git status --short"), " M a.ts", false)
	push(w, "bash", cmd("git status --short"), " M a.ts", false)
	if w.QuickRepeat() != nil {
		t.Error("status checks are polling")
	}
	push(w, "bash", cmd("gh run watch 42"), "failed", true)
	push(w, "bash", cmd("gh run watch 42"), "failed", true)
	if w.QuickRepeat() != nil {
		t.Error("watching a run is polling, even when it reports a failure")
	}
	push(w, "bash", cmd("npm run build"), "built", false)
	push(w, "bash", cmd("npm run build"), "built", false)
	if w.QuickRepeat() != nil {
		t.Error("a successful call that is not a read is not flagged")
	}
}

// twin: tests/stuck-done.test.ts:263
func TestQuickRepeatFlagsTheSameReadTwiceWithTheSameOutput(t *testing.T) {
	w := NewAttemptWindow(12)
	input := map[string]any{"path": "src/config.ts", "offset": float64(40), "limit": float64(20)}
	push(w, "read", input, "export interface StuckGuardConfig {", false)
	push(w, "bash", cmd("grep -n repeat src/stuck.ts"), "12: repeat", false)
	push(w, "read", map[string]any{"path": "src/config.ts", "offset": float64(60), "limit": float64(20)}, "other lines", false)
	push(w, "read", input, "export interface StuckGuardConfig {", false)
	repeat := w.QuickRepeat()
	if repeat == nil {
		t.Fatal("no repeat")
	}
	want := "pi-warden: you already have this output from `read src/config.ts` (3 calls ago); nothing changed since. Use that output instead of running the call again."
	if got := QuickRepeatNudge(*repeat); got != want {
		t.Errorf("nudge %q", got)
	}
	push(w, "bash", cmd("cat package.json"), "{}", false)
	push(w, "bash", cmd("cat package.json"), "{}", false)
	if w.QuickRepeat() == nil {
		t.Error("a read-only shell command counts as a read")
	}
}

// twin: tests/stuck-done.test.ts:279
func TestBuildStuckRequestSendsNumberedAttemptsWithOutcomesAndATask(t *testing.T) {
	w := NewAttemptWindow(12)
	push(w, "bash", cmd("npm test"), "boom", true)
	push(w, "edit", map[string]any{"path": "a.ts", "edits": []any{}}, "ok", false)
	r := BuildStuckRequest(w.Attempts, "fix it")
	if r.State["task"] != "fix it" {
		t.Errorf("task %v", r.State["task"])
	}
	mustEqual(t, r.State["attempts"], []map[string]any{
		{"n": 1, "tool": "bash", "call": "npm test", "outcome": "failed", "output": "boom"},
		{"n": 2, "tool": "edit", "call": "edit a.ts", "outcome": "ok", "output": "ok"},
	}, "attempts")
	ids := []string{}
	for id := range r.Questions {
		ids = append(ids, id)
	}
	mustEqual(t, sortedStrings(ids), []string{"approach_change", "progress", "same_strategy"}, "questions")
	if BuildStuckRequest(nil, "").State["task"] != "(no user request recorded in this session)" {
		t.Error("empty task text")
	}
}

// twin: tests/stuck-done.test.ts:442
func TestStuckDiffShowsAUnifiedDiffForOutputsDifferingInOneLine(t *testing.T) {
	r := StuckDiff("line1\nline2-old\nline3", "line1\nline2-new\nline3", DiffOptions{DiffLimit: 3000, TailLimit: 1000, FullPath: "/tmp/output.txt"})
	for _, p := range []string{`stuck-loop diff`, `- line2-old`, `\+ line2-new`, `Full output: /tmp/output\.txt`} {
		mustMatch(t, r, p, "diff")
	}
}

// twin: tests/stuck-done.test.ts:452
func TestStuckDiffSaysOutputsAreIdenticalWhenByteIdentical(t *testing.T) {
	same := "same output\nline2"
	r := StuckDiff(same, same, DiffOptions{DiffLimit: 3000, TailLimit: 1000, FullPath: "/tmp/out.txt"})
	mustMatch(t, r, `byte-identical`, "identical")
	mustMatch(t, r, `see the full output at /tmp/out\.txt`, "path")
	mustNotContain(t, r, "Full output:", "one-line form does not use the multi-line footer")
}

// twin: tests/stuck-done.test.ts:460
func TestStuckDiffTruncatesADiffExceedingTheCap(t *testing.T) {
	var prev, cur []string
	for i := 0; i < 200; i++ {
		prev = append(prev, "prev-"+itoa(i))
		cur = append(cur, "curr-"+itoa(i))
	}
	r := StuckDiff(strings.Join(prev, "\n"), strings.Join(cur, "\n"), DiffOptions{DiffLimit: 500, TailLimit: 200, FullPath: "/tmp/out.txt"})
	mustMatch(t, r, "\u2026 \\[diff truncated\\]", "truncated")
	mustMatch(t, r, `Full output: /tmp/out\.txt`, "path")
	if len(r) >= 2000 {
		t.Errorf("note should be compact; got %d", len(r))
	}
}

func lines(n int, replace map[int]string) string {
	out := make([]string, n)
	for i := range out {
		if r, ok := replace[i]; ok {
			out[i] = r
		} else {
			out[i] = "line-" + itoa(i)
		}
	}
	return strings.Join(out, "\n")
}

// twin: tests/stuck-done.test.ts:471
func TestLineDiffEmitsOneHunkWithContextForASingleLineDifference(t *testing.T) {
	r := StuckDiff(lines(60, nil), lines(60, map[int]string{30: "CHANGED"}), DiffOptions{DiffLimit: 3000, TailLimit: 1000, FullPath: "/tmp/out.txt"})
	if n := len(regexp.MustCompile(`(?m)^@@ `).FindAllString(r, -1)); n != 1 {
		t.Errorf("hunk headers: %d", n)
	}
	mustMatch(t, r, `- line-30`, "deleted line")
	mustMatch(t, r, `\+ CHANGED`, "added line")
	body := 0
	for _, l := range strings.Split(r, "\n") {
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, "+") {
			body++
		}
	}
	if body > 8 {
		t.Errorf("hunk body has %d lines, expected at most 8", body)
	}
}

// twin: tests/stuck-done.test.ts:485
func TestLineDiffEmitsTwoHunksForDifferencesAtLine5AndLine50(t *testing.T) {
	r := StuckDiff(lines(60, nil), lines(60, map[int]string{5: "FIRST-CHANGE", 50: "SECOND-CHANGE"}), DiffOptions{DiffLimit: 3000, TailLimit: 1000, FullPath: "/tmp/out.txt"})
	if n := len(regexp.MustCompile(`(?m)^@@ `).FindAllString(r, -1)); n != 2 {
		t.Errorf("hunk headers: %d", n)
	}
	for _, p := range []string{`- line-5`, `\+ FIRST-CHANGE`, `- line-50`, `\+ SECOND-CHANGE`} {
		mustMatch(t, r, p, "hunk")
	}
}

// Skipped twins (stuck evidence, tests/stuck-evidence.test.ts, 12 cases): the port runs in the
// documented `stuck.evidence: false` state (docs/guards.md "Stuck": "restores the tails-only state").
func TestSkippedTwinsStuckEvidence(t *testing.T) {
	t.Skip("stuck.evidence (parsed failures, edit diffs, digest for the judge) is deferred: owner decision needed; the port ships the tails-only state upstream documents as stuck.evidence:false")
}
