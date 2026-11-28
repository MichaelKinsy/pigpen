package agents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
)

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(fakeHome, ".gitconfig"))

	repo := t.TempDir()
	mustGit(t, repo, "init")
	mustGit(t, repo, "config", "user.name", "Test Author")
	mustGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "hello.txt")
	mustGit(t, repo, "commit", "-m", "initial commit")
	return repo
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
	return string(out)
}

func TestResolveModelForChildWithRoleHook(t *testing.T) {
	resetAllRegistries(t)
	prevHook := ResolveModelRole
	t.Cleanup(func() { ResolveModelRole = prevHook })

	models := []map[string]any{
		{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
		{"provider": "anthropic", "id": "claude-haiku-4-5", "contextWindow": 200000},
		{"provider": "openai", "id": "gpt-5.4-pro", "contextWindow": 256000},
		{"provider": "openai", "id": "gpt-5-mini", "contextWindow": 128000},
	}

	ext := sdk.New("test-role-res")
	var captured sdk.Context
	ext.RegisterTool(sdk.ToolDefinition{
		Name:       "cap",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			captured = ctx
			return "ok", nil
		},
	})
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID:   "role-res-sess",
		ActiveModel: map[string]any{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
		Models:      models,
	})
	if _, errMsg := h.callTool("cap", map[string]any{}); errMsg != "" {
		t.Fatalf("cap failed: %s", errMsg)
	}

	// 1. Nil hook falls back to parent active model.
	ResolveModelRole = nil
	m, think, role := resolveModelForChildWithRole(captured, []string{"@smol"})
	if m == nil || m["provider"] != "anthropic" || m["id"] != "claude-sonnet-4-5" || think != "" || role != "" {
		t.Fatalf("nil hook: got (%v, %q, %q), want parent fallback anthropic/claude-sonnet-4-5", m, think, role)
	}

	// 2. Wire role hook for @smol, @slow:high, pi/task, default.
	ResolveModelRole = func(_ sdk.Context, selector string) (provider, id, thinking string, ok bool) {
		switch selector {
		case "@smol":
			return "anthropic", "claude-haiku-4-5", "low", true
		case "@slow:high":
			return "OPENAI", "GPT-5.4-PRO", "high", true // tests case-insensitive fallback scan
		case "pi/task":
			return "openai", "gpt-5-mini", "", true
		case "default":
			return "anthropic", "claude-sonnet-4-5", "medium", true
		default:
			return "", "", "", false
		}
	}

	m, think, role = resolveModelForChildWithRole(captured, []string{"@smol"})
	if m == nil || m["provider"] != "anthropic" || m["id"] != "claude-haiku-4-5" || think != "low" || role != "smol" {
		t.Fatalf("@smol: got (%v, %q, %q), want anthropic/claude-haiku-4-5 low smol", m, think, role)
	}

	m, think = ResolveModelForChild(captured, []string{"@slow:high"})
	if m == nil || m["provider"] != "openai" || m["id"] != "gpt-5.4-pro" || think != "high" {
		t.Fatalf("@slow:high: got (%v, %q), want openai/gpt-5.4-pro high", m, think)
	}

	m, think, role = resolveModelForChildWithRole(captured, []string{"pi/task"})
	if m == nil || m["provider"] != "openai" || m["id"] != "gpt-5-mini" || think != "" || role != "task" {
		t.Fatalf("pi/task: got (%v, %q, %q), want openai/gpt-5-mini '' task", m, think, role)
	}

	m, think, role = resolveModelForChildWithRole(captured, []string{"default"})
	if m == nil || m["provider"] != "anthropic" || m["id"] != "claude-sonnet-4-5" || think != "medium" || role != "default" {
		t.Fatalf("default: got (%v, %q, %q), want anthropic/claude-sonnet-4-5 medium default", m, think, role)
	}

	// 3. Unresolved role falls back to parent active model.
	m, think, role = resolveModelForChildWithRole(captured, []string{"@unknown"})
	if m == nil || m["provider"] != "anthropic" || m["id"] != "claude-sonnet-4-5" || think != "" || role != "" {
		t.Fatalf("@unknown: got (%v, %q, %q), want parent fallback", m, think, role)
	}
}

