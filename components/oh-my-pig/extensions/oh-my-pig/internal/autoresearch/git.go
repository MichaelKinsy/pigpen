package autoresearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// upstream: packages/coding-agent/src/autoresearch/git.ts:7-8
const (
	AutoresearchBranchPrefix = "autoresearch/"
	BranchNameMaxLength      = 48
)

// EnsureBranchResult is the outcome of EnsureAutoresearchBranch.
// upstream: packages/coding-agent/src/autoresearch/git.ts:10-22
type EnsureBranchResult struct {
	OK         bool
	BranchName *string
	Created    bool
	Warning    string
	Error      string
}

// DirtyPathEntry classifies a porcelain dirty path as tracked or untracked.
// upstream: packages/coding-agent/src/autoresearch/git.ts:232-235
type DirtyPathEntry struct {
	Path      string
	Untracked bool
}

// gitEnv strips only the ambient GIT_DIR family so repo discovery always targets
// workDir while preserving GIT_AUTHOR_*, GIT_COMMITTER_*, and GIT_CONFIG_* overrides.
func gitEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") ||
			strings.HasPrefix(kv, "GIT_WORK_TREE=") ||
			strings.HasPrefix(kv, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY=") ||
			strings.HasPrefix(kv, "GIT_ALTERNATE_OBJECT_DIRECTORIES=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	return env
}

// runGit runs `git -C <workDir> <args...>` directly (never through a shell)
// with stripped ambient GIT_* env vars.
func runGit(ctx context.Context, workDir string, args ...string) (string, string, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmdArgs := make([]string, 0, len(args)+2)
	cmdArgs = append(cmdArgs, "-C", workDir)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return stdout.String(), stderr.String(), -1, err
		}
	}
	return stdout.String(), stderr.String(), exitCode, nil
}

// findUpwardDir walks up from start looking for a directory/file named target.
// Returns the directory containing target, or "" if not found.
func findUpwardDir(start, target string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, target)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isPureJj reports whether workDir is inside a Jujutsu workspace (`.jj/`)
// whose nearest VCS root has no colocated `.git/`.
// Matches upstream `vcs.isPureJj(workDir)` semantics tested in autoresearch-git.test.ts:
//   - pure `.jj` at root -> true
//   - colocated `.jj` + `.git` at same root -> false
//   - nested `.jj` inside outer `.git` -> true (nearest `.jj` is deeper than outer `.git`)
//   - nested `.git` inside outer `.jj` -> false (nearest `.git` is deeper than outer `.jj`)
//
// upstream: packages/coding-agent/src/autoresearch/git.ts:46-51
func isPureJj(workDir string) bool {
	jjRoot := findUpwardDir(workDir, ".jj")
	if jjRoot == "" {
		return false
	}
	gitRoot := findUpwardDir(workDir, ".git")
	if gitRoot == "" {
		return true
	}
	// If gitRoot is at the same level (colocated) or deeper (nested git repo inside jj),
	// it is NOT pure jj.
	if gitRoot == jjRoot {
		return false
	}
	rel, err := filepath.Rel(jjRoot, gitRoot)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// gitRoot is inside jjRoot -> nested git checkout under outer jj workspace
		return false
	}
	// jjRoot is deeper than gitRoot -> pure jj nested inside an outer git checkout
	return true
}

