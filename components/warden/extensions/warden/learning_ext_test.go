package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Layer-1 twins of the upstream extension.test.ts cases for the hold feedback loop, the steer adaptation and the
// judge cooldown. The Go trace shows each entry's line only; the outcome notes are read from the entries' details.

func (h *harness) replyPrompt(text string) {
	h.say("user", text)
	h.prompt(text)
}

func (h *harness) traceDetails() string {
	var b strings.Builder
	for _, e := range h.w.trace.Entries() {
		b.WriteString(e.Line + "\n" + strings.Join(e.Details, "\n") + "\n")
	}
	return b.String()
}

func (h *harness) setModel(id string) {
	h.mu.Lock()
	h.model = map[string]any{"id": id, "provider": "test", "name": id}
	h.mu.Unlock()
}

// readHoldLog reads the session log at path (n records expected).
func readHoldLogFile(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// twin: tests/extension.test.ts:1358
func TestExtensionAdaptiveStepsAnIntentMismatchSteerTheModelIgnoresBecomeTraceOnlyAndUnmute(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	cfg := on(BackendTypeSafe)
	cfg.Rules.Enabled, cfg.Slop.Enabled, cfg.Security.Enabled = false, false, false
	cfg.Notices = false
	cfg.Action.FeedbackLog = false
	cfg.Action.IntentTraceOnly = "invisible"
	cfg.Steers = SteersConfig{Adaptive: true, MinSteers: 2, MinFollowed: 0.2, MaxDisputed: 0.4, RecheckEvery: 4, ProbeEvery: 2}
	h := startHarness(t, harnessOpts{cfg: cfg})
	command := "git push origin main"
	plan := "Let me first list what is in build/ before removing anything."
	h.say("user", "Verify the RPC endpoint end to end")
	args, _ := json.Marshal(map[string]any{"command": command})
	h.assistantCalls(plan, map[string]string{"id": "call-1", "name": "bash", "args": string(args)})
	intentSteers := func() int {
		n := 0
		for _, s := range h.steers() {
			if strings.Contains(s.Content, "what you said you were about to do") {
				n++
			}
		}
		return n
	}
	// run is one mismatching push; the agent's next two messages carry on without a course change. It returns
	// whether the steer was sent.
	run := func(id string) int {
		t.Helper()
		h.setModel(id)
		h.start()
		h.ts.set(map[string]float64{"irreversible": 0.1, "off_task": 0.1, "mutates": 0.9, "visible": 0.9, "intent_mismatch": 0.95, "should_proceed": 1.0})
		before := intentSteers()
		if r, held := blocked(h.toolCall("bash", "call-1", map[string]any{"command": command})); held {
			t.Fatalf("a mismatch never holds, muted or not: %s", r)
		}
		sent := intentSteers() - before
		for _, text := range []string{"Pushed.", "Continuing with the next step."} {
			h.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": text},
				map[string]any{"type": "toolCall", "id": "c", "name": "bash", "arguments": map[string]any{"command": "ls"}},
			}}})
		}
		return sent
	}
	if a, b := run("a"), run("a"); a != 1 || b != 1 {
		t.Fatalf("under minSteers every steer is sent: %d %d", a, b)
	}
	if got := run("b"); got != 1 {
		t.Fatalf("another model has its own counts: %d", got)
	}
	if got := run("a"); got != 1 {
		t.Fatalf("the first steer after muting is a probe: %d", got)
	}
	if got := run("a"); got != 0 {
		t.Fatalf("then trace-only: %d", got)
	}
	if !regexp.MustCompile(`steer trace-only · intent-mismatch · test/a`).MatchString(h.traceDetails()) {
		t.Fatalf("trace:\n%s", h.traceDetails())
	}
	h.setModel("a")
	if got := h.statusText(); !regexp.MustCompile(`trace-only per model: test/a: intent-mismatch \(\d+ steers, 0% followed, 0% disputed\)`).MatchString(got) {
		t.Fatalf("status:\n%s", got)
	}
	h.host.Command("warden", "unmute intent-mismatch")
	if n := h.notices(); !strings.Contains(n[len(n)-1], "Reset intent-mismatch for test/a") {
		t.Fatalf("notices %q", n)
	}
	if got := run("a"); got != 1 {
		t.Fatalf("after unmute the steer is sent again: %d", got)
	}
	if s := h.w.statsCopy(); s.OffPlan < 1 {
		t.Fatalf("a mismatch of the last run is counted: %+v", s)
	}
	h.host.Command("warden", "unmute intent-mismatch test/b")
	off := *cfg
	off.Steers.Adaptive = false
	if err := SaveConfig(ConfigPath(h.home), off); err != nil {
		t.Fatal(err)
	}
	h.w.mu.Lock()
	h.w.loaded = false
	h.w.mu.Unlock()
	h.host.Command("warden", "unmute intent-mismatch")
}

