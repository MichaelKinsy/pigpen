package checkpoint

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

func decodeBoundaryEntries(t *testing.T, raw json.RawMessage) (entries []map[string]any, cont bool) {
	t.Helper()
	var env struct {
		Result struct {
			Continue bool             `json:"continue"`
			Entries  []map[string]any `json:"entries"`
		} `json:"_pigBoundaryResult"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal boundary result (%s): %v", string(raw), err)
	}
	return env.Result.Entries, env.Result.Continue
}

func applyDraftsToHost(h *fakeHost, drafts []map[string]any) {
	for _, d := range drafts {
		cp := make(map[string]any, len(d))
		for k, v := range d {
			cp[k] = v
		}
		h.appendEntry(cp)
	}
}

func TestCheckpointRebuildsActiveHistoryAtCheckpointToolResult(t *testing.T) {
	// upstream: packages/coding-agent/test/agent-session-checkpoint-rewind-branch.test.ts:
	// "rebuilds active history at the checkpoint before the post-rewind assistant turn"
	// "tracks direct checkpoint and rewind calls through the session lifecycle"
	// "shows a transient checkpoint-active reminder that is branch-cut away on rewind"
	prevEnabled := Enabled
	Enabled = func(_ sdk.Context, key string, _ bool) bool {
		return key == SettingKey
	}
	t.Cleanup(func() { Enabled = prevEnabled })

	ext := sdk.New("checkpoint-test")
	Register(ext)

	h := startHost(t, ext, hostOptions{
		cwd:         t.TempDir(),
		sessionID:   "sess-1",
		activeTools: []string{"read", "bash"},
	})
	defer h.stop()

	// Verify both tools are registered with Exposure: codemode and DefaultActive: false.
	if len(h.tools) != 2 {
		t.Fatalf("expected 2 registered tools, got %d", len(h.tools))
	}
	for _, tool := range h.tools {
		if tool.Exposure != sdk.ToolExposureCodemode {
			t.Errorf("tool %s expected Exposure=%q, got %q", tool.Name, sdk.ToolExposureCodemode, tool.Exposure)
		}
		if tool.DefaultActive == nil || *tool.DefaultActive {
			t.Errorf("tool %s expected DefaultActive=false, got %v", tool.Name, tool.DefaultActive)
		}
	}

	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	// checkpoint/rewind do not touch SetActiveTools directly; toolcmd owns mounting/activation.
	if len(h.callsTo("setActiveTools")) != 0 {
		t.Fatalf("expected checkpoint session_start not to call setActiveTools directly")
	}
	// Rewind without active checkpoint fails with omp's exact error.
	_, errText := h.callTool("rewind", map[string]any{"report": "early"})
	if errText != "No active checkpoint. Create a checkpoint before calling rewind." {
		t.Fatalf("got %q, want omp no-active-checkpoint error", errText)
	}

	// Realistic PiG persistence order: user message -> assistant message (persisted
	// at message_end BEFORE tools run) -> tool executes -> toolResult persisted -> turn_end.
	h.appendEntry(map[string]any{
		"id":      "e-user",
		"type":    "message",
		"message": map[string]any{"role": "user", "content": "investigate auth bug"},
	})
	h.appendEntry(map[string]any{
		"id":      "e-asst-cp",
		"type":    "message",
		"message": map[string]any{"role": "assistant", "content": "calling checkpoint"},
	})

	cpRaw, errText := h.callToolWithID("checkpoint", "call-cp-1", map[string]any{"goal": "find auth bug"})
	if errText != "" {
		t.Fatalf("checkpoint failed: %s", errText)
	}
	var cpRes struct {
		Content string                `json:"content"`
		Details CheckpointToolDetails `json:"details"`
	}
	if err := json.Unmarshal(cpRaw, &cpRes); err != nil {
		t.Fatalf("unmarshal checkpoint result: %v", err)
	}
	if cpRes.Content != "Checkpoint: find auth bug\nFinish exploration and formulate findings." {
		t.Fatalf("unexpected checkpoint content: %q", cpRes.Content)
	}
	if cpRes.Details.Goal != "find auth bug" || cpRes.Details.StartedAt == "" {
		t.Fatalf("unexpected checkpoint details: %+v", cpRes.Details)
	}

	// Verify steering notice uses omp's customType checkpoint-active-reminder with {goal, startedAt}.
	sendCalls := h.callsTo("sendMessage")
	if len(sendCalls) != 1 {
		t.Fatalf("expected 1 steering sendMessage call, got %d", len(sendCalls))
	}
	msgMap, _ := sendCalls[0].Args["message"].(map[string]any)
	if got := msgMap["customType"]; got != CheckpointActiveReminderType {
		t.Fatalf("expected customType=%q, got %v (args=%+v)", CheckpointActiveReminderType, got, sendCalls[0].Args)
	}
	if details, _ := msgMap["details"].(map[string]any); details["goal"] != "find auth bug" {
		t.Fatalf("expected reminder details.goal, got %+v", details)
	}

	// Persist the checkpoint toolResult AFTER tool execution (real host ordering).
	h.appendEntry(map[string]any{
		"id":   "e-tr-cp",
		"type": "message",
		"message": map[string]any{
			"role":       "toolResult",
			"toolName":   "checkpoint",
			"toolCallId": "call-cp-1",
			"content":    cpRes.Content,
			"details":    map[string]any{"goal": cpRes.Details.Goal, "startedAt": cpRes.Details.StartedAt},
		},
	})
	// Turn end of the checkpoint turn backfills CheckpointEntryID = "e-tr-cp".
	cpTurnRaw, errText := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":        "completed",
		"messageEntryId": "e-asst-cp",
		"toolResults": []any{
			map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": "call-cp-1"},
		},
		"toolResultEntryIds": []any{"e-tr-cp"},
	})
	if errText != "" {
		t.Fatalf("checkpoint turn_end failed: %s", errText)
	}
	cpTurnDrafts, _ := decodeBoundaryEntries(t, cpTurnRaw)
	applyDraftsToHost(h, cpTurnDrafts)

	// Add the steering reminder + exploration entries + a non-editable bashExecution entry.
	h.appendEntry(map[string]any{
		"id":         "e-reminder",
		"type":       "custom_message",
		"customType": CheckpointActiveReminderType,
		"content":    checkpointActiveNotice,
	})
	h.appendEntry(map[string]any{
		"id":      "e-bash-exec",
		"type":    "message",
		"message": map[string]any{"role": "bashExecution", "content": "user !ls"},
	})
	h.appendEntry(map[string]any{
		"id":      "e-explore-asst",
		"type":    "message",
		"message": map[string]any{"role": "assistant", "content": "running grep"},
	})
	h.appendEntry(map[string]any{
		"id":      "e-explore-tr",
		"type":    "message",
		"message": map[string]any{"role": "toolResult", "toolName": "grep", "content": "auth.go:42"},
	})

	// Second checkpoint while active fails.
	_, errText = h.callTool("checkpoint", map[string]any{"goal": "nested"})
	if errText != "Checkpoint already active." {
		t.Fatalf("got %q, want Checkpoint already active.", errText)
	}

	// Settle while checkpoint is active forces continue=true with reminder.
	settleRaw, errText := h.emitEvent(sdk.EventAgentBeforeSettle, map[string]any{})
	if errText != "" {
		t.Fatalf("agent_before_settle error: %s", errText)
	}
	settleEntries, settleCont := decodeBoundaryEntries(t, settleRaw)
	if !settleCont || len(settleEntries) != 1 {
		t.Fatalf("expected continue=true and 1 reminder entry, got cont=%v entries=%v", settleCont, settleEntries)
	}

	// Empty rewind report fails.
	_, errText = h.callTool("rewind", map[string]any{"report": "   "})
	if errText != "Report cannot be empty." {
		t.Fatalf("got %q, want Report cannot be empty.", errText)
	}

	// Rewind turn: assistant message persisted before rewind tool executes.
	h.appendEntry(map[string]any{
		"id":      "e-asst-rw",
		"type":    "message",
		"message": map[string]any{"role": "assistant", "content": "calling rewind"},
	})
	rwRaw, errText := h.callToolWithID("rewind", "call-rw-1", map[string]any{"report": "Found token expiry bug in auth.go:42"})
	if errText != "" {
		t.Fatalf("rewind failed: %s", errText)
	}
	var rwRes struct {
		Content string            `json:"content"`
		Details RewindToolDetails `json:"details"`
	}
	if err := json.Unmarshal(rwRaw, &rwRes); err != nil {
		t.Fatalf("unmarshal rewind result: %v", err)
	}
	if !rwRes.Details.Rewound || rwRes.Details.Report != "Found token expiry bug in auth.go:42" {
		t.Fatalf("unexpected rewind details: %+v", rwRes.Details)
	}
	h.appendEntry(map[string]any{
		"id":   "e-tr-rw",
		"type": "message",
		"message": map[string]any{
			"role":       "toolResult",
			"toolName":   "rewind",
			"toolCallId": "call-rw-1",
			"content":    rwRes.Content,
		},
	})

	// Aborted turn_end must NOT apply rewind.
	abortRaw, errText := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "aborted",
		"messageEntryId":     "e-asst-rw",
		"toolResultEntryIds": []any{"e-tr-rw"},
	})
	if errText != "" {
		t.Fatalf("aborted turn_end error: %s", errText)
	}
	if abortEntries, _ := decodeBoundaryEntries(t, abortRaw); len(abortEntries) != 0 {
		t.Fatalf("expected no drafts on aborted turn_end, got %v", abortEntries)
	}

	// Completed turn_end omits entries strictly after e-tr-cp, skipping non-editable bashExecution.
	turnRaw, errText := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-asst-rw",
		"toolResultEntryIds": []any{"e-tr-rw"},
	})
	if errText != "" {
		t.Fatalf("turn_end error: %s", errText)
	}
	turnEntries, _ := decodeBoundaryEntries(t, turnRaw)

	var omitted []string
	var sawReport bool
	for _, entry := range turnEntries {
		switch entry["type"] {
		case "context_edit":
			if tid, _ := entry["targetId"].(string); tid != "" {
				omitted = append(omitted, tid)
			}
		case "custom_message":
			if entry["customType"] == RewindReportMessageType {
				sawReport = true
			}
		}
	}
	if !sawReport {
		t.Fatalf("expected rewind-report custom_message in turn_end entries")
	}
	// Crucially: e-user, e-asst-cp, e-tr-cp MUST stay in context, and e-bash-exec MUST NOT be targeted!
	wantOmitted := []string{"e-reminder", "e-explore-asst", "e-explore-tr", "e-asst-rw", "e-tr-rw"}
	if !slices.Equal(omitted, wantOmitted) {
		t.Fatalf("omitted IDs = %v, want %v", omitted, wantOmitted)
	}

	// Persist the drafts on the host branch so awaitingCommit sees the rewind-report.
	applyDraftsToHost(h, turnEntries)

	// Calling rewind again after completion returns omp's already-completed error.
	// upstream: "tells the model to continue when rewind is repeated after completion"
	_, errText = h.callTool("rewind", map[string]any{"report": "duplicate"})
	if !strings.Contains(errText, "Checkpoint already completed") {
		t.Fatalf("expected already-completed error, got %q", errText)
	}
}

func TestCheckpointTreeNavigationDoesNotResurrectOrWipeFromRoot(t *testing.T) {
	// upstream: packages/coding-agent/test/agent-session-checkpoint-rewind-branch.test.ts:
	// "rehydrates completed rewind state from the branched path"
	// Plus regression test for /tree navigating before an active checkpoint.
	ext := sdk.New("checkpoint-tree-test")
	Register(ext)

	startedAt := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	rewoundAt := time.Date(2026, 5, 1, 10, 5, 0, 0, time.UTC).Format(time.RFC3339Nano)

	// Build a session tree with two branches:
	//   root: e-user (parentId: nil)
	//   abandoned branch A: e-cp-tr -> e-cp-state (active checkpoint on abandoned branch)
	//   abandoned branch B: e-rw-report (completed rewind on another abandoned branch)
	//   active leaf: e-user (user ran /tree back to root before the checkpoint!)
	h := startHost(t, ext, hostOptions{
		cwd:       t.TempDir(),
		sessionID: "sess-tree",
		leafID:    "e-user",
		entries: []map[string]any{
			{
				"id":       "e-user",
				"parentId": nil,
				"type":     "message",
				"message":  map[string]any{"role": "user", "content": "original prompt"},
			},
			{
				"id":       "e-cp-tr",
				"parentId": "e-user",
				"type":     "message",
				"message": map[string]any{
					"role":     "toolResult",
					"toolName": "checkpoint",
					"details":  map[string]any{"goal": "abandoned", "startedAt": startedAt},
				},
			},
			{
				"id":         "e-rw-report",
				"parentId":   "e-user",
				"type":       "custom_message",
				"customType": RewindReportMessageType,
				"content":    renderRewindReport("report on sibling branch"),
				"details":    map[string]any{"report": "report on sibling branch", "startedAt": startedAt, "rewoundAt": rewoundAt},
			},
		},
	})
	defer h.stop()

	// session_tree at e-user must NOT resurrect either the abandoned active checkpoint
	// or the abandoned sibling branch's completed rewind.
	h.emitEvent(sdk.EventSessionTree, map[string]any{})

	settleRaw, _ := h.emitEvent(sdk.EventAgentBeforeSettle, map[string]any{})
	_, cont := decodeBoundaryEntries(t, settleRaw)
	if cont {
		t.Fatalf("expected agent_before_settle NOT to continue after navigating before checkpoint")
	}

	_, errText := h.callTool("rewind", map[string]any{"report": "should fail"})
	if errText != "No active checkpoint. Create a checkpoint before calling rewind." {
		t.Fatalf("expected No active checkpoint (not resurrected completed state), got %q", errText)
	}

	// Navigating onto the branch with e-cp-tr rehydrates the active checkpoint.
	// upstream: "rehydrates active checkpoint state when resuming a session with no rewind yet"
	h.setLeaf("e-cp-tr")
	h.emitEvent(sdk.EventSessionTree, map[string]any{})
	_, errText = h.callTool("checkpoint", map[string]any{"goal": "duplicate"})
	if errText != "Checkpoint already active." {
		t.Fatalf("expected Checkpoint already active after navigating onto active checkpoint branch, got %q", errText)
	}

	// Navigating onto the branch with e-rw-report rehydrates the completed rewind state.
	h.setLeaf("e-rw-report")
	h.emitEvent(sdk.EventSessionTree, map[string]any{})
	_, errText = h.callTool("rewind", map[string]any{"report": "again"})
	if !strings.Contains(errText, "Checkpoint already completed") {
		t.Fatalf("expected completed rewind state on branched path, got %q", errText)
	}
}

func TestCheckpointRestoresActiveStateIfBoundaryDraftsRejected(t *testing.T) {
	ext := sdk.New("checkpoint-reject-test")
	Register(ext)

	h := startHost(t, ext, hostOptions{
		cwd:       t.TempDir(),
		sessionID: "sess-reject",
	})
	defer h.stop()

	h.appendEntry(map[string]any{"id": "e-user", "type": "message", "message": map[string]any{"role": "user", "content": "hi"}})
	h.appendEntry(map[string]any{"id": "e-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "cp"}})
	_, errText := h.callToolWithID("checkpoint", "c1", map[string]any{"goal": "g"})
	if errText != "" {
		t.Fatalf("checkpoint failed: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-cp-tr", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": "c1"}})
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-asst",
		"toolResults":        []any{map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": "c1"}},
		"toolResultEntryIds": []any{"e-cp-tr"},
	})

	_, errText = h.callToolWithID("rewind", "c2", map[string]any{"report": "findings"})
	if errText != "" {
		t.Fatalf("rewind failed: %s", errText)
	}
	// Simulate host rejecting turn_end drafts (do NOT append rewind-report to host).
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-rw-asst",
		"toolResultEntryIds": []any{"e-rw-tr"},
	})

	// Because rewind-report was never persisted, checkpoint MUST still be active
	// (calling rewind again succeeds instead of claiming "already completed").
	_, errText = h.callToolWithID("rewind", "c3", map[string]any{"report": "retry findings"})
	if errText != "" {
		t.Fatalf("expected checkpoint to stay active when host rejected drafts, got error: %s", errText)
	}
}

func TestCheckpointAcrossMultipleCyclesAndNewSession(t *testing.T) {
	// upstream: packages/coding-agent/test/agent-session-checkpoint-rewind-branch.test.ts:
	// "carries each rewind report into model context exactly once across checkpoint cycles"
	// "clears completed rewind state when starting a new session"
	ext := sdk.New("checkpoint-cycles-test")
	Register(ext)

	h := startHost(t, ext, hostOptions{
		cwd:       t.TempDir(),
		sessionID: "sess-cycles",
	})
	defer h.stop()

	h.appendEntry(map[string]any{"id": "e-u1", "type": "message", "message": map[string]any{"role": "user", "content": "task"}})

	for cycle := 1; cycle <= 2; cycle++ {
		asstID := "e-cp-asst-" + string(rune('0'+cycle))
		trID := "e-cp-tr-" + string(rune('0'+cycle))
		callID := "cp-" + string(rune('0'+cycle))
		h.appendEntry(map[string]any{"id": asstID, "type": "message", "message": map[string]any{"role": "assistant", "content": "cp"}})
		if _, errText := h.callToolWithID("checkpoint", callID, map[string]any{"goal": "cycle"}); errText != "" {
			t.Fatalf("cycle %d checkpoint: %s", cycle, errText)
		}
		h.appendEntry(map[string]any{"id": trID, "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": callID}})
		raw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
			"outcome":            "completed",
			"messageEntryId":     asstID,
			"toolResults":        []any{map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": callID}},
			"toolResultEntryIds": []any{trID},
		})
		drafts, _ := decodeBoundaryEntries(t, raw)
		applyDraftsToHost(h, drafts)

		rwAsst := "e-rw-asst-" + string(rune('0'+cycle))
		rwTr := "e-rw-tr-" + string(rune('0'+cycle))
		h.appendEntry(map[string]any{"id": rwAsst, "type": "message", "message": map[string]any{"role": "assistant", "content": "rw"}})
		if _, errText := h.callToolWithID("rewind", "rw", map[string]any{"report": "report " + string(rune('0'+cycle))}); errText != "" {
			t.Fatalf("cycle %d rewind: %s", cycle, errText)
		}
		h.appendEntry(map[string]any{"id": rwTr, "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "rewind", "toolCallId": "rw"}})
		rwRaw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
			"outcome":            "completed",
			"messageEntryId":     rwAsst,
			"toolResultEntryIds": []any{rwTr},
		})
		rwDrafts, _ := decodeBoundaryEntries(t, rwRaw)
		applyDraftsToHost(h, rwDrafts)

		reportCount := 0
		for _, d := range rwDrafts {
			if d["type"] == "custom_message" && d["customType"] == RewindReportMessageType {
				reportCount++
			}
		}
		if reportCount != 1 {
			t.Fatalf("cycle %d emitted %d rewind-report drafts, want 1", cycle, reportCount)
		}
	}

	// Starting a new empty session clears completed rewind state (host calls lifecycle.End).
	lifecycle.End("sess-cycles")
	h.mu.Lock()
	h.opts.sessionID = "sess-cycles-new"
	h.opts.entries = nil
	h.opts.leafID = ""
	h.mu.Unlock()
	h.setLeaf("")
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	_, errText := h.callTool("rewind", map[string]any{"report": "after new session"})
	if errText != "No active checkpoint. Create a checkpoint before calling rewind." {
		t.Fatalf("expected clean state after new session, got %q", errText)
	}
}

func TestCheckpointMountedThroughToolcmd(t *testing.T) {
	// upstream: packages/coding-agent/test/agent-session-checkpoint-rewind-branch.test.ts:
	// "tracks checkpoint and rewind through bash tool commands"
	// "rehydrates an active checkpoint from a bash tool command after branching and resume"
	ResetForTests()
	toolcmd.ResetForTests()
	prevEnabled := Enabled
	prevToolcmdEnabled := toolcmd.Enabled
	commandsOn := true
	Enabled = func(_ sdk.Context, key string, _ bool) bool {
		return key == SettingKey
	}
	toolcmd.Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "tools.commands" {
			return commandsOn
		}
		return def
	}
	t.Cleanup(func() {
		Enabled = prevEnabled
		toolcmd.Enabled = prevToolcmdEnabled
		toolcmd.ResetForTests()
		ResetForTests()
	})

	ext := sdk.New("checkpoint-toolcmd-test")
	Register(ext)
	toolcmd.Register(ext)
	var capturedCtx sdk.Context
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedCtx = ctx
		return nil, nil
	})
	// Register a bash transport tool that runs toolcmd.TryDirect inside its tool
	// handler, exercising the real Context.ExecuteTool -> fakeHost exposure rule
	// (direct tools callable only while active; codemode tools always callable).
	ext.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "Bash",
		Description:    "Shell",
		Parameters:     sdk.Schema{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
		PrepareLoadout: toolcmd.PrepareLoadout,
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			cmd, _ := params["command"].(string)
			out, rc, handled := toolcmd.TryDirect(ctx, cmd)
			if !handled {
				return sdk.ToolResult{Content: "not handled", IsError: true}, nil
			}
			return sdk.ToolResult{Content: out, IsError: rc != 0}, nil
		},
	})
	// Also register an inactive direct tool to verify fakeHost enforces the direct rule.
	defaultOff := false
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "inactive_direct",
		Label:         "Inactive Direct",
		Description:   "Should not be callable while inactive",
		Parameters:    sdk.Schema{"type": "object"},
		DefaultActive: &defaultOff,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			return "should not run", nil
		},
	})

	h := startHost(t, ext, hostOptions{
		cwd:         t.TempDir(),
		sessionID:   "sess-toolcmd",
		activeTools: []string{"bash"},
	})
	defer h.stop()

	// session_start syncs toolcmd: checkpoint & rewind mount as bash commands,
	// stay in activeTools for direct fallback dispatch (omp resolveFallbackTool),
	// appear in toolcmd.MountedNames, are omitted from toolcmd.AdvertisedTools,
	// and are hidden from model declarations via toolcmd.PrepareLoadout.
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	active := h.getActiveTools()
	if !slices.Contains(active, "checkpoint") || !slices.Contains(active, "rewind") {
		t.Fatalf("expected mounted checkpoint/rewind to stay in activeTools for direct fallback, got %v", active)
	}
	mounted := toolcmd.MountedNames(capturedCtx)
	if !slices.Contains(mounted, "checkpoint") || !slices.Contains(mounted, "rewind") {
		t.Fatalf("expected checkpoint and rewind in toolcmd.MountedNames, got %v", mounted)
	}
	advertised := toolcmd.AdvertisedTools(capturedCtx, active)
	if slices.Contains(advertised, "checkpoint") || slices.Contains(advertised, "rewind") {
		t.Fatalf("expected mounted checkpoint/rewind omitted from toolcmd.AdvertisedTools, got %v", advertised)
	}
	loadout := sdk.ToolLoadout{
		Declared: []sdk.AgentTool{
			{Name: "bash"},
			{Name: "checkpoint"},
			{Name: "rewind"},
		},
		GetExposure: func(name string) sdk.ToolExposure {
			if tl, ok := h.tools[name]; ok {
				return tl.Exposure
			}
			return sdk.ToolExposureDirect
		},
	}
	changes := toolcmd.PrepareLoadout(loadout)
	if changes == nil || !slices.Contains(changes.HiddenDeclarations, "checkpoint") || !slices.Contains(changes.HiddenDeclarations, "rewind") {
		t.Fatalf("expected toolcmd.PrepareLoadout to hide checkpoint and rewind, got %+v", changes)
	}
	if h.isCallableTool("inactive_direct") {
		t.Fatalf("expected inactive direct tool to be rejected by host exposure rule")
	}
	if !h.isCallableTool("checkpoint") || !h.isCallableTool("rewind") {
		t.Fatalf("expected codemode checkpoint/rewind to remain callable through ExecuteTool while mounted")
	}
	// Run mounted `checkpoint` and `rewind` through bash -> toolcmd.TryDirect -> Context.ExecuteTool.
	cpRaw, errText := h.callToolWithID("bash", "call-bash-1", map[string]any{"command": "checkpoint --goal 'bash exploration'"})
	if errText != "" {
		t.Fatalf("mounted bash checkpoint failed: %s", errText)
	}
	var cpOut struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(cpRaw, &cpOut)
	if cpOut.IsError || !strings.Contains(cpOut.Content, "Checkpoint: bash exploration") {
		t.Fatalf("unexpected mounted checkpoint output: %+v", cpOut)
	}

	rwRaw, errText := h.callToolWithID("bash", "call-bash-2", map[string]any{"command": "rewind --report 'bash findings'"})
	if errText != "" {
		t.Fatalf("mounted bash rewind failed: %s", errText)
	}
	var rwOut struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(rwRaw, &rwOut)
	if rwOut.IsError || !strings.Contains(rwOut.Content, "Rewind requested.") {
		t.Fatalf("unexpected mounted rewind output: %+v", rwOut)
	}

	// Direct calls to mounted `checkpoint` and `rewind` also dispatch (omp resolveFallbackTool).
	// Re-emit session_start on the empty log to clear the in-memory checkpoint before the direct cycle.
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	cpDirectRaw, errText := h.callToolWithID("checkpoint", "call-direct-cp", map[string]any{"goal": "direct exploration"})
	if errText != "" {
		t.Fatalf("direct call to mounted checkpoint failed: %s", errText)
	}
	var cpDirectOut struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(cpDirectRaw, &cpDirectOut)
	if cpDirectOut.IsError || !strings.Contains(cpDirectOut.Content, "Checkpoint: direct exploration") {
		t.Fatalf("unexpected direct checkpoint output: %+v", cpDirectOut)
	}
	rwDirectRaw, errText := h.callToolWithID("rewind", "call-direct-rw", map[string]any{"report": "direct findings"})
	if errText != "" {
		t.Fatalf("direct call to mounted rewind failed: %s", errText)
	}
	var rwDirectOut struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(rwDirectRaw, &rwDirectOut)
	if rwDirectOut.IsError || !strings.Contains(rwDirectOut.Content, "Rewind requested.") {
		t.Fatalf("unexpected direct rewind output: %+v", rwDirectOut)
	}
	// Verify branch replay recognizes a bash toolResult carrying toolcmd dispatch metadata.
	startedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	h.appendEntry(map[string]any{
		"id":   "e-bash-cp",
		"type": "message",
		"message": map[string]any{
			"role":     "toolResult",
			"toolName": "bash",
			"details": map[string]any{
				"toolCommands": []any{
					map[string]any{
						"tool":     "checkpoint",
						"mode":     "execute",
						"exitCode": 0,
						"inner":    map[string]any{"goal": "from bash", "startedAt": startedAt},
					},
				},
			},
		},
	})
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	_, errText = h.callTool("checkpoint", map[string]any{"goal": "second"})
	if errText != "Checkpoint already active." {
		t.Fatalf("expected active checkpoint rehydrated from bash toolCommand dispatch, got %q", errText)
	}

	// When tools.commands is off, checkpoint and rewind are advertised top-level and not hidden.
	commandsOn = false
	h.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	activeOff := h.getActiveTools()
	if !slices.Contains(activeOff, "checkpoint") || !slices.Contains(activeOff, "rewind") {
		t.Fatalf("expected checkpoint/rewind active when tools.commands is off, got %v", activeOff)
	}
	mountedOff := toolcmd.MountedNames(capturedCtx)
	if slices.Contains(mountedOff, "checkpoint") || slices.Contains(mountedOff, "rewind") {
		t.Fatalf("expected checkpoint/rewind not mounted when tools.commands is off, got %v", mountedOff)
	}
	advertisedOff := toolcmd.AdvertisedTools(capturedCtx, activeOff)
	if !slices.Contains(advertisedOff, "checkpoint") || !slices.Contains(advertisedOff, "rewind") {
		t.Fatalf("expected checkpoint/rewind advertised top-level when tools.commands is off, got %v", advertisedOff)
	}
	if changesOff := toolcmd.PrepareLoadout(loadout); changesOff != nil && (slices.Contains(changesOff.HiddenDeclarations, "checkpoint") || slices.Contains(changesOff.HiddenDeclarations, "rewind")) {
		t.Fatalf("expected checkpoint/rewind not hidden by PrepareLoadout when tools.commands is off, got %+v", changesOff)
	}
}
func TestCheckpointMultiSessionLifecycleAndDoubleRegister(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)

	ext1 := sdk.New("cp-ext-1")
	ext2 := sdk.New("cp-ext-2")
	Register(ext1)
	Register(ext2)

	h1 := startHost(t, ext1, hostOptions{cwd: t.TempDir(), sessionID: "sess-A"})
	defer h1.stop()
	h2 := startHost(t, ext2, hostOptions{cwd: t.TempDir(), sessionID: "sess-B"})
	defer h2.stop()

	// Start checkpoints in both sessions concurrently.
	if _, errText := h1.callTool("checkpoint", map[string]any{"goal": "goal A"}); errText != "" {
		t.Fatalf("sess-A checkpoint: %s", errText)
	}
	if _, errText := h2.callTool("checkpoint", map[string]any{"goal": "goal B"}); errText != "" {
		t.Fatalf("sess-B checkpoint: %s", errText)
	}

	// Ending sess-A via lifecycle.End (even after Register ran twice) releases
	// sess-A only; calling End twice is safe; sess-B remains active and usable.
	lifecycle.End("sess-A")
	lifecycle.End("sess-A")

	if _, errText := h1.callTool("rewind", map[string]any{"report": "after end"}); !strings.Contains(errText, "No active checkpoint") {
		t.Fatalf("expected sess-A state cleared after lifecycle.End, got %q", errText)
	}
	if _, errText := h2.callTool("rewind", map[string]any{"report": "findings B"}); errText != "" {
		t.Fatalf("expected sess-B to remain active after sess-A ended, got %q", errText)
	}
}
func TestCheckpointNoSessionShutdownHookRegistered(t *testing.T) {
	ext := sdk.New("cp-no-shutdown-hook")
	Register(ext)
	h := startHost(t, ext, hostOptions{cwd: t.TempDir(), sessionID: "sess-no-sd"})
	defer h.stop()
	if ids := h.handlers[sdk.EventSessionShutdown]; len(ids) != 0 {
		t.Fatalf("checkpoint must not register its own session_shutdown hook (lifecycle.OnEnd only), got %v", ids)
	}
}

func TestCheckpointMountedTwoResultsTurnWithoutAndWithToolCommands(t *testing.T) {
	// Regression test for N1:
	// (a) When a mounted bash checkpoint runs beside a sibling `read` in the same
	//     turn WITHOUT `details.toolCommands`, onTurnEnd must resolve the boundary
	//     at that turn's last tool-result entry (never scanning an older rewound
	//     cycle and never retrying on a later turn).
	// (b) When an aborted checkpoint turn ends without resolving the boundary, a
	//     subsequent rewind turn MUST NOT treat its own single tool result as the
	//     checkpoint boundary.
	// (c) When `details.toolCommands` IS stamped on the bash toolResult, onTurnEnd
	//     picks that exact bash entry even when another result follows it.
	ResetForTests()
	t.Cleanup(ResetForTests)

	ext := sdk.New("cp-n1-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{cwd: t.TempDir(), sessionID: "sess-n1"})
	defer h.stop()

	h.appendEntry(map[string]any{"id": "e-u1", "type": "message", "message": map[string]any{"role": "user", "content": "start"}})

	// First completed cycle (leaves an older checkpoint toolResult on the branch).
	h.appendEntry(map[string]any{"id": "e-old-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "old cp"}})
	if _, errText := h.callToolWithID("checkpoint", "old-cp", map[string]any{"goal": "old"}); errText != "" {
		t.Fatalf("old checkpoint: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-old-cp-tr", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": "old-cp"}})
	raw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-old-asst",
		"toolResults":        []any{map[string]any{"role": "toolResult", "toolName": "checkpoint", "toolCallId": "old-cp"}},
		"toolResultEntryIds": []any{"e-old-cp-tr"},
	})
	drafts, _ := decodeBoundaryEntries(t, raw)
	applyDraftsToHost(h, drafts)
	h.appendEntry(map[string]any{"id": "e-old-rw-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "old rw"}})
	if _, errText := h.callToolWithID("rewind", "old-rw", map[string]any{"report": "old report"}); errText != "" {
		t.Fatalf("old rewind: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-old-rw-tr", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "rewind", "toolCallId": "old-rw"}})
	rwRaw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-old-rw-asst",
		"toolResultEntryIds": []any{"e-old-rw-tr"},
	})
	rwDrafts, _ := decodeBoundaryEntries(t, rwRaw)
	applyDraftsToHost(h, rwDrafts)

	// Case (a): second cycle via mounted bash call (callID differs from bash callID,
	// and bash toolResult has NO details.toolCommands) alongside a sibling `read`.
	h.appendEntry(map[string]any{"id": "e-keep-msg", "type": "message", "message": map[string]any{"role": "user", "content": "keep this"}})
	h.appendEntry(map[string]any{"id": "e-asst-2", "type": "message", "message": map[string]any{"role": "assistant", "content": "bash+read"}})
	if _, errText := h.callToolWithID("checkpoint", "call-bash-sub/1", map[string]any{"goal": "mounted no-stamp"}); errText != "" {
		t.Fatalf("cycle 2 checkpoint: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-bash-tr-2", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "bash", "toolCallId": "call-bash-2", "content": "Checkpoint: mounted no-stamp"}})
	h.appendEntry(map[string]any{"id": "e-read-tr-2", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "call-read-2", "content": "file contents"}})
	cp2Raw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":        "completed",
		"messageEntryId": "e-asst-2",
		"toolResults": []any{
			map[string]any{"role": "toolResult", "toolName": "bash", "toolCallId": "call-bash-2"},
			map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "call-read-2"},
		},
		"toolResultEntryIds": []any{"e-bash-tr-2", "e-read-tr-2"},
	})
	cp2Drafts, _ := decodeBoundaryEntries(t, cp2Raw)
	applyDraftsToHost(h, cp2Drafts)

	// Exploration after turn 2, then rewind.
	h.appendEntry(map[string]any{"id": "e-exp-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "exploring"}})
	h.appendEntry(map[string]any{"id": "e-exp-tr", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "grep", "content": "match"}})
	h.appendEntry(map[string]any{"id": "e-rw2-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "rewinding"}})
	if _, errText := h.callToolWithID("rewind", "rw-2", map[string]any{"report": "cycle 2 report"}); errText != "" {
		t.Fatalf("cycle 2 rewind: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-rw2-tr", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "rewind", "toolCallId": "rw-2"}})
	rw2Raw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-rw2-asst",
		"toolResultEntryIds": []any{"e-rw2-tr"},
	})
	rw2Drafts, _ := decodeBoundaryEntries(t, rw2Raw)
	applyDraftsToHost(h, rw2Drafts)

	var omitted []string
	for _, d := range rw2Drafts {
		if d["type"] == "context_edit" {
			omitted = append(omitted, d["targetId"].(string))
		}
	}
	// Must omit strictly after e-read-tr-2 (the checkpoint turn's last tool result),
	// and MUST NOT omit e-keep-msg or e-old-* (which would happen if old cycle was matched).
	wantOmitted := []string{"e-exp-asst", "e-exp-tr", "e-rw2-asst", "e-rw2-tr"}
	if !slices.Equal(omitted, wantOmitted) {
		t.Fatalf("case (a) omitted = %v, want %v", omitted, wantOmitted)
	}

	// Case (b): aborted checkpoint turn leaves CheckpointEntryID empty; later rewind turn
	// MUST NOT treat its own result as the boundary (drops stale state, emits 0 drafts).
	h.appendEntry(map[string]any{"id": "e-abort-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "aborted cp"}})
	if _, errText := h.callToolWithID("checkpoint", "cp-aborted", map[string]any{"goal": "will abort"}); errText != "" {
		t.Fatalf("case (b) checkpoint: %s", errText)
	}
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "aborted",
		"messageEntryId":     "e-abort-asst",
		"toolResultEntryIds": []any{"e-abort-tr"},
	})
	h.appendEntry(map[string]any{"id": "e-after-abort-asst", "type": "message", "message": map[string]any{"role": "assistant", "content": "rw"}})
	if _, errText := h.callToolWithID("rewind", "rw-after-abort", map[string]any{"report": "after abort"}); errText != "" {
		t.Fatalf("case (b) rewind: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-after-abort-tr", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "rewind", "toolCallId": "rw-after-abort"}})
	abRwRaw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":            "completed",
		"messageEntryId":     "e-after-abort-asst",
		"toolResultEntryIds": []any{"e-after-abort-tr"},
	})
	if abDrafts, _ := decodeBoundaryEntries(t, abRwRaw); len(abDrafts) != 0 {
		t.Fatalf("case (b) expected 0 drafts when checkpoint turn was aborted, got %v", abDrafts)
	}

	// Case (c): bash toolResult WITH details.toolCommands alongside a trailing `read`
	// selects the exact bash entry (not the trailing read).
	h.appendEntry(map[string]any{"id": "e-asst-3", "type": "message", "message": map[string]any{"role": "assistant", "content": "bash+read stamped"}})
	if _, errText := h.callToolWithID("checkpoint", "call-bash-3/1", map[string]any{"goal": "stamped"}); errText != "" {
		t.Fatalf("case (c) checkpoint: %s", errText)
	}
	h.appendEntry(map[string]any{"id": "e-bash-tr-3", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "bash", "toolCallId": "call-bash-3"}})
	h.appendEntry(map[string]any{"id": "e-read-tr-3", "type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "call-read-3"}})
	cp3Raw, _ := h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"outcome":        "completed",
		"messageEntryId": "e-asst-3",
		"toolResults": []any{
			map[string]any{
				"role":       "toolResult",
				"toolName":   "bash",
				"toolCallId": "call-bash-3",
				"details": map[string]any{
					"toolCommands": []any{
						map[string]any{"tool": "checkpoint", "mode": "execute", "exitCode": 0},
					},
				},
			},
			map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "call-read-3"},
		},
		"toolResultEntryIds": []any{"e-bash-tr-3", "e-read-tr-3"},
	})
	cp3Drafts, _ := decodeBoundaryEntries(t, cp3Raw)
	if len(cp3Drafts) != 1 {
		t.Fatalf("case (c) expected 1 checkpoint-state draft, got %v", cp3Drafts)
	}
	dataMap, _ := cp3Drafts[0]["data"].(map[string]any)
	if got := dataMap["checkpointEntryId"]; got != "e-bash-tr-3" {
		t.Fatalf("case (c) checkpointEntryId = %v, want e-bash-tr-3", got)
	}
}
func TestCheckpointDefaultDisabled(t *testing.T) {
	ext := sdk.New("checkpoint-disabled-test")
	Register(ext)

	h := startHost(t, ext, hostOptions{
		cwd:         t.TempDir(),
		sessionID:   "sess-off",
		activeTools: []string{"read"},
	})
	defer h.stop()

	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	if calls := h.callsTo("setActiveTools"); len(calls) != 0 {
		t.Fatalf("expected no setActiveTools call when checkpoint.enabled defaults to false, got %d", len(calls))
	}
}

func TestCheckpointSessionReplay(t *testing.T) {
	// upstream: packages/coding-agent/test/agent-session-checkpoint-rewind-branch.test.ts:
	// "rehydrates completed rewind state from the retained report on resume"
	ext := sdk.New("checkpoint-replay-test")
	Register(ext)

	startedAt := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	rewoundAt := time.Date(2026, 5, 1, 10, 5, 0, 0, time.UTC).Format(time.RFC3339Nano)

	h := startHost(t, ext, hostOptions{
		cwd:       t.TempDir(),
		sessionID: "sess-replay",
		leafID:    "e2",
		entries: []map[string]any{
			{
				"id":         "e1",
				"parentId":   nil,
				"type":       "custom",
				"customType": CheckpointStateEntryType,
				"data": map[string]any{
					"active":            true,
					"goal":              "explore",
					"checkpointEntryId": "e0",
					"startedAt":         startedAt,
				},
			},
			{
				"id":         "e2",
				"parentId":   "e1",
				"type":       "custom_message",
				"customType": RewindReportMessageType,
				"content":    renderRewindReport("summary from disk"),
				"details": map[string]any{
					"report":    "summary from disk",
					"startedAt": startedAt,
					"rewoundAt": rewoundAt,
				},
			},
		},
	})
	defer h.stop()

	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	_, errText := h.callTool("rewind", map[string]any{"report": "again"})
	if !strings.Contains(errText, "Checkpoint already completed") {
		t.Fatalf("expected replayed completed-rewind error, got %q", errText)
	}
}
