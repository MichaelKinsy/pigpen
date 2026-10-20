package warden

// Twins of tests/turn-rules.test.ts (end-of-run rules). Each test builds its own repository in the state the
// upstream suite's earlier cases leave behind for it.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const turnMD = "# The change stays inside the task\nwhen: turn\nThe diff must contain only what the user's task asked for.\n\n# No console statements\nCode must not contain `console.log` calls. Use the logger.\n\n# No single-use abstractions\nwhen: turn\nA new helper introduced and used exactly once is a violation."

func gitOutputIn(t *testing.T, repo string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git %v: %v", args, err)
	}
	return string(out)
}

// turnRepo is the upstream suite's seeded repository: two tracked files, an ignore file, the rules file and one stash.
func turnRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitOutputIn(t, repo, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "bee\n")
	writeFile(t, filepath.Join(repo, ".gitignore"), "ignored.txt\n")
	writeFile(t, filepath.Join(repo, "pi-warden.md"), turnMD)
	gitOutputIn(t, repo, "add", ".")
	gitOutputIn(t, repo, "commit", "-q", "-m", "seed")
	// One stash entry, so the read-only check has a stash list worth keeping.
	writeFile(t, filepath.Join(repo, "b.txt"), "stashed\n")
	gitOutputIn(t, repo, "stash", "push", "-q", "-m", "seed-stash")
	writeFile(t, filepath.Join(repo, "b.txt"), "bee\n")
	return repo
}

// readOnlyState is everything the snapshot must leave alone: the stash list, the real index bytes, and the worktree state.
func readOnlyState(t *testing.T, repo string) string {
	t.Helper()
	index := strings.TrimSpace(gitOutputIn(t, repo, "rev-parse", "--git-path", "index"))
	data, err := os.ReadFile(resolvePath(repo, index))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return gitOutputIn(t, repo, "stash", "list") + "|" + hex.EncodeToString(sum[:]) + "|" + gitOutputIn(t, repo, "status", "--porcelain")
}

// turnJudge answers every rule question from violations by rule id (with the rule_ or turn_ prefix removed).
func turnJudge(violations map[string]float64) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		answers := map[string]Answer{}
		for id := range r.Questions {
			violation, ok := violations[strings.TrimPrefix(strings.TrimPrefix(id, "rule_"), "turn_")]
			if !ok {
				violation = 0.05
			}
			choice := "compliant"
			if violation >= 0.5 {
				choice = "violation"
			}
			answers[id] = Answer{Type: "choice", Choice: choice, Confidence: 0.9, Probabilities: map[string]float64{"compliant": 1 - violation - 0.02, "violation": violation, "not_applicable": 0.01, "insufficient_context": 0.01}}
		}
		return Evaluation{Model: "jev-test", ElapsedMs: 7, Answers: answers}, nil
	}}
}

func mustSnapshot(t *testing.T, repo string) string {
	t.Helper()
	snapshot := snapshotTree(repo)
	if snapshot.Tree == "" {
		t.Fatalf("snapshot must succeed: %s", snapshot.Reason)
	}
	return snapshot.Tree
}

// twin: tests/turn-rules.test.ts:81
func TestParseRulesWhenTurnMarksARuleForTheEndOfRunPass(t *testing.T) {
	rules := parseRules(strings.Join([]string{
		"# Edit rule", "Body.", "",
		"# Turn rule", "when: turn", "Body.", "",
		"# Mixed headers", "when: Turn", "paths: src/**", "severity: low", "threshold: 0.8", "Body.", "",
		"# Bad when", "when: later", "Body.", "",
		"# Duplicate when", "when: turn", "when: edit", "Body.", "",
	}, "\n"))
	mustEqual(t, rules[0].When, "", "no when: line means edit")
	mustEqual(t, rules[1].When, "turn", "turn")
	mustEqual(t, rules[2].When, "turn", "when: is case-insensitive like the other headers")
	mustEqual(t, rules[2].Paths, []string{"src/**"}, "paths")
	mustEqual(t, rules[2].Severity, "low", "severity")
	mustEqual(t, *rules[2].Threshold, 0.8, "threshold")
	mustEqual(t, rules[3].HeaderWarnings, []string{"when: later is not edit or turn; ignored"}, "bad value warns")
	mustEqual(t, rules[3].When, "", "a bad value is dropped")
	mustEqual(t, rules[4].HeaderWarnings, []string{"when: appears more than once; the first is kept"}, "duplicate warns")
	mustEqual(t, rules[4].When, "turn", "the first when: is kept")
	mustNotContain(t, rules[3].Body, "when:", "the header line never stays in the body")
	details := formatRuleSetDetails(&RuleSet{Sources: []string{"pi-warden.md"}, Rules: []Rule{rules[1]}}, "root", nil)
	mustMatch(t, details, `when: turn`, "/warden rules shows the header")
}

