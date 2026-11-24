package todo

import (
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func execTool(phases []TodoPhase, raw map[string]any) ([]TodoPhase, string, TodoToolDetails, bool) {
	resolved, errText := ResolveTodoParams(raw, len(phases) > 0)
	if errText != "" {
		return phases, errText, TodoToolDetails{Phases: phases, Storage: "memory"}, true
	}
	readOnly := resolved.Op == OpView
	var updated []TodoPhase
	var errs []string
	if readOnly {
		updated = ClonePhases(phases)
	} else {
		updated, errs = ApplyParams(ClonePhases(phases), resolved)
	}
	failed := len(errs) > 0
	effective := updated
	if failed {
		effective = phases
	}
	var completed []TodoCompletionTransition
	if !readOnly && !failed {
		completed = getCompletionTransitions(phases, updated)
	}
	details := TodoToolDetails{
		Op:             resolved.Op,
		Phases:         effective,
		Storage:        "memory",
		CompletedTasks: completed,
	}
	return effective, FormatSummary(effective, errs, readOnly), details, failed
}

func TestResolveTodoMarkdownPath(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "todo-workspace")

	// upstream: todo.test.ts: "defaults to TODO.md under cwd"
	got, err := ResolveTodoMarkdownPath("", cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(cwd, "TODO.md"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// upstream: todo.test.ts: "strips surrounding double quotes before resolving"
	got, err = ResolveTodoMarkdownPath(`"my todos.md"`, cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(cwd, "my todos.md"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// upstream: todo.test.ts: "rejects internal URL schemes"
	_, err = ResolveTodoMarkdownPath("artifact://todo", cwd)
	if err == nil || !strings.Contains(err.Error(), "internal scheme") {
		t.Fatalf("expected internal scheme error, got %v", err)
	}
}

func TestAutoStartAndPromotion(t *testing.T) {
	// upstream: todo.test.ts: "auto-starts the first task after init"
	phases, summary, details, isErr := execTool(nil, map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "Execution", "items": []any{"status", "diagnostics"}},
		},
	})
	if isErr {
		t.Fatalf("unexpected error: %s", summary)
	}
	if len(details.Phases) != 1 || len(details.Phases[0].Tasks) != 2 {
		t.Fatalf("unexpected phases: %+v", details.Phases)
	}
	if details.Phases[0].Tasks[0].Status != StatusInProgress || details.Phases[0].Tasks[1].Status != StatusPending {
		t.Fatalf("unexpected statuses: %+v", details.Phases[0].Tasks)
	}
	for _, want := range []string{"Remaining items (2):", "status [in_progress] (Execution)", "diagnostics [pending] (Execution)"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}

	// upstream: todo.test.ts: "auto-promotes the next pending task when current task is completed"
	phases, summary, details, isErr = execTool(phases, map[string]any{"op": "done", "task": "status"})
	if isErr {
		t.Fatalf("unexpected error: %s", summary)
	}
	if details.Phases[0].Tasks[0].Status != StatusCompleted || details.Phases[0].Tasks[1].Status != StatusInProgress {
		t.Fatalf("unexpected statuses after done: %+v", details.Phases[0].Tasks)
	}
	if len(details.CompletedTasks) != 1 || details.CompletedTasks[0].Content != "status" || details.CompletedTasks[0].Phase != "Execution" {
		t.Fatalf("unexpected completedTasks: %+v", details.CompletedTasks)
	}
	if !strings.Contains(summary, "Remaining items (1):") || !strings.Contains(summary, "diagnostics [in_progress] (Execution)") {
		t.Errorf("unexpected summary:\n%s", summary)
	}

	_, summary, _, isErr = execTool(phases, map[string]any{"op": "done", "task": "diagnostics"})
	if isErr {
		t.Fatalf("unexpected error: %s", summary)
	}
	if !strings.Contains(summary, "Remaining items: none.") {
		t.Errorf("expected Remaining items: none., got:\n%s", summary)
	}
}

func TestNextActionableTask(t *testing.T) {
	// upstream: todo.test.ts: "returns the in-progress task before the first pending task across phases"
	got := NextActionableTask([]TodoPhase{
		{Name: "First", Tasks: []TodoItem{{Content: "queued first", Status: StatusPending}}},
		{Name: "Second", Tasks: []TodoItem{{Content: "active second", Status: StatusInProgress}}},
	})
	if got == nil || got.Content != "active second" {
		t.Fatalf("got %+v, want active second", got)
	}

	// upstream: todo.test.ts: "falls back to the first pending task when nothing is in progress"
	got = NextActionableTask([]TodoPhase{
		{Name: "Done", Tasks: []TodoItem{{Content: "finished", Status: StatusCompleted}}},
		{Name: "Next", Tasks: []TodoItem{{Content: "first pending", Status: StatusPending}}},
	})
	if got == nil || got.Content != "first pending" {
		t.Fatalf("got %+v, want first pending", got)
	}
}

func TestTodoOperations(t *testing.T) {
	// upstream: todo.test.ts: "jumps to a specific task out of order"
	phases, _, _, _ := execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Phase A", "items": []any{"first", "second", "third"}}},
	})
	phases, _, details, isErr := execTool(phases, map[string]any{"op": "start", "task": "third"})
	if isErr || details.Op != OpStart {
		t.Fatalf("start failed: isErr=%v op=%s", isErr, details.Op)
	}
	if phases[0].Tasks[0].Status != StatusPending || phases[0].Tasks[1].Status != StatusPending || phases[0].Tasks[2].Status != StatusInProgress {
		t.Fatalf("unexpected statuses: %+v", phases[0].Tasks)
	}

	// upstream: todo.test.ts: "demotes the current in_progress task when starting another"
	phases, _, _, _ = execTool(nil, map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "A", "items": []any{"a1", "a2"}},
			map[string]any{"phase": "B", "items": []any{"b1"}},
		},
	})
	phases, _, _, _ = execTool(phases, map[string]any{"op": "start", "task": "b1"})
	if phases[0].Tasks[0].Status != StatusPending || phases[0].Tasks[1].Status != StatusPending || phases[1].Tasks[0].Status != StatusInProgress {
		t.Fatalf("unexpected statuses: %+v", phases)
	}

	// Done out of order: completing a later phase's task while earlier phase is open triggers workedAhead note.
	phases, summary, _, isErr := execTool(phases, map[string]any{"op": "done", "task": "b1"})
	if isErr {
		t.Fatalf("done b1 failed: %s", summary)
	}
	if phases[0].Tasks[0].Status != StatusInProgress || phases[1].Tasks[0].Status != StatusCompleted {
		t.Fatalf("expected a1 in_progress and b1 completed, got %+v", phases)
	}
	if !strings.Contains(summary, "nothing was un-completed") {
		t.Errorf("expected workedAhead explanation in summary:\n%s", summary)
	}

	// upstream: todo.test.ts: "appends items to an existing phase"
	phases, _, _, _ = execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"First"}}},
	})
	phases, _, _, isErr = execTool(phases, map[string]any{"op": "append", "phase": "Work", "items": []any{"Second"}})
	if isErr || len(phases[0].Tasks) != 2 || phases[0].Tasks[1].Content != "Second" || phases[0].Tasks[1].Status != StatusPending {
		t.Fatalf("append existing phase failed: %+v", phases)
	}

	// upstream: todo.test.ts: "creates a phase when append targets a missing phase"
	phases, _, _, isErr = execTool(phases, map[string]any{"op": "append", "phase": "Cleanup", "items": []any{"Remove dead code"}})
	if isErr || len(phases) != 2 || phases[1].Name != "Cleanup" || phases[1].Tasks[0].Content != "Remove dead code" {
		t.Fatalf("append new phase failed: %+v", phases)
	}

	// upstream: todo.test.ts: "marks all tasks in a phase done"
	phases, _, _, _ = execTool(nil, map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "Work", "items": []any{"First", "Second"}},
			map[string]any{"phase": "Later", "items": []any{"Third"}},
		},
	})
	phases, _, _, isErr = execTool(phases, map[string]any{"op": "done", "phase": "Work"})
	if isErr || phases[0].Tasks[0].Status != StatusCompleted || phases[0].Tasks[1].Status != StatusCompleted || phases[1].Tasks[0].Status != StatusInProgress {
		t.Fatalf("done phase failed: %+v", phases)
	}

	// upstream: todo.test.ts: "removes all tasks when rm omits task and phase"
	phases, summary, _, isErr = execTool(phases, map[string]any{"op": "rm"})
	if isErr || len(phases[0].Tasks) != 0 || !strings.Contains(summary, "Todo list cleared.") {
		t.Fatalf("rm all failed: isErr=%v summary=%q", isErr, summary)
	}

	// upstream: todo.test.ts: "drops all tasks in a phase"
	phases, _, _, _ = execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"First", "Second"}}},
	})
	phases, _, _, isErr = execTool(phases, map[string]any{"op": "drop", "phase": "Work"})
	if isErr || phases[0].Tasks[0].Status != StatusAbandoned || phases[0].Tasks[1].Status != StatusAbandoned {
		t.Fatalf("drop phase failed: %+v", phases)
	}

	// upstream: todo.test.ts: "view echoes state without mutating it"
	unnormalized := []TodoPhase{{
		Name: "Work",
		Tasks: []TodoItem{
			{Content: "First", Status: StatusPending},
			{Content: "Second", Status: StatusPending},
		},
	}}
	afterView, summary, _, isErr := execTool(unnormalized, map[string]any{"op": "view"})
	if isErr || afterView[0].Tasks[0].Status != StatusPending || afterView[0].Tasks[1].Status != StatusPending {
		t.Fatalf("view mutated state: %+v", afterView)
	}
	if !strings.Contains(summary, "First") || !strings.Contains(summary, "Second") {
		t.Errorf("view summary missing tasks:\n%s", summary)
	}

	// upstream: todo.test.ts: "view on an empty list reports empty, not cleared"
	_, summary, _, isErr = execTool(nil, map[string]any{"op": "view"})
	if isErr || !strings.Contains(summary, "Todo list is empty.") {
		t.Fatalf("expected Todo list is empty., got isErr=%v summary=%q", isErr, summary)
	}
}

