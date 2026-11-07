package kagentpolicy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

const (
	probeName = "mcp__kagent_tools__kagent_probe"
	wantBlock = `{"block":true,"reason":"Tool call was rejected by user."}`
)

func gateHost(t *testing.T, policy string, answer any) *Host {
	t.Helper()
	return StartHost(t, mustBuild(t, policy), HostOptions{Mode: "rpc", OnCall: confirmAnswer(answer)})
}

func toolCall(h *Host, name string, extra map[string]any) json.RawMessage {
	data := map[string]any{"toolCallId": "call_42", "toolName": name, "input": map[string]any{"note": "hi <b> & more"}}
	for k, v := range extra {
		data[k] = v
	}
	return h.Fire("tool_call", data)
}

func TestGateAsksWithTheExactTitleAndMessage(t *testing.T) {
	h := gateHost(t, probePolicy, true)
	if got := toolCall(h, probeName, nil); string(got) != "null" {
		t.Fatalf("an approved call was answered %s, want no block", got)
	}
	calls := h.CallsTo("ui.confirm")
	if len(calls) != 1 {
		t.Fatalf("%d confirm calls, want 1", len(calls))
	}
	if calls[0].Args["title"] != "kagent-approval" {
		t.Fatalf("title = %v", calls[0].Args["title"])
	}
	want := `{"v":1,"kind":"approval","toolCallId":"call_42","toolName":"mcp__kagent_tools__kagent_probe","input":{"note":"hi <b> & more"}}`
	if calls[0].Args["message"] != want {
		t.Fatalf("message = %v\nwant      %s", calls[0].Args["message"], want)
	}
}

func TestGateRejection(t *testing.T) {
	for name, answer := range map[string]any{
		"false":     false,
		"null":      nil,
		"a string":  "yes",
		"a number":  1,
		"an object": map[string]any{"ok": true},
		"a list":    []any{true},
	} {
		t.Run(name, func(t *testing.T) {
			h := gateHost(t, probePolicy, answer)
			if got := toolCall(h, probeName, nil); string(got) != wantBlock {
				t.Fatalf("answer %v gave %s, want %s", answer, got, wantBlock)
			}
		})
	}
}

func TestGateSkipsOtherTools(t *testing.T) {
	policy := `{"mcp_servers":[` +
		`{"name":"gated","url":"https://a.example","require_approval":true},` +
		`{"name":"free","url":"https://b.example"}]}`
	h := gateHost(t, policy, false)
	for _, name := range []string{"bash", "read", "write", "codemode", "other", "ask_user", "mcp__free__echo", "mcp__free__"} {
		if got := toolCall(h, name, nil); string(got) != "null" {
			t.Errorf("%s was answered %s, want pass through", name, got)
		}
	}
	if n := len(h.CallsTo("ui.confirm")); n != 0 {
		t.Fatalf("%d confirms for ungated tools", n)
	}
	if got := toolCall(h, "mcp__gated__echo", nil); string(got) != wantBlock {
		t.Fatalf("a gated tool was answered %s", got)
	}
}

// With a policy loaded every mcp__ tool of a server the policy does not name is refused: a server from mcp.json or
// another extension would otherwise bypass the allowlist and the approval. This holds for every kind of policy.
func TestDefaultDenyForUnknownMcpServers(t *testing.T) {
	const denied = `{"block":true,"reason":"MCP server is not part of this agent."}`
	policies := map[string]string{
		"approval and ask_user": probePolicy,
		"free server only":      `{"mcp_servers":[{"name":"free","url":"https://b.example"}]}`,
		"ask_user only":         `{"ask_user":true}`,
		"empty policy":          `{}`,
	}
	for name, policy := range policies {
		t.Run(name, func(t *testing.T) {
			h := gateHost(t, policy, true)
			for _, tool := range []string{"mcp__other__echo", "mcp__gate__echo", "mcp__gatedx__echo", "mcp__kagent_tool__x", "mcp__", "mcp__free", "mcp__kagent_tools"} {
				if got := toolCall(h, tool, nil); string(got) != denied {
					t.Errorf("%s was answered %s, want the default deny", tool, got)
				}
			}
			if n := len(h.CallsTo("ui.confirm")); n != 0 {
				t.Fatalf("%d confirms for denied tools", n)
			}
		})
	}
}

func TestResourceToolsPassWhenNoServerRequiresApproval(t *testing.T) {
	h := gateHost(t, `{"mcp_servers":[{"name":"free","url":"https://b.example"}]}`, true)
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		if got := toolCall(h, name, nil); string(got) != "null" {
			t.Errorf("%s was answered %s", name, got)
		}
	}
}

