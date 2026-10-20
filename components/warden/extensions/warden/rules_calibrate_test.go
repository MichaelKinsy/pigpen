package warden

// Twins of tests/rules-calibrate.test.ts (`/warden rules calibrate` and `/warden rules tune`).

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func calibrateRuleSet(markdown string) *RuleSet {
	return &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(markdown)}
}

// calibrateJudge answers every `rule_<id>` question with the violation P for that rule and file; unnamed rules are clean.
func calibrateJudge(violation func(ruleID, path string) float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		answers := map[string]Answer{}
		path, _ := r.State["path"].(string)
		for id, q := range r.Questions {
			if id == locatorQuestion {
				keys := q.CriteriaOrder
				probs := map[string]float64{}
				for i, key := range keys {
					if i == 0 {
						probs[key] = 0.7
					} else {
						probs[key] = 0.3 / float64(max(1, len(keys)-1))
					}
				}
				answers[id] = Answer{Type: "choice", Choice: keys[0], Confidence: 0.7, Probabilities: probs}
				continue
			}
			score := violation(strings.TrimPrefix(id, "rule_"), path)
			choice := "compliant"
			if score >= 0.5 {
				choice = "violation"
			}
			answers[id] = Answer{Type: "choice", Choice: choice, Confidence: 0.9, Probabilities: map[string]float64{"compliant": 1 - score - 0.02, "violation": score, "not_applicable": 0.01, "insufficient_context": 0.01}}
		}
		return Evaluation{Model: "jev-test", ElapsedMs: 5, Answers: answers}, nil
	}}
}

func hunkOf(mutate func(*HistoryHunk)) HistoryHunk {
	h := HistoryHunk{OldText: "old line", NewText: "new line", Before: "ctx\nold line\nctx", After: "ctx\nnew line\nctx", Start: 2, Count: 1}
	if mutate != nil {
		mutate(&h)
	}
	return h
}

func newTextHunk(text string) HistoryHunk { return hunkOf(func(h *HistoryHunk) { h.NewText = text }) }

func commitOf(files ...*HistoryFile) *HistoryCommit {
	return &HistoryCommit{Hash: "c0ffee01deadbeef", At: "2026-01-01T00:00:00.000Z", Files: files}
}

func fileOf(path string, hunks ...HistoryHunk) *HistoryFile {
	return &HistoryFile{Path: path, Hunks: hunks}
}

const historyLog = "\x01aaaa1111bbbb2222cccc3333dddd4444eeee5555\x022026-01-02T03:04:05Z\n" +
	"\n" +
	"diff --git a/src/app.ts b/src/app.ts\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/src/app.ts\n" +
	"+++ b/src/app.ts\n" +
	"@@ -1,4 +1,5 @@\n" +
	" const a = 1;\n" +
	"-const b = 2;\n" +
	"-const c = 3;\n" +
	"+const b = 20;\n" +
	"+const c = 30;\n" +
	"+const d = 40;\n" +
	" const e = 5;\n" +
	"diff --git a/logo.png b/logo.png\n" +
	"index 3333333..4444444 100644\n" +
	"Binary files a/logo.png and b/logo.png differ\n" +
	"\x019999888877776666555544443333222211110000\x022026-01-01T00:00:00Z\n" +
	"\n" +
	"diff --git a/my file.ts b/my file.ts\n" +
	"index 1111111..2222222 100644\n" +
	"--- \"a/my file.ts\"\n" +
	"+++ \"b/my file.ts\"\n" +
	"@@ -10,2 +10,1 @@\n" +
	" keep\n" +
	"-drop\n" +
	"diff --git a/gone.txt b/gone.txt\n" +
	"deleted file mode 100644\n" +
	"index 5555555..0000000\n" +
	"--- a/gone.txt\n" +
	"+++ /dev/null\n" +
	"@@ -1,2 +0,0 @@\n" +
	"-first\n" +
	"-second\n"

