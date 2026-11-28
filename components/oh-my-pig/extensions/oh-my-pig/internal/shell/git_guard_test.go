package shell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Scrub repo-location env vars so tests are isolated from outer git hooks/env.
	env := make([]string, 0, len(os.Environ())+5)
	for _, kv := range os.Environ() {
		key := kv
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			key = kv[:eq]
		}
		if isGitRepoLocationVar(key) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_TERMINAL_PROMPT=0",
	)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func initTestGitRepo(t *testing.T) (mainDir string, worktreeDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	root := t.TempDir()
	mainDir = filepath.Join(root, "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, mainDir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(mainDir, "file.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, mainDir, "add", "file.txt")
	runTestGit(t, mainDir, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(mainDir, "file.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, mainDir, "add", "file.txt")
	runTestGit(t, mainDir, "commit", "-m", "second")
	runTestGit(t, mainDir, "branch", "other")

	worktreeDir = filepath.Join(root, "wt")
	runTestGit(t, mainDir, "worktree", "add", "-b", "wt-branch", worktreeDir, "HEAD")
	return mainDir, worktreeDir
}

func TestGitGuardSharedCheckoutRules(t *testing.T) {
	mainDir, _ := initTestGitRepo(t)
	ctx := context.Background()

	// 1. Stash: mutating stash forms blocked; list/show/create allowed.
	for _, blocked := range []string{
		"git stash",
		"git stash push -m wip",
		"git stash pop",
		"git stash apply",
		"git stash drop",
		"git stash clear",
	} {
		dec := checkBashGitGuard(ctx, blocked, mainDir)
		if !strings.Contains(dec.Refusal, "git-guard: git stash is blocked") {
			t.Errorf("%q: expected stash refusal, got %+v", blocked, dec)
		}
	}
	for _, allowed := range []string{
		"git stash list",
		"git stash show",
		"git stash create",
	} {
		dec := checkBashGitGuard(ctx, allowed, mainDir)
		if dec.Refusal != "" {
			t.Errorf("%q: expected allowed, got refusal: %s", allowed, dec.Refusal)
		}
	}

	// 2. Reset: --hard and moving HEAD refused; unstaging allowed.
	if dec := checkBashGitGuard(ctx, "git reset --hard", mainDir); !strings.Contains(dec.Refusal, "git-guard: git reset is blocked") {
		t.Errorf("reset --hard: expected destructive refusal, got %+v", dec)
	}
	if dec := checkBashGitGuard(ctx, "git reset HEAD~1", mainDir); !strings.Contains(dec.Refusal, "git reset to another commit is blocked") {
		t.Errorf("reset HEAD~1: expected commit-move refusal, got %+v", dec)
	}
	for _, allowed := range []string{
		"git reset",
		"git reset HEAD",
		"git reset -- file.txt",
		"git reset file.txt",
		"git reset -q -- file.txt",
	} {
		if dec := checkBashGitGuard(ctx, allowed, mainDir); dec.Refusal != "" {
			t.Errorf("%q: expected allowed, got refusal: %s", allowed, dec.Refusal)
		}
	}

	// 3. Switch / checkout branch move refused; bare checkout allowed.
	for _, blocked := range []string{
		"git switch other",
		"git switch -c newbr",
		"git checkout other",
		"git checkout -b newbr",
		"git checkout -",
	} {
		dec := checkBashGitGuard(ctx, blocked, mainDir)
		if !strings.Contains(dec.Refusal, "to another branch or commit is blocked") {
			t.Errorf("%q: expected branch-move refusal, got %+v", blocked, dec)
		}
	}
	if dec := checkBashGitGuard(ctx, "git checkout", mainDir); dec.Refusal != "" {
		t.Errorf("bare git checkout: expected allowed, got %s", dec.Refusal)
	}

	// 4. Working-tree path restore / checkout outside conflict refused; --staged allowed.
	for _, blocked := range []string{
		"git checkout -- file.txt",
		"git checkout file.txt",
		"git restore file.txt",
		"git restore --worktree file.txt",
	} {
		dec := checkBashGitGuard(ctx, blocked, mainDir)
		if !strings.Contains(dec.Refusal, "not in one") {
			t.Errorf("%q: expected outside-conflict refusal, got %+v", blocked, dec)
		}
	}
	for _, allowed := range []string{
		"git restore --staged file.txt",
		"git restore -S file.txt",
	} {
		if dec := checkBashGitGuard(ctx, allowed, mainDir); dec.Refusal != "" {
			t.Errorf("%q: expected allowed, got %s", allowed, dec.Refusal)
		}
	}

	// 5. During active merge conflict: targeted restore/checkout allowed, forced/whole-tree still refused.
	mergeHead := filepath.Join(mainDir, ".git", "MERGE_HEAD")
	headSHA := runTestGit(t, mainDir, "rev-parse", "HEAD")
	if err := os.WriteFile(mergeHead, []byte(headSHA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []string{
		"git checkout -- file.txt",
		"git checkout --ours file.txt",
		"git restore file.txt",
		"git restore --theirs file.txt",
	} {
		if dec := checkBashGitGuard(ctx, allowed, mainDir); dec.Refusal != "" {
			t.Errorf("%q mid-conflict: expected allowed, got %s", allowed, dec.Refusal)
		}
	}
	for _, blocked := range []string{
		"git checkout -f file.txt",
		"git checkout -- .",
		"git restore .",
		"git restore :/",
		"git restore '*'",
		"git switch --discard-changes other",
	} {
		dec := checkBashGitGuard(ctx, blocked, mainDir)
		if !strings.Contains(dec.Refusal, "discards the working tree/index wholesale") {
			t.Errorf("%q mid-conflict: expected destructive refusal, got %+v", blocked, dec)
		}
	}
	_ = os.Remove(mergeHead)

	// 6. git clean is NOT guarded (matches upstream crates/shell/src/git/guard.rs).
	for _, allowed := range []string{"git clean -n", "git clean -f"} {
		if dec := checkBashGitGuard(ctx, allowed, mainDir); dec.Refusal != "" {
			t.Errorf("%q: expected allowed (upstream does not guard git clean), got %s", allowed, dec.Refusal)
		}
	}
	// 7. Redirection via env vars or --git-dir / --work-tree refused.
	for _, blocked := range []string{
		"GIT_DIR=/tmp/x git reset --hard",
		"GIT_WORK_TREE=/tmp/x git checkout -- file.txt",
		"GIT_INDEX_FILE=/tmp/idx git restore file.txt",
		"export GIT_DIR=/tmp/x; git stash",
		"git --git-dir=/tmp/x reset --hard",
		"git --work-tree /tmp/x checkout -- file.txt",
	} {
		dec := checkBashGitGuard(ctx, blocked, mainDir)
		if !strings.Contains(dec.Refusal, "points git elsewhere") {
			t.Errorf("%q: expected redirected refusal, got %+v", blocked, dec)
		}
	}

	// 8. Compound commands and -C targeting shared repo are caught.
	outside := t.TempDir()
	for _, blocked := range []string{
		"echo ok && git -C " + mainDir + " reset --hard",
		"cd " + mainDir + " && git stash",
		"(git -C " + mainDir + " switch other)",
	} {
		dec := checkBashGitGuard(ctx, blocked, outside)
		if dec.Refusal == "" {
			t.Errorf("%q: expected refusal, got allowed", blocked)
		}
	}

	// 9. Recursive submodule discards in shared checkout still hit the normal destructive refusal.
	if err := os.WriteFile(filepath.Join(mainDir, ".gitmodules"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{
		"git reset --hard --recurse-submodules",
		"git -c submodule.recurse=true reset --hard",
	} {
		dec := checkBashGitGuard(ctx, blocked, mainDir)
		if dec.Refusal == "" {
			t.Errorf("%q: expected refusal, got allowed", blocked)
		}
	}
	_ = os.Remove(filepath.Join(mainDir, ".gitmodules"))
}

func TestGitGuardPrivateWorktreeSnapshotsAndRestrictions(t *testing.T) {
	mainDir, wtDir := initTestGitRepo(t)
	ctx := context.Background()

	// 1. Branch switching is allowed in a linked worktree.
	if dec := checkBashGitGuard(ctx, "git switch -c feat-wt", wtDir); dec.Refusal != "" {
		t.Fatalf("switch in worktree should be allowed, got: %s", dec.Refusal)
	}

	// 2. Dirty working tree in worktree: reset --hard snapshots to refs/omp/discarded.
	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dec := checkBashGitGuard(ctx, "git reset --hard", wtDir)
	if dec.Refusal != "" {
		t.Fatalf("reset --hard in linked worktree should be allowed, got: %s", dec.Refusal)
	}
	if len(dec.Notes) != 1 || !strings.Contains(dec.Notes[0], gitGuardDiscardedRef) {
		t.Fatalf("expected snapshot note referencing %s, got %+v", gitGuardDiscardedRef, dec.Notes)
	}
	savedSHA := runTestGit(t, wtDir, "rev-parse", "--verify", gitGuardDiscardedRef)
	if savedSHA == "" {
		t.Fatalf("expected %s ref to exist", gitGuardDiscardedRef)
	}
	reflog := runTestGit(t, wtDir, "reflog", "show", "-n", "1", gitGuardDiscardedRef)
	if !strings.Contains(reflog, "git-guard:") {
		t.Fatalf("expected git-guard reflog entry, got: %s", reflog)
	}

	// 3. Foreign stash protection: stash created on main is refused from wt-branch.
	if err := os.WriteFile(filepath.Join(mainDir, "file.txt"), []byte("main dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, mainDir, "stash", "push", "-m", "from-main")
	for _, blocked := range []string{
		"git stash pop",
		"git stash apply stash@{0}",
		"git stash drop 0",
		"git stash clear",
	} {
		d := checkBashGitGuard(ctx, blocked, wtDir)
		if !strings.Contains(d.Refusal, "was saved on main") {
			t.Errorf("%q in wt-branch: expected foreign stash refusal, got %+v", blocked, d)
		}
	}

	// Own stash created on wt-branch can be popped from wt-branch.
	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("wt dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, wtDir, "stash", "push", "-m", "from-wt")
	if d := checkBashGitGuard(ctx, "git stash pop stash@{0}", wtDir); d.Refusal != "" {
		t.Errorf("popping own stash in wt-branch should be allowed, got: %s", d.Refusal)
	}

	// 4. Recursive submodule discard blocked in worktree when .gitmodules exists.
	if err := os.WriteFile(filepath.Join(wtDir, ".gitmodules"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := checkBashGitGuard(ctx, "git reset --hard --recurse-submodules", wtDir); !strings.Contains(d.Refusal, "recurses into submodules") {
		t.Errorf("expected recursive refusal in worktree, got %+v", d)
	}
	if d := checkBashGitGuard(ctx, "git -c submodule.recurse=true reset --hard", wtDir); !strings.Contains(d.Refusal, "recurses into submodules") {
		t.Errorf("expected -c submodule.recurse=true refusal in worktree, got %+v", d)
	}
	if d := checkBashGitGuard(ctx, "git -c submodule.recurse=true reset --hard --no-recurse-submodules", wtDir); d.Refusal != "" {
		t.Errorf("expected --no-recurse-submodules override to allow, got: %s", d.Refusal)
	}
	_ = os.Remove(filepath.Join(wtDir, ".gitmodules"))
}

func TestGitGuardOvnfsCheckoutTreatedAsPrivate(t *testing.T) {
	mainDir, _ := initTestGitRepo(t)
	runTestGit(t, mainDir, "config", "--local", "ovnfs.checkout", "true")

	dec := checkBashGitGuard(context.Background(), "git switch other", mainDir)
	if dec.Refusal != "" {
		t.Fatalf("expected ovnfs.checkout=true repo to be private, got refusal: %s", dec.Refusal)
	}
}

func TestBashToolGitGuardSettingIntegration(t *testing.T) {
	mainDir, wtDir := initTestGitRepo(t)

	prevSetting := Setting
	t.Cleanup(func() { Setting = prevSetting })

	rt := NewRuntime(t.TempDir())
	t.Cleanup(rt.DisposeSessions)
	cfg := DefaultConfig()
	cfg.WallTimeNoticeMinMS = -1
	cfg.PersistentState = false
	tool := NewBashTool(cfg, rt)

	// 1. Default (Setting == nil => bash.gitGuard=false): git checkout other succeeds in main checkout.
	Setting = nil
	res, err := tool.Execute(sdk.Context{}, map[string]any{
		"command": "git checkout other",
		"cwd":     mainDir,
	})
	if err != nil {
		t.Fatalf("default bash.gitGuard=false should allow git checkout, got err: %v", err)
	}
	if _, ok := res.(sdk.ToolResult); !ok {
		t.Fatalf("expected ToolResult, got %T", res)
	}
	// Return to main.
	runTestGit(t, mainDir, "checkout", "main")

	// 2. Enabled (bash.gitGuard=true): git checkout other is refused before execution.
	Setting = func(_ sdk.Context, key string) any {
		if key == "bash.gitGuard" {
			return true
		}
		return nil
	}
	_, err = tool.Execute(sdk.Context{}, map[string]any{
		"command": "git checkout other",
		"cwd":     mainDir,
	})
	if err == nil || !strings.Contains(err.Error(), "git-guard: git checkout to another branch or commit is blocked") {
		t.Fatalf("expected git-guard refusal, got err=%v", err)
	}
	// Verify branch did not change.
	if br := runTestGit(t, mainDir, "symbolic-ref", "--short", "HEAD"); br != "main" {
		t.Fatalf("branch moved despite git-guard refusal: %q", br)
	}

	// 3. Enabled in linked worktree: git reset --hard saves to refs/omp/discarded and includes notice.
	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("wt-tool-dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = tool.Execute(sdk.Context{}, map[string]any{
		"command": "git reset --hard",
		"cwd":     wtDir,
	})
	if err != nil {
		t.Fatalf("worktree reset --hard should succeed, got err: %v", err)
	}
	tr := res.(sdk.ToolResult)
	if !strings.Contains(tr.Content, gitGuardDiscardedRef) {
		t.Fatalf("expected %s notice in tool output, got:\n%s", gitGuardDiscardedRef, tr.Content)
	}
}

func TestGitGuardHardeningH1H2H3M1M2M3M4(t *testing.T) {
	mainDir, wtDir := initTestGitRepo(t)
	outside := t.TempDir()
	ctx := context.Background()

	// H1: cd inside subshell, after ; / || / | does NOT poison curCwd for later segments.
	for _, cmd := range []string{
		"(cd " + outside + "); git reset --hard",
		"cd " + outside + "; git reset --hard",
		"cd " + outside + " || git reset --hard",
		"cd " + outside + " | git reset --hard",
		"cd /nonexistent-git-guard-dir-xyz && git reset --hard",
		"git -C /nonexistent-git-guard-dir-xyz reset --hard",
		"git -C $DIR reset --hard",
		"git -C .git reset --soft HEAD~1",
	} {
		dec := checkBashGitGuard(ctx, cmd, mainDir)
		if dec.Refusal == "" {
			t.Errorf("H1 %q: expected refusal in shared repo, got allowed", cmd)
		}
	}

	// H2: Shell redirections stripped before git arg parsing.
	for _, cmd := range []string{
		"git reset HEAD~1 2>&1",
		"git reset HEAD~1 >/dev/null",
		"git reset HEAD~1 2> /dev/null",
		"git checkout other 2>&1",
	} {
		dec := checkBashGitGuard(ctx, cmd, mainDir)
		if dec.Refusal == "" {
			t.Errorf("H2 %q: expected refusal despite redirection, got allowed", cmd)
		}
	}
	if dec := checkBashGitGuard(ctx, "git checkout 2>&1", mainDir); dec.Refusal != "" {
		t.Errorf("H2 bare `git checkout 2>&1`: expected allowed, got %s", dec.Refusal)
	}

	// H3: Command wrappers, eval, sh -c / bash -c, line continuations, and double-quoted $(...) / backticks.
	for _, cmd := range []string{
		"command git reset --hard",
		"exec git reset --hard",
		"nohup git reset --hard",
		"time git reset --hard",
		"timeout 5 git reset --hard",
		"nice -n 5 git reset --hard",
		"sudo -u root git reset --hard",
		"xargs -n 1 git reset --hard",
		`eval "git reset --hard"`,
		`sh -c "git reset --hard"`,
		`bash -c "git checkout other"`,
		"git \\\nreset --hard",
		`echo "$(git reset --hard)"`,
		"echo \"`git stash`\"",
	} {
		dec := checkBashGitGuard(ctx, cmd, mainDir)
		if dec.Refusal == "" {
			t.Errorf("H3 %q: expected refusal, got allowed", cmd)
		}
	}

	// M1: Persisted shell exports and aliases are enforced.
	st := &sessionState{
		snap: &shellSnapshot{
			vars:    map[string]string{"GIT_DIR": `declare -x GIT_DIR="/tmp/elsewhere"`},
			aliases: map[string]string{"g": "alias g='git'"},
			funcs:   map[string]string{"nuke": "nuke () { git reset --hard; }"},
		},
	}
	if dec := checkBashGitGuardWithState(ctx, "git reset --hard", mainDir, st); !strings.Contains(dec.Refusal, "points git elsewhere") {
		t.Errorf("M1 persisted GIT_DIR: expected redirected refusal, got %+v", dec)
	}
	stAlias := &sessionState{
		snap: &shellSnapshot{
			aliases: map[string]string{"g": "alias g='git'"},
			funcs:   map[string]string{"nuke": "nuke () { git reset --hard; }"},
		},
	}
	if dec := checkBashGitGuardWithState(ctx, "g reset --hard", mainDir, stAlias); dec.Refusal == "" {
		t.Errorf("M1 persisted alias `g`: expected refusal, got allowed")
	}
	if dec := checkBashGitGuardWithState(ctx, "nuke", mainDir, stAlias); dec.Refusal == "" {
		t.Errorf("M1 persisted func `nuke`: expected refusal, got allowed")
	}

	// M2: Private worktree snapshot-requiring discard in non-first segment is refused.
	if dec := checkBashGitGuard(ctx, "echo hi && git reset --hard", wtDir); !strings.Contains(dec.Refusal, "compound command") {
		t.Errorf("M2 compound discard in private worktree: expected compound refusal, got %+v", dec)
	}
	if dec := checkBashGitGuard(ctx, "false && git checkout -- file.txt", wtDir); !strings.Contains(dec.Refusal, "compound command") {
		t.Errorf("M2 conditional discard in private worktree: expected compound refusal, got %+v", dec)
	}

	// M3: Cancelled context fails closed on gitMovesHead / gitRecursesSubmodules.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	repo, _ := discoverGitRepo(ctx, mainDir)
	if !gitMovesHead(canceledCtx, repo, "HEAD~1") {
		t.Errorf("M3 gitMovesHead with cancelled ctx should fail closed (return true)")
	}
	if !gitRecursesSubmodules(canceledCtx, repo, nil) {
		t.Errorf("M3 gitRecursesSubmodules with cancelled ctx should fail closed (return true)")
	}

	// M4: Heredoc bodies containing `git stash pop` or apostrophes do not trigger false refusals.
	heredocCmd := "cat <<'EOF'\ndon't run git stash pop\ngit reset --hard\nEOF"
	if dec := checkBashGitGuard(ctx, heredocCmd, mainDir); dec.Refusal != "" {
		t.Errorf("M4 heredoc body: expected allowed, got %s", dec.Refusal)
	}
}
