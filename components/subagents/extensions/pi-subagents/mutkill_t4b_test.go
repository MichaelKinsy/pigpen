package pi_subagents

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Tests for the survivors of the mutations of the prompt-template delegation bridge, the validate action and the mission records.

func TestDelegationRequestBounds(t *testing.T) {
	parse := func(overrides *jsObject) *delegationParseFailure {
		req := structuredRequest(t, "")
		for _, k := range overrides.order() {
			req.set(k, overrides.vals[k])
		}
		_, failure := parseSubagentDelegationRequest(req)
		return failure
	}
	eq(t, parse(jo("requestId", strings.Repeat("a", 256))) == nil, true, "256 characters")
	if f := parse(jo("requestId", strings.Repeat("a", 257))); f == nil || !strings.HasPrefix(f.Error, "Delegation requestId must be") {
		t.Errorf("257 characters: %+v", f)
	}
	if f := parse(jo("bogus", float64(1))); f == nil || f.Error != "Unsupported delegation field: bogus." {
		t.Errorf("unsupported field: %+v", f)
	}
	eq(t, parse(jo("timeoutMs", float64(2147483647))) == nil, true, "the largest timeout")
	if f := parse(jo("timeoutMs", float64(2147483648))); f == nil || f.Error != "timeoutMs must be <= 2147483647." {
		t.Errorf("timeout over the maximum: %+v", f)
	}
	big := jo("kind", "structured", "schema", jo("type", "object", "description", strings.Repeat("x", 70*1024)))
	if f := parse(jo("result", big)); f == nil || f.Error != "result.schema exceeds 64 KiB when encoded." {
		t.Errorf("schema over 64 KiB: %+v", f)
	}
	small := jo("kind", "structured", "schema", jo("type", "object", "description", strings.Repeat("x", 60*1024)))
	eq(t, parse(jo("result", small)) == nil, true, "a schema under 64 KiB")
}

func TestDelegationDurationAloneIsAHeartbeat(t *testing.T) {
	a := jo("currentTool", "read", "durationMs", float64(1))
	eq(t, sameDelegationProgress(a, jo("currentTool", "read", "durationMs", float64(2))), true, "same progress, later duration")
	eq(t, sameDelegationProgress(a, jo("currentTool", "write", "durationMs", float64(1))), false, "a different tool")
}

func TestDelegationBridgeTerminalOnceAndDuplicateNodes(t *testing.T) {
	events := &fakeDelegationEvents{}
	release := make(chan struct{})
	bridge := registerPromptTemplateDelegationBridge(events, func() any { return &delegationContext{Cwd: "/repo"} },
		func(string, *jsObject, <-chan struct{}, any, func(*jsObject)) (*jsObject, error) {
			<-release
			return js(t, `{"details":{"results":[{"agent":"worker","finalOutput":"ok","exitCode":0}]}}`).(*jsObject), nil
		})
	defer bridge.dispose()
	var statuses []string
	events.On(delegationResponseEvent, func(data any) {
		o, _ := data.(*jsObject)
		s, _ := o.str("status")
		statuses = append(statuses, s)
	})
	bridge.emitTerminal("k", jo("status", "first"))
	bridge.emitTerminal("k", jo("status", "second"))
	eq(t, statuses, []string{"first"}, "one terminal response per attempt")

	started := events.once(delegationStartedEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, ""))
	recv(t, started)
	duplicate := events.once(delegationResponseEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"r2"}`))
	r := recv(t, duplicate)
	eq(t, r.vals["status"], any("duplicate_node"), "a second request for the running node")
	close(release)
}

func TestValidateActionRefusals(t *testing.T) {
	_, err := validateAction(sdk.Context{}, map[string]any{"workflowScript": "return 1;", "workflowScriptPath": "flow.js", "agent": "x"})
	eq(t, err.Error(), "workflowScript and workflowScriptPath are mutually exclusive.", "both script sources")
	_, err = validateAction(sdk.Context{}, map[string]any{"workflowScript": "return 1;", "globalConcurrencyLimit": float64(3), "agent": "x"})
	eq(t, err.Error(), "Workflow capacity overrides are only supported on top-level workflowScript or workflowScriptPath calls; validate accepts maxSubagentSpawnsPerRun for static budget checks.", "capacity override")
}

func TestMissionReceiptUrlAndObjectiveText(t *testing.T) {
	receipt := func(url string) *jsObject {
		return jo("kind", "ci", "status", "ready", "url", url, "title", "T", "createdAt", "2026-01-01T00:00:00.000Z")
	}
	_, err := parseMissionReceipt(receipt("https://example.com/run/1"), "r")
	eq(t, err, nil, "an absolute URL")
	_, err = parseMissionReceipt(receipt("not a url"), "r")
	matchesRe(t, err, `r\.url must be an absolute URL`)
	_, err = parseMissionReceipt(receipt("https://"), "r")
	matchesRe(t, err, `r\.url must be an absolute URL`)

	rec := jo("schemaVersion", float64(1), "id", "m1", "title", "T", "objective", "  O  ", "status", "planned", "createdAt", "2026-01-01T00:00:00.000Z", "updatedAt", "2026-01-01T00:00:00.000Z",
		"runs", []any{}, "decisions", []any{}, "artifacts", []any{})
	r, err := parseMissionRecord(rec, "r")
	eq(t, err, nil, "record")
	eq(t, r.Objective, "O", "the objective is trimmed")
}