func filePaths(files []*HistoryFile) []string {
	out := []string{}
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// twin: tests/rules-calibrate.test.ts:109
func TestParseHistoryEachHunkIsOneOldNewPair(t *testing.T) {
	commits := parseHistory(historyLog)
	first, second := commits[0], commits[1]
	mustEqual(t, first.Hash, "aaaa1111bbbb2222cccc3333dddd4444eeee5555", "hash")
	mustEqual(t, first.At, "2026-01-02T03:04:05Z", "at")
	mustEqual(t, filePaths(first.Files), []string{"src/app.ts", "logo.png"}, "files")
	app := first.Files[0]
	mustEqual(t, app.Binary, false, "binary")
	mustEqual(t, len(app.Hunks), 1, "hunks")
	mustEqual(t, app.Hunks[0].OldText, "const b = 2;\nconst c = 3;", "oldText")
	mustEqual(t, app.Hunks[0].NewText, "const b = 20;\nconst c = 30;\nconst d = 40;", "newText")
	mustEqual(t, app.Hunks[0].Before, "const a = 1;\nconst b = 2;\nconst c = 3;\nconst e = 5;", "before")
	mustEqual(t, app.Hunks[0].After, "const a = 1;\nconst b = 20;\nconst c = 30;\nconst d = 40;\nconst e = 5;", "after")
	mustEqual(t, app.Hunks[0].Start, 1, "start")
	mustEqual(t, app.Hunks[0].Count, 5, "count")
	mustEqual(t, first.Files[1].Binary, true, "a binary patch carries no lines")
	mustEqual(t, len(first.Files[1].Hunks), 0, "binary hunks")

	mustEqual(t, second.Hash, "9999888877776666555544443333222211110000", "second hash")
	mustEqual(t, filePaths(second.Files), []string{"my file.ts", "gone.txt"}, "quoted paths and the deleted file's a-side are kept")
	mustEqual(t, second.Files[0].Hunks[0].OldText, "drop", "removed")
	mustEqual(t, second.Files[0].Hunks[0].NewText, "", "a pure removal writes nothing")
	mustEqual(t, second.Files[1].Hunks[0].NewText, "", "deleted file")
}

// twin: tests/rules-calibrate.test.ts:133
func TestParseHistoryAChangeLineThatLooksLikeAFileHeaderStaysContent(t *testing.T) {
	log := strings.Join([]string{"\x01ffff\x022026-01-01T00:00:00Z", "", "diff --git a/x.md b/x.md", "--- a/x.md", "+++ b/x.md", "@@ -1,1 +1,2 @@", "--- old quote", "+++ new quote", ""}, "\n")
	file := parseHistory(log)[0].Files[0]
	mustEqual(t, file.Path, "x.md", "the +++ inside the hunk does not become the path")
	mustEqual(t, file.Hunks[0].OldText, "-- old quote", "old")
	mustEqual(t, file.Hunks[0].NewText, "++ new quote", "new")
}

func gitIn(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git %v: %v %s", args, err, out)
	}
}

func makeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	return repo
}

func commitFiles(t *testing.T, repo string, files map[string]string, message string) {
	t.Helper()
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writeFile(t, filepath.Join(repo, name), files[name])
		gitIn(t, repo, "add", "-f", name)
	}
	gitIn(t, repo, "commit", "-q", "-m", message)
}

func mustHistory(t *testing.T, cwd string, n int) []*HistoryCommit {
	t.Helper()
	history, err := collectHistory(cwd, n)
	if err != nil {
		t.Fatal(err)
	}
	return history
}

// twin: tests/rules-calibrate.test.ts:175
func TestCollectHistoryTheFileAfterTheCommitIsTheContext(t *testing.T) {
	repo := makeRepo(t)
	commitFiles(t, repo, map[string]string{"src/app.ts": "line1\nline2\nline3\nline4\nline5\nline6\n"}, "first")
	commitFiles(t, repo, map[string]string{"src/app.ts": "line1\nline2\nCHANGED\nline4\nline5\nline6\n"}, "second")
	history := mustHistory(t, repo, 20)
	mustEqual(t, len(history), 2, "commits")
	mustEqual(t, history[0].Files[0].After, "line1\nline2\nCHANGED\nline4\nline5\nline6\n", "newest commit first, with its own file state")
	mustEqual(t, history[1].Files[0].After, "line1\nline2\nline3\nline4\nline5\nline6\n", "the older commit keeps its own file state")
	mustEqual(t, history[1].Files[0].Hunks[0].NewText, "line1\nline2\nline3\nline4\nline5\nline6", "first commit hunk")
	edits, _ := editViews(history[0].Files[0], editContextLines, maxEdits)
	mustEqual(t, len(edits), 1, "edits")
	mustMatch(t, *edits[0].After, `CHANGED`, "after")
	mustMatch(t, *edits[0].Before, `line3`, "before")
	mustEqual(t, edits[0].NewText, "CHANGED", "newText")
}

