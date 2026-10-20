package warden

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The steering and trace core every guard shares (port/oracle/src/extension.ts record 734-760, steer and its
// helpers 834-925, noteError 654; trace.ts; adaptive.ts types). Guards never call ctx.SendMessage for a
// notice themselves: they call w.steer, which counts the guard, applies the per-run budget and the repeat
// window, and sends the `pi-warden-steer` message. They never touch the trace or the widget directly: they
// call w.record.

// GuardName names a guard in the trace and the widget (trace.ts GuardName).
type GuardName string

const (
	GuardAction     GuardName = "action"
	GuardStuck      GuardName = "stuck"
	GuardDone       GuardName = "done"
	GuardProse      GuardName = "prose"
	GuardSecurity   GuardName = "security"
	GuardContext    GuardName = "context"
	GuardRunaway    GuardName = "runaway"
	GuardRules      GuardName = "rules"
	GuardSubagent   GuardName = "subagent"
	GuardConscience GuardName = "conscience"
	GuardWaste      GuardName = "waste"
)

// SteerGuard is the guard a steer is counted against (extension.ts:100).
type SteerGuard string

const (
	SteerGuardAction     SteerGuard = "action"
	SteerGuardRules      SteerGuard = "rules"
	SteerGuardSecurity   SteerGuard = "security"
	SteerGuardStuck      SteerGuard = "stuck"
	SteerGuardRepeat     SteerGuard = "repeat"
	SteerGuardDone       SteerGuard = "done"
	SteerGuardProse      SteerGuard = "prose"
	SteerGuardRunaway    SteerGuard = "runaway"
	SteerGuardSubagent   SteerGuard = "subagent"
	SteerGuardConscience SteerGuard = "conscience"
	SteerGuardLoops      SteerGuard = "loops"
)

// criticalSteerGuards gate the run itself; they deliver even when the per-run steer budget is spent
// (extension.ts:831).
var criticalSteerGuards = map[SteerGuard]bool{SteerGuardStuck: true, SteerGuardDone: true, SteerGuardRunaway: true, SteerGuardSubagent: true}

// SteerKind is one kind of note to the agent, finer than the steer guard (adaptive.ts:16).
type SteerKind string

const (
	SteerKindIntentMismatch   SteerKind = "intent-mismatch"
	SteerKindOffTask          SteerKind = "off-task"
	SteerKindShouldProceed    SteerKind = "should-proceed"
	SteerKindWarnHeadless     SteerKind = "warn-headless"
	SteerKindLargeOutput      SteerKind = "large-output"
	SteerKindSlop             SteerKind = "slop"
	SteerKindRules            SteerKind = "rules"
	SteerKindSensitivePath    SteerKind = "sensitive-path"
	SteerKindSecurityWrite    SteerKind = "security-write"
	SteerKindStuck            SteerKind = "stuck"
	SteerKindRepeat           SteerKind = "repeat"
	SteerKindDone             SteerKind = "done"
	SteerKindProse            SteerKind = "prose"
	SteerKindConscience       SteerKind = "conscience"
	SteerKindLoops            SteerKind = "loops"
	SteerKindRunaway          SteerKind = "runaway"
	SteerKindSubagent         SteerKind = "subagent"
	SteerKindHold             SteerKind = "hold"
	SteerKindConfirm          SteerKind = "confirm"
	SteerKindDeny             SteerKind = "deny"
	SteerKindMasking          SteerKind = "masking"
	SteerKindCredentialNotice SteerKind = "credential-notice"
)

// SteerKinds lists every kind in upstream order (adaptive.ts STEER_KINDS).
var SteerKinds = []SteerKind{
	SteerKindIntentMismatch, SteerKindOffTask, SteerKindShouldProceed, SteerKindWarnHeadless, SteerKindLargeOutput,
	SteerKindSlop, SteerKindRules, SteerKindSensitivePath, SteerKindSecurityWrite, SteerKindStuck, SteerKindRepeat,
	SteerKindDone, SteerKindProse, SteerKindConscience, SteerKindLoops, SteerKindRunaway, SteerKindSubagent,
	SteerKindHold, SteerKindConfirm, SteerKindDeny, SteerKindMasking, SteerKindCredentialNotice,
}

// NeverMuted: kinds that are never trace-only (adaptive.ts NEVER_MUTED). watchSteer needs it; the adaptive lane
// owns it from here.
var NeverMuted = map[SteerKind]bool{
	SteerKindHold: true, SteerKindConfirm: true, SteerKindDeny: true, SteerKindMasking: true,
	SteerKindCredentialNotice: true, SteerKindDone: true, SteerKindRunaway: true, SteerKindSubagent: true,
}

// kindGuard maps a steer kind to the guard whose widget line records it (extension.ts KIND_GUARD); a kind
// that is not listed records under the action guard.
var kindGuard = map[SteerKind]GuardName{
	SteerKindRules: GuardRules, SteerKindSensitivePath: GuardRules, SteerKindSecurityWrite: GuardSecurity,
	SteerKindStuck: GuardStuck, SteerKindRepeat: GuardStuck, SteerKindProse: GuardProse, SteerKindConscience: GuardConscience,
}

// ---------------------------------------------------------------------------
// Session counters (extension.ts:102 Stats; the /warden status Session line prints them).

type stats struct {
	Inspected, Judged, Warned, Held, Approved, OffPlan, OffPlanTraceOnly, OffTask, Slop int
	RuleChecks, RuleViolations, PathNotes, StuckChecks, Stuck, DoneChecks, Unverified   int
	ProseChecks, ProseNudges, Runaway, Errors, CooldownSkips                            int
	Steers, SteersSkipped, SteersMuted                                                  int
	// SteerGuards counts every steer against the guard that asked for it; one steer may carry several.
	SteerGuards                                  map[SteerGuard]int
	SubagentReports, SubagentWoken, Restatements int
}

func newStats() stats { return stats{SteerGuards: map[SteerGuard]int{}} }

// ---------------------------------------------------------------------------
// The trace (trace.ts): session memory of guard decisions.

// TraceEntry is one guard decision.
type TraceEntry struct {
	At    time.Time
	Guard GuardName
	// Line is the widget line for this event.
	Line string
	// Details are redacted: what was inspected, what the judge answered, what the agent was told.
	Details []string
	// Tokens are the raw template values, for re-rendering in live mode.
	Tokens map[string]string
}

// TraceEvent tells a listener (the trace file) what changed.
type TraceEvent struct {
	Kind  string // push | amend | clear
	Entry *TraceEntry
	Line  string
}

// traceLimit is the history bound (trace.ts: `new Trace()` keeps 100).
const traceLimit = 100

// Trace keeps the last traceLimit entries; the widget shows the latest line per guard, /warden trace the history.
type Trace struct {
	mu        sync.Mutex
	items     []*TraceEntry
	limit     int
	listeners map[int]func(TraceEvent)
	nextID    int
}

func newTrace(limit int) *Trace { return &Trace{limit: limit, listeners: map[int]func(TraceEvent){}} }

func (t *Trace) emit(e TraceEvent) {
	for _, l := range t.listeners {
		l(e)
	}
}

// Push appends an entry and drops the oldest past the limit.
func (t *Trace) Push(e *TraceEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items = append(t.items, e)
	if len(t.items) > t.limit {
		t.items = append([]*TraceEntry(nil), t.items[len(t.items)-t.limit:]...)
	}
	t.emit(TraceEvent{Kind: "push", Entry: e})
}

// Amend appends a detail line to an entry still in the trace (an outcome that lands after the event); false
// when the entry has been dropped.
func (t *Trace) Amend(e *TraceEntry, note string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, it := range t.items {
		if it == e {
			e.Details = append(e.Details, note)
			t.emit(TraceEvent{Kind: "amend", Entry: e, Line: note})
			return true
		}
	}
	return false
}

// Entries returns a snapshot, oldest first.
func (t *Trace) Entries() []TraceEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TraceEntry, len(t.items))
	for i, it := range t.items {
		out[i] = *it
		out[i].Details = append([]string(nil), it.Details...)
	}
	return out
}

