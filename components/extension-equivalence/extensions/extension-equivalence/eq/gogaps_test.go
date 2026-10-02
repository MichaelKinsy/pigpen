package eq

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func symbols(gaps []Gap) map[string]Gap {
	out := map[string]Gap{}
	for _, g := range gaps {
		out[g.Symbol] = g
	}
	return out
}

// A Go original (a port relocated from another repository, or a new extension) has no
// Pi API to scan for. What can block its fuse and its independence from PiG is what it
// imports and calls: PiG-internal packages, process-global calls, and SDK stand-ins.
func TestGoGapScannerFlagsInternalImportsHazardsAndStandIns(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module example.com/x\n",
		"ext.go": `package x

import (
	"fmt"
	"log"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/coding/internal/pixel"
	other "github.com/MichaelKinsy/PiG/coding/tui"
)

func Extension() *sdk.Extension {
	e := sdk.New("x")
	e.Command("c", "", func(ctx sdk.Context, args string) error {
		ctx.SetWidget("k", []string{"a"})
		fmt.Println("hi")
		if len(args) > 3 {
			os.Exit(1)
		}
		log.Fatalf("x")
		_ = pixel.New
		_ = other.Width
		return nil
	})
	return e
}
`,
		// tests and companion executables are not part of the fused extension
		"ext_test.go":      "package x\nimport \"os\"\nfunc f() { os.Exit(2) }\n",
		"cmd/tool/main.go": "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Println(1); os.Exit(0) }\n",
	})
	gaps, err := ScanGoPath(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	got := symbols(gaps)
	for _, sym := range []string{
		"import github.com/MichaelKinsy/PiG/coding/internal/pixel",
		"import github.com/MichaelKinsy/PiG/coding/tui",
		"os.Exit", "log.Fatalf", "fmt.Println",
	} {
		g, ok := got[sym]
		if !ok || g.Severity != "missing" {
			t.Errorf("%s not flagged as blocking: %+v (all: %+v)", sym, g, gaps)
		}
	}
	if g := got["os.Exit"]; g.File != "ext.go" || len(g.Lines) != 1 || g.Lines[0] != 19 {
		t.Errorf("os.Exit location: %+v", g)
	}
	if _, ok := got["import github.com/MichaelKinsy/PiG/extensions/sdk"]; ok {
		t.Error("the public SDK import must not be flagged")
	}
	if g, ok := got["Context.SetWidget"]; !ok || g.Severity != "partial" || !strings.Contains(g.Note, "component factory") {
		t.Errorf("stand-in SetWidget: %+v", g)
	}
	if !Blocking(gaps) {
		t.Error("internal imports and hazards must block")
	}
	for _, g := range gaps {
		if strings.Contains(g.File, "_test.go") || strings.HasPrefix(g.File, "cmd/") {
			t.Errorf("test and main-package files are not scanned: %+v", g)
		}
	}
}

func TestGoGapScannerCleanExtension(t *testing.T) {
	dir := writeTree(t, map[string]string{"ext.go": `package x

import (
	"context"
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New("x")
	e.Command("c", "", func(ctx sdk.Context, args string) error {
		select {
		case <-ctx.Done():
		default:
		}
		_ = context.Background()
		_ = fmt.Sprintf("ok")
		return nil
	})
	return e
}
`})
	gaps, err := ScanGoPath(dir, "")
	if err != nil || len(gaps) != 0 {
		t.Errorf("clean Go extension flagged: %v %+v", err, gaps)
	}
}

// Three kinds of oracle: the TypeScript original under Pi, an original that is already Go
// (relocated code) under PiG, and, when no original exists, the port itself. Only the first
// is proof of equivalence; the golden trace says which one it is.
func TestOracleSelection(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		kind string
		host string
		ext  string
		err  string
	}{
		{"ts", Config{Pi: "pi", Pig: "pig", TS: "o.ts", Go: "port"}, OracleTS, "pi", "o.ts", ""},
		{"go original", Config{Pig: "pig", GoOracle: "orig", Go: "port"}, OracleGoUpstream, "pig", "orig", ""},
		{"self", Config{Pig: "pig", Go: "port", Self: true}, OracleSelf, "pig", "port", ""},
		{"ts wins over go original", Config{Pi: "pi", Pig: "pig", TS: "o.ts", GoOracle: "orig"}, "", "", "", "one original"},
		{"nothing", Config{Pig: "pig", Go: "port"}, "", "", "", "no oracle"},
		{"ts without pi", Config{Pig: "pig", TS: "o.ts", Go: "port"}, "", "", "", "pi executable"},
		{"self with an original", Config{Pig: "pig", Go: "port", GoOracle: "orig", Self: true}, "", "", "", "one original"},
	}
	for _, c := range cases {
		lane, kind, err := c.cfg.oracle()
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || kind != c.kind || lane.Host != c.host || lane.Ext != c.ext {
			t.Errorf("%s: %+v %s %v", c.name, lane, kind, err)
		}
	}
	if !IsEquivalenceProof(OracleTS) || IsEquivalenceProof(OracleSelf) || !IsEquivalenceProof(OracleGoUpstream) {
		t.Error("only a self-recorded golden trace is not an equivalence proof")
	}
	if n := goldenNote(OracleSelf); !strings.Contains(n, "self-recorded") || !strings.Contains(n, "not equivalence") {
		t.Errorf("self note = %q", n)
	}
}

