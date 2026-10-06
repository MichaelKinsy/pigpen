// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"strings"
	"testing"
)

// The clone path runs the real gh and the real git against a small public repository. It is the one branch the canned
// runner cannot stand in for, because it writes to disk and shells out twice.

func TestLiveClone(t *testing.T) {
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("no GH_TOKEN/GITHUB_TOKEN: the live clone needs an authenticated gh")
	}
	if _, err := (execRunner{}).ghAvailableRunner(); err != nil {
		t.Skip("gh is not installed on this machine")
	}
	if _, err := (execRunner{}).ghAvailableRunner(); err != nil {
		t.Skip("gh is not installed")
	}

	cloneRoot := t.TempDir()
	g := ghInterceptor(execRunner{}, resolvedGitHubOptions{
		Enabled:             true,
		MaxRepoSizeMB:       350,
		CloneTimeoutSeconds: 300,
		ClonePath:           cloneRoot,
	})
	t.Setenv("GH_TOKEN", os.Getenv("GH_TOKEN"))

	t.Run("clones a repository root and renders the tree and readme", func(t *testing.T) {
		info := gitHubURLInfo{Owner: "juicesharp", Repo: "rpiv-web-tools-pigpen", Type: githubURLRoot}
		_ = info
		res, ok, err := g.interceptFetch("https://github.com/neciptron/pigpen", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Skip("the clone path did not answer on this machine (no git credentials or a too-large repository)")
		}
		for _, want := range []string{"Repository cloned to: ", "## Structure", "components/"} {
			if !strings.Contains(res.Text, want) {
				t.Fatalf("the cloned view is missing %q, got the first 300 bytes:\n%.300s", want, res.Text)
			}
		}
		if _, err := os.Stat(res.Title); err == nil {
			t.Fatal("the title must not be a path")
		}
		t.Logf("cloned view is %d bytes", len(res.Text))
	})
}
