package autolearn

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Tools []struct {
			Name          string          `json:"name"`
			Parameters    json.RawMessage `json:"parameters"`
			DefaultActive *bool           `json:"default_active"`
		} `json:"tools"`
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

type autolearnHost struct {
	t           *testing.T
	nc          net.Conn
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	handlers    map[string][]int
	pending     map[string]chan json.RawMessage
	nextID      int
	activeTools []string
	toolParams  map[string]json.RawMessage
	configHome  string
	sessionID   string
}

func startAutolearnHost(t *testing.T, ext *sdk.Extension, mode string, hasUI bool, configHome string) *autolearnHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &autolearnHost{
		t:           t,
		nc:          hostSide,
		runDone:     make(chan error, 1),
		handlers:    map[string][]int{},
		pending:     map[string]chan json.RawMessage{},
		activeTools: []string{"read", "bash"},
		toolParams:  map[string]json.RawMessage{},
		configHome:  configHome,
		sessionID:   "sess-al-" + mode,
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	reg := h.readFrame()
	if reg.Type != "register" || reg.Register == nil {
		t.Fatalf("expected register frame, got %s", reg.Type)
	}
	for _, hd := range reg.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	for _, tl := range reg.Register.Tools {
		h.toolParams[tl.Name] = tl.Parameters
	}
	stateRaw, _ := json.Marshal(map[string]any{
		"hasUI":     hasUI,
		"sessionId": h.sessionID,
		"model":     map[string]any{"id": "test-model", "provider": "test"},
	})
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"cwd":        t.TempDir(),
			"mode":       mode,
			"configHome": configHome,
			"state":      json.RawMessage(stateRaw),
		},
	})
	go h.serve()
	t.Cleanup(func() {
		h.writeFrame(map[string]any{"type": "shutdown"})
		_ = h.nc.Close()
		select {
		case <-h.runDone:
		case <-time.After(2 * time.Second):
		}
	})
	return h
}

func (h *autolearnHost) serve() {
	for {
		f, ok := h.readMaybe()
		if !ok {
			return
		}
		switch f.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				if f.Response != nil {
					ch <- f.Response.Result
				} else {
					ch <- nil
				}
			}
		case "call":
			if f.Call != nil {
				go h.answer(f)
			}
		}
	}
}

func (h *autolearnHost) answer(f wireFrame) {
	var args map[string]any
	_ = json.Unmarshal(f.Call.Args, &args)
	var res any = map[string]any{}
	switch f.Call.Method {
	case "getActiveTools":
		h.mu.Lock()
		cp := append([]string(nil), h.activeTools...)
		h.mu.Unlock()
		res = map[string]any{"tools": cp}
	case "setActiveTools":
		if list, ok := args["tools"].([]any); ok {
			next := make([]string, 0, len(list))
			for _, item := range list {
				if s, ok := item.(string); ok {
					next = append(next, s)
				}
			}
			h.mu.Lock()
			h.activeTools = next
			h.mu.Unlock()
		}
	case "getAllTools":
		h.mu.Lock()
		all := make([]map[string]any, 0, len(h.toolParams))
		for name, params := range h.toolParams {
			all = append(all, map[string]any{"name": name, "parameters": params, "source": "extension"})
		}
		h.mu.Unlock()
		res = map[string]any{"tools": all}
	case "registerTool":
		if name, ok := args["name"].(string); ok {
			raw, _ := json.Marshal(args["parameters"])
			h.mu.Lock()
			h.toolParams[name] = raw
			h.mu.Unlock()
		}
	case "configHome":
		res = map[string]any{"path": h.configHome}
	case "sessionRead":
		m, _ := args["method"].(string)
		switch m {
		case "getSessionId":
			res = h.sessionID
		default:
			res = nil
		}
	case "getSettings":
		res = map[string]any{"settings": map[string]any{}}
	}
	raw, _ := json.Marshal(res)
	h.writeFrame(map[string]any{
		"type":        "call_result",
		"id":          f.ID,
		"call_result": map[string]any{"result": json.RawMessage(raw)},
	})
}

