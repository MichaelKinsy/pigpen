package warden

import (
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// `/warden unmute` and `/warden recommend` (extension.ts 2726-2733, 2753-2800), and the lines the learning module adds
// to `/warden status`.

// unmuteCommand resets one steer kind's counts so its steers are sent again.
func (w *warden) unmuteCommand(ctx sdk.Context, rest []string) {
	var kind SteerKind
	if len(rest) > 0 {
		for _, k := range SteerKinds {
			if string(k) == rest[0] {
				kind = k
			}
		}
	}
	if kind == "" {
		var names []string
		for _, k := range SteerKinds {
			if !NeverMuted[k] {
				names = append(names, string(k))
			}
		}
		w.say(ctx, "Usage: /warden unmute <kind> [model]. Kinds: "+strings.Join(names, ", ")+".", "warning")
		return
	}
	var models []string
	switch {
	case len(rest) > 1:
		models = []string{rest[1]}
	case w.modelKey(ctx) != "":
		models = []string{w.modelKey(ctx)}
	default:
		models = w.adaptor.modelsWith(kind)
	}
	var reset []string
	for _, m := range models {
		if w.adaptor.unmute(m, kind) {
			reset = append(reset, m)
		}
	}
	if len(reset) > 0 {
		w.say(ctx, "Reset "+string(kind)+" for "+strings.Join(reset, ", ")+": its steers are sent again and counted from zero.", "info")
		return
	}
	where := strings.Join(models, ", ")
	if where == "" {
		where = "any model"
	}
	w.say(ctx, "No counts for "+string(kind)+" on "+where+"; nothing to reset.", "info")
}

// recommendCommand reports what the hold history says about the thresholds and the steers.
func (w *warden) recommendCommand(ctx sdk.Context) {
	store := w.learning()
	w.learnEnsureWhen(w.config(ctx), true)
	if store == nil {
		w.say(ctx, "pi-warden learning recommendations:\nNo recommendations yet. Need more hold data (current: "+strconv.Itoa(w.learn.ledger.Snapshot().Holds)+" holds).", "info")
		return
	}
	w.learnFlush()
	recs, err := store.GenerateRecommendations(ctx.Cwd())
	if err != nil {
		w.say(ctx, "warden: could not read the learning data: "+err.Error(), "warning")
		return
	}
	steerReport, err := store.AnalyzeSteerEffectivenessReport(ctx.Cwd())
	if err != nil {
		w.say(ctx, "warden: could not read the learning data: "+err.Error(), "warning")
		return
	}
	lines := []string{"pi-warden learning recommendations:"}
	if len(recs) == 0 && len(steerReport.Suggestions) == 0 {
		lines = append(lines, "No recommendations yet. Need more hold data (current: "+strconv.Itoa(w.learn.ledger.Snapshot().Holds)+" holds).")
	} else {
		if len(recs) > 0 {
			lines = append(lines, "", "Threshold & pattern recommendations:")
			for _, rec := range recs[:min(5, len(recs))] {
				lines = append(lines, "  ["+rec.Priority+"] "+rec.Message)
			}
		}
		if len(steerReport.Suggestions) > 0 {
			lines = append(lines, "", "Steer effectiveness:", "  Overall effectiveness: "+toFixed(steerReport.Overall*100, 0)+"%")
			for _, s := range steerReport.Suggestions[:min(3, len(steerReport.Suggestions))] {
				lines = append(lines, "  "+s)
			}
		}
		if len(steerReport.ByType) > 0 {
			lines = append(lines, "", "Effectiveness by type:")
			for _, typ := range steerReport.TypeOrder {
				s := steerReport.ByType[typ]
				lines = append(lines, "  "+typ+": "+toFixed(s.Rate*100, 0)+"% ("+strconv.Itoa(s.Effective)+"/"+strconv.Itoa(s.Total)+")")
			}
		}
		if len(steerReport.TopPatterns) > 0 {
			lines = append(lines, "", "Top performing patterns:")
			for _, p := range steerReport.TopPatterns[:min(3, len(steerReport.TopPatterns))] {
				lines = append(lines, "  "+p.Pattern+": "+toFixed(p.Effectiveness*100, 0)+"% effectiveness ("+strconv.Itoa(p.SampleSize)+" samples)")
			}
		}
	}
	w.say(ctx, strings.Join(lines, "\n"), "info")
}

// learningStatusLines are the lines of `/warden status` that come from the learning module: the steer kinds kept
// in the trace only, what the hold history recommends, this session's holds and the lifetime counts here.
func (w *warden) learningStatusLines(ctx sdk.Context, cfg Config) []string {
	s := w.statsCopy()
	lines := []string{strconv.Itoa(s.CooldownSkips) + " checks without Jev during a judge cooldown.", FormatMuted(w.adaptor.muted(), cfg.Steers)}
	if s.SteersMuted > 0 {
		plural := "s"
		if s.SteersMuted == 1 {
			plural = ""
		}
		lines[1] += " This session: " + strconv.Itoa(s.SteersMuted) + " steer" + plural + " kept in the trace only."
	}
	store := w.learning()
	if store != nil {
		w.learnFlush()
	}
	if cfg.Learning.PatternAnalysis && store != nil {
		recs, _ := store.GenerateRecommendations(ctx.Cwd())
		steerReport, _ := store.AnalyzeSteerEffectivenessReport(ctx.Cwd())
		lines = append(lines, "Learning: "+strconv.Itoa(len(recs))+" recommendations, steer effectiveness "+strconv.Itoa(jsRound(steerReport.Overall*100))+"% (use /warden recommend for details)")
	}
	logPath := ""
	var failure string
	w.learn.mu.Lock()
	if w.learn.log != nil {
		failure = w.learn.log.LastFailure()
		if cfg.Action.FeedbackLog {
			logPath = w.learn.log.Path
		}
	}
	w.learn.mu.Unlock()
	holds := FormatHolds(w.learn.ledger.Snapshot(), logPath)
	if failure != "" {
		holds += " Log write failed: " + failure + "."
	}
	lines = append(lines, holds)
	if store != nil {
		if ls, err := store.HoldStats(ctx.Cwd()); err == nil {
			word := "holds"
			if ls.Held == 1 {
				word = "hold"
			}
			if ls.Labeled == 0 {
				lines = append(lines, "Lifetime here: "+strconv.Itoa(ls.Held)+" "+word+", not yet measurable ("+strconv.Itoa(ls.Allowed)+" allowed).")
			} else {
				stood := ls.Declined + ls.Replanned
				lines = append(lines, "Lifetime here: "+strconv.Itoa(ls.Held)+" "+word+", "+strconv.Itoa(ls.Labeled)+" labeled, "+strconv.Itoa(stood)+" stood ("+strconv.Itoa(stood)+"/"+strconv.Itoa(ls.Labeled)+"), "+strconv.Itoa(ls.Allowed)+" allowed ("+strconv.Itoa(ls.Accepted)+" accepted, "+strconv.Itoa(ls.Regretted)+" regretted).")
			}
		}
	}
	w.learn.mu.Lock()
	warnings := append([]string(nil), w.learn.warnings...)
	w.learn.mu.Unlock()
	if n := len(warnings); n > 0 {
		lines = append(lines, "Learning warnings: "+strings.Join(warnings[max(0, n-3):], "; ")+".")
	}
	return lines
}

// statsCopy is a copy of the session counters.
func (w *warden) statsCopy() stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}
