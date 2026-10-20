package pi_permission_system

import "testing"

// Benchmarks of the path layer's hot calls (a gate builds an AccessPath per path argument and normalizes every tool input).

func BenchmarkAccessPathForPath(b *testing.B) {
	mockRealpathB(b)
	n := NewPathNormalizer(PosixPathFlavor, "/projects/my-app")
	b.ReportAllocs()
	for b.Loop() {
		n.ForPath("src/components/button.tsx", nil).MatchValues()
	}
}

func BenchmarkForToolPathRead(b *testing.B) {
	mockRealpathB(b)
	n := NewPathNormalizer(PosixPathFlavor, "/projects/my-app")
	b.ReportAllocs()
	for b.Loop() {
		n.ForToolPath("read", "src/components/button.tsx")
	}
}

func BenchmarkWin32Comparable(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Win32PathFlavor.Comparable(`src\Components\Button.tsx`, `C:\Projects\My-App`)
	}
}

func BenchmarkNormalizeInputMcp(b *testing.B) {
	in := newObject()
	in.set("tool", "github_search_code")
	servers := []string{"github", "todoist", "exa"}
	b.ReportAllocs()
	for b.Loop() {
		NormalizeInput("mcp", in, servers)
	}
}

func BenchmarkCreatePiMcpToolTargets(b *testing.B) {
	servers := []string{"danger-srv", "github", "todoist"}
	b.ReportAllocs()
	for b.Loop() {
		CreatePiMcpToolTargets("mcp__danger_srv__wipe", servers)
	}
}

func mockRealpathB(b *testing.B) {
	old := realpathSync
	realpathSync = identityRealpath
	b.Cleanup(func() { realpathSync = old })
}
