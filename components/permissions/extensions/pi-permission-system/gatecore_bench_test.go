package pi_permission_system

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Latency and allocations of one tool_call through the gate pipeline, on the inputs of port/probe/gatecore-bench.test.ts.

func BenchmarkGateCore(b *testing.B) {
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
	cfg := `{"permission":{"*":"allow","external_directory":{"*":"ask"},"path":{"*":"allow","*.env":"deny"},"mcp":{"*":"allow"},"bash":{"*":"allow","rm *":"deny"}}}`
	if err := os.WriteFile(globalConfigPath, []byte(cfg), 0o666); err != nil {
		b.Fatal(err)
	}
	manager := NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, McpServerNames: []string{}}})
	rules := &SessionRules{}
	paths := ExtensionPaths{PiInfrastructureDirs: []string{"/test/agent"}, PiInfrastructureExcludedDirs: []string{"/test/agent/logs"}}
	session := NewPermissionSession(paths, &fakeForwarding{}, manager, rules, &fakeConfigStore{current: defaultExtensionConfig()}, &fakeAuthSelection{}, PathFlavorForPlatform(runtime.GOOS))
	resolver := NewPermissionResolver(manager, rules)
	prompter := &gfEscalator{fn: func(*PromptPermissionDetails) (PermissionPromptDecision, error) {
		return PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: gfHuman}, nil
	}}
	runner := NewGateRunner(resolver, &SessionRules{}, prompter, NewGateDecisionReporter(&gfBenchLogger{}, &gfBenchEvents{}), func() bool { return false })
	tools := []any{}
	for _, n := range []string{"read", "write", "bash", "weather_lookup", "mcp__srv__list"} {
		tools = append(tools, obj("name", n))
	}
	handler := NewPermissionGateHandler(session, &gfToolRegistry{all: tools}, NewToolCallGatePipeline(resolver, session, nil, nil), NewSkillInputGatePipeline(ResolverSkillInputInputs{Resolver: resolver}), runner)
	ctx := SessionContext{AuthorityContext: AuthorityContext{ForwarderContext: ForwarderContext{Cwd: cwd, HasUI: true, SessionManager: gfSessionManager{}}}}
	ev := func(tool string, input *jsObject) *jsObject {
		return obj("type", "tool_call", "toolCallId", "tc-1", "toolName", tool, "input", input)
	}
	inputs := []struct {
		name  string
		event *jsObject
	}{
		{"ReadInside", ev("read", obj("path", "src/a.ts"))},
		{"ReadOutsideAsk", ev("read", obj("path", "/outside/project/file.ts"))},
		{"WriteDeniedByPath", ev("write", obj("path", "src/secret.env", "content", "x"))},
		{"ExtensionTool", ev("weather_lookup", obj("city", "Chicago", "units", "metric"))},
		{"McpTool", ev("mcp__srv__list", obj("target", "prod"))},
		{"BashChain", ev("bash", obj("command", "git status && ls src | wc -l"))},
		{"BashDeny", ev("bash", obj("command", "echo ok && rm -rf build"))},
	}
	for _, in := range inputs {
		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := handler.HandleToolCall(in.event, ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type gfBenchLogger struct{}

func (gfBenchLogger) Review(string, *jsObject) {}
func (gfBenchLogger) Debug(string, *jsObject)  {}
func (gfBenchLogger) Warn(string)              {}

type gfBenchEvents struct{}

func (gfBenchEvents) Emit(string, any) error { return nil }
