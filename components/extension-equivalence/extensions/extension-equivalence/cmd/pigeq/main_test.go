package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pigeq(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestGapsGoScansAGoOracle(t *testing.T) {
	dir := t.TempDir()
	body := "package x\nimport \"os\"\nfunc f() { os.Exit(1) }\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := pigeq("gaps", "--go", dir)
	if code != 1 || !strings.Contains(out, "MISSING os.Exit") {
		t.Errorf("code %d, out %q", code, out)
	}
	accept := filepath.Join(t.TempDir(), "accept.json")
	_ = os.WriteFile(accept, []byte(`{"os.Exit":"owner, #1"}`), 0o644)
	if code, out, _ := pigeq("gaps", "--go", dir, "--accept-gaps", accept); code != 0 || !strings.Contains(out, "ACCEPTED os.Exit") {
		t.Errorf("accepted: code %d, out %q", code, out)
	}
	if code, _, errs := pigeq("gaps"); code != 2 || !strings.Contains(errs, "--ts or --go") {
		t.Errorf("no source: code %d, err %q", code, errs)
	}
}

func TestRecordNeedsAnOracleAndSaysWhich(t *testing.T) {
	sc := t.TempDir()
	_ = os.WriteFile(filepath.Join(sc, "a.json"), []byte(`{"name":"a","steps":[{"name":"s","rpc":{"type":"get_state"}}]}`), 0o644)
	code, _, errs := pigeq("record", "--scenarios", sc, "--golden", t.TempDir(), "--go", t.TempDir(), "--pig", "pig")
	if code != 2 || !strings.Contains(errs, "--ts, --go-oracle or --self") {
		t.Errorf("code %d, err %q", code, errs)
	}
}

func TestEnvPrintsTheIsolatedScriptAndWritesGoWork(t *testing.T) {
	root := t.TempDir()
	fake := filepath.Join(t.TempDir(), "pig")
	_ = os.WriteFile(fake, []byte("#!/bin/sh\necho \"$PIG_HOME/sdk\"\n"), 0o755)
	mod := t.TempDir()
	code, out, errs := pigeq("env", "--root", root, "--pig", fake, "--module", mod)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	for _, want := range []string{"export HOME='" + root + "/home'", "export PIG_BIN='" + fake + "'", "export GOWORK='" + root + "/go.work'", "export PIG_SDK_DIR='" + root + "/pighome/sdk'", "unset PIG_SDK_GO_ROOT"} {
		if !strings.Contains(out, want) {
			t.Errorf("script lacks %q:\n%s", want, out)
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "go.work")); err != nil || !strings.Contains(string(b), mod) {
		t.Errorf("go.work: %v %s", err, b)
	}
}

func TestSourceSnapshotsAPiGRevision(t *testing.T) {
	repo := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	run(repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644)
	run(repo, "add", "-A")
	run(repo, "commit", "-qm", "c")
	out := filepath.Join(t.TempDir(), "src")
	code, stdout, errs := pigeq("source", "--from", repo, "--rev", "HEAD", "--out", out)
	if code != 0 || !strings.Contains(stdout, "PIG_SOURCE_ROOT="+out) {
		t.Fatalf("code %d %q %q", code, stdout, errs)
	}
	if _, err := os.Stat(filepath.Join(out, ".git")); err != nil {
		t.Error("not a git checkout")
	}
	if code, _, _ := pigeq("source", "--from", repo, "--out", out); code != 2 {
		t.Errorf("missing --rev: %d", code)
	}
}

func TestTwinsListAndCheck(t *testing.T) {
	up := t.TempDir()
	_ = os.WriteFile(filepath.Join(up, "a.test.mjs"), []byte("test('one', () => {});\ntest('two', () => {});\n"), 0o644)
	code, ledger, errs := pigeq("twins", "list", "--tests", up)
	if code != 0 || !strings.Contains(ledger, `"a"`) || !strings.Contains(ledger, "one") {
		t.Fatalf("list: %d %q %q", code, ledger, errs)
	}
	lp := filepath.Join(t.TempDir(), "ledger.json")
	_ = os.WriteFile(lp, []byte(ledger), 0o644)
	port := t.TempDir()
	_ = os.WriteFile(filepath.Join(port, "x_test.go"), []byte("package x\nfunc f(t *testing.T) { tw(t, f, \"one\", nil) }\n"), 0o644)
	code, out, _ := pigeq("twins", "check", "--ledger", lp, "--go", port)
	if code != 1 || !strings.Contains(out, "MISSING a: two") || !strings.Contains(out, "1 exact twins, 0 skipped") {
		t.Errorf("incomplete port: %d %q", code, out)
	}
	_ = os.WriteFile(filepath.Join(port, "y_test.go"), []byte("package x\nfunc g(t *testing.T) { tskip(t, f, \"two\", \"needs a display\") }\n"), 0o644)
	if code, out, _ := pigeq("twins", "check", "--ledger", lp, "--go", port); code != 0 || !strings.Contains(out, "SKIP \"two\": needs a display") {
		t.Errorf("complete port: %d %q", code, out)
	}
	if code, _, errs := pigeq("twins"); code != 2 || !strings.Contains(errs, "usage") {
		t.Errorf("no subcommand: %d %q", code, errs)
	}
}

func TestEnvWithoutARootMakesAPrivateOne(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // the private roots it makes are removed with the test
	code1, out1, errs := pigeq("env")
	code2, out2, _ := pigeq("env")
	if code1 != 0 || code2 != 0 {
		t.Fatalf("env without --root: %d %d %s", code1, code2, errs)
	}
	root := func(out string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "export HOME='") {
				return strings.TrimSuffix(strings.TrimPrefix(l, "export HOME='"), "/home'")
			}
		}
		return ""
	}
	r1, r2 := root(out1), root(out2)
	if r1 == "" || r1 == r2 || !strings.Contains(r1, "pigeq-") {
		t.Errorf("two lanes must not share an env directory: %q %q", r1, r2)
	}
	if !strings.Contains(out1, "# pigeq env root: "+r1) {
		t.Errorf("the chosen root must be printed for the lane to find again:\n%s", out1)
	}
}

func TestEnvRejectsAPiOfAnotherVersion(t *testing.T) {
	bin := t.TempDir()
	mk := func(name, out string) string {
		p := filepath.Join(bin, name)
		_ = os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755)
		return p
	}
	code, _, errs := pigeq("env", "--root", t.TempDir(), "--pig", mk("pig", "0.3.0+0.87.1"), "--pi", mk("pi", "0.84.0"))
	if code != 2 || !strings.Contains(errs, "0.87.1") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestCheckBuiltinNeedsNoGoDirectoryButMutateCannotUseIt(t *testing.T) {
	_, _, errs := pigeq("check", "--builtin", "--golden", "g", "--pig", "p", "--scenarios", filepath.Join(t.TempDir(), "none"))
	if strings.Contains(errs, "--go is required") {
		t.Errorf("--builtin must replace --go: %s", errs)
	}
	code, _, errs := pigeq("mutate", "--builtin", "--golden", "g", "--pig", "p", "--go", "x", "--mutations", "m", "--scenarios", "s")
	if code != 2 || !strings.Contains(errs, "cannot mutate") {
		t.Errorf("mutating a built binary must be refused: %d %s", code, errs)
	}
}