// twin: tests/extension.test.ts:1427 (the status line is the port's own; the Holds and Lifetime sentences are the
// original's)
func TestExtensionHoldFeedbackOfflineLabelsCallsTraceStatusAndLog(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "fix the bug")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", forcePush())); !held {
		t.Fatal("not held")
	}
	if got := h.statusText(); !strings.Contains(got, "Holds: 1 hold; 0 approved by you, 0 declined, 0 re-planned, 1 awaiting your reply; precision not yet measurable; 0 allowed") {
		t.Fatalf("status:\n%s", got)
	}
	h.replyPrompt("yes, go ahead")
	if r, held := blocked(h.toolCall("bash", "c2", forcePush())); held {
		t.Fatalf("the reply releases the hold: %s", r)
	}
	got := h.statusText()
	m := regexp.MustCompile(`Holds: (.*?)\. Log: (.+?\.jsonl)\.`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("status:\n%s", got)
	}
	if m[1] != "1 hold; 1 approved by you, 0 declined, 0 re-planned, 0 awaiting your reply; precision 0% over 1 label; 1 allowed (0 regretted by you, 0 accepted)" {
		t.Fatalf("holds: %s", m[1])
	}
	logPath := m[2]
	if !strings.HasPrefix(logPath, filepath.Join(h.dataDir(), "holds")) {
		t.Fatalf("log path %s", logPath)
	}
	if !strings.Contains(h.traceDetails(), "outcome: approved by the user (released on retry); the hold was a false positive") {
		t.Fatalf("the hold's trace entry carries its outcome:\n%s", h.traceDetails())
	}
	// A hold nobody approves: the user redirects, the agent does something else, and the prompt after that lands the label.
	h.replyPrompt("fix the bug")
	if _, held := blocked(h.toolCall("bash", "c3", map[string]any{"command": "git reset --hard HEAD~3"})); !held {
		t.Fatal("reset not held")
	}
	h.replyPrompt("leave it, run the tests instead")
	if r, held := blocked(h.toolCall("bash", "c4", map[string]any{"command": "npm test"})); held {
		t.Fatalf("npm test held: %s", r)
	}
	h.replyPrompt("thanks, now update the docs")
	if got := h.statusText(); !strings.Contains(got, "Holds: 2 holds; 1 approved by you, 0 declined, 1 re-planned, 0 awaiting your reply; precision 50% over 2 labels; 2 allowed (0 regretted by you, 2 accepted)") {
		t.Fatalf("status:\n%s", got)
	}
	// An allowed call the next message regrets: offline, the stop-word heuristic labels it.
	if r, held := blocked(h.toolCall("bash", "c5", map[string]any{"command": "rm -rf dist"})); held {
		t.Fatalf("rm -rf dist held: %s", r)
	}
	h.replyPrompt("wait, don't delete dist, I still need it")
	if got := h.statusText(); !strings.Contains(got, "3 allowed (1 regretted by you, 2 accepted)") {
		t.Fatalf("status:\n%s", got)
	}
	if !strings.Contains(h.traceDetails(), "outcome: the user's next message regrets this call; it should have been held") {
		t.Fatalf("trace:\n%s", h.traceDetails())
	}
	h.w.learnFlush()
	lines := readHoldLogFile(t, logPath)
	type row struct {
		held         bool
		outcome, via string
	}
	var got5 []row
	for _, l := range lines {
		held, _ := l["held"].(bool)
		o, _ := l["outcome"].(string)
		v, _ := l["outcomeVia"].(string)
		got5 = append(got5, row{held, o, v})
	}
	want := []row{{true, "approved", "retry"}, {false, "accepted", "text"}, {true, "replanned", "next prompt"}, {false, "accepted", "text"}, {false, "regretted", "text"}}
	if len(got5) != 5 {
		t.Fatalf("log rows %v", got5)
	}
	for i := range want {
		if got5[i] != want[i] {
			t.Fatalf("row %d: %v, want %v", i, got5[i], want[i])
		}
	}
	pat, _ := json.Marshal(lines[0]["patterns"])
	if string(pat) != `["git-force-push"]` || lines[0]["source"] != "pattern" {
		t.Fatalf("first record %v", lines[0])
	}
	text, _ := json.Marshal(lines)
	for _, secret := range []string{"origin main", "HEAD~3", "dist"} {
		if strings.Contains(string(text), secret) {
			t.Fatalf("the log never carries command text, but has %q", secret)
		}
	}
	if h.ts.count() != 0 {
		t.Fatalf("offline: %d network calls", h.ts.count())
	}
	// The log can be turned off; the counts stay.
	off := *offlineWarden()
	off.Action.FeedbackLog = false
	if err := SaveConfig(ConfigPath(h.home), off); err != nil {
		t.Fatal(err)
	}
	h.w.mu.Lock()
	h.w.loaded = false
	h.w.mu.Unlock()
	h.start()
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	h.say("user", "fix the bug")
	if _, held := blocked(h.toolCall("bash", "c6", map[string]any{"command": "git push --force"})); !held {
		t.Fatal("not held")
	}
	got = h.statusText()
	if !strings.Contains(got, "Holds: 1 hold; 0 approved by you, 0 declined, 0 re-planned, 1 awaiting your reply; precision not yet measurable; 0 allowed (0 regretted by you, 0 accepted).\n") || !strings.Contains(got, "Lifetime here:") || strings.Contains(got, "Log:") {
		t.Fatalf("status:\n%s", got)
	}
	h.replyPrompt("hm")
	h.replyPrompt("still thinking") // the hold is re-planned: a label lands, and still no log is written
	h.w.learnFlush()
	if _, err := os.Stat(logPath); err == nil {
		t.Fatal("nothing is written with feedbackLog off")
	}
}

