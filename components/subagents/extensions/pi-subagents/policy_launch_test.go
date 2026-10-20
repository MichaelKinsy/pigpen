package pi_subagents

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests of the launch identity pieces that preflight and every launch path share (policy_launch.go, policy_toolplan.go,
// policy_mcpconfig.go, policy_mcpgrant.go, policy_refinement.go), beyond what the twins of preflight.test.ts reach.

func TestStableJSON(t *testing.T) {
	equal(t, stableJSON(map[string]any{"b": float64(1), "a": []any{float64(2), "x"}}), `{"a":[2,"x"],"b":1}`, "sorted keys")
	obj := newObject()
	obj.set("z", undef{})
	obj.set("y", nil)
	obj.set("B", "q\"")
	obj.set("a", true)
	equal(t, stableJSON(obj), `{"a":true,"B":"q\"","y":null}`, "locale order, undefined left out, null kept")
	equal(t, stableJSON([]any{map[string]any{"k": "v"}}), `[{"k":"v"}]`, "nested")
	equal(t, stableJSONDigest("x"), "ba2df4903a2c14e86dc3bcca58911b44ac1d2514b7227bf6eb08cfb978f55a1b", "digest of a string")
}

func TestLaunchDigestsBindTheirInputs(t *testing.T) {
	e := newPfEnv(t)
	cwd := e.dir("repo")
	agentPath := filepath.Join(cwd, ".pi", "agents", "worker.md")
	writeFile(t, agentPath, "---\nname: worker\ndescription: Project worker\ntools: read, write\n---\nPrompt.\n")
	base := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "one"})
	other := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "two"})
	equal(t, base.LaunchContractDigest != other.LaunchContractDigest, true, "the task is bound")
	equal(t, base.Agent.DefinitionDigest, other.Agent.DefinitionDigest, "the task is not part of the definition")
	// a change the parsed definition does not show still changes the file digest
	writeFile(t, agentPath, "---\nname: worker\ndescription: Project worker\ntools: read, write\nunknownField: 1\n---\nPrompt.\n")
	changed := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "one"})
	equal(t, changed.Agent.DefinitionDigest != base.Agent.DefinitionDigest, true, "file content is bound into the definition")
	// excluded tools are bound even when the allowlist does not change
	writeFile(t, agentPath, "---\nname: worker\ndescription: Project worker\ntools: read, write\nexcludeTools: unknown_tool\n---\nPrompt.\n")
	excluded := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "one"})
	equal(t, excluded.Tools.EffectiveAllowlist, base.Tools.EffectiveAllowlist, "same allowlist")
	equal(t, excluded.LaunchContractDigest != base.LaunchContractDigest, true, "excluded tools are bound")
	// a model, the thinking level and the output path are bound
	writeFile(t, agentPath, "---\nname: worker\ndescription: Project worker\ntools: read, write\n---\nPrompt.\n")
	withModel := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "one", Model: "test/other", AvailableModels: pfModels("test", "other")})
	equal(t, withModel.LaunchContractDigest != base.LaunchContractDigest, true, "model is bound")
	withOutput := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "one", Output: "out.md"})
	equal(t, withOutput.LaunchContractDigest != base.LaunchContractDigest, true, "output path is bound")
	equal(t, filepath.Base(withOutput.Roots.OutputPath), "out.md", "output path")
	noOutput := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "one", Output: false})
	equal(t, noOutput.Roots.OutputPath, "", "output disabled")
	disabledArtifacts := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Artifacts: new(false)})
	equal(t, disabledArtifacts.Roots.ArtifactsDir, "", "artifacts disabled")
	equal(t, disabledArtifacts.Roots.ArtifactPaths == nil, true, "no artifact paths")
	enabled := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, RunID: "r1", ArtifactDir: "project"})
	equal(t, enabled.Roots.ArtifactsDir, filepath.Join(cwd, ".pi", "subagents", "artifacts"), "project artifacts")
	equal(t, filepath.Base(enabled.Roots.ArtifactPaths.InputPath), "r1_worker_0_input.md", "artifact name")
}

func TestPreflightRefusals(t *testing.T) {
	e := newPfEnv(t)
	cwd := e.dir("repo")
	file := filepath.Join(e.temp, "afile")
	writeFile(t, file, "x")
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nPrompt.\n")
	r := pfResolve(t, launchContractInput{Agent: "worker", Cwd: file})
	equal(t, r.Code, "invalid_cwd", "a file is not a directory")
	equal(t, r.Message, "cwd '"+file+"' is not a directory.", "message")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Context: new("bogus")})
	equal(t, r.Message, "Unsupported context 'bogus'; expected 'fresh' or 'fork'.", "context message")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, ArtifactDir: "bogus"})
	equal(t, r.Message, "Unsupported artifactDir 'bogus'; expected 'project', 'session', or 'temp'.", "artifact dir message")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Skill: "pi-subagents"})
	equal(t, r.Code, "missing_skill", "the orchestration skill is not injectable")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Skill: "nope, nada"})
	equal(t, r.Message, "Missing skills: nope, nada", "missing skills")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, ExtensionBindings: js(t, `[]`)})
	equal(t, r.Message, "extensionBindings must be a plain JSON object.", "bindings shape")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Fast: new(true)})
	equal(t, r.Code, "denied_required_tool", "fast needs a model")
	equal(t, r.Message, "fast mode requires an explicit supported native OpenAI-Codex model for agent 'worker'.", "fast message")
	r = pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Fast: new(true), Model: "test/m", AvailableModels: pfModels("test", "m")})
	equal(t, r.Message, "fast mode supports only native openai-codex/* models; unsupported model: test/m.", "fast provider")
	// the diagnostics of a launch without a snapshot of the host
	c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Model: "test/m"})
	var codes []string
	for _, d := range c.Diagnostics {
		codes = append(codes, d.Code+":"+d.Severity)
	}
	equal(t, codes, []string{"host_required:host-required", "host_required:host-required"}, "session paths and model snapshot")
	polMatches(t, c.Diagnostics[1].Message, `No availableModels snapshot was supplied`)
	// nested lifecycle paths need safe ids
	nested := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, RunID: "child", NestedRootRunID: "root"})
	equal(t, strings.HasSuffix(nested.Roots.Lifecycle.AsyncDir, filepath.Join("nested-subagent-runs", "root", "child")), true, "nested async dir")
	equal(t, strings.HasSuffix(nested.Roots.Lifecycle.ResultPath, filepath.Join("nested", "root", "child.json")), true, "nested result path")
	_, err := resolveSubagentLaunchContract(launchContractInput{Agent: "worker", Cwd: cwd, RunID: "child", NestedRootRunID: "../x"})
	if err == nil || err.Error() != "rootRunId must be a non-empty safe id token." {
		t.Errorf("unsafe root run id: %v", err)
	}
}

