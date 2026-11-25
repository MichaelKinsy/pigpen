package github

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	searchLimitDefault = 10
	searchLimitMax     = 50
)

var (
	relativeDurationRe   = regexp.MustCompile(`(?i)^(\d+)\s*(m|h|d|w|mo|y)$`)
	isoDateRe            = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	repoScopeQualifierRe = regexp.MustCompile(`(?i)(?:^|\s)-?(?:repo|org|user|owner):\S`)
	repositoryAPIURLRe   = regexp.MustCompile(`/repos/([^/]+/[^/]+)$`)
)

func resolveSearchLimit(params map[string]any) (int, error) {
	raw, exists := params["limit"]
	if !exists || raw == nil {
		return searchLimitDefault, nil
	}
	var val float64
	switch v := raw.(type) {
	case float64:
		val = v
	case int:
		val = float64(v)
	default:
		return 0, fmt.Errorf("limit must be a positive number")
	}
	if math.IsNaN(val) || math.IsInf(val, 0) || val <= 0 {
		return 0, fmt.Errorf("limit must be a positive number")
	}
	n := int(math.Floor(val))
	if n <= 0 {
		return 0, fmt.Errorf("limit must be a positive number")
	}
	return min(n, searchLimitMax), nil
}

// ParseSearchDateBound resolves a relative duration, ISO date, or ISO datetime
// to a GitHub-search-compatible date literal.
// upstream: tools/gh-search.ts:66-105
func ParseSearchDateBound(raw string, now time.Time) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("date bound must not be empty")
	}
	if m := relativeDurationRe.FindStringSubmatch(trimmed); m != nil {
		count, _ := strconv.Atoi(m[1])
		unit := strings.ToLower(m[2])
		var bound time.Time
		switch unit {
		case "m":
			bound = now.UTC().Add(-time.Duration(count) * time.Minute)
		case "h":
			bound = now.UTC().Add(-time.Duration(count) * time.Hour)
		case "d":
			bound = now.UTC().Add(-time.Duration(count) * 24 * time.Hour)
		case "w":
			bound = now.UTC().Add(-time.Duration(count) * 7 * 24 * time.Hour)
		case "mo":
			bound = now.UTC().AddDate(0, -count, 0)
		case "y":
			bound = now.UTC().AddDate(-count, 0, 0)
		}
		return bound.UTC().Format("2006-01-02"), nil
	}
	if isoDateRe.MatchString(trimmed) {
		return trimmed, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05Z"), nil
		}
	}
	return "", fmt.Errorf(`invalid date bound: %s. Expected a relative duration like "3d", "12h", "2w", an ISO date "YYYY-MM-DD", or an ISO datetime.`, raw)
}

// BuildSearchDateQualifier mirrors tools/gh-search.ts:111-129.
func BuildSearchDateQualifier(field, since, until string, now time.Time) (string, error) {
	var sinceVal, untilVal string
	var err error
	if strings.TrimSpace(since) != "" {
		sinceVal, err = ParseSearchDateBound(since, now)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(until) != "" {
		untilVal, err = ParseSearchDateBound(until, now)
		if err != nil {
			return "", err
		}
	}
	switch {
	case sinceVal != "" && untilVal != "":
		return fmt.Sprintf("%s:%s..%s", field, sinceVal, untilVal), nil
	case sinceVal != "":
		return fmt.Sprintf("%s:>=%s", field, sinceVal), nil
	case untilVal != "":
		return fmt.Sprintf("%s:<=%s", field, untilVal), nil
	default:
		return "", nil
	}
}

func resolveSearchDateField(command, requested string) string {
	if command == "commits" {
		return "committer-date"
	}
	df := requested
	if df == "" {
		df = "created"
	}
	if command == "repos" && df == "updated" {
		return "pushed"
	}
	return df
}

func composeSearchQuery(parts ...string) (string, error) {
	var cleaned []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			cleaned = append(cleaned, t)
		}
	}
	if len(cleaned) == 0 {
		return "", fmt.Errorf("query is required (or pass since/until to filter by date)")
	}
	return strings.Join(cleaned, " "), nil
}

