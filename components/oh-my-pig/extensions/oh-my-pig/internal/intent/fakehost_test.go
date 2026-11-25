package intent

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fakeRegisteredTool captures the raw wire JSON of `parameters` so tests can
// inspect exact property key order on the wire (not a Go map).
type fakeRegisteredTool struct {
	Name              string          `json:"name"`
	Label             string          `json:"label"`
	Description       string          `json:"description"`
	Parameters        json.RawMessage `json:"parameters"`
	PreparesArguments bool            `json:"prepares_arguments"`
}

type fakeHandler struct {
	Event     string `json:"event"`
	HandlerID int    `json:"handler_id"`
}

type fakeWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []fakeRegisteredTool `json:"tools"`
		Handlers []fakeHandler        `json:"handlers"`
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
	t             *testing.T
	nc            net.Conn
	cwd           string
	sessionID     string
	mode          string
	hasUI         bool
	runDone       chan error
	writeMu       sync.Mutex
	mu            sync.Mutex
	pending       map[string]chan fakeWireFrame
	tools         map[string]fakeRegisteredTool
	handlers      map[string][]int
	registerCalls []string
	nextID        int
}

func startFakeHost(t *testing.T, ext *sdk.Extension, sessionID string) *fakeHost {
	return startFakeHostWithMode(t, ext, sessionID, "tui", true)
}

func startFakeHostWithMode(t *testing.T, ext *sdk.Extension, sessionID, mode string, hasUI bool) *fakeHost {
	t.Helper()
	cwd := t.TempDir()
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:         t,
		nc:        hostSide,
		cwd:       cwd,
		sessionID: sessionID,
		mode:      mode,
		hasUI:     hasUI,
		runDone:   make(chan error, 1),
		pending:   map[string]chan fakeWireFrame{},
		tools:     map[string]fakeRegisteredTool{},
		handlers:  map[string][]int{},
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
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	state, _ := json.Marshal(map[string]any{"hasUI": hasUI})
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          cwd,
			"mode":         mode,
			"width":        80,
			"state":        json.RawMessage(state),
		},
	})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) stop() {
	h.writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}

func (h *fakeHost) readFrame() fakeWireFrame {
	h.t.Helper()
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		h.t.Fatalf("read frame length: %v", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	payload := make([]byte, n)
	if _, err := io.ReadFull(h.nc, payload); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var f fakeWireFrame
	if err := json.Unmarshal(payload, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *fakeHost) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(lenBuf[:])
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) serve() {
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(h.nc, payload); err != nil {
			return
		}
		var f fakeWireFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			continue
		}
		switch f.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "call":
			if f.Call != nil {
				h.answerCall(f)
			}
		}
	}
}

func (h *fakeHost) answerCall(f fakeWireFrame) {
	method := f.Call.Method
	var result any
	var errMsg string

	switch method {
	case "getSessionID":
		result = map[string]any{"sessionId": h.sessionID}
	case "session.getSessionId":
		result = h.sessionID
	case "registerTool":
		var tl fakeRegisteredTool
		if err := json.Unmarshal(f.Call.Args, &tl); err != nil {
			errMsg = err.Error()
		} else {
			h.mu.Lock()
			h.tools[tl.Name] = tl
			h.registerCalls = append(h.registerCalls, tl.Name)
			h.mu.Unlock()
			result = map[string]any{}
		}
	default:
		result = map[string]any{}
	}

	if f.ID == "" {
		return
	}
	cr := map[string]any{}
	if errMsg != "" {
		cr["error"] = map[string]any{"message": errMsg}
	} else {
		raw, _ := json.Marshal(result)
		cr["result"] = json.RawMessage(raw)
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
	case <-time.After(3 * time.Second):
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

func (h *fakeHost) registeredCallNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.registerCalls)
}

func (h *fakeHost) resetRegisterCalls() {
	h.mu.Lock()
	h.registerCalls = nil
	h.mu.Unlock()
}