// twin: tests/rules-calibrate.test.ts:191
func TestEditViewsGiveAWideWindowAroundTheChange(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "line "+itoaInt(i+1))
	}
	after := strings.Join(append(append(append([]string{}, lines[:14]...), "INSERTED"), lines[14:]...), "\n")
	file := &HistoryFile{Path: "src/wide.ts", After: after, HasAfter: true, Hunks: []HistoryHunk{{NewText: "INSERTED", Before: "line 15", After: "line 15\nINSERTED\nline 16", Start: 15, Count: 1}}}
	edits, _ := editViews(file, editContextLines, maxEdits)
	mustMatch(t, *edits[0].After, `line 10`, "the window reaches past the diff context")
	mustMatch(t, *edits[0].After, `line 20`, "after reaches line 20")
	mustMatch(t, *edits[0].Before, `line 10`, "before reaches line 10")
	mustNotContain(t, *edits[0].Before, "INSERTED", "before has no insertion")
}

// twin: tests/rules-calibrate.test.ts:208
func TestChangeSkipReason(t *testing.T) {
	mustEqual(t, changeSkipReason(&HistoryFile{Path: "a.png", Binary: true, Hunks: []HistoryHunk{hunkOf(nil)}}), "binary file", "binary")
	mustEqual(t, changeSkipReason(&HistoryFile{Path: "src/x.ts"}), "no content change", "empty")
	mustEqual(t, changeSkipReason(fileOf("dist/bundle.js", hunkOf(nil))), "generated file", "dist")
	mustEqual(t, changeSkipReason(fileOf("package-lock.json", hunkOf(nil))), "generated file", "lock")
	mustEqual(t, changeSkipReason(fileOf("src/schema.generated.ts", hunkOf(nil))), "generated file", "generated name")
	mustEqual(t, changeSkipReason(fileOf("src/gen.ts", newTextHunk("// Code generated by tool. DO NOT EDIT.\nexport const x = 1;"))), "generated file", "the standard marker counts even outside a generated path")
	mustEqual(t, changeSkipReason(fileOf("src/x.ts", hunkOf(nil))), "", "judged")
}

// twin: tests/rules-calibrate.test.ts:218
func TestPlanCalibrationSkipsWithReasons(t *testing.T) {
	repo := makeRepo(t)
	commitFiles(t, repo, map[string]string{
		"good.ts":           "export const n = 1;\nsecond line\n",
		"dist/bundle.js":    "var x=1;\n",
		"gen.ts":            "// Code generated by tool. DO NOT EDIT.\nexport const y = 1;\n",
		"secrets/token.txt": "value\n",
		"tests/a.test.ts":   "test();\n",
	}, "first")
	writeFile(t, filepath.Join(repo, ".gitignore"), "ignored.log\n")
	commitFiles(t, repo, map[string]string{"ignored.log": "log\n"}, "second")
	commitFiles(t, repo, map[string]string{"good.ts": "export const n = 1;\n"}, "removal only")
	config := rulesConfig(func(c *RulesConfig) { c.Exclude = []string{"secrets/**"}; c.Skip = []string{"tests/**"} })
	plan := planCalibration(mustHistory(t, repo, 10), PlanOptions{Set: calibrateRuleSet("# Only rule\nMust hold.\n"), Config: config, Cwd: repo, MaxRequests: 40})
	paths := []string{}
	for _, s := range plan.Samples {
		paths = append(paths, s.Path)
	}
	mustEqual(t, paths, []string{"good.ts"}, "samples")
	skips := map[string]string{}
	for _, s := range plan.Skipped {
		skips[s.Path] = s.Reason
	}
	mustEqual(t, skips, map[string]string{
		"good.ts":           "nothing written in this change",
		"ignored.log":       "gitignored by the project",
		"dist/bundle.js":    "generated file",
		"gen.ts":            "generated file",
		"secrets/token.txt": "excluded from Jev by rules.exclude (secrets/**)",
		"tests/a.test.ts":   "rules do not apply by rules.skip (tests/**)",
	}, "skips")
	mustEqual(t, plan.Requests, 1, "requests")
}

