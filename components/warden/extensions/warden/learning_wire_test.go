package warden

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Layer-1 wiring of the hold feedback loop, the learning store, the steer adaptation and the judge cooldown through
// the fake host (extension.test.ts: hold feedback :1430-1545, adaptive steers :1358, cooldown).

func offlineWarden() *Config {
	c := DefaultConfig()
	c.Enabled = true
	c.Rules.Enabled = false
	return &c
}

func (h *harness) dataDir() string { return filepath.Join(h.home, "pigpen-warden") }

// holdLogLines flushes the background writes and reads the session's hold log.
func (h *harness) holdLogLines() []map[string]any {
	h.t.Helper()
	h.w.learnFlush()
	entries, err := os.ReadDir(filepath.Join(h.dataDir(), "holds"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		h.t.Fatalf("no hold log: %v", err)
	}
	if len(entries) != 1 {
		h.t.Fatalf("hold logs: %v", entries)
	}
	raw, _ := os.ReadFile(filepath.Join(h.dataDir(), "holds", entries[0].Name()))
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			h.t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// holdRows reads the learning database rows of the session's project.
func (h *harness) holdRows() []Row {
	h.t.Helper()
	h.w.learnFlush()
	rows, err := NewLearning(LearningPath(h.dataDir(), h.w.opts.Getenv), nil).QueryHoldsForProject(h.cwd, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return rows
}

func (h *harness) prompt(text string) {
	h.host.Fire("before_agent_start", map[string]any{"prompt": text, "systemPromptOptions": map[string]any{}})
}

func (h *harness) statusText() string {
	h.t.Helper()
	h.host.Command("warden", "")
	m := h.messages()
	return m[len(m)-1].Content
}

func forcePush() map[string]any { return map[string]any{"command": "git push --force origin main"} }

func TestWiringHeldCallIsLoggedAndStoredAndReplannedAfterTwoPrompts(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "push my branch")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", forcePush())); !held {
		t.Fatal("not held")
	}
	lines := h.holdLogLines()
	if len(lines) != 0 {
		// The log is written when a label lands, not at record time: nothing was labelled yet.
		t.Fatalf("log %v", lines)
	}
	h.prompt("hm")
	h.prompt("another prompt")
	lines = h.holdLogLines()
	if len(lines) != 1 || lines[0]["outcome"] != "replanned" || lines[0]["outcomeVia"] != "next prompt" || lines[0]["held"] != true || lines[0]["tool"] != "bash" {
		t.Fatalf("log %v", lines)
	}
	if _, ok := lines[0]["callExcerpt"]; !ok || strings.Contains(toJSON(lines[0]), "origin main") {
		t.Fatalf("the log carries the tool, never the command: %v", lines[0])
	}
	rows := h.holdRows()
	if len(rows) != 1 || rows[0]["outcome"] != "replanned" || rows[0]["command_preview"] != "git push --force origin main" || rows[0]["held"] != int64(1) {
		t.Fatalf("rows %v", rows)
	}
	var amended bool
	for _, e := range h.w.trace.Entries() {
		for _, d := range e.Details {
			if strings.Contains(d, "outcome: never approved after the user replied; the agent re-planned, the hold stood") {
				amended = true
			}
		}
	}
	if !amended {
		t.Fatal("the trace entry of the hold says what became of it")
	}
	if got := h.statusText(); !strings.Contains(got, "Holds: 1 hold; 0 approved by you, 0 declined, 1 re-planned, 0 awaiting your reply; precision 100% over 1 label; 0 allowed") ||
		!strings.Contains(got, "Lifetime here: 1 hold, 1 labeled, 1 stood (1/1), 0 allowed (0 accepted, 0 regretted).") {
		t.Fatalf("status:\n%s", got)
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestWiringApprovedRetryLabelsTheHoldFalsePositive(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.say("user", "push my branch")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", forcePush())); !held {
		t.Fatal("not held")
	}
	h.say("assistant", "That push is a force push; do you want it?")
	h.say("user", "yes, force push it, I own that branch")
	h.ts.set(map[string]float64{"irreversible": 0.95, "approved": 0.97})
	if r, held := blocked(h.toolCall("bash", "c2", forcePush())); held {
		t.Fatalf("held again: %s", r)
	}
	lines := h.holdLogLines()
	if len(lines) < 1 || lines[0]["outcome"] != "approved" || lines[0]["outcomeVia"] != "retry" {
		t.Fatalf("log %v", lines)
	}
	if rows := h.holdRows(); len(rows) < 1 || rows[0]["outcome"] != "approved" {
		t.Fatalf("rows %v", rows)
	}
}

func TestWiringConfirmDialogLabelsAtOnce(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	cfg := offlineWarden()
	cfg.Mode = "confirm"
	for _, allow := range []bool{true, false} {
		h := startHarness(t, harnessOpts{cfg: cfg})
		h.confirm = allow
		h.say("user", "push my branch")
		h.start()
		h.toolCall("bash", "c1", forcePush())
		lines := h.holdLogLines()
		want := "declined"
		if allow {
			want = "approved"
		}
		if len(lines) != 1 || lines[0]["outcome"] != want || lines[0]["outcomeVia"] != "dialog" || lines[0]["mode"] != "confirm" {
			t.Fatalf("allow=%v log %v", allow, lines)
		}
		if rows := h.holdRows(); len(rows) != 1 || rows[0]["outcome"] != want {
			t.Fatalf("allow=%v rows %v", allow, rows)
		}
		CloseLearningDatabases()
	}
}

func TestWiringDenyRuleIsDeclinedViaDeny(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	cfg := offlineWarden()
	cfg.Action.CommandDenyRules = []CommandRule{{ID: "no-deploy", Pattern: `^make deploy`, Severity: "deny", Message: "never deploy from here"}}
	h := startHarness(t, harnessOpts{cfg: cfg})
	h.say("user", "ship it")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "make deploy"})); !held {
		t.Fatal("not blocked")
	}
	lines := h.holdLogLines()
	if len(lines) != 1 || lines[0]["outcome"] != "declined" || lines[0]["outcomeVia"] != "deny" {
		t.Fatalf("log %v", lines)
	}
}

