package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Port of github-issue-pr.ts: an issue or pull request URL of github.com becomes one Markdown
// document built from `gh issue view` / `gh pr view` and the review comments, or, when gh is not
// usable, from the REST API of api.github.com. Section order, caps and wording follow the
// original; the renderer works on decoded JSON objects like the original's.

const (
	ghTimeout          = 10 * time.Second
	ghTotalTimeout     = 20 * time.Second
	maxDocChars        = 150_000
	bodyInlineChars    = 4_000
	commentInlineChars = 700
	maxInlineComments  = 15
	maxInlineReviews   = 10
	maxInlineChecks    = 15
	maxInlineFiles     = 50
	maxInlineCommits   = 20
	maxInlineThreads   = 30
	ghMaxBuffer        = 10 * 1024 * 1024
)

var (
	prFields     = []string{"title", "number", "state", "isDraft", "author", "baseRefName", "headRefName", "headRepositoryOwner", "createdAt", "mergedAt", "closedAt", "labels", "milestone", "additions", "deletions", "changedFiles", "files", "commits", "reviews", "statusCheckRollup", "comments", "body", "closingIssuesReferences", "url"}
	prCoreFields = []string{"title", "number", "state", "isDraft", "author", "baseRefName", "headRefName", "headRepositoryOwner", "createdAt", "mergedAt", "closedAt", "labels", "milestone", "additions", "deletions", "changedFiles", "files", "commits", "reviews", "comments", "body", "url"}
	issueFields  = []string{"title", "number", "state", "stateReason", "author", "createdAt", "closedAt", "labels", "assignees", "milestone", "comments", "body", "closedByPullRequestsReferences", "url"}
	issueCore    = []string{"title", "number", "state", "author", "createdAt", "closedAt", "labels", "assignees", "milestone", "comments", "body", "url"}

	prSubpaths        = []string{"files", "commits", "checks", "conversation"}
	pullAnchorRE      = regexp.MustCompile(`(?i)^(?:issuecomment-\d+|discussion_r\d+)$`)
	issueAnchorRE     = regexp.MustCompile(`(?i)^issuecomment-\d+$`)
	issueCommentIDRE  = regexp.MustCompile(`(?i)^issuecomment-(\d+)$`)
	discussionIDRE    = regexp.MustCompile(`(?i)^discussion_r(\d+)$`)
	unknownJSONFieldE = regexp.MustCompile(`(?i)unknown (?:json )?field|UnknownField|Unknown JSON field`)
	passingCheckRE    = regexp.MustCompile(`(?i)(success|neutral|skipped|completed)$`)
)

// githubIssuePRURL is GitHubIssuePrUrlInfo; Kind is "pull" or "issue".
type githubIssuePRURL struct {
	Owner, Repo, Kind string
	Number            int
	Anchor            string
}

