package vibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/modes"
)

func resetAllTestState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ResetForTests()
		jobs.SetDeliverySink(nil)
		jobs.ResetDeliveryForTests()
		jobs.ResetForTests()
		agents.ResetForTests()
		approval.ResetForTests()
	})
	ResetForTests()
	jobs.SetDeliverySink(nil)
	jobs.ResetDeliveryForTests()
	jobs.ResetForTests()
	agents.ResetForTests()
	approval.ResetForTests()
}

// ── 1. Lifecycle vocabulary tests (upstream: vibe/lifecycle.ts) ──────────────

func TestParseLifecycleEventAndPersistedVibeChildIDs(t *testing.T) {
	// Valid spawn, turn-started, turn-settled, tombstone, tombstone-revoked.
	validCases := []map[string]any{
		{
			"version":          1,
			"id":               "WorkerA",
			"ownerId":          "Main",
			"parentSessionId":  "sess-1",
			"action":           "spawn",
			"cli":              "fast",
			"agent":            "sonic",
			"childSessionFile": "WorkerA.jsonl",
			"createdAt":        1700000000000,
			"extraIgnored":     true,
		},
		{
			"version":         1,
			"id":              "WorkerA",
			"ownerId":         "Main",
			"parentSessionId": "sess-1",
			"action":          "turn-started",
			"turn":            1,
		},
		{
			"version":         1,
			"id":              "WorkerA",
			"ownerId":         "Main",
			"parentSessionId": "sess-1",
			"action":          "turn-settled",
			"turn":            2,
		},
		{
			"version":         1,
			"id":              "WorkerA",
			"ownerId":         "Main",
			"parentSessionId": "sess-1",
			"action":          "tombstone",
			"reason":          "explicit-kill",
		},
		{
			"version":         1,
			"id":              "WorkerA",
			"ownerId":         "Main",
			"parentSessionId": "sess-1",
			"action":          "tombstone-revoked",
			"reason":          "mode-exit",
		},
	}
	for i, tc := range validCases {
		ev, ok := ParseLifecycleEvent(tc)
		if !ok {
			t.Fatalf("case %d: expected valid lifecycle event, got invalid: %+v", i, tc)
		}
		if ev.ID != "WorkerA" || ev.Version != LifecycleVersion {
			t.Fatalf("case %d: unexpected parsed event: %+v", i, ev)
		}
	}

	// Invalid payloads must be rejected.
	invalidCases := []any{
		nil,
		"not-an-object",
		map[string]any{"version": 2, "id": "A", "ownerId": "M", "parentSessionId": "S", "action": "tombstone", "reason": "explicit-kill"},
		map[string]any{"version": 1, "id": "", "ownerId": "M", "parentSessionId": "S", "action": "tombstone", "reason": "explicit-kill"},
		map[string]any{"version": 1, "id": "A", "ownerId": "M", "parentSessionId": "S", "action": "spawn", "cli": "medium", "agent": "sonic", "childSessionFile": "A.jsonl", "createdAt": 1},
		map[string]any{"version": 1, "id": "A", "ownerId": "M", "parentSessionId": "S", "action": "turn-started", "turn": 0},
		map[string]any{"version": 1, "id": "A", "ownerId": "M", "parentSessionId": "S", "action": "turn-started", "turn": 1.5},
		map[string]any{"version": 1, "id": "A", "ownerId": "M", "parentSessionId": "S", "action": "tombstone", "reason": "bogus"},
		map[string]any{"version": 1, "id": "A", "ownerId": "M", "parentSessionId": "S", "action": "tombstone-revoked", "reason": "explicit-kill"},
	}
	for i, tc := range invalidCases {
		if _, ok := ParseLifecycleEvent(tc); ok {
			t.Fatalf("invalid case %d unexpectedly accepted: %+v", i, tc)
		}
	}

	// PersistedVibeChildIDs only accepts safe IDs whose childSessionFile matches `<id>.jsonl`.
	entries := []any{
		map[string]any{"type": "custom", "customType": LifecycleCustomType, "data": validCases[0]},
		map[string]any{
			"type":       "custom",
			"customType": LifecycleCustomType,
			"data": map[string]any{
				"version":          1,
				"id":               "../escape",
				"ownerId":          "Main",
				"parentSessionId":  "sess-1",
				"action":           "spawn",
				"cli":              "good",
				"agent":            "task",
				"childSessionFile": "../escape.jsonl",
				"createdAt":        1,
			},
		},
		map[string]any{
			"type":       "custom",
			"customType": LifecycleCustomType,
			"data": map[string]any{
				"version":          1,
				"id":               "Mismatch",
				"ownerId":          "Main",
				"parentSessionId":  "sess-1",
				"action":           "spawn",
				"cli":              "good",
				"agent":            "task",
				"childSessionFile": "Other.jsonl",
				"createdAt":        1,
			},
		},
		map[string]any{"type": "custom", "customType": "other", "data": validCases[0]},
	}
	ids := PersistedVibeChildIDs(entries)
	if len(ids) != 1 || !ids["WorkerA"] {
		t.Fatalf("PersistedVibeChildIDs = %v, want {WorkerA: true}", ids)
	}
}

// ── 2. Upstream test/vibe/wait.test.ts ───────────────────────────────────────

func TestVibeWaitCompletionClassification(t *testing.T) {
	resetAllTestState(t)

	const owner = "test-owner"
	const worker = "test-worker"
	const parentSID = "test-parent-session"

	newParent := func() *ParentSession {
		return &ParentSession{
			OwnerID:   owner,
			SessionID: parentSID,
		}
	}

	startTurn := func(t *testing.T, reg *SessionRegistry) (jobID string, complete func(string), job *VibeTurnJob) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		doneCh := make(chan string, 1)
		j := newVibeTurnJob("job-"+worker+"-1", worker, owner, "test vibe turn", cancel)
		jobs.Add(parentSID, j)
		go func() {
			select {
			case txt := <-doneCh:
				j.finish("completed", txt, "", nil)
			case <-ctx.Done():
				j.finish("cancelled", "", "cancelled", nil)
			}
		}()
		reg.RegisterRecordForTests(TestRecordInput{
			ID:      worker,
			OwnerID: owner,
			JobID:   j.ID(),
		})
		return j.ID(), func(txt string) { doneCh <- txt }, j
	}

	t.Run("reports a true timer expiry as timed out", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		_, _, j := startTurn(t, reg)
		defer j.Cancel()

		outcome, err := reg.Wait(context.Background(), newParent(), WaitParams{
			Timeout:   20 * time.Millisecond,
			HasCustom: true,
		})
		if err != nil {
			t.Fatalf("Wait error: %v", err)
		}
		if !outcome.TimedOut {
			t.Fatalf("expected TimedOut=true, got %+v", outcome)
		}
		if len(outcome.Settled) != 0 {
			t.Fatalf("expected 0 settled, got %+v", outcome.Settled)
		}
		if !slices.Equal(outcome.StillRunning, []string{worker}) {
			t.Fatalf("StillRunning = %v, want [%s]", outcome.StillRunning, worker)
		}
	})

	t.Run("returns a settled worker result instead of timing out", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		jobID, complete, _ := startTurn(t, reg)

		go func() {
			time.Sleep(15 * time.Millisecond)
			complete("worker result")
		}()

		outcome, err := reg.Wait(context.Background(), newParent(), WaitParams{
			Timeout:   2 * time.Second,
			HasCustom: true,
		})
		if err != nil {
			t.Fatalf("Wait error: %v", err)
		}
		if outcome.TimedOut {
			t.Fatalf("expected TimedOut=false")
		}
		if len(outcome.Settled) != 1 || outcome.Settled[0].ID != worker || outcome.Settled[0].JobID != jobID || outcome.Settled[0].Status != "completed" || outcome.Settled[0].ResultText != "worker result" {
			t.Fatalf("unexpected settled: %+v", outcome.Settled)
		}
		if !jobs.IsConsumed(parentSID, jobID) {
			t.Fatalf("expected job %s to be marked consumed", jobID)
		}
	})

	t.Run("does not render an abort as an elapsed wait window even with long timeout", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		_, _, j := startTurn(t, reg)
		defer j.Cancel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		res, err := ExecuteWait(ctx, reg, newParent(), map[string]any{"timeout": 900.0}, nil)
		if err != nil {
			t.Fatalf("ExecuteWait error: %v", err)
		}
		det := res.Details.(ToolDetails)
		if det.Wait.TimedOut {
			t.Fatalf("expected TimedOut=false on aborted wait")
		}
		if !strings.Contains(res.Content, "Still running") {
			t.Fatalf("expected 'Still running' in output, got: %q", res.Content)
		}
		if strings.Contains(res.Content, "Wait window elapsed") || strings.Contains(res.Content, "re-issue vibe_wait") {
			t.Fatalf("aborted wait should not claim timeout elapsed: %q", res.Content)
		}
	})

	t.Run("restores async self-delivery after an interrupted wait", func(t *testing.T) {
		resetAllTestState(t)
		jobs.EnsureDeliveryListener()
		var delMu sync.Mutex
		var deliveries []string
		deliveredCh := make(chan struct{}, 1)
		jobs.SetDeliverySink(func(sessionID, customType, content string, display bool, opts sdk.SendMessageOptions) error {
			delMu.Lock()
			deliveries = append(deliveries, content)
			delMu.Unlock()
			select {
			case deliveredCh <- struct{}{}:
			default:
			}
			return nil
		})

		reg := Global()
		_, complete, j := startTurn(t, reg)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		outcome, err := reg.Wait(ctx, newParent(), WaitParams{
			Timeout:   2 * time.Second,
			HasCustom: true,
		})
		if err != nil {
			t.Fatalf("Wait error: %v", err)
		}
		if outcome.TimedOut {
			t.Fatalf("expected TimedOut=false")
		}

		complete("delivered later")
		<-j.Done()

		select {
		case <-deliveredCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for async delivery after interrupted wait")
		}

		delMu.Lock()
		got := append([]string(nil), deliveries...)
		delMu.Unlock()
		if len(got) == 0 || !strings.Contains(got[0], "delivered later") {
			t.Fatalf("unexpected deliveries: %v", got)
		}
	})

	t.Run("returns a cancelled worker settlement without classifying it as timeout", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		jobID, _, j := startTurn(t, reg)

		go func() {
			time.Sleep(15 * time.Millisecond)
			j.Cancel()
		}()

		outcome, err := reg.Wait(context.Background(), newParent(), WaitParams{
			Timeout:   2 * time.Second,
			HasCustom: true,
		})
		if err != nil {
			t.Fatalf("Wait error: %v", err)
		}
		if outcome.TimedOut {
			t.Fatalf("expected TimedOut=false")
		}
		if len(outcome.Settled) != 1 || outcome.Settled[0].JobID != jobID || outcome.Settled[0].Status != "cancelled" {
			t.Fatalf("unexpected settled: %+v", outcome.Settled)
		}
	})
}

