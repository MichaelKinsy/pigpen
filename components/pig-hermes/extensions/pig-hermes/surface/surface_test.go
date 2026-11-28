package surface_test

import (
	"bytes"
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
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
	"github.com/MichaelKinsy/pigpen/pig-hermes/agents"
	"github.com/MichaelKinsy/pigpen/pig-hermes/surface"
)

// ── Minimal in-memory PiG fake host speaking PiG v0.8.0 wire protocol ────────

type hostCall struct {
	Method string
	Args   map[string]any
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Flags []struct {
			Name string `json:"name"`
		} `json:"flags"`
		ToolRenderers int `json:"tool_renderers"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

type fakeHost struct {
	t         *testing.T
	nc        net.Conn
	writeMu   sync.Mutex
	mu        sync.Mutex
	calls     []hostCall
	pending   map[string]chan json.RawMessage
	failures  map[string]string
	handlers  map[string][]int
	cmds      map[string]bool
	flags     map[string]bool
	resolvers int
	nextID    int
	onCall    func(method string, args map[string]any) (any, string)
	runDone   chan error
}

func startFakeHost(t *testing.T, ext *sdk.Extension, onCall func(method string, args map[string]any) (any, string)) *fakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:        t,
		nc:       hostSide,
		pending:  make(map[string]chan json.RawMessage),
		failures: make(map[string]string),
		handlers: make(map[string][]int),
		cmds:     make(map[string]bool),
		flags:    make(map[string]bool),
		onCall:   onCall,
		runDone:  make(chan error, 1),
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.readFrame()
	for first.Type == "call" && first.Call != nil {
		h.answerCall(first)
		first = h.readFrame()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got %+v", first)
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	for _, c := range first.Register.Commands {
		h.cmds[c.Name] = true
	}
	for _, f := range first.Register.Flags {
		h.flags[f.Name] = true
	}
	h.resolvers = first.Register.ToolRenderers

	stateRaw, _ := json.Marshal(map[string]any{"hasUI": true})
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          t.TempDir(),
			"mode":         "tui",
			"width":        80,
			"state":        json.RawMessage(stateRaw),
		},
	})
	go h.serveLoop()
	t.Cleanup(func() {
		h.writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
		select {
		case <-h.runDone:
		case <-time.After(5 * time.Second):
			t.Errorf("extension did not stop after shutdown")
		}
		_ = h.nc.Close()
	})
	return h
}

func (h *fakeHost) readFrame() wireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	var f wireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *fakeHost) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) answerCall(f wireFrame) {
	args := map[string]any{}
	_ = json.Unmarshal(f.Call.Args, &args)
	h.mu.Lock()
	h.calls = append(h.calls, hostCall{Method: f.Call.Method, Args: args})
	h.mu.Unlock()
	go func() {
		var result any = map[string]any{}
		var errMsg string
		if h.onCall != nil {
			result, errMsg = h.onCall(f.Call.Method, args)
		}
		if errMsg != "" {
			h.writeFrame(map[string]any{
				"type":        "call_result",
				"id":          f.ID,
				"call_result": map[string]any{"error": map[string]any{"message": errMsg}},
			})
			return
		}
		if result == nil {
			result = map[string]any{}
		}
		h.writeFrame(map[string]any{
			"type":        "call_result",
			"id":          f.ID,
			"call_result": map[string]any{"result": result},
		})
	}()
}

func (h *fakeHost) serveLoop() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, buf); err != nil {
			return
		}
		var f wireFrame
		if json.Unmarshal(buf, &f) != nil {
			continue
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				h.answerCall(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch == nil || f.Response == nil {
				continue
			}
			if f.Response.Error != nil {
				h.mu.Lock()
				h.failures[f.ID] = f.Response.Error.Message
				h.mu.Unlock()
			}
			ch <- f.Response.Result
		}
	}
}

func (h *fakeHost) roundTrip(request map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.writeFrame(map[string]any{"type": "request", "id": id, "request": request})
	select {
	case res := <-ch:
		h.mu.Lock()
		failure := h.failures[id]
		h.mu.Unlock()
		return res, failure
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for %v", request["method"])
		return nil, ""
	}
}

func (h *fakeHost) runCommand(name, args string) {
	h.t.Helper()
	argv, _ := json.Marshal(args)
	if _, failure := h.roundTrip(map[string]any{"method": "command", "tool": name, "args": json.RawMessage(argv)}); failure != "" {
		h.t.Fatalf("command /%s %q failed: %s", name, args, failure)
	}
}

func (h *fakeHost) fireEvent(event string, data map[string]any) {
	h.t.Helper()
	if data == nil {
		data = map[string]any{}
	}
	data["type"] = event
	raw, _ := json.Marshal(data)
	h.mu.Lock()
	ids := append([]int(nil), h.handlers[event]...)
	h.mu.Unlock()
	for _, hid := range ids {
		if _, failure := h.roundTrip(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		}); failure != "" {
			h.t.Fatalf("event %s failed: %s", event, failure)
		}
	}
}

func (h *fakeHost) resolveToolRenderers(tool string) (string, string) {
	h.t.Helper()
	argv, _ := json.Marshal(map[string]any{"tool": tool, "next": nil})
	raw, failure := h.roundTrip(map[string]any{"method": "resolve_tool_renderers", "tool": tool, "args": json.RawMessage(argv)})
	if failure != "" {
		h.t.Fatalf("resolve_tool_renderers(%s) failed: %s", tool, failure)
	}
	var got struct {
		Use       string `json:"use"`
		Renderers string `json:"renderers"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		h.t.Fatalf("decode resolve_tool_renderers: %v", err)
	}
	return got.Use, got.Renderers
}

