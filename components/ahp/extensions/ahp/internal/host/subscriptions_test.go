package host_test

import (
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

func initialized(t *testing.T, h *host.Host, subs ...string) *testkit.Client {
	t.Helper()
	c := testkit.Connect(t, h)
	extra := map[string]any{}
	if len(subs) > 0 {
		extra["initialSubscriptions"] = subs
	}
	c.Initialize(nextClientID(), extra)
	return c
}

// Twins of upstream test/subscriptions.test.ts, describe "subscriptions".
func TestSubscriptions(t *testing.T) {
	h := testkit.NewHost(host.Options{})

	twin.Run(t, "subscriptions", "returns the current state as a snapshot", func(t *testing.T) {
		c := initialized(t, h)
		res := c.Subscribe(wire.RootChannel)
		if res.Snapshot == nil || res.Snapshot.Resource != wire.RootChannel {
			t.Fatalf("snapshot = %+v", res.Snapshot)
		}
		if root := res.Snapshot.State.Root; root == nil || len(root.Agents) != 1 {
			t.Fatalf("root state = %+v", root)
		}
	})

	twin.Run(t, "subscriptions", "rejects a channel scheme the host does not serve", func(t *testing.T) {
		c := initialized(t, h)
		c.ExpectError("subscribe", map[string]any{"channel": "ahp-unknown:/x"}, -32602)
	})

	twin.Run(t, "subscriptions", "rejects a well-formed URI for a channel that does not exist", func(t *testing.T) {
		c := initialized(t, h)
		c.ExpectError("subscribe", map[string]any{"channel": "ahp-session:/nope"}, -32008)
	})
}

// Twins of upstream test/subscriptions.test.ts, describe "action dispatch".
func TestActionDispatch(t *testing.T) {
	h := testkit.NewHost(host.Options{})

	twin.Run(t, "subscriptions", "echoes a client-dispatchable action with its origin", func(t *testing.T) {
		c := initialized(t, h)
		c.Subscribe(wire.RootChannel)
		seq := c.Dispatch(wire.RootChannel, map[string]any{"type": "root/configChanged", "config": map[string]any{"theme": "dark"}})
		env := c.NextEnvelope(wire.RootChannel, 0)
		if env.Channel != wire.RootChannel || actionType(t, env) != "root/configChanged" || env.RejectionReason != nil {
			t.Fatalf("envelope = %+v", env)
		}
		// The origin lets the write-ahead client match the echo to its own dispatch.
		if env.Origin == nil || env.Origin.ClientSeq != seq || env.ServerSeq <= 0 {
			t.Fatalf("origin = %+v serverSeq = %d", env.Origin, env.ServerSeq)
		}
	})

	twin.Run(t, "subscriptions", "rejects an action that is not client-dispatchable", func(t *testing.T) {
		c := initialized(t, h)
		c.Subscribe(wire.RootChannel)
		// root/agentsChanged is server-only: the host owns the agent list.
		c.Dispatch(wire.RootChannel, map[string]any{"type": "root/agentsChanged", "agents": []any{}})
		env := c.NextEnvelope(wire.RootChannel, 0)
		if env.RejectionReason == nil || !contains(*env.RejectionReason, "not client-dispatchable") {
			t.Fatalf("rejectionReason = %v", env.RejectionReason)
		}
		if root := h.Store().Root(wire.RootChannel); root == nil || len(root.Agents) != 1 {
			t.Fatalf("a rejected action must leave state untouched, got %+v", root)
		}
	})

	twin.Run(t, "subscriptions", "silently ignores an action for a channel that does not exist", func(t *testing.T) {
		c := initialized(t, h)
		c.Subscribe(wire.RootChannel)
		c.Dispatch("ahp-session:/ghost", map[string]any{"type": "session/titleChanged", "title": "x"})
		// No echo for the ghost channel, so the next envelope on root is the one dispatched afterwards.
		c.Dispatch(wire.RootChannel, map[string]any{"type": "root/configChanged", "config": map[string]any{"probe": 1}})
		if env := c.NextEnvelope("", 0); env.Channel != wire.RootChannel {
			t.Fatalf("first envelope was on %s", env.Channel)
		}
	})

	twin.Run(t, "subscriptions", "broadcasts to every subscriber and allocates one seq per action", func(t *testing.T) {
		alice, bob := initialized(t, h), initialized(t, h)
		alice.Subscribe(wire.RootChannel)
		bob.Subscribe(wire.RootChannel)
		alice.Dispatch(wire.RootChannel, map[string]any{"type": "root/configChanged", "config": map[string]any{"shared": true}})
		a, b := alice.NextEnvelope(wire.RootChannel, 0), bob.NextEnvelope(wire.RootChannel, 0)
		if a.ServerSeq != b.ServerSeq || testkit.JSON(t, a.Action) != testkit.JSON(t, b.Action) {
			t.Fatalf("alice %d %s, bob %d %s", a.ServerSeq, testkit.JSON(t, a.Action), b.ServerSeq, testkit.JSON(t, b.Action))
		}
		// Both clients reduce the same envelope, so they converge by construction.
		if a.Origin == nil || b.Origin == nil || a.Origin.ClientId != b.Origin.ClientId {
			t.Fatalf("origins differ: %+v %+v", a.Origin, b.Origin)
		}
	})

	twin.Run(t, "subscriptions", "treats root/configChanged as a no-op until the host declares a config schema", func(t *testing.T) {
		// AHP only merges config values into a schema the host has already published. The action is
		// still sequenced and echoed; it just does not change state.
		c := initialized(t, h)
		c.Subscribe(wire.RootChannel)
		c.Dispatch(wire.RootChannel, map[string]any{"type": "root/configChanged", "config": map[string]any{"theme": "dark"}})
		env := c.NextEnvelope(wire.RootChannel, 0)
		if env.RejectionReason != nil {
			t.Fatalf("unexpected rejection %q", *env.RejectionReason)
		}
		if root := h.Store().Root(wire.RootChannel); root == nil || root.Config != nil {
			t.Fatalf("config = %+v", root)
		}
	})

	twin.Run(t, "subscriptions", "stops delivering after unsubscribe", func(t *testing.T) {
		c := initialized(t, h)
		c.Subscribe(wire.RootChannel)
		c.Unsubscribe(wire.RootChannel)
		c.Ping()
		before := h.ServerSeq()
		h.DispatchServerAction(wire.RootChannel, rootActiveSessions(7))
		// The action was still sequenced and applied — the client just stops seeing it.
		if h.ServerSeq() != before+1 {
			t.Fatalf("serverSeq = %d, want %d", h.ServerSeq(), before+1)
		}
		if !c.Silent(func(n testkit.Notification) bool { _, ok := n.Envelope(); return ok }, 150*time.Millisecond) {
			t.Fatal("an unsubscribed client received an action")
		}
	})
}

func rootActiveSessions(n int64) ahptypes.StateAction {
	return ahptypes.StateAction{Value: &ahptypes.RootActiveSessionsChangedAction{Type: ahptypes.ActionTypeRootActiveSessionsChanged, ActiveSessions: n}}
}

func actionType(t *testing.T, env ahptypes.ActionEnvelope) string {
	t.Helper()
	m := testkit.Normalize(t, env.Action).(map[string]any)
	s, _ := m["type"].(string)
	return s
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
