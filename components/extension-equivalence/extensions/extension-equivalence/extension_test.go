package extension_equivalence_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// An API the Go SDK lacks blocks. (PiG 0.4.x implements pi.events for Go; an event the host never emits is
// still missing.)
func TestGapsToolFlagsAMissingAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.ts"), []byte(`export default (pi) => { pi.on("cache_warming_decision", () => {}); };`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_gaps", map[string]any{"ts": "x.ts"})
	if failure != "" {
		t.Fatal(failure)
	}
	out := text(t, raw)
	if !strings.HasPrefix(out, "FAIL\n") || !strings.Contains(out, `MISSING pi.on("cache_warming_decision")`) {
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
	_ = os.WriteFile(filepath.Join(dir, "ext", "lib", "bus.ts"), []byte(`export const wire = (pi) => { pi.on("cache_warming_decision", () => {}); };`), 0o644)
	h := StartHost(t, equivalence.Extension(), HostOptions{Mode: "rpc", Cwd: dir})
	raw, failure := h.Tool("equivalence_gaps", map[string]any{"ts": "ext"})
	if failure != "" {
		t.Fatal(failure)
	}
	if out := text(t, raw); !strings.HasPrefix(out, "FAIL\n") || !strings.Contains(out, "lib/bus.ts") {
		t.Errorf("gaps report = %s", out)
	}
}