func threeFileHistory() []*HistoryCommit {
	return []*HistoryCommit{{Hash: "c1", At: "2026-01-01T00:00:00.000Z", Files: []*HistoryFile{
		fileOf("f1.ts", newTextHunk("new 1")), fileOf("f2.ts", newTextHunk("new 2")), fileOf("f3.ts", newTextHunk("new 3")),
	}}}
}

// twin: tests/rules-calibrate.test.ts:244
func TestPlanCalibrationAndCalibrateTheCapKeepsTheNewestSamples(t *testing.T) {
	dir := t.TempDir()
	history := threeFileHistory()
	set := calibrateRuleSet("# Only rule\nMust hold.\n")
	config := rulesConfig()
	plan := planCalibration(history, PlanOptions{Set: set, Config: config, Cwd: dir, MaxRequests: 2})
	mustEqual(t, plan.Requests, 2, "requests")
	mustEqual(t, len(plan.Dropped), 1, "dropped")
	paths := []string{}
	for _, s := range plan.Samples {
		paths = append(paths, s.Path)
	}
	mustEqual(t, paths, []string{"f1.ts", "f2.ts"}, "samples")
	judge := calibrateJudge(func(string, string) float64 { return 0.05 })
	result := calibrate(history, CalibrateOptions{PlanOptions: PlanOptions{Set: set, Config: config, Cwd: dir, MaxRequests: 2}, Judge: judge, Timeout: time.Second})
	mustEqual(t, len(judge.calls()), 2, "the cap is the number of requests")
	mustEqual(t, result.Dropped, 1, "result dropped")
	mustEqual(t, result.Requests, 2, "result requests")
}

// twin: tests/rules-calibrate.test.ts:262
func TestCalibrateWithNoJudgeSendsAndScoresNothing(t *testing.T) {
	history := []*HistoryCommit{commitOf(fileOf("f.ts", hunkOf(nil)))}
	result := calibrate(history, CalibrateOptions{PlanOptions: PlanOptions{Set: calibrateRuleSet("# Only rule\nMust hold.\n"), Config: rulesConfig(), Cwd: t.TempDir(), MaxRequests: 5}, Timeout: time.Second})
	mustEqual(t, result.Requests, 0, "requests")
	mustEqual(t, len(result.Records), 0, "records")
	mustEqual(t, len(result.Rows), 0, "rows")
}

// twin: tests/rules-calibrate.test.ts:273
func TestCalibrateGate(t *testing.T) {
	mustEqual(t, calibrateGate(true, false), CalibrateGate{Kind: "confirm"}, "ui")
	mustEqual(t, calibrateGate(true, true), CalibrateGate{Kind: "send"}, "ui yes")
	mustEqual(t, calibrateGate(false, true), CalibrateGate{Kind: "send"}, "headless yes")
	refused := calibrateGate(false, false)
	mustEqual(t, refused.Kind, "refuse", "refuse")
	mustMatch(t, refused.Reason, `explicit --yes`, "reason")
}

// twin: tests/rules-calibrate.test.ts:282
func TestCalibrationNoticeNamesTheCountAndRedactsTheDiffs(t *testing.T) {
	history := []*HistoryCommit{commitOf(
		fileOf("src/app.ts", hunkOf(func(h *HistoryHunk) {
			h.NewText, h.After = "password: hunter2secret", "top\npassword: hunter2secret\nbottom"
		})),
		fileOf("src/other.ts", newTextHunk("other change")),
		fileOf("src/third.ts", newTextHunk("third change")),
		fileOf("src/fourth.ts", newTextHunk("fourth change")),
	)}
	plan := planCalibration(history, PlanOptions{Set: calibrateRuleSet("# Only rule\nMust hold.\n"), Config: rulesConfig(), Cwd: t.TempDir(), MaxRequests: 3})
	notice := calibrationNotice(plan)
	mustMatch(t, notice, `3 requests will go to the judgment backend`, "count")
	mustMatch(t, notice, `src/app\.ts`, "path")
	mustMatch(t, notice, `password: \[redacted\]`, "redacted")
	mustEqual(t, strings.Contains(notice, "hunter2secret"), false, "the diff is redacted before it is shown")
	mustMatch(t, notice, `1 further change stays outside the --max cap and is not sent\.`, "dropped")
}

