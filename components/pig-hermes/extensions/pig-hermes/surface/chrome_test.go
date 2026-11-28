package surface

import (
	"context"
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
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

func newTestChromeState(t *testing.T) (*state, paths.ProfilePaths) {
	t.Helper()
	home := t.TempDir()
	p := paths.ProfilePaths{
		RootHome:    home,
		ProfileName: "work",
		Home:        home,
		ConfigFile:  filepath.Join(home, "config.yaml"),
		EnvFile:     filepath.Join(home, ".env"),
		MemoriesDir: filepath.Join(home, "memories"),
		SkillsDir:   filepath.Join(home, "skills"),
		CronJobsFile: filepath.Join(home, "cron", "jobs.json"),
		SoulFile:    filepath.Join(home, "SOUL.md"),
	}
	skillDir := filepath.Join(p.SkillsDir, "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: deploy\ndescription: deploy app\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Model = "claude-sonnet-4"
	cfg.Skin = "default"
	cfg.Toolset = "coding"
	cfg.MCPServers = map[string]config.MCPServerConfig{
		"github": {Name: "github", Command: "npx", Enabled: true},
		"off":    {Name: "off", Command: "npx", Enabled: false},
	}

	memStore := memory.NewStore(p.MemoriesDir, 1000, 1000)
	_, _ = memStore.Apply(context.Background(), memory.TargetMemory, []memory.Operation{
		{Action: "add", Content: strings.Repeat("x", 250)},
	})
	st := newState(p, cfg, memStore)
	st.cwd = "/tmp/project-hermes"
	return st, p
}

func TestRenderHermesHeaderWideAndNarrow(t *testing.T) {
	st, _ := newTestChromeState(t)

	// Wide (>= 76): rounded Unicode 2-column bordered box
	wide := renderHermesHeader(st, 96)
	if len(wide) < 6 {
		t.Fatalf("expected >=6 lines for wide header, got %d", len(wide))
	}
	topPlain := stripANSI(wide[0])
	botPlain := stripANSI(wide[len(wide)-1])
	if !strings.HasPrefix(topPlain, "╭") || !strings.HasSuffix(topPlain, "╮") {
		t.Fatalf("expected rounded top border, got %q", topPlain)
	}
	if !strings.Contains(topPlain, "Hermes Agent · pig-hermes") {
		t.Fatalf("expected title in top border, got %q", topPlain)
	}
	if !strings.HasPrefix(botPlain, "╰") || !strings.HasSuffix(botPlain, "╯") {
		t.Fatalf("expected rounded bottom border, got %q", botPlain)
	}
	// Verify truecolor ANSI sequence present
	if !strings.Contains(wide[0], "\x1b[38;2;") {
		t.Fatalf("expected ANSI truecolor escape in wide header, got %q", wide[0])
	}

	joinedWide := stripANSI(strings.Join(wide, "\n"))
	for _, want := range []string{
		"claude-sonnet-4",
		"Profile: work",
		"Available Tools: coding",
		"Skills:          1 discovered",
		"MCP Servers:     1 configured",
		"Memory:",
		"/help · /skin · /indicator · /status",
	} {
		if !strings.Contains(joinedWide, want) {
			t.Errorf("wide header missing %q:\n%s", want, joinedWide)
		}
	}

	// Verify 2-column divider on body rows and width fit
	for i := 1; i < len(wide)-1; i++ {
		plain := stripANSI(wide[i])
		if !strings.HasPrefix(plain, "│") || !strings.HasSuffix(plain, "│") {
			t.Errorf("row %d missing outer borders: %q", i, plain)
		}
		if strings.Count(plain, "│") < 3 {
			t.Errorf("row %d missing center column divider: %q", i, plain)
		}
		if w := visibleWidth(wide[i]); w != 96 {
			t.Errorf("row %d visible width = %d, want 96 (%q)", i, w, plain)
		}
	}

	// YOLO badge appears when enabled
	st.cfg.SetYolo(true)
	wideYolo := stripANSI(strings.Join(renderHermesHeader(st, 96), "\n"))
	if !strings.Contains(wideYolo, "⚠ YOLO mode") {
		t.Errorf("expected YOLO badge in wide header:\n%s", wideYolo)
	}

	// Narrow (< 76): compact 2-line responsive banner that never exceeds width
	narrow := renderHermesHeader(st, 60)
	if len(narrow) != 2 {
		t.Fatalf("expected 2 lines for narrow header, got %d", len(narrow))
	}
	for i, line := range narrow {
		if w := visibleWidth(line); w > 60 {
			t.Errorf("narrow line %d width %d > 60: %q", i, w, stripANSI(line))
		}
	}
	narrowPlain := stripANSI(strings.Join(narrow, "\n"))
	if !strings.Contains(narrowPlain, "Hermes Agent · pig-hermes") || !strings.Contains(narrowPlain, "YOLO") {
		t.Errorf("unexpected narrow header:\n%s", narrowPlain)
	}
}

func TestRenderHermesFooterWidthTiersAndColors(t *testing.T) {
	st, _ := newTestChromeState(t)
	st.contextPct = 62
	st.hasContextPct = true
	usage.Default().Add("chrome-test-sess", "", "claude-sonnet-4", 1200, 300, 0.01, 1, 1)

	// Narrow tier (< 52): profile + meter only, no key hints
	narrow := renderHermesFooter(st, 44, nil)
	if len(narrow) != 1 {
		t.Fatalf("expected 1 footer line, got %d", len(narrow))
	}
	narrowPlain := stripANSI(narrow[0])
	if !strings.Contains(narrowPlain, "☤ work") || !strings.Contains(narrowPlain, "62%") {
		t.Errorf("narrow footer missing profile or meter: %q", narrowPlain)
	}
	if strings.Contains(narrowPlain, "/ cmds") {
		t.Errorf("narrow footer should not include right-aligned hints: %q", narrowPlain)
	}
	if w := visibleWidth(narrow[0]); w > 44 {
		t.Errorf("narrow footer width %d > 44", w)
	}

	// Medium tier (52-75): profile + skin + meter + tokens, no hints
	med := renderHermesFooter(st, 64, nil)
	medPlain := stripANSI(med[0])
	for _, want := range []string{"☤ work", "default", "62%", "Σ"} {
		if !strings.Contains(medPlain, want) {
			t.Errorf("medium footer missing %q: %q", want, medPlain)
		}
	}
	if strings.Contains(medPlain, "/ cmds") {
		t.Errorf("medium footer should not include key hints: %q", medPlain)
	}

	// Wide tier (>= 76): includes right-aligned key hints
	wide := renderHermesFooter(st, 100, nil)
	widePlain := stripANSI(wide[0])
	if !strings.Contains(widePlain, "/ cmds · @ files · ! shell") {
		t.Errorf("wide footer missing key hints: %q", widePlain)
	}
	if w := visibleWidth(wide[0]); w > 100 {
		t.Errorf("wide footer width %d > 100", w)
	}

	// Context meter color thresholds
	thresholds := []struct {
		pct  int
		want string
	}{
		{0, "#8FBC8F"},
		{49, "#8FBC8F"},
		{50, "#FFD700"},
		{80, "#FFD700"},
		{81, "#FF8C00"},
		{94, "#FF8C00"},
		{95, "#FF6B6B"},
		{100, "#FF6B6B"},
	}
	for _, tc := range thresholds {
		got := ContextMeterColor(tc.pct)
		if got != tc.want {
			t.Errorf("ContextMeterColor(%d) = %q, want %q", tc.pct, got, tc.want)
		}
		st.contextPct = tc.pct
		line := renderHermesFooter(st, 90, nil)[0]
		ansi := hexToANSI(tc.want)
		if !strings.Contains(line, ansi) {
			t.Errorf("footer at %d%% missing threshold ANSI color %q (%s)", tc.pct, tc.want, ansi)
		}
	}

	// ExtensionStatuses are appended in sorted key order, skipping redundant "hermes" key.
	withStatuses := renderHermesFooter(st, 120, map[string]string{
		"hermes": "☤ work | default",
		"zeta":   "Z:ok",
		"omh":    "OMH:ulw",
		"empty":  "   ",
	})
	stPlain := stripANSI(withStatuses[0])
	omhIdx := strings.Index(stPlain, "OMH:ulw")
	zetaIdx := strings.Index(stPlain, "Z:ok")
	if omhIdx < 0 || zetaIdx < 0 || omhIdx > zetaIdx {
		t.Errorf("expected sorted extension statuses (omh before zeta) in footer, got: %q", stPlain)
	}
	if strings.Contains(stPlain, "☤ work | default") {
		t.Errorf("expected redundant 'hermes' status to be skipped, got: %q", stPlain)
	}
}

func TestIndicatorStylesAndFrames(t *testing.T) {
	if len(IndicatorFrames(IndicatorKaomoji)) != 15 {
		t.Fatalf("expected 15 kaomoji frames, got %d", len(IndicatorFrames(IndicatorKaomoji)))
	}
	if len(IndicatorFrames(IndicatorEmoji)) != 6 {
		t.Fatalf("expected 6 emoji frames, got %d", len(IndicatorFrames(IndicatorEmoji)))
	}
	if len(IndicatorFrames(IndicatorUnicode)) != 10 {
		t.Fatalf("expected 10 unicode frames, got %d", len(IndicatorFrames(IndicatorUnicode)))
	}
	if len(IndicatorFrames(IndicatorASCII)) != 4 {
		t.Fatalf("expected 4 ascii frames, got %d", len(IndicatorFrames(IndicatorASCII)))
	}

	for _, style := range BuiltinIndicatorStyles() {
		msg0 := FormatWorkingMessage(style, 0)
		msg1 := FormatWorkingMessage(style, 1)
		if msg0 == "" || msg1 == "" || msg0 == msg1 {
			t.Errorf("style %s produced invalid rotating messages: %q vs %q", style, msg0, msg1)
		}
	}
}

// ── Wire-level test for header/footer renderers, working messages & /indicator

type chromeWireFrame struct {
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
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

type chromeTestHost struct {
	t        *testing.T
	nc       net.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []struct{ method string; args map[string]any }
	handlers map[string][]int
	cmds     map[string]bool
	pending  map[string]chan json.RawMessage
	nextID   int
	runDone  chan error
}

func startChromeHost(t *testing.T, ext *sdk.Extension) *chromeTestHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &chromeTestHost{
		t:        t,
		nc:       hostSide,
		handlers: make(map[string][]int),
		cmds:     make(map[string]bool),
		pending:  make(map[string]chan json.RawMessage),
		runDone:  make(chan error, 1),
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.readFrame()
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got %+v", first)
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	for _, c := range first.Register.Commands {
		h.cmds[c.Name] = true
	}

	stateRaw, _ := json.Marshal(map[string]any{"hasUI": true})
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          t.TempDir(),
			"mode":         "tui",
			"width":        96,
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
					h.calls = append(h.calls, struct{ method string; args map[string]any }{f.Call.Method, args})
					h.mu.Unlock()
					go func(callID string) {
						h.writeFrame(map[string]any{
							"type":        "call_result",
							"id":          callID,
							"call_result": map[string]any{"result": map[string]any{}},
						})
					}(f.ID)
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

func (h *chromeTestHost) close() {
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}

func (h *chromeTestHost) writeFrame(v any) {
	payload, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	h.writeMu.Lock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(payload)
	h.writeMu.Unlock()
}

func (h *chromeTestHost) tryReadFrame() (chromeWireFrame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		return chromeWireFrame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return chromeWireFrame{}, err
	}
	var f chromeWireFrame
	err := json.Unmarshal(buf, &f)
	return f, err
}

func (h *chromeTestHost) readFrame() chromeWireFrame {
	f, err := h.tryReadFrame()
	if err != nil {
		h.t.Fatalf("readFrame: %v", err)
	}
	return f
}

func (h *chromeTestHost) sendReq(req map[string]any) {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.writeFrame(map[string]any{
		"type":    "request",
		"id":      id,
		"request": req,
	})
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		h.t.Fatalf("timed out waiting for response to %v", req["method"])
	}
}

func (h *chromeTestHost) fireEvent(event string) {
	for _, hid := range h.handlers[event] {
		payload, _ := json.Marshal(map[string]any{})
		h.sendReq(map[string]any{
			"method":     "event",
			"handler_id": hid,
			"event":      event,
			"args":       json.RawMessage(payload),
		})
	}
}

func (h *chromeTestHost) runCommand(name, args string) {
	argv, _ := json.Marshal(args)
	h.sendReq(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   json.RawMessage(argv),
	})
}

func TestChromeLifecycleAndIndicatorCommand(t *testing.T) {
	_, p := newTestChromeState(t)
	cfg := config.Default()
	cfg.Model = "gpt-5"
	memStore := memory.NewStore(p.MemoriesDir, 1000, 1000)

	ext := sdk.New("pig-hermes-chrome-test")
	Register(ext, p, cfg, memStore)
	host := startChromeHost(t, ext)
	defer host.close()

	if !host.cmds["indicator"] {
		t.Fatalf("expected /indicator command to be registered")
	}
	if !host.cmds["banner"] {
		t.Fatalf("expected /banner command to be registered")
	}

	// Session start installs SetLogin and footer renderer, and must NOT call ui.setHeader
	// (which would overwrite the winged-pig SetLogin screen).
	host.fireEvent("session_start")

	// Switch indicator style via /indicator unicode and verify persistence in config.yaml
	host.runCommand("indicator", "unicode")
	rawCfg, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		t.Fatalf("expected config.yaml to be written: %v", err)
	}
	if !strings.Contains(string(rawCfg), "display.indicator_style: unicode") {
		t.Fatalf("expected display.indicator_style: unicode in config.yaml, got:\n%s", string(rawCfg))
	}

	// Status subcommand works
	host.runCommand("indicator", "status")

	// /banner displays 2-column summary on demand, /banner pin installs widget, /banner off clears it
	host.runCommand("banner", "")
	host.runCommand("banner", "pin")
	host.runCommand("banner", "off")

	// /skin and /yolo also refresh footer renderer
	host.runCommand("skin", "ares")
	host.runCommand("yolo", "on")

	// Agent start / turn start / turn end / agent end set, refresh footer, and clear working message
	host.fireEvent("agent_start")
	host.fireEvent("turn_start")
	host.fireEvent("turn_end")
	host.fireEvent("agent_end")

	host.mu.Lock()
	calls := append([]struct{ method string; args map[string]any }(nil), host.calls...)
	host.mu.Unlock()

	var sawHeader, sawLogin, sawWorkingIndicator, sawWorkingSet, sawWorkingClear, sawIndicatorMsg, sawBannerMsg, sawBannerWidget bool
	footerCalls := 0
	for _, c := range calls {
		switch c.method {
		case "ui.setHeader":
			sawHeader = true
		case "ui.setLogin":
			sawLogin = true
		case "ui.setFooter":
			footerCalls++
		case "ui.setWidget":
			if key, _ := c.args["key"].(string); key == "hermes_banner" {
				sawBannerWidget = true
			}
		case "ui.setWorkingIndicator":
			if frames, ok := c.args["frames"].([]any); ok && len(frames) == 10 {
				sawWorkingIndicator = true
			}
		case "ui.setWorkingMessage":
			if msg, ok := c.args["message"].(string); ok && msg != "" {
				// Unicode style emits bare "· <N>s" while native loader spins braille frames.
				if strings.Contains(msg, "· ") && strings.HasSuffix(msg, "s") {
					sawWorkingSet = true
				}
			} else if _, hasMsg := c.args["message"]; !hasMsg {
				sawWorkingClear = true
			}
		case "sendMessage":
			if m, ok := c.args["message"].(map[string]any); ok {
				txt, _ := m["content"].(string)
				if strings.Contains(txt, "Active Busy Indicator: unicode") {
					sawIndicatorMsg = true
				}
				if strings.Contains(txt, "Hermes Agent · pig-hermes") {
					sawBannerMsg = true
				}
			}
		}
	}

	if sawHeader {
		t.Errorf("OnSessionStart must NOT call ui.setHeader (it wipes SetLogin)")
	}
	if !sawLogin {
		t.Errorf("expected OnSessionStart to preserve winged-pig login via ui.setLogin")
	}
	// Expect footer pushed on session_start, /indicator, /skin, /yolo, agent_start, turn_end, agent_end (>= 6)
	if footerCalls < 6 {
		t.Errorf("expected ui.setFooter to be re-pushed on lifecycle and commands (>=6), got %d", footerCalls)
	}
	if !sawBannerMsg {
		t.Errorf("expected /banner to send 2-column Hermes summary banner")
	}
	if !sawBannerWidget {
		t.Errorf("expected /banner pin to set hermes_banner widget")
	}
	if !sawWorkingIndicator {
		t.Errorf("expected agent_start to configure native working indicator with unicode frames")
	}
	if !sawWorkingSet {
		t.Errorf("expected agent_start to set live working message with elapsed seconds")
	}
	if !sawWorkingClear {
		t.Errorf("expected agent_end to clear working message")
	}
	if !sawIndicatorMsg {
		t.Errorf("expected /indicator status to report active unicode indicator")
	}
}

