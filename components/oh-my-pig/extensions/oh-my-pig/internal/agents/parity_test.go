package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

var durationAttrRE = regexp.MustCompile(`duration="[0-9.]+(?:ms|s|m|h)"`)

// Oracle golden-text parity test: exact model-facing text of `task` spawn
// feedback (single & batch) and `wait` result (completed task envelope, no
// spurious `Agent:` line when job id == agent id, blank line after `<task-result>`).
// upstream: /tmp/omp-proof2/reference/oracle/report.json (`task-and-wait`, `task-errors-and-agent-urls`)
func TestOracleGoldenTaskAndWaitTexts(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	rt.Config.WaitSettleMs = 40
	ext := sdk.New("test-oracle-golden")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "oracle-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.untilWaitWatches()
			h.finishChildWithText(child, "ORACLE_CHILD_OK")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	// 1. Single spawn feedback matches oracle `task-and-wait` call 0 verbatim.
	spawnRes, errMsg := h.callTool("task", map[string]any{
		"context": "Oracle subagent test.",
		"tasks": []any{
			map[string]any{
				"name":          "EchoChild",
				"agent":         "task",
				"task":          "Reply with ORACLE_CHILD_OK.",
				"solutionSpace": "one fixed reply",
			},
		},
	})
	if errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}
	wantSpawn := "Spawned agent `EchoChild` (job `EchoChild`).\n\n" +
		"read proc:// lists jobs/services; read proc://<id> inspects status/output without consuming delivery; write proc://<id>/kill with empty `content` cancels/stops.\n" +
		"Job IDs are process-local; delivered results expire shortly (~30s), unconsumed results within ~5min. Agent output/transcripts remain readable at agent://<id> / history://<id>. write agent://<id> messages a live agent."
	gotSpawn, _ := spawnRes["content"].(string)
	if gotSpawn != wantSpawn {
		t.Fatalf("spawn feedback mismatch:\nGOT:\n%q\nWANT:\n%q", gotSpawn, wantSpawn)
	}

	// 2. Wait result matches oracle `task-and-wait` call 1 verbatim (after normalizing duration).
	waitRes, errMsg := h.callTool("wait", map[string]any{})
	if errMsg != "" {
		t.Fatalf("wait failed: %s", errMsg)
	}
	gotWait := durationAttrRE.ReplaceAllString(waitRes["content"].(string), `duration="<DURATION>"`)
	wantWait := "## Completed (1)\n\n" +
		"### EchoChild [task] — completed\n" +
		"Label: EchoChild\n" +
		"Delivery: delivered by this wait — it finished while you were waiting.\n" +
		"```\n" +
		"<task-result id=\"EchoChild\" agent=\"task\" status=\"completed\" duration=\"<DURATION>\">\n\n" +
		"<output>\n" +
		"ORACLE_CHILD_OK\n" +
		"</output>\n" +
		"</task-result>\n\n" +
		"EchoChild is now idle — message it via `write agent://EchoChild` to follow up; transcript at history://EchoChild\n" +
		"```"
	if gotWait != wantWait {
		t.Fatalf("wait output mismatch:\nGOT:\n%q\nWANT:\n%q", gotWait, wantWait)
	}

	// 3. Multi-child batch spawn feedback on a fresh session (lifecycle shown) has blank line after listing.
	rt2 := NewRuntime(DefaultConfig())
	ext2 := sdk.New("test-oracle-batch")
	RegisterWithRuntime(ext2, rt2)
	h2 := startFakeHost(t, ext2, fakeHostOptions{
		SessionID: "oracle-batch-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "OK")
			return nil
		},
	})
	batchRes, errMsg := h2.callTool("task", map[string]any{
		"context": "c",
		"tasks": []any{
			map[string]any{"name": "One", "agent": "task", "task": "Reply.", "solutionSpace": "s"},
			map[string]any{"name": "Two", "agent": "task", "task": "Reply.", "solutionSpace": "s"},
		},
	})
	if errMsg != "" {
		t.Fatalf("batch task failed: %s", errMsg)
	}
	gotBatch, _ := batchRes["content"].(string)
	wantPrefix := "Spawned 2 background agents using task.\n- `One` (job `One`)\n- `Two` (job `Two`)\n\nread proc:// lists jobs/services;"
	if !strings.HasPrefix(gotBatch, wantPrefix) {
		t.Fatalf("batch spawn feedback missing blank line after listing:\nGOT:\n%q\nWANT PREFIX:\n%q", gotBatch, wantPrefix)
	}
}

// Verify `write agent://Main` delivers to the parent session via SendMessage
// ("irc:incoming") instead of parking forever in PendingIRC, and returns failed
// when no live parent context exists.
// upstream: test/task/peer-message-after-report.test.ts, session/irc-bridge.ts:188-296
func TestAgentMessageToMainDeliveredViaSendMessage(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	ext := sdk.New("test-main-irc")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "main-irc-sess",
		IsIdle:    true,
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "done")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	// Simulate a child messaging Main through `agent://Main`.
	rt.Registry.RegisterChildSession("child-sess-1", ChildSessionMeta{
		ParentSessionID: "main-irc-sess",
		Depth:           1,
		AgentID:         "Worker1",
		AgentName:       "task",
	})
	text, det, isErr := rt.Registry.SendMessageInSession("main-irc-sess", "Worker1", "Main", "Blocked on API spec", false, "", false)
	if isErr {
		t.Fatalf("expected delivery to Main to succeed, got error: %s (%+v)", text, det)
	}
	if text != "Delivered to Main." {
		t.Fatalf("got %q, want %q", text, "Delivered to Main.")
	}
	if len(det.Receipts) != 1 || det.Receipts[0].Outcome != "woken" {
		t.Fatalf("expected woken receipt for idle Main, got %+v", det.Receipts)
	}

	h.mu.Lock()
	sent := append([]map[string]any(nil), h.sentMsgs...)
	h.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sendMessage call to Main host, got %d", len(sent))
	}
	msgMap, _ := sent[0]["message"].(map[string]any)
	if msgMap["customType"] != "irc:incoming" {
		t.Fatalf("expected customType irc:incoming, got %v", msgMap["customType"])
	}
	content, _ := msgMap["content"].(string)
	if !strings.Contains(content, "Incoming IRC message from agent `Worker1`") || !strings.Contains(content, "Blocked on API spec") {
		t.Fatalf("unexpected IRC content delivered to Main:\n%s", content)
	}
	// Main ref with no OnDeliver must fail instead of returning injected.
	rt.Registry.RegisterMain("orphan-sess", &AgentRef{ID: MainAgentID, Kind: "main", Status: "running"})
	_, failDet, failErr := rt.Registry.SendMessageInSession("orphan-sess", "Worker1", "Main", "hello", false, "", false)
	if !failErr || len(failDet.Receipts) != 1 || failDet.Receipts[0].Outcome != "failed" {
		t.Fatalf("expected failed receipt when Main has no delivery handler, got err=%v det=%+v", failErr, failDet)
	}
}

// Verify waking an idle keep-alive subagent via `write agent://<id>` runs a
// monitored wake turn: registers a wake job in `internal/jobs` (whose AgentURLID
// differs from JobID so `wait` prints `Agent: agent://X, history://X`), updates
// `<id>.md`, and delivers the follow-up answer back to Main.
// upstream: test/task/executor-irc-wake-yield-reset.test.ts, task/executor.ts:3457-3900
func TestWokenIdleSubagentRunsMonitoredTurnAndRelays(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	rt.Config.WaitSettleMs = 20
	ext := sdk.New("test-wake-relay")
	RegisterWithRuntime(ext, rt)

	var promptCount atomic.Int32
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "wake-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, promptText string) error {
			n := promptCount.Add(1)
			if n == 1 {
				h.finishChildWithText(child, "first turn answer")
				return nil
			}
			// Verify incoming wake prompt tells the child relayOnStop is active.
			if !strings.Contains(promptText, "delivered to `Main` when you stop") {
				t.Errorf("expected relayOnStop clause on wake turn, got:\n%s", promptText)
			}
			h.untilWaitWatches()
			h.finishChildWithText(child, "second turn follow-up answer")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	if _, errMsg := h.callTool("task", map[string]any{
		"context": "Wake test",
		"tasks":   []any{map[string]any{"name": "FollowWorker", "task": "initial", "solutionSpace": "s"}},
	}); errMsg != "" {
		t.Fatalf("spawn failed: %s", errMsg)
	}
	j1, ok := jobs.Get("wake-sess", "FollowWorker")
	if !ok {
		t.Fatalf("expected FollowWorker job")
	}
	select {
	case <-j1.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("FollowWorker initial turn did not finish")
	}
	// Wait for FollowWorker to transition to idle before messaging it.
	if ref, ok := rt.Registry.GetInSession("wake-sess", "FollowWorker"); ok {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			ref.mu.Lock()
			st := ref.Status
			ref.mu.Unlock()
			if st == "idle" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// The first turn's result is handed over before the wake: left undelivered, the `wait` below would report it
	// at once and never watch the wake job. The first turn has finished, so this call returns without watching.
	if _, errMsg := h.callTool("wait", map[string]any{}); errMsg != "" {
		t.Fatalf("drain of the first turn failed: %s", errMsg)
	}

	// Message idle FollowWorker from Main via `agent://FollowWorker`.
	wr, err := urls.Write(urls.Context{Cwd: h.opts.Cwd}, "agent://FollowWorker", "now answer follow-up question")
	if err != nil || wr.IsError {
		t.Fatalf("wake write failed: wr=%+v err=%v", wr, err)
	}

	// Wait collects the wake job and prints `Agent: agent://FollowWorker, history://FollowWorker`
	// because the wake job ID (`FollowWorker-wake-1`) differs from `FollowWorker`.
	waitRes, errMsg := h.callTool("wait", map[string]any{})
	if errMsg != "" {
		t.Fatalf("wake wait failed: %s", errMsg)
	}
	txt, _ := waitRes["content"].(string)
	if !strings.Contains(txt, "FollowWorker-wake-1") || !strings.Contains(txt, "second turn follow-up answer") {
		t.Fatalf("expected wake turn answer in wait output, got:\n%s", txt)
	}
	if !strings.Contains(txt, "Agent: agent://FollowWorker, history://FollowWorker") {
		t.Fatalf("expected Agent: line when wake job ID differs from agent ID, got:\n%s", txt)
	}

	// `<id>.md` must be updated so `agent://FollowWorker` now returns the wake turn's output.
	res, err := urls.Resolve(urls.Context{Cwd: h.opts.Cwd}, "agent://FollowWorker")
	if err != nil {
		t.Fatalf("resolve agent://FollowWorker failed: %v", err)
	}
	if strings.TrimSpace(res.Content) != "second turn follow-up answer" {
		t.Fatalf("expected agent://FollowWorker to reflect wake turn output, got %q", res.Content)
	}
}

// Verify that timed-out and yield-error subagents become `failed` jobs (NOT
// `cancelled`) so `internal/jobs` delivers their `<task-result status="cancelled">`
// envelope and `<abort-reason>` to the parent.
// upstream: test/task/executor-wall-clock.test.ts, task/index.ts:1620-1662
func TestAbortedSubagentTimeoutAndYieldErrorAreDeliveredAsFailedJobs(t *testing.T) {
	resetAllRegistries(t)
	cfg := DefaultConfig()
	cfg.MaxRuntimeMs = 60
	rt := NewRuntime(cfg)
	ext := sdk.New("test-wall-clock")
	RegisterWithRuntime(ext, rt)

	var deliveredMu sync.Mutex
	var deliveries []string
	// The sink is the process's: a job an earlier test left running delivers here too when it ends.
	jobs.SetDeliverySink(func(sessionID, customType, content string, _ bool, _ sdk.SendMessageOptions) error {
		if sessionID != "timeout-sess" {
			return nil
		}
		deliveredMu.Lock()
		deliveries = append(deliveries, content)
		deliveredMu.Unlock()
		return nil
	})
	t.Cleanup(func() { jobs.SetDeliverySink(nil) })

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "timeout-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			<-child.abortCh
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	if _, errMsg := h.callTool("task", map[string]any{
		"context": "Timeout delivery test",
		"tasks":   []any{map[string]any{"name": "SlowChild", "task": "hang", "solutionSpace": "s"}},
	}); errMsg != "" {
		t.Fatalf("spawn failed: %s", errMsg)
	}

	j, ok := jobs.Get("timeout-sess", "SlowChild")
	if !ok {
		t.Fatalf("expected SlowChild job")
	}
	select {
	case <-j.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("SlowChild did not time out")
	}
	if j.Status() != "failed" {
		t.Fatalf("expected timed-out child job status 'failed' (so it delivers), got %q", j.Status())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		deliveredMu.Lock()
		n := len(deliveries)
		deliveredMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	deliveredMu.Lock()
	got := append([]string(nil), deliveries...)
	deliveredMu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 auto-delivery for timed-out child, got %d", len(got))
	}
	if !strings.Contains(got[0], `status="cancelled"`) || !strings.Contains(got[0], "Subagent runtime limit exceeded (task.maxRuntimeMs=60)") {
		t.Fatalf("expected timeout abort reason in delivered envelope, got:\n%s", got[0])
	}
}

// Verify soft request budget: per-agent ceilings (`scout: 100`, `sonic: 100`,
// `default: 200`), exact `BuildBudgetNotice` text, 1.5x force-stop + forced
// wrap-up prompt (`subagent-yield-reminder.md`), and +5 grace hard-abort.
// upstream: test/task/executor-soft-budget.test.ts
func TestSoftRequestBudgetNoticeForceStopAndHardAbort(t *testing.T) {
	resetAllRegistries(t)

	// 1. Unit checks for ResolveSoftRequestBudget and BuildBudgetNotice.
	if b := ResolveSoftRequestBudget("scout", 200, nil); b != 100 {
		t.Fatalf("scout budget = %d, want 100", b)
	}
	if b := ResolveSoftRequestBudget("sonic", 200, nil); b != 100 {
		t.Fatalf("sonic budget = %d, want 100", b)
	}
	if b := ResolveSoftRequestBudget("task", 200, nil); b != 200 {
		t.Fatalf("task budget = %d, want 200", b)
	}
	if b := ResolveSoftRequestBudget("scout", 50, nil); b != 50 {
		t.Fatalf("configured tighter budget = %d, want 50", b)
	}
	if b := ResolveSoftRequestBudget("scout", 200, map[string]int{"scout": 150}); b != 150 {
		t.Fatalf("per-agent override = %d, want 150", b)
	}
	if b := ResolveSoftRequestBudget("scout", 0, nil); b != 0 {
		t.Fatalf("0 disables guard, got %d", b)
	}
	wantNotice := "[budget notice] You have used 2 requests in this run (soft budget: 2). Wrap up now: finish the current step and deliver your final report. At 3 requests the run is force-stopped and you will be asked to report whatever you have."
	if got := BuildBudgetNotice(2, 2); got != wantNotice {
		t.Fatalf("BuildBudgetNotice mismatch:\nGOT:  %q\nWANT: %q", got, wantNotice)
	}

	// 2. End-to-end: budget=2 -> steer at 2, 1.5x stop at 3, forced wrap-up prompt produces clean completion.
	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	cfg.SoftRequestBudget = 2
	rt := NewRuntime(cfg)
	ext := sdk.New("test-soft-budget")
	RegisterWithRuntime(ext, rt)

	var promptNum atomic.Int32
	var sawWrapReminder atomic.Bool
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "budget-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, promptText string) error {
			p := promptNum.Add(1)
			if p == 1 {
				// Emit 3 assistant messages (with tool_use so none is final) to cross soft (2) and 1.5x stop (3).
				for i := 0; i < 3; i++ {
					msg := map[string]any{
						"role":       "assistant",
						"stopReason": "toolUse",
						"content":    []any{map[string]any{"type": "toolCall", "name": "read", "id": "tc"}},
					}
					h.emitChildEvent(child, map[string]any{"type": "message_end", "message": msg})
					time.Sleep(15 * time.Millisecond)
				}
				// Wait for the 1.5x force-stop abort signal.
				select {
				case <-child.abortCh:
				case <-time.After(2 * time.Second):
					t.Errorf("expected 1.5x force-stop abort at 3 requests")
				}
				return nil
			}
			// Second prompt MUST be omp's budgetStop reminder.
			if strings.Contains(promptText, "Request budget crossed; in-flight turn stopped → forced wrap-up.") {
				sawWrapReminder.Store(true)
			}
			h.finishChildWithText(child, "partial findings after budget stop")
			return nil
		},
	})

	res, errMsg := h.callTool("task", map[string]any{
		"context": "Budget test",
		"tasks":   []any{map[string]any{"name": "Runner", "task": "loop", "solutionSpace": "s"}},
	})
	if errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}
	if !sawWrapReminder.Load() {
		t.Fatalf("expected forced wrap-up prompt after 1.5x stop")
	}
	children := h.getChildren()
	children[0].mu.Lock()
	steers := append([]string(nil), children[0].steers...)
	children[0].mu.Unlock()
	if len(steers) == 0 || steers[0] != wantNotice {
		t.Fatalf("expected omp budget notice steer %q, got %v", wantNotice, steers)
	}
	if txt, _ := res["content"].(string); !strings.Contains(txt, "partial findings after budget stop") || !strings.Contains(txt, `status="completed"`) {
		t.Fatalf("expected completed partial findings after forced wrap-up, got:\n%s", txt)
	}

	// 3. Hard abort after +5 grace requests when child keeps burning requests during wrap-up.
	cfg2 := DefaultConfig()
	cfg2.AsyncEnabled = false
	cfg2.SoftRequestBudget = 2
	rt2 := NewRuntime(cfg2)
	ext2 := sdk.New("test-soft-budget-hard")
	RegisterWithRuntime(ext2, rt2)
	var p2 atomic.Int32
	h2 := startFakeHost(t, ext2, fakeHostOptions{
		SessionID: "budget-hard-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			n := p2.Add(1)
			count := 3
			if n == 2 {
				count = BudgetStopGraceRequests // +5 more hits 8 -> hard abort
			}
			for i := 0; i < count; i++ {
				msg := map[string]any{
					"role":       "assistant",
					"stopReason": "toolUse",
					"content":    []any{map[string]any{"type": "toolCall", "name": "read", "id": "tc"}},
				}
				h.emitChildEvent(child, map[string]any{"type": "message_end", "message": msg})
				time.Sleep(10 * time.Millisecond)
			}
			return nil
		},
	})
	res2, _ := h2.callTool("task", map[string]any{
		"context": "Hard abort test",
		"tasks":   []any{map[string]any{"name": "Runaway", "task": "loop forever", "solutionSpace": "s"}},
	})
	if txt, _ := res2["content"].(string); !strings.Contains(txt, "Soft request budget exceeded") || !strings.Contains(txt, "agent did not yield when force-stopped") {
		t.Fatalf("expected hard-abort budget reason, got:\n%s", txt)
	}
}

