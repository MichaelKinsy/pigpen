package warden

import (
	"path/filepath"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func contentParts(v any) []ContentPart {
	var out []ContentPart
	switch c := v.(type) {
	case string:
		out = append(out, ContentPart{Type: "text", Text: c})
	case []any:
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				t, _ := m["type"].(string)
				s, _ := m["text"].(string)
				out = append(out, ContentPart{Type: t, Text: s})
			}
		}
	}
	return out
}

// projectPath is a path relative to the project with forward slashes, or the path itself when it lies outside.
func projectPath(target, cwd string) string {
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, target)
	}
	if isInside(abs, cwd) {
		if rel, err := filepath.Rel(cwd, abs); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return target
}

// rawContent is the result content as the host sent it: its parts, untouched, so a rewrite keeps images and fields.
func rawContent(v any) []any {
	switch c := v.(type) {
	case []any:
		return c
	case string:
		return []any{map[string]any{"type": "text", "text": c}}
	}
	return nil
}

// onToolResult keeps the run's memory: the loop window (repeats and stuck loops) and the evidence the
// done-check reads (changes, checks, UI proof), and rewrites the result the model reads: the call-waste note and
// the stuck-loop diff. Upstream order (extension.ts 1806-2140): masking, call waste record, conscience, stuck
// window, output security and context, stuck-loop diff, evidence, quick repeat, stuck verdict, call-waste note.
func (w *warden) onToolResult(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled {
		return nil, nil
	}
	tool, _ := data["toolName"].(string)
	input, _ := data["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	raw := rawContent(data["content"])
	content := contentParts(data["content"])
	isError, _ := data["isError"].(bool)
	details, _ := data["details"].(map[string]any)
	failed := ResultFailed(isError, details, content)
	text := ResultText(content)

	// ── supervision: call waste. Every finished call enters the window; a note, when one fires, is attached to
	// this result below, and never reaches a hold, the judge, or the steer budget.
	wasteNudge := w.wasteNote(ctx, cfg, tool, input, failed, text)

	out, changed := w.toolResultRun(ctx, cfg, tool, input, raw, content, failed, text)

	// ── supervision: the call-waste note rides the result the model is about to read ──
	if wasteNudge != nil {
		if next, ok := withWasteNote(out, wasteNudge.Text); ok {
			out, changed = next, true
			w.recordWasteNote(ctx, cfg, tool, input, wasteNudge)
		}
	}
	if !changed {
		return nil, nil
	}
	return map[string]any{"content": out}, nil
}

// toolResultRun is the run memory and the stuck guard; it returns the content to deliver and whether it changed.
func (w *warden) toolResultRun(ctx sdk.Context, cfg Config, tool string, input map[string]any, raw []any, content []ContentPart, failed bool, text string) ([]any, bool) {
	// Evidence for the done-check.
	w.mu.Lock()
	outcome := ClassifyToolResult(tool, input, failed, text)
	RecordOutcome(w.evidence, outcome, input, tool)
	if !failed {
		var written []string
		if tool == "write" || tool == "edit" {
			if p, ok := input["path"].(string); ok && p != "" {
				written = append(written, projectPath(p, ctx.Cwd()))
			}
		}
		RecordUI(w.evidence, written, IsVisualCheck(tool, input, failed, cfg.Done.VisualTools), cfg.Done.UIFiles)
	}
	w.mu.Unlock()

	if !cfg.Stuck.Enabled {
		return raw, false
	}
	w.mu.Lock()
	if w.window == nil {
		w.window = NewAttemptWindow(cfg.Stuck.Window)
	}
	w.window.Push(MakeAttempt(tool, input, content, failed))
	var quick *QuickRepeat
	if cfg.Stuck.Nudge && cfg.Stuck.RepeatSteer {
		quick = w.window.QuickRepeat()
	}
	judgeNow := w.window.ShouldJudge(cfg.Stuck)
	if judgeNow {
		w.stats.StuckChecks++
	}
	w.mu.Unlock()

	var verdict *StuckVerdict
	if judgeNow {
		judge := w.judgeFor(ctx, cfg)
		w.mu.Lock()
		win := w.window
		w.mu.Unlock()
		v := EvaluateStuck(newContext(), win, w.latestTask(ctx), StuckOptions{Config: cfg.Stuck, Judge: judge, Timeout: w.timeout(cfg)})
		verdict = &v
	}

	// ── supervision: the stuck-loop diff replaces a failed result that repeats a failure ──
	out, changed := w.stuckDiffResult(ctx, cfg, verdict, failed, text, raw)

	// A stuck verdict on the same call carries its own steer; the quick one would say the same thing twice.
	if quick != nil && (verdict == nil || !verdict.Stuck) {
		nudge := QuickRepeatNudge(*quick)
		sent := w.adaptiveSend(ctx, cfg, SteerKindRepeat) && w.steer(cfg, []SteerGuard{SteerGuardRepeat}, ctx, nudge, SteerOptions{DeliverAs: "steer"})
		if sent {
			w.watchSteer(ctx, cfg, []SteerKind{SteerKindRepeat}, SteerSubject{Call: &SteerCall{Name: tool, Input: input}}, false)
		}
		what, noticeWhat := "same output", "same output re-read"
		if quick.Attempt.Failed {
			what, noticeWhat = "same failure", "same call failed the same way"
		}
		note, told := "recorded only", "nothing (repeat of a recent steer, over the steer budget, or trace-only for this model)"
		if sent {
			note, told = "agent nudged", nudge
		}
		ago := strconv.Itoa(quick.CallsAgo)
		w.record(ctx, cfg, GuardStuck, "warden · stuck · repeat · "+quick.Attempt.Tool+" · "+what+" · "+note, []string{
			"call: " + quick.Attempt.Call, "previous identical call: " + ago + " calls ago, nothing changed between", "agent told: " + told,
		}, nil)
		if sent && cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden \u00b7 repeat: "+noticeWhat+" (agent nudged)", "warning")
		}
	}
	if verdict == nil {
		return out, changed
	}
	if verdict.Error != "" {
		w.noteError(ctx, verdict.Error, verdict.ErrorCode)
	}
	if verdict.Source == "repeat" && !verdict.Stuck {
		return out, changed
	}
	nudge := ""
	if verdict.Stuck && cfg.Stuck.Nudge {
		nudge = StuckNudge(*verdict)
	}
	w.mu.Lock()
	attempts := append([]Attempt(nil), w.window.Attempts...)
	w.mu.Unlock()
	w.record(ctx, cfg, GuardStuck, RenderTemplate(cfg.Widget.Stuck, stuckTokens(*verdict, w.opts.Now())), stuckDetails(*verdict, attempts, nudge), nil)
	if !verdict.Stuck {
		return out, changed
	}
	w.bump(func(s *stats) { s.Stuck++ })
	if cfg.Notices && ctx.HasUI() {
		suffix := ""
		if nudge != "" {
			suffix = " (agent nudged)"
		}
		ctx.Notify("warden \u00b7 stuck: "+strings.Join(verdict.Reasons, "; ")+suffix, "warning")
	}
	if nudge != "" && w.adaptiveSend(ctx, cfg, SteerKindStuck) && w.steer(cfg, []SteerGuard{SteerGuardStuck}, ctx, nudge, SteerOptions{DeliverAs: "steer"}) {
		w.watchSteer(ctx, cfg, []SteerKind{SteerKindStuck}, SteerSubject{Call: &SteerCall{Name: tool, Input: input}}, false)
	}
	return out, changed
}

