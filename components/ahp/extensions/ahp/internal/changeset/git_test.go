package changeset_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
	"github.com/MichaelKinsy/pigpen/ahp/internal/compose"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Behaviour of the git backend that upstream's cases only touch indirectly.
func TestGitChangesBackend(t *testing.T) {
	requireGit(t)
	root := realDir(t, t.TempDir())
	initRepo(t, root)
	write(t, filepath.Join(root, ".gitignore"), "*.log\nbuild/\n")
	write(t, filepath.Join(root, "tracked.txt"), "one\n")
	if err := os.WriteFile(filepath.Join(root, "tracked.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(root, "tracked.bin"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	appendTo(t, filepath.Join(root, "tracked.txt"), "two\nthree\n")
	write(t, filepath.Join(root, "ignored.log"), "ignored\n")
	if err := os.MkdirAll(filepath.Join(root, "build", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "build", "deep", "out.txt"), "artifact\n")
	write(t, filepath.Join(root, "we ird#name%.txt"), "a\nb\nc")
	write(t, filepath.Join(root, "empty.txt"), "")
	if err := os.WriteFile(filepath.Join(root, "untracked.bin"), []byte{0, 255}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nul.dat"), []byte("a\x00b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tracked.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks are not available")
	}
	if err := os.Mkdir(filepath.Join(root, "scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "scope", "in.txt"), "in\n")

	ctx := context.Background()
	backend := changeset.GitChanges{}
	ws, err := backend.Inspect(ctx, root)
	if err != nil || ws == nil {
		t.Fatalf("inspect = %+v, %v", ws, err)
	}
	if ws.Pathspec != "." || ws.Head == "" || ws.Parent != "" || ws.GitDirectory != filepath.Join(root, ".git") {
		t.Fatalf("workspace = %+v", ws)
	}

	files, err := backend.Compute(ctx, *ws, "sid", changeset.Uncommitted)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ahptypes.ChangesetFile{}
	for _, f := range files {
		byName[fileName(t, f)] = f
	}
	want := []string{"empty.txt", "in.txt", "nul.dat", "tracked.bin", "tracked.txt", "untracked.bin", "we ird#name%.txt"}
	if got := fileNames(t, &ahptypes.ChangesetState{Files: files}); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v (ignored files, symlinks and the build directory stay out)", got, want)
	}
	if weird := byName["we ird#name%.txt"]; weird.Id != "file://"+root+"/we%20ird%23name%25.txt" {
		t.Fatalf("id = %q", weird.Id)
	} else if added(weird) != 3 || *weird.Edit.Diff.Removed != 0 {
		t.Fatalf("an untracked file without a final newline has 3 lines, diff = %+v", weird.Edit.Diff)
	}
	if empty := byName["empty.txt"]; added(empty) != 0 {
		t.Fatalf("an empty untracked file has no lines, diff = %+v", empty.Edit.Diff)
	}
	if byName["untracked.bin"].Edit.Diff != nil || byName["tracked.bin"].Edit.Diff != nil || byName["nul.dat"].Edit.Diff != nil {
		t.Fatal("binary files have no line counts")
	}
	if d := byName["tracked.txt"].Edit.Diff; d == nil || *d.Added != 2 || *d.Removed != 0 {
		t.Fatalf("tracked.txt diff = %+v", d)
	}
	if byName["in.txt"].Edit.Before != nil || byName["in.txt"].Edit.After == nil {
		t.Fatalf("a new file has only an after side: %+v", byName["in.txt"].Edit)
	}
	if before := byName["tracked.txt"].Edit.Before; before == nil || !strings.HasPrefix(before.Content.Uri, "git-blob:") || before.Uri != "file://"+root+"/tracked.txt" {
		t.Fatalf("the baseline of an edit is a git blob of the working-tree file: %+v", before)
	}

	// Ignored paths (git lists an ignored directory whole, and ignored files singly) are what the
	// watcher must not recurse into.
	ignored, err := backend.IgnoredDirectories(ctx, *ws)
	if err != nil || !reflect.DeepEqual(ignored, []string{filepath.Join(root, "build"), filepath.Join(root, "ignored.log")}) {
		t.Fatalf("ignored directories = %v, %v", ignored, err)
	}

	// The working-directory subtree limits what is listed, whichever of its two guards applies.
	scoped, err := backend.Inspect(ctx, filepath.Join(root, "scope"))
	if err != nil || scoped == nil || scoped.Pathspec != "scope" {
		t.Fatalf("scoped = %+v, %v", scoped, err)
	}
	inside := func(ws changeset.Workspace) []string {
		files, err := backend.Compute(ctx, ws, "sid", changeset.Uncommitted)
		if err != nil {
			t.Fatal(err)
		}
		return fileNames(t, &ahptypes.ChangesetState{Files: files})
	}
	if got := inside(*scoped); !reflect.DeepEqual(got, []string{"in.txt"}) {
		t.Fatalf("scoped files = %v", got)
	}
	wide := *scoped
	wide.Pathspec = "."
	if got := inside(wide); !reflect.DeepEqual(got, []string{"in.txt"}) {
		t.Fatalf("the cwd filter must hold without the pathspec: %v", got)
	}
	narrow := *ws
	narrow.Pathspec = "scope"
	if got := inside(narrow); !reflect.DeepEqual(got, []string{"in.txt"}) {
		t.Fatalf("the pathspec must hold without the cwd filter: %v", got)
	}

	// A blob is readable from its reference and only from a path inside the subtree.
	blob := changeset.BlobRef{SessionID: "sid", OID: strings.TrimSpace(git(t, root, "rev-parse", "HEAD:tracked.txt")), Path: "tracked.txt"}
	data, err := backend.ReadBlob(ctx, *ws, blob)
	if err != nil || string(data) != "one\n" {
		t.Fatalf("blob = %q, %v", data, err)
	}
	if _, err := backend.ReadBlob(ctx, *scoped, blob); err == nil {
		t.Fatal("a blob outside the working-directory subtree must be refused")
	}
	if _, ok := backend.PathForBlob(*ws, changeset.BlobRef{Path: "../x"}); ok {
		t.Fatal("a path that leaves the repository must be refused")
	}
	for _, path := range []string{"a/../../x", "a/../tracked.txt", "./tracked.txt", "a//b", "/abs"} {
		if _, ok := backend.PathForBlob(*ws, changeset.BlobRef{Path: path}); ok {
			t.Fatalf("path %q must be refused", path)
		}
	}
	// An object id is a hash and nothing else: a revision expression would read any object.
	if _, err := backend.ReadBlob(ctx, *ws, changeset.BlobRef{SessionID: "sid", OID: "HEAD:tracked.txt", Path: "tracked.txt"}); err == nil {
		t.Fatal("a revision expression is not an object id")
	}
	if _, err := backend.ReadBlob(ctx, *ws, changeset.BlobRef{SessionID: "sid", OID: "not-an-oid", Path: "tracked.txt"}); err == nil {
		t.Fatal("a malformed object id must be refused")
	}

	// Latest Commit of a root commit lists every file of the commit.
	latest, err := backend.Compute(ctx, *ws, "sid", changeset.LatestCommit)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileNames(t, &ahptypes.ChangesetState{Files: latest}); !reflect.DeepEqual(got, []string{".gitignore", "tracked.bin", "tracked.txt"}) {
		t.Fatalf("latest commit of a root commit = %v", got)
	}
	for _, f := range latest {
		if f.Edit.Before != nil {
			t.Fatalf("a root commit adds files: %+v", f.Edit)
		}
		if !strings.HasPrefix(f.Id, "file://") || f.Edit.After == nil || !strings.HasPrefix(f.Edit.After.Uri, "git-blob:") {
			t.Fatalf("latest-commit files are identified by path and viewed as immutable blobs: %q %+v", f.Id, f.Edit)
		}
	}

	// Outside a work tree there is nothing to inspect.
	if plain, err := backend.Inspect(ctx, t.TempDir()); err != nil || plain != nil {
		t.Fatalf("a directory outside git = %+v, %v", plain, err)
	}
}

func TestGitBlobURIs(t *testing.T) {
	oid := strings.Repeat("a", 40)
	encode := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	good := "git-blob:/x/y.txt?" + encode(map[string]any{"sessionId": "s", "oid": oid, "path": "y.txt"})
	if ref, ok := changeset.ParseBlobURI(good); !ok || ref != (changeset.BlobRef{SessionID: "s", OID: oid, Path: "y.txt"}) {
		t.Fatalf("parse = %+v, %v", ref, ok)
	}
	if _, ok := changeset.ParseBlobURI("git-blob:/x?" + encode(map[string]any{"sessionId": "s", "oid": strings.Repeat("a", 64), "path": ""})); !ok {
		t.Fatal("a sha-256 object id with an empty path is well formed")
	}
	for name, uri := range map[string]string{
		"no query":      "git-blob:/x/y.txt",
		"empty query":   "git-blob:/x?",
		"fragment":      good + "#frag",
		"not base64url": "git-blob:/x?a+b",
		"other scheme":  "file:///x?" + encode(map[string]any{"sessionId": "s", "oid": oid, "path": "p"}),
		"not json":      "git-blob:/x?" + base64.RawURLEncoding.EncodeToString([]byte("nope")),
		"short oid":     "git-blob:/x?" + encode(map[string]any{"sessionId": "s", "oid": "abc", "path": "p"}),
		"upper oid":     "git-blob:/x?" + encode(map[string]any{"sessionId": "s", "oid": strings.Repeat("A", 40), "path": "p"}),
		"empty session": "git-blob:/x?" + encode(map[string]any{"sessionId": "", "oid": oid, "path": "p"}),
		"no session":    "git-blob:/x?" + encode(map[string]any{"oid": oid, "path": "p"}),
		"no path":       "git-blob:/x?" + encode(map[string]any{"sessionId": "s", "oid": oid}),
		"numeric path":  "git-blob:/x?" + encode(map[string]any{"sessionId": "s", "oid": oid, "path": 1}),
	} {
		if ref, ok := changeset.ParseBlobURI(uri); ok {
			t.Errorf("%s: parsed as %+v", name, ref)
		}
	}
}

// The remote filesystem policy governs which working directories may be inspected, and its roots
// confine the blobs, when the filesystem is opted in.
func TestGitChangesetsFollowTheFilesystemPolicy(t *testing.T) {
	requireGit(t)
	repo := realDir(t, t.TempDir())
	initRepo(t, repo)
	write(t, filepath.Join(repo, "a.txt"), "a\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "base")
	write(t, filepath.Join(repo, "b.txt"), "b\n")

	build := func(roots []string) (*compose.Built, *testkit.Client) {
		root := t.TempDir()
		built, err := compose.Build(compose.Options{
			ServerInfo: &ahptypes.Implementation{Name: "pi-ahp"}, WorkingDirectory: repo, SessionRoot: root,
			CreateSessionManager: pi.PersistentStorage(filepath.Join(root, "created")),
			CreateBackend:        func(*pi.LiveSession) (pi.Backend, error) { return nullBackend{}, nil },
			Filesystem:           &compose.FilesystemOptions{Roots: roots},
			TuneChangesets:       func(c *changeset.Options) { c.PollInterval, c.Debounce = 20*time.Millisecond, 5*time.Millisecond },
		})
		if err != nil {
			t.Fatal(err)
		}
		client := testkit.Connect(t, built.Host)
		client.Initialize("policy-client", nil)
		t.Cleanup(func() { client.Close(); built.Close() })
		return built, client
	}

	inside, insideClient := build([]string{repo})
	f := &fixture{t: t, built: inside, client: insideClient, workspace: repo}
	session := f.createSession(obj{})
	eventually(t, "the catalogue inside the filesystem roots", func() bool { return len(f.session(session).Changesets) == 2 })

	outside, outsideClient := build([]string{t.TempDir()})
	g := &fixture{t: t, built: outside, client: outsideClient, workspace: repo}
	other := g.createSession(obj{})
	g.attach(other)
	if got := g.session(other).Changesets; got != nil {
		t.Fatalf("a working directory outside the filesystem roots gets no changesets, got %+v", got)
	}
	// And so nothing is served for it, even for a reference that names its blob.
	oid := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD:a.txt"))
	meta, _ := json.Marshal(map[string]any{"sessionId": strings.TrimPrefix(other, "ahp-session:/"), "oid": oid, "path": "a.txt"})
	g.client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": "git-blob:/x?" + base64.RawURLEncoding.EncodeToString(meta)}, wire.CodeNotFound)
}

// A blob is authorized by the path it names: once a directory of the work tree is replaced by a
// link out of the filesystem roots, the blobs that were under it are no longer readable.
func TestGitBlobsAreAuthorizedByTheirPath(t *testing.T) {
	requireGit(t)
	repo := realDir(t, t.TempDir())
	initRepo(t, repo)
	if err := os.Mkdir(filepath.Join(repo, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "d", "f.txt"), "one\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "base")
	appendTo(t, filepath.Join(repo, "d", "f.txt"), "two\n")
	git(t, repo, "commit", "-qam", "edit")

	root := t.TempDir()
	built, err := compose.Build(compose.Options{
		ServerInfo: &ahptypes.Implementation{Name: "pi-ahp"}, WorkingDirectory: repo, SessionRoot: root,
		CreateSessionManager: pi.PersistentStorage(filepath.Join(root, "created")),
		CreateBackend:        func(*pi.LiveSession) (pi.Backend, error) { return nullBackend{}, nil },
		Filesystem:           &compose.FilesystemOptions{Roots: []string{repo}},
		TuneChangesets:       func(c *changeset.Options) { c.PollInterval, c.Debounce = 20*time.Millisecond, 5*time.Millisecond },
	})
	if err != nil {
		t.Fatal(err)
	}
	client := testkit.Connect(t, built.Host)
	client.Initialize("policy-client", nil)
	t.Cleanup(func() { client.Close(); built.Close() })
	f := &fixture{t: t, built: built, client: client, workspace: repo}
	session := f.createSession(obj{})
	channel := changeset.URI(strings.TrimPrefix(session, "ahp-session:/"), changeset.LatestCommit)
	eventually(t, "the catalogue", func() bool { return len(f.session(session).Changesets) == 2 })
	client.Subscribe(channel)
	state := f.readyState(channel)
	file := findFile(t, state, "f.txt")
	if file == nil {
		t.Fatalf("files = %v", fileNames(t, state))
	}
	uri := file.Edit.After.Content.Uri
	if got := f.readBlob(uri); got.Data != "one\ntwo\n" {
		t.Fatalf("blob = %q", got.Data)
	}
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(repo, "d")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "d")); err != nil {
		t.Skip("symlinks are not available")
	}
	client.ExpectError("resourceRead", obj{"channel": wire.RootChannel, "uri": uri, "encoding": "utf-8"}, wire.CodePermissionDenied)
}

// The Latest Commit channel exists only when the session advertises it: a repository without a
// commit has no such changeset to subscribe to.
func TestUnadvertisedChangesetsAreNotFound(t *testing.T) {
	requireGit(t)
	empty := realDir(t, t.TempDir())
	git(t, empty, "init", "-q")
	write(t, filepath.Join(empty, "first.txt"), "first\n")
	f := startFixture(t, empty, fixtureOptions{deleteFile: okDelete})
	session := f.createSession(obj{})
	f.attach(session)
	id := strings.TrimPrefix(session, "ahp-session:/")
	f.client.ExpectError("subscribe", obj{"channel": changeset.URI(id, changeset.LatestCommit)}, wire.CodeNotFound)
	if f.built.Host.Store().Has(changeset.URI(id, changeset.LatestCommit)) {
		t.Fatal("an unadvertised changeset must not be created")
	}
	f.client.Subscribe(changeset.URI(id, changeset.Uncommitted))
}