// Verify live settings reading from `internal/settings.StoreFor(ctx)` by
// observable behaviour for EVERY key claimed in UPSTREAM.md's settings table:
// async.enabled, task.batch, task.structured, task.steps, task.enableEffort,
// task.maxConcurrency, task.maxRecursionDepth, task.maxRuntimeMs,
// task.softRequestBudget, task.softRequestBudgetNotice, task.agentSoftRequestBudget,
// task.agentIdleTtlMs, task.disabledAgents, task.agentModelOverrides,
// task.perCallModel, task.isolation.enabled, and task.crossInstance; plus
// mid-session advertised description/schema re-render on `before_agent_start`.
func TestSettingsStoreHonouredPerCall(t *testing.T) {
	resetAllRegistries(t)
	ResetForTests()
	t.Cleanup(ResetForTests)
	settings.ResetForTests()
	t.Cleanup(settings.ResetForTests)

	store, err := settings.NewIsolated(map[string]any{
		"task.maxConcurrency": 1,
		"task.disabledAgents": []any{"sonic"},
		"task.agentModelOverrides": map[string]any{
			"scout": []any{"openai/gpt-5-mini"},
		},
		"task.crossInstance": true,
	})
	if err != nil {
		t.Fatalf("settings.NewIsolated: %v", err)
	}
	settings.SetStoreForTests(func(_ sdk.Context) *settings.Store { return store })

	ext := sdk.New("test-settings-live")
	Register(ext) // uses defaultRuntime with UseSettings: true

	var childBehaviorMu sync.Mutex
	var childBehavior func(h *fakeHost, child *fakeChildState, prompt string) error
	setChildBehavior := func(fn func(h *fakeHost, child *fakeChildState, prompt string) error) {
		childBehaviorMu.Lock()
		childBehavior = fn
		childBehaviorMu.Unlock()
	}

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "settings-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, prompt string) error {
			childBehaviorMu.Lock()
			fn := childBehavior
			childBehaviorMu.Unlock()
			if fn != nil {
				return fn(h, child, prompt)
			}
			h.finishChildWithText(child, "override ok")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	// 1. task.disabledAgents -> rejected with full omp disabled-agent message,
	// and omitted from advertised description.
	disRes, _ := h.callTool("task", map[string]any{
		"context": "c",
		"tasks":   []any{map[string]any{"name": "S", "agent": "sonic", "task": "t", "solutionSpace": "s"}},
	})
	disTxt, _ := disRes["content"].(string)
	if !strings.Contains(disTxt, `Agent "sonic" is disabled in settings. Enable it via /agents, or use a different agent type. Available:`) {
		t.Fatalf("task.disabledAgents: expected full omp disabled-agent message, got:\n%s", disTxt)
	}
	h.mu.Lock()
	initDesc := h.toolDescs["task"]
	h.mu.Unlock()
	if strings.Contains(initDesc, "- `sonic`") {
		t.Fatalf("task.disabledAgents: sonic must be omitted from initial description")
	}

	// 2. task.agentModelOverrides + task.crossInstance: numeric name `123` becomes `Agent123`
	// and scout gets `openai/gpt-5-mini`.
	spawnRes, errMsg := h.callTool("task", map[string]any{
		"context": "c",
		"tasks":   []any{map[string]any{"name": "123", "agent": "scout", "task": "search", "solutionSpace": "s"}},
	})
	if errMsg != "" {
		t.Fatalf("scout spawn failed: %s", errMsg)
	}
	spawnTxt, _ := spawnRes["content"].(string)
	if !strings.Contains(spawnTxt, "`Agent123`") {
		t.Fatalf("task.crossInstance=true: expected numeric name 123 to become Agent123, got:\n%s", spawnTxt)
	}
	_, _ = h.callTool("wait", map[string]any{})
	children := h.getChildren()
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	children[0].mu.Lock()
	modelMap, _ := children[0].createArgs["model"].(map[string]any)
	children[0].mu.Unlock()
	if modelMap["provider"] != "openai" || modelMap["id"] != "gpt-5-mini" {
		t.Fatalf("task.agentModelOverrides: expected openai/gpt-5-mini, got %+v", modelMap)
	}

	// Flip task.crossInstance to false mid-session: numeric name `456` must stay `456`.
	if err := store.Set("task.crossInstance", false, false); err != nil {
		t.Fatalf("set crossInstance=false: %v", err)
	}
	spawnRes2, errMsg := h.callTool("task", map[string]any{
		"context": "c",
		"tasks":   []any{map[string]any{"name": "456", "agent": "scout", "task": "search", "solutionSpace": "s"}},
	})
	if errMsg != "" {
		t.Fatalf("spawn 456 failed: %s", errMsg)
	}
	if txt, _ := spawnRes2["content"].(string); !strings.Contains(txt, "`456`") || strings.Contains(txt, "`Agent456`") {
		t.Fatalf("task.crossInstance=false: expected 456 verbatim, got:\n%s", txt)
	}
	_, _ = h.callTool("wait", map[string]any{})

	// 3. task.maxConcurrency: 1 serializes 2 parallel spawns; resizing mid-session to 2 lets both overlap.
	var inflight, maxInflight atomic.Int32
	gate1 := make(chan struct{})
	started1 := make(chan struct{}, 2)
	setChildBehavior(func(h *fakeHost, child *fakeChildState, _ string) error {
		cur := inflight.Add(1)
		for {
			old := maxInflight.Load()
			if cur <= old || maxInflight.CompareAndSwap(old, cur) {
				break
			}
		}
		started1 <- struct{}{}
		<-gate1
		inflight.Add(-1)
		h.finishChildWithText(child, "conc done")
		return nil
	})
	if _, errMsg := h.callTool("task", map[string]any{
		"context": "conc",
		"tasks": []any{
			map[string]any{"name": "C1", "task": "one", "solutionSpace": "s"},
			map[string]any{"name": "C2", "task": "two", "solutionSpace": "s"},
		},
	}); errMsg != "" {
		t.Fatalf("conc spawn failed: %s", errMsg)
	}
	<-started1
	select {
	case <-started1:
		t.Fatalf("task.maxConcurrency=1: second child started before first finished")
	case <-time.After(40 * time.Millisecond):
	}
	// Resize mid-session to 2 and verify second child is admitted without releasing gate1.
	if err := store.Set("task.maxConcurrency", 2, false); err != nil {
		t.Fatalf("set maxConcurrency=2: %v", err)
	}
	_ = defaultRuntime.EffectiveConfig(defaultRuntime.sessionContextFor("settings-sess", sdk.Context{}))
	select {
	case <-started1:
	case <-time.After(1 * time.Second):
		t.Fatalf("task.maxConcurrency=2: expected queued child to start after mid-session resize")
	}
	close(gate1)
	_, _ = h.callTool("wait", map[string]any{})
	if maxInflight.Load() != 2 {
		t.Fatalf("task.maxConcurrency=2: expected maxInflight=2, got %d", maxInflight.Load())
	}
	setChildBehavior(nil)

	// 4. async.enabled=false runs inline synchronously and returns <task-result> directly.
	if err := store.Set("async.enabled", false, false); err != nil {
		t.Fatalf("set async.enabled=false: %v", err)
	}
	syncRes, errMsg := h.callTool("task", map[string]any{
		"context": "sync",
		"tasks":   []any{map[string]any{"name": "SyncOne", "task": "inline", "solutionSpace": "s"}},
	})
	if errMsg != "" {
		t.Fatalf("async.enabled=false failed: %s", errMsg)
	}
	if txt, _ := syncRes["content"].(string); !strings.Contains(txt, `<task-result id="SyncOne"`) || strings.Contains(txt, "Spawned agent") {
		t.Fatalf("async.enabled=false: expected inline <task-result>, got:\n%s", txt)
	}

	// 5. task.batch=false swaps wire shape to flat AND before_agent_start re-renders advertised schema & description.
	if err := store.Set("task.batch", false, false); err != nil {
		t.Fatalf("set task.batch=false: %v", err)
	}
	if err := store.Set("task.disabledAgents", []any{}, false); err != nil {
		t.Fatalf("clear disabledAgents: %v", err)
	}
	h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	h.mu.Lock()
	flatDesc := h.toolDescs["task"]
	flatParams := h.toolParams["task"]
	h.mu.Unlock()
	if !strings.Contains(flatDesc, "- `sonic`") {
		t.Fatalf("before_agent_start refresh: expected sonic restored in description after clearing task.disabledAgents")
	}
	if props, _ := flatParams["properties"].(map[string]any); props["tasks"] != nil || props["task"] == nil {
		t.Fatalf("before_agent_start refresh: expected flat schema (task, no tasks) after task.batch=false, got props=%v", props)
	}
	// Batch payload must be rejected when task.batch=false, while flat succeeds.
	badBatch, _ := h.callTool("task", map[string]any{
		"context": "c",
		"tasks":   []any{map[string]any{"name": "B", "task": "t", "solutionSpace": "s"}},
	})
	if isErr, _ := badBatch["is_error"].(bool); !isErr {
		t.Fatalf("task.batch=false: expected batch payload to be rejected, got %+v", badBatch)
	}
	flatRes, errMsg := h.callTool("task", map[string]any{
		"name": "FlatOne", "task": "flat work", "solutionSpace": "s",
	})
	if errMsg != "" || flatRes["is_error"] == true {
		t.Fatalf("task.batch=false: flat payload failed: %v / %s", flatRes, errMsg)
	}

	// 6. task.steps=true switches task field to steps[] and rejects plain task.
	if err := store.Set("task.steps", true, false); err != nil {
		t.Fatalf("set task.steps=true: %v", err)
	}
	var capturedStepPrompts []string
	setChildBehavior(func(h *fakeHost, child *fakeChildState, prompt string) error {
		capturedStepPrompts = append(capturedStepPrompts, prompt)
		h.finishChildWithText(child, "steps done")
		return nil
	})
	stepRes, errMsg := h.callTool("task", map[string]any{
		"name": "Stepper", "steps": []any{"step one", "step two"}, "solutionSpace": "s",
	})
	if errMsg != "" || stepRes["is_error"] == true {
		t.Fatalf("task.steps=true failed: %v / %s", stepRes, errMsg)
	}
	if len(capturedStepPrompts) != 2 || !strings.Contains(capturedStepPrompts[0], "step one") || !strings.Contains(capturedStepPrompts[1], "step two") {
		t.Fatalf("task.steps=true: expected 2 sequential step prompts (step one, step two), got %v", capturedStepPrompts)
	}
	if err := store.Set("task.steps", false, false); err != nil {
		t.Fatalf("set task.steps=false: %v", err)
	}
	setChildBehavior(nil)

	// 7. task.structured=true injects `yield` custom tool when outputSchema is provided.
	if err := store.Set("task.structured", true, false); err != nil {
		t.Fatalf("set task.structured=true: %v", err)
	}
	var sawYieldTool bool
	setChildBehavior(func(h *fakeHost, child *fakeChildState, _ string) error {
		child.mu.Lock()
		rawTools, _ := child.createArgs["customTools"].([]any)
		child.mu.Unlock()
		for _, rt := range rawTools {
			if m, ok := rt.(map[string]any); ok && m["name"] == "yield" {
				sawYieldTool = true
			}
		}
		_, _ = h.callChildCustomTool(child.id, "yield", map[string]any{"data": map[string]any{"ok": true}})
		h.finishChildWithText(child, "done")
		return nil
	})
	structRes, errMsg := h.callTool("task", map[string]any{
		"name": "StructOne", "task": "return ok", "solutionSpace": "s",
		"outputSchema": map[string]any{"properties": map[string]any{"ok": map[string]any{"type": "boolean"}}},
	})
	if errMsg != "" || structRes["is_error"] == true || !sawYieldTool {
		t.Fatalf("task.structured=true: expected yield custom tool injected, sawYield=%v res=%v err=%s", sawYieldTool, structRes, errMsg)
	}
	if err := store.Set("task.structured", false, false); err != nil {
		t.Fatalf("set task.structured=false: %v", err)
	}
	setChildBehavior(nil)

	// 8. task.enableEffort=true passes thinkingLevel from effort ("hi" -> "high").
	if err := store.Set("task.enableEffort", true, false); err != nil {
		t.Fatalf("set task.enableEffort=true: %v", err)
	}
	var capturedThinking string
	setChildBehavior(func(h *fakeHost, child *fakeChildState, _ string) error {
		child.mu.Lock()
		capturedThinking, _ = child.createArgs["thinkingLevel"].(string)
		child.mu.Unlock()
		h.finishChildWithText(child, "effort done")
		return nil
	})
	effRes, errMsg := h.callTool("task", map[string]any{
		"name": "EffortOne", "task": "think hard", "solutionSpace": "s", "effort": "hi",
	})
	if errMsg != "" || effRes["is_error"] == true || capturedThinking != "high" {
		t.Fatalf("task.enableEffort=true: expected thinkingLevel=high, got %q res=%v err=%s", capturedThinking, effRes, errMsg)
	}
	if err := store.Set("task.enableEffort", false, false); err != nil {
		t.Fatalf("set task.enableEffort=false: %v", err)
	}
	setChildBehavior(nil)

	// 9. task.perCallModel="on" allows per-call model override; "off" rejects it.
	offPCM, _ := h.callTool("task", map[string]any{
		"name": "PCMOff", "task": "t", "solutionSpace": "s", "model": "openai/gpt-5-mini",
	})
	if isErr, _ := offPCM["is_error"].(bool); !isErr {
		t.Fatalf("task.perCallModel=off: expected model param to be rejected, got %+v", offPCM)
	}
	if err := store.Set("task.perCallModel", "on", false); err != nil {
		t.Fatalf("set task.perCallModel=on: %v", err)
	}
	var pcmModel map[string]any
	setChildBehavior(func(h *fakeHost, child *fakeChildState, _ string) error {
		child.mu.Lock()
		pcmModel, _ = child.createArgs["model"].(map[string]any)
		child.mu.Unlock()
		h.finishChildWithText(child, "pcm done")
		return nil
	})
	onPCM, errMsg := h.callTool("task", map[string]any{
		"name": "PCMOn", "task": "t", "solutionSpace": "s", "model": "openai/gpt-5-mini",
	})
	if errMsg != "" || onPCM["is_error"] == true || pcmModel["id"] != "gpt-5-mini" {
		t.Fatalf("task.perCallModel=on: expected openai/gpt-5-mini, got %+v res=%v err=%s", pcmModel, onPCM, errMsg)
	}
	if err := store.Set("task.perCallModel", "off", false); err != nil {
		t.Fatalf("reset task.perCallModel: %v", err)
	}
	setChildBehavior(nil)

	// 10. task.isolation.enabled=false rejects isolated=true in preflight.
	isoRes, _ := h.callTool("task", map[string]any{
		"name": "IsoOff", "task": "t", "solutionSpace": "s", "isolated": true,
	})
	if txt, _ := isoRes["content"].(string); !strings.Contains(txt, "task.isolation.enabled") {
		t.Fatalf("task.isolation.enabled=false: expected isolation refusal, got:\n%s", txt)
	}

	// 11. task.maxRuntimeMs aborts a slow child with runtime limit error.
	if err := store.Set("task.maxRuntimeMs", 40, false); err != nil {
		t.Fatalf("set task.maxRuntimeMs: %v", err)
	}
	setChildBehavior(func(_ *fakeHost, child *fakeChildState, _ string) error {
		select {
		case <-child.abortCh:
		case <-time.After(2 * time.Second):
		}
		return nil
	})
	rtRes, _ := h.callTool("task", map[string]any{
		"name": "SlowOne", "task": "hang", "solutionSpace": "s",
	})
	if txt, _ := rtRes["content"].(string); !strings.Contains(txt, "Subagent runtime limit exceeded") {
		t.Fatalf("task.maxRuntimeMs=40: expected runtime limit error, got:\n%s", txt)
	}
	if err := store.Set("task.maxRuntimeMs", 0, false); err != nil {
		t.Fatalf("reset task.maxRuntimeMs: %v", err)
	}
	setChildBehavior(nil)

	// 12. task.softRequestBudget, task.softRequestBudgetNotice, task.agentSoftRequestBudget.
	if err := store.Set("task.softRequestBudget", 2, false); err != nil {
		t.Fatalf("set softRequestBudget=2: %v", err)
	}
	if err := store.Set("task.softRequestBudgetNotice", false, false); err != nil {
		t.Fatalf("set softRequestBudgetNotice=false: %v", err)
	}
	var steersNoNotice []string
	setChildBehavior(func(h *fakeHost, child *fakeChildState, _ string) error {
		for range 2 {
			m := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c", "name": "read"}}, "stopReason": "toolUse"}
			h.emitChildEvent(child, map[string]any{"type": "message_end", "message": m})
		}
		time.Sleep(25 * time.Millisecond)
		child.mu.Lock()
		steersNoNotice = append([]string(nil), child.steers...)
		child.mu.Unlock()
		h.finishChildWithText(child, "budget no notice ok")
		return nil
	})
	_, _ = h.callTool("task", map[string]any{"name": "NoNotice", "task": "t", "solutionSpace": "s"})
	if len(steersNoNotice) != 0 {
		t.Fatalf("task.softRequestBudgetNotice=false: expected 0 steers, got %v", steersNoNotice)
	}
	if err := store.Set("task.softRequestBudgetNotice", true, false); err != nil {
		t.Fatalf("set softRequestBudgetNotice=true: %v", err)
	}
	if err := store.Set("task.agentSoftRequestBudget", map[string]any{"task": 1}, false); err != nil {
		t.Fatalf("set agentSoftRequestBudget: %v", err)
	}
	var steersWithNotice []string
	setChildBehavior(func(h *fakeHost, child *fakeChildState, _ string) error {
		m := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c", "name": "read"}}, "stopReason": "toolUse"}
		h.emitChildEvent(child, map[string]any{"type": "message_end", "message": m})
		time.Sleep(25 * time.Millisecond)
		child.mu.Lock()
		steersWithNotice = append([]string(nil), child.steers...)
		child.mu.Unlock()
		h.finishChildWithText(child, "budget notice ok")
		return nil
	})
	_, _ = h.callTool("task", map[string]any{"name": "WithNotice", "task": "t", "solutionSpace": "s"})
	if len(steersWithNotice) != 1 || !strings.Contains(steersWithNotice[0], "1 requests in this run (soft budget: 1)") {
		t.Fatalf("task.agentSoftRequestBudget={task:1} + notice=true: expected soft budget=1 steer, got %v", steersWithNotice)
	}
	_ = store.Set("task.softRequestBudget", 200, false)
	_ = store.Set("task.agentSoftRequestBudget", map[string]any{}, false)
	setChildBehavior(nil)

	// 13. task.agentIdleTtlMs=35 parks an async keep-alive child; 0 disables parking.
	if err := store.Set("async.enabled", true, false); err != nil {
		t.Fatalf("set async.enabled=true: %v", err)
	}
	if err := store.Set("task.agentIdleTtlMs", 35, false); err != nil {
		t.Fatalf("set agentIdleTtlMs=35: %v", err)
	}
	if _, errMsg := h.callTool("task", map[string]any{"name": "IdlePark", "task": "t", "solutionSpace": "s"}); errMsg != "" {
		t.Fatalf("IdlePark spawn failed: %s", errMsg)
	}
	_, _ = h.callTool("wait", map[string]any{})
	parkRef, ok := defaultRuntime.Registry.GetInSession("settings-sess", "IdlePark")
	if !ok {
		t.Fatalf("expected IdlePark ref")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		parkRef.mu.Lock()
		st := parkRef.Status
		parkRef.mu.Unlock()
		if st == "parked" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	parkRef.mu.Lock()
	parkStatus := parkRef.Status
	parkRef.mu.Unlock()
	if parkStatus != "parked" {
		t.Fatalf("task.agentIdleTtlMs=35: expected parked, got %q", parkStatus)
	}
	if err := store.Set("task.agentIdleTtlMs", 0, false); err != nil {
		t.Fatalf("set agentIdleTtlMs=0: %v", err)
	}
	if _, errMsg := h.callTool("task", map[string]any{"name": "StayIdle", "task": "t", "solutionSpace": "s"}); errMsg != "" {
		t.Fatalf("StayIdle spawn failed: %s", errMsg)
	}
	_, _ = h.callTool("wait", map[string]any{})
	time.Sleep(80 * time.Millisecond)
	stayRef, _ := defaultRuntime.Registry.GetInSession("settings-sess", "StayIdle")
	stayRef.mu.Lock()
	stayStatus := stayRef.Status
	stayRef.mu.Unlock()
	if stayStatus != "idle" {
		t.Fatalf("task.agentIdleTtlMs=0: expected child to stay idle, got %q", stayStatus)
	}

	// 14. task.maxRecursionDepth=0 blocks all spawning at depth 0.
	if err := store.Set("task.maxRecursionDepth", 0, false); err != nil {
		t.Fatalf("set maxRecursionDepth=0: %v", err)
	}
	depthRes, _ := h.callTool("task", map[string]any{"name": "BlockedDepth", "task": "t", "solutionSpace": "s"})
	if txt, _ := depthRes["content"].(string); !strings.Contains(txt, "maximum depth is 0") {
		t.Fatalf("task.maxRecursionDepth=0: expected depth error, got:\n%s", txt)
	}
}

// Verify role-alias agents (`@task`, `@smol`, `@slow`) fall back to the parent's
// active session model (`ctx.ModelQualified()`), NOT `Model: nil`.
func TestRoleAliasFallsBackToParentActiveModel(t *testing.T) {
	resetAllRegistries(t)
	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	rt := NewRuntime(cfg)
	ext := sdk.New("test-parent-model")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID:   "parent-model-sess",
		ActiveModel: map[string]any{"provider": "openai", "id": "gpt-5-mini", "contextWindow": 128000},
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "ok")
			return nil
		},
	})

	if _, errMsg := h.callTool("task", map[string]any{
		"context": "Parent model check",
		"tasks":   []any{map[string]any{"name": "DefaultRole", "agent": "task", "task": "reply", "solutionSpace": "s"}},
	}); errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}
	children := h.getChildren()
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	children[0].mu.Lock()
	modelMap, _ := children[0].createArgs["model"].(map[string]any)
	children[0].mu.Unlock()
	if modelMap["provider"] != "openai" || modelMap["id"] != "gpt-5-mini" {
		t.Fatalf("expected role-alias child to inherit parent model openai/gpt-5-mini, got %+v", modelMap)
	}
}

