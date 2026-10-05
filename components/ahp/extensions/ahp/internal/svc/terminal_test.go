package svc

import (
	"encoding/json"
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

// Twins of upstream test/terminal-service.test.ts: the service against a fake PTY, so nothing here
// depends on a shell.

const expectedScrollbackChars = 1_000_000

type fakePty struct {
	mu       sync.Mutex
	writes   []string
	resizes  [][2]int
	kills    int
	handlers PtyHandlers
}

func (p *fakePty) Write(data string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes = append(p.writes, data)
	return nil
}
func (p *fakePty) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]int{cols, rows})
	return nil
}
func (p *fakePty) Kill() error { p.mu.Lock(); defer p.mu.Unlock(); p.kills++; return nil }
func (p *fakePty) Writes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.writes...)
}
func (p *fakePty) Resizes() [][2]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][2]int(nil), p.resizes...)
}
func (p *fakePty) Kills() int           { p.mu.Lock(); defer p.mu.Unlock(); return p.kills }
func (p *fakePty) emitData(data string) { p.handlers.OnData(data) }
func (p *fakePty) emitExit(code int)    { p.handlers.OnExit(code) }

type spawnCall struct {
	file    string
	args    []string
	options PtyOptions
	pty     *fakePty
}

type fakeSpawner struct {
	mu    sync.Mutex
	calls []spawnCall
}

func (f *fakeSpawner) spawn(file string, args []string, opts PtyOptions, h PtyHandlers) (PtyProcess, error) {
	pty := &fakePty{handlers: h}
	f.mu.Lock()
	f.calls = append(f.calls, spawnCall{file, args, opts, pty})
	f.mu.Unlock()
	return pty, nil
}

func (f *fakeSpawner) Calls() []spawnCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]spawnCall(nil), f.calls...)
}

type terminalFixture struct {
	t         *testing.T
	directory string
	host      *host.Host
	spawner   *fakeSpawner
	terminals *TerminalService
}

const ownerID = "terminal-owner"

func startTerminalFixture(t *testing.T) (*terminalFixture, *testkit.Client) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	h := testkit.NewHost(host.Options{ReplayBufferCapacity: 4})
	spawner := &fakeSpawner{}
	terminals := NewTerminalService(h, TerminalOptions{DefaultWorkingDirectory: dir, Shell: "test-shell", Spawn: spawner.spawn})
	h.Serve(host.Capabilities{Terminals: terminals})
	f := &terminalFixture{t: t, directory: dir, host: h, spawner: spawner, terminals: terminals}
	t.Cleanup(terminals.Shutdown)
	owner := f.connect(ownerID)
	return f, owner
}

func (f *terminalFixture) connect(clientID string) *testkit.Client {
	c := testkit.Connect(f.t, f.host)
	f.t.Cleanup(c.Close)
	c.Initialize(clientID, nil)
	return c
}

func (f *terminalFixture) state(channel string) *ahptypes.TerminalState {
	f.t.Helper()
	s := f.host.Store().Terminal(channel)
	if s == nil {
		f.t.Fatalf("missing terminal state for %s", channel)
	}
	return s
}

func (f *terminalFixture) root() *ahptypes.RootState { return f.host.Store().Root(wire.RootChannel) }

func clientClaim(id string) map[string]any { return map[string]any{"kind": "client", "clientId": id} }

var terminalCounter int

func newTerminalChannel() string {
	terminalCounter++
	return "ahp-terminal:/" + newUUID()
}

func (f *terminalFixture) createTerminal(owner *testkit.Client, params map[string]any) (string, *fakePty) {
	f.t.Helper()
	channel, _ := params["channel"].(string)
	if channel == "" {
		channel = "agenthost-terminal:/" + newUUID()
	}
	req := map[string]any{"channel": channel, "claim": clientClaim(ownerID)}
	for k, v := range params {
		req[k] = v
	}
	owner.Must("createTerminal", req)
	calls := f.spawner.Calls()
	if len(calls) == 0 {
		f.t.Fatal("no PTY was spawned")
	}
	return channel, calls[len(calls)-1].pty
}

func contentValues(s *ahptypes.TerminalState) []string {
	var out []string
	for _, part := range s.Content {
		if u, ok := part.Value.(*ahptypes.TerminalUnclassifiedPart); ok {
			out = append(out, u.Value)
		} else {
			out = append(out, "?")
		}
	}
	return out
}