// twin: tests/turn-rules.test.ts:104
func TestEvaluateRulesTurnRulesAreNeverAskedOnAWriteOrAnEdit(t *testing.T) {
	repo := turnRepo(t)
	set := &RuleSet{Rules: parseRules(turnMD)}
	judge := turnJudge(map[string]float64{"no-single-use-abstractions": 0.9, "the-change-stays-inside-the-task": 0.9, "no-console-statements": 0.9})
	verdict := evaluateRules("write", writeInput("src/a.ts", "console.log(1)"), rulesOpts(repo, rulesConfig(), set, judge))
	mustEqual(t, keysOf(nth(t, judge, 0).Questions), []string{"rule_no-console-statements"}, "the turn rules are not asked per edit")
	mustEqual(t, len(verdict.Findings), 1, "findings")
	mustEqual(t, len(turnRulesFor(set)), 2, "turn rules")
	mustEqual(t, len(editRulesFor(set, "src/a.ts")), 1, "edit rules")
	turnOnly := &RuleSet{Rules: []Rule{parseRules(turnMD)[0]}}
	content := "x"
	mustEqual(t, skipReason(&RulesTarget{Tool: "write", Path: "src/a.ts", Content: &content}, turnOnly, rulesConfig()), "no rule's paths match this file", "skip reason")
}

