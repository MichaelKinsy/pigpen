package github

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	prDiffFilesPageSize = 100
	prDiffFilesMax      = 3000
)

// PrDiffFile describes one file section within a unified diff.
// upstream: tools/gh-pr-diff.ts:25-37
type PrDiffFile struct {
	Path        string `json:"path"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	ChangeType  string `json:"changeType"` // modified, added, deleted, renamed, binary
	OldPath     string `json:"oldPath,omitempty"`
	StartOffset int    `json:"startOffset"`
	EndOffset   int    `json:"endOffset"`
}

// PrDiffPayload stores the verbatim unified diff and parsed per-file index.
// upstream: tools/gh-pr-diff.ts:39-43
type PrDiffPayload struct {
	Unified string       `json:"unified"`
	Files   []PrDiffFile `json:"files"`
}

var (
	diffBoundaryRe = regexp.MustCompile(`(?m)^diff --git `)
	tooLarge406Re  = regexp.MustCompile(`\bHTTP 406\b`)
	tooLargeLineRe = regexp.MustCompile(`(?i)exceeded the maximum number of lines`)
	tooLargeCodeRe = regexp.MustCompile(`\btoo_large\b`)

	gitlabPathSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	gitlabHostRe        = regexp.MustCompile(`^[A-Za-z0-9.-]+(?::\d+)?$`)
)

// ParsePrUnifiedDiff splits unified diff text on `^diff --git ` boundaries.
// upstream: tools/gh-pr-diff.ts:58-83
func ParsePrUnifiedDiff(text string) PrDiffPayload {
	if text == "" {
		return PrDiffPayload{Unified: text}
	}
	matches := diffBoundaryRe.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return PrDiffPayload{Unified: text}
	}
	files := make([]PrDiffFile, 0, len(matches))
	for i, loc := range matches {
		start := loc[0]
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		files = append(files, parsePrDiffSection(text[start:end], start, end))
	}
	return PrDiffPayload{Unified: text, Files: files}
}

func parseDiffQuotedEscape(text string, slashIdx int) (string, int) {
	if slashIdx+1 >= len(text) {
		return `\`, slashIdx + 1
	}
	next := text[slashIdx+1]
	if next >= '0' && next <= '7' {
		end := slashIdx + 1
		for end < len(text) && end < slashIdx+4 && text[end] >= '0' && text[end] <= '7' {
			end++
		}
		v, _ := strconv.ParseInt(text[slashIdx+1:end], 8, 32)
		return string(rune(v)), end
	}
	switch next {
	case 'a':
		return "\x07", slashIdx + 2
	case 'b':
		return "\b", slashIdx + 2
	case 'f':
		return "\f", slashIdx + 2
	case 'n':
		return "\n", slashIdx + 2
	case 'r':
		return "\r", slashIdx + 2
	case 't':
		return "\t", slashIdx + 2
	case 'v':
		return "\v", slashIdx + 2
	default:
		return string(next), slashIdx + 2
	}
}

func parseDiffQuotedToken(text string, startIdx int) (string, int, bool) {
	if startIdx >= len(text) || text[startIdx] != '"' {
		return "", 0, false
	}
	var b strings.Builder
	for i := startIdx + 1; i < len(text); i++ {
		ch := text[i]
		if ch == '"' {
			return b.String(), i + 1, true
		}
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		val, nextIdx := parseDiffQuotedEscape(text, i)
		b.WriteString(val)
		i = nextIdx - 1
	}
	return "", 0, false
}

func parseDiffHeaderToken(text string, startIdx int) (string, int, bool) {
	i := startIdx
	for i < len(text) && text[i] == ' ' {
		i++
	}
	if i >= len(text) {
		return "", 0, false
	}
	if val, nextIdx, ok := parseDiffQuotedToken(text, i); ok {
		return val, nextIdx, true
	}
	sp := strings.IndexByte(text[i:], ' ')
	if sp < 0 {
		return text[i:], len(text), true
	}
	return text[i : i+sp], i + sp, true
}

func parsePrDiffHeaderPaths(header string) (oldPath, newPath string) {
	trail := strings.TrimPrefix(header, "diff --git ")
	if strings.HasPrefix(trail, `"`) {
		oldTok, nextIdx, ok := parseDiffQuotedToken(trail, 0)
		if !ok {
			return "", ""
		}
		newTok, _, ok := parseDiffHeaderToken(trail, nextIdx)
		if !ok {
			return "", ""
		}
		return strings.TrimPrefix(oldTok, "a/"), strings.TrimPrefix(newTok, "b/")
	}
	if strings.HasPrefix(trail, "a/") {
		if bIdx := strings.Index(trail, " b/"); bIdx > 0 {
			return trail[2:bIdx], trail[bIdx+3:]
		}
	}
	return "", ""
}

