package warden

// Layer-1 tests of the rules guard's wiring, through the fake host: the events PiG sends, the calls the extension
// makes back. The judge is injected, so nothing here reaches a network.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// rulesRouter answers the action guard's questions as harmless and every rules question from violations.
func rulesRouter(violations map[string]float64) *recordingJudge {
	rules := stubJudge(violations, "")
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		if _, ok := r.Questions["irreversible"]; ok {
			return actionAnswers(0.05, 0.05, "expected_step", nil), nil
		}
		return rules.handler(r)
	}}
}

func rulesHarness(t *testing.T, judge Judge, mutate func(*Config)) *harness {
	t.Helper()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD)
	cfg := on(BackendTypeSafe)
	if mutate != nil {
		mutate(cfg)
	}
	h := startHarness(t, harnessOpts{cfg: cfg, cwd: cwd, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "add the feature")
	h.start()
	return h
}

func rulesRequests(j *recordingJudge) int {
	n := 0
	for _, r := range j.calls() {
		for id := range r.Questions {
			if strings.HasPrefix(id, "rule_") {
				n++
				break
			}
		}
	}
	return n
}

// traceText is every trace line and its detail lines, as the trace panel shows them.
func traceText(h *harness) string {
	h.t.Helper()
	var out []string
	for _, e := range h.w.trace.Entries() {
		out = append(out, e.Line)
		out = append(out, e.Details...)
	}
	return strings.Join(out, "\n")
}

func contains1(t *testing.T, s, want, what string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("%s lacks %q:\n%s", what, want, s)
	}
}

// twin(wiring): tests/extension.test.ts:1689 (a write in a project with pi-warden.md gets its own request beside the
// action request and violations steer the agent).
func TestRulesAWriteGetsItsOwnRequestAndAViolationSteersTheAgent(t *testing.T) {
	judge := rulesRouter(map[string]float64{"no-console-statements": 0.9})
	h := rulesHarness(t, judge, nil)
	if reason, held := blocked(h.toolCall("write", "w1", map[string]any{"path": "src/a.ts", "content": "console.log(1)"})); held {
		t.Fatalf("a write with a rules finding is steered, never held: %s", reason)
	}
	steers := h.steers()
	if len(steers) != 1 {
		t.Fatalf("steers: %+v", steers)
	}
	contains1(t, steers[0].Content, `pi-warden: the content just written to src/a.ts violates project rule: "No console statements" (0.90)`, "the steer")
	contains1(t, steers[0].Content, "Fix it in your next edit.", "the steer")
	mustEqual(t, steers[0].Options["deliverAs"], "steer", "delivery")
	mustEqual(t, rulesRequests(judge), 1, "one rules request")
	mustEqual(t, len(judge.calls()), 2, "one action request and one rules request")
	trace := traceText(h)
	contains1(t, trace, "warden · rules · write src/a.ts", "the trace")
	contains1(t, trace, "No console statements 0.90", "the trace")
}

// twin(wiring): tests/extension.test.ts:1834 and 1860 (a held or declined write gets no rules steer; the approved
// retry is judged again and gets its own).
func TestRulesADeclinedWriteGetsNoSteerAndTheApprovedRetryDoes(t *testing.T) {
	judge := &recordingJudge{}
	rules := stubJudge(map[string]float64{"no-console-statements": 0.9}, "")
	judge.handler = func(r Request) (Evaluation, error) {
		if _, ok := r.Questions["irreversible"]; ok {
			return actionAnswers(0.95, 0.05, "expected_step", nil), nil
		}
		return rules.handler(r)
	}
	h := rulesHarness(t, judge, func(c *Config) { c.Mode = "confirm" })
	h.confirm = false
	if _, held := blocked(h.toolCall("write", "w1", map[string]any{"path": "src/a.ts", "content": "console.log(1)"})); !held {
		t.Fatal("the declined write was not blocked")
	}
	if len(h.steers()) != 0 {
		t.Fatalf("a declined write gets no rules steer: %+v", h.steers())
	}
	contains1(t, traceText(h), "agent not told: the write was held", "the trace")
	h.confirm = true
	if reason, held := blocked(h.toolCall("write", "w2", map[string]any{"path": "src/a.ts", "content": "console.log(1)"})); held {
		t.Fatalf("the approved write was held: %s", reason)
	}
	steers := h.steers()
	if len(steers) != 1 {
		t.Fatalf("the approved retry gets its own steer: %+v", steers)
	}
	contains1(t, steers[0].Content, `violates project rule: "No console statements"`, "the steer")
}

