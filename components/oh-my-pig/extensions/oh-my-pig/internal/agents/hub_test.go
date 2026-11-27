package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

func TestBuildHubSnapshotCountsSortingAndRevive(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	rt := NewRuntime(DefaultConfig())
	sessID := "sess-hub-1"

	// Empty session returns zero snapshot (keeps frontend HUD pill hidden).
	empty := rt.BuildHubSnapshot(sessID)
	if empty.InspectableCount != 0 || empty.RunningCount != 0 || len(empty.Agents) != 0 || len(empty.Activity) != 0 {
		t.Fatalf("expected empty snapshot, got %+v", empty)
	}

	// Main ref must not count as an inspectable subagent.
	rt.Registry.RegisterMain(sessID, &AgentRef{
		ID:        MainAgentID,
		Kind:      "main",
		Status:    "running",
		SessionID: sessID,
	})
	if snap := rt.BuildHubSnapshot(sessID); snap.InspectableCount != 0 {
		t.Fatalf("expected Main to be excluded, got InspectableCount=%d", snap.InspectableCount)
	}

	now := time.Now()
	rt.Registry.Register(&AgentRef{
		ID:            "Alpha",
		DisplayName:   "scout",
		AgentType:     "scout",
		Kind:          "sub",
		ParentID:      MainAgentID,
		Status:        "parked",
		SessionID:     sessID,
		Task:          "Inspect architecture",
		Model:         "anthropic/claude-sonnet-4-5",
		ModelRole:     "smol",
		ThinkingLevel: "low",
		Cost:          0.0125,
		DurationMs:    1500,
		Requests:      2,
		Tools:         3,
		Tokens:        1200,
		ContextTokens: 800,
		ContextWindow: 200000,
		CreatedAt:     now.Add(-5 * time.Minute),
		LastActive:    now.Add(-2 * time.Minute),
		OnRevive:      func() (string, error) { return "revived", nil },
		Messages:      []map[string]any{{"role": "user", "content": "hi"}},
	})
	rt.Registry.Register(&AgentRef{
		ID:            "Beta",
		DisplayName:   "reviewer",
		AgentType:     "reviewer",
		Kind:          "sub",
		ParentID:      MainAgentID,
		Status:        "running",
		SessionID:     sessID,
		Task:          "Review patch",
		Activity:      "Reading diff",
		Model:         "openai/gpt-5",
		Cost:          0.0250,
		DurationMs:    3200,
		TurnStartedAt: now.Add(-3 * time.Second),
		Requests:      4,
		Tools:         6,
		Tokens:        2400,
		CreatedAt:     now.Add(-2 * time.Minute),
		LastActive:    now.Add(-1 * time.Second),
	})
	rt.Registry.Register(&AgentRef{
		ID:            "Gamma",
		DisplayName:   "task",
		AgentType:     "task",
		Kind:          "sub",
		ParentID:      MainAgentID,
		Status:        "running",
		SessionID:     sessID,
		Task:          "Fix bug",
		Cost:          0.0050,
		DurationMs:    800,
		TurnStartedAt: now.Add(-1 * time.Second),
		Requests:      1,
		Tools:         1,
		Tokens:        500,
		CreatedAt:     now.Add(-1 * time.Minute),
		LastActive:    now,
	})
	rt.Registry.Register(&AgentRef{
		ID:          "Delta",
		DisplayName: "sonic",
		AgentType:   "sonic",
		Kind:        "sub",
		ParentID:    MainAgentID,
		Status:      "aborted",
		SessionID:   sessID,
		Cost:        0.0010,
		DurationMs:  400,
		Requests:    1,
		Tools:       0,
		Tokens:      100,
		CreatedAt:   now.Add(-4 * time.Minute),
		LastActive:  now.Add(-3 * time.Minute),
		// No SessionFile, Messages, OnRevive, or OnWake -> CanRevive must be false.
	})
	rt.Registry.Register(&AgentRef{
		ID:          "Epsilon",
		DisplayName: "scout",
		AgentType:   "scout",
		Kind:        "sub",
		ParentID:    MainAgentID,
		Status:      "idle",
		SessionID:   sessID,
		CreatedAt:   now.Add(-3 * time.Minute),
		LastActive:  now.Add(-30 * time.Second),
	})

	snap := rt.BuildHubSnapshot(sessID)
	if snap.InspectableCount != 5 {
		t.Fatalf("InspectableCount = %d, want 5", snap.InspectableCount)
	}
	if snap.RunningCount != 2 {
		t.Fatalf("RunningCount = %d, want 2", snap.RunningCount)
	}
	if diff := snap.SubagentCost - 0.0435; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("SubagentCost = %f, want 0.0435", snap.SubagentCost)
	}
	if snap.TotalDurationMs != 5900 {
		t.Fatalf("TotalDurationMs = %d, want 5900", snap.TotalDurationMs)
	}
	if snap.TotalRequests != 8 {
		t.Fatalf("TotalRequests = %d, want 8", snap.TotalRequests)
	}
	if snap.TotalTokens != 4200 {
		t.Fatalf("TotalTokens = %d, want 4200", snap.TotalTokens)
	}
	if snap.TotalTools != 10 {
		t.Fatalf("TotalTools = %d, want 10", snap.TotalTools)
	}

	// Order: running (newest first: Gamma, Beta), idle (Epsilon), parked (Alpha), aborted (Delta).
	wantOrder := []string{"Gamma", "Beta", "Epsilon", "Alpha", "Delta"}
	if len(snap.Agents) != len(wantOrder) {
		t.Fatalf("len(Agents) = %d, want %d", len(snap.Agents), len(wantOrder))
	}
	for i, id := range wantOrder {
		if snap.Agents[i].ID != id {
			t.Fatalf("Agents[%d].ID = %q, want %q", i, snap.Agents[i].ID, id)
		}
	}
	if snap.Agents[0].StartedAtMs == 0 || snap.Agents[2].StartedAtMs != 0 {
		t.Fatalf("StartedAtMs mismatch: running=%d idle=%d", snap.Agents[0].StartedAtMs, snap.Agents[2].StartedAtMs)
	}
	if !snap.Agents[3].CanRevive {
		t.Fatalf("expected parked Alpha with messages to have CanRevive=true")
	}
	if snap.Agents[4].CanRevive {
		t.Fatalf("expected aborted Delta without transcript/hook to have CanRevive=false")
	}
	if snap.Agents[0].CanRevive {
		t.Fatalf("expected running Gamma to have CanRevive=false")
	}
}

