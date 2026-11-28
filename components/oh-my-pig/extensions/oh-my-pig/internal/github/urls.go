package github

import (
	"context"
	_ "embed"
	"fmt"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

//go:embed issue.md
var issueDoc string

//go:embed issue-reference.md
var issueReferenceDoc string

//go:embed mr.md
var mrDoc string

const (
	listLimitDefault = 30
	listLimitMax     = 100
)

func resolveContextCwd(c urls.Context) string {
	if c.Cwd != "" {
		return c.Cwd
	}
	return c.SDK.Cwd()
}
func resolveGoCtx(c urls.Context) context.Context {
	ctx := context.Background()
	var zero sdk.Context
	if c.SDK != zero {
		if sig := c.SDK.Signal(); sig != nil {
			ctx = sig
		}
		ctx = withSessionID(ctx, sessionIDForSDK(c.SDK))
	}
	return ctx
}

// splitInternalURLSegments mirrors internal-urls/forge-diff.ts:37-57.
func splitInternalURLSegments(u urls.URL, scheme string) ([]string, error) {
	rawPath := strings.TrimPrefix(u.Path, "/")
	if rawPath == "" {
		return nil, nil
	}
	rawParts := strings.Split(rawPath, "/")
	parts := make([]string, 0, len(rawParts))
	for _, seg := range rawParts {
		decoded, err := url.PathUnescape(seg)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." {
			return nil, fmt.Errorf("Invalid %s:// URL: empty or unsafe path segment", scheme)
		}
		parts = append(parts, seg)
	}
	return parts, nil
}

type diffFamilyLabels struct {
	baseURL string
	heading string
	noun    string
	query   string
}

func formatDiffFileLine(idx int, f PrDiffFile, baseURL, query string) string {
	stats := fmt.Sprintf("+%d -%d", f.Additions, f.Deletions)
	if f.ChangeType == "binary" {
		stats = "(binary)"
	}
	rename := ""
	if f.OldPath != "" {
		rename = fmt.Sprintf("  (renamed from %s)", f.OldPath)
	}
	return fmt.Sprintf("%d. %s  %s  [%s]%s\n   %s/diff/%d%s", idx, f.Path, stats, f.ChangeType, rename, baseURL, idx, query)
}

// renderDiffFamilyResource mirrors internal-urls/forge-diff.ts:76-141.
func renderDiffFamilyResource(
	u urls.URL,
	mode string,
	index int,
	lookup ViewLookupResult[PrDiffPayload],
	labels diffFamilyLabels,
) (*urls.Resource, error) {
	files := lookup.Payload.Files
	freshness := FormatFreshnessNote(lookup.Status, lookup.FetchedAt, time.Now())
	fileCount := fmt.Sprintf("%d file", len(files))
	if len(files) != 1 {
		fileCount += "s"
	}
	notes := []string{freshness}
	if labels.query != "" {
		notes = append(notes, "Pinned snapshot")
	}

	switch mode {
	case "all":
		notes = append(notes, fmt.Sprintf("Full diff for %s (%s)", labels.baseURL, fileCount))
		return &urls.Resource{
			URL:         u.Href,
			Content:     lookup.Payload.Unified,
			ContentType: "text/plain",
			Notes:       notes,
		}, nil
	case "slice":
		if index < 1 || index > len(files) {
			return nil, fmt.Errorf("%s/diff/%d is out of range; %s has %s. Use %s/diff to list available indices.", labels.baseURL, index, labels.noun, fileCount, labels.baseURL)
		}
		f := files[index-1]
		start := max(0, min(f.StartOffset, len(lookup.Payload.Unified)))
		end := max(start, min(f.EndOffset, len(lookup.Payload.Unified)))
		content := lookup.Payload.Unified[start:end]
		notes = append(notes,
			fmt.Sprintf("Showing file %d/%d: %s", index, len(files), f.Path),
			fmt.Sprintf("Read all: %s/diff/all%s", labels.baseURL, labels.query),
		)
		return &urls.Resource{
			URL:         u.Href,
			Content:     content,
			ContentType: "text/plain",
			Notes:       notes,
		}, nil
	default:
		header := fmt.Sprintf("# %s (%s)", labels.heading, fileCount)
		body := "_No file changes._"
		if len(files) > 0 {
			lines := make([]string, len(files))
			for i, f := range files {
				lines[i] = formatDiffFileLine(i+1, f, labels.baseURL, labels.query)
			}
			body = strings.Join(lines, "\n\n")
		}
		footer := fmt.Sprintf("\n\n---\nRead all: `%s/diff/all%s`. Each file is also available as `%s/diff/<i>%s`.", labels.baseURL, labels.query, labels.baseURL, labels.query)
		notes = append(notes, fmt.Sprintf("File listing for %s", labels.baseURL))
		return &urls.Resource{
			URL:         u.Href,
			Content:     header + "\n\n" + body + footer,
			ContentType: "text/markdown",
			Notes:       notes,
		}, nil
	}
}

