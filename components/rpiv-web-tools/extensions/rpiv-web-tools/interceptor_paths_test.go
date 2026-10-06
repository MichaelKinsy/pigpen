// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fGH = "providers/interceptors/github"

// b64 is the API's base64 payload helper.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// ghInterceptor builds an interceptor over a fake runner with the production defaults, so a twin states only what it
// changes. upstream: the interceptor construction in each upstream case.
func ghInterceptor(runner githubRunner, options resolvedGitHubOptions) *gitHubInterceptor {
	if options.ClonePath == "" {
		options.ClonePath = os.TempDir()
	}
	if options.MaxRepoSizeMB == 0 {
		options.MaxRepoSizeMB = 350
	}
	if options.CloneTimeoutSeconds == 0 {
		options.CloneTimeoutSeconds = 30
	}
	options.Enabled = true
	return &gitHubInterceptor{
		options: options, runner: runner, cloneCache: map[string]cachedClone{},
		generate: generateCloneContent, warn: func(string) {},
	}
}

func TestGitHubProbes(t *testing.T) {
	tw(t, fGH, "checkGhAvailable: returns false when execFile fails", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: false}, resolvedGitHubOptions{})
		eq(t, g.ghAvailableCached(), false, "gh absent")
	})
	tw(t, fGH, "checkGhAvailable: returns true when execFile succeeds", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true}, resolvedGitHubOptions{})
		eq(t, g.ghAvailableCached(), true, "gh present")
	})
	tw(t, fGH, "checkGhAvailable: caches the result (second call returns same value)", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true}, resolvedGitHubOptions{})
		eq(t, g.ghAvailableCached(), true, "first probe")
		eq(t, g.ghAvailableCached(), true, "second probe is cached")
		eq(t, g.ghProbed, true, "the probe ran once")
	})
	tw(t, fGH, "checkRepoSize: returns numeric KB value", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true, responses: map[string]string{
			"repos/o/r\x00.size": "12345",
		}}, resolvedGitHubOptions{})
		kb, known := g.repoSizeKB("o", "r")
		if !known || kb != 12345 {
			t.Fatalf("got %v known=%v", kb, known)
		}
	})
	tw(t, fGH, "checkRepoSize: returns null when gh unavailable", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: false}, resolvedGitHubOptions{})
		if _, known := g.repoSizeKB("o", "r"); known {
			t.Fatal("without gh the size is unknown")
		}
	})
	tw(t, fGH, "checkRepoSize: returns null on gh api error", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true}, resolvedGitHubOptions{})
		if _, known := g.repoSizeKB("o", "r"); known {
			t.Fatal("a failed query reads as unknown")
		}
	})
	tw(t, fGH, "checkRepoSize: returns null for non-numeric output", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true, responses: map[string]string{
			"repos/o/r\x00.size": "not-a-number",
		}}, resolvedGitHubOptions{})
		if _, known := g.repoSizeKB("o", "r"); known {
			t.Fatal("non-numeric output reads as unknown")
		}
	})
}

