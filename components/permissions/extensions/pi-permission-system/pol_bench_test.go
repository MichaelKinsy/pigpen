package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"
)

// Costs of the policy paths: one warm check per tool call (every tool call pays it), the cold check (first call after a reload), a
// session-approval record, and the resolver's family fold. The same operations and inputs run in port/probe/manager-bench.test.ts
// against the original (port/bench-manager.sh interleaves the two).

const (
	benchGlobal = `{"permission":{"*":"ask","bash":{"*":"ask","git *":"allow","git push*":"ask","rm -rf *":"deny","npm *":"allow","ls":"allow","echo *":"allow","sudo *":"deny","node -e *":"ask","cat *":"allow","grep *":"allow","find *":"ask"},` +
		`"read":{"*":"allow","*.env":"deny","/secret/*":"deny"},"path":{"*":"allow","~/.ssh/*":"deny"},"external_directory":{"/tmp/*":"allow","/work/*":"allow"},` +
		`"mcp":{"*":"ask","exa_*":"allow","danger":"deny"},"skill":{"*":"ask","librarian":"allow"}}}`
	benchProject = `{"permission":{"bash":{"make *":"allow","git push --force*":"deny"},"write":{"*":"ask","src/*":"allow"}}}`
	benchAgent   = "---\nname: reviewer\npermission:\n  bash:\n    \"git log *\": allow\n    \"*\": ask\n  read: allow\n---\n# body\n"
)

func benchManager(b *testing.B, yolo bool) *PermissionManager {
	b.Helper()
	base := b.TempDir()
	global, agents := filepath.Join(base, "pi-permissions.jsonc"), filepath.Join(base, "agents")
	project := filepath.Join(base, "project", "pi-permissions.jsonc")
	for path, content := range map[string]string{global: benchGlobal, project: benchProject, filepath.Join(agents, "reviewer.md"): benchAgent} {
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
			b.Fatal(err)
		}
	}
	opts := PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: global, AgentsDir: agents, ProjectGlobalConfigPath: project,
		ProjectAgentsDir: filepath.Join(base, "project", "agents"), McpServerNames: []string{"exa", "danger"}}, IsYoloEnabled: func() bool { return yolo }}
	return NewPermissionManager(opts)
}

func benchBashIntent(b *testing.B, command string) ToolAccessIntent {
	b.Helper()
	input, err := parseJSON([]byte(`{"command":"` + command + `"}`))
	if err != nil {
		b.Fatal(err)
	}
	return ToolAccessIntent{Surface: "bash", Input: input, AgentName: sp("reviewer")}
}

func benchSessionRules() Ruleset {
	rules := Ruleset{}
	for _, p := range []string{"make test", "make build", "go test *", "go vet *", "cargo *", "pnpm *", "yarn *", "python *", "pytest *", "tsc *"} {
		rules = append(rules, sessionRule("bash", p))
	}
	return rules
}

func BenchmarkCheckBashWarm(b *testing.B) {
	m := benchManager(b, false)
	intent := benchBashIntent(b, "git push origin main")
	m.Check(intent, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Check(intent, nil)
	}
}

func BenchmarkCheckBashSessionRules(b *testing.B) {
	m := benchManager(b, false)
	intent := benchBashIntent(b, "make test")
	rules := benchSessionRules()
	m.Check(intent, rules)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Check(intent, rules)
	}
}

func BenchmarkCheckBashYolo(b *testing.B) {
	m := benchManager(b, true)
	intent := benchBashIntent(b, "find . -name x")
	m.Check(intent, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Check(intent, nil)
	}
}

func BenchmarkCheckPathValuesWarm(b *testing.B) {
	m := benchManager(b, false)
	intent := PathValuesAccessIntent{Surface: "path_read", Values: []string{"/work/project/src/app.ts", "src/app.ts"}, AgentName: sp("reviewer")}
	m.Check(intent, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Check(intent, nil)
	}
}

func BenchmarkCheckCold(b *testing.B) {
	base := b.TempDir()
	global, agents := filepath.Join(base, "pi-permissions.jsonc"), filepath.Join(base, "agents")
	project := filepath.Join(base, "project", "pi-permissions.jsonc")
	for path, content := range map[string]string{global: benchGlobal, project: benchProject, filepath.Join(agents, "reviewer.md"): benchAgent} {
		_ = os.MkdirAll(filepath.Dir(path), 0o777)
		_ = os.WriteFile(path, []byte(content), 0o666)
	}
	opts := PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: global, AgentsDir: agents, ProjectGlobalConfigPath: project,
		ProjectAgentsDir: filepath.Join(base, "project", "agents"), McpServerNames: []string{"exa", "danger"}}}
	intent := benchBashIntent(b, "git push origin main")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewPermissionManager(opts).Check(intent, nil)
	}
}

func BenchmarkToolPermissionAndDenied(b *testing.B) {
	m := benchManager(b, false)
	m.GetToolPermission("bash", sp("reviewer"))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.GetToolPermission("bash", sp("reviewer"))
		m.IsToolFullyDenied("bash", sp("reviewer"))
	}
}

func BenchmarkResolverFold(b *testing.B) {
	m := benchManager(b, false)
	rules := &SessionRules{}
	rules.Approve("external_directory", "/opt/*")
	resolver := NewPermissionResolver(m, rules)
	intent := PathValuesAccessIntent{Surface: "external_directory", Values: []string{"/tmp/x/y.txt", "/private/tmp/x/y.txt"}, AgentName: sp("reviewer")}
	resolver.ResolvePrefixed(intent)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resolver.ResolvePrefixed(intent)
	}
}

func BenchmarkSessionApprovals(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rules := &SessionRules{}
		for _, p := range []string{"/a/*", "/b/*", "/c/*", "/d/*"} {
			rules.RecordSessionApproval(SessionApprovalForPatterns("external_directory", []string{p, p + "x"}))
		}
		rules.Approve("bash", "git *")
		_ = rules.GetRuleset()
	}
}
