package changeset_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// controlledWatcher is upstream's ControlledWatcher: a watch the test fires by hand.
type controlledWatcher struct{ changed func(string) }

func (*controlledWatcher) Close() error { return nil }

// controlledWatches is upstream's ControlledWatchFactory.
type controlledWatches struct {
	mu       sync.Mutex
	watchers []*controlledWatcher
}

func (w *controlledWatches) create(_ context.Context, _ []string, _ func(string) bool, changed func(string), _ func(error)) (changeset.Watcher, error) {
	watcher := &controlledWatcher{changed: changed}
	w.mu.Lock()
	w.watchers = append(w.watchers, watcher)
	w.mu.Unlock()
	return watcher, nil
}

func (w *controlledWatches) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watchers)
}

func (w *controlledWatches) current(t *testing.T) *controlledWatcher {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.watchers) == 0 {
		t.Fatal("watcher was not created")
	}
	return w.watchers[len(w.watchers)-1]
}

// controlledGit is upstream's ControlledGit: its first computation can be held until released.
type controlledGit struct {
	workspace      changeset.Workspace
	computeStarted chan struct{}
	computeGate    chan struct{}
	startedOnce    sync.Once
	gateOnce       sync.Once

	mu             sync.Mutex
	inflight       int
	computeCalls   int
	ignoredCalls   int
	failCompute    bool
	failIgnored    bool
	blockFirstCall bool
}

func newControlledGit(workspace changeset.Workspace) *controlledGit {
	return &controlledGit{workspace: workspace, computeStarted: make(chan struct{}), computeGate: make(chan struct{}), blockFirstCall: true}
}

func (g *controlledGit) releaseGate() { g.gateOnce.Do(func() { close(g.computeGate) }) }

func (g *controlledGit) calls() (compute, ignored int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.computeCalls, g.ignoredCalls
}

func (g *controlledGit) Inspect(context.Context, string) (*changeset.Workspace, error) {
	ws := g.workspace
	return &ws, nil
}

func (g *controlledGit) running() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight
}

func (g *controlledGit) Compute(ctx context.Context, _ changeset.Workspace, _ string, _ changeset.Kind) ([]ahptypes.ChangesetFile, error) {
	g.mu.Lock()
	g.computeCalls++
	g.inflight++
	version := g.computeCalls
	fail, block := g.failCompute, g.blockFirstCall
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.inflight--; g.mu.Unlock() }()
	if fail {
		return nil, errors.New("Git diff failed")
	}
	if version == 1 && block {
		g.startedOnce.Do(func() { close(g.computeStarted) })
		select {
		case <-g.computeGate:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	uri := wire.PathToFileURI(filepath.Join(g.workspace.Cwd, "result.txt"))
	added, removed := int64(version), int64(0)
	return []ahptypes.ChangesetFile{{Id: uri, Edit: ahptypes.FileEdit{
		After: &ahptypes.FileEditSide{Uri: uri, Content: ahptypes.ContentRef{Uri: uri}},
		Diff:  &ahptypes.FileEditDiffStats{Added: &added, Removed: &removed},
	}}}, nil
}

func (g *controlledGit) IgnoredDirectories(context.Context, changeset.Workspace) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ignoredCalls++
	if g.failIgnored {
		return nil, errors.New("ignored paths unavailable")
	}
	return nil, nil
}

func (g *controlledGit) PathForBlob(changeset.Workspace, changeset.BlobRef) (string, bool) {
	return "", false
}

func (g *controlledGit) ReadBlob(context.Context, changeset.Workspace, changeset.BlobRef) ([]byte, error) {
	return nil, errors.New("not used")
}

type controlled struct {
	host      *host.Host
	client    *testkit.Client
	service   *changeset.Service
	git       *controlledGit
	watches   *controlledWatches
	session   changeset.Session
	chat      string
	channel   string
	workspace string
}

type controlledOptions struct{ failCompute, failIgnored, blockFirstCompute bool }

