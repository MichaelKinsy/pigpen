package eq

import (
	"reflect"
	"strings"
	"testing"
)

// The Skill's rule "port every upstream test case, or record a named skip" was a promise
// nothing checked. pigpen-websearch wrote its own title ledger and checker; this is the
// shared one: a ledger of upstream test titles, and a check that every title has a Go twin
// (tw) or a named skip (tskip), that no twin names a title the upstream does not have, and
// that skips do not hide behind one blanket reason.

func TestListUpstreamTitles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.test.mjs":    "import test from 'node:test';\ntest('first case', () => {});\n  it(\"second 'quoted' case\", async () => {});\ntest.skip(`third case`, () => {});\n// test('commented out', () => {});\nconst s = \"test('in a string')\";\n",
		"sub/b.test.ts": "describe('group', () => { it('only case', () => {}); });\n",
		"notes.md":      "test('not a test file', () => {})",
	})
	got, err := ListUpstreamTitles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"a":     {"first case", "second 'quoted' case", "third case"},
		"sub/b": {"only case"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %#v\nwant %#v", got, want)
	}
}

const goTwins = `package x

import "testing"

func tw(t *testing.T, file, title string, fn func(*testing.T))          {}
func tskip(t *testing.T, file, title, reason string)                    {}

const f = "a"

func TestA(t *testing.T) {
	tw(t, f, "first case", func(t *testing.T) {})
	tw(t, f, "second 'quoted' case", func(t *testing.T) {})
	tskip(t, f, "third case", "needs a live browser")
	tw(t, "sub/b", ` + "`only case`" + `, func(t *testing.T) {})
}
`