// ── 3. Upstream test/vibe/wall.test.ts ───────────────────────────────────────

func TestVibeWallHidesDirectorKilledSessions(t *testing.T) {
	resetAllTestState(t)
	const owner = "test-owner"
	const parentSID = "test-parent-session"
	parent := &ParentSession{OwnerID: owner, SessionID: parentSID}

	register := func(reg *SessionRegistry, id string, state SessionState, killed bool, jobID string) {
		reg.RegisterRecordForTests(TestRecordInput{
			ID:      id,
			OwnerID: owner,
			State:   state,
			Killed:  killed,
			JobID:   jobID,
		})
	}

	t.Run("vibe_list keeps live and self-terminated workers, collapsing killed ones to a trailing line", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		base := time.Now()
		seq := 0
		reg.now = func() time.Time { seq++; return base.Add(time.Duration(seq) * time.Millisecond) }

		register(reg, "Killed1", StateDead, true, "")
		register(reg, "Live", StateIdle, false, "")
		register(reg, "Crashed", StateDead, false, "")
		register(reg, "Killed2", StateDead, true, "")

		res, err := ExecuteList(reg, parent)
		if err != nil {
			t.Fatalf("ExecuteList error: %v", err)
		}
		det := res.Details.(ToolDetails)
		var screenIDs []string
		for _, sc := range det.Screens {
			screenIDs = append(screenIDs, sc.ID)
		}
		if !slices.Equal(screenIDs, []string{"Live", "Crashed"}) {
			t.Fatalf("screens = %v, want [Live Crashed]", screenIDs)
		}
		if !slices.Equal(det.HiddenKilled, []string{"Killed1", "Killed2"}) {
			t.Fatalf("hiddenKilled = %v, want [Killed1 Killed2]", det.HiddenKilled)
		}
		if !strings.Contains(res.Content, "- `Crashed` [fast] dead") {
			t.Fatalf("missing crashed row: %q", res.Content)
		}
		if strings.Contains(res.Content, "- `Killed1`") {
			t.Fatalf("killed row should be collapsed: %q", res.Content)
		}
		if !strings.Contains(res.Content, "Killed (2, transcripts at history://<id>): `Killed1`, `Killed2`") {
			t.Fatalf("missing killed summary line: %q", res.Content)
		}
	})

	t.Run("vibe_list with only killed sessions says so instead of offering a first spawn", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		register(reg, "Killed1", StateDead, true, "")

		res, err := ExecuteList(reg, parent)
		if err != nil {
			t.Fatalf("ExecuteList error: %v", err)
		}
		det := res.Details.(ToolDetails)
		if len(det.Screens) != 0 {
			t.Fatalf("expected 0 screens, got %+v", det.Screens)
		}
		if !strings.HasPrefix(res.Content, "No live vibe sessions.") {
			t.Fatalf("expected 'No live vibe sessions.' prefix, got %q", res.Content)
		}
	})

	t.Run("vibe_list names only the most recent killed sessions", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		base := time.Now()
		seq := 0
		reg.now = func() time.Time { seq++; return base.Add(time.Duration(seq) * time.Millisecond) }

		for i := 1; i <= 11; i++ {
			register(reg, fmt.Sprintf("K%d", i), StateDead, true, "")
		}
		res, err := ExecuteList(reg, parent)
		if err != nil {
			t.Fatalf("ExecuteList error: %v", err)
		}
		if !strings.Contains(res.Content, "Killed (11, transcripts at history://<id>): `K4`, ") {
			t.Fatalf("unexpected killed summary: %q", res.Content)
		}
		if !strings.HasSuffix(res.Content, "`K11`, +3 more") {
			t.Fatalf("expected suffix '`K11`, +3 more', got: %q", res.Content)
		}
		if strings.Contains(res.Content, "`K3`") {
			t.Fatalf("K3 should have been folded into +3 more: %q", res.Content)
		}
	})

	t.Run("vibe_wait shows a killed session the director names explicitly", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		register(reg, "Killed1", StateDead, true, "")

		res, err := ExecuteWait(context.Background(), reg, parent, map[string]any{
			"sessions": []any{"Killed1"},
			"timeout":  1.0,
		}, nil)
		if err != nil {
			t.Fatalf("ExecuteWait error: %v", err)
		}
		det := res.Details.(ToolDetails)
		if len(det.Screens) != 1 || det.Screens[0].ID != "Killed1" {
			t.Fatalf("screens = %+v, want [Killed1]", det.Screens)
		}
		if len(det.HiddenKilled) != 0 {
			t.Fatalf("expected no hiddenKilled for explicit named sessions, got %v", det.HiddenKilled)
		}
	})

	t.Run("vibe_wait keeps a card on the wall when its worker is killed mid-wait", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		reg.SetTeardownGraceForTesting(700 * time.Millisecond)

		release := make(chan struct{})
		j := newVibeTurnJob("job-worker-midwait", "Worker", owner, "worker turn", func() {})
		jobs.Add(parentSID, j)
		go func() {
			<-release
			j.finish("completed", "done", "", nil)
		}()
		register(reg, "Worker", StateRunning, false, j.ID())

		var mu sync.Mutex
		var frames []ToolDetails
		waitDone := make(chan sdk.ToolResult, 1)
		go func() {
			res, err := ExecuteWait(context.Background(), reg, parent, map[string]any{"timeout": 3.0}, func(update sdk.ToolResult) {
				if det, ok := update.Details.(ToolDetails); ok {
					mu.Lock()
					frames = append(frames, det)
					mu.Unlock()
				}
			})
			if err != nil {
				t.Errorf("ExecuteWait error: %v", err)
			}
			waitDone <- res
		}()

		time.Sleep(50 * time.Millisecond)
		if _, err := reg.Kill(parent, "Worker"); err != nil {
			t.Fatalf("Kill error: %v", err)
		}
		close(release)
		finalRes := <-waitDone

		mu.Lock()
		allFrames := append(append([]ToolDetails(nil), frames...), finalRes.Details.(ToolDetails))
		mu.Unlock()
		for i, f := range allFrames {
			if len(f.Screens) != 1 || f.Screens[0].ID != "Worker" {
				t.Fatalf("frame %d dropped Worker from wall: %+v", i, f.Screens)
			}
		}
	})
}

