package svc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// Twins of upstream test/resource-watch.test.serial.ts: real filesystem, in-memory transport. The
// upstream lifetime cases are red in the oracle environment (mocked timers); here the grace and
// debounce timers run on a hand-driven clock, so they run for real.

type watchFixture struct {
	t         *testing.T
	host      *host.Host
	watches   *WatchService
	client    *testkit.Client
	workspace string
	journals  []*watchEvents
}

type fixtureOptions struct {
	grace    time.Duration
	restrict bool
	clock    Clock
	hooks    func(*WatchOptions)
}

func startWatchFixture(t *testing.T, o fixtureOptions) *watchFixture {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	if o.restrict {
		roots = []string{workspace}
	}
	policy, err := NewPathPolicy(roots...)
	if err != nil {
		t.Fatal(err)
	}
	h := testkit.NewHost(host.Options{})
	opts := WatchOptions{Paths: policy, Grace: o.grace, Debounce: 20 * time.Millisecond, PollInterval: 10 * time.Millisecond, Clock: o.clock}
	if o.hooks != nil {
		o.hooks(&opts)
	}
	watches := NewWatchService(h, opts)
	h.Serve(host.Capabilities{ResourceWatches: watches})
	client := testkit.Connect(t, h)
	client.Initialize("watch-client", nil)
	f := &watchFixture{t: t, host: h, watches: watches, client: client, workspace: workspace}
	t.Cleanup(f.close)
	return f
}

func (f *watchFixture) close() {
	for _, j := range f.journals {
		_ = j.Close()
	}
	<-f.watches.Dispose()
	f.client.Close()
}

func (f *watchFixture) path(parts ...string) string {
	return filepath.Join(append([]string{f.workspace}, parts...)...)
}

func (f *watchFixture) create(params map[string]any) string {
	f.t.Helper()
	params["channel"] = wire.RootChannel
	var result ahptypes.CreateResourceWatchResult
	f.client.Decode(f.client.Must("createResourceWatch", params), &result)
	return result.Channel
}

func (f *watchFixture) observe(channel string) *watchEvents {
	f.t.Helper()
	result := f.client.Subscribe(channel)
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), `"state"`) {
		f.t.Fatal("watch must still exist when subscription is accepted")
	}
	events := newWatchEvents(clientSource{client: f.client, channel: channel}, nil, func() any {
		return map[string]any{"channel": channel, "exists": f.host.Store().Has(channel), "activeCount": f.watches.ActiveCount()}
	})
	f.journals = append(f.journals, events)
	return events
}

func expectChange(t *testing.T, events *watchEvents, path string, kind ahptypes.ResourceChangeType, since int) ahptypes.ResourceChange {
	t.Helper()
	want := ahptypes.ResourceChange{Uri: wire.PathToFileURI(path), Type: kind}
	changes, err := events.WaitFor(string(kind)+": "+want.Uri, hasURI(want.Uri), since, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Uri == want.Uri {
			if c != want {
				t.Fatalf("got %+v, want %+v", c, want)
			}
			return c
		}
	}
	t.Fatal("unreachable")
	return want
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o666); err != nil {
		t.Fatal(err)
	}
}

func uriOf(path string) string { return wire.PathToFileURI(path) }

func (f *watchFixture) expectError(code int, params map[string]any) {
	f.t.Helper()
	params["channel"] = wire.RootChannel
	f.client.ExpectError("createResourceWatch", params, code)
}