// PiG names a tool mcp__<server>__<tool> with - in the server name written as _.
func TestGateMatchesSanitizedServerNamesAndEveryToolName(t *testing.T) {
	h := gateHost(t, probePolicy, false)
	for _, name := range []string{
		"mcp__kagent_tools__kagent_probe",
		"mcp__kagent_tools__anything_else",
		"mcp__kagent_tools__get_thing_1a2b3c", // a name PiG suffixed after a collision
		"mcp__kagent_tools__",
	} {
		if got := toolCall(h, name, nil); string(got) != wantBlock {
			t.Errorf("%s was answered %s, want a block", name, got)
		}
	}
}

// A call a codemode script makes carries the id of the codemode call; it is held like a direct one.
func TestGateHoldsNestedCodemodeCalls(t *testing.T) {
	h := gateHost(t, probePolicy, false)
	got := toolCall(h, probeName, map[string]any{"toolCallId": "inner_1", "parentToolCallId": "outer_1"})
	if string(got) != wantBlock {
		t.Fatalf("nested call answered %s", got)
	}
	calls := h.CallsTo("ui.confirm")
	if len(calls) != 1 || !strings.Contains(calls[0].Args["message"].(string), `"toolCallId":"inner_1"`) {
		t.Fatalf("confirm calls = %+v", calls)
	}
}

func TestGateBlocksMcpResourceTools(t *testing.T) {
	h := gateHost(t, probePolicy, true)
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		got := toolCall(h, name, nil)
		if !strings.Contains(string(got), `"block":true`) || !strings.Contains(string(got), "MCP resources are not available") {
			t.Errorf("%s was answered %s", name, got)
		}
	}
	if n := len(h.CallsTo("ui.confirm")); n != 0 {
		t.Fatalf("resource tools asked for approval %d times", n)
	}
}

// A failing dialog is an error of the handler, which PiG reads as a refusal; it is never an approval.
func TestGateDialogFailureIsAnError(t *testing.T) {
	h := StartHost(t, mustBuild(t, probePolicy), HostOptions{Mode: "rpc", OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.confirm" {
			return nil, "dialog unavailable"
		}
		return nil, ""
	}})
	args, _ := json.Marshal(map[string]any{"type": "tool_call", "toolCallId": "c1", "toolName": probeName, "input": map[string]any{}})
	result, failure := h.roundTrip(map[string]any{"method": "event", "event": "tool_call", "handler_id": h.handlers["tool_call"], "args": json.RawMessage(args)})
	if !strings.Contains(failure, "dialog unavailable") {
		t.Fatalf("failure = %q, result = %s", failure, result)
	}
}

// PiG cuts a tool name longer than 64 characters to 55 and appends _ and the first 8 hex digits of
// sha256(server NUL tool). With the longest server name the policy allows (40 characters) the prefix survives the cut.
func TestGateHoldsToolsWithCutNames(t *testing.T) {
	server := strings.Repeat("s", 40)
	policy := `{"mcp_servers":[{"name":"` + server + `","url":"https://a.example","require_approval":true},` +
		`{"name":"free","url":"https://b.example"}]}`
	h := gateHost(t, policy, false)
	long := strings.Repeat("t", 128)
	sum := sha256.Sum256([]byte(server + "\x00" + long))
	cut := ("mcp__" + server + "__" + long)[:55] + "_" + hex.EncodeToString(sum[:])[:8]
	if len(cut) != 64 {
		t.Fatalf("test name has %d characters", len(cut))
	}
	for _, name := range []string{cut, "mcp__" + server + "__short"} {
		if got := toolCall(h, name, nil); string(got) != wantBlock {
			t.Errorf("%s was answered %s, want a block", name, got)
		}
	}
	if got := toolCall(h, "mcp__free__echo", nil); string(got) != "null" {
		t.Errorf("an ungated server was answered %s", got)
	}
}

func TestGateRejectsMalformedEvents(t *testing.T) {
	h := gateHost(t, probePolicy, true)
	for name, data := range map[string]map[string]any{
		"no tool name":   {"toolCallId": "c1", "input": map[string]any{}},
		"no input":       {"toolName": probeName, "toolCallId": "c1"},
		"string input":   {"toolName": probeName, "toolCallId": "c1", "input": "{}"},
		"null input":     {"toolName": probeName, "toolCallId": "c1", "input": nil},
		"no call id":     {"toolName": probeName, "input": map[string]any{}},
		"empty call id":  {"toolName": probeName, "toolCallId": ""},
		"numeric name":   {"toolName": 7, "toolCallId": "c1"},
		"numeric callid": {"toolName": probeName, "toolCallId": 7},
	} {
		data["type"] = "tool_call"
		args, _ := json.Marshal(data)
		_, failure := h.roundTrip(map[string]any{"method": "event", "event": "tool_call", "handler_id": h.handlers["tool_call"], "args": json.RawMessage(args)})
		if failure == "" {
			t.Errorf("%s: the handler did not fail", name)
		}
	}
	if n := len(h.CallsTo("ui.confirm")); n != 0 {
		t.Fatalf("%d confirms for malformed events", n)
	}
}
