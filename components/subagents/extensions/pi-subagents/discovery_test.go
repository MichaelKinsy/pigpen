package pi_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

const dfile = "agent-frontmatter"

func agentMD(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
}

func TestDiscovery(t *testing.T) {
	tw(t, dfile, "discovers project agents from both .agents and .pi/agents", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".agents", "skills", ".keep"), "")
		writeFile(t, filepath.Join(dir, ".agents", "legacy.md"), agentMD("legacy", "Legacy", "Legacy prompt"))
		writeFile(t, filepath.Join(dir, ".pi", "agents", "canonical.md"), agentMD("canonical", "Canonical", "Canonical prompt"))
		writeFile(t, filepath.Join(dir, ".pi", "agents", "SKILL.md"), agentMD("skill-named-agent", "Skill-named agent", "Skill-named agent prompt"))
		r := DiscoverAgents(dir, ScopeProject)
		for name, path := range map[string]string{"legacy": filepath.Join(dir, ".agents", "legacy.md"), "canonical": filepath.Join(dir, ".pi", "agents", "canonical.md"), "skill-named-agent": filepath.Join(dir, ".pi", "agents", "SKILL.md")} {
			a := findAgent(r.Agents, name)
			if a == nil || a.FilePath != path {
				t.Errorf("%s: %#v, want file %s", name, a, path)
			}
		}
		eq(t, r.ProjectAgentsDir, filepath.Join(dir, ".pi", "agents"), "projectAgentsDir")
	})
	tw(t, dfile, "prefers .pi/agents over .agents on project agent name collisions", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".agents", "shared.md"), agentMD("shared", "Legacy shared", "Legacy prompt"))
		writeFile(t, filepath.Join(dir, ".pi", "agents", "shared.md"), agentMD("shared", "Canonical shared", "Canonical prompt"))
		shared := findAgent(DiscoverAgents(dir, ScopeProject).Agents, "shared")
		if shared == nil {
			t.Fatal("shared not found")
		}
		eq(t, shared.FilePath, filepath.Join(dir, ".pi", "agents", "shared.md"), "file")
		eq(t, shared.Description, "Canonical shared", "description")
		eq(t, strings.TrimSpace(shared.SystemPrompt), "Canonical prompt", "prompt")
	})
	tw(t, dfile, "does not register legacy project skill files as agents", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".agents", "legacy.md"), agentMD("legacy", "Legacy", "Legacy prompt"))
		writeFile(t, filepath.Join(dir, ".agents", "skills", "directory-skill", "SKILL.md"), agentMD("directory-skill", "Directory skill", "Skill prompt"))
		writeFile(t, filepath.Join(dir, ".agents", "skills", "file-skill.md"), agentMD("file-skill", "File skill", "Skill prompt"))
		agents := DiscoverAgents(dir, ScopeProject).Agents
		if findAgent(agents, "legacy") == nil {
			t.Error("legacy missing")
		}
		for _, a := range agents {
			if strings.Contains(a.FilePath, string(filepath.Separator)+".agents"+string(filepath.Separator)+"skills"+string(filepath.Separator)) {
				t.Errorf("skill file registered: %s", a.FilePath)
			}
		}
		eq(t, findAgent(agents, "directory-skill") == nil && findAgent(agents, "file-skill") == nil, true, "skills absent")
	})
	tw(t, dfile, "does not register user SKILL.md files as agents", func(t *testing.T) {
		home := withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(home, ".agents", "user-agent.md"), agentMD("user-agent", "User agent", "User prompt"))
		writeFile(t, filepath.Join(home, ".agents", "skills", "user-skill", "SKILL.md"), agentMD("user-skill", "User skill", "Skill prompt"))
		agents := DiscoverAgents(dir, ScopeUser).Agents
		if findAgent(agents, "user-agent") == nil {
			t.Error("user-agent missing")
		}
		for _, a := range agents {
			if strings.Contains(a.FilePath, string(filepath.Separator)+".agents"+string(filepath.Separator)+"skills"+string(filepath.Separator)) {
				t.Errorf("skill file registered: %s", a.FilePath)
			}
		}
		eq(t, findAgent(agents, "user-skill"), (*AgentConfig)(nil), "user-skill")
	})
	tw(t, dfile, "discovers project chains from .pi/chains", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		chain := func(name, desc, agent, task string) string {
			return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n## " + agent + "\n\n" + task + "\n"
		}
		writeFile(t, filepath.Join(dir, ".pi", "agents", "ignored.chain.md"), chain("ignored-chain", "Ignored chain", "scout", "Ignore"))
		writeFile(t, filepath.Join(dir, ".pi", "chains", "flows", "canonical.chain.md"), chain("canonical-chain", "Canonical chain", "worker", "Inspect canonical"))
		r := DiscoverAgentsAll(dir)
		found := false
		for _, c := range r.Chains {
			if c.Name == "ignored-chain" {
				t.Error("ignored chain registered")
			}
			if c.Name == "canonical-chain" && c.FilePath == filepath.Join(dir, ".pi", "chains", "flows", "canonical.chain.md") {
				found = true
			}
		}
		eq(t, found, true, "canonical chain")
		eq(t, r.ProjectDir, filepath.Join(dir, ".pi", "agents"), "projectDir")
		eq(t, r.ProjectChainDir, filepath.Join(dir, ".pi", "chains"), "projectChainDir")
	})
	tw(t, dfile, "prefers project .pi/chains over user chains on name collisions", func(t *testing.T) {
		home := withTempHome(t)
		dir := tmp(t)
		chain := func(desc, agent, task string) string {
			return "---\nname: shared-chain\ndescription: " + desc + "\n---\n\n## " + agent + "\n\n" + task + "\n"
		}
		writeFile(t, filepath.Join(home, ".pi", "agent", "chains", "shared.chain.md"), chain("User chain", "scout", "Inspect user"))
		writeFile(t, filepath.Join(dir, ".pi", "chains", "shared.chain.md"), chain("Project chain", "worker", "Inspect project"))
		var shared []ChainConfig
		for _, c := range DiscoverAgentsAll(dir).Chains {
			if c.Name == "shared-chain" {
				shared = append(shared, c)
			}
		}
		eq(t, len(shared), 2, "count")
		eq(t, []AgentSource{shared[0].Source, shared[1].Source}, []AgentSource{SourceUser, SourceProject}, "sources")
		// the lookup the original builds keeps the last chain of a name: the project one
		last := shared[len(shared)-1]
		eq(t, last.FilePath, filepath.Join(dir, ".pi", "chains", "shared.chain.md"), "file")
		eq(t, last.Description, "Project chain", "description")
		eq(t, last.Steps[0].Agent, "worker", "agent")
		eq(t, last.Steps[0].Task, "Inspect project", "task")
	})
	tw(t, dfile, "recursively discovers nested project agents while keeping chain files separate", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		nested := filepath.Join(dir, ".pi", "agents", "code-analysis", "deep")
		nestedChain := filepath.Join(dir, ".pi", "chains", "code-analysis", "deep")
		writeFile(t, filepath.Join(nested, "scout.md"), agentMD("scout", "Nested scout", "Inspect code"))
		writeFile(t, filepath.Join(nestedChain, "review.chain.md"), "---\nname: review-flow\ndescription: Review flow\n---\n\n## scout\n\nReview\n")
		r := DiscoverAgentsAll(dir)
		scout := findAgent(r.Project, "scout")
		if scout == nil || scout.FilePath != filepath.Join(nested, "scout.md") {
			t.Errorf("scout: %#v", scout)
		}
		ok := false
		for _, c := range r.Chains {
			ok = ok || (c.Name == "review-flow" && c.FilePath == filepath.Join(nestedChain, "review.chain.md"))
		}
		eq(t, ok, true, "chain")
		for _, a := range r.Project {
			if strings.HasSuffix(a.FilePath, "review.chain.md") {
				t.Error("chain file registered as agent")
			}
		}
	})
	tw(t, dfile, "preserves supported frontmatter thinking strings", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
		for _, l := range levels {
			writeFile(t, filepath.Join(dir, ".pi", "agents", l+".md"), "---\nname: thinker-"+l+"\ndescription: Thinking "+l+"\nthinking: "+l+"\n---\n\nDo work\n")
		}
		agents := DiscoverAgents(dir, ScopeProject).Agents
		for _, l := range levels {
			a := findAgent(agents, "thinker-"+l)
			if a == nil {
				t.Fatalf("thinker-%s missing", l)
			}
			eq(t, a.Thinking, any(l), "thinking "+l)
		}
	})
	tw(t, dfile, "defaults ordinary agents to replace mode with no inherited context or skills", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".pi", "agents", "worker.md"), agentMD("worker", "Worker", "Do work"))
		w := findAgent(DiscoverAgents(dir, ScopeProject).Agents, "worker")
		if w == nil {
			t.Fatal("worker missing")
		}
		eq(t, []any{w.SystemPromptMode, w.InheritProjectContext, w.InheritSkills}, []any{"replace", false, false}, "defaults")
	})
	tw(t, dfile, "normalizes package frontmatter consistently for agents and chains", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".pi", "agents", "scout.md"), "---\nname: scout\npackage: Code Analysis!\ndescription: Fast recon\n---\n\nInspect\n")
		writeFile(t, filepath.Join(dir, ".pi", "chains", "review.chain.md"), "---\nname: review-flow\npackage: Code Analysis!\ndescription: Review flow\n---\n\n## code-analysis.scout\n\nReview\n")
		r := DiscoverAgentsAll(dir)
		if findAgent(r.Project, "code-analysis.scout") == nil {
			t.Error("packaged agent missing")
		}
		ok := false
		for _, c := range r.Chains {
			ok = ok || c.Name == "code-analysis.review-flow"
		}
		eq(t, ok, true, "packaged chain")
	})
	tw(t, dfile, "skips invalid package frontmatter that cannot be normalized", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".pi", "agents", "scout.md"), "---\nname: scout\npackage: !!!\ndescription: Fast recon\n---\n\nInspect\n")
		writeFile(t, filepath.Join(dir, ".pi", "chains", "review.chain.md"), "---\nname: review-flow\npackage: !!!\ndescription: Review flow\n---\n\n## scout\n\nReview\n")
		r := DiscoverAgentsAll(dir)
		for _, a := range r.Project {
			if strings.HasSuffix(a.FilePath, "scout.md") {
				t.Error("invalid package agent registered")
			}
		}
		msg := ""
		for _, d := range r.Diagnostics {
			if strings.HasSuffix(d.FilePath, "scout.md") {
				msg = d.Error
			}
		}
		matches(t, msg, `Agent 'scout' package is invalid after sanitization`)
		for _, c := range r.Chains {
			if strings.HasSuffix(c.FilePath, "review.chain.md") {
				t.Error("invalid package chain registered")
			}
		}
	})
	tw(t, dfile, "keeps packaged and un-packaged runtime names distinct while preserving un-packaged precedence", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".agents", "scout.md"), agentMD("scout", "Legacy scout", "Legacy"))
		writeFile(t, filepath.Join(dir, ".pi", "agents", "scout.md"), agentMD("scout", "Project scout", "Project"))
		writeFile(t, filepath.Join(dir, ".pi", "agents", "packaged.md"), "---\nname: scout\npackage: code-analysis\ndescription: Packaged scout\n---\n\nPackaged\n")
		agents := DiscoverAgents(dir, ScopeProject).Agents
		un, pk := findAgent(agents, "scout"), findAgent(agents, "code-analysis.scout")
		if un == nil || pk == nil {
			t.Fatalf("agents %#v %#v", un, pk)
		}
		eq(t, un.Description, "Project scout", "unqualified")
		eq(t, un.FilePath, filepath.Join(dir, ".pi", "agents", "scout.md"), "file")
		eq(t, pk.Description, "Packaged scout", "packaged")
	})
	tw(t, dfile, "keeps valid agents executable when another agent is malformed", func(t *testing.T) {
		withTempHome(t)
		project := tmp(t)
		writeFile(t, filepath.Join(project, ".pi", "agents", "broken.md"), "---\nname: broken\ndescription: Broken\nrunner:\n  type: unknown\n---\nBody")
		writeFile(t, filepath.Join(project, ".pi", "agents", "working.md"), "---\nname: working\ndescription: Working\n---\nBody")
		d := DiscoverAgents(project, ScopeProject)
		if findAgent(d.Agents, "working") == nil {
			t.Error("working agent missing")
		}
		if len(d.Diagnostics) == 0 {
			t.Fatal("no diagnostic")
		}
		eq(t, d.Diagnostics[0].Name, "broken", "diagnostic name")
	})
}

func TestDiscoveryDefaults(t *testing.T) {
	tw(t, dfile, "defaults delegate to append mode with inherited project context", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".pi", "agents", "delegate.md"), agentMD("delegate", "Delegate", "Do work"))
		d := findAgent(DiscoverAgents(dir, ScopeProject).Agents, "delegate")
		if d == nil {
			t.Fatal("delegate missing")
		}
		eq(t, []any{d.SystemPromptMode, d.InheritProjectContext, d.InheritSkills}, []any{"append", true, false}, "delegate defaults")
	})
	tw(t, dfile, "builtin agents inherit project context by default", func(t *testing.T) {
		withTempHome(t)
		dir := tmp(t)
		agents := DiscoverAgents(dir, ScopeBoth).Agents
		for _, n := range []string{"scout", "reviewer", "delegate"} {
			a := findAgent(agents, n)
			if a == nil {
				t.Fatalf("%s missing", n)
			}
			eq(t, a.InheritProjectContext, true, n+" inheritProjectContext")
		}
	})
}
