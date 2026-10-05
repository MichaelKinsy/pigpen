package warden

import (
	"context"
	"strings"
	"time"
)

// The action guard's decision (src/guard.ts `evaluateAction`, `buildRequest` and the steer texts).

const (
	approvalThreshold    = 0.7
	previousActionsLimit = 6
	visibleThreshold     = 0.8
)

// ActionInput is one call to judge.
type ActionInput struct {
	Tool    string
	Input   map[string]any
	Cwd     string
	Task    string
	Context []TaskMessage
	Spine   *TaskSpine
	Plan    string
}

// EvaluateOptions configure EvaluateAction.
type EvaluateOptions struct {
	Config ActionConfig
	// Judge is nil for offline (pattern-only) operation.
	Judge Judge
	// RetryAfterHold asks the approval question too: the previous call of this action was held.
	RetryAfterHold bool
	// Timeout overrides Config.TimeoutMs when non-zero.
	Timeout time.Duration
	// Git runs the git state checks (default: the real git).
	Git GitRunner
}

// BuildExtras are the optional parts of a judgment request.
type BuildExtras struct {
	Approval  bool
	Context   []TaskMessage
	Plan      string
	FloorHits string
	Spine     *TaskSpine
}

// describePlan is the agent's words as they leave the machine: redacted and bounded; "" when it said nothing.
func describePlan(plan string) string {
	text := jsTrim(plan)
	if text == "" {
		return ""
	}
	return truncate(Redact(text), planLimit)
}

// BuildRequest builds the judgment request for one action: the state the judge reads and the questions.
func BuildRequest(summary ActionSummary, task string, extras BuildExtras) Request {
	plan := describePlan(extras.Plan)
	taskText := "(no user request recorded in this session)"
	if t := jsTrim(task); t != "" {
		taskText = truncate(Redact(t), taskLimit)
	}
	contextMsgs := extras.Context
	if len(contextMsgs) > 8 {
		contextMsgs = contextMsgs[len(contextMsgs)-8:]
	}
	ctxOut := make([]map[string]any, 0, len(contextMsgs))
	for _, m := range contextMsgs {
		ctxOut = append(ctxOut, map[string]any{"role": m.Role, "text": truncate(Redact(m.Text), 750)})
	}
	state := map[string]any{"task": taskText, "action": summary, "context": ctxOut}
	if sp := extras.Spine; sp != nil {
		history := []string{}
		for i, turn := range sp.History {
			if i >= spineHistoryTurns {
				break
			}
			history = append(history, truncate(Redact(turn), spineHistoryLimit))
		}
		state["spine"] = map[string]any{"goal": truncate(Redact(sp.Goal), spineGoalLimit), "task_history": history}
	}
	if plan != "" {
		state["plan"] = plan
	}
	if extras.FloorHits != "" {
		state["floor_hits"] = extras.FloorHits
	}
	sets := []Questions{buildBase(), build(qs.ShouldProceed)}
	if summary.Command != "" {
		sets = append(sets, build(qs.Visible))
	}
	if plan != "" {
		sets = append(sets, build(qs.Intent))
	}
	if extras.Approval {
		sets = append(sets, build(qs.Approval))
	}
	return Request{State: state, Questions: mergeQuestions(sets...)}
}

func buildBase() Questions { return build(qs.Base) }

var builtInIDs = func() map[string]bool {
	m := map[string]bool{}
	for _, id := range exemptableIDs {
		m[id] = true
	}
	return m
}()

func answerNoul(answers map[string]Answer, id string) (float64, bool) {
	a, ok := answers[id]
	if !ok || a.Type != "noul" {
		return 0, false
	}
	return a.Noul, true
}

func fptr(v float64) *float64 { return &v }

