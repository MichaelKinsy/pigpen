package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Twins of test/unit/agent-management.test.ts (the management actions; the /subagents admin panel and the runtime agent registry
// cases are twinned with their own slices).

type mgmtEnv struct{ dir string }

func newMgmtEnv(t *testing.T) mgmtEnv {
	t.Helper()
	withTempHome(t)
	t.Setenv("PI_OFFLINE", "true")
	dir := tmp(t)
	// Anchor project discovery here, as the original does.
	mkdirAll(t, filepath.Join(dir, ".pi"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, "agent-home"))
	clearSkillCache()
	clearAgentDiscoveryCache()
	t.Cleanup(func() { clearSkillCache(); clearAgentDiscoveryCache() })
	return mgmtEnv{dir}
}

func (e mgmtEnv) ctx() mgmtContext { return testMgmtCtx(e.dir) }
func (e mgmtEnv) act(action string, params map[string]any) manageResult {
	return handleManagementAction(action, params, e.ctx())
}
func (e mgmtEnv) agentsDir() string { return filepath.Join(e.dir, ".pi", "agents") }
func (e mgmtEnv) read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func (e mgmtEnv) write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(e.agentsDir(), name)
	writeFile(t, p, content)
	return p
}

func (e mgmtEnv) capabilityRows(t *testing.T, r manageResult) []*jsObject {
	t.Helper()
	if r.details == nil {
		t.Fatal("no details")
	}
	snap := r.details.obj("agentCapabilities")
	if snap == nil {
		t.Fatal("no agentCapabilities")
	}
	var rows []*jsObject
	for _, a := range snap.vals["agents"].([]any) {
		rows = append(rows, a.(*jsObject))
	}
	return rows
}

