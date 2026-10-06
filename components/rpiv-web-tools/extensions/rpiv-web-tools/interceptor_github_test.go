// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "testing"

const fGitHub = "providers/interceptors/github"

// parseOrFail parses a URL the interceptor must own, failing the twin otherwise.
func parseOrFail(t *testing.T, raw string) gitHubURLInfo {
	t.Helper()
	info, ok := parseGitHubURL(raw)
	if !ok {
		t.Fatalf("%s must be owned by the interceptor", raw)
	}
	return *info
}

func TestGitHubURLParsing(t *testing.T) {
	tw(t, fGitHub, "returns null for non-GitHub URLs", func(t *testing.T) {
		for _, raw := range []string{"https://gitlab.com/a/b", "https://example.com/a/b", "https://raw.githubusercontent.com/a/b/main/f"} {
			if _, ok := parseGitHubURL(raw); ok {
				t.Fatalf("%s must not be owned", raw)
			}
		}
	})
	tw(t, fGitHub, "returns null for invalid URLs", func(t *testing.T) {
		for _, raw := range []string{"", "not a url", "github.com/owner/repo"} {
			if _, ok := parseGitHubURL(raw); ok {
				t.Fatalf("%q must not be owned", raw)
			}
		}
	})
	tw(t, fGitHub, "returns null when path has fewer than 2 segments", func(t *testing.T) {
		for _, raw := range []string{"https://github.com/owner", "https://github.com/"} {
			if _, ok := parseGitHubURL(raw); ok {
				t.Fatalf("%s must not be owned", raw)
			}
		}
	})
	tw(t, fGitHub, "parses a root repo URL", func(t *testing.T) {
		info := parseOrFail(t, "https://github.com/owner/repo")
		eq(t, info, gitHubURLInfo{Owner: "owner", Repo: "repo", Type: githubURLRoot}, "root info")
	})
	tw(t, fGitHub, "strips .git suffix from repo name", func(t *testing.T) {
		eq(t, parseOrFail(t, "https://github.com/owner/repo.git").Repo, "repo", "repo name")
	})
	tw(t, fGitHub, "handles www.github.com hostname", func(t *testing.T) {
		eq(t, parseOrFail(t, "https://www.github.com/owner/repo").Owner, "owner", "owner")
	})
	tw(t, fGitHub, "returns null when action is not blob or tree", func(t *testing.T) {
		for _, raw := range []string{
			"https://github.com/owner/repo/releases/tag/v1",
			"https://github.com/owner/repo/about",
			"https://github.com/owner/repo/pulls/1",
		} {
			if _, ok := parseGitHubURL(raw); ok {
				t.Fatalf("%s must not be owned", raw)
			}
		}
	})
	tw(t, fGitHub, "returns null when blob/tree has no ref segment", func(t *testing.T) {
		for _, raw := range []string{"https://github.com/owner/repo/blob", "https://github.com/owner/repo/tree"} {
			if _, ok := parseGitHubURL(raw); ok {
				t.Fatalf("%s must not be owned", raw)
			}
		}
	})
	tw(t, fGitHub, "parses a blob URL", func(t *testing.T) {
		info := parseOrFail(t, "https://github.com/owner/repo/blob/main/src/index.ts")
		eq(t, info, gitHubURLInfo{
			Owner: "owner", Repo: "repo", Ref: "main", HasRef: true, Path: "src/index.ts", HasPath: true, Type: githubURLBlob,
		}, "blob info")
	})
	tw(t, fGitHub, "detects full-SHA ref", func(t *testing.T) {
		sha := "0123456789abcdef0123456789abcdef01234567"
		info := parseOrFail(t, "https://github.com/owner/repo/blob/"+sha+"/f.txt")
		eq(t, info.RefIsFullSHA, true, "full sha")
		// A short or mixed-case ref is a branch or tag, not a pinned commit.
		eq(t, parseOrFail(t, "https://github.com/owner/repo/blob/0123456/f.txt").RefIsFullSHA, false, "short ref")
		eq(t, parseOrFail(t, "https://github.com/owner/repo/blob/v1.2.3/f.txt").RefIsFullSHA, false, "tag")
	})
	tw(t, fGitHub, "parses a tree URL with path", func(t *testing.T) {
		info := parseOrFail(t, "https://github.com/owner/repo/tree/main/docs")
		eq(t, info, gitHubURLInfo{
			Owner: "owner", Repo: "repo", Ref: "main", HasRef: true, Path: "docs", HasPath: true, Type: githubURLTree,
		}, "tree info")
	})
	tw(t, fGitHub, "parses a tree URL with empty path (repo root tree)", func(t *testing.T) {
		info := parseOrFail(t, "https://github.com/owner/repo/tree/main")
		eq(t, info, gitHubURLInfo{Owner: "owner", Repo: "repo", Ref: "main", HasRef: true, Type: githubURLTree}, "root tree")
		eq(t, info.HasPath, false, "no path")
	})
	tw(t, fGitHub, "decodes percent-encoded path segments", func(t *testing.T) {
		info := parseOrFail(t, "https://github.com/owner/repo/blob/main/a%20b/c%2Bd.txt")
		eq(t, info.Path, "a b/c+d.txt", "decoded path")
	})
}

