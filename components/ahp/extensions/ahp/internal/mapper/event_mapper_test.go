package mapper_test

import (
	"encoding/base64"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// onePixelPNG is upstream test/support/images.ts ONE_PIXEL_PNG.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func turnErrorMessage(state obj, turn int) string {
	last := get(state, "turns", turn, "responseParts", -1)
	if str(last, "kind") != "error" {
		return ""
	}
	return str(last, "error", "message")
}

func statusHas(state obj, bit float64) bool {
	f, _ := state["status"].(float64)
	return int(f)&int(bit) != 0
}

// Twins of upstream test/event-mapper.test.ts.
func TestEventMapperTurnBoundary(t *testing.T) {

	twin.Run(t, "event-mapper", "spans several agent runs in a single turn", func(t *testing.T) {
		// An auto-retry produces a second agent_start/agent_end pair. Closing the turn on the first
		// agent_end would make every later action a silent no-op in the reducer.
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "first attempt"), pi.agentEnd(true),
			pi.agentStart(), pi.assistantStart(), pi.text(0, "second attempt"), pi.assistantEnd(nil), pi.agentEnd(false),
			pi.settled(),
		})
		if r.state["activeTurn"] != nil {
			t.Fatal("turn still active")
		}
		if n := len(list(r.state, "turns")); n != 1 {
			t.Fatalf("turns = %d", n)
		}
		if got := str(r.state, "turns", 0, "state"); got != "complete" {
			t.Fatalf("state = %q", got)
		}
		// Both attempts survive as separate parts: the protocol has no action that removes one.
		equal(t, contents(partsOfKind(r.state, "markdown")), []string{"first attempt", "second attempt"}, "markdown")
	})

	twin.Run(t, "event-mapper", "only closes the turn on agent_settled", func(t *testing.T) {
		m := newMapper()
		var before = 0
		for _, e := range []obj{pi.agentStart(), pi.assistantStart(), pi.text(0, "hi"), pi.agentEnd(false)} {
			before += len(ofType(m.Handle(e), "chat/turnComplete"))
		}
		if before != 0 {
			t.Fatal("the turn closed before agent_settled")
		}
		// The terminator travels behind an activity clear, so look for it rather than assuming it is first.
		if len(ofType(m.Handle(pi.settled()), "chat/turnComplete")) == 0 {
			t.Fatal("agent_settled did not close the turn")
		}
	})

	twin.Run(t, "event-mapper", "reports an aborted run as cancelled, not complete", func(t *testing.T) {
		r := runTurn(t, []obj{pi.agentStart(), pi.assistantStart(), pi.text(0, "partial"), pi.streamError("aborted by user", true), pi.settled()})
		if got := str(r.state, "turns", 0, "state"); got != "cancelled" {
			t.Fatalf("state = %q", got)
		}
	})

	twin.Run(t, "event-mapper", "reports a failed run as an error and marks the chat", func(t *testing.T) {
		r := runTurn(t, []obj{pi.agentStart(), pi.assistantStart(), pi.streamError("overloaded_error", false), pi.settled()})
		errs := ofType(r.actions, "chat/error")
		if len(errs) != 1 {
			t.Fatalf("chat/error actions = %d", len(errs))
		}
		equal(t, errs[0]["part"], obj{"kind": "error", "error": obj{"errorType": "agentRunFailed", "message": "overloaded_error"}}, "error part")
		assertActionsValid(t, r.actions, "error action")
		if got := str(r.state, "turns", 0, "state"); got != "error" {
			t.Fatalf("state = %q", got)
		}
		if got := turnErrorMessage(r.state, 0); got != "overloaded_error" {
			t.Fatalf("turn error = %q", got)
		}
		if !statusHas(r.state, float64(ahptypes.SessionStatusError)) {
			t.Fatalf("chat status %v lacks the Error bit", r.state["status"])
		}
	})

	twin.Run(t, "event-mapper", "ignores events after the turn has closed", func(t *testing.T) {
		m := newMapper()
		m.Handle(pi.settled())
		if got := m.Handle(pi.text(0, "late")); len(got) != 0 {
			t.Fatalf("late event produced %d actions", len(got))
		}
		if got := m.Finish("", ""); len(got) != 0 {
			t.Fatalf("second finish produced %d actions", len(got))
		}
	})

	twin.Run(t, "event-mapper", "can be closed without agent_settled", func(t *testing.T) {
		// An extension command handled entirely by pi never reaches the model, so no agent event
		// ever arrives; the turn must still terminate.
		got := newMapper().Finish(mapper.OutcomeComplete, "")
		if len(got) == 0 || actionType(got[0]) != "chat/turnComplete" {
			t.Fatalf("finish(complete) = %v", got)
		}
	})
}

