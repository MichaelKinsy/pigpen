package tintinweb_subagents

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Twins of test/foreground-concurrency.test.ts and test/agent-manager-gc.test.ts: the manager's pools, its queue and
// its record GC. The original mocks the in-process run (runAgent); here the child is a fake one (fleet): its run
// settles when the test says so.

type mgrRig struct {
	t *testing.T
	m *manager
	f *fleet
}

func newMgrRig(t *testing.T, opts ...func(*manager)) *mgrRig {
	t.Helper()
	f := &fleet{}
	m := newManager(f.start, nil)
	for _, fn := range opts {
		fn(m)
	}
	t.Cleanup(func() { m.abortAll(); m.dispose() })
	return &mgrRig{t: t, m: m, f: f}
}

func (r *mgrRig) status(rec *agentRecord) string {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	return rec.Status
}

// started waits until the fleet has started n children.
func (r *mgrRig) started(n int) { r.t.Helper(); r.f.wait(r.t, n) }

// stays checks that no more than n children start (the run is in goroutines: allow them time to prove they do not).
func (r *mgrRig) stays(n int) {
	r.t.Helper()
	time.Sleep(60 * time.Millisecond)
	eq(r.t, r.f.count(), n)
}

func (r *mgrRig) child(prompt string) *fakeChild {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.f.mu.Lock()
		for _, c := range r.f.children {
			if c.spec.Prompt == prompt {
				r.f.mu.Unlock()
				return c
			}
		}
		r.f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("no child started for %q", prompt)
	return nil
}

// settle ends the run of the child started for prompt with prompt+"-result".
func (r *mgrRig) settle(prompt string) { r.t.Helper(); r.child(prompt).finish(prompt + "-result") }

