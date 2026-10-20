package warden

// Twins of tests/rules-audit.test.ts (`/warden rules audit` and `/warden bench`).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const auditRulesMD = "# No console statements\nCode must not contain `console.log`.\n\n# Return types on exports\npaths: src/**/*.ts\nEvery exported function must declare its return type.\n\n# Docs stay calm\npaths: docs/**\nDocumentation must stay factual."

// auditProject is the upstream suite's shared fixture; every test gets its own copy.
func auditProject(t *testing.T) (string, *RuleSet) {
	t.Helper()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), auditRulesMD)
	writeFile(t, filepath.Join(cwd, "src", "a.ts"), "export const a = 1;\n")
	writeFile(t, filepath.Join(cwd, "src", "b.ts"), "export const b = 2;\n")
	writeFile(t, filepath.Join(cwd, "src", "c.ts"), "export const c = 3;\n")
	writeFile(t, filepath.Join(cwd, "src", "sub", "d.ts"), "export const d = 4;\n")
	writeFile(t, filepath.Join(cwd, "src", "secret", "keys.ts"), "export const key = \"k\";\n")
	writeFile(t, filepath.Join(cwd, "src", "a.test.ts"), "export const t = 1;\n")
	writeFile(t, filepath.Join(cwd, "docs", "example.js"), "module.exports = 1;\n")
	set := NewRuleStore().Load(cwd, rulesConfig())
	if set == nil || len(set.Rules) != 3 {
		t.Fatal("fixture rules load")
	}
	return cwd, set
}

func guardedConfig() RulesConfig {
	return rulesConfig(func(c *RulesConfig) { c.Exclude = []string{"src/secret/**"}; c.Skip = []string{"**/*.test.ts"} })
}

// twin: tests/rules-audit.test.ts:76
func TestCollectAuditFilesCapsInSortedOrder(t *testing.T) {
	cwd, set := auditProject(t)
	guarded := guardedConfig()
	plan := collectAuditFiles(cwd, []string{"src"}, set, guarded, 2)
	mustEqual(t, plan.Files, []string{"src/a.ts", "src/b.ts"}, "files")
	mustEqual(t, plan.Matched, 4, "matched")
	mustEqual(t, plan.LeftOut, 2, "left out")
	mustEqual(t, len(plan.Missing), 0, "missing")
	full := collectAuditFiles(cwd, []string{"."}, set, guarded, 50)
	mustEqual(t, full.Files, []string{"docs/example.js", "src/a.ts", "src/b.ts", "src/c.ts", "src/sub/d.ts"}, "full")
	mustEqual(t, full.LeftOut, 0, "full left out")
	missing := collectAuditFiles(cwd, []string{"src", "nowhere.ts"}, set, guarded, 50)
	mustEqual(t, missing.Missing, []string{"nowhere.ts"}, "missing")
	mustEqual(t, missing.Matched, 4, "matched with a missing path")
}

// twin: tests/rules-audit.test.ts:91
func TestCollectAuditFilesExcludeAndSkip(t *testing.T) {
	cwd, set := auditProject(t)
	plain := collectAuditFiles(cwd, []string{"src"}, set, rulesConfig(), 50)
	mustEqual(t, plain.Files, []string{"src/a.test.ts", "src/a.ts", "src/b.ts", "src/c.ts", "src/secret/keys.ts", "src/sub/d.ts"}, "plain")
	filtered := collectAuditFiles(cwd, []string{"src"}, set, guardedConfig(), 50)
	mustEqual(t, filtered.Files, []string{"src/a.ts", "src/b.ts", "src/c.ts", "src/sub/d.ts"}, "filtered")
	mustEqual(t, filtered.Matched, 4, "matched")
}

// twin: tests/rules-audit.test.ts:99
func TestCollectAuditFilesNeedsAnApplicableRule(t *testing.T) {
	cwd, _ := auditProject(t)
	scopedOnly := &RuleSet{Sources: []string{"rules.md"}, Rules: parseRules("# Return types on exports\npaths: src/**/*.ts\nEvery exported function must declare its return type.")}
	plan := collectAuditFiles(cwd, []string{"docs", "src"}, scopedOnly, guardedConfig(), 50)
	mustEqual(t, plan.Files, []string{"src/a.ts", "src/b.ts", "src/c.ts", "src/sub/d.ts"}, "docs has no rule that applies to it")
}

