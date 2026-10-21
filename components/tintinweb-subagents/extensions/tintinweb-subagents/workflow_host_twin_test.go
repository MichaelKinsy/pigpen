package tintinweb_subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of the host-adapter cases of test/workflow-tool.test.ts, test/workflow-gate-worktree.test.ts and
// test/workflow-effective-config.test.ts. The original drives its real AgentManager with a mocked runner (and, for the
// gate, a real git repo); here the manager is the workflowManager seam, stubbed, and a stub that plays the manager's
// settle (the child's worktree created, the pre-cleanup hook run while it exists, the copy removed) takes the place of
// the real worktree. The real manager's worktree and git behavior is the worktree lane's, tested there.

type wfSpawnCall struct {
	typ, prompt string
	opts        workflowSpawnOptions
}

// stubManager is a manager that spawns nothing; settle decides what each spawn produces. upstream: stubManager.
type stubManager struct {
	mu      sync.Mutex
	spawns  []wfSpawnCall
	aborts  []string
	resumes [][2]string
	records map[string]workflowRecord
	settle  func(c *wfSpawnCall, spawned func(workflowRecord)) (workflowRecord, error)
	resume  func(id, prompt string) (workflowRecord, bool, error)
	n       int
}

func newStubManager(settle func(c *wfSpawnCall, spawned func(workflowRecord)) (workflowRecord, error)) *stubManager {
	m := &stubManager{records: map[string]workflowRecord{}, settle: settle}
	if settle == nil {
		m.settle = func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) { return record(nil), nil }
	}
	m.resume = func(id, prompt string) (workflowRecord, bool, error) {
		return record(func(r *workflowRecord) { r.ID, r.Result = id, "resumed" }), true, nil
	}
	return m
}

// record is a settled record, in the shape wfToSpawnResult reads.
func record(mod func(*workflowRecord)) workflowRecord {
	r := workflowRecord{ID: "agent-1", Status: statusCompleted, Result: "the answer"}
	if mod != nil {
		mod(&r)
	}
	return r
}

func (m *stubManager) store(r workflowRecord) {
	m.mu.Lock()
	m.records[r.ID] = r
	m.mu.Unlock()
}

func (m *stubManager) SpawnAndWait(typ, prompt string, opts workflowSpawnOptions, onSpawned func(string)) (workflowRecord, error) {
	call := &wfSpawnCall{typ, prompt, opts}
	m.mu.Lock()
	m.spawns = append(m.spawns, *call)
	m.mu.Unlock()
	spawned := func(r workflowRecord) {
		m.store(r)
		onSpawned(r.ID)
	}
	called := false
	rec, err := m.settle(call, func(r workflowRecord) { called = true; spawned(r) })
	if err != nil {
		return workflowRecord{}, err
	}
	if !called {
		spawned(rec)
	} else {
		m.store(rec)
	}
	return rec, nil
}

func (m *stubManager) Abort(id string) { m.mu.Lock(); m.aborts = append(m.aborts, id); m.mu.Unlock() }
func (m *stubManager) Resume(_ context.Context, id, prompt string) (workflowRecord, bool, error) {
	m.mu.Lock()
	m.resumes = append(m.resumes, [2]string{id, prompt})
	m.mu.Unlock()
	return m.resume(id, prompt)
}
func (m *stubManager) GetRecord(id string) (workflowRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	return r, ok
}
func (m *stubManager) call(i int) wfSpawnCall { m.mu.Lock(); defer m.mu.Unlock(); return m.spawns[i] }
func (m *stubManager) count() int           { m.mu.Lock(); defer m.mu.Unlock(); return len(m.spawns) }

func wfRequest(mod func(*workflowSpawnRequest)) *workflowSpawnRequest {
	r := &workflowSpawnRequest{Ctx: context.Background(), AgentID: "wf-agent-0", Index: 0, Prompt: "do the thing", Label: "step", AgentType: "general-purpose",
		OnResolved: func(workflowResolvedInfo) {}}
	if mod != nil {
		mod(r)
	}
	return r
}

type execRun struct {
	command, cwd          string
	existed, sawChildWork bool
}

// execStub is pi.exec: a gate command is answered from gate, and what its directory looked like at that instant is
// sampled inside the call (afterwards the worktree is gone, so a post-hoc stat would prove nothing either way).
type execStub struct {
	mu   sync.Mutex
	runs []execRun
	gate func(command string) (workflowExecResult, error)
	opts []time.Duration
}

const childFile = "child-work.txt"

func (e *execStub) exec(_ context.Context, command string, args []string, cwd string, timeout time.Duration) (workflowExecResult, error) {
	_, existsErr := os.Stat(cwd)
	_, sawErr := os.Stat(filepath.Join(cwd, childFile))
	run := execRun{command: args[len(args)-1], cwd: cwd, existed: existsErr == nil, sawChildWork: sawErr == nil}
	e.mu.Lock()
	e.runs = append(e.runs, run)
	e.opts = append(e.opts, timeout)
	e.mu.Unlock()
	if e.gate != nil {
		return e.gate(run.command)
	}
	return workflowExecResult{Stdout: "3 passing"}, nil
}

