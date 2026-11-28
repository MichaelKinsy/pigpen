package ratchet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

// upstream: packages/coding-agent/test/ratchet.test.ts
// upstream: packages/coding-agent/test/slash-commands/ratchet.test.ts

const testFlow = "router"

var testCases = []string{
	"case_0", "case_1", "case_2", "case_3", "case_4",
	"case_5", "case_6", "case_7", "case_8", "case_9",
}

type testAnswer string

const (
	answerApprove testAnswer = "Approve"
	answerRevise  testAnswer = "Revise"
	answerTimeout testAnswer = "timeout"
	answerAbort   testAnswer = "Abort"
)

type statusPayload struct {
	State
	ApprovalStatus ApprovalStatus `json:"approval_status"`
}

type splitPayload struct {
	TrainIDs []string `json:"train_ids"`
	TestIDs  []string `json:"test_ids"`
	Seed     int      `json:"seed"`
}

type askedPrompt struct {
	Stage    Stage
	Question string
	Preview  string
}

type testHarness struct {
	t         *testing.T
	root      string
	prelude   eval.Prelude
	split     splitPayload
	mu        sync.Mutex
	answer    testAnswer
	questions []askedPrompt
}

func (h *testHarness) flowPath(parts ...string) string {
	all := append([]string{h.root, ".omp", "ratchet", testFlow}, parts...)
	return filepath.Join(all...)
}

func (h *testHarness) callRaw(params map[string]any, answer testAnswer) (*eval.PreludeResult, error) {
	h.t.Helper()
	h.mu.Lock()
	h.answer = answer
	h.mu.Unlock()
	merged := map[string]any{"flow": testFlow}
	for k, v := range params {
		merged[k] = v
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		h.t.Fatalf("marshal params: %v", err)
	}
	return h.prelude.Invoke(eval.PreludeCall{CallID: "t"}, raw)
}

func (h *testHarness) callJSON(params map[string]any, dst any, answer testAnswer) error {
	h.t.Helper()
	res, err := h.callRaw(params, answer)
	if err != nil {
		return err
	}
	if dst == nil {
		return nil
	}
	b, err := json.Marshal(res.Details)
	if err != nil {
		h.t.Fatalf("marshal details: %v", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		h.t.Fatalf("unmarshal details into %T: %v\nraw: %s", dst, err, string(b))
	}
	return nil
}

func (h *testHarness) approve(stage Stage, answer testAnswer) (ApprovalOutcome, error) {
	var out ApprovalOutcome
	err := h.callJSON(map[string]any{
		"action":   "approve",
		"stage":    string(stage),
		"question": "ok?",
		"preview":  string(stage) + " table",
	}, &out, answer)
	return out, err
}

func (h *testHarness) approveAll() {
	h.t.Helper()
	for _, st := range Stages {
		out, err := h.approve(st, answerApprove)
		if err != nil {
			h.t.Fatalf("approve(%s) failed: %v", st, err)
		}
		if !out.Approved {
			h.t.Fatalf("approve(%s) = %+v, want approved=true", st, out)
		}
	}
}

func (h *testHarness) gate(variant string, change *string) (*GateResult, error) {
	params := map[string]any{"action": "gate", "variant": variant}
	if change != nil {
		params["change"] = *change
	}
	var out GateResult
	if err := h.callJSON(params, &out, answerApprove); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *testHarness) writeRows(variant string, grade func(id string) float64, extra func(id string) map[string]any) {
	h.t.Helper()
	var lines []string
	for _, id := range testCases {
		row := map[string]any{
			"prompt_id": id,
			"rep":       0,
			"grade":     map[string]any{"quality": grade(id)},
		}
		if extra != nil {
			for k, v := range extra(id) {
				row[k] = v
			}
		}
		b, err := json.Marshal(row)
		if err != nil {
			h.t.Fatalf("marshal row: %v", err)
		}
		lines = append(lines, string(b))
	}
	target := h.flowPath(variant, "results.jsonl")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		h.t.Fatalf("mkdir %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", target, err)
	}
}

type setupOptions struct {
	hold   []string
	models []map[string]any
}

func setupHarness(t *testing.T, opts setupOptions) *testHarness {
	t.Helper()
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "eval/cases.jsonl"), buildCasesJSONL(testCases))
	mustWriteFile(t, filepath.Join(root, "eval/run.ts"), "// runner\n")
	mustWriteFile(t, filepath.Join(root, "src/prompt.md"), "route emails\n")

	h := &testHarness{
		t:      t,
		root:   root,
		answer: answerApprove,
	}
	h.prelude = PreludeDefinition(PreludeOptions{
		CwdOverride: root,
		ModelLister: func(_ sdk.Context) ([]map[string]any, error) {
			return opts.models, nil
		},
		Prompter: func(_ context.Context, _ sdk.Context, stage Stage, question, preview string) (ApprovalOutcome, error) {
			h.mu.Lock()
			ans := h.answer
			h.questions = append(h.questions, askedPrompt{Stage: stage, Question: question, Preview: preview})
			h.mu.Unlock()
			switch ans {
			case answerApprove:
				return ApprovalOutcome{Approved: true}, nil
			case answerTimeout:
				return ApprovalOutcome{Approved: false, Aborted: new(false), Feedback: new("No answer before timeout")}, nil
			case answerRevise:
				return ApprovalOutcome{Approved: false, Aborted: new(false), Feedback: new("drop case_3")}, nil
			default:
				return ApprovalOutcome{Approved: false, Aborted: new(true)}, nil
			}
		},
	})

	if err := h.callJSON(map[string]any{
		"action":  "init",
		"cases":   []string{"eval/cases.jsonl"},
		"harness": []string{"eval/run.ts"},
		"change":  []string{"src/prompt.md"},
	}, nil, answerApprove); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	hold := opts.hold
	if hold == nil {
		hold = []string{}
	}
	if err := h.callJSON(map[string]any{
		"action":  "plan",
		"goal":    map[string]any{"target": "quality", "hold": hold},
		"reps":    1,
		"command": "bun eval/run.ts --variant {variant}",
	}, nil, answerApprove); err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	caseMap := map[string]string{}
	for _, id := range testCases {
		caseMap[id] = "billing"
	}
	if err := h.callJSON(map[string]any{
		"action": "split",
		"cases":  caseMap,
		"seed":   7,
	}, &h.split, answerApprove); err != nil {
		t.Fatalf("split failed: %v", err)
	}
	return h
}

