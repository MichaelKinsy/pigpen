package pi_goal_x

import (
	"errors"
	"sync"
	"testing"
)

// Test doubles for the dialog host (ported from tests/tui-test-utils.ts and the host() helpers of goal-rpc-dialogs.test.ts).

type dialogRecord struct {
	title   string
	options []string
}

type mockDialogUI struct {
	mu         sync.Mutex
	customFn   func(factory goalCustomFactory, opts goalCustomOptions) (any, error)
	selectFn   func(title string, options []string) (string, bool, error)
	inputFn    func(title, placeholder string) (string, bool, error)
	dialogs    []dialogRecord
	notices    []string
	workingLog []bool
}

func (u *mockDialogUI) hasCustom() bool { return u.customFn != nil }
func (u *mockDialogUI) hasSelect() bool { return u.selectFn != nil }
func (u *mockDialogUI) hasInput() bool  { return u.inputFn != nil }
func (u *mockDialogUI) custom(f goalCustomFactory, o goalCustomOptions) (any, error) {
	return u.customFn(f, o)
}
func (u *mockDialogUI) selectOption(title string, options []string) (string, bool, error) {
	return u.selectFn(title, options)
}
func (u *mockDialogUI) input(title, placeholder string) (string, bool, error) {
	return u.inputFn(title, placeholder)
}
func (u *mockDialogUI) notify(message, level string)   { u.notices = append(u.notices, message) }
func (u *mockDialogUI) setWorkingVisible(visible bool) { u.workingLog = append(u.workingLog, visible) }
func (u *mockDialogUI) onTerminalInput(func(string) *goalInputResult) func() { return func() {} }

type mockDialogCtx struct {
	uiFlag  bool
	modeStr string
	u       *mockDialogUI
}

func (c *mockDialogCtx) hasUI() bool      { return c.uiFlag }
func (c *mockDialogCtx) mode() string     { return c.modeStr }
func (c *mockDialogCtx) cwd() string      { return "/test" }
func (c *mockDialogCtx) ui() goalDialogUI { return c.u }

const dialogNone = -1

// scriptedHost is host(picks, inputs) of goal-rpc-dialogs.test.ts: an rpc host whose custom yields no result.
type scriptedHost struct {
	ctx    *mockDialogCtx
	picks  []int
	inputs []*string
}

func (h *scriptedHost) dialogs() []dialogRecord { return h.ctx.u.dialogs }

func newScriptedHost(picks []int, inputs ...*string) *scriptedHost {
	h := &scriptedHost{picks: picks, inputs: inputs}
	u := &mockDialogUI{}
	h.ctx = &mockDialogCtx{uiFlag: true, modeStr: "rpc", u: u}
	u.customFn = func(goalCustomFactory, goalCustomOptions) (any, error) { return nil, nil }
	u.selectFn = func(title string, options []string) (string, bool, error) {
		u.dialogs = append(u.dialogs, dialogRecord{title, options})
		if len(h.picks) == 0 {
			return "", false, nil
		}
		i := h.picks[0]
		h.picks = h.picks[1:]
		if i < 0 || i >= len(options) {
			return "", false, nil
		}
		return options[i], true, nil
	}
	u.inputFn = func(title, _ string) (string, bool, error) {
		u.dialogs = append(u.dialogs, dialogRecord{title: title})
		if len(h.inputs) == 0 || h.inputs[0] == nil {
			if len(h.inputs) > 0 {
				h.inputs = h.inputs[1:]
			}
			return "", false, nil
		}
		v := *h.inputs[0]
		h.inputs = h.inputs[1:]
		return v, true, nil
	}
	return h
}

func questionnaireInt(i int) *int { return &i }

// ── theme / tui doubles ───────────────────────────────────────────────────

type plainTestTheme struct{}

func (plainTestTheme) fg(_, text string) string { return text }
func (plainTestTheme) bold(text string) string  { return text }
func (plainTestTheme) bg(_, _ string) string    { return "" }

const ansiStyleOpen = "\x1b[32m"

func ansiStyle(s string) string { return ansiStyleOpen + s + "\x1b[0m" }

type ansiTestTheme struct{}

func (ansiTestTheme) fg(_, text string) string { return ansiStyle(text) }
func (ansiTestTheme) bold(text string) string  { return text }
func (ansiTestTheme) bg(_, _ string) string    { return "" }

type mockDialogTUI struct {
	shown       bool
	setCalls    []bool
	renderCalls int
	rows        *int
	prevLines   *int
}

func (m *mockDialogTUI) getShowHardwareCursor() bool  { return m.shown }
func (m *mockDialogTUI) setShowHardwareCursor(e bool) { m.setCalls = append(m.setCalls, e) }
func (m *mockDialogTUI) requestRender()               { m.renderCalls++ }
func (m *mockDialogTUI) terminalRows() (int, bool) {
	if m.rows == nil {
		return 0, false
	}
	return *m.rows, true
}
func (m *mockDialogTUI) previousLinesLen() (int, bool) {
	if m.prevLines == nil {
		return 0, false
	}
	return *m.prevLines, true
}

// capturedCustom is createMockExtensionContext(): custom() records the factory and never resolves (until the test ends).
type capturedCustom struct {
	ctx     *mockDialogCtx
	factory chan goalCustomFactory
}

func newCapturedCtx(t *testing.T) *capturedCustom {
	c := &capturedCustom{factory: make(chan goalCustomFactory, 4)}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	u := &mockDialogUI{}
	u.customFn = func(f goalCustomFactory, _ goalCustomOptions) (any, error) {
		c.factory <- f
		<-release
		return nil, errors.New("test ended")
	}
	c.ctx = &mockDialogCtx{uiFlag: true, u: u}
	return c
}

// open runs fn (which calls runGoalQuestionnaire) in the background and returns the captured factory.
func (c *capturedCustom) open(fn func(ctx goalDialogContext)) goalCustomFactory {
	go fn(c.ctx)
	return <-c.factory
}

// boundedDialog builds the component against a TUI exposing terminal.rows and previousLines.
func boundedDialog(f goalCustomFactory, rows, baseFrame int, theme dialogTheme) (goalDialogComponent, *mockDialogTUI) {
	m := &mockDialogTUI{rows: &rows, prevLines: &baseFrame}
	return f(m, theme, func(any) {}), m
}
