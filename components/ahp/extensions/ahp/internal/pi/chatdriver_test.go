package pi_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/chat-driver.test.ts: the chat driver end to end: a client turn reaching a
// backend, streamed output coming back as actions, and queued-message consumption. The backend
// is a scripted fake rather than a real agent: the point is the host's turn arbitration.

func scripted(t *testing.T) *scriptedBackend {
	return newScriptedBackend(func(text string) []mapper.Event {
		if text == "long running" {
			return nil
		}
		return say(t, "echo: "+text)
	})
}

func turnCount(f *fixture, n int) func() bool {
	return func() bool { return len(f.chat().Turns) == n }
}

func TestChatDriver(t *testing.T) {
	twin.Run(t, "chat-driver", "creates the session's default chat and points defaultChat at it", func(t *testing.T) {
		f := startFixture(t, scripted(t))
		session := f.session()
		if session.Lifecycle != ahptypes.SessionLifecycleReady {
			t.Fatalf("lifecycle = %s", session.Lifecycle)
		}
		if len(session.Chats) != 1 || session.Chats[0].Resource != f.chatChannel {
			t.Fatalf("chats = %+v", session.Chats)
		}
		if session.DefaultChat == nil || *session.DefaultChat != f.chatChannel {
			t.Fatalf("defaultChat = %v", session.DefaultChat)
		}
	})

	twin.Run(t, "chat-driver", "runs a client turn through the backend and streams the reply back", func(t *testing.T) {
		b := scripted(t)
		f := startFixture(t, b)
		f.turnStarted("t1", userMessage("hello"))
		testkit.Eventually(t, "the streamed turn to complete", turnCount(f, 1))
		if got := b.promptList(); !reflect.DeepEqual(got, []string{"hello"}) {
			t.Fatalf("prompts = %v", got)
		}
		turn := f.chat().Turns[0]
		if turn.State != ahptypes.TurnStateComplete {
			t.Fatalf("state = %s", turn.State)
		}
		part, ok := turn.ResponseParts[0].Value.(*ahptypes.MarkdownResponsePart)
		if !ok || part.Content != "echo: hello" {
			t.Fatalf("first part = %+v", turn.ResponseParts[0].Value)
		}
	})

	twin.Run(t, "chat-driver", "adapts supported attachments into pi prompt text", func(t *testing.T) {
		b := scripted(t)
		f := startFixture(t, b)
		text := "inspect the path from the client"
		expected := text + "\n\n/outside.ts\n\nselected context\n\nembedded context"
		message := userMessage(text)
		message["attachments"] = []any{
			obj{"type": "resource", "label": "outside.ts", "uri": "file:///outside.ts"},
			obj{"type": "simple", "label": "selection", "modelRepresentation": "selected context"},
			embeddedText("embedded context", "note.txt"),
			embeddedImage("screenshot.png"),
		}
		f.turnStarted("t-path", message)
		testkit.Eventually(t, "the attachment-expanded prompt to reach the backend", func() bool {
			for _, p := range b.promptList() {
				if p == expected {
					return true
				}
			}
			return false
		})
		b.mu.Lock()
		defer b.mu.Unlock()
		if got := b.prompts[len(b.prompts)-1]; got != expected {
			t.Fatalf("prompt = %q", got)
		}
		want := []mapper.Image{{Type: "image", Data: onePixelPNG, MimeType: "image/png"}}
		if got := b.promptImages[len(b.promptImages)-1]; !reflect.DeepEqual(got, want) {
			t.Fatalf("images = %+v", got)
		}
	})

	twin.Run(t, "chat-driver", "keeps the session catalog's chat summary in step", func(t *testing.T) {
		f := startFixture(t, scripted(t))
		f.turnStarted("t-summary", userMessage("update the summary"))
		testkit.Eventually(t, "the summary-driving turn to complete", turnCount(f, 1))
		testkit.Eventually(t, "the session summary to follow the chat", func() bool {
			s, c := f.session(), f.chat()
			return s.Chats[0].Status == c.Status && s.Chats[0].ModifiedAt == c.ModifiedAt
		})
	})

	twin.Run(t, "chat-driver", "forwards steering text and images to the active backend", func(t *testing.T) {
		b := scripted(t)
		f := startFixture(t, b)
		f.turnStarted("t-steering", userMessage("long running"))
		testkit.Eventually(t, "the steerable prompt to reach the backend", func() bool { return len(b.promptList()) == 1 })
		steer := userMessage("focus on tests")
		steer["attachments"] = []any{
			obj{"type": "simple", "label": "context", "modelRepresentation": "steering context"},
			embeddedImage("steering.png"),
		}
		f.pending("steering", "steer-1", steer)
		testkit.Eventually(t, "the steering message to reach the backend", func() bool {
			b.mu.Lock()
			defer b.mu.Unlock()
			return len(b.steers) == 1
		})
		b.mu.Lock()
		defer b.mu.Unlock()
		if !reflect.DeepEqual(b.steers, []string{"focus on tests\n\nsteering context"}) {
			t.Fatalf("steers = %v", b.steers)
		}
		if want := []mapper.Image{{Type: "image", Data: onePixelPNG, MimeType: "image/png"}}; !reflect.DeepEqual(b.steerImages[0], want) {
			t.Fatalf("images = %+v", b.steerImages[0])
		}
	})

	twin.Run(t, "chat-driver", "consumes a queued message as its own turn once the chat goes idle", func(t *testing.T) {
		// Queued messages never reach pi: the protocol's own state is the queue, and the host
		// starts a fresh turn for the head entry when idle.
		b := scripted(t)
		f := startFixture(t, b)
		message := userMessage("then do this")
		message["attachments"] = []any{embeddedText("queued context", "queued.txt"), embeddedImage("queued.png")}
		f.pending("queued", "q-1", message)
		testkit.Eventually(t, "the queued prompt to reach the backend", func() bool { return len(b.promptList()) == 1 })
		testkit.Eventually(t, "the queued turn to complete", func() bool {
			c := f.chat()
			return len(c.Turns) == 1 && c.Turns[0].State == ahptypes.TurnStateComplete
		})
		b.mu.Lock()
		if got := b.prompts[len(b.prompts)-1]; got != "then do this\n\nqueued context" {
			t.Fatalf("prompt = %q", got)
		}
		if want := []mapper.Image{{Type: "image", Data: onePixelPNG, MimeType: "image/png"}}; !reflect.DeepEqual(b.promptImages[0], want) {
			t.Fatalf("images = %+v", b.promptImages[0])
		}
		b.mu.Unlock()
		// The reducer removes the entry atomically with creating the turn, so a client can never
		// see it both queued and running.
		if q := f.chat().QueuedMessages; len(q) != 0 {
			t.Fatalf("queuedMessages = %+v", q)
		}
	})

	twin.Run(t, "chat-driver", "holds a queued message until the active turn settles", func(t *testing.T) {
		b := scripted(t)
		f := startFixture(t, b)
		f.turnStarted("t-blocking", userMessage("long running"))
		testkit.Eventually(t, "the active prompt to reach the backend", func() bool { return len(b.promptList()) == 1 })
		f.pending("queued", "q-after-active", userMessage("run after"))
		f.client.Ping()
		waiting := f.chat()
		if len(b.promptList()) != 1 {
			t.Fatal("queued message prompted before the active turn settled")
		}
		if waiting.ActiveTurn == nil || waiting.ActiveTurn.Id != "t-blocking" {
			t.Fatalf("activeTurn = %+v", waiting.ActiveTurn)
		}
		if len(waiting.QueuedMessages) == 0 || waiting.QueuedMessages[0].Id != "q-after-active" {
			t.Fatalf("queued = %+v", waiting.QueuedMessages)
		}
		b.emit(event(t, `{"type":"agent_settled"}`))
		testkit.Eventually(t, "the queued message to start after settlement", func() bool { return len(b.promptList()) == 2 })
		testkit.Eventually(t, "the queued turn to complete", func() bool {
			c := f.chat()
			return len(c.Turns) == 2 && c.Turns[1].State == ahptypes.TurnStateComplete && len(c.QueuedMessages) == 0
		})
		completed := f.chat()
		if got := b.promptList(); got[len(got)-1] != "run after" {
			t.Fatalf("prompts = %v", got)
		}
		if completed.Turns[len(completed.Turns)-1].Message.Text != "run after" {
			t.Fatalf("turns = %+v", turnShapes(completed.Turns))
		}
	})

	twin.Run(t, "chat-driver", "does not start a prompt cancelled during model selection", func(t *testing.T) {
		b := scripted(t)
		b.selectionGate = newRelease()
		defer b.selectionGate.open()
		f := startFixture(t, b)
		message := userMessage("do not run")
		message["model"] = obj{"id": "pi/test-model"}
		f.turnStarted("t-select-cancel", message)
		testkit.Eventually(t, "model selection to start", func() bool { return b.count(func(b *scriptedBackend) int { return b.selections }) == 1 })
		f.pending("queued", "after-select-cancel", userMessage("run after selection cancellation"))
		f.cancel("t-select-cancel")
		testkit.Eventually(t, "preflight cancellation to reach the backend", func() bool { return b.count(func(b *scriptedBackend) int { return b.aborts }) == 1 })

		b.selectionGate.open()
		testkit.Eventually(t, "queued work to complete after cancelled preflight settles", turnCount(f, 2))
		if got := b.promptList(); !reflect.DeepEqual(got, []string{"run after selection cancellation"}) {
			t.Fatalf("prompts = %v", got)
		}
		completed := f.chat()
		want := []turnShape{
			{"t-select-cancel", "do not run", ahptypes.TurnStateCancelled},
			{"turn-after-select-cancel", "run after selection cancellation", ahptypes.TurnStateComplete},
		}
		testkit.Eventually(t, "the queued turn to complete", func() bool { return f.chat().Turns[1].State == ahptypes.TurnStateComplete })
		completed = f.chat()
		if got := turnShapes(completed.Turns); !reflect.DeepEqual(got, want) {
			t.Fatalf("turns = %+v, want %+v", got, want)
		}

		before := turnShapes(completed.Turns)
		f.client.Dispatch(f.chatChannel, obj{"type": "chat/truncated", "turnId": "t-select-cancel"})
		f.client.Ping()
		if got := turnShapes(f.chat().Turns); !reflect.DeepEqual(got, before) {
			t.Fatalf("a turn cancelled before persistence must not acquire a truncation anchor: %+v", got)
		}
	})

	twin.Run(t, "chat-driver", "does not run a prompt cancelled during backend preflight", func(t *testing.T) {
		b := scripted(t)
		b.promptGate = newRelease()
		defer b.promptGate.open()
		f := startFixture(t, b)
		message := userMessage("do not run")
		message["attachments"] = []any{embeddedImage("screenshot.png")}
		f.turnStarted("t-preflight-cancel", message)
		testkit.Eventually(t, "backend preflight to start", func() bool { return b.count(func(b *scriptedBackend) int { return b.promptCalls }) == 1 })
		f.pending("queued", "after-preflight-cancel", userMessage("run after image preflight"))
		f.cancel("t-preflight-cancel")
		testkit.Eventually(t, "preflight cancellation to reach the backend", func() bool { return b.count(func(b *scriptedBackend) int { return b.aborts }) == 1 })

		b.promptGate.open()
		testkit.Eventually(t, "queued work to complete after backend preflight settles", func() bool {
			c := f.chat()
			return len(c.Turns) == 2 && c.Turns[1].State == ahptypes.TurnStateComplete
		})
		if got := b.promptList(); !reflect.DeepEqual(got, []string{"run after image preflight"}) {
			t.Fatalf("prompts = %v", got)
		}
		want := []turnShape{
			{"t-preflight-cancel", "do not run", ahptypes.TurnStateCancelled},
			{"turn-after-preflight-cancel", "run after image preflight", ahptypes.TurnStateComplete},
		}
		if got := turnShapes(f.chat().Turns); !reflect.DeepEqual(got, want) {
			t.Fatalf("turns = %+v, want %+v", got, want)
		}
	})

	twin.Run(t, "chat-driver", "anchors a cancelled turn before resuming queued work", func(t *testing.T) {
		b := scripted(t)
		f := startFixture(t, b)
		f.turnStarted("t-cancel", userMessage("long running"))
		testkit.Eventually(t, "the cancellable prompt to reach the backend", func() bool { return len(b.promptList()) == 1 })
		if a := f.chat().ActiveTurn; a == nil || a.Id != "t-cancel" {
			t.Fatalf("activeTurn = %+v", a)
		}

		live, ok := f.sessions.Get(f.sessionChannel)
		if !ok {
			t.Fatal("no live session")
		}
		// AgentSession notifies listeners, then records the same user message.
		user := `{"role":"user","content":"long running","timestamp":0}`
		b.emit(event(t, `{"type":"message_start","message":`+user+`}`))
		b.emit(event(t, `{"type":"message_end","message":`+user+`}`))
		manager := live.SessionManager.(*pisession.Manager)
		if _, err := manager.AppendMessage(event(t, user)); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(event(t, `{"role":"assistant","content":[],"stopReason":"aborted","timestamp":0}`)); err != nil {
			t.Fatal(err)
		}

		f.pending("queued", "after-cancel", userMessage("run after cancel"))
		f.cancel("t-cancel")
		testkit.Eventually(t, "cancellation to reach the backend", func() bool { return b.count(func(b *scriptedBackend) int { return b.aborts }) == 1 })
		testkit.Eventually(t, "queued work to complete after backend cancellation", func() bool {
			c := f.chat()
			return len(c.Turns) == 2 && c.Turns[1].State == ahptypes.TurnStateComplete
		})
		chat := f.chat()
		if chat.Turns[0].Id != "t-cancel" || chat.Turns[0].State != ahptypes.TurnStateCancelled {
			t.Fatalf("turns = %+v", turnShapes(chat.Turns))
		}
		if chat.Turns[1].Message.Text != "run after cancel" {
			t.Fatalf("turns = %+v", turnShapes(chat.Turns))
		}
		testkit.Eventually(t, "the session summary to follow the chat", func() bool { return f.session().Chats[0].Status == f.chat().Status })

		f.client.Dispatch(f.chatChannel, obj{"type": "chat/truncated", "turnId": "t-cancel"})
		f.client.Ping()
		var ids []string
		for _, turn := range f.chat().Turns {
			ids = append(ids, turn.Id)
		}
		if !reflect.DeepEqual(ids, []string{"t-cancel"}) {
			t.Fatalf("a recorded cancelled turn must remain a valid truncation target; turns = %v", ids)
		}
	})
}