// twin: tests/extension.test.ts:1482
func TestExtensionHoldOutcomeKnownAtRecordTimeIsPersistedToSQLite(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	cfg := offlineWarden()
	cfg.Mode = "confirm"
	h := startHarness(t, harnessOpts{cfg: cfg})
	h.say("user", "deploy the change")
	h.start()
	h.confirm = false
	if _, held := blocked(h.toolCall("bash", "c1", forcePush())); !held {
		t.Fatal("first call held via dialog")
	}
	h.w.learnFlush()
	has := func(outcome string) bool {
		for _, r := range h.holdRows() {
			if r["outcome"] == outcome {
				return true
			}
		}
		return false
	}
	if !has("declined") {
		t.Fatalf("declined outcome persisted (known at record time); rows %v", h.holdRows())
	}
	h.confirm = true
	if r, held := blocked(h.toolCall("bash", "c2", forcePush())); held {
		t.Fatalf("approved via dialog: %s", r)
	}
	if !has("approved") {
		t.Fatalf("approved outcome persisted (known at record time); rows %v", h.holdRows())
	}
}

// regretJudge answers like Jev: scores for the noul questions, and the choice questions by name.
func regretJudge(noulAnswers map[string]float64, choices map[string]string) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		answers := map[string]Answer{}
		for id, q := range r.Questions {
			switch q.Type {
			case "noul":
				v, ok := noulAnswers[id]
				if !ok {
					v = 0.05
				}
				answers[id] = noul(v)
			case "choice":
				c, ok := choices[id]
				if !ok {
					c = "expected_step"
				}
				answers[id] = Answer{Type: "choice", Choice: c, Confidence: 0.9}
			}
		}
		return Evaluation{Model: "jev-test", ElapsedMs: 5, Answers: answers}, nil
	}}
}

