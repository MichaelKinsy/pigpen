package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

func installFakeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake CLI scripts require Unix")
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return p
}

func setupFakeEnv(t *testing.T, ghScript, glabScript string) (binDir, logFile, cacheFile string) {
	t.Helper()
	ResetForTests()
	prevWT := worktreeBaseDirOverride
	tmp := t.TempDir()
	worktreeBaseDirOverride = filepath.Join(tmp, "worktrees")
	t.Cleanup(func() {
		worktreeBaseDirOverride = prevWT
		ResetForTests()
	})

	binDir = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile = filepath.Join(tmp, "cli.log")
	cacheFile = filepath.Join(tmp, "github-cache.json")

	if ghScript != "" {
		installFakeScript(t, binDir, "gh", fmt.Sprintf("printf '%%s\\n' \"$*\" >> %q\n%s", logFile, ghScript))
	}
	if glabScript != "" {
		installFakeScript(t, binDir, "glab", fmt.Sprintf("printf '%%s\\n' \"$*\" >> %q\n%s", logFile, glabScript))
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(CacheEnvVar, cacheFile)
	ghCfgDir := filepath.Join(tmp, "gh-config")
	if err := os.MkdirAll(ghCfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_CONFIG_DIR", ghCfgDir)
	t.Setenv("GH_TOKEN", "test-gh-token")
	t.Setenv("GH_HOST", "")
	t.Setenv("GITLAB_TOKEN", "test-gl-token")
	t.Setenv("GITLAB_HOST", "gitlab.example.com")
	t.Setenv("GLAB_CONFIG_DIR", tmp)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return binDir, logFile, cacheFile
}

func readLogLines(t *testing.T, logFile string) []string {
	t.Helper()
	b, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read log: %v", err)
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestGithubSchemaAndDescription(t *testing.T) {
	schema := githubSchema()
	props := schema["properties"].(map[string]any)
	opSpec := props["op"].(map[string]any)
	wantOps := []string{
		"repo_view", "file_read", "pr_create", "pr_checkout", "pr_push",
		"search_issues", "search_prs", "search_code", "search_commits", "search_repos", "run_watch",
	}
	gotOps := opSpec["enum"].([]string)
	if strings.Join(gotOps, ",") != strings.Join(wantOps, ",") {
		t.Fatalf("op enum = %v, want %v", gotOps, wantOps)
	}
	if !strings.Contains(githubPrompt, "`gh` op wrapper") {
		t.Fatalf("unexpected embedded github prompt")
	}
}

func TestGithubErrorFormatting(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "preserves structured API validation details hidden by generic stderr"
	// "formats structured API field errors that carry no message"
	// "does not duplicate structured details already rendered by gh"
	// "retains the existing error when stdout is not structured JSON"
	// "preserves translated authentication failures without extra API details"
	msg := formatGhJSONFailure(
		[]string{"api", "/repos/a/b"},
		`{"message":"Validation Failed","errors":[{"message":"Head invalid"}]}`,
		"gh: HTTP 422",
		defaultGhOptions(),
	)
	if !strings.Contains(msg, "GitHub details:\n- Validation Failed\n- Head invalid") {
		t.Fatalf("unexpected structured error: %q", msg)
	}

	fieldMsg := formatGhJSONFailure(
		[]string{"api", "/repos/a/b"},
		`{"errors":[{"resource":"PullRequest","field":"head","code":"invalid"}]}`,
		"gh: HTTP 422",
		defaultGhOptions(),
	)
	if !strings.Contains(fieldMsg, "- PullRequest.head: invalid") {
		t.Fatalf("unexpected field error: %q", fieldMsg)
	}

	dupMsg := formatGhJSONFailure(
		[]string{"api", "/repos/a/b"},
		`{"message":"Validation Failed"}`,
		"gh: Validation Failed",
		defaultGhOptions(),
	)
	if strings.Contains(dupMsg, "GitHub details:") {
		t.Fatalf("should not duplicate message already in stderr: %q", dupMsg)
	}

	plainMsg := formatGhJSONFailure([]string{"api"}, "not json", "boom", defaultGhOptions())
	if plainMsg != "boom" {
		t.Fatalf("got %q, want boom", plainMsg)
	}

	authMsg := formatGhJSONFailure([]string{"repo", "view"}, `{"message":"Bad creds"}`, "To get started with GitHub CLI, please run: gh auth login", defaultGhOptions())
	if authMsg != "GitHub CLI is not authenticated. Run `gh auth login`." {
		t.Fatalf("unexpected auth failure: %q", authMsg)
	}
}

func TestGithubLocalePinning(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "pins gh messages while preserving UTF-8 character locale"
	setupFakeEnv(t, `
if [ "${LC_MESSAGES-}" != "C" ]; then
	echo "LC_MESSAGES was ${LC_MESSAGES-<unset>}" >&2
	exit 41
fi
if [ "${LC_CTYPE-}" != "C.UTF-8" ]; then
	echo "LC_CTYPE was ${LC_CTYPE-<unset>}" >&2
	exit 42
fi
if [ "${LC_ALL+x}" = "x" ]; then
	echo "LC_ALL leaked: ${LC_ALL}" >&2
	exit 43
fi
echo ok
`, "")
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")

	res, err := ghRun(context.Background(), t.TempDir(), []string{"--version"}, defaultGhOptions())
	if err != nil {
		t.Fatalf("ghRun error: %v", err)
	}
	if res.exitCode != 0 || res.stdout != "ok" || res.stderr != "" {
		t.Fatalf("unexpected locale check result: %+v", res)
	}
}

func TestForgeStdoutCaptureCap(t *testing.T) {
	// upstream: packages/coding-agent/test/forge-cli.test.ts:
	// "reports truncated stdout even when the forge command succeeds"
	// "keeps an exact-cap stdout capture complete"
	// "does not mistake literal marker text for a truncated capture"
	bufOver := &limitedBuffer{limit: 16}
	_, _ = bufOver.Write([]byte("0123456789abcdefEXTRA"))
	text, trunc := bufOver.text("gh")
	if !trunc || strings.Contains(text, "EXTRA") || !strings.Contains(text, "[gh subprocess output truncated after 8 MiB]") {
		t.Fatalf("unexpected over-cap capture: trunc=%v text=%q", trunc, text)
	}

	bufExact := &limitedBuffer{limit: 16}
	_, _ = bufExact.Write([]byte("0123456789abcdef"))
	text, trunc = bufExact.text("gh")
	if trunc || text != "0123456789abcdef" {
		t.Fatalf("unexpected exact-cap capture: trunc=%v text=%q", trunc, text)
	}

	marker := "[gh subprocess output truncated after 8 MiB]"
	bufLit := &limitedBuffer{limit: 1024}
	_, _ = bufLit.Write([]byte(marker))
	text, trunc = bufLit.text("gh")
	if trunc || text != marker {
		t.Fatalf("literal marker misclassified as truncated: trunc=%v text=%q", trunc, text)
	}
}

func TestGithubRepoViewAndFileReadCases(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "formats repository metadata into readable text"
	// "reads repository text through GitHub's JSON contents API"
	// "adds repository, revision, and path context to file-read failures"
	// "returns GitHub images as model image content"
	// "identifies files GitHub returns without inline bytes"
	// "file_read passes the host as a flag and keeps it out of the API path"
	// "file_read links a bare repo to the host its request went to"
	textB64 := base64.StdEncoding.EncodeToString([]byte("package main\n\nfunc main() {}\n"))
	_, logFile, _ := setupFakeEnv(t, fmt.Sprintf(`
case "$*" in
  *"repo view"*)
    cat <<'JSON'
{"nameWithOwner":"acme/demo","description":"Demo repo","url":"https://github.com/acme/demo","defaultBranchRef":{"name":"main"},"stargazerCount":42,"forkCount":5,"visibility":"PUBLIC"}
JSON
    ;;
  *"contents/main.go"*)
    cat <<'JSON'
{"type":"file","encoding":"base64","size":28,"content":"%s","html_url":"https://github.com/acme/demo/blob/main/main.go"}
JSON
    ;;
  *"contents/empty.txt"*)
    echo '{"type":"file","encoding":"base64","size":0,"content":"","html_url":"https://github.com/acme/demo/blob/main/empty.txt"}'
    ;;
  *"contents/dir"*)
    echo '[{"type":"file","name":"a.go"}]'
    ;;
  *"contents/huge.bin"*)
    echo '{"type":"file","encoding":"none","size":2097152,"html_url":"https://github.com/acme/demo/blob/main/huge.bin"}'
    ;;
  *"contents/pixel.png"*)
    cat <<'JSON'
{"type":"file","encoding":"base64","size":68,"content":"%s","html_url":"https://github.com/acme/demo/blob/main/pixel.png"}
JSON
    ;;
  *"contents/missing.txt"*)
    echo "gh: Not Found (HTTP 404)" >&2
    exit 1
    ;;
  *"contents/enterprise.go"*)
    echo '{"type":"file","encoding":"base64","size":2,"content":"aGk=","html_url":""}'
    ;;
esac
`, textB64, tinyPNGBase64), "")

	ctx := context.Background()
	cwd := t.TempDir()

	// repo_view metadata + Details.Meta.Source
	res, err := executeRepoView(ctx, cwd, map[string]any{"repo": "acme/demo"})
	if err != nil {
		t.Fatalf("repo_view: %v", err)
	}
	if !strings.Contains(res.Content, "# acme/demo") || !strings.Contains(res.Content, "Stars: 42") {
		t.Fatalf("unexpected repo_view:\n%s", res.Content)
	}
	dJSON, _ := json.Marshal(res.Details)
	if !strings.Contains(string(dJSON), `"source":{"type":"url","value":"https://github.com/acme/demo"}`) {
		t.Fatalf("expected details.meta.source, got %s", string(dJSON))
	}

	// Regular text file
	fres, err := executeFileRead(ctx, cwd, map[string]any{"repo": "acme/demo", "path": "main.go", "branch": "main"})
	if err != nil || !strings.Contains(fres.Content, "func main() {}") {
		t.Fatalf("file_read text: err=%v content=%q", err, fres.Content)
	}

	// 0-byte empty file must return empty text, NOT "did not return file bytes"
	emptyRes, err := executeFileRead(ctx, cwd, map[string]any{"repo": "acme/demo", "path": "empty.txt"})
	if err != nil || emptyRes.Content != "" {
		t.Fatalf("empty file_read: err=%v content=%q", err, emptyRes.Content)
	}

	// Directory response (JSON array) must return "is not a file."
	if _, err := executeFileRead(ctx, cwd, map[string]any{"repo": "acme/demo", "path": "dir"}); err == nil || !strings.Contains(err.Error(), "GitHub path 'dir' is not a file.") {
		t.Fatalf("expected directory error, got %v", err)
	}

	// File without inline bytes uses omp's formatBytes ("2.0MB")
	hugeRes, err := executeFileRead(ctx, cwd, map[string]any{"repo": "acme/demo", "path": "huge.bin"})
	if err != nil || !strings.Contains(hugeRes.Content, "(2.0MB)") {
		t.Fatalf("no-bytes file_read: err=%v content=%q", err, hugeRes.Content)
	}

	// PNG image includes Dimensions: 1x1 and Images payload
	imgRes, err := executeFileRead(ctx, cwd, map[string]any{"repo": "acme/demo", "path": "pixel.png"})
	if err != nil || len(imgRes.Images) != 1 || imgRes.Images[0].MimeType != "image/png" || !strings.Contains(imgRes.Content, "Dimensions: 1x1") {
		t.Fatalf("image file_read: err=%v content=%q images=%d", err, imgRes.Content, len(imgRes.Images))
	}

	// Failure includes repo@revision:path context
	if _, err := executeFileRead(ctx, cwd, map[string]any{"repo": "acme/demo", "path": "missing.txt", "branch": "dev"}); err == nil || !strings.Contains(err.Error(), "acme/demo@dev:missing.txt") {
		t.Fatalf("expected contextualized failure, got %v", err)
	}

	// Enterprise host passed via --hostname and kept out of API path (when host is trusted via GH_HOST or hosts.yml)
	if err := os.WriteFile(filepath.Join(os.Getenv("GH_CONFIG_DIR"), "hosts.yml"), []byte("ghe.example.com:\n    user: octocat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entRes, err := executeFileRead(ctx, cwd, map[string]any{"repo": "ghe.example.com/acme/demo", "path": "enterprise.go"})
	if err != nil || entRes.Content != "hi" {
		t.Fatalf("enterprise file_read: err=%v content=%q", err, entRes.Content)
	}
	logs := strings.Join(readLogLines(t, logFile), "\n")
	if !strings.Contains(logs, "--hostname ghe.example.com /repos/acme/demo/contents/enterprise.go") {
		t.Fatalf("expected --hostname ghe.example.com in gh argv, got:\n%s", logs)
	}
	entDetails := entRes.Details.(GhToolDetails)
	if entDetails.Meta == nil || entDetails.Meta.Source == nil || !strings.HasPrefix(entDetails.Meta.Source.Value, "https://ghe.example.com/acme/demo/blob/") {
		t.Fatalf("expected enterprise fallback sourceUrl, got %+v", entDetails.Meta)
	}
}

func TestGithubSearchOpsAndQualifiers(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "parseSearchDateBound: relative duration walks back from `now` and returns YYYY-MM-DD"
	// "buildSearchDateQualifier: emits >=, <=, or range depending on which bounds are set"
	// "calls /search/issues via gh api with the full query verbatim (including leading-dash terms)"
	// "search_repos: maps dateField=updated to the `pushed:` qualifier"
	// "search_commits: forces `committer-date` regardless of `dateField`"
	// "search_code: rejects validated non-empty since and until values"
	// "search scopes the query to a bare slug and the request to the host"
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	q, err := BuildSearchDateQualifier("created", "3d", "2026-06-15", now)
	if err != nil || q != "created:2026-06-12..2026-06-15" {
		t.Fatalf("BuildSearchDateQualifier: err=%v q=%q", err, q)
	}

	_, logFile, _ := setupFakeEnv(t, `
case "$*" in
  *"/search/issues"*)
    cat <<'JSON'
{"total_count":1,"items":[{"number":7,"title":"Fix race","state":"closed","pull_request":{"merged_at":"2026-06-01T00:00:00Z"},"user":{"login":"alice"},"repository_url":"https://api.github.com/repos/acme/demo","html_url":"https://github.com/acme/demo/pull/7"}]}
JSON
    ;;
  *"/search/code"*)
    cat <<'JSON'
{"total_count":1,"items":[{"path":"src/app.go","sha":"abcdef1234567890","repository":{"full_name":"acme/demo"},"html_url":"https://github.com/acme/demo/blob/main/src/app.go","text_matches":[{"fragment":"func Run()"}]}]}
JSON
    ;;
  *"/search/commits"*)
    cat <<'JSON'
{"total_count":1,"items":[{"sha":"1234567890abcdef","commit":{"message":"fix: handle nil\n\ndetails","author":{"name":"Alice","date":"2026-06-10T00:00:00Z"}},"repository":{"full_name":"acme/demo"},"html_url":"https://github.com/acme/demo/commit/1234567890abcdef"}]}
JSON
    ;;
  *"/search/repositories"*)
    cat <<'JSON'
{"total_count":1,"items":[{"full_name":"acme/demo","description":"Fast tool","language":"Go","stargazers_count":99,"html_url":"https://github.com/acme/demo"}]}
JSON
    ;;
esac
`, "")

	ctx := context.Background()
	cwd := t.TempDir()

	// Leading-dash term + enterprise host scoping (trusted via hosts.yml)
	if err := os.WriteFile(filepath.Join(os.Getenv("GH_CONFIG_DIR"), "hosts.yml"), []byte("ghe.corp:\n    user: octocat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prs, err := executeSearchIssuesOrPrs(ctx, cwd, map[string]any{"repo": "ghe.corp/acme/demo", "query": "-label:wontfix race", "limit": float64(5)}, true)
	if err != nil || !strings.Contains(prs.Content, "State: merged") || prs.Details != nil {
		t.Fatalf("search_prs failed: err=%v details=%v content=%s", err, prs.Details, prs.Content)
	}

	if _, err := executeSearchCode(ctx, cwd, map[string]any{"query": "Run", "since": "3d"}); err == nil {
		t.Fatalf("expected search_code to reject since")
	}
	codeRes, err := executeSearchCode(ctx, cwd, map[string]any{"repo": "acme/demo", "query": "Run"})
	if err != nil || !strings.Contains(codeRes.Content, "src/app.go") {
		t.Fatalf("search_code failed: err=%v", err)
	}

	commitRes, err := executeSearchCommits(ctx, cwd, map[string]any{"repo": "acme/demo", "since": "2026-06-01", "dateField": "updated"})
	if err != nil || !strings.Contains(commitRes.Content, "1234567890ab fix: handle nil") {
		t.Fatalf("search_commits failed: err=%v", err)
	}

	repoRes, err := executeSearchRepos(ctx, cwd, map[string]any{"query": "org:acme", "since": "2026-06-01", "dateField": "updated"})
	if err != nil || !strings.Contains(repoRes.Content, "- acme/demo") {
		t.Fatalf("search_repos failed: err=%v", err)
	}

	logs := strings.Join(readLogLines(t, logFile), "\n")
	if !strings.Contains(logs, "--hostname ghe.corp -X GET /search/issues -f q=-label:wontfix race repo:acme/demo is:pr") {
		t.Fatalf("expected verbatim leading-dash query & enterprise hostname in argv:\n%s", logs)
	}
	if !strings.Contains(logs, "committer-date:>=2026-06-01") {
		t.Fatalf("expected committer-date qualifier in search_commits argv:\n%s", logs)
	}
	if !strings.Contains(logs, "pushed:>=2026-06-01") {
		t.Fatalf("expected pushed: qualifier in search_repos argv:\n%s", logs)
	}
}

func TestGithubPrCheckoutCrossRepoBatchPushAndNoFollowTags(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "checks out a pull request into a worktree and configures contributor push metadata"
	// "checks out multiple pull requests in a single call when pr is an array"
	// "rejects PR pushes from branches without checkout metadata"
	// "pushes the branch pr_checkout made for the named PR, not the current branch"
	// Plus gate.Check enforcement, resolveAvailableWorktreePath, and --no-follow-tags safety.
	tmp := t.TempDir()
	originBare := filepath.Join(tmp, "origin.git")
	forkBare := filepath.Join(tmp, "fork.git")
	localDir := filepath.Join(tmp, "local")

	mustGit(t, tmp, "init", "--bare", originBare)
	mustGit(t, tmp, "clone", originBare, localDir)
	mustGit(t, localDir, "config", "user.email", "test@example.com")
	mustGit(t, localDir, "config", "user.name", "Test")
	mustGit(t, localDir, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(localDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, localDir, "add", "README.md")
	mustGit(t, localDir, "commit", "-m", "initial")
	mustGit(t, localDir, "push", "-u", "origin", "main")

	// Set up fork bare repo for cross-repo PR #12 and same-repo PR #13 on origin.
	mustGit(t, tmp, "clone", "--bare", originBare, forkBare)

	mustGit(t, localDir, "checkout", "-b", "fork-feat")
	if err := os.WriteFile(filepath.Join(localDir, "fork.txt"), []byte("fork v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, localDir, "add", "fork.txt")
	mustGit(t, localDir, "commit", "-m", "fork v1")
	oid12 := strings.TrimSpace(mustGit(t, localDir, "rev-parse", "HEAD"))
	mustGit(t, localDir, "push", forkBare, "fork-feat:refs/heads/fork-feat")

	mustGit(t, localDir, "checkout", "main")
	mustGit(t, localDir, "checkout", "-b", "origin-feat")
	if err := os.WriteFile(filepath.Join(localDir, "orig.txt"), []byte("orig v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, localDir, "add", "orig.txt")
	mustGit(t, localDir, "commit", "-m", "orig v1")
	oid13 := strings.TrimSpace(mustGit(t, localDir, "rev-parse", "HEAD"))
	mustGit(t, localDir, "push", "origin", "origin-feat")
	mustGit(t, localDir, "checkout", "main")
	mustGit(t, localDir, "branch", "-D", "fork-feat", "origin-feat")

	setupFakeEnv(t, fmt.Sprintf(`
case "$*" in
  *"repo view"*contrib/demo*)
    printf '{"nameWithOwner":"contrib/demo","sshUrl":"%%s","url":"%%s"}\n' %q %q
    ;;
  *"pr view 12"*)
    cat <<'JSON'
{"number":12,"title":"Fork PR","state":"OPEN","baseRefName":"main","headRefName":"fork-feat","headRefOid":"%s","headRepository":{"nameWithOwner":"contrib/demo"},"headRepositoryOwner":{"login":"contrib"},"isCrossRepository":true,"maintainerCanModify":true,"url":"https://github.com/acme/demo/pull/12"}
JSON
    ;;
  *"pr view 13"*)
    cat <<'JSON'
{"number":13,"title":"Origin PR","state":"OPEN","baseRefName":"main","headRefName":"origin-feat","headRefOid":"%s","isCrossRepository":false,"maintainerCanModify":true,"url":"https://github.com/acme/demo/pull/13"}
JSON
    ;;
esac
`, forkBare, forkBare, oid12, oid13), "")

	ctx := context.Background()
	var sdkCtx sdk.Context

	// Verify gate.Check blocks mutating ops when a restricting policy is active.
	gate.SetPolicy(func(_ sdk.Context, r gate.Request) error {
		if r.Tool == "github" && r.Op == gate.OpExec {
			return errors.New("blocked by plan mode: " + r.Detail)
		}
		return nil
	})
	t.Cleanup(gate.ResetForTests)
	if _, err := handleGithubTool(sdkCtx, map[string]any{"op": "pr_checkout", "pr": "12"}); err == nil || !strings.Contains(err.Error(), "blocked by plan mode: pr_checkout") {
		t.Fatalf("expected gate.Check to block pr_checkout, got %v", err)
	}
	gate.ResetForTests()

	// Pre-occupy the base worktree path for PR #12 to verify resolveAvailableWorktreePath picks -2.
	repoRoot, _ := requireGitRepoRoot(ctx, localDir)
	primaryRoot, _ := requirePrimaryGitRepoRoot(ctx, repoRoot)
	occupied := filepath.Join(worktreeBaseDirOverride, fmt.Sprintf("12-%s", hashPath(primaryRoot)))
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}

	// Batch checkout of PR #12 (cross-repo) and PR #13 (same-repo).
	batchRes, err := executePrCheckout(ctx, sdkCtx, localDir, map[string]any{"pr": []any{"12", "13"}})
	if err != nil {
		t.Fatalf("batch pr_checkout failed: %v", err)
	}
	d := batchRes.Details.(GhToolDetails)
	if len(d.Checkouts) != 2 {
		t.Fatalf("expected 2 checkouts, got %d; content:\n%s", len(d.Checkouts), batchRes.Content)
	}
	wt12 := d.Checkouts[0].WorktreePath
	if !strings.HasSuffix(wt12, "-2") {
		t.Fatalf("expected disambiguated worktree path ending in -2, got %s", wt12)
	}
	if d.Checkouts[0].Remote != "fork-contrib" {
		t.Fatalf("expected fork-contrib remote, got %s", d.Checkouts[0].Remote)
	}

	// Pushing main (no PR metadata) must fail.
	if _, err := executePrPush(ctx, sdkCtx, localDir, map[string]any{}); err == nil || !strings.Contains(err.Error(), "has no PR push metadata") {
		t.Fatalf("expected missing metadata error, got %v", err)
	}

	// Commit on PR #12 worktree + create an annotated tag + enable push.followTags=true.
	// Push by PR number from localDir (whose current branch is main!) and verify:
	// (a) pr-12 is pushed (not main), (b) --no-follow-tags prevents the tag from being pushed.
	mustGit(t, localDir, "config", "push.followTags", "true")
	if err := os.WriteFile(filepath.Join(wt12, "fork.txt"), []byte("fork v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wt12, "add", "fork.txt")
	mustGit(t, wt12, "commit", "-m", "fork v2")
	mustGit(t, wt12, "tag", "-a", "v-should-not-push", "-m", "local tag")

	pushRes, err := executePrPush(ctx, sdkCtx, localDir, map[string]any{"pr": "12", "forceWithLease": true})
	if err != nil {
		t.Fatalf("pr_push failed: %v", err)
	}
	if !strings.Contains(pushRes.Content, "Pushed pr-12 to fork-contrib:fork-feat.") {
		t.Fatalf("unexpected pr_push output:\n%s", pushRes.Content)
	}
	tagsOnFork := strings.TrimSpace(mustGit(t, forkBare, "tag", "-l"))
	if tagsOnFork != "" {
		t.Fatalf("annotated tag leaked to fork despite --no-follow-tags: %q", tagsOnFork)
	}
}

func TestGithubRunWatchSnapshotsStepsAndRepoSafety(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "streams normalized GitHub Actions step progress in run watch details"
	// "fails fast when explicit `repo` differs from the cwd repo and no `branch`/`run` selector is given (issue #1949)"
	// "accepts case-only differences between explicit `repo` and a run URL repo (PR #1951)"
	prevGrace := runWatchGraceDefault
	prevInterval := runWatchIntervalDefault
	runWatchGraceDefault = 0
	runWatchIntervalDefault = time.Millisecond
	t.Cleanup(func() {
		runWatchGraceDefault = prevGrace
		runWatchIntervalDefault = prevInterval
	})

	setupFakeEnv(t, `
case "$*" in
  *"repo view --json url"*)
    echo "https://github.com/acme/demo"
    ;;
  *"/actions/runs/500/jobs"*)
    cat <<'JSON'
{"total_count":1,"jobs":[{"id":9001,"name":"test","status":"completed","conclusion":"failure","steps":[{"number":2,"name":"go test","status":"completed","conclusion":"failure","started_at":"2026-06-01T00:00:00Z","completed_at":"2026-06-01T00:00:05Z"}]}]}
JSON
    ;;
  *"/actions/runs/500"*)
    cat <<'JSON'
{"id":500,"name":"CI","display_title":"Run tests","status":"completed","conclusion":"failure","head_branch":"main","head_sha":"abcdef1234567890","html_url":"https://github.com/acme/demo/actions/runs/500"}
JSON
    ;;
  *"/actions/jobs/9001/logs"*)
    printf "\033[31mFAIL: TestBoom\033[0m\n"
    ;;
esac
`, "")

	ctx := context.Background()
	var sdkCtx sdk.Context
	cwd := t.TempDir()

	// Issue #1949: refuse watching commit HEAD when explicit repo != cwd repo.
	if _, err := executeRunWatch(ctx, sdkCtx, cwd, map[string]any{"repo": "other/repo"}); err == nil || !strings.Contains(err.Error(), "Cannot infer the watched commit for other/repo") {
		t.Fatalf("expected repo mismatch error, got %v", err)
	}

	// PR #1951: case-only differences between explicit repo and run URL repo are accepted.
	failRes, err := executeRunWatch(ctx, sdkCtx, cwd, map[string]any{
		"repo": "ACME/DEMO",
		"run":  "https://github.com/acme/demo/actions/runs/500",
		"tail": float64(5),
	})
	if err != nil {
		t.Fatalf("run_watch failed: %v", err)
	}
	// ANSI escapes stripped by normalizeBlock -> sanitizeText.
	if strings.Contains(failRes.Content, "\x1b[") || !strings.Contains(failRes.Content, "FAIL: TestBoom") {
		t.Fatalf("expected sanitized failed log output:\n%s", failRes.Content)
	}
	d := failRes.Details.(GhToolDetails)
	if d.Watch == nil || d.Watch.PollCount != nil || d.Watch.Run == nil || len(d.Watch.Run.Jobs) != 1 || d.Watch.Run.Jobs[0].Steps == nil || len(*d.Watch.Run.Jobs[0].Steps) != 1 {
		t.Fatalf("unexpected watch details shape: %+v", d.Watch)
	}
	if d.Meta == nil || d.Meta.Source == nil || d.Meta.Source.Value != "https://github.com/acme/demo/actions/runs/500" {
		t.Fatalf("expected meta.source on run_watch result, got %+v", d.Meta)
	}

	// Watching snapshot formatter produces "# Watching GitHub Actions Run #" and Note:.
	snapText := formatRunWatchSnapshot("acme/demo", ghRunSnapshot{ID: 500, Status: "in_progress"}, 2, "Waiting 5s")
	if !strings.Contains(snapText, "# Watching GitHub Actions Run #500") || !strings.Contains(snapText, "Poll: 2") || !strings.Contains(snapText, "Note: Waiting 5s") {
		t.Fatalf("unexpected watching snapshot:\n%s", snapText)
	}
}

func TestGithubPrDiff406FallbackAndParser(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/gh.test.ts:
	// "parses quoted diff headers instead of falling back to unknown paths"
	// "reassembles a unified diff from the per-file API when gh pr diff returns HTTP 406"
	// "keeps files with omitted patches visible instead of dropping them"
	// "rejects instead of silently reviewing a PR beyond the files API cap"
	quotedDiff := "diff --git \"a/dir with space/f.go\" \"b/dir with space/f.go\"\n--- \"a/dir with space/f.go\"\n+++ \"b/dir with space/f.go\"\n@@ -1 +1 @@\n-a\n+b\n"
	parsed := ParsePrUnifiedDiff(quotedDiff)
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "dir with space/f.go" {
		t.Fatalf("quoted header parse failed: %+v", parsed.Files)
	}

	setupFakeEnv(t, `
case "$*" in
  *"pr diff 77"*)
    echo "gh: HTTP 406: Sorry, the diff exceeded the maximum number of lines (20000)" >&2
    exit 1
    ;;
  *"/repos/acme/demo/pulls/77/files"*)
    cat <<'JSON'
[{"filename":"a.go","status":"modified","additions":1,"deletions":1,"patch":"@@ -1 +1 @@\n-old\n+new"},{"filename":"big.bin","status":"added","additions":0,"deletions":0}]
JSON
    ;;
  *"/repos/acme/demo/pulls/77"*)
    echo '{"changed_files":2}'
    ;;
  *"pr diff 78"*)
    echo "gh: HTTP 406: too_large" >&2
    exit 1
    ;;
  *"/repos/acme/demo/pulls/78"*)
    echo '{"changed_files":3001}'
    ;;
esac
`, "")

	ctx := context.Background()
	var sdkCtx sdk.Context
	res, err := getOrFetchPrDiff(ctx, sdkCtx, t.TempDir(), "acme/demo", 77)
	if err != nil {
		t.Fatalf("406 fallback failed: %v", err)
	}
	if len(res.Payload.Files) != 2 || res.Payload.Files[0].Path != "a.go" || res.Payload.Files[1].Path != "big.bin" {
		t.Fatalf("unexpected fallback files: %+v", res.Payload.Files)
	}
	if !strings.Contains(res.Payload.Unified, "patch unavailable") {
		t.Fatalf("expected omitted-patch marker in synthetic diff")
	}

	if _, err := getOrFetchPrDiff(ctx, sdkCtx, t.TempDir(), "acme/demo", 78); err == nil || !strings.Contains(err.Error(), "3000-file limit") {
		t.Fatalf("expected files API cap error, got %v", err)
	}
}

func TestGithubCacheSemantics(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/github-cache.test.ts:
	// "keys rows by GitHub auth identity"
	// "revalidates an open PR within the soft TTL but serves a merged one from cache"
	// "bypasses the cache entirely when github.cache.enabled = false"
	// "propagates aborts during a soft-expired synchronous refresh"
	// "does not let a refresh started before an invalidation overwrite the post-invalidation row"
	// "counts pinned retention from when the generation was retained, not fetched"
	setupFakeEnv(t, "", "")
	var sdkCtx sdk.Context

	fakeNow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return fakeNow }

	// 1. Auth identity isolation
	t.Setenv("GH_TOKEN", "token-alice")
	authA := resolveGithubCacheAuthKey("")
	t.Setenv("GH_TOKEN", "token-bob")
	authB := resolveGithubCacheAuthKey("")
	if authA == "" || authB == "" || authA == authB {
		t.Fatalf("expected distinct non-empty auth keys, got %q vs %q", authA, authB)
	}

	// 2. Open PR revalidates after 15s (OPEN_STATE_SOFT_TTL_MS), merged PR stays cached up to 300s
	calls := 0
	_, err := getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindPr, number: 1, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) {
			calls++
			return FreshView[ghPrViewData]{Rendered: "open", Payload: ghPrViewData{ghIssueViewData: ghIssueViewData{State: "OPEN"}}}, nil
		},
	})
	if err != nil || calls != 1 {
		t.Fatalf("initial open PR fetch: err=%v calls=%d", err, calls)
	}
	fakeNow = fakeNow.Add(20 * time.Second)
	res, err := getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindPr, number: 1, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) {
			calls++
			return FreshView[ghPrViewData]{Rendered: "merged", Payload: ghPrViewData{ghIssueViewData: ghIssueViewData{State: "MERGED"}}}, nil
		},
	})
	if err != nil || calls != 2 || res.Status != CacheStatusRefreshed {
		t.Fatalf("expected open PR to revalidate at 20s: err=%v calls=%d status=%s", err, calls, res.Status)
	}
	fakeNow = fakeNow.Add(60 * time.Second)
	res, err = getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindPr, number: 1, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) {
			calls++
			return FreshView[ghPrViewData]{}, errors.New("should not fetch")
		},
	})
	if err != nil || calls != 2 || res.Status != CacheStatusFresh {
		t.Fatalf("expected merged PR fresh from cache at 60s: err=%v calls=%d status=%s", err, calls, res.Status)
	}

	// 3. Propagates context.Canceled during soft-expired synchronous refresh
	fakeNow = fakeNow.Add(400 * time.Second)
	if _, err := getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindPr, number: 1, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) { return FreshView[ghPrViewData]{}, context.Canceled },
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled propagated, got %v", err)
	}

	// 4. Invalidation fencing: in-flight fetch started before invalidate must not store
	_, err = getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindIssue, number: 99, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) {
			InvalidateAllForNumber(sdkCtx, 99, "acme/demo")
			return FreshView[ghPrViewData]{Rendered: "stale-pre-inv"}, nil
		},
	})
	if err != nil {
		t.Fatalf("invalidation fence fetch: %v", err)
	}
	afterCalls := 0
	_, err = getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindIssue, number: 99, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) {
			afterCalls++
			return FreshView[ghPrViewData]{Rendered: "fresh-post-inv"}, nil
		},
	})
	if err != nil || afterCalls != 1 {
		t.Fatalf("expected fenced pre-invalidation row not cached, afterCalls=%d", afterCalls)
	}

	// 5. Cache kill switch bypasses cache
	prevFn := CacheSettingsFn
	CacheSettingsFn = func(_ sdk.Context) CacheTTLSettings {
		return CacheTTLSettings{Enabled: false, SoftTTL: 5 * time.Minute, HardTTL: 7 * 24 * time.Hour}
	}
	t.Cleanup(func() { CacheSettingsFn = prevFn })
	disabledRes, err := getOrFetchView(cacheLookupOptions[ghPrViewData]{
		sdkCtx: sdkCtx, repo: "acme/demo", kind: CacheKindPr, number: 1, includeComments: true, authKey: authA,
		fetchFresh: func() (FreshView[ghPrViewData], error) { return FreshView[ghPrViewData]{Rendered: "live"}, nil },
	})
	if err != nil || disabledRes.Status != CacheStatusDisabled {
		t.Fatalf("expected CacheStatusDisabled, got %+v err=%v", disabledRes.Status, err)
	}
	CacheSettingsFn = prevFn

	// 6. Pinned retention measured from retainedAt, not fetchedAt
	oldFetchedAt := fakeNow.Add(-10 * 24 * time.Hour).UnixMilli()
	genID, ok := RetainPrDiffGeneration(sdkCtx, "acme/demo", 55, oldFetchedAt, "diff --git a/x b/x\n", nil)
	if !ok {
		t.Fatalf("RetainPrDiffGeneration failed")
	}
	if _, ok := GetPinnedPrDiff(sdkCtx, "acme/demo", 55, genID); !ok {
		t.Fatalf("expected newly retained generation to survive even with old fetchedAt")
	}
	fakeNow = fakeNow.Add(8 * 24 * time.Hour)
	if _, ok := GetPinnedPrDiff(sdkCtx, "acme/demo", 55, genID); ok {
		t.Fatalf("expected pinned generation past hard TTL from retainedAt to expire")
	}
}