func TestBlockAndUnblock(t *testing.T) {
	// upstream: todo.test.ts: "blocks a task (excluded from remaining, counted distinctly) and unblocks it"
	phases, _, _, _ := execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"a", "b"}}},
	})
	phases, summary, _, isErr := execTool(phases, map[string]any{"op": "block", "task": "b", "reason": "waiting on sign-off"})
	if isErr {
		t.Fatalf("block failed: %s", summary)
	}
	if phases[0].Tasks[1].Status != StatusBlocked || phases[0].Tasks[1].Blocker != "waiting on sign-off" {
		t.Fatalf("unexpected b task: %+v", phases[0].Tasks[1])
	}
	if !strings.Contains(summary, "Remaining items (1):") || !strings.Contains(summary, "1 blocked") {
		t.Errorf("unexpected block summary:\n%s", summary)
	}
	phases, _, _, isErr = execTool(phases, map[string]any{"op": "unblock", "task": "b"})
	if isErr || phases[0].Tasks[1].Status != StatusPending || phases[0].Tasks[1].Blocker != "" {
		t.Fatalf("unblock failed: %+v", phases[0].Tasks[1])
	}

	// upstream: todo.test.ts: "does not auto-promote a blocked task to in_progress"
	only, _, _, _ := execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"only"}}},
	})
	only, _, _, _ = execTool(only, map[string]any{"op": "block", "task": "only"})
	if only[0].Tasks[0].Status != StatusBlocked {
		t.Fatalf("blocked task was promoted: %+v", only[0].Tasks[0])
	}

	// upstream: todo.test.ts: "blocking a phase leaves completed/abandoned tasks closed"
	phases, _, _, _ = execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"a", "b", "c"}}},
	})
	phases, _, _, _ = execTool(phases, map[string]any{"op": "done", "task": "a"})
	phases, _, _, _ = execTool(phases, map[string]any{"op": "drop", "task": "c"})
	phases, _, _, _ = execTool(phases, map[string]any{"op": "block", "phase": "Work", "reason": "waiting on infra"})
	if phases[0].Tasks[0].Status != StatusCompleted || phases[0].Tasks[0].Blocker != "" {
		t.Fatalf("completed task modified: %+v", phases[0].Tasks[0])
	}
	if phases[0].Tasks[1].Status != StatusBlocked || phases[0].Tasks[1].Blocker != "waiting on infra" {
		t.Fatalf("open task not blocked: %+v", phases[0].Tasks[1])
	}
	if phases[0].Tasks[2].Status != StatusAbandoned {
		t.Fatalf("abandoned task modified: %+v", phases[0].Tasks[2])
	}

	// upstream: todo.test.ts: "re-blocking an already-blocked task refines its blocker note"
	phases, _, _, _ = execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"a", "b"}}},
	})
	phases, _, _, _ = execTool(phases, map[string]any{"op": "block", "task": "b"})
	phases, _, _, _ = execTool(phases, map[string]any{"op": "block", "task": "b", "reason": "waiting on user"})
	if phases[0].Tasks[1].Status != StatusBlocked || phases[0].Tasks[1].Blocker != "waiting on user" {
		t.Fatalf("re-block did not refine reason: %+v", phases[0].Tasks[1])
	}

	// upstream: todo.test.ts: "rejects a block with neither task nor phase target"
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "block", "reason": "oops"})
	if !isErr || !strings.Contains(summary, "block requires a task or phase target") {
		t.Fatalf("expected target error, got isErr=%v summary=%q", isErr, summary)
	}

	// upstream: todo.test.ts: "rejects an unblock with neither task nor phase target"
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "unblock"})
	if !isErr || !strings.Contains(summary, "unblock requires a task or phase target") {
		t.Fatalf("expected unblock target error, got isErr=%v summary=%q", isErr, summary)
	}
}

