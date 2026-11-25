package web

// Site-specific URL scrapers ported from omp v19.1.1:
// GitHub (blob, repo, issue, pull), npm, PyPI, crates.io, Stack Overflow / Stack Exchange,
// Wikipedia (summary + mobile-html sections), arXiv, and Hacker News (items with depth-2 replies + listings).
// upstream: packages/coding-agent/src/web/scrapers/{github,npm,pypi,crates-io,stackoverflow,wikipedia,arxiv,hackernews}.ts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Scraper base URLs (vars so tests can route against httptest servers).
var (
	githubAPIBase     = "https://api.github.com"
	githubRawBase     = "https://raw.githubusercontent.com"
	npmRegistryBase   = "https://registry.npmjs.org"
	npmDownloadsBase  = "https://api.npmjs.org"
	pypiAPIBase       = "https://pypi.org/pypi"
	pypistatsAPIBase  = "https://pypistats.org/api/packages"
	cratesAPIBase     = "https://crates.io/api/v1/crates"
	docsRsBase        = "https://docs.rs/crate"
	stackExchangeBase = "https://api.stackexchange.com/2.3"
	wikiAPIBase       = "" // empty uses https://{lang}.wikipedia.org
	arxivAPIBase      = "https://export.arxiv.org/api/query"
	hnFirebaseBase    = "https://hacker-news.firebaseio.com/v0"
)

const scraperSubTimeout = 5 * time.Second

type scraperResult struct {
	content     string
	finalURL    string
	contentType string
	method      string
	notes       []string
}

// kvPair preserves JSON object key insertion order.
type kvPair struct {
	Key   string
	Value string
}

type orderedStringMap []kvPair

func (m *orderedStringMap) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		*m = nil
		return nil
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected JSON object")
	}
	var pairs []kvPair
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := kTok.(string)
		var rawVal json.RawMessage
		if err := dec.Decode(&rawVal); err != nil {
			return err
		}
		var val string
		if err := json.Unmarshal(rawVal, &val); err != nil {
			continue
		}
		pairs = append(pairs, kvPair{Key: key, Value: val})
	}
	_, _ = dec.Token() // closing '}'
	*m = pairs
	return nil
}

