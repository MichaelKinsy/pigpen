package pi_goal_x_test

// Extra tests (not twins) that pin behavior of the original which the twins left unasserted; each test names the mutants of
// port/mutations.json (lane B) it exists for. Expected values come from the original's code (port/oracle), not from the Go code.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

var mbAt = goalAt(2026, time.August, 4, 9, 0, 0)

func mbRig(t *testing.T, objective, overrides string, opts rigOptions) (*goalRig, toolsFixture) {
	t.Helper()
	f := newToolsFixture(t, objective, false, mbAt, overrides)
	r := newToolsRig(t, f, opts)
	r.startToolsSession()
	return r, f
}

func mbGoalID(f toolsFixture) string { return f.goal["id"].(string) }

// get-verbose-threshold-legacy, get-history-flag-verbose, get-mode-label
func TestMutantsBGetGoalProfiles(t *testing.T) {
	t.Run("legacy-v1", mbLegacyProfile)
	t.Run("compact-v2", mbCompactProfile)
}

func mbLegacyProfile(t *testing.T) {
	f := newToolsFixture(t, "Profile goal", false, mbAt, "")
	goal.ToolsAppendLedger(f.cwd, `{"type":"audit_result","goalId":"`+mbGoalID(f)+`","verdict":"disapproved","report":"Missing X","at":"2026-08-04T09:30:15.000Z"}`)
	r := newToolsRig(t, f, rigOptions{Env: map[string]string{"PI_GOAL_PROMPT_PROFILE": "legacy-v1"}})
	r.startToolsSession()
	text := r.callTool("get_goal", map[string]any{}).Content
	textContains(t, text, "Goal "+mbGoalID(f)+": running, regular\n", "first line names the mode")
	textContains(t, text, "Mode: regular\n", "mode line")
	textContains(t, text, "Lifecycle:", "legacy-v1 is verbose by default")
	textContains(t, text, "\n\nLast audit: disapproved (2026-08-04) \u2014 Missing X\nRecent events:\n  09:30:15 audit_result", "verbose includes the history block")

}

func mbCompactProfile(t *testing.T) {
	// compact-v2: verbose:true includes the history without include_history.
	f2 := newToolsFixture(t, "Profile goal", false, mbAt, "")
	goal.ToolsAppendLedger(f2.cwd, `{"type":"audit_result","goalId":"`+mbGoalID(f2)+`","verdict":"disapproved","report":"Missing X","at":"2026-08-04T09:30:15.000Z"}`)
	r2 := newToolsRig(t, f2, rigOptions{})
	r2.startToolsSession()
	concise := r2.callTool("get_goal", map[string]any{}).Content
	textNotContains(t, concise, "Recent events:", "concise default has no history")
	textContains(t, concise, "Goal "+mbGoalID(f2)+": running, regular", "concise first line")
	verbose := r2.callTool("get_goal", map[string]any{"verbose": true}).Content
	textContains(t, verbose, "Recent events:\n  09:30:15 audit_result", "verbose carries the history")
	withHistory := r2.callTool("get_goal", map[string]any{"include_history": true}).Content
	textContains(t, withHistory, "\nLast audit: disapproved (2026-08-04) \u2014 Missing X\nRecent events:\n  09:30:15 audit_result", "include_history adds the block to the concise read")
	textNotContains(t, withHistory, "Lifecycle:", "include_history stays concise")
}

