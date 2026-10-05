package pi

import (
	"fmt"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

const defaultPageSize = 20

// OlderTurnsPage is a page of history older than what the client already has.
type OlderTurnsPage struct {
	Turns      []ahptypes.Turn
	NextCursor string
}

func fullBranchTurns(store SessionStore, prefix string) []ahptypes.Turn {
	return RebuildHistory(store.Branch(""), RebuildOptions{TurnIDPrefix: prefix}).Turns
}

func indexOfTurn(turns []ahptypes.Turn, id string) int {
	for i, t := range turns {
		if t.Id == id {
			return i
		}
	}
	return -1
}

// InitialTurnsCursor is the cursor a client needs to fetch what precedes the visible window, ""
// when the window already starts at the beginning. The cursor is the id of the oldest visible turn.
func InitialTurnsCursor(store SessionStore, prefix string, visible []ahptypes.Turn) string {
	if len(visible) == 0 {
		return ""
	}
	if indexOfTurn(fullBranchTurns(store, prefix), visible[0].Id) > 0 {
		return visible[0].Id
	}
	return ""
}

// LoadOlderTurns returns the page of turns ending just before cursor.
func LoadOlderTurns(store SessionStore, prefix, cursor string, limit int) OlderTurnsPage {
	if cursor == "" {
		return OlderTurnsPage{Turns: []ahptypes.Turn{}}
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	full := fullBranchTurns(store, prefix)
	boundary := indexOfTurn(full, cursor)
	if boundary <= 0 {
		return OlderTurnsPage{Turns: []ahptypes.Turn{}}
	}
	start := boundary - limit
	if start < 0 {
		start = 0
	}
	page := OlderTurnsPage{Turns: append([]ahptypes.Turn{}, full[start:boundary]...)}
	// More remains only if this page did not reach the beginning.
	if start > 0 && len(page.Turns) > 0 {
		page.NextCursor = page.Turns[0].Id
	}
	return page
}

// DispatchOlderTurns serves fetchTurns: the page is dispatched to the chat before it returns.
func DispatchOlderTurns(h *host.Host, channel string, source HistorySource, cursor *string) error {
	state := h.Store().Chat(channel)
	if cursor != nil {
		var next string
		if state != nil && state.TurnsNextCursor != nil {
			next = *state.TurnsNextCursor
		}
		if state == nil || state.TurnsNextCursor == nil || *cursor != next {
			return wire.InvalidParams(fmt.Sprintf("Unrecognised fetchTurns cursor for %s", channel))
		}
	}
	c := ""
	if cursor != nil {
		c = *cursor
	}
	page := LoadOlderTurns(source.Store(), source.ID(), c, 0)
	// Dispatched even when empty: it is what clears a cursor that has no more history behind it, so
	// the client stops asking.
	action := &ahptypes.ChatTurnsLoadedAction{Type: ahptypes.ActionTypeChatTurnsLoaded, Turns: page.Turns}
	if page.NextCursor != "" {
		action.TurnsNextCursor = &page.NextCursor
	}
	h.DispatchServerAction(channel, ahptypes.StateAction{Value: action})
	return nil
}