func TestUnknownAgentMessage(t *testing.T) {
	d := Discovery{Cwd: "/work", Directories: []DirectoryReport{
		{Source: SourceProject, Path: "/work/.pi/agents", State: "candidates", CandidateCount: 2},
		{Source: SourceUser, Path: "/home/.agents", State: "candidates", CandidateCount: 1},
		{Source: SourceUser, Path: "/home/a", State: "empty"},
		{Source: SourceProject, Path: "/work/f", State: "not-directory"},
		{Source: SourceProject, Path: "/work/g", State: "absent"},
	}, Agents: []AgentConfig{{Name: "b", Source: SourceUser}, {Name: "a", Source: SourceProject}, {Name: "a", Source: SourceBuiltin}}}
	equal(t, formatUnknownAgentError("x", d, "Unknown agent"), strings.Join([]string{"Unknown agent: x", "Effective cwd: /work", "Consulted agent-definition directories:",
		"- project: /work/.pi/agents (2 candidates)", "- user: /home/.agents (1 candidate)", "- user: /home/a (present but empty)", "- project: /work/f (not a directory)", "- project: /work/g (absent)",
		"Discovered agents:", "- a (builtin)", "- a (project)", "- b (user)"}, "\n"), "message")
	equal(t, formatUnknownAgentError("x", Discovery{Cwd: "/w"}, "Unknown agent"), "Unknown agent: x\nEffective cwd: /w\nConsulted agent-definition directories:\n- (none)\nDiscovered agents:\n- (none)", "empty")
}

func TestWorkspaceScopeAuthority(t *testing.T) {
	for _, task := range []string{
		"Add a new package to the monorepo but only edit packages/api",
		"create the widget package, without root metadata changes",
		"Make a package for the workspace. Do not touch the lockfile.",
		"   ",
		"",
	} {
		d := taskWorkspaceScopeAuthorityDiagnostic(task)
		want := strings.TrimSpace(task) != ""
		equal(t, d != nil, want, task)
	}
	for _, task := range []string{"Create a new package and update the root workspace metadata", "only edit packages/api", "Fix the bug"} {
		equal(t, taskWorkspaceScopeAuthorityDiagnostic(task) == nil, true, task)
	}
	d := taskWorkspaceScopeAuthorityDiagnostic("Create a package in the workspace; do not edit root files")
	equal(t, d.Code+"/"+d.Severity, "workspace_scope_authority/warning", "diagnostic")
}

func TestExtensionBindings(t *testing.T) {
	n, err := normalizeExtensionBindings(js(t, `{"a.b/12":{"y":[2,{"q":1,"p":2}],"x":null}}`))
	equal(t, err, nil, "valid")
	equal(t, n != nil, true, "set")
	equal(t, n.json, `{"a.b/12":{"x":null,"y":[2,{"p":2,"q":1}]}}`, "canonical json")
	equal(t, marshalJSON(n.value, ""), n.json, "canonical value")
	n, err = normalizeExtensionBindings(nil)
	equal(t, [2]any{n != nil, err}, [2]any{false, error(nil)}, "undefined")
	bad := map[string]string{
		`[]`:                 "extensionBindings must be a plain JSON object.",
		`{"x":1}`:            "extensionBindings namespace 'x' must use a package-like name followed by '/<positive-version>'.",
		`{"x/0":1}`:          "extensionBindings namespace 'x/0' must use a package-like name followed by '/<positive-version>'.",
		`{"x/1000000000":1}`: "extensionBindings namespace 'x/1000000000' must use a package-like name followed by '/<positive-version>'.",
		`{"-x/1":1}`:         "extensionBindings namespace '-x/1' must use a package-like name followed by '/<positive-version>'.",
		`{"x/1":{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":{"j":{"k":{"l":{"m":{"n":{"o":{"p":{"q":1}}}}}}}}}}}}}}}}}}`: "extensionBindings.x/1.a.b.c.d.e.f.g.h.i.j.k.l.m.n.o.p exceeds the maximum nesting depth of 16.",
	}
	for in, want := range bad {
		_, err := normalizeExtensionBindings(js(t, in))
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
	var many strings.Builder
	many.WriteString("{")
	for i := range 17 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`"n` + strconv.Itoa(i) + `/1":1`)
	}
	many.WriteString("}")
	_, err = normalizeExtensionBindings(js(t, many.String()))
	if err == nil || err.Error() != "extensionBindings supports at most 16 namespaces." {
		t.Errorf("namespaces: %v", err)
	}
	_, err = normalizeExtensionBindings(js(t, `{"x/1":"`+strings.Repeat("a", 16*1024)+`"}`))
	if err == nil || !strings.HasPrefix(err.Error(), "extensionBindings canonical JSON is ") || !strings.HasSuffix(err.Error(), " bytes; maximum is 16384.") {
		t.Errorf("bytes: %v", err)
	}
	props := newObject()
	inner := newObject()
	for i := range 257 {
		inner.set("k"+strconv.Itoa(i), float64(i))
	}
	props.set("x/1", inner)
	_, err = normalizeExtensionBindings(props)
	if err == nil || err.Error() != "extensionBindings exceeds 256 total properties." {
		t.Errorf("properties: %v", err)
	}
}

