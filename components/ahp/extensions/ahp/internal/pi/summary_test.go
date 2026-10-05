package pi_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahp"
	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of the wire-level "session summary over the wire" describe of upstream
// test/session-summary.test.ts: projections checked with the unmodified AHP reducers.

const (
	startAt = "2025-01-01T00:00:00.000Z"
	endAt   = "2025-01-01T00:00:01.000Z"
)

func noopBackend(t *testing.T) *scriptedBackend {
	return newScriptedBackend(func(string) []mapper.Event { return nil })
}

// summaryFixture is upstream's fixture(): a session created through its provider alias, with the
// session and chat subscribed.
type summaryFixture struct {
	t        *testing.T
	h        *harness
	client   *testkit.Client
	clientID string
	session  string // canonical
	alias    string
	chat     string
	initial  ahptypes.SessionState
	mark     int
}

func newSummaryFixture(t *testing.T, factory pi.BackendFactory) *summaryFixture {
	t.Helper()
	defaultBackend := factory == nil
	if defaultBackend {
		factory = func(*pi.LiveSession) (pi.Backend, error) { return noopBackend(t), nil }
	}
	h := startHarness(t, harnessOptions{createBackend: factory})
	client := testkit.Connect(t, h.host)
	t.Cleanup(client.Close)
	clientID := nextClientID()
	client.Initialize(clientID, obj{"initialSubscriptions": []string{wire.RootChannel}})
	id := newID()
	f := &summaryFixture{t: t, h: h, client: client, clientID: clientID, session: wire.SessionURI(id), alias: "pi:/" + id, chat: wire.ChatURI(id)}
	client.Must("createSession", obj{"channel": f.alias})
	// The registry finishes creating on its own goroutine (upstream does it in the same tick), so
	// wait for the lifecycle to leave "creating" before taking the baseline the replay tests measure from.
	if defaultBackend {
		testkit.Eventually(t, "the session to finish creating", func() bool {
			s := f.sessionState()
			return s != nil && s.Lifecycle != ahptypes.SessionLifecycleCreating
		})
	}
	sub := client.Subscribe(f.alias)
	f.initial = decodeSession(t, sub.Snapshot.State)
	client.Subscribe(f.chat)
	client.Ping()
	f.mark = len(client.Notifications())
	return f
}

func decodeSession(t *testing.T, state any) ahptypes.SessionState {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var s ahptypes.SessionState
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *summaryFixture) clear() { f.mark = len(f.client.Notifications()) }

func (f *summaryFixture) notes() []testkit.Notification { return f.client.Notifications()[f.mark:] }

func (f *summaryFixture) envelopes() []ahptypes.ActionEnvelope {
	var out []ahptypes.ActionEnvelope
	for _, n := range f.notes() {
		if env, ok := n.Envelope(); ok {
			out = append(out, env)
		}
	}
	return out
}

func (f *summaryFixture) changes() []obj {
	out := []obj{}
	for _, n := range f.notes() {
		if n.Method == "root/sessionSummaryChanged" {
			out = append(out, testkit.Normalize(f.t, n.Params).(map[string]any))
		}
	}
	return out
}

func (f *summaryFixture) start() {
	f.client.Dispatch(f.chat, obj{"type": "chat/turnStarted", "turnId": "turn", "startedAt": startAt, "message": userMessage("go")})
	f.client.Ping()
}

func (f *summaryFixture) list() obj {
	f.t.Helper()
	raw := f.client.Must("listSessions", obj{"channel": wire.RootChannel})
	var result struct{ Items []obj }
	if err := json.Unmarshal(raw, &result); err != nil {
		f.t.Fatal(err)
	}
	if len(result.Items) != 1 {
		f.t.Fatalf("listSessions returned %d items: %s", len(result.Items), raw)
	}
	return result.Items[0]
}

func (f *summaryFixture) sessionState() *ahptypes.SessionState {
	return f.h.host.Store().Session(f.session)
}
func (f *summaryFixture) chatState() *ahptypes.ChatState { return f.h.host.Store().Chat(f.chat) }

func changeNote(f *summaryFixture, changes obj) obj {
	return obj{"channel": wire.RootChannel, "session": f.alias, "changes": changes}
}

func sameJSON(t *testing.T, got, want any, msg string) {
	t.Helper()
	g, w := testkit.Normalize(t, got), testkit.Normalize(t, want)
	if !reflect.DeepEqual(g, w) {
		gj, _ := json.MarshalIndent(g, "", " ")
		wj, _ := json.MarshalIndent(w, "", " ")
		t.Fatalf("%s\n got: %s\nwant: %s", msg, gj, wj)
	}
}

