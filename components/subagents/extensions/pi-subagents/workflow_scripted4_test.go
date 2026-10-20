package pi_subagents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestScriptedWorkflowTwinsParams(t *testing.T) {
	tw(t, scriptedFile, "validates every runs.all item before launching children", func(t *testing.T) {
		for _, script := range []string{
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, null]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "bad key", agent: "worker", task: "run" }]);`,
			`return await runs.all([{ key: "same", agent: "worker", task: "one" }, { key: "same", agent: "worker", task: "two" }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "nested", workflowScript: "return null" }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "legacy", agent: "worker", task: "run", parallel: [{ task: "nested" }] }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "undefined-action", agent: "worker", task: "run", action: undefined }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "uncloneable", agent: "worker", task: () => "run" }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "bad-binding", agent: "worker", task: "run", extensionBindings: { invalid: true } }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "bad-capacity", agent: "worker", task: "run", globalConcurrencyLimit: 2 }]);`,
			`return await runs.all([{ key: "valid", agent: "worker", task: "run" }, { key: "bad-budget", agent: "worker", task: "run", maxSubagentSpawnsPerRun: 2 }]);`,
			`const items = []; items[1] = { key: "valid", agent: "worker", task: "run" }; return await runs.all(items);`,
		} {
			var log launchLog
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			o.Launch = log.fn(okAnswer)
			we := wfFail(t, o)
			matchErr(t, we, `runs\.all|Duplicate workflow key`)
			if len(log.keys) != 0 {
				t.Fatalf("%s: launched %v", script, log.keys)
			}
		}
	})

	tw(t, scriptedFile, "accepts runs.run(...) launch promises passed via runs.all(items.map(...)) and warns once about lost batch semantics", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const items = ["alpha", "beta"];
				const results = await runs.all(items.map((name) => runs.run(name, { agent: "worker", task: "work-" + name })));
				return results.map((entry) => ({ key: entry.key, ok: entry.ok, output: entry.output }));
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "output", "done-"+key, "artifactPaths", []any{}, "results", []any{}))
		})
		res := wfRun(t, o)
		if fmt.Sprint(log.keys) != "[alpha beta]" {
			t.Fatalf("launches = %v", log.keys)
		}
		jsonEq(t, res.Value, `[{"key":"alpha","ok":true,"output":"done-alpha"},{"key":"beta","ok":true,"output":"done-beta"}]`)
		var warns []WorkflowConsoleEntry
		for _, c := range res.Console {
			if c.Level == "warn" {
				warns = append(warns, c)
			}
		}
		if len(warns) != 1 || !strings.Contains(warns[0].Text, "runs.run(...) launch promise") || !strings.Contains(warns[0].Text, "pass config objects") {
			t.Fatalf("warnings = %+v", warns)
		}
	})

	tw(t, scriptedFile, "accepts a mixed runs.all shape combining a config object and a runs.run(...) promise", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const results = await runs.all([
					{ key: "config-child", agent: "worker", task: "config" },
					runs.run("promise-child", { agent: "worker", task: "promise" }),
				]);
				return results.map((entry) => ({ key: entry.key, ok: entry.ok }));
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "done"))
		})
		res := wfRun(t, o)
		keys := append([]string(nil), log.keys...)
		sort.Strings(keys)
		if fmt.Sprint(keys) != "[config-child promise-child]" {
			t.Fatalf("launches = %v", keys)
		}
		jsonEq(t, res.Value, `[{"key":"config-child","ok":true},{"key":"promise-child","ok":true}]`)
	})

	tw(t, scriptedFile, "still rejects non-object non-thenable items in a permissive runs.all call", func(t *testing.T) {
		o := wfOptions(`return await runs.all([runs.run("valid", { agent: "worker", task: "run" }), null]);`)
		o.TimeoutMs = new(2000.0)
		matchErr(t, wfFail(t, o), `must be an object or a runs\.run\(\.\.\.\) launch promise`)
	})

	tw(t, scriptedFile, "rejects a runs.all batch incompatible with an earlier key before dispatching the batch", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
					await runs.run("same", { agent: "worker", task: "one" });
					return await runs.all([
						{ key: "valid", agent: "worker", task: "run" },
						{ key: "same", agent: "worker", task: "two" }
					]);
				`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		matchErr(t, wfFail(t, o), `Duplicate workflow key 'same'`)
		if fmt.Sprint(log.keys) != "[same]" {
			t.Fatalf("launches = %v", log.keys)
		}
	})

	tw(t, scriptedFile, "reports host-side children in launch order", func(t *testing.T) {
		o := wfOptions(`return await runs.all([
				{ key: "slow", agent: "worker", task: "slow" },
				{ key: "fast", agent: "worker", task: "fast" }
			]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			d := time.Duration(0)
			if key == "slow" {
				d = 30 * time.Millisecond
			}
			return WorkflowAfter(d, jo("key", key, "ok", true, "output", key, "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		var keys []string
		for _, c := range res.Value.([]any) {
			keys = append(keys, objStr(c.(*jsObject), "key"))
		}
		if fmt.Sprint(keys) != "[slow fast]" || fmt.Sprint(childKeys(res.Children)) != "[slow fast]" {
			t.Fatalf("value keys %v children %v", keys, childKeys(res.Children))
		}
	})

	tw(t, scriptedFile, "omits undefined child result fields before a script returns them", func(t *testing.T) {
		o := wfOptions(`return await runs.run("artifact-only", { agent: "worker", task: "write output" });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "output", "Saved output.", "artifactPaths", []any{"/tmp/output.md"}, "results", []any{jo("messages", undef{}, "savedOutputPath", "/tmp/output.md")}))
		}
		res := wfRun(t, o)
		jsonEq(t, res.Value, `{"key":"artifact-only","ok":true,"output":"Saved output.","artifactPaths":["/tmp/output.md"],"results":[{"savedOutputPath":"/tmp/output.md"}]}`)
	})

	tw(t, scriptedFile, "omits undefined fields in workflow return objects", func(t *testing.T) {
		o := wfOptions(`
				const children = await runs.all([{ key: "review", agent: "worker", task: "review" }]);
				return children.map((child) => ({
					key: child.key,
					status: child.status,
					output: child.output,
					values: [child.status],
				}));
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "output", "completed", "artifactPaths", []any{}, "results", []any{}))
		}
		jsonEq(t, wfRun(t, o).Value, `[{"key":"review","output":"completed","values":[null]}]`)
	})

	tw(t, scriptedFile, "reports completed child references when return serialization fails", func(t *testing.T) {
		o := wfOptions(`const child = await runs.run("writer", { agent: "worker", task: "write" }); return { child, invalid: new Map([["key", "value"]]) };`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-writer", "output", "saved", "outputReference", "/tmp/writer-output.md", "artifactPaths", []any{"/tmp/writer-artifact.md"}))
		}
		we := wfFail(t, o)
		for _, want := range []string{"return.invalid must contain only plain JSON objects", "Child work completed before return serialization failed", "'writer' (runId=run-writer, outputReference=/tmp/writer-output.md, artifact=/tmp/writer-artifact.md)", "Return a plain projection"} {
			if !strings.Contains(we.Message, want) {
				t.Errorf("message lacks %q: %s", want, we.Message)
			}
		}
		if len(we.Partial.Children) == 0 || objStr(we.Partial.Children[0], "runId") != "run-writer" {
			t.Fatalf("children = %v", we.Partial.Children)
		}
	})

	tw(t, scriptedFile, "omits non-JSON child result metadata before returning reused runs.run results", func(t *testing.T) {
		launches := 0
		o := wfOptions(`
				const first = await runs.run("non-plain", { agent: "worker", task: "write output" });
				const reused = await runs.run("non-plain", { agent: "worker", task: "write output" });
				return [first, reused];
			`)
		o.TimeoutMs = new(2000.0)
		type nonPlain struct{ source string } // a Map in the original: not a plain JSON object
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launches++
			return WorkflowResolved(jo("key", key, "ok", true, "output", "Saved output.", "artifactPaths", []any{}, "results", []any{jo("metadata", nonPlain{"worker"})}))
		}
		res := wfRun(t, o)
		if launches != 1 {
			t.Fatalf("launches = %d", launches)
		}
		jsonEq(t, res.Value, `[{"key":"non-plain","ok":true,"output":"Saved output.","artifactPaths":[]},{"key":"non-plain","ok":true,"output":"Saved output.","artifactPaths":[]}]`)
		results, _ := res.Children[0].vals["results"].([]any)
		if len(results) != 1 || results[0].(*jsObject).vals["metadata"] == nil {
			t.Fatalf("the host keeps the metadata: %v", res.Children[0].vals["results"])
		}
	})

	tw(t, scriptedFile, "passes retained resume items and rejects agent overrides", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`return runs.run("continue", { resume: "retained-run", task: "Apply the follow-up" });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "revived-run", "output", "continued", "artifactPaths", []any{}))
		})
		res := wfRun(t, o)
		jsonEq(t, log.params[0], `{"resume":"retained-run","task":"Apply the follow-up"}`)
		if res.Value.(*jsObject).vals["runId"] != "revived-run" {
			t.Fatalf("value = %s", marshalJSON(res.Value, ""))
		}
		o = wfOptions(`return runs.run("invalid", { resume: "retained-run", agent: "worker", task: "Override" });`)
		o.TimeoutMs = new(2000.0)
		matchErr(t, wfFail(t, o), `resume and agent are mutually exclusive`)
	})

	tw(t, scriptedFile, "passes per-child baseRef through runs.run and runs.all", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const one = await runs.run("one", { agent: "worker", task: "one", baseRef: "refs/heads/release" });
				const rest = await runs.all([
					{ key: "two", agent: "worker", task: "two", baseRef: "refs/heads/topic" },
					{ key: "head", agent: "worker", task: "head", baseRef: "HEAD" },
					{ key: "default", agent: "worker", task: "default" }
				]);
				return [one.key, ...rest.map((entry) => entry.key)];
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		wfRun(t, o)
		var got []string
		for i, p := range log.params {
			ref, ok := p.vals["baseRef"].(string)
			if !ok {
				ref = "undefined"
			}
			got = append(got, log.keys[i]+"="+ref)
		}
		if strings.Join(got, ",") != "one=refs/heads/release,two=refs/heads/topic,head=HEAD,default=undefined" {
			t.Fatalf("got %v", got)
		}
	})

	tw(t, scriptedFile, "rejects unsupported literal and computed child baseRef values before dispatch", func(t *testing.T) {
		var log launchLog
		for _, c := range []struct{ call, expression string }{
			{"run", `"` + strings.Repeat("a", 40) + `"`},
			{"all", `"` + strings.Repeat("A", 64) + `"`},
			{"run", `"a".repeat(64)`},
			{"all", `"A".repeat(40)`},
			{"run", `"HEAD" + "~1"`},
			{"all", `"@"`},
			{"run", `42`},
		} {
			script := fmt.Sprintf(`return runs.run("invalid", { agent: "worker", task: "Check", baseRef: %s });`, c.expression)
			if c.call == "all" {
				script = fmt.Sprintf(`return runs.all([{ key: "valid", agent: "worker", task: "Check", baseRef: "HEAD" }, { key: "invalid", agent: "worker", task: "Check", baseRef: %s }]);`, c.expression)
			}
			if strings.Contains(c.expression, "repeat") || strings.Contains(c.expression, " + ") {
				if r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{}); !r.OK || len(r.Errors) != 0 {
					t.Fatalf("%s: %+v", script, r)
				}
			}
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			o.Launch = log.fn(okAnswer)
			matchErr(t, wfFail(t, o), `baseRef.*HEAD.*named ref.*40/64-character commit IDs.*revision expressions.*unsupported`)
		}
		if len(log.keys) != 0 {
			t.Fatalf("launches = %v", log.keys)
		}
	})

	tw(t, scriptedFile, "leaves accessor-derived baseRef values to runtime validation", func(t *testing.T) {
		for _, baseRef := range []string{"HEAD", "HEAD~1"} {
			script := fmt.Sprintf(`return runs.run("child", { agent: "worker", task: "Check", baseRef: "invalid..ref", get baseRef() { return %q; } });`, baseRef)
			if r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{}); !r.OK || len(r.Errors) != 0 {
				t.Fatalf("%s: %+v", script, r)
			}
			var log launchLog
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			o.Launch = log.fn(okAnswer)
			if baseRef == "HEAD" {
				wfRun(t, o)
				if len(log.params) != 1 || log.params[0].vals["baseRef"] != "HEAD" {
					t.Fatalf("launches = %v", log.params)
				}
			} else {
				matchErr(t, wfFail(t, o), `baseRef.*revision expressions.*unsupported`)
				if len(log.keys) != 0 {
					t.Fatalf("launches = %v", log.keys)
				}
			}
		}
	})

	tw(t, scriptedFile, "passes per-child workflow controls through runs.run and runs.all", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const one = await runs.run("one", { agent: "worker", task: "one", worktree: true, control: { needsAttentionAfterMs: 111 } });
				const rest = await runs.all([
					{ key: "two", agent: "worker", task: "two", worktree: true, control: { activeNoticeAfterMs: 222 } },
					{ key: "three", agent: "reviewer", task: "three", worktree: false, control: { enabled: false } }
				]);
				return [one.key, ...rest.map((entry) => entry.key)];
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		wfRun(t, o)
		var got []any
		for i, p := range log.params {
			got = append(got, jo("key", log.keys[i], "worktree", p.vals["worktree"], "control", p.vals["control"]))
		}
		jsonEq(t, got, `[{"key":"one","worktree":true,"control":{"needsAttentionAfterMs":111}},{"key":"two","worktree":true,"control":{"activeNoticeAfterMs":222}},{"key":"three","worktree":false,"control":{"enabled":false}}]`)
	})

	tw(t, scriptedFile, "validates bounded lane metadata and passes it to child launch", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`return runs.run("writer", { agent: "worker", task: "write", lane: { version: 1, key: "writer", mode: "mutation", sourceRef: "owner/repo#1621", claims: ["src/shared/types.ts"], outputPaths: ["reports/writer.md"] } });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		wfRun(t, o)
		jsonEq(t, log.params[0].vals["lane"], `{"version":1,"key":"writer","mode":"mutation","sourceRef":"owner/repo#1621","claims":["src/shared/types.ts"],"outputPaths":["reports/writer.md"]}`)
		o = wfOptions(`return runs.run("writer", { agent: "worker", task: "write", lane: { version: 1, key: "other" } });`)
		o.TimeoutMs = new(2000.0)
		matchErr(t, wfFail(t, o), `must match workflow key`)
	})

	tw(t, scriptedFile, "passes distinct namespaced bindings to parallel children", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`return runs.all([
				{ key: "coder", agent: "worker", task: "code", extensionBindings: { "shepherd.dispatch/1": { role: "coder" } } },
				{ key: "reviewer", agent: "reviewer", task: "review", extensionBindings: { "shepherd.dispatch/1": { role: "reviewer" } } }
			]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		wfRun(t, o)
		var got []any
		for i, p := range log.params {
			got = append(got, jo("key", log.keys[i], "bindings", p.vals["extensionBindings"]))
		}
		jsonEq(t, got, `[{"key":"coder","bindings":{"shepherd.dispatch/1":{"role":"coder"}}},{"key":"reviewer","bindings":{"shepherd.dispatch/1":{"role":"reviewer"}}}]`)
	})

	tw(t, scriptedFile, "composes dynamic sequential and parallel phases with per-child controls", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const plan = await runs.run("plan", { agent: "planner", task: "plan", worktree: true });
				const targets = ["api", "ui"];
				const built = await runs.all(targets.map((target) => ({
					key: "build-" + target,
					agent: "worker",
					task: plan.output + ":" + target,
					worktree: true
				})));
				const review = await runs.run("review", {
					agent: "reviewer",
					task: built.map((child) => child.key).join(","),
					worktree: false
				});
				return { plan: plan.key, built: built.map((child) => child.key), review: review.key };
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		res := wfRun(t, o)
		jsonEq(t, res.Value, `{"plan":"plan","built":["build-api","build-ui"],"review":"review"}`)
		var got []any
		for i, p := range log.params {
			got = append(got, jo("key", log.keys[i], "agent", p.vals["agent"], "task", p.vals["task"], "worktree", p.vals["worktree"]))
		}
		jsonEq(t, got, `[{"key":"plan","agent":"planner","task":"plan","worktree":true},{"key":"build-api","agent":"worker","task":"plan:api","worktree":true},{"key":"build-ui","agent":"worker","task":"plan:ui","worktree":true},{"key":"review","agent":"reviewer","task":"build-api,build-ui","worktree":false}]`)
	})

	tw(t, scriptedFile, "rejects legacy orchestration params in runs.run", func(t *testing.T) {
		for _, params := range []string{`tasks: [{ agent: "scout", task: "scan" }]`, `parallel: [{ agent: "scout", task: "scan" }]`, `globalConcurrencyLimit: 2`, `maxSubagentSpawnsPerRun: 2`} {
			var log launchLog
			o := wfOptions(fmt.Sprintf(`return await runs.run("legacy", { %s });`, params))
			o.TimeoutMs = new(2000.0)
			o.Launch = log.fn(okAnswer)
			matchErr(t, wfFail(t, o), `(?i)accepts one child.*runs\.all`)
			if len(log.keys) != 0 {
				t.Fatalf("launched %v", log.keys)
			}
		}
	})

	tw(t, scriptedFile, "rejects clarify UI on workflow children", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`return runs.run("clarify", { agent: "worker", task: "Review", clarify: true });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		matchErr(t, wfFail(t, o), `does not support clarify UI`)
		if len(log.keys) != 0 {
			t.Fatalf("launched %v", log.keys)
		}
	})

	tw(t, scriptedFile, "rejects a duplicate key with incompatible params", func(t *testing.T) {
		o := wfOptions(`
					await runs.run("same", { agent: "scout", task: "one" });
					await runs.run("same", { agent: "scout", task: "two" });
				`)
		o.TimeoutMs = new(2000.0)
		matchErr(t, wfFail(t, o), `Duplicate workflow key 'same'`)
	})
}
