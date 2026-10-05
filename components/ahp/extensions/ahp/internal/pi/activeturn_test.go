package pi_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/active-turn-reconnect.test.ts: a client that drops in the middle of a
// turn resumes it by replay, or by snapshot when the replay buffer has moved on.

const (
	reconnectClientID = "active-turn-reconnect-client"
	activeTurnID      = "active-turn"
)

// controlledBackend records prompts and lets the test stream a response by hand.
type controlledBackend struct {
	mu        sync.Mutex
	prompts   []string
	steers    []string
	aborts    int
	listeners []func(mapper.Event)
}

func (b *controlledBackend) Subscribe(l func(mapper.Event)) func() {
	b.mu.Lock()
	b.listeners = append(b.listeners, l)
	b.mu.Unlock()
	return func() {}
}
func (b *controlledBackend) Prompt(_ context.Context, text string, _ []mapper.Image) error {
	b.mu.Lock()
	b.prompts = append(b.prompts, text)
	b.mu.Unlock()
	return nil
}
func (b *controlledBackend) Steer(_ context.Context, text string, _ []mapper.Image) error {
	b.mu.Lock()
	b.steers = append(b.steers, text)
	b.mu.Unlock()
	return nil
}
func (b *controlledBackend) Abort(context.Context) error {
	b.mu.Lock()
	b.aborts++
	b.mu.Unlock()
	return nil
}
func (b *controlledBackend) emit(t *testing.T, literal string) {
	b.mu.Lock()
	ls := append([]func(mapper.Event){}, b.listeners...)
	b.mu.Unlock()
	for _, l := range ls {
		l(event(t, literal))
	}
}
func (b *controlledBackend) startResponse(t *testing.T, text string) {
	b.emit(t, `{"type":"agent_start"}`)
	b.emit(t, `{"type":"message_start","message":{"role":"assistant"}}`)
	b.emit(t, `{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"`+text+`"}}`)
}
func (b *controlledBackend) finishResponse(t *testing.T) {
	b.emit(t, `{"type":"message_end","message":{"role":"assistant"}}`)
	b.emit(t, `{"type":"agent_end","messages":[],"willRetry":false}`)
	b.emit(t, `{"type":"agent_settled"}`)
}
func (b *controlledBackend) count(f func(*controlledBackend) int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return f(b)
}

type activeTurn struct {
	t             *testing.T
	backend       *controlledBackend
	h             *harness
	client        *testkit.Client
	session, chat string
	baseline      int64
}

func startActiveTurn(t *testing.T, replayBuffer int) *activeTurn {
	t.Helper()
	backend := &controlledBackend{}
	h := startHarness(t, harnessOptions{replayBuffer: replayBuffer, createBackend: func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }})
	client := testkit.Connect(t, h.host)
	t.Cleanup(client.Close)
	client.Initialize(reconnectClientID, nil)
	id := newID()
	f := &activeTurn{t: t, backend: backend, h: h, client: client, session: wire.SessionURI(id), chat: wire.ChatURI(id)}
	client.Must("createSession", obj{"channel": f.session})
	client.Subscribe(f.session)
	client.Subscribe(f.chat)
	client.Dispatch(f.chat, obj{"type": "chat/turnStarted", "turnId": activeTurnID, "startedAt": "2025-01-01T00:00:00.000Z", "message": userMessage("keep working")})
	testkit.Eventually(t, "the initial prompt to reach the backend", func() bool { return backend.count(func(b *controlledBackend) int { return len(b.prompts) }) == 1 })
	if a := h.host.Store().Chat(f.chat).ActiveTurn; a == nil || a.Id != activeTurnID {
		t.Fatalf("activeTurn = %+v", a)
	}
	f.baseline = h.host.ServerSeq()
	return f
}

