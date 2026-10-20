package pi_subagents_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The run policy through the host boundary: output file and mode, acceptance and gate, deadline, artifacts, spawn budget and
// capability ceiling around a fake child (policy_run.go). The original has no counterpart of these as one test; the pieces
// they use have twins in policy_*_test.go.

type policyHost struct {
	h     *Host
	calls *[]sub.ChildRequest
	cwd   string
}

// childFunc answers one child: stdout for a request, or blocks until done when it returns block.
type childFunc func(req sub.ChildRequest, done <-chan struct{}) (stdout string, block bool)

func policyHostWith(t *testing.T, child childFunc, opts HostOptions) *policyHost {
	t.Helper()
	sub.ResetRunPolicyState(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	var mu sync.Mutex
	var got []sub.ChildRequest
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		out, block := child(req, done)
		if block {
			<-done
			return sub.ChildResult{Stdout: out}, sub.ErrCancelled
		}
		return sub.ChildResult{Stdout: out}, nil
	})
	cwd := tmp(t)
	home := withTempHome(t)
	writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json"), `{"asyncByDefault": false}`)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "other.md"), agentMD("other", "Other", "Other."))
	opts.Cwd = cwd
	return &policyHost{h: StartHost(t, Extension(), opts), calls: &got, cwd: cwd}
}

func (p *policyHost) run(params map[string]any) (string, string) {
	p.h.t.Helper()
	base := map[string]any{"agent": "helper", "task": "do it"}
	for k, v := range params {
		base[k] = v
	}
	raw, failure := p.h.Tool("subagent", base)
	return resultOrFailure(raw, failure)
}

// resultOrFailure is the result text, and the failure: a host error, or the content of a result that says is_error.
func resultOrFailure(raw []byte, failure string) (string, string) {
	if failure == "" {
		var r struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		}
		if json.Unmarshal(raw, &r) == nil && r.IsError {
			failure = r.Content
		}
	}
	return string(raw), failure
}

func echo(out string) childFunc {
	return func(sub.ChildRequest, <-chan struct{}) (string, bool) { return out, false }
}

var pathLine = regexp.MustCompile(`(?m)^Write your findings to exactly this path: (.+)$`)

func TestOutputFileIsInstructedAndReadBack(t *testing.T) {
	p := policyHostWith(t, func(req sub.ChildRequest, _ <-chan struct{}) (string, bool) {
		if m := pathLine.FindStringSubmatch(req.Stdin); m != nil {
			os.MkdirAll(filepath.Dir(m[1]), 0o755)
			os.WriteFile(m[1], []byte("report from the file"), 0o644)
			return "receipt", false
		}
		return "no path asked", false
	}, HostOptions{})
	out := filepath.Join(p.cwd, "reports", "out.md")
	raw, failure := p.run(map[string]any{"output": "reports/out.md"})
	eq(t, failure, "", "failure")
	req := (*p.calls)[0]
	if !strings.Contains(req.Stdin, "Write your findings to exactly this path: "+out) || !strings.Contains(req.Stdin, "This path is authoritative for this run.") {
		t.Errorf("the output instruction is missing from the task: %q", req.Stdin)
	}
	joined := strings.Join(req.Args, "\x00")
	if !strings.Contains(joined, "Runtime output path override:") {
		t.Errorf("the system prompt lacks the override: %v", req.Args)
	}
	if !strings.Contains(raw, "report from the file") || !strings.Contains(raw, "Output saved to: "+out) {
		t.Errorf("result %s", raw)
	}

	// file-only: only the reference comes back
	raw, failure = p.run(map[string]any{"output": "reports/out2.md", "outputMode": "file-only"})
	eq(t, failure, "", "file-only failure")
	if strings.Contains(raw, "report from the file") || !strings.Contains(raw, "Output saved to:") {
		t.Errorf("file-only result %s", raw)
	}

	// the child did not write: its answer is saved there
	p2 := policyHostWith(t, echo("the answer"), HostOptions{})
	_, failure = p2.run(map[string]any{"output": "saved.md"})
	eq(t, failure, "", "failure")
	b, _ := os.ReadFile(filepath.Join(p2.cwd, "saved.md"))
	eq(t, string(b), "the answer", "persisted answer")
}

func TestOutputOptionsAreValidated(t *testing.T) {
	p := policyHostWith(t, echo("x"), HostOptions{})
	_, failure := p.run(map[string]any{"outputMode": "file-only"})
	if !strings.HasPrefix(failure, `Single run (helper) sets outputMode: "file-only" but does not configure an output file.`) {
		t.Errorf("file-only without output: %q", failure)
	}
	_, failure = p.run(map[string]any{"output": false, "outputMode": "file-only"})
	if !strings.HasPrefix(failure, `Single run (helper) sets outputMode: "file-only"`) {
		t.Errorf("output false: %q", failure)
	}
	eq(t, len(*p.calls), 0, "no child started")
	// the public boundary adds output: true; it names no file without an agent default
	_, failure = p.run(nil)
	eq(t, failure, "", "plain run")
	if strings.Contains((*p.calls)[0].Stdin, "Write your findings") {
		t.Errorf("a plain run got an output instruction: %q", (*p.calls)[0].Stdin)
	}
}

