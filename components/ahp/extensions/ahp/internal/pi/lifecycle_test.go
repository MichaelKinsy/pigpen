package pi_test

import (
	"encoding/base64"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/session-lifecycle.test.ts: create, ready, dispose, and the catalogue
// notifications that keep every client's session list current. The fixture is storage-only (no
// backend), so readiness is immediate.

type lifecycle struct {
	t         *testing.T
	h         *harness
	workspace string
}

func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	workspace := t.TempDir()
	return &lifecycle{t: t, h: startHarness(t, harnessOptions{workingDir: workspace}), workspace: workspace}
}

func (l *lifecycle) initialized(subscriptions ...string) *testkit.Client {
	l.t.Helper()
	if len(subscriptions) == 0 {
		subscriptions = []string{wire.RootChannel}
	}
	c := testkit.Connect(l.t, l.h.host)
	l.t.Cleanup(c.Close)
	c.Initialize(nextClientID(), obj{"initialSubscriptions": subscriptions})
	return c
}

// awaitAction waits for the next action envelope on channel whose action has the given type.
func awaitAction(t *testing.T, c *testkit.Client, channel, actionType string) (env ahptypes.ActionEnvelope) {
	t.Helper()
	n, ok := c.Await(func(n testkit.Notification) bool {
		e, isAction := n.Envelope()
		if !isAction || e.Channel != channel {
			return false
		}
		return actionType == "" || testkit.Normalize(t, e.Action).(map[string]any)["type"] == actionType
	}, testkit.Timeout)
	if !ok {
		t.Fatalf("timed out waiting for %s on %s", actionType, channel)
	}
	env, _ = n.Envelope()
	return env
}

func awaitNote(t *testing.T, c *testkit.Client, method string) testkit.Notification {
	t.Helper()
	n, ok := c.Await(func(n testkit.Notification) bool { return n.Method == method }, testkit.Timeout)
	if !ok {
		t.Fatalf("timed out waiting for %s", method)
	}
	return n
}

func TestSessionLifecycle(t *testing.T) {
	twin.Run(t, "session-lifecycle", "creates a session at the client-chosen URI and reports it ready", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		uri := wire.SessionURI(newID())
		c.Must("createSession", obj{"channel": uri, "provider": pi.Provider})
		state := snapshotState[ahptypes.SessionState](t, c, uri)
		if state.Provider != pi.Provider {
			t.Fatalf("provider = %q", state.Provider)
		}
		// Creation is asynchronous in the protocol, but this storage-only fixture has no backend
		// to start, so readiness is immediate.
		testkit.Eventually(t, "the session to be ready", func() bool {
			return l.h.host.Store().Session(uri).Lifecycle == ahptypes.SessionLifecycleReady
		})
		state = *l.h.host.Store().Session(uri)
		if !reflect.DeepEqual(state.WorkingDirectories, []ahptypes.URI{wire.PathToFileURI(l.workspace)}) {
			t.Fatalf("workingDirectories = %v", state.WorkingDirectories)
		}
		// Every session gets exactly one chat, and it is the default. The agent declares no
		// multipleChats capability, which is what tells a client not to call createChat.
		if len(state.Chats) != 1 || state.DefaultChat == nil || *state.DefaultChat != state.Chats[0].Resource {
			t.Fatalf("chats = %+v default %v", state.Chats, state.DefaultChat)
		}
		testkit.AssertValid(t, "state", "SessionState", state)
	})

	twin.Run(t, "session-lifecycle", "uses the first user message as an unnamed session's display title", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		id := newID()
		uri, chat := wire.SessionURI(id), wire.ChatURI(id)
		c.Must("createSession", obj{"channel": uri})
		c.Subscribe(uri)
		c.Subscribe(chat)
		message := userMessage("  Review\n   auth handling  ")
		message["attachments"] = []any{embeddedText("attachment text is not a session title", "context.txt")}
		c.Dispatch(chat, obj{"type": "chat/turnStarted", "turnId": "title-turn", "startedAt": "2025-01-01T00:00:00.000Z", "message": message})
		env := awaitAction(t, c, uri, "session/titleChanged")
		c.Ping()
		sameJSON(t, env.Action, obj{"type": "session/titleChanged", "title": "Review auth handling"}, "title action")
		if env.Origin != nil {
			t.Fatal("origin must be absent")
		}
		session := l.h.host.Store().Session(uri)
		if session.Title != "Review auth handling" || session.Chats[0].Title != "Review auth handling" || l.h.host.Store().Chat(chat).Title != "Review auth handling" {
			t.Fatalf("titles: %q %q %q", session.Title, session.Chats[0].Title, l.h.host.Store().Chat(chat).Title)
		}
		live, _ := l.h.services.Registry.Get(uri)
		if name := live.SessionManager.SessionName(); name != "" {
			t.Fatalf("the fallback title must not be persisted as a session name: %q", name)
		}
		var listed struct{ Items []obj }
		c.Decode(c.Must("listSessions", obj{"channel": wire.RootChannel}), &listed)
		found := false
		for _, item := range listed.Items {
			if item["resource"] == uri {
				found = item["title"] == "Review auth handling"
			}
		}
		if !found {
			t.Fatalf("listing = %v", listed.Items)
		}
	})

	twin.Run(t, "session-lifecycle", "uses pi's session id as the URI's uuid", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		id := newID()
		c.Must("createSession", obj{"channel": wire.SessionURI(id)})
		live, ok := l.h.services.Registry.Get(wire.SessionURI(id))
		if !ok {
			t.Fatal("no live session")
		}
		// One identity space, so no persistent uuid -> session-file mapping.
		if live.SessionID != id {
			t.Fatalf("sessionId = %q", live.SessionID)
		}
		if got := live.SessionManager.File(); !strings.HasSuffix(got, "_"+id+".jsonl") {
			t.Fatalf("session file = %q", got)
		}
	})

	twin.Run(t, "session-lifecycle", "announces the new session on the root channel", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		uri := wire.SessionURI(newID())
		c.Must("createSession", obj{"channel": uri})
		n := awaitNote(t, c, "root/sessionAdded")
		var params struct{ Summary ahptypes.SessionSummary }
		c.Decode(n.Params, &params)
		if params.Summary.Resource != uri {
			t.Fatalf("resource = %s", params.Summary.Resource)
		}
		testkit.AssertValid(t, "state", "SessionSummary", params.Summary)
	})

	twin.Run(t, "session-lifecycle", "rejects a duplicate session URI with SessionAlreadyExists", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		uri := wire.SessionURI(newID())
		c.Must("createSession", obj{"channel": uri})
		c.ExpectError("createSession", obj{"channel": uri}, -32003)
	})

	twin.Run(t, "session-lifecycle", "rejects an unknown provider with ProviderNotFound", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		c.ExpectError("createSession", obj{"channel": wire.SessionURI(newID()), "provider": "claude"}, -32002)
	})

	twin.Run(t, "session-lifecycle", "honours a client-supplied working directory", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		other := t.TempDir() + "/pi ahp cwd"
		if err := os.MkdirAll(other, 0o755); err != nil {
			t.Fatal(err)
		}
		uri := wire.SessionURI(newID())
		workingDirectory := wire.PathToFileURI(other)
		c.Must("createSession", obj{"channel": uri, "workingDirectories": []string{workingDirectory}})
		if got := l.h.host.Store().Session(uri).WorkingDirectories; !reflect.DeepEqual(got, []ahptypes.URI{workingDirectory}) {
			t.Fatalf("workingDirectories = %v", got)
		}
	})

	twin.Run(t, "session-lifecycle", "tracks the active session count in root state", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		count := func() int64 {
			if a := l.h.host.Store().Root(wire.RootChannel).ActiveSessions; a != nil {
				return *a
			}
			return 0
		}
		before := count()
		c.Must("createSession", obj{"channel": wire.SessionURI(newID())})
		if after := count(); after != before+1 {
			t.Fatalf("activeSessions %d -> %d", before, after)
		}
	})

	twin.Run(t, "session-lifecycle", "disposes a session, drops its channel, and announces the removal", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		id := newID()
		uri, chat := wire.SessionURI(id), wire.ChatURI(id)
		c.Must("createSession", obj{"channel": uri})
		c.Subscribe(uri)
		c.Subscribe(chat)
		if !l.h.host.Store().Has(uri) || l.h.host.SubscriberCount(uri) != 1 || l.h.host.SubscriberCount(chat) != 1 {
			t.Fatal("the session is not set up")
		}
		c.Must("disposeSession", obj{"channel": uri})
		n := awaitNote(t, c, "root/sessionRemoved")
		var params struct{ Session string }
		c.Decode(n.Params, &params)
		if params.Session != uri {
			t.Fatalf("removed = %s", params.Session)
		}
		if l.h.host.Store().Has(uri) || l.h.services.Registry.Has(uri) || l.h.host.SubscriberCount(uri) != 0 || l.h.host.SubscriberCount(chat) != 0 {
			t.Fatal("the session was not fully removed")
		}
	})

	twin.Run(t, "session-lifecycle", "does not let session disposal target another channel kind", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		c.ExpectError("disposeSession", obj{"channel": wire.RootChannel}, wire.CodeInvalidParams)
		if !l.h.host.Store().Has(wire.RootChannel) {
			t.Fatal("the root channel was disposed")
		}
	})

	twin.Run(t, "session-lifecycle", "rejects disposing a session that does not exist", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		c.ExpectError("disposeSession", obj{"channel": wire.SessionURI("nope")}, -32001)
	})

	twin.Run(t, "session-lifecycle", "accepts session/titleChanged from a client", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		uri := wire.SessionURI(newID())
		c.Must("createSession", obj{"channel": uri})
		c.Subscribe(uri)
		c.Dispatch(uri, obj{"type": "session/titleChanged", "title": "Refactor auth"})
		// Wait for the echo so the reducer has run before asserting.
		awaitAction(t, c, uri, "session/titleChanged")
		if got := l.h.host.Store().Session(uri).Title; got != "Refactor auth" {
			t.Fatalf("title = %q", got)
		}
	})

	twin.Run(t, "session-lifecycle", "applies VS Code's chat-addressed rename to the owning session", func(t *testing.T) {
		l := newLifecycle(t)
		c := testkit.Connect(t, l.h.host)
		t.Cleanup(c.Close)
		c.Initialize(nextClientID(), obj{"clientInfo": obj{"name": "vscode-editor-window"}})
		id := newID()
		clientSession := "pi:/" + id
		clientChat := "ahp-chat://default/" + base64.RawURLEncoding.EncodeToString([]byte(clientSession))
		c.Must("createSession", obj{"channel": clientSession})
		c.Subscribe(clientSession)
		c.Dispatch(clientChat, obj{"type": "session/titleChanged", "title": "Renamed from VS Code"})
		// session/ready may land first: the registry finishes creating on its own goroutine.
		env := awaitAction(t, c, clientSession, "session/titleChanged")
		if env.Channel != clientSession {
			t.Fatalf("channel = %s", env.Channel)
		}
		sameJSON(t, env.Action, obj{"type": "session/titleChanged", "title": "Renamed from VS Code"}, "action")
		if env.RejectionReason != nil {
			t.Fatalf("rejected: %s", *env.RejectionReason)
		}
		if got := l.h.host.Store().Session(wire.SessionURI(id)).Title; got != "Renamed from VS Code" {
			t.Fatalf("title = %q", got)
		}
	})

	twin.Run(t, "session-lifecycle", "declines VS Code's active client without blocking session creation", func(t *testing.T) {
		l := newLifecycle(t)
		clientID := nextClientID()
		c := testkit.Connect(t, l.h.host)
		t.Cleanup(c.Close)
		c.Initialize(clientID, obj{"clientInfo": obj{"name": "vscode-editor-window"}})
		id := newID()
		uri, clientURI := wire.SessionURI(id), "pi:/"+id
		activeClient := obj{"clientId": clientID, "tools": []any{}}
		// VS Code supplies this eagerly. It is ignored rather than making an otherwise usable
		// session fail.
		c.Must("createSession", obj{"channel": clientURI, "activeClient": activeClient})
		c.Subscribe(clientURI)
		for _, action := range []obj{
			{"type": "session/activeClientSet", "activeClient": activeClient},
			{"type": "session/activeClientRemoved", "clientId": clientID},
		} {
			c.Dispatch(clientURI, action)
			env := awaitAction(t, c, clientURI, action["type"].(string))
			if env.RejectionReason == nil || *env.RejectionReason != "This host does not accept active clients" {
				t.Fatalf("rejectionReason = %v", env.RejectionReason)
			}
		}
		if got := l.h.host.Store().Session(uri).ActiveClients; len(got) != 0 {
			t.Fatalf("activeClients = %v", got)
		}
	})
}

