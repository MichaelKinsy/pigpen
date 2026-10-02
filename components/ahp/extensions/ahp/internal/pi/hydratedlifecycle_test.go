package pi_test

import (
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/hydrated-session-lifecycle.test.ts: operations performed after a durable
// Pi session has been hydrated.

func (f *hydratedFixture) listItems() []obj {
	f.t.Helper()
	var r struct{ Items []obj }
	f.client.Decode(f.client.Must("listSessions", obj{"channel": wire.RootChannel}), &r)
	return r.Items
}

func (f *hydratedFixture) turnStarted(turnID, text string) {
	f.client.Dispatch(f.chat(), obj{"type": "chat/turnStarted", "turnId": turnID, "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "message": userMessage(text)})
}

func TestReadStateAfterHydration(t *testing.T) {
	f := startHydrated(t, hydratedOptions{})
	isRead := float64(ahptypes.SessionStatusIsRead)

	twin.Run(t, "hydrated-session-lifecycle", "reports every catalogue entry as read", func(t *testing.T) {
		items := f.listItems()
		if int64(items[0]["status"].(float64))&int64(isRead) == 0 {
			t.Fatalf("status = %v", items[0]["status"])
		}
	})

	twin.Run(t, "hydrated-session-lifecycle", "reports a hydrated session as read", func(t *testing.T) {
		state := snapshotState[ahptypes.SessionState](t, f.client, f.session())
		if state.Status&ahptypes.SessionStatusIsRead == 0 {
			t.Fatalf("status = %d", state.Status)
		}
		if state.Title != "Read note.txt" {
			t.Fatalf("title = %q", state.Title)
		}
	})

	twin.Run(t, "hydrated-session-lifecycle", "keeps session-owned read state when a starting turn marks the chat unread", func(t *testing.T) {
		f.client.Subscribe(f.chat())
		if before := f.host.Store().Chat(f.chat()).Status; before&ahptypes.SessionStatusIsRead == 0 {
			t.Fatalf("chat status before = %d", before)
		}
		f.turnStarted("t-unread", "hi")
		f.client.Ping()
		if got := f.host.Store().Chat(f.chat()).Status & ahptypes.SessionStatusIsRead; got != 0 {
			t.Fatalf("chat still read: %d", got)
		}
		if f.host.Store().Session(f.session()).Status&ahptypes.SessionStatusIsRead == 0 {
			t.Fatal("the session lost its read bit")
		}
		for _, item := range f.listItems() {
			if item["resource"] == f.session() && int64(item["status"].(float64))&int64(isRead) == 0 {
				t.Fatalf("listed status = %v", item["status"])
			}
		}
	})
}

func TestDisposingAHydratedSession(t *testing.T) {
	twin.Run(t, "hydrated-session-lifecycle", "removes it from the catalogue and deletes the file", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		f.client.Subscribe(f.session())
		f.client.Must("disposeSession", obj{"channel": f.session()})
		if f.host.Store().Has(f.session()) {
			t.Fatal("the session channel remains")
		}
		if len(f.deletedFiles()) != 1 {
			t.Fatalf("deleted = %v", f.deletedFiles())
		}
		if got := f.listItems(); len(got) != 0 {
			t.Fatalf("a disposed session must not come back on the next listing: %v", got)
		}
	})

	twin.Run(t, "hydrated-session-lifecycle", "prevents an unloaded session from being recreated or hydrated during deletion", func(t *testing.T) {
		started := make(chan struct{})
		finish := make(chan bool)
		var once sync.Once
		f := startHydrated(t, hydratedOptions{deleteFile: func(path string) (pi.SessionFileDeletionResult, error) {
			once.Do(func() { close(started) })
			ok := <-finish
			if ok {
				_ = os.Remove(path)
			}
			return pi.SessionFileDeletionResult{OK: ok}, nil
		}})
		defer func() {
			select {
			case finish <- false:
			default:
			}
		}()
		uri := f.session()
		disposed := make(chan *testkit.RPCError, 1)
		go func() { _, err := f.client.Request("disposeSession", obj{"channel": uri}); disposed <- err }()
		select {
		case <-started:
		case <-time.After(testkit.Timeout):
			t.Fatal("timed out waiting for durable deletion to start")
		}
		f.client.ExpectError("subscribe", obj{"channel": uri}, wire.CodeNotFound)
		f.client.ExpectError("createSession", obj{"channel": uri}, wire.CodeSessionAlreadyExists)

		finish <- true
		if err := <-disposed; err != nil {
			t.Fatal(err)
		}
		deleted := f.deletedFiles()
		if len(deleted) != 1 {
			t.Fatalf("deleted = %v", deleted)
		}
		if _, err := os.Stat(deleted[0]); !os.IsNotExist(err) {
			t.Fatalf("the deleted session file remains: %v", err)
		}
		for _, item := range f.listItems() {
			if item["resource"] == uri {
				t.Fatal("the disposed session is still listed")
			}
		}
	})
}

func TestResumingAHydratedSession(t *testing.T) {
	twin.Run(t, "hydrated-session-lifecycle", "starts an agent on the first turn and prompts it", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		f.client.Subscribe(f.chat())
		if got := f.backend.promptList(); len(got) != 0 {
			t.Fatalf("browsing history must not start an agent: %v", got)
		}
		f.turnStarted("t-resume", "continue please")
		testkit.Eventually(t, "the resumed prompt to reach the backend", func() bool { return len(f.backend.promptList()) == 1 })
		if got := f.backend.promptList(); !reflect.DeepEqual(got, []string{"continue please"}) {
			t.Fatalf("prompts = %v", got)
		}
		testkit.Eventually(t, "the resumed turn to settle", func() bool { return f.host.Store().Chat(f.chat()).ActiveTurn == nil })
	})

	twin.Run(t, "hydrated-session-lifecycle", "resumes onto the existing transcript rather than a fresh one", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		f.client.Subscribe(f.chat())
		before := len(f.host.Store().Chat(f.chat()).Turns)
		f.turnStarted("t-append", "and again")
		testkit.Eventually(t, "the resumed prompt to reach the backend", func() bool { return len(f.backend.promptList()) == 1 })
		testkit.Eventually(t, "the resumed turn to append after history", func() bool { return len(f.host.Store().Chat(f.chat()).Turns) == before+1 })
		turns := f.host.Store().Chat(f.chat()).Turns
		if turns[0].Message.Text != "Read note.txt" || turns[len(turns)-1].Message.Text != "and again" {
			t.Fatalf("turns = %+v", turnShapes(turns))
		}
	})
}

