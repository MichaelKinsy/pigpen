package surface

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

type rpHostCall struct {
	Method string
	Args   map[string]any
}

type rpWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
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

type rpFakeHost struct {
	t        *testing.T
	nc       net.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []rpHostCall
	pending  map[string]chan json.RawMessage
	failures map[string]string
	nextID   int
	onCall   func(method string, args map[string]any) (any, string)
	runDone  chan error
}

func startRPFakeHost(t *testing.T, ext *sdk.Extension, initialModel string, onCall func(method string, args map[string]any) (any, string)) *rpFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &rpFakeHost{
		t:        t,
		nc:       hostSide,
		pending:  make(map[string]chan json.RawMessage),
		failures: make(map[string]string),
		onCall:   onCall,
		runDone:  make(chan error, 1),
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.readFrame()
	for first.Type == "call" && first.Call != nil {
		h.answerCall(first)
		first = h.readFrame()
	}
	if first.Type != "register" {
		t.Fatalf("expected register frame, got %+v", first)
	}

	stateObj := map[string]any{"hasUI": true}
	stateRaw, _ := json.Marshal(stateObj)
	readyObj := map[string]any{
		"session_name": "",
		"cwd":          t.TempDir(),
		"mode":         "tui",
		"width":        80,
		"state":        json.RawMessage(stateRaw),
	}
	if initialModel != "" {
		readyObj["model"] = initialModel
	}
	h.writeFrame(map[string]any{
		"type":  "ready",
		"ready": readyObj,
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

func (h *rpFakeHost) readFrame() rpWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	var f rpWireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *rpFakeHost) writeFrame(v any) {
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

func (h *rpFakeHost) answerCall(f rpWireFrame) {
	args := map[string]any{}
	_ = json.Unmarshal(f.Call.Args, &args)
	h.mu.Lock()
	h.calls = append(h.calls, rpHostCall{Method: f.Call.Method, Args: args})
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

func (h *rpFakeHost) serveLoop() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, buf); err != nil {
			return
		}
		var f rpWireFrame
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

func (h *rpFakeHost) invokeCommand(name, args string) string {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := "cmd-" + string(rune('a'+h.nextID))
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	argv, _ := json.Marshal(args)
	h.writeFrame(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method": "command",
			"tool":   name,
			"args":   json.RawMessage(argv),
		},
	})
	select {
	case <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		return errMsg
	case <-time.After(5 * time.Second):
		h.t.Fatalf("command %q timed out", name)
		return ""
	}
}

func (h *rpFakeHost) findCalls(method string) []rpHostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []rpHostCall
	for _, c := range h.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestApplyRuntimeConfig_AppliesMaxTurnsThinkingDisabledToolsAndModelGuard(t *testing.T) {
	explicitThinkingFlag.Store(false)
	t.Cleanup(func() { explicitThinkingFlag.Store(false) })
	home := t.TempDir()
	p := paths.ProfilePaths{Home: home, ProfileName: "default", MemoriesDir: home}
	cfg := config.Default()
	cfg.Model = "claude-sonnet-4"
	cfg.Provider = "anthropic"
	cfg.DisabledTools = []string{"browser_exec", "web_extract"}
	cfg.Raw["agent.max_turns"] = "12"
	cfg.Raw["agent.reasoning_effort"] = "high"

	st := newState(p, cfg, memory.NewStore(home, cfg.MemoryLimit, cfg.UserLimit))

	ext := sdk.New("test-runtime-parity")
	ext.RegisterCommand("apply", sdk.CommandOptions{
		Handler: func(ctx sdk.Context, _ string) error {
			ApplyRuntimeConfig(ctx, st)
			return nil
		},
	})

	h := startRPFakeHost(t, ext, "", func(method string, args map[string]any) (any, string) {
		switch method {
		case "getAllTools":
			return map[string]any{
				"tools": []map[string]any{
					{"name": "read_file"},
					{"name": "browser_exec"},
					{"name": "web_extract"},
					{"name": "terminal"},
				},
			}, ""
		case "getActiveTools":
			return map[string]any{
				"tools": []string{"read_file", "browser_exec", "web_extract", "terminal"},
			}, ""
		case "getModelInfo":
			return map[string]any{}, ""
		case "resolveModel":
			return map[string]any{
				"model": map[string]any{"id": "claude-sonnet-4", "provider": "anthropic"},
			}, ""
		case "setModel":
			return map[string]any{"success": true}, ""
		default:
			return map[string]any{}, ""
		}
	})

	if errMsg := h.invokeCommand("apply", ""); errMsg != "" {
		t.Fatalf("apply command failed: %s", errMsg)
	}

	// 1. max_turns applied.
	st.mu.RLock()
	gotMaxTurns := st.maxTurns
	st.mu.RUnlock()
	if gotMaxTurns != 12 {
		t.Fatalf("expected maxTurns=12, got %d", gotMaxTurns)
	}

	// 2. reasoning_effort -> setThinkingLevel("high").
	thinkCalls := h.findCalls("setThinkingLevel")
	if len(thinkCalls) != 1 || thinkCalls[0].Args["level"] != "high" {
		t.Fatalf("expected setThinkingLevel(high), got %+v", thinkCalls)
	}

	// 3. DisabledTools filtered via setActiveTools.
	activeCalls := h.findCalls("setActiveTools")
	if len(activeCalls) != 1 {
		t.Fatalf("expected 1 setActiveTools call, got %+v", activeCalls)
	}
	rawTools, _ := activeCalls[0].Args["tools"].([]any)
	gotTools := make([]string, 0, len(rawTools))
	for _, x := range rawTools {
		gotTools = append(gotTools, x.(string))
	}
	wantTools := []string{"read_file", "terminal"}
	if !reflect.DeepEqual(gotTools, wantTools) {
		t.Fatalf("expected active tools %v, got %v", wantTools, gotTools)
	}

	// 4. Model set when host had no model.
	resolveCalls := h.findCalls("resolveModel")
	if len(resolveCalls) != 1 || resolveCalls[0].Args["spec"] != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected resolveModel(anthropic/claude-sonnet-4), got %+v", resolveCalls)
	}
	setModelCalls := h.findCalls("setModel")
	if len(setModelCalls) != 1 {
		t.Fatalf("expected setModel to be called once, got %+v", setModelCalls)
	}

	// 5. Model must NOT be overridden when host already has an active model (e.g. --model flag),
	//    and maxTurns must NOT be overridden when already set.
	st2 := newState(p, cfg, memory.NewStore(home, cfg.MemoryLimit, cfg.UserLimit))
	st2.maxTurns = 5
	ext2 := sdk.New("test-runtime-parity-guard")
	ext2.RegisterCommand("apply", sdk.CommandOptions{
		Handler: func(ctx sdk.Context, _ string) error {
			ApplyRuntimeConfig(ctx, st2)
			return nil
		},
	})
	h2 := startRPFakeHost(t, ext2, "gpt-4.1", func(method string, _ map[string]any) (any, string) {
		switch method {
		case "getAllTools":
			return map[string]any{"tools": []map[string]any{{"name": "read_file"}}}, ""
		case "getActiveTools":
			return map[string]any{"tools": []string{"read_file"}}, ""
		case "getModel", "getModelInfo":
			return map[string]any{"id": "gpt-4.1", "provider": "openai"}, ""
		default:
			return map[string]any{}, ""
		}
	})
	if errMsg := h2.invokeCommand("apply", ""); errMsg != "" {
		t.Fatalf("apply guard command failed: %s", errMsg)
	}
	if st2.maxTurns != 5 {
		t.Fatalf("expected CLI maxTurns=5 preserved, got %d", st2.maxTurns)
	}
	if len(h2.findCalls("resolveModel")) != 0 || len(h2.findCalls("setModel")) != 0 {
		t.Fatalf("expected model NOT overridden when already set, got resolve=%v set=%v",
			h2.findCalls("resolveModel"), h2.findCalls("setModel"))
	}
	// Re-applying on the same state must NOT call setThinkingLevel a second time.
	if errMsg := h2.invokeCommand("apply", ""); errMsg != "" {
		t.Fatalf("second apply command failed: %s", errMsg)
	}
	if len(h2.findCalls("setThinkingLevel")) != 1 {
		t.Fatalf("expected setThinkingLevel called only once per state, got %+v", h2.findCalls("setThinkingLevel"))
	}

	// 6. Explicit --thinking / --reasoning CLI flag suppresses config reasoning_effort.
	explicitThinkingFlag.Store(true)
	st3 := newState(p, cfg, memory.NewStore(home, cfg.MemoryLimit, cfg.UserLimit))
	ext3 := sdk.New("test-runtime-parity-thinking-guard")
	ext3.RegisterCommand("apply", sdk.CommandOptions{
		Handler: func(ctx sdk.Context, _ string) error {
			ApplyRuntimeConfig(ctx, st3)
			return nil
		},
	})
	h3 := startRPFakeHost(t, ext3, "gpt-4.1", nil)
	if errMsg := h3.invokeCommand("apply", ""); errMsg != "" {
		t.Fatalf("apply thinking guard command failed: %s", errMsg)
	}
	if len(h3.findCalls("setThinkingLevel")) != 0 {
		t.Fatalf("expected setThinkingLevel NOT called when explicit CLI thinking flag set, got %+v", h3.findCalls("setThinkingLevel"))
	}
}

