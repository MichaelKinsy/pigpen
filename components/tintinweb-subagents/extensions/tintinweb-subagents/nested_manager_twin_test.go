package tintinweb_subagents

import (
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
		eq(t, rec.ParentID, "parent-1")
		eq(t, rec.MaxDepth, 3)
		// The runner is given the nested runtime: the agent's own id as the owner of its children, its depth and cap, the config root.
		eq(t, c.spec.AgentID, rec.ID)
		eq(t, c.spec.Depth, 2)
		eq(t, c.spec.MaxDepth, 3)
		eq(t, c.spec.ConfigCwd, "/trusted/config")
		c.finish("x")
		settled(t, rec)
	})

	tskip(t, managerFile, "tells the runner which spawns are nested, so only top-level ones persist",
		"the flag only keeps a nested run's session from being saved for rememberAgents/persist_session, which are refused (session persistence is not ported)")

	tw(t, managerFile, "defaults top-level subagents to depth one", func(t *testing.T) {
		r := startRig(t)
		rec := r.app.mgr.spawn("scout", "top", "", childSpec{Prompt: "top"}, true)
		c := r.fleet.wait(t, 1)[0]
		eq(t, rec.Depth, 1)
		eq(t, c.spec.Depth, 1)
		eq(t, c.spec.AgentID, rec.ID)
		eq(t, isTopLevelAgent(rec), true)
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

	tskip(t, managerFile, "aborts children spawned during a resumed turn",
		"resuming a finished agent's session (resume) is not ported and is refused; the settle path this case guards for a resumed turn does not exist yet")

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
		eq(t, r.app.mgr.running, 1)
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
		eq(t, r.app.mgr.running, 1)
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

	tskip(t, managerFile, "fires once for a nested child, even though its spend is booked to ancestors too",
		"the per-message usage hook (onAssistantUsage, usage reporting) is not ported; a nested child's spend is carried in the owner's usage file instead (nested_tools twin: attributes a nested child's token spend)")
}

// Owned records stay out of everything the session's user sees and cannot be stopped through the bus.
func TestOwnedChildrenAreHiddenFromTheSession(t *testing.T) {
	r := startRig(t)
	top := r.app.mgr.spawn("general-purpose", "mine", "", childSpec{Prompt: "p"}, true)
	nested := r.app.mgr.spawnOwned("scout", "theirs", "", childSpec{Prompt: "p"}, true, &owner{ParentID: top.ID, Depth: 2, MaxDepth: 3})
	r.fleet.wait(t, 2)
	eq(t, r.app.mgr.listTopLevel(), []*agentRecord{top})
	eq(t, isTopLevelAgent(nested), false)

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
	eq(t, isTopLevelAgent(recs[0]), true)
	eq(t, recs[0].Depth, 1)
	eq(t, c.spec.Depth, 1)
	eq(t, c.spec.MaxDepth, getMaxSubagentDepth())
	eq(t, c.spec.ConfigCwd == "/elsewhere", false)
}