func TestRunSubagentPopulatesModelRoleFromRoleHook(t *testing.T) {
	resetAllRegistries(t)
	prevHook := ResolveModelRole
	t.Cleanup(func() { ResolveModelRole = prevHook })

	ResolveModelRole = func(_ sdk.Context, selector string) (provider, id, thinking string, ok bool) {
		if selector == "@smol" {
			return "openai", "gpt-5-mini", "low", true
		}
		return "", "", "", false
	}

	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	rt := NewRuntime(cfg)
	ext := sdk.New("test-subagent-role")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID:   "subagent-role-sess",
		ActiveModel: map[string]any{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			h.finishChildWithText(child, "scout done")
			return nil
		},
	})

	res, errMsg := h.callTool("task", map[string]any{
		"context": "Inspect files",
		"tasks": []any{
			map[string]any{"name": "ScoutRole", "agent": "scout", "task": "check repo", "solutionSpace": "bounded"},
		},
	})
	if errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}

	children := h.getChildren()
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	children[0].mu.Lock()
	modelMap, _ := children[0].createArgs["model"].(map[string]any)
	thinkLevel, _ := children[0].createArgs["thinkingLevel"].(string)
	children[0].mu.Unlock()
	if modelMap["provider"] != "openai" || modelMap["id"] != "gpt-5-mini" {
		t.Fatalf("expected scout @smol -> openai/gpt-5-mini, got %+v", modelMap)
	}
	if thinkLevel != "low" {
		t.Fatalf("expected thinkingLevel=low from @smol hook, got %q", thinkLevel)
	}

	// Verify SingleResult.ModelRole is populated on tool details.
	rawDet, _ := json.Marshal(res["details"])
	var det TaskToolDetails
	if err := json.Unmarshal(rawDet, &det); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if len(det.Results) != 1 || det.Results[0].ModelRole != "smol" {
		t.Fatalf("expected SingleResult.ModelRole=smol, got %+v", det.Results)
	}
}

func TestWorktreePatchCaptureApplyAndCleanup(t *testing.T) {
	resetAllRegistries(t)
	repo := initTestGitRepo(t)

	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	cfg.IsolationEnabled = true
	cfg.IsolationApply = true
	cfg.IsolationMerge = "patch"
	rt := NewRuntime(cfg)
	ext := sdk.New("test-wt-patch")
	RegisterWithRuntime(ext, rt)

	var childWorktreeDir string
	var childSysPrompt string
	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       repo,
		SessionID: "wt-patch-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			child.mu.Lock()
			cwdArg, _ := child.createArgs["cwd"].(string)
			resOpts, _ := child.createArgs["resources"].(map[string]any)
			appends, _ := resOpts["appendSystemPrompt"].([]any)
			if len(appends) > 0 {
				childSysPrompt, _ = appends[0].(string)
			}
			child.mu.Unlock()
			childWorktreeDir = cwdArg

			// Child edits tracked file and creates a new untracked file inside the worktree.
			if err := os.WriteFile(filepath.Join(cwdArg, "hello.txt"), []byte("line1\nline2-edited\n"), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(cwdArg, "added.txt"), []byte("brand new file\n"), 0o644); err != nil {
				return err
			}
			h.finishChildWithText(child, "edited hello.txt and added added.txt")
			return nil
		},
	})

	res, errMsg := h.callTool("task", map[string]any{
		"context": "Isolated patch edit",
		"tasks": []any{
			map[string]any{"name": "PatchWorker", "task": "edit files", "solutionSpace": "fixed", "isolated": true},
		},
	})
	if errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}
	if isErr, _ := res["is_error"].(bool); isErr {
		t.Fatalf("unexpected task error: %+v", res)
	}

	// Child must have run in a separate worktree dir, which must now be cleaned up.
	if childWorktreeDir == "" || childWorktreeDir == repo {
		t.Fatalf("expected child cwd to be isolated worktree, got %q (repo=%q)", childWorktreeDir, repo)
	}
	if _, err := os.Stat(childWorktreeDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir %q to be removed after run, stat err=%v", childWorktreeDir, err)
	}
	if !strings.Contains(childSysPrompt, "# Working Tree") || !strings.Contains(childSysPrompt, childWorktreeDir) {
		t.Fatalf("expected # Working Tree section in child system prompt, got:\n%s", childSysPrompt)
	}

	// Verify patch artifact and SingleResult fields.
	rawDet, _ := json.Marshal(res["details"])
	var det TaskToolDetails
	if err := json.Unmarshal(rawDet, &det); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if len(det.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(det.Results))
	}
	sr := det.Results[0]
	if !sr.Isolated || sr.PatchPath == "" || sr.HasRootChanges == nil || !*sr.HasRootChanges {
		t.Fatalf("expected Isolated=true, PatchPath set, HasRootChanges=true; got %+v", sr)
	}
	patchBytes, err := os.ReadFile(sr.PatchPath)
	if err != nil {
		t.Fatalf("read patch %s: %v", sr.PatchPath, err)
	}
	if !strings.Contains(string(patchBytes), "line2-edited") || !strings.Contains(string(patchBytes), "added.txt") {
		t.Fatalf("unexpected patch content:\n%s", string(patchBytes))
	}

	// Verify changes were cleanly applied to the base repo.
	gotHello, err := os.ReadFile(filepath.Join(repo, "hello.txt"))
	if err != nil || string(gotHello) != "line1\nline2-edited\n" {
		t.Fatalf("base repo hello.txt = %q (err=%v), want line1\\nline2-edited\\n", string(gotHello), err)
	}
	gotAdded, err := os.ReadFile(filepath.Join(repo, "added.txt"))
	if err != nil || string(gotAdded) != "brand new file\n" {
		t.Fatalf("base repo added.txt = %q (err=%v), want brand new file\\n", string(gotAdded), err)
	}

	// Verify `git worktree list` only lists the main repo (no leaked linked worktrees).
	wtList := mustGit(t, repo, "worktree", "list", "--porcelain")
	if strings.Count(wtList, "worktree ") != 1 {
		t.Fatalf("expected 1 worktree remaining after cleanup, got:\n%s", wtList)
	}
}

