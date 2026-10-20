package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Twins of test/unit/preflight.test.ts. Where the original asserts that a runtime extension path ends with a TypeScript file
// name (`fast-mode-extension.ts`, `subagent-prompt-runtime.ts`, `fanout-child.ts`), the twin asserts the child role that
// replaces that file in this port (`native:pi-subagents-fast-mode`, `-prompt-runtime`, `-fanout-child`; see policy_toolplan.go).

type pfEnv struct {
	t        *testing.T
	temp     string
	home     string
	agentDir string
}

func newPfEnv(t *testing.T) *pfEnv {
	t.Helper()
	temp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(temp, "home")
	for _, k := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(k, home)
	}
	agent := filepath.Join(home, ".pi", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", "")
	clearSkillCache()
	t.Cleanup(clearSkillCache)
	return &pfEnv{t: t, temp: temp, home: home, agentDir: agent}
}

func (e *pfEnv) dir(parts ...string) string {
	e.t.Helper()
	p := filepath.Join(append([]string{e.temp}, parts...)...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *pfEnv) writeSkill(cwd, name string) {
	writeFile(e.t, filepath.Join(cwd, ".pi", "skills", name, "SKILL.md"), "---\ndescription: "+name+"\n---\n\nUse "+name+".\n")
}

func (e *pfEnv) writeJSON(path string, value string) {
	var v any
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		e.t.Fatal(err)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	writeFile(e.t, path, string(b))
}

func (e *pfEnv) writeMcpFixture() {
	def := js(e.t, `{"command":"github-mcp"}`).(*jsObject)
	hash, err := computeMcpServerHash(def)
	if err != nil {
		e.t.Fatal(err)
	}
	e.writeJSON(filepath.Join(e.agentDir, "mcp-adapter.json"), `{"mcpServers":{"github":{"command":"github-mcp"}}}`)
	e.writeJSON(filepath.Join(e.agentDir, "mcp-cache.json"), `{"version":1,"servers":{"github":{"configHash":"`+hash+`","cachedAt":`+strconv.FormatInt(time.Now().UnixMilli(), 10)+
		`,"tools":[{"name":"search_repositories"},{"name":"create_issue"}],"resources":[]}}}`)
}

func pfModels(pairs ...string) []ModelInfo {
	out := []ModelInfo{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, ModelInfo{Provider: pairs[i], ID: pairs[i+1], FullID: pairs[i] + "/" + pairs[i+1]})
	}
	return out
}

// pfResolve runs the preflight and fails the test on a thrown error.
func pfResolve(t *testing.T, in launchContractInput) launchContractResult {
	t.Helper()
	r, err := resolveSubagentLaunchContract(in)
	if err != nil {
		t.Fatalf("preflight threw: %v", err)
	}
	return r
}

func pfOK(t *testing.T, in launchContractInput) *launchContract {
	t.Helper()
	r := pfResolve(t, in)
	if !r.OK {
		t.Fatalf("preflight refused (%s): %s", r.Code, r.Message)
	}
	return r.Contract
}

func hasSuffixIn(list []string, suffix string) bool {
	for _, e := range list {
		if strings.HasSuffix(e, suffix) {
			return true
		}
	}
	return false
}

const hex64 = `^[a-f0-9]{64}$`