func lifecycleJSON(s *ahptypes.TerminalState) string {
	raw, _ := json.Marshal(s.Lifecycle)
	return string(raw)
}

func TestTerminalService(t *testing.T) {
	twin.Run(t, "terminal-service", "creates a client-owned terminal and publishes its authoritative state", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		cwd := wire.PathToFileURI(f.directory)
		channel, _ := f.createTerminal(owner, map[string]any{"name": "Build", "cwd": cwd, "cols": 100, "rows": 30})
		calls := f.spawner.Calls()
		call := calls[0]
		if call.file != "test-shell" || len(call.args) != 0 || call.options.Name != "xterm-256color" || call.options.Cwd != f.directory || call.options.Cols != 100 || call.options.Rows != 30 {
			t.Fatalf("spawn call %+v", call)
		}
		state := f.state(channel)
		raw, _ := json.Marshal(state)
		var got any
		_ = json.Unmarshal(raw, &got)
		want := map[string]any{"title": "Build", "cwd": cwd, "cols": float64(100), "rows": float64(30), "content": []any{},
			"lifecycle": map[string]any{"status": "running"}, "claim": clientClaim(ownerID), "isPty": true}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("state\n got  %v\n want %v", got, want)
		}
		testkit.AssertValid(t, "state", "TerminalState", got)
		rootRaw, _ := json.Marshal(f.root().Terminals)
		wantRoot := `[{"resource":"` + channel + `","title":"Build","claim":{"kind":"client","clientId":"terminal-owner"},"lifecycle":{"status":"running"}}]`
		if string(rootRaw) != wantRoot {
			t.Fatalf("root terminals %s\nwant %s", rootRaw, wantRoot)
		}
		var rootGeneric any
		rr, _ := json.Marshal(f.root())
		_ = json.Unmarshal(rr, &rootGeneric)
		testkit.AssertValid(t, "state", "RootState", rootGeneric)

		sr := owner.Must("subscribe", map[string]any{"channel": channel})
		var snapWrapper struct {
			Snapshot struct {
				Resource string
				State    any
			}
		}
		_ = json.Unmarshal(sr, &snapWrapper)
		snapGeneric := snapWrapper.Snapshot
		if snapGeneric.Resource != channel || !reflect.DeepEqual(snapGeneric.State, want) {
			t.Fatalf("snapshot %s", sr)
		}
	})

	twin.Run(t, "terminal-service", "forwards output, input, resize, title, and clear without interpreting VT data", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		subscribeRaw(owner, channel)
		if f.state(channel).Title != "test-shell" {
			t.Fatalf("title %q", f.state(channel).Title)
		}
		pty.emitData("hello\x1b[31m red")
		testkit.Eventually(t, "terminal output to be published", func() bool { return len(f.state(channel).Content) > 0 })
		if got := contentValues(f.state(channel)); !reflect.DeepEqual(got, []string{"hello\x1b[31m red"}) {
			t.Fatalf("%q", got)
		}

		owner.Dispatch(channel, map[string]any{"type": "terminal/input", "data": "echo hi\r"})
		testkit.Eventually(t, "terminal input to reach the PTY", func() bool { return len(pty.Writes()) == 1 })
		if got := pty.Writes(); !reflect.DeepEqual(got, []string{"echo hi\r"}) {
			t.Fatalf("%q", got)
		}

		owner.Dispatch(channel, map[string]any{"type": "terminal/resized", "cols": 120, "rows": 40})
		testkit.Eventually(t, "terminal resize to reach the PTY", func() bool { return len(pty.Resizes()) == 1 })
		if got := pty.Resizes(); !reflect.DeepEqual(got, [][2]int{{120, 40}}) {
			t.Fatalf("%v", got)
		}
		if s := f.state(channel); *s.Cols != 120 || *s.Rows != 40 {
			t.Fatalf("state size %d x %d", *s.Cols, *s.Rows)
		}

		owner.Dispatch(channel, map[string]any{"type": "terminal/titleChanged", "title": "Tests"})
		testkit.Eventually(t, "the terminal title to reach root state", func() bool {
			r := f.root()
			return len(r.Terminals) > 0 && r.Terminals[0].Title == "Tests"
		})
		if f.state(channel).Title != "Tests" {
			t.Fatalf("title %q", f.state(channel).Title)
		}

		pty.emitData("stale pending output")
		clear := owner.Dispatch(channel, map[string]any{"type": "terminal/cleared"})
		owner.NextEnvelope(channel, clear)
		time.Sleep(20 * time.Millisecond)
		if got := f.state(channel).Content; len(got) != 0 {
			t.Fatalf("content after clear: %v", contentValues(f.state(channel)))
		}
	})

	twin.Run(t, "terminal-service", "batches burst output and flushes pending data before exit", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		before := f.host.ServerSeq()
		for i := 0; i < 100; i++ {
			pty.emitData("x")
		}
		testkit.Eventually(t, "terminal output to be published", func() bool { return len(f.state(channel).Content) > 0 })
		if f.host.ServerSeq() != before+1 {
			t.Fatalf("serverSeq %d, want %d: a burst must be one action", f.host.ServerSeq(), before+1)
		}
		if got := contentValues(f.state(channel)); !reflect.DeepEqual(got, []string{strings.Repeat("x", 100)}) {
			t.Fatalf("%q", got)
		}
		pty.emitData("tail")
		pty.emitExit(0)
		if got := contentValues(f.state(channel)); !reflect.DeepEqual(got, []string{strings.Repeat("x", 100) + "tail"}) {
			t.Fatalf("%q", got)
		}
		if got := lifecycleJSON(f.state(channel)); got != `{"status":"exited","exitCode":0}` {
			t.Fatalf("%s", got)
		}
	})

	twin.Run(t, "terminal-service", "restricts interaction and claim transfer without preventing cleanup", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		observer := f.connect("terminal-observer")
		subscribeRaw(owner, channel)
		subscribeRaw(observer, channel)

		input := observer.Dispatch(channel, map[string]any{"type": "terminal/input", "data": "nope"})
		rejected := nextFrom(t, observer, channel, "terminal-observer", input)
		if rejected.RejectionReason == nil || !strings.Contains(*rejected.RejectionReason, "claimed by another client") {
			t.Fatalf("%v", rejected.RejectionReason)
		}
		if len(pty.Writes()) != 0 {
			t.Fatalf("writes %q", pty.Writes())
		}

		claim := owner.Dispatch(channel, map[string]any{"type": "terminal/claimed", "claim": clientClaim("terminal-observer")})
		// A rejection is broadcast to the channel's subscribers, and both clients count clientSeq
		// from 1: match on the originating client as well.
		rejectedClaim := nextFrom(t, owner, channel, ownerID, claim)
		if rejectedClaim.RejectionReason == nil || !strings.Contains(*rejectedClaim.RejectionReason, "does not support transferring terminal claims") {
			t.Fatalf("%v", *rejectedClaim.RejectionReason)
		}
		raw, _ := json.Marshal(f.state(channel).Claim)
		if string(raw) != `{"kind":"client","clientId":"terminal-owner"}` {
			t.Fatalf("claim %s", raw)
		}

		observer.Must("disposeTerminal", map[string]any{"channel": channel})
		if pty.Kills() != 1 || f.host.Store().Has(channel) || len(f.root().Terminals) != 0 {
			t.Fatalf("kills=%d exists=%v terminals=%v", pty.Kills(), f.host.Store().Has(channel), f.root().Terminals)
		}
	})

	twin.Run(t, "terminal-service", "spawns only once when duplicate creates race", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		params := map[string]any{"channel": newTerminalChannel(), "claim": clientClaim(ownerID)}
		var wg sync.WaitGroup
		errs := make([]*testkit.RPCError, 2)
		for i := range errs {
			wg.Add(1)
			go func() { defer wg.Done(); _, errs[i] = owner.Request("createTerminal", params) }()
		}
		wg.Wait()
		failed := 0
		for _, e := range errs {
			if e != nil {
				failed++
				if e.Code != wire.CodeAlreadyExists {
					t.Fatalf("code %d", e.Code)
				}
			}
		}
		if failed != 1 || len(f.spawner.Calls()) != 1 {
			t.Fatalf("failed=%d spawns=%d", failed, len(f.spawner.Calls()))
		}
	})

	twin.Run(t, "terminal-service", "rejects unsupported claims and invalid creation parameters before spawning", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel := newTerminalChannel()
		for _, params := range []map[string]any{
			{"channel": channel, "claim": map[string]any{"kind": "session", "session": "ahp-session:/s", "chat": "ahp-chat:/s"}},
			{"channel": channel, "claim": clientClaim("someone-else")},
			{"channel": channel, "claim": clientClaim(ownerID), "cols": 0},
			{"channel": channel, "claim": clientClaim(ownerID), "cwd": "https://example.com"},
			{"channel": channel, "claim": clientClaim(ownerID), "cwd": "file://remote/share"},
		} {
			owner.ExpectError("createTerminal", params, wire.CodeInvalidParams)
		}
		if n := len(f.spawner.Calls()); n != 0 {
			t.Fatalf("%d PTYs spawned", n)
		}
	})

	twin.Run(t, "terminal-service", "reserves the provider session scheme from terminal creation", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		owner.ExpectError("createTerminal", map[string]any{"channel": "pi:/reserved-for-sessions", "claim": clientClaim(ownerID)}, wire.CodeInvalidParams)
		if n := len(f.spawner.Calls()); n != 0 {
			t.Fatalf("%d PTYs spawned", n)
		}
	})

	twin.Run(t, "terminal-service", "does not let terminal disposal target another channel kind", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		owner.ExpectError("disposeTerminal", map[string]any{"channel": wire.RootChannel}, wire.CodeInvalidParams)
		if !f.host.Store().Has(wire.RootChannel) {
			t.Fatal("the root channel was disposed")
		}
	})

	twin.Run(t, "terminal-service", "records natural exit and disposes exited or running terminals", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		first, firstPty := f.createTerminal(owner, map[string]any{"name": "First"})
		firstPty.emitExit(7)
		if got := lifecycleJSON(f.state(first)); got != `{"status":"exited","exitCode":7}` {
			t.Fatalf("%s", got)
		}
		if _, exited := f.root().Terminals[0].Lifecycle.Value.(*ahptypes.TerminalExitedLifecycleState); !exited {
			t.Fatal("root catalogue still shows the terminal as running")
		}
		owner.Must("disposeTerminal", map[string]any{"channel": first})
		if f.host.Store().Has(first) || firstPty.Kills() != 0 || len(f.root().Terminals) != 0 {
			t.Fatalf("exists=%v kills=%d terminals=%v", f.host.Store().Has(first), firstPty.Kills(), f.root().Terminals)
		}

		second, secondPty := f.createTerminal(owner, map[string]any{"name": "Second"})
		owner.Must("disposeTerminal", map[string]any{"channel": second})
		if secondPty.Kills() != 1 || f.host.Store().Has(second) {
			t.Fatalf("kills=%d exists=%v", secondPty.Kills(), f.host.Store().Has(second))
		}
		owner.Must("disposeTerminal", map[string]any{"channel": second})
	})

	twin.Run(t, "terminal-service", "keeps the PTY attached across a same-host reconnect", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		subscribeRaw(owner, channel)
		lastSeen := f.host.ServerSeq()
		owner.Close()
		pty.emitData("offline output")
		testkit.Eventually(t, "offline terminal output to be sequenced", func() bool { return f.host.ServerSeq() > lastSeen })

		resumed := testkit.Connect(t, f.host)
		t.Cleanup(resumed.Close)
		var result struct {
			Type    string
			Actions []struct{ Action map[string]any }
		}
		resumed.Decode(resumed.Must("reconnect", map[string]any{"channel": wire.RootChannel, "clientId": ownerID, "lastSeenServerSeq": lastSeen, "subscriptions": []string{channel}}), &result)
		if result.Type != "replay" || len(result.Actions) != 1 || !reflect.DeepEqual(result.Actions[0].Action, map[string]any{"type": "terminal/data", "data": "offline output"}) {
			t.Fatalf("%+v", result)
		}
		if len(f.spawner.Calls()) != 1 {
			t.Fatal("the reconnect spawned a second PTY")
		}
		resumed.Dispatch(channel, map[string]any{"type": "terminal/input", "data": "continued"})
		testkit.Eventually(t, "reconnected terminal input to reach the PTY", func() bool { return contains(strings.Join(pty.Writes(), ""), "continued") })
	})

	twin.Run(t, "terminal-service", "falls back to a live terminal snapshot after replay eviction", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		subscribeRaw(owner, channel)
		lastSeen := f.host.ServerSeq()
		owner.Close()
		chunks := []string{"zero", "one", "two", "three", "four", "five"}
		for _, chunk := range chunks {
			before := f.host.ServerSeq()
			pty.emitData(chunk)
			testkit.Eventually(t, "terminal output to be sequenced", func() bool { return f.host.ServerSeq() > before })
		}
		resumed := testkit.Connect(t, f.host)
		t.Cleanup(resumed.Close)
		var result struct {
			Type      string
			Snapshots []struct {
				Resource string
				FromSeq  int64
				State    map[string]any
			}
		}
		resumed.Decode(resumed.Must("reconnect", map[string]any{"channel": wire.RootChannel, "clientId": ownerID, "lastSeenServerSeq": lastSeen, "subscriptions": []string{channel}}), &result)
		if result.Type != "snapshot" || len(result.Snapshots) != 1 {
			t.Fatalf("%+v", result)
		}
		snap := result.Snapshots[0]
		if snap.Resource != channel || snap.FromSeq != f.host.ServerSeq() {
			t.Fatalf("%+v", snap)
		}
		want := map[string]any{"type": "unclassified", "value": strings.Join(chunks, "")}
		if !reflect.DeepEqual(snap.State["content"], []any{want}) || !reflect.DeepEqual(snap.State["lifecycle"], map[string]any{"status": "running"}) ||
			!reflect.DeepEqual(snap.State["claim"], clientClaim(ownerID)) || snap.State["cols"] != float64(80) || snap.State["rows"] != float64(24) {
			t.Fatalf("snapshot state %v", snap.State)
		}
		resumed.Dispatch(channel, map[string]any{"type": "terminal/input", "data": "after snapshot"})
		testkit.Eventually(t, "post-snapshot input to reach the PTY", func() bool { return contains(strings.Join(pty.Writes(), ""), "after snapshot") })
	})

	twin.Run(t, "terminal-service", "bounds snapshot scrollback while retaining the newest VT stream", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		pty.emitData("discard" + strings.Repeat("x", expectedScrollbackChars))
		testkit.Eventually(t, "terminal output to be published", func() bool { return len(f.state(channel).Content) > 0 })
		got := contentValues(f.state(channel))
		if len(got) != 1 || got[0] != strings.Repeat("x", expectedScrollbackChars) {
			t.Fatalf("scrollback holds %d parts, first of %d chars", len(got), len(got[0]))
		}
	})

	twin.Run(t, "terminal-service", "kills running PTYs during service shutdown", func(t *testing.T) {
		f, owner := startTerminalFixture(t)
		channel, pty := f.createTerminal(owner, nil)
		f.terminals.Shutdown()
		if pty.Kills() != 1 || f.host.Store().Has(channel) || len(f.root().Terminals) != 0 {
			t.Fatalf("kills=%d exists=%v terminals=%v", pty.Kills(), f.host.Store().Has(channel), f.root().Terminals)
		}
		f.terminals.Shutdown()
	})
}

