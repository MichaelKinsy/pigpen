package pi_background_tasks

import "fmt"

// The port does not run everything the original runs yet. Whatever it does not run is refused with this one message,
// which names the original (pinned in provenance.json) as the working alternative: never a silent fallback, never a
// partial run.
const (
	portPackageName  = "@pi-in-go/pigpen-background-tasks"
	originalSpec     = "npm:@vanillagreen/pi-background-tasks@2.2.0"
	refusalFormatFmt = "%s (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG."
)

// refusal is the text of a refused feature.
func refusal(feature string) string {
	return fmt.Sprintf(refusalFormatFmt, portPackageName, feature, originalSpec)
}
