package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutionProjectionOfDetachedStoppedAndTimedOutRuns(t *testing.T) {
	eq(t, buildExecutionProjection(runProjectionInput{Detached: true}), executionProjection{Status: "detached", Detached: true}, "detached is not completed")
	eq(t, buildExecutionProjection(runProjectionInput{Stopped: true}), executionProjection{Status: "stopped", Stopped: true}, "stopped")
	eq(t, buildExecutionProjection(runProjectionInput{TimedOut: true}), executionProjection{Status: "failed", TimedOut: true}, "a timed out run exiting 0 failed")
	eq(t, buildExecutionProjection(runProjectionInput{}), executionProjection{Status: "completed", Success: true}, "completed")
}

func TestFastModeRewritesBothPayloadShapes(t *testing.T) {
	in := map[string]any{"a": 1}
	eq(t, rewriteFastModeProviderRequest(in), any(map[string]any{"a": 1, "service_tier": "priority"}), "map")
	eq(t, in, map[string]any{"a": 1}, "the input map is not modified")
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

// A refresh drops the discovery cache: a file edited to the same size with its modification time put back (the discovery
// fingerprint cannot see it) is still read again.
func TestAdvertisementRefreshReadsAnEditThatKeepsSizeAndTime(t *testing.T) {
	e := newMgmtEnv(t)
	a := newAdvertisement()
	a.hasCtx, a.cwd = true, e.dir
	file := filepath.Join(e.dir, ".pi", "agents", "a.md")
	writeFile(t, file, "---\nname: a\ndescription: AAA\nadvertise: true\n---\n\nA.\n")
	a.refresh()
	eq(t, a.snapshot()[0].Description, "AAA", "first description")
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, "---\nname: a\ndescription: BBB\nadvertise: true\n---\n\nA.\n")
	if err := os.Chtimes(file, time.Now(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	eq(t, a.snapshot()[0].Description, "BBB", "the refresh rediscovers from the files")
}

func TestThinkingSuffixOfAModelThatCarriesOne(t *testing.T) {
	eq(t, applyThinkingSuffix("glm-5.2:high", "low", false), "glm-5.2:high", "an existing level stays")
	eq(t, applyThinkingSuffix("glm-5.2:high", "low", true), "glm-5.2:low", "an existing level is replaced on request")
	eq(t, applyThinkingSuffix("glm-5.2", "low", false), "glm-5.2:low", "a level is added")
}
