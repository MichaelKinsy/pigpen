package pi_subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Twins of test/unit/agent-frontmatter.test.ts: the agent frontmatter fields, runners, package manifests and project roots.

// fmProject is an isolated HOME and an empty project, the original's withTempHome plus mkdtemp.
func fmProject(t *testing.T) string {
	t.Helper()
	withTempHome(t)
	t.Setenv("PI_OFFLINE", "true")
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	return tmp(t)
}

func fmAgent(t *testing.T, project, name, body string) string {
	t.Helper()
	p := filepath.Join(project, ".pi", "agents", name+".md")
	writeFile(t, p, body)
	return p
}

func discoverProject(t *testing.T, project string, scope AgentScope) Discovery {
	t.Helper()
	return dAgents(t, project, scope, "")
}

func diagError(d Discovery, i int) string {
	if i < len(d.Diagnostics) {
		return d.Diagnostics[i].Error
	}
	return ""
}

func serializeNew(a *AgentConfig) string { return serializeAgent(*a, SerializeAgentOptions{}) }

func TestAgentFrontmatterSchemaAndRunners(t *testing.T) {
	const f = "agent-frontmatter"
	tw(t, f, "parses and serializes an inline object schema", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "typed", "---\nname: typed\ndescription: Typed agent\noutputSchema: {\"type\":\"object\",\"required\":[\"ok\"],\"properties\":{\"ok\":{\"type\":\"boolean\"}}}\n---\n\nReturn data.\n")
		typed := findAgent(discoverProject(t, project, ScopeProject).Agents, "typed")
		want := `{"properties":{"ok":{"type":"boolean"}},"required":["ok"],"type":"object"}`
		eq(t, canonJSON(typed.OutputSchema), want, "schema")
		fmAgent(t, project, "typed", serializeNew(typed))
		clearAgentDiscoveryCache()
		eq(t, canonJSON(findAgent(discoverProject(t, project, ScopeProject).Agents, "typed").OutputSchema), canonJSON(typed.OutputSchema), "round trip")
	})
	tw(t, f, "rejects malformed, null, and array output schemas", func(t *testing.T) {
		project := fmProject(t)
		for _, c := range [][2]string{{"malformed", "{"}, {"null", "null"}, {"array", "[]"}} {
			fmAgent(t, project, c[0], "---\nname: "+c[0]+"\ndescription: Invalid schema\noutputSchema: "+c[1]+"\n---\nBody\n")
		}
		d := discoverProject(t, project, ScopeProject)
		for _, n := range []string{"malformed", "null", "array"} {
			eq(t, hasName(d.Agents, n), false, n)
		}
		eq(t, len(d.Diagnostics), 3, "diagnostics")
		var malformed string
		objects := 0
		for _, g := range d.Diagnostics {
			if g.Name == "malformed" {
				malformed = g.Error
			}
			if regexpMatch(`(?i)outputSchema.*object`, g.Error) {
				objects++
			}
		}
		hasRe(t, malformed, `(?i)JSON|position|property|unexpected|invalid`)
		eq(t, objects, 2, "object diagnostics")
	})
	tw(t, f, "preserves omission and empty lists, round-trips canonical lists, and rejects malformed names", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "omitted", "---\nname: omitted\ndescription: Omitted\n---\n\nPrompt.\n")
		fmAgent(t, project, "empty", "---\nname: empty\ndescription: Empty\nallowedAgents:\n---\n\nPrompt.\n")
		fmAgent(t, project, "listed", "---\nname: listed\ndescription: Listed\nallowedAgents: worker, scout\n---\n\nPrompt.\n")
		fmAgent(t, project, "blocked", "---\nname: blocked\ndescription: Blocked\nallowedAgents:\n  - reviewer\n  - package.scout\n---\n\nPrompt.\n")
		fmAgent(t, project, "invalid", "---\nname: invalid\ndescription: Invalid\nallowedAgents: bad name\n---\n\nPrompt.\n")
		d := discoverProject(t, project, ScopeProject)
		eq(t, findAgent(d.Agents, "omitted").AllowedAgents == nil, true, "omitted")
		empty := findAgent(d.Agents, "empty").AllowedAgents
		eq(t, empty != nil && len(empty) == 0, true, "empty")
		eq(t, findAgent(d.Agents, "listed").AllowedAgents, []string{"scout", "worker"}, "listed")
		eq(t, findAgent(d.Agents, "blocked").AllowedAgents, []string{"package.scout", "reviewer"}, "blocked")
		var invalid string
		for _, g := range d.Diagnostics {
			if g.Name == "invalid" {
				invalid = g.Error
			}
		}
		hasRe(t, invalid, `Invalid capability ceiling allowedAgents entry 'bad name'`)
		listed := findAgent(d.Agents, "listed")
		fmAgent(t, project, "listed", serializeNew(listed))
		clearAgentDiscoveryCache()
		eq(t, findAgent(discoverProject(t, project, ScopeProject).Agents, "listed").AllowedAgents, []string{"scout", "worker"}, "round trip")
	})
	tw(t, f, "distinguishes absent, empty, candidates, unreadable, and non-directory paths", func(t *testing.T) {
		root := tmp(t)
		empty, candidates, file := filepath.Join(root, "empty"), filepath.Join(root, "candidates"), filepath.Join(root, "not-directory")
		mkdirAll(t, empty)
		mkdirAll(t, candidates)
		writeFile(t, filepath.Join(candidates, "worker.md"), "---\nname: worker\ndescription: Worker\n---\n")
		writeFile(t, file, "not a directory")
		eq(t, inspectAgentDefinitionDirectory(filepath.Join(root, "absent"), nil).state, "absent", "absent")
		eq(t, inspectAgentDefinitionDirectory(empty, nil).state, "empty", "empty")
		got := inspectAgentDefinitionDirectory(candidates, nil)
		eq(t, got.state, "candidates", "candidates")
		eq(t, got.files, []string{filepath.Join(candidates, "worker.md")}, "files")
		eq(t, inspectAgentDefinitionDirectory(file, nil).state, "not-directory", "file")
		// The original injects a readdirSync that throws; Go has no such seam, so a real directory without read permission stands in.
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Log("permission denial cannot be provoked on this platform or as root")
			return
		}
		blocked := filepath.Join(root, "blocked")
		mkdirAll(t, blocked)
		if err := os.Chmod(blocked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(blocked, 0o755) })
		eq(t, inspectAgentDefinitionDirectory(blocked, nil).state, "unreadable", "unreadable")
	})
	tw(t, f, "reports cached builtin and configured project definition paths without synthesizing a root", func(t *testing.T) {
		project := fmProject(t)
		wjc(t, filepath.Join(project, ".pi", "settings.json"), sub(m{"projectRootResolution": "nearest"}))
		d := discoverProject(t, project, ScopeProject)
		builtin := false
		var projectPaths []string
		for _, r := range d.Directories {
			if r.Source == SourceBuiltin {
				builtin = true
			}
			if r.Source == SourceProject {
				projectPaths = append(projectPaths, r.Path)
			}
		}
		eq(t, builtin, true, "builtin directory")
		eq(t, projectPaths, []string{filepath.Join(project, ".agents"), filepath.Join(project, ".pi", "agents")}, "project directories")
	})
	tw(t, f, "parses and serializes an external-cli runner", func(t *testing.T) {
		project := fmProject(t)
		cmd := filepath.Join(project, "run-external")
		fmAgent(t, project, "external", "---\nname: external\ndescription: External runner\nrunner:\n  type: external-cli\n  command: "+marshalJSON(cmd, "")+"\n  args: [\"-e\", \"process.stdin.pipe(process.stdout)\"]\n  promptDelivery: stdin\nasync: true\n---\nReview carefully.")
		external := findAgent(discoverProject(t, project, ScopeProject).Agents, "external")
		eq(t, canonJSON(runnerToJS(external.Runner)), canonJSON(m{"type": "external-cli", "command": cmd, "args": l{"-e", "process.stdin.pipe(process.stdout)"}, "promptDelivery": "stdin"}), "runner")
		hasRe(t, serializeNew(external), `runner:\n  type: external-cli\n  command:`)
		clearAgentDiscoveryCache()
		eq(t, canonJSON(runnerToJS(findAgent(discoverProject(t, project, ScopeProject).Agents, "external").Runner)), canonJSON(runnerToJS(external.Runner)), "again")
	})
	tw(t, f, "keeps external-cli stop code-owned while refusing capability widening", func(t *testing.T) {
		project := fmProject(t)
		write := func(cap string) {
			fmAgent(t, project, "external", "---\nname: external\ndescription: External runner\nrunner:\n  type: external-cli\n  command: node\n  capabilities:\n    "+cap+"\n---\nReview.")
			clearAgentDiscoveryCache()
		}
		write("steer: false")
		eq(t, canonJSON(runnerToJS(findAgent(discoverProject(t, project, ScopeProject).Agents, "external").Runner)), `{"capabilities":{"steer":false},"command":"node","type":"external-cli"}`, "narrowed")
		write("stop: false")
		d := discoverProject(t, project, ScopeProject)
		eq(t, hasName(d.Agents, "external"), false, "stop agent")
		hasRe(t, diagError(d, 0), `capabilities has unsupported fields: stop`)
		write("steer: true")
		d = discoverProject(t, project, ScopeProject)
		eq(t, hasName(d.Agents, "external"), false, "widened agent")
		hasRe(t, diagError(d, 0), `steer may only be false; user config cannot widen code-owned external adapter capabilities`)
	})
	tw(t, f, "parses and serializes an external-job runner", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "gpt-pro", "---\nname: gpt-pro\ndescription: Surf GPT Pro advisor\nrunner:\n  type: external-job\n  provider: surf-oracle\n  options:\n    tier: pro\nasync: true\n---\nReview carefully.")
		gptPro := findAgent(discoverProject(t, project, ScopeProject).Agents, "gpt-pro")
		eq(t, canonJSON(runnerToJS(gptPro.Runner)), `{"options":{"tier":"pro"},"provider":"surf-oracle","type":"external-job"}`, "runner")
		hasRe(t, serializeNew(gptPro), `runner:\n  type: external-job\n  provider: surf-oracle`)
	})
	tw(t, f, "rejects invalid and Pi-only external runner fields", func(t *testing.T) {
		cases := []string{
			"type: unknown\n  command: node",
			"type: external-cli\n  command: ''",
			"type: external-cli\n  command: node\n  args: nope",
			"type: external-cli\n  command: node\n  args: [ok, 1]",
			"type: external-cli\n  command: node\n  promptDelivery: argv",
			"type: external-job\n  provider: ''",
			"type: external-job\n  provider: surf-oracle\n  options: []",
		}
		for _, runner := range cases {
			project := fmProject(t)
			fmAgent(t, project, "external", "---\nname: external\ndescription: External\nrunner:\n  "+runner+"\n---\nBody")
			d := discoverProject(t, project, ScopeProject)
			eq(t, hasName(d.Agents, "external"), false, runner)
			hasRe(t, diagError(d, 0), `invalid runner\.type|non-empty command|args must be an array of strings|promptDelivery must be 'stdin'|provider string|options must be a JSON-serializable object`)
		}
		project := fmProject(t)
		fmAgent(t, project, "external", "---\nname: external\ndescription: External\nrunner:\n  type: external-cli\n  command: node\nmodel: provider/model\n---\nBody")
		hasRe(t, diagError(discoverProject(t, project, ScopeProject), 0), `unsupported Pi-only fields: model`)
		fmAgent(t, project, "external", "---\nname: external\ndescription: External\nrunner:\n  type: external-cli\n  command: node\nallowedAgents: worker\n---\nBody")
		clearAgentDiscoveryCache()
		hasRe(t, diagError(discoverProject(t, project, ScopeProject), 0), `unsupported Pi-only fields: allowedAgents`)
	})
	tw(t, f, "follows symlinked agent directories once without hiding diagnostics", func(t *testing.T) {
		project := fmProject(t)
		legacy := filepath.Join(project, ".agents")
		shared := filepath.Join(project, "shared-agents")
		linked := filepath.Join(legacy, "agents")
		mkdirAll(t, legacy)
		mkdirAll(t, shared)
		writeFile(t, filepath.Join(shared, "linked.md"), "---\nname: linked\ndescription: Linked agent\n---\nBody")
		writeFile(t, filepath.Join(shared, "broken.md"), "---\nname: broken\ndescription: Broken\nrunner:\n  type: unknown\n---\nBody")
		symlink(t, shared, linked)
		symlink(t, filepath.Join(shared, "missing"), filepath.Join(shared, "dangling"))
		if runtime.GOOS != "windows" {
			symlink(t, shared, filepath.Join(shared, "loop"))
		}
		d := discoverProject(t, project, ScopeProject)
		eq(t, findAgent(d.Agents, "linked").FilePath, filepath.Join(linked, "linked.md"), "linked path")
		var brokenPath string
		for _, g := range d.Diagnostics {
			if g.Name == "broken" {
				brokenPath = g.FilePath
			}
		}
		eq(t, brokenPath, filepath.Join(linked, "broken.md"), "diagnostic path")
	})
	tw(t, f, "keeps a lower-priority agent available when a project override is malformed", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "reviewer", "---\nname: reviewer\ndescription: Broken reviewer\nrunner:\n  type: unknown\n---\nBody")
		d := discoverProject(t, project, ScopeBoth)
		eq(t, findAgent(d.Agents, "reviewer").Source, SourceBuiltin, "reviewer source")
		var src AgentSource
		for _, g := range d.Diagnostics {
			if g.Name == "reviewer" {
				src = g.Source
			}
		}
		eq(t, src, SourceProject, "diagnostic source")
	})
	tw(t, f, "records the runtime name for malformed packaged agents", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "code-analysis.zeta-worker", "---\nname: zeta-worker\npackage: code-analysis\ndescription: Broken packaged worker\nrunner:\n  type: unknown\n---\nBody")
		d := discoverProject(t, project, ScopeProject)
		eq(t, hasName(d.Agents, "code-analysis.zeta-worker"), false, "agent")
		eq(t, len(d.Diagnostics) > 0, true, "diagnostics")
		eq(t, [2]string{d.Diagnostics[0].Name, d.Diagnostics[0].RuntimeName}, [2]string{"zeta-worker", "code-analysis.zeta-worker"}, "names")
	})
	tw(t, f, "parses and serializes comma-separated paths", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "worker", "---\nname: worker\ndescription: Worker\nskillPath: ./skills, ../shared-skills\n---\nbody")
		worker := findAgent(discoverProject(t, project, ScopeBoth).Agents, "worker")
		eq(t, worker.SkillPath, []string{"./skills", "../shared-skills"}, "skillPath")
		hasRe(t, serializeNew(worker), `(?m)^skillPath: \./skills, \.\./shared-skills$`)
	})
	tw(t, f, "parses and serializes explicit prompt advertisement", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "worker", "---\nname: worker\ndescription: Worker\nadvertise: true\n---\nbody")
		worker := findAgent(discoverProject(t, project, ScopeBoth).Agents, "worker")
		eq(t, worker.Advertise != nil && *worker.Advertise, true, "advertise")
		hasRe(t, serializeNew(worker), `(?m)^advertise: true$`)
	})
	tw(t, f, "rejects non-boolean advertise values", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "worker", "---\nname: worker\ndescription: Worker\nadvertise: yes\n---\nbody")
		hasRe(t, diagError(discoverProject(t, project, ScopeProject), 0), `invalid advertise frontmatter; expected true or false`)
	})
	tw(t, f, "parses and serializes agent aliases", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "worker", "---\nname: worker\ndescription: Worker\naliases: developer, coder, worker\n---\nbody")
		worker := findAgent(discoverProject(t, project, ScopeBoth).Agents, "worker")
		eq(t, worker.Aliases, []string{"developer", "coder"}, "aliases")
		hasRe(t, serializeNew(worker), `(?m)^aliases: developer, coder$`)
		ctx := testMgmtCtx(project)
		hasRe(t, handleManagementAction("list", map[string]any{}, ctx).text, `aliases: developer, coder`)
		hasRe(t, handleManagementAction("get", map[string]any{"agent": "developer"}, ctx).text, `Agent: worker`)
	})
	tw(t, f, "reports management alias collisions as ambiguous", func(t *testing.T) {
		project := fmProject(t)
		fmAgent(t, project, "review-agent", "---\nname: review-agent\ndescription: Custom reviewer\naliases: developer\n---\nbody")
		ctx := testMgmtCtx(project)
		get := handleManagementAction("get", map[string]any{"agent": "developer"}, ctx)
		eq(t, get.isError, true, "get")
		hasRe(t, get.text, `Ambiguous agent alias or name 'developer': review-agent, worker`)
		dis := handleManagementAction("disable", map[string]any{"agent": "developer"}, ctx)
		eq(t, dis.isError, true, "disable")
		hasRe(t, dis.text, `Ambiguous agent alias 'developer': worker, review-agent|Ambiguous agent alias 'developer': review-agent, worker`)
	})
}
