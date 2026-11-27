package snapcompact

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ── Unit tests for /shake parsing, formatting, stripping, and superseded reads ──

func TestParseAndFormatShake(t *testing.T) {
	cases := []struct {
		in      string
		want    ShakeMode
		wantErr bool
	}{
		{"", ShakeModeElide, false},
		{"  elide ", ShakeModeElide, false},
		{"IMAGES", ShakeModeImages, false},
		{"thinking", ShakeModeThinking, false},
		{"all", ShakeModeAll, false},
		{"bogus", "", true},
	}
	for _, tc := range cases {
		got, errMsg := ParseShakeMode(tc.in)
		if tc.wantErr {
			if errMsg == "" {
				t.Errorf("ParseShakeMode(%q) expected error, got mode %q", tc.in, got)
			}
			continue
		}
		if errMsg != "" || got != tc.want {
			t.Errorf("ParseShakeMode(%q) = (%q, %q), want (%q, \"\")", tc.in, got, errMsg, tc.want)
		}
	}

	items, err := shakeCompletions("")
	if err != nil || len(items) != 4 {
		t.Fatalf("shakeCompletions(\"\") = (%d, %v), want 4", len(items), err)
	}
	filtered, err := shakeCompletions("im")
	if err != nil || len(filtered) != 1 || filtered[0].Value != "images" {
		t.Fatalf("shakeCompletions(\"im\") = (%#v, %v), want [images]", filtered, err)
	}

	if s := FormatShakeSummary(ShakeResult{Mode: ShakeModeImages, ImagesDropped: 2, CharsFreed: 8000, TokensFreed: 2000}); !strings.Contains(s, "Dropped 2 images") {
		t.Errorf("unexpected images summary: %q", s)
	}
	if s := FormatShakeSummary(ShakeResult{Mode: ShakeModeThinking, ThinkingBlocksDropped: 1, CharsFreed: 400, TokensFreed: 100}); !strings.Contains(s, "Dropped 1 thinking block") {
		t.Errorf("unexpected thinking summary: %q", s)
	}
	if s := FormatShakeSummary(ShakeResult{Mode: ShakeModeElide, ToolResultsElided: 1, CharsFreed: 12000, TokensFreed: 3000}); !strings.Contains(s, "Shook 1 tool result") {
		t.Errorf("unexpected elide summary: %q", s)
	}
}

func TestResolveMidTurnThresholdPrecedence(t *testing.T) {
	// 1. thresholdTokens takes priority over thresholdPercent.
	if got := ResolveMidTurnThreshold(200000, 50000, 80); got != 50000 {
		t.Errorf("thresholdTokens priority: got %d, want 50000", got)
	}
	// 2. thresholdTokens >= contextWindow falls back to 90% safety valve.
	if got := ResolveMidTurnThreshold(100000, 120000, 40); got != 90000 {
		t.Errorf("thresholdTokens >= window fallback: got %d, want 90000", got)
	}
	// 3. thresholdPercent used when thresholdTokens is unset (-1 / 0).
	if got := ResolveMidTurnThreshold(200000, -1, 75); got != 150000 {
		t.Errorf("thresholdPercent: got %d, want 150000", got)
	}
	// 4. thresholdPercent clamped to [1, 99].
	if got := ResolveMidTurnThreshold(100000, -1, 150); got != 99000 {
		t.Errorf("thresholdPercent clamp high: got %d, want 99000", got)
	}
	// 5. Both unset (-1) -> 90% fallback.
	if got := ResolveMidTurnThreshold(128000, -1, -1); got != 115200 {
		t.Errorf("both unset fallback: got %d, want 115200", got)
	}
	// 6. Zero window -> 0 (disabled).
	if got := ResolveMidTurnThreshold(0, 50000, 50); got != 0 {
		t.Errorf("zero window: got %d, want 0", got)
	}
}

