package warden

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Tests of the steering and trace core (extension.ts steer 834-925, record 734-760, noteError 654; trace.ts).
// The Pi-harness case tests/steer-delivery.test.ts:61 (a steer sent from tool_call or tool_result for the last
// call of a turn rides the request the tool result needs) is a property of Pi's agent loop, not of the
// extension, and stays skipped.

// steerRig drives the internal API from inside a command handler, which is where an sdk.Context exists.
type steerRig struct {
	t *testing.T
	h *Host
	w *warden
}

func newSteerRig(t *testing.T, cfg Config, ui bool, adaptor steerAdaptor) *steerRig {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	e, w := newWarden(Options{Getenv: func(string) string { return "" }})
	if adaptor != nil {
		w.adaptor = adaptor
	}
	w.cfg, w.fileCfg, w.loaded = cfg, cfg, true
	r := &steerRig{t: t, w: w}
	e.Command("probe", "test probe", func(ctx sdk.Context, _ string) error { return r.fn(ctx) })
	r.h = StartHost(t, e, HostOptions{HasUI: &ui, Cwd: t.TempDir(), OnCall: func(m string, _ map[string]any) (map[string]any, string) {
		if m == "getModelInfo" {
			return map[string]any{"id": "mod", "provider": "prov", "name": "Mod"}, ""
		}
		return nil, ""
	}})
	return r
}

var rigFn func(ctx sdk.Context) error

func (r *steerRig) fn(ctx sdk.Context) error { return rigFn(ctx) }

// run executes f with a live context.
func (r *steerRig) run(f func(ctx sdk.Context)) {
	r.t.Helper()
	rigFn = func(ctx sdk.Context) error { f(ctx); return nil }
	if msg := r.h.Command("probe", ""); msg != "" {
		r.t.Fatal(msg)
	}
}

func (r *steerRig) steers() []map[string]any {
	var out []map[string]any
	for _, c := range r.h.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		if m["customType"] == steerType {
			out = append(out, c.Args)
		}
	}
	return out
}

func guards(g ...SteerGuard) []SteerGuard { return g }

var steerOpt = SteerOptions{DeliverAs: "steer"}

func TestSteerBudgetStopsNonCriticalAtTheLimit(t *testing.T) { // extension.ts:880-882
	cfg := DefaultConfig()
	cfg.SteerBudget = 2
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		w := r.w
		if !w.budgetAvailable(cfg) {
			t.Error("budget has room")
		}
		for i, want := range []bool{true, true, false} {
			if got := w.steer(cfg, guards(SteerGuardAction), ctx, "note "+string(rune('a'+i)), steerOpt); got != want {
				t.Errorf("steer %d = %v, want %v", i, got, want)
			}
		}
		if w.budgetAvailable(cfg) {
			t.Error("budget is spent")
		}
		if w.stats.Steers != 2 || w.stats.SteersSkipped != 1 {
			t.Errorf("stats %+v", w.stats)
		}
	})
	if n := len(r.steers()); n != 2 {
		t.Errorf("%d steers sent, want 2", n)
	}
}

func TestSteerBudgetZeroIsUnlimitedForSteerButNotAvailable(t *testing.T) { // extension.ts:834 `steerBudget > 0`
	cfg := DefaultConfig()
	cfg.SteerBudget = 0
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		if r.w.budgetAvailable(cfg) {
			t.Error("a zero budget has no room")
		}
		// upstream steer: a zero budget is "unlimited" for overBudget (steerBudget > 0 guard), so the steer delivers
		if !r.w.steer(cfg, guards(SteerGuardAction), ctx, "x", steerOpt) {
			t.Error("zero budget does not skip in steer")
		}
	})
}

func TestSpendBudgetUnitReservesOne(t *testing.T) { // extension.ts:838
	cfg := DefaultConfig()
	cfg.SteerBudget = 1
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		r.w.spendBudgetUnit()
		if r.w.budgetAvailable(cfg) {
			t.Error("one unit spent a budget of one")
		}
		if r.w.steer(cfg, guards(SteerGuardAction), ctx, "x", steerOpt) {
			t.Error("a spent budget skips a non-critical steer")
		}
	})
}