func TestActivityRingBoundedAndNewestFirst(t *testing.T) {
	reg := NewRegistry()
	sessID := "sess-ring"
	for i := range maxActivitiesPerSession + 40 {
		reg.RecordActivity(sessID, surface.AgentHubActivity{
			AgentID:     "Alpha",
			Kind:        "tool",
			Title:       "read",
			Detail:      "item-" + itoa(int64(i)),
			Status:      "ok",
			TimestampMs: int64(1000 + i),
		})
	}
	acts := reg.ListActivities(sessID)
	if len(acts) != maxActivitiesPerSession {
		t.Fatalf("len(ListActivities) = %d, want %d", len(acts), maxActivitiesPerSession)
	}
	if acts[0].Detail != "item-"+itoa(int64(maxActivitiesPerSession+39)) {
		t.Fatalf("expected newest activity first, got %q", acts[0].Detail)
	}
	if acts[len(acts)-1].Detail != "item-40" {
		t.Fatalf("expected oldest retained activity item-40, got %q", acts[len(acts)-1].Detail)
	}
}

func TestLoadTranscriptEntriesAndJSONLFallback(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	sessID := "sess-tr"

	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "Scout1.jsonl")
	rawMsgs := []map[string]any{
		{
			"role":      "user",
			"content":   "Investigate the bug",
			"timestamp": float64(1700000001000),
		},
		{
			"role":      "assistant",
			"timestamp": float64(1700000002000),
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "Let me read the file first."},
				map[string]any{"type": "text", "text": "Checking main.go"},
				map[string]any{
					"type":      "toolCall",
					"id":        "call-1",
					"name":      "read",
					"arguments": map[string]any{"path": "main.go"},
				},
			},
		},
		{
			"role":       "toolResult",
			"toolCallId": "call-1",
			"toolName":   "read",
			"isError":    false,
			"content":    []any{map[string]any{"type": "text", "text": "package main"}},
			"timestamp":  float64(1700000003000),
		},
		{
			"role":       "toolResult",
			"toolCallId": "call-orphan",
			"toolName":   "bash",
			"isError":    true,
			"content":    "exit status 1",
			"timestamp":  "2026-01-02T03:04:05Z",
		},
	}

	var b strings.Builder
	b.WriteString(`{"type":"session","version":3,"id":"Scout1"}` + "\n")
	for i, m := range rawMsgs {
		line, _ := json.Marshal(map[string]any{
			"type":    "message",
			"id":      "m" + itoa(int64(i+1)),
			"message": m,
		})
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(jsonlPath, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	rt.Registry.Register(&AgentRef{
		ID:          "Scout1",
		Kind:        "sub",
		Status:      "parked",
		SessionID:   sessID,
		SessionFile: jsonlPath,
	})

	entries := rt.LoadTranscriptEntries(sessID, "Scout1")
	if len(entries) != 4 {
		t.Fatalf("len(entries) = %d, want 4: %+v", len(entries), entries)
	}
	if entries[0].Role != "user" || entries[0].Text != "Investigate the bug" || entries[0].TimestampMs != 1700000001000 {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if entries[1].Role != "assistant" || entries[1].Thinking != "Let me read the file first." || entries[1].Text != "Checking main.go" {
		t.Fatalf("entries[1] = %+v", entries[1])
	}
	if entries[2].Role != "tool" || entries[2].ToolName != "read" || !strings.Contains(entries[2].ToolArgs, "main.go") || entries[2].ToolOutput != "package main" || entries[2].IsError {
		t.Fatalf("entries[2] = %+v", entries[2])
	}
	if entries[3].Role != "tool" || entries[3].ToolName != "bash" || entries[3].ToolOutput != "exit status 1" || !entries[3].IsError || entries[3].TimestampMs == 0 {
		t.Fatalf("entries[3] = %+v", entries[3])
	}
}

func TestHubHandlersLivePublishingKillReviveSendMessage(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	surface.ResetForTests()

	rt := NewRuntime(DefaultConfig())
	sessID := "sess-handlers"

	var mu sync.Mutex
	var snapshots []surface.AgentHubSnapshot
	unsub := surface.OnAgentHubChange(func(snap surface.AgentHubSnapshot) {
		mu.Lock()
		snapshots = append(snapshots, snap)
		mu.Unlock()
	})
	defer unsub()

	rt.BindHubSession(sessID)

	reviveCalled := false
	wokenMsgs := []IRCMessage{}
	ref := &AgentRef{
		ID:        "Worker1",
		AgentType: "task",
		Kind:      "sub",
		ParentID:  MainAgentID,
		Status:    "running",
		SessionID: sessID,
		Messages:  []map[string]any{{"role": "user", "content": "work"}},
		OnRevive: func() (string, error) {
			reviveCalled = true
			return "Revived Worker1", nil
		},
		OnWake: func(msg IRCMessage) bool {
			wokenMsgs = append(wokenMsgs, msg)
			return true
		},
	}
	rt.Registry.Register(ref)

	handlers := surface.GetAgentHubHandlers()
	if handlers.LoadTranscript == nil || handlers.Kill == nil || handlers.Revive == nil || handlers.SendMessage == nil {
		t.Fatalf("expected all AgentHubHandlers to be registered")
	}

	// SendMessage while running with nil Session parks in PendingIRC.
	if err := handlers.SendMessage("Worker1", "please check tests"); err != nil {
		t.Fatalf("SendMessage error: %v", err)
	}
	ref.mu.Lock()
	pendingCount := len(ref.PendingIRC)
	ref.mu.Unlock()
	if pendingCount != 1 {
		t.Fatalf("expected 1 PendingIRC message, got %d", pendingCount)
	}

	// Revive while running must fail.
	if _, err := handlers.Revive("Worker1"); err == nil {
		t.Fatalf("expected Revive on running agent to fail")
	}

	// Kill transitions Worker1 to aborted.
	msg, err := handlers.Kill("Worker1")
	if err != nil {
		t.Fatalf("Kill error: %v", err)
	}
	if !strings.Contains(msg, "Killed agent Worker1") {
		t.Fatalf("unexpected Kill message: %q", msg)
	}
	if snap := surface.GetAgentHubSnapshot(); snap.RunningCount != 0 || snap.InspectableCount != 1 || snap.Agents[0].Status != "aborted" {
		t.Fatalf("unexpected snapshot after Kill: %+v", snap)
	}

	// Kill on unknown agent returns error.
	if _, err := handlers.Kill("NoSuchAgent"); err == nil {
		t.Fatalf("expected Kill on unknown agent to fail")
	}

	// Revive on aborted agent calls OnRevive.
	ref.mu.Lock()
	ref.OnRevive = func() (string, error) {
		reviveCalled = true
		ref.mu.Lock()
		ref.Status = "idle"
		ref.mu.Unlock()
		rt.Registry.notify()
		return "Revived Worker1", nil
	}
	ref.mu.Unlock()
	revMsg, err := handlers.Revive("Worker1")
	if err != nil {
		t.Fatalf("Revive error: %v", err)
	}
	if !reviveCalled || !strings.Contains(revMsg, "Revived Worker1") {
		t.Fatalf("expected OnRevive to run, called=%v msg=%q", reviveCalled, revMsg)
	}
	if snap := surface.GetAgentHubSnapshot(); snap.Agents[0].Status != "idle" {
		t.Fatalf("expected status idle after revive, got %+v", snap.Agents[0])
	}

	// LoadTranscript returns entries.
	tr := handlers.LoadTranscript("Worker1")
	if len(tr) != 1 || tr[0].Text != "work" {
		t.Fatalf("unexpected transcript: %+v", tr)
	}

	// ClearHubIfActive ignores unrelated session, clears active session.
	rt.ClearHubIfActive("other-session")
	if snap := surface.GetAgentHubSnapshot(); snap.InspectableCount != 1 {
		t.Fatalf("unrelated session cleared hub: %+v", snap)
	}
	rt.ClearHubIfActive(sessID)
	if snap := surface.GetAgentHubSnapshot(); snap.InspectableCount != 0 {
		t.Fatalf("expected cleared snapshot after ClearHubIfActive, got %+v", snap)
	}

	mu.Lock()
	count := len(snapshots)
	mu.Unlock()
	if count < 4 {
		t.Fatalf("expected multiple live OnAgentHubChange deliveries, got %d", count)
	}
}

func TestAgentsSlashCommandNativeAndFallback(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
		ResetForTests()
	})
	surface.ResetForTests()

	rt := NewRuntime(DefaultConfig())
	ext := sdk.New("test-hub-cmd")
	RegisterWithRuntime(ext, rt)

	fh := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       t.TempDir(),
		SessionID: "sess-cmd",
		IsIdle:    true,
	})
	defer fh.stop()
	fh.fireEvent("session_start", nil)

	rt.Registry.Register(&AgentRef{
		ID:          "ScoutA",
		DisplayName: "scout",
		AgentType:   "scout",
		Kind:        "sub",
		ParentID:    MainAgentID,
		Status:      "idle",
		SessionID:   "sess-cmd",
		Model:       "anthropic/claude-haiku-4-5",
		Cost:        0.0042,
		DurationMs:  1200,
		Requests:    2,
		Tokens:      900,
		Messages: []map[string]any{
			{"role": "user", "content": "Find auth middleware"},
			{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Found in auth.go"}}},
		},
	})

	sessCtx := rt.sessionContextFor("sess-cmd", sdk.Context{})
	var zero sdk.Context
	if sessCtx == zero {
		t.Fatalf("expected bound session context")
	}

	// 1. Native opener handles `/agents` with no args.
	opened := 0
	surface.SetAgentHubOpener(func() { opened++ })
	if err := rt.handleAgentsCommand(sessCtx, ""); err != nil {
		t.Fatalf("handleAgentsCommand native: %v", err)
	}
	if opened != 1 {
		t.Fatalf("expected native opener to be called once, got %d", opened)
	}

	// 2. Without native opener, `/agents` renders summary via Notify (not SendMessage, so it does not pollute model context).
	surface.SetAgentHubOpener(nil)
	if err := rt.handleAgentsCommand(sessCtx, ""); err != nil {
		t.Fatalf("handleAgentsCommand fallback: %v", err)
	}
	fh.mu.Lock()
	sentCount := len(fh.sentMsgs)
	fh.mu.Unlock()
	if sentCount != 0 {
		t.Fatalf("expected 0 SendMessage calls (must not inject into model context), got %d", sentCount)
	}

	// 3. `/agents ScoutA` renders capped transcript via Notify even if native opener is registered.
	surface.SetAgentHubOpener(func() { opened++ })
	if err := rt.handleAgentsCommand(sessCtx, "ScoutA"); err != nil {
		t.Fatalf("handleAgentsCommand transcript: %v", err)
	}
	if opened != 1 {
		t.Fatalf("expected native opener NOT to be called for explicit agent ID")
	}
	fh.mu.Lock()
	sentCount = len(fh.sentMsgs)
	fh.mu.Unlock()
	if sentCount != 0 {
		t.Fatalf("expected 0 SendMessage calls for /agents ScoutA, got %d", sentCount)
	}

	// 4. `/agents Main` must be rejected (never dump parent session).
	rt.Registry.RegisterMain("sess-cmd", &AgentRef{
		ID:        MainAgentID,
		Kind:      "main",
		Status:    "running",
		SessionID: "sess-cmd",
		Messages:  []map[string]any{{"role": "user", "content": "TOP_SECRET_MAIN"}},
	})
	if err := rt.handleAgentsCommand(sessCtx, "Main"); err != nil {
		t.Fatalf("handleAgentsCommand Main: %v", err)
	}
	if entries := rt.LoadTranscriptEntries("sess-cmd", "Main"); len(entries) != 0 {
		t.Fatalf("expected LoadTranscriptEntries(Main) to return nil, got %+v", entries)
	}
	// Verify fallback formatter includes ScoutA and Found in auth.go.
	summary := formatAgentHubFallbackSummary(rt.BuildHubSnapshot("sess-cmd"))
	if !strings.Contains(summary, "ScoutA") || !strings.Contains(summary, "$0.0042") {
		t.Fatalf("expected fallback summary containing ScoutA and $0.0042, got %q", summary)
	}
	transcript := formatAgentTranscriptFallback("ScoutA", rt.LoadTranscriptEntries("sess-cmd", "ScoutA"))
	if !strings.Contains(transcript, "Found in auth.go") {
		t.Fatalf("expected fallback transcript containing Found in auth.go, got %q", transcript)
	}
}