func TestElideSupersededReadsHonorsMutations(t *testing.T) {
	bigReadA := strings.Repeat("line of code in foo.go\n", 40)
	bigReadB := strings.Repeat("updated line of code in foo.go\n", 40)

	// Case 1: exact same file+range read twice with no intervening write/edit -> first read elided.
	msgsSame := []map[string]any{
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "src/foo.go:1-50"}}}},
		{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": bigReadA}}},
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c2", "name": "read", "arguments": map[string]any{"path": "src/foo.go:1-50"}}}},
		{"role": "toolResult", "toolCallId": "c2", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": bigReadA}}},
	}
	n, chars, changed := elideSupersededReads(msgsSame)
	if n != 1 || chars <= 0 || len(changed) != 1 {
		t.Fatalf("same-range reread: got n=%d chars=%d changed=%d, want n=1", n, chars, len(changed))
	}
	if _, ok := changed[1]; !ok {
		t.Fatalf("expected index 1 to be elided, got %#v", changed)
	}

	// Case 2: different range on same file -> NOT superseded.
	msgsDiffRange := []map[string]any{
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "src/foo.go:1-50"}}}},
		{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": bigReadA}}},
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c2", "name": "read", "arguments": map[string]any{"path": "src/foo.go:51-100"}}}},
		{"role": "toolResult", "toolCallId": "c2", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": bigReadB}}},
	}
	if n2, _, _ := elideSupersededReads(msgsDiffRange); n2 != 0 {
		t.Fatalf("different range should not supersede, got n=%d", n2)
	}

	// Case 3: edit intervened between two reads of the same path -> NOT superseded.
	msgsEdited := []map[string]any{
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "src/foo.go"}}}},
		{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": bigReadA}}},
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c-edit", "name": "edit", "arguments": map[string]any{"path": "src/foo.go"}}}},
		{"role": "toolResult", "toolCallId": "c-edit", "toolName": "edit", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
		{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "c2", "name": "read", "arguments": map[string]any{"path": "src/foo.go"}}}},
		{"role": "toolResult", "toolCallId": "c2", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": bigReadB}}},
	}
	if n3, _, _ := elideSupersededReads(msgsEdited); n3 != 0 {
		t.Fatalf("intervening edit must block superseding, got n=%d", n3)
	}
}

// ── Extended net.Pipe wire host for shake + mid-turn + promotion tests ──

type wave4Host struct {
	t            *testing.T
	conn         net.Conn
	done         chan error
	handlers     map[string]int
	commands     map[string]bool
	model        map[string]any
	registry     []map[string]any
	entries      []json.RawMessage
	usage        map[string]any
	notifies     []string
	setModels    []map[string]any
	resolveSpecs []string
}

