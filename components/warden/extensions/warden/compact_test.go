package warden

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Twins of tests/compact.test.ts, and of the two cases at the end of tests/loops.test.ts (the appendix carries the
// open loops; warden_recall prints the appendix's sections word for word).

func attemptOf(key, call string, failed bool, output string) compactAttempt {
	return compactAttempt{Key: key, Call: call, Failed: failed, Output: output}
}

func mustMatchRe(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("no match for /%s/ in:\n%s", pattern, text)
	}
}

func mustNotMatchRe(t *testing.T, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("unexpected match for /%s/ in:\n%s", pattern, text)
	}
}

// twin: tests/compact.test.ts:13
func TestCompactEmptySnapshotReturnsEmptyString(t *testing.T) {
	if got := compactAppendix(CompactSnapshot{}); got != "" {
		t.Fatalf("got %q", got)
	}
}

// twin: tests/compact.test.ts:17
func TestCompactFullSnapshotProducesABoundedAppendixWithAllSections(t *testing.T) {
	snapshot := CompactSnapshot{
		SavedOutputs: []SavedOutput{{Tool: "bash", Path: "/tmp/pi-warden/out.txt", Bytes: 12000, HasBytes: true}},
		Checks: []CheckEntry{
			{Command: "npm run check", Passed: true, When: "current run"},
			{Command: "npm run test", Passed: false, When: "run 2, #2"},
		},
		Holds: []HeldEntry{
			{Tool: "bash", Preview: "rm -rf /tmp/demo", Outcome: "approved"},
			{Tool: "write", Preview: "src/config.ts", Outcome: "replanned"},
		},
		FailedAttempts: []FailedAttempt{{Call: "npm run build", Error: "error TS2322: Type 'string' is not assignable to type 'number'."}},
		Verification:   &Verification{Passed: true, Command: "npm run check", ChangedSince: false},
		Stuck:          &StuckState{Failures: 3, Window: 8},
		ActiveTask:     "Fix the login redirect",
	}
	text := compactAppendix(snapshot)
	if text == "" || utf16Len(text) > 2000 {
		t.Fatalf("length %d", utf16Len(text))
	}
	for _, want := range []string{
		"=== PI-WARDEN COMPACT EVIDENCE ===", "=== END PI-WARDEN COMPACT EVIDENCE ===", "Evidence warden kept across compaction",
		"### Saved full outputs", "### Last checks", "### Held actions", "### Stuck state", "### Active task", "### Tried and failed", "### Verification",
		"npm run check", "npm run test", "failures: 3 of the last 8 tool results",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("appendix lacks %q", want)
		}
	}
	mustMatchRe(t, text, `bash.*approved`)
	mustMatchRe(t, text, `write.*replanned`)
	mustNotMatchRe(t, text, `undefined|unknown`)
}

func manyHolds(n int) []HeldEntry {
	var holds []HeldEntry
	for i := range n {
		holds = append(holds, HeldEntry{Tool: "bash", Preview: "call-" + itoaInt(i), Outcome: "approved"})
	}
	return holds
}

// twin: tests/compact.test.ts:57
func TestCompactHoldsAreCappedAtTen(t *testing.T) {
	built := buildCompactSnapshot(snapshotInput{Holds: manyHolds(15), Runs: 1})
	if len(built.Holds) != 10 {
		t.Fatalf("holds %d", len(built.Holds))
	}
	if n := strings.Count(compactAppendix(built), "approved"); n != 10 {
		t.Fatalf("approved %d times", n)
	}
}

// twin: tests/compact.test.ts:74
func TestCompactChecksAreCappedAtFive(t *testing.T) {
	var checks []compactCheck
	for i := range 8 {
		checks = append(checks, compactCheck{Command: "cmd-" + itoaInt(i), Passed: i%2 == 0, RunIndex: 1, IndexInRun: i})
	}
	built := buildCompactSnapshot(snapshotInput{Checks: checks, Runs: 1})
	if len(built.Checks) != 5 {
		t.Fatalf("checks %d", len(built.Checks))
	}
	if n := strings.Count(compactAppendix(built), "cmd-"); n != 5 {
		t.Fatalf("cmd- %d times", n)
	}
}

