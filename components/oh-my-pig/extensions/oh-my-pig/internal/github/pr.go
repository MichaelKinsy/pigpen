package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var (
	ghRepoCloneFields = []string{"nameWithOwner", "sshUrl", "url"}
	ghPrCreateFields  = []string{
		"author", "baseRefName", "createdAt", "headRefName", "isDraft",
		"labels", "number", "state", "title", "url",
	}
	ghPrCheckoutFields = []string{
		"baseRefName", "headRefName", "headRefOid", "headRepository",
		"headRepositoryOwner", "isCrossRepository", "maintainerCanModify",
		"number", "title", "url",
	}
	nonAlnumRe      = regexp.MustCompile(`[^a-z0-9]+`)
	prHashNumRe     = regexp.MustCompile(`^#?(\d+)$`)
	repoLocksMu     sync.Mutex
	repoLocksByRoot = make(map[string]*sync.Mutex)
)

func lockRepo(repoRoot string) func() {
	repoLocksMu.Lock()
	mu, ok := repoLocksByRoot[repoRoot]
	if !ok {
		mu = &sync.Mutex{}
		repoLocksByRoot[repoRoot] = mu
	}
	repoLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnumRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func sanitizeRemoteName(val string) string {
	s := slugify(val)
	if s == "" {
		return "fork"
	}
	return "fork-" + s
}

func hashPath(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])[:12]
}

var worktreeBaseDirOverride string

func worktreeBaseDir(sdkCtx sdk.Context) string {
	if worktreeBaseDirOverride != "" {
		return worktreeBaseDirOverride
	}
	var zero sdk.Context
	if sdkCtx != zero {
		if home, err := sdkCtx.ConfigHome(); err == nil && home != "" {
			return filepath.Join(home, "worktrees")
		}
	}
	return filepath.Join(os.TempDir(), "oh-my-pig-worktrees")
}

const worktreePathMaxSuffix = 100

// resolveAvailableWorktreePath returns the first of basePath, basePath-2, ...
// basePath-100 that is neither a registered worktree nor present on disk.
// upstream: packages/coding-agent/src/worktree/paths.ts:20-41
func resolveAvailableWorktreePath(basePath string, existing []worktreeEntry) (string, error) {
	registered := make(map[string]bool, len(existing))
	for _, e := range existing {
		if abs, err := filepath.Abs(e.path); err == nil {
			registered[abs] = true
		} else {
			registered[e.path] = true
		}
	}
	for attempt := range worktreePathMaxSuffix {
		candidate := basePath
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", basePath, attempt+1)
		}
		normalized, err := filepath.Abs(candidate)
		if err != nil {
			normalized = candidate
		}
		if registered[normalized] {
			continue
		}
		_, statErr := os.Stat(normalized)
		if os.IsNotExist(statErr) {
			return candidate, nil
		}
		if statErr != nil {
			return "", statErr
		}
	}
	return "", fmt.Errorf("could not find an unused worktree path under %s (tried %d suffixes)", basePath, worktreePathMaxSuffix)
}

func requireGitRepoRoot(ctx context.Context, cwd string) (string, error) {
	root, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", fmt.Errorf("Current git repository is unavailable.")
	}
	return root, nil
}

func requirePrimaryGitRepoRoot(ctx context.Context, cwd string) (string, error) {
	commonDir, err := runGit(ctx, cwd, "rev-parse", "--git-common-dir")
	if err != nil || commonDir == "" {
		return requireGitRepoRoot(ctx, cwd)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(cwd, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir), nil
	}
	return requireGitRepoRoot(ctx, cwd)
}

type worktreeEntry struct {
	path   string
	branch string
}

func listWorktrees(ctx context.Context, repoRoot string) ([]worktreeEntry, error) {
	out, err := runGit(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
			cur = worktreeEntry{}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			flush()
			cur.path = p
		} else if b, ok := strings.CutPrefix(line, "branch "); ok {
			cur.branch = b
		}
	}
	flush()
	return entries, nil
}

