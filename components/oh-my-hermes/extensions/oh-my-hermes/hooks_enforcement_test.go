package ohmyhermes_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	ohmyhermes "github.com/MichaelKinsy/pigpen/oh-my-hermes"
)

func emitBusChannel(t *testing.T, h *Host, channel string, payload any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bus payload: %v", err)
	}
	dispatched := 0
	for _, c := range h.CallsTo("events.on") {
		if ch, _ := c.Args["channel"].(string); ch == channel {
			handlerID, _ := c.Args["handlerId"].(string)
			_, errMsg := h.roundTrip(map[string]any{
				"method": "events.dispatch",
				"args": map[string]any{
					"handlerId": handlerID,
					"channel":   channel,
					"json":      json.RawMessage(encoded),
				},
			})
			if errMsg != "" {
				t.Fatalf("events.dispatch %s (%s) failed: %s", channel, handlerID, errMsg)
			}
			dispatched++
		}
	}
	if dispatched == 0 {
		t.Fatalf("no events.on listener registered for channel %q", channel)
	}
}

func decodeToolCallBlock(t *testing.T, raw json.RawMessage) (bool, string) {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return false, ""
	}
	var dec struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &dec); err != nil {
		t.Fatalf("unmarshal tool_call decision %s: %v", string(raw), err)
	}
	return dec.Block, dec.Reason
}

func TestPlanStageAwaitingAcceptanceBlocksMutationsAllowsReads(t *testing.T) {
	setupTempOMH(t)
	sessionID := "sess-plan-guard"
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": sessionID}, ""
			}
			return map[string]any{}, ""
		},
	})

	// Set plan_stage=awaiting_acceptance via omh_todo.
	raw, errMsg := h.Tool("omh_todo", map[string]any{
		"action":     "set",
		"title":      "Draft Plan",
		"plan_stage": "awaiting_acceptance",
	})
	if errMsg != "" {
		t.Fatalf("omh_todo set failed: %s", errMsg)
	}
	if payload, isErr := parseToolJSON(t, raw); isErr || payload["plan_stage"] != "awaiting_acceptance" {
		t.Fatalf("unexpected omh_todo payload: err=%v payload=%v", isErr, payload)
	}

	// write_file must be blocked.
	res := h.Fire(sdk.EventToolCall, map[string]any{
		"type":     "tool_call",
		"toolName": "write_file",
		"input":    map[string]any{"path": "foo.go", "content": "package main"},
	})
	blocked, reason := decodeToolCallBlock(t, res)
	if !blocked || !strings.Contains(reason, "awaiting_acceptance") {
		t.Fatalf("expected write_file to be blocked during awaiting_acceptance, got blocked=%v reason=%q", blocked, reason)
	}

	// patch must be blocked.
	res = h.Fire(sdk.EventToolCall, map[string]any{
		"type":     "tool_call",
		"toolName": "patch",
		"input":    map[string]any{"path": "foo.go", "patch": "@@"},
	})
	blocked, reason = decodeToolCallBlock(t, res)
	if !blocked || !strings.Contains(reason, "awaiting_acceptance") {
		t.Fatalf("expected patch to be blocked during awaiting_acceptance, got blocked=%v reason=%q", blocked, reason)
	}

	// read_file must be allowed.
	res = h.Fire(sdk.EventToolCall, map[string]any{
		"type":     "tool_call",
		"toolName": "read_file",
		"input":    map[string]any{"path": "foo.go"},
	})
	blocked, reason = decodeToolCallBlock(t, res)
	if blocked {
		t.Fatalf("expected read_file to be allowed during awaiting_acceptance, got blocked=%v reason=%q", blocked, reason)
	}

	// Advance plan_stage to executing -> write_file is now allowed.
	_, errMsg = h.Tool("omh_todo", map[string]any{
		"action":     "set",
		"plan_stage": "executing",
	})
	if errMsg != "" {
		t.Fatalf("omh_todo executing failed: %s", errMsg)
	}
	res = h.Fire(sdk.EventToolCall, map[string]any{
		"type":     "tool_call",
		"toolName": "write_file",
		"input":    map[string]any{"path": "foo.go", "content": "package main"},
	})
	blocked, reason = decodeToolCallBlock(t, res)
	if blocked {
		t.Fatalf("expected write_file to be allowed once executing, got blocked=%v reason=%q", blocked, reason)
	}
}

