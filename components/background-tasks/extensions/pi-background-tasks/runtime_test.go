package pi_background_tasks_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// The port's own layer-1 tests: the task runtime against a fake host that records every call. What the original does
// is pinned by the twins and the scenarios; these tests pin how the Go runtime is wired to the host.

const refusalHead = "@pi-in-go/pigpen-background-tasks (Go port) does not support "
const refusalTail = " yet. Install the original extension, npm:@vanillagreen/pi-background-tasks@2.2.0, which runs unchanged in PiG."

func started(t *testing.T, config map[string]any) *Host {
	t.Helper()
	if config == nil {
		config = map[string]any{}
	}
	if _, ok := config["resourceControlEnabled"]; !ok {
		config["resourceControlEnabled"] = false
	}
	agentWith(t, config)
	bg.ResetSettings()
	trusted := func(method string, args map[string]any) (map[string]any, string) {
		if method == "isProjectTrusted" {
			return map[string]any{"trusted": false}, ""
		}
		return map[string]any{}, ""
	}
	h := StartHost(t, bg.Extension(), HostOptions{OnCall: trusted})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	t.Cleanup(func() { h.Fire("session_shutdown", map[string]any{"reason": "quit"}) })
	return h
}

type toolOut struct {
	Content string         `json:"content"`
	Details map[string]any `json:"details"`
}

func text(t *testing.T, raw json.RawMessage) toolOut {
	t.Helper()
	var out toolOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("tool result %s: %v", raw, err)
	}
	return out
}

func (o toolOut) text() string { return o.Content }

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
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

func TestRefusalMessage(t *testing.T) {
	eq(t, bg.Refusal("x"), refusalHead+"x"+refusalTail, "the exact text")
}

func TestSpawnLogStopAndClearRunARealProcess(t *testing.T) {
	h := started(t, nil)
	raw, failure := h.Tool("bg_task", map[string]any{"action": "spawn", "command": "echo hello; sleep 30", "notifyOnExit": false})
	eq(t, failure, "", "spawn")
	spawned := text(t, raw)
	if !strings.HasPrefix(spawned.text(), "Started bg-1 (pid ") {
		t.Fatalf("spawn text: %q", spawned.text())
	}
	eventually(t, "the output", func() bool {
		raw, _ := h.Tool("bg_task", map[string]any{"action": "log", "id": "bg-1"})
		return strings.Contains(text(t, raw).text(), "hello")
	})
	raw, _ = h.Tool("bg_task", map[string]any{"action": "list"})
	if !strings.Contains(text(t, raw).text(), "bg-1") || !strings.Contains(text(t, raw).text(), "running") {
		t.Errorf("list: %q", text(t, raw).text())
	}
	raw, failure = h.Tool("bg_task", map[string]any{"action": "stop", "id": "bg-1"})
	eq(t, failure, "", "stop")
	if !strings.Contains(text(t, raw).text(), "bg-1") {
		t.Errorf("stop text: %q", text(t, raw).text())
	}
	raw, _ = h.Tool("bg_task", map[string]any{"action": "clear"})
	if !strings.HasPrefix(text(t, raw).text(), "Removed ") {
		t.Errorf("clear: %q", text(t, raw).text())
	}
	_, failure = h.Tool("bg_task", map[string]any{"action": "log", "id": "bg-99"})
	eq(t, failure, "No background task matched that id or pid.", "an unknown id")
	_, failure = h.Tool("bg_status", map[string]any{"action": "log", "pid": 1})
	eq(t, failure, "No background task matched that pid.", "an unknown pid")
}