func (e *execStub) all() []execRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]execRun(nil), e.runs...)
}

func hostWith(m *stubManager, mod func(*workflowHostDeps)) *workflowAgentHost {
	d := workflowHostDeps{Manager: m, Cwd: "/session", Exec: (&execStub{}).exec}
	if mod != nil {
		mod(&d)
	}
	return newWorkflowHost(d)
}

// fakeRegistry is a model registry stub.
type fakeRegistry struct{ models []*workflowModel }

func (f fakeRegistry) Find(provider, id string) *workflowModel {
	for _, m := range f.models {
		if m.Provider == provider && m.ID == id {
			return m
		}
	}
	return nil
}
func (f fakeRegistry) GetAll() []*workflowModel       { return f.models }
func (f fakeRegistry) GetAvailable() []*workflowModel { return f.models }

func TestWorkflowHostSpawnMapping(t *testing.T) {
	const f = "workflow-tool"
	resetTypes()
	t.Cleanup(resetTypes)

	tw(t, f, "spawns through the manager and maps a completed record onto the script's result", func(t *testing.T) {
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) {
				r.Result, r.ToolUses, r.LifetimeTokens, r.OutputTokens = "the answer", 3, 125, 20
			}), nil
		})
		res, _ := hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Label = "review:bugs" }))
		eq(t, [4]any{res.OK, res.Text, *res.Tokens, *res.ToolCalls}, [4]any{true, "the answer", 125, 3})
		// tokens is the lifetime total; outputTokens feeds the script's budget.spent() and must be output alone.
		eq(t, res.OutputTokens, 20)
		c := m.call(0)
		eq(t, c.typ, "general-purpose")
		eq(t, c.prompt, "do the thing")
		// The label is the child's display description, so the fleet list and the workflow tree name it the same way.
		eq(t, c.opts.Description, "review:bugs")
	})
	tw(t, f, "passes isolation and the run's abort signal down to the spawn", func(t *testing.T) {
		m := newStubManager(nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		hostWith(m, func(d *workflowHostDeps) { d.Signal, d.RootSessionID = ctx, "root-1" }).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Isolation = "worktree" }))
		o := m.call(0).opts
		eq(t, o.Isolation, "worktree")
		eq(t, o.Ctx, ctx)
		eq(t, o.RootSessionID, "root-1")
	})
	tw(t, f, "routes an unknown agent type through the same dispatch the Agent tool uses", func(t *testing.T) {
		// Not a second resolution path: resolveSpawnType owns the fallback policy, so an unknown type falls back here exactly
		// as it does for a tool call...
		m := newStubManager(nil)
		fellBack, _ := hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentType = "no-such-agent" }))
		eq(t, fellBack.OK, true)
		eq(t, m.call(0).typ, "general-purpose")
		// ...and fails closed here too when the project configured strict dispatch.
		none := noFallback
		setFallbackSubagent(&none)
		defer setFallbackSubagent(nil)
		strict := newStubManager(nil)
		rejected, _ := hostWith(strict, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentType = "no-such-agent" }))
		eq(t, rejected.OK, false)
		eq(t, strings.Contains(rejected.Error, "no-such-agent"), true)
		eq(t, strict.count(), 0)
	})
	tw(t, f, "rejects a model the script named and cannot be resolved", func(t *testing.T) {
		m := newStubManager(nil)
		res, _ := hostWith(m, func(d *workflowHostDeps) { d.Registry = fakeRegistry{} }).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Model = "not-a-model" }))
		eq(t, res.OK, false)
		eq(t, strings.Contains(res.Error, "Model not found"), true)
		eq(t, m.count(), 0)
	})
	tw(t, f, "resolves a model the script named through the session's registry", func(t *testing.T) {
		m := newStubManager(nil)
		haiku := &workflowModel{ID: "claude-haiku-4-5", Name: "Haiku", Provider: "anthropic"}
		hostWith(m, func(d *workflowHostDeps) { d.Registry = fakeRegistry{[]*workflowModel{haiku}} }).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Model = "haiku" }))
		eq(t, m.call(0).opts.Model, "anthropic/claude-haiku-4-5")
	})
	tw(t, f, "stamps its children with the run id and keeps them out of the pool", func(t *testing.T) {
		// Ownership, not decoration: the stamp is what removes a workflow's agents from the fleet list, the widget and the
		// menus, and what keeps one fan-out from filling the session's concurrency pool.
		m := newStubManager(nil)
		hostWith(m, func(d *workflowHostDeps) { d.WorkflowID = "wf_run1" }).SpawnAgent(wfRequest(nil))
		// The stamp alone: the manager reads it to keep the child out of the pool, so no second opt-out is passed.
		eq(t, m.call(0).opts.WorkflowID, "wf_run1")
	})
	schema := compileWorkflowSchema(map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}})
	tw(t, f, "forwards a compiled schema and prefers the structured payload", func(t *testing.T) {
		// result is prose and picks up the worktree branch note on the way out; the caller asked for a schema and must get the payload.
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.Result, r.StructuredJSON = "here you go", `{"a":"x"}` }), nil
		})
		res, _ := hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Schema = schema.Compiled }))
		eq(t, m.call(0).opts.StructuredOutput, schema.Compiled)
		eq(t, [2]any{res.OK, res.Text}, [2]any{true, `{"a":"x"}`})
	})
	tw(t, f, "falls back to the prose when no schema was asked for", func(t *testing.T) {
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.Result = "here you go" }), nil
		})
		res, _ := hostWith(m, nil).SpawnAgent(wfRequest(nil))
		eq(t, m.call(0).opts.StructuredOutput == nil, true)
		eq(t, [2]any{res.OK, res.Text}, [2]any{true, "here you go"})
	})
	tw(t, f, "leaves the stamp off when no run id was injected", func(t *testing.T) {
		// An undefined stamp must not become the string "undefined" and quietly group unrelated agents.
		m := newStubManager(nil)
		hostWith(m, nil).SpawnAgent(wfRequest(nil))
		eq(t, m.call(0).opts.WorkflowID, "")
	})
	tw(t, f, "maps opts.effort onto the spawn's thinking level", func(t *testing.T) {
		m := newStubManager(nil)
		hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Effort = "high" }))
		eq(t, m.call(0).opts.ThinkingLevel, "high")
	})
	tw(t, f, "leaves thinkingLevel unset when no effort was asked for", func(t *testing.T) {
		// The agent definition's thinking resolves it downstream; sending a default would not be the same.
		m := newStubManager(nil)
		hostWith(m, nil).SpawnAgent(wfRequest(nil))
		eq(t, m.call(0).opts.ThinkingLevel, "")
	})
	tw(t, f, "surfaces a strict worktree-isolation failure as a failed agent, not a rejection", func(t *testing.T) {
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return workflowRecord{}, errors.New(`Cannot run with isolation: "worktree" — not a git repo`)
		})
		// A startup failure must land in the script as a null, so the rest of a fan-out keeps going.
		res, err := hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Isolation = "worktree" }))
		eq(t, err, nil)
		eq(t, res.OK, false)
		eq(t, strings.Contains(res.Error, `isolation: "worktree"`), true)
	})
	tw(t, f, "maps an errored record onto an error, and a stopped one onto skipped", func(t *testing.T) {
		failing := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.Status, r.Error = statusError, "boom" }), nil
		})
		stopped := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.Status = statusStopped }), nil
		})
		failed, _ := hostWith(failing, nil).SpawnAgent(wfRequest(nil))
		skipped, _ := hostWith(stopped, nil).SpawnAgent(wfRequest(nil))
		eq(t, [3]any{failed.OK, failed.Error, failed.Skipped}, [3]any{false, "boom", false})
		eq(t, [2]any{skipped.OK, skipped.Skipped}, [2]any{false, true})
	})
}

