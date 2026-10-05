package mapper_test

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/microsoft/agent-host-protocol/clients/go/ahp"
	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// Twins of upstream test/activity.test.ts, describe "activity descriptions".
func TestActivityDescriptions(t *testing.T) {
	describe := func(name string, args any, cwd string) string { return mapper.DescribeToolCall(name, args, cwd) }

	twin.Run(t, "activity", "names the file a read touches", func(t *testing.T) {
		if got := describe("read", obj{"path": "/ws/src/main.ts"}, "/ws"); got != "Reading src/main.ts" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "activity", "renders paths relative to the workspace", func(t *testing.T) {
		// An absolute path is mostly noise the user already knows.
		if got := describe("edit", obj{"path": "/ws/a/b.ts"}, "/ws"); got != "Editing a/b.ts" {
			t.Fatal(got)
		}
		if got := describe("edit", obj{"path": "/elsewhere/b.ts"}, "/ws"); got != "Editing /elsewhere/b.ts" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "activity", "shows the command a bash call runs", func(t *testing.T) {
		if got := describe("bash", obj{"command": "npm test"}, ""); got != "Running npm test" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "activity", "shortens the middle, keeping both informative ends", func(t *testing.T) {
		long := describe("bash", obj{"command": "git log " + strings.Repeat("x", 200) + " --oneline"}, "")
		if utf16Len(long) >= 80 {
			t.Fatalf("not shortened: %d units", utf16Len(long))
		}
		if !regexp.MustCompile(`^Running git log`).MatchString(long) || !strings.HasSuffix(long, "--oneline") {
			t.Fatal(long)
		}
	})

	twin.Run(t, "activity", "falls back to the tool's own name", func(t *testing.T) {
		// Extension and custom tools still say something useful.
		if got := describe("my_custom_tool", obj{}, ""); got != "Running my_custom_tool" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "activity", "copes with missing arguments", func(t *testing.T) {
		if got := describe("read", nil, ""); got != "Reading a file" {
			t.Fatal(got)
		}
		if got := describe("bash", obj{}, ""); got != "Running a command" {
			t.Fatal(got)
		}
	})
}

func runActivity(t testing.TB, events []obj) replayed {
	t.Helper()
	m := mapper.NewTurnMapper("t", 0, mapper.Options{WorkingDirectory: "/ws"})
	actions := []ahptypes.StateAction{mapper.UserTurnStarted("t", "go", "1970-01-01T00:00:00.000Z")}
	for _, e := range events {
		actions = append(actions, m.Handle(e)...)
	}
	state := channels.InitialChatState("ahp-chat:/a", "chat", nil)
	for _, a := range actions {
		ahp.ApplyActionToChat(state, a)
	}
	return replayed{actions: actions, state: toObj(t, state)}
}

// activities lists each chat/activityChanged action's activity, "<cleared>" when it has none.
func activities(actions []ahptypes.StateAction) []string {
	out := []string{}
	for _, a := range ofType(actions, "chat/activityChanged") {
		if s, ok := a["activity"].(string); ok {
			out = append(out, s)
		} else {
			out = append(out, "<cleared>")
		}
	}
	return out
}

func toolStartEvent(id, name string, args any) obj {
	return obj{"type": "tool_execution_start", "toolCallId": id, "toolName": name, "args": args}
}

// Twins of upstream test/activity.test.ts, describe "activity over a turn".
func TestActivityOverATurn(t *testing.T) {
	msgStart := obj{"type": "message_start", "message": obj{"role": "assistant"}}

	twin.Run(t, "activity", "tracks thinking, tools, and responding in order", func(t *testing.T) {
		r := runActivity(t, []obj{
			pi.agentStart(), msgStart,
			toolStartEvent("tc1", "read", obj{"path": "/ws/a.ts"}),
			{"type": "tool_execution_end", "toolCallId": "tc1", "toolName": "read", "result": obj{}, "isError": false},
			pi.text(0, "done"), pi.settled(),
		})
		equal(t, activities(r.actions), []string{mapper.ThinkingActivity, "Reading a.ts", mapper.ThinkingActivity, mapper.RespondingActivity, "<cleared>"}, "activities")
	})

	twin.Run(t, "activity", "clears the description when the turn ends", func(t *testing.T) {
		// A stale one would leave the client showing "Reading ..." against an idle chat.
		r := runActivity(t, []obj{pi.agentStart(), toolStartEvent("tc1", "bash", obj{"command": "ls"}), pi.settled()})
		if r.state["activity"] != nil {
			t.Fatalf("activity = %v", r.state["activity"])
		}
	})

	twin.Run(t, "activity", "clears it on a cancelled turn too", func(t *testing.T) {
		m := mapper.NewTurnMapper("t", 0)
		m.Handle(pi.agentStart())
		equal(t, activities(m.Finish(mapper.OutcomeCancelled, "")), []string{"<cleared>"}, "activities")
	})

	twin.Run(t, "activity", "does not re-send an unchanged description", func(t *testing.T) {
		// Every delta of one response is still "Responding"; re-sending would burn a serverSeq per token.
		events := []obj{pi.agentStart(), msgStart}
		for _, d := range []string{"a", "b", "c", "d"} {
			events = append(events, pi.text(0, d))
		}
		events = append(events, pi.settled())
		r := runActivity(t, events)
		equal(t, activities(r.actions), []string{mapper.ThinkingActivity, mapper.RespondingActivity, "<cleared>"}, "activities")
	})

	twin.Run(t, "activity", "emits schema-conforming actions", func(t *testing.T) {
		r := runActivity(t, []obj{pi.agentStart(), toolStartEvent("tc1", "grep", obj{"pattern": "TODO"}), pi.settled()})
		for _, a := range r.actions {
			if actionType(a) == "chat/activityChanged" {
				assertActionsValid(t, []ahptypes.StateAction{a}, "activity")
			}
		}
	})
}

// Twins of upstream test/activity.test.ts, describe "toolInputFor".
func TestToolInputFor(t *testing.T) {
	input := func(name string, args any) string {
		s, ok := mapper.ToolInputFor(name, args)
		if !ok {
			t.Fatalf("no input for %s", name)
		}
		return s
	}

	twin.Run(t, "activity", "shows the one argument a call is about", func(t *testing.T) {
		if got := input("bash", obj{"command": "ls -la", "timeout": 5}); got != "ls -la" {
			t.Fatal(got)
		}
		if got := input("read", obj{"path": "a.ts", "offset": 1, "limit": 50}); got != "a.ts" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "activity", "keeps a search's scope, and drops the defaults models restate", func(t *testing.T) {
		// Every one of these is what a capture actually contained: the model fills in the defaults.
		if got := input("grep", obj{"pattern": "BEACON", "path": ".", "glob": "**/*", "ignoreCase": false, "limit": 100}); got != "BEACON" {
			t.Fatal(got)
		}
		if got := input("grep", obj{"pattern": "TODO", "path": "src", "glob": "*.ts", "ignoreCase": true}); got != "TODO --glob *.ts --ignore-case in src" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "activity", "falls back to the arguments for a tool it does not know", func(t *testing.T) {
		// An extension's tool has no argument this code can single out; its arguments are still the
		// most informative thing available for it.
		if got := input("some-extension-tool", obj{"a": 1}); got != "{\n  \"a\": 1\n}" {
			t.Fatalf("%q", got)
		}
	})
}