// twin: tests/compact.test.ts:90
func TestCompactRedactsASeededCredentialInTheActiveTask(t *testing.T) {
	text := compactAppendix(CompactSnapshot{ActiveTask: "Set the API key sk-test-1234567890abcdef1234"})
	if strings.Contains(text, "sk-test-1234567890abcdef1234") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("not redacted: %s", text)
	}
}

// twin: tests/compact.test.ts:97
func TestCompactRedactsACredentialInAHeldPreview(t *testing.T) {
	text := compactAppendix(CompactSnapshot{Holds: []HeldEntry{{Tool: "bash", Preview: "TOKEN=ghp_Qk7mZ2pR9vT4xL8nW3sY6bD1cF5hJ0aM", Outcome: "approved"}}})
	if strings.Contains(text, "ghp_Qk7mZ2pR9vT4xL8nW3sY6bD1cF5hJ0aM") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("not redacted: %s", text)
	}
}

// twin: tests/compact.test.ts:107
func TestCompactRedactsACredentialInACheckCommand(t *testing.T) {
	text := compactAppendix(CompactSnapshot{Checks: []CheckEntry{{Command: "curl -H 'Authorization: Bearer sk-live-abcdefghij1234567890abcdef' https://api.example.com", Passed: true, When: "current run"}}})
	if strings.Contains(text, "sk-live-abcdefghij1234567890abcdef") {
		t.Fatalf("not redacted: %s", text)
	}
}

// twin: tests/compact.test.ts:116
func TestCompactRunLabelIsCurrentRunWhenRunsIsOne(t *testing.T) {
	built := buildCompactSnapshot(snapshotInput{Checks: []compactCheck{{Command: "tsc", Passed: true, RunIndex: 1, IndexInRun: 0}}, Runs: 1})
	if built.Checks[0].When != "current run" {
		t.Fatalf("when %q", built.Checks[0].When)
	}
}

// twin: tests/compact.test.ts:124
func TestCompactRunLabelIncludesTheRunIndexWhenRunsAboveOne(t *testing.T) {
	built := buildCompactSnapshot(snapshotInput{Checks: []compactCheck{{Command: "tsc", Passed: false, RunIndex: 2, IndexInRun: 1}}, Runs: 2})
	if built.Checks[0].When != "run 2, #2" {
		t.Fatalf("when %q", built.Checks[0].When)
	}
}

// twin: tests/compact.test.ts:132
func TestCompactStuckStateCountsFailuresInTheAttemptWindow(t *testing.T) {
	built := buildCompactSnapshot(snapshotInput{
		Attempts:   []compactAttempt{attemptOf("a", "npm test", true, "boom"), attemptOf("b", "ls", false, "ok"), attemptOf("a", "npm test", true, "boom")},
		ActiveTask: "test", Runs: 1,
	})
	if built.Stuck == nil || *built.Stuck != (StuckState{Failures: 2, Window: 3}) {
		t.Fatalf("stuck %+v", built.Stuck)
	}
}

// twin: tests/compact.test.ts:141
func TestCompactActiveTaskIsRedacted(t *testing.T) {
	built := buildCompactSnapshot(snapshotInput{ActiveTask: "Use the secret key sk-1234567890abcdef1234", Runs: 1})
	if strings.Contains(built.ActiveTask, "sk-1234567890abcdef1234") {
		t.Fatalf("not redacted: %s", built.ActiveTask)
	}
}

// twin: tests/compact.test.ts:149
func TestCompactAppendixIsCappedAtTwoThousandCharacters(t *testing.T) {
	var holds []HeldEntry
	for range 10 {
		holds = append(holds, HeldEntry{Tool: "bash", Preview: strings.Repeat("a", 100), Outcome: "approved"})
	}
	var checks []CheckEntry
	for i := range 5 {
		checks = append(checks, CheckEntry{Command: strings.Repeat("b", 80), Passed: i%2 == 0, When: "run 1, #" + itoaInt(i+1)})
	}
	text := compactAppendix(CompactSnapshot{
		SavedOutputs: []SavedOutput{{Tool: "bash", Path: "/tmp/out.txt", Bytes: 99999, HasBytes: true}},
		Checks:       checks, Holds: holds, Stuck: &StuckState{Failures: 5, Window: 12}, ActiveTask: strings.Repeat("c", 200),
	})
	if utf16Len(text) > 2000 {
		t.Fatalf("length %d", utf16Len(text))
	}
	if utf16Len(text) == 2000 && !strings.Contains(text, "\u2026 [truncated]") {
		t.Fatal("cut without the marker")
	}
}