type wave4Frame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Commands []struct {
			Name                string `json:"name"`
			ArgumentCompletions bool   `json:"argument_completions"`
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

func (h *wave4Host) readFrame() wave4Frame {
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
	var f wave4Frame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *wave4Host) writeFrame(v any) {
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

func startWave4Host(t *testing.T, ext *sdk.Extension, model map[string]any) (*wave4Host, func()) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()

	h := &wave4Host{
		t:        t,
		conn:     hostSide,
		done:     done,
		handlers: map[string]int{},
		commands: map[string]bool{},
		model:    model,
		registry: []map[string]any{model},
	}
	reg := h.readFrame()
	if reg.Type != "register" || reg.Register == nil {
		t.Fatalf("expected register, got %+v", reg)
	}
	for _, hnd := range reg.Register.Handlers {
		h.handlers[hnd.Event] = hnd.HandlerID
	}
	for _, cmd := range reg.Register.Commands {
		h.commands[cmd.Name] = cmd.ArgumentCompletions
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

func (h *wave4Host) handleCallsUntilResponse(reqID string) json.RawMessage {
	h.t.Helper()
	for {
		env := h.readFrame()
		if env.Type == "call" && env.Call != nil {
			var res any
			switch env.Call.Method {
			case "getSessionID":
				res = map[string]any{"sessionId": "sess-wave4"}
			case "getModelInfo":
				res = h.model
			case "getModelRegistryState":
				models := make([]any, 0, len(h.registry))
				for _, m := range h.registry {
					models = append(models, m)
				}
				res = map[string]any{
					"models":    models,
					"providers": map[string]any{},
				}
			case "getContextUsage":
				res = h.usage
			case "getEntries":
				res = h.entries
			case "getBranch":
				res = map[string]any{"entries": h.entries}
			case "watchSessionLog", "getSessionLog":
				var a map[string]any
				_ = json.Unmarshal(env.Call.Args, &a)
				cursor := 0
				if c, ok := a["cursor"].(float64); ok {
					cursor = int(c)
				}
				slice := []json.RawMessage{}
				if cursor < len(h.entries) {
					slice = h.entries[cursor:]
				}
				leafID := ""
				if len(h.entries) > 0 {
					var last map[string]any
					if json.Unmarshal(h.entries[len(h.entries)-1], &last) == nil {
						leafID, _ = last["id"].(string)
					}
				}
				res = map[string]any{
					"entries":    slice,
					"entryCount": len(h.entries),
					"hasMore":    false,
					"leafId":     leafID,
				}
			case "resolveModel":
				var a map[string]any
				_ = json.Unmarshal(env.Call.Args, &a)
				spec, _ := a["spec"].(string)
				h.resolveSpecs = append(h.resolveSpecs, spec)
				var found map[string]any
				for _, rm := range h.registry {
					id, _ := rm["id"].(string)
					prov, _ := rm["provider"].(string)
					if spec == id || spec == prov+"/"+id {
						found = rm
						break
					}
				}
				if found != nil {
					res = map[string]any{"model": found}
				} else {
					res = map[string]any{"error": "Model not found: " + spec}
				}
			case "setModel":
				var a map[string]any
				_ = json.Unmarshal(env.Call.Args, &a)
				if m, ok := a["model"].(map[string]any); ok {
					h.setModels = append(h.setModels, m)
					h.model = m
					res = map[string]any{"success": true}
				} else {
					res = map[string]any{"success": false, "error": "invalid model"}
				}
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
			continue
		}
		if env.Type == "response" && env.ID == reqID {
			if env.Response != nil && env.Response.Error != nil {
				h.t.Fatalf("request %s error: %s", reqID, env.Response.Error.Message)
			}
			if env.Response != nil {
				return env.Response.Result
			}
			return nil
		}
	}
}

func (h *wave4Host) emit(event string, payload map[string]any) map[string]any {
	h.t.Helper()
	hid, ok := h.handlers[event]
	if !ok {
		h.t.Fatalf("no handler for %s", event)
	}
	reqID := "req-" + event
	rawArgs, _ := json.Marshal(payload)
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
	raw := h.handleCallsUntilResponse(reqID)
	var out map[string]any
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (h *wave4Host) runCommand(name, args string) {
	h.t.Helper()
	reqID := "cmd-" + name
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
	_ = h.handleCallsUntilResponse(reqID)
}

func mustEntryJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	return raw
}

func unwrapBoundaryEntries(res map[string]any) ([]any, bool) {
	if res == nil {
		return nil, false
	}
	if br, ok := res["_pigBoundaryResult"].(map[string]any); ok {
		entries, _ := br["entries"].([]any)
		cont, _ := br["continue"].(bool)
		return entries, cont
	}
	entries, _ := res["entries"].([]any)
	cont, _ := res["continue"].(bool)
	return entries, cont
}

func TestShakeElideImagesThinkingEndToEnd(t *testing.T) {
	prevSetting := Setting
	prevSpill := WriteSpill
	defer func() {
		Setting = prevSetting
		WriteSpill = prevSpill
	}()
	Setting = nil
	var spilledLabels []string
	WriteSpill = func(_ sdk.Context, label, content string) string {
		spilledLabels = append(spilledLabels, label)
		return fmt.Sprintf("local://%d.log", len(spilledLabels)-1)
	}

	ext := sdk.New("shake-e2e")
	mgr := newManager()
	registerWithManager(ext, mgr)

	model := map[string]any{
		"id":            "gemini-4-neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 200000,
		"input":         []string{"text", "image"},
	}
	h, cleanup := startWave4Host(t, ext, model)
	defer cleanup()

	if !h.commands["shake"] {
		t.Fatalf("expected /shake command with argument completions registered, got %#v", h.commands)
	}

	bigBody := "HEAD_MARKER\n" + strings.Repeat("0123456789abcdef", 700) + "\nTAIL_MARKER"
	imgData := strings.Repeat("AAAA", 1200)

	// Populate branch with older image, older thinking, huge older toolResult, and newest user image.
	h.entries = []json.RawMessage{
		mustEntryJSON(t, map[string]any{
			"id": "e1", "parentId": nil, "type": "message",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "older screenshot"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": imgData},
				},
			},
		}),
		mustEntryJSON(t, map[string]any{
			"id": "e2", "parentId": "e1", "type": "message",
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "long internal reasoning chain"},
					map[string]any{"type": "toolCall", "id": "tc1", "name": "bash", "arguments": map[string]any{"command": "cat big.log"}},
				},
			},
		}),
		mustEntryJSON(t, map[string]any{
			"id": "e3", "parentId": "e2", "type": "message",
			"message": map[string]any{
				"role":       "toolResult",
				"toolCallId": "tc1",
				"toolName":   "bash",
				"content":    []any{map[string]any{"type": "text", "text": bigBody}},
			},
		}),
		mustEntryJSON(t, map[string]any{
			"id": "e4", "parentId": "e3", "type": "message",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "newest screenshot"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": imgData},
				},
			},
		}),
	}

	// 1. Default unshaken short session: EventContext and EventTurnEnd must be zero-side-effect no-ops.
	ctxNoop := h.emit(sdk.EventContext, map[string]any{
		"type": "context",
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
		},
	})
	if unchanged, _ := ctxNoop["_pigContextUnchanged"].(bool); !unchanged {
		t.Fatalf("expected unshaken short EventContext to be unchanged, got %#v", ctxNoop)
	}
	turnNoop := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":        "turn_end",
		"outcome":     "completed",
		"entries":     []any{},
		"toolResults": []any{},
	})
	if entries, _ := unwrapBoundaryEntries(turnNoop); len(entries) != 0 {
		t.Fatalf("expected unshaken short EventTurnEnd to emit no entries, got %#v", entries)
	}
	// 2. `/shake elide` via EventInput queues context_edit for e3 and spills body.
	inRes := h.emit(sdk.EventInput, map[string]any{"text": "/shake elide"})
	if inRes["action"] != "handled" {
		t.Fatalf("/shake elide action = %v, want handled", inRes["action"])
	}
	if len(h.notifies) == 0 || !strings.Contains(h.notifies[len(h.notifies)-1], "Shook 1 tool result") {
		t.Fatalf("expected shake notify, got %#v", h.notifies)
	}
	if len(spilledLabels) != 1 || spilledLabels[0] != "bash" {
		t.Fatalf("expected WriteSpill called once with label bash, got %#v", spilledLabels)
	}

	// 3. `/shake images` via RegisterCommand handler queues context_edit for e1 (keeping newest user e4).
	h.runCommand("shake", "images")
	if !strings.Contains(h.notifies[len(h.notifies)-1], "Dropped 1 image") {
		t.Fatalf("expected images drop notify, got %#v", h.notifies)
	}

	// 4. `/shake thinking` via EventInput queues context_edit for e2.
	inThink := h.emit(sdk.EventInput, map[string]any{"text": "/shake thinking"})
	if inThink["action"] != "handled" {
		t.Fatalf("/shake thinking action = %v, want handled", inThink["action"])
	}
	if !strings.Contains(h.notifies[len(h.notifies)-1], "Dropped 1 thinking block") {
		t.Fatalf("expected thinking drop notify, got %#v", h.notifies)
	}

	// 5. EventContext immediately strips older image (keeping newest user image), older thinking,
	// and elides the >8KB toolResult with head/tail preview + spill URL.
	ctxShaken := h.emit(sdk.EventContext, map[string]any{
		"type": "context",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "older screenshot"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": imgData},
				},
			},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "secret chain"},
					map[string]any{"type": "toolCall", "id": "tc1", "name": "bash"},
				},
			},
			map[string]any{
				"role":       "toolResult",
				"toolCallId": "tc1",
				"toolName":   "bash",
				"content":    []any{map[string]any{"type": "text", "text": bigBody}},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "newest screenshot"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": imgData},
				},
			},
		},
	})
	msgsOut, ok := ctxShaken["messages"].([]any)
	if !ok || len(msgsOut) != 4 {
		t.Fatalf("unexpected shaken EventContext output: %#v", ctxShaken)
	}
	// Older user message image stripped.
	for _, b := range msgsOut[0].(map[string]any)["content"].([]any) {
		if bm, _ := b.(map[string]any); bm["type"] == "image" {
			t.Fatalf("older user image should have been stripped")
		}
	}
	// Older assistant thinking stripped, toolCall preserved.
	asstBlocks := msgsOut[1].(map[string]any)["content"].([]any)
	if len(asstBlocks) != 1 || asstBlocks[0].(map[string]any)["type"] != "toolCall" {
		t.Fatalf("older assistant blocks = %#v, want only toolCall", asstBlocks)
	}
	// Older large toolResult elided with head, tail, and spill path.
	trText := msgsOut[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(trText, "HEAD_MARKER") || !strings.Contains(trText, "TAIL_MARKER") || !strings.Contains(trText, "local://0.log") {
		t.Fatalf("elided toolResult missing head/tail/spill: %q", trText)
	}
	if len(trText) >= len(bigBody) {
		t.Fatalf("elided toolResult not shorter: %d >= %d", len(trText), len(bigBody))
	}
	// Newest user message image preserved!
	newestHasImage := false
	for _, b := range msgsOut[3].(map[string]any)["content"].([]any) {
		if bm, _ := b.(map[string]any); bm["type"] == "image" {
			newestHasImage = true
		}
	}
	if !newestHasImage {
		t.Fatalf("newest user message image must be preserved")
	}

	// 6. EventTurnEnd commits all queued durable context_edit drafts (e3, e1, e2).
	turnRes := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":        "turn_end",
		"outcome":     "completed",
		"entries":     []any{},
		"toolResults": []any{},
	})
	drafts, _ := unwrapBoundaryEntries(turnRes)
	if len(drafts) != 3 {
		t.Fatalf("expected 3 durable context_edit drafts on EventTurnEnd, got %d: %#v", len(drafts), drafts)
	}
	seenTargets := map[string]bool{}
	for _, d := range drafts {
		dm := d.(map[string]any)
		if dm["type"] != "context_edit" {
			t.Errorf("expected context_edit draft, got %#v", dm)
		}
		tid, _ := dm["targetId"].(string)
		seenTargets[tid] = true
		rep, _ := dm["replacement"].(map[string]any)
		if rep == nil || rep["content"] == nil {
			t.Errorf("context_edit for %s missing replacement.content: %#v", tid, dm)
		}
	}
	for _, want := range []string{"e1", "e2", "e3"} {
		if !seenTargets[want] {
			t.Errorf("missing context_edit draft for %s; got %#v", want, seenTargets)
		}
	}

	// Next EventTurnEnd does not re-emit already committed drafts.
	turnRes2 := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":        "turn_end",
		"outcome":     "completed",
		"entries":     []any{},
		"toolResults": []any{},
	})
	if drafts2, _ := unwrapBoundaryEntries(turnRes2); len(drafts2) != 0 {
		t.Fatalf("expected 0 drafts on second EventTurnEnd, got %#v", drafts2)
	}
}

