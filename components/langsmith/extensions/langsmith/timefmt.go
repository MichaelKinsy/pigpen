package langsmith

// Port of run_trees.js getMicrosecondPrecisionDatestring / convertToDottedOrderFormat / stripNonAlphanumeric
// (langsmith 0.10.5). OWNER: wire lane.

// nowMs is Date.now().
func nowMs() int64 { panic("unported: timefmt.go nowMs") }

// jsISOString is new Date(epochMs).toISOString() (UTC, millisecond precision, always `Z`).
func jsISOString(epochMs int64) string { panic("unported: timefmt.go jsISOString") }

// getMicrosecondPrecisionDatestring is run_trees.js:24-28: the ISO string without its final `Z`, then the
// execution order as three digits (`executionOrder.toFixed(0).slice(0, 3).padStart(3, "0")`), then `Z`.
func getMicrosecondPrecisionDatestring(epochMs int64, executionOrder int) string {
	panic("unported: timefmt.go getMicrosecondPrecisionDatestring")
}

// convertToDottedOrderFormat is run_trees.js:29-35: dottedOrder is the datestring without `-`, `:` and `.`
// followed by runID.
func convertToDottedOrderFormat(epochMs int64, runID string, executionOrder int) (dottedOrder, microsecondPrecisionDatestring string) {
	panic("unported: timefmt.go convertToDottedOrderFormat")
}