func TestWiringOfflineRegretHeuristicLabelsTheAllowedCalls(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "run the tests")
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	h.toolCall("bash", "c2", map[string]any{"command": "npm run build"})
	s := h.w.learn.ledger.Snapshot()
	if s.Allowed != 2 {
		t.Fatalf("allowed calls are recorded: %+v", s)
	}
	h.prompt("stop, undo that")
	s = h.w.learn.ledger.Snapshot()
	if s.Regretted != 1 || s.Accepted != 1 {
		t.Fatalf("the latest call is the miss, the other is accepted: %+v", s)
	}
	lines := h.holdLogLines()
	if len(lines) != 2 || lines[0]["outcome"] != "accepted" || lines[1]["outcome"] != "regretted" || lines[1]["outcomeVia"] != "text" {
		t.Fatalf("log %v", lines)
	}
	rows := h.holdRows()
	if len(rows) != 2 || rows[1]["outcome"] != "regretted" || rows[1]["held"] != int64(0) {
		t.Fatalf("rows %v", rows)
	}
	if got := h.statusText(); !strings.Contains(got, "Holds: 0 holds; precision not yet measurable; 2 allowed (1 regretted by you, 1 accepted)") {
		t.Fatalf("status:\n%s", got)
	}
	// A reply that does not regret accepts every candidate.
	h2 := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h2.say("user", "run the tests")
	h2.start()
	h2.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	h2.prompt("looks good, now add docs")
	if s := h2.w.learn.ledger.Snapshot(); s.Accepted != 1 || s.Regretted != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestWiringSessionStartResetsTheLedgerAndOpensANewLog(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	if h.w.learn.ledger.Snapshot().Allowed != 1 {
		t.Fatal("not recorded")
	}
	h.start()
	if len(h.w.learn.ledger.Records()) != 0 {
		t.Fatal("a new session starts with an empty ledger")
	}
}