func TestToolRenderShellSelfAndExpandedDiff(t *testing.T) {
	st, _ := newTestChromeState(t)

	tr := st.toolRendererResolver("read_file", func() *sdk.ToolRenderers { return nil })
	if tr == nil || tr.Shell != sdk.ToolRenderShellSelf {
		t.Fatalf("expected ToolRenderShellSelf for read_file, got %+v", tr)
	}
	callLines, err := tr.Call(sdk.Context{}, map[string]any{"path": "main.go", "offset": 10, "limit": 20}, sdk.ToolRenderContext{}, 100)
	if err != nil || len(callLines) != 1 {
		t.Fatalf("unexpected call lines: %v (err=%v)", callLines, err)
	}
	callPlain := stripANSI(callLines[0])
	if !strings.Contains(callPlain, "├─ 📖 read main.go (10+20)") {
		t.Errorf("unexpected read_file call render: %q", callPlain)
	}
	if !strings.Contains(callLines[0], "\x1b[38;2;") {
		t.Errorf("expected ANSI truecolor in call line: %q", callLines[0])
	}

	// Check all mapped Hermes tools render their upstream glyph + verb.
	mapped := map[string]string{
		"write_file":     "✏️ write",
		"patch":          "🔧 patch",
		"search_files":   "🔍 search",
		"terminal":       "💻 $",
		"process_manage": "⚙️ proc",
		"memory":         "🧠 memory",
		"skills_list":    "⚡ skill",
		"delegate_task":  "⛓ delegate",
		"web_search":     "🌐 web",
		"execute_code":   "🐍 exec",
		"cronjob_manage": "⏰ cron",
		"todo_list":      "☑ todo",
		"browser_exec":   "🌍 browser",
		"vision_analyze": "👁 vision",
		"image_generate": "🎨 image",
		"text_to_speech": "🔊 tts",
		"tool_search":    "🔎 tools",
		"clarify":        "☤ clarify",
	}
	for name, want := range mapped {
		r := st.toolRendererResolver(name, func() *sdk.ToolRenderers { return nil })
		if r == nil || r.Shell != sdk.ToolRenderShellSelf {
			t.Fatalf("tool %s missing ToolRenderShellSelf", name)
		}
		lines, err := r.Call(sdk.Context{}, map[string]any{}, sdk.ToolRenderContext{}, 80)
		if err != nil || len(lines) != 1 || !strings.Contains(stripANSI(lines[0]), want) {
			t.Errorf("tool %s call render = %v (err=%v), want substring %q", name, lines, err, want)
		}
	}

	// Patch tool renders ✓ summary + │-guttered unified diff with green (+) and red (-) lines.
	patchR := st.toolRendererResolver("patch", func() *sdk.ToolRenderers { return nil })
	diffPayload := "--- a/foo.go\n+++ b/foo.go\n@@ -1,2 +1,2 @@\n-oldLine()\n+newLine()"
	resLines, err := patchR.Result(
		sdk.Context{},
		sdk.ToolRenderResult{
			Content: []map[string]any{{"type": "text", "text": "patched foo.go"}},
			Details: map[string]any{"diff": diffPayload},
		},
		sdk.ToolRenderResultOptions{Expanded: true},
		sdk.ToolRenderContext{},
		100,
	)
	if err != nil {
		t.Fatalf("patch Result error: %v", err)
	}
	if len(resLines) < 5 {
		t.Fatalf("expected summary + diff lines, got %d: %v", len(resLines), resLines)
	}
	if !strings.Contains(stripANSI(resLines[0]), "✓ patched foo.go") {
		t.Errorf("expected ✓ summary line, got %q", stripANSI(resLines[0]))
	}
	joined := strings.Join(resLines[1:], "\n")
	joinedPlain := stripANSI(joined)
	if !strings.Contains(joinedPlain, "│ -oldLine()") || !strings.Contains(joinedPlain, "│ +newLine()") {
		t.Errorf("expected guttered diff lines, got:\n%s", joinedPlain)
	}
	sk := st.currentSkin()
	if !strings.Contains(joined, hexToANSI(sk.Color("ui_ok", "#4caf50"))) {
		t.Errorf("expected green ANSI on '+' diff line")
	}
	if !strings.Contains(joined, hexToANSI(sk.Color("ui_error", "#ef5350"))) {
		t.Errorf("expected red ANSI on '-' diff line")
	}

	// Error result renders ✗ glyph.
	errLines, err := patchR.Result(
		sdk.Context{},
		sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "hunk failed"}}},
		sdk.ToolRenderResultOptions{},
		sdk.ToolRenderContext{IsError: true},
		80,
	)
	if err != nil || len(errLines) == 0 || !strings.Contains(stripANSI(errLines[0]), "✗ hunk failed") {
		t.Errorf("expected ✗ error summary, got %v (err=%v)", errLines, err)
	}
}

