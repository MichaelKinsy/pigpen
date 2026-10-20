package pi_permission_system

import (
	"path/filepath"
	"testing"
)

// The cost of the config system on the paths that run on every turn: re-reading the merged config (before_agent_start), loading
// the policy scopes through the mtime cache, the `/permission-system show` text. The fixture is what port/probe/config-bench.test.ts
// times in the original.

func benchRig(b *testing.B) (agentDir, cwd string) {
	b.Helper()
	tmp := b.TempDir()
	agentDir, cwd = filepath.Join(tmp, "agent"), filepath.Join(tmp, "project")
	write := func(path, content string) {
		writeTestFileB(b, path, content)
	}
	write(filepath.Join(agentDir, "extensions", "pi-permission-system", "config.json"),
		`{"yoloMode":false,"permissionReviewLog":true,"promptMaxRows":24,"permission":{"*":"ask","read":"allow","write":{"*":"deny","src/*":"allow"},"bash":{"*":"ask","git status":"allow","git diff *":"allow","npm *":{"action":"deny","reason":"Use pnpm instead"},"rm *":"deny"},"mcp":{"exa:*":"allow"},"skill":{"*":"ask","librarian":"allow"},"path":{"*":"allow","*.env":"deny"},"external_directory_read":{"~/dev/*":"allow"}}}`)
	write(filepath.Join(cwd, ".pi", "extensions", "pi-permission-system", "config.json"), `{"permission":{"bash":{"pnpm *":"allow"},"edit":"ask"}}`)
	write(filepath.Join(agentDir, "agents", "coder.md"), "---\nname: coder\ndescription: Does things\npermission:\n  bash:\n    \"git *\": allow\n    \"rm *\": deny\n  read: allow\n---\n# Coder\n")
	write(filepath.Join(agentDir, "mcp.json"), `{"mcpServers":{"exa":{},"research":{},"danger-srv":{}}}`)
	return agentDir, cwd
}

func BenchmarkLoadAndMergeConfigs(b *testing.B) {
	agentDir, cwd := benchRig(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		loadAndMergeConfigs(agentDir, cwd, "", MergeOptions{})
	}
}

func BenchmarkConfigStoreRefresh(b *testing.B) {
	agentDir, cwd := benchRig(b)
	store := NewConfigStore(ConfigStoreDeps{AgentDir: agentDir, PolicyPaths: probePaths{}, Logger: storeProbeLogger{}})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store.Refresh(cwd, true)
	}
}

// BenchmarkPolicyLoaderCached is the per-check cost of the loader: four scope loads and the cache stamp, all answered from the cache.
func BenchmarkPolicyLoaderCached(b *testing.B) {
	agentDir, cwd := benchRig(b)
	l := NewFilePolicyLoader(derivePolicyLoaderOptions(agentDir, cwd))
	l.LoadGlobalConfig()
	l.LoadProjectConfig()
	l.LoadAgentConfig("coder")
	l.GetConfiguredMcpServerNames()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.GetCacheStamp("coder")
		l.LoadGlobalConfig()
		l.LoadProjectConfig()
		l.LoadAgentConfig("coder")
		l.LoadProjectAgentConfig("coder")
		l.GetConfiguredMcpServerNames()
	}
}

func BenchmarkPermissionSystemShow(b *testing.B) {
	agentDir, cwd := benchRig(b)
	store := NewConfigStore(ConfigStoreDeps{AgentDir: agentDir, PolicyPaths: probePaths{}, Logger: storeProbeLogger{}})
	store.Refresh(cwd, true)
	rules := Ruleset{rule("read", "*", "allow", "config", "global"), rule("bash", "rm *", "deny", "config", "project")}
	controller := PermissionSystemConfigController{Config: store, ConfigPath: "/x", GetActiveAgentConfigRules: func() Ruleset { return rules }}
	ctx := &stubCommandContext{hasUI: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx.notes = ctx.notes[:0]
		_ = runPermissionSystemCommand(ctx, "show", controller)
	}
}

func BenchmarkValidateConfig(b *testing.B) {
	text := `{"yoloMode":false,"permission":{"*":"ask","read":"allow","bash":{"*":"ask","git status":"allow","npm *":{"action":"deny","reason":"Use pnpm instead"}}}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseUnifiedText(text, "/c.json")
	}
}