// Verify read-only children (no `write` tool, e.g. `scout`, `reviewer`) do NOT
// receive the `# Peers` section, and depth-1 children do NOT receive `task`
// because PiG's host does not bind `createChildSession` on child bridges.
// upstream: test/tools/task-agent-capabilities.test.ts, task/executor.ts:4832-4835
func TestReadOnlyChildOmitsPeersAndDepth1StripsTask(t *testing.T) {
	resetAllRegistries(t)
	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	rt := NewRuntime(cfg)
	ext := sdk.New("test-child-caps")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "caps-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "done")
			return nil
		},
	})

	if _, errMsg := h.callTool("task", map[string]any{
		"context": "Caps test",
		"tasks": []any{
			map[string]any{"name": "Scout1", "agent": "scout", "task": "find files", "solutionSpace": "s"},
			map[string]any{"name": "Task1", "agent": "task", "task": "edit files", "solutionSpace": "s"},
		},
	}); errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}

	children := h.getChildren()
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	var sawScout, sawTask bool
	for _, ch := range children {
		ch.mu.Lock()
		args := ch.createArgs
		ch.mu.Unlock()
		resOpts, _ := args["resources"].(map[string]any)
		appends, _ := resOpts["appendSystemPrompt"].([]any)
		sysPrompt, _ := appends[0].(string)
		if strings.Contains(sysPrompt, "You MUST operate as read-only.") {
			sawScout = true
			if strings.Contains(sysPrompt, "# Peers") {
				t.Fatalf("read-only scout must NOT receive # Peers section:\n%s", sysPrompt)
			}
		} else {
			sawTask = true
			if !strings.Contains(sysPrompt, "# Peers") {
				t.Fatalf("writable task agent should receive # Peers section:\n%s", sysPrompt)
			}
			// Depth-1 child must exclude `task` because PiG CLI host cannot nest child sessions.
			excl, _ := args["excludeTools"].([]any)
			hasTaskExcl := false
			for _, v := range excl {
				if v == "task" {
					hasTaskExcl = true
				}
			}
			if !hasTaskExcl {
				t.Fatalf("expected depth-1 child to exclude 'task', got excludeTools=%v", excl)
			}
		}
	}
	if !sawScout || !sawTask {
		t.Fatalf("expected both scout and task children, got scout=%v task=%v", sawScout, sawTask)
	}
}

// Verify exact TaskSchema shape (no `default` on agent, `additionalProperties: false`
// at both root and item levels) and `RenderSubagentSystemPrompt` spacing + `ts`
// yield schema + `outputSchemaOverridesAgent` paragraph.
// upstream: test/task/task-schema.test.ts, config/prompt-templates.ts:39-50
func TestTaskSchemaExactAndSubagentPromptFormatting(t *testing.T) {
	schema := TaskSchema(DefaultConfig(), DefaultSpawnAgent)
	if schema["additionalProperties"] != false {
		t.Fatalf("root schema missing additionalProperties:false: %+v", schema)
	}
	props := schema["properties"].(map[string]any)
	tasksProp := props["tasks"].(map[string]any)
	item := tasksProp["items"].(map[string]any)
	if item["additionalProperties"] != false {
		t.Fatalf("item schema missing additionalProperties:false: %+v", item)
	}
	itemProps := item["properties"].(map[string]any)
	agentProp := itemProps["agent"].(map[string]any)
	if _, hasDefault := agentProp["default"]; hasDefault {
		t.Fatalf("agent schema must NOT carry 'default', got %+v", agentProp)
	}

	// RenderSubagentSystemPrompt spacing and ```ts yield schema + caller override note.
	prompt := RenderSubagentSystemPrompt(RunRequest{
		ID:                         "WorkerA",
		Agent:                      AgentDefinition{Name: "task", SystemPrompt: "Do the thing."},
		Context:                    "# Goal\nShip it.",
		OutputSchema:               map[string]any{"properties": map[string]any{"ok": map[string]any{"type": "boolean"}}},
		OutputSchemaOverridesAgent: true,
		IRCEnabled:                 false,
	}, PeerRoster{}, true)
	if !strings.Contains(prompt, "Ship it.\n⁂ Coop\nYou are operating on a piece of work assigned to you by the main agent.\n⁂ Completion") {
		t.Fatalf("unexpected subagent prompt section spacing:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Caller schema overrides agent-native output instructions.") {
		t.Fatalf("missing outputSchemaOverridesAgent note:\n%s", prompt)
	}
	if !strings.Contains(prompt, "```ts\n{\n  data: {\n    ok: boolean;\n  };\n}\n```") {
		t.Fatalf("expected TypeScript yield schema block, got:\n%s", prompt)
	}
}

// Verify TUI `wait` description and elapsed wording (`endTurn: true`), and
// preflight error formatting (shortened `~` paths and `none` fallback).
// upstream: test/tools/wait.test.ts, task/task-preflight.test.ts
func TestTUIWaitEndTurnAndPreflightShortPaths(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	rt.Config.WaitFloorMs = 200
	rt.Config.WaitTopMs = 60
	ext := sdk.New("test-tui-wait")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "tui-sess",
		Mode:      "tui",
		HasUI:     true,
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	h.mu.Lock()
	waitDesc := h.toolDescs["wait"]
	h.mu.Unlock()
	if !strings.Contains(waitDesc, "End your turn instead: each of their results and messages starts your next turn.") {
		t.Fatalf("expected TUI endTurn wait description, got:\n%s", waitDesc)
	}

	// Unknown agent preflight must shorten home directory to `~`.
	res, _ := h.callTool("task", map[string]any{
		"context": "c",
		"tasks":   []any{map[string]any{"name": "Bad", "agent": "no-such", "task": "t", "solutionSpace": "s"}},
	})
	txt, _ := res["content"].(string)
	if !strings.Contains(txt, "Searched: ~") {
		t.Fatalf("expected shortened ~ path in Searched:, got:\n%s", txt)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.Contains(txt, home) {
		t.Fatalf("preflight leaked raw home path %q in:\n%s", home, txt)
	}

	// TUI wait timeout uses top window (60ms < floor 200ms) and TUI elapsed wording.
	bj := &fakeBashJob{
		id:      "bg_tui",
		label:   "long build",
		started: time.Now(),
		done:    make(chan struct{}),
		status:  "running",
	}
	jobs.Add("tui-sess", bj)
	defer close(bj.done)

	wRes, errMsg := h.callTool("wait", map[string]any{})
	if errMsg != "" {
		t.Fatalf("wait failed: %s", errMsg)
	}
	wTxt, _ := wRes["content"].(string)
	if !strings.Contains(wTxt, "Nothing else left? End your turn: results and messages start your next one.") {
		t.Fatalf("expected TUI endTurn elapsed line, got:\n%s", wTxt)
	}
}

// Verify `wait` unwatch re-delivers a job that settled while watched if `wait`
// is aborted/interrupted before consuming it, and verify idle parking via
// `task.agentIdleTtlMs` disposes the child session and fires `lifecycle.End`.
// upstream: test/tools/wait.test.ts, task/executor.ts:4789-4791
func TestIdleParkingDisposesChildAndFiresLifecycleEnd(t *testing.T) {
	resetAllRegistries(t)
	cfg := DefaultConfig()
	// Long enough not to fire by itself: the test arms the park timer once it has read what parking removes.
	cfg.AgentIdleTTLMs = 10 * 60 * 1000
	rt := NewRuntime(cfg)
	ext := sdk.New("test-idle-park")
	RegisterWithRuntime(ext, rt)

	var endedMu sync.Mutex
	var ended []string
	lifecycle.OnEnd("park-observer", func(sessionID string) {
		endedMu.Lock()
		ended = append(ended, sessionID)
		endedMu.Unlock()
	})

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID:     "park-sess",
		NoSessionFile: true,
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "quick answer")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	if _, errMsg := h.callTool("task", map[string]any{
		"context": "Park test",
		"tasks":   []any{map[string]any{"name": "ParkMe", "task": "do fast", "solutionSpace": "s"}},
	}); errMsg != "" {
		t.Fatalf("spawn failed: %s", errMsg)
	}
	_, _ = h.callTool("wait", map[string]any{})

	// Verify child's recorded local:// root matches parent's urls.LocalRoot (in-memory session without .jsonl).
	ref, ok := rt.Registry.Get("ParkMe")
	if !ok {
		t.Fatalf("expected ParkMe ref")
	}
	ref.mu.Lock()
	childSID := ref.ChildSessionID
	ref.mu.Unlock()
	if childSID == "" {
		t.Fatalf("expected non-empty ChildSessionID")
	}
	if lr, ok := rt.Registry.ChildLocalRoot(childSID); !ok || !strings.Contains(lr, filepath.Join("omp-local", "park-sess")) {
		t.Fatalf("expected child local:// root to match parent temp root omp-local/park-sess, got (%q, %v)", lr, ok)
	}

	// Parking ends the child session, which drops the lineage record read above; so the timer starts only now.
	rt.Registry.ArmIdleParkTimer(ref, 40)

	// Wait for idle park timer to fire and dispose child session.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ref.mu.Lock()
		st := ref.Status
		ref.mu.Unlock()
		if st == "parked" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ref.mu.Lock()
	finalStatus := ref.Status
	finalSess := ref.Session
	ref.mu.Unlock()
	if finalStatus != "parked" || finalSess != nil {
		t.Fatalf("expected parked with nil session after idle TTL, got status=%q sess=%v", finalStatus, finalSess)
	}
	// The status flips to "parked" before the session is disposed and lifecycle.End fires (omp commits the
	// status first too: registry/agent-lifecycle.ts:442-446), so the hook is awaited, not read at once.
	sawExactChild := func() bool {
		endedMu.Lock()
		defer endedMu.Unlock()
		return slices.Contains(ended, childSID)
	}
	for !sawExactChild() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !sawExactChild() {
		endedMu.Lock()
		defer endedMu.Unlock()
		t.Fatalf("expected lifecycle.End for exact child session ID %q, got %v", childSID, ended)
	}
}

// Verify wire `details` fields required by `internal/ansi` native renderers:
// `AgentProgress.RetryState`, `AgentProgress.RetryFailure`,
// `AgentProgress.InflightTaskDetails`, and `SingleResult.RetryFailure`.
// upstream: packages/coding-agent/src/task/executor.ts:2488-2511, 2269, 3382
func TestDetailsRetryStateRetryFailureAndInflightTaskDetails(t *testing.T) {
	resetAllRegistries(t)
	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	rt := NewRuntime(cfg)
	ext := sdk.New("test-details-wire")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "details-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			// Emit auto_retry_start -> auto_retry_end (failure) -> tool_execution_update (task).
			h.emitChildEvent(child, map[string]any{
				"type":         "auto_retry_start",
				"attempt":      1,
				"maxAttempts":  3,
				"delayMs":      2000,
				"errorMessage": "429 rate limit",
			})
			h.emitChildEvent(child, map[string]any{
				"type":       "auto_retry_end",
				"attempt":    1,
				"success":    false,
				"finalError": "429 rate limit exhausted",
			})
			h.emitChildEvent(child, map[string]any{
				"type":     "tool_execution_update",
				"toolName": "task",
				"partialResult": map[string]any{
					"details": map[string]any{
						"results":         []any{},
						"totalDurationMs": 10,
						"progress": []any{
							map[string]any{
								"index": 0, "id": "Nested1", "agent": "scout", "agentSource": "bundled",
								"status": "running", "task": "nested", "recentTools": []any{}, "recentOutput": []any{},
							},
						},
					},
				},
			})
			h.finishChildWithText(child, "done after retry")
			return nil
		},
	})

	res, errMsg := h.callTool("task", map[string]any{
		"context": "Details check",
		"tasks":   []any{map[string]any{"name": "RetryChild", "task": "work", "solutionSpace": "s"}},
	})
	if errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}

	// Inspect tool_update frames for RetryState and InflightTaskDetails.
	h.mu.Lock()
	updates := append([]map[string]any(nil), h.toolUpdates...)
	h.mu.Unlock()
	var sawRetryState, sawInflight bool
	for _, u := range updates {
		resObj, _ := u["result"].(map[string]any)
		det, _ := resObj["details"].(map[string]any)
		progs, _ := det["progress"].([]any)
		for _, rawP := range progs {
			pm, _ := rawP.(map[string]any)
			if rs, ok := pm["retryState"].(map[string]any); ok && rs["errorMessage"] == "429 rate limit" {
				sawRetryState = true
			}
			if inf, ok := pm["inflightTaskDetails"].(map[string]any); ok {
				if np, ok := inf["progress"].([]any); ok && len(np) == 1 {
					sawInflight = true
				}
			}
		}
	}
	if !sawRetryState {
		t.Fatalf("expected retryState on tool_update AgentProgress details (got %d updates)", len(updates))
	}
	if !sawInflight {
		t.Fatalf("expected inflightTaskDetails on tool_update AgentProgress details")
	}

	// Inspect final SingleResult.retryFailure on wire details.
	det, _ := res["details"].(map[string]any)
	results, _ := det["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r0, _ := results[0].(map[string]any)
	rf, _ := r0["retryFailure"].(map[string]any)
	if rf == nil || rf["errorMessage"] != "429 rate limit exhausted" {
		t.Fatalf("expected SingleResult.retryFailure on wire details, got %+v", r0)
	}
}

