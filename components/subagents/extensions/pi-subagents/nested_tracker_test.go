package pi_subagents

import (
	"io"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Twins of retained-nested-route-tracker, pi-web-session-liveness and result-delivery-ownership.

func waitUntil(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !check() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !check() {
		t.Fatal("condition did not become true before timeout")
	}
}

func hasRetained(s *SubagentState, id string) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	_, ok := s.RetainedForegroundNestedRoutes[id]
	return ok
}

func retainedCount(s *SubagentState) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return len(s.RetainedForegroundNestedRoutes)
}

func writeNestedState(t *testing.T, r nestedRoute, state string, ts float64, step *float64) {
	t.Helper()
	typ := "subagent.nested.updated"
	if state != "running" {
		typ = "subagent.nested.completed"
	}
	child := jm{"id": "nested-child", "parentRunId": r.RootRunID, "depth": 1, "path": []any{jm{"runId": r.RootRunID}}, "state": state, "agent": "worker", "lastUpdate": ts}
	if step != nil {
		child["parentStepIndex"] = *step
		child["path"] = []any{jm{"runId": r.RootRunID, "stepIndex": *step}}
	}
	nev(t, r, typ, ts, r.RootRunID, step, child)
}

func mustRetain(t *testing.T, s *SubagentState, r nestedRoute) {
	t.Helper()
	ok, err := retainLiveForegroundNestedRoute(s, r)
	if err != nil || !ok {
		t.Fatalf("retain: %v %v", ok, err)
	}
}

type fakeWatcher struct {
	mu     sync.Mutex
	closed int
}

func (w *fakeWatcher) Close() error { w.mu.Lock(); w.closed++; w.mu.Unlock(); return nil }
func (w *fakeWatcher) closes() int  { w.mu.Lock(); defer w.mu.Unlock(); return w.closed }

func TestRetainedNestedRouteTrackerTwins(t *testing.T) {
	const f = "retained-nested-route-tracker"
	tw(t, f, "releases retained liveness after the nested descendant completes", func(t *testing.T) {
		nestedTemp(t)
		state := newSubagentState()
		r := trackRoute(t, "retained-root")
		writeNestedState(t, r, "running", 100, nil)
		mustRetain(t, state, r)
		tr := newRetainedNestedRouteTracker(state, retainedNestedRouteTrackerOptions{PollInterval: 10 * time.Millisecond})
		defer tr.Clear()
		tr.Track(r.RootRunID)
		writeNestedState(t, r, "complete", 200, nil)
		waitUntil(t, func() bool { return !hasRetained(state, r.RootRunID) })
	})
	tw(t, f, "keeps the route through the handoff race until the first tracker refresh", func(t *testing.T) {
		nestedTemp(t)
		state := newSubagentState()
		r := trackRoute(t, "handoff-root")
		writeNestedState(t, r, "running", 100, nil)
		mustRetain(t, state, r)
		writeNestedState(t, r, "complete", 200, nil)
		tr := newRetainedNestedRouteTracker(state, retainedNestedRouteTrackerOptions{PollInterval: time.Minute})
		defer tr.Clear()
		tr.Track(r.RootRunID)
		eq(t, hasRetained(state, r.RootRunID), true, "still retained right after track")
		waitUntil(t, func() bool { return !hasRetained(state, r.RootRunID) })
	})
	tw(t, f, "keeps polling after a native watcher error", func(t *testing.T) {
		nestedTemp(t)
		state := newSubagentState()
		r := trackRoute(t, "watch-error-root")
		writeNestedState(t, r, "running", 100, nil)
		mustRetain(t, state, r)
		w := &fakeWatcher{}
		var onError func()
		tr := newRetainedNestedRouteTracker(state, retainedNestedRouteTrackerOptions{PollInterval: 10 * time.Millisecond,
			Watch: func(dir string, notify func(), fail func()) (io.Closer, error) { onError = fail; return w, nil }})
		defer tr.Clear()
		tr.Track(r.RootRunID)
		onError()
		writeNestedState(t, r, "complete", 200, nil)
		waitUntil(t, func() bool { return !hasRetained(state, r.RootRunID) })
	})
	tw(t, f, "refreshes every retained route instead of stopping at the first live one", func(t *testing.T) {
		nestedTemp(t)
		state := newSubagentState()
		live, terminal := trackRoute(t, "multi-live-root"), trackRoute(t, "multi-terminal-root")
		writeNestedState(t, live, "running", 100, nil)
		writeNestedState(t, terminal, "running", 100, nil)
		mustRetain(t, state, live)
		mustRetain(t, state, terminal)
		tr := newRetainedNestedRouteTracker(state, retainedNestedRouteTrackerOptions{PollInterval: 10 * time.Millisecond})
		defer tr.Clear()
		tr.Track(live.RootRunID)
		tr.Track(terminal.RootRunID)
		writeNestedState(t, terminal, "complete", 200, nil)
		waitUntil(t, func() bool { return !hasRetained(state, terminal.RootRunID) })
		eq(t, hasRetained(state, live.RootRunID), true, "the live route stays")
	})
	tw(t, f, "refreshes promptly from native watch events", func(t *testing.T) {
		nestedTemp(t)
		state := newSubagentState()
		r := trackRoute(t, "watched-root")
		writeNestedState(t, r, "running", 100, nil)
		mustRetain(t, state, r)
		w := &fakeWatcher{}
		var notify func()
		tr := newRetainedNestedRouteTracker(state, retainedNestedRouteTrackerOptions{PollInterval: time.Minute,
			Watch: func(dir string, n func(), fail func()) (io.Closer, error) { notify = n; return w, nil }})
		defer tr.Clear()
		tr.Track(r.RootRunID)
		time.Sleep(60 * time.Millisecond) // the first refresh ran and found the descendant live
		eq(t, hasRetained(state, r.RootRunID), true, "live after the first refresh")
		writeNestedState(t, r, "complete", 200, nil)
		notify()
		waitUntil(t, func() bool { return !hasRetained(state, r.RootRunID) })
		waitUntil(t, func() bool { return w.closes() == 1 })
	})
	tw(t, f, "clears every retained route during session teardown", func(t *testing.T) {
		nestedTemp(t)
		state := newSubagentState()
		r := trackRoute(t, "cleared-root")
		writeNestedState(t, r, "running", 100, nil)
		mustRetain(t, state, r)
		tr := newRetainedNestedRouteTracker(state, retainedNestedRouteTrackerOptions{PollInterval: time.Minute})
		tr.Track(r.RootRunID)
		tr.Clear()
		eq(t, retainedCount(state), 0, "retained")
	})
}

