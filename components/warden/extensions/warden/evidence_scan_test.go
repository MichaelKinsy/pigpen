package warden

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The hand-written scanners of evidence_scan.go replace regular
// expressions. The expressions they replaced are kept here as the reference, and every case below compares the
// two over generated inputs built from the characters that make the expressions differ: each kind of whitespace
// (Go's `\s` has no \v; the \u00a0 and \u2003 of JavaScript are not whitespace to it), blank lines (a leading
// `\s+` runs through them), partial and doubled literals, and non-ASCII and invalid UTF-8.
var (
	refGoFail     = &lazyRegexp{expr: `(?m)^\s*--- FAIL: (\S+)`}
	refGoBuild    = &lazyRegexp{expr: `(?m)^(\S+\.go):(\d+):(\d+): (.+)$`}
	refGoDetail   = &lazyRegexp{expr: `(?m)^\s+(\S+_test\.go:\d+): (.+)$`}
	refGoPackage  = &lazyRegexp{expr: `(?m)^FAIL\t(\S+)(?:\t| \[)`}
	refTapFail    = &lazyRegexp{expr: `(?m)^# fail (\d+)$`}
	refTapTests   = &lazyRegexp{expr: `(?m)^# tests (\d+)$`}
	refEvExitCode = &lazyRegexp{expr: `(?im)(?:exited with code|exit code:?|exit status|Error)\s+(\d+)\s*$`}
)

// genText joins up to maxPieces random pieces of a vocabulary.
func genText(r *rand.Rand, vocab []string, maxPieces int) string {
	n := r.IntN(maxPieces) + 1
	var b strings.Builder
	for range n {
		b.WriteString(vocab[r.IntN(len(vocab))])
	}
	return b.String()
}

var scanVocab = []string{
	"--- FAIL: ", "--- FAIL:", "---  FAIL: ", "--- fail: ", "    --- FAIL: ", "\t--- FAIL: ", "TestA", "TestA/b", "FAIL\t", "FAIL ", "FAIL", "pkg/foo", "example.com/x", "\t", " [", "[build failed]", "]", " ",
	"a.go", "x_test.go", "_test.go", "util_test.go", ".go", ":", "1", "23", "456", "7:8", ":9:10: ", ": ", "boom", "msg here",
	"\n", "\n", "\n\n", "\r", "\r\n", "\v", "\f", "\u00a0", "\u2003", "é", "\xff", "日本",
	"# fail ", "# fail", "# tests ", "# tests", "# fail  ", "12", "0", "x",
	"Error ", "error ", "ERROR", "exit code: ", "exit code", "exit status ", "Exit Status", "exited with code ", "EXITED WITH CODE", "exit code:", "exited with ", "exit  code ", "TypeError ", "99999999999999999999",
}

func TestScannersAgreeWithTheExpressionsTheyReplace(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	seeds := []string{
		"", "\n", "\n\n", "x", "--- FAIL: TestX (0.00s)\n", "\n\n  a_test.go:3: msg\n", "FAIL\tpkg\t0.1s\nFAIL\tpkg [build failed]\n",
		"a.go:1:2: msg\n", "a.go:1:2: \n", " a.go:1:2: msg", ".go:1:2: msg", "a.go:1:2:msg", "# fail 3\n# tests 4\n", "# fail 3 \n# fail 4\n",
		"Error 1", "Error 1\n\n", "Error \n 4 \nx", "exit code: 3\r\n", "exit code:\n\n7\n\nfoo",
	}
	var texts []string
	texts = append(texts, seeds...)
	for range 30000 {
		texts = append(texts, genText(r, scanVocab, 14))
	}
	for _, text := range texts {
		checkScanners(t, text)
		if t.Failed() {
			return
		}
	}
}