// Verify `session_before_switch` does NOT tear down session state (because any
// extension may cancel the switch), while `session_shutdown` does.
func TestSessionBeforeSwitchDoesNotTearDownSession(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	ext := sdk.New("test-before-switch")
	RegisterWithRuntime(ext, rt)

	started := make(chan struct{})
	release := make(chan struct{})
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "switch-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			close(started)
			select {
			case <-child.abortCh:
				t.Errorf("child must NOT be aborted by cancellable session_before_switch")
			case <-release:
				h.finishChildWithText(child, "survived switch cancel")
			}
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	if _, errMsg := h.callTool("task", map[string]any{
		"context": "Switch test",
		"tasks":   []any{map[string]any{"name": "Survivor", "task": "wait", "solutionSpace": "s"}},
	}); errMsg != "" {
		t.Fatalf("spawn failed: %s", errMsg)
	}
	<-started

	// Fire session_before_switch: must NOT cancel Survivor.
	h.fireEvent(sdk.EventSessionBeforeSwitch, map[string]any{})
	j, ok := jobs.Get("switch-sess", "Survivor")
	if !ok || j.Status() != "running" {
		t.Fatalf("expected Survivor still running after session_before_switch, ok=%v status=%v", ok, j)
	}
	close(release)
	select {
	case <-j.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("Survivor did not complete")
	}
	if j.Status() != "completed" {
		t.Fatalf("expected Survivor completed, got %q", j.Status())
	}
	// Wait for Survivor to be consumed cleanly before test ends.
	_, _ = h.callTool("wait", map[string]any{})
}

// Verify `wait` re-delivers a watched job that settled while `wait` was aborted
// during its settle window (never silently dropping the result).
// upstream: test/tools/wait.test.ts ("interrupt re-enqueue")
func TestWaitUnwatchRedeliversSettledJobOnAbort(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	rt.Config.WaitSettleMs = 300
	ext := sdk.New("test-wait-unwatch")
	RegisterWithRuntime(ext, rt)

	var deliveredMu sync.Mutex
	var deliveredIDs []string
	jobs.SetDeliverySink(func(sessionID string, _ string, content string, _ bool, _ sdk.SendMessageOptions) error {
		if sessionID != "unwatch-sess" {
			return nil
		}
		deliveredMu.Lock()
		deliveredIDs = append(deliveredIDs, content)
		deliveredMu.Unlock()
		return nil
	})
	t.Cleanup(func() { jobs.SetDeliverySink(nil) })

	// Create two AgentJobs: J1 finishes quickly to enter the settle window, J2 stays running.
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	j1 := NewAgentJob("J1", "J1", cancel1, AgentProgress{ID: "J1", Status: "running"})
	j2 := NewAgentJob("J2", "J2", cancel1, AgentProgress{ID: "J2", Status: "running"})
	jobs.Add("unwatch-sess", j1)
	jobs.Add("unwatch-sess", j2)
	_ = ctx1

	h := startFakeHost(t, ext, fakeHostOptions{SessionID: "unwatch-sess"})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	// Run wait in a goroutine, finish J1 to enter settle window, then cancel the wait request.
	cancelCh := make(chan struct{})
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_, _ = h.callToolAsync("wait", map[string]any{}, cancelCh)
	}()
	h.untilWaitWatches()
	j1.finish("completed", "J1 settled during wait", "", nil, nil, false, nil)
	time.Sleep(30 * time.Millisecond)
	close(cancelCh)
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("wait did not return after cancel")
	}
	// Unwatch defer must deliver J1 even though OnFinish saw SkipDelivery()==true.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		deliveredMu.Lock()
		n := len(deliveredIDs)
		deliveredMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	j2.finish("cancelled", "", "", nil, nil, false, nil)

	deliveredMu.Lock()
	got := append([]string(nil), deliveredIDs...)
	deliveredMu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], "J1 settled during wait") {
		t.Fatalf("expected unwatched J1 to be re-delivered once, got %v", got)
	}
}

