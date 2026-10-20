package pi_subagents

import (
	"strings"
	"testing"
)

func pubParams(t *testing.T, s string) map[string]any {
	t.Helper()
	o := js(t, s).(*jsObject)
	m := map[string]any{}
	for _, k := range o.order() {
		m[k] = o.vals[k]
	}
	return m
}

func normalizePublic(t *testing.T, s string) publicExecution {
	return normalizePublicSubagentExecution(pubParams(t, s))
}

func wantOK(t *testing.T, input, want string) {
	t.Helper()
	r := normalizePublic(t, input)
	if !r.OK {
		t.Errorf("%s: refused: %s", input, r.Error)
		return
	}
	got := newObject()
	for k, v := range r.Params {
		got.set(k, v)
	}
	equal(t, canonicalJSON(got), canonicalJSON(js(t, want)), input)
}

func TestPublicSubagentExecutionNormalization(t *testing.T) {
	tw(t, "public-execution", "accepts structured single-child, workflow, management, and schedules", func(t *testing.T) {
		same := []string{
			`{"workflowScript":"return 1","globalConcurrencyLimit":4,"maxSubagentSpawnsPerRun":8}`,
			`{"workflowScript":"return args.task","args":{"task":"review"}}`,
			`{"workflow":"review","args":{"task":"Review this"}}`,
			`{"workflowScript":"return 1","preflight":{"version":1,"lanes":[]}}`,
			`{"workflowScriptPath":"workflows/review.js","globalConcurrencyLimit":2}`,
			`{"workflowScriptPath":"workflows/review.js","args":{"task":"review"}}`,
		}
		for _, s := range same {
			wantOK(t, s, s)
		}
		task := "Use `quotes`\nand newlines"
		in := newObject()
		in.set("agent", " worker ")
		in.set("task", task)
		in.set("context", "fresh")
		in.set("async", false)
		r := normalizePublicSubagentExecution(map[string]any{"agent": " worker ", "task": task, "context": "fresh", "async": false})
		equal(t, r.OK, true, "single")
		equal(t, canonicalJSON(js(t, marshalJSON(mapToObject(r.Params), ""))), canonicalJSON(js(t, `{"agent":"worker","task":`+marshalJSON(task, "")+`,"context":"fresh","async":false,"output":true}`)), "single params")
		wantOK(t, `{"agent":"worker"}`, `{"agent":"worker","output":true}`)
		wantOK(t, `{"agent":"worker","async":true,"baseRef":"@/foo"}`, `{"agent":"worker","async":true,"baseRef":"@/foo","output":true}`)
		wantOK(t, `{"agent":"worker","output":false}`, `{"agent":"worker","output":false}`)
		wantOK(t, `{"agent":"worker","isolation":"none"}`, `{"agent":"worker","worktree":false,"output":true}`)
		wantOK(t, `{"action":" list "}`, `{"action":"list"}`)
		wantOK(t, `{"action":" list ","capabilities":true}`, `{"action":"list","capabilities":true}`)
		wantOK(t, `{"action":" validate ","workflowScript":"return args.task","args":{"task":"review"}}`, `{"action":"validate","workflowScript":"return args.task","args":{"task":"review"}}`)
		wantOK(t, `{"action":" validate ","workflowScriptPath":"workflow.js"}`, `{"action":"validate","workflowScriptPath":"workflow.js"}`)
		wantOK(t, `{"action":" validate ","workflowScript":"return 1","maxSubagentSpawnsPerRun":5}`, `{"action":"validate","workflowScript":"return 1","maxSubagentSpawnsPerRun":5}`)
		wantOK(t, `{"action":" schedule.create ","every":"1h","workflowScript":"return args.task","args":{"task":"review"}}`, `{"action":"schedule.create","every":"1h","workflowScript":"return args.task","args":{"task":"review"}}`)
		wantOK(t, `{"action":" schedule.create ","every":"1h","workflowScriptPath":"/tmp/workflow.js"}`, `{"action":"schedule.create","every":"1h","workflowScriptPath":"/tmp/workflow.js"}`)
	})
	tw(t, "public-execution", "rejects unsafe base refs at the public boundary", func(t *testing.T) {
		for _, ref := range []string{`"refs/heads/unsafe..ref"`, `"branch name"`, `"HEAD^{tree}"`, `"@"`, `"` + strings.Repeat("a", 40) + `"`, `"` + strings.Repeat("a", 64) + `"`, `42`} {
			r := normalizePublic(t, `{"agent":"worker","baseRef":`+ref+`}`)
			equal(t, r.OK, false, ref)
			polMatches(t, r.Error, `baseRef.*HEAD.*named ref.*40/64-character commit IDs.*revision expressions.*unsupported`)
		}
	})
	tw(t, "public-execution", "rejects base refs on management actions that do not consume them", func(t *testing.T) {
		r := normalizePublic(t, `{"action":"list","baseRef":"refs/heads/release"}`)
		equal(t, r.OK, false, "ok")
		polMatches(t, r.Error, `baseRef is only supported`)
	})
	tw(t, "public-execution", "accepts base refs on resume because resume consumes them", func(t *testing.T) {
		wantOK(t, `{"action":" resume ","id":"run-1","message":"Continue","baseRef":"refs/heads/release"}`, `{"action":"resume","id":"run-1","message":"Continue","baseRef":"refs/heads/release"}`)
	})
	tw(t, "public-execution", "rejects workflowScript with workflowScriptPath", func(t *testing.T) {
		r := normalizePublic(t, `{"workflowScript":"return 1","workflowScriptPath":"workflow.js"}`)
		equal(t, r.OK, false, "ok")
		polMatches(t, r.Error, `mutually exclusive`)
	})
	tw(t, "public-execution", "rejects named workflow combinations and caller-controlled provenance fields", func(t *testing.T) {
		for _, p := range []string{
			`{"workflow":"review","workflowScript":"return 1"}`, `{"workflow":"review","workflowScriptPath":"workflow.js"}`, `{"workflow":"review","agent":"worker"}`,
			`{"workflow":"review","task":"work"}`, `{"args":{"task":"work"}}`, `{"workflow":"review","resource":{"kind":"workflow"}}`,
			`{"workflow":"review","resourceProvenance":{"kind":"workflow"}}`, `{"workflow":"review","workflowResourcePermit":{}}`,
		} {
			equal(t, normalizePublic(t, p).OK, false, p)
		}
	})
	tw(t, "public-execution", "rejects bare arguments and arguments on direct child launches", func(t *testing.T) {
		for _, p := range []string{`{"args":{}}`, `{"agent":"worker","task":"work","args":{}}`} {
			r := normalizePublic(t, p)
			equal(t, r.OK, false, p)
			polMatches(t, r.Error, `args requires workflow`)
		}
	})
	tw(t, "public-execution", "keeps raw workflow inputs untrusted and rejects invalid named-resource arguments", func(t *testing.T) {
		raw := normalizePublic(t, `{"workflowScript":"return await runs.host(\"ci\", { kind: \"command\", command: \"npm test\", timeoutMs: 1000 });"}`)
		equal(t, raw.OK, true, "raw")
		_, has := raw.Params["resource"]
		equal(t, has, false, "resource")
		for _, p := range []string{`{"args":{"task":"work"}}`, `{"workflow":"","args":{}}`, `{"workflow":42,"args":{}}`} {
			equal(t, normalizePublic(t, p).OK, false, p)
		}
	})
	tw(t, "public-execution", "rejects preflight without a workflow input", func(t *testing.T) {
		r := normalizePublic(t, `{"agent":"worker","preflight":{"version":1,"lanes":[]}}`)
		equal(t, r.OK, false, "ok")
		polMatches(t, r.Error, `preflight requires workflowScript or workflowScriptPath`)
	})
	tw(t, "public-execution", "rejects private run fan-out fields at the public boundary", func(t *testing.T) {
		for _, p := range []string{`{"workflowScript":"return 1","runFanoutBudget":{"version":1}}`, `{"workflowScript":"return 1","runFanoutAdmitted":true}`} {
			r := normalizePublic(t, p)
			equal(t, r.OK, false, p)
			polMatches(t, r.Error, `does not accept internal run fan-out fields`)
		}
	})
	tw(t, "public-execution", "rejects private workflow child fields at the public boundary", func(t *testing.T) {
		for _, p := range []string{
			`{"agent":"worker","workflowParentRunId":"workflow"}`, `{"agent":"worker","workflowKey":"child"}`, `{"agent":"worker","workflowChildAsyncId":"child"}`,
			`{"agent":"worker","workflowAwaitAsync":true}`, `{"agent":"worker","workflowAwaitDetached":true}`, `{"agent":"worker","workflowParentDeadlineAt":1700000000000}`,
			`{"agent":"worker","suppressRoutineResultIntercom":true}`,
		} {
			r := normalizePublic(t, p)
			equal(t, r.OK, false, p)
			polMatches(t, r.Error, `internal workflow child fields`)
		}
	})
	tw(t, "public-execution", "rejects mixed, invalid, and removed public execution shapes", func(t *testing.T) {
		for _, p := range []string{
			`{"action":" "}`, `{"action":"single"}`, `{"action":"parallel"}`, `{"action":"chain"}`,
			`{"action":"append-step","id":"run","step":{"agent":"worker"}}`, `{"action":"approve-checkpoint","id":"run"}`, `{"action":"reject-checkpoint","id":"run"}`,
			`{"agent":""}`, `{"agent":42}`, `{"task":"work"}`, `{"agent":"worker","task":42}`, `{"agent":"worker","workflowScript":"return 1"}`,
			`{"action":"status","task":"work"}`, `{"tasks":[{"agent":"worker"}]}`, `{"chain":[{"agent":"worker"}]}`, `{"parallel":[{"agent":"worker"}]}`, `{"concurrency":2}`,
			`{"action":"get","chainName":"review-pipeline"}`, `{"action":"create","config":{"name":"review-pipeline","steps":[{"agent":"worker"}]}}`,
			`{"clarify":true,"workflowScript":"return 1"}`, `{"resume":"retained-run","workflowScript":"return 1"}`, `{}`, `{"workflowScript":" "}`, `{"workflowScriptPath":" "}`,
			`{"action":"status","workflowScript":"return 1"}`, `{"action":"schedule.create","every":"1h","agent":"worker","workflowScript":"return 1"}`,
			`{"workflowScript":"return 1","isolation":"invalid"}`, `{"workflowScript":"return 1","isolation":"none","worktree":true}`, `{"workflowScript":"return 1","isolation":"worktree","worktree":false}`,
			`{"agent":"worker","globalConcurrencyLimit":2}`, `{"workflow":"review","maxSubagentSpawnsPerRun":2}`, `{"action":"validate","workflowScript":"return 1","globalConcurrencyLimit":2}`,
			`{"workflowScript":"return 1","globalConcurrencyLimit":0}`, `{"workflowScript":"return 1","maxSubagentSpawnsPerRun":1.5}`, `{"workflowScript":"return 1","maxSubagentSpawnsPerRun":9007199254740992}`,
		} {
			equal(t, normalizePublic(t, p).OK, false, p)
		}
	})
}

func mapToObject(m map[string]any) *jsObject {
	o, _ := asObject(m)
	return o
}