func TestModelSelectionOnAHydratedSession(t *testing.T) {
	twin.Run(t, "hydrated-session-lifecycle", "seeds the picker from the model the session was using", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		chat := snapshotState[ahptypes.ChatState](t, f.client, f.chat())
		if chat.Draft == nil || chat.Draft.Model == nil || chat.Draft.Model.Id != "fixture/test-model" {
			t.Fatalf("draft = %+v", chat.Draft)
		}
	})
}

func TestRenamingAHydratedSession(t *testing.T) {
	twin.Run(t, "hydrated-session-lifecycle", "persists the name into the session file", func(t *testing.T) {
		f := startHydrated(t, hydratedOptions{})
		f.client.Subscribe(f.session())
		f.client.Dispatch(f.session(), obj{"type": "session/titleChanged", "title": "Archived work"})
		f.client.Ping()
		if got := f.host.Store().Session(f.session()).Title; got != "Archived work" {
			t.Fatalf("title = %q", got)
		}
		testkit.Eventually(t, "the name to reach the file", func() bool {
			file, _ := pi.NewCatalogue(f.root).FindSessionFile(f.sessionID)
			if file == "" {
				return false
			}
			m, err := pisession.Open(file)
			return err == nil && m.SessionName() == "Archived work"
		})
	})
}
