package todo

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	sdktui "github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

func decodePhasesFromRes(t *testing.T, res map[string]any) []TodoPhase {
	t.Helper()
	det, _ := res["details"].(map[string]any)
	raw, _ := json.Marshal(det["phases"])
	var out []TodoPhase
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode phases %s: %v", string(raw), err)
	}
	return out
}

func resSummary(res map[string]any) string {
	s, _ := res["content"].(string)
	return s
}

func resIsErr(res map[string]any) bool {
	if b, ok := res["is_error"].(bool); ok {
		return b
	}
	b, _ := res["isError"].(bool)
	return b
}

func boundaryResult(res map[string]any) map[string]any {
	if res == nil {
		return nil
	}
	if inner, exists := res["_pigBoundaryResult"]; exists {
		m, _ := inner.(map[string]any)
		return m
	}
	return res
}

func startTodoHost(t *testing.T, opts hostOptions) *fakeHost {
	t.Helper()
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	return startHost(t, ext, opts)
}

func TestTodoRegistrationSchemaAndDescription(t *testing.T) {
	h := startTodoHost(t, hostOptions{})
	tl, ok := h.tools["todo"]
	if !ok {
		t.Fatalf("todo tool not registered")
	}
	if tl.Label != "Todo" {
		t.Errorf("Label = %q, want %q", tl.Label, "Todo")
	}
	if tl.ExecutionMode != "sequential" {
		t.Errorf("ExecutionMode = %q, want %q", tl.ExecutionMode, "sequential")
	}
	wantDesc := strings.TrimRight(todoPrompt, "\r\n")
	if tl.Description != wantDesc {
		t.Errorf("Description mismatch: got len %d (trailing newline? %v), want len %d", len(tl.Description), strings.HasSuffix(tl.Description, "\n"), len(wantDesc))
	}
	props, _ := tl.Parameters["properties"].(map[string]any)
	opProp, _ := props["op"].(map[string]any)
	enumVals, _ := opProp["enum"].([]any)
	wantOps := []string{"init", "start", "done", "rm", "drop", "append", "view"}
	if len(enumVals) != len(wantOps) {
		t.Fatalf("op enum = %v, want %v", enumVals, wantOps)
	}
	for i, w := range wantOps {
		if enumVals[i] != w {
			t.Errorf("op enum[%d] = %v, want %q", i, enumVals[i], w)
		}
	}
	if _, hasReason := props["reason"]; hasReason {
		t.Errorf("retired reason property still in schema")
	}
	if !h.commands["todo"] {
		t.Errorf("/todo command not registered")
	}
}

func TestResolveTodoMarkdownPath(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "todo-workspace")

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "defaults to TODO.md under cwd"
	got, err := ResolveTodoMarkdownPath("", cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(cwd, "TODO.md"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "strips surrounding double quotes before resolving"
	got, err = ResolveTodoMarkdownPath(`"my todos.md"`, cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(cwd, "my todos.md"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "rejects internal URL schemes"
	_, err = ResolveTodoMarkdownPath("artifact://todo", cwd)
	if err == nil || !strings.Contains(err.Error(), "internal scheme") {
		t.Fatalf("expected internal scheme error, got %v", err)
	}

	// Tilde expansion (A12)
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err = ResolveTodoMarkdownPath("~/my.md", cwd)
	if err != nil {
		t.Fatalf("tilde error: %v", err)
	}
	if want := filepath.Join(home, "my.md"); got != want {
		t.Fatalf("tilde got %q, want %q", got, want)
	}
}

func TestTodoToolCoreOpsViaRealHandler(t *testing.T) {
	h := startTodoHost(t, hostOptions{})

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "auto-starts the first task after init"
	res, errMsg := h.callTool("todo", map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Execution", "items": []any{"status", "diagnostics"}}},
	})
	if errMsg != "" || resIsErr(res) {
		t.Fatalf("init failed: err=%q res=%+v", errMsg, res)
	}
	phases := decodePhasesFromRes(t, res)
	if phases[0].Tasks[0].Status != StatusInProgress || phases[0].Tasks[1].Status != StatusPending {
		t.Fatalf("unexpected statuses: %+v", phases[0].Tasks)
	}
	for _, want := range []string{"Remaining items (2):", "status [in_progress] (Execution)", "diagnostics [pending] (Execution)"} {
		if !strings.Contains(resSummary(res), want) {
			t.Errorf("summary missing %q:\n%s", want, resSummary(res))
		}
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "auto-promotes the next pending task when current task is completed"
	res, _ = h.callTool("todo", map[string]any{"op": "done", "task": "status"})
	phases = decodePhasesFromRes(t, res)
	if phases[0].Tasks[0].Status != StatusCompleted || phases[0].Tasks[1].Status != StatusInProgress {
		t.Fatalf("unexpected statuses after done: %+v", phases[0].Tasks)
	}

	// init with empty list replaces state with empty phases (upstream: packages/coding-agent/src/tools/todo.ts:339,621)
	res, _ = h.callTool("todo", map[string]any{"op": "init", "list": []any{}})
	if resIsErr(res) || resSummary(res) != "Todo list cleared." {
		t.Fatalf("init list:[] got isErr=%v summary=%q", resIsErr(res), resSummary(res))
	}
	if len(decodePhasesFromRes(t, res)) != 0 {
		t.Fatalf("expected empty phases after init list:[]")
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "view on an empty list reports empty, not cleared"
	res, _ = h.callTool("todo", map[string]any{"op": "view"})
	if resIsErr(res) || resSummary(res) != "Todo list is empty." {
		t.Fatalf("view empty got isErr=%v summary=%q", resIsErr(res), resSummary(res))
	}
}

func TestTodoMultiTaskTargetingAndErrors(t *testing.T) {
	h := startTodoHost(t, hostOptions{})

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "applies done and rm to every task in an array across phases"
	_, _ = h.callTool("todo", map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "A", "items": []any{"a1", "a2"}},
			map[string]any{"phase": "B", "items": []any{"b1", "b2"}},
		},
	})
	doneRes, _ := h.callTool("todo", map[string]any{"op": "done", "task": []any{"a1", "b1"}})
	if resIsErr(doneRes) {
		t.Fatalf("done array failed: %s", resSummary(doneRes))
	}
	phases := decodePhasesFromRes(t, doneRes)
	if phases[0].Tasks[0].Status != StatusCompleted || phases[0].Tasks[1].Status != StatusInProgress ||
		phases[1].Tasks[0].Status != StatusCompleted || phases[1].Tasks[1].Status != StatusPending {
		t.Fatalf("unexpected statuses: %+v", phases)
	}
	rmRes, _ := h.callTool("todo", map[string]any{"op": "rm", "task": []any{"a2", "b2"}})
	phases = decodePhasesFromRes(t, rmRes)
	if len(phases[0].Tasks) != 1 || phases[0].Tasks[0].Content != "a1" || len(phases[1].Tasks) != 1 || phases[1].Tasks[0].Content != "b1" {
		t.Fatalf("unexpected after rm array: %+v", phases)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "splits a flattened task string like a path list, preferring verbatim content"
	_, _ = h.callTool("todo", map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "A", "items": []any{"Fix foo, bar", "a1", "a2", "a3"}}},
	})
	res, _ := h.callTool("todo", map[string]any{"op": "done", "task": "Fix foo, bar"})
	if decodePhasesFromRes(t, res)[0].Tasks[0].Status != StatusCompleted {
		t.Fatalf("verbatim match failed")
	}
	res, _ = h.callTool("todo", map[string]any{"op": "done", "task": "a2; nope"})
	if !resIsErr(res) || !strings.Contains(resSummary(res), `Errors: Task "nope" not found`) {
		t.Fatalf("partial split got isErr=%v summary=%q", resIsErr(res), resSummary(res))
	}
	res, _ = h.callTool("todo", map[string]any{"op": "done", "task": " a1  a2 "})
	phases = decodePhasesFromRes(t, res)
	if phases[0].Tasks[1].Status != StatusCompleted || phases[0].Tasks[2].Status != StatusCompleted {
		t.Fatalf("spaced split failed: %+v", phases[0].Tasks)
	}
	res, _ = h.callTool("todo", map[string]any{"op": "drop", "task": `"a3",`})
	if decodePhasesFromRes(t, res)[0].Tasks[3].Status != StatusAbandoned {
		t.Fatalf("quoted split failed")
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "rejects a task array with a missing entry, or more than one task to start, without changing state"
	_, _ = h.callTool("todo", map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "A", "items": []any{"a1", "a2", "a3"}}},
	})
	res, _ = h.callTool("todo", map[string]any{"op": "done", "task": []any{"a2", "nope"}})
	if !resIsErr(res) || !strings.Contains(resSummary(res), `Task "nope" not found`) {
		t.Fatalf("expected missing entry error, got %q", resSummary(res))
	}
	if decodePhasesFromRes(t, res)[0].Tasks[1].Status != StatusPending {
		t.Fatalf("state mutated on failed done array")
	}
	res, _ = h.callTool("todo", map[string]any{"op": "start", "task": []any{"a2", "a3"}})
	if !resIsErr(res) || decodePhasesFromRes(t, res)[0].Tasks[0].Status != StatusInProgress {
		t.Fatalf("start multi-task should error without mutating state")
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "refuses done and rm that name no task, and reads items as the tasks"
	if res, _ = h.callTool("todo", map[string]any{"op": "rm"}); !resIsErr(res) {
		t.Fatalf("bare rm should fail")
	}
	if res, _ = h.callTool("todo", map[string]any{"op": "done"}); !resIsErr(res) {
		t.Fatalf("bare done should fail")
	}
	res, _ = h.callTool("todo", map[string]any{"op": "done", "items": []any{"a2"}})
	if resIsErr(res) || decodePhasesFromRes(t, res)[0].Tasks[1].Status != StatusCompleted {
		t.Fatalf("done items failed: %s", resSummary(res))
	}

	// Raw error interpolation without Go %q escaping (A3)
	res, _ = h.callTool("todo", map[string]any{"op": "done", "task": `Fix "bar" in C:\tmp`})
	if !strings.Contains(resSummary(res), `Task "Fix "bar" in C:\tmp" not found`) {
		t.Fatalf("error text escaped quotes/backslashes: %q", resSummary(res))
	}
}

