package terminal

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

type approvalWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

type approvalHost struct {
	t       *testing.T
	nc      net.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	calls   []struct {
		method string
		args   map[string]any
	}
	pending map[string]chan json.RawMessage
	onCall  func(method string, args map[string]any) any
	nextID  int
	runDone chan error
}

func startApprovalHost(t *testing.T, ext *sdk.Extension, hasUI bool, onCall func(method string, args map[string]any) any) *approvalHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &approvalHost{
		t:       t,
		nc:      hostSide,
		pending: make(map[string]chan json.RawMessage),
		onCall:  onCall,
		runDone: make(chan error, 1),
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.readFrame()
	if first.Type != "register" {
		t.Fatalf("expected register frame, got %+v", first)
	}
	mode := "print"
	if hasUI {
		mode = "tui"
	}
	stateRaw, _ := json.Marshal(map[string]any{"hasUI": hasUI})
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          t.TempDir(),
			"mode":         mode,
			"width":        90,
			"height":       28,
			"state":        json.RawMessage(stateRaw),
		},
	})

	go func() {
		for {
			f, err := h.tryReadFrame()
			if err != nil {
				return
			}
			switch f.Type {
			case "call":
				if f.Call != nil {
					var args map[string]any
					_ = json.Unmarshal(f.Call.Args, &args)
					h.mu.Lock()
					h.calls = append(h.calls, struct {
						method string
						args   map[string]any
					}{f.Call.Method, args})
					h.mu.Unlock()
					go func(callID, method string, callArgs map[string]any) {
						var res any
						if h.onCall != nil {
							res = h.onCall(method, callArgs)
						}
						if res == nil {
							res = map[string]any{}
						}
						h.writeFrame(map[string]any{
							"type":        "call_result",
							"id":          callID,
							"call_result": map[string]any{"result": res},
						})
					}(f.ID, f.Call.Method, args)
				}
			case "response":
				h.mu.Lock()
				ch := h.pending[f.ID]
				delete(h.pending, f.ID)
				h.mu.Unlock()
				if ch != nil {
					var raw json.RawMessage
					if f.Response != nil {
						raw = f.Response.Result
					}
					ch <- raw
				}
			}
		}
	}()
	return h
}

func (h *approvalHost) close() {
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}

func (h *approvalHost) writeFrame(v any) {
	payload, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	h.writeMu.Lock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(payload)
	h.writeMu.Unlock()
}

func (h *approvalHost) tryReadFrame() (approvalWireFrame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		return approvalWireFrame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return approvalWireFrame{}, err
	}
	var f approvalWireFrame
	err := json.Unmarshal(buf, &f)
	return f, err
}

func (h *approvalHost) readFrame() approvalWireFrame {
	f, err := h.tryReadFrame()
	if err != nil {
		h.t.Fatalf("readFrame: %v", err)
	}
	return f
}

func (h *approvalHost) callTool(name string, params map[string]any) sdk.ToolResult {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	argv, _ := json.Marshal(params)
	h.writeFrame(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": id,
			"args":         json.RawMessage(argv),
		},
	})
	select {
	case raw := <-ch:
		var out struct {
			Content any  `json:"content"`
			IsError bool `json:"is_error"`
			IsErr2  bool `json:"isError"`
		}
		_ = json.Unmarshal(raw, &out)
		var text string
		switch v := out.Content.(type) {
		case string:
			text = v
		case []any:
			var b strings.Builder
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						b.WriteString(t)
					}
				}
			}
			text = b.String()
		}
		return sdk.ToolResult{Content: text, IsError: out.IsError || out.IsErr2}
	case <-time.After(3 * time.Second):
		h.t.Fatalf("timed out waiting for tool_call %s", name)
		return sdk.ToolResult{}
	}
}

func (h *approvalHost) selectCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c.method == "ui.select" {
			n++
		}
	}
	return n
}

