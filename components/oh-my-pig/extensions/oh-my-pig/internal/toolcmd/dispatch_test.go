package toolcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// newDispatchTestEnv wires a strict fake host with mounted `fake`, `other`,
// `checkpoint`, and `github` tools plus a test `bash` tool that mirrors
// internal/shell's direct and preamble/bridge paths and optionally drains
// dispatches via DrainDispatches.
func newDispatchTestEnv(
	t *testing.T,
	execFn func(ctx context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome,
) (*fakeHost, *sync.Mutex, map[string][]map[string]any, map[string]sdk.Context) {
	t.Helper()
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	flagSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"i": {"type": "string", "description": "concise intent"},
			"flag": {"type": "string", "description": "Flag value."}
		},
		"required": ["i", "flag"]
	}`)
	checkpointSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"goal": {"type": "string"}
		},
		"required": ["goal"]
	}`)
	githubSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"op": {"type": "string"},
			"repo": {"type": "string"},
			"body": {"type": "string"}
		},
		"required": ["op"]
	}`)

	Mountable("fake", func(sdk.Context) bool { return true })
	Mountable("other", func(sdk.Context) bool { return true })
	Mountable("checkpoint", func(sdk.Context) bool { return true })
	Mountable("github", func(sdk.Context) bool { return true })

	var mu sync.Mutex
	drainedByCall := make(map[string][]map[string]any)
	ctxByCall := make(map[string]sdk.Context)

	bashPath := findBash(t)
	ext := sdk.New("test-dispatch-record")
	Register(ext)

	ext.Tool("bash", "Shell.", sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"command":    map[string]any{"type": "string"},
			"mode":       map[string]any{"type": "string"}, // "direct" | "bridge" | "bridge_abort"
			"drain":      map[string]any{"type": "boolean"},
			"drainTwice": map[string]any{"type": "boolean"},
		},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		script, _ := params["command"].(string)
		mode, _ := params["mode"].(string)
		wantDrain, hasDrain := params["drain"].(bool)
		if !hasDrain {
			wantDrain = true
		}
		drainTwice, _ := params["drainTwice"].(bool)

		mu.Lock()
		ctxByCall[ctx.ToolCallID()] = ctx
		mu.Unlock()

		var stdout, stderr string
		var rc int
		var handled bool

		if mode == "direct" {
			dr := TryDirectWithOptions(ctx, script, DirectOptions{Cwd: ctx.Cwd(), Timeout: 5 * time.Second})
			stdout = dr.Output
			rc = dr.ExitCode
			handled = dr.Handled
		} else {
			preamble, release := PreambleWithRelease(ctx)
			defer release()
			if preamble == "" {
				return nil, fmt.Errorf("empty preamble")
			}
			tmpFile, err := os.CreateTemp(t.TempDir(), "omp-dispatch-preamble-*.sh")
			if err != nil {
				return nil, err
			}
			preamblePath := tmpFile.Name()
			_, _ = tmpFile.WriteString(preamble)
			_ = tmpFile.Close()
			defer func() { _ = os.Remove(preamblePath) }()

			wrapper := fmt.Sprintf(". %s\n%s", singleQuoteShell(preamblePath), script)
			cmd := exec.Command(bashPath, "-c", wrapper)
			cmd.Dir = ctx.Cwd()
			var outBuf, errBuf bytes.Buffer
			cmd.Stdout = &outBuf
			cmd.Stderr = &errBuf
			runErr := cmd.Run()
			stdout = outBuf.String()
			stderr = errBuf.String()
			if runErr != nil {
				var ee *exec.ExitError
				if errorAs(runErr, &ee) {
					rc = ee.ExitCode()
				} else {
					return nil, runErr
				}
			}
			if mode == "bridge_abort" {
				// Simulate an aborted bash run that releases its grant without draining.
				release()
				return map[string]any{"aborted": true}, nil
			}
		}

		var dispatches []map[string]any
		var secondDrain []map[string]any
		if wantDrain {
			dispatches = DrainDispatches(ctx)
			if drainTwice {
				secondDrain = DrainDispatches(ctx)
			}
			mu.Lock()
			drainedByCall[ctx.ToolCallID()] = dispatches
			mu.Unlock()
		}

		details := map[string]any{}
		if len(dispatches) > 0 {
			details["toolCommands"] = dispatches
		}
		return map[string]any{
			"stdout":      stdout,
			"stderr":      stderr,
			"exitCode":    rc,
			"handled":     handled,
			"details":     details,
			"secondDrain": secondDrain,
		}, nil
	})

	var nestedSeq atomic.Int64
	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "dispatch-sess-" + t.Name(),
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "fake", Description: "Fixture fake tool.", Exposure: sdk.ToolExposureCodemode, Parameters: flagSchema},
			{Name: "other", Description: "Fixture other tool.", Exposure: sdk.ToolExposureCodemode, Parameters: flagSchema},
			{Name: "checkpoint", Description: "Checkpoint tool.", Exposure: sdk.ToolExposureCodemode, Parameters: checkpointSchema},
			{Name: "github", Description: "GitHub tool.", Exposure: sdk.ToolExposureCodemode, Parameters: githubSchema},
		},
		activeTools: []string{"bash"},
		executeTool: func(ctx context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			if execFn != nil {
				return execFn(ctx, callerID, name, args)
			}
			seq := nestedSeq.Add(1)
			nestedID := fmt.Sprintf("%s/%d", callerID, seq)
			switch name {
			case "checkpoint":
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: nestedID, Name: name, Arguments: args},
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: "checkpoint created"}},
						Details: map[string]any{"startedAt": "2026-01-01T00:00:00Z", "goal": args["goal"]},
					},
				}
			case "github":
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: nestedID, Name: name, Arguments: args},
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: "pr created"}},
						Details: map[string]any{"prNumber": 42},
					},
				}
			default:
				flagVal, _ := args["flag"].(string)
				isErr := flagVal == "fail"
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: nestedID, Name: name, Arguments: args},
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: fmt.Sprintf("%s:%s", name, flagVal)}},
						Details: map[string]any{"echoed": flagVal},
						IsError: isErr,
					},
					IsError: isErr,
				}
			}
		},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}
	return host, &mu, drainedByCall, ctxByCall
}

// upstream: packages/coding-agent/test/bash-tool-commands.test.ts: "runs a mounted tool and records one dispatch"
// upstream: packages/coding-agent/test/bash-tool-commands.test.ts: "records two tools of one line in invocation order"
// upstream: packages/coding-agent/test/bash-tool-commands.test.ts: "prints a tool's usage for --help without executing it"
func TestDispatchRecordingDirectAndBridgeWireShape(t *testing.T) {
	host, _, _, _ := newDispatchTestEnv(t, nil)

	// 1. Direct path: sole invocation wire shape equals omp's ToolCommandDispatch
	// (tool, mode, exitCode, args without harness-injected `i`, inner, plus nested callId).
	raw, errMsg := host.callToolWithID("bash", "call-direct-1", map[string]any{
		"command":    "fake --flag v",
		"mode":       "direct",
		"drainTwice": true,
	})
	if errMsg != "" {
		t.Fatalf("direct call failed: %s", errMsg)
	}
	var directRes map[string]any
	if err := json.Unmarshal(raw, &directRes); err != nil {
		t.Fatal(err)
	}
	if directRes["handled"] != true {
		t.Fatalf("expected direct path handled=true, got %+v", directRes)
	}
	if directRes["secondDrain"] != nil {
		t.Fatalf("second DrainDispatches should return nil, got %#v", directRes["secondDrain"])
	}
	dispatches := ToolCommandDispatches("bash", directRes)
	if len(dispatches) != 1 {
		t.Fatalf("expected 1 direct dispatch, got %+v", dispatches)
	}
	d0 := dispatches[0]
	if d0.Tool != "fake" || d0.Mode != "execute" || d0.ExitCode != 0 {
		t.Fatalf("unexpected direct dispatch header: %+v", d0)
	}
	if !reflect.DeepEqual(d0.Args, map[string]any{"flag": "v"}) {
		t.Fatalf("expected args without harness `i`, got %#v", d0.Args)
	}
	if !reflect.DeepEqual(d0.Inner, map[string]any{"echoed": "v"}) {
		t.Fatalf("expected inner details from ExecuteTool outcome, got %#v", d0.Inner)
	}
	if d0.CallID == "" || d0.CallID == "call-direct-1" || d0.CallID != "call-direct-1/1" {
		t.Fatalf("expected nested callId call-direct-1/1 (distinct from bash id), got %q", d0.CallID)
	}

	// Verify exact JSON keys on the wire for sole invocation.
	rawDetails := directRes["details"].(map[string]any)
	rawTC := rawDetails["toolCommands"].([]any)[0].(map[string]any)
	wantSoleKeys := map[string]bool{"tool": true, "mode": true, "exitCode": true, "args": true, "inner": true, "callId": true}
	if len(rawTC) != len(wantSoleKeys) {
		t.Fatalf("unexpected wire keys: got %#v, want keys %v", rawTC, wantSoleKeys)
	}
	for k := range wantSoleKeys {
		if _, ok := rawTC[k]; !ok {
			t.Fatalf("missing wire key %q in %#v", k, rawTC)
		}
	}

	// 2. Direct path `--help`: records mode="help", exitCode=0, no args/inner/callId.
	rawHelp, errMsg := host.callToolWithID("bash", "call-direct-help", map[string]any{
		"command": "fake --help",
		"mode":    "direct",
	})
	if errMsg != "" {
		t.Fatalf("direct help failed: %s", errMsg)
	}
	var helpRes map[string]any
	_ = json.Unmarshal(rawHelp, &helpRes)
	helpDispatches := ToolCommandDispatches("bash", helpRes)
	if len(helpDispatches) != 1 || helpDispatches[0].Tool != "fake" || helpDispatches[0].Mode != "help" || helpDispatches[0].ExitCode != 0 {
		t.Fatalf("unexpected help dispatch: %+v", helpDispatches)
	}
	rawHelpTC := helpRes["details"].(map[string]any)["toolCommands"].([]any)[0].(map[string]any)
	if _, hasArgs := rawHelpTC["args"]; hasArgs {
		t.Fatalf("help dispatch must omit args: %#v", rawHelpTC)
	}
	if _, hasInner := rawHelpTC["inner"]; hasInner {
		t.Fatalf("help dispatch must omit inner: %#v", rawHelpTC)
	}
	if _, hasCallID := rawHelpTC["callId"]; hasCallID {
		t.Fatalf("help dispatch must omit callId: %#v", rawHelpTC)
	}

	// 3. Bridge path: compound line executes two tools in order and records both once.
	rawComp, errMsg := host.callToolWithID("bash", "call-bridge-compound", map[string]any{
		"command":    "fake --flag a; other --flag b",
		"mode":       "bridge",
		"drainTwice": true,
	})
	if errMsg != "" {
		t.Fatalf("bridge compound failed: %s", errMsg)
	}
	var compRes map[string]any
	_ = json.Unmarshal(rawComp, &compRes)
	if compRes["secondDrain"] != nil {
		t.Fatalf("second DrainDispatches on bridge call should return nil, got %#v", compRes["secondDrain"])
	}
	compDispatches := ToolCommandDispatches("bash", compRes)
	if len(compDispatches) != 2 {
		t.Fatalf("expected 2 compound dispatches in order, got %+v", compDispatches)
	}
	if compDispatches[0].Tool != "fake" || compDispatches[0].Mode != "execute" || compDispatches[0].ExitCode != 0 ||
		!reflect.DeepEqual(compDispatches[0].Args, map[string]any{"flag": "a"}) ||
		compDispatches[0].CallID == "" || compDispatches[0].CallID == "call-bridge-compound" {
		t.Fatalf("unexpected first compound dispatch: %+v", compDispatches[0])
	}
	if compDispatches[1].Tool != "other" || compDispatches[1].Mode != "execute" || compDispatches[1].ExitCode != 0 ||
		!reflect.DeepEqual(compDispatches[1].Args, map[string]any{"flag": "b"}) ||
		compDispatches[1].CallID == "" || compDispatches[1].CallID == compDispatches[0].CallID {
		t.Fatalf("unexpected second compound dispatch: %+v", compDispatches[1])
	}

	// 4. Bridge path with checkpoint + error exit + github pr_create projection.
	rawMix, errMsg := host.callToolWithID("bash", "call-bridge-mix", map[string]any{
		"command": "checkpoint --goal explore; fake --flag fail; github --op pr_create --repo org/r --body huge-pr-body",
		"mode":    "bridge",
	})
	if errMsg != "" {
		t.Fatalf("bridge mix failed: %s", errMsg)
	}
	var mixRes map[string]any
	_ = json.Unmarshal(rawMix, &mixRes)
	mixDispatches := ToolCommandDispatches("bash", mixRes)
	if len(mixDispatches) != 3 {
		t.Fatalf("expected 3 dispatches, got %+v", mixDispatches)
	}
	if mixDispatches[0].Tool != "checkpoint" || mixDispatches[0].ExitCode != 0 ||
		!reflect.DeepEqual(mixDispatches[0].Inner, map[string]any{"startedAt": "2026-01-01T00:00:00Z", "goal": "explore"}) ||
		mixDispatches[0].CallID == "" {
		t.Fatalf("unexpected checkpoint dispatch: %+v", mixDispatches[0])
	}
	if mixDispatches[1].Tool != "fake" || mixDispatches[1].ExitCode != 1 {
		t.Fatalf("expected failing fake dispatch with exitCode=1, got %+v", mixDispatches[1])
	}
	if mixDispatches[2].Tool != "github" || !reflect.DeepEqual(mixDispatches[2].Args, map[string]any{"op": "pr_create", "repo": "org/r"}) {
		t.Fatalf("expected github pr_create args projection (no body), got %+v", mixDispatches[2])
	}
}

func TestDispatchRecordingConcurrentBashCallsIsolated(t *testing.T) {
	// Synchronize two concurrent bash calls so both execute tool commands while
	// the other is still in flight, proving neither sees the other's records.
	bothStarted := make(chan struct{})
	var arrived atomic.Int32
	host, _, _, _ := newDispatchTestEnv(t, func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
		flagVal, _ := args["flag"].(string)
		if flagVal == "first-A" || flagVal == "first-B" {
			if arrived.Add(1) == 2 {
				close(bothStarted)
			}
			select {
			case <-bothStarted:
			case <-time.After(5 * time.Second):
			}
		}
		return sdk.AgentToolCallOutcome{
			ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/" + name + "-" + flagVal, Name: name, Arguments: args},
			Result: sdk.AgentToolResult{
				Content: []sdk.ToolResultContent{{Type: "text", Text: name + ":" + flagVal}},
				Details: map[string]any{"owner": callerID, "flag": flagVal},
			},
		}
	})

	type callResult struct {
		raw    json.RawMessage
		errMsg string
	}
	resA := make(chan callResult, 1)
	resB := make(chan callResult, 1)

	go func() {
		raw, errMsg := host.callToolWithID("bash", "bash-call-A", map[string]any{
			"command": "fake --flag first-A; other --flag second-A",
			"mode":    "bridge",
		})
		resA <- callResult{raw: raw, errMsg: errMsg}
	}()
	go func() {
		raw, errMsg := host.callToolWithID("bash", "bash-call-B", map[string]any{
			"command": "other --flag first-B; fake --flag second-B",
			"mode":    "bridge",
		})
		resB <- callResult{raw: raw, errMsg: errMsg}
	}()

	gotA := <-resA
	gotB := <-resB
	if gotA.errMsg != "" {
		t.Fatalf("call A failed: %s", gotA.errMsg)
	}
	if gotB.errMsg != "" {
		t.Fatalf("call B failed: %s", gotB.errMsg)
	}

	var mapA, mapB map[string]any
	_ = json.Unmarshal(gotA.raw, &mapA)
	_ = json.Unmarshal(gotB.raw, &mapB)

	dA := ToolCommandDispatches("bash", mapA)
	dB := ToolCommandDispatches("bash", mapB)

	if len(dA) != 2 || dA[0].Tool != "fake" || dA[0].Args["flag"] != "first-A" ||
		dA[1].Tool != "other" || dA[1].Args["flag"] != "second-A" {
		t.Fatalf("call A dispatches leaked or reordered: %+v", dA)
	}
	if len(dB) != 2 || dB[0].Tool != "other" || dB[0].Args["flag"] != "first-B" ||
		dB[1].Tool != "fake" || dB[1].Args["flag"] != "second-B" {
		t.Fatalf("call B dispatches leaked or reordered: %+v", dB)
	}
}

func TestDispatchRecordingDroppedOnGrantReleaseAndSessionEnd(t *testing.T) {
	host, mu, _, ctxByCall := newDispatchTestEnv(t, nil)

	// 1. Aborted bridge call: runs a tool command, then releases its grant
	// without calling DrainDispatches. The records must be dropped immediately.
	if _, errMsg := host.callToolWithID("bash", "call-aborted-1", map[string]any{
		"command": "fake --flag aborted",
		"mode":    "bridge_abort",
		"drain":   false,
	}); errMsg != "" {
		t.Fatalf("aborted call failed: %s", errMsg)
	}

	mu.Lock()
	abortedCtx := ctxByCall["call-aborted-1"]
	mu.Unlock()
	if got := DrainDispatches(abortedCtx); got != nil {
		t.Fatalf("expected nil after grant release dropped undrained records, got %#v", got)
	}

	// Verify internal map has no lingering entry for the aborted call.
	sess := defaultRuntime.getSession("dispatch-sess-" + t.Name())
	if sess == nil {
		t.Fatal("expected active session")
	}
	sess.dispatchesMu.Lock()
	remaining := len(sess.dispatches)
	sess.dispatchesMu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 remaining dispatch buckets after release, got %d", remaining)
	}

	// 2. Direct call that never drains: dropped cleanly on lifecycle.End / session_shutdown.
	if _, errMsg := host.callToolWithID("bash", "call-undrained-direct", map[string]any{
		"command": "fake --flag undrained",
		"mode":    "direct",
		"drain":   false,
	}); errMsg != "" {
		t.Fatalf("undrained direct call failed: %s", errMsg)
	}
	sess.dispatchesMu.Lock()
	beforeEnd := len(sess.dispatches)
	sess.dispatchesMu.Unlock()
	if beforeEnd != 1 {
		t.Fatalf("expected 1 undrained bucket before shutdown, got %d", beforeEnd)
	}

	if _, errMsg := host.emitEvent(sdk.EventSessionShutdown, nil); errMsg != "" {
		t.Fatalf("session_shutdown: %s", errMsg)
	}
	mu.Lock()
	undrainedCtx := ctxByCall["call-undrained-direct"]
	mu.Unlock()
	if got := DrainDispatches(undrainedCtx); got != nil {
		t.Fatalf("expected nil after session shutdown, got %#v", got)
	}
	if defaultRuntime.getSession("dispatch-sess-"+t.Name()) != nil {
		t.Fatal("expected session removed from runtime on session_shutdown")
	}
}

// TestDispatchRecordingOmitsEmptyInnerDetails verifies that when a wrapped
// mounted tool returns empty Details (`{}`, `*sdk.OrderedObject{}` with no
// keys, or `nil`), `DrainDispatches` omits the `inner` key (matching omp
// `tools/tool-commands.ts:475`), while non-empty Details are preserved.
func TestDispatchRecordingOmitsEmptyInnerDetails(t *testing.T) {
	host, _, _, _ := newDispatchTestEnv(t, func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
		flagVal, _ := args["flag"].(string)
		var details any
		switch flagVal {
		case "empty-map":
			details = map[string]any{}
		case "empty-ordered":
			details = sdk.NewOrderedObject()
		case "nil-details":
			details = nil
		default:
			details = map[string]any{"kept": flagVal}
		}
		return sdk.AgentToolCallOutcome{
			ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
			Result: sdk.AgentToolResult{
				Content: []sdk.ToolResultContent{{Type: "text", Text: name + ":" + flagVal}},
				Details: details,
			},
		}
	})

	for _, tc := range []struct {
		flag      string
		wantInner bool
	}{
		{"empty-map", false},
		{"empty-ordered", false},
		{"nil-details", false},
		{"non-empty", true},
	} {
		raw, errMsg := host.callToolWithID("bash", "call-inner-"+tc.flag, map[string]any{
			"command": "fake --flag " + tc.flag,
			"mode":    "direct",
		})
		if errMsg != "" {
			t.Fatalf("%s failed: %s", tc.flag, errMsg)
		}
		var res map[string]any
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		tcList := res["details"].(map[string]any)["toolCommands"].([]any)
		if len(tcList) != 1 {
			t.Fatalf("%s: expected 1 dispatch, got %#v", tc.flag, tcList)
		}
		recMap := tcList[0].(map[string]any)
		_, hasInner := recMap["inner"]
		if hasInner != tc.wantInner {
			t.Fatalf("%s: hasInner=%v, want %v (record=%#v)", tc.flag, hasInner, tc.wantInner, recMap)
		}
	}
}