func TestTodoAppendPhaseDefaultsAndLenientInit(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/todo.test.ts: "appends to the active phase unless an explicit phase overrides it"
	h := startTodoHost(t, hostOptions{})
	_, _ = h.callTool("todo", map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "A", "items": []any{"a1", "a2"}},
			map[string]any{"phase": "B", "items": []any{"b1"}},
		},
	})
	res, _ := h.callTool("todo", map[string]any{"op": "append", "items": []any{"a3"}})
	if len(decodePhasesFromRes(t, res)[0].Tasks) != 3 {
		t.Fatalf("expected append to active phase A: %+v", decodePhasesFromRes(t, res))
	}
	_, _ = h.callTool("todo", map[string]any{"op": "start", "task": "b1"})
	res, _ = h.callTool("todo", map[string]any{"op": "append", "items": []any{"b2"}})
	if len(decodePhasesFromRes(t, res)[1].Tasks) != 2 {
		t.Fatalf("expected append to active phase B: %+v", decodePhasesFromRes(t, res))
	}
	res, _ = h.callTool("todo", map[string]any{"op": "append", "phase": "A", "items": []any{"a4"}})
	if len(decodePhasesFromRes(t, res)[0].Tasks) != 4 {
		t.Fatalf("explicit phase override failed")
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "uses the first pending phase when no task is active, skipping closed phases"
	updated, errs := ApplyParams([]TodoPhase{
		{Name: "Done", Tasks: []TodoItem{{Content: "wait", Status: StatusCompleted}}},
		{Name: "Next", Tasks: []TodoItem{{Content: "next", Status: StatusPending}}},
		{Name: "Later", Tasks: []TodoItem{{Content: "later", Status: StatusPending}}},
	}, TodoParams{Op: OpAppend, Items: []string{"new"}})
	if len(errs) != 0 || len(updated[1].Tasks) != 2 || updated[1].Tasks[1].Content != "new" {
		t.Fatalf("first pending phase append failed: errs=%v phases=%+v", errs, updated)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "appends to the last phase after completion without reopening completed work"
	h2 := startTodoHost(t, hostOptions{})
	_, _ = h2.callTool("todo", map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "A", "items": []any{"a1"}},
			map[string]any{"phase": "B", "items": []any{"b1"}},
		},
	})
	_, _ = h2.callTool("todo", map[string]any{"op": "done", "task": []any{"a1", "b1"}})
	res, _ = h2.callTool("todo", map[string]any{"op": "append", "items": []any{"b2"}})
	ph := decodePhasesFromRes(t, res)
	if ph[0].Tasks[0].Status != StatusCompleted || ph[1].Tasks[0].Status != StatusCompleted || ph[1].Tasks[1].Status != StatusInProgress {
		t.Fatalf("append after completion failed: %+v", ph)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "creates a phase for an empty list that subsequent phaseless appends reuse"
	h3 := startTodoHost(t, hostOptions{})
	_, _ = h3.callTool("todo", map[string]any{"op": "append", "items": []any{"first"}})
	res, _ = h3.callTool("todo", map[string]any{"op": "append", "items": []any{"second"}})
	ph = decodePhasesFromRes(t, res)
	if len(ph) != 1 || ph[0].Name != "Tasks" || len(ph[0].Tasks) != 2 {
		t.Fatalf("empty-list append reuse failed: %+v", ph)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "uses flat items when list is an empty placeholder array (#17346)"
	res, _ = h3.callTool("todo", map[string]any{"op": "init", "list": []any{}, "items": []any{"First", "Second"}})
	ph = decodePhasesFromRes(t, res)
	if resIsErr(res) || ph[0].Name != "Tasks" || len(ph[0].Tasks) != 2 {
		t.Fatalf("flat items with empty list failed: %+v", res)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "prefers a non-empty list over flat items"
	res, _ = h3.callTool("todo", map[string]any{
		"op":    "init",
		"list":  []any{map[string]any{"phase": "Work", "items": []any{"From list"}}},
		"items": []any{"From items"},
	})
	ph = decodePhasesFromRes(t, res)
	if ph[0].Name != "Work" || ph[0].Tasks[0].Content != "From list" {
		t.Fatalf("non-empty list precedence failed: %+v", ph)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "names an empty init phase Tasks so append can target it"
	res, _ = h3.callTool("todo", map[string]any{"op": "init", "list": []any{map[string]any{"phase": "", "items": []any{"One"}}}})
	res, _ = h3.callTool("todo", map[string]any{"op": "append", "phase": "Tasks", "items": []any{"Two"}})
	ph = decodePhasesFromRes(t, res)
	if resIsErr(res) || len(ph) != 1 || ph[0].Name != "Tasks" || len(ph[0].Tasks) != 2 {
		t.Fatalf("empty init phase rename failed: %+v", ph)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "rejects normalized duplicate phases without replacing existing state"
	res, _ = h3.callTool("todo", map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "", "items": []any{"A"}},
			map[string]any{"phase": "Tasks", "items": []any{"B"}},
		},
	})
	if !resIsErr(res) || !strings.Contains(resSummary(res), `Duplicate phase "Tasks"`) {
		t.Fatalf("expected duplicate phase error, got %q", resSummary(res))
	}
	if decodePhasesFromRes(t, res)[0].Tasks[0].Content != "One" {
		t.Fatalf("existing state was overwritten on duplicate phase error")
	}

	// PrepareArguments infers op before schema validation
	prep, errMsg := h3.prepareArgs("todo", map[string]any{"list": []any{map[string]any{"phase": "P", "items": []any{"x"}}}})
	if errMsg != "" || prep["op"] != "init" {
		t.Fatalf("prepareArgs failed: err=%q prep=%+v", errMsg, prep)
	}
}

func TestMarkdownRoundTripAndRetiredMarker(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/todo.test.ts: "rejects the retired [!] checklist marker from /todo edit"
	parsed, errs := MarkdownToPhases("# Work\n- [!] a\n- [ ] b\n")
	if len(errs) != 1 || errs[0] != `Line 2: unknown status marker "[!]" (use [ ], [x], [/], [-])` {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(parsed) != 1 || len(parsed[0].Tasks) != 1 || parsed[0].Tasks[0].Content != "b" {
		t.Fatalf("unexpected parsed: %+v", parsed)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "parses checklist items with backslash-escaped brackets from /todo edit"
	escaped := "# Todos\n* \\[x] first\n- \\[ \\] second\n+ \\[/\\] third"
	parsed, errs = MarkdownToPhases(escaped)
	if len(errs) != 0 || len(parsed[0].Tasks) != 3 {
		t.Fatalf("escaped brackets failed: errs=%v parsed=%+v", errs, parsed)
	}
	if parsed[0].Tasks[0].Status != StatusCompleted || parsed[0].Tasks[1].Status != StatusPending || parsed[0].Tasks[2].Status != StatusInProgress {
		t.Fatalf("unexpected statuses: %+v", parsed[0].Tasks)
	}

	// Empty markdown returns non-nil empty slice so JSON marshals as [] not null (B4)
	emptyPhases, emptyErrs := MarkdownToPhases("")
	if len(emptyErrs) != 0 || emptyPhases == nil {
		t.Fatalf("expected non-nil empty slice, got phases=%#v errs=%v", emptyPhases, emptyErrs)
	}
	raw, _ := json.Marshal(emptyPhases)
	if string(raw) != "[]" {
		t.Fatalf("empty phases marshaled as %s, want []", string(raw))
	}
}

func TestTodoReminderLoopAndGuards(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	settings := map[string]any{}
	Setting = func(_ sdk.Context, key string) any { return settings[key] }

	textStop := func(text string) map[string]any {
		return map[string]any{
			"outcome": "completed",
			"entries": []any{},
			"context": map[string]any{
				"llmMessages": []any{
					map[string]any{
						"role":       "assistant",
						"stopReason": "stop",
						"content":    []any{map[string]any{"type": "text", "text": text}},
					},
				},
			},
		}
	}

	// upstream: packages/coding-agent/test/agent-session-todo-reminder-loop.test.ts: "fires exactly one reminder per user pause when the agent only acknowledges"
	h := startTodoHost(t, hostOptions{})
	beforeStartPayload := map[string]any{"systemPrompt": "", "systemPromptOptions": map[string]any{}}
	_, _ = h.callTool("todo", map[string]any{"op": "init", "items": []any{"Finish task"}})
	if _, errMsg := h.emitEvent(sdk.EventBeforeAgentStart, beforeStartPayload); errMsg != "" {
		t.Fatalf("before_agent_start error: %s", errMsg)
	}
	r1, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("I paused."))
	b1 := boundaryResult(r1)
	if b1 == nil || b1["continue"] != true {
		t.Fatalf("expected reminder 1/3 to continue, got %+v", r1)
	}
	entries1, _ := b1["entries"].([]any)
	msg1, _ := entries1[0].(map[string]any)["content"].(string)
	if !strings.Contains(msg1, "(Reminder 1/3)") {
		t.Fatalf("expected (Reminder 1/3), got %q", msg1)
	}
	// Second text-only stop without any tool progress MUST be suppressed (not loop!)
	r2, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Okay, acknowledged."))
	if boundaryResult(r2) != nil {
		t.Fatalf("expected second ack stop to be suppressed, got %+v", r2)
	}

	// upstream: packages/coding-agent/test/agent-session-todo-reminder-loop.test.ts: "re-escalates after the agent makes tool-level progress between stops"
	_, _ = h.emitEvent(sdk.EventToolResult, map[string]any{"toolName": "todo", "isError": false})
	r3, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Did some work, stopping."))
	b3 := boundaryResult(r3)
	if b3 == nil || b3["continue"] != true {
		t.Fatalf("expected reminder 2/3 after tool progress, got %+v", r3)
	}
	entries3, _ := b3["entries"].([]any)
	if !strings.Contains(entries3[0].(map[string]any)["content"].(string), "(Reminder 2/3)") {
		t.Fatalf("expected (Reminder 2/3), got %+v", entries3[0])
	}
	// Tool progress -> 3/3 -> tool progress -> capped at 3/3!
	_, _ = h.emitEvent(sdk.EventToolResult, map[string]any{"toolName": "edit", "isError": false})
	r4, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Still going."))
	b4 := boundaryResult(r4)
	if b4 == nil || b4["continue"] != true {
		t.Fatalf("expected reminder 3/3, got %+v", r4)
	}
	_, _ = h.emitEvent(sdk.EventToolResult, map[string]any{"toolName": "edit", "isError": false})
	r5, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Done trying."))
	if boundaryResult(r5) != nil {
		t.Fatalf("expected cap at 3 reminders (B1), got %+v", r5)
	}

	// Reset on before_agent_start restores budget
	if _, errMsg := h.emitEvent(sdk.EventBeforeAgentStart, beforeStartPayload); errMsg != "" {
		t.Fatalf("before_agent_start reset error: %s", errMsg)
	}
	// Error outcome / tool calls stay silent (A4)
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, map[string]any{"outcome": "error"}); boundaryResult(r) != nil {
		t.Fatalf("expected silent on error outcome, got %+v", r)
	}
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, map[string]any{
		"outcome": "completed",
		"context": map[string]any{
			"llmMessages": []any{
				map[string]any{
					"role":    "assistant",
					"content": []any{map[string]any{"type": "toolCall", "name": "bash"}},
				},
			},
		},
	}); boundaryResult(r) != nil {
		t.Fatalf("expected silent when assistant message has toolCall, got %+v", r)
	}

	// upstream: packages/coding-agent/test/agent-session-todo-reminder-loop.test.ts: "does not remind or continue when the assistant yields with a user-facing question"
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("I need your feedback before continuing. Which trade-off should I optimize for?")); boundaryResult(r) != nil {
		t.Fatalf("expected silent when awaiting user answer, got %+v", r)
	}
	// upstream: packages/coding-agent/test/agent-session-todo-reminder-loop.test.ts: "still reminds when the assistant strikes through its own question"
	if IsAwaitingUserAnswer("~~What should I do next?~~") {
		t.Fatalf("IsAwaitingUserAnswer unexpectedly true for struck-through question")
	}
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("~~What should I do next?~~")); boundaryResult(r) == nil || boundaryResult(r)["continue"] != true {
		t.Fatalf("expected reminder on struck-through question, got %+v", r)
	}
	_, _ = h.emitEvent(sdk.EventBeforeAgentStart, beforeStartPayload)
	// upstream: packages/coding-agent/test/agent-session-todo-reminder-loop.test.ts: "stays silent when todo is not in the active roster"
	h.setActiveTools([]string{"bash"})
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Stopped.")); boundaryResult(r) != nil {
		t.Fatalf("expected silent when todo not active, got %+v", r)
	}
	h.setActiveTools([]string{"todo"})

	// Respects todo.reminders=false and todo.enabled=false via Setting
	settings["todo.reminders"] = false
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Stopped.")); boundaryResult(r) != nil {
		t.Fatalf("expected silent when todo.reminders=false, got %+v", r)
	}
	delete(settings, "todo.reminders")
	settings["todo.enabled"] = false
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, textStop("Stopped.")); boundaryResult(r) != nil {
		t.Fatalf("expected silent when todo.enabled=false, got %+v", r)
	}
	delete(settings, "todo.enabled")

	// upstream: packages/coding-agent/test/print-mode-todo-reminder.test.ts: "prints the requested answer without continuing stale todos in print mode"
	hp := startTodoHost(t, hostOptions{Mode: "print"})
	_, _ = hp.callTool("todo", map[string]any{"op": "init", "items": []any{"Await access"}})
	if r, _ := hp.emitEvent(sdk.EventAgentBeforeSettle, textStop("PONG")); boundaryResult(r) != nil {
		t.Fatalf("expected silent in print mode, got %+v", r)
	}
	hj := startTodoHost(t, hostOptions{Mode: "json"})
	_, _ = hj.callTool("todo", map[string]any{"op": "init", "items": []any{"Await access"}})
	if r, _ := hj.emitEvent(sdk.EventAgentBeforeSettle, textStop("PONG")); boundaryResult(r) != nil {
		t.Fatalf("expected silent in json mode, got %+v", r)
	}
}
func TestTodoCommandControllerViaRealHandler(t *testing.T) {
	cwd := t.TempDir()
	h := startTodoHost(t, hostOptions{Cwd: cwd})
	_, _ = h.callTool("todo", map[string]any{"op": "init", "list": []any{map[string]any{"phase": "Work", "items": []any{"Ship it"}}}})

	// upstream: packages/coding-agent/test/modes/controllers/todo-command-controller.test.ts: "exports a quoted path with spaces"
	target := filepath.Join(cwd, "todo file.md")
	if errMsg := h.runCommand("todo", `export "`+target+`"`); errMsg != "" {
		t.Fatalf("export error: %s", errMsg)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "# Work\n- [/] Ship it\n" {
		t.Fatalf("exported content = %q err=%v", string(raw), err)
	}

	// upstream: packages/coding-agent/test/modes/controllers/todo-command-controller.test.ts: "reports import parse errors without committing"
	badFile := filepath.Join(cwd, "bad.md")
	_ = os.WriteFile(badFile, []byte("# Imported\nnot a todo\n"), 0o644)
	_ = h.runCommand("todo", "import bad.md")
	res, _ := h.callTool("todo", map[string]any{"op": "view"})
	if !strings.Contains(resSummary(res), "Ship it") {
		t.Fatalf("parse error should not overwrite existing todos: %s", resSummary(res))
	}

	// upstream: packages/coding-agent/test/modes/controllers/todo-command-controller.test.ts: "tells the model not to recreate the list after /todo rm (all)"
	_ = h.runCommand("todo", "rm")
	msgs := h.callsTo("sendMessage")
	if len(msgs) == 0 {
		t.Fatalf("expected sendMessage after /todo rm")
	}
	msgArgs := msgs[len(msgs)-1].Args
	lastMsg, _ := msgArgs["content"].(string)
	if lastMsg == "" {
		if inner, ok := msgArgs["message"].(map[string]any); ok {
			lastMsg, _ = inner["content"].(string)
		}
	}
	if !strings.Contains(lastMsg, "intentionally cleared the todo list") || !strings.Contains(lastMsg, "Do NOT recreate") {
		t.Fatalf("unexpected rm-all reminder: %q (args=%+v)", lastMsg, msgArgs)
	}

	// /todo copy calls clipboard
	_, _ = h.callTool("todo", map[string]any{"op": "init", "items": []any{"Copy me"}})
	_ = h.runCommand("todo", "copy")
	if len(h.callsTo("copyToClipboard")) == 0 && len(h.callsTo("ui.copyToClipboard")) == 0 {
		// Check any clipboard call recorded
		found := false
		for _, c := range h.calls {
			if strings.Contains(strings.ToLower(c.Method), "clipboard") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected clipboard call from /todo copy, calls=%+v", h.calls)
		}
	}
}

func TestSelectCollapsedTodosWalkingViewport(t *testing.T) {
	mk := func(n int, inProg []int) []TodoItem {
		inSet := map[int]bool{}
		for _, i := range inProg {
			inSet[i] = true
		}
		out := make([]TodoItem, n)
		for i := range n {
			st := StatusPending
			if inSet[i+1] {
				st = StatusInProgress
			}
			out[i] = TodoItem{Content: "Task " + string(rune('0'+i+1)), Status: st}
		}
		return out
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "leads with the last closed task and omits the rest in collapsed mode"
	sel := SelectCollapsedTodos([]TodoItem{
		{Content: "done", Status: StatusCompleted},
		{Content: "dropped", Status: StatusAbandoned},
		{Content: "current", Status: StatusInProgress},
		{Content: "next", Status: StatusPending},
	}, 5)
	if len(sel.Items) != 3 || sel.Items[0].Content != "dropped" || sel.Items[1].Content != "current" || sel.Items[2].Content != "next" || sel.Summary != "" {
		t.Fatalf("unexpected selection: %+v", sel)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "keeps the closed lead row additive to the open-task cap"
	tasks := append([]TodoItem{{Content: "closed", Status: StatusCompleted}}, mk(5, []int{1})...)
	sel = SelectCollapsedTodos(tasks, 5)
	if len(sel.Items) != 6 || sel.Items[0].Content != "closed" || sel.Summary != "" {
		t.Fatalf("closed lead row should be additive: %+v", sel)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "keeps a summary when actives exactly fill the cap but pending remains"
	matched := func(item TodoItem) bool {
		return item.Content <= "Task 5"
	}
	sel = SelectCollapsedTodosMatched(mk(7, nil), matched, 5)
	if len(sel.Items) != 5 || sel.Summary != "… 2 more todos" {
		t.Fatalf("unexpected cap+pending selection: %+v", sel)
	}

	// upstream: packages/coding-agent/test/tools/todo.test.ts: "renders a lone hidden todo in place of a one-item summary row"
	sel = SelectCollapsedTodos(mk(6, []int{1}), 5)
	if len(sel.Items) != 6 || sel.Summary != "" {
		t.Fatalf("lone hidden todo should render directly: %+v", sel)
	}

	// HUD renders no "(dropped)" suffix (A11)
	lines := RenderWidgetLines([]TodoPhase{{
		Name: "Foundation",
		Tasks: []TodoItem{
			{Content: "done item", Status: StatusCompleted},
			{Content: "active item", Status: StatusInProgress},
			{Content: "dropped item", Status: StatusAbandoned},
		},
	}}, true)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "(dropped)") {
		t.Errorf("HUD should not show (dropped) suffix:\n%s", joined)
	}
	if !strings.Contains(joined, "☑ done item") || !strings.Contains(joined, "☐ dropped item") {
		t.Errorf("HUD missing expected rows:\n%s", joined)
	}
}

func TestReplayLegacyBlockedNormalizedToPending(t *testing.T) {
	branch := []map[string]any{
		{
			"type":       "custom",
			"customType": UserTodoEditCustomType,
			"data": map[string]any{
				"phases": []any{
					map[string]any{
						"name": "Phase 1",
						"tasks": []any{
							map[string]any{"content": "Step A", "status": "completed"},
							map[string]any{"content": "Step B", "status": "blocked", "blocker": "waiting"},
						},
					},
				},
			},
		},
	}
	h := startTodoHost(t, hostOptions{Branch: branch})
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)
	res, _ := h.callTool("todo", map[string]any{"op": "view"})
	ph := decodePhasesFromRes(t, res)
	if len(ph) != 1 || ph[0].Tasks[0].Status != StatusCompleted || ph[0].Tasks[1].Status != StatusPending {
		t.Fatalf("unexpected replayed phases: %+v", ph)
	}
}

func TestMultiSessionLifecycleAndDoubleRegister(t *testing.T) {
	// Two separate Register() calls in one process, two live sessions; lifecycle.End
	// for one session (called twice) and unknown ID must leave the other session intact.
	hA := startTodoHost(t, hostOptions{SessionID: "sess-lifecycle-A"})
	hB := startTodoHost(t, hostOptions{SessionID: "sess-lifecycle-B"})

	_, _ = hA.callTool("todo", map[string]any{"op": "init", "items": []any{"Task A"}})
	_, _ = hB.callTool("todo", map[string]any{"op": "init", "items": []any{"Task B"}})

	lifecycle.End("sess-lifecycle-A")
	lifecycle.End("sess-lifecycle-A")
	lifecycle.End("sess-unknown")
	lifecycle.End("")

	resA, _ := hA.callTool("todo", map[string]any{"op": "view"})
	if resSummary(resA) != "Todo list is empty." {
		t.Fatalf("expected session A cleared after lifecycle.End, got %q", resSummary(resA))
	}

	resB, errMsg := hB.callTool("todo", map[string]any{"op": "view"})
	if errMsg != "" || !strings.Contains(resSummary(resB), "Task B") || strings.Contains(resSummary(resB), "Task A") {
		t.Fatalf("session B corrupted after session A ended: err=%q res=%+v", errMsg, resB)
	}
}

func TestTodoSchemaMatchesOmpContent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "schemaorder", "omp_schemas.json"))
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	var captured map[string]struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("unmarshal omp_schemas.json: %v", err)
	}
	want := captured["todo"].Parameters
	if want == nil {
		t.Fatalf("omp_schemas.json missing todo entry")
	}
	gotRaw, err := json.Marshal(toolSchema())
	if err != nil {
		t.Fatalf("marshal toolSchema: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("unmarshal toolSchema: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("todo schema drift:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestWidgetWidthWrappingAndClamping(t *testing.T) {
	// upstream: packages/coding-agent/test/interactive-mode-todo-clear.test.ts: "wraps a row wider than the panel beneath its own content instead of the panel edge"
	phases := []TodoPhase{
		{
			Name: "A stage whose name is far too long to fit on one sixty column row, stage-end",
			Tasks: []TodoItem{
				{Content: "finished", Status: StatusCompleted},
				{Content: "a branch task described at such length that it has to wrap, branch-end", Status: StatusInProgress},
				{Content: "the last task, also described at such length that it wraps, last-end", Status: StatusPending},
			},
		},
	}
	rows60 := RenderWidgetLinesForWidth(phases, false, 60)
	for i, r := range rows60 {
		if w := sdktui.VisibleWidth(r); w > 60 {
			t.Fatalf("width=60 row %d exceeds 60 cols (%d): %q", i, w, r)
		}
		// Right padding column (hudPaddingX=1) must stay clear when wrapping fits inside width-2.
		if w := sdktui.VisibleWidth(r); w > 59 {
			t.Fatalf("width=60 row %d encroached on right padding (%d > 59): %q", i, w, r)
		}
	}
	foundStageCont := false
	foundBranchCont := false
	foundLastCont := false
	for _, r := range rows60 {
		if strings.HasPrefix(r, "  │  ") && strings.Contains(r, "stage-end") {
			foundStageCont = true
		}
		if strings.HasPrefix(r, "  │  │  ") && strings.Contains(r, "branch-end") {
			foundBranchCont = true
		}
		if strings.HasPrefix(r, "  │     ") && strings.Contains(r, "last-end") {
			foundLastCont = true
		}
	}
	if !foundStageCont {
		t.Errorf("expected stage continuation to hang under padded spine (\"  │  \"):\n%s", strings.Join(rows60, "\n"))
	}
	if !foundBranchCont {
		t.Errorf("expected branch task continuation to hang under padded spine+rail (\"  │  │  \"):\n%s", strings.Join(rows60, "\n"))
	}
	if !foundLastCont {
		t.Errorf("expected last task continuation to hang under padded spine (\"  │     \"):\n%s", strings.Join(rows60, "\n"))
	}

	// Check 80-column and 60-column wrapping respects left+right padding budget (width - 2).
	for _, width := range []int{60, 80} {
		// Content of length width-2-len(" │  └─ ☑ ") fits on one line; +1 char wraps onto a second line.
		prefixW := sdktui.VisibleWidth(" │  └─ ")
		contentBudget := width - 2 - prefixW
		exactText := strings.Repeat("x", contentBudget-sdktui.VisibleWidth("☐ "))
		overText := exactText + "y"
		fitRows := RenderWidgetLinesForWidth([]TodoPhase{{Name: "P", Tasks: []TodoItem{{Content: exactText, Status: StatusInProgress}}}}, false, width)
		if len(fitRows) != 4 {
			t.Fatalf("width=%d exact-fit task should stay on 1 row (4 total), got %d:\n%s", width, len(fitRows), strings.Join(fitRows, "\n"))
		}
		if gotW := sdktui.VisibleWidth(fitRows[2]); gotW != width-1 {
			t.Fatalf("width=%d exact-fit row width = %d, want %d (1 left pad + contentWidth, leaving 1 right pad)", width, gotW, width-1)
		}
		wrapRows := RenderWidgetLinesForWidth([]TodoPhase{{Name: "P", Tasks: []TodoItem{{Content: overText, Status: StatusInProgress}}}}, false, width)
		if len(wrapRows) != 5 {
			t.Fatalf("width=%d +1-char task should wrap to 2 rows (5 total), got %d:\n%s", width, len(wrapRows), strings.Join(wrapRows, "\n"))
		}
		if !strings.HasPrefix(wrapRows[3], "  │     ") {
			t.Fatalf("width=%d wrapped continuation = %q, want prefix %q", width, wrapRows[3], "  │     ")
		}
	}

	longPhases := []TodoPhase{
		{
			Name: "日本語ステージ 🚀 " + strings.Repeat("a", 50),
			Tasks: []TodoItem{
				{Content: "中文测试🎉 " + strings.Repeat("word ", 60) + "tail", Status: StatusInProgress},
			},
		},
	}
	for _, width := range []int{1, 2, 20, 60, 80} {
		lines := RenderWidgetLinesForWidth(longPhases, true, width)
		if len(lines) == 0 {
			t.Fatalf("width=%d produced no lines", width)
		}
		for i, line := range lines {
			if strings.ContainsAny(line, "\r\n") {
				t.Fatalf("width=%d line %d contains CR/LF: %q", width, i, line)
			}
			if w := sdktui.VisibleWidth(line); w > width {
				t.Fatalf("width=%d line %d exceeds width (%d > %d): %q", width, i, w, width, line)
			}
		}
	}
}

// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5432-5618
// Exact lines verified against real omp 19.1.1 plain-terminal capture (/tmp/omp-proof11-ansi/omp.screen.txt).
func TestWidgetMatchesOmpCaptureAndHudRules(t *testing.T) {
	verifyPhases := []TodoPhase{
		{
			Name: "Verify",
			Tasks: []TodoItem{
				{Content: "Check read card", Status: StatusCompleted},
				{Content: "Check todo card", Status: StatusCompleted},
			},
		},
	}
	wantCapture := []string{
		" TODO",
		"  ├─ Verify · 2/2",
		"  │  ├─ ☑ Check read card",
		"  │  └─ ☑ Check todo card",
		"  └─────",
	}
	if got := RenderWidgetLines(verifyPhases, false); !reflect.DeepEqual(got, wantCapture) {
		t.Fatalf("RenderWidgetLines mismatch:\ngot:  %#v\nwant: %#v", got, wantCapture)
	}
	for _, width := range []int{60, 80, 120} {
		if got := RenderWidgetLinesForWidth(verifyPhases, false, width); !reflect.DeepEqual(got, wantCapture) {
			t.Fatalf("RenderWidgetLinesForWidth(%d) mismatch:\ngot:  %#v\nwant: %#v", width, got, wantCapture)
		}
	}

	// Empty phases hide the HUD (returns nil).
	if got := RenderWidgetLines(nil, false); got != nil {
		t.Fatalf("nil phases should return nil, got %#v", got)
	}
	if got := RenderWidgetLinesForWidth([]TodoPhase{{Name: "Empty"}}, false, 80); got != nil {
		t.Fatalf("empty-task phase should return nil, got %#v", got)
	}

	// Settled multi-phase list falls back to the LAST non-empty phase (interactive-mode.ts:5437).
	settledMulti := []TodoPhase{
		{Name: "One", Tasks: []TodoItem{{Content: "t1", Status: StatusCompleted}}},
		{Name: "Two", Tasks: []TodoItem{{Content: "t2", Status: StatusAbandoned}}},
	}
	wantSettled := []string{
		" TODO",
		"  ├─ II. Two · 1/1",
		"  │  └─ ☐ t2",
		"  └─────",
	}
	if got := RenderWidgetLinesForWidth(settledMulti, false, 80); !reflect.DeepEqual(got, wantSettled) {
		t.Fatalf("settled multi-phase HUD mismatch:\ngot:  %#v\nwant: %#v", got, wantSettled)
	}

	// Stage cap (active + 4 subsequent stages) and "… N more stages" row (interactive-mode.ts:5483,5539-5563).
	var manyStages []TodoPhase
	for _, name := range []string{"One", "Two", "Three", "Four", "Five", "Six", "Seven"} {
		manyStages = append(manyStages, TodoPhase{Name: name, Tasks: []TodoItem{{Content: name + " task", Status: StatusPending}}})
	}
	gotStages := RenderWidgetLinesForWidth(manyStages, false, 80)
	wantStages := []string{
		" TODO",
		"  ├─ I. One · 0/1",
		"  │  └─ ☐ One task",
		"  ├─ II. Two · 0/1",
		"  ├─ III. Three · 0/1",
		"  ├─ IV. Four · 0/1",
		"  ├─ V. Five · 0/1",
		"  ├─ … 2 more stages",
		"  └─────",
	}
	if !reflect.DeepEqual(gotStages, wantStages) {
		t.Fatalf("stage-capped HUD mismatch:\ngot:\n%s\nwant:\n%s", strings.Join(gotStages, "\n"), strings.Join(wantStages, "\n"))
	}

	// Task cap (5 open tasks + "… N more todos" row, with 1 closed lead context).
	manyTasks := []TodoPhase{
		{
			Name: "Impl",
			Tasks: []TodoItem{
				{Content: "done-1", Status: StatusCompleted},
				{Content: "done-2", Status: StatusCompleted},
				{Content: "open-1", Status: StatusInProgress},
				{Content: "open-2", Status: StatusPending},
				{Content: "open-3", Status: StatusPending},
				{Content: "open-4", Status: StatusPending},
				{Content: "open-5", Status: StatusPending},
				{Content: "open-6", Status: StatusPending},
				{Content: "open-7", Status: StatusPending},
			},
		},
	}
	gotTasks := RenderWidgetLinesForWidth(manyTasks, false, 80)
	wantTasks := []string{
		" TODO",
		"  ├─ Impl · 2/9",
		"  │  ├─ ☑ done-2",
		"  │  ├─ ☐ open-1",
		"  │  ├─ ☐ open-2",
		"  │  ├─ ☐ open-3",
		"  │  ├─ ☐ open-4",
		"  │  ├─ ☐ open-5",
		"  │  └─ … 2 more todos",
		"  └─────",
	}
	if !reflect.DeepEqual(gotTasks, wantTasks) {
		t.Fatalf("task-capped HUD mismatch:\ngot:\n%s\nwant:\n%s", strings.Join(gotTasks, "\n"), strings.Join(wantTasks, "\n"))
	}
}

func TestTodoPlanModeAndMidRunNudgeGuards(t *testing.T) {
	t.Cleanup(gate.ResetForTests)

	h := startTodoHost(t, hostOptions{})
	_, _ = h.callTool("todo", map[string]any{"op": "init", "items": []any{"Task 1"}})

	// N2: plan mode suppresses stop reminder
	gate.SetPlanActive(func(_ sdk.Context) bool { return true })
	stopPayload := map[string]any{
		"outcome": "completed",
		"entries": []any{},
		"context": map[string]any{
			"llmMessages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Done."}}}},
		},
	}
	if r, _ := h.emitEvent(sdk.EventAgentBeforeSettle, stopPayload); boundaryResult(r) != nil {
		t.Fatalf("expected stop reminder suppressed in plan mode, got %+v", r)
	}

	// Accumulate 12 mutating tool results
	for range 12 {
		_, _ = h.emitEvent(sdk.EventToolResult, map[string]any{"toolName": "edit", "isError": false})
	}
	// N2: plan mode suppresses mid-run nudge
	if r, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{"toolResults": []any{map[string]any{"toolName": "edit"}}, "entries": []any{}}); boundaryResult(r) != nil {
		t.Fatalf("expected mid-run nudge suppressed in plan mode, got %+v", r)
	}

	gate.SetPlanActive(func(_ sdk.Context) bool { return false })

	// N4: turn_end with no toolResults (final text-only stop) must NOT emit a mid-run nudge
	if r, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{"toolResults": []any{}, "entries": []any{}}); boundaryResult(r) != nil {
		t.Fatalf("expected no mid-run nudge when turn has no toolResults, got %+v", r)
	}
	// turn_end WITH toolResults DOES emit the mid-run nudge
	rNudge, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{"toolResults": []any{map[string]any{"toolName": "edit"}}, "entries": []any{}})
	bn := boundaryResult(rNudge)
	if bn == nil {
		t.Fatalf("expected mid-run nudge on tool-result turn, got %+v", rNudge)
	}
	entries, _ := bn["entries"].([]any)
	if len(entries) != 1 || !strings.Contains(entries[0].(map[string]any)["content"].(string), "1 todo item still open") {
		t.Fatalf("unexpected mid-run nudge entries: %+v", entries)
	}
}

