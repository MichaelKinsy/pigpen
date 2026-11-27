package pighermes_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pighermes "github.com/MichaelKinsy/pigpen/pig-hermes"
)
func toolText(t *testing.T, raw json.RawMessage) (string, bool) {
	t.Helper()
	var parsed struct {
		IsError bool            `json:"isError"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal tool result %s: %v", string(raw), err)
	}
	var s string
	if err := json.Unmarshal(parsed.Content, &s); err == nil {
		return s, parsed.IsError
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(parsed.Content, &blocks); err == nil {
		var parts []string
		for _, c := range blocks {
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		return strings.Join(parts, "\n"), parsed.IsError
	}
	return string(parsed.Content), parsed.IsError
}

func TestFakeHostIntegrationAllHermesToolsCommandsAndHooks(t *testing.T) {
	tmpHome := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpHome)
	t.Setenv("HERMES_PROFILE", "")

	samplePath := filepath.Join(workDir, "hello.txt")
	if err := os.WriteFile(samplePath, []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpHome, "SOUL.md"), []byte("Be concise and accurate."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte("Use pure Go stdlib."), 0o644); err != nil {
		t.Fatal(err)
	}

	h := StartHost(t, pighermes.Extension(), HostOptions{
		Mode: "tui",
		Cwd:  workDir,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "getSessionID", "getSessionId":
				return map[string]any{"sessionId": "fakehost-sess-1"}, ""
			case "getContextUsage":
				return map[string]any{"tokens": 4200, "contextWindow": 200000, "percent": 2.1}, ""
			case "getAllTools":
				return map[string]any{"tools": []map[string]any{{"name": "read_file"}, {"name": "write_file"}}}, ""
			case "getActiveTools":
				return map[string]any{"tools": []string{"read_file", "write_file"}}, ""
			case "ui.setTheme":
				return map[string]any{"success": true}, ""
			default:
				return map[string]any{}, ""
			}
		},
	})

	// 1. Verify all 18 Hermes tools are registered
	expectedTools := []string{
		"read_file", "write_file", "patch", "search_files",
		"terminal", "process_manage",
		"memory", "skills_list", "skill_view", "skill_manage",
		"session_search",
		"delegate_task", "todo_list", "clarify", "cronjob_manage",
		"web_search", "web_extract", "execute_code",
	}
	for _, name := range expectedTools {
		if !h.tools[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	// 2. Verify all 13 Hermes slash commands are registered
	expectedCmds := []string{
		"memory", "skills", "profile", "skin", "toolsets", "tools",
		"cron", "usage", "status", "personality", "yolo", "compress", "todos",
	}
	for _, name := range expectedCmds {
		if !h.cmds[name] {
			t.Errorf("expected command %q to be registered", name)
		}
	}

	// 3. Exercise read_file -> patch -> search_files end-to-end through wire protocol
	raw, errMsg := h.Tool("read_file", map[string]any{"path": "hello.txt"})
	if errMsg != "" {
		t.Fatalf("read_file host error: %s", errMsg)
	}
	text, isErr := toolText(t, raw)
	if isErr || !strings.Contains(text, "2|line two") {
		t.Fatalf("read_file unexpected: err=%v text=%q", isErr, text)
	}

	raw, errMsg = h.Tool("patch", map[string]any{
		"path":       "hello.txt",
		"old_string": "line two",
		"new_string": "line TWO",
	})
	if errMsg != "" {
		t.Fatalf("patch host error: %s", errMsg)
	}
	if txt, isErr := toolText(t, raw); isErr {
		t.Fatalf("patch failed: %s", txt)
	}

	raw, errMsg = h.Tool("search_files", map[string]any{
		"pattern": "line TWO",
		"path":    workDir,
	})
	if errMsg != "" {
		t.Fatalf("search_files host error: %s", errMsg)
	}
	if txt, isErr := toolText(t, raw); isErr || !strings.Contains(txt, "hello.txt") {
		t.Fatalf("search_files unexpected: err=%v text=%q", isErr, txt)
	}

	// 4. Exercise memory tool (add + replace rejection)
	raw, errMsg = h.Tool("memory", map[string]any{
		"target": "memory",
		"operations": []any{
			map[string]any{"action": "add", "content": "Always run go test -race"},
		},
	})
	if errMsg != "" {
		t.Fatalf("memory add host error: %s", errMsg)
	}
	if txt, isErr := toolText(t, raw); isErr || !strings.Contains(txt, "Always run go test -race") {
		t.Fatalf("memory add failed: err=%v text=%q", isErr, txt)
	}

	raw, errMsg = h.Tool("memory", map[string]any{
		"target": "memory",
		"operations": []any{
			map[string]any{"action": "replace", "old_text": "go test", "content": "pytest"},
		},
	})
	if errMsg != "" {
		t.Fatalf("memory replace host error: %s", errMsg)
	}
	if txt, _ := toolText(t, raw); !strings.Contains(txt, `"success":false`) || !strings.Contains(txt, "patch") {
		t.Fatalf("expected replace rejection pointing to patch, got text=%q", txt)
	}

	// 5. Exercise todo_list + cronjob_manage + terminal
	raw, errMsg = h.Tool("todo_list", map[string]any{
		"todos": []any{
			map[string]any{"id": "1", "content": "verify pig-hermes", "status": "in_progress"},
		},
	})
	if errMsg != "" {
		t.Fatalf("todo_list host error: %s", errMsg)
	}
	if txt, isErr := toolText(t, raw); isErr || !strings.Contains(txt, "verify pig-hermes") {
		t.Fatalf("todo_list failed: err=%v text=%q", isErr, txt)
	}

	raw, errMsg = h.Tool("cronjob_manage", map[string]any{
		"action":   "create",
		"name":     "daily-check",
		"schedule": "0 9 * * *",
		"prompt":   "check status",
	})
	if errMsg != "" {
		t.Fatalf("cronjob_manage host error: %s", errMsg)
	}
	if txt, isErr := toolText(t, raw); isErr || !strings.Contains(txt, "daily-check") {
		t.Fatalf("cronjob_manage create failed: err=%v text=%q", isErr, txt)
	}

	raw, errMsg = h.Tool("terminal", map[string]any{"command": "echo hermes-ok"})
	if errMsg != "" {
		t.Fatalf("terminal host error: %s", errMsg)
	}
	if txt, isErr := toolText(t, raw); isErr || !strings.Contains(txt, "hermes-ok") {
		t.Fatalf("terminal failed: err=%v text=%q", isErr, txt)
	}

	// 6. Exercise slash commands
	for _, cmd := range [][2]string{{"status", ""}, {"skin", "ares"}, {"memory", "show"}, {"todos", ""}} {
		if cerr := h.Command(cmd[0], cmd[1]); cerr != "" {
			t.Fatalf("command /%s %s failed: %s", cmd[0], cmd[1], cerr)
		}
	}
}
