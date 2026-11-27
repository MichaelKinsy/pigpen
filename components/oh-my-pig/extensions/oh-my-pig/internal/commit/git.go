package commit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// upstream: crates/vcs/src/git/{read,diff,mutate,patch,filter}.rs,
//           crates/vcs/src/jj/mod.rs,
//           packages/coding-agent/src/autoresearch/git.ts

type errHeadMoved struct {
	expected string
	actual   string
}

func (e *errHeadMoved) Error() string {
	return fmt.Sprintf("HEAD moved (expected %s, actual %s)", e.expected, e.actual)
}

func isHeadMoved(err error) bool {
	var hm *errHeadMoved
	return errors.As(err, &hm)
}

type gitRepo struct {
	root      string
	gitDir    string
	commonDir string
}

// isPureJJ reports whether dir is inside a Jujutsu workspace without a colocated git repo at the same or deeper root.
// upstream: crates/vcs/src/jj/mod.rs, crates/vcs/src/lib.rs
func isPureJJ(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	cur := abs
	for {
		_, jjErr := os.Stat(filepath.Join(cur, ".jj", "repo"))
		_, gitErr := os.Stat(filepath.Join(cur, ".git"))
		if jjErr == nil {
			return gitErr != nil
		}
		if gitErr == nil {
			return false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// discoverGitRepo locates the git repository containing dir, returning nil if not inside one.
func discoverGitRepo(ctx context.Context, dir string) (*gitRepo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := runGit(ctx, dir, nil, nil, "rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return nil, nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) < 3 {
		return nil, nil
	}
	root := filepath.Clean(strings.TrimRight(lines[0], "\r"))
	gitDir := filepath.Clean(strings.TrimRight(lines[1], "\r"))
	commonDir := strings.TrimRight(lines[2], "\r")
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Clean(filepath.Join(dir, commonDir))
	} else {
		commonDir = filepath.Clean(commonDir)
	}
	return &gitRepo{root: root, gitDir: gitDir, commonDir: commonDir}, nil
}

func runGit(ctx context.Context, dir string, extraEnv []string, stdin []byte, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Strip ambient GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE so the repo at dir is always used.
	baseEnv := make([]string, 0, len(os.Environ())+len(extraEnv)+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") ||
			strings.HasPrefix(kv, "GIT_WORK_TREE=") ||
			strings.HasPrefix(kv, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY=") ||
			strings.HasPrefix(kv, "GIT_ALTERNATE_OBJECT_DIRECTORIES=") {
			continue
		}
		baseEnv = append(baseEnv, kv)
	}
	baseEnv = append(baseEnv, "GIT_TERMINAL_PROMPT=0")
	baseEnv = append(baseEnv, extraEnv...)
	cmd.Env = baseEnv
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), &gitCLIError{args: args, msg: msg, err: err}
	}
	return stdout.Bytes(), nil
}

type gitCLIError struct {
	args []string
	msg  string
	err  error
}

func (e *gitCLIError) Error() string { return e.msg }
func (e *gitCLIError) Unwrap() error { return e.err }

func (r *gitRepo) headSHA(ctx context.Context) (string, bool, error) {
	out, err := runGit(ctx, r.root, nil, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, nil
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", false, nil
	}
	return sha, true, nil
}

func (r *gitRepo) currentBranch(ctx context.Context) (string, bool) {
	out, err := runGit(ctx, r.root, nil, nil, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(string(out))
	if ref == "" {
		return "", false
	}
	return strings.TrimPrefix(ref, "refs/heads/"), true
}

// LogSubjects returns up to n recent commit subjects from the git repository at root.
// Returns nil, nil when n <= 0 or stdout is empty; returns an error on unborn repos or git failure
// (callers such as /commit ignore errors and fall back to an empty list, matching builtin-commit.ts:34).
// upstream: crates/vcs/src/git/read.rs (log_subjects), packages/coding-agent/src/slash-commands/builtin-commit.ts:31-34
func LogSubjects(ctx context.Context, root string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	out, err := runGit(ctx, root, nil, nil, "log", "-n", strconv.Itoa(n), "--format=%s")
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	trimmed := strings.TrimRight(string(out), "\r\n")
	if trimmed == "" {
		return nil, nil
	}
	rawLines := strings.Split(trimmed, "\n")
	subjects := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		subjects = append(subjects, strings.TrimRight(line, "\r"))
	}
	return subjects, nil
}

func (r *gitRepo) resolveRef(ctx context.Context, spec string) (string, bool) {
	out, err := runGit(ctx, r.root, nil, nil, "rev-parse", "--verify", "--quiet", spec)
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", false
	}
	return sha, true
}