func TestAgentDefaultOutputAndTimeoutFrontmatter(t *testing.T) {
	p := policyHostWith(t, echo("x"), HostOptions{})
	writeFile(t, filepath.Join(p.cwd, ".pi", "agents", "reporter.md"), "---\nname: reporter\ndescription: R\noutput: default-report.md\noutputMode: inline\ntimeoutMs: 123456\n---\n\nYou report.\n")
	_, failure := p.h.Tool("subagent", map[string]any{"agent": "reporter", "task": "t"})
	eq(t, failure, "", "failure")
	b, err := os.ReadFile(filepath.Join(p.cwd, "default-report.md"))
	eq(t, err, nil, "the agent's default output file")
	eq(t, string(b), "x", "content")
	_, failure = p.h.Tool("subagent", map[string]any{"agent": "reporter", "task": "t", "output": false})
	eq(t, failure, "", "output false")
	writeFile(t, filepath.Join(p.cwd, ".pi", "agents", "bad.md"), "---\nname: bad\ndescription: B\noutputMode: sideways\n---\n\nB.\n")
	_, failure = p.h.Tool("subagent", map[string]any{"agent": "bad", "task": "t"})
	if !strings.Contains(failure, "has invalid outputMode frontmatter; expected 'inline' or 'file-only'.") {
		t.Errorf("bad outputMode frontmatter: %q", failure)
	}
}

func reportFence(extra string) string {
	return "Done.\n```acceptance-report\n" + `{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"checked"}],"changedFiles":[],"testsAddedOrUpdated":[],"commandsRun":[{"command":"go test","result":"passed","summary":"ok"}],"residualRisks":["none"],"noStagedFiles":true` + extra + "}\n```"
}

func TestAcceptanceContractAndVerdict(t *testing.T) {
	p := policyHostWith(t, echo(reportFence("")), HostOptions{})
	// the contract goes to the child's system prompt, the report is stripped from the answer
	raw, failure := p.run(map[string]any{"acceptance": map[string]any{"level": "checked"}})
	eq(t, failure, "", "failure")
	req := (*p.calls)[0]
	joined := strings.Join(req.Args, "\x00")
	if !strings.Contains(joined, "## Acceptance Contract") || !strings.Contains(joined, "Acceptance level: checked") {
		t.Errorf("the contract is not in the system prompt: %v", req.Args)
	}
	var res struct{ Content string }
	_ = json.Unmarshal([]byte(raw), &res)
	if strings.Contains(raw, "acceptance-report") || !strings.Contains(raw, "Done.") {
		t.Errorf("the report must be stripped: %s", raw)
	}
	if !strings.Contains(raw, `"status":"checked"`) {
		t.Errorf("the ledger is in the details: %s", raw)
	}

	// an explicit acceptance whose report is missing rejects the run
	p2 := policyHostWith(t, echo("no report here"), HostOptions{})
	_, failure = p2.run(map[string]any{"acceptance": "checked"})
	if !strings.HasPrefix(failure, "Acceptance rejected: Structured acceptance report not found.") || !strings.Contains(failure, "Output:\nno report here") {
		t.Errorf("explicit acceptance: %q", failure)
	}
	// the same output without an acceptance option is not rejected (inferred acceptance is not explicit)
	_, failure = p2.run(nil)
	eq(t, failure, "", "inferred acceptance")

	// invalid acceptance is the original's text; false disables the contract
	_, failure = p2.run(map[string]any{"acceptance": "verified"})
	if !strings.Contains(failure, `acceptance level "verified" requires object form`) {
		t.Errorf("invalid acceptance: %q", failure)
	}
	before := len(*p2.calls)
	_, failure = p2.run(map[string]any{"acceptance": false})
	eq(t, failure, "", "acceptance false")
	if strings.Contains(strings.Join((*p2.calls)[before].Args, "\x00"), "## Acceptance Contract") {
		t.Errorf("acceptance:false must leave no contract")
	}
}