func (h *autolearnHost) emitEvent(event string, data map[string]any) []json.RawMessage {
	h.t.Helper()
	if data == nil {
		data = map[string]any{}
	}
	raw, _ := json.Marshal(data)
	var results []json.RawMessage
	for _, hid := range h.handlers[event] {
		h.mu.Lock()
		h.nextID++
		id := "req-" + time.Now().Format("150405.000000") + "-" + string(rune('a'+h.nextID%26))
		ch := make(chan json.RawMessage, 1)
		h.pending[id] = ch
		h.mu.Unlock()
		h.writeFrame(map[string]any{
			"type": "request",
			"id":   id,
			"request": map[string]any{
				"method":     "event",
				"event":      event,
				"handler_id": hid,
				"args":       json.RawMessage(raw),
			},
		})
		results = append(results, <-ch)
	}
	return results
}

func (h *autolearnHost) readFrame() wireFrame {
	f, ok := h.readMaybe()
	if !ok {
		h.t.Fatalf("unexpected EOF")
	}
	return f
}

func (h *autolearnHost) readMaybe() (wireFrame, bool) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		return wireFrame{}, false
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return wireFrame{}, false
	}
	var f wireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		return wireFrame{}, false
	}
	return f, true
}

func (h *autolearnHost) writeFrame(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(raw)
}

