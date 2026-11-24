package readashboard_test

import (
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// PushState sends a state_update notification with the members of state, as the host does when its state changes.
func (h *Host) PushState(state map[string]any) {
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": state}}})
}

// startWithServers starts the fake host and then does what the real host does once the rea member's registration applied:
// it pushes the server list and fires mcp_servers_change.
func startWithServers(t *testing.T, ext *sdk.Extension, opts HostOptions, servers any) *Host {
	t.Helper()
	h := StartHost(t, ext, opts)
	h.PushState(map[string]any{"mcpServers": servers})
	h.Fire("mcp_servers_change", map[string]any{"servers": servers})
	return h
}