func TestResourceWatch(t *testing.T) {
	twin.Run(t, "resource-watch", "returns a watch channel whose state describes what is watched", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		channel := f.create(map[string]any{"uri": uriOf(f.workspace), "recursive": true,
			"excludes": map[string]any{"items": []string{"**/.git/**"}}, "includes": map[string]any{"items": []string{"**/*.ts"}}})
		if !strings.HasPrefix(channel, "ahp-resource-watch:/") {
			t.Fatal(channel)
		}
		result := f.client.Subscribe(channel)
		raw, _ := json.Marshal(result)
		var snap struct {
			Snapshot struct{ State map[string]any } `json:"snapshot"`
		}
		_ = json.Unmarshal(raw, &snap)
		want := map[string]any{"root": uriOf(f.workspace), "recursive": true,
			"excludes": map[string]any{"items": []any{"**/.git/**"}}, "includes": map[string]any{"items": []any{"**/*.ts"}}}
		if !reflect.DeepEqual(snap.Snapshot.State, want) {
			t.Fatalf("state %v, want %v", snap.Snapshot.State, want)
		}
		testkit.AssertValid(t, "state", "ResourceWatchState", snap.Snapshot.State)
	})

	twin.Run(t, "resource-watch", "classifies newly created paths as added", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		events := f.observe(f.create(map[string]any{"uri": uriOf(f.workspace)}))
		target := f.path("created.txt")
		mustWrite(t, target, "hi")
		change := expectChange(t, events, target, ahptypes.ResourceChangeTypeAdded, 0)
		testkit.AssertValid(t, "state", "ResourceChange", map[string]any{"uri": change.Uri, "type": string(change.Type)})
	})

	twin.Run(t, "resource-watch", "classifies changes to existing paths as updated", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		target := f.path("existing.txt")
		mustWrite(t, target, "before")
		events := f.observe(f.create(map[string]any{"uri": uriOf(f.workspace)}))
		mustWrite(t, target, "after!")
		expectChange(t, events, target, ahptypes.ResourceChangeTypeUpdated, 0)
	})

	twin.Run(t, "resource-watch", "watches a single file at its actual URI", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		target := f.path("watched.txt")
		mustWrite(t, target, "before")
		events := f.observe(f.create(map[string]any{"uri": uriOf(target)}))
		mustWrite(t, f.path("sibling.txt"), "unrelated")
		mustWrite(t, target, "after!")
		expectChange(t, events, target, ahptypes.ResourceChangeTypeUpdated, 0)
		// Check delivered traffic; exhaustive sibling exclusion is a pure policy test.
		for _, c := range events.Changes() {
			if c.Uri != uriOf(target) {
				t.Fatalf("unexpected change %+v", c)
			}
		}
	})

	twin.Run(t, "resource-watch", "reports deletion and recreation of the watched file", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		target := f.path("recreated.txt")
		mustWrite(t, target, "before")
		events := f.observe(f.create(map[string]any{"uri": uriOf(target)}))
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		expectChange(t, events, target, ahptypes.ResourceChangeTypeDeleted, 0)
		since := events.Mark()
		mustWrite(t, target, "after")
		expectChange(t, events, target, ahptypes.ResourceChangeTypeAdded, since)
	})

	twin.Run(t, "resource-watch", "keeps a single-file watch attached across an atomic replacement", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		target, replacement := f.path("atomic.txt"), f.path(".atomic.txt.tmp")
		mustWrite(t, target, "before")
		events := f.observe(f.create(map[string]any{"uri": uriOf(target)}))
		mustWrite(t, replacement, "replacement")
		if err := os.Rename(replacement, target); err != nil {
			t.Fatal(err)
		}
		expectChange(t, events, target, ahptypes.ResourceChangeTypeUpdated, 0)
		time.Sleep(150 * time.Millisecond)
		since := events.Mark()
		mustWrite(t, target, "after")
		expectChange(t, events, target, ahptypes.ResourceChangeTypeUpdated, since)
	})

	twin.Run(t, "resource-watch", "keeps a directory watch attached across deletion and recreation", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		target := f.path("folder")
		if err := os.Mkdir(target, 0o777); err != nil {
			t.Fatal(err)
		}
		events := f.observe(f.create(map[string]any{"uri": uriOf(target)}))
		if err := os.RemoveAll(target); err != nil {
			t.Fatal(err)
		}
		expectChange(t, events, target, ahptypes.ResourceChangeTypeDeleted, 0)
		since := events.Mark()
		if err := os.Mkdir(target, 0o777); err != nil {
			t.Fatal(err)
		}
		expectChange(t, events, target, ahptypes.ResourceChangeTypeAdded, since)
		child := filepath.Join(target, "child.txt")
		mustWrite(t, child, "content")
		expectChange(t, events, child, ahptypes.ResourceChangeTypeAdded, since)
	})

	twin.Run(t, "resource-watch", "reports both sides of a rename without assuming batch boundaries", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		source, destination := f.path("before.txt"), f.path("after.txt")
		mustWrite(t, source, "content")
		events := f.observe(f.create(map[string]any{"uri": uriOf(f.workspace)}))
		if err := os.Rename(source, destination); err != nil {
			t.Fatal(err)
		}
		expectChange(t, events, source, ahptypes.ResourceChangeTypeDeleted, 0)
		expectChange(t, events, destination, ahptypes.ResourceChangeTypeAdded, 0)
	})

	twin.Run(t, "resource-watch", "does not lose paths from a burst", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		events := f.observe(f.create(map[string]any{"uri": uriOf(f.workspace)}))
		var paths []string
		for i := 0; i < 5; i++ {
			p := f.path("burst-" + string(rune('0'+i)) + ".txt")
			paths = append(paths, p)
			mustWrite(t, p, "x")
		}
		for _, p := range paths {
			expectChange(t, events, p, ahptypes.ResourceChangeTypeAdded, 0)
		}
	})

	twin.Run(t, "resource-watch", "reports direct children with recursive=false", func(t *testing.T) {
		watchDepth(t, false)
	})
	twin.Run(t, "resource-watch", "reports grandchildren with recursive=true", func(t *testing.T) {
		watchDepth(t, true)
	})

	twin.Run(t, "resource-watch", "wires includes into native watch delivery", func(t *testing.T) {
		watchFilter(t, true)
	})
	twin.Run(t, "resource-watch", "wires excludes into native watch delivery", func(t *testing.T) {
		watchFilter(t, false)
	})

	twin.Run(t, "resource-watch", "rejects watching something that does not exist", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		f.expectError(-32008, map[string]any{"uri": uriOf(f.path("missing"))})
		if f.watches.ActiveCount() != 0 {
			t.Fatal("a rejected watch stayed active")
		}
	})

	twin.Run(t, "resource-watch", "rejects a recursive watch on a file", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		target := f.path("file.txt")
		mustWrite(t, target, "x")
		f.expectError(-32602, map[string]any{"uri": uriOf(target), "recursive": true})
		if f.watches.ActiveCount() != 0 {
			t.Fatal("a rejected watch stayed active")
		}
	})

	twin.Run(t, "resource-watch", "rejects malformed filter parameters", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		f.expectError(-32602, map[string]any{"uri": uriOf(f.workspace), "includes": map[string]any{"items": []any{42}}})
		if f.watches.ActiveCount() != 0 {
			t.Fatal("a rejected watch stayed active")
		}
	})
}