func TestManageSkillHostActivationIntentAndResourceDiscovery(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")
	t.Setenv("PIG_CODING_AGENT_DIR", "")

	origEnabled := Enabled
	origToolcmdEnabled := toolcmd.Enabled
	origIntentConfigured := intent.Configured
	origAgentDir := AgentDirOverride
	t.Cleanup(func() {
		Enabled = origEnabled
		toolcmd.Enabled = origToolcmdEnabled
		intent.Configured = origIntentConfigured
		AgentDirOverride = origAgentDir
		toolcmd.ResetForTests()
	})
	AgentDirOverride = nil

	runCase := func(t *testing.T, autolearnOn, commandsOn, intentTracing bool, mode string, hasUI bool) ([]string, map[string]any) {
		t.Helper()
		toolcmd.ResetForTests()
		Enabled = func(_ sdk.Context, key string, def bool) bool {
			if key == SettingEnabled {
				return autolearnOn
			}
			return def
		}
		toolcmd.Enabled = func(_ sdk.Context, key string, def bool) bool {
			if key == "tools.commands" {
				return commandsOn
			}
			return def
		}
		intent.Configured = func(_ sdk.Context) (bool, bool) {
			return intentTracing, true
		}

		ext := sdk.New("autolearn-host-test")
		// Register bash + read placeholders so toolcmd sees a normal session.
		ext.RegisterTool(sdk.ToolDefinition{Name: "read", Parameters: sdk.Schema{"type": "object"}, Execute: func(sdk.Context, map[string]any) (any, error) { return nil, nil }})
		ext.RegisterTool(sdk.ToolDefinition{Name: "bash", Parameters: sdk.Schema{"type": "object"}, Execute: func(sdk.Context, map[string]any) (any, error) { return nil, nil }})
		toolcmd.Register(ext)
		Register(ext)

		cfgHome := filepath.Join(t.TempDir(), ".pig")
		h := startAutolearnHost(t, ext, mode, hasUI, cfgHome)
		h.emitEvent(sdk.EventSessionStart, nil)
		h.emitEvent(sdk.EventBeforeAgentStart, nil)

		h.mu.Lock()
		active := append([]string(nil), h.activeTools...)
		rawParams := h.toolParams["manage_skill"]
		h.mu.Unlock()

		var parsed map[string]any
		_ = json.Unmarshal(rawParams, &parsed)
		return active, parsed
	}

	t.Run("default off keeps manage_skill inactive", func(t *testing.T) {
		active, _ := runCase(t, false, true, true, "tui", true)
		if slices.Contains(active, "manage_skill") {
			t.Fatalf("manage_skill should be inactive when autolearn.enabled=false, got %v", active)
		}
	})

	t.Run("autolearn.enabled=true activates manage_skill with tools.commands=true", func(t *testing.T) {
		active, params := runCase(t, true, true, true, "tui", true)
		if !slices.Contains(active, "manage_skill") {
			t.Fatalf("manage_skill must activate when autolearn.enabled=true and tools.commands=true, got %v", active)
		}
		props, _ := params["properties"].(map[string]any)
		if _, hasI := props[intent.Field]; !hasI {
			t.Fatalf("expected intent field %q in TUI schema with intentTracing=true, got %v", intent.Field, props)
		}
	})

	t.Run("autolearn.enabled=true activates manage_skill with tools.commands=false", func(t *testing.T) {
		active, _ := runCase(t, true, false, true, "tui", true)
		if !slices.Contains(active, "manage_skill") {
			t.Fatalf("manage_skill must activate when autolearn.enabled=true and tools.commands=false, got %v", active)
		}
	})

	t.Run("tools.intentTracing=false strips intent field from manage_skill schema", func(t *testing.T) {
		active, params := runCase(t, true, true, false, "tui", true)
		if !slices.Contains(active, "manage_skill") {
			t.Fatalf("manage_skill should be active, got %v", active)
		}
		props, _ := params["properties"].(map[string]any)
		if _, hasI := props[intent.Field]; hasI {
			t.Fatalf("intentTracing=false must strip %q from manage_skill wire schema, got %v", intent.Field, props)
		}
	})

	t.Run("resources_discover returns managed-skills dir when it exists and omits when absent", func(t *testing.T) {
		toolcmd.ResetForTests()
		ext := sdk.New("autolearn-discover-test")
		Register(ext)
		cfgHome := filepath.Join(t.TempDir(), ".pig")
		t.Setenv("PIG_HOME", cfgHome)
		h := startAutolearnHost(t, ext, "tui", true, cfgHome)
		// Absent managed-skills dir returns nil/empty.
		results := h.emitEvent(sdk.EventResourcesDiscover, nil)
		for _, r := range results {
			var m struct {
				SkillPaths []string `json:"skillPaths"`
			}
			_ = json.Unmarshal(r, &m)
			if len(m.SkillPaths) > 0 {
				t.Fatalf("expected no skillPaths when managed-skills dir absent, got %v", m.SkillPaths)
			}
		}
		// Create managed skill via WriteManagedSkill under cfgHome/agent/managed-skills.
		agentDir := filepath.Join(cfgHome, "agent")
		if _, err := WriteManagedSkill(WriteManagedSkillInput{
			Action:      "create",
			Name:        "discovered",
			Description: "A discovered managed skill.",
			Body:        "# Body",
			AgentDir:    agentDir,
		}); err != nil {
			t.Fatalf("WriteManagedSkill: %v", err)
		}

		results = h.emitEvent(sdk.EventResourcesDiscover, nil)
		wantDir := ManagedSkillsDir(agentDir)
		found := false
		for _, r := range results {
			var m struct {
				SkillPaths []string `json:"skillPaths"`
			}
			_ = json.Unmarshal(r, &m)
			if slices.Contains(m.SkillPaths, wantDir) {
				found = true
			}
		}
		if !found {
			t.Fatalf("resources_discover did not return %q; results=%s", wantDir, results)
		}

		// Also verify PIG_CODING_AGENT_DIR overrides ConfigHome.
		overrideDir := filepath.Join(t.TempDir(), "custom-agent")
		if err := os.MkdirAll(ManagedSkillsDir(overrideDir), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIG_CODING_AGENT_DIR", overrideDir)
		results = h.emitEvent(sdk.EventResourcesDiscover, nil)
		wantOverride := ManagedSkillsDir(overrideDir)
		found = false
		for _, r := range results {
			var m struct {
				SkillPaths []string `json:"skillPaths"`
			}
			_ = json.Unmarshal(r, &m)
			if slices.Contains(m.SkillPaths, wantOverride) {
				found = true
			}
		}
		if !found {
			t.Fatalf("resources_discover did not honor PIG_CODING_AGENT_DIR %q; results=%s", wantOverride, results)
		}
	})
}