func TestWiringLearningOffAnswersLikeTheNoopDatabase(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden(), env: map[string]string{learningDBEnv: t.TempDir()}})
	h.say("user", "push my branch")
	h.start()
	// The database path is a directory: it cannot be opened, learning stays off, the guard carries on.
	if _, held := blocked(h.toolCall("bash", "c1", forcePush())); !held {
		t.Fatal("a broken learning database never changes a verdict")
	}
	h.w.learnFlush()
	if got := h.statusText(); !strings.Contains(got, "Holds: 1 hold") || !strings.Contains(got, "Learning warnings: pi-warden: could not open ") {
		t.Fatalf("status:\n%s", got)
	}
}

func TestWiringRecommendAndUnmuteCommands(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.host.Command("warden", "recommend")
	if n := h.notices(); len(n) == 0 || n[len(n)-1] != "pi-warden learning recommendations:\nNo recommendations yet. Need more hold data (current: 0 holds)." {
		t.Fatalf("notices %q", n)
	}
	h.host.Command("warden", "unmute")
	n := h.notices()
	if want := "Usage: /warden unmute <kind> [model]. Kinds: intent-mismatch, off-task, should-proceed, warn-headless, large-output, slop, rules, sensitive-path, security-write, stuck, repeat, prose, conscience, loops."; n[len(n)-1] != want {
		t.Fatalf("usage %q", n[len(n)-1])
	}
	h.host.Command("warden", "unmute done")
	if n := h.notices(); !strings.HasPrefix(n[len(n)-1], "No counts for done on prov/mod; nothing to reset.") {
		t.Fatalf("notices %q", n)
	}
	cfg := DefaultConfig().Steers
	for range 30 {
		h.w.adaptor.observe("prov/mod", SteerKindRules, SteerOutcome{}, cfg)
	}
	if got := h.statusText(); !strings.Contains(got, "Adaptive steers, trace-only per model: prov/mod: rules (30 steers, 0% followed, 0% disputed). /warden unmute <kind> resets a pair.") {
		t.Fatalf("status:\n%s", got)
	}
	h.host.Command("warden", "unmute rules")
	n = h.notices()
	if n[len(n)-1] != "Reset rules for prov/mod: its steers are sent again and counted from zero." {
		t.Fatalf("notices %q", n)
	}
	h.host.Command("warden", "unmute rules other/model")
	n = h.notices()
	if n[len(n)-1] != "No counts for rules on other/model; nothing to reset." {
		t.Fatalf("notices %q", n)
	}
	if _, err := os.Stat(filepath.Join(h.dataDir(), "steer-stats.json")); err != nil {
		t.Fatalf("the counts are kept across sessions: %v", err)
	}
}

func TestWiringRecommendReadsTheHistory(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.w.learnFlush() // the session start creates the schema in the background
	store := NewLearning(LearningPath(h.dataDir(), h.w.opts.Getenv), nil)
	for range 25 {
		id, err := store.RecordHold(baseHold(h.cwd, func(r *HoldRecord) {
			r.Level = "confirm"
			r.Scores = HoldScores{Irreversible: 0.9, Reasons: []string{"destructive: git force push"}}
			r.Reasons = []string{"destructive: git force push"}
			r.AgentReason = "irreversible action detected"
		}))
		if err != nil {
			t.Fatal(err)
		}
		store.RecordOutcome(id, "approved")
	}
	h.host.Command("warden", "recommend")
	n := h.notices()
	got := n[len(n)-1]
	for _, want := range []string{
		"pi-warden learning recommendations:\n\nThreshold & pattern recommendations:",
		"  [high] Pattern 'destructive' has a 100% false positive rate; consider adding it to exemptRules or raising its threshold",
		"Effectiveness by type:\n  irreversible: 100% (25/25)",
		"Top performing patterns:\n  irreversible: 100% effectiveness (25 samples)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWiringMessageEndSettlesWatchedSteers(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.w.adaptor.add("prov/mod", []SteerKind{SteerKindRules}, SteerSubject{Path: "src/a.go"}, false)
	h.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "toolCall", "id": "1", "name": "edit", "arguments": map[string]any{"path": "src/a.go"}},
	}}})
	p, ok := h.w.adaptor.pair("prov/mod", SteerKindRules)
	if !ok || p.Sent != 1 || p.Followed != 1 {
		t.Fatalf("the edit of the named file follows the note: %+v %v", p, ok)
	}
	// A user message does not settle through message_end.
	h.w.adaptor.add("prov/mod", []SteerKind{SteerKindRules}, SteerSubject{Path: "src/a.go"}, false)
	h.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "user", "content": "hi"}})
	if p, _ := h.w.adaptor.pair("prov/mod", SteerKindRules); p.Sent != 1 {
		t.Fatalf("%+v", p)
	}
}

