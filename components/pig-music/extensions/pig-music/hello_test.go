package pig_music

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderFillsTheTerminalWithoutOverlongLines(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {200, 60}, {40, 10}, {12, 4}, {11, 4}, {12, 3}, {5, 5}, {1, 1}, {2, 8}, {300, 2}} {
		width, rows := size[0], size[1]
		h := newHello(func() int { return rows })
		for _, in := range []string{"é", "日本語", "\x1b[200~日本語 paste\x1b[201~", "a very long key name that cannot be a key but is echoed anyway", "\x1b[1;5A"} {
			_, _ = h.HandleInput(in)
		}
		lines := h.Render(width)
		if len(lines) != rows {
			t.Errorf("%dx%d: %d lines", width, rows, len(lines))
		}
		for i, line := range lines {
			if len(line) != width {
				t.Errorf("%dx%d: line %d has %d cells: %q", width, rows, i, len(line), line)
			}
			if strings.ContainsFunc(line, func(r rune) bool { return r > 126 || r < 32 }) {
				t.Errorf("%dx%d: line %d has a non-ASCII or control rune: %q", width, rows, i, line)
			}
		}
	}
}

func TestRenderWithoutAHeightUsesTheFallback(t *testing.T) {
	h := newHello(func() int { return 0 })
	if got := len(h.Render(40)); got != fallbackRows {
		t.Fatalf("%d lines, want %d", got, fallbackRows)
	}
	if got := len(newHello(nil).Render(40)); got != fallbackRows {
		t.Fatalf("nil height: %d lines, want %d", got, fallbackRows)
	}
}

func TestRenderOfAZeroWidthIsEmpty(t *testing.T) {
	if lines := newHello(nil).Render(0); len(lines) != 0 {
		t.Fatalf("%q", lines)
	}
}

func TestKeyLogKeepsTheNewestKeysNewestFirst(t *testing.T) {
	h := newHello(func() int { return 60 })
	for _, in := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n"} {
		_, _ = h.HandleInput(in)
	}
	text := strings.Join(h.Render(80), "\n")
	if strings.Contains(text, `"a"`) || strings.Contains(text, `"b"`) || !strings.Contains(text, "keys received 14") {
		t.Errorf("old keys are still shown or the count is wrong:\n%s", text)
	}
	if strings.Index(text, `"n"`) > strings.Index(text, `"m"`) {
		t.Errorf("newest key is not first:\n%s", text)
	}
}

func TestInputClosesOnlyOnQEscapeAndCtrlC(t *testing.T) {
	closing := map[string]bool{"q": true, "\x1b": true, "\x03": true, "\x1b[113u": true, "\x1b[99;5u": true}
	for _, in := range []string{"q", "\x1b", "\x03", "\x1b[113u", "\x1b[99;5u", "Q", "w", "\x1b[A", "\r", " ", "\x1b[113;5u", "\x1bq", "\x1b[?1;2c"} {
		result, err := newHello(nil).HandleInput(in)
		if err != nil {
			t.Fatal(err)
		}
		if result.Done != closing[in] {
			t.Errorf("%q: Done = %v", in, result.Done)
		}
	}
}

func TestKeyReleasesAreNotEchoed(t *testing.T) {
	h := newHello(nil)
	result, _ := h.HandleInput("\x1b[113;1:3u") // release of q
	if result.Done {
		t.Error("a key release closed the screen")
	}
	if keys, _ := h.Counters(); keys != 0 {
		t.Errorf("a release counted as a key: %d", keys)
	}
}

func newFastHello(rows func() int) *hello {
	h := newHello(rows)
	h.tickEvery, h.pollEvery = 20*time.Millisecond, 5*time.Millisecond
	return h
}

func TestTimerInvalidatesWhileAttachedAndStopsAfterDetach(t *testing.T) {
	h := newFastHello(nil)
	var calls atomic.Int64
	h.SetInvalidate(func() { calls.Add(1) })
	waitUntil(t, "three timer redraws", func() bool { return calls.Load() >= 3 })
	h.SetInvalidate(nil)
	settled := calls.Load()
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != settled {
		t.Fatalf("the timer kept running after detach: %d -> %d", settled, got)
	}
	if _, ticks := h.Counters(); ticks < 3 {
		t.Fatalf("ticks = %d", ticks)
	}
}

func TestTimerDoesNotInvalidateWithinOneTick(t *testing.T) {
	h := newHello(nil) // one second per tick
	var calls atomic.Int64
	h.SetInvalidate(func() { calls.Add(1) })
	defer h.SetInvalidate(nil)
	time.Sleep(300 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("%d redraws within 300 ms with a one second timer and an unchanged terminal", got)
	}
}

func TestAChangedHeightInvalidatesBetweenTicks(t *testing.T) {
	var rows atomic.Int64
	rows.Store(24)
	h := newHello(func() int { return int(rows.Load()) })
	h.tickEvery = time.Hour
	h.pollEvery = 5 * time.Millisecond
	var calls atomic.Int64
	h.SetInvalidate(func() { calls.Add(1) })
	defer h.SetInvalidate(nil)
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("redraw with no change")
	}
	rows.Store(40)
	waitUntil(t, "redraw after a height change", func() bool { return calls.Load() == 1 })
}

func TestReattachingRestartsTheTimerAndKeepsTheState(t *testing.T) {
	h := newFastHello(nil)
	var calls atomic.Int64
	h.SetInvalidate(func() { calls.Add(1) })
	waitUntil(t, "a redraw", func() bool { return calls.Load() >= 1 })
	_, _ = h.HandleInput("x")
	h.SetInvalidate(nil)
	before := calls.Load()
	h.SetInvalidate(func() { calls.Add(1) })
	defer h.SetInvalidate(nil)
	waitUntil(t, "a redraw after reattach", func() bool { return calls.Load() > before })
	if keys, _ := h.Counters(); keys != 1 {
		t.Fatalf("keys = %d", keys)
	}
	h.SetInvalidate(nil)
	h.SetInvalidate(nil) // detaching twice is harmless
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestTheBoxEdgesAreDrawnToTheFullWidth(t *testing.T) {
	lines := newHello(func() int { return 10 }).Render(30)
	if want := "+- pig-music hello " + strings.Repeat("-", 30-1-len("+- pig-music hello ")) + "+"; lines[0] != want {
		t.Errorf("top edge %q, want %q", lines[0], want)
	}
	if want := "+" + strings.Repeat("-", 28) + "+"; lines[9] != want {
		t.Errorf("bottom edge %q, want %q", lines[9], want)
	}
	for _, line := range lines[1:9] {
		if !strings.HasPrefix(line, "| ") || !strings.HasSuffix(line, " |") {
			t.Errorf("side edges missing: %q", line)
		}
	}
}