func TestCriticalGuardsBypassBudgetAndRepeats(t *testing.T) { // extension.ts:831,879
	cfg := DefaultConfig()
	cfg.SteerBudget = 1
	for _, g := range []SteerGuard{SteerGuardStuck, SteerGuardDone, SteerGuardRunaway, SteerGuardSubagent} {
		r := newSteerRig(t, cfg, false, nil)
		r.run(func(ctx sdk.Context) {
			r.w.steer(cfg, guards(SteerGuardAction), ctx, "fill", steerOpt)
			for i := 0; i < 2; i++ {
				if !r.w.steer(cfg, guards(g), ctx, "same", steerOpt) {
					t.Errorf("%s steer %d skipped", g, i)
				}
			}
		})
	}
	for _, g := range []SteerGuard{SteerGuardAction, SteerGuardRules, SteerGuardSecurity, SteerGuardRepeat, SteerGuardProse, SteerGuardConscience, SteerGuardLoops} {
		r := newSteerRig(t, cfg, false, nil)
		r.run(func(ctx sdk.Context) {
			r.w.steer(cfg, guards(SteerGuardAction), ctx, "fill", steerOpt)
			if r.w.steer(cfg, guards(g), ctx, "other", steerOpt) {
				t.Errorf("%s is not critical", g)
			}
		})
	}
}

func TestSteerIsCriticalOnlyWhenEveryGuardIs(t *testing.T) { // extension.ts:874 names.every
	cfg := DefaultConfig()
	cfg.SteerBudget = 1
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		r.w.steer(cfg, guards(SteerGuardAction), ctx, "fill", steerOpt)
		if !r.w.steer(cfg, guards(SteerGuardStuck, SteerGuardDone), ctx, "both critical", steerOpt) {
			t.Error("two critical guards deliver")
		}
		if r.w.steer(cfg, guards(SteerGuardStuck, SteerGuardAction), ctx, "mixed", steerOpt) {
			t.Error("a mixed steer is not critical")
		}
	})
}

func TestSteerRepeatWindowRecordsRepeatsOnly(t *testing.T) { // extension.ts:880
	cfg := DefaultConfig()
	cfg.SteerBudget = 10
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		w := r.w
		if !w.steer(cfg, guards(SteerGuardRules), ctx, "same notice", steerOpt) {
			t.Error("first delivers")
		}
		if w.steer(cfg, guards(SteerGuardRules), ctx, "same notice", steerOpt) {
			t.Error("a repeat is recorded only")
		}
		if w.stats.SteersSkipped != 1 {
			t.Errorf("skipped = %d", w.stats.SteersSkipped)
		}
	})
}

func TestSteerOverBudgetDoesNotSpendTheFingerprint(t *testing.T) { // extension.ts:869-873: a notice skipped for the budget can deliver next run
	cfg := DefaultConfig()
	cfg.SteerBudget = 1
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		w := r.w
		w.steer(cfg, guards(SteerGuardAction), ctx, "first", steerOpt)
		if w.steer(cfg, guards(SteerGuardAction), ctx, "second", steerOpt) {
			t.Error("over budget")
		}
		w.mu.Lock()
		w.steersThisRun = 0
		w.mu.Unlock()
		if !w.steer(cfg, guards(SteerGuardAction), ctx, "second", steerOpt) {
			t.Error("the budget-skipped notice delivers next run")
		}
	})
}

func TestSteerCountsEveryGuard(t *testing.T) { // extension.ts:873
	cfg := DefaultConfig()
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) {
		w := r.w
		w.steer(cfg, guards(SteerGuardRules, SteerGuardSecurity), ctx, "a", steerOpt)
		w.steer(cfg, guards(SteerGuardRules), ctx, "b", steerOpt)
		w.steer(cfg, guards(SteerGuardRules), ctx, "b", steerOpt) // skipped, still counted against the guard
		g := w.stats.SteerGuards
		if g[SteerGuardRules] != 3 || g[SteerGuardSecurity] != 1 || len(g) != 2 {
			t.Errorf("steerGuards = %v", g)
		}
		if w.stats.Steers != 2 || w.stats.SteersSkipped != 1 {
			t.Errorf("stats %+v", w.stats)
		}
	})
}