// erroringJudge answers every request with err.
type erroringJudge struct {
	err   error
	calls int
}

func (j *erroringJudge) Evaluate(_ context.Context, _ Request) (Evaluation, error) {
	j.calls++
	return Evaluation{}, j.err
}

func TestWiringJudgeCooldownPausesAfterFailuresAndTellsTheUserOnce(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	judge := &erroringJudge{err: &IntegrationError{Code: "timeout", Message: "TypeSafe request timed out"}}
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "run the tests")
	h.start()
	for i := range 3 {
		h.toolCall("bash", "c"+itoaInt(i), map[string]any{"command": "npm test"})
	}
	if judge.calls != 3 {
		t.Fatalf("judged %d times before the cooldown", judge.calls)
	}
	var paused []string
	for _, n := range h.notices() {
		if strings.Contains(n, "judgments paused") {
			paused = append(paused, n)
		}
	}
	if len(paused) != 1 || paused[0] != "warden: judgments paused for 60s after a timeout failure; pattern checks run alone until then." {
		t.Fatalf("notices %q", paused)
	}
	h.toolCall("bash", "c9", map[string]any{"command": "npm test"})
	if judge.calls != 3 {
		t.Fatalf("a paused judge is left alone: %d calls", judge.calls)
	}
	if s := h.w.statsCopy(); s.CooldownSkips == 0 {
		t.Fatal("skips are counted")
	}
	// A new session trusts the judge again.
	h.start()
	h.toolCall("bash", "c10", map[string]any{"command": "npm test"})
	if judge.calls != 4 {
		t.Fatalf("the judge is asked again: %d", judge.calls)
	}
}

func TestWiringCooldownIgnoresBudgetAndValidationFailures(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	judge := &erroringJudge{err: &IntegrationError{Code: "budget", Message: "limit"}}
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "x")
	h.start()
	for i := range 5 {
		h.toolCall("bash", "c"+itoaInt(i), map[string]any{"command": "npm test"})
	}
	if judge.calls != 5 || h.w.learn.cooldown.Active() {
		t.Fatalf("budget failures say nothing about the backend: %d calls, active %v", judge.calls, h.w.learn.cooldown.Active())
	}
}

func TestWiringAWardenThatIsOffTouchesNoDatabaseAndOnceOnItCreatesIt(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	off := DefaultConfig() // off until /warden enable
	h := startHarness(t, harnessOpts{cfg: &off})
	h.say("user", "x")
	h.start()
	h.w.learnFlush()
	if _, err := os.Stat(filepath.Join(h.dataDir(), "holds.db")); err == nil {
		t.Fatal("an off warden created the learning database")
	}
	h.host.Command("warden", "enable offline")
	h.start()
	h.w.learnFlush()
	if _, err := os.Stat(filepath.Join(h.dataDir(), "holds.db")); err != nil {
		t.Fatalf("the session start of an on warden creates it: %v", err)
	}
}

