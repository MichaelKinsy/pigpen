package pi_subagents

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Twins of test/unit/agent-discovery-cache.test.ts and test/unit/agent-global-root-override.test.ts.

func TestAgentDiscoveryCache(t *testing.T) {
	const f = "agent-discovery-cache"
	type env struct{ root, home, project string }
	setup := func(t *testing.T) env {
		t.Helper()
		root := tmp(t)
		e := env{root, filepath.Join(root, "home"), filepath.Join(root, "project")}
		t.Setenv("HOME", e.home)
		t.Setenv("USERPROFILE", e.home)
		t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(e.home, ".pi", "agent"))
		t.Setenv("PI_OFFLINE", "true")
		unsetenv(t, "PI_SUBAGENT_EXTRA_AGENT_DIRS")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		return e
	}
	agent := func(t *testing.T, p, name, desc string) {
		t.Helper()
		writeFile(t, p, "---\nname: "+name+"\ndescription: "+desc+"\n---\n\n"+desc+".\n")
	}
	snap := func(t *testing.T, project string, scope AgentScope, noChains bool) (Discovery, DiscoveryAll) {
		t.Helper()
		eff, all, err := discoverAgentSnapshot(project, scope, "", DiscoveryOptions{NoChains: noChains})
		if err != nil {
			t.Fatal(err)
		}
		return eff, all
	}
	tw(t, f, "keeps effective and all-source projections distinct in one snapshot", func(t *testing.T) {
		e := setup(t)
		agent(t, filepath.Join(e.home, ".pi", "agent", "agents", "shared.md"), "shared", "User shared")
		agent(t, filepath.Join(e.project, ".pi", "agents", "shared.md"), "shared", "Project shared")
		agent(t, filepath.Join(e.home, ".pi", "agent", "agents", "scope-hidden.md"), "scope-hidden", "User hidden")
		wjc(t, filepath.Join(e.project, ".pi", "settings.json"), sub(m{"agentOverrides": m{"scope-hidden": m{"disabled": true}}}))
		eff, all := snap(t, e.project, ScopeBoth, true)
		eq(t, findAgent(eff.Agents, "shared").Source, SourceProject, "shared source")
		eq(t, hasName(eff.Agents, "scope-hidden"), false, "hidden effective")
		eq(t, hasName(all.User, "shared"), true, "all user shared")
		eq(t, hasName(all.Project, "shared"), true, "all project shared")
		eq(t, hasName(all.User, "scope-hidden"), true, "all user hidden")
		eq(t, hasName(all.Project, "scope-hidden"), false, "all project hidden")
		any := false
		for _, d := range eff.Directories {
			any = any || d.Source == SourceProject
		}
		eq(t, any, true, "project directory")
	})
	tskip(t, f, "retains hidden definitions for runtime collision checks", "registers a runtime agent through registerAgent and merges it with mergeRuntimeAgents (src/api/agents.ts, src/agents/runtime-agent-registry.ts), the runtime agent registry that is ported with the runtime-agent-registration slice, not this one")
	tw(t, f, "invalidates agent and chain projections when files change or appear", func(t *testing.T) {
		e := setup(t)
		agentPath := filepath.Join(e.project, ".pi", "agents", "fresh.md")
		chainPath := filepath.Join(e.project, ".pi", "chains", "fresh.chain.md")
		agent(t, agentPath, "fresh", "Initial agent")
		writeFile(t, chainPath, "---\nname: fresh-chain\ndescription: Initial chain\n---\n\n## fresh\nInspect\n")
		chain := func(a DiscoveryAll) *ChainConfig {
			for i := range a.Chains {
				if a.Chains[i].Name == "fresh-chain" {
					return &a.Chains[i]
				}
			}
			return nil
		}
		eff, all := snap(t, e.project, ScopeBoth, false)
		eq(t, findAgent(eff.Agents, "fresh").Description, "Initial agent", "agent")
		eq(t, chain(all).Description, "Initial chain", "chain")
		agent(t, agentPath, "fresh", "Updated agent")
		writeFile(t, chainPath, "---\nname: fresh-chain\ndescription: Updated chain\n---\n\n## fresh\nInspect\n")
		eff, all = snap(t, e.project, ScopeBoth, false)
		eq(t, findAgent(eff.Agents, "fresh").Description, "Updated agent", "updated agent")
		eq(t, chain(all).Description, "Updated chain", "updated chain")
		os.Remove(agentPath)
		agent(t, filepath.Join(e.project, ".pi", "agents", "added.md"), "added", "Added agent")
		changed := dAgents(t, e.project, ScopeBoth, "")
		eq(t, hasName(changed.Agents, "fresh"), false, "removed")
		eq(t, hasName(changed.Agents, "added"), true, "added")
	})
	tw(t, f, "matches scoped discovery settings and precedence for user and project views", func(t *testing.T) {
		e := setup(t)
		agent(t, filepath.Join(e.home, ".pi", "agent", "agents", "shared.md"), "shared", "User shared")
		agent(t, filepath.Join(e.project, ".pi", "agents", "shared.md"), "shared", "Project shared")
		agent(t, filepath.Join(e.home, ".pi", "agent", "agents", "user-only.md"), "user-only", "User only")
		agent(t, filepath.Join(e.project, ".pi", "agents", "project-only.md"), "project-only", "Project only")
		wjc(t, filepath.Join(e.home, ".pi", "agent", "settings.json"), sub(m{"agentOverrides": m{"shared": m{"description": "User override"}}}))
		wjc(t, filepath.Join(e.project, ".pi", "settings.json"), sub(m{"agentOverrides": m{"shared": m{"description": "Project override"}}}))
		for _, scope := range []AgentScope{ScopeUser, ScopeProject} {
			direct := dAgents(t, e.project, scope, "")
			eff, _ := snap(t, e.project, scope, true)
			diffValues(t, string(scope), reflect.ValueOf(eff), reflect.ValueOf(direct))
		}
	})
	tw(t, f, "invalidates when a new agent appears in a previously inspected nested directory", func(t *testing.T) {
		e := setup(t)
		nested := filepath.Join(e.project, ".pi", "agents", "nested")
		mkdirAll(t, nested)
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "nested-agent"), false, "initial")
		agent(t, filepath.Join(nested, "nested-agent.md"), "nested-agent", "Nested agent")
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "nested-agent"), true, "updated")
	})
	tw(t, f, "does not read out-of-scope user settings for a project snapshot", func(t *testing.T) {
		e := setup(t)
		agent(t, filepath.Join(e.project, ".pi", "agents", "project-agent.md"), "project-agent", "Project agent")
		writeFile(t, filepath.Join(e.home, ".pi", "agent", "settings.json"), "{ malformed")
		eff, all := snap(t, e.project, ScopeProject, true)
		eq(t, hasName(eff.Agents, "project-agent"), true, "effective")
		eq(t, hasName(all.Project, "project-agent"), true, "all")
	})
	tw(t, f, "keeps shared package agent metadata scoped to the selected settings source", func(t *testing.T) {
		e := setup(t)
		userRoot, projectRoot, shared := filepath.Join(e.root, "user-package"), filepath.Join(e.root, "project-package"), filepath.Join(e.root, "shared-package-agents")
		wjc(t, filepath.Join(e.home, ".pi", "agent", "settings.json"), m{"packages": l{userRoot}})
		wjc(t, filepath.Join(e.project, ".pi", "settings.json"), m{"packages": l{projectRoot}})
		relU, _ := filepath.Rel(userRoot, shared)
		relP, _ := filepath.Rel(projectRoot, shared)
		wjc(t, filepath.Join(userRoot, "package.json"), m{"name": "user-package", "pi-subagents": m{"agents": l{relU}}})
		wjc(t, filepath.Join(projectRoot, "package.json"), m{"name": "project-package", "pi-subagents": m{"agents": l{relP}}})
		agent(t, filepath.Join(shared, "shared.md"), "shared", "Shared package agent")
		directUser := findAgent(dAgents(t, e.project, ScopeUser, "").Agents, "shared")
		directProject := findAgent(dAgents(t, e.project, ScopeProject, "").Agents, "shared")
		su, _ := snap(t, e.project, ScopeUser, true)
		sp, _ := snap(t, e.project, ScopeProject, true)
		eq(t, directUser.PackageSourceName, "user-package", "direct user")
		eq(t, directProject.PackageSourceName, "project-package", "direct project")
		eq(t, findAgent(su.Agents, "shared").PackageSourceName, directUser.PackageSourceName, "snapshot user")
		eq(t, findAgent(sp.Agents, "shared").PackageSourceName, directProject.PackageSourceName, "snapshot project")
	})
	tw(t, f, "surfaces malformed in-scope settings while collecting package roots", func(t *testing.T) {
		e := setup(t)
		p := filepath.Join(e.home, ".pi", "agent", "settings.json")
		writeFile(t, p, "{ malformed")
		_, _, err := discoverAgentSnapshot(e.project, ScopeBoth, "", DiscoveryOptions{NoChains: true})
		errHas(t, err, p, "Failed to parse settings file")
	})
	tw(t, f, "invalidates when project-root policy changes", func(t *testing.T) {
		e := setup(t)
		outer := filepath.Join(e.root, "outer")
		nested := filepath.Join(outer, "nested")
		mkdirAll(t, filepath.Join(outer, ".git"))
		agent(t, filepath.Join(outer, ".pi", "agents", "outer.md"), "outer", "Outer agent")
		agent(t, filepath.Join(nested, ".pi", "agents", "nested.md"), "nested", "Nested agent")
		wjc(t, filepath.Join(nested, ".pi", "settings.json"), sub(m{"projectRootResolution": "git-root"}))
		g := dAgents(t, nested, ScopeBoth, "")
		eq(t, g.ProjectAgentsDir, filepath.Join(outer, ".pi", "agents"), "git-root dir")
		eq(t, hasName(g.Agents, "outer"), true, "outer")
		eq(t, hasName(g.Agents, "nested"), false, "nested hidden")
		wjc(t, filepath.Join(nested, ".pi", "settings.json"), sub(m{"projectRootResolution": "nearest"}))
		n := dAgents(t, nested, ScopeBoth, "")
		eq(t, n.ProjectAgentsDir, filepath.Join(nested, ".pi", "agents"), "nearest dir")
		eq(t, hasName(n.Agents, "nested"), true, "nested")
		eq(t, hasName(n.Agents, "outer"), false, "outer hidden")
	})
	tw(t, f, "does not parse chains on the ordinary effective discovery fast path", func(t *testing.T) {
		e := setup(t)
		agent(t, filepath.Join(e.project, ".pi", "agents", "worker.md"), "worker", "Worker")
		chainPath := filepath.Join(e.project, ".pi", "chains", "broken.chain.md")
		writeFile(t, chainPath, "not a valid chain")
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "worker"), true, "worker")
		found := false
		for _, d := range dAll(t, e.project).ChainDiagnostics {
			found = found || d.FilePath == chainPath
		}
		eq(t, found, true, "chain diagnostic")
	})
}

