package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Twins of tests/stuck-evidence.test.ts.

func evBash(command, output string, failed bool) Attempt {
	return MakeAttempt("bash", map[string]any{"command": command}, text(output), failed)
}

func evEdit(path, oldText, newText string) Attempt {
	return MakeAttempt("edit", map[string]any{"path": path, "edits": []any{map[string]any{"oldText": oldText, "newText": newText}}}, text("ok"), false)
}

func evRunNs(e StuckEvidence, f func(EvidenceRun) int) []int {
	out := []int{}
	for _, r := range e.Runs {
		out = append(out, f(r))
	}
	return out
}

// twin: tests/stuck-evidence.test.ts:20
func TestBuildStuckRequestWithoutEvidenceIsExactlyTheStateTheGuardSentBefore(t *testing.T) {
	attempts := []Attempt{evBash("npm test", "not ok 1 - alpha\n  error: boom", true), evEdit("src/a.ts", "const x = 1;", "const x = 2;")}
	req := BuildStuckRequest(attempts, "fix the tests", false)
	list := []map[string]any{}
	for i, a := range attempts {
		outcome := "ok"
		if a.Failed {
			outcome = "failed"
		}
		list = append(list, map[string]any{"n": i + 1, "tool": a.Tool, "call": a.Call, "outcome": outcome, "output": a.Output})
	}
	mustEqual(t, req.State, map[string]any{"task": "fix the tests", "attempts": list}, "state without evidence")
	if _, ok := req.State["evidence"]; ok {
		t.Error("evidence key present")
	}
	on := BuildStuckRequest(attempts, "fix the tests", true)
	if on.State["evidence"] == nil {
		t.Error("evidence is on by default")
	}
	if !DefaultConfig().Stuck.Evidence {
		t.Error("the shipped default is on")
	}
}

var evSamples = []struct{ format, output, name string }{
	{"tap", "TAP version 13\n# Subtest: week start\nnot ok 1 - weekStart maps Sunday to the previous Monday\n  ---\n  duration_ms: 3.1\n  location: '/home/dev/proj/src/week.test.js:8:3'\n  error: |-\n    expected 2024-03-03 to equal 2024-03-04\n  code: 'ERR_ASSERTION'\n  ...\n1..1\n# tests 1\n# pass 0\n# fail 1\n", "weekStart maps Sunday to the previous Monday"},
	{"jest", "FAIL src/cart.test.ts\n  ● cart › applies the bulk discount at 10 items\n\n    expect(received).toBe(expected)\n\n    Expected: 90\n    Received: 100\n\n      at Object.<anonymous> (src/cart.test.ts:42:5)\n\nTest Suites: 1 failed, 1 total\nTests:       1 failed, 1 total\n", "cart › applies the bulk discount at 10 items"},
	{"vitest", " FAIL  src/slug.test.ts > slugify > strips diacritics\nAssertionError: expected 'cafe' to be 'café'\n\n ❯ src/slug.test.ts:12:20\n\n      Tests  1 failed | 3 passed\n", "src/slug.test.ts > slugify > strips diacritics"},
	{"pytest", "=================== short test summary info ====================\nFAILED tests/test_invoice.py::test_invoice_due_date - AssertionError: assert '2024-02-01' == '2024-03-01'\n================= 1 failed, 3 passed in 0.42s =================\n", "tests/test_invoice.py::test_invoice_due_date"},
	{"cargo", "running 3 tests\ntest tests::split_remainder ... FAILED\n\ntest result: FAILED. 2 passed; 1 failed; finished in 0.03s\n\n---- tests::split_remainder stdout ----\nthread 'tests::split_remainder' panicked at src/lib.rs:88:9:\ncalled `Option::unwrap()` on a `None` value\nnote: run with `RUST_BACKTRACE=1`\n", "tests::split_remainder"},
	{"go", "--- FAIL: TestNormalizeSKU (0.00s)\n    --- FAIL: TestNormalizeSKU/whitespace (0.00s)\n        sku_test.go:44: got \"AB\", want \"A B\"\nFAIL\nFAIL\texample.com/inv\t0.012s\n", "TestNormalizeSKU/whitespace"},
	{"test-script", "FAIL split-spaces: got 2 tokens, want 3\nFAIL empty-input: got 1 token, want 0\n1 passed, 2 failed\n", "split-spaces"},
	{"playwright", "  1) [chromium] › tests/checkout.spec.ts:8:5 › checkout › completes checkout ──────\n\n    Error: expect(received).toHaveText(expected)\n\n    Expected string: \"Order placed\"\n    Received string: \"Order failed\"\n\n      at tests/checkout.spec.ts:14:5\n\n  1 failed\n  1 passed (2.4s)\n", "[chromium] tests/checkout.spec.ts:8:5 › checkout › completes checkout"},
}

