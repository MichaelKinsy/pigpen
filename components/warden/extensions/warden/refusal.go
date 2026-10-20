package warden

import "fmt"

// originalSpec is the unmodified TypeScript extension this port is held against (port/oracle/package.json).
const originalSpec = "npm:pi-warden@0.80.0"

// refusal is the one message for every upstream feature this port does not carry yet.
func refusal(feature string) string {
	return fmt.Sprintf("@pi-in-go/pigpen-warden (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG.", feature, originalSpec)
}

// unportedAction maps the first words of /warden args to the upstream action that is not ported, or "".
// Upstream's list: status, rules, report, enable, disable, mode, config, test, trace, init, audit, index, prefs,
// loops, recommend, unmute, bench (extension.ts). `rules` (with check, audit, calibrate and tune), `report` and `init`
// are ported (rules_commands.go).
func unportedAction(sub string, rest []string) string {
	switch sub {
	case "config", "audit", "recommend", "unmute", "bench":
		return "/warden " + sub
	}
	return ""
}
