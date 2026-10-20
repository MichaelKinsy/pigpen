package warden

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Layer-1 tests of the supervision wiring through the fake host: call-waste notes on tool results and the prompt
// tip, the stuck-loop diff, subagent report triage on agent_settled, and the message and input event registries.

func resultPatch(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("tool_result reply %s: %v", raw, err)
	}
	return out.Content
}

func (h *harness) fireResult(tool string, input map[string]any, text string) []map[string]any {
	h.t.Helper()
	raw := h.host.Fire("tool_result", map[string]any{"toolName": tool, "toolCallId": "r", "input": input, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": false})
	return resultPatch(h.t, raw)
}

// traceText is the session trace with each entry's details, as /warden trace renders it upstream.
func (h *harness) traceText() string {
	h.t.Helper()
	var out []string
	for _, e := range h.w.trace.Entries() {
		out = append(out, e.Line)
		out = append(out, e.Details...)
	}
	return strings.Join(out, "\n")
}

func TestAWastePollNoteRidesTheSecondPollResultOnly(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendNone)})
	h.start()
	poll := map[string]any{"command": pollCommand}
	if got := h.fireResult("bash", poll, `{"status":"running"}`); got != nil {
		t.Fatalf("one poll is waiting: %v", got)
	}
	got := h.fireResult("bash", poll, `{"status":"running"}`)
	if len(got) != 1 {
		t.Fatalf("content %v", got)
	}
	text, _ := got[0]["text"].(string)
	mustMatch(t, text, `^\{"status":"running"\}\n\nYou have waited 2 times in the last 2 calls\. Wait once instead:`, "result text carries the note")
	mustMatch(t, h.traceText(), `warden · waste · sleep · 2 waits in 2 calls · agent told`, "trace line")
	if len(h.steers()) != 0 {
		t.Errorf("a waste note is not a steer: %v", h.steers())
	}
	// The rate limit: the next poll inside the window gets no second note.
	if got := h.fireResult("bash", poll, `{"status":"running"}`); got != nil {
		t.Errorf("rate limited: %v", got)
	}
}

func TestAWasteNoteIsOffWhenTheGuardOrTheDetectorIsOff(t *testing.T) {
	for _, change := range []func(*Config){func(c *Config) { c.Waste.Enabled = false }, func(c *Config) { c.Waste.Sleep = false }} {
		c := on(BackendNone)
		change(c)
		h := startHarness(t, harnessOpts{cfg: c})
		h.start()
		poll := map[string]any{"command": pollCommand}
		h.fireResult("bash", poll, "x")
		if got := h.fireResult("bash", poll, "x"); got != nil {
			t.Errorf("a note with the guard off: %v", got)
		}
	}
}