// parseGitHubIssuePRURL is parseGitHubIssuePrUrl (github-issue-pr.ts:107).
func parseGitHubIssuePRURL(raw string) *githubIssuePRURL {
	segments := githubSegments(raw)
	if len(segments) < 4 {
		return nil
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	if !validGitHubName(owner, repo) {
		return nil
	}
	route := strings.ToLower(segments[2])
	if route != "pull" && route != "issues" || !digitsRE.MatchString(segments[3]) {
		return nil
	}
	sub := ""
	if len(segments) > 4 {
		sub = strings.ToLower(segments[4])
	}
	if route == "pull" && sub != "" && !sliceHas(prSubpaths, sub) {
		return nil
	}
	if route == "issues" && sub != "" {
		return nil
	}
	number, err := strconv.ParseInt(segments[3], 10, 64)
	if err != nil || number <= 0 || number > 1<<53-1 {
		return nil
	}
	info := &githubIssuePRURL{Owner: owner, Repo: repo, Kind: "issue", Number: int(number)}
	pattern := issueAnchorRE
	if route == "pull" {
		info.Kind, pattern = "pull", pullAnchorRE
	}
	if u, err := ParseURL(raw, nil); err == nil && pattern.MatchString(u.EscapedFragment()) {
		info.Anchor = u.EscapedFragment()
	}
	return info
}

// githubPRConfig is GitHubPrIssueConfig.
type githubPRConfig struct{ Enabled bool }

// issuePRConfig is loadConfig of github-issue-pr.ts: memoized for the process once it parsed.
func (s *githubState) issuePRConfig() (githubPRConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prConfig != nil {
		return *s.prConfig, nil
	}
	cfg := githubPRConfig{Enabled: true}
	path := ConfigPath()
	if _, err := os.Stat(path); err != nil {
		s.prConfig = &cfg
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return cfg, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	root, _ := parsed.(map[string]any)
	if value, present := root["githubPrIssue"]; present {
		obj, ok := value.(map[string]any)
		if !ok {
			return cfg, fmt.Errorf("githubPrIssue in %s must be an object", path)
		}
		if enabled, ok := obj["enabled"].(bool); ok {
			cfg.Enabled = enabled
		}
	}
	s.prConfig = &cfg
	return cfg, nil
}

// ---- JSON accessors (stringValue, numberValue, objectArray of the original) --------------------

func prString(v any) string {
	s, _ := v.(string)
	return s
}

func prNumber(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func prNum(f float64) string { return formatNumber(f) }

func prObjects(v any) []map[string]any {
	var out []map[string]any
	switch list := v.(type) {
	case []any:
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = list
	}
	return out
}

func prTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

// prToString is String(value) for decoded JSON.
func prToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return prNum(x)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = prToString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func authorLogin(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return "unknown"
	}
	if login := prString(m["login"]); login != "" {
		return login
	}
	return "unknown"
}

func listNames(v any) string {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return "none"
	}
	var names []string
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"name", "login", "title"} {
			if s := prString(m[key]); s != "" {
				names = append(names, s)
				break
			}
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func milestoneTitle(v any) string {
	if m, ok := v.(map[string]any); ok {
		if t := prString(m["title"]); t != "" {
			return t
		}
	}
	return "none"
}

func truncateText(text string, limit int, label string, appendix *[]string) string {
	if jsLen(text) <= limit {
		return text
	}
	*appendix = append(*appendix, "## Full "+label+"\n\n"+text)
	return jsSlice(text, 0, limit) + "\n\n[" + label + " truncated; use get_search_content findText or offset for the full text]"
}

func appendLimitedSection(lines []string, title string, items []string, max int, marker string) []string {
	lines = append(lines, "## "+title)
	if len(items) == 0 {
		return append(lines, "none", "")
	}
	if len(items) > max {
		lines = append(lines, items[:max]...)
		lines = append(lines, fmt.Sprintf("[%d of %d %s shown; use get_search_content offset or the gh command below for more]", max, len(items), marker))
	} else {
		lines = append(lines, items...)
	}
	return append(lines, "")
}

func commentID(c map[string]any) string {
	v := firstNonNil(c["id"], c["databaseId"])
	if v == nil {
		return ""
	}
	return prToString(v)
}

func anchorMatches(anchor string, c map[string]any) bool {
	if anchor == "" {
		return false
	}
	id := commentID(c)
	return anchor == "issuecomment-"+id || anchor == "discussion_r"+id
}

// anchoredIndex is the first comment the anchor names, or -1.
func anchoredIndex(comments []map[string]any, anchor string) int {
	if anchor == "" {
		return -1
	}
	for i, c := range comments {
		if anchorMatches(anchor, c) {
			return i
		}
	}
	return -1
}

// selectInline is the first n comments plus the anchored one when it lies beyond them.
func selectInline(total, n, forced int) []int {
	var selected []int
	for i := 0; i < total && i < n; i++ {
		selected = append(selected, i)
	}
	if forced >= 0 && forced >= len(selected) {
		selected = append(selected, forced)
	}
	return selected
}

func indent(text string) string { return strings.ReplaceAll(text, "\n", "\n  ") }

func renderComments(comments []map[string]any, anchor string, appendix *[]string) []string {
	forced := anchoredIndex(comments, anchor)
	selected := selectInline(len(comments), maxInlineComments, forced)
	lines := make([]string, 0, len(selected))
	for pos, idx := range selected {
		c := comments[idx]
		label := commentID(c)
		if label == "" {
			label = strconv.Itoa(pos + 1)
		}
		created := prString(c["createdAt"])
		if created == "" {
			created = prString(c["created_at"])
		}
		mark := ""
		if idx == forced {
			mark = " [anchored]"
		}
		text := truncateText(prString(c["body"]), commentInlineChars, "comment "+label, appendix)
		lines = append(lines, "- "+authorLogin(firstNonNil(c["author"], c["user"]))+" at "+created+mark+":\n  "+indent(text))
	}
	return lines
}

func renderCommentMarker(comments []map[string]any, rendered int, total any) (string, bool) {
	if known, ok := prNumber(total); ok && known > float64(len(comments)) {
		return fmt.Sprintf("[%d comments shown from at least %s; use get_search_content findText or offset, or gh issue view -c]", rendered, prNum(known)), true
	}
	if len(comments) > rendered {
		return fmt.Sprintf("[%d of %d comments shown; use get_search_content findText or offset, or gh issue view -c]", rendered, len(comments)), true
	}
	return "", false
}

// orZero is String(a || b || 0) for the counts.
func countText(a int, b any) string {
	if a != 0 {
		return strconv.Itoa(a)
	}
	if f, ok := prNumber(b); ok && f != 0 {
		return prNum(f)
	}
	return "0"
}

func commentCountText(view map[string]any) string {
	comments := len(prObjects(view["comments"]))
	if total, ok := prNumber(view["commentsCount"]); ok && total > float64(comments) {
		return "at least " + prNum(total)
	}
	return countText(comments, view["commentsCount"])
}

func reviewThreadCountText(comments []map[string]any, total any, bounded, unavailable bool) string {
	if unavailable {
		return "unavailable"
	}
	if bounded {
		return "at least " + strconv.Itoa(len(comments))
	}
	if known, ok := prNumber(total); ok && known > float64(len(comments)) {
		return "at least " + prNum(known)
	}
	return countText(len(comments), total)
}

func renderChecks(v any) []string {
	checks := prObjects(v)
	if len(checks) == 0 {
		return []string{"No check rollup data available."}
	}
	rows := make([]string, len(checks))
	failing := 0
	for i, c := range checks {
		name := "check"
		for _, key := range []string{"name", "context", "workflowName"} {
			if s := prString(c[key]); s != "" {
				name = s
				break
			}
		}
		state := "unknown"
		for _, key := range []string{"conclusion", "status", "state"} {
			if s := prString(c[key]); s != "" {
				state = s
				break
			}
		}
		rows[i] = "- " + name + ": " + state
		if !passingCheckRE.MatchString(rows[i]) {
			failing++
		}
	}
	rollup := "Rollup: no failing checks in shown data"
	if failing != 0 {
		rollup = fmt.Sprintf("Rollup: %d non-passing checks in shown data", failing)
	}
	out := []string{rollup}
	out = append(out, rows[:min(len(rows), maxInlineChecks)]...)
	if len(rows) > maxInlineChecks {
		out = append(out, fmt.Sprintf("[%d of %d checks shown]", maxInlineChecks, len(rows)))
	}
	return out
}

func renderReviewVerdicts(v any, appendix *[]string) []string {
	var order []string
	latest := map[string]map[string]any{}
	for _, review := range prObjects(v) {
		author := authorLogin(firstNonNil(review["author"], review["user"]))
		if _, seen := latest[author]; !seen {
			order = append(order, author)
		}
		latest[author] = review
	}
	var out []string
	for _, author := range order {
		review := latest[author]
		state := prString(review["state"])
		if state == "" {
			state = "COMMENTED"
		}
		body := truncateText(prString(review["body"]), 300, "review by "+author, appendix)
		line := "- " + author + ": " + state
		if body != "" {
			line += " — " + strings.ReplaceAll(body, "\n", " ")
		}
		out = append(out, line)
	}
	return out
}

func renderReviewVerdictsOrUnavailable(view map[string]any, appendix *[]string) []string {
	if rendered := renderReviewVerdicts(view["reviews"], appendix); len(rendered) > 0 {
		return rendered
	}
	if view["reviewsUnavailable"] == true {
		return []string{"Unavailable in this GitHub fetch path; use gh for review verdicts."}
	}
	return nil
}

func renderFiles(v any) []string {
	var out []string
	for _, f := range prObjects(v) {
		path := prString(f["path"])
		if path == "" {
			path = prString(f["filename"])
		}
		if path == "" {
			path = "file"
		}
		additions, deletions := 0.0, 0.0
		if n, ok := prNumber(f["additions"]); ok {
			additions = n
		}
		if n, ok := prNumber(f["deletions"]); ok {
			deletions = n
		}
		out = append(out, "- "+path+" (+"+prNum(additions)+"/−"+prNum(deletions)+")")
	}
	return out
}

func appendFilesSection(lines []string, view map[string]any) []string {
	lines = append(lines, "## Files")
	files := renderFiles(view["files"])
	total, haveTotal := prNumber(view["changedFiles"])
	if len(files) == 0 {
		if haveTotal && total > 0 {
			return append(lines, fmt.Sprintf("[0 files shown from at least %s; use get_search_content offset or the gh command below for more]", prNum(total)), "")
		}
		return append(lines, "none", "")
	}
	lines = append(lines, files[:min(len(files), maxInlineFiles)]...)
	if haveTotal && total > float64(len(files)) {
		lines = append(lines, fmt.Sprintf("[%d files shown from at least %s; use get_search_content offset or the gh command below for more]", min(len(files), maxInlineFiles), prNum(total)))
	} else if len(files) > maxInlineFiles {
		lines = append(lines, fmt.Sprintf("[%d of %d files shown; use get_search_content offset or the gh command below for more]", maxInlineFiles, len(files)))
	}
	return append(lines, "")
}

func renderCommits(v any) []string {
	var out []string
	for _, c := range prObjects(v) {
		oid := prString(c["oid"])
		if oid == "" {
			oid = prString(c["sha"])
		}
		message := prString(c["messageHeadline"])
		if message == "" {
			if inner, ok := c["commit"].(map[string]any); ok {
				message = strings.SplitN(prString(inner["message"]), "\n", 2)[0]
			}
		}
		out = append(out, jsTrim("- "+jsSlice(oid, 0, 7)+" "+message))
	}
	return out
}

func renderCommitsOrUnavailable(view map[string]any) []string {
	if rendered := renderCommits(view["commits"]); len(rendered) > 0 {
		return rendered
	}
	if view["commitsUnavailable"] == true {
		return []string{"Unavailable in this GitHub fetch path; use gh for commits."}
	}
	return nil
}

func renderLinked(v any, closedBy bool) []string {
	var out []string
	for _, item := range prObjects(v) {
		number := "?"
		if n, ok := prNumber(item["number"]); ok {
			number = prNum(n)
		}
		line := "- #" + number + " " + prString(item["title"])
		if closedBy {
			line += " (" + prString(item["url"]) + ")"
		}
		out = append(out, line)
	}
	return out
}

func renderLinkedOrUnavailable(view map[string]any, field string, closedBy bool) []string {
	if linked := renderLinked(view[field], closedBy); len(linked) > 0 {
		return linked
	}
	if view["linkedReferencesUnavailable"] == true {
		return []string{"Unavailable in this GitHub fetch path; use gh for linked references."}
	}
	return nil
}

func renderReviewThreads(comments []map[string]any, anchor string, appendix *[]string, total any, bounded, unavailable bool) []string {
	if unavailable {
		return []string{"Unavailable in this GitHub fetch path; use gh for review thread comments."}
	}
	forced := anchoredIndex(comments, anchor)
	selected := selectInline(len(comments), maxInlineThreads, forced)
	var lines []string
	for pos, idx := range selected {
		c := comments[idx]
		path := prString(c["path"])
		if path == "" {
			path = "unknown path"
		}
		line := "?"
		for _, key := range []string{"line", "original_line", "position"} {
			if n, ok := prNumber(c[key]); ok {
				line = prNum(n)
				break
			}
		}
		label := commentID(c)
		if label == "" {
			label = strconv.Itoa(pos + 1)
		}
		body := truncateText(prString(c["body"]), commentInlineChars, "review thread comment "+label, appendix)
		mark := ""
		if idx == forced {
			mark = " [anchored]"
		}
		lines = append(lines, "- "+path+":"+line+" — "+authorLogin(firstNonNil(c["user"], c["author"]))+mark+" (resolution state unknown):\n  "+indent(body))
	}
	const hint = "use get_search_content offset, or gh api repos/<owner>/<repo>/pulls/<number>/comments]"
	known, haveKnown := prNumber(total)
	switch {
	case bounded:
		lines = append(lines, fmt.Sprintf("[%d review thread comments shown from at least %d; %s", len(selected), len(comments), hint))
	case haveKnown && known > float64(len(comments)):
		lines = append(lines, fmt.Sprintf("[%d review thread comments shown from at least %s; %s", len(selected), prNum(known), hint))
	case len(comments) > len(selected):
		lines = append(lines, fmt.Sprintf("[%d of %d review thread comments shown; %s", len(selected), len(comments), hint))
	}
	return lines
}

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

// renderGitHubPRIssue is renderGitHubPrIssue (github-issue-pr.ts:538).
func renderGitHubPRIssue(d issuePRRender) ExtractedContent {
	view := d.View
	var appendix []string
	number := strconv.Itoa(d.Number)
	title := prString(view["title"])
	if title == "" {
		title = d.Owner + "/" + d.Repo + "#" + number
	}
	viewNumber := float64(d.Number)
	if n, ok := prNumber(view["number"]); ok {
		viewNumber = n
	}
	state := prString(view["state"])
	if state == "" {
		state = "unknown"
	}
	if view["isDraft"] == true {
		state += " draft"
	}
	lines := []string{"#" + prNum(viewNumber) + " " + title, ""}
	lines = append(lines, "- type: "+d.Kind)
	stateLine := "- state: " + state
	if reason := prString(view["stateReason"]); reason != "" {
		stateLine += " (" + reason + ")"
	}
	lines = append(lines, stateLine, "- author: "+authorLogin(view["author"]))
	numOr0 := func(key string) string {
		if n, ok := prNumber(view[key]); ok {
			return prNum(n)
		}
		return "0"
	}
	if d.Kind == "pull" {
		headOwner := authorLogin(view["headRepositoryOwner"])
		head := prString(view["headRefName"])
		if headOwner != "unknown" && headOwner != d.Owner {
			head = headOwner + ":" + head
		}
		lines = append(lines, "- branch: "+prString(view["baseRefName"])+" ← "+head)
		commitSummary := fmt.Sprintf("%d commits", len(prObjects(view["commits"])))
		if view["commitsUnavailable"] == true {
			commitSummary = "commits unavailable"
		}
		files := strconv.Itoa(len(prObjects(view["files"])))
		if n, ok := prNumber(view["changedFiles"]); ok {
			files = prNum(n)
		}
		lines = append(lines, "- changes: +"+numOr0("additions")+" −"+numOr0("deletions")+", "+files+" files, "+commitSummary)
	} else {
		lines = append(lines, "- assignees: "+listNames(view["assignees"]))
	}
	created := prString(view["createdAt"])
	if created == "" {
		created = "unknown"
	}
	lines = append(lines, "- created: "+created)
	if merged := prString(view["mergedAt"]); merged != "" {
		lines = append(lines, "- merged: "+merged)
	}
	if closed := prString(view["closedAt"]); closed != "" {
		lines = append(lines, "- closed: "+closed)
	}
	lines = append(lines, "- labels: "+listNames(view["labels"]), "- milestone: "+milestoneTitle(view["milestone"]),
		"- comments: "+commentCountText(view)+"; review threads: "+reviewThreadCountText(d.ReviewThreads, view["reviewCommentsCount"], d.ReviewThreadsBounded, d.ReviewThreadsUnavailable))
	if d.Anchor != "" {
		lines = append(lines, "- requested anchor: #"+d.Anchor)
	}
	lines = append(lines, "")

	body := prString(view["body"])
	if body == "" {
		body = "(empty)"
	}
	lines = append(lines, "## Body", truncateText(body, bodyInlineChars, "body", &appendix), "")

	if d.Kind == "pull" {
		lines = append(lines, "## Checks")
		lines = append(lines, renderChecks(view["statusCheckRollup"])...)
		lines = append(lines, "")
		lines = appendLimitedSection(lines, "Review verdicts", renderReviewVerdictsOrUnavailable(view, &appendix), maxInlineReviews, "review verdicts")
		lines = appendLimitedSection(lines, "Linked references", renderLinkedOrUnavailable(view, "closingIssuesReferences", false), 20, "linked references")
		lines = appendFilesSection(lines, view)
		lines = appendLimitedSection(lines, "Commits", renderCommitsOrUnavailable(view), maxInlineCommits, "commits")
	} else {
		lines = appendLimitedSection(lines, "Closed by pull requests", renderLinkedOrUnavailable(view, "closedByPullRequestsReferences", true), 20, "pull requests")
	}
	lines = append(lines, "## Conversation comments")
	comments := prObjects(view["comments"])
	commentLines := renderComments(comments, d.Anchor, &appendix)
	if marker, ok := renderCommentMarker(comments, len(commentLines), view["commentsCount"]); ok {
		commentLines = append(commentLines, marker)
	}
	if view["anchorUnavailable"] == true && strings.HasPrefix(d.Anchor, "issuecomment-") {
		commentLines = append(commentLines, "[anchored comment unavailable for this issue or pull request]")
	}
	if len(commentLines) == 0 {
		commentLines = []string{"none"}
	}
	lines = append(lines, commentLines...)
	lines = append(lines, "")
	if d.Kind == "pull" {
		lines = append(lines, "## Review thread comments")
		threadLines := renderReviewThreads(d.ReviewThreads, d.Anchor, &appendix, view["reviewCommentsCount"], d.ReviewThreadsBounded, d.ReviewThreadsUnavailable)
		if view["anchorUnavailable"] == true && strings.HasPrefix(d.Anchor, "discussion_r") {
			threadLines = append(threadLines, "[anchored review thread comment unavailable for this pull request]")
		}
		if len(threadLines) == 0 {
			threadLines = []string{"none"}
		}
		lines = append(lines, threadLines...)
		lines = append(lines, "")
	}
	if len(d.FallbackNotes) > 0 {
		lines = append(lines, "## Availability notes")
		for _, note := range d.FallbackNotes {
			lines = append(lines, "- "+note)
		}
		lines = append(lines, "")
	}
	if len(appendix) > 0 {
		lines = append(lines, "## Appendix")
		lines = append(lines, appendix...)
		lines = append(lines, "")
	}
	repoArg := d.Owner + "/" + d.Repo
	viewCommand := "gh pr view " + number + " --repo " + repoArg
	if d.Kind != "pull" {
		viewCommand = "gh issue view " + number + " --repo " + repoArg + " -c"
	}
	lines = append(lines, "## Escalation commands", "- Full view: `"+viewCommand+"`")
	if d.Kind == "pull" {
		lines = append(lines, "- Complete diff: `gh pr diff "+number+" --repo "+repoArg+"`")
	}
	lines = append(lines, "- Stored full document: use `get_search_content` with `offset` or `findText`.")
	content := strings.Join(lines, "\n")
	if jsLen(content) > maxDocChars {
		content = jsSlice(content, 0, maxDocChars) + fmt.Sprintf("\n\n[GitHub document truncated at %d chars; use the gh escalation commands for complete data]", maxDocChars)
	}
	return ExtractedContent{URL: d.URL, Title: d.Owner + "/" + d.Repo + " " + d.Kind + " #" + number + ": " + title, Content: content}
}

// ---- gh -----------------------------------------------------------------------------------

func runGh(ctx context.Context, args []string, timeout time.Duration) processResult {
	return runProcess(ctx, "gh", args, processOptions{Timeout: timeout, MaxBuffer: ghMaxBuffer, Env: []string{"GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0"}})
}

func remainingGhTimeout(deadline time.Time) time.Duration {
	return max(time.Millisecond, min(ghTimeout, time.Until(deadline)))
}

type ghViewResult struct {
	view  map[string]any
	notes []string
}

// ghView is ghView: `gh pr|issue view --json`, once more with the core fields when this gh version
// does not know one of the requested fields.
func ghView(ctx context.Context, info *githubIssuePRURL, deadline time.Time) *ghViewResult {
	repoArg := info.Owner + "/" + info.Repo
	command, fields, core := "issue", issueFields, issueCore
	if info.Kind == "pull" {
		command, fields, core = "pr", prFields, prCoreFields
	}
	view := func(fs []string) processResult {
		return runGh(ctx, []string{command, "view", strconv.Itoa(info.Number), "--repo", repoArg, "--json", strings.Join(fs, ",")}, remainingGhTimeout(deadline))
	}
	res := view(fields)
	var notes []string
	linkedUnavailable := false
	if !res.OK && unknownJSONFieldE.MatchString(res.Stderr+"\n"+res.Error) {
		res = view(core)
		notes = append(notes, "Some GitHub fields were unavailable from this gh version; retried with the core field set.")
		linkedUnavailable = true
	}
	if !res.OK {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		return nil
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return nil
	}
	if linkedUnavailable {
		m["linkedReferencesUnavailable"] = true
	}
	return &ghViewResult{view: m, notes: notes}
}

type ghThreads struct {
	comments          []map[string]any
	note              string
	bounded           bool
	unavailable       bool
	anchorUnavailable bool
}

func anchorID(anchor string, re *regexp.Regexp) string {
	if m := re.FindStringSubmatch(anchor); m != nil {
		return m[1]
	}
	return ""
}

func hasCommentID(comments []map[string]any, id string) bool {
	for _, c := range comments {
		if commentID(c) == id {
			return true
		}
	}
	return false
}

// associationMatches checks that a comment's *_url names this work item on api.github.com.
func associationMatches(v any, info *githubIssuePRURL, route string) bool {
	raw, ok := v.(string)
	if !ok {
		return false
	}
	u, err := ParseURL(raw, nil)
	if err != nil || u.Scheme != "https" || strings.ToLower(u.Hostname()) != "api.github.com" {
		return false
	}
	return strings.ToLower(u.EscapedPath()) == strings.ToLower(fmt.Sprintf("/repos/%s/%s/%s/%d", info.Owner, info.Repo, route, info.Number))
}

func belongsToIssue(c map[string]any, id string, info *githubIssuePRURL) bool {
	return commentID(c) == id && associationMatches(c["issue_url"], info, "issues")
}

func belongsToPull(c map[string]any, id string, info *githubIssuePRURL) bool {
	return commentID(c) == id && associationMatches(c["pull_request_url"], info, "pulls")
}

// ghReviewThreads is ghReviewThreads: up to three pages of review comments, then the anchored
// review comment when the pages do not hold it.
func ghReviewThreads(ctx context.Context, info *githubIssuePRURL, deadline time.Time) ghThreads {
	if info.Kind != "pull" {
		return ghThreads{}
	}
	var comments []map[string]any
	bounded, pageFailed := false, false
	for page := 1; page <= 3; page++ {
		res := runGh(ctx, []string{"api", fmt.Sprintf("repos/%s/%s/pulls/%d/comments?per_page=100&page=%d", info.Owner, info.Repo, info.Number, page)}, remainingGhTimeout(deadline))
		if !res.OK {
			pageFailed = true
			break
		}
		var items any
		if err := json.Unmarshal([]byte(res.Stdout), &items); err != nil {
			pageFailed = true
			break
		}
		list, ok := items.([]any)
		if !ok || len(list) == 0 {
			break
		}
		comments = append(comments, prObjects(list)...)
		if len(list) < 100 {
			break
		}
		if page == 3 {
			bounded = true
		}
	}
	result := func() ghThreads {
		return ghThreads{comments: comments, bounded: bounded || (pageFailed && len(comments) > 0), unavailable: pageFailed && len(comments) == 0}
	}
	if id := anchorID(info.Anchor, discussionIDRE); id != "" && !hasCommentID(comments, id) {
		res := runGh(ctx, []string{"api", fmt.Sprintf("repos/%s/%s/pulls/comments/%s", info.Owner, info.Repo, id)}, remainingGhTimeout(deadline))
		if !res.OK {
			r := result()
			r.anchorUnavailable = true
			return r
		}
		var parsed any
		if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
			r := result()
			r.note = "Anchored review thread comment unavailable from gh."
			return r
		}
		if c, ok := parsed.(map[string]any); ok && belongsToPull(c, id, info) {
			comments = append(comments, c)
		} else {
			r := result()
			r.anchorUnavailable = true
			return r
		}
	}
	return result()
}

// ---- REST fallback ------------------------------------------------------------------------

var restHeaders = http.Header{"Accept": {"application/vnd.github+json"}, "User-Agent": {"pi-web-access"}}

type restResponse struct {
	value any
	resp  *http.Response
}

func (r *restResponse) ok() bool { return r.resp.StatusCode >= 200 && r.resp.StatusCode <= 299 }

func isRateLimited(resp *http.Response) bool {
	return resp.StatusCode == 403 && resp.Header.Get("x-ratelimit-remaining") == "0"
}

func rateLimitResult(rawURL string, info *githubIssuePRURL, resp *http.Response) *ExtractedContent {
	resetNote := ""
	if reset := resp.Header.Get("x-ratelimit-reset"); reset != "" {
		resetNote = " Rate limit resets at Unix time " + reset + "."
	}
	status := resp.StatusCode
	return &ExtractedContent{
		URL:     rawURL,
		Title:   fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.Number),
		Content: fmt.Sprintf("GitHub API rate limit reached for %s/%s %s #%d.%s\n\nAuthenticate the gh CLI and retry:\n\n`gh auth login`\n\nThen fetch this URL again.", info.Owner, info.Repo, info.Kind, info.Number, resetNote),
		Error:   errp("GitHub API rate limit reached; authenticate gh to fetch this PR or issue."),
		Status:  &status,
	}
}