func TestEventMapperPartIdentity(t *testing.T) {

	twin.Run(t, "event-mapper", "keeps contentIndex collisions across messages apart", func(t *testing.T) {
		// pi restarts contentIndex at 0 for every assistant message; using it as a partId would
		// append the second message's text to the first message's paragraph.
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "first message"), pi.assistantEnd(nil),
			pi.assistantStart(), pi.text(0, "second message"), pi.assistantEnd(nil), pi.settled(),
		})
		equal(t, contents(partsOfKind(r.state, "markdown")), []string{"first message", "second message"}, "markdown")
	})

	twin.Run(t, "event-mapper", "appends consecutive deltas into one part", func(t *testing.T) {
		r := runTurn(t, []obj{pi.agentStart(), pi.assistantStart(), pi.text(0, "Hello"), pi.text(0, ", "), pi.text(0, "world"), pi.settled()})
		if got := str(r.state, "turns", 0, "responseParts", 0, "content"); got != "Hello, world" {
			t.Fatalf("content = %q", got)
		}
	})

	twin.Run(t, "event-mapper", "routes thinking into a reasoning part, separate from markdown", func(t *testing.T) {
		r := runTurn(t, []obj{pi.agentStart(), pi.assistantStart(), pi.thinking(0, "let me think"), pi.thinking(0, " harder"), pi.text(1, "the answer"), pi.settled()})
		if k := str(r.state, "turns", 0, "responseParts", 0, "kind"); k != "reasoning" {
			t.Fatalf("part 0 kind = %q", k)
		}
		if c := str(r.state, "turns", 0, "responseParts", 0, "content"); c != "let me think harder" {
			t.Fatalf("reasoning = %q", c)
		}
		if k := str(r.state, "turns", 0, "responseParts", 1, "kind"); k != "markdown" {
			t.Fatalf("part 1 kind = %q", k)
		}
	})

	twin.Run(t, "event-mapper", "creates parts in arrival order so interleaving survives", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "before"), pi.toolStart(1, "tc-1", "bash"),
			pi.toolEnd(1, "tc-1", "bash", obj{"command": "ls"}), pi.text(2, "after"), pi.settled(),
		})
		kinds := []string{}
		for _, p := range list(r.state, "turns", 0, "responseParts") {
			kinds = append(kinds, str(p, "kind"))
		}
		equal(t, kinds, []string{"markdown", "toolCall", "markdown"}, "kinds")
	})

	twin.Run(t, "event-mapper", "recovers a block a provider never streamed", func(t *testing.T) {
		// Some models emit thinking_start / thinking_end with no thinking_delta in between.
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.thinkingEnd(0, "reasoned without streaming"),
			pi.textEnd(1, "answered without streaming"), pi.settled(),
		})
		if k, c := str(r.state, "turns", 0, "responseParts", 0, "kind"), str(r.state, "turns", 0, "responseParts", 0, "content"); k != "reasoning" || c != "reasoned without streaming" {
			t.Fatalf("part 0 = %s %q", k, c)
		}
		if k, c := str(r.state, "turns", 0, "responseParts", 1, "kind"), str(r.state, "turns", 0, "responseParts", 1, "content"); k != "markdown" || c != "answered without streaming" {
			t.Fatalf("part 1 = %s %q", k, c)
		}
	})

	twin.Run(t, "event-mapper", "does not duplicate a block that was streamed", func(t *testing.T) {
		r := runTurn(t, []obj{pi.agentStart(), pi.assistantStart(), pi.text(0, "Hello"), pi.text(0, " world"), pi.textEnd(0, "Hello world"), pi.settled()})
		parts := list(r.state, "turns", 0, "responseParts")
		if len(parts) != 1 || str(parts[0], "content") != "Hello world" {
			t.Fatalf("parts = %v", parts)
		}
	})

	twin.Run(t, "event-mapper", "emits no part for an empty delta", func(t *testing.T) {
		r := runTurn(t, []obj{pi.agentStart(), pi.assistantStart(), pi.text(0, ""), pi.settled()})
		if n := len(list(r.state, "turns", 0, "responseParts")); n != 0 {
			t.Fatalf("parts = %d", n)
		}
	})
}