func TestRenamingASession(t *testing.T) {
	renamed := func(t *testing.T, l *lifecycle, uri, title string) {
		t.Helper()
		c := testkit.Connect(t, l.h.host)
		t.Cleanup(c.Close)
		c.Initialize(nextClientID(), nil)
		c.Subscribe(uri)
		seq := c.Dispatch(uri, obj{"type": "session/titleChanged", "title": title})
		c.NextEnvelope(uri, seq)
		c.Ping()
	}

	twin.Run(t, "session-lifecycle", "records the new name on the session", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		uri := wire.SessionURI(newID())
		c.Must("createSession", obj{"channel": uri})
		renamed(t, l, uri, "Refactor auth middleware")
		// The same call Pi's own /resume rename makes, so a session renamed here reads the same
		// from Pi's CLI.
		live, _ := l.h.services.Registry.Get(uri)
		testkit.Eventually(t, "the name to be recorded", func() bool { return live.SessionManager.SessionName() == "Refactor auth middleware" })
	})

	twin.Run(t, "session-lifecycle", "does not create a file for a session that never got a reply", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		uri := wire.SessionURI(newID())
		c.Must("createSession", obj{"channel": uri})
		renamed(t, l, uri, "Never answered")
		// Pi withholds the file until a session has an assistant message, so an empty
		// conversation leaves nothing behind: renaming does not change that policy, and forcing a
		// write here would litter the disk with empty sessions.
		live, _ := l.h.services.Registry.Get(uri)
		file := live.SessionManager.File()
		if file == "" {
			t.Fatal("no session file path")
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("a file exists for an unanswered session: %v", err)
		}
	})

	twin.Run(t, "session-lifecycle", "mirrors the name onto the chat and the catalogue entry", func(t *testing.T) {
		l := newLifecycle(t)
		c := l.initialized()
		id := newID()
		uri := wire.SessionURI(id)
		c.Must("createSession", obj{"channel": uri})
		renamed(t, l, uri, "Ship the release")
		session, chat := l.h.host.Store().Session(uri), l.h.host.Store().Chat(wire.ChatURI(id))
		if session.Title != "Ship the release" || chat.Title != "Ship the release" || session.Chats[0].Title != "Ship the release" {
			t.Fatalf("titles: %q %q %q", session.Title, chat.Title, session.Chats[0].Title)
		}
		// ChatState denormalises its summary fields, so both representations have to move together
		// or a client watching only the session drifts.
		c.Dispatch(wire.ChatURI(id), obj{"type": "chat/turnStarted", "turnId": "after-rename", "startedAt": "2025-01-01T00:00:00.000Z", "message": userMessage("This must not replace the explicit name")})
		c.Ping()
		if got := l.h.host.Store().Session(uri).Title; got != "Ship the release" {
			t.Fatalf("title = %q", got)
		}
	})
}