// ── 4. Upstream test/vibe/send-attribution.test.ts ───────────────────────────

func TestVibeSendAttributionAndFollowUp(t *testing.T) {
	resetAllTestState(t)
	const owner = "vibe-parent"
	const parentSID = "vibe-parent-session"

	t.Run("marks a busy worker steer as agent-authored", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		agentReg := agents.NewRegistry()
		release := make(chan struct{})
		started := make(chan struct{}, 1)

		var steerMu sync.Mutex
		var steerMsg, steerAttr string
		parent := &ParentSession{
			OwnerID:       owner,
			SessionID:     parentSID,
			AgentRegistry: agentReg,
			SubagentRunner: func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
				started <- struct{}{}
				<-release
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "done", DurationMs: 1}, nil
			},
			SteerWorker: func(ref *agents.AgentRef, message string, attribution string) (bool, error) {
				steerMu.Lock()
				steerMsg = message
				steerAttr = attribution
				steerMu.Unlock()
				return true, nil
			},
		}

		spawned, err := reg.Spawn(parent, SpawnParams{CLI: CLIFast, Prompt: "initial work"})
		if err != nil {
			t.Fatalf("Spawn error: %v", err)
		}
		<-started
		agentReg.Register(&agents.AgentRef{
			ID:        spawned.ID,
			Kind:      "sub",
			ParentID:  owner,
			SessionID: parentSID,
			Status:    "running",
		})

		outcome, err := reg.Send(parent, SendParams{Session: spawned.ID, Message: "new constraint"})
		if err != nil {
			t.Fatalf("Send error: %v", err)
		}
		if outcome.ID != spawned.ID || outcome.Mode != SendModeSteered {
			t.Fatalf("outcome = %+v, want mode=steered", outcome)
		}
		steerMu.Lock()
		gotMsg, gotAttr := steerMsg, steerAttr
		steerMu.Unlock()
		if gotMsg != "new constraint" || gotAttr != "agent" {
			t.Fatalf("steer got (%q, %q), want (%q, %q)", gotMsg, gotAttr, "new constraint", "agent")
		}

		close(release)
		if j, ok := jobs.Get(parentSID, spawned.JobID); ok {
			<-j.Done()
		}
	})

	t.Run("queues message when mid-turn worker is not steerable and drains it as next turn", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		agentReg := agents.NewRegistry()
		releaseFirst := make(chan struct{})
		firstStarted := make(chan struct{}, 1)
		followUpCh := make(chan string, 1)

		parent := &ParentSession{
			OwnerID:       owner,
			SessionID:     parentSID,
			AgentRegistry: agentReg,
			SubagentRunner: func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
				firstStarted <- struct{}{}
				<-releaseFirst
				agentReg.Register(&agents.AgentRef{
					ID:        req.ID,
					Kind:      "sub",
					ParentID:  owner,
					SessionID: parentSID,
					Status:    "idle",
				})
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "turn 1 done", DurationMs: 1}, nil
			},
			FollowUpRunner: func(ctx context.Context, req FollowUpRequest) (agents.SingleResult, error) {
				followUpCh <- req.Message
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "turn 2 done", DurationMs: 1}, nil
			},
			SteerWorker: func(ref *agents.AgentRef, message string, attribution string) (bool, error) {
				return false, nil
			},
		}

		spawned, err := reg.Spawn(parent, SpawnParams{CLI: CLIFast, Prompt: "first"})
		if err != nil {
			t.Fatalf("Spawn error: %v", err)
		}
		<-firstStarted

		outcome, err := reg.Send(parent, SendParams{Session: spawned.ID, Message: "queued follow-up"})
		if err != nil {
			t.Fatalf("Send error: %v", err)
		}
		if outcome.Mode != SendModeQueued {
			t.Fatalf("mode = %q, want queued", outcome.Mode)
		}

		close(releaseFirst)
		select {
		case msg := <-followUpCh:
			if msg != "queued follow-up" {
				t.Fatalf("follow-up message = %q, want 'queued follow-up'", msg)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for queued follow-up turn to drain")
		}
	})

	t.Run("keeps an idle worker message on the new-turn path", func(t *testing.T) {
		resetAllTestState(t)
		reg := Global()
		agentReg := agents.NewRegistry()
		steerCalled := false
		followUpCh := make(chan FollowUpRequest, 1)

		parent := &ParentSession{
			OwnerID:       owner,
			SessionID:     parentSID,
			AgentRegistry: agentReg,
			SubagentRunner: func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
				agentReg.Register(&agents.AgentRef{
					ID:        req.ID,
					Kind:      "sub",
					ParentID:  owner,
					SessionID: parentSID,
					Status:    "idle",
				})
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "done", DurationMs: 1}, nil
			},
			FollowUpRunner: func(ctx context.Context, req FollowUpRequest) (agents.SingleResult, error) {
				followUpCh <- req
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "next done", DurationMs: 1}, nil
			},
			SteerWorker: func(ref *agents.AgentRef, message string, attribution string) (bool, error) {
				steerCalled = true
				return true, nil
			},
		}

		spawned, err := reg.Spawn(parent, SpawnParams{CLI: CLIFast, Prompt: "initial work"})
		if err != nil {
			t.Fatalf("Spawn error: %v", err)
		}
		if j, ok := jobs.Get(parentSID, spawned.JobID); ok {
			<-j.Done()
		}
		outcome, err := reg.Send(parent, SendParams{Session: spawned.ID, Message: "next task"})
		if err != nil {
			t.Fatalf("Send error: %v", err)
		}
		if outcome.Mode != SendModeTurn || outcome.JobID == "" {
			t.Fatalf("outcome = %+v, want mode=turn with jobId", outcome)
		}
		select {
		case req := <-followUpCh:
			if req.Message != "next task" {
				t.Fatalf("followUp.Message = %q, want 'next task'", req.Message)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for follow-up turn")
		}
		if steerCalled {
			t.Fatalf("steer must not be called on idle worker")
		}
	})
}

// ── 5. Upstream test/vibe/spawn-model-role.test.ts ───────────────────────────

func TestVibeWorkerSpawnModelRoleAndNameSanitization(t *testing.T) {
	resetAllTestState(t)

	spawnAndCapture := func(t *testing.T, cli CLI, roles map[string]string, overrides map[string][]string, name string, nameGen func() string) SpawnRequest {
		t.Helper()
		resetAllTestState(t)
		reg := Global()
		reg.NameGenerator = nameGen
		captured := make(chan SpawnRequest, 1)
		parent := &ParentSession{
			OwnerID:        agents.MainAgentID,
			SessionID:      "parent-session",
			ModelRoles:     roles,
			ModelOverrides: overrides,
			SubagentRunner: func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
				captured <- req
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "done", DurationMs: 1}, nil
			},
		}
		out, err := reg.Spawn(parent, SpawnParams{CLI: cli, Name: name, Prompt: "work"})
		if err != nil {
			t.Fatalf("Spawn error: %v", err)
		}
		if j, ok := jobs.Get("parent-session", out.JobID); ok {
			<-j.Done()
		}
		return <-captured
	}

	t.Run("forwards task role behind good worker expanded patterns", func(t *testing.T) {
		req := spawnAndCapture(t, CLIGood, map[string]string{"default": "anthropic/opus", "task": "anthropic/sonnet"}, nil, "", nil)
		if !slices.Equal(req.ModelOverride, []string{"anthropic/sonnet"}) {
			t.Fatalf("ModelOverride = %v, want [anthropic/sonnet]", req.ModelOverride)
		}
		if req.ModelRole != "task" {
			t.Fatalf("ModelRole = %q, want task", req.ModelRole)
		}
	})

	t.Run("forwards smol role behind fast worker expanded patterns", func(t *testing.T) {
		req := spawnAndCapture(t, CLIFast, map[string]string{"default": "anthropic/opus", "smol": "fast/hy3"}, nil, "", nil)
		if !slices.Equal(req.ModelOverride, []string{"fast/hy3"}) {
			t.Fatalf("ModelOverride = %v, want [fast/hy3]", req.ModelOverride)
		}
		if req.ModelRole != "smol" {
			t.Fatalf("ModelRole = %q, want smol", req.ModelRole)
		}
	})

	t.Run("clears role identity when per-agent model override replaces alias", func(t *testing.T) {
		req := spawnAndCapture(t, CLIGood,
			map[string]string{"default": "anthropic/opus", "task": "anthropic/sonnet"},
			map[string][]string{"task": {"openai-codex/sol"}},
			"", nil,
		)
		if !slices.Equal(req.ModelOverride, []string{"openai-codex/sol"}) {
			t.Fatalf("ModelOverride = %v, want [openai-codex/sol]", req.ModelOverride)
		}
		if req.ModelRole != "" {
			t.Fatalf("ModelRole = %q, want empty", req.ModelRole)
		}
	})

	t.Run("keeps only [A-Za-z0-9_-] from a custom worker name", func(t *testing.T) {
		req := spawnAndCapture(t, CLIGood, nil, nil, "../My Worker!", nil)
		if req.ID != "MyWorker" {
			t.Fatalf("ID = %q, want MyWorker", req.ID)
		}
	})

	t.Run("falls back to generated name when nothing survives", func(t *testing.T) {
		req := spawnAndCapture(t, CLIGood, nil, nil, "!!!", func() string { return "GeneratedWorker" })
		if req.ID != "GeneratedWorker" {
			t.Fatalf("ID = %q, want GeneratedWorker", req.ID)
		}
	})
}

