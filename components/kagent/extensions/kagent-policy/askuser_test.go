package kagentpolicy_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const askMessage = `{"v":1,"kind":"ask_user","toolCallId":"call-1","toolName":"ask_user","input":{"questions":[` +
	`{"question":"Which env?","choices":["dev","prod"],"multiple":false},{"question":"Ticket?","choices":[],"multiple":true}]}}`

const declinedBlock = `{"block":true,"reason":"The user declined to answer."}`

var askParams = map[string]any{"questions": []any{
	map[string]any{"question": "Which env?", "choices": []any{"dev", "prod"}},
	map[string]any{"question": "Ticket?", "multiple": true},
}}

func askHost(t *testing.T, answer any) *Host {
	t.Helper()
	return StartHost(t, mustBuild(t, `{"ask_user":true}`), HostOptions{Mode: "rpc", OnCall: confirmAnswer(answer)})
}

// ask runs the two steps PiG runs for an ask_user call: the tool_call hook, then, unless the hook blocked, execute.
// The host numbers every tool call "call-1".
func ask(h *Host, params map[string]any) (hook json.RawMessage, result json.RawMessage, failure string) {
	hook = h.Fire("tool_call", map[string]any{"toolCallId": "call-1", "toolName": "ask_user", "input": params})
	if string(hook) != "null" {
		return hook, nil, ""
	}
	result, failure = h.Tool("ask_user", params)
	return hook, result, failure
}

func TestAskUserEmitsOneConfirmWithTheExactTitleAndMessage(t *testing.T) {
	h := askHost(t, "dev")
	hook, _, failure := ask(h, askParams)
	if failure != "" || string(hook) != "null" {
		t.Fatalf("hook %s failure %q", hook, failure)
	}
	calls := h.CallsTo("ui.confirm")
	if len(calls) != 1 {
		t.Fatalf("%d confirm calls, want 1 (execute must not ask again)", len(calls))
	}
	if calls[0].Args["title"] != "kagent-ask-user" || calls[0].Args["message"] != askMessage {
		t.Fatalf("title %v message %v\nwant message %s", calls[0].Args["title"], calls[0].Args["message"], askMessage)
	}
}

// The dialog is in the tool_call hook: PiG runs every hook of a batch before any call, so a parked harness leaves
// nothing executed. The tool must not be declared sequential either, which would make PiG run the batch call by call.
func TestAskUserAsksInTheHookAndIsNotSequential(t *testing.T) {
	h := askHost(t, "dev")
	h.Fire("tool_call", map[string]any{"toolCallId": "call-1", "toolName": "ask_user", "input": askParams})
	if n := len(h.CallsTo("ui.confirm")); n != 1 {
		t.Fatalf("the hook made %d confirm calls, want 1", n)
	}
	raw, _ := json.Marshal(register(t, mustBuild(t, `{"ask_user":true}`)))
	if strings.Contains(string(raw), "execution_mode") {
		t.Fatalf("ask_user declares an execution mode: %s", raw)
	}
}

func TestAskUserResultIsTheAnswer(t *testing.T) {
	cases := []struct {
		name   string
		answer any
		want   string
	}{
		{"a string", "prod, ticket 42", "prod, ticket 42"},
		{"true", true, "true"},
		{"an object", map[string]any{"a": []any{"dev"}}, `{"a":["dev"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := askHost(t, tc.answer)
			hook, result, failure := ask(h, askParams)
			if failure != "" || string(hook) != "null" {
				t.Fatalf("hook %s failure %q", hook, failure)
			}
			if !strings.Contains(string(result), tc.want) && !strings.Contains(string(result), strings.ReplaceAll(tc.want, `"`, `\"`)) {
				t.Fatalf("result = %s, want it to carry %s", result, tc.want)
			}
		})
	}
}

// A declined dialog blocks the call: the model reads the reason as an error result and execute never runs.
func TestAskUserDeclined(t *testing.T) {
	for name, answer := range map[string]any{"false": false, "cancelled": nil, "a blank string": "  "} {
		t.Run(name, func(t *testing.T) {
			h := askHost(t, answer)
			hook, _, _ := ask(h, askParams)
			if string(hook) != declinedBlock {
				t.Fatalf("hook = %s, want %s", hook, declinedBlock)
			}
		})
	}
}

