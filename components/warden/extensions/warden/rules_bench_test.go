package warden

// Benchmarks of the rules guard's hot paths: what one write or edit costs before the judge is asked.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func benchRulesDoc(n int) string {
	var parts []string
	for i := 1; i <= n; i++ {
		parts = append(parts, "# Rule "+itoaInt(i)+"\npaths: src/**/*.ts\nseverity: high\nThe code must follow rule number "+itoaInt(i)+" in every file it writes, with no exception.")
	}
	return strings.Join(parts, "\n\n")
}

func BenchmarkRulesParse40(b *testing.B) {
	doc := benchRulesDoc(40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseRules(doc)
	}
}

func BenchmarkRulesStoreLoadCached(b *testing.B) {
	dir := b.TempDir()
	if err := writeBenchFile(filepath.Join(dir, "pi-warden.md"), benchRulesDoc(40)); err != nil {
		b.Fatal(err)
	}
	store := NewRuleStore()
	cfg := rulesConfig()
	store.Load(dir, cfg)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store.Load(dir, cfg)
	}
}

func BenchmarkRulesBuildRequest40(b *testing.B) {
	dir := b.TempDir()
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(benchRulesDoc(40))}
	target := describeTarget("write", writeInput("src/a.ts", strings.Repeat("export const a = 1;\n", 300)), dir)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buildRulesRequest(target, set)
	}
}

func BenchmarkRulesDescribeWriteTarget(b *testing.B) {
	dir := b.TempDir()
	input := writeInput("src/a.ts", strings.Repeat("export const a = 1;\n", 600))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		describeTarget("write", input, dir)
	}
}

func BenchmarkShellWritesHeredoc(b *testing.B) {
	command := "mkdir -p src && cat > src/a.ts <<'EOF'\n" + strings.Repeat("export const a = 1;\n", 100) + "EOF\necho b >> b.ts"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		shellWrites(command, ShellWritesOptions{})
	}
}

func BenchmarkRulesScore40(b *testing.B) {
	rules := parseRules(benchRulesDoc(40))
	answers := map[string]Answer{}
	for _, r := range rules {
		answers["rule_"+r.ID] = Answer{Type: "choice", Choice: "compliant", Probabilities: map[string]float64{"compliant": 0.9, "violation": 0.05}}
	}
	cfg := rulesConfig()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scoreRuleAnswers(rules, answers, cfg, nil)
	}
}

func writeBenchFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }
