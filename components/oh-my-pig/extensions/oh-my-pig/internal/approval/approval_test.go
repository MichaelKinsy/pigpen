package approval

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// Fake host harness for testing interactive EventToolCall prompts via sdk.Context.Select.
type approvalFakeHost struct {
	t         *testing.T
	nc        net.Conn
	cwd       string
	sessionID string
	hasUI     bool
	runDone   chan error
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan json.RawMessage
	nextID    int
	handlers  map[string]int
	onSelect  func(title string, options []string) (any, bool)
}

type approvalWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
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

func startApprovalFakeHost(t *testing.T, ext *sdk.Extension, cwd, sessionID string, hasUI bool) *approvalFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &approvalFakeHost{
		t:         t,
		nc:        hostSide,
		cwd:       cwd,
		sessionID: sessionID,
		hasUI:     hasUI,
		runDone:   make(chan error, 1),
		pending:   map[string]chan json.RawMessage{},
		handlers:  map[string]int{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.read()
	for first.Type == "call" && first.Call != nil {
		h.answer(first)
		first = h.read()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = hd.HandlerID
	}
	mode := "print"
	if hasUI {
		mode = "tui"
	}
	state, _ := json.Marshal(map[string]any{"hasUI": hasUI})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": sessionID,
		"cwd":          cwd,
		"mode":         mode,
		"width":        100,
		"height":       40,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *approvalFakeHost) read() approvalWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f approvalWireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *approvalFakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal frame: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(data)
}

func (h *approvalFakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f approvalWireFrame
		if err := json.Unmarshal(data, &f); err != nil {
			return
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				h.answer(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				if f.Response != nil && f.Response.Error != nil {
					ch <- json.RawMessage(`{"__err":` + mustJSONStr(f.Response.Error.Message) + `}`)
				} else if f.Response != nil {
					ch <- f.Response.Result
				} else {
					ch <- nil
				}
			}
		}
	}
}

func mustJSONStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (h *approvalFakeHost) answer(f approvalWireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	go func() {
		var result any = map[string]any{}
		switch f.Call.Method {
		case "getSessionID", "session_get_id", "get_session_id":
			result = map[string]any{"sessionId": h.sessionID, "session_id": h.sessionID}
		case "ui.select":
			title, _ := args["title"].(string)
			rawOpts, _ := args["options"].([]any)
			opts := make([]string, 0, len(rawOpts))
			for _, o := range rawOpts {
				if s, ok := o.(string); ok {
					opts = append(opts, s)
				}
			}
			h.mu.Lock()
			cb := h.onSelect
			h.mu.Unlock()
			if cb != nil {
				sel, ok := cb(title, opts)
				if ok {
					result = map[string]any{"ok": true, "selected": sel}
				} else {
					result = map[string]any{"ok": false}
				}
			} else {
				result = map[string]any{"ok": false}
			}
		}
		h.write(map[string]any{
			"type": "call_result",
			"id":   f.ID,
			"call_result": map[string]any{
				"result": result,
			},
		})
	}()
}

func (h *approvalFakeHost) emitToolCall(toolName string, input map[string]any) (map[string]any, error) {
	handlerID, ok := h.handlers[sdk.EventToolCall]
	if !ok {
		return nil, fmt.Errorf("no tool_call handler registered")
	}
	evJSON, _ := json.Marshal(map[string]any{
		"toolName": toolName,
		"input":    input,
	})
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("ev-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":     "event",
			"event":      sdk.EventToolCall,
			"handler_id": handlerID,
			"args":       json.RawMessage(evJSON),
		},
	})
	raw := <-ch
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if errStr, ok := out["__err"].(string); ok {
		return nil, fmt.Errorf("%s", errStr)
	}
	if wrapped, ok := out["_pigToolCallResult"]; ok {
		if wrapped == nil {
			return nil, nil
		}
		if m, ok := wrapped.(map[string]any); ok {
			return m, nil
		}
	}
	return out, nil
}

func (h *approvalFakeHost) stop() {
	_ = h.nc.Close()
	<-h.runDone
}

type testROSchemeHandler struct {
	scheme string
	spec   urls.Spec
}

func (h testROSchemeHandler) Scheme() string  { return h.scheme }
func (h testROSchemeHandler) Spec() urls.Spec { return h.spec }
func (h testROSchemeHandler) Resolve(_ urls.Context, _ urls.URL) (*urls.Resource, error) {
	return &urls.Resource{Content: "ok"}, nil
}

type testFileSchemeHandler struct {
	testROSchemeHandler
}

func (h testFileSchemeHandler) Locate(_ urls.Context, _ urls.URL, _ urls.LocateOptions) (string, bool, error) {
	return "/tmp/x", true, nil
}
func (h testFileSchemeHandler) LocateSync(_ urls.Context, _ urls.URL) (string, bool) {
	return "/tmp/x", true
}

type testWriterSchemeHandler struct {
	testROSchemeHandler
}

func (h testWriterSchemeHandler) Write(_ urls.Context, _ urls.URL, _ string) (*urls.WriteResult, error) {
	return &urls.WriteResult{Text: "ok"}, nil
}

func mustRegisterScheme(t *testing.T, h urls.Handler) {
	t.Helper()
	if err := urls.Register(h); err != nil {
		t.Fatalf("urls.Register(%s): %v", h.Scheme(), err)
	}
}

