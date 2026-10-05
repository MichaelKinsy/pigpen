package host

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/microsoft/agent-host-protocol/clients/go/ahp"
	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// maxRetainedTerminalChars bounds the output kept for snapshots; live clients still receive every action.
const maxRetainedTerminalChars = 1_000_000

// Store holds the state tree of every state-bearing channel. Every state is mutated only by
// actions run through the protocol's reducers (the vendored Microsoft Go reducers), and reads
// return deep copies, so a caller never observes a later mutation. Port of src/core/state-store.ts.
type Store struct {
	mu       sync.RWMutex
	channels map[string]*entry
}

type entry struct {
	kind  wire.ChannelKind
	state any // *RootState, *SessionState, *ChatState, *TerminalState or *ResourceWatchState
}

func newStore() *Store { return &Store{channels: map[string]*entry{}} }

// Create registers a channel with its initial state (a pointer to the typed state). The kind is
// inferred from the scheme unless given, which callers do when a command establishes the kind of
// a client-chosen URI. It replaces any existing entry.
func (s *Store) Create(uri string, state any, kind ...wire.ChannelKind) error {
	var k wire.ChannelKind
	if len(kind) > 0 && kind[0] != "" {
		k = kind[0]
	} else if inferred, ok := wire.KindOf(uri); ok {
		k = inferred
	} else {
		return fmt.Errorf("cannot create channel with unknown scheme: %s", uri)
	}
	if !stateMatchesKind(state, k) {
		return fmt.Errorf("state %T does not belong to a %s channel", state, k)
	}
	s.mu.Lock()
	s.channels[uri] = &entry{kind: k, state: cloneState(state)}
	s.mu.Unlock()
	return nil
}

// UpdateChat edits a chat's state in place under the store lock, so an edit cannot overwrite an
// action reduced between reading the state and writing it back. fn reports whether it changed
// anything; UpdateChat returns false when the chat does not exist or fn made no change.
func (s *Store) UpdateChat(uri string, fn func(*ahptypes.ChatState) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.channels[uri]
	if !ok {
		return false
	}
	chat, ok := e.state.(*ahptypes.ChatState)
	if !ok {
		return false
	}
	return fn(chat)
}

func stateMatchesKind(state any, kind wire.ChannelKind) bool {
	switch state.(type) {
	case *ahptypes.RootState:
		return kind == wire.KindRoot
	case *ahptypes.SessionState:
		return kind == wire.KindSession
	case *ahptypes.ChatState:
		return kind == wire.KindChat
	case *ahptypes.TerminalState:
		return kind == wire.KindTerminal
	case *ahptypes.ChangesetState:
		return kind == wire.KindChangeset
	case *ahptypes.ResourceWatchState:
		return kind == wire.KindResourceWatch
	}
	return false
}

// cloneState deep-copies a typed state through its JSON form (the wire form is the contract).
func cloneState(state any) any {
	switch v := state.(type) {
	case *ahptypes.RootState:
		return cloneVia(v)
	case *ahptypes.SessionState:
		return cloneVia(v)
	case *ahptypes.ChatState:
		return cloneVia(v)
	case *ahptypes.TerminalState:
		return cloneVia(v)
	case *ahptypes.ChangesetState:
		return cloneVia(v)
	case *ahptypes.ResourceWatchState:
		return cloneVia(v)
	}
	return state
}

func cloneVia[T any](v *T) *T {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("host: state is not marshalable: %v", err))
	}
	out := new(T)
	if err := json.Unmarshal(b, out); err != nil {
		panic(fmt.Sprintf("host: state does not round-trip: %v", err))
	}
	return out
}

// Delete removes a channel.
func (s *Store) Delete(uri string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.channels[uri]
	delete(s.channels, uri)
	return ok
}

// Has reports whether a channel exists.
func (s *Store) Has(uri string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.channels[uri]
	return ok
}

// KindOf returns the kind of an existing channel.
func (s *Store) KindOf(uri string) (wire.ChannelKind, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.channels[uri]
	if !ok {
		return "", false
	}
	return e.kind, true
}

func get[T any](s *Store, uri string) *T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.channels[uri]
	if !ok {
		return nil
	}
	typed, ok := e.state.(*T)
	if !ok {
		return nil
	}
	return cloneVia(typed)
}

