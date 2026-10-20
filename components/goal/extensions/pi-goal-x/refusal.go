package pi_goal_x

// What this port does not do yet is refused, never ignored. The message names the working alternative: the original, which
// runs unchanged in PiG. Package name and version come from package.json and provenance.json.
const (
	packageName      = "@pi-in-go/pigpen-goal"
	originalSpec     = "npm:pi-goal-x@0.32.3"
	refusalDirective = "Install the original extension, " + originalSpec + ", which runs unchanged in PiG."
)

func refusal(feature string) string {
	return packageName + " (Go port) does not support " + feature + " yet. " + refusalDirective
}

const continuationFeature = "automatic goal continuation (maxAutonomousRuns other than 0)"
