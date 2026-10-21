package pi_background_tasks_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// Everything the port does not run yet is refused with one message that names the original, and nothing runs. These are
// the port's own tests: the original has no such refusals.

const refusalHead = "@pi-in-go/pigpen-background-tasks (Go port) does not support "
const refusalTail = " yet. Install the original extension, npm:@vanillagreen/pi-background-tasks@2.2.0, which runs unchanged in PiG."

func started(t *testing.T, config map[string]any) *Host {
	t.Helper()
	agentWith(t, config)
	bg.ResetSettings()
	trusted := func(method string, args map[string]any) (map[string]any, string) {
		if method == "isProjectTrusted" {
			return map[string]any{"trusted": false}, ""
		}
		return map[string]any{}, ""
	}
	return StartHost(t, bg.Extension(), HostOptions{OnCall: trusted})
}

func TestRefusalMessage(t *testing.T) {
	eq(t, bg.Refusal("x"), refusalHead+"x"+refusalTail, "the exact text")
}

func TestToolsKeepTheOriginalsDefinitions(t *testing.T) {
	h := started(t, nil)
	if !h.tools["bg_task"] || !h.tools["bg_status"] {
		t.Fatalf("tools registered: %v", h.tools)
	}
	var want map[string]struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(bg.ToolDefsJSON(), &want); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(want["bg_task"].Description, "Spawn, inspect, and stop explicit background shell tasks") {
		t.Errorf("bg_task description: %q", want["bg_task"].Description)
	}
}

func TestEveryToolActionIsRefused(t *testing.T) {
	h := started(t, nil)
	for tool, actions := range map[string][]string{
		"bg_task":   {"spawn", "list", "log", "stop", "clear"},
		"bg_status": {"list", "log", "stop"},
	} {
		for _, action := range actions {
			t.Run(tool+" "+action, func(t *testing.T) {
				_, failure := h.Tool(tool, map[string]any{"action": action, "command": "sleep 1"})
				eq(t, failure, bg.Refusal("the `"+tool+"` tool (action `"+action+"`)"), "refusal")
			})
		}
	}
}

func TestEveryCommandIsRefused(t *testing.T) {
	h := started(t, nil)
	for _, name := range []string{"bg", "bg:list", "bg:next", "bg:clear", "bg:run", "bg:stop"} {
		before := len(notifications(h))
		h.Command(name, "x")
		got := notifications(h)
		if len(got) != before+1 || !strings.HasPrefix(got[before], refusalHead) || !strings.HasSuffix(got[before], refusalTail) {
			t.Errorf("/%s: %v", name, got)
		}
	}
	eq(t, h.cmds["bg"], true, "/bg is registered")
}

func TestShortcutsRegisterFromTheSettingsReadAtLoad(t *testing.T) {
	h := started(t, nil)
	for _, key := range []string{"alt+shift+h", "f5", "alt+.", "alt+h"} {
		known, failure := h.shortcut(key)
		if !known || failure != "" {
			t.Errorf("default shortcut %s: known=%v failure=%q", key, known, failure)
		}
	}
	h = started(t, map[string]any{"dashboardShortcut": "none", "backgroundBashShortcut": "ctrl+b", "widgetToggleShortcut": "f5"})
	for key, want := range map[string]bool{"alt+shift+h": false, "f5": true, "alt+.": false, "ctrl+b": true, "alt+h": false} {
		known, _ := h.shortcut(key)
		eq(t, known, want, "shortcut "+key)
	}
	h = started(t, map[string]any{"dashboardShortcut": "F5"})
	known, _ := h.shortcut("F5")
	eq(t, known, true, "a dashboard shortcut spelled F5 is registered as given")
	known, _ = h.shortcut("f5")
	eq(t, known, false, "and f5 is not registered a second time")
}

func TestADisabledPackageRegistersNothing(t *testing.T) {
	h := started(t, map[string]any{"enabled": false})
	eq(t, len(h.tools), 0, "tools")
	eq(t, len(h.cmds), 0, "commands")
	eq(t, h.Registered("tool_call"), false, "events")
}

func blocked(t *testing.T, h *Host, command string) (bool, string) {
	t.Helper()
	raw := h.Fire("tool_call", map[string]any{"toolName": "bash", "toolCallId": "c1", "input": map[string]any{"command": command}})
	var res struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	return res.Block, res.Reason
}

func TestABashCommandThatWouldBeMovedToTheBackgroundIsRefusedNotRunInTheForeground(t *testing.T) {
	h := started(t, nil)
	block, reason := blocked(t, h, "watch -n 5 ls")
	eq(t, block, true, "watch is refused")
	eq(t, reason, bg.Refusal("moving a bash command into a background task (watch-style command)"), "reason")
	block, _ = blocked(t, h, "tail -f /var/log/syslog")
	eq(t, block, true, "follow")
	block, _ = blocked(t, h, "while true; do echo x; sleep 10; done")
	eq(t, block, true, "polling loop")
	block, _ = blocked(t, h, "ls -la")
	eq(t, block, false, "an ordinary command runs")
	block, _ = blocked(t, h, "   ")
	eq(t, block, false, "a blank command runs")
	raw := h.Fire("tool_call", map[string]any{"toolName": "read", "toolCallId": "c2", "input": map[string]any{"command": "watch ls"}})
	eq(t, len(raw) == 0 || string(raw) == "null", true, "only bash is considered")
}

func TestAUserBashCommandThatWouldBeMovedIsAnsweredWithTheRefusal(t *testing.T) {
	h := started(t, nil)
	raw := h.Fire("user_bash", map[string]any{"command": "watch ls", "excludeFromContext": false, "cwd": h.opts.Cwd})
	var res struct {
		Result struct {
			Output   string `json:"output"`
			ExitCode int    `json:"exitCode"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &res)
	eq(t, res.Result.Output, bg.Refusal("moving a bash command into a background task (watch-style command)"), "output")
	eq(t, res.Result.ExitCode, 1, "exit code")
	raw = h.Fire("user_bash", map[string]any{"command": "ls", "cwd": h.opts.Cwd})
	eq(t, len(raw) == 0 || string(raw) == "null", true, "an ordinary command is left to the host")
}

func TestAutoBackgroundingCanBeSwitchedOffAndUserPatternsAreRefused(t *testing.T) {
	h := started(t, map[string]any{"autoBackgroundBash": false})
	block, _ := blocked(t, h, "watch ls")
	eq(t, block, false, "autoBackgroundBash false: the command runs")
	h = started(t, map[string]any{"autoBackgroundPatterns": "# comment\n\n"})
	block, _ = blocked(t, h, "ls")
	eq(t, block, false, "only comments and blanks: no pattern, the command runs")
	h = started(t, map[string]any{"autoBackgroundPatterns": "^make deploy"})
	block, reason := blocked(t, h, "ls")
	eq(t, block, true, "a user pattern cannot be honored, so a bash command is refused")
	eq(t, reason, bg.Refusal("the `autoBackgroundPatterns` setting"), "reason")
}

var _ = sdk.Bool