func TestTodoEditPrefillAndMessages(t *testing.T) {
	var gotPrefill string
	editorReply := map[string]any{"ok": false}
	h := startTodoHost(t, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.editor" {
				gotPrefill, _ = args["prefill"].(string)
				return editorReply, ""
			}
			return nil, ""
		},
	})
	// Empty list prefill + cancel warning
	_ = h.runCommand("todo", "edit")
	if gotPrefill != "# Todos\n- [ ] (replace this with your tasks)\n" {
		t.Fatalf("empty /todo edit prefill = %q", gotPrefill)
	}
	notifs := h.callsTo("ui.notify")
	if len(notifs) == 0 || notifs[len(notifs)-1].Args["message"] != "Editor exited without saving; todos unchanged." {
		t.Fatalf("expected cancel warning, got %+v", notifs)
	}

	// Parse error message
	editorReply = map[string]any{"ok": true, "text": "# Todos\nnot valid\n"}
	_ = h.runCommand("todo", "edit")
	notifs = h.callsTo("ui.notify")
	if msg, _ := notifs[len(notifs)-1].Args["message"].(string); !strings.HasPrefix(msg, "Could not parse Markdown:\n  ") {
		t.Fatalf("unexpected parse error notification: %q", msg)
	}

	// Success message
	editorReply = map[string]any{"ok": true, "text": "# Work\n- [ ] Task A\n- [ ] Task B\n"}
	_ = h.runCommand("todo", "edit")
	notifs = h.callsTo("ui.notify")
	if msg, _ := notifs[len(notifs)-1].Args["message"].(string); msg != "Todos updated from editor: 1 phase(s), 2 task(s)." {
		t.Fatalf("unexpected success notification: %q", msg)
	}
}