func (h *fakeHost) emitEvent(event string, args map[string]any) {
	h.t.Helper()
	h.mu.Lock()
	ids := append([]int(nil), h.handlers[event]...)
	h.mu.Unlock()
	raw, _ := json.Marshal(args)
	for _, hid := range ids {
		resp := h.request(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		})
		if resp.Response != nil && resp.Response.Error != nil {
			h.t.Fatalf("event %s error: %s", event, resp.Response.Error.Message)
		}
	}
}

// callTool mirrors PiG's real agent loop order (`agent/tool_execution.go:376-402`,
// `agent/tool_execution.go:536`):
//  1. If the tool declared `prepares_arguments`, run `tool_prepare_arguments`.
//  2. Validate AND coerce prepared args against the registered wire schema,
//     mirroring `ai.ValidateToolArgumentsJSON` (`ai/validate.go:44-75`) and
//     `schema/coerce.go` + `schema/checks.go` + `schema/explain.go`.
//  3. Run `tool_call` with the prepared+coerced+validated args.
func (h *fakeHost) callTool(toolName, toolCallID string, rawArgs map[string]any) (json.RawMessage, error) {
	h.t.Helper()
	tl := h.tool(toolName)
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

// pigValidateAndCoerce faithfully mirrors PiG's `ai.ValidateToolArgumentsJSON`
// (`ai/validate.go:44-95`) and `schema` package (`schema/coerce.go:7-11, 684-889,
// 1122-1239`; `schema/checks.go:20-66`; `schema/explain.go:88-133, 306-350`),
// because `github.com/MichaelKinsy/PiG/ai` is in the host module and cannot be
// imported from the standalone extension SDK module (`extensions/sdk/go.mod`).
//
// Specifically it implements:
//   - Pass 1 (`schema/coerce.go:684-745`): drop `null` on optional properties
//     whose schema does not accept `null`.
//   - Pass 2 (`schema/coerce.go:751-889, 1222-1239`): TypeBox Value.Convert rules
//     (`null` -> `"null"`, `5` -> `"5"`, `true` -> `"true"` for `type: "string"`;
//     string->number/integer/boolean; single value -> 1-element array; union
//     first-matching-branch conversion). Note objects/arrays do NOT convert to
//     string so `i: {"x":1}` fails with `expected string, got object`.
//   - Validation (`schema/checks.go`, `schema/explain.go`): `type`, `const`,
//     `enum`, `required` (`- <field>: missing`), `additionalProperties: false`
//     (`- <field>: unknown field`), `anyOf`/`oneOf`, and nested properties/items.
func pigValidateAndCoerce(toolName string, rawSchema, rawArgs json.RawMessage) (json.RawMessage, error) {
	var schema map[string]any
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return nil, fmt.Errorf("Validation failed for tool %q: invalid schema JSON: %w", toolName, err)
	}
	var value any
	if err := json.Unmarshal(rawArgs, &value); err != nil {
		return nil, fmt.Errorf("Validation failed for tool %q:\n  - invalid JSON: %w", toolName, err)
	}
	if len(schema) == 0 {
		return rawArgs, nil
	}

	// Pass 1: drop optional nulls (schema/coerce.go:684-745).
	coerced := pigDropOptionalNulls(value, schema)
	// Pass 2: TypeBox Value.Convert (schema/coerce.go:751-889).
	coerced = pigConvertValue(schema, coerced)

	errs := pigExplain(schema, coerced, "")
	if len(errs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Validation failed for tool %q:\n", toolName)
		for i, e := range errs {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("  - ")
			b.WriteString(e)
		}
		return nil, errors.New(b.String())
	}

	out, err := json.Marshal(coerced)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func pigDropOptionalNulls(value any, schema map[string]any) any {
	obj, isObj := value.(map[string]any)
	if !isObj || schema == nil {
		return value
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return value
	}
	req := extractStringSlice(schema["required"])
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		propRaw, declared := props[k]
		if declared {
			propSchema, _ := propRaw.(map[string]any)
			if v == nil && !slices.Contains(req, k) && propSchema != nil && len(pigExplain(propSchema, nil, "")) > 0 {
				continue
			}
			if propSchema != nil {
				v = pigDropOptionalNulls(v, propSchema)
			}
		}
		out[k] = v
	}
	return out
}