func TestHardlineUnconditionalBlock(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := appendCommandAllowlist(cfgPath, "rm -rf /"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.SetYolo(true)
	st := newState(cfg)
	st.configFile = cfgPath
	st.recordSessionApproval("rm -rf /")

	if err := st.guardCommand(sdk.Context{}, "rm -rf /"); err == nil || !strings.Contains(err.Error(), "hardline") {
		t.Fatalf("expected unconditional hardline block, got: %v", err)
	}
}

func TestCompoundCommandAllowlistBypassPrevention(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := appendCommandAllowlist(cfgPath, "rm -rf ./build"); err != nil {
		t.Fatal(err)
	}

	st := newState(config.Default())
	st.configFile = cfgPath
	st.recordSessionApproval("rm -rf ./build")

	// Direct command matches allowlist (headless allowed without YOLO).
	if err := st.guardCommand(sdk.Context{}, "rm -rf ./build"); err != nil {
		t.Fatalf("expected direct allowlisted command to pass, got: %v", err)
	}

	// Compound commands with unquoted shell operators must NEVER match allowlist.
	compounds := []string{
		"rm -rf ./build; echo pwned",
		"rm -rf ./build && echo pwned",
		"rm -rf ./build | cat",
		"rm -rf ./build > /tmp/out",
		"rm -rf ./build < /dev/null",
		"rm -rf ./build `id`",
		`rm -rf ./build "$(id)"`,
		"rm -rf ./build &",
	}
	for _, cmd := range compounds {
		if !hasCompoundShellOperators(cmd) {
			t.Errorf("expected hasCompoundShellOperators(%q) = true", cmd)
		}
		if err := st.guardCommand(sdk.Context{}, cmd); err == nil {
			t.Errorf("compound command %q bypassed allowlist!", cmd)
		}
	}

	// Single-quoted operators are literal, not compound.
	if hasCompoundShellOperators(`echo 'a;b|c&d'`) {
		t.Errorf("single-quoted operators should not be treated as compound")
	}
}

func TestAdditiveCommandAllowlistPersistenceAndLiveRemoval(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	initial := "display:\n  skin: default\ncommand_allowlist:\n  - \"git reset --hard HEAD~1\"\nmodel: claude-sonnet-4\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	// Append a new entry additively without losing hand-edited entry or other keys.
	if err := appendCommandAllowlist(cfgPath, "rm -rf ./tmp_cache"); err != nil {
		t.Fatal(err)
	}
	// Duplicate append is a no-op.
	if err := appendCommandAllowlist(cfgPath, "rm -rf ./tmp_cache"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "git reset --hard HEAD~1") {
		t.Fatalf("hand-edited entry lost:\n%s", text)
	}
	if !strings.Contains(text, "rm -rf ./tmp_cache") {
		t.Fatalf("new entry missing:\n%s", text)
	}
	if !strings.Contains(text, "model: claude-sonnet-4") {
		t.Fatalf("other YAML key lost:\n%s", text)
	}
	if strings.Count(text, "rm -rf ./tmp_cache") != 1 {
		t.Fatalf("duplicate entry written:\n%s", text)
	}

	// Live re-read: command passes while present, then fails immediately after manual removal.
	st := newState(config.Default())
	st.configFile = cfgPath
	if err := st.guardCommand(sdk.Context{}, "rm -rf ./tmp_cache"); err != nil {
		t.Fatalf("expected allowlisted command to pass: %v", err)
	}

	// User manually removes command_allowlist entry on disk.
	if err := os.WriteFile(cfgPath, []byte("model: claude-sonnet-4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.guardCommand(sdk.Context{}, "rm -rf ./tmp_cache"); err == nil {
		t.Fatalf("expected manual removal from config.yaml to take effect immediately")
	}
}

func TestFiveChoiceApprovalModal(t *testing.T) {
	t.Run("once", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		st := newState(config.Default())
		st.configFile = cfgPath
		ext := sdk.New("approval-once")
		ext.RegisterTool(st.terminalToolDef())

		var sawOptions []any
		host := startApprovalHost(t, ext, true, func(method string, args map[string]any) any {
			if method == "ui.select" {
				sawOptions, _ = args["options"].([]any)
				return map[string]any{"selected": approvalChoiceOnce, "ok": true}
			}
			return nil
		})
		defer host.close()

		// Safe-to-execute dangerous command (nonexistent target).
		cmd := "rm -rf ./does_not_exist_once_test"
		res1 := host.callTool("terminal", map[string]any{"command": cmd})
		if res1.IsError {
			t.Fatalf("expected once choice to allow execution, got error: %s", res1.Content)
		}
		if len(sawOptions) != 5 {
			t.Fatalf("expected 5 Hermes choices in ui.select, got %d: %v", len(sawOptions), sawOptions)
		}
		// Calling again must prompt again (once does not record session approval).
		res2 := host.callTool("terminal", map[string]any{"command": cmd})
		if res2.IsError {
			t.Fatalf("expected second once call to succeed, got: %s", res2.Content)
		}
		if host.selectCalls() != 2 {
			t.Fatalf("expected 2 ui.select calls for 'once', got %d", host.selectCalls())
		}
	})

	t.Run("session", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		st := newState(config.Default())
		st.configFile = cfgPath
		ext := sdk.New("approval-session")
		ext.RegisterTool(st.terminalToolDef())

		host := startApprovalHost(t, ext, true, func(method string, _ map[string]any) any {
			if method == "ui.select" {
				return map[string]any{"selected": approvalChoiceSession, "ok": true}
			}
			return nil
		})
		defer host.close()

		cmd := "rm -rf ./does_not_exist_session_test"
		if res := host.callTool("terminal", map[string]any{"command": cmd}); res.IsError {
			t.Fatalf("first session call failed: %s", res.Content)
		}
		if res := host.callTool("terminal", map[string]any{"command": cmd}); res.IsError {
			t.Fatalf("second session call failed: %s", res.Content)
		}
		if host.selectCalls() != 1 {
			t.Fatalf("expected only 1 ui.select call for session-approved command, got %d", host.selectCalls())
		}
		// Should NOT persist to config.yaml.
		if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
			t.Fatalf("session choice should not create config.yaml")
		}
	})

	t.Run("always", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(cfgPath, []byte("command_allowlist:\n  - \"git clean -fd\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		st := newState(config.Default())
		st.configFile = cfgPath
		ext := sdk.New("approval-always")
		ext.RegisterTool(st.terminalToolDef())

		host := startApprovalHost(t, ext, true, func(method string, _ map[string]any) any {
			if method == "ui.select" {
				return map[string]any{"selected": approvalChoiceAlways, "ok": true}
			}
			return nil
		})
		defer host.close()

		cmd := "rm -rf ./does_not_exist_always_test"
		if res := host.callTool("terminal", map[string]any{"command": cmd}); res.IsError {
			t.Fatalf("always call failed: %s", res.Content)
		}
		if res := host.callTool("terminal", map[string]any{"command": cmd}); res.IsError {
			t.Fatalf("second always call failed: %s", res.Content)
		}
		if host.selectCalls() != 1 {
			t.Fatalf("expected 1 ui.select call for always-approved command, got %d", host.selectCalls())
		}
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "git clean -fd") || !strings.Contains(string(raw), cmd) {
			t.Fatalf("expected additive persistence in config.yaml, got:\n%s", string(raw))
		}
	})

	t.Run("deny", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		st := newState(config.Default())
		st.configFile = cfgPath
		ext := sdk.New("approval-deny")
		ext.RegisterTool(st.terminalToolDef())

		host := startApprovalHost(t, ext, true, func(method string, _ map[string]any) any {
			if method == "ui.select" {
				return map[string]any{"selected": approvalChoiceDeny, "ok": true}
			}
			return nil
		})
		defer host.close()

		res := host.callTool("terminal", map[string]any{"command": "rm -rf ./does_not_exist_deny_test"})
		if !res.IsError || !strings.Contains(res.Content, "denied by user") {
			t.Fatalf("expected deny error, got: %+v", res)
		}
	})

	t.Run("tell", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		st := newState(config.Default())
		st.configFile = cfgPath
		ext := sdk.New("approval-tell")
		ext.RegisterTool(st.terminalToolDef())

		var inputTitle string
		host := startApprovalHost(t, ext, true, func(method string, args map[string]any) any {
			switch method {
			case "ui.select":
				return map[string]any{"selected": approvalChoiceTell, "ok": true}
			case "ui.input":
				inputTitle, _ = args["title"].(string)
				return map[string]any{"text": "use git status instead", "ok": true}
			}
			return nil
		})
		defer host.close()

		res := host.callTool("terminal", map[string]any{"command": "git reset --hard HEAD~1"})
		if inputTitle != "Tell Hermes what to do instead" {
			t.Fatalf("expected steering input title, got %q", inputTitle)
		}
		if !res.IsError || !strings.Contains(res.Content, "BLOCKED by user: use git status instead") {
			t.Fatalf("expected steering block message, got: %+v", res)
		}
	})
}