func (h *harness) beforeAgentStart(prompt string, options map[string]any) map[string]any {
	h.t.Helper()
	raw := h.host.Fire("before_agent_start", map[string]any{"prompt": prompt, "systemPromptOptions": options})
	var out struct {
		Options map[string]any `json:"_pigPromptOptions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		h.t.Fatalf("%s: %v", raw, err)
	}
	return out.Options
}

// twin: tests/extension.test.ts:4756
func TestWasteTheSessionTipIsOffByDefaultAndAppendedToThePromptOnceWhenWasteTipIsOn(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendNone)})
	h.start()
	quiet := h.beforeAgentStart("read the file", map[string]any{"cwd": "/x", "skills": []any{}})
	if s, has := quiet["appendSystemPrompt"]; has && s != "" {
		t.Errorf("the default config appends nothing to the prompt: %v", s)
	}
	if n := strings.Count(h.traceText(), "waste · session tip"); n != 0 {
		t.Errorf("a tip that was never offered is not traced: %d", n)
	}

	c := on(BackendNone)
	c.Waste.Tip = true
	if err := SaveConfig(ConfigPath(h.home), *c); err != nil {
		t.Fatal(err)
	}
	h.start()
	options := map[string]any{"cwd": "/x", "skills": []any{}}
	got := h.beforeAgentStart("read the file", options)
	mustMatch(t, got["appendSystemPrompt"].(string), `^Tool calls are expensive: each one re-reads the whole conversation\.`, "tip")
	// A later run re-appends the same text; that is what keeps it in the prompt instead of being diffed away.
	again := h.beforeAgentStart("read it again", map[string]any{"cwd": "/x", "skills": []any{}, "appendSystemPrompt": got["appendSystemPrompt"]})
	if n := strings.Count(again["appendSystemPrompt"].(string), "Tool calls are expensive"); n != 1 {
		t.Errorf("the tip appears once: %d", n)
	}
	if n := strings.Count(h.traceText(), "waste · session tip"); n != 1 {
		t.Errorf("the trace records the tip delivery once per session: %d", n)
	}
}

func TestTheWasteTipLandsAfterTheHostsOwnAppendedPrompt(t *testing.T) {
	c := on(BackendNone)
	c.Waste.Tip = true
	h := startHarness(t, harnessOpts{cfg: c})
	h.start()
	want := "BASE PROMPT\n\n" + WasteTip
	for range 2 {
		if got := h.beforeAgentStart("go", map[string]any{"appendSystemPrompt": "BASE PROMPT"})["appendSystemPrompt"]; got != want {
			t.Errorf("run: %q", got)
		}
	}
}

// twin: tests/extension.test.ts:4777
func TestWasteANudgeRidesTheToolResultAndNeverBlocksACallOrChangesAHold(t *testing.T) {
	j := actionJudge(0.95, 0.1, "expected_step", nil)
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), newJudge: func(Config, *Budget) (Judge, error) { return j, nil }})
	h.start()
	var patch []map[string]any
	for i, r := range [][2]int{{1, 40}, {20, 30}, {30, 25}} {
		raw := h.host.Fire("tool_result", map[string]any{
			"toolName": "read", "toolCallId": "waste-" + itoaInt(i), "input": map[string]any{"path": "src/waste.ts", "offset": float64(r[0]), "limit": float64(r[1])},
			"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x\n", r[1]-1) + "x"}}, "isError": false, "details": map[string]any{},
		})
		patch = resultPatch(t, raw)
	}
	var texts []string
	for _, p := range patch {
		s, _ := p["text"].(string)
		texts = append(texts, s)
	}
	mustMatch(t, strings.Join(texts, "\n"), `You read src/waste\.ts in 3 calls`, "the note")
	if reason, isBlocked := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); !isBlocked {
		t.Errorf("the same destructive call is still held with the waste guard on: %q", reason)
	}
	j.handler = actionJudge(0.1, 0.1, "expected_step", nil).handler
	if r := h.toolCall("bash", "c2", map[string]any{"command": "npm test"}); r != nil {
		if _, isBlocked := blocked(r); isBlocked {
			t.Errorf("an ordinary call still runs: %v", r)
		}
	}
}

// twin: tests/extension.test.ts:4795
func TestWasteTheTriggerLineInTheTraceCarriesARedactedCommandNeverACredential(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendNone)})
	h.start()
	token := "sk-abc123def456ghi789"
	command := `sleep 5; curl -s -H "Authorization: Bearer ` + token + `" http://127.0.0.1:4000/api/state | jq .status`
	for range 2 {
		h.fireResult("bash", map[string]any{"command": command}, "{\n  \"status\": \"running\"\n}")
	}
	rendered := h.traceText()
	mustMatch(t, rendered, `waste · sleep`, "the note is in the trace")
	mustMatch(t, rendered, `Authorization: \[redacted\]`, "the command preview is the redacted one")
	mustNotContain(t, rendered, token, "the command's credential must not reach the trace")
	mustNotContain(t, rendered, "Bearer sk-", "no part of the credential reaches the trace")
}

func (h *harness) result(call map[string]any, out string, failed bool) []map[string]any {
	h.t.Helper()
	raw := h.host.Fire("tool_result", map[string]any{"toolName": "bash", "toolCallId": "r", "input": call, "content": []any{map[string]any{"type": "text", "text": out}}, "isError": failed})
	return resultPatch(h.t, raw)
}

func stuckHarness(t *testing.T, mutate func(*Config)) *harness {
	c := on(BackendTypeSafe)
	mutate(c)
	h := startHarness(t, harnessOpts{cfg: c, newJudge: func(Config, *Budget) (Judge, error) { return stuckJudge(0.9, 0.1, 0.1), nil }})
	h.start()
	h.beforeAgentStart("Run the test suite", map[string]any{"cwd": "/x"})
	return h
}

func diffText(patch []map[string]any) string {
	for _, p := range patch {
		if p["type"] == "text" {
			s, _ := p["text"].(string)
			return s
		}
	}
	return ""
}

// twin: tests/extension.test.ts:3739
func TestStuckLoopDiffThreeFailuresWithDifferingOutputsCarryADiffNote(t *testing.T) {
	h := stuckHarness(t, func(c *Config) { c.Stuck.DiffLimit = 5000 })
	var pad []string
	for i := range 50 {
		pad = append(pad, "context-line-"+strings.Repeat("0", 2-len(itoaInt(i)))+itoaInt(i)+": "+strings.Repeat("y", 30))
	}
	base := "FAIL tests/a.test.ts\n  Expected true, got false\n" + strings.Join(pad, "\n")
	call := map[string]any{"command": "npm test"}
	if h.result(call, base+"\noutcome-A", true) != nil || h.result(call, base+"\noutcome-B", true) != nil {
		t.Fatal("first and second stay")
	}
	text := diffText(h.result(call, base+"\noutcome-C", true))
	mustMatch(t, text, `stuck-loop diff`, "diff note")
	mustMatch(t, text, `outcome-C`, "the new line")
	path := regexpFind(t, text, `Full output: (.+)`)
	if len(text) >= len(base+"\noutcome-C") {
		t.Error("diff note is smaller than the original")
	}
	if saved, err := os.ReadFile(path); err != nil || string(saved) != base+"\noutcome-C" {
		t.Errorf("the full output is saved exactly: %v", err)
	}
}

func TestStuckLoopDiffTheSameOutputAgainIsAOneLineNoteThatReusesTheSavedPath(t *testing.T) {
	h := stuckHarness(t, func(c *Config) { c.Stuck.Cooldown = 0 })
	call := map[string]any{"command": "npm run build"}
	a, b, c := longOutput("a"), longOutput("b"), longOutput("c")
	h.result(call, a, true)
	h.result(call, b, true)
	first := diffText(h.result(call, c, true))
	path := regexpFind(t, first, `Full output: (\S+output\.txt)`)
	again := diffText(h.result(call, c, true))
	mustMatch(t, again, `Outputs are byte-identical; see the full output at `+regexpQuote(path), "identical note")
	if len(again) >= len(c) {
		t.Error("the note never grows the result")
	}
}

func TestStuckLoopDiffIdenticalFailuresAreNotReplacedByADiff(t *testing.T) {
	h := stuckHarness(t, func(c *Config) {})
	call := map[string]any{"command": "npm test"}
	full := "FAIL tests/a.test.ts\n  Expected true, got false\n" + strings.Repeat("x", 20_000)
	for i := range 3 {
		if text := diffText(h.result(call, full, true)); strings.Contains(text, "stuck-loop diff") {
			t.Errorf("run %d: a byte-identical failure with nothing saved is not a diff: %q", i, text[:60])
		}
	}
}

// twin: tests/extension.test.ts:3769
func TestStuckLoopDiffThreeIdenticalSuccessesAreNotReplaced(t *testing.T) {
	h := stuckHarness(t, func(c *Config) {})
	call := map[string]any{"command": "npm test"}
	for i := range 3 {
		if h.result(call, "all 42 tests passed\n", false) != nil {
			t.Errorf("run %d: successful repeats are not replaced", i)
		}
	}
}

// twin: tests/extension.test.ts:3778
func TestStuckLoopDiffTwoFailuresThenASuccessLeaveTheSuccessUnchanged(t *testing.T) {
	h := stuckHarness(t, func(c *Config) {})
	call := map[string]any{"command": "npm test"}
	fail := "FAIL tests/a.test.ts\n  Expected true, got false"
	if h.result(call, fail, true) != nil || h.result(call, fail, true) != nil || h.result(call, "all 42 tests passed", false) != nil {
		t.Error("nothing is replaced")
	}
}

func TestStuckLoopDiffNeverGrowsAShortResult(t *testing.T) {
	h := stuckHarness(t, func(c *Config) {})
	call := map[string]any{"command": "npm test"}
	h.result(call, "FAIL a", true)
	h.result(call, "FAIL b", true)
	if got := h.result(call, "FAIL c", true); got != nil {
		t.Errorf("a diff note longer than the result is not sent: %v", got)
	}
}

func TestStuckLoopDiffStopsWhenTheSameTargetChurnsPastTheThreshold(t *testing.T) {
	h := stuckHarness(t, func(c *Config) { c.Stuck.Cooldown = 0 })
	call := map[string]any{"command": "npm run build"}
	outputs := []string{longOutput("a"), longOutput("b"), longOutput("c"), longOutput("d"), longOutput("e")}
	var patched []bool
	for _, o := range outputs {
		patched = append(patched, h.result(call, o, true) != nil)
	}
	mustEqual(t, patched, []bool{false, false, true, true, false}, "the 5th call of one target is a churn verdict, which keeps the result whole")
}

// userCountingAdaptor counts the settles of the user's reply.
type userCountingAdaptor struct {
	sendAllAdaptor
	users *int
}

func (a userCountingAdaptor) user() []SteerOutcome { *a.users++; return nil }

func TestStuckLoopDiffStuckEnabledFalsePreventsAnyReplacement(t *testing.T) {
	h := stuckHarness(t, func(c *Config) { c.Stuck.Enabled = false })
	call := map[string]any{"command": "npm test"}
	for i := range 3 {
		if text := diffText(h.result(call, "FAIL tests/a.test.ts\n  Expected true, got false"+strings.Repeat("x", 5000)+itoaInt(i), true)); strings.Contains(text, "stuck-loop diff") {
			t.Errorf("run %d: no diff when stuck is disabled", i)
		}
	}
}

func longOutput(changed string) string {
	var b strings.Builder
	for i := range 120 {
		b.WriteString("error line " + itoaInt(i) + ": the build failed here with a long explanation\n")
	}
	return b.String() + "last: " + changed + "\n"
}

func regexpFind(t *testing.T, s, pattern string) string {
	t.Helper()
	m := lazyRE(pattern).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%q does not match %s", s, pattern)
	}
	return m[1]
}