// ── issue:// and pr:// ───────────────────────────────────────────────────────

type parsedForgeURL struct {
	kind      string // "single", "list", "pr-diff"
	repo      string
	number    int
	comments  bool
	diffMode  string // "list", "all", "slice"
	diffIndex int
	fetchedAt *int64
	state     string
	limit     int
	author    string
	label     string
}

func parseListOptions(u urls.URL, scheme, repo string) (parsedForgeURL, error) {
	allowed := []string{"open", "closed", "all"}
	if scheme == "pr" {
		allowed = []string{"open", "closed", "merged", "all"}
	}
	state := "open"
	if u.Query.Has("state") {
		raw := u.Query.Get("state")
		if !slices.Contains(allowed, raw) {
			return parsedForgeURL{}, fmt.Errorf("Invalid %s:// list state '%s'. Expected one of: %s.", scheme, raw, strings.Join(allowed, ", "))
		}
		state = raw
	}
	limit := listLimitDefault
	if u.Query.Has("limit") {
		raw := u.Query.Get("limit")
		n, ok := ParsePositiveDecimalInt(raw)
		if !ok {
			return parsedForgeURL{}, fmt.Errorf("Invalid %s:// list limit '%s'. Expected a positive integer (max %d).", scheme, raw, listLimitMax)
		}
		limit = min(n, listLimitMax)
	}
	return parsedForgeURL{
		kind:   "list",
		repo:   repo,
		state:  state,
		limit:  limit,
		author: u.Query.Get("author"),
		label:  u.Query.Get("label"),
	}, nil
}

