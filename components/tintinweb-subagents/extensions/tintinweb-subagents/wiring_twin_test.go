package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of the wiring tests of the original: the real extension (here: the real app on a fake host) driven through its
// registered tools, the session events and the event bus, with a fake child where the original mocks runAgent.

// asyncTool is a tool call in flight: Host.Tool blocks, and these tests need to start a call, do something else, and
// cancel it (an interrupted turn).
type asyncTool struct {
	r  *rig
	id string
	ch chan json.RawMessage
}

func (r *rig) startTool(name string, params obj) *asyncTool {
	r.t.Helper()
	argv, _ := json.Marshal(params)
	h := r.Host
	h.mu.Lock()
	h.nextID++
	id := "async-" + itoa(h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(obj{"type": "request", "id": id, "request": obj{"method": "tool_call", "tool": name, "tool_call_id": "call-" + id, "args": json.RawMessage(argv)}})
	return &asyncTool{r: r, id: id, ch: ch}
}

// cancel interrupts the call, as the host does for a turn the user aborted.
func (c *asyncTool) cancel() { c.r.Host.write(obj{"type": "cancel", "id": c.id}) }

// result waits for the call to end: its text, or the failure message.
func (c *asyncTool) result() (text, failure string) {
	c.r.t.Helper()
	select {
	case raw := <-c.ch:
		c.r.Host.mu.Lock()
		failure = c.r.Host.failures[c.id]
		c.r.Host.mu.Unlock()
		var res struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(raw, &res)
		return res.Content, failure
	case <-time.After(10 * time.Second):
		c.r.t.Fatal("the tool call never ended")
		return "", ""
	}
}

func (c *asyncTool) done() bool { return len(c.ch) > 0 }

// withSettings starts a rig whose workspace has the given subagents.json applied.
func rigWithSettings(t *testing.T, settings string) *rig {
	t.Helper()
	r := startRig(t)
	cwd := r.Host.opts.Cwd
	writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), settings)
	r.app.startup(cwd)
	t.Cleanup(func() { setScopeModelsEnabled(false); resetSettingsView() })
	return r
}

func (r *rig) fgCall(prompt string) *asyncTool {
	return r.startTool("Agent", obj{"prompt": prompt, "description": prompt, "subagent_type": "general-purpose", "run_in_background": false})
}

func (r *rig) childFor(prompt string) *fakeChild {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.fleet.mu.Lock()
		for _, c := range r.fleet.children {
			if c.spec.Prompt == prompt {
				r.fleet.mu.Unlock()
				return c
			}
		}
		r.fleet.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("no child for %q", prompt)
	return nil
}

func (r *rig) noMoreChildren(n int) {
	r.t.Helper()
	time.Sleep(80 * time.Millisecond)
	eq(r.t, r.fleet.count(), n)
}

func TestForegroundConcurrencyWiringTwins(t *testing.T) {
	const f = "foreground-concurrency-wiring"
	tw(t, f, "runs blocking calls one at a time and returns each its own result", func(t *testing.T) {
		r := rigWithSettings(t, `{"outputTranscript":false,"maxConcurrentForeground":1}`)
		first := r.fgCall("alpha")
		r.childFor("alpha") // the calls reach the extension concurrently: alpha is let in first
		second := r.fgCall("beta")
		r.noMoreChildren(1) // only one started; beta is parked on the pool
		r.childFor("alpha").finish("alpha-RESULT")
		text, _ := first.result()
		eq(t, contains(text, "alpha-RESULT"), true)
		r.childFor("beta").finish("beta-RESULT")
		text, _ = second.result()
		eq(t, contains(text, "beta-RESULT"), true)
	})
	tw(t, f, "resolves a queued call as STOPPED when the turn is interrupted", func(t *testing.T) {
		// A tool call that fails would reject the whole batch pi dispatches together: the interrupted call must resolve.
		r := rigWithSettings(t, `{"outputTranscript":false,"maxConcurrentForeground":1}`)
		first := r.fgCall("alpha")
		r.childFor("alpha") // the calls reach the extension concurrently: alpha is let in first
		second := r.fgCall("beta")
		r.noMoreChildren(1)
		second.cancel()
		text, failure := second.result()
		eq(t, failure, "")
		eq(t, contains(text, "STOPPED BY THE USER"), true)
		r.childFor("alpha").finish("x") // freeing the slot must not start the agent the user just stopped
		first.result()
		r.noMoreChildren(1)
	})
	tskip(t, f, "says it is queued in the live tool result, then stops saying it", "the live line is a partial tool result (onUpdate details: an activity string for the renderer, a spinner frame); this port streams no partial tool results, which belong with the tool renderers (G5)")
	tw(t, f, "leaves blocking calls unbounded when the setting is unset", func(t *testing.T) {
		r := rigWithSettings(t, `{"outputTranscript":false,"maxConcurrentForeground":0}`)
		calls := []*asyncTool{r.fgCall("a"), r.fgCall("b"), r.fgCall("c")}
		r.fleet.wait(t, 3)
		for _, p := range []string{"a", "b", "c"} {
			r.childFor(p).finish(p)
		}
		for _, c := range calls {
			c.result()
		}
	})
}

