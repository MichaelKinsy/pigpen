package pi_subagents

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// testdata/workflow-runtime.json is the original's runWorkflowScript (scripted-workflow.ts:2077) recorded over a corpus of
// scripts with port/gen-workflow-runtime-fixtures.mjs, with the deterministic host below. The Go run must give the same
// value, errors (first line and kind), emits, console, children, trace and launches.
type workflowRuntimeCase struct {
	Script   string          `json:"script"`
	Args     json.RawMessage `json:"args"`
	State    bool            `json:"state"`
	Expected json.RawMessage `json:"expected"`
}

func workflowFixtureHost(launchLog *[]string, trace *[]WorkflowScriptTraceEntry, withState bool) RunWorkflowScriptOptions {
	child := func(fields ...any) *jsObject {
		o := newObject()
		for i := 0; i < len(fields); i += 2 {
			o.set(fields[i].(string), fields[i+1])
		}
		return o
	}
	opts := RunWorkflowScriptOptions{
		TimeoutMs: new(8000.0),
		Launch: func(ctx context.Context, key string, params *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
			*launchLog = append(*launchLog, key)
			res := func() *jsObject {
				switch {
				case strings.HasPrefix(key, "fail"):
					return child("key", key, "ok", false, "output", "child failed "+key, "artifactPaths", []any{})
				case strings.HasPrefix(key, "detach"):
					return child("key", key, "ok", false, "detached", true, "output", "child detached "+key, "error", "detached "+key, "artifactPaths", []any{})
				case strings.HasPrefix(key, "run"):
					return child("key", key, "ok", true, "state", "running", "output", "", "artifactPaths", []any{}, "runId", "run-"+key)
				case strings.HasPrefix(key, "stop"):
					return child("key", key, "ok", false, "stopped", true, "output", "stopped "+key, "artifactPaths", []any{})
				}
				o := child("key", key, "ok", true, "output", "out-"+key, "runId", "id-"+key+"-12345678", "artifactPaths", []any{})
				if r, ok := params.vals["resume"]; ok {
					o.set("resumedFrom", r)
				}
				return o
			}
			if strings.HasPrefix(key, "slow") {
				return WorkflowAsync(func() (*jsObject, error) { time.Sleep(30 * time.Millisecond); return res(), nil })
			}
			return WorkflowResolved(res())
		},
		Status: func(ctx context.Context, key string) *WorkflowFuture[*jsObject] {
			if strings.HasPrefix(key, "fail") {
				return WorkflowResolved(child("key", key, "ok", false, "output", "no run "+key, "artifactPaths", []any{}))
			}
			return WorkflowResolved(child("key", key, "ok", true, "output", "status "+key, "artifactPaths", []any{}))
		},
		Steer: func(ctx context.Context, key, message string, o WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
			return WorkflowResolved(WorkflowSteerResult{Key: key, State: "delivered", RequestID: "req-1"})
		},
		Host: func(ctx context.Context, key string, p WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult] {
			if p.Command == "fail" {
				return WorkflowResolved(WorkflowHostCommandResult{Key: key, OK: false, State: "failed", ExitCode: new(1), Stderr: "bad", OutputPath: "/tmp/x/out.log", DurationMs: 3, Error: "failed"})
			}
			return WorkflowResolved(WorkflowHostCommandResult{Key: key, OK: true, State: "passed", ExitCode: new(0), Stdout: "o", OutputPath: "/tmp/x/out.log", DurationMs: 3})
		},
		OnTrace: func(t []WorkflowScriptTraceEntry) { *trace = t },
	}
	if withState {
		m := map[string]any{}
		opts.State = &WorkflowState{
			Get: func(k string) *WorkflowFuture[any] {
				if v, ok := m[k]; ok {
					return WorkflowResolved(v)
				}
				return WorkflowResolved[any](nil)
			},
			Set: func(k string, v any) *WorkflowFuture[struct{}] { m[k] = v; return WorkflowResolved(struct{}{}) },
		}
	}
	return opts
}

