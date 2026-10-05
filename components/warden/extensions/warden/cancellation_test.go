package warden

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Cancellation before, during and after the work (COMMON-DEFECTS 2): a cancelled tool call never hangs the agent
// and never lets a dangerous call through because the judge could not answer.

func TestInspectWithAnAlreadyCancelledContextStillHoldsAForcePush(t *testing.T) {
	g := NewActionGuard()
	j, _ := newStub()
	j.gate = make(chan struct{}) // a judge that would wait forever
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan Verdict, 1)
	go func() {
		done <- g.Inspect(ctx, bash("a", "git push --force origin main"), under("tidy the readme"), opts(j))
	}()
	select {
	case v := <-done:
		if v.Level != LevelConfirm {
			t.Fatalf("the offline patterns decide when the judge cannot answer: %s", v.Level)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Inspect hung on a cancelled context")
	}
	close(j.gate)
}

func TestInspectCancelledWhileTheJudgeIsThinkingFallsBackToThePatterns(t *testing.T) {
	g := NewActionGuard()
	j, _ := newStub()
	j.gate = make(chan struct{})
	defer close(j.gate)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Verdict, 1)
	go func() {
		done <- g.Inspect(ctx, bash("a", "rm -rf ~/projects"), under("clean the build output"), opts(j))
	}()
	waitFor(t, func() bool { return len(j.calls()) > 0 })
	cancel()
	select {
	case v := <-done:
		if v.Level != LevelConfirm {
			t.Fatalf("a destructive call stays held when the judge is cancelled mid-flight: %s", v.Level)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Inspect did not return after cancellation")
	}
}

func TestInspectCancelledAfterItReturnedChangesNothing(t *testing.T) {
	g := NewActionGuard()
	j, _ := newStub()
	ctx, cancel := context.WithCancel(context.Background())
	v := g.Inspect(ctx, bash("a", "npm test"), under("run the tests"), opts(j))
	cancel()
	if v.Level != LevelAllow {
		t.Fatalf("%s", v.Level)
	}
	if v2 := g.Inspect(context.Background(), bash("b", "npm test"), under("run the tests"), opts(j)); v2.Level != LevelAllow {
		t.Fatalf("the guard is still usable: %s", v2.Level)
	}
}

func TestSiblingJudgmentsAreBoundedByTheTimeoutAndNoGoroutineLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	g := NewActionGuard()
	j, _ := newStub()
	j.gate = make(chan struct{}) // never opened: only the timeout ends the judgments
	o := opts(j)
	o.Timeout = 60 * time.Millisecond
	siblings := []ToolCallRef{bash("a", "npm test"), bash("b", "npm run lint"), bash("c", "npm run build")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.Inspect(ctx, siblings[0], under("run the checks", siblings...), o); close(done) }()
	waitFor(t, func() bool { return len(j.calls()) >= 3 })
	cancel() // the parent is cancelled; the siblings' judgments were started for later calls and end at their timeout
	<-done
	finished := make(chan struct{})
	go func() { g.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("sibling judgments outlive their timeout")
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before+1 })
}

func TestALateSiblingResultIsDiscardedAfterTheTurnEnds(t *testing.T) {
	g := NewActionGuard()
	j, _ := newStub()
	j.gate = make(chan struct{})
	siblings := []ToolCallRef{bash("a", "npm test"), bash("b", "npm run lint")}
	done := make(chan Verdict, 1)
	go func() { done <- inspect(g, siblings[0], under("run the checks", siblings...), j) }()
	waitFor(t, func() bool { return len(j.calls()) == 2 }) // b's prejudgment and a's own request
	g.TurnEnd()                                            // b's prejudgment is still in flight when the turn ends
	close(j.gate)
	<-done
	g.Wait()
	j.mu.Lock()
	j.gate = nil
	j.mu.Unlock()
	if v := inspect(g, siblings[1], under("run the checks", siblings...), j); v.Level != LevelAllow {
		t.Fatalf("%s", v.Level)
	}
	g.Wait()
	lint := 0
	for _, r := range j.calls() {
		if actionOf(r) == "npm run lint" {
			lint++
		}
	}
	if lint != 2 {
		t.Fatalf("b is judged once before the turn ended (late) and once again fresh; the late result must not be reused: %d judgments", lint)
	}
}

