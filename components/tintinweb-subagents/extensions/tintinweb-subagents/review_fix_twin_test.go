package tintinweb_subagents

import (
	"path/filepath"
	"testing"
	"time"
)

// Twins of behavior of the original's index.ts that are not cases of its own test files; each cites the code.

func (r *rig) appended() []obj {
	var out []obj
	for _, c := range r.CallsTo("appendEntry") {
		out = append(out, c.Args)
	}
	return out
}

// index.ts:584-589: a top-level agent's final record is appended to the session; an owned child reports through its owner.
func TestSubagentsRecordEntry(t *testing.T) {
	r := startRig(t)
	id, _ := r.bgSpawn("top")
	rec := r.app.mgr.get(id)
	nested := r.app.mgr.spawnWith("general-purpose", "nested child", childSpec{Prompt: "nested"}, spawnOptions{Background: true, ParentAgentID: id, Depth: 2, MaxSubagentDepth: 2})
	r.childFor("nested").finish("nested result")
	<-nested.done
	eq(t, len(r.appended()), 0) // the nested child leaves no record
	r.childFor("top").finish("top result")
	<-rec.done
	got := r.appended()
	eq(t, len(got), 1)
	eq(t, got[0]["customType"], "subagents:record")
	data := got[0]["data"].(map[string]any)
	eq(t, [5]any{data["id"], data["type"], data["description"], data["status"], data["result"]},
		[5]any{id, "general-purpose", "queued-wait test agent", statusCompleted, "top result"})
	eq(t, data["error"], "")
	eq(t, data["startedAt"].(float64) > 0, true)
	eq(t, data["completedAt"].(float64) >= data["startedAt"].(float64), true)
}

// index.ts:2104-2109 and :1350: subagents:created is emitted for a background spawn only, never for a foreground one.
func TestCreatedEventIsBackgroundOnly(t *testing.T) {
	r := startRig(t)
	call := r.fgCall("fg")
	r.childFor("fg").finish("done")
	call.result()
	eq(t, r.emitted("subagents:created"), false)
	r.bgSpawn("bg")
	r.waitEmitted("subagents:created")
}

// index.ts:666-674 spawnResolved: reloadCustomAgents() first, so an agent file added mid-session is spawnable over the RPC.
func TestRPCSpawnReloadsAgentFiles(t *testing.T) {
	r := startRig(t)
	eq(t, r.request("subagents:rpc:spawn", obj{"type": "late", "prompt": "x", "options": obj{"isBackground": true}})["success"], true) // falls back to general-purpose
	writeSettings(t, filepath.Join(r.Host.opts.Cwd, ".pi", "agents", "late.md"), "---\ndescription: Late\n---\nLate agent body.\n")
	reply := r.request("subagents:rpc:spawn", obj{"type": "late", "prompt": "y", "options": obj{"isBackground": true}})
	eq(t, reply["success"], true)
	eq(t, contains(r.childFor("y").spec.SystemPrompt, "Late agent body."), true)
}

// agent-manager.ts:520-540, :969 and index.ts:592: an RPC spawn without isBackground (or with false) is detached: it runs at
// once with no pool slot, and nothing is announced to the model when it ends.
func TestRPCSpawnWithoutIsBackgroundIsDetached(t *testing.T) {
	for _, options := range []obj{{"description": "task"}, {"description": "task", "isBackground": false}} {
		r := rigWithSettings(t, `{"maxConcurrent":1,"outputTranscript":false}`)
		r.bgSpawn("holder") // the background pool is full
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "rpc", "options": options})
		eq(t, reply["success"], true)
		id := reply["data"].(map[string]any)["id"].(string)
		eq(t, r.app.mgr.get(id).Status, statusRunning)
		r.childFor("rpc").finish("rpc result")
		<-r.app.mgr.get(id).done
		time.Sleep(450 * time.Millisecond)
		eq(t, len(r.notifications()), 0)
		eq(t, r.emitted("subagents:completed"), true) // the lifecycle event is still emitted
	}
}

// The schema fills the types and the agent dir into the decoded strings: a quote or backslash must not break it.
func TestAgentSchemaWithQuotesInTheAgentDir(t *testing.T) {
	resetTypes()
	t.Cleanup(resetTypes)
	dir := `/the "agent"\dir`
	dc := descriptionContext{reg: currentRegistry(), agentDir: dir, warn: func(string) {}}
	s := agentSchemaFor(descriptionFlags{mode: "full"}, dc, loadToolSpecs()["Agent"])
	eq(t, s["properties"].(map[string]any)["subagent_type"].(map[string]any)["description"],
		"The type of specialized agent to use. Available types: general-purpose, Explore, Plan. Custom agents from .pi/agents/*.md (project) or "+dir+"/agents/*.md (global) are also available.")
}

// index.ts:1772: the Agent tool reloads the agent files on every call.
func TestAgentToolReloadsAgentFilesPerCall(t *testing.T) {
	r := startRig(t)
	writeSettings(t, filepath.Join(r.Host.opts.Cwd, ".pi", "agents", "added.md"), "---\ndescription: Added\n---\nAdded mid-session.\n")
	r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "added", "run_in_background": true})
	eq(t, contains(r.childFor("go").spec.SystemPrompt, "Added mid-session."), true)
}

// index.ts resolveAgentRef: get_subagent_result addresses an agent by id or by handle, and by nothing else.
func TestResultToolAddressesByIDOrHandleOnly(t *testing.T) {
	r := startRig(t)
	id, _ := r.bgSpawn("go")
	r.childFor("go").finish("the result")
	<-r.app.mgr.get(id).done
	for _, ref := range []string{id, "general-purpose", "GENERAL-PURPOSE"} {
		eq(t, contains(r.must("get_subagent_result", obj{"agent_id": ref}), "the result"), true)
	}
	eq(t, contains(r.must("get_subagent_result", obj{"agent_id": id[:3]}), "Agent not found"), true)
}

// agent-runner.ts:675-683 and agent-types.ts:221-224: with the unset fallback an unknown type resolves to general-purpose even
// when that was removed from the registry; the agent runs under the built-in definition, and the note says so (index.ts:1797).
func TestFallbackToARemovedGeneralPurposeStillRuns(t *testing.T) {
	r := startRig(t)
	setDefaultsDisabled(true)
	t.Cleanup(func() { setDefaultsDisabled(false) })
	out := r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "nothing", "run_in_background": true})
	eq(t, contains(out, `Note: Unknown agent type "nothing" — using the fallback agent config.`), true)
	eq(t, contains(out, "Type: Agent\n"), true) // getConfig's absolute fallback display name
	eq(t, contains(r.childFor("go").spec.SystemPrompt, `<active_agent name="general-purpose"/>`), true)
}
