package langsmith

// Port of src/metadata.ts: the shared coding-agent-v1 metadata stamped on the root run, with the git facts of the
// working directory (three git invocations, cached per directory) and the local user name.

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Frozen coding-agent-v1 literals (metadata.ts:9-13).
const (
	lsAgentPurpose       = "coding"
	lsIntegration        = "pi"
	lsAgentRuntime       = "Pi"
	lsTraceSchemaVersion = "coding-agent-v1"
)

// gitMaxBuffer is execFileSync's default maxBuffer (1 MiB): a larger output kills the child and throws.
const gitMaxBuffer = 1024 * 1024

// gitInfo is metadata.ts GitInfo; a nil pointer is `undefined`.
type gitInfo struct {
	RepositoryURL      *string
	RepositoryProvider *string
	RepositoryName     *string
	GitBranch          *string
	GitCommitSHA       *string
}

// gitInfoCache is metadata.ts:23 (module level, never evicted).
var (
	gitInfoMu    sync.Mutex
	gitInfoCache = map[string]*gitInfo{}
)

// limitedBuffer fails a write that would pass the maxBuffer.
type limitedBuffer struct{ bytes.Buffer }

var errMaxBuffer = errors.New("stdout maxBuffer length exceeded")

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > gitMaxBuffer {
		return 0, errMaxBuffer
	}
	return b.Buffer.Write(p)
}

// runGit is metadata.ts:25-36: the trimmed stdout of `git <args>` in cwd, undefined (nil) on any failure or empty
// output. stdin and stderr are discarded. ctx ends a stuck git; the original has no timeout.
func runGit(ctx context.Context, cwd string, args ...string) *string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	cmd.WaitDelay = time.Second // a killed git's children can hold the pipe open
	var out limitedBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	s := jsTrim(strings.ToValidUTF8(out.String(), "\uFFFD"))
	if s == "" {
		return nil
	}
	return &s
}

// parseRemote is metadata.ts:39-70.
func parseRemote(remote string) (url, provider, name string, ok bool) {
	var host, repoPath string
	if h, p, isScp := matchScp(remote); isScp {
		host, repoPath = h, p
	} else {
		var good bool
		host, repoPath, good = parseURLHostPath(remote)
		if !good {
			return "", "", "", false
		}
	}
	if host == "" || repoPath == "" {
		return "", "", "", false
	}
	name = strings.TrimLeft(repoPath, "/")
	name = strings.TrimSuffix(name, ".git")
	if name == "" {
		return "", "", "", false
	}
	switch {
	case strings.Contains(host, "github"):
		provider = "github"
	case strings.Contains(host, "gitlab"):
		provider = "gitlab"
	case strings.Contains(host, "bitbucket"):
		provider = "bitbucket"
	default:
		provider = "other"
	}
	return "https://" + host + "/" + name, provider, name, true
}

// matchScp is the regexp /^[^/@]+@([^:/]+):(.+)$/ (metadata.ts:43): the user part runs to the first `@` and holds no `/`,
// the host runs to the next `:` and holds no `/`, and the rest is non-empty without a line terminator.
func matchScp(s string) (host, path string, ok bool) {
	at := strings.IndexAny(s, "/@")
	if at <= 0 || s[at] != '@' {
		return "", "", false
	}
	rest := s[at+1:]
	end := strings.IndexAny(rest, ":/")
	if end <= 0 || rest[end] != ':' {
		return "", "", false
	}
	host, path = rest[:end], rest[end+1:]
	if path == "" || strings.ContainsAny(path, "\n\r\u2028\u2029") {
		return "", "", false
	}
	return host, path, true
}

// getGitInfo is metadata.ts:72-94. The three git calls run one after another in upstream's order; the result is cached
// per cwd unless ctx ended while they ran.
func getGitInfo(ctx context.Context, cwd string) *gitInfo {
	gitInfoMu.Lock()
	if c, ok := gitInfoCache[cwd]; ok {
		gitInfoMu.Unlock()
		return c
	}
	gitInfoMu.Unlock()

	// Sequential and in upstream's order (metadata.ts:76-80): a host that records exec calls sees the same sequence.
	remote := runGit(ctx, cwd, "config", "--get", "remote.origin.url")
	branch := runGit(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
	sha := runGit(ctx, cwd, "rev-parse", "HEAD")

	info := &gitInfo{GitBranch: branch, GitCommitSHA: sha}
	if remote != nil {
		if u, p, n, ok := parseRemote(*remote); ok {
			info.RepositoryURL, info.RepositoryProvider, info.RepositoryName = &u, &p, &n
		} else {
			// Unparseable remote: retain the raw URL rather than discard it (metadata.ts:85-87).
			info.RepositoryURL = remote
		}
	}
	if ctx.Err() == nil {
		gitInfoMu.Lock()
		gitInfoCache[cwd] = info
		gitInfoMu.Unlock()
	}
	return info
}

// localUsername is metadata.ts:96-102: os.userInfo().username (the account name, no domain), undefined when empty or
// unavailable.
func localUsername() *string {
	u, err := user.Current()
	if err != nil {
		return nil
	}
	name := u.Username
	if runtime.GOOS == "windows" {
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:]
		}
	}
	if name == "" {
		return nil
	}
	return &name
}

// codingAgentMetadataInput is metadata.ts CodingAgentMetadataInput. ThreadID nil and TurnNumber nil are `undefined`.
type codingAgentMetadataInput struct {
	AgentType  string // "root" | "subagent" | "middleware" | "compaction"
	ThreadID   *string
	Cwd        string
	TurnNumber *int
}

// codingAgentMetadata is metadata.ts:122-152. piVersion is the host's Pi VERSION (nil when the host gave none).
func codingAgentMetadata(ctx context.Context, in codingAgentMetadataInput, piVersion *string) *omap {
	git := getGitInfo(ctx, in.Cwd)
	opt := func(p *string) any {
		if p == nil {
			return undef
		}
		return *p
	}
	turn := undef
	if in.TurnNumber != nil {
		turn = *in.TurnNumber
	}
	// compact(): the undefined entries are dropped.
	return obj(
		"ls_agent_purpose", lsAgentPurpose,
		"ls_agent_type", in.AgentType,
		"ls_integration", lsIntegration,
		"ls_agent_runtime", lsAgentRuntime,
		"ls_trace_schema_version", lsTraceSchemaVersion,
		"thread_id", opt(in.ThreadID),
		"ls_integration_version", integrationVersion,
		"ls_agent_runtime_version", opt(piVersion),
		"turn_number", turn,
		"repository_url", opt(git.RepositoryURL),
		"repository_provider", opt(git.RepositoryProvider),
		"repository_name", opt(git.RepositoryName),
		"git_branch", opt(git.GitBranch),
		"git_commit_sha", opt(git.GitCommitSHA),
		"cwd", in.Cwd,
		"local_username", opt(localUsername()),
	).compact()
}
