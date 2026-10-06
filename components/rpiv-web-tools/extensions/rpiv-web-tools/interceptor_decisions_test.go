// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
	"testing"
)

// fakeRunner answers the external commands the interceptor shells out to, so every decision path — no gh, gh present,
// size threshold, abort, cache — is exercised without gh, git or the network. upstream: the vi.mock("node:child_process")
// double in providers/interceptors/github.test.ts.
type fakeRunner struct {
	gh        bool
	responses map[string]string // gh api path -> stdout
	clones    [][]string        // the clone argv it was asked to run
	cloneOK   bool
}

// ghAvailable reports the stubbed gh presence.
func (f *fakeRunner) ghAvailable() bool { return f.gh }

func (f *fakeRunner) ghJSON(path, jq string, _ float64) string {
	// The real gh answers per endpoint and per jq filter, so the stub is keyed by both.
	if v, ok := f.responses[path+"\x00"+jq]; ok {
		return v
	}
	return f.responses[path]
}

func (f *fakeRunner) ghRaw(path, jq string, _ float64, _ int) string {
	if v, ok := f.responses[path+"\x00"+jq]; ok {
		return v
	}
	return f.responses[path]
}

func (f *fakeRunner) clone(args []string, _ float64) (string, bool) {
	f.clones = append(f.clones, args)
	if !f.cloneOK {
		return "", false
	}
	return args[len(args)-1], true
}

