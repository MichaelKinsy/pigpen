package langsmith

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type remoteVector struct {
	Remote     string `json:"remote"`
	Skipped    string `json:"skipped"`
	Stored     string `json:"stored"`
	Repository struct {
		URL      *string `json:"repository_url"`
		Provider *string `json:"repository_provider"`
		Name     *string `json:"repository_name"`
	} `json:"repository"`
	Parsed *struct {
		URL      string `json:"url"`
		Provider string `json:"provider"`
		Name     string `json:"name"`
	} `json:"parsed"`
}

// TestParseRemoteVectors replays the real metadata.js parseRemote on every remote of the corpus (a temporary git
// repository per remote, run through the real codingAgentMetadata).
func TestParseRemoteVectors(t *testing.T) {
	vectors := readVectors[remoteVector](t, "ext_remote.json")
	if len(vectors) < 80 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	parsed := 0
	for _, v := range vectors {
		if v.Skipped != "" {
			continue // git refused to store the remote
		}
		url, provider, name, ok := parseRemote(v.Stored)
		switch {
		case v.Parsed == nil && ok:
			t.Errorf("%q: JS cannot parse it, got %s %s %s", v.Stored, url, provider, name)
		case v.Parsed != nil && !ok:
			t.Errorf("%q: JS gives %+v, got none", v.Stored, *v.Parsed)
		case ok:
			parsed++
			if url != v.Parsed.URL || provider != v.Parsed.Provider || name != v.Parsed.Name {
				t.Errorf("%q: got %s %s %s, want %+v", v.Stored, url, provider, name, *v.Parsed)
			}
		}
	}
	files := 0
	for _, v := range vectors {
		if strings.HasPrefix(v.Remote, "file:") {
			files++
		}
	}
	if files < 30 {
		t.Fatalf("only %d file: remotes", files)
	}
	if parsed < 30 {
		t.Fatalf("only %d remotes parsed", parsed)
	}
}

func TestMatchScp(t *testing.T) {
	for in, want := range map[string][3]string{
		"git@github.com:org/repo.git": {"github.com", "org/repo.git", "y"},
		"git@host:":                   {"", "", ""},
		"@host:x":                     {"", "", ""},
		"a/b@c:x":                     {"", "", ""},
		"git@host/x:y":                {"", "", ""},
		"a@b@c:d":                     {"b@c", "d", "y"},
		"git@h:a\nb":                  {"", "", ""},
		"git@h:a\u2028b":              {"", "", ""},
		"noat":                        {"", "", ""},
	} {
		h, p, ok := matchScp(in)
		if (want[2] == "y") != ok || h != want[0] || p != want[1] {
			t.Errorf("matchScp(%q) = %q %q %v", in, h, p, ok)
		}
	}
}

func TestLowerCaseVectors(t *testing.T) {
	type lv struct{ In, Out, Locale string }
	for _, v := range readVectors[lv](t, "ext_lower.json") {
		if got := jsToLowerCase(v.In); got != v.Out {
			t.Errorf("toLowerCase(%+q) = %+q, want %+q", v.In, got, v.Out)
		}
		if v.Locale != v.Out {
			t.Errorf("the node default locale lowers %+q differently from toLowerCase: the port assumes it does not", v.In)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git %v: %v %s", args, err, out)
	}
}

func resetGitCache() {
	gitInfoMu.Lock()
	clear(gitInfoCache)
	gitInfoMu.Unlock()
}

func TestGetGitInfoRepository(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.email", "a@b.c")
	gitIn(t, dir, "config", "user.name", "n")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	gitIn(t, dir, "remote", "add", "origin", "git@github.com:org/repo.git")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "m")
	resetGitCache()
	info := getGitInfo(context.Background(), dir)
	if info.RepositoryURL == nil || *info.RepositoryURL != "https://github.com/org/repo" || *info.RepositoryProvider != "github" || *info.RepositoryName != "org/repo" {
		t.Fatalf("%+v", info)
	}
	if info.GitBranch == nil || info.GitCommitSHA == nil || len(*info.GitCommitSHA) < 40 {
		t.Fatalf("branch/sha: %+v", info)
	}
	// Cached per cwd: a changed remote is not seen again.
	gitIn(t, dir, "remote", "set-url", "origin", "https://gitlab.com/x/y.git")
	if again := getGitInfo(context.Background(), dir); again != info {
		t.Fatal("the result is cached per cwd")
	}
	resetGitCache()
	if fresh := getGitInfo(context.Background(), dir); *fresh.RepositoryProvider != "gitlab" {
		t.Fatalf("%+v", fresh)
	}
}