type fakeEditorBase struct {
	text          string
	lastInput     string
	lastWidth     int
	lastMouseEv   sdk.EditorMouseEvent
	mouseHandled  bool
	lines         []string
}

func (f *fakeEditorBase) HandleInput(data string) error            { f.lastInput = data; return nil }
func (f *fakeEditorBase) HandleMouse(ev sdk.EditorMouseEvent) error { f.lastMouseEv = ev; f.mouseHandled = true; return nil }
func (f *fakeEditorBase) Render(width int) ([]string, error)       { f.lastWidth = width; return f.lines, nil }
func (f *fakeEditorBase) SetText(text string) error                { f.text = text; return nil }
func (f *fakeEditorBase) InsertTextAtCursor(text string) error     { f.text += text; return nil }
func (f *fakeEditorBase) AddToHistory(string) error                { return nil }
func (f *fakeEditorBase) GetText() (string, error)                 { return f.text, nil }

func TestHermesEditorComponentChromeAndPromptGlyph(t *testing.T) {
	st, _ := newTestChromeState(t)
	cursorLine := "hello \x1b_pi:c\x07world"
	baseBottom := strings.Repeat("─", 72)
	base := &fakeEditorBase{lines: []string{strings.Repeat("─", 72), cursorLine, "second line", baseBottom}}
	ed := newHermesEditorComponent(st, base)

	if err := ed.SetText("draft"); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	if got, err := ed.GetText(); err != nil || got != "draft" {
		t.Fatalf("GetText = %q, %v; want draft", got, err)
	}
	if err := ed.HandleInput("x"); err != nil || base.lastInput != "x" {
		t.Fatalf("HandleInput did not forward to base: err=%v got=%q", err, base.lastInput)
	}
	ev := sdk.EditorMouseEvent{X: 4, Y: 2}
	if err := ed.HandleMouse(ev); err != nil || !base.mouseHandled || base.lastMouseEv.Y != 2 || base.lastMouseEv.X != 4 {
		t.Fatalf("HandleMouse did not pass through unchanged: err=%v ev=%+v", err, base.lastMouseEv)
	}
	if err := ed.Invalidate(); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	lines, err := ed.Render(72)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if base.lastWidth != 72 {
		t.Fatalf("expected base.Render called with full width 72, got %d", base.lastWidth)
	}
	if len(lines) != 4 {
		t.Fatalf("expected same row count (4) as base, got %d", len(lines))
	}
	topPlain := stripANSI(lines[0])
	if !strings.Contains(topPlain, "─") || !strings.Contains(topPlain, "☤ hermes · work") {
		t.Errorf("unexpected top rule: %q", topPlain)
	}
	if w := visibleWidth(lines[0]); w != 72 {
		t.Errorf("expected top rule visible width 72, got %d", w)
	}
	// Content, APC cursor marker, and bottom border rows must be untouched verbatim.
	if lines[1] != cursorLine {
		t.Errorf("expected APC cursor line preserved verbatim, got %q", lines[1])
	}
	if lines[2] != "second line" {
		t.Errorf("expected second content line preserved verbatim, got %q", lines[2])
	}
	if lines[3] != baseBottom {
		t.Errorf("expected base bottom border preserved verbatim, got %q", lines[3])
	}

	// Switching skin updates top rule symbol; busy state shows live badge in top rule.
	st.mu.Lock()
	st.skin = "ares"
	st.busy = true
	st.busyStarted = time.Now()
	st.mu.Unlock()

	busyLines, err := ed.Render(72)
	if err != nil {
		t.Fatalf("busy Render: %v", err)
	}
	if !strings.Contains(stripANSI(busyLines[0]), "⚔") || !strings.Contains(stripANSI(busyLines[0]), "· 0s") {
		t.Errorf("expected ares symbol and busy badge in top rule, got %q", stripANSI(busyLines[0]))
	}
	if busyLines[1] != cursorLine {
		t.Errorf("expected content line still untouched in busy mode, got %q", busyLines[1])
	}
}

