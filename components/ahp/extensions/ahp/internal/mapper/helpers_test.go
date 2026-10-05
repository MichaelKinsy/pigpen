package mapper_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahp"
	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// obj is a decoded JSON object; the pi events and the reduced chat state are compared as data,
// the way upstream's tests compare plain objects.
type obj = map[string]any

const (
	chatURI = "ahp-chat:/c1"
	turnID  = "turn-1"
)

// pi mirrors the `pi` shorthand object of upstream's event-mapper.test.ts.
var pi = struct {
	agentStart, settled, assistantStart func() obj
	agentEnd                            func(willRetry bool) obj
	assistantEnd                        func(usage obj) obj
	text, thinking                      func(idx int, delta string) obj
	textEnd, thinkingEnd                func(idx int, content string) obj
	toolStart                           func(idx int, id, name string) obj
	toolDelta                           func(idx int, delta string) obj
	toolEnd                             func(idx int, id, name string, args any) obj
	execUpdate                          func(id, name, text string) obj
	execEnd                             func(id, name, text string, isError bool) obj
	streamError                         func(message string, aborted bool) obj
}{
	agentStart: func() obj { return obj{"type": "agent_start"} },
	agentEnd:   func(w bool) obj { return obj{"type": "agent_end", "messages": []any{}, "willRetry": w} },
	settled:    func() obj { return obj{"type": "agent_settled"} },
	assistantStart: func() obj {
		return obj{"type": "message_start", "message": obj{"role": "assistant", "content": []any{}}}
	},
	assistantEnd: func(usage obj) obj {
		msg := obj{"role": "assistant", "content": []any{}}
		if usage != nil {
			msg["usage"] = usage
			msg["model"] = "test-model"
		}
		return obj{"type": "message_end", "message": msg}
	},
	text:        func(i int, d string) obj { return update(obj{"type": "text_delta", "contentIndex": i, "delta": d}) },
	thinking:    func(i int, d string) obj { return update(obj{"type": "thinking_delta", "contentIndex": i, "delta": d}) },
	textEnd:     func(i int, c string) obj { return update(obj{"type": "text_end", "contentIndex": i, "content": c}) },
	thinkingEnd: func(i int, c string) obj { return update(obj{"type": "thinking_end", "contentIndex": i, "content": c}) },
	toolStart: func(i int, id, name string) obj {
		content := make([]any, i+1)
		for k := range content {
			content[k] = obj{}
		}
		content[i] = obj{"type": "toolCall", "id": id, "name": name}
		return update(obj{"type": "toolcall_start", "contentIndex": i, "partial": obj{"content": content}})
	},
	toolDelta: func(i int, d string) obj { return update(obj{"type": "toolcall_delta", "contentIndex": i, "delta": d}) },
	toolEnd: func(i int, id, name string, args any) obj {
		return update(obj{"type": "toolcall_end", "contentIndex": i, "toolCall": obj{"id": id, "name": name, "arguments": args}})
	},
	execUpdate: func(id, name, text string) obj {
		return obj{"type": "tool_execution_update", "toolCallId": id, "toolName": name,
			"partialResult": obj{"content": []any{obj{"type": "text", "text": text}}}}
	},
	execEnd: func(id, name, text string, isError bool) obj {
		return obj{"type": "tool_execution_end", "toolCallId": id, "toolName": name,
			"result": obj{"content": []any{obj{"type": "text", "text": text}}}, "isError": isError}
	},
	streamError: func(message string, aborted bool) obj {
		reason := "error"
		if aborted {
			reason = "aborted"
		}
		return update(obj{"type": "error", "reason": reason, "error": obj{"errorMessage": message, "stopReason": reason}})
	},
}

func update(ame obj) obj {
	return obj{"type": "message_update", "message": obj{"role": "assistant"}, "assistantMessageEvent": ame}
}

// replayed is the outcome of running a turn end to end.
type replayed struct {
	actions []ahptypes.StateAction
	state   obj // the reduced ChatState as JSON data
}

// reduce applies actions to a fresh chat with the protocol's own reducer.
func reduce(t testing.TB, actions []ahptypes.StateAction) obj {
	t.Helper()
	state := channels.InitialChatState(chatURI, "Test chat", nil)
	for _, a := range actions {
		ahp.ApplyActionToChat(state, a)
	}
	return toObj(t, state)
}

func toObj(t testing.TB, v any) obj {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out obj
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func actionType(a ahptypes.StateAction) string { return string(asObj(a)["type"].(string)) }

func asObj(v any) obj {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out obj
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

func ofType(actions []ahptypes.StateAction, typ string) []obj {
	var out []obj
	for _, a := range actions {
		if o := asObj(a); o["type"] == typ {
			out = append(out, o)
		}
	}
	return out
}

func newMapper() *mapper.TurnMapper { return mapper.NewTurnMapper(turnID, 0) }

// runTurn feeds events through a mapper and reduces the result, like upstream's runTurn.
func runTurn(t testing.TB, events []obj, text ...string) replayed {
	t.Helper()
	prompt := "Hello"
	if len(text) > 0 {
		prompt = text[0]
	}
	m := newMapper()
	actions := []ahptypes.StateAction{mapper.UserTurnStarted(turnID, prompt, "1970-01-01T00:00:00.000Z")}
	for _, e := range events {
		actions = append(actions, m.Handle(e)...)
	}
	return replayed{actions: actions, state: reduce(t, actions)}
}

// get walks decoded JSON: strings index objects, ints index arrays.
func get(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(obj)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok {
				return nil
			}
			if k < 0 {
				k += len(a)
			}
			if k < 0 || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func list(v any, path ...any) []any {
	l, _ := get(v, path...).([]any)
	return l
}

func str(v any, path ...any) string {
	s, _ := get(v, path...).(string)
	return s
}

// partsOfKind returns the response parts of the first (or active) turn with that kind.
func partsOfKind(state obj, kind string) []obj {
	turn := get(state, "turns", 0)
	if turn == nil {
		turn = get(state, "activeTurn")
	}
	var out []obj
	for _, p := range list(turn, "responseParts") {
		if str(p, "kind") == kind {
			out = append(out, p.(obj))
		}
	}
	return out
}

func contents(parts []obj) []string {
	out := []string{}
	for _, p := range parts {
		out = append(out, str(p, "content"))
	}
	return out
}

func toolCalls(state obj) []obj {
	var out []obj
	for _, p := range partsOfKind(state, "toolCall") {
		out = append(out, p["toolCall"].(obj))
	}
	return out
}

func equal(t testing.TB, got, want any, what string) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("%s:\n got  %s\n want %s", what, g, w)
	}
}

func assertActionsValid(t testing.TB, actions []ahptypes.StateAction, context string) {
	t.Helper()
	for _, a := range actions {
		if err := checkAction(a); err != nil {
			t.Fatalf("%s: non-conforming %s: %v", context, actionType(a), err)
		}
	}
}

func fail(t testing.TB, format string, args ...any) {
	t.Helper()
	t.Fatal(fmt.Sprintf(format, args...))
}

var _ = strings.TrimSpace
