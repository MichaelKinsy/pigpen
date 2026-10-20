package warden

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Twins of tests/excerpt.test.ts.

func excNoise(n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("progress step %d complete", i)
	}
	return strings.Join(out, "\n")
}

func mustExcerpt(t *testing.T, text string, format OutputFormat) string {
	t.Helper()
	out, ok := FormatExcerpt(text, format)
	if !ok {
		t.Fatalf("%s: no excerpt", format)
	}
	return out
}

func joinLines(l ...string) string { return strings.Join(l, "\n") }

// twin: tests/excerpt.test.ts:8
func TestFormatQuestionOffersEveryParserAndAnExplicitOther(t *testing.T) {
	var options []string
	for k := range FormatQuestion().Criteria {
		options = append(options, k)
	}
	sort.Strings(options)
	mustEqual(t, options, []string{"eslint", "git_diff", "git_log", "node_test", "npm_install", "other", "pytest", "tsc", "vitest_jest"}, "options")
	if _, ok := FormatExcerpt("anything", FormatOther); ok {
		t.Error("other has no parser")
	}
}

// twin: tests/excerpt.test.ts:14
func TestVitestAndJestExcerptsKeepFailingTestsAndTheSummary(t *testing.T) {
	vitest := joinLines(excNoise(200), " ✓ tests/b.test.ts (3 tests) 5ms", " ❯ tests/a.test.ts (2 tests | 1 failed) 12ms", "   ✓ subtracts", "   × adds numbers", "     → expected 3 to be 4", "", " Test Files  1 failed | 1 passed (2)", "      Tests  1 failed | 4 passed (5)", "   Duration  1.20s")
	out := mustExcerpt(t, vitest, FormatVitestJest)
	mustMatch(t, out, `× adds numbers`, "failing test")
	mustMatch(t, out, `expected 3 to be 4`, "assertion")
	mustMatch(t, out, `Tests {2}1 failed \| 4 passed`, "summary")
	mustNotContain(t, out, "progress step 5 complete", "noise")
	mustNotContain(t, out, "✓ subtracts", "passing")
	jest := joinLines("PASS src/a.test.ts", "FAIL src/b.test.ts", "  ● Calculator › adds", "", "    expect(received).toBe(expected)", "    Expected: 4", "    Received: 3", "      at Object.<anonymous> (src/b.test.ts:9:20)", "", "Test Suites: 1 failed, 1 passed, 2 total", "Tests:       1 failed, 3 passed, 4 total")
	jestOut := mustExcerpt(t, jest, FormatVitestJest)
	mustMatch(t, jestOut, `● Calculator › adds`, "jest failure")
	mustMatch(t, jestOut, `Expected: 4`, "expected")
	mustMatch(t, jestOut, `Tests: {7}1 failed`, "summary")
	if _, ok := FormatExcerpt(excNoise(50), FormatVitestJest); ok {
		t.Error("no markers, no parser")
	}
}

// twin: tests/excerpt.test.ts:30
func TestNodeTestExcerptsWorkForTapAndTheSpecReporter(t *testing.T) {
	tap := joinLines("TAP version 13", "# Subtest: adds", "ok 1 - adds", "# Subtest: fails", "not ok 2 - fails", "  ---", "  error: 'expected 1 to equal 2'", "  code: 'ERR_ASSERTION'", "  ...", "1..2", "# tests 2", "# pass 1", "# fail 1")
	out := mustExcerpt(t, tap, FormatNodeTest)
	mustMatch(t, out, `not ok 2 - fails`, "failing")
	mustMatch(t, out, `expected 1 to equal 2`, "assertion")
	mustMatch(t, out, `# fail 1`, "summary")
	mustNotContain(t, out, "ok 1 - adds", "passing")
	spec := joinLines("✔ adds (1.2ms)", "✖ fails (0.8ms)", "  AssertionError [ERR_ASSERTION]: expected 1 to equal 2", "      at TestContext.<anonymous> (file:///t.js:4:10)", "ℹ tests 2", "ℹ pass 1", "ℹ fail 1")
	specOut := mustExcerpt(t, spec, FormatNodeTest)
	mustMatch(t, specOut, `✖ fails`, "spec failing")
	mustMatch(t, specOut, `ℹ fail 1`, "spec summary")
}