// twin(wiring): tests/extension.test.ts:1771 (a heredoc write in bash is judged as a write; a skipped form leaves a
// trace note).
func TestRulesAHeredocWriteInBashIsJudgedAsAWriteAndASkippedFormIsTraced(t *testing.T) {
	judge := rulesRouter(map[string]float64{"no-console-statements": 0.9})
	h := rulesHarness(t, judge, nil)
	h.toolCall("bash", "b1", map[string]any{"command": "cat > src/a.ts <<'EOF'\nconsole.log(1)\nEOF"})
	steers := h.steers()
	if len(steers) != 1 {
		t.Fatalf("steers: %+v", steers)
	}
	contains1(t, steers[0].Content, "the content just written to src/a.ts", "the steer")
	contains1(t, traceText(h), "from bash: heredoc written to src/a.ts", "the trace")
	h.toolCall("bash", "b2", map[string]any{"command": "sed -i 's/a/b/' src/a.ts"})
	contains1(t, traceText(h), "shell write not judged: sed -i edits a file in place", "the skip note")
}

// twin(wiring): tests/extension.test.ts:1807 (appends to one file in one bash call are one rules request; past five
// files the rest are skipped).
func TestRulesAppendsToOneFileAreOneRequestAndPastFiveFilesTheRestAreSkipped(t *testing.T) {
	judge := rulesRouter(nil)
	h := rulesHarness(t, judge, nil)
	h.toolCall("bash", "b1", map[string]any{"command": "echo a >> log.md; echo b >> log.md; echo c >> log.md"})
	mustEqual(t, rulesRequests(judge), 1, "three appends to one file are one request")
	var cmds []string
	for i := 0; i < 7; i++ {
		cmds = append(cmds, "echo x > f"+itoaInt(i)+".txt")
	}
	judge.reset()
	h.toolCall("bash", "b2", map[string]any{"command": strings.Join(cmds, "; ")})
	mustEqual(t, rulesRequests(judge), shellRulesChecks, "past five files the rest are skipped")
	contains1(t, traceText(h), "only the first 5 files a command writes are judged", "the skip note")
}

// twin(wiring): tests/extension.test.ts:1689 (sensitive paths: one note per path per session).
func TestRulesASensitivePathGetsOneNotePerPath(t *testing.T) {
	judge := rulesRouter(nil)
	h := rulesHarness(t, judge, func(c *Config) {
		c.Rules.SensitivePaths = map[string]string{"migrations/**": "Tell the user this touches a migration and add a rollback"}
	})
	h.toolCall("write", "w1", map[string]any{"path": "src/db/migrations/0182.sql", "content": "alter table t add column c int"})
	steers := h.steers()
	if len(steers) != 1 {
		t.Fatalf("steers: %+v", steers)
	}
	contains1(t, steers[0].Content, "pi-warden: src/db/migrations/0182.sql is a sensitive path in this project (migrations/**). Tell the user this touches a migration and add a rollback.", "the note")
	h.toolCall("write", "w2", map[string]any{"path": "src/db/migrations/0182.sql", "content": "alter table t add column d int"})
	mustEqual(t, len(h.steers()), 1, "not repeated for the same path")
	contains1(t, traceText(h), "src/db/migrations/0182.sql matches migrations/** in rules.sensitivePaths", "the trace")
}

// twin(wiring): tests/guard.test.ts:1380 (rules.enabled false keeps the guard off).
func TestRulesDisabledSendsNoRulesRequest(t *testing.T) {
	judge := rulesRouter(map[string]float64{"no-console-statements": 0.9})
	h := rulesHarness(t, judge, func(c *Config) { c.Rules.Enabled = false })
	h.toolCall("write", "w1", map[string]any{"path": "src/a.ts", "content": "console.log(1)"})
	mustEqual(t, rulesRequests(judge), 0, "no rules request")
	mustEqual(t, len(h.steers()), 0, "no steer")
}

