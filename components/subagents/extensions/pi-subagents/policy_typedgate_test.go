package pi_subagents

import (
	"path/filepath"
	"testing"
)

func typedGateRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// printing is a gate command that prints exactly text on stdout and exits 0.
func printing(text string) string { return "printf '%s' " + shellQuote(text) }

func shellQuote(s string) string {
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
		} else {
			out += string(r)
		}
	}
	return out + "'"
}

func runGate(t *testing.T, gate any, cwd string, artifactsDir, runID string) *acceptanceLedger {
	t.Helper()
	norm, msg := normalizeGateAcceptance(gate, nil)
	if msg != "" {
		t.Fatal(msg)
	}
	a := resolve(t, acceptanceRun{AgentName: "reviewer", Task: "Review the report.", Explicit: norm, AgentContract: true})
	return evaluateAcceptance(acceptanceEval{Acceptance: a, Output: "done", Cwd: cwd, ReportOptional: true, ArtifactsDir: artifactsDir, RunID: runID})
}

func TestGateObjectForm(t *testing.T) {
	tw(t, "typed-gate-output", "parses the string form and the object form", func(t *testing.T) {
		g, msg := parseGateInput(" npm test ")
		equal(t, [2]any{g, msg}, [2]any{gateObjectInput{Command: "npm test"}, ""}, "string")
		g, msg = parseGateInput(js(t, `{"command":"classify.sh","output":"json","timeoutMs":5000}`))
		equal(t, [2]any{g, msg}, [2]any{gateObjectInput{Command: "classify.sh", Output: "json", TimeoutMs: 5000}, ""}, "object")
		got, msg := normalizeGateAcceptance(js(t, `{"command":"classify.sh","output":"json","schema":{"type":"object"}}`), nil)
		equal(t, msg, "", "error")
		equal(t, marshalJSON(got, ""), `{"level":"verified","verify":[{"id":"gate","command":"classify.sh","output":"json","schema":{"type":"object"}}]}`, "normalized")
	})
	tw(t, "typed-gate-output", "rejects malformed object gates with a field-level message", func(t *testing.T) {
		for _, c := range []struct{ gate, re string }{
			{`{}`, `non-empty command string`},
			{`{"command":" "}`, `non-empty command string`},
			{`{"command":"x","output":"yaml"}`, `gate\.output must be "json"`},
			{`{"command":"x","schema":{"type":"object"}}`, `gate\.schema requires gate\.output`},
			{`{"command":"x","output":"json","schema":"nope"}`, `gate\.schema must be a JSON Schema object`},
			{`{"command":"x","timeoutMs":0}`, `gate\.timeoutMs must be an integer`},
			{`{"command":"x","cwd":"."}`, `gate\.cwd is not supported`},
			{`["x"]`, `non-empty command string`},
		} {
			_, msg := parseGateInput(js(t, c.gate))
			if msg == "" {
				t.Errorf("%s accepted", c.gate)
			}
			polMatches(t, msg, c.re)
		}
	})
	tw(t, "typed-gate-output", "detects typed verify commands in either spelling", func(t *testing.T) {
		equal(t, acceptanceHasTypedVerify(nil), false, "undefined")
		equal(t, acceptanceHasTypedVerify("checked"), false, "string")
		equal(t, acceptanceHasTypedVerify(js(t, `{"level":"verified","verify":[{"id":"v","command":"x"}]}`)), false, "untyped")
		equal(t, acceptanceHasTypedVerify(js(t, `{"level":"verified","verify":[{"id":"v","command":"x","output":"json"}]}`)), true, "typed")
		gate, _ := normalizeGateAcceptance(js(t, `{"command":"x","output":"json"}`), nil)
		equal(t, acceptanceHasTypedVerify(gate), true, "gate")
	})
	tw(t, "typed-gate-output", "accepts output and schema on explicit acceptance.verify entries", func(t *testing.T) {
		equal(t, validateAcceptanceInput(js(t, `{"level":"verified","verify":[{"id":"v","command":"x","output":"json","schema":{"type":"object"}}]}`), "acceptance"), []string{}, "valid")
		polMatches(t, joinLines(validateAcceptanceInput(js(t, `{"level":"verified","verify":[{"id":"v","command":"x","output":"text"}]}`), "acceptance")), `output must be "json"`)
		polMatches(t, joinLines(validateAcceptanceInput(js(t, `{"level":"verified","verify":[{"id":"v","command":"x","schema":{}}]}`), "acceptance")), `schema requires output`)
	})
}