func TestInvalidCasesAndErrorMessages(t *testing.T) {
	// upstream: todo.test.ts: "still errors when init has neither list nor items"
	_, summary, _, isErr := execTool(nil, map[string]any{"op": "init"})
	if !isErr || !strings.Contains(summary, "Missing list for init operation") {
		t.Fatalf("expected missing list error, got isErr=%v summary=%q", isErr, summary)
	}

	// Duplicate phase and duplicate task in init list
	_, summary, _, isErr = execTool(nil, map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "A", "items": []any{"x"}},
			map[string]any{"phase": "A", "items": []any{"x"}},
		},
	})
	if !isErr || !strings.Contains(summary, `Duplicate phase "A" in init list`) || !strings.Contains(summary, `Duplicate task "x" in init list`) {
		t.Fatalf("expected duplicate phase/task errors, got isErr=%v summary=%q", isErr, summary)
	}

	// Task ID mistake hint
	phases, _, _, _ := execTool(nil, map[string]any{
		"op":   "init",
		"list": []any{map[string]any{"phase": "Work", "items": []any{"Real task"}}},
	})
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "done", "task": "task-1"})
	if !isErr || !strings.Contains(summary, "Tasks are referenced by content, not by IDs") {
		t.Fatalf("expected task-ID hint, got isErr=%v summary=%q", isErr, summary)
	}

	// Missing task on empty list hint
	_, summary, _, isErr = execTool(nil, map[string]any{"op": "done", "task": "Nope"})
	if !isErr || !strings.Contains(summary, "todo list is empty") {
		t.Fatalf("expected empty list hint, got isErr=%v summary=%q", isErr, summary)
	}

	// Missing phase
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "done", "phase": "NoSuchPhase"})
	if !isErr || !strings.Contains(summary, `Phase "NoSuchPhase" not found`) {
		t.Fatalf("expected phase not found, got isErr=%v summary=%q", isErr, summary)
	}

	// Append missing phase / empty items / duplicate task
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "append", "items": []any{"New"}})
	if !isErr || !strings.Contains(summary, "Missing phase name for append operation") {
		t.Fatalf("expected missing phase error, got %q", summary)
	}
	// upstream: todo.test.ts: "defers empty append items to an op-specific runtime error"
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "append", "phase": "Work", "items": []any{}})
	if !isErr || !strings.Contains(summary, "Missing items for append operation") {
		t.Fatalf("expected missing items error, got %q", summary)
	}
	_, summary, _, isErr = execTool(phases, map[string]any{"op": "append", "phase": "Work", "items": []any{"Real task"}})
	if !isErr || !strings.Contains(summary, `Task "Real task" already exists`) {
		t.Fatalf("expected already exists error, got %q", summary)
	}
}

