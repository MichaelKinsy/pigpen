package pi_permission_system

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Latency and allocations of the service queries and one before_agent_start turn, on the inputs of port/probe/handlers-bench.test.ts.

func BenchmarkHandlers(b *testing.B) {
	base, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	cwd := filepath.Join(base, "cwd")
	agentsDir := filepath.Join(base, "agents")
	for _, d := range []string{filepath.Join(cwd, "src"), agentsDir} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			b.Fatal(err)
		}
	}
	globalConfigPath := filepath.Join(base, "pi-permissions.jsonc")
	cfg := `{"permission":{"*":"allow","skill":{"*":"allow","secret-*":"deny"},"path":{"*":"allow","*.env":"deny"},"mcp":{"*":"allow"},"bash":{"*":"allow","rm *":"deny"},"write":"deny"}}`
	if err := os.WriteFile(globalConfigPath, []byte(cfg), 0o666); err != nil {
		b.Fatal(err)
	}
	manager := NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, McpServerNames: []string{}}})
	rules := &SessionRules{}
	paths := ExtensionPaths{PiInfrastructureDirs: []string{"/test/agent"}, PiInfrastructureExcludedDirs: []string{"/test/agent/logs"}}
	session := NewPermissionSession(paths, &fakeForwarding{}, manager, rules, &fakeConfigStore{current: defaultExtensionConfig()}, &fakeAuthSelection{}, PathFlavorForPlatform(runtime.GOOS))
	resolver := NewPermissionResolver(manager, rules)
	service := NewLocalPermissionsService(resolver, session, &ToolInputFormatterRegistry{}, &ToolAccessExtractorRegistry{}, &AuthorizerRegistry{})
	ctx := SessionContext{AuthorityContext: AuthorityContext{ForwarderContext: ForwarderContext{Cwd: cwd, HasUI: true, SessionManager: gfSessionManager{}}}, IsProjectTrusted: func() bool { return true }}
	if err := session.Activate(ctx); err != nil {
		b.Fatal(err)
	}
	bashparse.WarmBashParser()

	str := func(s string) *string { return &s }
	agent := str("agent")
	b.Run("ServiceSkill", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			service.CheckPermission("skill", str("skill-1"), agent)
		}
	})
	readPath := str(filepath.Join(cwd, "src", "a.ts"))
	b.Run("ServiceReadPath", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			service.CheckPermission("read", readPath, nil)
		}
	})
	b.Run("ServiceMcp", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			service.CheckPermission("mcp", str("srv:list"), nil)
		}
	})
	chain := str("git status && ls src | wc -l")
	b.Run("ServiceBashChain", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			service.CheckPermission("bash", chain, nil)
		}
	})

	toolNames := []string{"read", "bash", "edit", "write", "ls", "find", "grep", "weather_lookup", "mcp__srv__list", "mcp__srv__get", "task", "todo"}
	registry := newAPRegistry(toolNames)
	var blocks []string
	skillItems := make([]PromptSkill, 8)
	for i := range skillItems {
		name := "skill-" + string(rune('0'+i))
		if i%3 == 0 {
			name = "secret-" + string(rune('0'+i))
		}
		skillItems[i] = PromptSkill{Name: name}
		blocks = append(blocks, "  <skill>\n    <name>"+name+"</name>\n    <description>Description "+string(rune('0'+i))+"</description>\n    <location>/skills/"+string(rune('0'+i))+"/SKILL.md</location>\n  </skill>")
	}
	catalogue := "You are an assistant.\n\n<available_skills>\n" + strings.Join(blocks, "\n") + "\n</available_skills>"
	handler := NewAgentPrepHandler(NewSessionTurnPrep(session, func() {}, &fakeAnnouncer{order: &recordedOrder{}}, &fakeIssueReporter{}), session, resolver, registry, &gfBenchLogger{}, apDetector{false})
	b.Run("AgentPrepTurn", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			payload := BeforeAgentStartPayload{SystemPrompt: catalogue, Options: &PromptOptions{ToolSnippets: map[string]string{}, Sections: &apSections{values: map[string]string{}}, Skills: append([]PromptSkill{}, skillItems...)}}
			if err := handler.Handle(payload, ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