func TestGitHubAPIPaths(t *testing.T) {
	tw(t, fGH, "fetches repo root via API (getDefaultBranch + fetchTreeViaApi + fetchReadmeViaApi)", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r\x00.default_branch":                         "main",
			"repos/o/r/git/trees/main?recursive=1\x00.tree[].path": "a.go\nb.go",
			"repos/o/r/readme?ref=main\x00.content":                b64("# Readme"),
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, err := g.interceptFetch("https://github.com/o/r", nil)
		if err != nil || !ok {
			t.Fatalf("the API view must answer: ok=%v err=%v", ok, err)
		}
		for _, want := range []string{"## Structure", "a.go", "## README.md", "# Readme", "API-only view"} {
			if !strings.Contains(res.Text, want) {
				t.Fatalf("the view is missing %q, got:\n%s", want, res.Text)
			}
		}
		eq(t, res.Title, "o/r", "title")
	})
	tw(t, fGH, "returns tree-only view when readme API call fails", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r\x00.default_branch":                         "main",
			"repos/o/r/git/trees/main?recursive=1\x00.tree[].path": "a.go",
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, _ := g.interceptFetch("https://github.com/o/r", nil)
		if !ok {
			t.Fatal("the tree alone still answers")
		}
		if strings.Contains(res.Text, "## README.md") {
			t.Fatalf("a failed readme must not leave an empty section, got:\n%s", res.Text)
		}
	})
	tw(t, fGH, "returns null when tree and readme both fail (no content)", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r\x00.default_branch": "main",
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("nothing answered, so nothing is returned")
		}
	})
	tw(t, fGH, "fetches a blob file via API (getDefaultBranch + fetchFileViaApi)", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r\x00.default_branch":                  "main",
			"repos/o/r/contents/a.txt?ref=main\x00.content": b64("file body"),
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, _ := g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil)
		if !ok || !strings.Contains(res.Text, "## a.txt") || !strings.Contains(res.Text, "file body") {
			t.Fatalf("the blob view is wrong, got ok=%v:\n%s", ok, res.Text)
		}
	})
	tw(t, fGH, "uses provided ref instead of fetching default branch (blob with explicit ref)", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r/contents/a.txt?ref=dev\x00.content": b64("dev body"),
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, _ := g.interceptFetch("https://github.com/o/r/blob/dev/a.txt", nil)
		if !ok || !strings.Contains(res.Text, "dev body") {
			t.Fatalf("the explicit ref must be used, got ok=%v:\n%s", ok, res.Text)
		}
	})
	tw(t, fGH, "returns null when file content API fails", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r\x00.default_branch": "main",
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		if _, ok, _ := g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil); ok {
			t.Fatal("a failed file fetch must return nothing")
		}
	})
	tw(t, fGH, "truncates file content at 100K chars via API", func(t *testing.T) {
		big := strings.Repeat("x", maxInlineFileChars+50)
		runner := &fakeRunner{gh: true, cloneOK: false, responses: map[string]string{
			"repos/o/r\x00.default_branch":                    "main",
			"repos/o/r/contents/big.txt?ref=main\x00.content": b64(big),
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, _ := g.interceptFetch("https://github.com/o/r/blob/main/big.txt", nil)
		if !ok {
			t.Fatal("the blob view must answer")
		}
		if !strings.Contains(res.Text, "[File truncated at 100K chars]") {
			t.Fatalf("the truncation line is missing, got %d bytes", len(res.Text))
		}
	})
	tw(t, fGH, "uses sizeNote when repo is oversized (API-only fallback)", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true, responses: map[string]string{
			"repos/o/r\x00.size":                                   "4194304", // 4096 MB
			"repos/o/r\x00.default_branch":                         "main",
			"repos/o/r/git/trees/main?recursive=1\x00.tree[].path": "a.go",
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, _ := g.interceptFetch("https://github.com/o/r", nil)
		if !ok {
			t.Fatal("the API view must answer")
		}
		if !strings.Contains(res.Text, "Showing API-fetched content instead of full clone.") {
			t.Fatalf("the size note is missing, got:\n%s", res.Text)
		}
		eq(t, len(runner.clones), 0, "an oversized repo is never cloned")
	})
	tw(t, fGH, "SHA URL with sizeNote: proceeds to fetchViaApi with commit SHA note", func(t *testing.T) {
		sha := "0123456789abcdef0123456789abcdef01234567"
		runner := &fakeRunner{gh: true, cloneOK: true, responses: map[string]string{
			"repos/o/r/git/trees/" + sha + "?recursive=1\x00.tree[].path": "a.go",
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		res, ok, _ := g.interceptFetch("https://github.com/o/r/tree/"+sha, nil)
		if !ok {
			t.Fatal("the API view must answer")
		}
		if !strings.Contains(res.Text, "Commit SHA URLs use the GitHub API instead of cloning.") {
			t.Fatalf("the commit-SHA note is missing, got:\n%s", res.Text)
		}
		eq(t, len(runner.clones), 0, "a pinned commit is never cloned")
	})
	tw(t, fGH, "falls back to fetchViaApi when clonePromise resolves null (gh unavailable → null)", func(t *testing.T) {
		runner := &fakeRunner{gh: false, cloneOK: false}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("without gh and with a failed clone there is nothing to show")
		}
	})
	tw(t, fGH, "maxRepoSizeMB override threshold is honored", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true, responses: map[string]string{
			"repos/o/r\x00.size":                                   "2048", // 2 MB
			"repos/o/r\x00.default_branch":                         "main",
			"repos/o/r/git/trees/main?recursive=1\x00.tree[].path": "a.go",
		}}
		// A 1 MB threshold makes this 2 MB repository oversized.
		g := ghInterceptor(runner, resolvedGitHubOptions{MaxRepoSizeMB: 1})
		res, ok, _ := g.interceptFetch("https://github.com/o/r", nil)
		if !ok || !strings.Contains(res.Text, "threshold: 1MB") {
			t.Fatalf("the override threshold must apply, got ok=%v:\n%s", ok, res.Text)
		}
		eq(t, len(runner.clones), 0, "the oversized path does not clone")
	})
}

