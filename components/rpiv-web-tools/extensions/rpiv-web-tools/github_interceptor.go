package rpiv_web_tools

import (
	"context"
	"io"
	"os"
)

// Stub: the GitHub URL interceptor is not implemented yet.

const githubTokenEnvVar = "GITHUB_TOKEN"

var ghHintWriter io.Writer = os.Stderr

type githubURLInfo struct {
	Owner, Repo, Ref string
	RefIsFullSha     bool
	Path, Type       string
}

func parseGitHubURL(url string) *githubURLInfo { return nil }

type githubUserOptions struct {
	Enabled             *bool
	MaxRepoSizeMB       *float64
	CloneTimeoutSeconds *float64
	ClonePath           *string
}

type resolvedGitHubOptions struct {
	Enabled             bool
	MaxRepoSizeMB       float64
	CloneTimeoutSeconds float64
	ClonePath           string
}

var githubDefaults resolvedGitHubOptions

func resolveGitHubOptions(user any, consumer *bool) resolvedGitHubOptions { return resolvedGitHubOptions{} }

func readUserGitHubConfig(cfg webToolsConfig) any { return nil }

type githubInterceptor struct{ options resolvedGitHubOptions }

func newGitHubInterceptor(o githubUserOptions) *githubInterceptor { return &githubInterceptor{} }

func (g *githubInterceptor) name() string { return "github" }

func (g *githubInterceptor) intercept(ctx context.Context, url string, raw bool) *fetchResponse {
	return nil
}

func (g *githubInterceptor) fetchGitHub(ctx context.Context, url string, forceClone bool) *fetchResponse {
	return nil
}

func (g *githubInterceptor) checkGhAvailable(ctx context.Context) bool { return false }

func (g *githubInterceptor) checkRepoSize(ctx context.Context, owner, repo string) (float64, bool) {
	return 0, false
}

func (g *githubInterceptor) seedCache(key, localPath string, result *string) {}

func (g *githubInterceptor) cacheKeys() []string { return nil }

func (g *githubInterceptor) reset() {}
