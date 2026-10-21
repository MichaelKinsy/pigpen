package warden

// The Go side of the per-call comparison with the original (port/bench/perf-original.ts runs the same inputs under
// Node; port/bench/perf-run.sh interleaves the two). Cases that already have a benchmark in rules_bench_test.go,
// guardcore_bench_test.go and supervision_bench_test.go are not repeated; these are the ones the comparison needed
// in addition, with the inputs the Node harness uses.
//
//	go test -run xxx -bench 'RulesEvaluate|RulesDescribeEdit|ArmingArm|ArmingCheckNot|SQLTarget|MatchPatternsFive|MatchPatternsWritePath|MatchPathRules' -benchmem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cmpPathRules = []PathRule{
	{ID: "env", Paths: []string{"**/.env"}, Access: "none", Tools: []string{"*"}, Action: "warn"},
	{ID: "keys", Paths: []string{"~/.ssh/id_*"}, Access: "read", Tools: []string{"*"}, Action: "block"},
	{ID: "audit", Paths: []string{"/var/audit/*.log"}, Access: "write", Tools: []string{"*"}, Action: "confirm"},
}

// cmpJudge answers a rules request the way the Node harness's judge does: one rule violates, the rest comply.
type cmpJudge struct{}

func (cmpJudge) Evaluate(_ context.Context, r Request) (Evaluation, error) {
	answers := make(map[string]Answer, len(r.Questions))
	for id, q := range r.Questions {
		if id == locatorQuestion {
			probs := map[string]float64{}
			for i, key := range q.CriteriaOrder {
				if i == 0 {
					probs[key] = 0.7
				} else {
					probs[key] = 0.3 / float64(len(q.CriteriaOrder)-1)
				}
			}
			answers[id] = Answer{Type: "choice", Choice: q.CriteriaOrder[0], Confidence: 0.7, Probabilities: probs}
			continue
		}
		violation := 0.05
		if id == "rule_rule-7" {
			violation = 0.85
		}
		choice := "compliant"
		if violation >= 0.5 {
			choice = "violation"
		}
		answers[id] = Answer{Type: "choice", Choice: choice, Confidence: 0.9, Probabilities: map[string]float64{"compliant": 1 - violation - 0.02, "violation": violation, "not_applicable": 0.01, "insufficient_context": 0.01}}
	}
	return Evaluation{Model: "jev-test", ElapsedMs: 7, Answers: answers}, nil
}

func cmpProject(b *testing.B) string {
	dir := b.TempDir()
	var fns []string
	for i := range 80 {
		fns = append(fns, "export function f"+itoaInt(i)+"(a: number): number {\n  return a + "+itoaInt(i)+";\n}")
	}
	if err := writeBenchFile(filepath.Join(dir, "pi-warden.md"), benchRulesDoc(40)); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := writeBenchFile(filepath.Join(dir, "src", "a.ts"), strings.Join(fns, "\n")); err != nil {
		b.Fatal(err)
	}
	return dir
}

func BenchmarkRulesDescribeEditTarget(b *testing.B) {
	dir := cmpProject(b)
	input := map[string]any{"path": "src/a.ts", "edits": []any{
		map[string]any{"oldText": "return a + 3;", "newText": "return a + 3 + 1;"},
		map[string]any{"oldText": "return a + 20;", "newText": "console.log(a);\n  return a + 20;"},
		map[string]any{"oldText": "return a + 41;", "newText": "return a * 41;"},
	}}
	b.ReportAllocs()
	for b.Loop() {
		describeTarget("edit", input, dir)
	}
}

func benchRulesOptions(dir string, judge Judge) RulesOptions {
	return RulesOptions{Cwd: dir, Config: DefaultConfig().Rules, Set: &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(benchRulesDoc(40))}, Judge: judge, Timeout: time.Second}
}

// One write against 40 rules with no judge: the cost every write pays before the judge is asked.
func BenchmarkRulesEvaluateOffline(b *testing.B) {
	dir := cmpProject(b)
	input := writeInput("src/a.ts", strings.Repeat("export const a = 1;\n", 300))
	options := benchRulesOptions(dir, nil)
	b.ReportAllocs()
	for b.Loop() {
		evaluateRules("write", input, options)
	}
}

// The same write through the whole path with an instant judge: describe, request, ask, score.
func BenchmarkRulesEvaluateStubJudge40(b *testing.B) {
	dir := cmpProject(b)
	input := writeInput("src/a.ts", strings.Repeat("export const a = 1;\n", 300))
	options := benchRulesOptions(dir, cmpJudge{})
	b.ReportAllocs()
	for b.Loop() {
		evaluateRules("write", input, options)
	}
}

