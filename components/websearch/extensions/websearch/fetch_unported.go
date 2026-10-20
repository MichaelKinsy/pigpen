package websearch

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Classification of the URLs that pi-web-access's extract.ts hands to the GitHub extractors.
// This port has none of them yet, so a URL that the original would route to one is refused
// instead of being fetched as a plain page.

// sectionEnabled reads `<section>.enabled` from web-search.json: true unless it is a boolean false.
func sectionEnabled(section string) (bool, error) {
	root, err := readLenientConfig()
	if err != nil {
		return false, err
	}
	obj, _ := root[section].(map[string]any)
	if obj == nil {
		return true, nil
	}
	enabled, ok := obj["enabled"].(bool)
	return !ok || enabled, nil
}

var (
	ghOwnerRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	ghRepoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	ghDigits  = regexp.MustCompile(`^\d+$`)
)

var ghNonCodeSegments = []string{"issues", "pull", "pulls", "discussions", "releases", "wiki", "actions", "settings", "security", "projects", "graphs", "compare", "commits", "tags", "branches", "stargazers", "watchers", "network", "forks", "milestone", "labels", "packages", "codespaces", "contribute", "community", "sponsors", "invitations", "notifications", "insights"}

var ghPrSubpaths = []string{"files", "commits", "checks", "conversation"}

// githubSegments returns the decoded path segments of a github.com URL, or nil.
func githubSegments(raw string) []string {
	u, err := ParseURL(raw, nil)
	if err != nil {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return nil
	}
	var segments []string
	for _, s := range strings.Split(u.EscapedPath(), "/") {
		if s == "" {
			continue
		}
		d, err := url.PathUnescape(s)
		if err != nil {
			return nil
		}
		segments = append(segments, d)
	}
	return segments
}

// isGitHubRepoURL is github-extract.ts parseGitHubUrl: a repository root, blob or tree URL.
func isGitHubRepoURL(raw string) bool {
	segments := githubSegments(raw)
	if len(segments) < 2 {
		return false
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	if !ghOwnerRE.MatchString(owner) || strings.Contains(owner, "--") || !ghRepoRE.MatchString(repo) || repo == "." || repo == ".." {
		return false
	}
	if len(segments) > 2 && sliceHas(ghNonCodeSegments, strings.ToLower(segments[2])) {
		return false
	}
	if len(segments) == 2 {
		return true
	}
	if segments[2] != "blob" && segments[2] != "tree" || len(segments) < 4 {
		return false
	}
	ref := segments[3]
	if ref == "" || len(ref) > 1024 {
		return false
	}
	for _, c := range ref {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// isGitHubIssuePRURL is github-issue-pr.ts parseGitHubIssuePrUrl.
func isGitHubIssuePRURL(raw string) bool {
	segments := githubSegments(raw)
	if len(segments) < 4 {
		return false
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	if !ghOwnerRE.MatchString(owner) || strings.Contains(owner, "--") || !ghRepoRE.MatchString(repo) || repo == "." || repo == ".." {
		return false
	}
	route := strings.ToLower(segments[2])
	if route != "pull" && route != "issues" || !ghDigits.MatchString(segments[3]) {
		return false
	}
	sub := ""
	if len(segments) > 4 {
		sub = strings.ToLower(segments[4])
	}
	if route == "pull" && sub != "" && !sliceHas(ghPrSubpaths, sub) {
		return false
	}
	if route == "issues" && sub != "" {
		return false
	}
	n := segments[3]
	return strings.Trim(n, "0") != ""
}

// unportedFetchFeature names the feature of a specialised extractor that the original would use
// for rawURL and this port lacks ("" when the URL is a plain page). The error is a configuration
// parse failure, reported like the original does.
func unportedFetchFeature(rawURL string) (string, error) {
	if isGitHubIssuePRURL(rawURL) {
		if on, err := sectionEnabled("githubPrIssue"); err != nil {
			return "", err
		} else if on {
			return "GitHub issue and pull request extraction", nil
		}
	}
	if isGitHubRepoURL(rawURL) {
		if on, err := sectionEnabled("githubClone"); err != nil {
			return "", err
		} else if on {
			return "GitHub repository extraction (clone)", nil
		}
	}
	return "", nil
}

// plainError is an extraction failure that is reported verbatim as the URL's error.
type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

// fetchProviderRequested reports whether the original would call the hosted fetch provider p at
// this point: its availability check (a base URL, a key, or always for the keyless ones).
func fetchProviderRequested(p string) bool {
	cfgString := func(key string) string {
		root, _ := readLenientConfig()
		s, _ := root[key].(string)
		return strings.TrimSpace(s)
	}
	switch p {
	case "firecrawl":
		return strings.TrimSpace(os.Getenv("FIRECRAWL_BASE_URL")) != "" || cfgString("firecrawlBaseUrl") != ""
	case "crawl4ai":
		return strings.TrimSpace(os.Getenv("CRAWL4AI_BASE_URL")) != "" || cfgString("crawl4aiBaseUrl") != ""
	case "parallel-mcp":
		return true
	}
	return isProviderAvailable(p)
}

func fetchProviderLabel(p string) string {
	if p == "crawl4ai" {
		return "Crawl4AI"
	}
	return ProviderLabel(p)
}