// stuckDetails is the trace detail of a stuck verdict (trace.ts stuckDetails): the attempts, what the judge said,
// why, and what the agent was told.
func stuckDetails(v StuckVerdict, attempts []Attempt, told string) []string {
	lines := make([]string, 0, len(attempts)+4)
	for i, a := range attempts {
		mark := "\u2713"
		if a.Failed {
			mark = "\u2717"
		}
		line := strconv.Itoa(i+1) + ". " + mark + " " + clip(collapseSpaceAll(a.Call), 120)
		if a.Failed && a.Output != "" {
			line += " \u2192 " + clip(collapseSpaceAll(a.Output), 120)
		}
		lines = append(lines, line)
	}
	if j := v.Judgment; j != nil {
		lines = append(lines, "jev: same strategy "+percent(j.SameStrategy)+" \u00b7 approach change "+toFixed(j.ApproachChange, 2)+"/2 \u00b7 progress "+percent(j.Progress)+" \u00b7 "+j.Model+" \u00b7 "+strconv.Itoa(j.ElapsedMs)+" ms")
	}
	if len(v.Reasons) > 0 {
		lines = append(lines, "why: "+strings.Join(v.Reasons, "; "))
	}
	if v.Error != "" {
		lines = append(lines, "typesafe: "+v.Error)
	}
	if told != "" {
		lines = append(lines, "agent told: "+clip(told, 400))
	}
	return lines
}