func buildCasesJSONL(ids []string) string {
	lines := make([]string, len(ids))
	for i, id := range ids {
		b, _ := json.Marshal(map[string]string{"id": id})
		lines[i] = string(b)
	}
	return strings.Join(lines, "\n")
}

func mustWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func appendFile(t *testing.T, p, content string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append %s: %v", p, err)
	}
}

// ---------------------------------------------------------------------------
// Ported upstream tests: packages/coding-agent/test/ratchet.test.ts
// ---------------------------------------------------------------------------

// upstream: packages/coding-agent/test/ratchet.test.ts: "keeps a change only when train and test both improve, and reverts a train-only gain as overfit"
func TestRatchetGateKeepAndOverfitRevert(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	train := map[string]bool{}
	for _, id := range h.split.TrainIDs {
		train[id] = true
	}

	h.writeRows("baseline", func(string) float64 { return 0.2 }, nil)
	base, err := h.gate("baseline", nil)
	if err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	if base.Decision != DecisionBaseline {
		t.Fatalf("baseline decision = %q, want baseline", base.Decision)
	}

	h.writeRows("v1", func(string) float64 { return 0.5 }, nil)
	kept, err := h.gate("v1", new("define each queue"))
	if err != nil {
		t.Fatalf("gate(v1): %v", err)
	}
	if kept.Decision != DecisionKeep {
		t.Fatalf("v1 decision = %q, want keep (reasons=%v)", kept.Decision, kept.Reasons)
	}
	if kept.Best == nil || kept.Best.Variant != "v1" {
		t.Fatalf("v1 best = %+v, want v1", kept.Best)
	}

	h.writeRows("v2", func(id string) float64 {
		if train[id] {
			return 0.9
		}
		return 0.5
	}, nil)
	overfit, err := h.gate("v2", new("quote failing emails"))
	if err != nil {
		t.Fatalf("gate(v2): %v", err)
	}
	if overfit.Decision != DecisionRevert {
		t.Fatalf("v2 decision = %q, want revert", overfit.Decision)
	}
	if !strings.Contains(strings.Join(overfit.Reasons, " "), "overfit") {
		t.Fatalf("v2 reasons = %v, want substring 'overfit'", overfit.Reasons)
	}
	if overfit.Best == nil || overfit.Best.Variant != "v1" {
		t.Fatalf("v2 best = %+v, want v1", overfit.Best)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "reverts a target gain that regresses a guardrail"
func TestRatchetGateRevertsGuardrailRegression(t *testing.T) {
	h := setupHarness(t, setupOptions{hold: []string{"cost_usd"}})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, func(string) map[string]any { return map[string]any{"cost_usd": 0.01} })
	if _, err := h.gate("baseline", nil); err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	h.writeRows("v1", func(string) float64 { return 0.6 }, func(string) map[string]any { return map[string]any{"cost_usd": 0.05} })
	gate, err := h.gate("v1", new("switch to bigger model"))
	if err != nil {
		t.Fatalf("gate(v1): %v", err)
	}
	if gate.Decision != DecisionRevert {
		t.Fatalf("decision = %q, want revert", gate.Decision)
	}
	if !strings.Contains(strings.Join(gate.Reasons, " "), "cost_usd") {
		t.Fatalf("reasons = %v, want cost_usd", gate.Reasons)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "refuses to gate when a guardrail cannot be measured"
func TestRatchetGateRefusesUnmeasurableGuardrail(t *testing.T) {
	h := setupHarness(t, setupOptions{hold: []string{"cost_usd"}})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, func(string) map[string]any { return map[string]any{"cost_usd": 0.01} })
	if _, err := h.gate("baseline", nil); err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	h.writeRows("v1", func(string) float64 { return 0.9 }, func(string) map[string]any {
		return map[string]any{"model": "mystery-model", "usage": map[string]any{"input_tokens": 100}}
	})
	_, err := h.gate("v1", new("new model"))
	if err == nil || !strings.Contains(err.Error(), "Guardrail cost_usd") {
		t.Fatalf("expected error matching 'Guardrail cost_usd', got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "does not keep a round whose slots only errored"
func TestRatchetGateErroredSlotsForceRerun(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, nil)
	if _, err := h.gate("baseline", nil); err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	hard := h.split.TestIDs[0]
	var rows []string
	for _, id := range testCases {
		if id == hard {
			continue
		}
		b, _ := json.Marshal(map[string]any{"prompt_id": id, "rep": 0, "grade": map[string]any{"quality": 0.9}})
		rows = append(rows, string(b))
	}
	mustWriteFile(t, h.flowPath("v1", "results.jsonl"), strings.Join(rows, "\n"))
	errRow, _ := json.Marshal(map[string]any{"prompt_id": hard, "rep": 0, "class": "timeout"})
	mustWriteFile(t, h.flowPath("v1", "errors.jsonl"), string(errRow))

	gate, err := h.gate("v1", new("shorter prompt"))
	if err != nil {
		t.Fatalf("gate(v1): %v", err)
	}
	if gate.Decision != DecisionRerun {
		t.Fatalf("decision = %q, want rerun", gate.Decision)
	}
	if gate.Best == nil || gate.Best.Variant != "baseline" {
		t.Fatalf("best = %+v, want baseline", gate.Best)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "refuses duplicate result rows for one slot"
func TestRatchetGateRefusesDuplicateRows(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, nil)
	extra, _ := json.Marshal(map[string]any{"prompt_id": testCases[0], "rep": 0, "grade": map[string]any{"quality": 1}})
	appendFile(t, h.flowPath("baseline", "results.jsonl"), string(extra)+"\n")
	_, err := h.gate("baseline", nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "refuses to gate a run that wrote transcripts for held-out cases"
func TestRatchetGateRefusesTestTraces(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, nil)
	mustWriteFile(t, h.flowPath("baseline", "traces", h.split.TestIDs[0]+"_rep0.json"), "[]")
	_, err := h.gate("baseline", nil)
	if err == nil || !strings.Contains(err.Error(), "test cases") {
		t.Fatalf("expected test cases trace leakage error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "refuses to gate an incomplete run"
func TestRatchetGateRefusesIncompleteRun(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	row, _ := json.Marshal(map[string]any{"prompt_id": testCases[0], "rep": 0, "grade": map[string]any{"quality": 1}})
	mustWriteFile(t, h.flowPath("baseline", "results.jsonl"), string(row))
	_, err := h.gate("baseline", nil)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected incomplete error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "marks the grader approval stale when the harness changes after approval"
func TestRatchetApprovalStaleOnHarnessEdit(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	mustWriteFile(t, filepath.Join(h.root, "eval/run.ts"), "// runner that now reads the answer key\n")

	var st statusPayload
	if err := h.callJSON(map[string]any{"action": "status"}, &st, answerApprove); err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.ApprovalStatus[StageInputs] != "current" || st.ApprovalStatus[StageGrader] != "stale" || st.ApprovalStatus[StagePlan] != "current" {
		t.Fatalf("approval_status = %+v, want inputs=current grader=stale plan=current", st.ApprovalStatus)
	}
	if _, err := h.callRaw(map[string]any{"action": "check", "variant": "baseline"}, answerApprove); err == nil || !strings.Contains(err.Error(), "grader: stale") {
		t.Fatalf("expected 'grader: stale' error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "marks the plan approval stale when price overrides change"
func TestRatchetApprovalStaleOnPriceChange(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	if err := h.callJSON(map[string]any{
		"action": "plan",
		"prices": map[string]any{"my-model": map[string]any{"in": 0.01, "out": 0.01}},
	}, nil, answerApprove); err != nil {
		t.Fatalf("plan prices: %v", err)
	}
	var st statusPayload
	if err := h.callJSON(map[string]any{"action": "status"}, &st, answerApprove); err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.ApprovalStatus[StagePlan] != "stale" {
		t.Fatalf("plan approval status = %q, want stale", st.ApprovalStatus[StagePlan])
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "requires a new flow when the cases change after the baseline, even if re-approved"
func TestRatchetCasesChangedAfterBaseline(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, nil)
	if _, err := h.gate("baseline", nil); err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	extra, _ := json.Marshal(map[string]string{"id": "case_new"})
	appendFile(t, filepath.Join(h.root, "eval/cases.jsonl"), "\n"+string(extra))
	out, err := h.approve(StageInputs, answerApprove)
	if err != nil || !out.Approved {
		t.Fatalf("re-approve inputs: out=%+v err=%v", out, err)
	}
	if _, err := h.callRaw(map[string]any{"action": "check", "variant": "v1"}, answerApprove); err == nil || !strings.Contains(err.Error(), "cases changed") {
		t.Fatalf("expected 'cases changed' error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "keeps both approvals when two stages are approved concurrently"
func TestRatchetConcurrentApprovalsPreserved(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, st := range []Stage{StageInputs, StageGrader} {
		wg.Add(1)
		go func(stage Stage) {
			defer wg.Done()
			out, err := h.approve(stage, answerApprove)
			if err != nil {
				errs <- err
				return
			}
			if !out.Approved {
				errs <- fmt.Errorf("stage %s not approved", stage)
			}
		}(st)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent approve error: %v", err)
	}
	var st statusPayload
	if err := h.callJSON(map[string]any{"action": "status"}, &st, answerApprove); err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.ApprovalStatus[StageInputs] != "current" || st.ApprovalStatus[StageGrader] != "current" {
		t.Fatalf("approval_status = %+v, want both inputs and grader current", st.ApprovalStatus)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "shows the reviewed material in the question text, not only in option previews"
func TestRatchetApprovalIncludesPreview(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	if _, err := h.approve(StageInputs, answerApprove); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(h.questions) != 1 || !strings.Contains(h.questions[0].Preview, "inputs table") {
		t.Fatalf("questions = %+v, want preview containing 'inputs table'", h.questions)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "does not record an approval from a timed-out or revised answer"
func TestRatchetApprovalNotRecordedOnTimeoutOrRevise(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	outT, err := h.approve(StageInputs, answerTimeout)
	if err != nil || outT.Approved {
		t.Fatalf("timeout approve = %+v, err=%v", outT, err)
	}
	outR, err := h.approve(StageInputs, answerRevise)
	if err != nil {
		t.Fatalf("revise approve err=%v", err)
	}
	if outR.Approved || outR.Aborted == nil || *outR.Aborted || outR.Feedback == nil || *outR.Feedback != "drop case_3" {
		t.Fatalf("revise approve = %+v, want approved=false aborted=false feedback='drop case_3'", outR)
	}
	var st statusPayload
	if err := h.callJSON(map[string]any{"action": "status"}, &st, answerApprove); err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.ApprovalStatus[StageInputs] != "missing" {
		t.Fatalf("inputs status = %q, want missing", st.ApprovalStatus[StageInputs])
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "rejects approval without an interactive UI"
func TestRatchetApprovalRejectsHeadlessUI(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "eval/cases.jsonl"), buildCasesJSONL(testCases))
	mustWriteFile(t, filepath.Join(root, "eval/run.ts"), "// runner\n")
	mustWriteFile(t, filepath.Join(root, "src/prompt.md"), "route emails\n")

	// Prelude without a test Prompter falls through to sdk.Context.HasUI() -> false on zero Context.
	p := PreludeDefinition(PreludeOptions{CwdOverride: root})
	initRaw, _ := json.Marshal(map[string]any{
		"flow":    testFlow,
		"action":  "init",
		"cases":   []string{"eval/cases.jsonl"},
		"harness": []string{"eval/run.ts"},
		"change":  []string{"src/prompt.md"},
	})
	if _, err := p.Invoke(eval.PreludeCall{CallID: "t"}, initRaw); err != nil {
		t.Fatalf("init: %v", err)
	}
	approveRaw, _ := json.Marshal(map[string]any{
		"flow":     testFlow,
		"action":   "approve",
		"stage":    "inputs",
		"question": "ok?",
		"preview":  "t",
	})
	_, err := p.Invoke(eval.PreludeCall{CallID: "t"}, approveRaw)
	if err == nil || !strings.Contains(err.Error(), "interactive session") {
		t.Fatalf("expected interactive session error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "keeps the round cap when only the plateau is updated"
func TestRatchetPlanKeepsRoundCapOnPlateauUpdate(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	if err := h.callJSON(map[string]any{"action": "plan", "stop": map[string]any{"rounds": 8}}, nil, answerApprove); err != nil {
		t.Fatalf("plan rounds: %v", err)
	}
	var st State
	if err := h.callJSON(map[string]any{"action": "plan", "stop": map[string]any{"plateau": 4}}, &st, answerApprove); err != nil {
		t.Fatalf("plan plateau: %v", err)
	}
	if st.Stop.Plateau != 4 || st.Stop.Rounds == nil || *st.Stop.Rounds != 8 {
		t.Fatalf("stop = %+v, want plateau=4 rounds=8", st.Stop)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "freezes the split once the baseline is gated"
func TestRatchetSplitFrozenAfterBaseline(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	h.writeRows("baseline", func(string) float64 { return 0.2 }, nil)
	if _, err := h.gate("baseline", nil); err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	cases := map[string]string{}
	for _, id := range testCases {
		cases[id] = "x"
	}
	_, err := h.callRaw(map[string]any{"action": "split", "cases": cases, "seed": 1}, answerApprove)
	if err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("expected frozen error, got %v", err)
	}
}

// upstream: packages/coding-agent/test/ratchet.test.ts: "prices rows from the catalog and leaves unknown or subscription-shared ids unpriced"
func TestRatchetCatalogPricingAndAmbiguity(t *testing.T) {
	known := map[string]any{"input": 10.0, "output": 20.0, "cacheRead": 1.0, "cacheWrite": 12.5}
	models := []map[string]any{
		{"id": "known-model", "provider": "p", "cost": known},
		{"id": "shared-model", "provider": "paid", "cost": known},
		{"id": "shared-model", "provider": "subscription", "cost": map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0}},
	}
	h := setupHarness(t, setupOptions{models: models})
	h.approveAll()
	modelFor := func(id string) string {
		switch id {
		case testCases[0]:
			return "mystery-model"
		case testCases[1]:
			return "shared-model"
		default:
			return "known-model"
		}
	}
	h.writeRows("baseline", func(string) float64 { return 0.2 }, func(id string) map[string]any {
		return map[string]any{
			"model": modelFor(id),
			"usage": map[string]any{"input_tokens": 1000, "output_tokens": 500},
		}
	})
	gate, err := h.gate("baseline", nil)
	if err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	lines := strings.Split(gate.Table, "\n")
	if len(lines) < 3 || !strings.Contains(lines[2], "0.0200") {
		t.Fatalf("table line 2 = %q, want 0.0200\nfull table:\n%s", strings.Join(lines, "\n"), gate.Table)
	}
	warnStr := strings.Join(gate.Warnings, " ")
	if !strings.Contains(warnStr, "mystery-model") || !strings.Contains(warnStr, "shared-model") {
		t.Fatalf("warnings = %v, want mystery-model and shared-model", gate.Warnings)
	}
}

// ---------------------------------------------------------------------------
// Ported upstream tests: packages/coding-agent/test/slash-commands/ratchet.test.ts
// ---------------------------------------------------------------------------

// upstream: packages/coding-agent/test/slash-commands/ratchet.test.ts
func TestSlashCommandPrepareRatchet(t *testing.T) {
	t.Run("enables the prelude for the session only and submits the request as data inside the kickoff", func(t *testing.T) {
		enabled := false
		res := prepareRatchetWith(
			sdk.Context{},
			"the inbox router in src/router, lower cost",
			func() []string { return []string{"read", "eval", "task", "ask"} },
			func() bool { return enabled },
			func(v bool) error { enabled = v; return nil },
			func() bool { return enabled },
		)
		if res.Error != "" {
			t.Fatalf("unexpected error: %s", res.Error)
		}
		if !enabled {
			t.Fatalf("expected ratchet.enabled session override to be true")
		}
		if !strings.Contains(res.Kickoff, "<ratchet-request>\nthe inbox router in src/router, lower cost\n</ratchet-request>") {
			t.Fatalf("kickoff missing request block:\n%s", res.Kickoff)
		}
		if strings.Contains(res.Kickoff, "No `ask` in this session") {
			t.Fatalf("kickoff should not contain headless ask warning when ask is active:\n%s", res.Kickoff)
		}
	})

	t.Run("tells a session without ask to only continue fully approved flows", func(t *testing.T) {
		enabled := false
		res := prepareRatchetWith(
			sdk.Context{},
			"",
			func() []string { return []string{"eval", "task"} },
			func() bool { return enabled },
			func(v bool) error { enabled = v; return nil },
			func() bool { return enabled },
		)
		if res.Error != "" {
			t.Fatalf("unexpected error: %s", res.Error)
		}
		if !strings.Contains(res.Kickoff, "No `ask` in this session: NEVER build or approve.") {
			t.Fatalf("missing headless ask instruction:\n%s", res.Kickoff)
		}
		if !strings.Contains(res.Kickoff, "No flow named") {
			t.Fatalf("missing 'No flow named' fallback:\n%s", res.Kickoff)
		}
	})

	t.Run("refuses without the task tool and leaves the prelude off", func(t *testing.T) {
		enabled := false
		res := prepareRatchetWith(
			sdk.Context{},
			"router",
			func() []string { return []string{"eval", "ask"} },
			func() bool { return enabled },
			func(v bool) error { enabled = v; return nil },
			func() bool { return enabled },
		)
		if res.Error != "/ratchet needs the task tool active." {
			t.Fatalf("error = %q, want '/ratchet needs the task tool active.'", res.Error)
		}
		if enabled {
			t.Fatalf("expected ratchet.enabled to remain false")
		}
	})

	t.Run("rolls back when the session cannot host the prelude", func(t *testing.T) {
		enabled := false
		res := prepareRatchetWith(
			sdk.Context{},
			"router",
			func() []string { return []string{"read", "eval", "task", "ask"} },
			func() bool { return enabled },
			func(v bool) error { enabled = v; return nil },
			func() bool { return false },
		)
		if res.Error != "The ratchet eval prelude is unavailable in this session." {
			t.Fatalf("error = %q, want unavailable message", res.Error)
		}
		if enabled {
			t.Fatalf("expected ratchet.enabled override to be rolled back to false")
		}
	})
}

// ---------------------------------------------------------------------------
// Additional unit & validation tests: approval tiers, status formatting,
// disabled gating, invalid params, train view, OpenAI usage, split determinism
// ---------------------------------------------------------------------------

func TestRatchetApprovalTiersAndStatusAndDisabledGating(t *testing.T) {
	p := PreludeDefinition()

	// Default disabled when ratchet.enabled is false / unset.
	if p.Enabled(sdk.Context{}) {
		t.Fatalf("ratchet prelude must be disabled by default")
	}

	// Default eval description must NOT mention ratchet when disabled.
	eval.ResetPreludesForTests()
	t.Cleanup(eval.ResetPreludesForTests)
	eval.RegisterPrelude(p)
	defaultDesc := eval.RenderDescription(eval.DefaultDescriptionOptions(eval.EnabledPreludes(sdk.Context{})))
	if strings.Contains(defaultDesc, "ratchet") {
		t.Fatalf("default eval description must not list disabled ratchet prelude:\n%s", defaultDesc)
	}

	// When enabled via Setting, eval description includes ratchet link.
	prevSetting := Setting
	t.Cleanup(func() { Setting = prevSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingRatchetEnabled {
			return true
		}
		return nil
	}
	if !p.Enabled(sdk.Context{}) {
		t.Fatalf("ratchet prelude should be enabled when ratchet.enabled=true")
	}
	enabledDesc := eval.RenderDescription(eval.DefaultDescriptionOptions(eval.EnabledPreludes(sdk.Context{})))
	if !strings.Contains(enabledDesc, "- `ratchet`: Build a trusted eval for one LLM flow") || !strings.Contains(enabledDesc, "help://eval/ratchet") {
		t.Fatalf("enabled eval description missing ratchet link:\n%s", enabledDesc)
	}

	// Approval tiers: status, check, train -> "read"; everything else & malformed -> "write".
	for _, raw := range []string{
		`{"flow":"f","action":"status"}`,
		`{"flow":"f","action":"check","variant":"baseline"}`,
		`{"flow":"f","action":"train","variant":"baseline"}`,
	} {
		if got := p.ApprovalTier(json.RawMessage(raw)); got != "read" {
			t.Fatalf("ApprovalTier(%s) = %q, want read", raw, got)
		}
	}
	for _, raw := range []string{
		`{"flow":"f","action":"init"}`,
		`{"flow":"f","action":"plan"}`,
		`{"flow":"f","action":"split"}`,
		`{"flow":"f","action":"approve"}`,
		`{"flow":"f","action":"gate","variant":"v1"}`,
		`{}`,
		`not-json`,
	} {
		if got := p.ApprovalTier(json.RawMessage(raw)); got != "write" {
			t.Fatalf("ApprovalTier(%s) = %q, want write", raw, got)
		}
	}

	// Approval policy enforcement via approval.CheckPrelude under always-ask vs write mode.
	prevAppSetting := approval.Setting
	prevAppSession := approval.SessionSetting
	t.Cleanup(func() {
		approval.Setting = prevAppSetting
		approval.SessionSetting = prevAppSession
	})
	readOnlyPolicy := func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "always-ask"
		}
		return nil
	}
	approval.Setting = readOnlyPolicy
	approval.SessionSetting = readOnlyPolicy
	statusParams := json.RawMessage(`{"flow":"f","action":"status"}`)
	gateParams := json.RawMessage(`{"flow":"f","action":"gate","variant":"v1"}`)
	// In always-ask mode (headless), read tier is allowed while write tier is denied/prompted.
	if err := approval.CheckPrelude(sdk.Context{}, "ratchet", p.ApprovalTier(statusParams), statusParams); err != nil {
		t.Fatalf("status (read tier) unexpectedly denied under always-ask: %v", err)
	}
	if err := approval.CheckPrelude(sdk.Context{}, "ratchet", p.ApprovalTier(gateParams), gateParams); err == nil {
		t.Fatalf("gate (write tier) should be blocked headless under always-ask mode")
	}

	// Status line formatting.
	if got := DescribeCall(json.RawMessage(`{"flow":"inbox","action":"status"}`), nil); got != `ratchet("inbox").status()` {
		t.Fatalf("status line = %q", got)
	}
	if got := DescribeCall(json.RawMessage(`{"flow":"inbox","action":"approve","stage":"grader","question":"q","preview":"p"}`), nil); got != `ratchet("inbox").approve(grader)` {
		t.Fatalf("approve status line = %q", got)
	}
	if got := DescribeCall(json.RawMessage(`{"flow":"inbox","action":"gate","variant":"v2","change":"c"}`), &eval.PreludeResult{Details: &GateResult{Decision: DecisionKeep}}); got != `ratchet("inbox").gate(v2) → keep` {
		t.Fatalf("gate status line = %q", got)
	}
	if got := DescribeCall(json.RawMessage(`{"bad":true}`), nil); got != "" {
		t.Fatalf("invalid params status line = %q, want empty", got)
	}
}

func TestRatchetInvalidParamsValidation(t *testing.T) {
	p := PreludeDefinition(PreludeOptions{CwdOverride: t.TempDir()})
	badPayloads := []string{
		`null`,
		`{}`,
		`{"flow":"","action":"status"}`,
		`{"flow":"f","action":"unknown"}`,
		`{"flow":"f","action":"init","cases":"not-an-array","harness":[],"change":[]}`,
		`{"flow":"f","action":"plan","reps":0}`,
		`{"flow":"f","action":"plan","reps":1.5}`,
		`{"flow":"f","action":"plan","stop":{"plateau":1}}`,
		`{"flow":"f","action":"plan","goal":{"target":"q","direction":"sideways"}}`,
		`{"flow":"f","action":"plan","prices":{"m":{"in":-1,"out":1}}}`,
		`{"flow":"f","action":"split","cases":{"a":"t"},"test_fraction":1.0}`,
		`{"flow":"f","action":"split","cases":{"a":"t"},"seed":-1}`,
		`{"flow":"f","action":"approve","stage":"bogus","question":"q","preview":"p"}`,
		`{"flow":"f","action":"approve","stage":"inputs","question":"","preview":"p"}`,
		`{"flow":"f","action":"check","variant":""}`,
		`{"flow":"f","action":"gate","variant":"v1","change":123}`,
	}
	for _, raw := range badPayloads {
		_, err := p.Invoke(eval.PreludeCall{CallID: "t"}, json.RawMessage(raw))
		if err == nil || !strings.Contains(err.Error(), "ratchet received invalid arguments") {
			t.Fatalf("payload %s: expected invalid arguments error, got %v", raw, err)
		}
	}
}

func TestRatchetTrainViewAndOpenAIUsage(t *testing.T) {
	h := setupHarness(t, setupOptions{})
	h.approveAll()
	// Write baseline with OpenAI-shaped usage (prompt_tokens includes cached_tokens) and a train-only trace.
	if err := h.callJSON(map[string]any{
		"action": "plan",
		"prices": map[string]any{
			"gpt-test": map[string]any{"in": 10.0, "out": 20.0, "cache_read": 1.0},
		},
	}, nil, answerApprove); err != nil {
		t.Fatalf("plan prices: %v", err)
	}
	// Re-approve plan after adding prices.
	if out, err := h.approve(StagePlan, answerApprove); err != nil || !out.Approved {
		t.Fatalf("re-approve plan: %+v %v", out, err)
	}
	h.writeRows("baseline", func(string) float64 { return 0.4 }, func(string) map[string]any {
		return map[string]any{
			"model": "gpt-test",
			"usage": map[string]any{
				"prompt_tokens":         1000,
				"completion_tokens":     500,
				"prompt_tokens_details": map[string]any{"cached_tokens": 400},
			},
		}
	})
	trainID := h.split.TrainIDs[0]
	mustWriteFile(t, h.flowPath("baseline", "traces", trainID+"_rep0.json"), `[{"role":"user","content":"hi"}]`)
	gate, err := h.gate("baseline", nil)
	if err != nil {
		t.Fatalf("gate(baseline): %v", err)
	}
	// 600 uncached in * $10/M ($0.006) + 400 cached * $1/M ($0.0004) + 500 out * $20/M ($0.01) = $0.0164
	if !strings.Contains(gate.Table, "0.0164") {
		t.Fatalf("expected OpenAI cached token pricing 0.0164 in table:\n%s", gate.Table)
	}

	var tv TrainView
	if err := h.callJSON(map[string]any{"action": "train", "variant": "baseline"}, &tv, answerApprove); err != nil {
		t.Fatalf("train view: %v", err)
	}
	if len(tv.Rows) != len(h.split.TrainIDs) {
		t.Fatalf("train view rows = %d, want %d", len(tv.Rows), len(h.split.TrainIDs))
	}
	if len(tv.Traces) != 1 || !strings.HasSuffix(tv.Traces[0], "/baseline/traces/"+trainID+"_rep0.json") {
		t.Fatalf("train view traces = %v", tv.Traces)
	}
	if len(tv.History) != len(h.split.TrainIDs) {
		t.Fatalf("train view history keys = %d, want %d", len(tv.History), len(h.split.TrainIDs))
	}
	if v := tv.History[trainID]["baseline"]; v == nil || *v != 0.4 {
		t.Fatalf("history[%s][baseline] = %v, want 0.4", trainID, v)
	}
}

// ---------------------------------------------------------------------------
// End-to-end JS and Python eval kernel tests in a temporary git repository
// ---------------------------------------------------------------------------

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-q")
	mustWriteFile(t, filepath.Join(dir, "eval/cases.jsonl"), buildCasesJSONL(testCases))
	mustWriteFile(t, filepath.Join(dir, "eval/run.sh"), "#!/bin/sh\necho ok\n")
	mustWriteFile(t, filepath.Join(dir, "src/prompt.md"), "v0 prompt\n")
	runGit("add", ".")
	runGit("commit", "-q", "-m", "initial")
	return dir
}

func TestRatchetEndToEndPythonAndJSKernels(t *testing.T) {
	runE2E := func(t *testing.T, lang string, code string) {
		t.Helper()
		repo := initTempGitRepo(t)

		eval.ResetPreludesForTests()
		t.Cleanup(eval.ResetPreludesForTests)

		prevAppSetting := approval.Setting
		prevAppSession := approval.SessionSetting
		t.Cleanup(func() {
			approval.Setting = prevAppSetting
			approval.SessionSetting = prevAppSession
		})
		yolo := func(_ sdk.Context, key string) any {
			if key == "tools.approvalMode" {
				return "yolo"
			}
			return nil
		}
		approval.Setting = yolo
		approval.SessionSetting = yolo

		enabled := true
		eval.RegisterPrelude(PreludeDefinition(PreludeOptions{
			CwdOverride:  repo,
			EnabledCheck: func(sdk.Context) bool { return enabled },
			Prompter: func(_ context.Context, _ sdk.Context, _ Stage, _, _ string) (ApprovalOutcome, error) {
				return ApprovalOutcome{Approved: true}, nil
			},
		}))

		// Run the embedded prelude snippet in the real python3 / bun / node runtime
		// wired to our real Go PreludeDefinition.Invoke so every argument shape,
		// camelCase/snake_case translation, response unwrapping, and error propagation
		// is exercised end-to-end.
		runFacadeE2E(t, lang, repo, code)
	}

	pyCode := `
import json, pathlib

r = ratchet("inbox")
st = await r.init(cases="eval/cases.jsonl", harness=["eval/run.sh"], change="src/prompt.md")
assert st["flow"] == "inbox"
await r.plan(goal={"target": "quality", "hold": ["cost_usd"]}, reps=1, command="sh eval/run.sh {variant}")
cases = {f"case_{i}": "billing" for i in range(10)}
sp = await r.split(cases, seed=7)
assert len(sp["train_ids"]) + len(sp["test_ids"]) == 10

for stage in ("inputs", "grader", "plan"):
    res = await r.approve(stage, question="ok?", preview=f"{stage} preview")
    assert res["approved"] is True

chk = await r.check("baseline")
assert chk["expected_rows"] == 10
assert chk["command"] == "sh eval/run.sh baseline"

def write_variant(variant, score_fn, cost_fn):
    d = pathlib.Path(".omp/ratchet/inbox") / variant
    d.mkdir(parents=True, exist_ok=True)
    lines = [
        json.dumps({"prompt_id": f"case_{i}", "rep": 0, "grade": {"quality": score_fn(f"case_{i}")}, "cost_usd": cost_fn(f"case_{i}")})
        for i in range(10)
    ]
    (d / "results.jsonl").write_text("\n".join(lines) + "\n")

write_variant("baseline", lambda _: 0.2, lambda _: 0.01)
g0 = await r.gate("baseline")
assert g0["decision"] == "baseline"

write_variant("v1", lambda _: 0.6, lambda _: 0.01)
g1 = await r.gate("v1", change="clarify routing rules")
assert g1["decision"] == "keep"
assert g1["best"]["variant"] == "v1"

write_variant("v2", lambda _: 0.3, lambda _: 0.01)
g2 = await r.gate("v2", change="bad regression")
assert g2["decision"] == "revert"
assert g2["best"]["variant"] == "v1"

tv = await r.train("v1")
assert len(tv["rows"]) == len(sp["train_ids"])

status = await r.status()
assert status["best"]["variant"] == "v1"
assert status["approval_status"] == {"inputs": "current", "grader": "current", "plan": "current"}
print("PY_E2E_OK")
`

	jsCode := `
const fs = require("node:fs");
const path = require("node:path");

const r = ratchet("inbox");
const st = await r.init({ cases: "eval/cases.jsonl", harness: ["eval/run.sh"], change: "src/prompt.md" });
if (st.flow !== "inbox") throw new Error("bad flow");
await r.plan({ goal: { target: "quality", hold: ["cost_usd"] }, reps: 1, command: "sh eval/run.sh {variant}" });
const cases = Object.fromEntries(Array.from({ length: 10 }, (_, i) => ["case_" + i, "billing"]));
const sp = await r.split(cases, { testFraction: 0.4, seed: 7 });
if (sp.train_ids.length + sp.test_ids.length !== 10) throw new Error("bad split");

for (const stage of ["inputs", "grader", "plan"]) {
	const res = await r.approve(stage, { question: "ok?", preview: stage + " preview" });
	if (!res.approved) throw new Error("not approved");
}

const chk = await r.check("baseline");
if (chk.expected_rows !== 10) throw new Error("bad check");

const writeVariant = (variant, scoreFn, costFn) => {
	const d = path.join(".omp/ratchet/inbox", variant);
	fs.mkdirSync(d, { recursive: true });
	const lines = Array.from({ length: 10 }, (_, i) => {
		const id = "case_" + i;
		return JSON.stringify({ prompt_id: id, rep: 0, grade: { quality: scoreFn(id) }, cost_usd: costFn(id) });
	});
	fs.writeFileSync(path.join(d, "results.jsonl"), lines.join("\n") + "\n");
};

writeVariant("baseline", () => 0.2, () => 0.01);
const g0 = await r.gate("baseline");
if (g0.decision !== "baseline") throw new Error("expected baseline");

writeVariant("v1", () => 0.6, () => 0.01);
const g1 = await r.gate("v1", { change: "clarify routing rules" });
if (g1.decision !== "keep" || g1.best.variant !== "v1") throw new Error("expected keep v1");

writeVariant("v2", () => 0.3, () => 0.01);
const g2 = await r.gate("v2", { change: "bad regression" });
if (g2.decision !== "revert" || g2.best.variant !== "v1") throw new Error("expected revert v2");

const tv = await r.train("v1");
if (tv.rows.length !== sp.train_ids.length) throw new Error("bad train view");

const status = await r.status();
if (status.best.variant !== "v1") throw new Error("bad status best");
console.log("JS_E2E_OK");
`

	t.Run("python", func(t *testing.T) {
		runE2E(t, "py", pyCode)
	})
	t.Run("javascript", func(t *testing.T) {
		runE2E(t, "js", jsCode)
	})
}

func runFacadeE2E(t *testing.T, lang, repo, userCode string) {
	t.Helper()

	p, ok := eval.LookupPrelude("ratchet")
	if !ok {
		t.Fatalf("ratchet prelude not registered")
	}

	// Start a tiny loopback HTTP server mirroring internal/eval's prelude bridge
	// so the real python3 / bun / node subprocess executes assets/prelude.py and
	// assets/prelude.js unmodified against our Go PreludeDefinition.Invoke.
	srv, url := startTestPreludeBridge(t, p)
	defer srv()

	if lang == "py" {
		pyBin, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("python3 not on PATH")
		}
		driver := fmt.Sprintf(`
import asyncio, json, urllib.request

async def _omp_prelude(name, params):
    req = urllib.request.Request(
        %q,
        data=json.dumps({"name": name, "parameters": params}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req) as resp:
        payload = json.loads(resp.read().decode("utf-8"))
    if not payload.get("ok"):
        raise RuntimeError(payload.get("error", "prelude failed"))
    return payload.get("value")

%s

async def _main():
%s

asyncio.run(_main())
`, url, ratchetPython, indentLines(userCode, "    "))
		cmd := exec.Command(pyBin, "-c", driver)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("python e2e failed: %v\n%s", err, string(out))
		}
		if !strings.Contains(string(out), "PY_E2E_OK") {
			t.Fatalf("missing PY_E2E_OK in output:\n%s", string(out))
		}
		return
	}

	runtimeBin := ""
	if bin, err := exec.LookPath("bun"); err == nil {
		runtimeBin = bin
	} else if bin, err := exec.LookPath("node"); err == nil {
		runtimeBin = bin
	} else {
		t.Skip("neither bun nor node on PATH")
	}

	driver := fmt.Sprintf(`
globalThis.__omp_log__ = (_level, ...args) => console.log(...args);
globalThis.__omp_prelude__ = async (name, parameters) => {
	const resp = await fetch(%q, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ name, parameters }),
	});
	const payload = await resp.json();
	if (!payload.ok) throw new Error(payload.error || "prelude failed");
	return payload.value;
};
%s
(async () => {
%s
})().catch(err => { console.error(err); process.exit(1); });
`, url, ratchetJavascript, userCode)

	cmd := exec.Command(runtimeBin, "-e", driver)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("js e2e failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "JS_E2E_OK") {
		t.Fatalf("missing JS_E2E_OK in output:\n%s", string(out))
	}
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}