// Clear empties the trace.
func (t *Trace) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items = nil
	t.emit(TraceEvent{Kind: "clear"})
}

// Subscribe registers a listener; the returned function removes it. A listener must not call back into the Trace.
func (t *Trace) Subscribe(l func(TraceEvent)) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextID
	t.nextID++
	t.listeners[id] = l
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.listeners, id)
	}
}

// widgetLine is one guard's latest line; the widget keeps them in recency order (a Map delete+set).
type widgetLine struct {
	guard GuardName
	line  string
}

// record replaces the guard's widget line, appends to the trace and repaints (extension.ts record).
func (w *warden) record(ctx sdk.Context, cfg Config, guard GuardName, line string, details []string, tokens map[string]string) *TraceEntry {
	entry := &TraceEntry{At: w.opts.Now(), Guard: guard, Line: line, Details: append([]string(nil), details...), Tokens: tokens}
	w.mu.Lock()
	for i, wl := range w.widget {
		if wl.guard == guard {
			w.widget = append(w.widget[:i:i], w.widget[i+1:]...)
			break
		}
	}
	w.widget = append(w.widget, widgetLine{guard, line})
	w.mu.Unlock()
	w.trace.Push(entry)
	w.paint(ctx, cfg)
	return entry
}

// ---------------------------------------------------------------------------
// Steering.