func TestWorktreeBranchModeCreatesBranchAndRemovesWorktree(t *testing.T) {
	resetAllRegistries(t)
	repo := initTestGitRepo(t)

	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	cfg.IsolationEnabled = true
	cfg.IsolationMerge = "branch"
	rt := NewRuntime(cfg)
	ext := sdk.New("test-wt-branch")
	RegisterWithRuntime(ext, rt)

	var childWorktreeDir string
	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       repo,
		SessionID: "wt-branch-sess",
		OnChild: func(h *fakeHost, child *fakeChildState, _ string) error {
			child.mu.Lock()
			cwdArg, _ := child.createArgs["cwd"].(string)
			child.mu.Unlock()
			childWorktreeDir = cwdArg

			if err := os.WriteFile(filepath.Join(cwdArg, "feature.txt"), []byte("from branch worker\n"), 0o644); err != nil {
				return err
			}
			h.finishChildWithText(child, "created feature.txt")
			return nil
		},
	})

	res, errMsg := h.callTool("task", map[string]any{
		"context": "Branch isolation",
		"tasks": []any{
			map[string]any{"name": "BranchWorker", "task": "add feature", "solutionSpace": "fixed", "isolated": true},
		},
	})
	if errMsg != "" {
		t.Fatalf("task failed: %s", errMsg)
	}
	if isErr, _ := res["is_error"].(bool); isErr {
		t.Fatalf("unexpected task error: %+v", res)
	}

	// Worktree directory must be removed.
	if _, err := os.Stat(childWorktreeDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir %q removed, stat err=%v", childWorktreeDir, err)
	}

	rawDet, _ := json.Marshal(res["details"])
	var det TaskToolDetails
	if err := json.Unmarshal(rawDet, &det); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	sr := det.Results[0]
	if !sr.Isolated || sr.BranchName != "omp-task/BranchWorker" || sr.HasRootChanges == nil || !*sr.HasRootChanges {
		t.Fatalf("expected BranchName=omp-task/BranchWorker and HasRootChanges=true, got %+v", sr)
	}

	// Branch must exist in base repo and contain feature.txt while base working tree stays clean.
	fileOnBranch := mustGit(t, repo, "show", sr.BranchName+":feature.txt")
	if fileOnBranch != "from branch worker\n" {
		t.Fatalf("branch file content = %q, want 'from branch worker\\n'", fileOnBranch)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected base repo working tree not to have feature.txt in branch mode")
	}
}