// Root, Session, Chat, Terminal, ResourceWatch return a deep copy of the typed state, nil when
// the channel is absent or of another kind.
func (s *Store) Root(uri string) *ahptypes.RootState       { return get[ahptypes.RootState](s, uri) }
func (s *Store) Session(uri string) *ahptypes.SessionState { return get[ahptypes.SessionState](s, uri) }
func (s *Store) Chat(uri string) *ahptypes.ChatState       { return get[ahptypes.ChatState](s, uri) }
func (s *Store) Terminal(uri string) *ahptypes.TerminalState {
	return get[ahptypes.TerminalState](s, uri)
}
func (s *Store) ResourceWatch(uri string) *ahptypes.ResourceWatchState {
	return get[ahptypes.ResourceWatchState](s, uri)
}

// Apply reduces an action into a channel using the reducer for its kind. Reducers are total: an
// action they do not recognise is a no-op, which is how forward compatibility works. It returns
// false when the channel does not exist — the spec requires the host to silently ignore actions
// targeting an unknown channel rather than echoing a rejection.
func (s *Store) Apply(uri string, action ahptypes.StateAction) (applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.channels[uri]
	if !ok {
		return false
	}
	// The vendored reducers panic on a malformed timestamp; a hostile or buggy client must not take the host down.
	defer func() {
		if r := recover(); r != nil {
			applied = true
		}
	}()
	switch st := e.state.(type) {
	case *ahptypes.RootState:
		ahp.ApplyActionToRoot(st, action)
	case *ahptypes.SessionState:
		ahp.ApplyActionToSession(st, action)
	case *ahptypes.ChatState:
		ahp.ApplyActionToChat(st, action)
	case *ahptypes.TerminalState:
		ahp.ApplyActionToTerminal(st, action)
		if _, isData := action.Value.(*ahptypes.TerminalDataAction); isData {
			trimTerminal(st)
		}
	case *ahptypes.ChangesetState:
		ahp.ApplyActionToChangeset(st, action)
	case *ahptypes.ResourceWatchState:
		ahp.ApplyActionToResourceWatch(st, action)
	}
	return true
}

// Snapshot builds a subscribe/initialize snapshot for a channel at the given seq.
func (s *Store) Snapshot(uri string, fromSeq int64) (ahptypes.Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.channels[uri]
	if !ok {
		return ahptypes.Snapshot{}, false
	}
	var state ahptypes.SnapshotState
	switch st := e.state.(type) {
	case *ahptypes.RootState:
		state.Root = cloneVia(st)
	case *ahptypes.SessionState:
		state.Session = cloneVia(st)
	case *ahptypes.ChatState:
		state.Chat = cloneVia(st)
	case *ahptypes.TerminalState:
		state.Terminal = cloneVia(st)
	case *ahptypes.ChangesetState:
		state.Changeset = cloneVia(st)
	case *ahptypes.ResourceWatchState:
		state.ResourceWatch = cloneVia(st)
	}
	return ahptypes.Snapshot{Resource: uri, State: state, FromSeq: fromSeq}, true
}

// trimTerminal drops only the oldest retained output beyond the cap.
func trimTerminal(st *ahptypes.TerminalState) {
	remaining := maxRetainedTerminalChars
	trimmed := false
	var newestFirst []ahptypes.TerminalContentPart
	for i := len(st.Content) - 1; i >= 0; i-- {
		part := st.Content[i]
		out := terminalOutput(part)
		runes := []rune(out)
		if len(runes) <= remaining {
			newestFirst = append(newestFirst, part)
			remaining -= len(runes)
			continue
		}
		if remaining > 0 {
			newestFirst = append(newestFirst, withTerminalOutput(part, string(runes[len(runes)-remaining:])))
		}
		trimmed = true
		break
	}
	if !trimmed {
		return
	}
	content := make([]ahptypes.TerminalContentPart, 0, len(newestFirst))
	for i := len(newestFirst) - 1; i >= 0; i-- {
		content = append(content, newestFirst[i])
	}
	st.Content = content
}

func terminalOutput(p ahptypes.TerminalContentPart) string {
	switch v := p.Value.(type) {
	case *ahptypes.TerminalUnclassifiedPart:
		return v.Value
	case *ahptypes.TerminalCommandPart:
		return v.Output
	}
	return ""
}

func withTerminalOutput(p ahptypes.TerminalContentPart, out string) ahptypes.TerminalContentPart {
	switch v := p.Value.(type) {
	case *ahptypes.TerminalUnclassifiedPart:
		c := *v
		c.Value = out
		return ahptypes.TerminalContentPart{Value: &c}
	case *ahptypes.TerminalCommandPart:
		c := *v
		c.Output = out
		return ahptypes.TerminalContentPart{Value: &c}
	}
	return p
}
