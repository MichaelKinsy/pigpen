package pi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/fetch-turns.test.ts and test/truncate.test.ts.

// sessionFileFixture is a host serving one pre-written session file.
type sessionFileFixture struct {
	t         *testing.T
	host      *host.Host
	client    *testkit.Client
	sessionID string
	root      string
}

func (f *sessionFileFixture) chat() string    { return wire.ChatURI(f.sessionID) }
func (f *sessionFileFixture) session() string { return wire.SessionURI(f.sessionID) }

func startSessionFile(t *testing.T, write func(root, id, cwd string), backend pi.Backend) *sessionFileFixture {
	t.Helper()
	f := &sessionFileFixture{t: t, root: t.TempDir(), sessionID: newID()}
	workspace := t.TempDir()
	write(f.root, f.sessionID, workspace)
	opts := pi.ServicesOptions{
		SessionRoot: f.root, DefaultWorkingDirectory: workspace,
		CreateSessionManager: pi.PersistentStorage(filepath.Join(f.root, "created")),
	}
	f.host = testkit.NewHost(host.Options{})
	opts.Host = f.host
	if backend != nil {
		opts.CreateBackend = func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }
	}
	services := pi.NewServices(opts)
	f.host.Serve(services.Capabilities())
	f.client = testkit.Connect(t, f.host)
	t.Cleanup(f.client.Close)
	f.client.Initialize("paging-client", nil)
	return f
}

// jsonl builds a session file body: header + entries linked by id/parentId.
type jsonl struct {
	lines  []string
	parent any
	n      int
}

func newJSONL(id, cwd string) *jsonl {
	raw, _ := json.Marshal(obj{"type": "session", "id": id, "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z", "version": 3, "cwd": cwd})
	return &jsonl{lines: []string{string(raw)}}
}

func (j *jsonl) push(entry obj) string {
	j.n++
	id := "n" + strconv.Itoa(j.n)
	entry["id"], entry["parentId"], entry["timestamp"] = id, j.parent, "2026-01-01T00:00:00.000Z"
	raw, _ := json.Marshal(entry)
	j.lines = append(j.lines, string(raw))
	j.parent = id
	return id
}

func (j *jsonl) message(role string, content any) string {
	return j.push(obj{"type": "message", "message": obj{"role": role, "content": content, "timestamp": 0}})
}