func TestMidTurnCompactionBoundaryDraftAndThresholds(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()

	cfg := map[string]any{
		"compaction.enabled":          true,
		"compaction.midTurnEnabled":   true,
		"compaction.thresholdTokens":  -1,
		"compaction.thresholdPercent": 50,
		"compaction.keepRecentTokens": 200,
		"compaction.methodOrder":      []string{"remote", "snapcompact", "soft"},
	}
	Setting = func(_ sdk.Context, key string) any { return cfg[key] }

	ext := sdk.New("midturn-e2e")
	mgr := newManager()
	registerWithManager(ext, mgr)

	visionModel := map[string]any{
		"id":            "gemini-4-neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 100000,
		"input":         []string{"text", "image"},
	}
	h, cleanup := startWave4Host(t, ext, visionModel)
	defer cleanup()

	// Build a multi-turn branch large enough to summarize cleanly.
	chunk := strings.Repeat("Analyzing repository architecture and compiling test plan details. ", 30)
	parent := any(nil)
	for i := 1; i <= 10; i++ {
		uid := fmt.Sprintf("u%d", i)
		aid := fmt.Sprintf("a%d", i)
		h.entries = append(h.entries,
			mustEntryJSON(t, map[string]any{
				"id": uid, "parentId": parent, "type": "message",
				"message": map[string]any{"role": "user", "content": fmt.Sprintf("Task %d: %s", i, chunk)},
			}),
			mustEntryJSON(t, map[string]any{
				"id": aid, "parentId": uid, "type": "message",
				"message": map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "toolCall", "id": fmt.Sprintf("tc%d", i), "name": "read", "arguments": map[string]any{"path": "main.go"}},
					map[string]any{"type": "text", "text": fmt.Sprintf("Done step %d: %s", i, chunk)},
				}},
			}),
		)
		parent = aid
	}

	// 1. Below threshold (40k < 50% of 100k = 50k): no mid-turn compaction.
	h.usage = map[string]any{"tokens": 40000, "contextWindow": 100000, "percent": 40.0}
	belowRes := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":           "turn_end",
		"outcome":        "completed",
		"entries":        []any{},
		"messageEntryId": "a10",
		"toolResults":    []any{map[string]any{"role": "toolResult", "toolName": "read"}},
	})
	if entries, _ := unwrapBoundaryEntries(belowRes); len(entries) != 0 {
		t.Fatalf("below threshold should not compact mid-turn, got %#v", entries)
	}

	// 2. Above threshold (60k >= 50k) WITHOUT tool results: do NOT trigger mid-turn compaction.
	h.usage = map[string]any{"tokens": 60000, "contextWindow": 100000, "percent": 60.0}
	noToolRes := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":           "turn_end",
		"outcome":        "completed",
		"entries":        []any{},
		"messageEntryId": "a10",
		"toolResults":    []any{},
	})
	if entries, _ := unwrapBoundaryEntries(noToolRes); len(entries) != 0 {
		t.Fatalf("turn without toolResults should not trigger mid-turn compaction, got %#v", entries)
	}

	// 3. Above threshold WITH tool results -> emits compaction boundary draft via snapcompact!
	midRes := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":           "turn_end",
		"outcome":        "completed",
		"entries":        []any{},
		"messageEntryId": "a10",
		"toolResults":    []any{map[string]any{"role": "toolResult", "toolName": "read"}},
	})
	drafts, _ := unwrapBoundaryEntries(midRes)
	if len(drafts) != 1 {
		t.Fatalf("expected 1 compaction boundary draft, got %d: %#v", len(drafts), drafts)
	}
	compDraft := drafts[0].(map[string]any)
	if compDraft["type"] != "compaction" {
		t.Fatalf("draft type = %v, want compaction", compDraft["type"])
	}
	if firstKept, _ := compDraft["firstKeptEntryId"].(string); firstKept == "" {
		t.Fatalf("compaction draft missing firstKeptEntryId: %#v", compDraft)
	}
	summary, _ := compDraft["summary"].(string)
	if !strings.Contains(summary, "HISTORY\n===================") {
		t.Fatalf("expected snapcompact summary in mid-turn draft, got:\n%s", summary)
	}
	details, _ := compDraft["details"].(map[string]any)
	if details == nil || details["phase"] != "mid_turn" {
		t.Fatalf("expected details.phase=mid_turn, got %#v", details)
	}
	if _, ok := ExtractArchiveFromDetails(details); !ok {
		t.Fatalf("expected snapcompact archive inside mid-turn compaction details")
	}

	// 4. Same cut point dead-end guard prevents duplicate mid-turn draft on next call.
	dupRes := h.emit(sdk.EventTurnEnd, map[string]any{
		"type":           "turn_end",
		"outcome":        "completed",
		"entries":        []any{},
		"messageEntryId": "a10",
		"toolResults":    []any{map[string]any{"role": "toolResult", "toolName": "read"}},
	})
	if dupDrafts, _ := unwrapBoundaryEntries(dupRes); len(dupDrafts) != 0 {
		t.Fatalf("expected dead-end guard to suppress duplicate compaction draft, got %#v", dupDrafts)
	}
}