func TestRequiredChildExtensions(t *testing.T) {
	e := newPfEnv(t)
	real := filepath.Join(e.temp, "ext.mjs")
	writeFile(t, real, "export default () => {};\n")
	link := filepath.Join(e.temp, "link.mjs")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks")
	}
	other := filepath.Join(e.temp, "other.mjs")
	writeFile(t, other, "x")
	dispose, err := registerRequiredChildExtensions("s1", []requiredChildExtension{{ID: "a", Path: link}})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, resolveRequiredChildExtensions("s1"), []requiredChildExtension{{ID: "a", Path: real}}, "canonical path")
	equal(t, len(resolveRequiredChildExtensions("other")), 0, "other session")
	_, err = registerRequiredChildExtensions("s1", nil)
	equal(t, err.Error(), "Required child extensions are already registered for session 's1'; dispose them first.", "double registration")
	dispose()
	dispose()
	equal(t, len(resolveRequiredChildExtensions("s1")), 0, "disposed")
	again, err := registerRequiredChildExtensions("s1", []requiredChildExtension{{ID: "b", Path: other}})
	if err != nil {
		t.Fatal(err)
	}
	dispose() // a stale disposer must not drop the newer registration
	equal(t, len(resolveRequiredChildExtensions("s1")), 1, "stale disposer")
	again()
	cases := []struct {
		session string
		list    []requiredChildExtension
		want    string
	}{
		{"", nil, "Required child extension registration requires a non-empty trimmed sessionId of at most 256 characters without NUL."},
		{" x", nil, "Required child extension registration requires a non-empty trimmed sessionId of at most 256 characters without NUL."},
		{"s2", []requiredChildExtension{{"bad id", real}}, "Required child extensions entry 0 requires a safe id of at most 128 characters."},
		{"s2", []requiredChildExtension{{"a", real}, {"a", other}}, "Required child extensions id 'a' is duplicated."},
		{"s2", []requiredChildExtension{{"a", real}, {"b", link}}, "Required child extensions path '" + real + "' is duplicated."},
		{"s2", []requiredChildExtension{{"a", "  "}}, "Required child extensions 'a' requires a non-empty path of at most 4096 bytes without NUL."},
		{"s2", []requiredChildExtension{{"a", e.temp}}, "Required child extensions 'a' is not an importable file: not a file"},
	}
	for _, c := range cases {
		_, err := registerRequiredChildExtensions(c.session, c.list)
		if err == nil || err.Error() != c.want {
			t.Errorf("%q %v: %v, want %q", c.session, c.list, err, c.want)
		}
	}
	_, err = snapshotRequiredChildExtensions([]requiredChildExtension{{"a", "relative.mjs"}}, "Required child extensions", false)
	equal(t, err.Error(), "Required child extensions 'a' requires an absolute non-empty path of at most 4096 bytes without NUL.", "absolute path")
	many := make([]requiredChildExtension, 33)
	_, err = snapshotRequiredChildExtensions(many, "L", false)
	equal(t, err.Error(), "L must be an array of at most 32 entries.", "too many")
}