func TestRepeatCallLoopBreakerThresholdPollerExemptionAndProgressReset(t *testing.T) {
	setupTempOMH(t)
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "sess-loop-breaker"}, ""
			}
			return map[string]any{}, ""
		},
	})

	// 1. Threshold is 8: calls 1..7 allowed, 8th identical call blocked.
	ev := map[string]any{
		"type":     "tool_call",
		"toolName": "read_file",
		"input":    map[string]any{"path": "same.go", "offset": 1},
	}
	for i := 1; i <= 7; i++ {
		res := h.Fire(sdk.EventToolCall, ev)
		if blocked, reason := decodeToolCallBlock(t, res); blocked {
			t.Fatalf("call %d should not be blocked yet, got reason=%q", i, reason)
		}
	}
	res := h.Fire(sdk.EventToolCall, ev)
	blocked, reason := decodeToolCallBlock(t, res)
	if !blocked || !strings.Contains(strings.ToLower(reason), "loop breaker") {
		t.Fatalf("expected 8th identical call to trigger loop breaker, got blocked=%v reason=%q", blocked, reason)
	}

	// Changing arguments resets the streak.
	res = h.Fire(sdk.EventToolCall, map[string]any{
		"type":     "tool_call",
		"toolName": "read_file",
		"input":    map[string]any{"path": "other.go", "offset": 1},
	})
	if blocked, reason := decodeToolCallBlock(t, res); blocked {
		t.Fatalf("different args should reset loop breaker, got reason=%q", reason)
	}

	// 2. Polling tools (process_manage, *_poll, *_get_result) are exempt even past 8 identical calls.
	for _, pollTool := range []string{"process_manage", "job_poll", "subagent_get_result"} {
		pollEv := map[string]any{
			"type":     "tool_call",
			"toolName": pollTool,
			"input":    map[string]any{"action": "poll", "session_id": "bg-1"},
		}
		for i := 1; i <= 12; i++ {
			res := h.Fire(sdk.EventToolCall, pollEv)
			if blocked, reason := decodeToolCallBlock(t, res); blocked {
				t.Fatalf("polling tool %q call %d must be exempt, got reason=%q", pollTool, i, reason)
			}
		}
	}

	// 3. Differing tool_result output resets the streak to 1 (making progress).
	progEv := map[string]any{
		"type":     "tool_call",
		"toolName": "terminal",
		"input":    map[string]any{"command": "git status"},
	}
	for i := 1; i <= 10; i++ {
		res := h.Fire(sdk.EventToolCall, progEv)
		if blocked, reason := decodeToolCallBlock(t, res); blocked {
			t.Fatalf("progressing call %d should not be blocked, got reason=%q", i, reason)
		}
		_ = h.Fire(sdk.EventToolResult, map[string]any{
			"type":     "tool_result",
			"toolName": "terminal",
			"content":  []any{map[string]any{"type": "text", "text": "output step " + string(rune('a'+i))}},
		})
	}
}

