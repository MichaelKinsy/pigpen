// Package channels builds the protocol-shaped state of the session and chat channels. It
// ports pi-ahp's src/channels (root.ts, session.ts, chat.ts) and knows nothing about Pi or PiG.
package channels

import (
	"encoding/json"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// InitialSessionState is a fresh session in lifecycle "creating".
func InitialSessionState(provider, title, workingDirectory string) *ahptypes.SessionState {
	return &ahptypes.SessionState{
		Provider:           provider,
		Title:              title,
		Status:             ahptypes.SessionStatusIdle,
		Lifecycle:          ahptypes.SessionLifecycleCreating,
		WorkingDirectories: []ahptypes.URI{wire.PathToFileURI(workingDirectory)},
		ActiveClients:      []ahptypes.SessionActiveClient{},
		Chats:              []ahptypes.ChatSummary{},
	}
}

// statusActivityMask: the low five bits encode activity; higher bits carry independent metadata
// flags.
const statusActivityMask = ahptypes.SessionStatus(1<<5 - 1)

// SessionChatAggregate is what a session derives from its chat catalogue.
type SessionChatAggregate struct {
	Status     ahptypes.SessionStatus
	Activity   *string
	ModifiedAt string // "" when the session has no chats
}

// instant parses an ISO 8601 timestamp; an unparsable one sorts before every valid one.
func instant(summary ahptypes.ChatSummary) (t time.Time, ok bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z0700", "2006-01-02T15:04:05Z0700"} {
		if parsed, err := time.Parse(layout, summary.ModifiedAt); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func newer(a, b ahptypes.ChatSummary) bool { // a is strictly more recent than b
	ta, oka := instant(a)
	tb, okb := instant(b)
	switch {
	case !oka:
		return false
	case !okb:
		return true
	}
	return ta.After(tb)
}

func mostRecent(chats []ahptypes.ChatSummary, matches func(ahptypes.ChatSummary) bool) (ahptypes.ChatSummary, bool) {
	var latest ahptypes.ChatSummary
	found := false
	for _, chat := range chats {
		if matches(chat) && (!found || newer(chat, latest)) {
			latest, found = chat, true
		}
	}
	return latest, found
}

// AggregateSessionChats projects the session-level summary fields AHP derives from its chat
// catalogue. The current host exposes one chat, so this is a pass-through today; keeping the
// complete 0.9 rule makes the projection deterministic. It never mutates the state.
func AggregateSessionChats(state *ahptypes.SessionState) SessionChatAggregate {
	if len(state.Chats) == 0 {
		return SessionChatAggregate{Status: state.Status, Activity: state.Activity}
	}
	latest, ok := mostRecent(state.Chats, func(ahptypes.ChatSummary) bool { return true })
	if !ok {
		return SessionChatAggregate{Status: state.Status}
	}
	driver := latest
	if state.DefaultChat != nil {
		for _, chat := range state.Chats {
			if chat.Resource == *state.DefaultChat {
				driver = chat
				break
			}
		}
	}
	if c, ok := mostRecent(state.Chats, func(c ahptypes.ChatSummary) bool {
		return c.Status&ahptypes.SessionStatusInputNeeded == ahptypes.SessionStatusInputNeeded
	}); ok {
		driver = c
	} else if c, ok := mostRecent(state.Chats, func(c ahptypes.ChatSummary) bool {
		return c.Status&ahptypes.SessionStatusError == ahptypes.SessionStatusError
	}); ok {
		driver = c
	}
	metadata := state.Status &^ statusActivityMask
	return SessionChatAggregate{
		Status:     metadata | (driver.Status & statusActivityMask),
		Activity:   driver.Activity,
		ModifiedAt: latest.ModifiedAt,
	}
}

// SessionSummaryOf is the root-catalogue projection of a session.
func SessionSummaryOf(resource, createdAt string, state *ahptypes.SessionState, meta map[string]json.RawMessage) ahptypes.SessionSummary {
	aggregate := AggregateSessionChats(state)
	modifiedAt := aggregate.ModifiedAt
	if modifiedAt == "" {
		modifiedAt = createdAt
	}
	summary := ahptypes.SessionSummary{
		Resource: resource, Provider: state.Provider, Title: state.Title, Status: aggregate.Status,
		// Root summary activity is deliberately omitted for AHP 0.9: its partial update shape
		// cannot distinguish "clear" from "unchanged" on the JSON wire.
		CreatedAt: createdAt, ModifiedAt: modifiedAt,
	}
	if state.WorkingDirectories != nil {
		summary.WorkingDirectories = state.WorkingDirectories
	}
	if meta != nil {
		summary.Meta = meta
	}
	return summary
}
