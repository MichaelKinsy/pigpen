// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"os"
	"strings"
)

// The interceptor's decision path: what it claims, when it clones, when it falls back to the API, and when it answers
// nothing at all. upstream: providers/interceptors/github.ts GitHubInterceptor.fetchGitHub, cloneRepo, fetchViaApi,
// checkGhAvailable, checkRepoSize, getDefaultBranch and their private helpers.

// intercept answers a fetch target the interceptor owns. The order is the original's: a cached clone first, then the
// pinned-commit API path, then the size check that may switch to the API, then the clone, then the API as a fallback.
func (g *gitHubInterceptor) interceptFetch(target string, sig abortSignal) (fetchResponse, bool, error) {
	if g.abortedNow(sig) {
		return fetchResponse{}, false, nil
	}
	if !g.options.Enabled {
		return fetchResponse{}, false, nil
	}
	info, ok := parseGitHubURL(target)
	if !ok {
		return fetchResponse{}, false, nil
	}

	key := cloneCacheKey(info.Owner, info.Repo, info.Ref, info.HasRef)
	if g.cloneCache == nil {
		g.cloneCache = map[string]cachedClone{}
	}
	if cached, hit := g.cloneCache[key]; hit {
		if g.abortedNow(sig) {
			return fetchResponse{}, false, nil
		}
		if cached.ok {
			return gitHubCloneResponse(g.generate(cached.localPath, *info), *info), true, nil
		}
		return g.fetchViaAPI(*info, "")
	}

	// A pinned commit is never cloned: the clone would have no ref to check out cheaply and the API answers directly.
	if info.RefIsFullSHA {
		const sizeNote = "Note: Commit SHA URLs use the GitHub API instead of cloning."
		return g.fetchViaAPI(*info, sizeNote)
	}

	if !g.forceClone {
		if sizeKB, known := g.repoSizeKB(info.Owner, info.Repo); known {
			sizeMB := repoSizeMB(sizeKB)
			if sizeMB > g.options.MaxRepoSizeMB {
				sizeNote := "Note: Repository is " + itoa(int(sizeMB+0.5)) + "MB (threshold: " +
					itoa(int(g.options.MaxRepoSizeMB)) + "MB). " +
					"Showing API-fetched content instead of full clone. Ask the user if they'd like to clone the full repo — " +
					"if yes, call web_fetch again with the same URL."
				res, ok, err := g.fetchViaAPI(*info, sizeNote)
				if ok || err != nil {
					return res, ok, err
				}
				return fetchResponse{}, false, nil
			}
		}
	}

	if g.abortedNow(sig) {
		return fetchResponse{}, false, nil
	}

	localPath, cloned := g.cloneRepo(*info)
	entry := cachedClone{localPath: localPath, cloneResult: localPath, ok: cloned}
	g.cloneCache[key] = entry

	if g.abortedNow(sig) {
		if !cloned {
			delete(g.cloneCache, key)
		}
		return fetchResponse{}, false, nil
	}
	if !cloned {
		delete(g.cloneCache, key)
		return g.fetchViaAPI(*info, "")
	}
	return gitHubCloneResponse(g.generate(localPath, *info), *info), true, nil
}

// gitHubCloneResponse wraps the rendered clone body in the response envelope, whose title names the path when the URL
// carried one. upstream: the title expressions next to generateCloneContent.
func gitHubCloneResponse(text string, info gitHubURLInfo) fetchResponse {
	title := info.Owner + "/" + info.Repo
	if info.HasPath {
		title += " - " + info.Path
	}
	return fetchResponse{Text: text, Title: title, HasTitle: true, ContentType: "text/plain", HasContentType: true}
}

// abort carries the request's cancel state, so a twin can drive an abort without a context. upstream: the signal checks
// scattered through fetchGitHub.
type abortSignal interface {
	aborted() bool
}

func (g *gitHubInterceptor) abortedNow(sig abortSignal) bool {
	if g.aborted {
		return true
	}
	return sig != nil && sig.aborted()
}

// repoSizeKB asks gh for the repository size, caching gh's availability first. A missing gh or a failed query reads as
// "unknown", which is what lets the clone path run. upstream: checkGhAvailable + checkRepoSize.
func (g *gitHubInterceptor) repoSizeKB(owner, repo string) (float64, bool) {
	if !g.ghAvailableCached() {
		return 0, false
	}
	out := strings.TrimSpace(g.runner.ghJSON("repos/"+owner+"/"+repo, ".size", 10))
	if out == "" {
		return 0, false
	}
	var kb float64
	if _, err := fmtSscan(out, &kb); err != nil {
		return 0, false
	}
	return kb, true
}