// restJSON is restJson: a GET of api.github.com through the SSRF guard; the body is decoded only
// for a 2xx answer.
func restJSON(ctx context.Context, rawURL string, o ExtractOptions) (*restResponse, error) {
	ssrf, err := LoadSsrfConfig()
	if err != nil {
		return nil, err
	}
	policy, err := LoadFetchContentDomainPolicy()
	if err != nil {
		return nil, err
	}
	resp, err := FetchRemoteURL(ctx, rawURL, RequestInit{Method: "GET", Header: restHeaders.Clone()}, FetchRemoteOptions{
		ValidationOptions: ValidationOptions{Lookup: o.Lookup, DomainPolicy: &policy, AllowRanges: ssrf.AllowRanges, TrustEnvProxy: ssrf.TrustEnvProxy},
		Fetch:             currentPageFetch(),
	})
	if err != nil {
		return nil, err
	}
	r := &restResponse{resp: resp}
	if !r.ok() {
		_ = resp.Body.Close()
		return r, nil
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	if err := json.Unmarshal([]byte(text), &r.value); err != nil {
		return nil, err
	}
	return r, nil
}

func mapRestView(info *githubIssuePRURL, value any) map[string]any {
	source, _ := value.(map[string]any)
	if source == nil {
		source = map[string]any{}
	}
	user, _ := source["user"].(map[string]any)
	base, _ := source["base"].(map[string]any)
	head, _ := source["head"].(map[string]any)
	login := func(m map[string]any) string {
		if m == nil {
			return ""
		}
		return prString(m["login"])
	}
	ref := func(m map[string]any) string {
		if m == nil {
			return ""
		}
		return prString(m["ref"])
	}
	headOwner := ""
	if head != nil {
		if repo, ok := head["repo"].(map[string]any); ok {
			owner, _ := repo["owner"].(map[string]any)
			headOwner = login(owner)
		}
	}
	number := float64(info.Number)
	if n, ok := prNumber(source["number"]); ok {
		number = n
	}
	return map[string]any{
		"title":               prString(source["title"]),
		"number":              number,
		"state":               prString(source["state"]),
		"stateReason":         prString(source["state_reason"]),
		"isDraft":             prTruthy(source["draft"]),
		"author":              map[string]any{"login": login(user)},
		"baseRefName":         ref(base),
		"headRefName":         ref(head),
		"headRepositoryOwner": map[string]any{"login": headOwner},
		"createdAt":           prString(source["created_at"]),
		"mergedAt":            prString(source["merged_at"]),
		"closedAt":            prString(source["closed_at"]),
		"labels":              source["labels"],
		"assignees":           source["assignees"],
		"milestone":           source["milestone"],
		"additions":           source["additions"],
		"deletions":           source["deletions"],
		"changedFiles":        source["changed_files"],
		"body":                prString(source["body"]),
		"url":                 prString(source["html_url"]),
		"commentsCount":       source["comments"],
		"reviewCommentsCount": source["review_comments"],
	}
}

// restOutcome is what restFallback produced: a finished result, or the data to render.
type restOutcome struct {
	direct *ExtractedContent
	data   *issuePRRender
}

// restFallbackSteps is restFallback's body: the requests of github-issue-pr.ts:299-359, in order.
// A nil outcome is "nothing usable" (the original's null); an error is its thrown exception.
func restFallbackSteps(ctx context.Context, rawURL string, info *githubIssuePRURL, o ExtractOptions) (*restOutcome, error) {
	base := fmt.Sprintf("https://api.github.com/repos/%s/%s", info.Owner, info.Repo)
	mainPath := base + "/issues/" + strconv.Itoa(info.Number)
	if info.Kind == "pull" {
		mainPath = base + "/pulls/" + strconv.Itoa(info.Number)
	}
	limited := func(r *restResponse) *restOutcome {
		if isRateLimited(r.resp) {
			return &restOutcome{direct: rateLimitResult(rawURL, info, r.resp)}
		}
		return nil
	}
	main, err := restJSON(ctx, mainPath, o)
	if err != nil {
		return nil, err
	}
	if out := limited(main); out != nil {
		return out, nil
	}
	if !main.ok() {
		return nil, nil
	}
	view := mapRestView(info, main.value)
	view["linkedReferencesUnavailable"] = true
	comments, err := restJSON(ctx, base+"/issues/"+strconv.Itoa(info.Number)+"/comments?per_page=50", o)
	if err != nil {
		return nil, err
	}
	if out := limited(comments); out != nil {
		return out, nil
	}
	conversation := []map[string]any{}
	if comments.ok() {
		conversation = append(conversation, prObjects(comments.value)...)
	}
	if id := anchorID(info.Anchor, issueCommentIDRE); id != "" && !hasCommentID(conversation, id) {
		anchored, err := restJSON(ctx, base+"/issues/comments/"+id, o)
		if err != nil {
			return nil, err
		}
		if out := limited(anchored); out != nil {
			return out, nil
		}
		if c, ok := anchored.value.(map[string]any); ok && anchored.ok() {
			if belongsToIssue(c, id, info) {
				conversation = append(conversation, c)
			} else {
				view["anchorUnavailable"] = true
			}
		} else {
			view["anchorUnavailable"] = true
		}
	}
	asAny := make([]any, len(conversation))
	for i, c := range conversation {
		asAny[i] = c
	}
	view["comments"] = asAny
	var reviewThreads []map[string]any
	notes := []string{"REST fallback used; gh fields unavailable."}
	if info.Kind == "pull" {
		notes = []string{"Checks unavailable in REST fallback; install or authenticate gh for check status."}
		view["reviewsUnavailable"] = true
		view["commitsUnavailable"] = true
		files, err := restJSON(ctx, base+"/pulls/"+strconv.Itoa(info.Number)+"/files?per_page=50", o)
		if err != nil {
			return nil, err
		}
		if out := limited(files); out != nil {
			return out, nil
		}
		if list, ok := files.value.([]any); ok && files.ok() {
			view["files"] = list
		}
		threads, err := restJSON(ctx, base+"/pulls/"+strconv.Itoa(info.Number)+"/comments?per_page=50", o)
		if err != nil {
			return nil, err
		}
		if out := limited(threads); out != nil {
			return out, nil
		}
		if threads.ok() {
			if list, ok := threads.value.([]any); ok {
				reviewThreads = append(reviewThreads, prObjects(list)...)
			}
		}
		if id := anchorID(info.Anchor, discussionIDRE); id != "" && !hasCommentID(reviewThreads, id) {
			anchored, err := restJSON(ctx, base+"/pulls/comments/"+id, o)
			if err != nil {
				return nil, err
			}
			if out := limited(anchored); out != nil {
				return out, nil
			}
			if c, ok := anchored.value.(map[string]any); ok && anchored.ok() {
				if belongsToPull(c, id, info) {
					reviewThreads = append(reviewThreads, c)
				} else {
					view["anchorUnavailable"] = true
				}
			} else {
				view["anchorUnavailable"] = true
			}
		}
	}
	return &restOutcome{data: &issuePRRender{URL: rawURL, Owner: info.Owner, Repo: info.Repo, Kind: info.Kind, Number: info.Number, Anchor: info.Anchor, View: view, ReviewThreads: reviewThreads, FallbackNotes: notes}}, nil
}

// restFallback is restFallback: the failure of any request becomes the document's error, a
// cancellation becomes "Aborted".
func restFallback(ctx context.Context, rawURL string, info *githubIssuePRURL, o ExtractOptions) *restOutcome {
	out, err := restFallbackSteps(ctx, rawURL, info, o)
	if err == nil {
		return out
	}
	if ctx.Err() != nil || isAbortError(err) || errors.Is(err, context.Canceled) {
		return &restOutcome{direct: &ExtractedContent{URL: rawURL, Error: errp("Aborted")}}
	}
	return &restOutcome{direct: &ExtractedContent{
		URL: rawURL, Title: fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.Number), Content: err.Error(),
		Error: errp("GitHub REST fallback failed: " + err.Error()),
	}}
}