func scraperGet(ctx context.Context, client *http.Client, targetURL string, timeout time.Duration, headers map[string]string) (string, bool) {
	if timeout <= 0 {
		timeout = fetchDefaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", fetchUserAgents[0])
	req.Header.Set("Accept", "application/json, text/plain, */*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBytes))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func formatNumber(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
	}
	for i := rem; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func formatUnixDate(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02")
}

// runSiteScrapers dispatches rawURL to the matching site-specific scraper, if any.
func runSiteScrapers(ctx context.Context, client *http.Client, rawURL string) *scraperResult {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil
	}
	host := strings.ToLower(u.Hostname())

	switch {
	case host == "github.com":
		return scrapeGitHub(ctx, client, u)
	case host == "www.npmjs.com" || host == "npmjs.com":
		return scrapeNpm(ctx, client, u)
	case host == "pypi.org" || host == "www.pypi.org":
		return scrapePyPI(ctx, client, u)
	case host == "crates.io" || host == "www.crates.io":
		return scrapeCratesIo(ctx, client, u)
	case host == "arxiv.org":
		return scrapeArxiv(ctx, client, u)
	case strings.Contains(host, "news.ycombinator.com"):
		return scrapeHackerNews(ctx, client, u)
	case strings.HasSuffix(host, ".wikipedia.org"):
		return scrapeWikipedia(ctx, client, u)
	default:
		if stackExchangeSiteParam(host) != "" {
			return scrapeStackOverflow(ctx, client, u)
		}
	}
	return nil
}

// =============================================================================
// 1. GitHub (blob, repo, issue, pull)
// upstream: packages/coding-agent/src/web/scrapers/github.ts
// =============================================================================

func githubHeaders() map[string]string {
	h := map[string]string{
		"Accept": "application/vnd.github.v3+json",
	}
	if tok := envTrimmed("GITHUB_TOKEN"); tok != "" {
		h["Authorization"] = "Bearer " + tok
	} else if tok := envTrimmed("GH_TOKEN"); tok != "" {
		h["Authorization"] = "Bearer " + tok
	}
	return h
}

var digitsOnlyRe = regexp.MustCompile(`^\d+$`)

type githubComment struct {
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

func fetchGitHubIssueComments(ctx context.Context, client *http.Client, owner, repo string, number, expected int) []githubComment {
	const perPage = 100
	var comments []githubComment
	headers := githubHeaders()
	for page := 1; len(comments) < expected; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=%d&page=%d", githubAPIBase, owner, repo, number, perPage, page)
		body, ok := scraperGet(ctx, client, endpoint, fetchDefaultTimeout, headers)
		if !ok {
			break
		}
		var pageComments []githubComment
		if err := json.Unmarshal([]byte(body), &pageComments); err != nil || len(pageComments) == 0 {
			break
		}
		comments = append(comments, pageComments...)
		if len(pageComments) < perPage {
			break
		}
	}
	return comments
}

func scrapeGitHub(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(parts) < 2 {
		return nil
	}
	owner, repo := parts[0], parts[1]

	// /owner/repo/blob/ref/path... -> raw file
	// upstream: packages/coding-agent/src/web/scrapers/github.ts:768-785
	if len(parts) >= 5 && parts[2] == "blob" {
		ref := parts[3]
		filePath := strings.Join(parts[4:], "/")
		rawURL := fmt.Sprintf("%s/%s/%s/%s/%s", githubRawBase, owner, repo, ref, filePath)
		body, ok := scraperGet(ctx, client, rawURL, fetchDefaultTimeout, nil)
		if !ok {
			return nil
		}
		return &scraperResult{
			content:     body,
			finalURL:    rawURL,
			contentType: "text/plain",
			method:      "github-raw",
			notes:       []string{"Fetched raw: " + rawURL},
		}
	}

	// /owner/repo/issues/123 or /owner/repo/pull/123
	// upstream: packages/coding-agent/src/web/scrapers/github.ts:191-244, 805-818
	if len(parts) >= 4 && (parts[2] == "issues" || parts[2] == "pull") && digitsOnlyRe.MatchString(parts[3]) {
		num, _ := strconv.Atoi(parts[3])
		isPR := parts[2] == "pull"
		endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", githubAPIBase, owner, repo, num)
		if isPR {
			endpoint = fmt.Sprintf("%s/repos/%s/%s/pulls/%d", githubAPIBase, owner, repo, num)
		}
		body, ok := scraperGet(ctx, client, endpoint, fetchDefaultTimeout, githubHeaders())
		if !ok {
			return nil
		}
		var issue struct {
			Title     string `json:"title"`
			Number    int    `json:"number"`
			State     string `json:"state"`
			Merged    bool   `json:"merged"`
			Body      string `json:"body"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
			Comments  int    `json:"comments"`
			User      *struct {
				Login string `json:"login"`
			} `json:"user"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		}
		if err := json.Unmarshal([]byte(body), &issue); err != nil || issue.Title == "" {
			return nil
		}
		if issue.Number == 0 {
			issue.Number = num
		}
		state := issue.State
		if isPR && issue.Merged {
			state = "merged"
		}
		login := "ghost"
		if issue.User != nil && issue.User.Login != "" {
			login = issue.User.Login
		}
		var md strings.Builder
		fmt.Fprintf(&md, "# %s\n\n", issue.Title)
		fmt.Fprintf(&md, "**#%d** · %s · opened by @%s\n", issue.Number, state, login)
		fmt.Fprintf(&md, "Created: %s · Updated: %s\n", issue.CreatedAt, issue.UpdatedAt)
		if len(issue.Labels) > 0 {
			names := make([]string, len(issue.Labels))
			for i, l := range issue.Labels {
				names[i] = l.Name
			}
			fmt.Fprintf(&md, "Labels: %s\n", strings.Join(names, ", "))
		}
		md.WriteString("\n---\n\n")
		if issue.Body != "" {
			md.WriteString(issue.Body)
		} else {
			md.WriteString("*No description provided.*")
		}
		md.WriteString("\n\n---\n\n")

		if issue.Comments > 0 {
			comments := fetchGitHubIssueComments(ctx, client, owner, repo, issue.Number, issue.Comments)
			if len(comments) > 0 {
				countStr := strconv.Itoa(len(comments))
				if issue.Comments > len(comments) {
					countStr = fmt.Sprintf("%d of %d", len(comments), issue.Comments)
				}
				fmt.Fprintf(&md, "## Comments (%s)\n\n", countStr)
				for _, c := range comments {
					cLogin := "ghost"
					if c.User != nil && c.User.Login != "" {
						cLogin = c.User.Login
					}
					fmt.Fprintf(&md, "### @%s · %s\n\n%s\n\n---\n\n", cLogin, c.CreatedAt, c.Body)
				}
			}
		}

		method := "github-issue"
		if isPR {
			method = "github-pr"
		}
		return &scraperResult{
			content: md.String(),
			method:  method,
			notes:   []string{"Fetched via GitHub API"},
		}
	}

	// /owner/repo -> repo metadata + file tree + README
	// upstream: packages/coding-agent/src/web/scrapers/github.ts:485-541, 830-837
	if len(parts) == 2 {
		headers := githubHeaders()
		var repoBody, treeBody, readmeBody string
		var repoOK, treeOK, readmeOK bool
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			repoBody, repoOK = scraperGet(ctx, client, fmt.Sprintf("%s/repos/%s/%s", githubAPIBase, owner, repo), fetchDefaultTimeout, headers)
		}()
		go func() {
			defer wg.Done()
			treeBody, treeOK = scraperGet(ctx, client, fmt.Sprintf("%s/repos/%s/%s/git/trees/HEAD?recursive=1", githubAPIBase, owner, repo), fetchDefaultTimeout, headers)
		}()
		go func() {
			defer wg.Done()
			readmeBody, readmeOK = scraperGet(ctx, client, fmt.Sprintf("%s/repos/%s/%s/readme", githubAPIBase, owner, repo), fetchDefaultTimeout, headers)
		}()
		wg.Wait()

		if !repoOK {
			return nil
		}
		var info struct {
			FullName        string `json:"full_name"`
			Description     string `json:"description"`
			StargazersCount int    `json:"stargazers_count"`
			ForksCount      int    `json:"forks_count"`
			OpenIssuesCount int    `json:"open_issues_count"`
			Language        string `json:"language"`
			License         *struct {
				Name string `json:"name"`
			} `json:"license"`
		}
		if err := json.Unmarshal([]byte(repoBody), &info); err != nil || info.FullName == "" {
			return nil
		}
		var md strings.Builder
		fmt.Fprintf(&md, "# %s\n\n", info.FullName)
		if info.Description != "" {
			fmt.Fprintf(&md, "%s\n\n", info.Description)
		}
		fmt.Fprintf(&md, "Stars: %d · Forks: %d · Issues: %d\n", info.StargazersCount, info.ForksCount, info.OpenIssuesCount)
		if info.Language != "" {
			fmt.Fprintf(&md, "Language: %s\n", info.Language)
		}
		if info.License != nil && info.License.Name != "" {
			fmt.Fprintf(&md, "License: %s\n", info.License.Name)
		}
		md.WriteString("\n---\n\n")

		if treeOK {
			var treeResp struct {
				Tree []struct {
					Path string `json:"path"`
					Type string `json:"type"`
				} `json:"tree"`
			}
			if json.Unmarshal([]byte(treeBody), &treeResp) == nil && len(treeResp.Tree) > 0 {
				md.WriteString("## Files\n\n```\n")
				limit := min(len(treeResp.Tree), 100)
				for _, item := range treeResp.Tree[:limit] {
					prefix := "      "
					if item.Type == "tree" {
						prefix = "[dir] "
					}
					fmt.Fprintf(&md, "%s%s\n", prefix, item.Path)
				}
				if len(treeResp.Tree) > 100 {
					fmt.Fprintf(&md, "[…%d files elided…]\n", len(treeResp.Tree)-100)
				}
				md.WriteString("```\n\n")
			}
		}

		if readmeOK {
			var rm struct {
				Content  string `json:"content"`
				Encoding string `json:"encoding"`
			}
			if json.Unmarshal([]byte(readmeBody), &rm) == nil && rm.Encoding == "base64" {
				cleaned := strings.ReplaceAll(strings.ReplaceAll(rm.Content, "\n", ""), "\r", "")
				if decoded, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
					fmt.Fprintf(&md, "## README\n\n%s", string(decoded))
				}
			}
		}

		return &scraperResult{
			content: md.String(),
			method:  "github-repo",
			notes:   []string{"Fetched via GitHub API"},
		}
	}

	return nil
}

