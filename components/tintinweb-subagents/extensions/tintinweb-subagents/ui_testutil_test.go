package tintinweb_subagents

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Test stand-ins for the original's theme literals and the fakes of its UI contexts.

// fnTheme is a theme built from functions, as the original's `{ fg, bold, getBgAnsi, getColorMode }` literals.
type fnTheme struct {
	fg   func(color, text string) string
	bold func(text string) string
	bg   func(color string) string
	mode string
}

func (t fnTheme) Fg(color, text string) string { return t.fg(color, text) }
func (t fnTheme) Bold(text string) string      { return t.bold(text) }
func (t fnTheme) GetColorMode() string         { return t.mode }
func (t fnTheme) GetBgAnsi(color string) string {
	if t.bg == nil {
		return ""
	}
	return t.bg(color)
}

// tagTheme: fg(c, s) = `<c>s</c>`, bold(s) = `*s*`, getBgAnsi(c) = `<c>`, truecolor.
func tagTheme() fnTheme {
	return fnTheme{
		fg:   func(c, s string) string { return "<" + c + ">" + s + "</" + c + ">" },
		bold: func(s string) string { return "*" + s + "*" },
		bg:   func(c string) string { return "<" + c + ">" },
		mode: "truecolor",
	}
}

// plainTheme: styles nothing.
func plainTheme() fnTheme {
	return fnTheme{fg: func(_, s string) string { return s }, bold: func(s string) string { return s }, mode: "truecolor"}
}

// ansiTheme: every foreground is `\x1b[38;5;240m`, bold is SGR 1.
func ansiTheme() fnTheme {
	return fnTheme{
		fg:   func(_, s string) string { return "\x1b[38;5;240m" + s + "\x1b[0m" },
		bold: func(s string) string { return "\x1b[1m" + s + "\x1b[22m" },
		mode: "truecolor",
	}
}

func boolp(b bool) *bool { return &b }
func intp(n int) *int    { return &n }

var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiSGR.ReplaceAllString(s, "") }

func eqStr(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func hasStr(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%q does not contain %q", got, want)
	}
}

func lacksStr(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Errorf("%q contains %q", got, want)
	}
}

func assertAllLinesFit(t *testing.T, lines []string, width int) {
	t.Helper()
	for i, l := range lines {
		if vw := widthx.VisibleWidth(l); vw > width {
			t.Errorf("line %d exceeds width (%d > %d): %q", i, vw, width, l)
		}
	}
}

// fixedList is an agentLister over a slice the test can change, counting the scans.
type fixedList struct {
	mu     sync.Mutex
	agents []*agentView
	calls  int
}

func (l *fixedList) listAgents() []*agentView {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.agents
}

// fakeWidgetUI records what a widget does to its UI.
type fakeWidgetUI struct {
	mu        sync.Mutex
	render    widgetRender
	placement string
	statuses  []string // "<nil>" for a clear
	cleared   int
	rerender  int
}

func (u *fakeWidgetUI) SetStatus(key string, text *string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if text == nil {
		u.statuses = append(u.statuses, "<nil>")
	} else {
		u.statuses = append(u.statuses, *text)
	}
}

func (u *fakeWidgetUI) SetWidget(key string, render widgetRender, placement string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if render == nil {
		u.render = nil
		u.cleared++
		return
	}
	u.render, u.placement = render, placement
}

func (u *fakeWidgetUI) RequestRender() { u.mu.Lock(); u.rerender++; u.mu.Unlock() }

// frame renders the registered widget at width columns, "" when nothing is registered.
func (u *fakeWidgetUI) frame(width int, th uiTheme) string {
	u.mu.Lock()
	r := u.render
	u.mu.Unlock()
	if r == nil {
		return ""
	}
	return strings.Join(r(width, th), "\n")
}

func jsonStr(v any) string { return fmt.Sprintf("%+v", v) }