func benchArmingRule() ArmingRule {
	return ArmingRule{ID: "r", When: ArmingWhen{Edited: []string{"**/*.yaml"}}, Arms: ArmingArms{Command: `\bapply\b`, For: "10m"}, Action: "confirm"}
}

func BenchmarkArmingCheckNotArmed(b *testing.B) {
	tr := NewArmingTracker([]ArmingRule{benchArmingRule()}, nil)
	b.ReportAllocs()
	for b.Loop() {
		tr.CheckArmed("kubectl apply -f a.yaml")
	}
}

func BenchmarkArmingArmMatching(b *testing.B) {
	tr := NewArmingTracker([]ArmingRule{benchArmingRule()}, nil)
	b.ReportAllocs()
	for b.Loop() {
		tr.Arm("write", "/p/a.yaml", "/p", nil)
	}
}

func BenchmarkArmingArmNotMatching(b *testing.B) {
	tr := NewArmingTracker([]ArmingRule{benchArmingRule()}, nil)
	b.ReportAllocs()
	for b.Loop() {
		tr.Arm("edit", "/p/src/a.go", "/p", nil)
	}
}

func BenchmarkSQLTargetLocalDelete(b *testing.B) {
	command := `psql -h 127.0.0.1 -U postgres -d app -c "DELETE FROM users WHERE email LIKE 'crew-%';"`
	b.ReportAllocs()
	for b.Loop() {
		sqlTarget(command)
	}
}

func BenchmarkSQLTargetHostedWrite(b *testing.B) {
	command := `psql postgres://app:secret@db.example.com:5432/app -c "UPDATE users SET plan = 'pro' WHERE id = 7"`
	b.ReportAllocs()
	for b.Loop() {
		sqlTarget(command)
	}
}

// The six commands of bench_test.go without `git push --force-with-lease`, whose safe-lease check runs git in the
// original (a process per call) and a stub in that benchmark.
var cmpCommands = []string{
	"ls -la",
	"go test ./... -run TestFoo -count=1",
	"git status --short && git diff --stat",
	"rm -rf ./build && mkdir build && cp -r src build/ && cd build && make all",
	"cat > /tmp/x <<'EOF'\nline one\nline two\nEOF\nsed -i 's/a/b/' /tmp/x",
}

func BenchmarkMatchPatternsFiveCommands(b *testing.B) {
	opts := &PatternOptions{Git: func(string, ...string) (string, bool) { return "", false }}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		MatchPatterns("bash", map[string]any{"command": cmpCommands[i%len(cmpCommands)]}, "/work/repo", opts)
	}
}

func BenchmarkMatchPatternsFiveCommandsUserRules(b *testing.B) {
	rules := []CommandRule{
		{ID: "no-curl-post", Pattern: `curl\s+.*-X\s*POST`, Message: "POST"},
		{ID: "no-scp", Pattern: `\bscp\b`, Message: "scp"},
		{ID: "no-psql", Pattern: `psql\s+.*prod`, Message: "prod db"},
	}
	opts := &PatternOptions{Git: func(string, ...string) (string, bool) { return "", false }, CommandRules: rules, CommandDenyRules: rules[:1]}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		MatchPatterns("bash", map[string]any{"command": cmpCommands[i%len(cmpCommands)]}, "/work/repo", opts)
	}
}

func BenchmarkMatchPatternsWritePath(b *testing.B) {
	opts := &PatternOptions{Git: func(string, ...string) (string, bool) { return "", false }}
	input := map[string]any{"path": "/home/user/project/src/internal/module/file_name.go", "content": "x"}
	b.ReportAllocs()
	for b.Loop() {
		MatchPatterns("write", input, "/home/user/project", opts)
	}
}

func BenchmarkMatchPathRulesCommand(b *testing.B) {
	input := map[string]any{"command": "go test ./... && cat notes.txt | tee out.log"}
	exempt := map[string]bool{}
	b.ReportAllocs()
	for b.Loop() {
		matchPathRules("bash", input, "/work/project", cmpPathRules, exempt)
	}
}

func BenchmarkMatchPathRulesWrite(b *testing.B) {
	input := map[string]any{"path": "/work/project/.env", "content": "x"}
	exempt := map[string]bool{}
	b.ReportAllocs()
	for b.Loop() {
		matchPathRules("write", input, "/work/project", cmpPathRules, exempt)
	}
}