// SteerOptions are sendMessage's delivery options. An empty DeliverAs leaves the choice to the host.
type SteerOptions struct {
	DeliverAs   string // steer | followUp | nextTurn
	TriggerTurn bool
	// Display overrides cfg.SteerVisible when set.
	Display *bool
}

// budgetAvailable: a non-critical steer may deliver within the per-run budget (extension.ts budgetAvailable).
func (w *warden) budgetAvailable(cfg Config) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return cfg.SteerBudget > 0 && w.steersThisRun < cfg.SteerBudget
}

// spendBudgetUnit reserves one unit; call only after budgetAvailable returned true.
func (w *warden) spendBudgetUnit() {
	w.mu.Lock()
	w.steersThisRun++
	w.mu.Unlock()
}

// steer sends the agent one notice and reports whether it was delivered; false means it was recorded only.
// Every guard is counted. A notice delivered once is already in the agent's context, so a repeat is recorded
// only; so is a notice past the per-run budget (each one asks the agent for a reply sentence, and a closing run
// that collects six notices collects six restatements). A notice skipped for the budget keeps its
// fingerprint unrecorded (the repeat window is consulted only when the budget has room), so it can deliver next run. Critical guards (stuck, done, runaway,
// subagent) always deliver: their message starts the turn. TriggerTurn marks the run a continuation.
func (w *warden) steer(cfg Config, guards []SteerGuard, ctx sdk.Context, content string, opt SteerOptions) bool {
	w.mu.Lock()
	if w.stats.SteerGuards == nil {
		w.stats.SteerGuards = map[SteerGuard]int{}
	}
	critical := true
	for _, g := range guards {
		w.stats.SteerGuards[g]++
		if !criticalSteerGuards[g] {
			critical = false
		}
	}
	over := cfg.SteerBudget > 0 && w.steersThisRun >= cfg.SteerBudget
	deliver := critical || (!over && !w.repeats.Seen(content))
	if !deliver {
		w.stats.SteersSkipped++
		w.mu.Unlock()
		return false
	}
	w.stats.Steers++
	w.steersThisRun++
	if opt.TriggerTurn {
		w.continuation = true
	}
	w.mu.Unlock()
	opts := sdk.SendMessageOptions{DeliverAs: opt.DeliverAs}
	if opt.TriggerTurn {
		yes := true
		opts.TriggerTurn = &yes
	}
	display := cfg.SteerVisible
	if opt.Display != nil {
		display = *opt.Display
	}
	if err := ctx.SendMessage(steerType, content, display, opts); err != nil {
		w.mu.Lock()
		w.continuation = false
		w.mu.Unlock()
		return false
	}
	return true
}