// ghAvailableCached probes gh once and remembers the answer. upstream: the ghAvailable field and checkGhAvailable.
func (g *gitHubInterceptor) ghAvailableCached() bool {
	if g.ghProbed {
		return g.ghPresent
	}
	g.ghPresent = g.runner.ghAvailable()
	g.ghProbed = true
	return g.ghPresent
}

// cloneRepo clones through gh when it is available and through plain git otherwise, removing any previous clone first.
// upstream: cloneRepo.
func (g *gitHubInterceptor) cloneRepo(info gitHubURLInfo) (string, bool) {
	localPath := cloneDir(g.options.ClonePath, info.Owner, info.Repo, info.Ref, info.HasRef)
	_ = removeAll(localPath)
	timeout := g.options.CloneTimeoutSeconds

	if g.ghAvailableCached() {
		args := []string{"gh", "repo", "clone", info.Owner + "/" + info.Repo, localPath, "--", "--depth", "1", "--single-branch"}
		if info.HasRef {
			args = append(args, "--branch", info.Ref)
		}
		return g.runner.clone(args, timeout)
	}
	// Without gh the user is told once, because the git path cannot reach a private repository.
	g.showGhHint()
	args := []string{"git", "clone", "--depth", "1", "--single-branch"}
	if info.HasRef {
		args = append(args, "--branch", info.Ref)
	}
	args = append(args, "https://github.com/"+info.Owner+"/"+info.Repo+".git", localPath)
	return g.runner.clone(args, timeout)
}

// showGhHint prints the install hint once per process. upstream: showGhHint.
func (g *gitHubInterceptor) showGhHint() {
	if g.ghHintShown {
		return
	}
	g.ghHintShown = true
	g.warn("[rpiv-web-tools] Install `gh` CLI for better GitHub repo access including private repos.")
}

// fetchViaAPI builds the API-only view: a file for a blob URL, otherwise the tree plus the README and the note that
// this is the shallow view. Without gh, or without a resolvable ref, it answers nothing. upstream: fetchViaAPI.
func (g *gitHubInterceptor) fetchViaAPI(info gitHubURLInfo, sizeNote string) (fetchResponse, bool, error) {
	if !g.ghAvailableCached() {
		return fetchResponse{}, false, nil
	}
	ref := info.Ref
	if !info.HasRef {
		out := strings.TrimSpace(g.runner.ghJSON("repos/"+info.Owner+"/"+info.Repo, ".default_branch", 10))
		if out == "" {
			return fetchResponse{}, false, nil
		}
		ref = out
	}

	lines := []string{}
	if sizeNote != "" {
		lines = append(lines, sizeNote, "")
	}

	if info.Type == githubURLBlob && info.HasPath {
		content, ok := decodeBase64Content(
			g.runner.ghRaw("repos/"+info.Owner+"/"+info.Repo+"/contents/"+info.Path+"?ref="+ref, ".content", 10, 2*1024*1024), 0)
		if !ok {
			return fetchResponse{}, false, nil
		}
		lines = append(lines, "## "+info.Path)
		if len(content) > maxInlineFileChars {
			lines = append(lines, content[:maxInlineFileChars], "\n[File truncated at 100K chars]")
		} else {
			lines = append(lines, content)
		}
		return gitHubCloneResponse(strings.Join(lines, "\n"), info), true, nil
	}

	tree, hasTree := treeViaAPI(g.runner.ghRaw(
		"repos/"+info.Owner+"/"+info.Repo+"/git/trees/"+ref+"?recursive=1", ".tree[].path", 15, 5*1024*1024))
	readme, hasReadme := decodeBase64Content(
		g.runner.ghRaw("repos/"+info.Owner+"/"+info.Repo+"/readme?ref="+ref, ".content", 10, 0), readmeCharLimit)

	if !hasTree && !hasReadme {
		return fetchResponse{}, false, nil
	}
	if hasTree {
		lines = append(lines, "## Structure", tree, "")
	}
	if hasReadme {
		lines = append(lines, "## README.md", readme, "")
	}
	lines = append(lines, "This is an API-only view. Clone the repo or use `read`/`bash` for deeper exploration.")
	return gitHubCloneResponse(strings.Join(lines, "\n"), info), true, nil
}

// removeAll deletes a directory tree, ignoring the failure a best-effort cleanup may hit. upstream: the rmSync calls
// wrapped in try/catch.
func removeAll(path string) error { return os.RemoveAll(path) }

// fmtSscan parses a numeric probe result. upstream: parseInt(stdout.trim(), 10).
func fmtSscan(s string, out *float64) (int, error) { return fmt.Sscanf(s, "%g", out) }
