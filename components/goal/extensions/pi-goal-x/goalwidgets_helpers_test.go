package pi_goal_x

// Port of tests/tui-test-utils.ts: mock Theme, TUI and ExtensionContext for rendering widgets without a terminal.

import (
	"sync"
	"testing"
)

// ── Mock Theme ──

type wgTheme struct {
	// seen records every fg(color, value) call when non-nil.
	seen *[][2]string
}

func (th wgTheme) fg(color, value string) string {
	if th.seen != nil {
		*th.seen = append(*th.seen, [2]string{color, value})
	}
	return value
}
func (th wgTheme) bold(value string) string { return value }

func createMockTheme() goalTheme { return wgTheme{} }

// ── Mock TUI ──

type mockTUIState struct {
	getShowHardwareCursor      bool
	setShowHardwareCursorCalls []bool
	requestRenderCalls         int
}

type mockTUI struct {
	state *mockTUIState
	rows  *int
}

func (m *mockTUI) getShowHardwareCursor() bool { return m.state.getShowHardwareCursor }
func (m *mockTUI) setShowHardwareCursor(enabled bool) {
	m.state.setShowHardwareCursorCalls = append(m.state.setShowHardwareCursorCalls, enabled)
}
func (m *mockTUI) requestRender() { m.state.requestRenderCalls++ }

// terminalRows is `terminal.rows`; the mock exposes it only when created with terminalRows.
func (m *mockTUI) terminalRows() (int, bool) {
	if m.rows == nil {
		return 0, false
	}
	return *m.rows, true
}

func createMockTUI(terminalRows ...int) (*mockTUI, *mockTUIState) {
	state := &mockTUIState{}
	m := &mockTUI{state: state}
	if len(terminalRows) > 0 {
		m.rows = intPtr(terminalRows[0])
	}
	return m, state
}

// ── Mock ctx.ui.custom() ──

type customCallRecord struct {
	factory goalCustomFactory
	options goalCustomOptions
}

type mockUI struct {
	mu          sync.Mutex
	customCalls []customCallRecord
	called      chan struct{}
	release     chan struct{}
}

func (u *mockUI) custom(factory goalCustomFactory, opts goalCustomOptions) any {
	u.mu.Lock()
	u.customCalls = append(u.customCalls, customCallRecord{factory, opts})
	u.mu.Unlock()
	u.called <- struct{}{}
	// The promise resolves when the test calls done: the mock never resolves it by itself.
	<-u.release
	return nil
}
func (u *mockUI) setWorkingVisible(bool)                               {}
func (u *mockUI) onTerminalInput(func(string) *goalInputResult) func() { return func() {} }
func (u *mockUI) notify(string, string)                                {}
func (u *mockUI) input(string, string) (string, bool)                  { return "", false }

type mockExtensionContext struct {
	ui_    *mockUI
	hasUI_ bool
}

func (c *mockExtensionContext) hasUI() bool { return c.hasUI_ }
func (c *mockExtensionContext) cwd() string { return "/test" }
func (c *mockExtensionContext) ui() goalUI  { return c.ui_ }

// createMockExtensionContext is a context configured for TUI rendering (hasUI = true) that records every custom() call.
func createMockExtensionContext(t *testing.T) *mockExtensionContext {
	u := &mockUI{called: make(chan struct{}, 16), release: make(chan struct{})}
	t.Cleanup(func() { close(u.release) })
	return &mockExtensionContext{ui_: u, hasUI_: true}
}

// startCustom runs fn (which calls ctx.ui.custom and blocks) in the background and returns once custom() was called.
func (c *mockExtensionContext) startCustom(fn func()) {
	go fn()
	<-c.ui_.called
}

func (c *mockExtensionContext) customCalls() []customCallRecord {
	c.ui_.mu.Lock()
	defer c.ui_.mu.Unlock()
	return append([]customCallRecord{}, c.ui_.customCalls...)
}

// invokeCustomFactory invokes the captured factory with a mock TUI and theme and returns the created component.
func invokeCustomFactory(t *testing.T, calls []customCallRecord, index int) (goalComponent, *mockTUI, goalTheme) {
	t.Helper()
	if index >= len(calls) {
		t.Fatalf("No custom() call at index %d", index)
	}
	tui, _ := createMockTUI()
	theme := createMockTheme()
	component := calls[index].factory(tui, theme, func(any) {})
	return component, tui, theme
}

// renderComponent renders a component at a given width and returns the lines.
func renderComponent(c goalComponent, width int) []string { return c.Render(width) }

// ── Record builders shared by the widget twins ──

// wgObj builds an ordered object from key, value pairs.
func wgObj(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// wgTask is a GoalTask; kv are extra ordered keys (completedAt, subtasks, verificationContract...).
func wgTask(id, title, status string, kv ...any) *jsObject {
	return wgObj(append([]any{"id", id, "title", title, "status", status}, kv...)...)
}

const wgProposedAt = "2026-01-01T00:00:00.000Z"

func wgTaskList(tasks ...any) *jsObject {
	return wgObj("tasks", append([]any{}, tasks...), "blockCompletion", false, "proposedAt", wgProposedAt)
}

// with returns g with the given keys set (the TS `{...g, ...overrides}`).
func with(g *jsObject, kv ...any) *jsObject {
	c := g.clone()
	for i := 0; i+1 < len(kv); i += 2 {
		c.set(kv[i].(string), kv[i+1])
	}
	return c
}