func TestSessionSummaryOverTheWire(t *testing.T) {
	twin.Run(t, "session-summary", "projects an active turn closed by backend startup failure", func(t *testing.T) {
		startup := newRelease()
		f := newSummaryFixture(t, func(*pi.LiveSession) (pi.Backend, error) {
			<-startup.ch
			return nil, errors.New("backend unavailable")
		})
		f.start()
		f.clear()
		live, _ := f.h.services.Registry.Get(f.session)
		var attaching <-chan struct{}
		testkit.Eventually(t, "the backend attach to be in flight", func() bool { attaching = live.Attaching(); return attaching != nil })
		startup.open()
		<-attaching
		f.client.Ping()
		sameJSON(t, f.changes(), []obj{changeNote(f, obj{"status": float64(ahptypes.SessionStatusError)})}, "root notifications")
		state := f.sessionState()
		if state.Lifecycle != ahptypes.SessionLifecycleFailed {
			t.Fatalf("lifecycle = %s", state.Lifecycle)
		}
		if state.Chats[0].Status != ahptypes.SessionStatusError {
			t.Fatalf("chat status = %d", state.Chats[0].Status)
		}
		if got := f.list()["status"]; got != float64(ahptypes.SessionStatusError) {
			t.Fatalf("listed status = %v", got)
		}
	})

	twin.Run(t, "session-summary", "publishes exact start deltas and lists an unpersisted session for new clients", func(t *testing.T) {
		f := newSummaryFixture(t, nil)
		f.start()
		sameJSON(t, f.changes(), []obj{
			changeNote(f, obj{"title": "go"}),
			changeNote(f, obj{"status": float64(ahptypes.SessionStatusInProgress), "modifiedAt": startAt}),
		}, "root notifications")
		actions := f.envelopes()
		if len(actions) != 3 {
			t.Fatalf("%d action envelopes, want 3", len(actions))
		}
		if _, ok := actions[0].Action.Value.(*ahptypes.ChatTurnStartedAction); !ok {
			t.Fatalf("first action = %T", actions[0].Action.Value)
		}
		title := actions[1]
		if title.Channel != f.alias {
			t.Fatalf("title channel = %s", title.Channel)
		}
		sameJSON(t, title.Action, obj{"type": "session/titleChanged", "title": "go"}, "title action")
		if title.Origin != nil {
			t.Fatal("a host-originated action must carry no origin")
		}
		update := actions[2]
		if update.Channel != f.alias {
			t.Fatalf("update channel = %s", update.Channel)
		}
		summary := channels.ChatSummaryOf(f.chatState())
		expected := testkit.Normalize(t, summary).(map[string]any)
		delete(expected, "resource")
		sameJSON(t, update.Action, obj{"type": "session/chatUpdated", "chat": f.chat, "changes": expected}, "chat update action")
		if update.RejectionReason != nil || update.Origin != nil {
			t.Fatal("the projection must be an unrejected host action")
		}
		if title.ServerSeq != actions[0].ServerSeq+1 || update.ServerSeq != title.ServerSeq+1 {
			t.Fatalf("serverSeq %d %d %d not consecutive", actions[0].ServerSeq, title.ServerSeq, update.ServerSeq)
		}
		if got := f.sessionState().Status; got != ahptypes.SessionStatusIdle {
			t.Fatalf("session status = %d; the reducer cannot update it", got)
		}
		live, _ := f.h.services.Registry.Get(f.session)
		if _, err := os.Stat(live.SessionManager.File()); !os.IsNotExist(err) {
			t.Fatalf("session file exists before an assistant message: %v", err)
		}
		listed := f.list()
		if listed["resource"] != f.alias || listed["title"] != "go" || listed["status"] != float64(ahptypes.SessionStatusInProgress) || listed["modifiedAt"] != startAt {
			t.Fatalf("live listing = %v", listed)
		}
		if _, has := listed["activity"]; has {
			t.Fatal("the root catalogue must not carry activity")
		}
		other := testkit.Connect(t, f.h.host)
		t.Cleanup(other.Close)
		other.Initialize(nextClientID(), nil)
		raw := other.Must("listSessions", obj{"channel": wire.RootChannel})
		want := obj{}
		for k, v := range listed {
			want[k] = v
		}
		want["resource"] = f.session
		var result struct{ Items []any }
		_ = json.Unmarshal(raw, &result)
		sameJSON(t, result.Items, []any{want}, "another client's listing")
	})

	twin.Run(t, "session-summary", "clears activity through a full upsert and sends no redundant root updates or delta projections", func(t *testing.T) {
		f := newSummaryFixture(t, nil)
		f.start()
		startActions := f.envelopes()
		f.clear()
		thinking := "Thinking"
		f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatActivityChangedAction{Type: ahptypes.ActionTypeChatActivityChanged, Activity: &thinking}})
		f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatActivityChangedAction{Type: ahptypes.ActionTypeChatActivityChanged}})
		f.client.Ping()
		if got := f.changes(); len(got) != 0 {
			t.Fatalf("activity is not published on the root catalogue: %v", got)
		}
		actions := f.envelopes()
		var types []string
		for _, e := range actions {
			types = append(types, testkit.Normalize(t, e.Action).(map[string]any)["type"].(string))
		}
		want := []string{"chat/activityChanged", "session/chatUpdated", "session/activityChanged", "chat/activityChanged", "session/chatAdded", "session/activityChanged"}
		if !reflect.DeepEqual(types, want) {
			t.Fatalf("actions = %v, want %v", types, want)
		}
		mirror := f.initial
		for _, e := range append(startActions, actions...) {
			if e.Channel == f.alias {
				ahp.ApplyActionToSession(&mirror, e.Action)
			}
		}
		sameJSON(t, mirror, f.sessionState(), "the client's mirror must converge on the host's state")
		if mirror.Chats[0].Activity != nil || mirror.Activity != nil {
			t.Fatalf("activity should be cleared: %v %v", mirror.Chats[0].Activity, mirror.Activity)
		}
		f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatResponsePartAction{
			Type: ahptypes.ActionTypeChatResponsePart, TurnId: "turn",
			Part: ahptypes.ResponsePart{Value: &ahptypes.MarkdownResponsePart{Kind: ahptypes.ResponsePartKindMarkdown, Id: "text", Content: "a"}},
		}})
		f.client.Ping()
		f.clear()
		for i := 0; i < 20; i++ {
			f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatDeltaAction{Type: ahptypes.ActionTypeChatDelta, TurnId: "turn", PartId: "text", Content: "b"}})
		}
		f.client.Ping()
		envelopes := f.envelopes()
		if len(envelopes) != 20 {
			t.Fatalf("%d envelopes, want 20 (no delta projections)", len(envelopes))
		}
		for _, e := range envelopes {
			if _, ok := e.Action.Value.(*ahptypes.ChatDeltaAction); !ok || e.Channel != f.chat {
				t.Fatalf("unexpected envelope %s %T", e.Channel, e.Action.Value)
			}
		}
		if got := f.changes(); len(got) != 0 {
			t.Fatalf("root updates for deltas: %v", got)
		}
	})

	twin.Run(t, "session-summary", "projects complete and ignores rejected/no-op actions", func(t *testing.T) { projectsOutcome(t, "complete") })
	twin.Run(t, "session-summary", "projects cancel and ignores rejected/no-op actions", func(t *testing.T) { projectsOutcome(t, "cancel") })
	twin.Run(t, "session-summary", "projects error and ignores rejected/no-op actions", func(t *testing.T) { projectsOutcome(t, "error") })
	twin.Run(t, "session-summary", "projects truncate and ignores rejected/no-op actions", func(t *testing.T) { projectsOutcome(t, "truncate") })
	twin.Run(t, "session-summary", "replays running session projections to the same state as snapshot fallback", func(t *testing.T) { replaysProjection(t, true) })
	twin.Run(t, "session-summary", "replays completed session projections to the same state as snapshot fallback", func(t *testing.T) { replaysProjection(t, false) })
}

