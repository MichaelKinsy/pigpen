package tintinweb_subagents

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of test/workflow-runtime.test.ts (the host half of a run: the globals, caps, abort, control, schema, nested).

func rowsFor(seen [][]workflowEntry, index int) []*workflowAgentEntry {
	var out []*workflowAgentEntry
	for _, batch := range seen {
		for _, e := range batch {
			if a, ok := e.(*workflowAgentEntry); ok && a.Index == index {
				out = append(out, a)
			}
		}
	}
	return out
}

// progressRecorder collects the batches onProgress was called with.
type progressRecorder struct {
	mu   sync.Mutex
	seen [][]workflowEntry
}

func (p *progressRecorder) onProgress(es []workflowEntry) {
	p.mu.Lock()
	p.seen = append(p.seen, append([]workflowEntry(nil), es...))
	p.mu.Unlock()
}

func (p *progressRecorder) batches() [][]workflowEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]workflowEntry(nil), p.seen...)
}

func TestWorkflowRuntime(t *testing.T) {
	const f = "workflow-runtime"

	tw(t, f, "parses as JavaScript", func(t *testing.T) {
		// The original's worker source is a template literal in a .ts file, so tsc never sees the JavaScript inside it;
		// here the worker JavaScript and the prelude are compiled, which is the same bisect-saver.
		if _, err := compileWorkerSource(); err != nil {
			t.Fatalf("worker source does not parse: %v", err)
		}
	})

	// workflowConcurrency
	tw(t, f, "never returns zero on a small machine", func(t *testing.T) {
		eq(t, []int{workflowConcurrency(1), workflowConcurrency(2), workflowConcurrency(3)}, []int{1, 1, 1})
	})
	tw(t, f, "leaves two cores free and caps at 16", func(t *testing.T) {
		eq(t, []int{workflowConcurrency(8), workflowConcurrency(18), workflowConcurrency(64)}, []int{6, 16, 16})
	})

	// script globals
	tw(t, f, "runs a script, returns its value and reports meta", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, "const answer = await agent(\"hello\");\nreturn { answer };", func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "completed")
		eq(t, res.Value, map[string]any{"answer": "ok:hello"})
		eq(t, res.Meta.Name, "probe")
		eq(t, res.AgentCount, 1)
		calls := stub.Calls()
		eq(t, len(calls), 1)
		eq(t, calls[0].AgentType, "general-purpose")
		eq(t, calls[0].Label, "hello")
	})
	tw(t, f, "carries opts.effort to the spawn request", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, "await agent(\"deep\", { effort: \"xhigh\" });\nreturn null;", func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "completed")
		eq(t, stub.Calls()[0].Effort, "xhigh")
	})
	tw(t, f, "leaves effort unset when the script does not ask for one", func(t *testing.T) {
		stub := wfStubHost(nil)
		wfRun(t, "await agent(\"plain\");\nreturn null;", func(o *workflowRunOptions) { o.Host = stub })
		eq(t, stub.Calls()[0].Effort, "")
	})
	tw(t, f, "rejects an effort level pi does not have", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, "await agent(\"a\", { effort: \"ultra\" });\nreturn null;", func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "agent() opts.effort must be one of") {
			t.Fatal(res.Error)
		}
		eq(t, len(stub.Calls()), 0)
	})
	tw(t, f, "passes args through verbatim and exposes meta to the script", func(t *testing.T) {
		args := map[string]any{"files": []any{"a.ts", "b.ts"}, "depth": float64(2)}
		res := wfRun(t, "return { got: args, name: meta.name };", func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.Args, o.HasArgs = args, true })
		eq(t, res.Value, map[string]any{"got": args, "name": "probe"})
	})
	tw(t, f, "records phases, logs and console output", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{
			`phase("Scan");`, `log("scanned 41 files");`, `console.log("a", 1);`, `await agent("one");`, `phase("Verify");`,
			`await agent("two");`, `await agent("three", { phase: "Extra" });`, "return null;"}, "\n"),
			func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.Concurrency = 4 })
		eq(t, res.Status, "completed")
		var phases []workflowEntry
		var logs []string
		for _, e := range res.Progress {
			switch x := e.(type) {
			case *workflowPhaseEntry:
				phases = append(phases, x)
			case *workflowLogEntry:
				logs = append(logs, x.Message)
			}
		}
		eq(t, phases, []workflowEntry{newPhaseEntry(0, "Scan"), newPhaseEntry(1, "Verify"), newPhaseEntry(2, "Extra")})
		eq(t, logs, []string{"scanned 41 files", "a 1"})
		byIndex := map[int]*workflowAgentEntry{}
		for _, a := range wfAgentRows(res.Progress) {
			byIndex[a.Index] = a
		}
		eq(t, *byIndex[0].PhaseIndex, 0)
		eq(t, *byIndex[1].PhaseIndex, 1)
		// An explicit opts.phase files the agent elsewhere without moving the ambient phase for whatever comes next.
		eq(t, *byIndex[2].PhaseIndex, 2)
		eq(t, byIndex[2].PhaseTitle, "Extra")
	})
	tw(t, f, "reports line numbers that match the script the author wrote", func(t *testing.T) {
		// meta occupies line 1 and the leading newline is line 2, so the Error is constructed on line 3.
		res := wfRun(t, "\nconst here = new Error(\"x\").stack;\nreturn here;", func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		if s, _ := res.Value.(string); !strings.Contains(s, "workflow.js:3:") {
			t.Fatalf("stack %q", res.Value)
		}
	})
	tw(t, f, "maps a failed agent to null rather than throwing", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			if r.Prompt == "bad" {
				return workflowSpawnResult{Error: "child exploded"}
			}
			return workflowSpawnResult{OK: true, Text: "fine"}
		})
		res := wfRun(t, `return [await agent("bad"), await agent("good")];`, func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "completed")
		eq(t, res.Value, []any{nil, "fine"})
		var failed *workflowAgentEntry
		for _, a := range wfAgentRows(res.Progress) {
			if a.State == "error" {
				failed = a
				break
			}
		}
		eq(t, failed.Error, "child exploded")
	})

	// parallel
	tw(t, f, "is a barrier and folds a throwing thunk to null", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			if r.Prompt == "slow" {
				time.Sleep(60 * time.Millisecond)
			}
			return workflowSpawnResult{OK: true, Text: "ok:" + r.Prompt}
		})
		res := wfRun(t, strings.Join([]string{
			"const order = [];", "const values = await parallel([",
			`  async () => { const r = await agent("fast"); order.push("fast"); return r; },`,
			`  async () => { throw new Error("thunk exploded"); },`,
			`  async () => { const r = await agent("slow"); order.push("slow"); return r; },`,
			"]);", `order.push("after");`, "return { values, order };"}, "\n"),
			func(o *workflowRunOptions) { o.Host = stub; o.Concurrency = 4 })
		eq(t, res.Status, "completed")
		v := res.Value.(map[string]any)
		// The thrown thunk becomes null; its siblings are untouched.
		eq(t, v["values"], []any{"ok:fast", nil, "ok:slow"})
		// "after" last is the barrier: nothing past the await runs early.
		eq(t, v["order"], []any{"fast", "slow", "after"})
	})
	tw(t, f, "rejects more items than the cap allows", func(t *testing.T) {
		res := wfRun(t, "return await parallel(new Array(9).fill(async () => 1));", func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.ItemCap = 8 })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "9 items, over the limit of 8") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "defaults the item cap to 4096", func(t *testing.T) { eq(t, workflowItemCap, 4096) })

	// pipeline
	tw(t, f, "overlaps stages — item A reaches stage 2 before item B leaves stage 1", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			if r.Prompt == "s1:B" {
				time.Sleep(80 * time.Millisecond)
			}
			return workflowSpawnResult{OK: true, Text: "ok:" + r.Prompt}
		})
		res := wfRun(t, strings.Join([]string{
			"const order = [];", `const out = await pipeline(["A", "B"],`,
			`  async (value) => { await agent("s1:" + value); order.push("s1-out:" + value); return value; },`,
			`  async (value, item, index) => { order.push("s2-in:" + item + ":" + index); return value + "!"; },`,
			");", "return { order, out };"}, "\n"),
			func(o *workflowRunOptions) { o.Host = stub; o.Concurrency = 4 })
		eq(t, res.Status, "completed")
		v := res.Value.(map[string]any)
		eq(t, v["out"], []any{"A!", "B!"})
		eq(t, v["order"], []any{"s1-out:A", "s2-in:A:0", "s1-out:B", "s2-in:B:1"})
	})
	tw(t, f, "hands every stage (previous, original, index)", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{`return await pipeline(["x", "y"],`, "  async (value) => value.toUpperCase(),",
			`  async (value, item, index) => [value, item, index].join("/"),`, ");"}, "\n"), func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Value, []any{"X/x/0", "Y/y/1"})
	})
	tw(t, f, "drops a throwing item to null without touching its siblings", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{`return await pipeline(["keep", "drop"],`,
			`  async (value) => { if (value === "drop") throw new Error("stage failed"); return value; },`,
			`  async (value) => value + ":done",`, ");"}, "\n"), func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Value, []any{"keep:done", nil})
	})
	tw(t, f, "rejects more items than the cap allows", func(t *testing.T) {
		res := wfRun(t, "return await pipeline(new Array(5).fill(1), async v => v);", func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.ItemCap = 4 })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "over the limit of 4") {
			t.Fatal(res.Error)
		}
	})

	// semaphore
	peakHost := func(concurrentSleep time.Duration) (*wfStub, func() int) {
		var mu sync.Mutex
		active, peak := 0, 0
		return wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
				mu.Lock()
				active++
				peak = max(peak, active)
				mu.Unlock()
				time.Sleep(concurrentSleep)
				mu.Lock()
				active--
				mu.Unlock()
				return workflowSpawnResult{OK: true, Text: "done"}
			}), func() int {
				mu.Lock()
				defer mu.Unlock()
				return peak
			}
	}
	tw(t, f, "never exceeds the configured concurrency under a large fan-out", func(t *testing.T) {
		stub, peak := peakHost(5 * time.Millisecond)
		res := wfRun(t, `return (await parallel(new Array(24).fill(0).map((_, i) => () => agent("a" + i)))).length;`,
			func(o *workflowRunOptions) { o.Host = stub; o.Concurrency = 3 })
		eq(t, res.Status, "completed")
		eq(t, res.Value, float64(24))
		eq(t, peak(), 3)
	})
	tw(t, f, "holds the cap when fresh agents arrive while others are still queued", func(t *testing.T) {
		stub, peak := peakHost(5 * time.Millisecond)
		res := wfRun(t, strings.Join([]string{"return (await pipeline([0, 1, 2, 3, 4, 5],", `  async (item) => await agent("s1:" + item),`,
			`  async (previous, item) => await agent("s2:" + item),`, ")).length;"}, "\n"), func(o *workflowRunOptions) { o.Host = stub; o.Concurrency = 2 })
		eq(t, res.Status, "completed")
		eq(t, res.Value, float64(6))
		eq(t, peak(), 2)
	})
	tw(t, f, "queues agents it cannot start yet", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			time.Sleep(10 * time.Millisecond)
			return workflowSpawnResult{OK: true, Text: "done"}
		})
		res := wfRun(t, `return (await parallel(new Array(4).fill(0).map((_, i) => () => agent("a" + i)))).length;`,
			func(o *workflowRunOptions) { o.Host = stub; o.Concurrency = 1 })
		queuedOnly := 0
		for _, a := range wfAgentRows(res.Progress) {
			if a.QueuedAt != nil && a.StartedAt == nil {
				queuedOnly++
			}
		}
		if queuedOnly == 0 {
			t.Fatal("no row was ever only queued")
		}
	})

	// determinism prelude
	tw(t, f, "makes Date.now, new Date() and Math.random throw with guidance", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{"const messages = [];",
			"const capture = (fn) => { try { fn(); messages.push(null); } catch (error) { messages.push(error.message); } };",
			"capture(() => Date.now());", "capture(() => new Date());", "capture(() => Math.random());",
			"return { messages, stamped: new Date(0).getTime() };"}, "\n"), func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "completed")
		v := res.Value.(map[string]any)
		msgs := v["messages"].([]any)
		eq(t, len(msgs), 3)
		for _, m := range msgs {
			s, _ := m.(string)
			if !strings.Contains(s, "unavailable in workflow scripts (breaks resume)") || !strings.Contains(s, "Stamp results after the workflow returns, or pass timestamps via `args`.") {
				t.Fatal(m)
			}
		}
		for i, what := range []string{"Date.now()", "new Date()", "Math.random()"} {
			if !strings.Contains(msgs[i].(string), what) {
				t.Fatal(msgs[i])
			}
		}
		// An explicit timestamp still works — only the clock reads are blocked.
		eq(t, v["stamped"], float64(0))
	})
	tw(t, f, "blocks code generation from strings", func(t *testing.T) {
		res := wfRun(t, strings.Join([]string{"const seen = [];",
			`const capture = (fn) => { try { fn(); seen.push("no-throw"); } catch (error) { seen.push(error.name + ":" + (error instanceof EvalError)); } };`,
			`capture(() => eval("1"));`, `capture(() => Function("return 1"));`, "return seen;"}, "\n"), func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Value, []any{"EvalError:true", "EvalError:true"})
	})

	// the JSON boundary
	tw(t, f, "rejects ${name} on the way out", func(t *testing.T) {
		for _, c := range [][3]string{
			{"a cycle", "const a = {}; a.self = a; return a;", "a circular structure"},
			{"a BigInt", "return { n: 1n };", "a BigInt"},
			{"a function", "return () => 1;", "a function"},
			{"a Map", "return new Map();", "a non-plain object"},
			{"a sparse array", "const a = []; a[2] = 1; return a;", "a sparse array"},
			{"NaN", "return { n: 0 / 0 };", "a non-finite number"},
		} {
			t.Run("rejects "+c[0]+" on the way out", func(t *testing.T) {
				res := wfRun(t, c[1], func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
				eq(t, res.Status, "failed")
				if !strings.Contains(res.Error, c[2]) || !strings.Contains(res.Error, "across the workflow VM boundary") {
					t.Fatal(res.Error)
				}
			})
		}
	})
	tw(t, f, "rejects args that cannot cross, before the worker starts", func(t *testing.T) {
		err := wfRunErr("return 1;", func(o *workflowRunOptions) {
			o.Host = wfStubHost(nil)
			o.Args, o.HasArgs = map[string]any{"when": time.Unix(0, 0)}, true
		})
		if err == nil || !strings.Contains(err.Error(), "across the workflow VM boundary") {
			t.Fatalf("err %v", err)
		}
	})
	tw(t, f, "accepts plain JSON in both directions", func(t *testing.T) {
		if err := assertBoundarySafe(map[string]any{"a": []any{float64(1), "two", nil, map[string]any{"b": true}}}, "args"); err != nil {
			t.Fatal(err)
		}
		if err := assertBoundarySafe(nil, "args"); err != nil {
			t.Fatal(err)
		}
	})

	// caps and validation
	tw(t, f, "defaults the agent cap to 1000", func(t *testing.T) { eq(t, workflowAgentCap, 1000) })
	tw(t, f, "fails the run when the agent cap is hit, rather than returning null", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, `const seen = []; for (let i = 0; i < 4; i++) seen.push(await agent("a" + i)); return seen;`,
			func(o *workflowRunOptions) { o.Host = stub; o.AgentCap = 2 })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "cap of 2 agents") {
			t.Fatal(res.Error)
		}
		eq(t, len(stub.Calls()), 2)
	})
	tw(t, f, "does not let parallel swallow a cap breach into a null", func(t *testing.T) {
		res := wfRun(t, `return await parallel([() => agent("a"), () => agent("b"), () => agent("c")]);`,
			func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.AgentCap = 1; o.Concurrency = 1 })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "cap of 1 agents") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "rejects a script with control characters", func(t *testing.T) {
		_, err := runWorkflow(workflowRunOptions{Script: wfHead + "return \"a\u0007b\";", Host: wfStubHost(nil)})
		if err == nil || !strings.Contains(err.Error(), "control characters") {
			t.Fatalf("err %v", err)
		}
	})
	tw(t, f, "rejects a script over the size limit", func(t *testing.T) {
		_, err := runWorkflow(workflowRunOptions{Script: wfHead + "return \"" + strings.Repeat("x", 600_000) + "\";", Host: wfStubHost(nil)})
		if err == nil || !strings.Contains(err.Error(), "over the limit of 524288") {
			t.Fatalf("err %v", err)
		}
	})
	tw(t, f, "rejects a script with no meta block", func(t *testing.T) {
		_, err := runWorkflow(workflowRunOptions{Script: "return 1;", Host: wfStubHost(nil)})
		if err == nil || !strings.Contains(err.Error(), "must begin with") {
			t.Fatalf("err %v", err)
		}
	})
	tw(t, f, "reports a script that throws as a failed run", func(t *testing.T) {
		res := wfRun(t, `throw new Error("script blew up");`, func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "failed")
		eq(t, res.Error, "script blew up")
	})

	// abort
	tw(t, f, "terminates the run and aborts every in-flight child", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult { <-r.Ctx.Done(); return workflowSpawnResult{} })
		started := make(chan struct{})
		var once sync.Once
		go func() {
			<-started
			cancel()
		}()
		res := wfRun(t, "await agent(\"hangs forever\");\nreturn \"unreachable\";", func(o *workflowRunOptions) {
			o.Host, o.Ctx = stub, ctx
			o.OnProgress = func(es []workflowEntry) {
				for _, e := range es {
					if a, ok := e.(*workflowAgentEntry); ok && a.StartedAt != nil {
						once.Do(func() { close(started) })
					}
				}
			}
		})
		eq(t, res.Status, "killed")
		eq(t, res.Error, "Workflow aborted.")
		eq(t, stub.Aborted(), []string{"wf-agent-0"})
	})

	// effective configuration reported mid-run
	tw(t, f, "replaces the requested model with the effective one, before the agent settles", func(t *testing.T) {
		rec := &progressRecorder{}
		host := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			// Exactly where the real host fires it: the child's session now exists.
			r.OnResolved(workflowResolvedInfo{ModelName: "haiku 4.5", ModelID: "anthropic/claude-haiku-4-5"})
			return workflowSpawnResult{OK: true, Text: "done", OutputTokens: 1}
		})
		res := wfRun(t, `return await agent("go", { model: "haiku" });`, func(o *workflowRunOptions) { o.Host = host; o.OnProgress = rec.onProgress })
		eq(t, res.Status, "completed")
		rows := rowsFor(rec.batches(), 0)
		// The fuzzy request is what the row starts with...
		eq(t, rows[0].Model, "haiku")
		// ...and the resolved id is what it ends with, on a row still running.
		var corrected *workflowAgentEntry
		for _, r := range rows {
			if r.Model == "haiku 4.5" {
				corrected = r
				break
			}
		}
		if corrected == nil || corrected.State == "done" || corrected.ModelID != "anthropic/claude-haiku-4-5" {
			t.Fatalf("no corrected running row: %#v", corrected)
		}
		eq(t, rows[len(rows)-1].Model, "haiku 4.5")
	})
	tw(t, f, "fills in a row for an agent that named no model at all", func(t *testing.T) {
		rec := &progressRecorder{}
		host := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			r.OnResolved(workflowResolvedInfo{ModelName: "sonnet 4.6", Thinking: "high"})
			return workflowSpawnResult{OK: true, Text: "done", OutputTokens: 1}
		})
		wfRun(t, `return await agent("go");`, func(o *workflowRunOptions) { o.Host = host; o.OnProgress = rec.onProgress })
		rows := rowsFor(rec.batches(), 0)
		eq(t, rows[0].Model, "")
		eq(t, rows[len(rows)-1].Model, "sonnet 4.6")
		eq(t, rows[len(rows)-1].Thinking, "high")
	})
	tw(t, f, "carries the request forward when it was not honoured (#182)", func(t *testing.T) {
		rec := &progressRecorder{}
		host := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			r.OnResolved(workflowResolvedInfo{ModelName: "haiku 4.5", Thinking: "low", RequestedThinking: "max"})
			return workflowSpawnResult{OK: true, Text: "done", OutputTokens: 1}
		})
		wfRun(t, `return await agent("go", { effort: "max" });`, func(o *workflowRunOptions) { o.Host = host; o.OnProgress = rec.onProgress })
		rows := rowsFor(rec.batches(), 0)
		last := rows[len(rows)-1]
		eq(t, last.Thinking, "low")
		eq(t, last.RequestedThinking, "max")
	})
	tw(t, f, "keeps a resumed row saying what the row it continues said", func(t *testing.T) {
		rec := &progressRecorder{}
		host := &resumeStub{wfStub: wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			r.OnResolved(workflowResolvedInfo{ModelName: "haiku 4.5"})
			return workflowSpawnResult{OK: true, Text: "first", OutputTokens: 1}
		}), resume: func(id, prompt string, on func(workflowResolvedInfo)) workflowSpawnResult {
			on(workflowResolvedInfo{ModelName: "haiku 4.5"})
			return workflowSpawnResult{OK: true, Text: "second", OutputTokens: 1}
		}}
		res := wfRun(t, "await agent(\"go\", { label: \"impl\", model: \"haiku\" });\nreturn await agent(\"again\", { resume: \"impl\" });",
			func(o *workflowRunOptions) { o.Host = host; o.OnProgress = rec.onProgress })
		eq(t, res.Status, "completed")
		// Index 1 is the resumed call — the same child, so the same label.
		rows := rowsFor(rec.batches(), 1)
		eq(t, rows[len(rows)-1].Model, "haiku 4.5")
	})
	tw(t, f, "ignores a report that arrives after the agent settled", func(t *testing.T) {
		rec := &progressRecorder{}
		var late func()
		host := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			late = func() { r.OnResolved(workflowResolvedInfo{ModelName: "too late"}) }
			return workflowSpawnResult{OK: true, Text: "done", OutputTokens: 1}
		})
		res := wfRun(t, `return await agent("go");`, func(o *workflowRunOptions) { o.Host = host; o.OnProgress = rec.onProgress })
		late()
		eq(t, res.Status, "completed")
		rows := rowsFor(rec.batches(), 0)
		eq(t, rows[len(rows)-1].State, "done")
		for _, r := range rows {
			if r.Model == "too late" {
				t.Fatal("a late report reached the log")
			}
		}
	})
	tw(t, f, "leaves the row alone when the host reports nothing", func(t *testing.T) {
		rec := &progressRecorder{}
		wfRun(t, `return await agent("go", { model: "haiku" });`, func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.OnProgress = rec.onProgress })
		rows := rowsFor(rec.batches(), 0)
		eq(t, rows[len(rows)-1].Model, "haiku")
	})

	tw(t, f, "settles a script that never yields", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		var once sync.Once
		go func() {
			<-done
			cancel()
		}()
		// The agent call is the last thing the worker's event loop gets to do — the loop below never yields.
		res := wfRun(t, "await agent(\"start\");\nfor (;;) {}", func(o *workflowRunOptions) {
			o.Host, o.Ctx = wfStubHost(nil), ctx
			o.OnProgress = func(es []workflowEntry) {
				for _, e := range es {
					if a, ok := e.(*workflowAgentEntry); ok && a.State == "done" {
						once.Do(func() { close(done) })
					}
				}
			}
		})
		// Only an interrupt can stop a synchronous loop, which is why the script runs on its own goroutine.
		eq(t, res.Status, "killed")
		// And the goroutine really is gone, not merely detached: the original measures the CPU the process burns after
		// the abort (a worker still spinning on `for (;;)` would burn most of a 250ms window); a spinning realm is a
		// goroutine inside goja, so none may be left.
		time.Sleep(250 * time.Millisecond)
		buf := make([]byte, 1<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		if strings.Contains(stacks, "goja.(*vm).run") || strings.Contains(stacks, "workflowWorker).loop") {
			t.Fatalf("a worker is still running:\n%s", stacks)
		}
	})
	tw(t, f, "settles immediately when the signal is already aborted", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, `await agent("never");`, func(o *workflowRunOptions) { o.Host, o.Ctx = stub, wfCancelledCtx() })
		eq(t, res.Status, "killed")
		eq(t, len(stub.Calls()), 0)
	})

	// run control
	tw(t, f, "stops starting agents while paused, and starts them again on resume", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		type out struct{ res *workflowRunResult }
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "const a = await agent('one'); const b = await agent('two'); return [a, b].join('|');",
				func(o *workflowRunOptions) {
					o.Host = stub
					o.Concurrency = 4
					o.OnControl = func(c workflowControl) { control = c }
				})
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the first agent to start")
		control.Pause()
		eq(t, control.IsPaused(), true)
		stub.Finish("wf-agent-0", "first")
		// The second call is held at the gate — it must not reach the host.
		time.Sleep(60 * time.Millisecond)
		eq(t, stub.Started(), []string{"wf-agent-0"})
		control.Resume()
		wfUntil(t, func() bool { return len(stub.Started()) == 2 }, "the second agent to start")
		stub.Finish("wf-agent-1", "second")
		eq(t, (<-done).Value, "first|second")
	})
	tw(t, f, "skips a running agent, so its call returns null", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "return await agent('one');", func(o *workflowRunOptions) { o.Host = stub; o.OnControl = func(c workflowControl) { control = c } })
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the agent to start")
		eq(t, control.Skip(0), true)
		res := <-done
		eq(t, stub.Aborted(), []string{"wf-agent-0"})
		eq(t, res.Value, nil)
		last := wfLastRow(res.Progress)
		eq(t, [2]any{last.State, last.Skipped}, [2]any{"error", true})
	})
	tw(t, f, "skips an agent held at a pause without waiting for the resume", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "const a = await agent('one'); const b = await agent('two'); return JSON.stringify([a, b]);",
				func(o *workflowRunOptions) { o.Host = stub; o.OnControl = func(c workflowControl) { control = c } })
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the first agent to start")
		control.Pause()
		stub.Finish("wf-agent-0", "first")
		wfUntil(t, func() bool { return control.Skip(1) }, "the second call to reach the gate")
		res := <-done
		// Never handed to the host at all, and the script saw a null for it.
		eq(t, stub.Started(), []string{"wf-agent-0"})
		eq(t, res.Value, `["first",null]`)
	})
	tw(t, f, "retries a running agent into the same call", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "return await agent('one');", func(o *workflowRunOptions) { o.Host = stub; o.OnControl = func(c workflowControl) { control = c } })
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the first attempt to start")
		eq(t, control.Retry(0), true)
		// Same call, run again — the script is still awaiting it, which is the only reason a retry can deliver anything.
		wfUntil(t, func() bool { return len(stub.Started()) == 2 }, "the second attempt to start")
		stub.Finish("wf-agent-0", "second time lucky")
		res := <-done
		eq(t, res.Value, "second time lucky")
		last := wfLastRow(res.Progress)
		eq(t, [3]any{last.State, last.Attempt, last.LastAttemptReason}, [3]any{"done", 2, "user-retry"})
	})
	tw(t, f, "refuses to act on an agent that is not live", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "return await agent('one');", func(o *workflowRunOptions) { o.Host = stub; o.OnControl = func(c workflowControl) { control = c } })
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the agent to start")
		// Nothing at index 9, and a retry needs a child to stop.
		eq(t, control.Skip(9), false)
		eq(t, control.Retry(9), false)
		stub.Finish("wf-agent-0", "done")
		<-done
		// Settled: its value is already the script's, so there is nothing to redo.
		eq(t, control.Skip(0), false)
		eq(t, control.Retry(0), false)
	})
	tw(t, f, "does not leave an agent parked when a paused run is aborted", func(t *testing.T) {
		stub := newControllableHost()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "const a = await agent('one'); return await agent('two');",
				func(o *workflowRunOptions) {
					o.Host, o.Ctx = stub, ctx
					o.OnControl = func(c workflowControl) { control = c }
				})
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the first agent to start")
		control.Pause()
		stub.Finish("wf-agent-0", "first")
		time.Sleep(40 * time.Millisecond)
		cancel()
		// Resolves at all: a held agent must not outlive the run it belongs to.
		select {
		case res := <-done:
			eq(t, res.Status, "killed")
		case <-time.After(5 * time.Second):
			t.Fatal("the run never settled")
		}
	})

	// pause and the concurrency limit
	tw(t, f, "holds back an agent that was already queued behind the limit", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "const r = await parallel([() => agent('a'), () => agent('b')]); return JSON.stringify(r);",
				func(o *workflowRunOptions) {
					o.Host = stub
					o.Concurrency = 1
					o.OnControl = func(c workflowControl) { control = c }
				})
		}()
		// One permit, so 'a' runs and 'b' is queued behind it.
		wfUntil(t, func() bool { return len(stub.Started()) >= 1 }, "the first agent")
		eq(t, stub.Started(), []string{"wf-agent-0"})
		control.Pause()
		stub.Finish("wf-agent-0", "first")
		time.Sleep(80 * time.Millisecond)
		// The freed permit must not start 'b' — the run is paused.
		eq(t, stub.Started(), []string{"wf-agent-0"})
		control.Resume()
		wfUntil(t, func() bool { return len(stub.Started()) >= 2 }, "the second agent")
		stub.Finish("wf-agent-1", "second")
		eq(t, (<-done).Value, `["first","second"]`)
	})

	// Claude Code option compatibility
	tw(t, f, "names a misspelt option rather than ignoring it", func(t *testing.T) {
		res := wfRun(t, "return await agent('go', { agenttype: 'Explore' });", func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "agenttype") || !strings.Contains(res.Error, "agentType") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "accepts every option a Claude Code script actually uses", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, "return await agent('go', { label: 'L', phase: 'P', agentType: 'general-purpose', model: 'haiku', effort: 'high', isolation: 'worktree' });",
			func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "completed")
		c := stub.Calls()[0]
		eq(t, [3]string{c.Label, c.AgentType, c.Effort}, [3]string{"L", "general-purpose", "high"})
	})
	tw(t, f, "passes each pipeline stage (previous, item, index), as Claude Code does", func(t *testing.T) {
		res := wfRun(t, "return JSON.stringify(await pipeline(['a','b'], (p, item, i) => item + i, (p, item, i) => p + '/' + item + i));",
			func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Value, `["a0/a0","b1/b1"]`)
	})

	// the budget global
	tw(t, f, "reports no target, because pi has none to report", func(t *testing.T) {
		res := wfRun(t, "return JSON.stringify({ noTarget: budget.total === null, unbounded: budget.remaining() === Infinity, spent: budget.spent() });",
			func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "completed")
		eq(t, wfJSON(t, res.Value.(string)), map[string]any{"noTarget": true, "unbounded": true, "spent": float64(0)})
	})
	tw(t, f, "counts the output tokens of settled agents, and only those", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "x", Tokens: new(9000), OutputTokens: 300}
		})
		res := wfRun(t, "await agent('a'); await agent('b'); return budget.spent();", func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Value, float64(600))
	})
	tw(t, f, "runs Claude Code's loop-until-budget pattern verbatim", func(t *testing.T) {
		stub := wfStubHost(nil)
		res := wfRun(t, "const found = [];\nwhile (budget.total && budget.remaining() > 50_000) { found.push(await agent('find')); }\nreturn found.length;",
			func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "completed")
		eq(t, res.Value, float64(0))
		eq(t, len(stub.Calls()), 0)
	})
	tw(t, f, "runs Claude Code's static-scaling pattern verbatim", func(t *testing.T) {
		res := wfRun(t, "return budget.total ? Math.floor(budget.total / 100_000) : 5;", func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Value, float64(5))
	})

	// structured output
	findings := map[string]any{"type": "object", "properties": map[string]any{"findings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []any{"findings"}}
	schemaLiteral := `{"type":"object","properties":{"findings":{"type":"array","items":{"type":"string"}}},"required":["findings"]}`
	tw(t, f, "hands the script an object, not a string", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: `{"findings":["a","b"]}`}
		})
		res := wfRun(t, fmt.Sprintf("const r = await agent('go', { schema: %s });\n", schemaLiteral)+
			"return JSON.stringify([typeof r, r instanceof Object, r.findings instanceof Array, r.findings.length]);", func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Status, "completed")
		eq(t, wfJSON(t, res.Value.(string)), []any{"object", true, true, float64(2)})
	})
	tw(t, f, "passes the compiled schema to the host", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: `{"findings":[]}`}
		})
		wfRun(t, fmt.Sprintf("return await agent('go', { schema: %s });", schemaLiteral), func(o *workflowRunOptions) { o.Host = stub })
		c := stub.Calls()[0]
		if c.Schema == nil {
			t.Fatal("no schema")
		}
		eq(t, c.Schema.Schema, findings)
	})
	tw(t, f, "fails the call when the host returns prose instead", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "I found two things."}
		})
		res := wfRun(t, fmt.Sprintf("const r = await agent('go', { schema: %s });\nreturn r === null;", schemaLiteral), func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Value, true)
		last := wfLastRow(res.Progress)
		eq(t, last.State, "error")
		if !strings.Contains(last.Error, "not JSON") && !strings.Contains(last.Error, "did not match") {
			t.Fatal(last.Error)
		}
	})
	tw(t, f, "fails the call when the answer parses but does not match", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: `{"wrong":1}`}
		})
		res := wfRun(t, fmt.Sprintf("const r = await agent('go', { schema: %s });\nreturn r === null;", schemaLiteral), func(o *workflowRunOptions) { o.Host = stub })
		eq(t, res.Value, true)
		if !strings.Contains(wfLastRow(res.Progress).Error, "findings") {
			t.Fatal(wfLastRow(res.Progress).Error)
		}
	})
	tw(t, f, "rejects a schema that cannot be one, before spending a model call", func(t *testing.T) {
		for _, bad := range []string{"5", "'x'", "[]", "{ type: 'array' }"} {
			stub := wfStubHost(nil)
			res := wfRun(t, fmt.Sprintf("return await agent('go', { schema: %s });", bad), func(o *workflowRunOptions) { o.Host = stub })
			if res.Status != "failed" || !strings.Contains(res.Error, "schema") || len(stub.Calls()) != 0 {
				t.Fatalf("schema %s: %#v", bad, res)
			}
		}
	})
	tw(t, f, "refuses schema together with resume", func(t *testing.T) {
		res := wfRun(t, "await agent('first', { label: 'a' });\n"+fmt.Sprintf("return await agent('again', { resume: 'a', schema: %s });", schemaLiteral),
			func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "mutually exclusive") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "gives a schema call its own journal key", func(t *testing.T) {
		plain := &wfRecorder{}
		wfRun(t, "return await agent('go');", func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.Journal = plain.journal() })
		schemad := &wfRecorder{}
		wfRun(t, fmt.Sprintf("return await agent('go', { schema: %s });", schemaLiteral), func(o *workflowRunOptions) {
			o.Host = wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
				return workflowSpawnResult{OK: true, Text: `{"findings":[]}`}
			})
			o.Journal = schemad.journal()
		})
		// Same prompt, different contract — a resume must not replay one as the other.
		if schemad.entries[0].Key == plain.entries[0].Key {
			t.Fatal("same key")
		}
	})
	tw(t, f, "declines a replayed answer that no longer matches", func(t *testing.T) {
		stub := wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: `{"findings":["fresh"]}`}
		})
		stale := &wfRecorder{}
		wfRun(t, fmt.Sprintf("return await agent('go', { schema: %s });", schemaLiteral), func(o *workflowRunOptions) {
			o.Host = wfStubHost(func(r *workflowSpawnRequest) workflowSpawnResult {
				return workflowSpawnResult{OK: true, Text: `{"findings":["old"]}`}
			})
			o.Journal = stale.journal()
		})
		stale.entries[0].Text = wfStr("not json at all")
		res := wfRun(t, fmt.Sprintf("return (await agent('go', { schema: %s })).findings[0];", schemaLiteral),
			func(o *workflowRunOptions) {
				o.Host = stub
				o.Journal = &workflowJournalOptions{Entries: stale.entries}
			})
		eq(t, res.Value, "fresh")
		eq(t, res.ReplayedCount, 0)
		eq(t, len(stub.Calls()), 1)
	})

	// nested workflow()
	child := func(name, body string) string {
		return fmt.Sprintf("export const meta = { name: \"%s\", description: \"d\" };\n%s\n", name, body)
	}
	tw(t, f, "runs the child and returns its value", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "return 'from the child';")}, nil)
		res := wfRun(t, "return await workflow('audit');", func(o *workflowRunOptions) { o.Host = host })
		eq(t, res.Status, "completed")
		eq(t, res.Value, "from the child")
		eq(t, host.Asked(), []workflowRef{{Name: "audit"}})
	})
	tw(t, f, "passes args and exposes the child's own meta", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "return JSON.stringify([args.n, meta.name]);")}, nil)
		res := wfRun(t, "return await workflow('audit', { n: 7 });", func(o *workflowRunOptions) { o.Host = host })
		eq(t, wfJSON(t, res.Value.(string)), []any{float64(7), "audit"})
	})
	tw(t, f, "shares the parent's agent counter, so ids never collide", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "await agent('child-a'); return 1;")}, nil)
		res := wfRun(t, "await agent('parent'); await workflow('audit'); return 1;", func(o *workflowRunOptions) { o.Host = host })
		seen := map[string]bool{}
		var ids []string
		for _, a := range wfAgentRows(res.Progress) {
			if !seen[a.AgentID] {
				seen[a.AgentID] = true
				ids = append(ids, a.AgentID)
			}
		}
		eq(t, ids, []string{"wf-agent-0", "wf-agent-1"})
		// The run reports both as its own — there is only one run.
		eq(t, res.AgentCount, 2)
	})
	tw(t, f, "shares the parent's concurrency limit", func(t *testing.T) {
		stub, peak := peakHost(10 * time.Millisecond)
		host := newNestingHost(map[string]string{"audit": child("audit", "await agent('child'); return 1;")}, stub)
		wfRun(t, "await parallel([() => agent('a'), () => workflow('audit')]); return 1;", func(o *workflowRunOptions) { o.Host = host; o.Concurrency = 1 })
		eq(t, peak(), 1)
	})
	tw(t, f, "files the child's agents under their own phase group", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "await agent('scan'); return 1;")}, nil)
		res := wfRun(t, "phase('Review'); await agent('a'); await workflow('audit'); return 1;", func(o *workflowRunOptions) { o.Host = host })
		var titles []string
		for _, e := range res.Progress {
			if p, ok := e.(*workflowPhaseEntry); ok {
				titles = append(titles, p.Title)
			}
		}
		if !contains(strings.Join(titles, "|"), "Review") || !contains(strings.Join(titles, "|"), "▸ audit") {
			t.Fatal(titles)
		}
		groups := buildPhaseGroups(res.Progress, nil)
		eq(t, wfTitlesOf(groups), []string{"Review", "▸ audit"})
		eq(t, len(groups[1].Agents), 1)
	})
	tw(t, f, "does not let the child's phase leak back to the parent", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "phase('Scan'); await agent('c'); return 1;")}, nil)
		res := wfRun(t, "phase('Review'); await workflow('audit'); await agent('after'); return 1;", func(o *workflowRunOptions) { o.Host = host })
		for _, a := range wfAgentRows(res.Progress) {
			if a.Label == "after" {
				eq(t, a.PhaseTitle, "Review")
				return
			}
		}
		t.Fatal("no row for after")
	})
	tw(t, f, "keeps a child's phase from aliasing a parent phase of the same name", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "phase('Scan'); await agent('c'); return 1;")}, nil)
		res := wfRun(t, "phase('Scan'); await agent('p'); await workflow('audit'); return 1;", func(o *workflowRunOptions) { o.Host = host })
		var parent, nested *workflowAgentEntry
		for _, a := range wfAgentRows(res.Progress) {
			switch a.Label {
			case "p":
				parent = a
			case "c":
				nested = a
			}
		}
		if *parent.PhaseIndex == *nested.PhaseIndex {
			t.Fatal("aliased")
		}
		eq(t, nested.PhaseTitle, "▸ audit › Scan")
	})
	tw(t, f, "refuses to nest more than one level, by name", func(t *testing.T) {
		host := newNestingHost(map[string]string{"outer": child("outer", "return await workflow('inner');"), "inner": child("inner", "return 1;")}, nil)
		res := wfRun(t, "try { await workflow('outer'); return 'no throw'; } catch (error) { return error.message; }", func(o *workflowRunOptions) { o.Host = host })
		s := fmt.Sprint(res.Value)
		if !strings.Contains(s, "cannot be nested more than one level") || !strings.Contains(s, "outer") {
			t.Fatal(s)
		}
	})
	tw(t, f, "lets a script catch an unknown name", func(t *testing.T) {
		host := newNestingHost(map[string]string{}, nil)
		res := wfRun(t, "try { await workflow('nope'); return 'no throw'; } catch (error) { return error.message; }", func(o *workflowRunOptions) { o.Host = host })
		eq(t, res.Status, "completed")
		if !strings.Contains(fmt.Sprint(res.Value), "nope") {
			t.Fatal(res.Value)
		}
	})
	tw(t, f, "lets a script catch a child that is not a workflow", func(t *testing.T) {
		host := newNestingHost(map[string]string{"broken": "return 1;"}, nil)
		res := wfRun(t, "try { await workflow('broken'); return 'no throw'; } catch (error) { return error.message; }", func(o *workflowRunOptions) { o.Host = host })
		eq(t, res.Status, "completed")
		if !strings.Contains(strings.ToLower(fmt.Sprint(res.Value)), "meta") {
			t.Fatal(res.Value)
		}
	})
	tw(t, f, "is fatal on a host that cannot load workflows at all", func(t *testing.T) {
		res := wfRun(t, "return await workflow('audit');", func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "cannot run nested workflows") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "does not let parallel fold a fatal nested error into a null", func(t *testing.T) {
		res := wfRun(t, "const r = await parallel([() => workflow('audit')]);\nreturn JSON.stringify(r);", func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "cannot run nested workflows") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "caps how many nested runs one workflow may make", func(t *testing.T) {
		host := newNestingHost(map[string]string{"audit": child("audit", "return 1;")}, nil)
		res := wfRun(t, "for (let i = 0; i < 5; i++) await workflow('audit');\nreturn 'done';", func(o *workflowRunOptions) { o.Host = host; o.NestedCap = 2 })
		eq(t, res.Status, "failed")
		if !strings.Contains(res.Error, "cap of 2 nested") {
			t.Fatal(res.Error)
		}
	})
	tw(t, f, "journals the child's agents as the run's own", func(t *testing.T) {
		script := "await agent('parent'); await workflow('audit'); return 1;"
		library := map[string]string{"audit": child("audit", "await agent('child'); return 1;")}
		rec := &wfRecorder{}
		wfRun(t, script, func(o *workflowRunOptions) { o.Host = newNestingHost(library, nil); o.Journal = rec.journal() })
		eq(t, []int{rec.entries[0].Index, rec.entries[1].Index}, []int{0, 1})
		replayHost := newNestingHost(library, nil)
		replayed := wfRun(t, script, func(o *workflowRunOptions) {
			o.Host = replayHost
			o.Journal = &workflowJournalOptions{Entries: rec.entries}
		})
		eq(t, replayed.ReplayedCount, 2)
		eq(t, len(replayHost.stub.Calls()), 0)
	})
}

// resumeStub is a host that can also resume a child.
type resumeStub struct {
	*wfStub
	resume func(id, prompt string, on func(workflowResolvedInfo)) workflowSpawnResult
}

func (r *resumeStub) ResumeAgent(_ context.Context, id, prompt string, on func(workflowResolvedInfo)) (workflowSpawnResult, error) {
	return r.resume(id, prompt, on), nil
}

// nestingHost is a stub host that also serves saved workflows out of a map.
type nestingHost struct {
	stub    *wfStub
	library map[string]string
	mu      sync.Mutex
	asked   []workflowRef
}

func newNestingHost(library map[string]string, stub *wfStub) *nestingHost {
	if stub == nil {
		stub = wfStubHost(nil)
	}
	return &nestingHost{stub: stub, library: library}
}

func (n *nestingHost) SpawnAgent(r *workflowSpawnRequest) (workflowSpawnResult, error) {
	return n.stub.SpawnAgent(r)
}
func (n *nestingHost) AbortAgent(id string) { n.stub.AbortAgent(id) }
func (n *nestingHost) Asked() []workflowRef {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]workflowRef(nil), n.asked...)
}
func (n *nestingHost) LoadWorkflow(ref workflowRef) (workflowScriptSource, error) {
	n.mu.Lock()
	n.asked = append(n.asked, ref)
	n.mu.Unlock()
	if script, ok := n.library[ref.Name]; ok && ref.Name != "" {
		return workflowScriptSource{OK: true, Script: script}, nil
	}
	return workflowScriptSource{Message: fmt.Sprintf(`No saved workflow named "%s".`, ref.Name)}, nil
}