const flagsMD = "# Hot rule\nRuns hot.\n\n# Cold rule\nRuns cold.\n\n# Mid rule\nRuns mid.\n\n# Py rule\npaths: **/*.py\nRuns on Python."

func manyFilesHistory(n int) []*HistoryCommit {
	var files []*HistoryFile
	for i := 1; i <= n; i++ {
		name := "f" + padStart(itoaInt(i), 2) + ".ts"
		files = append(files, fileOf(strings.ReplaceAll(name, " ", "0"), newTextHunk("change "+itoaInt(i))))
	}
	return []*HistoryCommit{{Hash: "c1", At: "2026-01-01T00:00:00.000Z", Files: files}}
}

func ruleRowIDs(rows []RuleCalibration) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// twin: tests/rules-calibrate.test.ts:303
func TestCalibrateOneLinePerRuleWorstFirst(t *testing.T) {
	judge := calibrateJudge(func(ruleID, path string) float64 {
		switch ruleID {
		case "hot-rule":
			return 0.95
		case "cold-rule":
			return 0.05
		}
		n := jsToNumber(path[1:3])
		if n <= 11 {
			return 0.4
		}
		return 0.75
	})
	result := calibrate(manyFilesHistory(21), CalibrateOptions{PlanOptions: PlanOptions{Set: calibrateRuleSet(flagsMD), Config: rulesConfig(), Cwd: t.TempDir(), MaxRequests: 40}, Judge: judge, Timeout: time.Second})
	mustEqual(t, ruleRowIDs(result.Rows), []string{"hot-rule", "mid-rule", "cold-rule"}, "worst first: noisy, then undecided, then dead")
	hot, mid, cold := result.Rows[0], result.Rows[1], result.Rows[2]
	mustEqual(t, hot.Flags, []string{"fires on everything"}, "hot flags")
	mustEqual(t, hot.Applied, 21, "hot applied")
	mustEqual(t, hot.Fired, 21, "hot fired")
	if d := hot.MeanViolation - 0.95; d > 1e-9 || d < -1e-9 {
		t.Fatalf("mean %v", hot.MeanViolation)
	}
	mustEqual(t, mid.Flags, []string{"undecided"}, "mid flags")
	mustEqual(t, mid.Fired, 10, "mid fired")
	mustEqual(t, len(cold.Flags), 0, "no fire in a replayed sample is not a flag")
	mustEqual(t, cold.Unfired, true, "cold unfired")
	mustEqual(t, hot.Unfired, false, "hot unfired")
	mustEqual(t, cold.Applied, 21, "cold applied")
	mustEqual(t, cold.Fired, 0, "cold fired")
	mustEqual(t, result.NeverApplied, []NamedRule{{ID: "py-rule", Name: "Py rule"}}, "never applied")

	lines := strings.Split(formatCalibration(result), "\n")
	mustEqual(t, lines[0], "Rules calibrate: 21 requests, 1 commit.", "header")
	mustEqual(t, lines[1], "Worst first:", "worst first")
	mustEqual(t, lines[2], "1. Hot rule · 21 applied · 21 fired 100% · mean 0.95 · fires on everything", "hot line")
	mustEqual(t, lines[4], "3. Cold rule · 21 applied · 0 fired 0% · mean 0.05 · no violation in sample", "cold line")
	mustMatch(t, lines[5], `Never applied to any file in the sample \(1\): Py rule\.`, "never applied line")
}