func TestSteerSendsTheWardenMessage(t *testing.T) { // extension.ts:885-891
	cfg := DefaultConfig()
	cfg.SteerVisible = true
	r := newSteerRig(t, cfg, false, nil)
	hidden := false
	r.run(func(ctx sdk.Context) {
		r.w.steer(cfg, guards(SteerGuardAction), ctx, "shown", steerOpt)
		r.w.steer(cfg, guards(SteerGuardAction), ctx, "hidden", SteerOptions{DeliverAs: "followUp", TriggerTurn: true, Display: &hidden})
	})
	s := r.steers()
	if len(s) != 2 {
		t.Fatalf("%d steers", len(s))
	}
	m0, _ := s[0]["message"].(map[string]any)
	m1, _ := s[1]["message"].(map[string]any)
	if m0["content"] != "shown" || m0["display"] != true {
		t.Errorf("first %v", m0)
	}
	if m1["display"] != false {
		t.Errorf("Display override ignored: %v", m1)
	}
	o1, _ := s[1]["options"].(map[string]any)
	if o1["deliverAs"] != "followUp" || o1["triggerTurn"] != true {
		t.Errorf("options %v", o1)
	}
	if !r.w.continuation {
		t.Error("triggerTurn marks the run a continuation")
	}
}

func TestSteerWithoutTriggerLeavesContinuation(t *testing.T) { // extension.ts:892
	cfg := DefaultConfig()
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) { r.w.steer(cfg, guards(SteerGuardAction), ctx, "x", steerOpt) })
	if r.w.continuation {
		t.Error("no triggerTurn, no continuation")
	}
}

func TestRecordReplacesTheGuardLineAndKeepsHistory(t *testing.T) { // extension.ts:735-741
	cfg := DefaultConfig()
	cfg.Enabled = true
	r := newSteerRig(t, cfg, true, nil)
	var e1 *TraceEntry
	r.run(func(ctx sdk.Context) {
		w := r.w
		e1 = w.record(ctx, cfg, GuardAction, "action one", []string{"d"}, map[string]string{"level": "allow"})
		w.record(ctx, cfg, GuardStuck, "stuck one", nil, nil)
		w.record(ctx, cfg, GuardAction, "action two", nil, nil)
	})
	if e1.Guard != GuardAction || e1.Line != "action one" || e1.Tokens["level"] != "allow" || e1.At.IsZero() {
		t.Errorf("entry %+v", e1)
	}
	var lines []string
	for _, wl := range r.w.widget {
		lines = append(lines, string(wl.guard)+"="+wl.line)
	}
	if got := strings.Join(lines, "|"); got != "stuck=stuck one|action=action two" {
		t.Errorf("widget (recency order, one line per guard) = %s", got)
	}
	if n := len(r.w.trace.Entries()); n != 3 {
		t.Errorf("trace keeps all three, has %d", n)
	}
}

func TestRecordPaintsNothingWithoutUI(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	r := newSteerRig(t, cfg, false, nil)
	r.run(func(ctx sdk.Context) { r.w.record(ctx, cfg, GuardDone, "x", nil, nil) })
	if len(r.h.CallsTo("ui.setWidget"))+len(r.h.CallsTo("ui.setStatus")) != 0 {
		t.Error("no UI, no paint")
	}
}

func TestRecordWidgetDisabledClearsTheWidget(t *testing.T) { // extension.ts:~770 `!config.widget.enabled`
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Widget.Enabled = false
	r := newSteerRig(t, cfg, true, nil)
	r.run(func(ctx sdk.Context) { r.w.record(ctx, cfg, GuardDone, "x", nil, nil) })
	calls := r.h.CallsTo("ui.setWidget")
	if len(calls) == 0 {
		t.Fatal("no paint")
	}
	if c := calls[len(calls)-1].Args["content"]; c != nil {
		t.Errorf("widget shows %v although disabled", c)
	}
}