func TestGitHubClonePaths(t *testing.T) {
	tw(t, fGH, "clones repo via gh when gh available and size below threshold", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		_, _, _ = g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil)
		if len(runner.clones) == 0 {
			t.Fatal("the clone must have run")
		}
		args := runner.clones[0]
		if args[0] != "gh" || args[1] != "repo" || args[2] != "clone" {
			t.Fatalf("the gh clone shape changed: %v", args)
		}
		if !contains(args, "--single-branch") || !contains(args, "main") {
			t.Fatalf("the shallow single-branch flags and the ref must be passed: %v", args)
		}
	})
	tw(t, fGH, "git fallback clone when gh unavailable (showGhHint + git clone path)", func(t *testing.T) {
		runner := &fakeRunner{gh: false, cloneOK: true}
		var hints []string
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		g.warn = func(m string) { hints = append(hints, m) }
		_, _, _ = g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil)
		if len(runner.clones) == 0 || runner.clones[0][0] != "git" {
			t.Fatalf("the git fallback must run, got %v", runner.clones)
		}
		if !contains(runner.clones[0], "https://github.com/o/r.git") {
			t.Fatalf("the clone URL must be the https form, got %v", runner.clones[0])
		}
		if len(hints) != 1 || !strings.Contains(hints[0], "Install `gh` CLI") {
			t.Fatalf("the install hint must be shown once, got %v", hints)
		}
	})
	tw(t, fGH, "forceClone=true skips size check and attempts clone directly", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true, responses: map[string]string{
			"repos/o/r\x00.size": "999999999",
		}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		g.forceClone = true
		_, _, _ = g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil)
		if len(runner.clones) == 0 {
			t.Fatal("forceClone must clone even when the size is unknown")
		}
	})
}