// twin: tests/turn-rules.test.ts:117
func TestSnapshotTreeAndDiffSince(t *testing.T) {
	repo := turnRepo(t)
	before := readOnlyState(t, repo)
	tree := mustSnapshot(t, repo)
	mustEqual(t, readOnlyState(t, repo), before, "snapshotTree must not touch the stash list, the index, or the worktree")

	writeFile(t, filepath.Join(repo, "a.txt"), "one\ntwo\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "fresh\n")
	writeFile(t, filepath.Join(repo, "ignored.txt"), "never judged\n")
	dirty := readOnlyState(t, repo)
	diff := diffSince(repo, tree, 8000)
	if diff == nil {
		t.Fatal("no diff")
	}
	paths, statuses := []string{}, []string{}
	for _, f := range diff.Files {
		paths, statuses = append(paths, f.Path), append(statuses, f.Status)
	}
	mustEqual(t, paths, []string{"a.txt", "new.txt"}, "the changed tracked file and the new untracked file, not the ignored one")
	mustEqual(t, statuses, []string{"M", "A"}, "statuses")
	mustMatch(t, diff.Text, `\+two`, "the diff shows the added line")
	mustEqual(t, readOnlyState(t, repo), dirty, "diffSince must not touch the stash list, the index, or the worktree")
	mustEqual(t, len(strings.Split(strings.TrimSpace(gitOutputIn(t, repo, "stash", "list")), "\n")), 1, "the stash list is untouched")

	writeFile(t, filepath.Join(repo, "a.txt"), "one\n"+strings.Repeat("filler line to cut\n", 60))
	capped := diffSince(repo, tree, 200)
	found := false
	for _, cut := range capped.Cuts {
		found = found || strings.HasPrefix(cut, "a.txt: file diff cut")
	}
	if !found {
		t.Fatalf("a per-file cut is named: %v", capped.Cuts)
	}
	if utf16Len(capped.Text) > 200+100 {
		t.Fatal("the total is capped")
	}
	mustMatch(t, capped.Text, `cut`, "the cut is said inline")
	for _, f := range capped.Files {
		if f.Path == "a.txt" && !strings.Contains(f.Diff, "more chars of this file's diff cut") {
			t.Fatal("the per-file cap says what it cut")
		}
	}
}

// twin: tests/turn-rules.test.ts:144
func TestSnapshotTreeNotARepositoryGetsAReasonAndNoTree(t *testing.T) {
	result := snapshotTree(t.TempDir())
	mustEqual(t, result.Tree, "", "tree")
	mustEqual(t, result.Reason, "not a git repository", "reason")
}

func turnRunOpts(repo string, set *RuleSet, judge Judge, tree, task string, judged ...string) TurnRunOptions {
	already := map[string]bool{}
	for _, p := range judged {
		already[p] = true
	}
	return TurnRunOptions{Cwd: repo, Config: rulesConfig(), Set: set, Judge: judge, Timeout: time.Second, Task: task, StartTree: tree, AlreadyJudged: already}
}

func toolPaths(verdicts []RulesVerdict) [][2]string {
	out := [][2]string{}
	for _, v := range verdicts {
		out = append(out, [2]string{v.Tool, v.Path})
	}
	return out
}

// twin: tests/turn-rules.test.ts:150
func TestEvaluateTurnRunAFileAPerEditJudgmentSawIsJudgedOnce(t *testing.T) {
	repo := turnRepo(t)
	config := rulesConfig()
	set := NewRuleStore().Load(repo, config)
	tree := mustSnapshot(t, repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "one\ntwo\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "changed by sed\n")
	judge := turnJudge(map[string]float64{"no-console-statements": 0.9, "the-change-stays-inside-the-task": 0.9, "no-single-use-abstractions": 0.05})
	run := evaluateTurnRun(turnRunOpts(repo, set, judge, tree, "update the config", "b.txt"))
	mustEqual(t, toolPaths(run.Verdicts), [][2]string{{"turn", turnPath}, {"edit", "a.txt"}}, "the turn verdict plus the one file no per-edit check saw")
	calls := judge.calls()
	mustEqual(t, len(calls), 2, "requests")
	// The upstream stub reads the requests in arrival order; the turn request is the one with a task.
	var turnRequest, fileRequest Request
	for _, c := range calls {
		if _, ok := c.State["task"]; ok {
			turnRequest = c
		} else {
			fileRequest = c
		}
	}
	mustEqual(t, turnRequest.State["task"], "update the config", "task")
	mustMatch(t, turnRequest.State["diff"].(string), `a\.txt`, "the turn question sees the whole diff")
	mustMatch(t, turnRequest.State["diff"].(string), `changed by sed`, "the turn question sees every changed file, judged or not")
	newText := fileRequest.State["edits"].([]any)[0].(map[string]any)["newText"].(string)
	mustMatch(t, newText, `a\.txt`, "the file's diff stands as the edit")
	mustNotContain(t, newText, "changed by sed", "the already-judged file gets no second request")
	mustEqual(t, findingIDs(run.Verdicts[0].Findings), []string{"the-change-stays-inside-the-task"}, "turn finding")
	mustEqual(t, findingIDs(run.Verdicts[1].Findings), []string{"no-console-statements"}, "file finding")
}

// twin: tests/turn-rules.test.ts:176
func TestEvaluateTurnRunASedChangeNoPerEditGuardSawIsJudgedWithTheEditRules(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sed is not available")
	}
	repo := turnRepo(t)
	editOnly := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules("# No console statements\nCode must not contain `console.log` calls.")}
	writeFile(t, filepath.Join(repo, "b.txt"), "changed by sed\n")
	tree := mustSnapshot(t, repo)
	args := []string{"-i", "-e", "s/changed by sed/one/", "b.txt"}
	if runtime.GOOS == "darwin" {
		args = []string{"-i", "", "-e", "s/changed by sed/one/", "b.txt"}
	}
	cmd := exec.Command("sed", args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("sed: %v %s", err, out)
	}
	judge := turnJudge(map[string]float64{"no-console-statements": 0.9})
	run := evaluateTurnRun(turnRunOpts(repo, editOnly, judge, tree, "fix b"))
	mustEqual(t, toolPaths(run.Verdicts), [][2]string{{"edit", "b.txt"}}, "the sed -i file is judged as one edit of its diff")
	mustEqual(t, len(judge.calls()), 1, "no turn request when the rule set has no when: turn rules")
	mustEqual(t, findingIDs(run.Verdicts[0].Findings), []string{"no-console-statements"}, "finding")
}

