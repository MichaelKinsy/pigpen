package pi_subagents

import (
	"strings"
	"testing"
)

func TestExecutionProjectionOfDetachedStoppedAndTimedOutRuns(t *testing.T) {
	eq(t, buildExecutionProjection(runProjectionInput{Detached: true}), executionProjection{Status: "detached", Detached: true}, "detached is not completed")
	eq(t, buildExecutionProjection(runProjectionInput{Stopped: true}), executionProjection{Status: "stopped", Stopped: true}, "stopped")
	eq(t, buildExecutionProjection(runProjectionInput{TimedOut: true}), executionProjection{Status: "failed", TimedOut: true}, "a timed out run exiting 0 failed")
	eq(t, buildExecutionProjection(runProjectionInput{}), executionProjection{Status: "completed", Success: true}, "completed")
}

func TestFastModeRewritesTheObjectPayloadShape(t *testing.T) {
	out := rewriteFastModeProviderRequest(obj("a", float64(1))).(*jsObject)
	eq(t, marshalJSON(out, ""), `{"a":1,"service_tier":"priority"}`, "object")
	eq(t, rewriteFastModeProviderRequest("text"), any("text"), "other payloads stay")
}

func TestChildPromptStructuredOutputInstructions(t *testing.T) {
	with := rewriteSubagentPrompt("base", rewriteOptions{StructuredOutput: true})
	without := rewriteSubagentPrompt("base", rewriteOptions{})
	eq(t, strings.Contains(with, structuredOutputInstructions), true, "structured")
	eq(t, strings.Contains(without, structuredOutputInstructions), false, "plain")
}

func TestDelegationTerminalResponseIsEmittedOncePerAttempt(t *testing.T) {
	events := &fakeDelegationEvents{}
	bridge := registerPromptTemplateDelegationBridge(events, func() any { return nil }, nil)
	defer bridge.dispose()
	var got []*jsObject
	events.On(delegationResponseEvent, func(data any) { got = append(got, data.(*jsObject)) })
	bridge.emitTerminal("k", obj("status", "first"))
	bridge.emitTerminal("k", obj("status", "second"))
	eq(t, len(got), 1, "one terminal response per attempt")
	eq(t, got[0].vals["status"], any("first"), "the first")
}