func regexpQuote(s string) string { return strings.NewReplacer(`.`, `\.`).Replace(s) }

func TestStuckEvidenceReachesTheJudgeUnlessTheConfigTurnsItOff(t *testing.T) {
	for _, evidence := range []bool{true, false} {
		c := on(BackendTypeSafe)
		c.Stuck.Evidence = evidence
		j := stuckJudge(0.95, 0, 0.1)
		h := startHarness(t, harnessOpts{cfg: c, newJudge: func(Config, *Budget) (Judge, error) { return j, nil }})
		h.start()
		call := map[string]any{"command": "npm test"}
		for i := range 3 {
			h.result(call, "not ok 1 - alpha "+itoaInt(i)+"\n  error: boom\n", true)
		}
		calls := j.calls()
		if len(calls) != 1 {
			t.Fatalf("one stuck request, got %d", len(calls))
		}
		if _, has := calls[0].State["evidence"]; has != evidence {
			t.Errorf("evidence %v: state has it %v", evidence, has)
		}
	}
}

func (h *harness) subagentMessage(customType, content string) {
	h.t.Helper()
	h.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "custom", "customType": customType, "content": content}})
}

func (h *harness) wakeSteers() []sent {
	var out []sent
	for _, s := range h.steers() {
		if strings.Contains(s.Content, "subagent report") {
			out = append(out, s)
		}
	}
	return out
}