func TestToolResultOnOMHDAGAutoRefreshesStatusAndBelowEditorWidget(t *testing.T) {
	setupTempOMH(t)
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "tui",
		HasUI: sdk.Bool(true),
		Cwd:   t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "sess-dag-widget"}, ""
			}
			return map[string]any{}, ""
		},
	})

	// Create an active DAG.
	raw, errMsg := h.Tool("omh_dag", map[string]any{
		"action": "create",
		"run_id": "dag-live",
		"title":  "Live Widget Pipeline",
		"nodes": []any{
			map[string]any{"id": "n1", "title": "Build", "category": "deep"},
		},
	})
	if errMsg != "" {
		t.Fatalf("omh_dag create failed: %s", errMsg)
	}
	if _, isErr := parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_dag create returned error")
	}

	// Fire EventToolResult for omh_dag -> status + belowEditor widget should update.
	_ = h.Fire(sdk.EventToolResult, map[string]any{
		"type":     "tool_result",
		"toolName": "omh_dag",
	})

	statusCalls := h.CallsTo("ui.setStatus")
	if len(statusCalls) == 0 {
		t.Fatalf("expected ui.setStatus after omh_dag tool_result")
	}
	lastStatus := statusCalls[len(statusCalls)-1]
	if lastStatus.Args["key"] != "omh" {
		t.Fatalf("expected status key 'omh', got %v", lastStatus.Args["key"])
	}
	if text, _ := lastStatus.Args["text"].(string); !strings.Contains(text, "OMH[") || !strings.Contains(text, "dags:1") {
		t.Fatalf("unexpected status text: %q", text)
	}

	widgetCalls := h.CallsTo("ui.setWidget")
	if len(widgetCalls) == 0 {
		t.Fatalf("expected ui.setWidget after omh_dag tool_result")
	}
	lastWidget := widgetCalls[len(widgetCalls)-1]
	if lastWidget.Args["key"] != "omh_dag" {
		t.Fatalf("expected widget key 'omh_dag', got %v", lastWidget.Args["key"])
	}
	opts, _ := lastWidget.Args["options"].(map[string]any)
	if opts == nil || opts["placement"] != "belowEditor" {
		t.Fatalf("expected widget placement belowEditor, got %+v", lastWidget.Args["options"])
	}
	lines, _ := lastWidget.Args["content"].([]any)
	if len(lines) == 0 {
		t.Fatalf("expected non-empty widget content lines, got %+v", lastWidget.Args["content"])
	}

	// Complete the DAG -> next tool_result should clear the widget.
	_, errMsg = h.Tool("omh_dag", map[string]any{
		"action": "complete",
		"run_id": "dag-live",
	})
	if errMsg != "" {
		t.Fatalf("omh_dag complete failed: %s", errMsg)
	}
	_ = h.Fire(sdk.EventToolResult, map[string]any{
		"type":     "tool_result",
		"toolName": "omh_dag",
	})
	widgetCalls = h.CallsTo("ui.setWidget")
	cleared := widgetCalls[len(widgetCalls)-1]
	if cleared.Args["content"] != nil {
		t.Fatalf("expected widget to be cleared when all DAGs settled, got %+v", cleared.Args["content"])
	}
}

func TestTrackedChildSessionCalibrationAndSubagentStopDAGLatching(t *testing.T) {
	setupTempOMH(t)
	childSID := "child-session-42"
	currentSID := "parent-session-1"

	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": currentSID}, ""
			}
			return map[string]any{}, ""
		},
	})

	// Create a DAG run whose node will be latched on ChannelSubagentStop.
	_, errMsg := h.Tool("omh_dag", map[string]any{
		"action": "create",
		"run_id": "dag-latch",
		"title":  "Latch Test",
		"nodes": []any{
			map[string]any{"id": "worker-a", "title": "Worker A"},
		},
	})
	if errMsg != "" {
		t.Fatalf("omh_dag create failed: %s", errMsg)
	}

	// Emit ChannelSubagentStart for childSID.
	emitBusChannel(t, h, events.ChannelSubagentStart, events.SubagentStartPayload{
		ParentSessionID: "parent-session-1",
		ChildSessionID:  childSID,
		TaskID:          "dag-latch:worker-a",
		Goal:            "Implement worker A",
	})

	// Now switch host session ID to childSID and fire before_agent_start without isSubagent flag.
	currentSID = childSID
	resRaw := h.Fire(sdk.EventBeforeAgentStart, map[string]any{
		"prompt": "do worker A",
		"systemPromptOptions": map[string]any{
			"sections": map[string]any{
				events.SectionIdentity: "Hermes Identity",
			},
		},
	})
	var beforeResp struct {
		PigPromptOptions struct {
			Sections map[string]string `json:"sections"`
		} `json:"_pigPromptOptions"`
	}
	if err := json.Unmarshal(resRaw, &beforeResp); err != nil {
		t.Fatalf("unmarshal before_agent_start: %v", err)
	}
	secs := beforeResp.PigPromptOptions.Sections
	if strings.TrimSpace(secs[ohmyhermes.SectionOMHChildCalibration]) == "" {
		t.Fatalf("expected SectionOMHChildCalibration for tracked child session, got sections: %+v", secs)
	}
	if strings.TrimSpace(secs[ohmyhermes.SectionOMHMainCalibration]) != "" {
		t.Fatalf("child session should not receive SectionOMHMainCalibration, got %+v", secs)
	}

	// Emit ChannelSubagentStop -> removes child from registry and completes DAG node.
	emitBusChannel(t, h, events.ChannelSubagentStop, events.SubagentStopPayload{
		ParentSessionID: "parent-session-1",
		ChildSessionID:  childSID,
		TaskID:          "dag-latch:worker-a",
		Status:          "completed",
		DurationMs:      125,
	})

	raw, errMsg := h.Tool("omh_dag", map[string]any{"action": "get", "run_id": "dag-latch"})
	if errMsg != "" {
		t.Fatalf("omh_dag get failed: %s", errMsg)
	}
	payload, isErr := parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_dag get error: %v", payload)
	}
	run := payload["dag"].(map[string]any)
	if run["status"] != "completed" {
		t.Fatalf("expected DAG run to transition to completed after subagent stop, got %v", run["status"])
	}
}