func TestRunCompressCommand_CallsCompactWithCustomInstructions(t *testing.T) {
	home := t.TempDir()
	p := paths.ProfilePaths{Home: home, ProfileName: "default", MemoriesDir: home}
	cfg := config.Default()
	st := newState(p, cfg, memory.NewStore(home, cfg.MemoryLimit, cfg.UserLimit))

	ext := sdk.New("test-compress")
	ext.RegisterCommand("compress", sdk.CommandOptions{
		Handler: func(ctx sdk.Context, args string) error {
			return RunCompressCommand(ctx, st, args)
		},
	})

	h := startRPFakeHost(t, ext, "", func(method string, _ map[string]any) (any, string) {
		if method == "getContextUsage" {
			return map[string]any{"tokens": 4200, "contextWindow": 100000, "percent": 4.2}, ""
		}
		return map[string]any{}, ""
	})

	if errMsg := h.invokeCommand("compress", "keep architecture decisions"); errMsg != "" {
		t.Fatalf("compress failed: %s", errMsg)
	}

	compactCalls := h.findCalls("compact")
	if len(compactCalls) != 1 {
		t.Fatalf("expected 1 compact call, got %+v", compactCalls)
	}
	if compactCalls[0].Args["customInstructions"] != "keep architecture decisions" {
		t.Fatalf("expected customInstructions forwarded, got %+v", compactCalls[0].Args)
	}
	notifyCalls := h.findCalls("ui.notify")
	if len(notifyCalls) == 0 {
		t.Fatalf("expected ui.notify call after compact")
	}
}

