package warden

import "testing"

// The hold check runs once per tool call (onToolCall -> ActionGuard.Inspect -> MatchPatterns), so its
// per-call cost is what an enabled warden adds to every call. Run with:
//
//	go test -run xxx -bench . -benchmem
var benchCommands = []string{
	"ls -la",
	"go test ./... -run TestFoo -count=1",
	"git status --short && git diff --stat",
	"rm -rf ./build && mkdir build && cp -r src build/ && cd build && make all",
	"git push --force-with-lease origin feature",
	"cat > /tmp/x <<'EOF'\nline one\nline two\nEOF\nsed -i 's/a/b/' /tmp/x",
}

func BenchmarkMatchPatterns(b *testing.B) {
	git := func(string, ...string) (string, bool) { return "", false }
	opts := &PatternOptions{Git: git}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MatchPatterns("bash", map[string]any{"command": benchCommands[i%len(benchCommands)]}, "/work/repo", opts)
	}
}

// User rules are compiled from the configuration; a rule that is compiled on every call is paid for by every call.
func BenchmarkMatchPatternsUserRules(b *testing.B) {
	git := func(string, ...string) (string, bool) { return "", false }
	rules := []CommandRule{
		{ID: "no-curl-post", Pattern: `curl\s+.*-X\s*POST`, Message: "POST"},
		{ID: "no-scp", Pattern: `\bscp\b`, Message: "scp"},
		{ID: "no-psql", Pattern: `psql\s+.*prod`, Message: "prod db"},
	}
	opts := &PatternOptions{Git: git, CommandRules: rules, CommandDenyRules: rules[:1]}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MatchPatterns("bash", map[string]any{"command": benchCommands[i%len(benchCommands)]}, "/work/repo", opts)
	}
}

func BenchmarkSensitivePath(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SensitivePath("/home/user/project/src/internal/module/file_name.go")
	}
}

// RecordUI runs for every file a successful edit or write changed; the UI globs are the same each time.
func BenchmarkRecordUI(b *testing.B) {
	globs := DefaultUIFiles
	changed := []string{"src/components/Button.tsx", "internal/server/handler.go", "docs/readme.md"}
	e := EmptyEvidence()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RecordUI(e, changed, false, globs)
	}
}
