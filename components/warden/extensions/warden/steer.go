package warden

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// steer sends the agent one notice. A notice delivered once is already in its context, so a repeat is
// recorded only; notices past the per-run budget are skipped too (each one asks the agent for a reply
// sentence, and a closing run that collects six notices collects six restatements). Critical notices (stuck,
// done) always deliver: they start the turn. followUp with triggerTurn continues a run that has ended.
func (w *warden) steer(ctx sdk.Context, cfg Config, critical bool, content, deliverAs string, triggerTurn bool) bool {
	w.mu.Lock()
	over := cfg.SteerBudget > 0 && w.steersThisRun >= cfg.SteerBudget
	repeat := w.repeats.Seen(content)
	if !critical && (over || repeat) {
		w.stats.SteersSkipped++
		w.mu.Unlock()
		return false
	}
	w.stats.Steers++
	w.steersThisRun++
	if triggerTurn {
		w.continuation = true
	}
	w.mu.Unlock()
	opts := sdk.SendMessageOptions{DeliverAs: deliverAs}
	if triggerTurn {
		yes := true
		opts.TriggerTurn = &yes
	}
	if err := ctx.SendMessage(steerType, content, cfg.SteerVisible, opts); err != nil {
		w.mu.Lock()
		w.continuation = false
		w.mu.Unlock()
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// The trace: one line per verdict, in the widget and /warden trace.

func previewOf(v Verdict) string {
	s := v.Summary
	switch {
	case s.Command != "":
		return clip(strings.Join(strings.Fields(s.Command), " "), 60)
	case s.Path != "":
		return s.Path
	}
	return ""
}

// traceLine renders one verdict for the user: what warden did, to which call, and why.
func traceLine(v Verdict, outcome string) string {
	parts := []string{"warden", outcome, v.Summary.Tool}
	if p := previewOf(v); p != "" {
		parts = append(parts, p)
	}
	if len(v.Reasons) > 0 {
		parts = append(parts, strings.Join(v.Reasons, "; "))
	} else if v.Source == "read-only" {
		parts = append(parts, "read-only")
	}
	return strings.Join(parts, " · ")
}

func (w *warden) record(ctx sdk.Context, v Verdict, outcome string) {
	if v.Source == "read-only" {
		return
	}
	w.addTrace(ctx, traceLine(v, outcome))
}

func (w *warden) addTrace(ctx sdk.Context, line string) {
	w.mu.Lock()
	w.trace = append(w.trace, traceEntry{At: w.opts.Now(), Line: line})
	if len(w.trace) > 30 {
		w.trace = w.trace[len(w.trace)-30:]
	}
	w.mu.Unlock()
	w.refresh(ctx)
}

// refresh redraws the status line and the recent-verdicts widget.
func (w *warden) refresh(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	cfg := w.config(ctx)
	if !cfg.Enabled {
		ctx.SetStatus(statusKey, "warden: off · /warden enable")
		_ = ctx.SetWidget(widgetKey, []string(nil))
		return
	}
	w.mu.Lock()
	s := w.stats
	var lines []string
	from := max(0, len(w.trace)-4)
	for _, t := range w.trace[from:] {
		lines = append(lines, t.At.Format("15:04:05")+" "+t.Line)
	}
	w.mu.Unlock()
	ctx.SetStatus(statusKey, fmt.Sprintf("warden: %s · %s · %d checked, %d held", w.statusBackend(cfg), cfg.Mode, s.Inspected, s.Held))
	if len(lines) > 0 {
		_ = ctx.SetWidget(widgetKey, lines)
	}
}

// statusBackend names the judge in the status line, or says why the chosen one is not in use (warden then runs
// its offline patterns only).
func (w *warden) statusBackend(cfg Config) string {
	short := describeBackendShort(cfg.Backend)
	switch {
	case cfg.Backend == BackendNone:
		return short
	case cfg.Consent != cfg.Backend:
		return "offline (" + short + " not agreed to)"
	case cfg.Backend == BackendTypeSafe && w.opts.Getenv(typesafe.EnvAPIKey) == "":
		return "offline (typesafe: no " + typesafe.EnvAPIKey + ")"
	}
	return short
}

func describeBackendShort(b string) string {
	switch b {
	case BackendTypeSafe:
		return "typesafe"
	case BackendOwnModel:
		return "own model"
	}
	return "offline"
}

func describeBackend(b, target string) string {
	switch b {
	case BackendTypeSafe:
		return "TypeSafe judge at " + target
	case BackendOwnModel:
		return "your session model " + target + " as judge"
	}
	return "offline patterns only"
}

// targetOf names where a backend sends: the TypeSafe URL, or the session model.
func (w *warden) targetOf(ctx sdk.Context, backend string) string {
	switch backend {
	case BackendTypeSafe:
		return TypeSafeTarget(w.opts.Getenv)
	case BackendOwnModel:
		if info, err := ctx.GetModelInfo(); err == nil && info != nil {
			return info.Provider + "/" + info.ID
		}
		return "the session model"
	}
	return ""
}

// branchOf reads the session branch. A failed read is reported once (it means the plan and the earlier
// messages are unavailable) and the guard carries on with the latest prompt alone: a broken session mirror never
// blocks a tool call.
func (w *warden) branchOf(ctx sdk.Context) []sdk.BranchEntry {
	branch, err := ctx.GetBranch()
	if err != nil {
		w.noteOnce(ctx, "branch", "warden could not read the session ("+err.Error()+"): judging with your latest prompt only.")
		return nil
	}
	return branch
}