// scopeModels on the workflow spawn path. The policy itself (model-scope.ts checkModelScope, enabled-models.ts) is the
// settings lane's; the host asks it through workflowHostDeps.CheckScope and acts on the verdict. These cases keep the
// original's titles and run the host against a policy that implements the original's rules over the same fixtures.
func TestWorkflowHostScopeModels(t *testing.T) {
	const f = "workflow-tool"
	resetTypes()
	t.Cleanup(resetTypes)
	allowed := &workflowModel{ID: "allowed-model", Name: "Allowed", Provider: "anthropic"}
	blocked := &workflowModel{ID: "blocked-model", Name: "Blocked", Provider: "anthropic"}
	registry := fakeRegistry{[]*workflowModel{allowed, blocked}}
	// A stand-in for checkModelScope with enabledModels = ["anthropic/allowed-model"]: a model outside the list is an
	// error when the script named it and a warning otherwise.
	policy := func(on bool) func(workflowScopeInput) workflowScopeVerdict {
		return func(in workflowScopeInput) workflowScopeVerdict {
			if !on || in.Model == nil || (in.Model.Provider == "anthropic" && in.Model.ID == "allowed-model") {
				return workflowScopeVerdict{Kind: "ok"}
			}
			ref := in.Model.Provider + "/" + in.Model.ID
			if in.CallerSupplied {
				return workflowScopeVerdict{Kind: "error", Message: "Model not in scope: " + ref + ".\n\nEnabled models:\n  anthropic/allowed-model"}
			}
			return workflowScopeVerdict{Kind: "warn", Message: in.AgentLabel + " is using an out-of-scope model " + ref}
		}
	}
	var notes []string
	var noteMu sync.Mutex
	notify := func(msg, level string) { noteMu.Lock(); notes = append(notes, msg+"|"+level); noteMu.Unlock() }
	reset := func() { noteMu.Lock(); notes = nil; noteMu.Unlock() }
	pinnedAgent := func() {
		c := &agentConfig{Name: "pinned", DisplayName: "Pinned", Description: "an agent whose file pins a model", Model: "anthropic/blocked-model", SystemPrompt: "pinned",
			PromptMode: "replace", Enabled: true, Extensions: false, Skills: false}
		registerAgents(reg(c))
	}
	deps := func(on bool, parent *workflowModel) func(*workflowHostDeps) {
		return func(d *workflowHostDeps) {
			d.Registry, d.CheckScope, d.Notify = registry, policy(on), notify
			d.ParentModel = func() *workflowModel { return parent }
		}
	}

	tw(t, f, "refuses a model the script named that is out of scope, and fails only that agent", func(t *testing.T) {
		reset()
		m := newStubManager(nil)
		h := hostWith(m, deps(true, nil))
		res, _ := h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Model = "anthropic/blocked-model" }))
		// {ok: false} rather than a throw: the runtime turns this into null, so one refused model does not take siblings down.
		eq(t, res.OK, false)
		eq(t, strings.Contains(res.Error, "Model not in scope"), true)
		eq(t, strings.Contains(res.Error, "  anthropic/allowed-model"), true)
		eq(t, m.count(), 0)
		eq(t, len(notes), 0)
		in, _ := h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Model = "anthropic/allowed-model" }))
		eq(t, in.OK, true)
		eq(t, m.call(0).opts.Model, "anthropic/allowed-model")
	})
	tw(t, f, "warns but still spawns when the agent file pinned the out-of-scope model", func(t *testing.T) {
		// User-authored config, not a choice the script made: the model folds into modelInput, so keying callerSupplied off it would wrongly hard-error.
		reset()
		pinnedAgent()
		m := newStubManager(nil)
		res, _ := hostWith(m, deps(true, nil)).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentType = "pinned" }))
		eq(t, res.OK, true)
		eq(t, m.count(), 1)
		eq(t, m.call(0).opts.Model, "anthropic/blocked-model")
		eq(t, len(notes) == 1 && strings.Contains(notes[0], "out-of-scope model") && strings.HasSuffix(notes[0], "|warning"), true)
	})
	tw(t, f, "warns but still spawns on an inherited parent model", func(t *testing.T) {
		reset()
		m := newStubManager(nil)
		res, _ := hostWith(m, deps(true, blocked)).SpawnAgent(wfRequest(nil))
		eq(t, res.OK, true)
		eq(t, m.call(0).opts.Model, "anthropic/blocked-model")
		eq(t, len(notes) == 1 && strings.Contains(notes[0], "anthropic/blocked-model"), true)
	})
	tw(t, f, "toasts a repeated warning once, not once per child of a fan-out", func(t *testing.T) {
		reset()
		pinnedAgent()
		m := newStubManager(nil)
		h := hostWith(m, deps(true, nil))
		for i := 0; i < 3; i++ {
			res, _ := h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentID, r.AgentType = "wf-agent-"+string(rune('0'+i)), "pinned" }))
			eq(t, res.OK, true)
		}
		eq(t, m.count(), 3)
		eq(t, len(notes), 1)
	})
	tw(t, f, "leaves every spawn alone when scopeModels is off — the default", func(t *testing.T) {
		reset()
		pinnedAgent()
		m := newStubManager(nil)
		h := hostWith(m, deps(false, nil))
		named, _ := h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Model = "anthropic/blocked-model" }))
		pinned, _ := h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentID, r.AgentType = "wf-agent-1", "pinned" }))
		eq(t, [2]bool{named.OK, pinned.OK}, [2]bool{true, true})
		eq(t, m.count(), 2)
		eq(t, m.call(0).opts.Model, "anthropic/blocked-model")
		eq(t, len(notes), 0)
	})
}

