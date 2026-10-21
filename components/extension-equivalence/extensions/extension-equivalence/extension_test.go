package extension_equivalence_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	equivalence "github.com/MichaelKinsy/pigpen/extension-equivalence"
)

func text(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var r struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Content) == 0 {
		t.Fatalf("result %s", raw)
	}
	return r.Content[0].Text
}

func TestRegistersItsTools(t *testing.T) {
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc"})
	for _, name := range []string{"equivalence_gaps", "equivalence_run", "equivalence_diff", "equivalence_twins"} {
		if !h.tools[name] {
			t.Errorf("tool %s not registered", name)
		}
	}
}

// An API the Go SDK lacks blocks. (PiG 0.5.0 implements pi.events and cache_warming_decision for Go;
// ctx.newSession({ setup }) is still missing.)
const missingAPISource = `export default (pi) => { pi.registerCommand("c", { handler: async (_a, ctx) => { await ctx.newSession({ setup: async () => {} }); } }); };`
const missingAPISymbol = "ctx.newSession(options.setup)"

func TestGapsToolFlagsAMissingAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.ts"), []byte(missingAPISource), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_gaps", map[string]any{"ts": "x.ts"})
	if failure != "" {
		t.Fatal(failure)
	}
	out := text(t, raw)
	if !strings.HasPrefix(out, "FAIL\n") || !strings.Contains(out, "MISSING "+missingAPISymbol) {
		t.Errorf("gaps report = %s", out)
	}
}

func TestGapsToolPassesACleanExtension(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.ts"), []byte(`export default (pi) => { pi.on("session_start", (_e, ctx) => ctx.ui.notify("x", "info")); };`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_gaps", map[string]any{"ts": "x.ts"})
	if failure != "" || !strings.HasPrefix(text(t, raw), "PASS\n") {
		t.Errorf("clean extension: %q %s", failure, raw)
	}
}

func TestGapsToolScansGoSource(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nimport \"os\"\nfunc f() { os.Exit(1) }\n"), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_gaps", map[string]any{"go": "."})
	if failure != "" || !strings.HasPrefix(text(t, raw), "FAIL\n") || !strings.Contains(text(t, raw), "os.Exit") {
		t.Errorf("go gaps: %q %s", failure, raw)
	}
	if _, failure := h.Tool("equivalence_gaps", map[string]any{"go": ".", "ts": "x.ts"}); failure == "" {
		t.Error("ts and go together were accepted")
	}
}

func TestRunToolNamesTheOracleChoices(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"name":"a","steps":[{"name":"s","rpc":{"type":"get_state"}}]}`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	_, failure := h.Tool("equivalence_run", map[string]any{"mode": "record", "scenarios": "s.json", "golden": "g"})
	if !strings.Contains(failure, "goOracle") || !strings.Contains(failure, "self") {
		t.Errorf("record without an oracle: %q", failure)
	}
}

func TestTwinsToolListsAndChecks(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "up"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "up", "a.test.mjs"), []byte("test('one', () => {});\ntest('two', () => {});\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "port"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "port", "x_test.go"), []byte("package x\nfunc f(t *testing.T) { tw(t, f, \"one\", nil) }\n"), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_twins", map[string]any{"mode": "list", "tests": "up"})
	if failure != "" || !strings.Contains(text(t, raw), `"one"`) {
		t.Fatalf("list: %q %s", failure, raw)
	}
	_ = os.WriteFile(filepath.Join(dir, "ledger.json"), []byte(`{"a":["one","two"]}`), 0o644)
	raw, failure = h.Tool("equivalence_twins", map[string]any{"mode": "check", "ledger": "ledger.json", "go": "port"})
	out := text(t, raw)
	if failure != "" || !strings.HasPrefix(out, "FAIL\n") || !strings.Contains(out, "MISSING a: two") {
		t.Errorf("check: %q %s", failure, out)
	}
	raw, failure = h.Tool("equivalence_twins", map[string]any{"mode": "check", "ledger": "ledger.json", "go": "port", "deferred": map[string]any{"a": "the second case is a later slice"}})
	if out = text(t, raw); failure != "" || !strings.HasPrefix(out, "PASS\n") || !strings.Contains(out, "DEFERRED a (1 cases): the second case is a later slice") {
		t.Errorf("deferred: %q %s", failure, out)
	}
	if _, failure := h.Tool("equivalence_twins", map[string]any{"mode": "check", "go": "port"}); failure == "" {
		t.Error("check without a ledger was accepted")
	}
}

func TestGapsToolRequiresAPath(t *testing.T) {
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc"})
	if _, failure := h.Tool("equivalence_gaps", map[string]any{}); failure == "" {
		t.Error("missing ts was accepted")
	}
}

func TestDiffTool(t *testing.T) {
	dir := t.TempDir()
	trace := func(name, data string) {
		body := `{"kind":"header","scenario":"s","lane":"l","host":"h","extension":"e","normalizer":"v1"}` + "\n" + `{"step":"1","ch":"ui","data":` + data + `}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	trace("a.jsonl", `{"m":1}`)
	trace("b.jsonl", `{"m":1}`)
	trace("c.jsonl", `{"m":2}`)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, _ := h.Tool("equivalence_diff", map[string]any{"want": "a.jsonl", "got": "b.jsonl"})
	if !strings.HasPrefix(text(t, raw), "PASS") {
		t.Errorf("identical: %s", raw)
	}
	raw, _ = h.Tool("equivalence_diff", map[string]any{"want": "a.jsonl", "got": "c.jsonl"})
	if out := text(t, raw); !strings.HasPrefix(out, "FAIL") || !strings.Contains(out, "first difference at event 0") {
		t.Errorf("different: %s", out)
	}
}

