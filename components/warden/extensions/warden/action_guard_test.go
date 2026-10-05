package warden

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// stubJudge is tests/action-guard.test.ts `stubJudge`: `next` sets the answers of the next
// evaluations; `open` keeps requests pending until the test releases them, which is how
// overlapping sibling requests are observed.
type stubAnswers struct {
	irreversible  float64
	offTask       float64
	scope         string
	mutates       float64
	approved      *float64
	shouldProceed *float64
}

func newStub() (*recordingJudge, *stubAnswers) {
	next := &stubAnswers{irreversible: 0.1, offTask: 0.1, scope: "expected_step", mutates: 0.9}
	j := &recordingJudge{}
	j.handler = func(r Request) (Evaluation, error) {
		a := *next
		sp := 1.0
		if a.shouldProceed != nil {
			sp = *a.shouldProceed
		}
		e := Evaluation{Model: "jev-test", ElapsedMs: 5, Answers: map[string]Answer{
			"irreversible":   noul(a.irreversible),
			"off_task":       noul(a.offTask),
			"scope":          {Type: "choice", Choice: a.scope, Confidence: 0.9},
			"mutates":        noul(a.mutates),
			"should_proceed": noul(sp),
		}}
		if has(r.Questions, "approved") {
			ap := 0.0
			if a.approved != nil {
				ap = *a.approved
			}
			e.Answers["approved"] = noul(ap)
		}
		return e, nil
	}
	return j, next
}

func bash(id, command string) ToolCallRef {
	return ToolCallRef{ID: id, Tool: "bash", Input: map[string]any{"command": command}}
}

func opts(j Judge) InspectOptions {
	return InspectOptions{Config: DefaultConfig().Action, Cwd: cwd, Judge: j}
}

func under(task string, siblings ...ToolCallRef) Conversation {
	return Conversation{Task: task, Siblings: siblings}
}

func inspect(g *ActionGuard, call ToolCallRef, conv Conversation, j Judge) Verdict {
	return g.Inspect(context.Background(), call, conv, opts(j))
}

func askedApproval(j *recordingJudge) bool {
	calls := j.calls()
	return len(calls) > 0 && has(calls[len(calls)-1].Questions, "approved")
}

func actionOf(r Request) string {
	s := r.State["action"].(ActionSummary)
	if s.Command != "" {
		return s.Command
	}
	return s.Path
}

// twin: tests/action-guard.test.ts:47
func TestShouldProceedRecordsLowScoresAsTraceOnlyByDefaultAndSupportsSteerOptIn(t *testing.T) {
	j, next := newStub()
	next.irreversible = 0.1
	next.shouldProceed = f(0.3)
	for _, steer := range []bool{false, true} {
		o := opts(j)
		o.Config.ShouldProceed.Steer = steer
		v := NewActionGuard().Inspect(context.Background(), bash("quiet", "npm test"), under("run tests"), o)
		if v.Level != LevelWarn || v.Judgment.ShouldProceed == nil || *v.Judgment.ShouldProceed != 0.3 || !v.ShouldProceedSteer {
			t.Fatalf("steer=%v: %+v", steer, v)
		}
		if v.ShouldProceedTraceOnly == steer {
			t.Errorf("steer=%v traceOnly=%v", steer, v.ShouldProceedTraceOnly)
		}
		want := "should-proceed 0.30 (trace-only until calibrated)"
		if steer {
			want = "should-proceed 0.30 (may need user input before continuing)"
		}
		if !contains(v.Reasons, want) {
			t.Errorf("reasons %v lack %q", v.Reasons, want)
		}
		if !steer && v.Reasons[v.ShouldProceedTraceOnlyReasonIndex] != want {
			t.Errorf("trace-only index points at %q", v.Reasons[v.ShouldProceedTraceOnlyReasonIndex])
		}
	}
}

// twin: tests/action-guard.test.ts:63
func TestAHoldIsReleasedByAReplyJevReadsAsApproval(t *testing.T) {
	g := NewActionGuard()
	j, next := newStub()
	next.irreversible = 0.9
	v := inspect(g, bash("c1", "git push --force"), under("push my branch"), j)
	if v.Level != LevelConfirm {
		t.Fatalf("c1: %s", v.Level)
	}
	if askedApproval(j) {
		t.Error("no hold yet: nothing to approve")
	}
	g.Hold("push my branch")

	v = inspect(g, bash("c2", "git push --force"), under("push my branch"), j)
	if v.Level != LevelConfirm || askedApproval(j) {
		t.Errorf("same prompt: the user has not replied: %s asked=%v", v.Level, askedApproval(j))
	}
	g.Hold("push my branch")

	*next = stubAnswers{irreversible: 0.9, offTask: 0.1, scope: "expected_step", mutates: 0.9, approved: f(0.2)}
	v = inspect(g, bash("c3", "git push --force"), under("hmm, why is that needed?"), j)
	if v.Level != LevelConfirm || !askedApproval(j) {
		t.Errorf("Jev decides: 0.2 is not approval: %s asked=%v", v.Level, askedApproval(j))
	}
	g.Hold("hmm, why is that needed?")

	next.approved = f(0.95)
	v = inspect(g, bash("c4", "git push --force"), under("YES. Force push it now, I own that branch."), j)
	if v.Level != LevelAllow || !v.ApprovedByUser {
		t.Fatalf("c4: %+v", v)
	}
	mustMatch(t, v.Reasons[0], `^user approved in the latest message \(0\.95\)`, "reason")

	v = inspect(g, bash("c5", "git push --force"), under("push my branch"), j)
	if v.Level != LevelConfirm || askedApproval(j) {
		t.Errorf("approval is consumed; a new hold starts: %s asked=%v", v.Level, askedApproval(j))
	}
}

