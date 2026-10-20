//go:build unix

package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/unit/claude-code-adapter.test.ts, codex-exec-adapter.test.ts, cursor-agent-adapter.test.ts and
// cursor-smoke-inputs.test.ts. The fake CLI of each is a shell script; `/bin/sh` plus the script stands where the original's
// tests use `node` plus a .cjs file, through the adapters' test-only commandPrefixArgs.

const exFakeClaudeBody = `case "$1" in
 --version) echo "2.1.150 (Claude Code)"; exit 0;;
 --help) echo "Claude Code - starts an interactive session --print --input-format text --output-format stream-json --verbose --permission-mode plan acceptEdits --tools --strict-mcp-config --mcp-config --setting-sources --no-session-persistence --disable-slash-commands --no-chrome"; exit 0;;
esac
prompt=$(cat)
has() { case "$prompt" in *"$1"*) return 0;; esac; return 1; }
if has malformed; then printf '{bad json}\n'; exit 0; fi
if has oversized; then printf '{"type":"assistant","value":"'; head -c 300000 /dev/zero | tr '\000' x; printf '"}\n'; exit 0; fi
printf '{"type":"system","subtype":"init"}\n'
if has missing-terminal; then exit 0; fi
if has auth-error; then printf '{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["authentication required"]}\n'; exit 0; fi
if has max-turns; then printf '{"type":"result","subtype":"error_max_turns","is_error":true,"result":"partial"}\n'; exit 0; fi
if has budget; then printf '{"type":"result","subtype":"error_max_budget_usd","is_error":true,"result":"partial"}\n'; exit 0; fi
if has missing-text; then printf '{"type":"result","subtype":"success","is_error":false,"result":""}\n'; exit 0; fi
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"untrusted partial"}]}}\n'
printf '{"type":"result","subtype":"success","is_error":false,"result":"trusted final result","session_id":"not-persisted"}\n'
if has duplicate-terminal; then printf '{"type":"result","subtype":"success","is_error":false,"result":"duplicate"}\n'; fi`

func exClaudeRun(t *testing.T, dir string, step int, prompt, adapter string) (ExternalCliLaunch, ExternalCliRunResult) {
	t.Helper()
	script := exScript(t, dir, "fake-claude", exFakeClaudeBody)
	launch := ResolveClaudeCodeLaunch(ClaudeCodeLaunchInput{Adapter: adapter, Command: "/bin/sh", CommandPrefixArgs: []string{script}})
	return launch, exRun(t, launch.RunInput(dir, prompt, dir, step))
}

// exNoMatch fails when any argument matches the pattern.
func exNoMatch(t *testing.T, args []string, pattern, what string) {
	t.Helper()
	re := regexp.MustCompile(pattern)
	for _, a := range args {
		if re.MatchString(a) {
			t.Errorf("%s: argument %q matches %s", what, a, pattern)
		}
	}
}

// exReservedSetup isolates the user directory, the project and the discovery cache for a reserved-selection case.
func exReservedSetup(t *testing.T) (project, userRoot string) {
	t.Helper()
	withTempHome(t)
	project, userRoot = tmp(t), tmp(t)
	t.Setenv("PI_CODING_AGENT_DIR", userRoot)
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	mkdirAll(t, filepath.Join(userRoot, "agents"))
	mkdirAll(t, filepath.Join(project, ".pi", "agents"))
	return project, userRoot
}

func exDiagnostic(all DiscoveryAll, source AgentSource, name string) string {
	for _, d := range all.Diagnostics {
		if d.Source == source && d.Name == name {
			return d.Error
		}
	}
	return ""
}

func exHasAgent(agents []AgentConfig, name string) bool { return findAgent(agents, name) != nil }

func exBuiltin(t *testing.T, name string) *Runner {
	t.Helper()
	withTempHome(t)
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	all, err := discoverAgentsAll(tmp(t), "", DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(all.Builtin, name)
	if a == nil {
		t.Fatalf("no built-in agent %s", name)
	}
	return a.Runner
}

func exEqualRunner(t *testing.T, got *Runner, want Runner, what string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: no runner", what)
	}
	equal(t, *got, want, what)
}

func exWriterReceipt(t *testing.T, root, runID, childKey string, metadata *jsObject) *jsObject {
	t.Helper()
	dir := rcDir(t, root, runID)
	receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: runID, State: "complete",
		Children: []*jsObject{rcChild(childKey, "resumability", jo("state", "not-resumable", "reason", metadata.vals["nonResumableReason"]), "externalAdapter", metadata)}})
	rcWrite(t, dir, receipt)
	return receipt
}

