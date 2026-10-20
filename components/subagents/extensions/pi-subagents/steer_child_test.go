package pi_subagents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests of the runner side of steering (steer_child.go) against a fake child session: the consumer's own behavior, which the
// original's tests reach through its in-process runner.

type fakeChild struct {
	mu        sync.Mutex
	subs      []func(childEvent)
	steerErr  error
	followErr error
	steers    []string
	followUps []string
}

func (f *fakeChild) Subscribe(fn func(childEvent)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fn)
	return func() {}
}
func (f *fakeChild) Prompt(string) error { return nil }
func (f *fakeChild) Steer(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.steerErr != nil {
		return f.steerErr
	}
	f.steers = append(f.steers, m)
	return nil
}
func (f *fakeChild) FollowUp(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.followErr != nil {
		return f.followErr
	}
	f.followUps = append(f.followUps, m)
	return nil
}
func (f *fakeChild) Abort() error               { return nil }
func (f *fakeChild) HasQueuedMessages() bool    { return false }
func (f *fakeChild) Messages() []map[string]any { return nil }
func (f *fakeChild) SessionFile() string        { return "" }
func (f *fakeChild) SessionID() string          { return "" }
func (f *fakeChild) Dispose() error             { return nil }
func (f *fakeChild) emit(ev childEvent) {
	f.mu.Lock()
	subs := append([]func(childEvent){}, f.subs...)
	f.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
}
func (f *fakeChild) userMessage(text string) {
	f.emit(childEvent{"type": "message_end", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}})
}
func (f *fakeChild) steerCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.steers) }

func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type scEnv struct {
	saEnv
	runID string
	cfg   *RunConfig
	child *fakeChild
}

func newScEnv(t *testing.T, runID string) scEnv {
	t.Helper()
	e := newSaEnv(t)
	st := saStatus(runID, "single", 1)
	saWrite(t, e.dir(runID), st)
	return scEnv{saEnv: e, runID: runID, cfg: &RunConfig{ID: runID, AsyncDir: e.dir(runID), SessionID: "session"}, child: &fakeChild{}}
}

func (e scEnv) ledger(t *testing.T) *steeringStatus {
	t.Helper()
	st, err := readStatus(e.dir(e.runID))
	if err != nil || st == nil {
		t.Fatalf("status: %v", err)
	}
	l := statusSteering(st)
	if l == nil {
		t.Fatal("no steering ledger")
	}
	return l
}

func (e scEnv) targetState(t *testing.T, id string) string {
	t.Helper()
	req := findSteeringRequest(e.ledger(t), id)
	if req == nil || len(req.Targets) == 0 {
		return ""
	}
	return req.Targets[0].State
}

func (e scEnv) events(t *testing.T) []map[string]any {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(e.dir(e.runID), "events.jsonl"))
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func eventTypes(evs []map[string]any) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e["type"].(string))
	}
	return out
}

func TestSteerConsumerDelivery(t *testing.T) {
	e := newScEnv(t, "consume-delivered")
	stop := consumeSteerRequestsForRun(e.cfg, e.child)
	defer stop()
	if _, err := requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "r1", Message: "go left", TargetIndexes: []int{0}, HasTargetIndexes: true}); err != nil {
		t.Fatal(err)
	}
	waitCond(t, "queued", func() bool { return e.targetState(t, "r1") == "queued" })
	equal(t, e.child.steers, []string{formatSteerMessage(steerRequest{Message: "go left"})}, "steer text")
	e.child.userMessage(formatSteerMessage(steerRequest{Message: "go left"}))
	waitCond(t, "delivered", func() bool { return e.targetState(t, "r1") == "delivered" })
	l := e.ledger(t)
	equal(t, [3]int{l.Requested, l.Pending, l.Delivered}, [3]int{1, 0, 1}, "counts")
	st, _ := readStatus(e.dir(e.runID))
	equal(t, stepSteering(&st.Steps[0]).Recent[0].Targets[0].State, "delivered", "step ledger")
	types := eventTypes(e.events(t))
	for _, want := range []string{"subagent.steer.requested", "subagent.steer.routed", "subagent.steer.queued", "subagent.steer.delivered"} {
		found := false
		for _, ty := range types {
			found = found || ty == want
		}
		if !found {
			t.Errorf("no %s event in %v", want, types)
		}
	}
}

func TestSteerConsumerFollowUpUsesFollowUp(t *testing.T) {
	e := newScEnv(t, "consume-follow-up")
	stop := consumeSteerRequestsForRun(e.cfg, e.child)
	defer stop()
	requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "f1", Message: "later", Mode: steerModeFollowUp, TargetIndexes: []int{0}, HasTargetIndexes: true})
	waitCond(t, "queued", func() bool { return e.targetState(t, "f1") == "queued" })
	equal(t, len(e.child.followUps), 1, "follow ups")
	equal(t, e.child.steerCount(), 0, "steers")
	polMatches(t, e.child.followUps[0], "^Queued follow-up from the parent orchestrator:\n\nlater\n\n")
}

