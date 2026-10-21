package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twins of nested-tools.test.ts. The original calls createNestedSubagentTools with a fake manager; these call the tools
// of a child's extension with a fake process fleet, and ownership is checked on the records of the real manager.

const nestedTools = "nested-tools"

func TestNestedToolsTwins(t *testing.T) {
	spawnArgs := func(typ string, extra obj) obj {
		p := obj{"subagent_type": typ, "description": "do " + typ, "prompt": "Do it"}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	// finish waits for the child process the call started and lets it answer.
	finishWith := func(r *rig, text string) {
		r.fleet.wait(t, 1)[0].finish(text)
	}

	tw(t, nestedTools, "allows any enabled agent when allowed_subagents is omitted", func(t *testing.T) {
		r, cwd := scoutRig(t)
		out := nestedAsync(r, "Agent", spawnArgs("reviewer", obj{"description": "review evidence", "prompt": "Review it"}), nil)
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Prompt, "Review it")
		eq(t, c.spec.Depth, 2)
		eq(t, c.spec.MaxDepth, 2)
		eq(t, c.spec.ConfigCwd, cwd)
		recs := r.app.mgr.list()
		eq(t, len(recs), 1)
		eq(t, recs[0].ParentAgentID, "parent-1")
		eq(t, recs[0].Depth, 2)
		c.finish("done")
		o := recv(t, out).must(t)
		eq(t, o.IsError, false)
		eq(t, o.Text, "done")
	})

	tw(t, nestedTools, "keeps agent discovery rooted in inherited config, not the working directory", func(t *testing.T) {
		r, _ := scoutRig(t)
		work := t.TempDir()
		writeAgentFile(t, work, "intruder", "")
		r.app.cwd = work // the agent's working directory holds an agent the config root does not
		o := nestedExec(r, "Agent", spawnArgs("intruder", nil), nil).must(t)
		eq(t, o.IsError, true)
		eq(t, strings.Contains(o.Text, "Unknown or disabled"), true)
		eq(t, r.fleet.count(), 0)
	})

	tw(t, nestedTools, "enforces a narrow allowlist", func(t *testing.T) {
		r, cwd := scoutRig(t)
		r.app.nested.Allowed = []string{"scout"}
		_ = cwd
		denied := nestedExec(r, "Agent", spawnArgs("reviewer", obj{"description": "review evidence", "prompt": "Review it"}), nil).must(t)
		eq(t, denied.IsError, true)
		eq(t, strings.Contains(denied.Text, "not allowed"), true)
		eq(t, denied.Text, `Nested agent type "reviewer" is not allowed for this parent. Allowed: scout.`)
		eq(t, r.fleet.count(), 0)
		out := nestedAsync(r, "Agent", spawnArgs("scout", obj{"description": "find files"}), nil)
		finishWith(r, "found")
		allowed := recv(t, out).must(t)
		eq(t, allowed.IsError, false)
		eq(t, r.fleet.count(), 1)
	})

	tw(t, nestedTools, "resolves nested types without touching the process-global registry", func(t *testing.T) {
		r, _ := scoutRig(t)
		other := t.TempDir()
		writeAgentFile(t, other, "branch-only", "")
		r.app.nested.ConfigCwd = other
		before := getAvailableTypes()
		out := nestedAsync(r, "Agent", spawnArgs("branch-only", nil), nil)
		finishWith(r, "ok")
		o := recv(t, out).must(t)
		// Resolved from the inherited root...
		eq(t, o.IsError, false)
		// ...without leaking it into the shared registry.
		eq(t, getAvailableTypes(), before)
		eq(t, contains(strings.Join(getAvailableTypes(), ","), "branch-only"), false)
	})

	tw(t, nestedTools, "applies the scopeModels allowlist to a caller-supplied model", func(t *testing.T) {
		r, cwd := scoutRig(t)
		r.modelState = obj{
			"models": []any{
				obj{"id": "claude-haiku-4-5", "name": "Claude Haiku 4.5", "provider": "anthropic"},
				obj{"id": "gpt-4o", "name": "GPT-4o", "provider": "openai"},
			},
			"providers": obj{"anthropic": obj{"configured": true}, "openai": obj{"configured": true}},
		}
		writeJSONFile(t, filepath.Join(cwd, ".pi", "settings.json"), obj{"enabledModels": []string{"anthropic/claude-haiku-4-5"}})
		setScopeModelsEnabled(true)
		t.Cleanup(func() { setScopeModelsEnabled(false) })
		blocked := nestedExec(r, "Agent", spawnArgs("scout", obj{"description": "find files", "prompt": "Find them", "model": "gpt-4o"}), nil).must(t)
		eq(t, blocked.IsError, true)
		eq(t, strings.Contains(blocked.Text, "Model not in scope"), true)
		eq(t, r.fleet.count(), 0)
		out := nestedAsync(r, "Agent", spawnArgs("scout", obj{"description": "find files", "prompt": "Find them", "model": "haiku"}), nil)
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Model, "anthropic/claude-haiku-4-5")
		c.finish("x")
		eq(t, recv(t, out).must(t).IsError, false)
	})

	tw(t, nestedTools, "queues a steer for an owned child whose session is not ready yet", func(t *testing.T) {
		r, _ := scoutRig(t)
		rec := plant(r, &agentRecord{ID: "child-1", Status: statusRunning, ParentAgentID: "parent-1"})
		o := nestedExec(r, "steer_subagent", obj{"agent_id": "child-1", "message": "focus on tests"}, nil).must(t)
		eq(t, o.IsError, false)
		eq(t, o.Text, "Steering message queued for nested agent child-1.")
		eq(t, rec.pendingSteers, []string{"focus on tests"})
	})

	tw(t, nestedTools, "blocks delegation at the inherited depth cap", func(t *testing.T) {
		r, _ := scoutRig(t)
		r.app.nested.Depth, r.app.nested.MaxDepth = 2, 2
		o := nestedExec(r, "Agent", spawnArgs("scout", obj{"description": "find files", "prompt": "Find them"}), nil).must(t)
		eq(t, o.IsError, true)
		eq(t, o.Text, "Nested subagent call blocked (depth=2, max=2). Complete the task directly.")
		eq(t, r.fleet.count(), 0)
	})

	tw(t, nestedTools, "rejects unknown or disabled nested agent types instead of falling back", func(t *testing.T) {
		r, cwd := scoutRig(t)
		writeAgentFile(t, cwd, "disabled", "enabled: false\n")
		for _, typ := range []string{"missing", "disabled"} {
			o := nestedExec(r, "Agent", spawnArgs(typ, obj{"description": "invalid agent", "prompt": "Do work"}), nil).must(t)
			eq(t, o.IsError, true)
			eq(t, o.Text, `Unknown or disabled nested agent type: "`+typ+`". Allowed: general-purpose, Explore, Plan, reviewer, scout.`)
		}
		eq(t, r.fleet.count(), 0)
	})

	tw(t, nestedTools, "supports background launches and ownership-scopes result, resume, and steer", func(t *testing.T) {
		r, _ := scoutRig(t)
		r.app.nested.Allowed = []string{"scout"}
		launched := nestedExec(r, "Agent", spawnArgs("scout", obj{"description": "find files", "prompt": "Find them", "run_in_background": true}), nil).must(t)
		eq(t, strings.HasPrefix(launched.Text, "Nested agent started in background. Agent ID: "), true)
		id := strings.TrimPrefix(launched.Text, "Nested agent started in background. Agent ID: ")
		rec := r.app.mgr.get(id)
		eq(t, rec.Background, true)
		eq(t, rec.Depth, 2)
		eq(t, rec.ParentAgentID, "parent-1")
		own := nestedExec(r, "get_subagent_result", obj{"agent_id": id}, nil).must(t)
		eq(t, own.IsError, false)
		eq(t, own.Text, "Agent "+id+" is running.")

		foreign := plant(r, &agentRecord{ID: "foreign", Status: statusRunning, Result: "secret", ParentAgentID: "other"})
		got := nestedExec(r, "get_subagent_result", obj{"agent_id": "foreign"}, nil).must(t)
		eq(t, got.IsError, true)
		eq(t, got.Text, `Nested agent not found or not owned by this parent: "foreign".`)
		steer := nestedExec(r, "steer_subagent", obj{"agent_id": "foreign", "message": "stop"}, nil).must(t)
		eq(t, steer.IsError, true)
		eq(t, steer.Text, `Running nested agent not found or not owned by this parent: "foreign".`)
		resumed := nestedExec(r, "Agent", spawnArgs("scout", obj{"resume": "foreign", "description": "resume foreign", "prompt": "Continue"}), nil).must(t)
		eq(t, resumed.IsError, true)
		eq(t, resumed.Text, `Nested agent not found or not owned by this parent: "foreign".`)
		// Nothing reached the foreign record.
		eq(t, len(foreign.pendingSteers), 0)
		eq(t, foreign.Status, statusRunning)
		eq(t, r.fleet.count(), 1)
	})

	tw(t, nestedTools, "reports a background child that fails to start as a tool error", func(t *testing.T) {
		// The original's failure is a worktree copy that cannot be made; here the child process cannot be started.
		r, _ := scoutRig(t)
		r.app.nested.Allowed = []string{"scout"}
		r.fleet.setFail(errBoom)
		o := nestedExec(r, "Agent", spawnArgs("scout", obj{"description": "find files", "prompt": "Find them", "run_in_background": true}), nil).must(t)
		eq(t, o.IsError, true)
		eq(t, o.Text, "boom")
	})

	tw(t, nestedTools, "waits for a queued owned child to start and settle", func(t *testing.T) {
		r, _ := scoutRig(t)
		rec := plant(r, &agentRecord{ID: "queued-child", Status: statusQueued, ParentAgentID: "parent-1"})
		time.AfterFunc(10*time.Millisecond, func() {
			setStatus(r, rec, statusRunning, "")
			setStatus(r, rec, statusCompleted, "queued done")
			close(rec.done)
		})
		o := nestedExec(r, "get_subagent_result", obj{"agent_id": "queued-child", "wait": true}, nil).must(t)
		eq(t, o.Text, "queued done")
	})

	tw(t, nestedTools, "aborts a nested result wait without aborting the owned child", func(t *testing.T) {
		r, _ := scoutRig(t)
		rec := plant(r, &agentRecord{ID: "running-child", Status: statusRunning, ParentAgentID: "parent-1"})
		cancel := make(chan struct{})
		out := nestedAsync(r, "get_subagent_result", obj{"agent_id": "running-child", "wait": true}, cancel)
		close(cancel)
		o := recv(t, out)
		// The wait failed (the original's AbortError)...
		eq(t, o.Err != nil, true)
		// ...and the child was neither aborted nor consumed.
		eq(t, rec.Status, statusRunning)
		eq(t, rec.stopped, false)
		eq(t, rec.Consumed, false)
	})

	tw(t, nestedTools, "still rejects unknown types when the project configures a fallback", func(t *testing.T) {
		// A top-level fallback must not hand a nested caller an agent its allowlist never named.
		r, _ := scoutRig(t)
		r.app.nested.Allowed = []string{"scout"}
		fallback := "scout"
		setFallbackSubagent(&fallback)
		t.Cleanup(func() { setFallbackSubagent(nil) })
		o := nestedExec(r, "Agent", spawnArgs("definitely-missing", obj{"description": "typo", "prompt": "Do work"}), nil).must(t)
		eq(t, o.IsError, true)
		eq(t, strings.Contains(o.Text, "Unknown or disabled nested agent type"), true)
		eq(t, r.fleet.count(), 0)
	})

	tw(t, nestedTools, "hands the branch cap down to the child it spawns", func(t *testing.T) {
		r, _ := scoutRig(t)
		r.app.nested.Depth, r.app.nested.MaxDepth = 1, 3
		out := nestedAsync(r, "Agent", spawnArgs("scout", obj{"description": "child", "prompt": "Do work"}), nil)
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Depth, 2)
		eq(t, c.spec.MaxDepth, 3)
		c.finish("x")
		eq(t, recv(t, out).must(t).IsError, false)
	})

	tw(t, nestedTools, "flags a truncated child run instead of passing partial output off as complete", func(t *testing.T) {
		r, _ := scoutRig(t)
		out := nestedAsync(r, "Agent", spawnArgs("scout", obj{"description": "truncated", "prompt": "Do work"}), nil)
		c := r.fleet.wait(t, 1)[0]
		c.res = &childResult{Text: "half an answer", WrappedUp: true}
		close(c.release)
		o := recv(t, out).must(t)
		eq(t, o.IsError, false)
		// Foreground: the whole output is inline and no id came back, so the note must not invite a
		// get_subagent_result call the parent cannot make (#174).
		eq(t, strings.Contains(o.Text, "everything the agent produced is above"), true)
		eq(t, strings.Contains(o.Text, "output is partial"), false)
		// The warning leads, so it cannot read as part of the child's own answer.
		eq(t, strings.Index(o.Text, "half an answer") > 0, true)
		eq(t, o.Text, "Nested agent (wrapped up at the turn limit — everything the agent produced is above; the task may be unfinished).\n\nhalf an answer")
	})

	tw(t, nestedTools, "uses the fetchable wording when the parent polls a background child by id", func(t *testing.T) {
		r, _ := scoutRig(t)
		plant(r, &agentRecord{ID: "child-1", Status: statusAborted, Result: "partial work", ParentAgentID: "parent-1"})
		o := nestedExec(r, "get_subagent_result", obj{"agent_id": "child-1"}, nil).must(t)
		// Here the parent does hold a valid id, so the background wording applies.
		eq(t, strings.Contains(o.Text, "output may be incomplete"), true)
		eq(t, strings.Contains(o.Text, "everything the agent produced is above"), false)
	})

	tw(t, nestedTools, "keeps a failed child's partial output alongside the error", func(t *testing.T) {
		r, _ := scoutRig(t)
		out := nestedAsync(r, "Agent", spawnArgs("scout", obj{"description": "failing", "prompt": "Do work"}), nil)
		c := r.fleet.wait(t, 1)[0]
		c.res = &childResult{Text: "got this far", Failure: "provider exploded"}
		close(c.release)
		o := recv(t, out).must(t)
		eq(t, o.IsError, true)
		eq(t, strings.Contains(o.Text, "provider exploded"), true)
		eq(t, strings.Contains(o.Text, "got this far"), true)
		eq(t, o.Text, "Agent failed: provider exploded\n\nPartial output before the failure:\ngot this far")
	})

	tw(t, nestedTools, "attributes a nested child's token spend to the owning parent", func(t *testing.T) {
		// The owner's runner adds what is in its usage file to its own count; the child's settle writes the file.
		r, _ := scoutRig(t)
		r.app.nested.UsageFile = filepath.Join(t.TempDir(), "usage")
		nestedExec(r, "Agent", spawnArgs("scout", obj{"description": "spender", "prompt": "Do work", "run_in_background": true}), nil).must(t)
		c := r.fleet.wait(t, 1)[0]
		c.finish("spent") // 1500 tokens
		<-r.app.mgr.list()[0].done
		eq(t, readUsage(r.app.nested.UsageFile).Input, 1500)
		// A second child adds to it.
		nestedExec(r, "Agent", spawnArgs("scout", obj{"description": "spender", "prompt": "Do more", "run_in_background": true}), nil).must(t)
		r.fleet.wait(t, 2)[1].finish("spent")
		<-r.app.mgr.list()[1].done
		eq(t, readUsage(r.app.nested.UsageFile).Input, 3000)
	})

	tw(t, nestedTools, "attributes spend up the whole ancestor chain, not just one level", func(t *testing.T) {
		testSpendReachesTheTopThroughTheChain(t)
	})

	tw(t, nestedTools, "files a nested transcript under the root session, honoring output_transcript", func(t *testing.T) {
		r, cwd := scoutRig(t)
		r.app.nested.RootSessionID = "root-session"
		out := nestedAsync(r, "Agent", spawnArgs("scout", obj{"description": "traced", "prompt": "Do work"}), nil)
		c := r.fleet.wait(t, 1)[0]
		rec := r.app.mgr.list()[0]
		eq(t, strings.Contains(rec.OutputFile, filepath.Join("root-session", "tasks", rec.ID+".output")), true)
		c.finish("done")
		recv(t, out)
		// The child's own frontmatter still wins.
		writeAgentFile(t, cwd, "quiet", "output_transcript: false\n")
		out = nestedAsync(r, "Agent", spawnArgs("quiet", obj{"description": "untraced", "prompt": "Do work again"}), nil)
		c = r.fleet.waitPrompt(t, "Do work again")
		quiet := r.app.mgr.list()[1]
		eq(t, quiet.OutputFile, "")
		c.finish("done")
		recv(t, out)
	})

	tw(t, nestedTools, "forwards the execution context to the manager unmodified", func(t *testing.T) {
		// The call's own session context (here: the system prompt an `append` agent extends) reaches the spawn;
		// nothing is captured when the tools are built.
		r, cwd := scoutRig(t)
		writeAgentFile(t, cwd, "appender", "prompt_mode: append\n")
		r.mu.Lock()
		r.systemPrompt = "the childs own system prompt"
		r.mu.Unlock()
		out := nestedAsync(r, "Agent", spawnArgs("appender", obj{"description": "ctx check", "prompt": "Do work"}), nil)
		c := r.fleet.wait(t, 1)[0]
		eq(t, strings.HasPrefix(c.spec.SystemPrompt, "the childs own system prompt"), true)
		c.finish("x")
		eq(t, recv(t, out).must(t).IsError, false)
	})
}

// A nested tool never opens the environment of the process-wide registry: a sanity check that the files used above
// really sit where the rig says.
func TestNestedRigFilesExist(t *testing.T) {
	_, cwd := scoutRig(t)
	if _, err := os.Stat(filepath.Join(cwd, ".pi", "agents", "scout.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSetMaxSubagentDepthClamping(t *testing.T) {
	previous := getMaxSubagentDepth()
	t.Cleanup(func() { setMaxSubagentDepth(float64(previous)) })
	tw(t, nestedTools, "floors a negative depth at 0 rather than storing it", func(t *testing.T) {
		setMaxSubagentDepth(-1)
		eq(t, getMaxSubagentDepth(), 0)
	})
	tw(t, nestedTools, "truncates a fractional depth toward zero", func(t *testing.T) {
		setMaxSubagentDepth(2.9)
		eq(t, getMaxSubagentDepth(), 2)
	})
	tw(t, nestedTools, "stores a valid depth unchanged", func(t *testing.T) {
		setMaxSubagentDepth(3)
		eq(t, getMaxSubagentDepth(), 3)
	})
	t.Run("the default is two", func(t *testing.T) {
		eq(t, previous, 2) // main = 0, subagents = 1, their children = 2
	})
}