func TestGetGitInfoUnparseableRemoteKeepsRaw(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "remote", "add", "origin", "/srv/git/repo.git")
	resetGitCache()
	info := getGitInfo(context.Background(), dir)
	if info.RepositoryURL == nil || *info.RepositoryURL != "/srv/git/repo.git" || info.RepositoryProvider != nil || info.RepositoryName != nil {
		t.Fatalf("%+v", info)
	}
	if info.GitBranch != nil && *info.GitBranch == "" {
		t.Fatal("empty output is undefined")
	}
}

func TestGetGitInfoNoRepositoryAndMissingDir(t *testing.T) {
	resetGitCache()
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		info := getGitInfo(context.Background(), dir)
		if info.RepositoryURL != nil || info.GitBranch != nil || info.GitCommitSHA != nil {
			t.Errorf("%s: %+v", dir, info)
		}
	}
}

func TestGetGitInfoNotCachedWhenContextEnds(t *testing.T) {
	dir := t.TempDir()
	resetGitCache()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	getGitInfo(ctx, dir)
	gitInfoMu.Lock()
	_, cached := gitInfoCache[dir]
	gitInfoMu.Unlock()
	if cached {
		t.Fatal("a result gathered under an ended context must not poison the cache")
	}
}

func TestRunGitOutputCap(t *testing.T) {
	// execFileSync fails when stdout passes maxBuffer (1 MiB): the result is undefined.
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	big := strings.Repeat("x", gitMaxBuffer+10)
	gitIn(t, dir, "config", "remote.origin.url", big)
	if got := runGit(context.Background(), dir, "config", "--get", "remote.origin.url"); got != nil {
		t.Fatalf("an output over the buffer limit must fail, got %d bytes", len(*got))
	}
	gitIn(t, dir, "config", "remote.origin.url", "  spaced  ")
	if got := runGit(context.Background(), dir, "config", "--get", "remote.origin.url"); got == nil || *got != "spaced" {
		t.Fatalf("trimmed: %v", got)
	}
}

func TestRunGitStuckProcessEndsWithContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stuck process is a POSIX shell alias")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "alias.slow", "!sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := runGit(ctx, dir, "slow"); got != nil {
		t.Fatalf("got %v", got)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the stuck git was not stopped with the context")
	}
}

func TestCodingAgentMetadataKeyOrderAndOptionalKeys(t *testing.T) {
	dir := t.TempDir()
	resetGitCache()
	turn := 3
	thread := "sess"
	version := "1.5.0"
	m := codingAgentMetadata(context.Background(), codingAgentMetadataInput{AgentType: "root", ThreadID: &thread, Cwd: dir, TurnNumber: &turn}, &version)
	keys := m.ownKeys()
	got := strings.Join(keys, ",")
	for _, want := range []string{"ls_agent_purpose,ls_agent_type,ls_integration,ls_agent_runtime,ls_trace_schema_version,thread_id,ls_integration_version,ls_agent_runtime_version,turn_number"} {
		if !strings.HasPrefix(got, want) {
			t.Fatalf("keys %s", got)
		}
	}
	if !strings.Contains(got, ",cwd") || m.val("ls_integration_version") != "0.2.1" || m.val("ls_agent_runtime_version") != "1.5.0" || m.val("turn_number") != 3 || m.val("cwd") != dir {
		t.Fatalf("%s", Stringify(m))
	}
	for _, k := range []string{"repository_url", "git_branch", "git_commit_sha"} {
		if m.has(k) {
			t.Errorf("%s must be absent outside a repository", k)
		}
	}
	bare := codingAgentMetadata(context.Background(), codingAgentMetadataInput{AgentType: "compaction", Cwd: dir}, nil)
	for _, k := range []string{"thread_id", "turn_number", "ls_agent_runtime_version"} {
		if bare.has(k) {
			t.Errorf("%s is undefined and dropped", k)
		}
	}
	if bare.val("ls_agent_type") != "compaction" {
		t.Fatal("agent type")
	}
}

func TestLocalUsername(t *testing.T) {
	if name := localUsername(); name != nil && *name == "" {
		t.Fatal("an empty name is undefined")
	}
	_ = os.Getenv
}