func TestAskUserExecuteWithoutAnAnswerFails(t *testing.T) {
	h := askHost(t, "x")
	if _, failure := h.Tool("ask_user", askParams); !strings.Contains(failure, "no answer was recorded") {
		t.Fatalf("failure = %q", failure)
	}
	// An answer is used once.
	if _, _, failure := ask(h, askParams); failure != "" {
		t.Fatal(failure)
	}
	if _, failure := h.Tool("ask_user", askParams); !strings.Contains(failure, "no answer was recorded") {
		t.Fatalf("second execute: failure = %q", failure)
	}
}

func TestAskUserRejectsBadArgumentsWithoutAsking(t *testing.T) {
	many := make([]any, 9)
	for i := range many {
		many[i] = map[string]any{"question": "q"}
	}
	manyChoices := make([]any, 17)
	for i := range manyChoices {
		manyChoices[i] = "c"
	}
	cases := map[string]map[string]any{
		"no questions":       {},
		"empty list":         {"questions": []any{}},
		"too many questions": {"questions": many},
		"extra argument":     {"questions": []any{map[string]any{"question": "q"}}, "extra": 1},
		"not an object":      {"questions": []any{"q"}},
		"empty question":     {"questions": []any{map[string]any{"question": ""}}},
		"long question":      {"questions": []any{map[string]any{"question": strings.Repeat("q", 2001)}}},
		"missing question":   {"questions": []any{map[string]any{"choices": []any{"a"}}}},
		"extra member":       {"questions": []any{map[string]any{"question": "q", "default": "a"}}},
		"too many choices":   {"questions": []any{map[string]any{"question": "q", "choices": manyChoices}}},
		"empty choice":       {"questions": []any{map[string]any{"question": "q", "choices": []any{""}}}},
		"numeric choice":     {"questions": []any{map[string]any{"question": "q", "choices": []any{1}}}},
		"string multiple":    {"questions": []any{map[string]any{"question": "q", "multiple": "yes"}}},
	}
	h := askHost(t, "x")
	for name, params := range cases {
		hook := h.Fire("tool_call", map[string]any{"toolCallId": "call-1", "toolName": "ask_user", "input": params})
		if !strings.Contains(string(hook), `"block":true`) || !strings.Contains(string(hook), "ask_user") {
			t.Errorf("%s: hook = %s", name, hook)
		}
	}
	if n := len(h.CallsTo("ui.confirm")); n != 0 {
		t.Fatalf("%d confirms for rejected arguments", n)
	}
}

func TestAskUserBounds(t *testing.T) {
	eight := make([]any, 8)
	for i := range eight {
		eight[i] = map[string]any{"question": strings.Repeat("é", 2000), "choices": []any{strings.Repeat("c", 500)}}
	}
	h := askHost(t, "ok")
	hook, _, failure := ask(h, map[string]any{"questions": eight})
	if failure != "" || string(hook) != "null" {
		t.Fatalf("a question set at the limits was refused: hook %s failure %q", hook, failure)
	}
}

func TestAskUserDialogFailureIsAnError(t *testing.T) {
	h := StartHost(t, mustBuild(t, `{"ask_user":true}`), HostOptions{Mode: "rpc", OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.confirm" {
			return nil, "dialog unavailable"
		}
		return nil, ""
	}})
	args, _ := json.Marshal(map[string]any{"type": "tool_call", "toolCallId": "call-1", "toolName": "ask_user", "input": askParams})
	_, failure := h.roundTrip(map[string]any{"method": "event", "event": "tool_call", "handler_id": h.handlers["tool_call"], "args": json.RawMessage(args)})
	if !strings.Contains(failure, "dialog unavailable") {
		t.Fatalf("failure = %q", failure)
	}
}

// With ask_user and an approval server together one handler serves both.
func TestAskUserAndApprovalShareTheGate(t *testing.T) {
	h := StartHost(t, mustBuild(t, probePolicy), HostOptions{Mode: "rpc", OnCall: confirmAnswer(false)})
	if got := h.Fire("tool_call", map[string]any{"toolCallId": "c1", "toolName": probeName, "input": map[string]any{}}); string(got) != wantBlock {
		t.Fatalf("approval answered %s", got)
	}
	if got := h.Fire("tool_call", map[string]any{"toolCallId": "c2", "toolName": "ask_user", "input": askParams}); string(got) != declinedBlock {
		t.Fatalf("ask_user answered %s", got)
	}
	titles := []any{}
	for _, c := range h.CallsTo("ui.confirm") {
		titles = append(titles, c.Args["title"])
	}
	if len(titles) != 2 || titles[0] != "kagent-approval" || titles[1] != "kagent-ask-user" {
		t.Fatalf("titles = %v", titles)
	}
}
