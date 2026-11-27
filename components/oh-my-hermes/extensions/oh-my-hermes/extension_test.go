package ohmyhermes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
	ohmyhermes "github.com/MichaelKinsy/pigpen/oh-my-hermes"
)

func parseToolJSON(t *testing.T, raw json.RawMessage) (map[string]any, bool) {
	t.Helper()
	var parsed struct {
		IsError bool            `json:"isError"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal tool result %s: %v", string(raw), err)
	}
	var text string
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(parsed.Content, &blocks); err == nil && len(blocks) > 0 {
		text = blocks[0].Text
	} else {
		_ = json.Unmarshal(parsed.Content, &text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal tool payload JSON %q: %v", text, err)
	}
	return out, parsed.IsError
}

func setupTempOMH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ohmyhermes.SetOMHHomeOverride("")
	t.Setenv(ohmyhermes.EnvOMHHome, dir)
	t.Cleanup(func() { ohmyhermes.SetOMHHomeOverride("") })
	return dir
}

func TestValidateDAGCycleAndFrontier(t *testing.T) {
	// Valid diamond DAG: a -> {b, c} -> d
	nodes := []ohmyhermes.DAGNode{
		{ID: "a", Title: "root", Status: "pending"},
		{ID: "b", Title: "left", DependsOn: []string{"a"}, Status: "pending"},
		{ID: "c", Title: "right", DependsOn: []string{"a"}, Status: "pending"},
		{ID: "d", Title: "join", DependsOn: []string{"b", "c"}, Status: "pending"},
	}
	layers, err := ohmyhermes.ValidateDAG(nodes)
	if err != nil {
		t.Fatalf("expected valid DAG, got error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d (%v)", len(layers), layers)
	}

	// Self-dependency rejected
	_, err = ohmyhermes.ValidateDAG([]ohmyhermes.DAGNode{{ID: "x", DependsOn: []string{"x"}}})
	if err == nil || !strings.Contains(err.Error(), "self_dependency") {
		t.Fatalf("expected self_dependency error, got %v", err)
	}

	// Unknown dependency rejected
	_, err = ohmyhermes.ValidateDAG([]ohmyhermes.DAGNode{{ID: "x", DependsOn: []string{"missing"}}})
	if err == nil || !strings.Contains(err.Error(), "unknown_dependency") {
		t.Fatalf("expected unknown_dependency error, got %v", err)
	}

	// Cycle rejected: a -> b -> c -> a
	_, err = ohmyhermes.ValidateDAG([]ohmyhermes.DAGNode{
		{ID: "a", DependsOn: []string{"c"}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle_detected") {
		t.Fatalf("expected cycle_detected error, got %v", err)
	}
}

func TestBuiltinRolesAndIdentities(t *testing.T) {
	omhHome := setupTempOMH(t)
	roles := ohmyhermes.BuiltinRoles()
	if len(roles) != 9 {
		t.Fatalf("expected 9 built-in roles, got %d", len(roles))
	}
	wantIDs := []string{"planner", "architect", "implementer", "reviewer", "researcher", "debugger", "verifier", "writer", "explorer"}
	for _, id := range wantIDs {
		if _, ok := ohmyhermes.LookupBuiltinRole(id); !ok {
			t.Errorf("missing built-in role %q", id)
		}
	}

	store := ohmyhermes.NewIdentityStore(func() ohmyhermes.OMHPaths { return ohmyhermes.ResolvePaths(omhHome) })
	saved, err := store.Set(ohmyhermes.IdentityProfile{
		Name:     "go_auditor",
		Role:     "reviewer",
		Category: "review",
		Prompt:   "Check race conditions and fuse hazards.",
		ModelChain: []ohmyhermes.IdentityModelEntry{
			{ModelID: "gpt-6.1-sol", ReasoningEffort: "high"},
		},
	})
	if err != nil {
		t.Fatalf("set identity failed: %v", err)
	}
	if saved.Name != "go_auditor" {
		t.Fatalf("unexpected identity name %q", saved.Name)
	}
	got, ok, err := store.Get("custom:go_auditor")
	if err != nil || !ok || len(got.ModelChain) != 1 || got.ModelChain[0].ModelID != "gpt-6.1-sol" {
		t.Fatalf("unexpected fetched identity: ok=%v err=%v got=%+v", ok, err, got)
	}
	if _, err := os.Stat(filepath.Join(omhHome, "routing", "identities.json")); err != nil {
		t.Fatalf("expected identities.json on disk: %v", err)
	}
}

func TestCLISubcommandsAndOMHHomePreprocessor(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() { ohmyhermes.SetOMHHomeOverride("") })

	var out, errBuf bytes.Buffer
	res := cli.Dispatch(clihook.Env{
		Args:   []string{"--omh-home", tmpDir, "omh-doctor"},
		Stdout: &out,
		Stderr: &errBuf,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("omh-doctor dispatch failed: handled=%v exit=%d stderr=%q", res.Handled, res.Exit, errBuf.String())
	}
	if !strings.Contains(out.String(), tmpDir) || !strings.Contains(out.String(), "OMH Doctor") {
		t.Fatalf("unexpected omh-doctor output: %q", out.String())
	}

	// Test hud, model-chains, dag subcommands
	for _, sub := range []string{"hud", "model-chains", "dag"} {
		out.Reset()
		errBuf.Reset()
		r := cli.Dispatch(clihook.Env{
			Args:   []string{sub},
			Stdout: &out,
			Stderr: &errBuf,
		})
		if !r.Handled || r.Exit != 0 {
			t.Fatalf("subcommand %q failed: handled=%v exit=%d stderr=%q", sub, r.Handled, r.Exit, errBuf.String())
		}
		if strings.TrimSpace(out.String()) == "" {
			t.Errorf("subcommand %q produced empty output", sub)
		}
	}
}

func TestFakeHostIntegrationAllOMHToolsCommandsSeamsAndHooks(t *testing.T) {
	omhHome := setupTempOMH(t)
	workDir := t.TempDir()
	sessionID := "omh-fakehost-sess-1"

	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  workDir,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "getSessionID", "getSessionId":
				return map[string]any{"sessionId": sessionID}, ""
			default:
				return map[string]any{}, ""
			}
		},
	})

	// 1. Verify all 9 omh_* tools are registered
	expectedTools := []string{
		"omh_delegate_route",
		"omh_dag",
		"omh_team",
		"omh_loop",
		"omh_todo",
		"omh_completion",
		"omh_run_summary",
		"omh_remember",
		"omh_roles",
	}
	for _, name := range expectedTools {
		if !h.tools[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	// 2. Verify all 5 slash commands are registered
	expectedCmds := []string{"dag", "bots", "omh-model", "omh-hud", "omh-theme"}
	for _, name := range expectedCmds {
		if !h.cmds[name] {
			t.Errorf("expected slash command %q to be registered", name)
		}
	}

	// 3. OnSessionStart sets status bar "omh"
	_ = h.Fire(sdk.EventSessionStart, map[string]any{})
	foundStatus := false
	for _, c := range h.CallsTo("ui.setStatus") {
		if c.Args["key"] == "omh" {
			foundStatus = true
			break
		}
	}
	if !foundStatus {
		t.Errorf("expected ui.setStatus for key \"omh\" on session_start")
	}

	// 4. OnBeforeAgentStart preserves hermes_* sections and appends underscore-only omh_* sections
	beforeData := map[string]any{
		"prompt": "build the DAG",
		"systemPromptOptions": map[string]any{
			"sections": map[string]any{
				events.SectionIdentity: "Hermes Identity",
				events.SectionMemory:   "Hermes Memory",
			},
		},
	}
	resRaw := h.Fire(sdk.EventBeforeAgentStart, beforeData)
	var beforeResp struct {
		PigPromptOptions struct {
			Sections map[string]string `json:"sections"`
		} `json:"_pigPromptOptions"`
	}
	if err := json.Unmarshal(resRaw, &beforeResp); err != nil {
		t.Fatalf("unmarshal before_agent_start response: %v (raw=%s)", err, string(resRaw))
	}
	secs := beforeResp.PigPromptOptions.Sections
	if secs[events.SectionIdentity] != "Hermes Identity" || secs[events.SectionMemory] != "Hermes Memory" {
		t.Errorf("existing hermes_* sections were not preserved: %+v (raw=%s)", secs, string(resRaw))
	}
	for _, wantSec := range []string{
		ohmyhermes.SectionOMHAwareness,
		ohmyhermes.SectionOMHDelegationProtocol,
		ohmyhermes.SectionOMHMainCalibration,
	} {
		if strings.TrimSpace(secs[wantSec]) == "" {
			t.Errorf("expected non-empty prompt section %q, got %+v", wantSec, secs)
		}
	}

	// 5. omh_dag: cycle rejection + valid DAG frontier progression
	raw, errMsg := h.Tool("omh_dag", map[string]any{
		"action": "create",
		"run_id": "bad-cycle",
		"title":  "Cyclic",
		"nodes": []any{
			map[string]any{"id": "n1", "depends_on": []any{"n2"}},
			map[string]any{"id": "n2", "depends_on": []any{"n1"}},
		},
	})
	if errMsg != "" {
		t.Fatalf("omh_dag cycle host error: %s", errMsg)
	}
	payload, isErr := parseToolJSON(t, raw)
	_ = isErr
	if payload["ok"] != false || !strings.Contains(payload["error"].(string), "cycle_detected") {
		t.Fatalf("expected cycle rejection, got payload=%v", payload)
	}

	raw, errMsg = h.Tool("omh_dag", map[string]any{
		"action": "create",
		"run_id": "dag-1",
		"title":  "Build Pipeline",
		"nodes": []any{
			map[string]any{"id": "plan", "title": "Plan"},
			map[string]any{"id": "code", "title": "Code", "depends_on": []any{"plan"}},
			map[string]any{"id": "test", "title": "Test", "depends_on": []any{"code"}},
		},
	})
	if errMsg != "" {
		t.Fatalf("omh_dag create host error: %s", errMsg)
	}
	payload, isErr = parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_dag create failed: %v", payload)
	}
	if ready := payload["ready"].([]any); len(ready) != 1 || ready[0] != "plan" {
		t.Fatalf("expected initial ready=[plan], got %v", ready)
	}

	raw, _ = h.Tool("omh_dag", map[string]any{
		"action":  "update_node",
		"run_id":  "dag-1",
		"node_id": "plan",
		"status":  "completed",
		"result":  "approved",
	})
	payload, isErr = parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_dag update_node failed: %v", payload)
	}
	if ready := payload["ready"].([]any); len(ready) != 1 || ready[0] != "code" {
		t.Fatalf("expected frontier to advance to [code], got %v", ready)
	}

	// 6. omh_delegate_route + delegation.ResolveRoute integration
	raw, _ = h.Tool("omh_delegate_route", map[string]any{
		"action":         "set",
		"category":       "deep",
		"model":          "claude-opus-5-5",
		"thinking_level": "high",
	})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_delegate_route set failed: %v", payload)
	}
	dec, matched, err := delegation.ResolveRoute(delegation.SpawnRequest{Category: "deep"})
	if err != nil || !matched || dec.Model != "claude-opus-5-5" || dec.ThinkingLevel != "high" {
		t.Fatalf("delegation.ResolveRoute mismatch: matched=%v err=%v dec=%+v", matched, err, dec)
	}

	// 7. omh_todo unified with todo.Default()
	raw, _ = h.Tool("omh_todo", map[string]any{
		"action":         "set",
		"title":          "Phase 2",
		"plan_stage":     "accepted",
		"blocked_reason": "",
		"items": []any{
			map[string]any{"id": "t1", "content": "Wire OMH", "status": "in_progress"},
		},
	})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_todo set failed: %v", payload)
	}
	sharedItems := todo.Default().Get(sessionID)
	if len(sharedItems) != 1 || sharedItems[0].Content != "Wire OMH" {
		t.Fatalf("todo.Default() not unified with omh_todo: %+v", sharedItems)
	}

	// 8. omh_team + omh_loop
	raw, _ = h.Tool("omh_team", map[string]any{
		"action":  "create",
		"team_id": "team-1",
		"title":   "Parallel Build",
		"lanes":   []any{map[string]any{"lane_id": "lane-a", "title": "Slice A", "role": "implementer"}},
	})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_team create failed: %v", payload)
	}
	raw, _ = h.Tool("omh_loop", map[string]any{
		"action":           "create",
		"loop_id":          "loop-1",
		"goal":             "Converge tests",
		"permission_scope": "workspace-write",
		"max_iterations":   2,
	})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_loop create failed: %v", payload)
	}
	raw, _ = h.Tool("omh_loop", map[string]any{"action": "step", "loop_id": "loop-1", "note": "iter 1"})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_loop step 1 failed: %v", payload)
	}
	raw, _ = h.Tool("omh_loop", map[string]any{"action": "step", "loop_id": "loop-1", "note": "iter 2"})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_loop step 2 failed: %v", payload)
	}
	// Third step must fail because max_iterations=2 was reached
	raw, _ = h.Tool("omh_loop", map[string]any{"action": "step", "loop_id": "loop-1", "note": "iter 3"})
	if p3, _ := parseToolJSON(t, raw); p3["ok"] != false {
		t.Fatalf("expected omh_loop step 3 to fail after max_iterations=2, got %v", p3)
	}

	// 9. omh_completion evidence boundary
	raw, _ = h.Tool("omh_completion", map[string]any{
		"action":            "record",
		"record_id":         "comp-unverified",
		"task":              "Prepared only",
		"prepared_evidence": []any{"wrote patch"},
	})
	payload, isErr = parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_completion record failed: %v", payload)
	}
	if rec := payload["record"].(map[string]any); rec["verified"].(bool) {
		t.Fatalf("prepared-only completion must not be verified")
	}

	raw, _ = h.Tool("omh_completion", map[string]any{
		"action":    "record",
		"record_id": "comp-verified",
		"task":      "Verified build",
		"observed_evidence": []any{
			map[string]any{"command": "go test ./...", "exit_code": 0, "summary": "PASS"},
		},
	})
	payload, isErr = parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_completion verified record failed: %v", payload)
	}
	if rec := payload["record"].(map[string]any); !rec["verified"].(bool) {
		t.Fatalf("observed exit_code=0 completion should be verified: %v", rec)
	}

	// 10. omh_remember + OmhMemoryProvider global registration
	raw, _ = h.Tool("omh_remember", map[string]any{
		"action":  "save",
		"content": "Always keep PiG prompt section names underscore-only",
		"tags":    []any{"pig", "prompt"},
	})
	if payload, isErr = parseToolJSON(t, raw); isErr {
		t.Fatalf("omh_remember save failed: %v", payload)
	}
	memStore := memory.NewStore(t.TempDir(), 0, 0)
	provs := memStore.Providers()
	var omhProv memory.Provider
	for _, p := range provs {
		if p.Name() == "omh" {
			omhProv = p
			break
		}
	}
	if omhProv == nil {
		t.Fatalf("expected OmhMemoryProvider registered globally via memory.RegisterGlobalProvider")
	}
	prefetched, err := omhProv.Prefetch(context.Background(), sessionID, "underscore prompt")
	if err != nil || !strings.Contains(prefetched, "underscore-only") {
		t.Fatalf("OmhMemoryProvider.Prefetch failed: err=%v out=%q", err, prefetched)
	}
	if _, err := os.Stat(filepath.Join(omhHome, "memory", "notes.jsonl")); err != nil {
		t.Fatalf("expected notes.jsonl on disk: %v", err)
	}

	// 11. omh_roles + omh_run_summary
	raw, _ = h.Tool("omh_roles", map[string]any{"action": "list"})
	payload, isErr = parseToolJSON(t, raw)
	if isErr || len(payload["roles"].([]any)) != 9 {
		t.Fatalf("omh_roles list unexpected: err=%v payload=%v", isErr, payload)
	}

	usage.Default().Add(sessionID, "", "claude-sonnet-5-5", 1000, 400, 0.02, 2, 5)
	raw, _ = h.Tool("omh_run_summary", map[string]any{})
	payload, isErr = parseToolJSON(t, raw)
	if isErr {
		t.Fatalf("omh_run_summary failed: %v", payload)
	}
	u := payload["usage"].(map[string]any)
	if u["input_tokens"].(float64) < 1000 {
		t.Fatalf("expected usage.Default() tokens in omh_run_summary, got %v", u)
	}

	// 12. Exercise all 5 slash commands
	for _, cmd := range []struct{ name, args string }{
		{"dag", "dag-1"},
		{"bots", ""},
		{"omh-model", ""},
		{"omh-hud", ""},
		{"omh-theme", "kanagawa"},
	} {
		if errMsg := h.Command(cmd.name, cmd.args); errMsg != "" {
			t.Errorf("slash command /%s %q failed: %s", cmd.name, cmd.args, errMsg)
		}
	}
}
