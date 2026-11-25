package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	ghIssueStateReasonField = "stateReason"
	reviewCommentsPageSize  = 100
	filePreviewLimit        = 50
	// binarySniffBytes matches packages/utils/src/binary.ts:22 (BINARY_SNIFF_BYTES).
	binarySniffBytes = 8192
)

var (
	ghRepoFields = []string{
		"nameWithOwner", "description", "url", "defaultBranchRef", "homepageUrl",
		"forkCount", "isArchived", "isFork", "primaryLanguage", "repositoryTopics",
		"stargazerCount", "updatedAt", "viewerPermission", "visibility",
	}
	ghIssueFields = []string{
		"author", "body", "comments", "createdAt", "labels", "number",
		"state", "stateReason", "title", "updatedAt", "url",
	}
	ghIssueFieldsNoComments = []string{
		"author", "body", "createdAt", "labels", "number",
		"state", "stateReason", "title", "updatedAt", "url",
	}
	ghPrFields = []string{
		"author", "baseRefName", "body", "comments", "createdAt", "files",
		"headRefName", "isDraft", "labels", "mergeStateStatus", "number",
		"reviews", "reviewDecision", "state", "title", "updatedAt", "url",
	}
	ghPrFieldsNoComments = []string{
		"author", "baseRefName", "body", "createdAt", "files",
		"headRefName", "isDraft", "labels", "mergeStateStatus", "number",
		"reviews", "reviewDecision", "state", "title", "updatedAt", "url",
	}
)

func ghJSONErrorNamesField(err error, field string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, "Unknown JSON field") {
		return false
	}
	return strings.Contains(msg, `"`+field+`"`) || strings.Contains(msg, `'`+field+`'`) || strings.Contains(msg, field)
}

func dropJSONField(args []string, field string) ([]string, bool) {
	next := append([]string(nil), args...)
	for i := range next {
		if next[i] == "--json" && i+1 < len(next) {
			parts := strings.Split(next[i+1], ",")
			var kept []string
			for _, p := range parts {
				if p != field {
					kept = append(kept, p)
				}
			}
			if len(kept) == len(parts) {
				return nil, false
			}
			next[i+1] = strings.Join(kept, ",")
			return next, true
		}
	}
	return nil, false
}

// githubIssueJSONWithStateReasonFallback retries without stateReason on older gh releases.
// upstream: tools/gh-view.ts:100-114
func githubIssueJSONWithStateReasonFallback(ctx context.Context, cwd string, args []string, dst any, opts ghCommandOptions) error {
	err := ghJSON(ctx, cwd, args, dst, opts)
	if err == nil {
		return nil
	}
	if !ghJSONErrorNamesField(err, ghIssueStateReasonField) {
		return err
	}
	retryArgs, ok := dropJSONField(args, ghIssueStateReasonField)
	if !ok {
		return err
	}
	return ghJSON(ctx, cwd, retryArgs, dst, opts)
}

func fetchPrReviewComments(ctx context.Context, cwd, repo string, prNumber int) ([]ghPrReviewComment, error) {
	ref := ParseRepoRef(repo)
	var out []ghPrReviewComment
	for page := 1; ; page++ {
		args := []string{"api"}
		args = append(args, ghAPIHostArgs(ref)...)
		args = append(args,
			"--method", "GET",
			fmt.Sprintf("/repos/%s/pulls/%d/comments", ref.Slug, prNumber),
			"-F", fmt.Sprintf("per_page=%d", reviewCommentsPageSize),
			"-F", fmt.Sprintf("page=%d", page),
		)
		var resp []ghPrReviewCommentAPI
		if err := ghJSON(ctx, cwd, args, &resp, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
			return nil, err
		}
		for _, c := range resp {
			if c.ID == nil {
				continue
			}
			out = append(out, ghPrReviewComment{
				Author:       c.User,
				Body:         c.Body,
				CreatedAt:    strings.TrimSpace(c.CreatedAt),
				ID:           *c.ID,
				InReplyToID:  c.InReplyToID,
				Line:         c.Line,
				OriginalLine: c.OriginalLine,
				Path:         strings.TrimSpace(c.Path),
				Side:         strings.TrimSpace(c.Side),
				URL:          strings.TrimSpace(c.HTMLURL),
			})
		}
		if len(resp) < reviewCommentsPageSize {
			break
		}
	}
	return out, nil
}

