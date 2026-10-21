package schedule_prompt

import "fmt"

// What the port does not do yet is refused with one message that names the working alternative, never silently
// skipped or partly run (upstream: n/a, Pigpen's parity-program refusal convention).
const (
	packageName  = "@pi-in-go/pigpen-schedule-prompt"
	originalSpec = "npm:pi-schedule-prompt@0.4.1"
)

func unsupported(feature string) error {
	return fmt.Errorf("%s (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG.", packageName, feature, originalSpec)
}

var errNoSession = fmt.Errorf("schedule_prompt is not ready: the session has not started")