// bgSpawn starts a background agent through the tool and returns its id and whether it queued.
func (r *rig) bgSpawn(prompt string) (id string, queued bool) {
	r.t.Helper()
	text := r.must("Agent", obj{"prompt": prompt, "description": "queued-wait test agent", "subagent_type": "general-purpose", "run_in_background": true})
	return idFrom(r.t, text), contains(text, "queued in background")
}

func (r *rig) notifications() []obj {
	var out []obj
	for _, m := range r.messages() {
		if mm, _ := m["message"].(map[string]any); mm["customType"] == "subagent-notification" {
			out = append(out, m)
		}
	}
	return out
}

func TestWaitQueuedTwins(t *testing.T) {
	const f = "wait-queued"
	queuedRig := func(t *testing.T) (*rig, string) {
		r := rigWithSettings(t, `{"maxConcurrent":1}`)
		r.bgSpawn("one")
		id, queued := r.bgSpawn("two")
		eq(t, queued, true)
		return r, id
	}
	tw(t, f, "waits through queue start and returns the result (no 'still running')", func(t *testing.T) {
		r, queuedID := queuedRig(t)
		wait := r.startTool("get_subagent_result", obj{"agent_id": queuedID, "wait": true}) // on the QUEUED agent
		r.childFor("one").finish("x")
		r.childFor("two").finish("THE-RESULT-PAYLOAD")
		text, _ := wait.result()
		eq(t, contains(text, "THE-RESULT-PAYLOAD"), true)
		eq(t, contains(text, "still running"), false)
		time.Sleep(350 * time.Millisecond)
		eq(t, contains(string(mustJSON(r.messages())), queuedID), false) // the result was read, so its notification was not sent
	})
	tw(t, f, "aborts a running result wait without aborting or consuming the child", func(t *testing.T) {
		r := rigWithSettings(t, `{}`)
		id, _ := r.bgSpawn("one")
		wait := r.startTool("get_subagent_result", obj{"agent_id": id, "wait": true})
		time.Sleep(50 * time.Millisecond)
		wait.cancel()
		_, failure := wait.result()
		eq(t, failure != "", true) // the wait fails with the cancellation (AbortError in the original)
		eq(t, r.childFor("one").abortedNow(), false)
		r.childFor("one").finish("THE-RESULT-PAYLOAD")
		time.Sleep(450 * time.Millisecond)
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, len(r.notifications()), 1) // not consumed by the cancelled wait: the notification still went out
		eq(t, contains(out, "THE-RESULT-PAYLOAD"), true)
	})
	tw(t, f, "aborts a queued result wait before the agent starts", func(t *testing.T) {
		r, queuedID := queuedRig(t)
		wait := r.startTool("get_subagent_result", obj{"agent_id": queuedID, "wait": true})
		time.Sleep(50 * time.Millisecond)
		wait.cancel()
		_, failure := wait.result()
		eq(t, failure != "", true)
		r.childFor("one").finish("x")
		r.childFor("two").finish("THE-RESULT-PAYLOAD")
		deadline := time.Now().Add(5 * time.Second)
		var out string
		for time.Now().Before(deadline) && !contains(out, "THE-RESULT-PAYLOAD") {
			out = r.must("get_subagent_result", obj{"agent_id": queuedID})
			time.Sleep(25 * time.Millisecond)
		}
		eq(t, contains(out, "THE-RESULT-PAYLOAD"), true)
	})
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestManagerRegistryGuardTwins(t *testing.T) {
	const f = "manager-registry-guard"
	fresh := func(t *testing.T) {
		registryMu.Lock()
		prior := activeEntry
		activeEntry = nil
		registryMu.Unlock()
		t.Cleanup(func() { registryMu.Lock(); activeEntry = prior; registryMu.Unlock() })
	}
	tw(t, f, "child activation does not overwrite the root entry; child shutdown does not delete it", func(t *testing.T) {
		fresh(t)
		root := newApp(sdk.New("root"), (&fleet{}).start)
		rootEntry := managerRegistry()
		eq(t, rootEntry == root.entry && rootEntry != nil, true)
		spawned := root.mgr.spawn("general-purpose", "registry test agent", "", childSpec{}, true)
		eq(t, rootEntry.GetRecord(spawned.ID) == spawned, true)

		child := newApp(sdk.New("child"), (&fleet{}).start)
		eq(t, managerRegistry() == rootEntry, true)
		eq(t, managerRegistry().GetRecord(spawned.ID) != nil, true)
		child.shutdown()
		eq(t, managerRegistry() == rootEntry, true)
		root.shutdown()
		eq(t, managerRegistry() == nil, true)
	})

	// forge boots a fresh owner and spawns through the registry with forged options.
	forge := func(t *testing.T, options obj) (*rig, *agentRecord) {
		fresh(t)
		r := startRig(t)
		registryMu.Lock()
		activeEntry = r.app.entry // the rig's app is the owner for this case
		registryMu.Unlock()
		ctx, _ := r.app.context()
		opts := obj{"description": "forged", "isBackground": true}
		for k, v := range options {
			opts[k] = v
		}
		id, err := managerRegistry().Spawn(ctx, "general-purpose", "go", opts)
		eq(t, err, nil)
		return r, managerRegistry().GetRecord(id)
	}
	tw(t, f, "refuses a forged nesting, so the agent cannot hide under someone else's id", func(t *testing.T) {
		_, rec := forge(t, obj{"parentAgentId": "victim-agent-id", "depth": 9, "maxSubagentDepth": 99})
		eq(t, rec != nil, true) // a top-level record: it is reachable through the registry
		eq(t, [3]any{rec.ParentAgentID, rec.Depth, rec.MaxSubagentDepth}, [3]any{"", 1, 0})
	})
	tw(t, f, "refuses a forged transcript directory and config root", func(t *testing.T) {
		r, rec := forge(t, obj{"rootSessionId": "../../elsewhere", "configCwd": "/etc"})
		c := r.fleet.wait(t, 1)[0]
		// This port has no transcript directory or config root to forge: the options are dropped, and the child starts in the session's workspace.
		eq(t, rec != nil && c.spec.Cwd == r.Host.opts.Cwd || c.spec.Cwd == r.app.cwd, true)
		eq(t, stripInternalSpawnOptions(obj{"rootSessionId": "x", "configCwd": "y", "description": "d"}), obj{"description": "d"})
	})
	tw(t, f, "refuses a forged session file, which would replay someone else's conversation", func(t *testing.T) {
		r, _ := forge(t, obj{"resumeSessionFile": "/home/victim/.pi/agent/sessions/private.jsonl"})
		r.fleet.wait(t, 1)
		eq(t, contains(strings.Join(childArgs(r.fleet.children[0].spec), " "), "private.jsonl"), false)
		eq(t, stripInternalSpawnOptions(obj{"resumeSessionFile": "/x"}), obj{})
	})
	tw(t, f, "refuses a forged reclaim and allocates a handle the ordinary way", func(t *testing.T) {
		_, rec := forge(t, obj{"reclaim": obj{"handle": "explore", "alias": "auth-audit"}})
		eq(t, [2]string{rec.Handle, rec.Alias}, [2]string{"general-purpose", ""})
	})
}