// get-section-skip-summary, get-unknown-section-text, get-history-events-only-history, get-page-cursor-key, get-detail-needs-section
func TestMutantsBGetGoalSections(t *testing.T) {
	f := newToolsFixture(t, "Section goal objective", false, mbAt, "")
	id := mbGoalID(f)
	goal.ToolsAppendLedger(f.cwd, `{"type":"audit_result","goalId":"`+id+`","verdict":"disapproved","report":"Missing X","at":"2026-08-04T09:30:15.000Z"}`)
	r := newToolsRig(t, f, rigOptions{})
	r.startToolsSession()

	summary := r.callTool("get_goal", map[string]any{"section": "summary"}).Content
	textContains(t, summary, "Goal "+id+": running, regular", "section=summary is the compact read")
	equal(t, r.callTool("get_goal", map[string]any{"section": "bogus"}).Content, "Unknown goal section.", "unknown section")

	history := r.callTool("get_goal", map[string]any{"section": "history"})
	textContains(t, history.Content, "audit_result", "the history page lists the ledger events")
	obj := r.callTool("get_goal", map[string]any{"section": "objective"})
	textContains(t, obj.Content, "Section goal objective", "the objective page")
	page, _ := obj.Details["page"].(map[string]any)
	equal(t, page["totalChars"], len("Section goal objective"), "page.totalChars is the section length")
	equal(t, page["content"], "Section goal objective", "page.content")

	detail := "Use section=objective, tasks, or history for detail retrieval; task_id requires tasks."
	equal(t, r.callTool("get_goal", map[string]any{"task_id": "t1"}).Content, detail, "task_id without a section")
	equal(t, r.callTool("get_goal", map[string]any{"cursor": "abc"}).Content, detail, "cursor without a section")
}

// get-objective-truncate-180, get-objective-trunc-width, get-contract-trunc-240, get-task-trunc-180, get-current-task-line,
// get-blocker-status, get-verbose-blocker-status, get-verbose-usage-seconds
func TestMutantsBGetGoalLines(t *testing.T) {
	more := " (full: get_goal section=objective)"
	line := func(text, prefix string) string {
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		return ""
	}
	for _, c := range []struct {
		n    int
		want string
	}{
		{180, strings.Repeat("x", 180)},
		{181, strings.Repeat("x", 177) + "..." + more},
		{250, strings.Repeat("x", 177) + "..." + more},
	} {
		r, _ := mbRig(t, strings.Repeat("x", c.n), "", rigOptions{})
		equal(t, line(r.callTool("get_goal", map[string]any{}).Content, "Objective: "), "Objective: "+c.want, "objective of "+strconv.Itoa(c.n))
	}

	tasks := `{"currentTaskId":"t1","taskList":{"tasks":[{"id":"t1","title":"` + strings.Repeat("T", 300) + `","status":"pending","verificationContract":"` + strings.Repeat("c", 300) + `"}],"blockCompletion":false,"proposedAt":"` + nowZ() + `"}}`
	r, _ := mbRig(t, "Task lines", tasks, rigOptions{})
	text := r.callTool("get_goal", map[string]any{}).Content
	equal(t, line(text, "Current task: "), "Current task: t1 \u2014 "+strings.Repeat("T", 177)+"... \u2014 contract: "+strings.Repeat("c", 237)+"...", "current task title and contract caps")

	r, _ = mbRig(t, "Bare current task", "", rigOptions{})
	r.Probe.SetGoalField("currentTaskId", "t9")
	equal(t, line(r.callTool("get_goal", map[string]any{}).Content, "Current task"), "Current task: t9", "current task id without a task list")

	blocked := `{"status":"blocked","pauseReason":"Need the API key","pauseSuggestedAction":"Ask the user","usage":{"tokensUsed":0,"activeSeconds":1}}`
	r, _ = mbRig(t, "Blocked goal", blocked, rigOptions{})
	equal(t, line(r.callTool("get_goal", map[string]any{}).Content, "Blocker: "), "Blocker: Need the API key", "concise blocker line for a blocked goal")
	v := r.callTool("get_goal", map[string]any{"verbose": true}).Content
	textContains(t, v, "\nBlocker: Need the API key\nSuggested action: Ask the user\n", "verbose blocker and suggestion for a blocked goal")
	textContains(t, v, "Cumulative goal usage (not context occupancy): 1s\n", "one active second is shown")
}