// extractGitHubIssuePR is extractGitHubIssuePr (github-issue-pr.ts:671): nil when the URL is not an
// issue or pull request URL or the extractor is disabled; "Aborted" when the call was cancelled;
// otherwise the document from gh, else from the REST API (nil when neither answers).
func (s *githubState) extractGitHubIssuePR(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	info := parseGitHubIssuePRURL(rawURL)
	if info == nil {
		return nil, nil
	}
	cfg, err := s.issuePRConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	aborted := func() (*ExtractedContent, error) {
		return &ExtractedContent{URL: rawURL, Error: errp("Aborted")}, nil
	}
	if ctx.Err() != nil {
		return aborted()
	}
	available := s.checkGhAvailable(ctx)
	if ctx.Err() != nil {
		return aborted()
	}
	if available {
		deadline := time.Now().Add(ghTotalTimeout)
		viewed := ghView(ctx, info, deadline)
		if ctx.Err() != nil {
			return aborted()
		}
		if viewed != nil {
			threads := ghReviewThreads(ctx, info, deadline)
			if ctx.Err() != nil {
				return aborted()
			}
			if threads.anchorUnavailable {
				viewed.view["anchorUnavailable"] = true
			}
			notes := viewed.notes
			if threads.note != "" {
				notes = append(notes, threads.note)
			}
			res := renderGitHubPRIssue(issuePRRender{URL: rawURL, Owner: info.Owner, Repo: info.Repo, Kind: info.Kind, Number: info.Number, Anchor: info.Anchor,
				View: viewed.view, ReviewThreads: threads.comments, ReviewThreadsBounded: threads.bounded, ReviewThreadsUnavailable: threads.unavailable, FallbackNotes: notes})
			return &res, nil
		}
	} else {
		s.showGhHint()
	}
	out := restFallback(ctx, rawURL, info, o)
	switch {
	case out == nil:
		return nil, nil
	case out.direct != nil:
		return out.direct, nil
	}
	res := renderGitHubPRIssue(*out.data)
	return &res, nil
}

func extractGitHubIssuePR(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	return defaultGitHub().extractGitHubIssuePR(ctx, rawURL, o)
}
