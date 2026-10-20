package warden

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func questionIDs(r Request) []string {
	var ids []string
	for id := range r.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// slopJudge is guard.test.ts `withSlop`: the base answers plus a score per slop symptom asked and an approval answer.
func slopJudge(irreversible, offTask float64, slop map[string]float64, approved float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		e := actionAnswers(irreversible, offTask, "expected_step", nil)
		for _, s := range SlopSymptoms {
			if has(r.Questions, "slop_"+s) {
				v, ok := slop[s]
				if !ok {
					v = 0.05
				}
				e.Answers["slop_"+s] = noul(v)
			}
		}
		if has(r.Questions, "approved") {
			e.Answers["approved"] = noul(approved)
		}
		return e, nil
	}}
}

// twin: tests/guard.test.ts:901 "slop questions join the write/edit request only, score per symptom, and never raise the level"
func TestSlopQuestionsJoinTheWriteRequestOnly(t *testing.T) {
	config := DefaultConfig()
	j := slopJudge(0.1, 0.1, map[string]float64{"stub": 0.95, "hedging": 0.8, "comments": 0.2}, 0)
	slopOn := &config.Slop
	write := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(cwd, "a.ts"), "content": "// TODO implement\nexport function a() { return null as any; }"}, Task: "implement a()"}, EvaluateOptions{Config: config.Action, Judge: j, GuardExtras: GuardExtras{Slop: slopOn}})
	mustEqual(t, questionIDs(j.calls()[0]), []string{"irreversible", "mutates", "off_task", "scope", "should_proceed", "slop_comments", "slop_dead", "slop_hedging", "slop_stub"}, "write questions")
	if write.Level != LevelAllow {
		t.Fatal("slop never blocks")
	}
	mustEqual(t, *write.Slop, SlopJudgment{Stub: 0.95, Comments: 0.2, Dead: 0.05, Hedging: 0.8}, "scores")
	mustEqual(t, write.SlopSymptoms, []string{"stub", "hedging"}, "strongest first")
	mustMatch(t, write.SlopReasons[0], `stub or placeholder code .* \(0\.95\)`, "reason")
	mustMatch(t, FormatVerdict(write), `slop: stub 0\.95, hedging 0\.80`, "widget line")

	bash := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "test"}, EvaluateOptions{Config: config.Action, Judge: j, GuardExtras: GuardExtras{Slop: slopOn}})
	mustEqual(t, questionIDs(j.calls()[1]), []string{"irreversible", "mutates", "off_task", "scope", "should_proceed", "visible"}, "no slop questions for bash")
	if bash.Slop != nil {
		t.Fatal("bash slop")
	}

	clean := eval(ActionInput{Tool: "edit", Input: map[string]any{"path": filepath.Join(cwd, "a.ts"), "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}}, Task: "rename"}, EvaluateOptions{Config: config.Action, Judge: slopJudge(0.1, 0.1, nil, 0), GuardExtras: GuardExtras{Slop: slopOn}})
	if clean.Slop == nil || clean.SlopSymptoms != nil {
		t.Fatalf("%+v", clean)
	}
	mustMatch(t, FormatVerdict(clean), `slop: none`, "widget line")

	off := config.Slop
	off.Enabled = false
	if v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(cwd, "a.ts"), "content": "x"}, Task: "t"}, EvaluateOptions{Config: config.Action, Judge: j, GuardExtras: GuardExtras{Slop: &off}}); v.Slop != nil {
		t.Fatal("slop while off")
	}
}