func formatCommentsSection(comments []ghComment) []string {
	if len(comments) == 0 {
		return nil
	}
	var visible []ghComment
	for _, c := range comments {
		if !c.IsMinimized {
			visible = append(visible, c)
		}
	}
	hiddenCount := len(comments) - len(visible)
	lines := []string{"## Comments", ""}
	if len(visible) == 0 {
		lines = append(lines, fmt.Sprintf("No visible comments. Minimized comments omitted: %d.", hiddenCount))
		return lines
	}
	lines[0] = fmt.Sprintf("## Comments (%d)", len(visible))
	for _, c := range visible {
		author := formatAuthor(c.Author)
		if author == "" {
			author = "unknown"
		}
		created := ""
		if c.CreatedAt != "" {
			created = " · " + c.CreatedAt
		}
		lines = append(lines, fmt.Sprintf("### %s%s", author, created), "")
		body := normalizeText(c.Body)
		if body == "" {
			body = "No comment body."
		}
		lines = append(lines, body)
		if c.URL != "" {
			lines = append(lines, "", "URL: "+c.URL)
		}
		lines = append(lines, "")
	}
	if hiddenCount > 0 {
		lines = append(lines, fmt.Sprintf("Minimized comments omitted: %d.", hiddenCount))
	}
	return lines
}

func formatReviewsSection(reviews []ghPrReview) []string {
	if len(reviews) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("## Reviews (%d)", len(reviews)), ""}
	for _, r := range reviews {
		author := formatAuthor(r.Author)
		if author == "" {
			author = "unknown"
		}
		submitted := ""
		if r.SubmittedAt != "" {
			submitted = " - " + r.SubmittedAt
		}
		state := ""
		if r.State != "" {
			state = fmt.Sprintf(" [%s]", r.State)
		}
		lines = append(lines, fmt.Sprintf("### %s%s%s", author, submitted, state))
		if r.Commit != nil && r.Commit.OID != "" {
			lines = append(lines, "", "Commit: "+formatShortSHA(r.Commit.OID))
		}
		body := normalizeText(r.Body)
		if body == "" {
			body = "No review body."
		}
		lines = append(lines, "", body, "")
	}
	return lines
}

func formatReviewCommentsSection(comments []ghPrReviewComment) []string {
	if len(comments) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("## Review Comments (%d)", len(comments)), ""}
	for _, c := range comments {
		author := formatAuthor(c.Author)
		if author == "" {
			author = "unknown"
		}
		created := ""
		if c.CreatedAt != "" {
			created = " · " + c.CreatedAt
		}
		lines = append(lines, fmt.Sprintf("### %s%s", author, created), "")
		if c.Path != "" {
			loc := c.Path
			if c.Line != nil {
				loc = fmt.Sprintf("%s:%d", c.Path, *c.Line)
			} else if c.OriginalLine != nil {
				loc = fmt.Sprintf("%s:%d", c.Path, *c.OriginalLine)
			}
			pushLine(&lines, "Location", loc)
		}
		pushLine(&lines, "Side", c.Side)
		pushLineInt(&lines, "Reply to", c.InReplyToID)
		pushLine(&lines, "URL", c.URL)
		body := normalizeText(c.Body)
		if body == "" {
			body = "No review comment body."
		}
		lines = append(lines, "", body, "")
	}
	return lines
}

