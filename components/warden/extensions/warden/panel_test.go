package warden

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func newTestPanel(trace *Trace, unfocus func() bool) *TracePanel {
	return NewTracePanel(trace, bareTheme{}, panelActions{unfocus: unfocus})
}

// twin: tests/panel.test.ts:22
func TestPanelEscapeReleasesFocusAndKeepsThePanelOpenWhenTheHandleCanUnfocus(t *testing.T) {
	unfocused := 0
	p := newTestPanel(newTrace(10), func() bool { unfocused++; return true })
	res, err := p.HandleInput("\x1b")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "unfocused", unfocused, 1)
	eq(t, "done", res.Done, false)
}

// twin: tests/panel.test.ts:34
func TestPanelEscapeClosesThePanelWhenTheHandleCannotUnfocus(t *testing.T) {
	for name, unfocus := range map[string]func() bool{"no handle": func() bool { return false }, "no function": nil} {
		p := newTestPanel(newTrace(10), unfocus)
		res, err := p.HandleInput("\x1b")
		if err != nil || !res.Done {
			t.Errorf("%s: %v %v", name, res, err)
		}
	}
}

func TestTracePanelKeysAndRendering(t *testing.T) {
	trace := newTrace(100)
	for i := 0; i < 40; i++ {
		trace.Push(testTraceEntry("warden · bash · irreversible 0.20 · allow", "ran: npm test", "jev: irreversible 0.20"))
	}
	renders := 0
	p := newTestPanel(trace, func() bool { return true })
	p.SetInvalidate(func() { renders++ })
	p.SetViewport(12)
	p.SetFocused(true)
	lines := p.Render(60)
	if len(lines) != 12 {
		t.Fatalf("%d rows for a 12-row viewport", len(lines))
	}
	if !strings.Contains(lines[0], "pi-warden trace · 40 events") || !strings.Contains(lines[1], "d compact") {
		t.Errorf("header %q %q", lines[0], lines[1])
	}
	if !strings.Contains(lines[11], "more below") {
		t.Errorf("footer %q", lines[11])
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "│ ") {
			t.Errorf("no border: %q", l)
		}
	}
	for _, key := range []string{"j", "\x1b[B", "\x1b[6~", "\x1b[F", "\x1b[H", "k", "\x1b[A", "\x1b[5~"} {
		if res, _ := p.HandleInput(key); res.Done {
			t.Errorf("%q closed the panel", key)
		}
	}
	if renders != 8 {
		t.Errorf("each scroll key asks for one render, got %d", renders)
	}
	p.HandleInput("d") // detailed: more lines per entry
	if detailed := p.Render(60); !strings.Contains(detailed[1], "d detailed") {
		t.Errorf("mode label %q", detailed[1])
	}
	p.HandleInput("\x1b[F") // End
	if end := p.Render(60); !strings.Contains(end[11], "above") {
		t.Errorf("scrolled footer %q", end[11])
	}
	if p.HandleInput("c"); len(trace.Entries()) != 0 {
		t.Error("c clears the trace")
	}
	if empty := p.Render(60); !strings.Contains(strings.Join(empty, "\n"), "No guarded activity yet this session") {
		t.Errorf("empty trace: %q", empty)
	}
	if res, _ := p.HandleInput("q"); !res.Done {
		t.Error("q closes")
	}
	if res, _ := p.HandleInput("\x03"); !res.Done {
		t.Error("ctrl+c closes")
	}
	// A pushed entry asks for a render without the panel polling.
	before := renders
	trace.Push(testTraceEntry("x"))
	if renders != before+1 {
		t.Errorf("a push should invalidate once: %d -> %d", before, renders)
	}
	p.Dispose()
	trace.Push(testTraceEntry("y"))
	if renders != before+1 {
		t.Error("a disposed panel still listens")
	}
}

func TestTracePanelMouse(t *testing.T) {
	p := newTestPanel(newTrace(10), func() bool { return true })
	focused := 0
	p.actions.focus = func() { focused++ }
	p.SetViewport(5)
	if _, err := p.HandleMouse(sdk.MouseEvent{Type: sdk.MouseWheel, Button: sdk.MouseButtonNone, WheelDelta: 2}); err != nil {
		t.Fatal(err)
	}
	p.HandleMouse(sdk.MouseEvent{Type: sdk.MousePress, Button: sdk.MouseButtonLeft})
	p.HandleMouse(sdk.MouseEvent{Type: sdk.MouseMove, Button: sdk.MouseButtonNone})
	eq(t, "focus", focused, 1)
}

func TestConfigPanelBrowseEditAndSave(t *testing.T) {
	var saved map[string]any
	var order []string
	p := NewConfigPanel(DefaultConfig(), bareTheme{}, panelActions{unfocus: func() bool { return true }}, func(e map[string]any, o []string) error { saved, order = e, o; return nil })
	p.SetViewport(8)
	p.SetFocused(true)
	// The sections the panel skips are not listed, nested objects are headers, lists are left out.
	var labels []string
	for _, e := range p.entries {
		labels = append(labels, strings.Repeat(" ", e.level)+e.label)
		if skippedSections[e.label] && e.level == 0 {
			t.Errorf("%s should be skipped", e.label)
		}
		if e.label == "warnings" || e.label == "skipTools" || e.label == "exclude" || e.label == "files" {
			t.Errorf("list %s listed", e.label)
		}
	}
	if labels[0] != "enabled" || p.entries[0].kind != "boolean" {
		t.Errorf("order %v", labels[:5])
	}
	text := strings.Join(p.Render(60), "\n")
	if !strings.Contains(text, "> enabled ○") || !strings.Contains(text, "pi-warden config") {
		t.Errorf("render:\n%s", text)
	}
	p.HandleInput("\r") // toggle the boolean under the cursor
	if text := strings.Join(p.Render(60), "\n"); !strings.Contains(text, "enabled ●") || !strings.Contains(text, " *") {
		t.Errorf("toggle:\n%s", text)
	}
	// Move to timeoutMs (a number) and edit it.
	for i, e := range p.entries {
		if e.path == "timeoutMs" {
			p.cursor = i
		}
	}
	p.HandleInput("\r")
	for i := 0; i < 4; i++ {
		p.HandleInput("\x7f") // backspace
	}
	for _, c := range "250" {
		p.HandleInput(string(c))
	}
	p.HandleInput("\r")
	// Edit a string with text that is not a number into a number field: the raw text is kept, as upstream does.
	p.HandleInput("s")
	eq(t, "order", order, []string{"enabled", "timeoutMs"})
	eq(t, "saved", saved, map[string]any{"enabled": true, "timeoutMs": 250.0})
	eq(t, "cleared after saving", len(p.Edits()), 0)
	// q while editing types a q; esc cancels.
	p.HandleInput("\r")
	if res, _ := p.HandleInput("q"); res.Done {
		t.Error("q must not close while editing")
	}
	p.HandleInput("\x1b")
	if res, _ := p.HandleInput("q"); !res.Done {
		t.Error("q closes when browsing")
	}
}

func TestJSToNumber(t *testing.T) {
	for in, want := range map[string]float64{"12": 12, " 7 ": 7, "": 0, "  ": 0, "1.5": 1.5, "-2": -2, "1e3": 1000, "0x10": 16, "0b11": 3, "0o17": 15, ".5": 0.5, "5.": 5} {
		if got := panelJSToNumber(in); got != want {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
	for _, in := range []string{"abc", "1_0", "Infinity", "NaN", "0x", "1.2.3", "--1"} {
		if got := panelJSToNumber(in); got == got {
			t.Errorf("%q should be NaN, got %v", in, got)
		}
	}
}