func (r *mgrRig) rec(prompt string) *agentRecord {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range r.m.list() {
			if rec.Description == prompt {
				return rec
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatalf("no record %q", prompt)
	return nil
}

// fg is a blocking spawn, as the Agent tool makes: it returns what spawnAndWait returns, on a channel.
func (r *mgrRig) fg(prompt string, o ...spawnOptions) <-chan *agentRecord {
	var opts spawnOptions
	if len(o) > 0 {
		opts = o[0]
	}
	out := make(chan *agentRecord, 1)
	go func() { out <- r.m.spawnAndWait("general-purpose", prompt, childSpec{Prompt: prompt}, opts) }()
	r.rec(prompt) // the spawn is registered before the next call, so queue order is the call order
	return out
}

// fgNow is the synchronous half of a blocking spawn (the record exists, queued or running, when it returns).
func (r *mgrRig) fgNow(prompt string, o ...spawnOptions) *agentRecord {
	var opts spawnOptions
	if len(o) > 0 {
		opts = o[0]
	}
	opts.Blocking = true
	return r.m.spawnWith("general-purpose", prompt, childSpec{Prompt: prompt}, opts)
}

func (r *mgrRig) bg(prompt string) *agentRecord {
	return r.m.spawnWith("general-purpose", prompt, childSpec{Prompt: prompt}, spawnOptions{Background: true})
}

func await(t *testing.T, ch <-chan *agentRecord) *agentRecord {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never resolved")
		return nil
	}
}

func (r *mgrRig) resultOf(rec *agentRecord) string {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	return rec.Result
}

func TestForegroundConcurrencyTwins(t *testing.T) {
	const f = "foreground-concurrency"
	tw(t, f, "is unlimited by default, and the default path stays synchronous", func(t *testing.T) {
		r := newMgrRig(t)
		eq(t, r.m.getMaxConcurrentForeground(), 0)
		a, b, c := r.fgNow("a"), r.fgNow("b"), r.fgNow("c")
		// Synchronously, not after a wait: a gate, a counter check that waits or a queue entry would defer at least one.
		for _, rec := range []*agentRecord{a, b, c} {
			eq(t, r.status(rec), statusRunning)
		}
		r.started(3)
	})
	tw(t, f, "queues blocking spawns past the limit and starts them as slots free, in order", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		first, second, third := r.fg("a"), r.fg("b"), r.fg("c")
		r.started(1)
		eq(t, [2]string{r.status(r.rec("b")), r.status(r.rec("c"))}, [2]string{statusQueued, statusQueued})
		r.settle("a")
		rec := await(t, first)
		eq(t, [2]string{rec.Status, r.resultOf(rec)}, [2]string{statusCompleted, "a-result"})
		// FIFO: b was queued first, so b runs, not c.
		r.child("b")
		eq(t, [2]string{r.status(r.rec("b")), r.status(r.rec("c"))}, [2]string{statusRunning, statusQueued})
		r.settle("b")
		eq(t, r.resultOf(await(t, second)), "b-result")
		r.settle("c")
		eq(t, r.resultOf(await(t, third)), "c-result")
	})
	tw(t, f, "never queues a nested child, however full the pool is", func(t *testing.T) {
		// The deadlock guard: a nested child's parent is blocked awaiting it, so queueing it behind its own parent can never progress.
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		parent := r.fgNow("parent")
		eq(t, r.status(parent), statusRunning)
		child := r.fgNow("child", spawnOptions{ParentAgentID: parent.ID, Depth: 2})
		eq(t, r.status(child), statusRunning)
		r.started(2)
	})
	tw(t, f, "never queues a workflow's children, which the run already bounds", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		for _, p := range []string{"wf-a", "wf-b", "wf-c"} {
			eq(t, r.status(r.fgNow(p, spawnOptions{WorkflowID: "wf1"})), statusRunning)
		}
		r.started(3)
		// The session's own blocking work is still bounded: the exemption is for the workflow's children, not a hole in the limit.
		eq(t, r.status(r.fgNow("mine-1")), statusRunning)
		eq(t, r.status(r.fgNow("mine-2")), statusQueued)
	})
	tw(t, f, "is independent of the background pool, in both directions", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrent(1)
		r.m.setMaxConcurrentForeground(1)
		bg1, bg2 := r.bg("bg1"), r.bg("bg2")
		eq(t, [2]string{r.status(bg1), r.status(bg2)}, [2]string{statusRunning, statusQueued}) // background pool full
		eq(t, r.status(r.fgNow("fg1")), statusRunning)                                         // a blocking spawn is not charged to the saturated background pool
		eq(t, r.status(r.fgNow("fg2")), statusQueued)                                          // and the saturated foreground pool queues the next
		eq(t, r.status(bg2), statusQueued)                                                     // still only bg1's slot
	})
	tw(t, f, "does not queue a detached spawn, even one flagged isBackground: false", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fgNow("holder")
		rpc := r.m.spawnWith("general-purpose", "rpc", childSpec{Prompt: "rpc"}, spawnOptions{Background: false}) // blocks nobody
		eq(t, r.status(rpc), statusRunning)
	})
	tw(t, f, "starts a bypassQueue spawn at the limit, and still counts its slot", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fgNow("holder")
		eq(t, r.status(r.fgNow("scheduled", spawnOptions{BypassQueue: true})), statusRunning)
		// Counted, not invisible: the pool is now over its limit, so the next ordinary blocking spawn queues.
		eq(t, r.status(r.fgNow("next")), statusQueued)
	})
	tw(t, f, "resolves — never rejects — when a queued agent is aborted", func(t *testing.T) {
		// A rejection would escape into the caller's tool call and reject pi's whole batch of tool calls.
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("holder")
		queued := r.fg("victim")
		victim := r.rec("victim")
		eq(t, r.m.abort(victim), true)
		rec := await(t, queued)
		eq(t, rec.Status, statusStopped)
		eq(t, rec.CompletedAt.IsZero(), false)
		r.settle("holder") // freeing the slot must not resurrect it
		r.stays(1)
	})
	tw(t, f, "releases a queued agent when the caller's signal aborts (Esc)", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("holder")
		ctx, cancel := context.WithCancel(context.Background())
		queued := r.fg("victim", spawnOptions{Signal: ctx.Done()})
		eq(t, r.status(r.rec("victim")), statusQueued)
		cancel()
		eq(t, await(t, queued).Status, statusStopped)
	})
	tw(t, f, "never enqueues a spawn whose signal is already aborted", func(t *testing.T) {
		// A listener never fires on an already-aborted signal, so enqueueing would wait forever.
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("holder")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := await(t, r.fg("victim", spawnOptions{Signal: ctx.Done()}))
		eq(t, rec.Status, statusStopped)
		r.stays(1)
	})
	tw(t, f, "stops an immediate spawn whose signal is already aborted, pool off", func(t *testing.T) {
		// Not gated on the setting: an aborted signal used to leave the agent running beyond the reach of Esc.
		r := newMgrRig(t)
		eq(t, r.m.getMaxConcurrentForeground(), 0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		doomed := r.fgNow("doomed", spawnOptions{Signal: ctx.Done()})
		eq(t, r.status(doomed), statusStopped)
		eq(t, r.child("doomed").abortedNow(), true)
	})
	tw(t, f, "releases queued waiters on abortAll()", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("holder")
		queued := r.fg("victim")
		r.m.abortAll()
		eq(t, await(t, queued).Status, statusStopped)
	})
	tw(t, f, "releases queued waiters on dispose()", func(t *testing.T) {
		// Without this, any failing test of this file would hang the suite instead of failing.
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("holder")
		queued := r.fg("victim")
		r.m.dispose()
		await(t, queued)
	})
	tw(t, f, "frees the slot when a foreground agent fails", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		first := r.fg("a")
		r.fg("b")
		eq(t, r.status(r.rec("b")), statusQueued)
		c := r.child("a")
		c.err = errBoom
		close(c.release)
		eq(t, await(t, first).Status, statusError)
		r.child("b")
		eq(t, r.status(r.rec("b")), statusRunning)
	})
	tw(t, f, "frees the slot exactly once when a running agent is aborted", func(t *testing.T) {
		// Freeing in abort as well as in the settle path is a double-free that permanently lifts the limit: three at a limit of one catches it.
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		first := r.fg("a")
		r.fg("b")
		r.fg("c")
		r.m.abort(r.rec("a"))
		// the aborted run settles on its own, and gives the slot back exactly once
		await(t, first)
		r.child("b")
		eq(t, [2]string{r.status(r.rec("b")), r.status(r.rec("c"))}, [2]string{statusRunning, statusQueued})
	})
	tskip(t, f, "rethrows a drain-time startup failure, and keeps draining", "the startup failure is the strict worktree creation failing (isolation: \"worktree\"), a feature of the worktree slice (G3); this port refuses isolation and has no startup step that can fail while queued")
	tw(t, f, "drains immediately when the limit is raised or cleared", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("a")
		r.fg("b")
		r.fg("c")
		r.started(1)
		r.m.setMaxConcurrentForeground(0) // back to unlimited
		r.started(3)
	})
	tw(t, f, "releases the slot a cleared limit no longer describes", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		a := r.fg("a")
		r.m.setMaxConcurrentForeground(0) // unlimited, while a holds a slot
		r.settle("a")
		await(t, a)
		// The slot must have come back: if it leaked, nothing can free it and every later blocking spawn queues forever.
		r.m.setMaxConcurrentForeground(1)
		eq(t, r.status(r.fgNow("b")), statusRunning)
	})
	tw(t, f, "does not release a slot a newly-set limit was never charged", func(t *testing.T) {
		r := newMgrRig(t)
		a := r.fg("a") // unlimited: takes no slot
		r.m.setMaxConcurrentForeground(1)
		r.settle("a")
		await(t, a)
		// The counter must still be 0, not -1: a -1 silently lifts the limit by one.
		eq(t, [2]string{r.status(r.fgNow("b")), r.status(r.fgNow("c"))}, [2]string{statusRunning, statusQueued})
	})
	tw(t, f, "clamps a negative limit to unlimited rather than to 1", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(-3)
		eq(t, r.m.getMaxConcurrentForeground(), 0)
	})
	tw(t, f, "counts a queued foreground agent as active, and waitForAll waits it out", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		r.fg("a")
		r.fg("b")
		eq(t, r.m.hasRunning(), true)
		done := make(chan struct{})
		go func() { r.m.waitForAll(); close(done) }()
		r.settle("a")
		r.settle("b")
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("waitForAll did not wait the queued agent out")
		}
		eq(t, r.status(r.rec("b")), statusCompleted)
	})
	tw(t, f, "fires each caller's onSpawned hook when its own deferred spawn starts", func(t *testing.T) {
		// A deferred start must reach the caller that queued it, not whatever hook is installed at drain time.
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		var mu sync.Mutex
		var first, second []string
		hook := func(into *[]string) func(string) {
			return func(id string) { mu.Lock(); *into = append(*into, id); mu.Unlock() }
		}
		count := func(l *[]string) int { mu.Lock(); defer mu.Unlock(); return len(*l) }
		a := r.fg("a", spawnOptions{OnSpawned: hook(&first)})
		b := r.fg("b", spawnOptions{OnSpawned: hook(&second)})
		eq(t, [2]int{count(&first), count(&second)}, [2]int{1, 0})
		r.settle("a")
		await(t, a)
		deadline := time.Now().Add(5 * time.Second)
		for count(&second) == 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		mu.Lock()
		eq(t, [2]int{len(first), len(second)}, [2]int{1, 1})
		eq(t, second[0], r.rec("b").ID)
		mu.Unlock()
		r.settle("b")
		await(t, b)
	})
	tw(t, f, "reports how many are ahead when a spawn is queued", func(t *testing.T) {
		r := newMgrRig(t)
		r.m.setMaxConcurrentForeground(1)
		var got [][2]any
		onQueued := func(id string, ahead int) { got = append(got, [2]any{id, ahead}) }
		r.fgNow("holder")
		b := r.fgNow("b", spawnOptions{OnQueued: onQueued})
		c := r.fgNow("c", spawnOptions{OnQueued: onQueued})
		eq(t, got, [][2]any{{b.ID, 0}, {c.ID, 1}})
	})
	tw(t, f, "does not let a full foreground queue head-of-line-block the background pool", func(t *testing.T) {
		// One queue serves both pools: a saturated foreground pool at the head must not stall the background agents behind it.
		r := newMgrRig(t)
		r.m.setMaxConcurrent(1)
		r.m.setMaxConcurrentForeground(1)
		r.fgNow("fg-holder")
		r.fgNow("fg-queued")
		r.bg("bg-holder")
		r.bg("bg-queued")
		eq(t, [2]string{r.status(r.rec("fg-queued")), r.status(r.rec("bg-queued"))}, [2]string{statusQueued, statusQueued})
		r.settle("bg-holder") // free a BACKGROUND slot while the foreground queue is still stuck
		r.child("bg-queued")
		eq(t, [2]string{r.status(r.rec("bg-queued")), r.status(r.rec("fg-queued"))}, [2]string{statusRunning, statusQueued})
	})
}

