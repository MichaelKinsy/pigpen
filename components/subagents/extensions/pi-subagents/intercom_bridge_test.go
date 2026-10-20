package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of intercom-bridge.test.ts.

func TestIntercomBridgeTwins(t *testing.T) {
	const f = "intercom-bridge"
	withTempHome(t)
	makeAgent := func(a AgentConfig) AgentConfig {
		a.Name, a.Description, a.SystemPrompt = "worker", "Test worker", "Base prompt"
		a.SystemPromptMode, a.Source, a.FilePath = "replace", "user", "/tmp/worker.md"
		return a
	}
	_ = makeAgent

	tw(t, f, "defaults unknown values to always", func(t *testing.T) {
		equal(t, resolveIntercomBridgeMode(nil), "always", "undefined")
		equal(t, resolveIntercomBridgeMode("nope"), "always", "unknown")
	})
	tw(t, f, "accepts explicit modes", func(t *testing.T) {
		for _, m := range []string{"off", "fork-only", "always"} {
			equal(t, resolveIntercomBridgeMode(m), m, m)
		}
	})
	tw(t, f, "prefers an explicit session name", func(t *testing.T) {
		equal(t, resolveIntercomSessionTarget("planner", "session-12345678", "session-stableabcdef"), "planner", "target")
	})
	tw(t, f, "uses a runtime-only subagent chat alias when unnamed", func(t *testing.T) {
		equal(t, resolveIntercomSessionTarget("", "session-12345678", ""), "subagent-chat-12345678", "target")
	})
	tw(t, f, "uses the current pi-intercom runtime id for unnamed fallback when present", func(t *testing.T) {
		equal(t, resolveIntercomSessionTarget("", "session-12345678", "session-stableabcdef"), "subagent-chat-stableabcdef", "short")
		equal(t, resolveIntercomSessionTarget("", "session-12345678", "session-abcdefghijklmnopqrst"), "subagent-chat-abcdefghijklmnopqr", "sliced to 18")
	})
	tw(t, f, "builds stable child session targets from run metadata", func(t *testing.T) {
		equal(t, resolveSubagentIntercomTarget("78f659a3", "worker", nil), "subagent-worker-78f659a3", "plain")
		one := 1
		equal(t, resolveSubagentIntercomTarget("78f659a3", "senior executor", &one), "subagent-senior-executor-78f659a3-2", "indexed")
	})
	tw(t, f, "reports the native supervisor channel as available without external package discovery", func(t *testing.T) {
		d := diagnoseIntercomBridge(js(t, `{"mode":"always"}`), "fresh", "main")
		equal(t, [4]any{d.Active, d.WantsIntercom, d.SupervisorChannelAvailable, d.ExtensionDir}, [4]any{true, true, true, nativeIntercomExtensionDir}, "diagnostic")
	})
	tw(t, f, "does not read external intercom config when bridge mode is off", func(t *testing.T) {
		d := diagnoseIntercomBridge(js(t, `{"mode":"off"}`), "fresh", "main")
		equal(t, [2]any{d.Active, d.Reason}, [2]any{false, "bridge mode is off"}, "diagnostic")
	})
	tw(t, f, "activates when mode/context permit and an orchestrator target exists", func(t *testing.T) {
		b := resolveIntercomBridge(js(t, `{"mode":"fork-only"}`), nil, "fork", "main")
		equal(t, [4]any{b.Active, b.ResultDelivery, b.OrchestratorTarget, b.ExtensionDir}, [4]any{true, false, "main", nativeIntercomExtensionDir}, "bridge")
	})
	tw(t, f, "uses a per-run override instead of the global config", func(t *testing.T) {
		off, _ := validateIntercomBridgeConfig(js(t, `{"mode":"off"}`), "b")
		b := resolveIntercomBridge(js(t, `{"mode":"always","resultDelivery":true}`), &off, "fresh", "main")
		equal(t, [3]any{b.Active, b.Mode, b.ResultDelivery}, [3]any{false, "off", false}, "bridge")
	})
	tw(t, f, "can disable external grouped-result delivery without disabling supervisor coordination", func(t *testing.T) {
		b := resolveIntercomBridge(js(t, `{"mode":"always","resultDelivery":false}`), nil, "fresh", "main")
		equal(t, [2]bool{b.Active, b.ResultDelivery}, [2]bool{true, false}, "bridge")
	})
	tw(t, f, "stays inactive for fresh context when mode is fork-only", func(t *testing.T) {
		equal(t, resolveIntercomBridge(js(t, `{"mode":"fork-only"}`), nil, "fresh", "main").Active, false, "active")
	})
	tw(t, f, "loads custom instructions from instructionFile", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "bridge.md")
		if err := os.WriteFile(file, []byte("Custom bridge for {orchestratorTarget}\nUse ask then send."), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := newObject()
		cfg.set("mode", "always")
		cfg.set("instructionFile", file)
		b := resolveIntercomBridge(cfg, nil, "fresh", "main")
		equal(t, b.Active, true, "active")
		polMatches(t, b.Instruction, "Custom bridge for main")
		equal(t, b.InterpolatesOrchestratorTarget, true, "interpolates")
	})
	tw(t, f, "keeps the default instruction independent of the parent session", func(t *testing.T) {
		cfg := js(t, `{"mode":"always"}`)
		first := resolveIntercomBridge(cfg, nil, "fresh", "subagent-chat-first")
		second := resolveIntercomBridge(cfg, nil, "fresh", "subagent-chat-second")
		equal(t, [2]bool{first.Active, first.InterpolatesOrchestratorTarget}, [2]bool{true, false}, "first")
		equal(t, first.Instruction, second.Instruction, "instruction")
		equal(t, strings.Contains(first.Instruction, "subagent-chat-first"), false, "names the session")
		equal(t, first.OrchestratorTarget, "subagent-chat-first", "target")
	})
	tw(t, f, "uses stronger default instructions for fork-aware coordination", func(t *testing.T) {
		b := resolveIntercomBridge(js(t, `{"mode":"always"}`), nil, "fork", "main")
		equal(t, b.Active, true, "active")
		for _, p := range []string{"(?i)reference-only", "(?i)normal assistant text", "contact_supervisor", "need_decision", "progress_update", "(?i)focused task result"} {
			polMatches(t, b.Instruction, p)
		}
	})

	active := intercomBridgeState{Active: true, Mode: "always", ResultDelivery: true, OrchestratorTarget: "main", ExtensionDir: nativeIntercomExtensionDir,
		Instruction: "Intercom orchestration channel:\n- Need a decision or blocked: contact_supervisor({ reason: \"need_decision\", message: \"<question>\" })\n- Blocked/update: contact_supervisor({ reason: \"progress_update\", message: \"UPDATE: <summary>\" })"}
	count := func(list []string, name string) int {
		n := 0
		for _, x := range list {
			if x == name {
				n++
			}
		}
		return n
	}

	tw(t, f, "preserves the parsed definition digest across the overlay", func(t *testing.T) {
		agent := makeAgent(AgentConfig{Tools: []string{"read"}})
		parsed := agentDefinitionDigest(agent)
		bridged := applyIntercomBridgeToAgent(agent, active)
		equal(t, bridged.DefinitionDigest, parsed, "digest")
		equal(t, agentDefinitionDigest(bridged), parsed, "recomputed digest")
		equal(t, agentDefinitionDigest(applyIntercomBridgeToAgent(bridged, active)), parsed, "second overlay")
		equal(t, agent.DefinitionDigest, "", "the input is not changed")
	})
	tw(t, f, "injects contact_supervisor and prompt instructions", func(t *testing.T) {
		updated := applyIntercomBridgeToAgent(makeAgent(AgentConfig{Tools: []string{"read", "bash"}}), active)
		equal(t, updated.Tools, []string{"read", "bash", "contact_supervisor"}, "tools")
		polMatches(t, updated.SystemPrompt, "Intercom orchestration channel:")
		polMatches(t, updated.SystemPrompt, "contact_supervisor")
		polNotMatches(t, updated.SystemPrompt, "Generic intercom")
	})
	tw(t, f, "is idempotent", func(t *testing.T) {
		first := applyIntercomBridgeToAgent(makeAgent(AgentConfig{Tools: []string{"read"}}), active)
		second := applyIntercomBridgeToAgent(first, active)
		equal(t, count(second.Tools, "intercom"), 0, "intercom tools")
		equal(t, count(second.Tools, "contact_supervisor"), 1, "contact_supervisor tools")
		equal(t, second.SystemPrompt, first.SystemPrompt, "prompt")
	})
	tw(t, f, "does not block native supervisor tools for agents with explicit extension allowlists", func(t *testing.T) {
		updated := applyIntercomBridgeToAgent(makeAgent(AgentConfig{Tools: []string{"read"}, Extensions: []string{"/tmp/other-extension/index.ts"}}), active)
		equal(t, updated.Tools, []string{"read", "contact_supervisor"}, "tools")
		polMatches(t, updated.SystemPrompt, "contact_supervisor")
	})
	tw(t, f, "preserves explicitly requested external intercom tools", func(t *testing.T) {
		updated := applyIntercomBridgeToAgent(makeAgent(AgentConfig{Tools: []string{"read", "intercom"}}), active)
		equal(t, updated.Tools, []string{"read", "intercom", "contact_supervisor"}, "tools")
	})
	tw(t, f, "does not widen explicit empty or MCP-only builtin allowlists", func(t *testing.T) {
		empty := applyIntercomBridgeToAgent(makeAgent(AgentConfig{Tools: []string{}}), active)
		equal(t, empty.Tools, []string{}, "empty tools")
		polMatches(t, empty.SystemPrompt, "contact_supervisor")
		mcp := applyIntercomBridgeToAgent(makeAgent(AgentConfig{Tools: []string{}, McpDirectTools: []string{"github/search_repositories"}}), active)
		equal(t, mcp.Tools, []string{}, "mcp-only tools")
		equal(t, mcp.McpDirectTools, []string{"github/search_repositories"}, "mcp tools")
		polMatches(t, mcp.SystemPrompt, "contact_supervisor")
	})

	tw(t, f, "accepts the documented fields and drops nothing", func(t *testing.T) {
		cfg, msg := validateIntercomBridgeConfig(js(t, `{"mode":"fork-only","instructionFile":"./bridge.md","resultDelivery":true}`), "intercomBridge")
		equal(t, msg, "", "message")
		equal(t, [3]any{cfg.Mode, *cfg.InstructionFile, *cfg.ResultDelivery}, [3]any{"fork-only", "./bridge.md", true}, "config")
		empty, msg := validateIntercomBridgeConfig(js(t, `{}`), "intercomBridge")
		equal(t, msg, "", "empty message")
		equal(t, empty.Mode == "" && empty.InstructionFile == nil && empty.ResultDelivery == nil, true, "empty config")
	})
	tw(t, f, "rejects unknown fields and wrong types with the caller's label", func(t *testing.T) {
		for in, want := range map[string]string{
			`null`:                     "bridge must be an object.",
			`[]`:                       "bridge must be an object.",
			`{"extra":1}`:              "bridge.extra is not supported.",
			`{"mode":"loud"}`:          "bridge.mode is invalid.",
			`{"instructionFile":1}`:    "bridge.instructionFile must be a string.",
			`{"resultDelivery":"yes"}`: "bridge.resultDelivery must be a boolean.",
		} {
			_, got := validateIntercomBridgeConfig(js(t, in), "bridge")
			equal(t, got, want, in)
		}
	})
}