func TestPortGapResultBlocksInternalImportsAndAcceptsApproval(t *testing.T) {
	dir := writeTree(t, map[string]string{"ext.go": "package x\nimport (\n\t\"os\"\n\tsdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n)\nfunc Extension() *sdk.Extension { os.Exit(1); return nil }\n"})
	r, err := (Config{Go: dir}).portGapResult()
	if err != nil || r.Pass || r.Scenario != PortGapScenario || !strings.Contains(r.Detail, "os.Exit") {
		t.Fatalf("hazard must fail: %v %+v", err, r)
	}
	r, _ = (Config{Go: dir, AcceptedGaps: map[string]string{"os.Exit": "owner, #1"}}).portGapResult()
	if !r.Pass || !strings.Contains(r.Detail, "ACCEPTED") {
		t.Errorf("accepted hazard: %+v", r)
	}
}

func TestExecCoverageScansTheGoOracleToo(t *testing.T) {
	orig := writeTree(t, map[string]string{"o.go": "package o\nfunc f(ctx sdk.Context) { ctx.Exec(\"herdr\", nil) }\n"})
	port := writeTree(t, map[string]string{"p.go": "package p\nfunc f(ctx sdk.Context) { ctx.Exec(\"git\", nil) }\n"})
	scs := []*Scenario{{Name: "a", Commands: map[string]CommandSpec{"git": {}}}}
	if r, _ := execCoverage(scs, "", orig, port); r.Pass || !strings.Contains(r.Detail, `"herdr"`) {
		t.Errorf("the original's undeclared command passed: %+v", r)
	}
}

// A mutant that removes a dialog leaves every later dialog step waiting for its full timeout:
// the review's red run took 6.5 minutes on a stub. Mutants get a shorter default step timeout
// (a hung mutant is a killed mutant); an explicit --step-timeout wins.
func TestMutationStepTimeoutDefaultsShorterAndHonorsAnExplicitValue(t *testing.T) {
	if got := (Options{}).step(); got != 60*time.Second {
		t.Errorf("default step timeout = %s", got)
	}
	if got := mutantOptions(Options{}).step(); got != 20*time.Second {
		t.Errorf("mutant default = %s, want 20s", got)
	}
	if got := mutantOptions(Options{StepTimeout: 90 * time.Second}).step(); got != 90*time.Second {
		t.Errorf("explicit timeout overridden: %s", got)
	}
}

