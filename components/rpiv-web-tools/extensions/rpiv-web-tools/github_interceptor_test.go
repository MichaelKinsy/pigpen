package rpiv_web_tools

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The twins of providers/interceptors/github.test.ts. The original mocks node:child_process.execFile;
// here a fakeExec stands in for the host command runner the interceptor calls (hostexec.go), and its
// handler gets the same command and argument list the original's mock gets.

type execCall struct {
	Name string
	Args []string
	Opts execOptions
}

type fakeExec struct {
	mu    sync.Mutex
	calls []execCall
	fn    func(name string, args []string) (string, error)
}

func (f *fakeExec) run(_ context.Context, name string, args []string, o execOptions) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, execCall{name, append([]string(nil), args...), o})
	f.mu.Unlock()
	return f.fn(name, args)
}

// count is the number of calls whose arguments the predicate accepts.
func (f *fakeExec) count(pred func(execCall) bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if pred(c) {
			n++
		}
	}
	return n
}

func (f *fakeExec) all() []execCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execCall(nil), f.calls...)
}

// ghExec returns a context whose host commands go to fn.
func ghExec(t *testing.T, fn func(name string, args []string) (string, error)) (context.Context, *fakeExec) {
	t.Helper()
	f := &fakeExec{fn: fn}
	return withExec(context.Background(), f.run), f
}

func ghMakeUnavailable(string, []string) (string, error) {
	return "", errors.New("gh: not found")
}

func ghMakeAvailableAPIsFail(_ string, args []string) (string, error) {
	if args[0] == "--version" {
		return "gh version 2.0.0", nil
	}
	return "", errors.New("not found")
}

// ghAPIMock answers `gh api` by the first response whose key the "path|jq filter" string contains, in the
// order the original lists them.
func ghAPIMock(responses ...[2]string) func(string, []string) (string, error) {
	return func(_ string, args []string) (string, error) {
		if args[0] == "--version" {
			return "gh version 2.0.0", nil
		}
		if args[0] != "api" {
			return "", errors.New("unexpected command")
		}
		jq := ""
		if len(args) > 3 {
			jq = args[3]
		}
		routeKey := args[1] + "|" + jq
		for _, r := range responses {
			if strings.Contains(routeKey, r[0]) {
				return r[1], nil
			}
		}
		return "", errors.New("not found")
	}
}

func ghB64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func ghHas(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%q does not contain %q", got, want)
	}
}

func ghHasNot(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("%q contains %q", got, want)
	}
}

