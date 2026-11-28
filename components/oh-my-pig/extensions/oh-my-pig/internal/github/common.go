package github

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const githubHost = "github.com"

var (
	repoURLPattern  = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/?#]+)`)
	prURLPattern    = regexp.MustCompile(`^https://([^/]+)/([^/]+/[^/]+)/pull/(\d+)(?:/.*)?$`)
	issueURLPattern = regexp.MustCompile(`^https://([^/]+)/([^/]+/[^/]+)/issues/(\d+)(?:/.*)?$`)
	digitsOnlyRe    = regexp.MustCompile(`^\d+$`)

	// ANSI escape matcher covering CSI, OSC, and 2-byte ESC sequences.
	ansiEscapeRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
)

// GhRepoRef is a repository in `[HOST/]OWNER/REPO` form.
// upstream: tools/gh-common.ts:65-70
type GhRepoRef struct {
	Host string
	Slug string
}

// ParseRepoRef splits `[HOST/]OWNER/REPO`; any other shape is taken as a slug.
// upstream: tools/gh-common.ts:73-79
func ParseRepoRef(repo string) GhRepoRef {
	first := strings.IndexByte(repo, '/')
	if first < 0 {
		return GhRepoRef{Slug: repo}
	}
	second := strings.IndexByte(repo[first+1:], '/')
	if second < 0 {
		return GhRepoRef{Slug: repo}
	}
	second += first + 1
	if strings.IndexByte(repo[second+1:], '/') >= 0 {
		return GhRepoRef{Slug: repo}
	}
	return GhRepoRef{Host: repo[:first], Slug: repo[first+1:]}
}

// FormatRepoRef joins an optional host and `OWNER/REPO`.
// upstream: tools/gh-common.ts:82-84
func FormatRepoRef(host, slug string) string {
	if host != "" {
		return host + "/" + slug
	}
	return slug
}

func ghAPIHostArgs(ref GhRepoRef) []string {
	if ref.Host != "" {
		return []string{"--hostname", ref.Host}
	}
	return nil
}

func defaultGhHost() string {
	if h := os.Getenv("GH_HOST"); h != "" {
		return strings.ToLower(h)
	}
	return githubHost
}

func effectiveHost(ref GhRepoRef) string {
	if ref.Host != "" {
		return strings.ToLower(ref.Host)
	}
	return defaultGhHost()
}

// githubRepoSlugEquals compares two repo refs case-insensitively over their effective host.
// upstream: tools/gh-common.ts:203-209
func githubRepoSlugEquals(left, right string) bool {
	if left == "" {
		return false
	}
	l := ParseRepoRef(left)
	r := ParseRepoRef(right)
	if effectiveHost(l) != effectiveHost(r) {
		return false
	}
	return strings.EqualFold(l.Slug, r.Slug)
}

func repoFromURL(value string) string {
	m := repoURLPattern.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return ""
	}
	host := strings.ToLower(m[1])
	slug := m[2] + "/" + strings.TrimSuffix(m[3], ".git")
	if host == defaultGhHost() {
		return slug
	}
	return FormatRepoRef(host, slug)
}

// ParsePositiveDecimalInt parses a digit-only positive integer.
// upstream: tools/gh-common.ts:168-173
func ParsePositiveDecimalInt(value string) (int, bool) {
	if !digitsOnlyRe.MatchString(value) {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func parsePullRequestURL(value string) (repo string, prNumber int, ok bool) {
	m := prURLPattern.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return FormatRepoRef(m[1], m[2]), n, true
}

func parseIssueURL(value string) (repo string, issueNumber int, ok bool) {
	m := issueURLPattern.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return FormatRepoRef(m[1], m[2]), n, true
}

func appendRepoFlag(args *[]string, repo, identifier string) {
	if repo == "" || strings.HasPrefix(identifier, "https://") {
		return
	}
	*args = append(*args, "--repo", repo)
}

// sanitizeText strips ANSI escape sequences and C0/C1 control characters (except
// \t and \n), matching packages/utils/src/sanitize-text.ts:25-40.
func sanitizeText(s string) string {
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, '\x1b') {
		s = ansiEscapeRe.ReplaceAllString(s, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == '\n' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.TrimSpace(s)
}

// normalizeBlock matches tools/gh-common.ts:16-20.
func normalizeBlock(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	s = sanitizeText(s)
	return strings.TrimRight(s, " \t\n\r")
}

// encodeURIComponent matches JavaScript's encodeURIComponent (RFC 2396
// unescaped: A-Z a-z 0-9 - _ . ! ~ * ' ( )).
func encodeURIComponent(s string) string {
	const hexChars = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '!' || c == '~' || c == '*' || c == '\'' || c == '(' || c == ')' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexChars[c>>4])
			b.WriteByte(hexChars[c&0x0F])
		}
	}
	return b.String()
}

