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

func TestOMHThemeFilesAndSetTheme(t *testing.T) {
	setupTempOMH(t)
	agentDir := t.TempDir()
	t.Setenv("PIG_AGENT_DIR", agentDir)

	if err := ohmyhermes.EnsureOMHThemeFiles(agentDir); err != nil {
		t.Fatalf("EnsureOMHThemeFiles failed: %v", err)
	}
	// Idempotent second call must also succeed without error.
	if err := ohmyhermes.EnsureOMHThemeFiles(agentDir); err != nil {
		t.Fatalf("second EnsureOMHThemeFiles failed: %v", err)
	}

	requiredColorKeys := []string{
		"accent", "border", "borderAccent", "borderMuted",
		"success", "error", "warning", "muted", "dim", "text", "thinkingText",
		"selectedBg", "scrollbarTrack", "scrollbarThumb", "searchMatchBg", "searchMatchText",
		"userMessageBg", "userMessageText", "customMessageBg", "customMessageText", "customMessageLabel",
		"toolPendingBg", "toolSuccessBg", "toolErrorBg", "toolTitle", "toolOutput",
		"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder",
		"mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
		"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
		"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable",
		"syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
		"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium",
		"thinkingHigh", "thinkingXhigh", "thinkingMax", "bashMode",
	}

	expectedThemes := map[string]string{
		"omh-sky":      "#38BDF8",
		"omh-amber":    "#F59E0B",
		"omh-crimson":  "#F43F5E",
		"omh-mono":     "#D4D4D8",
		"omh-kanagawa": "#7E9CD8",
	}

	for themeName, wantAccent := range expectedThemes {
		p := filepath.Join(agentDir, "themes", themeName+".json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("missing theme file %s: %v", p, err)
		}
		var doc struct {
			Schema     string            `json:"$schema"`
			Name       string            `json:"name"`
			Appearance string            `json:"appearance"`
			Vars       map[string]string `json:"vars"`
			Colors     map[string]string `json:"colors"`
			Export     map[string]string `json:"export"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("invalid JSON in %s: %v", p, err)
		}
		if doc.Name != themeName {
			t.Errorf("%s: name = %q, want %q", p, doc.Name, themeName)
		}
		if doc.Appearance != "dark" {
			t.Errorf("%s: appearance = %q, want dark", p, doc.Appearance)
		}
		if doc.Vars["accent"] != wantAccent {
			t.Errorf("%s: vars.accent = %q, want %q", p, doc.Vars["accent"], wantAccent)
		}
		for _, k := range requiredColorKeys {
			if _, ok := doc.Colors[k]; !ok {
				t.Errorf("%s: missing required color key %q", p, k)
			}
		}
		for _, ek := range []string{"pageBg", "cardBg", "infoBg"} {
			if doc.Export[ek] == "" {
				t.Errorf("%s: missing export key %q", p, ek)
			}
		}
	}

	// Verify /omh-theme kanagawa triggers ui.setTheme("omh-kanagawa") when HasUI=true.
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "tui",
		HasUI: sdk.Bool(true),
		Cwd:   t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.setTheme" {
				return map[string]any{"success": true}, ""
			}
			return map[string]any{}, ""
		},
	})
	if errMsg := h.Command("omh-theme", "kanagawa"); errMsg != "" {
		t.Fatalf("/omh-theme kanagawa failed: %s", errMsg)
	}
	foundThemeCall := false
	for _, c := range h.CallsTo("ui.setTheme") {
		if c.Args["theme"] == "omh-kanagawa" {
			foundThemeCall = true
			break
		}
	}
	if !foundThemeCall {
		t.Fatalf("expected ui.setTheme(\"omh-kanagawa\"), got calls: %+v", h.CallsTo("ui.setTheme"))
	}
}

func TestOMHLoginAndSpriteDefinitionsExactDimensionsAndPalette(t *testing.T) {
	validateGrid := func(t *testing.T, name string, rows []string, wantW, wantH int, used map[byte]bool) {
		t.Helper()
		if len(rows) != wantH {
			t.Fatalf("%s height = %d, want %d", name, len(rows), wantH)
		}
		for y, row := range rows {
			if len(row) != wantW {
				t.Fatalf("%s[%d] width = %d, want %d (%q)", name, y, len(row), wantW, row)
			}
			for x := range len(row) {
				ch := row[x]
				if ch > 127 || ch < 33 {
					t.Fatalf("%s[%d][%d] non-printable/space ASCII byte %d", name, y, x, ch)
				}
				if ch != '.' {
					used[ch] = true
				}
			}
		}
	}
	validatePalette := func(t *testing.T, label string, palette map[string]string, used map[byte]bool) {
		t.Helper()
		if len(palette) == 0 || len(palette) > 32 {
			t.Fatalf("%s palette size = %d, want 1..32", label, len(palette))
		}
		for sym := range used {
			hex, ok := palette[string(sym)]
			if !ok {
				t.Errorf("%s: symbol %q used in grid but missing from palette", label, sym)
				continue
			}
			if len(hex) != 7 || hex[0] != '#' {
				t.Errorf("%s: palette[%q] = %q, want #RRGGBB", label, sym, hex)
			}
		}
		for k := range palette {
			if len(k) != 1 || k == "." {
				t.Errorf("%s: invalid palette key %q", label, k)
				continue
			}
			if !used[k[0]] {
				t.Errorf("%s: palette key %q is unused in grid (PiG rejects unused palette symbols)", label, k)
			}
		}
	}

	sprite := ohmyhermes.OMHSpriteDefinition()
	if sprite.ID != "omh-pig" {
		t.Errorf("sprite.ID = %q, want omh-pig", sprite.ID)
	}
	if len(sprite.Name) == 0 || len(sprite.Name) > 24 {
		t.Errorf("sprite.Name length = %d out of bounds", len(sprite.Name))
	}
	if len(sprite.Tagline) == 0 || len(sprite.Tagline) > 76 {
		t.Errorf("sprite.Tagline length = %d out of bounds", len(sprite.Tagline))
	}
	spriteUsed := make(map[byte]bool)
	validateGrid(t, "sprite.Mascot", sprite.Mascot, 16, 14, spriteUsed)
	validatePalette(t, "sprite", sprite.Palette, spriteUsed)

	login := ohmyhermes.OMHLoginDefinition()
	if len(login.Name) == 0 || len(login.Name) > 24 {
		t.Errorf("login.Name length = %d out of bounds", len(login.Name))
	}
	if len(login.Description) == 0 || len(login.Description) > 48 {
		t.Errorf("login.Description length = %d out of bounds", len(login.Description))
	}
	if len(login.Tagline) == 0 || len(login.Tagline) > 76 {
		t.Errorf("login.Tagline length = %d out of bounds", len(login.Tagline))
	}
	loginUsed := make(map[byte]bool)
	validateGrid(t, "login.Brand", login.Brand, 41, 5, loginUsed)
	validateGrid(t, "login.Hero", login.Hero, 32, 14, loginUsed)
	validateGrid(t, "login.Mascot", login.Mascot, 16, 14, loginUsed)
	validatePalette(t, "login", login.Palette, loginUsed)

	// Verify OnSessionStart registers sprite and login when HasUI=true.
	setupTempOMH(t)
	t.Setenv("PIG_AGENT_DIR", t.TempDir())
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "tui",
		HasUI: sdk.Bool(true),
		Cwd:   t.TempDir(),
	})
	_ = h.Fire(sdk.EventSessionStart, map[string]any{})
	if len(h.CallsTo("ui.registerSprite")) == 0 {
		t.Errorf("expected ui.registerSprite call on session_start with HasUI=true")
	}
	if len(h.CallsTo("ui.setLogin")) == 0 {
		t.Errorf("expected ui.setLogin call on session_start with HasUI=true")
	}
}

func TestInteractiveOverlaysWithAndWithoutUI(t *testing.T) {
	omhHome := setupTempOMH(t)
	t.Setenv("HERMES_HOME", t.TempDir())
	t.Setenv("PIG_AGENT_DIR", t.TempDir())

	// Seed a DAG run, team, and usage record so /dag and /bots have rich data.
	pathsFn := func() ohmyhermes.OMHPaths { return ohmyhermes.ResolvePaths(omhHome) }
	dags := ohmyhermes.NewDAGStore(pathsFn)
	if _, _, err := dags.Create("sess-ui", "dag-ui-1", "Overlay DAG", []ohmyhermes.DAGNode{
		{ID: "plan", Title: "Plan"},
		{ID: "build", Title: "Build", DependsOn: []string{"plan"}},
	}); err != nil {
		t.Fatalf("seed DAG failed: %v", err)
	}
	teams := ohmyhermes.NewTeamStore(pathsFn)
	if _, err := teams.Create("sess-ui", "team-ui-1", "Overlay Team", []ohmyhermes.TeamLane{
		{LaneID: "lane-a", Title: "Inspect", Category: "quick"},
	}); err != nil {
		t.Fatalf("seed team failed: %v", err)
	}
	usage.Default().Add("sess-ui", "", "claude-sonnet-4-6", 500, 200, 0.01, 1, 2)

	// 1. HasUI=true: /omh-model interactive loop picks category -> preset -> persists -> done.
	modelStep := 0
	dagStep := 0
	botsStep := 0
	hUI := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "tui",
		HasUI: sdk.Bool(true),
		Cwd:   t.TempDir(),
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method != "ui.select" {
				return map[string]any{}, ""
			}
			title, _ := args["title"].(string)
			rawOpts, _ := args["options"].([]any)
			opts := make([]string, 0, len(rawOpts))
			for _, o := range rawOpts {
				if s, ok := o.(string); ok {
					opts = append(opts, s)
				}
			}
			switch {
			case strings.Contains(title, "OMH Model Routing"):
				modelStep++
				if modelStep == 1 {
					for _, o := range opts {
						if strings.Contains(o, "quick") {
							return map[string]any{"ok": true, "selected": o}, ""
						}
					}
				}
				return map[string]any{"ok": true, "selected": opts[len(opts)-1]}, ""
			case strings.Contains(title, "OMH Model Chain [quick]"):
				return map[string]any{"ok": true, "selected": opts[0]}, ""
			case strings.Contains(title, "OMH DAG Runs"):
				dagStep++
				if dagStep == 1 {
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				}
				return map[string]any{"ok": true, "selected": opts[len(opts)-1]}, ""
			case strings.Contains(title, "DAG dag-ui-1"):
				// Advance first ready node ("plan"), then go back.
				if dagStep == 1 {
					dagStep++
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				}
				return map[string]any{"ok": true, "selected": opts[len(opts)-1]}, ""
			case strings.Contains(title, "Set status for node"):
				return map[string]any{"ok": true, "selected": "completed"}, ""
			case strings.Contains(title, "OMH Bot Board — Profiles"):
				botsStep++
				if botsStep == 1 {
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				}
				return map[string]any{"ok": true, "selected": opts[len(opts)-1]}, ""
			case strings.Contains(title, "Bot Board ["):
				if botsStep == 1 {
					botsStep++
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				}
				return map[string]any{"ok": true, "selected": opts[len(opts)-1]}, ""
			default:
				// Level 3 detail view -> back
				return map[string]any{"ok": true, "selected": opts[len(opts)-1]}, ""
			}
		},
	})

	if errMsg := hUI.Command("omh-model", ""); errMsg != "" {
		t.Fatalf("interactive /omh-model failed: %s", errMsg)
	}
	if errMsg := hUI.Command("dag", ""); errMsg != "" {
		t.Fatalf("interactive /dag failed: %s", errMsg)
	}
	if errMsg := hUI.Command("bots", ""); errMsg != "" {
		t.Fatalf("interactive /bots failed: %s", errMsg)
	}
	if len(hUI.CallsTo("ui.select")) < 7 {
		t.Fatalf("expected interactive /omh-model, /dag, /bots to invoke ui.select across flows, got %d calls", len(hUI.CallsTo("ui.select")))
	}
	// Verify /omh-model saved the override to routing/model-chains.json.
	if _, err := os.Stat(filepath.Join(omhHome, "routing", "model-chains.json")); err != nil {
		t.Fatalf("expected routing/model-chains.json written by interactive /omh-model: %v", err)
	}
	// Verify /dag advanced node "plan" to completed.
	updatedDAG, err := dags.Get("dag-ui-1")
	if err != nil {
		t.Fatalf("reload dag-ui-1 failed: %v", err)
	}
	if updatedDAG.Nodes[0].Status != "completed" {
		t.Errorf("expected interactive /dag to advance plan -> completed, got %q", updatedDAG.Nodes[0].Status)
	}

	// 2. HasUI=false: /omh-model, /dag, /bots fall back cleanly without calling ui.select.
	hNoUI := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "print",
		HasUI: sdk.Bool(false),
		Cwd:   t.TempDir(),
	})
	for _, cmd := range []string{"omh-model", "dag", "bots"} {
		if errMsg := hNoUI.Command(cmd, ""); errMsg != "" {
			t.Fatalf("non-UI /%s failed: %s", cmd, errMsg)
		}
	}
	if got := len(hNoUI.CallsTo("ui.select")); got != 0 {
		t.Fatalf("expected 0 ui.select calls when HasUI=false, got %d", got)
	}
}