func watchDepth(t *testing.T, recursive bool) {
	f := startWatchFixture(t, fixtureOptions{})
	nested := f.path("nested")
	if err := os.Mkdir(nested, 0o777); err != nil {
		t.Fatal(err)
	}
	events := f.observe(f.create(map[string]any{"uri": uriOf(f.workspace), "recursive": recursive}))
	direct, deep := f.path("direct.txt"), filepath.Join(nested, "deep.txt")
	mustWrite(t, deep, "deep")
	mustWrite(t, direct, "direct")
	if recursive {
		expectChange(t, events, deep, ahptypes.ResourceChangeTypeAdded, 0)
		return
	}
	expectChange(t, events, direct, ahptypes.ResourceChangeTypeAdded, 0)
	for _, c := range events.Changes() {
		if c.Uri == uriOf(deep) {
			t.Fatalf("a grandchild was reported by a non-recursive watch: %+v", c)
		}
	}
}

func watchFilter(t *testing.T, includes bool) {
	f := startWatchFixture(t, fixtureOptions{})
	params := map[string]any{"uri": uriOf(f.workspace), "recursive": true}
	if includes {
		params["includes"] = map[string]any{"items": []string{"**/*.md"}}
	} else {
		params["excludes"] = map[string]any{"items": []string{"**/node_modules/**"}}
	}
	events := f.observe(f.create(params))
	ignored := f.path("node_modules")
	if err := os.Mkdir(ignored, 0o777); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(ignored, "ignored.txt"), "x")
	target := f.path("kept.md")
	mustWrite(t, target, "x")
	expectChange(t, events, target, ahptypes.ResourceChangeTypeAdded, 0)
	for _, c := range events.Changes() {
		if includes && !strings.HasSuffix(c.Uri, ".md") {
			t.Fatalf("include filter leaked %+v", c)
		}
		if !includes && strings.Contains(c.Uri, "/node_modules/") {
			t.Fatalf("exclude filter leaked %+v", c)
		}
	}
}

