package warden

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// agentReasons drops the reasons that are trace-only: they stay in the trace, the agent is not told
// (extension.ts agentDeliveryReasons).
func agentReasons(v Verdict) []string {
	drop := map[int]bool{}
	if v.OffTaskTraceOnly && v.OffTaskTraceOnlyReasonIndex >= 0 {
		drop[v.OffTaskTraceOnlyReasonIndex] = true
	}
	if v.ShouldProceedTraceOnly && v.ShouldProceedTraceOnlyReasonIndex >= 0 {
		drop[v.ShouldProceedTraceOnlyReasonIndex] = true
	}
	if v.IntentTraceOnly && v.IntentTraceOnlyReasonIndex >= 0 {
		drop[v.IntentTraceOnlyReasonIndex] = true
	}
	var out []string
	for i, r := range v.Reasons {
		if !drop[i] {
			out = append(out, r)
		}
	}
	return out
}

// deliveryVerdict is the verdict with the trace-only reasons and steer flags removed.
func deliveryVerdict(v Verdict) Verdict {
	d := v
	d.Reasons = agentReasons(v)
	if v.OffTaskTraceOnly {
		d.OffTaskSteer = false
	}
	if v.ShouldProceedTraceOnly {
		d.ShouldProceedSteer = false
	}
	if v.IntentTraceOnly {
		d.IntentMismatch = false
	}
	return d
}

func clip(s string, n int) string {
	if utf16Len(s) <= n {
		return s
	}
	return utf16Slice(s, 0, n) + "…"
}

// confirmMessage is the dialog body: what the call is, why warden stopped, and what the judge said.
func confirmMessage(v Verdict) string {
	s := v.Summary
	var lines []string
	if s.Command != "" {
		lines = append(lines, clip(s.Command, 1200))
	}
	if s.Path != "" {
		line := s.Tool + " " + s.Path
		if s.Location == "outside_project" {
			line += " (outside the project)"
		}
		if s.Exists != nil && !*s.Exists {
			line += " (new file)"
		}
		lines = append(lines, line)
	}
	if s.Input != "" {
		lines = append(lines, clip(s.Input, 1200))
	}
	lines = append(lines, "", "Why: "+strings.Join(v.Reasons, "; "))
	if j := v.Judgment; j != nil {
		lines = append(lines, "Judge: irreversible "+percent(j.Irreversible)+", off-task "+percent(j.OffTask)+", "+strings.ReplaceAll(j.Scope, "_", " ")+" ("+j.Model+", "+itoaInt(j.ElapsedMs)+" ms)")
	}
	lines = append(lines, "Judgments are model output, not authorization. Yes runs the tool; No blocks it and tells the agent.")
	return strings.Join(lines, "\n")
}

func blockResult(reason string) any { return map[string]any{"block": true, "reason": reason} }