// N1: Verify `wait` and the `internal/jobs` delivery listener NEVER hand over
// the same job twice — neither for non-agent (bash) jobs settling mid-wait nor
// for an agent job whose follow-up delivery is already in flight when `wait`
// starts (150 bash runs + slow-sink in-flight delivery race).
// upstream: test/tools/wait.test.ts, tools/wait.ts:466,637
func TestWaitAndListenerSingleHandoverAllJobKinds(t *testing.T) {
	resetAllRegistries(t)
	rt := NewRuntime(DefaultConfig())
	rt.Config.WaitSettleMs = 0
	ext := sdk.New("test-wait-single-handover")
	RegisterWithRuntime(ext, rt)

	const sessID = "n1-single-handover-sess"
	h := startFakeHost(t, ext, fakeHostOptions{SessionID: sessID})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	// Part 1: 150 runs of a non-agent (KindBash) job settling ~2ms into `wait`.
	// Every job must be delivered EXACTLY ONCE (either in wait's result OR via
	// the async-result follow-up listener, never both and never lost).
	var followMu sync.Mutex
	followCounts := map[string]int{}
	jobs.SetDeliverySink(func(sid, customType, content string, _ bool, _ sdk.SendMessageOptions) error {
		if sid != sessID || customType != "async-result" {
			return nil
		}
		followMu.Lock()
		for id := range followCounts {
			if strings.Contains(content, "TOKEN-"+id+"-END") {
				followCounts[id]++
			}
		}
		followMu.Unlock()
		return nil
	})
	t.Cleanup(func() { jobs.SetDeliverySink(nil) })

	bothCount := 0
	lostCount := 0
	for i := range 150 {
		jobID := fmt.Sprintf("bg_%d", i+1)
		token := "TOKEN-" + jobID + "-END"
		followMu.Lock()
		followCounts[jobID] = 0
		followMu.Unlock()

		jb := &fakeBashJob{
			id:      jobID,
			label:   "echo " + jobID,
			started: time.Now(),
			done:    make(chan struct{}),
			status:  "running",
		}
		jobs.Add(sessID, jb)

		go func(j *fakeBashJob, tok string) {
			time.Sleep(2 * time.Millisecond)
			j.mu.Lock()
			j.status = "completed"
			j.result = tok
			j.mu.Unlock()
			close(j.done)
		}(jb, token)

		res, errMsg := h.callTool("wait", map[string]any{})
		if errMsg != "" {
			t.Fatalf("run %d wait failed: %s", i, errMsg)
		}
		content, _ := res["content"].(string)
		inWait := strings.Contains(content, token)

		// Give any stray listener goroutine a chance to run.
		if !inWait {
			deadline := time.Now().Add(500 * time.Millisecond)
			for time.Now().Before(deadline) {
				followMu.Lock()
				fc := followCounts[jobID]
				followMu.Unlock()
				if fc > 0 {
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
		followMu.Lock()
		fc := followCounts[jobID]
		followMu.Unlock()

		if inWait && fc > 0 {
			bothCount++
		} else if !inWait && fc == 0 {
			lostCount++
		} else if fc > 1 {
			bothCount++
		}
		jobs.Remove(sessID, jobID)
	}
	if bothCount > 0 || lostCount > 0 {
		t.Fatalf("non-agent wait/listener race over 150 runs: duplicates=%d lost=%d (want 0/0)", bothCount, lostCount)
	}

	// Part 2: Agent job whose follow-up send is ALREADY in flight (slow 150ms
	// delivery sink) when `wait` enters: `wait` must NOT also recover/return the
	// result body because the listener already holds jobs.Claim.
	inFlightEntered := make(chan struct{})
	releaseSink := make(chan struct{})
	var slowDeliveries atomic.Int32
	jobs.SetDeliverySink(func(sid, customType, content string, _ bool, _ sdk.SendMessageOptions) error {
		if sid != sessID || customType != "async-result" {
			return nil
		}
		if strings.Contains(content, "SLOW-AGENT-PAYLOAD") {
			close(inFlightEntered)
			<-releaseSink
			slowDeliveries.Add(1)
		}
		return nil
	})
	_, cancelSlow := context.WithCancel(context.Background())
	defer cancelSlow()
	aj := NewAgentJob("SlowAgent", "SlowAgent", cancelSlow, AgentProgress{ID: "SlowAgent", Status: "running"})
	jobs.Add(sessID, aj)
	aj.finish("completed", "SLOW-AGENT-PAYLOAD", "", nil, nil, false, nil)

	select {
	case <-inFlightEntered:
	case <-time.After(2 * time.Second):
		t.Fatalf("delivery listener did not enter slow sink")
	}
	waitRes, errMsg := h.callTool("wait", map[string]any{})
	close(releaseSink)
	if errMsg != "" {
		t.Fatalf("wait during slow send failed: %s", errMsg)
	}
	waitTxt, _ := waitRes["content"].(string)
	if strings.Contains(waitTxt, "SLOW-AGENT-PAYLOAD") {
		t.Fatalf("wait handed over job body while listener already held claim:\n%s", waitTxt)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && slowDeliveries.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if slowDeliveries.Load() != 1 {
		t.Fatalf("expected exactly 1 listener delivery, got %d", slowDeliveries.Load())
	}
}

// N2: Two concurrent top-level sessions on defaultRuntime spawning colliding IDs:
// A spawns Alpha; B spawns Alpha and Beta.
// Verifies:
//   - A can message and read ITS Alpha (not B's);
//   - A cannot read B's Beta via agent:// or history://;
//   - bare history:// lists only the caller's session agents (one Main, no cross-session rows);
//   - A ending disposes A's Alpha and calls lifecycle.End for A's Alpha's child session ID,
//     leaving B's Alpha and Beta live and untouched.
func TestTwoLiveSessionsRegistryCollisionIsolation(t *testing.T) {
	resetAllRegistries(t)
	ResetForTests()
	t.Cleanup(ResetForTests)
	settings.ResetForTests()
	t.Cleanup(settings.ResetForTests)

	store, err := settings.NewIsolated(map[string]any{"task.agentIdleTtlMs": 0})
	if err != nil {
		t.Fatalf("settings.NewIsolated: %v", err)
	}
	settings.SetStoreForTests(func(_ sdk.Context) *settings.Store { return store })

	var endedMu sync.Mutex
	ended := map[string]int{}
	lifecycle.OnEnd("two-sess-observer", func(sessionID string) {
		endedMu.Lock()
		ended[sessionID]++
		endedMu.Unlock()
	})

	extA := sdk.New("sess-A-ext")
	extB := sdk.New("sess-B-ext")
	Register(extA)
	Register(extB)

	var wakeAMu sync.Mutex
	var wakeAPrompts []string
	hA := startFakeHost(t, extA, fakeHostOptions{
		SessionID: "sess-A",
		OnChild: func(h *fakeHost, child *fakeChildState, prompt string) error {
			wakeAMu.Lock()
			wakeAPrompts = append(wakeAPrompts, prompt)
			wakeAMu.Unlock()
			h.finishChildWithText(child, "OUTPUT-FROM-A-ALPHA")
			return nil
		},
	})
	hB := startFakeHost(t, extB, fakeHostOptions{
		SessionID: "sess-B",
		OnChild: func(h *fakeHost, child *fakeChildState, prompt string) error {
			if strings.Contains(prompt, "beta-task") {
				h.finishChildWithText(child, "OUTPUT-FROM-B-BETA")
			} else {
				h.finishChildWithText(child, "OUTPUT-FROM-B-ALPHA")
			}
			return nil
		},
	})
	hA.fireEvent(sdk.EventSessionStart, map[string]any{})
	hB.fireEvent(sdk.EventSessionStart, map[string]any{})

	// A spawns Alpha; B spawns Alpha and Beta.
	if _, errMsg := hA.callTool("task", map[string]any{
		"context": "A",
		"tasks":   []any{map[string]any{"name": "Alpha", "task": "alpha-a", "solutionSpace": "s"}},
	}); errMsg != "" {
		t.Fatalf("A spawn Alpha failed: %s", errMsg)
	}
	_, _ = hA.callTool("wait", map[string]any{})

	if _, errMsg := hB.callTool("task", map[string]any{
		"context": "B",
		"tasks": []any{
			map[string]any{"name": "Alpha", "task": "alpha-b", "solutionSpace": "s"},
			map[string]any{"name": "Beta", "task": "beta-task", "solutionSpace": "s"},
		},
	}); errMsg != "" {
		t.Fatalf("B spawn Alpha+Beta failed: %s", errMsg)
	}
	_, _ = hB.callTool("wait", map[string]any{})

	ctxA := defaultRuntime.sessionContextFor("sess-A", sdk.Context{})
	ctxB := defaultRuntime.sessionContextFor("sess-B", sdk.Context{})

	// 1. A reads agent://Alpha and history://Alpha -> gets A's Alpha, not B's.
	resAlphaA, err := urls.Resolve(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "agent://Alpha")
	if err != nil || !strings.Contains(resAlphaA.Content, "OUTPUT-FROM-A-ALPHA") || strings.Contains(resAlphaA.Content, "OUTPUT-FROM-B-ALPHA") {
		t.Fatalf("A agent://Alpha got err=%v res=%+v", err, resAlphaA)
	}
	histAlphaA, err := urls.Resolve(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "history://Alpha")
	if err != nil || !strings.Contains(histAlphaA.Content, "OUTPUT-FROM-A-ALPHA") || strings.Contains(histAlphaA.Content, "OUTPUT-FROM-B-ALPHA") {
		t.Fatalf("A history://Alpha got err=%v res=%+v", err, histAlphaA)
	}

	// 2. A CANNOT read B's Beta via agent://Beta or history://Beta.
	if res, err := urls.Resolve(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "agent://Beta"); err == nil {
		t.Fatalf("A must not read B's agent://Beta, got %+v", res)
	}
	if res, err := urls.Resolve(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "history://Beta"); err == nil {
		t.Fatalf("A must not read B's history://Beta, got %+v", res)
	}

	// 3. Bare history:// in A lists only A's Main and Alpha (one Main row, no Beta).
	histIdxA, err := urls.Resolve(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "history://")
	if err != nil {
		t.Fatalf("A history:// failed: %v", err)
	}
	if strings.Count(histIdxA.Content, "| Main | running | main |") != 1 || !strings.Contains(histIdxA.Content, "| Alpha |") || strings.Contains(histIdxA.Content, "| Beta |") {
		t.Fatalf("A bare history:// leaked or duplicated rows:\n%s", histIdxA.Content)
	}
	// history://Main in A must resolve unambiguously (not fail as ambiguous).
	if _, err := urls.Resolve(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "history://Main"); err != nil && strings.Contains(err.Error(), "Unknown agent") {
		t.Fatalf("A history://Main failed as ambiguous/unknown: %v", err)
	}

	// 4. A can message ITS Alpha via write agent://Alpha and A's child receives it.
	wr, err := urls.Write(urls.Context{SDK: ctxA, Cwd: hA.opts.Cwd}, "agent://Alpha", "follow-up for A")
	if err != nil || wr.IsError || !strings.Contains(wr.Text, "Delivered to Alpha") {
		t.Fatalf("A write agent://Alpha failed: wr=%+v err=%v", wr, err)
	}
	_, _ = hA.callTool("wait", map[string]any{})
	wakeAMu.Lock()
	gotWake := false
	for _, p := range wakeAPrompts {
		if strings.Contains(p, "follow-up for A") {
			gotWake = true
		}
	}
	wakeAMu.Unlock()
	if !gotWake {
		t.Fatalf("A's Alpha never received follow-up message; prompts=%v", wakeAPrompts)
	}

	// Record A's Alpha child session ID and B's refs before ending A.
	refAlphaA, ok := defaultRuntime.Registry.GetInSession("sess-A", "Alpha")
	if !ok {
		t.Fatalf("expected sess-A Alpha ref")
	}
	refAlphaA.mu.Lock()
	childSIDA := refAlphaA.ChildSessionID
	refAlphaA.mu.Unlock()
	if childSIDA == "" {
		t.Fatalf("expected non-empty ChildSessionID for A's Alpha")
	}

	// 5. Ending A disposes A's Alpha and calls lifecycle.End(childSIDA),
	// leaving B's Alpha and Beta live and readable.
	hA.fireEvent(sdk.EventSessionShutdown, map[string]any{})

	childrenA := hA.getChildren()
	if len(childrenA) != 1 {
		t.Fatalf("expected 1 child on hA, got %d", len(childrenA))
	}
	childrenA[0].mu.Lock()
	disposedA := childrenA[0].disposed
	childrenA[0].mu.Unlock()
	if !disposedA {
		t.Fatalf("ending sess-A did not dispose A's Alpha child session")
	}
	endedMu.Lock()
	endedChildA := ended[childSIDA]
	endedMu.Unlock()
	if endedChildA != 1 {
		t.Fatalf("expected lifecycle.End called once for A's Alpha (%q), got %d", childSIDA, endedChildA)
	}

	// B's Alpha and Beta must still be live and readable.
	for _, c := range hB.getChildren() {
		c.mu.Lock()
		d := c.disposed
		c.mu.Unlock()
		if d {
			t.Fatalf("ending sess-A disposed B's child %s", c.id)
		}
	}
	resBetaB, err := urls.Resolve(urls.Context{SDK: ctxB, Cwd: hB.opts.Cwd}, "agent://Beta")
	if err != nil || !strings.Contains(resBetaB.Content, "OUTPUT-FROM-B-BETA") {
		t.Fatalf("B agent://Beta broken after A ended: res=%+v err=%v", resBetaB, err)
	}
}

// N3: A wake that races idle parking MUST NEVER report `Delivered` and drop
// the message. Run 300 iterations with a tiny TTL (4ms) and sends aimed at it:
// every run reporting Delivered/woken MUST be received by the child.
func TestWakeRacingIdleParkNeverDropsDeliveredMessage(t *testing.T) {
	resetAllRegistries(t)
	cfg := DefaultConfig()
	cfg.AgentIdleTTLMs = 4
	rt := NewRuntime(cfg)
	ext := sdk.New("test-wake-park-race")
	RegisterWithRuntime(ext, rt)

	const sessID = "wake-park-race-sess"
	var receivedMu sync.Mutex
	received := map[string]bool{}

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: sessID,
		OnChild: func(h *fakeHost, child *fakeChildState, prompt string) error {
			if idx := strings.Index(prompt, "RACE-MSG-"); idx >= 0 {
				end := idx + len("RACE-MSG-")
				for end < len(prompt) && prompt[end] >= '0' && prompt[end] <= '9' {
					end++
				}
				tag := prompt[idx:end]
				receivedMu.Lock()
				received[tag] = true
				receivedMu.Unlock()
			}
			h.finishChildWithText(child, "ok")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	const totalRuns = 300
	wokenCount := 0
	refusedCount := 0
	droppedCount := 0

	for i := range totalRuns {
		name := fmt.Sprintf("Racer%d", i+1)
		tag := fmt.Sprintf("RACE-MSG-%d", i+1)
		// Use sync spawn? No, keep-alive requires async; spawn directly via RunSubagent
		// on the fakeHost's session context so 300 iterations complete fast and deterministically.
		sessCtx := rt.sessionContextFor(sessID, sdk.Context{})
		ag, _ := GetAgent(LoadBundledAgents(), "task")
		_ = RunSubagent(context.Background(), rt.Registry, RunRequest{
			SDK:             sessCtx,
			ParentSessionID: sessID,
			ParentAgentID:   MainAgentID,
			Cwd:             h.opts.Cwd,
			ID:              name,
			Agent:           ag,
			Assignment:      "initial",
			AgentIdleTTLMs:  4,
			KeepAlive:       true,
		})
		// Aim sends right around the 4ms idle park timer boundary (0..6ms).
		delay := time.Duration(i%7) * time.Millisecond
		if delay > 0 {
			time.Sleep(delay)
		}
		_, det, isErr := rt.Registry.SendMessageInSession(sessID, MainAgentID, name, tag, false, "", false)
		delivered := !isErr && len(det.Receipts) == 1 && det.Receipts[0].Outcome != "failed"
		if delivered {
			wokenCount++
			// Wait briefly for the wake goroutine to deliver prompt to child.
			deadline := time.Now().Add(1 * time.Second)
			for time.Now().Before(deadline) {
				receivedMu.Lock()
				got := received[tag]
				receivedMu.Unlock()
				if got {
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
			receivedMu.Lock()
			got := received[tag]
			receivedMu.Unlock()
			if !got {
				droppedCount++
			}
		} else {
			refusedCount++
		}
		// Ensure child is cleaned up before next iteration so resources stay bounded.
		if ref, ok := rt.Registry.GetInSession(sessID, name); ok {
			ref.StopIdleTimer()
			rt.Registry.ParkIdleRef(ref)
		}
		rt.Registry.UnregisterInSession(sessID, name)
	}
	if droppedCount > 0 {
		t.Fatalf("wake-vs-park race over %d runs: %d messages reported delivered but never received (woken=%d, refused=%d)", totalRuns, droppedCount, wokenCount, refusedCount)
	}
	if wokenCount == 0 || refusedCount == 0 {
		t.Logf("note: wake-vs-park distribution woken=%d refused=%d", wokenCount, refusedCount)
	}
}

// Verify `yield`, `wait`, and `task` parameter schemas against omp v19.1.1's
// real captured schemas in `internal/schemaorder/omp_schemas.json` so they
// cannot drift, and verify `yield`'s dynamic schema-description rendering and
// closed-root argument handling.
// upstream: packages/coding-agent/src/tools/yield.ts, tools/wait.ts, task/types.ts
func TestAgentsSchemasMatchOmpSchemasJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "schemaorder", "omp_schemas.json"))
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	var captured map[string]struct {
		Parameters any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("parse omp_schemas.json: %v", err)
	}
	roundTrip := func(v any) any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal schema: %v", err)
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal schema: %v", err)
		}
		return out
	}

	// 1. `yield` (captured with unconstrained schema `true`): exact content match.
	yieldDef := BuildYieldToolDefinition(true, nil, nil)
	gotYield := roundTrip(yieldDef.Parameters)
	wantYield := captured["yield"].Parameters
	if !reflect.DeepEqual(gotYield, wantYield) {
		gotJSON, _ := json.MarshalIndent(gotYield, "", "  ")
		wantJSON, _ := json.MarshalIndent(wantYield, "", "  ")
		t.Fatalf("yield schema content differs from omp v19.1.1:\ngot:\n%s\nwant:\n%s", gotJSON, wantJSON)
	}

	// Verify dynamic schema rendering in `yield` properties.data (spread caller
	// schema + section variants `anyOf` + caller keyword order in description).
	objSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"answer": map[string]any{"type": "number"}, "summary": map[string]any{"type": "string"}},
		"required":   []string{"answer", "summary"},
	}
	objYield := BuildYieldToolDefinition(objSchema, nil, nil)
	objYieldRT := roundTrip(objYield.Parameters).(map[string]any)
	objDataProp := objYieldRT["properties"].(map[string]any)["data"].(map[string]any)
	wantObjDesc := "Structured output matching the schema:\n{\n  \"type\": \"object\",\n  \"properties\": {\n    \"answer\": {\n      \"type\": \"number\"\n    },\n    \"summary\": {\n      \"type\": \"string\"\n    }\n  },\n  \"required\": [\n    \"answer\",\n    \"summary\"\n  ]\n}"
	if objDataProp["description"] != wantObjDesc {
		t.Fatalf("yield object-schema data.description mismatch:\ngot:  %q\nwant: %q", objDataProp["description"], wantObjDesc)
	}
	anyOf, ok := objDataProp["anyOf"].([]any)
	if !ok || len(anyOf) != 3 {
		t.Fatalf("yield object-schema data.anyOf = %#v, want 3 branches [fullObject, number, string]", objDataProp["anyOf"])
	}
	nilYield := BuildYieldToolDefinition(nil, nil, nil)
	nilDataProp := nilYield.Parameters["properties"].(map[string]any)["data"].(map[string]any)
	if nilDataProp["description"] != "Structured JSON output (no schema specified)" || nilDataProp["type"] != "object" || nilDataProp["additionalProperties"] != true {
		t.Fatalf("yield nil-schema data property mismatch: %+v", nilDataProp)
	}
	falseYield := BuildYieldToolDefinition(false, nil, nil)
	falseDataProp := falseYield.Parameters["properties"].(map[string]any)["data"].(map[string]any)
	if desc, _ := falseDataProp["description"].(string); !strings.Contains(desc, "boolean false schema rejects all outputs") {
		t.Fatalf("yield false-schema data.description mismatch: %+v", falseDataProp)
	}
	// Verify closed root rejects unknown extra fields while still accepting all declared fields (including nil fillers).
	resUnknown, err := yieldDef.Execute(sdk.Context{}, map[string]any{"data": map[string]any{"ok": true}, "extra": 123})
	if err != nil {
		t.Fatalf("yield Execute returned error: %v", err)
	}
	if tr, ok := resUnknown.(sdk.ToolResult); !ok || !tr.IsError || !strings.Contains(tr.Content, "unsupported root field") {
		t.Fatalf("expected unsupported root field rejection, got %+v", resUnknown)
	}
	resValid, err := yieldDef.Execute(sdk.Context{}, map[string]any{"data": map[string]any{"ok": true}, "error": nil, "type": nil})
	if err != nil {
		t.Fatalf("yield Execute valid returned error: %v", err)
	}
	if tr, ok := resValid.(sdk.ToolResult); !ok || tr.IsError {
		t.Fatalf("expected valid yield with nil optional fillers to succeed, got %+v", resValid)
	}

	// 2. `wait`: exact content match.
	gotWait := roundTrip(WaitSchema())
	wantWait := captured["wait"].Parameters
	if !reflect.DeepEqual(gotWait, wantWait) {
		t.Fatalf("wait schema content differs from omp v19.1.1:\ngot:  %#v\nwant: %#v", gotWait, wantWait)
	}

	// 3. `task`: matches omp v19.1.1 identically except the intentionally omitted
	// `tasks[].tools` property (eval-defined subagent tools are not ported).
	gotTask := roundTrip(TaskSchema(DefaultConfig(), DefaultSpawnAgent))
	wantTask := roundTrip(captured["task"].Parameters).(map[string]any)
	wantTaskItemProps := wantTask["properties"].(map[string]any)["tasks"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, hasTools := wantTaskItemProps["tools"]; !hasTools {
		t.Fatalf("expected omp captured task schema to include tasks[].tools")
	}
	delete(wantTaskItemProps, "tools")
	if !reflect.DeepEqual(gotTask, wantTask) {
		gotJSON, _ := json.MarshalIndent(gotTask, "", "  ")
		wantJSON, _ := json.MarshalIndent(wantTask, "", "  ")
		t.Fatalf("task schema content differs from omp v19.1.1 (beyond omitted tasks[].tools):\ngot:\n%s\nwant:\n%s", gotJSON, wantJSON)
	}
}

// fakeFailedBashJob mimics a failed managedJob implementing jobs.Job + jobs.JobDetailer.
type fakeFailedBashJob struct {
	id         string
	label      string
	started    time.Time
	ended      time.Time
	done       chan struct{}
	resultText string
	errorText  string
}

func (f *fakeFailedBashJob) ID() string              { return f.id }
func (f *fakeFailedBashJob) Kind() jobs.Kind         { return jobs.KindBash }
func (f *fakeFailedBashJob) Label() string           { return f.label }
func (f *fakeFailedBashJob) Started() time.Time      { return f.started }
func (f *fakeFailedBashJob) Done() <-chan struct{}   { return f.done }
func (f *fakeFailedBashJob) Status() string          { return "failed" }
func (f *fakeFailedBashJob) Output() string          { return f.resultText }
func (f *fakeFailedBashJob) Result() string          { return f.resultText }
func (f *fakeFailedBashJob) Cancel()                 {}
func (f *fakeFailedBashJob) Ended() time.Time        { return f.ended }
func (f *fakeFailedBashJob) ErrorText() string       { return f.errorText }
func (f *fakeFailedBashJob) Command() string         { return f.label }
func (f *fakeFailedBashJob) Intent() string          { return "" }
func (f *fakeFailedBashJob) Details() map[string]any { return nil }
func (f *fakeFailedBashJob) SkipDelivery() bool      { return false }

// TestRound3_WaitPrefersFailedJobErrorTextAndKillAgent verifies:
//   - wait_tool.go renders a failed job's ErrorText with "Error: ..." rather than
//     preferring ResultText in a fenced block (Triage #8, job-control.ts:446-447).
//   - Registry.KillAgent marks a subagent tombstone "aborted" and returns omp's
//     killed-agent message (Triage #7, job-control.ts:641-681).
func TestRound3_WaitPrefersFailedJobErrorTextAndKillAgent(t *testing.T) {
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	rt := NewRuntime(DefaultConfig())
	sessID := "sess-round3-wait-failed"
	done := make(chan struct{})
	close(done)
	fj := &fakeFailedBashJob{
		id:         "bg_1",
		label:      "echo started; sleep 20; echo never",
		started:    time.Now().Add(-3 * time.Second),
		ended:      time.Now(),
		done:       done,
		resultText: "should-not-win-over-error-text",
		errorText:  "[Command timed out after 3 seconds]\nstarted",
	}
	jobs.Add(sessID, fj)

	snaps := rt.snapshotJobListWithClaims(sessID, []jobs.Job{fj}, map[string]bool{"bg_1": true})
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].ResultText != "" {
		t.Fatalf("expected empty ResultText on failed job snapshot, got %q", snaps[0].ResultText)
	}
	if snaps[0].ErrorText != fj.errorText {
		t.Fatalf("expected ErrorText=%q, got %q", fj.errorText, snaps[0].ErrorText)
	}

	res := rt.buildWaitResult(context.Background(), sessID, []jobs.Job{fj}, map[string]bool{}, time.Now(), map[string]bool{"bg_1": true})
	if strings.Contains(res.Content, "```") {
		t.Fatalf("failed job should render as Error: line, not fenced result:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "Error: [Command timed out after 3 seconds]\nstarted") {
		t.Fatalf("expected omp Error: line in wait output:\n%s", res.Content)
	}

	// KillAgent kills a registered subagent and leaves an aborted tombstone.
	ref := &AgentRef{
		ID:        "SlowChild",
		Kind:      "sub",
		ParentID:  MainAgentID,
		Status:    "running",
		SessionID: sessID,
	}
	rt.Registry.Register(ref)
	status, msg := rt.Registry.KillAgent(sessID, "SlowChild")
	if status != "cancelled" {
		t.Fatalf("KillAgent status = %q, want cancelled", status)
	}
	wantMsg := "Killed agent SlowChild; its transcript stays at history://SlowChild."
	if msg != wantMsg {
		t.Fatalf("KillAgent msg = %q, want %q", msg, wantMsg)
	}
	gotRef, ok := rt.Registry.GetInSession(sessID, "SlowChild")
	if !ok || gotRef.Status != "aborted" {
		t.Fatalf("expected aborted tombstone in registry, got ok=%v ref=%+v", ok, gotRef)
	}
	if st, _ := rt.Registry.KillAgent(sessID, "Missing"); st != "not_found" {
		t.Fatalf("expected not_found for unknown agent, got %q", st)
	}
}

// Verify that subagents with explicit frontmatter `tools:` record their exact
// resolved tool order (frontmatter order plus upstream's appended `task` /
// `wait` / `yield`, and `exec` expanded at tail per `task/executor.ts:4800-4830`
// and `tools/index.ts:862-869`) on ChildSessionMeta, that `ExplicitTools(ctx)`
// returns that order for the child and nil for agents without `tools:` and
// top-level sessions, and that `lifecycle.End` releases it with the session.
// upstream: packages/coding-agent/src/tools/index.ts:862-869, task/executor.ts:4800-4830
func TestExplicitToolsOrderAndLifecycle(t *testing.T) {
	resetAllRegistries(t)
	ResetForTests()
	t.Cleanup(ResetForTests)

	bundled := LoadBundledAgents()
	reviewer, ok := GetAgent(bundled, "reviewer")
	if !ok {
		t.Fatalf("bundled reviewer agent missing")
	}
	taskAgent, ok := GetAgent(bundled, "task")
	if !ok {
		t.Fatalf("bundled task agent missing")
	}

	// 1. Unit-check ResolveChildToolNames order against upstream task/executor.ts:4800-4830.
	// Reviewer frontmatter: read, find, grep, glob, bash, lsp, web_search, ast_grep; spawns: scout.
	// At depth 1 with host cap 1, `task` is stripped; `bash` triggers appended `wait`;
	// structured run appends `yield`.
	gotRevStruct, _ := ResolveChildToolNames(reviewer, 1, 2, true)
	wantRevStruct := []string{"read", "find", "grep", "glob", "bash", "lsp", "web_search", "ast_grep", "wait", "yield"}
	if !reflect.DeepEqual(gotRevStruct, wantRevStruct) {
		t.Fatalf("reviewer structured tools = %v, want %v", gotRevStruct, wantRevStruct)
	}
	gotRevUnstruct, _ := ResolveChildToolNames(reviewer, 1, 2, false)
	wantRevUnstruct := []string{"read", "find", "grep", "glob", "bash", "lsp", "web_search", "ast_grep", "wait"}
	if !reflect.DeepEqual(gotRevUnstruct, wantRevUnstruct) {
		t.Fatalf("reviewer unstructured tools = %v, want %v", gotRevUnstruct, wantRevUnstruct)
	}
	// When depth permits spawning (depth 0 < maxDepth 1), `task` is appended before `wait` and `yield`.
	gotRevSpawn, _ := ResolveChildToolNames(reviewer, 0, 1, true)
	wantRevSpawn := []string{"read", "find", "grep", "glob", "bash", "lsp", "web_search", "ast_grep", "task", "wait", "yield"}
	if !reflect.DeepEqual(gotRevSpawn, wantRevSpawn) {
		t.Fatalf("reviewer spawn-enabled tools = %v, want %v", gotRevSpawn, wantRevSpawn)
	}
	// `exec` expands to `eval`, `bash` at tail (after `task`, before `wait`), matching task/executor.ts:4812-4830.
	execAgent := AgentDefinition{
		Name:   "custom-exec",
		Tools:  []string{"exec", "read", "grep"},
		Spawns: &SpawnsSpec{All: true},
	}
	gotExec, _ := ResolveChildToolNames(execAgent, 0, 1, false)
	wantExec := []string{"read", "grep", "task", "eval", "bash", "wait"}
	if !reflect.DeepEqual(gotExec, wantExec) {
		t.Fatalf("exec expansion order = %v, want %v", gotExec, wantExec)
	}
	// Agent without frontmatter `tools:` yields nil (default omp toolset).
	if gotTask, _ := ResolveChildToolNames(taskAgent, 1, 2, false); gotTask != nil {
		t.Fatalf("task agent without frontmatter tools = %v, want nil", gotTask)
	}

	// 2. End-to-end RunSubagent using defaultRuntime.Registry so ExplicitTools(ctx)
	// reads the real child session metadata recorded before BindExtensions.
	ext := sdk.New("test-explicit-tools")
	Register(ext)

	const parentSessID = "parent-explicit-tools"
	var capturedChildSessIDs []string
	var capturedChildOrders [][]string
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: parentSessID,
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			childSessID := "sess-" + child.id
			capturedChildSessIDs = append(capturedChildSessIDs, childSessID)
			capturedChildOrders = append(capturedChildOrders, defaultRuntime.Registry.ChildExplicitTools(childSessID))
			h.finishChildWithText(child, "done")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})
	parentCtx := defaultRuntime.sessionContextFor(parentSessID, sdk.Context{})

	// Top-level session has no explicit tool list -> ExplicitTools returns nil.
	if got := ExplicitTools(parentCtx); got != nil {
		t.Fatalf("ExplicitTools(parentCtx) = %v, want nil", got)
	}

	// Spawn reviewer (structured: reviewer has frontmatter `output:` schema) with KeepAlive
	// so we can also probe ExplicitTools(childCtx) before lifecycle.End tears it down.
	_ = RunSubagent(context.Background(), defaultRuntime.Registry, RunRequest{
		SDK:             parentCtx,
		ParentSessionID: parentSessID,
		ParentAgentID:   MainAgentID,
		Cwd:             h.opts.Cwd,
		ID:              "RevExplicit",
		Agent:           reviewer,
		Assignment:      "review",
		OutputSchema:    reviewer.Output,
		MaxDepth:        2,
		KeepAlive:       true,
	})
	// Spawn task (no frontmatter `tools:`).
	_ = RunSubagent(context.Background(), defaultRuntime.Registry, RunRequest{
		SDK:             parentCtx,
		ParentSessionID: parentSessID,
		ParentAgentID:   MainAgentID,
		Cwd:             h.opts.Cwd,
		ID:              "TaskDefault",
		Agent:           taskAgent,
		Assignment:      "work",
		MaxDepth:        2,
		KeepAlive:       true,
	})

	if len(capturedChildSessIDs) != 2 {
		t.Fatalf("expected 2 child sessions, got %d", len(capturedChildSessIDs))
	}
	revSessID := capturedChildSessIDs[0]
	taskSessID := capturedChildSessIDs[1]

	if !reflect.DeepEqual(capturedChildOrders[0], wantRevStruct) {
		t.Fatalf("reviewer ChildExplicitTools during run = %v, want %v", capturedChildOrders[0], wantRevStruct)
	}
	if capturedChildOrders[1] != nil {
		t.Fatalf("task ChildExplicitTools during run = %v, want nil", capturedChildOrders[1])
	}

	// Probe exported ExplicitTools(ctx) through a real sdk.Context bound to each child session ID.
	probeCtx := func(sessID string) sdk.Context {
		var gotCtx sdk.Context
		probeExt := sdk.New("probe-" + sessID)
		probeExt.RegisterTool(sdk.ToolDefinition{
			Name:       "probe",
			Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
			Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
				gotCtx = ctx
				return "ok", nil
			},
		})
		ph := startFakeHost(t, probeExt, fakeHostOptions{SessionID: sessID})
		if _, errMsg := ph.callTool("probe", map[string]any{}); errMsg != "" {
			t.Fatalf("probe %s failed: %s", sessID, errMsg)
		}
		return gotCtx
	}

	revCtx := probeCtx(revSessID)
	gotViaCtx := ExplicitTools(revCtx)
	if !reflect.DeepEqual(gotViaCtx, wantRevStruct) {
		t.Fatalf("ExplicitTools(revCtx) = %v, want %v", gotViaCtx, wantRevStruct)
	}
	// Mutating the returned slice must not corrupt stored metadata.
	gotViaCtx[0] = "mutated"
	if again := ExplicitTools(revCtx); !reflect.DeepEqual(again, wantRevStruct) {
		t.Fatalf("ExplicitTools returned aliased slice: got %v, want %v", again, wantRevStruct)
	}

	taskCtx := probeCtx(taskSessID)
	if got := ExplicitTools(taskCtx); got != nil {
		t.Fatalf("ExplicitTools(taskCtx) = %v, want nil", got)
	}

	// Releasing the child session through internal/lifecycle clears ExplicitTools.
	lifecycle.End(revSessID)
	if got := ExplicitTools(revCtx); got != nil {
		t.Fatalf("after lifecycle.End(revSessID), ExplicitTools(revCtx) = %v, want nil", got)
	}
	lifecycle.End(taskSessID)
}

