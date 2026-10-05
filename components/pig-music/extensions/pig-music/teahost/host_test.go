package teahost

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// toy is a model whose behavior the tests choose through cmds.
type toy struct {
	log   *[]string
	mu    *sync.Mutex
	w, h  int
	body  string
	onKey func(key string) tea.Cmd
}

type doneMsg string
type ranMsg struct{}

func (m toy) Init() tea.Cmd { return func() tea.Msg { return doneMsg("init") } }

func (m toy) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		*m.log = append(*m.log, "size")
	case tea.KeyPressMsg:
		*m.log = append(*m.log, "key:"+msg.String())
		if m.onKey != nil {
			return m, m.onKey(msg.String())
		}
	case doneMsg:
		m.body = string(msg)
		*m.log = append(*m.log, "done:"+string(msg))
	case ranMsg:
		*m.log = append(*m.log, "ran")
	}
	return m, nil
}

func (m toy) View() tea.View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return tea.NewView(strings.Join([]string{m.body, "second \x1b[31mred\x1b[0m line that is long", "third"}, "\n"))
}

func newToy(onKey func(string) tea.Cmd) (toy, *[]string, *sync.Mutex) {
	var log []string
	mu := &sync.Mutex{}
	return toy{log: &log, mu: mu, onKey: onKey}, &log, mu
}

func snapshot(log *[]string, mu *sync.Mutex) []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), (*log)...)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func contains(log *[]string, mu *sync.Mutex, s string) func() bool {
	return func() bool {
		for _, l := range snapshot(log, mu) {
			if l == s {
				return true
			}
		}
		return false
	}
}

func TestStartRunsInitAndDeliversTheWindowSizeFirst(t *testing.T) {
	m, log, mu := newToy(nil)
	h := New(m, Options{})
	defer h.Stop()
	h.Start(80, 24)
	waitFor(t, "the init command's message", contains(log, mu, "done:init"))
	if got := snapshot(log, mu); got[0] != "size" {
		t.Fatalf("first message %q, want the window size: %v", got[0], got)
	}
}

func TestLinesAreExactlyWidthByHeightWithAnsiClipped(t *testing.T) {
	m, log, mu := newToy(nil)
	h := New(m, Options{})
	defer h.Stop()
	h.Start(20, 6)
	waitFor(t, "init", contains(log, mu, "done:init"))
	lines := h.Lines(20, 6)
	if len(lines) != 6 {
		t.Fatalf("%d lines, want 6", len(lines))
	}
	for i, l := range lines {
		if w := visibleWidth(l); w != 20 {
			t.Errorf("line %d is %d cells: %q", i, w, l)
		}
	}
	if !strings.Contains(lines[1], "\x1b[31mred") || strings.Contains(lines[1], "long") {
		t.Errorf("styled line not clipped at the width with its style kept: %q", lines[1])
	}
	// More rows than the model draws are padded; fewer clip.
	if got := h.Lines(20, 2); len(got) != 2 {
		t.Errorf("clipped to %d rows", len(got))
	}
}

func TestResizeReachesTheModelBeforeLinesReturn(t *testing.T) {
	m, log, mu := newToy(nil)
	h := New(m, Options{})
	defer h.Stop()
	h.Start(80, 24)
	waitFor(t, "init", contains(log, mu, "done:init"))
	before := len(snapshot(log, mu))
	lines := h.Lines(33, 5)
	if len(lines) != 5 || visibleWidth(lines[0]) != 33 {
		t.Fatalf("lines for 33x5: %d lines, first %d cells", len(lines), visibleWidth(lines[0]))
	}
	if after := snapshot(log, mu); len(after) != before+1 || after[len(after)-1] != "size" {
		t.Fatalf("the resize did not reach the model once: %v", after)
	}
	h.Lines(33, 5) // same size: no message
	if len(snapshot(log, mu)) != before+1 {
		t.Error("an unchanged size was sent again")
	}
}

func TestAKeyIsDeliveredAndCommandsRunOffTheLoop(t *testing.T) {
	var release = make(chan struct{})
	m, log, mu := newToy(func(key string) tea.Cmd {
		return func() tea.Msg { <-release; return ranMsg{} }
	})
	h := New(m, Options{})
	defer h.Stop()
	h.Start(40, 10)
	waitFor(t, "init", contains(log, mu, "done:init"))
	start := time.Now()
	if quit := h.Input(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"})); quit {
		t.Fatal("a quit")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Input waited for a slow command (%s)", time.Since(start))
	}
	// The loop is free while the command blocks.
	h.Resize(41, 10)
	close(release)
	waitFor(t, "the command's message", contains(log, mu, "ran"))
	if !contains(log, mu, "key:a")() {
		t.Fatal("the key never arrived")
	}
}