func TestTypedGateOutput(t *testing.T) {
	gate := func(t *testing.T, command string, rest string) any {
		o := `{"command":` + marshalJSON(command, "") + `,"output":"json"` + rest + `}`
		return js(t, o)
	}
	tw(t, "typed-gate-output", "parses a passing json gate's stdout into the verify run and exposes it", func(t *testing.T) {
		skipWindows(t)
		l := runGate(t, gate(t, printing(`{"verdict":"blocked","score":0.9}`), ""), typedGateRepo(t), "", "")
		equal(t, l.Status, "verified", "status")
		equal(t, l.VerifyRuns[0].Status, "passed", "run")
		equal(t, marshalJSON(l.VerifyRuns[0].StructuredOutput, ""), `{"verdict":"blocked","score":0.9}`, "structured")
		v, ok := typedVerifyOutput(l)
		equal(t, [2]any{marshalJSON(v, ""), ok}, [2]any{`{"verdict":"blocked","score":0.9}`, true}, "typedVerifyOutput")
	})
	tw(t, "typed-gate-output", "validates the parsed stdout against gate.schema", func(t *testing.T) {
		skipWindows(t)
		cwd := typedGateRepo(t)
		schema := `,"schema":{"type":"object","properties":{"verdict":{"type":"string","enum":["ok","blocked"]}},"required":["verdict"],"additionalProperties":false}`
		good := runGate(t, gate(t, printing(`{"verdict":"ok"}`), schema), cwd, "", "")
		equal(t, good.Status, "verified", "good")
		v, _ := typedVerifyOutput(good)
		equal(t, marshalJSON(v, ""), `{"verdict":"ok"}`, "good value")
		bad := runGate(t, gate(t, printing(`{"verdict":"maybe"}`), schema), cwd, "", "")
		equal(t, bad.Status, "rejected", "bad")
		equal(t, bad.VerifyRuns[0].Status, "failed", "bad run")
		polMatches(t, bad.VerifyRuns[0].StructuredOutputError, `does not match gate\.schema`)
		polMatches(t, acceptanceFailureMessage(bad), `verification 'gate' failed: output: "json" stdout does not match gate\.schema`)
		_, ok := typedVerifyOutput(bad)
		equal(t, ok, false, "bad exposes nothing")
	})
	tw(t, "typed-gate-output", "fails the gate on invalid or empty stdout instead of dropping the verdict", func(t *testing.T) {
		skipWindows(t)
		cwd := typedGateRepo(t)
		invalid := runGate(t, gate(t, printing("WRITER-FIX report=x"), ""), cwd, "", "")
		equal(t, invalid.Status, "rejected", "invalid")
		polMatches(t, invalid.VerifyRuns[0].StructuredOutputError, `not valid JSON`)
		empty := runGate(t, gate(t, printing(""), ""), cwd, "", "")
		equal(t, empty.Status, "rejected", "empty")
		polMatches(t, empty.VerifyRuns[0].StructuredOutputError, `printed nothing`)
	})
	tw(t, "typed-gate-output", "leaves string gates and non-json verify commands untouched", func(t *testing.T) {
		skipWindows(t)
		l := runGate(t, printing(`{"verdict":"blocked"}`), typedGateRepo(t), "", "")
		equal(t, l.Status, "verified", "status")
		equal(t, l.VerifyRuns[0].HasStructuredOutput, false, "structured")
		_, ok := typedVerifyOutput(l)
		equal(t, ok, false, "typedVerifyOutput")
	})
	tw(t, "typed-gate-output", "never memoizes a typed gate, because its input can change without the tracked tree changing", func(t *testing.T) {
		skipWindows(t)
		cwd := typedGateRepo(t)
		dir := filepath.Join(cwd, ".artifacts")
		first := runGate(t, gate(t, printing(`{"n":1}`), ""), cwd, dir, "typed-memo")
		second := runGate(t, gate(t, printing(`{"n":1}`), ""), cwd, dir, "typed-memo")
		equal(t, first.VerifyRuns[0].Memoized == nil, true, "first")
		equal(t, second.VerifyRuns[0].Memoized == nil, true, "second")
		equal(t, second.VerifyRuns[0].CacheKey, "", "cache key")
	})
}

func TestAcceptanceCompactionSkip(t *testing.T) {
	tskip(t, "acceptance-compaction", "exact acceptance survives actual SDK split-turn compaction: ${host}/${systemPromptMode}/${structured ? \"structured\" : \"fenced\"}", "drives the original's in-process Pi SDK session (createDefaultChildSessionFactory over @earendil-works/pi-coding-agent 0.86.1) through a real split-turn compaction; the Go port's children are pig processes, which compact inside pig itself")
}