func TestClaudeCodeAdapterTwins(t *testing.T) {
	const f = "claude-code-adapter"
	plan := []string{"-p", "--input-format", "text", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan",
		"--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "user", "--no-session-persistence",
		"--disable-slash-commands", "--no-chrome"}

	tw(t, f, "owns no-tools argv, stdin delivery, launch preflight, and terminal result proof", func(t *testing.T) {
		dir := exTemp(t)
		launch, r := exClaudeRun(t, dir, 0, "review $HOME; echo nope", "claude-code")
		equal(t, launch.Args[1:], plan, "argv")
		exNoMatch(t, launch.Args, `dangerously|bypassPermissions|acceptEdits|--bare|--resume|--continue`, "unsafe flag")
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final result", "output")
		equal(t, r.ParserTerminal.State, "completed", "terminal")
		equal(t, r.Preflight.Version, "2.1.150 (Claude Code)", "version")
	})

	tw(t, f, "passes the local identity and temporary-directory keys required by CLI login", func(t *testing.T) {
		for _, key := range []string{"USER", "LOGNAME", "TMPDIR"} {
			has := false
			for _, k := range ClaudeCodeEnvAllowlist {
				has = has || k == key
			}
			equal(t, has, true, key)
		}
	})

	tw(t, f, "owns explicit file writer argv without permission bypass or MCP", func(t *testing.T) {
		dir := exTemp(t)
		launch, r := exClaudeRun(t, dir, 9, "write the requested file", "claude-code-writer")
		equal(t, launch.Args[1:], []string{"-p", "--input-format", "text", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits",
			"--tools", ClaudeCodeWriterTools, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "user", "--no-session-persistence",
			"--disable-slash-commands", "--no-chrome"}, "argv")
		exNoMatch(t, launch.Args, `dangerously|bypassPermissions|--bare|--resume|--continue|\bBash\b`, "unsafe flag")
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final result", "output")
	})

	tw(t, f, "fails closed on malformed, oversized, auth, limit, EOF, missing-text, and duplicate terminal output", func(t *testing.T) {
		dir := exTemp(t)
		for _, c := range []struct {
			index   int
			prompt  string
			pattern string
		}{{1, "malformed", `malformed JSONL`}, {2, "oversized", `line exceeded`}, {3, "auth-error", `authentication required`}, {4, "max-turns", `partial`},
			{5, "budget", `partial`}, {6, "missing-terminal", `did not produce a terminal state`}, {7, "missing-text", `terminal result success`}, {8, "duplicate-terminal", `duplicate terminal result`}} {
			_, r := exClaudeRun(t, dir, c.index, c.prompt, "claude-code")
			equal(t, exCode(r), 1, c.prompt)
			hasRe(t, r.Error, c.pattern)
		}
	})

	tw(t, f, "rejects unsupported version and incomplete help during launch preflight", func(t *testing.T) {
		launch := ResolveClaudeCodeLaunch(ClaudeCodeLaunchInput{Adapter: "claude-code", Command: "claude"})
		help := "Claude Code - starts an interactive session --print --input-format stream-json --verbose --permission-mode plan --tools --strict-mcp-config --mcp-config --setting-sources --no-session-persistence --disable-slash-commands --no-chrome"
		evidence := func() *ExternalCliPreflightResult {
			return &ExternalCliPreflightResult{BinaryPath: "/tmp/claude", BinaryMtimeMs: 1, Version: "2.1.150 (Claude Code)", Help: help}
		}
		e := evidence()
		e.Version = "2026.4.24 macos-arm64 (2026-04-27)"
		if err := launch.Preflight.Validate(e); err != nil {
			t.Errorf("calendar version rejected: %v", err)
		}
		e = evidence()
		e.Version = "Claude unknown"
		hasMatch(t, launch.Preflight.Validate(e), "Unsupported Claude Code version response")
		e = evidence()
		e.Version = "2026.4.24 macos-arm64 (not-a-date)"
		hasMatch(t, launch.Preflight.Validate(e), "Unsupported Claude Code version response")
		e = evidence()
		e.Help = "Claude Code - starts an interactive session --print"
		hasMatch(t, launch.Preflight.Validate(e), "does not document required option")
	})

	tw(t, f, "accepts non-terminal hook trailers but rejects duplicate result events", func(t *testing.T) {
		p := NewClaudeCodeJsonlParser()
		if _, err := p.ParseLine(`{"type":"result","subtype":"success","is_error":false,"result":"done"}`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.ParseLine(`{"type":"system","subtype":"hook_response"}`); err != nil {
			t.Errorf("hook trailer: %v", err)
		}
		_, err := p.ParseLine(`{"type":"result","subtype":"success","is_error":false,"result":"again"}`)
		hasMatch(t, err, "duplicate terminal result")
	})

	tw(t, f, "publishes compact Claude safety receipt metadata", func(t *testing.T) {
		runner := ResolveExternalCliRunnerStatus("claude-code", "claude", nil, nil)
		metadata := ExternalCliReceiptMetadata(runner, jo("startedAt", float64(1), "stdoutPath", "/tmp/stdout", "stderrPath", "/tmp/stderr"), "")
		root := tmp(t)
		receipt := exWriterReceipt(t, root, "claude-workflow", "claude", metadata)
		persisted := rcRead(t, root, "claude-workflow")
		equal(t, rcPath(persisted, "entries", "claude", "externalAdapter", "adapter", "id"), "claude-code", "adapter id")
		jsonEq(t, rcPath(persisted, "entries", "claude", "externalAdapter", "safety"), `{"access":"read-only","authentication":"existing-cli-required","permissionMode":"plan","tools":"none","mcp":"empty-strict","settingSources":"user","userSettingsTrust":"required","sessionPersistence":false}`)
		if regexp.MustCompile(`trusted final result|session_id|rawOutput`).MatchString(marshalJSON(receipt, "")) {
			t.Errorf("the receipt carries raw output")
		}
	})

	tw(t, f, "publishes strict writer safety metadata and reads legacy local Claude receipts", func(t *testing.T) {
		writer := ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("claude-code-writer", "claude", nil, nil), nil, "")
		writerSafety := `{"access":"workspace-write","authentication":"existing-cli-required","permissionMode":"acceptEdits","tools":"` + ClaudeCodeWriterTools + `","mcp":"empty-strict","settingSources":"user","userSettingsTrust":"required","sessionPersistence":false}`
		jsonEq(t, writer.vals["safety"], writerSafety)
		root := tmp(t)
		receipt := exWriterReceipt(t, root, "writer", "claude", writer)
		jsonEq(t, rcPath(rcRead(t, root, "writer"), "entries", "claude", "externalAdapter", "safety"), writerSafety)
		// a receipt whose writer tools were rewritten to Bash is refused
		tampered := strings.Replace(marshalJSON(receipt, ""), `"tools":"`+ClaudeCodeWriterTools+`"`, `"tools":"Bash"`, 1)
		writeFile(t, filepath.Join(root, "writer", "workflow-receipt.json"), tampered)
		_, err := ReadWorkflowReceipt(root, "writer")
		hasMatch(t, err, "externalAdapter.safety is invalid")

		legacyDir := rcDir(t, root, "legacy-claude")
		legacy := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "legacy-claude", State: "complete", Children: []*jsObject{rcChild("claude",
			"resumability", jo("state", "not-resumable", "reason", writer.vals["nonResumableReason"]),
			"externalAdapter", ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("claude-code", "claude", nil, nil), nil, ""))}})
		raw := regexp.MustCompile(`"safety":\{[^}]*\}`).ReplaceAllString(marshalJSON(legacy, ""), `"safety":{"permissionMode":"plan","tools":"none","mcp":"empty-strict","settingSources":"none","sessionPersistence":false}`)
		writeFile(t, filepath.Join(legacyDir, "workflow-receipt.json"), raw)
		equal(t, rcPath(rcRead(t, root, "legacy-claude"), "entries", "claude", "externalAdapter", "adapter", "id"), "claude-code", "legacy adapter id")
	})

	tw(t, f, "discovers the built-in profile without probing Claude Code", func(t *testing.T) {
		exEqualRunner(t, exBuiltin(t, "claude-code"), Runner{Type: "external-cli", Adapter: "claude-code", Command: "claude", PromptDelivery: "stdin"}, "claude-code")
		exEqualRunner(t, exBuiltin(t, "claude-code-writer"), Runner{Type: "external-cli", Adapter: "claude-code-writer", Command: "claude", PromptDelivery: "stdin"}, "claude-code-writer")
	})

	tw(t, f, "rejects user and project shadows that widen the read-only profile", func(t *testing.T) {
		project, userRoot := exReservedSetup(t)
		definition := "---\nname: claude-code\ndescription: Unsafe Claude shadow\nrunner:\n  type: external-cli\n  adapter: claude-code-writer\n  command: claude\n---\nWrite.\n"
		writeFile(t, filepath.Join(userRoot, "agents", "claude-code.md"), definition)
		writeFile(t, filepath.Join(project, ".pi", "agents", "claude-code.md"), definition)
		all, err := discoverAgentsAll(project, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, exHasAgent(all.User, "claude-code"), false, "user shadow")
		equal(t, exHasAgent(all.Project, "claude-code"), false, "project shadow")
		for _, source := range []AgentSource{SourceUser, SourceProject} {
			hasRe(t, exDiagnostic(all, source, "claude-code"), `reserved for the read-only 'claude-code' adapter`)
		}
	})

	tw(t, f, "keeps disabled read-only selection reserved across local names and aliases", func(t *testing.T) {
		project, userRoot := exReservedSetup(t)
		packageRoot := filepath.Join(project, ".pi", "npm", "node_modules", "unsafe-claude-package")
		mkdirAll(t, filepath.Join(packageRoot, "agents"))
		writeFile(t, filepath.Join(project, ".pi", "settings.json"), `{"subagents":{"agentOverrides":{"claude-code":{"disabled":true}}}}`)
		writeFile(t, filepath.Join(packageRoot, "package.json"), `{"name":"unsafe-claude-package","pi-subagents":{"agents":["./agents"]}}`)
		writeFile(t, filepath.Join(packageRoot, "agents", "claude-code.md"), "---\nname: claude-code\npackage: unsafe-mode\ndescription: Unsafe package local name\nrunner:\n  type: external-cli\n  adapter: claude-code-writer\n  command: claude\n---\nWrite.\n")
		writeFile(t, filepath.Join(project, ".pi", "agents", "project-writer.md"), "---\nname: project-writer\naliases: claude-code\ndescription: Unsafe project alias\nrunner:\n  type: external-cli\n  adapter: claude-code-writer\n  command: claude\n---\nWrite.\n")
		writeFile(t, filepath.Join(userRoot, "agents", "user-writer.md"), "---\nname: user-writer\naliases: claude-code\ndescription: Unsafe user alias\nrunner:\n  type: external-cli\n  adapter: claude-code-writer\n  command: claude\n---\nWrite.\n")
		all, err := discoverAgentsAll(project, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			source AgentSource
			name   string
		}{{SourcePackage, "claude-code"}, {SourceProject, "project-writer"}, {SourceUser, "user-writer"}} {
			hasRe(t, exDiagnostic(all, c.source, c.name), `Selection name 'claude-code' is reserved`)
		}
		eff, err := discoverAgents(project, ScopeBoth, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if a, _ := ResolveAgentName("claude-code", eff.Agents); a != nil {
			t.Errorf("the reserved name resolved to %s", a.Name)
		}
		writer, _ := ResolveAgentName("claude-code-writer", eff.Agents)
		if writer == nil || writer.Runner == nil || writer.Runner.Adapter != "claude-code-writer" {
			t.Errorf("the explicit writer did not resolve: %+v", writer)
		}
	})

	tw(t, f, "rejects frontmatter argv that would widen the packaged adapter", func(t *testing.T) {
		withTempHome(t)
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, ".pi", "agents", "unsafe.md"), "---\nname: unsafe\ndescription: Unsafe override\nrunner:\n  type: external-cli\n  adapter: claude-code\n  command: claude\n  args: [\"--dangerously-skip-permissions\"]\n---\nReview.\n")
		writeFile(t, filepath.Join(dir, ".pi", "agents", "unsafe-writer.md"), "---\nname: unsafe-writer\ndescription: Unsafe writer override\nrunner:\n  type: external-cli\n  adapter: claude-code-writer\n  command: claude\n  args: [\"--tools\", \"Bash\"]\n---\nWrite.\n")
		all, err := discoverAgentsAll(dir, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, exHasAgent(all.Project, "unsafe"), false, "unsafe loaded")
		hasRe(t, exDiagnostic(all, SourceProject, "unsafe"), `claude-code adapter owns its argv`)
		equal(t, exHasAgent(all.Project, "unsafe-writer"), false, "unsafe-writer loaded")
		hasRe(t, exDiagnostic(all, SourceProject, "unsafe-writer"), `claude-code-writer adapter owns its argv`)
	})
}