// twin: tests/rules-calibrate.test.ts:340
func TestCalibrateARulesOwnThresholdDecidesItsFire(t *testing.T) {
	md := "# Strict rule\nthreshold: 0.9\nMust be strict.\n\n# Normal rule\nMust be normal."
	history := []*HistoryCommit{commitOf(fileOf("f.ts", hunkOf(nil)))}
	judge := calibrateJudge(func(ruleID, _ string) float64 {
		if ruleID == "strict-rule" {
			return 0.85
		}
		return 0.8
	})
	result := calibrate(history, CalibrateOptions{PlanOptions: PlanOptions{Set: calibrateRuleSet(md), Config: rulesConfig(), Cwd: t.TempDir(), MaxRequests: 5}, Judge: judge, Timeout: time.Second})
	var strict, normal RuleRecord
	for _, r := range result.Records {
		if r.ID == "strict-rule" {
			strict = r
		}
		if r.ID == "normal-rule" {
			normal = r
		}
	}
	mustEqual(t, strict.Threshold, 0.9, "strict threshold")
	mustEqual(t, strict.Finding, false, "0.85 is under the rule's own 0.9 cutoff")
	mustEqual(t, normal.Threshold, 0.7, "normal threshold")
	mustEqual(t, normal.Finding, true, "normal finding")
	type pair struct {
		id    string
		fired int
	}
	got := []pair{}
	for _, r := range result.Rows {
		got = append(got, pair{r.ID, r.Fired})
	}
	mustEqual(t, got, []pair{{"normal-rule", 1}, {"strict-rule", 0}}, "rows")
}

