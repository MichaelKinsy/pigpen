package changeset_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
	"github.com/MichaelKinsy/pigpen/ahp/internal/compose"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

type obj = map[string]any

// wait is how long an asynchronous result may take: real git runs and a polling watcher are
// slower than the in-memory fakes testkit.Timeout is sized for.
const wait = 15 * time.Second

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// git runs a git command in dir with the ambient repository overrides removed.
func git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t testing.TB, dir string) {
	t.Helper()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "pi-ahp test")
}

func write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t testing.TB, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func realDir(t testing.TB, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// fileName is the base name of a changed file, as upstream's test reads it from the edit URIs.
func fileName(t testing.TB, file ahptypes.ChangesetFile) string {
	t.Helper()
	uri := ""
	switch {
	case file.Edit.After != nil:
		uri = file.Edit.After.Uri
	case file.Edit.Before != nil:
		uri = file.Edit.Before.Uri
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(parsed.Path)
}

func fileNames(t testing.TB, state *ahptypes.ChangesetState) []string {
	names := []string{}
	for _, f := range state.Files {
		names = append(names, fileName(t, f))
	}
	sort.Strings(names)
	return names
}

func added(file ahptypes.ChangesetFile) int64 {
	if file.Edit.Diff == nil || file.Edit.Diff.Added == nil {
		return -1
	}
	return *file.Edit.Diff.Added
}

func findFile(t testing.TB, state *ahptypes.ChangesetState, name string) *ahptypes.ChangesetFile {
	if state == nil {
		return nil
	}
	for i := range state.Files {
		if fileName(t, state.Files[i]) == name {
			return &state.Files[i]
		}
	}
	return nil
}

// nullBackend is an agent that does nothing: these tests are about the repository.
type nullBackend struct{}

func (nullBackend) Subscribe(func(mapper.Event)) func()                  { return func() {} }
func (nullBackend) Prompt(context.Context, string, []mapper.Image) error { return nil }
func (nullBackend) Steer(context.Context, string, []mapper.Image) error  { return nil }
func (nullBackend) Abort(context.Context) error                          { return nil }

var idMu sync.Mutex
var idN int

func newID() string {
	idMu.Lock()
	defer idMu.Unlock()
	idN++
	return "0190a1b2-c3d4-7e5f-8a9b-" + strings.Repeat("0", 11) + string(rune('0'+idN%10))
}

type fixture struct {
	t         *testing.T
	built     *compose.Built
	client    *testkit.Client
	workspace string
}

func (f *fixture) changeset(channel string) *ahptypes.ChangesetState {
	return f.built.Host.Store().Changeset(channel)
}

func (f *fixture) session(uri string) *ahptypes.SessionState {
	return f.built.Host.Store().Session(uri)
}

// attach is upstream's built.changesets.attach(built.sessions.get(session)).
func (f *fixture) attach(uri string) {
	f.t.Helper()
	s, ok := f.built.Sessions.Registry.Get(uri)
	if !ok {
		f.t.Fatalf("session %s is not registered", uri)
	}
	if err := f.built.Changesets.Attach(context.Background(), changeset.Session{URI: s.URI, SessionID: s.SessionID, WorkingDirectory: s.WorkingDirectory}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) createSession(params obj) string {
	f.t.Helper()
	id := newID()
	uri := wire.SessionURI(id)
	params["channel"] = uri
	f.client.Must("createSession", params)
	return uri
}

// readyState waits for the changeset to be ready, then returns its subscribed snapshot.
func (f *fixture) readyState(channel string) *ahptypes.ChangesetState {
	f.t.Helper()
	eventually(f.t, channel+" to become ready", func() bool {
		s := f.changeset(channel)
		return s != nil && s.Status == ahptypes.ChangesetStatusReady
	})
	snap := f.client.Subscribe(channel)
	if snap.Snapshot == nil || snap.Snapshot.State.Changeset == nil {
		f.t.Fatalf("no changeset snapshot for %s", channel)
	}
	state := snap.Snapshot.State.Changeset
	testkit.AssertValid(f.t, "state", "ChangesetState", state)
	return state
}

func (f *fixture) readBlob(uri string) ahptypes.ResourceReadResult {
	f.t.Helper()
	var result ahptypes.ResourceReadResult
	f.client.Decode(f.client.Must("resourceRead", obj{"channel": wire.RootChannel, "uri": uri, "encoding": "utf-8"}), &result)
	return result
}

type fixtureOptions struct {
	deleteFile func(path string) (pi.SessionFileDeletionResult, error)
}

func startFixture(t *testing.T, workspace string, o fixtureOptions) *fixture {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	built, err := compose.Build(compose.Options{
		ServerInfo: &ahptypes.Implementation{Name: "pi-ahp"}, WorkingDirectory: workspace, SessionRoot: root,
		CreateSessionManager: pi.PersistentStorage(filepath.Join(root, "created")),
		CreateBackend:        func(*pi.LiveSession) (pi.Backend, error) { return nullBackend{}, nil },
		DeleteFile:           o.deleteFile,
		TuneChangesets: func(c *changeset.Options) {
			c.PollInterval, c.Debounce = 20*time.Millisecond, 5*time.Millisecond
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := testkit.Connect(t, built.Host)
	client.Initialize("changeset-client", nil)
	t.Cleanup(func() {
		client.Close()
		built.Close()
	})
	return &fixture{t: t, built: built, client: client, workspace: workspace}
}

func okDelete(string) (pi.SessionFileDeletionResult, error) {
	return pi.SessionFileDeletionResult{OK: true}, nil
}

// Twins of upstream test/changeset.test.serial.ts.
func TestGitChangesets(t *testing.T) {
	requireGit(t)
	workspace := realDir(t, t.TempDir())
	initRepo(t, workspace)
	write(t, filepath.Join(workspace, "edited.txt"), "base\n")
	write(t, filepath.Join(workspace, "deleted.txt"), "deleted\n")
	write(t, filepath.Join(workspace, "old-name.txt"), "renamed\n")
	write(t, filepath.Join(workspace, "tab\tname.txt"), "tab base\n")
	git(t, workspace, "add", ".")
	git(t, workspace, "commit", "-qm", "base")

	appendTo(t, filepath.Join(workspace, "edited.txt"), "committed\n")
	if err := os.Remove(filepath.Join(workspace, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(workspace, "old-name.txt"), filepath.Join(workspace, "new-name.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(workspace, "committed-only.txt"), "committed only\n")
	appendTo(t, filepath.Join(workspace, "tab\tname.txt"), "tab committed\n")
	git(t, workspace, "add", ".")
	git(t, workspace, "commit", "-qm", "latest")

	appendTo(t, filepath.Join(workspace, "edited.txt"), "dirty\n")
	appendTo(t, filepath.Join(workspace, "tab\tname.txt"), "tab dirty\n")
	write(t, filepath.Join(workspace, "untracked.txt"), "untracked\n")

	f := startFixture(t, workspace, fixtureOptions{deleteFile: okDelete})

	twin.Run(t, "changeset", "keeps the latest commit separate from uncommitted work and refreshes it", func(t *testing.T) {
		f.t = t
		session := f.createSession(obj{})
		f.client.Subscribe(session)
		eventually(t, "the Git changeset catalogue", func() bool { return len(f.session(session).Changesets) == 2 })
		state := f.session(session)
		testkit.AssertValid(t, "state", "SessionState", state)
		type labelled struct{ Label, Kind string }
		var got []labelled
		for _, c := range state.Changesets {
			got = append(got, labelled{c.Label, c.ChangeKind})
		}
		if want := []labelled{{"Latest Commit", "branch"}, {"Uncommitted Changes", "uncommitted"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("catalogue = %v, want %v", got, want)
		}
		latestChannel, uncommittedChannel := state.Changesets[0].UriTemplate, state.Changesets[1].UriTemplate

		f.client.Subscribe(latestChannel)
		f.client.Subscribe(uncommittedChannel)
		latest := f.readyState(latestChannel)
		uncommitted := f.readyState(uncommittedChannel)
		if want := []string{"committed-only.txt", "deleted.txt", "edited.txt", "new-name.txt", "tab\tname.txt"}; !reflect.DeepEqual(fileNames(t, latest), want) {
			t.Fatalf("latest commit files = %v, want %v", fileNames(t, latest), want)
		}
		if want := []string{"edited.txt", "tab\tname.txt", "untracked.txt"}; !reflect.DeepEqual(fileNames(t, uncommitted), want) {
			t.Fatalf("uncommitted files = %v, want %v", fileNames(t, uncommitted), want)
		}

		latestAfter := findFile(t, latest, "edited.txt").Edit.After
		uncommittedBefore := findFile(t, uncommitted, "edited.txt").Edit.Before
		if latestAfter == nil || !strings.HasPrefix(latestAfter.Content.Uri, "git-blob:") {
			t.Fatalf("latest commit content = %+v", latestAfter)
		}
		query := latestAfter.Content.Uri[strings.Index(latestAfter.Content.Uri, "?"):]
		if len(query) < 2 || strings.Trim(query[1:], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
			t.Fatalf("blob query %q is not one opaque token", query)
		}
		if uncommittedBefore == nil || !strings.HasPrefix(uncommittedBefore.Content.Uri, "git-blob:") {
			t.Fatalf("uncommitted baseline = %+v", uncommittedBefore)
		}
		committed := f.readBlob(latestAfter.Content.Uri)
		baseline := f.readBlob(uncommittedBefore.Content.Uri)
		if committed.Data != "base\ncommitted\n" || baseline.Data != committed.Data {
			t.Fatalf("blob contents = %q and %q", committed.Data, baseline.Data)
		}
		if n := f.built.Changesets.ActiveWatcherCount(); n != 1 {
			t.Fatalf("active watchers = %d, want 1", n)
		}

		appendTo(t, filepath.Join(workspace, "edited.txt"), "later\n")
		eventually(t, "the uncommitted changeset to refresh", func() bool {
			s := f.changeset(uncommittedChannel)
			return s != nil && s.Status == ahptypes.ChangesetStatusReady && findFile(t, s, "edited.txt") != nil && added(*findFile(t, s, "edited.txt")) == 2
		})
		unchanged := findFile(t, f.changeset(latestChannel), "edited.txt")
		if after := f.readBlob(unchanged.Edit.After.Content.Uri); after.Data != "base\ncommitted\n" {
			t.Fatalf("latest commit after a refresh reads %q", after.Data)
		}

		liveFile := filepath.Join(workspace, "live-list.txt")
		write(t, liveFile, "live\n")
		eventually(t, "a newly added file to enter the changeset", func() bool {
			s := f.changeset(uncommittedChannel)
			return s != nil && s.Status == ahptypes.ChangesetStatusReady && findFile(t, s, "live-list.txt") != nil
		})
		appendTo(t, liveFile, "updated\n")
		eventually(t, "the added file to receive live updates", func() bool {
			file := findFile(t, f.changeset(uncommittedChannel), "live-list.txt")
			return file != nil && added(*file) == 2
		})
		if err := os.Remove(liveFile); err != nil {
			t.Fatal(err)
		}
		eventually(t, "a deleted untracked file to leave the changeset", func() bool {
			s := f.changeset(uncommittedChannel)
			return s != nil && s.Status == ahptypes.ChangesetStatusReady && findFile(t, s, "live-list.txt") == nil
		})

		git(t, workspace, "add", ".")
		git(t, workspace, "commit", "-qm", "move uncommitted work into latest")
		eventually(t, "the committed work to move between changesets", func() bool {
			l, u := f.changeset(latestChannel), f.changeset(uncommittedChannel)
			return l != nil && l.Status == ahptypes.ChangesetStatusReady && findFile(t, l, "untracked.txt") != nil &&
				u != nil && u.Status == ahptypes.ChangesetStatusReady && len(u.Files) == 0
		})
		f.client.Unsubscribe(latestChannel)
		f.client.Unsubscribe(uncommittedChannel)
		eventually(t, "the unobserved Git watcher to close", func() bool { return f.built.Changesets.ActiveWatcherCount() == 0 })
		f.client.Subscribe(uncommittedChannel)
		eventually(t, "the observed Git watcher to reopen", func() bool { return f.built.Changesets.ActiveWatcherCount() == 1 })

		f.client.Must("disposeSession", obj{"channel": session})
		if f.built.Host.Store().Has(latestChannel) || f.built.Host.Store().Has(uncommittedChannel) {
			t.Fatal("the changeset channels must go with their session")
		}
		if n := f.built.Changesets.ActiveWatcherCount(); n != 0 {
			t.Fatalf("active watchers after disposal = %d", n)
		}
	})

	twin.Run(t, "changeset", "limits changes to the session working-directory subtree", func(t *testing.T) {
		f.t = t
		scoped, sibling := filepath.Join(workspace, "scope"), filepath.Join(workspace, "sibling")
		for _, dir := range []string{scoped, sibling} {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		write(t, filepath.Join(scoped, "inside.txt"), "base\n")
		write(t, filepath.Join(sibling, "outside.txt"), "base\n")
		git(t, workspace, "add", ".")
		git(t, workspace, "commit", "-qm", "add scoped files")
		appendTo(t, filepath.Join(scoped, "inside.txt"), "changed\n")
		appendTo(t, filepath.Join(sibling, "outside.txt"), "changed\n")

		session := f.createSession(obj{"workingDirectories": []string{wire.PathToFileURI(scoped)}})
		f.attach(session)
		var channel string
		for _, c := range f.session(session).Changesets {
			if c.ChangeKind == "uncommitted" {
				channel = c.UriTemplate
			}
		}
		if channel == "" {
			t.Fatalf("no uncommitted changeset in %+v", f.session(session).Changesets)
		}
		f.client.Subscribe(channel)
		if got := fileNames(t, f.readyState(channel)); !reflect.DeepEqual(got, []string{"inside.txt"}) {
			t.Fatalf("files = %v", got)
		}
		f.client.Must("disposeSession", obj{"channel": session})
	})

	twin.Run(t, "changeset", "ignores ambient Git repository overrides", func(t *testing.T) {
		f.t = t
		other := t.TempDir()
		git(t, other, "init", "-q")
		t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
		t.Setenv("GIT_INDEX_FILE", filepath.Join(other, "foreign-index"))
		t.Setenv("GIT_WORK_TREE", other)
		inspected, err := changeset.GitChanges{}.Inspect(context.Background(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if inspected == nil || inspected.RepositoryRoot != realDir(t, workspace) {
			t.Fatalf("inspected = %+v", inspected)
		}
	})

	twin.Run(t, "changeset", "omits changesets when Git is unavailable", func(t *testing.T) {
		f.t = t
		directory, emptyPath := t.TempDir(), t.TempDir()
		t.Setenv("PATH", emptyPath)
		session := f.createSession(obj{"workingDirectories": []string{wire.PathToFileURI(directory)}})
		f.attach(session)
		if got := f.session(session).Changesets; got != nil {
			t.Fatalf("changesets = %+v, want none", got)
		}
	})

	twin.Run(t, "changeset", "omits changesets outside Git workspaces", func(t *testing.T) {
		f.t = t
		directory := t.TempDir()
		session := f.createSession(obj{"workingDirectories": []string{wire.PathToFileURI(directory)}})
		f.attach(session)
		if got := f.session(session).Changesets; got != nil {
			t.Fatalf("changesets = %+v, want none", got)
		}
		f.client.Must("disposeSession", obj{"channel": session})
	})

	twin.Run(t, "changeset", "omits Latest Commit before the repository has a commit", func(t *testing.T) {
		f.t = t
		empty := realDir(t, t.TempDir())
		git(t, empty, "init", "-q")
		write(t, filepath.Join(empty, "first.txt"), "first\n")
		session := f.createSession(obj{"workingDirectories": []string{wire.PathToFileURI(empty)}})
		f.attach(session)
		state := f.session(session)
		if len(state.Changesets) != 1 || state.Changesets[0].Label != "Uncommitted Changes" {
			t.Fatalf("changesets = %+v", state.Changesets)
		}
		channel := state.Changesets[0].UriTemplate
		f.client.Subscribe(channel)
		if got := fileNames(t, f.readyState(channel)); !reflect.DeepEqual(got, []string{"first.txt"}) {
			t.Fatalf("files = %v", got)
		}
		f.client.Must("disposeSession", obj{"channel": session})
	})
}

func TestGitChangesetsDeleteFailure(t *testing.T) {
	requireGit(t)
	twin.Run(t, "changeset", "resumes changeset updates when durable session deletion fails", func(t *testing.T) {
		workspace := realDir(t, t.TempDir())
		initRepo(t, workspace)
		file := filepath.Join(workspace, "tracked.txt")
		write(t, file, "base\n")
		git(t, workspace, "add", ".")
		git(t, workspace, "commit", "-qm", "base")
		appendTo(t, file, "before failure\n")
		var mu sync.Mutex
		attempts := 0
		f := startFixture(t, workspace, fixtureOptions{deleteFile: func(string) (pi.SessionFileDeletionResult, error) {
			mu.Lock()
			attempts++
			mu.Unlock()
			return pi.SessionFileDeletionResult{OK: false, Error: "deletion blocked"}, nil
		}})
		session := f.createSession(obj{})
		f.attach(session)
		var channel string
		for _, c := range f.session(session).Changesets {
			if c.ChangeKind == "uncommitted" {
				channel = c.UriTemplate
			}
		}
		f.client.Subscribe(channel)
		f.readyState(channel)

		rpcErr := f.client.ExpectError("disposeSession", obj{"channel": session}, wire.CodeInternalError)
		if !strings.Contains(rpcErr.Message, "failed durable deletion") && !strings.Contains(rpcErr.Message, "deletion blocked") {
			t.Fatalf("message = %q", rpcErr.Message)
		}
		mu.Lock()
		if attempts != 1 {
			t.Fatalf("deletion attempts = %d", attempts)
		}
		mu.Unlock()
		if !f.built.Host.Store().Has(session) || !f.built.Host.Store().Has(channel) {
			t.Fatal("a failed deletion must keep the session and its changeset")
		}
		eventually(t, "the changeset watcher to resume", func() bool { return f.built.Changesets.ActiveWatcherCount() == 1 })

		appendTo(t, file, "after failure\n")
		eventually(t, "changes after failed deletion", func() bool {
			s := f.changeset(channel)
			return s != nil && s.Status == ahptypes.ChangesetStatusReady && len(s.Files) == 1 && added(s.Files[0]) == 2
		})
	})
}

func TestGitChangesetsHydration(t *testing.T) {
	requireGit(t)
	twin.Run(t, "changeset", "hydrates the parent session when its changeset is subscribed directly", func(t *testing.T) {
		root, workspace := t.TempDir(), realDir(t, t.TempDir())
		initRepo(t, workspace)
		write(t, filepath.Join(workspace, "tracked.txt"), "tracked\n")
		git(t, workspace, "add", ".")
		git(t, workspace, "commit", "-qm", "tracked")
		sessionID := newID()
		writer, err := pisession.Create(workspace, filepath.Join(root, "fixture"), pisession.Options{ID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.AppendMessage(obj{"role": "user", "content": "hello", "timestamp": 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.AppendMessage(obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "hi"}}, "timestamp": 0}); err != nil {
			t.Fatal(err)
		}

		h := testkit.NewHost(host.Options{})
		services := pi.NewServices(pi.ServicesOptions{
			Host: h, SessionRoot: root, DefaultWorkingDirectory: workspace,
			CreateSessionManager: pi.PersistentStorage(filepath.Join(root, "created")), DeleteFile: okDelete,
		})
		toSession := func(s *pi.LiveSession) changeset.Session {
			return changeset.Session{URI: s.URI, SessionID: s.SessionID, WorkingDirectory: s.WorkingDirectory}
		}
		byID := func(id string) (changeset.Session, bool) {
			s, ok := services.Registry.Get(wire.SessionURI(id))
			if !ok {
				return changeset.Session{}, false
			}
			return toSession(s), true
		}
		changesets := changeset.New(h, changeset.Options{
			GetSession: byID,
			GetSessionByChat: func(chat string) (changeset.Session, bool) {
				s, ok := services.Registry.GetByChat(chat)
				if !ok {
					return changeset.Session{}, false
				}
				return toSession(s), true
			},
			HydrateSession: func(ctx context.Context, id string) (changeset.Session, bool, error) {
				if _, err := services.Hydrator.Hydrate(ctx, wire.SessionURI(id)); err != nil {
					return changeset.Session{}, false, err
				}
				s, ok := byID(id)
				return s, ok, nil
			},
			OnSessionAvailable: func(l func(changeset.Session)) func() {
				return services.Registry.OnSessionAvailable(func(s *pi.LiveSession) { l(toSession(s)) })
			},
			OnSessionDeletionCommitted: services.Registry.OnSessionDeletionCommitted,
			StateGrace:                 0, Debounce: 5 * time.Millisecond, PollInterval: 20 * time.Millisecond,
		})
		t.Cleanup(changesets.Dispose)
		caps := services.Capabilities()
		caps.Hydrator = chained{changesets, services.Hydrator}
		h.Serve(caps)
		client := testkit.Connect(t, h)
		t.Cleanup(client.Close)
		client.Initialize("direct-changeset-client", nil)

		client.ExpectError("subscribe", obj{"channel": "ahp-changeset:/" + sessionID + "/unknown"}, wire.CodeNotFound)
		if services.Registry.Has(wire.SessionURI(sessionID)) {
			t.Fatal("an unknown changeset must not hydrate its session")
		}

		channel := changeset.URI(sessionID, changeset.LatestCommit)
		snap := client.Subscribe(channel)
		if snap.Snapshot == nil || snap.Snapshot.Resource != channel {
			t.Fatalf("snapshot = %+v", snap.Snapshot)
		}
		if !services.Registry.Has(wire.SessionURI(sessionID)) {
			t.Fatal("subscribing to a changeset must hydrate the session")
		}
		ready := func() *ahptypes.ChangesetState {
			eventually(t, "the changeset to be ready", func() bool {
				s := h.Store().Changeset(channel)
				return s != nil && s.Status == ahptypes.ChangesetStatusReady
			})
			return h.Store().Changeset(channel)
		}
		if got := fileNames(t, ready()); !reflect.DeepEqual(got, []string{"tracked.txt"}) {
			t.Fatalf("files = %v", got)
		}

		client.Unsubscribe(channel)
		eventually(t, "the unobserved changeset state to be evicted", func() bool { return !h.Store().Has(channel) })
		restored := client.Subscribe(channel)
		if restored.Snapshot == nil || restored.Snapshot.Resource != channel {
			t.Fatalf("restored snapshot = %+v", restored.Snapshot)
		}
		if got := fileNames(t, ready()); !reflect.DeepEqual(got, []string{"tracked.txt"}) {
			t.Fatalf("files after restoring = %v", got)
		}
	})
}

type chained []host.ChannelHydrator

func (c chained) Hydrate(ctx context.Context, channel string) (bool, error) {
	for _, h := range c {
		if ok, err := h.Hydrate(ctx, channel); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// Behaviour beyond upstream's cases: what the wire carries for a client that asks for a blob, and
// that changesets are read-only.
func TestGitBlobResources(t *testing.T) {
	requireGit(t)
	workspace := realDir(t, t.TempDir())
	initRepo(t, workspace)
	write(t, filepath.Join(workspace, "text.go"), "package x\n")
	if err := os.WriteFile(filepath.Join(workspace, "bin.dat"), []byte{0, 1, 2, 255}, 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(workspace, "noext"), "plain\n")
	git(t, workspace, "add", ".")
	git(t, workspace, "commit", "-qm", "base")
	f := startFixture(t, workspace, fixtureOptions{deleteFile: okDelete})
	session := f.createSession(obj{})
	f.attach(session)
	channel := changeset.URI(strings.TrimPrefix(session, "ahp-session:/"), changeset.LatestCommit)
	f.client.Subscribe(channel)
	state := f.readyState(channel)

	read := func(name, encoding string) ahptypes.ResourceReadResult {
		file := findFile(t, state, name)
		if file == nil {
			t.Fatalf("no %s in %v", name, fileNames(t, state))
		}
		var result ahptypes.ResourceReadResult
		f.client.Decode(f.client.Must("resourceRead", obj{"channel": wire.RootChannel, "uri": file.Edit.After.Content.Uri, "encoding": encoding}), &result)
		return result
	}
	if r := read("text.go", "utf-8"); r.Data != "package x\n" || r.Encoding != ahptypes.ContentEncodingUtf8 || r.ContentType == nil || *r.ContentType != "text/plain" {
		t.Fatalf("text.go = %+v", r)
	}
	if r := read("text.go", "base64"); r.Encoding != ahptypes.ContentEncodingBase64 || r.Data != base64.StdEncoding.EncodeToString([]byte("package x\n")) {
		t.Fatalf("text.go as base64 = %+v", r)
	}
	if r := read("bin.dat", "utf-8"); r.Encoding != ahptypes.ContentEncodingBase64 || r.Data != base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255}) || r.ContentType != nil {
		t.Fatalf("bin.dat = %+v", r)
	}
	if r := read("noext", "utf-8"); r.Data != "plain\n" || r.ContentType == nil || *r.ContentType != "text/plain" {
		t.Fatalf("noext = %+v", r)
	}

	uri := findFile(t, state, "text.go").Edit.After.Content.Uri
	f.client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": "git-blob:/x?!!"}, wire.CodeInvalidParams)
	// A reference to another session or a blob that is not there is not found.
	meta, _ := json.Marshal(obj{"sessionId": "nope", "oid": strings.Repeat("a", 40), "path": "text.go"})
	f.client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": "git-blob:/x?" + base64.RawURLEncoding.EncodeToString(meta)}, wire.CodeNotFound)
	meta, _ = json.Marshal(obj{"sessionId": strings.TrimPrefix(session, "ahp-session:/"), "oid": strings.Repeat("a", 40), "path": "text.go"})
	f.client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": "git-blob:/x?" + base64.RawURLEncoding.EncodeToString(meta)}, wire.CodeNotFound)
	meta, _ = json.Marshal(obj{"sessionId": strings.TrimPrefix(session, "ahp-session:/"), "oid": strings.Repeat("a", 40), "path": "../escape.txt"})
	f.client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": "git-blob:/x?" + base64.RawURLEncoding.EncodeToString(meta)}, wire.CodeNotFound)
	// Without the filesystem opt-in nothing but git blobs is served.
	f.client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": wire.PathToFileURI(filepath.Join(workspace, "text.go"))}, wire.CodeMethodNotFound)
	_ = uri

	// A client may not edit a changeset.
	n := len(f.client.Notifications())
	f.client.Dispatch(channel, obj{"type": "changeset/filesReviewChanged", "files": []string{"x"}, "reviewed": true})
	f.client.Ping()
	rejected := false
	for _, note := range f.client.Notifications()[n:] {
		if env, ok := note.Envelope(); ok && env.Channel == channel && env.RejectionReason != nil && *env.RejectionReason == "This host exposes read-only changesets" {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("a client action on a changeset must be rejected")
	}
}