// get-nogoal-other-count, get-nogoal-text, get-other-goals-line
func TestMutantsBGetGoalPool(t *testing.T) {
	f := newEmptyToolsFixture(t)
	r := newToolsRig(t, f, rigOptions{})
	r.startToolsSession()
	equal(t, r.callTool("get_goal", map[string]any{}).Content,
		"No goal is set in this session. Call create_goal with the objective when the user explicitly asks to start a persistent goal.", "no goal at all")

	// One open goal that the session does not focus.
	f = newEmptyToolsFixture(t)
	goal.ToolsGoal(f.cwd, "Unfocused", false, mbAt, "")
	r = newToolsRig(t, f, rigOptions{})
	r.startToolsSession()
	equal(t, r.callTool("get_goal", map[string]any{}).Content,
		"No goal is focused in this session. 1 open goal exist in the goal pool. Use /goal-focus to choose the session focus before doing goal work."+
			"\n\nCall create_goal with the objective to create and focus a new goal, or ask the user to run /goal-focus to choose an open goal.", "one unfocused goal")

	// A focused goal and one other open goal.
	f = newToolsFixture(t, "Focused", false, mbAt, "")
	goal.ToolsGoal(f.cwd, "Other", false, mbAt+1000, "")
	r = newToolsRig(t, f, rigOptions{})
	r.startToolsSession()
	v := r.callTool("get_goal", map[string]any{"verbose": true}).Content
	textContains(t, v, "\nOther open goals: 1 (user can run /goal-list or /goal-focus)\n", "other open goals line")
}

// create-trim, create-empty-error, create-mode-sisyphus, create-contract-extracted, create-autocontinue, create-other-plural,
// create-other-line, create-bootstrap, create-terminate, create-derived-only-without-tasklist
func TestMutantsBCreateGoal(t *testing.T) {
	f := newEmptyToolsFixture(t)
	r := newToolsRig(t, f, rigOptions{})
	r.startToolsSession()
	if msg := r.callToolErr("create_goal", map[string]any{"objective": " \n\t "}); !strings.Contains(msg, "create_goal requires a non-empty objective.") {
		t.Errorf("a blank objective is refused, got %q", msg)
	}
	out := r.callTool("create_goal", map[string]any{"objective": "  \nShip the thing\nVerification contract: Run the suite (0 failures).\n  "})
	if !out.terminated() {
		t.Error("create_goal ends the turn")
	}
	g := r.Probe.Goal()
	equal(t, g["objective"], "Ship the thing", "the objective is trimmed and its contract line extracted")
	equal(t, g["verificationContract"], "Run the suite (0 failures).", "the contract is kept on the record")
	equal(t, g["sisyphus"], false, "no mode is regular mode")
	equal(t, g["autoContinue"], true, "a created goal auto-continues")

	// Another goal exists: singular wording and the focus sentence.
	out = r.callTool("create_goal", map[string]any{"objective": "Second\n1. First step\n2. Second step"})
	textContains(t, out.Content, "\n1 other open goal remain in .pi/goals \u2014 this goal is now the session focus.", "one other goal")
	textContains(t, out.Content, "\n\nThe objective contains 2 ordered steps; propose them as the task tree with set_goal_tasks if the user wants tracked milestones.", "bootstrap guidance")
	out = r.callTool("create_goal", map[string]any{"objective": "Third", "mode": "sisyphus"})
	textContains(t, out.Content, "\n2 other open goals remain in .pi/goals \u2014 this goal is now the session focus.", "two other goals")
	equal(t, r.Probe.Goal()["sisyphus"], true, "sisyphus mode")
}

// update-exclusive-xor, update-mix-reason, update-mix-summary
func TestMutantsBUpdateGoalExclusive(t *testing.T) {
	r, _ := mbRig(t, "Exclusive goal", "", rigOptions{})
	want := "Provide exactly one lifecycle status or continuation, without mixing their fields."
	equal(t, r.callTool("update_goal", map[string]any{}).Content, want, "neither status nor continuation")
	equal(t, r.callTool("update_goal", map[string]any{"status": "paused", "continuation": map[string]any{"kind": "ready"}}).Content, want, "both")
	for _, extra := range []map[string]any{{"reason": "why"}, {"completion_summary": "done"}, {"suggested_action": "go"}, {"attempted_actions": []any{"a"}}} {
		params := map[string]any{"continuation": map[string]any{"kind": "ready"}}
		for k, v := range extra {
			params[k] = v
		}
		equal(t, r.callTool("update_goal", params).Content, want, "continuation mixed with "+jsonOf(extra))
	}
}