// twin: tests/rules-calibrate.test.ts:357
func TestCalibrateRecordsEveryScoreWithSourceCalibrate(t *testing.T) {
	repo := makeRepo(t)
	commitFiles(t, repo, map[string]string{"a.ts": "const a = 1;\n"}, "first")
	judge := calibrateJudge(func(ruleID, _ string) float64 {
		if ruleID == "hot-rule" {
			return 0.95
		}
		return 0.05
	})
	result := calibrate(mustHistory(t, repo, 5), CalibrateOptions{PlanOptions: PlanOptions{Set: calibrateRuleSet(flagsMD), Config: rulesConfig(), Cwd: repo, MaxRequests: 10}, Judge: judge, Timeout: time.Second, Session: "s1"})
	if len(result.Records) < 3 {
		t.Fatalf("records: %d", len(result.Records))
	}
	for _, r := range result.Records {
		if r.Source != "calibrate" || r.Tool != "edit" || r.Session != "s1" {
			t.Fatalf("record %+v", r)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "records-log.jsonl")
	log := NewRulesLog(dir, dir, "s1", RulesLogOptions{Path: path})
	log.AppendRecords(result.Records)
	log.Flush()
	stored := mustReadLog(t, path)
	mustEqual(t, len(stored), len(result.Records), "stored")
	for _, r := range stored {
		if r.Source != "calibrate" {
			t.Fatal("the source field survives the log round trip")
		}
	}
	data, _ := os.ReadFile(path)
	mustNotContain(t, string(data), `"content"`, "no content is stored")
	mustNotContain(t, string(data), `"body"`, "no rule body is stored")

	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	live := buildRulesReport(stored, ReportOptions{Now: now, Days: 30, Source: "live", CurrentRules: []NamedRule{{ID: "hot-rule", Name: "Hot rule"}}})
	mustEqual(t, len(live.Rows), 0, "replays are not counted as live verdicts")
	mustEqual(t, live.Unheard, []NamedRule{{ID: "hot-rule", Name: "Hot rule"}}, "unheard")
	replays := buildRulesReport(stored, ReportOptions{Now: now, Days: 30, Source: "calibrate"})
	mustEqual(t, len(replays.Rows), len(result.Rows), "and they can be read on their own")
}

func checkResultOf(flagged ...RuleCheck) *RulesCheckResult {
	return &RulesCheckResult{Source: "typesafe", Sources: []string{"pi-warden.md"}, Checked: len(flagged), Results: flagged, Attention: flagged, Requests: 1}
}

var flaggedCheck = RuleCheck{ID: "cold-rule", Name: "Cold rule", Judgeability: "needs_other_files", JudgeabilityScore: 0.8, Mechanical: 0.05, NeedsAttention: true}

func tuneIDs(targets []TuneRule) []string {
	out := []string{}
	for _, t := range targets {
		out = append(out, t.ID)
	}
	sort.Strings(out)
	return out
}

// twin: tests/rules-calibrate.test.ts:392
func TestTuneTargetsFlaggedRulesArriveWithTheirReasons(t *testing.T) {
	judge := calibrateJudge(func(ruleID, _ string) float64 {
		switch ruleID {
		case "hot-rule":
			return 0.95
		case "cold-rule":
			return 0.05
		}
		return 0.4
	})
	calibration := calibrate(manyFilesHistory(21), CalibrateOptions{PlanOptions: PlanOptions{Set: calibrateRuleSet(flagsMD), Config: rulesConfig(), Cwd: t.TempDir(), MaxRequests: 40}, Judge: judge, Timeout: time.Second})
	rules := parseRules(flagsMD)
	// Only noise and indecision tune: the rule with no fire in the sample is not flagged even on its own.
	mustEqual(t, tuneIDs(tuneTargets(TuneInputs{Calibration: &calibration, Rules: rules})), []string{"hot-rule", "mid-rule"}, "calibrate only")
	targets := tuneTargets(TuneInputs{Calibration: &calibration, Check: checkResultOf(flaggedCheck), Rules: rules})
	var cold TuneRule
	for _, target := range targets {
		if target.ID == "cold-rule" {
			cold = target
		}
		if target.ID == "py-rule" {
			t.Fatal("a rule the sample never reached is not flagged")
		}
	}
	mustEqual(t, cold.Reasons, []string{"flagged by the rules check: needs another file to judge (0.80)"}, "no fire in the calibrate sample is not a reason to rewrite")
}

// twin: tests/rules-calibrate.test.ts:407
func TestBuildTunePromptNamesEachFlaggedRule(t *testing.T) {
	prompt := buildTunePrompt([]TuneRule{{ID: "hot-rule", Name: "Hot rule", Body: "Runs hot.", Reasons: []string{"flagged fires on everything in the latest calibrate: 21 applied, 21 fired (100%), mean 0.95"}}})
	mustMatch(t, prompt, "Rewrite the flagged project rules in `pi-warden\\.md` with your file tools", "intro")
	mustMatch(t, prompt, `## Hot rule \(hot-rule\)`, "heading")
	mustMatch(t, prompt, `Flagged: flagged fires on everything in the latest calibrate: 21 applied, 21 fired \(100%\), mean 0\.95\.`, "reason")
	mustMatch(t, prompt, `Current text:\nRuns hot\.`, "text")
	mustMatch(t, prompt, `judgeable from the content of one changed file alone`, "judgeable")
	mustMatch(t, prompt, "keep or add a `paths:` line", "paths")
}

// twin: tests/rules-calibrate.test.ts:417
func TestTuneRequestWithNothingFlaggedSendsNothing(t *testing.T) {
	rules := parseRules(flagsMD)
	untouched := Calibration{Requests: 1, Commits: 1}
	noneYet := tuneRequest(TuneInputs{Rules: rules})
	mustEqual(t, noneYet.Prompt, "", "no prompt yet")
	mustMatch(t, noneYet.Reason, `run /warden rules calibrate or /warden rules check first`, "none yet")
	clean := tuneRequest(TuneInputs{Calibration: &untouched, Check: checkResultOf(), Rules: rules})
	mustEqual(t, clean.Prompt, "", "clean prompt")
	mustMatch(t, clean.Reason, `no rule that needs a rewrite`, "clean")
	// A calibrate sample where the only signal is "no violation in sample" sends nothing.
	quiet := untouched
	quiet.Rows = []RuleCalibration{{ID: "cold-rule", Name: "Cold rule", Applied: 21, FiredRate: 0, MeanViolation: 0.05, Unfired: true}}
	quietResult := tuneRequest(TuneInputs{Calibration: &quiet, Rules: rules})
	mustEqual(t, quietResult.Prompt, "", "quiet prompt")
	mustMatch(t, quietResult.Reason, `no rule that needs a rewrite`, "rules with no fire in the sample alone tune nothing")
	mustMatch(t, tuneRequest(TuneInputs{}).Reason, `no separate rules`, "no rules")
	sent := tuneRequest(TuneInputs{Check: checkResultOf(flaggedCheck), Rules: rules})
	mustEqual(t, sent.Reason, "", "sent reason")
	mustMatch(t, sent.Prompt, `## Cold rule`, "prompt")
}