func TestClearCompletedWiringTwins(t *testing.T) {
	const f = "clear-completed-wiring"
	completedBackground := func(t *testing.T) (*rig, string) {
		r := rigWithSettings(t, `{}`)
		id, _ := r.bgSpawn("go")
		r.childFor("go").finish("THE-RESULT-PAYLOAD")
		<-r.app.mgr.get(id).done
		return r, id
	}
	tw(t, f, "session_before_switch (user switches sessions) does NOT wipe the unread result", func(t *testing.T) {
		r, id := completedBackground(t)
		r.Fire("session_before_switch", obj{})
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, contains(out, "Agent not found"), false)
		eq(t, contains(out, "THE-RESULT-PAYLOAD"), true)
	})
	tw(t, f, "session_start (/resume) does NOT wipe the unread result", func(t *testing.T) {
		r, id := completedBackground(t)
		r.Fire("session_start", obj{"reason": "resume"})
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, contains(out, "Agent not found"), false)
		eq(t, contains(out, "THE-RESULT-PAYLOAD"), true)
	})
	tw(t, f, "once read, a session switch DOES evict it — the fix stays surgical, no leak", func(t *testing.T) {
		r, id := completedBackground(t)
		eq(t, contains(r.must("get_subagent_result", obj{"agent_id": id}), "THE-RESULT-PAYLOAD"), true)
		r.Fire("session_before_switch", obj{})
		eq(t, contains(r.must("get_subagent_result", obj{"agent_id": id}), "Agent not found"), true)
	})
}