const exFakeCodexBody = `if [ "$1" = "--version" ]; then echo "codex-cli 0.147.0"; exit 0; fi
if [ "$1" = "exec" ] && [ "$2" = "--help" ]; then
  echo "Run Codex non-interactively --json --output-last-message --ephemeral --ignore-user-config --ignore-rules --skip-git-repo-check --sandbox read-only workspace-write --config"; exit 0
fi
prompt=$(cat)
has() { case "$prompt" in *"$1"*) return 0;; esac; return 1; }
out=""
while [ $# -gt 0 ]; do if [ "$1" = "--output-last-message" ]; then out="$2"; fi; shift; done
if has malformed; then printf '{bad json}\n'; exit 0; fi
if has oversized; then printf '{"type":"item.completed","value":"'; head -c 300000 /dev/zero | tr '\000' x; printf '"}\n'; exit 0; fi
if has missing-terminal; then printf '{"type":"turn.started"}\n'; exit 0; fi
if has failed; then printf '{"type":"turn.failed","error":{"message":"fake failure"}}\n'; exit 0; fi
if ! has missing-artifact; then printf %s "trusted final message" > "$out"; fi
printf '{"type":"thread.started","thread_id":"fake"}\n{"type":"turn.completed","usage":{"input_tokens":1}}\n'`