func TestExecutorPopulatesAgentRefTelemetryAndHubSnapshot(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
		ResetForTests()
	})
	surface.ResetForTests()

	rt := NewRuntime(DefaultConfig())
	ext := sdk.New("test-hub-exec")
	RegisterWithRuntime(ext, rt)

	fh := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       t.TempDir(),
		SessionID: "sess-exec-hub",
		IsIdle:    true,
		ActiveModel: map[string]any{
			"provider":      "anthropic",
			"id":            "claude-sonnet-4-5",
			"contextWindow": 200000,
		},
		OnChild: func(h *fakeHost, child *fakeChildState, promptText string) error {
			h.emitChildEvent(child, map[string]any{
				"type":       "tool_execution_start",
				"toolCallId": "tc-1",
				"toolName":   "read",
				"args":       map[string]any{"path": "README.md", "i": "Reading README"},
			})
			h.emitChildEvent(child, map[string]any{
				"type":       "tool_execution_end",
				"toolCallId": "tc-1",
				"toolName":   "read",
				"isError":    false,
				"result":     map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello"}}},
			})
			msg := map[string]any{
				"role":       "assistant",
				"provider":   "anthropic",
				"model":      "claude-sonnet-4-5",
				"stopReason": "stop",
				"content":    []any{map[string]any{"type": "text", "text": "All done."}},
				"usage": map[string]any{
					"input":         100,
					"output":        50,
					"cacheWrite":    25,
					"totalTokens":   175,
					"contextTokens": 150,
					"cost":          map[string]any{"total": 0.0075},
				},
			}
			child.mu.Lock()
			child.messages = append(child.messages, map[string]any{"role": "user", "content": promptText}, msg)
			child.mu.Unlock()
			h.emitChildEvent(child, map[string]any{"type": "message_end", "message": msg})
			h.emitChildEvent(child, map[string]any{"type": "agent_end", "messages": []any{msg}})
			return nil
		},
	})
	defer fh.stop()
	fh.fireEvent("session_start", nil)

	sessCtx := rt.sessionContextFor("sess-exec-hub", sdk.Context{})
	res := RunSubagent(t.Context(), rt.Registry, RunRequest{
		SDK:             sessCtx,
		ParentSessionID: "sess-exec-hub",
		Cwd:             fh.opts.Cwd,
		ID:              "ScoutHub",
		Agent:           AgentDefinition{Name: "scout", SystemPrompt: "Be fast."},
		Assignment:      "Check README",
		KeepAlive:       false,
	})
	if res.ExitCode != 0 {
		t.Fatalf("RunSubagent failed: %+v", res)
	}

	snap := surface.GetAgentHubSnapshot()
	if snap.InspectableCount != 1 || snap.RunningCount != 0 {
		t.Fatalf("unexpected hub snapshot counts: %+v", snap)
	}
	item := snap.Agents[0]
	if item.ID != "ScoutHub" || item.AgentType != "scout" || item.Task != "Check README" {
		t.Fatalf("unexpected identity fields: %+v", item)
	}
	if item.Status != "parked" || !item.CanRevive {
		t.Fatalf("expected parked & revivable, got status=%q canRevive=%v", item.Status, item.CanRevive)
	}
	if item.Requests != 1 || item.Tools != 1 || item.Tokens != 175 || item.ContextTokens != 150 {
		t.Fatalf("unexpected metrics: %+v", item)
	}
	if diff := item.Cost - 0.0075; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("Cost = %f, want 0.0075", item.Cost)
	}
	if item.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("Model = %q, want anthropic/claude-sonnet-4-5", item.Model)
	}
	if len(snap.Activity) < 4 {
		t.Fatalf("expected spawn + tool start + tool end + response + status activities, got %d: %+v", len(snap.Activity), snap.Activity)
	}
}

