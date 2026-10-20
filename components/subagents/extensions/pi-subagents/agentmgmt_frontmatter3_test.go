package pi_subagents

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Package-provided agents and chains, and project roots (agent-frontmatter.test.ts:944-1220).

func pkgJSON(t *testing.T, root string, v any) { wjc(t, filepath.Join(root, "package.json"), v) }

func TestAgentFrontmatterPackages(t *testing.T) {
	const f = "agent-frontmatter"
	pkgAgent := func(t *testing.T, root, file, name, pkg, desc, body string) {
		writeFile(t, filepath.Join(root, "agents", file), "---\nname: "+name+"\npackage: "+pkg+"\ndescription: "+desc+"\n---\n\n"+body+"\n")
	}
	tw(t, f, "discovers package agents and chains from installed package manifests", func(t *testing.T) {
		dir := fmProject(t)
		workflow := filepath.Join(dir, ".pi", "npm", "node_modules", "my-pi-workflow")
		chains := filepath.Join(dir, ".pi", "npm", "node_modules", "@scope", "chain-workflow")
		pkgJSON(t, workflow, m{"name": "my-pi-workflow", "version": "1.2.3", "pi-subagents": m{"agents": l{"./agents"}}})
		pkgAgent(t, workflow, "reviewer.md", "reviewer", "my-workflow", "Review changes for this workflow.", "Review the workflow.")
		pkgJSON(t, chains, m{"name": "@scope/chain-workflow", "pi": m{"subagents": m{"chains": l{"./chains"}}}})
		writeFile(t, filepath.Join(chains, "chains", "review.chain.md"), "---\nname: review\npackage: my-workflow\ndescription: Run workflow review.\n---\n\n## my-workflow.reviewer\n\nReview the task.\n")
		all := dAll(t, dir)
		pa := findAgent(all.Package, "my-workflow.reviewer")
		if pa == nil {
			t.Fatal("packaged agent missing")
		}
		eq(t, pa.Source, SourcePackage, "source")
		eq(t, pa.PackageName, "my-workflow", "package")
		eq(t, pa.PackageSourceName, "my-pi-workflow", "source name")
		eq(t, pa.PackageSourceVersion, "1.2.3", "version")
		eq(t, pa.PackageSourceRoot, workflow, "root")
		eq(t, pa.FilePath, filepath.Join(workflow, "agents", "reviewer.md"), "file")
		eq(t, findAgent(discoverProject(t, dir, ScopeBoth).Agents, "my-workflow.reviewer").Source, SourcePackage, "both")
		var chain *ChainConfig
		for i := range all.Chains {
			if all.Chains[i].Name == "my-workflow.review" {
				chain = &all.Chains[i]
			}
		}
		if chain == nil {
			t.Fatal("packaged chain missing")
		}
		eq(t, chain.Source, SourcePackage, "chain source")
		eq(t, chain.Steps[0].Agent, "my-workflow.reviewer", "chain agent")
	})
	tw(t, f, "loads packages referenced from Pi settings", func(t *testing.T) {
		dir := fmProject(t)
		root := filepath.Join(dir, ".pi", "vendor", "workflow")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"packages": l{m{"source": "file:./vendor/workflow"}}})
		pkgJSON(t, root, m{"name": "settings-workflow", "pi": m{"subagents": m{"agents": l{"./agents"}}}})
		pkgAgent(t, root, "planner.md", "planner", "settings-workflow", "Plan from a settings-installed package.", "Plan the work.")
		a := findAgent(discoverProject(t, dir, ScopeBoth).Agents, "settings-workflow.planner")
		if a == nil {
			t.Fatal("agent missing")
		}
		eq(t, a.Source, SourcePackage, "source")
	})
	tw(t, f, "loads bare HTTPS git packages from project Pi settings", func(t *testing.T) {
		dir := fmProject(t)
		root := filepath.Join(dir, ".pi", "git", "github.com", "user", "repo")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"packages": l{"https://github.com/user/repo.git"}})
		pkgJSON(t, root, m{"name": "bare-git-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		pkgAgent(t, root, "planner.md", "planner", "bare-git-workflow", "Plan from a bare HTTPS git package.", "Plan the work.")
		a := findAgent(discoverProject(t, dir, ScopeBoth).Agents, "bare-git-workflow.planner")
		if a == nil {
			t.Fatal("agent missing")
		}
		eq(t, a.Source, SourcePackage, "source")
		eq(t, a.PackageSourceRoot, root, "root")
	})
	tw(t, f, "loads bare HTTP git packages from user Pi settings", func(t *testing.T) {
		home := withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		dir := tmp(t)
		agentDir := filepath.Join(home, ".pi", "agent")
		root := filepath.Join(agentDir, "git", "git.example.com", "team", "repo")
		wjc(t, filepath.Join(agentDir, "settings.json"), m{"packages": l{"http://git.example.com/team/repo.git"}})
		pkgJSON(t, root, m{"name": "user-bare-git-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		pkgAgent(t, root, "reviewer.md", "reviewer", "user-bare-git-workflow", "Review from a bare HTTP git package.", "Review the work.")
		a := findAgent(discoverProject(t, dir, ScopeBoth).Agents, "user-bare-git-workflow.reviewer")
		if a == nil {
			t.Fatal("agent missing")
		}
		eq(t, a.Source, SourcePackage, "source")
		eq(t, a.PackageSourceRoot, root, "root")
	})
	tw(t, f, "discovers project package agents when cwd is nested below the project root", func(t *testing.T) {
		dir := fmProject(t)
		nested := filepath.Join(dir, "packages", "app", "src")
		root := filepath.Join(dir, ".pi", "npm", "node_modules", "nested-workflow")
		mkdirAll(t, nested)
		pkgJSON(t, root, m{"name": "nested-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		pkgAgent(t, root, "reviewer.md", "reviewer", "nested-workflow", "Review from a project package.", "Review nested project work.")
		a := findAgent(discoverProject(t, nested, ScopeBoth).Agents, "nested-workflow.reviewer")
		if a == nil {
			t.Fatal("agent missing")
		}
		eq(t, a.Source, SourcePackage, "source")
		eq(t, a.FilePath, filepath.Join(root, "agents", "reviewer.md"), "file")
	})
	tw(t, f, "keeps nearest project root discovery by default when a nested .pi exists", func(t *testing.T) {
		dir := fmProject(t)
		nested := filepath.Join(dir, "packages", "app", "src")
		root := filepath.Join(dir, ".pi", "npm", "node_modules", "outer-workflow")
		mkdirAll(t, nested)
		mkdirAll(t, filepath.Join(dir, "packages", "app", ".pi"))
		mkdirAll(t, filepath.Join(dir, ".git"))
		pkgJSON(t, root, m{"name": "outer-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		pkgAgent(t, root, "planner.md", "planner", "outer-workflow", "Plan from the outer project package.", "Plan outer project work.")
		eq(t, hasName(discoverProject(t, nested, ScopeBoth).Agents, "outer-workflow.planner"), false, "outer package")
	})
	tw(t, f, "can resolve project packages and overrides from the git root", func(t *testing.T) {
		dir := fmProject(t)
		nested := filepath.Join(dir, "packages", "app", "src")
		root := filepath.Join(dir, ".pi", "npm", "node_modules", "outer-workflow")
		mkdirAll(t, nested)
		mkdirAll(t, filepath.Join(dir, "packages", "app", ".pi"))
		writeFile(t, filepath.Join(dir, ".git"), "gitdir: ../.git/worktrees/app\n")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), sub(m{"projectRootResolution": "git-root", "agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		pkgJSON(t, root, m{"name": "outer-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		pkgAgent(t, root, "planner.md", "planner", "outer-workflow", "Plan from the outer project package.", "Plan outer project work.")
		agents := discoverProject(t, nested, ScopeBoth).Agents
		pa := findAgent(agents, "outer-workflow.planner")
		if pa == nil {
			t.Fatal("package agent missing")
		}
		eq(t, pa.Source, SourcePackage, "source")
		eq(t, pa.FilePath, filepath.Join(root, "agents", "planner.md"), "file")
		reviewer := findAgent(agents, "reviewer")
		eq(t, reviewer.Model, "openai/gpt-5.4", "model")
		eq(t, reviewer.Override.Path, filepath.Join(dir, ".pi", "settings.json"), "override path")
	})
	nestedLinkedWorktree := func(t *testing.T) (repo, worktree string) {
		t.Helper()
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git is not installed")
		}
		repo = tmp(t)
		run := func(args ...string) {
			t.Helper()
			c := exec.Command("git", args...)
			c.Dir = repo
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
			}
		}
		run("init")
		run("config", "user.email", "test@example.com")
		run("config", "user.name", "Test User")
		run("config", "commit.gpgsign", "false")
		writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
		run("add", "base.txt")
		run("commit", "-m", "base")
		worktree = filepath.Join(repo, ".worktrees", "linked")
		run("worktree", "add", "-b", "linked", worktree)
		wt, err := filepath.EvalSymlinks(worktree)
		if err == nil {
			worktree = wt
		}
		return repo, worktree
	}
	linkedPackage := func(t *testing.T, repo string) (name, agentPath, settingsPath string) {
		root := filepath.Join(repo, ".pi", "vendor", "workflow")
		agentPath = filepath.Join(root, "agents", "reviewer.md")
		settingsPath = filepath.Join(repo, ".pi", "settings.json")
		wjc(t, settingsPath, m{"packages": l{m{"source": "file:./vendor/workflow"}}, "subagents": m{"projectRootResolution": "git-root", "agentOverrides": m{"reviewer": m{"model": "issue950/outer-model"}}}})
		pkgJSON(t, root, m{"name": "issue950-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		writeFile(t, agentPath, "---\nname: reviewer\npackage: issue950-workflow\ndescription: Review from the primary checkout.\n---\n\nReview the linked worktree.\n")
		return "issue950-workflow.reviewer", agentPath, settingsPath
	}
	tw(t, f, "keeps git-root discovery stable when a nested linked worktree creates incidental .pi state", func(t *testing.T) {
		withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		repo, worktree := nestedLinkedWorktree(t)
		name, agentPath, settingsPath := linkedPackage(t, repo)
		repoReal := repo
		_ = repoReal
		before := discoverProject(t, worktree, ScopeBoth).Agents
		bp := findAgent(before, name)
		if bp == nil {
			t.Fatal("package agent missing before")
		}
		eq(t, bp.Source, SourcePackage, "source")
		eq(t, bp.FilePath, agentPath, "file")
		eq(t, findAgent(before, "reviewer").Override.Path, settingsPath, "override before")
		mkdirAll(t, filepath.Join(worktree, ".pi", "todos"))
		after := discoverProject(t, worktree, ScopeBoth).Agents
		all := dAll(t, worktree)
		ap := findAgent(after, name)
		allp := findAgent(all.Package, name)
		eq(t, ap.Source, bp.Source, "source after")
		eq(t, ap.FilePath, bp.FilePath, "file after")
		eq(t, allp.FilePath, ap.FilePath, "all file")
		eq(t, all.ProjectSettingsPath, settingsPath, "settings path")
		eq(t, findAgent(after, "reviewer").Override.Path, all.ProjectSettingsPath, "override after")
	})
	tw(t, f, "lets a linked worktree opt back into nearest-root discovery", func(t *testing.T) {
		withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		repo, worktree := nestedLinkedWorktree(t)
		name, _, _ := linkedPackage(t, repo)
		wsp := filepath.Join(worktree, ".pi", "settings.json")
		wjc(t, wsp, sub(m{"projectRootResolution": "nearest", "agentOverrides": m{"reviewer": m{"model": "issue950/worktree-model"}}}))
		discovered := discoverProject(t, worktree, ScopeBoth).Agents
		all := dAll(t, worktree)
		eq(t, hasName(discovered, name), false, "package agent")
		eq(t, hasName(all.Package, name), false, "all package")
		eq(t, findAgent(discovered, "reviewer").Model, "issue950/worktree-model", "model")
		eq(t, all.ProjectSettingsPath, wsp, "settings path")
	})
}
