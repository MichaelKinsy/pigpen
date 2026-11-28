package ansi

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestHighlightPythonEvalExactTitaniumANSI(t *testing.T) {
	p := paletteFromTheme(sdk.UITheme{})
	lines := p.highlightCodeLines([]string{"print('eval_ok', 6 * 7)"}, "python")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	got := lines[0]

	// Exact omp titanium truecolor sequences (theme.go):
	// syntaxFunction    = #00ff88 -> 0;255;136
	// syntaxPunctuation = #9ca3b0 -> 156;163;176
	// syntaxString      = #d4c090 -> 212;192;144
	// syntaxNumber      = #ffb347 -> 255;179;71
	// syntaxOperator    = #00b4ff -> 0;180;255
	wantTokens := []struct {
		name string
		seq  string
	}{
		{"syntaxFunction print", "\x1b[38;2;0;255;136mprint\x1b[39m"},
		{"syntaxPunctuation (", "\x1b[38;2;156;163;176m(\x1b[39m"},
		{"syntaxPunctuation open '", "\x1b[38;2;156;163;176m'\x1b[39m"},
		{"syntaxString eval_ok", "\x1b[38;2;212;192;144meval_ok\x1b[39m"},
		{"syntaxPunctuation close '", "\x1b[38;2;156;163;176m'\x1b[39m"},
		{"syntaxPunctuation ,", "\x1b[38;2;156;163;176m,\x1b[39m"},
		{"syntaxNumber 6", "\x1b[38;2;255;179;71m6\x1b[39m"},
		{"syntaxOperator *", "\x1b[38;2;0;180;255m*\x1b[39m"},
		{"syntaxNumber 7", "\x1b[38;2;255;179;71m7\x1b[39m"},
		{"syntaxPunctuation )", "\x1b[38;2;156;163;176m)\x1b[39m"},
	}
	for _, wt := range wantTokens {
		if !strings.Contains(got, wt.seq) {
			t.Errorf("missing %s (%q) in:\n  %q", wt.name, wt.seq, got)
		}
	}
	if plain := StripANSI(got); plain != "print('eval_ok', 6 * 7)" {
		t.Errorf("ANSI-stripped mismatch: got %q", plain)
	}
}

func TestHighlightLanguages(t *testing.T) {
	p := paletteFromTheme(sdk.UITheme{})
	fgKeyword := "\x1b[38;2;0;180;255m"
	fgFunc := "\x1b[38;2;0;255;136m"
	fgStr := "\x1b[38;2;212;192;144m"
	fgNum := "\x1b[38;2;255;179;71m"
	fgComment := "\x1b[38;2;107;114;128m"
	fgVar := "\x1b[38;2;232;236;244m"

	t.Run("javascript", func(t *testing.T) {
		lines := p.highlightCodeLines([]string{
			"const x = `hi` // note",
			"foo(0x2a)",
		}, "js")
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines, got %d", len(lines))
		}
		if !strings.Contains(lines[0], fgKeyword+"const\x1b[39m") {
			t.Errorf("js missing keyword const: %q", lines[0])
		}
		if !strings.Contains(lines[0], fgStr+"`hi`\x1b[39m") {
			t.Errorf("js missing template string: %q", lines[0])
		}
		if !strings.Contains(lines[0], fgComment+"// note\x1b[39m") {
			t.Errorf("js missing comment: %q", lines[0])
		}
		if !strings.Contains(lines[1], fgFunc+"foo\x1b[39m") || !strings.Contains(lines[1], fgNum+"0x2a\x1b[39m") {
			t.Errorf("js missing call/hex number: %q", lines[1])
		}
	})

	t.Run("go", func(t *testing.T) {
		lines := p.highlightCodeLines([]string{
			`func main() { fmt.Println("ok", 42) }`,
		}, "go")
		if len(lines) != 1 {
			t.Fatalf("expected 1 line")
		}
		if !strings.Contains(lines[0], fgKeyword+"func\x1b[39m") ||
			!strings.Contains(lines[0], fgFunc+"Println\x1b[39m") ||
			!strings.Contains(lines[0], fgStr+"ok\x1b[39m") ||
			!strings.Contains(lines[0], fgNum+"42\x1b[39m") {
			t.Errorf("go highlight mismatch: %q", lines[0])
		}
	})

	t.Run("json", func(t *testing.T) {
		lines := p.highlightCodeLines([]string{
			`{"key": "val", "n": 3.14, "ok": true}`,
		}, "json")
		if len(lines) != 1 {
			t.Fatalf("expected 1 line")
		}
		if !strings.Contains(lines[0], fgVar+`"key"`+"\x1b[39m") ||
			!strings.Contains(lines[0], fgStr+"val\x1b[39m") ||
			!strings.Contains(lines[0], fgNum+"3.14\x1b[39m") ||
			!strings.Contains(lines[0], fgKeyword+"true\x1b[39m") {
			t.Errorf("json highlight mismatch: %q", lines[0])
		}
	})

	t.Run("bash", func(t *testing.T) {
		lines := p.highlightCodeLines([]string{
			`if [ -f "$HOME/x" ]; then echo $USER # done`,
		}, "bash")
		if len(lines) != 1 {
			t.Fatalf("expected 1 line")
		}
		if !strings.Contains(lines[0], fgKeyword+"if\x1b[39m") ||
			!strings.Contains(lines[0], fgKeyword+"then\x1b[39m") ||
			!strings.Contains(lines[0], fgFunc+"echo\x1b[39m") ||
			!strings.Contains(lines[0], fgVar+"$USER\x1b[39m") ||
			!strings.Contains(lines[0], fgComment+"# done\x1b[39m") {
			t.Errorf("bash highlight mismatch: %q", lines[0])
		}
	})

	t.Run("multiline block comment and triple quote", func(t *testing.T) {
		pyLines := p.highlightCodeLines([]string{`"""`, `hello`, `"""`}, "py")
		for i, l := range pyLines {
			if !strings.Contains(l, fgStr) {
				t.Errorf("py triple-quote line %d missing syntaxString: %q", i, l)
			}
		}
		goLines := p.highlightCodeLines([]string{`/* start`, `end */ var x = 1`}, "go")
		if !strings.Contains(goLines[0], fgComment) || !strings.Contains(goLines[1], fgComment+"end */\x1b[39m") || !strings.Contains(goLines[1], fgKeyword+"var\x1b[39m") {
			t.Errorf("go multiline comment mismatch: %q", goLines)
		}
	})

	t.Run("unknown language returns nil for fallback", func(t *testing.T) {
		if got := p.highlightCodeLines([]string{"plain text"}, "unknownlang"); got != nil {
			t.Errorf("expected nil for unknown language, got %q", got)
		}
		if got := p.highlightCodeLines([]string{"plain text"}, ""); got != nil {
			t.Errorf("expected nil for empty language, got %q", got)
		}
	})
}

func TestCodeCellRendersHighlightedPython(t *testing.T) {
	p := paletteFromTheme(sdk.UITheme{})
	rows := p.renderCodeCellLines(codeCellOpts{
		code:         "print('eval_ok', 6 * 7)",
		language:     "python",
		showLanguage: true,
		title:        "Proof Python eval",
		status:       "complete",
		output:       "eval_ok 42",
		width:        80,
	})
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "\x1b[38;2;0;255;136mprint\x1b[39m") {
		t.Errorf("code cell did not syntax-highlight python print():\n%s", joined)
	}
	if !strings.Contains(joined, "\x1b[38;2;212;192;144meval_ok\x1b[39m") {
		t.Errorf("code cell did not syntax-highlight python string:\n%s", joined)
	}
}