// =============================================================================
// 2. npm
// upstream: packages/coding-agent/src/web/scrapers/npm.ts
// =============================================================================

var npmPkgRe = regexp.MustCompile(`^/package/(.+?)(?:/|$)`)
var npmScopedRe = regexp.MustCompile(`^/package/(@[^/]+/[^/]+)`)

func scrapeNpm(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	m := npmPkgRe.FindStringSubmatch(u.Path)
	if m == nil {
		return nil
	}
	pkgName, _ := url.PathUnescape(m[1])
	if strings.HasPrefix(pkgName, "@") {
		if sm := npmScopedRe.FindStringSubmatch(u.Path); sm != nil {
			pkgName, _ = url.PathUnescape(sm[1])
		}
	}
	latestURL := fmt.Sprintf("%s/%s/latest", npmRegistryBase, pkgName)
	dlURL := fmt.Sprintf("%s/downloads/point/last-week/%s", npmDownloadsBase, url.PathEscape(pkgName))

	var body, dlBody string
	var ok, dlOK bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		body, ok = scraperGet(ctx, client, latestURL, fetchDefaultTimeout, nil)
	}()
	go func() {
		defer wg.Done()
		dlBody, dlOK = scraperGet(ctx, client, dlURL, scraperSubTimeout, nil)
	}()
	wg.Wait()

	if !ok {
		return nil
	}
	var pkg struct {
		Name        string   `json:"name"`
		Version     string   `json:"version"`
		Description string   `json:"description"`
		License     any      `json:"license"`
		Homepage    string   `json:"homepage"`
		Repository  any      `json:"repository"`
		Keywords    []string `json:"keywords"`
		Maintainers []struct {
			Name string `json:"name"`
		} `json:"maintainers"`
		Dependencies orderedStringMap `json:"dependencies"`
		Readme       string           `json:"readme"`
	}
	if err := json.Unmarshal([]byte(body), &pkg); err != nil || pkg.Name == "" {
		return nil
	}

	var weeklyDownloads *int
	if dlOK {
		var dl struct {
			Downloads *int `json:"downloads"`
		}
		if json.Unmarshal([]byte(dlBody), &dl) == nil {
			weeklyDownloads = dl.Downloads
		}
	}

	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", pkg.Name)
	if pkg.Description != "" {
		fmt.Fprintf(&md, "%s\n\n", pkg.Description)
	}
	ver := pkg.Version
	if ver == "" {
		ver = "unknown"
	}
	fmt.Fprintf(&md, "**Latest:** %s", ver)
	if lic := extractStringOrField(pkg.License, "type"); lic != "" {
		fmt.Fprintf(&md, " · **License:** %s", lic)
	}
	md.WriteString("\n")
	if weeklyDownloads != nil {
		fmt.Fprintf(&md, "**Weekly Downloads:** %s\n", formatNumber(*weeklyDownloads))
	}
	md.WriteString("\n")
	if pkg.Homepage != "" {
		fmt.Fprintf(&md, "**Homepage:** %s\n", pkg.Homepage)
	}
	if repoURL := extractStringOrField(pkg.Repository, "url"); repoURL != "" {
		repoURL = strings.TrimPrefix(repoURL, "git+")
		repoURL = strings.TrimSuffix(repoURL, ".git")
		fmt.Fprintf(&md, "**Repository:** %s\n", repoURL)
	}
	if len(pkg.Keywords) > 0 {
		fmt.Fprintf(&md, "**Keywords:** %s\n", strings.Join(pkg.Keywords, ", "))
	}
	if len(pkg.Maintainers) > 0 {
		names := make([]string, len(pkg.Maintainers))
		for i, m := range pkg.Maintainers {
			names[i] = m.Name
		}
		fmt.Fprintf(&md, "**Maintainers:** %s\n", strings.Join(names, ", "))
	}
	if len(pkg.Dependencies) > 0 {
		md.WriteString("\n## Dependencies\n\n")
		for _, kv := range pkg.Dependencies {
			fmt.Fprintf(&md, "- %s: %s\n", kv.Key, kv.Value)
		}
	}
	if pkg.Readme != "" {
		fmt.Fprintf(&md, "\n---\n\n## README\n\n%s\n", pkg.Readme)
	}
	return &scraperResult{
		content: md.String(),
		method:  "npm",
		notes:   []string{"Fetched via npm registry"},
	}
}