func (h *fakeHost) renderTool(tool, renderers, phase string, args map[string]any, result map[string]any) []string {
	h.t.Helper()
	payload := map[string]any{
		"tool":      tool,
		"card":      "card-1",
		"renderers": renderers,
		"phase":     phase,
		"args":      args,
		"context":   map[string]any{},
		"width":     80,
	}
	if result != nil {
		payload["result"] = result
		payload["options"] = map[string]any{"expanded": false, "isPartial": false}
	}
	argv, _ := json.Marshal(payload)
	raw, failure := h.roundTrip(map[string]any{"method": "render_tool", "tool": tool, "args": json.RawMessage(argv)})
	if failure != "" {
		h.t.Fatalf("render_tool(%s, %s) failed: %s", tool, phase, failure)
	}
	var got struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		h.t.Fatalf("decode render_tool: %v", err)
	}
	return got.Lines
}

func (h *fakeHost) lastSentMessages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var msgs []string
	for _, c := range h.calls {
		if c.Method == "sendMessage" {
			if m, ok := c.Args["message"].(map[string]any); ok {
				if content, ok := m["content"].(string); ok {
					msgs = append(msgs, content)
				}
			}
		}
	}
	return msgs
}

func (h *fakeHost) lastStatus(key string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var last string
	for _, c := range h.calls {
		if c.Method == "ui.setStatus" && c.Args["key"] == key {
			if text, ok := c.Args["text"].(string); ok {
				last = text
			}
		}
	}
	return last
}

// ── Unit Tests ───────────────────────────────────────────────────────────────

func TestBuiltinSkins(t *testing.T) {
	names := surface.BuiltinSkinNames()
	expected := []string{
		"default", "ares", "mono", "slate", "daylight",
		"warm-lightmode", "poseidon", "sisyphus", "charizard",
	}
	if len(names) != len(expected) {
		t.Fatalf("got %d skins, want %d", len(names), len(expected))
	}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("skin[%d] = %q, want %q", i, names[i], want)
		}
		sk, ok := surface.GetSkin(want)
		if !ok {
			t.Errorf("GetSkin(%q) returned false", want)
			continue
		}
		if sk.Name != want {
			t.Errorf("skin.Name = %q, want %q", sk.Name, want)
		}
		if sk.Color("banner_title", "") == "" {
			t.Errorf("skin %q missing banner_title color", want)
		}
		if sk.ToolPrefix == "" {
			t.Errorf("skin %q missing ToolPrefix", want)
		}
	}

	def, ok := surface.GetSkin("default")
	if !ok {
		t.Fatal("default skin not found")
	}
	if def.Color("banner_title", "") != "#FFD700" || def.Color("banner_accent", "") != "#FFBF00" || def.Color("banner_border", "") != "#CD7F32" {
		t.Errorf("default skin palette mismatch: %+v", def.Colors)
	}
	if _, ok := surface.GetSkin("nonexistent-skin"); ok {
		t.Error("expected nonexistent skin to return false")
	}

	st := surface.FormatStatusText("work", "ares", 42, true)
	if !strings.Contains(st, "work") || !strings.Contains(st, "ares") || !strings.Contains(st, "42%") || !strings.Contains(st, "YOLO") {
		t.Errorf("unexpected status text: %q", st)
	}
}

func TestToolsetsCatalogAndResolution(t *testing.T) {
	all := surface.BuiltinToolsets()
	if len(all) < 10 {
		t.Fatalf("expected at least 10 built-in toolsets, got %d", len(all))
	}

	cliTools := surface.ResolveToolsetTools("hermes-cli")
	if len(cliTools) < 15 {
		t.Fatalf("expected hermes-cli to resolve >= 15 tools, got %d", len(cliTools))
	}

	dbgTools := surface.ResolveToolsetTools("debugging")
	found := make(map[string]bool)
	for _, tool := range dbgTools {
		found[tool] = true
	}
	for _, want := range []string{"terminal", "process_manage", "web_search", "read_file", "patch"} {
		if !found[want] {
			t.Errorf("debugging toolset missing %q (got %v)", want, dbgTools)
		}
	}

	// Deduplication across comma-separated toolsets.
	combined := surface.ResolveToolsetTools("file,debugging,file")
	seen := make(map[string]bool)
	for _, tool := range combined {
		if seen[tool] {
			t.Errorf("duplicate tool %q in ResolveToolsetTools", tool)
		}
		seen[tool] = true
	}
}

