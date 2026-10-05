package pi_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/session-hydration.test.ts: opening a session that only exists on disk.
// The catalogue is backed by Pi's session files, most of which no live host has ever touched;
// without lazy loading every session the catalogue advertises would answer NotFound on subscribe.

func snapshotState[T any](t *testing.T, client *testkit.Client, channel string) T {
	t.Helper()
	result := client.Subscribe(channel)
	if result.Snapshot == nil {
		t.Fatalf("no snapshot for %s", channel)
	}
	raw, _ := json.Marshal(result.Snapshot.State)
	var state T
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestOpeningASessionFromTheCatalogue(t *testing.T) {
	f := startHydrated(t, hydratedOptions{})

	twin.Run(t, "session-hydration", "hydrates a catalogued session and counts it as active", func(t *testing.T) {
		state := snapshotState[ahptypes.SessionState](t, f.client, f.session())
		if state.Lifecycle != ahptypes.SessionLifecycleReady || len(state.Chats) != 1 {
			t.Fatalf("state = %+v", state)
		}
		if state.DefaultChat == nil || *state.DefaultChat != f.chat() {
			t.Fatalf("defaultChat = %v", state.DefaultChat)
		}
		if !reflect.DeepEqual(state.WorkingDirectories, []ahptypes.URI{wire.PathToFileURI(f.workspace)}) {
			t.Fatalf("workingDirectories = %v", state.WorkingDirectories)
		}
		testkit.AssertValid(t, "state", "SessionState", state)
		if got := f.host.Store().Root(wire.RootChannel).ActiveSessions; got == nil || *got != 1 {
			t.Fatalf("activeSessions = %v", got)
		}
	})

	twin.Run(t, "session-hydration", "rebuilds the transcript onto the chat channel", func(t *testing.T) {
		chat := snapshotState[ahptypes.ChatState](t, f.client, f.chat())
		if len(chat.Turns) != 2 || chat.Turns[0].Message.Text != "Read note.txt" || chat.Turns[0].State != ahptypes.TurnStateComplete {
			t.Fatalf("turns = %+v", turnShapes(chat.Turns))
		}
		testkit.AssertValid(t, "state", "ChatState", chat)
	})

	twin.Run(t, "session-hydration", "pairs each tool call with the result that followed it", func(t *testing.T) {
		chat := snapshotState[ahptypes.ChatState](t, f.client, f.chat())
		parts := chat.Turns[0].ResponseParts
		var kinds []ahptypes.ResponsePartKind
		for _, p := range parts {
			raw, _ := json.Marshal(p)
			var k struct{ Kind ahptypes.ResponsePartKind }
			_ = json.Unmarshal(raw, &k)
			kinds = append(kinds, k.Kind)
		}
		want := []ahptypes.ResponsePartKind{ahptypes.ResponsePartKindReasoning, ahptypes.ResponsePartKindToolCall, ahptypes.ResponsePartKindMarkdown}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("kinds = %v", kinds)
		}
		call := parts[1].Value.(*ahptypes.ToolCallResponsePart).ToolCall.Value.(*ahptypes.ToolCallCompletedState)
		if call.Status != ahptypes.ToolCallStatusCompleted || call.ToolName != "read" {
			t.Fatalf("toolCall = %+v", call)
		}
		sameJSON(t, call.Content, []any{obj{"type": "text", "text": "ALPHA"}}, "tool content")
	})

	twin.Run(t, "session-hydration", "still answers NotFound for a session that really does not exist", func(t *testing.T) {
		f.client.ExpectError("subscribe", obj{"channel": wire.SessionURI(newID())}, -32008)
	})

	twin.Run(t, "session-hydration", "restores images stored in pi user messages", func(t *testing.T) {
		fresh := startHydrated(t, hydratedOptions{includeImage: true})
		chat := snapshotState[ahptypes.ChatState](t, fresh.client, fresh.chat())
		sameJSON(t, chat.Turns[0].Message.Attachments, []any{obj{
			"type": "embeddedResource", "label": "Image 1", "displayKind": "image", "data": onePixelPNG, "contentType": "image/png",
		}}, "attachments")
		testkit.AssertValid(t, "state", "ChatState", chat)
	})

	twin.Run(t, "session-hydration", "hydrates from either half of the pair", func(t *testing.T) {
		fresh := startHydrated(t, hydratedOptions{})
		if r := fresh.client.Subscribe(fresh.chat()); r.Snapshot == nil {
			t.Fatal("no snapshot")
		}
		if !fresh.host.Store().Has(fresh.session()) {
			t.Fatal("the session must load alongside its chat")
		}
	})

	twin.Run(t, "session-hydration", "coalesces concurrent session and chat hydration", func(t *testing.T) {
		source := startHydrated(t, hydratedOptions{})
		catalogue := &blockingCatalogue{inner: pi.NewCatalogue(source.root), started: make(chan struct{}), gate: make(chan struct{})}
		h := testkit.NewHost(host.Options{})
		var mu sync.Mutex
		live := map[string]bool{}
		var adopted []string
		hydrator := pi.NewHydrator(pi.HydratorOptions{
			Host: h, Catalogue: catalogue,
			IsLive:      func(s string) bool { mu.Lock(); defer mu.Unlock(); return live[s] },
			IsDisposing: func(string) bool { return false },
			Adopt: func(a pi.AdoptedSession) {
				mu.Lock()
				live[a.URI] = true
				adopted = append(adopted, a.URI)
				mu.Unlock()
			},
		})
		session, chat := wire.SessionURI(source.sessionID), wire.ChatURI(source.sessionID)
		results := make(chan bool, 2)
		go func() { ok, _ := hydrator.Hydrate(context.Background(), session); results <- ok }()
		<-catalogue.started
		go func() { ok, _ := hydrator.Hydrate(context.Background(), chat); results <- ok }()
		time.Sleep(20 * time.Millisecond) // let the second caller reach the coalescing point
		close(catalogue.gate)
		if a, b := <-results, <-results; !a || !b {
			t.Fatalf("hydrate results = %v %v", a, b)
		}
		if catalogue.lookupCount() != 1 {
			t.Fatalf("lookups = %d", catalogue.lookupCount())
		}
		if !reflect.DeepEqual(adopted, []string{session}) || !h.Store().Has(session) || !h.Store().Has(chat) {
			t.Fatalf("adopted %v, session %v chat %v", adopted, h.Store().Has(session), h.Store().Has(chat))
		}
	})

	twin.Run(t, "session-hydration", "preserves catalogue modifiedAt when a disk session becomes a live overlay", func(t *testing.T) {
		fixture := startHydrated(t, hydratedOptions{})
		file, err := pi.NewCatalogue(fixture.root).FindSessionFile(fixture.sessionID)
		if err != nil || file == "" {
			t.Fatalf("file %q err %v", file, err)
		}
		stamp := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		if err := os.Chtimes(file, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		iso := "2025-06-01T12:00:00.000Z"
		listItems := func() []obj {
			var r struct{ Items []obj }
			fixture.client.Decode(fixture.client.Must("listSessions", obj{"channel": wire.RootChannel}), &r)
			return r.Items
		}
		if got := listItems(); len(got) != 1 || got[0]["modifiedAt"] != iso {
			t.Fatalf("before = %v", got)
		}
		chat := snapshotState[ahptypes.ChatState](t, fixture.client, fixture.chat())
		if chat.ModifiedAt != iso {
			t.Fatalf("chat modifiedAt = %s", chat.ModifiedAt)
		}
		if got := listItems(); len(got) != 1 || got[0]["modifiedAt"] != iso {
			t.Fatalf("after = %v", got)
		}
	})
}

// blockingCatalogue holds the first lookup until the test releases it.
type blockingCatalogue struct {
	inner   *pi.Catalogue
	started chan struct{}
	gate    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	lookups int
}

func (b *blockingCatalogue) FindSessionFile(id string) (string, error) {
	b.mu.Lock()
	b.lookups++
	b.mu.Unlock()
	b.once.Do(func() { close(b.started) })
	<-b.gate
	return b.inner.FindSessionFile(id)
}

func (b *blockingCatalogue) lookupCount() int { b.mu.Lock(); defer b.mu.Unlock(); return b.lookups }

// snapshotStateOf decodes the state of an already-received subscribe result.
func snapshotStateOf[T any](t *testing.T, result ahptypes.SubscribeResult) T {
	t.Helper()
	if result.Snapshot == nil {
		t.Fatal("no snapshot")
	}
	raw, _ := json.Marshal(result.Snapshot.State)
	var state T
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