func TestMemoryPrefetchMergesStructuredRecordsAndDeduplicates(t *testing.T) {
	omhHome := setupTempOMH(t)
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  t.TempDir(),
	})

	// Capture a durable structured memory record via omh_memory.
	raw, errMsg := h.Tool("omh_memory", map[string]any{
		"action":          "capture",
		"summary":         "Always stop tickers on agent_end to avoid goroutine leaks",
		"record_type":     "lesson",
		"tags":            []any{"goroutines", "lifecycle"},
		"retention_class": "durable",
	})
	if errMsg != "" {
		t.Fatalf("omh_memory capture failed: %s", errMsg)
	}
	if _, isErr := parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_memory capture returned error")
	}

	// Also save a distinct episodic note via omh_remember.
	_, errMsg = h.Tool("omh_remember", map[string]any{
		"action":  "save",
		"content": "Ticker cleanup verified in unit tests for lifecycle hooks",
		"tags":    []any{"lifecycle"},
	})
	if errMsg != "" {
		t.Fatalf("omh_remember save failed: %s", errMsg)
	}

	prov := ohmyhermes.NewOmhMemoryProvider(func() ohmyhermes.OMHPaths {
		return ohmyhermes.ResolvePaths(omhHome)
	})
	block, err := prov.Prefetch(context.Background(), "sess-mem", "ticker lifecycle goroutine")
	if err != nil {
		t.Fatalf("Prefetch failed: %v", err)
	}
	if !strings.Contains(block, "Always stop tickers on agent_end") {
		t.Fatalf("expected captured record in Prefetch output, got:\n%s", block)
	}
	if !strings.Contains(block, "Ticker cleanup verified") {
		t.Fatalf("expected episodic note in Prefetch output, got:\n%s", block)
	}
	// Ensure the captured summary (which also wrote to notes.jsonl) is deduplicated to a single line.
	if strings.Count(block, "Always stop tickers on agent_end") != 1 {
		t.Fatalf("expected deduplicated summary in Prefetch output, got:\n%s", block)
	}
}

