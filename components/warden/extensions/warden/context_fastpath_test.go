package warden

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// Go-only: each fast path of the context saver is held to the code it replaces, over inputs built to break it.

// ─── isASCII ─────────────────────────────────────────────────────────────

func isASCIIReference(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// TestIsASCIIEqualsTheByteLoop: a byte of 0x80 or more at every position of every length around the word and block
// sizes the scan steps by.
func TestIsASCIIEqualsTheByteLoop(t *testing.T) {
	for n := range 140 {
		base := []byte(strings.Repeat("a", n))
		if !isASCII(string(base)) {
			t.Fatalf("length %d: ASCII text refused", n)
		}
		for i := range n {
			for _, high := range []byte{0x80, 0xC3, 0xFF} {
				b := append([]byte(nil), base...)
				b[i] = high
				if isASCII(string(b)) {
					t.Fatalf("length %d, byte %#x at %d: accepted", n, high, i)
				}
			}
		}
	}
	rng := rand.New(rand.NewPCG(3, 1))
	for range 5000 {
		b := make([]byte, rng.IntN(200))
		for i := range b {
			b[i] = byte(rng.IntN(0x80))
			if rng.IntN(400) == 0 {
				b[i] = byte(rng.IntN(256))
			}
		}
		if got, want := isASCII(string(b)), isASCIIReference(string(b)); got != want {
			t.Fatalf("%q: %v, want %v", b, got, want)
		}
	}
}

// ─── the diagnostic scan ─────────────────────────────────────────────────

var scanWords = []string{"error", "Error", "ERROR", "eRRor", "errors", "fail", "FAILED", "failure", "failing", "failures", "warn", "WARNING", "warnings", "fatal", "Fatal", "exception", "exceptions", "exit", "EXITED", "exiting", "summary", "Summary", "SUMMARY", "summaries",
	"x", "_", "-", " ", ".", ":", "1", "progress", "compiled", "\n", "\n", "\t", "\r", "e", "f", "w", "s", "ex", "fa", "wa", "su", "er"}

func randomScanText(rng *rand.Rand) string {
	var b strings.Builder
	for range rng.IntN(40) + 1 {
		if rng.IntN(25) == 0 {
			b.WriteByte(byte(rng.IntN(0x80)))
		} else {
			b.WriteString(scanWords[rng.IntN(len(scanWords))])
		}
	}
	return b.String()
}

// TestNextDiagnosticLineEqualsTheLineByLineScan: the lines the in-place scan yields, one after another from the end of
// the previous one, are the lines diagnosticLine accepts in a split of the text.
func TestNextDiagnosticLineEqualsTheLineByLineScan(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 1))
	hits := 0
	for range 30000 {
		text := randomScanText(rng)
		var want []string
		for _, line := range strings.Split(text, "\n") {
			if diagnosticLine(line) {
				want = append(want, line)
			}
		}
		var got []string
		for from := 0; from < len(text); {
			start, end := nextDiagnosticLine(text, from)
			if start < 0 {
				break
			}
			got = append(got, text[start:end])
			from = end + 1
		}
		hits += len(got)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("%q: scan %q, lines %q", text, got, want)
		}
	}
	if hits == 0 {
		t.Fatal("never found a line: the test proves nothing")
	}
}

// TestNextDiagnosticLineFindsEveryWordAtEveryOffset: each spelling of each word, after every padding length (so the
// word starts at every position of the eight the scan tests at once, and straddles every word boundary), with a
// letter before it or not, and cut short at the end of the text.
func TestNextDiagnosticLineFindsEveryWordAtEveryOffset(t *testing.T) {
	words := []string{"error", "ERROR", "Error", "fail", "FAILED", "failure", "fatal", "FATAL", "warn", "Warning", "exception", "exit", "Exited", "summary", "SUMMARY", "errors", "failing", "warnings", "exiting", "summarize", "erro", "fai", "su", "e"}
	for _, word := range words {
		for pad := range 40 {
			for _, before := range []string{" ", "a", "_", "-", "\n"} {
				for _, after := range []string{"", " x", "s", "ed", "_", "\n", "\nerror"} {
					text := strings.Repeat("q", pad) + before + word + after
					for from := 0; from <= pad; from += 3 {
						if from > 0 && text[from-1] != '\n' {
							// a search starts at a line start, or at the start of the text
							continue
						}
						start, end := nextDiagnosticLine(text, from)
						wantStart, wantEnd := -1, -1
						for s := from; s < len(text); {
							e := strings.IndexByte(text[s:], '\n')
							if e < 0 {
								e = len(text) - s
							}
							if diagnosticLine(text[s : s+e]) {
								wantStart, wantEnd = s, s+e
								break
							}
							s += e + 1
						}
						if start != wantStart || end != wantEnd {
							t.Fatalf("%q from %d: got %d..%d, want %d..%d", text, from, start, end, wantStart, wantEnd)
						}
					}
				}
			}
		}
	}
}