// GitRepoRoot returns the top-level git worktree root for workDir, or "" if not in a git repo.
func GitRepoRoot(ctx context.Context, workDir string) string {
	stdout, _, code, err := runGit(ctx, workDir, "rev-parse", "--show-toplevel")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// GitCurrentBranch returns the current git branch name, or "" if detached or not a git repo.
func GitCurrentBranch(ctx context.Context, workDir string) string {
	stdout, _, code, err := runGit(ctx, workDir, "branch", "--show-current")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// GitHeadSHA returns the current HEAD commit SHA, or "" if unavailable.
func GitHeadSHA(ctx context.Context, workDir string) string {
	stdout, _, code, err := runGit(ctx, workDir, "rev-parse", "HEAD")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// GitStatusPorcelain runs `git status --porcelain=v1 --untracked-files=all -z`.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:241-247
func GitStatusPorcelain(ctx context.Context, workDir string) (string, error) {
	stdout, stderr, code, err := runGit(ctx, workDir, "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git status failed (%d): %s", code, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// TryGitStatus returns porcelain status output or "" on error.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:241-247
func TryGitStatus(ctx context.Context, workDir string) string {
	out, err := GitStatusPorcelain(ctx, workDir)
	if err != nil {
		return ""
	}
	return out
}

// TryGitPrefix returns the subdirectory prefix of workDir inside the git repo, or "".
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:249-255
func TryGitPrefix(ctx context.Context, workDir string) string {
	stdout, _, code, err := runGit(ctx, workDir, "rev-parse", "--show-prefix")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// GetCurrentAutoresearchBranch returns the active branch if it starts with `autoresearch/`, else "".
// upstream: packages/coding-agent/src/autoresearch/git.ts:24-27
func GetCurrentAutoresearchBranch(ctx context.Context, workDir string) string {
	branch := GitCurrentBranch(ctx, workDir)
	if strings.HasPrefix(branch, AutoresearchBranchPrefix) {
		return branch
	}
	return ""
}

// EnsureAutoresearchBranch ensures workDir is on a dedicated `autoresearch/*` branch when possible.
// upstream: packages/coding-agent/src/autoresearch/git.ts:37-99
func EnsureAutoresearchBranch(ctx context.Context, workDir string, goal *string) EnsureBranchResult {
	if isPureJj(workDir) {
		return EnsureBranchResult{
			OK:    false,
			Error: "Autoresearch needs a Git checkout for branch isolation and baseline commits, but this workspace is pure Jujutsu (`.jj/` without a colocated `.git/`). Run `jj git init --colocate` to add a Git checkout before starting autoresearch.",
		}
	}

	if GitRepoRoot(ctx, workDir) == "" {
		return EnsureBranchResult{
			OK:         true,
			BranchName: nil,
			Created:    false,
			Warning:    "Not in a git repository — autoresearch will run without branch isolation, baseline reset, or auto-commits.",
		}
	}

	dirtyPathsOutput, err := GitStatusPorcelain(ctx, workDir)
	if err != nil {
		return EnsureBranchResult{
			OK:    false,
			Error: fmt.Sprintf("Unable to inspect git status before starting autoresearch: %v", err),
		}
	}

	workDirPrefix := TryGitPrefix(ctx, workDir)
	dirtyPaths := collectRelativeDirtyPaths(dirtyPathsOutput, workDirPrefix)
	branch := GitCurrentBranch(ctx, workDir)
	if strings.HasPrefix(branch, AutoresearchBranchPrefix) {
		b := branch
		return EnsureBranchResult{OK: true, BranchName: &b, Created: false}
	}
	if len(dirtyPaths) > 0 {
		preview := formatDirtyPaths(dirtyPaths)
		return EnsureBranchResult{
			OK:    false,
			Error: fmt.Sprintf("Worktree is dirty (%s). Commit or stash these changes before starting autoresearch — a fresh autoresearch/* branch needs a clean baseline.", preview),
		}
	}

	goalText := ""
	if goal != nil {
		goalText = *goal
	}
	branchName := allocateBranchName(ctx, workDir, goalText, time.Now())
	_, stderr, code, err := runGit(ctx, workDir, "checkout", "-b", branchName)
	if err != nil || code != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return EnsureBranchResult{
			OK:    false,
			Error: fmt.Sprintf("Failed to create autoresearch branch %s: %s", branchName, msg),
		}
	}
	return EnsureBranchResult{OK: true, BranchName: &branchName, Created: true}
}

// allocateBranchName constructs `autoresearch/<slug>-YYYYMMDD[-N]`.
// upstream: packages/coding-agent/src/autoresearch/git.ts:187-205
func allocateBranchName(ctx context.Context, workDir, goal string, now time.Time) string {
	goalSlug := Slugify(goal, BranchNameMaxLength)
	if goalSlug == "" {
		goalSlug = "session"
	}
	baseName := fmt.Sprintf("%s%s-%s", AutoresearchBranchPrefix, goalSlug, now.Format("20060102"))
	candidate := baseName
	suffix := 2
	for gitRefExists(ctx, workDir, "refs/heads/"+candidate) {
		candidate = fmt.Sprintf("%s-%d", baseName, suffix)
		suffix++
	}
	return candidate
}

func gitRefExists(ctx context.Context, workDir, refName string) bool {
	_, _, code, err := runGit(ctx, workDir, "show-ref", "--verify", "--quiet", refName)
	return err == nil && code == 0
}

// gitStageAll stages all changes in workDir (`git add -A`).
func gitStageAll(ctx context.Context, workDir string) error {
	_, stderr, code, err := runGit(ctx, workDir, "add", "-A")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	return nil
}

// gitStageFiles stages specific files (`git add -- <files...>`), or all if files is empty.
func gitStageFiles(ctx context.Context, workDir string, files []string) error {
	if len(files) == 0 {
		return gitStageAll(ctx, workDir)
	}
	args := make([]string, 0, len(files)+2)
	args = append(args, "add", "--")
	args = append(args, files...)
	_, stderr, code, err := runGit(ctx, workDir, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	return nil
}

// gitHasCachedDiff reports whether there are staged changes for the given files (or anywhere if empty).
func gitHasCachedDiff(ctx context.Context, workDir string, files []string) bool {
	args := []string{"diff", "--cached", "--quiet"}
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	_, _, code, err := runGit(ctx, workDir, args...)
	if err != nil {
		return false
	}
	return code != 0
}

// gitCommit creates a commit with message, optionally scoped to files (`git commit -m <msg> -- <files...>`).
func gitCommit(ctx context.Context, workDir, message string, files []string) (string, error) {
	args := []string{"commit", "-m", message}
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	_, stderr, code, err := runGit(ctx, workDir, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	return GitHeadSHA(ctx, workDir), nil
}

// gitResetHardAndClean resets workDir hard to target (e.g. "HEAD" or baseline SHA) and cleans untracked files.
func gitResetHardAndClean(ctx context.Context, workDir, target string) error {
	_, stderr, code, err := runGit(ctx, workDir, "reset", "--hard", target)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git reset --hard %s: %s", target, strings.TrimSpace(stderr))
	}
	_, stderr, code, err = runGit(ctx, workDir, "clean", "-fd")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git clean -fd: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// gitRestoreTracked restores tracked files in both index and worktree from HEAD.
func gitRestoreTracked(ctx context.Context, workDir string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	args := make([]string, 0, len(files)+5)
	args = append(args, "restore", "--source=HEAD", "--staged", "--worktree", "--")
	args = append(args, files...)
	_, stderr, code, err := runGit(ctx, workDir, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	return nil
}

// ParseWorkDirDirtyPaths parses git status output and relativizes paths to workDirPrefix.
// upstream: packages/coding-agent/src/autoresearch/git.ts:101-109
func ParseWorkDirDirtyPaths(statusOutput, workDirPrefix string) []string {
	var relativePaths []string
	for _, dirtyPath := range ParseDirtyPaths(statusOutput) {
		rel, ok := RelativizeGitPathToWorkDir(dirtyPath, workDirPrefix)
		if !ok {
			continue
		}
		relativePaths = append(relativePaths, rel)
	}
	if relativePaths == nil {
		return []string{}
	}
	return relativePaths
}

// RelativizeGitPathToWorkDir strips workDirPrefix from a repo-relative path.
// upstream: packages/coding-agent/src/autoresearch/git.ts:111-124
func RelativizeGitPathToWorkDir(repoRelativePath, workDirPrefix string) (string, bool) {
	normalizedPath := NormalizeStatusPath(repoRelativePath)
	normalizedPrefix := NormalizePathSpec(workDirPrefix)
	if normalizedPrefix == "" || normalizedPrefix == "." {
		return normalizedPath, true
	}
	if normalizedPath == normalizedPrefix {
		return ".", true
	}
	if !strings.HasPrefix(normalizedPath, normalizedPrefix+"/") {
		return "", false
	}
	return NormalizePathSpec(normalizedPath[len(normalizedPrefix)+1:]), true
}

// ParseDirtyPaths parses porcelain v1 output (either NUL-terminated or newline-terminated).
// upstream: packages/coding-agent/src/autoresearch/git.ts:135-177
func ParseDirtyPaths(statusOutput string) []string {
	entries := ParseDirtyPathsWithStatus(statusOutput)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

// NormalizeStatusPath unquotes and normalizes a path from git status output.
// upstream: packages/coding-agent/src/autoresearch/git.ts:179-185
func NormalizeStatusPath(rawPath string) string {
	normalized := strings.TrimSpace(rawPath)
	if len(normalized) >= 2 && strings.HasPrefix(normalized, "\"") && strings.HasSuffix(normalized, "\"") {
		normalized = normalized[1 : len(normalized)-1]
	}
	return NormalizePathSpec(normalized)
}

func isRenameOrCopy(statusToken string) bool {
	trimmed := strings.TrimSpace(statusToken)
	return strings.HasPrefix(trimmed, "R") || strings.HasPrefix(trimmed, "C")
}

func collectRelativeDirtyPaths(statusOutput, workDirPrefix string) []string {
	var dirtyPaths []string
	for _, dirtyPath := range ParseDirtyPaths(statusOutput) {
		if rel, ok := RelativizeGitPathToWorkDir(dirtyPath, workDirPrefix); ok {
			dirtyPaths = append(dirtyPaths, rel)
		} else {
			dirtyPaths = append(dirtyPaths, NormalizeStatusPath(dirtyPath))
		}
	}
	return dirtyPaths
}

func formatDirtyPaths(paths []string) string {
	limit := 5
	if len(paths) <= limit {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(paths[:limit], ", "), len(paths)-limit)
}

// ParseDirtyPathsWithStatus parses git porcelain status into DirtyPathEntry records.
// upstream: packages/coding-agent/src/autoresearch/git.ts:237-291
func ParseDirtyPathsWithStatus(statusOutput string) []DirtyPathEntry {
	if strings.ContainsRune(statusOutput, 0) {
		return parseDirtyPathsNulWithStatus(statusOutput)
	}
	return parseDirtyPathsLinesWithStatus(statusOutput)
}

func parseDirtyPathsNulWithStatus(statusOutput string) []DirtyPathEntry {
	seen := make(map[string]bool)
	var results []DirtyPathEntry
	index := 0
	for index+3 <= len(statusOutput) {
		statusToken := statusOutput[index : index+3]
		index += 3
		relEnd := strings.IndexByte(statusOutput[index:], 0)
		if relEnd < 0 {
			break
		}
		firstPath := statusOutput[index : index+relEnd]
		index += relEnd + 1
		untracked := strings.HasPrefix(strings.TrimSpace(statusToken), "??")
		addDirtyPathEntry(seen, &results, firstPath, untracked)
		if isRenameOrCopy(statusToken) {
			secondEnd := strings.IndexByte(statusOutput[index:], 0)
			if secondEnd < 0 {
				break
			}
			secondPath := statusOutput[index : index+secondEnd]
			index += secondEnd + 1
			addDirtyPathEntry(seen, &results, secondPath, false)
		}
	}
	return results
}

func parseDirtyPathsLinesWithStatus(statusOutput string) []DirtyPathEntry {
	seen := make(map[string]bool)
	var results []DirtyPathEntry
	for _, line := range strings.Split(statusOutput, "\n") {
		trimmedLine := strings.TrimRight(line, " \t\r")
		if len(trimmedLine) < 4 {
			continue
		}
		statusToken := trimmedLine[:3]
		rawPath := strings.TrimSpace(trimmedLine[3:])
		if rawPath == "" {
			continue
		}
		untracked := strings.HasPrefix(strings.TrimSpace(statusToken), "??")
		for _, part := range strings.Split(rawPath, " -> ") {
			addDirtyPathEntry(seen, &results, part, untracked)
		}
	}
	return results
}

func addDirtyPathEntry(seen map[string]bool, results *[]DirtyPathEntry, rawPath string, untracked bool) {
	normalizedPath := NormalizeStatusPath(rawPath)
	if normalizedPath == "" || seen[normalizedPath] {
		return
	}
	seen[normalizedPath] = true
	*results = append(*results, DirtyPathEntry{Path: normalizedPath, Untracked: untracked})
}

// ParseWorkDirDirtyPathsWithStatus parses status output and relativizes each entry to workDirPrefix.
// upstream: packages/coding-agent/src/autoresearch/git.ts:293-301
func ParseWorkDirDirtyPathsWithStatus(statusOutput, workDirPrefix string) []DirtyPathEntry {
	var results []DirtyPathEntry
	for _, entry := range ParseDirtyPathsWithStatus(statusOutput) {
		rel, ok := RelativizeGitPathToWorkDir(entry.Path, workDirPrefix)
		if !ok {
			continue
		}
		results = append(results, DirtyPathEntry{Path: rel, Untracked: entry.Untracked})
	}
	return results
}

// ComputeRunModifiedPaths filters current dirty paths against preRunDirtyPaths,
// splitting newly modified paths into tracked and untracked slices.
// upstream: packages/coding-agent/src/autoresearch/git.ts:303-320
func ComputeRunModifiedPaths(preRunDirtyPaths []string, currentStatusOutput, workDirPrefix string) (tracked []string, untracked []string) {
	preRunSet := make(map[string]bool, len(preRunDirtyPaths))
	for _, p := range preRunDirtyPaths {
		preRunSet[p] = true
	}
	for _, entry := range ParseWorkDirDirtyPathsWithStatus(currentStatusOutput, workDirPrefix) {
		if preRunSet[entry.Path] {
			continue
		}
		if entry.Untracked {
			untracked = append(untracked, entry.Path)
		} else {
			tracked = append(tracked, entry.Path)
		}
	}
	return tracked, untracked
}