func TestBatchRunsEveryCommandAndNilCommandsAreIgnored(t *testing.T) {
	var n atomic.Int32
	m, log, mu := newToy(func(string) tea.Cmd {
		return tea.Batch(
			func() tea.Msg { n.Add(1); return doneMsg("one") },
			nil,
			tea.Batch(func() tea.Msg { n.Add(1); return doneMsg("two") }, func() tea.Msg { n.Add(1); return nil }),
		)
	})
	h := New(m, Options{})
	defer h.Stop()
	h.Start(40, 10)
	h.Input(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	waitFor(t, "both messages", func() bool { return contains(log, mu, "done:one")() && contains(log, mu, "done:two")() })
	waitFor(t, "all three commands", func() bool { return n.Load() == 3 })
}

func TestQuitHidesAndInputReportsItWhileTheModelKeepsRunning(t *testing.T) {
	var hides atomic.Int32
	m, log, mu := newToy(func(key string) tea.Cmd {
		if key == "q" {
			return tea.Quit
		}
		return nil
	})
	h := New(m, Options{Hide: func() { hides.Add(1) }})
	defer h.Stop()
	h.Start(40, 10)
	waitFor(t, "init", contains(log, mu, "done:init"))
	if !h.Input(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"})) {
		t.Fatal("Input did not report the quit")
	}
	if hides.Load() != 1 {
		t.Fatalf("Hide called %d times", hides.Load())
	}
	// Hiding is not stopping: the model still receives messages.
	h.Send(doneMsg("after"))
	waitFor(t, "a message after the quit", contains(log, mu, "done:after"))
	h.Show()
	if h.Input(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"})) {
		t.Fatal("a quit that did not happen")
	}
}

func TestNoRedrawIsRequestedWhileHiddenAndOneComesWhenShown(t *testing.T) {
	var redraws atomic.Int32
	m, log, mu := newToy(nil)
	h := New(m, Options{Redraw: func() { redraws.Add(1) }})
	defer h.Stop()
	h.Start(40, 10)
	waitFor(t, "init", contains(log, mu, "done:init"))
	waitFor(t, "a redraw while visible", func() bool { return redraws.Load() > 0 })
	h.Hide()
	base := redraws.Load()
	for i := 0; i < 5; i++ {
		h.Send(doneMsg("hidden"))
	}
	waitFor(t, "the hidden messages", func() bool {
		n := 0
		for _, l := range snapshot(log, mu) {
			if l == "done:hidden" {
				n++
			}
		}
		return n == 5
	})
	if got := redraws.Load(); got != base {
		t.Fatalf("%d redraws requested while hidden", got-base)
	}
	h.Show()
	if got := redraws.Load(); got != base+1 {
		t.Fatalf("Show requested %d redraws, want 1", got-base)
	}
}

func TestStopEndsTheLoopAndLaterCallsAreSafe(t *testing.T) {
	m, _, _ := newToy(nil)
	h := New(m, Options{})
	h.Start(40, 10)
	h.Stop()
	h.Stop()
	h.Send(doneMsg("late"))
	if lines := h.Lines(10, 3); len(lines) != 3 {
		t.Fatalf("Lines after Stop returned %d lines", len(lines))
	}
	if h.Input(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"})) {
		t.Fatal("quit after stop")
	}
}

func TestACommandThatFinishesAfterStopIsDropped(t *testing.T) {
	release := make(chan struct{})
	m, _, _ := newToy(func(string) tea.Cmd { return func() tea.Msg { <-release; return doneMsg("late") } })
	h := New(m, Options{})
	h.Start(40, 10)
	h.Input(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	h.Stop()
	close(release)
	time.Sleep(50 * time.Millisecond) // nothing to assert but that it does not panic or block
}

// A quit must not depend on how quickly a goroutine is scheduled: here a message
// that is slow to handle (a player state arriving while the key is handled) takes
// the loop before the quit command can deliver its message, and the overlay still
// has to close on that key, not leave the user pressing q again.
func TestQuitIsReportedEvenWhenTheLoopIsBusyAfterTheKey(t *testing.T) {
	var h *Host
	m, log, mu := newToy(func(key string) tea.Cmd {
		if key != "q" {
			return nil
		}
		queued := make(chan struct{})
		go func() {
			close(queued)
			h.do(func() { time.Sleep(4 * settleWait) })
		}()
		<-queued
		time.Sleep(settleWait / 3) // the slow message is now waiting for the loop, ahead of the quit
		return tea.Quit
	})
	h = New(m, Options{})
	defer h.Stop()
	h.Start(40, 10)
	waitFor(t, "init", contains(log, mu, "done:init"))
	if !h.Input(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"})) {
		t.Fatal("q was lost: Input did not report the quit because the loop was busy for longer than the settle wait")
	}
}

// A panicking model must not take the extension (and, fused into a Piglet Binary,
// PiG itself) down: the key is dropped and the model keeps running.
func TestAPanickingUpdateOrCommandIsContained(t *testing.T) {
	m, log, mu := newToy(func(key string) tea.Cmd {
		switch key {
		case "p":
			panic("update panicked")
		case "c":
			return func() tea.Msg { panic("command panicked") }
		}
		return nil
	})
	h := New(m, Options{})
	defer h.Stop()
	h.Start(40, 10)
	waitFor(t, "init", contains(log, mu, "done:init"))
	h.Input(tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"}))
	h.Input(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
	h.Send(doneMsg("still running"))
	waitFor(t, "a message after the panics", contains(log, mu, "done:still running"))
}

func TestAnUpdateThatChangesNothingOnScreenRequestsNoRedraw(t *testing.T) {
	var redraws atomic.Int32
	m, log, mu := newToy(nil)
	h := New(m, Options{Redraw: func() { redraws.Add(1) }})
	defer h.Stop()
	h.Start(40, 10)
	waitFor(t, "init", contains(log, mu, "done:init"))
	waitFor(t, "the first redraw", func() bool { return redraws.Load() > 0 })
	base := redraws.Load()
	h.Send(ranMsg{}) // the model changes nothing it draws
	h.Send(ranMsg{})
	waitFor(t, "both messages", func() bool {
		n := 0
		for _, l := range snapshot(log, mu) {
			if l == "ran" {
				n++
			}
		}
		return n == 2
	})
	if got := redraws.Load(); got != base {
		t.Errorf("%d redraws for updates that left the screen as it was (every redraw costs the host a repaint)", got-base)
	}
	h.Send(doneMsg("changed")) // the body is drawn
	waitFor(t, "a redraw for a real change", func() bool { return redraws.Load() == base+1 })
}