// Error paths of the configuration file (COMMON-DEFECTS 4): each branch, with its cause kept.

func TestLoadConfigErrors(t *testing.T) {
	dir := t.TempDir()
	if cfg, err := LoadConfig(filepath.Join(dir, "missing.json")); err != nil || cfg.Enabled {
		t.Fatalf("a missing file is the (off) defaults: %v %v", cfg.Enabled, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(bad)
	if err == nil {
		t.Fatal("a corrupt file is an error the caller shows")
	}
	if cfg.Enabled {
		t.Fatal("a corrupt file must never turn warden on")
	}
	if _, err := LoadConfig(dir); err == nil { // a directory where a file should be
		t.Fatal("reading a directory is an error, not the defaults")
	}
}

func TestSaveConfigErrorsKeepTheirCause(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The parent of the config directory is a file, so the directory cannot be created.
	if err := SaveConfig(filepath.Join(blocker, "sub", "config.json"), DefaultConfig()); err == nil {
		t.Fatal("expected an error")
	}
	// The destination is a directory, so the rename fails and no temporary file is left behind.
	target := filepath.Join(dir, "pigpen-warden", "config.json")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(target, DefaultConfig()); err == nil {
		t.Fatal("expected an error renaming onto a directory")
	}
	if _, err := os.Stat(target + ".tmp"); err == nil {
		t.Fatal("a temporary file remains after a failed rename")
	}
	// A successful save is owner-only.
	ok := filepath.Join(dir, "ok", "config.json")
	if err := SaveConfig(ok, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(ok); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
}

// A prejudgment started for a later sibling belongs to the turn, not to the tool call that started it: cancelling
// that call's context must not turn the sibling's judgment into a pattern fallback.
func TestASiblingJudgmentSurvivesTheCancellationOfTheCallThatStartedIt(t *testing.T) {
	g := NewActionGuard()
	j, next := newStub()
	j.gate = make(chan struct{})
	siblings := []ToolCallRef{bash("a", "npm test"), bash("b", "npm run lint")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.Inspect(ctx, siblings[0], under("run the checks", siblings...), opts(j)); close(done) }()
	waitFor(t, func() bool { return len(j.calls()) == 2 })
	next.irreversible = 0.95 // what the judge will say about both once it answers
	cancel()
	<-done
	close(j.gate)
	g.Wait()
	j.mu.Lock()
	j.gate = nil
	j.mu.Unlock()
	v := g.Inspect(context.Background(), siblings[1], under("run the checks", siblings...), opts(j))
	if v.Level != LevelConfirm {
		t.Fatalf("b's judgment (irreversible 0.95) was lost when a's context was cancelled: %s", v.Level)
	}
}

func TestRedactHidesABareBearerTokenLikeTheOriginal(t *testing.T) {
	// Expected value taken from the pinned original: got Bearer [redacted] in the log.
	got := Redact("got Bearer abcdefghij1234567890XYZ in the log")
	if got != "got Bearer [redacted] in the log" {
		t.Fatalf("%q", got)
	}
}

func TestAPromptNeverAuthorizesTheDangerousRmTargetEvenWithAMatchingScope(t *testing.T) {
	v := violation{id: "rm-recursive-dangerous-target", severity: SeverityDestructive, scope: violationScope{paths: []string{"/home/x/projects"}}}
	if authorize("please delete /home/x/projects", v) {
		t.Fatal("rm-recursive-dangerous-target must never be released by a prompt")
	}
	ok := violation{id: "rm-recursive", severity: SeverityDestructive, scope: violationScope{paths: []string{"/home/x/projects"}}}
	if !authorize("please delete /home/x/projects", ok) {
		t.Fatal("the control: an ordinary rm with a matching scope is released")
	}
}
