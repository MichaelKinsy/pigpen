package websearch

import "context"

// Port of github-issue-pr.ts. Compilable stubs: the twins in github_issue_pr_test.go fail until the
// implementation lands.

type githubIssuePRURL struct {
	Owner, Repo, Kind string
	Number            int
	Anchor            string
}

func parseGitHubIssuePRURL(raw string) *githubIssuePRURL { return nil }

// issuePRRender is RenderData.
type issuePRRender struct {
	URL, Owner, Repo, Kind   string
	Number                   int
	Anchor                   string
	View                     map[string]any
	ReviewThreads            []map[string]any
	ReviewThreadsBounded     bool
	ReviewThreadsUnavailable bool
	FallbackNotes            []string
}

func renderGitHubPRIssue(d issuePRRender) ExtractedContent { return ExtractedContent{} }

func (s *githubState) extractGitHubIssuePR(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	return nil, nil
}
