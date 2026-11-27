package ansi

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func renderVizPlain(cmd string, running bool, exitCode *int, width int) []string {
	p := paletteFromTheme(sdk.UITheme{})
	pres := BuildShellVizPresentation(cmd, running, exitCode, "")
	lines := RenderShellVizTerminal(p, pres, width)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(StripANSI(l))
	}
	return out
}

func TestShellVizExactOmpPairs(t *testing.T) {
	// Exact paired omp-visualizer rows from proof/ansi_capture.py BASH_SHELL_VIZ_EXACT_PAIRS.
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "printf single-quoted newline escape",
			cmd:  `printf 'alpha\nbeta\n'`,
			want: `print("alpha\\nbeta\\n")`,
		},
		{
			name: "reject double-quoted literal strips quotes",
			cmd:  `reject "Proof preview only"`,
			want: `reject Proof preview only`,
		},
		{
			name: "seq pipe awk embedded highlight",
			cmd:  `seq 1 30 | awk '{printf "fold_line_%02d\n", $1}'`,
			want: `seq 1 30 → awk ‹{printf "fold_line_%02d\n", $1}›`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderVizPlain(tc.cmd, false, nil, 120)
			if len(got) != 1 {
				t.Fatalf("got %d lines (%q), want 1 line %q", len(got), got, tc.want)
			}
			if got[0] != tc.want {
				t.Errorf("command %q:\n  got:  %q\n  want: %q", tc.cmd, got[0], tc.want)
			}
		})
	}
}

func TestShellVizIdiomsAndControlFlow(t *testing.T) {
	t.Run("echo flags skipped", func(t *testing.T) {
		got := renderVizPlain(`echo -n "hello world"`, false, nil, 120)
		want := `print("hello world")`
		if len(got) != 1 || got[0] != want {
			t.Fatalf("got %q, want [%q]", got, want)
		}
	})

	t.Run("pipeline and head/tail folding", func(t *testing.T) {
		gotHead := renderVizPlain(`cat file.txt | head -n 5`, false, nil, 120)
		if len(gotHead) != 1 || gotHead[0] != `cat file.txt ⇡ first 5` {
			t.Errorf("head fold: got %q, want %q", gotHead, `cat file.txt ⇡ first 5`)
		}
		gotTail := renderVizPlain(`ls -la | tail -20`, false, nil, 120)
		if len(gotTail) != 1 || gotTail[0] != `ls -la ⇣ last 20` {
			t.Errorf("tail fold: got %q, want %q", gotTail, `ls -la ⇣ last 20`)
		}
		gotPipeErr := renderVizPlain(`make |& tee build.log`, false, nil, 120)
		if len(gotPipeErr) != 1 || !strings.Contains(gotPipeErr[0], `make →& tee build.log`) {
			t.Errorf("pipe stderr: got %q", gotPipeErr)
		}
	})

	t.Run("and/or chain and may fail", func(t *testing.T) {
		gotChain := renderVizPlain(`echo a && echo b || echo c`, false, nil, 120)
		want := []string{
			`print("a")`,
			`and print("b")`,
			`or print("c")`,
		}
		if len(gotChain) != len(want) {
			t.Fatalf("and/or got %q, want %q", gotChain, want)
		}
		for i := range want {
			if gotChain[i] != want[i] {
				t.Errorf("line %d: got %q, want %q", i, gotChain[i], want[i])
			}
		}

		gotMayFail := renderVizPlain(`rm -f tmp.log || true`, false, nil, 120)
		if len(gotMayFail) != 1 || !strings.Contains(gotMayFail[0], `rm -f tmp.log may fail`) {
			t.Errorf("may fail: got %q", gotMayFail)
		}
	})

	t.Run("cd and set idioms", func(t *testing.T) {
		gotCd := renderVizPlain(`cd /tmp/work`, false, nil, 120)
		if len(gotCd) != 1 || gotCd[0] != `cwd /tmp/work` {
			t.Errorf("cd idiom: got %q", gotCd)
		}
		gotSet := renderVizPlain(`set -e`, false, nil, 120)
		if len(gotSet) != 1 || gotSet[0] != `# stop on first error` {
			t.Errorf("set -e idiom: got %q", gotSet)
		}
	})

	t.Run("env prefix and redirections", func(t *testing.T) {
		gotEnv := renderVizPlain(`FOO=bar BAZ=1 mycmd arg`, false, nil, 120)
		if len(gotEnv) != 1 || gotEnv[0] != `env(FOO = "bar", BAZ = "1") mycmd arg` {
			t.Errorf("env prefix: got %q", gotEnv)
		}
		gotRedir := renderVizPlain(`cmd 2>/dev/null`, false, nil, 120)
		if len(gotRedir) != 1 || gotRedir[0] != `cmd 🔇 err` {
			t.Errorf("stderr discard: got %q", gotRedir)
		}
		gotMerge := renderVizPlain(`cmd 2>&1`, false, nil, 120)
		if len(gotMerge) != 1 || gotMerge[0] != `cmd +err` {
			t.Errorf("stderr merge: got %q", gotMerge)
		}
	})

	t.Run("curl http_status and timeout", func(t *testing.T) {
		gotCurl := renderVizPlain(`curl -s -o /dev/null -w '%{http_code}' https://example.com/health`, false, nil, 120)
		if len(gotCurl) != 1 || !strings.Contains(gotCurl[0], `http_status("https://example.com/health")`) {
			t.Errorf("curl idiom: got %q", gotCurl)
		}
		gotTimeout := renderVizPlain(`timeout 30 sleep 5`, false, nil, 120)
		if len(gotTimeout) != 1 || gotTimeout[0] != `⏱ 30s sleep 5` {
			t.Errorf("timeout idiom: got %q", gotTimeout)
		}
	})

	t.Run("if and for control flow", func(t *testing.T) {
		script := `if [ -f foo.txt ]; then echo yes; else echo no; fi`
		got := renderVizPlain(script, false, nil, 120)
		want := []string{
			`if is_file("foo.txt"):`,
			`print("yes")`,
			`else:`,
			`print("no")`,
		}
		if len(got) != len(want) {
			t.Fatalf("if/else got %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("if/else line %d: got %q, want %q", i, got[i], want[i])
			}
		}

		forScript := `for x in a b; do echo "$x"; done`
		gotFor := renderVizPlain(forScript, false, nil, 120)
		if len(gotFor) != 2 || gotFor[0] != `for x in ["a", "b"]:` || gotFor[1] != `print(f"{x}")` {
			t.Errorf("for loop: got %q", gotFor)
		}
	})

	t.Run("invalid syntax fallback", func(t *testing.T) {
		got := renderVizPlain(`if true; then echo "unterminated`, false, nil, 120)
		if len(got) != 1 || got[0] != `if true; then echo "unterminated` {
			t.Errorf("fallback: got %q", got)
		}
	})
}