// blocked-reason-trim, blocked-reason-required, mutation-message-default, pause-detail, pause-account-progress
func TestMutantsBBlockedAndPause(t *testing.T) {
	required := `update_goal({ status: "blocked" }) requires a "reason" describing the concrete blocker. The goal remains active.`
	r, f := mbRig(t, "Blocked flow", "", rigOptions{})
	equal(t, r.callTool("update_goal", map[string]any{"status": "blocked"}).Content, required, "no reason")
	equal(t, r.callTool("update_goal", map[string]any{"status": "blocked", "reason": " \n "}).Content, required, "blank reason")
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "active", "the goal stays active")
	restore := r.Probe.RejectMutations("")
	rejected := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "r"}).Content
	equal(t, rejected, "Goal blocked state update failed: the state mutation was rejected. The goal was NOT marked blocked. Retry after resolving the conflict.", "default rejection text")
	restore()
	out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "  Need the key \n"})
	if !out.terminated() {
		t.Error("blocking ends the turn")
	}
	g := goal.ToolsActiveGoal(f.cwd)
	equal(t, g["status"], "blocked", "blocked")
	equal(t, g["pauseReason"], "Need the key", "the reason is trimmed")

	// pause: details and the progress charged before the pause.
	f2 := newToolsFixture(t, "Pause flow", false, mbAt, "")
	r2 := newToolsRig(t, f2, rigOptions{})
	fc := r2.Probe.FakeClock(mbAt)
	r2.startToolsSession()
	fc.Tick(30_000)
	out = r2.callTool("update_goal", map[string]any{"status": "paused", "reason": " Waiting for review ", "suggested_action": " Ping the owner "})
	equal(t, out.Details["resultDetail"], "Pause reason: Waiting for review\nSuggested action: Ping the owner", "pause details")
	if !out.terminated() {
		t.Error("pausing ends the turn")
	}
	textContains(t, out.Content, "Goal paused by the agent: Waiting for review. Suggested next step: Ping the owner Stop now;", "pause text")
	g = goal.ToolsActiveGoal(f2.cwd)
	equal(t, g["usage"].(map[string]any)["activeSeconds"], 30, "the active time up to the pause is charged")
}

var mbOracleModels = func(method string, args map[string]any) (any, string) {
	if method == "getModelRegistryState" {
		return map[string]any{"models": []any{map[string]any{"provider": "p", "id": "m"}}, "providers": map[string]any{"p": map[string]any{"configured": true}}}, ""
	}
	return map[string]any{}, ""
}

func mbSettingsOracle(extra string) string {
	return `{"maxAutonomousRuns":0,"oracle":{"enabled":true,"provider":"p","model":"m","maxFailedAttemptsPerBlocker":2` + extra + `}}`
}

func mbAdvice(diagnosis, title, disposition string) string {
	return `{"diagnosis":"` + diagnosis + `","alternatives":[{"title":"` + title + `","rationale":"why","steps":["one","two"],"expectedEvidence":["ev"]}],"recommendedIndex":0,"unresolvedQuestions":[],"disposition":"` + disposition + `"}`
}

func mbLedger(cwd, typ string) []map[string]any { return eventsOfType(cwd, typ) }