func TestLenientInitAndOpRecovery(t *testing.T) {
	// upstream: todo.test.ts: "accepts a flattened init with bare items and no phase"
	phases, _, _, isErr := execTool(nil, map[string]any{"op": "init", "items": []any{"First", "Second"}})
	if isErr || len(phases) != 1 || phases[0].Name != "Tasks" || phases[0].Tasks[0].Status != StatusInProgress {
		t.Fatalf("flattened init failed: %+v", phases)
	}

	// upstream: todo.test.ts: "honors a bare phase on a flattened init"
	phases, _, _, isErr = execTool(nil, map[string]any{"op": "init", "phase": "Cleanup", "items": []any{"Remove dead code"}})
	if isErr || phases[0].Name != "Cleanup" {
		t.Fatalf("bare phase init failed: %+v", phases)
	}

	// upstream: todo.test.ts: "infers init from a bare list payload"
	phases, _, details, isErr := execTool(nil, map[string]any{
		"list": []any{map[string]any{"phase": "Fixes", "items": []any{"One", "Two"}}},
	})
	if isErr || details.Op != OpInit || phases[0].Name != "Fixes" {
		t.Fatalf("inferred init failed: isErr=%v details=%+v", isErr, details)
	}

	// upstream: todo.test.ts: "infers append from phase plus items"
	phases, _, details, isErr = execTool(phases, map[string]any{"phase": "Fixes", "items": []any{"Three"}})
	if isErr || details.Op != OpAppend || len(phases[0].Tasks) != 3 {
		t.Fatalf("inferred append failed: isErr=%v details=%+v", isErr, details)
	}

	// upstream: todo.test.ts: "infers init from bare items only when no todos exist"
	_, _, details, isErr = execTool(nil, map[string]any{"items": []any{"Only task"}})
	if isErr || details.Op != OpInit {
		t.Fatalf("expected inferred init on empty state")
	}
	_, _, _, isErr = execTool(phases, map[string]any{"items": []any{"Ambiguous"}})
	if !isErr {
		t.Fatalf("expected error for bare items on populated state")
	}

	// upstream: todo.test.ts: "surfaces the schema error when op is missing and not inferable"
	_, summary, _, isErr := execTool(nil, map[string]any{"task": "Something"})
	if !isErr || !strings.Contains(summary, "Invalid todo arguments") || !strings.Contains(summary, "op") {
		t.Fatalf("expected Invalid todo arguments with op, got %q", summary)
	}
}

