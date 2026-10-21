package schedule_prompt_test

import (
	"strings"
	"testing"

	schedule_prompt "github.com/MichaelKinsy/pigpen/schedule-prompt"
)

const (
	wantTool    = "@pi-in-go/pigpen-schedule-prompt (Go port) does not support the schedule_prompt tool (scheduling, listing and managing prompts) yet. Install the original extension, npm:pi-schedule-prompt@0.4.1, which runs unchanged in PiG."
	wantCommand = "@pi-in-go/pigpen-schedule-prompt (Go port) does not support the /schedule-prompt command (the Jobs overlay and Settings) yet. Install the original extension, npm:pi-schedule-prompt@0.4.1, which runs unchanged in PiG."
)

// Every action of the tool is refused with the program's message, as a tool error: nothing is scheduled, stored or listed.
func TestToolRefusesEveryAction(t *testing.T) {
	for _, action := range []string{"add", "remove", "list", "enable", "disable", "update", "cleanup"} {
		t.Run(action, func(t *testing.T) {
			cwd := t.TempDir()
			h := StartHost(t, schedule_prompt.Extension(), HostOptions{Cwd: cwd})
			_, failure := h.Tool("schedule_prompt", map[string]any{"action": action, "schedule": "+10s", "prompt": "hi", "jobId": "x"})
			if failure != wantTool {
				t.Fatalf("failure = %q, want %q", failure, wantTool)
			}
			if got := h.CallsTo("sendMessage"); len(got) != 0 {
				t.Fatalf("a refused call sent %d messages", len(got))
			}
		})
	}
}

// The command is refused with a notification, never a silent no-op.
func TestCommandRefuses(t *testing.T) {
	h := StartHost(t, schedule_prompt.Extension(), HostOptions{})
	if failure := h.Command("schedule-prompt", ""); failure != "" {
		t.Fatalf("command failed: %s", failure)
	}
	calls := h.CallsTo("ui.notify")
	if len(calls) != 1 {
		t.Fatalf("notify calls = %d, want 1", len(calls))
	}
	if calls[0].Args["message"] != wantCommand || calls[0].Args["level"] != "error" {
		t.Fatalf("notify = %v", calls[0].Args)
	}
	if !strings.HasSuffix(wantCommand, "which runs unchanged in PiG.") {
		t.Fatal("refusal text drifted")
	}
}
