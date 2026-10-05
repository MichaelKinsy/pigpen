package host_test

import (
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

func derivedChat(providerSession string) string {
	return "ahp-chat://default/" + base64.RawURLEncoding.EncodeToString([]byte(providerSession))
}

// Twins of upstream test/client-workarounds.test.ts, describe "session URI classification"
// (the cases that need no session registry; the wire-level ones live with the registry tests).
func TestSessionURIClassification(t *testing.T) {
	twin.Run(t, "client-workarounds", "recognises only canonical or explicitly allowed session schemes", func(t *testing.T) {
		id := "9991C40A-74CC-4991-85CC-F37CD2BFF065"
		check := func(uri string, aliases []string, want string, ok bool) {
			t.Helper()
			got, gotOK := wire.SessionIDFromURI(uri, aliases...)
			if got != want || gotOK != ok {
				t.Errorf("SessionIDFromURI(%q, %v) = %q, %v; want %q, %v", uri, aliases, got, gotOK, want, ok)
			}
		}
		check("pi:/"+id, []string{"pi"}, id, true)
		check("ahp-session:/abc", nil, "abc", true)
		check("copilot:/test-session", nil, "", false)
		check("copilot:/test-session", []string{"copilot"}, "test-session", true)
		check("pi://test-session", []string{"pi"}, "", false)
	})

	twin.Run(t, "client-workarounds", "does not infer sessions from other channel schemes", func(t *testing.T) {
		for _, uri := range []string{"ahp-chat:/c1", "ahp-terminal:/t1", "agenthost-terminal:/t1", "file:///etc/passwd"} {
			if id, ok := wire.SessionIDFromURI(uri, "pi"); ok {
				t.Errorf("%s classified as session %q", uri, id)
			}
		}
	})

	twin.Run(t, "client-workarounds", "retargets VS Code's chat-addressed rename to the owning session", func(t *testing.T) {
		id := "rename-me"
		w := host.NewClientWorkarounds("pi")
		w.Identify("vscode-editor-window")
		msg := map[string]any{"jsonrpc": "2.0", "method": "dispatchAction", "params": map[string]any{
			"channel": derivedChat("pi:/" + id), "clientSeq": float64(1),
			"action": map[string]any{"type": "session/titleChanged", "title": "Renamed"},
		}}
		if err := w.ApplyToIncoming(msg); err != nil {
			t.Fatal(err)
		}
		if got := msg["params"].(map[string]any)["channel"]; got != wire.SessionURI(id) {
			t.Fatalf("channel = %v, want %s", got, wire.SessionURI(id))
		}
	})

	twin.Run(t, "client-workarounds", "rewrites URI-bearing action and catalogue fields for VS Code", func(t *testing.T) {
		id := "nested-fields"
		session, chat := wire.SessionURI(id), wire.ChatURI(id)
		providerSession := "pi:/" + id
		derived := derivedChat(providerSession)
		w := host.NewClientWorkarounds("pi")
		w.Identify("vscode-editor-window")

		removed := w.ApplyToOutgoing(map[string]any{"jsonrpc": "2.0", "method": "root/sessionRemoved", "params": map[string]any{"channel": wire.RootChannel, "session": session}})
		wantRemoved := map[string]any{"jsonrpc": "2.0", "method": "root/sessionRemoved", "params": map[string]any{"channel": wire.RootChannel, "session": providerSession}}
		if !reflect.DeepEqual(removed, wantRemoved) {
			t.Fatalf("removed = %#v", removed)
		}

		updated := w.ApplyToOutgoing(map[string]any{"jsonrpc": "2.0", "method": "action", "params": map[string]any{
			"channel": session, "serverSeq": float64(1),
			"action": map[string]any{"type": "session/chatUpdated", "chat": chat, "changes": map[string]any{"resource": chat}},
		}})
		wantUpdated := map[string]any{"jsonrpc": "2.0", "method": "action", "params": map[string]any{
			"channel": providerSession, "serverSeq": float64(1),
			"action": map[string]any{"type": "session/chatUpdated", "chat": derived, "changes": map[string]any{"resource": derived}},
		}}
		if !reflect.DeepEqual(updated, wantUpdated) {
			t.Fatalf("updated = %#v", testkit.Normalize(t, updated))
		}
	})

	twin.Run(t, "client-workarounds", "leaves VS Code's client-chosen terminal URI outside session translation", func(t *testing.T) {
		w := host.NewClientWorkarounds("pi")
		w.Identify("vscode-editor-window")
		msg := map[string]any{"jsonrpc": "2.0", "method": "action", "params": map[string]any{"channel": "agenthost-terminal:/terminal-1"}}
		if got := w.ApplyToOutgoing(msg); !reflect.DeepEqual(got, msg) {
			t.Fatalf("got %#v", got)
		}
	})
}
