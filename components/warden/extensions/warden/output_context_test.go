package warden

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of the compression half of tests/output.test.ts (the security half is the security lane's).

func ctxOutputOptions() OutputOptions {
	c := DefaultConfig()
	return OutputOptions{Security: c.Security, Context: c.Context, Timeout: time.Second}
}

func outputLog() string {
	return "start\n" + strings.Repeat("progress complete\n", 2000) + "ERROR: important failure\n" + strings.Repeat("progress complete\n", 2000) + "exit code 1\n"
}

// retentionJudge answers injection, exfiltration and retention like tests/output.test.ts `judge`.
func retentionJudge(injection, exfiltration float64, retention string, confidence float64) Judge {
	return judgeFunc(func(context.Context, Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 1, Answers: map[string]Answer{
			"injection":    {Type: "noul", Noul: injection},
			"exfiltration": {Type: "noul", Noul: exfiltration},
			"retention":    {Type: "choice", Choice: retention, Confidence: confidence, Probabilities: map[string]float64{"all": 1 - confidence, retention: confidence}},
		}}, nil
	})
}

// twin: tests/output.test.ts:96
func TestOutputRecognisedFormatAboveFormatConfidenceSelectsTheParser(t *testing.T) {
	vitest := strings.Repeat("progress complete\n", 1500) + " ❯ tests/a.test.ts (2 tests | 1 failed) 12ms\n   × adds numbers\n     → expected 3 to be 4\n\n Test Files  1 failed (1)\n      Tests  1 failed | 1 passed (2)\n"
	withFormat := func(format string, probability float64) Judge {
		return judgeFunc(func(context.Context, Request) (Evaluation, error) {
			return Evaluation{Model: "jev-test", ElapsedMs: 1, Answers: map[string]Answer{
				"injection":    {Type: "noul", Noul: 0.1},
				"exfiltration": {Type: "noul", Noul: 0.1},
				"retention":    {Type: "choice", Choice: "errors_and_summary", Confidence: 0.9, Probabilities: map[string]float64{"all": 0.1, "errors_and_summary": 0.9}},
				"format":       {Type: "choice", Choice: format, Confidence: probability, Probabilities: map[string]float64{format: probability, "other": 1 - probability}},
			}}, nil
		})
	}
	eval := func(j Judge) OutputVerdict {
		o := ctxOutputOptions()
		o.Judge = j
		return EvaluateOutput(context.Background(), "bash", vitest, "run the tests", o, nil)
	}
	parsed := eval(withFormat("vitest_jest", 0.9))
	if parsed.Format != FormatVitestJest {
		t.Fatalf("format %q", parsed.Format)
	}
	excerpt, ok := CompressOutput(vitest, parsed.Retention, parsed.Format)
	if !ok {
		t.Fatal("no excerpt")
	}
	mustMatch(t, excerpt, `vitest_jest format`, "format")
	mustMatch(t, excerpt, `× adds numbers`, "failing")
	mustMatch(t, excerpt, `expected 3 to be 4`, "assertion")
	mustMatch(t, excerpt, `Tests {2}1 failed`, "summary")
	mustNotContain(t, excerpt, "[head excerpt]", "parser, not head")
	unsure := eval(withFormat("vitest_jest", 0.5))
	if unsure.Format != "" || unsure.FormatConfidence == nil || *unsure.FormatConfidence != 0.5 {
		t.Errorf("unsure: %+v", unsure)
	}
	if other := eval(withFormat("other", 0.95)); other.Format != "" {
		t.Errorf("other: %q", other.Format)
	}
	// The judge names a format whose markers are absent: the generic excerpt is used, nothing is lost.
	generic, ok := CompressOutput(outputLog(), RetentionErrorsAndSummary, FormatTsc)
	if !ok {
		t.Fatal("no generic excerpt")
	}
	mustMatch(t, generic, `\[head excerpt\]`, "generic")
}

// twin: tests/output.test.ts:124
func TestOutputKeysIgnoreColourCodesAndTrailingWhitespaceTheDuplicateNoteNamesTheEarlierToolAndSize(t *testing.T) {
	plain := "line one\nline two\n"
	if OutputKey(plain) != OutputKey("\u001b[32mline one\u001b[0m   \nline two  \n\n") {
		t.Error("colour and trailing whitespace do not make a new output")
	}
	if OutputKey(plain) == OutputKey("line one\nline three\n") {
		t.Error("different text, same key")
	}
	note := DuplicateNote(strings.Repeat("x\n", 1000), "bash")
	mustMatch(t, note, `duplicate; this 2000-character, 1001-line output is identical to an earlier bash result`, "note")
	if len(note) >= 300 {
		t.Errorf("note of %d", len(note))
	}
}

