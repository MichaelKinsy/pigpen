package plugins

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

func testProfilePaths(t *testing.T) paths.ProfilePaths {
	t.Helper()
	home := t.TempDir()
	return paths.ProfilePaths{
		RootHome:    home,
		ProfileName: "default",
		Home:        home,
		ConfigFile:  filepath.Join(home, "config.yaml"),
		EnvFile:     filepath.Join(home, ".env"),
		MemoriesDir: filepath.Join(home, "memories"),
		SkillsDir:   filepath.Join(home, "skills"),
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestParsePluginYAMLAndPortableJSON(t *testing.T) {
	tmp := t.TempDir()

	// 1. Hermes plugin.yaml
	yamlDir := filepath.Join(tmp, "yaml-plug")
	yamlFile := filepath.Join(yamlDir, "plugin.yaml")
	writeFile(t, yamlFile, `
name: my-hermes-plugin
version: "1.2.3"
description: "Sample Hermes plugin"
kind: standalone
entry: custom_entry.py
provides_tools:
  - alpha_tool
  - beta_tool
hooks: [pre_tool_call, post_tool_call]
commands:
  - mycmd
platforms: [darwin, linux]
provides_memory_provider: false
settings:
  greeting: "hello"
  retries: 3
`)
	m, err := ParseManifestFile(yamlFile, yamlDir, SourceUser, "")
	if err != nil {
		t.Fatalf("ParseManifestFile: %v", err)
	}
	if m.CanonicalKey() != "my-hermes-plugin" || m.Version != "1.2.3" || m.Entry != "custom_entry.py" {
		t.Fatalf("unexpected manifest core fields: %+v", m)
	}
	if len(m.Tools) != 2 || m.Tools[0] != "alpha_tool" || m.Tools[1] != "beta_tool" {
		t.Fatalf("unexpected tools: %+v", m.Tools)
	}
	if len(m.Hooks) != 2 || m.Hooks[0] != "pre_tool_call" {
		t.Fatalf("unexpected hooks: %+v", m.Hooks)
	}
	if len(m.Commands) != 1 || m.Commands[0] != "mycmd" {
		t.Fatalf("unexpected commands: %+v", m.Commands)
	}
	if len(m.Platforms) != 2 || m.Platforms[0] != "darwin" {
		t.Fatalf("unexpected platforms: %+v", m.Platforms)
	}
	if m.Settings["greeting"] != "hello" || m.Settings["retries"] != 3 {
		t.Fatalf("unexpected settings: %+v", m.Settings)
	}

	// 2. Portable Agent Plugin plugin.json + mcp.json + skills/
	portableDir := filepath.Join(tmp, "portable-pack")
	writeFile(t, filepath.Join(portableDir, "plugin.json"), `{
  "name": "portable-pack",
  "version": "0.5.0",
  "description": "Portable MCP + skill pack",
  "author": {"name": "Nous"},
  "keywords": ["mcp", "skills"],
  "extensions": {
    "com.nousresearch.hermes": {
      "servers": {
        "demo-srv": {"trust": "untrusted"}
      }
    }
  }
}`)
	writeFile(t, filepath.Join(portableDir, "mcp.json"), `{
  "mcpServers": {
    "demo-srv": {
      "command": "${PLUGIN_ROOT}/bin/server",
      "args": ["--data", "${PLUGIN_DATA}/db"],
      "env": {"ROOT": "${PLUGIN_ROOT}"}
    }
  }
}`)
	writeFile(t, filepath.Join(portableDir, "skills", "greet", "SKILL.md"), `---
name: greet
description: Greeting workflow skill
---
# Greet
Say hello.
`)
	hermesHome := t.TempDir()
	pm, err := ParsePortableManifest(portableDir, SourceUser, "", hermesHome)
	if err != nil {
		t.Fatalf("ParsePortableManifest: %v", err)
	}
	if !pm.Portable || pm.CanonicalKey() != "portable-pack" || pm.Author != "Nous" {
		t.Fatalf("unexpected portable manifest: %+v", pm)
	}
	srv, ok := pm.MCPServers["demo-srv"]
	if !ok {
		t.Fatalf("expected demo-srv in MCPServers, got %+v", pm.MCPServers)
	}
	if srv.Command != filepath.Join(portableDir, "bin/server") {
		t.Fatalf("expected expanded PLUGIN_ROOT command, got %q", srv.Command)
	}
	if srv.Trust != "untrusted" {
		t.Fatalf("expected trust=untrusted, got %q", srv.Trust)
	}
	if len(srv.Args) != 2 || !strings.Contains(srv.Args[1], "agent-plugin-portable-pack-") {
		t.Fatalf("expected expanded PLUGIN_DATA arg, got %+v", srv.Args)
	}
	if len(pm.Skills) != 1 || pm.Skills[0].Name != "greet" || pm.Skills[0].Description != "Greeting workflow skill" {
		t.Fatalf("unexpected portable skills: %+v", pm.Skills)
	}
}

func TestDiscoveryPrecedenceImpostorGuardAndGating(t *testing.T) {
	p := testProfilePaths(t)
	bundledDir := t.TempDir()
	projectCwd := t.TempDir()

	t.Setenv("HERMES_BUNDLED_PLUGINS", bundledDir)
	t.Setenv("HERMES_ENABLE_PROJECT_PLUGINS", "1")

	// Bundled plugin "alpha"
	writeFile(t, filepath.Join(bundledDir, "alpha", "plugin.yaml"), "name: alpha\nversion: 1.0.0\n")
	// Bundled plugin "kanban"
	writeFile(t, filepath.Join(bundledDir, "kanban", "plugin.yaml"), "name: kanban\nversion: 1.0.0\n")
	// Bundled excluded dir "memory" (must be skipped by general discovery)
	writeFile(t, filepath.Join(bundledDir, "memory", "mem0", "plugin.yaml"), "name: mem0\n")

	// User override of "alpha" (same dir name -> allowed, shadows bundled)
	writeFile(t, filepath.Join(p.Home, "plugins", "alpha", "plugin.yaml"), "name: alpha\nversion: 2.0.0\n")
	// User IMPOSTOR claiming "kanban" from dir "evil-dir" (must be rejected!)
	writeFile(t, filepath.Join(p.Home, "plugins", "evil-dir", "plugin.yaml"), "name: kanban\nversion: 9.9.9\n")
	// Foreign harness dir (must be skipped)
	writeFile(t, filepath.Join(p.Home, "plugins", ".claude-plugin", "plugin.json"), `{"name":"foreign"}`)

	// Project override of "alpha" (highest precedence)
	writeFile(t, filepath.Join(projectCwd, ".hermes", "plugins", "alpha", "plugin.yaml"), "name: alpha\nversion: 3.0.0\n")

	manifests := CollectDirectoryManifests(p, projectCwd)
	winners, warnings := ResolveManifestWinners(manifests)

	if len(warnings) == 0 {
		t.Fatalf("expected impostor guard warning for evil-dir/kanban")
	}
	if w, ok := winners["alpha"]; !ok || w.Version != "3.0.0" || w.Source != SourceProject {
		t.Fatalf("expected project alpha v3.0.0 to win, got %+v", w)
	}
	if w, ok := winners["kanban"]; !ok || w.Version != "1.0.0" || w.Source != SourceBundled {
		t.Fatalf("expected bundled kanban v1.0.0 to survive impostor guard, got %+v", w)
	}
	if _, exists := winners["mem0"]; exists {
		t.Fatalf("expected bundled memory/ to be skipped")
	}
	if _, exists := winners["foreign"]; exists {
		t.Fatalf("expected .claude-plugin/ to be skipped")
	}

	// Gating: opt-in by default, disabled overrides enabled.
	gDefault := GateManifest(winners["alpha"], PluginConfig{})
	if gDefault.Enabled || gDefault.Action != "placeholder" {
		t.Fatalf("expected opt-in default disabled, got %+v", gDefault)
	}
	gEnabled := GateManifest(winners["alpha"], PluginConfig{Enabled: []string{"alpha"}})
	if !gEnabled.Enabled || gEnabled.Action != "load" {
		t.Fatalf("expected enabled alpha to load, got %+v", gEnabled)
	}
	gBoth := GateManifest(winners["alpha"], PluginConfig{Enabled: []string{"alpha"}, Disabled: []string{"alpha"}})
	if gBoth.Enabled || gBoth.Reason != InBothListsReason {
		t.Fatalf("expected disabled to override enabled, got %+v", gBoth)
	}

	// Project plugins disabled when env unset.
	t.Setenv("HERMES_ENABLE_PROJECT_PLUGINS", "")
	infos, err := ListPlugins(p, projectCwd)
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	foundAlpha := false
	for _, info := range infos {
		if info.Manifest.CanonicalKey() == "alpha" {
			foundAlpha = true
			if info.Manifest.Source != SourceUser || info.Manifest.Version != "2.0.0" {
				t.Fatalf("expected user alpha v2.0.0 when project plugins off, got %+v", info.Manifest)
			}
		}
	}
	if !foundAlpha {
		t.Fatalf("alpha not found in ListPlugins")
	}
}

func TestEnableDisablePersistenceAndCLISubcommand(t *testing.T) {
	p := testProfilePaths(t)
	t.Setenv("HERMES_HOME", p.Home)
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	t.Setenv("HERMES_ENABLE_PROJECT_PLUGINS", "")

	// Seed config.yaml with an unrelated key to verify preservation.
	writeFile(t, p.ConfigFile, "skin: dracula\n")
	writeFile(t, filepath.Join(p.Home, "plugins", "demo", "plugin.yaml"), "name: demo\nversion: 0.1.0\ndescription: Demo plugin\n")

	if _, err := EnablePlugin(p, t.TempDir(), "demo"); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}
	info, err := InspectPlugin(p, t.TempDir(), "demo")
	if err != nil || !info.Gate.Enabled {
		t.Fatalf("expected demo enabled after EnablePlugin, got info=%+v err=%v", info, err)
	}

	rawCfg, _ := os.ReadFile(p.ConfigFile)
	if !strings.Contains(string(rawCfg), "skin: dracula") {
		t.Fatalf("expected unrelated config.yaml keys preserved, got:\n%s", string(rawCfg))
	}

	// Verify headless CLI subcommand `plugins list` and `plugins info demo` and `plugins disable demo`.
	var stdout, stderr bytes.Buffer
	res := cli.Dispatch(clihook.Env{
		Args:   []string{"plugins", "info", "demo"},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("CLI plugins info failed: res=%+v stderr=%s", res, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Plugin: demo") || !strings.Contains(stdout.String(), "enabled") {
		t.Fatalf("unexpected CLI info output:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	res = cli.Dispatch(clihook.Env{
		Args:   []string{"plugins", "disable", "demo"},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("CLI plugins disable failed: res=%+v stderr=%s", res, stderr.String())
	}
	info2, err := InspectPlugin(p, t.TempDir(), "demo")
	if err != nil || info2.Gate.Enabled {
		t.Fatalf("expected demo disabled after CLI disable, got info=%+v err=%v", info2, err)
	}
}

func TestLivePythonHermesPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on PATH")
	}

	p := testProfilePaths(t)
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	t.Setenv("HERMES_ENABLE_PROJECT_PLUGINS", "")

	plugDir := filepath.Join(p.Home, "plugins", "hello-py")
	writeFile(t, filepath.Join(plugDir, "plugin.yaml"), `
name: hello-py
version: "1.0.0"
description: Live Python Hermes plugin for integration testing
settings:
  prefix: "Hi"
`)
	writeFile(t, filepath.Join(plugDir, "__init__.py"), `
def register(ctx):
    # Stray stdout print must not corrupt NDJSON framing
    print("debug noise from plugin register()")

    prefix = ctx.get_setting("prefix", "Hello")
    ctx.state.set("loaded_count", (ctx.state.get("loaded_count") or 0) + 1)

    def hello_handler(args):
        who = args.get("name", "world")
        return f"{prefix}, {who}! (count={ctx.state.get('loaded_count')})"

    ctx.register_tool(
        name="hello_plugin",
        toolset="plugins",
        schema={
            "type": "object",
            "properties": {
                "name": {"type": "string"},
            },
        },
        handler=hello_handler,
        description="Greet someone from a Hermes Python plugin",
    )

    def pre_tool_guard(tool_name="", args=None, **_kwargs):
        if tool_name == "dangerous_tool":
            return {"action": "block", "message": "blocked by hello-py pre_tool_call"}
        return None

    ctx.register_hook("pre_tool_call", pre_tool_guard)
    ctx.register_system_prompt_section("greeting_rules", "Always be polite via hello-py.")
    ctx.register_command("hello", lambda raw_args: f"slash-hello: {raw_args}", description="Say hello")
`)

	if _, err := EnablePlugin(p, t.TempDir(), "hello-py"); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}

	ext := sdk.New("test-hermes-plugins")
	mgr := RegisterWithManager(ext, p, config.Default())
	if mgr == nil {
		t.Fatalf("RegisterWithManager returned nil")
	}
	defer mgr.Shutdown()

	// Verify manager recorded loaded plugin and spawned worker PID.
	infos := mgr.LoadedInfos()
	if len(infos) != 1 || !infos[0].Loaded || infos[0].LoadError != "" {
		t.Fatalf("expected hello-py loaded cleanly, got %+v", infos)
	}
	if len(infos[0].RegisteredTools) != 1 || infos[0].RegisteredTools[0] != "hello_plugin" {
		t.Fatalf("expected hello_plugin registered, got %+v", infos[0].RegisteredTools)
	}
	if len(infos[0].RegisteredHooks) != 1 || infos[0].RegisteredHooks[0] != "pre_tool_call" {
		t.Fatalf("expected pre_tool_call registered, got %+v", infos[0].RegisteredHooks)
	}
	expectedSec := SanitizeSectionName("hello-py_greeting_rules")
	if len(infos[0].RegisteredPrompt) != 1 || infos[0].RegisteredPrompt[0] != expectedSec {
		t.Fatalf("expected prompt section %q, got %+v", expectedSec, infos[0].RegisteredPrompt)
	}
	if strings.Contains(expectedSec, "-") || !strings.HasPrefix(expectedSec, "hermes_plugin_") {
		t.Fatalf("section name %q must be underscore-only with hermes_plugin_ prefix", expectedSec)
	}

	mgr.mu.RLock()
	if len(mgr.workers) != 1 {
		mgr.mu.RUnlock()
		t.Fatalf("expected 1 live python worker, got %d", len(mgr.workers))
	}
	worker := mgr.workers[0]
	workerPID := worker.PID()
	mgr.mu.RUnlock()
	if workerPID <= 0 {
		t.Fatalf("expected positive worker PID, got %d", workerPID)
	}

	// Execute the translated tool via the live python3 NDJSON bridge.
	rawTool, err := worker.Call(context.Background(), "call_tool", map[string]any{
		"name": "hello_plugin",
		"args": map[string]any{"name": "Hermes"},
	})
	if err != nil {
		t.Fatalf("call_tool(hello_plugin): %v", err)
	}
	toolRes := formatToolRPCResult(rawTool)
	if toolRes.IsError || !strings.Contains(toolRes.Content, "Hi, Hermes! (count=1)") {
		t.Fatalf("unexpected hello_plugin result: %+v", toolRes)
	}

	// Invoke pre_tool_call hook for a blocked tool and verify veto.
	rawBlock, err := worker.Call(context.Background(), "call_hook", map[string]any{
		"ref_id": "hook_1",
		"payload": map[string]any{
			"tool_name": "dangerous_tool",
			"args":      map[string]any{"cmd": "rm -rf /"},
		},
	})
	if err != nil {
		t.Fatalf("call_hook(pre_tool_call): %v", err)
	}
	if !strings.Contains(string(rawBlock), "blocked by hello-py pre_tool_call") {
		t.Fatalf("expected pre_tool_call block response, got %s", string(rawBlock))
	}

	// Render plugin prompt section and verify underscore-only section name & content.
	rawSec, err := worker.Call(context.Background(), "render_prompt_section", map[string]any{
		"id":      "greeting_rules",
		"context": map[string]any{},
	})
	if err != nil {
		t.Fatalf("render_prompt_section: %v", err)
	}
	secText := string(rawSec)
	sections := &sdk.SystemPromptSections{}
	sections.Set("hermes_identity", "You are Hermes.")
	sections.Set(expectedSec, secText)
	foundIdentity := false
	foundPluginSec := false
	for _, s := range *sections {
		if s.Name == "hermes_identity" && s.Value == "You are Hermes." {
			foundIdentity = true
		}
		if s.Name == expectedSec && strings.Contains(s.Value, "Always be polite via hello-py.") {
			foundPluginSec = true
		}
	}
	if !foundIdentity || !foundPluginSec {
		t.Fatalf("expected both hermes_identity and %s in sections, got %+v", expectedSec, *sections)
	}

	// Clean teardown on Shutdown (zero leaked python3 PIDs).
	mgr.Shutdown()
	if _, err := worker.Call(context.Background(), "call_tool", map[string]any{"name": "hello_plugin"}); err == nil {
		t.Fatalf("expected worker.Call to fail after Shutdown")
	}
}
