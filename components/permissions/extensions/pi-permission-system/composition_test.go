package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of test/composition-root.test.ts and test/session-start.test.ts. The original runs the real factory over a fake `pi`; here the
// real composition root (newExtensionWith) runs over an in-memory event bus and a synthetic session context, so every node of a
// test is a full extension instance in this process, sharing the process-global registries as production nodes do.

// memBus is an in-process pi.events: both the bus the node emits on and the one it subscribes to.
type memBus struct {
	mu       sync.Mutex
	handlers map[string][]*memSub
	emitted  []busEmit
}

type memSub struct {
	fn  func(any)
	off bool
}

func newMemBus() *memBus { return &memBus{handlers: map[string][]*memSub{}} }

func (b *memBus) Emit(channel string, data any) error {
	// payloads cross the bus as JSON
	raw, _ := json.Marshal(data)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	b.mu.Lock()
	b.emitted = append(b.emitted, busEmit{channel, decoded})
	subs := append([]*memSub{}, b.handlers[channel]...)
	b.mu.Unlock()
	for _, s := range subs {
		if !s.off {
			s.fn(decoded)
		}
	}
	return nil
}

func (b *memBus) On(channel string, handler func(data any)) (func(), error) {
	s := &memSub{fn: handler}
	b.mu.Lock()
	b.handlers[channel] = append(b.handlers[channel], s)
	b.mu.Unlock()
	return func() { b.mu.Lock(); s.off = true; b.mu.Unlock() }, nil
}

func (b *memBus) emits(channel string) []any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []any
	for _, e := range b.emitted {
		if e.channel == channel {
			out = append(out, e.data)
		}
	}
	return out
}

// compUI is the UI of a synthetic context: select answers through selectFn (nil: dismissed), titles are recorded.
type compUI struct {
	mu       sync.Mutex
	selectFn func(title string, options []string) (string, bool)
	titles   []string
}

func (u *compUI) Select(title string, options []string) (string, bool, error) {
	u.mu.Lock()
	u.titles = append(u.titles, title)
	fn := u.selectFn
	u.mu.Unlock()
	if fn == nil {
		return "", false, nil
	}
	v, ok := fn(title, options)
	return v, ok, nil
}
func (u *compUI) Input(string, string) (string, bool, error) { return "", false, nil }
func (u *compUI) Custom(PromptComponentFactory, PromptCustomOptions) (UnattributedDecision, bool, error) {
	return UnattributedDecision{}, false, nil
}
func (u *compUI) GetToolsExpanded() bool { return false }
func (u *compUI) SetToolsExpanded(bool)  {}
func (u *compUI) selected() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string{}, u.titles...)
}

type compCtxOpts struct {
	hasUI     *bool
	select_   func(title string, options []string) (string, bool)
	untrusted bool
	notify    func(string)
}

type compNotifier struct{ notify func(string) }

func (n compNotifier) Notify(message, _ string) {
	if n.notify != nil {
		n.notify(message)
	}
}
func (compNotifier) SetStatus(string, string) {}

// makeBaseCtx is the original's makeBaseCtx: cwd, a (by default trusted) project, a minimal session manager and a UI whose select is
// scripted. Mode "rpc" selects the select/input dialog, as a ctx with no `mode` does in the original.
func makeBaseCtx(cwd, sessionID string, o compCtxOpts) (SessionContext, *compUI) {
	ui := &compUI{selectFn: o.select_}
	hasUI := true
	if o.hasUI != nil {
		hasUI = *o.hasUI
	}
	trusted := !o.untrusted
	return SessionContext{
		AuthorityContext: AuthorityContext{
			ForwarderContext: ForwarderContext{HasUI: hasUI, UI: ui, Cwd: cwd, SessionManager: idManager{id: sessionID}},
			Mode:             "rpc",
			PromptUI:         ui,
			SessionName:      func() *string { return nil },
		},
		Notifier:         compNotifier{o.notify},
		IsProjectTrusted: func() bool { return trusted },
	}, ui
}

func noUI() *bool { f := false; return &f }

func makeChildCtx(cwd, sessionID string) SessionContext {
	c, _ := makeBaseCtx(cwd, sessionID, compCtxOpts{hasUI: noUI()})
	return c
}

// compTools is the fake pi's tool registry.
type compTools struct{ names []string }

