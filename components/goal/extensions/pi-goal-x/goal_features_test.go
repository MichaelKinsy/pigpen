package pi_goal_x

// Twins of tests/goal-features.test.ts (F1-F7) and tests/goal-network-recovery.test.ts (classification).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	featFile = "goal-features"
	// The exact reported failures of goal-network-recovery.test.ts.
	netReported503             = `503: {"type":"server_error","message":"Error from provider (Console): Upstream request failed: Endpoint is unavailable."}`
	netReported503AfterRetries = "Retry failed after 3 attempts: " + netReported503
	netReportedHTTP2           = `Post "https://chatgpt.com/backend-api/codex/responses": stream error: stream ID 1; PROTOCOL_ERROR; received from peer`
)

func netErr(t *testing.T, message string) *jsObject {
	t.Helper()
	o := newObject()
	o.set("role", "assistant")
	o.set("stopReason", "error")
	o.set("errorMessage", message)
	return o
}

func TestGoalNetworkClassification(t *testing.T) {
	f := "goal-network-recovery"
	tw(t, f, "classification: HTTP/2 stream PROTOCOL_ERROR is transient", func(t *testing.T) {
		if !isNetworkErrorAssistantMessage(netErr(t, netReportedHTTP2)) {
			t.Error("the reported transport failure must engage goal-level recovery")
		}
	})
	tw(t, f, "classification: exact reported 503 server_error payload is a network error", func(t *testing.T) {
		if !isNetworkErrorAssistantMessage(netErr(t, netReported503)) {
			t.Error("the raw 503 server_error payload must classify as recoverable")
		}
		if !isNetworkErrorAssistantMessage(netErr(t, netReported503AfterRetries)) {
			t.Error("the payload after Pi exhausts its own retries must still classify")
		}
		if !hasNetworkErrorAssistantMessage([]any{netErr(t, netReported503AfterRetries)}) {
			t.Error("hasNetworkErrorAssistantMessage")
		}
	})
	tw(t, f, "classification: 429 rate-limit payloads are transient", func(t *testing.T) {
		if !isNetworkErrorAssistantMessage(netErr(t, "429 status code (no body)")) {
			t.Error("pi's compact 429 formatter must classify")
		}
		reported := `{"message":"Provider returned error","code":429,"metadata":{"raw":"stealth/ox-alpha is temporarily rate-limited upstream. Please retry shortly.","provider_name":"Stealth","limit_source":"upstream_provider_shared_pool"}}`
		if !isNetworkErrorAssistantMessage(netErr(t, "Retry failed after 3 attempts: 429: "+reported)) {
			t.Error("the exact field-reported 429 payload must classify")
		}
		if isNetworkErrorAssistantMessage(netErr(t, "429: quota exceeded for this billing period")) {
			t.Error("quota/billing exhaustion stays non-transient even with a 429 code")
		}
	})
	tw(t, f, "classification: non-transient errors stay non-recoverable", func(t *testing.T) {
		for _, message := range []string{
			`401: {"type":"authentication_error","message":"invalid api key"}`,
			"400: invalid_request_error: malformed payload",
			"Provider finish_reason: content_filter",
		} {
			if isNetworkErrorAssistantMessage(netErr(t, message)) {
				t.Errorf("must not classify as network error: %s", message)
			}
		}
	})
	tw(t, f, "classification: generic provider finish_reason: error is transient", func(t *testing.T) {
		if !isNetworkErrorAssistantMessage(netErr(t, "Provider finish_reason: error")) {
			t.Error("the generic provider finish_reason: error must engage goal-level recovery")
		}
	})
}

// featGoal is makeGoal: createGoal(...) at Date.UTC(2026, 8, 5), with an optional token budget.
func featGoal(objective string, sisyphus bool, budget float64) *jsObject {
	g := createGoal(objective, true, sisyphus, false, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC).UnixMilli())
	if budget > 0 {
		g.set("tokenBudget", budget)
	}
	return g
}