// Verify lenient `task` PrepareArguments:
//   - Schema-invalid inputs return omp's exact `validateShapeParams`/`validateSpawnParams`
//     refusal wording instead of letting PiG's host validator refuse in PiG wording
//     or coerce `context: null` -> `"null"`.
//   - Schema-valid inputs apply omp's arktype default `tasks[].agent: "task"` (and
//     flat `agent: "task"` when batch is disabled).
//   - Raw fallback inputs that pass `ValidateTaskParams` are returned untouched.
//
// upstream: packages/coding-agent/src/task/index.ts:222-336,748, task/types.ts:67,144-239, packages/agent/src/tool-arguments.ts:29-42
func TestTaskLenientPrepareArguments(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	def := intent.Tool(schemaorder.Apply(taskToolDef(rt, DefaultConfig(), LoadBundledAgents(), "*")))

	// 1. Lenient raw fallback refusals carry omp's exact wording.
	refusalCases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "empty object",
			args: map[string]any{},
			want: "Missing `tasks`. Provide a `tasks` array (one subagent per item) with a shared `context`.",
		},
		{
			name: "missing context",
			args: map[string]any{"tasks": []any{map[string]any{"task": "abc", "solutionSpace": "abc"}}},
			want: "Missing `context`. Provide the shared background for this batch — goal, constraints, and any contract the tasks share.",
		},
		{
			name: "null context (must not coerce to 'null')",
			args: map[string]any{"context": nil, "tasks": []any{map[string]any{"task": "abc", "solutionSpace": "abc"}}},
			want: "Missing `context`. Provide the shared background for this batch — goal, constraints, and any contract the tasks share.",
		},
		{
			name: "non-array tasks",
			args: map[string]any{"context": "abc", "tasks": "not-an-array"},
			want: "Missing `tasks`. Provide at least one task item with `task`.",
		},
		{
			name: "non-object task item",
			args: map[string]any{"context": "abc", "tasks": []any{"oops"}},
			want: "Task 1 is missing `task`. Every task needs complete, self-contained instructions.",
		},
		{
			name: "missing task in named item",
			args: map[string]any{"context": "abc", "tasks": []any{map[string]any{"name": "Alpha", "solutionSpace": "abc"}}},
			want: "Task 1 (`Alpha`) is missing `task`. Every task needs complete, self-contained instructions.",
		},
		{
			name: "top-level agent in batch",
			args: map[string]any{"context": "abc", "agent": "scout", "tasks": []any{map[string]any{"task": "abc", "solutionSpace": "abc"}}},
			want: "Top-level `agent` is not part of the batch shape. Set `tasks[].agent` instead.",
		},
		{
			name: "stale schema field",
			args: map[string]any{"context": "abc", "schema": map[string]any{}, "tasks": []any{map[string]any{"task": "abc", "solutionSpace": "abc"}}},
			want: "The task tool uses `outputSchema`; rename the stale `schema` field.",
		},
	}
	for _, tc := range refusalCases {
		_, err := def.PrepareArguments(tc.args)
		if err == nil {
			t.Fatalf("%s: expected error %q, got nil", tc.name, tc.want)
		}
		if err.Error() != tc.want {
			t.Fatalf("%s: error = %q, want %q", tc.name, err.Error(), tc.want)
		}
	}

	// 2. Schema-valid batch call fills default `tasks[].agent: "task"`, while preserving explicit agent.
	got, err := def.PrepareArguments(map[string]any{
		"context": "shared",
		"tasks": []any{
			map[string]any{"task": "work1", "solutionSpace": "one fix"},
			map[string]any{"agent": "scout", "task": "work2", "solutionSpace": "one fix"},
		},
	})
	if err != nil {
		t.Fatalf("valid batch PrepareArguments error: %v", err)
	}
	tasks := got["tasks"].([]any)
	if tasks[0].(map[string]any)["agent"] != "task" {
		t.Fatalf("tasks[0].agent = %v, want %q", tasks[0].(map[string]any)["agent"], "task")
	}
	if tasks[1].(map[string]any)["agent"] != "scout" {
		t.Fatalf("tasks[1].agent = %v, want %q", tasks[1].(map[string]any)["agent"], "scout")
	}

	// 3. Raw fallback that passes ValidateTaskParams (missing solutionSpace) is NOT mutated by prepareTaskArguments.
	rawMissingSS := map[string]any{
		"context": "abc",
		"tasks":   []any{map[string]any{"task": "abc", "name": "Alpha"}},
	}
	gotRaw, err := prepareTaskArguments(DefaultConfig(), DefaultSpawnAgent, rawMissingSS)
	if err != nil {
		t.Fatalf("unexpected error on raw missing-solutionSpace fallback: %v", err)
	}
	if _, hasAgent := gotRaw["tasks"].([]any)[0].(map[string]any)["agent"]; hasAgent {
		t.Fatalf("raw fallback must not add default agent, got %+v", gotRaw)
	}
}

// Verify Round 4 Oracle Triage 2 findings for `internal/agents`:
//   - Finding 10: `yield` `data` parameter built from caller's schema (`anyOf`
//     section variants, strict sanitization, caller keyword order in hint).
//   - Finding 16: `task` initial details have non-nil `[]` for `recentTools`/
//     `recentOutput`, deterministic `status: "pending"`, and no extra `modelOverride`.
//   - Finding 17: temp task artifacts dir matches `omp-task-<16 hex>`.
//   - Finding 18: structured child prompt has no blank line between override
//     paragraph and `Your terminal yield MUST…`, renders `data: unknown;` for
//     JSON Schema with `type`, and pre-registers all batch siblings so `One`'s
//     `# Peers` deterministically lists `Two`.
//   - Wait details: `meta.source` report on `buildWaitResult`, `"jobs": []`
//     preserved on empty wait, and `agentOutputPublished` omitted on initial
//     task spawns (matching omp `task/index.ts:1663`).
func TestRound4_YieldSchemaTaskDetailsPromptAndWaitDetails(t *testing.T) {
	resetAllRegistries(t)
	ResetForTests()
	t.Cleanup(ResetForTests)

	// Finding 10: yield data schema from caller's JSON Schema (oracle task-structured-yield).
	callerSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer":  map[string]any{"type": "number"},
			"summary": map[string]any{"type": "string"},
		},
		"required": []string{"answer", "summary"},
	}
	yieldDef := BuildYieldToolDefinition(callerSchema, nil, nil)
	rawParams, err := json.Marshal(yieldDef.Parameters)
	if err != nil {
		t.Fatalf("marshal yield params: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rawParams, &decoded); err != nil {
		t.Fatalf("unmarshal yield params: %v", err)
	}
	dataSchema := decoded["properties"].(map[string]any)["data"].(map[string]any)
	desc, _ := dataSchema["description"].(string)
	wantDesc := "Structured output matching the schema:\n{\n  \"type\": \"object\",\n  \"properties\": {\n    \"answer\": {\n      \"type\": \"number\"\n    },\n    \"summary\": {\n      \"type\": \"string\"\n    }\n  },\n  \"required\": [\n    \"answer\",\n    \"summary\"\n  ]\n}"
	if desc != wantDesc {
		t.Fatalf("yield data description mismatch:\ngot:\n%s\nwant:\n%s", desc, wantDesc)
	}
	anyOf, ok := dataSchema["anyOf"].([]any)
	if !ok || len(anyOf) != 3 {
		t.Fatalf("yield data.anyOf = %#v, want 3 branches [fullObject, number, string]", dataSchema["anyOf"])
	}
	if anyOf[0].(map[string]any)["type"] != "object" || anyOf[1].(map[string]any)["type"] != "number" || anyOf[2].(map[string]any)["type"] != "string" {
		t.Fatalf("unexpected yield data.anyOf branches: %#v", anyOf)
	}

	// Finding 17: temp artifacts dir is named omp-task-<16 hex>.
	rt := NewRuntime(DefaultConfig())
	dir, isTemp := rt.sessionArtifactsDir(sdk.Context{})
	if !isTemp || dir == "" {
		t.Fatalf("expected temp artifacts dir, got (%q, %v)", dir, isTemp)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	base := filepath.Base(dir)
	if !regexp.MustCompile(`^omp-task-[0-9a-f]{16}$`).MatchString(base) {
		t.Fatalf("temp artifacts dir base = %q, want omp-task-<16 hex>", base)
	}

	// Finding 18 (1) & (2): structured child prompt spacing & `data: unknown;` for JSON Schema with `type`.
	structPrompt := RenderSubagentSystemPrompt(RunRequest{
		ID:                         "StructChild",
		Agent:                      AgentDefinition{Name: "task", SystemPrompt: "Work."},
		OutputSchema:               callerSchema,
		OutputSchemaOverridesAgent: true,
		IRCEnabled:                 false,
	}, PeerRoster{}, true)
	if strings.Contains(structPrompt, "full `data` object.\n\nYour terminal `yield` MUST") {
		t.Fatalf("extra blank line before Your terminal yield MUST:\n%s", structPrompt)
	}
	if !strings.Contains(structPrompt, "full `data` object.\nYour terminal `yield` MUST") {
		t.Fatalf("override paragraph and Your terminal yield MUST must be joined without blank line:\n%s", structPrompt)
	}
	if !strings.Contains(structPrompt, "```ts\n{\n  data: unknown;\n}\n```") {
		t.Fatalf("expected `data: unknown;` for JSON Schema with `type` (jtd-utils.ts:67-69), got:\n%s", structPrompt)
	}

	// Finding 16, Finding 18 (3), and Wait details end-to-end with a 2-child batch.
	ext := sdk.New("test-round4-batch")
	RegisterWithRuntime(ext, rt)
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: "round4-batch-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.untilWaitWatches()
			h.finishChildWithText(child, "ok")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})

	// Empty wait preserves `"jobs": []` in JSON details.
	emptyWaitRes, errMsg := h.callTool("wait", map[string]any{})
	if errMsg != "" {
		t.Fatalf("empty wait failed: %s", errMsg)
	}
	emptyWaitJSON, _ := json.Marshal(emptyWaitRes["details"])
	if !strings.Contains(string(emptyWaitJSON), `"jobs":[]`) {
		t.Fatalf("empty wait details omitted jobs:[]: %s", emptyWaitJSON)
	}

	// Spawn a 2-child batch (One, Two).
	spawnRes, errMsg := h.callTool("task", map[string]any{
		"context": "batch test",
		"tasks": []any{
			map[string]any{"name": "One", "task": "reply", "solutionSpace": "one fix"},
			map[string]any{"name": "Two", "task": "reply", "solutionSpace": "one fix"},
		},
	})
	if errMsg != "" {
		t.Fatalf("batch task failed: %s", errMsg)
	}
	detJSON, _ := json.Marshal(spawnRes["details"])
	var detMap map[string]any
	_ = json.Unmarshal(detJSON, &detMap)
	progs := detMap["progress"].([]any)
	if len(progs) != 2 {
		t.Fatalf("expected 2 progress entries, got %d", len(progs))
	}
	for i, raw := range progs {
		p := raw.(map[string]any)
		if p["status"] != "pending" {
			t.Fatalf("progress[%d].status = %v, want pending", i, p["status"])
		}
		if rtVal, ok := p["recentTools"].([]any); !ok || rtVal == nil {
			t.Fatalf("progress[%d].recentTools = %#v, want []", i, p["recentTools"])
		}
		if roVal, ok := p["recentOutput"].([]any); !ok || roVal == nil {
			t.Fatalf("progress[%d].recentOutput = %#v, want []", i, p["recentOutput"])
		}
		if _, hasMO := p["modelOverride"]; hasMO {
			t.Fatalf("progress[%d] should not emit extra modelOverride: %+v", i, p)
		}
	}

	// Wait for both children to finish and check wait details (`meta.source`, no `agentOutputPublished`).
	waitRes, errMsg := h.callTool("wait", map[string]any{})
	if errMsg != "" {
		t.Fatalf("wait failed: %s", errMsg)
	}
	waitDetJSON, _ := json.Marshal(waitRes["details"])
	var waitDet map[string]any
	_ = json.Unmarshal(waitDetJSON, &waitDet)
	meta, _ := waitDet["meta"].(map[string]any)
	src, _ := meta["source"].(map[string]any)
	if src["type"] != "report" || src["value"] != "background jobs snapshot" {
		t.Fatalf("wait details.meta = %#v, want report background jobs snapshot", meta)
	}
	for _, raw := range waitDet["jobs"].([]any) {
		j := raw.(map[string]any)
		if _, hasPub := j["agentOutputPublished"]; hasPub {
			t.Fatalf("initial task wait job should omit agentOutputPublished (omp task/index.ts:1663), got %+v", j)
		}
	}

	// Verify One's `# Peers` lists Two and Two's `# Peers` lists One.
	var sawOneListsTwo, sawTwoListsOne bool
	for _, ch := range h.getChildren() {
		ch.mu.Lock()
		args := ch.createArgs
		ch.mu.Unlock()
		resOpts, _ := args["resources"].(map[string]any)
		appends, _ := resOpts["appendSystemPrompt"].([]any)
		if len(appends) == 0 {
			continue
		}
		sys, _ := appends[0].(string)
		if strings.Contains(sys, "Your id is `One`.") && strings.Contains(sys, "- `Two` — task (sub, running)") {
			sawOneListsTwo = true
		}
		if strings.Contains(sys, "Your id is `Two`.") && strings.Contains(sys, "- `One` — task (sub, running)") {
			sawTwoListsOne = true
		}
	}
	if !sawOneListsTwo || !sawTwoListsOne {
		t.Fatalf("batch sibling roster incomplete: oneListsTwo=%v twoListsOne=%v", sawOneListsTwo, sawTwoListsOne)
	}
}