func TestBusySpinnerStartStopTeardown(t *testing.T) {
	st, _ := newTestChromeState(t)
	// Headless start/stop must not leak or block.
	st.startBusySpinner(sdk.Context{})
	if !st.isBusy() {
		t.Fatalf("expected state to be busy after startBusySpinner")
	}
	st.stopBusySpinner(sdk.Context{})
	if st.isBusy() {
		t.Fatalf("expected state to be idle after stopBusySpinner")
	}
	// Repeated stop is idempotent.
	st.stopBusySpinner(sdk.Context{})

	// Live message formatting across styles.
	if got := formatLiveWorkingMessage(IndicatorKaomoji, 0); got != "pondering · 0s" {
		t.Errorf("kaomoji 0s = %q, want 'pondering · 0s'", got)
	}
	if got := formatLiveWorkingMessage(IndicatorKaomoji, 3*time.Second); got != "contemplating · 3s" {
		t.Errorf("kaomoji 3s = %q, want 'contemplating · 3s'", got)
	}
	if got := formatLiveWorkingMessage(IndicatorUnicode, 5*time.Second); got != "· 5s" {
		t.Errorf("unicode 5s = %q, want '· 5s'", got)
	}
}

func TestRichFooterBusyIdleHintsModelAndGitBranch(t *testing.T) {
	st, _ := newTestChromeState(t)
	st.contextPct = 42
	st.hasContextPct = true

	idle := renderHermesFooterWithOpts(st, 120, nil, footerOptions{
		Model:     "gpt-5.4",
		GitBranch: "feat/pig-hermes",
	})
	idlePlain := stripANSI(idle[0])
	for _, want := range []string{
		"☤ work",
		"gpt-5.4",
		"42%",
		"project-hermes (feat/pig-hermes)",
		"/ cmds · @ files · ! shell",
	} {
		if !strings.Contains(idlePlain, want) {
			t.Errorf("idle rich footer missing %q: %q", want, idlePlain)
		}
	}

	st.mu.Lock()
	st.busy = true
	st.skin = "poseidon"
	st.mu.Unlock()

	busy := renderHermesFooterWithOpts(st, 120, nil, footerOptions{
		Model:     "gpt-5.4",
		GitBranch: "feat/pig-hermes",
	})
	busyPlain := stripANSI(busy[0])
	if !strings.Contains(busyPlain, "Ψ work") {
		t.Errorf("expected poseidon skin symbol in busy footer: %q", busyPlain)
	}
	if !strings.Contains(busyPlain, "enter steer · ctrl+c stop") {
		t.Errorf("expected busy hints in busy footer: %q", busyPlain)
	}
	if strings.Contains(busyPlain, "/ cmds · @ files · ! shell") {
		t.Errorf("busy footer should not show idle hints: %q", busyPlain)
	}
}
