package host_test

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// The replay buffer serves a client only when it still holds every action after lastSeenServerSeq:
// one action past the edge must fall back to fresh snapshots, never a replay with a hole in it.
func TestReplayWindowEdge(t *testing.T) {
	h := testkit.NewHost(host.Options{ReplayBufferCapacity: 4})
	c := testkit.Connect(t, h)
	c.Initialize("edge-client", map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
	c.Close()
	seen := h.ServerSeq()
	for i := 0; i < 4; i++ {
		bumpActiveSessions(h, int64(i))
	}
	inside := reconnect(t, testkit.Connect(t, h), "edge-client", seen, []string{wire.RootChannel})
	if inside.Type != "replay" || len(inside.Actions) != 4 {
		t.Fatalf("the buffer still holds every action after lastSeen: type=%s actions=%d", inside.Type, len(inside.Actions))
	}
	bumpActiveSessions(h, 9) // evicts the oldest action the client has not seen
	outside := reconnect(t, testkit.Connect(t, h), "edge-client", seen, []string{wire.RootChannel})
	if outside.Type != "snapshot" || len(outside.Snaps) != 1 {
		t.Fatalf("one action beyond the buffer must fall back to snapshots: type=%s snapshots=%d", outside.Type, len(outside.Snaps))
	}
}