// EvaluateAction decides one tool call: the offline pattern floor first, the judge's answers second.
func EvaluateAction(ctx context.Context, action ActionInput, opts EvaluateOptions) Verdict {
	config, judge := opts.Config, opts.Judge
	summary := DescribeAction(action.Tool, action.Input, action.Cwd)
	guarded := false
	for _, t := range config.Tools {
		if t == action.Tool {
			guarded = true
		}
	}
	if !config.Enabled || !guarded {
		return Verdict{Level: LevelAllow, Source: "skipped", Summary: summary}
	}
	plan := describePlan(action.Plan)
	withPlan := func(v Verdict) Verdict {
		if plan != "" {
			v.Plan = plan
		}
		return v
	}
	patterns := MatchPatterns(action.Tool, action.Input, action.Cwd, &PatternOptions{CommandRules: config.CommandRules, CommandDenyRules: config.CommandDenyRules, ExemptRules: config.ExemptRules, Git: opts.Git})
	// A hit leaves the level computation only when the prompt authorizes every deletion it stands for.
	perHit := hitViolations(patterns, action.Tool, action.Input)
	var active []PatternHit
	for i, hit := range patterns {
		own := perHit[i]
		all := len(own) > 0
		for _, v := range own {
			if !authorize(action.Task, v) {
				all = false
			}
		}
		if !all {
			active = append(active, hit)
		}
	}
	var reasons []string
	level := LevelAllow
	// A shell command that merely mentions a secrets file (grep for key names, cat .env.example) is decided
	// after the judge says whether it can write; write/edit on such a path, and offline runs, warn at once.
	deferSensitive := judge != nil && action.Tool != "write" && action.Tool != "edit"
	var builtInHits []string
	hasBuiltInDestructive, hasBuiltInOther, hasDeferredSensitive, hasOutside, outsideExisting := false, false, false, false, false
	evidenceMode := config.Floor == "evidence" && judge != nil
	for _, hit := range active {
		if hit.Severity == SeverityDeny {
			level = LevelDeny
			reasons = append(reasons, orDefault(hit.Message, hit.Label))
			continue
		}
		if hit.Severity == SeveritySensitive && deferSensitive {
			hasDeferredSensitive = true
			continue
		}
		if evidenceMode && builtInIDs[hit.ID] {
			// Built-in pattern hits become evidence: listed in the request for the judge and traced,
			// but not level-setters.
			builtInHits = append(builtInHits, hit.Label+" ["+string(hit.Severity)+"]")
			reasons = append(reasons, string(hit.Severity)+": "+hit.Label+" (evidence)")
			if hit.Severity == SeverityDestructive {
				hasBuiltInDestructive = true
			} else {
				hasBuiltInOther = true
			}
			continue
		}
		if hit.Severity == SeverityDestructive {
			level = Higher(level, LevelConfirm)
		} else {
			level = Higher(level, LevelWarn)
		}
		reasons = append(reasons, string(hit.Severity)+": "+hit.Label)
	}
	if summary.Location == "outside_project" {
		exists := summary.Exists != nil && *summary.Exists
		if evidenceMode {
			note := "creates a file outside the project " + summary.Path
			if action.Tool == "write" && exists {
				note = "overwrites an existing file outside the project " + summary.Path
			}
			builtInHits = append(builtInHits, note)
			reasons = append(reasons, "outside project: "+note+" (evidence)")
			hasOutside = true
			if action.Tool == "write" && exists {
				outsideExisting = true
			}
		} else if action.Tool == "write" && exists {
			level = Higher(level, LevelConfirm)
			reasons = append(reasons, "overwrites an existing file outside the project")
		} else {
			level = Higher(level, LevelWarn)
			verb := "changes"
			if action.Tool == "write" {
				verb = "creates"
			}
			reasons = append(reasons, verb+" a file outside the project")
		}
	}
	// A deny-level pattern hit blocks the call immediately; no judge, no dialog.
	if level == LevelDeny {
		return withPlan(Verdict{Level: level, Source: "pattern", Summary: summary, Patterns: patterns, Reasons: reasons})
	}
	view, hasView := CommandOf(action.Tool, action.Input)
	if hasView && view.Shell && len(patterns) == 0 && IsReadOnlyCommand(view.Command) {
		return Verdict{Level: level, Source: "read-only", Summary: summary, Patterns: patterns, Reasons: reasons}
	}
	if judge == nil {
		return withPlan(Verdict{Level: level, Source: "pattern", Summary: summary, Patterns: patterns, Reasons: reasons})
	}
	floorHits := "none"
	if len(builtInHits) > 0 {
		floorHits = strings.Join(builtInHits, "; ")
	}
	request := BuildRequest(summary, action.Task, BuildExtras{Approval: opts.RetryAfterHold, Context: action.Context, Plan: action.Plan, FloorHits: floorHits, Spine: action.Spine})
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = time.Duration(config.TimeoutMs) * time.Millisecond
	}
	result := Ask(ctx, judge, request, timeout)
	if !result.OK {
		switch {
		case !config.FailOpen:
			level = Higher(level, LevelConfirm)
			reasons = append(reasons, "TypeSafe unavailable and failOpen is false")
		case evidenceMode && (hasBuiltInDestructive || hasBuiltInOther || hasDeferredSensitive || hasOutside):
			// The judge failed in evidence mode: the floor applies as in level mode.
			if hasBuiltInDestructive || outsideExisting {
				level = Higher(level, LevelConfirm)
			} else {
				level = Higher(level, LevelWarn)
			}
			reasons = append(reasons, "TypeSafe unavailable; built-in patterns decide")
		default:
			reasons = append(reasons, "TypeSafe unavailable; allowed by failOpen")
		}
		return withPlan(Verdict{Level: level, Source: "error", Summary: summary, Patterns: patterns, Reasons: reasons, Error: result.Error, ErrorCode: result.ErrorCode})
	}
	answers := result.Answers
	j := &Judgment{Scope: answers["scope"].Choice, ScopeConfidence: answers["scope"].Confidence, Model: result.Model, ElapsedMs: result.ElapsedMs}
	j.Irreversible = answers["irreversible"].Noul
	j.OffTask = answers["off_task"].Noul
	if v, ok := answerNoul(answers, "approved"); ok {
		j.Approved = fptr(v)
	}
	if v, ok := answerNoul(answers, "mutates"); ok {
		j.Mutates = fptr(v)
	}
	if v, ok := answerNoul(answers, "intent_mismatch"); ok && plan != "" {
		j.IntentMismatch = fptr(v)
	}
	if v, ok := answerNoul(answers, "visible"); ok && summary.Command != "" {
		j.Visible = fptr(v)
	}
	if deferSensitive {
		for _, hit := range patterns {
			if hit.Severity != SeveritySensitive {
				continue
			}
			mutates := 1.0
			if j.Mutates != nil {
				mutates = *j.Mutates
			}
			if mutates >= 0.5 {
				level = Higher(level, LevelWarn)
				reasons = append(reasons, string(hit.Severity)+": "+hit.Label)
			} else {
				reasons = append(reasons, hit.Label+" (read-only, not warned)")
			}
		}
	}
	// write/edit always change something; a command the judge reads as read-only is warned about, never held.
	canChange := summary.Tool == "write" || summary.Tool == "edit" || j.Mutates == nil || *j.Mutates >= 0.5
	if j.Irreversible >= config.Irreversible.Confirm {
		level = Higher(level, LevelConfirm)
		reasons = append(reasons, "irreversible "+percent(j.Irreversible))
	} else if j.Irreversible >= config.Irreversible.Warn {
		level = Higher(level, LevelWarn)
		reasons = append(reasons, "possibly irreversible "+percent(j.Irreversible))
	}
	// Off-task never holds. Scope gates it: the categorical answer vetoes or overrides the score, which alone
	// has no signal. Off-task steers stay trace-only until the score is calibrated.
	offTaskSteer, offTaskTraceOnly := false, false
	offTaskIdx := -1
	addOffTask := func(reason string) {
		offTaskIdx = len(reasons)
		reasons = append(reasons, reason)
	}
	switch j.Scope {
	case "expected_step":
		// A required step; the off-task score is noise.
	case "unrelated":
		offTaskSteer = canChange
		offTaskTraceOnly = true
		level = Higher(level, LevelWarn)
		if canChange {
			addOffTask("off-task " + percent(j.OffTask) + " (unrelated to the request; trace-only until AUC clears 0.51)")
		} else {
			addOffTask("off-task " + percent(j.OffTask) + " (unrelated, but read-only; trace-only)")
		}
	case "plausible_side_step":
		offTaskTraceOnly = true
		level = Higher(level, LevelWarn)
		addOffTask("off-task " + percent(j.OffTask) + " (plausible side step; trace-only)")
	case "unclear":
		// Missing context is not itself off-task evidence.
	default:
		// No scope answer: fall back to the score, trace-only.
		if j.OffTask >= config.OffTask.Steer {
			offTaskSteer = canChange
			offTaskTraceOnly = true
			level = Higher(level, LevelWarn)
			addOffTask("off-task " + percent(j.OffTask) + " (trace-only until AUC clears 0.51)")
		} else if j.OffTask >= config.OffTask.Warn {
			offTaskTraceOnly = true
			level = Higher(level, LevelWarn)
			addOffTask("off-task " + percent(j.OffTask) + " (trace-only)")
		}
	}
	// A call at odds with the agent's own plan is warned about and the agent told; never held on that alone.
	// A visible action (commit, push, merge, publish, launch) needs less mismatch to be flagged.
	visible := 0.0
	if j.Visible != nil {
		visible = *j.Visible
	}
	visibleDrift := j.IntentMismatch != nil && visible >= visibleThreshold && *j.IntentMismatch >= config.VisibleMismatch
	mismatch := j.IntentMismatch != nil && canChange && (*j.IntentMismatch >= config.IntentMismatch || visibleDrift)
	visibleEffect := (hasView && view.Shell && IsVisibleCommand(view.Command)) || visible >= visibleThreshold
	intentTraceOnly := mismatch && (config.IntentTraceOnly == "all" || (config.IntentTraceOnly == "invisible" && !visibleEffect))
	intentIdx := -1
	if mismatch {
		level = Higher(level, LevelWarn)
		if intentTraceOnly {
			intentIdx = len(reasons)
		}
		traceOnly := ""
		if intentTraceOnly {
			traceOnly = "; trace-only"
			if config.IntentTraceOnly == "invisible" {
				traceOnly += ", no visible effect"
			}
		}
		if visibleDrift && *j.IntentMismatch < config.IntentMismatch {
			reasons = append(reasons, "intent mismatch "+percent(*j.IntentMismatch)+" on a visible action ("+percent(visible)+"; a commit, push, merge, publish, or launch the plan did not describe"+traceOnly+")")
		} else {
			reasons = append(reasons, "intent mismatch "+percent(*j.IntentMismatch)+" (the call differs from the agent's stated plan"+traceOnly+")")
		}
	}
	// Poor calibration makes should-proceed diagnostic-only unless the user opts into steers.
	shouldProceedSteer := false
	spIdx := -1
	if v, ok := answerNoul(answers, "should_proceed"); ok {
		j.ShouldProceed = fptr(v)
		if v <= config.ShouldProceed.Threshold {
			shouldProceedSteer = true
			level = Higher(level, LevelWarn)
			text := "may need user input before continuing"
			if !config.ShouldProceed.Steer {
				spIdx = len(reasons)
				text = "trace-only until calibrated"
			}
			reasons = append(reasons, "should-proceed "+percent(v)+" ("+text+")")
		}
	}
	verdict := withPlan(Verdict{Level: level, Source: "typesafe", Summary: summary, Patterns: patterns, Reasons: reasons, Judgment: j})
	verdict.IntentMismatch = mismatch
	if intentIdx >= 0 {
		verdict.IntentTraceOnly = true
		verdict.IntentTraceOnlyReasonIndex = intentIdx
	}
	verdict.OffTaskSteer = offTaskSteer
	verdict.OffTaskTraceOnly = offTaskTraceOnly
	verdict.ShouldProceedSteer = shouldProceedSteer
	if spIdx >= 0 {
		verdict.ShouldProceedTraceOnly = true
		verdict.ShouldProceedTraceOnlyReasonIndex = spIdx
	}
	if offTaskIdx >= 0 {
		verdict.OffTaskTraceOnlyReasonIndex = offTaskIdx
	}
	if level == LevelConfirm && j.Approved != nil && *j.Approved >= approvalThreshold {
		verdict.Level = LevelAllow
		verdict.ApprovedByUser = true
		verdict.Reasons = append([]string{"user approved in the latest message (" + percent(*j.Approved) + ")"}, reasons...)
		if offTaskIdx >= 0 {
			verdict.OffTaskTraceOnlyReasonIndex++
		}
		if spIdx >= 0 {
			verdict.ShouldProceedTraceOnlyReasonIndex++
		}
		if intentIdx >= 0 {
			verdict.IntentTraceOnlyReasonIndex++
		}
	}
	return verdict
}