// oracle-* mutants of runGoalBlockedFlow, update-attempted-only-strings
func TestMutantsBOracleConsult(t *testing.T) {
	goal.RigDisarmOracle()
	t.Cleanup(goal.RigDisarmOracle)
	f := newToolsFixture(t, "Oracle goal", false, mbAt, "")
	r := newToolsRig(t, f, rigOptions{Settings: mbSettingsOracle(""), OnCall: mbOracleModels})
	r.startToolsSession()
	reason := strings.Repeat("r", 1500)
	var prompt string
	r.Probe.SetOracleSession(func(p string, submit func(string)) error {
		prompt = p
		submit(mbAdvice(strings.Repeat("d", 800), strings.Repeat("t", 150), "actionable"))
		return nil
	})
	attempted := []any{"a1", 7, "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10"}
	out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": reason, "attempted_actions": attempted})
	if out.terminated() {
		t.Error("actionable advice does not block")
	}
	textContains(t, out.Content, "Oracle diagnosis: "+strings.Repeat("d", 800)+"\nRecommended alternative: "+strings.Repeat("t", 150)+" \u2014 why\n", "the advice is relayed")
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "active", "the goal stays active after actionable advice")
	var items []string
	for _, l := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(l, "- ") {
			items = append(items, l)
		}
	}
	equal(t, items, []string{"- a1", "- a2", "- a3", "- a4", "- a5", "- a6", "- a7", "- a8"}, "only the first eight string actions reach the Oracle")
	started := mbLedger(f.cwd, "oracle_started")
	if len(started) != 1 || len(started[0]["reason"].(string)) != 1000 {
		t.Fatalf("oracle_started with the reason cut to 1000 chars: %v", started)
	}
	result := mbLedger(f.cwd, "oracle_result")
	if len(result) != 1 {
		t.Fatalf("oracle_result: %v", result)
	}
	equal(t, len(result[0]["summary"].(string)), 500, "the summary is cut to 500 chars")
	equal(t, result[0]["recommendedTitle"], strings.Repeat("t", 150), "the recommended title")
	if !goal.RigOraclePending(mbGoalID(f)) {
		t.Error("the advice is armed for a follow-up")
	}

	// While the advice is still armed, a repeated block of the same blocker is committed (the reminder is only re-issued once).
	again := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": reason})
	if !again.terminated() {
		t.Error("the block is committed while the advice is armed")
	}
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "blocked", "blocked")
}

func TestMutantsBOracleFailures(t *testing.T) {
	goal.RigDisarmOracle()
	t.Cleanup(goal.RigDisarmOracle)
	f := newToolsFixture(t, "Oracle failures", false, mbAt, "")
	r := newToolsRig(t, f, rigOptions{Settings: mbSettingsOracle(""), OnCall: mbOracleModels})
	r.startToolsSession()
	r.Probe.SetOracleSession(func(string, func(string)) error { return errString(strings.Repeat("e", 300)) })
	reason := "same blocker"
	first := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": reason})
	equal(t, first.Content, "Oracle consultation failed (provider): "+strings.Repeat("e", 200)+" The goal remains active; try again or continue working.", "first failure may be retried")
	failed := mbLedger(f.cwd, "oracle_failed")
	equal(t, len(failed), 1, "one failure recorded")
	equal(t, len(failed[0]["message"].(string)), 300, "the recorded message is cut to 300 chars")
	// The second failure reaches the limit of 2: no retry hint.
	second := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": reason})
	equal(t, second.Content, "Oracle consultation failed (provider): "+strings.Repeat("e", 200), "the last allowed failure carries no retry hint")
	// At the limit the block goes through with a durable annotation.
	consulted := false
	r.Probe.SetOracleSession(func(string, func(string)) error { consulted = true; return nil })
	third := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": reason})
	if consulted {
		t.Error("no consultation once the failure limit is reached")
	}
	if !third.terminated() {
		t.Error("the block is committed at the failure limit")
	}
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "blocked", "blocked at the limit")
	annotations := mbLedger(f.cwd, "oracle_failed")
	last := annotations[len(annotations)-1]
	equal(t, last["message"], "failure limit reached; blocking with durable Oracle failure annotation", "annotation")
	equal(t, last["errorCode"], "provider", "the last failure's code")
}

type errString string

func (e errString) Error() string { return string(e) }