func selectPrCloneURL(originURL string, repo ghRepoViewData) string {
	if strings.HasPrefix(originURL, "http://") || strings.HasPrefix(originURL, "https://") {
		if repo.URL != "" {
			return repo.URL
		}
		return repo.SSHURL
	}
	if repo.SSHURL != "" {
		return repo.SSHURL
	}
	return repo.URL
}

func ensurePrRemote(ctx context.Context, repoRoot string, data ghPrViewData) (remoteName, remoteURL string, err error) {
	isCross := data.IsCrossRepository != nil && *data.IsCrossRepository
	if !isCross {
		originURL, err := runGit(ctx, repoRoot, "remote", "get-url", "origin")
		if err != nil || originURL == "" {
			return "", "", fmt.Errorf("origin remote is unavailable for this repository.")
		}
		return "origin", originURL, nil
	}

	if data.HeadRepository == nil || strings.TrimSpace(data.HeadRepository.NameWithOwner) == "" {
		return "", "", fmt.Errorf("head repository must not be empty")
	}
	headRepo := strings.TrimSpace(data.HeadRepository.NameWithOwner)
	pullRepo, _, _ := parsePullRequestURL(data.URL)
	var pullHost string
	if pullRepo != "" {
		pullHost = ParseRepoRef(pullRepo).Host
	}
	var repoSummary ghRepoViewData
	if err := ghJSON(ctx, repoRoot, []string{
		"repo", "view", FormatRepoRef(pullHost, headRepo),
		"--json", strings.Join(ghRepoCloneFields, ","),
	}, &repoSummary, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
		return "", "", err
	}
	originURL, _ := runGit(ctx, repoRoot, "remote", "get-url", "origin")
	rURL := selectPrCloneURL(originURL, repoSummary)
	if rURL == "" {
		return "", "", fmt.Errorf("Could not determine a clone URL for %s.", headRepo)
	}

	remotesOut, _ := runGit(ctx, repoRoot, "remote")
	existing := make(map[string]string)
	for _, r := range strings.Split(remotesOut, "\n") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if u, err := runGit(ctx, repoRoot, "remote", "get-url", r); err == nil && u != "" {
			existing[r] = u
			if u == rURL {
				return r, u, nil
			}
		}
	}

	owner := "fork"
	if data.HeadRepositoryOwner != nil && data.HeadRepositoryOwner.Login != "" {
		owner = data.HeadRepositoryOwner.Login
	} else if first, _, ok := strings.Cut(headRepo, "/"); ok && first != "" {
		owner = first
	}
	preferred := sanitizeRemoteName(owner)
	name := preferred
	for suffix := 2; ; suffix++ {
		if _, exists := existing[name]; !exists {
			break
		}
		name = fmt.Sprintf("%s-%d", preferred, suffix)
	}
	if _, err := runGitWrite(ctx, repoRoot, "remote", "add", name, rURL); err != nil {
		return "", "", fmt.Errorf("git remote add %s failed: %w", name, err)
	}
	return name, rURL, nil
}

type prCheckoutOutcome struct {
	data         ghPrViewData
	localBranch  string
	worktreePath string
	remoteName   string
	remoteURL    string
	headRefName  string
	reused       bool
}

