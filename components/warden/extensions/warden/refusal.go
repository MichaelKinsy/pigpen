package warden

import (
	"fmt"
	"strings"
)

// originalSpec is the unmodified TypeScript extension this port is held against (port/oracle/package.json).
const originalSpec = "npm:pi-warden@0.80.0"

// refusal is the one message for every upstream feature this port does not carry yet.
func refusal(feature string) string {
	return fmt.Sprintf("@pi-in-go/pigpen-warden (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG.", feature, originalSpec)
}

// unportedAction maps the first words of /warden args to the upstream action that is not ported, or "".
// Upstream's list: status, rules, report, enable, disable, mode, config, test, trace, init, audit, index, prefs,
// loops, recommend, unmute, bench (extension.ts), plus the two-word rules check, audit, calibrate and tune.
func unportedAction(sub string, rest []string) string {
	switch sub {
	case "rules":
		if len(rest) > 0 {
			switch strings.ToLower(rest[0]) {
			case "check", "audit", "calibrate", "tune":
				return "/warden rules " + strings.ToLower(rest[0])
			}
		}
		return "/warden rules"
	case "report", "config", "init", "audit", "index", "recommend", "unmute", "bench":
		return "/warden " + sub
	}
	return ""
}