// twin(wiring): tests/extension.test.ts:2538 and the command output of /warden rules.
func TestRulesCommandListsTheRulesAndCheckNamesWhatNeedsAttention(t *testing.T) {
	judge := lintJudge(func(id string, q Question) Answer {
		if q.Type == "noul" {
			return mechanicalAnswer(0.05)
		}
		if id == "judgeable_todo-comments-need-a-reference" {
			return judgedAnswer("needs_other_files")
		}
		return judgedAnswer("from_change_alone")
	}, nil)
	h := rulesHarness(t, judge, nil)
	h.host.Command("warden", "rules")
	notices := strings.Join(h.notices(), "\n")
	contains1(t, notices, "Rules in force: pi-warden.md (4 rules, 0 dropped)", "/warden rules")
	h.host.Command("warden", "rules check")
	notices = strings.Join(h.notices(), "\n")
	contains1(t, notices, "Rules check: pi-warden.md — 4 rules checked in 1 request.", "/warden rules check")
	contains1(t, notices, "- TODO comments need a reference (todo-comments-need-a-reference): needs another file to judge (0.80).", "/warden rules check")
	// The check is remembered: tune asks the session's agent for the one rewrite prompt.
	h.host.Command("warden", "rules tune")
	asks := h.host.CallsTo("sendUserMessage")
	if len(asks) != 1 {
		t.Fatalf("sendUserMessage calls: %+v", asks)
	}
	prompt, _ := asks[0].Args["content"].(string)
	contains1(t, prompt, "## TODO comments need a reference (todo-comments-need-a-reference)", "the tune prompt")
	contains1(t, strings.Join(h.notices(), "\n"), "Rewrite prompt sent.", "/warden rules tune")
}

// twin(wiring): tests/extension.test.ts:2538 (without a key or consent nothing is sent and the notice says why).
func TestRulesCheckWithoutConsentSendsNothing(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD)
	h := startHarness(t, harnessOpts{cwd: cwd})
	h.say("user", "x")
	h.start()
	h.host.Command("warden", "rules check")
	got := strings.Join(h.notices(), "\n")
	contains1(t, got, "Rules check sent nothing: Jev judgments are off. warden: Jev judgments are off (no consent). Run /warden enable. /warden rules shows the rule set locally.", "the notice")
	mustEqual(t, h.ts.count(), 0, "nothing sent")
}

// twin(wiring): tests/extension.test.ts:2586 (rules audit confirms before sending, writes the markdown copy, and needs
// --yes headless).
func TestRulesAuditNeedsYesHeadlessAndWritesTheMarkdownCopy(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), "# No console statements\nCode must not contain `console.log`.\n")
	writeFile(t, filepath.Join(cwd, "src", "a.ts"), "export const a = 1;\n")
	judge := stubJudge(map[string]float64{"no-console-statements": 0.9}, "")
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), cwd: cwd, noUI: true, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.start()
	h.host.Command("warden", "rules audit src")
	var statuses []string
	for _, m := range h.messages() {
		if m.Type == statusType {
			statuses = append(statuses, m.Content)
		}
	}
	got := strings.Join(statuses, "\n")
	contains1(t, got, "Rules audit sent nothing: a headless run needs --yes before 1 file samples go to", "headless without --yes")
	mustEqual(t, len(judge.calls()), 0, "nothing was sent")
	h.host.Command("warden", "rules audit src --yes")
	mustEqual(t, len(judge.calls()), 1, "one file judged")
	data := readFileString(t, filepath.Join(cwd, ".pi-warden", "rules-audit.md"))
	contains1(t, data, "# Rules audit", "the markdown copy")
}

// twin(wiring): tests/extension.test.ts:2421 and 2439 (/warden init --force overwrites in headless mode; without
// --force it refuses).
func TestInitRefusesWithoutForceAndAsksTheAgentWithIt(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), "# Old\nBody.\n")
	h := startHarness(t, harnessOpts{cwd: cwd, noUI: true})
	h.start()
	h.host.Command("warden", "init")
	var got []string
	for _, m := range h.messages() {
		if m.Type == statusType {
			got = append(got, m.Content)
		}
	}
	contains1(t, strings.Join(got, "\n"), "pi-warden.md already exists at "+filepath.Join(cwd, "pi-warden.md")+". Pass --force to overwrite.", "init without --force")
	mustEqual(t, len(h.host.CallsTo("sendUserMessage")), 0, "nothing asked of the agent")
	h.host.Command("warden", "init --force")
	asks := h.host.CallsTo("sendUserMessage")
	if len(asks) != 1 {
		t.Fatalf("sendUserMessage calls: %+v", asks)
	}
	prompt, _ := asks[0].Args["content"].(string)
	contains1(t, prompt, "Create a `pi-warden.md` file for this project.", "the init prompt")
	contains1(t, prompt, "Existing pi-warden.md rules (preserve these)", "the init prompt keeps the old rules")
}