// twin: tests/action-guard.test.ts:94 (regression: Ryan, 2026-09-16)
func TestApprovalAppliesToTheActionNotTheExactCommandString(t *testing.T) {
	g := NewActionGuard()
	j, next := newStub()
	*next = stubAnswers{irreversible: 0.92, offTask: 0.1, scope: "expected_step", mutates: 0.95}
	if v := inspect(g, bash("c1", "cd wt && command -v supabase; supabase db reset 2>&1 | tail -25"), under("prove the three migrations"), j); v.Level != LevelConfirm {
		t.Fatalf("c1: %s", v.Level)
	}
	g.Hold("prove the three migrations")
	next.approved = f(0.93)
	if v := inspect(g, bash("c2", "cd wt && supabase db reset 2>&1 | tail -25"), under("Yes you can."), j); v.Level != LevelAllow {
		t.Fatalf("reworded retry after approval runs: %s", v.Level)
	}
	if !askedApproval(j) {
		t.Error("Jev was asked whether the reply approves this action")
	}

	next.approved = nil
	if v := inspect(g, bash("c3", "supabase db reset"), under("wipe the local db and replay migrations"), j); v.Level != LevelConfirm {
		t.Fatalf("c3: %s", v.Level)
	}
	g.Hold("wipe the local db and replay migrations")
	next.approved = f(0.2)
	if v := inspect(g, bash("c4", "supabase db reset 2>&1 | tail -25"), under("Yes you can."), j); v.Level != LevelConfirm {
		t.Fatalf("held again: 0.2 is not approval: %s", v.Level)
	}
	g.Hold("Yes you can.")
	next.approved = f(0.9)
	again := inspect(g, bash("c5", "supabase db reset 2>&1 | tail -40"), under("Yes you can."), j)
	if !askedApproval(j) || again.Level != LevelAllow {
		t.Errorf("the re-hold did not consume the user's reply: asked=%v level=%s", askedApproval(j), again.Level)
	}

	*next = stubAnswers{irreversible: 0.9, offTask: 0.2, scope: "expected_step", mutates: 0.95}
	if v := inspect(g, bash("c6", "git push --force"), under("clean up"), j); v.Level != LevelConfirm {
		t.Fatalf("c6: %s", v.Level)
	}
	g.Hold("clean up")
	*next = stubAnswers{irreversible: 0.95, offTask: 0.9, scope: "unrelated", mutates: 0.95, approved: f(0.05)}
	if v := inspect(g, bash("c7", "rm -rf ~/Documents"), under("yes"), j); v.Level != LevelConfirm {
		t.Errorf("a yes to one action does not approve a different one: %s", v.Level)
	}
}

// twin: tests/action-guard.test.ts:127
func TestWithoutAJudgeAReplyThatReadsAsApprovalStandsInForTheQuestion(t *testing.T) {
	g := NewActionGuard()
	level := func(id, task string) Verdict { return inspect(g, bash(id, "git push --force"), under(task), nil) }
	if level("c1", "push my branch").Level != LevelConfirm {
		t.Fatal("destructive pattern")
	}
	g.Hold("push my branch")
	if level("c2", "push my branch").Level != LevelConfirm {
		t.Fatal("same prompt")
	}
	g.Hold("push my branch")
	if level("c3", "hmm, why is that needed?").Level != LevelConfirm {
		t.Fatal("a question is not approval")
	}
	g.Hold("hmm, why is that needed?")
	if level("c4", "no, don't do that").Level != LevelConfirm {
		t.Fatal("a refusal with a yes-word is not approval")
	}
	g.Hold("no, don't do that")
	approved := level("c5", "yes, that's fine")
	if approved.Level != LevelAllow || !approved.ApprovedByUser || approved.Reasons[0] != "user approved in the latest message" {
		t.Fatalf("approved: %+v", approved)
	}
	if level("c6", "yes, that's fine").Level != LevelConfirm {
		t.Fatal("approval is consumed by the call it released")
	}
}

