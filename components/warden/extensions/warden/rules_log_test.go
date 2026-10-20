package warden

// Twins of tests/rules-log.test.ts (the local rules verdict log and the report over it).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var logNow = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

func logScore(id, name string, violation float64) []RuleScore {
	outcome := "compliant"
	if violation >= 0.7 {
		outcome = "violation"
	}
	return []RuleScore{{ID: id, Name: name, Outcome: outcome, Violation: violation}}
}

func logVerdict(path string, scores []RuleScore) RulesVerdict {
	return RulesVerdict{Source: "typesafe", Path: path, Tool: "write", Sources: []string{"pi-warden.md"}, Asked: len(scores), Scores: scores}
}

func logRecord(mutate func(*RuleRecord)) RuleRecord {
	r := RuleRecord{At: isoTime(logNow), Session: "s1", Path: "src/a.ts", Tool: "write", ID: "r", Name: "Rule", Outcome: "compliant", Violation: 0.1, Threshold: 0.7}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

func at(offsetMs int64) string {
	return isoTime(logNow.Add(time.Duration(offsetMs) * time.Millisecond))
}

func newLog(t *testing.T, name string, maxRecords int) (*RulesLog, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	return NewRulesLog(dir, dir, "s1", RulesLogOptions{Path: path, MaxRecords: maxRecords}), path
}

func mustReadLog(t *testing.T, path string) []RuleRecord {
	t.Helper()
	records, err := readRulesLog(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// twin: tests/rules-log.test.ts:32
func TestARecordHoldsTheJudgmentOnly(t *testing.T) {
	log, path := newLog(t, "shape.jsonl", 0)
	log.Record(logVerdict("src/a.ts", logScore("no-stubs", "No partial implementations", 0.91)), 0.7, logNow, 0)
	log.Flush()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &parsed); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mustEqual(t, keys, []string{"at", "finding", "id", "name", "outcome", "path", "session", "threshold", "tool", "violation"}, "keys")
	mustEqual(t, parsed["name"], "No partial implementations", "name")
	mustEqual(t, parsed["outcome"], "violation", "outcome")
	mustEqual(t, parsed["violation"], 0.91, "violation")
	mustEqual(t, parsed["threshold"], 0.7, "threshold")
	mustEqual(t, parsed["finding"], true, "finding")
	mustEqual(t, strings.Contains(string(data), "content"), false, "the written content is never stored")
}

// twin: tests/rules-log.test.ts:48
func TestTheLogFileIsKeyedByAHashOfTheProjectPath(t *testing.T) {
	dir := t.TempDir()
	a := rulesLogPath("/work/one", dir)
	b := rulesLogPath("/work/two", dir)
	mustEqual(t, rulesLogPath("/work/one", dir), a, "the same project keeps the same file")
	if a == b {
		t.Fatal("two projects share a file")
	}
	mustEqual(t, strings.Contains(a, "/work/one"), false, "the path is not in clear")
	mustEqual(t, strings.HasSuffix(a, ".jsonl"), true, "extension")
}

func clearedOf(items []RuleObservation) []bool {
	out := []bool{}
	for _, i := range items {
		out = append(out, i.Cleared)
	}
	return out
}

// twin: tests/rules-log.test.ts:57
func TestFlagThenFixOnTheSamePathIsClearedOnce(t *testing.T) {
	log, path := newLog(t, "cleared.jsonl", 0)
	flag := log.Record(logVerdict("src/a.ts", logScore("no-stubs", "No partial implementations", 0.91)), 0.7, logNow, 0)
	mustEqual(t, clearedOf(flag), []bool{false}, "flag")
	fix := log.Record(logVerdict("src/a.ts", logScore("no-stubs", "No partial implementations", 0.12)), 0.7, logNow.Add(time.Second), 0)
	mustEqual(t, clearedOf(fix), []bool{true}, "fix")
	log.Flush()
	records := mustReadLog(t, path)
	mustEqual(t, []bool{records[0].Cleared, records[1].Cleared}, []bool{false, true}, "records")
}

// twin: tests/rules-log.test.ts:69
func TestFlagThenFlagAgainIsNotAClear(t *testing.T) {
	tracker := NewRuleClearTracker()
	first := tracker.Observe("src/a.ts", logScore("no-stubs", "No stubs", 0.9), 0.7, 0)
	second := tracker.Observe("src/a.ts", logScore("no-stubs", "No stubs", 0.8), 0.7, 0)
	mustEqual(t, clearedOf(append(first, second...)), []bool{false, false}, "flag then flag")
	other := tracker.Observe("src/b.ts", logScore("no-stubs", "No stubs", 0.1), 0.7, 0)
	mustEqual(t, clearedOf(other), []bool{false}, "a clear on another path does not count")
	back := tracker.Observe("src/a.ts", logScore("no-stubs", "No stubs", 0.1), 0.7, 0)
	mustEqual(t, clearedOf(back), []bool{true}, "back on the first path")
}

// twin: tests/rules-log.test.ts:80
func TestTheLogKeepsTheNewestRecordsPastTheCap(t *testing.T) {
	log, path := newLog(t, "bound.jsonl", 3)
	for i := 1; i <= 5; i++ {
		log.Record(logVerdict("src/a.ts", logScore("rule-"+itoaInt(i), "Rule "+itoaInt(i), float64(i)/10)), 0.7, logNow.Add(time.Duration(i)*time.Millisecond), 0)
	}
	log.Flush()
	records := mustReadLog(t, path)
	ids := []string{}
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	mustEqual(t, ids, []string{"rule-3", "rule-4", "rule-5"}, "newest kept")
	data, _ := os.ReadFile(path)
	mustEqual(t, len(strings.Split(strings.TrimSpace(string(data)), "\n")), 3, "lines")
	mustEqual(t, strings.Join(trimRecords([]string{"a", "b", "c", "d"}, 2), ""), "cd", "trimRecords")
	if rulesLogMaxRecords < 3 {
		t.Fatal("cap")
	}
}

// twin: tests/rules-log.test.ts:94
func TestTheReportGivesEachRuleItsCountsAndFlags(t *testing.T) {
	var records []RuleRecord
	// A: 20 judgments, none fired.
	for i := 0; i < neverFiresMin; i++ {
		records = append(records, logRecord(func(r *RuleRecord) { r.ID, r.Name, r.At, r.Violation = "a", "Never fires", at(int64(-i*1000)), 0.05 }))
	}
	// B: 5 judgments, 4 fired (80%).
	for i := 0; i < 5; i++ {
		records = append(records, logRecord(func(r *RuleRecord) {
			r.ID, r.Name, r.At = "b", "Fires on everything", at(int64(-i*1000))
			r.Violation, r.Finding = 0.9, i != 0
			if i == 0 {
				r.Violation = 0.1
			}
		}))
	}
	// C: 6 judgments, 4 in (0.3, 0.5).
	for i := 0; i < 6; i++ {
		records = append(records, logRecord(func(r *RuleRecord) {
			r.ID, r.Name, r.At = "c", "Undecided", at(int64(-i*1000))
			r.Violation = 0.8
			if i < 4 {
				r.Violation = 0.4
			}
		}))
	}
	// D: an ordinary rule.
	records = append(records, logRecord(func(r *RuleRecord) { r.ID, r.Name, r.Violation = "d", "Ordinary", 0.2 }))
	report := buildRulesReport(records, ReportOptions{Days: 30, Now: logNow.Add(time.Second), CurrentRules: []NamedRule{{ID: "e", Name: "Unheard"}}})
	byID := map[string]RuleReportRow{}
	order := []string{}
	for _, row := range report.Rows {
		byID[row.ID] = row
		order = append(order, row.ID)
	}
	mustEqual(t, byID["a"].Judged, neverFiresMin, "a judged")
	mustEqual(t, byID["a"].Fired, 0, "a fired")
	mustEqual(t, byID["a"].Flags, []string{"never fires"}, "a flags")
	mustEqual(t, byID["b"].Flags, []string{"fires on everything"}, "b flags")
	mustEqual(t, jsRound(byID["b"].FiredRate*100), 80, "b rate")
	mustEqual(t, byID["c"].Flags, []string{"undecided"}, "c flags")
	mustEqual(t, order, []string{"b", "c", "a", "d"}, "worst first")
	mustEqual(t, report.Unheard, []NamedRule{{ID: "e", Name: "Unheard"}}, "unheard")
}

// twin: tests/rules-log.test.ts:117
func TestAClearedFixCountsAndTheMeanIsTheAverage(t *testing.T) {
	records := []RuleRecord{
		logRecord(func(r *RuleRecord) { r.ID, r.Name, r.Violation, r.Finding = "x", "X", 0.9, true }),
		logRecord(func(r *RuleRecord) { r.ID, r.Name, r.Violation, r.Finding, r.Cleared = "x", "X", 0.1, false, true }),
	}
	report := buildRulesReport(records, ReportOptions{Days: 30, Now: logNow.Add(time.Second)})
	mustEqual(t, report.Rows[0].Judged, 2, "judged")
	mustEqual(t, report.Rows[0].Fired, 1, "fired")
	mustEqual(t, report.Rows[0].Cleared, 1, "cleared")
	mustEqual(t, toFixed(report.Rows[0].MeanViolation, 2), "0.50", "mean")
}

// twin: tests/rules-log.test.ts:129
func TestTheReportIgnoresRecordsOutsideTheWindow(t *testing.T) {
	old := logRecord(func(r *RuleRecord) { r.ID, r.Name, r.At = "a", "A", isoTime(logNow.Add(-60*24*time.Hour)) })
	report := buildRulesReport([]RuleRecord{old}, ReportOptions{Days: 30, Now: logNow})
	mustEqual(t, len(report.Rows), 0, "rows")
	mustMatch(t, formatRulesReport(report), `^Rules report: no judgments in the last 30 days\.`, "empty window")
	empty := formatRulesReport(buildRulesReport(nil, ReportOptions{Days: 7, Now: logNow}))
	mustMatch(t, empty, `last 7 days`, "days")
	mustMatch(t, empty, `Local log only; nothing is sent\.`, "footer")
}

// twin: tests/rules-log.test.ts:139
func TestTheSampleReportReadsInShortLines(t *testing.T) {
	var records []RuleRecord
	for i := 0; i < 12; i++ {
		records = append(records, logRecord(func(r *RuleRecord) {
			r.ID, r.Name, r.At = "partial", "No partial implementations", at(int64(-i*1000))
			r.Violation, r.Finding = 0.2, false
			if i < 8 {
				r.Violation, r.Finding = 0.85, true
			}
		}))
	}
	for i := 0; i < neverFiresMin; i++ {
		records = append(records, logRecord(func(r *RuleRecord) {
			r.ID, r.Name, r.At, r.Violation = "secrets", "No hardcoded secrets", at(int64(-i*1000)), 0.04
		}))
	}
	for i := 0; i < 6; i++ {
		records = append(records, logRecord(func(r *RuleRecord) {
			r.ID, r.Name, r.At, r.Violation = "catch", "Errors are not swallowed", at(int64(-i*1000)), 0.4
		}))
	}
	records = append(records, logRecord(func(r *RuleRecord) {
		r.ID, r.Name, r.Violation, r.Finding, r.Cleared = "stdout", "Source modules do not write to stdout", 0.9, true, true
	}))
	text := formatRulesReport(buildRulesReport(records, ReportOptions{Days: 30, Now: logNow.Add(time.Second), CurrentRules: []NamedRule{{ID: "hermetic", Name: "Tests stay hermetic"}}}))
	mustMatch(t, text, `No partial implementations .* fires on everything`, "noisy")
	mustMatch(t, text, `Errors are not swallowed .* undecided`, "undecided")
	mustMatch(t, text, `No hardcoded secrets .* never fires`, "dead")
	mustMatch(t, text, `No records \(1\): Tests stay hermetic\.`, "unheard")
	for _, line := range strings.Split(text, "\n") {
		if utf16Len(line) > 120 {
			t.Fatalf("a line is too long: %q", line)
		}
	}
}

// twin: tests/rules-log.test.ts:154
func TestRowOrderIsStableForEqualFlags(t *testing.T) {
	row := func(id string, fired, judged int) RuleReportRow {
		return RuleReportRow{ID: id, Name: id, Judged: judged, Fired: fired, FiredRate: float64(fired) / float64(judged), MeanViolation: 0.5}
	}
	rows := []RuleReportRow{row("z", 1, 4), row("a", 3, 4), row("b", 2, 4)}
	sort.SliceStable(rows, func(i, j int) bool { return compareRows(rows[i], rows[j]) < 0 })
	mustEqual(t, []string{rows[0].ID, rows[1].ID, rows[2].ID}, []string{"a", "b", "z"}, "order")
}

// twin: tests/rules-log.test.ts:163
func TestASoftFindingBelowTheRulesOwnCutoffIsRecordedSoft(t *testing.T) {
	log, path := newLog(t, "soft.jsonl", 0)
	cutoff := 0.9
	scored := []RuleScore{{ID: "bools", Name: "Boolean names", Outcome: "compliant", Violation: 0.62, Threshold: &cutoff}}
	log.Record(logVerdict("src/a.ts", scored), 0.7, logNow, 0.5)
	log.Flush()
	got := mustReadLog(t, path)[0]
	mustEqual(t, got.Finding, false, "finding")
	mustEqual(t, got.Soft, true, "soft")
	mustEqual(t, got.Threshold, 0.9, "the rule's own cutoff, not the global one")
}

// twin: tests/rules-log.test.ts:175
func TestAMissingLogFileReadsAsNoRecords(t *testing.T) {
	records := mustReadLog(t, filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	mustEqual(t, len(records), 0, "records")
}

// twin: tests/rules-log.test.ts:179
func TestTwoLogHandlesOnOneFileTrimWithoutAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.jsonl")
	first := NewRulesLog(dir, dir, "s1", RulesLogOptions{Path: path, MaxRecords: 4})
	second := NewRulesLog(dir, dir, "s2", RulesLogOptions{Path: path, MaxRecords: 4})
	for i := 0; i < 6; i++ {
		first.Record(logVerdict("src/a.ts", logScore("a"+itoaInt(i), "Rule a"+itoaInt(i), 0.1)), 0.7, logNow.Add(time.Duration(i)*time.Millisecond), 0)
		second.Record(logVerdict("src/a.ts", logScore("b"+itoaInt(i), "Rule b"+itoaInt(i), 0.1)), 0.7, logNow.Add(time.Duration(i)*time.Millisecond), 0)
	}
	first.Flush()
	second.Flush()
	mustEqual(t, first.LastFailure(), "", "first failure")
	mustEqual(t, second.LastFailure(), "", "second failure")
	if len(mustReadLog(t, path)) == 0 {
		t.Fatal("the log is empty")
	}
}

var _ = regexp.MustCompile