func TestRunToolsCommand_ListEnableDisableLive(t *testing.T) {
	home := t.TempDir()
	p := paths.ProfilePaths{Home: home, ProfileName: "default", MemoriesDir: home}
	cfg := config.Default()
	st := newState(p, cfg, memory.NewStore(home, cfg.MemoryLimit, cfg.UserLimit))

	var mu sync.Mutex
	active := []string{"read_file", "terminal"}
	var messages []string

	ext := sdk.New("test-tools")
	ext.RegisterCommand("tools", sdk.CommandOptions{
		Handler: func(ctx sdk.Context, args string) error {
			return RunToolsCommand(ctx, st, args)
		},
	})

	h := startRPFakeHost(t, ext, "", func(method string, args map[string]any) (any, string) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "getAllTools":
			return map[string]any{
				"tools": []map[string]any{
					{"name": "read_file"},
					{"name": "terminal"},
					{"name": "web_search"},
					{"name": "browser_exec"},
				},
			}, ""
		case "getActiveTools":
			cp := append([]string(nil), active...)
			return map[string]any{"tools": cp}, ""
		case "setActiveTools":
			raw, _ := args["tools"].([]any)
			next := make([]string, 0, len(raw))
			for _, x := range raw {
				next = append(next, x.(string))
			}
			active = next
			return map[string]any{}, ""
		case "sendMessage":
			if msg, ok := args["message"].(map[string]any); ok {
				if content, ok := msg["content"].(string); ok {
					messages = append(messages, content)
				}
			} else if content, ok := args["content"].(string); ok {
				messages = append(messages, content)
			}
			return map[string]any{}, ""
		default:
			return map[string]any{}, ""
		}
	})

	// 1. /tools (list)
	if errMsg := h.invokeCommand("tools", ""); errMsg != "" {
		t.Fatalf("/tools failed: %s", errMsg)
	}
	// 2. /tools enable web_search
	if errMsg := h.invokeCommand("tools", "enable web_search"); errMsg != "" {
		t.Fatalf("/tools enable failed: %s", errMsg)
	}
	mu.Lock()
	afterEnable := append([]string(nil), active...)
	mu.Unlock()
	if !reflect.DeepEqual(afterEnable, []string{"read_file", "terminal", "web_search"}) {
		t.Fatalf("expected web_search enabled, got %v", afterEnable)
	}

	// 3. /tools disable terminal
	if errMsg := h.invokeCommand("tools", "disable terminal"); errMsg != "" {
		t.Fatalf("/tools disable failed: %s", errMsg)
	}
	mu.Lock()
	afterDisable := append([]string(nil), active...)
	listMsg := messages[0]
	mu.Unlock()
	if !reflect.DeepEqual(afterDisable, []string{"read_file", "web_search"}) {
		t.Fatalf("expected terminal disabled, got %v", afterDisable)
	}
	if !strings.Contains(listMsg, "Active:") || !strings.Contains(listMsg, "Disabled:") || !strings.Contains(listMsg, "browser_exec") {
		t.Fatalf("unexpected /tools list output:\n%s", listMsg)
	}
}
