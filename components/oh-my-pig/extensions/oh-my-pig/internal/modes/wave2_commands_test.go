package modes

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

func TestOpenSlashCommand(t *testing.T) {
	origOpen := openURLFn
	t.Cleanup(func() { openURLFn = origOpen })

	var opened []string
	openURLFn = func(href string) error {
		opened = append(opened, href)
		return nil
	}

	// 1. No link in branch.
	_, h := newTestExtension(t, hostOptions{SessionID: "s-open-empty"})
	h.callCommand("open", "")
	if got := h.lastNotify(); got != "No link to open." {
		t.Fatalf("expected No link to open., got %q", got)
	}

	// 2. Invalid arg -> usage.
	h.callCommand("open", "bogus")
	if got := h.lastNotify(); !strings.Contains(got, "Usage: /open [link]") {
		t.Fatalf("expected usage, got %q", got)
	}

	// 3. Opens last assistant link.
	_, h2 := newTestExtension(t, hostOptions{
		SessionID: "s-open-link",
		Branch: []map[string]any{
			{
				"id":   "a1",
				"type": "message",
				"message": map[string]any{
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "text", "text": "See https://example.com/one and https://example.com/two."},
					},
				},
			},
		},
	})
	h2.callCommand("open", "link")
	if len(opened) != 1 || opened[0] != "https://example.com/two" {
		t.Fatalf("expected https://example.com/two, got %v (notify=%q)", opened, h2.lastNotify())
	}
	if got := h2.lastNotify(); got != "Opening https://example.com/two" {
		t.Fatalf("expected Opening notice, got %q", got)
	}

	// 4. defaultOpenURL rejects non-http(s) schemes.
	if err := defaultOpenURL("file:///etc/passwd"); err == nil {
		t.Fatalf("expected file:// scheme rejection")
	}
}

func TestSkillfulSlashCommand(t *testing.T) {
	origSetting := Setting
	origSetSetting := SetSetting
	t.Cleanup(func() {
		Setting = origSetting
		SetSetting = origSetSetting
	})

	skillfulVal := true
	Setting = func(_ sdk.Context, key string) any {
		if key == "skillful" {
			return skillfulVal
		}
		return nil
	}
	SetSetting = func(_ sdk.Context, key string, value any, persist bool) error {
		if key == "skillful" {
			if persist {
				t.Errorf("/skillful must be session-only (persist=false)")
			}
			skillfulVal = value.(bool)
		}
		return nil
	}

	hasUI := true
	_, h := newTestExtension(t, hostOptions{SessionID: "s-skillful", HasUI: &hasUI})

	h.callCommand("skillful", "status")
	if got := h.lastNotify(); got != "Skill listing: on." {
		t.Fatalf("expected Skill listing: on., got %q", got)
	}

	h.callCommand("skillful", "")
	if skillfulVal || h.lastNotify() != "Skill listing disabled for this session." {
		t.Fatalf("expected disabled, got val=%v notify=%q", skillfulVal, h.lastNotify())
	}

	h.callCommand("skillful", "on")
	if !skillfulVal || h.lastNotify() != "Skill listing enabled for this session." {
		t.Fatalf("expected enabled, got val=%v notify=%q", skillfulVal, h.lastNotify())
	}

	h.callCommand("skillful", "wat")
	if got := h.lastNotify(); got != "Usage: /skillful [on|off|status]" {
		t.Fatalf("expected usage, got %q", got)
	}
}