func resolveSearchRepoScope(ctx context.Context, cwd, repo, query string) string {
	if repo != "" {
		return repo
	}
	if query != "" && repoScopeQualifierRe.MatchString(query) {
		return ""
	}
	return tryResolveCurrentRepo(ctx, cwd)
}

func buildGhAPISearchArgs(endpoint, query string, limit int, host string, extraHeaders ...string) []string {
	args := []string{"api"}
	if host != "" {
		args = append(args, "--hostname", host)
	}
	args = append(args, "-X", "GET", "/search/"+endpoint, "-f", "q="+query, "-F", fmt.Sprintf("per_page=%d", limit))
	for _, h := range extraHeaders {
		args = append(args, "-H", h)
	}
	return args
}

func repoFromRepositoryURL(val string) string {
	if m := repositoryAPIURLRe.FindStringSubmatch(val); m != nil {
		return m[1]
	}
	return ""
}

type ghAPISearchResponse[T any] struct {
	TotalCount int `json:"total_count"`
	Items      []T `json:"items"`
}

type ghAPISearchIssueItem struct {
	Number        *int      `json:"number"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	User          *ghUser   `json:"user"`
	Labels        []ghLabel `json:"labels"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
	HTMLURL       string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	PullRequest   *struct {
		MergedAt string `json:"merged_at"`
	} `json:"pull_request"`
}

type ghAPISearchCodeItem struct {
	Path       string `json:"path"`
	SHA        string `json:"sha"`
	HTMLURL    string `json:"html_url"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	TextMatches []struct {
		Fragment string `json:"fragment"`
		Property string `json:"property"`
	} `json:"text_matches"`
}

type ghAPISearchCommitItem struct {
	SHA     string  `json:"sha"`
	HTMLURL string  `json:"html_url"`
	Author  *ghUser `json:"author"`
	Commit  *struct {
		Author *struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
		Committer *struct {
			Date string `json:"date"`
		} `json:"committer"`
		Message string `json:"message"`
	} `json:"commit"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type ghAPISearchRepoItem struct {
	FullName        string `json:"full_name"`
	Description     string `json:"description"`
	Language        string `json:"language"`
	StargazersCount *int   `json:"stargazers_count"`
	ForksCount      *int   `json:"forks_count"`
	OpenIssuesCount *int   `json:"open_issues_count"`
	Archived        *bool  `json:"archived"`
	Fork            *bool  `json:"fork"`
	Visibility      string `json:"visibility"`
	UpdatedAt       string `json:"updated_at"`
	HTMLURL         string `json:"html_url"`
}

func executeSearchIssuesOrPrs(ctx context.Context, cwd string, params map[string]any, isPr bool) (sdk.ToolResult, error) {
	limit, err := resolveSearchLimit(params)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	cmd := "issues"
	kindLabel := "issues"
	typeQual := "is:issue"
	if isPr {
		cmd = "prs"
		kindLabel = "pull requests"
		typeQual = "is:pr"
	}
	dateField := resolveSearchDateField(cmd, optString(params, "dateField"))
	dateQual, err := BuildSearchDateQualifier(dateField, optString(params, "since"), optString(params, "until"), time.Now())
	if err != nil {
		return sdk.ToolResult{}, err
	}
	displayQuery, err := composeSearchQuery(optString(params, "query"), dateQual)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	rawRepo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), rawRepo); err != nil {
		return sdk.ToolResult{}, err
	}
	repo := resolveSearchRepoScope(ctx, cwd, rawRepo, displayQuery)
	var repoQual, host string
	if repo != "" {
		ref := ParseRepoRef(repo)
		repoQual = "repo:" + ref.Slug
		host = ref.Host
	}
	apiQuery, err := composeSearchQuery(displayQuery, repoQual, typeQual)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	args := buildGhAPISearchArgs("issues", apiQuery, limit, host)
	var resp ghAPISearchResponse[ghAPISearchIssueItem]
	if err := ghJSON(ctx, cwd, args, &resp, defaultGhOptions()); err != nil {
		return sdk.ToolResult{}, err
	}

	count := len(resp.Items)
	lines := []string{fmt.Sprintf("# GitHub %s search", kindLabel), "", "Query: " + displayQuery}
	pushLine(&lines, "Repository", repo)
	pushLineInt(&lines, "Results", &count)
	if count == 0 {
		lines = append(lines, "", fmt.Sprintf("No %s found.", kindLabel))
	} else {
		for _, item := range resp.Items {
			num := "?"
			if item.Number != nil {
				num = fmt.Sprintf("%d", *item.Number)
			}
			title := item.Title
			if title == "" {
				title = "Untitled"
			}
			state := item.State
			if item.PullRequest != nil && item.PullRequest.MergedAt != "" {
				state = "merged"
			}
			lines = append(lines, "", fmt.Sprintf("- #%s %s", num, title))
			pushLine(&lines, "  Repo", repoFromRepositoryURL(item.RepositoryURL))
			pushLine(&lines, "  State", state)
			pushLine(&lines, "  Author", formatAuthor(item.User))
			pushLine(&lines, "  Labels", formatLabels(item.Labels))
			pushLine(&lines, "  Created", item.CreatedAt)
			pushLine(&lines, "  Updated", item.UpdatedAt)
			pushLine(&lines, "  URL", item.HTMLURL)
		}
	}
	return sdk.ToolResult{
		Content: strings.TrimSpace(strings.Join(lines, "\n")),
	}, nil
}