// modelKey names the session model as "provider/id", or "" when none is selected (extension.ts modelKey).
func (w *warden) modelKey(ctx sdk.Context) string {
	info, err := ctx.GetModelInfo()
	if err != nil || info == nil {
		return ""
	}
	return info.Provider + "/" + info.ID
}

// ---------------------------------------------------------------------------
// Adaptive steering hook points (adaptive.ts SteerStats and SteerWatch). The default adaptor sends everything
// and records nothing; the adaptive lane installs the real one in w.adaptor.

// SteerDecision is SteerStats.request's answer.
type SteerDecision string

const (
	SteerSend      SteerDecision = "send"
	SteerTraceOnly SteerDecision = "trace-only"
)

// SteerCounts are follow and dispute counts.
type SteerCounts struct{ Sent, Followed, Disputed int }

// SteerPair is the state of one (model, kind) pair (adaptive.ts PairState).
type SteerPair struct {
	SteerCounts
	Muted bool
	// Asked: steers asked for since the pair became trace-only or was last re-checked.
	Asked int
	Probe SteerCounts
}

// MutedPair is one trace-only pair, for the status (adaptive.ts MutedPair).
type MutedPair struct {
	Model string
	Kind  SteerKind
	SteerCounts
}

// SteerCall is a tool call named by a steer or made after it.
type SteerCall struct {
	Name  string
	Input map[string]any
}

// SteerCapability is a tool or skill the conscience named.
type SteerCapability struct{ Kind, ID string }

// SteerSubject is what a steer was about, for the follow check (adaptive.ts SteerSubject).
type SteerSubject struct {
	Path       string
	Call       *SteerCall
	Capability *SteerCapability
}

// AssistantView is one assistant message after a steer: its text and tool calls.
type AssistantView struct {
	Text  string
	Calls []SteerCall
}

// SteerOutcome is how one watched (model, kind) steer settled.
type SteerOutcome struct {
	Model              string
	Kind               SteerKind
	Followed, Disputed bool
}

// steerStatsStore mirrors SteerStats.
type steerStatsStore interface {
	request(model string, kind SteerKind, cfg SteersConfig) SteerDecision
	pair(model string, kind SteerKind) (SteerPair, bool)
	observe(model string, kind SteerKind, outcome SteerOutcome, cfg SteersConfig)
	unmute(model string, kind SteerKind) bool
	muted() []MutedPair
	modelsWith(kind SteerKind) []string
}

// steerWatcher mirrors SteerWatch.
type steerWatcher interface {
	add(model string, kinds []SteerKind, subject SteerSubject, nextTurn bool)
	user() []SteerOutcome
	assistant(view AssistantView) []SteerOutcome
}

// steerAdaptor is what the adaptive lane implements.
type steerAdaptor interface {
	steerStatsStore
	steerWatcher
}

// sendAllAdaptor is the default: every steer is sent and nothing is counted.
type sendAllAdaptor struct{}

func (sendAllAdaptor) request(string, SteerKind, SteersConfig) SteerDecision { return SteerSend }
func (sendAllAdaptor) pair(string, SteerKind) (SteerPair, bool)              { return SteerPair{}, false }
func (sendAllAdaptor) observe(string, SteerKind, SteerOutcome, SteersConfig) {}
func (sendAllAdaptor) unmute(string, SteerKind) bool                         { return false }
func (sendAllAdaptor) muted() []MutedPair                                    { return nil }
func (sendAllAdaptor) modelsWith(SteerKind) []string                         { return nil }
func (sendAllAdaptor) add(string, []SteerKind, SteerSubject, bool)           {}
func (sendAllAdaptor) user() []SteerOutcome                                  { return nil }
func (sendAllAdaptor) assistant(AssistantView) []SteerOutcome                { return nil }