func featTaskList(t *testing.T, tasks string) *jsObject {
	t.Helper()
	return jo(t, `{"tasks":`+tasks+`,"blockCompletion":false,"proposedAt":"2026-08-05T00:00:00.000Z"}`)
}

// featHost is the coreHarness ctx: a UI session that is idle, answers the evidence prompt and confirms.
type featHost struct{ fakeHost }

func (h *featHost) Input(string, string) (string, bool) { return "evidence from test", true }

func featCore(t *testing.T) (*core, *featHost) {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
	invalidateGoalSettingsCache()
	t.Cleanup(func() { invalidateGoalPoolCache(); invalidateGoalSettingsCache() })
	h := &featHost{fakeHost{cwd: cwd}}
	return newCore(h), h
}

func (h *featHost) notified(sub string) bool {
	for _, e := range h.events {
		if e[0] == "notify" && strings.Contains(e[2].(string), sub) {
			return true
		}
	}
	return false
}

func featLedger(cwd string) string {
	b, _ := os.ReadFile(filepath.Join(cwd, ".pi", "goals", "goal_events.jsonl"))
	return string(b)
}

func featAuditorEvents(t *testing.T, ctx storage) []*jsObject {
	t.Helper()
	invalidateGoalLedgerCache()
	var out []*jsObject
	for _, e := range readGoalLedger(ctx).events {
		if evStr(e, "type") == "auditor_toggled" {
			out = append(out, e)
		}
	}
	return out
}