func pigConvertValue(schema map[string]any, value any) any {
	if schema == nil {
		return value
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		return pigConvertUnion(anyOf, value)
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		return pigConvertUnion(oneOf, value)
	}
	if typeName, ok := schema["type"].(string); ok {
		switch typeName {
		case "string":
			// schema/coerce.go:802-808, 1222-1239 (`tryString`):
			// nil -> "null", bool -> "true"/"false", finite number -> decimal string.
			// Objects and arrays do NOT coerce to string.
			if _, ok := value.(string); ok {
				return value
			}
			switch x := value.(type) {
			case nil:
				return "null"
			case bool:
				return strconv.FormatBool(x)
			case float64:
				if !math.IsNaN(x) && !math.IsInf(x, 0) {
					return strconv.FormatFloat(x, 'f', -1, 64)
				}
			}
			return value
		case "number":
			if _, ok := value.(float64); ok {
				return value
			}
			if s, ok := value.(string); ok {
				if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
					return n
				}
			}
			return value
		case "integer":
			if n, ok := value.(float64); ok {
				return math.Trunc(n)
			}
			if s, ok := value.(string); ok {
				if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
					return math.Trunc(n)
				}
			}
			return value
		case "boolean":
			if _, ok := value.(bool); ok {
				return value
			}
			if s, ok := value.(string); ok {
				if s == "true" {
					return true
				}
				if s == "false" {
					return false
				}
			}
			return value
		case "array":
			itemSchema, _ := schema["items"].(map[string]any)
			if arr, ok := value.([]any); ok {
				if itemSchema == nil {
					return arr
				}
				out := make([]any, len(arr))
				for i, item := range arr {
					out[i] = pigConvertValue(itemSchema, item)
				}
				return out
			}
			if itemSchema != nil {
				return []any{pigConvertValue(itemSchema, value)}
			}
			return []any{value}
		case "object":
			return pigConvertObject(schema, value)
		}
	}
	if _, hasProps := schema["properties"]; hasProps {
		return pigConvertObject(schema, value)
	}
	return value
}

func pigConvertUnion(branches []any, value any) any {
	for _, br := range branches {
		bm, ok := br.(map[string]any)
		if !ok {
			continue
		}
		if len(pigExplain(bm, value, "")) == 0 {
			return value
		}
	}
	for _, br := range branches {
		bm, ok := br.(map[string]any)
		if !ok {
			continue
		}
		cand := pigConvertValue(bm, value)
		if len(pigExplain(bm, cand, "")) == 0 {
			return cand
		}
	}
	return value
}

func pigConvertObject(schema map[string]any, value any) any {
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	props, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		if ps, ok := props[k].(map[string]any); ok {
			out[k] = pigConvertValue(ps, v)
		} else {
			out[k] = v
		}
	}
	return out
}