// twin: tests/action-guard.test.ts:144
func TestSiblingsOfOneAssistantMessageAreJudgedTogetherEachOnce(t *testing.T) {
	g := NewActionGuard()
	j, next := newStub()
	siblings := []ToolCallRef{
		bash("a", "npm test"),
		bash("b", "npm run lint"),
		{ID: "c", Tool: "read", Input: map[string]any{"path": "README.md"}},
		{ID: "d", Tool: "write", Input: map[string]any{"path": "note.txt", "content": "hello"}},
	}
	j.gate = make(chan struct{})
	done := make(chan Verdict, 1)
	go func() { done <- inspect(g, siblings[0], under("run the checks", siblings...), j) }()
	waitFor(t, func() bool { return len(j.calls()) == 3 })
	got := []string{}
	for _, r := range j.calls() {
		got = append(got, actionOf(r))
	}
	mustEqual(t, sortedStrings(got), []string{"note.txt", "npm run lint", "npm test"}, "the read is not guarded; the other three requests are in flight")
	close(j.gate)
	j.mu.Lock()
	j.gate = nil
	j.mu.Unlock()
	if v := <-done; v.Level != LevelAllow {
		t.Fatalf("a: %s", v.Level)
	}
	if v := inspect(g, siblings[1], under("run the checks", siblings...), j); v.Level != LevelAllow {
		t.Fatalf("b: %s", v.Level)
	}
	if v := inspect(g, siblings[3], under("run the checks", siblings...), j); v.Level != LevelAllow {
		t.Fatalf("d: %s", v.Level)
	}
	if n := len(j.calls()); n != 3 {
		t.Fatalf("each sibling is judged exactly once: %d", n)
	}

	// Another hook rewrote b's input before the guard saw it: the stored judgment is stale and a fresh one is made.
	g.TurnEnd()
	inspect(g, siblings[0], under("run the checks", siblings...), j)
	inspect(g, bash("b", "npm run lint -- --fix"), under("run the checks", siblings...), j)
	calls := j.calls()
	if len(calls) != 7 {
		t.Fatalf("three prejudged plus one fresh judgment for the changed input: %d", len(calls))
	}
	if actionOf(calls[len(calls)-1]) != "npm run lint -- --fix" {
		t.Errorf("last request %q", actionOf(calls[len(calls)-1]))
	}

	// A retry after a hold stays sequential: an approval consumed by one sibling would change the question for the next.
	g.Wait()
	next.irreversible = 0.9
	g.Hold("run the checks")
	j.reset()
	*next = stubAnswers{irreversible: 0.9, offTask: 0.1, scope: "expected_step", mutates: 0.9, approved: f(0.9)}
	inspect(g, siblings[0], under("yes", siblings...), j)
	if n := len(j.calls()); n != 1 {
		t.Errorf("no sibling preflight while an approval is pending: %d", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

// twin: tests/action-guard.test.ts:180
func TestAPrejudgmentIsUsedOnceAndDoesNotSurviveTheTurn(t *testing.T) {
	g := NewActionGuard()
	j, next := newStub()
	siblings := []ToolCallRef{bash("a", "npm test"), bash("b", "npm run lint")}
	inspect(g, siblings[0], under("run the checks", siblings...), j)
	if n := len(j.calls()); n != 2 {
		t.Fatalf("first: %d", n)
	}
	inspect(g, siblings[0], under("run the checks", siblings...), j)
	if n := len(j.calls()); n != 3 {
		t.Fatalf("the same call inspected again is judged afresh: %d", n)
	}
	g.TurnEnd()
	inspect(g, siblings[1], under("run the checks", siblings...), j)
	if n := len(j.calls()); n != 5 {
		t.Fatalf("b's prejudgment did not outlive the turn; a's is made again for the new turn: %d", n)
	}

	g.Wait()
	next.irreversible = 0.9
	g.Hold("run the checks")
	g.Reset()
	next.approved = f(0.99)
	v := inspect(g, bash("c", "git push --force"), under("yes"), j)
	if v.Level != LevelConfirm || askedApproval(j) {
		t.Errorf("after reset there is no hold to approve: %s asked=%v", v.Level, askedApproval(j))
	}
}

// Skipped twin: tests/action-guard.test.ts:201 "the large-output question rides bash requests ..."
func TestSkippedTwinLargeOutputQuestion(t *testing.T) {
	t.Skip("large-output steer (context.largeOutput) is not part of this port: owner decision needed to add it")
}

func TestActionGuardIsSafeUnderConcurrentInspection(t *testing.T) {
	g := NewActionGuard()
	j, _ := newStub()
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			siblings := []ToolCallRef{bash(fmt.Sprintf("a%d", i), "npm test"), bash(fmt.Sprintf("b%d", i), "npm run lint")}
			v := inspect(g, siblings[0], under("run the checks", siblings...), j)
			if v.Level != LevelAllow {
				errs <- fmt.Errorf("level %s", v.Level)
				return
			}
			g.TurnEnd()
			errs <- nil
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