func TestIntercomBridge(t *testing.T) {
	e := newPfEnv(t)
	_, msg := validateIntercomBridgeConfig(js(t, `[]`), "b")
	equal(t, msg, "b must be an object.", "shape")
	for in, want := range map[string]string{
		`{"x":1}`:               "b.x is not supported.",
		`{"mode":"x"}`:          "b.mode is invalid.",
		`{"instructionFile":1}`: "b.instructionFile must be a string.",
		`{"resultDelivery":1}`:  "b.resultDelivery must be a boolean.",
	} {
		_, got := validateIntercomBridgeConfig(js(t, in), "b")
		equal(t, got, want, in)
	}
	cfg, msg := validateIntercomBridgeConfig(js(t, `{"mode":"fork-only","instructionFile":"f","resultDelivery":true}`), "b")
	equal(t, msg, "", "valid")
	equal(t, [3]any{cfg.Mode, *cfg.InstructionFile, *cfg.ResultDelivery}, [3]any{"fork-only", "f", true}, "values")

	// the default bridge is active for a fresh launch with a target; the instruction carries the marker once
	s := resolveIntercomBridge(nil, nil, "fresh", " main ")
	equal(t, [4]any{s.Active, s.Mode, s.OrchestratorTarget, s.ExtensionDir}, [4]any{true, "always", "main", "native:pi-subagents-supervisor-channel"}, "default state")
	equal(t, strings.HasPrefix(s.Instruction, "Intercom orchestration channel:\n"), true, "marker")
	equal(t, strings.Count(s.Instruction, "Intercom orchestration channel:"), 1, "marker once")
	equal(t, s.InterpolatesOrchestratorTarget, false, "default template names no session")
	// inactive states
	for _, c := range []struct {
		cfg     string
		context string
		target  string
	}{{`{"mode":"off"}`, "fresh", "main"}, {`{"mode":"fork-only"}`, "fresh", "main"}, {`{"mode":"always"}`, "fresh", "  "}} {
		over, _ := validateIntercomBridgeConfig(js(t, c.cfg), "b")
		st := resolveIntercomBridge(nil, &over, c.context, c.target)
		equal(t, st.Active, false, c.cfg+" "+c.target)
		equal(t, st.OrchestratorTarget, "", "no target when inactive")
		equal(t, strings.Contains(st.Instruction, "{orchestratorTarget}"), true, "placeholder kept when inactive")
	}
	fork, _ := validateIntercomBridgeConfig(js(t, `{"mode":"fork-only"}`), "b")
	equal(t, resolveIntercomBridge(nil, &fork, "fork", "main").Active, true, "fork-only with a fork")
	equal(t, resolveIntercomBridge(js(t, `{"mode":"off","resultDelivery":true}`), nil, "fresh", "main").ResultDelivery, true, "config result delivery")
	equal(t, resolveIntercomBridge(js(t, `{"mode":"bogus"}`), nil, "fresh", "main").Mode, "always", "unknown mode is always")
	// the override replaces the config
	off, _ := validateIntercomBridgeConfig(js(t, `{"mode":"off"}`), "b")
	equal(t, resolveIntercomBridge(js(t, `{"mode":"always","resultDelivery":true}`), &off, "fresh", "main").ResultDelivery, false, "override replaces")

	// a custom instruction file: relative to the config directory, "~/" and the default when unreadable
	cfgDir := filepath.Join(e.agentDir, "extensions", "subagent")
	writeFile(t, filepath.Join(cfgDir, "rel.md"), "  Talk to {orchestratorTarget} and {orchestratorTarget}.  \n")
	writeFile(t, filepath.Join(e.home, "home.md"), "Intercom orchestration channel:\nHome.")
	for _, c := range []struct {
		file, want   string
		interpolates bool
	}{
		{"rel.md", "Intercom orchestration channel:\nTalk to main and main.", true},
		{"~/home.md", "Intercom orchestration channel:\nHome.", false},
		{"missing.md", resolveIntercomBridge(nil, nil, "fresh", "main").Instruction, false},
	} {
		over, _ := validateIntercomBridgeConfig(js(t, `{"instructionFile":`+strconv.Quote(c.file)+`}`), "b")
		st := resolveIntercomBridge(nil, &over, "fresh", "main")
		equal(t, st.Instruction, c.want, c.file)
		equal(t, st.InterpolatesOrchestratorTarget, c.interpolates, c.file+" interpolates")
	}

	// applying the bridge to an agent
	active := resolveIntercomBridge(nil, nil, "fresh", "main")
	a := AgentConfig{Name: "w", Tools: []string{"read"}, SystemPrompt: "  Base.  ", FilePath: "/nonexistent"}
	b := applyIntercomBridgeToAgent(a, active)
	equal(t, b.Tools, []string{"read", "contact_supervisor"}, "tool added")
	equal(t, b.SystemPrompt, "Base.\n\n"+active.Instruction, "prompt")
	equal(t, b.DefinitionDigest, agentDefinitionDigest(a), "definition digest captured")
	again := applyIntercomBridgeToAgent(b, active)
	equal(t, again.Tools, b.Tools, "tool added once")
	equal(t, again.SystemPrompt, b.SystemPrompt, "marker keeps the prompt")
	unrestricted := applyIntercomBridgeToAgent(AgentConfig{Name: "w"}, active)
	equal(t, unrestricted.Tools == nil, true, "an unrestricted agent stays unrestricted")
	equal(t, unrestricted.SystemPrompt, active.Instruction, "empty prompt")
	inactive := resolveIntercomBridge(nil, &off, "fresh", "main")
	equal(t, applyIntercomBridgeToAgent(a, inactive).SystemPrompt, a.SystemPrompt, "inactive leaves the agent")
}