func TestStrictAgentFilesWiringTwins(t *testing.T) {
	const f = "strict-agent-files-wiring"
	const broken = "---\nname: broken\ndescription: Use this: that\n---\n\nBroken.\n"
	setup := func(t *testing.T) (cwd, path string, warns *[]string) {
		cwd = t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(cwd, "agent-dir"))
		path = filepath.Join(cwd, ".pi", "agents", "broken.md")
		writeSettings(t, path, broken)
		got := []string{}
		prev := agentFileWarn
		agentFileWarn = func(m string) { got = append(got, m) }
		resetTypes()
		t.Cleanup(func() { agentFileWarn = prev; resetTypes(); resetSettingsView() })
		return cwd, path, &got
	}
	boot := func(cwd string) (a *app, err any) {
		defer func() { err = recover() }()
		a = newApp(sdk.New("tintinweb-subagents"), nil)
		a.toolDefinitions(cwd)
		return a, nil
	}
	tw(t, f, "aborts activation naming the file when enabled", func(t *testing.T) {
		cwd, path, _ := setup(t)
		writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), `{"strictAgentFiles":true}`)
		_, err := boot(cwd)
		e, _ := err.(error)
		eq(t, e != nil && contains(e.Error(), path), true)
	})
	tw(t, f, "skips the file and activates when disabled (the default)", func(t *testing.T) {
		cwd, _, warns := setup(t)
		_, err := boot(cwd)
		eq(t, err, nil)
		eq(t, len(*warns) > 0 && contains((*warns)[0], "Skipping agent file"), true)
	})
	tw(t, f, "is a startup decision: a later reload of the same file does not throw", func(t *testing.T) {
		cwd, path, _ := setup(t)
		writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), `{"strictAgentFiles":true}`)
		writeSettings(t, path, "---\ndescription: Fixed\n---\n\nFixed.\n")
		a, err := boot(cwd)
		eq(t, err, nil)
		writeSettings(t, path, broken)
		a.cwd = cwd
		a.reregisterAgents() // what every Agent call does: it keeps warning
		res := resolveSpawnType("nope")
		eq(t, res.OK, true) // dispatch went on, with the fallback
	})
}

func TestFallbackSubagentWiringTwins(t *testing.T) {
	const f = "fallback-subagent-wiring"
	boot := func(t *testing.T) *rig {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		r := startRig(t)
		cwd := r.Host.opts.Cwd
		writeSettings(t, filepath.Join(cwd, ".pi", "agents", "scout.md"), "---\ndescription: Scout\ntools: read\n---\nScout.\n")
		writeSettings(t, filepath.Join(cwd, ".pi", "agents", "retired.md"), "---\ndescription: Retired\ntools: read\nenabled: false\n---\nRetired.\n")
		r.app.reload(cwd)
		t.Cleanup(func() { setFallbackSubagent(nil) })
		return r
	}
	none := noFallback
	tw(t, f, "refuses an unknown type without spawning (run_in_background: ${background})", func(t *testing.T) {
		for _, background := range []bool{false, true} {
			t.Run("run_in_background "+map[bool]string{false: "false", true: "true"}[background], func(t *testing.T) {
				r := boot(t)
				setFallbackSubagent(&none)
				out := r.must("Agent", obj{"prompt": "do it", "description": "typo dispatch", "subagent_type": "definitely-missing", "run_in_background": background})
				eq(t, contains(out, `Unknown or disabled agent type: "definitely-missing"`), true)
				eq(t, contains(out, "scout"), true)
				r.noMoreChildren(0)
			})
		}
	})
	tw(t, f, "still falls back — and says so — when the setting is unset", func(t *testing.T) {
		r := boot(t)
		out := r.must("Agent", obj{"prompt": "do it", "description": "typo dispatch", "subagent_type": "definitely-missing"})
		eq(t, contains(out, `Note: Unknown agent type "definitely-missing"`), true)
		r.fleet.wait(t, 1)
		eq(t, r.app.mgr.list()[0].Type, "general-purpose")
	})
	tw(t, f, "carries the fallback note on the background branch too", func(t *testing.T) {
		r := boot(t)
		out := r.must("Agent", obj{"prompt": "do it", "description": "typo dispatch", "subagent_type": "definitely-missing", "run_in_background": true})
		eq(t, contains(out, `Note: Unknown agent type "definitely-missing"`), true)
		eq(t, contains(out, "started in background"), true)
	})
	tw(t, f, "refuses a disabled type, which used to dispatch with a mixed identity", func(t *testing.T) {
		r := boot(t)
		setFallbackSubagent(&none)
		reg := currentRegistry()
		eq(t, inList(reg.keys, "retired"), true)
		eq(t, inList(reg.availableTypes(), "retired"), false)
		out := r.must("Agent", obj{"prompt": "do it", "description": "disabled dispatch", "subagent_type": "retired"})
		eq(t, contains(out, "Unknown or disabled agent type"), true)
		r.noMoreChildren(0)
	})
	tskip(t, f, "never persists a blank type into a scheduled job", "scheduled runs (the schedule parameter) are refused by this port until the scheduling slice (G2) lands, so no job exists to persist a type into")
	tskip(t, f, "never blocks resume, which ignores subagent_type entirely", "resume of a finished agent's session is refused by this port until the resume slice (G9) lands; the Agent call with a resume parameter fails before any type dispatch")
	tw(t, f, "applies the same contract to cross-extension spawns", func(t *testing.T) {
		r := boot(t)
		setFallbackSubagent(&none)
		ctx, _ := r.app.context()
		_, err := r.app.entry.Spawn(ctx, "definitely-missing", "do it", obj{"description": "rpc"})
		eq(t, err != nil && contains(err.Error(), "Unknown or disabled agent type"), true)
		r.noMoreChildren(0)
	})
}