func (f *activeTurn) reconnect(c *testkit.Client, into any) {
	f.t.Helper()
	c.Decode(c.Must("reconnect", obj{"channel": wire.RootChannel, "clientId": reconnectClientID, "lastSeenServerSeq": f.baseline, "subscriptions": []string{f.session, f.chat}}), into)
}

func responseText(state *ahptypes.ChatState) string {
	turn := (*ahptypes.Turn)(nil)
	var parts []ahptypes.ResponsePart
	if state.ActiveTurn != nil {
		parts = state.ActiveTurn.ResponseParts
	} else if n := len(state.Turns); n > 0 {
		turn = &state.Turns[n-1]
		parts = turn.ResponseParts
	}
	for _, p := range parts {
		if md, ok := p.Value.(*ahptypes.MarkdownResponsePart); ok {
			return md.Content
		}
	}
	return ""
}

func (f *activeTurn) prompts() []string {
	f.backend.mu.Lock()
	defer f.backend.mu.Unlock()
	return append([]string(nil), f.backend.prompts...)
}

func TestActiveTurnReconnect(t *testing.T) {
	twin.Run(t, "active-turn-reconnect", "replays offline output and accepts steering and cancellation after reconnect", func(t *testing.T) {
		f := startActiveTurn(t, 64)
		observer := testkit.Connect(t, f.h.host)
		t.Cleanup(observer.Close)
		observer.Initialize(nextClientID(), nil)
		observer.Subscribe(f.chat)

		f.client.Close()
		testkit.Eventually(t, "only the observer to remain subscribed", func() bool { return f.h.host.SubscriberCount(f.chat) == 1 })
		f.backend.startResponse(t, "offline partial")
		n, ok := observer.Await(func(n testkit.Notification) bool {
			e, isAction := n.Envelope()
			if !isAction {
				return false
			}
			_, part := e.Action.Value.(*ahptypes.ChatResponsePartAction)
			return part
		}, testkit.Timeout)
		if !ok {
			t.Fatal("the observer saw no response part")
		}
		observed, _ := n.Envelope()

		resumed := testkit.Connect(t, f.h.host)
		t.Cleanup(resumed.Close)
		var result struct {
			Type    string
			Actions []ahptypes.ActionEnvelope
		}
		f.reconnect(resumed, &result)
		if result.Type != "replay" {
			t.Fatalf("type = %q", result.Type)
		}
		found := false
		for _, e := range result.Actions {
			found = found || e.ServerSeq == observed.ServerSeq
		}
		if !found {
			t.Fatal("the replay lacks the action the observer saw")
		}
		if got := responseText(f.h.host.Store().Chat(f.chat)); got != "offline partial" {
			t.Fatalf("response = %q", got)
		}
		if got := f.prompts(); !reflect.DeepEqual(got, []string{"keep working"}) {
			t.Fatalf("prompts = %v", got)
		}

		resumed.Dispatch(f.chat, obj{"type": "chat/pendingMessageSet", "kind": "steering", "id": "after-reconnect", "message": userMessage("focus here")})
		testkit.Eventually(t, "reconnected steering to reach the backend", func() bool { return f.backend.count(func(b *controlledBackend) int { return len(b.steers) }) == 1 })
		resumed.Dispatch(f.chat, obj{"type": "chat/turnCancelled", "turnId": activeTurnID, "duration": 0})
		testkit.Eventually(t, "reconnected cancellation to reach the backend", func() bool { return f.backend.count(func(b *controlledBackend) int { return b.aborts }) == 1 })
		state := f.h.host.Store().Chat(f.chat)
		if state.ActiveTurn != nil || state.Turns[len(state.Turns)-1].State != ahptypes.TurnStateCancelled {
			t.Fatalf("activeTurn %+v, turns %+v", state.ActiveTurn, turnShapes(state.Turns))
		}
	})

	twin.Run(t, "active-turn-reconnect", "replays a turn that completed while its client was offline", func(t *testing.T) {
		f := startActiveTurn(t, 64)
		f.client.Close()
		testkit.Eventually(t, "the original chat subscription to disconnect", func() bool { return f.h.host.SubscriberCount(f.chat) == 0 })
		f.backend.startResponse(t, "finished offline")
		f.backend.finishResponse(t)

		resumed := testkit.Connect(t, f.h.host)
		t.Cleanup(resumed.Close)
		var result struct {
			Type    string
			Actions []ahptypes.ActionEnvelope
		}
		f.reconnect(resumed, &result)
		if result.Type != "replay" {
			t.Fatalf("type = %q", result.Type)
		}
		completed := false
		for _, e := range result.Actions {
			_, ok := e.Action.Value.(*ahptypes.ChatTurnCompleteAction)
			completed = completed || ok
		}
		if !completed {
			t.Fatal("the replay lacks chat/turnComplete")
		}
		state := f.h.host.Store().Chat(f.chat)
		if state.Turns[len(state.Turns)-1].State != ahptypes.TurnStateComplete || responseText(state) != "finished offline" {
			t.Fatalf("turns %+v response %q", turnShapes(state.Turns), responseText(state))
		}
		if got := f.prompts(); !reflect.DeepEqual(got, []string{"keep working"}) {
			t.Fatalf("prompts = %v", got)
		}
	})

	twin.Run(t, "active-turn-reconnect", "falls back to an active-turn snapshot and continues streaming", func(t *testing.T) {
		f := startActiveTurn(t, 4)
		f.client.Close()
		testkit.Eventually(t, "the original chat subscription to disconnect", func() bool { return f.h.host.SubscriberCount(f.chat) == 0 })
		f.backend.startResponse(t, "snapshot partial")
		for i := int64(0); i < 6; i++ {
			f.h.host.DispatchServerAction(wire.RootChannel, activeSessions(i))
		}
		resumed := testkit.Connect(t, f.h.host)
		t.Cleanup(resumed.Close)
		// The snapshot list also carries the session: decode chat state only for its own entry.
		raw := resumed.Must("reconnect", obj{"channel": wire.RootChannel, "clientId": reconnectClientID, "lastSeenServerSeq": f.baseline, "subscriptions": []string{f.session, f.chat}})
		var generic struct {
			Type      string
			Snapshots []struct {
				Resource string
				State    map[string]any
			}
		}
		resumed.Decode(raw, &generic)
		if generic.Type != "snapshot" {
			t.Fatalf("type = %q", generic.Type)
		}
		var chatState *ahptypes.ChatState
		for _, s := range generic.Snapshots {
			if s.Resource == f.chat {
				var st ahptypes.ChatState
				resumedJSON := testkit.JSON(t, s.State)
				if err := jsonUnmarshal(resumedJSON, &st); err != nil {
					t.Fatal(err)
				}
				chatState = &st
			}
		}
		if chatState == nil || responseText(chatState) != "snapshot partial" {
			t.Fatalf("chat snapshot = %+v", chatState)
		}
		f.backend.finishResponse(t)
		if _, ok := resumed.Await(func(n testkit.Notification) bool {
			e, isAction := n.Envelope()
			if !isAction {
				return false
			}
			_, done := e.Action.Value.(*ahptypes.ChatTurnCompleteAction)
			return done
		}, testkit.Timeout); !ok {
			t.Fatal("the resumed client never saw the turn complete")
		}
		state := f.h.host.Store().Chat(f.chat)
		if state.Turns[len(state.Turns)-1].State != ahptypes.TurnStateComplete {
			t.Fatalf("turns %+v", turnShapes(state.Turns))
		}
		if got := f.prompts(); !reflect.DeepEqual(got, []string{"keep working"}) {
			t.Fatalf("prompts = %v", got)
		}
	})
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func activeSessions(n int64) ahptypes.StateAction {
	return ahptypes.StateAction{Value: &ahptypes.RootActiveSessionsChangedAction{Type: ahptypes.ActionTypeRootActiveSessionsChanged, ActiveSessions: n}}
}