func registerTestSchemes(t *testing.T) {
	t.Helper()
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	// Register representative omp schemes matching internal-urls specs.
	mustRegisterScheme(t, testFileSchemeHandler{testROSchemeHandler{
		scheme: "local",
		spec: urls.Spec{
			Backing:  urls.BackingFile,
			Linkable: true,
			Write:    &urls.WritePolicy{Via: "file", Payload: "text", Scope: "sandbox"},
		},
	}})
	mustRegisterScheme(t, testWriterSchemeHandler{testROSchemeHandler{
		scheme: "agent",
		spec: urls.Spec{
			Backing: urls.BackingVirtual,
			Write:   &urls.WritePolicy{Via: "handler", Payload: "verbatim", Scope: "coordination"},
		},
	}})
	mustRegisterScheme(t, testWriterSchemeHandler{testROSchemeHandler{
		scheme: "out",
		spec: urls.Spec{
			Backing:   urls.BackingDevice,
			Immutable: true,
			Write:     &urls.WritePolicy{Via: "handler", Payload: "verbatim", Scope: "reply"},
		},
	}})
	mustRegisterScheme(t, testWriterSchemeHandler{testROSchemeHandler{
		scheme: "cfg",
		spec: urls.Spec{
			Backing:   urls.BackingVirtual,
			Immutable: true,
			Write:     &urls.WritePolicy{Via: "handler", Payload: "verbatim", Scope: "workspace"},
		},
	}})
	mustRegisterScheme(t, testWriterSchemeHandler{testROSchemeHandler{
		scheme: "conflict",
		spec: urls.Spec{
			Backing: urls.BackingVirtual,
			Write:   &urls.WritePolicy{Via: "handler", Payload: "verbatim", Scope: "workspace"},
		},
	}})
	mustRegisterScheme(t, testWriterSchemeHandler{testROSchemeHandler{
		scheme: "proc",
		spec: urls.Spec{
			Backing:   urls.BackingDevice,
			Immutable: true,
			Write:     &urls.WritePolicy{Via: "handler", Payload: "verbatim", Scope: "workspace"},
		},
	}})
	mustRegisterScheme(t, testROSchemeHandler{
		scheme: "history",
		spec: urls.Spec{
			Backing:   urls.BackingVirtual,
			Immutable: true,
		},
	})
	mustRegisterScheme(t, testROSchemeHandler{
		scheme: "skill",
		spec: urls.Spec{
			Backing:   urls.BackingFile,
			Immutable: true,
		},
	})
}

func saveApprovalGlobals(t *testing.T) {
	t.Helper()
	prevSetting := Setting
	prevSess := SessionSetting
	prevSub := IsSubagent
	prevOut := OutPolicy
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		Setting = prevSetting
		SessionSetting = prevSess
		IsSubagent = prevSub
		OutPolicy = prevOut
		ResetForTests()
		lifecycle.ResetForTests()
	})
}