func (r *gitRepo) showBlob(ctx context.Context, spec string) ([]byte, error) {
	return runGit(ctx, r.root, nil, nil, "cat-file", "blob", spec)
}

func (r *gitRepo) writeBlob(ctx context.Context, data []byte) (string, error) {
	out, err := runGit(ctx, r.root, nil, data, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *gitRepo) statusPorcelain(ctx context.Context) (string, error) {
	out, err := runGit(ctx, r.root, nil, nil, "-c", "core.quotePath=false", "status", "--porcelain", "--untracked-files=all", "-z")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (r *gitRepo) changedFilesCached(ctx context.Context) ([]string, error) {
	_, hasHead, err := r.headSHA(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"-c", "core.quotePath=false", "diff", "--cached", "--name-only", "-z"}
	if !hasHead {
		args = []string{"-c", "core.quotePath=false", "diff", "--cached", "--name-only", "-z", emptyTreeSHA}
	}
	out, err := runGit(ctx, r.root, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	return parseNulList(string(out)), nil
}

func (r *gitRepo) changedFilesBetween(ctx context.Context, base, head string) ([]string, error) {
	out, err := runGit(ctx, r.root, nil, nil, "-c", "core.quotePath=false", "diff", "--name-only", "-z", base, head)
	if err != nil {
		return nil, err
	}
	return parseNulList(string(out)), nil
}

func (r *gitRepo) diffTextBetween(ctx context.Context, base, head, file string) (string, error) {
	out, err := runGit(ctx, r.root, nil, nil, "-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "diff", "--binary", "--full-index", "--no-renames", base, head, "--", file)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type treeEntry struct {
	path string
	mode string
	id   string
}

func (r *gitRepo) lsTree(ctx context.Context, rev string, paths []string) ([]treeEntry, error) {
	args := []string{"ls-tree", "-r", "-z", rev}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := runGit(ctx, r.root, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for rec := range strings.SplitSeq(string(out), "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Split(meta, " ")
		if len(fields) < 3 {
			continue
		}
		if fields[1] != "blob" && fields[1] != "commit" {
			continue
		}
		entries = append(entries, treeEntry{path: p, mode: fields[0], id: fields[2]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, nil
}

type commitDetails struct {
	sha        string
	parents    []string
	authorDate time.Time
	hasDate    bool
	message    string
}

func (r *gitRepo) commitDetails(ctx context.Context, rev string) (*commitDetails, error) {
	out, err := runGit(ctx, r.root, nil, nil, "show", "-s", "--format=%H%x00%P%x00%aI%x00%B", rev)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(out), "\x00", 4)
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected git show output for %s", rev)
	}
	var parents []string
	if p := strings.TrimSpace(parts[1]); p != "" {
		parents = strings.Fields(p)
	}
	var authorDate time.Time
	hasDate := false
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[2])); err == nil {
		authorDate = t
		hasDate = true
	}
	msg := strings.TrimSuffix(parts[3], "\n")
	// git show --format=%B adds a trailing newline beyond the raw commit message;
	// strip one trailing newline to match gix's message_raw().strip_suffix('\n').
	msg = strings.TrimSuffix(msg, "\n")
	return &commitDetails{
		sha:        strings.TrimSpace(parts[0]),
		parents:    parents,
		authorDate: authorDate,
		hasDate:    hasDate,
		message:    msg,
	}, nil
}

// operationKind detects an in-progress git operation or unresolved index entries.
// upstream: crates/vcs/src/git/read.rs:238
func (r *gitRepo) operationKind(ctx context.Context) (string, error) {
	if _, err := os.Stat(filepath.Join(r.gitDir, "rebase-merge")); err == nil {
		return "rebase", nil
	}
	if _, err := os.Stat(filepath.Join(r.gitDir, "rebase-apply")); err == nil {
		if _, err := os.Stat(filepath.Join(r.gitDir, "rebase-apply", "applying")); err == nil {
			return "am", nil
		}
		return "rebase", nil
	}
	for _, pair := range [][2]string{
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
	} {
		if _, err := os.Stat(filepath.Join(r.gitDir, pair[0])); err == nil {
			return pair[1], nil
		}
	}
	out, err := runGit(ctx, r.root, nil, nil, "ls-files", "--unmerged")
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(out)) > 0 {
		return "unmerged-index", nil
	}
	return "", nil
}

// worktreeToGit converts worktree bytes for each (path, data) into git's stored blob form
// (applying core.autocrlf, .gitattributes text/eol, clean filters) using an ephemeral
// temporary object directory so the repository's object database stays clean.
// upstream: crates/vcs/src/git/filter.rs:111
func (r *gitRepo) worktreeToGit(ctx context.Context, entries []worktreeInput) ([][]byte, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	// Fast path: check if autocrlf is off and no .gitattributes exists? No — global/repo
	// attributes (.git/info/attributes, core.attributesFile) can also configure filters.
	// Using git hash-object into a temporary object dir handles every git config/filter
	// faithfully; to keep performance fast for many files, check whether any conversion
	// config is active or batch via temp dir.
	tmpObj, err := os.MkdirTemp("", "omp-wt-obj-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpObj)

	altObjects := filepath.Join(r.commonDir, "objects")
	extraEnv := []string{
		"GIT_OBJECT_DIRECTORY=" + tmpObj,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + altObjects,
	}

	// Quick check: if no clean filters, no attributes files, and core.autocrlf is false/unset,
	// we can avoid spawning 2*N git processes when there are many files (e.g. 1000 untracked files).
	if len(entries) > 4 && r.noFiltersActive(ctx) {
		out := make([][]byte, len(entries))
		for i, e := range entries {
			out[i] = e.data
		}
		return out, nil
	}

	out := make([][]byte, len(entries))
	for i, entry := range entries {
		oidRaw, err := runGit(ctx, r.root, extraEnv, entry.data, "hash-object", "-w", "--path="+entry.path, "--stdin")
		if err != nil {
			return nil, err
		}
		oid := strings.TrimSpace(string(oidRaw))
		cleaned, err := runGit(ctx, r.root, extraEnv, nil, "cat-file", "blob", oid)
		if err != nil {
			return nil, err
		}
		out[i] = cleaned
	}
	return out, nil
}

type worktreeInput struct {
	path string
	data []byte
}

func (r *gitRepo) noFiltersActive(ctx context.Context) bool {
	// Check core.autocrlf and core.eol and filter.*.clean and core.attributesFile.
	if out, err := runGit(ctx, r.root, nil, nil, "config", "--get", "core.autocrlf"); err == nil {
		v := strings.ToLower(strings.TrimSpace(string(out)))
		if v == "true" || v == "input" {
			return false
		}
	}
	if out, err := runGit(ctx, r.root, nil, nil, "config", "--get-regexp", `^filter\..*\.clean$`); err == nil && len(bytes.TrimSpace(out)) > 0 {
		return false
	}
	if _, err := os.Stat(filepath.Join(r.root, ".gitattributes")); err == nil {
		return false
	}
	if infoAttr, err := os.ReadFile(filepath.Join(r.commonDir, "info", "attributes")); err == nil {
		for line := range strings.SplitSeq(string(infoAttr), "\n") {
			t := strings.TrimSpace(line)
			if t != "" && !strings.HasPrefix(t, "#") {
				return false
			}
		}
	}
	if out, err := runGit(ctx, r.root, nil, nil, "config", "--get", "core.attributesFile"); err == nil && strings.TrimSpace(string(out)) != "" {
		return false
	}
	return true
}

// diffBlobs renders a binary git patch between frozen blobs in the object database.
// upstream: crates/vcs/src/git/diff.rs:141
func (r *gitRepo) diffBlobs(ctx context.Context, path string, before, after *WholeFileState) (string, error) {
	if before == nil && after == nil {
		return "", nil
	}
	if before != nil && after != nil && before.ID == after.ID && before.Mode == after.Mode {
		return "", nil
	}
	if before != nil && after != nil && before.ID == after.ID && before.Mode != after.Mode {
		// Mode-only change: include index line naming the exact blob so `git apply --cached`
		// refuses to apply onto a diverged staged blob (matching vcs/src/git/patch.rs:3392).
		oldPath := EncodeGitDiffPath("a/" + path)
		newPath := EncodeGitDiffPath("b/" + path)
		return fmt.Sprintf("diff --git %s %s\nold mode %s\nnew mode %s\nindex %s..%s\n",
			oldPath, newPath, before.Mode, after.Mode, before.ID, after.ID), nil
	}
	makeTree := func(state *WholeFileState) (string, error) {
		if state == nil {
			return emptyTreeSHA, nil
		}
		tmpIdx := filepath.Join(r.gitDir, "omp-diff-"+randomHex(6)+".index")
		defer os.Remove(tmpIdx)
		env := []string{"GIT_INDEX_FILE=" + tmpIdx}
		// Feed via `--index-info` with C-quoted path (handles leading `-`, spaces, newlines).
		indexLine := fmt.Sprintf("%s %s\t%s\n", state.Mode, state.ID, EncodeGitDiffPath(path))
		if _, err := runGit(ctx, r.root, env, []byte(indexLine), "update-index", "--index-info"); err != nil {
			return "", err
		}
		treeOut, err := runGit(ctx, r.root, env, nil, "write-tree")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(treeOut)), nil
	}
	oldTree, err := makeTree(before)
	if err != nil {
		return "", err
	}
	newTree, err := makeTree(after)
	if err != nil {
		return "", err
	}
	out, err := runGit(ctx, r.root, nil, nil, "-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "diff", "--binary", "--full-index", "--no-renames", oldTree, newTree)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// readTreeKeepingSparse seeds indexPath from treeish while retaining the shared index's
// sparse-checkout exclusions (skip-worktree bits and sparse directory entries).
// upstream: crates/vcs/src/git/mutate.rs:684
func (r *gitRepo) readTreeKeepingSparse(ctx context.Context, treeish, indexPath string) error {
	sharedIndex := filepath.Join(r.gitDir, "index")
	if data, err := os.ReadFile(sharedIndex); err == nil {
		if err := os.WriteFile(indexPath, data, 0o644); err != nil {
			return err
		}
		env := []string{"GIT_INDEX_FILE=" + indexPath}
		if _, err := runGit(ctx, r.root, env, nil, "read-tree", "--reset", treeish); err == nil {
			return nil
		}
		_ = os.Remove(indexPath)
	}
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	_, err := runGit(ctx, r.root, env, nil, "read-tree", treeish)
	return err
}

func (r *gitRepo) readTreeShared(ctx context.Context, treeish string) error {
	_, err := runGit(ctx, r.root, nil, nil, "read-tree", treeish)
	return err
}

func (r *gitRepo) writeTree(ctx context.Context, indexPath string) (string, error) {
	var env []string
	if indexPath != "" {
		env = []string{"GIT_INDEX_FILE=" + indexPath}
	}
	out, err := runGit(ctx, r.root, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *gitRepo) applyPatchCached(ctx context.Context, patch, indexPath string, reverse bool) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	var env []string
	if indexPath != "" {
		env = []string{"GIT_INDEX_FILE=" + indexPath}
	}
	args := []string{"apply", "--cached"}
	if reverse {
		args = append(args, "--reverse")
	}
	_, err := runGit(ctx, r.root, env, []byte(patch), args...)
	return err
}

func (r *gitRepo) canApplyPatchCached(ctx context.Context, patch, indexPath string, reverse bool) (bool, error) {
	if strings.TrimSpace(patch) == "" {
		return true, nil
	}
	var env []string
	if indexPath != "" {
		env = []string{"GIT_INDEX_FILE=" + indexPath}
	}
	args := []string{"apply", "--cached", "--check"}
	if reverse {
		args = append(args, "--reverse")
	}
	_, err := runGit(ctx, r.root, env, []byte(patch), args...)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, nil
}

// joinPatches joins non-empty patches ensuring each ends with a newline.
func joinPatches(patches []string) string {
	var b strings.Builder
	for _, p := range patches {
		if p == "" {
			continue
		}
		b.WriteString(p)
		if !strings.HasSuffix(p, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type commitCreateOptions struct {
	indexPath    string
	expectedHead string
	hasExpected  bool
	amend        bool
	noVerify     bool
}

// commitCreate builds a commit from options.indexPath with expected-HEAD CAS,
// pre-commit / commit-msg / post-commit hooks, and optional GPG signing.
// upstream: crates/vcs/src/git/mutate.rs:223
func (r *gitRepo) commitCreate(ctx context.Context, message string, opts commitCreateOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Verify commit.gpgSign setting.
	sign := false
	if out, err := runGit(ctx, r.root, nil, nil, "config", "--type=bool", "--get", "commit.gpgSign"); err == nil {
		sign = strings.TrimSpace(string(out)) == "true"
	} else {
		// Check if the config key exists with a malformed value.
		if raw, rawErr := runGit(ctx, r.root, nil, nil, "config", "--get", "commit.gpgSign"); rawErr == nil && strings.TrimSpace(string(raw)) != "" {
			return "", fmt.Errorf("git commit read commit.gpgSign: invalid boolean %q", strings.TrimSpace(string(raw)))
		}
	}

	// Ensure index exists when alternate index is specified.
	if opts.indexPath != "" {
		if _, err := os.Stat(opts.indexPath); err != nil {
			return "", fmt.Errorf("index file not found: %s", opts.indexPath)
		}
	}
	checkUnmerged := func() error {
		var env []string
		if opts.indexPath != "" {
			env = []string{"GIT_INDEX_FILE=" + opts.indexPath}
		}
		out, err := runGit(ctx, r.root, env, nil, "ls-files", "--unmerged")
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(out)) > 0 {
			return errors.New("unmerged entries in index")
		}
		return nil
	}
	if err := checkUnmerged(); err != nil {
		return "", err
	}
	if !opts.noVerify {
		if err := r.runCommitHook(ctx, "pre-commit", nil, opts.indexPath); err != nil {
			return "", err
		}
		if err := checkUnmerged(); err != nil {
			return "", err
		}
	}

	oldCommit, hasOld, err := r.headSHA(ctx)
	if err != nil {
		return "", err
	}
	if opts.hasExpected {
		if !hasOld || oldCommit != opts.expectedHead {
			return "", &errHeadMoved{expected: opts.expectedHead, actual: oldCommit}
		}
	}

	tree, err := r.writeTree(ctx, opts.indexPath)
	if err != nil {
		return "", err
	}

	var parents []string
	var authorEnv []string
	if opts.amend {
		if !hasOld {
			return "", errors.New("cannot amend an unborn HEAD")
		}
		// Read parents and original author metadata from oldCommit.
		metaOut, err := runGit(ctx, r.root, nil, nil, "show", "-s", "--format=%P%x00%an%x00%ae%x00%ad", "--date=raw", oldCommit)
		if err != nil {
			return "", err
		}
		parts := strings.Split(strings.TrimRight(string(metaOut), "\n"), "\x00")
		if len(parts) >= 4 {
			if p := strings.TrimSpace(parts[0]); p != "" {
				parents = strings.Fields(p)
			}
			authorEnv = []string{
				"GIT_AUTHOR_NAME=" + parts[1],
				"GIT_AUTHOR_EMAIL=" + parts[2],
				"GIT_AUTHOR_DATE=" + parts[3],
			}
		}
	} else if hasOld {
		parents = []string{oldCommit}
	}

	// Reject empty commit (matching mutate.rs:307).
	if len(parents) > 0 {
		parentTreeOut, err := runGit(ctx, r.root, nil, nil, "rev-parse", parents[0]+"^{tree}")
		if err == nil && strings.TrimSpace(string(parentTreeOut)) == tree {
			return "", errors.New("nothing to commit, working tree clean")
		}
	} else if tree == emptyTreeSHA {
		return "", errors.New("nothing to commit, working tree clean")
	}

	// Private commit message file so concurrent commitCreate calls never collide on COMMIT_EDITMSG.
	msgPath := filepath.Join(r.gitDir, fmt.Sprintf(".omp-commit-msg-%d-%s", os.Getpid(), randomHex(8)))
	if err := os.WriteFile(msgPath, []byte(message), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(msgPath)
	_ = os.WriteFile(filepath.Join(r.gitDir, "COMMIT_EDITMSG"), []byte(message), 0o644)

	if !opts.noVerify {
		if err := r.runCommitHook(ctx, "commit-msg", []string{msgPath}, opts.indexPath); err != nil {
			return "", err
		}
	}
	finalMsgBytes, err := os.ReadFile(msgPath)
	if err != nil {
		return "", err
	}
	finalMessage := string(finalMsgBytes)

	ctArgs := []string{"commit-tree", tree}
	for _, p := range parents {
		ctArgs = append(ctArgs, "-p", p)
	}
	if sign {
		ctArgs = append(ctArgs, "-S")
	}
	shaOut, err := runGit(ctx, r.root, authorEnv, []byte(finalMessage), ctArgs...)
	if err != nil {
		return "", err
	}
	newSHA := strings.TrimSpace(string(shaOut))

	// Atomic compare-and-swap update of HEAD.
	reflogMsg := "commit"
	if opts.amend {
		reflogMsg = "commit (amend)"
	}
	oldExpected := strings.Repeat("0", 40)
	if hasOld {
		oldExpected = oldCommit
	}
	if _, err := runGit(ctx, r.root, nil, nil, "update-ref", "-m", reflogMsg, "HEAD", newSHA, oldExpected); err != nil {
		// Translate CAS failure to errHeadMoved.
		msg := err.Error()
		if strings.Contains(msg, "but expected") || strings.Contains(msg, "reference already exists") || strings.Contains(msg, "cannot lock ref") {
			cur, _, _ := r.headSHA(ctx)
			if cur != oldCommit {
				return "", &errHeadMoved{expected: oldCommit, actual: cur}
			}
		}
		return "", err
	}

	_ = r.runCommitHook(ctx, "post-commit", nil, opts.indexPath)
	return newSHA, nil
}

func (r *gitRepo) hookPath(ctx context.Context, name string) (string, bool) {
	var dir string
	if out, err := runGit(ctx, r.root, nil, nil, "config", "--get", "core.hooksPath"); err == nil {
		v := strings.TrimSpace(string(out))
		if v != "" {
			if filepath.IsAbs(v) {
				dir = v
			} else {
				dir = filepath.Join(r.root, v)
			}
		}
	}
	if dir == "" {
		dir = filepath.Join(r.commonDir, "hooks")
	}
	p := filepath.Join(dir, name)
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", false
	}
	if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
		return "", false
	}
	return p, true
}

func (r *gitRepo) runCommitHook(ctx context.Context, name string, args []string, indexPath string) error {
	hook, ok := r.hookPath(ctx, name)
	if !ok {
		return nil
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		gitArgs := append([]string{"hook", "run", "--ignore-missing", name, "--"}, args...)
		cmd = exec.CommandContext(ctx, "git", gitArgs...)
	} else {
		cmd = exec.CommandContext(ctx, hook, args...)
	}
	cmd.Dir = r.root
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") || strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"GIT_DIR="+r.gitDir,
		"GIT_WORK_TREE="+r.root,
		"GIT_TERMINAL_PROMPT=0",
	)
	if indexPath != "" {
		env = append(env, "GIT_INDEX_FILE="+indexPath)
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Match crates/vcs/src/error.rs cli_message: return hook output verbatim when non-empty.
		msg := strings.TrimSpace(out.String())
		if msg != "" {
			return errors.New(msg)
		}
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return fmt.Errorf("git commit (%s hook) failed with exit code %d", name, exitCode)
	}
	return nil
}

// dirtyPathEntry mirrors autoresearch/git.ts DirtyPathEntry.
type dirtyPathEntry struct {
	path      string
	untracked bool
}

// parseDirtyPathsWithStatus parses `git status --porcelain -z` (or newline fallback).
// upstream: packages/coding-agent/src/autoresearch/git.ts:237
func parseDirtyPathsWithStatus(statusOutput string) []dirtyPathEntry {
	if strings.ContainsRune(statusOutput, 0) {
		return parseDirtyPathsNulWithStatus(statusOutput)
	}
	return parseDirtyPathsLinesWithStatus(statusOutput)
}

func parseDirtyPathsNulWithStatus(statusOutput string) []dirtyPathEntry {
	seen := make(map[string]bool)
	var results []dirtyPathEntry
	index := 0
	for index+3 <= len(statusOutput) {
		token := statusOutput[index : index+3]
		index += 3
		end := strings.IndexByte(statusOutput[index:], 0)
		if end < 0 {
			break
		}
		firstPath := statusOutput[index : index+end]
		index += end + 1
		untracked := strings.HasPrefix(strings.TrimSpace(token), "??")
		addDirtyPathEntry(seen, &results, firstPath, untracked)
		trimmed := strings.TrimSpace(token)
		if strings.HasPrefix(trimmed, "R") || strings.HasPrefix(trimmed, "C") {
			end2 := strings.IndexByte(statusOutput[index:], 0)
			if end2 < 0 {
				break
			}
			secondPath := statusOutput[index : index+end2]
			index += end2 + 1
			addDirtyPathEntry(seen, &results, secondPath, false)
		}
	}
	return results
}

func parseDirtyPathsLinesWithStatus(statusOutput string) []dirtyPathEntry {
	seen := make(map[string]bool)
	var results []dirtyPathEntry
	for line := range strings.SplitSeq(statusOutput, "\n") {
		trimmedLine := strings.TrimRight(line, " \t\r")
		if len(trimmedLine) < 4 {
			continue
		}
		token := trimmedLine[:3]
		rawPath := strings.TrimSpace(trimmedLine[3:])
		if rawPath == "" {
			continue
		}
		untracked := strings.HasPrefix(strings.TrimSpace(token), "??")
		for part := range strings.SplitSeq(rawPath, " -> ") {
			addDirtyPathEntry(seen, &results, part, untracked)
		}
	}
	return results
}

func addDirtyPathEntry(seen map[string]bool, results *[]dirtyPathEntry, rawPath string, untracked bool) {
	p := normalizeStatusPath(rawPath)
	if p == "" || seen[p] {
		return
	}
	seen[p] = true
	*results = append(*results, dirtyPathEntry{path: p, untracked: untracked})
}

func normalizeStatusPath(rawPath string) string {
	p := strings.TrimSpace(rawPath)
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		p = p[1 : len(p)-1]
	}
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	p = strings.TrimSuffix(p, "/")
	return p
}

func parseNulList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, "\x00") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

func randomBase36(maxVal int) string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	v := int(uint32(buf[0])|uint32(buf[1])<<8|uint32(buf[2])<<16|uint32(buf[3])<<24) & 0x7fffffff
	if maxVal > 0 {
		v %= maxVal
	}
	return strconv.FormatInt(int64(v), 36)
}
