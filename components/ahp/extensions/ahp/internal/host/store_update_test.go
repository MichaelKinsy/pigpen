package host_test

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// UpdateChat is a read-modify-write under the store lock: edits racing with reduced actions must
// never drop either side (a title rewrite that replaced the whole state once erased a running turn).
func TestUpdateChatDoesNotLoseConcurrentActions(t *testing.T) {
	const turns = 200
	s := testkit.NewHost(host.Options{}).Store()
	uri := "ahp-chat:/00000000-0000-7000-8000-000000000001"
	if err := s.Create(uri, &ahptypes.ChatState{Resource: uri, Title: "t"}, wire.KindChat); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < turns; i++ {
			s.Apply(uri, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{Type: ahptypes.ActionTypeChatTurnStarted, TurnId: "x", StartedAt: "2025-01-01T00:00:00.000Z"}})
			s.Apply(uri, ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{Type: ahptypes.ActionTypeChatTurnComplete, TurnId: "x", Duration: 1}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < turns; i++ {
			s.UpdateChat(uri, func(c *ahptypes.ChatState) bool { c.Title = "renamed"; return true })
		}
	}()
	wg.Wait()
	got := s.Chat(uri)
	if len(got.Turns) != turns || got.ActiveTurn != nil {
		t.Fatalf("turns = %d active = %v, want %d complete turns", len(got.Turns), got.ActiveTurn != nil, turns)
	}
	if got.Title != "renamed" {
		t.Fatalf("title = %q", got.Title)
	}
	if s.UpdateChat("ahp-chat:/missing", func(*ahptypes.ChatState) bool { return true }) {
		t.Fatal("updating a missing chat reported a change")
	}
}

// An action reduced while an edit is in flight waits for it: the edit sees, and keeps, whatever
// came before, and the action lands after (deterministic form of the race above; needs no -race).
func TestUpdateChatExcludesReducers(t *testing.T) {
	s := testkit.NewHost(host.Options{}).Store()
	uri := "ahp-chat:/00000000-0000-7000-8000-000000000002"
	if err := s.Create(uri, &ahptypes.ChatState{Resource: uri}, wire.KindChat); err != nil {
		t.Fatal(err)
	}
	inside, applied := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.UpdateChat(uri, func(c *ahptypes.ChatState) bool {
			close(inside)
			go func() {
				s.Apply(uri, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{Type: ahptypes.ActionTypeChatTurnStarted, TurnId: "x", StartedAt: "2025-01-01T00:00:00.000Z"}})
				close(applied)
			}()
			select {
			case <-applied:
				t.Error("an action was reduced while the edit held the chat")
			case <-time.After(50 * time.Millisecond):
			}
			c.Title = "edited"
			return true
		})
	}()
	<-inside
	<-done
	<-applied
	got := s.Chat(uri)
	if got.Title != "edited" || got.ActiveTurn == nil {
		t.Fatalf("title=%q active=%v: the edit and the action must both survive", got.Title, got.ActiveTurn != nil)
	}
}