// ghMake is the original's make(): an enabled interceptor with the default thresholds, and a clone
// directory of its own so that no test reads another's clone.
func ghMake(t *testing.T, opts githubUserOptions) *githubInterceptor {
	t.Helper()
	if opts.Enabled == nil {
		opts.Enabled = new(true)
	}
	if opts.ClonePath == nil {
		opts.ClonePath = new(t.TempDir())
	}
	ghHintWriter = discardWriter{}
	return newGitHubInterceptor(opts)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func ghResolved(path string) *string { return &path }

// ghIntercept is intercept(url, { raw: false }).
func ghIntercept(ctx context.Context, g *githubInterceptor, url string) *fetchResponse {
	return g.intercept(ctx, url, false)
}

func TestGitHubParseURL(t *testing.T) {
	const f = "providers/interceptors/github"
	tw(t, f, "returns null for non-GitHub URLs", func(t *testing.T) {
		eq(t, parseGitHubURL("https://example.com/foo"), (*githubURLInfo)(nil))
		eq(t, parseGitHubURL("https://gitlab.com/owner/repo"), (*githubURLInfo)(nil))
	})
	tw(t, f, "returns null for invalid URLs", func(t *testing.T) {
		eq(t, parseGitHubURL("not a url"), (*githubURLInfo)(nil))
		eq(t, parseGitHubURL(""), (*githubURLInfo)(nil))
	})
	tw(t, f, "returns null when path has fewer than 2 segments", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/"), (*githubURLInfo)(nil))
		eq(t, parseGitHubURL("https://github.com/owner"), (*githubURLInfo)(nil))
	})
	tw(t, f, "parses a root repo URL", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/owner/repo"), &githubURLInfo{Owner: "owner", Repo: "repo", RefIsFullSha: false, Type: "root"})
	})
	tw(t, f, "strips .git suffix from repo name", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/owner/repo.git").Repo, "repo")
	})
	tw(t, f, "handles www.github.com hostname", func(t *testing.T) {
		eq(t, parseGitHubURL("https://www.github.com/owner/repo").Type, "root")
	})
	for _, segment := range []string{"issues", "pull", "pulls", "discussions", "releases", "wiki", "actions", "settings", "security", "commits", "tags", "branches"} {
		// it.each(...)("returns null for NON_CODE_SEGMENT: %s"): the ledger lists no title for it.each cases.
		t.Run("returns null for NON_CODE_SEGMENT: "+segment, func(t *testing.T) {
			eq(t, parseGitHubURL("https://github.com/owner/repo/"+segment), (*githubURLInfo)(nil))
		})
	}
	tw(t, f, "returns null when action is not blob or tree", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/owner/repo/compare/main...feat"), (*githubURLInfo)(nil))
	})
	tw(t, f, "returns null when blob/tree has no ref segment", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/owner/repo/blob"), (*githubURLInfo)(nil))
	})
	tw(t, f, "parses a blob URL", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/owner/repo/blob/main/src/file.ts"),
			&githubURLInfo{Owner: "owner", Repo: "repo", Ref: "main", Type: "blob", Path: "src/file.ts", RefIsFullSha: false})
	})
	tw(t, f, "detects full-SHA ref", func(t *testing.T) {
		sha := strings.Repeat("a", 40)
		info := parseGitHubURL("https://github.com/owner/repo/blob/" + sha + "/file.ts")
		eq(t, info.RefIsFullSha, true)
		eq(t, info.Ref, sha)
	})
	tw(t, f, "parses a tree URL with path", func(t *testing.T) {
		info := parseGitHubURL("https://github.com/owner/repo/tree/main/src")
		eq(t, [3]string{info.Type, info.Ref, info.Path}, [3]string{"tree", "main", "src"})
	})
	tw(t, f, "parses a tree URL with empty path (repo root tree)", func(t *testing.T) {
		info := parseGitHubURL("https://github.com/owner/repo/tree/main")
		eq(t, info.Path, "")
		eq(t, info.Type, "tree")
	})
	tw(t, f, "decodes percent-encoded path segments", func(t *testing.T) {
		eq(t, parseGitHubURL("https://github.com/owner/repo/blob/main/path%20with%20spaces/file.ts").Path, "path with spaces/file.ts")
	})
}

func TestGitHubResolveOptions(t *testing.T) {
	const f = "providers/interceptors/github"
	enabled := func(v bool) resolvedGitHubOptions {
		d := githubDefaults
		d.Enabled = v
		return d
	}
	tw(t, f, "absent user config + no consumer default → disabled", func(t *testing.T) {
		eq(t, resolveGitHubOptions(nil, nil), githubDefaults)
	})
	tw(t, f, "absent user config + consumer:true → enabled with defaults", func(t *testing.T) {
		eq(t, resolveGitHubOptions(nil, new(true)), enabled(true))
	})
	tw(t, f, "absent user config + consumer:false → disabled", func(t *testing.T) {
		eq(t, resolveGitHubOptions(nil, new(false)), githubDefaults)
	})
	tw(t, f, "user:false beats consumer:true → disabled (explicit user override wins)", func(t *testing.T) {
		eq(t, resolveGitHubOptions(false, new(true)), enabled(false))
	})
	tw(t, f, "user:true beats consumer:false → enabled", func(t *testing.T) {
		eq(t, resolveGitHubOptions(true, new(false)), enabled(true))
	})
	tw(t, f, "user object form implies opt-in regardless of consumer default", func(t *testing.T) {
		r := resolveGitHubOptions(githubUserOptions{MaxRepoSizeMB: new(1000.0)}, new(false))
		eq(t, r.Enabled, true)
		eq(t, r.MaxRepoSizeMB, 1000.0)
		eq(t, r.CloneTimeoutSeconds, githubDefaults.CloneTimeoutSeconds)
		eq(t, r.ClonePath, githubDefaults.ClonePath)
	})
	tw(t, f, `user object with explicit "enabled": false honors it`, func(t *testing.T) {
		r := resolveGitHubOptions(githubUserOptions{Enabled: new(false), MaxRepoSizeMB: new(1000.0)}, new(true))
		eq(t, r.Enabled, false)
		eq(t, r.MaxRepoSizeMB, 1000.0)
	})
	tw(t, f, "user object overrides every default field", func(t *testing.T) {
		r := resolveGitHubOptions(githubUserOptions{MaxRepoSizeMB: new(1.0), CloneTimeoutSeconds: new(2.0), ClonePath: new("/x")}, nil)
		eq(t, r, resolvedGitHubOptions{Enabled: true, MaxRepoSizeMB: 1, CloneTimeoutSeconds: 2, ClonePath: "/x"})
	})
}

