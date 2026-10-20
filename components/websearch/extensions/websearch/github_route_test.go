package websearch

// The routing of GitHub URLs inside ExtractContent and the session hooks, which the twins and the
// recorded wire cases reach through the extractors directly.

import (
	"strings"
	"testing"
)

const readmeClone = `for a; do dest="$a"; done
mkdir -p "$dest"
printf '# Cloned\n' > "$dest/README.md"`

func TestGitHubSessionHooksClearTheCloneCache(t *testing.T) {
	w := newGHWorld(t, nil)
	w.shim(t, "gh", "exit 1")
	w.shim(t, "git", readmeClone)
	t.Cleanup(ClearResults)
	r, err := NewRuntime(&fakeHost{})
	noErr(t, err)
	for _, hook := range []func(){func() { r.SessionStarted(nil) }, r.SessionShutdown} {
		res := ExtractContent(bg(), "https://github.com/owner/repo", pageOpts())
		if res.Error != nil {
			t.Fatal(*res.Error)
		}
		runtimeDir := clonedPathDir(&res)
		if runtimeDir == "" || !pathExists(runtimeDir) {
			t.Fatalf("no clone: %+v", res)
		}
		hook()
		if pathExists(runtimeDir) {
			t.Fatal("the session hook left the runtime directory behind")
		}
	}
}

func clonedPathDir(r *ExtractedContent) string {
	p := clonedPath(r)
	if p == "" {
		return ""
	}
	return p[:strings.LastIndex(p, "/")]
}

func TestGitHubForceCloneOptionSkipsTheSizeCheck(t *testing.T) {
	w := newGHWorld(t, nil)
	w.shim(t, "gh", `case "$*" in
  '--version') exit 0;;
  'api repos/owner/repo --jq .size') echo 999999999; exit 0;;
  'api repos/owner/repo --jq .default_branch') echo main; exit 0;;
  'api repos/owner/repo/git/trees/main?recursive=1 --jq .tree[].path') echo a.txt; exit 0;;
  'api repos/owner/repo/readme?ref=main --jq .content') printf 'IyBhcGkK'; exit 0;;
  'repo clone owner/repo '*) mkdir -p "$4"; printf '# forced\n' > "$4/README.md"; exit 0;;
esac
exit 1`)
	useNet(t, func(netCall) netReply { return reply(404, "unexpected page fetch") })
	o := pageOpts()
	res := ExtractContent(bg(), "https://github.com/owner/repo", o)
	matchRE(t, `^Note: Repository is 976562MB \(threshold: 350MB\)`, res.Content+"")
	if res.Error != nil || !strings.Contains(res.Content, "Showing API-fetched content") {
		t.Fatalf("an oversized repository without forceClone: %+v", res)
	}
	o.ForceClone = true
	res = ExtractContent(bg(), "https://github.com/owner/repo", o)
	if res.Error != nil || clonedPath(&res) == "" || !strings.Contains(res.Content, "# forced") {
		t.Fatalf("forceClone: %+v", res)
	}
}

func TestGitHubConfigErrorsRoute(t *testing.T) {
	t.Run("a configuration that does not parse is the URL's error", func(t *testing.T) {
		w := newGHWorld(t, nil)
		w.shim(t, "gh", "exit 1")
		writeConfig(t, w.agentDir, "{ nope")
		n := useNet(t, func(netCall) netReply { return reply(200, "x") })
		for _, u := range []string{"https://github.com/o/r", "https://github.com/o/r/pull/3", "https://github.com/o/r/issues/4"} {
			res := ExtractContent(bg(), u, pageOpts())
			if !strings.HasPrefix(errOf(res), "Failed to parse ") {
				t.Fatalf("%s: %+v", u, res)
			}
		}
		wantCalls(t, n)
	})
	t.Run("any other failure of an extractor leaves the URL to the page fetch", func(t *testing.T) {
		w := newGHWorld(t, nil)
		w.shim(t, "gh", "exit 1")
		writeConfig(t, w.agentDir, "null")
		if _, err := ghState(t).extractGitHub(bg(), "https://github.com/o/r", false); err == nil || !strings.Contains(err.Error(), "Cannot read properties of null") {
			t.Fatalf("a null configuration: %v", err)
		}
		// extractGitHubURL swallows it, as extract.ts does: the fetch goes on with the page.
		if res, done := extractGitHubURL(bg(), "https://github.com/o/r", pageOpts()); done {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("a pull request with githubPrIssue disabled is the plain page", func(t *testing.T) {
		w := newGHWorld(t, nil)
		w.shim(t, "gh", "exit 1")
		writeConfig(t, w.agentDir, `{"githubPrIssue":{"enabled":false}}`)
		n := useNet(t, func(netCall) netReply { return reply(404, "gone") })
		res := ExtractContent(bg(), "https://github.com/o/r/pull/3", pageOpts())
		if res.Status == nil || *res.Status != 404 {
			t.Fatalf("%+v", res)
		}
		wantCalls(t, n, "https://github.com/o/r/pull/3")
	})
}

func TestParseGitHubURLEdges(t *testing.T) {
	for _, u := range []string{
		"https://github.com/o/r/ISSUES/1", "https://github.com/o/r/Pull/1", "https://github.com/o", "https://github.com/",
		"https://github.com/o/r/blob", "https://github.com/o/r/tree/", "https://github.com/o/r/blob/" + strings.Repeat("a", 1025) + "/x",
		"https://github.com/-o/r", "https://github.com/o-/r", "https://github.com/o--p/r", "https://github.com/" + strings.Repeat("a", 40) + "/r",
		"https://github.com/o/..", "https://github.com/o/r/blob/%FF/x", "https://gist.github.com/o/r", "https://github.com.evil.example/o/r", "not a url",
	} {
		if got := parseGitHubURL(u); got != nil {
			t.Errorf("%s: %+v", u, got)
		}
	}
	if got := parseGitHubURL("https://www.GitHub.com/o/r/blob/" + strings.Repeat("b", 1024) + "/x"); got == nil || got.Ref != strings.Repeat("b", 1024) {
		t.Errorf("a 1024-character ref: %+v", got)
	}
	got := parseGitHubURL("https://github.com/o/r/blob/0123456789abcdef0123456789abcdef01234567/a/b.txt")
	if got == nil || !got.RefIsFullSHA || got.Path != "a/b.txt" || got.Type != "blob" {
		t.Errorf("sha: %+v", got)
	}
	if got := parseGitHubURL("https://github.com/o/r/tree/0123456789ABCDEF0123456789abcdef01234567"); got == nil || got.RefIsFullSHA {
		t.Errorf("an upper-case digest is not a full SHA: %+v", got)
	}
	if got := parseGitHubURL("https://github.com/o/r/tree/main"); got == nil || got.Path != "" || got.Type != "tree" {
		t.Errorf("tree root: %+v", got)
	}
}