func TestRunToolValidatesItsMode(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"name":"s","steps":[{"rpc":{"type":"get_state"}}]}`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	if _, failure := h.Tool("equivalence_run", map[string]any{"mode": "bogus", "scenarios": "s.json"}); failure == "" {
		t.Error("unknown mode accepted")
	}
	if _, failure := h.Tool("equivalence_run", map[string]any{"mode": "check", "scenarios": "s.json"}); !strings.Contains(failure, "pig is required") {
		t.Errorf("check without pig: %q", failure)
	}
}

func TestRunToolMutateModeNeedsAMutationList(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"name":"s","steps":[{"rpc":{"type":"get_state"}}]}`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	_, failure := h.Tool("equivalence_run", map[string]any{"mode": "mutate", "scenarios": "s.json", "pig": "/bin/true", "go": ".", "golden": "."})
	if !strings.Contains(failure, "mutations is required") {
		t.Errorf("mutate without mutations: %q", failure)
	}
}

func TestGapsToolScansAMultiFileExtension(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "ext", "lib"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "ext", "index.ts"), []byte(`import { wire } from "./lib/bus"; export default (pi) => wire(pi);`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "ext", "lib", "bus.ts"), []byte(`export const wire = (pi) => { pi.registerCommand("c", { handler: async (_a, ctx) => { await ctx.newSession({ setup: async () => {} }); } }); };`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_gaps", map[string]any{"ts": "ext"})
	if failure != "" {
		t.Fatal(failure)
	}
	if out := text(t, raw); !strings.HasPrefix(out, "FAIL\n") || !strings.Contains(out, "lib/bus.ts") {
		t.Errorf("gaps report = %s", out)
	}
}

// The command line's builtin, keep, unit and lane are tool parameters too (README: the tool and pigeq are the
// same code, so a person and an agent get identical verdicts).

func scenarioFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"name":"s","steps":[{"rpc":{"type":"get_state"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeHost is a `pig` that reads one request and exits: a lane on it ends with a recorded failure, which is all
// the parameter tests need (the lane machinery itself is tested in eq).
func fakeHost(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake host is a POSIX shell script")
	}
	bin := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nread x\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestRunToolBuiltinChecksABinaryWithoutAGoDirectory(t *testing.T) {
	dir := scenarioFile(t)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	// Without builtin the port directory is required.
	if _, failure := h.Tool("equivalence_run", map[string]any{"mode": "check", "scenarios": "s.json", "pig": "/bin/true", "golden": "."}); !strings.Contains(failure, "go is required") {
		t.Errorf("check without go or builtin: %q", failure)
	}
	// With builtin the port is compiled into pig: no go directory is asked for.
	_, failure := h.Tool("equivalence_run", map[string]any{"mode": "check", "builtin": true, "scenarios": "s.json", "pig": "/bin/true", "golden": "."})
	if strings.Contains(failure, "go is required") {
		t.Errorf("check --builtin asked for a go directory: %q", failure)
	}
	if _, failure := h.Tool("equivalence_run", map[string]any{"mode": "check", "builtin": true, "scenarios": "s.json", "golden": "."}); !strings.Contains(failure, "pig is required") {
		t.Errorf("check --builtin without pig: %q", failure)
	}
}

func TestRunToolBuiltinCannotMutateOrRun(t *testing.T) {
	dir := scenarioFile(t)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	_, failure := h.Tool("equivalence_run", map[string]any{"mode": "mutate", "builtin": true, "scenarios": "s.json", "pig": "/bin/true", "go": ".", "golden": ".", "mutations": "m.json"})
	if !strings.Contains(failure, "cannot mutate a built binary") {
		t.Errorf("mutate with builtin: %q", failure)
	}
	_, failure = h.Tool("equivalence_run", map[string]any{"mode": "run", "builtin": true, "scenarios": "s.json", "pig": "/bin/true", "go": "."})
	if !strings.Contains(failure, "builtin applies to mode check and record") {
		t.Errorf("run with builtin: %q", failure)
	}
}

func TestRunToolBuiltinRecordsASelfBaselineWithoutAGoDirectory(t *testing.T) {
	dir := scenarioFile(t)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	_, failure := h.Tool("equivalence_run", map[string]any{"mode": "record", "builtin": true, "self": true, "scenarios": "s.json", "pig": fakeHost(t), "golden": "g"})
	if strings.Contains(failure, "go is required") || strings.Contains(failure, "needs a go") || strings.Contains(failure, "--go") {
		t.Errorf("record --self --builtin asked for a go directory: %q", failure)
	}
	// Without builtin, a self baseline names the port it records.
	_, failure = h.Tool("equivalence_run", map[string]any{"mode": "record", "self": true, "scenarios": "s.json", "pig": fakeHost(t), "golden": "g"})
	if failure == "" {
		t.Error("record --self without go or builtin was accepted")
	}
}

func TestRunToolLaneRunsOneHostAndWritesItsTraces(t *testing.T) {
	dir := scenarioFile(t)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	for name, params := range map[string]map[string]any{
		"no host": {"mode": "lane", "scenarios": "s.json", "bin": "/bin/true", "ext": "x.ts"},
		"no bin":  {"mode": "lane", "scenarios": "s.json", "host": "pig", "ext": "x.ts"},
		"no ext":  {"mode": "lane", "scenarios": "s.json", "host": "pig", "bin": "/bin/true"},
	} {
		if _, failure := h.Tool("equivalence_run", params); !strings.Contains(failure, "is required for this mode") {
			t.Errorf("%s: %q", name, failure)
		}
	}
	out := filepath.Join(dir, "traces")
	raw, failure := h.Tool("equivalence_run", map[string]any{"mode": "lane", "scenarios": "s.json", "host": "pig", "bin": fakeHost(t), "ext": ".", "out": out})
	if failure != "" {
		t.Fatalf("lane: %s", failure)
	}
	// The host died, so the lane failed; it is reported as a lane failure, with the trace written, not as a tool error.
	if r := text(t, raw); !strings.HasPrefix(r, "FAIL\n") || !strings.Contains(r, "lane failed while running") {
		t.Errorf("report %q", r)
	}
	if files, _ := filepath.Glob(filepath.Join(out, "*")); len(files) != 1 {
		t.Errorf("one trace under out, got %v", files)
	}
}

func TestRunToolKeepNamesTheRunDirectory(t *testing.T) {
	dir := scenarioFile(t)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	run := func(keep any) string {
		params := map[string]any{"mode": "lane", "scenarios": "s.json", "host": "pig", "bin": fakeHost(t), "ext": ".", "out": filepath.Join(t.TempDir(), "o")}
		if keep != nil {
			params["keep"] = keep
		}
		raw, failure := h.Tool("equivalence_run", params)
		if failure != "" {
			t.Fatal(failure)
		}
		return text(t, raw)
	}
	report := run(true)
	_, rest, ok := strings.Cut(report, "eq: keeping ")
	if !ok {
		t.Fatalf("keep must say where the run directory is: %q", report)
	}
	kept := strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
	t.Cleanup(func() { _ = os.RemoveAll(kept) })
	if fi, err := os.Stat(kept); err != nil || !fi.IsDir() {
		t.Errorf("the kept run directory %q does not exist: %v", kept, err)
	}
	if report := run(nil); strings.Contains(report, "eq: keeping") {
		t.Errorf("a run without keep named a directory: %q", report)
	}
}

func TestRunToolUnitNeedsAnSDKDirectory(t *testing.T) {
	t.Setenv("PIG_SDK_DIR", "")
	dir := scenarioFile(t)
	_ = os.WriteFile(filepath.Join(dir, "m.json"), []byte(`[{"name":"a","file":"f.go","find":"x","replace":"y"}]`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	_, failure := h.Tool("equivalence_run", map[string]any{"mode": "mutate", "unit": true, "scenarios": "s.json", "pig": "/bin/true", "go": ".", "golden": ".", "mutations": "m.json"})
	if !strings.Contains(failure, "unit needs sdkDir") {
		t.Errorf("unit without an SDK directory: %q", failure)
	}
	// unitOnly says so under its own name.
	_, failure = h.Tool("equivalence_run", map[string]any{"mode": "mutate", "unitOnly": true, "go": ".", "mutations": "m.json"})
	if !strings.Contains(failure, "unitOnly needs sdkDir") {
		t.Errorf("unitOnly without an SDK directory: %q", failure)
	}
}
