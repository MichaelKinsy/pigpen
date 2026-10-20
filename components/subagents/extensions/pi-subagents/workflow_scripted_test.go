package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Twins of test/unit/scripted-workflow.test.ts (the original's workflow sandbox tests). A promise-returning callback becomes a
// *WorkflowFuture; an `assert.rejects` becomes a wfFail; `assert.deepEqual` becomes jsonEq.

const scriptedFile = "scripted-workflow"

func TestScriptedWorkflowTwins(t *testing.T) {
	tw(t, scriptedFile, "exposes supplied and executor-normalized empty arguments as deeply frozen values", func(t *testing.T) {
		o := wfOptions(`return { value: args.nested.items[0], rootFrozen: Object.isFrozen(args), nestedFrozen: Object.isFrozen(args.nested), arrayFrozen: Object.isFrozen(args.nested.items) };`)
		o.Args = jo("nested", jo("items", []any{"kept"}))
		jsonEq(t, wfRun(t, o).Value, `{"value":"kept","rootFrozen":true,"nestedFrozen":true,"arrayFrozen":true}`)

		o = wfOptions(`return { args, frozen: Object.isFrozen(args) };`)
		jsonEq(t, wfRun(t, o).Value, `{"args":{},"frozen":true}`)

		o = wfOptions(strings.Join([]string{
			"const attempts = [args, args.nested, args.nested.items].map((value) => {",
			`  try { return value.constructor.constructor("return typeof process")(); } catch (error) { return error.constructor.name; }`,
			"});",
			"return attempts;",
		}, "\n"))
		o.Args = jo("nested", jo("items", []any{"kept"}))
		jsonEq(t, wfRun(t, o).Value, `["EvalError","EvalError","EvalError"]`, "args prototypes must not reach an unrestricted Function")
	})

	tw(t, scriptedFile, "uses ordinary statement-body return semantics", func(t *testing.T) {
		implicit := wfRun(t, wfOptions(`({ answer: 42 });`))
		explicit := wfRun(t, wfOptions(`return ({ answer: 42 });`))
		if implicit.Value != nil {
			t.Fatalf("implicit = %v, want null", implicit.Value)
		}
		jsonEq(t, explicit.Value, `{"answer":42}`)
	})

	tskip(t, scriptedFile, "resolves the workflow parser from pi-subagents outside the project cwd", "tests Node's resolution of the acorn package from the extension directory; the Go port parses with goja and has no parser package to resolve")

	tw(t, scriptedFile, "guides invalid JavaScript caused by Markdown fence backticks", func(t *testing.T) {
		script := strings.Join([]string{"const task = `Run:", "```bash", "npm test", "```;", "return task;"}, "\n")
		o := wfOptions(script)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "unexpected"))
		}
		we := wfFail(t, o)
		for _, want := range []string{"workflowScript must be valid JavaScript", `array joined with "\n"`, "Unexpected token", "SyntaxError"} {
			if !strings.Contains(we.Message, want) {
				t.Errorf("message lacks %q: %s", want, we.Message)
			}
		}
	})

	tw(t, scriptedFile, "validates workflow syntax and portable structure offline", func(t *testing.T) {
		syntax := ValidateWorkflowScript(strings.Join([]string{"const value = 1;", "return (;"}, "\n"), WorkflowScriptValidationOptions{})
		if syntax.OK || syntax.Errors[0].Line != 2 || syntax.Errors[0].Column <= 0 || strings.HasSuffix(syntax.Errors[0].Message, "(3:9)") {
			t.Fatalf("syntax = %+v", syntax)
		}
		if strings.Contains(syntax.Errors[0].Message, "(3:") {
			t.Errorf("message keeps the position: %q", syntax.Errors[0].Message)
		}

		structural := ValidateWorkflowScript(strings.Join([]string{
			`const results = await runs.all([{ key: "bad key", agent: "worker" }, { key: "someChild", agent: "reviewer" }]);`,
			`async function helper() { return "no"; }`,
			`return results.someChild;`,
		}, "\n"), WorkflowScriptValidationOptions{})
		has := func(r WorkflowScriptValidationResult, sub string) bool {
			for _, e := range r.Errors {
				if strings.Contains(e.Message, sub) {
					return true
				}
			}
			return false
		}
		if structural.OK || !has(structural, "runs.all item key") || !has(structural, "nested async functions") || !has(structural, "ordered array") {
			t.Fatalf("structural = %+v", structural)
		}
		invalidRunKey := ValidateWorkflowScript(`return runs.run("bad key", { agent: "worker" });`, WorkflowScriptValidationOptions{})
		if invalidRunKey.OK || !has(invalidRunKey, "runs.run key") {
			t.Fatalf("invalidRunKey = %+v", invalidRunKey)
		}
		for _, script := range []string{
			`return runs.run("single", { agent: "worker", task: undefined });`,
			`return runs.all([{ key: "same", agent: "worker", task: undefined }]);`,
		} {
			r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{})
			if r.OK || !has(r, "undefined is not JSON-representable") {
				t.Fatalf("%s: %+v", script, r)
			}
		}
		for _, script := range []string{
			`return runs.run("single", { agent: "worker", task: undefined, task: "real" });`,
			`return runs.all([{ ...{ key: "bad key" }, agent: "worker" }]);`,
			`return runs.run("same", { agent: selectedAgent });`,
		} {
			if r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{}); !r.OK || len(r.Errors) != 0 {
				t.Fatalf("%s: %+v", script, r)
			}
		}
	})

	tw(t, scriptedFile, "reports literal child baseRef policy errors with source locations offline", func(t *testing.T) {
		for _, c := range []struct{ call, value string }{
			{"run", `"` + strings.Repeat("a", 40) + `"`},
			{"run", `"` + strings.Repeat("A", 64) + `"`},
			{"all", `"` + strings.Repeat("A", 40) + `"`},
			{"all", "`" + strings.Repeat("a", 64) + "`"},
			{"run", `"HEAD~1"`},
			{"all", `"refs/heads/bad..ref"`},
			{"run", "null"},
			{"all", "42"},
			{"run", "false"},
		} {
			first := `return runs.all([{`
			last := `}]);`
			if c.call == "run" {
				first, last = `return runs.run("child", {`, `});`
			}
			script := strings.Join([]string{first, `  key: "child", agent: "worker", task: "Check",`, "  baseRef: " + c.value, last}, "\n")
			r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{})
			if r.OK || len(r.Errors) != 1 || r.Errors[0].Line != 3 || r.Errors[0].Column != 12 {
				t.Fatalf("%s: %+v", script, r)
			}
			m := r.Errors[0].Message
			if !strings.Contains(m, "runs."+c.call) || !strings.Contains(m, "baseRef") || !strings.Contains(m, "HEAD") || !strings.Contains(m, "named ref") || !strings.Contains(m, "40/64-character commit IDs") || !strings.Contains(m, "revision expressions") || !strings.Contains(m, "unsupported") {
				t.Fatalf("message %q", m)
			}
		}
	})

	tw(t, scriptedFile, "validates only the final statically known child baseRef without guessing overwrites", func(t *testing.T) {
		for _, fields := range []string{
			"", `baseRef: "HEAD"`, `baseRef: "refs/heads/release"`, `baseRef: "refs/tags/v1"`, `baseRef: "origin/main"`,
			`baseRef: "HEAD~1", baseRef: "HEAD"`, "baseRef: \"HEAD~1\", [\"baseRef\"]: `HEAD`", `baseRef: "HEAD~1", baseRef: selectedRef`,
			`baseRef: "HEAD~1", baseRef: "refs/heads/" + branch`, `baseRef: "HEAD~1", ...overrides`, `baseRef: "HEAD~1", [field]: "HEAD"`,
			`baseRef: "HEAD~1", get baseRef() { return "HEAD"; }`, `baseRef: "HEAD~1", set baseRef(value) {}`,
		} {
			for _, script := range []string{
				fmt.Sprintf(`return runs.run("child", { agent: "worker", task: "Check", %s });`, fields),
				fmt.Sprintf(`return runs.all([{ key: "child", agent: "worker", task: "Check", %s }]);`, fields),
			} {
				if r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{}); !r.OK || len(r.Errors) != 0 {
					t.Fatalf("%s: %+v", script, r)
				}
			}
		}
		for _, fields := range []string{
			`baseRef: "HEAD", baseRef: "HEAD~1"`, `...defaults, baseRef: "HEAD~1"`, `[field]: "HEAD", ["baseRef"]: "HEAD~1"`, `get baseRef() { return "HEAD"; }, baseRef: "HEAD~1"`,
		} {
			r := ValidateWorkflowScript(fmt.Sprintf(`return runs.run("child", { agent: "worker", task: "Check", %s });`, fields), WorkflowScriptValidationOptions{})
			if r.OK || !strings.Contains(r.Errors[0].Message, "baseRef") {
				t.Fatalf("%s: %+v", fields, r)
			}
		}
		if ValidateWorkflowScript(`return runs.run("child", { baseRef() { return "HEAD"; } });`, WorkflowScriptValidationOptions{}).OK {
			t.Fatalf("a baseRef method must be invalid")
		}
	})

	tw(t, scriptedFile, "validates runs.host shape offline without executing it", func(t *testing.T) {
		if r := ValidateWorkflowScript(`return runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, output: "reports/tests.log", role: "ci" });`, WorkflowScriptValidationOptions{}); !r.OK || len(r.Errors) != 0 {
			t.Fatalf("%+v", r)
		}
		for _, script := range []string{
			`return runs.host("tests", { command: "npm test", timeoutMs: 1000 });`,
			`return runs.host("tests", { kind: "http", command: "npm test", timeoutMs: 1000 });`,
			`return runs.host("tests", { kind: "command", command: "npm test" });`,
			`return runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, output: "../tests.log" });`,
		} {
			if ValidateWorkflowScript(script, WorkflowScriptValidationOptions{}).OK {
				t.Fatalf("valid: %s", script)
			}
		}
		cwd := ValidateWorkflowScript(`return runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, cwd: "/tmp" });`, WorkflowScriptValidationOptions{})
		found := false
		for _, e := range cwd.Errors {
			if strings.Contains(e.Message, "unsupported field 'cwd'") && strings.Contains(e.Message, "does not accept per-step cwd") && strings.Contains(e.Message, "workflow cwd") && strings.Contains(e.Message, "outer subagent request") && strings.Contains(e.Message, "cd /path/to/worktree") {
				found = true
			}
		}
		if cwd.OK || !found {
			t.Fatalf("cwd = %+v", cwd)
		}
	})

	tw(t, scriptedFile, "explains the workflow cwd workaround for dynamic runs.host params", func(t *testing.T) {
		called := false
		o := wfOptions(`const params = { kind: "command", command: "npm test", timeoutMs: 1000, cwd: "/tmp" }; return await runs.host("tests", params);`)
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			called = true
			return WorkflowResolved(hostOK(key, "tests.log"))
		}
		we := wfFail(t, o)
		for _, want := range []string{"does not accept per-step cwd", "workflow cwd", "outer subagent request", "cd /path/to/worktree"} {
			if !strings.Contains(we.Message, want) {
				t.Errorf("message lacks %q: %s", want, we.Message)
			}
		}
		if called {
			t.Fatalf("the host ran a command that fails validation")
		}
	})

	tw(t, scriptedFile, "runs an awaited host command through the host boundary", func(t *testing.T) {
		var steps []string
		o := wfOptions(`return await runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, role: "ci", provider: "local" });`)
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			return WorkflowResolved(WorkflowHostCommandResult{Key: key, OK: true, State: "passed", ExitCode: new(0), Stdout: "ok", OutputPath: "tests.log", DurationMs: 2})
		}
		o.OnHostStep = func(s WorkflowHostStepNode) { steps = append(steps, s.State+":"+s.Role) }
		res := wfRun(t, o)
		if st := res.Value.(*jsObject).vals["state"]; st != "passed" {
			t.Fatalf("state = %v", st)
		}
		if strings.Join(steps, ",") != "running:ci,done:ci" {
			t.Fatalf("steps = %v", steps)
		}
		if got := traceStates(res.Trace); fmt.Sprint(got) != "[[host started] [host completed]]" {
			t.Fatalf("trace = %v", got)
		}
	})

	tw(t, scriptedFile, "fails the workflow when a host command fails", func(t *testing.T) {
		o := wfOptions(`return await runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000 });`)
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			return WorkflowResolved(WorkflowHostCommandResult{Key: key, OK: false, State: "failed", ExitCode: new(2), Stderr: "bad", OutputPath: "tests.log", DurationMs: 2, Error: "Command exited with code 2."})
		}
		if we := wfFail(t, o); !strings.Contains(we.Message, "Host command 'tests' failed") {
			t.Fatalf("message = %q", we.Message)
		}
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			return WorkflowRejected[WorkflowHostCommandResult](errors.New("host boundary failed"))
		}
		if we := wfFail(t, o); !strings.Contains(we.Message, "host boundary failed") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects an unawaited host command", func(t *testing.T) {
		o := wfOptions(`runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000 }); return "done";`)
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			return WorkflowResolved(hostOK(key, "tests.log"))
		}
		if we := wfFail(t, o); !strings.Contains(we.Message, "unawaited runs.host") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "bounds host command rows", func(t *testing.T) {
		var calls []string
		for i := range 33 {
			calls = append(calls, fmt.Sprintf(`await runs.host("host-%d", { kind: "command", command: "true", timeoutMs: 1000 });`, i))
		}
		o := wfOptions(strings.Join(calls, "\n") + "\nreturn \"done\";")
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			return WorkflowResolved(hostOK(key, key+".log"))
		}
		if we := wfFail(t, o); !strings.Contains(we.Message, "at most 32 runs.host calls") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "rejects a statically provable workflow that exceeds its spawn budget", func(t *testing.T) {
		script := strings.Join([]string{
			"const [architecture, adjacent, proof] = await runs.all([",
			`  { key: "architecture", agent: "scout", task: "Inspect architecture" },`,
			`  { key: "adjacent", agent: "scout", task: "Inspect adjacent scope" },`,
			`  { key: "proof", agent: "oracle", task: "Challenge the proof" },`,
			"]);",
			`const owner = await runs.run("owner", { agent: "worker", task: architecture.output });`,
			`const review = await runs.run("review", { agent: "reviewer", task: owner.output });`,
			`const fixed = await runs.run("owner-fix", { resume: owner.runId, task: review.output });`,
			"return { adjacent: adjacent.output, proof: proof.output, fixed: fixed.output };",
		}, "\n")
		rejected := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{MaxSubagentSpawnsPerRun: new(5)})
		want := WorkflowScriptValidationError{Kind: "spawn-budget", Message: "workflowScript statically requires child launches 'architecture', 'adjacent', 'proof', 'owner', 'review', 'owner-fix'; minimum required: 6; configured: 5."}
		if rejected.OK || len(rejected.Errors) != 1 || rejected.Errors[0] != want {
			t.Fatalf("rejected = %+v", rejected)
		}
		if ok := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{MaxSubagentSpawnsPerRun: new(6)}); !ok.OK || len(ok.Errors) != 0 {
			t.Fatalf("ok = %+v", ok)
		}
	})

	tw(t, scriptedFile, "checks literal child agent names that resolve from the workflow cwd and scope", func(t *testing.T) {
		known := map[string]bool{"worker": true, "reviewer": true}
		agentNameError := func(name string) string {
			if known[name] {
				return ""
			}
			return fmt.Sprintf("Unknown agent '%s'.", name)
		}
		r := ValidateWorkflowScript(strings.Join([]string{
			`const scan = await runs.run("scan", { agent: "worker", task: "Scan" });`,
			`await runs.all([{ key: "review", agent: "reviwer", task: scan.output }]);`,
			`await runs.lanes([{ key: "lane", stages: [{ key: "write", agent: "wroker", task: "Write" }, { key: "fix", resume: "previous", task: "Fix" }] }]);`,
			`await runs.run("elsewhere", { agent: "remote-only", cwd: "../other", task: "Scan" });`,
			`await runs.run("scoped", { agent: "project-only", agentScope: "project", task: "Scan" });`,
			`await runs.run("spread", { agent: "missing", ...overrides });`,
			`await runs.lanes([{ key: "replaced", stages: [{ key: "write", agent: "missing", task: "Write" }], ...laneOverrides }]);`,
			`return runs.run("dynamic", { agent: selectedAgent, task: "Scan" });`,
		}, "\n"), WorkflowScriptValidationOptions{AgentNameError: agentNameError})
		if r.OK || len(r.Errors) != 2 {
			t.Fatalf("r = %+v", r)
		}
		jsonEq(t, []any{
			jo("kind", r.Errors[0].Kind, "message", r.Errors[0].Message, "line", float64(r.Errors[0].Line)),
			jo("kind", r.Errors[1].Kind, "message", r.Errors[1].Message, "line", float64(r.Errors[1].Line)),
		}, `[{"kind":"agent","message":"runs.all item: Unknown agent 'reviwer'.","line":2},{"kind":"agent","message":"runs.lanes stage: Unknown agent 'wroker'.","line":3}]`)
		shadow := ValidateWorkflowScript("const runs = { run: (key) => key };\nreturn runs.run(\"local\", { agent: \"missing\" });", WorkflowScriptValidationOptions{AgentNameError: func(string) string { return "Unknown agent." }})
		if !shadow.OK || len(shadow.Errors) != 0 {
			t.Fatalf("shadow = %+v", shadow)
		}
	})

	tw(t, scriptedFile, "warns instead of guessing a dynamic spawn count", func(t *testing.T) {
		r := ValidateWorkflowScript(strings.Join([]string{
			`const prefix = "lane";`,
			`const child = await runs.run(prefix + "-writer", { agent: selectedAgent, task: taskText });`,
			`const results = await runs.all(items.map((item) => ({ key: item.key, agent: item.agent, task: item.task })));`,
			`if (needsReview) await runs.run("conditional-review", { agent: "reviewer", task: child.output });`,
			`return { child: child.output, outputs: results.map((entry) => entry.output) };`,
		}, "\n"), WorkflowScriptValidationOptions{MaxSubagentSpawnsPerRun: new(1)})
		if !r.OK || len(r.Errors) != 0 || len(r.Warnings) == 0 || r.Warnings[0].Kind != "dynamic-spawn-count" {
			t.Fatalf("r = %+v", r)
		}
		m := r.Warnings[0].Message
		if !strings.Contains(m, "proved 0 launch(es)") || !strings.Contains(m, "configured budget of 1") {
			t.Fatalf("warning = %q", m)
		}
	})

	tw(t, scriptedFile, "keeps uncertain control flow and shadowed workflow APIs advisory", func(t *testing.T) {
		for _, script := range []string{
			"if (true) return \"done\";\nawait runs.run(\"a\", {}); await runs.run(\"b\", {});",
			`try { return "done"; } catch (error) { await runs.run("a", {}); await runs.run("b", {}); }`,
			"class Deferred { child = runs.run(\"a\", {}); }\nreturn \"done\";",
			"maybe?.(runs.run(\"a\", {}));\nreturn \"done\";",
			`{ const runs = { run(key) { return key; } }; runs.run("a", {}); runs.run("b", {}); }`,
			`return runs.all([{ key: "a", agent: "worker", ...overrides }, { key: "b", agent: "worker" }]);`,
		} {
			r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{MaxSubagentSpawnsPerRun: new(1)})
			if !r.OK || len(r.Errors) != 0 || len(r.Warnings) == 0 || r.Warnings[0].Kind != "dynamic-spawn-count" {
				t.Fatalf("%s: %+v", script, r)
			}
		}
	})

	tw(t, scriptedFile, "keeps dynamic workflow keys silent when no spawn budget is requested", func(t *testing.T) {
		r := ValidateWorkflowScript(strings.Join([]string{
			`const prefix = "lane";`,
			`const child = await runs.run(prefix + "-writer", { agent: selectedAgent, task: taskText });`,
			`const results = await runs.all(items.map((item) => ({ key: item.key, agent: item.agent, task: item.task })));`,
			`return { child: child.output, outputs: results.map((entry) => entry.output) };`,
		}, "\n"), WorkflowScriptValidationOptions{})
		if !r.OK || len(r.Errors) != 0 || len(r.Warnings) != 0 {
			t.Fatalf("r = %+v", r)
		}
	})

	tw(t, scriptedFile, "allows Array properties that match runs.all child keys", func(t *testing.T) {
		clean := func(script string) bool {
			r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{})
			return r.OK && len(r.Errors) == 0
		}
		join := func(lines ...string) string { return strings.Join(lines, "\n") }
		if !clean(join(
			`const mapResults = await runs.all([{ key: "map", agent: "reviewer", task: "Review" }]);`,
			`const lengthResults = await runs.all([{ key: "length", agent: "reviewer", task: "Review" }]);`,
			`return { outputs: mapResults.map((result) => result.output), count: lengthResults.length };`)) {
			t.Fatalf("Array properties named like child keys must pass")
		}
		keyed := ValidateWorkflowScript(join(`const results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `return results.someChild;`), WorkflowScriptValidationOptions{})
		found := false
		for _, e := range keyed.Errors {
			found = found || strings.Contains(e.Message, "'results.someChild' is keyed access")
		}
		if keyed.OK || !found {
			t.Fatalf("keyed = %+v", keyed)
		}
		for _, bad := range []string{
			join(`let results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `return results.someChild;`),
			join(`let results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `emit(results.someChild);`, `results = { someChild: "local" };`),
			join(`let results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `if (args.replace) results = { someChild: "local" };`, `return results.someChild;`),
			join(`let results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `results ||= { someChild: "local" };`, `return results.someChild;`),
		} {
			if ValidateWorkflowScript(bad, WorkflowScriptValidationOptions{}).OK {
				t.Fatalf("valid: %s", bad)
			}
		}
		for _, good := range []string{
			join(`let results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `results = { someChild: "local" };`, `return results.someChild;`),
			join(`let results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `holder = results = { someChild: "local" };`, `return results.someChild;`),
			join(`const results = await runs.all([{ key: "someChild", agent: "reviewer", task: "Review" }]);`, `{ const results = { someChild: "local" }; emit(results.someChild); }`, `return results[0].output;`),
		} {
			if !clean(good) {
				t.Fatalf("invalid: %s", good)
			}
		}
	})

	tw(t, scriptedFile, "rejects statically non-JSON workflow boundary values", func(t *testing.T) {
		for _, script := range []string{`return void 0;`, `return undefined;`, `return [void 0, { value: void 0 }];`, `return [undefined, { value: undefined }];`} {
			if r := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{}); !r.OK || len(r.Errors) != 0 {
				t.Fatalf("%s: %+v", script, r)
			}
		}
		r := ValidateWorkflowScript(`emit(void 0); emit(undefined); state.set("void", void 0); state.set("undefined", undefined); return [1, , 2];`, WorkflowScriptValidationOptions{})
		undefinedErrors, sparse := 0, false
		for _, e := range r.Errors {
			if strings.Contains(e.Message, "undefined is not JSON-representable") {
				undefinedErrors++
			}
			sparse = sparse || strings.Contains(e.Message, "sparse arrays")
		}
		if r.OK || undefinedErrors != 4 || !sparse {
			t.Fatalf("r = %+v", r)
		}
	})

	tw(t, scriptedFile, "previews only simple explicit-return child scripts", func(t *testing.T) {
		p, ok := PreviewSimpleWorkflowRun(`return runs.run('main', { agent: 'worker', task: 'Review' });`)
		if !ok || p.Agent == nil || *p.Agent != "worker" || p.Task == nil || *p.Task != "Review" {
			t.Fatalf("p = %+v", p)
		}
		p, ok = PreviewSimpleWorkflowRun(`return runs.run("main", {"agent":"scout","task":"Scan"})`)
		if !ok || *p.Agent != "scout" || *p.Task != "Scan" {
			t.Fatalf("p = %+v", p)
		}
		if _, ok := PreviewSimpleWorkflowRun(`const agent = "worker"; return runs.run("main", { agent });`); ok {
			t.Fatalf("a binding must not preview")
		}
		p, ok = PreviewSimpleWorkflowRun(`return runs.run("main", { agent: selected });`)
		if !ok || p.Agent != nil || p.Task != nil {
			t.Fatalf("p = %+v", p)
		}
	})

	tw(t, scriptedFile, "allows scripts to run without a timeout", func(t *testing.T) {
		res := wfRun(t, wfOptions(`return "done";`))
		if res.Value != "done" {
			t.Fatalf("value = %v", res.Value)
		}
	})
	_ = time.Second
}