func TestWorkflowHostWorktreeCwd(t *testing.T) {
	const f = "workflow-tool"
	resetTypes()
	t.Cleanup(resetTypes)
	tw(t, f, "reports the directory the child ran in, so a gate verifies that tree", func(t *testing.T) {
		worktree := t.TempDir()
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.WorktreePath = worktree }), nil
		})
		res, _ := hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Isolation = "worktree" }))
		eq(t, res.Cwd, worktree)
	})
	tw(t, f, "reports no cwd once the worktree has been torn down", func(t *testing.T) {
		// The manager commits the child's changes to a branch and REMOVES the copy before the spawn resolves. Handing that
		// path to a gate would fail every gated worktree agent with a spawn error instead of a test result.
		gone := filepath.Join(t.TempDir(), "already-removed")
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.WorktreePath = gone }), nil
		})
		res, _ := hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Isolation = "worktree" }))
		eq(t, res.Cwd, "")
	})
	tw(t, f, "reports no cwd for a child that never had a worktree", func(t *testing.T) {
		res, _ := hostWith(newStubManager(nil), nil).SpawnAgent(wfRequest(nil))
		eq(t, res.Cwd, "")
	})
}

func TestWorkflowHostAbortResumeGate(t *testing.T) {
	const f = "workflow-tool"
	resetTypes()
	t.Cleanup(resetTypes)
	tw(t, f, "aborts the manager record the runtime's agent id stands for", func(t *testing.T) {
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.ID = "manager-id-7" }), nil
		})
		h := hostWith(m, nil)
		h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentID = "wf-agent-3" }))
		h.AbortAgent("wf-agent-3")
		eq(t, m.aborts, []string{"manager-id-7"})
	})
	tw(t, f, "does not abort anything for an agent that never started", func(t *testing.T) {
		m := newStubManager(nil)
		hostWith(m, nil).AbortAgent("wf-agent-0")
		eq(t, len(m.aborts), 0)
	})
	tw(t, f, "resumes the same manager record, long after that agent settled", func(t *testing.T) {
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.ID = "manager-id-7" }), nil
		})
		h := hostWith(m, nil)
		h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.AgentID = "wf-agent-0" }))
		res, _ := h.ResumeAgent(context.Background(), "wf-agent-0", "and now this", nil)
		eq(t, m.resumes, [][2]string{{"manager-id-7", "and now this"}})
		eq(t, [2]any{res.OK, res.Text}, [2]any{true, "resumed"})
	})
	tw(t, f, "refuses to resume an agent the run never spawned", func(t *testing.T) {
		m := newStubManager(nil)
		res, _ := hostWith(m, nil).ResumeAgent(context.Background(), "wf-agent-9", "continue", nil)
		eq(t, res.OK, false)
		eq(t, len(m.resumes), 0)
	})
	tw(t, f, "asks the manager for a pre-cleanup hook only when the agent is gated", func(t *testing.T) {
		// The hook is how a gate reaches the child's worktree before it is deleted. An ungated agent must not acquire one:
		// the manager's settle path is shared with every other spawn.
		m := newStubManager(nil)
		h := hostWith(m, nil)
		h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Isolation = "worktree" }))
		eq(t, m.call(0).opts.OnBeforeWorktreeCleanup == nil, true)
		h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Isolation, r.Gate = "worktree", "npm test" }))
		eq(t, m.call(1).opts.OnBeforeWorktreeCleanup != nil, true)
	})
	gateHost := func(e *execStub, cwd string) *workflowAgentHost {
		return hostWith(newStubManager(nil), func(d *workflowHostDeps) { d.Exec, d.Cwd = e.exec, cwd })
	}
	tw(t, f, "runs a gate through pi.exec in the child's own tree", func(t *testing.T) {
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{Stdout: "3 passing"}, nil }}
		gate, _ := gateHost(e, "/session").RunGate(context.Background(), "npm test", "wf-agent-0", "/worktree")
		eq(t, gate, workflowGateResult{OK: true, Output: "3 passing"})
		eq(t, e.all()[0].cwd, "/worktree")
	})
	tw(t, f, "falls back to the session's cwd when the child had no tree of its own", func(t *testing.T) {
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{}, nil }}
		gateHost(e, "/session").RunGate(context.Background(), "npm test", "wf-agent-0", "")
		eq(t, e.all()[0].cwd, "/session")
	})
	tw(t, f, "fails a gate on a non-zero exit and surfaces its output", func(t *testing.T) {
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{Code: 1, Stderr: "1 failing"}, nil }}
		gate, _ := gateHost(e, "/session").RunGate(context.Background(), "npm test", "wf-agent-0", "")
		eq(t, gate, workflowGateResult{Output: "1 failing"})
	})
	tw(t, f, "fails a gate that was killed, which pi.exec reports with exit code 0", func(t *testing.T) {
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{Killed: true}, nil }}
		gate, _ := gateHost(e, "/session").RunGate(context.Background(), "sleep 999", "wf-agent-0", "")
		eq(t, gate.OK, false)
		eq(t, strings.Contains(gate.Output, "timed out"), true)
	})
}