// twin: tests/rules-audit.test.ts:105
func TestCollectAuditFilesSkipsGitignoredFiles(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git is not available: %v %s", err, out)
	}
	writeFile(t, filepath.Join(repo, ".gitignore"), "gen/\n")
	writeFile(t, filepath.Join(repo, "gen", "hook.ts"), "export const h = 1;\n")
	writeFile(t, filepath.Join(repo, "kept.ts"), "export const k = 1;\n")
	writeFile(t, filepath.Join(repo, "pi-warden.md"), "# Use const\nPrefer const.\n")
	repoSet := NewRuleStore().Load(repo, rulesConfig())
	plan := collectAuditFiles(repo, []string{"."}, repoSet, rulesConfig(), 50)
	mustEqual(t, plan.Files, []string{"kept.ts"}, "files")
}

// twin: tests/rules-audit.test.ts:125
func TestParseRulesAuditArgsAndParseBenchArgs(t *testing.T) {
	mustEqual(t, parseRulesAuditArgs(nil), RulesAuditArgs{Paths: []string{"."}, Max: 50}, "defaults")
	mustEqual(t, parseRulesAuditArgs([]string{"src", "tests", "--max", "15", "--yes"}), RulesAuditArgs{Paths: []string{"src", "tests"}, Max: 15, Yes: true}, "flags")
	mustMatch(t, parseRulesAuditArgs([]string{"--max"}).Error, `--max needs a positive integer`, "--max alone")
	mustMatch(t, parseRulesAuditArgs([]string{"--max", "0"}).Error, `--max needs a positive integer`, "--max 0")
	mustMatch(t, parseRulesAuditArgs([]string{"--nope"}).Error, `Unknown option --nope`, "unknown")
	mustEqual(t, parseBenchArgs(nil), BenchArgs{Runs: 10}, "bench defaults")
	mustEqual(t, parseBenchArgs([]string{"--runs", "3"}), BenchArgs{Runs: 3}, "bench runs")
	mustMatch(t, parseBenchArgs([]string{"--runs", "x"}).Error, `--runs needs a positive integer`, "bench bad runs")
	mustMatch(t, parseBenchArgs([]string{"--fast"}).Error, `Unknown option --fast`, "bench unknown")
}

func auditOpts(cwd string, set *RuleSet, config RulesConfig, judge Judge) RulesAuditOptions {
	return RulesAuditOptions{Cwd: cwd, Paths: []string{"src"}, Max: 50, Config: config, Set: set, Judge: judge, Timeout: time.Second, Destination: "api.example.test"}
}

func requestPaths(j *recordingJudge) []string {
	var out []string
	for _, r := range j.calls() {
		out = append(out, r.State["path"].(string))
	}
	sortStrings(out)
	return out
}

// twin: tests/rules-audit.test.ts:140
func TestRunRulesAuditConfirmDialogNamesWhatLeavesTheMachine(t *testing.T) {
	cwd, set := auditProject(t)
	judge := stubJudge(map[string]float64{"no-console-statements": 0.9}, "")
	type shownDialog struct{ title, body string }
	var shown []shownDialog
	options := auditOpts(cwd, set, guardedConfig(), judge)
	options.Confirm = func(title, body string) bool { shown = append(shown, shownDialog{title, body}); return false }
	declined := runRulesAudit(options)
	mustEqual(t, declined.Status, "cancelled", "declined")
	mustEqual(t, len(judge.calls()), 0, "a decline sends nothing")
	mustEqual(t, len(shown), 1, "dialogs")
	mustMatch(t, shown[0].title, `^Send 4 files to api\.example\.test for a rules audit\?$`, "title")
	mustMatch(t, shown[0].body, `redacted sample of each file \(up to 6000 characters per file\)`, "what leaves")
	mustMatch(t, shown[0].body, `Nothing is written to the rules log\.`, "no log")

	options.Confirm = func(string, string) bool { return true }
	accepted := runRulesAudit(options)
	mustEqual(t, accepted.Status, "done", "accepted")
	mustEqual(t, len(judge.calls()), 4, "one write request per file")
	mustEqual(t, requestPaths(judge), []string{"src/a.ts", "src/b.ts", "src/c.ts", "src/sub/d.ts"}, "paths")
	for _, request := range judge.calls() {
		if _, ok := request.State["content"].(string); !ok {
			t.Fatal("each file is judged as a write")
		}
		if !has(request.Questions, "rule_no-console-statements") || !has(request.Questions, "rule_return-types-on-exports") {
			t.Fatal("rule questions")
		}
		if has(request.Questions, "rule_docs-stay-calm") {
			t.Fatal("path-scoped rules stay out of other files")
		}
	}
}

