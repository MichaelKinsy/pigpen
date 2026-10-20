package powerline_footer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// /cd, /reply and the last-prompt line through the real SDK and a fake host (cdquote_ext.go).

const testSessionFile = `{"type":"session","version":3,"id":"0192f5a0-0000-7000-8000-000000000001","timestamp":"2026-10-20T10:00:00.000Z","cwd":"/x"}
{"type":"message","id":"a1b2c3d4","parentId":null,"timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"user","content":"hello","timestamp":1792490401000}}
{"type":"message","id":"b2c3d4e5","parentId":"a1b2c3d4","timestamp":"2026-10-20T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}],"timestamp":1792490402000}}
`

type cdRig struct {
	mode         string
	root         string
	cwd          string
	sessionFile  string // "" for an in-memory session
	sessionsRoot string
	switchResult map[string]any // the host's answer to switchSession
	switchError  string
	branch       []any
	noUI         bool
	selectAnswer map[string]any
	customAnswer map[string]any

	mu       sync.Mutex
	switched []map[string]any
}

func (r *cdRig) start(t *testing.T) *Host { return r.startWithAgentDir(t, t.TempDir()) }

func (r *cdRig) startWithAgentDir(t *testing.T, home string) *Host {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	r.root = t.TempDir()
	r.cwd = filepath.Join(r.root, "work")
	if err := os.MkdirAll(filepath.Join(r.cwd, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(r.cwd, "file.txt"), []byte("x"), 0o644)
	r.sessionsRoot = filepath.Join(r.root, "agent-sessions")
	if r.sessionFile == "<new>" {
		dir := filepath.Join(r.sessionsRoot, "--work--")
		_ = os.MkdirAll(dir, 0o755)
		r.sessionFile = filepath.Join(dir, "2026-10-20T10-00-00-000Z_0192f5a0-0000-7000-8000-000000000001.jsonl")
		if err := os.WriteFile(r.sessionFile, []byte(testSessionFile), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if r.mode == "" {
		r.mode = "tui"
	}
	var h *Host
	hasUI := !r.noUI
	h = StartHost(t, powerline.Extension(), HostOptions{Mode: r.mode, Cwd: r.cwd, HasUI: &hasUI, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionFile":
			if r.sessionFile == "" {
				return map[string]any{}, ""
			}
			return map[string]any{"sessionFile": r.sessionFile}, ""
		case "sessionRead":
			switch args["method"] {
			case "usesDefaultSessionDir":
				return true, ""
			case "getSessionDir":
				return filepath.Join(r.sessionsRoot, "--work--"), ""
			case "getBranch":
				return r.branch, ""
			}
			return nil, "unexpected sessionRead " + args["method"].(string)
		case "ui.select":
			if r.selectAnswer != nil {
				return r.selectAnswer, ""
			}
		case "ui.custom":
			if r.customAnswer != nil {
				return r.customAnswer, ""
			}
		case "ui.getEditorText":
			return map[string]any{"text": "draft"}, ""
		case "switchSession":
			r.mu.Lock()
			r.switched = append(r.switched, args)
			r.mu.Unlock()
			if r.switchError != "" {
				return nil, r.switchError
			}
			if handle, ok := args["withSession"].(string); ok && r.switchResult["cancelled"] != true {
				target := filepath.Join(r.cwd, "child")
				_, failure := h.roundTrip(map[string]any{"method": "with_session", "args": map[string]any{"handle": handle, "ready": map[string]any{"cwd": target, "mode": r.mode, "state": map[string]any{"hasUI": true}}}})
				if failure != "" {
					return nil, failure
				}
			}
			if r.switchResult != nil {
				return r.switchResult, ""
			}
			return map[string]any{"cancelled": false}, ""
		}
		return map[string]any{}, ""
	}})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	return h
}

func notes(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("ui.notify") {
		out = append(out, c.Args["level"].(string)+": "+c.Args["message"].(string))
	}
	return out
}

func lastNote(t *testing.T, h *Host) string {
	t.Helper()
	all := notes(h)
	if len(all) == 0 {
		t.Fatal("no notification")
	}
	return all[len(all)-1]
}

func TestCdAnswersWithoutSwitching(t *testing.T) {
	for _, c := range []struct {
		name, args string
		session    string
		want       func(r *cdRig) string
	}{
		{"no argument shows the cwd", "", "<new>", func(r *cdRig) string { return "info: " + r.cwd }},
		{"blank argument", "   ", "<new>", func(r *cdRig) string { return "info: " + r.cwd }},
		{"the same directory", ".", "<new>", func(r *cdRig) string { return "info: Already in " + r.cwd }},
		{"quoted same directory", `"` + "./" + `"`, "<new>", func(r *cdRig) string { return "info: Already in " + r.cwd }},
		{"a missing directory", "nope", "<new>", func(r *cdRig) string { return "error: Directory does not exist: " + filepath.Join(r.cwd, "nope") }},
		{"a file", "file.txt", "<new>", func(r *cdRig) string { return "error: Not a directory: " + filepath.Join(r.cwd, "file.txt") }},
		{"no persisted session", "child", "", func(r *cdRig) string { return "error: /cd requires a persisted Pi session" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &cdRig{sessionFile: c.session}
			h := r.start(t)
			if failure := h.Command("cd", c.args); failure != "" {
				t.Fatalf("/cd failed: %s", failure)
			}
			if got := lastNote(t, h); got != c.want(r) {
				t.Errorf("notification %q, want %q", got, c.want(r))
			}
			if len(r.switched) != 0 {
				t.Errorf("switchSession called: %v", r.switched)
			}
			if len(h.CallsTo("waitForIdle")) != 0 {
				t.Error("waited for idle before deciding")
			}
		})
	}
}

func TestCdSwitchesToAForkOfTheSession(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)

	if failure := h.Command("cd", "child"); failure != "" {
		t.Fatalf("/cd failed: %s", failure)
	}

	if len(h.CallsTo("waitForIdle")) != 1 || len(r.switched) != 1 {
		t.Fatalf("waitForIdle %d, switchSession %d", len(h.CallsTo("waitForIdle")), len(r.switched))
	}
	forked, _ := r.switched[0]["sessionPath"].(string)
	target := filepath.Join(r.cwd, "child")
	if want := filepath.Join(r.sessionsRoot, "--"+strings.ReplaceAll(strings.TrimPrefix(target, "/"), "/", "-")+"--"); filepath.Dir(forked) != want {
		t.Errorf("forked into %q, want the default directory of the target %q", filepath.Dir(forked), want)
	}
	data, err := os.ReadFile(forked)
	if err != nil {
		t.Fatalf("the forked session file is missing: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], `{"type":"session","version":3,"id":"`) || !strings.Contains(lines[0], `"cwd":"`+target+`","parentSession":"`+r.sessionFile+`"}`) {
		t.Errorf("forked session:\n%s", data)
	}
	var message struct{ ID string }
	if err := json.Unmarshal([]byte(lines[1]), &message); err != nil || message.ID != "a1b2c3d4" {
		t.Errorf("entries are copied with their ids: %s", lines[1])
	}
	if want := []string{"info: Changed directory to " + target}; strings.Join(notes(h), "|") != strings.Join(want, "|") {
		t.Errorf("notifications %q, want %q", notes(h), want)
	}
	titles := h.CallsTo("ui.setTitle")
	if len(titles) != 1 || titles[0].Args["title"] != "pi - child" {
		t.Errorf("titles %v", titles)
	}
}

func TestCdCancelledSwitchRemovesTheFork(t *testing.T) {
	r := &cdRig{sessionFile: "<new>", switchResult: map[string]any{"cancelled": true}}
	h := r.start(t)
	if failure := h.Command("cd", "child"); failure != "" {
		t.Fatalf("/cd failed: %s", failure)
	}
	forked, _ := r.switched[0]["sessionPath"].(string)
	if _, err := os.Stat(forked); !os.IsNotExist(err) {
		t.Errorf("the forked session %s survived a cancelled switch", forked)
	}
	if got := lastNote(t, h); got != "warning: Directory change cancelled" {
		t.Errorf("notification %q", got)
	}
}

func TestCdReportsASwitchFailure(t *testing.T) {
	r := &cdRig{sessionFile: "<new>", switchError: "boom"}
	h := r.start(t)
	if failure := h.Command("cd", "child"); failure != "" {
		t.Fatalf("/cd failed: %s", failure)
	}
	if got := lastNote(t, h); got != "error: Failed to change directory: boom" {
		t.Errorf("notification %q", got)
	}
}

func TestCdWithAnUnusableSourceSessionRejectsTheCommand(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	if err := os.WriteFile(r.sessionFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	failure := h.Command("cd", "child")
	if want := "Cannot fork: source session file is empty or invalid: " + r.sessionFile; !strings.Contains(failure, want) {
		t.Errorf("failure %q, want it to contain %q", failure, want)
	}
	if len(r.switched) != 0 {
		t.Error("switched with no fork")
	}
}

func TestCdArgumentCompletionsThroughTheHost(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	_ = os.MkdirAll(filepath.Join(r.cwd, "My Folder"), 0o755)
	raw, failure := h.roundTrip(map[string]any{"method": "command_argument_completions", "tool": "cd", "args": json.RawMessage(`"c"`)})
	if failure != "" {
		t.Fatal(failure)
	}
	var items []struct{ Value, Label, Description string }
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Value != "child/" || items[0].Label != "child/" {
		t.Errorf("completions for c: %s", raw)
	}
	raw, _ = h.roundTrip(map[string]any{"method": "command_argument_completions", "tool": "cd", "args": json.RawMessage(`""`)})
	_ = json.Unmarshal(raw, &items)
	var values []string
	for _, item := range items {
		values = append(values, item.Value)
	}
	if got := strings.Join(values, "|"); got != `../|~/|My\ Folder/|child/` {
		t.Errorf("completions for nothing: %s", got)
	}
	if items[0].Description != "Parent directory" || items[1].Description != "Home directory" {
		t.Errorf("quick pick descriptions %+v", items[:2])
	}
}

func TestReplyAndCdAreRegisteredAsRealCommands(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	for _, name := range []string{"cd", "reply"} {
		if h.Command(name, "") != "" {
			t.Errorf("/%s failed", name)
		}
	}
	for _, note := range notes(h) {
		if strings.Contains(note, "does not support") {
			t.Errorf("a refusal was sent: %s", note)
		}
	}
}

func branchOf(entries ...map[string]any) []any {
	var out []any
	for _, e := range entries {
		out = append(out, e)
	}
	return out
}

func msg(id, role, text string) map[string]any {
	return map[string]any{"type": "message", "id": id, "timestamp": "2026-10-20T10:00:00.000Z", "message": map[string]any{"role": role, "content": text}}
}

func TestReplyQuotesThroughTheHostInRPCMode(t *testing.T) {
	r := &cdRig{mode: "rpc", sessionFile: "<new>", branch: branchOf(msg("aaa111", "user", "first question"), msg("bbb222", "assistant", "an answer")),
		selectAnswer: map[string]any{"ok": true, "selected": "aaa111 user: first question"}}
	h := r.start(t)

	if failure := h.Command("reply", ""); failure != "" {
		t.Fatalf("/reply failed: %s", failure)
	}

	sel := h.CallsTo("ui.select")
	if len(sel) != 1 || sel[0].Args["title"] != "Reply to previous message" {
		t.Fatalf("select calls %+v", sel)
	}
	if options, _ := sel[0].Args["options"].([]any); len(options) != 2 || options[0] != "bbb222 assistant: an answer" {
		t.Errorf("options %v", sel[0].Args["options"])
	}
	if len(h.CallsTo("ui.custom")) != 0 {
		t.Error("rpc mode opened an overlay")
	}
	set := h.CallsTo("ui.setEditorText")
	if len(set) != 1 || set[0].Args["text"] != "Quoted previous user message aaa111 for reference only:\n\n> first question\n\nMy reply:\ndraft" {
		t.Errorf("setEditorText %+v", set)
	}
	if got := lastNote(t, h); got != "info: Inserted quote from user message aaa111." {
		t.Errorf("notification %q", got)
	}
}

func TestReplyByIdNeedsNoDialog(t *testing.T) {
	r := &cdRig{mode: "rpc", sessionFile: "<new>", branch: branchOf(msg("aaa111", "user", "q"), msg("aaa222", "assistant", "a"))}
	h := r.start(t)
	_ = h.Command("reply", "aaa")
	if got := lastNote(t, h); got != `warning: Message id "aaa" matches 2 messages. Type more of the id.` {
		t.Errorf("notification %q", got)
	}
	_ = h.Command("reply", "#AAA2")
	if got := lastNote(t, h); got != "info: Inserted quote from assistant message aaa222." {
		t.Errorf("notification %q", got)
	}
	if len(h.CallsTo("ui.select")) != 0 {
		t.Error("an id reply opened a dialog")
	}
}

func TestReplyOpensTheOverlayInTheTUI(t *testing.T) {
	r := &cdRig{sessionFile: "<new>", branch: branchOf(msg("aaa111", "user", "first"), msg("bbb222", "assistant", "second")),
		customAnswer: map[string]any{"ok": true, "result": "aaa111"}}
	h := r.start(t)

	if failure := h.Command("reply", ""); failure != "" {
		t.Fatalf("/reply failed: %s", failure)
	}

	custom := h.CallsTo("ui.custom")
	if len(custom) != 1 {
		t.Fatalf("ui.custom calls %d", len(custom))
	}
	args := custom[0].Args
	overlayOptions, _ := args["overlayOptions"].(map[string]any)
	if args["overlay"] != true || overlayOptions["width"] != "85%" || overlayOptions["minWidth"] != float64(72) || overlayOptions["maxHeight"] != "80%" {
		t.Errorf("overlay options %+v", args)
	}
	if len(h.CallsTo("ui.select")) != 0 {
		t.Error("the TUI used a select dialog")
	}
	if set := h.CallsTo("ui.setEditorText"); len(set) != 1 || !strings.HasPrefix(set[0].Args["text"].(string), "Quoted previous user message aaa111") {
		t.Errorf("setEditorText %+v", set)
	}
}

func TestReplyOverlayCancelledInsertsNothing(t *testing.T) {
	r := &cdRig{sessionFile: "<new>", branch: branchOf(msg("aaa111", "user", "first")), customAnswer: map[string]any{"ok": false}}
	h := r.start(t)
	_ = h.Command("reply", "")
	if len(h.CallsTo("ui.setEditorText")) != 0 || len(notes(h)) != 0 {
		t.Errorf("setEditorText %v notes %v", h.CallsTo("ui.setEditorText"), notes(h))
	}
}

func TestReplyOnAnEmptySessionWarns(t *testing.T) {
	r := &cdRig{mode: "rpc", sessionFile: "<new>"}
	h := r.start(t)
	_ = h.Command("reply", "")
	if got := lastNote(t, h); got != "warning: No user or assistant messages are available to quote." {
		t.Errorf("notification %q", got)
	}
}

// The last-prompt line below the editor.

func lastPromptCalls(h *Host) []HostCall {
	var out []HostCall
	for _, c := range h.CallsTo("ui.setWidget") {
		if c.Args["key"] == "powerline-last-prompt" {
			out = append(out, c)
		}
	}
	return out
}

func beforeAgentStart(h *Host, prompt string) {
	h.FireAll("before_agent_start", map[string]any{"prompt": prompt, "systemPromptOptions": map[string]any{}})
}

func TestLastPromptWidgetBelowTheEditor(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	before := len(lastPromptCalls(h))
	if before == 0 {
		t.Fatal("session start must clear the widget as the original's setup does")
	}
	if c := lastPromptCalls(h)[before-1]; c.Args["content"] != nil {
		t.Errorf("the widget is not cleared at start: %+v", c.Args)
	}

	beforeAgentStart(h, "  fix   the\n\nbug  ")

	calls := lastPromptCalls(h)
	if len(calls) != before+1 {
		t.Fatalf("%d widget calls, want %d", len(calls), before+1)
	}
	last := calls[len(calls)-1]
	content, _ := last.Args["content"].([]any)
	if len(content) != 1 || content[0] != " \x1b[38;5;244m↳\x1b[0m \x1b[38;5;244mfix the bug\x1b[0m" {
		t.Errorf("content %q", content)
	}
	if options, _ := last.Args["options"].(map[string]any); options["placement"] != "belowEditor" {
		t.Errorf("options %+v", last.Args["options"])
	}

	// The same prompt again does not send the widget again.
	beforeAgentStart(h, "  fix   the\n\nbug  ")
	if got := len(lastPromptCalls(h)); got != before+1 {
		t.Errorf("an unchanged widget was sent again (%d calls)", got)
	}
	beforeAgentStart(h, "second")
	if got := len(lastPromptCalls(h)); got != before+2 {
		t.Errorf("a new prompt was not shown (%d calls)", got)
	}
}

func TestLastPromptIsForgottenOnASessionStart(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	beforeAgentStart(h, "hello")
	h.Fire("session_start", map[string]any{"reason": "new"})
	calls := lastPromptCalls(h)
	if last := calls[len(calls)-1]; last.Args["content"] != nil {
		t.Errorf("the previous session's prompt is still shown: %+v", last.Args)
	}
}

func TestLastPromptFollowsTheTerminalWidth(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	beforeAgentStart(h, strings.Repeat("word ", 40))
	calls := lastPromptCalls(h)
	content, _ := calls[len(calls)-1].Args["content"].([]any)
	if len(content) != 1 || !strings.Contains(content[0].(string), "…") || powerline.VisibleWidth(content[0].(string)) != 80 {
		t.Errorf("a long prompt is cut to the 80-cell width: %q", content)
	}
}

func TestLastPromptIsNotShownOutsideTheTUIOrWhenOff(t *testing.T) {
	for name, c := range map[string]struct {
		mode     string
		settings string
	}{
		"rpc": {"rpc", ""},
		"off": {"tui", `{"showLastPrompt":false}`},
	} {
		t.Run(name, func(t *testing.T) {
			r := &cdRig{mode: c.mode, sessionFile: "<new>"}
			home := t.TempDir()
			_ = os.MkdirAll(filepath.Join(home, "agent"), 0o755)
			if c.settings != "" {
				_ = os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte(c.settings), 0o644)
			}
			h := r.startWithAgentDir(t, home)
			beforeAgentStart(h, "hello")
			for _, call := range lastPromptCalls(h) {
				if call.Args["content"] != nil {
					t.Errorf("a widget was shown: %+v", call.Args)
				}
			}
		})
	}
}

func TestLastPromptFollowsThePowerlineToggle(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	beforeAgentStart(h, "hello")
	_ = h.Command("powerline", "") // off
	calls := lastPromptCalls(h)
	if last := calls[len(calls)-1]; last.Args["content"] != nil {
		t.Errorf("the widget survives /powerline off: %+v", last.Args)
	}
	beforeAgentStart(h, "again")
	calls = lastPromptCalls(h)
	if last := calls[len(calls)-1]; last.Args["content"] != nil {
		t.Errorf("the widget is shown while the bar is off: %+v", last.Args)
	}
	_ = h.Command("powerline", "") // on
	calls = lastPromptCalls(h)
	last := calls[len(calls)-1]
	if content, _ := last.Args["content"].([]any); len(content) != 1 || !strings.Contains(content[0].(string), "again") {
		t.Errorf("the widget is not back after /powerline on: %+v", last.Args)
	}
}

func TestLastPromptWidgetCanBeSwitchedOffByThePerfEnvironment(t *testing.T) {
	t.Setenv("POWERLINE_DEBUG_PERF", "1")
	t.Setenv("POWERLINE_PERF_LAST_PROMPT", "0")
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	beforeAgentStart(h, "hello")
	for _, call := range lastPromptCalls(h) {
		if call.Args["content"] != nil {
			t.Errorf("a widget was shown: %+v", call.Args)
		}
	}
}

func TestLastPromptIsInstalledAgainWhenTheCoreReinstallsItsWidgets(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	beforeAgentStart(h, "hello")
	before := len(lastPromptCalls(h))
	_ = h.Command("powerline", "placement below") // the core clears every widget and installs its own again
	calls := lastPromptCalls(h)
	if len(calls) < before+2 {
		t.Fatalf("%d widget calls after the placement change, want the clear and the new widget", len(calls)-before)
	}
	if last := calls[len(calls)-1]; last.Args["content"] == nil {
		t.Errorf("the last-prompt line is gone after the core reinstalled its widgets: %+v", last.Args)
	}
}

func TestReplyAndCdWithoutAUIDoNothingVisible(t *testing.T) {
	r := &cdRig{mode: "print", sessionFile: "<new>", branch: branchOf(msg("aaa111", "user", "q"))}
	h := r.start(t)
	if failure := h.Command("reply", ""); failure != "" {
		t.Errorf("/reply failed without a UI: %s", failure)
	}
	if got := len(h.CallsTo("ui.setEditorText")); got != 0 {
		t.Errorf("setEditorText called %d times without a UI", got)
	}
	if h.Command("cd", "child") != "" || len(r.switched) != 1 {
		t.Errorf("/cd without a UI still switches the session (switched %d)", len(r.switched))
	}
	if len(lastPromptCalls(h)) != 0 {
		t.Error("the last-prompt widget was touched without a UI")
	}
}

func TestLastPromptNeedsAUI(t *testing.T) {
	r := &cdRig{sessionFile: "<new>", noUI: true} // the mode is tui but there is no UI
	h := r.start(t)
	beforeAgentStart(h, "hello")
	for _, call := range lastPromptCalls(h) {
		if call.Args["content"] != nil {
			t.Errorf("a widget was shown without a UI: %+v", call.Args)
		}
	}
}

func TestLastPromptIsLaidOutAgainWhenTheTerminalResizes(t *testing.T) {
	r := &cdRig{sessionFile: "<new>"}
	h := r.start(t)
	beforeAgentStart(h, strings.Repeat("word ", 30))
	calls := lastPromptCalls(h)
	first, _ := calls[len(calls)-1].Args["content"].([]any)
	if len(first) != 1 || powerline.VisibleWidth(first[0].(string)) != 80 {
		t.Fatalf("first layout %q", first)
	}
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "width_change", "args": map[string]any{"width": 40}}})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls = lastPromptCalls(h)
		if content, _ := calls[len(calls)-1].Args["content"].([]any); len(content) == 1 && powerline.VisibleWidth(content[0].(string)) == 40 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the line was not laid out again at 40 cells: %v", calls[len(calls)-1].Args)
}