// twin: tests/compact.test.ts:168
func TestCompactTriedAndFailedEachDistinctFailedCallOnce(t *testing.T) {
	attempts := []compactAttempt{
		attemptOf("k1", "npm test", true, "FAIL tests/a.test.ts\n  Expected true, got false\n\n1 failing"),
		attemptOf("k2", "git push", true, "fatal: no upstream configured"),
		attemptOf("k3", "cat notes.md", false, "all fine"),
		attemptOf("k1", "npm test", true, "Error: Cannot find module './b.js'\n    at load (node:internal)\n"),
	}
	for _, key := range []string{"k4", "k5", "k6", "k7"} {
		attempts = append(attempts, attemptOf(key, "make "+key, true, key+" exploded"))
	}
	built := buildCompactSnapshot(snapshotInput{Attempts: attempts, Runs: 1})
	var calls []string
	for _, a := range built.FailedAttempts {
		calls = append(calls, a.Call)
	}
	if !slices.Equal(calls, []string{"npm test", "make k4", "make k5", "make k6", "make k7"}) {
		t.Fatalf("calls %v (git push is the oldest of six and drops)", calls)
	}
	if built.FailedAttempts[0].Error != "Error: Cannot find module './b.js'" {
		t.Fatalf("error %q", built.FailedAttempts[0].Error)
	}
	text := compactAppendix(built)
	mustMatchRe(t, text, `### Tried and failed`)
	if strings.Count(text, "npm test") != 1 {
		t.Errorf("not deduplicated by call:\n%s", text)
	}
	mustMatchRe(t, text, `- npm test → Error: Cannot find module '\./b\.js'`)
	if strings.Index(text, "make k4") > strings.Index(text, "make k7") {
		t.Error("not oldest to newest")
	}
	if strings.Contains(text, "cat notes.md") {
		t.Error("a successful call is listed")
	}
}

// twin: tests/compact.test.ts:187
func TestCompactTriedAndFailedEntriesAreAtMost160CharactersAndRedacted(t *testing.T) {
	built := buildCompactSnapshot(snapshotInput{
		Attempts: []compactAttempt{attemptOf("k", "curl -H 'Authorization: Bearer sk-live-abcdefghij1234567890abcdef' "+strings.Repeat("x", 200), true, "[500 earlier chars] …error: "+strings.Repeat("y", 300))},
		Runs:     1,
	})
	text := compactAppendix(built)
	var entry string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- curl") {
			entry = line
			break
		}
	}
	if entry == "" || utf16Len(entry)-2 > 160 {
		t.Fatalf("entry is %d chars: %q", utf16Len(entry)-2, entry)
	}
	if strings.Contains(text, "sk-live-abcdefghij1234567890abcdef") {
		t.Error("not redacted")
	}
	mustNotMatchRe(t, text, `earlier chars`)
}

// twin: tests/compact.test.ts:200
func TestCompactVerificationStates(t *testing.T) {
	verification := func(mutations int, before *int, checks ...Check) string {
		return compactAppendix(buildCompactSnapshot(snapshotInput{Evidence: &RunEvidence{Mutations: mutations, ChecksBeforeMutation: before, Checks: checks}, Runs: 1}))
	}
	i := func(n int) *int { return &n }
	mustMatchRe(t, verification(1, i(0), Check{Call: "npm run check", Passed: true}), `- last passing check: npm run check; code changed since last passing check: no`)
	mustMatchRe(t, verification(1, i(1), Check{Call: "npm test", Passed: true}, Check{Call: "npm run lint", Passed: false}), `- last passing check: npm test; code changed since last passing check: yes`)
	mustMatchRe(t, verification(2, i(1), Check{Call: "npm test", Passed: false}), `- no check has passed yet; code was changed`)
	mustNotMatchRe(t, verification(0, nil, Check{Call: "npm test", Passed: false}), `### Verification`)
}