// ── 6. Upstream test/vibe/token-rate.test.ts ─────────────────────────────────

func TestAggregateVibeWorkerTokensPerSecond(t *testing.T) {
	resetAllTestState(t)
	const owner = "test-owner"

	assistantMsg := func(output int, durationMs int) map[string]any {
		return map[string]any{
			"role":      "assistant",
			"timestamp": 1000,
			"duration":  durationMs,
			"usage":     map[string]any{"output": output},
		}
	}

	type fakeSess struct {
		msgs      []map[string]any
		streaming bool
	}

	setup := func() (*SessionRegistry, *agents.Registry, map[string]*fakeSess) {
		reg := NewSessionRegistry()
		agReg := agents.NewRegistry()
		sessions := map[string]*fakeSess{}
		return reg, agReg, sessions
	}

	inspectFrom := func(sessions map[string]*fakeSess) func(*agents.AgentRef) ([]map[string]any, bool) {
		return func(ref *agents.AgentRef) ([]map[string]any, bool) {
			fs := sessions[ref.ID]
			if fs == nil {
				return nil, false
			}
			return fs.msgs, fs.streaming
		}
	}

	t.Run("returns false when owner has no worker sessions", func(t *testing.T) {
		reg, agReg, sessions := setup()
		if _, ok := reg.AggregateWorkerTokensPerSecond(owner, agReg, inspectFrom(sessions)); ok {
			t.Fatalf("expected ok=false when no workers exist")
		}
	})

	t.Run("sums tok/s across every live streaming worker", func(t *testing.T) {
		reg, agReg, sessions := setup()
		reg.RegisterRecordForTests(TestRecordInput{ID: "w1", OwnerID: owner})
		reg.RegisterRecordForTests(TestRecordInput{ID: "w2", OwnerID: owner})
		agReg.Register(&agents.AgentRef{ID: "w1", Kind: "sub", Status: "running"})
		agReg.Register(&agents.AgentRef{ID: "w2", Kind: "sub", Status: "running"})
		sessions["w1"] = &fakeSess{msgs: []map[string]any{assistantMsg(100, 1000)}, streaming: true}
		sessions["w2"] = &fakeSess{msgs: []map[string]any{assistantMsg(50, 500)}, streaming: true}

		rate, ok := reg.AggregateWorkerTokensPerSecond(owner, agReg, inspectFrom(sessions))
		if !ok || rate != 200 {
			t.Fatalf("rate = (%v, %v), want (200, true)", rate, ok)
		}
	})

	t.Run("ignores idle workers whose last turn finished", func(t *testing.T) {
		reg, agReg, sessions := setup()
		reg.RegisterRecordForTests(TestRecordInput{ID: "w1", OwnerID: owner})
		agReg.Register(&agents.AgentRef{ID: "w1", Kind: "sub", Status: "idle"})
		sessions["w1"] = &fakeSess{msgs: []map[string]any{assistantMsg(100, 1000)}, streaming: false}

		if _, ok := reg.AggregateWorkerTokensPerSecond(owner, agReg, inspectFrom(sessions)); ok {
			t.Fatalf("expected ok=false for idle worker")
		}
	})

	t.Run("counts streaming workers while skipping idle and detached siblings", func(t *testing.T) {
		reg, agReg, sessions := setup()
		reg.RegisterRecordForTests(TestRecordInput{ID: "w1", OwnerID: owner})
		reg.RegisterRecordForTests(TestRecordInput{ID: "w2", OwnerID: owner})
		reg.RegisterRecordForTests(TestRecordInput{ID: "w3", OwnerID: owner})
		agReg.Register(&agents.AgentRef{ID: "w1", Kind: "sub", Status: "running"})
		agReg.Register(&agents.AgentRef{ID: "w2", Kind: "sub", Status: "idle"})
		// w3 is detached (not in agReg).
		sessions["w1"] = &fakeSess{msgs: []map[string]any{assistantMsg(100, 1000)}, streaming: true}
		sessions["w2"] = &fakeSess{msgs: []map[string]any{assistantMsg(50, 500)}, streaming: false}

		rate, ok := reg.AggregateWorkerTokensPerSecond(owner, agReg, inspectFrom(sessions))
		if !ok || rate != 100 {
			t.Fatalf("rate = (%v, %v), want (100, true)", rate, ok)
		}
	})

	t.Run("scopes to requesting owner", func(t *testing.T) {
		reg, agReg, sessions := setup()
		reg.RegisterRecordForTests(TestRecordInput{ID: "w1", OwnerID: owner})
		reg.RegisterRecordForTests(TestRecordInput{ID: "w2", OwnerID: "other-owner"})
		agReg.Register(&agents.AgentRef{ID: "w1", Kind: "sub", Status: "running"})
		agReg.Register(&agents.AgentRef{ID: "w2", Kind: "sub", Status: "running"})
		sessions["w1"] = &fakeSess{msgs: []map[string]any{assistantMsg(100, 1000)}, streaming: true}
		sessions["w2"] = &fakeSess{msgs: []map[string]any{assistantMsg(50, 500)}, streaming: true}

		rate, ok := reg.AggregateWorkerTokensPerSecond(owner, agReg, inspectFrom(sessions))
		if !ok || rate != 100 {
			t.Fatalf("rate = (%v, %v), want (100, true)", rate, ok)
		}
	})
}

// ── 7. Parked follow-up refusal & Rehydrate ──────────────────────────────────

