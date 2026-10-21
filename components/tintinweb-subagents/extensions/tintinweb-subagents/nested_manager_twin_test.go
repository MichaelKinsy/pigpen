package tintinweb_subagents

import (
	"sync"
	"testing"
	"time"
)

// Twins of the agent-manager.test.ts cases about owned children, depth and the concurrency pool. The original mocks
// runAgent; these run the real manager over a fake process fleet. The rest of that file stays deferred (slices.json).

const managerFile = "agent-manager"

// settled waits for a record to settle.
func settled(t *testing.T, rec *agentRecord) {
	t.Helper()
	select {
	case <-rec.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("agent %s did not settle", rec.ID)
	}
}

func TestAgentManagerNestedTwins(t *testing.T) {
	tw(t, managerFile, "stores nesting metadata and passes the owning manager/runtime to runAgent", func(t *testing.T) {
		r := startRig(t)
		rec := r.app.mgr.spawnOwned("scout", "nested", "", childSpec{Prompt: "nested"}, true,
			&owner{ParentID: "parent-1", Depth: 2, MaxDepth: 3, ConfigCwd: "/trusted/config"})
		c := r.fleet.wait(t, 1)[0]
		eq(t, rec.Depth, 2)
		eq(t, rec.ParentAgentID, "parent-1")
		eq(t, rec.MaxSubagentDepth, 3)
		// The runner is given the nested runtime: the agent's own id as the owner of its children, its depth and cap, the config root.
		eq(t, c.spec.AgentID, rec.ID)
		eq(t, c.spec.Depth, 2)
		eq(t, c.spec.MaxDepth, 3)
		eq(t, c.spec.ConfigCwd, "/trusted/config")
		c.finish("x")
		settled(t, rec)
	})

	tw(t, managerFile, "tells the runner which spawns are nested, so only top-level ones persist", func(t *testing.T) {
		// rememberAgents persists a top-level agent's session; a nested one is addressable by nothing later, so it stays in memory.
		r, _ := scoutRig(t)
		top := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "nested"}, nil)
		child := r.fleet.waitPrompt(t, "nested")
		eq(t, child.spec.Persist, false)
		child.finish("x")
		recv(t, top)
		r.must("Agent", obj{"prompt": "top", "description": "d", "subagent_type": "scout"})
		eq(t, r.fleet.waitPrompt(t, "top").spec.Persist, true)
	})

	tw(t, managerFile, "defaults top-level subagents to depth one", func(t *testing.T) {
		r := startRig(t)
		rec := r.app.mgr.spawn("scout", "top", "", childSpec{Prompt: "top"}, true)
		c := r.fleet.wait(t, 1)[0]
		eq(t, rec.Depth, 1)
		eq(t, c.spec.Depth, 1)
		eq(t, c.spec.AgentID, rec.ID)
		eq(t, rec.topLevel(), true)
		c.finish("x")
		settled(t, rec)
	})

	tw(t, managerFile, "starts a nested background child even when the concurrency pool is full", func(t *testing.T) {
		// A parent holding the only slot and waiting on its own child would otherwise deadlock.
		r := startRig(t)
		r.app.mgr.maxConcurrent = 1
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		child := r.app.mgr.spawnOwned("scout", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		// A second top-level background agent still queues: the pool is untouched.
		sibling := r.app.mgr.spawn("general-purpose", "sibling", "", childSpec{Prompt: "sibling"}, true)
		r.fleet.wait(t, 2)
		eq(t, child.Status, statusRunning)
		eq(t, sibling.Status, statusQueued)
	})

	tw(t, managerFile, "aborts owned children when the parent settles", func(t *testing.T) {
		r := startRig(t)
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		child := r.app.mgr.spawnOwned("scout", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 4})
		grandchild := r.app.mgr.spawnOwned("scout", "grandchild", "", childSpec{Prompt: "grandchild"}, true, &owner{ParentID: child.ID, Depth: 3, MaxDepth: 4})
		r.fleet.wait(t, 3)
		r.fleet.waitPrompt(t, "parent").finish("done")
		settled(t, parent)
		// The child's own settle path stops the generation below it.
		settled(t, child)
		eq(t, child.Status, statusStopped)
		settled(t, grandchild)
		eq(t, grandchild.Status, statusStopped)
	})

	tw(t, managerFile, "aborts children spawned during a resumed turn", func(t *testing.T) {
		// The spawn settle path already ran, so only the settle of the resumed turn can stop what it launched.
		r := startRig(t)
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		c := r.fleet.waitPrompt(t, "parent")
		c.finish("done")
		settled(t, parent)
		eq(t, r.app.mgr.resume(parent, "keep going", true, ""), true)
		for i := 0; i < 400; i++ {
			c.mu.Lock()
			n := len(c.resumed)
			c.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		child := r.app.mgr.spawnOwned("scout", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		r.fleet.waitPrompt(t, "child")
		c.finish("resumed")
		settled(t, child)
		eq(t, child.Status, statusStopped)
	})

	tw(t, managerFile, "a nested child settling does not free a pool slot it never held", func(t *testing.T) {
		r := startRig(t)
		r.app.mgr.maxConcurrent = 1
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		child := r.app.mgr.spawnOwned("scout", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		sibling := r.app.mgr.spawn("general-purpose", "sibling", "", childSpec{Prompt: "sibling"}, true)
		eq(t, sibling.Status, statusQueued)
		r.fleet.wait(t, 2)
		r.fleet.waitPrompt(t, "child").finish("done")
		settled(t, child)
		// The parent still holds the only slot, so the sibling must stay queued.
		time.Sleep(50 * time.Millisecond)
		eq(t, sibling.Status, statusQueued)
		eq(t, r.app.mgr.runningBackground, 1)
	})

	tw(t, managerFile, "a nested child failing does not free a pool slot either", func(t *testing.T) {
		r := startRig(t)
		r.app.mgr.maxConcurrent = 1
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		child := r.app.mgr.spawnOwned("scout", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		sibling := r.app.mgr.spawn("general-purpose", "sibling", "", childSpec{Prompt: "sibling"}, true)
		r.fleet.wait(t, 2)
		c := r.fleet.waitPrompt(t, "child")
		c.err = errBoom
		close(c.release)
		settled(t, child)
		eq(t, child.Status, statusError)
		time.Sleep(50 * time.Millisecond)
		eq(t, sibling.Status, statusQueued)
		eq(t, r.app.mgr.runningBackground, 1)
	})

	tw(t, managerFile, "a top-level agent settling DOES free its slot", func(t *testing.T) {
		// The other half of the rule: it guards against over-correcting into "never decrement", which wedges the queue.
		r := startRig(t)
		r.app.mgr.maxConcurrent = 1
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		sibling := r.app.mgr.spawn("general-purpose", "sibling", "", childSpec{Prompt: "sibling"}, true)
		eq(t, sibling.Status, statusQueued)
		r.fleet.waitPrompt(t, "parent").finish("done")
		settled(t, parent)
		r.fleet.waitPrompt(t, "sibling").finish("x")
		settled(t, sibling)
	})

	tw(t, managerFile, "never names a nested child, however it was spawned", func(t *testing.T) {
		// Nested agents are hidden from every top-level surface; a name would make one addressable through a boundary
		// only its owner may cross.
		r := startRig(t)
		rec := r.app.mgr.spawnOwned("Explore", "go", "child", childSpec{Prompt: "go"}, true, &owner{ParentID: "parent-1", Depth: 2, MaxDepth: 3})
		eq(t, rec.Alias, "")
		eq(t, rec.Handle, "")
		eq(t, r.app.mgr.resolve("child") == nil, true)
		eq(t, r.app.mgr.resolve(rec.ID) == nil, true)
		// A later top-level agent can take the type's handle: the nested child consumed none.
		top := r.app.mgr.spawn("Explore", "mine", "", childSpec{}, true)
		eq(t, top.Handle, "explore")
	})

	tw(t, managerFile, "fires once for a nested child, even though its spend is booked to ancestors too", func(t *testing.T) {
		// The hook sits on the child's own messages; the owner's count is a sum made when the owner settles (accountNested),
		// so one child message is billed once to the session's report.
		r := startRig(t)
		var seen []lifetimeUsage
		var mu sync.Mutex
		r.app.mgr.onUsage = func(_ *agentRecord, u lifetimeUsage) { mu.Lock(); seen = append(seen, u); mu.Unlock() }
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		r.fleet.waitPrompt(t, "parent").finish("done")
		settled(t, parent)
		mu.Lock()
		seen = nil
		mu.Unlock()
		child := r.app.mgr.spawnOwned("general-purpose", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		r.fleet.waitPrompt(t, "child").finish("done")
		settled(t, child)
		mu.Lock()
		defer mu.Unlock()
		eq(t, seen, []lifetimeUsage{{Input: 1500}})
	})
}

// Owned records stay out of everything the session's user sees and cannot be stopped through the bus.
func TestOwnedChildrenAreHiddenFromTheSession(t *testing.T) {
	r := startRig(t)
	top := r.app.mgr.spawn("general-purpose", "mine", "", childSpec{Prompt: "p"}, true)
	nested := r.app.mgr.spawnOwned("scout", "theirs", "", childSpec{Prompt: "p"}, true, &owner{ParentID: top.ID, Depth: 2, MaxDepth: 3})
	r.fleet.wait(t, 2)
	eq(t, r.app.mgr.listTopLevel(), []*agentRecord{top})
	eq(t, nested.topLevel(), false)

	// The stop RPC refuses what another agent owns, and does not touch it. upstream: cross-extension-rpc.ts:178.
	reply := r.request("subagents:rpc:stop", obj{"agentId": nested.ID})
	eq(t, reply["success"], false)
	eq(t, reply["error"], "Agent is owned by another agent or workflow")
	eq(t, nested.Status, statusRunning)
	// Its own is stopped.
	reply = r.request("subagents:rpc:stop", obj{"agentId": top.ID})
	eq(t, reply["success"], true)

	// The tools of the session read top-level agents only.
	text, _ := r.tool("get_subagent_result", obj{"agent_id": nested.ID})
	eq(t, text, `Agent not found: "`+nested.ID+`". It may have been cleaned up.`)
}

// A process-external caller cannot hand an agent an owner, a depth or a cap: the values are dropped.
// upstream: index.ts spawnTopLevel.
func TestSpawnRPCDropsOwnershipOptions(t *testing.T) {
	r := startRig(t)
	reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": obj{
		"description": "d", "parentAgentId": "someone", "depth": 7.0, "maxSubagentDepth": 99.0, "configCwd": "/elsewhere"}})
	eq(t, reply["success"], true)
	c := r.fleet.wait(t, 1)[0]
	recs := r.app.mgr.list()
	eq(t, len(recs), 1)
	eq(t, recs[0].topLevel(), true)
	eq(t, recs[0].Depth, 1)
	eq(t, c.spec.Depth, 1)
	eq(t, c.spec.MaxDepth, getMaxSubagentDepth())
	eq(t, c.spec.ConfigCwd == "/elsewhere", false)
}
