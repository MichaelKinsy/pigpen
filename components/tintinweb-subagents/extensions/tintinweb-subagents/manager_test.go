package tintinweb_subagents

import (
	"testing"
	"time"
)

func TestQueueBeyondTheConcurrencyLimit(t *testing.T) {
	r := startRig(t)
	r.app.mgr.maxConcurrent = 1
	first := r.app.mgr.spawn("general-purpose", "one", "", childSpec{Prompt: "1"}, true)
	second := r.app.mgr.spawn("general-purpose", "two", "", childSpec{Prompt: "2"}, true)
	third := r.app.mgr.spawn("general-purpose", "three", "", childSpec{Prompt: "3"}, true)
	cs := r.fleet.wait(t, 1)
	eq(t, first.Status, statusRunning)
	eq(t, second.Status, statusQueued)
	eq(t, third.Status, statusQueued)
	time.Sleep(50 * time.Millisecond)
	eq(t, r.fleet.count(), 1)
	// A queued agent can be stopped before it ever starts.
	eq(t, r.app.mgr.abort(third), true)
	eq(t, third.Status, statusStopped)
	// The first finishing starts the second, in order.
	cs[0].finish("a")
	cs = r.fleet.wait(t, 2)
	eq(t, cs[1].spec.Prompt, "2")
	cs[1].finish("b")
	<-second.done
	time.Sleep(50 * time.Millisecond)
	eq(t, r.fleet.count(), 2) // the stopped one never started
	eq(t, r.app.mgr.running, 0)
}

func TestForegroundAgentsAreNotQueued(t *testing.T) {
	r := startRig(t)
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.spawn("general-purpose", "bg", "", childSpec{}, true)
	fg := r.app.mgr.spawn("general-purpose", "fg", "", childSpec{}, false)
	r.fleet.wait(t, 2)
	eq(t, fg.Status, statusRunning)
}

func TestResolveByIDPrefixAndName(t *testing.T) {
	r := startRig(t)
	a := r.app.mgr.spawn("Explore", "a", "alpha", childSpec{}, true)
	r.fleet.wait(t, 1)
	eq(t, r.app.mgr.resolve(a.ID), a)
	eq(t, r.app.mgr.resolve(a.ID[:6]), a)
	eq(t, r.app.mgr.resolve("alpha"), a)
	eq(t, r.app.mgr.resolve("zzz") == nil, true)
}

func TestAbortAll(t *testing.T) {
	r := startRig(t)
	a := r.app.mgr.spawn("Explore", "a", "", childSpec{}, true)
	r.fleet.wait(t, 1)
	r.app.mgr.abortAll()
	<-a.done
	eq(t, a.Status, statusStopped)
	eq(t, r.app.mgr.abort(a), false)
}

func TestAnAgentThatErrorsRecordsIt(t *testing.T) {
	r := startRig(t)
	r.fleet.setFail(errBoom)
	a := r.app.mgr.spawn("Explore", "a", "", childSpec{}, true)
	<-a.done
	eq(t, a.Status, statusError)
	eq(t, a.Error, "boom")
}