func TestMutantsBOracleDispositions(t *testing.T) {
	goal.RigDisarmOracle()
	t.Cleanup(goal.RigDisarmOracle)
	for _, disposition := range []string{"needs_human", "insufficient_context"} {
		f := newToolsFixture(t, "Oracle "+disposition, false, mbAt, "")
		r := newToolsRig(t, f, rigOptions{Settings: mbSettingsOracle(""), OnCall: mbOracleModels})
		r.startToolsSession()
		r.Probe.SetOracleSession(func(_ string, submit func(string)) error {
			submit(mbAdvice("Cannot proceed", "Ask the owner", disposition))
			return nil
		})
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "need " + disposition})
		if !out.terminated() {
			t.Errorf("%s blocks the goal", disposition)
		}
		equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "blocked", disposition+": blocked")
		if goal.RigOraclePending(mbGoalID(f)) {
			t.Errorf("%s: the follow-up marker is consumed when the goal is blocked", disposition)
		}
	}
	// A recorded needs_human / insufficient_context result blocks without consulting.
	for _, disposition := range []string{"needs_human", "insufficient_context"} {
		f := newToolsFixture(t, "Recorded "+disposition, false, mbAt, "")
		r := newToolsRig(t, f, rigOptions{Settings: mbSettingsOracle(""), OnCall: mbOracleModels})
		r.startToolsSession()
		fp := r.Probe.RigBlockerFingerprint("recorded")
		goal.ToolsAppendLedger(f.cwd, `{"type":"oracle_result","goalId":"`+mbGoalID(f)+`","fingerprint":"`+fp+`","adviceId":"a1","disposition":"`+disposition+`","summary":"s","at":"2026-08-04T09:30:00.000Z"}`)
		consulted := false
		r.Probe.SetOracleSession(func(string, func(string)) error { consulted = true; return nil })
		out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "recorded"})
		if consulted || !out.terminated() {
			t.Errorf("%s: a recorded result blocks without a new consultation (consulted=%v terminated=%v)", disposition, consulted, out.terminated())
		}
	}
	// A recorded actionable result: refused until attempted; once attempted the block is committed.
	f := newToolsFixture(t, "Recorded actionable", false, mbAt, "")
	r := newToolsRig(t, f, rigOptions{Settings: mbSettingsOracle(""), OnCall: mbOracleModels})
	r.startToolsSession()
	fp := r.Probe.RigBlockerFingerprint("recorded")
	goal.ToolsAppendLedger(f.cwd, `{"type":"oracle_result","goalId":"`+mbGoalID(f)+`","fingerprint":"`+fp+`","adviceId":"a1","disposition":"actionable","summary":"Try X","at":"2026-08-04T09:30:00.000Z"}`)
	refused := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "recorded"})
	textContains(t, refused.Content, "Try X\n(The same blocker fingerprint already received Oracle advice; no new consultation will run until you attempt it.)\n\nThe goal was NOT marked blocked.", "re-armed reminder")
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "active", "refused")
	goal.RigDisarmOracle()
	goal.ToolsAppendLedger(f.cwd, `{"type":"oracle_followup_attempted","goalId":"`+mbGoalID(f)+`","fingerprint":"`+fp+`","adviceId":"a1","firstToolName":"bash","at":"2026-08-04T09:31:00.000Z"}`)
	committed := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "recorded"})
	if !committed.terminated() {
		t.Error("after the follow-up attempt the block is committed")
	}
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "blocked", "blocked after the attempt")
}

// oracle-focus-check
func TestMutantsBOracleFocusChange(t *testing.T) {
	goal.RigDisarmOracle()
	t.Cleanup(goal.RigDisarmOracle)
	f := newToolsFixture(t, "Oracle focus", false, mbAt, "")
	r := newToolsRig(t, f, rigOptions{Settings: mbSettingsOracle(""), OnCall: mbOracleModels})
	r.startToolsSession()
	r.Probe.SetOracleSession(func(_ string, submit func(string)) error {
		r.Probe.RigUnfocusInsideRun()
		submit(mbAdvice("late", "Late", "actionable"))
		return nil
	})
	out := r.callTool("update_goal", map[string]any{"status": "blocked", "reason": "focus moves"})
	textContains(t, out.Content, "Blocker Oracle", "the consultation is cancelled when the focus moved")
	textNotContains(t, out.Content, "Oracle diagnosis", "the advice of a cancelled consultation is dropped")
	if len(mbLedger(f.cwd, "oracle_result")) != 0 {
		t.Error("no oracle_result for a cancelled consultation")
	}
}

var _ = regexp.MustCompile
