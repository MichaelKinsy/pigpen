package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScriptedWorkflowTwinsSteerAndObserve(t *testing.T) {
	tw(t, scriptedFile, "validates runs.steer input before calling the host", func(t *testing.T) {
		for _, script := range []string{
			`return runs.steer("bad key", "guide");`,
			`return runs.steer("writer", " ");`,
			`return runs.steer("writer", "guide", { mode: "later" });`,
			`return runs.steer("writer", "guide", { index: -1 });`,
			`return runs.steer("writer", "guide", { ackTimeoutMs: 0 });`,
			`return runs.steer("writer", "guide", { runId: "raw-id" });`,
		} {
			var steerCalls atomic.Int32
			o := wfOptions(script)
			o.Steer = func(ctx context.Context, key, message string, opts WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
				steerCalls.Add(1)
				return WorkflowResolved(WorkflowSteerResult{Key: key, State: "delivered"})
			}
			matchErr(t, wfFail(t, o), `runs\.steer`)
			if steerCalls.Load() != 0 {
				t.Fatalf("%s: steer was called", script)
			}
		}
	})

	tw(t, scriptedFile, "steers a still-running sibling after Promise.race and awaits both children", func(t *testing.T) {
		slow := NewWorkflowFuture[*jsObject]()
		o := wfOptions(`
				const fast = runs.run("fast", { agent: "worker", task: "fast" });
				const slow = runs.run("slow", { agent: "worker", task: "slow" });
				const first = await Promise.race([fast, slow]);
				const receipt = await runs.steer("slow", "Focus on tests.", { mode: "auto", index: 0, ackTimeoutMs: 100 });
				const children = await Promise.all([fast, slow]);
				return { first: first.key, receipt, children: children.map((child) => child.key) };
			`)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			if key == "fast" {
				return WorkflowResolved(okChild(key, "fast"))
			}
			return slow
		}
		o.Steer = func(ctx context.Context, key, message string, opts WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
			if key != "slow" || message != "Focus on tests." || opts.Mode != "auto" || opts.Index == nil || *opts.Index != 0 || opts.AckTimeoutMs == nil || *opts.AckTimeoutMs != 100 {
				t.Errorf("steer(%q, %q, %+v)", key, message, opts)
			}
			slow.Resolve(okChild(key, "slow"))
			return WorkflowResolved(WorkflowSteerResult{Key: key, State: "delivered", RequestID: "request-1", DeliveryStatus: "delivered", Targets: []WorkflowSteerTarget{{Index: 0, State: "delivered"}}})
		}
		res := wfRun(t, o)
		jsonEq(t, res.Value, `{"first":"fast","receipt":{"key":"slow","state":"delivered","requestId":"request-1","deliveryStatus":"delivered","targets":[{"index":0,"state":"delivered"}]},"children":["fast","slow"]}`)
		var states []string
		for _, e := range res.Trace {
			if e.Operation == "steer" {
				states = append(states, e.State)
			}
		}
		if fmt.Sprint(states) != "[started delivered]" {
			t.Fatalf("steer trace = %v", states)
		}
	})

	tw(t, scriptedFile, "uses Promise.race to roll through child completions and steer the remaining work", func(t *testing.T) {
		beta, gamma := NewWorkflowFuture[*jsObject](), NewWorkflowFuture[*jsObject]()
		o := wfOptions(`
				let pending = [
					{ key: "alpha", promise: runs.run("alpha", { agent: "worker", task: "alpha" }).then((result) => ({ key: "alpha", result })) },
					{ key: "beta", promise: runs.run("beta", { agent: "worker", task: "beta" }).then((result) => ({ key: "beta", result })) },
					{ key: "gamma", promise: runs.run("gamma", { agent: "worker", task: "gamma" }).then((result) => ({ key: "gamma", result })) },
				];
				const first = await Promise.race(pending.map((child) => child.promise));
				pending = pending.filter((child) => child.key !== first.key);
				const target = pending.find((child) => child.key === "gamma") ?? pending[0];
				const receipt = await runs.steer(target.key, "Challenge the first result: " + first.result.output, { mode: "auto", ackTimeoutMs: 100 });
				const second = await Promise.race(pending.map((child) => child.promise));
				pending = pending.filter((child) => child.key !== second.key);
				const rest = await Promise.all(pending.map((child) => child.promise));
				return { first: first.key, second: second.key, rest: rest.map((child) => child.key), receipt };
			`)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			switch key {
			case "alpha":
				return WorkflowResolved(okChild(key, "alpha done"))
			case "beta":
				return beta
			}
			return gamma
		}
		o.Steer = func(ctx context.Context, key, message string, opts WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
			if key != "gamma" || message != "Challenge the first result: alpha done" || opts.Mode != "auto" || opts.Index != nil || opts.AckTimeoutMs == nil || *opts.AckTimeoutMs != 100 {
				t.Errorf("steer(%q, %q, %+v)", key, message, opts)
			}
			gamma.Resolve(okChild(key, "gamma done"))
			time.AfterFunc(5*time.Millisecond, func() { beta.Resolve(okChild("beta", "beta done")) })
			return WorkflowResolved(WorkflowSteerResult{Key: key, State: "delivered", RequestID: "request-rolling", DeliveryStatus: "delivered", Targets: []WorkflowSteerTarget{{Index: 0, State: "delivered"}}})
		}
		res := wfRun(t, o)
		jsonEq(t, res.Value, `{"first":"alpha","second":"gamma","rest":["beta"],"receipt":{"key":"gamma","state":"delivered","requestId":"request-rolling","deliveryStatus":"delivered","targets":[{"index":0,"state":"delivered"}]}}`)
		if fmt.Sprint(childKeys(res.Children)) != "[alpha beta gamma]" {
			t.Fatalf("children = %v", childKeys(res.Children))
		}
		var completed, steers []string
		for _, e := range res.Trace {
			if e.Operation == "run" && e.State == "completed" {
				completed = append(completed, e.Key)
			}
			if e.Operation == "steer" {
				steers = append(steers, e.Key+":"+e.State)
			}
		}
		if fmt.Sprint(completed) != "[alpha gamma beta]" || fmt.Sprint(steers) != "[gamma:started gamma:delivered]" {
			t.Fatalf("completed %v steers %v", completed, steers)
		}
	})

	tw(t, scriptedFile, "waits for and rejects an unawaited runs.steer side effect", func(t *testing.T) {
		var steerSettled atomic.Bool
		o := wfOptions(`await runs.run("writer", { agent: "worker", task: "work" }); runs.steer("writer", "Checkpoint."); return "done";`)
		o.Steer = func(ctx context.Context, key, message string, opts WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
			return WorkflowAsync(func() (WorkflowSteerResult, error) {
				time.Sleep(10 * time.Millisecond)
				steerSettled.Store(true)
				return WorkflowSteerResult{Key: key, State: "queued"}, nil
			})
		}
		we := wfFail(t, o)
		queued := false
		for _, e := range we.Partial.Trace {
			queued = queued || (e.Operation == "steer" && e.State == "queued")
		}
		if !strings.Contains(we.Message, "unawaited runs.steer call(s): 'writer'") || !queued || !steerSettled.Load() {
			t.Fatalf("message %q queued=%v settled=%v", we.Message, queued, steerSettled.Load())
		}
	})

	tw(t, scriptedFile, "rejects an unawaited runs.steer host-invariant failure", func(t *testing.T) {
		o := wfOptions(`runs.steer("missing", "Checkpoint."); return "done";`)
		o.Steer = func(ctx context.Context, key, message string, opts WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
			return WorkflowResolved(WorkflowSteerResult{Key: key, State: "delivered"})
		}
		we := wfFail(t, o)
		failed := false
		for _, e := range we.Partial.Trace {
			failed = failed || (e.Operation == "steer" && e.State == "failed")
		}
		if !(strings.Contains(we.Message, "unawaited runs.steer call(s): 'missing'") || strings.Contains(we.Message, "runs.steer('missing') requires a prior runs.run/runs.all launch with that key")) || !failed {
			t.Fatalf("message %q failed=%v", we.Message, failed)
		}
	})

	tw(t, scriptedFile, "rejects and aborts an unawaited child launch when the script completes", func(t *testing.T) {
		var childAborted atomic.Bool
		o := wfOptions(`runs.run("bg", { agent: "worker", task: "fire and forget" }); return "done";`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowAsync(func() (*jsObject, error) {
				<-ctx.Done()
				childAborted.Store(true)
				return nil, context.Cause(ctx)
			})
		}
		we := wfFail(t, o)
		if !strings.Contains(we.Message, "unawaited runs.run launch(es): 'bg'") || !strings.Contains(we.Message, "await runs.all([{key, agent, task}, ...])") {
			t.Fatalf("message = %q", we.Message)
		}
		// the child's signal is cancelled as the run finishes; give its goroutine a moment to observe it
		for i := 0; i < 100 && !childAborted.Load(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if !childAborted.Load() {
			t.Fatalf("the child was not aborted")
		}
	})

	tw(t, scriptedFile, "rejects an unawaited child launch that settles before the script completes", func(t *testing.T) {
		o := wfOptions(`runs.run("fast", { agent: "worker", task: "quick" }); await runs.status("probe"); return "done";`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "fast output"))
		}
		o.Status = func(ctx context.Context, key string) *WorkflowFuture[*jsObject] {
			return workflowAfterMicrotask(okChild(key, "ok"))
		}
		we := wfFail(t, o)
		found := false
		for _, c := range we.Partial.Children {
			found = found || objStr(c, "key") == "fast"
		}
		if !strings.Contains(we.Message, "unawaited runs.run launch(es): 'fast'") || !found {
			t.Fatalf("message %q children %v", we.Message, childKeys(we.Partial.Children))
		}
	})

	tw(t, scriptedFile, "rejects an unawaited runs.all launch group", func(t *testing.T) {
		o := wfOptions(`runs.all([{ key: "a", agent: "worker", task: "one" }]); return "done";`)
		o.TimeoutMs = new(2000.0)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): 'a'") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects an unawaited ${kind} permissive runs.all aggregate", func(t *testing.T) {
		for _, c := range []struct{ kind, items, keys string }{
			{"direct", `p`, "'a'"},
			{"wrapped", `Promise.resolve(p)`, "'a'"},
			{"mixed", `p, { key: "b", agent: "worker", task: "two" }`, "'a', 'b'"},
		} {
			t.Run(c.kind, func(t *testing.T) {
				o := wfOptions(fmt.Sprintf(`const p = runs.run("a", { agent: "worker", task: "one" }); runs.all([%s]); await runs.status("a"); return "done";`, c.items))
				o.Status = func(ctx context.Context, key string) *WorkflowFuture[*jsObject] {
					return WorkflowAfter(40*time.Millisecond, okChild(key, "ok"))
				}
				if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): "+c.keys) {
					t.Fatalf("message = %q", we.Message)
				}
			})
		}
	})

	tw(t, scriptedFile, "can catch permissive runs.all validation and the original launch rejection", func(t *testing.T) {
		o := wfOptions(`
				const p = runs.run("bad", { agent: "worker", task: "fail" });
				const errors = [];
				try { runs.all([p, { key: "unused", agent: "worker", task: "unused" }, null]); } catch (e) { errors.push(e.message); }
				try { await p; } catch (e) { errors.push(e.message); }
				await runs.status("bad");
				return errors;
			`)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(failedChild(key, "child failed"))
		}
		o.Status = func(ctx context.Context, key string) *WorkflowFuture[*jsObject] {
			return WorkflowAfter(40*time.Millisecond, okChild(key, "ok"))
		}
		res := wfRun(t, o)
		jsonEq(t, res.Value, `["runs.all item 2 must be an object or a runs.run(...) launch promise.","Run 'bad' failed: child failed"]`)
		if fmt.Sprint(childKeys(res.Children)) != "[bad]" {
			t.Fatalf("children = %v", childKeys(res.Children))
		}
	})

	tw(t, scriptedFile, "rejects an unawaited runs.lanes launch", func(t *testing.T) {
		o := wfOptions(`runs.lanes([{ key: "lane", stages: [{ key: "writer", agent: "worker", task: "write" }] }]); return "done";`)
		o.TimeoutMs = new(2000.0)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): 'lane.writer'") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects an unawaited native Promise.all over runs.run", func(t *testing.T) {
		o := wfOptions(`Promise.all([runs.run("native", { agent: "worker", task: "one" })]); return "done";`)
		o.TimeoutMs = new(2000.0)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): 'native'") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects an unawaited new Promise wrapper over runs.run", func(t *testing.T) {
		o := wfOptions(`new Promise((resolve) => resolve(runs.run("wrapped", { agent: "worker", task: "fire" }))); return "done";`)
		o.TimeoutMs = new(2000.0)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): 'wrapped'") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects nested async helper syntax for portable workflow parity", func(t *testing.T) {
		for _, script := range []string{
			`async function helper() { return runs.run("async-function", { agent: "worker", task: "run" }); } const child = await helper(); return child.output;`,
			`const helper = async () => runs.run("async-arrow", { agent: "worker", task: "run" }); const child = await helper(); return child.output;`,
			`const helpers = { async scan() { return runs.run("async-method", { agent: "worker", task: "run" }); } }; return helpers.scan();`,
			`const helpers = { async ["scan"]() { return runs.run("async-computed-method", { agent: "worker", task: "run" }); } }; helpers.scan(); return "done";`,
		} {
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			we := wfFail(t, o)
			if !strings.Contains(we.Message, "does not support nested async functions") || len(we.Partial.Children) != 0 {
				t.Fatalf("%s: %q", script, we.Message)
			}
		}
	})

	tw(t, scriptedFile, "rejects nested async helpers before launching children", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`async function patchLane(key) { const writer = await runs.run(key, { agent: "worker", task: "write" }); return runs.run(key + "-review", { agent: "reviewer", task: writer.output }); } await Promise.all([patchLane("lane")]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		we := wfFail(t, o)
		for _, want := range []string{"does not support nested async functions", "validation failed before child launch; no children launched", "Parallel plus sequential rewrite", "const [aResult] = await Promise.all([a])"} {
			if !strings.Contains(we.Message, want) {
				t.Errorf("message lacks %q: %s", want, we.Message)
			}
		}
		if len(we.Partial.Children) != 0 || len(log.keys) != 0 {
			t.Fatalf("children %v launches %v", we.Partial.Children, log.keys)
		}
	})

	tw(t, scriptedFile, "ignores async-looking text in regex literals", func(t *testing.T) {
		o := wfOptions(strings.Join([]string{
			`const pattern = /async function helper/;`,
			`if (true) /async function helper/.test("async function helper");`,
			`if (true) /* comment */ /async function helper/.test("async function helper");`,
			`const inResult = "async function helper" in /async function helper/;`,
			`const inBlockComment = "async function helper" in /* comment */ /async function helper/;`,
			`const inLineComment = "async function helper" in // comment`,
			`/async function helper/;`,
			`let instanceResult = false;`,
			`try { instanceResult = {} instanceof /* comment */ /async function helper/; } catch {}`,
			`function helper() { return runs.run("regex-text", { agent: "worker", task: "run" }); }`,
			`const child = await helper();`,
			`return pattern.test("async function helper") && !inResult && !inBlockComment && !inLineComment && !instanceResult ? child.output : "missing";`,
		}, "\n"))
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "regex output"))
		}
		if v := wfRun(t, o).Value; v != "regex output" {
			t.Fatalf("value = %v", v)
		}
	})

	tw(t, scriptedFile, "rejects nested async helper syntax inside template expressions", func(t *testing.T) {
		o := wfOptions("const value = `${(async () => runs.run(\"template-async\", { agent: \"worker\", task: \"run\" }))()}`; return value;")
		o.TimeoutMs = new(2000.0)
		we := wfFail(t, o)
		if !strings.Contains(we.Message, "does not support nested async functions") || len(we.Partial.Children) != 0 {
			t.Fatalf("message = %q", we.Message)
		}
	})

	accepted := func(script, keyOutput string) func(t *testing.T) {
		return func(t *testing.T) {
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
				return WorkflowResolved(okChild(key, keyOutput))
			}
			if v := wfRun(t, o).Value; v != keyOutput {
				t.Fatalf("value = %v, want %s", v, keyOutput)
			}
		}
	}
	tw(t, scriptedFile, "accepts portable plain helper wrappers over runs.run", accepted(`function helper() { return runs.run("plain-helper", { agent: "worker", task: "run" }); } const child = await helper(); return child.output;`, "helper output"))
	tw(t, scriptedFile, "accepts Promise.all over a portable plain helper wrapper", accepted(`function helper() { return runs.run("plain-helper-all", { agent: "worker", task: "run" }); } const children = await Promise.all([helper()]); return children[0].output;`, "helper all output"))

	tw(t, scriptedFile, "accepts awaited Promise.all over then chains that return child launches", func(t *testing.T) {
		var mu sync.Mutex
		o := wfOptions(`
				function patchLane(key) {
					return runs.run(key + "-writer", { agent: "worker", task: "write " + key })
						.then((writer) => runs.run(key + "-review", { agent: "reviewer", task: "review " + writer.output }));
				}
				const reviews = await Promise.all([patchLane("alpha"), patchLane("beta")]);
				return reviews.map((review) => review.key);
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			mu.Lock()
			defer mu.Unlock()
			if strings.HasSuffix(key, "-review") && !strings.Contains(objStr(params, "task"), "writer output") {
				t.Errorf("task = %q", objStr(params, "task"))
			}
			return WorkflowResolved(okChild(key, key+" writer output"))
		}
		res := wfRun(t, o)
		jsonEq(t, res.Value, `["alpha-review","beta-review"]`)
		if fmt.Sprint(childKeys(res.Children)) != "[alpha-writer beta-writer alpha-review beta-review]" {
			t.Fatalf("children = %v", childKeys(res.Children))
		}
	})

	tw(t, scriptedFile, "accepts awaited and returned handlers on portable plain helper wrappers", func(t *testing.T) {
		for _, mode := range []string{"await", "return"} {
			for _, h := range []struct{ name, chain string }{{"then", "then((value) => value)"}, {"catch", "catch(() => ({ output: 'fallback' }))"}, {"finally", "finally(() => {})"}} {
				key := mode + "-" + h.name
				expression := "helper()." + h.chain
				body := "return " + expression + ";"
				if mode == "await" {
					body = "const child = await " + expression + "; return child.output;"
				}
				o := wfOptions(fmt.Sprintf(`function helper() { return runs.run("%s", { agent: "worker", task: "run" }); } %s`, key, body))
				o.TimeoutMs = new(2000.0)
				o.Launch = func(ctx context.Context, k string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
					return WorkflowResolved(okChild(k, "helper chain output"))
				}
				v := wfRun(t, o).Value
				if mode == "return" {
					v = v.(*jsObject).vals["output"]
				}
				if v != "helper chain output" {
					t.Fatalf("%s: value = %v", key, v)
				}
			}
		}
	})

	tw(t, scriptedFile, "accepts awaited and returned nested plain helper wrappers", func(t *testing.T) {
		for _, mode := range []string{"await", "return"} {
			key := "nested-" + mode
			body := "return outer();"
			if mode == "await" {
				body = "const child = await outer(); return child.output;"
			}
			o := wfOptions(fmt.Sprintf(`function inner() { return runs.run("%s", { agent: "worker", task: "run" }); } function outer() { return inner(); } %s`, key, body))
			o.TimeoutMs = new(2000.0)
			o.Launch = func(ctx context.Context, k string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
				return WorkflowResolved(okChild(k, "nested output"))
			}
			v := wfRun(t, o).Value
			if mode == "return" {
				v = v.(*jsObject).vals["output"]
			}
			if v != "nested output" {
				t.Fatalf("%s: value = %v", mode, v)
			}
		}
	})

	tw(t, scriptedFile, "rejects a launch passed to an ignored repeated Promise resolution", func(t *testing.T) {
		o := wfOptions(`
				await new Promise((resolve) => {
					resolve("done");
					Promise.resolve().then(() => resolve(runs.run("ignored", { agent: "worker", task: "fire" })));
				});
				return "done";
			`)
		o.TimeoutMs = new(2000.0)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): 'ignored'") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects fire-and-forget callbacks on a runs.run promise", func(t *testing.T) {
		o := wfOptions(`runs.run("bg", { agent: "worker", task: "fire" }).then(() => {}); return "done";`)
		o.TimeoutMs = new(2000.0)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.run launch(es): 'bg'") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects detached promise handlers after an await", func(t *testing.T) {
		for _, h := range []struct{ key, chain string }{{"bg-then", "then(() => {})"}, {"bg-catch", "catch(() => {})"}, {"bg-finally", "finally(() => {})"}} {
			o := wfOptions(fmt.Sprintf(`await Promise.resolve(); runs.run("%s", { agent: "worker", task: "fire" }).%s; return "done";`, h.key, h.chain))
			o.TimeoutMs = new(2000.0)
			if we := wfFail(t, o); !strings.Contains(we.Message, fmt.Sprintf("unawaited runs.run launch(es): '%s'", h.key)) {
				t.Fatalf("%s: %q", h.key, we.Message)
			}
		}
	})

	tw(t, scriptedFile, "rejects reading output from an unawaited runs.run promise", func(t *testing.T) {
		o := wfOptions(`return runs.run("x", { agent: "worker", task: "run" }).output;`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return neverUntilAbort(ctx)
		}
		we := wfFail(t, o)
		if !strings.Contains(we.Message, "unawaited runs.run launch(es): 'x'") || !strings.Contains(we.Message, "do not read .output from unawaited launches") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "names every outstanding workflow launch", func(t *testing.T) {
		o := wfOptions(`runs.run("first", { agent: "worker", task: "one" }); runs.run("second", { agent: "worker", task: "two" }); return null;`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return neverUntilAbort(ctx)
		}
		we := wfFail(t, o)
		if !strings.Contains(we.Message, "'first', 'second'") || len(we.Partial.Children) != 0 {
			t.Fatalf("message %q children %v", we.Message, we.Partial.Children)
		}
	})

	tw(t, scriptedFile, "accepts a directly returned runs.run promise", func(t *testing.T) {
		o := wfOptions(`return runs.run("direct", { agent: "worker", task: "run" });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "direct output"))
		}
		if v := wfRun(t, o).Value.(*jsObject).vals["output"]; v != "direct output" {
			t.Fatalf("output = %v", v)
		}
	})

	tw(t, scriptedFile, "accepts a docs-style awaited runs.run launch", accepted(`const child = await runs.run("awaited", { agent: "worker", task: "run" }); return child.output;`, "awaited output"))
	tw(t, scriptedFile, "accepts a docs-style awaited runs.all launch group", accepted(`const children = await runs.all([{ key: "one", agent: "worker", task: "run" }]); return children[0].output;`, "group output"))

	tw(t, scriptedFile, "accepts sequential awaited launches that use the first output", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const first = await runs.run("first", { agent: "worker", task: "plan" });
				const second = await runs.run("second", { agent: "worker", task: first.output });
				return second.output;
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			out := "second output"
			if key == "first" {
				out = "first output"
			}
			return WorkflowResolved(okChild(key, out))
		})
		res := wfRun(t, o)
		if fmt.Sprint([]any{log.params[0].vals["task"], log.params[1].vals["task"]}) != "[plan first output]" || res.Value != "second output" {
			t.Fatalf("tasks %v value %v", log.params, res.Value)
		}
	})

	tw(t, scriptedFile, "accepts an awaited native Promise combinator over launches", accepted(`const children = await Promise.all([runs.run("native", { agent: "worker", task: "run" })]); return children[0].output;`, "native output"))
	tw(t, scriptedFile, "accepts Promise.resolve over a pending helper wrapper", accepted(`
				const helper = new Promise((resolve) => Promise.resolve().then(() =>
					resolve(runs.run("resolve-helper", { agent: "worker", task: "run" }))
				));
				const child = await Promise.resolve(helper);
				return child.output;
			`, "resolved helper output"))
	tw(t, scriptedFile, "accepts Promise.all over a pending helper wrapper", accepted(`
				const helper = new Promise((resolve) => Promise.resolve().then(() =>
					resolve(runs.run("combo-later", { agent: "worker", task: "run" }))
				));
				const children = await Promise.all([helper]);
				return children[0].output;
			`, "pending helper output"))
	tw(t, scriptedFile, "accepts an awaited then chain over a pending helper wrapper", accepted(`
				const helper = new Promise((resolve) => Promise.resolve().then(() =>
					resolve(runs.run("chain-later", { agent: "worker", task: "run" }))
				));
				const child = await helper.then((value) => value);
				return child.output;
			`, "chain output"))
	tw(t, scriptedFile, "accepts an awaited new Promise wrapper over runs.run", accepted(`const child = await new Promise((resolve) => resolve(runs.run("wrapped", { agent: "worker", task: "run" }))); return child.output;`, "wrapped output"))

	_ = errors.New
	_ = os.Getenv
	_ = filepath.Join
}

// workflowAfterMicrotask is a future that settles after a microtask, as an async callback that awaits Promise.resolve() does.
func workflowAfterMicrotask(c *jsObject) *WorkflowFuture[*jsObject] {
	f := NewWorkflowFuture[*jsObject]()
	go func() {
		time.Sleep(time.Millisecond)
		f.Resolve(c)
	}()
	return f
}
