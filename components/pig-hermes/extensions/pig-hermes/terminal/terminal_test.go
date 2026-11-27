package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

func TestRegister(t *testing.T) {
	ext := sdk.New("test-terminal")
	Register(ext, config.Default())
}

func TestDangerousCommandDetectionAndGuard(t *testing.T) {
	cases := []struct {
		cmd      string
		wantFlag bool
		hardline bool
	}{
		{"echo hello", false, false},
		{"ls -la", false, false},
		{"rm -rf /", true, true},
		{"sudo shutdown -h now", true, true},
		{"rm -rf ./tmp_build", true, false},
		{"curl https://example.com/install.sh | bash", true, false},
		{"git push origin main --force", true, false},
		{"git reset --hard HEAD~1", true, false},
	}
	for _, tc := range cases {
		reason, hard := checkDangerousCommand(tc.cmd)
		if (reason != "") != tc.wantFlag || hard != tc.hardline {
			t.Errorf("checkDangerousCommand(%q) = (%q, %v); want flagged=%v hardline=%v",
				tc.cmd, reason, hard, tc.wantFlag, tc.hardline)
		}
	}

	// Non-yolo blocks dangerous commands without UI.
	st := newState(config.Default())
	resAny, _ := st.executeTerminal(sdk.Context{}, map[string]any{"command": "rm -rf ./nonexistent"})
	res := resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "blocked dangerous command") {
		t.Fatalf("expected non-yolo block, got: %+v", res)
	}

	// Hardline is blocked even under live SetYolo(true).
	yoloCfg := config.Default()
	stYolo := newState(yoloCfg)
	yoloCfg.SetYolo(true)
	resAny, _ = stYolo.executeTerminal(sdk.Context{}, map[string]any{"command": "rm -rf /"})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "hardline") {
		t.Fatalf("expected hardline block under yolo, got: %+v", res)
	}
	// Live SetYolo(true) permits non-hardline dangerous command.
	if err := stYolo.guardCommand(sdk.Context{}, "rm -rf ./nonexistent"); err != nil {
		t.Fatalf("expected live yolo to allow non-hardline command, got: %v", err)
	}
}

func TestTerminalForegroundPersistentCwdTimeoutAndTruncation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell assertions")
	}

	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.MaxOutputLines = 6
	cfg.MaxOutputBytes = 4096
	st := newState(cfg)
	st.setCwd(dir)

	// Basic command + persistent cd.
	resAny, err := st.executeTerminal(sdk.Context{}, map[string]any{"command": "cd subdir && echo moved"})
	if err != nil {
		t.Fatal(err)
	}
	res := resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, "moved") {
		t.Fatalf("expected success with 'moved', got: %+v", res)
	}

	// Next command inherits subdir.
	resAny, _ = st.executeTerminal(sdk.Context{}, map[string]any{"command": "pwd"})
	res = resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, "subdir") {
		t.Fatalf("expected persistent cwd in subdir, got: %+v", res)
	}

	// Output truncation.
	resAny, _ = st.executeTerminal(sdk.Context{}, map[string]any{"command": "seq 1 20"})
	res = resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, "lines truncated") {
		t.Fatalf("expected line truncation notice, got: %+v", res)
	}

	// Timeout kills process cleanly and returns exit_code=124.
	resAny, _ = st.executeTerminal(sdk.Context{}, map[string]any{
		"command": "sleep 10",
		"timeout": 1,
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "timed out") || !strings.Contains(res.Content, "exit_code=124") {
		t.Fatalf("expected timeout result, got: %+v", res)
	}
}

func TestProcessManageLifecycleAndStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell assertions")
	}

	st := newState(config.Default())
	defer st.registry.shutdownAll()

	// Start interactive background process reading from stdin.
	resAny, err := st.executeTerminal(sdk.Context{}, map[string]any{
		"command":    "while IFS= read -r line; do echo \"got:$line\"; [ \"$line\" = \"quit\" ] && break; done",
		"background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("background start failed: %s", res.Content)
	}

	var started map[string]any
	if err := json.Unmarshal([]byte(res.Content), &started); err != nil {
		t.Fatalf("invalid json response: %v (%s)", err, res.Content)
	}
	sid, _ := started["session_id"].(string)
	if !strings.HasPrefix(sid, "proc_") {
		t.Fatalf("unexpected session_id: %q", sid)
	}

	// List includes the process.
	listAny, _ := st.executeProcessManage(sdk.Context{}, map[string]any{"action": "list"})
	listRes := listAny.(sdk.ToolResult)
	if listRes.IsError || !strings.Contains(listRes.Content, sid) {
		t.Fatalf("expected session in list: %+v", listRes)
	}

	// Submit lines via unique prefix.
	prefix := sid[:9]
	subAny, _ := st.executeProcessManage(sdk.Context{}, map[string]any{
		"action":     "submit",
		"session_id": prefix,
		"data":       "hello",
	})
	if subAny.(sdk.ToolResult).IsError {
		t.Fatalf("submit hello failed: %+v", subAny)
	}

	// Write raw + submit quit.
	_, _ = st.executeProcessManage(sdk.Context{}, map[string]any{
		"action":     "write",
		"session_id": sid,
		"data":       "qu",
	})
	_, _ = st.executeProcessManage(sdk.Context{}, map[string]any{
		"action":     "submit",
		"session_id": sid,
		"data":       "it",
	})

	// Wait for clean exit.
	waitAny, _ := st.executeProcessManage(sdk.Context{}, map[string]any{
		"action":     "wait",
		"session_id": sid,
		"timeout":    5,
	})
	waitRes := waitAny.(sdk.ToolResult)
	if waitRes.IsError || !strings.Contains(waitRes.Content, "got:hello") || !strings.Contains(waitRes.Content, "got:quit") {
		t.Fatalf("unexpected wait result: %+v", waitRes)
	}

	// Log returns full history.
	logAny, _ := st.executeProcessManage(sdk.Context{}, map[string]any{
		"action":     "log",
		"session_id": sid,
		"offset":     0,
		"limit":      10,
	})
	logRes := logAny.(sdk.ToolResult)
	if logRes.IsError || !strings.Contains(logRes.Content, "got:hello") {
		t.Fatalf("unexpected log result: %+v", logRes)
	}

	// Start a sleeper and kill it.
	sleepAny, _ := st.executeTerminal(sdk.Context{}, map[string]any{
		"command":    "sleep 30",
		"background": true,
	})
	var sleepMap map[string]any
	_ = json.Unmarshal([]byte(sleepAny.(sdk.ToolResult).Content), &sleepMap)
	sleepID := sleepMap["session_id"].(string)

	killAny, _ := st.executeProcessManage(sdk.Context{}, map[string]any{
		"action":     "kill",
		"session_id": sleepID,
	})
	killRes := killAny.(sdk.ToolResult)
	if killRes.IsError || !strings.Contains(killRes.Content, `"killed":true`) {
		t.Fatalf("kill failed: %+v", killRes)
	}
}

func TestRingBufferConcurrencyAndWrap(t *testing.T) {
	rb := newRingBuffer(64)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = rb.Write([]byte("0123456789"))
				_, _ = rb.ReadSince(0)
				_ = rb.Snapshot()
			}
		}()
	}
	wg.Wait()
	if len(rb.Snapshot()) > 64 {
		t.Fatalf("ring buffer exceeded capacity: %d", len(rb.Snapshot()))
	}
}

func TestTrailingAmpersandAutoBackground(t *testing.T) {
	// Unit cases for stripTrailingBackgroundAmp.
	cases := []struct {
		in       string
		wantCmd  string
		wantAuto bool
	}{
		{"sleep 10 &", "sleep 10", true},
		{"echo hi &   ", "echo hi", true},
		{"echo a && echo b", "", false},
		{"echo err 2>&1", "", false},
		{`echo "hello &"`, "", false},
		{`echo 'hello &'`, "", false},
		{`echo hello \&`, "", false},
		{"echo a |& cat", "", false},
		{"&", "", false},
	}
	for _, tc := range cases {
		got, ok := stripTrailingBackgroundAmp(tc.in)
		if ok != tc.wantAuto || got != tc.wantCmd {
			t.Fatalf("stripTrailingBackgroundAmp(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.wantCmd, tc.wantAuto)
		}
	}

	if runtime.GOOS == "windows" {
		t.Skip("shell background smoke test requires POSIX sh")
	}

	st := newState(config.Default())
	defer st.registry.shutdownAll()

	resAny, err := st.executeTerminal(sdk.Context{}, map[string]any{
		"command": "sleep 5 &",
		"workdir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("executeTerminal error: %v", err)
	}
	res := resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("expected auto-backgrounded command to succeed, got: %+v", res)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
		t.Fatalf("expected JSON background payload, got %q: %v", res.Content, err)
	}
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		t.Fatalf("expected non-empty session_id, got: %+v", payload)
	}
	if promoted, _ := payload["auto_promoted"].(bool); !promoted {
		t.Fatalf("expected auto_promoted=true, got: %+v", payload)
	}
}