func TestPiWebSessionLivenessTwins(t *testing.T) {
	const f = "pi-web-session-liveness"
	tw(t, f, "reports only queued or running async jobs as live", func(t *testing.T) {
		s := newSubagentState()
		s.AsyncJobs["retained"] = &AsyncJobState{Status: "complete"}
		eq(t, hasLiveSubagentWork(s), false, "complete")
		s.AsyncJobs["queued"] = &AsyncJobState{Status: "queued"}
		eq(t, hasLiveSubagentWork(s), true, "queued")
		delete(s.AsyncJobs, "queued")
		s.AsyncJobs["running"] = &AsyncJobState{Status: "running"}
		eq(t, hasLiveSubagentWork(s), true, "running")
	})
	tw(t, f, "keeps a terminal parent live while a nested descendant is active", func(t *testing.T) {
		s := newSubagentState()
		nested := jm{"state": "running"}
		s.AsyncJobs["parent"] = &AsyncJobState{Status: "complete", NestedChildren: []jm{{"state": "complete", "children": []jm{nested}}}}
		eq(t, hasLiveSubagentWork(s), true, "active descendant")
		nested["state"] = "complete"
		eq(t, hasLiveSubagentWork(s), false, "settled descendant")
	})
	tw(t, f, "reports foreground scheduling or active children as live", func(t *testing.T) {
		s := newSubagentState()
		s.ForegroundControls["settled"] = &ForegroundRunRecord{ActiveChildren: map[string]bool{}}
		eq(t, hasLiveSubagentWork(s), false, "settled")
		s.ForegroundControls["scheduling"] = &ForegroundRunRecord{SchedulingOwners: 1, ActiveChildren: map[string]bool{}}
		eq(t, hasLiveSubagentWork(s), true, "scheduling")
		delete(s.ForegroundControls, "scheduling")
		s.ForegroundControls["child"] = &ForegroundRunRecord{ActiveChildren: map[string]bool{"0": true}}
		eq(t, hasLiveSubagentWork(s), true, "active child")
	})
	tw(t, f, "retains a foreground nested route until its async descendant finishes", func(t *testing.T) {
		nestedTemp(t)
		s := newSubagentState()
		r := trackRoute(t, "foreground-root")
		writeNestedState(t, r, "running", 100, fl(0))
		mustRetain(t, s, r)
		eq(t, hasRetained(s, r.RootRunID), true, "retained")
		eq(t, hasLiveSubagentWork(s), true, "live")
		writeNestedState(t, r, "complete", 200, fl(0))
		eq(t, hasLiveSubagentWork(s), true, "the synchronous probe reads only its cached projection")
	})
	tw(t, f, "does not retain foreground nested routes without a host registration", func(t *testing.T) {
		nestedTemp(t)
		s := newSubagentState()
		r := trackRoute(t, "unregistered-foreground-root")
		writeNestedState(t, r, "running", 100, fl(0))
		s.ForegroundControls[r.RootRunID] = &ForegroundRunRecord{RunID: r.RootRunID, ActiveChildren: map[string]bool{}, NestedRoute: &r}
		eq(t, removeForegroundControlIfIdle(s, r.RootRunID, nil), true, "removed")
		eq(t, s.RetainedForegroundNestedRoutes == nil, true, "nothing retained without a tracker")
	})
	tw(t, f, "transfers a live nested route before removing its settled foreground control", func(t *testing.T) {
		nestedTemp(t)
		s := newSubagentState()
		r := trackRoute(t, "settled-foreground-root")
		writeNestedState(t, r, "running", 100, fl(0))
		s.ForegroundControls[r.RootRunID] = &ForegroundRunRecord{RunID: r.RootRunID, ActiveChildren: map[string]bool{}, NestedRoute: &r}
		var tracked string
		eq(t, removeForegroundControlIfIdle(s, r.RootRunID, func(id string) { tracked = id }), true, "removed")
		_, has := s.ForegroundControls[r.RootRunID]
		eq(t, has, false, "control removed")
		eq(t, hasRetained(s, r.RootRunID), true, "route retained")
		eq(t, tracked, r.RootRunID, "tracker told")
	})
	tw(t, f, "does not retain foreground routes without live descendants", func(t *testing.T) {
		nestedTemp(t)
		s := newSubagentState()
		r := trackRoute(t, "terminal-foreground-root")
		writeNestedState(t, r, "complete", 100, fl(0))
		ok, err := retainLiveForegroundNestedRoute(s, r)
		eq(t, ok, false, "retained")
		eq(t, err, nil, "error")
		eq(t, s.RetainedForegroundNestedRoutes == nil, true, "map untouched")
	})
	tskip(t, f, "is a no-op outside pi-web or with an incompatible protocol", "registerPiWebSessionLiveness looks up pi-web's registry through globalThis[Symbol.for('@agegr/pi-web/session-liveness/v1')], a property of the Node process that hosts Pi and pi-web; a pig extension process shares no JavaScript global with any host, so there is no registry to find or to be incompatible")
	tskip(t, f, "registers exact session aliases and returns the host release function", "the same pi-web registry: register({name: 'pi-subagents', sessionId, sessionFile, isActive}) is called on a registry object found on globalThis; no such object exists across pig's process boundary (the part that answers isActive, hasLiveSubagentWork, is twinned above)")
}

