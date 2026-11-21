package pi_subagents

// The parent-session marker (PIG_PARENT_SESSION): what every child carries, and nothing else — this plugin declares
// provenance, not policy (../pig-bg-tasks/DELEGATED-WORKSPACES.md).

import "testing"

func TestChildEnvDeclaresTheParentSession(t *testing.T) {
	t.Cleanup(func() { setParentSessionID("") })

	setParentSessionID("sess-7")
	eq(t, childEnv([]string{"A=1", "PIG_PARENT_SESSION=stale"}, 0, "scout"), []string{
		"A=1",
		"PIG_PARENT_SESSION=sess-7",
		"PIG_SUBAGENT=1",
		"PIG_SUBAGENT_DEPTH=1",
		"PIG_AGENT_ROLE=scout",
	}, "the marker names this session; an inherited one never leaks")

	setParentSessionID("")
	eq(t, childEnv(nil, 0, "scout"), []string{
		"PIG_SUBAGENT=1",
		"PIG_SUBAGENT_DEPTH=1",
		"PIG_AGENT_ROLE=scout",
	}, "without a session to name the marker is omitted")
}