// twin: tests/compact.test.ts:216 (the compact half; ContextLedger.record/storedOutputs are the context lane's saver twins)
func TestCompactSavedOutputsShowTheRealToolAndByteSize(t *testing.T) {
	saved := []SavedOutput{
		{Tool: "bash", Path: "/tmp/pi-warden-output-a/output.txt", Bytes: 34567, HasBytes: true},
		{Tool: "ctx_execute", Path: "/tmp/pi-warden-output-b/output.txt", Bytes: 12345, HasBytes: true},
	}
	text := compactAppendix(buildCompactSnapshot(snapshotInput{SavedOutputs: saved, Runs: 1}))
	mustMatchRe(t, text, `- bash → /tmp/pi-warden-output-a/output\.txt \(34567 bytes\)`)
	mustMatchRe(t, text, `- ctx_execute → /tmp/pi-warden-output-b/output\.txt \(12345 bytes\)`)
	mustNotMatchRe(t, text, `unknown|\(0 bytes\)`)
}

// twin: tests/compact.test.ts:226 (the compact half, see above)
func TestCompactSavedOutputsRecordedWithoutASourceShowOnlyThePath(t *testing.T) {
	text := compactAppendix(buildCompactSnapshot(snapshotInput{SavedOutputs: []SavedOutput{{Path: "/tmp/pi-warden-output-c/output.txt"}}, Runs: 1}))
	mustMatchRe(t, text, `\n- /tmp/pi-warden-output-c/output\.txt\n`)
	mustNotMatchRe(t, text, `unknown|bytes\)|undefined|→`)
}

// twin: tests/compact.test.ts:235
func TestCompactOverTheCapSavedOutputsShrinkThenHoldsGoAndFailedAttemptsStayWhole(t *testing.T) {
	var attempts []compactAttempt
	for i := range 5 {
		attempts = append(attempts, attemptOf("k"+itoaInt(i), "npm run step-"+itoaInt(i)+" "+strings.Repeat("a", 60), true, "error: step "+itoaInt(i)+" "+strings.Repeat("e", 120)))
	}
	var saved []SavedOutput
	for i := range 8 {
		saved = append(saved, SavedOutput{Tool: "bash", Path: "/tmp/pi-warden-output-" + itoaInt(i) + "/output.txt", Bytes: 1000 + i, HasBytes: true})
	}
	var holds []HeldEntry
	for i := range 10 {
		holds = append(holds, HeldEntry{Tool: "bash", Preview: "rm -rf build-" + itoaInt(i) + " " + strings.Repeat("h", 60), Outcome: "approved"})
	}
	one := 1
	built := buildCompactSnapshot(snapshotInput{
		SavedOutputs: saved, Checks: []compactCheck{{Command: "npm run check", Passed: true, RunIndex: 1}}, Holds: holds, Attempts: attempts,
		Evidence: &RunEvidence{Mutations: 1, ChecksBeforeMutation: &one, Checks: []Check{{Call: "npm run check", Passed: true}}}, ActiveTask: "Fix the build", Runs: 1,
	})
	text := compactAppendix(built)
	if utf16Len(text) > 2000 {
		t.Fatalf("length %d", utf16Len(text))
	}
	mustNotMatchRe(t, text, `\[truncated\]`)
	mustNotMatchRe(t, text, `### Held actions`)
	if n := strings.Count(text, "pi-warden-output-"); n != 3 {
		t.Errorf("%d saved outputs kept", n)
	}
	mustMatchRe(t, text, `pi-warden-output-7`)
	mustNotMatchRe(t, text, `pi-warden-output-4/`)
	for i := range 5 {
		mustMatchRe(t, text, `npm run step-`+itoaInt(i))
	}
	mustMatchRe(t, text, `code changed since last passing check: yes`)
}

// twin: tests/compact.test.ts:257
func TestCompactSampleAppendix(t *testing.T) {
	one := 1
	text := compactAppendix(buildCompactSnapshot(snapshotInput{
		SavedOutputs: []SavedOutput{
			{Tool: "bash", Path: "/tmp/pi-warden-output-a/output.txt", Bytes: 48213, HasBytes: true},
			{Tool: "ctx_execute", Path: "/tmp/pi-warden-output-b/output.txt", Bytes: 20480, HasBytes: true},
		},
		Checks: []compactCheck{{Command: "npm run check", Passed: true, RunIndex: 1}},
		Attempts: []compactAttempt{
			attemptOf("k1", "npm run build", true, "src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.\nFound 1 error."),
			attemptOf("k2", "node scripts/migrate.mjs --dry-run", true, "Error: ENOENT: no such file or directory, open 'data/seed.json'\n    at open (node:fs)"),
			attemptOf("k3", "npm run check", false, "all passed"),
		},
		Evidence:   &RunEvidence{Mutations: 2, ChecksBeforeMutation: &one, Checks: []Check{{Call: "npm run check", Passed: true}}},
		ActiveTask: "Make the migration script read the seed file", Runs: 1,
	}))
	mustMatchRe(t, text, `(?s)### Tried and failed.*npm run build → src/a\.ts\(3,7\): error TS2322.*node scripts/migrate\.mjs --dry-run → Error: ENOENT`)
	mustMatchRe(t, text, `code changed since last passing check: yes`)
	mustMatchRe(t, text, `ctx_execute → /tmp/pi-warden-output-b/output\.txt \(20480 bytes\)`)
}

