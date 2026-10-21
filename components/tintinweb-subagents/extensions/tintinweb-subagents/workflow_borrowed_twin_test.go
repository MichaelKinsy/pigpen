package tintinweb_subagents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of test/workflow-borrowed.test.ts: gate, resume and unawaited-launch detection.

type gateCall struct{ command, agentID, cwd string }
type resumeCall struct{ agentID, prompt string }

// borrowedHost is a stub host with the two optional host methods, both recorded rather than simulated.
type borrowedHost struct {
	mu        sync.Mutex
	calls     []*workflowSpawnRequest
	gateCalls []gateCall
	resumes   []resumeCall
	aborted   []string
	reply     func(*workflowSpawnRequest) workflowSpawnResult
	gate      func(command string) workflowGateResult
	resume    func(resumeCall) workflowSpawnResult
}

func (b *borrowedHost) SpawnAgent(r *workflowSpawnRequest) (workflowSpawnResult, error) {
	b.mu.Lock()
	b.calls = append(b.calls, r)
	b.mu.Unlock()
	if b.reply != nil {
		return b.reply(r), nil
	}
	return workflowSpawnResult{OK: true, Text: "ok:" + r.Prompt}, nil
}

func (b *borrowedHost) AbortAgent(id string) {
	b.mu.Lock()
	b.aborted = append(b.aborted, id)
	b.mu.Unlock()
}

func (b *borrowedHost) RunGate(_ context.Context, command, agentID, cwd string) (workflowGateResult, error) {
	b.mu.Lock()
	b.gateCalls = append(b.gateCalls, gateCall{command, agentID, cwd})
	b.mu.Unlock()
	if b.gate != nil {
		return b.gate(command), nil
	}
	return workflowGateResult{OK: true}, nil
}

func (b *borrowedHost) ResumeAgent(_ context.Context, agentID, prompt string, _ func(workflowResolvedInfo)) (workflowSpawnResult, error) {
	b.mu.Lock()
	b.resumes = append(b.resumes, resumeCall{agentID, prompt})
	b.mu.Unlock()
	if b.resume != nil {
		return b.resume(resumeCall{agentID, prompt}), nil
	}
	return workflowSpawnResult{OK: true, Text: "resumed:" + prompt}, nil
}

func (b *borrowedHost) snapshot() (calls []*workflowSpawnRequest, gates []gateCall, resumes []resumeCall, aborted []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append(calls, b.calls...), append(gates, b.gateCalls...), append(resumes, b.resumes...), append(aborted, b.aborted...)
}

// spawnOnly is a host with neither gate nor resume nor loader.
type spawnOnly struct{ h workflowHost }

func (s spawnOnly) SpawnAgent(r *workflowSpawnRequest) (workflowSpawnResult, error) {
	return s.h.SpawnAgent(r)
}
func (s spawnOnly) AbortAgent(id string) { s.h.AbortAgent(id) }

// noResume is a host that can gate but not resume.
type noResume struct {
	spawnOnly
	g workflowGateRunner
}

func (n noResume) RunGate(ctx context.Context, c, a, cwd string) (workflowGateResult, error) {
	return n.g.RunGate(ctx, c, a, cwd)
}

// latest is the last entry written for an index: the log is append-only, last write wins.
func latest(progress []workflowEntry, index int) *workflowAgentEntry {
	var out *workflowAgentEntry
	for _, a := range wfAgentRows(progress) {
		if a.Index == index {
			out = a
		}
	}
	return out
}

func hang(r *workflowSpawnRequest) workflowSpawnResult { <-r.Ctx.Done(); return workflowSpawnResult{} }

