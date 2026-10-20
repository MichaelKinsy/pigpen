package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScriptedWorkflowTwinsRuns(t *testing.T) {
	tw(t, scriptedFile, "exposes validated state only when a mission state adapter is present", func(t *testing.T) {
		values := map[string]any{}
		var mu sync.Mutex
		o := wfOptions("if (typeof state !== \"object\") throw new Error(\"state missing\");\nawait state.set(\"review.stage\", { count: 2 });\nreturn await state.get(\"review.stage\");")
		o.State = &WorkflowState{
			Get: func(key string) *WorkflowFuture[any] {
				mu.Lock()
				defer mu.Unlock()
				if v, ok := values[key]; ok {
					return WorkflowResolved(v)
				}
				return WorkflowResolved[any](undef{})
			},
			Set: func(key string, v any) *WorkflowFuture[struct{}] {
				mu.Lock()
				defer mu.Unlock()
				values[key] = v
				return WorkflowResolved(struct{}{})
			},
		}
		jsonEq(t, wfRun(t, o).Value, `{"count":2}`)

		if v := wfRun(t, wfOptions(`return typeof state;`)).Value; v != "undefined" {
			t.Fatalf("typeof state = %v", v)
		}
		for _, script := range []string{`return state.get("bad key");`, `return state.set("valid", undefined);`} {
			o := wfOptions(script)
			o.State = &WorkflowState{
				Get: func(string) *WorkflowFuture[any] { return WorkflowResolved[any](undef{}) },
				Set: func(string, any) *WorkflowFuture[struct{}] { return WorkflowResolved(struct{}{}) },
			}
			if we := wfFail(t, o); !strings.Contains(we.Message, "state") {
				t.Fatalf("%s: %q", script, we.Message)
			}
		}
	})

	tw(t, scriptedFile, "runs keyed children, streams progress, and exposes no host capabilities", func(t *testing.T) {
		var mu sync.Mutex
		var launches []string
		asyncParam := false
		var traceSnapshots, emitSnapshots []int
		o := wfOptions(strings.Join([]string{
			`if (typeof process !== "undefined" || typeof require !== "undefined") throw new Error("host globals leaked");`,
			`const scan = await runs.run("scan", { agent: "scout", task: "find targets" });`,
			`const reviews = await runs.all(scan.structuredOutput.items.map((item) => ({ key: "review-" + item, agent: "reviewer", task: item })));`,
			`emit({ count: reviews.length });`,
			`console.log("reviewed", reviews.length);`,
			`return { refs: runs.refs(reviews) };`,
		}, "\n"))
		o.TimeoutMs = new(2000.0)
		o.OnTrace = func(tr []WorkflowScriptTraceEntry) { traceSnapshots = append(traceSnapshots, len(tr)) }
		o.OnEmit = func(e []any) error { emitSnapshots = append(emitSnapshots, len(e)); return nil }
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			mu.Lock()
			launches = append(launches, key)
			if _, has := params.vals["async"]; has {
				asyncParam = true
			}
			mu.Unlock()
			if key == "scan" {
				return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-scan", "output", "targets", "structuredOutput", jo("items", []any{"a", "b"}), "artifactPaths", []any{"/tmp/scan.json"}, "results", []any{}))
			}
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-"+key+"-complete", "output", "reviewed "+objStr(params, "task"), "artifactPaths", []any{"/tmp/" + key + ".md"}, "results", []any{}))
		}
		res := wfRun(t, o)
		if strings.Join(launches, ",") != "scan,review-a,review-b" || asyncParam {
			t.Fatalf("launches = %v async=%v", launches, asyncParam)
		}
		jsonEq(t, res.Emits, `[{"count":2}]`)
		jsonEq(t, res.Console, `[{"level":"log","text":"reviewed 2"}]`)
		out := marshalJSON(res.Value, "")
		if !strings.Contains(out, "[run review-a; id=run-revi]") || strings.Contains(out, "artifacts=") {
			t.Fatalf("value = %s", out)
		}
		completed := 0
		for _, e := range res.Trace {
			if e.State == "completed" {
				completed++
			}
		}
		if completed != 3 || len(traceSnapshots) < 6 || fmt.Sprint(emitSnapshots) != "[1]" {
			t.Fatalf("completed=%d traces=%d emits=%v", completed, len(traceSnapshots), emitSnapshots)
		}
	})

	tw(t, scriptedFile, "passes a per-run intercom bridge override to the host launch", func(t *testing.T) {
		var got *jsObject
		o := wfOptions(`return runs.run("isolated", { agent: "worker", task: "Run", intercomBridge: { mode: "off" } });`)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			got = params
			return WorkflowResolved(okChild(key, "done"))
		}
		wfRun(t, o)
		jsonEq(t, got.vals["intercomBridge"], `{"mode":"off"}`)
	})

	tw(t, scriptedFile, "resolves a keyed workflow receipt despite an invalid current worktree source", func(t *testing.T) {
		var launchParams *jsObject
		var resolved WorkflowResumeRef
		o := wfOptions(`return runs.run("cross-review", { resume: { workflowRunId: "workflow-1", key: "advisor", latest: true }, task: "Continue" });`)
		// preflightWorkflowWorktrees skips a call with `resume` (subagent-executor.ts:4893): a retained child is never admitted
		// against the current worktree source, which here is not a git repository at all.
		o.Admit = func(ctx context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}] {
			for _, c := range calls {
				if _, has := c.Params.vals["resume"]; !has {
					return WorkflowRejected[struct{}](errors.New("Worktree admission failed: not a git repository"))
				}
			}
			return WorkflowResolved(struct{}{})
		}
		o.ResolveResume = func(ctx context.Context, ref WorkflowResumeRef, index *int) *WorkflowFuture[WorkflowResolvedResume] {
			resolved = ref
			return WorkflowResolved(WorkflowResolvedResume{RunID: "retained-run", RunIDs: []string{"ancestor-run", "retained-run"}, Object: true})
		}
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launchParams = params
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "continued-run", "output", "done", "artifactPaths", []any{}))
		}
		res := wfRun(t, o)
		if resolved != (WorkflowResumeRef{WorkflowRunID: "workflow-1", Key: "advisor"}) {
			t.Fatalf("resolved = %+v", resolved)
		}
		jsonEq(t, launchParams, `{"resume":"retained-run","task":"Continue"}`)
		v := res.Value.(*jsObject)
		if v.vals["runId"] != "continued-run" {
			t.Fatalf("value = %v", marshalJSON(v, ""))
		}
		jsonEq(t, v.vals["continuation"], `{"runIds":["ancestor-run","retained-run","continued-run"]}`)
	})

	tw(t, scriptedFile, "fails closed for invalid or unavailable keyed workflow receipt resume", func(t *testing.T) {
		for _, resume := range []string{
			`{ workflowRunId: "workflow-1", key: "advisor", latest: false }`,
			`{ workflowRunId: "workflow-1", key: "bad key", latest: true }`,
			`{ workflowRunId: "workflow-1", key: "advisor", latest: true, extra: true }`,
		} {
			o := wfOptions(fmt.Sprintf(`return runs.run("cross-review", { resume: %s, task: "Continue" });`, resume))
			o.Admit = func(ctx context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}] {
				t.Errorf("Invalid receipt reached admission")
				return WorkflowResolved(struct{}{})
			}
			if we := wfFail(t, o); !strings.Contains(we.Message, "keyed resume") {
				t.Fatalf("%s: %q", resume, we.Message)
			}
		}
		o := wfOptions(`return runs.run("cross-review", { resume: { workflowRunId: "workflow-1", key: "advisor", latest: true }, task: "Continue" });`)
		if we := wfFail(t, o); !strings.Contains(we.Message, "unavailable in this host") {
			t.Fatalf("message = %q", we.Message)
		}
	})

	tw(t, scriptedFile, "waits for every runs.all child and returns ordinary failures in input order", func(t *testing.T) {
		var delayedFinished, delayedAborted atomic.Bool
		o := wfOptions(strings.Join([]string{
			"const children = await runs.all([",
			`	{ key: "fails-first", agent: "worker", task: "fail" },`,
			`	{ key: "finishes-later", agent: "worker", task: "finish" }`,
			"]);",
			"return children.map(({ key, ok, error, results }) => error === undefined ? { key, ok, results } : { key, ok, error, results });",
		}, "\n"))
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			if key == "fails-first" {
				return WorkflowResolved(jo("key", key, "ok", false, "output", "acceptance rejected", "artifactPaths", []any{}, "results", []any{jo("acceptance", jo("status", "rejected"))}))
			}
			return WorkflowAsync(func() (*jsObject, error) {
				select {
				case <-time.After(50 * time.Millisecond):
					delayedFinished.Store(true)
					return jo("key", key, "ok", true, "output", "completed", "artifactPaths", []any{}, "results", []any{}), nil
				case <-ctx.Done():
					delayedAborted.Store(!delayedFinished.Load())
					return nil, context.Cause(ctx)
				}
			})
		}
		res := wfRun(t, o)
		if !delayedFinished.Load() || delayedAborted.Load() {
			t.Fatalf("finished=%v aborted=%v", delayedFinished.Load(), delayedAborted.Load())
		}
		jsonEq(t, res.Value, `[{"key":"fails-first","ok":false,"error":"acceptance rejected","results":[{"acceptance":{"status":"rejected"}}]},{"key":"finishes-later","ok":true,"results":[]}]`)
		var got []string
		for _, e := range res.Trace {
			if e.Operation == "run" && e.State != "started" {
				got = append(got, e.Key+":"+e.State)
			}
		}
		if strings.Join(got, ",") != "fails-first:failed,finishes-later:completed" {
			t.Fatalf("trace = %v", got)
		}
	})

	tw(t, scriptedFile, "caps concurrent workflow child launches across runs.all", func(t *testing.T) {
		var active, maxActive atomic.Int32
		o := wfOptions(`return await runs.all([
				{ key: "one", agent: "worker", task: "one" },
				{ key: "two", agent: "worker", task: "two" },
				{ key: "three", agent: "worker", task: "three" },
				{ key: "four", agent: "worker", task: "four" }
			]);`)
		o.GlobalConcurrencyLimit = new(2.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			n := active.Add(1)
			for {
				m := maxActive.Load()
				if n <= m || maxActive.CompareAndSwap(m, n) {
					break
				}
			}
			return WorkflowAsync(func() (*jsObject, error) {
				time.Sleep(20 * time.Millisecond)
				active.Add(-1)
				return okChild(key, key), nil
			})
		}
		res := wfRun(t, o)
		if maxActive.Load() != 2 {
			t.Fatalf("maxActive = %d", maxActive.Load())
		}
		var keys []string
		for _, c := range res.Value.([]any) {
			keys = append(keys, objStr(c.(*jsObject), "key"))
		}
		if strings.Join(keys, ",") != "one,two,three,four" {
			t.Fatalf("keys = %v", keys)
		}
	})

	tw(t, scriptedFile, "runs parallel lane starts and sequential stages with retained resume", func(t *testing.T) {
		var mu sync.Mutex
		var launches []string
		params := map[string]*jsObject{}
		var active, maxActive atomic.Int32
		var betaWriterDone, alphaChallengeBeforeBeta atomic.Bool
		o := wfOptions(`return await runs.lanes([
				{ key: "alpha", stages: [
					{ key: "writer", agent: "worker", task: "alpha write" },
					{ key: "challenge", resume: "previous", task: "alpha challenge" }
				] },
				{ key: "beta", stages: [
					{ key: "writer", agent: "worker", task: "beta write" },
					{ key: "review", agent: "reviewer", task: "beta review" }
				] }
			]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, p *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			mu.Lock()
			launches = append(launches, key)
			params[key] = p
			mu.Unlock()
			n := active.Add(1)
			for {
				m := maxActive.Load()
				if n <= m || maxActive.CompareAndSwap(m, n) {
					break
				}
			}
			return WorkflowAsync(func() (*jsObject, error) {
				d := 5 * time.Millisecond
				if key == "beta.writer" {
					d = 50 * time.Millisecond
				}
				time.Sleep(d)
				if key == "beta.writer" {
					betaWriterDone.Store(true)
				}
				if key == "alpha.challenge" {
					alphaChallengeBeforeBeta.Store(!betaWriterDone.Load())
				}
				active.Add(-1)
				return jo("key", key, "ok", true, "runId", "run-"+key, "output", key+" output", "outputReference", "/tmp/"+key+".md", "artifactPaths", []any{}, "results", []any{}), nil
			})
		}
		res := wfRun(t, o)
		if maxActive.Load() != 2 {
			t.Errorf("first stages should be admitted together: maxActive = %d", maxActive.Load())
		}
		if !alphaChallengeBeforeBeta.Load() {
			t.Errorf("a settled lane should advance without waiting for slower siblings")
		}
		if strings.Join(launches, ",") != "alpha.writer,beta.writer,alpha.challenge,beta.review" {
			t.Fatalf("launches = %v", launches)
		}
		jsonEq(t, params["alpha.challenge"], `{"resume":"run-alpha.writer","task":"alpha challenge"}`)
		for _, lane := range []string{"alpha", "beta"} {
			n := 0
			for _, e := range res.Trace {
				if e.Operation == "run" && strings.HasPrefix(e.Key, lane+".") {
					n++
					if e.GeneratedLaneKey != lane {
						t.Errorf("trace entry %+v lacks lane key %s", e, lane)
					}
				}
			}
			if n == 0 {
				t.Errorf("no trace for %s", lane)
			}
		}
		jsonEq(t, res.Value, `[
			{"key":"alpha","state":"complete","stages":[
				{"key":"writer","runId":"run-alpha.writer","ok":true,"state":"completed","outputReference":"/tmp/alpha.writer.md"},
				{"key":"challenge","runId":"run-alpha.challenge","ok":true,"state":"completed","outputReference":"/tmp/alpha.challenge.md"}]},
			{"key":"beta","state":"complete","stages":[
				{"key":"writer","runId":"run-beta.writer","ok":true,"state":"completed","outputReference":"/tmp/beta.writer.md"},
				{"key":"review","runId":"run-beta.review","ok":true,"state":"completed","outputReference":"/tmp/beta.review.md"}]}]`)
	})

	tw(t, scriptedFile, "marks only runs.lanes stages with generated lane provenance", func(t *testing.T) {
		o := wfOptions(`const direct = await runs.run("audit.shadow", { agent: "worker", task: "direct" }); const lanes = await runs.lanes([{ key: "audit", stages: [{ key: "writer", agent: "worker", task: "generated" }] }]); return { direct: direct.output, lanes };`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-"+key, "output", key, "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		direct, generated := 0, 0
		for _, e := range res.Trace {
			if e.Operation != "run" {
				continue
			}
			switch e.Key {
			case "audit.shadow":
				direct++
				if e.GeneratedLaneKey != "" {
					t.Errorf("direct run marked as generated: %+v", e)
				}
			case "audit.writer":
				generated++
				if e.GeneratedLaneKey != "audit" {
					t.Errorf("generated run lacks provenance: %+v", e)
				}
			}
		}
		if direct == 0 || generated == 0 {
			t.Fatalf("direct=%d generated=%d", direct, generated)
		}
	})

	tw(t, scriptedFile, "keeps a rejected first-stage result local to its lane", func(t *testing.T) {
		var mu sync.Mutex
		var launches []string
		o := wfOptions(`return await runs.lanes([
				{ key: "broken", stages: [
					{ key: "writer", agent: "worker", task: "boundary failure" },
					{ key: "review", agent: "reviewer", task: "must be skipped" }
				] },
				{ key: "healthy", stages: [
					{ key: "writer", agent: "worker", task: "continue" },
					{ key: "review", agent: "reviewer", task: "continue review" }
				] }
			]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			mu.Lock()
			launches = append(launches, key)
			mu.Unlock()
			if key == "broken.writer" {
				so := newObject()
				so.set("self", so)
				return WorkflowResolved(jo("key", key, "ok", true, "runId", "broken-run", "output", "not persisted", "structuredOutput", so, "artifactPaths", []any{}, "results", []any{}))
			}
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-"+key, "output", "done", "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		if strings.Join(launches, ",") != "broken.writer,healthy.writer,healthy.review" {
			t.Fatalf("launches = %v", launches)
		}
		board := res.Value.([]any)
		if len(board) != 2 {
			t.Fatalf("board = %s", marshalJSON(board, ""))
		}
		first := board[0].(*jsObject)
		writer := first.vals["stages"].([]any)[0].(*jsObject)
		errText, _ := writer.vals["error"].(string)
		if !strings.Contains(errText, "must contain only JSON data") {
			t.Fatalf("error = %q", errText)
		}
		jsonEq(t, first, fmt.Sprintf(`{"key":"broken","state":"blocked","failedStage":"writer","stages":[{"key":"writer","ok":false,"state":"failed","error":%s},{"key":"review","state":"skipped"}]}`, marshalJSON(errText, "")))
		jsonEq(t, board[1], `{"key":"healthy","state":"complete","stages":[{"key":"writer","runId":"run-healthy.writer","ok":true,"state":"completed"},{"key":"review","runId":"run-healthy.review","ok":true,"state":"completed"}]}`)
		for _, c := range res.Children {
			switch objStr(c, "key") {
			case "broken.writer":
				if objBool(c, "ok") {
					t.Errorf("broken.writer must be failed")
				}
				if _, has := c.vals["structuredOutput"]; has {
					t.Errorf("broken.writer kept its structuredOutput")
				}
			case "healthy.writer":
				if !objBool(c, "ok") {
					t.Errorf("healthy.writer must be ok")
				}
			}
		}
	})

	tw(t, scriptedFile, "blocks one lane on an explicit structured verdict while siblings continue", func(t *testing.T) {
		var mu sync.Mutex
		var launches []string
		o := wfOptions(`return await runs.lanes([
				{ key: "blocked", stages: [
					{ key: "review", agent: "reviewer", task: "block this lane" },
					{ key: "followup", agent: "worker", task: "must not run" }
				] },
				{ key: "healthy", stages: [
					{ key: "review", agent: "reviewer", task: "continue this lane" },
					{ key: "followup", agent: "worker", task: "continue followup" }
				] }
			]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			mu.Lock()
			launches = append(launches, key)
			mu.Unlock()
			verdict := "ready"
			if key == "blocked.review" {
				verdict = "blocked"
			}
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-"+key, "output", "done", "structuredOutput", jo("verdict", verdict), "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		if strings.Join(launches, ",") != "blocked.review,healthy.review,healthy.followup" {
			t.Fatalf("launches = %v", launches)
		}
		jsonEq(t, res.Value, `[
			{"key":"blocked","state":"blocked","failedStage":"review","stages":[
				{"key":"review","runId":"run-blocked.review","ok":true,"state":"blocked","verdict":"blocked","error":"Stage returned a blocked verdict."},
				{"key":"followup","state":"skipped"}]},
			{"key":"healthy","state":"complete","stages":[
				{"key":"review","runId":"run-healthy.review","ok":true,"state":"completed","verdict":"ready"},
				{"key":"followup","runId":"run-healthy.followup","ok":true,"state":"completed","verdict":"ready"}]}]`)
	})

	tw(t, scriptedFile, "settles a failed lane without aborting later stages in sibling lanes", func(t *testing.T) {
		var mu sync.Mutex
		var launches []string
		o := wfOptions(`return await runs.lanes([
				{ key: "fails", stages: [
					{ key: "writer", agent: "worker", task: "write" },
					{ key: "challenge", agent: "worker", task: "fail challenge" },
					{ key: "final", agent: "worker", task: "must be skipped" }
				] },
				{ key: "passes", stages: [
					{ key: "writer", agent: "worker", task: "write" },
					{ key: "challenge", agent: "worker", task: "pass challenge" }
				] }
			]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			mu.Lock()
			launches = append(launches, key)
			mu.Unlock()
			if key == "fails.challenge" {
				return WorkflowResolved(jo("key", key, "ok", false, "runId", "failed-run", "output", "challenge failed", "error", "challenge failed", "artifactPaths", []any{}, "results", []any{}))
			}
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-"+key, "output", "done", "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		if strings.Join(launches, ",") != "fails.writer,passes.writer,fails.challenge,passes.challenge" {
			t.Fatalf("launches = %v", launches)
		}
		jsonEq(t, res.Value, `[
			{"key":"fails","state":"blocked","failedStage":"challenge","stages":[
				{"key":"writer","runId":"run-fails.writer","ok":true,"state":"completed"},
				{"key":"challenge","runId":"failed-run","ok":false,"state":"failed","error":"challenge failed"},
				{"key":"final","state":"skipped"}]},
			{"key":"passes","state":"complete","stages":[
				{"key":"writer","runId":"run-passes.writer","ok":true,"state":"completed"},
				{"key":"challenge","runId":"run-passes.challenge","ok":true,"state":"completed"}]}]`)
	})

	tw(t, scriptedFile, "gives actionable guidance for a retained stage-0 resume", func(t *testing.T) {
		launches := 0
		o := wfOptions(`return runs.lanes([{ key: "lane", stages: [{ key: "writer", resume: "retained-run", task: "continue" }] }]);`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launches++
			return WorkflowResolved(okChild(key, "unexpected"))
		}
		we := wfFail(t, o)
		for _, want := range []string{"runs.lanes lane 0 stage 0 cannot resume a retained run id in runs.lanes", "use runs.run(key, { resume: id }) outside lanes", `start the lane with an agent stage and use resume: "previous" later`} {
			if !strings.Contains(we.Message, want) {
				t.Errorf("message lacks %q: %s", want, we.Message)
			}
		}
		if launches != 0 {
			t.Fatalf("launches = %d", launches)
		}
	})

	tw(t, scriptedFile, "validates all lane stages before launching any child", func(t *testing.T) {
		for _, script := range []string{
			`return runs.lanes([{ key: "bad lane", stages: [{ key: "writer", agent: "worker", task: "write" }] }]);`,
			`return runs.lanes([{ key: "lane", stages: [{ key: "writer", agent: "worker", task: "write" }, { key: "writer", agent: "worker", task: "again" }] }]);`,
			`return runs.lanes([{ key: "lane", stages: [{ key: "writer", resume: "previous", task: "cannot start" }] }]);`,
			`return runs.lanes([{ key: "lane", stages: [{ key: "writer", agent: "worker", task: "write" }, { key: "review", resume: "raw-run-id", task: "invalid" }] }]);`,
		} {
			launches := 0
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
				launches++
				return WorkflowResolved(okChild(key, "unexpected"))
			}
			if we := wfFail(t, o); !strings.Contains(we.Message, "runs.lanes") {
				t.Fatalf("%s: %q", script, we.Message)
			}
			if launches != 0 {
				t.Fatalf("%s: launches = %d", script, launches)
			}
		}
	})

	tw(t, scriptedFile, "rejects an invalid workflow concurrency limit before launching", func(t *testing.T) {
		launches := 0
		o := wfOptions(`return runs.run("one", { agent: "worker", task: "one" });`)
		o.GlobalConcurrencyLimit = new(0.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launches++
			return WorkflowResolved(okChild(key, key))
		}
		if we := wfFail(t, o); !strings.Contains(we.Message, "workflow script global concurrency limit must be a positive integer") {
			t.Fatalf("message = %q", we.Message)
		}
		if launches != 0 {
			t.Fatalf("launches = %d", launches)
		}
	})
}