func TestWiringTheLearningRowCarriesARedactedTaskAndThePathAsPreview(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "deploy with https://user:secret123@github.com/repo.git please")
	h.start()
	h.toolCall("write", "w1", map[string]any{"path": "notes.txt", "content": "x"})
	h.w.learnFlush()
	store := NewLearning(LearningPath(h.dataDir(), h.w.opts.Getenv), nil)
	rows, err := store.open().all("SELECT task, command_preview, tool FROM holds")
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	task, _ := rows[0]["task"].(string)
	if strings.Contains(task, "secret123") || !strings.Contains(task, "deploy with") {
		t.Fatalf("the task is redacted: %q", task)
	}
	if rows[0]["command_preview"] != "notes.txt" || rows[0]["tool"] != "write" {
		t.Fatalf("a write is previewed by its path: %v", rows[0])
	}
}

func TestWiringAReadOnlyCallLeavesNoRecordAndNoRow(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "look around")
	h.start()
	h.toolCall("bash", "r1", map[string]any{"command": "ls"})
	if got := len(h.w.learn.ledger.Records()); got != 0 {
		t.Fatalf("a call the guard skipped as read-only could never have been held: %d records", got)
	}
	if rows := h.holdRows(); len(rows) != 0 {
		t.Fatalf("%v", rows)
	}
}

func TestWiringAUserMessageNeverSettlesAWatchedSteer(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.w.adaptor.add("prov/mod", []SteerKind{SteerKindRules}, SteerSubject{Path: "src/a.go"}, false)
	h.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "user", "content": "as I said, this is expected"}})
	if _, ok := h.w.adaptor.pair("prov/mod", SteerKindRules); ok {
		t.Fatal("only the agent's replies are read for a follow or a dispute")
	}
}

func TestWiringARulesVerdictLandsInTheLedgerThroughTheRulesLanesHook(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	holdsRecordRules(h.w, RulesVerdict{Source: "typesafe", Error: "boom", Findings: []RuleFinding{{RuleScore: RuleScore{Name: "No hardcoded secrets", Violation: 0.88}}}})
	records := h.w.learn.ledger.Records()
	if len(records) != 1 || records[0].Tool != "rules" || !reflect.DeepEqual(records[0].Reasons, []string{"No hardcoded secrets 0.88", "error: boom"}) || records[0].Level != LevelWarn {
		t.Fatalf("%+v", records)
	}
}