func TestWorkflowBorrowed(t *testing.T) {
	const f = "workflow-borrowed"
	opts := func(h workflowHost) func(*workflowRunOptions) { return func(o *workflowRunOptions) { o.Host = h } }

	// gate
	tw(t, f, "turns a failing gate into a failed agent, with the command output as the error", func(t *testing.T) {
		h := &borrowedHost{gate: func(string) workflowGateResult { return workflowGateResult{Output: "FAIL src/auth.test.ts\n1 failing"} }}
		res := wfRun(t, `return await agent("fix the auth test", { gate: "npm test" });`, opts(h))
		eq(t, res.Status, "completed")
		// Not a new state and not a new entry type: a gated agent whose command fails is a failed agent.
		e := latest(res.Progress, 0)
		eq(t, e.State, "error")
		eq(t, e.Error, "FAIL src/auth.test.ts\n1 failing")
		eq(t, e.Skipped, false)
		// ...and the script sees the same null a dead agent produces.
		eq(t, res.Value, nil)
		_, gates, _, _ := h.snapshot()
		eq(t, gates, []gateCall{{"npm test", "wf-agent-0", ""}})
	})
	tw(t, f, "leaves a passing gate's agent done, with its value intact", func(t *testing.T) {
		h := &borrowedHost{gate: func(string) workflowGateResult { return workflowGateResult{OK: true, Output: "42 passing"} }}
		res := wfRun(t, `return await agent("fix it", { gate: "npm test" });`, opts(h))
		eq(t, res.Status, "completed")
		eq(t, res.Value, "ok:fix it")
		e := latest(res.Progress, 0)
		eq(t, e.State, "done")
		eq(t, e.Error, "")
		_, gates, _, _ := h.snapshot()
		eq(t, len(gates), 1)
	})
	tw(t, f, "keeps the agent's own accounting when the gate rejects the work", func(t *testing.T) {
		h := &borrowedHost{
			reply: func(*workflowSpawnRequest) workflowSpawnResult {
				return workflowSpawnResult{OK: true, Text: "done", Tokens: new(1234), ToolCalls: new(7)}
			},
			gate: func(string) workflowGateResult { return workflowGateResult{Output: "boom"} }}
		res := wfRun(t, `return await agent("x", { gate: "npm test" });`, opts(h))
		e := latest(res.Progress, 0)
		eq(t, *e.Tokens, 1234)
		eq(t, *e.ToolCalls, 7)
		eq(t, e.ResultPreview, "")
	})
	tw(t, f, "names the command when a failing gate says nothing", func(t *testing.T) {
		h := &borrowedHost{gate: func(string) workflowGateResult { return workflowGateResult{Output: "   "} }}
		res := wfRun(t, `return await agent("x", { gate: "npm run lint" });`, opts(h))
		eq(t, latest(res.Progress, 0).Error, "Gate command failed: npm run lint")
	})
	tw(t, f, "does not run for an agent that already failed", func(t *testing.T) {
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult { return workflowSpawnResult{Error: "child exploded"} }}
		res := wfRun(t, `return await agent("x", { gate: "npm test" });`, opts(h))
		// Gating a dead agent verifies nothing and can only overwrite the real reason it died.
		_, gates, _, _ := h.snapshot()
		eq(t, len(gates), 0)
		eq(t, latest(res.Progress, 0).Error, "child exploded")
		eq(t, res.Value, nil)
	})
	tw(t, f, "does not run for an agent the user skipped", func(t *testing.T) {
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{Skipped: true, Error: "Skipped by user."}
		}}
		res := wfRun(t, `return await agent("x", { gate: "npm test" });`, opts(h))
		_, gates, _, _ := h.snapshot()
		eq(t, len(gates), 0)
		e := latest(res.Progress, 0)
		eq(t, [2]any{e.State, e.Skipped}, [2]any{"error", true})
	})
	tw(t, f, "runs inside the child's worktree when it has one", func(t *testing.T) {
		h := &borrowedHost{reply: func(r *workflowSpawnRequest) workflowSpawnResult {
			if r.Isolation == "worktree" {
				return workflowSpawnResult{OK: true, Text: "done", Cwd: "/tmp/wt-review"}
			}
			return workflowSpawnResult{OK: true, Text: "done"}
		}}
		wfRun(t, strings.Join([]string{`await agent("in a worktree", { gate: "npm test", isolation: "worktree" });`,
			`await agent("in the main tree", { gate: "npm test" });`, "return null;"}, "\n"), func(o *workflowRunOptions) { o.Host = h; o.Concurrency = 1 })
		// Gating the main tree for a worktree child would verify code that child never touched.
		_, gates, _, _ := h.snapshot()
		eq(t, gates, []gateCall{{"npm test", "wf-agent-0", "/tmp/wt-review"}, {"npm test", "wf-agent-1", ""}})
	})
	tw(t, f, "offers the command to the host, and shapes the verdict a host that ran it reports", func(t *testing.T) {
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "done", Gate: &workflowGateResult{Output: "1 failing"}}
		}}
		res := wfRun(t, `return await agent("x", { gate: "npm test", isolation: "worktree" });`, opts(h))
		calls, gates, _, _ := h.snapshot()
		eq(t, calls[0].Gate, "npm test")
		eq(t, len(gates), 0) // what must not happen is a second execution
		eq(t, res.Value, nil)
		eq(t, latest(res.Progress, 0).Error, "1 failing")
	})
	tw(t, f, "keeps an agent done when the host's own gate run passed", func(t *testing.T) {
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "42", Gate: &workflowGateResult{OK: true, Output: "3 passing"}}
		}}
		res := wfRun(t, `return await agent("x", { gate: "npm test", isolation: "worktree" });`, opts(h))
		eq(t, res.Value, "42")
		eq(t, latest(res.Progress, 0).State, "done")
		_, gates, _, _ := h.snapshot()
		eq(t, len(gates), 0)
	})
	tw(t, f, "names the command when a gate the host ran fails silently", func(t *testing.T) {
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "done", Gate: &workflowGateResult{Output: "  "}}
		}}
		res := wfRun(t, `return await agent("x", { gate: "npm test" });`, opts(h))
		eq(t, latest(res.Progress, 0).Error, "Gate command failed: npm test")
	})
	tw(t, f, "offers nothing to the host for an ungated agent", func(t *testing.T) {
		h := &borrowedHost{}
		wfRun(t, `return await agent("x");`, opts(h))
		calls, _, _, _ := h.snapshot()
		eq(t, calls[0].Gate, "")
	})
	tw(t, f, "rejects an empty gate command", func(t *testing.T) {
		h := &borrowedHost{}
		res := wfRun(t, `return await agent("x", { gate: "  " });`, opts(h))
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "agent() opts.gate requires a non-empty string.") {
			t.Fatal(res.Error)
		}
		calls, _, _, _ := h.snapshot()
		eq(t, len(calls), 0)
	})
	tw(t, f, "fails the run rather than skipping the gate when the host cannot run one", func(t *testing.T) {
		res := wfRun(t, `return await agent("x", { gate: "npm test" });`, opts(spawnOnly{&borrowedHost{}}))
		eq(t, res.Status, "failed")
		eq(t, res.Error, "This workflow host cannot run gate commands.")
	})

	// resume
	tw(t, f, "continues the child that ran under the label, and still reports progress", func(t *testing.T) {
		h := &borrowedHost{}
		res := wfRun(t, strings.Join([]string{
			`const first = await agent("write the parser", { label: "impl", agentType: "Explore", model: "haiku" });`,
			`const second = await agent("now handle escapes", { resume: "impl" });`, "return [first, second];"}, "\n"), opts(h))
		eq(t, res.Status, "completed")
		eq(t, res.Value, []any{"ok:write the parser", "resumed:now handle escapes"})
		calls, _, resumes, _ := h.snapshot()
		// One spawn, one resume — the second call did not pay for a fresh context.
		eq(t, len(calls), 1)
		eq(t, resumes, []resumeCall{{"wf-agent-0", "now handle escapes"}})
		second := latest(res.Progress, 1)
		eq(t, second.State, "done")
		// Same child, so the same agent id — which is also what makes abort reach it.
		eq(t, second.AgentID, "wf-agent-0")
		// The revived child keeps the contract it was started with, and the row says so.
		eq(t, [4]string{second.AgentType, second.Model, second.Label, second.PromptPreview}, [4]string{"Explore", "haiku", "impl", "now handle escapes"})
		n := 0
		for _, a := range wfAgentRows(res.Progress) {
			if a.Index == 1 {
				n++
			}
		}
		eq(t, n, 3) // the same start → done lifecycle as any other agent
	})
	tw(t, f, "holds a semaphore slot like any other agent", func(t *testing.T) {
		var mu sync.Mutex
		active, peak := 0, 0
		occupy := func() workflowSpawnResult {
			mu.Lock()
			active++
			peak = max(peak, active)
			mu.Unlock()
			time.Sleep(15 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return workflowSpawnResult{OK: true, Text: "done"}
		}
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult { return occupy() }, resume: func(resumeCall) workflowSpawnResult { return occupy() }}
		res := wfRun(t, strings.Join([]string{`await agent("seed", { label: "impl" });`, "await parallel([", `  () => agent("more", { resume: "impl" }),`,
			`  () => agent("fresh one"),`, "]);", "return null;"}, "\n"), func(o *workflowRunOptions) { o.Host = h; o.Concurrency = 1 })
		eq(t, res.Status, "completed")
		eq(t, peak, 1)
	})
	tw(t, f, "resumes a child whose gate rejected its work", func(t *testing.T) {
		h := &borrowedHost{gate: func(string) workflowGateResult { return workflowGateResult{Output: "1 failing"} }}
		res := wfRun(t, strings.Join([]string{`const first = await agent("fix it", { label: "impl", gate: "npm test" });`,
			`const second = await agent("still failing, try again", { resume: "impl" });`, "return [first, second];"}, "\n"), opts(h))
		eq(t, res.Value, []any{nil, "resumed:still failing, try again"})
		_, _, resumes, _ := h.snapshot()
		eq(t, len(resumes), 1)
	})
	tw(t, f, "names the label and lists the known ones when it does not exist", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{`await agent("a", { label: "scan" });`, `await agent("b", { label: "impl" });`,
			`await agent("c", { resume: "implement" });`, "return null;"}, "\n"), opts(&borrowedHost{}))
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, `the label "implement"`) || !strings.Contains(res.Error, `Known labels: "scan", "impl".`) {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "says so when nothing has completed yet", func(t *testing.T) {
		res := wfRun(t, `return await agent("c", { resume: "impl" });`, opts(&borrowedHost{}))
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "No agent has completed yet.") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "does not offer a failed child as a resume target", func(t *testing.T) {
		h := &borrowedHost{reply: func(*workflowSpawnRequest) workflowSpawnResult { return workflowSpawnResult{Error: "child exploded"} }}
		res := wfRun(t, strings.Join([]string{`await agent("a", { label: "impl" });`, `await agent("b", { resume: "impl" });`, "return null;"}, "\n"), opts(h))
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "No agent has completed yet.") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "fails the run rather than starting a fresh child when the host cannot resume", func(t *testing.T) {
		h := &borrowedHost{}
		res := wfRun(t, strings.Join([]string{`await agent("a", { label: "impl" });`, `await agent("b", { resume: "impl" });`, "return null;"}, "\n"),
			opts(noResume{spawnOnly{h}, h}))
		eq(t, res.Status, "failed")
		eq(t, res.Error, "This workflow host cannot resume agents.")
	})
	// guardrails
	tw(t, f, "rejects resume combined with ${name}", func(t *testing.T) {
		for _, c := range [][3]string{
			{"gate", "await agent(\"a\", { label: \"impl\" });\nawait agent(\"b\", { resume: \"impl\", gate: \"npm test\" });", "agent() opts.gate cannot be combined with opts.resume."},
			{"agentType", `await agent("b", { resume: "impl", agentType: "Explore" });`, "agent() opts.resume and opts.agentType are mutually exclusive"},
			{"model", `await agent("b", { resume: "impl", model: "haiku" });`, "agent() opts.resume and opts.model are mutually exclusive"},
			{"isolation", `await agent("b", { resume: "impl", isolation: "worktree" });`, "agent() opts.resume and opts.isolation are mutually exclusive"},
			{"effort", `await agent("b", { resume: "impl", effort: "high" });`, "agent() opts.resume and opts.effort are mutually exclusive"},
		} {
			t.Run("rejects resume combined with "+c[0], func(t *testing.T) {
				h := &borrowedHost{}
				res := wfRun(t, c[1]+"\nreturn null;", opts(h))
				eq(t, res.Status, "failed")
				if !strings.Contains(res.Error, c[2]) {
					t.Fatal(res.Error)
				}
				_, _, resumes, _ := h.snapshot()
				eq(t, len(resumes), 0)
			})
		}
	})
	tw(t, f, "rejects an empty resume label", func(t *testing.T) {
		res := wfRun(t, `return await agent("b", { resume: "" });`, opts(&borrowedHost{}))
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "agent() opts.resume requires a non-empty string.") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "rejects a resume with no follow-up task", func(t *testing.T) {
		h := &borrowedHost{}
		res := wfRun(t, strings.Join([]string{`await agent("a", { label: "impl" });`, `await agent("   ", { resume: "impl" });`, "return null;"}, "\n"), opts(h))
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "agent(prompt) requires a non-empty string.") {
			t.Fatal(res.Error)
		}
		_, _, resumes, _ := h.snapshot()
		eq(t, len(resumes), 0)
	})

	// unawaited launches
	tw(t, f, "fails the run and aborts the child when a launch is dropped", func(t *testing.T) {
		h := &borrowedHost{reply: hang}
		res := wfRun(t, strings.Join([]string{`agent("scan the repo", { label: "scan" });`, `return "done";`}, "\n"), opts(h))
		// Nobody is waiting for that agent's answer, so finishing "successfully" would hand back a result assembled from thrown-away work.
		eq(t, res.Status, "failed")
		eq(t, res.Error, "workflow script completed with unawaited agent launch(es): 'scan'. Await or return each launch.")
		eq(t, res.Value, nil)
		eq(t, res.ValueJSON == nil, true)
		_, _, _, aborted := h.snapshot()
		eq(t, aborted, []string{"wf-agent-0"})
	})
	tw(t, f, "lists every dropped launch, in call order", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{`agent("a", { label: "label-a" });`, `agent("b", { label: "label-b" });`, "return null;"}, "\n"),
			func(o *workflowRunOptions) { o.Host = &borrowedHost{reply: hang}; o.Concurrency = 4 })
		eq(t, res.Error, "workflow script completed with unawaited agent launch(es): 'label-a', 'label-b'. Await or return each launch.")
	})
	tw(t, f, "does not fire for a script that awaits everything", func(t *testing.T) {
		h := &borrowedHost{reply: func(r *workflowSpawnRequest) workflowSpawnResult {
			if r.Prompt == "slow" {
				time.Sleep(40 * time.Millisecond)
			}
			return workflowSpawnResult{OK: true, Text: "ok:" + r.Prompt}
		}}
		res := wfRun(t, strings.Join([]string{`const held = agent("slow");`, `const values = await parallel([() => agent("p1"), () => agent("p2")]);`,
			`const out = await pipeline(["x"], async (item) => await agent("s1:" + item));`, "return [await held, values, out];"}, "\n"),
			func(o *workflowRunOptions) { o.Host = h; o.Concurrency = 4 })
		eq(t, res.Status, "completed")
		eq(t, res.Value, []any{"ok:slow", []any{"ok:p1", "ok:p2"}, []any{"ok:s1:x"}})
	})
	tw(t, f, "does not fire for a launch that settled before the script returned", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{`agent("fire and forget");`, `await agent("awaited");`, `return "done";`}, "\n"),
			func(o *workflowRunOptions) { o.Host = &borrowedHost{}; o.Concurrency = 4 })
		eq(t, res.Status, "completed")
		eq(t, res.Value, "done")
	})
	tw(t, f, "does not report unawaited launches when the run was aborted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{})
		var once sync.Once
		go func() { <-started; cancel() }()
		res := wfRun(t, strings.Join([]string{`agent("dropped", { label: "drop" });`, `await agent("blocks forever");`, "return null;"}, "\n"), func(o *workflowRunOptions) {
			o.Host, o.Concurrency, o.Ctx = &borrowedHost{reply: hang}, 4, ctx
			o.OnProgress = func(es []workflowEntry) {
				for _, e := range es {
					if a, ok := e.(*workflowAgentEntry); ok && a.StartedAt != nil {
						once.Do(func() { close(started) })
					}
				}
			}
		})
		// A killed run has in-flight agents by definition; calling that a script bug would blame the user for pressing stop.
		eq(t, res.Status, "killed")
		eq(t, res.Error, "Workflow aborted.")
	})
}
