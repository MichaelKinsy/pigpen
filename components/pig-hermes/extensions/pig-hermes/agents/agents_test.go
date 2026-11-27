package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

// --- Cron Parser, Next-Run & Store CRUD Tests ---

func TestParseScheduleAndComputeNextRun(t *testing.T) {
	base := time.Date(2026, 6, 15, 10, 30, 0, 0, time.UTC) // Monday

	t.Run("5-field cron expression", func(t *testing.T) {
		sched, err := ParseSchedule("0 9 * * *", base)
		if err != nil {
			t.Fatalf("ParseSchedule failed: %v", err)
		}
		if sched.Kind != "cron" || sched.Expr != "0 9 * * *" {
			t.Fatalf("unexpected schedule: %+v", sched)
		}
		next, err := ComputeNextRun(sched, base)
		if err != nil || next == nil {
			t.Fatalf("ComputeNextRun failed: %v (%v)", next, err)
		}
		want := time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	})

	t.Run("cron steps ranges and named weekdays/months", func(t *testing.T) {
		sched, err := ParseSchedule("*/15 9-17 * JUN MON-FRI", base)
		if err != nil {
			t.Fatalf("ParseSchedule failed: %v", err)
		}
		next, err := ComputeNextRun(sched, base)
		if err != nil || next == nil {
			t.Fatalf("ComputeNextRun failed: %v", err)
		}
		want := time.Date(2026, 6, 15, 10, 45, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	})

	t.Run("natural weekday phrase", func(t *testing.T) {
		sched, err := ParseSchedule("every monday 9am", base)
		if err != nil {
			t.Fatalf("ParseSchedule failed: %v", err)
		}
		if sched.Kind != "cron" || sched.Expr != "0 9 * * 1" {
			t.Fatalf("unexpected schedule: %+v", sched)
		}
		next, err := ComputeNextRun(sched, base)
		if err != nil || next == nil {
			t.Fatalf("ComputeNextRun failed: %v", err)
		}
		want := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	})

	t.Run("interval schedules", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			mins  int
		}{
			{"30m", 30},
			{"2h", 120},
			{"every 15m", 15},
			{"every hour", 60},
			{"1d", 1440},
		} {
			sched, err := ParseSchedule(tc.input, base)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) error: %v", tc.input, err)
			}
			if sched.Kind != "interval" || sched.Minutes != tc.mins {
				t.Fatalf("ParseSchedule(%q) = %+v, want interval %dm", tc.input, sched, tc.mins)
			}
			next, err := ComputeNextRun(sched, base)
			if err != nil || next == nil {
				t.Fatalf("ComputeNextRun(%q) failed: %v", tc.input, err)
			}
			want := base.Add(time.Duration(tc.mins) * time.Minute)
			if !next.Equal(want) {
				t.Fatalf("next(%q) = %s, want %s", tc.input, next, want)
			}
		}
	})

	t.Run("RFC3339 one-shot and relative one-shot", func(t *testing.T) {
		sched, err := ParseSchedule("2026-07-01T14:00:00Z", base)
		if err != nil {
			t.Fatalf("ParseSchedule RFC3339 failed: %v", err)
		}
		if sched.Kind != "once" {
			t.Fatalf("kind = %q, want once", sched.Kind)
		}
		next, err := ComputeNextRun(sched, base)
		if err != nil || next == nil {
			t.Fatalf("ComputeNextRun failed: %v", err)
		}
		if next.Format(time.RFC3339) != "2026-07-01T14:00:00Z" {
			t.Fatalf("next = %s, want 2026-07-01T14:00:00Z", next.Format(time.RFC3339))
		}

		past, err := ParseSchedule("2026-01-01T00:00:00Z", base)
		if err != nil {
			t.Fatalf("ParseSchedule past failed: %v", err)
		}
		if nextPast, err := ComputeNextRun(past, base); err != nil || nextPast != nil {
			t.Fatalf("expected nil next run for past one-shot, got %v, %v", nextPast, err)
		}

		rel, err := ParseSchedule("in 45m", base)
		if err != nil {
			t.Fatalf("ParseSchedule 'in 45m' failed: %v", err)
		}
		if rel.Kind != "once" || rel.RunAt != base.Add(45*time.Minute).Format(time.RFC3339) {
			t.Fatalf("unexpected relative schedule: %+v", rel)
		}
	})

	t.Run("invalid schedules rejected", func(t *testing.T) {
		for _, bad := range []string{
			"",
			"not a schedule",
			"60 * * * *",
			"* 25 * * *",
			"* * 32 * *",
			"* * * 13 *",
			"* * * * 9",
			"*/0 * * * *",
			"5-2 * * * *",
			"0m",
			"2026-99-99T00:00:00Z",
		} {
			if _, err := ParseSchedule(bad, base); err == nil {
				t.Errorf("expected ParseSchedule(%q) to fail, but succeeded", bad)
			}
		}
	})
}

func TestCronStoreCRUDAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "cron", "jobs.json")
	store := NewCronStore(jobsFile)
	fixedNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }

	// Initially empty list.
	jobs, err := store.List(true)
	if err != nil {
		t.Fatalf("List empty failed: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}

	// Create job.
	created, err := store.Create(CreateJobRequest{
		Name:     "morning-check",
		Schedule: "0 9 * * *",
		Prompt:   "Check overnight CI status",
		Skills:   []string{"ci-triage"},
		Deliver:  "local",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.ID == "" || created.State != "scheduled" || !created.Enabled {
		t.Fatalf("unexpected created job: %+v", created)
	}
	if created.NextRunAt != "2026-06-16T09:00:00Z" {
		t.Fatalf("NextRunAt = %q, want 2026-06-16T09:00:00Z", created.NextRunAt)
	}

	// Verify file exists and has Hermes-compatible JSON shape {"jobs": [...]}.
	raw, err := os.ReadFile(jobsFile)
	if err != nil {
		t.Fatalf("ReadFile jobs.json failed: %v", err)
	}
	var disk struct {
		Jobs []CronJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil || len(disk.Jobs) != 1 {
		t.Fatalf("unexpected jobs.json content: %s (err=%v)", string(raw), err)
	}

	// Get by ID and by case-insensitive name.
	gotByID, err := store.Get(created.ID)
	if err != nil || gotByID.Name != "morning-check" {
		t.Fatalf("Get by ID failed: %+v, %v", gotByID, err)
	}
	gotByName, err := store.Get("MORNING-CHECK")
	if err != nil || gotByName.ID != created.ID {
		t.Fatalf("Get by name failed: %+v, %v", gotByName, err)
	}

	// Pause and Resume.
	paused, err := store.Pause(created.ID)
	if err != nil || paused.Enabled || paused.State != "paused" || paused.PausedAt == "" {
		t.Fatalf("Pause failed: %+v, %v", paused, err)
	}
	activeOnly, err := store.List(false)
	if err != nil || len(activeOnly) != 0 {
		t.Fatalf("expected 0 active jobs while paused, got %d (%v)", len(activeOnly), err)
	}

	resumed, err := store.Resume(created.ID)
	if err != nil || !resumed.Enabled || resumed.State != "scheduled" || resumed.PausedAt != "" {
		t.Fatalf("Resume failed: %+v, %v", resumed, err)
	}

	// MarkRun advances next_run_at.
	ranAt := time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)
	marked, err := store.MarkRun(created.ID, "ok", "", ranAt)
	if err != nil {
		t.Fatalf("MarkRun failed: %v", err)
	}
	if marked.LastStatus != "ok" || marked.NextRunAt != "2026-06-17T09:00:00Z" {
		t.Fatalf("unexpected marked job: %+v", marked)
	}

	// Remove.
	removed, err := store.Remove(created.ID)
	if err != nil || removed.ID != created.ID {
		t.Fatalf("Remove failed: %+v, %v", removed, err)
	}
	if _, err := store.Get(created.ID); err == nil {
		t.Fatal("expected Get after Remove to fail")
	}

	// Concurrent Create calls must be race-safe and preserve all jobs.
	var wg sync.WaitGroup
	const workers = 8
	for i := range workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := store.Create(CreateJobRequest{
				Name:     "job-" + string(rune('a'+n)),
				Schedule: "30m",
				Prompt:   "ping",
			})
			if err != nil {
				t.Errorf("concurrent Create %d failed: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	all, err := store.List(true)
	if err != nil {
		t.Fatalf("List after concurrent creates failed: %v", err)
	}
	if len(all) != workers {
		t.Fatalf("expected %d jobs after concurrent creates, got %d", workers, len(all))
	}
}

func TestExecuteCronManageActions(t *testing.T) {
	store := NewCronStore(filepath.Join(t.TempDir(), "jobs.json"))
	var zeroCtx sdk.Context

	res := executeCronManage(zeroCtx, store, map[string]any{
		"action":   "create",
		"name":     "hourly-sync",
		"schedule": "1h",
		"prompt":   "Sync workspace",
	})
	if res.IsError {
		t.Fatalf("create returned error: %s", res.Content)
	}

	listRes := executeCronManage(zeroCtx, store, map[string]any{"action": "list"})
	if listRes.IsError || !strings.Contains(listRes.Content, "hourly-sync") {
		t.Fatalf("list unexpected: %s", listRes.Content)
	}

	getRes := executeCronManage(zeroCtx, store, map[string]any{"action": "get", "job_id": "hourly-sync"})
	if getRes.IsError || !strings.Contains(getRes.Content, "Sync workspace") {
		t.Fatalf("get unexpected: %s", getRes.Content)
	}

	pauseRes := executeCronManage(zeroCtx, store, map[string]any{"action": "pause", "job_id": "hourly-sync"})
	if pauseRes.IsError || !strings.Contains(pauseRes.Content, `"paused"`) {
		t.Fatalf("pause unexpected: %s", pauseRes.Content)
	}

	resumeRes := executeCronManage(zeroCtx, store, map[string]any{"action": "resume", "job_id": "hourly-sync"})
	if resumeRes.IsError || !strings.Contains(resumeRes.Content, `"scheduled"`) {
		t.Fatalf("resume unexpected: %s", resumeRes.Content)
	}

	removeRes := executeCronManage(zeroCtx, store, map[string]any{"action": "remove", "job_id": "hourly-sync"})
	if removeRes.IsError {
		t.Fatalf("remove unexpected: %s", removeRes.Content)
	}

	badRes := executeCronManage(zeroCtx, store, map[string]any{"action": "unknown"})
	if !badRes.IsError {
		t.Fatal("expected unknown action to return error")
	}
}

// --- Todo Store & Compaction Injection Tests ---

func TestTodoExecuteAndMergeAndCompaction(t *testing.T) {
	store := todo.NewStore()
	sid := "sess-todo-1"

	// Read empty list.
	res, mutated, items := executeTodo(store, sid, map[string]any{})
	if res.IsError || mutated || len(items) != 0 {
		t.Fatalf("empty read unexpected: err=%v mutated=%v items=%v", res.IsError, mutated, items)
	}
	if !strings.Contains(res.Content, "No todo items.") {
		t.Fatalf("expected 'No todo items.' in %s", res.Content)
	}

	// Replace with two items.
	res, mutated, items = executeTodo(store, sid, map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "Write parser", "status": "in_progress"},
			map[string]any{"id": "2", "content": "Write tests", "status": "pending"},
		},
	})
	if res.IsError || !mutated || len(items) != 2 {
		t.Fatalf("replace failed: err=%v mutated=%v content=%s", res.IsError, mutated, res.Content)
	}

	// Merge updates item 1 and appends item 3.
	res, mutated, items = executeTodo(store, sid, map[string]any{
		"merge": true,
		"todos": []any{
			map[string]any{"id": "1", "content": "Write parser", "status": "completed"},
			map[string]any{"id": "3", "content": "Run vet", "status": "pending"},
		},
	})
	if res.IsError || !mutated || len(items) != 3 {
		t.Fatalf("merge failed: err=%v mutated=%v items=%+v content=%s", res.IsError, mutated, items, res.Content)
	}
	if items[0].Status != "completed" || items[1].ID != "2" || items[2].ID != "3" {
		t.Fatalf("unexpected merged items: %+v", items)
	}

	// Compaction injection should include active todos.
	inj := BuildCompactionTodoInjection(store, sid)
	if !strings.Contains(inj, TodoInjectionHeader) || !strings.Contains(inj, "Run vet") {
		t.Fatalf("unexpected compaction injection: %q", inj)
	}

	// Completing all items suppresses compaction injection.
	_, _, _ = executeTodo(store, sid, map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "Done", "status": "completed"},
		},
	})
	if inj := BuildCompactionTodoInjection(store, sid); inj != "" {
		t.Fatalf("expected empty compaction injection when no active items, got %q", inj)
	}

	// Invalid status returns error and does not mutate.
	res, mutated, _ = executeTodo(store, sid, map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "Bad", "status": "bogus"},
		},
	})
	if !res.IsError || mutated {
		t.Fatalf("expected error on invalid status, got isError=%v mutated=%v", res.IsError, mutated)
	}

	// Legacy alias helper.
	if !IsTodoToolName("todo_list") || !IsTodoToolName("todo") || IsTodoToolName("other") {
		t.Fatal("IsTodoToolName returned unexpected result")
	}
}

