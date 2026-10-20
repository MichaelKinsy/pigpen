package warden

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Layer-1 twins of the conscience cases of tests/extension.test.ts (lifecycle, delivery, reminders, load mode, the
// index write exemption), through the fake host with a scripted judge. The original's mock answers every question
// from one table: a number is a score (0.8 on its own level, the rest shared), a string a choice.

type csExt struct {
	*harness
	mu      sync.Mutex
	answers map[string]any
	model   string
	reqs    []Request
	failure error
	// gate, when set, holds every judge request until closed.
	gate chan struct{}
	// host, when set, answers host calls before the defaults below.
	host2 func(method string, args map[string]any) (map[string]any, string, bool)
}

func (e *csExt) judge(Config, *Budget) (Judge, error) {
	return csJudge(func(ctx context.Context, req Request) (Evaluation, error) {
		e.mu.Lock()
		e.reqs = append(e.reqs, req)
		answers, model, failure, gate := e.answers, e.model, e.failure, e.gate
		e.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return Evaluation{}, ctx.Err()
			}
		}
		if failure != nil {
			return Evaluation{}, failure
		}
		out := map[string]Answer{}
		for id, q := range req.Questions {
			value := answers[id]
			switch q.Type {
			case "choice":
				pick := ""
				for _, k := range q.CriteriaOrder {
					if pick == "" {
						pick = k
					}
				}
				if s, ok := value.(string); ok {
					pick = s
				} else if pick == "" {
					pick = "advance"
				}
				probs := map[string]float64{}
				for k := range q.Criteria {
					if k == pick {
						probs[k] = 0.8
					} else {
						probs[k] = 0.2 / float64(len(q.Criteria)-1)
					}
				}
				out[id] = Answer{Type: "choice", Choice: pick, Confidence: 0.8, Probabilities: probs}
			case "score":
				v := 0.0
				if n, ok := value.(int); ok {
					v = float64(n)
				}
				levels := len(q.Levels)
				probs := map[string]float64{}
				for i := 0; i < levels; i++ {
					if i == int(v+0.5) {
						probs[itoaInt(i)] = 0.8
					} else {
						probs[itoaInt(i)] = 0.2 / float64(levels-1)
					}
				}
				out[id] = Answer{Type: "score", Score: v, Confidence: 0.8, Probabilities: probs}
			}
		}
		return Evaluation{Answers: out, Model: model}, nil
	}), nil
}

func (e *csExt) set(answers map[string]any) {
	e.mu.Lock()
	e.answers = answers
	e.mu.Unlock()
}

func (e *csExt) requests() []Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Request(nil), e.reqs...)
}

type csOpts struct {
	conscience func(*ConscienceConfig)
	cfg        func(*Config)
	noUI       bool
	cwd        string
}

func newCsExt(t *testing.T, o csOpts) *csExt {
	t.Helper()
	e := &csExt{answers: map[string]any{}, model: "jev-1.13.0"}
	cfg := on(BackendTypeSafe)
	cfg.Notices = false
	cfg.Conscience.Enabled = true
	cfg.Conscience.RecommendThreshold = 0.5
	if o.conscience != nil {
		o.conscience(&cfg.Conscience)
	}
	if o.cfg != nil {
		o.cfg(cfg)
	}
	e.harness = startHarness(t, harnessOpts{cfg: cfg, newJudge: e.judge, noUI: o.noUI, cwd: o.cwd, answer: func(method string, args map[string]any) (map[string]any, string, bool) {
		e.mu.Lock()
		hook := e.host2
		e.mu.Unlock()
		if hook != nil {
			if r, msg, ok := hook(method, args); ok {
				return r, msg, ok
			}
		}
		switch method {
		case "getAllTools":
			return map[string]any{"tools": []any{}}, "", true
		case "isProjectTrusted":
			return map[string]any{"trusted": true}, "", true
		case "getContextUsage":
			return map[string]any{"tokens": 5000, "contextWindow": 200000, "percent": 2.5}, "", true
		}
		return nil, "", false
	}})
	e.start()
	return e
}

func csSkillEvent(name, description, path string) map[string]any {
	return map[string]any{"name": name, "description": description, "filePath": path, "baseDir": filepath.Dir(path), "disableModelInvocation": false}
}

func csDefaultSkillEvents() []map[string]any {
	return []map[string]any{csSkillEvent("impeccable", "UI design", "/skills/impeccable/SKILL.md")}
}