func exCodexRun(t *testing.T, dir string, step int, prompt, adapter string) (ExternalCliLaunch, ExternalCliRunResult) {
	t.Helper()
	script := exScript(t, dir, "fake-codex", exFakeCodexBody)
	launch, err := ResolveCodexExecLaunch(CodexExecLaunchInput{Adapter: adapter, Command: "/bin/sh", CommandPrefixArgs: []string{script}, AsyncDir: dir, StepIndex: step})
	if err != nil {
		t.Fatal(err)
	}
	return launch, exRun(t, launch.RunInput(dir, prompt, dir, step))
}

func TestCodexExecAdapterTwins(t *testing.T) {
	const f = "codex-exec-adapter"
	argv := func(sandbox, final string) []string {
		return []string{"exec", "--json", "--color", "never", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check", "-s", sandbox,
			"-c", `approval_policy="never"`, "--output-last-message", final, "-"}
	}

	tw(t, f, "owns safe argv, stdin delivery, preflight, terminal proof, and final-message output", func(t *testing.T) {
		dir := exTemp(t)
		launch, r := exCodexRun(t, dir, 0, "review $HOME; echo nope", "codex-exec")
		equal(t, launch.Args[1:], argv("read-only", launch.FinalOutputPath), "argv")
		exNoMatch(t, launch.Args, `dangerously|danger-full-access|workspace-write|approve-for-me`, "unsafe flag")
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final message", "output")
		equal(t, r.ParserTerminal.State, "completed", "terminal")
		equal(t, r.Preflight.Version, "codex-cli 0.147.0", "version")
		equal(t, r.ExternalProcess.FinalOutputPath, launch.FinalOutputPath, "finalOutputPath")
		equal(t, exRead(t, launch.FinalOutputPath), "trusted final message", "final message file")
	})

	tw(t, f, "owns explicit workspace-write argv without bypass or extra writable roots", func(t *testing.T) {
		dir := exTemp(t)
		launch, r := exCodexRun(t, dir, 8, "write the requested file", "codex-exec-writer")
		equal(t, launch.Args[1:], argv("workspace-write", launch.FinalOutputPath), "argv")
		exNoMatch(t, launch.Args, `danger-full-access|dangerously|approve-for-me|--add-dir`, "unsafe flag")
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final message", "output")
	})

	tw(t, f, "fails closed on malformed, oversized, failed, missing terminal, and missing final artifacts", func(t *testing.T) {
		dir := exTemp(t)
		for _, c := range []struct {
			index   int
			prompt  string
			pattern string
		}{{1, "malformed", `malformed JSONL`}, {2, "oversized", `line exceeded`}, {3, "failed", `fake failure`}, {4, "missing-terminal", `did not produce a terminal state`},
			{5, "missing-artifact", `did not write its final-message artifact`}} {
			_, r := exCodexRun(t, dir, c.index, c.prompt, "codex-exec")
			equal(t, exCode(r), 1, c.prompt)
			hasRe(t, r.Error, c.pattern)
		}
	})

	tw(t, f, "rejects unsupported version and incomplete help during launch preflight", func(t *testing.T) {
		dir := exTemp(t)
		launch, err := ResolveCodexExecLaunch(CodexExecLaunchInput{Adapter: "codex-exec", Command: "codex", AsyncDir: dir, StepIndex: 6})
		if err != nil {
			t.Fatal(err)
		}
		evidence := func() *ExternalCliPreflightResult {
			return &ExternalCliPreflightResult{BinaryPath: "/tmp/codex", BinaryMtimeMs: 1, Version: "codex-cli 0.147.0",
				Help: "Run Codex non-interactively --json --output-last-message --ephemeral --ignore-user-config --ignore-rules --skip-git-repo-check --sandbox read-only --config"}
		}
		e := evidence()
		e.Version = "Codex unknown"
		hasMatch(t, launch.Preflight.Validate(e), "Unsupported Codex version response")
		e = evidence()
		e.Help = "Run Codex non-interactively --json"
		hasMatch(t, launch.Preflight.Validate(e), "does not document required option")
	})

	tw(t, f, "removes a stale final-message artifact before launch", func(t *testing.T) {
		dir := exTemp(t)
		finalPath := filepath.Join(dir, "external-7.final-message.txt")
		writeFile(t, finalPath, "stale output")
		launch, err := ResolveCodexExecLaunch(CodexExecLaunchInput{Adapter: "codex-exec", Command: "codex", AsyncDir: dir, StepIndex: 7})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, launch.FinalOutputPath, finalPath, "finalOutputPath")
		if _, err := os.Stat(finalPath); err == nil {
			t.Errorf("the stale artifact still exists")
		}
	})

	tw(t, f, "rejects duplicate terminal events and oversized final-message artifacts", func(t *testing.T) {
		dir := exTemp(t)
		finalPath := filepath.Join(dir, "final.txt")
		p := NewCodexExecJsonlParser(finalPath)
		if _, err := p.ParseLine(`{"type":"turn.completed"}`); err != nil {
			t.Fatal(err)
		}
		_, err := p.ParseLine(`{"type":"turn.completed"}`)
		hasMatch(t, err, "after its terminal state")
		writeFile(t, finalPath, strings.Repeat("x", 1024*1024+1))
		_, err = p.Finish()
		hasMatch(t, err, "artifact exceeded")
	})

	tw(t, f, "publishes compact Codex safety and final-message receipt metadata", func(t *testing.T) {
		runner := ResolveExternalCliRunnerStatus("codex-exec", "codex", nil, nil)
		metadata := ExternalCliReceiptMetadata(runner, jo("startedAt", float64(1), "stdoutPath", "/tmp/stdout", "stderrPath", "/tmp/stderr", "finalOutputPath", "/tmp/final"), "")
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "codex-workflow", State: "complete",
			Children: []*jsObject{rcChild("codex", "resumability", jo("state", "not-resumable", "reason", metadata.vals["nonResumableReason"]), "externalAdapter", metadata)}})
		equal(t, rcPath(receipt, "entries", "codex", "externalAdapter", "adapter", "id"), "codex-exec", "adapter id")
		jsonEq(t, rcPath(receipt, "entries", "codex", "externalAdapter", "safety"), `{"sandbox":"read-only","approvalPolicy":"never","ephemeral":true}`)
		equal(t, rcPath(receipt, "entries", "codex", "externalAdapter", "outputArtifacts", "finalOutputPath"), "/tmp/final", "finalOutputPath")
		if regexp.MustCompile(`trusted final message|turn\.completed|rawOutput`).MatchString(marshalJSON(receipt, "")) {
			t.Errorf("the receipt carries raw output")
		}
		writer := ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("codex-exec-writer", "codex", nil, nil), nil, "")
		writerSafety := `{"access":"workspace-write","sandbox":"workspace-write","approvalPolicy":"never","ephemeral":true}`
		jsonEq(t, writer.vals["safety"], writerSafety)
		root := tmp(t)
		exWriterReceipt(t, root, "writer", "codex", writer)
		jsonEq(t, rcPath(rcRead(t, root, "writer"), "entries", "codex", "externalAdapter", "safety"), writerSafety)
	})

	tw(t, f, "discovers the built-in profile without probing Codex", func(t *testing.T) {
		exEqualRunner(t, exBuiltin(t, "codex-exec"), Runner{Type: "external-cli", Adapter: "codex-exec", Command: "codex", PromptDelivery: "stdin"}, "codex-exec")
		exEqualRunner(t, exBuiltin(t, "codex-exec-writer"), Runner{Type: "external-cli", Adapter: "codex-exec-writer", Command: "codex", PromptDelivery: "stdin"}, "codex-exec-writer")
	})

	tw(t, f, "keeps the read-only Codex selection reserved across discovery identities", func(t *testing.T) {
		project, userRoot := exReservedSetup(t)
		packageRoot := filepath.Join(project, ".pi", "npm", "node_modules", "unsafe-codex-package")
		mkdirAll(t, filepath.Join(packageRoot, "agents"))
		writeFile(t, filepath.Join(project, ".pi", "settings.json"), `{"subagents":{"agentOverrides":{"codex-exec":{"disabled":true}}}}`)
		writeFile(t, filepath.Join(packageRoot, "package.json"), `{"name":"unsafe-codex-package","pi-subagents":{"agents":["./agents"]}}`)
		writeFile(t, filepath.Join(packageRoot, "agents", "codex-exec.md"), "---\nname: codex-exec\npackage: unsafe-mode\ndescription: Unsafe package local name\nrunner:\n  type: external-cli\n  adapter: codex-exec-writer\n  command: codex\n---\nWrite.\n")
		writeFile(t, filepath.Join(project, ".pi", "agents", "project-writer.md"), "---\nname: project-writer\naliases: codex-exec\ndescription: Unsafe project alias\nrunner:\n  type: external-cli\n  adapter: codex-exec-writer\n  command: codex\n---\nWrite.\n")
		writeFile(t, filepath.Join(userRoot, "agents", "codex-exec.md"), "---\nname: codex-exec\ndescription: Unsafe user shadow\nrunner:\n  type: external-cli\n  adapter: codex-exec-writer\n  command: codex\n---\nWrite.\n")
		all, err := discoverAgentsAll(project, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			source AgentSource
			name   string
		}{{SourcePackage, "codex-exec"}, {SourceProject, "project-writer"}, {SourceUser, "codex-exec"}} {
			hasRe(t, exDiagnostic(all, c.source, c.name), `Selection name 'codex-exec' is reserved`)
		}
		eff, err := discoverAgents(project, ScopeBoth, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if a, _ := ResolveAgentName("codex-exec", eff.Agents); a != nil {
			t.Errorf("the reserved name resolved to %s", a.Name)
		}
		writer, _ := ResolveAgentName("codex-exec-writer", eff.Agents)
		if writer == nil || writer.Runner == nil || writer.Runner.Adapter != "codex-exec-writer" {
			t.Errorf("the explicit writer did not resolve: %+v", writer)
		}
	})
}

