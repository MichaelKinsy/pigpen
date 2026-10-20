package warden

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Session state and event hooks of the guard core (extension.ts: `arming`, `sessionScratch`, `scratchPending`,
// `largeOutputSteered`, `sessionHostPaths`, the one-time config notices). Every hook is one call from the event handler
// that owns the moment, so another module's change to that handler stays line-local.

type scratchPendingCall struct {
	started    int64
	candidates []string
}

type guardcoreState struct {
	mu sync.Mutex
	// arming is the session's arming tracker; its rules come from the config on each call.
	arming *ArmingTracker
	// scratch are real paths the agent created under the temp directory in this session.
	scratch ScratchRecords
	// scratchPending are the temp paths a bash/write call may create, taken before it runs.
	scratchPending map[string]scratchPendingCall
	// largeOutputSteered are the command families already told to print less.
	largeOutputSteered map[string]bool
	// hostPaths are the real paths of the host directories a write may reach without the outside-project hold.
	hostPaths []string
	// once flags: said once per process, like upstream's per-load flags.
	exemptReported, inertReported, unparseableReported bool
	// previousActions yields the allowed calls of the previous turn for the regret question (the learning module sets
	// it); nil asks no regret question.
	previousActions func() []PreviousAction
}

func newGuardcoreState() guardcoreState {
	return guardcoreState{
		arming:             NewArmingTracker(nil, nil),
		scratch:            ScratchRecords{},
		scratchPending:     map[string]scratchPendingCall{},
		largeOutputSteered: map[string]bool{},
	}
}

// wardenIndexDir is the directory a /warden index command writes: the only place outside the project a warden command
// asks the agent to write. The conscience module owns the directory; until it says otherwise it is `<data dir>/index`.
var wardenIndexDir = func(configHome string) string { return filepath.Join(DataDir(configHome), "index") }

// gcSessionStart clears the session's arming, scratch and large-output memory and reads the host paths.
func (w *warden) gcSessionStart(ctx sdk.Context) {
	index := ""
	if home, err := ctx.ConfigHome(); err == nil {
		index = wardenIndexDir(home)
	}
	roots := wardenHostPaths(w.opts.Getenv, index)
	w.gc.mu.Lock()
	w.gc.arming.Reset()
	w.gc.scratch = ScratchRecords{}
	w.gc.scratchPending = map[string]scratchPendingCall{}
	w.gc.largeOutputSteered = map[string]bool{}
	w.gc.hostPaths = roots
	w.gc.mu.Unlock()
}

// gcConfigNotices says once what the rule lists carry that cannot work: an exemptRules id that matches nothing, a path
// rule that can never fire, an arming rule whose regex does not compile.
func (w *warden) gcConfigNotices(ctx sdk.Context, cfg Config) {
	a := cfg.Action
	var texts []string
	w.gc.mu.Lock()
	if unknown := UnknownExemptIds(a.ExemptRules, a.CommandRules, a.CommandDenyRules, a.PathRules, a.ArmingRules); len(unknown) > 0 && !w.gc.exemptReported {
		w.gc.exemptReported = true
		texts = append(texts, "warden: exemptRules names "+strings.Join(unknown, ", ")+", which match no built-in or user rule; those entries are inert")
	}
	if inert := inertPathRules(a.PathRules, a.Tools); len(inert) > 0 && !w.gc.inertReported {
		w.gc.inertReported = true
		texts = append(texts, "warden: path rules "+strings.Join(inert, ", ")+" can never fire with the current access/tools combination; check access polarity or add the tool to action.tools")
	}
	if bad := unparseableArmingRules(a.ArmingRules); len(bad) > 0 && !w.gc.unparseableReported {
		// A separate flag, so an operator with both an inert path rule and a bad arming regex sees both warnings.
		w.gc.unparseableReported = true
		texts = append(texts, "warden: arming rules "+strings.Join(bad, ", ")+" have an invalid command regex; the rules will never fire")
	}
	w.gc.arming.UpdateRules(a.ArmingRules)
	w.gc.mu.Unlock()
	for _, text := range texts {
		w.say(ctx, text, "warning")
	}
}

// gcToolCallStart notes the temp paths a bash or write call may create, before it runs (the birth-time exemption reads
// them after), and says the config notices.
func (w *warden) gcToolCallStart(ctx sdk.Context, cfg Config, id, tool string, input map[string]any) {
	w.gcConfigNotices(ctx, cfg)
	if tool != "bash" && tool != "write" {
		return
	}
	pending := scratchPendingCall{started: time.Now().UnixMilli(), candidates: scratchCandidates(tool, input, ctx.Cwd(), w.opts.ScratchPlatform)}
	w.gc.mu.Lock()
	w.gc.scratchPending[id] = pending
	w.gc.mu.Unlock()
}

// gcArm: a write/edit to a protected path arms matching command patterns for a window. Bash redirect and tee targets that
// match a when.edited glob also arm. This is session state, not a per-call verdict: it runs before the action guard so
// the armed check on a later command sees the preparation.
func (w *warden) gcArm(ctx sdk.Context, cfg Config, tool string, input map[string]any) {
	if len(cfg.Action.ArmingRules) == 0 {
		return
	}
	path, _ := input["path"].(string)
	var sinks []string
	if view, ok := CommandOf(tool, input); ok && view.Command != "" {
		// Parity with the armed check: strip data text before extracting write-sink targets, so a redirect mentioned
		// inside a data heredoc (no shell sink) does not arm, but a real redirect in an executed heredoc does.
		sinks = writeSinkTargets(StripDataText(view.Command).Text)
	}
	w.gc.arming.Arm(tool, path, ctx.Cwd(), sinks)
}

// gcExtras are the optional inputs of the action guard for one call: the config sections that add questions, the
// session's scratch and host paths, and last turn's allowed calls.
func (w *warden) gcExtras(cfg Config) GuardExtras {
	w.gc.mu.Lock()
	scratch := make(ScratchRecords, len(w.gc.scratch))
	for k, v := range w.gc.scratch {
		scratch[k] = v
	}
	roots := w.gc.hostPaths
	previous := w.gc.previousActions
	w.gc.mu.Unlock()
	extras := GuardExtras{Slop: &cfg.Slop, LargeOutput: &cfg.Context.LargeOutput, Security: &cfg.Security, Rules: &cfg.Rules, Scratch: scratch, HostPaths: roots, Platform: w.opts.ScratchPlatform}
	if previous != nil {
		extras.PreviousActions = previous()
	}
	return extras
}

// gcArmed fires the armed rules whose command regex matches this call: a deterministic hit, whether or not the judge
// answered. hold becomes a steer-mode hold, confirm a dialog, block a deny. exemptRules silence an armed rule by its own
// id (the injected hit is `armed:<id>`).
func (w *warden) gcArmed(cfg Config, tool string, input map[string]any, v Verdict) Verdict {
	if len(cfg.Action.ArmingRules) == 0 {
		return v
	}
	view, ok := CommandOf(tool, input)
	if !ok || view.Command == "" {
		return v
	}
	var visible []ArmedHit
	for _, hit := range w.gc.arming.CheckArmed(view.Command) {
		if !slices.Contains(cfg.Action.ExemptRules, hit.ID) {
			visible = append(visible, hit)
		}
	}
	if len(visible) == 0 {
		return v
	}
	patterns := append([]PatternHit(nil), v.Patterns...)
	reasons := append([]string(nil), v.Reasons...)
	level := v.Level
	for _, hit := range visible {
		severity := SeverityDestructive
		if hit.Action == "block" {
			severity = SeverityDeny
		}
		label := orDefault(hit.Message, "armed rule "+hit.ID)
		pattern := PatternHit{ID: "armed:" + hit.ID, Severity: severity, Label: label, Message: hit.Message}
		switch hit.Action {
		case "confirm":
			pattern.Action = "dialog"
		case "hold":
			pattern.Action = "hold"
		}
		patterns = append(patterns, pattern)
		// The confirm dialog names the files that armed it.
		armedBy := ""
		if len(hit.ArmedByPaths) > 0 {
			armedBy = " (armed by: " + strings.Join(hit.ArmedByPaths, ", ") + ")"
		}
		if severity == SeverityDeny {
			level = LevelDeny
			reasons = append(reasons, label+armedBy)
		} else {
			level = Higher(level, LevelConfirm)
			reasons = append(reasons, "armed: "+orDefault(hit.Message, hit.ID)+armedBy)
		}
	}
	v.Patterns, v.Reasons = patterns, reasons
	v.Level = Higher(v.Level, level)
	// An armed hit on a read-only command still records a trace line.
	if v.Source == "read-only" {
		v.Source = "pattern"
	}
	return v
}

// gcLargeOutput is the large-output steer for this verdict, once per command family per session: "" when there is nothing to say.
func (w *warden) gcLargeOutput(v Verdict) string {
	w.gc.mu.Lock()
	defer w.gc.mu.Unlock()
	return largeOutputNotice(v, w.gc.largeOutputSteered)
}

// gcToolResult records what a finished bash/write call created under the temp directory, and drops records whose path
// is gone or replaced.
func (w *warden) gcToolResult(id, tool string, input map[string]any, output string) {
	w.gc.mu.Lock()
	defer w.gc.mu.Unlock()
	if pending, ok := w.gc.scratchPending[id]; ok {
		delete(w.gc.scratchPending, id)
		for path, identity := range createdScratch(tool, input, output, pending.started, pending.candidates, nil, w.opts.ScratchPlatform) {
			w.gc.scratch[path] = identity
		}
	}
	pruneScratch(w.gc.scratch)
}

// gcArmingStatus is the status line of the arming rules.
func (w *warden) gcArmingStatus(cfg Config) string {
	if len(cfg.Action.ArmingRules) == 0 {
		return ""
	}
	line := w.gc.arming.StatusLine()
	if line == "" {
		line = "no rules armed"
	}
	return "Arming: " + line + "."
}