// twin: tests/guard.test.ts:954 "the regret question rides the request with last turn's allowed calls; a locator joins from two candidates"
func TestTheRegretQuestionRidesTheRequest(t *testing.T) {
	summary := DescribeAction("bash", map[string]any{"command": "npm test"}, cwd)
	one := []PreviousAction{{ID: "a1", Tool: "bash", Command: "git push origin main"}}
	single := BuildRequest(summary, "wait, don't push yet", BuildExtras{PreviousActions: one})
	mustEqual(t, single.State["previous_actions"], any([]PreviousAction(one)), "previous_actions")
	if !has(single.Questions, "regretted") || has(single.Questions, "regret_target") {
		t.Fatal("one candidate needs no locator")
	}
	if _, ok := BuildRequest(summary, "t", BuildExtras{}).State["previous_actions"]; ok {
		t.Fatal("no candidates, no field")
	}

	var many []PreviousAction
	for i := 1; i <= 8; i++ {
		many = append(many, PreviousAction{ID: "a" + itoaInt(i), Tool: "bash", Command: "step " + itoaInt(i) + " " + strings.Repeat("x", 400)})
	}
	request := BuildRequest(summary, "t", BuildExtras{PreviousActions: many})
	sent := request.State["previous_actions"].([]PreviousAction)
	var ids []string
	for _, a := range sent {
		ids = append(ids, a.ID)
		if utf16Len(a.Command) >= 340 {
			t.Errorf("command not truncated: %d", utf16Len(a.Command))
		}
	}
	mustEqual(t, ids, []string{"a3", "a4", "a5", "a6", "a7", "a8"}, "the six most recent")
	mustEqual(t, request.Questions["regret_target"].CriteriaOrder, ids, "locator options")

	j := &recordingJudge{handler: func(Request) (Evaluation, error) {
		e := actionAnswers(0.1, 0.1, "expected_step", nil)
		e.Answers["regretted"] = noul(0.9)
		e.Answers["regret_target"] = Answer{Type: "choice", Choice: "a2", Confidence: 0.7}
		return e, nil
	}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "wait, undo that"}, EvaluateOptions{Config: cfg(), Judge: j, GuardExtras: GuardExtras{PreviousActions: []PreviousAction{one[0], {ID: "a2", Tool: "write", Path: "a.ts"}}}})
	if v.Level != LevelAllow {
		t.Fatal("regret labels earlier calls; it never changes this verdict")
	}
	if v.Judgment.Regretted == nil || *v.Judgment.Regretted != 0.9 || v.Judgment.RegretTarget != "a2" {
		t.Fatalf("%+v", v.Judgment)
	}
	mustEqual(t, questionIDs(j.calls()[0]), []string{"irreversible", "mutates", "off_task", "regret_target", "regretted", "scope", "should_proceed", "visible"}, "questions")
}

// twin: tests/guard.test.ts:1380 "rules.enabled false keeps the rules file out of the action request; true and omitted send it as before"
func TestRulesEnabledFalseKeepsTheRulesFileOut(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "pi-warden.md"), []byte("# Rules\n\n- Never commit secrets.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := func(rules *RulesConfig) map[string]any {
		spy := actionJudge(0.1, 0.1, "", nil)
		eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Cwd: project, Task: "run the tests"}, EvaluateOptions{Config: cfg(), Judge: spy, GuardExtras: GuardExtras{Rules: rules}})
		return spy.calls()[0].State
	}
	off := state(&RulesConfig{Enabled: false})
	if _, ok := off["rules"]; ok {
		t.Fatal("no rules content is sent when the rules guard is off")
	}
	if _, ok := off["rulesSource"]; ok {
		t.Fatal("and no rulesSource names the file it came from")
	}
	on := state(&RulesConfig{Enabled: true})
	mustMatch(t, on["rules"].(string), `Never commit secrets`, "the rules content is sent when the rules guard is on")
	if on["rulesSource"] != "pi-warden.md" {
		t.Fatal(on["rulesSource"])
	}
	unset := state(nil)
	if unset["rules"] != on["rules"] || unset["rulesSource"] != "pi-warden.md" {
		t.Fatal("a library caller that passes no rules config keeps the earlier behaviour")
	}
}

func largeOutputJudge(score float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		e := actionAnswers(0.05, 0.05, "expected_step", f(0.05))
		if has(r.Questions, "large_output") {
			e.Answers["large_output"] = noul(score)
		}
		return e, nil
	}}
}