func TestGoalFeatures(t *testing.T) {
	tw(t, featFile, "shows counts, next pending with contracts, and recent completions", func(t *testing.T) {
		goal := featGoal("F1 test", false, 0)
		goal.set("taskList", featTaskList(t, `[{"id":"t1","title":"Done task","status":"complete","evidence":"verified green"},{"id":"t2","title":"Next task","status":"pending","verificationContract":"run the suite"}]`))
		block := buildGoalTaskDetailBlock(goal)
		for _, re := range []string{`1/2 tasks complete`, `\[ \] t2: Next task`, `contract: run the suite`, `\[x\] t1: Done task — verified green`} {
			if !regexp.MustCompile(re).MatchString(block) {
				t.Errorf("%q does not match /%s/", block, re)
			}
		}
	})

	tw(t, featFile, "derives tasks from checklist markers", func(t *testing.T) {
		tasks := deriveTasksFromObjective("- [ ] first\n- [ ] second\n- [ ] third")
		if len(tasks) != 3 || gstr(asRecord(tasks[0]), "title") != "first" {
			t.Errorf("tasks %v", tasks)
		}
	})

	tw(t, featFile, "derives tasks from numbered steps (2+)", func(t *testing.T) {
		tasks := deriveTasksFromObjective("1. extract\n2. wire\n3. test")
		if len(tasks) != 3 || gstr(asRecord(tasks[2]), "title") != "test" {
			t.Errorf("tasks %v", tasks)
		}
	})

	tw(t, featFile, "returns null without structure or with a lone numbered line", func(t *testing.T) {
		if deriveTasksFromObjective("just an objective") != nil || deriveTasksFromObjective("1. lone step") != nil {
			t.Error("expected null")
		}
	})

	tw(t, featFile, "invokes the toggle callback with goal+task on Enter over a task row", func(t *testing.T) {
		goal := featGoal("F3 test", false, 0)
		goal.set("taskList", featTaskList(t, `[{"id":"t1","title":"T1","status":"pending"}]`))
		mockCtx := createMockExtensionContext(t)
		pool := poolOf(goal)
		toggled := make(chan string, 4)
		mockCtx.startCustom(func() {
			showTaskListOverlay(mockCtx, pool, new(gstr(goal, "id")), taskListOverlayOptions{onToggleTask: func(gid, tid string) overlayToggleResult {
				toggled <- gid + ":" + tid
				return overlayToggleResult{ok: true}
			}})
		})
		component, _, _ := invokeCustomFactory(t, mockCtx.customCalls(), 0)
		component.HandleInput("\r") // raw Enter data (matchesKey parses escape sequences)
		select {
		case got := <-toggled:
			if got != gstr(goal, "id")+":t1" {
				t.Errorf("toggled %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("toggle callback not invoked")
		}
		if len(toggled) != 0 {
			t.Errorf("toggled more than once")
		}
	})

	tw(t, featFile, "toggleTaskViaService completes a pending task and reopens a complete one", func(t *testing.T) {
		co, h := featCore(t)
		goal := mustWrite(t, storage{cwd: h.cwd}, featGoal("F3 mutation test", false, 0))
		co.loadState()
		// focus the goal + set the in-memory task list
		co.setFocusedGoalId(new(gstr(goal, "id")), "selected", false)
		withTasks := goalWith(co.state(), "taskList", featTaskList(t, `[{"id":"t1","title":"T1","status":"pending"}]`))
		co.updateFocusedGoal(withTasks, true)

		status := func() string {
			return gstr(asRecord(co.state().obj("taskList").vals["tasks"].([]any)[0]), "status")
		}
		if ok, msg := co.toggleTaskViaService(gstr(goal, "id"), "t1"); !ok {
			t.Fatalf("complete: %s", msg)
		}
		if got := status(); got != "complete" {
			t.Errorf("after complete: %q", got)
		}
		if ok, msg := co.toggleTaskViaService(gstr(goal, "id"), "t1"); !ok {
			t.Fatalf("reopen: %s", msg)
		}
		if got := status(); got != "pending" {
			t.Errorf("after reopen: %q", got)
		}
	})

	tw(t, featFile, "toggleGoalAuditor flips per-goal skipAuditor, persists it, records the event, and notifies", func(t *testing.T) {
		co, h := featCore(t)
		ctx := storage{cwd: h.cwd}
		goal := mustWrite(t, ctx, featGoal("Auditor toggle test", false, 0))
		co.loadState()
		co.setFocusedGoalId(new(gstr(goal, "id")), "selected", false)
		if co.state().has("skipAuditor") && truthy(co.state().vals["skipAuditor"]) {
			t.Fatal("auditor on by default")
		}
		onDisk := func() *jsObject {
			parsed := parseGoalFile(filepath.Join(h.cwd, gstr(co.state(), "activePath")))
			if parsed == nil {
				t.Fatal("goal file must parse")
			}
			return parsed
		}

		co.toggleGoalAuditor()
		if !gflag(co.state(), "skipAuditor") {
			t.Error("toggle disables the auditor in memory")
		}
		toggled := featAuditorEvents(t, ctx)
		if len(toggled) != 1 {
			t.Fatalf("one auditor_toggled event: %d", len(toggled))
		}
		if v, _ := toggled[0].flag("enabled"); v {
			t.Error("event records the new disabled state")
		}
		if !h.notified("Auditor disabled") {
			t.Error("disabling is announced")
		}
		if !gflag(onDisk(), "skipAuditor") {
			t.Error("skipAuditor persisted to the goal file")
		}

		co.toggleGoalAuditor()
		if truthy(co.state().vals["skipAuditor"]) {
			t.Error("toggling again enables the auditor")
		}
		toggled = featAuditorEvents(t, ctx)
		if len(toggled) != 2 {
			t.Fatalf("second event recorded: %d", len(toggled))
		}
		if v, _ := toggled[1].flag("enabled"); !v {
			t.Error("second event records the enabled state")
		}
		if truthy(onDisk().vals["skipAuditor"]) {
			t.Error("enabling clears the persisted skipAuditor")
		}
	})

	tw(t, featFile, "toggleGoalAuditor is inert with no focused goal", func(t *testing.T) {
		co, h := featCore(t)
		co.loadState()
		co.toggleGoalAuditor()
		if !h.notified("No focused goal") {
			t.Error("no-goal guard announces")
		}
		if n := len(featAuditorEvents(t, storage{cwd: h.cwd})); n != 0 {
			t.Errorf("no event without a goal: %d", n)
		}
	})

	tw(t, featFile, "toggleGoalAuditor is inert for a complete goal", func(t *testing.T) {
		co, h := featCore(t)
		co.loadState()
		co.setGoal(goalWith(featGoal("Complete toggle test", false, 0), "status", "complete"), true, "")
		co.toggleGoalAuditor()
		if !h.notified("complete; the auditor no longer applies") {
			t.Error("complete-goal guard announces")
		}
		if truthy(co.state().vals["skipAuditor"]) {
			t.Error("no mutation on a complete goal")
		}
	})

	tw(t, featFile, "renders the Step N/M badge and current-step highlight", func(t *testing.T) {
		goal := featGoal("1. a\n2. b\n3. c", true, 0)
		goal.set("taskList", featTaskList(t, `[{"id":"t1","title":"a","status":"complete"},{"id":"t2","title":"b","status":"pending"},{"id":"t3","title":"c","status":"pending"}]`))
		joined := strings.Join(renderGoalWidgetLines(goal, createMockTheme(), 100, goalWidgetLinesOptions{openGoalCount: 1}), "\n")
		// The unified dashboard derives progress from the task tree (§9.1): the widget shows task progress.
		for _, want := range []string{"Tasks · ✓1 done · 2 open", "Current  t2 · b"} {
			if !strings.Contains(joined, want) {
				t.Errorf("%q not in %q", want, joined)
			}
		}
	})

	tw(t, featFile, "emits a [GOAL STALLED] note + ledger event after the timeout; off by default", func(t *testing.T) {
		co, h := featCore(t)
		if err := os.WriteFile(filepath.Join(h.cwd, ".pi", "pi-goal-x-settings.json"), []byte(`{"stallTimeoutMinutes":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		goal := mustWrite(t, storage{cwd: h.cwd}, featGoal("F5 test", false, 0))
		co.loadState()
		co.setFocusedGoalId(new(gstr(goal, "id")), "selected", false)

		// no activity for >1 minute: age the clock for the check
		realNow := clockMs
		defer func() { clockMs = realNow }()
		clockMs = func() int64 { return realNow() + 70_000 }
		note := co.checkStall()
		if !strings.Contains(note, "[GOAL STALLED") || !strings.Contains(note, "No continuation or tool activity for 1 minute") {
			t.Errorf("note %q", note)
		}
		if !h.notified("Goal stalled") {
			t.Error("stall notification fired")
		}
		if !strings.Contains(featLedger(h.cwd), "goal_stalled") {
			t.Error("goal_stalled ledger event")
		}
		// second call: one-shot (stallNotified)
		if again := co.checkStall(); again != "" {
			t.Errorf("stall note fires once: %q", again)
		}
	})

	tw(t, featFile, "returns nothing when the detector is off", func(t *testing.T) {
		co, h := featCore(t)
		goal := mustWrite(t, storage{cwd: h.cwd}, featGoal("F5 off", false, 0))
		co.loadState()
		co.setFocusedGoalId(new(gstr(goal, "id")), "selected", false)
		if got := co.checkStall(); got != "" {
			t.Errorf("checkStall %q", got)
		}
	})

	tw(t, featFile, "emits goal_budget_warning events at 50/75/90%", func(t *testing.T) {
		co, h := featCore(t)
		goal := mustWrite(t, storage{cwd: h.cwd}, featGoal("F6 test", false, 100000))
		co.loadState()
		co.setFocusedGoalId(new(gstr(goal, "id")), "selected", false)
		co.beginAccounting()
		co.accountProgress(60000) // 60% → 50% warning
		co.accountProgress(20000) // 80% → 75% warning
		co.accountProgress(20000) // 100% → 90% warning + budget_limited
		ledger := featLedger(h.cwd)
		if n := strings.Count(ledger, `"type":"goal_budget_warning"`); n != 3 {
			t.Errorf("one warning per crossed threshold (50/75/90): %d", n)
		}
		for _, want := range []string{`"pct":60`, `"pct":80`, `"pct":100`, "goal_budget_limited"} {
			if !strings.Contains(ledger, want) {
				t.Errorf("ledger lacks %s", want)
			}
		}
	})
}