func TestLaunchContextAndStepBehavior(t *testing.T) {
	for _, c := range []struct {
		explicit, agent, global string
		canFork                 bool
		want                    string
	}{
		{"fresh", "fork", "fork", true, "fresh"}, {"fork", "", "", false, "fork"}, {"", "fork", "", true, "fork"}, {"", "fork", "", false, "fresh"},
		{"", "", "fork", true, "fork"}, {"", "fork", "fresh", true, "fresh"}, {"", "", "", true, "fresh"},
	} {
		equal(t, resolveSubagentLaunchContext(c.explicit, c.agent, c.global, c.canFork), c.want, c.explicit+"/"+c.agent+"/"+c.global)
	}
	e := newPfEnv(t)
	session := filepath.Join(e.temp, "s.jsonl")
	writeFile(t, session, "{}\n")
	equal(t, canPreferForkFromSnapshot(session, "leaf"), true, "forkable")
	equal(t, canPreferForkFromSnapshot(session, ""), false, "no leaf")
	equal(t, canPreferForkFromSnapshot("", "leaf"), false, "no file")
	equal(t, canPreferForkFromSnapshot(session+"x", "leaf"), false, "missing file")

	for in, want := range map[any]any{false: false, "false": false, true: nil, "true": nil, "": nil, "x.md": "x.md", float64(1): nil, nil: nil} {
		equal(t, normalizeOutputOverride(in), want, "normalizeOutputOverride")
	}
	agent := AgentConfig{Output: "agent.md", OutputMode: "file-only", Skills: []string{"a"}, Model: "m", OutputSchema: map[string]any{"type": "object"}}
	b := resolveStepBehavior(agent, stepOverrides{})
	equal(t, [4]any{b.Output, b.OutputMode, b.Skills[0], b.Model}, [4]any{"agent.md", "file-only", "a", "m"}, "agent defaults")
	equal(t, b.OutputSchema, agent.OutputSchema, "agent schema")
	b = resolveStepBehavior(agent, stepOverrides{Output: false, OutputMode: "inline", Skills: SkillInput{Set: true, False: true}, Model: "x", OutputSchema: false})
	equal(t, [5]any{b.Output, b.OutputMode, b.SkillsFalse, b.Model, b.OutputSchema}, [5]any{false, "inline", true, "x", nil}, "overrides")
	b = resolveStepBehavior(agent, stepOverrides{Output: "o.md", Skills: SkillInput{Set: true, Items: []string{"s"}}, OutputSchema: map[string]any{"a": 1}})
	equal(t, [3]any{b.Output, b.Skills[0], b.OutputSchema.(map[string]any)["a"]}, [3]any{"o.md", "s", 1}, "explicit")
	b = resolveStepBehavior(AgentConfig{}, stepOverrides{})
	equal(t, [3]any{b.Output, b.OutputMode, b.OutputSchema}, [3]any{false, "inline", nil}, "empty agent")
}

func TestRefinementOverlay(t *testing.T) {
	e := newPfEnv(t)
	cwd := e.dir("repo")
	meta := `<!-- pi-subagents-refinement:v1
{"agent":"worker","revision":1,"updatedAt":"2026-01-01T00:00:00.000Z","base":{"source":"project","filePath":"/x/worker.md","systemPromptSha256":"abc"},"evidence":{}}
-->

# Current refinement for ` + "`worker`" + `

` + "```pi-subagents-refinement-current\nBe brief.\n```" + `

# Snapshots

` + "```pi-subagents-refinement-snapshots-json\n[]\n```\n"
	path, err := getAgentRefinementPath(cwd, "worker")
	equal(t, err, nil, "path")
	equal(t, path, filepath.Join(cwd, ".pi", "subagents", "refinements", "worker.md"), "refinement path")
	equal(t, appendAgentRefinementOverlay("Base", cwd, "worker"), "Base", "no file")
	writeFile(t, path, meta)
	got := appendAgentRefinementOverlay("Base", cwd, "worker")
	equal(t, got, "Base\n\n<pi-subagents-refinement agent=\"worker\" source=\".pi/subagents/refinements/worker.md\">\nProject-local refinement guidance generated from recent bounded evidence.\n"+
		"It does not override tool, developer, task, output, acceptance, or safety instructions.\n\nBe brief.\n</pi-subagents-refinement>", "overlay")
	equal(t, strings.HasPrefix(appendAgentRefinementOverlay("", cwd, "worker"), "<pi-subagents-refinement"), true, "overlay alone")
	for name, broken := range map[string]string{
		"no metadata":   "# nothing\n",
		"bad json":      strings.Replace(meta, `"revision":1`, `"revision":`, 1),
		"bad source":    strings.Replace(meta, `"source":"project"`, `"source":"nowhere"`, 1),
		"no current":    strings.Replace(meta, "pi-subagents-refinement-current", "other", 1),
		"bad snapshots": strings.Replace(meta, "[]\n```", "{}\n```", 1),
		"empty current": strings.Replace(meta, "Be brief.", "  ", 1),
		"bad snapshot":  strings.Replace(meta, "[]\n```", `[{"revision":1}]`+"\n```", 1),
		"no sha":        strings.Replace(meta, `,"systemPromptSha256":"abc"`, "", 1),
		"no evidence":   strings.Replace(meta, `,"evidence":{}`, "", 1),
		"fraction":      strings.Replace(meta, `"revision":1`, `"revision":1.5`, 1),
	} {
		writeFile(t, path, broken)
		equal(t, appendAgentRefinementOverlay("Base", cwd, "worker"), "Base", name)
	}
	if _, err := getAgentRefinementPath(cwd, "../evil"); err == nil {
		t.Error("a path-like agent name names a refinement file")
	}
	if _, err := getAgentRefinementPath(cwd, "a..b"); err == nil {
		t.Error("a name with .. names a refinement file")
	}
	equal(t, appendAgentRefinementOverlay("Base", cwd, "bad name!"), "Base", "unusable name")
}

func TestEffectiveSystemPromptOrder(t *testing.T) {
	e := newPfEnv(t)
	cwd := e.dir("repo")
	agent := AgentConfig{Name: "w", SystemPrompt: "  Base.  "}
	skills := []ResolvedSkill{{Name: "s", Path: "/p/SKILL.md", Content: "skill body"}}
	got := buildEffectiveSystemPrompt(agent, skills, cwd, "/out/report.md")
	baseIndex, skillIndex, outputIndex := strings.Index(got, "Base."), strings.Index(got, "skill"), strings.Index(got, "Runtime output path override:")
	equal(t, baseIndex == 0 && skillIndex > baseIndex && outputIndex > skillIndex, true, "base, skills, output path: "+got)
	equal(t, buildEffectiveSystemPrompt(agent, nil, cwd, ""), "Base.", "trimmed base prompt")
	equal(t, strings.HasPrefix(buildEffectiveSystemPrompt(AgentConfig{Name: "w"}, skills, cwd, ""), "<"), true, "skills alone")
}

