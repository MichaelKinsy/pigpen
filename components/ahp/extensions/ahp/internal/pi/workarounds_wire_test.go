package pi_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of the wire-level describes of upstream test/client-workarounds.test.ts: per-connection
// URI compatibility without leaking client dialects into core state. (The classification and
// rewriting unit twins live in internal/host and internal/wire.)

func derivedChatURI(providerSession string) string {
	return "ahp-chat://default/" + base64.RawURLEncoding.EncodeToString([]byte(providerSession))
}

func expectVSCodeDisposalRefusal(t *testing.T, c *testkit.Client, channel string) {
	t.Helper()
	e := c.ExpectError("disposeSession", obj{"channel": channel}, wire.CodeInvalidRequest)
	for _, want := range []string{"temporarily disabled for VS Code", "VS Code provisional-session lifecycle bug", "session was kept"} {
		if !strings.Contains(e.Message, want) {
			t.Fatalf("refusal %q lacks %q", e.Message, want)
		}
	}
}

func initialSnapshotResources(t *testing.T, f *hydratedFixture, clientID string, subscriptions []string, clientInfo obj) []string {
	t.Helper()
	c := testkit.Connect(t, f.host)
	defer c.Close()
	extra := obj{"initialSubscriptions": subscriptions}
	if clientInfo != nil {
		extra["clientInfo"] = clientInfo
	}
	result := c.Initialize(clientID, extra)
	var out []string
	for _, s := range result.Snapshots {
		out = append(out, s.Resource)
	}
	return out
}

func TestClientURIDialectsOverTheWire(t *testing.T) {
	f := startHydrated(t, hydratedOptions{})

	twin.Run(t, "client-workarounds", "answers VS Code at the URIs it computes for itself", func(t *testing.T) {
		client := f.connectAsVSCode()
		providerSession := "pi:/" + f.sessionID
		derived := derivedChatURI(providerSession)
		session := client.Subscribe(providerSession)
		if session.Snapshot.Resource != providerSession {
			t.Fatalf("snapshot resource = %s", session.Snapshot.Resource)
		}
		state := snapshotStateOf[ahptypes.SessionState](t, session)
		if state.DefaultChat == nil || *state.DefaultChat != derived {
			t.Fatalf("defaultChat = %v, want %s", state.DefaultChat, derived)
		}
		result := client.Subscribe(derived)
		chat := snapshotStateOf[ahptypes.ChatState](t, result)
		if result.Snapshot.Resource != derived {
			t.Fatalf("chat resource = %s", result.Snapshot.Resource)
		}
		if len(chat.Turns) == 0 {
			t.Fatal("the transcript must come back, not an empty chat")
		}
		testkit.AssertValid(t, "state", "ChatState", chat)
	})

	twin.Run(t, "client-workarounds", "applies the VS Code dialect to initialize-time subscriptions", func(t *testing.T) {
		providerSession := "pi:/" + f.sessionID
		subscriptions := []string{providerSession, derivedChatURI(providerSession)}
		got := initialSnapshotResources(t, f, "initial-subscriptions-VS Code", subscriptions, obj{"name": "vscode-editor-window"})
		sameJSON(t, got, subscriptions, "initial snapshots")
	})

	twin.Run(t, "client-workarounds", "applies the unnamed provider-alias client dialect to initialize-time subscriptions", func(t *testing.T) {
		providerSession := "pi:/" + f.sessionID
		subscriptions := []string{providerSession, wire.ChatURI(f.sessionID)}
		got := initialSnapshotResources(t, f, "initial-subscriptions-unnamed", subscriptions, nil)
		sameJSON(t, got, subscriptions, "initial snapshots")
	})
}

