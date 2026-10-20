package pi_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

// agent-frontmatter.test.ts:1221-1472: broad package roots, precedence, read-only packages, depth and nested fanout.

func TestAgentFrontmatterPackages2(t *testing.T) {
	const f = "agent-frontmatter"
	tw(t, f, "does not register legacy skill files from broad package agent roots", func(t *testing.T) {
		dir := fmProject(t)
		root := filepath.Join(dir, ".pi", "npm", "node_modules", "broad-workflow")
		pkgJSON(t, root, m{"name": "broad-workflow", "pi-subagents": m{"agents": l{"."}}})
		writeFile(t, filepath.Join(root, "agent.md"), "---\nname: package-agent\ndescription: Package agent\n---\n\nPackage prompt\n")
		writeFile(t, filepath.Join(root, ".agents", "skills", "package-skill", "SKILL.md"), "---\nname: package-skill\ndescription: Package skill\n---\n\nSkill prompt\n")
		writeFile(t, filepath.Join(root, "agents", "SKILL.md"), "---\nname: skill-named-package-agent\ndescription: Skill-named package agent\n---\n\nAgent prompt\n")
		pkgs := dAll(t, dir).Package
		a := findAgent(pkgs, "package-agent")
		eq(t, a != nil && a.FilePath == filepath.Join(root, "agent.md"), true, "package-agent")
		b := findAgent(pkgs, "skill-named-package-agent")
		eq(t, b != nil && b.FilePath == filepath.Join(root, "agents", "SKILL.md"), true, "skill-named")
		skills := string(filepath.Separator) + ".agents" + string(filepath.Separator) + "skills" + string(filepath.Separator)
		for _, p := range pkgs {
			if len(p.FilePath) > 0 && filepath.ToSlash(p.FilePath) != "" && strings.Contains(p.FilePath, skills) {
				t.Errorf("registered %s", p.FilePath)
			}
		}
		eq(t, hasName(pkgs, "package-skill"), false, "package-skill")
	})
	tw(t, f, "prunes repo internals and nested project roots from broad package discovery", func(t *testing.T) {
		dir := fmProject(t)
		pkgJSON(t, dir, m{"name": "repo-root-workflow", "pi": m{"subagents": m{"agents": l{"."}, "chains": l{"."}}}})
		ign := func(name string) string {
			return "---\nname: " + name + "\ndescription: Should not be discovered\n---\n\nIgnored\n"
		}
		writeFile(t, filepath.Join(dir, "root-agent.md"), "---\nname: root-agent\ndescription: Root package agent\n---\n\nRoot prompt\n")
		writeFile(t, filepath.Join(dir, "root.chain.md"), "---\nname: root-chain\ndescription: Root package chain\n---\n\n## root-agent\n\nRun root agent\n")
		writeFile(t, filepath.Join(dir, "node_modules", "bad-agent.md"), ign("node-modules-agent"))
		writeFile(t, filepath.Join(dir, ".git", "hooks", "bad-agent.md"), ign("git-agent"))
		writeFile(t, filepath.Join(dir, "vendor", "submodule", "submodule-agent.md"), ign("submodule-agent"))
		writeFile(t, filepath.Join(dir, "vendor", "submodule", ".git"), "gitdir: ../.git/modules/submodule\n")
		writeFile(t, filepath.Join(dir, "packages", "app", "local-agent.md"), ign("nested-project-agent"))
		mkdirAll(t, filepath.Join(dir, "packages", "app", ".pi"))
		writeFile(t, filepath.Join(dir, "node_modules", "bad.chain.md"), "---\nname: node-modules-chain\ndescription: Should not be discovered\n---\n\n## root-agent\n\nIgnored\n")
		all := dAll(t, dir)
		a := findAgent(all.Package, "root-agent")
		eq(t, a != nil && a.FilePath == filepath.Join(dir, "root-agent.md"), true, "root-agent")
		for _, n := range []string{"node-modules-agent", "git-agent", "submodule-agent", "nested-project-agent"} {
			eq(t, hasName(all.Package, n), false, n+" should be pruned")
		}
		var root *ChainConfig
		for i, c := range all.Chains {
			if c.Name == "node-modules-chain" {
				t.Error("node-modules-chain discovered")
			}
			if c.Name == "root-chain" {
				root = &all.Chains[i]
			}
		}
		eq(t, root != nil && root.FilePath == filepath.Join(dir, "root.chain.md"), true, "root-chain")
	})
	tw(t, f, "keeps package definitions below user and project overrides", func(t *testing.T) {
		home := withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		dir := tmp(t)
		root := filepath.Join(dir, ".pi", "npm", "node_modules", "override-workflow")
		pkgJSON(t, root, m{"name": "override-workflow", "pi-subagents": m{"agents": l{"./agents"}, "chains": l{"./chains"}}})
		ag := func(d string) string {
			return "---\nname: scout\ndescription: " + d + " scout\n---\n\n" + d + " scout.\n"
		}
		ch := func(d string) string {
			return "---\nname: shared\ndescription: " + d + " chain\n---\n\n## scout\n\n" + d + " chain.\n"
		}
		writeFile(t, filepath.Join(root, "agents", "scout.md"), ag("Package"))
		writeFile(t, filepath.Join(root, "chains", "shared.chain.md"), ch("Package"))
		writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "scout.md"), ag("User"))
		writeFile(t, filepath.Join(dir, ".pi", "agents", "scout.md"), ag("Project"))
		writeFile(t, filepath.Join(home, ".pi", "agent", "chains", "shared.chain.md"), ch("User"))
		writeFile(t, filepath.Join(dir, ".pi", "chains", "shared.chain.md"), ch("Project"))
		eq(t, findAgent(discoverProject(t, dir, ScopeUser).Agents, "scout").Source, SourceUser, "user")
		eq(t, findAgent(discoverProject(t, dir, ScopeProject).Agents, "scout").Source, SourceProject, "project")
		// The original builds a Map by name, so the last chain of a name wins.
		var shared *ChainConfig
		all := dAll(t, dir).Chains
		for i := range all {
			if all[i].Name == "shared" {
				shared = &all[i]
			}
		}
		eq(t, shared.Source, SourceProject, "chain")
	})
	tw(t, f, "does not allow management updates to package agents", func(t *testing.T) {
		dir := fmProject(t)
		root := filepath.Join(dir, ".pi", "npm", "node_modules", "readonly-workflow")
		pkgJSON(t, root, m{"name": "readonly-workflow", "pi-subagents": m{"agents": l{"./agents"}}})
		writeFile(t, filepath.Join(root, "agents", "reviewer.md"), "---\nname: reviewer\npackage: readonly-workflow\ndescription: Read-only package reviewer.\n---\n\nReview only.\n")
		r := handleManagementAction("update", map[string]any{"agent": "readonly-workflow.reviewer", "config": m{"description": "Changed"}}, testMgmtCtx(dir))
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `read-only`)
	})
	tw(t, f, "serializes maxSubagentDepth into agent frontmatter", func(t *testing.T) {
		one := 1
		a := AgentConfig{Name: "scout", Description: "Scout", SystemPrompt: "Inspect code", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/tmp/scout.md", MaxSubagentDepth: &one}
		hasRe(t, serializeNew(&a), `maxSubagentDepth: 1`)
	})
	tw(t, f, "parses maxSubagentDepth from discovered agent frontmatter", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "scout", "---\nname: scout\ndescription: Scout\nmaxSubagentDepth: 1\n---\n\nInspect code\n")
		s := findAgent(discoverProject(t, dir, ScopeProject).Agents, "scout")
		eq(t, s.MaxSubagentDepth != nil && *s.MaxSubagentDepth == 1, true, "depth")
	})
	tw(t, f, "serializes and parses explicit nested fanout authorization", func(t *testing.T) {
		yes := true
		a := AgentConfig{Name: "delegator", Description: "Delegator", SystemPrompt: "Delegate focused work", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/tmp/delegator.md", AllowNestedSubagents: &yes}
		hasRe(t, serializeNew(&a), `allowNestedSubagents: true`)
		dir := fmProject(t)
		fmAgent(t, dir, "delegator", "---\nname: delegator\ndescription: Delegator\nallowNestedSubagents: true\n---\n\nDelegate focused work\n")
		d := findAgent(discoverProject(t, dir, ScopeProject).Agents, "delegator")
		eq(t, d.AllowNestedSubagents != nil && *d.AllowNestedSubagents, true, "allow")
		for _, kv := range d.ExtraFields {
			if kv.Key == "allowNestedSubagents" {
				t.Error("allowNestedSubagents stays in extraFields")
			}
		}
	})
}