func checkScanners(t *testing.T, text string) {
	t.Helper()
	var fails, packages []string
	var builds, details [][]string
	eachLine(text, func(line string, _ int, prevBlank bool) bool {
		if n, ok := goFailName(line); ok {
			fails = append(fails, n)
		}
		if f, l, c, m, ok := goBuildError(line); ok {
			builds = append(builds, []string{f, l, c, m})
		}
		if loc, m, ok := goTestDetail(line, prevBlank); ok {
			details = append(details, []string{loc, m})
		}
		if p, ok := goFailPackage(line); ok {
			packages = append(packages, p)
		}
		return true
	})
	var wantFails, wantPackages []string
	var wantBuilds, wantDetails [][]string
	for _, m := range refGoFail.FindAllStringSubmatch(text, -1) {
		wantFails = append(wantFails, m[1])
	}
	for _, m := range refGoBuild.FindAllStringSubmatch(text, -1) {
		wantBuilds = append(wantBuilds, m[1:])
	}
	for _, m := range refGoDetail.FindAllStringSubmatch(text, -1) {
		wantDetails = append(wantDetails, m[1:])
	}
	for _, m := range refGoPackage.FindAllStringSubmatch(text, -1) {
		wantPackages = append(wantPackages, m[1])
	}
	if !reflect.DeepEqual(fails, wantFails) {
		t.Fatalf("--- FAIL names of %q: got %q want %q", text, fails, wantFails)
	}
	if !reflect.DeepEqual(builds, wantBuilds) {
		t.Fatalf("build errors of %q: got %q want %q", text, builds, wantBuilds)
	}
	if !reflect.DeepEqual(details, wantDetails) {
		t.Fatalf("test details of %q: got %q want %q", text, details, wantDetails)
	}
	if !reflect.DeepEqual(packages, wantPackages) {
		t.Fatalf("packages of %q: got %q want %q", text, packages, wantPackages)
	}
	for _, c := range []struct {
		prefix string
		re     *lazyRegexp
	}{{"# fail ", refTapFail}, {"# tests ", refTapTests}} {
		want := ""
		if m := c.re.FindStringSubmatch(text); m != nil {
			want = m[1]
		}
		if got := lineDigitsAfter(text, c.prefix); got != want {
			t.Fatalf("%q digits of %q: got %q want %q", c.prefix, text, got, want)
		}
	}
	if isASCII(text) {
		want := ""
		if m := refEvExitCode.FindAllStringSubmatch(text, -1); len(m) > 0 {
			want = m[len(m)-1][1]
		}
		if got := asciiExitCode(text); got != want {
			t.Fatalf("exit code of %q: got %q want %q", text, got, want)
		}
	}
}

// The exit code of a text with non-ASCII characters is read by the expression itself, so both paths give one answer.
func TestEvExitCodeAgreesForNonASCIIAndOverflow(t *testing.T) {
	// ſ folds to s under (?i): "ſtatus" is a status to the expression, which a non-ASCII text is left to.
	for text, want := range map[string]int{"Error 3": 3, "ſtatus exit status 4\n": 4, "exit ſtatus 9": 9, "é Error 12": 12, "é no code": 0} {
		got := evExitCode(text)
		if want == 0 {
			if got != nil {
				t.Errorf("%q: %d", text, *got)
			}
			continue
		}
		if got == nil || *got != want {
			t.Errorf("%q: %v want %d", text, got, want)
		}
	}
	if evExitCode("Error 99999999999999999999") != nil {
		t.Error("an overflowing code is no code")
	}
}

var noiseVocab = []string{
	"/tmp/x", "/private/tmp/y", "/var/folders/ab/c", "/tmp/", "/tmp", ".tmpAb12", ".tmp", "pytest-12", "pytest-", "pytest-x", "2024-03-05T10:20:30Z", "2024-03-05 10:20", "2024/03/05T10:20:30.5", "10:20:30", "12:34", "1:2:3",
	"run=abcdef1", "run=abcdef", "run=zz", "run=", "12ms", "3.5 s", "7 µs", "1µs", "12 m", "9h", "4us", "5 ns", "6.s", "8.5", "10x", "0x1F", "0x", "100x1", "(12)", "(1,2)", "(1, 2)", "(", ")", "12345", "1234", "123456789",
	":42", ":12:5", "a:b", "file.go:12:5", "go", "FAIL", "x", "TestX3", " ", "  ", "\t", "\n", "\v", "\u00a0", "\u2003", "\ufeff", "é", "\xff", "<tmp>", "<time>", "-", "/", ".", ",", ":", "0", "1", "2", "9",
}