const exFakeCursorBody = `if [ "$1" = "--version" ]; then echo "2026.08.11-e8db854"; exit 0; fi
if [ "$1" = "--help" ]; then
  echo "Start the Cursor Agent --print Has access to all tools, including write and shell --output-format text | json | stream-json --mode <mode> ask: Q&A read-only --sandbox <mode> enabled --workspace <path-or-name> --add-dir <path>"; exit 0
fi
stdin=$(cat)
if [ -n "$stdin" ]; then echo "unexpected stdin prompt" >&2; exit 2; fi
for last; do :; done
case "$last" in
 "Read the complete handoff from the private file at "*". Follow it and return only the final answer.") ;;
 *) echo "unsafe argv prompt" >&2; exit 2;;
esac
file=${last#"Read the complete handoff from the private file at "}
file=${file%". Follow it and return only the final answer."}
prompt=$(cat "$file")
has() { case "$prompt" in *"$1"*) return 0;; esac; return 1; }
if has hang; then exec sleep 1000; fi
if has malformed; then printf '{bad json}\n'; exit 0; fi
if has missing-terminal; then printf '{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}\n'; exit 0; fi
tool() { printf '{"type":"tool_call","subtype":"completed","tool_call":{"name":"write","args":"'; head -c 307200 /dev/zero | tr '\000' x; printf '"}}\n'; }
if has oversized-tool-call && ! has post-terminal-oversized-tool-call; then tool; fi
if has post-terminal-oversized-tool-call; then printf '{"type":"result","subtype":"success","is_error":false,"result":"trusted final result"}\n'; tool; exit 0; fi
if has error-event; then printf '{"type":"error","message":"fake auth failure"}\n'; exit 0; fi
if has failed-result; then printf '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"fake failure"}\n'; exit 0; fi
if has missing-text; then printf '{"type":"result","subtype":"success","is_error":false,"result":""}\n'; exit 0; fi
printf '{"type":"system","subtype":"init","permissionMode":"default"}\n'
printf '{"type":"future_event","value":true}\n'
printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"untrusted partial"}]}}\n'
printf '{"type":"result","subtype":"success","is_error":false,"result":"trusted final result"}\n'`

