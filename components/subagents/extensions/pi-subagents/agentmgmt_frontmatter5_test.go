package pi_subagents

import (
	"path/filepath"
	"regexp"
	"testing"
)

// Twins of the last cases of test/unit/agent-frontmatter.test.ts (thinking, removed fields, prompt mode, inheritance flags,
// child-only extensions, fast mode, bundled agents, packaged discovery), agent-memory's get and agent-disabled's list.

func TestAgentFrontmatterTail(t *testing.T) {
	const f = "agent-frontmatter"
	setup := func(t *testing.T) string {
		withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		return tmp(t)
	}
	worker := func() AgentConfig {
		return AgentConfig{Name: "worker", Description: "Worker", SystemPrompt: "Do work", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/tmp/worker.md"}
	}
	projectAgent := func(t *testing.T, dir, name, body string) string {
		p := filepath.Join(dir, ".pi", "agents", name+".md")
		writeFile(t, p, body)
		return p
	}
	find := func(t *testing.T, dir, name string) *AgentConfig {
		a := findAgent(dAgents(t, dir, ScopeProject, "").Agents, name)
		if a == nil {
			t.Fatalf("agent %s not discovered", name)
		}
		return a
	}

	tw(t, f, "coerces frontmatter false strings to disabled thinking", func(t *testing.T) {
		dir := setup(t)
		for _, c := range [][2]string{{"unquoted", "false"}, {"quoted", `"false"`}} {
			projectAgent(t, dir, c[0], "---\nname: "+c[0]+"\ndescription: "+c[0]+"\nmodel: glm-5.2-short-fast\nthinking: "+c[1]+"\n---\n\nDo work\n")
		}
		agents := dAgents(t, dir, ScopeProject, "").Agents
		for _, name := range []string{"unquoted", "quoted"} {
			a := findAgent(agents, name)
			eq(t, a != nil && a.Thinking == any(false), true, name+" thinking is false")
			// The in-process launch of the original is the run pipeline's; its model for a disabled thinking is the bare model.
			eq(t, applyThinkingSuffix(a.Model, a.Thinking, false), "glm-5.2-short-fast", name+" launch model")
		}
	})
	tw(t, f, "rejects fallbackModels clearly", func(t *testing.T) {
		dir := setup(t)
		projectAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nfallbackModels: openai/gpt-5-mini, anthropic/claude-sonnet-4\n---\n\nDo work\n")
		d := dAgents(t, dir, ScopeProject, "")
		msg := ""
		for _, diag := range d.Diagnostics {
			if diag.Name == "worker" {
				msg = diag.Error
			}
		}
		polMatches(t, msg, `removed frontmatter field 'fallbackModels'`)
	})
	tw(t, f, "serializes systemPromptMode into agent frontmatter", func(t *testing.T) {
		polMatches(t, serializeAgent(worker(), SerializeAgentOptions{}), `systemPromptMode: replace`)
	})
	tw(t, f, "parses systemPromptMode from discovered agent frontmatter", func(t *testing.T) {
		dir := setup(t)
		projectAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nsystemPromptMode: replace\n---\n\nDo work\n")
		eq(t, find(t, dir, "worker").SystemPromptMode, "replace", "mode")
	})
	tw(t, f, "serializes inheritProjectContext and inheritSkills into agent frontmatter", func(t *testing.T) {
		a := worker()
		a.InheritProjectContext, a.InheritGlobalContext, a.InheritSkills = true, true, true
		s := serializeAgent(a, SerializeAgentOptions{})
		polMatches(t, s, `inheritProjectContext: true`)
		polMatches(t, s, `inheritGlobalContext: true`)
		polMatches(t, s, `inheritSkills: true`)
	})
	tw(t, f, "parses inheritProjectContext and inheritSkills from discovered agent frontmatter", func(t *testing.T) {
		dir := setup(t)
		projectAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\ninheritProjectContext: true\ninheritGlobalContext: true\ninheritSkills: true\n---\n\nDo work\n")
		a := find(t, dir, "worker")
		eq(t, []bool{a.InheritProjectContext, a.InheritGlobalContext, a.InheritSkills}, []bool{true, true, true}, "flags")
	})
	tw(t, f, "defaults inheritGlobalContext to false when frontmatter omits it", func(t *testing.T) {
		dir := setup(t)
		projectAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\n---\n\nDo work\n")
		eq(t, find(t, dir, "worker").InheritGlobalContext, false, "inheritGlobalContext")
	})
	tw(t, f, "serializes subagentOnlyExtensions into agent frontmatter", func(t *testing.T) {
		a := worker()
		a.SubagentOnlyExtensions = []string{"./tools/child-search.ts", "/opt/pi/child-only.ts"}
		polMatches(t, serializeAgent(a, SerializeAgentOptions{}), `subagentOnlyExtensions: \./tools/child-search\.ts, /opt/pi/child-only\.ts`)
	})
	tw(t, f, "parses subagentOnlyExtensions from discovered agent frontmatter", func(t *testing.T) {
		dir := setup(t)
		p := projectAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nsubagentOnlyExtensions: ./tools/child-search.ts, /opt/pi/child-only.ts\n---\n\nDo work\n")
		eq(t, find(t, dir, "worker").SubagentOnlyExtensions, []string{filepath.Join(filepath.Dir(p), "tools", "child-search.ts"), "/opt/pi/child-only.ts"}, "extensions")
	})
	tw(t, f, "parses and serializes fast mode", func(t *testing.T) {
		a := worker()
		a.Fast = new(bool)
		*a.Fast = true
		polMatches(t, serializeAgent(a, SerializeAgentOptions{}), `fast: true`)
		dir := setup(t)
		projectAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nmodel: openai-codex/gpt-5.6-luna\nfast: true\n---\n\nDo work\n")
		got := find(t, dir, "worker")
		eq(t, got.Fast != nil && *got.Fast, true, "fast")
		for _, kv := range got.ExtraFields {
			eq(t, kv.Key == "fast", false, "fast is not an extra field")
		}
	})
	tw(t, f, "adds the fast extension only for native OpenAI-Codex models", func(t *testing.T) {
		needed, err := resolveFastModeExtension(true, "openai-codex/gpt-6-sol:low", "worker")
		eq(t, err == nil && needed, true, "allowed")
		for _, model := range []string{"anthropic/claude-sonnet-4", "openai/gpt-6-sol"} {
			_, err := resolveFastModeExtension(true, model, "worker")
			hasMatch(t, err, `fast mode supports only`)
		}
	})
	tw(t, f, "bundled agents all have explicit tool allowlists", func(t *testing.T) {
		dir := setup(t)
		builtins := dAll(t, dir).Builtin
		eq(t, len(builtins) > 0, true, "builtins")
		for _, a := range builtins {
			if a.Runner != nil && (a.Runner.Type == "external-cli" || a.Runner.Type == "external-job") {
				continue
			}
			eq(t, len(a.Tools) > 0, true, a.Name+" should have explicit tools frontmatter")
		}
	})
	tw(t, f, "bundled standard agents keep bounded tool allowlists", func(t *testing.T) {
		dir := setup(t)
		agents := dAll(t, dir).Builtin
		expected := map[string][]string{
			"worker":           {"read", "grep", "find", "ls", "bash", "edit", "write", "contact_supervisor"},
			"delegate":         {"read", "grep", "find", "ls", "bash", "edit", "write", "contact_supervisor"},
			"reviewer":         {"read", "grep", "find", "ls", "watchdog_diff", "contact_supervisor"},
			"scout":            {"read", "grep", "find", "ls", "bash", "write", "contact_supervisor"},
			"researcher":       {"read", "write", "web_search", "fetch_content", "get_search_content", "source_check"},
			"evidence-auditor": {"read", "web_search", "fetch_content", "get_search_content", "source_check"},
		}
		for name, tools := range expected {
			a := findAgent(agents, name)
			if a == nil {
				t.Fatalf("%s builtin should be discovered", name)
			}
			eq(t, a.Tools, tools, name+" tools")
		}
		auditor := findAgent(agents, "evidence-auditor")
		eq(t, auditor.InheritProjectContext, true, "auditor inheritProjectContext")
		eq(t, auditor.InheritSkills, false, "auditor inheritSkills")
		prompt := findAgent(agents, "researcher").SystemPrompt
		for _, re := range []string{
			`search-result summaries as discovery aids, not final evidence`, `(?s)source_check.*decision-critical or disputed claims`,
			`direct evidence, source interpretation, and researcher inference distinctly`, `(?s)Record contradictions.*Record missing evidence`,
			`Never invent dates, quotations, citations, or unsupported precision`, "`source_check` must be registered by the loaded provider before launch",
			"If a registered `source_check` call fails, continue", `\*\*Support:\*\* direct evidence \| interpretation\. \*\*Confidence:\*\* high \| medium \| low`,
		} {
			polMatches(t, prompt, re)
		}
	})
	tw(t, f, "registers packaged agents by runtime name and serializes local name plus package", func(t *testing.T) {
		dir := setup(t)
		projectAgent(t, dir, "scout", "---\nname: scout\npackage: code-analysis\ndescription: Fast recon\n---\n\nInspect code\n")
		scout := findAgent(dAgents(t, dir, ScopeProject, "").Agents, "code-analysis.scout")
		if scout == nil {
			t.Fatal("packaged scout not found")
		}
		eq(t, scout.LocalName, "scout", "localName")
		eq(t, scout.PackageName, "code-analysis", "packageName")
		s := serializeAgent(*scout, SerializeAgentOptions{})
		polMatches(t, s, `(?m)^name: scout$`)
		polMatches(t, s, `(?m)^package: code-analysis$`)
		polNotMatches(t, s, `(?m)^name: code-analysis\.scout$`)
	})
	tw(t, f, "recursively discovers packaged chains by runtime name and preserves package on serialize", func(t *testing.T) {
		dir := setup(t)
		writeFile(t, filepath.Join(dir, ".pi", "chains", "flows", "review.chain.md"), "---\nname: review-flow\npackage: code-analysis\ndescription: Review flow\n---\n\n## code-analysis.scout\n\nInspect {task}\n")
		var chain *ChainConfig
		for _, c := range dAll(t, dir).Chains {
			if c.Name == "code-analysis.review-flow" {
				c := c
				chain = &c
			}
		}
		if chain == nil {
			t.Fatal("packaged chain not found")
		}
		eq(t, chain.LocalName, "review-flow", "localName")
		eq(t, chain.PackageName, "code-analysis", "packageName")
		eq(t, chain.Steps[0].Agent, "code-analysis.scout", "step agent")
		s := SerializeChain(chain)
		polMatches(t, s, `(?m)^name: review-flow$`)
		polMatches(t, s, `(?m)^package: code-analysis$`)
		polMatches(t, s, `(?m)^## code-analysis\.scout$`)
		polNotMatches(t, s, `(?m)^name: code-analysis\.review-flow$`)
	})
	tw(t, f, "uses the project root for the canonical project agent dir even when only .agents exists", func(t *testing.T) {
		dir := setup(t)
		nested := filepath.Join(dir, "packages", "app")
		mkdirAll(t, filepath.Join(dir, ".agents", "skills"))
		mkdirAll(t, nested)
		eq(t, dAll(t, nested).ProjectDir, filepath.Join(dir, ".pi", "agents"), "projectDir")
	})
}

func TestAgentMemoryGetAndDisabledList(t *testing.T) {
	tw(t, "agent-memory", "surfaces the memory scope in the get action", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		e.projectAgent(t, "security-reviewer", "---\nname: security-reviewer\ndescription: Recurring security reviewer\nmemory:\n  scope: project\n  path: security-reviewer\n---\n\nReview for threats.\n")
		res := getAgent(e.project, map[string]any{"agent": "security-reviewer"})
		eq(t, res.isError, false, res.text)
		polMatches(t, res.text, `Memory: project scope, path: security-reviewer`)
	})
	tw(t, "agent-disabled", "hides disabled builtins from agent-facing management list output", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), js(t, `{"subagents":{"disableBuiltins":true}}`))
		e.projectAgent(t, "helper", "---\nname: helper\ndescription: Helper\n---\n\nHelp.\n")
		disabled := dAll(t, e.project).Builtin
		eq(t, len(disabled) > 0, true, "disabled builtins")
		text := listAgents(e.project, map[string]any{}).text
		polMatches(t, text, `(?m)^- helper \(project\): Helper$`)
		polNotMatches(t, text, `Disabled builtins:`)
		for _, a := range disabled {
			polNotMatches(t, text, `(?m)^- `+regexp.QuoteMeta(a.Name)+` \(builtin`)
		}
	})
}