func TestChatDriverBackendFailure(t *testing.T) {
	twin.Run(t, "chat-driver", "marks the session failed when the backend cannot start", func(t *testing.T) {
		h, reg := startWith(t, pi.RegistryOptions{CreateBackend: func(*pi.LiveSession) (pi.Backend, error) {
			return nil, errors.New("no credentials")
		}})
		uri := wire.SessionURI(newID())
		if err := reg.Create(context.Background(), ahptypes.CreateSessionParams{Channel: uri}); err != nil {
			t.Fatal(err)
		}
		testkit.Eventually(t, "backend startup failure to reach session state", func() bool {
			return h.Store().Session(uri).Lifecycle != ahptypes.SessionLifecycleCreating
		})
		state := h.Store().Session(uri)
		if state.Lifecycle != ahptypes.SessionLifecycleFailed {
			t.Fatalf("lifecycle = %s", state.Lifecycle)
		}
		if state.CreationError == nil || !strings.Contains(state.CreationError.Message, "no credentials") {
			t.Fatalf("creationError = %+v", state.CreationError)
		}
	})

	twin.Run(t, "chat-driver", "closes the turn when prompt() rejects before any agent event", func(t *testing.T) {
		backend := funcBackend{prompt: func(context.Context, string) error { return errors.New("model unavailable") }}
		f := startFixture(t, backend)
		// Dispatched through the wire so the reducer creates the active turn before the side
		// effect runs: the same ordering production relies on.
		f.turnStarted("t1", userMessage("hi"))
		// Nothing will ever emit agent_settled, so without explicit handling the turn would stay
		// active and the session stuck at InProgress.
		testkit.Eventually(t, "the rejected prompt to close its turn", turnCount(f, 1))
		state := f.chat()
		if state.ActiveTurn != nil {
			t.Fatalf("activeTurn = %+v", state.ActiveTurn)
		}
		turn := state.Turns[0]
		if turn.State != ahptypes.TurnStateError {
			t.Fatalf("state = %s", turn.State)
		}
		last, ok := turn.ResponseParts[len(turn.ResponseParts)-1].Value.(*ahptypes.ErrorResponsePart)
		if !ok || !strings.Contains(last.Error.Message, "model unavailable") {
			t.Fatalf("error part = %+v", turn.ResponseParts)
		}
		testkit.Eventually(t, "the session summary to follow the chat", func() bool {
			return f.session().Chats[0].Status == f.chat().Status
		})
		overrides := f.sessions.CatalogueOverrides()
		if len(overrides) == 0 || overrides[0].Summary.Status != f.chat().Status {
			t.Fatalf("catalogue overrides = %+v", overrides)
		}
	})

	twin.Run(t, "chat-driver", "clears the pending message once pi consumes it", func(t *testing.T) {
		// The failure this covers looks like a hang: pi injects the steering message into the
		// run, but ChatState.steeringMessage never clears, so the client shows it as forever
		// unsent even though the model got it.
		var mu sync.Mutex
		steered := make(chan struct{})
		var listeners []func(mapper.Event)
		emit := func(literal string) {
			mu.Lock()
			ls := append([]func(mapper.Event){}, listeners...)
			mu.Unlock()
			for _, l := range ls {
				l(event(t, literal))
			}
		}
		backend := funcBackend{
			subscribe: func(l func(mapper.Event)) func() {
				mu.Lock()
				listeners = append(listeners, l)
				mu.Unlock()
				return func() {}
			},
			prompt: func(context.Context, string) error { emit(`{"type":"agent_start"}`); return nil },
			// pi acknowledges the message by growing its queue...
			steer: func(context.Context, string) error {
				emit(`{"type":"queue_update","steering":["focus on tests"],"followUp":[]}`)
				close(steered)
				return nil
			},
		}
		f := startFixture(t, backend)
		f.turnStarted("t1", userMessage("go"))
		f.pending("steering", "steer-1", userMessage("focus on tests"))
		testkit.Eventually(t, "the steering message to enter protocol state", func() bool { return f.chat().SteeringMessage != nil })
		// The side effect runs on its own goroutine here (upstream's runs inline in the same
		// tick), so wait for pi's acknowledgement before consuming.
		<-steered
		// ...and consumes it by shrinking the queue right before injecting.
		emit(`{"type":"queue_update","steering":[],"followUp":[]}`)
		testkit.Eventually(t, "the consumed steering message to leave protocol state", func() bool { return f.chat().SteeringMessage == nil })
		// The text itself is not recorded here: pi delivers it as an ordinary user message, and
		// the mapper turns that into its own turn. Only the pending slot is cleared by this path.
	})
}

var _ = host.Options{}