// collapseSpaceAll is `.replace(/\s+/g, " ")` without a trim.
func collapseSpaceAll(s string) string { return reJSSpace.ReplaceAllString(s, " ") }

func (w *warden) latestTask(ctx sdk.Context) string {
	if text, ok := w.latestUserPrompt(ctx); ok && text != "" {
		return text
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastPrompt
}

// onAgentEnd is the done-check: a final message that claims the work is finished, after file changes no check
// exercised or a UI change nothing showed, is asked to verify (or to say plainly that nothing was verified).
//
// The order of upstream's agent_end handler (extension.ts 2298-2450). Other lanes add their piece at its marker, as
// ONE call, so the order stays upstream's:
//
//	1. the enabled gate
//	2. regret settled by the prompt text, when no guarded call carried the question   (guardcore)
//	3. a runaway stop: the recovery steer, then the handler returns                   (security)
//	4. the open-loops reminder                                                        (tools)
//	5. restatement measured on the final message, recorded only                       (security: prose)
//	6. the conscience reminder, when no final text answered                           (conscience)
//	7. turn rules: one judgment against the whole diff                                (rules)
//	8. with a final message and a judge: prose and done evaluated together, prose recorded
//	   and nudged first, then the done verdict and its follow-up                      (prose: security; done: here)
func (w *warden) onAgentEnd(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	// 1. enabled gate.
	if !cfg.Enabled {
		return nil, nil
	}
	// 2. regret settle: guardcore.
	// 3. runaway recovery (returns): security.
	// 4. open-loops reminder: tools.
	// 5. restatement: security.
	// 6. conscience reminder: conscience.
	// 7. turn rules: rules.
	// 8. done check (below); the prose check runs with it.
	if !cfg.Done.Enabled {
		return nil, nil
	}
	var messages []map[string]any
	if raw, ok := data["messages"].([]any); ok {
		for _, m := range raw {
			if mm, ok := m.(map[string]any); ok {
				messages = append(messages, mm)
			}
		}
	}
	final, ok := FinalAssistantText(messages)
	w.mu.Lock()
	ev := w.evidence
	needs := ok && NeedsDoneCheck(ev)
	w.mu.Unlock()
	if !needs {
		return nil, nil
	}
	judge := w.judgeFor(ctx, cfg)
	if judge == nil {
		return nil, nil
	}
	w.bump(func(s *stats) { s.DoneChecks++ })
	verdict := EvaluateDone(newContext(), w.latestTask(ctx), final, ev, DoneOptions{Config: cfg.Done, Judge: judge, Timeout: w.timeout(cfg)})
	if verdict.Error != "" {
		w.noteOnce(ctx, "err:"+verdict.Error, "warden: "+verdict.Error)
	}
	w.mu.Lock()
	nudge := ""
	if verdict.Unverified && cfg.Done.Nudge && !w.doneNudged {
		nudge = DoneNudge(verdict)
	}
	w.mu.Unlock()
	w.record(ctx, cfg, GuardDone, FormatDone(verdict), nil, doneTokens(verdict, w.opts.Now()))
	if !verdict.Unverified {
		return nil, nil
	}
	w.bump(func(s *stats) { s.Unverified++ })
	if cfg.Notices && ctx.HasUI() {
		suffix := ""
		if nudge != "" {
			suffix = " (agent asked to verify)"
		}
		ctx.Notify("warden · done-check: "+strings.Join(verdict.Reasons, "; ")+suffix, "warning")
	}
	if nudge != "" {
		w.mu.Lock()
		w.doneNudged = true
		w.mu.Unlock()
		w.steer(cfg, []SteerGuard{SteerGuardDone}, ctx, nudge, SteerOptions{DeliverAs: "followUp", TriggerTurn: true})
	}
	return nil, nil
}