func TestIndentlessAllowlistYAMLAndEscaping(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// Indentless YAML sequence (- items at column 0 under command_allowlist:)
	initial := "model: claude-sonnet-4\ncommand_allowlist:\n- \"git reset --hard HEAD~1\"\n- \"rm -rf ./cache\"\ndisplay:\n  skin: default\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	items := loadCommandAllowlist(cfgPath)
	if len(items) != 2 || items[0] != "git reset --hard HEAD~1" || items[1] != "rm -rf ./cache" {
		t.Fatalf("failed to parse indentless command_allowlist: %#v", items)
	}

	if err := appendCommandAllowlist(cfgPath, "rm -rf ./dist"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// New item must be emitted at existing indent (0 spaces) inside the block.
	if !strings.Contains(string(raw), "\n- \"rm -rf ./dist\"\ndisplay:\n") {
		t.Fatalf("expected indentless append at 0-space indent before display:, got:\n%s", string(raw))
	}

	// Quote/unquote escapes \n, \r, \t without injecting raw newlines.
	quoted := quoteAllowlistItem("echo a\nb\rc\td")
	if strings.ContainsAny(quoted, "\n\r") {
		t.Fatalf("quoteAllowlistItem emitted raw newline/CR: %q", quoted)
	}
	if unq := unquoteAllowlistItem(quoted); unq != "echo a\nb\rc\td" {
		t.Fatalf("unquoteAllowlistItem round-trip mismatch: got %q", unq)
	}

	// Compound commands must never be persisted to disk allowlist.
	if err := appendCommandAllowlist(cfgPath, "rm -rf ./tmp\nrm -rf /etc"); err != nil {
		t.Fatal(err)
	}
	afterCompound, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(afterCompound), "/etc") {
		t.Fatalf("compound/newline command was persisted to config.yaml:\n%s", string(afterCompound))
	}
}