func formatShortSHA(sha string) string {
	if sha == "" {
		return ""
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func pushLine(lines *[]string, label, value string) {
	if value == "" {
		return
	}
	*lines = append(*lines, fmt.Sprintf("%s: %s", label, value))
}

func pushLineInt(lines *[]string, label string, value *int) {
	if value == nil {
		return
	}
	*lines = append(*lines, fmt.Sprintf("%s: %d", label, *value))
}

func pushLineBool(lines *[]string, label string, value *bool) {
	if value == nil {
		return
	}
	*lines = append(*lines, fmt.Sprintf("%s: %t", label, *value))
}

func formatAuthor(u *ghUser) string {
	if u == nil {
		return ""
	}
	if u.Login != "" {
		return "@" + u.Login
	}
	return u.Name
}

func formatLabels(labels []ghLabel) string {
	var names []string
	for _, l := range labels {
		if l.Name != "" {
			names = append(names, l.Name)
		}
	}
	return strings.Join(names, ", ")
}

// formatBytes matches packages/utils/src/format.ts:62-67:
// "512B", "1.5KB", "2.3MB", "1.2GB".
func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fGB", float64(n)/(1024*1024*1024))
	}
}

// truncateLineBytes truncates s to at most maxBytes on a UTF-8 rune boundary,
// appending "…" when truncated.
func truncateLineBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	const ellipsis = "…"
	budget := maxBytes - len(ellipsis)
	if budget <= 0 {
		return ellipsis
	}
	end := 0
	for i := range s {
		if i > budget {
			break
		}
		end = i
	}
	if end == 0 && len(s) > 0 {
		_, size := utf8.DecodeRuneInString(s)
		if size <= budget {
			end = size
		}
	}
	return s[:end] + ellipsis
}

func outranksOrigin(name string) bool {
	lower := strings.ToLower(name)
	return lower == "upstream" || lower == "github"
}

type ghOriginView struct {
	URL    string `json:"url"`
	Parent *struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"parent"`
}

func unrelatedOrigin(ctx context.Context, cwd, picked string) string {
	remotesOut, err := runGit(ctx, cwd, "remote")
	if err != nil || remotesOut == "" {
		return ""
	}
	remotes := strings.Split(remotesOut, "\n")
	hasOrigin := false
	hasHigher := false
	for _, r := range remotes {
		r = strings.TrimSpace(r)
		if r == "origin" {
			hasOrigin = true
		}
		if outranksOrigin(r) {
			hasHigher = true
		}
	}
	if !hasOrigin || !hasHigher {
		return ""
	}
	for _, r := range remotes {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if v, err := runGit(ctx, cwd, "config", "--get", fmt.Sprintf("remote.%s.gh-resolved", r)); err == nil && v != "" {
			return ""
		}
	}
	originURL, err := runGit(ctx, cwd, "remote", "get-url", "origin")
	if err != nil || originURL == "" {
		return ""
	}
	var view ghOriginView
	if err := ghJSON(ctx, cwd, []string{"repo", "view", originURL, "--json", "url,parent"}, &view, defaultGhOptions()); err != nil {
		return ""
	}
	origin := repoFromURL(view.URL)
	if origin == "" || githubRepoSlugEquals(origin, picked) {
		return ""
	}
	if view.Parent != nil {
		parent := FormatRepoRef(ParseRepoRef(origin).Host, view.Parent.Owner.Login+"/"+view.Parent.Name)
		if githubRepoSlugEquals(parent, picked) {
			return ""
		}
	}
	return origin
}

func resolveRepoFromCwd(ctx context.Context, cwd string) (string, error) {
	urlStr, err := ghText(ctx, cwd, []string{"repo", "view", "--json", "url", "-q", ".url"}, defaultGhOptions())
	if err != nil {
		return "", err
	}
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return "", fmt.Errorf("repo must not be empty")
	}
	repo := repoFromURL(urlStr)
	if repo == "" {
		return "", fmt.Errorf("GitHub CLI returned an unrecognized repository URL: %s", urlStr)
	}
	if origin := unrelatedOrigin(ctx, cwd, repo); origin != "" {
		return origin, nil
	}
	return repo, nil
}

var defaultRepoMemo = newCwdLookupMemo(resolveRepoFromCwd)

func resolveDefaultRepoMemoized(ctx context.Context, cwd string) (string, error) {
	return defaultRepoMemo.get(ctx, cwd)
}

func tryResolveCurrentRepo(ctx context.Context, cwd string) string {
	repo, err := resolveDefaultRepoMemoized(ctx, cwd)
	if err != nil {
		return ""
	}
	return repo
}

func tryResolveCurrentRepoFresh(ctx context.Context, cwd string) string {
	repo, err := resolveRepoFromCwd(ctx, cwd)
	if err != nil {
		return ""
	}
	return repo
}

func resolveGitHubRepo(ctx context.Context, cwd, repo, runRepo string) (string, error) {
	if repo != "" && runRepo != "" && !githubRepoSlugEquals(repo, runRepo) {
		return "", fmt.Errorf("run URL repository does not match the provided repo")
	}
	if repo != "" {
		return repo, nil
	}
	if runRepo != "" {
		return runRepo, nil
	}
	return resolveRepoFromCwd(ctx, cwd)
}

func requireCurrentGitBranch(ctx context.Context, cwd string) (string, error) {
	branch, err := runGit(ctx, cwd, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch == "" {
		return "", fmt.Errorf("Current git branch is unavailable. Pass `branch` or `run` explicitly.")
	}
	return branch, nil
}

func requireCurrentGitHead(ctx context.Context, cwd string) (string, error) {
	sha, err := runGit(ctx, cwd, "rev-parse", "HEAD")
	if err != nil || sha == "" {
		return "", fmt.Errorf("Current git HEAD is unavailable. Pass `run` explicitly.")
	}
	return sha, nil
}
