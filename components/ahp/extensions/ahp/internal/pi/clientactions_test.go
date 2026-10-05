package pi_test

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/client-actions.test.ts: the client-action policy of this host, seen from
// a sender and an observer. The fixture is storage-only (no backend).

type actionsFixture struct {
	t         *testing.T
	h         *harness
	client    *testkit.Client
	observer  *testkit.Client
	workspace string
}

func startActions(t *testing.T) *actionsFixture {
	t.Helper()
	workspace := t.TempDir()
	f := &actionsFixture{t: t, workspace: workspace, h: startHarness(t, harnessOptions{workingDir: workspace})}
	f.client, f.observer = testkit.Connect(t, f.h.host), testkit.Connect(t, f.h.host)
	t.Cleanup(f.client.Close)
	t.Cleanup(f.observer.Close)
	f.client.Initialize(nextClientID(), nil)
	f.observer.Initialize(nextClientID(), nil)
	return f
}

func (f *actionsFixture) createChannels() (session, chat string) {
	f.t.Helper()
	id := newID()
	session, chat = wire.SessionURI(id), wire.ChatURI(id)
	f.client.Must("createSession", obj{"channel": session})
	var snapshot ahptypes.SubscribeResult
	for _, c := range []*testkit.Client{f.client, f.observer} {
		snapshot = c.Subscribe(session)
		c.Subscribe(chat)
	}
	// The host attaches the session's backend on its own goroutine, so the subscriber may see lifecycle
	// "creating" and then the host's session/ready action. Wait for that action (the snapshot already
	// says "ready" when the host won the race) so that a state taken before a client action is the state
	// the session settles in. Upstream's harness is ready synchronously in storage-only mode.
	if snapshot.Snapshot == nil || testkit.Normalize(f.t, snapshot.Snapshot.State).(map[string]any)["lifecycle"] != string(ahptypes.SessionLifecycleReady) {
		awaitAction(f.t, f.observer, session, "session/ready")
	}
	return session, chat
}

func (f *actionsFixture) state(channel string) any {
	if wire.IsChatChannel(channel) {
		return f.h.host.Store().Chat(channel)
	}
	return f.h.host.Store().Session(channel)
}

// rawEnvelope is an action envelope decoded generically: a rejected malformed action cannot be
// decoded into the typed union, and the echo of one must still be checkable.
type rawEnvelope struct {
	Channel         string
	Action          map[string]any
	ServerSeq       int64
	Origin          *struct{ ClientSeq int64 }
	RejectionReason *string
}

func (f *actionsFixture) nextRaw(c *testkit.Client, channel string, seq int64) rawEnvelope {
	f.t.Helper()
	n, ok := c.Await(func(n testkit.Notification) bool {
		if n.Method != "action" {
			return false
		}
		var e rawEnvelope
		return json.Unmarshal(n.Params, &e) == nil && e.Channel == channel && e.Origin != nil && e.Origin.ClientSeq == seq
	}, testkit.Timeout)
	if !ok {
		f.t.Fatalf("timed out waiting for the echo of clientSeq %d on %s", seq, channel)
	}
	var e rawEnvelope
	_ = json.Unmarshal(n.Params, &e)
	return e
}

func (f *actionsFixture) expectRejected(channel string, action obj, reason string) {
	f.t.Helper()
	before := testkit.Normalize(f.t, f.state(channel))
	seq := f.client.Dispatch(channel, action)
	sender := f.nextRaw(f.client, channel, seq)
	seen := f.nextRaw(f.observer, channel, seq)
	if sender.Action["type"] != action["type"] {
		f.t.Fatalf("%v: echoed action type %v", action["type"], sender.Action["type"])
	}
	if sender.RejectionReason == nil || !regexp.MustCompile(reason).MatchString(*sender.RejectionReason) {
		f.t.Fatalf("%v: rejectionReason = %v, want /%s/", action["type"], sender.RejectionReason, reason)
	}
	if seen.ServerSeq != sender.ServerSeq || seen.RejectionReason == nil || *seen.RejectionReason != *sender.RejectionReason {
		f.t.Fatalf("%v: the observer saw a different envelope: %+v vs %+v", action["type"], seen, sender)
	}
	sameJSON(f.t, seen.Action, sender.Action, "observer's action")
	sameJSON(f.t, f.state(channel), before, "state must not change")
}

// The policy cases snapshot a channel's state and compare it after a rejected action, so the fixture must hand
// back sessions that have finished starting: the host attaches a session's backend on its own goroutine, and a
// "creating" snapshot followed by the "ready" transition is not a change the rejected action made.
func TestActionsFixtureHandsBackReadySessions(t *testing.T) {
	f := startActions(t)
	for i := 0; i < 200; i++ {
		session, _ := f.createChannels()
		if got := f.h.host.Store().Session(session).Lifecycle; got != ahptypes.SessionLifecycleReady {
			t.Fatalf("session %d lifecycle = %s, want ready", i, got)
		}
	}
}

