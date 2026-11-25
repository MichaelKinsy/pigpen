// The port's own fake PiG host for the module-root tests. It is NOT a copy of
// the porting Skill's fakehost_test.go template (that name is reserved for
// unchanged copies): this harness adds what the assembled extension needs
// (session ids, project trust, scripted ui.select answers).

package ohmypig

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type fakeRegisteredTool struct {
	Name              string          `json:"name"`
	Label             string          `json:"label"`
	Description       string          `json:"description"`
	Parameters        json.RawMessage `json:"parameters"`
	Exposure          string          `json:"exposure,omitempty"`
	DefaultActive     *bool           `json:"default_active,omitempty"`
	PreparesArguments bool            `json:"prepares_arguments"`
	PreparesLoadout   bool            `json:"prepares_loadout"`
}

type fakeWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Tools    []fakeRegisteredTool `json:"tools"`
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
	Notify *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"notify,omitempty"`
}

type fakeHostOptions struct {
	Cwd            string
	SessionID      string
	Mode           string
	HasUI          bool
	FakeHome       string
	ProjectTrusted bool
	// SelectAnswer optionally answers host ui.select dialogs (e.g. bash approval prompt).
	// If nil, ui.select returns ok=false (dismissed/denied).
	SelectAnswer func(title string, options []string) (string, bool)
}

type fakeHostCall struct {
	Method string
	Args   map[string]any
}

type fakeHost struct {
	t                  *testing.T
	nc                 net.Conn
	cwd                string
	sessionID          string
	sessionFile        string
	configHome         string
	mode               string
	hasUI              bool
	projectTrusted     bool
	selectAnswer       func(title string, options []string) (string, bool)
	runDone            chan error
	writeMu            sync.Mutex
	loadoutMu          sync.Mutex
	mu                 sync.Mutex
	tools              map[string]fakeRegisteredTool
	toolOrder          []string
	activeTools        []string
	hiddenOrder        []string
	hiddenSet          map[string]bool
	loadoutDescs       map[string]string
	handlers           map[string][]int
	pending            map[string]chan fakeWireFrame
	calls              []string
	callLog            []fakeHostCall
	setActiveToolCalls [][]string
	nextID             int
	stopped            bool
}

func startFakeHost(t *testing.T, ext *sdk.Extension, opts fakeHostOptions) *fakeHost {
	t.Helper()
	fakeHome := opts.FakeHome
	if fakeHome == "" {
		fakeHome = t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("USERPROFILE", fakeHome)
		t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
		t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd = t.TempDir()
	}
	sid := opts.SessionID
	if sid == "" {
		sid = "ext-test-sess"
	}
	mode := opts.Mode
	if mode == "" {
		mode = "tui"
	}
	sessionFile := filepath.Join(t.TempDir(), sid+".jsonl")

	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:              t,
		nc:             hostSide,
		cwd:            cwd,
		sessionID:      sid,
		sessionFile:    sessionFile,
		configHome:     filepath.Join(fakeHome, ".pig"),
		mode:           mode,
		hasUI:          opts.HasUI,
		projectTrusted: opts.ProjectTrusted,
		selectAnswer:   opts.SelectAnswer,
		runDone:        make(chan error, 1),
		tools:          map[string]fakeRegisteredTool{},
		hiddenSet:      map[string]bool{},
		loadoutDescs:   map[string]string{},
		handlers:       map[string][]int{},
		pending:        map[string]chan fakeWireFrame{},
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
	for _, tl := range first.Register.Tools {
		h.tools[tl.Name] = tl
		h.toolOrder = append(h.toolOrder, tl.Name)
		if fakeToolStartsActive(tl) {
			h.activeTools = append(h.activeTools, tl.Name)
		}
	}
	for _, hnd := range first.Register.Handlers {
		h.handlers[hnd.Event] = append(h.handlers[hnd.Event], hnd.HandlerID)
	}

	stateObj := map[string]any{
		"hasUI":       opts.HasUI,
		"sessionId":   sid,
		"sessionFile": sessionFile,
		"session":     map[string]any{"sessionId": sid, "sessionFile": sessionFile},
		"model":       map[string]any{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
	}
	stateRaw, _ := json.Marshal(stateObj)
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          cwd,
			"mode":         mode,
			"width":        80,
			"state":        json.RawMessage(stateRaw),
		},
	})
	go h.serve()
	h.applyLoadoutLocked(append([]string(nil), h.activeTools...))
	t.Cleanup(h.stop)
	return h
}

func fakeToolExposure(tl fakeRegisteredTool) string {
	if tl.Exposure == "" {
		return string(sdk.ToolExposureDirect)
	}
	return tl.Exposure
}

func fakeToolStartsActive(tl fakeRegisteredTool) bool {
	exp := fakeToolExposure(tl)
	if exp != string(sdk.ToolExposureDirect) && exp != string(sdk.ToolExposureModelOnly) {
		return false
	}
	return tl.DefaultActive == nil || *tl.DefaultActive
}

func fakeToolCallable(tl fakeRegisteredTool, isActive bool) bool {
	exp := fakeToolExposure(tl)
	return exp == string(sdk.ToolExposureCodemode) || exp == string(sdk.ToolExposureDeferred) || (exp == string(sdk.ToolExposureDirect) && isActive)
}

// applyLoadoutLocked mirrors PiG's Session.applyToolLoadout + planToolLoadout
// (coding/session_loadout.go:134-276):
//   - Preserves the current order of tools that stay active, appending newly
//     active ones in caller order.
//   - Invokes each active tool's `tool_prepare_loadout` hook (in active order)
//     and records merged `Descriptions` and `HiddenDeclarations` (deduped in
//     first-seen order).
func (h *fakeHost) applyLoadoutLocked(requested []string) {
	h.loadoutMu.Lock()
	defer h.loadoutMu.Unlock()

	h.mu.Lock()
	reqSet := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		reqSet[name] = struct{}{}
	}
	ordered := make([]string, 0, len(requested))
	for _, name := range h.activeTools {
		if _, ok := reqSet[name]; ok {
			ordered = append(ordered, name)
		}
	}
	ordered = append(ordered, requested...)

	seen := make(map[string]struct{}, len(ordered))
	nextActive := make([]string, 0, len(ordered))
	for _, name := range ordered {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		tl, exists := h.tools[name]
		if !exists || fakeToolExposure(tl) == string(sdk.ToolExposureHidden) {
			continue
		}
		nextActive = append(nextActive, name)
	}
	h.activeTools = nextActive

	activeSet := make(map[string]bool, len(nextActive))
	declared := make([]map[string]any, 0, len(nextActive))
	var hooks []string
	for _, name := range nextActive {
		activeSet[name] = true
		tl := h.tools[name]
		declared = append(declared, map[string]any{
			"name":        tl.Name,
			"label":       tl.Label,
			"description": tl.Description,
			"parameters":  tl.Parameters,
		})
		if tl.PreparesLoadout {
			hooks = append(hooks, tl.Name)
		}
	}
	callable := make([]map[string]any, 0, len(h.toolOrder))
	registered := make([]map[string]any, 0, len(h.toolOrder))
	exposures := make(map[string]string, len(h.toolOrder))
	for _, name := range h.toolOrder {
		tl := h.tools[name]
		view := map[string]any{
			"name":        tl.Name,
			"label":       tl.Label,
			"description": tl.Description,
			"parameters":  tl.Parameters,
		}
		registered = append(registered, view)
		exposures[name] = fakeToolExposure(tl)
		if fakeToolCallable(tl, activeSet[name]) {
			callable = append(callable, view)
		}
	}
	h.mu.Unlock()

	descs := map[string]string{}
	hiddenSet := map[string]bool{}
	hiddenOrder := []string{}
	if len(hooks) > 0 {
		payloadRaw, _ := json.Marshal(map[string]any{
			"declared":         declared,
			"callable":         callable,
			"registered":       registered,
			"exposures":        exposures,
			"namespaces":       map[string]any{},
			"promptGuidelines": map[string][]string{},
		})
		for _, hookTool := range hooks {
			resp := h.request(map[string]any{
				"method": "tool_prepare_loadout",
				"tool":   hookTool,
				"args":   json.RawMessage(payloadRaw),
			})
			if resp.Response == nil || resp.Response.Error != nil || len(resp.Response.Result) == 0 {
				continue
			}
			var changes struct {
				Descriptions       map[string]string `json:"descriptions"`
				HiddenDeclarations []string          `json:"hiddenDeclarations"`
			}
			if err := json.Unmarshal(resp.Response.Result, &changes); err != nil {
				continue
			}
			for k, v := range changes.Descriptions {
				descs[k] = v
			}
			for _, name := range changes.HiddenDeclarations {
				if !hiddenSet[name] {
					hiddenSet[name] = true
					hiddenOrder = append(hiddenOrder, name)
				}
			}
		}
	}

	h.mu.Lock()
	h.loadoutDescs = descs
	h.hiddenSet = hiddenSet
	h.hiddenOrder = hiddenOrder
	h.mu.Unlock()
}

// declaredToolsForModel returns the active tools after applying
// PrepareLoadout.HiddenDeclarations (matching PiG's hideDeclarations,
// coding/session_loadout.go:288-327).
func (h *fakeHost) declaredToolsForModel() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.activeTools))
	for _, name := range h.activeTools {
		if !h.hiddenSet[name] {
			out = append(out, name)
		}
	}
	return out
}

// hiddenToolsForPrompt returns the current HiddenDeclarations in order.
func (h *fakeHost) hiddenToolsForPrompt() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.hiddenOrder...)
}

// activeToolNames returns a copy of the host's live active tool list.
func (h *fakeHost) activeToolNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.activeTools...)
}

// resetCallLog clears recorded host calls and setActiveTools call history.
func (h *fakeHost) resetCallLog() {
	h.mu.Lock()
	h.calls = nil
	h.callLog = nil
	h.setActiveToolCalls = nil
	h.mu.Unlock()
}
func (h *fakeHost) stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.mu.Unlock()
	h.writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test-done"}})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(3 * time.Second):
	}
}

func (h *fakeHost) readFrame() fakeWireFrame {
	var n uint32
	if err := binary.Read(h.nc, binary.BigEndian, &n); err != nil {
		if err == io.EOF {
			return fakeWireFrame{}
		}
		h.t.Fatalf("read frame length: %v", err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var f fakeWireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *fakeHost) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("encode frame: %v", err)
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_ = binary.Write(h.nc, binary.BigEndian, uint32(len(data)))
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) serve() {
	for {
		var n uint32
		if err := binary.Read(h.nc, binary.BigEndian, &n); err != nil {
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(h.nc, buf); err != nil {
			return
		}
		var f fakeWireFrame
		if err := json.Unmarshal(buf, &f); err != nil {
			return
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				go h.answerCall(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
}

func (h *fakeHost) answerCall(f fakeWireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, f.Call.Method)
	h.callLog = append(h.callLog, fakeHostCall{Method: f.Call.Method, Args: args})
	h.mu.Unlock()
	var result any = map[string]any{}
	var errMsg string

	switch f.Call.Method {
	case "registerTool":
		var tl fakeRegisteredTool
		if err := json.Unmarshal(f.Call.Args, &tl); err == nil && tl.Name != "" {
			h.mu.Lock()
			_, existed := h.tools[tl.Name]
			if !existed {
				h.toolOrder = append(h.toolOrder, tl.Name)
			}
			h.tools[tl.Name] = tl
			curActive := append([]string(nil), h.activeTools...)
			if !existed && fakeToolStartsActive(tl) && !slices.Contains(curActive, tl.Name) {
				curActive = append(curActive, tl.Name)
			}
			h.mu.Unlock()
			h.applyLoadoutLocked(curActive)
		}
	case "getAllTools":
		h.mu.Lock()
		list := make([]map[string]any, 0, len(h.toolOrder))
		for _, name := range h.toolOrder {
			tl := h.tools[name]
			entry := map[string]any{
				"name":        tl.Name,
				"description": tl.Description,
				"parameters":  tl.Parameters,
				"source":      "extension",
			}
			if tl.Exposure != "" {
				entry["exposure"] = tl.Exposure
			}
			list = append(list, entry)
		}
		h.mu.Unlock()
		result = map[string]any{"tools": list}
	case "getActiveTools":
		h.mu.Lock()
		cp := append([]string(nil), h.activeTools...)
		h.mu.Unlock()
		result = map[string]any{"tools": cp}
	case "setActiveTools":
		if raw, ok := args["tools"].([]any); ok {
			next := make([]string, 0, len(raw))
			for _, item := range raw {
				if s, ok := item.(string); ok {
					next = append(next, s)
				}
			}
			h.mu.Lock()
			h.setActiveToolCalls = append(h.setActiveToolCalls, append([]string(nil), next...))
			h.mu.Unlock()
			h.applyLoadoutLocked(next)
		}
	case "sessionRead":
		m, _ := args["method"].(string)
		switch m {
		case "getSessionId":
			result = h.sessionID
		case "getSessionFile":
			result = h.sessionFile
		case "getCwd":
			result = h.cwd
		case "getBranch", "getEntries":
			result = []any{}
		default:
			result = nil
		}
	case "configHome":
		result = map[string]any{"path": h.configHome}
	case "isProjectTrusted":
		result = map[string]any{"trusted": h.projectTrusted}
	case "getSettings":
		result = map[string]any{"settings": map[string]any{}}
	case "getSystemPromptOptions":
		h.mu.Lock()
		sel := append([]string(nil), h.activeTools...)
		hid := append([]string(nil), h.hiddenOrder...)
		h.mu.Unlock()
		result = map[string]any{"options": map[string]any{
			"selectedTools": sel,
			"hiddenTools":   hid,
		}}
	case "ui.select":
		title, _ := args["title"].(string)
		var options []string
		if rawOpts, ok := args["options"].([]any); ok {
			for _, it := range rawOpts {
				if s, ok := it.(string); ok {
					options = append(options, s)
				}
			}
		}
		// The SDK reads {"ok": true, "selected": <value>}; anything else is a
		// dismissed dialog (extensions/sdk/context.go valueDialog).
		result = map[string]any{"ok": false}
		if h.selectAnswer != nil {
			if ans, ok := h.selectAnswer(title, options); ok {
				result = map[string]any{"ok": true, "selected": ans}
			}
		}
	case "isIdle":
		result = map[string]any{"idle": true}
	case "fileMutation.enter":
		h.mu.Lock()
		h.nextID++
		ticket := fmt.Sprintf("fm-%d", h.nextID)
		h.mu.Unlock()
		result = map[string]any{"ticket": ticket}
	case "fileMutation.exit":
		result = map[string]any{}
	case "diff.patch":
		result = map[string]any{"patch": ""}
	case "executeTool":
		toolName, _ := args["name"].(string)
		rawArgs, _ := args["args"].(map[string]any)
		if rawArgs == nil {
			rawArgs = map[string]any{}
		}
		resBytes, callErr := h.callToolInternal(toolName, "nested-"+toolName, rawArgs, true)
		if callErr != nil {
			result = map[string]any{
				"toolCall": map[string]any{"type": "toolCall", "id": "nested-" + toolName, "name": toolName, "arguments": rawArgs},
				"result":   map[string]any{"content": []map[string]any{{"type": "text", "text": callErr.Error()}}, "isError": true},
				"isError":  true,
			}
		} else {
			var tr struct {
				Content string         `json:"content"`
				Details map[string]any `json:"details"`
				IsError bool           `json:"isError"`
			}
			_ = json.Unmarshal(resBytes, &tr)
			result = map[string]any{
				"toolCall": map[string]any{"type": "toolCall", "id": "nested-" + toolName, "name": toolName, "arguments": rawArgs},
				"result":   map[string]any{"content": []map[string]any{{"type": "text", "text": tr.Content}}, "details": tr.Details, "isError": tr.IsError},
				"isError":  tr.IsError,
			}
		}
	}

	cr := map[string]any{}
	if errMsg != "" {
		cr["error"] = map[string]any{"message": errMsg}
	} else {
		rawRes, _ := json.Marshal(result)
		cr["result"] = json.RawMessage(rawRes)
	}
	h.writeFrame(map[string]any{
		"type":        "call_result",
		"id":          f.ID,
		"call_result": cr,
	})
}

func (h *fakeHost) request(req map[string]any) fakeWireFrame {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan fakeWireFrame, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.writeFrame(map[string]any{
		"type":    "request",
		"id":      id,
		"request": req,
	})
	select {
	case resp := <-ch:
		return resp
	case <-time.After(45 * time.Second):
		h.t.Fatalf("timeout waiting for response to %+v", req)
		return fakeWireFrame{}
	}
}

func (h *fakeHost) tool(name string) fakeRegisteredTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	tl, ok := h.tools[name]
	if !ok {
		h.t.Fatalf("tool %q not registered", name)
	}
	return tl
}

func (h *fakeHost) registeredToolNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.tools))
	for k := range h.tools {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

func (h *fakeHost) emitEvent(event string, args map[string]any) {
	h.t.Helper()
	_ = h.emitEventResults(event, args)
}

func (h *fakeHost) emitEventResults(event string, args map[string]any) []map[string]any {
	h.t.Helper()
	h.mu.Lock()
	ids := append([]int(nil), h.handlers[event]...)
	h.mu.Unlock()
	if args == nil {
		args = map[string]any{}
	}
	raw, _ := json.Marshal(args)
	var results []map[string]any
	for _, hid := range ids {
		resp := h.request(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		})
		if resp.Response != nil && resp.Response.Error != nil {
			h.t.Fatalf("event %s (handler %d) error: %s", event, hid, resp.Response.Error.Message)
		}
		if resp.Response != nil && len(resp.Response.Result) > 0 && string(bytes.TrimSpace(resp.Response.Result)) != "null" {
			var m map[string]any
			if err := json.Unmarshal(resp.Response.Result, &m); err == nil && m != nil {
				results = append(results, m)
			}
		}
	}
	return results
}

// emitBeforeAgentStart fires `before_agent_start` with realistic PiG
// `systemPromptOptions` (including `selectedTools` and `hiddenTools` from the
// current loadout) and returns the rendered system prompt if a handler returned one.
func (h *fakeHost) emitBeforeAgentStart() string {
	h.t.Helper()
	h.mu.Lock()
	sel := append([]string(nil), h.activeTools...)
	hid := append([]string(nil), h.hiddenOrder...)
	h.mu.Unlock()
	results := h.emitEventResults(sdk.EventBeforeAgentStart, map[string]any{
		"systemPromptOptions": map[string]any{
			"cwd":           h.cwd,
			"selectedTools": sel,
			"hiddenTools":   hid,
		},
	})
	var rendered string
	for _, m := range results {
		if inner, ok := m["_pigPromptResult"].(map[string]any); ok {
			m = inner
		}
		if sp, ok := m["systemPrompt"].(string); ok && sp != "" {
			rendered = sp
		}
	}
	return rendered
}
func (h *fakeHost) callCommand(name, args string) error {
	h.t.Helper()
	resp := h.request(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   args,
	})
	if resp.Response != nil && resp.Response.Error != nil {
		return errors.New(resp.Response.Error.Message)
	}
	return nil
}

type toolCallOutcome struct {
	Content string         `json:"content"`
	Details map[string]any `json:"details"`
	IsError bool           `json:"isError"`
}

// callTool mirrors PiG's real agent loop order for a direct model tool call:
//  0. Refuse inactive/unknown tools with `Tool <name> not found`
//     (matching PiG agent/tool_execution.go:366-368; hidden declarations stay
//     active and therefore remain directly callable).
//  1. If prepares_arguments is true, run tool_prepare_arguments.
//  2. Validate and coerce against the tool's registered wire schema.
//  3. Run EventToolCall before-hooks (veto/block).
//  4. Run tool_call with the prepared+validated args.
func (h *fakeHost) callTool(toolName, toolCallID string, rawArgs map[string]any) (json.RawMessage, error) {
	h.t.Helper()
	return h.callToolInternal(toolName, toolCallID, rawArgs, false)
}

func (h *fakeHost) callToolInternal(toolName, toolCallID string, rawArgs map[string]any, nested bool) (json.RawMessage, error) {
	h.t.Helper()
	h.mu.Lock()
	tl, exists := h.tools[toolName]
	isActive := slices.Contains(h.activeTools, toolName)
	h.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("Tool %s not found", toolName)
	}
	if nested {
		if !fakeToolCallable(tl, isActive) {
			return nil, fmt.Errorf("Tool %s not found", toolName)
		}
	} else if !isActive {
		return nil, fmt.Errorf("Tool %s not found", toolName)
	}
	argsBytes, err := json.Marshal(rawArgs)
	if err != nil {
		return nil, err
	}
	if tl.PreparesArguments {
		prepResp := h.request(map[string]any{
			"method": "tool_prepare_arguments",
			"tool":   toolName,
			"args":   json.RawMessage(argsBytes),
		})
		if prepResp.Response != nil && prepResp.Response.Error != nil {
			return nil, errors.New(prepResp.Response.Error.Message)
		}
		if prepResp.Response != nil && len(prepResp.Response.Result) > 0 && string(bytes.TrimSpace(prepResp.Response.Result)) != "null" {
			argsBytes = prepResp.Response.Result
		}
	}
	validatedBytes, err := pigValidateAndCoerce(toolName, tl.Parameters, argsBytes)
	if err != nil {
		return nil, err
	}
	// Run EventToolCall hooks (e.g. bash approval veto).
	var validatedInput map[string]any
	_ = json.Unmarshal(validatedBytes, &validatedInput)
	vetoResults := h.emitEventResults(sdk.EventToolCall, map[string]any{
		"toolName":   toolName,
		"toolCallId": toolCallID,
		"input":      validatedInput,
	})
	for _, res := range vetoResults {
		if inner, ok := res["_pigToolCallResult"].(map[string]any); ok {
			res = inner
		}
		if block, _ := res["block"].(bool); block {
			reason, _ := res["reason"].(string)
			if reason == "" {
				reason = "Tool call blocked"
			}
			return nil, errors.New(reason)
		}
	}
	resp := h.request(map[string]any{
		"method":       "tool_call",
		"tool":         toolName,
		"tool_call_id": toolCallID,
		"args":         json.RawMessage(validatedBytes),
	})
	if resp.Response != nil && resp.Response.Error != nil {
		return nil, errors.New(resp.Response.Error.Message)
	}
	if resp.Response == nil {
		return nil, nil
	}
	return resp.Response.Result, nil
}

func decodeToolOutcome(t *testing.T, raw json.RawMessage) toolCallOutcome {
	t.Helper()
	var out toolCallOutcome
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode tool outcome (%s): %v", string(raw), err)
	}
	return out
}