// twin: tests/stuck-evidence.test.ts:45
func TestTheParserNamesTheFailingTestInOneSampleOfEachRunnerFormat(t *testing.T) {
	for _, s := range evSamples {
		p := ParseFailure(s.output)
		if p.Format != s.format {
			t.Errorf("%s: format %s %+v", s.format, p.Format, p)
		}
		if !contains(p.Failing, s.name) {
			t.Errorf("%s: failing %q", s.format, p.Failing)
		}
	}
}

// twin: tests/stuck-evidence.test.ts:53
func TestTscEslintAndALispConditionAreParsedToo(t *testing.T) {
	tsc := ParseFailure("src/order.ts(42,5): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.\n")
	if tsc.Format != "tsc" || tsc.Errors[0] != "TS2345 src/order.ts: Argument of type 'string' is not assignable to parameter of type 'number'." || tsc.Location != "src/order.ts:42:5" {
		t.Errorf("tsc %+v", tsc)
	}
	eslint := ParseFailure("/home/dev/proj/src/retry.js\n  12:7  error  'attempt' is assigned a value but never used  no-unused-vars\n\n✖ 1 problem (1 error, 0 warnings)\n")
	if eslint.Format != "eslint" || len(eslint.Errors) == 0 || !strings.Contains(eslint.Errors[0], "no-unused-vars") {
		t.Errorf("eslint %+v", eslint)
	}
	lisp := ParseFailure("Unhandled SIMPLE-ERROR in thread main:\n  Couldn't load \"src/util.lisp\"\n")
	if lisp.Format != "lisp-condition" {
		t.Fatalf("lisp %+v", lisp)
	}
	mustMatch(t, lisp.Errors[0], `Couldn't load`, "lisp error")
}

// twin: tests/stuck-evidence.test.ts:66
func TestAnOutputNoParserKnowsFallsBackToTheHeadAndTailOfTheRun(t *testing.T) {
	output := strings.Repeat("step ", 200) + "\nthe widget exploded in an unexpected way\n" + strings.Repeat("more ", 200)
	if ParseFailure("just some words\nand more words").Format != "unparsed" {
		t.Error("unparsed")
	}
	e := BuildStuckEvidence([]Attempt{evBash("./run-thing", output, true), evBash("./run-thing", output, true)}, EvidenceLimit)
	for _, r := range e.Runs {
		if r.Parser != "" {
			t.Errorf("parser %q", r.Parser)
		}
		mustMatch(t, r.OutputHeadTail, `^step step`, "head")
		mustMatch(t, r.OutputHeadTail, `more more$`, "tail")
		mustMatch(t, r.OutputHeadTail, `middle chars`, "middle")
		if utf16Len(r.OutputHeadTail) >= utf16Len(output) {
			t.Error("not shorter")
		}
	}
}

// twin: tests/stuck-evidence.test.ts:79
func TestAChangedFailureIsADifferentSignatureAndTheDigestCountsBoth(t *testing.T) {
	first := "not ok 1 - alpha\n  error: expected 1 to equal 2\nnot ok 2 - beta\n  error: boom\n"
	second := "not ok 1 - alpha\n  error: expected 1 to equal 3\nnot ok 2 - beta\n  error: boom\n"
	e := BuildStuckEvidence([]Attempt{evBash("npm test", first, true), evBash("npm test", second, true)}, EvidenceLimit)
	if e.Digest.FailedRuns != 2 || e.Digest.DistinctFailures != 2 || e.Digest.LatestFailureSeen {
		t.Errorf("%+v", e.Digest)
	}
	for _, r := range e.Runs {
		if r.SameFailureAsRun.n != 0 || !r.SameFailureAsRun.set {
			t.Errorf("same_failure_as_run %+v", r.SameFailureAsRun)
		}
	}
}