func TestBackgroundByDefaultTwins(t *testing.T) {
	const f = "background-by-default"
	tw(t, f, "returns an agent ID, not the result, when the call doesn't specify", func(t *testing.T) {
		r := startRig(t)
		out := r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose"})
		r.childFor("go").finish("THE-PAYLOAD")
		eq(t, contains(out, "Agent ID:"), true)
		eq(t, contains(out, "THE-PAYLOAD"), false)
	})
	tw(t, f, "still blocks and returns the output inline when run_in_background is false", func(t *testing.T) {
		r := startRig(t)
		call := r.fgCall("go")
		r.childFor("go").finish("THE-PAYLOAD")
		out, _ := call.result()
		eq(t, contains(out, "THE-PAYLOAD"), true)
		eq(t, contains(out, "started in background"), false)
	})
	tw(t, f, "starts a six-way fan-out concurrently instead of queueing the tail", func(t *testing.T) {
		r := startRig(t)
		var outs []string
		for i := 0; i < 6; i++ {
			outs = append(outs, r.must("Agent", obj{"prompt": "go" + itoa(i), "description": "d", "subagent_type": "general-purpose"}))
		}
		eq(t, len(outs), 6)
		for _, out := range outs {
			if contains(out, "queued") {
				t.Logf("a fan-out call was queued: %s", out)
			}
			eq(t, contains(out, "queued"), false)
		}
	})
}

func TestForegroundResultRetrievalTwins(t *testing.T) {
	const f = "foreground-result-retrieval"
	steered := func(t *testing.T, r *rig) (text, id string) {
		call := r.startTool("Agent", obj{"prompt": "Perform a very thorough read-only codebase exploration.", "description": "Locate organization-scope changes",
			"subagent_type": "Explore", "max_turns": 20, "run_in_background": false})
		c := r.childFor("Perform a very thorough read-only codebase exploration.")
		c.finishWith(&childResult{Text: "THE-RESULT-PAYLOAD", WrappedUp: true})
		text, _ = call.result()
		recs := r.app.mgr.list()
		eq(t, len(recs), 1)
		return text, recs[0].ID // the original keeps the id in the renderer details only
	}
	tw(t, f, "is NOT cleaned up — get_subagent_result with the real id still resolves it", func(t *testing.T) {
		r := startRig(t)
		text, id := steered(t, r)
		eq(t, contains(text, "wrapped up at the turn limit"), true)
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, contains(out, "Agent not found"), false)
		eq(t, contains(out, "THE-RESULT-PAYLOAD"), true)
	})
	tw(t, f, "never hands the model an agent id — the id lives only in renderer details", func(t *testing.T) {
		r := startRig(t)
		text, id := steered(t, r)
		eq(t, contains(text, id), false)
		eq(t, contains(text, "Agent ID:"), false)
		eq(t, contains(r.must("get_subagent_result", obj{"agent_id": "3f1320a7-74ec-422"}), "Agent not found"), true)
	})
	tw(t, f, "survives a subagent session's OWN activation lifecycle (adversarial: cross-activation eviction)", func(t *testing.T) {
		r := startRig(t)
		_, id := steered(t, r)
		// A second activation in the same process, with its own session lifecycle.
		e2 := sdk.New("tintinweb-subagents")
		a2 := newApp(e2, (&fleet{}).start)
		a2.register(e2)
		h2 := StartHost(t, e2, HostOptions{Cwd: t.TempDir(), OnCallValue: func(string, map[string]any) (any, string) { return obj{}, "" }})
		h2.Fire("session_start", obj{"reason": "startup"})
		h2.Fire("session_shutdown", obj{})
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, contains(out, "Agent not found"), false)
		eq(t, contains(out, "THE-RESULT-PAYLOAD"), true)
	})
	tw(t, f, "IS evicted by a session switch — its result was already delivered inline", func(t *testing.T) {
		r := startRig(t)
		_, id := steered(t, r)
		r.Fire("session_before_switch", obj{})
		eq(t, contains(r.must("get_subagent_result", obj{"agent_id": id}), "Agent not found"), true)
	})
}

