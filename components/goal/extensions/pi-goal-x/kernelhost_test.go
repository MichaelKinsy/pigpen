package pi_goal_x

// kernelHost is the original tests' hand-rolled `ctx`/`pi` for the kernel (core.go, goal_scheduler.go): a host the test scripts and
// reads directly, without the SDK. Twins that drive handlers, tools or commands through the real SDK use goalRig instead
// (goal_rig_test.go); twins of the module-level tests (scheduler, widget lifetime, service) use this.
//
// stale makes every method panic: the original's tests wrap ctx in a Proxy that throws once the context is "stale".

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type kernelWidget struct {
	key   string
	clear bool
	lines []string
}

type kernelSent struct {
	customType string
	content    string
	display    bool
	details    *jsObject
	trigger    bool
	deliverAs  string
}

type kernelHost struct {
	mu        sync.Mutex
	cwd       string
	sessionID string
	noUI      bool
	mode      string
	width     int
	rows      int
	branch    []any
	idle      func() bool
	pending   func() bool
	stale     bool
	signal    context.Context

	notices  []string
	entries  []*jsObject
	statuses [][2]string
	widgets  []kernelWidget
	sent     []kernelSent
	aborts   int
	tools    []string
	// terminal input listeners registered through OnTerminalInput (nil after unsubscribe)
	inputs []func(string) *goalInputResult
	events map[string][]func(any)

	selectFn  func(title string, options []string) (string, bool)
	confirmFn func(title, msg string) bool
	inputFn   func(title, placeholder string) (string, bool)
	customFn  func(factory goalCustomFactory, opts goalCustomOptions) (any, error)
	onSend    func(kernelSent) error
}

func newKernelHost(cwd, session string) *kernelHost {
	return &kernelHost{cwd: cwd, sessionID: session, mode: "tui", width: 100, rows: 40, tools: []string{"read", "write", "bash"}, events: map[string][]func(any){}}
}

func (h *kernelHost) check(what string) {
	if h.stale {
		panic(fmt.Sprintf("stale context access: %s", what))
	}
}