func (t *compTools) GetAll() []any {
	out := []any{}
	for _, n := range t.names {
		o := newObject()
		o.set("name", n)
		out = append(out, o)
	}
	return out
}
func (t *compTools) GetActive() []string { return t.names }
func (t *compTools) SetActive([]string)  {}

type compAgentTools struct{ *compTools }

func (t compAgentTools) GetAll() ([]any, error)       { return t.compTools.GetAll(), nil }
func (t compAgentTools) GetActive() ([]string, error) { return t.compTools.names, nil }

// compNode is one extension instance under test.
type compNode struct {
	t   *testing.T
	p   *permissionSystem
	bus *memBus
}

// compEnv is the shared per-test environment: an isolated agent dir and the process-global registries cleaned up.
func compEnv(t *testing.T) string {
	t.Helper()
	fwClearHintEnv(t)
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	return dir
}

func newCompNode(t *testing.T, tools ...string) *compNode {
	t.Helper()
	bus := newMemBus()
	_, p := newExtensionWith(buildOptions{events: bus, lifecycleBus: bus})
	n := &compNode{t: t, p: p, bus: bus}
	reg := &compTools{names: tools}
	p.gateTools, p.agentTools = reg, compAgentTools{reg}
	return n
}

func (n *compNode) sessionStart(ctx SessionContext) {
	n.t.Helper()
	if err := n.p.lifecycle.HandleSessionStart(SessionStartPayload{Reason: "start"}, ctx); err != nil {
		n.t.Fatalf("session_start: %v", err)
	}
	n.t.Cleanup(func() { n.p.lifecycle.HandleSessionShutdown() })
}

func (n *compNode) shutdown() { n.p.lifecycle.HandleSessionShutdown() }

func (n *compNode) beforeAgentStart(ctx SessionContext, payload BeforeAgentStartPayload) {
	n.t.Helper()
	if err := n.p.agentPrep.Handle(payload, ctx); err != nil {
		n.t.Fatalf("before_agent_start: %v", err)
	}
}

// toolCall fires a tool_call through the fail-closed boundary with a synthetic context.
func (n *compNode) toolCall(ctx SessionContext, toolName string, input map[string]any) ToolCallResult {
	n.t.Helper()
	n.p.contextOf = func(sdk.Context) SessionContext { return ctx }
	return n.p.toolCall(map[string]any{"toolName": toolName, "toolCallId": "tc-1", "input": input}, sdk.Context{})
}

func (n *compNode) service(sessionID string) PermissionsService {
	n.t.Helper()
	s := GetPermissionsService(sessionID)
	if s == nil {
		n.t.Fatalf("no service published for %s", sessionID)
	}
	return s
}

func compWriteGlobalConfig(t *testing.T, agentDir string, config map[string]any) {
	t.Helper()
	merged := map[string]any{"debugLog": false, "permissionReviewLog": true, "yoloMode": false, "doublePressToConfirm": true}
	for k, v := range config {
		merged[k] = v
	}
	raw, _ := json.MarshalIndent(merged, "", "  ")
	p := filepath.Join(agentDir, "extensions", "pi-permission-system", "config.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compWriteProjectConfig(t *testing.T, cwd string, config map[string]any) {
	t.Helper()
	raw, _ := json.MarshalIndent(config, "", "  ")
	p := filepath.Join(cwd, ".pi", "extensions", "pi-permission-system", "config.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compReviewLog(agentDir string) []map[string]any {
	raw, err := os.ReadFile(filepath.Join(agentDir, "extensions", "pi-permission-system", "logs", reviewLogFilename))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(line), &m)
		out = append(out, m)
	}
	return out
}

func compEventCount(log []map[string]any, event string) int {
	n := 0
	for _, e := range log {
		if e["event"] == event {
			n++
		}
	}
	return n
}

func emptyPromptPayload() BeforeAgentStartPayload { return BeforeAgentStartPayload{SystemPrompt: ""} }

// unregisterOnCleanup keeps the process-global subagent registry clean between tests.
func registerChild(t *testing.T, childID, parentID string) {
	t.Helper()
	GetSubagentSessionRegistry().Register(childID, SubagentSessionInfo{ParentSessionID: &parentID})
	t.Cleanup(func() { GetSubagentSessionRegistry().Unregister(childID) })
}

func approveFirst(string, []string) (string, bool) { return "Yes", true }

var _ = time.Second