// twin: tests/rules-audit.test.ts:170
func TestRunRulesAuditYesSkipsTheDialogAndHeadlessNeedsIt(t *testing.T) {
	cwd, set := auditProject(t)
	judge := stubJudge(nil, "")
	options := auditOpts(cwd, set, guardedConfig(), judge)
	options.Max, options.Yes = 1, true
	options.Confirm = func(string, string) bool { t.Fatal("--yes must not open a dialog"); return false }
	accepted := runRulesAudit(options)
	mustEqual(t, accepted.Status, "done", "accepted")
	mustEqual(t, accepted.Outcome.LeftOut, 3, "the cap is reported")
	mustEqual(t, len(judge.calls()), 1, "one request")

	headless := auditOpts(cwd, set, guardedConfig(), judge)
	got := runRulesAudit(headless)
	mustEqual(t, got.Status, "needs-yes", "headless")
	mustEqual(t, got.Files, 4, "files")
	mustEqual(t, len(judge.calls()), 1, "without a dialog and without --yes nothing is sent")
}

// twin: tests/rules-audit.test.ts:191
func TestRunRulesAuditWithNoJudgeSaysSoBeforeAnyDialog(t *testing.T) {
	cwd, set := auditProject(t)
	options := auditOpts(cwd, set, rulesConfig(), nil)
	options.Confirm = func(string, string) bool { t.Fatal("no judge must not open a dialog"); return false }
	mustEqual(t, runRulesAudit(options).Status, "no-judge", "no judge")
	noRules := auditOpts(cwd, nil, rulesConfig(), stubJudge(nil, ""))
	noRules.Yes = true
	got := runRulesAudit(noRules)
	mustEqual(t, got.Status, "no-files", "no rules")
	mustMatch(t, got.Reason, `no rules file`, "reason")
}

// twin: tests/rules-audit.test.ts:205
func TestRunRulesAuditAtMostFourRequestsInFlight(t *testing.T) {
	many := t.TempDir()
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(many, "f"+itoaInt(i)+".ts"), "export const v"+itoaInt(i)+" = "+itoaInt(i)+";\n")
	}
	writeFile(t, filepath.Join(many, "pi-warden.md"), "# Use const\nPrefer const.\n")
	manySet := NewRuleStore().Load(many, rulesConfig())
	var mu sync.Mutex
	active, peak, total := 0, 0, 0
	judge := &recordingJudge{handler: func(Request) (Evaluation, error) {
		mu.Lock()
		active++
		total++
		peak = max(peak, active)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return Evaluation{Model: "jev-test", ElapsedMs: 5, Answers: map[string]Answer{"rule_use-const": {Type: "choice", Choice: "compliant", Probabilities: map[string]float64{"violation": 0.05}}}}, nil
	}}
	options := auditOpts(many, manySet, rulesConfig(), judge)
	options.Paths, options.Yes = []string{"."}, true
	got := runRulesAudit(options)
	mustEqual(t, got.Status, "done", "status")
	mustEqual(t, total, 8, "requests")
	mustEqual(t, peak, 4, "peak concurrency")
}

func auditFinding(id, name string, violation float64) RuleFinding {
	return RuleFinding{RuleScore: RuleScore{ID: id, Name: name, Outcome: "violation", Violation: violation}}
}

func auditClean(id, name string, violation float64) RuleFinding {
	return RuleFinding{RuleScore: RuleScore{ID: id, Name: name, Outcome: "compliant", Violation: violation}}
}