// pigpen-pig-snake: `mutate --unit` printed "KILLED unit test: go: downloading go1.26" for 84 of
// 84 mutants. A version-manager shim had failed to start the toolchain, the tests never ran, and a
// non-zero exit was taken as a failing test. Only a reported test failure is a kill; anything else
// is INVALID, and the unmutated port's own tests must pass before any mutant is judged.
func fakeGo(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestUnitRunCountsOnlyAReportedTestFailureAsAKill(t *testing.T) {
	dir := t.TempDir()
	u := UnitTest{SDKDir: t.TempDir(), Args: []string{"test", "./..."}}

	fakeGo(t, "echo 'go: downloading go1.26 (linux/amd64)'; exit 1")
	if failed, _, err := u.run(dir, nil); failed || err == nil || !strings.Contains(err.Error(), "did not run the tests") {
		t.Errorf("a toolchain failure was counted as a kill: failed=%v err=%v", failed, err)
	}

	fakeGo(t, "echo '--- FAIL: TestX (0.00s)'; echo FAIL; echo 'FAIL\texample.com/x\t0.01s'; exit 1")
	if failed, detail, err := u.run(dir, nil); !failed || err != nil || !strings.Contains(detail, "TestX") {
		t.Errorf("a failing test was not a kill: %v %q %v", failed, detail, err)
	}

	fakeGo(t, "echo 'panic: boom'; echo 'FAIL\texample.com/x\t0.01s'; exit 1")
	if failed, _, err := u.run(dir, nil); !failed || err != nil {
		t.Errorf("a panicking test package must be a kill: %v %v", failed, err)
	}

	fakeGo(t, "echo ok; exit 0")
	if failed, _, err := u.run(dir, nil); failed || err != nil {
		t.Errorf("passing tests: %v %v", failed, err)
	}
}

func TestMutateRefusesToJudgeMutantsWhenTheUnmutatedPortsTestsFail(t *testing.T) {
	port := writeTree(t, map[string]string{"x.go": "package x\nvar a = 1\n", "go.mod": "module x\n"})
	fakeGo(t, "echo 'go: downloading go1.26 (linux/amd64)'; exit 1")
	unit := &UnitTest{SDKDir: t.TempDir(), Args: []string{"test", "./..."}}
	_, err := Mutate([]*Scenario{{Name: "a"}}, Config{Go: port}, t.TempDir(), []Mutation{{Name: "m", File: "x.go", Find: "a = 1", Replace: "a = 2"}}, unit)
	if err == nil || !strings.Contains(err.Error(), "unmutated") {
		t.Errorf("err = %v, want the baseline refusal", err)
	}
}

func TestUnitRunMapsTheSDKWithAReplace(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(t.TempDir(), "gowork.txt")
	fakeGo(t, "cat \"$GOWORK\" > "+rec+"; exit 0")
	u := UnitTest{SDKDir: "/the/sdk", Args: []string{"test", "./..."}}
	sibling := t.TempDir()
	if _, _, err := u.run(dir, []string{sibling}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(rec)
	if !strings.Contains(string(b), "replace github.com/MichaelKinsy/PiG/extensions/sdk => /the/sdk") || strings.Contains(string(b), "\t/the/sdk\n") || !strings.Contains(string(b), "\t"+sibling+"\n") {
		t.Errorf("go.work:\n%s", b)
	}
}

// pigpen-acp: `pigeq gaps --ts` on pi-acp (a standalone adapter, not an extension) reported
// `PARTIAL ctx.model` for `const model = state?.model` and "no gaps" for a file with no Pi API
// use at all, which reads as a pass. A source that is not a Pi extension gets no gap verdict.
func TestGapScanIgnoresSourcesThatAreNotPiExtensions(t *testing.T) {
	adapter := "import { spawn } from 'node:child_process';\nexport function run(state) {\n  const model = state?.model;\n  const ctx = { model };\n  return ctx.model;\n}\n"
	if gaps := ScanGaps(adapter, ""); len(gaps) != 0 {
		t.Errorf("a non-extension produced gaps: %+v", gaps)
	}
	if LooksLikeExtension(adapter) {
		t.Error("an adapter was classified as an extension")
	}
	ext := "import type { ExtensionAPI } from '@earendil-works/pi-coding-agent';\nexport default function (pi: ExtensionAPI) {\n  pi.on('session_start', async (_e, ctx) => { const m = ctx.model; });\n}\n"
	if !LooksLikeExtension(ext) {
		t.Error("an extension was not recognised")
	}
	if g := symbols(ScanGaps(ext, "")); g["ctx.model"].Severity != "partial" {
		t.Errorf("ctx.model in a real extension must still be reported: %+v", g)
	}
	// A helper file of a multi-file extension has no default export but takes pi.
	helper := "export function wire(pi) { pi.events.on('x', () => {}); }\n"
	if !LooksLikeExtension(helper) || len(ScanGaps(helper, "")) == 0 {
		t.Error("a helper that uses pi.* must still be scanned")
	}
}

func TestGapsPathSaysWhenNoSourceIsAnExtension(t *testing.T) {
	dir := writeTree(t, map[string]string{"src/a.ts": "export const x = state?.model;\n", "src/b.ts": "export function f(ctx) { return ctx.model; }\n"})
	gaps, err := ScanGapsPath(dir, "")
	if err != nil || len(gaps) != 0 {
		t.Fatalf("%v %+v", err, gaps)
	}
	ok, err := PathLooksLikeExtension(dir)
	if err != nil || ok {
		t.Errorf("PathLooksLikeExtension = %v %v", ok, err)
	}
	if msg := NotAnExtensionNote(); !strings.Contains(msg, "not a Pi extension") || !strings.Contains(msg, "kind") {
		t.Errorf("note = %q", msg)
	}
}

// pigpen-a2a: 101 mutants of an adapter ran serially (~35 minutes, split into shards by hand),
// a mutant that removes a timeout hung `go test` for its default ten minutes, an adapter has no
// scenarios to check mutants against, and `--go .` named every mutant copy ".".
func TestNewUnitTestBoundsEachRunWithATimeout(t *testing.T) {
	u := NewUnitTest("/sdk")
	joined := strings.Join(u.Args, " ")
	if u.SDKDir != "/sdk" || !strings.Contains(joined, "-timeout=180s") || !strings.Contains(joined, "-count=1") || !strings.HasSuffix(joined, "./...") {
		t.Errorf("args = %v", u.Args)
	}
}

func mutantsPort(t *testing.T) (string, []Mutation) {
	port := writeTree(t, map[string]string{"x.go": "package x\nvar a = 1\nvar b = 2\nvar c = 3\nvar d = 4\n", "go.mod": "module x\n"})
	return port, []Mutation{
		{Name: "a", File: "x.go", Find: "a = 1", Replace: "a = 9"}, {Name: "b", File: "x.go", Find: "b = 2", Replace: "b = 9"},
		{Name: "c", File: "x.go", Find: "c = 3", Replace: "c = 9"}, {Name: "d", File: "x.go", Find: "d = 4", Replace: "d = 9"},
	}
}

func TestMutateUnitOnlyNeedsNoScenariosAndReportsSurvivors(t *testing.T) {
	port, muts := mutantsPort(t)
	// The baseline passes; a mutant of `b` fails the tests, the others survive.
	fakeGo(t, `if grep -q 'b = 9' x.go; then echo '--- FAIL: TestB (0.00s)'; echo 'FAIL	x	0.01s'; exit 1; fi; echo ok; exit 0`)
	res, err := Mutate(nil, Config{Go: port, UnitOnly: true}, "", muts, NewUnitTest(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	var killed, survived []string
	for _, r := range res {
		if r.Killed {
			killed = append(killed, r.Mutation.Name)
		} else if !r.Invalid {
			survived = append(survived, r.Mutation.Name)
		}
	}
	if !reflect.DeepEqual(killed, []string{"b"}) || !reflect.DeepEqual(survived, []string{"a", "c", "d"}) {
		t.Errorf("killed %v survived %v: %+v", killed, survived, res)
	}
	if _, err := Mutate(nil, Config{Go: port, UnitOnly: true}, "", muts, nil); err == nil {
		t.Error("unit-only without a unit runner was accepted")
	}
}

func TestMutateRunsMutantsInParallelAndKeepsTheirOrder(t *testing.T) {
	port, muts := mutantsPort(t)
	fakeGo(t, `sleep 0.4; if grep -q '= 9' x.go; then echo '--- FAIL: TestX (0.00s)'; exit 1; fi; exit 0`)
	start := time.Now()
	res, err := Mutate(nil, Config{Go: port, UnitOnly: true, Jobs: 4}, "", muts, NewUnitTest(t.TempDir()))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.Mutation.Name)
		if !r.Killed {
			t.Errorf("%s not killed: %+v", r.Mutation.Name, r)
		}
	}
	if !reflect.DeepEqual(names, []string{"a", "b", "c", "d"}) {
		t.Errorf("results out of order: %v", names)
	}
	// baseline (0.4 s) + four mutants: serial is at least 2.0 s, four jobs about 0.8 s.
	if elapsed > 1500*time.Millisecond {
		t.Errorf("4 jobs took %s: mutants did not run in parallel", elapsed)
	}
}

func TestMutantCopyKeepsTheExtensionDirectoryNameWhenGivenADot(t *testing.T) {
	parent := t.TempDir()
	ext := filepath.Join(parent, "my-ext")
	_ = os.MkdirAll(ext, 0o755)
	_ = os.WriteFile(filepath.Join(ext, "x.go"), []byte("package x\n"), 0o644)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(ext); err != nil {
		t.Fatal(err)
	}
	dir, _, cleanup, err := prepareMutantTree(".")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Base(dir) != "my-ext" {
		t.Errorf("mutant copy is named %q, want my-ext (an extension's identity is its directory name)", filepath.Base(dir))
	}
}