func TestPiClientActionPolicy(t *testing.T) {
	twin.Run(t, "client-actions", "accounts for every client action on channels this host serves", func(t *testing.T) {
		var actual []string
		for _, typ := range wire.ClientDispatchableTypes() {
			for _, prefix := range []string{"root/", "session/", "chat/", "terminal/"} {
				if strings.HasPrefix(typ, prefix) {
					actual = append(actual, typ)
				}
			}
		}
		expected := []string{
			"root/configChanged", "session/titleChanged", "session/activeClientSet", "session/activeClientRemoved",
			"session/workingDirectorySet", "session/workingDirectoryRemoved", "session/workingDirectoryReplaced",
			"session/customizationToggled", "session/mcpServerStartRequested", "session/mcpServerStopRequested",
			"session/isReadChanged", "session/isArchivedChanged", "session/configChanged",
			"chat/turnStarted", "chat/turnResume", "chat/toolCallConfirmed", "chat/toolCallComplete",
			"chat/toolCallResultConfirmed", "chat/toolCallContentChanged", "chat/turnCancelled",
			"chat/workingDirectorySet", "chat/workingDirectoryRemoved", "chat/pendingMessageSet",
			"chat/pendingMessageRemoved", "chat/queuedMessagesReordered", "chat/draftChanged",
			"chat/inputAnswerChanged", "chat/inputCompleted", "chat/truncated",
			"terminal/input", "terminal/resized", "terminal/claimed", "terminal/titleChanged", "terminal/cleared",
			// Client-dispatchable in the pinned spec commit but absent from the npm 0.9.0 package
			// upstream tested against (see PORT.md); this host refuses both.
			"chat/isArchivedChanged", "session/mcpServerBackgroundRequested",
		}
		sort.Strings(expected)
		sameJSON(t, actual, expected, "client-dispatchable actions on served channels")
	})

	twin.Run(t, "client-actions", "rejects actions addressed to the wrong channel kind", func(t *testing.T) {
		f := startActions(t)
		session, chat := f.createChannels()
		f.expectRejected(session, obj{"type": "chat/draftChanged", "draft": userMessage("wrong channel")}, `does not belong on a session channel`)
		f.expectRejected(chat, obj{"type": "session/titleChanged", "title": "wrong channel"}, `does not belong on a chat channel`)
		f.expectRejected(chat, obj{"type": "terminal/cleared"}, `does not belong on a chat channel`)
		f.expectRejected(session, obj{"type": "root/configChanged", "config": obj{}}, `does not belong on a session channel`)
	})

	twin.Run(t, "client-actions", "rejects capabilities pi does not implement", func(t *testing.T) {
		f := startActions(t)
		session, chat := f.createChannels()
		wd := "file://" + f.workspace
		for _, a := range []obj{
			{"type": "session/workingDirectorySet", "directory": "file:///tmp/other"},
			{"type": "session/workingDirectoryRemoved", "directory": wd},
			{"type": "session/workingDirectoryReplaced", "directory": wd, "replacement": "file:///tmp/other"},
		} {
			f.expectRejected(session, a, `changing working directories`)
		}
		for _, a := range []obj{
			{"type": "chat/workingDirectorySet", "directory": "file:///tmp/other"},
			{"type": "chat/workingDirectoryRemoved", "directory": wd},
		} {
			f.expectRejected(chat, a, `changing working directories`)
		}
		f.expectRejected(session, obj{"type": "session/customizationToggled", "id": "plugin", "enablement": []any{}}, `customizations`)
		for _, typ := range []string{"session/mcpServerStartRequested", "session/mcpServerStopRequested", "session/mcpServerBackgroundRequested"} {
			f.expectRejected(session, obj{"type": typ, "id": "mcp"}, `MCP servers`)
		}
		f.expectRejected(session, obj{"type": "session/isReadChanged", "isRead": false}, `read or archive state`)
		f.expectRejected(session, obj{"type": "session/isArchivedChanged", "isArchived": true}, `read or archive state`)
		f.expectRejected(chat, obj{"type": "chat/isArchivedChanged", "isArchived": true}, `read or archive state`)
		f.expectRejected(session, obj{"type": "session/configChanged", "config": obj{"probe": true}}, `no mutable configuration`)
		for _, a := range []obj{
			{"type": "chat/toolCallConfirmed", "turnId": "turn", "toolCallId": "tool", "approved": false, "reason": "denied"},
			{"type": "chat/toolCallComplete", "turnId": "turn", "toolCallId": "tool", "result": obj{"success": false, "pastTenseMessage": "Failed"}},
			{"type": "chat/toolCallResultConfirmed", "turnId": "turn", "toolCallId": "tool", "approved": true},
			{"type": "chat/toolCallContentChanged", "turnId": "turn", "toolCallId": "tool", "content": []any{}},
		} {
			f.expectRejected(chat, a, `client tool execution or confirmation`)
		}
		f.expectRejected(chat, obj{"type": "chat/turnResume", "turnId": "turn"}, `cannot resume an errored turn`)
		f.expectRejected(chat, obj{"type": "chat/inputAnswerChanged", "requestId": "input", "questionId": "question"}, `interactive input requests`)
		f.expectRejected(chat, obj{"type": "chat/inputCompleted", "requestId": "input", "response": "cancel"}, `interactive input requests`)
	})

	twin.Run(t, "client-actions", "rejects malformed supported actions without mutating state", func(t *testing.T) {
		f := startActions(t)
		session, chat := f.createChannels()
		f.expectRejected(session, obj{"type": "session/titleChanged", "title": 42}, `title must be a string`)
		f.expectRejected(chat, obj{"type": "chat/turnStarted", "turnId": "malformed", "startedAt": "2025-01-01T00:00:00.000Z",
			"message": obj{"text": 42, "origin": obj{"kind": "user"}}}, `requires text and an origin`)
		f.expectRejected(chat, obj{"type": "chat/queuedMessagesReordered", "order": []any{42}}, `order must be an array of ids`)
	})

	twin.Run(t, "client-actions", "validates turn and user-message invariants", func(t *testing.T) {
		f := startActions(t)
		_, chat := f.createChannels()
		agent := userMessage("not from the user")
		agent["origin"] = obj{"kind": "agent"}
		at := "2025-01-01T00:00:00.000Z"
		f.expectRejected(chat, obj{"type": "chat/turnStarted", "turnId": "agent-turn", "startedAt": at, "message": agent}, `only start a turn with a user message`)
		f.expectRejected(chat, obj{"type": "chat/pendingMessageSet", "kind": "queued", "id": "agent-queue", "message": agent}, `only queue a user message`)
		f.expectRejected(chat, obj{"type": "chat/draftChanged", "draft": agent}, `only draft a user message`)
		withContext := userMessage("with context")
		withContext["attachments"] = []any{obj{"type": "simple", "label": "context"}}
		f.expectRejected(chat, obj{"type": "chat/turnStarted", "turnId": "attachment-turn", "startedAt": at, "message": withContext}, `requires modelRepresentation`)
		customAgent := userMessage("custom agent")
		customAgent["agent"] = obj{"uri": "agent:/fixture"}
		f.expectRejected(chat, obj{"type": "chat/draftChanged", "draft": customAgent}, `custom agents`)
		f.expectRejected(chat, obj{"type": "chat/turnStarted", "turnId": "queued-turn", "startedAt": at, "message": userMessage("queued"), "queuedMessageId": "client-owned"}, `Only the host can start a queued message`)
		f.expectRejected(chat, obj{"type": "chat/turnCancelled", "turnId": "absent", "duration": 0}, `No matching active turn`)
		f.expectRejected(chat, obj{"type": "chat/pendingMessageRemoved", "kind": "queued", "id": "absent"}, `No matching queued message`)
		f.h.host.DispatchServerAction(chat, ahptypes.StateAction{Value: &ahptypes.ChatPendingMessageSetAction{
			Type: ahptypes.ActionTypeChatPendingMessageSet, Kind: ahptypes.PendingMessageKindSteering, Id: "steering",
			Message: ahptypes.Message{Text: "steer", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
		}})
		f.expectRejected(chat, obj{"type": "chat/pendingMessageRemoved", "kind": "steering", "id": "steering"}, `cannot be withdrawn`)
		f.h.host.DispatchServerAction(chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
			Type: ahptypes.ActionTypeChatTurnStarted, TurnId: "active", StartedAt: at,
			Message: ahptypes.Message{Text: "active", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
		}})
		f.expectRejected(chat, obj{"type": "chat/turnStarted", "turnId": "second", "startedAt": at, "message": userMessage("second")}, `A turn is already active`)
	})

	twin.Run(t, "client-actions", "keeps state-only draft and queued-message actions", func(t *testing.T) {
		f := startActions(t)
		_, chat := f.createChannels()
		accepted := func(action obj) {
			t.Helper()
			seq := f.client.Dispatch(chat, action)
			if env := f.client.NextEnvelope(chat, seq); env.RejectionReason != nil {
				t.Fatalf("%v rejected: %s", action["type"], *env.RejectionReason)
			}
		}
		accepted(obj{"type": "chat/draftChanged", "draft": userMessage("draft")})
		if got := f.h.host.Store().Chat(chat).Draft; got == nil || got.Text != "draft" {
			t.Fatalf("draft = %+v", got)
		}
		for _, id := range []string{"first", "second"} {
			f.h.host.DispatchServerAction(chat, ahptypes.StateAction{Value: &ahptypes.ChatPendingMessageSetAction{
				Type: ahptypes.ActionTypeChatPendingMessageSet, Kind: ahptypes.PendingMessageKindQueued, Id: id,
				Message: ahptypes.Message{Text: id, Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
			}})
		}
		ids := func() []string {
			var out []string
			for _, m := range f.h.host.Store().Chat(chat).QueuedMessages {
				out = append(out, m.Id)
			}
			return out
		}
		accepted(obj{"type": "chat/queuedMessagesReordered", "order": []string{"second", "first"}})
		sameJSON(t, ids(), []string{"second", "first"}, "queued order")
		accepted(obj{"type": "chat/pendingMessageRemoved", "kind": "queued", "id": "first"})
		sameJSON(t, ids(), []string{"second"}, "queued after removal")
	})
}