// twin: tests/turn-rules.test.ts:190
func TestEvaluateTurnRunWithNoTurnRulesAndNoUnseenChangeIsSilent(t *testing.T) {
	repo := turnRepo(t)
	editOnly := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules("# No console statements\nCode must not contain `console.log` calls.")}
	tree := mustSnapshot(t, repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "written by the agent\n")
	judge := turnJudge(nil)
	run := evaluateTurnRun(turnRunOpts(repo, editOnly, judge, tree, "", "a.txt", "pi-warden.md"))
	mustEqual(t, len(run.Verdicts), 0, "verdicts")
	mustEqual(t, run.Skipped, "", "skipped")
	mustEqual(t, len(judge.calls()), 0, "requests")
}

// twin: tests/turn-rules.test.ts:203
func TestEvaluateTurnRunNoJudgeOrNoRulesFileIsOneSkipEach(t *testing.T) {
	repo := turnRepo(t)
	config := rulesConfig()
	set := NewRuleStore().Load(repo, config)
	writeFile(t, filepath.Join(repo, "a.txt"), "unseen change\n")
	tree := mustSnapshot(t, repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "unseen change again\n")
	offline := evaluateTurnRun(turnRunOpts(repo, set, nil, tree, ""))
	mustEqual(t, offline.Skipped, "TypeSafe judgments are off", "offline")
	noSet := evaluateTurnRun(turnRunOpts(repo, nil, turnJudge(nil), tree, ""))
	mustEqual(t, noSet.Skipped, "no rules file", "no set")

	judge := turnJudge(map[string]float64{"no-single-use-abstractions": 0.85})
	run := evaluateTurnRun(turnRunOpts(repo, set, judge, tree, "t"))
	var verdict RulesVerdict
	for _, v := range run.Verdicts {
		if v.Tool == "turn" {
			verdict = v
		}
	}
	mustEqual(t, findingIDs(verdict.Findings), []string{"no-single-use-abstractions"}, "turn finding")
}

// twin: tests/turn-rules.test.ts:222
func TestTurnSteerOneSteerCoversTheTurnVerdictAndTheMissedFiles(t *testing.T) {
	finding := RuleFinding{RuleScore: RuleScore{ID: "r", Name: "Stay inside the task", Outcome: "violation", Violation: 0.9}, Body: "The diff adds a scheduler the task did not ask for."}
	turn := RulesVerdict{Source: "typesafe", Tool: "turn", Path: turnPath, Sources: []string{"pi-warden.md"}, Asked: 1, Findings: []RuleFinding{finding}}
	other := finding
	other.ID, other.Name = "r2", "No console statements"
	file := RulesVerdict{Source: "typesafe", Tool: "edit", Path: "src/a.ts", Sources: []string{"pi-warden.md"}, Asked: 1, Findings: []RuleFinding{other}}
	steer := turnSteer([]RulesVerdict{turn, file}, nil)
	mustMatch(t, steer, `^pi-warden: the changes this run made violate a project rule: "Stay inside the task"`, "turn part")
	mustMatch(t, steer, `the change to src/a\.ts \(made by a command, not an edit\)`, "file part")
	mustEqual(t, len(strings.Split(steer, "pi-warden:")), 2, "one steer for the run")
	clean := turn
	clean.Findings = nil
	mustEqual(t, turnSteer([]RulesVerdict{clean}, nil), "", "no findings means no steer")
}