// twin: tests/extension.test.ts:684
func TestSubagentReportsSilentAppendByDefaultOneBatchedWakeForAReportThatNamesTrouble(t *testing.T) {
	answer := 0.0
	j := &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 3, Answers: map[string]Answer{"wake": noul(answer)}}, nil
	}}
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), newJudge: func(Config, *Budget) (Judge, error) { return j, nil }})
	h.start()
	h.say("user", "run the migration")
	failure := "Background tasks completed (1): **explorer**\n\n1. explorer\nResult: the migration failed with exit code 1\nParallel handoff: /tmp/handoff.md"
	progress := "Background task progress: **explorer** is still reading src/config.ts"
	completion := "Background tasks completed (1): **writer**\n\n1. writer\nResult: rewrote the parser; all 12 tests pass"
	settled := func() { h.host.Fire("agent_settled", nil) }

	// A progress line and a clean completion: read in code, no request, no wake, one trace line each.
	h.subagentMessage("subagent-incremental-child-notify", progress)
	h.subagentMessage("subagent-notify", completion)
	settled()
	if len(j.calls()) != 0 || len(h.messages()) != 0 {
		t.Fatalf("neither report needed the judge and nothing was sent: %d requests, %v", len(j.calls()), h.messages())
	}
	trace := h.traceText()
	mustMatch(t, trace, `subagent-incremental-child-notify · silent · appended silently`, "progress line")
	mustMatch(t, trace, `subagent-notify · silent · appended silently`, "completion")
	mustMatch(t, trace, `incremental progress notify`, "reason")
	mustMatch(t, trace, `no failure, blocker, or question for the agent`, "reason")
	if n := strings.Count(trace, "appended silently"); n != 2 {
		t.Errorf("one trace line per report: %d", n)
	}
	// The same reports are not triaged again on the next idle moment.
	settled()
	if len(j.calls()) != 0 || len(h.messages()) != 0 {
		t.Fatal("seen reports are skipped")
	}

	// A report that names a failure: the judge decides, and a high answer wakes the agent with a pointer, not a summary.
	answer = 0.95
	h.subagentMessage("subagent-notify", failure)
	settled()
	calls := j.calls()
	if len(calls) != 1 || calls[0].State["kind"] != "subagent-notify" {
		t.Fatalf("one request for the report that names trouble: %v", calls)
	}
	mustMatch(t, calls[0].State["report"].(string), `the migration failed with exit code 1`, "report digest")
	wakes := h.wakeSteers()
	if len(wakes) != 1 {
		t.Fatalf("the agent was woken: %v", h.messages())
	}
	mustMatch(t, wakes[0].Content, `^pi-warden: one subagent report needs you:`, "wake")
	mustMatch(t, wakes[0].Content, `explorer`, "wake names the report")
	mustNotContain(t, wakes[0].Content, "Parallel handoff", "the steer points at the report instead of repeating it")
	if wakes[0].Options["triggerTurn"] != true || wakes[0].Options["deliverAs"] != "followUp" {
		t.Errorf("an idle agent is woken, not merely informed: %v", wakes[0].Options)
	}

	// Inside the wake window a second report waits; the next window carries the whole batch as one steer.
	before := len(h.messages())
	h.subagentMessage("subagent-notify", strings.Replace(failure, "explorer", "tester", -1))
	settled()
	if len(j.calls()) != 2 || len(h.messages()) != before {
		t.Fatalf("it was still judged, but the wake window held it back: %d requests", len(j.calls()))
	}
	h.w.mu.Lock()
	s := h.w.stats
	h.w.mu.Unlock()
	if s.SubagentWoken != 1 || s.SubagentReports != 4 {
		t.Errorf("1/4 subagent reports woken: %d/%d", s.SubagentWoken, s.SubagentReports)
	}
	before = len(h.messages())
	h.host.Command("warden", "status")
	var status string
	for _, m := range h.messages()[before:] {
		if m.Type == statusType {
			status = m.Content
		}
	}
	mustMatch(t, status, `1/4 subagent reports woken`, "status counts the reports")
	h.w.mu.Lock()
	h.w.cfg.Subagent.CooldownMs = 0
	h.w.mu.Unlock()
	h.subagentMessage("subagent-notify", strings.Replace(failure, "explorer", "builder", -1))
	settled()
	if len(j.calls()) != 3 {
		t.Fatalf("requests %d", len(j.calls()))
	}
	wakes = h.wakeSteers()
	if len(wakes) != 2 {
		t.Fatalf("the waiting report and this one arrive as one wake: %v", wakes)
	}
	mustMatch(t, wakes[1].Content, `^pi-warden: 2 subagent reports need you:`, "batch")
	mustMatch(t, wakes[1].Content, `tester`, "batch")
	mustMatch(t, wakes[1].Content, `builder`, "batch")

	// A low answer stays quiet, and turning the section off ignores reports completely.
	answer = 0.2
	h.subagentMessage("subagent-notify", failure)
	settled()
	if len(h.wakeSteers()) != 2 {
		t.Error("a below-threshold report does not interrupt the user")
	}
	h.w.mu.Lock()
	h.w.cfg.Subagent.Enabled = false
	h.w.mu.Unlock()
	h.subagentMessage("subagent-notify", failure)
	settled()
	if len(j.calls()) != 4 || len(h.wakeSteers()) != 2 {
		t.Errorf("no triage request with the section off: %d", len(j.calls()))
	}
}

