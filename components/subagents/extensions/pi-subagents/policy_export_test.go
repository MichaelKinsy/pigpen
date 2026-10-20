package pi_subagents

import "testing"

// Internals of the run policy for the external host tests (policy_run_test.go).

// ResetRunPolicyState clears the session's spawn counters and registered capability ceilings for one test.
func ResetRunPolicyState(t *testing.T) {
	t.Helper()
	runPolicyMu.Lock()
	runState = &subagentState{}
	runPolicyMu.Unlock()
}

// RegisterCeilingForTest registers a capability ceiling (JSON) for a session until the test ends.
func RegisterCeilingForTest(t *testing.T, session, source, ceilingJSON string) {
	t.Helper()
	v, err := parseJSON([]byte(ceilingJSON))
	if err != nil {
		t.Fatal(err)
	}
	h, err := registerSubagentCapabilityCeiling(session, source, v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.dispose)
}

// SetExtensionConfigForTest replaces the extension config the policy reads (timeoutMs, maxSubagentSpawnsPerSession).
func SetExtensionConfigForTest(t *testing.T, timeoutMs, maxSpawnsPerSession any) {
	t.Helper()
	old := loadExtensionConfig
	t.Cleanup(func() { loadExtensionConfig = old })
	loadExtensionConfig = func() extensionConfig {
		return extensionConfig{TimeoutMs: timeoutMs, MaxSubagentSpawnsPerSession: maxSpawnsPerSession}
	}
}