func TestMarkdownRoundTrip(t *testing.T) {
	// upstream: todo.test.ts: "preserves blocked status across the markdown round-trip"
	phases := []TodoPhase{{
		Name: "Work",
		Tasks: []TodoItem{
			{Content: "a", Status: StatusBlocked, Blocker: "x"},
			{Content: "b", Status: StatusCompleted},
		},
	}}
	md := PhasesToMarkdown(phases)
	if !strings.Contains(md, "- [!] a <!-- blocker: x -->") {
		t.Fatalf("unexpected markdown:\n%s", md)
	}
	parsed, errs := MarkdownToPhases(md)
	if len(errs) != 0 || parsed[0].Tasks[0].Status != StatusBlocked || parsed[0].Tasks[0].Blocker != "x" {
		t.Fatalf("round-trip failed: errs=%v parsed=%+v", errs, parsed)
	}

	// upstream: todo.test.ts: "parses checklist items with backslash-escaped brackets from /todo edit"
	escaped := "# Todos\n* \\[x] first\n- \\[ \\] second\n+ \\[/\\] third"
	parsed, errs = MarkdownToPhases(escaped)
	if len(errs) != 0 || len(parsed[0].Tasks) != 3 {
		t.Fatalf("escaped brackets failed: errs=%v parsed=%+v", errs, parsed)
	}
	if parsed[0].Tasks[0].Status != StatusCompleted || parsed[0].Tasks[1].Status != StatusPending || parsed[0].Tasks[2].Status != StatusInProgress {
		t.Fatalf("unexpected statuses: %+v", parsed[0].Tasks)
	}

	// upstream: todo.test.ts: "normalizes a multi-line blocker reason so the markdown round-trip survives"
	initPh, _, _, _ := execTool(nil, map[string]any{"op": "init", "list": []any{map[string]any{"phase": "Work", "items": []any{"a"}}}})
	blockedPh, _, _, _ := execTool(initPh, map[string]any{"op": "block", "task": "a", "reason": "waiting on user:\nline two\n\tindented three"})
	if blockedPh[0].Tasks[0].Blocker != "waiting on user: line two indented three" {
		t.Fatalf("blocker not normalized: %q", blockedPh[0].Tasks[0].Blocker)
	}
	md2 := PhasesToMarkdown(blockedPh)
	parsed2, errs2 := MarkdownToPhases(md2)
	if len(errs2) != 0 || parsed2[0].Tasks[0].Blocker != "waiting on user: line two indented three" {
		t.Fatalf("multi-line blocker round-trip failed: errs=%v parsed=%+v", errs2, parsed2)
	}
}

