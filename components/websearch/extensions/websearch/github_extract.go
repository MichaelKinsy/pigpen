package websearch

import (
	"context"
	"io"
	"runtime"
	"sync"
)

// Port of github-extract.ts. Compilable stubs: the twins in github_extract_test.go fail until the
// implementation lands.

// githubURL is GitHubUrlInfo: Ref is empty for a repository root.
type githubURL struct {
	Owner, Repo, Ref string
	RefIsFullSHA     bool
	Path, Type       string
}

func parseGitHubURL(raw string) *githubURL { return nil }

type githubCloneConfig struct {
	Enabled             bool
	MaxRepoSizeMB       float64
	CloneTimeoutSeconds float64
	ClonePath           string
}

func normalizeClonePath(value any, fallback string) string { return fallback }

// githubState is the module state of github-extract.ts, github-api.ts and github-issue-pr.ts of
// one process (the original keeps it in module variables).
type githubState struct {
	mu          sync.Mutex
	pid         int
	beforeSweep func()
	sweeps      sync.WaitGroup
	hintOut     io.Writer
}

func newGitHubState() *githubState { return &githubState{} }

func (s *githubState) cloneConfig() (githubCloneConfig, error) { return githubCloneConfig{}, nil }

func (s *githubState) extractGitHub(ctx context.Context, rawURL string, forceClone bool) (*ExtractedContent, error) {
	return nil, nil
}

func (s *githubState) clearCloneCache() {}

// nodePlatform is process.platform for this build.
func nodePlatform() string { return runtime.GOOS }