func TestContextPromotionOnOverflowAndLoopGuard(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()

	cfg := map[string]any{
		"contextPromotion.enabled": true,
		"contextPromotion.target":  "anthropic/claude-opus-4-6",
	}
	Setting = func(_ sdk.Context, key string) any { return cfg[key] }

	ext := sdk.New("promote-e2e")
	mgr := newManager()
	registerWithManager(ext, mgr)

	smallModel := map[string]any{
		"id":            "claude-sonnet-4-5",
		"provider":      "anthropic",
		"api":           "anthropic-messages",
		"contextWindow": 200000,
		"input":         []string{"text", "image"},
	}
	largeModel := map[string]any{
		"id":            "claude-opus-4-6",
		"provider":      "anthropic",
		"api":           "anthropic-messages",
		"contextWindow": 1000000,
		"input":         []string{"text", "image"},
	}
	h, cleanup := startWave4Host(t, ext, smallModel)
	defer cleanup()
	h.registry = []map[string]any{smallModel, largeModel}
	h.entries = []json.RawMessage{
		mustEntryJSON(t, map[string]any{
			"id": "u1", "parentId": nil, "type": "message",
			"message": map[string]any{"role": "user", "content": "big prompt"},
		}),
		mustEntryJSON(t, map[string]any{
			"id": "a-overflow", "parentId": "u1", "type": "message",
			"message": map[string]any{
				"role":         "assistant",
				"stopReason":   "error",
				"errorMessage": "prompt is too long: 210000 tokens > 200000 maximum",
				"content":      []any{},
			},
		}),
	}

	// 1. Turn ends with overflow error + compaction fails.
	_ = h.emit(sdk.EventTurnEnd, map[string]any{
		"type":           "turn_end",
		"outcome":        "error",
		"messageEntryId": "a-overflow",
		"message": map[string]any{
			"role":         "assistant",
			"stopReason":   "error",
			"errorMessage": "prompt is too long: 210000 tokens > 200000 maximum",
		},
	})
	_ = h.emit(sdk.EventSessionCompactFailed, map[string]any{
		"type":         "session_compact_failed",
		"reason":       "overflow",
		"errorMessage": "compaction failed",
		"aborted":      false,
	})

	// 2. EventAgentBeforeSettle promotes model via SetModelNamed, omits failed assistant turn, and sets continue=true.
	settle1 := h.emit(sdk.EventAgentBeforeSettle, map[string]any{
		"type":    "agent_before_settle",
		"outcome": "error",
		"entries": []any{},
	})
	drafts1, cont1 := unwrapBoundaryEntries(settle1)
	if !cont1 {
		t.Fatalf("expected continue=true after overflow promotion, got %#v", settle1)
	}
	if len(h.resolveSpecs) != 1 || h.resolveSpecs[0] != "anthropic/claude-opus-4-6" {
		t.Fatalf("expected SetModelNamed(anthropic/claude-opus-4-6), got %#v", h.resolveSpecs)
	}
	if len(h.setModels) != 1 || h.setModels[0]["id"] != "claude-opus-4-6" {
		t.Fatalf("expected setModel to switch to claude-opus-4-6, got %#v", h.setModels)
	}
	if len(drafts1) != 1 {
		t.Fatalf("expected 1 context_edit omitting a-overflow, got %#v", drafts1)
	}
	omitDraft := drafts1[0].(map[string]any)
	if omitDraft["type"] != "context_edit" || omitDraft["targetId"] != "a-overflow" || omitDraft["replacement"] != nil {
		t.Fatalf("unexpected omit draft: %#v", omitDraft)
	}

	// 3. Loop guard: a second overflow in the same turn MUST NOT promote again or return continue=true.
	_ = h.emit(sdk.EventSessionCompactFailed, map[string]any{
		"type":         "session_compact_failed",
		"reason":       "overflow",
		"errorMessage": "still too long",
		"aborted":      false,
	})
	settle2 := h.emit(sdk.EventAgentBeforeSettle, map[string]any{
		"type":    "agent_before_settle",
		"outcome": "error",
		"entries": []any{},
	})
	drafts2, cont2 := unwrapBoundaryEntries(settle2)
	if cont2 || len(drafts2) != 0 {
		t.Fatalf("loop guard must block second promotion in same turn, got drafts=%#v cont=%v", drafts2, cont2)
	}
	if len(h.setModels) != 1 {
		t.Fatalf("setModel should not be called a second time, got %d calls", len(h.setModels))
	}

	// 4. After a completed turn resets the per-turn guard, a later overflow can promote again (using modelRoles.slow fallback).
	_ = h.emit(sdk.EventTurnEnd, map[string]any{
		"type":        "turn_end",
		"outcome":     "completed",
		"entries":     []any{},
		"toolResults": []any{},
	})
	h.model = smallModel
	cfg["contextPromotion.target"] = ""
	cfg["modelRoles.slow"] = "anthropic/claude-opus-4-6"
	_ = h.emit(sdk.EventSessionCompactFailed, map[string]any{
		"type":         "session_compact_failed",
		"reason":       "overflow",
		"errorMessage": "context_length_exceeded",
		"aborted":      false,
	})
	settle3 := h.emit(sdk.EventAgentBeforeSettle, map[string]any{
		"type":    "agent_before_settle",
		"outcome": "error",
		"entries": []any{},
	})
	_, cont3 := unwrapBoundaryEntries(settle3)
	if !cont3 || len(h.setModels) != 2 {
		t.Fatalf("expected promotion to re-arm after completed turn via modelRoles.slow, cont=%v setModels=%d", cont3, len(h.setModels))
	}
}