func TestParkedWorkerFollowUpRefusesTruthfullyAndRehydrateRestoresRoster(t *testing.T) {
	resetAllTestState(t)

	dir := t.TempDir()
	parentFile := filepath.Join(dir, "parent.jsonl")
	artDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	childFile := filepath.Join(artDir, "WorkerA.jsonl")
	if err := os.WriteFile(childFile, []byte(`{"type":"session","version":3,"id":"WorkerA"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write child jsonl: %v", err)
	}

	makeEntry := func(id string, ev LifecycleEvent) json.RawMessage {
		b, _ := json.Marshal(map[string]any{
			"id":         id,
			"type":       "custom",
			"customType": LifecycleCustomType,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
			"data":       ev.ToMap(),
		})
		return b
	}
	entries := []json.RawMessage{
		makeEntry("e1", LifecycleEvent{
			Version:          LifecycleVersion,
			ID:               "WorkerA",
			OwnerID:          agents.MainAgentID,
			ParentSessionID:  "sess-rehydrate",
			Action:           ActionSpawn,
			CLI:              CLIFast,
			Agent:            "sonic",
			ChildSessionFile: "WorkerA.jsonl",
			CreatedAt:        1700000000000,
		}),
		makeEntry("e2", LifecycleEvent{
			Version:         LifecycleVersion,
			ID:              "WorkerA",
			OwnerID:         agents.MainAgentID,
			ParentSessionID: "sess-rehydrate",
			Action:          ActionTurnStarted,
			Turn:            1,
		}),
		makeEntry("e3", LifecycleEvent{
			Version:         LifecycleVersion,
			ID:              "WorkerA",
			OwnerID:         agents.MainAgentID,
			ParentSessionID: "sess-rehydrate",
			Action:          ActionTurnSettled,
			Turn:            1,
		}),
	}

	reg := NewSessionRegistry()
	agReg := agents.NewRegistry()
	parent := &ParentSession{
		OwnerID:       agents.MainAgentID,
		SessionID:     "sess-rehydrate",
		SessionFile:   parentFile,
		AgentRegistry: agReg,
		GetEntries:    func() ([]json.RawMessage, error) { return entries, nil },
		GetBranch: func() ([]sdk.BranchEntry, error) {
			return []sdk.BranchEntry{{ID: "e1"}, {ID: "e2"}, {ID: "e3"}}, nil
		},
	}

	restored, err := reg.Rehydrate(parent)
	if err != nil {
		t.Fatalf("Rehydrate error: %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored = %d, want 1", restored)
	}
	// Rehydrated worker has no live session, so it must NOT present as idle zombie.
	screens, err := reg.Screens(parent, nil)
	if err != nil {
		t.Fatalf("Screens error: %v", err)
	}
	if len(screens) != 1 || screens[0].ID != "WorkerA" || screens[0].State != StateDead {
		t.Fatalf("rehydrated sessionless worker must be dead (not idle zombie), got: %+v", screens)
	}
	if _, err := reg.Send(parent, SendParams{Session: "WorkerA", Message: "continue"}); err == nil || !strings.Contains(err.Error(), "dead") {
		t.Fatalf("expected dead-worker refusal on Send after Rehydrate, got: %v", err)
	}

	// A parked/sessionless worker whose record was previously idle must also
	// fail fast on Send and transition to StateDead + unrecoverable tombstone.
	var appended []map[string]any
	parentWithAppend := &ParentSession{
		OwnerID:       agents.MainAgentID,
		SessionID:     "sess-parked-zombie",
		SessionFile:   parentFile,
		AgentRegistry: agReg,
		AppendEntry: func(customType string, data any) error {
			if m, ok := data.(map[string]any); ok {
				appended = append(appended, m)
			}
			return nil
		},
	}
	agReg.Register(&agents.AgentRef{
		ID:        "ParkedWorker",
		Kind:      "sub",
		ParentID:  agents.MainAgentID,
		SessionID: "sess-parked-zombie",
		Status:    "parked",
		Session:   nil,
	})
	reg.RegisterRecordForTests(TestRecordInput{
		ID:    "ParkedWorker",
		Scope: &OwnerScope{OwnerID: agents.MainAgentID, ParentSessionID: "sess-parked-zombie", ParentSessionFile: parentFile},
		State: StateIdle,
	})
	if _, err := reg.Send(parentWithAppend, SendParams{Session: "ParkedWorker", Message: "hello"}); err == nil || !strings.Contains(err.Error(), "cold revival") {
		t.Fatalf("expected cold-revival error on parked worker Send, got: %v", err)
	}
	pScreens, _ := reg.Screens(parentWithAppend, nil)
	if len(pScreens) != 1 || pScreens[0].State != StateDead {
		t.Fatalf("parked worker must transition to StateDead after Send refusal, got: %+v", pScreens)
	}
	foundTombstone := false
	for _, m := range appended {
		if m["action"] == string(ActionTombstone) && m["reason"] == string(TombstoneUnrecoverable) && m["id"] == "ParkedWorker" {
			foundTombstone = true
		}
	}
	if !foundTombstone {
		t.Fatalf("expected unrecoverable tombstone persisted for ParkedWorker, got: %+v", appended)
	}

	// Attempting a follow-up turn via defaultRunFollowUpTurn on a parked worker
	// (no live sdk.AgentSession) must fail truthfully, never stub or fake success.
	_, err = defaultRunFollowUpTurn(context.Background(), FollowUpRequest{
		Scope: OwnerScope{
			OwnerID:           agents.MainAgentID,
			ParentSessionID:   "sess-rehydrate",
			ParentSessionFile: parentFile,
		},
		ID:            "WorkerA",
		CLI:           CLIFast,
		Message:       "continue",
		AgentRegistry: agReg,
	})
	if err == nil || !strings.Contains(err.Error(), "parked") || !strings.Contains(err.Error(), "history://WorkerA") {
		t.Fatalf("expected truthful parked-revival error, got: %v", err)
	}
}

// ── 8. End-to-end /vibe command, prompt injection, full tool cycle, cleanup ──

func TestVibeEndToEndToggleToolsPromptAndCleanup(t *testing.T) {
	resetAllTestState(t)

	ext := sdk.New("vibe-e2e")
	Register(ext)
	approval.Register(ext)

	h := startFakeHost(t, ext, fakeHostOpts{
		SessionID:     "sess-vibe-e2e",
		Mode:          "tui",
		HasUI:         true,
		IsIdle:        true,
		InitialTools:  []string{"read", "todo", "bash", "edit"},
		InitialActive: []string{"read", "todo", "bash", "edit"},
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	// 1. Default session (vibe OFF): ZERO vibe tools registered, ZERO prompt lines injected.
	for _, name := range ToolNames {
		if slices.Contains(h.getRegisteredToolNames(), name) {
			t.Fatalf("default session must not register %q before /vibe", name)
		}
	}
	beforeOff := h.emitEvent(sdk.EventBeforeAgentStart, nil)
	for _, raw := range beforeOff {
		if strings.Contains(string(raw), "<vibe-mode>") {
			t.Fatalf("default session injected <vibe-mode> while inactive: %s", string(raw))
		}
	}

	// 2. Plan/goal conflict check: entering /vibe while plan mode active warns and does not activate.
	ActiveModeCheck = func(_ sdk.Context) string { return modes.ModePlan }
	if err := h.callCommand("vibe", ""); err != nil {
		t.Fatalf("/vibe while plan active returned error: %v", err)
	}
	if slices.Contains(h.getActiveTools(), "vibe_spawn") {
		t.Fatalf("/vibe must not activate while plan mode is active")
	}
	ActiveModeCheck = func(_ sdk.Context) string { return "" }

	// 3. Activate /vibe with an initial prompt.
	if err := h.callCommand("vibe", "Split this into two workstreams"); err != nil {
		t.Fatalf("/vibe activate failed: %v", err)
	}
	gotActive := h.getActiveTools()
	wantActive := append([]string{"read", "todo"}, ToolNames...)
	if !slices.Equal(gotActive, wantActive) {
		t.Fatalf("active tools in vibe mode = %v, want %v", gotActive, wantActive)
	}
	h.mu.Lock()
	userMsgs := append([]map[string]any(nil), h.sentUserMessages...)
	statusVibe := h.statuses[statusKeyVibe]
	h.mu.Unlock()
	if len(userMsgs) != 1 || userMsgs[0]["content"] != "Split this into two workstreams" {
		t.Fatalf("expected initial prompt sent via sendUserMessage, got %+v", userMsgs)
	}
	if statusVibe != "VIBE" {
		t.Fatalf("status = %q, want VIBE", statusVibe)
	}

	// 4. before_agent_start injects `<vibe-mode>` with `todo` included.
	beforeOn := h.emitEvent(sdk.EventBeforeAgentStart, nil)
	var directiveContent, directiveType string
	for _, raw := range beforeOn {
		var env map[string]any
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		if inner, ok := env["_pigPromptResult"].(map[string]any); ok {
			env = inner
		}
		if msg, ok := env["message"].(map[string]any); ok {
			directiveType, _ = msg["customType"].(string)
			directiveContent, _ = msg["content"].(string)
		}
	}
	if directiveType != ModeContextMessageType || !strings.Contains(directiveContent, "<vibe-mode>") || !strings.Contains(directiveContent, "`todo`") {
		t.Fatalf("expected <vibe-mode> directive with todo in before_agent_start, got type=%q content=%q", directiveType, directiveContent)
	}
	// 5. Full lifecycle: vibe_spawn -> vibe_send -> vibe_wait -> vibe_list -> vibe_kill.
	// Allow tools under yolo approval mode for this E2E run.
	approval.Setting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "yolo"
		}
		return nil
	}
	defer func() { approval.Setting = nil }()

	var turnMu sync.Mutex
	turn2Done := make(chan struct{}, 1)
	DefaultSubagentRunner = func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
		if req.OnProgress != nil {
			req.OnProgress(agents.AgentProgress{
				ToolCount:     1,
				CurrentTool:   "read",
				LastIntent:    "Inspecting files",
				ResolvedModel: "anthropic/claude-haiku-4-5",
				RecentTools:   []agents.RecentTool{{Tool: "read", Args: "main.go", EndMs: 100}},
				RecentOutput:  []string{"checked main.go"},
			})
		}
		agents.DefaultRuntime().Registry.Register(&agents.AgentRef{
			ID:        req.ID,
			Kind:      "sub",
			ParentID:  req.Scope.OwnerID,
			SessionID: req.Scope.ParentSessionID,
			Status:    "idle",
		})
		return agents.SingleResult{
			ID:            req.ID,
			Agent:         req.Agent.Name,
			ExitCode:      0,
			Output:        "first turn output",
			DurationMs:    42,
			Requests:      1,
			ResolvedModel: "anthropic/claude-haiku-4-5",
		}, nil
	}
	DefaultFollowUpRunner = func(ctx context.Context, req FollowUpRequest) (agents.SingleResult, error) {
		turnMu.Lock()
		defer turnMu.Unlock()
		defer func() {
			select {
			case turn2Done <- struct{}{}:
			default:
			}
		}()
		return agents.SingleResult{
			ID:         req.ID,
			Agent:      req.Agent.Name,
			ExitCode:   0,
			Output:     "second turn output for: " + req.Message,
			DurationMs: 25,
			Requests:   1,
		}, nil
	}

	spawnRaw, err := h.callTool("vibe_spawn", "c1", map[string]any{
		"cli":    "fast",
		"name":   "Builder",
		"prompt": "Build module A",
	})
	if err != nil {
		t.Fatalf("vibe_spawn failed: %v", err)
	}
	if !strings.Contains(string(spawnRaw), "Spawned fast session `Builder`") {
		t.Fatalf("unexpected vibe_spawn result: %s", string(spawnRaw))
	}

	// Wait for turn 1 to settle via vibe_wait.
	wait1Raw, err := h.callTool("vibe_wait", "c2", map[string]any{
		"sessions": []any{"Builder"},
		"timeout":  5.0,
	})
	if err != nil {
		t.Fatalf("vibe_wait turn 1 failed: %v", err)
	}
	var wait1Res struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(wait1Raw, &wait1Res)
	if !strings.Contains(wait1Res.Content, "first turn output") || !strings.Contains(wait1Res.Content, "<vibe-turn") {
		t.Fatalf("unexpected vibe_wait turn 1 result: %s", wait1Res.Content)
	}
	// Send follow-up message via vibe_send.
	sendRaw, err := h.callTool("vibe_send", "c3", map[string]any{
		"session": "Builder",
		"message": "Now add unit tests",
	})
	if err != nil {
		t.Fatalf("vibe_send failed: %v", err)
	}
	if !strings.Contains(string(sendRaw), "Started a new turn on `Builder`") {
		t.Fatalf("unexpected vibe_send result: %s", string(sendRaw))
	}
	<-turn2Done

	wait2Raw, err := h.callTool("vibe_wait", "c4", map[string]any{
		"sessions": []any{"Builder"},
		"timeout":  5.0,
	})
	if err != nil {
		t.Fatalf("vibe_wait turn 2 failed: %v", err)
	}
	if !strings.Contains(string(wait2Raw), "second turn output for: Now add unit tests") {
		t.Fatalf("unexpected vibe_wait turn 2 result: %s", string(wait2Raw))
	}

	// List via vibe_list.
	listRaw, err := h.callTool("vibe_list", "c5", map[string]any{})
	if err != nil {
		t.Fatalf("vibe_list failed: %v", err)
	}
	if !strings.Contains(string(listRaw), "`Builder` [fast] idle · 2 turns") {
		t.Fatalf("unexpected vibe_list result: %s", string(listRaw))
	}

	// Kill via vibe_kill.
	killRaw, err := h.callTool("vibe_kill", "c6", map[string]any{"session": "Builder"})
	if err != nil {
		t.Fatalf("vibe_kill failed: %v", err)
	}
	if !strings.Contains(string(killRaw), "Killed session `Builder`") || !strings.Contains(string(killRaw), "history://Builder") {
		t.Fatalf("unexpected vibe_kill result: %s", string(killRaw))
	}

	// 6. Deactivate /vibe: restores exact pre-vibe active toolset and strips directive from context.
	if err := h.callCommand("vibe", ""); err != nil {
		t.Fatalf("/vibe deactivate failed: %v", err)
	}
	restored := h.getActiveTools()
	if !slices.Equal(restored, []string{"read", "todo", "bash", "edit"}) {
		t.Fatalf("restored active tools = %v, want [read todo bash edit]", restored)
	}
	beforeAfterExit := h.emitEvent(sdk.EventBeforeAgentStart, nil)
	for _, raw := range beforeAfterExit {
		if strings.Contains(string(raw), "<vibe-mode>") {
			t.Fatalf("directive still injected after exit: %s", string(raw))
		}
	}

	// Context hook strips persisted vibe-mode-context messages once inactive.
	ctxRes := h.emitEvent(sdk.EventContext, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "custom", "customType": ModeContextMessageType, "content": "<vibe-mode>stale</vibe-mode>"},
		},
	})
	for _, raw := range ctxRes {
		if strings.Contains(string(raw), "<vibe-mode>") {
			t.Fatalf("context hook did not strip stale vibe-mode-context: %s", string(raw))
		}
	}

	// Calling a vibe tool after exit fails with an explicit inactive error.
	if _, err := h.callTool("vibe_list", "c7", map[string]any{}); err == nil || !strings.Contains(err.Error(), "Vibe mode is not active") {
		t.Fatalf("expected inactive vibe error after exit, got: %v", err)
	}
}

func TestVibeEmptyPreVibeToolsetAndStorageFailureRetry(t *testing.T) {
	resetAllTestState(t)

	ext := sdk.New("vibe-empty-tools")
	Register(ext)

	h := startFakeHost(t, ext, fakeHostOpts{
		SessionID:     "sess-empty",
		Mode:          "tui",
		HasUI:         true,
		IsIdle:        true,
		InitialTools:  []string{"read", "todo"},
		InitialActive: []string{}, // Legitimate empty active toolset
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	if err := h.callCommand("vibe", ""); err != nil {
		t.Fatalf("enter vibe error: %v", err)
	}
	want := append([]string{"read", "todo"}, ToolNames...)
	if got := h.getActiveTools(); !slices.Equal(got, want) {
		t.Fatalf("active tools = %v, want %v", got, want)
	}

	// Simulate journal write failure during exit: mode stays active so user can retry.
	h.mu.Lock()
	h.failNextAppend = true
	h.mu.Unlock()
	if err := h.callCommand("vibe", ""); err == nil || !strings.Contains(err.Error(), "journal atomic publish failed") {
		t.Fatalf("expected appendEntry failure on exit, got: %v", err)
	}
	if got := h.getActiveTools(); !slices.Equal(got, want) {
		t.Fatalf("tools changed despite failed exit: %v", got)
	}

	// Retry succeeds and restores the empty active toolset.
	if err := h.callCommand("vibe", ""); err != nil {
		t.Fatalf("retry exit failed: %v", err)
	}
	if got := h.getActiveTools(); len(got) != 0 {
		t.Fatalf("expected empty restored toolset, got %v", got)
	}
}

func TestVibeSessionEndCleanupIsolation(t *testing.T) {
	resetAllTestState(t)

	reg := Global()
	lifecycle.OnEnd("vibe", reg.EndSession)

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	jobA := newVibeTurnJob("WorkerA-t1", "WorkerA", agents.MainAgentID, "vibe A", cancelA)
	jobB := newVibeTurnJob("WorkerB-t1", "WorkerB", agents.MainAgentID, "vibe B", cancelB)
	jobs.Add("sess-A", jobA)
	jobs.Add("sess-B", jobB)

	reg.RegisterRecordForTests(TestRecordInput{
		ID:    "WorkerA",
		Scope: &OwnerScope{OwnerID: agents.MainAgentID, ParentSessionID: "sess-A"},
		State: StateRunning,
		JobID: jobA.ID(),
	})
	reg.RegisterRecordForTests(TestRecordInput{
		ID:    "WorkerB",
		Scope: &OwnerScope{OwnerID: agents.MainAgentID, ParentSessionID: "sess-B"},
		State: StateRunning,
		JobID: jobB.ID(),
	})

	// End only sess-A: WorkerA's job cancels and its record disappears; sess-B is untouched.
	lifecycle.End("sess-A")

	if ctxA.Err() == nil {
		t.Fatalf("expected sess-A worker job to be cancelled on lifecycle.End")
	}
	if ctxB.Err() != nil {
		t.Fatalf("sess-B worker job must not be cancelled when sess-A ends")
	}
	idsA, _ := reg.ListIDs(&ParentSession{SessionID: "sess-A"})
	idsB, _ := reg.ListIDs(&ParentSession{SessionID: "sess-B"})
	if len(idsA) != 0 {
		t.Fatalf("expected sess-A records cleared, got %v", idsA)
	}
	if !slices.Equal(idsB, []string{"WorkerB"}) {
		t.Fatalf("expected sess-B records preserved, got %v", idsB)
	}
}

func TestVibeApprovalTiers(t *testing.T) {
	resetAllTestState(t)

	cases := []struct {
		tool string
		want approval.Tier
	}{
		{"vibe_spawn", approval.TierExec},
		{"vibe_send", approval.TierExec},
		{"vibe_wait", approval.TierRead},
		{"vibe_kill", approval.TierRead},
		{"vibe_list", approval.TierRead},
	}
	for _, tc := range cases {
		dec := approval.EvaluateToolDecision(sdk.Context{}, tc.tool, map[string]any{})
		if dec.Tier != tc.want {
			t.Fatalf("approval.EvaluateToolDecision(%q) = %q, want %q", tc.tool, dec.Tier, tc.want)
		}
		// Under `always-ask`: read-tier tools auto-allow, exec-tier tools prompt.
		res := approval.ResolvePolicy(tc.tool, dec, approval.ModeAlwaysAsk, "")
		if tc.want == approval.TierRead && res.Policy != approval.PolicyAllow {
			t.Fatalf("%s under always-ask = %q, want allow", tc.tool, res.Policy)
		}
		if tc.want == approval.TierExec && res.Policy != approval.PolicyPrompt {
			t.Fatalf("%s under always-ask = %q, want prompt", tc.tool, res.Policy)
		}

		// Under `write` mode: read-tier allows, exec-tier prompts.
		resWrite := approval.ResolvePolicy(tc.tool, dec, approval.ModeWrite, "")
		if tc.want == approval.TierRead && resWrite.Policy != approval.PolicyAllow {
			t.Fatalf("%s under write = %q, want allow", tc.tool, resWrite.Policy)
		}
		if tc.want == approval.TierExec && resWrite.Policy != approval.PolicyPrompt {
			t.Fatalf("%s under write = %q, want prompt", tc.tool, resWrite.Policy)
		}

		// Explicit user deny always blocks.
		resDeny := approval.ResolvePolicy(tc.tool, dec, approval.ModeYolo, approval.PolicyDeny)
		if resDeny.Policy != approval.PolicyDeny {
			t.Fatalf("%s with user deny = %q, want deny", tc.tool, resDeny.Policy)
		}
	}
}

func TestVibeConcurrentSpawnAndKillRaceClean(t *testing.T) {
	resetAllTestState(t)

	reg := NewSessionRegistry()
	parent := &ParentSession{
		OwnerID:   agents.MainAgentID,
		SessionID: "sess-race",
		SubagentRunner: func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
			if req.OnProgress != nil {
				req.OnProgress(agents.AgentProgress{ToolCount: 1, CurrentTool: "bash", LastIntent: "running"})
			}
			select {
			case <-ctx.Done():
				return agents.SingleResult{}, errors.New("cancelled")
			case <-time.After(15 * time.Millisecond):
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "ok", DurationMs: 15}, nil
			}
		},
	}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, err := reg.Spawn(parent, SpawnParams{
				CLI:    CLIFast,
				Name:   fmt.Sprintf("Worker%d", idx),
				Prompt: "work",
			})
			if err != nil {
				return
			}
			_, _ = reg.Screens(parent, nil)
			if idx%2 == 0 {
				_, _ = reg.Kill(parent, out.ID)
			}
		}(i)
	}
	wg.Wait()
	_, _ = reg.KillAll(parent, nil)
}

// ── 9. Regression tests for peer-review findings ─────────────────────────────

func TestVibeConcurrentSendNeverOverlapsTurns(t *testing.T) {
	resetAllTestState(t)

	const iterations = 320
	for iter := range iterations {
		reg := NewSessionRegistry()
		agReg := agents.NewRegistry()
		sid := fmt.Sprintf("sess-send-race-%d", iter)

		var activeMu sync.Mutex
		activeTurns := 0
		maxOverlap := 0

		parent := &ParentSession{
			OwnerID:       agents.MainAgentID,
			SessionID:     sid,
			AgentRegistry: agReg,
			FollowUpRunner: func(ctx context.Context, req FollowUpRequest) (agents.SingleResult, error) {
				activeMu.Lock()
				activeTurns++
				if activeTurns > maxOverlap {
					maxOverlap = activeTurns
				}
				activeMu.Unlock()
				time.Sleep(200 * time.Microsecond)
				activeMu.Lock()
				activeTurns--
				activeMu.Unlock()
				return agents.SingleResult{ID: req.ID, Agent: req.Agent.Name, ExitCode: 0, Output: "ok", DurationMs: 1}, nil
			},
			SteerWorker: func(ref *agents.AgentRef, message string, attribution string) (bool, error) {
				return false, nil
			},
		}
		agReg.Register(&agents.AgentRef{
			ID:        "Worker1",
			Kind:      "sub",
			ParentID:  agents.MainAgentID,
			SessionID: sid,
			Status:    "idle",
		})
		reg.RegisterRecordForTests(TestRecordInput{
			ID:    "Worker1",
			Scope: &OwnerScope{OwnerID: agents.MainAgentID, ParentSessionID: sid},
			State: StateIdle,
		})

		startGate := make(chan struct{})
		outcomes := make([]SendOutcome, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for g := range 2 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-startGate
				outcomes[idx], errs[idx] = reg.Send(parent, SendParams{
					Session: "Worker1",
					Message: fmt.Sprintf("msg-%d", idx),
				})
			}(g)
		}
		close(startGate)
		wg.Wait()

		for g := range 2 {
			if errs[g] != nil {
				t.Fatalf("iter %d goroutine %d unexpected error: %v", iter, g, errs[g])
			}
		}
		// Wait for all turns to settle before inspecting overlap.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			screens, _ := reg.Screens(parent, nil)
			if len(screens) == 1 && screens[0].State == StateIdle && screens[0].Queued == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}

		activeMu.Lock()
		overlap := maxOverlap
		activeMu.Unlock()
		if overlap > 1 {
			t.Fatalf("iter %d: overlapping turns detected (maxOverlap=%d, outcomes=%+v)", iter, overlap, outcomes)
		}
		if outcomes[0].Mode == SendModeTurn && outcomes[1].Mode == SendModeTurn && outcomes[0].JobID == outcomes[1].JobID {
			t.Fatalf("iter %d: duplicate job IDs %+v", iter, outcomes)
		}
		jobs.ClearSession(sid)
	}
}

func TestRenderTurnResultUpstreamFidelityAndUTF16Truncation(t *testing.T) {
	// 1. Padded markdown table, trailing whitespace, double blank lines,
	// raw attribute values (no Go %q escaping).
	rawResponse := "| Col A   |   Col B  |   \n| ------- | :------: |\n| foo     |   bar    |   \n\n\nAfter double blank   \n\nAfter single blank"
	got := RenderTurnResult(TurnResultData{
		ID:            "W1",
		CLI:           CLIGood,
		Turn:          2,
		Status:        "completed",
		Duration:      "1.2s",
		Requests:      3,
		ToolCount:     4,
		Model:         `anthropic/claude-opus-4-6`,
		Trace:         []string{"read(a.go)", "edit(a.go)"},
		TraceOverflow: 2,
		Response:      rawResponse,
		Alive:         true,
	})
	want := strings.Join([]string{
		`<vibe-turn session="W1" cli="good" turn="2" status="completed" duration="1.2s" model="anthropic/claude-opus-4-6">`,
		`<activity tool-calls="4" requests="3">`,
		`- read(a.go)`,
		`- edit(a.go)`,
		`- … 2 earlier tool call(s) not shown`,
		`</activity>`,
		`<response>`,
		`|Col A|Col B|`,
		`|---|:---:|`,
		`|foo|bar|`,
		`After double blank`,
		``,
		`After single blank`,
		`</response>`,
		"Session `W1` is idle and retains this conversation — continue it with vibe_send. Transcript: history://W1",
		`</vibe-turn>`,
	}, "\n")
	if got != want {
		t.Fatalf("RenderTurnResult mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// 2. UTF-16 code unit counting & safe UTF-8 boundary truncation:
	// 3000 3-byte BMP CJK runes = 9000 UTF-8 bytes, but only 3000 UTF-16 units
	// (<= ResponsePreviewMax=6000), so must NOT truncate.
	cjk := strings.Repeat("漢", 3000)
	if out, trunc := truncateResponseUTF16(cjk, ResponsePreviewMax); trunc || out != cjk {
		t.Fatalf("3000 BMP CJK runes (3000 UTF-16 units) should not truncate: trunc=%v len=%d", trunc, len(out))
	}
	// 3001 astral emoji runes (U+1F600) = 6002 UTF-16 units > 6000 -> truncates to
	// 3000 valid runes without splitting any UTF-8 sequence.
	emoji := strings.Repeat("😀", 3001)
	out, trunc := truncateResponseUTF16(emoji, ResponsePreviewMax)
	if !trunc {
		t.Fatalf("3001 astral emoji (6002 UTF-16 units) must truncate")
	}
	if out != strings.Repeat("😀", 3000) {
		t.Fatalf("truncated emoji length = %d bytes, want %d bytes", len(out), 3000*len("😀"))
	}
}

func TestRehydrateColdResumeDoesNotClobberFreshToolset(t *testing.T) {
	resetAllTestState(t)

	// Target session was saved in vibe mode when only `read` and `todo` existed.
	mcEntry, _ := json.Marshal(map[string]any{
		"id":         "e-vibe",
		"type":       "custom",
		"customType": entryTypeModeChange,
		"data": map[string]any{
			"mode": "vibe",
			"data": map[string]any{"previousTools": []string{"read", "todo"}},
		},
	})
	ext := sdk.New("vibe-cold-resume")
	Register(ext)
	h := startFakeHost(t, ext, fakeHostOpts{
		SessionID:      "sess-cold-resume",
		Mode:           "tui",
		HasUI:          true,
		IsIdle:         true,
		InitialTools:   []string{"read", "todo", "bash"},
		InitialActive:  []string{"read", "todo", "bash"},
		InitialEntries: []json.RawMessage{mcEntry},
		InitialBranch:  []sdk.BranchEntry{{ID: "e-vibe"}},
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	// Vibe mode is active now (narrowed).
	if slices.Contains(h.getActiveTools(), "bash") {
		t.Fatalf("expected bash stripped during resumed vibe mode, active=%v", h.getActiveTools())
	}
	// Exiting vibe must restore the freshly built live toolset (`read`, `todo`, `bash`),
	// NOT the stale persisted `previousTools` (`read`, `todo`).
	if err := h.callCommand("vibe", ""); err != nil {
		t.Fatalf("exit vibe error: %v", err)
	}
	restored := h.getActiveTools()
	if !slices.Equal(restored, []string{"read", "todo", "bash"}) {
		t.Fatalf("cold-resume exit restored = %v, want [read todo bash]", restored)
	}
}

func TestMarkTerminalRefGuardsForeignAgentEntries(t *testing.T) {
	resetAllTestState(t)
	reg := agents.NewRegistry()
	scope := OwnerScope{OwnerID: "Main", ParentSessionID: "sess-1", ParentSessionFile: "/tmp/p.jsonl"}

	// Foreign ref with different ParentID must not be overwritten.
	foreign := &agents.AgentRef{
		ID:          "Shared",
		Kind:        "sub",
		ParentID:    "OtherOwner",
		SessionID:   "sess-1",
		SessionFile: "/tmp/other/Shared.jsonl",
		Status:      "running",
	}
	reg.Register(foreign)
	markTerminalRef(reg, scope, "Shared", "/tmp/p/Shared.jsonl")
	got, ok := reg.GetInSession("sess-1", "Shared")
	if !ok || got.ParentID != "OtherOwner" || got.Status != "running" {
		t.Fatalf("markTerminalRef clobbered foreign ref: %+v", got)
	}
}

func TestKillAllAbortsOnPersistenceErrorAndKillDetectsScopeChange(t *testing.T) {
	resetAllTestState(t)
	reg := NewSessionRegistry()
	parentFile := filepath.Join(t.TempDir(), "parent.jsonl")
	childFile := filepath.Join(artifactsDirForParentFile(parentFile), "W1.jsonl")

	cancelCalled := false
	j := newVibeTurnJob("W1-t1", "W1", agents.MainAgentID, "vibe", func() { cancelCalled = true })
	jobs.Add("sess-killall", j)
	scope := OwnerScope{OwnerID: agents.MainAgentID, ParentSessionID: "sess-killall", ParentSessionFile: parentFile}
	reg.RegisterRecordForTests(TestRecordInput{
		ID:    "W1",
		Scope: &scope,
		State: StateRunning,
		JobID: j.ID(),
	})
	reg.mu.Lock()
	reg.records[scopeKey(scope, "W1")].ChildSessionFile = childFile
	reg.mu.Unlock()

	parent := &ParentSession{
		OwnerID:     agents.MainAgentID,
		SessionID:   "sess-killall",
		SessionFile: parentFile,
		AppendEntry: func(string, any) error { return errors.New("disk full") },
	}
	if _, err := reg.KillAll(parent, &scope); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected disk full error from KillAll, got: %v", err)
	}
	if cancelCalled {
		t.Fatalf("KillAll must abort before cancelling workers when tombstone persistence fails")
	}
	// Scope must not be terminated on persistence error.
	reg.mu.Lock()
	terminated := reg.terminatedScopes[scopeKey(scope, "")]
	reg.mu.Unlock()
	if terminated {
		t.Fatalf("scope must not be marked terminated when KillAll persistence failed")
	}

	// Scope mismatch on killRecord tombstone returns explicit error.
	otherParent := &ParentSession{
		OwnerID:     agents.MainAgentID,
		SessionID:   "sess-different",
		SessionFile: parentFile,
		AppendEntry: func(string, any) error { return nil },
	}
	reg.mu.Lock()
	rec := reg.records[scopeKey(scope, "W1")]
	reg.mu.Unlock()
	_, err := reg.killRecord(otherParent, rec, TombstoneExplicitKill, true, time.Now().Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), "changed parent scope before termination") {
		t.Fatalf("expected scope-change error from killRecord, got: %v", err)
	}
}

func TestSendDuringPeerIRCWakeDoesNotMarkDeadAndKillAllStillKills(t *testing.T) {
	resetAllTestState(t)
	reg := NewSessionRegistry()
	agReg := agents.NewRegistry()
	const sid = "sess-irc-wake"
	scope := OwnerScope{OwnerID: agents.MainAgentID, ParentSessionID: sid}

	// Live worker ref currently running a peer IRC wake turn (rec.Turn == nil,
	// ref.Status == "running", ref.Session != nil).
	liveSess := &sdk.AgentSession{}
	agReg.Register(&agents.AgentRef{
		ID:        "PeerWorker",
		Kind:      "sub",
		ParentID:  agents.MainAgentID,
		SessionID: sid,
		Status:    "running",
		Session:   liveSess,
	})
	reg.RegisterRecordForTests(TestRecordInput{
		ID:    "PeerWorker",
		Scope: &scope,
		State: StateIdle,
	})
	reg.mu.Lock()
	reg.records[scopeKey(scope, "PeerWorker")].AgentRegistry = agReg
	reg.mu.Unlock()

	parent := &ParentSession{
		OwnerID:       agents.MainAgentID,
		SessionID:     sid,
		AgentRegistry: agReg,
	}

	// Send during peer IRC wake must return a busy error WITHOUT marking dead.
	if _, err := reg.Send(parent, SendParams{Session: "PeerWorker", Message: "follow up"}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("expected busy ToolError during peer IRC wake, got: %v", err)
	}
	screens, err := reg.Screens(parent, nil)
	if err != nil {
		t.Fatalf("Screens error: %v", err)
	}
	if len(screens) != 1 || screens[0].State != StateIdle {
		t.Fatalf("worker must remain StateIdle (not dead) during peer IRC wake, got: %+v", screens)
	}

	// Clear Session before KillAll so KillAgent doesn't invoke wire Dispose on
	// the zero-value sdk.AgentSession, while still verifying KillAll counts and
	// kills the record (and a second KillAll on a fileless session returns 0).
	agReg.Register(&agents.AgentRef{
		ID:        "PeerWorker",
		Kind:      "sub",
		ParentID:  agents.MainAgentID,
		SessionID: sid,
		Status:    "idle",
	})
	killed, err := reg.KillAll(parent, &scope)
	if err != nil {
		t.Fatalf("KillAll error: %v", err)
	}
	if killed != 1 {
		t.Fatalf("KillAll killed = %d, want 1", killed)
	}
	killedAgain, err := reg.KillAll(parent, &scope)
	if err != nil {
		t.Fatalf("second KillAll error: %v", err)
	}
	if killedAgain != 0 {
		t.Fatalf("second KillAll in fileless session killed = %d, want 0", killedAgain)
	}
}
