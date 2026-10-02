package channels

import (
	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Catalogue notifications are ephemeral and never replayed on reconnect; after reconnecting a
// client re-fetches the catalogue with listSessions.

// NotifySessionAdded announces a session on the root channel.
func NotifySessionAdded(h *host.Host, summary ahptypes.SessionSummary) {
	h.Notify(wire.RootChannel, "root/sessionAdded", map[string]any{"summary": summary})
}

// NotifySessionRemoved announces a removed session.
func NotifySessionRemoved(h *host.Host, session string) {
	h.Notify(wire.RootChannel, "root/sessionRemoved", map[string]any{"session": session})
}

// NotifySessionSummaryChanged announces changed catalogue fields.
func NotifySessionSummaryChanged(h *host.Host, session string, changes ahptypes.PartialSessionSummary) {
	h.Notify(wire.RootChannel, "root/sessionSummaryChanged", map[string]any{"session": session, "changes": changes})
}

// TerminalInfoOf is the root catalogue projection of a terminal; process handles never enter
// protocol state.
func TerminalInfoOf(resource string, state *ahptypes.TerminalState) ahptypes.TerminalInfo {
	return ahptypes.TerminalInfo{Resource: resource, Title: state.Title, Claim: state.Claim, Lifecycle: state.Lifecycle}
}