func extractStringOrField(v any, field string) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if s, ok := x[field].(string); ok {
			return s
		}
	}
	return ""
}

// =============================================================================
// 3. PyPI
// upstream: packages/coding-agent/src/web/scrapers/pypi.ts
// =============================================================================

var pypiPkgRe = regexp.MustCompile(`^/project/([^/]+)`)

func scrapePyPI(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	m := pypiPkgRe.FindStringSubmatch(u.Path)
	if m == nil {
		return nil
	}
	pkgName, _ := url.PathUnescape(m[1])
	var body, dlBody string
	var ok, dlOK bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		body, ok = scraperGet(ctx, client, fmt.Sprintf("%s/%s/json", pypiAPIBase, pkgName), fetchDefaultTimeout, nil)
	}()
	go func() {
		defer wg.Done()
		dlBody, dlOK = scraperGet(ctx, client, fmt.Sprintf("%s/%s/recent", pypistatsAPIBase, pkgName), scraperSubTimeout, nil)
	}()
	wg.Wait()

	if !ok {
		return nil
	}
	var pkg struct {
		Info struct {
			Name           string           `json:"name"`
			Version        string           `json:"version"`
			Summary        string           `json:"summary"`
			Description    string           `json:"description"`
			Author         string           `json:"author"`
			AuthorEmail    string           `json:"author_email"`
			License        string           `json:"license"`
			HomePage       string           `json:"home_page"`
			ProjectURLs    orderedStringMap `json:"project_urls"`
			RequiresPython string           `json:"requires_python"`
			Keywords       string           `json:"keywords"`
		} `json:"info"`
		RequiresDist []string `json:"requires_dist"`
	}
	if err := json.Unmarshal([]byte(body), &pkg); err != nil || pkg.Info.Name == "" {
		return nil
	}

	var weeklyDownloads *int
	if dlOK {
		var dl struct {
			Data struct {
				LastWeek *int `json:"last_week"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(dlBody), &dl) == nil {
			weeklyDownloads = dl.Data.LastWeek
		}
	}

	info := pkg.Info
	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", info.Name)
	if info.Summary != "" {
		fmt.Fprintf(&md, "%s\n\n", info.Summary)
	}
	fmt.Fprintf(&md, "**Latest:** %s", info.Version)
	if info.License != "" {
		fmt.Fprintf(&md, " · **License:** %s", info.License)
	}
	md.WriteString("\n")
	if weeklyDownloads != nil {
		fmt.Fprintf(&md, "**Weekly Downloads:** %s\n", formatNumber(*weeklyDownloads))
	}
	md.WriteString("\n")
	if info.Author != "" {
		fmt.Fprintf(&md, "**Author:** %s", info.Author)
		if info.AuthorEmail != "" {
			fmt.Fprintf(&md, " <%s>", info.AuthorEmail)
		}
		md.WriteString("\n")
	}
	if info.RequiresPython != "" {
		fmt.Fprintf(&md, "**Python:** %s\n", info.RequiresPython)
	}
	if info.HomePage != "" {
		fmt.Fprintf(&md, "**Homepage:** %s\n", info.HomePage)
	}
	if len(info.ProjectURLs) > 0 {
		md.WriteString("\n**Project URLs:**\n")
		for _, kv := range info.ProjectURLs {
			fmt.Fprintf(&md, "- %s: %s\n", kv.Key, kv.Value)
		}
	}
	if info.Keywords != "" {
		fmt.Fprintf(&md, "\n**Keywords:** %s\n", info.Keywords)
	}
	if len(pkg.RequiresDist) > 0 {
		md.WriteString("\n## Dependencies\n\n")
		for _, dep := range pkg.RequiresDist {
			fmt.Fprintf(&md, "- %s\n", dep)
		}
	}
	if info.Description != "" {
		fmt.Fprintf(&md, "\n---\n\n## Description\n\n%s\n", info.Description)
	}
	return &scraperResult{
		content: md.String(),
		method:  "pypi",
		notes:   []string{"Fetched via PyPI JSON API"},
	}
}

// =============================================================================
// 4. crates.io
// upstream: packages/coding-agent/src/web/scrapers/crates-io.ts
// =============================================================================

var cratesPkgRe = regexp.MustCompile(`^/crates/([^/]+)`)

func scrapeCratesIo(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	m := cratesPkgRe.FindStringSubmatch(u.Path)
	if m == nil {
		return nil
	}
	crateName, _ := url.PathUnescape(m[1])
	body, ok := scraperGet(ctx, client, fmt.Sprintf("%s/%s", cratesAPIBase, crateName), fetchDefaultTimeout, nil)
	if !ok {
		return nil
	}
	var data struct {
		Crate struct {
			Name            string   `json:"name"`
			Description     string   `json:"description"`
			Downloads       int      `json:"downloads"`
			RecentDownloads int      `json:"recent_downloads"`
			MaxVersion      string   `json:"max_version"`
			Repository      string   `json:"repository"`
			Homepage        string   `json:"homepage"`
			Documentation   string   `json:"documentation"`
			Categories      []string `json:"categories"`
			Keywords        []string `json:"keywords"`
		} `json:"crate"`
		Versions []struct {
			Num         string `json:"num"`
			Downloads   int    `json:"downloads"`
			CreatedAt   string `json:"created_at"`
			License     string `json:"license"`
			RustVersion string `json:"rust_version"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil || data.Crate.Name == "" {
		return nil
	}

	c := data.Crate
	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", c.Name)
	if c.Description != "" {
		fmt.Fprintf(&md, "%s\n\n", c.Description)
	}
	fmt.Fprintf(&md, "**Latest:** %s", c.MaxVersion)
	if len(data.Versions) > 0 {
		if data.Versions[0].License != "" {
			fmt.Fprintf(&md, " · **License:** %s", data.Versions[0].License)
		}
		if data.Versions[0].RustVersion != "" {
			fmt.Fprintf(&md, " · **MSRV:** %s", data.Versions[0].RustVersion)
		}
	}
	md.WriteString("\n")
	fmt.Fprintf(&md, "**Downloads:** %s total · %s recent\n\n", formatNumber(c.Downloads), formatNumber(c.RecentDownloads))
	if c.Repository != "" {
		fmt.Fprintf(&md, "**Repository:** %s\n", c.Repository)
	}
	if c.Homepage != "" && c.Homepage != c.Repository {
		fmt.Fprintf(&md, "**Homepage:** %s\n", c.Homepage)
	}
	if c.Documentation != "" {
		fmt.Fprintf(&md, "**Docs:** %s\n", c.Documentation)
	}
	if len(c.Keywords) > 0 {
		fmt.Fprintf(&md, "**Keywords:** %s\n", strings.Join(c.Keywords, ", "))
	}
	if len(c.Categories) > 0 {
		fmt.Fprintf(&md, "**Categories:** %s\n", strings.Join(c.Categories, ", "))
	}
	if len(data.Versions) > 0 {
		md.WriteString("\n## Recent Versions\n\n")
		limit := min(len(data.Versions), 5)
		for _, v := range data.Versions[:limit] {
			date := strings.Split(v.CreatedAt, "T")[0]
			fmt.Fprintf(&md, "- **%s** (%s) - %s downloads\n", v.Num, date, formatNumber(v.Downloads))
		}
	}
	readmeURL := fmt.Sprintf("%s/%s/%s/source/README.md", docsRsBase, crateName, c.MaxVersion)
	if readme, ok := scraperGet(ctx, client, readmeURL, scraperSubTimeout, nil); ok && len(readme) > 100 && !looksLikeHTML(readme) {
		fmt.Fprintf(&md, "\n---\n\n## README\n\n%s\n", readme)
	}
	return &scraperResult{
		content: md.String(),
		method:  "crates.io",
		notes:   []string{"Fetched via crates.io API"},
	}
}

// =============================================================================
// 5. Stack Overflow / Stack Exchange
// upstream: packages/coding-agent/src/web/scrapers/stackoverflow.ts
// =============================================================================

var standaloneSESites = map[string]string{
	"stackoverflow.com": "stackoverflow",
	"superuser.com":     "superuser",
	"serverfault.com":   "serverfault",
	"askubuntu.com":     "askubuntu",
	"mathoverflow.net":  "mathoverflow",
	"stackapps.com":     "stackapps",
}

var seSubdomainRe = regexp.MustCompile(`^([a-z0-9-]+)\.stackexchange\.com$`)
var soQuestionRe = regexp.MustCompile(`/questions/(\d+)`)

func stackExchangeSiteParam(hostname string) string {
	host := strings.TrimPrefix(strings.ToLower(hostname), "www.")
	if s, ok := standaloneSESites[host]; ok {
		return s
	}
	if m := seSubdomainRe.FindStringSubmatch(host); m != nil {
		return m[1]
	}
	return ""
}

func scrapeStackOverflow(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	site := stackExchangeSiteParam(u.Hostname())
	if site == "" {
		return nil
	}
	m := soQuestionRe.FindStringSubmatch(u.Path)
	if m == nil {
		return nil
	}
	qid := m[1]
	qURL := fmt.Sprintf("%s/questions/%s?order=desc&sort=votes&site=%s&filter=withbody", stackExchangeBase, qid, site)
	qBody, ok := scraperGet(ctx, client, qURL, fetchDefaultTimeout, nil)
	if !ok {
		return nil
	}
	var qData struct {
		Items []struct {
			Title        string   `json:"title"`
			Body         string   `json:"body"`
			Score        int      `json:"score"`
			CreationDate int64    `json:"creation_date"`
			Tags         []string `json:"tags"`
			AnswerCount  int      `json:"answer_count"`
			IsAnswered   bool     `json:"is_answered"`
			Owner        struct {
				DisplayName string `json:"display_name"`
			} `json:"owner"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(qBody), &qData); err != nil || len(qData.Items) == 0 {
		return nil
	}

	q := qData.Items[0]
	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", q.Title)
	fmt.Fprintf(&md, "**Score:** %d · **Answers:** %d", q.Score, q.AnswerCount)
	if q.IsAnswered {
		md.WriteString(" (Answered)")
	}
	fmt.Fprintf(&md, "\n**Tags:** %s\n", strings.Join(q.Tags, ", "))
	fmt.Fprintf(&md, "**Asked by:** %s · %s\n\n", q.Owner.DisplayName, formatUnixDate(q.CreationDate))
	fmt.Fprintf(&md, "---\n\n## Question\n\n%s\n\n", HTMLToMarkdown(q.Body, u.String()))

	aURL := fmt.Sprintf("%s/questions/%s/answers?order=desc&sort=votes&site=%s&filter=withbody", stackExchangeBase, qid, site)
	if aBody, ok := scraperGet(ctx, client, aURL, fetchDefaultTimeout, nil); ok {
		var aData struct {
			Items []struct {
				Body       string `json:"body"`
				Score      int    `json:"score"`
				IsAccepted bool   `json:"is_accepted"`
				Owner      struct {
					DisplayName string `json:"display_name"`
				} `json:"owner"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(aBody), &aData) == nil && len(aData.Items) > 0 {
			md.WriteString("---\n\n## Answers\n\n")
			limit := min(len(aData.Items), 5)
			for _, a := range aData.Items[:limit] {
				accepted := ""
				if a.IsAccepted {
					accepted = " (Accepted)"
				}
				fmt.Fprintf(&md, "### Score: %d%s · by %s\n\n%s\n\n---\n\n", a.Score, accepted, a.Owner.DisplayName, HTMLToMarkdown(a.Body, u.String()))
			}
		}
	}

	return &scraperResult{
		content: md.String(),
		method:  "stackexchange",
		notes:   []string{fmt.Sprintf("Fetched via Stack Exchange API (site=%s)", site)},
	}
}

// =============================================================================
// 6. Wikipedia (REST summary + mobile-html article sections)
// upstream: packages/coding-agent/src/web/scrapers/wikipedia.ts
// =============================================================================

var (
	wikiHostRe    = regexp.MustCompile(`^(\w+)\.wikipedia\.org$`)
	wikiPathRe    = regexp.MustCompile(`/wiki/(.+)`)
	wikiSkipHeads = map[string]bool{
		"References":      true,
		"External links":  true,
		"See also":        true,
		"Notes":           true,
		"Further reading": true,
	}
)

func findSectionHeading(sec *htmlNode) (tag, text string) {
	var walk func(n *htmlNode) bool
	walk = func(n *htmlNode) bool {
		for _, ch := range n.children {
			if ch.kind != nodeElement {
				continue
			}
			if ch.tag == "section" {
				continue
			}
			if ch.tag == "h2" || ch.tag == "h3" || ch.tag == "h4" {
				tag = strings.ToUpper(ch.tag)
				text = strings.TrimSpace(collectPlainText(ch))
				return true
			}
			if walk(ch) {
				return true
			}
		}
		return false
	}
	walk(sec)
	return tag, text
}

func renderWikiSections(root *htmlNode, md *strings.Builder) {
	var walkSection func(sec *htmlNode)
	var walkChildren func(n *htmlNode, inSection bool)

	walkChildren = func(n *htmlNode, inSection bool) {
		for _, ch := range n.children {
			if ch.kind != nodeElement {
				continue
			}
			switch ch.tag {
			case "section":
				walkSection(ch)
			case "p":
				if inSection {
					txt := strings.TrimSpace(collectPlainText(ch))
					if utf8.RuneCountInString(txt) > 20 {
						fmt.Fprintf(md, "%s\n\n", txt)
					}
				}
			default:
				walkChildren(ch, inSection)
			}
		}
	}

	walkSection = func(sec *htmlNode) {
		headingTag, headingText := findSectionHeading(sec)
		if headingText != "" && wikiSkipHeads[headingText] {
			return
		}
		if headingText != "" {
			level := "###"
			if headingTag == "H2" {
				level = "##"
			}
			fmt.Fprintf(md, "%s %s\n\n", level, headingText)
		}
		walkChildren(sec, true)
	}

	walkChildren(root, false)
}

func scrapeWikipedia(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	hm := wikiHostRe.FindStringSubmatch(strings.ToLower(u.Hostname()))
	if hm == nil {
		return nil
	}
	pm := wikiPathRe.FindStringSubmatch(u.Path)
	if pm == nil {
		return nil
	}
	lang := hm[1]
	title, _ := url.PathUnescape(pm[1])
	base := wikiAPIBase
	if base == "" {
		base = fmt.Sprintf("https://%s.wikipedia.org", lang)
	}
	summaryURL := fmt.Sprintf("%s/api/rest_v1/page/summary/%s", base, url.PathEscape(title))
	contentURL := fmt.Sprintf("%s/api/rest_v1/page/mobile-html/%s", base, url.PathEscape(title))

	var md strings.Builder
	if body, ok := scraperGet(ctx, client, summaryURL, fetchDefaultTimeout, nil); ok {
		var summary struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Extract     string `json:"extract"`
		}
		if json.Unmarshal([]byte(body), &summary) == nil && summary.Title != "" {
			fmt.Fprintf(&md, "# %s\n\n", summary.Title)
			if summary.Description != "" {
				fmt.Fprintf(&md, "*%s*\n\n", summary.Description)
			}
			fmt.Fprintf(&md, "%s\n\n---\n\n", summary.Extract)
		}
	}

	if mobileHTML, ok := scraperGet(ctx, client, contentURL, fetchDefaultTimeout, nil); ok {
		renderWikiSections(parseHTMLTree(mobileHTML), &md)
	}

	if md.Len() == 0 {
		return nil
	}
	return &scraperResult{
		content: md.String(),
		method:  "wikipedia",
		notes:   []string{"Fetched via Wikipedia API"},
	}
}

// =============================================================================
// 7. arXiv
// upstream: packages/coding-agent/src/web/scrapers/arxiv.ts
// =============================================================================

var arxivPathRe = regexp.MustCompile(`/(abs|pdf)/(.+?)(?:\.pdf)?$`)

func scrapeArxiv(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	m := arxivPathRe.FindStringSubmatch(u.Path)
	if m == nil {
		return nil
	}
	paperID := m[2]
	apiURL := fmt.Sprintf("%s?id_list=%s", arxivAPIBase, url.QueryEscape(paperID))
	body, ok := scraperGet(ctx, client, apiURL, fetchDefaultTimeout, nil)
	if !ok {
		return nil
	}
	var feed struct {
		Entries []struct {
			Title     string `xml:"title"`
			Summary   string `xml:"summary"`
			Published string `xml:"published"`
			Authors   []struct {
				Name string `xml:"name"`
			} `xml:"author"`
			Categories []struct {
				Term string `xml:"term,attr"`
			} `xml:"category"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal([]byte(body), &feed); err != nil || len(feed.Entries) == 0 {
		return nil
	}
	e := feed.Entries[0]
	title := strings.TrimSpace(wsCollapseRe.ReplaceAllString(e.Title, " "))
	if title == "" {
		title = "arXiv Paper"
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", title)
	if len(e.Authors) > 0 {
		names := make([]string, 0, len(e.Authors))
		for _, a := range e.Authors {
			if n := strings.TrimSpace(a.Name); n != "" {
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&md, "**Authors:** %s\n", strings.Join(names, ", "))
		}
	}
	if pub := strings.TrimSpace(e.Published); pub != "" {
		fmt.Fprintf(&md, "**Published:** %s\n", strings.Split(pub, "T")[0])
	}
	if len(e.Categories) > 0 {
		cats := make([]string, 0, len(e.Categories))
		for _, c := range e.Categories {
			if c.Term != "" {
				cats = append(cats, c.Term)
			}
		}
		if len(cats) > 0 {
			fmt.Fprintf(&md, "**Categories:** %s\n", strings.Join(cats, ", "))
		}
	}
	fmt.Fprintf(&md, "**arXiv:** %s\n\n", paperID)
	abstract := strings.TrimSpace(e.Summary)
	if abstract == "" {
		abstract = "No abstract available."
	}
	fmt.Fprintf(&md, "---\n\n## Abstract\n\n%s\n", abstract)
	return &scraperResult{
		content: md.String(),
		method:  "arxiv",
		notes:   []string{"Fetched via arXiv API"},
	}
}

// =============================================================================
// 8. Hacker News (items with depth-2 replies + top/new/best listings)
// upstream: packages/coding-agent/src/web/scrapers/hackernews.ts
// =============================================================================

type hnAPIItem struct {
	ID          int    `json:"id"`
	Deleted     bool   `json:"deleted"`
	Dead        bool   `json:"dead"`
	Type        string `json:"type"`
	By          string `json:"by"`
	Time        int64  `json:"time"`
	Text        string `json:"text"`
	URL         string `json:"url"`
	Score       *int   `json:"score"`
	Title       string `json:"title"`
	Descendants int    `json:"descendants"`
	Kids        []int  `json:"kids"`
}

func formatHNTimestamp(unixTime int64) string {
	diff := nowFunc().Sub(time.Unix(unixTime, 0))
	hours := int(diff.Hours())
	days := hours / 24
	if days > 7 {
		return formatUnixDate(unixTime)
	}
	if days > 0 {
		return fmt.Sprintf("%dd ago", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh ago", hours)
	}
	mins := int(diff.Minutes())
	return fmt.Sprintf("%dm ago", mins)
}

func fetchHNItem(ctx context.Context, client *http.Client, id int) *hnAPIItem {
	body, ok := scraperGet(ctx, client, fmt.Sprintf("%s/item/%d.json", hnFirebaseBase, id), fetchDefaultTimeout, nil)
	if !ok {
		return nil
	}
	var item hnAPIItem
	if err := json.Unmarshal([]byte(body), &item); err != nil || item.Deleted || item.Dead {
		return nil
	}
	return &item
}

func fetchHNItems(ctx context.Context, client *http.Client, ids []int, limit int) []*hnAPIItem {
	if len(ids) > limit {
		ids = ids[:limit]
	}
	results := make([]*hnAPIItem, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(idx, itemID int) {
			defer wg.Done()
			results[idx] = fetchHNItem(ctx, client, itemID)
		}(i, id)
	}
	wg.Wait()
	out := make([]*hnAPIItem, 0, len(ids))
	for _, r := range results {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

func renderHNStory(ctx context.Context, client *http.Client, item *hnAPIItem, depth int) string {
	var out strings.Builder
	if depth == 0 {
		fmt.Fprintf(&out, "# %s\n\n", item.Title)
		if item.URL != "" {
			fmt.Fprintf(&out, "**URL:** %s\n\n", item.URL)
		}
		score := 0
		if item.Score != nil {
			score = *item.Score
		}
		fmt.Fprintf(&out, "**Posted by:** %s | **Score:** %d | **Time:** %s", item.By, score, formatHNTimestamp(item.Time))
		if item.Descendants > 0 {
			fmt.Fprintf(&out, " | **Comments:** %d", item.Descendants)
		}
		out.WriteString("\n\n")
	}

	if item.Text != "" {
		fmt.Fprintf(&out, "%s\n\n", decodeHNText(item.Text))
	}

	if len(item.Kids) > 0 && depth < 2 {
		limit := 10
		if depth == 0 {
			limit = 20
		}
		comments := fetchHNItems(ctx, client, item.Kids, limit)
		if len(comments) > 0 {
			if depth == 0 {
				out.WriteString("---\n\n## Comments\n\n")
			}
			childOutputs := make([]string, len(comments))
			if depth < 1 {
				sem := make(chan struct{}, 2)
				var wg sync.WaitGroup
				for i, c := range comments {
					if len(c.Kids) == 0 {
						continue
					}
					wg.Add(1)
					go func(idx int, cm *hnAPIItem) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()
						childOutputs[idx] = renderHNStory(ctx, client, cm, depth+1)
					}(i, c)
				}
				wg.Wait()
			}
			indent := strings.Repeat("  ", depth)
			for i, c := range comments {
				fmt.Fprintf(&out, "%s**%s** (%s)", indent, c.By, formatHNTimestamp(c.Time))
				if c.Score != nil {
					fmt.Fprintf(&out, " [%d]", *c.Score)
				}
				out.WriteByte('\n')
				if c.Text != "" {
					lines := strings.Split(decodeHNText(c.Text), "\n")
					for j, line := range lines {
						lines[j] = indent + line
					}
					out.WriteString(strings.Join(lines, "\n"))
					out.WriteString("\n\n")
				}
				out.WriteString(childOutputs[i])
			}
		}
	}

	return out.String()
}

func renderHNListing(ctx context.Context, client *http.Client, ids []int, title string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", title)
	stories := fetchHNItems(ctx, client, ids, 20)
	for i, s := range stories {
		score := 0
		if s.Score != nil {
			score = *s.Score
		}
		fmt.Fprintf(&out, "%d. **%s**\n", i+1, s.Title)
		if s.URL != "" {
			fmt.Fprintf(&out, "   %s\n", s.URL)
		}
		fmt.Fprintf(&out, "   %d points by %s | %s", score, s.By, formatHNTimestamp(s.Time))
		if s.Descendants > 0 {
			fmt.Fprintf(&out, " | %d comments", s.Descendants)
		}
		fmt.Fprintf(&out, "\n   https://news.ycombinator.com/item?id=%d\n\n", s.ID)
	}
	return out.String()
}

func scrapeHackerNews(ctx context.Context, client *http.Client, u *url.URL) *scraperResult {
	if itemID := u.Query().Get("id"); itemID != "" {
		id, err := strconv.Atoi(itemID)
		if err != nil || id <= 0 {
			return nil
		}
		item := fetchHNItem(ctx, client, id)
		if item == nil {
			return &scraperResult{
				content: fmt.Sprintf("# Error fetching Hacker News content\n\nFailed to fetch item %s", itemID),
				method:  "hackernews",
				notes:   []string{"Error: Failed to fetch item " + itemID},
			}
		}
		return &scraperResult{
			content: renderHNStory(ctx, client, item, 0),
			method:  "hackernews",
			notes:   []string{fmt.Sprintf("Fetched HN item %s with top-level comments (depth 2)", itemID)},
		}
	}

	var endpoint, title, note string
	switch u.Path {
	case "", "/", "/news":
		endpoint = hnFirebaseBase + "/topstories.json"
		title = "Hacker News - Top Stories"
		note = "Fetched top 20 stories from HN front page"
	case "/newest":
		endpoint = hnFirebaseBase + "/newstories.json"
		title = "Hacker News - New Stories"
		note = "Fetched top 20 new stories"
	case "/best":
		endpoint = hnFirebaseBase + "/beststories.json"
		title = "Hacker News - Best Stories"
		note = "Fetched top 20 best stories"
	default:
		return nil
	}

	raw, ok := scraperGet(ctx, client, endpoint, fetchDefaultTimeout, nil)
	if !ok {
		return nil
	}
	var ids []int
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return &scraperResult{
		content: renderHNListing(ctx, client, ids, title),
		method:  "hackernews",
		notes:   []string{note},
	}
}
