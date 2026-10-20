package pi_subagents_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The case of test/unit/tool-description.test.ts that registers the extension and reads the tool it registered.

func registeredSubagent(t *testing.T, config map[string]any, custom string) RegisteredTool {
	t.Helper()
	agentDir := tmp(t)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	b, _ := json.Marshal(config)
	writeFile(t, filepath.Join(agentDir, "extensions", "subagent", "config.json"), string(b))
	if custom != "" {
		writeFile(t, filepath.Join(agentDir, "subagent-tool-description.md"), custom)
	}
	return StartHost(t, sub.Extension(), HostOptions{Cwd: tmp(t)}).RegisteredTool("subagent")
}

func TestToolDescriptionRegistrationTwins(t *testing.T) {
	const f = "tool-description"
	tw(t, f, "registers split, full, compact, custom, and fallback descriptions from extension config", func(t *testing.T) {
		withTempHome(t)
		def := registeredSubagent(t, map[string]any{}, "")
		eq(t, def.Description, sub.DefaultSubagentToolDescription, "default description")
		if _, has := def.Parameters["properties"].(map[string]any)["step"]; has {
			t.Error("the schema has `step`")
		}
		for _, gone := range []string{"append-step", "approve-checkpoint", "reject-checkpoint"} {
			if strings.Contains(def.Description, gone) {
				t.Errorf("the default description names %s", gone)
			}
		}
		eq(t, def.PromptSnippet, sub.SubagentToolPromptSnippet, "default snippet")
		eq(t, def.PromptGuidelines, sub.SubagentToolPromptGuidelines, "default guidelines")

		full := registeredSubagent(t, map[string]any{"toolDescriptionMode": "full"}, "")
		eq(t, full.Description, sub.FullSubagentToolDescription, "full description")
		eq(t, full.PromptSnippet, "", "full snippet")
		eq(t, full.PromptGuidelines == nil, true, "full guidelines")

		compact := registeredSubagent(t, map[string]any{"toolDescriptionMode": "compact"}, "")
		eq(t, compact.Description, sub.CompactSubagentToolDescription, "compact description")
		eq(t, compact.PromptSnippet, "", "compact snippet")
		eq(t, compact.PromptGuidelines == nil, true, "compact guidelines")

		custom := registeredSubagent(t, map[string]any{"toolDescriptionMode": "custom"}, "Registered custom description.").Description
		mustContain(t, custom, "Registered custom description")
		mustContain(t, custom, "SAFETY-CRITICAL SUBAGENT GUIDANCE")

		eq(t, registeredSubagent(t, map[string]any{"toolDescriptionMode": "custom"}, "").Description, sub.FullSubagentToolDescription, "custom without a file")
		eq(t, registeredSubagent(t, map[string]any{"toolDescriptionMode": "tiny"}, "").Description, sub.FullSubagentToolDescription, "invalid mode")
	})
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%q does not contain %q", s, sub)
	}
}