func auditResult(path string, findings, soft, scores []RuleFinding) RulesAuditFileResult {
	var all []RuleScore
	for _, group := range [][]RuleFinding{findings, soft, scores} {
		for _, item := range group {
			all = append(all, item.RuleScore)
		}
	}
	return RulesAuditFileResult{Path: path, Verdict: RulesVerdict{Source: "typesafe", Path: path, Tool: "write", Sources: []string{"pi-warden.md"}, Asked: 2, Scores: all, Findings: findings, SoftFindings: soft}}
}

// twin: tests/rules-audit.test.ts:245
func TestRuleAuditRowsOneRowPerRuleFlaggedFirst(t *testing.T) {
	rows := ruleAuditRows([]RulesAuditFileResult{
		auditResult("src/a.ts", []RuleFinding{auditFinding("no-console-statements", "No console statements", 0.91)}, nil, nil),
		auditResult("src/b.ts", []RuleFinding{auditFinding("no-console-statements", "No console statements", 0.72), auditFinding("errors-swallowed", "Errors are not swallowed", 0.80)}, nil, nil),
		auditResult("src/c.ts", nil, nil, []RuleFinding{auditClean("no-console-statements", "No console statements", 0.05)}),
	})
	type row struct {
		id              string
		judged, flagged int
		mean            string
	}
	got := []row{}
	for _, r := range rows {
		got = append(got, row{r.ID, r.Judged, r.Flagged, toFixed(r.Mean, 2)})
	}
	mustEqual(t, got, []row{{"no-console-statements", 3, 2, "0.56"}, {"errors-swallowed", 1, 1, "0.80"}}, "rows")
}

// twin: tests/rules-audit.test.ts:257
func TestFormatRulesAuditRowsAndFilesInShortLines(t *testing.T) {
	results := []RulesAuditFileResult{
		auditResult("src/b.ts", []RuleFinding{auditFinding("errors-swallowed", "Errors are not swallowed", 0.80)}, nil, nil),
		auditResult("src/a.ts", []RuleFinding{auditFinding("no-console-statements", "No console statements", 0.91), auditFinding("errors-swallowed", "Errors are not swallowed", 0.72)}, []RuleFinding{auditFinding("comments-why", "Comments explain why, not what", 0.55)}, nil),
	}
	table := strings.Split(formatRulesAuditRows(ruleAuditRows(results)), "\n")
	mustMatch(t, table[0], `^rule\s+judged\s+flagged\s+mean$`, "header")
	mustMatch(t, table[1], `^errors-swallowed\s+2\s+2\s+0\.76$`, "errors-swallowed")
	mustMatch(t, table[2], `^no-console-statements\s+1\s+1\s+0\.91$`, "no-console-statements")
	mustMatch(t, table[3], `^comments-why\s+1\s+0\s+0\.55$`, "comments-why")
	for _, line := range table {
		if utf16Len(line) > 70 {
			t.Fatalf("short terminal lines: %q", line)
		}
	}
	list := formatRulesAuditFiles(results)
	mustEqual(t, strings.Split(list, "\n"), []string{
		"src/a.ts",
		"  No console statements 0.91",
		"  Errors are not swallowed 0.72",
		"  Comments explain why, not what 0.55 (double-check)",
		"src/b.ts",
		"  Errors are not swallowed 0.80",
	}, "list")
	mustEqual(t, formatRulesAuditFiles([]RulesAuditFileResult{auditResult("src/c.ts", nil, nil, nil)}), "no file flagged", "none")
	paths := []string{}
	for _, f := range flaggedAuditFiles(results) {
		paths = append(paths, f.Path)
	}
	mustEqual(t, paths, []string{"src/a.ts", "src/b.ts"}, "most findings first")
}

