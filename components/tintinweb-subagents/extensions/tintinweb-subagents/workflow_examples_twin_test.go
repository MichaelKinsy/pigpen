package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Twins of test/workflow-claude-code-compat.test.ts and test/workflow-examples.test.ts. The examples are the
// original's own files, vendored byte for byte under port/oracle/examples/workflows (docs/workflows.md links them).

const examplesDir = "../../port/oracle/examples/workflows"

func exampleNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func readExample(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// compatHost answers every spawn from a function and loads one nested workflow.
type compatHost struct {
	reply  func(*workflowSpawnRequest) workflowSpawnResult
	loaded string
}

func (c compatHost) SpawnAgent(r *workflowSpawnRequest) (workflowSpawnResult, error) {
	return c.reply(r), nil
}
func (c compatHost) AbortAgent(string) {}
func (c compatHost) LoadWorkflow(workflowRef) (workflowScriptSource, error) {
	return workflowScriptSource{OK: true, Script: c.loaded}, nil
}

// Claude Code's canonical review-changes example, verbatim from its tool description.
const ccScript = "export const meta = {\n" +
	"  name: 'review-changes',\n" +
	"  description: 'Review changed files across dimensions, verify each finding',\n" +
	"  phases: [{ title: 'Review' }, { title: 'Verify' }],\n" +
	"}\n" +
	"const FINDINGS_SCHEMA = { type: 'object', properties: { findings: { type: 'array', items: { type: 'object', properties: { title: { type: 'string' }, file: { type: 'string' } }, required: ['title'] } } }, required: ['findings'] }\n" +
	"const VERDICT_SCHEMA = { type: 'object', properties: { isReal: { type: 'boolean' } }, required: ['isReal'] }\n" +
	"const DIMENSIONS = [{key: 'bugs', prompt: 'find bugs'}, {key: 'perf', prompt: 'find perf issues'}]\n" +
	"const results = await pipeline(\n" +
	"  DIMENSIONS,\n" +
	"  d => agent(d.prompt, {label: `review:${d.key}`, phase: 'Review', schema: FINDINGS_SCHEMA}),\n" +
	"  review => parallel(review.findings.map(f => () =>\n" +
	"    agent(`Adversarially verify: ${f.title}`, {label: `verify:${f.file}`, phase: 'Verify', schema: VERDICT_SCHEMA})\n" +
	"      .then(v => ({...f, verdict: v}))\n" +
	"  ))\n" +
	")\n" +
	"const confirmed = results.flat().filter(Boolean).filter(f => f.verdict?.isReal)\n" +
	"return { confirmed: confirmed.length, total: results.flat().length }\n"

// exampleHost answers every spawn plausibly and deterministically, keyed off the label (the schema reaches the host
// compiled, so sniffing it silently matches nothing). The payloads still have to satisfy the real schemas.
type exampleHost struct {
	mu           sync.Mutex
	spawns       []*workflowSpawnRequest
	labels       map[string]string
	gateFailsFor []string
	childDir     string
	t            *testing.T
}

func newExampleHost(t *testing.T, gateFailsFor ...string) *exampleHost {
	return &exampleHost{labels: map[string]string{}, gateFailsFor: gateFailsFor, childDir: filepath.Join(examplesDir, "lib"), t: t}
}

func (h *exampleHost) SpawnAgent(r *workflowSpawnRequest) (workflowSpawnResult, error) {
	h.mu.Lock()
	h.spawns = append(h.spawns, r)
	h.labels[r.AgentID] = r.Label
	h.mu.Unlock()
	label := r.Label
	ok := func(text string) (workflowSpawnResult, error) {
		return workflowSpawnResult{OK: true, Text: text, OutputTokens: 10}, nil
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	// Only a call that asked for a schema gets JSON.
	if r.Schema != nil {
		switch {
		case strings.HasPrefix(label, "review:"):
			return ok(js(map[string]any{"findings": []any{map[string]any{"title": label + " finding", "file": "a.ts", "severity": "low"}}}))
		case strings.HasPrefix(label, "verify:"):
			return ok(js(map[string]any{"isReal": true, "why": "reproduced"}))
		case label == "scan":
			return ok(js(map[string]any{"files": []any{"a.ts", "b.ts"}}))
		}
		// Deliberately invalid: an unhandled label fails validation rather than passing on an empty object.
		return ok("{}")
	}
	if label == "discover" {
		return ok("src/routes/a.ts\nsrc/routes/b.ts")
	}
	return ok("ok:" + label)
}

func (h *exampleHost) AbortAgent(string) {}

func (h *exampleHost) ResumeAgent(_ context.Context, _ string, prompt string, _ func(workflowResolvedInfo)) (workflowSpawnResult, error) {
	return workflowSpawnResult{OK: true, Text: "resumed:" + string([]rune(prompt)[:min(20, len([]rune(prompt)))]), OutputTokens: 10}, nil
}

// RunGate: required, not optional: the runtime fails a gate it cannot run. A rejected gate is reported here rather
// than as a failed spawn, because a child whose spawn failed is not offered as a resume target.
func (h *exampleHost) RunGate(_ context.Context, command, agentID, _ string) (workflowGateResult, error) {
	h.mu.Lock()
	label := h.labels[agentID]
	h.mu.Unlock()
	for _, f := range h.gateFailsFor {
		if f == label {
			return workflowGateResult{Output: command + ": 1 failing"}, nil
		}
	}
	return workflowGateResult{OK: true, Output: command + ": ok"}, nil
}

func (h *exampleHost) LoadWorkflow(ref workflowRef) (workflowScriptSource, error) {
	if ref.Name == "" {
		return workflowScriptSource{Message: "only `name` refs are stubbed"}, nil
	}
	for _, c := range exampleNames(h.t, h.childDir) {
		if c == ref.Name+".js" {
			return workflowScriptSource{OK: true, Script: readExample(h.t, h.childDir, c)}, nil
		}
	}
	return workflowScriptSource{Message: `no child workflow "` + ref.Name + `"`}, nil
}

func TestWorkflowClaudeCodeCompat(t *testing.T) {
	const f = "workflow-claude-code-compat"
	tw(t, f, "runs the canonical review-changes example", func(t *testing.T) {
		host := compatHost{reply: func(r *workflowSpawnRequest) workflowSpawnResult {
			text := `{"findings":[{"title":"t1","file":"a.ts"},{"title":"t2","file":"b.ts"}]}`
			if strings.HasPrefix(r.Label, "verify:") {
				text = `{"isReal":` + map[bool]string{true: "true", false: "false"}[strings.Contains(r.Label, "a.ts")] + `}`
			}
			return workflowSpawnResult{OK: true, Text: text, OutputTokens: 10}
		}}
		res, err := runWorkflow(workflowRunOptions{Script: ccScript, Host: host})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.HasError, false)
		eq(t, res.Status, "completed")
		// 2 dimensions x 2 findings verified, of which the a.ts ones are real.
		eq(t, res.Value, map[string]any{"confirmed": float64(2), "total": float64(4)})
		eq(t, res.AgentCount, 6)
	})
	host := compatHost{reply: func(*workflowSpawnRequest) workflowSpawnResult {
		return workflowSpawnResult{OK: true, Text: "ok", OutputTokens: 25}
	},
		loaded: "export const meta = { name: \"sub\", description: \"d\" };\nreturn args.n * 2;\n"}
	tw(t, f, "runs the loop-until-budget and static-scaling patterns as written", func(t *testing.T) {
		script := strings.Join([]string{"export const meta = { name: 'b', description: 'd' };", "const found = [];",
			"while (budget.total && budget.remaining() > 50_000) { found.push(await agent('find')); }",
			"const FLEET = budget.total ? Math.floor(budget.total / 100_000) : 5;", "return JSON.stringify([found.length, FLEET, budget.spent()]);"}, "\n")
		res, err := runWorkflow(workflowRunOptions{Script: script, Host: host})
		if err != nil {
			t.Fatal(err)
		}
		// No target, so the loop never runs and the fallback fleet size is used.
		eq(t, wfJSON(t, res.Value.(string)), []any{float64(0), float64(5), float64(0)})
	})
	tw(t, f, "composes a saved workflow through workflow()", func(t *testing.T) {
		script := strings.Join([]string{"export const meta = { name: 'c', description: 'd' };", "const doubled = await workflow('sub', { n: 21 });", "return doubled;"}, "\n")
		res, err := runWorkflow(workflowRunOptions{Script: script, Host: host})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Value, float64(42))
	})
}