// TestDecisionMatrixEveryToolEveryModeEveryPolicy verifies omp's full approval
// decision table across every tool (all 24 registered tools + hidden/special
// tools + MCP/extension tools + dynamic argument tiers), every approvalMode,
// every user policy (unset/allow/deny/prompt), and interactive vs headless.
// upstream: packages/coding-agent/test/tools/approval.test.ts: "resolveApproval tier matrix",
// "resolveApproval override and user policy", "MCP fallback and prompt formatting"
func TestDecisionMatrixEveryToolEveryModeEveryPolicy(t *testing.T) {
	saveApprovalGlobals(t)
	registerTestSchemes(t)
	OutPolicy = func(_ sdk.Context) urls.OutPolicy { return urls.OutPolicy{Enabled: true} }

	type toolCase struct {
		name     string
		input    map[string]any
		wantTier Tier
	}
	toolCases := []toolCase{
		// Read-tier tools
		{"read", map[string]any{"path": "src/main.go"}, TierRead},
		{"read", map[string]any{"path": "ssh://prod/etc/hosts"}, TierExec},
		{"read", map[string]any{"path": "doc.pdf:page1"}, TierExec},
		{"read", map[string]any{"path": "doc.pdf:1-5"}, TierRead},
		{"glob", map[string]any{"path": "src"}, TierRead},
		{"glob", map[string]any{"path": "ssh://prod/src"}, TierExec},
		{"grep", map[string]any{"pattern": "foo", "path": "src"}, TierRead},
		{"grep", map[string]any{"pattern": "foo", "path": "ssh://prod/src"}, TierExec},
		{"find", map[string]any{"query": "auth", "path": "."}, TierRead},
		{"ast_grep", map[string]any{"pat": "$A", "path": "src"}, TierRead},
		{"ask", map[string]any{"questions": []any{}}, TierRead},
		{"todo", map[string]any{"ops": []any{}}, TierRead},
		{"wait", map[string]any{}, TierRead},
		{"web_search", map[string]any{"query": "go"}, TierRead},
		{"checkpoint", map[string]any{}, TierRead},
		{"rewind", map[string]any{}, TierRead},
		{"yield", map[string]any{"data": "ok"}, TierRead},
		{"resolve", map[string]any{"reason": "ok"}, TierRead},
		{"reject", map[string]any{"reason": "no"}, TierRead},
		{"propose", map[string]any{"title": "plan"}, TierRead},
		// Dynamic read/write/exec tools
		{"write", map[string]any{"path": "foo.txt", "content": "x"}, TierWrite},
		{"write", map[string]any{"path": "local://plan.md", "content": "x"}, TierRead},
		{"write", map[string]any{"path": "agent://Peer", "content": "hi"}, TierRead},
		{"write", map[string]any{"path": "out://", "content": "hi"}, TierRead},
		{"write", map[string]any{"path": "cfg://theme", "content": "dark"}, TierWrite},
		{"write", map[string]any{"path": "conflict://1", "content": "ours"}, TierWrite},
		{"write", map[string]any{"path": "proc://bg_1/kill", "content": ""}, TierExec},
		{"edit", map[string]any{"input": "[foo.go#1A2B]\nPUT 1.=1:\n+x"}, TierWrite},
		{"edit", map[string]any{"input": "[local://notes.md#1A2B]\nPUT 1.=1:\n+x"}, TierRead},
		{"edit", map[string]any{"input": "[local://notes.md#1A2B]\nPUT 1.=1:\n+x\n[src/a.go#1A2B]\nPUT 1.=1:\n+y"}, TierWrite},
		{"ast_edit", map[string]any{"paths": []any{"src/a.go"}}, TierWrite},
		{"ast_edit", map[string]any{"paths": []any{"local://a.go"}}, TierRead},
		{"lsp", map[string]any{"action": "diagnostics"}, TierRead},
		{"lsp", map[string]any{"action": "rename"}, TierWrite},
		{"debug", map[string]any{"action": "threads"}, TierRead},
		{"debug", map[string]any{"action": "launch", "program": "./app"}, TierExec},
		{"github", map[string]any{"op": "repo_view"}, TierRead},
		{"github", map[string]any{"op": "pr_create"}, TierExec},
		{"goal", map[string]any{"op": "get"}, TierRead},
		{"goal", map[string]any{"op": "create"}, TierWrite},
		// Exec-tier tools
		{"bash", map[string]any{"command": "echo hi"}, TierExec},
		{"eval", map[string]any{"language": "js", "code": "1+1"}, TierExec},
		{"task", map[string]any{"tasks": []any{}}, TierExec},
		{"browser", map[string]any{"op": "open"}, TierExec},
		// MCP tools are write tier (mcp/tool-bridge.ts:811); unannotated extension tools default to exec
		{"mcp__server__tool", map[string]any{"x": 1}, TierWrite},
		{"custom_ext_tool", map[string]any{}, TierExec},
	}

	modes := []Mode{ModeAlwaysAsk, ModeAlwaysAskCached, ModeWrite, ModeYolo}
	userPolicies := []Policy{"", PolicyAllow, PolicyDeny, PolicyPrompt}

	upstreamExpected := func(tier Tier, mode Mode, userPol Policy) (Policy, string) {
		if userPol == PolicyDeny {
			return PolicyDeny, "user"
		}
		if mode == ModeYolo {
			if userPol != "" {
				return userPol, "user"
			}
			return PolicyAllow, "mode"
		}
		if userPol != "" {
			return userPol, "user"
		}
		switch mode {
		case ModeAlwaysAsk, ModeAlwaysAskCached:
			if tier == TierRead {
				return PolicyAllow, "mode"
			}
			return PolicyPrompt, "mode"
		case ModeWrite:
			if tier == TierRead || tier == TierWrite {
				return PolicyAllow, "mode"
			}
			return PolicyPrompt, "mode"
		}
		return PolicyAllow, "mode"
	}

	cells := 0
	for _, tc := range toolCases {
		dec := EvaluateToolDecision(sdk.Context{}, tc.name, tc.input)
		if dec.Tier != tc.wantTier {
			t.Fatalf("tool %q input %v: got tier=%q, want %q", tc.name, tc.input, dec.Tier, tc.wantTier)
		}
		for _, mode := range modes {
			for _, uPol := range userPolicies {
				cells++
				got := ResolvePolicy(tc.name, dec, mode, uPol)
				wantPol, wantSrc := upstreamExpected(tc.wantTier, mode, uPol)
				if got.Policy != wantPol || got.Source != wantSrc {
					t.Errorf("tool=%q tier=%q mode=%q userPol=%q: got {%s,%s}, want {%s,%s}",
						tc.name, tc.wantTier, mode, uPol, got.Policy, got.Source, wantPol, wantSrc)
				}
			}
		}
	}
	if cells < 40*4*4 {
		t.Fatalf("expected >= 640 decision cells, checked %d", cells)
	}
}