// twin: tests/stuck-evidence.test.ts:89
func TestLineNumbersTimestampsTempPathsAndOrderingAreNoiseNotAChangedFailure(t *testing.T) {
	mustEqual(t, NormaliseFailureText("at src/a.test.js:8:3 in 812ms (12345)"), "at src/a.test.js:# in #t (#)", "positions")
	mustEqual(t, NormaliseFailureText("/tmp/proj/x.log"), "<tmp>", "tmp")
	mustEqual(t, NormaliseFailureText("2026-09-26T10:00:01 build"), "<time> build", "time")
	outputs := []string{
		"not ok 1 - alpha\n  error: expected 1 to equal 2\n  location: '/home/dev/proj/src/a.test.js:8:3'\nnot ok 2 - beta\n  error: boom\n",
		"not ok 1 - beta\n  error: boom\n  location: '/home/dev/proj/src/b.test.js:9:4'\nnot ok 2 - alpha\n  error: expected 1 to equal 2\n  location: '/home/dev/proj/src/b.test.js:31:7'\n",
		"not ok 1 - alpha\n  error: expected 1 to equal 2\n  location: '/tmp/pytest-of-dev/pytest-91/a.test.js:19:2'\n  duration_ms: 812.4\nnot ok 2 - beta\n  error: boom\n  location: '/tmp/pytest-of-dev/pytest-91/a.test.js:44:1'\n",
	}
	sigs := map[string]bool{}
	var attempts []Attempt
	for _, o := range outputs {
		sigs[FailureSignature(ParseFailure(o), o)] = true
		attempts = append(attempts, evBash("npm test", o, true))
	}
	if len(sigs) != 1 {
		t.Errorf("signatures %v", sigs)
	}
	e := BuildStuckEvidence(attempts, EvidenceLimit)
	if e.Digest.FailedRuns != 3 || e.Digest.DistinctFailures != 1 || !e.Digest.LatestFailureSeen {
		t.Errorf("%+v", e.Digest)
	}
	mustEqual(t, evRunNs(e, func(r EvidenceRun) int { return r.SameFailureAsRun.n }), []int{0, 1, 1}, "same_failure_as_run")
	mustEqual(t, e.Digest.SameCommandRuns, []int{2, 3}, "same command")
}

// twin: tests/stuck-evidence.test.ts:108
func TestATokenInAnOutputOrADiffNeverReachesTheState(t *testing.T) {
	secret := "sk-live0123456789abcdefXYZ"
	attempts := []Attempt{
		evBash("API_KEY="+secret+" ./deploy.sh", "Error: token "+secret+" rejected\n  at /home/dev/deploy.js:4:9\n", true),
		evEdit("src/keys.ts", "const key = 1;", `const key = "`+secret+`";`),
		evBash("API_KEY="+secret+" ./deploy.sh", "Error: token "+secret+" rejected\n  at /home/dev/deploy.js:9:2\n", true),
	}
	raw, _ := json.Marshal(BuildStuckRequest(attempts, "rotate the deploy key", true).State)
	mustNotContain(t, string(raw), secret, "state")
	mustMatch(t, string(raw), `\[redacted\]`, "state")
}

// twin: tests/stuck-evidence.test.ts:120
func TestTheEvidenceObjectHoldsItsSizeCapByDroppingTheOldestRunsFirst(t *testing.T) {
	var attempts []Attempt
	for i := range 12 {
		attempts = append(attempts, evBash("npm test -- "+strconv.Itoa(i), strings.Repeat("filler "+strconv.Itoa(i)+" ", 4000)+"\nthe widget exploded\n", true))
	}
	e := BuildStuckEvidence(attempts, EvidenceLimit)
	if n := len(marshalPlain(e)); n > EvidenceLimit {
		t.Errorf("%d bytes", n)
	}
	if len(e.Runs) >= 12 || e.Runs[len(e.Runs)-1].N != 12 || e.Runs[0].N <= 1 {
		t.Errorf("runs %d first %d last %d", len(e.Runs), e.Runs[0].N, e.Runs[len(e.Runs)-1].N)
	}
	if e.Digest.FailedRuns != len(e.Runs) || len(e.Digest.SameCommandRuns) != 0 {
		t.Errorf("digest %+v", e.Digest)
	}
}

