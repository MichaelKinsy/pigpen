// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The GitHub URL interceptor's two pure halves: what a URL is, and whether the interceptor is on. upstream:
// providers/interceptors/github.ts parseGitHubUrl and resolveGitHubOptions, plus readUserGitHubConfig.
//
// The clone and API halves that follow these need gh, git and the GitHub API, so they live with the rest of slice 6's
// work; nothing here touches the network or the disk.

// gitHubTokenEnvVar is the token variable. upstream: github.ts GITHUB_TOKEN_ENV_VAR.
const gitHubTokenEnvVar = "GITHUB_TOKEN"

// nonCodeSegments are the path segments that belong to GitHub's own pages rather than to code, so a URL under them is
// not the interceptor's business. upstream: github.ts NON_CODE_SEGMENTS.
var nonCodeSegments = map[string]bool{
	"issues": true, "pull": true, "pulls": true, "discussions": true, "releases": true, "wiki": true,
	"actions": true, "settings": true, "security": true, "projects": true, "graphs": true, "compare": true,
	"commits": true, "tags": true, "branches": true, "stargazers": true, "watchers": true, "network": true,
	"forks": true, "milestone": true, "labels": true, "packages": true, "codespaces": true, "contribute": true,
	"community": true, "sponsors": true, "invitations": true, "notifications": true, "insights": true,
}

// The three URL shapes the interceptor owns. upstream: GitHubUrlInfo's type union.
const (
	githubURLRoot = "root"
	githubURLBlob = "blob"
	githubURLTree = "tree"
)

// gitHubURLInfo is what a parsed URL means. RefIsFullSha distinguishes a pinned commit from a branch or tag, which is
// what decides between the clone path and the API path. upstream: GitHubUrlInfo.
type gitHubURLInfo struct {
	Owner        string
	Repo         string
	Ref          string
	HasRef       bool
	RefIsFullSHA bool
	Path         string
	HasPath      bool
	Type         string
}

// fullSHARegex is the pinned-commit shape. upstream: the /^[0-9a-f]{40}$/ test in parseGitHubUrl.
var fullSHARegex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// parseGitHubURL decides whether a fetch target is this interceptor's, and what it is asking for. Anything that is not
// a github.com code URL returns nil so the chain falls through to the next interceptor. upstream:
// providers/interceptors/github.ts parseGitHubUrl.
func parseGitHubURL(raw string) (*gitHubURLInfo, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return nil, false
	}

	segments := []string{}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "" {
			continue
		}
		// A segment that will not decode stays as it arrived rather than failing the whole URL.
		if decoded, err := url.PathUnescape(segment); err == nil {
			segments = append(segments, decoded)
		} else {
			segments = append(segments, segment)
		}
	}
	if len(segments) < 2 {
		return nil, false
	}

	owner := segments[0]
	repo := strings.TrimSuffix(segments[1], ".git")

	if len(segments) > 2 && nonCodeSegments[strings.ToLower(segments[2])] {
		return nil, false
	}
	if len(segments) == 2 {
		return &gitHubURLInfo{Owner: owner, Repo: repo, Type: githubURLRoot}, true
	}

	action := segments[2]
	if action != githubURLBlob && action != githubURLTree {
		return nil, false
	}
	if len(segments) < 4 {
		return nil, false
	}

	ref := segments[3]
	info := &gitHubURLInfo{
		Owner:        owner,
		Repo:         repo,
		Ref:          ref,
		HasRef:       true,
		RefIsFullSHA: fullSHARegex.MatchString(ref),
		Type:         action,
	}
	if pathParts := segments[4:]; len(pathParts) > 0 {
		info.Path, info.HasPath = strings.Join(pathParts, "/"), true
	}
	return info, true
}

// resolvedGitHubOptions is the stanza with every default filled in. upstream: ResolvedGitHubOptions.
type resolvedGitHubOptions struct {
	Enabled             bool
	MaxRepoSizeMB       float64
	CloneTimeoutSeconds float64
	ClonePath           string
}

// gitHubInterceptorDefaults is the default stanza: off, 350 MB, 30 seconds, a temp clone directory. upstream: github.ts
// DEFAULTS.
func gitHubInterceptorDefaults() resolvedGitHubOptions {
	return resolvedGitHubOptions{
		Enabled:             false,
		MaxRepoSizeMB:       350,
		CloneTimeoutSeconds: 30,
		ClonePath:           filepath.Join(os.TempDir(), "pi-github-repos"),
	}
}

// userGitHubConfig is the interceptor stanza as the user configured it: absent, a boolean or an object. upstream:
// github.ts readUserGitHubConfig's return type.
type userGitHubConfig struct {
	Set      bool
	Disabled bool // the boolean false arm
	Object   *githubInterceptorOptions
}

// readUserGitHubConfig reads the stanza off the canonical config, delegating validation and the fail-soft handling to
// the slice 1 reader, so the orchestrator and the interceptor see the same parsed object. upstream:
// providers/interceptors/github.ts readUserGitHubConfig.
func readUserGitHubConfig() userGitHubConfig {
	cfg := ReadConfig()
	if cfg.Interceptors == nil || cfg.Interceptors.GitHub == nil {
		return userGitHubConfig{}
	}
	gh := cfg.Interceptors.GitHub
	if gh.Object != nil {
		return userGitHubConfig{Set: true, Object: gh.Object}
	}
	return userGitHubConfig{Set: true, Disabled: gh.Disabled}
}

// resolveGitHubOptions is the two-tier opt-in: the user's stanza wins over the consumer's programmatic default. The
// object form implies opt-in, `enabled: false` inside an object is redundant but accepted, and a boolean false at
// either tier turns the interceptor off regardless of lower-tier overrides. upstream: github.ts resolveGitHubOptions.
func resolveGitHubOptions(user userGitHubConfig, consumerDefault bool) resolvedGitHubOptions {
	base := gitHubInterceptorDefaults()
	switch {
	case user.Set && user.Disabled:
		base.Enabled = false
		return base
	case user.Set && user.Object != nil:
		base.Enabled = user.Object.Enabled == nil || *user.Object.Enabled
		if user.Object.MaxRepoSizeMB != nil {
			base.MaxRepoSizeMB = *user.Object.MaxRepoSizeMB
		}
		if user.Object.CloneTimeoutSeconds != nil {
			base.CloneTimeoutSeconds = *user.Object.CloneTimeoutSeconds
		}
		if user.Object.ClonePath != nil {
			base.ClonePath = *user.Object.ClonePath
		}
		return base
	case user.Set: // the boolean true arm
		base.Enabled = true
		return base
	case consumerDefault:
		base.Enabled = true
	}
	return base
}