// --- Clarify Headless & Interactive UI Tests ---

type stubClarifyUI struct {
	hasUI      bool
	selectResp []struct {
		val any
		ok  bool
		err error
	}
	selectIdx int
	inputVal  any
	inputOK   bool
	inputErr  error
}

func (s *stubClarifyUI) HasUI() bool { return s.hasUI }

func (s *stubClarifyUI) Select(_ string, _ []string) (any, bool, error) {
	if s.selectIdx >= len(s.selectResp) {
		return nil, false, nil
	}
	r := s.selectResp[s.selectIdx]
	s.selectIdx++
	return r.val, r.ok, r.err
}

func (s *stubClarifyUI) Input(_, _ string) (any, bool, error) {
	return s.inputVal, s.inputOK, s.inputErr
}

func TestClarifyHeadlessAndUI(t *testing.T) {
	t.Run("missing question errors", func(t *testing.T) {
		res := executeClarify(&stubClarifyUI{hasUI: false}, map[string]any{})
		if !res.IsError {
			t.Fatal("expected error for missing question")
		}
	})

	t.Run("headless returns structured non_interactive notice", func(t *testing.T) {
		res := executeClarify(&stubClarifyUI{hasUI: false}, map[string]any{
			"question": "Which database?",
			"choices":  []any{"postgres", "sqlite"},
			"default":  "sqlite",
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if out["outcome"] != "non_interactive" || out["user_response"] != "sqlite" {
			t.Fatalf("unexpected headless response: %+v", out)
		}
	})

	t.Run("interactive open-ended input", func(t *testing.T) {
		ui := &stubClarifyUI{hasUI: true, inputVal: "custom answer", inputOK: true}
		res := executeClarify(ui, map[string]any{
			"question": "Project name?",
			"default":  "demo",
		})
		if res.IsError || !strings.Contains(res.Content, `"custom answer"`) {
			t.Fatalf("unexpected input response: %s", res.Content)
		}
	})

	t.Run("interactive single choice", func(t *testing.T) {
		ui := &stubClarifyUI{
			hasUI: true,
			selectResp: []struct {
				val any
				ok  bool
				err error
			}{{val: "postgres", ok: true}},
		}
		res := executeClarify(ui, map[string]any{
			"question": "Database?",
			"choices":  []any{"postgres", "sqlite"},
		})
		if res.IsError || !strings.Contains(res.Content, `"postgres"`) {
			t.Fatalf("unexpected select response: %s", res.Content)
		}
	})

	t.Run("interactive multi-select toggle and submit", func(t *testing.T) {
		ui := &stubClarifyUI{
			hasUI: true,
			selectResp: []struct {
				val any
				ok  bool
				err error
			}{
				{val: "[ ] lint", ok: true},
				{val: "[ ] test", ok: true},
				{val: clarifyDoneOption, ok: true},
			},
		}
		res := executeClarify(ui, map[string]any{
			"question":       "Checks to run?",
			"choices":        []any{"lint", "test", "bench"},
			"allow_multiple": true,
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out["outcome"] != "submitted" {
			t.Fatalf("outcome = %v, want submitted", out["outcome"])
		}
		arr, ok := out["user_response"].([]any)
		if !ok || len(arr) != 2 || arr[0] != "lint" || arr[1] != "test" {
			t.Fatalf("unexpected multi-select response: %+v", out["user_response"])
		}
	})

	t.Run("interactive cancelled", func(t *testing.T) {
		ui := &stubClarifyUI{hasUI: true, inputOK: false}
		res := executeClarify(ui, map[string]any{"question": "Proceed?"})
		if res.IsError || !strings.Contains(res.Content, `"cancelled"`) {
			t.Fatalf("unexpected cancel response: %s", res.Content)
		}
	})
}

// --- Delegate Task Routing, Depth, Blocked-Tools, Events & Usage Tests ---

func TestDelegateRouteDepthBlockedToolsAndParallel(t *testing.T) {
	cfg := config.Default()
	cfg.MaxSpawnDepth = 1
	cfg.MaxConcurrent = 2

	mgr := NewDelegateManager(cfg)
	ledger := usage.NewLedger()
	mgr.ledger = ledger

	// Register a Phase 2 route resolver matching category="research".
	delegation.RegisterRouteResolver(delegation.RouteResolverFunc(func(req delegation.SpawnRequest) (delegation.RouteDecision, bool, error) {
		if req.Category == "research" {
			return delegation.RouteDecision{
				Model:         "anthropic/claude-sonnet-4",
				ThinkingLevel: "high",
				Reason:        "research-route",
			}, true, nil
		}
		return delegation.RouteDecision{}, false, nil
	}))

	var (
		mu             sync.Mutex
		emittedStarts  []events.SubagentStartPayload
		emittedStops   []events.SubagentStopPayload
		capturedOpts   []sdk.CreateAgentSessionOptions
		capturedReqs   []delegation.SpawnRequest
		activeChildren atomic.Int32
		maxParallel    atomic.Int32
	)

	mgr.emit = func(_ sdk.Context, channel string, payload any) {
		mu.Lock()
		defer mu.Unlock()
		switch channel {
		case events.ChannelSubagentStart:
			if p, ok := payload.(events.SubagentStartPayload); ok {
				emittedStarts = append(emittedStarts, p)
			}
		case events.ChannelSubagentStop:
			if p, ok := payload.(events.SubagentStopPayload); ok {
				emittedStops = append(emittedStops, p)
			}
		}
	}

	mgr.runner = func(_ context.Context, _ sdk.Context, req delegation.SpawnRequest, opts sdk.CreateAgentSessionOptions, _ string, onStart func(string)) childRunOutcome {
		realID := "child-" + req.TaskID
		if onStart != nil {
			onStart(realID)
		}
		cur := activeChildren.Add(1)
		for {
			prev := maxParallel.Load()
			if cur <= prev || maxParallel.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		activeChildren.Add(-1)

		mu.Lock()
		capturedOpts = append(capturedOpts, opts)
		capturedReqs = append(capturedReqs, req)
		mu.Unlock()

		return childRunOutcome{
			ChildSessionID: realID,
			Summary:        "done: " + req.Goal,
			InputTokens:    120,
			OutputTokens:   45,
			Cost:           0.012,
			Turns:          2,
			ToolCalls:      1,
		}
	}

	var zeroCtx sdk.Context

	// 1. Parallel batch of 2 tasks (one routed by category, one requesting blocked tools).
	res := mgr.Execute(zeroCtx, map[string]any{
		"tasks": []any{
			map[string]any{
				"goal":     "Investigate issue A",
				"category": "research",
				"toolsets": []any{"read", "delegate_task", "clarify", "memory", "cronjob_manage", "send_message", "grep"},
			},
			map[string]any{
				"goal":     "Investigate issue B",
				"identity": "auditor",
			},
		},
	})
	if res.IsError {
		t.Fatalf("parallel execute failed: %s", res.Content)
	}
	if len(emittedStarts) != 2 || len(emittedStops) != 2 {
		t.Fatalf("expected 2 start & 2 stop events, got %d / %d", len(emittedStarts), len(emittedStops))
	}
	// Verify start and stop events share the exact same real child session ID.
	startsByTask := make(map[string]string, len(emittedStarts))
	for _, st := range emittedStarts {
		startsByTask[st.TaskID] = st.ChildSessionID
		if !strings.HasPrefix(st.ChildSessionID, "child-") {
			t.Errorf("start event used synthetic ID %q instead of real child session ID", st.ChildSessionID)
		}
	}
	for _, sp := range emittedStops {
		if startsByTask[sp.TaskID] != sp.ChildSessionID {
			t.Errorf("start/stop child session ID mismatch for task %s: start=%q stop=%q", sp.TaskID, startsByTask[sp.TaskID], sp.ChildSessionID)
		}
	}
	foundRouted := false
	for i, r := range capturedReqs {
		opts := capturedOpts[i]
		// ExcludeTools must always contain all 5 blocked tools.
		for _, blocked := range BlockedToolsList() {
			found := false
			for _, ex := range opts.ExcludeTools {
				if ex == blocked {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("ExcludeTools missing %q: %v", blocked, opts.ExcludeTools)
			}
		}
		if r.Category == "research" {
			foundRouted = true
			if r.Model != "anthropic/claude-sonnet-4" || r.ThinkingLevel != "high" {
				t.Errorf("route override not applied: model=%q thinking=%q", r.Model, r.ThinkingLevel)
			}
			if len(opts.Tools) != 2 || opts.Tools[0] != "read" || opts.Tools[1] != "grep" {
				t.Errorf("blocked tools not filtered from Tools allowlist: %v", opts.Tools)
			}
		}
	}
	if !foundRouted {
		t.Fatal("research task was not captured")
	}

	// Verify usage ledger recorded both child sessions.
	inTok, outTok, _, turns, toolCalls := ledger.Totals()
	if inTok != 240 || outTok != 90 || turns != 4 || toolCalls != 2 {
		t.Fatalf("unexpected usage totals: in=%d out=%d turns=%d tools=%d", inTok, outTok, turns, toolCalls)
	}

	// 2. Exceeding max_concurrent rejected.
	overRes := mgr.Execute(zeroCtx, map[string]any{
		"tasks": []any{
			map[string]any{"goal": "t1"},
			map[string]any{"goal": "t2"},
			map[string]any{"goal": "t3"},
		},
	})
	if !overRes.IsError || !strings.Contains(overRes.Content, "max_concurrent") {
		t.Fatalf("expected max_concurrent error, got: %s", overRes.Content)
	}

	// 3. Depth limit enforcement.
	mgr.SetSessionDepth("default", 1)
	depthRes := mgr.Execute(zeroCtx, map[string]any{"goal": "grandchild"})
	if !depthRes.IsError || !strings.Contains(depthRes.Content, "depth limit reached") {
		t.Fatalf("expected depth limit error, got: %s", depthRes.Content)
	}
	mgr.SetSessionDepth("default", 0)

	// 4. Child runner error propagates cleanly.
	mgr.runner = func(_ context.Context, _ sdk.Context, _ delegation.SpawnRequest, _ sdk.CreateAgentSessionOptions, _ string, _ func(string)) childRunOutcome {
		return childRunOutcome{Err: errors.New("boom")}
	}
	errRes := mgr.Execute(zeroCtx, map[string]any{"goal": "fail task"})
	if !errRes.IsError || !strings.Contains(errRes.Content, "boom") {
		t.Fatalf("expected error result with 'boom', got: %s", errRes.Content)
	}
}

func TestDelegateToolsetExpansionAndOutcomeClassification(t *testing.T) {
	// Toolset expansion expands Hermes toolset names and strips blocked tools.
	got := FilterAllowedTools([]string{"file", "web", "todo", "delegation", "cronjob", "clarify", "memory", "code_execution", "custom_tool"})
	want := []string{"read_file", "write_file", "patch", "search_files", "web_search", "web_extract", "todo_list", "execute_code", "custom_tool"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("FilterAllowedTools = %v, want %v", got, want)
	}

	// classifyChildOutcome distinguishes completed, cancelled (aborted / ctx err), and error.
	if err, cancelled := classifyChildOutcome(nil, nil, "stop", ""); err != nil || cancelled {
		t.Fatalf("stop: got (%v, %v), want (nil, false)", err, cancelled)
	}
	if err, cancelled := classifyChildOutcome(nil, context.Canceled, "", ""); err == nil || !cancelled {
		t.Fatalf("ctx canceled with nil promptErr: got (%v, %v), want (err, true)", err, cancelled)
	}
	if err, cancelled := classifyChildOutcome(nil, nil, "aborted", "user interrupted"); err == nil || !cancelled || !strings.Contains(err.Error(), "user interrupted") {
		t.Fatalf("aborted stopReason: got (%v, %v), want (user interrupted, true)", err, cancelled)
	}
	if err, cancelled := classifyChildOutcome(nil, nil, "error", "rate limited"); err == nil || cancelled || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error stopReason: got (%v, %v), want (rate limited, false)", err, cancelled)
	}

	// Cancelled child outcome reports status="cancelled" in DelegateManager.Execute.
	mgr := NewDelegateManager(config.Default())
	mgr.emit = nil
	mgr.ledger = nil
	mgr.runner = func(_ context.Context, _ sdk.Context, _ delegation.SpawnRequest, _ sdk.CreateAgentSessionOptions, _ string, onStart func(string)) childRunOutcome {
		onStart("real-child-abort")
		return childRunOutcome{
			ChildSessionID: "real-child-abort",
			Err:            errors.New("aborted by user"),
			Cancelled:      true,
		}
	}
	res := mgr.Execute(sdk.Context{}, map[string]any{"goal": "aborted task"})
	if !res.IsError || !strings.Contains(res.Content, `"status": "cancelled"`) {
		t.Fatalf("expected cancelled status, got: %s", res.Content)
	}
}

func TestCronStorePreservesUnknownJSONFieldsAndLocalTime(t *testing.T) {
	dir := t.TempDir()
	jobsFile := filepath.Join(dir, "jobs.json")

	// Seed jobs.json with extra top-level metadata and per-job/per-schedule Hermes fields.
	initial := `{
  "version": 2,
  "metadata": {"source": "hermes-python"},
  "jobs": [
    {
      "id": "job-ext-1",
      "name": "shared-job",
      "prompt": "Check logs",
      "schedule": {
        "kind": "cron",
        "expr": "0 9 * * *",
        "display": "0 9 * * *",
        "timezone": "America/New_York"
      },
      "schedule_display": "0 9 * * *",
      "enabled": true,
      "state": "scheduled",
      "created_at": "2026-06-01T09:00:00-04:00",
      "origin": {"platform": "telegram", "chat_id": "12345"},
      "repeat": {"times": 5, "completed": 1},
      "custom_flag": true
    }
  ]
}`
	if err := os.WriteFile(jobsFile, []byte(initial), 0o600); err != nil {
		t.Fatalf("write seed jobs.json: %v", err)
	}

	store := NewCronStore(jobsFile)
	// Default clock must use local time rather than forcing UTC.
	localNow := store.now()
	if localNow.Location() != time.Local {
		t.Fatalf("expected NewCronStore default clock in time.Local, got %v", localNow.Location())
	}

	// Mutate via Pause, Resume, MarkRun, and Create; verify all unknown fields survive.
	if _, err := store.Pause("job-ext-1"); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	if _, err := store.Resume("job-ext-1"); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	loc := time.FixedZone("EST", -5*3600)
	ranAt := time.Date(2026, 6, 15, 9, 0, 0, 0, loc)
	marked, err := store.MarkRun("job-ext-1", "ok", "", ranAt)
	if err != nil {
		t.Fatalf("MarkRun failed: %v", err)
	}
	// Verify local timezone preserved in NextRunAt (not coerced to UTC).
	if !strings.HasSuffix(marked.NextRunAt, "-05:00") {
		t.Fatalf("expected NextRunAt in -05:00 local offset, got %q", marked.NextRunAt)
	}

	raw, err := os.ReadFile(jobsFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal jobs.json: %v", err)
	}
	if doc["version"] != float64(2) {
		t.Fatalf("top-level 'version' lost: %+v", doc)
	}
	meta, ok := doc["metadata"].(map[string]any)
	if !ok || meta["source"] != "hermes-python" {
		t.Fatalf("top-level 'metadata' lost: %+v", doc["metadata"])
	}
	jobsList, ok := doc["jobs"].([]any)
	if !ok || len(jobsList) != 1 {
		t.Fatalf("unexpected jobs array: %+v", doc["jobs"])
	}
	jobMap, _ := jobsList[0].(map[string]any)
	origin, _ := jobMap["origin"].(map[string]any)
	if origin["platform"] != "telegram" || origin["chat_id"] != "12345" {
		t.Fatalf("per-job 'origin' lost: %+v", jobMap["origin"])
	}
	repeat, _ := jobMap["repeat"].(map[string]any)
	if repeat["times"] != float64(5) {
		t.Fatalf("per-job 'repeat' lost: %+v", jobMap["repeat"])
	}
	if jobMap["custom_flag"] != true {
		t.Fatalf("per-job 'custom_flag' lost: %+v", jobMap["custom_flag"])
	}
	schedMap, _ := jobMap["schedule"].(map[string]any)
	if schedMap["timezone"] != "America/New_York" {
		t.Fatalf("per-schedule 'timezone' lost: %+v", schedMap)
	}
}

func TestRestoreTodosFromEntriesAndZeroContext(t *testing.T) {
	store := todo.NewStore()
	// Safe on zero-value sdk.Context (must not panic).
	restoreTodosFromSession(sdk.Context{}, store)

	entries := []json.RawMessage{
		json.RawMessage(`{"type":"message","id":"m1"}`),
		json.RawMessage(`{"type":"custom","customType":"hermes_todo","data":{"session_id":"sess-42","todos":[{"id":"1","content":"Old item","status":"pending"}]}}`),
		json.RawMessage(`{"type":"custom","customType":"other_ext","data":{"foo":"bar"}}`),
		json.RawMessage(`{"type":"custom","customType":"hermes_todo","data":{"session_id":"sess-42","todos":[{"id":"1","content":"Updated item","status":"completed"},{"id":"2","content":"Next item","status":"in_progress"}]}}`),
	}
	if ok := restoreTodosFromEntries(entries, "sess-42", store); !ok {
		t.Fatal("expected restoreTodosFromEntries to restore latest hermes_todo entry")
	}
	got := store.Get("sess-42")
	if len(got) != 2 || got[0].Content != "Updated item" || got[0].Status != "completed" || got[1].ID != "2" {
		t.Fatalf("unexpected restored todos: %+v", got)
	}
}

func TestRegisterWiresAllToolsWithoutPanic(t *testing.T) {
	ext := sdk.New("test-agents")
	p := paths.ProfilePaths{
		CronJobsFile: filepath.Join(t.TempDir(), "cron", "jobs.json"),
	}
	Register(ext, p, config.Default())
}