// parseIssuePrURL mirrors internal-urls/issue-pr-protocol.ts:113-222.
func parseIssuePrURL(u urls.URL, scheme string) (parsedForgeURL, error) {
	host := u.Host
	parts, err := splitInternalURLSegments(u, scheme)
	if err != nil {
		return parsedForgeURL{}, err
	}

	var repoHost string
	dottedHost := strings.Contains(host, ".")
	if dottedHost && len(parts) < 2 {
		return parsedForgeURL{}, fmt.Errorf("Invalid %s:// URL. Expected %s://<host>/<owner>/<repo> or %s://<host>/<owner>/<repo>/<number>", scheme, scheme, scheme)
	}
	hostPrefixed := false
	if dottedHost {
		hostPrefixed = len(parts) >= 2
	} else if len(parts) >= 3 {
		_, ok := ParsePositiveDecimalInt(parts[2])
		hostPrefixed = ok
	}
	if hostPrefixed {
		repoHost = host
		host = parts[0]
		parts = parts[1:]
	}

	var repo, numPart string
	var diffParts []string

	switch {
	case host == "" && len(parts) == 0:
		return parseListOptions(u, scheme, "")
	case host != "" && len(parts) == 0:
		numPart = host
	case len(parts) >= 1 && parts[0] == "diff":
		if _, ok := ParsePositiveDecimalInt(host); ok {
			numPart = host
			diffParts = parts
		} else if len(parts) == 1 {
			repo = FormatRepoRef(repoHost, host+"/"+parts[0])
			return parseListOptions(u, scheme, repo)
		} else {
			repo = FormatRepoRef(repoHost, host+"/"+parts[0])
			numPart = parts[1]
			diffParts = parts[2:]
		}
	case host != "" && len(parts) == 1:
		repo = FormatRepoRef(repoHost, host+"/"+parts[0])
		return parseListOptions(u, scheme, repo)
	case host != "" && len(parts) >= 2:
		repo = FormatRepoRef(repoHost, host+"/"+parts[0])
		numPart = parts[1]
		diffParts = parts[2:]
	default:
		return parsedForgeURL{}, fmt.Errorf("Invalid %s:// URL. Expected %s://, %s://<number>, %s://<owner>/<repo>, or %s://<owner>/<repo>/<number>", scheme, scheme, scheme, scheme, scheme)
	}

	if len(diffParts) > 0 {
		if scheme == "issue" {
			return parsedForgeURL{}, fmt.Errorf("Invalid issue:// URL. Issue views do not have a diff; use pr://<owner>/<repo>/<n>/diff for pull requests.")
		}
		if diffParts[0] != "diff" || len(diffParts) > 2 {
			return parsedForgeURL{}, fmt.Errorf("Invalid pr:// URL. Expected pr://<n>, pr://<n>/diff, pr://<n>/diff/all, or pr://<n>/diff/<i>")
		}
	}

	num, ok := ParsePositiveDecimalInt(numPart)
	if !ok {
		if numPart == "" {
			numPart = "(missing)"
		}
		return parsedForgeURL{}, fmt.Errorf("Invalid %s:// number: %s", scheme, numPart)
	}

	if len(diffParts) == 0 {
		comments := true
		if u.Query.Has("comments") {
			v := strings.ToLower(u.Query.Get("comments"))
			comments = !(v == "0" || v == "false")
		}
		return parsedForgeURL{
			kind:     "single",
			repo:     repo,
			number:   num,
			comments: comments,
		}, nil
	}

	var fetchedAt *int64
	if u.Query.Has("at") {
		raw := u.Query.Get("at")
		if !digitsOnlyRe.MatchString(raw) {
			return parsedForgeURL{}, fmt.Errorf("Invalid pr:// diff snapshot '%s'. Expected a positive integer cache generation.", raw)
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			return parsedForgeURL{}, fmt.Errorf("Invalid pr:// diff snapshot '%s'. Expected a positive integer cache generation.", raw)
		}
		fetchedAt = &v
	}

	if len(diffParts) == 1 {
		return parsedForgeURL{kind: "pr-diff", repo: repo, number: num, diffMode: "list", fetchedAt: fetchedAt}, nil
	}
	sub := diffParts[1]
	if sub == "all" {
		return parsedForgeURL{kind: "pr-diff", repo: repo, number: num, diffMode: "all", fetchedAt: fetchedAt}, nil
	}
	idx, ok := ParsePositiveDecimalInt(sub)
	if !ok {
		return parsedForgeURL{}, fmt.Errorf("Invalid pr:// diff sub-path '%s'. Use 'all' or a 1-indexed file number.", sub)
	}
	return parsedForgeURL{kind: "pr-diff", repo: repo, number: num, diffMode: "slice", diffIndex: idx, fetchedAt: fetchedAt}, nil
}