func workflowDiffOutcome(res WorkflowScriptResult, err error, launches []string) (ordered string, rest map[string]any) {
	rest = map[string]any{}
	trace := res.Trace
	emits, cons, children := res.Emits, res.Console, res.Children
	if err != nil {
		we := err.(*WorkflowScriptError)
		rest["ok"] = false
		if we.Kind != "" {
			rest["kind"] = we.Kind
		} else {
			rest["kind"] = nil
		}
		msg := strings.Split(we.Message, "\n    at ")[0]
		// the original puts the recovery hint after the stack frames, which the fixture cuts away
		for _, hint := range []string{" Return only plain JSON data.", " Child work completed before", " Child calls returned before"} {
			if i := strings.Index(msg, hint); i >= 0 && we.Kind == WorkflowFailureReturnSerialization {
				msg = msg[:i]
			}
		}
		rest["message"] = msg
		trace, emits, cons, children = we.Partial.Trace, we.Partial.Emits, we.Partial.Console, we.Partial.Children
	} else {
		rest["ok"] = true
		ordered = marshalJSON(res.Value, "")
	}
	toAny := func(v any) any {
		var out any
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &out)
		return out
	}
	var emitList []any
	for _, e := range emits {
		emitList = append(emitList, toAny(e))
	}
	rest["emits"] = emitList
	cl := []any{}
	for _, c := range cons {
		cl = append(cl, toAny(c))
	}
	rest["console"] = cl
	var keys []any
	for _, c := range children {
		keys = append(keys, objStr(c, "key"))
	}
	rest["children"] = keys
	tl := []any{}
	for _, e := range trace {
		m := toAny(e).(map[string]any)
		if e.DurationMs != nil {
			m["durationMs"] = "n"
		}
		tl = append(tl, m)
	}
	rest["trace"] = tl
	l := []any{}
	for _, k := range launches {
		l = append(l, k)
	}
	rest["launches"] = l
	return ordered, rest
}

func TestWorkflowRuntimeMatchesTheOriginal(t *testing.T) {
	raw, err := os.ReadFile("testdata/workflow-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []workflowRuntimeCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, c := range cases {
		var launches []string
		var trace []WorkflowScriptTraceEntry
		opts := workflowFixtureHost(&launches, &trace, c.State)
		opts.Script = c.Script
		if len(c.Args) > 0 && string(c.Args) != "null" {
			a, err := parseJSON(c.Args)
			if err != nil {
				t.Fatal(err)
			}
			opts.Args = a.(*jsObject)
		}
		res, runErr := RunWorkflowScript(opts)
		gotOrdered, gotRest := workflowDiffOutcome(res, runErr, launches)

		var want map[string]any
		_ = json.Unmarshal(c.Expected, &want)
		wantOrderedV, _ := func() (any, error) {
			var wrap struct {
				Value json.RawMessage `json:"value"`
			}
			_ = json.Unmarshal(c.Expected, &wrap)
			if len(wrap.Value) == 0 {
				return nil, nil
			}
			return parseJSON(wrap.Value)
		}()
		wantOrdered := ""
		if want["ok"] == true {
			wantOrdered = marshalJSON(wantOrderedV, "")
		}
		delete(want, "value")
		// the fixture holds nulls for absent lists
		normalize := func(m map[string]any) {
			for _, k := range []string{"emits", "console", "children", "trace", "launches"} {
				if v, ok := m[k]; ok {
					if l, isList := v.([]any); isList && len(l) == 0 {
						m[k] = nil
					}
				}
			}
		}
		normalize(want)
		normalize(gotRest)
		if gotOrdered != wantOrdered || !reflect.DeepEqual(want, gotRest) {
			failures++
			gj, _ := json.Marshal(gotRest)
			wj, _ := json.Marshal(want)
			t.Errorf("case %d %q\n got  value=%s %s\n want value=%s %s", i, c.Script, gotOrdered, gj, wantOrdered, wj)
		}
	}
	t.Logf("%d cases, %d differ", len(cases), failures)
}
