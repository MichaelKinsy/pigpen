package websearch

// Twins of github-issue-pr.test.mjs.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

const ghIssuePRFile = "github-issue-pr"

// prWorld is an isolated agent directory with fake executables ahead of PATH and no githubClone settings.
func prWorld(t *testing.T) *ghWorld {
	t.Helper()
	w := newGHWorld(t, nil)
	writeConfig(t, w.agentDir, "{}")
	return w
}

func extractPR(t *testing.T, rawURL string, opts ExtractOptions) *ExtractedContent {
	t.Helper()
	s := ghState(t)
	res, err := s.extractGitHubIssuePR(bg(), rawURL, opts)
	noErr(t, err)
	if res == nil {
		t.Fatal("no result")
	}
	return res
}

// pagedGh answers the review comment pages of PR 7 with 100 comments each.
const pagedComments = `i=1
printf '['
while [ $i -le 100 ]; do
  [ $i -gt 1 ] && printf ','
  printf '{"id":%d,"user":{"login":"page"},"path":"a.ts","line":1,"body":"paged"}' $i
  i=$((i+1))
done
printf ']'`

func TestUpstream_github_issue_pr(t *testing.T) {
	F := ghIssuePRFile

	tw(t, F, "parseGitHubIssuePrUrl accepts PR and issue variants without changing repo parsing", func(t *testing.T) {
		cases := []struct {
			url  string
			want *githubIssuePRURL
		}{
			{"https://github.com/owner/repo/pull/123/files#discussion_r42", &githubIssuePRURL{Owner: "owner", Repo: "repo", Kind: "pull", Number: 123, Anchor: "discussion_r42"}},
			{"https://www.github.com/owner/repo/issues/5#issuecomment-99", &githubIssuePRURL{Owner: "owner", Repo: "repo", Kind: "issue", Number: 5, Anchor: "issuecomment-99"}},
			{"https://www.github.com/owner/repo/issues/5#discussion_r99", &githubIssuePRURL{Owner: "owner", Repo: "repo", Kind: "issue", Number: 5}},
			{"https://github.com/owner/repo/pulls", nil},
			{"https://github.com/owner/repo/issues/new", nil},
			{"https://github.com/owner/repo/pull/123/unknown", nil},
			{"https://github.com/owner/repo/issues/5/edit", nil},
			{"https://github.com/owner%2Frepo/repo/issues/1", nil},
		}
		for _, c := range cases {
			if got := parseGitHubIssuePRURL(c.url); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("%s: got %+v, want %+v", c.url, got, c.want)
			}
		}
	})

	tw(t, F, "renderer orders sections, marks truncation, and forces anchored comments inline", func(t *testing.T) {
		comments := make([]any, 20)
		for i := range comments {
			body := fmt.Sprintf("comment %d", i+1)
			if i == 18 {
				body = "anchored comment body"
			}
			comments[i] = map[string]any{"id": float64(i + 1), "user": map[string]any{"login": fmt.Sprintf("user%d", i+1)}, "created_at": "2026-01-01T00:00:00Z", "body": body}
		}
		res := renderGitHubPRIssue(issuePRRender{
			URL: "https://github.com/owner/repo/pull/7#issuecomment-19", Owner: "owner", Repo: "repo", Kind: "pull", Number: 7, Anchor: "issuecomment-19",
			View: map[string]any{
				"number": 7.0, "title": "Improve fetch", "state": "OPEN", "isDraft": false, "author": map[string]any{"login": "alice"},
				"baseRefName": "main", "headRefName": "feature", "headRepositoryOwner": map[string]any{"login": "fork"},
				"createdAt": "2026-01-01T00:00:00Z", "labels": []any{map[string]any{"name": "bug"}},
				"additions": 5.0, "deletions": 1.0, "changedFiles": 1.0, "body": strings.Repeat("x", 4100),
				"files":   []any{map[string]any{"path": "a.ts", "additions": 5.0, "deletions": 1.0}},
				"commits": []any{map[string]any{"oid": "abcdef123456", "messageHeadline": "commit subject"}},
				"comments": comments, "statusCheckRollup": []any{map[string]any{"name": "test", "conclusion": "SUCCESS"}},
			},
		})
		if res.Error != nil {
			t.Fatal(*res.Error)
		}
		c := res.Content
		matchRE(t, `#7 Improve fetch`, c)
		if strings.Index(c, "## Body") > strings.Index(c, "## Checks") {
			t.Fatal("Body must come before Checks")
		}
		matchRE(t, `\[body truncated; use get_search_content`, c)
		matchRE(t, `(?s)user19.*\[anchored\]`, c)
		matchRE(t, `15 of 20 comments shown|16 of 20 comments shown`, c)
		matchRE(t, `gh pr diff 7 --repo owner/repo`, c)
	})

	tw(t, F, "renderer marks truncated review verdicts", func(t *testing.T) {
		reviews := make([]any, 11)
		for i := range reviews {
			reviews[i] = map[string]any{"author": map[string]any{"login": fmt.Sprintf("reviewer%d", i)}, "state": "COMMENTED", "body": fmt.Sprintf("review %d", i)}
		}
		res := renderGitHubPRIssue(issuePRRender{
			URL: "https://github.com/owner/repo/pull/7", Owner: "owner", Repo: "repo", Kind: "pull", Number: 7,
			View: map[string]any{"number": 7.0, "title": "Reviews", "state": "OPEN", "author": map[string]any{"login": "alice"}, "body": "body", "reviews": reviews},
		})
		matchRE(t, `10 of 11 review verdicts shown`, res.Content)
	})

	tw(t, F, "extractGitHubIssuePr retries gh view with core fields on unknown --json field", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `if [ "$1" = "--version" ]; then echo "gh version 2.0.0"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  for a; do [ "$prev" = "--json" ] && fields="$a"; prev="$a"; done
  case "$fields" in *statusCheckRollup*) echo "Unknown JSON field: statusCheckRollup" >&2; exit 1;; esac
  echo '{"number":12,"title":"Fallback fields","state":"OPEN","author":{"login":"alice"},"body":"body","comments":[]}'
  exit 0
fi
if [ "$1" = "api" ]; then echo '[]'; exit 0; fi
exit 1`)
		res := extractPR(t, "https://github.com/owner/repo/pull/12", ExtractOptions{})
		matchRE(t, `Fallback fields`, res.Content)
		matchRE(t, `retried with the core field set`, res.Content)
		matchRE(t, `Linked references\nUnavailable in this GitHub fetch path`, res.Content)
	})

	tw(t, F, "gh path fetches an anchored review comment beyond fetched pages", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `if [ "$1" = "--version" ]; then echo "gh version 2.0.0"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{"number":7,"title":"Deep anchor","state":"OPEN","author":{"login":"alice"},"body":"body","comments":[]}'
  exit 0
fi
case "$2" in
  *'/pulls/7/comments?'*) `+pagedComments+`; exit 0;;
  */pulls/comments/999) echo '{"id":999,"pull_request_url":"https://api.github.com/repos/owner/repo/pulls/7","user":{"login":"reviewer"},"path":"deep.ts","line":44,"body":"deep anchor"}'; exit 0;;
esac
exit 1`)
		res := extractPR(t, "https://github.com/owner/repo/pull/7#discussion_r999", ExtractOptions{})
		matchRE(t, `(?s)reviewer.*\[anchored\]`, res.Content)
		matchRE(t, `deep anchor`, res.Content)
		matchRE(t, `review threads: at least 301`, res.Content)
		matchRE(t, `review thread comments shown from at least 301`, res.Content)
	})

	tw(t, F, "gh path rejects anchored review comments from another pull request", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `if [ "$1" = "--version" ]; then echo "gh version 2.0.0"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{"number":7,"title":"Cross anchor","state":"OPEN","author":{"login":"alice"},"body":"body","comments":[]}'
  exit 0
fi
case "$2" in
  *'/pulls/7/comments?'*) echo '[]'; exit 0;;
  */pulls/comments/999) echo '{"id":999,"pull_request_url":"https://api.github.com/repos/other/repo/pulls/7","user":{"login":"wrong"},"path":"wrong.ts","line":1,"body":"wrong pull"}'; exit 0;;
esac
exit 1`)
		res := extractPR(t, "https://github.com/owner/repo/pull/7#discussion_r999", ExtractOptions{})
		noMatchRE(t, `wrong pull`, res.Content)
		matchRE(t, `anchored review thread comment unavailable for this pull request`, res.Content)
	})

	tw(t, F, "gh path still attempts an anchored review comment after page failure", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `if [ "$1" = "--version" ]; then echo "gh version 2.0.0"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{"number":7,"title":"Page fail anchor","state":"OPEN","author":{"login":"alice"},"body":"body","comments":[]}'
  exit 0
fi
case "$2" in
  *'/pulls/7/comments?'*) exit 1;;
  */pulls/comments/999) echo '{"id":999,"pull_request_url":"https://api.github.com/repos/owner/repo/pulls/7","user":{"login":"reviewer"},"path":"deep.ts","line":44,"body":"deep after page failure"}'; exit 0;;
esac
exit 1`)
		res := extractPR(t, "https://github.com/owner/repo/pull/7#discussion_r999", ExtractOptions{})
		matchRE(t, `(?s)reviewer.*\[anchored\]`, res.Content)
		matchRE(t, `deep after page failure`, res.Content)
	})

	tw(t, F, "gh review thread page failure renders unavailable instead of none", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `if [ "$1" = "--version" ]; then echo "gh version 2.0.0"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{"number":7,"title":"Unavailable reviews","state":"OPEN","author":{"login":"alice"},"body":"body","comments":[]}'
  exit 0
fi
exit 1`)
		res := extractPR(t, "https://github.com/owner/repo/pull/7", ExtractOptions{})
		matchRE(t, `review threads: unavailable`, res.Content)
		matchRE(t, `Review thread comments\nUnavailable in this GitHub fetch path`, res.Content)
	})

	tw(t, F, "extractGitHubIssuePr aborts active gh work with the caller signal", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `if [ "$1" = "--version" ]; then echo "gh version 2.0.0"; exit 0; fi
sleep 30`)
		n := useNet(t, func(netCall) netReply { return reply(200, "{}") })
		ctx, cancel := context.WithCancel(bg())
		time.AfterFunc(50*time.Millisecond, cancel)
		defer cancel()
		started := time.Now()
		res, err := ghState(t).extractGitHubIssuePR(ctx, "https://github.com/owner/repo/pull/7", pageOpts())
		noErr(t, err)
		if res == nil || errOf(*res) != "Aborted" {
			t.Fatalf("got %+v", res)
		}
		if el := time.Since(started); el > 5*time.Second {
			t.Fatalf("expected abort before the gh timeout, got %v", el)
		}
		wantCalls(t, n)
	})

	tw(t, F, "extractGitHubIssuePr aborts a hung gh availability probe", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `sleep 30`)
		ctx, cancel := context.WithCancel(bg())
		time.AfterFunc(50*time.Millisecond, cancel)
		defer cancel()
		started := time.Now()
		res, err := ghState(t).extractGitHubIssuePR(ctx, "https://github.com/owner/repo/pull/7", ExtractOptions{})
		noErr(t, err)
		if res == nil || errOf(*res) != "Aborted" {
			t.Fatalf("got %+v", res)
		}
		if el := time.Since(started); el > 5*time.Second {
			t.Fatalf("expected abort before the gh probe timeout, got %v", el)
		}
	})

	tw(t, F, "REST fallback preserves caller cancellation", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `exit 1`)
		ctx, cancel := context.WithCancel(bg())
		defer cancel()
		var fetched []string
		t.Cleanup(SetPageFetch(func(c context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
			fetched = append(fetched, u.String())
			time.AfterFunc(10*time.Millisecond, cancel)
			<-c.Done()
			return nil, fmt.Errorf("Aborted REST fetch")
		}))
		res := ExtractContent(ctx, "https://github.com/owner/repo/issues/12", pageOpts())
		if errOf(res) != "Aborted" {
			t.Fatalf("got %+v", res)
		}
		// The invariant is ordering: the aborted REST request is the only fetch, the generic github.com
		// page fallback never starts.
		if !eqStrings(fetched, []string{"https://api.github.com/repos/owner/repo/issues/12"}) {
			t.Fatalf("fetched %v", fetched)
		}
	})

	tw(t, F, "extractContent returns an actionable GitHub rate-limit result instead of HTML fall-through", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `exit 1`)
		useNet(t, func(netCall) netReply {
			return netReply{Status: 403, Body: "rate limited", Header: hdr("x-ratelimit-remaining", "0", "x-ratelimit-reset", "1800000000")}
		})
		res := ExtractContent(bg(), "https://github.com/owner/repo/issues/12", pageOpts())
		if res.Status == nil || *res.Status != 403 {
			t.Fatalf("status %+v", res.Status)
		}
		matchRE(t, `(?i)rate limit`, errOf(res))
		matchRE(t, `gh auth login`, res.Content)
	})

	tw(t, F, "REST fallback fetches an anchored issue comment outside the first page", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `exit 1`)
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasSuffix(c.URL, "/issues/12"):
				return reply(200, `{"number":12,"title":"Issue","state":"open","user":{"login":"owner"},"body":"body","comments":75}`)
			case strings.HasSuffix(c.URL, "/issues/12/comments?per_page=50"):
				return reply(200, `[{"id":1,"user":{"login":"first"},"created_at":"2026-01-01T00:00:00Z","body":"first page"}]`)
			case strings.HasSuffix(c.URL, "/issues/comments/99"):
				return reply(200, `{"id":99,"issue_url":"https://api.github.com/repos/owner/repo/issues/12","user":{"login":"anchored"},"created_at":"2026-01-02T00:00:00Z","body":"outside first page"}`)
			}
			return netReply{Err: fmt.Errorf("unexpected URL %s", c.URL)}
		})
		res := ExtractContent(bg(), "https://github.com/owner/repo/issues/12#issuecomment-99", pageOpts())
		matchRE(t, `(?s)anchored.*\[anchored\]`, res.Content)
		matchRE(t, `outside first page`, res.Content)
		matchRE(t, `comments: at least 75`, res.Content)
		matchRE(t, `comments shown from at least 75`, res.Content)
	})

	tw(t, F, "REST fallback rejects anchored issue comments from another work item", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `exit 1`)
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasSuffix(c.URL, "/issues/12"):
				return reply(200, `{"number":12,"title":"Issue","state":"open","user":{"login":"owner"},"body":"body","comments":1}`)
			case strings.HasSuffix(c.URL, "/issues/12/comments?per_page=50"):
				return reply(200, `[]`)
			case strings.HasSuffix(c.URL, "/issues/comments/99"):
				return reply(200, `{"id":98,"issue_url":"https://api.github.com/repos/other/repo/issues/12","user":{"login":"wrong"},"created_at":"2026-01-02T00:00:00Z","body":"wrong issue"}`)
			}
			return netReply{Err: fmt.Errorf("unexpected URL %s", c.URL)}
		})
		res := ExtractContent(bg(), "https://github.com/owner/repo/issues/12#issuecomment-99", pageOpts())
		noMatchRE(t, `wrong issue`, res.Content)
		matchRE(t, `anchored comment unavailable for this issue or pull request`, res.Content)
	})

	tw(t, F, "REST fallback fetches an anchored PR review comment outside the first page", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `exit 1`)
		files := make([]string, 50)
		for i := range files {
			files[i] = fmt.Sprintf(`{"filename":"file%d.ts","additions":1,"deletions":0}`, i)
		}
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasSuffix(c.URL, "/pulls/7"):
				return reply(200, `{"number":7,"title":"PR","state":"open","user":{"login":"owner"},"body":"body","draft":false,"base":{"ref":"main"},"head":{"ref":"feature","repo":{"owner":{"login":"owner"}}},"additions":1,"deletions":0,"changed_files":75,"review_comments":12}`)
			case strings.HasSuffix(c.URL, "/issues/7/comments?per_page=50"):
				return reply(200, `[]`)
			case strings.HasSuffix(c.URL, "/pulls/7/files?per_page=50"):
				return reply(200, "["+strings.Join(files, ",")+"]")
			case strings.HasSuffix(c.URL, "/pulls/7/comments?per_page=50"):
				return reply(200, `[{"id":1,"user":{"login":"first"},"path":"a.ts","line":1,"body":"first review"}]`)
			case strings.HasSuffix(c.URL, "/pulls/comments/42"):
				return reply(200, `{"id":42,"pull_request_url":"https://api.github.com/repos/owner/repo/pulls/7","user":{"login":"reviewer"},"path":"b.ts","line":9,"body":"outside review page"}`)
			}
			return netReply{Err: fmt.Errorf("unexpected URL %s", c.URL)}
		})
		res := ExtractContent(bg(), "https://github.com/owner/repo/pull/7#discussion_r42", pageOpts())
		c := res.Content
		matchRE(t, `(?s)reviewer.*\[anchored\]`, c)
		matchRE(t, `outside review page`, c)
		matchRE(t, `commits unavailable`, c)
		matchRE(t, `Review verdicts\nUnavailable in this GitHub fetch path`, c)
		matchRE(t, `50 files shown from at least 75`, c)
		matchRE(t, `Commits\nUnavailable in this GitHub fetch path`, c)
		matchRE(t, `review threads: at least 12`, c)
		matchRE(t, `review thread comments shown from at least 12`, c)
		matchRE(t, `Linked references\nUnavailable in this GitHub fetch path`, c)
	})

	tw(t, F, "REST fallback does not render positive changedFiles as none when files are missing", func(t *testing.T) {
		w := prWorld(t)
		w.shim(t, "gh", `exit 1`)
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasSuffix(c.URL, "/pulls/7"):
				return reply(200, `{"number":7,"title":"PR","state":"open","user":{"login":"owner"},"body":"body","draft":false,"base":{"ref":"main"},"head":{"ref":"feature","repo":{"owner":{"login":"owner"}}},"additions":1,"deletions":0,"changed_files":3}`)
			case strings.HasSuffix(c.URL, "/issues/7/comments?per_page=50"), strings.HasSuffix(c.URL, "/pulls/7/files?per_page=50"), strings.HasSuffix(c.URL, "/pulls/7/comments?per_page=50"):
				return reply(200, `[]`)
			}
			return netReply{Err: fmt.Errorf("unexpected URL %s", c.URL)}
		})
		res := ExtractContent(bg(), "https://github.com/owner/repo/pull/7", pageOpts())
		matchRE(t, `0 files shown from at least 3`, res.Content)
		noMatchRE(t, `Files\nnone`, res.Content)
	})
}
