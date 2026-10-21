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
		"powerline-perf": "the editor performance profiler (/powerline-perf)",
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
		"vibe off":           `{"workingVibe":"off"}`,
		"delay zero":         `{"powerline":{"sendDelayMs":0}}`,
		"delay set":          `{"powerline":{"sendDelayMs":1500}}`,
		"bash mode settings": `{"bashMode":{"completions":true}}`,
		"native compaction":  `{"powerline":{"queue":{"compactPromptMode":"native"}}}`,
		"last prompt off":    `{"showLastPrompt":false}`,
		"last prompt on":     `{"showLastPrompt":true}`,
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