// twin: tests/rules-audit.test.ts:282
func TestFormatRulesAuditSummaryTableFileListAndCopy(t *testing.T) {
	results := []RulesAuditFileResult{
		auditResult("src/a.ts", []RuleFinding{auditFinding("no-console-statements", "No console statements", 0.91)}, nil, nil),
		auditResult("src/b.ts", nil, nil, []RuleFinding{auditClean("no-console-statements", "No console statements", 0.05)}),
		auditResult("src/c.ts", nil, nil, []RuleFinding{auditClean("no-console-statements", "No console statements", 0.05)}),
	}
	outcome := RulesAuditOutcome{
		Paths: []string{"src"}, Max: 2, Selected: 3, Judged: 3, LeftOut: 1, Missing: []string{"nowhere.ts"}, Unreadable: []string{"src/empty.ts"}, Errors: []string{"src/boom.ts"},
		Results: results, Rows: ruleAuditRows(results), Flagged: 1, Sources: []string{"pi-warden.md"}, Rules: 1,
		ReportPath: ".pi-warden/rules-audit.md", ElapsedMs: 12,
	}
	lines := strings.Split(formatRulesAudit(outcome), "\n")
	mustEqual(t, lines[0], "Rules audit: 3 files judged as new writes against pi-warden.md (1 rule in play); 1 left out at the --max 2 cap; 1 of 3 flagged.", "summary")
	mustMatch(t, lines[1], `^rule\s+judged\s+flagged\s+mean$`, "table header")
	mustMatch(t, lines[2], `^no-console-statements\s+3\s+1\s+0\.34$`, "table row")
	mustEqual(t, lines[3], "flagged files, worst first:", "list header")
	mustEqual(t, lines[4:6], []string{"src/a.ts", "  No console statements 0.91"}, "list")
	mustEqual(t, lines[6], "not found in the project: nowhere.ts.", "missing")
	mustEqual(t, lines[7], "not judged (unreadable or empty): src/empty.ts.", "unreadable")
	mustEqual(t, lines[8], "judgment failed for 1 file: src/boom.ts.", "errors")
	mustEqual(t, lines[9], "Markdown copy: .pi-warden/rules-audit.md. Nothing was recorded in the rules log.", "copy")
}

// twin: tests/rules-audit.test.ts:305
func TestRunRulesAuditWritesTheMarkdownCopy(t *testing.T) {
	cwd, set := auditProject(t)
	judge := stubJudge(map[string]float64{"no-console-statements": 0.95}, "")
	options := auditOpts(cwd, set, guardedConfig(), judge)
	options.Yes = true
	mustEqual(t, runRulesAudit(options).Status, "done", "status")
	data, err := os.ReadFile(filepath.Join(cwd, ".pi-warden", "rules-audit.md"))
	if err != nil {
		t.Fatal(err)
	}
	markdown := string(data)
	mustMatch(t, markdown, `^# Rules audit\n`, "title")
	mustMatch(t, markdown, `- Judged: 4 of 4 selected files as new writes \(0 left out at the --max 50 cap\)`, "judged")
	mustMatch(t, markdown, `## By rule\n\n\| rule \| judged \| flagged \| mean \|`, "table")
	mustMatch(t, markdown, "\\| `no-console-statements` \\| 4 \\| 4 \\| 0\\.95 \\|", "row")
	mustMatch(t, markdown, `## Flagged files, worst first`, "flagged")
	mustMatch(t, markdown, "- `src/a\\.ts` — No console statements 0\\.95", "file")
	mustMatch(t, markdown, `Nothing was recorded in the rules log\.\n$`, "footer")
	mustNotContain(t, markdown, cwd, "the copy carries no machine paths")
}