// TestOracleApprovalsPolicyAndPatterns reproduces the exact oracle case
// `approvals-policy-and-patterns` (#0 read, #1 write with tools.approval.write: deny,
// #2 bash pattern deny, #3 bash prompt headless refusal) byte-for-byte.
func TestOracleApprovalsPolicyAndPatterns(t *testing.T) {
	saveApprovalGlobals(t)
	registerTestSchemes(t)

	// Install a bash evaluator matching the oracle's bash.patterns: [{match: "echo forbidden*", approval: "deny"}].
	SetBashEvaluator(func(_ sdk.Context, input map[string]any) ToolDecision {
		cmd, _ := input["command"].(string)
		if strings.HasPrefix(cmd, "echo forbidden") {
			return ToolDecision{
				Tier:     TierExec,
				Policy:   PolicyDeny,
				Override: true,
				Reason:   "Blocked by bash pattern: echo forbidden*",
			}
		}
		return ToolDecision{Tier: TierExec}
	}, nil)
	t.Cleanup(func() { SetBashEvaluator(nil, nil) })

	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "always-ask"
		case "tools.approval":
			return map[string]any{"write": "deny"}
		}
		return nil
	}
	SessionSetting = Setting

	// Call 0: read r.txt -> allowed under always-ask (read tier)
	res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "read",
		"input":    map[string]any{"path": "r.txt"},
	})
	if err != nil || res != nil {
		t.Fatalf("call 0 read: expected allow, got res=%#v err=%v", res, err)
	}

	// Call 1: write blocked.txt -> user policy deny, exact omp text byte-for-byte
	res, err = HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "write",
		"input":    map[string]any{"path": "blocked.txt", "content": "nope"},
	})
	m, _ := res.(map[string]any)
	wantWriteDeny := "Tool \"write\" is blocked by user policy.\nTo allow: remove \"tools.approval.write: deny\" from config."
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantWriteDeny {
		t.Fatalf("call 1 write:\ngot:  %#v (err=%v)\nwant: block=true reason=%q", res, err, wantWriteDeny)
	}

	// Call 2: bash echo forbidden_cmd -> tool policy deny, exact omp text byte-for-byte
	res, err = HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "echo forbidden_cmd"},
	})
	m, _ = res.(map[string]any)
	wantBashDeny := "Tool \"bash\" is blocked by tool policy.\nReason: Blocked by bash pattern: echo forbidden*"
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantBashDeny {
		t.Fatalf("call 2 bash deny:\ngot:  %#v (err=%v)\nwant: block=true reason=%q", res, err, wantBashDeny)
	}

	// Call 3: bash echo needs_prompt -> headless prompt refusal, exact omp text byte-for-byte
	res, err = HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "echo needs_prompt"},
	})
	m, _ = res.(map[string]any)
	wantBashPrompt := "Tool \"bash\" requires approval but no interactive UI available.\n" +
		"Options:\n" +
		"  1. Set tools.approvalMode: yolo in /settings\n" +
		"  2. Add tools.approval.bash: allow to config\n" +
		"  3. Use an interactive UI to approve the tool call"
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantBashPrompt {
		t.Fatalf("call 3 bash prompt:\ngot:  %#v (err=%v)\nwant: block=true reason=%q", res, err, wantBashPrompt)
	}
}

// TestReadOnlyAndDisabledURLWriteDenials verifies that writes to read-only URL
// schemes (history://, skill://) and disabled/subagent out:// return omp's
// tool-policy deny text at the approval gate.
// upstream: internal-urls/router.ts:414-419, out-protocol.ts:57-65, approval.ts:594-598
func TestReadOnlyAndDisabledURLWriteDenials(t *testing.T) {
	saveApprovalGlobals(t)
	registerTestSchemes(t)

	Setting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "yolo"
		}
		return nil
	}

	// Read-only scheme history://
	res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "write",
		"input":    map[string]any{"path": "history://x", "content": "hi"},
	})
	m, _ := res.(map[string]any)
	wantHist := "Tool \"write\" is blocked by tool policy.\nReason: history:// URLs are read-only"
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantHist {
		t.Fatalf("write history://x: got %#v want %q", res, wantHist)
	}

	// Disabled out://
	OutPolicy = func(_ sdk.Context) urls.OutPolicy { return urls.OutPolicy{Enabled: false} }
	res, err = HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "write",
		"input":    map[string]any{"path": "out://", "content": "hi"},
	})
	m, _ = res.(map[string]any)
	wantOutDisabled := "Tool \"write\" is blocked by tool policy.\nReason: out:// is off in this session (`out.enabled`; by default on only in the interactive TUI); reply in your final message instead."
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantOutDisabled {
		t.Fatalf("write out:// disabled: got %#v want %q", res, wantOutDisabled)
	}

	// Subagent out://
	OutPolicy = func(_ sdk.Context) urls.OutPolicy { return urls.OutPolicy{Enabled: false, Subagent: true} }
	res, err = HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "write",
		"input":    map[string]any{"path": "out://", "content": "hi"},
	})
	m, _ = res.(map[string]any)
	wantOutSub := "Tool \"write\" is blocked by tool policy.\nReason: Subagents cannot reply to the user through out://; report through your result instead."
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantOutSub {
		t.Fatalf("write out:// subagent: got %#v want %q", res, wantOutSub)
	}

	// Edit targeting read-only skill:// via hashline header
	res, err = HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "edit",
		"input":    map[string]any{"input": "[skill://foo/SKILL.md#1A2B]\nPUT 1.=1:\n+hack"},
	})
	m, _ = res.(map[string]any)
	wantEditRO := "Tool \"edit\" is blocked by tool policy.\nReason: skill:// URLs are read-only"
	if err != nil || m == nil || m["block"] != true || m["reason"] != wantEditRO {
		t.Fatalf("edit skill://: got %#v want %q", res, wantEditRO)
	}
}

