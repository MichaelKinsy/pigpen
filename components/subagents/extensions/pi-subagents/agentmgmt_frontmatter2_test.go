package pi_subagents

import (
	"path/filepath"
	"testing"
)

func TestAgentFrontmatterLists(t *testing.T) {
	const f = "agent-frontmatter"
	ag := func(project string) string { return filepath.Join(project, ".pi", "agents") }
	tw(t, f, "discovers newline block lists for all list fields and routes MCP tools", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\ntools:\n  - read-only\n  - mcp:github/search_repositories\ndefaultReads:\n  - input-one.md\n  - input-two.md\nskill:\n  - review-checklist\n  - safe-bash\nskillPath:\n  - ./private-skills\n  - ../shared-skills\nextensions:\n  - ./extension-one.ts\n  - ./extension-two.ts\nsubagentOnlyExtensions:\n  - ./child-only.ts\n  - ./child-helper.ts\n---\n\nDo work\n")
		w := findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker")
		eq(t, w.Tools, []string{"read-only"}, "tools")
		eq(t, w.McpDirectTools, []string{"github/search_repositories"}, "mcp")
		eq(t, w.DefaultReads, []string{"input-one.md", "input-two.md"}, "reads")
		eq(t, w.Skills, []string{"review-checklist", "safe-bash"}, "skills")
		eq(t, w.SkillPath, []string{"./private-skills", "../shared-skills"}, "skillPath")
		eq(t, w.Extensions, []string{filepath.Join(ag(dir), "extension-one.ts"), filepath.Join(ag(dir), "extension-two.ts")}, "extensions")
		eq(t, w.SubagentOnlyExtensions, []string{filepath.Join(ag(dir), "child-only.ts"), filepath.Join(ag(dir), "child-helper.ts")}, "subagent only")
	})
	tw(t, f, "preserves MCP-only tools as an explicit empty builtin allowlist", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "mcp-only", "---\nname: mcp-only\ndescription: MCP only\ntools: mcp:github/search_repositories\n---\n\nDo MCP work\n")
		a := findAgent(discoverProject(t, dir, ScopeProject).Agents, "mcp-only")
		eq(t, a.Tools != nil && len(a.Tools) == 0, true, "empty allowlist")
		eq(t, a.McpDirectTools, []string{"github/search_repositories"}, "mcp")
		hasRe(t, serializeNew(a), `(?m)^tools: mcp:github/search_repositories$`)
	})
	tw(t, f, "preserves comma-separated syntax across all list fields", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\ntools: read-only, mcp:github/search_repositories\ndefaultReads: input-one.md, input-two.md\nskills: review-checklist, safe-bash\nskillPath: ./private-skills, ../shared-skills\nextensions: ./extension-one.ts, ./extension-two.ts\nsubagentOnlyExtensions: ./child-only.ts, ./child-helper.ts\n---\n\nDo work\n")
		w := findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker")
		eq(t, w.Tools, []string{"read-only"}, "tools")
		eq(t, w.McpDirectTools, []string{"github/search_repositories"}, "mcp")
		eq(t, w.DefaultReads, []string{"input-one.md", "input-two.md"}, "reads")
		eq(t, w.Skills, []string{"review-checklist", "safe-bash"}, "skills")
		eq(t, w.SkillPath, []string{"./private-skills", "../shared-skills"}, "skillPath")
		eq(t, w.Extensions, []string{filepath.Join(ag(dir), "extension-one.ts"), filepath.Join(ag(dir), "extension-two.ts")}, "extensions")
		eq(t, w.SubagentOnlyExtensions, []string{filepath.Join(ag(dir), "child-only.ts"), filepath.Join(ag(dir), "child-helper.ts")}, "subagent only")
	})
	tw(t, f, "discovers and serializes excludeTools without validating names", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nexcludeTools:\n  - write\n  - made-up-tool\n---\n\nDo work\n")
		w := findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker")
		eq(t, w.ExcludeTools, []string{"write", "made-up-tool"}, "excludeTools")
		hasRe(t, serializeNew(w), `(?m)^excludeTools: write, made-up-tool$`)
	})
	tw(t, f, "parses and preserves explicit non-bash permission rules", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\ntools: bash,read,write\npermission:\n  read: allow\n  write: ask\n  edit: deny\n---\n\nDo work\n")
		w := findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker")
		eq(t, canonJSON(w.Permissions), `{"edit":"deny","read":"allow","write":"ask"}`, "permissions")
		for _, kv := range w.ExtraFields {
			if kv.Key == "permission" {
				t.Error("permission stays in extraFields")
			}
		}
		hasRe(t, serializeNew(w), `(?m)^permissions:\n  read: allow\n  write: ask\n  edit: deny$`)
	})
	tw(t, f, "rejects bash permission rules and conflicting aliases", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\npermission:\n  bash: deny\n---\n")
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `pi-guard`)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\npermission:\n  write: ask\npermissions:\n  edit: deny\n---\n")
		clearAgentDiscoveryCache()
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `cannot declare both permission and permissions`)
	})
	tw(t, f, "serializes defaultContext into agent frontmatter", func(t *testing.T) {
		a := AgentConfig{Name: "worker", Description: "Worker", SystemPrompt: "Do work", SystemPromptMode: "replace", InheritProjectContext: true, Source: SourceProject, FilePath: "/tmp/worker.md", DefaultContext: "fork"}
		hasRe(t, serializeNew(&a), `defaultContext: fork`)
	})
	tw(t, f, "parses defaultContext from discovered agent frontmatter", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\ndefaultContext: fork\n---\n\nDo work\n")
		eq(t, findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker").DefaultContext, "fork", "defaultContext")
	})
	tw(t, f, "loads packaged worker fresh and oracle forked with advisor alias", func(t *testing.T) {
		dir := fmProject(t)
		d, err := discoverAgentsAll(dir, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		agents := d.Builtin
		eq(t, findAgent(agents, "worker").DefaultContext, "fresh", "worker")
		oracle := findAgent(agents, "oracle")
		eq(t, oracle.DefaultContext, "fork", "oracle")
		eq(t, oracle.Aliases, []string{"advisor"}, "aliases")
		noRe(t, joinComma(oracle.Tools), `contact_supervisor`)
		for _, n := range []string{"scout", "researcher", "oracle", "reviewer"} {
			for _, tool := range findAgent(agents, n).Tools {
				if tool == "intercom" {
					t.Errorf("%s should not require generic intercom", n)
				}
			}
		}
		hasRe(t, oracle.SystemPrompt, `asking or consulting the oracle`)
		hasRe(t, oracle.SystemPrompt, "When runtime bridge instructions provide `contact_supervisor`")
		hasRe(t, oracle.SystemPrompt, `If no supervisor channel is available`)
		for _, n := range []string{"planner", "context-builder", "gpt-pro"} {
			eq(t, hasName(agents, n), false, n)
		}
	})
	tskip(t, f, "keeps bundled agent definitions from module load during package file updates", "copies the TypeScript src and node_modules into a fixture and runs the module under node --experimental-strip-types after rewriting agents/scout.md on disk; the Go port embeds the bundled definitions in the binary (//go:embed builtin/*.md), so a later file update cannot reach them and there is no module load to race")
	tw(t, f, "parses, serializes, and validates outputMode defaults", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\noutputMode: file-only\n---\n\nDo work\n")
		w := findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker")
		eq(t, w.OutputMode, "file-only", "outputMode")
		hasRe(t, serializeNew(w), `(?m)^outputMode: file-only$`)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\noutputMode: artifact-only\n---\n\nDo work\n")
		clearAgentDiscoveryCache()
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `Agent 'worker' has invalid outputMode frontmatter; expected 'inline' or 'file-only'`)
	})
	tw(t, f, "serializes and discovers single-agent launch defaults", func(t *testing.T) {
		dir := fmProject(t)
		off, to, tt := false, 90000, 600000
		a := AgentConfig{Name: "worker", Description: "Worker", SystemPrompt: "Do work", SystemPromptMode: "replace", Source: SourceProject, FilePath: filepath.Join(ag(dir), "worker.md"),
			DefaultAsync: &off, DefaultTimeoutMs: &to, DefaultToolTimeoutMs: &tt, DefaultAcceptance: parseJSONMust(t, `{"level":"none","reason":"lightweight lookup"}`)}
		ser := serializeNew(&a)
		hasRe(t, ser, `(?m)^async: false$`)
		hasRe(t, ser, `(?m)^timeoutMs: 90000$`)
		hasRe(t, ser, `(?m)^toolTimeoutMs: 600000$`)
		hasRe(t, ser, `(?m)^acceptance: \{"level":"none","reason":"lightweight lookup"\}$`)
		fmAgent(t, dir, "worker", ser)
		w := findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker")
		eq(t, w.DefaultAsync != nil && !*w.DefaultAsync, true, "async")
		eq(t, *w.DefaultTimeoutMs, 90000, "timeout")
		eq(t, *w.DefaultToolTimeoutMs, 600000, "tool timeout")
		eq(t, canonJSON(w.DefaultAcceptance), `{"level":"none","reason":"lightweight lookup"}`, "acceptance")
	})
	tw(t, f, "parses scalar acceptance defaults and rejects invalid policies", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nacceptance: checked\n---\n\nDo work\n")
		eq(t, findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker").DefaultAcceptance, "checked", "scalar")
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nacceptance: { level: \"none\", reason: \"NA\" }\n---\n\nDo work\n")
		clearAgentDiscoveryCache()
		eq(t, canonJSON(findAgent(discoverProject(t, dir, ScopeProject).Agents, "worker").DefaultAcceptance), `{"level":"none","reason":"NA"}`, "object")
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nacceptance: none\n---\n\nDo work\n")
		clearAgentDiscoveryCache()
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `Agent 'worker' acceptance frontmatter level "none" requires a reason`)
	})
	tw(t, f, "parses, serializes, and validates acceptance roles", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "explorer", "---\nname: explorer\ndescription: Explorer\nacceptanceRole: read-only\n---\n\nExplore the codebase\n")
		e := findAgent(discoverProject(t, dir, ScopeProject).Agents, "explorer")
		eq(t, e.AcceptanceRole, "read-only", "role")
		hasRe(t, serializeNew(e), `(?m)^acceptanceRole: read-only$`)
		for _, kv := range e.ExtraFields {
			if kv.Key == "acceptanceRole" {
				t.Error("acceptanceRole stays in extraFields")
			}
		}
		fmAgent(t, dir, "explorer", "---\nname: explorer\ndescription: Explorer\nacceptanceRole: observer\n---\n\nExplore the codebase\n")
		clearAgentDiscoveryCache()
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `Agent 'explorer' has invalid acceptanceRole frontmatter; expected 'read-only' or 'writer'`)
	})
	tw(t, f, "rejects invalid launch defaults instead of silently ignoring them", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\nasync: sometimes\n---\n\nDo work\n")
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `Agent 'worker' has invalid async frontmatter; expected true or false`)
	})
	tw(t, f, "rejects oversized toolTimeoutMs frontmatter at discovery", func(t *testing.T) {
		dir := fmProject(t)
		fmAgent(t, dir, "worker", "---\nname: worker\ndescription: Worker\ntoolTimeoutMs: 2147483648\n---\n\nDo work\n")
		hasRe(t, diagError(discoverProject(t, dir, ScopeProject), 0), `Agent 'worker' has invalid toolTimeoutMs frontmatter; expected a positive integer no larger than 2147483647`)
	})
	tw(t, f, "prefers same-scope .chain.json over .chain.md for the same runtime name", func(t *testing.T) {
		dir := fmProject(t)
		chains := filepath.Join(dir, ".pi", "chains")
		writeFile(t, filepath.Join(chains, "dynamic-review.chain.md"), "---\nname: dynamic-review\ndescription: Markdown fallback\n---\n\n## scout\n\nRun the markdown chain\n")
		writeFile(t, filepath.Join(chains, "dynamic-review.chain.json"), marshalJSON(m{"name": "dynamic-review", "description": "JSON dynamic chain", "chain": l{
			m{"agent": "scout", "task": "Return targets", "as": "targets", "outputSchema": m{"type": "object"}},
			m{"expand": m{"from": m{"output": "targets", "path": "/items"}, "maxItems": 4.0}, "parallel": m{"agent": "reviewer", "task": "Review {item.path}"}, "collect": m{"as": "reviews"}},
		}}, ""))
		d := dAll(t, dir)
		var chain *ChainConfig
		for i := range d.Chains {
			if d.Chains[i].Name == "dynamic-review" {
				chain = &d.Chains[i]
			}
		}
		eq(t, chain.Description, "JSON dynamic chain", "description")
		eq(t, filepath.Ext(chain.FilePath), ".json", "json file")
		_, has := chain.JSONSteps[1].(*jsObject).get("expand")
		eq(t, has, true, "expand")
	})
}
