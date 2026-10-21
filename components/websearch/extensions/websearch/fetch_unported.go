package websearch

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Classification of the URLs that pi-web-access's extract.ts hands to a specialised extractor
// (YouTube, local video, GitHub). This port has none of those extractors yet, so a URL that the
// original would route to one is refused instead of being fetched as a plain page.

var youtubeRE = regexp.MustCompile(`(?:(?:www\.|m\.)?youtube\.com/(?:watch\?.*v=|shorts/|live/|embed/|v/)|youtu\.be/)([a-zA-Z0-9_-]{11})`)

// isYouTubeURL is youtube-extract.ts isYouTubeURL.
func isYouTubeURL(raw string) bool {
	if u, err := ParseURL(raw, nil); err == nil && u.Path == "/playlist" {
		return false
	}
	return youtubeRE.MatchString(raw)
}

var videoExtensions = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".avi": true, ".mpeg": true, ".mpg": true, ".wmv": true, ".flv": true, ".3gp": true, ".3gpp": true}

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

// isLocalVideoFile is video-extract.ts isVideoFile: a path (or file: URL) with a video extension
// that names an existing regular file, while video.enabled is not false.
func isLocalVideoFile(input string) (bool, error) {
	enabled, err := sectionEnabled("video")
	if err != nil || !enabled {
		return false, err
	}
	if !(strings.HasPrefix(input, "/") || strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") || strings.HasPrefix(input, "file://")) {
		return false, nil
	}
	path := input
	if strings.HasPrefix(input, "file://") {
		u, err := url.Parse(input)
		if err != nil {
			return false, nil
		}
		path = u.Path
	}
	if !videoExtensions[strings.ToLower(filepath.Ext(path))] {
		return false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, nil
	}
	st, err := os.Stat(abs)
	return err == nil && st.Mode().IsRegular(), nil
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
func unportedFetchFeature(rawURL string, o ExtractOptions) (string, error) {
	local, err := isLocalVideoFile(rawURL)
	if err != nil {
		return "", err
	}
	yt := isYouTubeURL(rawURL)
	if o.Frames > 0 || o.Timestamp != "" {
		switch {
		case yt || local:
			return "video frame and timestamp extraction", nil
		case o.Timestamp != "":
			return "", &plainError{"Timestamp extraction only works with YouTube and local video files"}
		default:
			return "", &plainError{"Frame extraction only works with YouTube and local video files"}
		}
	}
	if local {
		return "local video file analysis", nil
	}
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
	if yt {
		if on, err := sectionEnabled("youtube"); err != nil {
			return "", err
		} else if on {
			return "YouTube video extraction", nil
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