func TestAnExitWakeIsSentAsACustomMessage(t *testing.T) {
	h := started(t, nil)
	_, failure := h.Tool("bg_task", map[string]any{"action": "spawn", "command": "echo done", "notifyOnExit": true})
	eq(t, failure, "", "spawn")
	eventually(t, "the exit wake", func() bool { return len(h.CallsTo("sendMessage")) > 0 })
	call := h.CallsTo("sendMessage")[0]
	if !strings.Contains(string(mustJSON(call.Args)), "bg-1") {
		t.Errorf("wake does not name the task: %v", call.Args)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestSpawnRefusesAPatternItCannotTranslate(t *testing.T) {
	h := started(t, nil)
	_, failure := h.Tool("bg_task", map[string]any{"action": "spawn", "command": "true", "notifyOnOutput": true, "notifyPattern": "/(?<a>x)(?<a>y)/"})
	if !strings.HasPrefix(failure, refusalHead) || !strings.HasSuffix(failure, refusalTail) {
		t.Errorf("failure: %q", failure)
	}
}

func TestEveryCommandIsRegisteredAndAnswers(t *testing.T) {
	h := started(t, nil)
	for _, name := range []string{"bg", "bg:list", "bg:next", "bg:clear", "bg:run", "bg:stop"} {
		eq(t, h.cmds[name], true, "/"+name+" is registered")
	}
	before := len(notifications(h))
	eq(t, h.Command("bg:list", ""), "", "/bg:list")
	if len(notifications(h)) == before {
		t.Errorf("/bg:list notified nothing")
	}
}

func TestDashboardAndWidgetShortcutsAreRegistered(t *testing.T) {
	h := started(t, nil)
	for _, key := range []string{"alt+shift+h", "f5", "alt+h"} {
		known, failure := h.shortcut(key)
		eq(t, known, true, "default shortcut "+key)
		eq(t, failure, "", "shortcut "+key)
	}
	known, _ := h.shortcut("alt+.")
	eq(t, known, true, "the arm-next-bash shortcut is registered")
	h = started(t, map[string]any{"dashboardShortcut": "none", "backgroundBashShortcut": "ctrl+b", "widgetToggleShortcut": "f5"})
	for key, want := range map[string]bool{"alt+shift+h": false, "f5": true, "alt+.": false, "ctrl+b": true, "alt+h": false} {
		known, _ := h.shortcut(key)
		eq(t, known, want, "shortcut "+key)
	}
}

func TestADisabledPackageRegistersNothing(t *testing.T) {
	h := started(t, map[string]any{"enabled": false})
	eq(t, len(h.tools), 0, "tools")
	eq(t, len(h.cmds), 0, "commands")
	eq(t, h.Registered("tool_call"), false, "events")
}

func fired(h *Host, command string) (reason string, blocked bool, input string) {
	raw := h.Fire("tool_call", map[string]any{"toolName": "bash", "toolCallId": "c1", "input": map[string]any{"command": command}})
	var res struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	return res.Reason, res.Block, ""
}

func TestAWatchStyleBashCommandIsMovedToATaskNotRefused(t *testing.T) {
	h := started(t, nil)
	_, block, _ := fired(h, "watch -n 5 ls")
	eq(t, block, false, "not blocked: the command is replaced by the acknowledgement")
	raw, _ := h.Tool("bg_task", map[string]any{"action": "list"})
	if !strings.Contains(text(t, raw).text(), "watch -n 5 ls") {
		t.Errorf("the task was not spawned: %q", text(t, raw).text())
	}
	before := len(h.CallsTo("ui.notify"))
	_, _, _ = fired(h, "ls -la")
	_, _, _ = fired(h, "   ")
	eq(t, len(h.CallsTo("ui.notify")), before, "an ordinary or blank command spawns nothing")
	raw = h.Fire("tool_call", map[string]any{"toolName": "read", "toolCallId": "c2", "input": map[string]any{"command": "watch ls"}})
	eq(t, len(raw) == 0 || string(raw) == "null", true, "only bash is considered")
}

func TestAUserBashCommandThatIsMovedIsAnsweredWithTheAcknowledgement(t *testing.T) {
	h := started(t, nil)
	raw := h.Fire("user_bash", map[string]any{"command": "watch ls", "excludeFromContext": false, "cwd": h.opts.Cwd})
	var res struct {
		Result struct {
			Output   string `json:"output"`
			ExitCode int    `json:"exitCode"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &res)
	if !strings.HasPrefix(res.Result.Output, "Started bg-1 (pid ") || !strings.Contains(res.Result.Output, "Reason: watch-style command.") {
		t.Errorf("output: %q", res.Result.Output)
	}
	eq(t, res.Result.ExitCode, 0, "exit code")
	raw = h.Fire("user_bash", map[string]any{"command": "ls", "cwd": h.opts.Cwd})
	eq(t, len(raw) == 0 || string(raw) == "null", true, "an ordinary command is left to the host")
}

func TestAutoBackgroundingCanBeSwitchedOffAndAnUntranslatablePatternIsRefused(t *testing.T) {
	h := started(t, map[string]any{"autoBackgroundBash": false})
	_, block, _ := fired(h, "watch ls")
	eq(t, block, false, "autoBackgroundBash false: the command runs")
	raw, _ := h.Tool("bg_task", map[string]any{"action": "list"})
	if strings.Contains(text(t, raw).text(), "watch ls") {
		t.Errorf("a task was spawned: %q", text(t, raw).text())
	}
	h = started(t, map[string]any{"autoBackgroundPatterns": "(?<a>x)(?<a>y)"})
	reason, block, _ := fired(h, "ls")
	eq(t, block, true, "a pattern that cannot be honored refuses the bash command")
	if !strings.HasPrefix(reason, refusalHead) {
		t.Errorf("reason: %q", reason)
	}
}

var _ = sdk.Bool