func checkoutPullRequest(ctx context.Context, sdkCtx sdk.Context, cwd, prRef, repo string, force bool) (prCheckoutOutcome, error) {
	if strings.HasPrefix(prRef, "-") {
		return prCheckoutOutcome{}, fmt.Errorf("invalid PR identifier: %s. Pass a PR number, URL, or branch name.", prRef)
	}
	if err := validateRepoParam(sessionIDFromCtx(ctx), repo); err != nil {
		return prCheckoutOutcome{}, err
	}
	args := []string{"pr", "view"}
	if prRef != "" {
		args = append(args, prRef)
	}
	appendRepoFlag(&args, repo, prRef)
	args = append(args, "--json", strings.Join(ghPrCheckoutFields, ","))

	var data ghPrViewData
	if err := ghJSON(ctx, cwd, args, &data, ghCommandOptions{repoProvided: repo != "", trimOutput: true}); err != nil {
		return prCheckoutOutcome{}, err
	}
	if data.Number == nil {
		return prCheckoutOutcome{}, fmt.Errorf("GitHub CLI did not return a pull request number.")
	}
	prNumber := *data.Number
	headRefName := strings.TrimSpace(data.HeadRefName)
	if headRefName == "" {
		return prCheckoutOutcome{}, fmt.Errorf("head branch must not be empty")
	}
	headRefOID := strings.TrimSpace(data.HeadRefOID)
	if headRefOID == "" {
		return prCheckoutOutcome{}, fmt.Errorf("head commit must not be empty")
	}

	repoRoot, err := requireGitRepoRoot(ctx, cwd)
	if err != nil {
		return prCheckoutOutcome{}, err
	}
	primaryRoot, err := requirePrimaryGitRepoRoot(ctx, repoRoot)
	if err != nil {
		return prCheckoutOutcome{}, err
	}
	localBranch := fmt.Sprintf("pr-%d", prNumber)
	localBranchRef := "refs/heads/" + localBranch
	wtPath := filepath.Join(worktreeBaseDir(sdkCtx), fmt.Sprintf("%d-%s", prNumber, hashPath(primaryRoot)))

	unlock := lockRepo(primaryRoot)
	defer unlock()

	worktrees, _ := listWorktrees(ctx, repoRoot)
	var existingWT *worktreeEntry
	for i := range worktrees {
		if worktrees[i].branch == localBranchRef {
			existingWT = &worktrees[i]
			break
		}
	}

	remoteName, remoteURL, err := ensurePrRemote(ctx, repoRoot, data)
	if err != nil {
		return prCheckoutOutcome{}, err
	}
	fetchSpec := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", headRefName, remoteName, headRefName)
	if _, err := runGitNetwork(ctx, repoRoot, "fetch", remoteName, fetchSpec); err != nil {
		return prCheckoutOutcome{}, fmt.Errorf("git fetch %s failed: %w", remoteName, err)
	}

	if existingWT == nil {
		remoteRef := fmt.Sprintf("refs/remotes/%s/%s", remoteName, headRefName)
		if existingOID, err := runGit(ctx, repoRoot, "rev-parse", "--verify", localBranchRef); err == nil && existingOID != "" {
			if existingOID != headRefOID {
				if !force {
					return prCheckoutOutcome{}, fmt.Errorf("local branch %s already exists at %s; pass force=true to reset it", localBranch, formatShortSHA(existingOID))
				}
				if _, err := runGitWrite(ctx, repoRoot, "branch", "-f", localBranch, remoteRef); err != nil {
					return prCheckoutOutcome{}, fmt.Errorf("git branch -f %s failed: %w", localBranch, err)
				}
			}
		} else {
			if _, err := runGitWrite(ctx, repoRoot, "branch", localBranch, remoteRef); err != nil {
				return prCheckoutOutcome{}, fmt.Errorf("git branch %s failed: %w", localBranch, err)
			}
		}
	}

	prefix := fmt.Sprintf("branch.%s.", localBranch)
	isCross := data.IsCrossRepository != nil && *data.IsCrossRepository
	canModify := data.MaintainerCanModify != nil && *data.MaintainerCanModify
	configs := [][2]string{
		{prefix + "remote", remoteName},
		{prefix + "merge", "refs/heads/" + headRefName},
		{prefix + "pushRemote", remoteName},
		{prefix + "ompPrHeadRef", headRefName},
		{prefix + "ompPrUrl", data.URL},
		{prefix + "ompPrIsCrossRepository", strconv.FormatBool(isCross)},
		{prefix + "ompPrMaintainerCanModify", strconv.FormatBool(canModify)},
	}
	for _, kv := range configs {
		if _, err := runGitWrite(ctx, repoRoot, "config", kv[0], kv[1]); err != nil {
			return prCheckoutOutcome{}, fmt.Errorf("git config %s failed: %w", kv[0], err)
		}
	}

	finalWT := wtPath
	if existingWT != nil {
		finalWT = existingWT.path
	} else {
		resolvedPath, err := resolveAvailableWorktreePath(wtPath, worktrees)
		if err != nil {
			return prCheckoutOutcome{}, err
		}
		finalWT = resolvedPath
		if err := os.MkdirAll(filepath.Dir(finalWT), 0o755); err != nil {
			return prCheckoutOutcome{}, err
		}
		if _, err := runGitWrite(ctx, repoRoot, "worktree", "add", finalWT, localBranch); err != nil {
			return prCheckoutOutcome{}, fmt.Errorf("git worktree add failed: %w", err)
		}
	}
	if resolved, err := filepath.EvalSymlinks(finalWT); err == nil {
		finalWT = resolved
	}

	return prCheckoutOutcome{
		data:         data,
		localBranch:  localBranch,
		worktreePath: finalWT,
		remoteName:   remoteName,
		remoteURL:    remoteURL,
		headRefName:  headRefName,
		reused:       existingWT != nil,
	}, nil
}