func TestASubagentCompletionWithNoTroubleWakesNobodyAndCostsNoRequest(t *testing.T) {
	j := wakeJudge(0.99)
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), newJudge: func(Config, *Budget) (Judge, error) { return j, nil }})
	h.start()
	h.subagentMessage("subagent-notify", subCompletion)
	h.host.Fire("agent_settled", nil)
	if len(h.steers()) != 0 || len(j.calls()) != 0 {
		t.Errorf("steers %v requests %d", h.steers(), len(j.calls()))
	}
}

func TestOnlyTheKnownSubagentNotifyTypesAreCollectedAndOnlyTheNewestEightWait(t *testing.T) {
	_, w := newWarden(Options{})
	w.collectSubagentReport(map[string]any{"role": "custom", "customType": "someone-elses", "content": "x"})
	w.collectSubagentReport(map[string]any{"role": "assistant", "customType": "subagent-notify", "content": "x"})
	w.collectSubagentReport(map[string]any{"role": "custom", "customType": "subagent-notify", "content": []any{"not a string"}})
	for i := range 12 {
		w.collectSubagentReport(map[string]any{"role": "custom", "customType": "subagent-notify", "content": "report " + itoaInt(i) + " failed"})
	}
	reports := NewReports(w.sup.subagentPending, map[string]bool{}, 8)
	if len(w.sup.subagentPending) != 8 || len(reports) != 8 || reports[0].Text != "report 4 failed" || reports[7].Text != "report 11 failed" {
		t.Errorf("pending %d: %+v", len(w.sup.subagentPending), reports)
	}
}