func TestShellVizPresentationJSONShape(t *testing.T) {
	exitErr := 2
	pres := BuildShellVizPresentation(`rm -rf /tmp/demo`, false, &exitErr, "removed\n")
	raw, err := json.Marshal(pres)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	for _, key := range []string{"preferences", "rows", "original", "output", "running", "executing", "watches", "timeline"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("presentation missing required top-level key %q", key)
		}
	}
	prefs := asMap(decoded["preferences"])
	if strField(prefs, "view") != "pseudo" {
		t.Errorf("preferences.view = %q, want pseudo", strField(prefs, "view"))
	}
	rows := asSlice(decoded["rows"])
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := asMap(rows[0])
	if strField(row, "state") != "fail" {
		t.Errorf("row.state = %q, want fail", strField(row, "state"))
	}
	if strField(row, "role") != "stmt" {
		t.Errorf("row.role = %q, want stmt", strField(row, "role"))
	}
	effs := asSlice(row["effects"])
	if len(effs) == 0 || strField(asMap(effs[0]), "kind") != "destructive" {
		t.Errorf("expected destructive effect on rm, got %v", effs)
	}

	// Render with failure gutter (`×`) and effects glyph (`×`)
	p := paletteFromTheme(sdk.UITheme{})
	rendered := RenderShellVizTerminal(p, pres, 80)
	if len(rendered) != 1 {
		t.Fatalf("expected 1 rendered line, got %d", len(rendered))
	}
	plain := StripANSI(rendered[0])
	if !strings.HasPrefix(plain, "× ") {
		t.Errorf("failed stmt should start with '× ' gutter, got %q", plain)
	}
}

