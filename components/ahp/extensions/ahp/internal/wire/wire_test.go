package wire_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/uri.test.ts (src/core/uri.ts).
func TestFileURIConversion(t *testing.T) {
	twin.Run(t, "uri", "decodes percent-escapes", func(t *testing.T) {
		got, err := wire.FileURIToPath("file:///tmp/a%20b")
		if err != nil || got != "/tmp/a b" {
			t.Fatalf("got %q, %v; want /tmp/a b", got, err)
		}
	})
	twin.Run(t, "uri", "round-trips a path", func(t *testing.T) {
		got, err := wire.FileURIToPath(wire.PathToFileURI("/tmp/a b/c"))
		if err != nil || got != "/tmp/a b/c" {
			t.Fatalf("got %q, %v; want /tmp/a b/c", got, err)
		}
	})
	twin.Run(t, "uri", "passes a bare path through", func(t *testing.T) {
		// Clients occasionally send a path where the protocol asks for a URI.
		got, err := wire.FileURIToPath("/tmp/plain")
		if err != nil || got != "/tmp/plain" {
			t.Fatalf("got %q, %v; want /tmp/plain", got, err)
		}
	})
	twin.Run(t, "uri", "refuses a URI naming a remote host", func(t *testing.T) {
		if got, err := wire.FileURIToPath("file://host/share/x"); err == nil {
			t.Fatalf("expected an error, got %q", got)
		}
	})
}

// Channel helpers (src/core/channels.ts); no upstream test file, so these are additional Go cases.
func TestChannelKinds(t *testing.T) {
	cases := map[string]wire.ChannelKind{
		"ahp-root://":           wire.KindRoot,
		"ahp-session:/abc":      wire.KindSession,
		"ahp-chat:/abc":         wire.KindChat,
		"ahp-terminal:/t1":      wire.KindTerminal,
		"ahp-changeset:/c":      wire.KindChangeset,
		"ahp-resource-watch:/w": wire.KindResourceWatch,
	}
	for uri, want := range cases {
		if got, ok := wire.KindOf(uri); !ok || got != want {
			t.Errorf("KindOf(%q) = %v, %v; want %v", uri, got, ok, want)
		}
	}
	for _, uri := range []string{"ahp-unknown:/x", "pi:/abc", "", "ahp-session:"} {
		if got, ok := wire.KindOf(uri); ok {
			t.Errorf("KindOf(%q) = %v, want unsupported", uri, got)
		}
	}
	if !wire.ActionBelongsToChannel("chat/delta", wire.KindChat) || wire.ActionBelongsToChannel("chat/delta", wire.KindSession) {
		t.Error("action namespace must match the channel kind")
	}
	if wire.ActionBelongsToChannel("resourceWatch/changed", wire.KindResourceWatch) != true {
		t.Error("resourceWatch/ belongs to resourceWatch")
	}
	if wire.SessionURI("x") != "ahp-session:/x" || wire.ChatURI("x") != "ahp-chat:/x" {
		t.Error("uri constructors")
	}
	if id, ok := wire.ChatIDFromURI("ahp-chat:/abc"); !ok || id != "abc" {
		t.Errorf("ChatIDFromURI = %q, %v", id, ok)
	}
	if _, ok := wire.ChatIDFromURI("ahp-session:/abc"); ok {
		t.Error("a session URI is not a chat")
	}
	if id, ok := wire.SessionIDFromURI("ahp-session:/abc"); !ok || id != "abc" {
		t.Errorf("SessionIDFromURI canonical = %q, %v", id, ok)
	}
	if id, ok := wire.SessionIDFromURI("pi:/abc", "pi"); !ok || id != "abc" {
		t.Errorf("SessionIDFromURI alias = %q, %v", id, ok)
	}
	if _, ok := wire.SessionIDFromURI("pi:/abc"); ok {
		t.Error("shape alone never makes an unknown URI a session")
	}
	if _, ok := wire.SessionIDFromURI("ahp-session:/a/b"); ok {
		t.Error("a session URI has a single path segment")
	}
}

func TestNegotiateProtocolVersion(t *testing.T) {
	got, err := wire.NegotiateProtocolVersion([]string{"0.9.0", "0.8.0"})
	if err != nil || got != "0.9.0" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = wire.NegotiateProtocolVersion([]string{"99.0.0", "0.9.0"})
	if err != nil || got != "0.9.0" {
		t.Fatalf("unknown versions are skipped: got %q, %v", got, err)
	}
	_, err = wire.NegotiateProtocolVersion([]string{"0.8.0"})
	var pe *wire.Error
	if !asError(err, &pe) || pe.Code != wire.CodeUnsupportedProtocolVersion {
		t.Fatalf("want UnsupportedProtocolVersion, got %v", err)
	}
	if !reflect.DeepEqual(pe.Data, map[string]any{"supportedVersions": []string{"0.9.0"}}) {
		t.Fatalf("error data = %#v", pe.Data)
	}
	if _, err = wire.NegotiateProtocolVersion(nil); err == nil {
		t.Fatal("an empty offer is invalid")
	}
}

// The table is generated from the pinned spec's @clientDispatchable annotations
// (types/action-origin.generated.ts, IS_CLIENT_DISPATCHABLE); this checks a sample of both kinds.
func TestClientDispatchable(t *testing.T) {
	for _, typ := range []string{"root/configChanged", "session/titleChanged", "chat/turnStarted", "chat/turnCancelled", "chat/truncated", "terminal/input", "terminal/cleared"} {
		if !wire.IsClientDispatchable(typ) {
			t.Errorf("%s must be client-dispatchable", typ)
		}
	}
	for _, typ := range []string{"root/agentsChanged", "session/ready", "chat/delta", "chat/turnComplete", "terminal/data", "resourceWatch/changed", "nope/unknown"} {
		if wire.IsClientDispatchable(typ) {
			t.Errorf("%s must be server-only", typ)
		}
	}
}