func TestOMHRoleAliasAndOMHInteractEndToEnd(t *testing.T) {
	setupTempOMH(t)
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  t.TempDir(),
	})

	// 1. Verify singular omh_role alias works identically to omh_roles.
	if !h.tools["omh_role"] || !h.tools["omh_roles"] {
		t.Fatalf("expected both omh_role and omh_roles to be registered")
	}
	raw, errMsg := h.Tool("omh_role", map[string]any{"action": "list"})
	if errMsg != "" {
		t.Fatalf("omh_role list failed: %s", errMsg)
	}
	payload, isErr := parseToolJSON(t, raw)
	if isErr || len(payload["roles"].([]any)) != 9 {
		t.Fatalf("unexpected omh_role list result: err=%v payload=%v", isErr, payload)
	}
	raw, _ = h.Tool("omh_role", map[string]any{"action": "get", "role": "architect"})
	payload, isErr = parseToolJSON(t, raw)
	if isErr || payload["kind"] != "builtin" {
		t.Fatalf("unexpected omh_role get result: err=%v payload=%v", isErr, payload)
	}

	// 2. Verify omh_interact with high-confidence technical query.
	raw, errMsg = h.Tool("omh_interact", map[string]any{
		"message": "review the security architecture and audit race conditions",
		"source":  "hermes",
		"mode":    "plan",
		"limit":   4,
	})
	if errMsg != "" {
		t.Fatalf("omh_interact failed: %s", errMsg)
	}
	payload, isErr = parseToolJSON(t, raw)
	if isErr || payload["schema"] != "chat_interaction/v1" {
		t.Fatalf("unexpected omh_interact payload: err=%v payload=%v", isErr, payload)
	}
	if payload["suggested_route"] == "" || payload["workflow_plan"] == nil || payload["recommendations"] == nil {
		t.Fatalf("missing required chat_interaction/v1 fields: %v", payload)
	}

	// 3. Verify omh_interact emits route_question when confidence is low.
	raw, errMsg = h.Tool("omh_interact", map[string]any{
		"message": "zzz qqq",
		"source":  "slack",
		"mode":    "auto",
		"limit":   3,
	})
	if errMsg != "" {
		t.Fatalf("omh_interact low-confidence failed: %s", errMsg)
	}
	payload, isErr = parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_interact low-confidence returned error: %v", payload)
	}
	if q, _ := payload["route_question"].(string); strings.TrimSpace(q) == "" {
		t.Fatalf("expected route_question on low confidence, got %v", payload)
	}
}

func TestULWOmhStatusAndInteractiveOmhThemeCommands(t *testing.T) {
	setupTempOMH(t)
	var selectedTitle string
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "tui",
		HasUI: sdk.Bool(true),
		Cwd:   t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				selectedTitle, _ = args["title"].(string)
				return map[string]any{"ok": true, "selected": "  amber — Amber gold on deep bronze. (#F59E0B)"}, ""
			case "ui.setTheme":
				return map[string]any{"success": true}, ""
			}
			return map[string]any{}, ""
		},
	})

	for _, cmd := range []string{"ulw", "omh-status", "omh-theme"} {
		if !h.cmds[cmd] {
			t.Fatalf("expected slash command /%s to be registered", cmd)
		}
	}

	if errMsg := h.Command("ulw", "debug race condition in goroutine worker"); errMsg != "" {
		t.Fatalf("/ulw failed: %s", errMsg)
	}
	if errMsg := h.Command("omh-status", ""); errMsg != "" {
		t.Fatalf("/omh-status failed: %s", errMsg)
	}
	if errMsg := h.Command("omh-theme", ""); errMsg != "" {
		t.Fatalf("interactive /omh-theme failed: %s", errMsg)
	}
	if selectedTitle != "Select OMH Theme" {
		t.Fatalf("expected ui.select with title 'Select OMH Theme', got %q", selectedTitle)
	}
	foundAmber := false
	for _, c := range h.CallsTo("ui.setTheme") {
		if c.Args["name"] == "omh-amber" || c.Args["theme"] == "omh-amber" {
			foundAmber = true
			break
		}
	}
	if !foundAmber {
		t.Fatalf("expected ui.setTheme('omh-amber') after interactive picker, got %+v", h.CallsTo("ui.setTheme"))
	}

	// Verify /ulw and /omh-status emitted messages with expected content.
	var sawULW, sawStatus bool
	for _, c := range h.CallsTo("sendMessage") {
		content, _ := c.Args["content"].(string)
		if content == "" {
			if msg, ok := c.Args["message"].(map[string]any); ok {
				content, _ = msg["content"].(string)
			}
		}
		if strings.Contains(content, "OMH /ulw Workflow Router") && strings.Contains(content, "pigpen-omh-") {
			sawULW = true
		}
		if strings.Contains(content, "OMH Status Card") && strings.Contains(content, "Memory:") && strings.Contains(content, "Routing:") {
			sawStatus = true
		}
	}
	if !sawULW {
		t.Fatalf("expected /ulw output message with pigpen-omh-* recommendation")
	}
	if !sawStatus {
		t.Fatalf("expected /omh-status output message with OMH Status Card")
	}
}