// jsRound is Math.round for the non-negative values the rates take.
func jsRound(x float64) int { return int(math.Floor(x + 0.5)) }

// adaptiveSend is false when steers of this kind are trace-only for the current model; the skipped note gets a
// trace line. Holds and judgments are not affected: only whether the note reaches the agent (extension.ts adaptiveSend).
func (w *warden) adaptiveSend(ctx sdk.Context, cfg Config, kind SteerKind) bool {
	model := w.modelKey(ctx)
	if model == "" {
		return true
	}
	if w.adaptor.request(model, kind, cfg.Steers) != SteerTraceOnly {
		return true
	}
	rates := "no counts"
	if p, ok := w.adaptor.pair(model, kind); ok {
		sent := float64(max(1, p.Sent))
		rates = strconv.Itoa(p.Sent) + " steers observed, " + strconv.Itoa(jsRound(float64(p.Followed)/sent*100)) + "% followed, " + strconv.Itoa(jsRound(float64(p.Disputed)/sent*100)) + "% disputed"
	}
	w.bump(func(s *stats) { s.SteersMuted++ })
	guard, ok := kindGuard[kind]
	if !ok {
		guard = GuardAction
	}
	w.record(ctx, cfg, guard, "warden · steer trace-only · "+string(kind)+" · "+model, []string{
		string(kind) + " steers are trace-only for " + model + " (" + rates + "); 1 in " + strconv.Itoa(cfg.Steers.ProbeEvery) + " is still sent for the re-check",
		"/warden unmute " + string(kind) + " sends them again",
	}, nil)
	return false
}

// watchSteer waits for the agent's next two messages after a delivered steer; the outcome is counted for the
// (model, kind) pair. Kinds that are never muted are not watched, except the done-check.
func (w *warden) watchSteer(ctx sdk.Context, cfg Config, kinds []SteerKind, subject SteerSubject, nextTurn bool) {
	model := w.modelKey(ctx)
	if model == "" || !cfg.Steers.Adaptive {
		return
	}
	var watched []SteerKind
	for _, k := range kinds {
		if !NeverMuted[k] || k == SteerKindDone {
			watched = append(watched, k)
		}
	}
	w.adaptor.add(model, watched, subject, nextTurn)
}

// settleSteers counts settled outcomes for their (model, kind) pairs.
func (w *warden) settleSteers(cfg Config, outcomes []SteerOutcome) {
	for _, o := range outcomes {
		w.adaptor.observe(o.Model, o.Kind, o, cfg.Steers)
	}
}

// ---------------------------------------------------------------------------
// Errors and the latest prompt.

// noteError counts a guard error and tells the user (extension.ts:654). Code "budget" means the judge budget is
// spent; the notice then says pattern checks continue without the judge.
func (w *warden) noteError(ctx sdk.Context, message, code string) {
	w.mu.Lock()
	w.stats.Errors++
	if code == "budget" {
		w.budgetExhausted = true
	}
	exhausted := w.budgetExhausted
	w.mu.Unlock()
	if ctx.HasUI() {
		suffix := ""
		if exhausted {
			suffix = " Pattern checks continue without TypeSafe for the rest of this session."
		}
		ctx.Notify("warden: "+message+suffix, "warning")
	}
}

// latestUserPrompt is the text of the newest user message on the session branch (extension.ts latestUserPrompt).
func (w *warden) latestUserPrompt(ctx sdk.Context) (string, bool) {
	branch := w.branchOf(ctx)
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == "message" && branch[i].Role == "user" {
			return strings.TrimSpace(branch[i].Content), true
		}
	}
	return "", false
}
