package host_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

var clientCounter int

func nextClientID() string {
	clientCounter++
	return "test-client-" + string(rune('a'+clientCounter%26)) + itoa(clientCounter)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func rootParams(extra map[string]any) map[string]any {
	p := map[string]any{"channel": wire.RootChannel}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// Twins of upstream test/handshake.test.ts, describe "handshake".
func TestHandshake(t *testing.T) {
	h := testkit.NewHost(host.Options{})

	twin.Run(t, "handshake", "negotiates the client's most-preferred supported version", func(t *testing.T) {
		c := testkit.Connect(t, h)
		res := c.Initialize(nextClientID(), nil)
		if res.ProtocolVersion != "0.9.0" {
			t.Fatalf("protocolVersion = %q", res.ProtocolVersion)
		}
		if res.ServerInfo == nil || res.ServerInfo.Name != "pi-ahp" {
			t.Fatalf("serverInfo = %+v", res.ServerInfo)
		}
		if res.Snapshots == nil {
			t.Fatal("snapshots must be an array, not null")
		}
	})

	twin.Run(t, "handshake", "does not advertise deferred host capabilities", func(t *testing.T) {
		c := testkit.Connect(t, h)
		res := c.Initialize(nextClientID(), nil)
		if res.TerminalCommandPrefix != nil || res.Telemetry != nil || res.Automations != nil {
			t.Fatalf("deferred capabilities advertised: %+v", res)
		}
	})

	twin.Run(t, "handshake", "ignores versions it does not know before the current one", func(t *testing.T) {
		c := testkit.Connect(t, h)
		res := c.Initialize(nextClientID(), map[string]any{"protocolVersions": []string{"99.0.0", "0.9.0"}})
		if res.ProtocolVersion != "0.9.0" {
			t.Fatalf("protocolVersion = %q", res.ProtocolVersion)
		}
	})

	twin.Run(t, "handshake", "rejects an older wire model with UnsupportedProtocolVersion", func(t *testing.T) {
		c := testkit.Connect(t, h)
		rpcErr := c.ExpectError("initialize", rootParams(map[string]any{"clientId": nextClientID(), "protocolVersions": []string{"0.8.0"}}), wire.CodeUnsupportedProtocolVersion)
		var data struct {
			SupportedVersions []string `json:"supportedVersions"`
		}
		if err := json.Unmarshal(rpcErr.Data, &data); err != nil || !reflect.DeepEqual(data.SupportedVersions, []string{"0.9.0"}) {
			t.Fatalf("error data = %s (%v)", rpcErr.Data, err)
		}
	})

	twin.Run(t, "handshake", "returns a snapshot for each initialSubscription in the same round-trip", func(t *testing.T) {
		c := testkit.Connect(t, h)
		res := c.Initialize(nextClientID(), map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		if len(res.Snapshots) != 1 {
			t.Fatalf("snapshots = %d", len(res.Snapshots))
		}
		snap := res.Snapshots[0]
		if snap.Resource != wire.RootChannel || snap.FromSeq != res.ServerSeq {
			t.Fatalf("snapshot = %s fromSeq %d (serverSeq %d)", snap.Resource, snap.FromSeq, res.ServerSeq)
		}
		if snap.State.Root == nil || !reflect.DeepEqual(testkit.Normalize(t, snap.State.Root.Agents), testkit.Normalize(t, []ahptypes.AgentInfo{testkit.TestAgent()})) {
			t.Fatalf("root agents = %+v", snap.State.Root)
		}
	})

	twin.Run(t, "handshake", "skips unknown initialSubscriptions instead of failing the handshake", func(t *testing.T) {
		c := testkit.Connect(t, h)
		res := c.Initialize(nextClientID(), map[string]any{"initialSubscriptions": []string{wire.RootChannel, "ahp-session:/does-not-exist"}})
		if len(res.Snapshots) != 1 || res.Snapshots[0].Resource != wire.RootChannel {
			t.Fatalf("snapshots = %+v", res.Snapshots)
		}
	})

	twin.Run(t, "handshake", "rejects an initialize without a clientId", func(t *testing.T) {
		c := testkit.Connect(t, h)
		c.ExpectError("initialize", rootParams(map[string]any{"protocolVersions": testkit.SupportedVersions()}), wire.CodeInvalidParams)
	})

	twin.Run(t, "handshake", "rejects malformed initialize descriptors", func(t *testing.T) {
		c := testkit.Connect(t, h)
		for _, invalid := range []map[string]any{
			{"protocolVersions": []int{42}},
			{"clientInfo": map[string]any{"name": 42}},
			{"locale": 42},
			{"capabilities": []any{}},
		} {
			params := rootParams(map[string]any{"clientId": nextClientID(), "protocolVersions": testkit.SupportedVersions()})
			for k, v := range invalid {
				params[k] = v
			}
			c.ExpectError("initialize", params, wire.CodeInvalidParams)
		}
		if res := c.Initialize(nextClientID(), nil); res.ProtocolVersion != "0.9.0" {
			t.Fatalf("the connection must still accept a valid handshake, got %q", res.ProtocolVersion)
		}
	})

	twin.Run(t, "handshake", "answers ping before initialize", func(t *testing.T) {
		// The spec requires ping to work regardless of handshake or subscription state.
		testkit.Connect(t, h).Ping()
	})

	twin.Run(t, "handshake", "ignores notifications before initialize or reconnect", func(t *testing.T) {
		c := testkit.Connect(t, h)
		before := h.ServerSeq()
		c.Dispatch(wire.RootChannel, map[string]any{"type": "root/configChanged", "config": map[string]any{"ignored": true}})
		c.Ping() // in-order delivery makes the ping response a barrier after the notification
		if h.ServerSeq() != before {
			t.Fatalf("serverSeq moved from %d to %d", before, h.ServerSeq())
		}
	})

	twin.Run(t, "handshake", "requires initialize or reconnect before other requests", func(t *testing.T) {
		c := testkit.Connect(t, h)
		c.ExpectError("listSessions", rootParams(nil), wire.CodeInvalidRequest)
	})

	twin.Run(t, "handshake", "rejects a second handshake without changing client compatibility mode", func(t *testing.T) {
		c := testkit.Connect(t, h)
		c.Initialize(nextClientID(), nil)
		session := wire.SessionURI("rejected-second-handshake")
		mustCreate(t, h, session, channels.InitialSessionState("pi", "Still canonical", "/tmp"), wire.KindSession)
		c.ExpectError("initialize", rootParams(map[string]any{
			"clientId": nextClientID(), "protocolVersions": testkit.SupportedVersions(),
			"clientInfo": map[string]any{"name": "vscode-editor-window"},
		}), wire.CodeInvalidRequest)
		if got := c.Subscribe(session); got.Snapshot == nil || got.Snapshot.Resource != session {
			t.Fatalf("subscribe = %+v", got)
		}
	})

	twin.Run(t, "handshake", "rejects malformed reconnect state without binding the connection", func(t *testing.T) {
		c := testkit.Connect(t, h)
		for _, invalid := range []map[string]any{
			{"clientId": "", "lastSeenServerSeq": 0, "subscriptions": []string{}},
			{"lastSeenServerSeq": -1, "subscriptions": []string{}},
			{"lastSeenServerSeq": 0, "subscriptions": 42},
		} {
			params := rootParams(map[string]any{"clientId": nextClientID()})
			for k, v := range invalid {
				params[k] = v
			}
			c.ExpectError("reconnect", params, wire.CodeInvalidParams)
		}
		if res := c.Initialize(nextClientID(), nil); res.ProtocolVersion != "0.9.0" {
			t.Fatalf("protocolVersion = %q", res.ProtocolVersion)
		}
	})

	twin.Run(t, "handshake", "rejects malformed subscription lists without binding or poisoning the connection", func(t *testing.T) {
		c := testkit.Connect(t, h)
		id := "handshake-retry"
		canonical := wire.SessionURI(id)
		mustCreate(t, h, canonical, channels.InitialSessionState("pi", "Retry", "/tmp"), wire.KindSession)
		// The valid provider alias must not establish a dialect when another element makes the whole handshake invalid.
		c.ExpectError("initialize", rootParams(map[string]any{
			"clientId": nextClientID(), "protocolVersions": testkit.SupportedVersions(),
			"initialSubscriptions": []any{"pi:/" + id, 42},
		}), wire.CodeInvalidParams)
		res := c.Initialize(nextClientID(), map[string]any{"initialSubscriptions": []string{canonical}})
		if res.ProtocolVersion != "0.9.0" {
			t.Fatalf("protocolVersion = %q", res.ProtocolVersion)
		}
		if len(res.Snapshots) != 1 || res.Snapshots[0].Resource != canonical {
			t.Fatalf("snapshots = %+v", res.Snapshots)
		}
	})
}

func mustCreate(t *testing.T, h *host.Host, uri string, state any, kind wire.ChannelKind) {
	t.Helper()
	if err := h.Store().Create(uri, state, kind); err != nil {
		t.Fatalf("store.Create(%s): %v", uri, err)
	}
}