func TestMcpDirectToolGrant(t *testing.T) {
	servers := []mcpGrantServerEntry{
		{Name: "my-server", Server: mcpGrantServer{}},
		{Name: "docs", Server: mcpGrantServer{ToolPrefix: "none", IncludeTools: []any{"search*", "get_*"}, ExcludeTools: []any{"search-private"}}},
		{Name: "bigmcp", Server: mcpGrantServer{ToolPrefix: "short", ExposeResources: new(false)}},
	}
	tool := func(n string) mcpGrantTool { return mcpGrantTool{Name: &n} }
	res := func(name, uri string) mcpGrantResource { return mcpGrantResource{Name: &name, URI: &uri} }
	meta := map[string]mcpGrantMetadata{
		"my-server": {Tools: []mcpGrantTool{tool("do.it"), tool("my-server_other"), tool("read"), {}}, Resources: []mcpGrantResource{res("My Doc 1", "x://d"), res("", "x://e")}},
		"docs":      {Tools: []mcpGrantTool{tool("search"), tool("search-private"), tool("other")}, Resources: []mcpGrantResource{res("Get Me", "x://g")}},
		"bigmcp":    {Tools: []mcpGrantTool{tool("t")}, Resources: []mcpGrantResource{res("r", "x://r")}},
	}
	grant := func(selectors ...string) mcpDirectToolGrant {
		return planMcpDirectToolGrant(mcpDirectToolGrantInput{Selectors: selectors, Servers: servers, Metadata: meta})
	}
	names := func(g mcpDirectToolGrant) []string {
		out := []string{}
		for _, s := range g.Selections {
			out = append(out, s.Name+"="+s.Selector)
		}
		return out
	}
	equal(t, names(grant("my-server")), []string{"my_2d_server_do_it=my-server/do.it", "my_2d_server_my-server_other=my-server/my-server_other", "my_2d_server_read=my-server/read", "my_2d_server_get_my_doc_1=my-server/get_my_doc_1"}, "whole server, server prefix")
	equal(t, names(grant("my-server/read")), []string{"my-server_read=my-server/read"}, "one tool")
	equal(t, names(grant("docs")), []string{"search=docs/search", "get_get_me=docs/get_get_me"}, "include and exclude globs, no prefix")
	equal(t, names(grant("bigmcp")), []string{"big_t=bigmcp/t"}, "short prefix, resources hidden")
	equal(t, grant("nothing", "my-server/missing", "my-server/read/extra").UnresolvedSelectors, []string{"nothing", "my-server/missing"}, "unresolved")
	equal(t, len(grant("").Selections), 0, "empty selector")
	equal(t, grant("my-server//").UnresolvedSelectors == nil, true, "trailing slashes are dropped")
	equal(t, normalizeMcpDirectToolSelectors([]string{"a/", "a", "", "b//", "b"}), []string{"a", "b"}, "normalized")
	sel := parseMcpDirectToolSelectors([]string{"a", "b/c", "b/d", "e/", "/f", "g/h/i"})
	equal(t, [3]any{sel.Servers["a"] && sel.Servers["e"], len(sel.Tools["b"]), sel.Tools["g"]["h"]}, [3]any{true, 2, true}, "parsed selectors")
	equal(t, sel.Tools["f"] == nil, true, "empty server with a tool names nothing")
	// the adapter's prefix rules
	equal(t, getMcpServerPrefix("a b", "server"), "a_20_b", "sanitized")
	equal(t, getMcpServerPrefix("x-MCP", "short"), "x", "short drops mcp")
	equal(t, getMcpServerPrefix("mcp", "short"), "mcp", "short keeps a bare mcp")
	equal(t, getMcpServerPrefix("s", "mcp"), "mcp__s", "mcp prefix")
	equal(t, getMcpServerPrefix("s", "none"), "", "no prefix")
	equal(t, normalizeMcpToolPrefix("bogus"), "server", "default prefix")
	equal(t, formatMcpToolName("x.y", "s", "server"), "s_x_y", "dots")
	equal(t, mcpResourceNameToToolName("  9 Lives!! "), "resource_9_lives", "resource names")
	equal(t, mcpResourceNameToToolName("!!"), "resource", "empty resource name")
	// a tool named like a builtin is not granted
	g := planMcpDirectToolGrant(mcpDirectToolGrantInput{Selectors: []string{"x"}, Servers: []mcpGrantServerEntry{{Name: "x", Server: mcpGrantServer{ToolPrefix: "none"}}},
		Metadata: map[string]mcpGrantMetadata{"x": {Tools: []mcpGrantTool{tool("read"), tool("fine")}}}})
	equal(t, names(g), []string{"fine=x/fine"}, "builtin names")
	equal(t, formatUnresolvedMcpDirectToolSelectors([]string{"a", "b"}), "Unresolved MCP direct-tool selectors: a, b. Direct MCP tools require a matching configured server and fresh metadata cache; runtime-registered servers require a host/pi-mcp-adapter handoff before child launch.", "message")
}