func TestExactAllowlistMatchingAndWildcardSafety(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// Hand-authored glob entry + literal entry
	content := "command_allowlist:\n  - \"rm -rf ./build/*\"\n  - \"sudo rm ./a.log\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	st := newState(config.Default())
	st.configFile = cfgPath
	st.recordSessionApproval("sudo rm ./session.log")

	// Exact session approval matches, but prefix extension MUST NOT match.
	if !st.isApproved("sudo rm ./session.log") {
		t.Errorf("expected exact session approval to match")
	}
	if st.isApproved("sudo rm ./session.log -rf /var") {
		t.Errorf("prefix extension must NOT match session approval")
	}

	// Exact disk entry matches, but prefix extension MUST NOT match.
	if !st.isApproved("sudo rm ./a.log") {
		t.Errorf("expected exact disk entry to match")
	}
	if st.isApproved("sudo rm ./a.log -rf /var") {
		t.Errorf("prefix extension must NOT match disk allowlist entry")
	}

	// Hand-authored glob matches intended target.
	if !st.isApproved("rm -rf ./build/out") {
		t.Errorf("expected hand-authored glob to match")
	}

	// Modal "Always allow" for a command containing * must escape metacharacters so it only matches literally.
	ext := sdk.New("approval-glob-escape")
	ext.RegisterTool(st.terminalToolDef())
	host := startApprovalHost(t, ext, true, func(method string, _ map[string]any) any {
		if method == "ui.select" {
			return map[string]any{"selected": approvalChoiceAlways, "ok": true}
		}
		return nil
	})
	defer host.close()

	_ = host.callTool("terminal", map[string]any{"command": "rm -rf ./nonexistent_*.tmp"})
	// Literal command matches, but arbitrary expansion MUST NOT match.
	if !st.isApproved("rm -rf ./nonexistent_*.tmp") {
		t.Errorf("expected escaped literal command to match itself")
	}
	if st.isApproved("rm -rf ./nonexistent_other_file.tmp") {
		t.Errorf("modal-approved command containing '*' must NOT act as wildcard")
	}
}