type listItem struct {
	Number      *int      `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	Author      *ghUser   `json:"author"`
	Labels      []ghLabel `json:"labels"`
	CreatedAt   string    `json:"createdAt"`
	UpdatedAt   string    `json:"updatedAt"`
	URL         string    `json:"url"`
	IsDraft     bool      `json:"isDraft"`
	BaseRefName string    `json:"baseRefName"`
	HeadRefName string    `json:"headRefName"`
}

func formatListItem(scheme, repo string, item listItem) string {
	numStr := "?"
	itemURL := fmt.Sprintf("%s://%s", scheme, repo)
	if item.Number != nil {
		numStr = strconv.Itoa(*item.Number)
		itemURL = fmt.Sprintf("%s://%s/%d", scheme, repo, *item.Number)
	}
	title := item.Title
	if title == "" {
		title = "(no title)"
	}
	state := strings.ToLower(item.State)
	if state == "" {
		state = "?"
	}
	author := "?"
	if item.Author != nil && item.Author.Login != "" {
		author = item.Author.Login
	}
	updated := item.UpdatedAt
	if updated == "" {
		updated = item.CreatedAt
	}
	draftSuffix := ""
	if scheme == "pr" && item.IsDraft {
		draftSuffix = " [draft]"
	}
	labelSuffix := ""
	if l := formatLabels(item.Labels); l != "" {
		labelSuffix = "  labels: " + l
	}
	return fmt.Sprintf("- [%s%s] #%s  @%s  %s\n    %s%s\n    %s", state, draftSuffix, numStr, author, updated, title, labelSuffix, itemURL)
}

func fetchAndRenderList(c urls.Context, scheme string, parsed parsedForgeURL, u urls.URL) (*urls.Resource, error) {
	ctx := resolveGoCtx(c)
	cwd := resolveContextCwd(c)
	repo := parsed.repo
	if repo == "" {
		var err error
		repo, err = resolveDefaultRepoMemoized(ctx, cwd)
		if err != nil {
			return nil, fmt.Errorf("%s:// could not resolve a default repo from the current session: %w\nUse %s://<owner>/<repo> instead.", scheme, err, scheme)
		}
	}

	fields := "number,title,state,author,labels,createdAt,updatedAt,url"
	if scheme == "pr" {
		fields = "number,title,state,isDraft,author,baseRefName,headRefName,labels,createdAt,updatedAt,url"
	}
	args := []string{
		scheme, "list",
		"--repo", repo,
		"--state", parsed.state,
		"--limit", strconv.Itoa(parsed.limit),
		"--json", fields,
	}
	if parsed.author != "" {
		args = append(args, "--author", parsed.author)
	}
	if parsed.label != "" {
		args = append(args, "--label", parsed.label)
	}

	var items []listItem
	var err error
	if scheme == "issue" {
		err = githubIssueJSONWithStateReasonFallback(ctx, cwd, args, &items, ghCommandOptions{repoProvided: true, trimOutput: true})
	} else {
		err = ghJSON(ctx, cwd, args, &items, ghCommandOptions{repoProvided: true, trimOutput: true})
	}
	if err != nil {
		return nil, err
	}

	header := fmt.Sprintf("# Issues in %s (%s, up to %d)", repo, parsed.state, parsed.limit)
	if scheme == "pr" {
		header = fmt.Sprintf("# Pull Requests in %s (%s, up to %d)", repo, parsed.state, parsed.limit)
	}
	body := "_No matches._"
	if len(items) > 0 {
		formatted := make([]string, len(items))
		for i, it := range items {
			formatted[i] = formatListItem(scheme, repo, it)
		}
		body = strings.Join(formatted, "\n\n")
	}
	footer := fmt.Sprintf("\n\n---\nRead a specific item: `%s://%s/<N>` (or `%s://<N>` for the current repo).", scheme, repo, scheme)
	return &urls.Resource{
		URL:         u.Href,
		Content:     header + "\n\n" + body + footer,
		ContentType: "text/markdown",
		Notes:       []string{"Live listing for " + repo},
	}, nil
}

func buildSingleResource(u urls.URL, scheme string, parsed parsedForgeURL, rendered string, status CacheStatus, fetchedAt int64, repo string) *urls.Resource {
	notes := []string{FormatFreshnessNote(status, fetchedAt, time.Now())}
	if !parsed.comments {
		notes = append(notes, "Comments disabled")
	}
	if scheme == "pr" {
		seg := repo
		if seg == "" {
			seg = parsed.repo
		}
		diffURL := fmt.Sprintf("pr://%d/diff", parsed.number)
		if seg != "" {
			diffURL = fmt.Sprintf("pr://%s/%d/diff", seg, parsed.number)
		}
		notes = append(notes, "Diff: "+diffURL)
	}
	content := rendered
	if status == CacheStatusStale {
		content = fmt.Sprintf("> WARNING: Live GitHub refresh failed; this %s content is cached and may be stale.\n\n%s", scheme, rendered)
	}
	return &urls.Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/markdown",
		Notes:       notes,
	}
}

type issueHandler struct{}

func (issueHandler) Scheme() string { return "issue" }
func (issueHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingRemote, Selectors: urls.SelectorsLines, Immutable: true}
}
func (issueHandler) PromptDoc(_ urls.SchemeHost) string {
	if !ghAvailable() {
		return ""
	}
	return strings.TrimSpace(issueDoc)
}
func (issueHandler) PromptReference() string {
	if !ghAvailable() {
		return ""
	}
	return strings.TrimSpace(issueReferenceDoc)
}