func TestReviveConcurrentSerializationAndGuards(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	rt := NewRuntime(DefaultConfig())
	sessID := "sess-revive-race"
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32

	ref := &AgentRef{
		ID:        "Racer",
		Kind:      "sub",
		Status:    "parked",
		SessionID: sessID,
	}
	ref.OnRevive = func() (string, error) {
		ref.mu.Lock()
		if ref.reviving {
			ref.mu.Unlock()
			return "", os.ErrExist
		}
		ref.reviving = true
		ref.mu.Unlock()
		defer func() {
			ref.mu.Lock()
			ref.reviving = false
			ref.mu.Unlock()
		}()
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		ref.mu.Lock()
		ref.Status = "idle"
		ref.mu.Unlock()
		return "Revived Racer.", nil
	}
	rt.Registry.Register(ref)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := rt.ReviveAgent(sessID, "Racer")
		errs <- err
	}()
	<-entered
	// Second concurrent revive must fail immediately while first is in-flight.
	if _, err := rt.ReviveAgent(sessID, "Racer"); err == nil {
		t.Fatalf("expected second concurrent ReviveAgent to fail while first is reviving")
	}
	close(release)
	wg.Wait()
	if err := <-errs; err != nil {
		t.Fatalf("first ReviveAgent failed: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected OnRevive called once, got %d", calls.Load())
	}
}

