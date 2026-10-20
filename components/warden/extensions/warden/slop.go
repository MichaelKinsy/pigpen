package warden

import (
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The slop steer (extension.ts 285-296 slopSteer, 1655-1665): the judge scores the content a write or edit
// proposes for stubs, restating comments, dead code and hedging (guard.go asks the questions and sets
// Verdict.SlopSymptoms); the steer names what was found and waits until the call proceeds.

// SlopFixes say how to fix each symptom (extension.ts SLOP_FIXES).
var SlopFixes = map[string]string{
	"stub":     "replace stubs, placeholders, and hard-coded fake data with the working implementation, or state in your reply exactly what is left unimplemented and why",
	"comments": "delete comments that restate the code; keep only those that explain intent, constraints, or non-obvious behaviour",
	"dead":     "remove commented-out code, unused imports and variables, duplicated logic, and unreachable branches",
	"hedging":  `replace "should work", "for now", and TODOs without a plan with a definite statement or a concrete follow-up`,
}

// SlopSteer names each symptom and its fix; repeats in the session turn the note into a standing rule.
func SlopSteer(where string, symptoms []string, counts map[string]int) string {
	named := make([]string, len(symptoms))
	fixes := make([]string, len(symptoms))
	standing := false
	for i, s := range symptoms {
		named[i] = SlopLabels[s]
		if counts[s] >= 3 {
			named[i] += " (" + itoaInt(counts[s]) + "th time this session)"
			standing = true
		}
		fixes[i] = SlopFixes[s]
	}
	tail := ""
	if standing {
		tail = " Treat this as a standing rule for the rest of the session."
	}
	return "pi-warden: the content just written to " + where + " has " + strings.Join(named, "; ") + ". Fix it in your next edit: " + strings.Join(fixes, "; ") + "." + tail
}

// slopNote queues the slop steer of one verdict as a content note of the call: it waits until the call proceeds, so
// a held, denied or declined write never lands and drops it, and an approved retry is judged again and brings its
// own. The user is told at once.
func (w *warden) slopNote(ctx sdk.Context, cfg Config, tool string, input map[string]any, v Verdict, cn *callNotes) {
	if len(v.SlopSymptoms) == 0 || len(v.SlopReasons) == 0 {
		return
	}
	w.bump(func(s *stats) { s.Slop++ })
	symptoms := v.SlopSymptoms
	where := v.Summary.Path
	if where == "" {
		where = tool
		if command, ok := input["command"].(string); ok && tool == "bash" {
			home, err := os.UserHomeDir()
			var paths []string
			for _, write := range shellWrites(command, ShellWritesOptions{Home: home, HasHome: err == nil}).Writes {
				paths = append(paths, write.Path)
			}
			if len(paths) > 0 {
				where = strings.Join(paths, ", ")
			}
		}
	}
	if ctx.HasUI() && cfg.Notices {
		ctx.Notify("warden · slop · "+where+": "+strings.Join(v.SlopReasons, "; "), "warning")
	}
	cn.content = append(cn.content, func(proceeds bool) *contentNote {
		if !proceeds || !w.adaptiveSend(ctx, cfg, SteerKindSlop) {
			return nil
		}
		w.sec.mu.Lock()
		for _, s := range symptoms {
			w.sec.slopCounts[s]++
		}
		counts := make(map[string]int, len(w.sec.slopCounts))
		for k, c := range w.sec.slopCounts {
			counts[k] = c
		}
		w.sec.mu.Unlock()
		return &contentNote{guard: SteerGuardAction, kind: SteerKindSlop, text: SlopSteer(where, symptoms, counts)}
	})
}
