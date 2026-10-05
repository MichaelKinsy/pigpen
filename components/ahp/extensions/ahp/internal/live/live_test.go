package live

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
)

// Additions: the adapter's event handling, which needs no host (the SDK-facing calls are proved
// against a real pig in the root package's real-pig tests).

func collect(s *Session) (*[]mapper.Event, func()) {
	var got []mapper.Event
	unsub := s.Subscribe(func(e mapper.Event) { got = append(got, e) })
	return &got, unsub
}

func TestDeliverAddsTheTypeAndDropsSystemMessages(t *testing.T) {
	s := New()
	got, _ := collect(s)
	s.deliver("message_start", map[string]any{"message": map[string]any{"role": "system", "content": ""}})
	s.deliver("message_end", map[string]any{"message": map[string]any{"role": "system", "content": ""}})
	s.deliver("agent_start", map[string]any{})
	s.deliver("message_start", map[string]any{"message": map[string]any{"role": "assistant"}})
	types := []any{}
	for _, e := range *got {
		types = append(types, e["type"])
	}
	if !reflect.DeepEqual(types, []any{"agent_start", "message_start"}) {
		t.Fatalf("delivered %v", types)
	}
}

func TestSubscribersAreIndependent(t *testing.T) {
	s := New()
	a, unsubA := collect(s)
	b, _ := collect(s)
	s.deliver("agent_start", map[string]any{})
	unsubA()
	s.deliver("agent_end", map[string]any{})
	if len(*a) != 1 || len(*b) != 2 {
		t.Fatalf("a=%d b=%d", len(*a), len(*b))
	}
}

func TestSteeringIsReportedConsumedWhenItsUserMessageArrives(t *testing.T) {
	s := New()
	got, _ := collect(s)
	s.steering = []string{"first", "second"}
	user := func(text string) map[string]any {
		return map[string]any{"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}}
	}
	s.deliver("message_start", user("first"))
	// the user message itself, then the synthesised queue_update carrying what is still pending
	if len(*got) != 2 || (*got)[1]["type"] != "queue_update" || !reflect.DeepEqual((*got)[1]["steering"], []any{"second"}) {
		t.Fatalf("events %v", *got)
	}
	s.deliver("message_start", user("not queued"))
	if len(*got) != 3 {
		t.Fatalf("an unrelated user message must not emit a queue_update: %v", *got)
	}
}

func TestOnlyTheRunningSessionHasABackend(t *testing.T) {
	s := New()
	if _, err := s.BackendFactory(&pi.LiveSession{SessionID: "x"}); err == nil {
		t.Fatal("no session is running yet")
	}
	s.id = "live-id"
	if b, err := s.BackendFactory(&pi.LiveSession{SessionID: "live-id"}); err != nil || b == nil {
		t.Fatalf("the running session must get its backend: %v", err)
	}
	if _, err := s.BackendFactory(&pi.LiveSession{SessionID: "other"}); err == nil {
		t.Fatal("another session must not get a backend")
	}
}

func TestCallsWithoutASessionFailClearly(t *testing.T) {
	s := New()
	if _, err := s.Context(); err == nil {
		t.Fatal("no session, no context")
	}
	if err := s.Abort(nil); err == nil {
		t.Fatal("abort without a session must fail")
	}
	if s.CurrentSelection() != nil || s.Models() != nil {
		t.Fatal("nothing to report without a session")
	}
}