func TestKillDuringStartAndQueuedJobBailout(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	rt := NewRuntime(DefaultConfig())
	sessID := "sess-kill-start"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pre-register a running ref and matching background agent job, then kill via Hub.
	rt.Registry.Register(&AgentRef{
		ID:        "QueuedSub",
		Kind:      "sub",
		Status:    "running",
		SessionID: sessID,
	})
	job := NewAgentJob("QueuedSub", "QueuedSub", cancel, AgentProgress{ID: "QueuedSub", Status: "running"})
	jobs.Add(sessID, job)
	t.Cleanup(func() { jobs.ClearSession(sessID) })

	if _, err := rt.KillHubAgent(sessID, "QueuedSub"); err != nil {
		t.Fatalf("KillHubAgent: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatalf("expected KillHubAgent to cancel the matching background agent job context")
	}

	// RunSubagent must bail immediately when the pre-registered ref is already aborted.
	res := RunSubagent(context.Background(), rt.Registry, RunRequest{
		ID:              "QueuedSub",
		ParentSessionID: sessID,
		Agent:           AgentDefinition{Name: "scout"},
		Assignment:      "should not run",
	})
	if !res.Aborted {
		t.Fatalf("expected RunSubagent to bail out as aborted when ref was pre-killed, got %+v", res)
	}
}

func TestBuildSessionEntriesFromMessagesAndWorktreeCanReviveFalse(t *testing.T) {
	entries := buildSessionEntriesFromMessages([]map[string]any{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "world"},
	})
	if len(entries) != 2 {
		t.Fatalf("expected 2 session entries, got %d", len(entries))
	}
	if entries[0]["type"] != "message" || entries[0]["id"] != "m1" || entries[0]["parentId"] != nil {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1]["parentId"] != "m1" || entries[1]["id"] != "m2" {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}

	// Worktree-isolated agent (no OnRevive installed) must have CanRevive == false.
	wtRef := &AgentRef{
		ID:       "Isolated",
		Kind:     "sub",
		Status:   "parked",
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	}
	if wtRef.CanRevive() {
		t.Fatalf("expected agent without OnRevive (e.g. worktree-isolated) to have CanRevive=false")
	}
}