func TestCLIDispatchSubcommandsAndProfileFlag(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HERMES_HOME", root)
	t.Setenv("HERMES_PROFILE", "")

	// init() already registers CLI subcommands and -p/--profile preprocessor before Register() is called.
	if clihook.Registered() == nil {
		t.Fatalf("expected clihook.Registered() to be non-nil from surface init()")
	}
	runCLI := func(args ...string) (clihook.Result, string, string) {
		var stdout, stderr bytes.Buffer
		res := cli.Dispatch(clihook.Env{
			Args:   args,
			Stdout: &stdout,
			Stderr: &stderr,
			Getenv: os.Getenv,
		})
		return res, stdout.String(), stderr.String()
	}

	// 1. profile create / list / use / current / delete
	res, out, errOut := runCLI("profile", "create", "coder")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("profile create failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	res, out, _ = runCLI("profile", "list")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "coder") {
		t.Fatalf("profile list missing coder: %q", out)
	}
	res, out, errOut = runCLI("profile", "use", "coder")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("profile use failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// Test -p / --profile preprocessor sets HERMES_PROFILE and strips the flag.
	res, out, errOut = runCLI("-p", "coder", "profile", "current")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "coder") {
		t.Fatalf("-p coder profile current failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	if got := os.Getenv("HERMES_PROFILE"); got != "coder" {
		t.Errorf("HERMES_PROFILE = %q, want coder", got)
	}
	// Non-existent profile name with -p passes through untouched so PiG's print mode `-p "hello"` works.
	t.Setenv("HERMES_PROFILE", "")
	res, _, _ = runCLI("-p", "hello")
	if res.Handled || len(res.Args) != 2 || res.Args[0] != "-p" || res.Args[1] != "hello" {
		t.Fatalf("expected -p hello to pass through to PiG print mode, got res=%+v", res)
	}
	if got := os.Getenv("HERMES_PROFILE"); got != "" {
		t.Errorf("HERMES_PROFILE should remain empty for non-profile -p hello, got %q", got)
	}
	// -p default is always recognized as the default profile.
	res, out, errOut = runCLI("-p", "default", "profile", "current")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "default") {
		t.Fatalf("-p default profile current failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// Invalid profile name via --profile= returns exit 2.
	res, _, errOut = runCLI("--profile=../bad", "hermes-status")
	if !res.Handled || res.Exit != 2 || !strings.Contains(errOut, "invalid profile") {
		t.Fatalf("expected invalid profile error, got res=%+v err=%q", res, errOut)
	}

	// Reset HERMES_PROFILE to default and delete coder.
	t.Setenv("HERMES_PROFILE", "")
	res, out, errOut = runCLI("profile", "delete", "coder")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("profile delete failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// 2. skills list / view
	skillDir := filepath.Join(root, "skills", "go-testing")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: go-testing\ndescription: Go unit testing best practices\n---\nAlways run -race.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	res, out, errOut = runCLI("skills", "list")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "go-testing") {
		t.Fatalf("skills list failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	res, out, errOut = runCLI("skills", "view", "go-testing")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "Always run -race.") {
		t.Fatalf("skills view failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// 3. cron list / remove using real agents.CronStore format (CronSchedule object)
	cronStore := agents.NewCronStore(filepath.Join(root, "cron", "jobs.json"))
	created, err := cronStore.Create(agents.CreateJobRequest{
		Name:     "daily-check",
		Schedule: "0 9 * * *",
		Prompt:   "check status",
	})
	if err != nil {
		t.Fatalf("cronStore.Create failed: %v", err)
	}
	res, out, errOut = runCLI("cron", "list")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, created.ID) || !strings.Contains(out, "0 9 * * *") {
		t.Fatalf("cron list failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	res, out, errOut = runCLI("cron", "remove", created.ID)
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "Removed cron job "+created.ID) {
		t.Fatalf("cron remove failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// 4. hermes-status (does not shadow PiG's built-in status subcommand)
	res, _, _ = runCLI("status")
	if res.Handled {
		t.Fatalf("expected bare 'status' to remain unhandled so PiG built-in status is not shadowed, got %+v", res)
	}
	res, out, errOut = runCLI("hermes-status")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "HERMES_HOME:") || !strings.Contains(out, "MEMORY.md:") {
		t.Fatalf("hermes-status failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	// 5. doctor
	res, out, errOut = runCLI("doctor")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "Hermes Doctor") {
		t.Fatalf("doctor failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// 6. Unknown subcommand passes through unhandled.
	res, _, _ = runCLI("unrecognized-subcommand")
	if res.Handled {
		t.Fatalf("expected unrecognized subcommand to be unhandled, got %+v", res)
	}
}

func TestSlashCommandsAndSessionLifecycle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HERMES_HOME", root)
	t.Setenv("HERMES_PROFILE", "")

	p := paths.ResolveProfilePaths("")
	cfg := config.Default()
	memStore := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)

	// Seed a skill and a cron job.
	skillDir := filepath.Join(p.SkillsDir, "refactor")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: refactor\ndescription: Clean refactoring\n---\nKeep diffs small.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cronStore := agents.NewCronStore(p.CronJobsFile)
	seededCron, err := cronStore.Create(agents.CreateJobRequest{
		Name:     "nightly",
		Schedule: "0 2 * * *",
		Prompt:   "run tests",
	})
	if err != nil {
		t.Fatalf("seed cron job: %v", err)
	}

	// Seed usage and todo stores.
	usage.Default().Add("sess-surface-test", "", "claude-sonnet", 1000, 250, 0.015, 2, 4)
	_, _ = todo.Default().Replace("sess-surface-test", []todo.Item{
		{ID: "t1", Content: "Ship surface slice", Status: "in_progress"},
	})

	ext := sdk.New("pig-hermes-surface-test")
	surface.Register(ext, p, cfg, memStore)

	tokens := 12000
	pct := 12.0
	host := startFakeHost(t, ext, func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionId", "getSessionID":
			return map[string]any{"sessionId": "sess-surface-test"}, ""
		case "getContextUsage":
			return map[string]any{"tokens": tokens, "contextWindow": 100000, "percent": pct}, ""
		case "getAllTools":
			return map[string]any{
				"tools": []map[string]any{
					{"name": "read_file", "description": "Read file"},
					{"name": "terminal", "description": "Run shell"},
				},
			}, ""
		case "getActiveTools":
			return map[string]any{"tools": []string{"read_file", "terminal"}}, ""
		case "ui.setTheme":
			return map[string]any{"success": true}, ""
		default:
			return map[string]any{}, ""
		}
	})

	// Verify all 5 flags, 13 slash commands, and tool renderer resolver registered.
	for _, f := range []string{"profile", "toolsets", "skin", "yolo", "max-turns"} {
		if !host.flags[f] {
			t.Errorf("flag %q not registered", f)
		}
	}
	for _, cmd := range []string{
		"memory", "skills", "profile", "skin", "toolsets", "tools",
		"cron", "usage", "status", "personality", "yolo", "compress", "todos",
	} {
		if !host.cmds[cmd] {
			t.Errorf("slash command /%s not registered", cmd)
		}
	}
	if host.resolvers < 1 {
		t.Errorf("expected at least 1 tool renderer resolver, got %d", host.resolvers)
	}

	// Verify Hermes compact tree-style tool renderer.
	use, renderID := host.resolveToolRenderers("terminal")
	if use != "own" || renderID == "" {
		t.Fatalf("expected own tool renderer for terminal, got use=%q id=%q", use, renderID)
	}
	callLines := host.renderTool("terminal", renderID, "call", map[string]any{"command": "echo hi"}, nil)
	if len(callLines) != 1 || !strings.Contains(callLines[0], "├─") || !strings.Contains(callLines[0], "echo hi") {
		t.Errorf("unexpected tool call render lines: %v", callLines)
	}
	resLines := host.renderTool("terminal", renderID, "result", map[string]any{}, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "hi"}},
	})
	if len(resLines) != 1 || !strings.Contains(resLines[0], "└─") || !strings.Contains(resLines[0], "hi") {
		t.Errorf("unexpected tool result render lines: %v", resLines)
	}

	// Trigger session_start and verify status bar is populated.
	host.fireEvent("session_start", map[string]any{})
	st := host.lastStatus("hermes")
	if !strings.Contains(st, "default") || !strings.Contains(st, "mem 0%") {
		t.Errorf("unexpected session_start status: %q", st)
	}

	lastMsg := func() string {
		msgs := host.lastSentMessages()
		if len(msgs) == 0 {
			return ""
		}
		return msgs[len(msgs)-1]
	}

	// /memory add, show, clear
	host.runCommand("memory", "add Prefer table-driven Go tests")
	if got := lastMsg(); !strings.Contains(got, "Added to memory") {
		t.Errorf("/memory add unexpected output: %q", got)
	}
	host.runCommand("memory", "show")
	if got := lastMsg(); !strings.Contains(got, "Prefer table-driven Go tests") {
		t.Errorf("/memory show missing entry: %q", got)
	}
	host.runCommand("memory", "clear memory")
	if got := lastMsg(); !strings.Contains(got, "Cleared memory") {
		t.Errorf("/memory clear unexpected output: %q", got)
	}

	// /skills list, search, inspect
	host.runCommand("skills", "list")
	if got := lastMsg(); !strings.Contains(got, "refactor") {
		t.Errorf("/skills list missing refactor: %q", got)
	}
	host.runCommand("skills", "search clean")
	if got := lastMsg(); !strings.Contains(got, "refactor") {
		t.Errorf("/skills search missing refactor: %q", got)
	}
	host.runCommand("skills", "inspect refactor")
	if got := lastMsg(); !strings.Contains(got, "Keep diffs small.") {
		t.Errorf("/skills inspect missing content: %q", got)
	}

	// /profile
	host.runCommand("profile", "")
	if got := lastMsg(); !strings.Contains(got, "Active Profile:") {
		t.Errorf("/profile unexpected output: %q", got)
	}

	// /skin list & switch
	host.runCommand("skin", "")
	if got := lastMsg(); !strings.Contains(got, "charizard") || !strings.Contains(got, "poseidon") {
		t.Errorf("/skin list missing skins: %q", got)
	}
	host.runCommand("skin", "poseidon")
	if got := lastMsg(); !strings.Contains(got, "Switched skin to poseidon") {
		t.Errorf("/skin switch unexpected output: %q", got)
	}
	if st := host.lastStatus("hermes"); !strings.Contains(st, "poseidon") {
		t.Errorf("status bar not updated after /skin poseidon: %q", st)
	}

	// /toolsets & /tools (verify SetActiveTools is invoked when switching toolsets)
	host.runCommand("toolsets", "")
	if got := lastMsg(); !strings.Contains(got, "hermes-cli") {
		t.Errorf("/toolsets unexpected output: %q", got)
	}
	host.runCommand("toolsets", "file")
	if got := lastMsg(); !strings.Contains(got, "Active toolset set to file") {
		t.Errorf("/toolsets file unexpected output: %q", got)
	}
	sawSetActiveTools := false
	host.mu.Lock()
	for _, c := range host.calls {
		if c.Method == "setActiveTools" {
			sawSetActiveTools = true
			break
		}
	}
	host.mu.Unlock()
	if !sawSetActiveTools {
		t.Errorf("expected /toolsets file to call setActiveTools on host")
	}
	host.runCommand("tools", "")
	if got := lastMsg(); !strings.Contains(got, "read_file") {
		t.Errorf("/tools unexpected output: %q", got)
	}

	// /cron list, pause, resume, remove (against real agents.CronStore)
	host.runCommand("cron", "list")
	if got := lastMsg(); !strings.Contains(got, seededCron.ID) {
		t.Errorf("/cron list unexpected output: %q", got)
	}
	host.runCommand("cron", "pause "+seededCron.ID)
	if got := lastMsg(); !strings.Contains(got, "Paused cron job "+seededCron.ID) {
		t.Errorf("/cron pause unexpected output: %q", got)
	}
	host.runCommand("cron", "resume "+seededCron.ID)
	if got := lastMsg(); !strings.Contains(got, "Resumed cron job "+seededCron.ID) {
		t.Errorf("/cron resume unexpected output: %q", got)
	}
	host.runCommand("cron", "remove "+seededCron.ID)
	if got := lastMsg(); !strings.Contains(got, "Removed cron job "+seededCron.ID) {
		t.Errorf("/cron remove unexpected output: %q", got)
	}
	// /usage
	host.runCommand("usage", "")
	if got := lastMsg(); !strings.Contains(got, "sess-surface-test") {
		t.Errorf("/usage unexpected output: %q", got)
	}

	// /status
	host.runCommand("status", "")
	if got := lastMsg(); !strings.Contains(got, "Hermes Session Status") || !strings.Contains(got, "poseidon") {
		t.Errorf("/status unexpected output: %q", got)
	}

	// /personality set, show, clear
	host.runCommand("personality", "set Be concise and direct.")
	if got := lastMsg(); !strings.Contains(got, "Updated SOUL.md") {
		t.Errorf("/personality set unexpected output: %q", got)
	}
	host.runCommand("personality", "show")
	if got := lastMsg(); !strings.Contains(got, "Be concise and direct.") {
		t.Errorf("/personality show unexpected output: %q", got)
	}
	host.runCommand("personality", "clear")
	if got := lastMsg(); !strings.Contains(got, "Cleared SOUL.md") {
		t.Errorf("/personality clear unexpected output: %q", got)
	}

	// /yolo toggle updates cfg.IsYolo()
	host.runCommand("yolo", "")
	if got := lastMsg(); !strings.Contains(got, "YOLO mode: ON") {
		t.Errorf("/yolo toggle on unexpected output: %q", got)
	}
	if !cfg.IsYolo() {
		t.Errorf("expected cfg.IsYolo() to be true after /yolo toggle on")
	}
	host.runCommand("yolo", "")
	if got := lastMsg(); !strings.Contains(got, "YOLO mode: OFF") {
		t.Errorf("/yolo toggle off unexpected output: %q", got)
	}
	if cfg.IsYolo() {
		t.Errorf("expected cfg.IsYolo() to be false after /yolo toggle off")
	}

	// /compress
	host.runCommand("compress", "")
	if got := lastMsg(); !strings.Contains(got, "Compaction triggered") || !strings.Contains(got, "12000") {
		t.Errorf("/compress unexpected output: %q", got)
	}

	// /todos
	host.runCommand("todos", "")
	if got := lastMsg(); !strings.Contains(got, "Ship surface slice") {
		t.Errorf("/todos unexpected output: %q", got)
	}
}

func TestSessionFlagsMaxTurnsYoloToolsetsAndDefaultTodos(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HERMES_HOME", root)
	t.Setenv("HERMES_PROFILE", "")

	p := paths.ResolveProfilePaths("")
	cfg := config.Default()
	memStore := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)

	// Seed todos under "default" session ID (matching todo_list fallback).
	_, _ = todo.Default().Replace("default", []todo.Item{
		{ID: "d1", Content: "Default fallback item", Status: "pending"},
	})

	ext := sdk.New("pig-hermes-surface-flags-test")
	surface.Register(ext, p, cfg, memStore)

	host := startFakeHost(t, ext, func(method string, args map[string]any) (any, string) {
		switch method {
		case "getFlag":
			switch args["name"] {
			case "yolo":
				return map[string]any{"value": true}, ""
			case "toolsets":
				return map[string]any{"value": "file"}, ""
			case "max-turns":
				return map[string]any{"value": "2"}, ""
			}
			return map[string]any{}, ""
		case "getSessionId", "getSessionID":
			return map[string]any{"sessionId": "session-without-todos"}, ""
		default:
			return map[string]any{}, ""
		}
	})

	host.fireEvent("session_start", map[string]any{})
	if !cfg.IsYolo() {
		t.Errorf("expected --yolo flag to enable cfg.IsYolo()")
	}

	// Verify --toolsets flag called setActiveTools.
	sawSetActive := false
	host.mu.Lock()
	for _, c := range host.calls {
		if c.Method == "setActiveTools" {
			sawSetActive = true
			break
		}
	}
	host.mu.Unlock()
	if !sawSetActive {
		t.Errorf("expected --toolsets flag to call setActiveTools")
	}

	// Verify --max-turns resets on agent_start and aborts on turn_end at limit.
	host.fireEvent("agent_start", map[string]any{})
	host.fireEvent("turn_end", map[string]any{})
	abortCount := func() int {
		host.mu.Lock()
		defer host.mu.Unlock()
		n := 0
		for _, c := range host.calls {
			if c.Method == "abort" {
				n++
			}
		}
		return n
	}
	if got := abortCount(); got != 0 {
		t.Fatalf("expected 0 aborts after turn 1/2, got %d", got)
	}
	host.fireEvent("turn_end", map[string]any{})
	if got := abortCount(); got != 1 {
		t.Fatalf("expected 1 abort after turn 2/2, got %d", got)
	}
	// Next agent_start resets turn counter.
	host.fireEvent("agent_start", map[string]any{})
	host.fireEvent("turn_end", map[string]any{})
	if got := abortCount(); got != 1 {
		t.Fatalf("expected turn counter reset on agent_start (still 1 abort), got %d", got)
	}

	// Verify /todos falls back to "default" session ID.
	host.runCommand("todos", "")
	msgs := host.lastSentMessages()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1], "Default fallback item") {
		t.Fatalf("expected /todos to fall back to default session, got %v", msgs)
	}
}

