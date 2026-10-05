package powerline_footer

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Git status for the git segment. upstream: git-status.ts. The original refreshes git in the background and serves the last
// known counts; this port reads git synchronously while the footer context is built (at an event), with the same time-to-live
// caches, so a burst of events costs one git call. The remote host is read while the git segment renders, also from the footer
// renderer's goroutine, so the caches are guarded by gitMu.

const (
	statusTTLMs = 1000
	branchTTLMs = 500
	remoteTTLMs = 60000
)

type timed[T any] struct {
	value T
	at    int64
	cwd   string
	ok    bool
}

// gitMu guards the caches: the footer renderer reads the remote host on the SDK's goroutine while handlers invalidate them.
var gitMu sync.Mutex

var (
	statusCache timed[struct{ Staged, Unstaged, Untracked int }]
	branchCache timed[*string]
	remoteCache timed[string]
)

// readOnlyGitEnv adds GIT_OPTIONAL_LOCKS=0, so polling `git status` does not take .git/index.lock.
func readOnlyGitEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out["GIT_OPTIONAL_LOCKS"] = "0"
	return out
}

func environ() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// runGit runs git with the read-only environment and a timeout; ok is false on any failure or a non-zero exit.
func runGit(cwd string, timeout time.Duration, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	for k, v := range readOnlyGitEnv(environ()) {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRightFunc(string(out), isJSSpace), true
}

func fetchGitBranch(cwd string) *string {
	if b, ok := runGit(cwd, 200*time.Millisecond, "symbolic-ref", "--short", "HEAD"); ok && b != "" {
		return &b
	}
	if sha, ok := runGit(cwd, 200*time.Millisecond, "rev-parse", "--short", "HEAD"); ok && sha != "" {
		d := sha + " (detached)"
		return &d
	}
	return nil
}

func parseGitStatusOutput(output string) (staged, unstaged, untracked int) {
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		x := line[0]
		var y byte
		if len(line) > 1 {
			y = line[1]
		}
		if x == '?' && y == '?' {
			untracked++
			continue
		}
		if x != ' ' && x != '?' {
			staged++
		}
		if y != 0 && y != ' ' {
			unstaged++
		}
	}
	return
}

var scpLike = regexp.MustCompile(`^[^/@]+@([^:/]+):`)

// detectGitHost classifies an origin URL; nil for no remote.
func detectGitHost(remote *string) *string {
	if remote == nil {
		return nil
	}
	trimmed := jsTrim(*remote)
	if trimmed == "" {
		return nil
	}
	var host string
	if m := scpLike.FindStringSubmatch(trimmed); m != nil {
		host = m[1]
	} else {
		u, err := url.Parse(trimmed)
		if err != nil || u.Hostname() == "" {
			return ptr("other")
		}
		host = u.Hostname()
	}
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, h := range [][2]string{{"github.com", "github"}, {"gitlab.com", "gitlab"}, {"bitbucket.org", "bitbucket"}} {
		if host == h[0] || strings.HasSuffix(host, "."+h[0]) {
			return ptr(h[1])
		}
	}
	return ptr("other")
}

// getGitRemoteHost is the origin host of cwd's repository: "github", "gitlab", "bitbucket", "other" or "" (no remote).
func getGitRemoteHost(cwd string) string {
	now := clock()
	gitMu.Lock()
	cached := remoteCache
	gitMu.Unlock()
	if cached.ok && cached.cwd == cwd && now-cached.at < remoteTTLMs {
		return cached.value
	}
	host := ""
	if url, ok := runGit(cwd, 200*time.Millisecond, "remote", "get-url", "origin"); ok {
		if h := detectGitHost(&url); h != nil {
			host = *h
		}
	}
	gitMu.Lock()
	remoteCache = timed[string]{value: host, at: now, cwd: cwd, ok: true}
	gitMu.Unlock()
	return host
}

func invalidateGitStatus() {
	gitMu.Lock()
	defer gitMu.Unlock()
	statusCache = timed[struct{ Staged, Unstaged, Untracked int }]{}
}

func invalidateGitBranch() {
	gitMu.Lock()
	defer gitMu.Unlock()
	branchCache = timed[*string]{}
	remoteCache = timed[string]{}
}

func getCurrentBranch(provider *string, cwd string) *string {
	if provider != nil && *provider != "" && *provider != "detached" {
		return provider
	}
	now := clock()
	gitMu.Lock()
	cached := branchCache
	gitMu.Unlock()
	if cached.ok && cached.cwd == cwd && now-cached.at < branchTTLMs {
		return cached.value
	}
	b := fetchGitBranch(cwd)
	gitMu.Lock()
	branchCache = timed[*string]{value: b, at: now, cwd: cwd, ok: true}
	gitMu.Unlock()
	return b
}

// getGitStatus returns the branch and, in "full" polling mode, the staged, unstaged and untracked counts.
func getGitStatus(providerBranch *string, polling, cwd string) gitStatus {
	var branch *string
	if polling == "off" {
		branch = providerBranch
	} else {
		branch = getCurrentBranch(providerBranch, cwd)
	}
	if polling != "full" {
		return gitStatus{Branch: branch}
	}
	now := clock()
	gitMu.Lock()
	cached := statusCache
	gitMu.Unlock()
	if !cached.ok || cached.cwd != cwd || now-cached.at >= statusTTLMs {
		next := struct{ Staged, Unstaged, Untracked int }{}
		if out, ok := runGit(cwd, 500*time.Millisecond, "status", "--porcelain"); ok {
			next.Staged, next.Unstaged, next.Untracked = parseGitStatusOutput(out)
		}
		cached = timed[struct{ Staged, Unstaged, Untracked int }]{value: next, at: now, cwd: cwd, ok: true}
		gitMu.Lock()
		statusCache = cached
		gitMu.Unlock()
	}
	c := cached.value
	return gitStatus{Branch: branch, Staged: c.Staged, Unstaged: c.Unstaged, Untracked: c.Untracked}
}