func TestIssueAndPrProtocolHandlers(t *testing.T) {
	// upstream: packages/coding-agent/test/internal-urls/issue-pr-protocol.test.ts:
	// "resolves issue://owner/repo/<n> through the shared cache"
	// "retries issue://owner/repo/<n> without stateReason when gh does not support it"
	// "rejects empty / dot / dotdot path segments"
	// "routes pr://<host>/<owner>/<repo>/<n> at that host"
	// "keeps github.com rows separate from the host GH_HOST names"
	// "pr://owner/repo/<n>/diff lists files with per-file hint URLs"
	// "pr://owner/repo/<n>/diff/<i> slices the i-th file (1-indexed) as text/plain"
	// "pr://owner/repo/<n>/diff/all returns the verbatim unified diff as text/plain"
	// "serves ?at= from the pinned generation after the live row changes"
	// "invalid state errors instead of silently falling back to 'open'"
	// "treats `diff` as a repository name in repo-scoped listing URLs"
	_, logFile, _ := setupFakeEnv(t, `
case "$1 $2" in
  "repo view")
    echo "https://github.com/acme/demo"
    ;;
  "issue view")
    case "$*" in
      *" 99 "*"stateReason"*)
        echo 'Unknown JSON field: "stateReason"' >&2
        exit 1
        ;;
    esac
    cat <<'JSON'
{"number":42,"title":"Crash on startup","state":"OPEN","author":{"login":"octocat"},"body":"Steps to reproduce","createdAt":"2026-04-01T09:00:00Z","updatedAt":"2026-04-01T10:00:00Z","url":"https://github.com/acme/demo/issues/42","labels":[{"name":"bug"}],"comments":[{"author":{"login":"dev"},"body":"Looking into it","createdAt":"2026-04-01T11:00:00Z","isMinimized":false}]}
JSON
    ;;
  "issue list")
    cat <<'JSON'
[{"number":42,"title":"Crash on startup","state":"OPEN","author":{"login":"octocat"},"labels":[{"name":"bug"}],"updatedAt":"2026-04-01T10:00:00Z"}]
JSON
    ;;
  "pr view")
    cat <<'JSON'
{"number":7,"title":"Fix crash","state":"OPEN","isDraft":false,"baseRefName":"main","headRefName":"fix/crash","author":{"login":"dev"},"body":"Fixes #42","url":"https://github.com/acme/demo/pull/7","files":[{"path":"main.go","additions":2,"deletions":1,"changeType":"MODIFIED"}],"reviews":[],"comments":[]}
JSON
    ;;
  "pr diff")
    cat <<'DIFF'
diff --git a/a.go b/a.go
--- a/a.go
+++ a/a.go
@@ -1 +1 @@
-old
+new
diff --git a/b.go b/b.go
new file mode 100644
--- /dev/null
+++ b/b.go
@@ -0,0 +1 @@
+added
DIFF
    ;;
  "api"*)
    echo "[]"
    ;;
esac
`, "")

	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	_ = urls.Register(issueHandler{})
	_ = urls.Register(prHandler{})

	uctx := urls.Context{Cwd: t.TempDir()}

	// Single issue + cache hit
	res1, err := urls.Resolve(uctx, "issue://acme/demo/42")
	if err != nil || !strings.Contains(res1.Content, "# Issue #42: Crash on startup") || res1.Notes[0] != "Fetched live" {
		t.Fatalf("issue resolve 1: err=%v notes=%v", err, res1.Notes)
	}
	callsAfterFirst := len(readLogLines(t, logFile))
	res2, err := urls.Resolve(uctx, "issue://acme/demo/42")
	if err != nil || res2.Content != res1.Content || !strings.HasPrefix(res2.Notes[0], "Cached:") || len(readLogLines(t, logFile)) != callsAfterFirst {
		t.Fatalf("expected cache hit without extra gh call")
	}

	// stateReason fallback on older gh
	if _, err := urls.Resolve(uctx, "issue://acme/demo/99"); err != nil {
		t.Fatalf("expected stateReason fallback to succeed, got %v", err)
	}

	// Empty / dot / dotdot path segments rejected
	for _, bad := range []string{"pr://owner//77", "pr://owner/./77", "pr://owner/../77"} {
		if _, err := urls.Resolve(uctx, bad); err == nil || !strings.Contains(err.Error(), "empty or unsafe path segment") {
			t.Fatalf("expected unsafe path segment error for %s, got %v", bad, err)
		}
	}

	// Invalid state errors instead of falling back to open
	if _, err := urls.Resolve(uctx, "issue://acme/demo?state=bogus"); err == nil || !strings.Contains(err.Error(), "Invalid issue:// list state 'bogus'") {
		t.Fatalf("expected invalid state error, got %v", err)
	}

	// Repo named "diff" is treated as repo-scoped listing
	diffRepoRes, err := urls.Resolve(uctx, "issue://acme/diff")
	if err != nil || !strings.Contains(diffRepoRes.Content, "# Issues in acme/diff") {
		t.Fatalf("expected repo named diff to list issues: err=%v content=%s", err, diffRepoRes.Content)
	}

	// Enterprise host routing (trusted via hosts.yml)
	if err := os.WriteFile(filepath.Join(os.Getenv("GH_CONFIG_DIR"), "hosts.yml"), []byte("ghe.example.com:\n    user: octocat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := urls.Resolve(uctx, "pr://ghe.example.com/acme/demo/7"); err != nil {
		t.Fatalf("enterprise pr:// failed: %v", err)
	}
	if !strings.Contains(strings.Join(readLogLines(t, logFile), "\n"), "--repo ghe.example.com/acme/demo") {
		t.Fatalf("expected --repo ghe.example.com/acme/demo in gh argv")
	}
	// PR diff family + pinned generation
	diffList, err := urls.Resolve(uctx, "pr://acme/demo/7/diff")
	if err != nil || !strings.Contains(diffList.Content, "pr://acme/demo/7/diff/2") {
		t.Fatalf("pr diff list: err=%v", err)
	}
	diffSlice, err := urls.Resolve(uctx, "pr://acme/demo/7/diff/2")
	if err != nil || !strings.Contains(diffSlice.Content, "diff --git a/b.go b/b.go") {
		t.Fatalf("pr diff slice: err=%v", err)
	}
	diffAll, err := urls.Resolve(uctx, "pr://acme/demo/7/diff/all")
	if err != nil || !strings.Contains(diffAll.Content, "a/a.go") || !strings.Contains(diffAll.Content, "b/b.go") {
		t.Fatalf("pr diff all: err=%v", err)
	}
	pinnedDiff := "diff --git a/pinned.go b/pinned.go\n--- a/pinned.go\n+++ b/pinned.go\n@@ -1 +1 @@\n-x\n+y\n"
	genID, ok := RetainPrDiffGeneration(uctx.SDK, "acme/demo", 7, 1700000000000, pinnedDiff, ParsePrUnifiedDiff(pinnedDiff).Files)
	if !ok {
		t.Fatalf("RetainPrDiffGeneration failed")
	}
	pinnedRes, err := urls.Resolve(uctx, fmt.Sprintf("pr://acme/demo/7/diff?at=%d", genID))
	if err != nil || !strings.Contains(pinnedRes.Content, "pinned.go") {
		t.Fatalf("pinned diff resolve: err=%v", err)
	}
}

func TestMrProtocolHandler(t *testing.T) {
	// upstream: packages/coding-agent/test/internal-urls/mr-protocol.test.ts:
	// "lists changed files of a nested-group project with per-file mr:// links"
	// "slices one file, returns the full diff, and rejects an out-of-range index"
	// "serves a repeat read from the cache without re-running glab"
	// "resolves the short form against the project glab reports for the session cwd"
	// "rejects URLs without a diff suffix, a group/project pair, or a plain host"
	// "retries without --raw when the instance has no raw diff endpoint"
	// "fails with an upgrade hint instead of parsing an old glab's header-less diff"
	// "keeps env tokens away from hosts the user has not configured"
	_, logFile, _ := setupFakeEnv(t, "", `
case "$1 $2" in
  "repo view")
    echo '{"web_url":"https://gitlab.example.com/acme/platform/api"}'
    ;;
  "mr diff")
    case "$*" in
      *"--raw"*)
        if [ -f "$GLAB_CONFIG_DIR/old-glab" ]; then
          echo "unknown flag: --raw" >&2
          exit 1
        fi
        if [ -f "$GLAB_CONFIG_DIR/fail-raw" ]; then
          echo "could not obtain raw diff: 404 Not Found" >&2
          exit 1
        fi
        ;;
    esac
    cat <<'DIFF'
diff --git a/src/one.ts b/src/one.ts
--- a/src/one.ts
+++ b/src/one.ts
@@ -1 +1 @@
-one
+uno
diff --git a/src/two.ts b/src/two.ts
--- a/src/two.ts
+++ b/src/two.ts
@@ -1 +1 @@
-two
+dos
DIFF
    ;;
esac
`)

	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	_ = urls.Register(mrHandler{})

	uctx := urls.Context{Cwd: t.TempDir()}

	res, err := urls.Resolve(uctx, "mr://gitlab.example.com/acme/platform/api/7/diff")
	if err != nil || !strings.Contains(res.Content, "# Merge Request Diff: gitlab.example.com/acme/platform/api!7 (2 files)") {
		t.Fatalf("mr diff list: err=%v", err)
	}
	callsAfterFirst := len(readLogLines(t, logFile))
	// Repeat read served from cache without re-running glab.
	res2, err := urls.Resolve(uctx, "mr://gitlab.example.com/acme/platform/api/7/diff/1")
	if err != nil || !strings.HasPrefix(res2.Notes[0], "Cached:") || len(readLogLines(t, logFile)) != callsAfterFirst {
		t.Fatalf("expected cached mr diff read, notes=%v", res2.Notes)
	}

	// Short form resolves default project from cwd.
	shortRes, err := urls.Resolve(uctx, "mr://7/diff/2")
	if err != nil || !strings.Contains(shortRes.Content, "src/two.ts") {
		t.Fatalf("mr short slice: err=%v", err)
	}

	// Invalid shapes rejected.
	for _, bad := range []string{"mr://gitlab.com/g/p/7", "mr://gitlab.com/g/7/diff", "mr://gitlab.com@evil.example/g/p/7/diff"} {
		if _, err := urls.Resolve(uctx, bad); err == nil || !strings.Contains(err.Error(), "Invalid mr:// URL") {
			t.Fatalf("expected invalid mr:// URL for %s, got %v", bad, err)
		}
	}

	// Untrusted host blocked when env token set.
	if _, err := urls.Resolve(uctx, "mr://attacker.example/a/b/1/diff"); err == nil || !strings.Contains(err.Error(), "Refusing to send GitLab token") {
		t.Fatalf("expected untrusted host error, got %v", err)
	}

	// Fallback when --raw returns 404.
	glabCfgDir := os.Getenv("GLAB_CONFIG_DIR")
	if err := os.WriteFile(filepath.Join(glabCfgDir, "fail-raw"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fallbackRes, err := urls.Resolve(uctx, "mr://gitlab.com/g/p/9/diff"); err != nil || !strings.Contains(fallbackRes.Content, "(2 files)") {
		t.Fatalf("mr --raw fallback: err=%v", err)
	}

	// Upgrade hint when glab is too old for --raw.
	if err := os.WriteFile(filepath.Join(glabCfgDir, "old-glab"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := urls.Resolve(uctx, "mr://gitlab.com/g/p/10/diff"); err == nil || !strings.Contains(err.Error(), "too old for `glab mr diff --raw`") {
		t.Fatalf("expected upgrade hint error, got %v", err)
	}
}

func TestGithubMountedToolcmdAndMultiSessionLifecycle(t *testing.T) {
	// Tests: (1) github registers with Exposure: codemode, DefaultActive: false,
	// and toolcmd.Mountable; under toolcmd's mounted contract, github stays in
	// activeTools (for direct fallback dispatch, matching omp's resolveFallbackTool),
	// appears in toolcmd.MountedNames, is omitted from toolcmd.AdvertisedTools,
	// is hidden from model declarations via toolcmd.PrepareLoadout (HiddenDeclarations),
	// and executes via both bash -> toolcmd.TryDirect -> Context.ExecuteTool and
	// a direct tool call; when tools.commands is off, github is not mounted, not
	// hidden by PrepareLoadout, and is advertised top-level;
	// (2) Register called twice + two sessions in one process: lifecycle.End for
	// session A kills only session A's subprocesses while session B keeps working.
	setupFakeEnv(t, `
case "$1" in
  slow)
    sleep 30
    ;;
  repo)
    echo '{"nameWithOwner":"acme/demo","description":"ok","url":"https://github.com/acme/demo"}'
    ;;
esac
`, "")

	toolcmd.ResetForTests()
	prevEnabled := Enabled
	prevToolcmdEnabled := toolcmd.Enabled
	commandsOn := true
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == SettingEnabled {
			return true
		}
		return def
	}
	toolcmd.Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "tools.commands" {
			return commandsOn
		}
		return def
	}
	t.Cleanup(func() {
		Enabled = prevEnabled
		toolcmd.Enabled = prevToolcmdEnabled
		toolcmd.ResetForTests()
	})

	ext1 := sdk.New("gh-ext-1")
	ext2 := sdk.New("gh-ext-2")
	Register(ext1)
	toolcmd.Register(ext1)
	Register(ext2)
	toolcmd.Register(ext2)

	var capturedCtx sdk.Context
	ext1.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedCtx = ctx
		return nil, nil
	})

	// Mount a bash transport tool on ext1 and an inactive direct tool to verify
	// host exposure enforcement.
	ext1.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "Bash",
		Description:    "Shell",
		Parameters:     sdk.Schema{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
		PrepareLoadout: toolcmd.PrepareLoadout,
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			cmd, _ := params["command"].(string)
			out, rc, handled := toolcmd.TryDirect(ctx, cmd)
			if !handled {
				return sdk.ToolResult{Content: "not handled", IsError: true}, nil
			}
			return sdk.ToolResult{Content: out, IsError: rc != 0}, nil
		},
	})
	defaultOff := false
	ext1.RegisterTool(sdk.ToolDefinition{
		Name:          "inactive_direct",
		Label:         "Inactive Direct",
		Description:   "Must not be callable when inactive",
		Parameters:    sdk.Schema{"type": "object"},
		DefaultActive: &defaultOff,
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) {
			return "nope", nil
		},
	})

	h := startGhFakeHost(t, ext1, t.TempDir(), "gh-mounted-sess", []string{"bash"})
	defer h.stop()

	ghTool := h.tools["github"]
	if ghTool.Exposure != sdk.ToolExposureCodemode {
		t.Fatalf("github Exposure = %q, want %q", ghTool.Exposure, sdk.ToolExposureCodemode)
	}
	if ghTool.DefaultActive == nil || *ghTool.DefaultActive {
		t.Fatalf("github DefaultActive = %v, want false", ghTool.DefaultActive)
	}

	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	active := h.getActiveTools()
	if !slices.Contains(active, "github") {
		t.Fatalf("expected mounted github to stay in activeTools for direct fallback, got %v", active)
	}
	if mounted := toolcmd.MountedNames(capturedCtx); !slices.Contains(mounted, "github") {
		t.Fatalf("expected github in toolcmd.MountedNames, got %v", mounted)
	}
	if advertised := toolcmd.AdvertisedTools(capturedCtx, active); slices.Contains(advertised, "github") {
		t.Fatalf("expected mounted github omitted from toolcmd.AdvertisedTools, got %v", advertised)
	}
	loadout := sdk.ToolLoadout{
		Declared: []sdk.AgentTool{
			{Name: "bash"},
			{Name: "github"},
		},
		GetExposure: func(name string) sdk.ToolExposure {
			if tl, ok := h.tools[name]; ok {
				return tl.Exposure
			}
			return sdk.ToolExposureDirect
		},
	}
	if changes := toolcmd.PrepareLoadout(loadout); changes == nil || !slices.Contains(changes.HiddenDeclarations, "github") {
		t.Fatalf("expected toolcmd.PrepareLoadout to hide github, got %+v", changes)
	}
	if h.isCallableTool("inactive_direct") {
		t.Fatalf("expected inactive direct tool rejected by host exposure rule")
	}
	if !h.isCallableTool("github") {
		t.Fatalf("expected codemode github tool callable while mounted")
	}

	// Execute mounted `github` via bash -> toolcmd.TryDirect -> Context.ExecuteTool.
	raw, errText := h.callToolWithID("bash", "call-bash-gh", map[string]any{"command": "github repo_view --repo acme/demo"})
	if errText != "" {
		t.Fatalf("mounted github bash call failed: %s", errText)
	}
	var out struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.IsError || !strings.Contains(out.Content, "# acme/demo") {
		t.Fatalf("unexpected mounted github output: %+v", out)
	}

	// Direct call to mounted `github` still dispatches (omp resolveFallbackTool).
	rawDirect, errDirect := h.callToolWithID("github", "call-direct-gh", map[string]any{
		"op":   "repo_view",
		"repo": "acme/demo",
	})
	if errDirect != "" {
		t.Fatalf("direct call to mounted github failed: %s", errDirect)
	}
	var directOut struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(rawDirect, &directOut)
	if directOut.IsError || !strings.Contains(directOut.Content, "# acme/demo") {
		t.Fatalf("unexpected direct github output: %+v", directOut)
	}

	// When tools.commands is off, github is advertised top-level and not hidden.
	commandsOn = false
	h.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	activeOff := h.getActiveTools()
	if !slices.Contains(activeOff, "github") {
		t.Fatalf("expected github active when tools.commands is off, got %v", activeOff)
	}
	if mountedOff := toolcmd.MountedNames(capturedCtx); slices.Contains(mountedOff, "github") {
		t.Fatalf("expected github not mounted when tools.commands is off, got %v", mountedOff)
	}
	if advertisedOff := toolcmd.AdvertisedTools(capturedCtx, activeOff); !slices.Contains(advertisedOff, "github") {
		t.Fatalf("expected github advertised top-level when tools.commands is off, got %v", advertisedOff)
	}
	if changesOff := toolcmd.PrepareLoadout(loadout); changesOff != nil && slices.Contains(changesOff.HiddenDeclarations, "github") {
		t.Fatalf("expected github not hidden by PrepareLoadout when tools.commands is off, got %+v", changesOff)
	}

	// Two concurrent sessions running subprocesses: ending sess-A kills only sess-A's child.
	ctxA := withSessionID(context.Background(), "gh-sess-A")
	ctxB := withSessionID(context.Background(), "gh-sess-B")
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := ghRun(ctxA, t.TempDir(), []string{"slow"}, defaultGhOptions())
		errCh <- err
	}()

	// Wait briefly until sess-A's child is registered in liveProcs.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		liveProcs.mu.Lock()
		n := len(liveProcs.procs)
		liveProcs.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// End sess-A twice (idempotent); sess-B must still succeed.
	lifecycle.End("gh-sess-A")
	lifecycle.End("gh-sess-A")
	wg.Wait()
	errA := <-errCh
	if errA == nil {
		t.Fatalf("expected sess-A slow subprocess to terminate on lifecycle.End")
	}
	// N3(d): session-end kill must NOT surface as "command timed out".
	if strings.Contains(errA.Error(), "timed out") || !errors.Is(errA, context.Canceled) {
		t.Fatalf("expected context.Canceled (not timeout) on session-end kill, got: %v", errA)
	}

	resB, err := ghText(ctxB, t.TempDir(), []string{"repo", "view", "acme/demo"}, defaultGhOptions())
	if err != nil || !strings.Contains(resB, "acme/demo") {
		t.Fatalf("expected sess-B to keep working after sess-A ended: err=%v out=%q", err, resB)
	}
}