var largeOutputOn = &LargeOutputConfig{Enabled: true, Threshold: 0.85}

// twin: tests/guard.test.ts:1444 "large output: above the threshold steers once per command family per session and never holds"
func TestLargeOutputAboveTheThresholdSteersOncePerFamily(t *testing.T) {
	steered := map[string]bool{}
	run := func(command, task string, score float64) Verdict {
		return eval(ActionInput{Tool: "bash", Input: map[string]any{"command": command}, Task: task}, EvaluateOptions{Config: cfg(), Judge: largeOutputJudge(score), GuardExtras: GuardExtras{LargeOutput: largeOutputOn}})
	}
	first := run("npm test -- --reporter=spec", "Run the tests and fix the failures", 0.92)
	if first.Judgment.LargeOutput == nil || *first.Judgment.LargeOutput != 0.92 || first.LargeOutputFamily != "npm test" {
		t.Fatalf("%+v %q", first.Judgment, first.LargeOutputFamily)
	}
	if first.Level != LevelAllow || len(first.Reasons) != 0 {
		t.Fatalf("the command is not held or warned: %s %v", first.Level, first.Reasons)
	}
	told := largeOutputNotice(first, steered)
	if told != "pi-warden: `npm test` commands may print far more than you need (large-output 0.92). This one runs unchanged.\nNext time, redirect and show the tail: `npm test … > /tmp/warden-npm-test.log 2>&1; tail -40 /tmp/warden-npm-test.log`." {
		t.Fatalf("%q", told)
	}
	if len(strings.Split(told, "\n")) > 3 {
		t.Fatal("too long")
	}
	again := run("CI=1 npm test --verbose", "Run the tests and fix the failures", 0.97)
	if again.LargeOutputFamily != "npm test" || largeOutputNotice(again, steered) != "" {
		t.Fatal("the second call in the same family is not steered")
	}
	other := run("git log -p", "Why did the build break?", 0.9)
	if other.Source != "read-only" || other.LargeOutputFamily != "" {
		t.Fatalf("read-only commands skip the judge, so the question never rides them: %s", other.Source)
	}
}

// twin: tests/guard.test.ts:1463 "large output: below the threshold does not steer"
func TestLargeOutputBelowTheThresholdDoesNotSteer(t *testing.T) {
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "Run the tests"}, EvaluateOptions{Config: cfg(), Judge: largeOutputJudge(0.84), GuardExtras: GuardExtras{LargeOutput: largeOutputOn}})
	if v.Judgment.LargeOutput == nil || *v.Judgment.LargeOutput != 0.84 || v.LargeOutputFamily != "" || largeOutputNotice(v, map[string]bool{}) != "" {
		t.Fatalf("%+v", v)
	}
}

// twin: tests/guard.test.ts:1470 "large output: non-bash tools never ask the question"
func TestLargeOutputNonBashToolsNeverAsk(t *testing.T) {
	config := cfg()
	config.Tools = []string{"bash", "powershell", "write", "ctx_execute"}
	for _, c := range []struct {
		tool  string
		input map[string]any
	}{
		{"write", map[string]any{"path": "notes.txt", "content": "hello"}},
		{"powershell", map[string]any{"command": "Get-Content big.log"}},
		{"ctx_execute", map[string]any{"language": "shell", "code": "npm test"}},
	} {
		probe := largeOutputJudge(0.99)
		v := eval(ActionInput{Tool: c.tool, Input: c.input, Task: "Run the tests"}, EvaluateOptions{Config: config, Judge: probe, GuardExtras: GuardExtras{LargeOutput: largeOutputOn}})
		if len(probe.calls()) != 1 || has(probe.calls()[0].Questions, "large_output") || v.LargeOutputFamily != "" {
			t.Errorf("%s: %d calls", c.tool, len(probe.calls()))
		}
	}
	if has(BuildRequest(DescribeAction("write", map[string]any{"path": "a.txt", "content": "x"}, cwd), "task", BuildExtras{LargeOutput: true}).Questions, "large_output") {
		t.Fatal("write")
	}
	if !has(BuildRequest(DescribeAction("bash", map[string]any{"command": "npm test"}, cwd), "task", BuildExtras{LargeOutput: true}).Questions, "large_output") {
		t.Fatal("bash")
	}
}