// twin(wiring): /warden report over the local log.
func TestReportReadsTheLocalLog(t *testing.T) {
	cwd := t.TempDir()
	h := startHarness(t, harnessOpts{cwd: cwd})
	h.say("user", "x")
	h.start()
	path := rulesLogPath(cwd, filepath.Dir(ConfigPath(h.home)))
	log := NewRulesLog(cwd, filepath.Dir(path), "s1", RulesLogOptions{Path: path})
	now := time.Now()
	log.AppendRecords([]RuleRecord{
		{At: isoTime(now), Session: "s1", Path: "a.ts", Tool: "write", ID: "no-stubs", Name: "No stubs", Outcome: "violation", Violation: 0.9, Threshold: 0.7, Finding: true},
		{At: isoTime(now), Session: "s1", Path: "a.ts", Tool: "edit", ID: "no-stubs", Name: "No stubs", Outcome: "compliant", Violation: 0.1, Threshold: 0.7, Cleared: true, Source: "calibrate"},
	})
	log.Flush()
	h.host.Command("warden", "report")
	got := strings.Join(h.notices(), "\n")
	contains1(t, got, "Rules report: 1 judgment, 1 rule, last 30 days.", "the report")
	contains1(t, got, "1 calibration replay record in this log, counted apart (source: calibrate).", "the replay note")
	h.host.Command("warden", "report --days 0")
	contains1(t, strings.Join(h.notices(), "\n"), "Usage: /warden report [--days N], where N is at least 1. Default: 30 days.", "the usage")
}

// twin(wiring): tests/extension.test.ts:4984 (one end-of-run steer covers the run's diff, delivered like the
// done-check's).
func TestRulesTurnRulesSteerOnceAtTheEndOfTheRun(t *testing.T) {
	cwd := t.TempDir()
	gitOutputIn(t, cwd, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(cwd, "a.txt"), "one\n")
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), "# Stay inside the task\nwhen: turn\nThe diff must contain only what the task asked for.\n")
	gitOutputIn(t, cwd, "add", ".")
	gitOutputIn(t, cwd, "commit", "-q", "-m", "seed")
	judge := rulesRouter(map[string]float64{"stay-inside-the-task": 0.9, "turn_stay-inside-the-task": 0.9})
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), cwd: cwd, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "fix a")
	h.start()
	h.host.Fire("agent_start", nil)
	h.baseline()
	writeFile(t, filepath.Join(cwd, "a.txt"), "one\ntwo\nscheduler\n")
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Done."}}}}})
	steers := h.steers()
	if len(steers) != 1 {
		t.Fatalf("steers: %+v", steers)
	}
	contains1(t, steers[0].Content, `pi-warden: the changes this run made violate a project rule: "Stay inside the task" (0.90)`, "the steer")
	contains1(t, steers[0].Content, "Review the run's diff and fix the violations before you finish.", "the steer")
	mustEqual(t, steers[0].Options["deliverAs"], "followUp", "delivery")
	mustEqual(t, steers[0].Options["triggerTurn"], true, "starts a turn")
	contains1(t, traceText(h), "warden · rules · turn the run's diff", "the trace")
}

// twin(wiring): tests/extension.test.ts:5018 and 5032 (no turn rules and no unseen change: the end of the run asks
// nothing; no repository: skipped).
func TestRulesTurnRulesAskNothingWithoutTurnRulesOrARepository(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), "# Stay inside the task\nwhen: turn\nThe diff must contain only what the task asked for.\n")
	judge := rulesRouter(map[string]float64{"stay-inside-the-task": 0.9, "turn_stay-inside-the-task": 0.9})
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), cwd: cwd, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "fix a")
	h.start()
	h.host.Fire("agent_start", nil)
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Done."}}}}})
	mustEqual(t, len(judge.calls()), 0, "no request without a repository")
	mustEqual(t, len(h.steers()), 0, "no steer")
	contains1(t, traceText(h), "warden · rules · turn rules skipped this run: not a git repository", "the trace")
}