// prompt fires before_agent_start with the skills in systemPromptOptions and returns the hook's result.
func (e *csExt) prompt(text string, skills []map[string]any, extra ...map[string]any) map[string]any {
	e.t.Helper()
	list := []any{}
	for _, s := range skills {
		list = append(list, s)
	}
	data := map[string]any{"prompt": text, "systemPromptOptions": map[string]any{"cwd": e.cwd, "skills": list}}
	for _, x := range extra {
		for k, v := range x {
			data[k] = v
		}
	}
	raw := e.host.Fire("before_agent_start", data)
	var reply struct {
		Result map[string]any `json:"_pigPromptResult"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &reply)
	}
	return reply.Result
}

func csMessage(r map[string]any) (customType, content string, ok bool) {
	m, _ := r["message"].(map[string]any)
	if m == nil {
		return "", "", false
	}
	customType, _ = m["customType"].(string)
	content, _ = m["content"].(string)
	return customType, content, true
}

// traceText is the trace as the original's /warden trace prints it: each line with its details.
func (e *csExt) traceText() string {
	var b strings.Builder
	for _, entry := range e.w.trace.Entries() {
		b.WriteString(entry.Line + "\n")
		for _, d := range entry.Details {
			b.WriteString("  " + d + "\n")
		}
	}
	return b.String()
}

func (e *csExt) conscienceMessages() int {
	n := 0
	for _, m := range e.messages() {
		if m.Type == conscienceType {
			n++
		}
	}
	return n
}

func (e *csExt) reminders() []sent {
	var out []sent
	for _, m := range e.steers() {
		if strings.Contains(m.Content, "Reminder") {
			out = append(out, m)
		}
	}
	return out
}

func csAgentEnd(e *csExt, finalText string) {
	e.host.Fire("agent_end", map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "x"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": finalText}}, "stopReason": "stop"},
	}})
}

func csAgentEndToolCall(e *csExt) {
	e.host.Fire("agent_end", map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "x"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "t1", "name": "bash", "arguments": map[string]any{"command": "ls"}}}, "stopReason": "toolUse"},
	}})
}

func csAdvance(first int) map[string]any {
	return map[string]any{"conscience_disposition": "advance", "c1": first}
}

// twin: tests/extension.test.ts:2994
func TestConscienceNoToolLifecycleFixtureForRecommendMode(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "advance", "c1": 3, "c2": 0})
	skills := []map[string]any{csSkillEvent("impeccable", "Frontend interface design, polish, and UX", "/skills/impeccable/SKILL.md"), csSkillEvent("tdd", "Test-driven development", "/skills/tdd/SKILL.md")}
	r := e.prompt("Take a screenshot of this page and make it look better", skills)
	_, content, ok := csMessage(r)
	if !ok || !strings.Contains(strings.ToLower(content), "impeccable") || !strings.Contains(content, "Consider using") {
		t.Fatalf("before_agent_start must return a recommendation: %v", r)
	}
}

func csWriteSkill(t *testing.T, name, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".pi", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func csLoadMode(c *ConscienceConfig) { c.Skills.Mode = "load"; c.Tools.Enabled = false }

// twin: tests/extension.test.ts:3009
func TestConscienceNoToolLifecycleFixtureForLoadMode(t *testing.T) {
	path := csWriteSkill(t, "impeccable", "---\nname: impeccable\ndescription: Frontend interface design, polish, and UX\n---\n\nDetailed instructions for polishing frontend interfaces.")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	r := e.prompt("Take a screenshot of this page and make it look better", []map[string]any{csSkillEvent("impeccable", "Frontend interface design, polish, and UX", path)})
	_, content, ok := csMessage(r)
	if !ok || len(content) <= 100 || !strings.Contains(content, "impeccable") {
		t.Fatalf("load mode should supply the full skill body: %v", r)
	}
}

// twin: tests/extension.test.ts:3033
func TestConsciencedDefaultThresholdTracesAssessmentButDeliversNothing(t *testing.T) {
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { c.RecommendThreshold = 1.0 }})
	e.set(csAdvance(3))
	r := e.prompt("design a landing page", csDefaultSkillEvents())
	if _, _, ok := csMessage(r); ok || e.conscienceMessages() != 0 {
		t.Fatalf("should not deliver a message at threshold 1.0: %v", r)
	}
	tr := e.traceText()
	if !strings.Contains(tr, "conscience") && !strings.Contains(tr, "assessed") || !strings.Contains(tr, "below_threshold") {
		t.Errorf("trace should note below_threshold:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3053
func TestConscienceLoweredThresholdDeliversOneMessageViaHookReturn(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	r := e.prompt("design a landing page", csDefaultSkillEvents())
	ct, content, ok := csMessage(r)
	if !ok || ct != "pi-warden-conscience" || !strings.Contains(content, "impeccable") {
		t.Fatalf("should return a message from the hook: %v", r)
	}
	if e.conscienceMessages() != 0 {
		t.Error("should not use sendMessage")
	}
	if m := r["message"].(map[string]any); m["display"] != true {
		t.Errorf("the message follows steerVisible: %v", m)
	}
}

// twin: tests/extension.test.ts:3067
// twin: tests/conscience-gunner.test.ts:739
func TestConscienceSteerBudgetExhaustedBlocksDelivery(t *testing.T) {
	e := newCsExt(t, csOpts{cfg: func(c *Config) { c.SteerBudget = 0 }})
	e.set(csAdvance(3))
	r := e.prompt("design a landing page", csDefaultSkillEvents())
	if _, _, ok := csMessage(r); ok {
		t.Fatal("should not deliver when budget exhausted")
	}
	if !strings.Contains(e.traceText(), "budget") {
		t.Errorf("trace should note budget exhaustion:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3085
func TestConscienceACapabilityHeldWithNoPolicyIsNeverSentLaterByTheAgentEndReminder(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.mu.Lock()
	e.model = "jev-other"
	e.mu.Unlock()
	if _, _, ok := csMessage(e.prompt("design a landing page", csDefaultSkillEvents())); ok {
		t.Fatal("the activation gate holds the first recommendation")
	}
	csAgentEnd(e, "I designed the landing page.")
	if r := e.reminders(); len(r) != 0 {
		t.Errorf("the reminder must not deliver what the gate held: %v", r)
	}
	if !strings.Contains(e.traceText(), "no policy for current hash/model") {
		t.Errorf("trace:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3100
func TestConscienceTheAgentEndReminderStillSendsACapabilityThatPassedTheActivationGate(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("design a landing page", csDefaultSkillEvents())); !ok {
		t.Fatal("the first recommendation passes the gate")
	}
	csAgentEndToolCall(e)
	var n int
	for _, m := range e.steers() {
		if strings.Contains(m.Content, `Reminder: consider using the "impeccable" skill`) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("reminders: %d", n)
	}
}

// twin: tests/extension.test.ts:3113
func TestConscienceNoAgentEndReminderAfterTheRunEndedWithAFinalTextReply(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("design a landing page", csDefaultSkillEvents())); !ok {
		t.Fatal("the first recommendation passes the gate")
	}
	csAgentEnd(e, "I designed the landing page.")
	if r := e.reminders(); len(r) != 0 {
		t.Errorf("the agent already answered: %v", r)
	}
}

// twin: tests/extension.test.ts:3125
// twin: tests/conscience-gunner.test.ts:745
func TestConscienceSessionStartDuringAssessmentProducesStaleTrace(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("first prompt", csDefaultSkillEvents())); !ok {
		t.Fatal("first prompt should deliver")
	}
	e.start()
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("second prompt after reload", csDefaultSkillEvents())); !ok {
		t.Fatal("second prompt after session_start should deliver (generation reset)")
	}
	if !strings.Contains(e.traceText(), "assessed") {
		t.Error("trace should have conscience entries")
	}
}

// Go-only: an assessment that is still running when the operator types is stale: nothing is delivered, the trace
// says so (the in-flight half of tests/extension.test.ts:3125 and the input handler, extension.ts 2273).
func TestConscienceOperatorInputDuringAssessmentMakesItStale(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	gate := make(chan struct{})
	e.mu.Lock()
	e.gate = gate
	e.mu.Unlock()
	done := make(chan map[string]any, 1)
	go func() { done <- e.prompt("design a landing page", csDefaultSkillEvents()) }()
	waitUntil(t, func() bool { return len(e.requests()) > 0 })
	e.host.Fire("input", map[string]any{"text": "wait, something else", "source": "interactive"})
	close(gate)
	r := <-done
	if _, _, ok := csMessage(r); ok {
		t.Fatalf("a stale assessment delivers nothing: %v", r)
	}
	if tr := e.traceText(); !strings.Contains(tr, "stale: generation changed during assessment") || !strings.Contains(tr, "skipReason: stale") || !strings.Contains(tr, "generation: 0 → 1") {
		t.Errorf("trace:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3147
func TestConscienceExplicitSkillInvocationTracedAsExplicitSkill(t *testing.T) {
	e := newCsExt(t, csOpts{})
	r := e.prompt("Use /skill:tdd to write tests", nil)
	if _, _, ok := csMessage(r); ok {
		t.Fatal("explicit skill invocation should not return a message")
	}
	tr := e.traceText()
	if !strings.Contains(tr, "explicit skill invocation") || !strings.Contains(tr, "tdd") {
		t.Errorf("trace:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3158
func TestConscienceJudgeErrorFailsOpenWithTrace(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.mu.Lock()
	e.failure = &IntegrationError{Code: "http", Message: "TypeSafe request failed (HTTP 400)."}
	e.mu.Unlock()
	r := e.prompt("design a landing page", csDefaultSkillEvents())
	if _, _, ok := csMessage(r); ok {
		t.Fatal("should not deliver on error")
	}
	tr := e.traceText()
	if !strings.Contains(tr, "skipReason: error") || strings.Contains(tr, "upstream") || strings.Contains(tr, "bad request") {
		t.Errorf("trace should note error and carry no exception body:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3182
func TestConscienceLongPromptIsTruncatedAndTruncationRecordedInTrace(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "no_gap"})
	e.prompt(strings.Repeat("x", 2500), csDefaultSkillEvents())
	for _, r := range e.requests() {
		if n := len(r.State["task"].(string)); n > 2000 {
			t.Errorf("prompt in state should be truncated to 2000 chars, got %d", n)
		}
	}
	tr := e.traceText()
	if !strings.Contains(tr, "assessed") || !strings.Contains(tr, "truncat") {
		t.Errorf("trace should record truncation:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3200
func TestConscienceConfigDefaultsMatchTheExpectedSchema(t *testing.T) {
	c := DefaultConfig().Conscience
	if c.Enabled || c.Skills.Mode != "recommend" || !c.Tools.Enabled || c.TimeoutMs != 3000 || c.MaxAssessments != 3 || c.MaxNudges != 2 {
		t.Errorf("%+v", c)
	}
}

// twin: tests/extension.test.ts:3214
func TestConscienceToolCallAndToolResultAreTracedForSelectedCapability(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	e.toolCall("read", "read-1", map[string]any{"path": "/skills/impeccable/SKILL.md"})
	e.toolResult("read", map[string]any{"path": "/skills/impeccable/SKILL.md"}, "Skill body", false)
	e.host.Fire("agent_settled", nil)
	tr := e.traceText()
	if !strings.Contains(tr, "read_observed: impeccable") || !strings.Contains(tr, "settled") {
		t.Errorf("trace:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3231
// twin: tests/conscience-gunner.test.ts:751
func TestConscienceMaxAssessmentsBlocksFurtherAssessments(t *testing.T) {
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { c.MaxAssessments = 1 }})
	e.set(csAdvance(3))
	e.prompt("fix the bug", csDefaultSkillEvents())
	e.toolCall("bash", "call-1", map[string]any{"command": "npm test"})
	e.toolResult("bash", map[string]any{"command": "npm test"}, "FAIL", true)
	e.host.Fire("turn_end", map[string]any{"turnIndex": 1})
	if strings.Contains(e.traceText(), "turn_end reassessment") {
		t.Error("should not re-assess when maxAssessments reached")
	}
}

// twin: tests/extension.test.ts:3249
func TestConscienceAgentEndProducesSettledTraceEntry(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	csAgentEnd(e, "Done")
	e.host.Fire("agent_settled", nil)
	if !strings.Contains(e.traceText(), "settled") {
		t.Errorf("trace should have settled entry after agent_settled:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3263
func TestConscienceReminderSuppressedWhenDispositionIsAwaitingUser(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "awaiting_user", "c1": 3})
	e.prompt("design a landing page", csDefaultSkillEvents())
	csAgentEnd(e, "What style?")
	if r := e.reminders(); len(r) != 0 {
		t.Errorf("should not remind when awaiting_user: %v", r)
	}
}

// twin: tests/extension.test.ts:3276
func TestConscienceReadOfSkillFileMarksReadObserved(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	e.toolCall("read", "read-1", map[string]any{"path": "/skills/impeccable/SKILL.md"})
	e.toolResult("read", map[string]any{"path": "/skills/impeccable/SKILL.md"}, "Skill instructions...", false)
	if !strings.Contains(e.traceText(), "read_observed: impeccable") {
		t.Errorf("trace should note read_observed:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3291
func TestConscienceOrdinaryReadDoesNotTriggerReadObserved(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	e.toolCall("read", "read-2", map[string]any{"path": "src/index.ts"})
	e.toolResult("read", map[string]any{"path": "src/index.ts"}, "code", false)
	// The original's assertion: the read's path is not what the trace names. (Its tool_result handler settles the
	// pending skill on any read; only tool_call matches the path.)
	tr := e.traceText()
	if strings.Contains(tr, "read_observed: impeccable") && strings.Contains(tr, "src/index.ts") {
		t.Errorf("an ordinary read should not match the skill file:\n%s", tr)
	}
	if strings.Contains(tr, "trigger: tool_call") {
		t.Errorf("the tool_call hook matches the skill's path only:\n%s", tr)
	}
}

// twin: tests/extension.test.ts:3307
// twin: tests/conscience-gunner.test.ts:377
func TestConscienceAQueuedPromptIsAssessedWithTheTaskSpine(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "no_gap"})
	e.say("user", "add a rate limiter")
	e.say("user", "wire it into the app")
	e.prompt("wire it into the app", csDefaultSkillEvents())
	// The prompt was admitted through before_agent_start; the next user message arrives queued.
	e.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "user", "content": "wire it into the app"}})
	before := len(e.requests())
	e.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "user", "content": "now the tests"}})
	reqs := e.requests()
	if len(reqs) <= before {
		t.Fatal("the queued prompt was assessed")
	}
	st := reqs[len(reqs)-1].State
	want := map[string]any{"goal": "add a rate limiter", "task_history": []string{"wire it into the app"}}
	if st["task"] != "now the tests" || !jsonEqual(st["spine"], want) {
		t.Errorf("state %v", st)
	}
	if !strings.Contains(e.traceText(), "queued prompt assessed") {
		t.Errorf("trace:\n%s", e.traceText())
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// twin: tests/extension.test.ts:3328
func TestConscienceNoSkillsAvailableDeliversNothingAndDoesNotCrash(t *testing.T) {
	e := newCsExt(t, csOpts{})
	if _, _, ok := csMessage(e.prompt("direct input", nil)); ok {
		t.Error("no message when no skills available")
	}
}

// Go-only: a queued prompt with no skill snapshot, or no judge, is traced as catalog_unavailable (extension.ts 1351).
func TestConscienceQueuedPromptWithoutSkillsIsTracedCatalogUnavailable(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "user", "content": "first"}})
	e.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "user", "content": "queued"}})
	if !strings.Contains(e.traceText(), "queued prompt: no judge or no skills") || !strings.Contains(e.traceText(), "skipReason: catalog_unavailable") {
		t.Errorf("trace:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3340
func TestConscienceToolFailureTriggersOneTurnEndReassessmentSecondDoesNot(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("fix the bug", csDefaultSkillEvents())
	afterInit := len(e.requests())
	if afterInit < 1 {
		t.Fatalf("initial assessment should make at least 1 request, got %d", afterInit)
	}
	fail := func(id string) {
		e.toolCall("read", id, map[string]any{"path": "/skills/impeccable/SKILL.md"})
		e.toolResult("read", map[string]any{"path": "/skills/impeccable/SKILL.md"}, "", true)
	}
	fail("r1")
	e.host.Fire("turn_end", map[string]any{"turnIndex": 1})
	afterFirst := len(e.requests())
	if afterFirst <= afterInit {
		t.Fatalf("turn_end should add a request, got %d (was %d)", afterFirst, afterInit)
	}
	fail("r2")
	e.host.Fire("turn_end", map[string]any{"turnIndex": 2})
	if got := len(e.requests()); got != afterFirst {
		t.Errorf("second turn_end should not add a request, got %d (was %d)", got, afterFirst)
	}
}

// twin: tests/extension.test.ts:3367
func TestConscienceReminderFiresOnceSecondAgentEndSaysUnresolved(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	csAgentEndToolCall(e)
	r1 := e.reminders()
	if len(r1) < 1 || !strings.Contains(r1[0].Content, "impeccable") {
		t.Fatalf("first agent_end should send a reminder: %v", r1)
	}
	csAgentEndToolCall(e)
	if got := len(e.reminders()); got != len(r1) {
		t.Errorf("second agent_end should not send a reminder: %d", got)
	}
	e.host.Fire("agent_settled", nil)
	if !strings.Contains(e.traceText(), "settled: reminded") {
		t.Errorf("trace should say reminded after the reminder was sent:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3391
func TestConscienceReminderSuppressedOnAwaitingUserAndWhenNudgesExhausted(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "awaiting_user", "c1": 3})
	e.prompt("what style?", csDefaultSkillEvents())
	csAgentEndToolCall(e)
	if r := e.reminders(); len(r) != 0 {
		t.Errorf("reminder suppressed when awaiting_user: %v", r)
	}
	e2 := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { c.MaxNudges = 1 }})
	e2.set(csAdvance(3))
	e2.prompt("design a page", csDefaultSkillEvents())
	csAgentEndToolCall(e2)
	first := len(e2.reminders())
	if first < 1 {
		t.Fatal("first agent_end should send the one allowed reminder")
	}
	csAgentEndToolCall(e2)
	if got := len(e2.reminders()); got != first {
		t.Errorf("reminder suppressed when maxNudges exhausted: %d", got)
	}
}

// twin: tests/extension.test.ts:3427
func TestConscienceLoadModeLifecycleFixtureDeliversSkillBody(t *testing.T) {
	path := csWriteSkill(t, "test-skill", "---\nname: test-skill\ndescription: A test skill\n---\n\nThis is the skill body.")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	_, content, ok := csMessage(e.prompt("use test-skill", []map[string]any{csSkillEvent("test-skill", "A test skill", path)}))
	for _, want := range []string{"Skill: test-skill", "This is the skill body", "Resolve this skill"} {
		if !ok || !strings.Contains(content, want) {
			t.Errorf("message lacks %q: %q", want, content)
		}
	}
}

// twin: tests/extension.test.ts:3453
func TestConscienceRecommendModeNeverReadsSkillFiles(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel-skill.txt")
	os.WriteFile(sentinel, []byte("never-read"), 0o644)
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("do something", []map[string]any{csSkillEvent("sentinel", "Sentinel skill", sentinel)})
	// The skill file is hashed to find its index entry (the original does too); recommend mode never loads its text.
	if raw, _ := os.ReadFile(sentinel); string(raw) != "never-read" {
		t.Error("recommend mode should not change the skill file")
	}
	if strings.Contains(e.traceText(), "loaded:") {
		t.Error("recommend mode loads nothing")
	}
}

// twin: tests/extension.test.ts:3473
// twin: tests/conscience-gunner.test.ts:346
func TestConscienceLoadDeliversBodyAndSetsInstructionsSupplied(t *testing.T) {
	path := csWriteSkill(t, "my-skill", "---\nname: my-skill\ndescription: My skill\n---\n\nBody text.")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	_, content, ok := csMessage(e.prompt("use my-skill", []map[string]any{csSkillEvent("my-skill", "My skill", path)}))
	for _, want := range []string{"Skill: my-skill", "Body text.", "Resolve this skill"} {
		if !ok || !strings.Contains(content, want) {
			t.Errorf("message lacks %q: %q", want, content)
		}
	}
	e.host.Fire("agent_settled", nil)
	// The state the load leaves: queued. (The original moves it on only for an assistant message that carries the
	// conscience's customType, which the host never emits: the settled line says queued on both.)
	if !strings.Contains(e.traceText(), "instructions: queued") {
		t.Errorf("trace:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3495
func TestConscienceJudgeAnswerWithPathDoesNotBypassLoadSafety(t *testing.T) {
	path := csWriteSkill(t, "safe-skill", "---\nname: safe-skill\ndescription: Safe\n---\n\nClean body.")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	_, content, ok := csMessage(e.prompt("use safe-skill", []map[string]any{csSkillEvent("safe-skill", "Safe", path)}))
	if !ok || !strings.Contains(content, "Clean body") {
		t.Errorf("%q", content)
	}
}

// twin: tests/extension.test.ts:3516
func TestConscienceOversizedSkillBodyRejected(t *testing.T) {
	path := csWriteSkill(t, "big-skill", "---\nname: big-skill\ndescription: Big\n---\n\n"+strings.Repeat("x", 50000))
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { csLoadMode(c); c.MaxSkillBytes = 1000 }})
	e.set(csAdvance(3))
	_, content, ok := csMessage(e.prompt("use big-skill", []map[string]any{csSkillEvent("big-skill", "Big", path)}))
	if !ok || !strings.Contains(content, "Consider using") {
		t.Errorf("should fall back to recommend: %q", content)
	}
	if !strings.Contains(e.traceText(), "skipReason: load_too_large") {
		t.Errorf("trace:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3539
// twin: tests/extension.test.ts:3543
// twin: tests/extension.test.ts:3547
// twin: tests/extension.test.ts:3551
func TestActivationGateIsAPureFunctionOfPolicyHashAndModel(t *testing.T) {
	if policyMatches(nil, "fb2d35042f667b3c", "jev-1.13.0") {
		t.Error("no policy never matches")
	}
	if !policyMatches(conscienceBetaPolicy, "fb2d35042f667b3c", "jev-1.13.0") {
		t.Error("matching hash and model pass")
	}
	if policyMatches(conscienceBetaPolicy, "0000000000000000", "jev-1.13.0") {
		t.Error("hash mismatch fails")
	}
	if policyMatches(conscienceBetaPolicy, "fb2d35042f667b3c", "jev-other") || policyMatches(conscienceBetaPolicy, "fb2d35042f667b3c", "") {
		t.Error("model mismatch fails, and an empty model never matches")
	}
}

// twin: tests/extension.test.ts:3559
func TestConscienceModelMismatchBlocksDelivery(t *testing.T) {
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { c.RecommendThreshold = 0.0 }})
	e.mu.Lock()
	e.model = "jev-other"
	e.mu.Unlock()
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("design a page", csDefaultSkillEvents())); ok {
		t.Fatal("a model the policy does not name blocks delivery")
	}
	if !strings.Contains(e.traceText(), "no_policy") {
		t.Errorf("trace should note no_policy:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3573
func TestConscienceCredentialCanaryNeverLeaksToTraceOrMessage(t *testing.T) {
	path := csWriteSkill(t, "canary-skill", "---\nname: canary-skill\ndescription: Has a secret\n---\n\nToken: ghp_ABCDEFGHIJKLMNOPqrstuvwxyz1234567890\n")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	r := e.prompt("use canary-skill", []map[string]any{csSkillEvent("canary-skill", "Has a secret", path)})
	if strings.Contains(e.traceText(), "ghp_") {
		t.Error("trace must not contain credential")
	}
	if _, content, ok := csMessage(r); ok && strings.Contains(content, "ghp_") {
		t.Error("message must not contain credential")
	}
	if !strings.Contains(e.traceText(), "skipReason: metadata_unsafe") {
		t.Errorf("the load is refused:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3624
func TestConscienceUnreadableSkillFileFailsLoad(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	path := csWriteSkill(t, "locked-skill", "---\nname: locked-skill\ndescription: Locked\n---\n\nBody.")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	os.Chmod(path, 0)
	defer os.Chmod(path, 0o644)
	e.prompt("use locked-skill", []map[string]any{csSkillEvent("locked-skill", "Locked", path)})
	if !strings.Contains(e.traceText(), "load_failed") {
		t.Errorf("trace should note load_failed:\n%s", e.traceText())
	}
}

// twin: tests/extension.test.ts:3649
func TestConscienceUserOnlySkillIsNotLoaded(t *testing.T) {
	path := csWriteSkill(t, "user-only-skill", "---\nname: user-only-skill\ndescription: User only\ndisable-model-invocation: true\n---\n\nBody.")
	e := newCsExt(t, csOpts{conscience: csLoadMode})
	e.set(csAdvance(3))
	skill := csSkillEvent("user-only-skill", "User only", path)
	skill["disableModelInvocation"] = true
	if _, _, ok := csMessage(e.prompt("use user-only-skill", []map[string]any{skill})); ok {
		t.Error("user-only skill should not produce a message")
	}
}

// twin: tests/extension.test.ts:3668
func TestConscienceExcludedSkillIsNotLoaded(t *testing.T) {
	path := csWriteSkill(t, "excluded-skill", "---\nname: excluded-skill\ndescription: Excluded\n---\n\nBody.")
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { csLoadMode(c); c.Skills.Exclude = []string{"excluded-skill"} }})
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("use excluded-skill", []map[string]any{csSkillEvent("excluded-skill", "Excluded", path)})); ok {
		t.Error("excluded skill should not produce a message")
	}
}

// twin: tests/extension.test.ts:3687
func TestConscienceAbsolutePathInPromptNeverLeaks(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	r := e.prompt("design /Users/secret/project/layout.ts", csDefaultSkillEvents())
	for _, req := range e.requests() {
		if strings.Contains(jsonCompact(req.State), "/Users/secret") {
			t.Error("judge request must not contain absolute path")
		}
	}
	if strings.Contains(e.traceText(), "/Users/secret") {
		t.Error("trace must not contain absolute path")
	}
	if _, content, ok := csMessage(r); ok && strings.Contains(content, "/Users/secret") {
		t.Error("message must not contain absolute path")
	}
}

// twin: tests/extension.test.ts:3706
func TestConscienceWardenSteerDoesNotInvalidateConscienceAssessment(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	e.host.Fire("turn_end", map[string]any{"turnIndex": 1})
	if strings.Contains(e.traceText(), "stale") {
		t.Errorf("conscience should not be stale after a warden steer:\n%s", e.traceText())
	}
	if e.w.conscience.gen() != 0 {
		t.Errorf("generation %d", e.w.conscience.gen())
	}
}

var reIndexRunningNotice = regexp.MustCompile(`building the capability index`)

// twin: tests/extension.test.ts:3956
// twin: tests/extension.test.ts:3968
func TestIndexWriteBypassAbsoluteAndTildePathsToTheGlobalIndexAreAllowedWhileIndexRunning(t *testing.T) {
	e := newCsExt(t, csOpts{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	global := filepath.Join(home, "idx", "global.json")
	e.w.setIndexRunning(true, []string{global})
	for _, p := range []string{global, "~/idx/global.json"} {
		if r := e.toolCall("write", "w-"+p, map[string]any{"path": p, "content": "{}"}); r != nil {
			t.Errorf("%s should be allowed: %v", p, r)
		}
	}
	if n := strings.Count(e.traceText(), "index write, expected"); n != 2 {
		t.Errorf("both writes are traced as expected: %d\n%s", n, e.traceText())
	}
}

// twin: tests/extension.test.ts:3981
// twin: tests/extension.test.ts:3995
// twin: tests/extension.test.ts:4003
func TestIndexWriteBypassSiblingFilesAndAStoppedIndexRunAreJudgedNormally(t *testing.T) {
	e := newCsExt(t, csOpts{})
	dir := t.TempDir()
	global := filepath.Join(dir, "global.json")
	e.w.setIndexRunning(true, []string{global})
	e.toolCall("write", "w1", map[string]any{"path": filepath.Join(dir, "sibling.json"), "content": "{}"})
	if strings.Contains(e.traceText(), "index write, expected") {
		t.Error("a sibling file in the same directory is judged, not exempt")
	}
	e.w.setIndexRunning(false, nil)
	e.toolCall("write", "w2", map[string]any{"path": global, "content": "{}"})
	if strings.Contains(e.traceText(), "index write, expected") {
		t.Error("the index path is judged when no index run is on, and after the flag clears")
	}
}

// Go-only: while /warden index runs, the operator's input waits (extension.ts 2268).
func TestInputWaitsWhileTheIndexIsBeingBuilt(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.w.setIndexRunning(true, nil)
	raw := e.host.Fire("input", map[string]any{"text": "hello", "source": "interactive"})
	if !strings.Contains(string(raw), `"handled"`) {
		t.Errorf("input should be handled: %s", raw)
	}
	var notified bool
	for _, n := range e.notices() {
		if reIndexRunningNotice.MatchString(n) {
			notified = true
		}
	}
	if !notified {
		t.Errorf("the user is told: %v", e.notices())
	}
	e.w.setIndexRunning(false, nil)
	if raw := e.host.Fire("input", map[string]any{"text": "hello"}); strings.Contains(string(raw), "handled") {
		t.Errorf("input passes when no index run is on: %s", raw)
	}
}

// Go-only: without consent the assessment is traced no_consent (the second half of the case at extension.test.ts line 4417; the
// first half, a saved rejected key, is a named skip: the port keeps no saved key state).
func TestConscienceMissingConsentTracesNoConsent(t *testing.T) {
	e := newCsExt(t, csOpts{cfg: func(c *Config) { c.Consent = "" }})
	e.prompt("design a landing page", csDefaultSkillEvents())
	tr := e.traceText()
	if !strings.Contains(tr, "skipReason: no_consent") || strings.Contains(tr, "key_rejected") {
		t.Errorf("trace:\n%s", tr)
	}
}

// Go-only: a missing key for the chosen backend is traced no_key, not no_consent.
func TestConscienceMissingKeyTracesNoKey(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.w.opts.NewJudge = nil
	e.w.opts.Getenv = func(string) string { return "" }
	e.prompt("design a landing page", csDefaultSkillEvents())
	if tr := e.traceText(); !strings.Contains(tr, "skipReason: no_key") {
		t.Errorf("trace:\n%s", tr)
	}
}