// TestTwoViewSecurityProjectMayTightenNeverLoosen verifies that project-layer
// settings can tighten operator approvalMode and tools.approval.<tool> policies,
// and can NEVER loosen operator settings.
func TestTwoViewSecurityProjectMayTightenNeverLoosen(t *testing.T) {
	saveApprovalGlobals(t)

	// Case 1: Operator sets always-ask + write: deny; project tries to loosen to yolo + write: allow + eval: allow.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "always-ask"
		case "tools.approval":
			return map[string]any{"write": "deny", "edit": "prompt"}
		}
		return nil
	}
	SessionSetting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "yolo"
		case "tools.approval":
			return map[string]any{"write": "allow", "edit": "allow", "eval": "allow"}
		}
		return nil
	}
	if got := EffectiveApprovalMode(sdk.Context{}); got != ModeAlwaysAsk {
		t.Fatalf("project loosened approvalMode: got %q, want always-ask", got)
	}
	if got := EffectiveUserPolicy(sdk.Context{}, "write"); got != PolicyDeny {
		t.Fatalf("project loosened write deny: got %q, want deny", got)
	}
	if got := EffectiveUserPolicy(sdk.Context{}, "edit"); got != PolicyPrompt {
		t.Fatalf("project loosened edit prompt: got %q, want prompt", got)
	}
	if got := EffectiveUserPolicy(sdk.Context{}, "eval"); got != "" {
		t.Fatalf("project granted eval allow when operator unset: got %q, want empty", got)
	}

	// Case 2: Operator sets yolo + write: allow; project tightens to write mode + write: deny + eval: prompt.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "yolo"
		case "tools.approval":
			return map[string]any{"write": "allow"}
		}
		return nil
	}
	SessionSetting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "write"
		case "tools.approval":
			return map[string]any{"write": "deny", "eval": "prompt"}
		}
		return nil
	}
	if got := EffectiveApprovalMode(sdk.Context{}); got != ModeWrite {
		t.Fatalf("project failed to tighten approvalMode: got %q, want write", got)
	}
	if got := EffectiveUserPolicy(sdk.Context{}, "write"); got != PolicyDeny {
		t.Fatalf("project failed to tighten write allow->deny: got %q, want deny", got)
	}
	if got := EffectiveUserPolicy(sdk.Context{}, "eval"); got != PolicyPrompt {
		t.Fatalf("project failed to tighten eval unset->prompt: got %q, want prompt", got)
	}
}

// TestSubagentInheritsYoloModeButHonorsUserDeny verifies omp's subagent rule
// (`task/executor.ts:1298-1301`): subagents run headless with tools.approvalMode: yolo
// (parent `task` approval is the authorization boundary), while explicit user
// `tools.approval.<tool>: deny` still blocks.
func TestSubagentInheritsYoloModeButHonorsUserDeny(t *testing.T) {
	saveApprovalGlobals(t)
	IsSubagent = func(_ sdk.Context) bool { return true }
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "always-ask"
		case "tools.approval":
			return map[string]any{"eval": "deny"}
		}
		return nil
	}

	// write succeeds unattended in subagent (approvalMode is yolo for child).
	if res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "write",
		"input":    map[string]any{"path": "a.txt", "content": "hi"},
	}); err != nil || res != nil {
		t.Fatalf("subagent write should run in yolo mode: res=%#v err=%v", res, err)
	}

	// eval is STILL blocked by user `tools.approval.eval: deny`.
	res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "eval",
		"input":    map[string]any{"language": "js", "code": "1+1"},
	})
	m, _ := res.(map[string]any)
	want := "Tool \"eval\" is blocked by user policy.\nTo allow: remove \"tools.approval.eval: deny\" from config."
	if err != nil || m == nil || m["block"] != true || m["reason"] != want {
		t.Fatalf("subagent user deny: got %#v want %q", res, want)
	}
}

