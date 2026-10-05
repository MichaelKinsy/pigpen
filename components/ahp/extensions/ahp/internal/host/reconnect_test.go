package host_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

const reconnectClientID = "reconnecting-client"

func bumpActiveSessions(h *host.Host, n int64) {
	h.DispatchServerAction(wire.RootChannel, rootActiveSessions(n))
}

type replayResult struct {
	Type    string                    `json:"type"`
	Actions []ahptypes.ActionEnvelope `json:"actions"`
	Missing []string                  `json:"missing"`
	Snaps   []ahptypes.Snapshot       `json:"snapshots"`
}

func reconnect(t *testing.T, c *testkit.Client, clientID string, lastSeen int64, subs []string) replayResult {
	t.Helper()
	var r replayResult
	c.Decode(c.Must("reconnect", rootParams(map[string]any{"clientId": clientID, "lastSeenServerSeq": lastSeen, "subscriptions": subs})), &r)
	return r
}

// Twins of upstream test/reconnect.test.ts, describe "reconnect".
func TestReconnect(t *testing.T) {
	// A tiny buffer makes the eviction path cheap to exercise.
	h := testkit.NewHost(host.Options{ReplayBufferCapacity: 4})

	twin.Run(t, "reconnect", "keeps non-VS Code and explicitly misrouted reconnects strict", func(t *testing.T) {
		c := testkit.Connect(t, h)
		for _, tc := range []struct {
			context string
			params  map[string]any
		}{
			{"non-VS Code reconnect without a channel", map[string]any{"clientId": reconnectClientID + "-missing-channel", "lastSeenServerSeq": 0, "subscriptions": []string{wire.RootChannel}}},
			{"VS Code reconnect with the wrong channel", map[string]any{
				"channel": wire.SessionURI("wrong-channel"), "clientId": reconnectClientID + "-wrong-channel", "lastSeenServerSeq": 0,
				"subscriptions": []string{wire.RootChannel}, "_meta": map[string]any{"vscode.telemetryLevel": "off"},
			}},
		} {
			e := c.ExpectError("reconnect", tc.params, wire.CodeInvalidParams)
			if !strings.HasSuffix(e.Message, "reconnect requires channel ahp-root://") {
				t.Errorf("%s: message %q", tc.context, e.Message)
			}
		}
	})

	twin.Run(t, "reconnect", "replays only the actions the client missed", func(t *testing.T) {
		first := testkit.Connect(t, h)
		init := first.Initialize(reconnectClientID, map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		bumpActiveSessions(h, 1)
		bumpActiveSessions(h, 2)
		first.Close()

		r := reconnect(t, testkit.Connect(t, h), reconnectClientID, init.ServerSeq, []string{wire.RootChannel})
		if r.Type != "replay" || len(r.Actions) != 2 {
			t.Fatalf("result = %+v", r)
		}
		if r.Actions[0].ServerSeq != init.ServerSeq+1 || r.Actions[1].ServerSeq != init.ServerSeq+2 {
			t.Fatalf("serverSeqs = %d, %d (from %d)", r.Actions[0].ServerSeq, r.Actions[1].ServerSeq, init.ServerSeq)
		}
		if len(r.Missing) != 0 {
			t.Fatalf("missing = %v", r.Missing)
		}
	})

	twin.Run(t, "reconnect", "falls back to snapshots when the gap predates the replay buffer", func(t *testing.T) {
		c := testkit.Connect(t, h)
		c.Initialize(reconnectClientID+"-gap", map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		stale := h.ServerSeq()
		// Overflow the 4-entry buffer so stale+1 is evicted.
		for i := int64(0); i < 6; i++ {
			bumpActiveSessions(h, i)
		}
		c.Close()
		r := reconnect(t, testkit.Connect(t, h), reconnectClientID+"-gap", stale, []string{wire.RootChannel})
		if r.Type != "snapshot" || len(r.Snaps) != 1 || r.Snaps[0].Resource != wire.RootChannel || r.Snaps[0].FromSeq != h.ServerSeq() {
			t.Fatalf("result = %+v (serverSeq %d)", r, h.ServerSeq())
		}
	})

	twin.Run(t, "reconnect", "reports subscriptions it cannot resume as missing", func(t *testing.T) {
		c := testkit.Connect(t, h)
		init := c.Initialize(reconnectClientID+"-missing", map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		disposed := wire.SessionURI("already-gone")
		c.Close()
		r := reconnect(t, testkit.Connect(t, h), reconnectClientID+"-missing", init.ServerSeq, []string{wire.RootChannel, disposed})
		if r.Type != "replay" || !reflect.DeepEqual(r.Missing, []string{disposed}) {
			t.Fatalf("result = %+v", r)
		}
	})

	twin.Run(t, "reconnect", "returns an empty replay for a client that is already current", func(t *testing.T) {
		c := testkit.Connect(t, h)
		c.Initialize(reconnectClientID+"-current", map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		c.Close()
		r := reconnect(t, testkit.Connect(t, h), reconnectClientID+"-current", h.ServerSeq(), []string{wire.RootChannel})
		if r.Type != "replay" || r.Actions == nil || len(r.Actions) != 0 {
			t.Fatalf("result = %+v (actions must be an empty array)", r)
		}
	})

	twin.Run(t, "reconnect", "replaces a half-open connection that reuses a clientId", func(t *testing.T) {
		clientID := reconnectClientID + "-duplicate"
		before := h.SubscriberCount(wire.RootChannel)
		first := testkit.Connect(t, h)
		first.Initialize(clientID, nil)
		first.Subscribe(wire.RootChannel)
		if got := h.SubscriberCount(wire.RootChannel); got != before+1 {
			t.Fatalf("subscribers = %d, want %d", got, before+1)
		}
		replacement := testkit.Connect(t, h)
		replacement.Initialize(clientID, nil)
		replacement.Subscribe(wire.RootChannel)
		if got := h.SubscriberCount(wire.RootChannel); got != before+1 {
			t.Fatalf("subscribers = %d, want %d", got, before+1)
		}
		// The host closes the replaced transport on its own goroutine (a Close that blocks must not stall the handshake).
		testkit.Eventually(t, "the half-open connection to be closed", first.ServerClosed)
	})

	twin.Run(t, "reconnect", "retains clientInfo when a replacement initialize omits it", func(t *testing.T) {
		clientID := reconnectClientID + "-reinitialize"
		id := "vscode-reinitialize"
		mustCreate(t, h, wire.SessionURI(id), channels.InitialSessionState("pi", "Reinitialize", "/tmp"), wire.KindSession)
		first := testkit.Connect(t, h)
		first.Must("initialize", rootParams(map[string]any{"clientId": clientID, "protocolVersions": testkit.SupportedVersions(), "clientInfo": map[string]any{"name": "vscode-editor-window"}}))
		first.Close()
		replacement := testkit.Connect(t, h)
		replacement.Initialize(clientID, nil)
		clientURI := "pi:/" + id
		if got := replacement.Subscribe(clientURI); got.Snapshot == nil || got.Snapshot.Resource != clientURI {
			t.Fatalf("subscribe(%s) = %+v", clientURI, got)
		}
	})

	twin.Run(t, "reconnect", "infers the provider URI dialect without clientInfo", func(t *testing.T) {
		clientID := reconnectClientID + "-provider-alias"
		id := "provider-alias-reconnect"
		canonical, alias := wire.SessionURI(id), "pi:/"+id
		mustCreate(t, h, canonical, channels.InitialSessionState("pi", "Reconnect", "/tmp"), wire.KindSession)
		first := testkit.Connect(t, h)
		first.Initialize(clientID, nil)
		if got := first.Subscribe(alias); got.Snapshot == nil || got.Snapshot.Resource != alias {
			t.Fatalf("subscribe = %+v", got)
		}
		stale := h.ServerSeq()
		first.Close()
		for i := int64(0); i < 6; i++ {
			bumpActiveSessions(h, i)
		}
		r := reconnect(t, testkit.Connect(t, h), clientID, stale, []string{alias})
		if r.Type != "snapshot" || len(r.Snaps) != 1 || r.Snaps[0].Resource != alias {
			t.Fatalf("result = %+v", r)
		}
		if !h.Store().Has(canonical) || h.Store().Has(alias) {
			t.Fatal("core state stays canonical")
		}
	})

	twin.Run(t, "reconnect", "retains VS Code's URI dialect across connections", func(t *testing.T) {
		clientID := reconnectClientID + "-vscode"
		id := "vscode-reconnect"
		clientURI := "pi:/" + id
		mustCreate(t, h, wire.SessionURI(id), channels.InitialSessionState("pi", "Reconnect", "/tmp"), wire.KindSession)
		first := testkit.Connect(t, h)
		first.Must("initialize", rootParams(map[string]any{"clientId": clientID, "protocolVersions": testkit.SupportedVersions(), "clientInfo": map[string]any{"name": "vscode-editor-window"}}))
		if got := first.Subscribe(clientURI); got.Snapshot == nil || got.Snapshot.Resource != clientURI {
			t.Fatalf("subscribe = %+v", got)
		}
		stale := h.ServerSeq()
		first.Close()
		for i := int64(0); i < 6; i++ {
			bumpActiveSessions(h, i)
		}
		r := reconnect(t, testkit.Connect(t, h), clientID, stale, []string{clientURI})
		if r.Type != "snapshot" || len(r.Snaps) != 1 || r.Snaps[0].Resource != clientURI {
			t.Fatalf("result = %+v", r)
		}
	})
}

var _ = json.Marshal