func TestHermesLoginAndSpriteDefinitions(t *testing.T) {
	isValidHex := func(s string) bool {
		if len(s) != 7 || s[0] != '#' {
			return false
		}
		for i := 1; i < 7; i++ {
			c := s[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
		return true
	}

	checkGridsAndPalette := func(t *testing.T, label string, palette map[string]string, grids ...struct {
		name   string
		rows   []string
		width  int
		height int
	}) {
		t.Helper()
		if len(palette) == 0 {
			t.Fatalf("%s: palette must not be empty", label)
		}
		if len(palette) > 32 {
			t.Fatalf("%s: palette size %d exceeds 32", label, len(palette))
		}
		used := map[byte]bool{}
		for _, g := range grids {
			if len(g.rows) != g.height {
				t.Fatalf("%s.%s: got %d rows, want %d", label, g.name, len(g.rows), g.height)
			}
			for y, row := range g.rows {
				if len(row) != g.width {
					t.Fatalf("%s.%s[%d]: got width %d, want %d", label, g.name, y, len(row), g.width)
				}
				for x := range len(row) {
					c := row[x]
					if c > 127 || c <= ' ' {
						t.Fatalf("%s.%s[%d][%d]: non-printable/non-ASCII byte %q", label, g.name, y, x, c)
					}
					if c == '.' {
						continue
					}
					used[c] = true
					if _, ok := palette[string(c)]; !ok {
						t.Fatalf("%s.%s[%d][%d]: symbol %q missing from palette", label, g.name, y, x, c)
					}
				}
			}
		}
		for k, v := range palette {
			if len(k) != 1 || k == "." || k[0] <= ' ' || k[0] > 127 {
				t.Fatalf("%s: invalid palette key %q", label, k)
			}
			if !used[k[0]] {
				t.Fatalf("%s: palette key %q is unused in grids", label, k)
			}
			if !isValidHex(v) {
				t.Fatalf("%s: palette[%q] = %q is not #RRGGBB", label, k, v)
			}
		}
	}

	login := surface.HermesLoginDefinition()
	checkGridsAndPalette(t, "login", login.Palette,
		struct {
			name   string
			rows   []string
			width  int
			height int
		}{"brand", login.Brand, surface.LoginBrandWidth, surface.LoginBrandHeight},
		struct {
			name   string
			rows   []string
			width  int
			height int
		}{"hero", login.Hero, surface.LoginHeroWidth, surface.LoginHeroHeight},
		struct {
			name   string
			rows   []string
			width  int
			height int
		}{"mascot", login.Mascot, surface.LoginMascotWidth, surface.LoginMascotHeight},
	)
	if login.Name != "pig-hermes" || len(login.Name) > 24 {
		t.Errorf("unexpected login.Name: %q", login.Name)
	}
	if login.Description != "Hermes Agent on PiG" || len(login.Description) > 48 {
		t.Errorf("unexpected login.Description: %q", login.Description)
	}
	if login.Tagline != "Winged messenger, forged in Go." || len(login.Tagline) > 76 {
		t.Errorf("unexpected login.Tagline: %q", login.Tagline)
	}
	if 2+len(login.Name)+2+len(login.Description) > 80 {
		t.Errorf("login metadata width exceeds 80 columns")
	}

	sprite := surface.HermesSpriteDefinition()
	checkGridsAndPalette(t, "sprite", sprite.Palette,
		struct {
			name   string
			rows   []string
			width  int
			height int
		}{"mascot", sprite.Mascot, surface.LoginMascotWidth, surface.LoginMascotHeight},
	)
	if sprite.ID != "hermes-pig" || len(sprite.ID) > 32 {
		t.Errorf("unexpected sprite.ID: %q", sprite.ID)
	}
	if sprite.Name != "Hermes PiG" || len(sprite.Name) > 24 {
		t.Errorf("unexpected sprite.Name: %q", sprite.Name)
	}
	if sprite.Tagline == "" || len(sprite.Tagline) > 76 {
		t.Errorf("unexpected sprite.Tagline: %q", sprite.Tagline)
	}
}

func TestGenerateAndEnsureHermesThemeFiles(t *testing.T) {
	// Every color token in PiG v0.8.0's theme_dark.json schema.
	requiredColorTokens := []string{
		"accent", "border", "borderAccent", "borderMuted",
		"success", "error", "warning", "muted", "dim", "text", "thinkingText",
		"selectedBg", "scrollbarTrack", "scrollbarThumb", "searchMatchBg", "searchMatchText",
		"userMessageBg", "userMessageText",
		"customMessageBg", "customMessageText", "customMessageLabel",
		"toolPendingBg", "toolSuccessBg", "toolErrorBg", "toolTitle", "toolOutput",
		"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder",
		"mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
		"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
		"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable",
		"syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
		"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium",
		"thinkingHigh", "thinkingXhigh", "thinkingMax", "bashMode",
	}

	agentDir := t.TempDir()
	if err := surface.EnsureThemeFiles(agentDir); err != nil {
		t.Fatalf("EnsureThemeFiles: %v", err)
	}
	// Calling a second time must be idempotent.
	if err := surface.EnsureThemeFiles(agentDir); err != nil {
		t.Fatalf("EnsureThemeFiles (2nd call): %v", err)
	}

	names := surface.BuiltinSkinNames()
	if len(names) != 9 {
		t.Fatalf("expected 9 builtin skins, got %d", len(names))
	}
	for _, name := range names {
		themeName := "hermes-" + name
		path := filepath.Join(agentDir, "themes", themeName+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing theme file %q: %v", path, err)
		}
		var doc surface.ThemeDocument
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("invalid JSON in %q: %v", path, err)
		}
		if doc.Schema != "https://pi.dev/schemas/theme.schema.json" {
			t.Errorf("%s: unexpected $schema %q", themeName, doc.Schema)
		}
		if doc.Name != themeName {
			t.Errorf("%s: got name %q", themeName, doc.Name)
		}
		wantAppearance := "dark"
		if name == "daylight" || name == "warm-lightmode" {
			wantAppearance = "light"
		}
		if doc.Appearance != wantAppearance {
			t.Errorf("%s: appearance = %q, want %q", themeName, doc.Appearance, wantAppearance)
		}
		for _, tok := range requiredColorTokens {
			v, ok := doc.Colors[tok]
			if !ok || strings.TrimSpace(v) == "" {
				t.Errorf("%s: missing color token %q", themeName, tok)
			}
		}
		for _, expTok := range []string{"pageBg", "cardBg", "infoBg"} {
			if strings.TrimSpace(doc.Export[expTok]) == "" {
				t.Errorf("%s: missing export token %q", themeName, expTok)
			}
		}
		if name == "default" {
			if doc.Vars["accent"] != "#FFD700" || doc.Vars["border"] != "#CD7F32" {
				t.Errorf("hermes-default missing signature gold/bronze palette: %+v", doc.Vars)
			}
		}
	}
}

func TestOnSessionStartAndSkinSwitchBrandingAndThemes(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	hostInfo, _ := json.Marshal(map[string]any{"agentDir": agentDir})
	t.Setenv("PIG_EXT_HOST_INFO", string(hostInfo))
	t.Setenv("HERMES_HOME", root)
	t.Setenv("HERMES_PROFILE", "")

	p := paths.ResolveProfilePaths("")
	cfg := config.Default()
	memStore := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)

	ext := sdk.New("pig-hermes-branding-test")
	surface.Register(ext, p, cfg, memStore)

	host := startFakeHost(t, ext, func(method string, _ map[string]any) (any, string) {
		if method == "ui.setTheme" {
			return map[string]any{"success": true}, ""
		}
		return map[string]any{}, ""
	})

	host.fireEvent("session_start", map[string]any{})

	var sawSprite, sawLogin bool
	var themeCalls []string
	host.mu.Lock()
	for _, c := range host.calls {
		switch c.Method {
		case "ui.registerSprite":
			if id, _ := c.Args["id"].(string); id == "hermes-pig" {
				sawSprite = true
			}
		case "ui.setLogin":
			if name, _ := c.Args["name"].(string); name == "pig-hermes" {
				sawLogin = true
			}
		case "ui.setTheme":
			if theme, _ := c.Args["theme"].(string); theme != "" {
				themeCalls = append(themeCalls, theme)
			}
		}
	}
	host.mu.Unlock()

	if !sawSprite {
		t.Errorf("expected OnSessionStart to call ui.registerSprite for hermes-pig")
	}
	if !sawLogin {
		t.Errorf("expected OnSessionStart to call ui.setLogin for pig-hermes")
	}
	if len(themeCalls) == 0 || themeCalls[len(themeCalls)-1] != "hermes-default" {
		t.Errorf("expected OnSessionStart to call ui.setTheme(\"hermes-default\"), got %v", themeCalls)
	}

	// Remove hermes-ares.json to prove /skin ares ensures it before calling ui.setTheme.
	aresPath := filepath.Join(agentDir, "themes", "hermes-ares.json")
	_ = os.Remove(aresPath)

	host.runCommand("skin", "ares")

	if _, err := os.Stat(aresPath); err != nil {
		t.Fatalf("expected /skin ares to write %q: %v", aresPath, err)
	}
	var lastTheme string
	host.mu.Lock()
	for _, c := range host.calls {
		if c.Method == "ui.setTheme" {
			if theme, _ := c.Args["theme"].(string); theme != "" {
				lastTheme = theme
			}
		}
	}
	host.mu.Unlock()
	if lastTheme != "hermes-ares" {
		t.Errorf("expected /skin ares to call ui.setTheme(\"hermes-ares\"), got %q", lastTheme)
	}
}