func projectsOutcome(t *testing.T, outcome string) {
	f := newSummaryFixture(t, nil)
	f.start()
	f.clear()
	before := testkit.Normalize(t, f.sessionState())
	f.client.Dispatch(f.chat, obj{"type": "chat/turnCancelled", "turnId": "wrong", "duration": 1000})
	f.client.Ping()
	f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{Type: ahptypes.ActionTypeChatTurnComplete, TurnId: "wrong", Duration: 1000}})
	f.client.Ping()
	envs := f.envelopes()
	if len(envs) != 2 || envs[0].RejectionReason == nil {
		t.Fatalf("envelopes = %d, first rejection %v", len(envs), envs[0].RejectionReason)
	}
	if got := f.changes(); len(got) != 0 {
		t.Fatalf("rejected/no-op actions must not change the catalogue: %v", got)
	}
	sameJSON(t, f.sessionState(), before, "session state")
	f.clear()
	switch outcome {
	case "cancel":
		f.client.Dispatch(f.chat, obj{"type": "chat/turnCancelled", "turnId": "turn", "duration": 1000})
	case "truncate":
		f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatTruncatedAction{Type: ahptypes.ActionTypeChatTruncated}})
	case "error":
		f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatErrorAction{
			Type: ahptypes.ActionTypeChatError, TurnId: "turn", Duration: 1000,
			Part: ahptypes.ErrorResponsePart{Kind: ahptypes.ResponsePartKindError, Error: ahptypes.ErrorInfo{ErrorType: "test", Message: "failed"}},
		}})
	default:
		f.h.host.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{Type: ahptypes.ActionTypeChatTurnComplete, TurnId: "turn", Duration: 1000}})
	}
	f.client.Ping()
	status := ahptypes.SessionStatusIdle
	if outcome == "error" {
		status = ahptypes.SessionStatusError
	}
	changes := obj{"status": float64(status)}
	if outcome != "truncate" {
		changes["modifiedAt"] = endAt
	}
	sameJSON(t, f.changes(), []obj{changeNote(f, changes)}, "root notifications")
	listed := f.list()
	wantModified := endAt
	if outcome == "truncate" {
		wantModified = startAt
	}
	if listed["status"] != float64(status) || listed["modifiedAt"] != wantModified {
		t.Fatalf("listed = %v", listed)
	}
	if got := f.sessionState().Chats[0].Status; got != status {
		t.Fatalf("chat summary status = %d", got)
	}
}