func TestWorkflowExamples(t *testing.T) {
	const f = "workflow-examples"
	exampleFiles := exampleNames(t, examplesDir)
	childFiles := exampleNames(t, filepath.Join(examplesDir, "lib"))
	sampleArgs := map[string]any{"fan-out-audit.js": map[string]any{"root": "src/routes/"}, "compose.js": map[string]any{"root": "src/"}}
	runExample := func(t *testing.T, name string, host workflowHost) *workflowRunResult {
		t.Helper()
		opts := workflowRunOptions{Script: readExample(t, examplesDir, name), Host: host}
		if a, ok := sampleArgs[name]; ok {
			opts.Args, opts.HasArgs = a, true
		}
		res, err := runWorkflow(opts)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	tw(t, f, "ships at least the examples the guide links", func(t *testing.T) {
		// docs/workflows.md has a row per file; a deletion should break this first.
		eq(t, exampleFiles, []string{"compose.js", "fan-out-audit.js", "gated-fix.js", "review-panel.js", "structured-findings.js"})
		eq(t, childFiles, []string{"count-child.js"})
	})
	// Tier 1 — glob-driven, so a new example is covered the moment it lands.
	tw(t, f, "is recognisable as a workflow and declares a usable meta block", func(t *testing.T) {
		check := func(dir, name string) {
			t.Run(name, func(t *testing.T) {
				source := readExample(t, dir, name)
				// The same test the saved-workflow lookup applies before it will resolve a file.
				eq(t, hasMetaDeclaration(source), true)
				meta, _, err := extractMeta(source)
				if err != nil || meta.Name == "" || meta.Description == "" {
					t.Fatalf("meta %+v: %v", meta, err)
				}
			})
		}
		for _, n := range exampleFiles {
			check(examplesDir, n)
		}
		for _, n := range childFiles {
			check(filepath.Join(examplesDir, "lib"), n)
		}
	})
	// Tier 2 — the one that actually catches things.
	tw(t, f, "runs to completion against a stub host", func(t *testing.T) {
		for _, name := range exampleFiles {
			t.Run(name, func(t *testing.T) {
				res := runExample(t, name, newExampleHost(t))
				eq(t, res.HasError, false)
				eq(t, res.Status, "completed")
			})
		}
	})
	tw(t, f, "tolerates being run with no args at all", func(t *testing.T) {
		for _, name := range exampleFiles {
			t.Run(name, func(t *testing.T) {
				res, err := runWorkflow(workflowRunOptions{Script: readExample(t, examplesDir, name), Host: newExampleHost(t)})
				if err != nil {
					t.Fatal(err)
				}
				eq(t, res.HasError, false)
				eq(t, res.Status, "completed")
			})
		}
	})
	// Tier 3 — explicit values, including the fan-out width.
	tw(t, f, "fan-out-audit returns one verified finding per discovered file", func(t *testing.T) {
		res := runExample(t, "fan-out-audit.js", newExampleHost(t))
		eq(t, res.AgentCount, 5) // 1 discovery + 2 files x (audit + verify)
		eq(t, res.Value, []any{"ok:verify:src/routes/a.ts", "ok:verify:src/routes/b.ts"})
	})
	tw(t, f, "structured-findings returns validated objects, not prose", func(t *testing.T) {
		res := runExample(t, "structured-findings.js", newExampleHost(t))
		eq(t, res.AgentCount, 4) // 2 dimensions x (1 review + 1 finding verified)
		eq(t, res.Value.(map[string]any)["confirmed"], float64(2))
	})
	tw(t, f, "review-panel synthesizes once, after every lens", func(t *testing.T) {
		h := newExampleHost(t)
		res := runExample(t, "review-panel.js", h)
		eq(t, res.AgentCount, 4) // 3 lenses + 1 synthesis
		eq(t, res.Value.(map[string]any)["reviewed"], float64(3))
		low := 0
		var synth string
		for _, s := range h.spawns {
			if s.Effort == "low" {
				low++
			}
			if s.Label == "synthesize" {
				synth = s.Effort
			}
		}
		eq(t, low, 3) // the cheap/expensive split is the point of the example
		eq(t, synth, "high")
	})
	tw(t, f, "compose runs its nested child and reports what it counted", func(t *testing.T) {
		res := runExample(t, "compose.js", newExampleHost(t))
		// The child's agent counts toward the parent run — they share the counter.
		eq(t, res.AgentCount, 2)
		v := res.Value.(map[string]any)
		eq(t, [2]any{v["ok"], v["count"]}, [2]any{true, float64(2)})
	})
	tw(t, f, "gated-fix passes straight through when the gate is happy", func(t *testing.T) {
		h := newExampleHost(t)
		res := runExample(t, "gated-fix.js", h)
		eq(t, res.AgentCount, 1)
		eq(t, res.Value.(map[string]any)["passed"], true)
		eq(t, h.spawns[0].Gate, "npm test")
	})
	tw(t, f, "gated-fix resumes the same child when the gate rejects the work", func(t *testing.T) {
		// The branch the example exists to demonstrate: fail the first gate only.
		h := newExampleHost(t, "fix")
		res := runExample(t, "gated-fix.js", h)
		eq(t, res.HasError, false)
		eq(t, res.Value.(map[string]any)["passed"], true)
		// fix (gated, fails) → resume → verify (gated, passes).
		var labels []string
		for _, s := range h.spawns {
			labels = append(labels, s.Label)
		}
		eq(t, labels, []string{"fix", "verify"})
		eq(t, res.AgentCount, 3)
	})
	// A negative case, rather than shipping a deliberately broken file.
	tw(t, f, "a file with no meta declaration is not treated as a workflow", func(t *testing.T) {
		// `const meta` without `export` is the near-miss worth pinning: it reads like a workflow and is not one.
		notAWorkflow := "const meta = { name: 'x' };\nreturn 1;\n"
		eq(t, hasMetaDeclaration(notAWorkflow), false)
		_, _, err := extractMeta(notAWorkflow)
		if _, ok := err.(*workflowMetaError); !ok {
			t.Fatalf("err %v", err)
		}
	})
}