func TestPreflight(t *testing.T) {
	tw(t, "preflight", "exposes required host IDs without exposing their private paths", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "required.md"), "---\nname: required\ndescription: Required extension fixture\n---\nFixture.\n")
		extensionPath := filepath.Join(e.temp, "required-provider.mjs")
		writeFile(t, extensionPath, "export default () => {};\n")
		dispose, err := registerRequiredChildExtensions("preflight-required", []requiredChildExtension{{ID: "safe-provider", Path: extensionPath}})
		if err != nil {
			t.Fatal(err)
		}
		defer dispose()
		c := pfOK(t, launchContractInput{Agent: "required", Cwd: cwd, ParentSessionID: "preflight-required"})
		equal(t, c.Tools.RequiredExtensionIDs, []string{"safe-provider"}, "required ids")
		real, _ := filepath.EvalSymlinks(extensionPath)
		equal(t, contains(c.Tools.ExtensionArgs, real), false, "private path exposed")
	})

	tw(t, "preflight", "resolves an ordinary single-agent contract without creating launch directories", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		e.writeSkill(cwd, "project-skill")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), `---
name: worker
description: Project worker
tools:
  - read
  - write
  - /tmp/private-tool.ts
model: test/primary
thinking: high
skills:
  - project-skill
output: report.md
---
Project prompt.
`)
		sessionRoot := filepath.Join(e.temp, "sessions")
		handle, err := registerSubagentCapabilityCeiling("preflight-session", "test", js(t, `{"allowedTools":["read"],"denyExtensions":true}`))
		if err != nil {
			t.Fatal(err)
		}
		defer handle.dispose()
		ceiling := resolveSubagentCapabilityCeiling("preflight-session", nil)
		in := launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect the repo", RunID: "run-123", SessionRoot: sessionRoot,
			AvailableModels: pfModels("test", "primary", "test", "fallback"), CapabilityCeiling: ceiling}
		c := pfOK(t, in)
		equal(t, c.Version, subagentLaunchContractVersion, "version")
		equal(t, c.Agent.Source, SourceProject, "source")
		equal(t, c.Agent.DefinitionProjectionVersion, 2, "definition projection version")
		polMatches(t, c.Agent.DefinitionDigest, hex64)
		polMatches(t, c.LaunchContractDigest, hex64)
		shadowed := false
		for _, s := range c.Agent.ShadowedCandidates {
			shadowed = shadowed || s.Name == "worker" && s.Source == SourceBuiltin
		}
		equal(t, shadowed, true, "builtin worker shadowed")
		equal(t, c.Model, "test/primary:high", "model")
		equal(t, c.Thinking, "high", "thinking")
		equal(t, c.Skills.Requested, []string{"project-skill"}, "requested skills")
		equal(t, c.Skills.Resolved[0].Name, "project-skill", "resolved skill")
		equal(t, c.Tools.EffectiveAllowlist, []string{"read"}, "effective allowlist")
		// The bridge adds contact_supervisor before the ceiling applies, exactly as execution does.
		equal(t, c.Tools.CapabilityAudit.RemovedTools, []string{"write", "contact_supervisor"}, "removed tools")
		equal(t, c.Tools.CapabilityAudit.RemovedExtensionCount, 1, "removed extension count")
		equal(t, [2]any{c.BridgeMode, c.BridgeActive}, [2]any{"always", true}, "bridge")
		equal(t, c.Tools.DisableAmbientExtensions, true, "ambient")
		equal(t, c.Roots.SessionFile, filepath.Join(sessionRoot, "run-123", "run-0", "session.jsonl"), "session file")
		equal(t, c.Roots.OutputPath, filepath.Join(tempArtifactsDir(), "outputs", "run-123", "report.md"), "output path")
		equal(t, strings.HasSuffix(c.Roots.Lifecycle.StatusPath, filepath.Join("run-123", "status.json")), true, "status path")
		equal(t, strings.HasSuffix(c.Roots.Lifecycle.EventsPath, filepath.Join("run-123", "events.jsonl")), true, "events path")
		equal(t, strings.HasSuffix(c.Roots.Lifecycle.ProcessTerminalPath, filepath.Join("run-123", "process-terminal.json")), true, "process terminal path")
		equal(t, c.Roots.Lifecycle.AsyncDir != c.Roots.ArtifactsDir, true, "async dir apart from artifacts")
		polMatches(t, c.Digest, hex64)
		repeated := pfOK(t, in)
		equal(t, repeated.Digest, c.Digest, "repeated digest")
		if _, err := os.Stat(sessionRoot); !os.IsNotExist(err) {
			t.Errorf("session root was created: %v", err)
		}
		if _, err := os.Stat(filepath.Join(cwd, ".pi/subagents")); !os.IsNotExist(err) {
			t.Errorf("subagents directory was created: %v", err)
		}
	})

	tw(t, "preflight", "projects concurrent children under distinct run-id roots for an explicit sessionDir", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-session-dir-keying")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nWorker.\n")
		sessionDir := filepath.Join(e.temp, "caller-sessions")
		var files []string
		for _, runID := range []string{"run-123", "run-456"} {
			c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect the repo", RunID: runID, SessionDir: sessionDir})
			equal(t, c.Roots.SessionFile, filepath.Join(sessionDir, runID, "run-0", "session.jsonl"), "session file")
			equal(t, c.Roots.SessionDir, filepath.Join(sessionDir, runID, "run-0"), "session dir")
			equal(t, c.Roots.SessionRoot, filepath.Join(sessionDir, runID), "session root")
			files = append(files, c.Roots.SessionFile)
		}
		equal(t, files[0] != files[1], true, "distinct session files")
		c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect the repo", SessionDir: sessionDir})
		equal(t, c.Roots.SessionFile, filepath.Join(sessionDir, "preflight", "run-0", "session.jsonl"), "placeholder session file")
		equal(t, c.Roots.SessionDir, filepath.Join(sessionDir, "preflight", "run-0"), "placeholder session dir")
	})

	tw(t, "preflight", "binds canonical extension metadata into public preflight provenance", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("bindings-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "binding-worker.md"), "---\nname: binding-worker\ndescription: Binding worker\n---\nWorker.\n")
		omitted := pfOK(t, launchContractInput{Agent: "binding-worker", Cwd: cwd, Task: "Inspect"})
		bound := pfOK(t, launchContractInput{Agent: "binding-worker", Cwd: cwd, Task: "Inspect",
			ExtensionBindings: js(t, `{"shepherd.dispatch/1":{"writeScope":["src/a.ts"],"role":"coder"}}`)})
		equal(t, bound.LaunchContractDigest != omitted.LaunchContractDigest, true, "bindings change the launch digest")
		invalid := pfResolve(t, launchContractInput{Agent: "binding-worker", Cwd: cwd, ExtensionBindings: js(t, `{"invalid":true}`)})
		equal(t, invalid.OK, false, "invalid bindings refused")
		equal(t, invalid.Code, "invalid_extension_bindings", "code")
	})

	tw(t, "preflight", "uses the parent provider for provider-scoped agent overrides", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("provider-overrides")
		e.writeJSON(filepath.Join(cwd, ".pi", "settings.json"), `{"subagents":{"agentOverridesByProvider":{"github-copilot":{"worker":{"model":"github-copilot/gpt-5-mini"}},"openrouter":{"worker":{"model":"openrouter/openai/gpt-5-mini"}}}}}`)
		models := pfModels("github-copilot", "gpt-5-mini", "openrouter", "openai/gpt-5-mini")
		copilot := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentModel: &ParentModel{Provider: "github-copilot", ID: "parent"}, AvailableModels: models})
		openrouter := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, PreferredProvider: "openrouter", ParentModel: &ParentModel{Provider: "github-copilot", ID: "parent"}, AvailableModels: models})
		equal(t, copilot.Model, "github-copilot/gpt-5-mini:high", "copilot model")
		equal(t, openrouter.Model, "openrouter/openai/gpt-5-mini:high", "openrouter model")
	})

	tw(t, "preflight", "warns when workspace package work has package-only authority", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("workspace-scope-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nWorker.\n")
		c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd,
			Task: "Create a new workspace package, but only edit files under packages/widget and do not change root workspace metadata."})
		found := false
		for _, d := range c.Diagnostics {
			found = found || d.Code == "workspace_scope_authority" && d.Severity == "warning"
		}
		equal(t, found, true, "workspace scope warning")
	})

	tw(t, "preflight", "binds fast mode runtime extension into public preflight provenance", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("fast-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "fast-worker.md"), `---
name: fast-worker
description: Fast worker
model: openai-codex/gpt-5.6-luna
fast: true
---
Worker.
`)
		models := pfModels("openai-codex", "gpt-5.6-luna")
		enabled := pfOK(t, launchContractInput{Agent: "fast-worker", Cwd: cwd, Task: "Run", AvailableModels: models})
		disabled := pfOK(t, launchContractInput{Agent: "fast-worker", Cwd: cwd, Task: "Run", AvailableModels: models, Fast: new(false)})
		equal(t, hasSuffixIn(enabled.Tools.RuntimeExtensions, "fast-mode"), true, "fast mode extension when enabled")
		equal(t, hasSuffixIn(disabled.Tools.RuntimeExtensions, "fast-mode"), false, "no fast mode extension when disabled")
		equal(t, enabled.LaunchContractDigest != disabled.LaunchContractDigest, true, "digests differ")
	})

	tw(t, "preflight", "enforces maxThinking before a child launch and accepts levels at the ceiling", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("thinking-repo")
		e.writeJSON(filepath.Join(cwd, ".pi", "settings.json"), `{"subagents":{"maxThinking":"xhigh"}}`)
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\nmodel: test/worker\nthinking: xhigh\n---\nWorker.\n")
		models := pfModels("test", "worker")
		accepted := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect", AvailableModels: models})
		equal(t, accepted.ThinkingCeiling, "xhigh", "ceiling")
		rejected := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect", Thinking: "max", AvailableModels: models})
		equal(t, rejected.OK, false, "refused")
		equal(t, rejected.Code, "thinking_ceiling", "code")
		polMatches(t, rejected.Message, `max.*xhigh.*worker`)
	})

	tw(t, "preflight", "binds the resolved agent outputMode into the launch digest", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-output-mode")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\noutput: report.md\noutputMode: file-only\n---\nProject prompt.\n")
		defaultMode := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd})
		explicitDefault := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, OutputMode: "file-only"})
		override := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, OutputMode: "inline"})
		equal(t, defaultMode.LaunchContractDigest, explicitDefault.LaunchContractDigest, "explicit default digest")
		equal(t, defaultMode.LaunchContractDigest != override.LaunchContractDigest, true, "override digest")
	})

	tw(t, "preflight", "rejects an unresolved configured model when the host registry is available", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-unresolved-model")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\nmodel: fast\n---\nProject prompt.\n")
		_, err := resolveSubagentLaunchContract(launchContractInput{Agent: "worker", Cwd: cwd, AvailableModels: pfModels("test", "primary")})
		if err == nil {
			t.Fatal("expected a thrown error")
		}
		polMatches(t, err.Error(), `Unknown subagent model 'fast'`)
	})

	tw(t, "preflight", "rejects an explicit per-call unknown model before launch", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-explicit-unknown-model")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\nmodel: test/primary\n---\nProject prompt.\n")
		_, err := resolveSubagentLaunchContract(launchContractInput{Agent: "worker", Cwd: cwd, Model: "test/unknown", AvailableModels: pfModels("test", "primary")})
		if err == nil {
			t.Fatal("expected a thrown error")
		}
		polMatches(t, err.Error(), `Unknown subagent model 'test/unknown'`)
	})

	tw(t, "preflight", "trusts an inherited parent model outside the host registry", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-parent-model")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nProject prompt.\n")
		c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentModel: &ParentModel{Provider: "gateway", ID: "parent-model"}, AvailableModels: pfModels("test", "primary")})
		equal(t, c.Model, "gateway/parent-model", "model")
	})

	tw(t, "preflight", "uses subagents.defaultProvider when resolving launch model ids", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-default-provider")
		e.writeJSON(filepath.Join(e.home, ".pi", "agent", "settings.json"), `{"subagents":{"defaultProvider":"gpu-b"}}`)
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\nmodel: gpt-5-mini\n---\nProject prompt.\n")
		c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentModel: &ParentModel{Provider: "openai", ID: "gpt-5-mini"},
			AvailableModels: pfModels("openai", "gpt-5-mini", "gpu-b", "gpt-5-mini")})
		equal(t, c.Model, "gpu-b/gpt-5-mini", "model")
	})

	tw(t, "preflight", "bypasses native model validation for external CLI runners", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-external-model")
		e.writeJSON(filepath.Join(e.home, ".pi", "agent", "settings.json"), `{"subagents":{"defaultModel":"mock/default-model"}}`)
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "external.md"), "---\nname: external\ndescription: External runner\nrunner:\n  type: external-cli\n  command: "+strconv.Quote(os.Args[0])+"\n---\nProject prompt.\n")
		c := pfOK(t, launchContractInput{Agent: "external", Cwd: cwd, AvailableModels: pfModels("other", "known")})
		equal(t, c.Model, "", "model")
	})

	tw(t, "preflight", "resolves agent aliases to the canonical launch contract agent", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-alias")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\naliases: developer, coder\n---\nProject prompt.\n")
		c := pfOK(t, launchContractInput{Agent: "developer", Cwd: cwd, Task: "Implement the change"})
		equal(t, c.Agent.Name, "worker", "canonical name")
	})

	tw(t, "preflight", "reports alias collisions as ambiguous agents", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-alias-collision")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\naliases: coder\n---\nProject prompt.\n")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Project reviewer\naliases: coder\n---\nReview prompt.\n")
		r := pfResolve(t, launchContractInput{Agent: "coder", Cwd: cwd})
		equal(t, r.OK, false, "refused")
		equal(t, r.Code, "ambiguous_agent", "code")
		polMatches(t, r.Message, `Ambiguous agent alias 'coder': reviewer, worker|Ambiguous agent alias 'coder': worker, reviewer`)
	})

	tw(t, "preflight", "changes definition and launch digests when selected agent content changes", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		agentPath := filepath.Join(cwd, ".pi", "agents", "worker.md")
		writeFile(t, agentPath, "---\nname: digest-worker\ndescription: Digest worker\ntools:\n  - read\n---\nFirst prompt.\n")
		before := pfOK(t, launchContractInput{Agent: "digest-worker", Cwd: cwd, RunID: "digest-test"})
		writeFile(t, agentPath, "---\nname: digest-worker\ndescription: Digest worker\ntools:\n  - read\n---\nChanged prompt.\n")
		after := pfOK(t, launchContractInput{Agent: "digest-worker", Cwd: cwd, RunID: "digest-test"})
		equal(t, after.Agent.DefinitionDigest != before.Agent.DefinitionDigest, true, "definition digest")
		equal(t, after.LaunchContractDigest != before.LaunchContractDigest, true, "launch digest")
		equal(t, after.Digest != before.Digest, true, "contract digest")
	})

	tw(t, "preflight", "binds resolved skill content into the launch digest", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		e.writeSkill(cwd, "digest-skill")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ntools:\n  - read\nskills:\n  - digest-skill\n---\nProject prompt.\n")
		before := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, RunID: "skill-digest-test"})
		writeFile(t, filepath.Join(cwd, ".pi", "skills", "digest-skill", "SKILL.md"), "---\ndescription: updated digest-skill\n---\n\nUse digest-skill.\n")
		clearSkillCache()
		after := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, RunID: "skill-digest-test"})
		equal(t, after.Agent.DefinitionDigest, before.Agent.DefinitionDigest, "definition digest")
		equal(t, after.LaunchContractDigest != before.LaunchContractDigest, true, "launch digest")
		equal(t, after.Digest != before.Digest, true, "contract digest")
	})

	tw(t, "preflight", "returns closed failures for missing agents and missing skills", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\nskills:\n  - missing-skill\n---\nProject prompt.\n")

		missingAgent := pfResolve(t, launchContractInput{Agent: "missing", Cwd: cwd})
		equal(t, missingAgent.OK, false, "missing agent refused")
		equal(t, missingAgent.Code, "missing_agent", "missing agent code")
		equal(t, missingAgent.Diagnostics, []launchContractDiagnostic{}, "missing agent diagnostics")
		polMatches(t, missingAgent.Message, `^Unknown agent: missing\nEffective cwd: `)
		polMatches(t, missingAgent.Message, `Consulted agent-definition directories:`)
		polMatches(t, missingAgent.Message, `project: .*\.pi[\\/]agents \(1 candidate\)`)
		polMatches(t, missingAgent.Message, `Discovered agents:\n[\s\S]*worker \(project\)`)

		invalid := func(agent string, scope AgentScope, want string) launchContractResult {
			t.Helper()
			r := pfResolve(t, launchContractInput{Agent: agent, Cwd: cwd, AgentScope: scope})
			equal(t, r.OK, false, agent+" refused")
			if want != "" {
				equal(t, r.Code, "missing_agent", agent+" code")
				polMatches(t, r.Message, want)
			}
			return r
		}
		bad := func(name, extra string) string {
			return "---\nname: " + name + "\n" + extra + "description: Broken\nrunner:\n  type: unknown\n---\nBroken prompt.\n"
		}
		_ = bad
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "broken.md"), "---\nname: broken\ndescription: Broken worker\nrunner:\n  type: unknown\n---\nBroken prompt.\n")
		r := invalid("broken", "", `Agent 'broken' has invalid configuration: Agent 'broken' has invalid runner\.type`)
		equal(t, r.Diagnostics, []launchContractDiagnostic{{Code: "missing_agent", Severity: "error", Message: r.Message}}, "broken diagnostics")

		writeFile(t, filepath.Join(cwd, ".pi", "agents", "code-analysis.zeta-worker.md"), "---\nname: zeta-worker\npackage: code-analysis\ndescription: Broken packaged worker\nrunner:\n  type: unknown\n---\nBroken prompt.\n")
		r = invalid("code-analysis.zeta-worker", "project", `Agent 'code-analysis\.zeta-worker' has invalid configuration: Agent 'zeta-worker' has invalid runner\.type`)
		equal(t, r.Diagnostics, []launchContractDiagnostic{{Code: "missing_agent", Severity: "error", Message: r.Message}}, "broken packaged diagnostics")

		writeFile(t, filepath.Join(cwd, ".pi", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Broken reviewer\nrunner:\n  type: unknown\n---\nBroken prompt.\n")
		invalid("reviewer", "both", `Agent 'reviewer' has invalid configuration: Agent 'reviewer' has invalid runner\.type`)
		invalid(" reviewer ", "both", `Agent ' reviewer ' has invalid configuration: Agent 'reviewer' has invalid runner\.type`)

		writeFile(t, filepath.Join(e.agentDir, "agents", "foo.md"), "---\nname: foo\ndescription: User foo\n---\nUser prompt.\n")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "acme.foo.md"), "---\nname: foo\npackage: acme\ndescription: Broken packaged foo\nrunner:\n  type: unknown\n---\nBroken prompt.\n")
		localFoo := pfResolve(t, launchContractInput{Agent: "foo", Cwd: cwd, AgentScope: "both"})
		equal(t, localFoo.OK, true, "local foo resolves")
		r = pfResolve(t, launchContractInput{Agent: "acme.foo", Cwd: cwd, AgentScope: "both"})
		equal(t, r.OK, false, "packaged foo refused")
		polMatches(t, r.Message, `Agent 'acme\.foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)

		writeFile(t, filepath.Join(e.agentDir, "agents", "package-shadow.md"), "---\nname: package-shadow\ndescription: User package shadow\n---\nUser prompt.\n")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "package-shadow.md"), "---\nname: package-shadow\npackage: !!!\ndescription: Broken package shadow\n---\nBroken prompt.\n")
		r = pfResolve(t, launchContractInput{Agent: "package-shadow", Cwd: cwd, AgentScope: "both"})
		equal(t, r.OK, false, "package shadow refused")
		polMatches(t, r.Message, `Agent 'package-shadow' has invalid configuration: Agent 'package-shadow' package is invalid after sanitization`)

		writeFile(t, filepath.Join(cwd, ".agents", "shared.md"), "---\nname: shared\ndescription: Legacy shared\n---\nLegacy prompt.\n")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "shared.md"), "---\nname: shared\ndescription: Broken canonical shared\nrunner:\n  type: unknown\n---\nBroken prompt.\n")
		r = pfResolve(t, launchContractInput{Agent: "shared", Cwd: cwd, AgentScope: "project"})
		equal(t, r.OK, false, "canonical shared refused")
		polMatches(t, r.Message, `Agent 'shared' has invalid configuration: Agent 'shared' has invalid runner\.type`)

		packageRoot := filepath.Join(cwd, "package")
		writeFile(t, filepath.Join(packageRoot, "agents", "ambiguous.md"), "---\nname: ambiguous\npackage: acme\ndescription: Package ambiguous\n---\nPackage prompt.\n")
		writeFile(t, filepath.Join(packageRoot, "package.json"), `{"pi-subagents":{"agents":["agents"]}}`)
		writeFile(t, filepath.Join(cwd, ".pi", "settings.json"), `{"packages":[`+strconv.Quote(packageRoot)+`]}`)
		writeFile(t, filepath.Join(e.agentDir, "agents", "ambiguous.md"), "---\nname: ambiguous\ndescription: User ambiguous\n---\nUser prompt.\n")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "ambiguous.md"), "---\nname: ambiguous\ndescription: Broken project ambiguous\nrunner:\n  type: unknown\n---\nBroken prompt.\n")
		invalid("ambiguous", "both", `Agent 'ambiguous' has invalid configuration: Agent 'ambiguous' has invalid runner\.type`)

		missingSkill := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd})
		equal(t, missingSkill.OK, false, "missing skill refused")
		equal(t, missingSkill.Code, "missing_skill", "missing skill code")
		polMatches(t, missingSkill.Message, `missing-skill`)
	})

	tw(t, "preflight", "fails closed for invalid runtime inputs", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nProject prompt.\n")
		invalidCwd := pfResolve(t, launchContractInput{Agent: "worker", Cwd: filepath.Join(e.temp, "missing")})
		equal(t, invalidCwd.OK, false, "invalid cwd refused")
		equal(t, invalidCwd.Code, "invalid_cwd", "invalid cwd code")
		unsupportedMode := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Context: new("bogus")})
		equal(t, unsupportedMode.OK, false, "unsupported mode refused")
		equal(t, unsupportedMode.Code, "unsupported_mode", "unsupported mode code")
		invalidArtifactDir := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, ArtifactDir: "bogus"})
		equal(t, invalidArtifactDir.OK, false, "invalid artifact dir refused")
		equal(t, invalidArtifactDir.Code, "invalid_artifact_dir", "invalid artifact dir code")
	})

	tw(t, "preflight", "projects MCP, extension, fanout, structured-output, and fork diagnostics", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		e.writeMcpFixture()
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "fanout.md"), `---
name: fanout
description: Project fanout
tools:
  - read
  - subagent
  - /tmp/tool-ext.ts
  - mcp:github/search_repositories
extensions:
  - /tmp/config-ext.ts
subagentOnlyExtensions:
  - /tmp/subagent-only.ts
defaultContext: fork
---
Project prompt.
`)
		parentSessionFile := filepath.Join(e.temp, "parent.jsonl")
		writeFile(t, parentSessionFile, `{"type":"session","version":1,"id":"parent","timestamp":"2026-04-16T00:00:00.000Z","cwd":"/tmp"}`+"\n")
		c := pfOK(t, launchContractInput{Agent: "fanout", Cwd: cwd, OutputSchema: js(t, `{"type":"object","additionalProperties":false}`), ParentSessionFile: parentSessionFile, ParentLeafID: "leaf-current"})
		equal(t, c.Context, "fork", "context")
		hostRequired := false
		for _, d := range c.Diagnostics {
			hostRequired = hostRequired || d.Code == "host_required"
		}
		equal(t, hostRequired, true, "host_required diagnostic")
		equal(t, c.Tools.DeclaredBuiltin, []string{"read", "subagent", "contact_supervisor"}, "declared builtin")
		equal(t, c.Tools.ExplicitAllowlist, true, "explicit allowlist")
		equal(t, c.Tools.FanoutAuthorized, true, "fanout")
		equal(t, c.Tools.InternalTools, []string{"structured_output"}, "internal tools")
		equal(t, c.Tools.EffectiveMcpTools, []string{"github_search_repositories"}, "effective mcp tools")
		equal(t, c.Tools.RequiredChildTools, []string{"read", "subagent", "github_search_repositories", "structured_output"}, "required child tools")
		equal(t, c.Tools.ToolExtensionPaths, []string{"/tmp/tool-ext.ts"}, "tool extension paths")
		equal(t, c.Tools.DisableAmbientExtensions, true, "ambient")
		equal(t, hasSuffixIn(c.Tools.RuntimeExtensions, "prompt-runtime"), true, "prompt runtime")
		equal(t, hasSuffixIn(c.Tools.RuntimeExtensions, "fanout-child"), true, "fanout child")
		equal(t, contains(c.Tools.ExtensionArgs, "/tmp/config-ext.ts"), true, "config extension")
		equal(t, contains(c.Tools.ExtensionArgs, "/tmp/subagent-only.ts"), true, "subagent-only extension")
	})

	tw(t, "preflight", "inherits agent structured output and honors false for native and external runners", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("schema-default-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "typed.md"), "---\nname: typed\ndescription: Typed\noutputSchema: {\"type\":\"object\",\"required\":[\"ok\"]}\n---\nPrompt.\n")
		inherited := pfOK(t, launchContractInput{Agent: "typed", Cwd: cwd, Task: "Inspect"})
		equal(t, inherited.Tools.InternalTools, []string{"structured_output"}, "inherited internal tools")
		disabled := pfOK(t, launchContractInput{Agent: "typed", Cwd: cwd, Task: "Inspect", OutputSchema: false})
		equal(t, disabled.Tools.InternalTools, []string{}, "disabled internal tools")
		equal(t, inherited.LaunchContractDigest != disabled.LaunchContractDigest, true, "digests differ")

		writeFile(t, filepath.Join(cwd, ".pi", "agents", "external.md"), "---\nname: external\ndescription: External\noutputSchema: {\"type\":\"object\"}\nrunner:\n  type: external-cli\n  command: "+strconv.Quote(os.Args[0])+"\n---\nPrompt.\n")
		rejected := pfResolve(t, launchContractInput{Agent: "external", Cwd: cwd})
		equal(t, rejected.OK, false, "external with schema refused")
		equal(t, rejected.Code, "unsupported_mode", "code")
		equal(t, pfResolve(t, launchContractInput{Agent: "external", Cwd: cwd, OutputSchema: false}).OK, true, "external with schema disabled")
	})

	tw(t, "preflight", "projects per-agent tool exclusions and binds them into launch identity", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("exclude-tools-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ntools: read, write\nexcludeTools: write, unknown_tool\n---\nProject prompt.\n")
		c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect"})
		equal(t, c.Tools.ExcludeTools, []string{"write", "unknown_tool"}, "exclude tools")
		equal(t, c.Tools.EffectiveAllowlist, []string{"read", "contact_supervisor"}, "effective allowlist")
		polMatches(t, c.LaunchContractDigest, hex64)
	})

	tw(t, "preflight", "binds Intercom bridge activation into launch identity without touching definition identity", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("bridge-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ntools: read\n---\nProject prompt.\n")
		in := launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect"}
		base := pfOK(t, in)
		equal(t, [2]any{base.BridgeMode, base.BridgeActive}, [2]any{"always", true}, "base bridge")
		equal(t, contains(base.Tools.EffectiveAllowlist, "contact_supervisor"), true, "bridge tool")
		for _, d := range base.Diagnostics {
			equal(t, strings.Contains(d.Message, "orchestratorTarget"), false, "no target diagnostic")
		}
		// The default template never names the parent session, so a host that supplies its real target gets the same digest as
		// one that does not.
		withTargetIn := in
		withTargetIn.OrchestratorTarget = "subagent-chat-1234"
		withTarget := pfOK(t, withTargetIn)
		equal(t, withTarget.LaunchContractDigest, base.LaunchContractDigest, "target does not change the digest")

		offIn := in
		offIn.IntercomBridge = js(t, `{"mode":"off"}`)
		off := pfOK(t, offIn)
		equal(t, [2]any{off.BridgeMode, off.BridgeActive}, [2]any{"off", false}, "off bridge")
		equal(t, contains(off.Tools.EffectiveAllowlist, "contact_supervisor"), false, "no bridge tool when off")
		equal(t, off.LaunchContractDigest != base.LaunchContractDigest, true, "off changes the digest")
		equal(t, off.Agent.DefinitionDigest, base.Agent.DefinitionDigest, "definition digest unchanged")

		// Inactive fork-only for a fresh launch hashes exactly like off: activation, not the mode label, is bound.
		forkOnlyIn := in
		forkOnlyIn.Context = new("fresh")
		forkOnlyIn.IntercomBridge = js(t, `{"mode":"fork-only"}`)
		forkOnly := pfOK(t, forkOnlyIn)
		equal(t, [2]any{forkOnly.BridgeMode, forkOnly.BridgeActive}, [2]any{"fork-only", false}, "fork-only bridge")
		equal(t, forkOnly.LaunchContractDigest, off.LaunchContractDigest, "fork-only hashes like off")
	})

	tw(t, "preflight", "requires a host target only when the bridge instruction file names the session", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("bridge-template-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nProject prompt.\n")
		instructionFile := filepath.Join(e.temp, "bridge.md")
		writeFile(t, instructionFile, "Custom bridge for {orchestratorTarget}\nUse ask then send.\n")
		in := launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect", IntercomBridge: js(t, `{"mode":"always","instructionFile":`+strconv.Quote(instructionFile)+`}`)}
		withoutTarget := pfOK(t, in)
		found := false
		for _, d := range withoutTarget.Diagnostics {
			found = found || d.Code == "host_required" && strings.Contains(d.Message, "orchestratorTarget")
		}
		equal(t, found, true, "host_required for the target")
		in.OrchestratorTarget = "main"
		withTarget := pfOK(t, in)
		for _, d := range withTarget.Diagnostics {
			equal(t, strings.Contains(d.Message, "orchestratorTarget"), false, "target diagnostic gone")
		}
		equal(t, withTarget.LaunchContractDigest != withoutTarget.LaunchContractDigest, true, "digests differ")
	})

	tw(t, "preflight", "fails closed for an invalid intercomBridge override", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("bridge-invalid-repo")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\n---\nProject prompt.\n")
		for _, bridge := range []any{js(t, `{"mode":"loud"}`), js(t, `{"extra":true}`), "always"} {
			r := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, IntercomBridge: bridge})
			equal(t, r.OK, false, "bridge refused")
			equal(t, r.Code, "invalid_intercom_bridge", "bridge code")
		}
		// Execution never has an empty target, so accepting one would silently deactivate the bridge in preflight only.
		for _, target := range []any{"", "   ", float64(7)} {
			r := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, OrchestratorTarget: target})
			equal(t, r.OK, false, "target refused")
			equal(t, r.Code, "invalid_intercom_bridge", "target code")
			polMatches(t, r.Message, `orchestratorTarget`)
		}
	})

	tw(t, "preflight", "falls back implicit default fork to fresh when the parent session is not forkable", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-implicit-fork")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ndefaultContext: fork\n---\nProject prompt.\n")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd}).Context, "fresh", "missing session")
		missingFile := filepath.Join(e.temp, "missing-parent.jsonl")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentSessionFile: missingFile, ParentLeafID: "leaf-current"}).Context, "fresh", "unpersisted session")
		parentSessionFile := filepath.Join(e.temp, "implicit-parent.jsonl")
		writeFile(t, parentSessionFile, `{"type":"session","version":1,"id":"parent","timestamp":"2026-04-16T00:00:00.000Z","cwd":"/tmp"}`+"\n")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentSessionFile: parentSessionFile}).Context, "fresh", "missing leaf")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Context: new("fork")}).Context, "fork", "explicit fork")
	})

	tw(t, "preflight", "applies defaultSubagentContext fork without overriding explicit fresh", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-global-fork")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ndefaultContext: fresh\n---\nProject prompt.\n")
		e.writeJSON(filepath.Join(e.agentDir, "extensions", "subagent", "config.json"), `{"defaultSubagentContext":"fork"}`)
		parentSessionFile := filepath.Join(e.temp, "global-parent.jsonl")
		writeFile(t, parentSessionFile, `{"type":"session","version":1,"id":"parent","timestamp":"2026-04-16T00:00:00.000Z","cwd":"/tmp"}`+"\n")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentSessionFile: parentSessionFile, ParentLeafID: "leaf-current"}).Context, "fork", "implicit")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, Context: new("fresh"), ParentSessionFile: parentSessionFile, ParentLeafID: "leaf-current"}).Context, "fresh", "explicit fresh")
		equal(t, pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd}).Context, "fresh", "unavailable fork")
	})

	tw(t, "preflight", "applies defaultSubagentContext fresh over an agent fork default", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-global-fresh")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ndefaultContext: fork\n---\nProject prompt.\n")
		e.writeJSON(filepath.Join(e.agentDir, "extensions", "subagent", "config.json"), `{"defaultSubagentContext":"fresh"}`)
		c := pfOK(t, launchContractInput{Agent: "worker", Cwd: cwd, ParentSessionFile: filepath.Join(e.temp, "global-parent.jsonl"), ParentLeafID: "leaf-current"})
		equal(t, c.Context, "fresh", "context")
	})

	tw(t, "preflight", "fails closed when a capability ceiling denies read required for child skills", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo")
		e.writeSkill(cwd, "project-skill")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ntools:\n  - read\nskills:\n  - project-skill\n---\nProject prompt.\n")
		r := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd,
			CapabilityCeiling: &resolvedCeiling{Version: 1, AllowedTools: []string{}, HasAllowedTools: true, Sources: []string{"test"}}})
		equal(t, r.OK, false, "refused")
		equal(t, r.Code, "denied_required_tool", "code")
		polMatches(t, r.Message, `excludes required tool 'read'`)
	})

	tw(t, "preflight", "reports unresolved runtime-style MCP selectors during preflight", func(t *testing.T) {
		e := newPfEnv(t)
		cwd := e.dir("repo-unresolved-runtime-mcp")
		writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Project worker\ntools:\n  - read\n  - mcp:rt__wiki/read_wiki_structure\n---\nProject prompt.\n")
		r := pfResolve(t, launchContractInput{Agent: "worker", Cwd: cwd, Task: "Inspect"})
		equal(t, r.OK, false, "refused")
		equal(t, r.Code, "denied_required_tool", "code")
		polMatches(t, r.Message, `Unresolved MCP direct-tool selectors: rt__wiki/read_wiki_structure\.`)
	})
}