func pigExplain(schema map[string]any, value any, path string) []string {
	if schema == nil {
		return nil
	}
	loc := path
	if loc == "" {
		loc = "root"
	}

	if rawType, hasType := schema["type"]; hasType {
		var wantTypes []string
		switch t := rawType.(type) {
		case string:
			wantTypes = []string{t}
		case []any:
			for _, x := range t {
				if s, ok := x.(string); ok {
					wantTypes = append(wantTypes, s)
				}
			}
		}
		if len(wantTypes) > 0 && !pigMatchesAnyType(value, wantTypes) {
			return []string{fmt.Sprintf("%s: expected %s, got %s", loc, strings.Join(wantTypes, " or "), pigTypeName(value))}
		}
	}

	var errs []string
	if c, hasConst := schema["const"]; hasConst {
		if !pigEqual(c, value) {
			errs = append(errs, fmt.Sprintf("%s: expected %v", loc, c))
		}
	}
	if rawEnum, hasEnum := schema["enum"].([]any); hasEnum {
		matched := false
		for _, item := range rawEnum {
			if pigEqual(item, value) {
				matched = true
				break
			}
		}
		if !matched {
			errs = append(errs, fmt.Sprintf("%s: expected one of %v", loc, rawEnum))
		}
	}

	if anyOf, ok := schema["anyOf"].([]any); ok {
		matched := false
		var firstBranchErrs []string
		for _, br := range anyOf {
			bm, ok := br.(map[string]any)
			if !ok {
				continue
			}
			be := pigExplain(bm, value, path)
			if len(be) == 0 {
				matched = true
				break
			}
			if firstBranchErrs == nil {
				firstBranchErrs = be
			}
		}
		if !matched {
			errs = append(errs, firstBranchErrs...)
		}
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		matched := 0
		var firstBranchErrs []string
		for _, br := range oneOf {
			bm, ok := br.(map[string]any)
			if !ok {
				continue
			}
			be := pigExplain(bm, value, path)
			if len(be) == 0 {
				matched++
			} else if firstBranchErrs == nil {
				firstBranchErrs = be
			}
		}
		if matched != 1 {
			if len(firstBranchErrs) > 0 {
				errs = append(errs, firstBranchErrs...)
			} else {
				errs = append(errs, fmt.Sprintf("%s: expected exactly one oneOf branch to match (matched %d)", loc, matched))
			}
		}
	}

	if obj, isObj := value.(map[string]any); isObj {
		props, _ := schema["properties"].(map[string]any)
		for _, reqName := range extractStringSlice(schema["required"]) {
			if _, present := obj[reqName]; !present {
				errs = append(errs, fmt.Sprintf("%s: missing", joinPath(path, reqName)))
			}
		}
		for k, v := range obj {
			if ps, ok := props[k].(map[string]any); ok {
				errs = append(errs, pigExplain(ps, v, joinPath(path, k))...)
			} else if b, ok := props[k].(bool); ok && !b {
				errs = append(errs, fmt.Sprintf("%s: no value is allowed here", joinPath(path, k)))
			} else if addProps, hasAdd := schema["additionalProperties"]; hasAdd {
				if ab, ok := addProps.(bool); ok && !ab {
					errs = append(errs, fmt.Sprintf("%s: unknown field", joinPath(path, k)))
				} else if as, ok := addProps.(map[string]any); ok {
					errs = append(errs, pigExplain(as, v, joinPath(path, k))...)
				}
			}
		}
	}

	if arr, isArr := value.([]any); isArr {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				errs = append(errs, pigExplain(itemSchema, item, joinPath(path, strconv.Itoa(i)))...)
			}
		}
	}

	return errs
}

func joinPath(base, seg string) string {
	if base == "" {
		return seg
	}
	return base + "." + seg
}

func pigMatchesAnyType(v any, types []string) bool {
	for _, t := range types {
		switch t {
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "number":
			if n, ok := v.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
				return true
			}
		case "integer":
			if n, ok := v.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "null":
			if v == nil {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		}
	}
	return false
}

func pigTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func pigEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// jsonObjectKeys parses the top-level keys of a JSON object in wire order.
func jsonObjectKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("decode object open: %v (raw=%s)", err, string(raw))
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("expected '{', got %v (raw=%s)", tok, string(raw))
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("decode key: %v", err)
		}
		ks, ok := k.(string)
		if !ok {
			t.Fatalf("expected string key, got %T", k)
		}
		keys = append(keys, ks)
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("decode value for %q: %v", ks, err)
		}
	}
	return keys
}
