package tintinweb_subagents

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// What keeps a chain of agents from growing without bound, and what keeps a panic from stranding an owner.

func TestNoChainOfProcessesIsLongerThanTheCeiling(t *testing.T) {
	// Whatever cap the setting or the environment names, no agent past the ceiling is given the means to start another.
	eq(t, childSpec{Allowed: "all", Depth: subagentDepthCeiling - 1, MaxDepth: 99}.nestedActive(), true)
	eq(t, childSpec{Allowed: "all", Depth: subagentDepthCeiling, MaxDepth: 99}.nestedActive(), false)
	t.Setenv(depthEnv, "3")
	t.Setenv(maxDepthEnv, "1000000")
	t.Setenv(allowedEnv, `"all"`)
	n := nestedConfigFromEnv()
	eq(t, n.MaxDepth, subagentDepthCeiling)
	t.Setenv(depthEnv, "16")
	eq(t, nestedConfigFromEnv().toolsEnabled(), false)
	// And the call itself is refused at the cap, tools or not.
	r, _ := scoutRig(t)
	r.app.nested.Depth, r.app.nested.MaxDepth = subagentDepthCeiling, subagentDepthCeiling
	o := nestedExec(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p"}, nil).must(t)
	eq(t, o.IsError, true)
	eq(t, r.fleet.count(), 0)
}

// panicChild is a child whose wait panics.
type panicChild struct{ *fakeChild }

func (panicChild) wait() (*childResult, error) { panic("boom") }

func TestAPanicInAnAgentsRunSettlesItAndFreesItsSlot(t *testing.T) {
	r := startRig(t)
	r.app.mgr.maxConcurrent = 1
	var calls atomic.Int32
	real := r.app.mgr.start
	r.app.mgr.start = func(ctx context.Context, spec childSpec) (child, error) {
		if calls.Add(1) == 1 {
			return panicChild{&fakeChild{release: make(chan struct{})}}, nil
		}
		return real(ctx, spec)
	}
	first := r.app.mgr.spawn("general-purpose", "first", "", childSpec{Prompt: "first"}, true)
	second := r.app.mgr.spawn("general-purpose", "second", "", childSpec{Prompt: "second"}, true)
	settled(t, first)
	eq(t, first.Status, statusError)
	eq(t, first.Error, "internal error in the agent run")
	// The queue moved on: the second agent started in the freed slot, and the count is right.
	c := r.fleet.waitPrompt(t, "second")
	eq(t, second.Status, statusRunning)
	c.finish("ok")
	settled(t, second)
	eq(t, second.Status, statusCompleted)
	eq(t, r.app.mgr.runningBackground, 0)
}

// A notice that was shown is not shown again for the same text, so a session that restarts or changes directory does not
// repeat it. (A mutant of the refusal path survived without it.)
func TestAnUnportedSettingIsAnnouncedOncePerText(t *testing.T) {
	r := settingsRig(t, `{"showCost": true}`)
	eq(t, len(notices(r)), 1)
	r.Fire("session_start", obj{"reason": "resume"})
	r.Fire("session_start", obj{"reason": "resume"})
	eq(t, len(notices(r)), 1)
}

// A parent that settles stops what it owns and nothing another agent owns. (A mutant that stopped every owned record
// survived without it.)
func TestASettledOwnerStopsOnlyItsOwnChildren(t *testing.T) {
	r := startRig(t)
	a := r.app.mgr.spawn("general-purpose", "a", "", childSpec{Prompt: "a"}, true)
	b := r.app.mgr.spawn("general-purpose", "b", "", childSpec{Prompt: "b"}, true)
	ofA := r.app.mgr.spawnOwned("scout", "of-a", "", childSpec{Prompt: "of-a"}, true, &owner{ParentID: a.ID, Depth: 2, MaxDepth: 3})
	ofB := r.app.mgr.spawnOwned("scout", "of-b", "", childSpec{Prompt: "of-b"}, true, &owner{ParentID: b.ID, Depth: 2, MaxDepth: 3})
	r.fleet.wait(t, 4)
	r.fleet.waitPrompt(t, "a").finish("done")
	settled(t, a)
	settled(t, ofA)
	eq(t, ofA.Status, statusStopped)
	r.app.mgr.mu.Lock()
	status := ofB.Status
	r.app.mgr.mu.Unlock()
	eq(t, status, statusRunning) // b's child is not a's to stop
	r.fleet.waitPrompt(t, "b").finish("done")
	settled(t, b)
	settled(t, ofB)
}

// Whoever is released by "started" finds a child that could not start already failed (the nested Agent tool answers a
// failed background start as a tool error). The manager's lock is held while the failed start returns, so the order of the
// two steps is the test's, not the scheduler's: "started" must not be announced before the failure is recorded.
func TestAChildThatCouldNotStartIsAFailureWhenItsStartIsAnnounced(t *testing.T) {
	r := startRig(t)
	m := r.app.mgr
	reached, proceed := make(chan struct{}), make(chan struct{})
	m.start = func(context.Context, childSpec) (child, error) {
		close(reached)
		<-proceed
		return nil, errBoom
	}
	rec := m.spawnOwned("scout", "x", "", childSpec{Prompt: "x"}, true, &owner{ParentID: "p", Depth: 2, MaxDepth: 3})
	<-reached
	m.mu.Lock()
	close(proceed)
	select {
	case <-rec.started:
		m.mu.Unlock()
		t.Fatal("the start was announced before the failure was recorded")
	case <-time.After(300 * time.Millisecond):
	}
	m.mu.Unlock()
	if err := m.awaitStartup(rec, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	status, _, errText := m.snapshot(rec)
	eq(t, []string{status, errText}, []string{statusError, "boom"})
}