// twin: tests/extension.test.ts:1504 (the trace-detail lines about the regret belong to the trace module)
func TestExtensionHoldFeedbackWithJevTheRegretQuestionRidesTheFirstRequestAfterTheReply(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	var judge *recordingJudge
	cfg := on(BackendTypeSafe)
	cfg.Rules.Enabled = false
	h := startHarness(t, harnessOpts{cfg: cfg, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	judge = regretJudge(map[string]float64{"irreversible": 0.1, "off_task": 0.1}, nil)
	h.say("user", "clean up the build")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "rm -rf build"})); held {
		t.Fatal("rm -rf build held")
	}
	if _, held := blocked(h.toolCall("write", "c2", map[string]any{"path": "notes.txt", "content": "cleaned"})); held {
		t.Fatal("write held")
	}
	last := func() Request { c := judge.calls(); return c[len(c)-1] }
	if _, ok := last().State["previous_actions"]; ok {
		t.Fatal("same prompt: nothing to regret yet")
	}
	h.replyPrompt("wait, stop, I still needed build/")
	judge.handler = regretJudge(map[string]float64{"irreversible": 0.1, "off_task": 0.1, "regretted": 0.92}, map[string]string{"regret_target": "a1"}).handler
	if _, held := blocked(h.toolCall("bash", "c3", map[string]any{"command": "npm test"})); held {
		t.Fatal("npm test held")
	}
	req := last()
	prev, _ := json.Marshal(req.State["previous_actions"])
	if string(prev) != `[{"id":"a1","tool":"bash","command":"rm -rf build"},{"id":"a2","tool":"write","path":"notes.txt"}]` {
		t.Fatalf("previous_actions %s", prev)
	}
	if _, ok := req.Questions["regretted"]; !ok {
		t.Fatal("no regretted question")
	}
	if _, ok := req.Questions["regret_target"]; !ok {
		t.Fatal("no regret_target question")
	}
	h.toolCall("bash", "c4", map[string]any{"command": "npm run lint"})
	if _, ok := last().State["previous_actions"]; ok {
		t.Fatal("asked once per prompt")
	}
	if got := h.statusText(); !strings.Contains(got, "Holds: 0 holds; precision not yet measurable; 4 allowed (1 regretted by you, 1 accepted)") {
		t.Fatalf("status:\n%s", got)
	}
	details := h.traceDetails()
	if !strings.Contains(details, "outcome: the user's next message regrets this call (0.92); it should have been held") ||
		!strings.Contains(details, "outcome: the user's next message does not regret this call (0.92)") {
		t.Fatalf("trace:\n%s", details)
	}
	// The agent only replies to the next prompt: no request carries the question, so the heuristic reads the prompt
	// at the end of the run... here the next prompt arrives with the judge cooled down.
	h.replyPrompt("undo that")
	h.host.Fire("agent_end", map[string]any{"messages": []any{}})
	if got := h.statusText(); !strings.Contains(got, "4 allowed (2 regretted by you, 2 accepted)") {
		t.Fatalf("status:\n%s", got)
	}
}

// twin: tests/extension.test.ts:1534
func TestExtensionHoldFeedbackInConfirmModeTheDialogLabelsAtOnce(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	cfg := offlineWarden()
	cfg.Mode = "confirm"
	h := startHarness(t, harnessOpts{cfg: cfg})
	h.say("user", "ship")
	h.start()
	h.confirm = false
	h.toolCall("bash", "c1", map[string]any{"command": "git push --force"})
	h.confirm = true
	if r, held := blocked(h.toolCall("bash", "c2", map[string]any{"command": "git push --force"})); held {
		t.Fatalf("held: %s", r)
	}
	if got := h.statusText(); !strings.Contains(got, "Holds: 2 holds; 1 approved by you, 1 declined, 0 re-planned, 0 awaiting your reply; precision 50% over 2 labels") {
		t.Fatalf("status:\n%s", got)
	}
	details := h.traceDetails()
	if !strings.Contains(details, "outcome: declined by the user in the confirm dialog; the hold stood") || !strings.Contains(details, "outcome: approved by the user (confirm dialog); the hold was a false positive") {
		t.Fatalf("trace:\n%s", details)
	}
}

// ---------------------------------------------------------------------------
// Judge cooldown.