func rowNamed(rows []*jsObject, name string) *jsObject {
	for _, r := range rows {
		if n, _ := r.str("name"); n == name {
			return r
		}
	}
	return nil
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAgentManagement(t *testing.T) {
	const f = "agent-management"
	tw(t, f, "surfaces JSON parse errors for create config strings", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": `{"name":`})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "config must be valid JSON:")
	})
	tw(t, f, "hides lower-priority agents shadowed by project agents in list output", func(t *testing.T) {
		e := newMgmtEnv(t)
		e.write(t, "scout.md", "---\nname: scout\ndescription: Project scout override\n---\n\nProject scout agent.\n")
		r := e.act("list", map[string]any{"agentScope": "project"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `- scout \(project\): Project scout override`)
		noRe(t, r.text, `- scout \(builtin`)
	})
	tw(t, f, "lists compact declared capabilities without exposing system prompts", func(t *testing.T) {
		e := newMgmtEnv(t)
		e.write(t, "capability-worker.md", strings.Join([]string{"---", "name: capability-worker", "description: Capability worker", "aliases: capability",
			"tools: read, grep, mcp:github/search", "model: openai/gpt-5-mini", "async: true", "timeoutMs: 123", "thinking: high", "acceptance:",
			"  level: checked", "  report: on", "  evidence:", "    - changed-files", "    - commands-run", "  verify:", "    - id: unit", "      command: npm test",
			"  criteria:", "    - Patch the bug", "    - Keep the diff narrow", "  stopRules:", "    - Do not stop after analysis", "  review:", "    agent: reviewer",
			"acceptanceRole: writer", "skills: typescript-code", "extensions: github", "subagentOnlyExtensions: surf", "mutationTools: edit, write",
			"output: report.md", "outputMode: file-only", "machine: workmac", "---", "SYSTEM_PROMPT_SENTINEL", "---"}, "\n"))
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `^Executable agents \(capabilities\):`)
		hasRe(t, r.text, `- capability-worker \(project, machine: workmac \(saved Herdr placement\), aliases: capability\): Description: Capability worker; Tools: read, grep, mcp:github/search; Model: openai/gpt-5-mini; Thinking: high; Machine: workmac \(saved Herdr placement\); Acceptance: checked \(changed-files, commands-run, verify: "unit", criteria: 2, stopRules: 1, review: required by "reviewer", report: on\); Acceptance role: writer`)
		noRe(t, r.text, "unsupported for native agents")
		noRe(t, r.text, "System Prompt:|SYSTEM_PROMPT_SENTINEL")
		rows := e.capabilityRows(t, r)
		row := rowNamed(rows, "capability-worker")
		if row == nil {
			t.Fatal("no row")
		}
		eq(t, row.vals["executable"], true, "executable")
		eq(t, canonJSON(row.vals["tools"]), `{"ambient":false,"mcpDirectTools":["github/search"],"mutationTools":["edit","write"],"names":["read","grep"]}`, "tools")
		eq(t, canonJSON(row.vals["model"]), `{"thinking":"high","value":"openai/gpt-5-mini"}`, "model")
		eq(t, canonJSON(row.vals["execution"]), `{"defaultAsync":true,"timeoutMs":123}`, "execution")
		eq(t, canonJSON(row.vals["acceptance"]), `{"policy":{"criteria":["Patch the bug","Keep the diff narrow"],"evidence":["changed-files","commands-run"],"level":"checked","report":"on","review":{"agent":"reviewer"},"stopRules":["Do not stop after analysis"],"verify":[{"command":"npm test","id":"unit"}]},"role":"writer"}`, "acceptance")
		eq(t, canonJSON(row.vals["output"]), `{"mode":"file-only","path":"report.md"}`, "output")
		eq(t, canonJSON(row.vals["extensions"]), `{"names":["github"],"skills":["typescript-code"],"subagentOnly":["surf"]}`, "extensions")
		noRe(t, marshalJSON(r.details, ""), "SYSTEM_PROMPT_SENTINEL")
	})
	tw(t, f, "reports bundled reviewer supervisor contact without mutation tools in capabilities", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		reviewer := rowNamed(e.capabilityRows(t, r), "reviewer")
		if reviewer == nil {
			t.Fatal("reviewer builtin should be present in capability output")
		}
		eq(t, reviewer.obj("tools").vals["names"], l{"read", "grep", "find", "ls", "watchdog_diff", "contact_supervisor"}, "names")
		hasRe(t, r.text, `Tools: read, grep, find, ls, watchdog_diff, contact_supervisor`)
		eq(t, reviewer.has("acceptance"), false, "acceptance")
	})
	tw(t, f, "reports bare and disabled acceptance declarations in capabilities", func(t *testing.T) {
		e := newMgmtEnv(t)
		e.write(t, "accepted-worker.md", "---\nname: accepted-worker\ndescription: Accepted worker\nacceptance: checked\n---\n\nAccepted.\n")
		e.write(t, "open-worker.md", "---\nname: open-worker\ndescription: Open worker\nacceptance: false\n---\n\nOpen.\n")
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `(?m)- accepted-worker \(project\): Description: Accepted worker; .*; Acceptance: checked$`)
		hasRe(t, r.text, `(?m)- open-worker \(project\): Description: Open worker; .*; Acceptance: disabled$`)
		rows := e.capabilityRows(t, r)
		eq(t, canonJSON(rowNamed(rows, "accepted-worker").vals["acceptance"]), `{"policy":"checked"}`, "accepted")
		eq(t, canonJSON(rowNamed(rows, "open-worker").vals["acceptance"]), `{"policy":false}`, "open")
	})
	tw(t, f, "reports optional review gates as optional in capabilities", func(t *testing.T) {
		e := newMgmtEnv(t)
		e.write(t, "optional-review.md", "---\nname: optional-review\ndescription: Optional review\nacceptance:\n  level: checked\n  review:\n    required: false\n---\n\nOptional.\n")
		e.write(t, "named-optional-review.md", "---\nname: named-optional-review\ndescription: Named optional review\nacceptance:\n  level: checked\n  review:\n    agent: reviewer\n    required: false\n---\n\nOptional.\n")
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `(?m)- optional-review \(project\): .*; Acceptance: checked \(review: optional\)$`)
		hasRe(t, r.text, `(?m)- named-optional-review \(project\): .*; Acceptance: checked \(review: optional by "reviewer"\)$`)
		rows := e.capabilityRows(t, r)
		eq(t, canonJSON(rowNamed(rows, "optional-review").vals["acceptance"]), `{"policy":{"level":"checked","review":{"required":false}}}`, "optional")
		eq(t, canonJSON(rowNamed(rows, "named-optional-review").vals["acceptance"]), `{"policy":{"level":"checked","review":{"agent":"reviewer","required":false}}}`, "named")
	})
	tw(t, f, "safely bounds acceptance labels in capability summaries without changing structured values", func(t *testing.T) {
		e := newMgmtEnv(t)
		verifyID := "unit, ); Acceptance role: forged\n- forged-verify\u0007" + strings.Repeat("v", 100)
		reviewAgent := "reviewer; report: forged\n- forged-review\u001b[31m" + strings.Repeat("r", 100)
		acc := m{"level": "checked", "verify": l{m{"id": verifyID, "command": "true"}}, "review": m{"agent": reviewAgent}}
		e.write(t, "unsafe-acceptance.md", "---\nname: unsafe-acceptance\ndescription: Unsafe acceptance labels\nacceptance: "+marshalJSON(acc, "")+"\n---\nUnsafe labels.")
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		var matching []string
		for _, line := range strings.Split(r.text, "\n") {
			if strings.HasPrefix(line, "- unsafe-acceptance ") {
				matching = append(matching, line)
			}
		}
		eq(t, len(matching), 1, "rows")
		hasRe(t, matching[0], `verify: "unit, \); Acceptance role: forged - forged-verify v+\.\.\."`)
		hasRe(t, matching[0], `review: required by "reviewer; report: forged - forged-review r+\.\.\."`)
		noRe(t, r.text, "[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f\u001b]")
		noRe(t, r.text, `\n- forged-(?:verify|review)`)
		row := rowNamed(e.capabilityRows(t, r), "unsafe-acceptance")
		eq(t, canonJSON(row.vals["acceptance"]), canonJSON(obj("policy", parseJSONMust(t, marshalJSON(acc, "")))), "structured values")
	})
	tw(t, f, "reports passive external CLI availability for present and absent commands", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fake CLI is a POSIX shell script")
		}
		e := newMgmtEnv(t)
		bin := filepath.Join(e.dir, "pathbin")
		mkdirAll(t, bin)
		writeScript(t, bin, "present-cli", "exit 0")
		e.write(t, "present-external.md", "---\nname: present-external\ndescription: Present external CLI\nrunner:\n  type: external-cli\n  command: present-cli\n---\nPresent.\n")
		e.write(t, "missing-external.md", "---\nname: missing-external\ndescription: Missing external CLI\nrunner:\n  type: external-cli\n  command: missing-external-cli\n---\nMissing.\n")
		t.Setenv("PATH", bin)
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `external-cli:present-cli ✓`)
		hasRe(t, r.text, `external-cli:missing-external-cli missing`)
		rows := e.capabilityRows(t, r)
		present, missing := rowNamed(rows, "present-external"), rowNamed(rows, "missing-external")
		eq(t, []any{present.vals["executable"], missing.vals["executable"]}, []any{true, true}, "executable")
		pr, mr := present.obj("runner"), missing.obj("runner")
		eq(t, []any{pr.vals["type"], mr.vals["type"]}, []any{"external-cli", "external-cli"}, "type")
		eq(t, []any{pr.vals["command"], pr.vals["available"], pr.has("unavailableReason")}, []any{"present-cli", true, false}, "present")
		eq(t, []any{mr.vals["command"], mr.vals["available"]}, []any{"missing-external-cli", false}, "missing")
		reason, _ := mr.str("unavailableReason")
		hasRe(t, reason, `External CLI binary 'missing-external-cli' was not found on PATH\.`)
		eq(t, utf16Length(reason) <= 256, true, "reason length")
	})
	tw(t, f, "does not preflight Herdr machines while listing capabilities", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fake ssh is a POSIX shell script")
		}
		e := newMgmtEnv(t)
		e.write(t, "remote-external.md", "---\nname: remote-external\ndescription: Remote external CLI\nmachine: workmac\nrunner:\n  type: external-cli\n  adapter: codex-exec\n  command: codex\n---\nRemote.\n")
		bin := filepath.Join(e.dir, "bin")
		mkdirAll(t, bin)
		writeScript(t, bin, "ssh", "exit 0")
		t.Setenv("PATH", bin)
		t.Setenv("HERDR_BIN", filepath.Join(bin, "missing-herdr"))
		r := e.act("list", map[string]any{"agentScope": "project", "capabilities": true})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `external-cli:codex @ workmac saved Herdr placement; transport ✓; machine not preflighted`)
		runner := rowNamed(e.capabilityRows(t, r), "remote-external").obj("runner")
		eq(t, []any{runner.vals["type"], runner.vals["available"]}, []any{"external-cli", true}, "runner")
	})
	tw(t, f, "rejects management attempts to widen the reserved read-only Claude profile", func(t *testing.T) {
		e := newMgmtEnv(t)
		writer := m{"type": "external-cli", "adapter": "claude-code-writer", "command": "claude"}
		r := e.act("create", map[string]any{"config": m{"name": "claude-code", "description": "Unsafe shadow", "scope": "project", "runner": writer}})
		eq(t, r.isError, true, "create")
		hasRe(t, r.text, `reserved for the read-only 'claude-code' adapter`)
		r = e.act("create", map[string]any{"config": m{"name": "claude-code", "package": "custom", "description": "Unsafe local name", "scope": "project", "runner": writer}})
		eq(t, r.isError, true, "local name")
		hasRe(t, r.text, `Selection name 'claude-code' is reserved`)
		r = e.act("create", map[string]any{"config": m{"name": "aliased-writer", "aliases": l{"claude-code"}, "description": "Unsafe alias", "scope": "project", "runner": writer}})
		eq(t, r.isError, true, "alias")
		hasRe(t, r.text, `Selection name 'claude-code' is reserved`)
		r = e.act("create", map[string]any{"config": m{"name": "claude-code", "description": "Narrow shadow", "scope": "project", "runner": m{"type": "external-cli", "adapter": "claude-code", "command": "claude"}}})
		eq(t, r.isError, false, "read-only create")
		r = e.act("update", map[string]any{"agent": "claude-code", "agentScope": "project", "config": m{"runner": writer}})
		eq(t, r.isError, true, "update")
		hasRe(t, r.text, `reserved for the read-only 'claude-code' adapter`)
		hasRe(t, e.read(t, filepath.Join(e.agentsDir(), "claude-code.md")), `adapter: claude-code\n`)
		r = e.act("create", map[string]any{"config": m{"name": "custom-writer", "description": "Writer", "scope": "project", "runner": writer}})
		eq(t, r.isError, false, "writer create")
		r = e.act("update", map[string]any{"agent": "custom-writer", "agentScope": "project", "config": m{"aliases": l{"claude-code"}}})
		eq(t, r.isError, true, "alias update")
		hasRe(t, r.text, `Selection name 'claude-code' is reserved`)
		r = e.act("update", map[string]any{"agent": "custom-writer", "agentScope": "project", "config": m{"name": "claude-code"}})
		eq(t, r.isError, true, "rename")
		hasRe(t, r.text, `reserved for the read-only 'claude-code' adapter`)
	})
	tw(t, f, "rejects create, update, alias, and rename widening for Codex and Cursor", func(t *testing.T) {
		e := newMgmtEnv(t)
		for _, c := range [][3]string{{"codex-exec", "codex-exec-writer", "codex"}, {"cursor-agent", "cursor-agent-writer", "cursor-agent"}} {
			readOnly, writerName, command := c[0], c[1], c[2]
			writer := m{"type": "external-cli", "adapter": writerName, "command": command}
			isErr := func(action string, params map[string]any) bool { return e.act(action, params).isError }
			eq(t, isErr("create", map[string]any{"config": m{"name": readOnly, "description": "Unsafe shadow", "scope": "project", "runner": writer}}), true, "shadow")
			eq(t, isErr("create", map[string]any{"config": m{"name": readOnly, "package": "custom-" + readOnly, "description": "Unsafe local name", "scope": "project", "runner": writer}}), true, "local name")
			eq(t, isErr("create", map[string]any{"config": m{"name": readOnly + "-alias", "aliases": l{readOnly}, "description": "Unsafe alias", "scope": "project", "runner": writer}}), true, "alias")
			eq(t, isErr("create", map[string]any{"config": m{"name": readOnly, "description": "Narrow shadow", "scope": "project", "runner": m{"type": "external-cli", "adapter": readOnly, "command": command}}}), false, "narrow")
			eq(t, isErr("update", map[string]any{"agent": readOnly, "agentScope": "project", "config": m{"runner": writer}}), true, "update")
			customName := "custom-" + writerName
			eq(t, isErr("create", map[string]any{"config": m{"name": customName, "description": "Writer", "scope": "project", "runner": writer}}), false, "writer")
			eq(t, isErr("update", map[string]any{"agent": customName, "agentScope": "project", "config": m{"aliases": l{readOnly}}}), true, "alias update")
			eq(t, isErr("update", map[string]any{"agent": customName, "agentScope": "project", "config": m{"name": readOnly}}), true, "rename")
		}
	})
	tw(t, f, "lists valid agents and diagnoses malformed agent definitions", func(t *testing.T) {
		e := newMgmtEnv(t)
		e.write(t, "broken.md", "---\nname: broken\ndescription: Broken\nrunner:\n  type: unknown\n---\nBroken agent.\n")
		e.write(t, "working.md", "---\nname: working\ndescription: Working\n---\nWorking agent.\n")
		listed := e.act("list", map[string]any{})
		eq(t, listed.isError, false, "list")
		hasRe(t, listed.text, `- working \(project`)
		hasRe(t, listed.text, `Invalid agent definitions:\n- broken \(project\): Agent 'broken' has invalid runner\.type`)
		invalid := e.act("get", map[string]any{"agent": "broken"})
		eq(t, invalid.isError, true, "get")
		hasRe(t, invalid.text, `Agent 'broken' has invalid configuration: Agent 'broken' has invalid runner\.type`)
		e.write(t, "bad-agent.md", "---\nname: bad-agent\ndescription: Bad agent\nrunner:\n  type: unknown\n---\nBroken agent.\n")
		n := e.act("get", map[string]any{"agent": "Bad Agent"})
		eq(t, n.isError, true, "normalized")
		hasRe(t, n.text, `Agent 'Bad Agent' has invalid configuration: Agent 'bad-agent' has invalid runner\.type`)
		e.write(t, "code-analysis.zeta-worker.md", "---\nname: zeta-worker\npackage: code-analysis\ndescription: Broken packaged worker\nrunner:\n  type: unknown\n---\nBroken agent.\n")
		p := e.act("get", map[string]any{"agent": "code-analysis.zeta-worker"})
		eq(t, p.isError, true, "packaged")
		hasRe(t, p.text, `Agent 'code-analysis\.zeta-worker' has invalid configuration: Agent 'zeta-worker' has invalid runner\.type`)
		e.write(t, "reviewer.md", "---\nname: reviewer\ndescription: Broken reviewer\nrunner:\n  type: unknown\n---\nBroken agent.\n")
		s := e.act("get", map[string]any{"agent": "reviewer"})
		eq(t, s.isError, true, "shadow")
		hasRe(t, s.text, `Agent 'reviewer' has invalid configuration: Agent 'reviewer' has invalid runner\.type`)
	})
	tw(t, f, "gets only the effective agent detail and respects explicit scope", func(t *testing.T) {
		e := newMgmtEnv(t)
		userAgents := filepath.Join(e.dir, "agent-home", "agents")
		pkg := filepath.Join(e.dir, ".pi", "npm", "node_modules", "test-agents")
		e.write(t, "worker.md", "---\nname: worker\ndescription: Project worker override\n---\n\nProject worker.\n")
		writeFile(t, filepath.Join(userAgents, "worker.md"), "---\nname: worker\ndescription: User worker override\n---\n\nUser worker.\n")
		wjc(t, filepath.Join(pkg, "package.json"), m{"pi-subagents": m{"agents": l{"agents"}}})
		writeFile(t, filepath.Join(pkg, "agents", "worker.md"), "---\nname: worker\ndescription: Package worker override\n---\n\nPackage worker.\n")
		eff := e.act("get", map[string]any{"agent": "worker"}).text
		hasRe(t, eff, `Agent: worker \(project\)`)
		hasRe(t, eff, `Description: Project worker override`)
		noRe(t, eff, `User worker override|Package worker override|Implementation agent for normal tasks`)
		us := e.act("get", map[string]any{"agent": "worker", "agentScope": "user"}).text
		hasRe(t, us, `Agent: worker \(user\)`)
		hasRe(t, us, `Description: User worker override`)
		noRe(t, us, `Project worker override|Implementation agent for normal tasks`)
	})
	tskip(t, f, "surfaces package source and external-job provider status in list and get", "registers an external-job provider through registerExternalJobProvider (src/api/external-job-provider.ts), a registry that lives on a JavaScript global shared between extensions of one Node process (globalThis[Symbol.for(...)]); Go extensions run in separate processes and share no registry, so the port's registry is always empty and the `✓` status cannot be produced")
	tskip(t, f, "keeps external-job provider registry errors visible", "replaces the JavaScript global registry object (globalThis[Symbol.for(EXTERNAL_JOB_PROVIDER_REGISTRY_KEY)]) with an unsupported version to provoke `Unsupported external-job provider registry`; the port has no such global and its registry cannot be in an unsupported state")
	tw(t, f, "does not apply a malformed project diagnostic to an explicit user get", func(t *testing.T) {
		e := newMgmtEnv(t)
		writeFile(t, filepath.Join(e.dir, "agent-home", "agents", "worker.md"), "---\nname: worker\ndescription: User worker\n---\nUser worker.\n")
		e.write(t, "worker.md", "---\nname: worker\ndescription: Broken project worker\nrunner:\n  type: unknown\n---\nBroken worker.\n")
		r := e.act("get", map[string]any{"agent": "worker", "agentScope": "user"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `Agent: worker \(user\)`)
		hasRe(t, r.text, `Description: User worker`)
	})
	tw(t, f, "blocks a malformed higher-precedence user agent from an extra user agent", func(t *testing.T) {
		e := newMgmtEnv(t)
		extra := filepath.Join(e.dir, "extra-agents")
		t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", extra)
		writeFile(t, filepath.Join(extra, "foo.md"), "---\nname: foo\ndescription: Extra user foo\n---\nExtra user foo.\n")
		writeFile(t, filepath.Join(e.dir, "agent-home", "agents", "foo.md"), "---\nname: foo\ndescription: Broken configured user foo\nrunner:\n  type: unknown\n---\nBroken user foo.\n")
		r := e.act("get", map[string]any{"agent": "foo", "agentScope": "user"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)
	})
	tw(t, f, "blocks a malformed higher-precedence package agent from a lower package agent", func(t *testing.T) {
		e := newMgmtEnv(t)
		high, low := filepath.Join(e.dir, "high-package"), filepath.Join(e.dir, "low-package")
		for _, root := range []string{high, low} {
			wjc(t, filepath.Join(root, "package.json"), m{"pi-subagents": m{"agents": l{"agents"}}})
		}
		writeFile(t, filepath.Join(high, "agents", "foo.md"), "---\nname: foo\npackage: acme\ndescription: Broken high package foo\nrunner:\n  type: unknown\n---\nBroken foo.\n")
		writeFile(t, filepath.Join(low, "agents", "foo.md"), "---\nname: foo\npackage: acme\ndescription: Valid low package foo\n---\nValid foo.\n")
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), m{"packages": l{high, low}})
		r := e.act("get", map[string]any{"agent": "acme.foo"})
		eq(t, r.isError, true, "runtime name")
		hasRe(t, r.text, `Agent 'acme\.foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)
		r = e.act("get", map[string]any{"agent": "foo"})
		eq(t, r.isError, true, "local name")
		hasRe(t, r.text, `Agent 'foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)
	})
	tw(t, f, "reports a malformed project agent before lower-priority ambiguity", func(t *testing.T) {
		e := newMgmtEnv(t)
		root := filepath.Join(e.dir, "package")
		wjc(t, filepath.Join(root, "package.json"), m{"pi-subagents": m{"agents": l{"agents"}}})
		writeFile(t, filepath.Join(root, "agents", "foo.md"), "---\nname: foo\npackage: acme\ndescription: Package foo\n---\nPackage foo.\n")
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), m{"packages": l{root}})
		e.write(t, "foo.md", "---\nname: foo\ndescription: Broken project foo\nrunner:\n  type: unknown\n---\nBroken foo.\n")
		writeFile(t, filepath.Join(e.dir, "agent-home", "agents", "foo.md"), "---\nname: foo\ndescription: User foo\n---\nUser foo.\n")
		r := e.act("get", map[string]any{"agent": "foo"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)
	})
	tw(t, f, "does not let malformed packaged diagnostics block an un-packaged local name", func(t *testing.T) {
		e := newMgmtEnv(t)
		writeFile(t, filepath.Join(e.dir, "agent-home", "agents", "foo.md"), "---\nname: foo\ndescription: User foo\n---\nUser foo.\n")
		e.write(t, "acme.foo.md", "---\nname: foo\npackage: acme\ndescription: Broken packaged foo\nrunner:\n  type: unknown\n---\nBroken foo.\n")
		local := e.act("get", map[string]any{"agent": "foo"})
		eq(t, local.isError, false, "local")
		hasRe(t, local.text, `Agent: foo \(user\)`)
		packaged := e.act("get", map[string]any{"agent": "acme.foo"})
		eq(t, packaged.isError, true, "packaged")
		hasRe(t, packaged.text, `Agent 'acme\.foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)
	})
	tw(t, f, "blocks a malformed invalid-package override of a local agent", func(t *testing.T) {
		e := newMgmtEnv(t)
		writeFile(t, filepath.Join(e.dir, "agent-home", "agents", "foo.md"), "---\nname: foo\ndescription: User foo\n---\nUser foo.\n")
		e.write(t, "foo.md", "---\nname: foo\npackage: !!!\ndescription: Broken package\n---\nBroken foo.\n")
		r := e.act("get", map[string]any{"agent": "foo"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'foo' has invalid configuration: Agent 'foo' package is invalid after sanitization`)
	})
	tw(t, f, "blocks a malformed .pi agent from falling back to a legacy project agent", func(t *testing.T) {
		e := newMgmtEnv(t)
		writeFile(t, filepath.Join(e.dir, ".agents", "shared.md"), "---\nname: shared\ndescription: Legacy shared\n---\nLegacy shared.\n")
		e.write(t, "shared.md", "---\nname: shared\ndescription: Broken canonical shared\nrunner:\n  type: unknown\n---\nBroken shared.\n")
		r := e.act("get", map[string]any{"agent": "shared"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'shared' has invalid configuration: Agent 'shared' has invalid runner\.type`)
	})
	tw(t, f, "blocks a malformed custom canonical agent directory from falling back to legacy", func(t *testing.T) {
		e := newMgmtEnv(t)
		root := filepath.Join(e.dir, "coding-agent-root")
		wjc(t, filepath.Join(root, "package.json"), m{"name": "@earendil-works/pi-coding-agent", "piConfig": m{"configDir": ".custom-pi"}})
		t.Setenv(piCodingAgentPackageRootEnv, root)
		writeFile(t, filepath.Join(e.dir, ".agents", "shared.md"), "---\nname: shared\ndescription: Legacy shared\n---\nLegacy shared.\n")
		writeFile(t, filepath.Join(e.dir, ".custom-pi", "agents", "shared.md"), "---\nname: shared\ndescription: Broken canonical shared\nrunner:\n  type: unknown\n---\nBroken shared.\n")
		r := e.act("get", map[string]any{"agent": "shared"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'shared' has invalid configuration: Agent 'shared' has invalid runner\.type`)
	})
	tw(t, f, "blocks a malformed canonical .agents/agents definition from legacy .agents", func(t *testing.T) {
		e := newMgmtEnv(t)
		root := filepath.Join(e.dir, "coding-agent-root")
		wjc(t, filepath.Join(root, "package.json"), m{"name": "@earendil-works/pi-coding-agent", "piConfig": m{"configDir": ".agents"}})
		t.Setenv(piCodingAgentPackageRootEnv, root)
		writeFile(t, filepath.Join(e.dir, ".agents", "foo.md"), "---\nname: foo\ndescription: Legacy foo\n---\nLegacy foo.\n")
		writeFile(t, filepath.Join(e.dir, ".agents", "agents", "foo.md"), "---\nname: foo\ndescription: Broken canonical foo\nrunner:\n  type: unknown\n---\nBroken foo.\n")
		r := e.act("get", map[string]any{"agent": "foo"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'foo' has invalid configuration: Agent 'foo' has invalid runner\.type`)
	})
	tw(t, f, "surfaces JSON parse errors for update config strings", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("update", map[string]any{"agent": "reviewer", "config": `{"description":`})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "config must be valid JSON:")
	})
	tw(t, f, "creates, gets, updates, and deletes a packaged agent by runtime name", func(t *testing.T) {
		e := newMgmtEnv(t)
		created := e.act("create", map[string]any{"config": m{"name": "Scout", "package": "Code Analysis", "description": "Fast recon", "scope": "project", "systemPrompt": "Inspect"}})
		eq(t, created.isError, false, "create")
		hasRe(t, created.text, `Created agent 'code-analysis.scout'`)
		filePath := filepath.Join(e.agentsDir(), "code-analysis.scout.md")
		content := e.read(t, filePath)
		hasRe(t, content, `(?m)^name: scout$`)
		hasRe(t, content, `(?m)^package: code-analysis$`)
		noRe(t, content, `(?m)^name: code-analysis\.scout$`)
		got := e.act("get", map[string]any{"agent": "code-analysis.scout"})
		eq(t, got.isError, false, "get")
		hasRe(t, got.text, `Agent: code-analysis\.scout`)
		hasRe(t, got.text, `Local name: scout`)
		hasRe(t, got.text, `Package: code-analysis`)
		updated := e.act("update", map[string]any{"agent": "code-analysis.scout", "config": m{"package": "documentation"}})
		eq(t, updated.isError, false, "update")
		hasRe(t, updated.text, `code-analysis\.scout' to 'documentation\.scout'`)
		eq(t, fileExists2(filePath), false, "old file")
		updatedPath := filepath.Join(e.agentsDir(), "documentation.scout.md")
		content = e.read(t, updatedPath)
		hasRe(t, content, `(?m)^name: scout$`)
		hasRe(t, content, `(?m)^package: documentation$`)
		deleted := e.act("delete", map[string]any{"agent": "documentation.scout"})
		eq(t, deleted.isError, false, "delete")
		eq(t, fileExists2(updatedPath), false, "deleted")
	})
	tw(t, f, "creates, reports, and clears agent-local skill paths", func(t *testing.T) {
		e := newMgmtEnv(t)
		writeFile(t, filepath.Join(e.agentsDir(), "skills", "private", "SKILL.md"), "---\ndescription: Private skill\n---\nbody\n")
		created := e.act("create", map[string]any{"config": m{"name": "Local", "description": "Local skills", "scope": "project", "skills": "private", "skillPath": l{"./skills", "./skills"}}})
		eq(t, created.isError, false, "create")
		noRe(t, created.text, "skills not found")
		filePath := filepath.Join(e.agentsDir(), "local.md")
		hasRe(t, e.read(t, filePath), `(?m)^skillPath: \./skills$`)
		hasRe(t, e.act("get", map[string]any{"agent": "local"}).text, `(?m)^Skill paths: \./skills$`)
		updated := e.act("update", map[string]any{"agent": "local", "config": m{"skills": false, "skillPath": false}})
		eq(t, updated.isError, false, "update")
		content := e.read(t, filePath)
		noRe(t, content, `(?m)^skills?:`)
		noRe(t, content, `(?m)^skillPath:`)
		invalid := e.act("update", map[string]any{"agent": "local", "config": m{"skillPath": l{"./skills", 1.0}}})
		eq(t, invalid.isError, true, "invalid")
		hasRe(t, invalid.text, `config\.skillPath must be`)
	})
	tw(t, f, "rejects package values that cannot be normalized", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": m{"name": "Scout", "package": "!!!", "description": "Fast recon", "scope": "project"}})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `config\.package is invalid`)
	})
	tw(t, f, "rejects durable chain definitions", func(t *testing.T) {
		e := newMgmtEnv(t)
		c := e.act("create", map[string]any{"config": m{"name": "Review Flow", "description": "Review flow", "scope": "project", "steps": l{m{"agent": "scout", "task": "Inspect"}}}})
		eq(t, c.isError, true, "create")
		hasRe(t, c.text, `Durable chain definitions were removed`)
		a := e.act("create", map[string]any{"config": m{"name": "Scout", "description": "Scout", "scope": "project"}})
		eq(t, a.isError, false, "agent")
		u := e.act("update", map[string]any{"agent": "scout", "config": marshalJSON(m{"steps": l{m{"agent": "scout", "task": "Inspect"}}}, "")})
		eq(t, u.isError, true, "update")
		hasRe(t, u.text, `Durable chain definitions were removed`)
	})
	tw(t, f, "ignores discovered legacy chains during agent management", func(t *testing.T) {
		e := newMgmtEnv(t)
		chains := filepath.Join(e.dir, ".pi", "chains")
		writeFile(t, filepath.Join(chains, "scout.chain.md"), "---\nname: scout\ndescription: Old scout flow\n---\n\n## worker\nInspect\n")
		writeFile(t, filepath.Join(chains, "scout-review.chain.md"), "---\nname: scout-review\ndescription: Old review flow\n---\n\n## worker\nReview\n")
		writeFile(t, filepath.Join(chains, "reference.chain.md"), "---\nname: reference\ndescription: Old reference flow\n---\n\n## scout-review\nUse old agent\n")
		c := e.act("create", map[string]any{"config": m{"name": "Scout", "description": "Scout", "scope": "project"}})
		eq(t, c.isError, false, "create")
		u := e.act("update", map[string]any{"agent": "scout", "config": m{"name": "Scout Review"}})
		eq(t, u.isError, false, "update")
		noRe(t, strings.ReplaceAll(u.text, e.dir, ""), `(?i)chains`)
		d := e.act("delete", map[string]any{"agent": "scout-review"})
		eq(t, d.isError, false, "delete")
		noRe(t, strings.ReplaceAll(d.text, e.dir, ""), `(?i)chains`)
	})
	tw(t, f, "creates and updates agents with single-agent launch defaults", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": m{"name": "background-reviewer", "description": "Review in the background", "scope": "project", "async": false, "timeoutMs": 120000.0,
			"acceptance": m{"level": "none", "reason": "lightweight reviewer"}, "outputMode": "file-only"}})
		eq(t, r.isError, false, "create")
		filePath := filepath.Join(e.agentsDir(), "background-reviewer.md")
		content := e.read(t, filePath)
		hasRe(t, content, `(?m)^async: false$`)
		hasRe(t, content, `(?m)^timeoutMs: 120000$`)
		hasRe(t, content, `(?m)^acceptance: \{"level":"none","reason":"lightweight reviewer"\}$`)
		hasRe(t, content, `(?m)^outputMode: file-only$`)
		got := e.act("get", map[string]any{"agent": "background-reviewer"})
		eq(t, got.isError, false, "get")
		hasRe(t, got.text, `Async: false`)
		hasRe(t, got.text, `Timeout: 120000ms`)
		hasRe(t, got.text, `Acceptance: \{"level":"none","reason":"lightweight reviewer"\}`)
		hasRe(t, got.text, `Output mode: file-only`)
		u := e.act("update", map[string]any{"agent": "background-reviewer", "config": m{"async": true, "timeoutMs": false, "acceptance": "", "outputMode": "inline"}})
		eq(t, u.isError, false, "update")
		content = e.read(t, filePath)
		hasRe(t, content, `(?m)^async: true$`)
		noRe(t, content, `(?m)^timeoutMs:`)
		noRe(t, content, `(?m)^acceptance:`)
		hasRe(t, content, `(?m)^outputMode: inline$`)
		d := e.act("update", map[string]any{"agent": "background-reviewer", "config": m{"acceptance": false}})
		eq(t, d.isError, false, "deprecated false")
		hasRe(t, e.read(t, filePath), `(?m)^acceptance: false$`)
	})
	tw(t, f, "rejects invalid single-agent launch defaults", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": m{"name": "bad-launch-defaults", "description": "Bad defaults", "scope": "project", "timeoutMs": 0.0}})
		eq(t, r.isError, true, "timeout")
		hasRe(t, r.text, `config\.timeoutMs must be a positive integer`)
		a := e.act("create", map[string]any{"config": m{"name": "bad-acceptance-default", "description": "Bad acceptance", "scope": "project", "acceptance": "none"}})
		eq(t, a.isError, true, "acceptance")
		hasRe(t, a.text, `config\.acceptance level "none" requires a reason`)
		o := e.act("create", map[string]any{"config": m{"name": "bad-output-mode", "description": "Bad output mode", "scope": "project", "outputMode": false}})
		eq(t, o.isError, true, "output mode")
		hasRe(t, o.text, `config\.outputMode must be 'inline' or 'file-only'`)
	})
	tw(t, f, "creates and updates agents with tool budgets", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": m{"name": "budgeted-reviewer", "description": "Review with a budget", "scope": "project", "toolBudget": parseJSONMust(t, `{"soft":4,"hard":7,"block":["read","grep"]}`)}})
		eq(t, r.isError, false, "create")
		filePath := filepath.Join(e.agentsDir(), "budgeted-reviewer.md")
		hasRe(t, e.read(t, filePath), `(?m)^toolBudget: \{"soft":4,"hard":7,"block":\["read","grep"\]\}$`)
		got := e.act("get", map[string]any{"agent": "budgeted-reviewer"})
		eq(t, got.isError, false, "get")
		hasRe(t, got.text, `Tool budget: \{"soft":4,"hard":7,"block":\["read","grep"\]\}`)
		u := e.act("update", map[string]any{"agent": "budgeted-reviewer", "config": m{"toolBudget": parseJSONMust(t, `{"hard":3,"block":"*"}`)}})
		eq(t, u.isError, false, "update")
		hasRe(t, e.read(t, filePath), `(?m)^toolBudget: \{"hard":3,"block":"\*"\}$`)
	})
	tw(t, f, "rejects invalid tool budget management config", func(t *testing.T) {
		e := newMgmtEnv(t)
		a := e.act("create", map[string]any{"config": m{"name": "bad-budget", "description": "Bad budget", "scope": "project", "toolBudget": m{"soft": 5.0, "hard": 4.0}}})
		eq(t, a.isError, true, "agent")
		hasRe(t, a.text, `config\.toolBudget\.soft must be <= config\.toolBudget\.hard`)
		c := e.act("create", map[string]any{"config": m{"name": "bad-chain-budget", "description": "Bad budget", "scope": "project", "steps": l{m{"agent": "reviewer"}}}})
		eq(t, c.isError, true, "chain")
		hasRe(t, c.text, `Durable chain definitions were removed`)
	})
	tw(t, f, "creates, updates, reports, clears, and validates acceptance roles", func(t *testing.T) {
		e := newMgmtEnv(t)
		c := e.act("create", map[string]any{"config": m{"name": "explorer", "description": "Explore code", "scope": "project", "acceptanceRole": "read-only"}})
		eq(t, c.isError, false, "create")
		filePath := filepath.Join(e.agentsDir(), "explorer.md")
		hasRe(t, e.read(t, filePath), `(?m)^acceptanceRole: read-only$`)
		hasRe(t, e.act("get", map[string]any{"agent": "explorer"}).text, `Acceptance role: read-only`)
		u := e.act("update", map[string]any{"agent": "explorer", "config": m{"acceptanceRole": "writer"}})
		eq(t, u.isError, false, "update")
		hasRe(t, e.read(t, filePath), `(?m)^acceptanceRole: writer$`)
		cl := e.act("update", map[string]any{"agent": "explorer", "config": m{"acceptanceRole": false}})
		eq(t, cl.isError, false, "clear")
		noRe(t, e.read(t, filePath), `(?m)^acceptanceRole:`)
		eq(t, e.act("update", map[string]any{"agent": "explorer", "config": m{"acceptanceRole": "read-only"}}).isError, false, "set")
		eq(t, e.act("update", map[string]any{"agent": "explorer", "config": m{"acceptanceRole": ""}}).isError, false, "empty")
		noRe(t, e.read(t, filePath), `(?m)^acceptanceRole:`)
		inv := e.act("update", map[string]any{"agent": "explorer", "config": m{"acceptanceRole": "observer"}})
		eq(t, inv.isError, true, "invalid")
		hasRe(t, inv.text, `config\.acceptanceRole must be 'read-only', 'writer', or false`)
	})
	tw(t, f, "creates agents with subagent-only extensions", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": m{"name": "child-tool-user", "description": "Uses child tools", "scope": "project", "subagentOnlyExtensions": "./tools/child-only.ts, /opt/pi/child.ts"}})
		eq(t, r.isError, false, "create")
		hasRe(t, e.read(t, filepath.Join(e.agentsDir(), "child-tool-user.md")), `(?m)^subagentOnlyExtensions: \./tools/child-only\.ts, /opt/pi/child\.ts$`)
		got := e.act("get", map[string]any{"agent": "child-tool-user"})
		eq(t, got.isError, false, "get")
		eq(t, strings.Contains(got.text, "Subagent-only extensions: "+filepath.Join(e.agentsDir(), "tools", "child-only.ts")+", /opt/pi/child.ts"), true, "resolved paths")
	})
	tw(t, f, "preserves relative extension paths during unrelated updates", func(t *testing.T) {
		e := newMgmtEnv(t)
		p := e.write(t, "portable.md", "---\nname: portable\ndescription: Portable agent\nextensions: ./tools/parent.ts, package-extension\nsubagentOnlyExtensions: ../child.ts, ~/shared.ts\n---\nOriginal prompt.\n")
		u := e.act("update", map[string]any{"agent": "portable", "config": m{"description": "Updated agent"}})
		eq(t, u.isError, false, "update")
		content := e.read(t, p)
		hasRe(t, content, `(?m)^description: Updated agent$`)
		hasRe(t, content, `(?m)^extensions: \./tools/parent\.ts, package-extension$`)
		hasRe(t, content, `(?m)^subagentOnlyExtensions: \.\./child\.ts, ~/shared\.ts$`)
		eq(t, strings.Contains(content, e.dir), false, "no absolute paths")
	})
	tw(t, f, "fails when extension frontmatter cannot be reread", func(t *testing.T) {
		e := newMgmtEnv(t)
		filePath := filepath.Join(e.agentsDir(), "removed.md")
		_, err := editableAgentConfig(AgentConfig{Name: "removed", Description: "Removed agent", SystemPromptMode: "replace", SystemPrompt: "Original prompt.", Source: SourceProject, FilePath: filePath,
			Extensions: []string{filepath.Join(e.agentsDir(), "tools", "parent.ts")}})
		// Go reports the missing file as "no such file or directory" where Node says ENOENT.
		eq(t, err != nil && os.IsNotExist(unwrapPathError(err)), true, "missing file")
	})
	tw(t, f, "returns a structured error when a definition disappears after discovery", func(t *testing.T) {
		e := newMgmtEnv(t)
		agentPath := e.write(t, "removed-during-update.md", "---\nname: removed-during-update\ndescription: Temporary agent\nextensions: ./tools/parent.ts\n---\nOriginal prompt.\n")
		ctx := e.ctx()
		calls := 0
		ctx.discoverAll = func(cwd, provider string) (DiscoveryAll, error) {
			calls++
			d, err := discoverAgentsAll(cwd, provider, DiscoveryOptions{})
			if calls == 1 {
				// the definition disappears between the discovery and the update's re-read
				os.Remove(agentPath)
			}
			return d, err
		}
		r := handleManagementAction("update", map[string]any{"agent": "removed-during-update", "config": m{"description": "Updated agent"}}, ctx)
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Could not reread agent definition .*removed-during-update\.md before updating 'removed-during-update':.*(ENOENT|no such file or directory)`)
		eq(t, fileExists2(agentPath), false, "file")
	})
	tw(t, f, "does not serialize settings overrides into custom agent frontmatter during updates", func(t *testing.T) {
		e := newMgmtEnv(t)
		ctx := e.ctx()
		ctx.available = func() []ModelInfo {
			return []ModelInfo{{Provider: "anthropic", ID: "claude-sonnet-4-6", FullID: "anthropic/claude-sonnet-4-6"}}
		}
		act := func(a string, p map[string]any) manageResult { return handleManagementAction(a, p, ctx) }
		wjc(t, filepath.Join(e.dir, "agent-home", "settings.json"), sub(m{"agentOverrides": m{"implementer": m{"output": "user.md", "defaultReads": l{"user.md"}, "model": "anthropic/claude-sonnet-4-6"}}}))
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), sub(m{"agentOverrides": m{"implementer": m{"output": "artifacts/implementer.md", "outputMode": "file-only", "defaultReads": l{"CONTEXT.md"},
			"model": "anthropic/claude-sonnet-4-6", "systemPromptMode": "append", "inheritProjectContext": true, "inheritSkills": true}}}))
		agentPath := e.write(t, "implementer.md", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		got := act("get", map[string]any{"agent": "implementer"})
		eq(t, got.isError, false, "get")
		for _, re := range []string{`Output: artifacts/implementer\.md`, `Output mode: file-only`, `Reads: CONTEXT\.md`, `Model: anthropic/claude-sonnet-4-6`, `System prompt mode: append`, `Inherit project context: true`, `Inherit skills: true`} {
			hasRe(t, got.text, re)
		}
		u := act("update", map[string]any{"agent": "implementer", "config": m{"description": "Updated implementer"}})
		eq(t, u.isError, false, "update")
		content := e.read(t, agentPath)
		hasRe(t, content, `(?m)^description: Updated implementer$`)
		for _, k := range []string{"output", "outputMode", "defaultReads", "model", "systemPromptMode", "inheritProjectContext", "inheritSkills"} {
			noRe(t, content, `(?m)^`+k+`:`)
		}
		after := act("get", map[string]any{"agent": "implementer"})
		eq(t, after.isError, false, "get after")
		for _, re := range []string{`Output: artifacts/implementer\.md`, `Output mode: file-only`, `Reads: CONTEXT\.md`, `Model: anthropic/claude-sonnet-4-6`, `System prompt mode: append`, `Inherit project context: true`, `Inherit skills: true`} {
			hasRe(t, after.text, re)
		}
	})
	tw(t, f, "does not serialize settings descriptions, fast, or defaults into custom agent frontmatter during updates", func(t *testing.T) {
		e := newMgmtEnv(t)
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), sub(m{"defaultModel": "openai/gpt-default", "defaultThinking": "high",
			"agentOverrides": m{"implementer": m{"description": "Settings description", "output": "settings.md", "fast": true}}}))
		agentPath := e.write(t, "implementer.md", "---\nname: implementer\ndescription: Frontmatter description\n---\n\nDrive the failing test first.\n")
		before := e.act("get", map[string]any{"agent": "implementer"}).text
		hasRe(t, before, `Description: Settings description`)
		hasRe(t, before, `Model: openai/gpt-default`)
		hasRe(t, before, `Thinking: high`)
		u := e.act("update", map[string]any{"agent": "implementer", "config": m{"output": "local.md"}})
		eq(t, u.isError, false, "update")
		content := e.read(t, agentPath)
		hasRe(t, content, `(?m)^description: Frontmatter description$`)
		hasRe(t, content, `(?m)^output: local\.md$`)
		noRe(t, content, `(?m)^fast:`)
		noRe(t, content, `(?m)^model:`)
		noRe(t, content, `(?m)^thinking:`)
	})
	tw(t, f, "preserves blank output and defaultReads frontmatter while settings overrides replace them", func(t *testing.T) {
		e := newMgmtEnv(t)
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), sub(m{"agentOverrides": m{"implementer": m{"output": "settings.md", "defaultReads": l{"settings.md"}}}}))
		agentPath := e.write(t, "implementer.md", "---\nname: implementer\ndescription: TDD implementer\noutput:\ndefaultReads:\n---\n\nDrive the failing test first.\n")
		u := e.act("update", map[string]any{"agent": "implementer", "config": m{"description": "Updated implementer"}})
		eq(t, u.isError, false, "update")
		content := e.read(t, agentPath)
		hasRe(t, content, `(?m)^output: ?$`)
		hasRe(t, content, `(?m)^defaultReads: ?$`)
		after := e.act("get", map[string]any{"agent": "implementer"}).text
		hasRe(t, after, `Output: settings\.md`)
		hasRe(t, after, `Reads: settings\.md`)
	})
	tw(t, f, "preserves explicit default-like frontmatter while settings overrides replace it", func(t *testing.T) {
		e := newMgmtEnv(t)
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), sub(m{"agentOverrides": m{"implementer": m{"thinking": "high", "tools": l{"bash"}, "skills": l{"override-skill"}, "defaultContext": "fork", "toolBudget": m{"hard": 3.0}}}}))
		agentPath := e.write(t, "implementer.md", "---\nname: implementer\ndescription: TDD implementer\nthinking: off\ntools:\nskills:\ndefaultContext:\ntoolBudget:\n---\n\nDrive the failing test first.\n")
		got := e.act("get", map[string]any{"agent": "implementer"})
		eq(t, got.isError, false, "get")
		hasRe(t, got.text, `Thinking: high`)
		noRe(t, got.text, `Thinking: off`)
		u := e.act("update", map[string]any{"agent": "implementer", "config": m{"description": "Updated implementer"}})
		eq(t, u.isError, false, "update")
		content := e.read(t, agentPath)
		hasRe(t, content, `(?m)^description: Updated implementer$`)
		hasRe(t, content, `(?m)^thinking: off$`)
		for _, k := range []string{"tools", "skills", "defaultContext", "toolBudget"} {
			hasRe(t, content, `(?m)^`+k+`: ?$`)
		}
		after := e.act("get", map[string]any{"agent": "implementer"})
		eq(t, after.isError, false, "get after")
		hasRe(t, after.text, `Thinking: high`)
		noRe(t, after.text, `Thinking: off`)
	})
	tw(t, f, "reports builtin runtime-loaded model mappings from current session state", func(t *testing.T) {
		e := newMgmtEnv(t)
		ctx := e.ctx()
		ctx.available = func() []ModelInfo {
			return []ModelInfo{{Provider: "openai", ID: "gpt-5-mini", FullID: "openai/gpt-5-mini"}, {Provider: "anthropic", ID: "claude-sonnet-4", FullID: "anthropic/claude-sonnet-4"}}
		}
		ctx.model = &ParentModel{"openai", "gpt-5-mini"}
		r := handleManagementAction("models", map[string]any{}, ctx)
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `(?m)^Subagent models`)
		hasRe(t, r.text, `Current session model:\n  openai/gpt-5-mini`)
		hasRe(t, r.text, `(?:^|\n)scout\n  model:\n    openai/gpt-5-mini\n  source: inherits current session model(?:\n|$)`)
		hasRe(t, r.text, `(?:^|\n)advisor\n  model:\n    openai/gpt-5-mini\n  source: inherits current session model(?:\n|$)`)
		noRe(t, r.text, `advisor\n  model:\n    \(builtin definition not found\)`)
		hasRe(t, r.text, `Available models in this session's registry`)
		hasRe(t, r.text, `  anthropic/claude-sonnet-4\n  openai/gpt-5-mini`)
		hasRe(t, r.text, `Use an exact provider/id from this list when you pass model`)
	})
	tw(t, f, "resolves the advisor builtin alias in a filtered model mapping", func(t *testing.T) {
		e := newMgmtEnv(t)
		ctx := e.ctx()
		ctx.available = func() []ModelInfo {
			return []ModelInfo{{Provider: "openai", ID: "gpt-5-mini", FullID: "openai/gpt-5-mini"}}
		}
		ctx.model = &ParentModel{"openai", "gpt-5-mini"}
		r := handleManagementAction("models", map[string]any{"agent": "advisor"}, ctx)
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `Agent: advisor`)
		hasRe(t, r.text, `Effective model:\n  openai/gpt-5-mini`)
		noRe(t, r.text, `Builtin agent 'advisor' not found|source: missing`)
	})
	tw(t, f, "reports effective model mappings for discovered package, user, and project agents", func(t *testing.T) {
		e := newMgmtEnv(t)
		userAgents := filepath.Join(e.dir, "agent-home", "agents")
		pkg := filepath.Join(e.dir, ".pi", "npm", "node_modules", "model-agents")
		wjc(t, filepath.Join(pkg, "package.json"), m{"name": "model-agents", "version": "1.2.3", "pi": m{"subagents": m{"agents": l{"agents"}}}})
		writeAgent := func(dir, name, model, thinking string) {
			writeFile(t, filepath.Join(dir, name+".md"), strings.Join([]string{"---", "name: " + name, "description: " + name + " model mapping", "model: " + model, "thinking: " + thinking, "---", "Model mapping test agent."}, "\n"))
		}
		writeAgent(filepath.Join(pkg, "agents"), "package-worker", "anthropic/claude-sonnet-4", "low")
		writeAgent(userAgents, "user-worker", "gpt-5-mini", "medium")
		writeAgent(userAgents, "shadowed", "openai/gpt-5-mini", "low")
		writeAgent(e.agentsDir(), "project-worker", "anthropic/claude-sonnet-4", "high")
		writeAgent(e.agentsDir(), "shadowed", "openai/gpt-5-mini", "high")
		e.write(t, "off-worker.md", strings.Join([]string{"---", "name: off-worker", "description: off-worker model mapping", "model: openai/gpt-5-mini", "thinking: false", "---", "Explicitly disable thinking."}, "\n"))
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), sub(m{"agentOverrides": m{"reviewer": m{"disabled": true}}}))
		ctx := e.ctx()
		ctx.available = func() []ModelInfo {
			return []ModelInfo{{Provider: "openai", ID: "gpt-5-mini", FullID: "openai/gpt-5-mini"}, {Provider: "anthropic", ID: "claude-sonnet-4", FullID: "anthropic/claude-sonnet-4"}}
		}
		ctx.model = &ParentModel{"openai", "gpt-5-mini"}
		r := handleManagementAction("models", map[string]any{}, ctx)
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `package-worker\n  model:\n    anthropic/claude-sonnet-4\n  source: package agent config\n  thinking: low`)
		hasRe(t, r.text, `user-worker\n  model:\n    openai/gpt-5-mini\n  source: user agent config\n  thinking: medium`)
		hasRe(t, r.text, `project-worker\n  model:\n    anthropic/claude-sonnet-4\n  source: project agent config\n  thinking: high`)
		hasRe(t, r.text, `off-worker\n  model:\n    openai/gpt-5-mini\n  source: project agent config\n  thinking: off`)
		hasRe(t, r.text, `shadowed\n  model:\n    openai/gpt-5-mini\n  source: project agent config`)
		noRe(t, r.text, `shadowed\n  model:\n    openai/gpt-5-mini\n  source: user agent config`)
		hasRe(t, r.text, `reviewer\n  model:\n    openai/gpt-5-mini\n  source: inherits current session model; disabled`)
		noRe(t, r.text, `(?i)api[_-]?key|token|secret`)
	})
	tw(t, f, "reports override source and disabled builtin state in runtime model mappings", func(t *testing.T) {
		e := newMgmtEnv(t)
		wjc(t, filepath.Join(e.dir, ".pi", "settings.json"), sub(m{"agentOverrides": m{"reviewer": m{"model": "claude-sonnet-4", "disabled": true}}}))
		ctx := e.ctx()
		ctx.available = func() []ModelInfo {
			return []ModelInfo{{Provider: "openai", ID: "gpt-5-mini", FullID: "openai/gpt-5-mini"}, {Provider: "anthropic", ID: "claude-sonnet-4", FullID: "anthropic/claude-sonnet-4"}}
		}
		ctx.model = &ParentModel{"openai", "gpt-5-mini"}
		r := handleManagementAction("models", map[string]any{"agent": "reviewer"}, ctx)
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `(?m)^Subagent model`)
		hasRe(t, r.text, `Agent: reviewer`)
		hasRe(t, r.text, `Effective model:\n  anthropic/claude-sonnet-4`)
		hasRe(t, r.text, `Source: project override`)
		hasRe(t, r.text, `Requested model setting:\n  claude-sonnet-4`)
		hasRe(t, r.text, `Disabled: true`)
		hasRe(t, strings.ReplaceAll(r.text, `\`, "/"), `Override file:\n  .*\.pi/settings\.json`)
	})
	tw(t, f, "rejects unknown agents for runtime model mappings", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("models", map[string]any{"agent": "not-a-builtin"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `Agent 'not-a-builtin' not found`)
	})
	tw(t, f, "creates delegate with its builtin prompt defaults", func(t *testing.T) {
		e := newMgmtEnv(t)
		r := e.act("create", map[string]any{"config": m{"name": "delegate", "description": "Delegate helper", "scope": "project"}})
		eq(t, r.isError, false, "error")
		content := e.read(t, filepath.Join(e.agentsDir(), "delegate.md"))
		hasRe(t, content, `systemPromptMode: append`)
		hasRe(t, content, `inheritProjectContext: true`)
		hasRe(t, content, `inheritSkills: false`)
	})
	tw(t, f, "lists proactive skill subagent suggestions from repeated configured skill use", func(t *testing.T) {
		e := newMgmtEnv(t)
		writeFile(t, filepath.Join(e.dir, ".pi", "skills", "deslop", "SKILL.md"), "---\ndescription: Cleanup review.\n---\n\nReview for cleanup.\n")
		for _, n := range []string{"cleanup-a", "cleanup-b"} {
			e.write(t, n+".md", "---\nname: "+n+"\ndescription: Cleanup "+n+"\nskills: deslop\n---\n\nInspect cleanup.\n")
		}
		r := e.act("list", map[string]any{})
		hasRe(t, r.text, `Proactive skill subagent suggestions:`)
		hasRe(t, r.text, `- deslop via reviewer`)
		hasRe(t, r.text, `Cleanup review\.`)
	})
	tw(t, f, "can disable proactive skill subagent suggestions in config", func(t *testing.T) {
		e := newMgmtEnv(t)
		ctx := e.ctx()
		ctx.proactive = false
		writeFile(t, filepath.Join(e.dir, ".pi", "skills", "deslop", "SKILL.md"), "Review for cleanup.\n")
		for _, n := range []string{"cleanup-a", "cleanup-b"} {
			e.write(t, n+".md", "---\nname: "+n+"\ndescription: Cleanup "+n+"\nskills: deslop\n---\n\nInspect cleanup.\n")
		}
		r := handleManagementAction("list", map[string]any{}, ctx)
		noRe(t, r.text, `Proactive skill subagent suggestions:`)
	})
}

var _ = regexp.MustCompile

func unwrapPathError(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe
	}
	return err
}

func parseJSONMust(t *testing.T, s string) any {
	t.Helper()
	v, msg := jsJSONParse(s)
	if msg != "" {
		t.Fatal(msg)
	}
	return v
}