// ---------------------------------------------------------------------------
// What the agent reads.

// IntentSteer is what the agent reads after a call that differs from its own plan ran: name the gap and
// bound the answer to one line, so the model does not write an accounting of every notice.
func IntentSteer(v Verdict) string {
	score := ""
	visible := ""
	if j := v.Judgment; j != nil {
		if j.IntentMismatch != nil {
			score = " (intent mismatch " + percent(*j.IntentMismatch) + ")"
		}
		if j.Visible != nil && *j.Visible >= visibleThreshold {
			visible = " and its effect is visible outside the working tree (a commit, push, merge, publish, or launched program)"
		}
	}
	return "pi-warden: this " + v.Summary.Tool + " call does something different from what you said you were about to do" + score + visible + ". It ran. Do not write a report about this notice: in your next message, name what changed and why in at most one short sentence, then continue the task (or make the described call if it is still needed). If you already accounted for a similar notice, say nothing more about it."
}

// ShouldProceedMessage is what the agent reads when should_proceed is low: pause and ask the user.
func ShouldProceedMessage(v Verdict) string {
	score := ""
	if v.Judgment != nil && v.Judgment.ShouldProceed != nil {
		score = " (should-proceed " + percent(*v.Judgment.ShouldProceed) + ")"
	}
	return "pi-warden: this " + v.Summary.Tool + " call may need user input before it runs" + score + ". Pause, explain what you are about to do and why, and wait for the user's approval before continuing."
}