// twin: tests/stuck-evidence.test.ts:133
func TestOverTheCapWithOneRunLeftTheOldestEditsGo(t *testing.T) {
	attempts := []Attempt{evBash("npm test", "the widget exploded\n", true)}
	for i := range 6 {
		attempts = append(attempts, evEdit("src/file-"+strconv.Itoa(i)+".ts", strings.Repeat("old\n", 400), strings.Repeat("new\n", 400)))
	}
	e := BuildStuckEvidence(attempts, EvidenceLimit)
	if len(marshalPlain(e)) > EvidenceLimit || len(e.Runs) != 1 || e.Edits[len(e.Edits)-1].N != 7 || len(e.Edits) >= 6 {
		t.Errorf("runs %d edits %d", len(e.Runs), len(e.Edits))
	}
}

// twin: tests/stuck-evidence.test.ts:143
func TestEditDiffReportsThePathTheLineCountsAndACappedDiff(t *testing.T) {
	d := MakeEditDiff("edit", map[string]any{"path": "src/a.ts", "edits": []any{map[string]any{"oldText": "one\ntwo\nthree", "newText": "one\nTWO\nthree"}}})
	mustEqual(t, d.Path, "src/a.ts", "path")
	mustEqual(t, d.RemovedLines, []string{"two"}, "removed")
	mustEqual(t, d.AddedLines, []string{"TWO"}, "added")
	mustEqual(t, d.Diff, "-two\n+TWO", "diff")
	w := MakeEditDiff("write", map[string]any{"path": "src/b.ts", "content": "line"})
	if w.Added != 1 || w.Removed != 0 || w.Diff != "+line" {
		t.Errorf("write %+v", w)
	}
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = strings.Repeat("x", 20)
	}
	long := MakeEditDiff("write", map[string]any{"path": "src/c.ts", "content": strings.Join(lines, "\n")})
	if utf16Len(long.Diff) >= 700 || !strings.HasSuffix(long.Diff, "[diff capped]") {
		t.Errorf("long %d", utf16Len(long.Diff))
	}
	a := evEdit("src/a.ts", "one\ntwo", "one\nTWO")
	if !reflect.DeepEqual(a.Change, &AttemptChange{Path: "src/a.ts", Added: 1, Removed: 1, Diff: "-two\n+TWO"}) {
		t.Errorf("change %+v", a.Change)
	}
	if evBash("ls", "ok", false).Change != nil {
		t.Error("a bash call has no change")
	}
}

// The config case of the stuck-evidence tests, "stuck.evidence is a boolean, defaults on, settable from the
// user file). Its project-file and junk-value legs belong to the config loader (configui lane), so the upstream
// case itself is a named skip.
func TestStuckEvidenceDefaultsOnAndTheUserFileTurnsItOff(t *testing.T) {
	if !DefaultConfig().Stuck.Evidence {
		t.Fatal("default on")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"stuck":{"evidence":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || cfg.Stuck.Evidence {
		t.Errorf("file turns it off: %v %+v", err, cfg.Stuck)
	}
	if cfg.Stuck.DiffLimit != 3000 || cfg.Stuck.TailLimit != 1000 {
		t.Errorf("limits %+v", cfg.Stuck)
	}
}

// twin: tests/stuck-evidence.test.ts:185
func TestEvaluateStuckSendsTheEvidenceTheConfigAsksFor(t *testing.T) {
	for _, evidence := range []bool{true, false} {
		j := stuckJudge(0.9, 1, 0.1)
		c := stuckConfig()
		c.Evidence = evidence
		w := NewAttemptWindow(c.Window)
		for i := range 3 {
			push(w, "bash", cmd("npm test"), "not ok 1 - alpha "+strconv.Itoa(i)+"\n  error: boom\n", true)
		}
		v := evalStuck(w, "fix the tests", c, j)
		if !v.Stuck {
			t.Fatalf("stuck %+v", v)
		}
		calls := j.calls()
		if len(calls) != 1 {
			t.Fatalf("calls %d", len(calls))
		}
		ev, has := calls[0].State["evidence"]
		if has != evidence {
			t.Errorf("evidence %v has %v", evidence, has)
		}
		if evidence && ev.(StuckEvidence).Digest.FailedRuns != 3 {
			t.Errorf("digest %+v", ev)
		}
	}
}
