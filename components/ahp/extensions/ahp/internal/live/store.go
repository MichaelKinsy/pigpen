package live

import (
	"encoding/json"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
)

// Store is a pi.SessionStore over the running session: entries come from the host's session
// mirror, so it is current without reading the file Pi is writing, and a rename goes through the
// SDK rather than into the file.
type Store struct{ s *Session }

// NewStore returns the live session's store.
func (s *Session) NewStore() *Store { return &Store{s} }

var _ pi.SessionStore = (*Store)(nil)

func (st *Store) snapshot() (*pisession.Manager, sdk.Context, error) {
	ctx, err := st.s.Context()
	if err != nil {
		return nil, ctx, err
	}
	raws, err := ctx.GetEntries()
	if err != nil {
		return nil, ctx, err
	}
	entries := make([]pisession.Entry, 0, len(raws))
	for _, raw := range raws {
		var e pisession.Entry
		if json.Unmarshal(raw, &e) == nil && e.Type() != "session" {
			entries = append(entries, e)
		}
	}
	file := ""
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		file = *f
	}
	leaf := ""
	if l, err := ctx.GetLeafID(); err == nil && l != nil {
		leaf = *l
	}
	m, err := pisession.FromEntries(st.s.WorkingDirectory(), st.s.ID(), file, entries, leaf)
	if err != nil {
		// A leaf the mirror does not know yet: fall back to the last entry.
		m, err = pisession.FromEntries(st.s.WorkingDirectory(), st.s.ID(), file, entries, "")
	}
	return m, ctx, err
}

// File implements pi.SessionStore.
func (st *Store) File() string {
	ctx, err := st.s.Context()
	if err != nil {
		return ""
	}
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		return *f
	}
	return ""
}

// LeafEntry implements pi.SessionStore.
func (st *Store) LeafEntry() pisession.Entry {
	m, _, err := st.snapshot()
	if err != nil {
		return nil
	}
	return m.LeafEntry()
}

// SessionName implements pi.SessionStore.
func (st *Store) SessionName() string {
	ctx, err := st.s.Context()
	if err != nil {
		return ""
	}
	if name, err := ctx.GetSessionName(); err == nil && name != nil {
		return *name
	}
	return ""
}

// AppendSessionInfo implements pi.SessionStore. It renames through the SDK, so the entry is
// appended by the session's own writer.
func (st *Store) AppendSessionInfo(name string) (string, error) {
	ctx, err := st.s.Context()
	if err != nil {
		return "", err
	}
	return "", ctx.SetSessionName(name)
}

// Branch implements pi.SessionStore.
func (st *Store) Branch(fromID string) []pisession.Entry {
	m, _, err := st.snapshot()
	if err != nil {
		return nil
	}
	return m.Branch(fromID)
}

// ContextEntries implements pi.SessionStore.
func (st *Store) ContextEntries() []pisession.Entry {
	m, _, err := st.snapshot()
	if err != nil {
		return nil
	}
	return m.ContextEntries()
}