func refNormalise(value string) string {
	for _, n := range failureNoise {
		value = n.re.ReplaceAllString(value, n.repl)
	}
	return jsTrim(refSpaceRuns.ReplaceAllString(value, " "))
}

var refSpaceRuns = &lazyRegexp{expr: reJSSpace.expr}

func TestNormaliseFailureTextAgreesWithApplyingEveryExpression(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 5))
	for i := range 60000 {
		text := genText(r, noiseVocab, 10)
		if got, want := NormaliseFailureText(text), refNormalise(text); got != want {
			t.Fatalf("case %d %q: got %q want %q", i, text, got, want)
		}
		if got, want := collapseSpaceRuns(text), refSpaceRuns.ReplaceAllString(text, " "); got != want {
			t.Fatalf("case %d %q: collapse got %q want %q", i, text, got, want)
		}
	}
	for _, text := range []string{"", " ", "  ", "a b", "a  b", " a", "a\n", "x/tmp/y z", "wait 12ms then 3.5s (12) at 0xdeadbeef :42:7 12345678", "pytest-1 pytest-22"} {
		if got, want := NormaliseFailureText(text), refNormalise(text); got != want {
			t.Errorf("%q: got %q want %q", text, got, want)
		}
	}
}

// refGoParse is the go parser of evidence.go as it was before the scanners: four expressions over the whole text.
func refGoParse(text string) (parsedFields, bool) {
	fails := col(subs(refGoFail, text), 1)
	build := subs(refGoBuild, text)
	if len(fails) == 0 && len(build) == 0 {
		return parsedFields{}, false
	}
	details := subs(refGoDetail, text)
	var packages []string
	seen := map[string]bool{}
	for _, m := range subs(refGoPackage, text) {
		if !seen[m[1]] {
			seen[m[1]] = true
			packages = append(packages, m[1])
		}
	}
	var errs []string
	if len(details) > 0 {
		errs = col(details, 2)
	} else {
		for _, m := range build {
			errs = append(errs, m[1]+": "+m[4])
		}
	}
	loc := ""
	if len(details) > 0 {
		loc = details[0][1]
	} else if len(build) > 0 {
		loc = build[0][1] + ":" + build[0][2]
	}
	summary := ""
	if len(packages) > 0 {
		summary = "FAIL " + strings.Join(packages, ", ")
		if strings.Contains(text, "[build failed]") {
			summary += " [build failed]"
		}
	}
	return parsedFields{failing: fails, errors: errs, location: loc, summary: summary}, true
}

func TestTheGoParserAgreesWithItsExpressionsOverGeneratedOutput(t *testing.T) {
	var goParse func(string) (parsedFields, bool)
	for _, p := range evParsers {
		if p.format == "go" {
			goParse = p.parse
		}
	}
	r := rand.New(rand.NewPCG(3, 3))
	for i := range 40000 {
		text := genText(r, scanVocab, 16)
		got, gotOK := goParse(text)
		want, wantOK := refGoParse(text)
		if gotOK != wantOK {
			t.Fatalf("case %d %q: parsed %v want %v", i, text, gotOK, wantOK)
		}
		if !gotOK {
			continue
		}
		if a, b := evResult("go", got, nil), evResult("go", want, nil); !reflect.DeepEqual(a, b) {
			t.Fatalf("case %d %q:\n got %+v\nwant %+v", i, text, a, b)
		}
	}
}
