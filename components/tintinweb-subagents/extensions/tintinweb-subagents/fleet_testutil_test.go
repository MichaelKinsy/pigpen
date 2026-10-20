package tintinweb_subagents

import (
	"sync"
	"time"
)

// fakeHost is the terminal a viewer runs in.
type fakeHost struct {
	mu      sync.Mutex
	rows    int
	renders int
}

func (h *fakeHost) Rows() int { return h.rows }
func (h *fakeHost) RequestRender() {
	h.mu.Lock()
	h.renders++
	h.mu.Unlock()
}
func (h *fakeHost) requested() int { h.mu.Lock(); defer h.mu.Unlock(); return h.renders }

// stubFleetManager is the manager as the fleet list sees it.
type stubFleetManager struct {
	*fixedList
	mu      sync.Mutex
	steered [][2]string
	aborted []string
	session viewerSession
	// abortGate, when set, holds every abort until it is closed (an abort is a round trip to the child).
	abortGate chan struct{}
}

func (m *stubFleetManager) abort(id string) bool {
	if m.abortGate != nil {
		<-m.abortGate
	}
	m.mu.Lock()
	m.aborted = append(m.aborted, id)
	m.mu.Unlock()
	return true
}

func (m *stubFleetManager) steer(id, message string) error {
	m.mu.Lock()
	m.steered = append(m.steered, [2]string{id, message})
	m.mu.Unlock()
	return nil
}

func (m *stubFleetManager) sessionOf(string) viewerSession {
	if m.session != nil {
		return m.session
	}
	return emptySession{}
}

func (m *stubFleetManager) steeredCalls() [][2]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][2]string(nil), m.steered...)
}

// fakeFleetUI is a fleetUI with the overlay and the input handler under the test's control.
type fakeFleetUI struct {
	mu         sync.Mutex
	render     widgetRender
	placement  string
	handler    func(string) *inputResult
	editorText string
	opened     bool
	closed     bool
	done       func()
	component  viewerComponent
	notices    []string
	rerender   int
	host       *fakeHost
	theme      uiTheme
	keys       viewerKeybindings
}

func (u *fakeFleetUI) SetWidget(key string, render widgetRender, placement string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.render, u.placement = render, placement
}

func (u *fakeFleetUI) OnTerminalInput(h func(string) *inputResult) func() {
	u.mu.Lock()
	u.handler = h
	u.mu.Unlock()
	return func() { u.mu.Lock(); u.handler = nil; u.mu.Unlock() }
}

func (u *fakeFleetUI) GetEditorText() string { u.mu.Lock(); defer u.mu.Unlock(); return u.editorText }
func (u *fakeFleetUI) Notify(m, _ string) {
	u.mu.Lock()
	u.notices = append(u.notices, m)
	u.mu.Unlock()
}
func (u *fakeFleetUI) RequestRender() { u.mu.Lock(); u.rerender++; u.mu.Unlock() }

func (u *fakeFleetUI) Custom(factory viewerFactory, _ overlayOptions, onClose func()) {
	host, th := u.host, u.theme
	if host == nil {
		host = &fakeHost{rows: 40}
	}
	if th == nil {
		th = tagTheme()
	}
	u.mu.Lock()
	u.opened = true
	u.mu.Unlock()
	done := func() {
		u.mu.Lock()
		first := u.done != nil
		u.closed, u.done = true, nil
		u.mu.Unlock()
		if first {
			onClose()
		}
	}
	u.mu.Lock()
	u.done = done
	u.mu.Unlock()
	c := factory(host, th, u.keys, done)
	u.mu.Lock()
	u.component = c
	u.mu.Unlock()
}

func (u *fakeFleetUI) press(data string) *inputResult {
	u.mu.Lock()
	h := u.handler
	u.mu.Unlock()
	if h == nil {
		return nil
	}
	return h(data)
}

// manualClock replaces the tickers: a test fires them by hand.
type manualClock struct {
	mu      sync.Mutex
	tickers map[int]func()
	next    int
}

func installManualClock() (*manualClock, func()) {
	c := &manualClock{tickers: map[int]func(){}}
	old := startTicker
	startTicker = func(_ time.Duration, f func()) func() {
		c.mu.Lock()
		id := c.next
		c.next++
		c.tickers[id] = f
		c.mu.Unlock()
		return func() { c.mu.Lock(); delete(c.tickers, id); c.mu.Unlock() }
	}
	return c, func() { startTicker = old }
}

func (c *manualClock) running() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.tickers) }

func (c *manualClock) fire() {
	c.mu.Lock()
	fs := make([]func(), 0, len(c.tickers))
	for _, f := range c.tickers {
		fs = append(fs, f)
	}
	c.mu.Unlock()
	for _, f := range fs {
		f()
	}
}