func cooldownHarness(t *testing.T, judge JudgeConfig) (*harness, *time.Time) {
	t.Helper()
	t.Cleanup(CloseLearningDatabases)
	cfg := on(BackendTypeSafe)
	cfg.Notices = true
	cfg.Rules.Enabled = false
	cfg.TimeoutMs = 50
	if judge.FailuresBeforeCooldown != 0 {
		cfg.Judge.FailuresBeforeCooldown = judge.FailuresBeforeCooldown
	}
	if judge.CooldownMs != 0 {
		cfg.Judge.CooldownMs = judge.CooldownMs
	}
	h := startHarness(t, harnessOpts{cfg: cfg})
	now := time.Now()
	h.w.opts.Now = func() time.Time { return now }
	h.say("user", "run the checks")
	h.start()
	return h, &now
}

func (h *harness) pausedNotices() []string { return h.noticesMatching("judgments paused") }
func (h *harness) noticesMatching(sub string) []string {
	var out []string
	for _, n := range h.notices() {
		if strings.Contains(n, sub) {
			out = append(out, n)
		}
	}
	return out
}
func (h *harness) setStatus(code int) { h.ts.mu.Lock(); h.ts.status = code; h.ts.mu.Unlock() }

// twin: tests/extension.test.ts:4114 (a 503 stands for the hung backend: the port's judge has no retry, D7)
func TestExtensionJudgeCooldownThreeFailuresPauseTheJudge(t *testing.T) {
	h, _ := cooldownHarness(t, JudgeConfig{})
	h.setStatus(503)
	for i, command := range []string{"npm test", "npm run lint", "npm run build"} {
		if r, held := blocked(h.toolCall("bash", "c"+itoaInt(i), map[string]any{"command": command})); held {
			t.Fatalf("each failure still fails open: %s", r)
		}
	}
	asked := h.ts.count()
	paused := h.pausedNotices()
	if len(paused) != 1 || !strings.Contains(paused[0], "network failure") || !strings.Contains(paused[0], "60s") {
		t.Fatalf("one notice when the cooldown starts, naming the kind and the duration: %q", paused)
	}
	h.toolCall("bash", "c9", map[string]any{"command": "npm run typecheck"})
	if h.ts.count() != asked {
		t.Fatalf("no request is made while cooled down: %d -> %d", asked, h.ts.count())
	}
	if len(h.pausedNotices()) != 1 {
		t.Fatal("nothing is said per action while cooled down")
	}
}

// twin: tests/extension.test.ts:4128
func TestExtensionJudgeCooldownASuccessBetweenFailuresResetsTheCounter(t *testing.T) {
	h, _ := cooldownHarness(t, JudgeConfig{})
	h.setStatus(503)
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	h.toolCall("bash", "c2", map[string]any{"command": "npm run lint"})
	h.setStatus(200)
	h.toolCall("bash", "c3", map[string]any{"command": "npm run build"})
	h.setStatus(503)
	h.toolCall("bash", "c4", map[string]any{"command": "npm run typecheck"})
	h.toolCall("bash", "c5", map[string]any{"command": "npm run format"})
	if len(h.pausedNotices()) != 0 {
		t.Fatal("two failures after a success are not three in a row")
	}
	asked := h.ts.count()
	h.toolCall("bash", "c6", map[string]any{"command": "npm run docs"})
	if h.ts.count() <= asked {
		t.Fatal("the judge is still asked")
	}
}

// twin: tests/extension.test.ts:4144
func TestExtensionJudgeCooldownOneAuthFailurePausesAtOnceAndNamesTheRemedy(t *testing.T) {
	h, _ := cooldownHarness(t, JudgeConfig{})
	h.setStatus(401)
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	paused := h.pausedNotices()
	if len(paused) != 1 || !strings.Contains(paused[0], "auth failure") || !strings.Contains(paused[0], "TYPESAFE_API_KEY") || !strings.Contains(paused[0], "/warden enable") {
		t.Fatalf("notices %q", paused)
	}
	if strings.Contains(paused[0], tsErrorMarker) {
		t.Fatal("upstream error bodies stay out of the UI")
	}
	asked := h.ts.count()
	h.toolCall("bash", "c2", map[string]any{"command": "npm run lint"})
	if h.ts.count() != asked {
		t.Fatal("a paused judge is left alone")
	}
}