// twin: tests/excerpt.test.ts:43
func TestTscEslintAndPytestExcerptsKeepLocationsAndMessages(t *testing.T) {
	tsc := joinLines(excNoise(30), "src/a.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.", "src/b.ts:3:1 - error TS1005: ';' expected.", "", "Found 2 errors in 2 files.", "", "Errors  Files", "     1  src/a.ts:12")
	tscOut := mustExcerpt(t, tsc, FormatTsc)
	mustMatch(t, tscOut, `src/a\.ts\(12,5\): error TS2322`, "paren location")
	mustMatch(t, tscOut, `src/b\.ts:3:1 - error TS1005`, "colon location")
	mustMatch(t, tscOut, `Found 2 errors`, "found")
	eslint := joinLines("", "/repo/src/a.ts", "  10:3  error    'x' is assigned a value but never used  no-unused-vars", "  12:1  warning  Unexpected console statement            no-console", "", "/repo/src/clean.ts", "", "✖ 2 problems (1 error, 1 warning)", "")
	eslintOut := mustExcerpt(t, eslint, FormatEslint)
	mustMatch(t, eslintOut, `/repo/src/a\.ts\n {2}10:3 {2}error`, "file then problem")
	mustMatch(t, eslintOut, `✖ 2 problems`, "total")
	mustNotContain(t, eslintOut, "clean.ts", "files without problems are dropped")
	pytest := joinLines("============================= test session starts ==============================", "collected 3 items", "", "tests/test_a.py ..F                                                       [100%]", "", "=================================== FAILURES ===================================", "__________________________________ test_add ____________________________________", "", "    def test_add():", ">       assert add(1, 2) == 4", "E       assert 3 == 4", "E        +  where 3 = add(1, 2)", "", "tests/test_a.py:7: AssertionError", "=========================== short test summary info ============================", "FAILED tests/test_a.py::test_add - assert 3 == 4", "========================= 1 failed, 2 passed in 0.05s ==========================")
	pyOut := mustExcerpt(t, pytest, FormatPytest)
	mustMatch(t, pyOut, `_+ test_add _+`, "header")
	mustMatch(t, pyOut, `E {7}assert 3 == 4`, "assertion")
	mustMatch(t, pyOut, `FAILED tests/test_a\.py::test_add`, "summary line")
	mustMatch(t, pyOut, `1 failed, 2 passed`, "summary")
	mustNotContain(t, pyOut, "collected 3 items", "noise")
}

// twin: tests/excerpt.test.ts:63
func TestGitAndNpmExcerptsReduceToFileCountsCommitSubjectsAndNotices(t *testing.T) {
	diff := joinLines("diff --git a/src/a.ts b/src/a.ts", "index 1..2 100644", "--- a/src/a.ts", "+++ b/src/a.ts", "@@ -1,3 +1,4 @@", " const a = 1;", "-const b = 2;", "+const b = 3;", "+const c = 4;", "diff --git a/src/new.ts b/src/new.ts", "new file mode 100644", "--- /dev/null", "+++ b/src/new.ts", "@@ -0,0 +1,2 @@", "+export {};", "+// new")
	diffOut := mustExcerpt(t, diff, FormatGitDiff)
	mustMatch(t, diffOut, `src/a\.ts {2}\+2 -1`, "a.ts")
	mustMatch(t, diffOut, `src/new\.ts {2}\+2 -0 {2}\(new file mode 100644\)`, "new.ts")
	mustMatch(t, diffOut, `2 files changed, \+4 -1`, "total")
	mustNotContain(t, diffOut, "const c = 4", "hunk text")
	log := joinLines("commit 0123456789abcdef0123456789abcdef01234567", "Author: A <a@example.invalid>", "Date:   Mon Jan 1 00:00:00 2024 +0000", "", "    Fix the parser", "", "    Longer body text that is not needed.", "", "commit 89abcdef0123456789abcdef0123456789abcdef", "Author: B <b@example.invalid>", "", "    Add tests")
	logOut := mustExcerpt(t, log, FormatGitLog)
	mustMatch(t, logOut, `0123456789ab {2}Fix the parser`, "first")
	mustMatch(t, logOut, `89abcdef0123 {2}Add tests`, "second")
	mustNotContain(t, logOut, "Longer body", "body")
	mustNotContain(t, logOut, "Author", "author")
	status := joinLines("On branch main", "Changes not staged for commit:", "  (use \"git add <file>...\" to update what will be committed)", "", "\tmodified:   src/a.ts", "", "Untracked files:", "\tnew.txt", "")
	mustMatch(t, mustExcerpt(t, status, FormatGitLog), `modified: {3}src/a\.ts`, "status")
	npm := joinLines(excNoise(20), "npm warn deprecated inflight@1.0.6: This module is not supported", "", "added 412 packages, and audited 413 packages in 9s", "", "62 packages are looking for funding", "", "2 moderate severity vulnerabilities", "", "To address all issues, run:", "  npm audit fix")
	npmOut := mustExcerpt(t, npm, FormatNpmInstall)
	mustMatch(t, npmOut, `npm warn deprecated inflight`, "deprecated")
	mustMatch(t, npmOut, `added 412 packages`, "added")
	mustMatch(t, npmOut, `2 moderate severity vulnerabilities`, "vulns")
	mustNotContain(t, npmOut, "progress step", "noise")
	if _, ok := FormatExcerpt(excNoise(20), FormatNpmInstall); ok {
		t.Error("no markers, no parser")
	}
}

// twin: tests/excerpt.test.ts:87
func TestExcerptsAreCappedAndAlwaysEndWithTheLastNonEmptyLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "src/f%d.ts(1,1): error TS2322: Type 'a' is not assignable to type 'b'.\n", i)
	}
	b.WriteString("Found 500 errors in 500 files.")
	out := mustExcerpt(t, b.String(), FormatTsc)
	if utf16Len(out) > 6200 {
		t.Errorf("length %d", utf16Len(out))
	}
	mustMatch(t, out, `omitted for size`, "cap note")
	mustMatch(t, out, `Found 500 errors in 500 files\.$`, "final status")
	for _, f := range []OutputFormat{FormatVitestJest, FormatNodeTest, FormatTsc, FormatEslint, FormatPytest, FormatGitDiff, FormatGitLog, FormatNpmInstall} {
		if _, ok := FormatExcerpt("", f); ok {
			t.Errorf("%s on empty text", f)
		}
	}
}