func TestTerminalNotWired(t *testing.T) {
	twin.Run(t, "terminal-service", "returns MethodNotFound when terminal support is not wired", func(t *testing.T) {
		h := testkit.NewHost(host.Options{})
		c := testkit.Connect(t, h)
		defer c.Close()
		c.Initialize("no-terminals", nil)
		c.ExpectError("createTerminal", map[string]any{"channel": "ahp-terminal:/none", "claim": clientClaim("no-terminals")}, wire.CodeMethodNotFound)
	})
}

// subscribeRaw subscribes without decoding the snapshot: the vendored typed SnapshotState cannot
// tell a terminal state from a session state (see the FRICTION notes).
func subscribeRaw(c *testkit.Client, channel string) json.RawMessage {
	return c.Must("subscribe", map[string]any{"channel": channel})
}

func nextFrom(t *testing.T, c *testkit.Client, channel, clientID string, clientSeq int64) ahptypes.ActionEnvelope {
	t.Helper()
	n, ok := c.Await(func(n testkit.Notification) bool {
		env, isAction := n.Envelope()
		return isAction && env.Channel == channel && env.Origin != nil && env.Origin.ClientId == clientID && env.Origin.ClientSeq == clientSeq
	}, testkit.Timeout)
	if !ok {
		t.Fatalf("timed out waiting for %s's action %d on %s", clientID, clientSeq, channel)
	}
	env, _ := n.Envelope()
	return env
}