func executeSearchCode(ctx context.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	query := optString(params, "query")
	if query == "" {
		return sdk.ToolResult{}, fmt.Errorf("query must not be empty")
	}
	if optString(params, "since") != "" || optString(params, "until") != "" {
		return sdk.ToolResult{}, fmt.Errorf("search_code does not support since/until; GitHub code search has no date qualifier.")
	}
	limit, err := resolveSearchLimit(params)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	rawRepo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), rawRepo); err != nil {
		return sdk.ToolResult{}, err
	}
	repo := resolveSearchRepoScope(ctx, cwd, rawRepo, query)
	var repoQual, host string
	if repo != "" {
		ref := ParseRepoRef(repo)
		repoQual = "repo:" + ref.Slug
		host = ref.Host
	}
	apiQuery, err := composeSearchQuery(query, repoQual)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	args := buildGhAPISearchArgs("code", apiQuery, limit, host, "Accept: application/vnd.github.text-match+json")
	var resp ghAPISearchResponse[ghAPISearchCodeItem]
	if err := ghJSON(ctx, cwd, args, &resp, defaultGhOptions()); err != nil {
		return sdk.ToolResult{}, err
	}

	count := len(resp.Items)
	lines := []string{"# GitHub code search", "", "Query: " + query}
	pushLine(&lines, "Repository", repo)
	pushLineInt(&lines, "Results", &count)
	if count == 0 {
		lines = append(lines, "", "No code matches found.")
	} else {
		for _, item := range resp.Items {
			p := item.Path
			if p == "" {
				p = "(unknown path)"
			}
			lines = append(lines, "", "- "+p)
			if item.Repository != nil {
				pushLine(&lines, "  Repo", item.Repository.FullName)
			}
			pushLine(&lines, "  Commit", formatShortSHA(item.SHA))
			pushLine(&lines, "  URL", item.HTMLURL)
			for _, m := range item.TextMatches {
				if m.Fragment != "" {
					firstLine, _, _ := strings.Cut(normalizeText(m.Fragment), "\n")
					pushLine(&lines, "  Match", firstLine)
					break
				}
			}
		}
	}
	return sdk.ToolResult{
		Content: strings.TrimSpace(strings.Join(lines, "\n")),
	}, nil
}