// controllableHost's children hang until the test lets them go, or are aborted: what the real host does, a stopped
// child comes back as a skipped result rather than a failure.
type controllableHost struct {
	mu      sync.Mutex
	started []string
	aborted []string
	release map[string]chan workflowSpawnResult
}

func newControllableHost() *controllableHost {
	return &controllableHost{release: map[string]chan workflowSpawnResult{}}
}

func (c *controllableHost) SpawnAgent(r *workflowSpawnRequest) (workflowSpawnResult, error) {
	ch := make(chan workflowSpawnResult, 4)
	c.mu.Lock()
	c.started = append(c.started, r.AgentID)
	c.release[r.AgentID] = ch
	c.mu.Unlock()
	select {
	case res := <-ch:
		return res, nil
	case <-r.Ctx.Done():
		return workflowSpawnResult{}, nil
	}
}

func (c *controllableHost) AbortAgent(id string) {
	c.mu.Lock()
	c.aborted = append(c.aborted, id)
	ch := c.release[id]
	c.mu.Unlock()
	if ch != nil {
		ch <- workflowSpawnResult{Skipped: true, Error: "Stopped."}
	}
}

func (c *controllableHost) Started() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.started...)
}

func (c *controllableHost) Aborted() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.aborted...)
}

func (c *controllableHost) Finish(id, text string) {
	c.mu.Lock()
	ch := c.release[id]
	c.mu.Unlock()
	if ch != nil {
		ch <- workflowSpawnResult{OK: true, Text: text}
	}
}
