package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// upstream: packages/coding-agent/src/task/worktree.ts,
// packages/coding-agent/src/task/isolation-runner.ts

const (
	// taskBranchPrefix is the git branch namespace for isolated task branches.
	taskBranchPrefix = "omp-task/"
)

// repoLocks serializes mutations (git apply / branch creation / cherry-pick)
// against the same parent git repository across concurrent isolated subagents.
// upstream: packages/coding-agent/src/utils/repo-lock.ts
var (
	repoLocksMu sync.Mutex
	repoLocks   = map[string]*sync.Mutex{}
)

func lockRepo(repoRoot string) func() {
	key := filepath.Clean(repoRoot)
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	repoLocksMu.Lock()
	mu, ok := repoLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		repoLocks[key] = mu
	}
	repoLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// runGit executes git with explicit arguments (never via a shell) in dir.
func runGit(ctx context.Context, dir string, extraEnv []string, stdin []byte, args ...string) (stdout, stderr string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func formatGitError(op string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg != "" {
		return fmt.Errorf("%s: %s", op, msg)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// WorktreeBaseline captures the parent repository's pre-spawn uncommitted state
// so baseline edits are visible in the isolated worktree and excluded from the
// child's delta patch/branch.
// upstream: packages/coding-agent/src/task/worktree.ts:35-42
type WorktreeBaseline struct {
	RepoRoot       string
	HeadCommit     string
	StagedPatch    string
	UnstagedPatch  string
	UntrackedFiles []string
	// BaselineTree is the synthetic tree SHA representing HEAD + uncommitted WIP
	// inside the parent repo's ODB (written via a temporary index file).
	BaselineTree string
}

// IsolatedWorktree represents a live git worktree created for one subagent run.
type IsolatedWorktree struct {
	RepoRoot     string
	WorktreeDir  string
	Baseline     WorktreeBaseline
	cleanupOnce  sync.Once
	cleanupError error
}

// IsolationCapture describes the captured outcome of an isolated subagent run.
type IsolationCapture struct {
	PatchPath      string
	HasRootChanges bool
	BranchName     string
	BranchBaseSHA  string
	Applied        bool
	ApplySummary   string
}

// ResolveGitRepoRoot resolves the git work-tree root owning cwd.
// Returns a clear error when cwd is outside any git repository, inside a pure
// Jujutsu workspace, or inside a bare repository.
// upstream: packages/coding-agent/src/task/worktree.ts:59-90
func ResolveGitRepoRoot(ctx context.Context, cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("Isolated execution requires a git repository")
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", errors.New("Isolated execution requires a git repository")
	}
	// Reject pure Jujutsu workspaces (`.jj` without colocated `.git`) at their root.
	if isPureJJWorkspace(absCwd) {
		return "", errors.New("Isolated execution requires a git repository (workspace is pure Jujutsu without colocated .git)")
	}
	insideOut, _, err := runGit(ctx, absCwd, nil, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(insideOut) != "true" {
		return "", errors.New("Isolated execution requires a git repository")
	}
	rootOut, _, err := runGit(ctx, absCwd, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("Isolated execution requires a git repository")
	}
	root := strings.TrimSpace(rootOut)
	if root == "" {
		return "", errors.New("Isolated execution requires a git repository")
	}
	return filepath.Clean(root), nil
}

func isPureJJWorkspace(start string) bool {
	dir := filepath.Clean(start)
	for {
		_, jjErr := os.Stat(filepath.Join(dir, ".jj"))
		_, gitErr := os.Stat(filepath.Join(dir, ".git"))
		if jjErr == nil {
			return gitErr != nil
		}
		if gitErr == nil {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// CreateIsolatedWorktree creates a detached git worktree at HEAD under a safe
// temporary directory, propagates any uncommitted working-tree state (staged,
// unstaged, and untracked files) from repoRoot, and records the baseline tree
// snapshot so only the child's delta is later captured.
// upstream: packages/coding-agent/src/task/worktree.ts:260-370,666-716
func CreateIsolatedWorktree(ctx context.Context, cwd string) (*IsolatedWorktree, error) {
	repoRoot, err := ResolveGitRepoRoot(ctx, cwd)
	if err != nil {
		return nil, err
	}
	headOut, headErr, err := runGit(ctx, repoRoot, nil, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, formatGitError("isolated execution requires at least one git commit (HEAD)", err, headErr)
	}
	headCommit := strings.TrimSpace(headOut)

	parentTmp, err := os.MkdirTemp("", "omp-worktree-*")
	if err != nil {
		return nil, fmt.Errorf("create worktree temp dir: %w", err)
	}
	// Use a child path inside the temp dir so `git worktree add` creates a fresh
	// checkout directory and never collides with an already-existing empty dir.
	worktreeDir := filepath.Join(parentTmp, "wt")

	if _, stderr, err := runGit(ctx, repoRoot, nil, nil, "worktree", "add", "--detach", "--", worktreeDir, headCommit); err != nil {
		_ = os.RemoveAll(parentTmp)
		return nil, formatGitError("git worktree add", err, stderr)
	}

	wt := &IsolatedWorktree{
		RepoRoot:    repoRoot,
		WorktreeDir: worktreeDir,
		Baseline: WorktreeBaseline{
			RepoRoot:   repoRoot,
			HeadCommit: headCommit,
		},
	}

	// Capture parent uncommitted WIP and materialize it inside the worktree so
	// the child sees the parent's current working tree.
	if err := wt.propagateBaseline(ctx); err != nil {
		_ = wt.Cleanup(context.Background())
		return nil, err
	}
	// Record the synthetic tree representing the exact starting state inside
	// the worktree (HEAD + baseline WIP).
	baseTree, err := writeWorkingTreeObject(ctx, wt.WorktreeDir)
	if err != nil {
		_ = wt.Cleanup(context.Background())
		return nil, err
	}
	wt.Baseline.BaselineTree = baseTree
	return wt, nil
}

// propagateBaseline copies the parent's staged, unstaged, and untracked WIP
// into the fresh detached worktree without mutating the parent repository.
func (w *IsolatedWorktree) propagateBaseline(ctx context.Context) error {
	staged, stderr, err := runGit(ctx, w.RepoRoot, nil, nil, "diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", "HEAD", "--")
	if err != nil {
		return formatGitError("capture baseline staged diff", err, stderr)
	}
	unstaged, stderr, err := runGit(ctx, w.RepoRoot, nil, nil, "diff", "--binary", "--no-ext-diff", "--no-textconv", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", "--")
	if err != nil {
		return formatGitError("capture baseline unstaged diff", err, stderr)
	}
	untrackedOut, stderr, err := runGit(ctx, w.RepoRoot, nil, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return formatGitError("list baseline untracked files", err, stderr)
	}

	w.Baseline.StagedPatch = staged
	w.Baseline.UnstagedPatch = unstaged

	if strings.TrimSpace(staged) != "" {
		if _, stderr, err := runGit(ctx, w.WorktreeDir, nil, []byte(staged), "apply", "--index", "--binary", "--whitespace=nowarn", "-"); err != nil {
			return formatGitError("apply baseline staged diff to worktree", err, stderr)
		}
	}
	if strings.TrimSpace(unstaged) != "" {
		if _, stderr, err := runGit(ctx, w.WorktreeDir, nil, []byte(unstaged), "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
			return formatGitError("apply baseline unstaged diff to worktree", err, stderr)
		}
	}
	if untrackedOut != "" {
		parts := strings.Split(untrackedOut, "\x00")
		for _, rel := range parts {
			if rel == "" {
				continue
			}
			w.Baseline.UntrackedFiles = append(w.Baseline.UntrackedFiles, rel)
			if err := copyBaselineEntry(w.RepoRoot, w.WorktreeDir, rel); err != nil {
				return fmt.Errorf("copy baseline untracked file %q: %w", rel, err)
			}
		}
	}
	return nil
}

func copyBaselineEntry(srcRoot, dstRoot, rel string) error {
	cleanRel := filepath.FromSlash(rel)
	srcPath := filepath.Join(srcRoot, cleanRel)
	dstPath := filepath.Join(dstRoot, cleanRel)
	// Ensure destination stays strictly within dstRoot.
	relCheck, err := filepath.Rel(dstRoot, dstPath)
	if err != nil || strings.HasPrefix(relCheck, "..") || filepath.IsAbs(relCheck) {
		return fmt.Errorf("unsafe baseline path %q", rel)
	}
	info, err := os.Lstat(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(srcPath)
		if err != nil {
			return err
		}
		_ = os.Remove(dstPath)
		return os.Symlink(target, dstPath)
	}
	if info.IsDir() {
		return os.MkdirAll(dstPath, info.Mode().Perm())
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// writeWorkingTreeObject snapshots the entire working tree of dir (tracked +
// untracked non-ignored files) into a git tree object using an ephemeral
// `GIT_INDEX_FILE` so the worktree's real index is never modified.
func writeWorkingTreeObject(ctx context.Context, dir string) (string, error) {
	tmpIdx, err := os.CreateTemp("", "omp-wt-index-*")
	if err != nil {
		return "", fmt.Errorf("create temp git index: %w", err)
	}
	idxPath := tmpIdx.Name()
	_ = tmpIdx.Close()
	// Remove empty placeholder so git initializes a fresh index file cleanly.
	_ = os.Remove(idxPath)
	defer os.Remove(idxPath)

	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, stderr, err := runGit(ctx, dir, env, nil, "read-tree", "HEAD"); err != nil {
		return "", formatGitError("git read-tree HEAD", err, stderr)
	}
	if _, stderr, err := runGit(ctx, dir, env, nil, "add", "-A", "--", "."); err != nil {
		return "", formatGitError("git add -A", err, stderr)
	}
	treeOut, stderr, err := runGit(ctx, dir, env, nil, "write-tree")
	if err != nil {
		return "", formatGitError("git write-tree", err, stderr)
	}
	treeSHA := strings.TrimSpace(treeOut)
	if treeSHA == "" {
		return "", errors.New("git write-tree returned empty tree SHA")
	}
	return treeSHA, nil
}

// CaptureDeltaDiff returns the binary unified diff of ONLY the child's changes
// relative to the worktree's initial baseline snapshot (including newly created
// untracked files, edits, deletions, and commits made inside the worktree, while
// excluding pre-existing uncommitted parent WIP).
// upstream: packages/coding-agent/src/task/worktree.ts:372-396
func (w *IsolatedWorktree) CaptureDeltaDiff(ctx context.Context) (string, error) {
	currentTree, err := writeWorkingTreeObject(ctx, w.WorktreeDir)
	if err != nil {
		return "", err
	}
	if currentTree == w.Baseline.BaselineTree {
		return "", nil
	}
	diffOut, stderr, err := runGit(ctx, w.WorktreeDir, nil, nil, "diff-tree", "-p", "--binary", "--no-renames", w.Baseline.BaselineTree, currentTree)
	if err != nil {
		return "", formatGitError("git diff-tree", err, stderr)
	}
	return diffOut, nil
}

// FinalizePatch captures the child's delta patch, writes it to
// `<artifactsDir>/<agentID>.patch` (or a temp file if artifactsDir is empty),
// and optionally applies it cleanly to the parent repo (checking first with
// `git apply --check` so failed applies never leave half-applied state).
// upstream: packages/coding-agent/src/task/isolation-runner.ts:278-297,850-955
func (w *IsolatedWorktree) FinalizePatch(ctx context.Context, artifactsDir, agentID string, apply bool) (IsolationCapture, error) {
	diffText, err := w.CaptureDeltaDiff(ctx)
	if err != nil {
		return IsolationCapture{}, err
	}
	hasChanges := strings.TrimSpace(diffText) != ""

	patchPath, err := writePatchArtifact(artifactsDir, agentID, diffText)
	if err != nil {
		return IsolationCapture{}, err
	}

	capRes := IsolationCapture{
		PatchPath:      patchPath,
		HasRootChanges: hasChanges,
	}
	if !hasChanges {
		capRes.Applied = apply
		if apply {
			capRes.ApplySummary = "No changes to apply."
		} else {
			capRes.ApplySummary = "Isolation: no changes captured."
		}
		return capRes, nil
	}
	if !apply {
		capRes.ApplySummary = fmt.Sprintf("Isolation: changes captured at `%s` (apply=false). Not applied.", patchPath)
		return capRes, nil
	}

	if err := ApplyPatchCleanly(ctx, w.RepoRoot, diffText); err != nil {
		capRes.Applied = false
		capRes.ApplySummary = fmt.Sprintf("Patches were not applied (%v). Patch artifact: %s", err, patchPath)
		return capRes, err
	}
	capRes.Applied = true
	capRes.ApplySummary = "Applied patches: yes"
	return capRes, nil
}

// ApplyPatchCleanly validates patchText against repoRoot with `git apply --check`
// before applying it, holding a per-repo lock to prevent concurrent patch races.
// Returns an error without mutating repoRoot if the patch conflicts.
// upstream: packages/coding-agent/src/task/isolation-runner.ts:928-955
func ApplyPatchCleanly(ctx context.Context, repoRoot, patchText string) error {
	if strings.TrimSpace(patchText) == "" {
		return nil
	}
	if !strings.HasSuffix(patchText, "\n") {
		patchText += "\n"
	}
	unlock := lockRepo(repoRoot)
	defer unlock()

	// Pre-flight check ensures atomic refusal on conflict (no half-applied state).
	if _, stderr, err := runGit(ctx, repoRoot, nil, []byte(patchText), "apply", "--check", "--binary", "--whitespace=nowarn", "-"); err != nil {
		return formatGitError("git apply conflict", err, stderr)
	}
	if _, stderr, err := runGit(ctx, repoRoot, nil, []byte(patchText), "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
		return formatGitError("git apply", err, stderr)
	}
	return nil
}

// FinalizeBranch commits the child's delta onto a dedicated branch
// `omp-task/<agentID>` in the parent repository so the branch survives worktree
// removal. When there are no changes, no branch is created.
// upstream: packages/coding-agent/src/task/worktree.ts:1020-1085
func (w *IsolatedWorktree) FinalizeBranch(ctx context.Context, agentID, description string) (IsolationCapture, error) {
	diffText, err := w.CaptureDeltaDiff(ctx)
	if err != nil {
		return IsolationCapture{}, err
	}
	if strings.TrimSpace(diffText) == "" {
		return IsolationCapture{
			HasRootChanges: false,
			ApplySummary:   "No changes to apply.",
		}, nil
	}

	branchName := taskBranchPrefix + sanitizeBranchSegment(agentID)
	commitMsg := strings.TrimSpace(description)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("omp-task: %s", agentID)
	}

	// Build the commit tree on top of HeadCommit using a temporary index so
	// neither the parent checkout nor the worktree index is disturbed.
	treeSHA, err := applyDeltaToSyntheticTree(ctx, w.RepoRoot, w.Baseline.HeadCommit, diffText, w.Baseline)
	if err != nil {
		return IsolationCapture{}, err
	}

	unlock := lockRepo(w.RepoRoot)
	defer unlock()

	env := ensureGitCommitIdentityEnv(ctx, w.RepoRoot)
	commitOut, stderr, err := runGit(ctx, w.RepoRoot, env, []byte(commitMsg+"\n"), "commit-tree", treeSHA, "-p", w.Baseline.HeadCommit)
	if err != nil {
		return IsolationCapture{}, formatGitError("git commit-tree", err, stderr)
	}
	commitSHA := strings.TrimSpace(commitOut)
	if commitSHA == "" {
		return IsolationCapture{}, errors.New("git commit-tree returned empty commit SHA")
	}

	if _, stderr, err := runGit(ctx, w.RepoRoot, nil, nil, "branch", "-f", "--", branchName, commitSHA); err != nil {
		return IsolationCapture{}, formatGitError("git branch", err, stderr)
	}
	return IsolationCapture{
		HasRootChanges: true,
		BranchName:     branchName,
		BranchBaseSHA:  w.Baseline.HeadCommit,
		ApplySummary:   fmt.Sprintf("Isolation: changes captured on branch `%s`.", branchName),
	}, nil
}

// applyDeltaToSyntheticTree applies diffText to baseCommit inside a temporary
// index and returns the resulting tree SHA. Falls back to `--3way` and then
// baseline-WIP-seeded synthesis if the delta touched lines adjacent to dirty
// baseline edits.
func applyDeltaToSyntheticTree(ctx context.Context, repoRoot, baseCommit, diffText string, baseline WorktreeBaseline) (string, error) {
	if !strings.HasSuffix(diffText, "\n") {
		diffText += "\n"
	}
	if tree, err := synthTreeWithPatches(ctx, repoRoot, baseCommit, []string{diffText}, false); err == nil {
		return tree, nil
	}
	if tree, err := synthTreeWithPatches(ctx, repoRoot, baseCommit, []string{diffText}, true); err == nil {
		return tree, nil
	}
	// If baseline had uncommitted edits overlapping delta hunks, synthesize
	// against the baseline tree itself so the branch still captures the delta.
	if baseline.BaselineTree != "" {
		if tree, err := synthTreeWithPatches(ctx, repoRoot, baseline.BaselineTree, []string{diffText}, false); err == nil {
			return tree, nil
		}
	}
	return "", errors.New("failed to synthesize commit tree from isolated delta patch")
}

func synthTreeWithPatches(ctx context.Context, repoRoot, baseTreeish string, patches []string, threeWay bool) (string, error) {
	tmpIdx, err := os.CreateTemp("", "omp-synth-index-*")
	if err != nil {
		return "", err
	}
	idxPath := tmpIdx.Name()
	_ = tmpIdx.Close()
	_ = os.Remove(idxPath)
	defer os.Remove(idxPath)

	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, stderr, err := runGit(ctx, repoRoot, env, nil, "read-tree", baseTreeish); err != nil {
		return "", formatGitError("git read-tree", err, stderr)
	}
	for _, p := range patches {
		if strings.TrimSpace(p) == "" {
			continue
		}
		args := []string{"apply", "--cached", "--binary", "--whitespace=nowarn"}
		if threeWay {
			args = append(args, "--3way")
		}
		args = append(args, "-")
		if _, stderr, err := runGit(ctx, repoRoot, env, []byte(p), args...); err != nil {
			return "", formatGitError("git apply --cached", err, stderr)
		}
	}
	treeOut, stderr, err := runGit(ctx, repoRoot, env, nil, "write-tree")
	if err != nil {
		return "", formatGitError("git write-tree", err, stderr)
	}
	return strings.TrimSpace(treeOut), nil
}

// ensureGitCommitIdentityEnv returns fallback GIT_AUTHOR_* / GIT_COMMITTER_*
// env vars only when the repository/environment does not already configure a
// git user.name or user.email (e.g. clean CI or test temp homes).
func ensureGitCommitIdentityEnv(ctx context.Context, repoRoot string) []string {
	var env []string
	nameOut, _, nameErr := runGit(ctx, repoRoot, nil, nil, "config", "--get", "user.name")
	if nameErr != nil || strings.TrimSpace(nameOut) == "" {
		if os.Getenv("GIT_AUTHOR_NAME") == "" {
			env = append(env, "GIT_AUTHOR_NAME=oh-my-pig")
		}
		if os.Getenv("GIT_COMMITTER_NAME") == "" {
			env = append(env, "GIT_COMMITTER_NAME=oh-my-pig")
		}
	}
	emailOut, _, emailErr := runGit(ctx, repoRoot, nil, nil, "config", "--get", "user.email")
	if emailErr != nil || strings.TrimSpace(emailOut) == "" {
		if os.Getenv("GIT_AUTHOR_EMAIL") == "" {
			env = append(env, "GIT_AUTHOR_EMAIL=oh-my-pig@localhost")
		}
		if os.Getenv("GIT_COMMITTER_EMAIL") == "" {
			env = append(env, "GIT_COMMITTER_EMAIL=oh-my-pig@localhost")
		}
	}
	return env
}

// Cleanup removes the temporary git worktree registration and deletes its
// directory. Safe to call multiple times (idempotent) and in defer blocks.
func (w *IsolatedWorktree) Cleanup(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.cleanupOnce.Do(func() {
		if ctx == nil || ctx.Err() != nil {
			ctx = context.Background()
		}
		if w.RepoRoot != "" && w.WorktreeDir != "" {
			if _, stderr, err := runGit(ctx, w.RepoRoot, nil, nil, "worktree", "remove", "--force", "--", w.WorktreeDir); err != nil {
				w.cleanupError = formatGitError("git worktree remove", err, stderr)
			}
			// Prune stale admin entries if removal needed force cleanup.
			_, _, _ = runGit(ctx, w.RepoRoot, nil, nil, "worktree", "prune")
		}
		if w.WorktreeDir != "" {
			parentTmp := filepath.Dir(w.WorktreeDir)
			if err := os.RemoveAll(parentTmp); err != nil && w.cleanupError == nil {
				w.cleanupError = err
			}
		}
	})
	return w.cleanupError
}

func writePatchArtifact(artifactsDir, agentID, patchText string) (string, error) {
	safeID := sanitizeBranchSegment(agentID)
	if safeID == "" {
		safeID = "task"
	}
	if artifactsDir != "" {
		if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
			return "", fmt.Errorf("create artifacts dir: %w", err)
		}
		dest := filepath.Join(artifactsDir, safeID+".patch")
		if err := os.WriteFile(dest, []byte(patchText), 0o644); err != nil {
			return "", fmt.Errorf("write patch artifact: %w", err)
		}
		return dest, nil
	}
	tmp, err := os.CreateTemp("", "omp-task-"+safeID+"-*.patch")
	if err != nil {
		return "", fmt.Errorf("create temp patch artifact: %w", err)
	}
	dest := tmp.Name()
	if _, err := tmp.WriteString(patchText); err != nil {
		_ = tmp.Close()
		_ = os.Remove(dest)
		return "", fmt.Errorf("write temp patch artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("close temp patch artifact: %w", err)
	}
	return dest, nil
}

func sanitizeBranchSegment(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return "task"
	}
	return out
}

// RunIsolatedSubagent runs req inside a fresh detached git worktree, captures
// its changes (patch or branch mode), optionally applies the patch back to the
// parent repository, and always tears down the temporary worktree in defer.
// upstream: packages/coding-agent/src/task/isolation-runner.ts:494-737
func RunIsolatedSubagent(
	ctx context.Context,
	reg *Registry,
	req RunRequest,
	mergeMode string,
	applyChanges bool,
) (res SingleResult, mergeSummary string) {
	wt, err := CreateIsolatedWorktree(ctx, req.Cwd)
	if err != nil {
		if reg != nil {
			reg.SettleIfRunning(req.ParentSessionID, req.ID, "aborted")
		}
		return SingleResult{
			Index:         req.Index,
			ID:            req.ID,
			Agent:         req.Agent.Name,
			AgentSource:   req.Agent.Source,
			Task:          RenderSubagentUserPrompt(req.Assignment),
			Assignment:    req.Assignment,
			ExitCode:      1,
			Stderr:        err.Error(),
			Error:         err.Error(),
			ModelOverride: req.ModelOverride,
			Isolated:      true,
		}, ""
	}
	retainWorktree := false
	defer func() {
		if !retainWorktree {
			_ = wt.Cleanup(context.Background())
		}
	}()

	isoReq := req
	isoReq.Cwd = wt.WorktreeDir
	isoReq.Worktree = wt.WorktreeDir
	// Isolated subagents cannot be resumed/messaged after the worktree is removed.
	isoReq.KeepAlive = false

	res = RunSubagent(ctx, reg, isoReq)
	res.Isolated = true

	// Skip change capture when the subagent failed or was aborted.
	if res.Aborted || res.ExitCode != 0 || res.Error != "" {
		return res, ""
	}

	mode := strings.ToLower(strings.TrimSpace(mergeMode))
	if mode == "" {
		mode = "patch"
	}

	switch mode {
	case "branch":
		capRes, capErr := wt.FinalizeBranch(context.Background(), req.ID, req.Assignment)
		hasChanges := capRes.HasRootChanges
		res.HasRootChanges = &hasChanges
		res.BranchName = capRes.BranchName
		res.BranchBaseSHA = capRes.BranchBaseSHA
		if capErr != nil {
			// Preserve the child's work by falling back to a patch artifact (or
			// retaining the worktree directory if patch capture also fails).
			if diffText, dErr := wt.CaptureDeltaDiff(context.Background()); dErr == nil && strings.TrimSpace(diffText) != "" {
				if patchPath, pErr := writePatchArtifact(req.ArtifactsDir, req.ID, diffText); pErr == nil {
					hasChanges = true
					res.HasRootChanges = &hasChanges
					res.PatchPath = patchPath
					msg := fmt.Sprintf("Branch capture failed (%v). Saved fallback patch artifact: %s", capErr, patchPath)
					res.Error = msg
					return res, msg
				}
			}
			retainWorktree = true
			msg := fmt.Sprintf("Branch capture failed (%v). Retained worktree at: %s", capErr, wt.WorktreeDir)
			res.Error = msg
			return res, msg
		}
		return res, capRes.ApplySummary
	default:
		capRes, capErr := wt.FinalizePatch(context.Background(), req.ArtifactsDir, req.ID, applyChanges)
		hasChanges := capRes.HasRootChanges
		res.PatchPath = capRes.PatchPath
		res.HasRootChanges = &hasChanges
		if capErr != nil {
			res.Error = fmt.Sprintf("Patch apply failed: %v", capErr)
			return res, capRes.ApplySummary
		}
		return res, capRes.ApplySummary
	}
}
