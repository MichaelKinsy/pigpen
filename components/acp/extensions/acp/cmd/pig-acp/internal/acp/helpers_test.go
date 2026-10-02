package acp

// Test helpers: the fakes of test/helpers/fakes.ts (FakeAgentSideConnection and
// FakePiRpcProcess) and small assertions. The test files are named after the upstream
// files they port; every twin keeps its upstream test name in a comment or subtest name.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// sentUpdate is one session/update the fake connection received.
type sentUpdate struct {
	SessionID string
	Update    Update
}

// fakeConn is FakeAgentSideConnection.
type fakeConn struct {
	mu                 sync.Mutex
	updates            []sentUpdate
	permissionRequests []PermissionRequest
	nextPermission     PermissionResponse
	// sessionUpdateHook, when set, runs inside SessionUpdate before the update is recorded.
	sessionUpdateHook func(u Update)
}

func newFakeConn() *fakeConn {
	return &fakeConn{nextPermission: PermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "allow"}}}
}

func (c *fakeConn) SessionUpdate(sessionID string, update Update) error {
	c.mu.Lock()
	hook := c.sessionUpdateHook
	c.mu.Unlock()
	if hook != nil {
		hook(update)
	}
	c.mu.Lock()
	c.updates = append(c.updates, sentUpdate{sessionID, update})
	c.mu.Unlock()
	return nil
}

func (c *fakeConn) RequestPermission(req PermissionRequest) (PermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissionRequests = append(c.permissionRequests, req)
	return c.nextPermission, nil
}

func (c *fakeConn) all() []sentUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sentUpdate(nil), c.updates...)
}

func (c *fakeConn) kinds() []string {
	var out []string
	for _, u := range c.all() {
		out = append(out, str(u.Update["sessionUpdate"]))
	}
	return out
}

func (c *fakeConn) ofKind(kind string) []sentUpdate {
	var out []sentUpdate
	for _, u := range c.all() {
		if u.Update["sessionUpdate"] == kind {
			out = append(out, u)
		}
	}
	return out
}

func (c *fakeConn) reset() {
	c.mu.Lock()
	c.updates = nil
	c.mu.Unlock()
}

func (c *fakeConn) permissions() []PermissionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]PermissionRequest(nil), c.permissionRequests...)
}

// fakeProc is FakePiRpcProcess. Tests override behavior through the func fields.
type fakeProc struct {
	mu       sync.Mutex
	handlers []func(Event)

	prompts              []promptCall
	extensionUIResponses []map[string]any
	abortCount           int
	getSessionStatsCount int
	disposed             int
	disposeHook          func()

	sessionStats      SessionStats
	sessionStatsError error
	statsTimeouts     []int

	getStateFn         func() (map[string]any, error)
	getModelsFn        func() (map[string]any, error)
	getLevelsFn        func() ([]string, error)
	getMessagesFn      func() (map[string]any, error)
	getCommandsFn      func() (map[string]any, error)
	setModelFn         func(provider, id string) error
	setThinkingFn      func(level string) error
	setSteeringFn      func(mode string) error
	setFollowUpFn      func(mode string) error
	setSessionNameFn   func(name string) error
	compactFn          func(instr string) (map[string]any, error)
	setAutoCompactFn   func(bool) error
	exportHTMLFn       func(path string) (string, error)
	getSessionStatsFn  func(timeoutMs int) (SessionStats, error)
	promptErr          error
	promptHook         func()
	extensionUIRespErr error
}

type promptCall struct {
	Message string
	Images  []Image
}

func newFakeProc() *fakeProc { return &fakeProc{} }

func (p *fakeProc) OnEvent(h func(Event)) func() {
	p.mu.Lock()
	p.handlers = append(p.handlers, h)
	idx := len(p.handlers) - 1
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if idx < len(p.handlers) {
			p.handlers[idx] = func(Event) {}
		}
	}
}

// emit delivers an event to every handler, synchronously, as FakePiRpcProcess.emit does.
func (p *fakeProc) emit(ev Event) {
	p.mu.Lock()
	hs := append([]func(Event){}, p.handlers...)
	p.mu.Unlock()
	for _, h := range hs {
		h(ev)
	}
}

func (p *fakeProc) Prompt(message string, images []Image) error {
	p.mu.Lock()
	if images == nil {
		images = []Image{}
	}
	p.prompts = append(p.prompts, promptCall{message, images})
	err, hook := p.promptErr, p.promptHook
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (p *fakeProc) promptList() []promptCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]promptCall(nil), p.prompts...)
}

func (p *fakeProc) Abort() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.abortCount++
	return nil
}

func (p *fakeProc) aborts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.abortCount
}

func (p *fakeProc) SendExtensionUIResponse(resp map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.extensionUIResponses = append(p.extensionUIResponses, resp)
	return p.extensionUIRespErr
}

func (p *fakeProc) uiResponses() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.extensionUIResponses...)
}

func (p *fakeProc) GetState() (map[string]any, error) {
	if p.getStateFn != nil {
		return p.getStateFn()
	}
	return map[string]any{}, nil
}

func (p *fakeProc) GetAvailableModels() (map[string]any, error) {
	if p.getModelsFn != nil {
		return p.getModelsFn()
	}
	return map[string]any{"models": []any{map[string]any{"provider": "test", "id": "model", "name": "model"}}}, nil
}