// OffTaskSteer is what the agent reads after an unrelated change ran.
func OffTaskSteer(v Verdict) string {
	score := ""
	if v.Judgment != nil {
		score = " (off-task " + percent(v.Judgment.OffTask) + ")"
	}
	return "pi-warden: this " + v.Summary.Tool + " call looks unrelated to the user's request" + score + ". It ran. If it serves the request, say how in at most one short sentence; otherwise return to what the user asked for, or ask before widening the work. Do not restate session state or re-answer notices you have already addressed."
}

var (
	reApprove = lazyRE(`(?i)\b(?:yes|yep|yeah|go ahead|do it|proceed|approved?|confirm(?:ed)?|ok(?:ay)?|sure|please do|run it)\b`)
	reDecline = lazyRE(`(?i)\b(?:no|don't|do not|stop|wait|instead|not)\b`)
)

// TextApproves is the offline stand-in for the approval question when no judge is available.
func TextApproves(task string) bool {
	return reApprove.MatchString(task) && !reDecline.MatchString(task)
}

// SteerReason is the text the agent receives when a call is held. It explains the judgment and the two
// acceptable next moves, so the model re-plans instead of retrying. It contains no command text and no secrets.
func SteerReason(v Verdict, canApprove bool) string {
	lines := []string{
		"pi-warden held this " + v.Summary.Tool + " call before it ran: " + strings.Join(v.Reasons, "; ") + ".",
		"Do not retry it unchanged. Either (1) reach the goal with a recoverable alternative that stays inside the project (a targeted path, a dry run, a move instead of a delete, a normal push), or (2) if this exact action is genuinely required, stop and tell the user in one or two sentences what it does, what cannot be undone, and why it is needed, then wait for their reply.",
	}
	if canApprove {
		lines = append(lines, "If the user's reply approves it, retry the same call and pi-warden will let it through.")
	} else {
		lines = append(lines, "pi-warden allows the same call again once the user has replied with approval.")
	}
	return strings.Join(lines, " ")
}