func TestGitHubTokenEnvVar(t *testing.T) {
	tw(t, "providers/interceptors/github", "is GITHUB_TOKEN", func(t *testing.T) {
		eq(t, githubTokenEnvVar, "GITHUB_TOKEN")
	})
}

func TestGitHubFastNullReturns(t *testing.T) {
	const f = "providers/interceptors/github"
	tw(t, f, "returns null for non-GitHub URL", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://example.com"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null for NON_CODE_SEGMENTS URLs", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{})
		eq(t, ghIntercept(ctx, g, "https://github.com/owner/repo/issues"), (*fetchResponse)(nil))
		eq(t, ghIntercept(ctx, g, "https://github.com/owner/repo/pulls"), (*fetchResponse)(nil))
		eq(t, ghIntercept(ctx, g, "https://github.com/owner/repo/actions"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null when signal is already aborted", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null when interceptor.enabled is false", func(t *testing.T) {
		ctx, fe := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{Enabled: new(false)})
		eq(t, ghIntercept(ctx, g, "https://github.com/owner/repo"), (*fetchResponse)(nil))
		eq(t, len(fe.all()), 0)
	})
	tw(t, f, "returns null for full-SHA ref (gh unavailable → API path → getDefaultBranch null)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		sha := strings.Repeat("a", 40)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/"+sha+"/file.ts"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null for root URL when gh unavailable and git clone fails", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null for blob URL when no gh and no clone", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/main/file.ts"), (*fetchResponse)(nil))
	})
}

func TestGitHubAvailableAPIsFail(t *testing.T) {
	const f = "providers/interceptors/github"
	tw(t, f, "returns null for root URL (size=null, clone fails, API=null)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeAvailableAPIsFail)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null for blob URL (clone fails, API=null)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeAvailableAPIsFail)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/main/file.ts"), (*fetchResponse)(nil))
	})
}

func TestGitHubReset(t *testing.T) {
	const f = "providers/interceptors/github"
	tw(t, f, "can be called on an empty cache without throwing", func(t *testing.T) {
		g := ghMake(t, githubUserOptions{})
		g.reset()
		g.reset()
	})
	tw(t, f, "resets ghAvailable so checkGhAvailable re-probes on next call", func(t *testing.T) {
		g := ghMake(t, githubUserOptions{})
		mode := ghMakeUnavailable
		fe := &fakeExec{fn: func(n string, a []string) (string, error) { return mode(n, a) }}
		ctx := withExec(context.Background(), fe.run)
		ghIntercept(ctx, g, "https://github.com/owner/repo/issues")
		g.reset()
		mode = ghMakeAvailableAPIsFail
		ghIntercept(ctx, g, "https://github.com/owner/repo/issues")
		// No assertion on the return value in the original: no throw, and the re-probe path is hit.
	})
}