func noticesMatching(h *harness, substr string) []string {
	var out []string
	for _, n := range h.notices() {
		if strings.Contains(n, substr) {
			out = append(out, n)
		}
	}
	return out
}

// twin(wiring): tests/extension.test.ts:286 (a bound rules.files entry keeps the first-run notice away from the fallback
// document; with the files gone the same session does name it).
func TestRulesABoundRulesFilesEntryKeepsTheFirstRunNoticeAway(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "AGENTS.md"), "# Agents\nProject-wide policy.\n")
	global := filepath.Join(cwd, "global-rules.md")
	writeFile(t, global, "# Global rule\nGlobal body.\n")
	writeFile(t, filepath.Join(cwd, "warden-local-rules.md"), "# Local rule\nLocal body.\n")
	cfg := on(BackendTypeSafe)
	cfg.Notices = true
	cfg.Rules.Files = []string{global, "warden-local-rules.md"}
	h := startHarness(t, harnessOpts{cfg: cfg, cwd: cwd, newJudge: func(Config, *Budget) (Judge, error) { return rulesRouter(nil), nil }})
	h.say("user", "x")
	h.start()
	h.toolCall("bash", "b1", map[string]any{"command": "npm test"})
	if got := append(noticesMatching(h, "fallback rules"), noticesMatching(h, "No rules file detected")...); len(got) != 0 {
		t.Fatalf("the configured files are the rules in force, so there is nothing to notice: %v", got)
	}
	// Control, so a notice that never fires for any reason cannot pass this test: with the files gone the same session
	// does name the fallback document.
	cfg.Rules.Files = nil
	if err := SaveConfig(ConfigPath(h.home), *cfg); err != nil {
		t.Fatal(err)
	}
	h.start()
	h.toolCall("bash", "b2", map[string]any{"command": "npm test"})
	got := noticesMatching(h, "Using AGENTS.md as active fallback rules")
	if len(got) != 1 {
		t.Fatalf("the fallback notice: %v", got)
	}
}

// twin(wiring): tests/extension.test.ts:643 and 665 (rules past the cap: one notice per session; a write with notices off
// does not use it up).
func TestRulesPastTheCapOneNoticePerSession(t *testing.T) {
	cwd := t.TempDir()
	var many []string
	for i := 1; i <= 40; i++ {
		many = append(many, "# Rule "+itoaInt(i)+"\nBody "+itoaInt(i)+".")
	}
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), strings.Join(many, "\n\n"))
	judge := rulesRouter(nil)
	cfg := on(BackendTypeSafe)
	cfg.Notices = false
	h := startHarness(t, harnessOpts{cfg: cfg, cwd: cwd, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "x")
	h.start()
	h.toolCall("write", "w1", map[string]any{"path": "src/quiet.ts", "content": "export const quiet = 1;"})
	mustEqual(t, len(noticesMatching(h, "not judged, past the 31-question cap")), 0, "no notice with notices off")
	cfg.Notices = true
	if err := SaveConfig(ConfigPath(h.home), *cfg); err != nil {
		t.Fatal(err)
	}
	h.start()
	h.toolCall("write", "w2", map[string]any{"path": "src/loud.ts", "content": "export const loud = 2;"})
	shown := noticesMatching(h, "not judged, past the 31-question cap")
	if len(shown) != 1 {
		t.Fatalf("the first shown notice comes on the later write: %v", shown)
	}
	for _, want := range []string{"pi-warden.md", "rule-32", "9 rules"} {
		contains1(t, shown[0], want, "the notice")
	}
	h.toolCall("write", "w3", map[string]any{"path": "src/again.ts", "content": "export const again = 3;"})
	mustEqual(t, len(noticesMatching(h, "not judged, past the 31-question cap")), 1, "not repeated in the same session")
	contains1(t, traceText(h), "9 past the question cap from rule-32", "the trace")
}

// rulesGitProject is a repository with the rules file (not committed) and the commits of files in order.
func rulesGitProject(t *testing.T, rules string, commits ...map[string]string) string {
	t.Helper()
	repo := makeRepo(t)
	writeFile(t, filepath.Join(repo, "pi-warden.md"), rules)
	writeFile(t, filepath.Join(repo, ".gitignore"), "ignored.log\npi-warden.md\n")
	for i, files := range commits {
		commitFiles(t, repo, files, "commit "+itoaInt(i+1))
	}
	return repo
}