func TestSteerConsumerRejection(t *testing.T) {
	e := newScEnv(t, "consume-rejected")
	e.child.steerErr = errors.New("transport rejected steer")
	stop := consumeSteerRequestsForRun(e.cfg, e.child)
	defer stop()
	requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "x1", Message: "go", TargetIndexes: []int{0}, HasTargetIndexes: true})
	waitCond(t, "failed", func() bool { return e.targetState(t, "x1") == "failed" })
	req := findSteeringRequest(e.ledger(t), "x1")
	equal(t, req.Targets[0].Reason, "transport rejected steer", "reason")
	st, _ := readStatus(e.dir(e.runID))
	equal(t, st.ActivityState, "needs_attention", "attention")
	var notice map[string]any
	for _, ev := range e.events(t) {
		if ev["type"] == "subagent.steering.notice" {
			notice = ev
		}
	}
	if notice == nil || notice["state"] != "failed" || notice["currentSessionId"] != "session" {
		t.Fatalf("failure notice: %v", notice)
	}
}

func TestSteerConsumerLateAcknowledgement(t *testing.T) {
	e := newScEnv(t, "consume-late")
	stop := consumeSteerRequestsForRun(e.cfg, e.child)
	defer stop()
	requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "l1", Message: "go", TargetIndexes: []int{0}, HasTargetIndexes: true})
	waitCond(t, "queued", func() bool { return e.targetState(t, "l1") == "queued" })
	if _, _, err := claimSteeringRecovery(e.dir(e.runID), steeringClaim{RequestID: "l1", SourceRunID: e.runID, CommittedAt: 1}); err != nil {
		t.Fatal(err)
	}
	e.child.userMessage(formatSteerMessage(steerRequest{Message: "go"}))
	waitCond(t, "late", func() bool { return e.targetState(t, "l1") == "late" })
	req := findSteeringRequest(e.ledger(t), "l1")
	equal(t, req.Targets[0].Reason, "acknowledged after recovery commit", "reason")
}

func TestSteerConsumerShutdownFailsUnconsumed(t *testing.T) {
	e := newScEnv(t, "consume-shutdown")
	stop := consumeSteerRequestsForRun(e.cfg, e.child)
	requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "u1", Message: "go", TargetIndexes: []int{0}, HasTargetIndexes: true})
	waitCond(t, "queued", func() bool { return e.targetState(t, "u1") == "queued" })
	// the run ends: its supervisor writes the terminal status, then stops the consumer
	st, _ := readStatus(e.dir(e.runID))
	st.State = "complete"
	st.EndedAt = new(ms(nowMs()))
	writeStatus(e.dir(e.runID), st)
	// a request that arrives after the end is refused by the closed inbox, one already in it is failed
	if _, err := requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "u2", Message: "too late", TargetIndexes: []int{0}, HasTargetIndexes: true}); err != nil {
		t.Fatal(err)
	}
	stop()
	equal(t, ccExists(steerInboxClosedPath(e.dir(e.runID))), true, "inbox closed")
	_, err := requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "u3", Message: "never", TargetIndexes: []int{0}, HasTargetIndexes: true})
	hasMatch(t, err, "no longer accepts steering")
	req := findSteeringRequest(e.ledger(t), "u1")
	equal(t, [2]string{req.Targets[0].State, req.Targets[0].Reason}, [2]string{"failed", "child completed before consuming steering"}, "unconsumed")
	late := findSteeringRequest(e.ledger(t), "u2")
	if late != nil {
		equal(t, late.Targets[0].State, "failed", "request after the end")
		polMatches(t, late.Targets[0].Reason, "run became complete before steering request was consumed")
	}
	notices := 0
	for _, ev := range e.events(t) {
		if ev["type"] == "subagent.steering.notice" && ev["state"] == "failed" {
			notices++
		}
	}
	if notices == 0 {
		t.Error("no failure notice")
	}
}

func TestSteerConsumerRefusesAfterRunEnded(t *testing.T) {
	e := newScEnv(t, "consume-ended")
	st, _ := readStatus(e.dir(e.runID))
	st.State = "failed"
	writeStatus(e.dir(e.runID), st)
	requestAsyncSteer(e.dir(e.runID), steerPayload{ID: "e1", Message: "go", TargetIndexes: []int{0}, HasTargetIndexes: true})
	stop := consumeSteerRequestsForRun(e.cfg, e.child)
	defer stop()
	waitCond(t, "failed", func() bool { return e.targetState(t, "e1") == "failed" })
	req := findSteeringRequest(e.ledger(t), "e1")
	equal(t, req.Targets[0].Reason, "run became failed before steering request was consumed", "reason")
	equal(t, e.child.steerCount(), 0, "no steer reached the child")
}