func startControlled(t *testing.T, o controlledOptions) *controlled {
	t.Helper()
	workspace := t.TempDir()
	gitDir := filepath.Join(workspace, ".git")
	for _, dir := range []string{filepath.Join(gitDir, "refs", "heads"), filepath.Join(gitDir, "info")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(gitDir, "index"), "")
	write(t, filepath.Join(gitDir, "info", "exclude"), "")

	id := newID()
	sessionChannel, chatChannel := wire.SessionURI(id), wire.ChatURI(id)
	session := changeset.Session{URI: sessionChannel, SessionID: id, WorkingDirectory: workspace}
	h := testkit.NewHost(host.Options{})
	state := &ahptypes.SessionState{
		Provider: "pi", Title: "Controlled", Status: ahptypes.SessionStatusIdle, Lifecycle: ahptypes.SessionLifecycleReady,
		WorkingDirectories: []string{wire.PathToFileURI(workspace)}, ActiveClients: []ahptypes.SessionActiveClient{}, Chats: []ahptypes.ChatSummary{},
	}
	if err := h.Store().Create(sessionChannel, state, wire.KindSession); err != nil {
		t.Fatal(err)
	}
	if err := h.Store().Create(chatChannel, channels.InitialChatState(chatChannel, "Controlled", nil), wire.KindChat); err != nil {
		t.Fatal(err)
	}
	git := newControlledGit(changeset.Workspace{
		Cwd: workspace, RepositoryRoot: workspace, GitDirectory: gitDir, CommonGitDirectory: gitDir, Pathspec: ".",
	})
	git.workspace.Head, git.workspace.Parent = repeat('a', 40), repeat('b', 40)
	git.failCompute, git.failIgnored, git.blockFirstCall = o.failCompute, o.failIgnored, o.blockFirstCompute
	watches := &controlledWatches{}
	service := changeset.New(h, changeset.Options{
		GetSession: func(sessionID string) (changeset.Session, bool) { return session, sessionID == id },
		GetSessionByChat: func(chat string) (changeset.Session, bool) {
			return session, chat == chatChannel
		},
		HydrateSession: func(_ context.Context, sessionID string) (changeset.Session, bool, error) {
			return session, sessionID == id, nil
		},
		Git: git, Watch: watches.create, Debounce: 5 * time.Millisecond, StateGrace: changeset.DefaultStateGrace,
	})
	if err := service.Attach(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	caps := host.Capabilities{Hydrator: service}
	h.Serve(caps)
	client := testkit.Connect(t, h)
	client.Initialize("controlled-"+id, nil)
	c := &controlled{host: h, client: client, service: service, git: git, watches: watches, session: session,
		chat: chatChannel, channel: changeset.URI(id, changeset.Uncommitted), workspace: workspace}
	t.Cleanup(func() {
		git.releaseGate()
		client.Close()
		service.Dispose()
	})
	return c
}

func repeat(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func (c *controlled) state() *ahptypes.ChangesetState { return c.host.Store().Changeset(c.channel) }

func (c *controlled) status() ahptypes.ChangesetStatus {
	if s := c.state(); s != nil {
		return s.Status
	}
	return ""
}

// Twins of upstream test/changeset-lifecycle.test.serial.ts.
func TestChangesetLifecycle(t *testing.T) {
	twin.Run(t, "changeset-lifecycle", "observes changes during the initial scan and rebuilds changed ignore rules", func(t *testing.T) {
		c := startControlled(t, controlledOptions{blockFirstCompute: true})
		c.client.Subscribe(c.channel)
		<-c.git.computeStarted
		c.watches.current(t).changed(filepath.Join(c.workspace, ".gitignore"))
		c.git.releaseGate()

		eventually(t, "a follow-up changeset scan", func() bool { n, _ := c.git.calls(); return n >= 2 })
		eventually(t, "the rebuilt watcher", func() bool { _, n := c.git.calls(); return n >= 2 })
		eventually(t, "the follow-up scan to publish", func() bool {
			s := c.state()
			return s != nil && s.Status == ahptypes.ChangesetStatusReady && len(s.Files) == 1 && added(s.Files[0]) == 2
		})
	})

	twin.Run(t, "changeset-lifecycle", "refreshes file changes while a turn is still active", func(t *testing.T) {
		c := startControlled(t, controlledOptions{})
		c.client.Subscribe(c.channel)
		eventually(t, "the initial changeset", func() bool { return c.status() == ahptypes.ChangesetStatusReady })
		initial, _ := c.git.calls()
		c.host.DispatchServerAction(c.chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
			Type: ahptypes.ActionTypeChatTurnStarted, TurnId: "active-turn", StartedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			Message: ahptypes.Message{Text: "Keep working", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
		}})
		c.watches.current(t).changed(filepath.Join(c.workspace, "during-turn.ts"))

		eventually(t, "a changeset refresh before turn completion", func() bool {
			n, _ := c.git.calls()
			s := c.state()
			return n > initial && s != nil && s.Status == ahptypes.ChangesetStatusReady && len(s.Files) == 1 && added(s.Files[0]) > int64(initial)
		})
		if chat := c.host.Store().Chat(c.chat); chat.ActiveTurn == nil || chat.ActiveTurn.Id != "active-turn" {
			t.Fatalf("the turn must still be active: %+v", chat.ActiveTurn)
		}
	})

	twin.Run(t, "changeset-lifecycle", "does not recursively watch when ignored paths cannot be determined", func(t *testing.T) {
		c := startControlled(t, controlledOptions{failIgnored: true})
		c.client.Subscribe(c.channel)
		eventually(t, "the one-shot changeset scan", func() bool { return c.status() == ahptypes.ChangesetStatusReady })
		if _, ignored := c.git.calls(); ignored == 0 {
			t.Fatal("the ignored directories must have been asked for")
		}
		if n := c.watches.count(); n != 0 {
			t.Fatalf("watchers created = %d, want none", n)
		}
		if n := c.service.ActiveWatcherCount(); n != 0 {
			t.Fatalf("active watchers = %d", n)
		}
	})

	twin.Run(t, "changeset-lifecycle", "publishes an explicit error when Git cannot compute the changeset", func(t *testing.T) {
		c := startControlled(t, controlledOptions{failCompute: true})
		c.client.Subscribe(c.channel)
		eventually(t, "the failed changeset", func() bool { return c.status() == ahptypes.ChangesetStatusError })
		s := c.state()
		if s.Error == nil || s.Error.ErrorType != "gitChangesFailed" || s.Error.Message != "Git diff failed" {
			t.Fatalf("error = %+v", s.Error)
		}
	})

	twin.Run(t, "changeset-lifecycle", "drains an in-flight scan before deleting its channel", func(t *testing.T) {
		c := startControlled(t, controlledOptions{blockFirstCompute: true})
		c.client.Subscribe(c.channel)
		<-c.git.computeStarted
		c.service.RemoveSession(c.session.SessionID)
		if c.host.Store().Has(c.channel) {
			t.Fatal("the channel must be gone once the session is removed")
		}
		c.git.releaseGate()
		time.Sleep(50 * time.Millisecond)
		if c.host.Store().Has(c.channel) {
			t.Fatal("a released scan must not bring the channel back")
		}
	})
}

// A turn that ends may have changed files the watcher missed, so each way a turn ends refreshes
// a changeset somebody is looking at (changeset-service.ts, the onActionCommitted hook).
func TestChangesetRefreshesWhenATurnEnds(t *testing.T) {
	ends := map[string]func(turn string) ahptypes.StateAction{
		"complete": func(turn string) ahptypes.StateAction {
			return ahptypes.StateAction{Value: &ahptypes.ChatTurnCompleteAction{Type: ahptypes.ActionTypeChatTurnComplete, TurnId: turn}}
		},
		"cancelled": func(turn string) ahptypes.StateAction {
			return ahptypes.StateAction{Value: &ahptypes.ChatTurnCancelledAction{Type: ahptypes.ActionTypeChatTurnCancelled, TurnId: turn}}
		},
		"error": func(turn string) ahptypes.StateAction {
			return ahptypes.StateAction{Value: &ahptypes.ChatErrorAction{Type: ahptypes.ActionTypeChatError, TurnId: turn}}
		},
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			c := startControlled(t, controlledOptions{})
			c.client.Subscribe(c.channel)
			eventually(t, "the initial changeset", func() bool { return c.status() == ahptypes.ChangesetStatusReady })
			initial, _ := c.git.calls()
			c.host.DispatchServerAction(c.chat, ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
				Type: ahptypes.ActionTypeChatTurnStarted, TurnId: "t1", StartedAt: "2025-01-01T00:00:00.000Z",
				Message: ahptypes.Message{Text: "go", Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
			}})
			time.Sleep(150 * time.Millisecond)
			if n, _ := c.git.calls(); n != initial {
				t.Fatalf("starting a turn must not recompute (%d -> %d)", initial, n)
			}
			c.host.DispatchServerAction(c.chat, end("t1"))
			eventually(t, "the changeset to be recomputed after the turn", func() bool { n, _ := c.git.calls(); return n > initial })
		})
	}
}