func TestWorktreeDirtyBaselinePropagatedAndExcludedFromChildDelta(t *testing.T) {
	resetAllRegistries(t)
	repo := initTestGitRepo(t)

	// Create dirty baseline in parent repo: unstaged tracked edit + untracked file.
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-dirty-wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("parent uncommitted wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt, err := CreateIsolatedWorktree(context.Background(), repo)
	if err != nil {
		t.Fatalf("CreateIsolatedWorktree: %v", err)
	}
	defer wt.Cleanup(context.Background())

	// Child must see both dirty tracked edit and untracked baseline file.
	gotHello, err := os.ReadFile(filepath.Join(wt.WorktreeDir, "hello.txt"))
	if err != nil || string(gotHello) != "line1\nline2-dirty-wip\n" {
		t.Fatalf("child hello.txt = %q (err=%v), want dirty baseline", string(gotHello), err)
	}
	gotWip, err := os.ReadFile(filepath.Join(wt.WorktreeDir, "wip.txt"))
	if err != nil || string(gotWip) != "parent uncommitted wip\n" {
		t.Fatalf("child wip.txt = %q (err=%v), want baseline untracked file", string(gotWip), err)
	}

	// Child makes its own independent change.
	if err := os.WriteFile(filepath.Join(wt.WorktreeDir, "child.txt"), []byte("child only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	capRes, err := wt.FinalizePatch(context.Background(), t.TempDir(), "DeltaWorker", true)
	if err != nil {
		t.Fatalf("FinalizePatch: %v", err)
	}
	if !capRes.HasRootChanges || !capRes.Applied {
		t.Fatalf("expected HasRootChanges=true Applied=true, got %+v", capRes)
	}

	patchBytes, err := os.ReadFile(capRes.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	patchStr := string(patchBytes)
	if !strings.Contains(patchStr, "child.txt") {
		t.Fatalf("expected child.txt in delta patch:\n%s", patchStr)
	}
	if strings.Contains(patchStr, "wip.txt") || strings.Contains(patchStr, "line2-dirty-wip") {
		t.Fatalf("delta patch must NOT contain parent baseline WIP:\n%s", patchStr)
	}

	// Parent repo still has its WIP plus child.txt applied cleanly.
	gotChild, err := os.ReadFile(filepath.Join(repo, "child.txt"))
	if err != nil || string(gotChild) != "child only\n" {
		t.Fatalf("parent child.txt = %q (err=%v), want 'child only\\n'", string(gotChild), err)
	}
	parentHello, _ := os.ReadFile(filepath.Join(repo, "hello.txt"))
	if string(parentHello) != "line1\nline2-dirty-wip\n" {
		t.Fatalf("parent WIP should be preserved, got %q", string(parentHello))
	}
}

func TestWorktreePatchConflictDoesNotHalfApply(t *testing.T) {
	resetAllRegistries(t)
	repo := initTestGitRepo(t)

	wt, err := CreateIsolatedWorktree(context.Background(), repo)
	if err != nil {
		t.Fatalf("CreateIsolatedWorktree: %v", err)
	}
	defer wt.Cleanup(context.Background())

	// Child edits hello.txt and creates a second file second.txt.
	if err := os.WriteFile(filepath.Join(wt.WorktreeDir, "hello.txt"), []byte("line1\nchild-edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.WorktreeDir, "second.txt"), []byte("should not half apply\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Meanwhile parent modifies hello.txt conflictingly.
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nparent-conflict\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	capRes, err := wt.FinalizePatch(context.Background(), t.TempDir(), "ConflictWorker", true)
	if err == nil {
		t.Fatalf("expected conflict error, got nil (cap=%+v)", capRes)
	}
	if capRes.Applied {
		t.Fatalf("expected Applied=false on conflict")
	}
	// Verify second.txt was NOT created in base repo (atomic refusal via --check).
	if _, statErr := os.Stat(filepath.Join(repo, "second.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected atomic refusal without half-applied second.txt, statErr=%v", statErr)
	}
}

func TestWorktreeNonGitRepoError(t *testing.T) {
	resetAllRegistries(t)
	nonGitDir := t.TempDir()

	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	cfg.IsolationEnabled = true
	rt := NewRuntime(cfg)
	ext := sdk.New("test-wt-nongit")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       nonGitDir,
		SessionID: "wt-nongit-sess",
	})

	res, _ := h.callTool("task", map[string]any{
		"context": "Non-git check",
		"tasks": []any{
			map[string]any{"name": "NoGit", "task": "edit", "solutionSpace": "s", "isolated": true},
		},
	})
	if isErr, _ := res["is_error"].(bool); !isErr {
		t.Fatalf("expected error for non-git dir, got %+v", res)
	}
	if txt, _ := res["content"].(string); !strings.Contains(txt, "Isolated execution requires a git repository") {
		t.Fatalf("expected clear non-git error, got:\n%s", txt)
	}
}

func TestWorktreePlanModeRefusalEvenWhenIsolationEnabled(t *testing.T) {
	resetAllRegistries(t)
	repo := initTestGitRepo(t)

	cfg := DefaultConfig()
	cfg.AsyncEnabled = false
	cfg.IsolationEnabled = true
	rt := NewRuntime(cfg)
	ext := sdk.New("test-wt-plan")
	RegisterWithRuntime(ext, rt)

	gate.SetPlanActive(func(_ sdk.Context) bool { return true })

	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       repo,
		SessionID: "wt-plan-sess",
	})

	res, _ := h.callTool("task", map[string]any{
		"context": "Plan isolation refusal",
		"tasks": []any{
			map[string]any{"name": "PlanIso", "task": "edit", "solutionSpace": "s", "isolated": true},
		},
	})
	if isErr, _ := res["is_error"].(bool); !isErr {
		t.Fatalf("expected plan-mode refusal, got %+v", res)
	}
	if txt, _ := res["content"].(string); !strings.Contains(txt, "Subagent isolation, apply, and merge controls are unavailable in plan mode.") {
		t.Fatalf("expected plan-mode refusal message, got:\n%s", txt)
	}
}

func TestWorktreeDefaultDisabledPreservesSchemaAndRefusesIsolated(t *testing.T) {
	resetAllRegistries(t)
	repo := initTestGitRepo(t)

	// Default config: IsolationEnabled=false -> TaskSchema must NOT expose `isolated`.
	defaultCfg := DefaultConfig()
	if defaultCfg.IsolationEnabled {
		t.Fatalf("DefaultConfig().IsolationEnabled must be false")
	}
	schema := TaskSchema(defaultCfg, DefaultSpawnAgent)
	props := schema["properties"].(map[string]any)
	tasksProp := props["tasks"].(map[string]any)
	itemProps := tasksProp["items"].(map[string]any)["properties"].(map[string]any)
	if _, hasIso := itemProps["isolated"]; hasIso {
		t.Fatalf("default task schema must NOT include `isolated` property")
	}
	desc := RenderTaskDescription(defaultCfg, LoadBundledAgents(), "*")
	if strings.Contains(desc, "`isolated`") {
		t.Fatalf("default task description must NOT mention `isolated`:\n%s", desc)
	}

	// Enabled schema exposes `isolated` boolean property.
	enabledCfg := defaultCfg
	enabledCfg.IsolationEnabled = true
	enabledSchema := TaskSchema(enabledCfg, DefaultSpawnAgent)
	enabledItemProps := enabledSchema["properties"].(map[string]any)["tasks"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, hasIso := enabledItemProps["isolated"]; !hasIso {
		t.Fatalf("expected `isolated` property when task.isolation.enabled=true")
	}

	// With live settings store (default task.isolation.enabled=false), calling isolated:true fails preflight.
	rt := NewRuntime(DefaultConfig())
	rt.UseSettings = true
	ext := sdk.New("test-wt-default-off")
	settings.Register(ext)
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       repo,
		SessionID: "wt-default-off-sess",
	})
	res, _ := h.callTool("task", map[string]any{
		"context": "Default off check",
		"tasks": []any{
			map[string]any{"name": "DefaultOff", "task": "edit", "solutionSpace": "s", "isolated": true},
		},
	})
	if isErr, _ := res["is_error"].(bool); !isErr {
		t.Fatalf("expected error when task.isolation.enabled=false, got %+v", res)
	}
	if txt, _ := res["content"].(string); !strings.Contains(txt, "Subagent isolated execution requires task.isolation.enabled; it is currently false.") {
		t.Fatalf("unexpected refusal text:\n%s", txt)
	}
}