func (issueHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	ctx := resolveGoCtx(c)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("aborted")
	}
	parsed, err := parseIssuePrURL(u, "issue")
	if err != nil {
		return nil, err
	}
	if err := validateRepoParam(sessionIDFromCtx(ctx), parsed.repo); err != nil {
		return nil, fmt.Errorf("issue:// resolution failed: %w", err)
	}
	if parsed.kind == "list" {
		res, err := fetchAndRenderList(c, "issue", parsed, u)
		if err != nil {
			return nil, fmt.Errorf("issue:// listing failed: %w", err)
		}
		return res, nil
	}
	lookup, err := getOrFetchIssue(ctx, c.SDK, resolveContextCwd(c), parsed.repo, strconv.Itoa(parsed.number), parsed.comments)
	if err != nil {
		return nil, fmt.Errorf("issue:// resolution failed: %w", err)
	}
	return buildSingleResource(u, "issue", parsed, lookup.Rendered, lookup.Status, lookup.FetchedAt, parsed.repo), nil
}

type prHandler struct{}

func (prHandler) Scheme() string { return "pr" }
func (prHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingRemote, Selectors: urls.SelectorsLines, Immutable: true}
}

func (prHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	ctx := resolveGoCtx(c)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("aborted")
	}
	parsed, err := parseIssuePrURL(u, "pr")
	if err != nil {
		return nil, err
	}
	if err := validateRepoParam(sessionIDFromCtx(ctx), parsed.repo); err != nil {
		return nil, fmt.Errorf("pr:// resolution failed: %w", err)
	}
	if parsed.kind == "list" {
		res, err := fetchAndRenderList(c, "pr", parsed, u)
		if err != nil {
			return nil, fmt.Errorf("pr:// listing failed: %w", err)
		}
		return res, nil
	}
	cwd := resolveContextCwd(c)
	repo := parsed.repo
	if repo == "" {
		repo, err = resolveDefaultRepoMemoized(ctx, cwd)
		if err != nil {
			if parsed.kind == "pr-diff" {
				return nil, fmt.Errorf("pr:// diff resolution failed: pr://%d/diff could not resolve a default repo from the current session: %w\nUse pr://<owner>/<repo>/%d/diff.", parsed.number, err, parsed.number)
			}
			return nil, fmt.Errorf("pr://%d could not resolve a default repo from the current session: %w\nUse pr://<owner>/<repo>/%d.", parsed.number, err, parsed.number)
		}
	}

	if parsed.kind == "pr-diff" {
		var lookup ViewLookupResult[PrDiffPayload]
		if parsed.fetchedAt != nil {
			var ok bool
			lookup, ok = GetPinnedPrDiff(c.SDK, repo, parsed.number, *parsed.fetchedAt)
			if !ok {
				return nil, fmt.Errorf("pr:// diff resolution failed: pr://%s/%d/diff?at=%d is not available: the pinned snapshot expired, the GitHub cache is disabled, or it was saved under another GitHub account. Run /review again.", repo, parsed.number, *parsed.fetchedAt)
			}
		} else {
			lookup, err = getOrFetchPrDiff(ctx, c.SDK, cwd, repo, parsed.number)
			if err != nil {
				return nil, fmt.Errorf("pr:// diff resolution failed: %w", err)
			}
		}
		query := ""
		if parsed.fetchedAt != nil {
			query = fmt.Sprintf("?at=%d", *parsed.fetchedAt)
		}
		res, err := renderDiffFamilyResource(u, parsed.diffMode, parsed.diffIndex, lookup, diffFamilyLabels{
			baseURL: fmt.Sprintf("pr://%s/%d", repo, parsed.number),
			heading: fmt.Sprintf("Pull Request Diff: %s#%d", repo, parsed.number),
			noun:    "PR",
			query:   query,
		})
		if err != nil {
			return nil, fmt.Errorf("pr:// diff resolution failed: %w", err)
		}
		return res, nil
	}

	lookup, err := getOrFetchPr(ctx, c.SDK, cwd, repo, parsed.number, parsed.comments)
	if err != nil {
		return nil, fmt.Errorf("pr:// resolution failed: %w", err)
	}
	return buildSingleResource(u, "pr", parsed, lookup.Rendered, lookup.Status, lookup.FetchedAt, repo), nil
}