func TestTraceAmendAppendsDetailsToLiveEntriesOnly(t *testing.T) { // trace.ts amend
	tr := newTrace(2)
	a, b, c := &TraceEntry{Line: "a"}, &TraceEntry{Line: "b"}, &TraceEntry{Line: "c"}
	var events []string
	tr.Subscribe(func(e TraceEvent) { events = append(events, e.Kind) })
	tr.Push(a)
	tr.Push(b)
	if !tr.Amend(b, "outcome") {
		t.Error("amend of a live entry")
	}
	tr.Push(c) // drops a
	if tr.Amend(a, "late") {
		t.Error("amend of a dropped entry must report false")
	}
	got := tr.Entries()
	if len(got) != 2 || got[0].Line != "b" || got[1].Line != "c" {
		t.Fatalf("entries %+v", got)
	}
	if len(got[0].Details) != 1 || got[0].Details[0] != "outcome" || len(a.Details) != 0 {
		t.Errorf("details %v / %v", got[0].Details, a.Details)
	}
	got[0].Details[0] = "mutated"
	if tr.Entries()[0].Details[0] != "outcome" {
		t.Error("Entries returns copies")
	}
	tr.Clear()
	if len(tr.Entries()) != 0 {
		t.Error("clear")
	}
	if strings.Join(events, ",") != "push,push,amend,push,clear" {
		t.Errorf("events %v", events)
	}
}

func TestTraceIsBoundedAtOneHundred(t *testing.T) { // extension.ts:396 new Trace(), trace.ts limit = 100
	tr := newTrace(traceLimit)
	for i := 0; i < 130; i++ {
		tr.Push(&TraceEntry{Line: strings.Repeat("x", i%3)})
	}
	if n := len(tr.Entries()); n != 100 {
		t.Errorf("%d entries", n)
	}
}

func TestWardenTraceCommandReadsTheStore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	r := newSteerRig(t, cfg, true, nil)
	r.run(func(ctx sdk.Context) { r.w.record(ctx, cfg, GuardDone, "warden · done-check · ok", nil, nil) })
	if msg := r.h.Command("warden", "trace"); msg != "" {
		t.Fatal(msg)
	}
	var shown string
	for _, c := range r.h.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		if m["customType"] == statusType {
			shown, _ = m["content"].(string)
		}
	}
	if !strings.HasSuffix(shown, " warden · done-check · ok") {
		t.Errorf("trace shows %q", shown)
	}
}

// --- adaptor hooks -----------------------------------------------------------------------------------

type fakeAdaptor struct {
	sendAllAdaptor
	decision SteerDecision
	asked    []string
	added    [][]SteerKind
	observed []SteerOutcome
}

func (f *fakeAdaptor) request(m string, k SteerKind, _ SteersConfig) SteerDecision {
	f.asked = append(f.asked, m+"|"+string(k))
	return f.decision
}
func (f *fakeAdaptor) pair(string, SteerKind) (SteerPair, bool) {
	return SteerPair{SteerCounts: SteerCounts{Sent: 40, Followed: 4, Disputed: 20}, Muted: true}, true
}
func (f *fakeAdaptor) add(_ string, k []SteerKind, _ SteerSubject, _ bool) {
	f.added = append(f.added, k)
}
func (f *fakeAdaptor) observe(_ string, _ SteerKind, o SteerOutcome, _ SteersConfig) {
	f.observed = append(f.observed, o)
}

func TestDefaultAdaptorSendsEverythingAndRecordsNothing(t *testing.T) { // extension.ts:906 no model / "send"
	cfg := DefaultConfig()
	cfg.Enabled = true
	r := newSteerRig(t, cfg, true, nil)
	r.run(func(ctx sdk.Context) {
		w := r.w
		if !w.adaptiveSend(ctx, cfg, SteerKindIntentMismatch) {
			t.Error("default adaptor sends")
		}
		w.watchSteer(ctx, cfg, SteerKinds, SteerSubject{}, false)
		w.settleSteers(cfg, []SteerOutcome{{Model: "m", Kind: SteerKindRules}})
		if w.stats.SteersMuted != 0 || len(w.trace.Entries()) != 0 {
			t.Error("default adaptor records nothing")
		}
		if got := w.modelKey(ctx); got != "prov/mod" {
			t.Errorf("modelKey = %q", got)
		}
	})
}