func TestSendHubMessageAutoRevivesParkedAgent(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	rt := NewRuntime(DefaultConfig())
	sessID := "sess-auto-revive"
	var revived, woken atomic.Bool

	ref := &AgentRef{
		ID:        "Sleeper",
		Kind:      "sub",
		Status:    "parked",
		SessionID: sessID,
	}
	ref.OnRevive = func() (string, error) {
		revived.Store(true)
		ref.mu.Lock()
		ref.Status = "idle"
		ref.OnWake = func(msg IRCMessage) bool {
			woken.Store(msg.Body == "wake up")
			return true
		}
		ref.mu.Unlock()
		return "Revived Sleeper.", nil
	}
	rt.Registry.Register(ref)

	if err := rt.SendHubMessage(sessID, "Sleeper", "wake up"); err != nil {
		t.Fatalf("SendHubMessage: %v", err)
	}
	if !revived.Load() || !woken.Load() {
		t.Fatalf("expected parked agent to auto-revive and wake (revived=%v, woken=%v)", revived.Load(), woken.Load())
	}
}

func TestNestedGrandchildSubagentsInHubSnapshot(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	rt := NewRuntime(DefaultConfig())
	topSess := "sess-top"
	childSess := "sess-child-1"

	rt.Registry.RegisterChildSession(childSess, ChildSessionMeta{
		ParentSessionID: topSess,
		Depth:           1,
		AgentID:         "ParentSub",
	})
	rt.Registry.Register(&AgentRef{
		ID:             "ParentSub",
		Kind:           "sub",
		Status:         "running",
		SessionID:      topSess,
		ChildSessionID: childSess,
		Cost:           0.0100,
	})
	rt.Registry.Register(&AgentRef{
		ID:        "GrandchildSub",
		Kind:      "sub",
		ParentID:  "ParentSub",
		Status:    "running",
		SessionID: childSess,
		Cost:      0.0050,
	})
	rt.Registry.RecordActivity(childSess, surface.AgentHubActivity{
		AgentID: "GrandchildSub",
		Kind:    "tool",
		Title:   "read foo.go",
		Status:  "ok",
	})

	snap := rt.BuildHubSnapshot(topSess)
	if snap.InspectableCount != 2 || snap.RunningCount != 2 {
		t.Fatalf("expected 2 agents (parent + grandchild), got %+v", snap)
	}
	if snap.SubagentCost < 0.0149 || snap.SubagentCost > 0.0151 {
		t.Fatalf("expected rolled-up cost ~0.0150, got %.6f", snap.SubagentCost)
	}
	if len(snap.Activity) != 1 || snap.Activity[0].AgentID != "GrandchildSub" {
		t.Fatalf("expected grandchild activity merged into top hub, got %+v", snap.Activity)
	}
}