func TestApprovalPromptComponentRenderPagingHotkeysAndTell(t *testing.T) {
	// Build a multi-line command with >10 lines to test paging.
	var cmdLines []string
	for i := 1; i <= 15; i++ {
		cmdLines = append(cmdLines, fmt.Sprintf("echo line_%02d_payload", i))
	}
	longCmd := strings.Join(cmdLines, "\n")

	comp := newApprovalPromptComponent(longCmd, "recursive delete")
	invalidations := 0
	comp.SetInvalidate(func() { invalidations++ })

	rendered := comp.Render(80)
	if len(rendered) < 18 {
		t.Fatalf("expected double-bordered modal lines, got %d", len(rendered))
	}
	topPlain := approvalStripANSI(rendered[0])
	botPlain := approvalStripANSI(rendered[len(rendered)-1])
	if !strings.HasPrefix(topPlain, "╔") || !strings.HasSuffix(topPlain, "╗") {
		t.Errorf("expected double-bordered top line, got %q", topPlain)
	}
	if !strings.Contains(topPlain, "⚠ approval required · recursive delete") {
		t.Errorf("expected header in top border, got %q", topPlain)
	}
	if !strings.HasPrefix(botPlain, "╚") || !strings.HasSuffix(botPlain, "╝") {
		t.Errorf("expected double-bordered bottom line, got %q", botPlain)
	}
	if !strings.Contains(rendered[0], approvalColor(approvalWarnGoldHex)) {
		t.Errorf("expected warn gold ANSI color in modal border")
	}

	page1Plain := approvalStripANSI(strings.Join(rendered, "\n"))
	if !strings.Contains(page1Plain, "Lines 1-10 of 15 · v for more") {
		t.Errorf("expected page 1 indicator, got:\n%s", page1Plain)
	}
	if !strings.Contains(page1Plain, "▸ "+approvalChoiceOnce) {
		t.Errorf("expected cursor on option 1, got:\n%s", page1Plain)
	}

	// Press 'v' to cycle to page 2 (lines 11-15).
	res, err := comp.HandleInput("v")
	if err != nil || res.Done {
		t.Fatalf("expected 'v' to page without closing: res=%+v err=%v", res, err)
	}
	page2Plain := approvalStripANSI(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(page2Plain, "Lines 11-15 of 15 · v for more") || !strings.Contains(page2Plain, "line_15_payload") {
		t.Errorf("expected page 2 lines after 'v', got:\n%s", page2Plain)
	}

	// Cursor navigation via j/k and arrow keys.
	_, _ = comp.HandleInput("j")
	if comp.cursor != 1 {
		t.Fatalf("expected cursor=1 after 'j', got %d", comp.cursor)
	}
	_, _ = comp.HandleInput("\x1b[A")
	if comp.cursor != 0 {
		t.Fatalf("expected cursor=0 after Up arrow, got %d", comp.cursor)
	}
	if invalidations == 0 {
		t.Errorf("expected SetInvalidate callback to be invoked on state changes")
	}

	// Direct hotkeys 1-4 return expected choices immediately.
	hotkeyCases := []struct {
		key  string
		want string
	}{
		{"1", "once"},
		{"2", "session"},
		{"3", "always"},
		{"4", "deny"},
	}
	for _, tc := range hotkeyCases {
		c := newApprovalPromptComponent("rm -rf ./tmp", "recursive delete")
		r, err := c.HandleInput(tc.key)
		if err != nil || !r.Done {
			t.Fatalf("hotkey %q did not complete: res=%+v err=%v", tc.key, r, err)
		}
		ar, ok := r.Value.(approvalResult)
		if !ok || ar.Choice != tc.want {
			t.Fatalf("hotkey %q value = %#v, want choice %q", tc.key, r.Value, tc.want)
		}
	}

	// Esc and Ctrl+C deny.
	for _, key := range []string{"\x1b", "\x03"} {
		c := newApprovalPromptComponent("rm -rf ./tmp", "recursive delete")
		r, err := c.HandleInput(key)
		if err != nil || !r.Done {
			t.Fatalf("key %q did not complete: res=%+v err=%v", key, r, err)
		}
		ar, _ := r.Value.(approvalResult)
		if ar.Choice != "deny" {
			t.Fatalf("key %q choice = %q, want deny", key, ar.Choice)
		}
	}

	// Option 5 switches to inline tell input mode; Esc returns to choices; Enter submits tell + reason.
	tellComp := newApprovalPromptComponent("git reset --hard HEAD~1", "destructive git reset")
	r, err := tellComp.HandleInput("5")
	if err != nil || r.Done || !tellComp.tellMode {
		t.Fatalf("expected '5' to enter tellMode: res=%+v err=%v tellMode=%v", r, err, tellComp.tellMode)
	}
	tellRendered := approvalStripANSI(strings.Join(tellComp.Render(80), "\n"))
	if !strings.Contains(tellRendered, "> ") {
		t.Errorf("expected inline '> ' prompt in tell mode, got:\n%s", tellRendered)
	}

	// Esc in tellMode returns to choice list without closing.
	r, err = tellComp.HandleInput("\x1b")
	if err != nil || r.Done || tellComp.tellMode {
		t.Fatalf("expected Esc in tellMode to return to choices: res=%+v err=%v tellMode=%v", r, err, tellComp.tellMode)
	}

	// Re-enter tell mode, type instruction with backspace, then Enter.
	_, _ = tellComp.HandleInput("5")
	_, _ = tellComp.HandleInput("use git statusx")
	_, _ = tellComp.HandleInput("\x7f")
	r, err = tellComp.HandleInput("\r")
	if err != nil || !r.Done {
		t.Fatalf("expected Enter in tellMode to submit: res=%+v err=%v", r, err)
	}
	ar, ok := r.Value.(approvalResult)
	if !ok || ar.Choice != "tell" || ar.Reason != "use git status" {
		t.Fatalf("unexpected tell result: %#v", r.Value)
	}
}

func TestApprovalPromptSanitizationAndKittyKeys(t *testing.T) {
	// Control characters / CSI / CR / C1 in command must be sanitized to visible escapes.
	malicious := "echo safe\r\x1b[2Krm -rf /\t\x07\u009b31m"
	comp := newApprovalPromptComponent(malicious, "danger\x1b[31m")
	lines := comp.Render(80)
	joined := approvalStripANSI(strings.Join(lines, "\n"))
	if strings.Contains(comp.command, "\x1b") || strings.Contains(comp.command, "\r") || strings.Contains(comp.command, "\t") || strings.Contains(comp.command, "\x07") || strings.Contains(comp.command, "\u009b") {
		t.Fatalf("unsanitized control chars remained in command: %q", comp.command)
	}
	if !strings.Contains(joined, "␛[2K") || !strings.Contains(joined, "\\x0d") || !strings.Contains(joined, "\\x07") || !strings.Contains(joined, "\\x9b") {
		t.Errorf("expected visible escape replacements in rendered modal, got:\n%s", joined)
	}
	for i, line := range lines {
		if w := approvalVisibleWidth(line); w != 80 {
			t.Errorf("row %d visible width = %d, want 80 (%q)", i, w, approvalStripANSI(line))
		}
	}

	// Kitty-protocol sequences for Escape (\x1b[27u), Ctrl+C (\x1b[99;5u), Enter (\x1b[13u),
	// Backspace (\x1b[127u), and Ctrl+U (\x1b[117;5u).
	cEsc := newApprovalPromptComponent("rm -rf ./tmp", "danger")
	if r, err := cEsc.HandleInput("\x1b[27u"); err != nil || !r.Done || r.Value.(approvalResult).Choice != "deny" {
		t.Fatalf("Kitty Escape did not deny: res=%+v err=%v", r, err)
	}
	cCtrlC := newApprovalPromptComponent("rm -rf ./tmp", "danger")
	if r, err := cCtrlC.HandleInput("\x1b[99;5u"); err != nil || !r.Done || r.Value.(approvalResult).Choice != "deny" {
		t.Fatalf("Kitty Ctrl+C did not deny: res=%+v err=%v", r, err)
	}
	cTell := newApprovalPromptComponent("rm -rf ./tmp", "danger")
	_, _ = cTell.HandleInput("5")
	_, _ = cTell.HandleInput("hello")
	_, _ = cTell.HandleInput("\x1b[117;5u") // Kitty Ctrl+U clears line
	if cTell.tellInput != "" {
		t.Fatalf("Kitty Ctrl+U did not clear tellInput: %q", cTell.tellInput)
	}
	_, _ = cTell.HandleInput("fixx")
	_, _ = cTell.HandleInput("\x1b[127u") // Kitty Backspace
	r, err := cTell.HandleInput("\x1b[13u") // Kitty Enter
	if err != nil || !r.Done {
		t.Fatalf("Kitty Enter in tellMode failed: res=%+v err=%v", r, err)
	}
	if ar := r.Value.(approvalResult); ar.Choice != "tell" || ar.Reason != "fix" {
		t.Fatalf("unexpected Kitty tell result: %+v", ar)
	}
}