func TestCheckTwinsCountsExactTwinsAndNamedSkips(t *testing.T) {
	ledger := map[string][]string{"a": {"first case", "second 'quoted' case", "third case"}, "sub/b": {"only case"}}
	dir := writeTree(t, map[string]string{"x_test.go": goTwins})
	rep, err := CheckTwins(ledger, dir, TwinOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Twins != 3 || len(rep.Skipped) != 1 || len(rep.Missing) != 0 || len(rep.Unknown) != 0 || !rep.OK() {
		t.Fatalf("%+v", rep)
	}
	if rep.Skipped[0].Title != "third case" || rep.Skipped[0].Reason != "needs a live browser" {
		t.Errorf("skip = %+v", rep.Skipped[0])
	}
	if s := rep.Summary(); !strings.Contains(s, "3 exact twins, 1 skipped") || !strings.Contains(s, "third case") {
		t.Errorf("summary = %q", s)
	}
}

func TestCheckTwinsReportsMissingUnknownAndBlanketReasons(t *testing.T) {
	ledger := map[string][]string{"a": {"one", "two", "three", "four", "five", "six"}}
	src := "package x\nfunc f(t *testing.T) {\n" +
		"\ttw(t, f, \"one\", nil)\n" +
		"\ttw(t, f, \"twoo\", nil)\n" + // misspelt: unknown, and \"two\" is missing
		"\ttskip(t, f, \"three\", \"not applicable\")\n" +
		"\ttskip(t, f, \"four\", \"not applicable\")\n" +
		"\ttskip(t, f, \"five\", \"not applicable\")\n" +
		"\ttskip(t, f, \"six\", \"not applicable\")\n}\n"
	dir := writeTree(t, map[string]string{"x_test.go": src})
	rep, _ := CheckTwins(ledger, dir, TwinOptions{})
	if rep.OK() {
		t.Fatal("a broken ledger passed")
	}
	if !reflect.DeepEqual(rep.Missing, []string{"a: two"}) || !reflect.DeepEqual(rep.Unknown, []string{"twoo"}) {
		t.Errorf("missing %v unknown %v", rep.Missing, rep.Unknown)
	}
	if len(rep.Blanket) != 1 || rep.Blanket[0] != "not applicable" {
		t.Errorf("blanket reasons = %v", rep.Blanket)
	}
	if rep2, _ := CheckTwins(ledger, dir, TwinOptions{MaxSameReason: 4}); len(rep2.Blanket) != 0 {
		t.Errorf("threshold ignored: %v", rep2.Blanket)
	}
}

func TestCheckTwinsRestrictsToShippedFiles(t *testing.T) {
	ledger := map[string][]string{"a": {"one"}, "b": {"unshipped"}}
	dir := writeTree(t, map[string]string{"x_test.go": "package x\nfunc f(t *testing.T) { tw(t, f, \"one\", nil) }\n"})
	if rep, _ := CheckTwins(ledger, dir, TwinOptions{}); rep.OK() {
		t.Error("an unshipped file's titles must be missing when not restricted")
	}
	rep, _ := CheckTwins(ledger, dir, TwinOptions{Files: []string{"a"}})
	if !rep.OK() || rep.Twins != 1 {
		t.Errorf("restricted: %+v", rep)
	}
	if _, err := CheckTwins(ledger, dir, TwinOptions{Files: []string{"nope"}}); err == nil {
		t.Error("an unknown file was accepted")
	}
}

func TestCheckTwinsSameTitleInTwoFilesNeedsTwoTwins(t *testing.T) {
	ledger := map[string][]string{"a": {"works"}, "b": {"works"}}
	dir := writeTree(t, map[string]string{"x_test.go": "package x\nfunc f(t *testing.T) { tw(t, f, \"works\", nil) }\n"})
	rep, _ := CheckTwins(ledger, dir, TwinOptions{})
	if rep.OK() || len(rep.Missing) != 1 {
		t.Errorf("duplicate titles must each have a twin: %+v", rep)
	}
}

func TestCheckTwinsListsComputedTitlesWithoutFailing(t *testing.T) {
	ledger := map[string][]string{"a": {"one"}}
	src := "package x\nfunc f(t *testing.T) {\n\ttw(t, f, \"one\", nil)\n\tfor _, v := range vs { tw(t, f, \"case \"+v, nil) }\n}\n"
	dir := writeTree(t, map[string]string{"x_test.go": src})
	rep, _ := CheckTwins(ledger, dir, TwinOptions{})
	if !rep.OK() || len(rep.Dynamic) != 1 || !strings.Contains(rep.Summary(), "NOTE x_test.go:4") {
		t.Errorf("%+v", rep)
	}
}

// pigpen-pig-snake: the ledger tool read only TypeScript titles, so the 65 cases of PiG's Go
// game tests were mapped by hand. A Go original's cases are its Test functions and literal
// subtests; a port keeps the names (the function is the twin) or names a skip (t.Skip as the
// first statement, or tskip).
func TestListUpstreamTitlesReadsGoTests(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pixel/canvas_test.go": "package pixel\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {\n\tt.Run(\"sub case\", func(t *testing.T) {})\n\tfor _, n := range names { t.Run(n, nil) }\n}\nfunc helper(t *testing.T) {}\nfunc BenchmarkX(b *testing.B) {}\n",
		"a.test.mjs":           "test('js case', () => {});\n",
		// verbatim upstream copies are kept as .go.txt so they are not compiled (Skill, kind 2)
		"upstream/termgame_test.go.txt": "package termgame\nimport \"testing\"\nfunc TestKeys(t *testing.T) {}\n",
	})
	got, err := ListUpstreamTitles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"pixel/canvas": {"TestOne", "TestTwo", "TestTwo/sub case"}, "a": {"js case"}, "upstream/termgame": {"TestKeys"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %#v\nwant %#v", got, want)
	}
}

func TestCheckTwinsAcceptsRetainedGoTestNamesAndT_SkipAsANamedSkip(t *testing.T) {
	ledger := map[string][]string{"canvas": {"TestOne", "TestTwo", "TestTwo/sub case", "TestMouse", "TestFour"}}
	port := writeTree(t, map[string]string{
		"canvas_test.go": "package p\nimport \"testing\"\n" +
			"func TestOne(t *testing.T) {}\n" +
			"func TestTwo(t *testing.T) { t.Run(\"sub case\", func(t *testing.T) {}) }\n" +
			"func TestMouse(t *testing.T) {\n\tt.Skip(\"the port has no mouse input\")\n}\n" +
			"func TestExtra(t *testing.T) {}\n" + // the port's own test: not in the ledger, and not an error
			"func TestNotRenamed(t *testing.T) { tskip(t, f, \"TestFour\", \"moved to another package\") }\n",
	})
	rep, err := CheckTwins(ledger, port, TwinOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || rep.Twins != 3 || len(rep.Skipped) != 2 {
		t.Fatalf("%+v", rep)
	}
	reasons := map[string]string{}
	for _, s := range rep.Skipped {
		reasons[s.Title] = s.Reason
	}
	if reasons["TestMouse"] != "the port has no mouse input" || reasons["TestFour"] != "moved to another package" {
		t.Errorf("skips = %v", reasons)
	}
	// a missing test is still missing
	rep, _ = CheckTwins(map[string][]string{"canvas": {"TestOne", "TestGone"}}, port, TwinOptions{})
	if rep.OK() || !reflect.DeepEqual(rep.Missing, []string{"canvas: TestGone"}) {
		t.Errorf("%+v", rep)
	}
}

// pigpen-websearch deferred 27 whole providers (570 cases): the BLANKET check made it generate a
// synthetic reason per case. A slice map states the deferral once per upstream file, honestly, and
// still leaves the cases counted (as deferred, not as skipped and not as twinned).
func TestCheckTwinsAcceptsADeferredSlicePerFile(t *testing.T) {
	ledger := map[string][]string{
		"providers/exa":    {"exa one", "exa two", "exa three", "exa four", "exa five"},
		"providers/brave":  {"brave one", "brave two"},
		"extraction/fetch": {"fetch one"},
	}
	port := writeTree(t, map[string]string{"x_test.go": "package p\nfunc T(t *testing.T) {\n\ttw(t, \"extraction/fetch\", \"fetch one\", nil)\n\ttw(t, \"providers/brave\", \"brave one\", nil)\n}\n"})
	o := TwinOptions{Deferred: map[string]string{"providers/exa": "Exa provider is a later slice (roadmap row 14)", "providers/brave": "Brave provider is a later slice"}}
	rep, err := CheckTwins(ledger, port, o)
	if err != nil {
		t.Fatal(err)
	}
	// brave one is a twin; brave two is deferred with its file; exa's five are deferred
	if rep.Twins != 2 || rep.Deferred["providers/exa"] != 5 || rep.Deferred["providers/brave"] != 1 || !rep.OK() || len(rep.Skipped) != 0 {
		t.Fatalf("%+v", rep)
	}
	if s := rep.Summary(); !strings.Contains(s, "DEFERRED providers/exa (5 cases): Exa provider is a later slice") {
		t.Errorf("summary hides the deferral:\n%s", s)
	}
	// a case that is neither twinned nor covered by a deferral is still missing
	rep, _ = CheckTwins(ledger, port, TwinOptions{Deferred: map[string]string{"providers/exa": "later"}})
	if rep.OK() || !reflect.DeepEqual(rep.Missing, []string{"providers/brave: brave two"}) {
		t.Errorf("%+v", rep)
	}
	// a slice map that names a file the ledger does not have, or gives no reason, is refused
	for name, d := range map[string]map[string]string{"unknown file": {"nope": "x"}, "no reason": {"providers/exa": " "}} {
		if _, err := CheckTwins(ledger, port, TwinOptions{Deferred: d}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A skipped test is not an exact twin (review of the porter-driver lane). pig-snake keeps upstream names for
// the mechanics it lacks and skips them through a helper, `func TestJump(t *testing.T) { skipUpstream(t, "...") }`;
// `twins check` counted all 28 as exact twins because only a literal t.Skip first statement was a named skip.
// The same holds for twins and subtests declared inside a test function that skips first: they never run.
func TestCheckTwinsCountsASkipHelperAndEverythingInsideASkippedTestAsSkips(t *testing.T) {
	ledger := map[string][]string{"game": {"TestJump", "TestDuck", "TestRuns", "TestRuns/sub", "TestLater", "TestLater/inner", "helper case", "TestPlain"}}
	port := writeTree(t, map[string]string{
		"skip_test.go": "package p\nimport \"testing\"\n" +
			"func skipUpstream(t *testing.T, why string) {\n\tt.Helper()\n\tt.Skip(\"gap: \" + why)\n}\n" +
			"func TestJump(t *testing.T) { skipUpstream(t, \"no jumping\") }\n" +
			"func TestDuck(t *testing.T) {\n\tskipUpstream(t, \"no ducking\")\n}\n" +
			"func TestRuns(t *testing.T) { t.Run(\"sub\", func(t *testing.T) {}) }\n" +
			"func TestLater(t *testing.T) {\n\tt.Skip(\"later slice\")\n\tt.Run(\"inner\", func(t *testing.T) {})\n}\n" +
			"func TestHelperGroup(t *testing.T) {\n\tt.SkipNow()\n\ttw(t, \"game\", \"helper case\", func(t *testing.T) {})\n}\n" +
			"func TestPlain(t *testing.T) { if false { t.Skip(\"only on one OS\") } }\n",
	})
	rep, err := CheckTwins(ledger, port, TwinOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, s := range rep.Skipped {
		reasons[s.Title] = s.Reason
	}
	want := map[string]string{
		"TestJump": "no jumping", "TestDuck": "no ducking",
		"TestLater": "later slice", "TestLater/inner": "later slice",
		"helper case": "<no reason: t.SkipNow>",
	}
	if rep.Twins != 3 || !reflect.DeepEqual(reasons, want) || !rep.OK() {
		t.Fatalf("twins = %d (want 3: TestRuns, TestRuns/sub, TestPlain), skips = %v\nwant %v\n%+v", rep.Twins, reasons, want, rep)
	}
}

// The file argument of tw/tskip is part of the contract ("file is the upstream test file"), but the check
// matched titles only: a twin filed under the wrong upstream file satisfied a case of another file, and a
// file the ledger does not have went unnoticed (review of the porter-driver lane). A literal file argument
// now has to name the ledger file whose case it twins; a computed one (a const) still matches by title.
func TestCheckTwinsMatchesALiteralFileArgument(t *testing.T) {
	ledger := map[string][]string{"a": {"same", "only a"}, "b": {"same"}}
	port := writeTree(t, map[string]string{"x_test.go": "package p\nconst f = \"a\"\nfunc T(t *testing.T) {\n" +
		"\ttw(t, \"a\", \"same\", nil)\n\ttw(t, \"a\", \"same\", nil)\n" + // two twins of a's case, none of b's
		"\ttw(t, f, \"only a\", nil)\n" + // computed file: matched by title
		"\ttskip(t, \"c\", \"same\", \"a file the ledger does not have\")\n}\n"})
	rep, err := CheckTwins(ledger, port, TwinOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Twins != 2 || !reflect.DeepEqual(rep.Missing, []string{"b: same"}) || !reflect.DeepEqual(rep.Unknown, []string{"c: same"}) || rep.OK() {
		t.Fatalf("%+v", rep)
	}
	// restricted to b, a's twins do not stand in for b's case
	rep, _ = CheckTwins(ledger, port, TwinOptions{Files: []string{"b"}})
	if rep.Twins != 0 || !reflect.DeepEqual(rep.Missing, []string{"b: same"}) {
		t.Fatalf("restricted: %+v", rep)
	}
}