func isPrDiffFileHeaderLine(line string) bool {
	return line == "--- /dev/null" ||
		line == "+++ /dev/null" ||
		strings.HasPrefix(line, "--- a/") ||
		strings.HasPrefix(line, "+++ b/") ||
		strings.HasPrefix(line, `--- "a/`) ||
		strings.HasPrefix(line, `+++ "b/`)
}

func parsePrDiffSection(section string, startOffset, endOffset int) PrDiffFile {
	lines := strings.Split(section, "\n")
	oldPath, newPath := parsePrDiffHeaderPaths(lines[0])
	changeType := "modified"
	isBinary := false
	additions := 0
	deletions := 0
	inHunk := false

	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "new file mode"):
			changeType = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			changeType = "deleted"
		case strings.HasPrefix(line, "rename from "):
			changeType = "renamed"
			oldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			newPath = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
			isBinary = true
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case !inHunk && isPrDiffFileHeaderLine(line):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}

	if isBinary {
		if changeType == "modified" {
			changeType = "binary"
		}
		additions = 0
		deletions = 0
	}

	displayPath := newPath
	if changeType == "deleted" {
		if oldPath != "" {
			displayPath = oldPath
		}
	}
	if displayPath == "" {
		displayPath = oldPath
	}
	if displayPath == "" {
		displayPath = "(unknown)"
	}

	f := PrDiffFile{
		Path:        displayPath,
		Additions:   additions,
		Deletions:   deletions,
		ChangeType:  changeType,
		StartOffset: startOffset,
		EndOffset:   endOffset,
	}
	if oldPath != "" && oldPath != displayPath {
		f.OldPath = oldPath
	}
	return f
}

func isPrDiffTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return tooLarge406Re.MatchString(msg) || tooLargeLineRe.MatchString(msg) || tooLargeCodeRe.MatchString(msg)
}

type ghPrFileAPI struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