func executeSearchCommits(ctx context.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	limit, err := resolveSearchLimit(params)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	dateField := resolveSearchDateField("commits", optString(params, "dateField"))
	dateQual, err := BuildSearchDateQualifier(dateField, optString(params, "since"), optString(params, "until"), time.Now())
	if err != nil {
		return sdk.ToolResult{}, err
	}
	displayQuery, err := composeSearchQuery(optString(params, "query"), dateQual)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	rawRepo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), rawRepo); err != nil {
		return sdk.ToolResult{}, err
	}
	repo := resolveSearchRepoScope(ctx, cwd, rawRepo, displayQuery)
	var repoQual, host string
	if repo != "" {
		ref := ParseRepoRef(repo)
		repoQual = "repo:" + ref.Slug
		host = ref.Host
	}
	apiQuery, err := composeSearchQuery(displayQuery, repoQual)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	args := buildGhAPISearchArgs("commits", apiQuery, limit, host)
	var resp ghAPISearchResponse[ghAPISearchCommitItem]
	if err := ghJSON(ctx, cwd, args, &resp, defaultGhOptions()); err != nil {
		return sdk.ToolResult{}, err
	}

	count := len(resp.Items)
	lines := []string{"# GitHub commits search", "", "Query: " + displayQuery}
	pushLine(&lines, "Repository", repo)
	pushLineInt(&lines, "Results", &count)
	if count == 0 {
		lines = append(lines, "", "No commits found.")
	} else {
		for _, item := range resp.Items {
			sha := formatShortSHA(item.SHA)
			if sha == "" {
				sha = "(unknown sha)"
			}
			subject := "(no commit message)"
			if item.Commit != nil && item.Commit.Message != "" {
				if first, _, _ := strings.Cut(normalizeText(item.Commit.Message), "\n"); first != "" {
					subject = first
				}
			}
			lines = append(lines, "", fmt.Sprintf("- %s %s", sha, subject))
			if item.Repository != nil {
				pushLine(&lines, "  Repo", item.Repository.FullName)
			}
			author := formatAuthor(item.Author)
			if author == "" && item.Commit != nil && item.Commit.Author != nil {
				author = item.Commit.Author.Name
			}
			pushLine(&lines, "  Author", author)
			date := ""
			if item.Commit != nil {
				if item.Commit.Author != nil && item.Commit.Author.Date != "" {
					date = item.Commit.Author.Date
				} else if item.Commit.Committer != nil {
					date = item.Commit.Committer.Date
				}
			}
			pushLine(&lines, "  Date", date)
			pushLine(&lines, "  URL", item.HTMLURL)
		}
	}
	return sdk.ToolResult{
		Content: strings.TrimSpace(strings.Join(lines, "\n")),
	}, nil
}

func executeSearchRepos(ctx context.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	limit, err := resolveSearchLimit(params)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	dateField := resolveSearchDateField("repos", optString(params, "dateField"))
	dateQual, err := BuildSearchDateQualifier(dateField, optString(params, "since"), optString(params, "until"), time.Now())
	if err != nil {
		return sdk.ToolResult{}, err
	}
	query, err := composeSearchQuery(optString(params, "query"), dateQual)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	args := buildGhAPISearchArgs("repositories", query, limit, "")
	var resp ghAPISearchResponse[ghAPISearchRepoItem]
	if err := ghJSON(ctx, cwd, args, &resp, defaultGhOptions()); err != nil {
		return sdk.ToolResult{}, err
	}

	count := len(resp.Items)
	lines := []string{"# GitHub repositories search", "", "Query: " + query}
	pushLineInt(&lines, "Results", &count)
	if count == 0 {
		lines = append(lines, "", "No repositories found.")
	} else {
		for _, item := range resp.Items {
			name := item.FullName
			if name == "" {
				name = "(unknown repository)"
			}
			lines = append(lines, "", "- "+name)
			if desc, _, _ := strings.Cut(normalizeText(item.Description), "\n"); desc != "" {
				pushLine(&lines, "  Description", desc)
			}
			pushLine(&lines, "  Language", item.Language)
			pushLineInt(&lines, "  Stars", item.StargazersCount)
			pushLineInt(&lines, "  Forks", item.ForksCount)
			pushLineInt(&lines, "  Open issues", item.OpenIssuesCount)
			pushLine(&lines, "  Visibility", item.Visibility)
			pushLineBool(&lines, "  Archived", item.Archived)
			pushLineBool(&lines, "  Fork", item.Fork)
			pushLine(&lines, "  Updated", item.UpdatedAt)
			pushLine(&lines, "  URL", item.HTMLURL)
		}
	}
	return sdk.ToolResult{
		Content: strings.TrimSpace(strings.Join(lines, "\n")),
	}, nil
}