func TestGitHubInterceptorDecisions(t *testing.T) {
	newInterceptor := func(enabled bool, runner githubRunner) *gitHubInterceptor {
		return &gitHubInterceptor{
			options: resolvedGitHubOptions{
				Enabled: enabled, MaxRepoSizeMB: 350, CloneTimeoutSeconds: 30, ClonePath: t.TempDir(),
			},
			runner:     runner,
			cloneCache: map[string]cachedClone{},
			generate:   generateCloneContent,
			warn:       func(string) {},
		}
	}

	tw(t, fGitHub, "returns null for non-GitHub URL", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: true, cloneOK: true})
		if _, ok, _ := g.intercept("https://gitlab.com/a/b", false); ok {
			t.Fatal("a non-GitHub URL must fall through")
		}
	})
	tw(t, fGitHub, "returns null for NON_CODE_SEGMENTS URLs", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: true, cloneOK: true})
		for _, target := range []string{
			"https://github.com/owner/repo/issues/1",
			"https://github.com/owner/repo/pull/2",
			"https://github.com/owner/repo/actions",
			"https://github.com/owner/repo/wiki",
		} {
			if _, ok, _ := g.intercept(target, false); ok {
				t.Fatalf("%s must fall through", target)
			}
		}
	})
	tw(t, fGitHub, "returns null when signal is already aborted", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: true, cloneOK: true})
		g.aborted = true
		if _, ok, _ := g.intercept("https://github.com/owner/repo", false); ok {
			t.Fatal("an aborted request must return nothing")
		}
	})
	tw(t, fGitHub, "returns null when interceptor.enabled is false", func(t *testing.T) {
		g := newInterceptor(false, &fakeRunner{gh: true, cloneOK: true})
		if _, ok, _ := g.intercept("https://github.com/owner/repo", false); ok {
			t.Fatal("a disabled interceptor must return nothing")
		}
	})
	tw(t, fGitHub, "returns null for full-SHA ref (gh unavailable → API path → getDefaultBranch null)", func(t *testing.T) {
		// With no gh the API path cannot resolve a ref, so a pinned-commit URL yields nothing rather than an error.
		g := newInterceptor(true, &fakeRunner{gh: false})
		sha := "0123456789abcdef0123456789abcdef01234567"
		if _, ok, _ := g.intercept("https://github.com/owner/repo/blob/"+sha+"/f.txt", false); ok {
			t.Fatal("without gh a pinned commit cannot be resolved")
		}
	})
	tw(t, fGitHub, "returns null for root URL when gh unavailable and git clone fails", func(t *testing.T) {
		runner := &fakeRunner{gh: false, cloneOK: false}
		g := newInterceptor(true, runner)
		if _, ok, _ := g.intercept("https://github.com/owner/repo", false); ok {
			t.Fatal("a failed clone with no API fallback must return nothing")
		}
		if len(runner.clones) != 1 {
			t.Fatalf("the git fallback must have run once, got %d", len(runner.clones))
		}
		eq(t, runner.clones[0][0], "git", "the plain git path")
	})
	tw(t, fGitHub, "returns null for blob URL when no gh and no clone", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: false, cloneOK: false})
		if _, ok, _ := g.intercept("https://github.com/owner/repo/blob/main/f.txt", false); ok {
			t.Fatal("a failed clone must return nothing")
		}
	})
	tw(t, fGitHub, "returns null for root URL (size=null, clone fails, API=null)", func(t *testing.T) {
		// gh answers the probes with nothing, so the size is unknown and the clone is still attempted.
		runner := &fakeRunner{gh: true, cloneOK: false}
		g := newInterceptor(true, runner)
		if _, ok, _ := g.intercept("https://github.com/owner/repo", false); ok {
			t.Fatal("every path failed, so nothing is returned")
		}
		eq(t, runner.clones[0][0], "gh", "with gh present the gh clone path is used")
	})
	tw(t, fGitHub, "returns null for blob URL (clone fails, API=null)", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: true, cloneOK: false})
		if _, ok, _ := g.intercept("https://github.com/owner/repo/blob/main/f.txt", false); ok {
			t.Fatal("every path failed, so nothing is returned")
		}
	})
	t.Run("surfaces the size note and uses the API when the repo is over the threshold", func(t *testing.T) {
		// 900 MB is over the 350 MB default, so the interceptor must not clone.
		runner := &fakeRunner{gh: true, cloneOK: true, responses: map[string]string{
			"repos/owner/repo\x00.default_branch":                         "main",
			"repos/owner/repo\x00.size":                                   "921600",
			"repos/owner/repo/git/trees/main?recursive=1\x00.tree[].path": "src/main.go\nREADME.md",
		}}
		g := newInterceptor(true, runner)
		res, ok, err := g.intercept("https://github.com/owner/repo", false)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("the API view must answer")
		}
		if len(runner.clones) != 0 {
			t.Fatalf("an over-threshold repo must not be cloned, got %v", runner.clones)
		}
		if !strings.Contains(res.Text, "Repository is 900MB") {
			t.Fatalf("the size note must explain the fallback, got:\n%s", res.Text)
		}
	})
	t.Run("clones through gh when it is available", func(t *testing.T) {
		runner := &fakeRunner{gh: true, cloneOK: true}
		g := newInterceptor(true, runner)
		if _, ok, _ := g.intercept("https://github.com/owner/repo/blob/main/f.txt", false); ok {
			// The clone path is stubbed, so the generated content is what answers; either way the clone must have run.
			t.Log("clone answered with an empty path")
		}
		if len(runner.clones) == 0 {
			t.Fatal("the clone must have run")
		}
		eq(t, runner.clones[0][0], "gh", "gh clones when it is present")
	})
	tw(t, fGitHub, "can be called on an empty cache without throwing", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: true, cloneOK: true})
		eq(t, len(g.cloneCache), 0, "the cache starts empty")
		_, _, _ = g.intercept("https://github.com/owner/repo", false)
		_, _, _ = g.intercept("https://github.com/owner/repo", false)
	})
	tw(t, fGitHub, "resets ghAvailable so the probe re-runs on next call", func(t *testing.T) {
		g := newInterceptor(true, &fakeRunner{gh: false})
		eq(t, g.ghProbed, false, "the probe has not run yet")
		_, _, _ = g.intercept("https://github.com/owner/repo", false)
		eq(t, g.ghProbed, true, "the probe ran and cached its answer")
		g.reset()
		eq(t, g.ghProbed, false, "reset clears the cached probe")
		eq(t, len(g.cloneCache), 0, "reset clears the clone cache")
	})
}
