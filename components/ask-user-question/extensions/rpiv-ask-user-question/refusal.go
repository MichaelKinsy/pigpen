package ask_user_question

import "fmt"

// What the port does not do yet is refused with this message, which names the working alternative, never silently
// skipped. upstream: n/a (Pigpen's refusal convention).
const (
	packageName  = "@pi-in-go/pigpen-ask-user-question"
	originalSpec = "npm:@juicesharp/rpiv-ask-user-question@2.11.0"
)

func unsupported(feature string) error {
	return fmt.Errorf("%s (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG.", packageName, feature, originalSpec)
}