// the gate on an isolated child: the stub plays the manager's settle.
func TestWorkflowGateWorktree(t *testing.T) {
	const f = "workflow-gate-worktree"
	resetTypes()
	t.Cleanup(resetTypes)
	// isolatedChild plays an isolated child's whole life: the worktree is created and the child writes into it, the
	// record is settled, the pre-cleanup hook runs while the copy still exists, and the copy is then removed.
	isolatedChild := func(t *testing.T, status, failure string) (*stubManager, *string) {
		var path string
		m := newStubManager(nil)
		m.settle = func(c *wfSpawnCall, spawned func(workflowRecord)) (workflowRecord, error) {
			path = filepath.Join(t.TempDir(), "worktree")
			_ = os.MkdirAll(path, 0o755)
			rec := record(func(r *workflowRecord) { r.Status = statusRunning; r.WorktreePath = path; r.Result = "" })
			spawned(rec)
			_ = os.WriteFile(filepath.Join(path, childFile), []byte("the child wrote this"), 0o644)
			rec.Status, rec.Result, rec.Error = status, "done", failure
			if failure != "" {
				rec.Result = ""
			}
			m.store(rec)
			if c.opts.OnBeforeWorktreeCleanup != nil {
				_ = c.opts.OnBeforeWorktreeCleanup(path)
			}
			_ = os.RemoveAll(path) // the manager commits the work to a branch and removes the copy inside the child's own settle
			return rec, nil
		}
		return m, &path
	}
	gated := func(mod func(*workflowSpawnRequest)) *workflowSpawnRequest {
		return wfRequest(func(r *workflowSpawnRequest) {
			r.Isolation, r.Gate = "worktree", "npm test"
			if mod != nil {
				mod(r)
			}
		})
	}
	runScript := func(t *testing.T, body string, h workflowHost) *workflowRunResult {
		return wfRun(t, body, func(o *workflowRunOptions) { o.Host = h })
	}

	tw(t, f, "runs the gate inside the child's worktree, while it still exists", func(t *testing.T) {
		m, path := isolatedChild(t, statusCompleted, "")
		e := &execStub{}
		res, _ := hostWith(m, func(d *workflowHostDeps) { d.Exec, d.Cwd = e.exec, "/repo" }).SpawnAgent(gated(nil))
		runs := e.all()
		eq(t, len(runs), 1)
		// The exact path, not merely "something was set": the whole failure mode is a gate that ran somewhere plausible and wrong.
		eq(t, runs[0].cwd, *path)
		eq(t, runs[0].cwd == "/repo", false)
		eq(t, [2]bool{runs[0].existed, runs[0].sawChildWork}, [2]bool{true, true})
		eq(t, res.OK, true)
		eq(t, *res.Gate, workflowGateResult{OK: true, Output: "3 passing"})
		// ...and the copy is still torn down afterwards: verifying it must not keep it.
		_, err := os.Stat(*path)
		eq(t, os.IsNotExist(err), true)
	})
	tw(t, f, "fails the agent with the gate's output, and still cleans the worktree up", func(t *testing.T) {
		m, path := isolatedChild(t, statusCompleted, "")
		e := &execStub{gate: func(string) (workflowExecResult, error) {
			return workflowExecResult{Code: 1, Stderr: "FAIL src/auth.test.ts\n1 failing"}, nil
		}}
		h := hostWith(m, func(d *workflowHostDeps) { d.Exec, d.Cwd = e.exec, "/repo" })
		res := runScript(t, `return await agent("fix it", { gate: "npm test", isolation: "worktree" });`, h)
		eq(t, res.Status, "completed")
		// The documented shape: a failed gate is a failed agent, the command output is the error, and the script sees null.
		eq(t, res.Value, nil)
		last := wfLastRow(res.Progress)
		eq(t, [2]string{last.State, last.Error}, [2]string{"error", "FAIL src/auth.test.ts\n1 failing"})
		// Exactly one execution: the host ran it in the worktree, so the runtime must not run it again against the session's tree.
		runs := e.all()
		eq(t, len(runs), 1)
		eq(t, runs[0].cwd, *path)
		eq(t, runs[0].sawChildWork, true)
		_, err := os.Stat(runs[0].cwd)
		eq(t, os.IsNotExist(err), true)
	})
	tw(t, f, "names the command when a failing gate in a worktree says nothing", func(t *testing.T) {
		m, _ := isolatedChild(t, statusCompleted, "")
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{Stdout: "  ", Code: 1}, nil }}
		h := hostWith(m, func(d *workflowHostDeps) { d.Exec = e.exec })
		res := runScript(t, `return await agent("x", { gate: "npm run lint", isolation: "worktree" });`, h)
		eq(t, wfLastRow(res.Progress).Error, "Gate command failed: npm run lint")
	})
	tw(t, f, "counts a killed gate as failed even though pi.exec reports exit code 0", func(t *testing.T) {
		m, _ := isolatedChild(t, statusCompleted, "")
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{Killed: true}, nil }}
		res, _ := hostWith(m, func(d *workflowHostDeps) { d.Exec = e.exec }).SpawnAgent(gated(func(r *workflowSpawnRequest) { r.Gate = "sleep 999" }))
		eq(t, res.Gate.OK, false)
		eq(t, strings.Contains(res.Gate.Output, "timed out"), true)
	})
	tw(t, f, "treats a gate that could not run at all as a failed gate, not an un-run one", func(t *testing.T) {
		// If this leaked as an exception the manager would swallow it, the spawn result would carry no verdict, and the
		// runtime would helpfully re-run the command — in the session's tree, which is the bug.
		m, path := isolatedChild(t, statusCompleted, "")
		e := &execStub{gate: func(string) (workflowExecResult, error) { return workflowExecResult{}, errors.New("spawn sh ENOENT") }}
		h := hostWith(m, func(d *workflowHostDeps) { d.Exec = e.exec })
		res := runScript(t, `return await agent("x", { gate: "npm test", isolation: "worktree" });`, h)
		eq(t, res.Value, nil)
		eq(t, wfLastRow(res.Progress).Error, "spawn sh ENOENT")
		eq(t, len(e.all()), 1)
		_, err := os.Stat(*path)
		eq(t, os.IsNotExist(err), true)
	})
	tw(t, f, "gates a steered child too — a steer is a finished child, not a failed one", func(t *testing.T) {
		m, path := isolatedChild(t, statusSteered, "")
		e := &execStub{}
		res, _ := hostWith(m, func(d *workflowHostDeps) { d.Exec = e.exec }).SpawnAgent(gated(nil))
		eq(t, res.OK, true)
		runs := e.all()
		eq(t, len(runs), 1)
		eq(t, [2]any{runs[0].cwd, runs[0].sawChildWork}, [2]any{*path, true})
	})
	tw(t, f, "skips the gate for a child that failed, and reports the child's own failure", func(t *testing.T) {
		m, path := isolatedChild(t, statusError, "provider exploded")
		e := &execStub{}
		res, _ := hostWith(m, func(d *workflowHostDeps) { d.Exec = e.exec }).SpawnAgent(gated(nil))
		eq(t, [2]any{res.OK, res.Error}, [2]any{false, "provider exploded"})
		eq(t, res.Gate == nil, true)
		eq(t, len(e.all()), 0)
		_, err := os.Stat(*path)
		eq(t, os.IsNotExist(err), true)
	})
	tw(t, f, "runs the gate once, in the session's own tree", func(t *testing.T) {
		// A child with no worktree of its own: the session's tree is the tree the child edited.
		repo := t.TempDir()
		m := newStubManager(func(*wfSpawnCall, func(workflowRecord)) (workflowRecord, error) {
			_ = os.WriteFile(filepath.Join(repo, childFile), []byte("the child wrote this"), 0o644)
			return record(func(r *workflowRecord) { r.Result = "done" }), nil
		})
		e := &execStub{}
		h := hostWith(m, func(d *workflowHostDeps) { d.Exec, d.Cwd = e.exec, repo })
		res := runScript(t, `return await agent("x", { gate: "npm test" });`, h)
		eq(t, res.Value, "done")
		runs := e.all()
		eq(t, len(runs), 1)
		eq(t, runs[0].cwd, repo)
		rec, _ := m.GetRecord("agent-1")
		eq(t, rec.WorktreePath, "")
	})
	tskip(t, f, "is untouched: nothing runs, and the work still lands on a branch", "asserts the real manager's git behavior (the child's file committed to a branch before its worktree is removed), which is the worktree lane's manager.go/worktree.go and needs a real git repo; the host half (no hook for an ungated agent, no gate run) is the 'asks the manager for a pre-cleanup hook only when the agent is gated' case")
}

