package warden

import (
	"path/filepath"
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

// onToolResult keeps the run's memory: the loop window (repeats and stuck loops) and the evidence the
// done-check reads (changes, checks, UI proof).
func (w *warden) onToolResult(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled {
		return nil, nil
	}
	out := w.toolOutput(ctx, cfg, data) // CONTEXT-LANE HOOK: masking, output verdicts, notices, duplicate note, compression, repeated runs (tool_output.go)
	if out.aborted {
		return nil, nil
	}
	if _, err := w.toolResultGuards(ctx, data, out.text); err != nil {
		return nil, err
	}
	return out.patch(), nil
}

// toolResultGuards keeps the run's memory and runs the loop checks. `text` is the masked text of the result.
func (w *warden) toolResultGuards(ctx sdk.Context, data map[string]any, text string) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled {
		return nil, nil
	}
	tool, _ := data["toolName"].(string)
	input, _ := data["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	content := contentParts(data["content"])
	isError, _ := data["isError"].(bool)
	details, _ := data["details"].(map[string]any)
	failed := ResultFailed(isError, details, content)

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
		return nil, nil
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
	// A stuck verdict on the same call carries its own steer; the quick one would say the same thing twice.
	if quick != nil && (verdict == nil || !verdict.Stuck) {
		nudge := QuickRepeatNudge(*quick)
		sent := w.steer(cfg, []SteerGuard{SteerGuardRepeat}, ctx, nudge, SteerOptions{DeliverAs: "steer"})
		what := "same output"
		if quick.Attempt.Failed {
			what = "same failure"
		}
		note := "recorded only"
		if sent {
			note = "agent nudged"
		}
		w.record(ctx, cfg, GuardStuck, "warden · repeat · "+tool+" · "+what+" · "+note, nil, nil)
		if sent && cfg.Notices && ctx.HasUI() {
			ctx.Notify("warden · repeat: "+what+" (agent nudged)", "warning")
		}
	}
	if verdict == nil {
		return nil, nil
	}
	if verdict.Error != "" {
		w.noteOnce(ctx, "err:"+verdict.Error, "warden: "+verdict.Error)
	}
	if verdict.Source == "repeat" && !verdict.Stuck {
		return nil, nil
	}
	nudge := ""
	if verdict.Stuck && cfg.Stuck.Nudge {
		nudge = StuckNudge(*verdict)
	}
	w.record(ctx, cfg, GuardStuck, FormatStuck(*verdict), nil, stuckTokens(*verdict, w.opts.Now()))
	if !verdict.Stuck {
		return nil, nil
	}
	w.bump(func(s *stats) { s.Stuck++ })
	if cfg.Notices && ctx.HasUI() {
		suffix := ""
		if nudge != "" {
			suffix = " (agent nudged)"
		}
		ctx.Notify("warden · stuck: "+strings.Join(verdict.Reasons, "; ")+suffix, "warning")
	}
	if nudge != "" {
		w.steer(cfg, []SteerGuard{SteerGuardStuck}, ctx, nudge, SteerOptions{DeliverAs: "steer"})
	}
	return nil, nil
}

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
func (w *warden) onAgentEnd(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	if cfg.Enabled {
		w.toolsAgentEnd(ctx, cfg) // TOOLS-LANE HOOK: the open-loops reminder (tools_wiring.go)
	}
	if !cfg.Enabled || !cfg.Done.Enabled {
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