func TestVSCodeSessionDisposalWorkaround(t *testing.T) {
	twin.Run(t, "client-workarounds", "protects a durable session that this connection did not create", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		client := f.connectAsVSCode()
		resource := "pi:/" + f.sessionID
		expectVSCodeDisposalRefusal(t, client, resource)
		if got := f.deletedFiles(); len(got) != 0 {
			t.Fatalf("deleted = %v", got)
		}
		var listed struct{ Items []obj }
		client.Decode(client.Must("listSessions", obj{"channel": wire.RootChannel}), &listed)
		found := false
		for _, item := range listed.Items {
			found = found || item["resource"] == resource
		}
		if !found {
			t.Fatalf("the protected session vanished from the listing: %v", listed.Items)
		}
	})

	twin.Run(t, "client-workarounds", "still disposes an empty session created by this VS Code connection", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		client := f.connectAsVSCode()
		id := newID()
		resource := "pi:/" + id
		client.Must("createSession", obj{"channel": resource})
		client.Must("disposeSession", obj{"channel": resource})
		if f.host.Store().Has(wire.SessionURI(id)) {
			t.Fatal("the session channel remains")
		}
	})

	twin.Run(t, "client-workarounds", "restores an empty session after disposal fails so VS Code can retry", func(t *testing.T) {
		attempts := 0
		f := startHydrated(t, hydratedOptions{deleteFile: func(string) (pi.SessionFileDeletionResult, error) {
			attempts++
			if attempts == 1 {
				return pi.SessionFileDeletionResult{Error: "temporary failure"}, nil
			}
			return pi.SessionFileDeletionResult{OK: true}, nil
		}})
		client := f.connectAsVSCode()
		id := newID()
		resource := "pi:/" + id
		client.Must("createSession", obj{"channel": resource})
		e := client.ExpectError("disposeSession", obj{"channel": resource}, wire.CodeInternalError)
		if !strings.Contains(e.Message, "temporary failure") {
			t.Fatalf("message = %q", e.Message)
		}
		client.Must("disposeSession", obj{"channel": resource})
		if attempts != 2 || f.host.Store().Has(wire.SessionURI(id)) {
			t.Fatalf("attempts = %d, channel present = %v", attempts, f.host.Store().Has(wire.SessionURI(id)))
		}
	})

	twin.Run(t, "client-workarounds", "protects a session after its first turn starts", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		client := f.connectAsVSCode()
		id := newID()
		resource := "pi:/" + id
		client.Must("createSession", obj{"channel": resource})
		client.Dispatch(derivedChatURI(resource), obj{"type": "chat/turnStarted", "turnId": "materialized-turn", "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "message": userMessage("keep this")})
		testkit.Eventually(t, "the turn to be accepted", func() bool {
			c := f.host.Store().Chat(wire.ChatURI(id))
			return c != nil && (c.ActiveTurn != nil || len(c.Turns) > 0)
		})
		expectVSCodeDisposalRefusal(t, client, resource)
		if !f.host.Store().Has(wire.SessionURI(id)) {
			t.Fatal("the protected session was disposed")
		}
	})
}

func TestSessionURIWorkaroundsOverTheWire(t *testing.T) {
	twin.Run(t, "client-workarounds", "does not hydrate or dispose a durable session through VS Code's terminal URI", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		client := f.connectAsVSCode()
		channel := "agenthost-terminal:/" + f.sessionID
		if _, err := client.Request("subscribe", obj{"channel": channel}); err == nil {
			t.Fatal("an uncreated terminal must not open a matching session")
		}
		if _, err := client.Request("disposeSession", obj{"channel": channel}); err == nil {
			t.Fatal("a terminal URI must not delete a matching session")
		}
		if f.host.Store().Has(channel) {
			t.Fatal("the terminal channel exists")
		}
		if got := f.deletedFiles(); len(got) != 0 {
			t.Fatalf("deleted = %v", got)
		}
	})

	twin.Run(t, "client-workarounds", "accepts the provider scheme but rejects an undeclared session scheme", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		if _, err := f.client.Request("createSession", obj{"channel": "custom:/" + newID()}); err == nil {
			t.Fatal("an undeclared session scheme must be rejected")
		}
		id := strings.ToUpper(newID())
		uri := "pi:/" + id
		f.client.Must("createSession", obj{"channel": uri})
		result := f.client.Subscribe(uri)
		if result.Snapshot == nil || result.Snapshot.Resource != uri {
			t.Fatalf("snapshot = %+v", result.Snapshot)
		}
		state := snapshotStateOf[ahptypes.SessionState](t, result)
		if state.DefaultChat == nil || *state.DefaultChat != wire.ChatURI(id) {
			t.Fatalf("defaultChat = %v", state.DefaultChat)
		}
		if !f.host.Store().Has(wire.SessionURI(id)) || f.host.Store().Has(uri) {
			t.Fatal("core state stays canonical")
		}
		f.client.Must("disposeSession", obj{"channel": uri})
		if f.host.Store().Has(wire.SessionURI(id)) {
			t.Fatal("the session remains after disposal")
		}
	})
}
