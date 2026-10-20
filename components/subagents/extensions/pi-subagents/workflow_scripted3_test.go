package pi_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordedScripts are the workflowScript texts the original's own test handed to runWorkflowScript, by test title
// (testdata/workflow-scripts.json, port/record-workflow-scripts.sh): the long ones are read from it, not retyped.
func recordedScripts(t *testing.T, title string) []string {
	t.Helper()
	raw, err := os.ReadFile("testdata/workflow-scripts.json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string][]struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range all[title] {
		out = append(out, s.Script)
	}
	if len(out) == 0 {
		t.Fatalf("no recorded script for %q", title)
	}
	return out
}

// launchLog is a launch callback that records its calls and answers from fn.
type launchLog struct {
	mu     sync.Mutex
	keys   []string
	params []*jsObject
}

func (l *launchLog) add(key string, params *jsObject) {
	l.mu.Lock()
	l.keys = append(l.keys, key)
	l.params = append(l.params, params)
	l.mu.Unlock()
}

func (l *launchLog) fn(answer func(key string, params *jsObject) *WorkflowFuture[*jsObject]) func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
	return func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
		l.add(key, params)
		return answer(key, params)
	}
}

func okAnswer(key string, _ *jsObject) *WorkflowFuture[*jsObject] {
	return WorkflowResolved(jo("key", key, "ok", true, "output", key, "artifactPaths", []any{}, "results", []any{}))
}

func failKind(t *testing.T, o RunWorkflowScriptOptions, kind string) *WorkflowScriptError {
	t.Helper()
	we := wfFail(t, o)
	if we.Kind != kind {
		t.Fatalf("kind = %q, want %q: %s", we.Kind, kind, we.Message)
	}
	return we
}

func matchErr(t *testing.T, we *WorkflowScriptError, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(we.Message) {
		t.Fatalf("message %q does not match /%s/", we.Message, pattern)
	}
}

func recoveryObject() *jsObject {
	return jo("status", "available-for-review", "reason", "acceptance-metadata-rejected", "reportPath", "/tmp/writer-report.md", "reportHash", strings.Repeat("a", 64))
}

func writerRecoveryResult(key string) *jsObject {
	rec := recoveryObject()
	return jo("key", key, "ok", false, "output", "saved writer report", "error", "Acceptance rejected: malformed acceptance-report", "runId", "writer-run", "outputReference", "/tmp/writer-report.md", "recovery", rec, "artifactPaths", []any{"/tmp/writer-report.md"}, "results", []any{jo("acceptance", jo("status", "rejected", "recovery", rec))})
}