// TestInteractiveSelectAndSessionGrantsAndLifecycleRelease verifies interactive
// UI prompting, session-scoped grants (`<tool>\x00<tier>`), isolation between
// tiers and sessions, and release via `lifecycle.End`.
func TestInteractiveSelectAndSessionGrantsAndLifecycleRelease(t *testing.T) {
	saveApprovalGlobals(t)
	registerTestSchemes(t)

	Setting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "always-ask"
		}
		return nil
	}
	SessionSetting = Setting

	ext := sdk.New("approval-test")
	Register(ext)
	sessA := "sess-A"
	h := startApprovalFakeHost(t, ext, t.TempDir(), sessA, true)

	var seenTitles []string
	var seenOpts [][]string
	var nextChoice string
	h.mu.Lock()
	h.onSelect = func(title string, opts []string) (any, bool) {
		seenTitles = append(seenTitles, title)
		seenOpts = append(seenOpts, append([]string(nil), opts...))
		return nextChoice, nextChoice != ""
	}
	h.mu.Unlock()

	// 1. Plain "Approve" allows write once and does NOT store a session grant.
	nextChoice = "Approve"
	res, err := h.emitToolCall("write", map[string]any{"path": "a.txt", "content": "hello"})
	if err != nil || res != nil {
		t.Fatalf("write Approve once: res=%#v err=%v", res, err)
	}
	if len(seenOpts) != 1 || len(seenOpts[0]) != 3 || seenOpts[0][1] != "Approve write for this session" {
		t.Fatalf("unexpected select options: %v", seenOpts)
	}
	if !strings.Contains(seenTitles[0], "Allow tool: write") || !strings.Contains(seenTitles[0], "Path: a.txt") {
		t.Fatalf("unexpected prompt title: %q", seenTitles[0])
	}
	if HasSessionApproval(sessA, "write\x00write") {
		t.Fatalf("plain Approve must not grant session approval")
	}

	// 2. "Approve write for this session" grants `write\x00write`.
	nextChoice = "Approve write for this session"
	res, err = h.emitToolCall("write", map[string]any{"path": "b.txt", "content": "world"})
	if err != nil || res != nil {
		t.Fatalf("write Approve session: res=%#v err=%v", res, err)
	}
	if !HasSessionApproval(sessA, "write\x00write") {
		t.Fatalf("expected write\\x00write grant recorded")
	}

	// 3. Subsequent write-tier call runs WITHOUT prompting.
	nextChoice = "Deny"
	res, err = h.emitToolCall("write", map[string]any{"path": "c.txt", "content": "auto"})
	if err != nil || res != nil {
		t.Fatalf("session-granted write should not prompt: res=%#v err=%v", res, err)
	}
	if len(seenOpts) != 2 {
		t.Fatalf("expected 2 prompts so far, got %d", len(seenOpts))
	}

	// 4. An exec-tier `write proc://bg_1/kill` is NOT covered by `write\x00write`
	// (tier pinning: approval.ts:578-589) and MUST prompt!
	nextChoice = "Deny"
	res, err = h.emitToolCall("write", map[string]any{"path": "proc://bg_1/kill", "content": ""})
	if err != nil || res == nil || res["block"] != true || res["reason"] != "Tool call denied by user: write" {
		t.Fatalf("exec-tier write proc:// kill must prompt despite write\\x00write grant: got res=%#v err=%v", res, err)
	}
	if len(seenOpts) != 3 {
		t.Fatalf("expected 3 prompts after proc:// kill, got %d", len(seenOpts))
	}

	// 5. Mounted tool called directly (e.g. `lsp` rename) prompts in always-ask.
	nextChoice = "Approve"
	res, err = h.emitToolCall("lsp", map[string]any{"action": "rename", "file": "main.go"})
	if err != nil || res != nil {
		t.Fatalf("lsp rename Approve: res=%#v err=%v", res, err)
	}
	if len(seenOpts) != 4 || !strings.Contains(seenTitles[3], "Allow tool: lsp") || !strings.Contains(seenTitles[3], "Action: rename") {
		t.Fatalf("unexpected lsp prompt: title=%q opts=%v", seenTitles[3], seenOpts[3])
	}

	// 6. Explicit user `prompt` policy does NOT offer a session grant (only 2 choices).
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "yolo"
		case "tools.approval":
			return map[string]any{"eval": "prompt"}
		}
		return nil
	}
	SessionSetting = Setting
	nextChoice = "Approve"
	res, err = h.emitToolCall("eval", map[string]any{"language": "py", "code": "1+1"})
	if err != nil || res != nil {
		t.Fatalf("eval explicit prompt: res=%#v err=%v", res, err)
	}
	if len(seenOpts) != 5 || len(seenOpts[4]) != 2 {
		t.Fatalf("explicit user prompt must offer only [Approve, Deny], got %v", seenOpts[4])
	}

	// 7. lifecycle.End(sessA) releases session grants.
	lifecycle.End(sessA)
	if HasSessionApproval(sessA, "write\x00write") {
		t.Fatalf("write\\x00write grant survived lifecycle.End")
	}
}

// TestLegacyNameFailClosedInheritance verifies upstream approval.test.ts:
// "honors deny/prompt policies written against a tool's legacy (pre-rename) name"
// and "does not inherit a legacy-name allow".
func TestLegacyNameFailClosedInheritance(t *testing.T) {
	dec := ToolDecision{Tier: TierWrite}
	gotDeny := ResolvePolicyWithLegacy("mcp__context7_query_docs", "mcp__context_query_docs", dec, ModeYolo, "", PolicyDeny)
	if gotDeny.Policy != PolicyDeny || gotDeny.Source != "user" || gotDeny.PolicyKey != "mcp__context_query_docs" {
		t.Fatalf("legacy deny: got %+v", gotDeny)
	}
	if msg := DenyError(gotDeny, "mcp__context7_query_docs"); msg != "Tool \"mcp__context_query_docs\" is blocked by user policy.\nTo allow: remove \"tools.approval.mcp__context_query_docs: deny\" from config." {
		t.Fatalf("legacy deny error text = %q", msg)
	}
	gotAllow := ResolvePolicyWithLegacy("mcp__context7_query_docs", "mcp__context_query_docs", dec, ModeAlwaysAsk, "", PolicyAllow)
	if gotAllow.Policy != PolicyPrompt {
		t.Fatalf("legacy allow must NOT be inherited: got %+v", gotAllow)
	}
}