// startRigNoSession is startRig without the session_start event: the state of an extension that is loaded and not yet bound.
func startRigNoSession(t *testing.T) *rig {
	t.Helper()
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	r.app.register(e)
	r.Host = StartHost(t, e, HostOptions{Cwd: t.TempDir(), OnCallValue: r.answer})
	t.Cleanup(func() { r.app.mgr.abortAll() })
	return r
}

func TestRPCLifecycleGatingTwins(t *testing.T) {
	const f = "rpc-lifecycle-gating"
	channels := []string{"subagents:rpc:ping", "subagents:rpc:spawn", "subagents:rpc:stop"}
	tw(t, f, "does NOT advertise or register RPC at factory time (the filtered-out case)", func(t *testing.T) {
		r := startRigNoSession(t)
		eq(t, r.emitted("subagents:ready"), false) // no subagents:ready before session_start
		for _, ch := range channels {
			eq(t, len(r.extBus[ch]), 0)
		}
		eq(t, len(r.CallsTo("events.on")), 0)
	})
	tw(t, f, "advertises and registers RPC on session_start, and spawn works once bound", func(t *testing.T) {
		r := startRigNoSession(t)
		r.Fire("session_start", obj{"reason": "startup"})
		r.waitEmitted("subagents:ready")
		for _, ch := range channels {
			eq(t, len(r.extBus[ch]), 1)
		}
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "go", "options": obj{"description": "rpc gating test"}})
		eq(t, reply["success"], true)
		eq(t, reply["data"].(map[string]any)["id"] != "", true)
	})
	tskip(t, f, "renders an RPC-spawned agent in the native widget while it is running", "the assertion is the widget (setWidget with a factory, the \"1 running agent\" status) which is the TUI of the widget slice (G5); this port draws no widget")
	tskip(t, f, "shows live tool activity for an RPC-spawned background agent", "the assertion is the widget's rendered row (\"running command…\") fed by the in-process session's tool activity callback; this port draws no widget (G5) and a child process reports no tool activity to the manager")
	tw(t, f, "is idempotent — a second session_start does not re-advertise or double-register", func(t *testing.T) {
		r := startRigNoSession(t)
		r.Fire("session_start", obj{"reason": "startup"})
		r.Fire("session_start", obj{"reason": "startup"})
		r.waitEmitted("subagents:ready")
		n := 0
		for _, c := range r.emits {
			if c == "subagents:ready" {
				n++
			}
		}
		eq(t, n, 1)
		for _, ch := range channels {
			eq(t, len(r.extBus[ch]), 1)
		}
	})
}