func TestScriptedWorkflowTwinsFailures(t *testing.T) {
	tw(t, scriptedFile, "returns runs.all launch errors without aborting successful siblings", func(t *testing.T) {
		o := wfOptions(`
				const children = await runs.all([
					{ key: "cannot-launch", agent: "missing", task: "fail" },
					{ key: "still-runs", agent: "worker", task: "finish" }
				]);
				return children.map(({ key, ok, error }) => error === undefined ? { key, ok } : { key, ok, error });
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			if key == "cannot-launch" {
				return WorkflowRejected[*jsObject](errors.New("agent is unavailable"))
			}
			return WorkflowAfter(25*time.Millisecond, jo("key", key, "ok", true, "output", "completed", "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		want := `[{"key":"cannot-launch","ok":false,"error":"agent is unavailable"},{"key":"still-runs","ok":true}]`
		jsonEq(t, res.Value, want)
		var children []any
		for _, c := range res.Children {
			if e, has := objHasString(c, "error"); has {
				children = append(children, jo("key", c.vals["key"], "ok", c.vals["ok"], "error", e))
			} else {
				children = append(children, jo("key", c.vals["key"], "ok", c.vals["ok"]))
			}
		}
		jsonEq(t, children, want)
	})

	tw(t, scriptedFile, "accepts one gate command and rejects gate with acceptance", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`return runs.run("gated", { agent: "worker", gate: "npm test" });`)
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "done"))
		})
		wfRun(t, o)
		if log.params[0].vals["gate"] != "npm test" {
			t.Fatalf("gate = %v", log.params[0].vals["gate"])
		}
		o.Script = `return runs.run("gated-disabled", { agent: "worker", gate: "npm test", acceptance: false });`
		wfRun(t, o)
		if log.params[1].vals["gate"] != "npm test" || log.params[1].vals["acceptance"] != false {
			t.Fatalf("params = %s", marshalJSON(log.params[1], ""))
		}
		o.Script = `return runs.run("invalid", { agent: "worker", gate: "npm test", acceptance: "checked" });`
		matchErr(t, wfFail(t, o), `gate cannot be combined with acceptance`)
	})

	tw(t, scriptedFile, "accepts an object gate with json output and rejects malformed object gates", func(t *testing.T) {
		var log launchLog
		gate := `{ command: "classify.sh --report r.md", output: "json", schema: { type: "object" }, timeoutMs: 5000 }`
		o := wfOptions(fmt.Sprintf(`return runs.run("typed", { agent: "reviewer", gate: %s });`, gate))
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "done"))
		})
		wfRun(t, o)
		jsonEq(t, log.params[0].vals["gate"], `{"command":"classify.sh --report r.md","output":"json","schema":{"type":"object"},"timeoutMs":5000}`)
		for _, c := range []struct{ bad, pattern string }{
			{`{ command: "x", output: "yaml" }`, `gate\.output must be "json"`},
			{`{ command: "x", schema: {} }`, `gate\.schema requires gate\.output`},
			{`{ command: "x", cwd: "." }`, `gate\.cwd is not supported`},
			{`{ output: "json" }`, `non-empty command string`},
		} {
			o.Script = fmt.Sprintf(`return runs.run("invalid", { agent: "reviewer", gate: %s });`, c.bad)
			matchErr(t, wfFail(t, o), c.pattern)
		}
	})

	tw(t, scriptedFile, "lanes block on a verdict supplied through a typed gate", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const board = await runs.lanes([{ key: "pr", stages: [
					{ key: "review", agent: "reviewer", gate: { command: "classify.sh", output: "json" } },
					{ key: "land", agent: "worker", task: "Land it" }
				] }]);
				return board[0].stages.map((stage) => [stage.key, stage.state, stage.verdict ?? "none"]);
			`)
		o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			// the runtime bridges a passing json gate into structuredOutput; the workflow only sees the result
			if key == "pr.review" {
				return WorkflowResolved(jo("key", key, "ok", true, "output", "", "structuredOutput", jo("verdict", "blocked", "action", "writer-fix"), "artifactPaths", []any{}))
			}
			return WorkflowResolved(okChild(key, "done"))
		})
		res := wfRun(t, o)
		if fmt.Sprint(log.keys) != "[pr.review]" {
			t.Fatalf("launched = %v", log.keys)
		}
		jsonEq(t, res.Value, `[["review","blocked","blocked"],["land","skipped","none"]]`)
	})

	tw(t, scriptedFile, "reuses a gated key when acceptance false is explicit", func(t *testing.T) {
		var log launchLog
		o := wfOptions(`
				const first = await runs.run("g", { agent: "worker", gate: "npm test" });
				const second = await runs.run("g", { agent: "worker", gate: "npm test", acceptance: false });
				return { first: first.key, second: second.key };
			`)
		o.TimeoutMs = new(2000.0)
		o.Launch = log.fn(okAnswer)
		res := wfRun(t, o)
		jsonEq(t, res.Value, `{"first":"g","second":"g"}`)
		if fmt.Sprint(log.keys) != "[g]" {
			t.Fatalf("launches = %v", log.keys)
		}
	})

	tw(t, scriptedFile, "rejects retained resume with gate", func(t *testing.T) {
		o := wfOptions(`return runs.run("resume", { resume: "retained-run", task: "Continue", gate: "npm test" });`)
		matchErr(t, wfFail(t, o), `gate is not supported with retained resume`)
	})

	tw(t, scriptedFile, "keeps runs.run fail-fast for ordinary child failures", func(t *testing.T) {
		o := wfOptions(`return await runs.run("fails", { agent: "worker", task: "fail" });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", false, "output", "failed", "artifactPaths", []any{}, "results", []any{jo("acceptance", jo("status", "rejected"))}))
		}
		matchErr(t, wfFail(t, o), `Run 'fails' failed: failed`)
	})

	recoveryLaunch := func(log *launchLog, reviewOutput *jsObject) func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
		return log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
			if key == "writer" {
				return WorkflowResolved(writerRecoveryResult(key))
			}
			if reviewOutput != nil {
				return WorkflowResolved(reviewOutput)
			}
			return WorkflowResolved(jo("key", key, "ok", true, "output", "review complete", "runId", "review-run", "artifactPaths", []any{}))
		})
	}

	tw(t, scriptedFile, "continues with a later review after a durable acceptance metadata rejection", func(t *testing.T) {
		var log launchLog
		o := wfOptions(recordedScripts(t, "continues with a later review after a durable acceptance metadata rejection")[0])
		o.Launch = recoveryLaunch(&log, nil)
		res := wfRun(t, o)
		if fmt.Sprint(log.keys) != "[writer review]" {
			t.Fatalf("launches = %v", log.keys)
		}
		jsonEq(t, res.Value, `{"writerOk":false,"writerStatus":"rejected","writerRecovery":{"status":"available-for-review","reason":"acceptance-metadata-rejected","reportPath":"/tmp/writer-report.md","reportHash":"`+strings.Repeat("a", 64)+`"},"reviewOk":true}`)
		var got []string
		for _, e := range res.Trace {
			if e.Operation == "run" && e.State != "started" {
				got = append(got, e.Key+":"+e.State)
			}
		}
		if strings.Join(got, ",") != "writer:failed,review:completed" {
			t.Fatalf("trace = %v", got)
		}
	})

	allowed := func(title string) func(t *testing.T) {
		return func(t *testing.T) {
			var log launchLog
			o := wfOptions(recordedScripts(t, title)[0])
			o.Launch = recoveryLaunch(&log, nil)
			res := wfRun(t, o)
			if fmt.Sprint(log.keys) != "[writer review]" || res.Value != true {
				t.Fatalf("launches = %v value = %v", log.keys, res.Value)
			}
		}
	}
	tw(t, scriptedFile, "allows review prompts to describe mutation nouns after durable acceptance metadata recovery", allowed("allows review prompts to describe mutation nouns after durable acceptance metadata recovery"))
	tw(t, scriptedFile, "allows review prompts to describe anaphoric regression examples after durable acceptance metadata recovery", allowed("allows review prompts to describe anaphoric regression examples after durable acceptance metadata recovery"))
	tw(t, scriptedFile, "allows listed blocked examples after durable acceptance metadata recovery", allowed("allows listed blocked examples after durable acceptance metadata recovery"))
	tw(t, scriptedFile, "allows quoted blocked examples phrased as blocked examples after durable acceptance metadata recovery", allowed("allows quoted blocked examples phrased as blocked examples after durable acceptance metadata recovery"))

	tw(t, scriptedFile, "blocks mutating workflow work after a durable acceptance metadata rejection", func(t *testing.T) {
		scripts := recordedScripts(t, "blocks mutating workflow work after a durable acceptance metadata rejection")
		if len(scripts) < 100 {
			t.Fatalf("only %d recorded prompts", len(scripts))
		}
		for _, script := range scripts {
			var log launchLog
			o := wfOptions(script)
			o.Launch = log.fn(func(key string, p *jsObject) *WorkflowFuture[*jsObject] {
				if key == "mutate" {
					return WorkflowResolved(jo("key", key, "ok", true, "output", "mutated", "artifactPaths", []any{}))
				}
				return WorkflowResolved(writerRecoveryResult(key))
			})
			we := wfFail(t, o)
			if !strings.Contains(we.Message, "only explicit read-only review children with acceptance:false may follow") {
				t.Fatalf("%s\nmessage: %s", script, we.Message)
			}
			if fmt.Sprint(log.keys) != "[writer]" {
				t.Fatalf("%s\nlaunches = %v", script, log.keys)
			}
		}
	})

	tw(t, scriptedFile, "blocks mission state writes after a durable acceptance metadata rejection", func(t *testing.T) {
		values := map[string]any{}
		var log launchLog
		o := wfOptions("\nconst writer = await runs.run(\"writer\", { agent: \"worker\", task: \"write\" });\nawait state.set(\"recovered.output\", writer.output);\n")
		o.State = &WorkflowState{
			Get: func(k string) *WorkflowFuture[any] { return WorkflowResolved(values[k]) },
			Set: func(k string, v any) *WorkflowFuture[struct{}] { values[k] = v; return WorkflowResolved(struct{}{}) },
		}
		o.Launch = recoveryLaunch(&log, nil)
		matchErr(t, wfFail(t, o), `Run 'state\.set\('recovered\.output'\)' cannot launch after run 'writer' returned rejected acceptance recovery`)
		if fmt.Sprint(log.keys) != "[writer]" || len(values) != 0 {
			t.Fatalf("launches = %v values = %v", log.keys, values)
		}
	})

	tw(t, scriptedFile, "classifies a review lane whose child registry lacks repository tools as failed, not completed", func(t *testing.T) {
		o := wfOptions(`return await runs.run("review", { agent: "reviewer", task: "Review the diff" });`)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowRejected[*jsObject](errors.New(formatChildToolDiagnosticForTest("reviewer", []string{"read", "grep", "find", "ls"})))
		}
		we := wfFail(t, o)
		matchErr(t, we, `Run 'review' failed: Agent 'reviewer' requested unavailable child tools: read, grep, find, ls\.`)
		if len(we.Partial.Children) == 0 || objBool(we.Partial.Children[0], "ok") || !strings.Contains(objStr(we.Partial.Children[0], "error"), "requested unavailable child tools") {
			t.Fatalf("children = %v", we.Partial.Children)
		}
		failed := false
		for _, e := range we.Partial.Trace {
			failed = failed || (e.Key == "review" && e.State == "failed")
		}
		if !failed {
			t.Fatalf("trace = %v", we.Partial.Trace)
		}
		summary, err := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: "tool-call", WorkflowRunID: "workflow-review", WorkflowState: "failed", InventoryComplete: true, Trace: we.Partial.Trace, Children: we.Partial.Children})
		if err != nil || summary.Children[0].State != "failed" {
			t.Fatalf("summary = %+v, %v", summary, err)
		}
	})

	tw(t, scriptedFile, "tags only fail-fast detached child errors as detached-child", func(t *testing.T) {
		detached := func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", false, "detached", true, "output", "reply first", "error", "reply first", "artifactPaths", []any{}, "results", []any{}))
		}
		o := wfOptions(`return await runs.run("detaches", { agent: "worker", task: "ask" });`)
		o.TimeoutMs = new(2000.0)
		o.Launch = detached
		we := failKind(t, o, WorkflowFailureDetachedChild)
		matchErr(t, we, `Run 'detaches' detached`)

		o = wfOptions("\n\t\t\t\t\tawait runs.all([{ key: \"detaches\", agent: \"worker\", task: \"ask\" }]);\n\t\t\t\t\tthrow new Error(\"manual hard failure\");\n\t\t\t\t")
		o.TimeoutMs = new(2000.0)
		o.Launch = detached
		we = failKind(t, o, WorkflowFailureScript)
		matchErr(t, we, `manual hard failure`)
		if len(we.Partial.Children) == 0 || !objBool(we.Partial.Children[0], "detached") {
			t.Fatalf("children = %v", we.Partial.Children)
		}
	})

	tw(t, scriptedFile, "classifies validation, script, return serialization, and child failures", func(t *testing.T) {
		assertKind := func(script, kind string, launch func(key string) *WorkflowFuture[*jsObject]) {
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			if launch != nil {
				o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
					return launch(key)
				}
			}
			failKind(t, o, kind)
		}
		assertKind("return (", WorkflowFailureValidation, nil)
		assertKind(`throw new Error("manual failure");`, WorkflowFailureScript, nil)
		assertKind(`JSON.parse("not json");`, WorkflowFailureScript, nil)
		assertKind(`return { value: 1n };`, WorkflowFailureReturnSerialization, nil)
		assertKind(`return runs.run("writer", { agent: "worker", task: "write" });`, WorkflowFailureChild, func(key string) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", false, "output", "child failed", "error", "child failed", "artifactPaths", []any{}))
		})
	})

	tw(t, scriptedFile, "preserves an unawaited child failure kind and classifies runtime setup failures", func(t *testing.T) {
		o := wfOptions(`runs.run("writer", { agent: "worker", task: "write" }); await new Promise(() => {});`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", false, "output", "child failed", "error", "child failed", "artifactPaths", []any{}))
		}
		failKind(t, o, WorkflowFailureChild)

		o = wfOptions(`runs.host("ci", { kind: "command", command: "npm test", timeoutMs: 1000 }); await new Promise(() => {});`)
		o.TimeoutMs = new(2000.0)
		o.Host = func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			return WorkflowRejected[WorkflowHostCommandResult](errors.New("host boundary failed"))
		}
		failKind(t, o, WorkflowFailureScript)

		// an abort before the worker starts is not a failure: it keeps the signal's reason and has no kind
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(errors.New("stopped by user"))
		o = wfOptions(`return "unreachable";`)
		o.Ctx = ctx
		we := wfFail(t, o)
		if we.Message != "stopped by user" || we.Kind != "" {
			t.Fatalf("we = %+v", we)
		}

		o = wfOptions(" ")
		failKind(t, o, WorkflowFailureValidation)
		// (the original also passes `processCwd` for a missing directory here: that option re-anchors the Node process's own
		// cwd, which a Go extension never changes, so the port has no such option; see the skipped cwd cases below)
	})
}

// formatChildToolDiagnosticForTest is formatChildToolDiagnostic (tool-availability.ts:15) without the parent-host variant, for the
// one twin that feeds its text through a failing launch. The function itself belongs to the child tool plan port.
func formatChildToolDiagnosticForTest(agent string, missing []string) string {
	return strings.Join([]string{
		fmt.Sprintf("Agent '%s' requested unavailable child tools: %s.", agent, strings.Join(missing, ", ")),
		"The `tools` field is a strict allowlist; it does not load extension code.",
		"For extension tools, add the provider path to `subagentOnlyExtensions` (child-only), `extensions`, or as a path-like entry in `tools`, while keeping each registered tool name in `tools`.",
		"For MCP tools, verify the MCP adapter configuration and selected tool names. For builtin tools, verify the name against the installed Pi version.",
	}, "\n")
}