func TestEventMapperToolCalls(t *testing.T) {

	twin.Run(t, "event-mapper", "drives a tool call from streaming to completed without confirmation", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.toolStart(0, "tc-1", "bash"), pi.toolDelta(0, `{"command":`), pi.toolDelta(0, `"ls"}`),
			pi.toolEnd(0, "tc-1", "bash", obj{"command": "ls"}), pi.assistantEnd(nil), pi.execEnd("tc-1", "bash", "file-a\nfile-b", false), pi.settled(),
		})
		calls := toolCalls(r.state)
		if len(calls) != 1 || str(calls[0], "status") != "completed" || str(calls[0], "toolName") != "bash" {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "event-mapper", "publishes partial output while a tool is still running", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.toolStart(0, "tc-1", "bash"),
			pi.toolEnd(0, "tc-1", "bash", obj{"command": "long-command"}), pi.execUpdate("tc-1", "bash", "partial output"),
		})
		calls := toolCalls(r.state)
		if len(calls) != 1 || str(calls[0], "status") != "running" {
			t.Fatalf("calls = %v", calls)
		}
		equal(t, calls[0]["content"], []any{obj{"type": "text", "text": "partial output"}}, "content")
	})

	twin.Run(t, "event-mapper", "never enters pending-confirmation", func(t *testing.T) {
		// pi has no permission system, so there is nothing for a client to approve. Auto-confirming
		// is the protocol's `confirmed` path and means no client renders approve/deny UI.
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.toolStart(0, "tc-1", "bash"),
			pi.toolEnd(0, "tc-1", "bash", obj{"command": "ls"}), pi.execEnd("tc-1", "bash", "ok", false), pi.settled(),
		})
		ready := ofType(r.actions, "chat/toolCallReady")
		if len(ready) != 1 || ready[0]["confirmed"] != "setting" {
			t.Fatalf("ready = %v", ready)
		}
	})

	twin.Run(t, "event-mapper", "marks a failed tool call as unsuccessful", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.toolStart(0, "tc-1", "bash"),
			pi.toolEnd(0, "tc-1", "bash", obj{"command": "false"}), pi.execEnd("tc-1", "bash", "boom", true), pi.settled(),
		})
		calls := toolCalls(r.state)
		if len(calls) != 1 || str(calls[0], "status") != "completed" || calls[0]["success"] != false {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "event-mapper", "force-cancels a tool call still running when the turn ends", func(t *testing.T) {
		// The reducer does this for us; asserting it pins the behaviour the mapper relies on when
		// an abort lands mid-tool.
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.toolStart(0, "tc-1", "bash"),
			pi.toolEnd(0, "tc-1", "bash", obj{"command": "sleep 100"}), pi.settled(),
		})
		calls := toolCalls(r.state)
		if len(calls) != 1 || str(calls[0], "status") != "cancelled" {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "event-mapper", "ignores tool events for a call it never saw", func(t *testing.T) {
		got := newMapper().Handle(pi.execEnd("unknown", "bash", "x", false))
		// No tool-call action: there is nothing to complete. The activity change still goes out.
		if n := len(ofType(got, "chat/toolCallComplete")); n != 0 {
			t.Fatalf("completed %d unknown tool calls", n)
		}
	})
}

func TestEventMapperUsageAndSchema(t *testing.T) {

	twin.Run(t, "event-mapper", "carries token usage onto the turn", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "hi"),
			pi.assistantEnd(obj{"input": 100, "output": 20, "cacheRead": 5}), pi.settled(),
		})
		equal(t, get(r.state, "turns", 0, "usage"), obj{"inputTokens": 100, "outputTokens": 20, "cacheReadTokens": 5, "model": "test-model"}, "usage")
	})

	twin.Run(t, "event-mapper", "keeps the usage fields the protocol has no slot for", func(t *testing.T) {
		// pi reports cache writes, a reasoning-token breakdown and computed cost; UsageInfo has
		// fields for none of them. They ride in _meta.
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "hi"),
			pi.assistantEnd(obj{"input": 3, "output": 49, "cacheRead": 0, "cacheWrite": 2699, "reasoning": 20, "totalTokens": 2751}), pi.settled(),
		})
		equal(t, get(r.state, "turns", 0, "usage", "_meta"), obj{"cacheWriteTokens": 2699, "reasoningTokens": 20, "totalTokens": 2751}, "_meta")
	})

	twin.Run(t, "event-mapper", "emits only schema-conforming actions", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.thinking(0, "hmm"), pi.text(1, "answer"),
			pi.toolStart(2, "tc-1", "read"), pi.toolDelta(2, `{"path":"a"}`), pi.toolEnd(2, "tc-1", "read", obj{"path": "a"}),
			pi.assistantEnd(obj{"input": 1, "output": 2, "cacheRead": 0}), pi.execEnd("tc-1", "read", "contents", false), pi.settled(),
		})
		assertActionsValid(t, r.actions, "turn")
	})
}