func TestFormatAgentTranscriptFallbackCapped(t *testing.T) {
	entries := make([]surface.AgentHubTranscriptEntry, 0, 100)
	big := strings.Repeat("x", 1000)
	for range 100 {
		entries = append(entries, surface.AgentHubTranscriptEntry{
			Role:       "tool",
			ToolName:   "bash",
			ToolOutput: big,
		})
	}
	rendered := formatAgentTranscriptFallback("BigAgent", entries)
	if len(rendered) > maxFallbackTranscriptBytes+256 {
		t.Fatalf("expected capped fallback transcript, got %d bytes", len(rendered))
	}
	if !strings.Contains(rendered, "history://BigAgent") {
		t.Fatalf("expected history:// pointer in capped fallback transcript")
	}
}

func TestSerializedHubPublishAndClearNoResurrection(t *testing.T) {
	t.Cleanup(func() {
		resetHubBindingsForTests()
		surface.ResetForTests()
	})
	rt := NewRuntime(DefaultConfig())
	sessID := "sess-serial"
	rt.BindHubSession(sessID)
	rt.Registry.Register(&AgentRef{
		ID:        "Worker",
		Kind:      "sub",
		Status:    "running",
		SessionID: sessID,
	})

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.PublishHub(sessID)
		}()
	}
	wg.Wait()
	rt.ClearHubIfActive(sessID)
	if got := surface.GetAgentHubSnapshot(); got.InspectableCount != 0 {
		t.Fatalf("expected cleared hub snapshot to stay empty, got %+v", got)
	}
}