func TestReplayFromBranch(t *testing.T) {
	entries := []map[string]any{
		{
			"type": "message",
			"message": map[string]any{
				"role":     "toolResult",
				"toolName": "todo",
				"isError":  false,
				"details": map[string]any{
					"op": "init",
					"phases": []any{
						map[string]any{
							"name": "Phase 1",
							"tasks": []any{
								map[string]any{"content": "Step A", "status": "in_progress"},
								map[string]any{"content": "Step B", "status": "pending"},
							},
						},
					},
				},
			},
		},
		// View entries must be ignored.
		{
			"type": "message",
			"message": map[string]any{
				"role":     "toolResult",
				"toolName": "todo",
				"isError":  false,
				"details": map[string]any{
					"op":     "view",
					"phases": []any{},
				},
			},
		},
		// Error entries must be ignored.
		{
			"type": "message",
			"message": map[string]any{
				"role":     "toolResult",
				"toolName": "todo",
				"isError":  true,
				"details": map[string]any{
					"op":     "rm",
					"phases": []any{},
				},
			},
		},
		// User edit custom entry takes precedence when newer.
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

	got := GetLatestTodoPhasesFromEntries(entries)
	if len(got) != 1 || len(got[0].Tasks) != 2 {
		t.Fatalf("unexpected replayed phases: %+v", got)
	}
	if got[0].Tasks[0].Status != StatusCompleted || got[0].Tasks[1].Status != StatusBlocked || got[0].Tasks[1].Blocker != "waiting" {
		t.Fatalf("unexpected replayed tasks: %+v", got[0].Tasks)
	}
}

func TestWidgetLines(t *testing.T) {
	if lines := RenderWidgetLines(nil, false); lines != nil {
		t.Fatalf("expected nil for empty phases, got %v", lines)
	}

	phases := []TodoPhase{
		{
			Name: "Foundation",
			Tasks: []TodoItem{
				{Content: "done item", Status: StatusCompleted},
				{Content: "active item", Status: StatusInProgress},
				{Content: "blocked item", Status: StatusBlocked, Blocker: "waiting"},
			},
		},
		{
			Name: "Polish",
			Tasks: []TodoItem{
				{Content: "clear\x1b[2Jscreen\x07bell", Status: StatusPending},
			},
		},
	}

	collapsed := RenderWidgetLines(phases, false)
	joined := strings.Join(collapsed, "\n")
	for _, want := range []string{"TODO", "I. Foundation · 1/3", "☑ done item", "☐ active item", "☐ blocked item (blocked: waiting)", "II. Polish · 0/1", "└─────"} {
		if !strings.Contains(joined, want) {
			t.Errorf("collapsed widget missing %q:\n%s", want, joined)
		}
	}

	// Expanded includes Polish's sanitized task without ANSI/bell bytes.
	expanded := RenderWidgetLines(phases, true)
	expJoined := strings.Join(expanded, "\n")
	if strings.Contains(expJoined, "\x1b[2J") || strings.Contains(expJoined, "\x07") {
		t.Fatalf("widget did not sanitize control sequences:\n%q", expJoined)
	}
	if !strings.Contains(expJoined, "clearscreenbell") {
		t.Errorf("sanitized text missing:\n%s", expJoined)
	}
}

func TestStopReminder(t *testing.T) {
	phases := []TodoPhase{
		{
			Name: "Work",
			Tasks: []TodoItem{
				{Content: "done", Status: StatusCompleted},
				{Content: "blocked", Status: StatusBlocked, Blocker: "user"},
				{Content: "open 1", Status: StatusInProgress},
				{Content: "open 2", Status: StatusPending},
			},
		},
	}
	text, count := BuildStopReminder(phases, 1, 3)
	if count != 2 {
		t.Fatalf("got count %d, want 2", count)
	}
	for _, want := range []string{
		"<system-reminder>",
		"You stopped with 2 incomplete todo item(s):",
		"- Work",
		"  - open 1",
		"  - open 2",
		"(Reminder 1/3)",
		"</system-reminder>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("reminder missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "blocked") || strings.Contains(text, "done") {
		t.Errorf("reminder should exclude blocked/completed items:\n%s", text)
	}

	// All closed or blocked -> no reminder.
	noneText, noneCount := BuildStopReminder([]TodoPhase{{
		Name: "Work",
		Tasks: []TodoItem{
			{Content: "done", Status: StatusCompleted},
			{Content: "blocked", Status: StatusBlocked},
		},
	}}, 1, 3)
	if noneCount != 0 || noneText != "" {
		t.Fatalf("expected no reminder, got count=%d text=%q", noneCount, noneText)
	}
}

func TestRegisterDoesNotPanic(t *testing.T) {
	e := sdk.New("oh-my-pig-test")
	Register(e)
}