func formatPrCheckoutResult(o prCheckoutOutcome) string {
	numStr := "?"
	if o.data.Number != nil {
		numStr = fmt.Sprintf("%d", *o.data.Number)
	}
	header := fmt.Sprintf("# Checked Out Pull Request #%s", numStr)
	if o.reused {
		header = fmt.Sprintf("# Pull Request #%s Worktree", numStr)
	}
	lines := []string{header, ""}
	pushLine(&lines, "Title", o.data.Title)
	pushLine(&lines, "URL", o.data.URL)
	pushLine(&lines, "Base", o.data.BaseRefName)
	pushLine(&lines, "Head", o.data.HeadRefName)
	pushLine(&lines, "Local branch", o.localBranch)
	pushLine(&lines, "Worktree", o.worktreePath)
	pushLine(&lines, "Remote", o.remoteName)
	pushLine(&lines, "Remote URL", o.remoteURL)
	pushLineBool(&lines, "Cross repository", o.data.IsCrossRepository)
	pushLineBool(&lines, "Maintainer can modify", o.data.MaintainerCanModify)
	lines = append(lines, "")
	if o.reused {
		lines = append(lines, "Reused the existing PR worktree.")
	} else {
		lines = append(lines, "Created a dedicated worktree for this PR and configured the local branch to push back to the PR head branch.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func outcomeToSummary(o prCheckoutOutcome) GhPrCheckoutSummary {
	return GhPrCheckoutSummary{
		PrNumber:     o.data.Number,
		URL:          o.data.URL,
		Branch:       o.localBranch,
		WorktreePath: o.worktreePath,
		Remote:       o.remoteName,
		RemoteBranch: o.headRefName,
		Reused:       o.reused,
	}
}

func executePrCheckout(ctx context.Context, sdkCtx sdk.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	repo := optString(params, "repo")
	force := optBool(params, "force")
	prList := optStringOrSlice(params, "pr")
	if len(prList) == 0 {
		prList = []string{""}
	}
	isMulti := len(prList) > 1

	var outcomes []prCheckoutOutcome
	type failRec struct {
		ref string
		err error
	}
	var failures []failRec

	for _, ref := range prList {
		o, err := checkoutPullRequest(ctx, sdkCtx, cwd, ref, repo, force)
		if err != nil {
			failures = append(failures, failRec{ref: ref, err: err})
			continue
		}
		outcomes = append(outcomes, o)
	}

	if len(failures) > 0 {
		if ctx.Err() != nil {
			return sdk.ToolResult{}, ctx.Err()
		}
		var failLines []string
		for _, f := range failures {
			label := f.ref
			if label == "" {
				label = "(current branch)"
			}
			failLines = append(failLines, fmt.Sprintf("- %s: %s", label, f.err.Error()))
		}
		if len(outcomes) == 0 {
			if len(failures) == 1 {
				return sdk.ToolResult{}, failures[0].err
			}
			return sdk.ToolResult{}, fmt.Errorf("all %d PR checkouts failed:\n%s", len(failures), strings.Join(failLines, "\n"))
		}
		var sections []string
		var summaries []GhPrCheckoutSummary
		for _, o := range outcomes {
			sections = append(sections, formatPrCheckoutResult(o))
			summaries = append(summaries, outcomeToSummary(o))
		}
		header := fmt.Sprintf("# %d/%d Pull Request Worktrees checked out (%d failed)", len(outcomes), len(prList), len(failures))
		text := header + "\n\n" + strings.Join(sections, "\n\n---\n\n") + "\n\n## Failed\n" + strings.Join(failLines, "\n")
		return sdk.ToolResult{
			Content: strings.TrimSpace(text),
			Details: GhToolDetails{Repo: repo, Checkouts: summaries},
		}, nil
	}

	if !isMulti {
		o := outcomes[0]
		r := repo
		if r == "" && o.data.HeadRepository != nil {
			r = o.data.HeadRepository.NameWithOwner
		}
		return sdk.ToolResult{
			Content: formatPrCheckoutResult(o),
			Details: GhToolDetails{
				Meta:         metaWithSourceURL(o.data.URL),
				Repo:         r,
				Branch:       o.localBranch,
				WorktreePath: o.worktreePath,
				Remote:       o.remoteName,
				RemoteBranch: o.headRefName,
				Checkouts:    []GhPrCheckoutSummary{outcomeToSummary(o)},
			},
		}, nil
	}

	var sections []string
	var summaries []GhPrCheckoutSummary
	reusedCount := 0
	for _, o := range outcomes {
		sections = append(sections, formatPrCheckoutResult(o))
		summaries = append(summaries, outcomeToSummary(o))
		if o.reused {
			reusedCount++
		}
	}
	newCount := len(outcomes) - reusedCount
	var parts []string
	if newCount > 0 {
		parts = append(parts, fmt.Sprintf("%d checked out", newCount))
	}
	if reusedCount > 0 {
		parts = append(parts, fmt.Sprintf("%d reused", reusedCount))
	}
	header := fmt.Sprintf("# %d Pull Request Worktrees (%s)", len(outcomes), strings.Join(parts, ", "))
	text := header + "\n\n" + strings.Join(sections, "\n\n---\n\n")
	return sdk.ToolResult{
		Content: strings.TrimSpace(text),
		Details: GhToolDetails{Repo: repo, Checkouts: summaries},
	}, nil
}

func prCheckoutBranch(prList []string) string {
	if len(prList) == 0 {
		return ""
	}
	ref := prList[0]
	if m := prHashNumRe.FindStringSubmatch(ref); m != nil {
		return "pr-" + m[1]
	}
	if _, n, ok := parsePullRequestURL(ref); ok {
		return fmt.Sprintf("pr-%d", n)
	}
	return ""
}

func executePrPush(ctx context.Context, sdkCtx sdk.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	repoRoot, err := requireGitRepoRoot(ctx, cwd)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	localBranch := optString(params, "branch")
	if localBranch == "" {
		localBranch = prCheckoutBranch(optStringOrSlice(params, "pr"))
	}
	if localBranch == "" {
		localBranch, err = requireCurrentGitBranch(ctx, repoRoot)
		if err != nil {
			return sdk.ToolResult{}, err
		}
	}
	localRef := "refs/heads/" + localBranch
	if _, err := runGit(ctx, repoRoot, "rev-parse", "--verify", localRef); err != nil {
		return sdk.ToolResult{}, fmt.Errorf("local branch %s does not exist", localBranch)
	}

	prefix := fmt.Sprintf("branch.%s.", localBranch)
	headRef, _ := runGit(ctx, repoRoot, "config", "--get", prefix+"ompPrHeadRef")
	if headRef == "" {
		return sdk.ToolResult{}, fmt.Errorf("branch %s has no PR push metadata; check it out via op: pr_checkout first", localBranch)
	}
	remoteName, _ := runGit(ctx, repoRoot, "config", "--get", prefix+"pushRemote")
	if remoteName == "" {
		remoteName, _ = runGit(ctx, repoRoot, "config", "--get", prefix+"remote")
	}
	if remoteName == "" {
		return sdk.ToolResult{}, fmt.Errorf("branch %s has no configured push remote", localBranch)
	}
	prURL, _ := runGit(ctx, repoRoot, "config", "--get", prefix+"ompPrUrl")
	remoteURL, _ := runGit(ctx, repoRoot, "remote", "get-url", remoteName)

	curBranch, _ := runGit(ctx, repoRoot, "symbolic-ref", "--short", "HEAD")
	sourceRef := localRef
	if curBranch == localBranch {
		sourceRef = "HEAD"
	}
	refspec := fmt.Sprintf("%s:refs/heads/%s", sourceRef, headRef)
	forceWithLease := optBool(params, "forceWithLease")
	pushArgs := []string{"push", "--no-follow-tags"}
	if forceWithLease {
		pushArgs = append(pushArgs, "--force-with-lease")
	}
	pushArgs = append(pushArgs, remoteName, refspec)
	if _, err := runGitNetwork(ctx, repoRoot, pushArgs...); err != nil {
		return sdk.ToolResult{}, fmt.Errorf("git push failed: %w", err)
	}

	if pushedRepo, pushedNum, ok := parsePullRequestURL(prURL); ok {
		InvalidateAllForNumber(sdkCtx, pushedNum, pushedRepo)
	}

	lines := []string{"# Pushed Pull Request Branch", ""}
	pushLine(&lines, "Local branch", localBranch)
	pushLine(&lines, "Remote", remoteName)
	pushLine(&lines, "Remote branch", headRef)
	pushLine(&lines, "Remote URL", remoteURL)
	pushLine(&lines, "PR", prURL)
	pushLineBool(&lines, "Force with lease", &forceWithLease)
	lines = append(lines, "", fmt.Sprintf("Pushed %s to %s:%s.", localBranch, remoteName, headRef))
	return sdk.ToolResult{
		Content: strings.TrimSpace(strings.Join(lines, "\n")),
		Details: GhToolDetails{
			Meta:         metaWithSourceURL(prURL),
			Branch:       localBranch,
			Remote:       remoteName,
			RemoteBranch: headRef,
		},
	}, nil
}

func executePrCreate(ctx context.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	repo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), repo); err != nil {
		return sdk.ToolResult{}, err
	}
	title := optString(params, "title")
	body, hasBody := params["body"].(string)
	base := optString(params, "base")
	head := optString(params, "head")
	draft := optBool(params, "draft")
	fill := optBool(params, "fill")
	reviewers := optStringOrSlice(params, "reviewer")
	assignees := optStringOrSlice(params, "assignee")
	labels := optStringOrSlice(params, "label")

	if !fill && title == "" {
		return sdk.ToolResult{}, fmt.Errorf("title is required unless fill is true")
	}
	if fill && (title != "" || hasBody) {
		return sdk.ToolResult{}, fmt.Errorf("fill is mutually exclusive with title and body")
	}

	args := []string{"pr", "create"}
	appendRepoFlag(&args, repo, "")
	if title != "" {
		args = append(args, "--title", title)
	}
	if base != "" {
		args = append(args, "--base", base)
	}
	if head != "" {
		args = append(args, "--head", head)
	}
	if draft {
		args = append(args, "--draft")
	}
	if fill {
		args = append(args, "--fill")
	}
	for _, r := range reviewers {
		args = append(args, "--reviewer", r)
	}
	for _, a := range assignees {
		args = append(args, "--assignee", a)
	}
	for _, l := range labels {
		args = append(args, "--label", l)
	}

	var bodyDir string
	defer func() {
		if bodyDir != "" {
			_ = os.RemoveAll(bodyDir)
		}
	}()

	if !fill {
		if hasBody && len(body) > 0 {
			dir, err := os.MkdirTemp("", "gh-pr-body-")
			if err != nil {
				return sdk.ToolResult{}, err
			}
			bodyDir = dir
			bodyFile := filepath.Join(dir, "body.md")
			if err := os.WriteFile(bodyFile, []byte(body), 0o600); err != nil {
				return sdk.ToolResult{}, err
			}
			args = append(args, "--body-file", bodyFile)
		} else {
			args = append(args, "--body", "")
		}
	}

	output, err := ghText(ctx, cwd, args, ghCommandOptions{repoProvided: repo != "", trimOutput: true})
	if err != nil {
		return sdk.ToolResult{}, err
	}
	prURL := strings.TrimSpace(output)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") {
			prURL = line
			break
		}
	}
	parsedRepo, parsedNum, hasParsed := parsePullRequestURL(prURL)
	resolvedRepo := repo
	if resolvedRepo == "" {
		resolvedRepo = parsedRepo
	}

	var prView *ghPrViewData
	if resolvedRepo != "" && hasParsed {
		var v ghPrViewData
		if err := ghJSON(ctx, cwd, []string{
			"pr", "view", strconv.Itoa(parsedNum),
			"--repo", resolvedRepo,
			"--json", strings.Join(ghPrCreateFields, ","),
		}, &v, ghCommandOptions{repoProvided: true, trimOutput: true}); err == nil {
			prView = &v
		}
	}

	var numPtr *int
	if hasParsed {
		numPtr = &parsedNum
	} else if prView != nil && prView.Number != nil {
		numPtr = prView.Number
	}
	headerTitle := title
	if prView != nil && prView.Title != "" {
		headerTitle = prView.Title
	}
	if headerTitle == "" {
		headerTitle = "Untitled"
	}
	header := "# Created Pull Request: " + headerTitle
	if numPtr != nil {
		header = fmt.Sprintf("# Created Pull Request #%d: %s", *numPtr, headerTitle)
	}
	lines := []string{header, ""}
	u := prURL
	if u == "" && prView != nil {
		u = prView.URL
	}
	pushLine(&lines, "URL", u)
	if prView != nil {
		pushLine(&lines, "State", prView.State)
		if prView.IsDraft != nil {
			pushLineBool(&lines, "Draft", prView.IsDraft)
		} else {
			pushLineBool(&lines, "Draft", &draft)
		}
		b := prView.BaseRefName
		if b == "" {
			b = base
		}
		h := prView.HeadRefName
		if h == "" {
			h = head
		}
		pushLine(&lines, "Base", b)
		pushLine(&lines, "Head", h)
		pushLine(&lines, "Author", formatAuthor(prView.Author))
		pushLine(&lines, "Created", prView.CreatedAt)
		pushLine(&lines, "Labels", formatLabels(prView.Labels))
	} else {
		pushLineBool(&lines, "Draft", &draft)
		pushLine(&lines, "Base", base)
		pushLine(&lines, "Head", head)
	}

	var details any
	if meta := metaWithSourceURL(u); meta != nil {
		details = GhToolDetails{Meta: meta}
	}
	return sdk.ToolResult{
		Content: strings.TrimSpace(strings.Join(lines, "\n")),
		Details: details,
	}, nil
}