func TestResourceWatchRoots(t *testing.T) {
	twin.Run(t, "resource-watch", "maps a permitted directory symlink back to the requested URI", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{restrict: true})
		target, link := f.path("target"), f.path("link")
		if err := os.Mkdir(target, 0o777); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(target, "watched.txt")
		mustWrite(t, child, "before")
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks unavailable: ", err)
		}
		events := f.observe(f.create(map[string]any{"uri": uriOf(link), "recursive": true}))
		mustWrite(t, child, "after!")
		expectChange(t, events, filepath.Join(link, "watched.txt"), ahptypes.ResourceChangeTypeUpdated, 0)
	})

	twin.Run(t, "resource-watch", "uses the same root and symlink policy as resource operations", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{restrict: true})
		outside, _ := filepath.EvalSymlinks(t.TempDir())
		channel := f.create(map[string]any{"uri": uriOf(f.workspace)})
		if !strings.HasPrefix(channel, "ahp-resource-watch:/") {
			t.Fatal(channel)
		}
		f.expectError(-32009, map[string]any{"uri": uriOf(outside)})
		link := f.path("escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Skip("symlinks unavailable: ", err)
		}
		f.expectError(-32009, map[string]any{"uri": uriOf(link)})
		if f.watches.ActiveCount() != 1 {
			t.Fatalf("active=%d", f.watches.ActiveCount())
		}
	})
}

// ── lifetime ────────────────────────────────────────────────────────────

func waitCount(t *testing.T, f *watchFixture, want int) {
	t.Helper()
	if got := f.watches.ActiveCount(); got != want {
		t.Fatalf("active watches = %d, want %d", got, want)
	}
}

