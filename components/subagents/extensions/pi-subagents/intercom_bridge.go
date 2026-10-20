package pi_subagents

import (
	"regexp"
	"strconv"
	"strings"
)

// The rest of src/intercom/intercom-bridge.ts (the validation, the resolution and the prompt overlay are in policy_launch.go):
// the session and child targets and the bridge diagnostic.

const (
	defaultIntercomTargetPrefix = "subagent-chat"
	piIntercomSessionIDEnv      = "PI_INTERCOM_SESSION_ID"
)

// resolveIntercomSessionTarget is the intercom name of a session: its explicit name, else a runtime-only alias built from the
// intercom runtime id (inside a child, the child's own route; a name is a readable label and need not be unique).
func resolveIntercomSessionTarget(sessionName, sessionID, intercomSessionID string) string {
	if name := jsTrim(sessionName); name != "" {
		return name
	}
	fallback := jsTrim(intercomSessionID)
	if fallback == "" {
		fallback = sessionID
	}
	// The slice length is pi-intercom's resolveIntercomPresenceName (18): a different length leaves the fallback target unresolvable.
	fallback = strings.TrimPrefix(fallback, "session-")
	return defaultIntercomTargetPrefix + "-" + jsSlice(fallback, 18)
}

var (
	intercomTargetUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)
	intercomTargetEdges  = regexp.MustCompile(`^-+|-+$`)
)

func sanitizeIntercomTargetPart(value string) string {
	s := intercomTargetUnsafe.ReplaceAllString(strings.ToLower(jsTrim(value)), "-")
	s = intercomTargetEdges.ReplaceAllString(s, "")
	if s == "" {
		return "agent"
	}
	return s
}

// resolveSubagentIntercomTarget is the stable target of a child session: agent and run, and the step number of an indexed child.
func resolveSubagentIntercomTarget(runID, agent string, index *int) string {
	suffix := ""
	if index != nil {
		suffix = "-" + strconv.Itoa(*index+1)
	}
	return "subagent-" + sanitizeIntercomTargetPart(agent) + "-" + sanitizeIntercomTargetPart(runID) + suffix
}

// intercomBridgeDiagnostic is the original's IntercomBridgeDiagnostic.
type intercomBridgeDiagnostic struct {
	Active                     bool
	Mode                       string
	WantsIntercom              bool
	SupervisorChannelAvailable bool
	ExtensionDir               string
	OrchestratorTarget         string
	Reason                     string
}

// diagnoseIntercomBridge reports whether the bridge would be active (the native supervisor channel needs no external package).
func diagnoseIntercomBridge(global any, context, orchestratorTarget string) intercomBridgeDiagnostic {
	mode, _, _ := intercomBridgeOf(global, nil)
	target := jsTrim(orchestratorTarget)
	reason := intercomInactiveReason(mode, context, target)
	return intercomBridgeDiagnostic{Active: reason == "", Mode: mode, WantsIntercom: mode != "off" && !(mode == "fork-only" && context != "fork"),
		SupervisorChannelAvailable: true, ExtensionDir: nativeIntercomExtensionDir, OrchestratorTarget: target, Reason: reason}
}