func TestGithubNoSessionShutdownHookAndHardenedArgs(t *testing.T) {
	// Regression tests for N2 and N3(c).
	// github.Register calls intent.RegisterSync(e, ghDef), which installs intent's
	// single per-extension session_shutdown hook via intent.RegisterLifecycle(e).
	// Pre-installing intent.RegisterLifecycle(e) on the same extension proves that
	// github.Register adds zero session_shutdown handlers of its own (cleanup runs
	// strictly via lifecycle.OnEnd).
	extBase := sdk.New("gh-intent-base")
	intent.RegisterLifecycle(extBase)
	hBase := startGhFakeHost(t, extBase, t.TempDir(), "gh-intent-base-sd", []string{"bash"})
	defer hBase.stop()
	wantShutdownCount := len(hBase.handlers[sdk.EventSessionShutdown])
	if wantShutdownCount != 1 {
		t.Fatalf("expected intent.RegisterLifecycle to install 1 session_shutdown hook, got %v", hBase.handlers[sdk.EventSessionShutdown])
	}

	ext := sdk.New("gh-no-shutdown")
	intent.RegisterLifecycle(ext)
	Register(ext)
	// Registering a second area's tool via intent.RegisterSync on the same
	// extension must not add another session_shutdown hook either.
	intent.RegisterSync(ext, sdk.ToolDefinition{
		Name:        "other_area_tool",
		Label:       "Other",
		Description: "Second area tool",
		Parameters:  sdk.Schema{"type": "object"},
		Execute:     func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	})
	h := startGhFakeHost(t, ext, t.TempDir(), "gh-no-sd", []string{"bash"})
	defer h.stop()
	if ids := h.handlers[sdk.EventSessionShutdown]; len(ids) != wantShutdownCount {
		t.Fatalf("github (and second RegisterSync) must not register extra session_shutdown hooks beyond intent's single hook (%d), got %v", wantShutdownCount, ids)
	}

	writeArgs := hardenedGitArgs([]string{"config", "branch.pr-1.remote", "origin"}, false)
	wantWrite := []string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "config", "branch.pr-1.remote", "origin"}
	if strings.Join(writeArgs, "|") != strings.Join(wantWrite, "|") {
		t.Fatalf("hardenedGitArgs(write) = %v, want %v", writeArgs, wantWrite)
	}
	readArgs := hardenedGitArgs([]string{"rev-parse", "HEAD"}, true)
	if len(readArgs) == 0 || readArgs[0] != "--no-optional-locks" {
		t.Fatalf("hardenedGitArgs(read) = %v, want leading --no-optional-locks", readArgs)
	}
}