func seedSteerStats(t *testing.T, h *harness, model string, kind SteerKind) {
	t.Helper()
	if err := os.MkdirAll(h.dataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	pair := `{"sent":40,"followed":0,"disputed":0,"muted":true,"asked":1,"probe":{"sent":0,"followed":0,"disputed":0}}`
	if err := os.WriteFile(filepath.Join(h.dataDir(), "steer-stats.json"), []byte(`{"version":1,"models":{"`+model+`":{"`+string(kind)+`":`+pair+`}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWiringAShouldProceedSteerIsSentUnlessTheModelHasLearnedToIgnoreIt(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	run := func(muted bool) int {
		cfg := on(BackendTypeSafe)
		cfg.Rules.Enabled = false
		cfg.Action.ShouldProceed.Steer = true
		cfg.Notices = false
		h := startHarness(t, harnessOpts{cfg: cfg})
		if muted {
			seedSteerStats(t, h, "prov/mod", SteerKindShouldProceed)
		}
		h.ts.set(map[string]float64{"should_proceed": 0.1, "irreversible": 0.1})
		h.say("user", "run the build")
		h.start()
		h.toolCall("bash", "c1", map[string]any{"command": "npm run build"})
		n := 0
		for _, s := range h.steers() {
			if strings.Contains(s.Content, "may need user input before it runs") {
				n++
			}
		}
		if muted && !strings.Contains(h.traceDetails(), "steer trace-only · should-proceed · prov/mod") {
			t.Fatalf("the skipped steer has a trace line:\n%s", h.traceDetails())
		}
		return n
	}
	if got := run(false); got != 1 {
		t.Fatalf("the steer is sent: %d", got)
	}
	if got := run(true); got != 0 {
		t.Fatalf("a trace-only kind stays out of the agent's context: %d", got)
	}
}

func TestWiringIntentMismatchesAreCountedForTheSession(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	cfg := on(BackendTypeSafe)
	cfg.Rules.Enabled, cfg.Notices = false, false
	h := startHarness(t, harnessOpts{cfg: cfg})
	h.ts.set(map[string]float64{"intent_mismatch": 0.95, "irreversible": 0.1, "should_proceed": 1.0, "mutates": 0.9, "visible": 0.1, "off_task": 0.1})
	h.say("user", "tidy")
	args, _ := json.Marshal(map[string]any{"command": "touch a.txt"})
	h.assistantCalls("I will list the folder first.", map[string]string{"id": "c1", "name": "bash", "args": string(args)})
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "touch a.txt"})
	if s := h.w.statsCopy(); s.OffPlan != 1 || s.OffPlanTraceOnly != 1 {
		t.Fatalf("a mismatch is counted, and counted as trace-only when the agent is not told: %+v", s)
	}
}

func TestWiringRecommendShowsAtMostThreeSuggestions(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.w.learnFlush()
	store := NewLearning(LearningPath(h.dataDir(), h.w.opts.Getenv), nil)
	for _, reason := range []string{"irreversible action", "off-task drift", "intent mismatch", "pattern match"} {
		for range 5 {
			id, _ := store.RecordHold(baseHold(h.cwd, func(r *HoldRecord) { r.Level, r.AgentReason = "confirm", reason }))
			store.RecordOutcome(id, "declined")
		}
	}
	h.host.Command("warden", "recommend")
	n := h.notices()
	got := n[len(n)-1]
	if c := strings.Count(got, "\n  Steer type '"); c != 3 {
		t.Fatalf("%d suggestions in:\n%s", c, got)
	}
	if !strings.Contains(got, "Overall effectiveness: 0%") {
		t.Fatalf("%s", got)
	}
}

func TestWiringStatusLifetimeLineAndALogWriteFailure(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.w.learnFlush()
	store := NewLearning(LearningPath(h.dataDir(), h.w.opts.Getenv), nil)
	if _, err := store.RecordHold(baseHold(h.cwd, nil)); err != nil {
		t.Fatal(err)
	}
	if got := h.statusText(); !strings.Contains(got, "Lifetime here: 1 hold, not yet measurable (0 allowed).") {
		t.Fatalf("status:\n%s", got)
	}
	if _, err := store.RecordHold(baseHold(h.cwd, nil)); err != nil {
		t.Fatal(err)
	}
	if got := h.statusText(); !strings.Contains(got, "Lifetime here: 2 holds, not yet measurable (0 allowed).") {
		t.Fatalf("status:\n%s", got)
	}
	blocker := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.w.learn.mu.Lock()
	h.w.learn.log = NewHoldLog(filepath.Join(blocker, "log.jsonl"))
	log := h.w.learn.log
	h.w.learn.mu.Unlock()
	log.Save([]CallRecord{{ID: 1}})
	if got := h.statusText(); !strings.Contains(got, " Log write failed: ") {
		t.Fatalf("status:\n%s", got)
	}
}

func TestWiringStatusWaitsForTheQueuedDatabaseWrites(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	h := startHarness(t, harnessOpts{cfg: offlineWarden()})
	h.say("user", "x")
	h.start()
	h.w.learnFlush()
	store := NewLearning(LearningPath(h.dataDir(), h.w.opts.Getenv), nil)
	h.w.learn.queue.do(func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = store.RecordHold(baseHold(h.cwd, nil))
	})
	if got := h.statusText(); !strings.Contains(got, "Lifetime here: 1 hold,") {
		t.Fatalf("the status reads the database after the writes queued before it:\n%s", got)
	}
}