func TestMcpConfigSources(t *testing.T) {
	e := newPfEnv(t)
	cwd := e.dir("repo")
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "x.md"), "---\nname: x\ndescription: d\n---\nb\n") // anchors the project root
	def := func(s string) *jsObject { return js(t, s).(*jsObject) }
	hash := func(d *jsObject) string {
		h, err := computeMcpServerHash(d)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	fresh := func(name string, d *jsObject, tools ...string) {
		var tl []string
		for _, n := range tools {
			tl = append(tl, `{"name":"`+n+`"}`)
		}
		e.writeJSON(filepath.Join(e.agentDir, "mcp-cache.json"), `{"version":1,"servers":{"`+name+`":{"configHash":"`+hash(d)+`","cachedAt":`+strconv.FormatInt(time.Now().UnixMilli(), 10)+`,"tools":[`+strings.Join(tl, ",")+`]}}}`)
	}
	selections := func(selectors ...string) ([]string, []string) {
		r, err := resolveMcpDirectToolResolution(selectors, cwd)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, s := range r.Selections {
			names = append(names, s.Name)
		}
		return names, r.UnresolvedSelectors
	}
	// the project file, with the metadata cache
	e.writeJSON(filepath.Join(cwd, ".mcp.json"), `{"mcp-servers":{"proj":{"command":"p"}},"settings":{"toolPrefix":"none"}}`)
	fresh("proj", def(`{"command":"p"}`), "go")
	names, unresolved := selections("proj")
	equal(t, [2]any{names, len(unresolved)}, [2]any{[]string{"go"}, 0}, "project server, settings prefix")
	// a stale entry, one for a changed definition and a malformed cache resolve nothing
	e.writeJSON(filepath.Join(e.agentDir, "mcp-cache.json"), `{"version":1,"servers":{"proj":{"configHash":"`+hash(def(`{"command":"p"}`))+`","cachedAt":`+strconv.FormatInt(time.Now().Add(-8*24*time.Hour).UnixMilli(), 10)+`,"tools":[{"name":"go"}]}}}`)
	_, unresolved = selections("proj")
	equal(t, unresolved, []string{"proj"}, "stale cache")
	fresh("proj", def(`{"command":"other"}`), "go")
	_, unresolved = selections("proj")
	equal(t, unresolved, []string{"proj"}, "changed definition")
	for _, bad := range []string{`not json`, `{"version":2,"servers":{}}`, `{"version":1}`, `[]`} {
		writeFile(t, filepath.Join(e.agentDir, "mcp-cache.json"), bad)
		_, unresolved = selections("proj")
		equal(t, unresolved, []string{"proj"}, "cache "+bad)
	}
	os.Remove(filepath.Join(e.agentDir, "mcp-cache.json"))
	_, unresolved = selections("proj")
	equal(t, unresolved, []string{"proj"}, "no cache")
	// the adapter file of the agent directory and the later file win; an import fills what is not configured
	e.writeJSON(filepath.Join(e.agentDir, "mcp-adapter.json"), `{"mcpServers":{"proj":{"command":"global"},"imp":{"command":"own"}},"imports":["cursor"]}`)
	e.writeJSON(filepath.Join(e.home, ".cursor", "mcp.json"), `{"mcpServers":{"imp":{"command":"cursor"},"fromcursor":{"command":"c"}}}`)
	fresh("fromcursor", def(`{"command":"c"}`), "t")
	names, _ = selections("fromcursor")
	equal(t, names, []string{"fromcursor_t"}, "imported server (the adapter's own settings do not set a prefix here)")
	cfg, err := loadMcpConfig(cwd)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, cfg.Servers.vals["proj"].(*jsObject).vals["command"], "p", "the project file wins over the agent directory")
	equal(t, cfg.Servers.vals["imp"].(*jsObject).vals["command"], "own", "an own definition wins over an import")
	// package servers and agent plugin servers
	pkg := e.dir("pkg")
	writeFile(t, filepath.Join(pkg, "package.json"), `{"name":"@scope/Pkg","pi":{"mcp":"mcp.json"}}`)
	writeFile(t, filepath.Join(pkg, "mcp.json"), `{"mcpServers":{"srv.x":{"command":"k"},"bad":{"command":1}}}`)
	e.writeJSON(filepath.Join(cwd, ".pi", "settings.json"), `{"packages":[`+strconv.Quote(pkg)+`]}`)
	cfg, err = loadMcpConfig(cwd)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, cfg.Servers.has("scope_Pkg__srv_x"), true, "package server name "+strings.Join(cfg.Servers.keys, ","))
	equal(t, cfg.Servers.has("scope_Pkg__bad"), false, "invalid package server")
	writeFile(t, filepath.Join(pkg, "package.json"), `{ not json`)
	if _, err = loadMcpConfig(cwd); err == nil || !strings.HasPrefix(err.Error(), "Invalid JSON in '"+filepath.Join(pkg, "package.json")+"': ") {
		t.Errorf("malformed package manifest: %v", err)
	}
	os.Remove(filepath.Join(cwd, ".pi", "settings.json"))
	plugin := e.dir("plugin")
	writeFile(t, filepath.Join(plugin, "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"my.plug"}`)
	writeFile(t, filepath.Join(plugin, "mcp.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{
		"a":{"type":"stdio","command":"./bin/run","args":["${PLUGIN_ROOT}/x"],"env":{"K":"${PLUGIN_DATA}"}},
		"b":{"type":"streamable-http","url":"https://example.com/mcp"},
		"c":{"type":"stdio","command":"/abs/path"},
		"d":{"type":"sse","url":"http://example.com/x"},
		"e":{"type":"stdio","command":"run","env":{"PLUGIN_ROOT":"x"}},
		"f":{"type":"stdio","command":"run","extra":1},
		"g":{"type":"sse","url":"http://127.0.0.1:1/x","headers":{"A":"1","a":"2"}}}}`)
	e.writeJSON(filepath.Join(cwd, ".mcp.json"), `{"settings":{"agentPluginPaths":[`+strconv.Quote(plugin)+`]}}`)
	cfg, err = loadMcpConfig(cwd)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, cfg.Servers.keys, []string{"my_plug__a", "my_plug__b"}, "plugin servers (a stdio and a remote one are valid)")
	a := cfg.Servers.vals["my_plug__a"].(*jsObject)
	equal(t, a.vals["command"], filepath.Join(plugin, "bin", "run"), "contained command")
	equal(t, a.vals["args"], []any{plugin + "/x"}, "expanded args")
	equal(t, a.vals["env"].(*jsObject).vals["K"], filepath.Join(e.agentDir, "agent-plugin-data", "my.plug"), "plugin data dir")
	equal(t, a.vals["literalEnv"], true, "literal env")
}

func TestMcpServerHash(t *testing.T) {
	t.Setenv("MCP_HASH_X", "vx")
	def := func(s string) *jsObject { return js(t, s).(*jsObject) }
	h := func(s string) string {
		v, err := computeMcpServerHash(def(s))
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		return v
	}
	equal(t, h(`{"command":"a"}`), h(`{"command":"a","unrelated":1}`), "only the identity fields count")
	equal(t, h(`{"command":"a","env":{"K":"${MCP_HASH_X}"}}`), h(`{"command":"a","env":{"K":"vx"}}`), "env interpolation")
	equal(t, h(`{"command":"a","env":{"K":"$env:MCP_HASH_X"}}`), h(`{"command":"a","env":{"K":"vx"}}`), "powershell style")
	equal(t, h(`{"command":"a","env":{"K":"{env:MCP_HASH_X}"}}`), h(`{"command":"a","env":{"K":"vx"}}`), "brace style")
	equal(t, h(`{"command":"a","env":{"K":"!${MCP_HASH_X}"}}`) != h(`{"command":"a","env":{"K":"vx"}}`), true, "a command value is not interpolated")
	equal(t, h(`{"command":"a","env":{"K":"!!${MCP_HASH_X}"}}`), h(`{"command":"a","env":{"K":"!vx"}}`), "an escaped bang")
	equal(t, h(`{"command":"a","cwd":"~/x"}`), h(`{"command":"a","cwd":"`+filepath.Join(homeDir(), "x")+`"}`), "home in paths")
	equal(t, h(`{"command":"a","bearerTokenEnv":"MCP_HASH_X"}`) != h(`{"command":"a","bearerTokenEnv":"MCP_HASH_X","bearerToken":"t"}`), true, "bearer token")
	equal(t, h(`{"command":"a"}`) != h(`{"command":"b"}`), true, "command")
	equal(t, h(`{"command":"a"}`) != h(`{"command":"a","args":["x"]}`), true, "args")
	equal(t, h(`{"url":"https://h/${MCP_HASH_X}"}`), h(`{"url":"https://h/vx"}`), "url interpolation")
	equal(t, h(`{"command":"a","includeTools":["x"]}`) != h(`{"command":"a","excludeTools":["x"]}`), true, "include and exclude differ")
	equal(t, h(`{"command":"a","requestHeadersCommand":{"command":"c","timeoutMs":5}}`) != h(`{"command":"a","requestHeadersCommand":{"command":"c","timeoutMs":6}}`), true, "request headers command")
	// the hash is the sha256 of the stable text of the identity
	equal(t, h(`{"command":"a"}`), "948f33bf2f93b322ff7f904875bb9ea93ca54c5ab4142f6d851bdcff210a19fa", "pinned hash")
	_, err := computeMcpServerHash(def(`{"url":"https://h/${MCP_HASH_MISSING_1}/${MCP_HASH_MISSING_2}"}`))
	if err == nil || err.Error() != "Missing environment variables in MCP server URL: MCP_HASH_MISSING_1, MCP_HASH_MISSING_2" {
		t.Errorf("missing variables: %v", err)
	}
	_, err = computeMcpServerHash(def(`{"url":"https://h/${MCP_HASH_MISSING_1}"}`))
	equal(t, err.Error(), "Missing environment variable in MCP server URL: MCP_HASH_MISSING_1", "one missing")
	_, err = computeMcpServerHash(def(`{"url":"not a url"}`))
	equal(t, err.Error(), "Invalid MCP server URL after environment interpolation: not a url", "invalid url")
	// isMcpServerDefinition
	for in, want := range map[string]bool{`{}`: true, `{"command":1}`: false, `{"args":[1]}`: false, `{"env":{"a":1}}`: false, `{"exposeResources":"x"}`: false,
		`{"auth":"oauth"}`: true, `{"auth":"x"}`: false, `{"auth":false}`: true, `{"directTools":["a"]}`: true, `{"directTools":1}`: false,
		`{"requestHeadersCommand":{"command":"c","args":["a"],"timeoutMs":1}}`: true, `{"requestHeadersCommand":{"args":[]}}`: false, `{"requestHeadersCommand":{"command":"c","env":[]}}`: false} {
		equal(t, isMcpServerDefinition(js(t, in)), want, in)
	}
	equal(t, isMcpServerDefinition("x"), false, "not an object")
}
