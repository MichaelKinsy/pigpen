package pi_permission_system

// The refusal every unported option, mode and path gives. It names the Package and the working alternative, as every Pigpen port
// does: a call the port cannot decide is blocked with this text, never allowed and never partly run.

const (
	// packageName is "name" in ../../package.json.
	packageName = "@pi-in-go/pigpen-permissions"
	// originalSpec is the original extension at the pinned release (provenance.json; port/oracle/packages/pi-permission-system/package.json).
	originalSpec = "npm:@gotgenes/pi-permission-system@39.0.3"
)

// refusal is the text that says the Go port does not support feature yet and where the original runs unchanged.
func refusal(feature string) string {
	return packageName + " (Go port) does not support " + feature + " yet. Install the original extension, " + originalSpec +
		", which runs unchanged in PiG."
}