func TestResourceWatchLifetime(t *testing.T) {
	const grace = 120 * time.Millisecond

	twin.Run(t, "resource-watch", "waits for the last subscriber before scheduling release", func(t *testing.T) {
		clock := &fakeClock{}
		f := startWatchFixture(t, fixtureOptions{grace: grace, clock: clock})
		other := testkit.Connect(t, f.host)
		defer other.Close()
		other.Initialize("other-watch-client", nil)
		channel := f.create(map[string]any{"uri": uriOf(f.workspace)})
		f.client.Subscribe(channel)
		other.Subscribe(channel)
		f.client.Unsubscribe(channel)
		f.client.Ping()
		clock.Tick(120 * time.Millisecond)
		waitCount(t, f, 1)
		other.Unsubscribe(channel)
		other.Ping()
		clock.Tick(119 * time.Millisecond)
		waitCount(t, f, 1)
		clock.Tick(1 * time.Millisecond)
		waitCount(t, f, 0)
		if f.host.Store().Has(channel) {
			t.Fatal("the released watch channel still exists")
		}
	})

	twin.Run(t, "resource-watch", "cancels grace on subscribe and grants a full window after the next unsubscribe", func(t *testing.T) {
		cancelGrace(t, "subscribe")
	})
	twin.Run(t, "resource-watch", "cancels grace on initialize and grants a full window after the next unsubscribe", func(t *testing.T) {
		cancelGrace(t, "initialize")
	})
	twin.Run(t, "resource-watch", "cancels grace on reconnect and grants a full window after the next unsubscribe", func(t *testing.T) {
		cancelGrace(t, "reconnect")
	})

	twin.Run(t, "resource-watch", "releases a watch when the client disconnects without unsubscribing", func(t *testing.T) {
		clock := &fakeClock{}
		f := startWatchFixture(t, fixtureOptions{grace: grace, clock: clock})
		channel := f.create(map[string]any{"uri": uriOf(f.workspace)})
		f.client.Subscribe(channel)
		disconnected := make(chan struct{})
		var once sync.Once
		unhook := f.host.OnSubscriberCountChanged(func(changed string, count int) {
			if changed == channel && count == 0 {
				once.Do(func() { close(disconnected) })
			}
		})
		defer unhook()
		f.client.Close()
		select {
		case <-disconnected:
		case <-time.After(4 * time.Second):
			t.Fatal("the disconnect never reached zero subscribers")
		}
		clock.Tick(119 * time.Millisecond)
		waitCount(t, f, 1)
		clock.Tick(1 * time.Millisecond)
		waitCount(t, f, 0)
		if f.host.Store().Has(channel) {
			t.Fatal("the released watch channel still exists")
		}
	})

	twin.Run(t, "resource-watch", "releases a watch nobody ever subscribed to", func(t *testing.T) {
		clock := &fakeClock{}
		f := startWatchFixture(t, fixtureOptions{grace: grace, clock: clock})
		channel := f.create(map[string]any{"uri": uriOf(f.workspace)})
		clock.Tick(119 * time.Millisecond)
		waitCount(t, f, 1)
		clock.Tick(1 * time.Millisecond)
		waitCount(t, f, 0)
		if f.host.Store().Has(channel) {
			t.Fatal("the released watch channel still exists")
		}
	})

	twin.Run(t, "resource-watch", "disposes every watch and returns the same shutdown promise", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		channel := f.create(map[string]any{"uri": uriOf(f.workspace)})
		f.client.Subscribe(channel)
		closing := f.watches.Dispose()
		if f.watches.Dispose() != closing {
			t.Fatal("dispose must return the same shutdown handle")
		}
		<-closing
		waitCount(t, f, 0)
		if f.host.Store().Has(channel) {
			t.Fatal("the released watch channel still exists")
		}
	})

	twin.Run(t, "resource-watch", "waits for a blocked creation to settle before completing shutdown", func(t *testing.T) {
		entered, gate := make(chan struct{}), make(chan struct{})
		var enterOnce sync.Once
		f := startWatchFixture(t, fixtureOptions{hooks: func(o *WatchOptions) {
			o.afterPath = func() {
				enterOnce.Do(func() { close(entered) })
				<-gate
			}
		}})
		created := make(chan error, 1)
		go func() {
			_, err := f.watches.Create(nil, ahptypes.CreateResourceWatchParams{Channel: wire.RootChannel, Uri: uriOf(f.workspace)})
			created <- err
		}()
		<-entered
		closing := f.watches.Dispose()
		select {
		case <-closing:
			t.Fatal("shutdown must drain the admitted create operation")
		case <-time.After(50 * time.Millisecond):
		}
		close(gate)
		<-closing
		if err := <-created; err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("create: %v", err)
		}
		waitCount(t, f, 0)
	})

	twin.Run(t, "resource-watch", "closes a native watcher if shutdown wins at its ready boundary", func(t *testing.T) {
		var mu sync.Mutex
		closed := 0
		var shutdown <-chan struct{}
		var svc *WatchService
		f := startWatchFixture(t, fixtureOptions{hooks: func(o *WatchOptions) {
			o.afterReady = func() {
				mu.Lock()
				defer mu.Unlock()
				shutdown = svc.Dispose()
			}
			o.onClosed = func() { mu.Lock(); closed++; mu.Unlock() }
		}})
		mu.Lock()
		svc = f.watches
		mu.Unlock()
		_, err := f.watches.Create(nil, ahptypes.CreateResourceWatchParams{Channel: wire.RootChannel, Uri: uriOf(f.workspace)})
		if err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("create: %v", err)
		}
		mu.Lock()
		s := shutdown
		mu.Unlock()
		if s == nil {
			t.Fatal("the watcher must have reached ready")
		}
		<-s
		mu.Lock()
		defer mu.Unlock()
		if closed != 1 {
			t.Fatalf("closed %d watchers, want 1", closed)
		}
		waitCount(t, f, 0)
	})

	twin.Run(t, "resource-watch", "drains in-flight creations and rejects new ones after shutdown", func(t *testing.T) {
		f := startWatchFixture(t, fixtureOptions{})
		params := ahptypes.CreateResourceWatchParams{Channel: wire.RootChannel, Uri: uriOf(f.workspace)}
		created := make(chan error, 1)
		go func() { _, err := f.watches.Create(nil, params); created <- err }()
		closing := f.watches.Dispose()
		if f.watches.Dispose() != closing {
			t.Fatal("dispose must return the same shutdown handle")
		}
		<-closing
		// The in-flight creation either won admission (and was drained and released) or lost it.
		<-created
		waitCount(t, f, 0)
		if _, err := f.watches.Create(nil, params); err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("create after shutdown: %v", err)
		}
	})
}