func TestGateRunsAfterTheChild(t *testing.T) {
	p := policyHostWith(t, echo("child done"), HostOptions{})
	raw, failure := p.run(map[string]any{"agentContract": map[string]any{"version": 1}, "gate": map[string]any{"command": `printf '%s' '{"verdict":"ok"}'`, "output": "json"}})
	eq(t, failure, "", "failure")
	if !strings.Contains(raw, `"structuredOutput":{"verdict":"ok"}`) {
		t.Errorf("a typed gate supplies the structured output: %s", raw)
	}
	// a gate alone is a verified acceptance: the child must report, as in the original, unless the call opts into the contract
	_, failure = p.run(map[string]any{"gate": "true"})
	if !strings.HasPrefix(failure, "Acceptance rejected: Structured acceptance report not found.") {
		t.Errorf("a gate without agentContract needs a report: %q", failure)
	}
	_, failure = p.run(map[string]any{"agentContract": map[string]any{"version": 1}, "gate": "true"})
	eq(t, failure, "", "a gate under agentContract")
	raw, failure = p.run(map[string]any{"agentContract": map[string]any{"version": 1}, "gate": "exit 3"})
	eq(t, failure, "", "a failing gate under agentContract does not change the run's exit")
	if !strings.Contains(raw, `"status":"rejected"`) || !strings.Contains(raw, `"execution":{`) {
		t.Errorf("the ledger records the failing gate: %s", raw)
	}
	_, failure = p.run(map[string]any{"agentContract": map[string]any{"version": 2}})
	eq(t, failure, "agentContract must be { version: 1 }.", "bad contract")
	_, failure = p.run(map[string]any{"gate": "true", "acceptance": "checked"})
	if !strings.Contains(failure, "gate cannot be combined with acceptance") {
		t.Errorf("gate with acceptance: %q", failure)
	}
	_, failure = p.run(map[string]any{"gate": ""})
	if !strings.Contains(failure, "non-empty command string") {
		t.Errorf("empty gate: %q", failure)
	}
}

func TestDeadlines(t *testing.T) {
	p := policyHostWith(t, func(sub.ChildRequest, <-chan struct{}) (string, bool) { return "partial work", true }, HostOptions{})
	start := time.Now()
	_, failure := p.run(map[string]any{"timeoutMs": 60})
	if !strings.HasPrefix(failure, "Subagent timed out after 60ms.\n\nRecovery summary:\n- termination: timed-out") || !strings.Contains(failure, "Partial output before timeout:\npartial work") {
		t.Errorf("timeout: %q", failure)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	_, failure = p.run(map[string]any{"maxRuntimeMs": 60})
	if !strings.HasPrefix(failure, "Subagent timed out after 60ms.") {
		t.Errorf("maxRuntimeMs alias: %q", failure)
	}
	_, failure = p.run(map[string]any{"timeoutMs": 60, "maxRuntimeMs": 70})
	eq(t, failure, "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both.", "disagreeing aliases")
	_, failure = p.run(map[string]any{"timeoutMs": 0})
	eq(t, failure, "timeoutMs must be a positive integer.", "zero")
	_, failure = p.run(map[string]any{"timeoutMs": 2147483648})
	eq(t, failure, "timeoutMs must be a positive integer no larger than 2147483647.", "too large")
	_, failure = p.run(map[string]any{"timeoutMs": 1.5})
	eq(t, failure, "timeoutMs must be a positive integer.", "fractional")
	// an agent's frontmatter deadline applies when the call sets none
	writeFile(t, filepath.Join(p.cwd, ".pi", "agents", "quick.md"), "---\nname: quick\ndescription: Q\ntimeoutMs: 40\n---\n\nQ.\n")
	_, failure = p.h.Tool("subagent", map[string]any{"agent": "quick", "task": "t"})
	if !strings.HasPrefix(failure, "Subagent timed out after 40ms.") {
		t.Errorf("frontmatter deadline: %q", failure)
	}
	// the config default
	sub.SetExtensionConfigForTest(t, 50.0, nil)
	_, failure = p.run(nil)
	if !strings.HasPrefix(failure, "Subagent timed out after 50ms.") {
		t.Errorf("config deadline: %q", failure)
	}
}

func TestArtifactsAreWrittenUnlessDisabled(t *testing.T) {
	p := policyHostWith(t, echo("the output"), HostOptions{})
	raw, failure := p.run(nil)
	eq(t, failure, "", "failure")
	m := regexp.MustCompile(`"outputPath":"([^"]+)"`).FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("no artifact paths in %s", raw)
	}
	b, err := os.ReadFile(m[1])
	eq(t, err, nil, "output artifact")
	eq(t, string(b), "the output", "output artifact content")
	in, _ := os.ReadFile(strings.Replace(m[1], "_output.md", "_input.md", 1))
	eq(t, string(in), "do it", "input artifact")
	meta, err := os.ReadFile(strings.Replace(m[1], "_output.md", "_meta.json", 1))
	eq(t, err, nil, "metadata artifact")
	if !strings.Contains(string(meta), `"exitCode": 0`) {
		t.Errorf("metadata %s", meta)
	}
	raw, _ = p.run(map[string]any{"artifacts": false})
	if strings.Contains(raw, "artifactPaths") {
		t.Errorf("artifacts:false must write none: %s", raw)
	}
}

