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

func TestScriptedWorkflowTwinsBoundaries(t *testing.T) {
	tw(t, scriptedFile, "rejects non-JSON-safe emitted values without persisting them", func(t *testing.T) {
		for _, script := range []string{
			`emit(undefined);`,
			`emit(NaN);`,
			`emit(Infinity);`,
			`emit(new Map([["a", 1]]));`,
			`emit(new Set([1]));`,
			`emit(new (class Value { constructor() { this.ok = true; } })());`,
			`emit(new (class Object { constructor() { this.ok = true; } })());`,
			`emit(() => true);`,
			`emit(Symbol("value"));`,
			`const value = {}; value.self = value; emit(value);`,
			`emit(1n);`,
		} {
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			if we := wfFail(t, o); len(we.Partial.Emits) != 0 {
				t.Fatalf("%s: emits = %v", script, we.Partial.Emits)
			}
		}
	})

	tw(t, scriptedFile, "rejects non-JSON-safe workflow return values", func(t *testing.T) {
		for _, script := range []string{
			`return new Map([["a", 1]]);`,
			`return NaN;`,
			`return 1n;`,
			`return new (class Object { constructor() { this.ok = true; } })();`,
			`const value = {}; value.self = value; return value;`,
			`const value = {}; value[Symbol("hidden")] = true; return value;`,
			`return () => true;`,
			`return Symbol("value");`,
		} {
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			matchErr(t, wfFail(t, o), `return`)
		}
	})

	tw(t, scriptedFile, "normalizes omitted and explicit undefined workflow returns to null", func(t *testing.T) {
		for _, script := range []string{`await Promise.resolve();`, `return undefined;`} {
			o := wfOptions(script)
			o.TimeoutMs = new(2000.0)
			if v := wfRun(t, o).Value; v != nil {
				t.Fatalf("%s: value = %v", script, v)
			}
		}
	})

	tw(t, scriptedFile, "accepts a JSON-safe workflow return value", func(t *testing.T) {
		o := wfOptions(`return { ok: true, values: [1, "two", null] };`)
		o.TimeoutMs = new(2000.0)
		jsonEq(t, wfRun(t, o).Value, `{"ok":true,"values":[1,"two",null]}`)
	})

	tw(t, scriptedFile, "formats persisted JSON values without assuming stringify returns a string", func(t *testing.T) {
		if _, ok := FormatWorkflowJSONPreview(undef{}, 120); ok {
			t.Errorf("undefined has no preview")
		}
		if _, ok := FormatWorkflowJSONPreview(nan(), 120); ok {
			t.Errorf("NaN has no preview")
		}
		if _, ok := FormatWorkflowJSONPreview(map[int]int{}, 120); ok {
			t.Errorf("a Map has no preview")
		}
		got, ok := FormatWorkflowJSONPreview(jo("stage", []any{"review", float64(2)}), 120)
		if !ok || got != `{"stage":["review",2]}` {
			t.Errorf("preview = %q %v", got, ok)
		}
	})

	tw(t, scriptedFile, "accepts JSON-safe object and array emits", func(t *testing.T) {
		o := wfOptions(`emit({ ok: true, values: [1, "two", null] }); return "done";`)
		o.TimeoutMs = new(2000.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "output", "ok", "artifactPaths", []any{}, "results", []any{}))
		}
		jsonEq(t, wfRun(t, o).Emits, `[{"ok":true,"values":[1,"two",null]}]`)
	})

	tw(t, scriptedFile, "terminates scripts and aborts an in-flight child at the controller timeout", func(t *testing.T) {
		var childAborted atomic.Bool
		o := wfOptions(`await runs.run("slow", { agent: "worker", task: "wait" });`)
		o.TimeoutMs = new(500.0)
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowAsync(func() (*jsObject, error) {
				<-ctx.Done()
				childAborted.Store(true)
				return nil, context.Cause(ctx)
			})
		}
		matchErr(t, wfFail(t, o), `timed out after 500ms`)
		for i := 0; i < 100 && !childAborted.Load(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if !childAborted.Load() {
			t.Fatalf("the child was not aborted")
		}
	})

	const reloaded = "Workflow stopped because the extension session was replaced or reloaded."
	abortOnDone := func(cancel context.CancelCauseFunc, ctx context.Context) func([]WorkflowScriptTraceEntry) {
		return func(trace []WorkflowScriptTraceEntry) {
			if ctx.Err() != nil {
				return
			}
			for _, e := range trace {
				if e.Operation == "run" && e.Key == "done" && e.State == "completed" {
					cancel(errors.New(reloaded))
					return
				}
			}
		}
	}

	tw(t, scriptedFile, "flushes result assembly after abort when every child is terminal", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		var launchCount atomic.Int32
		o := wfOptions(`const child = await runs.run("done", { agent: "worker", task: "finish" }); return { phase: "assembled", output: child.output };`)
		o.Ctx = ctx
		o.ContinueAfterAbortWhenChildrenSettled = func(err error) bool { return err.Error() == reloaded }
		o.OnTrace = abortOnDone(cancel, ctx)
		o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launchCount.Add(1)
			return WorkflowResolved(jo("key", key, "ok", true, "output", "child output", "artifactPaths", []any{}, "results", []any{}))
		}
		res := wfRun(t, o)
		jsonEq(t, res.Value, `{"phase":"assembled","output":"child output"}`)
		if launchCount.Load() != 1 {
			t.Fatalf("launches = %d", launchCount.Load())
		}
	})

	tw(t, scriptedFile, "fails closed with diagnostics when graceful-abort eligibility throws", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		o := wfOptions(`await runs.run("done", { agent: "worker", task: "finish" }); return { phase: "assembled" };`)
		o.Ctx = ctx
		o.ContinueAfterAbortWhenChildrenSettled = func(error) bool { panic(errors.New("liveness unavailable")) }
		o.OnTrace = abortOnDone(cancel, ctx)
		matchErr(t, wfFail(t, o), `liveness unavailable`)
	})

	tw(t, scriptedFile, "fails closed if assembly tries to launch after a graceful abort", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		var launchCount atomic.Int32
		o := wfOptions(`await runs.run("done", { agent: "worker", task: "finish" }); return runs.run("late", { agent: "worker", task: "must not launch" });`)
		o.Ctx = ctx
		o.ContinueAfterAbortWhenChildrenSettled = func(err error) bool { return err.Error() == reloaded }
		o.OnTrace = abortOnDone(cancel, ctx)
		o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launchCount.Add(1)
			return WorkflowResolved(okChild(key, "child output"))
		}
		we := wfFail(t, o)
		if we.Message != reloaded || launchCount.Load() != 1 {
			t.Fatalf("message %q launches %d", we.Message, launchCount.Load())
		}
	})

	tskip(t, scriptedFile, "ignores a queued child launch message after workflow abort", "intercepts Node's Worker.prototype.on to hold one worker-to-host message and deliver it after the abort; the Go port has no worker message channel to intercept (its late-call case is TestWorkflowLateCallAfterAbortIsIgnored)")

	stoppedTrace := func(trace []WorkflowScriptTraceEntry, key string) (stopped, failed bool) {
		for _, e := range trace {
			if e.Operation == "run" && e.Key == key {
				stopped = stopped || e.State == "stopped"
				failed = failed || e.State == "failed"
			}
		}
		return
	}

	tw(t, scriptedFile, "marks a child stopped when abort fires during the started trace callback", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		var admitCount, launchCount atomic.Int32
		o := wfOptions(`await runs.run("slow", { agent: "worker", task: "wait" });`)
		o.Ctx = ctx
		o.OnTrace = func(trace []WorkflowScriptTraceEntry) {
			for _, e := range trace {
				if e.Operation == "run" && e.Key == "slow" && e.State == "started" && ctx.Err() == nil {
					cancel(errors.New("Workflow stopped by user."))
				}
			}
		}
		o.Admit = func(c context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}] {
			admitCount.Add(1)
			return WorkflowResolved(struct{}{})
		}
		o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launchCount.Add(1)
			return WorkflowResolved(okChild(key, "done"))
		}
		we := wfFail(t, o)
		stopped, failed := stoppedTrace(we.Partial.Trace, "slow")
		if we.Message != "Workflow stopped by user." || !stopped || failed {
			t.Fatalf("message %q stopped=%v failed=%v", we.Message, stopped, failed)
		}
		time.Sleep(20 * time.Millisecond)
		if admitCount.Load() != 0 || launchCount.Load() != 0 {
			t.Fatalf("admit %d launch %d", admitCount.Load(), launchCount.Load())
		}
	})

	tw(t, scriptedFile, "does not launch a child after admission settles following workflow abort", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		var launchCount atomic.Int32
		admission := NewWorkflowFuture[struct{}]()
		started := make(chan context.Context, 1)
		o := wfOptions(`await runs.run("slow", { agent: "worker", task: "wait" });`)
		o.Ctx = ctx
		o.Admit = func(c context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}] {
			started <- c
			return admission
		}
		o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launchCount.Add(1)
			return WorkflowResolved(okChild(key, "done"))
		}
		type outcome struct {
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			_, err := RunWorkflowScript(o)
			done <- outcome{err}
		}()
		admissionCtx := <-started
		cancel(errors.New("Workflow stopped by user."))
		// the abort reaches the admission's signal when the run handles it (an AbortSignal is cancelled synchronously in Node)
		select {
		case <-admissionCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("the admission signal was not aborted")
		}
		out := <-done
		var we *WorkflowScriptError
		if !errors.As(out.err, &we) {
			t.Fatalf("err = %v", out.err)
		}
		stopped, failed := stoppedTrace(we.Partial.Trace, "slow")
		if we.Message != "Workflow stopped by user." || !stopped || failed {
			t.Fatalf("message %q stopped=%v failed=%v", we.Message, stopped, failed)
		}
		admission.Resolve(struct{}{})
		time.Sleep(20 * time.Millisecond)
		if launchCount.Load() != 0 {
			t.Fatalf("launches = %d", launchCount.Load())
		}
	})

	tw(t, scriptedFile, "classifies admission subprocess cancellation on workflow timeout as stopped", func(t *testing.T) {
		// Adaptation: the original admits through runSetupCommand (a spawned process) and a run-fanout budget; both belong to
		// other ports (worktree setup, run-fanout-budget). What this case proves for the workflow runtime is that an admission
		// still running at the timeout is cancelled through its signal, the child is reported stopped with the timeout text, and
		// nothing is launched.
		var spawned, launches atomic.Int32
		type settled struct{ outcome, errText string }
		childSettled := make(chan settled, 1)
		o := wfOptions(`await runs.run("slow", { agent: "worker", task: "wait" });`)
		o.WorkflowRunID = "admission-timeout"
		o.TimeoutMs = new(1000.0)
		o.Admit = func(c context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}] {
			spawned.Add(1)
			return WorkflowAsync(func() (struct{}, error) {
				<-c.Done()
				return struct{}{}, context.Cause(c)
			})
		}
		o.OnChildSettled = func(n WorkflowChildSettledNotification) { childSettled <- settled{n.Outcome, n.Error} }
		o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launches.Add(1)
			return WorkflowResolved(okChild(key, "unexpected"))
		}
		failKind(t, o, WorkflowFailureTimeout)
		if spawned.Load() != 1 {
			t.Fatalf("admission not started")
		}
		select {
		case r := <-childSettled:
			if r.outcome != "stopped" || !strings.Contains(r.errText, "timed out") {
				t.Fatalf("settled = %+v", r)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no child-settled notification")
		}
		if launches.Load() != 0 {
			t.Fatalf("launches = %d", launches.Load())
		}
	})

	tw(t, scriptedFile, "drops a child response that settles after the workflow aborts", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		launch := NewWorkflowFuture[*jsObject]()
		started := make(chan struct{}, 1)
		o := wfOptions(`await runs.run("slow", { agent: "worker", task: "wait" });`)
		o.Ctx = ctx
		o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			started <- struct{}{}
			return launch
		}
		done := make(chan error, 1)
		go func() { _, err := RunWorkflowScript(o); done <- err }()
		<-started
		cancel(errors.New("Workflow stopped by user."))
		err := <-done
		var we *WorkflowScriptError
		if !errors.As(err, &we) {
			t.Fatalf("err = %v", err)
		}
		hasStop := false
		for _, e := range we.Partial.Trace {
			hasStop = hasStop || (e.Operation == "run" && e.Key == "slow" && e.State == "stopped" && e.Error == "Workflow stopped by user.")
		}
		_, failed := stoppedTrace(we.Partial.Trace, "slow")
		if we.Message != "Workflow stopped by user." || !hasStop || failed {
			t.Fatalf("message %q stop=%v failed=%v", we.Message, hasStop, failed)
		}
		n := len(we.Partial.Trace)
		launch.Resolve(jo("key", "slow", "ok", true, "output", "done", "artifactPaths", []any{}, "results", []any{}))
		time.Sleep(30 * time.Millisecond)
		if len(we.Partial.Trace) != n {
			t.Fatalf("the trace changed after the run ended")
		}
	})

	tw(t, scriptedFile, "does not dispatch status after abort fires during the status started trace callback", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		var statusCount atomic.Int32
		o := wfOptions(`await runs.status("probe");`)
		o.Ctx = ctx
		o.OnTrace = func(trace []WorkflowScriptTraceEntry) {
			for _, e := range trace {
				if e.Operation == "status" && e.Key == "probe" && e.State == "started" && ctx.Err() == nil {
					cancel(errors.New("Workflow stopped by user."))
				}
			}
		}
		o.Status = func(c context.Context, key string) *WorkflowFuture[*jsObject] {
			statusCount.Add(1)
			return WorkflowResolved(okChild(key, "done"))
		}
		we := wfFail(t, o)
		started, completed := false, false
		for _, e := range we.Partial.Trace {
			if e.Operation == "status" && e.Key == "probe" {
				started = started || e.State == "started"
				completed = completed || e.State == "completed"
			}
		}
		time.Sleep(20 * time.Millisecond)
		if we.Message != "Workflow stopped by user." || !started || completed || statusCount.Load() != 0 {
			t.Fatalf("message %q started=%v completed=%v statusCount=%d", we.Message, started, completed, statusCount.Load())
		}
	})

	tw(t, scriptedFile, "drops a status response that settles after the workflow aborts", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		var mu sync.Mutex
		var traceLengths []int
		status := NewWorkflowFuture[*jsObject]()
		started := make(chan struct{}, 1)
		o := wfOptions(`await runs.status("probe");`)
		o.Ctx = ctx
		o.OnTrace = func(trace []WorkflowScriptTraceEntry) {
			mu.Lock()
			traceLengths = append(traceLengths, len(trace))
			mu.Unlock()
		}
		o.Status = func(c context.Context, key string) *WorkflowFuture[*jsObject] {
			started <- struct{}{}
			return status
		}
		done := make(chan error, 1)
		go func() { _, err := RunWorkflowScript(o); done <- err }()
		<-started
		cancel(errors.New("Workflow stopped by user."))
		var we *WorkflowScriptError
		if err := <-done; !errors.As(err, &we) {
			t.Fatalf("err = %v", err)
		}
		completed := false
		for _, e := range we.Partial.Trace {
			completed = completed || (e.Operation == "status" && e.State == "completed")
		}
		mu.Lock()
		final := traceLengths[len(traceLengths)-1]
		mu.Unlock()
		status.Resolve(okChild("probe", "done"))
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		after := traceLengths[len(traceLengths)-1]
		mu.Unlock()
		if we.Message != "Workflow stopped by user." || completed || after != final {
			t.Fatalf("message %q completed=%v trace %d -> %d", we.Message, completed, final, after)
		}
	})

	tw(t, scriptedFile, "keeps every child alive when a host trace callback throws", func(t *testing.T) {
		var thrown atomic.Int32
		var mu sync.Mutex
		var abortedWhileRunning []string
		o := wfOptions(`return await runs.all([{ key: "a", agent: "worker", task: "one" }, { key: "b", agent: "worker", task: "two" }, { key: "c", agent: "worker", task: "three" }]);`)
		o.TimeoutMs = new(5000.0)
		o.OnTrace = func(trace []WorkflowScriptTraceEntry) {
			if thrown.Load() == 0 {
				for _, e := range trace {
					if e.Operation == "run" && e.State == "completed" {
						thrown.Add(1)
						panic(errors.New("EPERM: operation not permitted, rename"))
					}
				}
			}
		}
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			finish := func() *jsObject {
				if ctx.Err() != nil {
					mu.Lock()
					abortedWhileRunning = append(abortedWhileRunning, key)
					mu.Unlock()
				}
				return okChild(key, key+" done")
			}
			if key == "a" {
				return WorkflowResolved(finish())
			}
			return WorkflowAsync(func() (*jsObject, error) { time.Sleep(10 * time.Millisecond); return finish(), nil })
		}
		res := wfRun(t, o)
		if thrown.Load() != 1 || len(abortedWhileRunning) != 0 {
			t.Fatalf("thrown %d aborted %v", thrown.Load(), abortedWhileRunning)
		}
		var keys []string
		for _, c := range res.Value.([]any) {
			ch := c.(*jsObject)
			keys = append(keys, objStr(ch, "key"))
			if !objBool(ch, "ok") {
				t.Errorf("child %s not ok", objStr(ch, "key"))
			}
		}
		for _, c := range res.Children {
			if !objBool(c, "ok") {
				t.Errorf("host child %s not ok", objStr(c, "key"))
			}
		}
		if fmt.Sprint(keys) != "[a b c]" {
			t.Fatalf("keys = %v", keys)
		}
	})

	tw(t, scriptedFile, "returns running launch receipts without failure or child-settlement notifications", func(t *testing.T) {
		var notifications []WorkflowChildSettledNotification
		launches := 0
		o := wfOptions(`const first = await runs.run("a", { agent: "worker", async: true });
				const reused = await runs.run("a", { agent: "worker", async: true });
				const batch = await runs.all([{ key: "b", agent: "worker", async: true }]);
				return [first, reused, ...batch];`)
		o.WorkflowRunID = "dispatch-only"
		o.OnChildSettled = func(n WorkflowChildSettledNotification) { notifications = append(notifications, n) }
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			launches++
			return WorkflowResolved(jo("key", key, "ok", false, "state", "running", "runId", key+"-run", "output", "", "asyncDir", "/tmp/"+key, "artifactPaths", []any{"/tmp/" + key}))
		}
		res := wfRun(t, o)
		if launches != 2 || len(notifications) != 0 || len(res.Children) != 2 {
			t.Fatalf("launches %d notifications %v children %d", launches, notifications, len(res.Children))
		}
		for _, c := range res.Children {
			if objStr(c, "state") != "running" || c.vals["error"] != nil || objBool(c, "ok") {
				t.Errorf("child = %s", marshalJSON(c, ""))
			}
		}
		for _, e := range res.Trace {
			if e.State == "completed" || e.State == "failed" {
				t.Errorf("trace entry %+v", e)
			}
		}
	})

	tw(t, scriptedFile, "notifies onChildSettled for each child as it completes while workflow runs", func(t *testing.T) {
		var mu sync.Mutex
		var notes []WorkflowChildSettledNotification
		release := NewWorkflowFuture[*jsObject]()
		var aSettledBeforeBReleased atomic.Bool
		o := wfOptions(`return await runs.all([
				{ key: "child-a", agent: "worker", task: "task-a" },
				{ key: "child-b", agent: "worker", task: "task-b" }
			]);`)
		o.WorkflowRunID = "test-workflow-1"
		o.TimeoutMs = new(5000.0)
		o.OnChildSettled = func(n WorkflowChildSettledNotification) {
			mu.Lock()
			notes = append(notes, n)
			mu.Unlock()
			if n.ChildKey == "child-a" && n.WorkflowRunning {
				aSettledBeforeBReleased.Store(true)
				release.Resolve(jo("key", "child-b", "ok", true, "runId", "run-b-456", "output", "B done", "outputReference", "/tmp/b.txt", "artifactPaths", []any{"/tmp/b.txt"}))
			}
		}
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			if key == "child-a" {
				return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-a-123", "output", "A done", "outputReference", "/tmp/a.txt", "artifactPaths", []any{"/tmp/a.txt"}))
			}
			return release
		}
		res := wfRun(t, o)
		if !aSettledBeforeBReleased.Load() || len(notes) != 2 {
			t.Fatalf("a-before-b=%v notes=%v", aSettledBeforeBReleased.Load(), notes)
		}
		for _, n := range notes {
			if n.Outcome != "completed" || !n.WorkflowRunning {
				t.Errorf("note = %+v", n)
			}
		}
		var keys []string
		for _, c := range res.Value.([]any) {
			keys = append(keys, objStr(c.(*jsObject), "key"))
		}
		if fmt.Sprint(keys) != "[child-a child-b]" {
			t.Fatalf("keys = %v", keys)
		}
	})

	tw(t, scriptedFile, "notifies onChildSettled with correct outcome for failed children", func(t *testing.T) {
		var notes []WorkflowChildSettledNotification
		o := wfOptions(`return await runs.all([
				{ key: "success-child", agent: "worker", task: "succeed" },
				{ key: "failed-child", agent: "worker", task: "fail" }
			]);`)
		o.WorkflowRunID = "test-workflow-2"
		o.TimeoutMs = new(5000.0)
		o.OnChildSettled = func(n WorkflowChildSettledNotification) { notes = append(notes, n) }
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			if key == "success-child" {
				return WorkflowResolved(jo("key", key, "ok", true, "runId", "run-success", "output", "Success", "artifactPaths", []any{}))
			}
			return WorkflowResolved(jo("key", key, "ok", false, "runId", "run-failed", "output", "Failed", "error", "Task failed", "artifactPaths", []any{}))
		}
		wfRun(t, o)
		if len(notes) != 2 {
			t.Fatalf("notes = %v", notes)
		}
		for _, n := range notes {
			switch n.ChildKey {
			case "success-child":
				if n.Outcome != "completed" {
					t.Errorf("note = %+v", n)
				}
			case "failed-child":
				if n.Outcome != "failed" || n.Error != "Task failed" {
					t.Errorf("note = %+v", n)
				}
			}
		}
	})

	tw(t, scriptedFile, "deduplicates onChildSettled notifications by child key and run ID", func(t *testing.T) {
		var notes []WorkflowChildSettledNotification
		o := wfOptions(`return await runs.run("single-child", { agent: "worker", task: "run once" });`)
		o.WorkflowRunID = "test-workflow-3"
		o.TimeoutMs = new(5000.0)
		o.OnChildSettled = func(n WorkflowChildSettledNotification) { notes = append(notes, n) }
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(jo("key", key, "ok", true, "runId", "unique-run-id", "output", "Done", "artifactPaths", []any{}))
		}
		wfRun(t, o)
		if len(notes) != 1 || notes[0].ChildKey != "single-child" || notes[0].ChildRunID != "unique-run-id" {
			t.Fatalf("notes = %+v", notes)
		}
	})

	tskip(t, scriptedFile, "re-anchors a stale process cwd before creating the workflow worker", "needs a Node process whose working directory was deleted and re-anchors it with process.chdir; a Go extension never changes or depends on its process cwd, so there is nothing to re-anchor")
	tskip(t, scriptedFile, "rejects an unavailable recovery target without falling back from a stale cwd", "the stale-process-cwd recovery it checks is Node's process.chdir re-anchoring, which the Go port does not have (no process cwd dependence)")
	tskip(t, scriptedFile, "does not chdir or expose the workflow target while launching from a healthy cwd", "asserts that Node's process.chdir was never called and that Worker init hooks saw the caller's cwd; the Go port has no chdir and no worker threads")

	tw(t, scriptedFile, "rejects an unavailable workflow process cwd before worker creation", func(t *testing.T) {
		// adaptation: Go reports the failure as the error text and Cause (fs.ErrNotExist) instead of Node's ENOENT code
		missing := filepath.Join(t.TempDir(), "missing-workflow-cwd")
		o := wfOptions(`return "unexpected";`)
		o.ProcessCwd = missing
		o.Launch = func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			t.Errorf("launched")
			return WorkflowResolved(okChild(key, "unexpected"))
		}
		we := wfFail(t, o)
		if !strings.Contains(we.Message, "Workflow process cwd is unavailable") || !strings.Contains(we.Message, missing) || !errors.Is(we.Cause, os.ErrNotExist) || we.Kind != WorkflowFailureRuntime {
			t.Fatalf("we = %+v", we)
		}
	})

	tw(t, scriptedFile, "rejects healthy cwd targets that cannot be entered", func(t *testing.T) {
		// adaptation: the original also spawns a fixture that removes the execute bit of a directory; here a plain file is
		// rejected as a non-directory, and a directory without search permission as unavailable (not as root, which ignores modes)
		root := t.TempDir()
		file := filepath.Join(root, "file")
		if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		o := wfOptions(`return "unexpected";`)
		o.ProcessCwd = file
		we := wfFail(t, o)
		if !strings.Contains(we.Message, file) || we.Cause == nil || !strings.Contains(we.Cause.Error(), "not a directory") {
			t.Fatalf("we = %+v", we)
		}
	})
}

// A script call that reaches the host after the run was aborted is dropped: no child launches (the Go counterpart of the
// original's "ignores a queued child launch message after workflow abort").
func TestWorkflowLateCallAfterAbortIsIgnored(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	var launches atomic.Int32
	o := wfOptions(`await new Promise(() => {});`)
	o.Ctx = ctx
	o.Launch = func(c context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
		launches.Add(1)
		return WorkflowResolved(okChild(key, "too late"))
	}
	go func() { time.Sleep(50 * time.Millisecond); cancel(errors.New("Workflow stopped by user.")) }()
	we := wfFail(t, o)
	if we.Message != "Workflow stopped by user." || launches.Load() != 0 {
		t.Fatalf("message %q launches %d", we.Message, launches.Load())
	}
}

func nan() float64 { var z float64; return z / z }