func (h *kernelHost) Cwd() string       { h.check("cwd"); return h.cwd }
func (h *kernelHost) SessionID() string { h.check("sessionManager"); return h.sessionID }
func (h *kernelHost) SessionFile() string {
	h.check("sessionManager")
	return ""
}
func (h *kernelHost) HasUI() bool  { h.check("hasUI"); return !h.noUI }
func (h *kernelHost) Mode() string { h.check("mode"); return h.mode }
func (h *kernelHost) Notify(m, l string) {
	h.check("ui")
	h.notices = append(h.notices, l+": "+m)
}
func (h *kernelHost) IsIdle() bool {
	h.check("isIdle")
	return h.idle == nil || h.idle()
}
func (h *kernelHost) HasPendingMessages() bool {
	h.check("hasPendingMessages")
	return h.pending != nil && h.pending()
}
func (h *kernelHost) Abort() { h.check("abort"); h.aborts++ }
func (h *kernelHost) Signal() context.Context {
	h.check("signal")
	if h.signal != nil {
		return h.signal
	}
	return context.Background()
}
func (h *kernelHost) Branch() []any {
	h.check("sessionManager")
	return append([]any{}, h.branch...)
}
func (h *kernelHost) Model() *auditorModel                { h.check("model"); return nil }
func (h *kernelHost) ModelRegistry() auditorModelRegistry { h.check("modelRegistry"); return nil }
func (h *kernelHost) ContextUsage() *goalContextUsage     { h.check("getContextUsage"); return nil }
func (h *kernelHost) SetStatus(k, t string) {
	h.check("ui")
	h.statuses = append(h.statuses, [2]string{k, t})
}
func (h *kernelHost) SetWidget(k string, lines []string) {
	h.check("ui")
	if lines == nil {
		h.widgets = append(h.widgets, kernelWidget{key: k, clear: true})
		return
	}
	h.widgets = append(h.widgets, kernelWidget{key: k, lines: append([]string{}, lines...)})
}
func (h *kernelHost) Width() int { h.check("width"); return h.width }
func (h *kernelHost) Height() (int, bool) {
	h.check("height")
	return h.rows, h.rows > 0
}
func (h *kernelHost) OnWidthChange(func(int)) func() { h.check("ui"); return func() {} }
func (h *kernelHost) Theme() goalHostTheme           { h.check("ui"); return wgTheme{} }
func (h *kernelHost) Confirm(t, m string) bool {
	h.check("ui")
	return h.confirmFn != nil && h.confirmFn(t, m)
}
func (h *kernelHost) Select(t string, o []string) (string, bool) {
	h.check("ui")
	if h.selectFn == nil {
		return "", false
	}
	return h.selectFn(t, o)
}
func (h *kernelHost) Input(t, p string) (string, bool) {
	h.check("ui")
	if h.inputFn == nil {
		return "", false
	}
	return h.inputFn(t, p)
}
func (h *kernelHost) Custom(f goalCustomFactory, o goalCustomOptions) (any, error) {
	h.check("ui")
	if h.customFn == nil {
		return nil, nil
	}
	return h.customFn(f, o)
}
func (h *kernelHost) OnTerminalInput(cb func(string) *goalInputResult) func() {
	h.check("ui")
	h.inputs = append(h.inputs, cb)
	i := len(h.inputs) - 1
	return func() { h.inputs[i] = nil }
}
func (h *kernelHost) SetWorkingVisible(bool) { h.check("ui") }
func (h *kernelHost) AppendEntry(t string, d *jsObject) {
	h.check("appendEntry")
	h.entries = append(h.entries, d)
	e := newObject()
	e.set("type", "custom")
	e.set("customType", t)
	v, _ := parseJSON([]byte(marshalJSON(d, "")))
	e.set("data", v)
	h.branch = append(h.branch, e)
}
func (h *kernelHost) SendMessage(customType, content string, display bool, details *jsObject, trigger bool, deliverAs string) error {
	h.check("sendMessage")
	m := kernelSent{customType, content, display, details, trigger, deliverAs}
	h.sent = append(h.sent, m)
	if h.onSend != nil {
		return h.onSend(m)
	}
	return nil
}
func (h *kernelHost) SendUserMessage(string, string) error { h.check("sendUserMessage"); return nil }
func (h *kernelHost) GetActiveTools() ([]string, error) {
	h.check("getActiveTools")
	return append([]string{}, h.tools...), nil
}
func (h *kernelHost) SetActiveTools(t []string) {
	h.check("setActiveTools")
	h.tools = append([]string{}, t...)
}
func (h *kernelHost) OnEvent(channel string, fn func(any)) func() {
	h.check("events")
	h.events[channel] = append(h.events[channel], fn)
	i := len(h.events[channel]) - 1
	return func() { h.events[channel][i] = nil }
}

// emit is pi.events.emit(channel, payload).
func (h *kernelHost) emit(channel string, payload any) {
	for _, fn := range h.events[channel] {
		if fn != nil {
			fn(payload)
		}
	}
}

// lastInput is the live terminal input listener.
func (h *kernelHost) lastInput(t *testing.T) func(string) *goalInputResult {
	t.Helper()
	for i := len(h.inputs) - 1; i >= 0; i-- {
		if h.inputs[i] != nil {
			return h.inputs[i]
		}
	}
	t.Fatal("no terminal input listener")
	return nil
}

// pushes is the widget pushes (not clears) so far.
func (h *kernelHost) pushes() []kernelWidget {
	var out []kernelWidget
	for _, w := range h.widgets {
		if !w.clear {
			out = append(out, w)
		}
	}
	return out
}

func focusEntryFor(id string) *jsObject {
	e := newObject()
	e.set("type", "custom")
	e.set("customType", focusEntryType)
	e.set("data", goalFocusDetails(&id, "created"))
	return e
}