func TestAdaptiveSendTraceOnlyRecordsAndCounts(t *testing.T) { // extension.ts:906-921
	cfg := DefaultConfig()
	cfg.Enabled = true
	f := &fakeAdaptor{decision: SteerTraceOnly}
	r := newSteerRig(t, cfg, true, f)
	r.run(func(ctx sdk.Context) {
		w := r.w
		if w.adaptiveSend(ctx, cfg, SteerKindSensitivePath) {
			t.Error("trace-only must not send")
		}
		if w.stats.SteersMuted != 1 {
			t.Errorf("steersMuted = %d", w.stats.SteersMuted)
		}
		es := w.trace.Entries()
		if len(es) != 1 || es[0].Guard != GuardRules || es[0].Line != "warden · steer trace-only · sensitive-path · prov/mod" {
			t.Fatalf("trace %+v", es)
		}
		if !strings.Contains(es[0].Details[0], "(40 steers observed, 10% followed, 50% disputed); 1 in 5 is still sent") || es[0].Details[1] != "/warden unmute sensitive-path sends them again" {
			t.Errorf("details %q", es[0].Details)
		}
		w.adaptiveSend(ctx, cfg, SteerKindOffTask)
		if w.trace.Entries()[1].Guard != GuardAction {
			t.Error("an unmapped kind records under action")
		}
	})
	if len(f.asked) != 2 || f.asked[0] != "prov/mod|sensitive-path" {
		t.Errorf("asked %v", f.asked)
	}
}

func TestWatchSteerSkipsNeverMutedExceptDone(t *testing.T) { // extension.ts:925
	cfg := DefaultConfig()
	f := &fakeAdaptor{}
	r := newSteerRig(t, cfg, false, f)
	r.run(func(ctx sdk.Context) {
		r.w.watchSteer(ctx, cfg, []SteerKind{SteerKindHold, SteerKindDone, SteerKindRules, SteerKindRunaway}, SteerSubject{}, true)
		off := cfg
		off.Steers.Adaptive = false
		r.w.watchSteer(ctx, off, []SteerKind{SteerKindRules}, SteerSubject{}, false)
		r.w.settleSteers(cfg, []SteerOutcome{{Model: "m", Kind: SteerKindRules, Followed: true}})
	})
	if len(f.added) != 1 || len(f.added[0]) != 2 || f.added[0][0] != SteerKindDone || f.added[0][1] != SteerKindRules {
		t.Errorf("watched %v", f.added)
	}
	if len(f.observed) != 1 || !f.observed[0].Followed {
		t.Errorf("observed %v", f.observed)
	}
}

// --- noteError and the latest prompt -------------------------------------------------------------------

func TestNoteErrorCountsAndNotifies(t *testing.T) { // extension.ts:654-658
	cfg := DefaultConfig()
	r := newSteerRig(t, cfg, true, nil)
	r.run(func(ctx sdk.Context) {
		r.w.noteError(ctx, "judge failed.", "timeout")
		r.w.noteError(ctx, "budget gone.", "budget")
		r.w.noteError(ctx, "later.", "")
	})
	if r.w.stats.Errors != 3 || !r.w.budgetExhausted {
		t.Errorf("errors %d exhausted %v", r.w.stats.Errors, r.w.budgetExhausted)
	}
	var got []string
	for _, c := range r.h.CallsTo("ui.notify") {
		got = append(got, c.Args["message"].(string))
	}
	suffix := " Pattern checks continue without TypeSafe for the rest of this session."
	if len(got) != 3 || got[0] != "warden: judge failed." || got[1] != "warden: budget gone."+suffix || got[2] != "warden: later."+suffix {
		t.Errorf("notices %q", got)
	}
}

func TestNoteErrorWithoutUICountsOnly(t *testing.T) {
	r := newSteerRig(t, DefaultConfig(), false, nil)
	r.run(func(ctx sdk.Context) { r.w.noteError(ctx, "x", "") })
	if r.w.stats.Errors != 1 || len(r.h.CallsTo("ui.notify")) != 0 {
		t.Error("count, no notice")
	}
}

func TestSessionStartResetsCoreState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	r := newSteerRig(t, cfg, true, nil)
	r.run(func(ctx sdk.Context) {
		r.w.record(ctx, cfg, GuardDone, "x", nil, nil)
		r.w.steer(cfg, guards(SteerGuardAction), ctx, "x", steerOpt)
		r.w.noteError(ctx, "b", "budget")
	})
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	w := r.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stats.Steers != 0 || len(w.stats.SteerGuards) != 0 || w.stats.Errors != 0 || w.budgetExhausted || len(w.widget) != 0 || len(w.trace.Entries()) != 0 {
		t.Errorf("not reset: %+v widget=%v", w.stats, w.widget)
	}
}