func cancelGrace(t *testing.T, method string) {
	const grace = 120 * time.Millisecond
	clock := &fakeClock{}
	f := startWatchFixture(t, fixtureOptions{grace: grace, clock: clock})
	channel := f.create(map[string]any{"uri": uriOf(f.workspace)})
	f.client.Subscribe(channel)
	var mu sync.Mutex
	var counts []int
	unhook := f.host.OnSubscriberCountChanged(func(changed string, count int) {
		if changed == channel {
			mu.Lock()
			counts = append(counts, count)
			mu.Unlock()
		}
	})
	defer unhook()
	f.client.Unsubscribe(channel)
	f.client.Ping()
	clock.Tick(60 * time.Millisecond)
	owner := f.client
	switch method {
	case "subscribe":
		owner.Subscribe(channel)
	default:
		owner = testkit.Connect(t, f.host)
		defer owner.Close()
		if method == "initialize" {
			owner.Initialize("replacement", map[string]any{"initialSubscriptions": []string{channel}})
		} else {
			owner.Must("reconnect", map[string]any{"channel": wire.RootChannel, "clientId": "replacement", "lastSeenServerSeq": f.host.ServerSeq(), "subscriptions": []string{channel}})
		}
	}
	snapshot := func() []int { mu.Lock(); defer mu.Unlock(); return append([]int(nil), counts...) }
	if got := snapshot(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("counts %v, want [0 1]", got)
	}
	clock.Tick(120 * time.Millisecond)
	waitCount(t, f, 1)
	owner.Unsubscribe(channel)
	owner.Ping()
	clock.Tick(119 * time.Millisecond)
	waitCount(t, f, 1)
	clock.Tick(1 * time.Millisecond)
	waitCount(t, f, 0)
	if got := snapshot(); !reflect.DeepEqual(got, []int{0, 1, 0}) {
		t.Fatalf("counts %v, want [0 1 0]", got)
	}
}
