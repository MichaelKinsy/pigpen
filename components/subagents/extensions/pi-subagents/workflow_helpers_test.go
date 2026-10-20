package pi_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Helpers shared by the workflow twins. They stand in for the object literals and async callbacks of the original's tests.

// okChild is `{ key, ok: true, output, artifactPaths: [] }`.
func okChild(key, output string) *jsObject {
	return jo("key", key, "ok", true, "output", output, "artifactPaths", []any{})
}

func failedChild(key, output string) *jsObject {
	return jo("key", key, "ok", false, "output", output, "artifactPaths", []any{})
}

func resolvedChild(c *jsObject) *WorkflowFuture[*jsObject] { return WorkflowResolved(c) }

// wfOptions returns options whose launch and status answer `{ key, ok: true, output: "ok" }`.
func wfOptions(script string) RunWorkflowScriptOptions {
	return RunWorkflowScriptOptions{
		Script: script,
		Launch: func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "ok"))
		},
		Status: func(ctx context.Context, key string) *WorkflowFuture[*jsObject] {
			return WorkflowResolved(okChild(key, "ok"))
		},
	}
}

// wfRun runs a script and fails the test on an error.
func wfRun(t *testing.T, opts RunWorkflowScriptOptions) WorkflowScriptResult {
	t.Helper()
	res, err := RunWorkflowScript(opts)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	return res
}

// wfFail runs a script that must fail and returns the error.
func wfFail(t *testing.T, opts RunWorkflowScriptOptions) *WorkflowScriptError {
	t.Helper()
	_, err := RunWorkflowScript(opts)
	if err == nil {
		t.Fatalf("workflow succeeded; want an error")
	}
	var we *WorkflowScriptError
	if !errors.As(err, &we) {
		t.Fatalf("error is %T, not a WorkflowScriptError: %v", err, err)
	}
	return we
}

// jsonEq compares host data with an expected JSON text, ignoring object key order (assert.deepEqual).
func jsonEq(t *testing.T, got any, want string, msg ...string) {
	t.Helper()
	norm := func(v any) any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out any
		_ = json.Unmarshal(b, &out)
		return out
	}
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad expectation %q: %v", want, err)
	}
	if g := norm(got); !reflect.DeepEqual(g, w) {
		gj, _ := json.Marshal(g)
		t.Fatalf("%s\n got  %s\n want %s", strings.Join(msg, " "), gj, want)
	}
}

func traceStates(trace []WorkflowScriptTraceEntry) [][2]string {
	var out [][2]string
	for _, e := range trace {
		out = append(out, [2]string{e.Operation, e.State})
	}
	return out
}

func childKeys(children []*jsObject) []string {
	out := []string{}
	for _, c := range children {
		out = append(out, objStr(c, "key"))
	}
	return out
}

// onAbort returns a future that fails with the context's cause once ctx is cancelled (a launch that waits for its abort signal).
func neverUntilAbort(ctx context.Context) *WorkflowFuture[*jsObject] {
	return WorkflowAsync(func() (*jsObject, error) {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})
}

func delayed(d time.Duration, c *jsObject) *WorkflowFuture[*jsObject] {
	return WorkflowAfter(d, c)
}

func hostOK(key string, outputPath string) WorkflowHostCommandResult {
	return WorkflowHostCommandResult{Key: key, OK: true, State: "passed", ExitCode: new(0), OutputPath: outputPath, DurationMs: 1}
}
