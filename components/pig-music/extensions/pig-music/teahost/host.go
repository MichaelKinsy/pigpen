// Package teahost drives a Bubble Tea v2 model without a terminal.
//
// tea.Program owns the terminal. Inside PiG there is none to own: the extension
// sends line snapshots to the host and receives input chunks. A Host runs the
// model's Init/Update loop on one goroutine of its own, runs every tea.Cmd on its
// own goroutine and feeds the result back as a message, and turns the model's
// View into lines of an exact size.
//
// Quit means "hide the player", not "stop it": the model and everything it holds
// (the music player, the search results) stay alive, and messages keep arriving
// while hidden. A model that returns tea.Quit itself (not inside a batch) is seen
// to quit by the key that asked, whatever the scheduler does; a quit message that
// a batch or another command produces is reported only if it arrives within the
// settle wait. tea.Sequence is not supported: its message type is unexported, so
// a Host cannot unpack it. Use tea.Batch.
package teahost

import (
	"reflect"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// settleWait is how long Input waits for the commands a key started to finish, so
// that a quit command is seen by the key that asked for it. Slower commands (a
// search) go on in the background and deliver their message later.
const settleWait = 30 * time.Millisecond

// Options are the host's callbacks. Both are optional and must not block.
type Options struct {
	// Redraw asks for a new snapshot after the view changed. It is not called while the host is hidden.
	Redraw func()
	// Hide is called when the model quits.
	Hide func()
}

// Host runs one model.
type Host struct {
	opts Options

	actions chan action
	stop    chan struct{}
	done    chan struct{}
	stopMu  sync.Once

	// loop-owned
	model tea.Model

	mu      sync.Mutex
	view    string
	w, h    int
	visible bool
	hideReq bool
	stopped bool
}

type action struct {
	run func()
	ack chan struct{}
}

// New returns a host for model. Start begins the loop.
func New(model tea.Model, opts Options) *Host {
	return &Host{
		opts: opts, model: model, visible: true,
		actions: make(chan action), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

// Start runs the model's Init and delivers the window size first.
func (h *Host) Start(width, height int) {
	go h.loop()
	h.Resize(width, height)
	h.do(func() { h.runCmd(h.model.Init(), nil) })
}

func (h *Host) loop() {
	defer close(h.done)
	for {
		select {
		case a := <-h.actions:
			a.run()
			close(a.ack)
		case <-h.stop:
			return
		}
	}
}

// do runs f on the loop and waits for it. It reports false when the host has stopped.
func (h *Host) do(f func()) bool {
	a := action{run: f, ack: make(chan struct{})}
	select {
	case h.actions <- a:
		<-a.ack
		return true
	case <-h.stop:
		return false
	}
}

// Send delivers a message to the model.
func (h *Host) Send(msg tea.Msg) { h.do(func() { h.update(msg, nil) }) }

// Input delivers a key and reports whether the model quit as a result. It waits a
// moment for the commands the key started, so an immediate tea.Quit is reported.
func (h *Host) Input(msg tea.Msg) (quit bool) {
	h.mu.Lock()
	h.hideReq = false
	h.mu.Unlock()
	var wg sync.WaitGroup
	if !h.do(func() { h.update(msg, &wg) }) {
		return false
	}
	settled := make(chan struct{})
	go func() { wg.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(settleWait):
	case <-h.stop:
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hideReq
}

// Resize tells the model the new size and waits until it has taken it.
func (h *Host) Resize(width, height int) {
	h.do(func() {
		h.mu.Lock()
		h.w, h.h = width, height
		h.mu.Unlock()
		h.update(tea.WindowSizeMsg{Width: width, Height: height}, nil)
	})
}

// Lines returns the model's view as exactly height lines of exactly width cells,
// clipped and padded without breaking styles. A different size from the last is
// delivered to the model first.
func (h *Host) Lines(width, height int) []string {
	h.mu.Lock()
	same := h.w == width && h.h == height
	h.mu.Unlock()
	if !same {
		h.Resize(width, height)
	}
	h.mu.Lock()
	view := h.view
	h.mu.Unlock()
	return fit(view, width, height)
}

// Hide stops redraw requests; the model keeps running.
func (h *Host) Hide() {
	h.mu.Lock()
	h.visible = false
	h.mu.Unlock()
}

// Show resumes redraw requests and asks for one now.
func (h *Host) Show() {
	h.mu.Lock()
	h.visible = true
	h.hideReq = false
	h.mu.Unlock()
	if h.opts.Redraw != nil {
		h.opts.Redraw()
	}
}

// Stop ends the loop. Commands still running are dropped when they finish.
func (h *Host) Stop() {
	h.stopMu.Do(func() {
		h.mu.Lock()
		h.stopped = true
		h.mu.Unlock()
		close(h.stop)
	})
	<-h.done
}

// update runs on the loop.
func (h *Host) update(msg tea.Msg, wg *sync.WaitGroup) {
	if _, ok := msg.(tea.QuitMsg); ok {
		h.quit()
		return
	}
	var cmd tea.Cmd
	changed := true
	func() {
		defer func() { _ = recover() }() // a panicking model must not take the extension down
		h.model, cmd = h.model.Update(msg)
		content := h.model.View().Content
		h.mu.Lock()
		changed = content != h.view
		h.view = content
		h.mu.Unlock()
	}()
	if isQuit(cmd) {
		// Seen here, on the loop, the quit does not wait for a goroutine to be
		// scheduled and deliver tea.QuitMsg, so Input reports it for this key.
		h.quit()
		cmd = nil
	}
	h.runCmd(cmd, wg)
	h.mu.Lock()
	visible := h.visible && !h.stopped
	h.mu.Unlock()
	// A screen that came out the same needs no repaint, and each repaint costs the host (measured: most of the CPU of the pulse).
	if visible && changed && h.opts.Redraw != nil {
		h.opts.Redraw()
	}
}

// quit records that the model asked to quit (hide). It runs on the loop.
func (h *Host) quit() {
	h.mu.Lock()
	h.hideReq = true
	h.mu.Unlock()
	if h.opts.Hide != nil {
		h.opts.Hide()
	}
}

// quitCmd is tea.Quit's code pointer: a tea.Cmd is a func and cannot be compared.
var quitCmd = reflect.ValueOf(tea.Quit).Pointer()

func isQuit(cmd tea.Cmd) bool { return cmd != nil && reflect.ValueOf(cmd).Pointer() == quitCmd }

// runCmd starts cmd on its own goroutine; a batch starts each of its commands.
func (h *Host) runCmd(cmd tea.Cmd, wg *sync.WaitGroup) {
	if cmd == nil {
		return
	}
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		var msg tea.Msg
		func() {
			defer func() { _ = recover() }()
			msg = cmd()
		}()
		switch m := msg.(type) {
		case nil:
		case tea.BatchMsg:
			h.do(func() {
				for _, c := range m {
					h.runCmd(c, wg)
				}
			})
		default:
			h.do(func() { h.update(m, wg) })
		}
	}()
}

// fit clips and pads the view's lines to exactly width cells and height lines.
func fit(view string, width, height int) []string {
	if width < 1 || height < 1 {
		return nil
	}
	var src []string
	if view != "" {
		src = splitLines(view)
	}
	out := make([]string, height)
	for i := range out {
		line := ""
		if i < len(src) {
			line = src[i]
		}
		if w := ansi.StringWidth(line); w > width {
			line = ansi.Truncate(line, width, "")
		} else if w < width {
			line += spaces(width - w)
		}
		out[i] = line
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, trimCR(s[start:i]))
			start = i + 1
		}
	}
	return append(lines, trimCR(s[start:]))
}

func trimCR(s string) string {
	if n := len(s); n > 0 && s[n-1] == '\r' {
		return s[:n-1]
	}
	return s
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func visibleWidth(s string) int { return ansi.StringWidth(s) }
