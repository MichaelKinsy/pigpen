package ws_test

import "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

func activeSessions(n int64) ahptypes.StateAction {
	return ahptypes.StateAction{Value: &ahptypes.RootActiveSessionsChangedAction{Type: ahptypes.ActionTypeRootActiveSessionsChanged, ActiveSessions: n}}
}
