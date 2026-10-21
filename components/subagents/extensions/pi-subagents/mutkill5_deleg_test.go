package pi_subagents

import (
	"errors"
	"strings"
	"testing"
)

// The strict parts of the delegation request parser that the twins of the prompt-template bridge do not reach.
func TestDelegationRequestLimits(t *testing.T) {
	rejected := func(overrides string) string {
		t.Helper()
		_, failure := parseSubagentDelegationRequest(structuredRequest(t, overrides))
		if failure == nil {
			return ""
		}
		return failure.Error
	}
	eq(t, rejected(`{"requestId":"`+strings.Repeat("a", 256)+`"}`), "", "an identity of 256 characters")
	eq(t, rejected(`{"requestId":"`+strings.Repeat("a", 257)+`"}`), "Delegation requestId must be a non-empty string of at most 256 characters without newlines.", "an identity of 257 characters")
	eq(t, rejected(`{"bogus":1}`), "Unsupported delegation field: bogus.", "an unknown field")
	eq(t, rejected(`{"timeoutMs":2147483647}`), "", "the largest timeout")
	eq(t, rejected(`{"timeoutMs":2147483648}`), "timeoutMs must be <= 2147483647.", "a timeout past the largest")
	eq(t, rejected(`{"result":{"kind":"structured","schema":{"value":"`+strings.Repeat("x", 65536)+`"}}}`), "result.schema exceeds 64 KiB when encoded.", "an oversized schema")
}

// A second request for a logical node that is running is answered duplicate_node and never started.
func TestDelegationRefusesARunningNodeAgain(t *testing.T) {
	events := &fakeDelegationEvents{}
	runs := make(chan struct{}, 2)
	bridge := registerPromptTemplateDelegationBridge(events, func() any { return &delegationContext{Cwd: "/repo"} }, func(_ string, _ *jsObject, done <-chan struct{}, _ any, _ func(*jsObject)) (*jsObject, error) {
		runs <- struct{}{}
		<-done
		return nil, errors.New("aborted")
	})
	defer bridge.dispose()
	started := events.once(delegationStartedEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"first"}`))
	recv(t, started)
	response := events.once(delegationResponseEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"second"}`))
	r := recv(t, response)
	eq(t, r.vals["status"], any("duplicate_node"), "status")
	eq(t, r.vals["requestId"], any("second"), "requestId")
	<-runs
	eq(t, len(runs), 0, "a second execution")
}