func TestInputIsHandledWhileARegisteredLaneIsBusyAndPassesOtherwise(t *testing.T) {
	ext, w := newWarden(Options{})
	busy := true
	w.sup.blockInput(func() bool { return false }, "never shown")
	w.sup.blockInput(func() bool { return busy }, "pi-warden is generating rules. Please wait...")
	passed, settles := 0, 0
	w.adaptor = userCountingAdaptor{users: &settles}
	w.sup.onInputPass(func(sdk.Context) { passed++ })
	h := &harness{t: t, ts: newFakeTypeSafe(t), home: t.TempDir()}
	t.Setenv("PIG_HOME", h.home)
	hasUI := true
	h.host = StartHost(t, ext, HostOptions{HasUI: &hasUI, Cwd: t.TempDir(), OnCall: h.onCall})
	h.start()
	raw := h.host.Fire("input", map[string]any{"text": "hello", "source": "interactive"})
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["action"] != "handled" || passed != 0 {
		t.Errorf("busy: %s passed=%d", raw, passed)
	}
	mustEqual(t, h.notices(), []string{"pi-warden is generating rules. Please wait..."}, "the user is told")
	if settles != 0 {
		t.Errorf("a swallowed input settles nothing: %d", settles)
	}
	busy = false
	raw = h.host.Fire("input", map[string]any{"text": "hello", "source": "interactive"})
	if s := string(raw); s != "" && s != "null" {
		t.Errorf("idle input passes: %s", s)
	}
	if passed != 1 || settles != 1 {
		t.Errorf("pass-through pieces run once and the user's reply settles the watched steers once: %d %d", passed, settles)
	}
}