// Verify that a settled background AgentJob's Details()["progress"][0] reflects
// the final SingleResult (requests, tokens, cost, durationMs, contextTokens,
// contextWindow, resolvedModel*, retryFailure, recentOutput, cleared live
// currentTool/retryState/streaming) even when no OnProgress callback fired after
// the last turn, for completed, failed, and cancelled children.
// upstream: packages/coding-agent/src/task/index.ts:1628-1672, packages/tui/src/tools/task.ts:1900-1973
func TestAgentJobSettledDetailsMatchFinalResultWithoutLastProgressCallback(t *testing.T) {
	// 1. Completed child: last OnProgress had stale in-flight turn-1 stats + live tool + retryState;
	// final turn fired no OnProgress before finish().
	_, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	jCompleted := NewAgentJob("CompChild", "CompChild", cancel1, AgentProgress{
		ID:     "CompChild",
		Agent:  "task",
		Status: "pending",
	})
	jCompleted.updateProgress(AgentProgress{
		ID:                 "CompChild",
		Agent:              "task",
		Status:             "running",
		Requests:           1,
		Tokens:             100,
		Cost:               0.01,
		DurationMs:         50,
		CurrentTool:        "bash",
		CurrentToolArgs:    "echo hi",
		CurrentToolStartMs: 12345,
		Streaming:          &AgentStreaming{Kind: "text"},
		RetryState:         &AgentRetryState{Attempt: 1, MaxAttempts: 3, DelayMs: 500, ErrorMessage: "transient"},
		RecentOutput:       []string{"stale partial line"},
	}, "Running agent CompChild...")
	finalCompleted := SingleResult{
		ID:                    "CompChild",
		Agent:                 "task",
		ExitCode:              0,
		Output:                "line one\nfinal answer",
		DurationMs:            420,
		Tokens:                2500,
		Requests:              3,
		ContextTokens:         1900,
		ContextWindow:         200000,
		Usage:                 &Usage{Cost: UsageCost{Total: 0.125}},
		ResolvedModel:         "openai/gpt-5:high",
		ResolvedModelIdentity: "openai/gpt-5",
		ResolvedThinkingLevel: "high",
		LastIntent:            "Verifying patch",
	}
	jCompleted.finish("completed", "done", "", &finalCompleted, nil, false, nil)

	det := jCompleted.Details()
	progs := det["progress"].([]AgentProgress)
	if len(progs) != 1 {
		t.Fatalf("expected 1 progress entry, got %d", len(progs))
	}
	p := progs[0]
	if p.Status != "completed" || p.Requests != 3 || p.Tokens != 2500 || p.Cost != 0.125 || p.DurationMs != 420 {
		t.Fatalf("completed Details() kept stale figures: %+v", p)
	}
	if p.ContextTokens != 1900 || p.ContextWindow != 200000 {
		t.Fatalf("completed context stats = %d/%d, want 1900/200000", p.ContextTokens, p.ContextWindow)
	}
	if p.ResolvedModel != "openai/gpt-5:high" || p.ResolvedModelIdentity != "openai/gpt-5" || p.ResolvedThinkingLevel != "high" {
		t.Fatalf("completed resolvedModel mismatch: %+v", p)
	}
	if p.CurrentTool != "" || p.CurrentToolArgs != "" || p.CurrentToolStartMs != 0 || p.RetryState != nil || p.Streaming != nil {
		t.Fatalf("completed Details() leaked live in-flight fields: %+v", p)
	}
	if !reflect.DeepEqual(p.RecentOutput, []string{"line one", "final answer"}) {
		t.Fatalf("completed RecentOutput = %#v, want final output lines", p.RecentOutput)
	}

	// 2. Failed child (with RetryFailure and no ResolvedModel): clears stale model & records RetryFailure.
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	jFailed := NewAgentJob("FailChild", "FailChild", cancel2, AgentProgress{
		ID:            "FailChild",
		Agent:         "task",
		Status:        "running",
		Requests:      1,
		Tokens:        50,
		Cost:          0.005,
		DurationMs:    10,
		ResolvedModel: "stale/model",
		CurrentTool:   "read",
	})
	finalFailed := SingleResult{
		ID:           "FailChild",
		Agent:        "task",
		ExitCode:     1,
		Error:        "boom",
		Output:       "partial before crash",
		DurationMs:   310,
		Tokens:       900,
		Requests:     2,
		Usage:        &Usage{Cost: UsageCost{Total: 0.04}},
		RetryFailure: &AgentRetryFailure{Attempt: 3, ErrorMessage: "rate limit"},
	}
	jFailed.finish("failed", "boom", "boom", &finalFailed, nil, false, nil)
	pf := jFailed.Details()["progress"].([]AgentProgress)[0]
	if pf.Status != "failed" || pf.Requests != 2 || pf.Tokens != 900 || pf.Cost != 0.04 || pf.DurationMs != 310 {
		t.Fatalf("failed Details() kept stale figures: %+v", pf)
	}
	if pf.ResolvedModel != "" || pf.CurrentTool != "" || pf.RetryFailure == nil || pf.RetryFailure.Attempt != 3 {
		t.Fatalf("failed Details() model/retryFailure mismatch: %+v", pf)
	}

	// 3. Cancelled child (both with SingleResult and nil SingleResult e.g. cancelled before start).
	_, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	jCancelled := NewAgentJob("CancelChild", "CancelChild", cancel3, AgentProgress{
		ID:          "CancelChild",
		Agent:       "task",
		Status:      "running",
		CurrentTool: "bash",
	})
	finalCancelled := SingleResult{
		ID:          "CancelChild",
		Agent:       "task",
		ExitCode:    1,
		Aborted:     true,
		AbortReason: "Cancelled by caller",
		DurationMs:  180,
		Tokens:      400,
		Requests:    2,
		Usage:       &Usage{Cost: UsageCost{Total: 0.02}},
	}
	jCancelled.finish("cancelled", "cancelled", "", &finalCancelled, nil, false, nil)
	pc := jCancelled.Details()["progress"].([]AgentProgress)[0]
	if pc.Status != "aborted" || pc.Requests != 2 || pc.Tokens != 400 || pc.Cost != 0.02 || pc.DurationMs != 180 || pc.CurrentTool != "" {
		t.Fatalf("cancelled Details() mismatch: %+v", pc)
	}

	_, cancel4 := context.WithCancel(context.Background())
	defer cancel4()
	jNilSR := NewAgentJob("AbortBeforeStart", "AbortBeforeStart", cancel4, AgentProgress{
		ID:          "AbortBeforeStart",
		Agent:       "task",
		Status:      "pending",
		CurrentTool: "bash",
	})
	jNilSR.finish("cancelled", "Aborted before execution", "Aborted before execution", nil, nil, false, nil)
	pn := jNilSR.Details()["progress"].([]AgentProgress)[0]
	if pn.Status != "aborted" || pn.CurrentTool != "" || pn.RecentTools == nil || pn.RecentOutput == nil {
		t.Fatalf("cancelled nil-sr Details() mismatch: %+v", pn)
	}
}

// TestRound5_WaitIncludesJobIntentLine verifies that `wait` copies
// `JobDetailer.Intent()` onto `JobSnapshot.Intent` and renders `Intent: <text>`
// between the job heading and `Label:` (matching omp `async/job-control.ts:421`
// and oracle case `intent-tracing-on` #3).
func TestRound5_WaitIncludesJobIntentLine(t *testing.T) {
	resetAllRegistries(t)
	sessID := "sess-wait-intent"
	done := make(chan struct{})
	close(done)
	j := &intentStubJob{
		id:      "bg_1",
		kind:    jobs.KindBash,
		label:   "sleep 0.3; echo bg_intent",
		intent:  "Running background job",
		status:  "completed",
		result:  "bg_intent\n",
		started: time.Now().Add(-300 * time.Millisecond),
		ended:   time.Now(),
		done:    done,
	}
	jobs.Add(sessID, j)
	rt := NewRuntime(DefaultConfig())
	res := rt.buildWaitResult(context.Background(), sessID, []jobs.Job{j}, map[string]bool{}, time.Now(), map[string]bool{"bg_1": true})
	wantBlock := "### bg_1 [bash] — completed\nIntent: Running background job\nLabel: sleep 0.3; echo bg_intent"
	if !strings.Contains(res.Content, wantBlock) {
		t.Fatalf("wait output missing Intent line:\ngot:\n%s\nwant substring:\n%s", res.Content, wantBlock)
	}
	det, ok := res.Details.(CoordinationDetails)
	if !ok || len(det.Jobs) != 1 || det.Jobs[0].Intent != "Running background job" {
		t.Fatalf("wait details missing JobSnapshot.Intent: %#v", res.Details)
	}
}

type intentStubJob struct {
	id      string
	kind    jobs.Kind
	label   string
	intent  string
	status  string
	result  string
	started time.Time
	ended   time.Time
	done    chan struct{}
}

func (j *intentStubJob) ID() string              { return j.id }
func (j *intentStubJob) Kind() jobs.Kind         { return j.kind }
func (j *intentStubJob) Label() string           { return j.label }
func (j *intentStubJob) Status() string          { return j.status }
func (j *intentStubJob) Started() time.Time      { return j.started }
func (j *intentStubJob) Output() string          { return j.result }
func (j *intentStubJob) Result() string          { return j.result }
func (j *intentStubJob) Done() <-chan struct{}   { return j.done }
func (j *intentStubJob) Cancel()                 {}
func (j *intentStubJob) Ended() time.Time        { return j.ended }
func (j *intentStubJob) Command() string         { return j.label }
func (j *intentStubJob) Intent() string          { return j.intent }
func (j *intentStubJob) ErrorText() string       { return "" }
func (j *intentStubJob) Details() map[string]any { return nil }
func (j *intentStubJob) SkipDelivery() bool      { return true }

// Verify that ResolveChildThinkingLevel maps "auto" to omp's provisional
// fallback ("high", matching packages/tui/src/thinking.ts:421-425) rather than
// "" (which falls back to PiG's session default "medium"), while keeping
// "inherit"/"" as "" and honouring explicit effort/suffix overrides.
// upstream: packages/tui/src/thinking.ts:421-425, session/model-controls.ts:186-193,712-718, task/executor.ts:5103-5107
func TestResolveChildThinkingLevelAutoFallback(t *testing.T) {
	cases := []struct {
		name       string
		agentLevel string
		effort     string
		suffix     string
		want       string
	}{
		{name: "bundled task auto fallback", agentLevel: "auto", want: "high"},
		{name: "explicit effort overrides auto", agentLevel: "auto", effort: "lo", want: "low"},
		{name: "model suffix overrides auto", agentLevel: "auto", suffix: "medium", want: "medium"},
		{name: "inherit stays session default", agentLevel: "inherit", want: ""},
		{name: "empty stays session default", agentLevel: "", want: ""},
		{name: "explicit agent level preserved", agentLevel: "medium", want: "medium"},
	}
	for _, tc := range cases {
		if got := ResolveChildThinkingLevel(tc.agentLevel, tc.effort, tc.suffix); got != tc.want {
			t.Errorf("%s: ResolveChildThinkingLevel(%q, %q, %q) = %q, want %q",
				tc.name, tc.agentLevel, tc.effort, tc.suffix, got, tc.want)
		}
	}
}

// Verify `task.maxEffort` caps subagent thinking effort after ResolveChildThinkingLevel
// across every effort level in omp's THINKING_EFFORTS ladder (`minimal`, `low`,
// `medium`, `high`, `xhigh`, `max`), preserves `off`/`""`, returns `off` on
// non-reasoning models, snaps past unsupported model levels, caps bundled `task`'s
// `auto` fallback (`high`), and is honoured end-to-end from `internal/settings.StoreFor`.
// upstream: packages/tui/src/thinking.ts:362-384, packages/coding-agent/src/task/executor.ts:5083-5093, task/settings.ts:650-663
func TestTaskMaxEffortCapAcrossAllLevelsAndSettings(t *testing.T) {
	resetAllRegistries(t)
	ResetForTests()
	t.Cleanup(ResetForTests)

	reasoningModel := map[string]any{"provider": "anthropic", "id": "claude-opus", "reasoning": true}
	nonReasoningModel := map[string]any{"provider": "openai", "id": "gpt-4o", "reasoning": false}
	sparseModel := map[string]any{
		"provider":  "test",
		"id":        "sparse",
		"reasoning": true,
		"thinkingLevelMap": map[string]any{
			"medium": nil, // medium unsupported; ceiling=medium must snap down to low
		},
	}

	// 1. Matrix over all 6 effort levels requested vs every ceiling level.
	levels := []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	for ci, ceil := range levels {
		for ri, req := range levels {
			got := ClampThinkingLevelToCeiling(reasoningModel, req, ceil)
			want := req
			if ri > ci {
				want = ceil
			}
			if got != want {
				t.Errorf("ClampThinkingLevelToCeiling(reasoning, req=%q, ceil=%q) = %q, want %q", req, ceil, got, want)
			}
		}
	}

	// 2. Special cases: `off`, empty/inherit, non-reasoning model, sparse model, and `auto` fallback capped.
	if got := ClampThinkingLevelToCeiling(reasoningModel, "off", "minimal"); got != "off" {
		t.Errorf("off under minimal cap = %q, want off", got)
	}
	if got := ClampThinkingLevelToCeiling(reasoningModel, "", "minimal"); got != "" {
		t.Errorf("empty under minimal cap = %q, want empty", got)
	}
	if got := ClampThinkingLevelToCeiling(nonReasoningModel, "high", "max"); got != "off" {
		t.Errorf("non-reasoning model with high = %q, want off", got)
	}
	if got := ClampThinkingLevelToCeiling(sparseModel, "high", "medium"); got != "low" {
		t.Errorf("sparse model (medium unsupported) high under medium cap = %q, want low", got)
	}
	// Child agent asking for more than cap (including bundled `task` `auto` -> `high`).
	if got := ClampThinkingLevelToCeiling(reasoningModel, ResolveChildThinkingLevel("auto", "", ""), "low"); got != "low" {
		t.Errorf("bundled task auto under low cap = %q, want low", got)
	}
	if got := ClampThinkingLevelToCeiling(reasoningModel, ResolveChildThinkingLevel("xhigh", "", ""), "medium"); got != "medium" {
		t.Errorf("agent xhigh under medium cap = %q, want medium", got)
	}

	// 3. End-to-end: setting `task.maxEffort: "low"` via internal/settings caps a
	// spawned bundled `task` child (frontmatter `thinkingLevel: "auto"` -> `"high"`)
	// to `"low"` in CreateAgentSessionOptions.ThinkingLevel.
	settings.ResetForTests()
	t.Cleanup(settings.ResetForTests)
	store, err := settings.NewIsolated(map[string]any{
		"task.maxEffort": "low",
		"async.enabled":  false,
	})
	if err != nil {
		t.Fatalf("settings.NewIsolated: %v", err)
	}
	settings.SetStoreForTests(func(_ sdk.Context) *settings.Store { return store })

	ext := sdk.New("test-max-effort")
	Register(ext)
	const sessID = "max-effort-sess"
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID: sessID,
		ActiveModel: map[string]any{
			"provider":  "proof",
			"id":        "proof-reasoning",
			"reasoning": true,
		},
		Models: []map[string]any{
			{"provider": "proof", "id": "proof-reasoning", "reasoning": true},
		},
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "done")
			return nil
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{})
	if _, errMsg := h.callTool("task", map[string]any{
		"context": "test cap",
		"tasks": []any{
			map[string]any{"name": "CappedChild", "task": "do work", "solutionSpace": "one fix"},
		},
	}); errMsg != "" {
		t.Fatalf("task call failed: %s", errMsg)
	}
	children := h.getChildren()
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	children[0].mu.Lock()
	gotThink, _ := children[0].createArgs["thinkingLevel"].(string)
	children[0].mu.Unlock()
	if gotThink != "low" {
		t.Fatalf("child thinkingLevel with task.maxEffort=low = %q, want %q", gotThink, "low")
	}
}

// Verify Review Final Code Finding 4: aborting a batch larger than the
// concurrency limit (8 children, maxConcurrency=2) settles every pre-registered
// sibling AgentRef across async, sync fan-out, and RunSubagent early-cancel
// paths so zero "running" subagent entries remain in the registry, peer roster,
// or agent:// listing.
func TestAbortedLargeBatchSettlesAllPreRegisteredRefs(t *testing.T) {
	resetAllRegistries(t)
	ResetForTests()
	t.Cleanup(ResetForTests)

	const (
		batchSize   = 8
		concurrency = 2
	)
	makeTasks := func(prefix string) []any {
		out := make([]any, batchSize)
		for i := range batchSize {
			out[i] = map[string]any{
				"name":          fmt.Sprintf("%s%d", prefix, i+1),
				"task":          "hold until aborted",
				"solutionSpace": "one fix",
			}
		}
		return out
	}

	// 1. Async batch of 8 with maxConcurrency=2: hold the 2 active slots,
	// cancel all 8 jobs (simulating proc://<id>/kill / abort while 6 children
	// are queued on rt.sem.Acquire), then assert zero "running" subagent refs.
	cfg := DefaultConfig()
	cfg.MaxConcurrency = concurrency
	rt := NewRuntime(cfg)
	ext := sdk.New("test-abort-batch-async")
	RegisterWithRuntime(ext, rt)

	startedAsync := make(chan struct{}, batchSize)
	const asyncSess = "abort-batch-async-sess"
	hAsync := startFakeHost(t, ext, fakeHostOptions{
		SessionID: asyncSess,
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			startedAsync <- struct{}{}
			<-child.abortCh
			h.finishChildAborted(child)
			return nil
		},
	})
	hAsync.fireEvent(sdk.EventSessionStart, map[string]any{})

	if _, errMsg := hAsync.callTool("task", map[string]any{
		"context": "abort large async batch",
		"tasks":   makeTasks("AsyncChild"),
	}); errMsg != "" {
		t.Fatalf("async batch spawn failed: %s", errMsg)
	}
	// Wait for the 2 concurrency slots to be occupied so the other 6 are queued on rt.sem.Acquire.
	for range concurrency {
		select {
		case <-startedAsync:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for active async children to start")
		}
	}
	// Cancel every job in the session (mirrors proc://<id>/kill on queued + active jobs).
	allJobs := jobs.List(asyncSess)
	if len(allJobs) != batchSize {
		t.Fatalf("expected %d jobs, got %d", batchSize, len(allJobs))
	}
	for _, j := range allJobs {
		j.Cancel()
	}
	for _, j := range allJobs {
		select {
		case <-j.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("job %s did not settle after cancel", j.ID())
		}
	}
	// Poll briefly for deferred SettleIfRunning (runs right after job.finish returns in goroutine).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, ref := range rt.Registry.ListInSession(asyncSess) {
			ref.mu.Lock()
			isSubRunning := ref.Kind == "sub" && ref.Status == "running"
			ref.mu.Unlock()
			if isSubRunning {
				running++
			}
		}
		if running == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, ref := range rt.Registry.ListInSession(asyncSess) {
		ref.mu.Lock()
		kind, st, id := ref.Kind, ref.Status, ref.ID
		ref.mu.Unlock()
		if kind == "sub" && st == "running" {
			t.Fatalf("async child %s still running in registry after batch abort", id)
		}
	}
	if roster := rt.Registry.CollectPeerRosterForSession(asyncSess, MainAgentID); len(roster.Peers) != 0 {
		t.Fatalf("expected no running peers after async batch abort, got %+v", roster.Peers)
	}

	// 2. Sync fan-out batch of 8 with maxConcurrency=2, aborted via tool cancellation.
	syncCfg := DefaultConfig()
	syncCfg.AsyncEnabled = false
	syncCfg.MaxConcurrency = concurrency
	rtSync := NewRuntime(syncCfg)
	extSync := sdk.New("test-abort-batch-sync")
	RegisterWithRuntime(extSync, rtSync)

	startedSync := make(chan struct{}, batchSize)
	const syncSess = "abort-batch-sync-sess"
	hSync := startFakeHost(t, extSync, fakeHostOptions{
		SessionID: syncSess,
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			startedSync <- struct{}{}
			<-child.abortCh
			h.finishChildAborted(child)
			return nil
		},
	})
	hSync.fireEvent(sdk.EventSessionStart, map[string]any{})

	cancelCh := make(chan struct{})
	doneSync := make(chan struct{})
	go func() {
		defer close(doneSync)
		_, _ = hSync.callToolAsync("task", map[string]any{
			"context": "abort large sync batch",
			"tasks":   makeTasks("SyncChild"),
		}, cancelCh)
	}()
	for range concurrency {
		select {
		case <-startedSync:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for active sync children to start")
		}
	}
	close(cancelCh)
	select {
	case <-doneSync:
	case <-time.After(5 * time.Second):
		t.Fatalf("sync fan-out did not settle after cancel")
	}
	for _, ref := range rtSync.Registry.ListInSession(syncSess) {
		ref.mu.Lock()
		kind, st, id := ref.Kind, ref.Status, ref.ID
		ref.mu.Unlock()
		if kind == "sub" && st == "running" {
			t.Fatalf("sync child %s still running in registry after batch abort", id)
		}
	}
	if roster := rtSync.Registry.CollectPeerRosterForSession(syncSess, MainAgentID); len(roster.Peers) != 0 {
		t.Fatalf("expected no running peers after sync batch abort, got %+v", roster.Peers)
	}

	// 3. Direct RunSubagent with pre-registered ref and already-cancelled ctx settles ref.
	regDirect := NewRegistry()
	regDirect.Register(&AgentRef{
		ID:        "PreCancelChild",
		Kind:      "sub",
		Status:    "running",
		SessionID: "direct-sess",
	})
	canceledCtx, cancelDirect := context.WithCancel(context.Background())
	cancelDirect()
	ag, _ := GetBundledAgent("task")
	_ = RunSubagent(canceledCtx, regDirect, RunRequest{
		ParentSessionID: "direct-sess",
		ID:              "PreCancelChild",
		Agent:           ag,
		Assignment:      "never starts",
	})
	if ref, ok := regDirect.GetInSession("direct-sess", "PreCancelChild"); !ok {
		t.Fatalf("expected PreCancelChild ref to exist")
	} else {
		ref.mu.Lock()
		st := ref.Status
		ref.mu.Unlock()
		if st == "running" {
			t.Fatalf("RunSubagent early ctx.Err() left PreCancelChild running")
		}
	}
}