func TestModelPresetSlashCommand(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("HOME", cfgHome)
	currentModel := "anthropic/claude-sonnet-4-5"
	currentThinking := "high"
	hasUI := false
	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-preset",
		HasUI:     &hasUI,
		Model:     "claude-sonnet-4-5",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "getConfigHome", "configHome":
				return map[string]any{"path": cfgHome}, ""
			case "getThinkingLevel":
				return map[string]any{"level": currentThinking}, ""
			case "setThinkingLevel":
				if lvl, ok := args["level"].(string); ok {
					currentThinking = lvl
				}
				return map[string]any{}, ""
			case "setModelNamed":
				if m, ok := args["model"].(string); ok {
					currentModel = m
				} else if m, ok := args["name"].(string); ok {
					currentModel = m
				}
				return map[string]any{"ok": true}, ""
			}
			return nil, ""
		},
	})

	// 1. Empty list.
	h.callCommand("modelpreset", "list")
	if got := h.lastNotify(); got != noPresetsMessage {
		t.Fatalf("expected no presets message, got %q", got)
	}

	// 2. Invalid name rejected.
	h.callCommand("modelpreset", "save 123bad")
	if got := h.lastNotify(); !strings.Contains(got, "Invalid preset name") {
		t.Fatalf("expected invalid preset name, got %q", got)
	}

	// 3. Save preset.
	h.callCommand("modelpreset", "save fast")
	if got := h.lastNotify(); got != `Saved model preset "fast"` {
		t.Fatalf("expected saved notice, got %q", got)
	}

	// 4. List shows saved preset.
	h.callCommand("modelpreset", "list")
	if got := h.lastNotify(); got != "Model presets: fast" {
		t.Fatalf("expected Model presets: fast, got %q", got)
	}

	// 5. Switch restores model + thinking level.
	_ = currentModel
	currentThinking = "low"
	h.callCommand("modelpreset", "switch fast")
	if currentThinking != "high" {
		t.Fatalf("expected thinking restored to high, got %q", currentThinking)
	}
	if got := h.lastNotify(); !strings.Contains(got, `Switched to model preset "fast"`) {
		t.Fatalf("expected switched notice, got %q", got)
	}

	// 6. Delete preset.
	h.callCommand("modelpreset", "delete fast")
	if got := h.lastNotify(); got != `Deleted model preset "fast"` {
		t.Fatalf("expected deleted notice, got %q", got)
	}
}

func TestToolsCommandMarkdownAndCompact(t *testing.T) {
	allTools := []map[string]any{
		{"name": "read", "description": "Read files | directories\nwith line ranges."},
		{"name": "bash", "description": "Run shell commands."},
		{"name": "find", "description": "Find files."},
	}
	activeTools := []string{"read", "bash"}

	// Interactive UI -> Markdown table.
	hasUI := true
	_, hUI := newTestExtension(t, hostOptions{
		SessionID:   "s-tools-ui",
		HasUI:       &hasUI,
		AllTools:    allTools,
		ActiveTools: activeTools,
	})
	hUI.callCommand("tools", "")
	md := hUI.lastNotify()
	if !strings.Contains(md, "| Tool | Description |") || !strings.Contains(md, "| `read` | Read files \\| directories with line ranges. |") {
		t.Fatalf("expected markdown table with escaped pipe, got:\n%s", md)
	}
	if strings.Contains(md, "`find`") {
		t.Fatalf("inactive tool find should not appear in interactive table:\n%s", md)
	}

	// Non-UI -> compact */- format.
	noUI := false
	_, hPrint := newTestExtension(t, hostOptions{
		SessionID:   "s-tools-print",
		HasUI:       &noUI,
		AllTools:    allTools,
		ActiveTools: activeTools,
	})
	hPrint.callCommand("tools", "")
	compact := hPrint.lastNotify()
	if !strings.Contains(compact, "* read") || !strings.Contains(compact, "- find") {
		t.Fatalf("expected compact */- format, got:\n%s", compact)
	}

	// BuildToolsMarkdown empty case.
	if got := BuildToolsMarkdown(nil, nil); got != "No tools are currently visible to the agent." {
		t.Fatalf("unexpected empty BuildToolsMarkdown: %q", got)
	}
	// BuildToolsMarkdown with mounted command.
	got := BuildToolsMarkdown(
		[]sdk.ToolInfo{{Name: "bash", Description: "Shell"}},
		[]toolcmd.CommandEntry{{Name: "lsp", Summary: "Language server"}},
	)
	if !strings.Contains(got, "| `lsp` | Language server |") {
		t.Fatalf("expected mounted command in table:\n%s", got)
	}
}