func (j *jsonl) write(t testing.TB, root, id, cwd string) {
	dir := fixtureSessionDirectory(t, root, cwd)
	if err := os.WriteFile(filepath.Join(dir, "2026-01-01T00-00-00-000Z_"+id+".jsonl"), []byte(strings.Join(j.lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeCompactedSession writes a session with `before` exchanges, then a compaction, then `after`.
// The compaction is what makes the earlier turns invisible to the default window, which is exactly
// the case paging exists for.
func writeCompactedSession(before, after int) func(root, id, cwd string) {
	return func(root, id, cwd string) {
		j := newJSONL(id, cwd)
		exchange := func(n int) {
			j.message("user", fmt.Sprintf("question %d", n))
			j.message("assistant", []any{obj{"type": "text", "text": fmt.Sprintf("answer %d", n)}})
		}
		for i := 0; i < before; i++ {
			exchange(i)
		}
		// The compaction keeps nothing before itself: firstKeptEntryId points at the entry that
		// follows it, so the window starts here.
		j.push(obj{"type": "compaction", "summary": "earlier work", "firstKeptEntryId": "none", "tokensBefore": 1000})
		for i := 0; i < after; i++ {
			exchange(before + i)
		}
		j.write(t0{}, root, id, cwd)
	}
}

// t0 lets the writers above (called without a *testing.T) fail loudly.
type t0 struct{ testing.TB }

func (t0) Helper()                           {}
func (t0) Fatal(args ...any)                 { panic(fmt.Sprint(args...)) }
func (t0) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

func TestFetchTurns(t *testing.T) {
	// 25 pre-compaction exchanges, so a 20-turn page leaves a second one.
	f := startSessionFile(t, writeCompactedSession(25, 2), nil)
	f.client.Subscribe(f.chat())
	state := func() *ahptypes.ChatState { return f.host.Store().Chat(f.chat()) }

	twin.Run(t, "fetch-turns", "pages backward while preserving a complete oldest-first transcript", func(t *testing.T) {
		initial := state()
		// Its presence is the protocol's signal that turns is a tail window.
		if initial.TurnsNextCursor == nil {
			t.Fatal("a compacted session must offer more history")
		}
		testkit.AssertValid(t, "state", "ChatState", initial)
		newest := initial.Turns[len(initial.Turns)-1].Id
		initialCount := len(initial.Turns)

		f.client.Must("fetchTurns", obj{"channel": f.chat(), "cursor": *initial.TurnsNextCursor})
		current := state()
		if len(current.Turns) <= initialCount {
			t.Fatalf("no older turns arrived: %d", len(current.Turns))
		}
		if got := current.Turns[len(current.Turns)-1].Id; got != newest {
			t.Fatalf("paging must preserve the visible tail: %s vs %s", got, newest)
		}
		if !regexp.MustCompile(`question \d+`).MatchString(current.Turns[0].Message.Text) {
			t.Fatalf("oldest turn = %q", current.Turns[0].Message.Text)
		}
		for current.TurnsNextCursor != nil {
			f.client.Must("fetchTurns", obj{"channel": f.chat(), "cursor": *current.TurnsNextCursor})
			current = state()
		}
		var numbered []int
		re := regexp.MustCompile(`question (\d+)`)
		for _, turn := range current.Turns {
			if m := re.FindStringSubmatch(turn.Message.Text); m != nil {
				n, _ := strconv.Atoi(m[1])
				numbered = append(numbered, n)
			}
		}
		if !sort.IntsAreSorted(numbered) || len(numbered) != 27 {
			t.Fatalf("questions = %v", numbered)
		}
		// Without this the client would keep asking for pages that do not exist.
		if current.TurnsNextCursor != nil {
			t.Fatal("the cursor must clear once history is exhausted")
		}
	})

	twin.Run(t, "fetch-turns", "rejects a session channel for this chat-scoped command", func(t *testing.T) {
		f.client.ExpectError("fetchTurns", obj{"channel": f.session()}, wire.CodeInvalidParams)
	})

	twin.Run(t, "fetch-turns", "rejects a cursor it did not issue", func(t *testing.T) {
		f.client.ExpectError("fetchTurns", obj{"channel": f.chat(), "cursor": "made-up"}, -32602)
	})

	twin.Run(t, "fetch-turns", "offers no cursor for a session that was never compacted", func(t *testing.T) {
		fresh := startSessionFile(t, writeCompactedSession(0, 3), nil)
		chat := snapshotState[ahptypes.ChatState](t, fresh.client, fresh.chat())
		if chat.TurnsNextCursor != nil {
			t.Fatalf("turnsNextCursor = %v", *chat.TurnsNextCursor)
		}
	})
}

// truncatingBackend records the entries it was asked to move back to.
type truncatingBackend struct {
	mu        sync.Mutex
	truncated []string
	accept    bool
}

func (b *truncatingBackend) Subscribe(func(mapper.Event)) func()                  { return func() {} }
func (b *truncatingBackend) Prompt(context.Context, string, []mapper.Image) error { return nil }
func (b *truncatingBackend) Steer(context.Context, string, []mapper.Image) error  { return nil }
func (b *truncatingBackend) Abort(context.Context) error                          { return nil }
func (b *truncatingBackend) Truncate(_ context.Context, entryID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.truncated = append(b.truncated, entryID)
	return b.accept, nil
}
func (b *truncatingBackend) list() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.truncated...)
}

// writeTwoTurns is upstream truncate.test.ts's writeSession: two complete turns.
func writeTwoTurns(root, id, cwd string) {
	j := newJSONL(id, cwd)
	j.message("user", "first question")
	j.message("assistant", []any{obj{"type": "text", "text": "first answer"}})
	j.message("user", "second question")
	j.message("assistant", []any{obj{"type": "text", "text": "second answer"}})
	j.write(t0{}, root, id, cwd)
}

func TestTruncationAnchors(t *testing.T) {
	entry := func(id, role, text string) pisession.Entry {
		return pisession.Entry{"type": "message", "id": id, "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z", "message": obj{"role": role, "content": text}}
	}
	twin.Run(t, "truncate", "anchors a turn on its last entry, not its first", func(t *testing.T) {
		// navigateTree is inclusive for a non-user entry and exclusive for a user one, so pointing
		// at the turn's *last* entry is what expresses "keep turns up to and including this one".
		history := pi.RebuildHistory([]pisession.Entry{entry("u1", "user", "q1"), entry("a1", "assistant", "a1"), entry("u2", "user", "q2"), entry("a2", "assistant", "a2")}, pi.RebuildOptions{TurnIDPrefix: "s"})
		if len(history.Turns) != 2 {
			t.Fatalf("%d turns", len(history.Turns))
		}
		if history.Anchors[history.Turns[0].Id] != "a1" || history.Anchors[history.Turns[1].Id] != "a2" {
			t.Fatalf("anchors = %v", history.Anchors)
		}
	})

	twin.Run(t, "truncate", "anchors 'clear everything' on the first user entry", func(t *testing.T) {
		// Navigating to a user entry lands the leaf on its parent, and the first one's parent is
		// null, which is how Pi expresses an empty branch.
		u1 := entry("u1", "user", "q")
		a1 := entry("a1", "assistant", "a")
		a1["parentId"] = "u1"
		history := pi.RebuildHistory([]pisession.Entry{u1, a1}, pi.RebuildOptions{TurnIDPrefix: "s"})
		if history.Anchors[pi.ClearAllAnchor] != "u1" {
			t.Fatalf("anchors = %v", history.Anchors)
		}
	})
}

func TestChatTruncated(t *testing.T) {
	twin.Run(t, "truncate", "moves pi's leaf to the named turn, not just the client's view", func(t *testing.T) {
		backend := &truncatingBackend{accept: true}
		f := startSessionFile(t, writeTwoTurns, backend)
		chat := snapshotState[ahptypes.ChatState](t, f.client, f.chat())
		if len(chat.Turns) != 2 {
			t.Fatalf("%d turns", len(chat.Turns))
		}
		f.client.Dispatch(f.chat(), obj{"type": "chat/truncated", "turnId": chat.Turns[0].Id})
		testkit.Eventually(t, "pi's history to receive the truncation", func() bool { return len(backend.list()) == 1 })
		// Both halves have to move: the reducer drops the later turn, and Pi is told to branch from
		// the kept one.
		if got := len(f.host.Store().Chat(f.chat()).Turns); got != 1 {
			t.Fatalf("%d turns remain", got)
		}
		// The entry Pi is told to move to is the kept turn's last entry (the first answer).
		file, _ := pi.NewCatalogue(f.root).FindSessionFile(f.sessionID)
		m, _ := pisession.Open(file)
		var assistantIDs []string
		for _, e := range m.Entries() {
			if e.Role() == "assistant" {
				assistantIDs = append(assistantIDs, e.ID())
			}
		}
		if !reflect.DeepEqual(backend.list(), assistantIDs[:1]) {
			t.Fatalf("truncated to %v, want %v", backend.list(), assistantIDs[:1])
		}
	})

	twin.Run(t, "truncate", "maps 'clear everything' onto the first entry", func(t *testing.T) {
		backend := &truncatingBackend{accept: true}
		f := startSessionFile(t, writeTwoTurns, backend)
		f.client.Subscribe(f.chat())
		f.client.Dispatch(f.chat(), obj{"type": "chat/truncated"})
		testkit.Eventually(t, "pi's history to receive the clear-all truncation", func() bool { return len(backend.list()) == 1 })
		if got := len(f.host.Store().Chat(f.chat()).Turns); got != 0 {
			t.Fatalf("%d turns remain", got)
		}
		file, _ := pi.NewCatalogue(f.root).FindSessionFile(f.sessionID)
		m, _ := pisession.Open(file)
		if first := m.Entries()[0].ID(); !reflect.DeepEqual(backend.list(), []string{first}) {
			t.Fatalf("truncated to %v, want the first entry %s", backend.list(), first)
		}
	})

	twin.Run(t, "truncate", "refuses a turn it cannot anchor, instead of diverging", func(t *testing.T) {
		backend := &truncatingBackend{accept: true}
		f := startSessionFile(t, writeTwoTurns, backend)
		before := len(snapshotState[ahptypes.ChatState](t, f.client, f.chat()).Turns)
		mark := len(f.client.Notifications())
		f.client.Dispatch(f.chat(), obj{"type": "chat/truncated", "turnId": "no-such-turn"})
		// Refused before the reducer runs: accepting would truncate the client's view while Pi
		// kept everything.
		n, ok := f.client.Await(func(n testkit.Notification) bool { _, isAction := n.Envelope(); return isAction }, testkit.Timeout)
		_ = mark
		if !ok {
			t.Fatal("no action envelope")
		}
		env, _ := n.Envelope()
		if env.RejectionReason == nil || !strings.Contains(*env.RejectionReason, "Cannot truncate") {
			t.Fatalf("rejectionReason = %v", env.RejectionReason)
		}
		if got := len(f.host.Store().Chat(f.chat()).Turns); got != before {
			t.Fatalf("%d turns, want %d", got, before)
		}
		if got := backend.list(); len(got) != 0 {
			t.Fatalf("truncated = %v", got)
		}
	})
}