func replaysProjection(t *testing.T, running bool) {
	f := newSummaryFixture(t, nil)
	baseline := f.h.host.ServerSeq()
	f.client.Close()
	h := f.h.host
	h.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
		Type: ahptypes.ActionTypeChatTurnStarted, TurnId: "turn", StartedAt: startAt,
		Message: ahptypes.Message{Text: "offline", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
	}})
	working := "Working"
	h.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatActivityChangedAction{Type: ahptypes.ActionTypeChatActivityChanged, Activity: &working}})
	if !running {
		h.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatActivityChangedAction{Type: ahptypes.ActionTypeChatActivityChanged}})
		h.DispatchServerAction(f.chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{Type: ahptypes.ActionTypeChatTurnComplete, TurnId: "turn", Duration: 1000}})
	}
	resumed := testkit.Connect(t, h)
	t.Cleanup(resumed.Close)
	var replay struct {
		Type    string
		Actions []ahptypes.ActionEnvelope
	}
	resumed.Decode(resumed.Must("reconnect", obj{"channel": wire.RootChannel, "clientId": f.clientID, "lastSeenServerSeq": baseline, "subscriptions": []string{f.alias, f.chat}}), &replay)
	if replay.Type != "replay" {
		t.Fatalf("reconnect type = %q, want replay", replay.Type)
	}
	mirror := f.initial
	for _, e := range replay.Actions {
		if e.Channel == f.alias {
			ahp.ApplyActionToSession(&mirror, e.Action)
		}
	}
	fresh := testkit.Connect(t, h)
	t.Cleanup(fresh.Close)
	var fallback struct {
		Type      string
		Snapshots []struct{ State json.RawMessage }
	}
	fresh.Decode(fresh.Must("reconnect", obj{"channel": wire.RootChannel, "clientId": nextClientID(), "lastSeenServerSeq": baseline, "subscriptions": []string{f.alias}}), &fallback)
	if fallback.Type != "snapshot" || len(fallback.Snapshots) != 1 {
		t.Fatalf("fallback = %+v", fallback)
	}
	sameJSON(t, mirror, fallback.Snapshots[0].State, "replayed mirror vs snapshot fallback")
	if mirror.Status != ahptypes.SessionStatusIdle {
		t.Fatalf("mirror status = %d", mirror.Status)
	}
	wantModified := endAt
	if running {
		wantModified = startAt
	}
	if mirror.Chats[0].ModifiedAt != wantModified {
		t.Fatalf("chat modifiedAt = %s", mirror.Chats[0].ModifiedAt)
	}
	if running != (mirror.Chats[0].Activity != nil && *mirror.Chats[0].Activity == "Working") || (!running && mirror.Chats[0].Activity != nil) {
		t.Fatalf("chat activity = %v", mirror.Chats[0].Activity)
	}
	// Root notifications are not replayed: a fresh list supplies the final status.
	raw := fresh.Must("listSessions", obj{"channel": wire.RootChannel})
	var result struct{ Items []obj }
	_ = json.Unmarshal(raw, &result)
	wantStatus := ahptypes.SessionStatusIdle
	if running {
		wantStatus = ahptypes.SessionStatusInProgress
	}
	if result.Items[0]["status"] != float64(wantStatus) {
		t.Fatalf("listed status = %v", result.Items[0]["status"])
	}
}