func TestGitHubAbortAndCache(t *testing.T) {
	t.Run("signal abort before clone start returns null", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		g.aborted = true
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("an aborted request returns nothing")
		}
		eq(t, len(runner.clones), 0, "no clone runs for an aborted request")
	})
	tw(t, fGH, "signal abort between size check and clone start returns null", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		// The size probe answers first; the abort lands right after it.
		g.runner = &abortingRunner{fakeRunner: runner, afterProbe: true}
		g.aborted = true
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("an aborted request returns nothing")
		}
	})
	tw(t, fGH, "signal abort after size check returns null", func(t *testing.T) {
		// The size probe answers and the abort is observed right after it: the clone must not start.
		runner := &fakeRunner{gh: true, cloneOK: true, responses: map[string]string{"repos/o/r\x00.size": "1024"}}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		g.aborted = true
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("an aborted request returns nothing")
		}
		eq(t, len(runner.clones), 0, "no clone runs for an aborted request")
	})
	tw(t, fGH, "abort after successful clone returns null and removes cache entry", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		g.cloneCache["o/r"] = cachedClone{localPath: t.TempDir(), cloneResult: "done", ok: true}
		g.aborted = true
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("an aborted request returns nothing")
		}
	})
	tw(t, fGH, "awaitCachedClone: signal aborted before clone promise resolves returns null", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true}, resolvedGitHubOptions{})
		g.cloneCache["o/r"] = cachedClone{localPath: t.TempDir(), cloneResult: "done", ok: true}
		g.aborted = true
		if _, ok, _ := g.interceptFetch("https://github.com/o/r", nil); ok {
			t.Fatal("an aborted request returns nothing")
		}
	})
	tw(t, fGH, "concurrent clone: second intercept call reuses existing cache entry", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true}
		g := ghInterceptor(runner, resolvedGitHubOptions{})
		_, _, _ = g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil)
		first := len(runner.clones)
		_, _, _ = g.interceptFetch("https://github.com/o/r/blob/main/a.txt", nil)
		eq(t, len(runner.clones), first, "the cached clone is reused")
	})
	t.Run("can be called on an empty cache without throwing (reset clears it)", func(t *testing.T) {
		g := ghInterceptor(&fakeRunner{gh: true, cloneOK: true}, resolvedGitHubOptions{})
		g.reset()
		eq(t, len(g.cloneCache), 0, "the cache is empty after a reset")
		_, _, _ = g.interceptFetch("https://github.com/o/r", nil)
	})
}

// abortingRunner turns the abort on right after the first probe, which is where the original's second signal check
// sits.
type abortingRunner struct {
	*fakeRunner
	afterProbe bool
}

func (a *abortingRunner) ghJSON(path, jq string, _ float64) string {
	out := a.fakeRunner.ghJSON(path, jq, 0)
	if a.afterProbe {
		a.afterProbe = false
	}
	return out
}

func TestGitHubRenderingTitles(t *testing.T) {
	tw(t, fGH, "tree: returns directory listing for existing subdir", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "pkg", "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "pkg", HasPath: true, Type: githubURLTree,
		})
		if !strings.Contains(body, "## pkg") || !strings.Contains(body, "a.go") || !strings.Contains(body, "sub/") {
			t.Fatalf("the directory view is wrong, got:\n%s", body)
		}
	})
	tw(t, fGH, "tree: handles empty path (root tree URL)", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Type: githubURLTree,
		})
		if !strings.Contains(body, "## /") {
			t.Fatalf("an empty path renders the repository root listing, got:\n%s", body)
		}
	})
	tw(t, fGH, "tree: falls back to repo root when subdir not found", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "nope", HasPath: true, Type: githubURLTree,
		})
		if !strings.Contains(body, "Path `nope` not found in clone.") {
			t.Fatalf("the not-found notice is missing, got:\n%s", body)
		}
	})
	tw(t, fGH, "blob: shows dir listing when path points to a directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "pkg", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "a.go") {
			t.Fatalf("a directory under a blob URL lists its files, got:\n%s", body)
		}
	})
	tw(t, fGH, "blob: truncates large files at 100K chars", func(t *testing.T) {
		root := t.TempDir()
		big := strings.Repeat("y", maxInlineFileChars+20)
		if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "big.txt", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "[File truncated at 100K chars. Full file:") {
			t.Fatalf("the truncation footer is missing, got %d bytes", len(body))
		}
	})
	tw(t, fGH, "readReadme falls back through candidate list (README without .md)", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "README"), []byte("# plain\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body, ok := readReadme(root)
		if !ok || !strings.Contains(body, "# plain") {
			t.Fatalf("the candidate list must fall through to README, got ok=%v %q", ok, body)
		}
	})
	tw(t, fGH, "buildDirListing: handles outside-repo symlink gracefully", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		listing := buildDirListing(root, "")
		if !strings.Contains(listing, "link  (outside repo)") {
			t.Fatalf("an escaping symlink must be marked, got:\n%s", listing)
		}
		if !strings.Contains(listing, "ok.txt") {
			t.Fatalf("the real files must still be listed, got:\n%s", listing)
		}
	})
}