func TestWave3ToolsetsIncludeNewTools(t *testing.T) {
	cases := map[string][]string{
		"tool_search": {"tool_search", "tool_describe", "tool_call"},
		"browser":     {"browser_exec"},
		"vision":      {"vision_analyze"},
		"image_gen":   {"image_generate"},
		"tts":         {"text_to_speech"},
	}
	for name, wantTools := range cases {
		ts, ok := surface.GetToolset(name)
		if !ok {
			t.Fatalf("expected toolset %q to exist", name)
		}
		resolved := surface.ResolveToolsetTools(name)
		for _, want := range wantTools {
			found := false
			for _, got := range resolved {
				if got == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("toolset %q (%+v) missing tool %q", name, ts, want)
			}
		}
	}

	cliTools := surface.ResolveToolsetTools("hermes-cli")
	cliSet := make(map[string]bool, len(cliTools))
	for _, tool := range cliTools {
		cliSet[tool] = true
	}
	for _, want := range []string{
		"tool_search", "tool_describe", "tool_call",
		"browser_exec", "vision_analyze", "image_generate", "text_to_speech",
	} {
		if !cliSet[want] {
			t.Errorf("hermes-cli toolset missing Wave 3 tool %q", want)
		}
	}
}

func TestWave3SessionAndContextSlashCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HERMES_HOME", root)
	t.Setenv("HERMES_PROFILE", "")

	p := paths.ResolveProfilePaths("")
	cfg := config.Default()
	memStore := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)
	if err := os.WriteFile(p.SoulFile, []byte("Hermes test soul"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a PiG JSONL session file so /sessions discovers it.
	sessDir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessFile := filepath.Join(sessDir, "sess-wave3.jsonl")
	jsonlLines := strings.Join([]string{
		`{"type":"session","id":"sess-wave3","cwd":"/tmp/demo","timestamp":"2026-11-27T10:00:00Z"}`,
		`{"type":"session_info","name":"Wave3 Planning","timestamp":"2026-11-27T10:00:00Z"}`,
		`{"type":"message","id":"m1","parentId":"","timestamp":"2026-11-27T10:00:01Z","message":{"role":"user","content":"Plan wave 3 slash commands"}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-11-27T10:00:02Z","message":{"role":"assistant","content":"Sure, adding /context and /sessions"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessFile, []byte(jsonlLines), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionEntries := []map[string]any{
		{
			"type":      "message",
			"id":        "e1",
			"parentId":  "",
			"timestamp": "2026-11-27T10:00:01Z",
			"message": map[string]any{
				"role":    "user",
				"content": "First user turn",
			},
		},
		{
			"type":      "message",
			"id":        "e2",
			"parentId":  "e1",
			"timestamp": "2026-11-27T10:00:02Z",
			"message": map[string]any{
				"role":    "assistant",
				"content": "First assistant reply",
			},
		},
		{
			"type":      "message",
			"id":        "e3",
			"parentId":  "e2",
			"timestamp": "2026-11-27T10:00:03Z",
			"message": map[string]any{
				"role":    "user",
				"content": "Second user turn to retry",
			},
		},
	}

	var retriedContent string
	var navTargetID string

	ext := sdk.New("pig-hermes-wave3-cmd-test")
	surface.Register(ext, p, cfg, memStore)

	host := startFakeHost(t, ext, func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": "sess-wave3"}, ""
		case "getSessionFile":
			return map[string]any{"sessionFile": sessFile}, ""
		case "getContextUsage":
			return map[string]any{"tokens": 1200, "contextWindow": 200000, "percent": 0.6}, ""
		case "watchSessionLog":
			if complete, _ := args["complete"].(bool); complete {
				return map[string]any{
					"entries":    []map[string]any{},
					"entryCount": len(sessionEntries),
					"hasMore":    false,
					"leafId":     "e3",
				}, ""
			}
			return map[string]any{
				"entries":    sessionEntries,
				"entryCount": len(sessionEntries),
				"hasMore":    false,
				"leafId":     "e3",
			}, ""
		case "sendUserMessage":
			if s, ok := args["content"].(string); ok {
				retriedContent = s
			}
			return map[string]any{}, ""
		case "navigateTree":
			if id, ok := args["targetId"].(string); ok {
				navTargetID = id
			}
			return map[string]any{"cancelled": false}, ""
		default:
			return map[string]any{}, ""
		}
	})

	// Verify all 6 new command names are registered.
	for _, cmd := range []string{"context", "ctx", "sessions", "save", "retry", "undo"} {
		if !host.cmds[cmd] {
			t.Errorf("slash command /%s not registered", cmd)
		}
	}

	lastMsg := func() string {
		msgs := host.lastSentMessages()
		if len(msgs) == 0 {
			return ""
		}
		return msgs[len(msgs)-1]
	}

	// 1. /context and /ctx
	host.runCommand("context", "")
	if got := lastMsg(); !strings.Contains(got, "Hermes Context Manifest") || !strings.Contains(got, "SOUL.md") || !strings.Contains(got, "MEMORY.md") {
		t.Errorf("/context unexpected output: %q", got)
	}
	host.runCommand("ctx", "")
	if got := lastMsg(); !strings.Contains(got, "Hermes Context Manifest") {
		t.Errorf("/ctx alias unexpected output: %q", got)
	}

	// 2. /sessions
	host.runCommand("sessions", "")
	if got := lastMsg(); !strings.Contains(got, "sess-wave3") || !strings.Contains(got, "[pig]") {
		t.Errorf("/sessions unexpected output: %q", got)
	}
	host.runCommand("sessions", "Wave3")
	if got := lastMsg(); !strings.Contains(got, "sess-wave3") {
		t.Errorf("/sessions query unexpected output: %q", got)
	}

	// 3. /save md & /save json
	mdPath := filepath.Join(t.TempDir(), "export.md")
	host.runCommand("save", "md "+mdPath)
	mdRaw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("/save md did not write file: %v", err)
	}
	if !strings.Contains(string(mdRaw), "First user turn") || !strings.Contains(string(mdRaw), "Second user turn to retry") {
		t.Errorf("/save md content missing messages: %s", string(mdRaw))
	}

	jsonPath := filepath.Join(t.TempDir(), "export.json")
	host.runCommand("save", "json "+jsonPath)
	jsonRaw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("/save json did not write file: %v", err)
	}
	var exported map[string]any
	if err := json.Unmarshal(jsonRaw, &exported); err != nil {
		t.Fatalf("/save json invalid JSON: %v", err)
	}
	if count, _ := exported["count"].(float64); int(count) != len(sessionEntries) {
		t.Errorf("/save json expected count=%d, got %v", len(sessionEntries), exported["count"])
	}

	// 4. /retry
	host.runCommand("retry", "")
	if retriedContent != "Second user turn to retry" {
		t.Errorf("/retry expected %q, got %q", "Second user turn to retry", retriedContent)
	}

	// 5. /undo
	host.runCommand("undo", "")
	if navTargetID != "e2" {
		t.Errorf("/undo expected NavigateTree targetId=%q, got %q", "e2", navTargetID)
	}
	if got := lastMsg(); !strings.Contains(got, "Undid 1 turn(s)") {
		t.Errorf("/undo unexpected output: %q", got)
	}
}