func TestGithubPositionalDashGuardAndUntrustedHostRefusal(t *testing.T) {
	// Regression tests for N3(a) and N3(b):
	// (a) leading '-' in model-supplied repo is rejected before invoking gh;
	// (b) untrusted non-github.com hosts in repo / pr:// / issue:// are refused
	//     unless trusted via GH_HOST or gh hosts.yml.
	_, logFile, _ := setupFakeEnv(t, `echo '{"nameWithOwner":"acme/demo"}'`, "")
	ctx := withSessionID(context.Background(), "gh-guard-sess")
	cwd := t.TempDir()

	// N3(a): leading '-' repo rejected in repo_view, file_read, search, pr_create, pr_checkout.
	if _, err := executeRepoView(ctx, cwd, map[string]any{"repo": "--web"}); err == nil || !strings.Contains(err.Error(), "invalid repo: --web") {
		t.Fatalf("expected leading-dash repo rejected in repo_view, got %v", err)
	}
	if _, err := executeFileRead(ctx, cwd, map[string]any{"repo": "-R", "path": "a.go"}); err == nil || !strings.Contains(err.Error(), "invalid repo: -R") {
		t.Fatalf("expected leading-dash repo rejected in file_read, got %v", err)
	}
	if _, err := executeSearchIssuesOrPrs(ctx, cwd, map[string]any{"repo": "--hostname=evil", "query": "x"}, false); err == nil || !strings.Contains(err.Error(), "invalid repo") {
		t.Fatalf("expected leading-dash repo rejected in search_issues, got %v", err)
	}

	// N3(b): untrusted host refused across repo_view, pr://, issue:// before spawning gh.
	if _, err := executeRepoView(ctx, cwd, map[string]any{"repo": "evil.example/acme/demo"}); err == nil || !strings.Contains(err.Error(), "untrusted host evil.example") {
		t.Fatalf("expected untrusted host error for repo_view, got %v", err)
	}
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	_ = urls.Register(issueHandler{})
	_ = urls.Register(prHandler{})
	uctx := urls.Context{Cwd: cwd}
	if _, err := urls.Resolve(uctx, "pr://evil.example/acme/demo/7"); err == nil || !strings.Contains(err.Error(), "untrusted host evil.example") {
		t.Fatalf("expected untrusted host error for pr://, got %v", err)
	}
	if _, err := urls.Resolve(uctx, "issue://evil.example/acme/demo/7"); err == nil || !strings.Contains(err.Error(), "untrusted host evil.example") {
		t.Fatalf("expected untrusted host error for issue://, got %v", err)
	}
	if calls := readLogLines(t, logFile); len(calls) != 0 {
		t.Fatalf("expected 0 gh invocations for rejected inputs, got %v", calls)
	}

	// Setting GH_HOST trusts that host.
	t.Setenv("GH_HOST", "ghe.trusted.example")
	if _, err := executeRepoView(ctx, cwd, map[string]any{"repo": "ghe.trusted.example/acme/demo"}); err != nil {
		t.Fatalf("expected GH_HOST to be trusted, got %v", err)
	}

	// Writing host into hosts.yml trusts that host too.
	t.Setenv("GH_HOST", "")
	if err := os.WriteFile(filepath.Join(os.Getenv("GH_CONFIG_DIR"), "hosts.yml"), []byte("ghe.auth.example:\n    user: alice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeRepoView(ctx, cwd, map[string]any{"repo": "ghe.auth.example/acme/demo"}); err != nil {
		t.Fatalf("expected hosts.yml host to be trusted, got %v", err)
	}
}
