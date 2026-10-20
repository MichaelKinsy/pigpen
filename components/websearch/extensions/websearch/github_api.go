package websearch

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Port of github-api.ts: repository metadata, trees, READMEs and files through `gh api`. The API
// view is what a repository that is too large to clone, a commit SHA and a failed clone fall back to.

const (
	maxTreeEntries     = 200
	maxInlineFileChars = 100_000
	readmeTruncated    = "\n\n[README truncated at 8K chars]"
)

// ghHint is the one-time line showGhHint prints.
const ghHint = "[pi-web-access] Install `gh` CLI for better GitHub repo access including private repos."

// checkGhAvailable is checkGhAvailable: `gh --version` within 5 s, cached for the process unless the
// caller's cancellation cut it short.
func (s *githubState) checkGhAvailable(ctx context.Context) bool {
	s.mu.Lock()
	if s.ghAvailable != nil {
		v := *s.ghAvailable
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()
	res := runProcess(ctx, "gh", []string{"--version"}, processOptions{Timeout: 5 * time.Second})
	if ctx.Err() == nil {
		s.mu.Lock()
		v := res.OK
		s.ghAvailable = &v
		s.mu.Unlock()
	}
	return res.OK
}

// showGhHint is showGhHint: one line on stderr, once.
func (s *githubState) showGhHint() {
	s.mu.Lock()
	if s.ghHintShown {
		s.mu.Unlock()
		return
	}
	s.ghHintShown = true
	out := s.hintOut
	s.mu.Unlock()
	if out == nil {
		out = os.Stderr
	}
	_, _ = io.WriteString(out, ghHint+"\n")
}

// ghAPI runs `gh api <path> --jq <jq>` and returns the trimmed-later stdout; ok is false on any failure.
func (s *githubState) ghAPI(ctx context.Context, path, jq string, timeout time.Duration, maxBuffer int) (string, bool) {
	if !s.checkGhAvailable(ctx) {
		return "", false
	}
	res := runProcess(ctx, "gh", []string{"api", path, "--jq", jq}, processOptions{Timeout: timeout, MaxBuffer: maxBuffer})
	return res.Stdout, res.OK
}

// checkRepoSize is checkRepoSize: the repository size in KB, or false.
func (s *githubState) checkRepoSize(ctx context.Context, owner, repo string) (float64, bool) {
	out, ok := s.ghAPI(ctx, "repos/"+owner+"/"+repo, ".size", 10*time.Second, 0)
	if !ok {
		return 0, false
	}
	return jsParseInt10(out)
}

func (s *githubState) getDefaultBranch(ctx context.Context, owner, repo string) string {
	out, ok := s.ghAPI(ctx, "repos/"+owner+"/"+repo, ".default_branch", 10*time.Second, 0)
	if !ok {
		return ""
	}
	return jsTrim(out)
}

// fetchTreeViaAPI is fetchTreeViaApi: the recursive tree's paths, at most 200 of them; "" when none.
func (s *githubState) fetchTreeViaAPI(ctx context.Context, owner, repo, ref string) string {
	out, ok := s.ghAPI(ctx, "repos/"+owner+"/"+repo+"/git/trees/"+ref+"?recursive=1", ".tree[].path", 15*time.Second, 5*1024*1024)
	if !ok {
		return ""
	}
	var paths []string
	for _, p := range strings.Split(jsTrim(out), "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	if len(paths) > maxTreeEntries {
		return strings.Join(paths[:maxTreeEntries], "\n") + fmt.Sprintf("\n... (%d total entries)", len(paths))
	}
	return strings.Join(paths, "\n")
}

// fetchReadmeViaAPI is fetchReadmeViaApi: the README, cut at 8192 UTF-16 units; "" when unavailable.
func (s *githubState) fetchReadmeViaAPI(ctx context.Context, owner, repo, ref string) string {
	out, ok := s.ghAPI(ctx, "repos/"+owner+"/"+repo+"/readme?ref="+ref, ".content", 10*time.Second, 0)
	if !ok {
		return ""
	}
	decoded := decodeUTF8Lossy(nodeBase64Decode(jsTrim(out)))
	if jsLen(decoded) > 8192 {
		return jsSlice(decoded, 0, 8192) + readmeTruncated
	}
	return decoded
}

// fetchFileViaAPI is fetchFileViaApi: the decoded file; "" when unavailable.
func (s *githubState) fetchFileViaAPI(ctx context.Context, owner, repo, path, ref string) string {
	out, ok := s.ghAPI(ctx, "repos/"+owner+"/"+repo+"/contents/"+path+"?ref="+ref, ".content", 10*time.Second, 2*1024*1024)
	if !ok {
		return ""
	}
	return decodeUTF8Lossy(nodeBase64Decode(jsTrim(out)))
}

func repoTitle(owner, repo string, info *githubURL) string {
	if info.Path != "" {
		return owner + "/" + repo + " - " + info.Path
	}
	return owner + "/" + repo
}

// fetchViaAPI is fetchViaApi: the view of a file, or the tree and README, through gh; nil when gh
// cannot provide it. The tree and the README are fetched together.
func (s *githubState) fetchViaAPI(ctx context.Context, rawURL, owner, repo string, info *githubURL, sizeNote string) *ExtractedContent {
	ref := info.Ref
	if ref == "" {
		ref = s.getDefaultBranch(ctx, owner, repo)
	}
	if ref == "" {
		return nil
	}
	var lines []string
	if sizeNote != "" {
		lines = append(lines, sizeNote, "")
	}
	if info.Type == "blob" && info.Path != "" {
		content := s.fetchFileViaAPI(ctx, owner, repo, info.Path, ref)
		if content == "" {
			return nil
		}
		lines = append(lines, "## "+info.Path)
		if jsLen(content) > maxInlineFileChars {
			lines = append(lines, jsSlice(content, 0, maxInlineFileChars), "\n[File truncated at 100K chars]")
		} else {
			lines = append(lines, content)
		}
		return &ExtractedContent{URL: rawURL, Title: owner + "/" + repo + " - " + info.Path, Content: strings.Join(lines, "\n")}
	}

	var tree, readme string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); tree = s.fetchTreeViaAPI(ctx, owner, repo, ref) }()
	go func() { defer wg.Done(); readme = s.fetchReadmeViaAPI(ctx, owner, repo, ref) }()
	wg.Wait()
	if tree == "" && readme == "" {
		return nil
	}
	if tree != "" {
		lines = append(lines, "## Structure", tree, "")
	}
	if readme != "" {
		lines = append(lines, "## README.md", readme, "")
	}
	lines = append(lines, "This is an API-only view. Clone the repo or use `read`/`bash` for deeper exploration.")
	return &ExtractedContent{URL: rawURL, Title: repoTitle(owner, repo, info), Content: strings.Join(lines, "\n")}
}