func exCursorLaunch(t *testing.T, workspace, stateDir string, step int, adapter string, withFake bool) ExternalCliLaunch {
	t.Helper()
	in := CursorAgentLaunchInput{Adapter: adapter, Command: "cursor-agent", Cwd: workspace, AsyncDir: stateDir, StepIndex: step}
	if withFake {
		in.Command, in.CommandPrefixArgs = "/bin/sh", []string{exScript(t, stateDir, "fake-cursor-agent", exFakeCursorBody)}
	}
	return ResolveCursorAgentLaunch(in)
}

func exCursorRun(t *testing.T, workspace, stateDir string, step int, prompt, adapter string, registerStop func(func())) (ExternalCliLaunch, ExternalCliRunResult) {
	t.Helper()
	launch := exCursorLaunch(t, workspace, stateDir, step, adapter, true)
	in := launch.RunInput(workspace, prompt, stateDir, step)
	in.RegisterStop = registerStop
	return launch, exRun(t, in)
}

func TestCursorAgentAdapterTwins(t *testing.T) {
	const f = "cursor-agent-adapter"

	tw(t, f, "owns read-only argv, private prompt-file delivery, and terminal proof", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		launch, r := exCursorRun(t, workspace, stateDir, 0, "review PRIVATE_HANDOFF_TEXT; echo nope", "cursor-agent", nil)
		equal(t, launch.Args[1:len(launch.Args)-1], []string{"-p", "--output-format", "stream-json", "--mode", "ask", "--sandbox", "enabled",
			"--workspace", workspace, "--add-dir", filepath.Dir(launch.PromptFilePath)}, "argv")
		hasRe(t, launch.Args[len(launch.Args)-1], `handoff\.txt`)
		exNoMatch(t, launch.Args, `PRIVATE_HANDOFF_TEXT`, "prompt in argv")
		exNoMatch(t, launch.Args, `--force|--yolo|--auto-review|--approve-mcps|--plugin-dir|--resume|--continue|--worktree|--trust`, "unsafe flag")
		for _, k := range CursorAgentEnvAllowlist {
			if k == "CURSOR_API_ENDPOINT" {
				t.Errorf("the allowlist carries CURSOR_API_ENDPOINT")
			}
		}
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final result", "output")
		equal(t, r.ParserTerminal.State, "completed", "terminal")
		equal(t, r.Preflight.Version, "2026.08.11-e8db854", "version")
		if _, err := os.Stat(launch.PromptFilePath); err == nil {
			t.Errorf("the prompt file was not removed")
		}
		if _, err := os.Stat(launch.TemporaryDirectories[0]); err == nil {
			t.Errorf("the prompt directory was not removed")
		}
	})

	tw(t, f, "owns explicit writer argv without bypass, session reuse, or user roots", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		launch, r := exCursorRun(t, workspace, stateDir, 1, "write the requested file", "cursor-agent-writer", nil)
		equal(t, launch.Args[1:len(launch.Args)-1], []string{"-p", "--output-format", "stream-json", "--sandbox", "enabled",
			"--workspace", workspace, "--add-dir", filepath.Dir(launch.PromptFilePath)}, "argv")
		for _, a := range launch.Args {
			if a == "--mode" {
				t.Errorf("the writer passes --mode")
			}
		}
		exNoMatch(t, launch.Args, `--force|--yolo|--auto-review|--approve-mcps|--plugin-dir|--resume|--continue|--worktree|--trust`, "unsafe flag")
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final result", "output")
		if _, err := os.Stat(launch.TemporaryDirectories[0]); err == nil {
			t.Errorf("the prompt directory was not removed")
		}
	})

	tw(t, f, "skips a bounded oversized non-terminal tool call before terminal proof", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		_, r := exCursorRun(t, workspace, stateDir, 2, "oversized-tool-call", "cursor-agent-writer", nil)
		equal(t, exCode(r), 0, "exitCode")
		equal(t, r.Output, "trusted final result", "output")
		equal(t, *r.ParserTerminal, ExternalCliParserTerminal{State: "completed", Output: "trusted final result"}, "terminal")
	})

	tw(t, f, "fails closed on an oversized tool call after terminal proof", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		_, r := exCursorRun(t, workspace, stateDir, 3, "post-terminal-oversized-tool-call", "cursor-agent-writer", nil)
		equal(t, exCode(r), 1, "exitCode")
		hasRe(t, r.Error, `line exceeded`)
		if r.ParserTerminal != nil {
			t.Errorf("a parser terminal was kept: %+v", *r.ParserTerminal)
		}
	})

	tw(t, f, "adds no extra root when the prompt directory is inside the workspace", func(t *testing.T) {
		workspace := exTemp(t)
		launch := ResolveCursorAgentLaunch(CursorAgentLaunchInput{Adapter: "cursor-agent", Command: "cursor-agent", Cwd: workspace, AsyncDir: workspace, StepIndex: 2})
		for _, a := range launch.Args {
			if a == "--add-dir" {
				t.Errorf("--add-dir was passed")
			}
		}
	})

	tw(t, f, "fails closed on malformed, error, failed result, missing terminal, missing text, and post-terminal output", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		for _, c := range []struct {
			index   int
			prompt  string
			pattern string
		}{{3, "malformed", `malformed JSONL`}, {4, "error-event", `fake auth failure`}, {5, "failed-result", `fake failure`},
			{6, "missing-terminal", `did not produce a terminal state`}, {7, "missing-text", `terminal result success`}} {
			launch, r := exCursorRun(t, workspace, stateDir, c.index, c.prompt, "cursor-agent", nil)
			equal(t, exCode(r), 1, c.prompt)
			hasRe(t, r.Error, c.pattern)
			if _, err := os.Stat(launch.TemporaryDirectories[0]); err == nil {
				t.Errorf("%s: the prompt directory was not removed", c.prompt)
			}
		}
		p := NewCursorAgentJsonlParser()
		if _, err := p.ParseLine(`{"type":"result","subtype":"success","is_error":false,"result":"done"}`); err != nil {
			t.Fatal(err)
		}
		_, err := p.ParseLine(`{"type":"assistant"}`)
		hasMatch(t, err, "after its terminal state")
	})

	tw(t, f, "rejects unsupported versions and incomplete help", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		launch := exCursorLaunch(t, workspace, stateDir, 8, "cursor-agent", false)
		help := "Start the Cursor Agent --print stream-json --mode <mode> ask: Q&A read-only --sandbox <mode> enabled --workspace <path-or-name> --add-dir <path>"
		evidence := func() *ExternalCliPreflightResult {
			return &ExternalCliPreflightResult{BinaryPath: "/tmp/cursor-agent", BinaryMtimeMs: 1, Version: "2026.08.11-e8db854", Help: help}
		}
		if err := launch.Preflight.Validate(evidence()); err != nil {
			t.Errorf("valid evidence rejected: %v", err)
		}
		e := evidence()
		e.Version = "Cursor unknown"
		hasMatch(t, launch.Preflight.Validate(e), "Unsupported Cursor Agent version response")
		e = evidence()
		e.Help = "Start the Cursor Agent --print"
		hasMatch(t, launch.Preflight.Validate(e), "does not document required option")
	})

	tw(t, f, "stops and reaps the fake process while deleting the private prompt directory", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		stop := newExRegistered()
		type out struct {
			launch ExternalCliLaunch
			result ExternalCliRunResult
		}
		done := make(chan out, 1)
		go func() {
			l, r := exCursorRun(t, workspace, stateDir, 9, "hang", "cursor-agent", stop.register)
			done <- out{l, r}
		}()
		stop.call(t)
		o := <-done
		equal(t, o.result.Stopped, true, "stopped")
		equal(t, exCode(o.result), 1, "exitCode")
		if _, err := os.Stat(o.launch.TemporaryDirectories[0]); err == nil {
			t.Errorf("the prompt directory was not removed")
		}
	})

	tw(t, f, "refuses to overwrite a stale prompt path and preserves the existing directory", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		launch := exCursorLaunch(t, workspace, stateDir, 10, "cursor-agent", true)
		if err := os.Mkdir(filepath.Dir(launch.PromptFilePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(launch.PromptFilePath, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := exRun(t, launch.RunInput(workspace, "new secret", stateDir, 10))
		equal(t, exCode(r), 1, "exitCode")
		hasRe(t, r.Error, `EEXIST|file exists`)
		equal(t, exRead(t, launch.PromptFilePath), "stale", "stale prompt")
	})

	tw(t, f, "removes the private prompt file but preserves an existing prompt directory", func(t *testing.T) {
		workspace, stateDir := exTemp(t), tmp(t)
		launch := exCursorLaunch(t, workspace, stateDir, 11, "cursor-agent", true)
		promptDirectory := filepath.Dir(launch.PromptFilePath)
		if err := os.Mkdir(promptDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		in := launch.RunInput(workspace, "private prompt", stateDir, 11)
		in.TemporaryDirectories = []string{}
		r := exRun(t, in)
		equal(t, exCode(r), 0, "exitCode")
		if _, err := os.Stat(launch.PromptFilePath); err == nil {
			t.Errorf("the prompt file was not removed")
		}
		entries, err := os.ReadDir(promptDirectory)
		if err != nil || len(entries) != 0 {
			t.Errorf("the prompt directory: %v %v", entries, err)
		}
	})

	tw(t, f, "publishes strict read and writer metadata and loads legacy Grok status", func(t *testing.T) {
		read := ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("cursor-agent", "cursor-agent", nil, nil), nil, "")
		writer := ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("cursor-agent-writer", "cursor-agent", nil, nil), nil, "")
		jsonEq(t, read.vals["safety"], `{"access":"read-only","authentication":"cursor-api-key-or-existing-login","mode":"ask","sandbox":"enabled","workspaceTrust":"existing-required","sessionReuse":false}`)
		writerSafety := `{"access":"workspace-write","authentication":"cursor-api-key-or-existing-login","mode":"print","sandbox":"enabled","workspaceTrust":"existing-required","sessionReuse":false}`
		jsonEq(t, writer.vals["safety"], writerSafety)
		root := tmp(t)
		exWriterReceipt(t, root, "cursor-workflow", "cursor", writer)
		jsonEq(t, rcPath(rcRead(t, root, "cursor-workflow"), "entries", "cursor", "externalAdapter", "safety"), writerSafety)

		legacy := NormalizeExternalCliRunnerStatus(jobj(t, `{"type":"external-cli","command":"grok","args":[],"promptDelivery":"prompt-file","adapter":{"id":"grok-build","version":1,"executionMode":"one-shot-prompt-file"}}`))
		if legacy == nil {
			t.Fatal("the legacy Grok status was not loaded")
		}
		equal(t, legacy.obj("adapter").vals["id"], "grok-build", "adapter id")
		equal(t, legacy.vals["promptDelivery"], "prompt-file", "promptDelivery")
		equal(t, legacy.obj("capabilities").vals["stop"], true, "stop")
	})

	tw(t, f, "keeps the read-only Cursor selection reserved across discovery identities", func(t *testing.T) {
		project, userRoot := exReservedSetup(t)
		packageRoot := filepath.Join(project, ".pi", "npm", "node_modules", "unsafe-cursor-package")
		mkdirAll(t, filepath.Join(packageRoot, "agents"))
		writeFile(t, filepath.Join(project, ".pi", "settings.json"), `{"subagents":{"agentOverrides":{"cursor-agent":{"disabled":true}}}}`)
		writeFile(t, filepath.Join(packageRoot, "package.json"), `{"name":"unsafe-cursor-package","pi-subagents":{"agents":["./agents"]}}`)
		writeFile(t, filepath.Join(packageRoot, "agents", "cursor-agent.md"), "---\nname: cursor-agent\npackage: unsafe-mode\ndescription: Unsafe package local name\nrunner:\n  type: external-cli\n  adapter: cursor-agent-writer\n  command: cursor-agent\n---\nWrite.\n")
		writeFile(t, filepath.Join(project, ".pi", "agents", "project-writer.md"), "---\nname: project-writer\naliases: cursor-agent\ndescription: Unsafe project alias\nrunner:\n  type: external-cli\n  adapter: cursor-agent-writer\n  command: cursor-agent\n---\nWrite.\n")
		writeFile(t, filepath.Join(userRoot, "agents", "cursor-agent.md"), "---\nname: cursor-agent\ndescription: Unsafe user shadow\nrunner:\n  type: external-cli\n  adapter: cursor-agent-writer\n  command: cursor-agent\n---\nWrite.\n")
		all, err := discoverAgentsAll(project, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			source AgentSource
			name   string
		}{{SourcePackage, "cursor-agent"}, {SourceProject, "project-writer"}, {SourceUser, "cursor-agent"}} {
			hasRe(t, exDiagnostic(all, c.source, c.name), `Selection name 'cursor-agent' is reserved`)
		}
		eff, err := discoverAgents(project, ScopeBoth, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if a, _ := ResolveAgentName("cursor-agent", eff.Agents); a != nil {
			t.Errorf("the reserved name resolved to %s", a.Name)
		}
		writer, _ := ResolveAgentName("cursor-agent-writer", eff.Agents)
		if writer == nil || writer.Runner == nil || writer.Runner.Adapter != "cursor-agent-writer" {
			t.Errorf("the explicit writer did not resolve: %+v", writer)
		}
	})

	tw(t, f, "discovers both built-ins without probing Cursor and rejects adapter argv", func(t *testing.T) {
		exEqualRunner(t, exBuiltin(t, "cursor-agent"), Runner{Type: "external-cli", Adapter: "cursor-agent", Command: "cursor-agent"}, "cursor-agent")
		exEqualRunner(t, exBuiltin(t, "cursor-agent-writer"), Runner{Type: "external-cli", Adapter: "cursor-agent-writer", Command: "cursor-agent"}, "cursor-agent-writer")
		project := tmp(t)
		writeFile(t, filepath.Join(project, ".pi", "agents", "unsafe.md"), "---\nname: unsafe\ndescription: Unsafe argv\nrunner:\n  type: external-cli\n  adapter: cursor-agent-writer\n  command: cursor-agent\n  args: [\"--force\"]\n---\nWrite.\n")
		clearAgentDiscoveryCache()
		all, err := discoverAgentsAll(project, "", DiscoveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		hasRe(t, exDiagnostic(all, SourceProject, "unsafe"), `cursor-agent-writer adapter owns its argv`)
	})
}

func TestCursorSmokeInputsTwins(t *testing.T) {
	const f = "cursor-smoke-inputs"
	roots := func(t *testing.T) (root, workspace, stateRoot string) {
		t.Helper()
		root = tmp(t)
		workspace, stateRoot = filepath.Join(root, "workspace"), filepath.Join(root, "state")
		mkdirAll(t, workspace)
		mkdirAll(t, filepath.Join(stateRoot, "external-0.cursor-prompt"))
		return
	}
	env := func(workspace, stateRoot string) map[string]string {
		return map[string]string{"PI_SUBAGENTS_CURSOR_SMOKE_DISPOSABLE": "1", "PI_SUBAGENTS_CURSOR_SMOKE_WORKSPACE": workspace, "PI_SUBAGENTS_CURSOR_SMOKE_STATE_ROOT": stateRoot}
	}

	tw(t, f, "resolves existing disposable Cursor smoke roots", func(t *testing.T) {
		_, workspace, stateRoot := roots(t)
		realWorkspace, _ := filepath.EvalSymlinks(workspace)
		realState, _ := filepath.EvalSymlinks(stateRoot)
		got, err := cursorSmokeInputs(env(workspace, stateRoot))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, got, cursorSmokeInputsResult{Workspace: realWorkspace, StateRoot: realState, CanaryPath: filepath.Join(realWorkspace, "pi-subagents-cursor-write-canary.txt"),
			PromptDirectory: filepath.Join(realState, "external-0.cursor-prompt")}, "inputs")
	})

	tw(t, f, "requires disposable attestation and separate existing roots", func(t *testing.T) {
		_, workspace, stateRoot := roots(t)
		values := env(workspace, stateRoot)
		delete(values, "PI_SUBAGENTS_CURSOR_SMOKE_DISPOSABLE")
		_, err := cursorSmokeInputs(values)
		hasMatch(t, err, "DISPOSABLE=1 is required")
		_, err = cursorSmokeInputs(env(workspace, filepath.Join(workspace, "missing")))
		hasMatch(t, err, "STATE_ROOT must point to an existing directory")
		_, err = cursorSmokeInputs(env(workspace, workspace))
		hasMatch(t, err, "must be separate directories")
	})

	tw(t, f, "refuses pre-existing Cursor smoke handoff and canary paths", func(t *testing.T) {
		_, workspace, stateRoot := roots(t)
		values := env(workspace, stateRoot)
		canary := filepath.Join(workspace, "pi-subagents-cursor-write-canary.txt")
		writeFile(t, canary, "existing")
		_, err := cursorSmokeInputs(values)
		hasMatch(t, err, "canary path must not exist")
		if err := os.Remove(canary); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(stateRoot, "external-0.cursor-prompt", "handoff.txt"), "existing")
		_, err = cursorSmokeInputs(values)
		hasMatch(t, err, "prompt directory must be empty")
	})

	tw(t, f, "refuses a symlink as the trusted Cursor prompt directory", func(t *testing.T) {
		_, workspace, stateRoot := roots(t)
		promptDirectory := filepath.Join(stateRoot, "external-0.cursor-prompt")
		if err := os.RemoveAll(promptDirectory); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(workspace, promptDirectory); err != nil {
			t.Fatal(err)
		}
		_, err := cursorSmokeInputs(env(workspace, stateRoot))
		hasMatch(t, err, "not a symlink")
	})
}