func TestGitHubCloneContent(t *testing.T) {
	const f = "providers/interceptors/github"
	// Each case: gh unavailable, a temp directory as the clone, injected through the clone cache.
	setup := func(t *testing.T) (context.Context, string) {
		t.Helper()
		ctx, _ := ghExec(t, ghMakeUnavailable)
		return ctx, t.TempDir()
	}
	write := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seeded := func(t *testing.T, key, dir string) *githubInterceptor {
		g := ghMake(t, githubUserOptions{})
		g.seedCache(key, dir, ghResolved(dir))
		return g
	}
	tw(t, f, "root: returns file tree + README", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "README.md"), "# My Repo\nHello world.")
		write(t, filepath.Join(dir, "index.ts"), "export const x = 1;")
		write(t, filepath.Join(dir, "src", "main.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo", dir), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		for _, s := range []string{"Repository cloned to:", "## Structure", "index.ts", "src/", "## README.md", "My Repo"} {
			ghHas(t, r.Text, s)
		}
		eq(t, r.Title, "owner/repo")
		eq(t, r.ContentType, "text/plain")
	})
	tw(t, f, "root: works without a README", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "index.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo", dir), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "## Structure")
		ghHasNot(t, r.Text, "## README.md")
	})
	tw(t, f, "root: truncates README at 8K chars", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "README.md"), strings.Repeat("x", 9000))
		r := ghIntercept(ctx, seeded(t, "owner/repo", dir), "https://github.com/owner/repo")
		ghHas(t, r.Text, "[README truncated at 8K chars]")
	})
	tw(t, f, "blob: handles unreadable file (readFileSync catch path) via chmod 000", func(t *testing.T) {
		ctx, dir := setup(t)
		file := filepath.Join(dir, "unreadable.ts")
		write(t, file, "const x = 1;")
		os.Chmod(file, 0)
		defer os.Chmod(file, 0o644)
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/unreadable.ts")
		if r == nil {
			t.Fatal("null")
		}
	})
	tw(t, f, "buildTree truncation at MAX_TREE_ENTRIES (>200 files)", func(t *testing.T) {
		ctx, dir := setup(t)
		for i := range 201 {
			write(t, filepath.Join(dir, "file"+pad3(i)+".ts"), "")
		}
		r := ghIntercept(ctx, seeded(t, "owner/repo", dir), "https://github.com/owner/repo")
		ghHas(t, r.Text, "truncated at 200 entries")
	})
	tw(t, f, "root: formatFileSize shows bytes for tiny files and KB for medium files", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "subdir", "tiny.ts"), "x")
		write(t, filepath.Join(dir, "subdir", "medium.ts"), strings.Repeat("x", 2048))
		write(t, filepath.Join(dir, "subdir", "large.ts"), strings.Repeat("x", 1_100_000))
		r := ghIntercept(ctx, seeded(t, "owner/repo@v", dir), "https://github.com/owner/repo/tree/v/subdir")
		if r == nil {
			t.Fatal("null")
		}
		if !regexp.MustCompile(`\d+ B|\d+\.\d+ KB|\d+\.\d+ MB`).MatchString(r.Text) {
			t.Fatal(r.Text)
		}
	})
	tw(t, f, "root: buildTree marks outside-repo symlink as skipped", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "normal.ts"), "")
		if err := os.Symlink(os.TempDir(), filepath.Join(dir, "escape-link")); err != nil {
			t.Skip("symlink creation fails here: " + err.Error())
		}
		r := ghIntercept(ctx, seeded(t, "owner/repo", dir), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "normal.ts")
		ghHas(t, r.Text, "outside repo skipped")
	})
	tw(t, f, "root: skips NOISE_DIRS in tree output", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "node_modules", "pkg.js"), "")
		write(t, filepath.Join(dir, "index.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo", dir), "https://github.com/owner/repo")
		ghHas(t, r.Text, "node_modules/  [skipped]")
		ghHasNot(t, r.Text, "pkg.js")
	})
	tw(t, f, "blob: returns file content", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "file.ts"), "export const answer = 42;")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/file.ts")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "## file.ts")
		ghHas(t, r.Text, "export const answer = 42;")
		eq(t, r.Title, "owner/repo - file.ts")
	})
	tw(t, f, "blob: returns binary message for known binary extension (.png)", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "image.png"), "\x89PNG")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/image.png")
		ghHas(t, r.Text, "Binary file")
		ghHas(t, r.Text, "png")
	})
	tw(t, f, "blob: returns binary message for file with null bytes", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "data.bin"), strings.Repeat("\x00", 16))
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/data.bin")
		ghHas(t, r.Text, "Binary file")
	})
	tw(t, f, "blob: falls back to repo root when file not found in clone", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "README.md"), "# Repo")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/missing.ts")
		ghHas(t, r.Text, "not found in clone")
		ghHas(t, r.Text, "## Structure")
	})
	tw(t, f, "blob: shows dir listing when path points to a directory", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "mydir", "file.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/mydir")
		ghHas(t, r.Text, "file.ts")
	})
	tw(t, f, "blob: truncates large files at 100K chars", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "big.ts"), strings.Repeat("x", 110_000))
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/blob/main/big.ts")
		ghHas(t, r.Text, "[File truncated at 100K chars")
	})
	tw(t, f, "tree: returns directory listing for existing subdir", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "src", "a.ts"), "")
		write(t, filepath.Join(dir, "src", "b.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/tree/main/src")
		ghHas(t, r.Text, "## src")
		ghHas(t, r.Text, "a.ts")
		ghHas(t, r.Text, "b.ts")
	})
	tw(t, f, "tree: falls back to repo root when subdir not found", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "index.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/tree/main/missing-dir")
		ghHas(t, r.Text, "not found in clone")
		ghHas(t, r.Text, "## Structure")
	})
	tw(t, f, "tree: handles empty path (root tree URL)", func(t *testing.T) {
		ctx, dir := setup(t)
		write(t, filepath.Join(dir, "index.ts"), "")
		r := ghIntercept(ctx, seeded(t, "owner/repo@main", dir), "https://github.com/owner/repo/tree/main")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "index.ts")
	})
	tw(t, f, "falls back to fetchViaApi when clonePromise resolves null (gh unavailable → null)", func(t *testing.T) {
		ctx, dir := setup(t)
		g := ghMake(t, githubUserOptions{})
		g.seedCache("owner/repo", dir, nil)
		eq(t, ghIntercept(ctx, g, "https://github.com/owner/repo"), (*fetchResponse)(nil))
	})
}