// abortedNow: whether the child was told to stop.
func (c *fakeChild) abortedNow() bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		a := c.aborted
		c.mu.Unlock()
		if a {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// A clock the GC reads: the original's fake timers move Date.now too.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) get() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

const (
	tenMinutes = 10 * time.Minute
	gcTick     = time.Minute
)

func TestAgentManagerGCTwins(t *testing.T) {
	const f = "agent-manager-gc"
	setup := func(t *testing.T) (*mgrRig, *testClock) {
		clock := &testClock{now: time.Now()}
		r := newMgrRig(t, func(m *manager) { m.stopGCLoop(); m.now = clock.get })
		return r, clock
	}
	// settled spawns a background agent and lets it finish.
	settled := func(r *mgrRig, typ, prompt string) *agentRecord {
		rec := r.m.spawnWith(typ, prompt, childSpec{Prompt: prompt}, spawnOptions{Background: true})
		r.settle(prompt)
		<-rec.done
		return rec
	}
	age := func(r *mgrRig, rec *agentRecord, completedAt time.Time) {
		r.m.mu.Lock()
		rec.CompletedAt = completedAt
		r.m.mu.Unlock()
	}
	exists := func(r *mgrRig, id string) bool { return r.m.get(id) != nil }

	tw(t, f, "keeps a record that completed inside the retention window", func(t *testing.T) {
		r, clock := setup(t)
		rec := settled(r, "X", "recent")
		age(r, rec, clock.get().Add(-(tenMinutes - 2*gcTick)))
		clock.add(gcTick)
		r.m.cleanup()
		eq(t, exists(r, rec.ID), true)
	})
	tw(t, f, "evicts a record that completed before the cutoff and disposes its session", func(t *testing.T) {
		// A Go agent has no session of its own to dispose: its child process was already reaped when it settled.
		r, clock := setup(t)
		rec := settled(r, "X", "stale")
		age(r, rec, clock.get().Add(-(tenMinutes + 30*time.Second)))
		clock.add(gcTick)
		r.m.cleanup()
		eq(t, exists(r, rec.ID), false)
		for _, l := range r.m.list() {
			eq(t, l.ID == rec.ID, false)
		}
	})
	tskip(t, f, "closes the evicted session's extension lifecycle before disposing it (#242)", "an agent here is a child process that exits when it settles; there is no in-process session whose extensions need a session_shutdown before dispose (PORT.md D1)")
	tw(t, f, "never evicts a running agent, however old its timestamp looks", func(t *testing.T) {
		r, clock := setup(t)
		rec := r.bg("live")
		eq(t, r.status(rec), statusRunning)
		age(r, rec, clock.get().Add(-10*tenMinutes))
		clock.add(5 * gcTick)
		r.m.cleanup()
		eq(t, exists(r, rec.ID), true)
	})
	tw(t, f, "never evicts a queued agent", func(t *testing.T) {
		r, clock := setup(t)
		r.m.setMaxConcurrent(1)
		r.bg("holder")
		queued := r.bg("waiter")
		eq(t, r.status(queued), statusQueued)
		age(r, queued, clock.get().Add(-10*tenMinutes))
		clock.add(5 * gcTick)
		r.m.cleanup()
		eq(t, r.status(r.m.get(queued.ID)), statusQueued)
	})
	tw(t, f, "sweeps repeatedly, not just once", func(t *testing.T) {
		clock := &testClock{now: time.Now()}
		r := newMgrRig(t, func(m *manager) { m.stopGCLoop(); m.now = clock.get })
		rec := settled(r, "X", "ages-out")
		age(r, rec, clock.get().Add(-(tenMinutes - 3*gcTick)))
		r.m.startGC(5 * time.Millisecond) // the scheduled sweep, at a pace the test can wait for
		time.Sleep(60 * time.Millisecond)
		eq(t, exists(r, rec.ID), true) // still inside the window
		clock.add(4 * gcTick)
		deadline := time.Now().Add(5 * time.Second)
		for exists(r, rec.ID) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		eq(t, exists(r, rec.ID), false) // aged out on a later tick
	})

	evictable := func(r *mgrRig, clock *testClock, typ, prompt, sessionFile string) *agentRecord {
		rec := settled(r, typ, prompt)
		r.m.mu.Lock()
		rec.SessionFile = sessionFile
		rec.CompletedAt = clock.get().Add(-(tenMinutes + 30*time.Second))
		r.m.mu.Unlock()
		return rec
	}
	tw(t, f, "keeps an evicted agent reachable by name when its session is on disk", func(t *testing.T) {
		r, clock := setup(t)
		rec := evictable(r, clock, "Explore", "audit the RPC path", "/sessions/explore.jsonl")
		clock.add(gcTick)
		r.m.cleanup()
		live, tomb := r.m.resolveMention("explore")
		eq(t, live == nil, true)
		eq(t, *tomb, tombstone{Handle: "explore", ID: rec.ID, Type: "Explore", Description: "audit the RPC path", SessionFile: "/sessions/explore.jsonl", CompletedAt: tomb.CompletedAt})
	})
	tw(t, f, "leaves nothing behind when the session was only ever in memory", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "ephemeral", "")
		r.m.cleanup()
		live, tomb := r.m.resolveMention("explore")
		eq(t, [2]bool{live == nil, tomb == nil}, [2]bool{true, true})
		eq(t, len(r.m.listTombstones()), 0)
	})
	tw(t, f, "holds the evicted handle so a later agent of the same type can't shadow it", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "first", "/sessions/first.jsonl")
		r.m.cleanup()
		next := r.m.spawnWith("Explore", "second", childSpec{Prompt: "second"}, spawnOptions{Background: true})
		eq(t, next.Handle, "explore-2")
		_, tomb := r.m.resolveMention("explore")
		eq(t, tomb != nil, true)
	})
	tw(t, f, "prefers a live agent over a tombstone holding the same name", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "old", "/sessions/old.jsonl")
		r.m.cleanup()
		live := r.m.spawnWith("Explore", "live", childSpec{Prompt: "live"}, spawnOptions{Background: true})
		r.m.mu.Lock()
		live.Handle = "explore"
		r.m.mu.Unlock()
		got, tomb := r.m.resolveMention("explore")
		eq(t, [2]any{got == live, tomb == nil}, [2]any{true, true})
	})
	tw(t, f, "drops the oldest once the cap is reached, keeping the most recent", func(t *testing.T) {
		r, clock := setup(t)
		for i := 0; i < 101; i++ {
			rec := evictable(r, clock, "Explore", "run-"+itoa(i), "/sessions/"+itoa(i)+".jsonl")
			r.m.mu.Lock()
			rec.CompletedAt = clock.get().Add(-(tenMinutes + 101*time.Second - time.Duration(i)*time.Second))
			r.m.mu.Unlock()
		}
		r.m.cleanup()
		eq(t, len(r.m.listTombstones()), 100)
		live, tomb := r.m.resolveMention("explore") // run-0's handle
		eq(t, [2]bool{live == nil, tomb == nil}, [2]bool{true, true})
		_, tomb = r.m.resolveMention("explore-101")
		eq(t, tomb != nil, true)
	})
	tw(t, f, "hands a reclaimed name straight back, numbering nothing", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "first", "/sessions/first.jsonl")
		r.m.spawnWith("Explore", "second", childSpec{Prompt: "second"}, spawnOptions{Background: true})
		r.m.cleanup()
		got := r.m.spawnWith("Explore", "first", childSpec{Prompt: "first-again"}, spawnOptions{Background: true, Reclaim: &reclaim{Handle: "explore", Alias: "auth-audit"}})
		eq(t, [2]string{got.Handle, got.Alias}, [2]string{"explore", "auth-audit"})
	})
	tw(t, f, "ignores a reclaim on a nested child, which has no name to hold", func(t *testing.T) {
		r, _ := setup(t)
		got := r.m.spawnWith("Explore", "child", childSpec{Prompt: "child"}, spawnOptions{ParentAgentID: "parent-id", Reclaim: &reclaim{Handle: "explore", Alias: "auth-audit"}})
		eq(t, [2]string{got.Handle, got.Alias}, [2]string{"", ""})
	})
	tw(t, f, "stops answering a name once its tombstone is dropped", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "done", "/sessions/done.jsonl")
		r.m.cleanup()
		_, tomb := r.m.resolveMention("explore")
		eq(t, tomb != nil, true)
		r.m.dropTombstone("explore")
		live, tomb := r.m.resolveMention("explore")
		eq(t, [2]bool{live == nil, tomb == nil}, [2]bool{true, true})
		eq(t, len(r.m.listTombstones()), 0)
	})
	tw(t, f, "frees a dropped name for the next agent of that type", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "done", "/sessions/done.jsonl")
		r.m.cleanup()
		r.m.dropTombstone("explore")
		next := r.m.spawnWith("Explore", "next", childSpec{Prompt: "next"}, spawnOptions{Background: true})
		eq(t, next.Handle, "explore")
	})
	tw(t, f, "forgets every name when the session ends", func(t *testing.T) {
		r, clock := setup(t)
		evictable(r, clock, "Explore", "prior session", "/sessions/prior.jsonl")
		r.m.cleanup()
		eq(t, len(r.m.listTombstones()), 1)
		r.m.clearCompleted(true)
		eq(t, len(r.m.listTombstones()), 0)
		live, tomb := r.m.resolveMention("explore")
		eq(t, [2]bool{live == nil, tomb == nil}, [2]bool{true, true})
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