// upstream: packages/coding-agent/src/task/widget-inputs.ts:25-29,
// packages/coding-agent/src/job-widgets/runtime.ts:640-688,
// packages/coding-agent/src/async/job-control.ts:188-243
func TestAgentJobFallbackWidgetStatBlockAndResolvedModelInDetails(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	j := NewAgentJob("StructChild", "StructChild", cancel, AgentProgress{
		ID:                    "StructChild",
		Agent:                 "task",
		Status:                "running",
		ResolvedModel:         "proof/proof-1",
		ResolvedModelIdentity: "proof/proof-1",
	})

	// Before any tool call: fallback widget has empty blocks and Details() exposes resolvedModel*.
	detRunning := j.Details()
	if detRunning["resolvedModel"] != "proof/proof-1" || detRunning["resolvedModelIdentity"] != "proof/proof-1" {
		t.Fatalf("running AgentJob.Details() missing resolvedModel*: %#v", detRunning)
	}
	wEmpty := j.WidgetView(time.Now())
	if wEmpty == nil || wEmpty.State != "fallback" || wEmpty.Title != "StructChild" || len(wEmpty.Blocks) != 0 {
		t.Fatalf("running AgentJob widget before tool calls = %#v, want empty blocks", wEmpty)
	}

	// Record one finished `yield` tool call (matches omp's `task-structured-yield#1` where yield returns 1 line).
	j.RecordToolCall("yield", "", "Result submitted.")
	j.finish("completed", "done", "", &SingleResult{
		ID:                    "StructChild",
		Agent:                 "task",
		ResolvedModel:         "proof/proof-1",
		ResolvedModelIdentity: "proof/proof-1",
	}, nil, false, nil)

	snap := j.snapshotWithResult(time.Now(), true)
	if snap.Widget == nil || snap.Widget.State != "fallback" || snap.Widget.Stage != "" {
		t.Fatalf("settled AgentJob snapshot widget = %#v", snap.Widget)
	}
	if len(snap.Widget.Blocks) != 1 || snap.Widget.Blocks[0].Kind != "stat" || len(snap.Widget.Blocks[0].Items) != 2 {
		t.Fatalf("settled AgentJob widget blocks = %#v, want 1 stat block with lines & lines/s", snap.Widget.Blocks)
	}
	if snap.Widget.Blocks[0].Items[0].Label != "lines" || snap.Widget.Blocks[0].Items[0].Text != "2" {
		t.Fatalf("settled AgentJob widget lines stat = %+v, want {lines 2}", snap.Widget.Blocks[0].Items[0])
	}
}

// upstream: packages/coding-agent/src/task/executor.ts:415-450 (collectIrcPeerRoster),
// packages/coding-agent/test/prompt-templates.test.ts: "production prompt includes live peers and omits parked identity and activity",
// packages/coding-agent/test/tools/irc-roster-activity.test.ts: "setActivity refreshes lastActivity so a working agent is not shown as stale",
// packages/coding-agent/test/advisor/advisor-visibility.test.ts: "excludes advisor refs from listVisibleTo"
func TestCollectPeerRosterOrderingAndScoping(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	at := func(ms int64) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }

	type refSpec struct {
		id         string
		display    string
		kind       string
		parentID   string
		status     string
		sessionID  string
		createdAt  time.Time
		lastActive time.Time
		activity   string
	}

	cases := []struct {
		name        string
		sessionID   string
		selfID      string
		refs        []refSpec
		wantIDs     []string
		wantParked  int
		wantOmitted int
	}{
		{
			// Oracle case `task-errors-and-agent-urls`: Main registered at session start,
			// then batch siblings One and Two registered later. From One's view, Two has
			// newer LastActive than Main so Two sorts before Main.
			name:      "batch_siblings_before_older_main",
			sessionID: "sess-a",
			selfID:    "One",
			refs: []refSpec{
				{id: MainAgentID, display: "main", kind: "main", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(100)},
				{id: "One", display: "task", kind: "sub", parentID: MainAgentID, status: "running", sessionID: "sess-a", createdAt: at(200), lastActive: at(200)},
				{id: "Two", display: "task", kind: "sub", parentID: MainAgentID, status: "running", sessionID: "sess-a", createdAt: at(201), lastActive: at(201)},
			},
			wantIDs: []string{"Two", MainAgentID},
		},
		{
			// 3+ siblings + parent + grandparent-less Main + different LastActive times:
			// status bucket (running before idle), then LastActive descending (NOT alphabetical,
			// NOT siblings-before-ancestors if an ancestor had newer activity).
			name:      "three_siblings_parent_and_main_sorted_by_status_then_last_activity_desc",
			sessionID: "sess-a",
			selfID:    "Child2",
			refs: []refSpec{
				{id: MainAgentID, display: "main", kind: "main", status: "running", sessionID: "sess-a", createdAt: at(10), lastActive: at(10)},
				{id: "ParentA", display: "task", kind: "sub", parentID: MainAgentID, status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(500)},
				{id: "Child1", display: "task", kind: "sub", parentID: "ParentA", status: "running", sessionID: "sess-a", createdAt: at(200), lastActive: at(300)},
				{id: "Child2", display: "task", kind: "sub", parentID: "ParentA", status: "running", sessionID: "sess-a", createdAt: at(201), lastActive: at(350)},
				{id: "Child3", display: "task", kind: "sub", parentID: "ParentA", status: "running", sessionID: "sess-a", createdAt: at(202), lastActive: at(700)},
				{id: "Child4", display: "task", kind: "sub", parentID: "ParentA", status: "running", sessionID: "sess-a", createdAt: at(203), lastActive: at(400)},
				{id: "IdleNew", display: "reviewer", kind: "sub", parentID: MainAgentID, status: "idle", sessionID: "sess-a", createdAt: at(50), lastActive: at(999)},
				{id: "IdleOld", display: "scout", kind: "sub", parentID: MainAgentID, status: "idle", sessionID: "sess-a", createdAt: at(60), lastActive: at(20)},
			},
			wantIDs: []string{"Child3", "ParentA", "Child4", "Child1", MainAgentID, "IdleNew", "IdleOld"},
		},
		{
			// Exact tie on both status and LastActive preserves JS Map insertion order
			// (NOT alphabetical ID order: "Zeta" inserted before "Alpha" stays before "Alpha").
			name:      "equal_last_activity_preserves_insertion_order",
			sessionID: "sess-a",
			selfID:    "Self",
			refs: []refSpec{
				{id: "Zeta", display: "task", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(500)},
				{id: "Alpha", display: "task", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(500)},
				{id: "Mid", display: "task", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(500)},
				{id: "Self", display: "task", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(500)},
			},
			wantIDs: []string{"Zeta", "Alpha", "Mid"},
		},
		{
			// Port of omp prompt-templates.test.ts:362-414 + advisor-visibility.test.ts:
			// running before idle, parked counted but omitted from peers, aborted and advisor omitted from both.
			name:      "omp_prompt_templates_live_idle_parked_aborted_advisor",
			sessionID: "sess-a",
			selfID:    "Child",
			refs: []refSpec{
				{id: MainAgentID, display: MainAgentID, kind: "main", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(100)},
				{id: "LiveWorker", display: "implementer", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(200), lastActive: at(200), activity: "editing auth.ts"},
				{id: "IdleReviewer", display: "reviewer", kind: "sub", status: "idle", sessionID: "sess-a", createdAt: at(300), lastActive: at(300)},
				{id: "ParkedSecretId", display: "secret parked label", kind: "sub", status: "parked", sessionID: "sess-a", createdAt: at(400), lastActive: at(400), activity: "reviewing classified.diff"},
				{id: "KilledChild", display: "task", kind: "sub", status: "aborted", sessionID: "sess-a", createdAt: at(450), lastActive: at(450)},
				{id: "AdvisorPeer", display: "advisor", kind: "advisor", status: "running", sessionID: "sess-a", createdAt: at(500), lastActive: at(500)},
				{id: "ParkedAdvisor", display: "advisor", kind: "advisor", status: "parked", sessionID: "sess-a", createdAt: at(550), lastActive: at(550)},
			},
			wantIDs:    []string{"LiveWorker", MainAgentID, "IdleReviewer"},
			wantParked: 1,
		},
		{
			// Peers of another session (both live and parked) must never appear or increment parkedCount.
			name:      "excludes_other_session_live_and_parked_peers",
			sessionID: "sess-a",
			selfID:    "WorkerA",
			refs: []refSpec{
				{id: MainAgentID, display: "main", kind: "main", status: "running", sessionID: "sess-a", createdAt: at(10), lastActive: at(10)},
				{id: MainAgentID, display: "main", kind: "main", status: "running", sessionID: "sess-b", createdAt: at(900), lastActive: at(900)},
				{id: "WorkerA", display: "task", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(100), lastActive: at(100)},
				{id: "SiblingA", display: "task", kind: "sub", status: "running", sessionID: "sess-a", createdAt: at(120), lastActive: at(120)},
				{id: "ParkedA", display: "task", kind: "sub", status: "parked", sessionID: "sess-a", createdAt: at(130), lastActive: at(130)},
				{id: "ForeignLive", display: "task", kind: "sub", status: "running", sessionID: "sess-b", createdAt: at(950), lastActive: at(950)},
				{id: "ForeignIdle", display: "task", kind: "sub", status: "idle", sessionID: "sess-b", createdAt: at(960), lastActive: at(960)},
				{id: "ForeignParked", display: "task", kind: "sub", status: "parked", sessionID: "sess-b", createdAt: at(970), lastActive: at(970)},
			},
			wantIDs:    []string{"SiblingA", MainAgentID},
			wantParked: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			for _, sp := range tc.refs {
				ref := &AgentRef{
					ID:          sp.id,
					DisplayName: sp.display,
					Kind:        sp.kind,
					ParentID:    sp.parentID,
					Status:      sp.status,
					SessionID:   sp.sessionID,
					CreatedAt:   sp.createdAt,
					LastActive:  sp.lastActive,
					Activity:    sp.activity,
				}
				if sp.kind == "main" {
					reg.RegisterMain(sp.sessionID, ref)
				} else {
					reg.Register(ref)
				}
			}
			roster := reg.CollectPeerRosterForSession(tc.sessionID, tc.selfID)
			gotIDs := make([]string, len(roster.Peers))
			for i, p := range roster.Peers {
				gotIDs[i] = p.ID
			}
			if !reflect.DeepEqual(gotIDs, tc.wantIDs) {
				t.Fatalf("peer IDs = %v, want %v", gotIDs, tc.wantIDs)
			}
			if roster.ParkedCount != tc.wantParked {
				t.Fatalf("ParkedCount = %d, want %d", roster.ParkedCount, tc.wantParked)
			}
			if roster.OmittedCount != tc.wantOmitted {
				t.Fatalf("OmittedCount = %d, want %d", roster.OmittedCount, tc.wantOmitted)
			}
		})
	}

	// Verify the 32-peer cap keeps the 32 newest-activity peers (running before idle)
	// and reports OmittedCount truthfully (`executor.ts:420-437`).
	t.Run("cap_keeps_newest_32_peers", func(t *testing.T) {
		reg := NewRegistry()
		reg.RegisterMain("sess-cap", &AgentRef{
			ID: MainAgentID, DisplayName: "main", Kind: "main", Status: "running",
			SessionID: "sess-cap", CreatedAt: at(1), LastActive: at(1),
		})
		for i := 1; i <= 35; i++ {
			reg.Register(&AgentRef{
				ID: fmt.Sprintf("W%02d", i), DisplayName: "task", Kind: "sub", Status: "running",
				SessionID: "sess-cap", CreatedAt: at(int64(10 + i)), LastActive: at(int64(10 + i)),
			})
		}
		roster := reg.CollectPeerRosterForSession("sess-cap", "Self")
		if len(roster.Peers) != defaultPeerRosterLimit || roster.OmittedCount != 4 {
			t.Fatalf("len=%d omitted=%d, want 32 and 4", len(roster.Peers), roster.OmittedCount)
		}
		if roster.Peers[0].ID != "W35" || roster.Peers[31].ID != "W04" {
			t.Fatalf("cap endpoints = [%s .. %s], want [W35 .. W04]", roster.Peers[0].ID, roster.Peers[31].ID)
		}
	})

	// Verify RegisterMain preserves Main's original LastActive on subsequent ensureMainRef refreshes
	// so newly spawned batch siblings sort ahead of Main, and SetActivity refreshes LastActive while
	// SetStatus to idle clears Activity (`irc-roster-activity.test.ts:19-46`).
	t.Run("register_main_preserves_last_active_and_activity_rules", func(t *testing.T) {
		reg := NewRegistry()
		mainStart := at(100)
		reg.RegisterMain("sess-m", &AgentRef{
			ID: MainAgentID, DisplayName: "main", Kind: "main", Status: "running",
			SessionID: "sess-m", CreatedAt: mainStart, LastActive: mainStart,
		})
		// Refreshing Main with zero LastActive (as ensureMainRef does on each `task` call) must NOT bump LastActive.
		reg.RegisterMain("sess-m", &AgentRef{
			ID: MainAgentID, DisplayName: "main", Kind: "main", Status: "running",
			SessionID: "sess-m",
		})
		mRef, ok := reg.LookupMain("sess-m")
		if !ok || !mRef.LastActive.Equal(mainStart) {
			t.Fatalf("RegisterMain refresh bumped LastActive: got %v, want %v", mRef.LastActive, mainStart)
		}
		reg.Register(&AgentRef{
			ID: "One", DisplayName: "task", Kind: "sub", Status: "running",
			SessionID: "sess-m", CreatedAt: at(200), LastActive: at(200),
		})
		reg.Register(&AgentRef{
			ID: "Two", DisplayName: "task", Kind: "sub", Status: "running",
			SessionID: "sess-m", CreatedAt: at(201), LastActive: at(201),
		})
		// One sees Two before Main; Two sees One before Main.
		if got := reg.CollectPeerRosterForSession("sess-m", "One").Peers; len(got) != 2 || got[0].ID != "Two" || got[1].ID != MainAgentID {
			t.Fatalf("One peer order = %+v, want [Two, Main]", got)
		}
		if got := reg.CollectPeerRosterForSession("sess-m", "Two").Peers; len(got) != 2 || got[0].ID != "One" || got[1].ID != MainAgentID {
			t.Fatalf("Two peer order = %+v, want [One, Main]", got)
		}
		// SetActivity updates running peer; SetStatus("idle") clears Activity; SetActivity on idle is ignored.
		reg.SetActivityInSession("sess-m", "Two", "editing\nauth.ts")
		if got := reg.CollectPeerRosterForSession("sess-m", "One").Peers[0].Activity; got != "editing auth.ts" {
			t.Fatalf("SetActivity = %q, want %q", got, "editing auth.ts")
		}
		reg.SetStatusInSession("sess-m", "Two", "idle")
		reg.SetActivityInSession("sess-m", "Two", "late heartbeat")
		peersAfterIdle := reg.CollectPeerRosterForSession("sess-m", "One").Peers
		if len(peersAfterIdle) != 2 || peersAfterIdle[0].ID != MainAgentID || peersAfterIdle[1].ID != "Two" || peersAfterIdle[1].Activity != "" {
			t.Fatalf("after Two idle, peers = %+v, want [Main(running), Two(idle, empty activity)]", peersAfterIdle)
		}
	})
}