func TestIncludeProgress(t *testing.T) {
	p := policyHostWith(t, echo("x"), HostOptions{})
	raw, _ := p.run(map[string]any{"includeProgress": true})
	if !strings.Contains(raw, `"progress":[{`) {
		t.Errorf("progress: %s", raw)
	}
	raw, _ = p.run(nil)
	if strings.Contains(raw, `"progress"`) {
		t.Errorf("progress is off by default: %s", raw)
	}
}

func TestPublicBoundaryMessages(t *testing.T) {
	p := policyHostWith(t, echo("x"), HostOptions{})
	_, failure := p.h.Tool("subagent", map[string]any{"task": "work"})
	eq(t, failure, "Structured single-child execution requires agent to be a non-empty string.", "task without agent")
	_, failure = p.h.Tool("subagent", map[string]any{"agent": "helper", "task": 42})
	eq(t, failure, "Structured single-child task must be a string when provided.", "numeric task")
	_, failure = p.h.Tool("subagent", map[string]any{"agent": "  helper  ", "task": "t"})
	eq(t, failure, "", "the agent name is trimmed")
	eq(t, len(*p.calls), 1, "children")
}

func TestSpawnBudgetOfTheSession(t *testing.T) {
	p := policyHostWith(t, echo("x"), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": "session-a"}, ""
		case "ui.confirm":
			return map[string]any{"confirmed": true}, ""
		}
		return map[string]any{}, ""
	}})
	sub.SetExtensionConfigForTest(t, nil, 2.0)
	for i := 0; i < 2; i++ {
		_, failure := p.run(nil)
		eq(t, failure, "", fmt.Sprintf("run %d", i))
	}
	_, failure := p.run(nil)
	if !strings.HasPrefix(failure, "Subagent spawn limit reached for this session (2/2 used, 1 requested). 0 remaining;") {
		t.Errorf("third run: %q", failure)
	}
	eq(t, len(*p.calls), 2, "children")
	// the explicit grant (native confirmation) adds capacity, at most the configured cap in total
	raw, failure := p.run(map[string]any{"action": "grant-spawn-budget", "additional": 1, "agent": nil, "task": nil})
	eq(t, failure, "", "grant failure")
	if !strings.Contains(raw, "Spawn budget grant applied: +1. Spawn budget: 2/3 used") && !strings.Contains(raw, "Spawn budget grant applied: +1.") {
		t.Errorf("grant: %s", raw)
	}
	_, failure = p.run(nil)
	eq(t, failure, "", "run after grant")
	_, failure = p.run(map[string]any{"action": "grant-spawn-budget", "additional": 5, "agent": nil, "task": nil})
	if !strings.Contains(failure, "5 requested but only 1 of the original configured limit remains grantable.") {
		t.Errorf("over-grant: %q", failure)
	}
	_, failure = p.run(map[string]any{"action": "grant-spawn-budget", "agent": nil, "task": nil})
	eq(t, failure, "action='grant-spawn-budget' requires additional to be a positive integer.", "no additional")
}

func TestGrantNeedsTheRootInteractiveSession(t *testing.T) {
	noUI := false
	p := policyHostWith(t, echo("x"), HostOptions{HasUI: &noUI})
	_, failure := p.run(map[string]any{"action": "grant-spawn-budget", "additional": 1, "agent": nil, "task": nil})
	eq(t, failure, "Action 'grant-spawn-budget' is available only from the root interactive parent session.", "no UI")
}

func TestCapabilityCeilingAppliesToTheRun(t *testing.T) {
	p := policyHostWith(t, echo("x"), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "getSessionID" {
			return map[string]any{"sessionId": "session-ceiling"}, ""
		}
		return map[string]any{}, ""
	}})
	sub.RegisterCeilingForTest(t, "session-ceiling", "plan-mode", `{"allowedAgents":["other"],"allowedTools":["read","grep"]}`)
	_, failure := p.run(nil)
	eq(t, failure, "Capability ceiling from plan-mode does not allow agent 'helper'. Allowed agents: other.", "restricted agent")
	eq(t, len(*p.calls), 0, "no child started")
	_, failure = p.run(map[string]any{"agent": "other"})
	eq(t, failure, "", "allowed agent")
	req := (*p.calls)[0]
	i := -1
	for n, a := range req.Args {
		if a == "--tools" {
			i = n
		}
	}
	if i < 0 || req.Args[i+1] != "grep,read" {
		t.Errorf("tools are narrowed to the ceiling: %v", req.Args)
	}
}