func TestRPCResultConsumptionTwins(t *testing.T) {
	const f = "rpc-result-consumption"
	spawnOverRPC := func(r *rig, requestID string) string {
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "go", "options": obj{"description": "task #1", "isBackground": true}})
		return reply["data"].(map[string]any)["id"].(string)
	}
	tw(t, f, "notifies for an RPC-spawned agent nobody consumed", func(t *testing.T) {
		r := rigWithSettings(t, `{"outputTranscript":false}`)
		spawnOverRPC(r, "req-spawn-1")
		r.childFor("go").finish("TASK_EXECUTE_AGENT_OK")
		time.Sleep(450 * time.Millisecond)
		eq(t, len(r.notifications()), 1)
	})
	tw(t, f, "suppresses the notification once the caller consumes the result", func(t *testing.T) {
		r := rigWithSettings(t, `{"outputTranscript":false}`)
		r.onBus("subagents:completed", func(d any) {
			m, _ := d.(map[string]any)
			r.emit("subagents:rpc:consume", obj{"requestId": "req-consume", "agentId": m["id"]})
		})
		spawnOverRPC(r, "req-spawn-2")
		r.childFor("go").finish("TASK_EXECUTE_AGENT_OK")
		time.Sleep(450 * time.Millisecond)
		eq(t, len(r.notifications()), 0)
	})
	tw(t, f, "refuses to consume an agent that is still running", func(t *testing.T) {
		r := rigWithSettings(t, `{"outputTranscript":false}`)
		id := spawnOverRPC(r, "req-spawn-3")
		reply := r.request("subagents:rpc:consume", obj{"agentId": id})
		eq(t, [2]any{reply["success"], reply["error"]}, [2]any{false, "Agent not found or still running"})
	})
	tw(t, f, "reports an unknown agent rather than silently succeeding", func(t *testing.T) {
		r := rigWithSettings(t, `{"outputTranscript":false}`)
		reply := r.request("subagents:rpc:consume", obj{"agentId": "no-such-agent"})
		eq(t, [2]any{reply["success"], reply["error"]}, [2]any{false, "Agent not found or still running"})
	})
}

// gatedStart is a child starter that waits to be let go: the agent exists, and has no child yet.
type gatedStart struct {
	gate  chan struct{}
	inner childStarter
}