func TestShellVizHeredocs(t *testing.T) {
	// H1: multi-line heredoc after redirection
	cmd := "cat > f <<'EOF'\nline1\nline2\nEOF"
	pres := BuildShellVizPresentation(cmd, false, nil, "")
	rows := asSlice(pres["rows"])
	if len(rows) < 2 {
		t.Fatalf("expected >= 2 rows for heredoc, got %d", len(rows))
	}
	foundBody := false
	for _, r := range rows {
		row := asMap(r)
		for _, s := range asSlice(row["segments"]) {
			if strings.Contains(strField(asMap(s), "text"), "line1") {
				foundBody = true
			}
		}
	}
	if !foundBody {
		t.Fatalf("expected heredoc body 'line1' to be parsed and rendered, got rows=%v", rows)
	}

	// Single-line heredoc
	single := "cat <<EOF\nhello\nEOF"
	presSingle := BuildShellVizPresentation(single, false, nil, "")
	if len(asSlice(presSingle["rows"])) < 2 {
		t.Fatalf("expected >= 2 rows for single-line heredoc")
	}

	// #! python heredoc language detection
	pyCmd := "cat > run.sh <<'EOF'\n#!/usr/bin/env python3\nprint('hi')\nEOF"
	presPy := BuildShellVizPresentation(pyCmd, false, nil, "")
	foundPy := false
	for _, r := range asSlice(presPy["rows"]) {
		for _, s := range asSlice(asMap(r)["segments"]) {
			if strField(asMap(s), "language") == "py" {
				foundPy = true
			}
		}
	}
	if !foundPy {
		t.Errorf("expected python heredoc language detection for #!/usr/bin/env python3")
	}

	// Piped heredoc
	pipeCmd := "cat <<EOF | grep x\nalpha\nxray\nEOF"
	presPipe := BuildShellVizPresentation(pipeCmd, false, nil, "")
	if len(asSlice(presPipe["rows"])) < 2 {
		t.Fatalf("expected >= 2 rows for piped heredoc")
	}
}

func TestShellVizRunningNotDimmed(t *testing.T) {
	// M1: running presentation should emit running=false, executing=true so
	// RenderShellVizTerminal does NOT dim the entire command with SGR faint (\x1b[2m).
	pres := BuildShellVizPresentation("go test ./...", true, nil, "")
	if pres["running"] != false {
		t.Errorf("expected running=false for trace-less presentation, got %v", pres["running"])
	}
	if pres["executing"] != true {
		t.Errorf("expected executing=true, got %v", pres["executing"])
	}
	p := paletteFromTheme(sdk.UITheme{})
	rendered := RenderShellVizTerminal(p, pres, 80)
	if len(rendered) == 0 {
		t.Fatalf("expected rendered lines")
	}
	if strings.Contains(rendered[0], "\x1b[2m") {
		t.Errorf("running command should not be dimmed with SGR faint: %q", rendered[0])
	}
}

func TestShellVizAdversarialInputs(t *testing.T) {
	// M4: deep ((( x 1000, nested ( x 500, 1000-word command, 50KB single-line CJK/spaces
	cases := []struct {
		name string
		cmd  string
	}{
		{"arith_1000", strings.Repeat("(", 1000)},
		{"nested_subshell_500", strings.Repeat("(", 500) + "echo hi" + strings.Repeat(")", 500)},
		{"words_1000", "echo " + strings.Repeat("arg ", 1000)},
		{"cjk_50kb", "echo " + strings.Repeat("你好世界 ", 5000)},
		{"oversized_300kb", "echo " + strings.Repeat("x", 300*1024)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pres := BuildShellVizPresentation(tc.cmd, false, nil, "")
			if pres == nil {
				t.Fatalf("expected non-nil presentation")
			}
			if len(asSlice(pres["rows"])) == 0 {
				t.Fatalf("expected at least 1 row")
			}
		})
	}
}

func TestShellVizArrayAssignAndParamOperators(t *testing.T) {
	// L1: bash array assignment should parse cleanly without falling back or mis-parsing rm as destructive
	pres := BuildShellVizPresentation("files=(rm -rf build)", false, nil, "")
	rows := asSlice(pres["rows"])
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for array assignment, got %d", len(rows))
	}
	if strField(asMap(rows[0]), "role") != "stmt" {
		t.Errorf("expected role=stmt for array assignment, got %q", strField(asMap(rows[0]), "role"))
	}

	// L5: ${var/-/_}, ${v%-*}, ${x//+/ } should not mis-render as is_set(...)
	for _, expr := range []string{"var/-/_", "v%-*", "x//+/ "} {
		got := svPrintParam(expr, nil)
		if strings.Contains(got, "is_set(") || strings.Contains(got, "nonempty(") {
			t.Errorf("svPrintParam(%q) = %q, should not mis-render as is_set/nonempty", expr, got)
		}
	}
	// Rust lifetime 'a should not be treated as a char literal
	segs := svHighlightEmbedded("fn f<'a>(x: &'a str) {}", "rust", "n1", nil)
	for _, seg := range segs {
		if strField(seg, "style") == "str" {
			t.Errorf("rust lifetime 'a should not produce str segment: %v", segs)
		}
	}
}