func loopSnapshot(openLoopsText string) CompactSnapshot {
	one := 1
	return buildCompactSnapshot(snapshotInput{
		SavedOutputs: []SavedOutput{{Tool: "bash", Path: "/tmp/pi-warden-saved/out-1.txt", Bytes: 48000, HasBytes: true}},
		Checks:       []compactCheck{{Command: "npm test", Passed: true, RunIndex: 1}},
		Attempts: []compactAttempt{
			attemptOf("a", "bash: npm run build", true, "src/app.ts(3,1): error TS2304: Cannot find name 'x'.\nFound 1 error."),
			attemptOf("b", "bash: npm test", false, "ok"),
		},
		Evidence:   &RunEvidence{Mutations: 2, ChecksBeforeMutation: &one, Checks: []Check{{Call: "npm test", Passed: true}}},
		ActiveTask: "Fix the build", Runs: 1, OpenLoops: openLoopsText,
	})
}

// twin: tests/loops.test.ts:123
func TestLoopsCompactionAppendixIncludesTheOpenLoops(t *testing.T) {
	loops := formatOpenLoops(addedLoops(t, "Bump the version", "Rerun the flaky test").Loops)
	appendix := compactAppendix(loopSnapshot(loops))
	mustMatchRe(t, appendix, `### Open loops\nPromised earlier in this session and not done yet; close each with warden_loops done or drop\.\n- #1 Bump the version\n- #2 Rerun the flaky test`)
	if strings.Index(appendix, "### Open loops") > strings.Index(appendix, "### Last checks") {
		t.Error("loops come after the sections the cap cuts first")
	}
	mustNotMatchRe(t, compactAppendix(loopSnapshot("")), `Open loops`)
}

// twin: tests/loops.test.ts:131
func TestLoopsRecallPrintsTheAppendixSectionsWordForWord(t *testing.T) {
	snap := loopSnapshot(formatOpenLoops(addedLoops(t, "Bump the version").Loops))
	appendix := compactAppendix(snap)
	recall := recallText(snap)
	sections := strings.Split(recall, "\n\n")
	var heads []string
	for _, s := range sections {
		heads = append(heads, strings.SplitN(s, "\n", 2)[0])
		if !strings.Contains(appendix, s) {
			t.Errorf("appendix lacks: %s", s)
		}
	}
	if !slices.Equal(heads, []string{"### Tried and failed", "### Verification", "### Saved full outputs"}) {
		t.Fatalf("sections %v", heads)
	}
	mustMatchRe(t, recall, `- bash: npm run build → src/app\.ts\(3,1\): error TS2304: Cannot find name 'x'\.`)
	mustMatchRe(t, recall, `last passing check: npm test; code changed since last passing check: yes`)
	mustMatchRe(t, recall, `bash → /tmp/pi-warden-saved/out-1\.txt \(48000 bytes\)`)
	mustNotMatchRe(t, recall, `Open loops|Active task|Last checks`)
	if got := recallText(buildCompactSnapshot(snapshotInput{Runs: 1})); got != "Nothing recorded yet in this session: no failed attempt, no check, and no saved output." {
		t.Fatalf("empty recall %q", got)
	}
}

// The error line of a failed output is the last specific one (compact.ts errorLine), not the first.
func TestCompactErrorLineIsTheLastSpecificErrorLine(t *testing.T) {
	for output, want := range map[string]string{
		"error: first\nnoise\nerror: second\nFound 2 errors.": "error: second",
		"plain first line\nplain last line":                   "plain last line",
		"":                                                    "",
		"-----\n=====":                                        "",
	} {
		if got := compactErrorLine(output); got != want {
			t.Errorf("compactErrorLine(%q) = %q, want %q", output, got, want)
		}
	}
}