// upstream: packages/coding-agent/src/tools/todo.ts:496-507,696
func TestTodoPrepareArgumentsLenientValidation(t *testing.T) {
	h := startTodoHost(t, hostOptions{})

	cases := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "invalid string op keeps quotes",
			args:    map[string]any{"op": "bogus"},
			wantErr: `Invalid todo arguments: op must be "init", "start", "done", "rm", "drop", "append" or "view" (was "bogus")`,
		},
		{
			name:    "numeric op",
			args:    map[string]any{"op": float64(42)},
			wantErr: `Invalid todo arguments: op must be "init", "start", "done", "rm", "drop", "append" or "view" (was 42)`,
		},
		{
			name:    "missing op without inferable payload",
			args:    map[string]any{"task": "foo"},
			wantErr: `Invalid todo arguments: op must be "init", "start", "done", "rm", "drop", "append" or "view" (was missing)`,
		},
		{
			name:    "empty list entry items",
			args:    map[string]any{"op": "init", "list": []any{map[string]any{"phase": "P", "items": []any{}}}},
			wantErr: `Invalid todo arguments: list[0].items must be at least length 1 (was 0)`,
		},
		{
			name:    "list entry missing phase",
			args:    map[string]any{"op": "init", "list": []any{map[string]any{"items": []any{"a"}}}},
			wantErr: `Invalid todo arguments: list[0].phase must be a string (was missing)`,
		},
		{
			name:    "list entry missing items",
			args:    map[string]any{"op": "init", "list": []any{map[string]any{"phase": "P"}}},
			wantErr: `Invalid todo arguments: list[0].items must be an array (was missing)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errMsg := h.prepareArgs("todo", tc.args)
			if errMsg != tc.wantErr {
				t.Fatalf("prepareArgs(%v) = %q, want %q", tc.args, errMsg, tc.wantErr)
			}
		})
	}

	// Direct ResolveTodoParams checks for raw type errors (bypassing omp's pre-coercion).
	if _, errMsg := ResolveTodoParams(map[string]any{"op": "done", "task": float64(123)}, false); errMsg != `Invalid todo arguments: task must be a string or an array (was a number)` {
		t.Fatalf("ResolveTodoParams wrong task type = %q", errMsg)
	}
	if _, errMsg := ResolveTodoParams(map[string]any{"op": "append", "items": float64(123)}, false); errMsg != `Invalid todo arguments: items must be an array (was a number)` {
		t.Fatalf("ResolveTodoParams wrong items type = %q", errMsg)
	}
}

// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5676-5703
func TestNativeTodoHudWidgetViewAndSurfaceSwitch(t *testing.T) {
	surface.ResetForTests()
	resetSurfaceListener()
	t.Cleanup(func() {
		surface.ResetForTests()
		resetSurfaceListener()
	})

	// 1. Unit check on BuildNativeWidgetView: empty -> false, single phase -> plain title,
	// multi-phase -> Roman numeral title, all statuses + last note preserved even when all closed.
	if _, ok := BuildNativeWidgetView(nil); ok {
		t.Fatalf("nil phases must not produce a native HUD view")
	}
	if _, ok := BuildNativeWidgetView([]TodoPhase{{Name: "Empty"}}); ok {
		t.Fatalf("empty-task phases must not produce a native HUD view")
	}

	singlePhases := []TodoPhase{
		{
			Name: "Verify",
			Tasks: []TodoItem{
				{Content: "Check read card", Status: StatusCompleted},
				{Content: "Check todo card", Status: StatusCompleted},
			},
		},
	}
	singleView, ok := BuildNativeWidgetView(singlePhases)
	if !ok {
		t.Fatalf("all-completed phases must stay mounted on native surface")
	}
	root, ok := singleView.Root.(*kit.VStack)
	if !ok || root.ID != "omp.hud.todo" || root.Gap != 0 || len(root.Children) != 1 {
		t.Fatalf("unexpected root: %#v", singleView.Root)
	}
	p0, ok := root.Children[0].(*kit.VStack)
	if !ok || p0.ID != "omp.todo.phase#0" || p0.Gap != 0 || len(p0.Children) != 5 {
		t.Fatalf("unexpected phase 0: %#v", root.Children[0])
	}
	head0 := p0.Children[0].(*kit.Text)
	if head0.ID != "omp.todo.phase.head#0" || head0.Text != "Verify" {
		t.Fatalf("phase 0 head = %+v, want plain title Verify", head0)
	}
	item0 := p0.Children[1].(*kit.Text)
	st0 := p0.Children[2].(*kit.Text)
	item1 := p0.Children[3].(*kit.Text)
	st1 := p0.Children[4].(*kit.Text)
	if item0.ID != "omp.todo.item#0.0" || item0.Text != "☑ Check read card" || st0.ID != "omp.todo.item.status#0.0" || st0.Text != "done" {
		t.Fatalf("item 0 mismatch: item=%+v status=%+v", item0, st0)
	}
	if item1.ID != "omp.todo.item#0.1" || item1.Text != "☑ Check todo card" || st1.ID != "omp.todo.item.status#0.1" || st1.Text != "done" {
		t.Fatalf("item 1 mismatch: item=%+v status=%+v", item1, st1)
	}

	multiPhases := []TodoPhase{
		{
			Name: "Foundation",
			Tasks: []TodoItem{
				{Content: "done task", Status: StatusCompleted, Notes: []string{"old note", "final note"}},
				{Content: "active task", Status: StatusInProgress},
			},
		},
		{
			Name: "Cleanup",
			Tasks: []TodoItem{
				{Content: "dropped task", Status: StatusAbandoned},
				{Content: "queued task", Status: StatusPending},
			},
		},
	}
	multiView, ok := BuildNativeWidgetView(multiPhases)
	if !ok {
		t.Fatalf("expected multi-phase native view")
	}
	mRoot := multiView.Root.(*kit.VStack)
	if len(mRoot.Children) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(mRoot.Children))
	}
	mp0 := mRoot.Children[0].(*kit.VStack)
	if mp0.Children[0].(*kit.Text).Text != "I. Foundation" {
		t.Fatalf("phase 0 title = %q, want %q", mp0.Children[0].(*kit.Text).Text, "I. Foundation")
	}
	note0 := mp0.Children[3].(*kit.Text)
	if note0.ID != "omp.todo.item.note#0.0" || note0.Text != "final note" {
		t.Fatalf("expected last note node, got %+v", note0)
	}
	if mp0.Children[4].(*kit.Text).Text != "◐ active task" || mp0.Children[5].(*kit.Text).Text != "active" {
		t.Fatalf("active task mismatch: %+v %+v", mp0.Children[4], mp0.Children[5])
	}
	mp1 := mRoot.Children[1].(*kit.VStack)
	if mp1.ID != "omp.todo.phase#1" || mp1.Children[0].(*kit.Text).ID != "omp.todo.phase.head#1" || mp1.Children[0].(*kit.Text).Text != "II. Cleanup" {
		t.Fatalf("phase 1 head mismatch: %+v", mp1)
	}
	if mp1.Children[1].(*kit.Text).Text != "☒ dropped task" || mp1.Children[2].(*kit.Text).Text != "dropped" {
		t.Fatalf("dropped task mismatch: %+v %+v", mp1.Children[1], mp1.Children[2])
	}
	if mp1.Children[3].(*kit.Text).Text != "☐ queued task" || mp1.Children[4].(*kit.Text).Text != "pending" {
		t.Fatalf("pending task mismatch: %+v %+v", mp1.Children[3], mp1.Children[4])
	}

	// 2. End-to-end wire check on native surface vs terminal surface.
	surface.SetNative(true)
	hNative := startTodoHost(t, hostOptions{Mode: "tui"})
	_, errMsg := hNative.callTool("todo", map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "Plan", "items": []any{"First", "Second"}},
			map[string]any{"phase": "Verify", "items": []any{"Check"}},
		},
	})
	if errMsg != "" {
		t.Fatalf("todo init failed: %s", errMsg)
	}
	pushes := hNative.callsTo("widget_push")
	if len(pushes) == 0 {
		t.Fatalf("expected widget_push carrying kit.View on native surface, calls=%+v", hNative.calls)
	}
	lastPush := pushes[len(pushes)-1].Args
	if lastPush["key"] != WidgetKey {
		t.Fatalf("widget_push key = %v, want %q", lastPush["key"], WidgetKey)
	}
	viewRaw, _ := json.Marshal(lastPush["view"])
	viewStr := string(viewRaw)
	for _, want := range []string{`"omp.hud.todo"`, `"omp.todo.phase#0"`, `"omp.todo.phase.head#0"`, `"I. Plan"`, `"omp.todo.item#0.0"`, `"omp.todo.item.status#0.0"`, `"active"`, `"omp.todo.phase#1"`, `"II. Verify"`, `"omp.todo.item#1.0"`} {
		if !strings.Contains(viewStr, want) {
			t.Fatalf("native widget_push view missing %s:\n%s", want, viewStr)
		}
	}
	if _, hasLines := lastPush["lines"]; hasLines {
		t.Fatalf("native widget_push must carry view only, not lines: %+v", lastPush)
	}

	// Clearing todos clears the native widget via ui.setWidget content=nil.
	_, errMsg = hNative.callTool("todo", map[string]any{"op": "init", "list": []any{}})
	if errMsg != "" {
		t.Fatalf("todo clear failed: %s", errMsg)
	}
	setWidgets := hNative.callsTo("ui.setWidget")
	if len(setWidgets) == 0 || setWidgets[len(setWidgets)-1].Args["key"] != WidgetKey || setWidgets[len(setWidgets)-1].Args["content"] != nil {
		t.Fatalf("expected ui.setWidget clear on empty todos, calls=%+v", setWidgets)
	}

	// 3. Terminal surface still uses SetWidgetRenderer returning ANSI lines (no view).
	surface.SetNative(false)
	hTerm := startTodoHost(t, hostOptions{Mode: "tui"})
	_, errMsg = hTerm.callTool("todo", map[string]any{
		"op":    "init",
		"items": []any{"Term task"},
	})
	if errMsg != "" {
		t.Fatalf("terminal todo init failed: %s", errMsg)
	}
	if len(hTerm.callsTo("widget_push")) != 0 {
		t.Fatalf("terminal surface must not emit widget_push view, got %+v", hTerm.callsTo("widget_push"))
	}
	termWidgets := hTerm.callsTo("ui.setWidget")
	if len(termWidgets) == 0 {
		t.Fatalf("terminal surface expected ui.setWidget from SetWidgetRenderer, calls=%+v", hTerm.calls)
	}
	lastTerm := termWidgets[len(termWidgets)-1].Args
	contentLines, _ := lastTerm["lines"].([]any)
	if len(contentLines) == 0 {
		t.Fatalf("terminal widget lines = %#v, want non-empty", lastTerm["lines"])
	}
	firstLine, _ := contentLines[0].(string)
	if sanitizeDisplayText(firstLine) != " TODO" {
		t.Fatalf("terminal widget plain line[0] = %q, want %q", sanitizeDisplayText(firstLine), " TODO")
	}
	if !strings.Contains(firstLine, "\x1b[38;2;0;180;255mTODO\x1b[39m") {
		t.Fatalf("terminal widget line[0] must style TODO in accent, got %q", firstLine)
	}

	// 4. Switching surface terminal -> native pushes the kit.View for the active session.
	surface.SetNative(true)
	deadline := time.Now().Add(2 * time.Second)
	for len(hTerm.callsTo("widget_push")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	swPushes := hTerm.callsTo("widget_push")
	if len(swPushes) == 0 {
		t.Fatalf("surface switch to native did not push kit.View widget")
	}
	swRaw, _ := json.Marshal(swPushes[len(swPushes)-1].Args["view"])
	if !strings.Contains(string(swRaw), `"omp.hud.todo"`) || !strings.Contains(string(swRaw), `"◐ Term task"`) {
		t.Fatalf("surface-switched native widget view mismatch: %s", string(swRaw))
	}
}

// fakeHudClock provides a deterministic injectable timer queue for testing
// todo HUD auto-clear without wall-clock sleeps.
type fakeHudClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeHudTimer
}

type fakeHudTimer struct {
	clock   *fakeHudClock
	fireAt  time.Duration
	fn      func()
	stopped bool
	fired   bool
}

func (c *fakeHudClock) afterFunc(d time.Duration, f func()) hudTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeHudTimer{clock: c, fireAt: c.now + d, fn: f}
	c.timers = append(c.timers, t)
	return t
}

func (ft *fakeHudTimer) Stop() bool {
	ft.clock.mu.Lock()
	defer ft.clock.mu.Unlock()
	if ft.stopped || ft.fired {
		return false
	}
	ft.stopped = true
	return true
}

func (c *fakeHudClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []*fakeHudTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired && t.fireAt <= c.now {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.fn()
	}
}

func (c *fakeHudClock) pendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped && !t.fired {
			n++
		}
	}
	return n
}

func installFakeHudClock(t *testing.T) *fakeHudClock {
	t.Helper()
	clk := &fakeHudClock{}
	hudAfterFuncMu.Lock()
	prev := hudAfterFunc
	hudAfterFunc = clk.afterFunc
	hudAfterFuncMu.Unlock()
	t.Cleanup(func() {
		hudAfterFuncMu.Lock()
		hudAfterFunc = prev
		hudAfterFuncMu.Unlock()
	})
	return clk
}

func setTodoClearDelayForTest(t *testing.T, val any) {
	t.Helper()
	prev := Setting
	Setting = func(_ sdk.Context, key string) any {
		if key == "tasks.todoClearDelay" {
			return val
		}
		return nil
	}
	t.Cleanup(func() { Setting = prev })
}

func branchHasEntry(branch []map[string]any, customType string, vis TodoHudVisibility) bool {
	for _, e := range branch {
		if e["type"] != "custom" || e["customType"] != customType {
			continue
		}
		raw, _ := json.Marshal(e["data"])
		var d TodoHudStateEntryData
		if json.Unmarshal(raw, &d) == nil && d.Visibility == vis && d.SourceEntryID != "" && d.Fingerprint != "" {
			return true
		}
	}
	return false
}

func lastWidgetCallIsClear(calls []hostCall) bool {
	for i := len(calls) - 1; i >= 0; i-- {
		c := calls[i]
		if c.Method == "widget_push" && c.Args["key"] == WidgetKey {
			return false
		}
		if c.Method == "ui.setWidget" && c.Args["key"] == WidgetKey {
			_, hasLines := c.Args["lines"]
			_, hasView := c.Args["view"]
			return !hasLines && !hasView && c.Args["content"] == nil
		}
	}
	return false
}

// upstream: packages/coding-agent/test/interactive-mode-todo-clear.test.ts:
// "clears closed todos after the configured delay",
// "cancels an old dismissal timer when a replacement plan becomes visible",
// "leaves closed todos visible when auto-clear is disabled",
// "persists dismissal and explicit reveal across fresh session loads without losing tasks",
// "treats identical canonical todo edits as new snapshots after dismissal"
func TestTodoHudAutoClearAndPersistedVisibility(t *testing.T) {
	surface.ResetForTests()
	resetSurfaceListener()
	t.Cleanup(func() {
		surface.ResetForTests()
		resetSurfaceListener()
	})

	// 1. All closed -> hidden after the delay on BOTH terminal and native surfaces,
	// persisting todo_hud_state {sourceEntryId, fingerprint, visibility:"dismissed"}.
	for _, nativeMode := range []bool{false, true} {
		name := "terminal"
		if nativeMode {
			name = "native"
		}
		t.Run("auto_clear_"+name, func(t *testing.T) {
			clk := installFakeHudClock(t)
			setTodoClearDelayForTest(t, 5.0)
			surface.SetNative(nativeMode)

			h := startTodoHost(t, hostOptions{Mode: "tui"})
			if errMsg := h.runCommand("todo", "append ship fix"); errMsg != "" {
				t.Fatalf("append failed: %s", errMsg)
			}
			if errMsg := h.runCommand("todo", "done"); errMsg != "" {
				t.Fatalf("done failed: %s", errMsg)
			}
			if clk.pendingCount() != 1 {
				t.Fatalf("expected 1 pending auto-clear timer, got %d", clk.pendingCount())
			}
			if lastWidgetCallIsClear(h.callsSnapshot()) {
				t.Fatalf("widget must stay visible before delay elapses")
			}

			// 4.999s: still visible.
			clk.advance(4999 * time.Millisecond)
			if lastWidgetCallIsClear(h.callsSnapshot()) {
				t.Fatalf("widget cleared early at 4999ms")
			}

			// +1ms -> fires, persists dismissal, hides HUD while preserving phases.
			clk.advance(1 * time.Millisecond)
			if !lastWidgetCallIsClear(h.callsSnapshot()) {
				t.Fatalf("expected widget cleared after 5s delay on %s surface, calls=%+v", name, h.callsSnapshot())
			}
			h.mu.Lock()
			branchSnap := append([]map[string]any(nil), h.opts.Branch...)
			h.mu.Unlock()
			if !branchHasEntry(branchSnap, TodoHudStateCustomType, HudVisibilityDismissed) {
				t.Fatalf("expected persisted todo_hud_state dismissed entry in branch: %+v", branchSnap)
			}
			res, _ := h.callTool("todo", map[string]any{"op": "view"})
			if len(decodePhasesFromRes(t, res)) != 1 {
				t.Fatalf("auto-clear must not mutate stored todo phases")
			}

			// Resume into a fresh session with the persisted branch -> stays hidden, no new timer.
			hResume := startTodoHost(t, hostOptions{Mode: "tui", Branch: branchSnap})
			if _, errMsg := hResume.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
				t.Fatalf("resume session_start failed: %s", errMsg)
			}
			if !lastWidgetCallIsClear(hResume.callsSnapshot()) {
				t.Fatalf("resumed dismissed HUD must stay hidden on %s surface, calls=%+v", name, hResume.callsSnapshot())
			}
			if clk.pendingCount() != 0 {
				t.Fatalf("resumed dismissed HUD must not arm a new timer, pending=%d", clk.pendingCount())
			}
			resR, _ := hResume.callTool("todo", map[string]any{"op": "view"})
			if len(decodePhasesFromRes(t, resR)) != 1 {
				t.Fatalf("resumed dismissed session must keep phases intact")
			}

			// Explicit /todo expand reveals the HUD and persists visibility:"revealed".
			if errMsg := hResume.runCommand("todo", "expand"); errMsg != "" {
				t.Fatalf("/todo expand failed: %s", errMsg)
			}
			if lastWidgetCallIsClear(hResume.callsSnapshot()) {
				t.Fatalf("/todo expand must reveal the HUD on %s surface", name)
			}
			hResume.mu.Lock()
			branchAfterReveal := append([]map[string]any(nil), hResume.opts.Branch...)
			hResume.mu.Unlock()
			if !branchHasEntry(branchAfterReveal, TodoHudStateCustomType, HudVisibilityRevealed) {
				t.Fatalf("expected persisted todo_hud_state revealed entry in branch: %+v", branchAfterReveal)
			}

			// Resume after explicit reveal -> stays visible and does not re-arm auto-clear.
			hRevealResume := startTodoHost(t, hostOptions{Mode: "tui", Branch: branchAfterReveal})
			if _, errMsg := hRevealResume.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
				t.Fatalf("reveal resume failed: %s", errMsg)
			}
			if lastWidgetCallIsClear(hRevealResume.callsSnapshot()) {
				t.Fatalf("resumed revealed HUD must stay visible on %s surface", name)
			}
			if clk.pendingCount() != 0 {
				t.Fatalf("resumed revealed HUD must not arm auto-clear timer, pending=%d", clk.pendingCount())
			}
		})
	}

	// 2. A change before the delay elapses cancels the timer (generation guard).
	t.Run("change_before_delay_cancels", func(t *testing.T) {
		clk := installFakeHudClock(t)
		setTodoClearDelayForTest(t, 2.0)
		surface.SetNative(false)

		h := startTodoHost(t, hostOptions{Mode: "tui"})
		_ = h.runCommand("todo", "append first task")
		_ = h.runCommand("todo", "done")
		clk.advance(1 * time.Second)
		// Add a new open task before the 2s timer fires.
		_ = h.runCommand("todo", "append second task")
		if clk.pendingCount() != 0 {
			t.Fatalf("expected timer canceled when open task added, pending=%d", clk.pendingCount())
		}
		clk.advance(5 * time.Second)
		if lastWidgetCallIsClear(h.callsSnapshot()) {
			t.Fatalf("HUD must stay visible after replacement task cancelled timer")
		}
	})

	// 3. Negative or non-finite delay -> never schedules auto-clear.
	for _, delay := range []any{-1.0, math.NaN(), math.Inf(1)} {
		t.Run(fmt.Sprintf("disabled_delay_%v", delay), func(t *testing.T) {
			clk := installFakeHudClock(t)
			setTodoClearDelayForTest(t, delay)
			surface.SetNative(true)

			h := startTodoHost(t, hostOptions{Mode: "tui"})
			_ = h.runCommand("todo", "append only task")
			_ = h.runCommand("todo", "done")
			if clk.pendingCount() != 0 {
				t.Fatalf("delay=%v must never schedule timer, pending=%d", delay, clk.pendingCount())
			}
			clk.advance(24 * time.Hour)
			if lastWidgetCallIsClear(h.callsSnapshot()) {
				t.Fatalf("delay=%v must leave HUD visible", delay)
			}
		})
	}

	// 4. Deferred persistence when tool call completes with delay=0 before message_end persists toolResult.
	t.Run("tool_call_delay_zero_flushes_on_turn_end", func(t *testing.T) {
		clk := installFakeHudClock(t)
		setTodoClearDelayForTest(t, 0.0)
		surface.SetNative(true)

		h := startTodoHost(t, hostOptions{Mode: "tui"})
		_, _ = h.callTool("todo", map[string]any{"op": "init", "items": []any{"ship"}})
		res, _ := h.callTool("todo", map[string]any{"op": "done", "task": "ship"})
		// Fire the 0s timer immediately (before host has appended toolResult to branch).
		clk.advance(0)
		if !lastWidgetCallIsClear(h.callsSnapshot()) {
			t.Fatalf("delay=0 should hide HUD immediately")
		}
		// Simulate host persisting the toolResult message to branch, then firing turn_end.
		h.mu.Lock()
		h.opts.Branch = append(h.opts.Branch, map[string]any{
			"id":   "tr-1",
			"type": "message",
			"message": map[string]any{
				"role":     "toolResult",
				"toolName": "todo",
				"isError":  false,
				"details":  res["details"],
			},
		})
		h.mu.Unlock()
		_, _ = h.emitEvent(sdk.EventTurnEnd, map[string]any{"toolResults": []any{}})

		h.mu.Lock()
		branchSnap := append([]map[string]any(nil), h.opts.Branch...)
		h.mu.Unlock()
		if !branchHasEntry(branchSnap, TodoHudStateCustomType, HudVisibilityDismissed) {
			t.Fatalf("expected deferred todo_hud_state dismissed entry persisted on turn_end: %+v", branchSnap)
		}
	})

	// 5. Session end via lifecycle.End cancels any pending timer.
	t.Run("lifecycle_end_cancels_timer", func(t *testing.T) {
		clk := installFakeHudClock(t)
		setTodoClearDelayForTest(t, 10.0)
		surface.SetNative(false)

		h := startTodoHost(t, hostOptions{Mode: "tui", SessionID: "sess-autoclear-end"})
		_ = h.runCommand("todo", "append done soon")
		_ = h.runCommand("todo", "done")
		if clk.pendingCount() != 1 {
			t.Fatalf("expected 1 pending timer before lifecycle.End")
		}
		lifecycle.End("sess-autoclear-end")
		if clk.pendingCount() != 0 {
			t.Fatalf("expected timer canceled on lifecycle.End, pending=%d", clk.pendingCount())
		}
	})
}

// upstream: packages/coding-agent/src/modes/interactive-mode.ts:641-661,5315-5329,5521-5618
// Verifies exact SGR styling for every HUD element, every task status, active vs inactive
// phases, overflow rows, note markers, and progress-filled spine/tail using internal/ansi's theme tokens.
func TestWidgetStyledSGRPerElementAndStatus(t *testing.T) {
	const (
		fgAccent  = "\x1b[38;2;0;180;255m"
		fgSuccess = "\x1b[38;2;0;255;136m"
		fgError   = "\x1b[38;2;255;71;87m"
		fgMuted   = "\x1b[38;2;156;163;176m"
		fgDim     = "\x1b[38;2;107;114;128m"
		fgReset   = "\x1b[39m"
		boldOn    = "\x1b[1m"
		boldOff   = "\x1b[22m"
		italicOn  = "\x1b[3m"
		italicOff = "\x1b[23m"
		strikeOn  = "\x1b[9m"
		strikeOff = "\x1b[29m"
	)

	t.Run("all_closed_verify_capture_sgr", func(t *testing.T) {
		phases := []TodoPhase{
			{
				Name: "Verify",
				Tasks: []TodoItem{
					{Content: "Check read card", Status: StatusCompleted},
					{Content: "Check todo card", Status: StatusCompleted},
				},
			},
		}
		got := RenderStyledWidgetLinesForWidth(sdk.UITheme{}, phases, false, 80)
		want := []string{
			" " + boldOn + fgAccent + "TODO" + fgReset + boldOff,
			"  " + fgAccent + "├─ " + fgReset + boldOn + fgAccent + "Verify" + fgReset + boldOff + fgDim + " · 2/2" + fgReset,
			"  " + fgAccent + "│  " + fgReset + fgDim + "├─" + fgReset + " " + fgSuccess + "☑ " + strikeOn + "Check read card" + strikeOff + fgReset,
			"  " + fgAccent + "│  " + fgReset + fgDim + "└─" + fgReset + " " + fgSuccess + "☑ " + strikeOn + "Check todo card" + strikeOff + fgReset,
			"  " + fgAccent + "└─────" + fgReset,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("styled Verify HUD mismatch:\ngot:\n%#v\nwant:\n%#v", got, want)
		}
	})

	t.Run("every_status_and_notes_and_phases", func(t *testing.T) {
		phases := []TodoPhase{
			{
				Name: "Active",
				Tasks: []TodoItem{
					{Content: "done task", Status: StatusCompleted, Notes: []string{"n1", "n2"}},
					{Content: "running task", Status: StatusInProgress, Notes: []string{"n1"}},
					{Content: "dropped task", Status: StatusAbandoned},
					{Content: "queued task", Status: StatusPending},
				},
			},
			{
				Name:  "Later",
				Tasks: []TodoItem{{Content: "later task", Status: StatusPending}},
			},
			{Name: "Three", Tasks: []TodoItem{{Content: "t3", Status: StatusPending}}},
			{Name: "Four", Tasks: []TodoItem{{Content: "t4", Status: StatusPending}}},
			{Name: "Five", Tasks: []TodoItem{{Content: "t5", Status: StatusPending}}},
			{Name: "Six", Tasks: []TodoItem{{Content: "t6", Status: StatusPending}}},
		}
		expandedRows := RenderStyledWidgetLinesForWidth(sdk.UITheme{}, phases, true, 80)
		collapsedRows := RenderStyledWidgetLinesForWidth(sdk.UITheme{}, phases, false, 80)

		cases := []struct {
			name string
			rows []string
			want string
		}{
			{
				name: "title_bold_accent",
				rows: expandedRows,
				want: " " + boldOn + fgAccent + "TODO" + fgReset + boldOff,
			},
			{
				name: "active_phase_bold_accent_progress_dim",
				rows: expandedRows,
				want: boldOn + fgAccent + "I. Active" + fgReset + boldOff + fgDim + " · 2/4" + fgReset,
			},
			{
				name: "completed_success_strike_and_superscript_notes",
				rows: expandedRows,
				want: fgDim + "├─" + fgReset + " " + fgSuccess + "☑ " + strikeOn + "done task" + strikeOff + fgReset + fgDim + italicOn + " ⁺²" + italicOff + fgReset,
			},
			{
				name: "in_progress_accent_and_note_marker",
				rows: expandedRows,
				want: fgDim + "├─" + fgReset + " " + fgAccent + "☐ running task" + fgReset + fgDim + italicOn + " ⁺¹" + italicOff + fgReset,
			},
			{
				name: "abandoned_error_strikethrough",
				rows: expandedRows,
				want: fgDim + "├─" + fgReset + " " + fgError + "☐ " + strikeOn + "dropped task" + strikeOff + fgReset,
			},
			{
				name: "pending_dim",
				rows: expandedRows,
				want: fgDim + "└─" + fgReset + " " + fgDim + "☐ queued task" + fgReset,
			},
			{
				name: "inactive_phase_muted_and_progress_dim",
				rows: collapsedRows,
				want: fgMuted + "II. Later" + fgReset + fgDim + " · 0/1" + fgReset,
			},
			{
				name: "overflow_stages_muted",
				rows: collapsedRows,
				want: fgMuted + "… 1 more stage" + fgReset,
			},
			{
				name: "unlit_tail_dim_when_incomplete",
				rows: collapsedRows,
				want: "  " + fgDim + "└─────" + fgReset,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				joined := strings.Join(tc.rows, "\n")
				if !strings.Contains(joined, tc.want) {
					t.Fatalf("missing expected styled fragment %q in:\n%#v", tc.want, tc.rows)
				}
			})
		}
	})

	t.Run("partial_progress_splits_spine_accent_and_dim", func(t *testing.T) {
		// 2 tasks, 1 closed (50%): contentLines=3 (phase + 2 tasks), tailLen=6, pathLen=9 -> filled=round(4.5)=5 ->
		// all 3 spine rows lit in accent, and 2 of 6 tail cells lit in accent + 4 in dim.
		phases := []TodoPhase{
			{
				Name: "Work",
				Tasks: []TodoItem{
					{Content: "first", Status: StatusCompleted},
					{Content: "second", Status: StatusInProgress},
				},
			},
		}
		rows := RenderStyledWidgetLinesForWidth(sdk.UITheme{}, phases, false, 80)
		wantTail := "  " + fgAccent + "└─" + fgReset + fgDim + "────" + fgReset
		if gotTail := rows[len(rows)-1]; gotTail != wantTail {
			t.Fatalf("partial tail = %q, want %q", gotTail, wantTail)
		}
	})

	t.Run("zero_closed_spine_all_dim", func(t *testing.T) {
		phases := []TodoPhase{
			{Name: "Start", Tasks: []TodoItem{{Content: "first", Status: StatusInProgress}}},
		}
		rows := RenderStyledWidgetLinesForWidth(sdk.UITheme{}, phases, false, 80)
		if !strings.HasPrefix(rows[1], "  "+fgDim+"├─ "+fgReset) {
			t.Fatalf("0%% progress spine should be dim, got %q", rows[1])
		}
		if rows[len(rows)-1] != "  "+fgDim+"└─────"+fgReset {
			t.Fatalf("0%% progress tail should be dim, got %q", rows[len(rows)-1])
		}
	})

	t.Run("light_theme_uses_host_theme_tokens", func(t *testing.T) {
		surface.ResetForTests()
		surface.SetNative(false)
		t.Cleanup(surface.ResetForTests)

		h := startTodoHost(t, hostOptions{
			Mode: "tui",
			Theme: map[string]any{
				"name":       "light",
				"appearance": "light",
				"modifiers":  true,
				"mode":       "truecolor",
				"foregrounds": map[string]any{
					"accent":  "\x1b[38;2;1;2;3m",
					"success": "\x1b[38;2;4;5;6m",
					"dim":     "\x1b[38;2;7;8;9m",
				},
			},
		})
		_, errMsg := h.callTool("todo", map[string]any{
			"op": "init",
			"list": []any{
				map[string]any{"phase": "Verify", "items": []any{"done"}},
			},
		})
		if errMsg != "" {
			t.Fatalf("init failed: %s", errMsg)
		}
		_, errMsg = h.callTool("todo", map[string]any{"op": "done", "task": "done"})
		if errMsg != "" {
			t.Fatalf("done failed: %s", errMsg)
		}
		widgets := h.callsTo("ui.setWidget")
		if len(widgets) == 0 {
			t.Fatalf("expected ui.setWidget call")
		}
		rawLines, _ := widgets[len(widgets)-1].Args["lines"].([]any)
		lines := make([]string, 0, len(rawLines))
		for _, rl := range rawLines {
			if s, ok := rl.(string); ok {
				lines = append(lines, s)
			}
		}
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"\x1b[38;2;1;2;3mTODO\x1b[39m", "\x1b[38;2;4;5;6m☑ ", "\x1b[38;2;7;8;9m · 1/1\x1b[39m"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("light theme output missing %q:\n%#v", want, lines)
			}
		}
	})
}