func formatRepoView(data ghRepoViewData, repo, branch string) string {
	name := data.NameWithOwner
	if name == "" {
		name = repo
	}
	if name == "" {
		name = "GitHub Repository"
	}
	desc := normalizeText(data.Description)
	if desc == "" {
		desc = "No description provided."
	}
	lines := []string{"# " + name, "", desc, ""}
	pushLine(&lines, "URL", data.URL)
	if data.DefaultBranchRef != nil {
		pushLine(&lines, "Default branch", data.DefaultBranchRef.Name)
	}
	pushLine(&lines, "Branch", strings.TrimSpace(branch))
	pushLine(&lines, "Visibility", data.Visibility)
	pushLine(&lines, "Viewer permission", data.ViewerPermission)
	if data.PrimaryLanguage != nil {
		pushLine(&lines, "Primary language", data.PrimaryLanguage.Name)
	}
	pushLineInt(&lines, "Stars", data.StargazerCount)
	pushLineInt(&lines, "Forks", data.ForkCount)
	pushLineBool(&lines, "Archived", data.IsArchived)
	pushLineBool(&lines, "Fork", data.IsFork)
	pushLine(&lines, "Updated", data.UpdatedAt)
	pushLine(&lines, "Homepage", data.HomepageURL)
	var topics []string
	for _, t := range data.RepositoryTopics {
		if t.Name != "" {
			topics = append(topics, t.Name)
		} else if t.Topic != nil && t.Topic.Name != "" {
			topics = append(topics, t.Topic.Name)
		}
	}
	pushLine(&lines, "Topics", strings.Join(topics, ", "))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatIssueView(data ghIssueViewData, issue string, includeComments bool) string {
	numStr := issue
	if data.Number != nil {
		numStr = fmt.Sprintf("%d", *data.Number)
	}
	title := data.Title
	if title == "" {
		title = "Untitled"
	}
	lines := []string{fmt.Sprintf("# Issue #%s: %s", numStr, title), ""}
	pushLine(&lines, "State", data.State)
	pushLine(&lines, "State reason", data.StateReason)
	pushLine(&lines, "Author", formatAuthor(data.Author))
	pushLine(&lines, "Created", data.CreatedAt)
	pushLine(&lines, "Updated", data.UpdatedAt)
	pushLine(&lines, "Labels", formatLabels(data.Labels))
	pushLine(&lines, "URL", data.URL)
	body := normalizeText(data.Body)
	if body == "" {
		body = "No description provided."
	}
	lines = append(lines, "", "## Body", "", body)
	if includeComments && len(data.Comments) > 0 {
		if sec := formatCommentsSection(data.Comments); len(sec) > 0 {
			lines = append(lines, "")
			lines = append(lines, sec...)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatPrFiles(files []ghPrFile) []string {
	if len(files) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("## Files (%d)", len(files)), ""}
	limit := min(len(files), filePreviewLimit)
	for _, f := range files[:limit] {
		ct := f.ChangeType
		if ct == "" {
			ct = "CHANGED"
		}
		p := f.Path
		if p == "" {
			p = "(unknown file)"
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] (+%d -%d)", p, ct, f.Additions, f.Deletions))
	}
	if len(files) > filePreviewLimit {
		lines = append(lines, fmt.Sprintf("[…%d files elided…]", len(files)-filePreviewLimit))
	}
	return lines
}

func formatPrView(data ghPrViewData, pr string, includeComments bool) string {
	prID := pr
	if data.Number != nil {
		prID = fmt.Sprintf("%d", *data.Number)
	}
	if prID == "" {
		prID = "current"
	}
	title := data.Title
	if title == "" {
		title = "Untitled"
	}
	lines := []string{fmt.Sprintf("# Pull Request #%s: %s", prID, title), ""}
	pushLine(&lines, "State", data.State)
	pushLineBool(&lines, "Draft", data.IsDraft)
	pushLine(&lines, "Author", formatAuthor(data.Author))
	pushLine(&lines, "Base", data.BaseRefName)
	pushLine(&lines, "Head", data.HeadRefName)
	pushLine(&lines, "Review decision", data.ReviewDecision)
	pushLine(&lines, "Merge state", data.MergeStateStatus)
	pushLine(&lines, "Created", data.CreatedAt)
	pushLine(&lines, "Updated", data.UpdatedAt)
	pushLine(&lines, "Labels", formatLabels(data.Labels))
	pushLine(&lines, "URL", data.URL)
	body := normalizeText(data.Body)
	if body == "" {
		body = "No description provided."
	}
	lines = append(lines, "", "## Body", "", body)

	if sec := formatPrFiles(data.Files); len(sec) > 0 {
		lines = append(lines, "")
		lines = append(lines, sec...)
	}
	if includeComments && len(data.Reviews) > 0 {
		if sec := formatReviewsSection(data.Reviews); len(sec) > 0 {
			lines = append(lines, "")
			lines = append(lines, sec...)
		}
	}
	if includeComments && len(data.ReviewComments) > 0 {
		if sec := formatReviewCommentsSection(data.ReviewComments); len(sec) > 0 {
			lines = append(lines, "")
			lines = append(lines, sec...)
		}
	}
	if includeComments && len(data.Comments) > 0 {
		if sec := formatCommentsSection(data.Comments); len(sec) > 0 {
			lines = append(lines, "")
			lines = append(lines, sec...)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func fetchIssueViewFresh(ctx context.Context, cwd, repo, identifier string, includeComments bool) (FreshView[ghIssueViewData], error) {
	args := []string{"issue", "view", identifier}
	appendRepoFlag(&args, repo, identifier)
	fields := ghIssueFields
	if !includeComments {
		fields = ghIssueFieldsNoComments
	}
	args = append(args, "--json", strings.Join(fields, ","))
	var data ghIssueViewData
	if err := githubIssueJSONWithStateReasonFallback(ctx, cwd, args, &data, ghCommandOptions{
		repoProvided: repo != "",
		trimOutput:   true,
	}); err != nil {
		return FreshView[ghIssueViewData]{}, err
	}
	return FreshView[ghIssueViewData]{
		Rendered:  formatIssueView(data, identifier, includeComments),
		SourceURL: data.URL,
		Payload:   data,
	}, nil
}

func fetchPrViewFresh(ctx context.Context, cwd, repo string, number int, includeComments bool) (FreshView[ghPrViewData], error) {
	numStr := fmt.Sprintf("%d", number)
	args := []string{"pr", "view", numStr}
	appendRepoFlag(&args, repo, numStr)
	fields := ghPrFields
	if !includeComments {
		fields = ghPrFieldsNoComments
	}
	args = append(args, "--json", strings.Join(fields, ","))
	var data ghPrViewData
	if err := ghJSON(ctx, cwd, args, &data, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
		return FreshView[ghPrViewData]{}, err
	}
	if includeComments && data.Number != nil {
		comments, err := fetchPrReviewComments(ctx, cwd, repo, *data.Number)
		if err != nil {
			return FreshView[ghPrViewData]{}, err
		}
		data.ReviewComments = comments
	}
	return FreshView[ghPrViewData]{
		Rendered:  formatPrView(data, numStr, includeComments),
		SourceURL: data.URL,
		Payload:   data,
	}, nil
}

func getOrFetchIssue(ctx context.Context, sdkCtx sdk.Context, cwd, repo, issue string, includeComments bool) (ViewLookupResult[ghIssueViewData], error) {
	identifier := strings.TrimSpace(issue)
	if identifier == "" {
		return ViewLookupResult[ghIssueViewData]{}, fmt.Errorf("issue must not be empty")
	}
	if strings.HasPrefix(identifier, "-") {
		return ViewLookupResult[ghIssueViewData]{}, fmt.Errorf("invalid issue identifier: %s. Pass an issue number or URL.", identifier)
	}
	urlRepo, urlNum, isURL := parseIssueURL(identifier)
	if isURL {
		repo = urlRepo
	}
	cacheNum := urlNum
	hasNum := isURL
	if !hasNum {
		if n, ok := ParsePositiveDecimalInt(identifier); ok {
			cacheNum = n
			hasNum = true
		}
	}
	if hasNum && repo == "" {
		if r, err := resolveDefaultRepoMemoized(ctx, cwd); err == nil {
			repo = r
		}
	}
	doFetch := func() (FreshView[ghIssueViewData], error) {
		return fetchIssueViewFresh(ctx, cwd, repo, identifier, includeComments)
	}
	if repo == "" || !hasNum {
		fresh, err := doFetch()
		if err != nil {
			return ViewLookupResult[ghIssueViewData]{}, err
		}
		return ViewLookupResult[ghIssueViewData]{
			Rendered:  fresh.Rendered,
			SourceURL: fresh.SourceURL,
			Payload:   fresh.Payload,
			Status:    CacheStatusMiss,
		}, nil
	}
	authKey := resolveGithubCacheAuthKey("")
	return getOrFetchView(cacheLookupOptions[ghIssueViewData]{
		sdkCtx:          sdkCtx,
		repo:            repo,
		kind:            CacheKindIssue,
		number:          cacheNum,
		includeComments: includeComments,
		authKey:         authKey,
		bypassCache:     authKey == "",
		fetchFresh:      doFetch,
	})
}

func getOrFetchPr(ctx context.Context, sdkCtx sdk.Context, cwd, repo string, number int, includeComments bool) (ViewLookupResult[ghPrViewData], error) {
	authKey := resolveGithubCacheAuthKey("")
	return getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx:          sdkCtx,
		repo:            repo,
		kind:            CacheKindPr,
		number:          number,
		includeComments: includeComments,
		authKey:         authKey,
		bypassCache:     authKey == "",
		fetchFresh: func() (FreshView[ghPrViewData], error) {
			return fetchPrViewFresh(ctx, cwd, repo, number, includeComments)
		},
	})
}

func executeRepoView(ctx context.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	repo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), repo); err != nil {
		return sdk.ToolResult{}, err
	}
	branch := optString(params, "branch")
	args := []string{"repo", "view"}
	if repo != "" {
		args = append(args, repo)
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--json", strings.Join(ghRepoFields, ","))

	var data ghRepoViewData
	if err := ghJSON(ctx, cwd, args, &data, ghCommandOptions{repoProvided: repo != "", trimOutput: true}); err != nil {
		return sdk.ToolResult{}, err
	}
	var details any
	if meta := metaWithSourceURL(data.URL); meta != nil {
		details = GhToolDetails{Meta: meta}
	}
	return sdk.ToolResult{
		Content: formatRepoView(data, repo, branch),
		Details: details,
	}, nil
}

type githubContentsFile struct {
	Type     string  `json:"type"`
	Encoding string  `json:"encoding"`
	Size     *int    `json:"size"`
	Content  *string `json:"content"`
	HTMLURL  string  `json:"html_url"`
}

func decodeBase64Flexible(encoded string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
}

func executeFileRead(ctx context.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	rawRepo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), rawRepo); err != nil {
		return sdk.ToolResult{}, err
	}
	repo, err := resolveGitHubRepo(ctx, cwd, rawRepo, "")
	if err != nil {
		return sdk.ToolResult{}, err
	}
	filePath := optString(params, "path")
	if filePath == "" {
		return sdk.ToolResult{}, fmt.Errorf("path must not be empty")
	}
	if strings.HasPrefix(filePath, "/") {
		return sdk.ToolResult{}, fmt.Errorf("path must be repository-relative")
	}
	branch := optString(params, "branch")

	segments := strings.Split(filePath, "/")
	for i, seg := range segments {
		segments[i] = encodeURIComponent(seg)
	}
	endpointPath := strings.Join(segments, "/")
	ref := ParseRepoRef(repo)

	args := []string{"api"}
	args = append(args, ghAPIHostArgs(ref)...)
	args = append(args,
		fmt.Sprintf("/repos/%s/contents/%s", ref.Slug, endpointPath),
		"--method", "GET",
		"-H", "Accept: application/vnd.github+json",
		"-H", "Accept-Encoding: identity",
	)
	if branch != "" {
		args = append(args, "-f", "ref="+branch)
	}

	var rawResp json.RawMessage
	if err := ghJSON(ctx, cwd, args, &rawResp, ghCommandOptions{repoProvided: true, trimOutput: false}); err != nil {
		rev := branch
		if rev == "" {
			rev = "HEAD"
		}
		return sdk.ToolResult{}, fmt.Errorf("GitHub file read failed for '%s@%s:%s': %w", repo, rev, filePath, err)
	}

	// Contents API returns a JSON array for a directory.
	// upstream: tools/gh.ts:118-122, 240-242
	trimmed := bytes.TrimSpace(rawResp)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return sdk.ToolResult{}, fmt.Errorf("GitHub path '%s' is not a file.", filePath)
	}
	var resp githubContentsFile
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return sdk.ToolResult{}, fmt.Errorf("GitHub CLI returned invalid JSON output.")
	}
	if resp.Type != "file" {
		return sdk.ToolResult{}, fmt.Errorf("GitHub path '%s' is not a file.", filePath)
	}

	fallbackHost := ref.Host
	if fallbackHost == "" {
		fallbackHost = defaultGhHost()
	}
	rev := branch
	if rev == "" {
		rev = "HEAD"
	}
	sourceURL := resp.HTMLURL
	if sourceURL == "" {
		sourceURL = fmt.Sprintf("https://%s/%s/blob/%s/%s", fallbackHost, ref.Slug, encodeURIComponent(rev), endpointPath)
	}
	details := GhToolDetails{
		Meta:   metaWithSourceURL(sourceURL),
		Repo:   repo,
		Branch: branch,
	}

	if resp.Encoding != "base64" || resp.Content == nil {
		sizeStr := "unknown size"
		if resp.Size != nil && *resp.Size >= 0 {
			sizeStr = formatBytes(*resp.Size)
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("[GitHub did not return file bytes for '%s' (%s). Open %s to view it.]", filePath, sizeStr, sourceURL),
			Details: details,
		}, nil
	}

	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, *resp.Content)
	decoded, err := decodeBase64Flexible(cleaned)
	if err != nil {
		size := 0
		if resp.Size != nil && *resp.Size >= 0 {
			size = *resp.Size
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("[Cannot read binary file '%s' (%s); not valid UTF-8 text. Open %s to view it.]", filePath, formatBytes(size), sourceURL),
			Details: details,
		}, nil
	}

	if imgMeta, ok := parseImageMetadata(decoded); ok {
		dims := ""
		if imgMeta.width > 0 && imgMeta.height > 0 {
			dims = fmt.Sprintf("\nDimensions: %dx%d", imgMeta.width, imgMeta.height)
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("Image file: %s\nMIME: %s\nSize: %s%s", filePath, imgMeta.mimeType, formatBytes(len(decoded)), dims),
			Images:  []sdk.ImageContent{{Data: base64.StdEncoding.EncodeToString(decoded), MimeType: imgMeta.mimeType}},
			Details: details,
		}, nil
	}

	if isProbablyBinary(decoded) || !utf8.Valid(decoded) {
		return sdk.ToolResult{
			Content: fmt.Sprintf("[Cannot read binary file '%s' (%s); not valid UTF-8 text. Open %s to view it.]", filePath, formatBytes(len(decoded)), sourceURL),
			Details: details,
		}, nil
	}

	return sdk.ToolResult{
		Content: string(decoded),
		Details: details,
	}, nil
}