func (g *gatedStart) start(ctx context.Context, spec childSpec) (child, error) {
	select {
	case <-g.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.inner(ctx, spec)
}

// steerFails is a child whose steering always fails.
type steerFails struct{ *fakeChild }

func (steerFails) steer(string) error { return errors.New("session closed") }

func TestSteerSubagentWiringTwins(t *testing.T) {
	const f = "steer-subagent-wiring"
	steer := func(r *rig, id, msg string) string {
		return r.must("steer_subagent", obj{"agent_id": id, "message": msg})
	}
	// before: the agent is running and its child does not exist yet.
	before := func(t *testing.T) (*rig, string, *gatedStart, *[]obj) {
		r := startRig(t)
		g := &gatedStart{gate: make(chan struct{}), inner: r.fleet.start}
		r.app.mgr.start = g.start
		var steers []obj
		var mu sync.Mutex
		r.onBus("subagents:steered", func(d any) { mu.Lock(); m, _ := d.(map[string]any); steers = append(steers, m); mu.Unlock() })
		t.Cleanup(func() { r.app.mgr.abortAll() })
		id, _ := r.bgSpawn("go")
		return r, id, g, &steers
	}
	tw(t, f, "queues the message on the record and says so", func(t *testing.T) {
		r, id, g, _ := before(t)
		out := steer(r, id, "change course")
		eq(t, contains(out, "queued"), true)
		r.waitEmitted("subagents:steered")
		_ = g
	})
	tw(t, f, "appends a second queued steer instead of replacing the first", func(t *testing.T) {
		r, id, g, _ := before(t)
		steer(r, id, "first")
		steer(r, id, "second")
		close(g.gate) // the session now exists: the queued steers are delivered, in order
		c := r.fleet.wait(t, 1)[0]
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			n := len(c.steered)
			c.mu.Unlock()
			if n == 2 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		c.mu.Lock()
		eq(t, c.steered, []string{"first", "second"})
		c.mu.Unlock()
	})
	tw(t, f, "does not call steerAgent — there is no session to steer yet", func(t *testing.T) {
		r, id, _, _ := before(t)
		steer(r, id, "hello")
		r.noMoreChildren(0) // nothing was delivered anywhere: there is no child to steer
	})
	tw(t, f, "reports failure and emits no event when the steer throws", func(t *testing.T) {
		r := startRig(t)
		r.app.mgr.start = func(ctx context.Context, spec childSpec) (child, error) {
			c, err := r.fleet.start(ctx, spec)
			return steerFails{c.(*fakeChild)}, err
		}
		var steers []any
		r.onBus("subagents:steered", func(d any) { steers = append(steers, d) })
		id, _ := r.bgSpawn("go")
		r.fleet.wait(t, 1)
		time.Sleep(30 * time.Millisecond)
		out := steer(r, id, "too late")
		eq(t, contains(out, "Failed to steer agent"), true)
		eq(t, contains(out, "session closed"), true)
		time.Sleep(50 * time.Millisecond)
		eq(t, len(steers), 0)
	})
	tw(t, f, "delivers through steerAgent and announces the steer on success", func(t *testing.T) {
		r := startRig(t)
		var steers []obj
		var mu sync.Mutex
		r.onBus("subagents:steered", func(d any) { mu.Lock(); m, _ := d.(map[string]any); steers = append(steers, m); mu.Unlock() })
		id, _ := r.bgSpawn("go")
		c := r.fleet.wait(t, 1)[0]
		time.Sleep(30 * time.Millisecond)
		out := steer(r, id, "refocus")
		c.mu.Lock()
		eq(t, c.steered, []string{"refocus"})
		c.mu.Unlock()
		eq(t, contains(out, "Steering message sent"), true)
		r.waitEmitted("subagents:steered")
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		eq(t, steers, []obj{{"id": r.app.mgr.list()[0].ID, "message": "refocus"}})
		mu.Unlock()
	})
}

func TestStatusNoteWiringTwins(t *testing.T) {
	const f = "status-note-wiring"
	tw(t, f, "foreground turn-limit abort → the Agent result flags an incomplete outcome", func(t *testing.T) {
		r := startRig(t)
		call := r.fgCall("go")
		r.childFor("go").finishWith(&childResult{Text: "partial work so far", Aborted: true}) // a hard turn-limit abort
		out, _ := call.result()
		eq(t, contains(out, "aborted at the turn limit"), true)
		eq(t, contains(out, "partial work so far"), true)  // the partial result is still delivered
		eq(t, contains(out, "STOPPED BY THE USER"), false) // not mislabelled as a user stop
		eq(t, contains(out, "everything the agent produced is above"), true)
		eq(t, contains(out, "the task is unfinished"), true)
		eq(t, contains(out, "re-spawn") || contains(out, "get_subagent_result"), false)
	})
	tw(t, f, "foreground user-stop → tells the parent NOT to restart it unasked", func(t *testing.T) {
		r := startRig(t)
		call := r.fgCall("go")
		c := r.childFor("go")
		call.cancel() // the user hits ESC
		out, _ := call.result()
		_ = c
		eq(t, contains(out, "STOPPED BY THE USER"), true)
		eq(t, contains(out, "everything the agent produced is above"), true)
		eq(t, contains(out, "the task is unfinished"), true)
		eq(t, contains(out, "re-spawn") || contains(out, "ask before"), false)
	})
	tw(t, f, "hides nested records from top-level tools, registry, transcripts, and lifecycle", func(t *testing.T) {
		r := startRig(t)
		ctx, _ := r.app.context()
		// An owner spawned through the registry, with a forged parent: it is top-level.
		topID, err := r.app.entry.Spawn(ctx, "general-purpose", "top", obj{"description": "top-level owner", "isBackground": true, "parentAgentId": "forged-parent", "depth": 99, "maxSubagentDepth": 99, "configCwd": "/untrusted/config"})
		eq(t, err, nil)
		top := r.app.mgr.get(topID)
		eq(t, [2]any{top.ParentAgentID, top.Depth}, [2]any{"", 1})
		r.fleet.wait(t, 1)
		// A nested child, as the nested tools spawn it, owned by the top-level agent.
		nested := r.app.mgr.spawnWith("general-purpose", "nested child", childSpec{Prompt: "nested"}, spawnOptions{Background: true, ParentAgentID: topID, Depth: 2, MaxSubagentDepth: 2})
		r.fleet.wait(t, 2)
		eq(t, r.app.entry.GetRecord(nested.ID) == nil, true)
		for _, tool := range [][2]any{{"get_subagent_result", obj{"agent_id": nested.ID}}, {"steer_subagent", obj{"agent_id": nested.ID, "message": "stop"}}} {
			eq(t, contains(r.must(tool[0].(string), tool[1].(obj)), "Agent not found"), true)
		}
		// (The original's third probe, Agent with `resume`, is refused by this port before any lookup: resume is not ported.)
		r.fleet.children[1].finish("nested result")
		<-nested.done
		time.Sleep(450 * time.Millisecond)
		eq(t, r.emitted("subagents:completed") || r.emitted("subagents:failed"), false) // no lifecycle event for the nested child
		eq(t, len(r.messages()), 0)
	})
	tw(t, f, "background user-stop → get_subagent_result flags STOPPED BY THE USER (not completed)", func(t *testing.T) {
		r := startRig(t)
		id, _ := r.bgSpawn("go")
		r.request("subagents:rpc:stop", obj{"agentId": id})
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, contains(out, "STOPPED BY THE USER"), true)
		eq(t, contains(out, "the task was NOT finished"), true)
		eq(t, contains(out, "Done"), false) // not surfaced as a normal completion
		eq(t, contains(out, "everything the agent produced is above"), false)
	})
	_ = os.Stat
}