// ─── CompressOutput and FormatExcerpt ────────────────────────────────────

// compressOutputReference is CompressOutput as it was: every line split off, each tested, and every measure taken
// through the UTF-16 helpers.
func compressOutputReference(text string, retention Retention, format OutputFormat) (string, bool) {
	if retention == RetentionAll {
		return "", false
	}
	lines := strings.Split(text, "\n")
	total := utf16Len(text)
	if format != "" {
		if parsed, ok := formatExcerptReference(text, format); ok {
			result := fmt.Sprintf("[pi-warden: %s; %d original characters, %d lines. Exact lines selected for the %s format; omitted text is in the full-output file.]\n%s", retention, total, len(lines), format, parsed)
			if total-utf16Len(result) >= 1000 {
				return result, true
			}
			return "", false
		}
	}
	head := ""
	if retention == RetentionErrorsAndSummary {
		head = utf16Slice(text, 0, 1000)
	}
	tail := text
	if total > 2000 {
		tail = utf16Slice(text, total-2000, -1)
	}
	var diagnostics []string
	diagnosticChars := 0
	for _, line := range lines {
		if !reDiagnostic.MatchString(line) {
			continue
		}
		clipped := utf16Slice(line, 0, 500)
		if diagnosticChars+utf16Len(clipped)+1 > 2000 {
			break
		}
		diagnostics = append(diagnostics, clipped)
		diagnosticChars += utf16Len(clipped) + 1
	}
	var body []string
	if head != "" {
		body = append(body, "[head excerpt]\n"+head)
	}
	if len(diagnostics) > 0 {
		body = append(body, "[diagnostic excerpts; may be incomplete]\n"+strings.Join(diagnostics, "\n"))
	}
	body = append(body, "[tail excerpt]\n"+tail)
	result := fmt.Sprintf("[pi-warden: %s; %d original characters, %d lines. %s]\n%s", retention, total, len(lines), ExcerptNote, strings.Join(body, "\n\n"))
	if total-utf16Len(result) >= 1000 {
		return result, true
	}
	return "", false
}