func resetGlobalNpmRootCache() {
	agentsNpmRoot.mu.Lock()
	agentsNpmRoot.root, agentsNpmRoot.set = "", false
	agentsNpmRoot.mu.Unlock()
}

func TestAgentGlobalRootOverride(t *testing.T) {
	const f = "agent-global-root-override"
	if runtime.GOOS == "windows" {
		t.Skip("the fake npm is a POSIX shell script")
	}
	temp := tmp(t)
	fixture := filepath.Join(temp, "quote's & space")
	project := filepath.Join(fixture, "project")
	rootA := filepath.Join(fixture, "prefix-a", "lib", "node_modules")
	rootB := filepath.Join(fixture, "prefix-b", "lib", "node_modules")
	invocations := filepath.Join(fixture, "npm-invocations")
	home := filepath.Join(temp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(temp, "missing-appdata"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	unsetenv(t, "PI_OFFLINE")
	t.Cleanup(func() { clearAgentDiscoveryCache(); resetGlobalNpmRootCache() })
	writeAgentFile(t, filepath.Join(project, ".pi", "agents"), "local-only", "local-only")
	for _, name := range []string{"global-a", "global-b"} {
		root := rootA
		if name == "global-b" {
			root = rootB
		}
		wjc(t, filepath.Join(root, name, "package.json"), m{"name": name, "pi-subagents": m{"agents": l{"agents"}}})
		writeAgentFile(t, filepath.Join(root, name, "agents"), name, name)
	}
	bin := filepath.Join(fixture, "bin")
	mkdirAll(t, bin)
	script := "#!/bin/sh\nprintf x >> '" + strings.ReplaceAll(invocations, "'", `'\''`) + "'\nprintf '%s\\n' '" + strings.ReplaceAll(rootA, "'", `'\''`) + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	clearAgentDiscoveryCache()
	resetGlobalNpmRootCache()
	none, a, b := new(""), new(rootA), new(rootB)
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	tw(t, f, "explicit null skips global lookup while retaining local agents and does not poison later roots", func(t *testing.T) {
		eff, all, err := discoverAgentSnapshot(project, ScopeBoth, "", DiscoveryOptions{NoChains: true, GlobalNpmRoot: none})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, hasName(eff.Agents, "local-only"), true, "local")
		eq(t, hasName(eff.Agents, "global-a"), false, "effective global")
		eq(t, hasName(all.Package, "global-a"), false, "all global")
		eq(t, exists(invocations), false, "npm not run")
		eff, all, err = discoverAgentSnapshot(project, ScopeBoth, "", DiscoveryOptions{NoChains: true, GlobalNpmRoot: a})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, hasName(eff.Agents, "global-a"), true, "completed effective")
		eq(t, hasName(all.Package, "global-a"), true, "completed all")
		eq(t, exists(invocations), false, "npm still not run")
	})
	tw(t, f, "preserves synchronous custom-prefix lookup without an override", func(t *testing.T) {
		r := dAgents(t, project, ScopeBoth, "")
		ag := findAgent(r.Agents, "global-a")
		eq(t, ag != nil && ag.Source == SourcePackage, true, "package agent")
		b, _ := os.ReadFile(invocations)
		eq(t, strings.TrimSpace(string(b)), "x", "one npm run")
	})
	tw(t, f, "different explicit roots cannot reuse stale cached package sources", func(t *testing.T) {
		changed, err := discoverAgents(project, ScopeBoth, "", DiscoveryOptions{GlobalNpmRoot: b})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, hasName(changed.Agents, "global-b"), true, "global-b")
		eq(t, hasName(changed.Agents, "global-a"), false, "global-a")
		user, _ := discoverAgents(project, ScopeUser, "", DiscoveryOptions{GlobalNpmRoot: b})
		eq(t, hasName(user.Agents, "global-b"), true, "user scope")
		bb, _ := os.ReadFile(invocations)
		eq(t, strings.TrimSpace(string(bb)), "x", "npm run once")
	})
}
