package warden

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

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

// recordVerdict records an action verdict in the trace; a read-only call leaves no line.
func (w *warden) recordVerdict(ctx sdk.Context, cfg Config, v Verdict, outcome string) {
	if v.Source == "read-only" {
		return
	}
	w.record(ctx, cfg, GuardAction, traceLine(v, outcome), nil, actionTokens(v, w.opts.Now()))
}

// refresh repaints with the current configuration.
func (w *warden) refresh(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	w.paint(ctx, w.config(ctx))
}

// paint redraws the status line and the widget: the latest line of each guard, most recent last.
func (w *warden) paint(ctx sdk.Context, cfg Config) {
	if !ctx.HasUI() {
		return
	}
	if !cfg.Enabled {
		ctx.SetStatus(statusKey, "warden: off · /warden enable")
		_ = ctx.SetWidget(widgetKey, []string(nil))
		return
	}
	w.mu.Lock()
	s := w.stats
	var lines []string
	if cfg.Widget.Enabled {
		for _, wl := range w.widget {
			lines = append(lines, wl.line)
		}
	}
	w.mu.Unlock()
	ctx.SetStatus(statusKey, fmt.Sprintf("warden: %s · %s · %d checked, %d held", w.statusBackend(cfg), cfg.Mode, s.Inspected, s.Held))
	if len(lines) > 0 {
		_ = ctx.SetWidget(widgetKey, lines)
	} else {
		_ = ctx.SetWidget(widgetKey, []string(nil))
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