// twin: tests/extension.test.ts:4158
func TestExtensionJudgeCooldownAfterTheWindowTheJudgeIsAskedAgainAndRecoveryIsAnnouncedOnce(t *testing.T) {
	h, now := cooldownHarness(t, JudgeConfig{CooldownMs: 60_000, FailuresBeforeCooldown: 1})
	h.setStatus(503)
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	if len(h.pausedNotices()) != 1 {
		t.Fatalf("notices %q", h.notices())
	}
	*now = now.Add(60_001 * time.Millisecond)
	h.setStatus(200)
	asked := h.ts.count()
	h.toolCall("bash", "c2", map[string]any{"command": "npm run lint"})
	if h.ts.count() <= asked {
		t.Fatal("the first action after the window asks the judge")
	}
	if r := h.noticesMatching("judgments resumed"); len(r) != 1 {
		t.Fatalf("one recovery notice: %q", r)
	}
	for _, c := range h.host.CallsTo("ui.notify") {
		if m, _ := c.Args["message"].(string); strings.Contains(m, "judgments resumed") && c.Args["level"] != "info" {
			t.Fatalf("recovery is info: %v", c.Args)
		}
	}
	h.toolCall("bash", "c3", map[string]any{"command": "npm run build"})
	if len(h.noticesMatching("judgments resumed")) != 1 {
		t.Fatal("later successes stay quiet")
	}
}

// twin: tests/extension.test.ts:4175
func TestExtensionJudgeCooldownAFailedProbeReopensTheWindowWithoutASecondNotice(t *testing.T) {
	h, now := cooldownHarness(t, JudgeConfig{CooldownMs: 60_000, FailuresBeforeCooldown: 1})
	h.setStatus(503)
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	*now = now.Add(60_001 * time.Millisecond)
	probed := h.ts.count()
	h.toolCall("bash", "c2", map[string]any{"command": "npm run lint"})
	if h.ts.count() <= probed {
		t.Fatal("the probe after the window asks the judge")
	}
	asked := h.ts.count()
	h.toolCall("bash", "c3", map[string]any{"command": "npm run build"})
	if h.ts.count() != asked {
		t.Fatal("the window is open again")
	}
	if len(h.pausedNotices()) != 1 {
		t.Fatal("the user was already told")
	}
}

// twin: tests/extension.test.ts:4190 (the guards that reach the backend in the port are the action guard and the
// rules guard; slop, security and the context filter are other lanes and read the same cooldown through judgeFor)
func TestExtensionJudgeCooldownWhileActiveNoGuardSendsAnythingAndStatusCountsSkips(t *testing.T) {
	h, _ := cooldownHarness(t, JudgeConfig{FailuresBeforeCooldown: 1})
	h.replyPrompt("explain the bug")
	h.setStatus(503)
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	if len(h.pausedNotices()) != 1 {
		t.Fatal("not paused")
	}
	asked := h.ts.count()
	h.toolCall("write", "c2", map[string]any{"path": "src/a.ts", "content": "export const a = 1;\n"})
	h.toolResult("read", map[string]any{}, strings.Repeat("safe operational output\n", 1000), false)
	h.host.Fire("agent_end", map[string]any{"messages": []any{}})
	if h.ts.count() != asked {
		t.Fatalf("no guard reached the transport: %d -> %d", asked, h.ts.count())
	}
	if got := h.statusText(); !regexp.MustCompile(`[1-9]\d* checks without Jev during a judge cooldown`).MatchString(got) {
		t.Fatalf("status counts the skips:\n%s", got)
	}
}

// twin: tests/extension.test.ts:4207
func TestExtensionJudgeCooldownANewSessionTrustsTheJudgeAgain(t *testing.T) {
	h, _ := cooldownHarness(t, JudgeConfig{FailuresBeforeCooldown: 1})
	h.setStatus(503)
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	h.start()
	h.setStatus(200)
	asked := h.ts.count()
	h.toolCall("bash", "c2", map[string]any{"command": "npm run lint"})
	if h.ts.count() <= asked {
		t.Fatal("the judge is asked again")
	}
}