func TestMessageAndSettledPiecesRunInRegistrationOrderAndTheFirstReplacementWins(t *testing.T) {
	var order []string
	ext, _ := newWarden(Options{setup: func(w *warden) {
		w.sup.onMessageStart(func(sdk.Context, map[string]any) (any, error) { order = append(order, "start1"); return nil, nil })
		w.sup.onMessageStart(func(sdk.Context, map[string]any) (any, error) { order = append(order, "start2"); return nil, nil })
		w.sup.onMessageUpdate(func(sdk.Context, map[string]any) (any, error) { order = append(order, "update"); return nil, nil })
		w.sup.onMessageEnd(func(sdk.Context, map[string]any) (any, error) { order = append(order, "end1"); return nil, nil })
		w.sup.onMessageEnd(func(sdk.Context, map[string]any) (any, error) {
			order = append(order, "end2")
			return map[string]any{"message": map[string]any{"role": "user", "content": "replaced"}}, nil
		})
		w.sup.onMessageEnd(func(sdk.Context, map[string]any) (any, error) { order = append(order, "end3"); return nil, nil })
		w.sup.onSettled(func(sdk.Context) { order = append(order, "settled2") })
	}})
	h := &harness{t: t, ts: newFakeTypeSafe(t), home: t.TempDir()}
	t.Setenv("PIG_HOME", h.home)
	hasUI := true
	h.host = StartHost(t, ext, HostOptions{HasUI: &hasUI, Cwd: t.TempDir(), OnCall: h.onCall})
	h.start()
	h.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "assistant"}})
	h.host.Fire("message_update", map[string]any{"message": map[string]any{"role": "assistant"}})
	raw := h.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "user", "content": "x"}})
	h.host.Fire("agent_settled", nil)
	mustEqual(t, order, []string{"start1", "start2", "update", "end1", "end2", "settled2"}, "order")
	mustMatch(t, string(raw), `"content":"replaced"`, "the replacement reaches the host")
}

func TestEventsWithoutPiecesAreNotSubscribedExceptTheOnesThisModuleNeeds(t *testing.T) {
	e := sdk.New("probe")
	(&warden{sup: newSupervision()}).registerSupervisionEvents(e)
	h := &harness{t: t, ts: newFakeTypeSafe(t), home: t.TempDir()}
	t.Setenv("PIG_HOME", h.home)
	hasUI := true
	h.host = StartHost(t, e, HostOptions{HasUI: &hasUI, Cwd: t.TempDir(), OnCall: h.onCall})
	for _, ev := range []string{"message_start", "message_update"} {
		if h.host.Registered(ev) {
			t.Errorf("%s has no piece, so the host is not asked per message or per token", ev)
		}
	}
	for _, ev := range []string{"message_end", "input", "agent_settled"} {
		if !h.host.Registered(ev) {
			t.Errorf("%s is needed by the subagent triage and the input registry", ev)
		}
	}
}

func TestTheStackedWardenSubscribesToTheStreamOnlyForTheRunawayGuard(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendNone)})
	for _, ev := range []string{"message_start", "message_update", "message_end", "input", "agent_settled"} {
		if !h.host.Registered(ev) {
			t.Errorf("%s: the runaway guard's pieces and the subagent triage are registered", ev)
		}
	}
}