func TestEventMapperOutcomeFromStopReason(t *testing.T) {

	twin.Run(t, "event-mapper", "treats an aborted assistant message as a cancelled turn", func(t *testing.T) {
		// Aborting before anything streams produces no `error` delta: the only signal is the
		// finished message's stopReason (observed live in the `abort` fixture).
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(),
			{"type": "message_end", "message": obj{"role": "assistant", "stopReason": "aborted", "errorMessage": "Request aborted"}},
			pi.settled(),
		})
		if got := str(r.state, "turns", 0, "state"); got != "cancelled" {
			t.Fatalf("state = %q", got)
		}
	})

	twin.Run(t, "event-mapper", "treats a failed assistant message as an error turn", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(),
			{"type": "message_end", "message": obj{"role": "assistant", "stopReason": "error", "errorMessage": "upstream exploded"}},
			pi.settled(),
		})
		if got := str(r.state, "turns", 0, "state"); got != "error" {
			t.Fatalf("state = %q", got)
		}
		if got := turnErrorMessage(r.state, 0); got != "upstream exploded" {
			t.Fatalf("error = %q", got)
		}
	})
}

func TestEventMapperInjectedMessages(t *testing.T) {

	twin.Run(t, "event-mapper", "gives an injected user message its own turn", func(t *testing.T) {
		// pi stores a steering message as an ordinary user message with nothing marking it as
		// steering, so a rebuild from disk necessarily makes it a turn. The live path must match.
		injected := []any{obj{"type": "text", "text": "Stop counting."}, obj{"type": "image", "data": onePixelPNG, "mimeType": "image/png"}}
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "1\n2\n3"), pi.assistantEnd(nil),
			{"type": "message_start", "message": obj{"role": "user", "content": injected}},
			{"type": "message_end", "message": obj{"role": "user", "content": injected}},
			pi.assistantStart(), pi.text(0, "STOPPED"), pi.assistantEnd(nil), pi.settled(),
		})
		if n := len(list(r.state, "turns")); n != 2 {
			t.Fatalf("turns = %d", n)
		}
		if got := str(r.state, "turns", 0, "message", "text"); got != "Hello" {
			t.Fatalf("turn 0 text = %q", got)
		}
		if got := str(r.state, "turns", 1, "message", "text"); got != "Stop counting." {
			t.Fatalf("turn 1 text = %q", got)
		}
		equal(t, get(r.state, "turns", 1, "message", "attachments"), []any{obj{
			"type": "embeddedResource", "label": "Image 1", "displayKind": "image", "data": onePixelPNG, "contentType": "image/png",
		}}, "attachments")
		if got := str(r.state, "turns", 1, "responseParts", 0, "content"); got != "STOPPED" {
			t.Fatalf("turn 1 response = %q", got)
		}
	})

	twin.Run(t, "event-mapper", "does not split on the run's own opening prompt", func(t *testing.T) {
		// The first user message is the prompt the client already opened the turn with; splitting
		// there would produce an empty leading turn.
		r := runTurn(t, []obj{
			pi.agentStart(),
			{"type": "message_start", "message": obj{"role": "user", "content": "Hello"}},
			{"type": "message_end", "message": obj{"role": "user", "content": "Hello"}},
			pi.assistantStart(), pi.text(0, "hi"), pi.settled(),
		})
		if n := len(list(r.state, "turns")); n != 1 {
			t.Fatalf("turns = %d", n)
		}
	})

	twin.Run(t, "event-mapper", "keeps part ids unique across a split", func(t *testing.T) {
		r := runTurn(t, []obj{
			pi.agentStart(), pi.assistantStart(), pi.text(0, "before"),
			{"type": "message_start", "message": obj{"role": "user", "content": "switch"}},
			pi.assistantStart(), pi.text(0, "after"), pi.settled(),
		})
		seen := map[string]bool{}
		for _, a := range ofType(r.actions, "chat/responsePart") {
			id := str(a, "part", "id")
			if seen[id] {
				t.Fatalf("part id %q collided across the split", id)
			}
			seen[id] = true
		}
	})
}

var _ = base64.StdEncoding