// Removing a session returns only when the scan that was in flight has ended, so nothing of it can
// touch the host afterwards.
func TestChangesetRemoveSessionWaitsForTheScan(t *testing.T) {
	c := startControlled(t, controlledOptions{blockFirstCompute: true})
	c.client.Subscribe(c.channel)
	<-c.git.computeStarted
	if c.git.running() != 1 {
		t.Fatalf("running scans = %d", c.git.running())
	}
	c.service.RemoveSession(c.session.SessionID)
	if n := c.git.running(); n != 0 {
		t.Fatalf("a scan is still running after the session was removed: %d", n)
	}
}

// A refresh of a changeset somebody is watching says so: the status goes to computing before the
// new content arrives (changeset-service.ts, refreshOnce).
func TestChangesetRefreshAnnouncesComputing(t *testing.T) {
	c := startControlled(t, controlledOptions{})
	c.client.Subscribe(c.channel)
	eventually(t, "the initial changeset", func() bool { return c.status() == ahptypes.ChangesetStatusReady })
	initial, _ := c.git.calls()
	before := len(c.client.Notifications())
	c.watches.current(t).changed(filepath.Join(c.workspace, "x.ts"))
	eventually(t, "the refresh", func() bool {
		n, _ := c.git.calls()
		s := c.state()
		return n > initial && s != nil && s.Status == ahptypes.ChangesetStatusReady && added(s.Files[0]) > int64(initial)
	})
	var order []string
	for _, note := range c.client.Notifications()[before:] {
		env, ok := note.Envelope()
		if !ok || env.Channel != c.channel {
			continue
		}
		switch a := env.Action.Value.(type) {
		case *ahptypes.ChangesetStatusChangedAction:
			order = append(order, "status:"+string(a.Status))
		case *ahptypes.ChangesetContentChangedAction:
			order = append(order, "content")
		}
	}
	want := []string{"status:computing", "content", "status:ready"}
	if len(order) < 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("actions of a refresh = %v, want %v first", order, want)
	}
}
