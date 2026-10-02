package pi_test

import (
	"testing"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Additions (not upstream twins): a turn nobody dispatched through AHP, typed into the running
// PiG session, is mirrored into the chat when AdoptForeignTurns is on; upstream's host owns its
// agent, so it never sees one and ignores such events (which stays the default).

func foreignFixture(t *testing.T, adopt bool) (*fixture, *scriptedBackend) {
	t.Helper()
	backend := newScriptedBackend(func(string) []mapper.Event { return nil })
	h, reg := startWith(t, pi.RegistryOptions{
		CreateBackend:     func(*pi.LiveSession) (pi.Backend, error) { return backend, nil },
		AdoptForeignTurns: adopt,
	})
	client := testkit.Connect(t, h)
	t.Cleanup(client.Close)
	client.Initialize("foreign-client", nil)
	id := newID()
	f := &fixture{t: t, host: h, sessions: reg, client: client, id: id, sessionChannel: wire.SessionURI(id), chatChannel: wire.ChatURI(id)}
	client.Must("createSession", obj{"channel": f.sessionChannel})
	client.Subscribe(f.sessionChannel)
	client.Subscribe(f.chatChannel)
	testkit.Eventually(t, "the session to become ready", func() bool {
		s := f.session()
		return s != nil && s.Lifecycle == ahptypes.SessionLifecycleReady
	})
	return f, backend
}

func localPrompt(t *testing.T, backend *scriptedBackend, text, answer string) {
	backend.emit(event(t, `{"type":"agent_start"}`))
	backend.emit(event(t, `{"type":"turn_start"}`))
	backend.emit(event(t, `{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"`+text+`"}]}}`))
	backend.emit(event(t, `{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"`+text+`"}]}}`))
	for _, e := range say(t, answer)[1:] {
		backend.emit(e)
	}
}

func TestForeignTurnsAreMirrored(t *testing.T) {
	f, backend := foreignFixture(t, true)
	localPrompt(t, backend, "typed locally", "answered locally")
	testkit.Eventually(t, "the local turn to complete", func() bool {
		c := f.chat()
		return c != nil && len(c.Turns) == 1 && len(c.Turns[0].ResponseParts) > 0 && c.Activity == nil
	})
	turn := f.chat().Turns[0]
	if turn.Message.Text != "typed locally" {
		t.Fatalf("user message %q", turn.Message.Text)
	}
	if len(turn.ResponseParts) == 0 {
		t.Fatal("the local answer never reached the chat")
	}
	// a second local prompt is a second turn
	localPrompt(t, backend, "again", "second")
	testkit.Eventually(t, "the second local turn", func() bool { return len(f.chat().Turns) == 2 })
}

func TestForeignTurnsAreIgnoredByDefault(t *testing.T) {
	f, backend := foreignFixture(t, false)
	localPrompt(t, backend, "typed locally", "answered locally")
	if !f.client.Silent(func(n testkit.Notification) bool { env, ok := n.Envelope(); return ok && env.Channel == f.chatChannel }, 200_000_000) {
		t.Fatal("a turn nobody started must be ignored unless AdoptForeignTurns is set")
	}
	if n := len(f.chat().Turns); n != 0 {
		t.Fatalf("%d turns", n)
	}
	_ = host.Options{}
	_ = mapper.Event{}
}

func TestCancelledTailIsNotAdopted(t *testing.T) {
	f, backend := foreignFixture(t, true)
	f.turnStarted("remote-1", userMessage("go"))
	testkit.Eventually(t, "the prompt to reach the backend", func() bool { return backend.count(func(b *scriptedBackend) int { return b.promptCalls }) == 1 })
	f.cancel("remote-1")
	testkit.Eventually(t, "the cancel to reach the backend", func() bool { return backend.count(func(b *scriptedBackend) int { return b.aborts }) >= 1 })
	// the aborted run's trailing events carry no user message, so they start nothing
	for _, e := range say(t, "late")[1:] {
		backend.emit(e)
	}
	if n := len(f.chat().Turns); n != 1 {
		t.Fatalf("%d turns after a cancelled run's tail", n)
	}
}

// While a cancellation is settling, a user message must not open a turn: it can only be the cancelled
// run's own input replaying, and adopting it would resurrect the turn the client just cancelled.
func TestNothingIsAdoptedWhileACancellationSettles(t *testing.T) {
	f, backend := foreignFixture(t, true)
	gate := newRelease()
	backend.mu.Lock()
	backend.abortGate = gate
	backend.mu.Unlock()
	f.turnStarted("remote-1", userMessage("go"))
	testkit.Eventually(t, "the prompt to reach the backend", func() bool { return backend.count(func(b *scriptedBackend) int { return b.promptCalls }) == 1 })
	f.cancel("remote-1")
	testkit.Eventually(t, "the abort to be in flight", func() bool { return backend.count(func(b *scriptedBackend) int { return b.aborts }) == 1 })

	backend.emit(event(t, `{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"replayed"}]}}`))
	if n, active := len(f.chat().Turns), f.chat().ActiveTurn; n != 1 || active != nil {
		t.Fatalf("a message during the settling cancellation opened a turn: %d turns, active=%v", n, active != nil)
	}

	gate.open()
	// The settling has no signal of its own: a user message is offered until one is adopted.
	testkit.Eventually(t, "a local prompt after the cancellation to be mirrored", func() bool {
		backend.emit(event(t, `{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"typed later"}]}}`))
		return f.chat().ActiveTurn != nil
	})
}