const consoleRule = "# No console statements\nCode must not contain `console.log`. Use the logger.\n"

// twin(wiring): tests/extension.test.ts:4851 (the calibrate dialog shows the requests and the redacted diffs; declining
// sends nothing).
func TestRulesCalibrateDeclineSendsNothing(t *testing.T) {
	project := rulesGitProject(t, consoleRule,
		map[string]string{"src/app.ts": "const a = 1;\npassword: hunter2secret\n"},
		map[string]string{"src/app.ts": "const a = 2;\npassword: hunter2secret2\n"},
		map[string]string{"ignored.log": "log\n"})
	judge := rulesRouter(nil)
	cfg := on(BackendTypeSafe)
	cfg.Notices = true
	h := startHarness(t, harnessOpts{cfg: cfg, cwd: project, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "x")
	h.start()
	h.confirm = false
	h.host.Command("warden", "rules calibrate --commits 5 --max 10")
	dialog := h.host.CallsTo("ui.confirm")
	mustEqual(t, len(dialog), 1, "the dialog is shown before anything is sent")
	contains1(t, dialog[0].Args["title"].(string), "send 2 calibration requests?", "dialog title")
	message := dialog[0].Args["message"].(string)
	contains1(t, message, "2 requests will go to the judgment backend", "dialog")
	contains1(t, message, "src/app.ts", "dialog")
	contains1(t, message, "redacted", "dialog")
	mustNotContain(t, message, "hunter2secret", "the diff is redacted before it is shown")
	mustEqual(t, len(judge.calls()), 0, "declining sends nothing")
	notices := h.notices()
	mustEqual(t, notices[len(notices)-1], "Cancelled. Nothing was sent.", "notice")
}

// twin(wiring): tests/extension.test.ts:4870 (--yes sends with no dialog, respects the cap, and records source
// calibrate; a headless run without --yes refuses).
func TestRulesCalibrateYesSendsRespectsTheCapAndRecords(t *testing.T) {
	project := rulesGitProject(t, consoleRule,
		map[string]string{"src/app.ts": "const a = 1;\n"},
		map[string]string{"src/app.ts": "const a = 2;\n"},
		map[string]string{"src/other.ts": "const b = 3;\n"})
	judge := rulesRouter(nil)
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), cwd: project, noUI: true, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.start()
	statusMessages := func() string {
		var out []string
		for _, m := range h.messages() {
			if m.Type == statusType {
				out = append(out, m.Content)
			}
		}
		return strings.Join(out, "\n---\n")
	}
	h.host.Command("warden", "rules calibrate --commits 5 --max 2 --yes")
	mustEqual(t, len(h.host.CallsTo("ui.confirm")), 0, "--yes stands in for the dialog")
	mustEqual(t, len(judge.calls()), 2, "the cap is the number of requests")
	report := statusMessages()
	contains1(t, report, "Rules calibrate: 2 requests, 3 commits, 1 past the cap, not sent.", "report")
	mustMatch(t, report, `Worst first:\n1\. No console statements · 2 applied · 0 fired 0% · mean 0\.05 · no violation in sample`, "report rows")
	contains1(t, report, `2 scores saved to the local rules log with source "calibrate"`, "report")
	h.w.rules.log.Flush()
	stored := mustReadLog(t, h.w.rules.log.Path)
	mustEqual(t, len(stored), 2, "stored records")
	for _, r := range stored {
		if r.Source != "calibrate" || r.Tool != "edit" {
			t.Fatalf("record %+v", r)
		}
	}
	judge.reset()
	h.host.Command("warden", "rules calibrate --commits 5 --max 2")
	mustEqual(t, len(judge.calls()), 0, "a headless run without --yes sends nothing")
	contains1(t, statusMessages(), "headless run needs an explicit --yes", "refusal")
}

// twin(wiring): tests/extension.test.ts:4895 (tune with nothing flagged says so and asks the agent nothing).
func TestRulesTuneWithNothingFlaggedAsksNothing(t *testing.T) {
	h := rulesHarness(t, rulesRouter(nil), nil)
	h.host.Command("warden", "rules tune")
	mustEqual(t, len(h.host.CallsTo("sendUserMessage")), 0, "nothing flagged means no prompt")
	got := h.notices()
	contains1(t, got[len(got)-1], "Nothing flagged: run /warden rules calibrate or /warden rules check first", "notice")
}

