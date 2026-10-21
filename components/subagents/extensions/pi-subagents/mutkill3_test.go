package pi_subagents

import (
	"strings"
	"testing"
)

func TestExecutionProjectionOfDetachedAndTimedOutRuns(t *testing.T) {
	eq(t, buildExecutionProjection(runProjectionInput{Detached: true}), executionProjection{Status: "detached", Detached: true}, "detached is not completed")
	eq(t, buildExecutionProjection(runProjectionInput{Stopped: true}), executionProjection{Status: "stopped", Stopped: true}, "stopped")
	eq(t, buildExecutionProjection(runProjectionInput{TimedOut: true}), executionProjection{Status: "failed", TimedOut: true}, "a timed out run exiting 0 failed")
	eq(t, buildExecutionProjection(runProjectionInput{}), executionProjection{Status: "completed", Success: true}, "completed")
}

func TestFastModeRewritesBothPayloadShapes(t *testing.T) {
	eq(t, rewriteFastModeProviderRequest(map[string]any{"a": 1}), any(map[string]any{"a": 1, "service_tier": "priority"}), "map")
	obj := rewriteFastModeProviderRequest(jo("a", float64(1))).(*jsObject)
	eq(t, marshalJSON(obj, ""), `{"a":1,"service_tier":"priority"}`, "object")
	eq(t, rewriteFastModeProviderRequest("text"), any("text"), "other payloads stay")
}

func TestChildPromptStructuredOutputInstructions(t *testing.T) {
	with := rewriteSubagentPrompt("base", rewriteOptions{StructuredOutput: true})
	without := rewriteSubagentPrompt("base", rewriteOptions{})
	eq(t, strings.Contains(with, structuredOutputInstructions), true, "structured")
	eq(t, strings.Contains(without, structuredOutputInstructions), false, "plain")
}

func TestAppendRequestsWithTheSameTimeAreOrderedByID(t *testing.T) {
	reqs := []ChainAppendRequest{{ID: "b", CreatedAt: 1}, {ID: "a", CreatedAt: 1}, {ID: "c", CreatedAt: 0}}
	sortAppendRequests(reqs)
	eq(t, []string{reqs[0].ID, reqs[1].ID, reqs[2].ID}, []string{"c", "a", "b"}, "order")
}

func TestDynamicFanoutAllowsExactlyMaxItems(t *testing.T) {
	step := dfBase(t, func(s *jsObject) { s.obj("expand").set("maxItems", float64(2)) })
	if _, err := materializeDynamicParallelStep(step, dfOutputs(t, dfTargets), 1, DynamicFanoutConfig{}); err != nil {
		t.Errorf("two items under maxItems 2: %v", err)
	}
}

func TestDelegationTerminalResponseIsEmittedOnce(t *testing.T) {
	events := &fakeEvents{}
	b := newBridge(events, nil)
	var got recorder
	events.On(subagentDelegationResponseEvent, got.add)
	b.emitTerminal("k", map[string]any{"status": "first"})
	b.emitTerminal("k", map[string]any{"status": "second"})
	eq(t, len(got.list), 1, "one terminal response per attempt")
	eq(t, got.list[0]["status"], any("first"), "the first")
}