func pad3(i int) string {
	s := "00" + itoa(i)
	return s[len(s)-3:]
}

func TestGitHubViaAPIAndClone(t *testing.T) {
	const f = "providers/interceptors/github"
	tw(t, f, "fetches a blob file via API (getDefaultBranch + fetchFileViaApi)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{".content", ghB64("export const x = 1;\n")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/main/src/file.ts")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "export const x = 1;")
		eq(t, r.Title, "owner/repo - src/file.ts")
	})
	tw(t, f, "fetches repo root via API (getDefaultBranch + fetchTreeViaApi + fetchReadmeViaApi)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock(
			[2]string{".default_branch", "main"},
			[2]string{".tree[].path", "src/index.ts\nREADME.md"},
			[2]string{"repos/owner/repo/readme", ghB64("# Hello World")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "## Structure")
		ghHas(t, r.Text, "src/index.ts")
		ghHas(t, r.Text, "## README.md")
		ghHas(t, r.Text, "Hello World")
	})
	tw(t, f, "returns tree-only view when readme API call fails", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{".tree[].path", "index.ts\nlib.ts"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "## Structure")
		ghHasNot(t, r.Text, "## README.md")
	})
	tw(t, f, "returns null when tree and readme both fail (no content)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}))
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
	})
	tw(t, f, "returns null when file content API fails", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}))
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/main/file.ts"), (*fetchResponse)(nil))
	})
	tw(t, f, "truncates file content at 100K chars via API", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{".content", ghB64(strings.Repeat("x", 110_000))}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/main/big.ts")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "[File truncated at 100K chars]")
	})
	tw(t, f, "uses sizeNote when repo is oversized (API-only fallback)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock(
			[2]string{".size", "400000"},
			[2]string{".default_branch", "main"},
			[2]string{".tree[].path", "file.ts"},
			[2]string{"repos/owner/repo/readme", ghB64("# Big Repo")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "Repository is")
		ghHas(t, r.Text, "threshold")
	})
	tw(t, f, "uses provided ref instead of fetching default branch (blob with explicit ref)", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".content", ghB64("const v = 2;")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/feature-branch/file.ts")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "const v = 2;")
	})
	tw(t, f, "clones repo via gh when gh available and size below threshold", func(t *testing.T) {
		ctx, _ := ghExec(t, func(name string, args []string) (string, error) {
			switch {
			case args[0] == "--version":
				return "gh 2.0.0", nil
			case args[0] == "api":
				switch args[3] {
				case ".size":
					return "100", nil
				case ".default_branch":
					return "main", nil
				}
				return "", errors.New("unexpected api")
			case args[0] == "repo" && args[1] == "clone":
				target := args[3]
				os.MkdirAll(target, 0o755)
				os.WriteFile(filepath.Join(target, "README.md"), []byte("# Cloned!"), 0o644)
				return "", nil
			}
			return "", errors.New("unexpected")
		})
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "Repository cloned to:")
		ghHas(t, r.Text, "README.md")
	})
	tw(t, f, "git fallback clone when gh unavailable (showGhHint + git clone path)", func(t *testing.T) {
		var hint strings.Builder
		ctx, _ := ghExec(t, func(name string, args []string) (string, error) {
			switch args[0] {
			case "--version":
				return "", errors.New("not found")
			case "clone":
				target := args[len(args)-1]
				os.MkdirAll(target, 0o755)
				os.WriteFile(filepath.Join(target, "index.ts"), []byte("export default 1;"), 0o644)
				return "", nil
			}
			return "", errors.New("unexpected")
		})
		g := ghMake(t, githubUserOptions{})
		ghHintWriter = &hint
		defer func() { ghHintWriter = discardWriter{} }()
		r := ghIntercept(ctx, g, "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "Repository cloned to:")
		eq(t, hint.String(), "[rpiv-web-tools] Install `gh` CLI for better GitHub repo access including private repos.\n")
	})
	tw(t, f, "checkRepoSize: returns null when gh unavailable", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		_, ok := ghMake(t, githubUserOptions{}).checkRepoSize(ctx, "owner", "repo")
		eq(t, ok, false)
	})
	tw(t, f, "checkRepoSize: returns null on gh api error", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeAvailableAPIsFail)
		_, ok := ghMake(t, githubUserOptions{}).checkRepoSize(ctx, "owner", "repo")
		eq(t, ok, false)
	})
	tw(t, f, "checkRepoSize: returns numeric KB value", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".size", "12345"}))
		size, ok := ghMake(t, githubUserOptions{}).checkRepoSize(ctx, "owner", "repo")
		eq(t, ok, true)
		eq(t, size, 12345.0)
	})
	tw(t, f, "checkRepoSize: returns null for non-numeric output", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".size", "not-a-number"}))
		_, ok := ghMake(t, githubUserOptions{}).checkRepoSize(ctx, "owner", "repo")
		eq(t, ok, false)
	})
	tw(t, f, "checkGhAvailable: returns false when execFile fails", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeUnavailable)
		eq(t, ghMake(t, githubUserOptions{}).checkGhAvailable(ctx), false)
	})
	tw(t, f, "checkGhAvailable: returns true when execFile succeeds", func(t *testing.T) {
		ctx, _ := ghExec(t, ghMakeAvailableAPIsFail)
		eq(t, ghMake(t, githubUserOptions{}).checkGhAvailable(ctx), true)
	})
	tw(t, f, "checkGhAvailable: caches the result (second call returns same value)", func(t *testing.T) {
		mode := ghMakeAvailableAPIsFail
		fe := &fakeExec{fn: func(n string, a []string) (string, error) { return mode(n, a) }}
		ctx := withExec(context.Background(), fe.run)
		g := ghMake(t, githubUserOptions{})
		g.checkGhAvailable(ctx)
		mode = ghMakeUnavailable
		eq(t, g.checkGhAvailable(ctx), true)
		eq(t, len(fe.all()), 1)
	})
	tw(t, f, "signal abort between size check and clone start returns null", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sizeCheckDone := false
		fe := &fakeExec{fn: func(name string, args []string) (string, error) {
			if args[0] == "--version" {
				return "gh 2.0.0", nil
			}
			if args[0] == "api" && args[3] == ".size" {
				cancel()
				sizeCheckDone = true
				return "100", nil
			}
			return "", errors.New("unexpected")
		}}
		ctx = withExec(ctx, fe.run)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
		eq(t, sizeCheckDone, true)
	})
	tw(t, f, "forceClone=true skips size check and attempts clone directly", func(t *testing.T) {
		ctx, fe := ghExec(t, ghMakeAvailableAPIsFail)
		r := ghMake(t, githubUserOptions{}).fetchGitHub(ctx, "https://github.com/owner/repo", true)
		eq(t, r, (*fetchResponse)(nil))
		eq(t, fe.count(func(c execCall) bool { return len(c.Args) > 3 && c.Args[3] == ".size" }), 0)
	})
	tw(t, f, "signal abort after size check returns null", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sizeCallCount := 0
		fe := &fakeExec{fn: func(name string, args []string) (string, error) {
			if args[0] == "--version" {
				return "gh 2.0.0", nil
			}
			if args[0] == "api" && args[3] == ".size" {
				sizeCallCount++
				cancel()
				return "100", nil
			}
			return "", errors.New("unexpected")
		}}
		ctx = withExec(ctx, fe.run)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
		eq(t, sizeCallCount, 1)
	})
	tw(t, f, "readReadme falls back through candidate list (README without .md)", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "README"), []byte("Bare README content"), 0o644)
		ctx, _ := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{})
		g.seedCache("owner/repo", dir, ghResolved(dir))
		r := ghIntercept(ctx, g, "https://github.com/owner/repo")
		ghHas(t, r.Text, "Bare README content")
	})
	tw(t, f, "buildDirListing: handles outside-repo symlink gracefully", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "normal.ts"), nil, 0o644)
		if err := os.Symlink(os.TempDir(), filepath.Join(dir, "escape-link")); err != nil {
			t.Skip("symlink creation fails here: " + err.Error())
		}
		ctx, _ := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{})
		g.seedCache("owner/repo@main", dir, ghResolved(dir))
		r := ghIntercept(ctx, g, "https://github.com/owner/repo/tree/main")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "normal.ts")
	})
	tw(t, f, "abort after successful clone returns null and removes cache entry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cloneCallCount := 0
		fe := &fakeExec{fn: func(name string, args []string) (string, error) {
			switch {
			case args[0] == "--version":
				return "gh 2.0.0", nil
			case args[0] == "api" && args[3] == ".size":
				return "", errors.New("size unavailable")
			case args[0] == "repo" && args[1] == "clone":
				cloneCallCount++
				os.MkdirAll(args[3], 0o755)
				cancel()
				return "", nil
			}
			return "", errors.New("unexpected")
		}}
		ctx = withExec(ctx, fe.run)
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo"), (*fetchResponse)(nil))
		eq(t, cloneCallCount, 1)
	})
	tw(t, f, "awaitCachedClone: signal aborted before clone promise resolves returns null", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "index.ts"), nil, 0o644)
		ctx, _ := ghExec(t, ghMakeUnavailable)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		g := ghMake(t, githubUserOptions{})
		g.seedCache("owner/repo", dir, ghResolved(dir))
		eq(t, ghIntercept(ctx, g, "https://github.com/owner/repo"), (*fetchResponse)(nil))
	})
	tw(t, f, "concurrent clone: second intercept call reuses existing cache entry", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "index.ts"), []byte("const x = 1;"), 0o644)
		ctx, _ := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{})
		g.seedCache("owner/repo", dir, ghResolved(dir))
		r := ghIntercept(ctx, g, "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "index.ts")
	})
	tw(t, f, "SHA URL with sizeNote: proceeds to fetchViaApi with commit SHA note", func(t *testing.T) {
		sha := strings.Repeat("b", 40)
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{".content", ghB64("const sha = true;")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/owner/repo/blob/"+sha+"/file.ts")
		if r == nil {
			t.Fatal("null")
		}
		ghHas(t, r.Text, "Note: Commit SHA URLs use the GitHub API")
		ghHas(t, r.Text, "const sha = true;")
	})
	tw(t, f, "maxRepoSizeMB override threshold is honored", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock(
			[2]string{".size", "400000"},
			[2]string{".default_branch", "main"},
			[2]string{".tree[].path", "file.ts"},
			[2]string{"repos/owner/repo/readme", ghB64("# Big Repo")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{MaxRepoSizeMB: new(50.0)}), "https://github.com/owner/repo")
		if r == nil {
			t.Fatal("null")
		}
		if !regexp.MustCompile(`threshold: 50`).MatchString(r.Text) {
			t.Fatal(r.Text)
		}
	})
}