func TestResultDeliveryOwnershipTwins(t *testing.T) {
	const f = "result-delivery-ownership"
	const owner = "owner-a"
	tw(t, f, "claims only the directly proven previous runtime session", func(t *testing.T) {
		cur := "old"
		o := newResultDeliveryOwnership(func() (string, string) { return cur, owner })
		eq(t, o.ClaimPredecessor("", "old"), false, "no file")
		eq(t, o.ClaimPredecessor("different", "old"), false, "different file")
		eq(t, o.ClaimPredecessor("old", "different"), false, "different runtime id")
		eq(t, o.ClaimPredecessor("old", "old"), true, "proven")
		cur = "new"
		eq(t, o.Owns("old", owner), true, "claimed predecessor")
		eq(t, o.Owns("new", owner), true, "current")
		eq(t, o.Owns("old", "other-owner"), false, "other owner")
		eq(t, o.Owns("foreign", owner), false, "foreign session")
	})
	tw(t, f, "keeps predecessor claims bounded", func(t *testing.T) {
		cur := "session-0"
		o := newResultDeliveryOwnership(func() (string, string) { return cur, owner })
		for i := range 9 {
			prev := "session-" + strconv.Itoa(i)
			eq(t, o.ClaimPredecessor(prev, prev), true, prev)
			cur = "session-" + strconv.Itoa(i+1)
		}
		eq(t, o.ClaimedSessionIDs(), []string{"session-1", "session-2", "session-3", "session-4", "session-5", "session-6", "session-7", "session-8"}, "claims")
		eq(t, o.Owns("session-0", owner), false, "evicted")
		eq(t, o.Owns("session-8", owner), true, "kept")
	})
}