type imageMetadata struct {
	mimeType string
	width    int
	height   int
}

func parseImageMetadata(b []byte) (imageMetadata, bool) {
	// PNG: 8-byte signature + IHDR chunk (width @ 16, height @ 20).
	if len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' &&
		b[4] == 0x0D && b[5] == 0x0A && b[6] == 0x1A && b[7] == 0x0A {
		m := imageMetadata{mimeType: "image/png"}
		if len(b) >= 24 && string(b[12:16]) == "IHDR" {
			m.width = int(binary.BigEndian.Uint32(b[16:20]))
			m.height = int(binary.BigEndian.Uint32(b[20:24]))
		}
		return m, true
	}
	// GIF: "GIF87a" / "GIF89a" + little-endian uint16 width/height @ 6..10.
	if len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a") {
		m := imageMetadata{mimeType: "image/gif"}
		if len(b) >= 10 {
			m.width = int(binary.LittleEndian.Uint16(b[6:8]))
			m.height = int(binary.LittleEndian.Uint16(b[8:10]))
		}
		return m, true
	}
	// JPEG: SOI 0xFF 0xD8 + scan SOF markers for dimensions.
	if len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF {
		m := imageMetadata{mimeType: "image/jpeg"}
		for i := 2; i+1 < len(b); {
			if b[i] != 0xFF {
				break
			}
			for i < len(b) && b[i] == 0xFF {
				i++
			}
			if i >= len(b) {
				break
			}
			marker := b[i]
			i++
			if marker == 0xD8 || marker == 0xD9 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
				continue
			}
			if i+2 > len(b) {
				break
			}
			segLen := int(binary.BigEndian.Uint16(b[i : i+2]))
			if segLen < 2 || i+segLen > len(b) {
				break
			}
			if (marker >= 0xC0 && marker <= 0xC3) || (marker >= 0xC5 && marker <= 0xC7) ||
				(marker >= 0xC9 && marker <= 0xCB) || (marker >= 0xCD && marker <= 0xCF) {
				if segLen >= 7 {
					m.height = int(binary.BigEndian.Uint16(b[i+3 : i+5]))
					m.width = int(binary.BigEndian.Uint16(b[i+5 : i+7]))
				}
				break
			}
			i += segLen
		}
		return m, true
	}
	// WebP: RIFF....WEBP
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		m := imageMetadata{mimeType: "image/webp"}
		if len(b) >= 30 && string(b[12:16]) == "VP8X" {
			m.width = 1 + int(b[24]) | int(b[25])<<8 | int(b[26])<<16
			m.height = 1 + int(b[27]) | int(b[28])<<8 | int(b[29])<<16
		}
		return m, true
	}
	return imageMetadata{}, false
}

func isProbablyBinary(b []byte) bool {
	n := min(len(b), binarySniffBytes)
	for i := range n {
		if b[i] == 0 {
			return true
		}
	}
	return false
}