var (
	reDigits    = lazyRE(`\d+(?:\.\d+)?`)
	reSpaceRuns = lazyRE(`\s+`)
)

// SteerFingerprint is a notice with scores, counts and whitespace removed: what it actually says.
func SteerFingerprint(content string) string {
	return jsTrim(reSpaceRuns.ReplaceAllString(reDigits.ReplaceAllString(content, "#"), " "))
}

// SteerRepeatWindow collapses repeated notices: the same notice with only a score changed carries no new
// information, but each copy makes the model write another accounting paragraph.
type SteerRepeatWindow struct {
	recent []string
	window int
}

// NewSteerRepeatWindow keeps the last three fingerprints.
func NewSteerRepeatWindow() *SteerRepeatWindow { return &SteerRepeatWindow{window: 3} }

// Seen is true when this normalised text was already sent inside the window; the text is recorded either way.
func (w *SteerRepeatWindow) Seen(content string) bool {
	fp := SteerFingerprint(content)
	repeat := false
	for _, r := range w.recent {
		if r == fp {
			repeat = true
		}
	}
	w.recent = append(w.recent, fp)
	if len(w.recent) > w.window {
		w.recent = w.recent[1:]
	}
	return repeat
}

// Reset forgets the window.
func (w *SteerRepeatWindow) Reset() { w.recent = nil }