// TestReviewFixes_RestrictedFoldersAndCachedGrantsAndFailClosed covers all
// review findings + binding integrator two-view decisions:
//   - tools.restrictedFolders (override in yolo/allow, symlink containment, recursive search above restricted folder, headless error)
//   - tools.approvalMode: always-ask-cached (session-scoped target & folder grants, folder listing, exact choice labels)
//   - strict-of-two-views (always-ask vs always-ask-cached in both directions, operator vs project allow/deny/prompt)
//   - malformed operator config fail-closed to always-ask with one-time notify
//   - CheckPrelude gating eval preludes (allow, deny, headless refusal, restricted folder)
//   - edit target & MV destination approval + prompt formatting
//   - write header unwrapping (`[proc://bg_1/kill#1A2B]` -> exec, `[skill://foo#1A2B]` -> deny)
//   - write `artifact://1` allowed in yolo (not registered read-only in omp v19.1.1)
func TestReviewFixes_RestrictedFoldersAndCachedGrantsAndFailClosed(t *testing.T) {
	saveApprovalGlobals(t)
	registerTestSchemes(t)

	cwd := t.TempDir()
	secretDir := filepath.Join(cwd, "secrets")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Restricted folder overrides yolo AND explicit user allow; fails closed headless.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "yolo"
		case "tools.approval":
			return map[string]any{"read": "allow", "write": "allow"}
		case "tools.restrictedFolders":
			return []string{"secrets"}
		}
		return nil
	}
	SessionSetting = Setting

	ext := sdk.New("approval-review-test")
	Register(ext)
	hHeadless := startApprovalFakeHost(t, ext, cwd, "sess-restr-headless", false)

	// Direct read inside restricted folder -> headless restriction error.
	res, err := hHeadless.emitToolCall("read", map[string]any{"path": "secrets/key.pem"})
	if err != nil || res == nil || res["block"] != true {
		t.Fatalf("restricted read must block headless, got res=%#v err=%v", res, err)
	}
	reasonStr, _ := res["reason"].(string)
	if !strings.Contains(reasonStr, "Restricted folder:") || !strings.Contains(reasonStr, "A restricted folder (tools.restrictedFolders) asks in every approval mode.") {
		t.Fatalf("unexpected headless restriction error: %q", reasonStr)
	}

	// Recursive grep rooted at cwd (above secrets/) ALSO triggers restricted folder.
	resGrep, err := hHeadless.emitToolCall("grep", map[string]any{"pattern": "API_KEY", "path": "."})
	if err != nil || resGrep == nil || resGrep["block"] != true || !strings.Contains(fmt.Sprint(resGrep["reason"]), "Restricted folder:") {
		t.Fatalf("recursive grep above restricted folder must prompt/block, got res=%#v err=%v", resGrep, err)
	}

	// Symlink pointing into restricted folder is caught via ApprovalCacheKeyForPath.
	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(cwd, "alias-secrets")
		if err := os.Symlink(secretDir, linkPath); err == nil {
			resLink, err := hHeadless.emitToolCall("read", map[string]any{"path": "alias-secrets/key.pem"})
			if err != nil || resLink == nil || resLink["block"] != true || !strings.Contains(fmt.Sprint(resLink["reason"]), "Restricted folder:") {
				t.Fatalf("symlink into restricted folder must block, got res=%#v err=%v", resLink, err)
			}
		}
	}

	// 2. always-ask-cached session-scoped target & folder grants with folder listing.
	cachedDir := t.TempDir()
	Setting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "always-ask-cached"
		}
		return nil
	}
	SessionSetting = Setting

	extCached := sdk.New("approval-cached-test")
	Register(extCached)
	hCached := startApprovalFakeHost(t, extCached, cachedDir, "sess-cached", true)
	var promptCount int
	var lastTitle string
	var lastOpts []string
	var pick string
	hCached.mu.Lock()
	hCached.onSelect = func(title string, opts []string) (any, bool) {
		promptCount++
		lastTitle = title
		lastOpts = append([]string(nil), opts...)
		return pick, pick != ""
	}
	hCached.mu.Unlock()

	// First read of a.txt prompts with Folder: ... — 0 entries (0 folders, 0 files) and Approve File.
	pick = "Approve File"
	if res, err := hCached.emitToolCall("read", map[string]any{"path": "a.txt"}); err != nil || res != nil {
		t.Fatalf("cached read a.txt Approve File: res=%#v err=%v", res, err)
	}
	if promptCount != 1 || !strings.Contains(lastTitle, "0 entries (0 folders, 0 files)") {
		t.Fatalf("expected folder listing on empty dir, got title=%q", lastTitle)
	}
	// Second read of a.txt is now cached (no prompt).
	if res, err := hCached.emitToolCall("read", map[string]any{"path": "a.txt"}); err != nil || res != nil {
		t.Fatalf("cached repeat read a.txt: res=%#v err=%v", res, err)
	}
	if promptCount != 1 {
		t.Fatalf("expected cached read a.txt to skip prompt, promptCount=%d", promptCount)
	}
	// Write to a.txt still prompts because read grant does not cover write tier.
	pick = "Approve File"
	if res, err := hCached.emitToolCall("write", map[string]any{"path": "a.txt", "content": "x"}); err != nil || res != nil {
		t.Fatalf("cached write a.txt: res=%#v err=%v", res, err)
	}
	if promptCount != 2 {
		t.Fatalf("expected write after read grant to prompt, promptCount=%d", promptCount)
	}
	// Approve Directory on b.txt covers c.txt in the same folder.
	var dirChoice string
	for _, o := range lastOpts {
		if strings.HasPrefix(o, "Approve Directory: ") {
			dirChoice = o
			break
		}
	}
	if dirChoice == "" {
		t.Fatalf("missing Approve Directory choice in %v", lastOpts)
	}
	pick = dirChoice
	if res, err := hCached.emitToolCall("read", map[string]any{"path": "b.txt"}); err != nil || res != nil {
		t.Fatalf("cached read b.txt Approve Directory: res=%#v err=%v", res, err)
	}
	if res, err := hCached.emitToolCall("read", map[string]any{"path": "c.txt"}); err != nil || res != nil {
		t.Fatalf("cached read c.txt covered by directory grant: res=%#v err=%v", res, err)
	}
	if promptCount != 3 {
		t.Fatalf("expected directory grant to cover c.txt without prompting, promptCount=%d", promptCount)
	}

	// 3. Two-view strictness: operator always-ask-cached + project always-ask -> read STILL prompts (operator view stricter).
	Setting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "always-ask-cached"
		}
		return nil
	}
	SessionSetting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "always-ask"
		}
		return nil
	}
	extTwoView := sdk.New("approval-twoview-test")
	Register(extTwoView)
	hTwoView := startApprovalFakeHost(t, extTwoView, t.TempDir(), "sess-twoview", false)
	if res, err := hTwoView.emitToolCall("read", map[string]any{"path": "a.txt"}); err != nil || res == nil || res["block"] != true {
		t.Fatalf("operator always-ask-cached + project always-ask must prompt/block read, got res=%#v err=%v", res, err)
	}

	// Reverse: operator always-ask + project always-ask-cached -> read ALSO prompts (session view stricter).
	Setting, SessionSetting = SessionSetting, Setting
	if res, err := hTwoView.emitToolCall("read", map[string]any{"path": "a.txt"}); err != nil || res == nil || res["block"] != true {
		t.Fatalf("operator always-ask + project always-ask-cached must prompt/block read, got res=%#v err=%v", res, err)
	}

	// 4. Malformed operator config fails closed to always-ask (prompts write, allows read).
	Setting = func(_ sdk.Context, key string) any { return nil }
	SessionSetting = Setting
	OperatorConfigError = func(_ sdk.Context) error { return fmt.Errorf("YAML parse error at line 2") }
	if res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{"toolName": "write", "input": map[string]any{"path": "x.txt", "content": "1"}}); err != nil || res == nil {
		t.Fatalf("malformed operator config must fail closed on write, got res=%#v err=%v", res, err)
	}
	if res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{"toolName": "read", "input": map[string]any{"path": "x.txt"}}); err != nil || res != nil {
		t.Fatalf("malformed operator config (always-ask) allows read, got res=%#v err=%v", res, err)
	}
	OperatorConfigError = nil

	// 5. Header unwrapping & write artifact://1 parity.
	Setting = func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "write"
		}
		return nil
	}
	SessionSetting = Setting
	// [proc://bg_1/kill#1A2B] unwraps to proc://bg_1/kill -> exec tier -> prompts under write mode.
	if res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{"toolName": "write", "input": map[string]any{"path": "[proc://bg_1/kill#1A2B]", "content": ""}}); err != nil || res == nil {
		t.Fatalf("wrapped [proc://bg_1/kill#1A2B] must resolve to exec and block headless under write mode, got res=%#v err=%v", res, err)
	}
	// artifact://1 is NOT a registered read-only scheme in omp v19.1.1 -> write tier -> allowed under write mode.
	if res, err := HandleToolCallVeto(sdk.Context{}, map[string]any{"toolName": "write", "input": map[string]any{"path": "artifact://1", "content": "x"}}); err != nil || res != nil {
		t.Fatalf("write artifact://1 must be write tier (allowed under write mode), got res=%#v err=%v", res, err)
	}

	// 6. edit multi-target & MV destination check (`edit/index.ts:460-478`).
	editMoveToProc := "[local://note.md#1A2B]\nMV proc://bg_1/kill\n"
	if dec := EvaluateToolDecision(sdk.Context{}, "edit", map[string]any{"input": editMoveToProc}); dec.Tier != TierExec {
		t.Fatalf("edit MV to proc:// must escalate to exec, got %+v", dec)
	}
	editReadOnly := "[skill://foo#1A2B]\nPUT 1.=1:\n+x\n"
	if dec := EvaluateToolDecision(sdk.Context{}, "edit", map[string]any{"input": editReadOnly}); dec.Policy != PolicyDeny || dec.Reason != "skill:// URLs are read-only" {
		t.Fatalf("edit skill:// must deny read-only, got %+v", dec)
	}
	editPrompt := FormatApprovalPrompt("edit", map[string]any{"input": "[a.txt#1A2B]\nMV b.txt\n"}, "")
	if !strings.Contains(editPrompt, "File: a.txt") || !strings.Contains(editPrompt, "Move: a.txt → b.txt") {
		t.Fatalf("unexpected edit prompt details: %q", editPrompt)
	}

	// 7. CheckPrelude enforces user deny & headless refusal (`eval/preludes.ts:149-187`).
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "always-ask"
		case "tools.approval":
			return map[string]any{"browser": "deny"}
		}
		return nil
	}
	SessionSetting = Setting
	if err := CheckPrelude(sdk.Context{}, "browser", "exec", nil); err == nil || !strings.Contains(err.Error(), `Tool "browser" is blocked by user policy.`) {
		t.Fatalf("CheckPrelude deny mismatch: %v", err)
	}
	if err := CheckPrelude(sdk.Context{}, "custom_prelude", "exec", nil); err == nil || !strings.Contains(err.Error(), `Eval prelude "custom_prelude" requires approval but no interactive UI is available.`) {
		t.Fatalf("CheckPrelude headless refusal mismatch: %v", err)
	}
}
