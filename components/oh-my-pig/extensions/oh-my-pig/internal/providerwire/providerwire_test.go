package providerwire

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// --- Wire fakeHost harness (net.Pipe + length-prefixed JSON framing) ---

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
	} `json:"register,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
}

type fakeHost struct {
	t         *testing.T
	conn      net.Conn
	done      chan error
	handlers  map[string]int
	commands  map[string]bool
	sessionID string
	model     map[string]any
	models    []map[string]any
	providers map[string]map[string]any
	notifies  []string
	setModels []map[string]any
	seq       int
}

func (h *fakeHost) readFrame() wireFrame {
	h.t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(h.conn, hdr[:]); err != nil {
		h.t.Fatalf("read frame header: %v", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.conn, buf); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var f wireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *fakeHost) writeFrame(v any) {
	h.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal frame: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	_, _ = h.conn.Write(hdr[:])
	_, _ = h.conn.Write(raw)
}

func startFakeHost(t *testing.T, ext *sdk.Extension, sessionID string, model map[string]any, models []map[string]any) (*fakeHost, func()) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()

	providers := map[string]map[string]any{}
	for _, m := range models {
		if p, _ := m["provider"].(string); p != "" {
			providers[p] = map[string]any{
				"name":       p,
				"configured": true,
			}
		}
	}

	h := &fakeHost{
		t:         t,
		conn:      hostSide,
		done:      done,
		handlers:  map[string]int{},
		commands:  map[string]bool{},
		sessionID: sessionID,
		model:     model,
		models:    models,
		providers: providers,
	}

	reg := h.readFrame()
	if reg.Type != "register" || reg.Register == nil {
		t.Fatalf("expected register frame, got %+v", reg)
	}
	for _, hnd := range reg.Register.Handlers {
		h.handlers[hnd.Event] = hnd.HandlerID
	}
	for _, cmd := range reg.Register.Commands {
		h.commands[cmd.Name] = true
	}

	h.writeFrame(map[string]any{
		"type":  "ready",
		"ready": map[string]any{"cwd": t.TempDir(), "width": 120},
	})

	cleanup := func() {
		h.writeFrame(map[string]any{"type": "shutdown"})
		_ = hostSide.Close()
		<-done
	}
	return h, cleanup
}

func (h *fakeHost) handleHostCall(env wireFrame) {
	var res any
	switch env.Call.Method {
	case "getSessionID":
		res = map[string]any{"sessionId": h.sessionID}
	case "getModelInfo":
		res = h.model
	case "getModel":
		var a struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		_ = json.Unmarshal(env.Call.Args, &a)
		for _, m := range h.models {
			if m["provider"] == a.Provider && m["id"] == a.ModelID {
				res = m
				break
			}
		}
	case "getModelRegistryState":
		res = map[string]any{
			"models":    h.models,
			"providers": h.providers,
		}
	case "setModel":
		var a map[string]any
		_ = json.Unmarshal(env.Call.Args, &a)
		h.setModels = append(h.setModels, a)
		if m, ok := a["model"].(map[string]any); ok {
			h.model = m
		}
		res = map[string]any{"ok": true}
	case "ui.notify":
		var a map[string]any
		_ = json.Unmarshal(env.Call.Args, &a)
		if msg, _ := a["message"].(string); msg != "" {
			h.notifies = append(h.notifies, msg)
		}
		res = map[string]any{"ok": true}
	default:
		res = map[string]any{}
	}
	h.writeFrame(map[string]any{
		"type":        "call_result",
		"id":          env.ID,
		"call_result": map[string]any{"result": res},
	})
}

func (h *fakeHost) emitRaw(event string, args map[string]any) json.RawMessage {
	h.t.Helper()
	hid, ok := h.handlers[event]
	if !ok {
		h.t.Fatalf("no handler registered for %s", event)
	}
	h.seq++
	reqID := event + "-" + string(rune('0'+h.seq%10))
	rawArgs, _ := json.Marshal(args)
	h.writeFrame(map[string]any{
		"type": "request",
		"id":   reqID,
		"request": map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(rawArgs),
		},
	})
	for {
		env := h.readFrame()
		if env.Type == "call" && env.Call != nil {
			h.handleHostCall(env)
			continue
		}
		if env.Type == "response" && env.ID == reqID {
			if env.Response != nil && env.Response.Error != nil {
				h.t.Fatalf("event %s returned error: %s", event, env.Response.Error.Message)
			}
			if env.Response == nil {
				return nil
			}
			return env.Response.Result
		}
	}
}

func (h *fakeHost) emit(event string, args map[string]any) map[string]any {
	h.t.Helper()
	raw := h.emitRaw(event, args)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		h.t.Fatalf("unmarshal event %s response %s: %v", event, string(raw), err)
	}
	return out
}

func (h *fakeHost) runCommand(name, args string) {
	h.t.Helper()
	if !h.commands[name] {
		h.t.Fatalf("command %q not registered", name)
	}
	h.seq++
	reqID := "cmd-" + name + "-" + string(rune('0'+h.seq%10))
	rawArgs, _ := json.Marshal(args)
	h.writeFrame(map[string]any{
		"type": "request",
		"id":   reqID,
		"request": map[string]any{
			"method": "command",
			"tool":   name,
			"args":   json.RawMessage(rawArgs),
		},
	})
	for {
		env := h.readFrame()
		if env.Type == "call" && env.Call != nil {
			h.handleHostCall(env)
			continue
		}
		if env.Type == "response" && env.ID == reqID {
			if env.Response != nil && env.Response.Error != nil {
				h.t.Fatalf("command %s error: %s", name, env.Response.Error.Message)
			}
			return
		}
	}
}

func resetGlobals(t *testing.T) {
	t.Helper()
	prevSetting := Setting
	prevSetSetting := SetSetting
	t.Cleanup(func() {
		Setting = prevSetting
		SetSetting = prevSetSetting
	})
	Setting = nil
	SetSetting = nil
}

// --- Tests ---

func TestNoOpFastPathLeavesDefaultPayloadAndHeadersUntouched(t *testing.T) {
	resetGlobals(t)

	ext := sdk.New("pw-noop")
	mgr := newManager()
	registerWithManager(ext, mgr)

	anthropicModel := map[string]any{
		"id":       "claude-sonnet-4-5",
		"provider": "anthropic",
		"api":      "anthropic-messages",
	}
	h, cleanup := startFakeHost(t, ext, "sess-noop", anthropicModel, []map[string]any{anthropicModel})
	defer cleanup()

	// 1. Default before_provider_request must reply null (SDK omits reply when payload is unchanged).
	rawReq := h.emitRaw(sdk.EventBeforeProviderRequest, map[string]any{
		"type": "before_provider_request",
		"payload": map[string]any{
			"model":    "claude-sonnet-4-5",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"stream":   true,
		},
	})
	if len(rawReq) > 0 && string(rawReq) != "null" {
		t.Fatalf("default before_provider_request must be no-op (null), got %s", string(rawReq))
	}

	// 2. Default before_provider_headers must return headers verbatim with no added tokens.
	origHeaders := map[string]any{
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    "prompt-caching-2024-07-31",
	}
	gotHeaders := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type":    "before_provider_headers",
		"headers": origHeaders,
	})
	if !reflect.DeepEqual(gotHeaders, origHeaders) {
		t.Fatalf("default before_provider_headers altered headers:\ngot:  %#v\nwant: %#v", gotHeaders, origHeaders)
	}
}

func TestAnthropicFastSlowAndBetaHeaderMerging(t *testing.T) {
	resetGlobals(t)

	ext := sdk.New("pw-anthropic")
	mgr := newManager()
	registerWithManager(ext, mgr)

	anthropicModel := map[string]any{
		"id":       "claude-opus-4-6",
		"provider": "anthropic",
		"api":      "anthropic-messages",
	}
	h, cleanup := startFakeHost(t, ext, "sess-anth", anthropicModel, []map[string]any{anthropicModel})
	defer cleanup()

	// Enable /fast on.
	h.runCommand("fast", "on")

	// Payload gets speed: "fast".
	reqFast := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
		"type": "before_provider_request",
		"payload": map[string]any{
			"model":    "claude-opus-4-6",
			"messages": []any{},
		},
	})
	if reqFast["speed"] != "fast" {
		t.Fatalf("expected speed=fast on Anthropic fast mode, got %#v", reqFast)
	}

	// Headers merge fast-mode-2026-02-01 preserving existing CSV tokens and key casing, without duplicating.
	hdrs := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type": "before_provider_headers",
		"headers": map[string]any{
			"Anthropic-Beta": "prompt-caching-2024-07-31, interleaved-thinking-2025-05-14",
		},
	})
	wantBeta := "prompt-caching-2024-07-31,interleaved-thinking-2025-05-14," + FastModeBeta
	if hdrs["Anthropic-Beta"] != wantBeta {
		t.Fatalf("Anthropic-Beta = %q, want %q", hdrs["Anthropic-Beta"], wantBeta)
	}

	// Deduplication check when beta is already present.
	hdrsDedup := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type": "before_provider_headers",
		"headers": map[string]any{
			"anthropic-beta": FastModeBeta + ",effort-2025-11-24",
		},
	})
	if hdrsDedup["anthropic-beta"] != FastModeBeta+",effort-2025-11-24" {
		t.Fatalf("expected deduplicated anthropic-beta, got %q", hdrsDedup["anthropic-beta"])
	}

	// Switch to /fast off: removes speed if present in payload and stops injecting beta header.
	h.runCommand("fast", "off")
	reqOff := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
		"type": "before_provider_request",
		"payload": map[string]any{
			"model": "claude-opus-4-6",
			"speed": "fast",
		},
	})
	if _, exists := reqOff["speed"]; exists {
		t.Fatalf("expected speed to be removed when /fast off, got %#v", reqOff)
	}

	hdrsOff := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type":    "before_provider_headers",
		"headers": map[string]any{"anthropic-version": "2023-06-01"},
	})
	if _, hasBeta := hdrsOff["anthropic-beta"]; hasBeta {
		t.Fatalf("expected no anthropic-beta when /fast off, got %#v", hdrsOff)
	}

	// /slow on also removes speed on Anthropic.
	h.runCommand("slow", "on")
	reqSlow := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
		"type": "before_provider_request",
		"payload": map[string]any{
			"model": "claude-opus-4-6",
			"speed": "fast",
		},
	})
	if _, exists := reqSlow["speed"]; exists {
		t.Fatalf("expected speed removed on Anthropic /slow, got %#v", reqSlow)
	}
}

func TestOpenAIServiceTiersAcrossWireAPIs(t *testing.T) {
	resetGlobals(t)

	for _, api := range []string{
		"openai-completions",
		"openai-responses",
		"openai-codex-responses",
		"azure-openai-responses",
	} {
		t.Run(api, func(t *testing.T) {
			ext := sdk.New("pw-openai-" + api)
			mgr := newManager()
			registerWithManager(ext, mgr)

			model := map[string]any{
				"id":       "gpt-5.4",
				"provider": "openai",
				"api":      api,
			}
			h, cleanup := startFakeHost(t, ext, "sess-oai-"+api, model, []map[string]any{model})
			defer cleanup()

			// /fast on -> service_tier: "priority"
			h.runCommand("fast", "on")
			resFast := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
				"type":    "before_provider_request",
				"payload": map[string]any{"model": "gpt-5.4"},
			})
			if resFast["service_tier"] != TierPriority {
				t.Fatalf("%s /fast on: service_tier = %v, want priority", api, resFast["service_tier"])
			}

			// /fast ultra -> service_tier: "ultrafast"
			h.runCommand("fast", "ultra")
			resUltra := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
				"type":    "before_provider_request",
				"payload": map[string]any{"model": "gpt-5.4"},
			})
			if resUltra["service_tier"] != TierUltrafast {
				t.Fatalf("%s /fast ultra: service_tier = %v, want ultrafast", api, resUltra["service_tier"])
			}

			// /slow on -> service_tier: "flex"
			h.runCommand("slow", "on")
			resSlow := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
				"type":    "before_provider_request",
				"payload": map[string]any{"model": "gpt-5.4"},
			})
			if resSlow["service_tier"] != TierFlex {
				t.Fatalf("%s /slow on: service_tier = %v, want flex", api, resSlow["service_tier"])
			}

			// Non-Anthropic headers must NOT get Anthropic betas.
			hdrs := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
				"type":    "before_provider_headers",
				"headers": map[string]any{"Authorization": "Bearer test"},
			})
			if _, hasBeta := hdrs["anthropic-beta"]; hasBeta {
				t.Fatalf("%s should never get anthropic-beta header, got %#v", api, hdrs)
			}
		})
	}
}

func TestExtendedContextToggleAndBetaHeader(t *testing.T) {
	resetGlobals(t)

	persisted := map[string]any{}
	Setting = func(_ sdk.Context, key string) any {
		return persisted[key]
	}
	SetSetting = func(_ sdk.Context, key string, value any, _ bool) error {
		persisted[key] = value
		return nil
	}

	ext := sdk.New("pw-extctx")
	mgr := newManager()
	registerWithManager(ext, mgr)

	model := map[string]any{
		"id":       "claude-sonnet-4-6",
		"provider": "anthropic",
		"api":      "anthropic-messages",
	}
	h, cleanup := startFakeHost(t, ext, "sess-extctx", model, []map[string]any{model})
	defer cleanup()

	// Initially off: no beta header injected.
	hdrsInit := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type":    "before_provider_headers",
		"headers": map[string]any{"anthropic-version": "2023-06-01"},
	})
	if _, has := hdrsInit["anthropic-beta"]; has {
		t.Fatalf("expected no extended-context beta initially, got %#v", hdrsInit)
	}

	// Toggle on via /extended-context (bare toggle).
	h.runCommand("extended-context", "")
	if persisted["extendedContext"] != true {
		t.Fatalf("expected extendedContext setting updated to true, got %#v", persisted["extendedContext"])
	}
	hdrsOn := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type":    "before_provider_headers",
		"headers": map[string]any{"anthropic-beta": "effort-2025-11-24"},
	})
	if hdrsOn["anthropic-beta"] != "effort-2025-11-24,"+ExtendedContextBeta {
		t.Fatalf("anthropic-beta = %q, want effort-2025-11-24,%s", hdrsOn["anthropic-beta"], ExtendedContextBeta)
	}

	// Combine with /fast on -> payload["betas"] carries both betas without overwriting empty headers (preserving PiG OAuth betas).
	h.runCommand("fast", "on")
	reqBoth := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
		"type":    "before_provider_request",
		"payload": map[string]any{"model": "claude-sonnet-4-5", "betas": []any{"oauth-2025-04-20"}},
	})
	wantBetas := []any{"oauth-2025-04-20", FastModeBeta, ExtendedContextBeta}
	if !reflect.DeepEqual(reqBoth["betas"], wantBetas) {
		t.Fatalf("combined payload betas = %#v, want %#v", reqBoth["betas"], wantBetas)
	}
	hdrsEmpty := h.emit(sdk.EventBeforeProviderHeaders, map[string]any{
		"type":    "before_provider_headers",
		"headers": map[string]any{},
	})
	if _, has := hdrsEmpty["anthropic-beta"]; has {
		t.Fatalf("empty headers must not inject anthropic-beta (would clobber PiG OAuth betas), got %#v", hdrsEmpty)
	}

	// Turn extended-context off -> only fast beta remains in payload["betas"].
	h.runCommand("extended-context", "off")
	reqFastOnly := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
		"type":    "before_provider_request",
		"payload": map[string]any{"model": "claude-sonnet-4-5"},
	})
	if !reflect.DeepEqual(reqFastOnly["betas"], []any{FastModeBeta}) {
		t.Fatalf("fast-only payload betas = %#v, want [%s]", reqFastOnly["betas"], FastModeBeta)
	}
}

func TestForcedToolChoiceAllWireShapesAndAutoClear(t *testing.T) {
	resetGlobals(t)

	cases := []struct {
		name      string
		model     map[string]any
		tools     []any
		checkFunc func(t *testing.T, res map[string]any)
	}{
		{
			name:  "anthropic",
			model: map[string]any{"id": "claude-sonnet-4-5", "provider": "anthropic", "api": "anthropic-messages"},
			tools: []any{
				map[string]any{"name": "bash", "input_schema": map[string]any{"type": "object"}},
				map[string]any{"name": "read", "input_schema": map[string]any{"type": "object"}},
			},
			checkFunc: func(t *testing.T, res map[string]any) {
				want := map[string]any{"type": "tool", "name": "read"}
				if !reflect.DeepEqual(res["tool_choice"], want) {
					t.Fatalf("anthropic tool_choice = %#v, want %#v", res["tool_choice"], want)
				}
			},
		},
		{
			name:  "openai-completions",
			model: map[string]any{"id": "gpt-5.4", "provider": "openai", "api": "openai-completions"},
			tools: []any{
				map[string]any{"type": "function", "function": map[string]any{"name": "read", "parameters": map[string]any{}}},
			},
			checkFunc: func(t *testing.T, res map[string]any) {
				want := map[string]any{"type": "function", "function": map[string]any{"name": "read"}}
				if !reflect.DeepEqual(res["tool_choice"], want) {
					t.Fatalf("openai-completions tool_choice = %#v, want %#v", res["tool_choice"], want)
				}
			},
		},
		{
			name:  "openai-responses",
			model: map[string]any{"id": "gpt-5.4", "provider": "openai", "api": "openai-responses"},
			tools: []any{
				map[string]any{"type": "function", "name": "read", "parameters": map[string]any{}},
			},
			checkFunc: func(t *testing.T, res map[string]any) {
				want := map[string]any{"type": "function", "name": "read"}
				if !reflect.DeepEqual(res["tool_choice"], want) {
					t.Fatalf("openai-responses tool_choice = %#v, want %#v", res["tool_choice"], want)
				}
			},
		},
		{
			name:  "google-gemini",
			model: map[string]any{"id": "gemini-3.5-pro", "provider": "google", "api": "google-generative-ai"},
			tools: []any{
				map[string]any{
					"functionDeclarations": []any{
						map[string]any{"name": "read", "description": "Read a file"},
					},
				},
			},
			checkFunc: func(t *testing.T, res map[string]any) {
				want := map[string]any{
					"functionCallingConfig": map[string]any{
						"mode":                 "ANY",
						"allowedFunctionNames": []any{"read"},
					},
				}
				if !reflect.DeepEqual(res["toolConfig"], want) {
					t.Fatalf("google toolConfig = %#v, want %#v", res["toolConfig"], want)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := sdk.New("pw-tool-" + tc.name)
			mgr := newManager()
			registerWithManager(ext, mgr)

			sid := "sess-tool-" + tc.name
			h, cleanup := startFakeHost(t, ext, sid, tc.model, []map[string]any{tc.model})
			defer cleanup()

			// 1. Arm forced tool_choice for "read".
			mgr.setForcedToolChoice(sid, "read")
			res := h.emit(sdk.EventBeforeProviderRequest, map[string]any{
				"type": "before_provider_request",
				"payload": map[string]any{
					"model": tc.model["id"],
					"tools": tc.tools,
				},
			})
			tc.checkFunc(t, res)

			// 2. Safety skip when forced tool is NOT in payload tools.
			mgr.setForcedToolChoice(sid, "nonexistent_tool")
			rawSkipped := h.emitRaw(sdk.EventBeforeProviderRequest, map[string]any{
				"type": "before_provider_request",
				"payload": map[string]any{
					"model": tc.model["id"],
					"tools": tc.tools,
				},
			})
			if len(rawSkipped) > 0 && string(rawSkipped) != "null" {
				t.Fatalf("expected no-op when forced tool absent from payload tools, got %s", string(rawSkipped))
			}

			// 3. Mid-stream turn_end before request runs preserves pending forced tool;
			// once applied on before_provider_request, turn_end clears it.
			mgr.setForcedToolChoice(sid, "read")
			_ = h.emit(sdk.EventTurnEnd, map[string]any{"type": "turn_end"})
			if got := mgr.getForcedTool(sid); got != "read" {
				t.Fatalf("expected unapplied forced tool to survive in-flight turn_end, got %q", got)
			}
			_ = h.emit(sdk.EventBeforeProviderRequest, map[string]any{
				"type": "before_provider_request",
				"payload": map[string]any{
					"model": tc.model["id"],
					"tools": tc.tools,
				},
			})
			_ = h.emit(sdk.EventTurnEnd, map[string]any{"type": "turn_end"})
			if got := mgr.getForcedTool(sid); got != "" {
				t.Fatalf("expected applied forced tool cleared on turn_end, still %q", got)
			}
			rawAfterTurn := h.emitRaw(sdk.EventBeforeProviderRequest, map[string]any{
				"type": "before_provider_request",
				"payload": map[string]any{
					"model": tc.model["id"],
					"tools": tc.tools,
				},
			})
			if len(rawAfterTurn) > 0 && string(rawAfterTurn) != "null" {
				t.Fatalf("expected no-op after turn_end clear, got %s", string(rawAfterTurn))
			}

			// 4. Auto-clear on EventAgentEnd as well.
			mgr.setForcedToolChoice(sid, "read")
			_ = h.emit(sdk.EventAgentEnd, map[string]any{"type": "agent_end"})
			if got := mgr.getForcedTool(sid); got != "" {
				t.Fatalf("expected forced tool cleared on agent_end, still %q", got)
			}
		})
	}
}

func TestResolveRoleAndRoleCommand(t *testing.T) {
	resetGlobals(t)

	Setting = func(_ sdk.Context, key string) any {
		if key == "modelRoles" {
			return map[string]any{
				"default": "anthropic/claude-sonnet-4-5",
				"plan":    "anthropic/claude-opus-4-8:high",
				"commit":  "@smol",
				"task":    "openai/gpt-5.4-mini",
			}
		}
		return nil
	}

	models := []map[string]any{
		{"id": "claude-sonnet-4-5", "provider": "anthropic", "api": "anthropic-messages"},
		{"id": "claude-opus-4-8", "provider": "anthropic", "api": "anthropic-messages"},
		{"id": "claude-haiku-4-5", "provider": "anthropic", "api": "anthropic-messages"},
		{"id": "gpt-5.4-mini", "provider": "openai", "api": "openai-responses"},
	}

	ext := sdk.New("pw-roles")
	mgr := newManager()
	registerWithManager(ext, mgr)

	// Register a test command to exercise ResolveRole directly with a live sdk.Context.
	type roleCheck struct {
		input        string
		wantProvider string
		wantID       string
		wantThinking string
		wantOK       bool
	}
	checks := []roleCheck{
		{"plan", "anthropic", "claude-opus-4-8", "high", true},
		{"@plan", "anthropic", "claude-opus-4-8", "high", true},
		{"@task", "openai", "gpt-5.4-mini", "", true},
		{"commit", "anthropic", "claude-sonnet-4-5", "", true}, // @smol inherits default before priority
		{"smol", "anthropic", "claude-sonnet-4-5", "", true},
		{"@default:medium", "anthropic", "claude-sonnet-4-5", "medium", true},
		{"*", "anthropic", "claude-sonnet-4-5", "", true},
		{"pi/plan", "anthropic", "claude-opus-4-8", "high", true},
		{"@tiny", "anthropic", "claude-sonnet-4-5", "", true}, // tiny falls back to smol
	}

	var checkErrs []string
	ext.RegisterCommand("test-resolve", sdk.CommandOptions{
		Description: "test helper",
		Handler: func(ctx sdk.Context, _ string) error {
			for _, c := range checks {
				p, id, th, ok := ResolveRoleWithThinking(ctx, c.input)
				if p != c.wantProvider || id != c.wantID || th != c.wantThinking || ok != c.wantOK {
					checkErrs = append(checkErrs,
						c.input+": got ("+p+","+id+","+th+") want ("+c.wantProvider+","+c.wantID+","+c.wantThinking+")")
				}
			}
			// Now test built-in smol priority chain when modelRoles.default is unset.
			prev := Setting
			Setting = func(_ sdk.Context, _ string) any { return nil }
			p, id, ok := ResolveRole(ctx, "smol")
			Setting = prev
			if !ok || p != "anthropic" || id != "claude-haiku-4-5" {
				checkErrs = append(checkErrs, "unconfigured smol fallback: got ("+p+","+id+") want (anthropic,claude-haiku-4-5)")
			}

			// Fallback to active session model for an unconfigured chat role like plan.
			Setting = func(_ sdk.Context, _ string) any { return nil }
			pPlan, idPlan, okPlan := ResolveRole(ctx, "@plan")
			Setting = prev
			if !okPlan || pPlan != "anthropic" || idPlan != "claude-sonnet-4-5" {
				checkErrs = append(checkErrs, "unconfigured @plan active fallback: got ("+pPlan+","+idPlan+")")
			}
			return nil
		},
	})

	h, cleanup := startFakeHost(t, ext, "sess-roles", models[0], models)
	defer cleanup()

	h.runCommand("test-resolve", "")
	if len(checkErrs) > 0 {
		t.Fatalf("ResolveRole mismatches:\n%s", strings.Join(checkErrs, "\n"))
	}

	// Test `/role @plan` switches the model via SetModel.
	h.runCommand("role", "@plan")
	if len(h.setModels) == 0 {
		t.Fatalf("expected /role @plan to call setModel")
	}
	if h.model["id"] != "claude-opus-4-8" {
		t.Fatalf("active model after /role @plan = %v, want claude-opus-4-8", h.model["id"])
	}

	// Test `/role list` emits a notification with resolved roles.
	h.runCommand("role", "list")
	if len(h.notifies) == 0 || !strings.Contains(h.notifies[len(h.notifies)-1], "@plan -> anthropic/claude-opus-4-8") {
		t.Fatalf("expected /role list notification, got %#v", h.notifies)
	}
}

func TestParseRoleAliasAndCycleSafety(t *testing.T) {
	role, th, ok := ParseRoleAlias("@smol:low")
	if !ok || role != "smol" || th != "low" {
		t.Fatalf("ParseRoleAlias(@smol:low) = (%q,%q,%v)", role, th, ok)
	}
	role, th, ok = ParseRoleAlias("*:xhigh")
	if !ok || role != "default" || th != "xhigh" {
		t.Fatalf("ParseRoleAlias(*:xhigh) = (%q,%q,%v)", role, th, ok)
	}
	role, th, ok = ParseRoleAlias("pi/slow")
	if !ok || role != "slow" || th != "" {
		t.Fatalf("ParseRoleAlias(pi/slow) = (%q,%q,%v)", role, th, ok)
	}
	if _, _, ok := ParseRoleAlias("anthropic/claude-opus-4-8"); ok {
		t.Fatalf("provider/model should not parse as role alias")
	}

	// Cycle safety: smol -> @tiny -> @smol must terminate and fall back to built-in smol defaults.
	cyclic := map[string]string{
		"smol": "@tiny",
		"tiny": "@smol",
	}
	patterns := expandRolePatterns("smol", cyclic, nil)
	if len(patterns) == 0 {
		t.Fatalf("expected cyclic smol expansion to fall back to built-in smol defaults")
	}
}