// ── mr:// (GitLab via glab) ──────────────────────────────────────────────────

const invalidMrURLMsg = "Invalid mr:// URL. Expected mr://<iid>/diff or mr://<host>/<group>/<project>/<iid>/diff, optionally followed by /all or /<i>."

type parsedMrURL struct {
	project string
	iid     int
	mode    string // "list", "all", "slice"
	index   int
}

// parseMrURL mirrors internal-urls/mr-protocol.ts:35-75.
func parseMrURL(u urls.URL) (parsedMrURL, error) {
	host := u.Host
	segs, err := splitInternalURLSegments(u, "mr")
	if err != nil {
		return parsedMrURL{}, err
	}
	all := append([]string{host}, segs...)
	last := len(all) - 1

	var iidIdx int
	var mode string
	var idx int

	switch {
	case all[last] == "diff":
		mode = "list"
		iidIdx = last - 1
	case len(all) >= 2 && all[last-1] == "diff":
		sub := all[last]
		iidIdx = last - 2
		if sub == "all" {
			mode = "all"
		} else {
			n, ok := ParsePositiveDecimalInt(sub)
			if !ok {
				return parsedMrURL{}, fmt.Errorf("Invalid mr:// diff sub-path '%s'. Use 'all' or a 1-indexed file number.", sub)
			}
			mode = "slice"
			idx = n
		}
	default:
		return parsedMrURL{}, fmt.Errorf("%s", invalidMrURLMsg)
	}

	if iidIdx < 0 {
		return parsedMrURL{}, fmt.Errorf("%s", invalidMrURLMsg)
	}
	iid, ok := ParsePositiveDecimalInt(all[iidIdx])
	if !ok {
		return parsedMrURL{}, fmt.Errorf("%s", invalidMrURLMsg)
	}
	if iidIdx == 0 {
		return parsedMrURL{iid: iid, mode: mode, index: idx}, nil
	}

	pathSegs := all[1:iidIdx]
	if !gitlabHostRe.MatchString(host) || len(pathSegs) < 2 {
		return parsedMrURL{}, fmt.Errorf("%s", invalidMrURLMsg)
	}
	for _, s := range pathSegs {
		if !gitlabPathSegmentRe.MatchString(s) {
			return parsedMrURL{}, fmt.Errorf("%s", invalidMrURLMsg)
		}
	}
	return parsedMrURL{
		project: strings.ToLower(host) + "/" + strings.Join(pathSegs, "/"),
		iid:     iid,
		mode:    mode,
		index:   idx,
	}, nil
}

type mrHandler struct{}

func (mrHandler) Scheme() string { return "mr" }
func (mrHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingRemote, Selectors: urls.SelectorsLines, Immutable: true}
}
func (mrHandler) PromptDoc(_ urls.SchemeHost) string {
	if !glabAvailable() {
		return ""
	}
	return strings.TrimSpace(mrDoc)
}

func (mrHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	ctx := resolveGoCtx(c)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("aborted")
	}
	parsed, err := parseMrURL(u)
	if err != nil {
		return nil, err
	}
	cwd := resolveContextCwd(c)
	project := parsed.project
	if project == "" {
		project, err = resolveDefaultGitlabProjectMemoized(ctx, cwd)
		if err != nil {
			return nil, fmt.Errorf("mr:// diff resolution failed: mr://%d/diff could not resolve a default GitLab project from the current session: %w\nUse mr://<host>/<group>/<project>/%d/diff.", parsed.iid, err, parsed.iid)
		}
	}
	lookup, err := getOrFetchMrDiff(ctx, c.SDK, cwd, project, parsed.iid)
	if err != nil {
		return nil, fmt.Errorf("mr:// diff resolution failed: %w", err)
	}
	res, err := renderDiffFamilyResource(u, parsed.mode, parsed.index, lookup, diffFamilyLabels{
		baseURL: fmt.Sprintf("mr://%s/%d", project, parsed.iid),
		heading: fmt.Sprintf("Merge Request Diff: %s!%d", project, parsed.iid),
		noun:    "MR",
	})
	if err != nil {
		return nil, fmt.Errorf("mr:// diff resolution failed: %w", err)
	}
	return res, nil
}
