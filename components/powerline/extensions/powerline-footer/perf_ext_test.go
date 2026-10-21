package powerline_footer_test

import (
	"strings"
	"testing"
	"time"
)

// The editor profiler through the wire: opt-in, a report that names what was measured, and the A/B switches.

func lastNotification(h *Host) string {
	n := notifications(h)
	return n[len(n)-1]
}

func TestProfilerIsOffByDefault(t *testing.T) {
	h := tuiRig{settings: `{"powerline":{"welcome":false}}`}.start(t)
	h.Command("powerline-perf", "")
	if got := lastNotification(h); got != "info: Set POWERLINE_DEBUG_PERF=1 and reload to enable editor profiling" {
		t.Errorf("notification %q", got)
	}
	h.Command("powerline-perf", "reset")
	if got := lastNotification(h); !strings.Contains(got, "enable editor profiling") {
		t.Errorf("reset without profiling: %q", got)
	}
	for _, n := range notifications(h) {
		if strings.Contains(n, "does not support") {
			t.Errorf("a ported command was refused: %q", n)
		}
	}
}

func TestProfilerReportsTheEditorAndTheWidgets(t *testing.T) {
	t.Setenv("POWERLINE_DEBUG_PERF", "1")
	h := tuiRig{settings: `{"powerline":{"welcome":false}}`}.start(t)
	for _, n := range notifications(h) {
		if strings.Contains(n, "POWERLINE_DEBUG_PERF") {
			t.Errorf("the profiler environment was refused: %q", n)
		}
	}
	editorInput(h, "a")
	editorInput(h, "b")
	waitUntil(t, "two inputs to be measured", func() bool {
		h.Command("powerline-perf", "")
		return strings.Contains(lastNotification(h), "inputs=2")
	})
	report := lastNotification(h)
	for _, want := range []string{
		"Powerline editor perf (",
		"A/B: fast-render=on, editor-chrome=on, widgets=on, bash-widgets=on, last-prompt=on",
		"input.total: n=2",
		"input.base-editor: n=2",
		"editor.render.total: n=",
		"editor.render.base: n=",
		"editor.render.fast-probe: n=",
		"layout.segment-context: n=",
		"widget.primary: n=",
		"widget.status: n=",
		"max-draft=",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	h.Command("powerline-perf", "RESET ")
	if got := lastNotification(h); got != "info: Powerline editor performance counters reset" {
		t.Errorf("reset: %q", got)
	}
	h.Command("powerline-perf", "")
	if got := lastNotification(h); !strings.Contains(got, "inputs=0, editor-renders=0, renders/input=0.00") {
		t.Errorf("counters after reset:\n%s", got)
	}
}

func TestProfilerSwitchesTurnFeaturesOff(t *testing.T) {
	t.Setenv("POWERLINE_DEBUG_PERF", "1")
	t.Setenv("POWERLINE_PERF_WIDGETS", "0")
	t.Setenv("POWERLINE_PERF_EDITOR_CHROME", "0")
	t.Setenv("POWERLINE_PERF_FAST_RENDER", "0")
	h := tuiRig{settings: `{"powerline":{"welcome":false}}`}.start(t)
	for _, c := range h.CallsTo("ui.setWidget") {
		if c.Args["key"] == "powerline-top" && c.Args["content"] != nil {
			t.Errorf("the primary widget was installed with the widgets switched off: %v", c.Args)
		}
	}
	h.Command("powerline-perf", "")
	if got := lastNotification(h); !strings.Contains(got, "A/B: fast-render=off, editor-chrome=off, widgets=off, bash-widgets=on, last-prompt=on") {
		t.Errorf("report:\n%s", got)
	}
}

func TestProfilerBashWidgetSwitchKeepsTheTranscriptWidgetOff(t *testing.T) {
	t.Setenv("POWERLINE_DEBUG_PERF", "1")
	t.Setenv("POWERLINE_PERF_BASH_WIDGETS", "0")
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	b.setText("echo hidden")
	editorInput(b.h, "\r")
	waitUntil(t, "the command to be taken", func() bool { return b.draft() == "" })
	time.Sleep(300 * time.Millisecond)
	for _, c := range b.h.CallsTo("ui.setWidget") {
		if c.Args["key"] == "powerline-bash-transcript" && c.Args["content"] != nil {
			t.Errorf("the transcript widget was drawn with its switch off: %v", c.Args["content"])
		}
	}
	b.h.Command("powerline-perf", "")
	if got := lastNotification(b.h); !strings.Contains(got, "bash-widgets=off") {
		t.Errorf("report:\n%s", got)
	}
}