// twin: tests/rules-audit.test.ts:320
func TestRulesAuditMarkdownACleanRunStillWritesACopy(t *testing.T) {
	outcome := RulesAuditOutcome{Paths: []string{"."}, Max: 50, Selected: 1, Judged: 1, Results: []RulesAuditFileResult{auditResult("src/a.ts", nil, nil, nil)}, Sources: []string{"pi-warden.md"}, ReportPath: ".pi-warden/rules-audit.md", ElapsedMs: 1}
	markdown := rulesAuditMarkdown(outcome, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	mustMatch(t, markdown, `- Date: 2026-10-01T00:00:00\.000Z`, "date")
	mustMatch(t, markdown, `- no file flagged`, "no flags")
}

// twin: tests/rules-audit.test.ts:334
func TestPercentileNearestRank(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	mustEqual(t, percentile(values, 50), 5.0, "p50")
	mustEqual(t, percentile(values, 95), 10.0, "p95")
	mustEqual(t, percentile([]float64{7}, 95), 7.0, "single")
	mustEqual(t, percentile(nil, 50), 0.0, "empty")
}

// twin: tests/rules-audit.test.ts:342
func TestRunBenchLatencyAndUsage(t *testing.T) {
	cwd, set := auditProject(t)
	timings := []float64{40, 10, 30, 20}
	clock, run := 0.0, 0
	usage := BenchUsage{}
	judge := &recordingJudge{handler: func(Request) (Evaluation, error) {
		clock += timings[run]
		run++
		usage.RequestsStarted++
		usage.InputTokens += 100
		usage.EstimatedUSD += 0.001
		compliant := Answer{Type: "choice", Choice: "compliant", Confidence: 0.9, Probabilities: map[string]float64{"compliant": 0.95, "violation": 0.04, "not_applicable": 0.005, "insufficient_context": 0.005}}
		return Evaluation{Model: "jev-test", ElapsedMs: 5, Answers: map[string]Answer{"rule_no-console-statements": compliant, "rule_return-types-on-exports": compliant}}, nil
	}}
	result := runBench(BenchOptions{Runs: 4, Cwd: cwd, Config: rulesConfig(), Set: set, Judge: judge, GetUsage: func() BenchUsage { return usage }, Timeout: time.Second, Now: func() float64 { return clock }})
	mustEqual(t, result.Status, "done", "status")
	mustEqual(t, result.P50, 20, "p50")
	mustEqual(t, result.P95, 40, "p95")
	mustEqual(t, result.Requests, 4, "requests")
	mustEqual(t, result.Asked, 2, "asked")
	mustEqual(t, result.MeanInputTokens, 100.0, "tokens")
	mustEqual(t, result.CostPerCheck, 0.001, "cost")
	mustEqual(t, result.CostPer100, 0.1, "cost per 100")
	text := formatBench(result)
	mustMatch(t, text, `(?m)^Bench: 4 checks of one built-in sample file against the active rules \(2 rules per check\)\.$`, "header")
	mustMatch(t, text, `The sample is built in and no project content is sent, so no confirmation was needed\.`, "no content")
	mustMatch(t, text, `Latency: p50 20 ms, p95 40 ms; requests 4\.`, "latency")
	mustMatch(t, text, `Input tokens per check: 100 \(mean\)\.`, "input tokens")
	mustMatch(t, text, `Estimated cost per check: \$0\.001000; per 100 edits: \$0\.100000\.`, "cost text")
}

// twin: tests/rules-audit.test.ts:379
func TestRunBenchWithNoJudgeSaysSoAndSendsNothing(t *testing.T) {
	cwd, set := auditProject(t)
	result := runBench(BenchOptions{Runs: 10, Cwd: cwd, Config: rulesConfig(), Set: set, Judge: nil,
		GetUsage: func() BenchUsage { t.Fatal("no usage without a judge"); return BenchUsage{} }, Timeout: time.Second})
	mustEqual(t, result.Status, "no-judge", "status")
}

// twin: tests/rules-audit.test.ts:387
func TestRunBenchASampleTheRulesKeepOutIsNeverSent(t *testing.T) {
	cwd, set := auditProject(t)
	judge := stubJudge(nil, "")
	excluded := runBench(BenchOptions{Runs: 2, Cwd: cwd, Config: rulesConfig(func(c *RulesConfig) { c.Exclude = []string{"src/**"} }), Set: set, Judge: judge, Timeout: time.Second})
	mustEqual(t, excluded.Status, "skipped", "excluded")
	mustMatch(t, excluded.Reason, `rules\.exclude`, "exclude reason")
	skipped := runBench(BenchOptions{Runs: 2, Cwd: cwd, Config: rulesConfig(func(c *RulesConfig) { c.Skip = []string{"**/warden-bench-sample.ts"} }), Set: set, Judge: judge, Timeout: time.Second})
	mustEqual(t, skipped.Status, "skipped", "skipped")
	mustMatch(t, skipped.Reason, `rules\.skip`, "skip reason")
	mustEqual(t, len(judge.calls()), 0, "requests")
}
