package tintinweb_subagents

import (
	"os"
	"testing"
)

// Costs nested delegation adds to starting a child. Run: go test -run xxx -bench Nested -benchmem. The first benchmark is
// the environment of a child before this change (the parent's environment and the depth variable); the others are what
// a child costs now, with and without the nested variables, and what a child that may delegate pays to register.

func BenchmarkNestedChildEnvBefore(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = append(os.Environ(), depthEnv+"=1")
	}
}

func BenchmarkNestedChildEnvPlain(b *testing.B) {
	b.ReportAllocs()
	spec := childSpec{Depth: 1, MaxDepth: 2}
	for i := 0; i < b.N; i++ {
		_ = childEnv(spec, "")
	}
}

func BenchmarkNestedChildEnvWithAllowance(b *testing.B) {
	b.ReportAllocs()
	spec := childSpec{Depth: 1, MaxDepth: 2, Allowed: []string{"worker", "scout"}, AgentID: "0123456789abcdef0", ConfigCwd: "/work"}
	for i := 0; i < b.N; i++ {
		_ = childEnv(spec, "/tmp/usage")
	}
}

func BenchmarkNestedChildArgs(b *testing.B) {
	b.ReportAllocs()
	spec := childSpec{Depth: 1, MaxDepth: 2, Allowed: "all", AllowTools: []string{"read", "grep"}, Model: "m", Thinking: "low", SystemPrompt: "p"}
	for i := 0; i < b.N; i++ {
		_ = childArgs(spec)
	}
}

func BenchmarkNestedConfigFromEnv(b *testing.B) {
	b.Setenv(depthEnv, "1")
	b.Setenv(maxDepthEnv, "2")
	b.Setenv(allowedEnv, `["worker","scout"]`)
	b.Setenv(agentIDEnv, "0123456789abcdef0")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = nestedConfigFromEnv()
	}
}

// BenchmarkNestedRegistration is the cost of Extension() in a child that may delegate: three tool definitions, four
// hooks, one read of the settings and agent files.
func BenchmarkNestedRegistration(b *testing.B) {
	b.Setenv("PIG_CODING_AGENT_DIR", b.TempDir())
	b.Chdir(b.TempDir())
	b.Setenv(depthEnv, "1")
	b.Setenv(maxDepthEnv, "2")
	b.Setenv(allowedEnv, `"all"`)
	b.Setenv(agentIDEnv, "0123456789abcdef0")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Extension()
	}
}

// BenchmarkNestedRegistrationWithoutAllowance is the same for a child that may not: it registers nothing.
func BenchmarkNestedRegistrationWithoutAllowance(b *testing.B) {
	b.Setenv(depthEnv, "1")
	b.Setenv(allowedEnv, "")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Extension()
	}
}