// formatExcerptReference is FormatExcerpt with the last three non-empty lines taken from a list of all of them.
func formatExcerptReference(text string, format OutputFormat) (string, bool) {
	if format == FormatOther {
		return "", false
	}
	parser, ok := formatParsers[format]
	if !ok {
		return "", false
	}
	lines := strings.Split(text, "\n")
	selected, ok := parser(lines)
	if !ok {
		return "", false
	}
	var nonEmpty []string
	for _, line := range lines {
		if jsTrim(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	tail := nonEmpty
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	var body []string
	size, dropped := 0, 0
	for _, line := range selected {
		clipped := clip400(line)
		if size+utf16Len(clipped)+1 > formatExcerptLimit-400 {
			dropped++
			continue
		}
		body = append(body, clipped)
		size += utf16Len(clipped) + 1
	}
	omitted := ""
	if dropped > 0 {
		omitted = ", " + itoaInt(dropped) + " omitted for size"
	}
	parts := append([]string{"[" + string(format) + " excerpt: " + itoaInt(len(selected)) + " selected lines" + omitted + "]"}, body...)
	if format != FormatGitDiff && format != FormatGitLog && len(tail) > 0 {
		last := tail[len(tail)-1]
		found := false
		for _, b := range body {
			if b == last {
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, "[tail]")
			for _, line := range tail {
				parts = append(parts, clip400(line))
			}
		}
	}
	return strings.Join(parts, "\n"), true
}

var compressPieces = []string{
	"progress 12 complete: compiled module 12 without warnings", "ERROR: parser.test.ts keeps the header: expected 3 to equal 4", "warning: unused variable", "Exit code 1", "summary: 1 failed, 3999 passed",
	"src/f1.ts(3,5): error TS2322: Type 'string' is not assignable to type 'number'.", "Found 2 errors in 1 files.", "  indented continuation line", "", "   ", "\t",
	"FAIL  src/a.test.ts", "PASS  src/b.test.ts", "Test Files  1 failed | 2 passed", "● suite › case", "  Expected: 1", "    at Object.<anonymous> (a.ts:1:2)", "✓ ok", "✗ nope", "TAP version 13", "# tests 3", "# fail 1", "not ok 2 - x",
	"/a.ts", "  1:1  error  Unexpected  no-undef", "✖ 2 problems (2 errors, 0 warnings)", "___ test_x ___", "=== 1 failed, 2 passed in 0.1s ===", "diff --git a/x b/x", "@@ -1,2 +1,2 @@", "+added", "-removed", "commit abc123", "added 12 packages in 2s",
	"é accents in a line", "emoji 😀 pair", "日本語のログ行", "\u2028 separator", "\ufeffbom line", "a`b", "ünïcode error here", "naïve failure",
}

func randomCompressText(rng *rand.Rand, nonASCII bool) string {
	var b strings.Builder
	lines := rng.IntN(400) + 1
	if rng.IntN(12) == 0 {
		lines = rng.IntN(4000) + 400
	}
	for i := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		for {
			piece := compressPieces[rng.IntN(len(compressPieces))]
			if !nonASCII && !isASCIIReference(piece) {
				continue
			}
			b.WriteString(piece)
			break
		}
		switch rng.IntN(12) {
		case 0:
			b.WriteString(strings.Repeat("long ", rng.IntN(300)))
		case 1:
			b.WriteString(" error ")
			b.WriteString(strings.Repeat("e", rng.IntN(600)))
		}
	}
	if rng.IntN(4) == 0 {
		b.WriteByte('\n')
	}
	return b.String()
}

// TestCompressOutputEqualsTheReference: the excerpt of a text, with and without a parser, with ASCII and with other
// characters, around the 1,000-character gain, the 2,000-character diagnostic budget and the 500-character clip, is the
// one the line-by-line version builds.
func TestCompressOutputEqualsTheReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 1))
	formats := []OutputFormat{"", FormatTsc, FormatVitestJest, FormatNodeTest, FormatEslint, FormatPytest, FormatGitDiff, FormatGitLog, FormatNpmInstall, FormatOther}
	retentions := []Retention{RetentionAll, RetentionErrorsAndSummary, RetentionSummaryOnly}
	compressed, parsed := 0, 0
	for i := range 1500 {
		text := randomCompressText(rng, i%3 == 0)
		retention := retentions[rng.IntN(len(retentions))]
		format := formats[rng.IntN(len(formats))]
		want, wantOK := compressOutputReference(text, retention, format)
		got, gotOK := CompressOutput(text, retention, format)
		if got != want || gotOK != wantOK {
			t.Fatalf("case %d (%s, %q, %d bytes): got %v %q, want %v %q", i, retention, format, len(text), gotOK, got, wantOK, want)
		}
		if gotOK {
			compressed++
			if format != "" && strings.Contains(got, "Exact lines selected") {
				parsed++
			}
		}
	}
	if compressed < 500 || parsed < 50 {
		t.Fatalf("%d compressed, %d by a parser: the test does not reach both routes", compressed, parsed)
	}
	// The sizes of the benchmarks, and the edges of the gain.
	for _, text := range []string{benchLog(3500), benchTsc(2000), strings.Repeat("x", 2999), strings.Repeat("x", 3000), strings.Repeat("x", 3001), strings.Repeat("é", 3001), "", "\n"} {
		for _, format := range []OutputFormat{"", FormatTsc} {
			want, wantOK := compressOutputReference(text, RetentionErrorsAndSummary, format)
			if got, ok := CompressOutput(text, RetentionErrorsAndSummary, format); got != want || ok != wantOK {
				t.Fatalf("%d bytes, %q: got %v %q, want %v %q", len(text), format, ok, got, wantOK, want)
			}
		}
	}
}

// TestFormatExcerptEqualsTheReference: the tail is the last three non-empty lines whatever the empty lines around them.
func TestFormatExcerptEqualsTheReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(22, 1))
	for i := range 400 {
		text := randomCompressText(rng, i%2 == 0)
		if rng.IntN(2) == 0 {
			text += strings.Repeat("\n  \t", rng.IntN(5))
		}
		for _, format := range []OutputFormat{FormatTsc, FormatVitestJest, FormatNodeTest, FormatEslint, FormatPytest, FormatGitDiff, FormatGitLog, FormatNpmInstall} {
			want, wantOK := formatExcerptReference(text, format)
			if got, ok := FormatExcerpt(text, format); got != want || ok != wantOK {
				t.Fatalf("%q, %d bytes: got %v %q, want %v %q", format, len(text), ok, got, wantOK, want)
			}
		}
	}
}