func formatSyntheticDiffPath(prefix, p string) string {
	full := prefix + p
	needsQuote := false
	for i := range len(full) {
		c := full[i]
		if c < 32 || c == ' ' || c == '\t' || c == '"' || c == '\\' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return full
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(full) {
		c := full[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 32 {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func buildSyntheticDiffSection(file ghPrFileAPI) string {
	if file.Filename == "" {
		return ""
	}
	status := file.Status
	if status == "" {
		status = "modified"
	}
	oldPath := file.PreviousFilename
	if oldPath == "" {
		oldPath = file.Filename
	}
	oldDiff := formatSyntheticDiffPath("a/", oldPath)
	newDiff := formatSyntheticDiffPath("b/", file.Filename)
	lines := []string{fmt.Sprintf("diff --git %s %s", oldDiff, newDiff)}
	switch {
	case status == "added":
		lines = append(lines, "new file mode 100644")
	case status == "removed":
		lines = append(lines, "deleted file mode 100644")
	case status == "renamed" || file.PreviousFilename != "":
		lines = append(lines, "rename from "+oldPath, "rename to "+file.Filename)
	}
	if file.Patch != "" {
		if status == "added" {
			lines = append(lines, "--- /dev/null")
		} else {
			lines = append(lines, "--- "+oldDiff)
		}
		if status == "removed" {
			lines = append(lines, "+++ /dev/null")
		} else {
			lines = append(lines, "+++ "+newDiff)
		}
		lines = append(lines, file.Patch)
	} else {
		lines = append(lines, fmt.Sprintf("* patch unavailable (binary or too large); additions %d, deletions %d", file.Additions, file.Deletions))
	}
	return strings.Join(lines, "\n")
}

func fetchPrDiffViaFilesAPI(ctx context.Context, cwd, repo string, number int) (string, error) {
	ref := ParseRepoRef(repo)
	var pull struct {
		ChangedFiles int `json:"changed_files"`
	}
	args := append([]string{"api"}, ghAPIHostArgs(ref)...)
	args = append(args, "--method", "GET", fmt.Sprintf("/repos/%s/pulls/%d", ref.Slug, number))
	if err := ghJSON(ctx, cwd, args, &pull, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
		return "", err
	}
	if pull.ChangedFiles > prDiffFilesMax {
		return "", fmt.Errorf("Pull request changes %d files, exceeding GitHub's %d-file limit for the per-file diff API.", pull.ChangedFiles, prDiffFilesMax)
	}

	var sections []string
	for page := 1; ; page++ {
		pArgs := append([]string{"api"}, ghAPIHostArgs(ref)...)
		pArgs = append(pArgs,
			"--method", "GET",
			fmt.Sprintf("/repos/%s/pulls/%d/files", ref.Slug, number),
			"-F", fmt.Sprintf("per_page=%d", prDiffFilesPageSize),
			"-F", fmt.Sprintf("page=%d", page),
		)
		var resp []ghPrFileAPI
		if err := ghJSON(ctx, cwd, pArgs, &resp, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
			return "", err
		}
		for _, f := range resp {
			if sec := buildSyntheticDiffSection(f); sec != "" {
				sections = append(sections, sec)
			}
		}
		if len(resp) < prDiffFilesPageSize {
			break
		}
	}
	if len(sections) == 0 {
		return "", nil
	}
	return strings.Join(sections, "\n") + "\n", nil
}

func fetchPrDiffFresh(ctx context.Context, cwd, repo string, number int) (FreshView[PrDiffPayload], error) {
	numStr := fmt.Sprintf("%d", number)
	args := []string{"pr", "diff", numStr, "--color", "never"}
	appendRepoFlag(&args, repo, numStr)
	text, err := ghText(ctx, cwd, args, ghCommandOptions{repoProvided: true, trimOutput: false})
	if err != nil {
		if !isPrDiffTooLargeError(err) {
			return FreshView[PrDiffPayload]{}, err
		}
		text, err = fetchPrDiffViaFilesAPI(ctx, cwd, repo, number)
		if err != nil {
			return FreshView[PrDiffPayload]{}, err
		}
	}
	parsed := ParsePrUnifiedDiff(text)
	return FreshView[PrDiffPayload]{
		Rendered: text,
		Payload:  PrDiffPayload{Files: parsed.Files},
	}, nil
}

func getOrFetchPrDiff(ctx context.Context, sdkCtx sdk.Context, cwd, repo string, number int) (ViewLookupResult[PrDiffPayload], error) {
	authKey := resolveGithubCacheAuthKey("")
	res, err := getOrFetchView(cacheLookupOptions[PrDiffPayload]{
		sdkCtx:          sdkCtx,
		repo:            repo,
		kind:            CacheKindPrDiff,
		number:          number,
		includeComments: false,
		authKey:         authKey,
		bypassCache:     authKey == "",
		fetchFresh: func() (FreshView[PrDiffPayload], error) {
			return fetchPrDiffFresh(ctx, cwd, repo, number)
		},
	})
	if err != nil {
		return ViewLookupResult[PrDiffPayload]{}, err
	}
	res.Payload.Unified = res.Rendered
	return res, nil
}

// ── GitLab MR diff (glab) ────────────────────────────────────────────────────

func formatGitlabProjectArg(project string) string {
	return "https://" + project
}

func parseGitlabProjectWebURL(webURL string) string {
	u, err := url.Parse(webURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	var segs []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) < 2 {
		return ""
	}
	for _, s := range segs {
		if !gitlabPathSegmentRe.MatchString(s) {
			return ""
		}
	}
	return strings.ToLower(u.Host) + "/" + strings.Join(segs, "/")
}

func resolveGitlabProjectFromCwd(ctx context.Context, cwd string) (string, error) {
	var view struct {
		WebURL        string `json:"web_url"`
		HTTPURLToRepo string `json:"http_url_to_repo"`
	}
	if err := glabJSON(ctx, cwd, []string{"repo", "view", "-F", "json"}, &view); err != nil {
		return "", err
	}
	project := parseGitlabProjectWebURL(view.WebURL)
	if project == "" && view.HTTPURLToRepo != "" {
		project = parseGitlabProjectWebURL(strings.TrimSuffix(view.HTTPURLToRepo, ".git"))
	}
	if project == "" {
		return "", fmt.Errorf("GitLab CLI returned an unrecognized project URL: %s", view.WebURL)
	}
	return project, nil
}

var defaultGitlabProjectMemo = newCwdLookupMemo(resolveGitlabProjectFromCwd)

func resolveDefaultGitlabProjectMemoized(ctx context.Context, cwd string) (string, error) {
	return defaultGitlabProjectMemo.get(ctx, cwd)
}

// assertGitlabHostTrustedForEnvToken mirrors tools/glab-mr.ts:47-80.
func assertGitlabHostTrustedForEnvToken(ctx context.Context, host, cwd string) error {
	hasEnvToken := false
	for _, name := range gitlabTokenEnvVars {
		if os.Getenv(name) != "" {
			hasEnvToken = true
			break
		}
	}
	if !hasEnvToken {
		return nil
	}
	trusted := map[string]bool{"gitlab.com": true}
	for _, name := range []string{"GITLAB_HOST", "GITLAB_URI", "GL_HOST"} {
		val := strings.TrimSpace(os.Getenv(name))
		if val == "" {
			continue
		}
		raw := val
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			trusted[strings.ToLower(u.Host)] = true
		}
	}
	if trusted[host] {
		return nil
	}
	if data, err := os.ReadFile(getGlabConfigPath()); err == nil {
		for _, h := range parseGlabConfigHosts(string(data)) {
			trusted[strings.ToLower(h)] = true
		}
	}
	if trusted[host] {
		return nil
	}
	if ownProject, err := resolveDefaultGitlabProjectMemoized(ctx, cwd); err == nil {
		if ownHost, _, _ := strings.Cut(ownProject, "/"); ownHost == host {
			return nil
		}
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("Refusing to send GitLab token environment variables to %s: it is not gitlab.com, GITLAB_HOST, a host in glab's config, or this checkout's GitLab host. Run `glab auth login --hostname %s` or set GITLAB_HOST to trust it.", host, host)
}

// parseGlabConfigHosts extracts top-level keys under `hosts:` in glab's config.yml
// without pulling in a third-party YAML dependency.
func parseGlabConfigHosts(content string) []string {
	var hosts []string
	inHosts := false
	hostIndent := -1
	for _, raw := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if !inHosts {
			if indent == 0 && (trimmed == "hosts:" || strings.HasPrefix(trimmed, "hosts:")) {
				inHosts = true
			}
			continue
		}
		if indent == 0 {
			break
		}
		if hostIndent < 0 {
			hostIndent = indent
		}
		if indent == hostIndent && strings.HasSuffix(trimmed, ":") {
			key := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			key = strings.Trim(key, `"'`)
			if key != "" {
				hosts = append(hosts, key)
			}
		}
	}
	return hosts
}

func fetchMrDiffFresh(ctx context.Context, cwd, project string, iid int) (FreshView[PrDiffPayload], error) {
	baseArgs := []string{"mr", "diff", fmt.Sprintf("%d", iid), "-R", formatGitlabProjectArg(project)}
	rawArgs := append(append([]string(nil), baseArgs...), "--raw", "--color", "never")
	text, err := glabText(ctx, cwd, rawArgs, false)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown flag: --raw") {
			return FreshView[PrDiffPayload]{}, fmt.Errorf("GitLab CLI is too old for `glab mr diff --raw`; upgrade glab.")
		}
		if !(strings.Contains(msg, "could not obtain raw diff") && strings.Contains(msg, "404")) {
			return FreshView[PrDiffPayload]{}, err
		}
		fallbackArgs := append(append([]string(nil), baseArgs...), "--color", "never")
		text, err = glabText(ctx, cwd, fallbackArgs, false)
		if err != nil {
			return FreshView[PrDiffPayload]{}, err
		}
	}
	parsed := ParsePrUnifiedDiff(text)
	if len(parsed.Files) == 0 && strings.TrimSpace(text) != "" {
		return FreshView[PrDiffPayload]{}, fmt.Errorf("GitLab CLI returned a diff without `diff --git` file headers; upgrade glab.")
	}
	return FreshView[PrDiffPayload]{
		Rendered: text,
		Payload:  PrDiffPayload{Files: parsed.Files},
	}, nil
}

func getOrFetchMrDiff(ctx context.Context, sdkCtx sdk.Context, cwd, project string, iid int) (ViewLookupResult[PrDiffPayload], error) {
	host, _, _ := strings.Cut(project, "/")
	if err := assertGitlabHostTrustedForEnvToken(ctx, host, cwd); err != nil {
		return ViewLookupResult[PrDiffPayload]{}, err
	}
	authKey := resolveGitlabCacheAuthKey(host)
	res, err := getOrFetchView(cacheLookupOptions[PrDiffPayload]{
		sdkCtx:          sdkCtx,
		repo:            project,
		kind:            CacheKindMrDiff,
		number:          iid,
		includeComments: false,
		authKey:         authKey,
		bypassCache:     authKey == "",
		fetchFresh: func() (FreshView[PrDiffPayload], error) {
			return fetchMrDiffFresh(ctx, cwd, project, iid)
		},
	})
	if err != nil {
		return ViewLookupResult[PrDiffPayload]{}, err
	}
	res.Payload.Unified = res.Rendered
	return res, nil
}