func (p *fakeProc) GetAvailableThinkingLevels() ([]string, error) {
	if p.getLevelsFn != nil {
		return p.getLevelsFn()
	}
	return []string{"medium", "high"}, nil
}

func (p *fakeProc) GetMessages() (map[string]any, error) {
	if p.getMessagesFn != nil {
		return p.getMessagesFn()
	}
	return map[string]any{"messages": []any{}}, nil
}

func (p *fakeProc) GetCommands() (map[string]any, error) {
	if p.getCommandsFn != nil {
		return p.getCommandsFn()
	}
	return nil, fmt.Errorf("pi get_commands failed: unsupported")
}

func (p *fakeProc) GetSessionStats(timeoutMs int) (SessionStats, error) {
	p.mu.Lock()
	p.getSessionStatsCount++
	p.statsTimeouts = append(p.statsTimeouts, timeoutMs)
	fn, stats, err := p.getSessionStatsFn, p.sessionStats, p.sessionStatsError
	p.mu.Unlock()
	if fn != nil {
		return fn(timeoutMs)
	}
	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = SessionStats{}
	}
	return stats, nil
}

func (p *fakeProc) statsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getSessionStatsCount
}

func (p *fakeProc) SetModel(provider, id string) error {
	if p.setModelFn != nil {
		return p.setModelFn(provider, id)
	}
	return nil
}

func (p *fakeProc) SetThinkingLevel(level string) error {
	if p.setThinkingFn != nil {
		return p.setThinkingFn(level)
	}
	return nil
}

func (p *fakeProc) SetSteeringMode(mode string) error {
	if p.setSteeringFn != nil {
		return p.setSteeringFn(mode)
	}
	return nil
}

func (p *fakeProc) SetFollowUpMode(mode string) error {
	if p.setFollowUpFn != nil {
		return p.setFollowUpFn(mode)
	}
	return nil
}

func (p *fakeProc) SetSessionName(name string) error {
	if p.setSessionNameFn != nil {
		return p.setSessionNameFn(name)
	}
	return nil
}

func (p *fakeProc) Compact(instr string) (map[string]any, error) {
	if p.compactFn != nil {
		return p.compactFn(instr)
	}
	return map[string]any{}, nil
}

func (p *fakeProc) SetAutoCompaction(enabled bool) error {
	if p.setAutoCompactFn != nil {
		return p.setAutoCompactFn(enabled)
	}
	return nil
}

func (p *fakeProc) ExportHTML(path string) (string, error) {
	if p.exportHTMLFn != nil {
		return p.exportHTMLFn(path)
	}
	return path, nil
}

func (p *fakeProc) Dispose() {
	p.mu.Lock()
	p.disposed++
	hook := p.disposeHook
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (p *fakeProc) disposeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.disposed
}

// --- assertions ---

func str(v any) string {
	s, _ := v.(string)
	return s
}

// norm round-trips a value through JSON, so a typed struct and a map literal compare equal.
func norm(t testing.TB, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return out
}

// jsonEqual is assert.deepEqual over JSON values.
func jsonEqual(t testing.TB, got, want any, msg ...any) {
	t.Helper()
	g, w := norm(t, got), norm(t, want)
	if !reflect.DeepEqual(g, w) {
		gb, _ := json.MarshalIndent(g, "", "  ")
		wb, _ := json.MarshalIndent(w, "", "  ")
		t.Fatalf("%v\n got: %s\nwant: %s", msg, gb, wb)
	}
}

// eventually polls cond (the JS tests await a macrotask; the Go port runs turns on goroutines).
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// settle waits for the session's emit queue to drain: it publishes context usage on a session,
// which flushes every queued update (used where the JS tests `await` a timer).
func settle(t testing.TB, s *Session) {
	t.Helper()
	s.flushEmits()
}

// newTestSession is `new PiAcpSession({sessionId:'s1', cwd, mcpServers:[], proc, conn, fileCommands:[]})`.
func newTestSession(cwd string, proc Proc, conn Conn, cmds ...FileSlashCommand) *Session {
	return NewSession(SessionOptions{SessionID: "s1", Cwd: cwd, McpServers: []any{}, Proc: proc, Conn: conn, FileCommands: cmds})
}

func wait(t testing.TB, ch <-chan TurnResult) TurnResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("prompt turn did not finish")
		return TurnResult{}
	}
}

func settledTurn(proc *fakeProc) {
	proc.emit(Event{"type": "agent_start"})
	proc.emit(Event{"type": "turn_end"})
	proc.emit(Event{"type": "agent_end"})
	proc.emit(Event{"type": "agent_settled"})
}

func tmp(t testing.TB) string {
	t.Helper()
	return t.TempDir()
}

// promptsOf waits until pi has received n prompts: the session hands a prompt to pi on its own
// goroutine, so a test that has only seen the turn settle cannot assume the call was recorded.
func promptsOf(t testing.TB, p *fakeProc, n int) []promptCall {
	t.Helper()
	eventually(t, "the prompt to reach pi", func() bool { return len(p.promptList()) >= n })
	return p.promptList()
}
