// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"strings"
	"testing"
)

// These cases run the port's own decision path against the real gh CLI and the real GitHub API, which the canned-runner
// twins cannot cover: that the CLI answers where the seam assumes, that the jq filters name real fields, and that the
// assembled view is what the original would produce.
//
// They are skipped unless GITHUB_TOKEN (or GH_TOKEN) is set and gh is installed, so a machine without either still
// runs the whole suite. Set LIVE_GITHUB_REPO=owner/repo to point them at another public repository.

func liveRepo(t *testing.T) (owner, repo string) {
	t.Helper()
	if os.Getenv("LIVE_GITHUB_REPO") != "" {
		parts := strings.SplitN(os.Getenv("LIVE_GITHUB_REPO"), "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("no GH_TOKEN/GITHUB_TOKEN: the live GitHub cases need an authenticated gh")
	}
	if _, err := (execRunner{}).ghAvailableRunner(); err != nil {
		t.Skip("gh is not installed on this machine")
	}
	return "juicesharp", "rpiv-mono"
}

func TestLiveGitHubAPI(t *testing.T) {
	owner, repo := liveRepo(t)
	t.Setenv("GH_TOKEN", os.Getenv("GH_TOKEN"))

	t.Run("the gh probe reports the real CLI", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		if !g.ghAvailableCached() {
			t.Fatal("gh is installed, so the probe must find it")
		}
	})
	t.Run("checkRepoSize reads the real size", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		kb, known := g.repoSizeKB(owner, repo)
		if !known {
			t.Fatalf("the real query must answer for %s/%s", owner, repo)
		}
		if kb <= 0 {
			t.Fatalf("the reported size must be positive, got %v", kb)
		}
		t.Logf("%s/%s is %v KB (%.1f MB)", owner, repo, kb, kb/1024)
	})
	t.Run("an unknown repository reads as unknown rather than failing", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		if _, known := g.repoSizeKB(owner, "definitely-not-a-real-repo-9f3a"); known {
			t.Fatal("a missing repository must read as unknown")
		}
	})
	t.Run("the API view of a repository root is assembled from the real API", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		info := gitHubURLInfo{Owner: owner, Repo: repo, Type: githubURLRoot}
		res, ok, err := g.fetchViaAPI(*mustPtr(info), "")
		if err != nil || !ok {
			t.Fatalf("the API view must answer: ok=%v err=%v", ok, err)
		}
		for _, want := range []string{"## Structure", "## README.md", "API-only view"} {
			if !strings.Contains(res.Text, want) {
				t.Fatalf("the view is missing %q, got the first 400 bytes:\n%.400s", want, res.Text)
			}
		}
		if res.Title != owner+"/"+repo {
			t.Fatalf("the title must name the repository, got %q", res.Title)
		}
		t.Logf("view is %d bytes, title %q", len(res.Text), res.Title)
	})
	t.Run("a blob URL resolves its default branch and reads the file", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		info := gitHubURLInfo{Owner: owner, Repo: repo, Type: githubURLBlob, Path: "packages/rpiv-web-tools/package.json", HasPath: true}
		res, ok, err := g.fetchViaAPI(info, "")
		if err != nil || !ok {
			t.Fatalf("the blob view must answer: ok=%v err=%v", ok, err)
		}
		if !strings.Contains(res.Text, "## packages/rpiv-web-tools/package.json") {
			t.Fatalf("the heading is missing, got:\n%.300s", res.Text)
		}
		if !strings.Contains(res.Text, `"name"`) {
			t.Fatalf("the file content is missing, got:\n%.300s", res.Text)
		}
	})
	t.Run("a 100K+ file is truncated with its notice", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		info := gitHubURLInfo{Owner: owner, Repo: repo, Type: githubURLBlob, Path: "packages/rpiv-web-tools/index.test.ts", HasPath: true}
		res, ok, _ := g.fetchViaAPI(info, "")
		if !ok {
			t.Skip("this repository has no such file to truncate")
		}
		if len(res.Text) > maxInlineFileChars && !strings.Contains(res.Text, "[File truncated at 100K chars]") {
			t.Fatalf("an over-budget file must carry its notice, got %d bytes", len(res.Text))
		}
		t.Logf("the fetched view is %d bytes", len(res.Text))
	})
	t.Run("a pinned commit URL is served from the API with its note", func(t *testing.T) {
		g := ghInterceptor(execRunner{}, resolvedGitHubOptions{})
		info := gitHubURLInfo{Owner: owner, Repo: repo, Ref: "main", HasRef: true, RefIsFullSHA: true, Type: githubURLTree}
		res, ok, err := g.fetchViaAPI(info, "Note: Commit SHA URLs use the GitHub API instead of cloning.")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Skip("the API answered nothing for this repository at this ref")
		}
		if !strings.Contains(res.Text, "Commit SHA URLs use the GitHub API instead of cloning.") {
			t.Fatalf("the size note must lead the view, got:\n%.200s", res.Text)
		}
		if !strings.Contains(res.Text, "## Structure") {
			t.Fatalf("the tree must follow the note, got:\n%.200s", res.Text)
		}
	})
}

// mustPtr is the pointer form the API helper takes.
func mustPtr(info gitHubURLInfo) *gitHubURLInfo { return &info }