func TestGitHubOptions(t *testing.T) {
	tw(t, fGitHub, "is GITHUB_TOKEN", func(t *testing.T) {
		eq(t, gitHubTokenEnvVar, "GITHUB_TOKEN", "token env var")
	})
	tw(t, fGitHub, "absent user config + no consumer default → disabled", func(t *testing.T) {
		resolved := resolveGitHubOptions(userGitHubConfig{}, false)
		eq(t, resolved.Enabled, false, "disabled")
		eq(t, resolved.MaxRepoSizeMB, 350.0, "default size")
		eq(t, resolved.CloneTimeoutSeconds, 30.0, "default timeout")
	})
	tw(t, fGitHub, "absent user config + consumer:true → enabled with defaults", func(t *testing.T) {
		resolved := resolveGitHubOptions(userGitHubConfig{}, true)
		eq(t, resolved.Enabled, true, "enabled")
		eq(t, resolved.MaxRepoSizeMB, 350.0, "default size")
	})
	tw(t, fGitHub, "absent user config + consumer:false → disabled", func(t *testing.T) {
		eq(t, resolveGitHubOptions(userGitHubConfig{}, false).Enabled, false, "disabled")
	})
	tw(t, fGitHub, "user:false beats consumer:true → disabled (explicit user override wins)", func(t *testing.T) {
		resolved := resolveGitHubOptions(userGitHubConfig{Set: true, Disabled: true}, true)
		eq(t, resolved.Enabled, false, "disabled")
	})
	tw(t, fGitHub, "user:true beats consumer:false → enabled", func(t *testing.T) {
		eq(t, resolveGitHubOptions(userGitHubConfig{Set: true}, false).Enabled, true, "enabled")
	})
	tw(t, fGitHub, "user object form implies opt-in regardless of consumer default", func(t *testing.T) {
		resolved := resolveGitHubOptions(userGitHubConfig{Set: true, Object: &githubInterceptorOptions{}}, false)
		eq(t, resolved.Enabled, true, "object form opts in")
	})
	tw(t, fGitHub, "user object with explicit \"enabled\": false honors it", func(t *testing.T) {
		off := false
		resolved := resolveGitHubOptions(userGitHubConfig{Set: true, Object: &githubInterceptorOptions{Enabled: &off}}, true)
		eq(t, resolved.Enabled, false, "explicit false inside the object is honored")
	})
	tw(t, fGitHub, "user object overrides every default field", func(t *testing.T) {
		size, timeout := 900.0, 90.0
		path := "/tmp/custom"
		resolved := resolveGitHubOptions(userGitHubConfig{Set: true, Object: &githubInterceptorOptions{
			MaxRepoSizeMB: &size, CloneTimeoutSeconds: &timeout, ClonePath: &path,
		}}, false)
		eq(t, resolved.MaxRepoSizeMB, 900.0, "size")
		eq(t, resolved.CloneTimeoutSeconds, 90.0, "timeout")
		eq(t, resolved.ClonePath, "/tmp/custom", "clone path")
	})
	t.Run("reads the stanza off the canonical config", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"interceptors":{"github":true}}`)
		cfg := readUserGitHubConfig()
		eq(t, cfg.Set, true, "stanza present")
		eq(t, cfg.Disabled, false, "boolean true")
		write(`{"interceptors":{"github":{"maxRepoSizeMB":10}}}`)
		cfg = readUserGitHubConfig()
		if cfg.Object == nil || cfg.Object.MaxRepoSizeMB == nil {
			t.Fatal("the object form must reach the interceptor")
		}
		eq(t, *cfg.Object.MaxRepoSizeMB, 10.0, "size")
		eq(t, resolveGitHubOptions(cfg, false).Enabled, true, "object implies opt-in")
	})
	t.Run("is absent when the config has no interceptor stanza", func(t *testing.T) {
		configHome(t)
		eq(t, readUserGitHubConfig().Set, false, "absent")
	})
}
