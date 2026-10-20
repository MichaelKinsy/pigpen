package powerline_footer_test

import (
	"testing"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The unported commands and settings are refused with one message that names the working alternative.

const refusalSuffix = ", which runs unchanged in PiG."

func refusalFor(feature string) string {
	return "@pi-in-go/pigpen-powerline (Go port) does not support " + feature +
		" yet. Install the original extension, npm:pi-powerline-footer@0.19.1" + refusalSuffix
}

func notifications(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("ui.notify") {
		out = append(out, c.Args["level"].(string)+": "+c.Args["message"].(string))
	}
	return out
}

func TestRefusalMessageIsExact(t *testing.T) {
	want := "@pi-in-go/pigpen-powerline (Go port) does not support delayed send yet. Install the original extension, npm:pi-powerline-footer@0.19.1, which runs unchanged in PiG."
	if got := powerline.Refusal("delayed send"); got != want {
		t.Errorf("refusal = %q, want %q", got, want)
	}
}

func TestEveryUnportedCommandIsRefused(t *testing.T) {
	commands := map[string]string{
		"reply":          "quote reply (/reply)",
		"queue":          "the prompt queue (/queue)",
		"bash-mode":      "bash mode (/bash-mode)",
		"bash-reset":     "bash mode (/bash-reset)",
		"powerline-perf": "the editor performance profiler (/powerline-perf)",
		"cd":             "the /cd command",
	}
	for name, feature := range commands {
		t.Run(name, func(t *testing.T) {
			h := tuiRig{}.start(t)
			before := len(notifications(h))
			if failure := h.Command(name, "anything"); failure != "" {
				t.Fatalf("/%s failed: %s", name, failure)
			}
			got := notifications(h)
			if len(got) != before+1 || got[len(got)-1] != "error: "+refusalFor(feature) {
				t.Errorf("/%s notifications = %q, want the refusal %q last", name, got, refusalFor(feature))
			}
		})
	}
}

func TestEveryUnportedSettingIsRefusedAtSessionStart(t *testing.T) {
	settings := map[string]struct{ json, feature string }{
		"welcome":        {`{"powerline":{"welcome":true}}`, "the startup welcome (powerline.welcome)"},
		"sendDelayMs":    {`{"powerline":{"sendDelayMs":1500}}`, "delayed send (powerline.sendDelayMs)"},
		"bashMode":       {`{"bashMode":{"completions":true}}`, "bash mode (bashMode)"},
		"showLastPrompt": {`{"showLastPrompt":true}`, "the last-prompt line (showLastPrompt)"},
	}
	for name, c := range settings {
		t.Run(name, func(t *testing.T) {
			h := tuiRig{settings: c.json}.start(t)
			got := notifications(h)
			want := "warning: " + refusalFor(c.feature)
			if len(got) != 1 || got[0] != want {
				t.Errorf("notifications = %q, want exactly %q", got, want)
			}
		})
	}
}

func TestTheProfilerEnvironmentIsRefused(t *testing.T) {
	t.Setenv("POWERLINE_DEBUG_PERF", "1")
	h := tuiRig{}.start(t)
	got := notifications(h)
	want := "warning: " + refusalFor("the editor performance profiler (POWERLINE_DEBUG_PERF)")
	if len(got) != 1 || got[0] != want {
		t.Errorf("notifications = %q, want exactly %q", got, want)
	}
}

func TestSettingsTheOriginalAlsoIgnoresAreNotRefused(t *testing.T) {
	for name, json := range map[string]string{
		"welcome off":        `{"powerline":{"welcome":false}}`,
		"vibe off":           `{"workingVibe":"off"}`,
		"delay zero":         `{"powerline":{"sendDelayMs":0}}`,
		"native compaction":  `{"powerline":{"queue":{"compactPromptMode":"native"}}}`,
		"last prompt off":    `{"showLastPrompt":false}`,
		"auto follow-up off": `{"powerline":{"autoFollowUp":false}}`,
		"auto follow-up on":  `{"powerline":{"autoFollowUp":true}}`,
		"no settings at all": ``,
		"a preset string":    `{"powerline":"minimal"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := notifications(tuiRig{settings: json}.start(t)); len(got) != 0 {
				t.Errorf("notifications = %q, want none", got)
			}
		})
	}
}

func TestSeveralUnportedSettingsAreEachRefused(t *testing.T) {
	h := tuiRig{settings: `{"powerline":{"sendDelayMs":1000,"autoFollowUp":true},"bashMode":{}}`}.start(t)
	got := notifications(h)
	want := []string{
		"warning: " + refusalFor("delayed send (powerline.sendDelayMs)"),
		"warning: " + refusalFor("bash mode (bashMode)"),
	}
	if len(got) != len(want) {
		t.Fatalf("notifications = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("notification %d = %q, want %q", i, got[i], want[i])
		}
	}
}