// twin: tests/output.test.ts:145
func TestOutputTailCompressionNeedsConsentConfidenceSufficientSizeAndOneTextBlock(t *testing.T) {
	eval := func(o OutputOptions, tool, text string) OutputVerdict {
		return EvaluateOutput(context.Background(), tool, text, "", o, nil)
	}
	for _, retention := range []Retention{RetentionErrorsAndSummary, RetentionSummaryOnly} {
		o := ctxOutputOptions()
		o.Judge = retentionJudge(0.1, 0.1, string(retention), 0.95)
		if got := eval(o, "bash", outputLog()).Retention; got != retention {
			t.Errorf("%s: %s", retention, got)
		}
	}
	o := ctxOutputOptions()
	o.Judge = retentionJudge(0.1, 0.1, "summary_only", 0.5)
	if got := eval(o, "bash", outputLog()).Retention; got != RetentionAll {
		t.Errorf("uncertain: %s", got)
	}
	o = ctxOutputOptions()
	o.Security.Enabled = false
	o.Judge = judgeFunc(func(context.Context, Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 1, Answers: map[string]Answer{"retention": {Type: "choice", Choice: "summary_only", Confidence: 0.99}}}, nil
	})
	if got := eval(o, "bash", outputLog()).Retention; got != RetentionAll {
		t.Errorf("a missing probability keeps the full output: %s", got)
	}
	o = ctxOutputOptions()
	o.NotCompressible = true
	o.Judge = retentionJudge(0.1, 0.1, "summary_only", 0.95)
	if got := eval(o, "bash", outputLog()).Retention; got != RetentionAll {
		t.Errorf("mixed: %s", got)
	}
	o = ctxOutputOptions()
	o.Judge = retentionJudge(0.1, 0.1, "summary_only", 0.95)
	if got := eval(o, "read", "short output").Retention; got != RetentionAll {
		t.Errorf("small: %s", got)
	}
}

// twin: tests/output.test.ts:161
func TestOutputCompressionIsDeterministicKeepsDiagnosticsAndTailAndDoesNotInventASummary(t *testing.T) {
	text := outputLog()
	compressed, ok := CompressOutput(text, RetentionErrorsAndSummary, "")
	if !ok {
		t.Fatal("no excerpt")
	}
	if again, _ := CompressOutput(text, RetentionErrorsAndSummary, ""); again != compressed {
		t.Error("not deterministic")
	}
	mustMatch(t, compressed, `ERROR: important failure`, "diagnostic")
	mustMatch(t, compressed, `exit code 1`, "tail")
	if utf16Len(compressed) >= 6500 {
		t.Errorf("length %d", utf16Len(compressed))
	}
	if _, ok := CompressOutput("short", RetentionSummaryOnly, ""); ok {
		t.Error("short output stays whole")
	}
	if _, ok := CompressOutput(text, RetentionAll, ""); ok {
		t.Error("all keeps everything")
	}
	if big, ok := CompressOutput(strings.Repeat("x", 50000), RetentionSummaryOnly, ""); !ok || utf16Len(big) >= 6500 {
		t.Errorf("long single line: %v %d", ok, utf16Len(big))
	}
}

// twin: tests/output.test.ts:173
func TestOutputStoredIsExactOwnerOnlyAndAvailableIndependentlyOfTheSession(t *testing.T) {
	text := outputLog() + "😀"
	path, err := SaveOutput(text)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	got, err := os.ReadFile(path)
	if err != nil || string(got) != text {
		t.Fatalf("stored text differs: %v", err)
	}
	if runtime.GOOS != "windows" {
		file, _ := os.Stat(path)
		dir, _ := os.Stat(filepath.Dir(path))
		if file.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Errorf("modes %v %v", file.Mode().Perm(), dir.Mode().Perm())
		}
	}
}

// twin: tests/output.test.ts:191
func TestCompressionLearnerRecordsOutcomesAndSuggestsRetention(t *testing.T) {
	l := &CompressionLearner{}
	for range 5 {
		l.Record("bash", RetentionErrorsAndSummary, "", false)
	}
	for range 2 {
		l.Record("bash", RetentionSummaryOnly, "", true)
	}
	if best := l.BestRetention("bash"); best != RetentionErrorsAndSummary {
		t.Errorf("suggests retention with lowest recall rate: %q", best)
	}
	if s := l.Stats("bash"); s.Total != 7 || s.Recalled != 2 {
		t.Errorf("%+v", s)
	}
}

// twin: tests/output.test.ts:210
func TestCompressionLearnerReturnsNothingForInsufficientData(t *testing.T) {
	l := &CompressionLearner{}
	l.Record("bash", RetentionErrorsAndSummary, "", false)
	if best := l.BestRetention("bash"); best != "" {
		t.Errorf("%q", best)
	}
}

// twin: tests/output.test.ts:218
func TestCompressionLearnerResetsCorrectly(t *testing.T) {
	l := &CompressionLearner{}
	l.Record("bash", RetentionErrorsAndSummary, "", false)
	l.Reset()
	if best := l.BestRetention("bash"); best != "" {
		t.Errorf("%q", best)
	}
}
