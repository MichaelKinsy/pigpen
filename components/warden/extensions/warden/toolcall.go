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
	tool, _ := data["toolName"].(string)
	if !cfg.Enabled || !cfg.Action.Enabled || !toolGuarded(cfg.Action, tool) {
		return nil, nil
	}
	input, _ := data["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	id, _ := data["toolCallId"].(string)
	judge := w.judgeFor(ctx, cfg)
	w.mu.Lock()
	fallback := w.lastPrompt
	w.stats.Inspected++
	w.mu.Unlock()
	call := ToolCallRef{ID: id, Tool: tool, Input: input}
	conv := conversationOf(w.branchOf(ctx), id, fallback, call)
	verdict := w.guard.Inspect(newContext(), call, conv, InspectOptions{Config: cfg.Action, Cwd: ctx.Cwd(), Judge: judge, Timeout: w.timeout(cfg), Git: w.opts.Git})

	dv := deliveryVerdict(verdict)
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
	if verdict.Source == "error" {
		w.noteOnce(ctx, "err:"+verdict.Error, "warden: "+verdict.Error+" Judged checks are skipped for this call (offline patterns still apply).")
	}

	// What the agent is told about a call that runs (or that will not): the plan gap, the off-task drift.
	var notes []string
	if verdict.IntentMismatch && !verdict.IntentTraceOnly {
		notes = append(notes, IntentSteer(verdict))
	}
	if verdict.OffTaskSteer && !verdict.OffTaskTraceOnly {
		notes = append(notes, OffTaskSteer(verdict))
	}
	if verdict.ShouldProceedSteer && !verdict.ShouldProceedTraceOnly {
		notes = append(notes, ShouldProceedMessage(verdict))
	}
	deliverNotes := func() {
		if len(notes) > 0 {
			w.steer(ctx, cfg, false, strings.Join(notes, "\n\n"), "steer", false)
		}
	}
	reasons := strings.Join(verdict.Reasons, "; ")
	deliveredReasons := strings.Join(dv.Reasons, "; ")

	switch verdict.Level {
	case LevelWarn:
		deliverNotes()
		w.bump(func(s *stats) { s.Warned++ })
		w.record(ctx, verdict, "warned")
		if cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · "+tool+": "+reasons, "warning")
		} else if !ctx.HasUI() && len(dv.Reasons) > 0 {
			w.steer(ctx, cfg, false, "pi-warden: this "+tool+" call ran with a warning ("+deliveredReasons+"). Nobody sees this in a headless run, so it is on you: if the flagged risk is expected, continue; otherwise fix it or ask the user before going on.", "steer", false)
		}
		return nil, nil
	case LevelDeny:
		deliverNotes()
		w.bump(func(s *stats) { s.Held++ })
		w.record(ctx, verdict, "blocked")
		if cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · blocked "+tool+": "+reasons, "error")
		}
		return blockResult("pi-warden blocked this " + tool + " call (" + deliveredReasons + "). A deny rule matched; this command is not allowed to run. Ask the user if this is genuinely required."), nil
	case LevelConfirm:
	default:
		deliverNotes()
		w.record(ctx, verdict, "allowed")
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
		allowed, err := ctx.Confirm("warden: allow this "+tool+" call?", confirmMessage(verdict))
		if err != nil {
			return nil, err
		}
		deliverNotes()
		if allowed {
			w.record(ctx, verdict, "allowed by you")
			return nil, nil
		}
		w.bump(func(s *stats) { s.Held++ })
		w.record(ctx, verdict, "declined by you")
		return blockResult("pi-warden: the user declined this " + tool + " call (" + deliveredReasons + "). Do not retry it unchanged; ask the user how to proceed."), nil
	}
	if dialogRule && ctx.HasUI() {
		return askUser()
	}
	switch mode {
	case "advise":
		deliverNotes()
		w.bump(func(s *stats) { s.Warned++ })
		w.record(ctx, verdict, "advised")
		if cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · "+tool+" (advise mode, not held): "+reasons, "warning")
		} else if !ctx.HasUI() {
			w.steer(ctx, cfg, false, "pi-warden: this "+tool+" call ran with a warning ("+deliveredReasons+"). Nobody sees this in a headless run, so it is on you: if the flagged risk is expected, continue; otherwise fix it or ask the user before going on.", "steer", false)
		}
		return nil, nil
	case "confirm":
		return askUser()
	}
	deliverNotes()
	w.bump(func(s *stats) { s.Held++ })
	w.guard.Hold(conv.Task)
	w.record(ctx, verdict, "held")
	if cfg.Notices && ctx.HasUI() {
		ctx.Notify("warden · held "+tool+": "+reasons+". The agent was told why and asked to re-plan or ask you.", "warning")
	}
	return blockResult(SteerReason(dv, judge != nil)), nil
}

func (w *warden) bump(f func(*stats)) {
	w.mu.Lock()
	f(&w.stats)
	w.mu.Unlock()
}