func (w *warden) onToolCall(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	if cfg.Enabled {
		w.contextToolCall(ctx, cfg, data) // CONTEXT-LANE HOOK: a read of a stored full output is a recall
	}
	tool, _ := data["toolName"].(string)
	input, _ := data["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	if cfg.Enabled {
		w.conscienceToolCall(ctx, cfg, tool, input) // CONSCIENCE-LANE HOOK: the selected capability was reached for
	}
	if !cfg.Enabled || !cfg.Action.Enabled || !toolGuarded(cfg.Action, tool) {
		return nil, nil
	}
	id, _ := data["toolCallId"].(string)
	if cfg.Enabled {
		// guardcore: temp paths this call may create (session scratch) and the one-time rule notices.
		w.gcToolCallStart(ctx, cfg, id, tool, input)
	}
	if !cfg.Enabled || !cfg.Action.Enabled || !toolGuarded(cfg.Action, tool) {
		return nil, nil
	}
	judge := w.judgeFor(ctx, cfg)
	w.mu.Lock()
	fallback := w.lastPrompt
	w.stats.Inspected++
	w.mu.Unlock()
	if w.conscienceIndexWrite(ctx, cfg, tool, input) { // CONSCIENCE-LANE HOOK: /warden index writes its own files
		return nil, nil
	}
	call := ToolCallRef{ID: id, Tool: tool, Input: input}
	conv := conversationOf(w.branchOf(ctx), id, fallback, call)
	// guardcore: a write/edit to a protected path arms matching command patterns before the guard looks at the call.
	w.gcArm(ctx, cfg, tool, input)
	w.rulesFirstRunNotice(ctx, cfg)                            // rules hook: the first-run notice
	plan := w.rulesStart(ctx, cfg, call, conv.Siblings, judge) // rules hook: the rules requests start beside the action request
	verdict := w.guard.Inspect(newContext(), call, conv, InspectOptions{Config: cfg.Action, Cwd: ctx.Cwd(), Judge: judge, Timeout: w.timeout(cfg), Git: w.opts.Git, GuardExtras: w.gcExtras(cfg)})
	// guardcore: armed rules fire as deterministic hits on top of the verdict.
	verdict = w.gcArmed(cfg, tool, input, verdict)
	// SECURITY: the proposed write may introduce a security weakness (a steer of its own, before the call's notes).
	if verdict.Judgment != nil {
		w.securityWrite(ctx, cfg, verdict.Judgment.SecurityRisk, verdict.Summary.Path)
	}

	dv := deliveryVerdict(verdict)
	w.settleRegretFromVerdict(cfg, verdict) // learning lane: the regret answer that rode this judgment labels last turn's allowed calls
	mode := w.mode(cfg, ctx)
	w.mu.Lock()
	if verdict.Judgment != nil {
		w.stats.Judged++
	}
	if verdict.Source == "error" {
		w.stats.Errors++
	}
	if verdict.ApprovedByUser {
		w.stats.Approved++
	}
	w.mu.Unlock()
	if verdict.ApprovedByUser {
		w.holdApproved(cfg, tool) // learning lane: the reply released a pending hold
	}
	if verdict.Source == "error" {
		w.noteOnce(ctx, "err:"+verdict.Error, "warden: "+verdict.Error+" Judged checks are skipped for this call (offline patterns still apply).")
	}

	// What the agent is told about a call that runs (or that will not): the plan gap, the off-task drift.
	var notes []string
	var noteKinds []SteerKind
	// learning lane: each action note is gated by the steer adaptation (a kind the model ignores stays in the trace)
	// and counted; the delivery watches what the agent does next (extension.ts adaptiveSend/watchSteer).
	if verdict.IntentMismatch {
		w.bump(func(s *stats) {
			s.OffPlan++
			if verdict.IntentTraceOnly {
				s.OffPlanTraceOnly++
			}
		})
		if !verdict.IntentTraceOnly && w.adaptiveSend(ctx, cfg, SteerKindIntentMismatch) {
			notes = append(notes, IntentSteer(verdict))
			noteKinds = append(noteKinds, SteerKindIntentMismatch)
		}
	}
	if verdict.OffTaskSteer {
		w.bump(func(s *stats) { s.OffTask++ })
		if !verdict.OffTaskTraceOnly && w.adaptiveSend(ctx, cfg, SteerKindOffTask) {
			notes = append(notes, OffTaskSteer(verdict))
			noteKinds = append(noteKinds, SteerKindOffTask)
		}
	}
	if verdict.ShouldProceedSteer && !verdict.ShouldProceedTraceOnly && w.adaptiveSend(ctx, cfg, SteerKindShouldProceed) {
		notes = append(notes, ShouldProceedMessage(verdict))
		noteKinds = append(noteKinds, SteerKindShouldProceed)
	}
	// guardcore: the large-output steer, once per command family per session (never holds).
	if note := w.gcLargeOutput(verdict); note != "" && w.adaptiveSend(ctx, cfg, SteerKindLargeOutput) {
		notes = append(notes, note)
		noteKinds = append(noteKinds, SteerKindLargeOutput)
	}
	// One combined steer per call: the action notes, then the rules findings, then the sensitive-path notes.
	cn := &callNotes{notes: notes, kinds: noteKinds}
	if len(notes) > 0 {
		cn.addGuard(SteerGuardAction)
	}
	w.slopNote(ctx, cfg, tool, input, verdict, cn)             // SECURITY: slop findings wait with the call's content notes
	w.rulesVerdicts(ctx, cfg, plan, tool, verdict.Summary, cn) // rules hook: await the rules requests, log, trace, path notes
	deliverNotes := func(proceeds bool) { w.deliverCallNotes(ctx, cfg, cn, proceeds, notePathOf(verdict.Summary, plan)) }
	reasons := strings.Join(verdict.Reasons, "; ")
	deliveredReasons := strings.Join(dv.Reasons, "; ")
	// learning lane (extension.ts `track`): what happens to this call is the label for its scores; a decision made in
	// the dialog lands at once, a steer-mode hold waits for the user.
	track := func(label string, held bool, outcome CallOutcome, via OutcomeVia) {
		entry := w.recordVerdict(ctx, cfg, verdict, label)
		agentReason := ""
		if held {
			agentReason = SteerReason(dv, judge != nil)
		}
		w.trackHold(ctx, cfg, holdTrack{verdict: verdict, entry: entry, mode: mode, held: held, outcome: outcome, via: via, conv: conv, agentReason: agentReason})
	}

	switch verdict.Level {
	case LevelWarn:
		deliverNotes(true)
		w.bump(func(s *stats) { s.Warned++ })
		track("warned", false, "", "")
		if cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · "+tool+": "+reasons, "warning")
		} else if !ctx.HasUI() && len(dv.Reasons) > 0 {
			w.steer(cfg, []SteerGuard{SteerGuardAction}, ctx, "pi-warden: this "+tool+" call ran with a warning ("+deliveredReasons+"). Nobody sees this in a headless run, so it is on you: if the flagged risk is expected, continue; otherwise fix it or ask the user before going on.", SteerOptions{DeliverAs: "steer"})
		}
		return nil, nil
	case LevelDeny:
		deliverNotes(false)
		w.bump(func(s *stats) { s.Held++ })
		track("blocked", true, OutcomeDeclined, ViaDeny)
		if cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · blocked "+tool+": "+reasons, "error")
		}
		w.notifyDesktop(ctx, cfg, "Blocked "+tool+": "+reasons+". A deny rule matched; the call never ran.") // SECURITY
		return blockResult("pi-warden blocked this " + tool + " call (" + deliveredReasons + "). A deny rule matched; this command is not allowed to run. Ask the user if this is genuinely required."), nil
	case LevelConfirm:
	default:
		deliverNotes(true)
		track("allowed", false, "", "")
		return nil, nil
	}

	// A held call. A user-defined confirm rule asks the user in every mode, advise included.
	dialogRule := false
	for _, h := range verdict.Patterns {
		if h.Action == "dialog" {
			dialogRule = true
		}
	}
	askUser := func() (any, error) {
		w.notifyDesktop(ctx, cfg, "Waiting for you: allow this "+tool+" call? "+reasons) // SECURITY
		allowed, err := ctx.Confirm("warden: allow this "+tool+" call?", confirmMessage(verdict))
		if err != nil {
			return nil, err
		}
		deliverNotes(allowed)
		if allowed {
			track("allowed by you", true, OutcomeApproved, ViaDialog)
			return nil, nil
		}
		w.bump(func(s *stats) { s.Held++ })
		track("declined by you", true, OutcomeDeclined, ViaDialog)
		return blockResult("pi-warden: the user declined this " + tool + " call (" + deliveredReasons + "). Do not retry it unchanged; ask the user how to proceed."), nil
	}
	if dialogRule && ctx.HasUI() {
		return askUser()
	}
	switch mode {
	case "advise":
		deliverNotes(true)
		w.bump(func(s *stats) { s.Warned++ })
		track("advised", false, "", "")
		if cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · "+tool+" (advise mode, not held): "+reasons, "warning")
		} else if !ctx.HasUI() {
			w.steer(cfg, []SteerGuard{SteerGuardAction}, ctx, "pi-warden: this "+tool+" call ran with a warning ("+deliveredReasons+"). Nobody sees this in a headless run, so it is on you: if the flagged risk is expected, continue; otherwise fix it or ask the user before going on.", SteerOptions{DeliverAs: "steer"})
		}
		return nil, nil
	case "confirm":
		return askUser()
	}
	deliverNotes(false)
	w.bump(func(s *stats) { s.Held++ })
	w.guard.Hold(conv.Task)
	track("held", true, "", "")
	if cfg.Notices && ctx.HasUI() {
		ctx.Notify("warden · held "+tool+": "+reasons+". The agent was told why and asked to re-plan or ask you.", "warning")
	}
	w.notifyDesktop(ctx, cfg, "Held "+tool+": "+reasons+". The agent will re-plan or ask you in chat.") // SECURITY
	return blockResult(SteerReason(dv, judge != nil)), nil
}

func (w *warden) bump(f func(*stats)) {
	w.mu.Lock()
	f(&w.stats)
	w.mu.Unlock()
}