// twin: tests/guard.test.ts:1483 "large output: a disabled config never asks the question"
func TestLargeOutputADisabledConfigNeverAsks(t *testing.T) {
	for _, lo := range []*LargeOutputConfig{{Enabled: false, Threshold: 0.85}, nil} {
		probe := largeOutputJudge(0.99)
		v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "Run the tests"}, EvaluateOptions{Config: cfg(), Judge: probe, GuardExtras: GuardExtras{LargeOutput: lo}})
		if has(probe.calls()[0].Questions, "large_output") || v.Judgment.LargeOutput != nil || v.LargeOutputFamily != "" {
			t.Fatalf("%+v", lo)
		}
	}
	if DefaultConfig().Context.LargeOutput != (LargeOutputConfig{Enabled: true, Threshold: 0.85}) {
		t.Fatal("default")
	}
}

// Not a twin: the tokens half of guard.test.ts:1494 (the trace details half rides trace.ts actionDetails, another lane).
func TestLargeOutputScoreIsInTheActionTokensWhenJudged(t *testing.T) {
	judged := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "Run the tests"}, EvaluateOptions{Config: cfg(), Judge: largeOutputJudge(0.3), GuardExtras: GuardExtras{LargeOutput: largeOutputOn}})
	if actionTokens(judged, time.Unix(0, 0))["largeOutput"] != "0.30" {
		t.Fatal("judged")
	}
	unasked := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "Run the tests"}, EvaluateOptions{Config: cfg(), Judge: largeOutputJudge(0.3)})
	if _, ok := actionTokens(unasked, time.Unix(0, 0))["largeOutput"]; ok {
		t.Fatal("unasked")
	}
}

// twin: tests/guard.test.ts:1513 "commandFamily names the head and, for tools with subcommands, the subcommand"
func TestCommandFamilyNamesTheHeadAndSubcommand(t *testing.T) {
	for command, want := range map[string]string{
		"npm test -- --verbose":              "npm test",
		"npm run build 2>&1":                 "npm run build",
		"cd repo && git log -p":              "git log",
		"sudo /usr/bin/find / -name '*.log'": "find",
		"FOO=1 cat big.log | wc -l":          "cat",
		"git":                                "git",
		"   ":                                "",
	} {
		if got := commandFamily(command); got != want {
			t.Errorf("%q: %q, want %q", command, got, want)
		}
	}
}

// twin: tests/action-guard.test.ts:201 "the large-output question rides bash requests through the guard when the context config enables it"
func TestTheLargeOutputQuestionRidesBashRequestsThroughTheGuard(t *testing.T) {
	judge, _ := newStub()
	g := NewActionGuard()
	with := func(lo LargeOutputConfig) InspectOptions {
		o := opts(judge)
		o.GuardExtras = GuardExtras{LargeOutput: &lo}
		return o
	}
	last := func() Request { c := judge.calls(); return c[len(c)-1] }
	largeOutput := DefaultConfig().Context.LargeOutput
	g.Inspect(t.Context(), bash("b1", "npm test"), under("Run the tests"), with(largeOutput))
	if !has(last().Questions, "large_output") {
		t.Fatal("bash")
	}
	g.Inspect(t.Context(), ToolCallRef{ID: "w1", Tool: "write", Input: map[string]any{"path": "notes.txt", "content": "hello"}}, under("Run the tests"), with(largeOutput))
	if has(last().Questions, "large_output") {
		t.Fatal("non-bash tools never ask")
	}
	off := largeOutput
	off.Enabled = false
	g.Inspect(t.Context(), bash("b2", "npm run build"), under("Run the tests"), with(off))
	if has(last().Questions, "large_output") {
		t.Fatal("disabled config never asks")
	}
}