// the host reports a child's effective configuration.
func TestWorkflowEffectiveConfig(t *testing.T) {
	const f = "workflow-effective-config"
	resetTypes()
	t.Cleanup(resetTypes)
	// configCollector collects only the reports that say something about the child's EFFECTIVE configuration: the record
	// id fires once more, ahead of any session, and is stripped here (it has its own case at the bottom).
	collect := func(into *[]workflowResolvedInfo) func(workflowResolvedInfo) {
		return func(info workflowResolvedInfo) {
			info.RecordID = ""
			if info != (workflowResolvedInfo{}) {
				*into = append(*into, info)
			}
		}
	}
	// childSessionReports stands in for pi resolving the child's session: the manager writes the invocation onto the
	// record and then fires onSessionCreated, so firing this is what makes the invocation populated by the time the host reads it.
	childSessionReports := func(model *workflowModel, thinking string) *stubManager {
		return newStubManager(func(c *wfSpawnCall, spawned func(workflowRecord)) (workflowRecord, error) {
			rec := record(func(r *workflowRecord) {
				if model != nil {
					name, id := wfDescribeModel(*model)
					r.Invocation = &workflowInvocation{ModelName: name, ModelID: id, Thinking: thinking}
					if c.opts.Invocation.Thinking != "" && c.opts.Invocation.Thinking != thinking {
						r.Invocation.RequestedThinking = c.opts.Invocation.Thinking
					}
				}
			})
			spawned(rec) // the manager registers the record when the child starts
			c.opts.OnSessionCreated()
			return rec, nil
		})
	}
	tw(t, f, "hands over the model the session actually resolved to, not the script's spelling", func(t *testing.T) {
		haiku := &workflowModel{Provider: "anthropic", ID: "claude-haiku-4-5", Name: "Haiku 4.5"}
		m := childSessionReports(haiku, "")
		var reported []workflowResolvedInfo
		hostWith(m, func(d *workflowHostDeps) { d.Registry = fakeRegistry{[]*workflowModel{haiku}} }).SpawnAgent(
			wfRequest(func(r *workflowSpawnRequest) { r.Model, r.OnResolved = "haiku", collect(&reported) }))
		eq(t, len(reported), 1)
		eq(t, reported[0].ModelID, "anthropic/claude-haiku-4-5")
		eq(t, reported[0].ModelName != "", true)
	})
	tw(t, f, "reports for an agent that named no model — the inherited case", func(t *testing.T) {
		m := childSessionReports(&workflowModel{Provider: "anthropic", ID: "claude-sonnet-4-6"}, "")
		var reported []workflowResolvedInfo
		hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.OnResolved = collect(&reported) }))
		// This is the one #168 was filed about: nothing was requested, so without the read-back there is nothing to render.
		eq(t, reported[0].ModelID, "anthropic/claude-sonnet-4-6")
	})
	tw(t, f, "discloses a thinking level pi clamped below what was asked for (#182)", func(t *testing.T) {
		m := childSessionReports(&workflowModel{Provider: "anthropic", ID: "claude-haiku-4-5"}, "low")
		var reported []workflowResolvedInfo
		hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Effort, r.OnResolved = "max", collect(&reported) }))
		// Both halves, or the row cannot say "low (asked max)".
		eq(t, [2]string{reported[0].Thinking, reported[0].RequestedThinking}, [2]string{"low", "max"})
	})
	tw(t, f, "lets the script's model outrank the agent file's, so there is nothing to disclose", func(t *testing.T) {
		// The workflow path resolves request.model ?? config?.model, so the script outranks the agent file and therefore
		// always got the model it asked for: seeding a requestedModel would describe a precedence that does not exist.
		haiku := &workflowModel{Provider: "anthropic", ID: "claude-haiku-4-5", Name: "Haiku 4.5"}
		opus := &workflowModel{Provider: "anthropic", ID: "claude-opus-4-6", Name: "Opus 4.6"}
		registerAgents(reg(&agentConfig{Name: "pinned", Model: "anthropic/claude-opus-4-6", Description: "d", SystemPrompt: "p", PromptMode: "replace", Enabled: true}))
		m := childSessionReports(haiku, "")
		var reported []workflowResolvedInfo
		hostWith(m, func(d *workflowHostDeps) { d.Registry = fakeRegistry{[]*workflowModel{haiku, opus}} }).SpawnAgent(
			wfRequest(func(r *workflowSpawnRequest) {
				r.AgentType, r.Model, r.OnResolved = "pinned", "haiku", collect(&reported)
			}))
		// The script's "haiku" won over the file's pinned opus...
		eq(t, m.call(0).opts.Model, "anthropic/claude-haiku-4-5")
		// ...so nothing was overridden, and nothing is disclosed.
		eq(t, reported[0].RequestedModel, "")
	})
	tw(t, f, "says nothing about a level that was honoured", func(t *testing.T) {
		m := childSessionReports(&workflowModel{Provider: "anthropic", ID: "claude-haiku-4-5"}, "low")
		var reported []workflowResolvedInfo
		hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Effort, r.OnResolved = "low", collect(&reported) }))
		eq(t, reported[0].RequestedThinking, "")
	})
	tw(t, f, "does not fire when the session never reports a model", func(t *testing.T) {
		// A stubbed or older session must degrade to "nothing to say" rather than firing an empty report.
		m := childSessionReports(nil, "")
		var reported []workflowResolvedInfo
		hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.OnResolved = collect(&reported) }))
		eq(t, len(reported), 0)
	})
	tw(t, f, "reports the record id ahead of any session, so the row is openable either way", func(t *testing.T) {
		// The id is knowable as soon as the manager issues it, and gating it on the session would leave exactly the
		// children worth reading, the ones that died in startup, unopenable.
		m := newStubManager(nil)
		var reported []workflowResolvedInfo
		hostWith(m, nil).SpawnAgent(wfRequest(func(r *workflowSpawnRequest) {
			r.OnResolved = func(i workflowResolvedInfo) { reported = append(reported, i) }
		}))
		eq(t, len(reported), 1)
		// The id the manager issued, and not the run's own wf-agent-0 handle.
		eq(t, reported[0].RecordID != "" && reported[0].RecordID != "wf-agent-0", true)
		_, ok := m.GetRecord(reported[0].RecordID)
		eq(t, ok, true)
	})
}