// twin(wiring): tests/extension.test.ts:5047 (the end-of-run pass judges at most five unseen files and names the rest).
func TestRulesTurnRulesPassJudgesAtMostFiveUnseenFiles(t *testing.T) {
	repo := makeRepo(t)
	writeFile(t, filepath.Join(repo, "pi-warden.md"), consoleRule)
	writeFile(t, filepath.Join(repo, "seed.txt"), "seed\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "seed")
	judge := rulesRouter(nil)
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), cwd: repo, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "generate the reports")
	h.start()
	h.host.Fire("agent_start", nil)
	h.baseline()
	// Twelve files changed outside any tool call, like a generator's output; no per-edit check judged them.
	for i := 1; i <= 12; i++ {
		writeFile(t, filepath.Join(repo, "f"+strings.ReplaceAll(padStart(itoaInt(i), 2), " ", "0")+".txt"), "generated "+itoaInt(i)+"\n")
	}
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Done."}}}}})
	mustEqual(t, len(judge.calls()), shellRulesChecks, "one request per judged file, capped")
	var paths []string
	for _, r := range judge.calls() {
		paths = append(paths, r.State["path"].(string))
	}
	sortStrings(paths)
	mustEqual(t, paths, []string{"f01.txt", "f02.txt", "f03.txt", "f04.txt", "f05.txt"}, "the first files in diff order are judged")
	trace := traceText(h)
	for i := 6; i <= 12; i++ {
		p := "f" + strings.ReplaceAll(padStart(itoaInt(i), 2), " ", "0") + ".txt"
		contains1(t, trace, "unseen change not judged ("+p+"): only the first 5 files a run changes are judged", "trace")
	}
}

// twin(wiring): tests/extension.test.ts:5073 (agent_start returns without waiting for the snapshot, and the run end
// waits for it). A git shim holds `write-tree` until a marker file appears.
func TestRulesAgentStartDoesNotWaitForTheSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git shim is a shell script")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not available")
	}
	repo := makeRepo(t)
	writeFile(t, filepath.Join(repo, "pi-warden.md"), "# Stay inside the task\nwhen: turn\nOnly what the task asked for.\n\n# No console statements\nCode must not contain `console.log`.\n")
	writeFile(t, filepath.Join(repo, "app.txt"), "alpha\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "seed")
	shim := t.TempDir()
	marker, started := filepath.Join(shim, "released"), filepath.Join(shim, "write-tree-started")
	script := "#!/bin/sh\nif [ \"$1\" = \"write-tree\" ] && [ ! -e '" + marker + "' ]; then : > '" + started + "'; while [ ! -e '" + marker + "' ]; do sleep 0.05; done; fi\nexec '" + realGit + "' \"$@\"\n"
	writeFile(t, filepath.Join(shim, "git"), script)
	if err := os.Chmod(filepath.Join(shim, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	judge := rulesRouter(map[string]float64{"stay-inside-the-task": 0.1, "turn_stay-inside-the-task": 0.1})
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), cwd: repo, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }})
	h.say("user", "rename alpha to beta")
	h.start()
	began := time.Now()
	h.host.Fire("agent_start", nil)
	if held := time.Since(began); held > 1500*time.Millisecond {
		t.Fatalf("agent_start must return without waiting for the snapshot: held %v", held)
	}
	waitUntil(t, func() bool { _, err := os.Stat(started); return err == nil })
	writeFile(t, filepath.Join(repo, "app.txt"), "beta\n")
	writeFile(t, marker, "")
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Done."}}}}})
	mustEqual(t, len(judge.calls()), 2, "the run end waits for the snapshot and judges the change made after it started")
}

// baseline waits until the run's baseline snapshot has been taken (upstream's tests wait for the git shim's marker).
func (h *harness) baseline() {
	h.t.Helper()
	run := h.w.rules.snapshot
	if run == nil {
		h.t.Fatal("no baseline snapshot was started")
	}
	select {
	case <-run.done:
	case <-time.After(10 * time.Second):
		h.t.Fatal("the baseline snapshot did not finish")
	}
}
