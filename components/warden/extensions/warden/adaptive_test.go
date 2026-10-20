package warden

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	modelA = "provider/model-a"
	modelB = "provider/model-b"
)

func newSteerStatsT(t *testing.T) *SteerStats {
	t.Helper()
	return NewSteerStats(filepath.Join(t.TempDir(), "stats.json"))
}

// feedSteers: n observed steers, the first followed of them followed and the next disputed disputed.
func feedSteers(s *SteerStats, model string, kind SteerKind, n, followed, disputed int, cfg SteersConfig) {
	for i := range n {
		s.observe(model, kind, SteerOutcome{Followed: i < followed, Disputed: i >= followed && i < followed+disputed}, cfg)
	}
}

func mustPair(t *testing.T, s *SteerStats, model string, kind SteerKind) SteerPair {
	t.Helper()
	p, ok := s.pair(model, kind)
	if !ok {
		t.Fatalf("no pair for %s %s", model, kind)
	}
	return p
}

func isMuted(s *SteerStats, model string, kind SteerKind) bool {
	p, ok := s.pair(model, kind)
	return ok && p.Muted
}

// twin: tests/adaptive.test.ts:23
func TestAdaptiveNoPairMutedUnderMinSteers(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindIntentMismatch, 29, 0, 29, cfg)
	if isMuted(s, modelA, SteerKindIntentMismatch) {
		t.Fatal("muted under 30")
	}
	if d := s.request(modelA, SteerKindIntentMismatch, cfg); d != SteerSend {
		t.Fatalf("decision %q", d)
	}
	if len(s.muted()) != 0 {
		t.Fatal("muted list")
	}
}

// twin: tests/adaptive.test.ts:31
func TestAdaptiveMutedAtThresholdsNotAtTheThresholdsThemselves(t *testing.T) {
	cfg := defaultSteersConfig()
	followLow := newSteerStatsT(t)
	feedSteers(followLow, modelA, SteerKindIntentMismatch, 30, 5, 0, cfg) // 16.7% followed
	if !isMuted(followLow, modelA, SteerKindIntentMismatch) {
		t.Fatal("16.7% followed should mute")
	}
	if d := followLow.request(modelA, SteerKindIntentMismatch, cfg); d != SteerProbe {
		t.Fatalf("the first steer after muting is a probe, got %q", d)
	}
	if d := followLow.request(modelA, SteerKindIntentMismatch, cfg); d != SteerTraceOnly {
		t.Fatalf("got %q", d)
	}
	followAt := newSteerStatsT(t)
	feedSteers(followAt, modelA, SteerKindIntentMismatch, 30, 6, 0, cfg) // exactly 20% followed
	if isMuted(followAt, modelA, SteerKindIntentMismatch) {
		t.Fatal("exactly 20% must not mute")
	}
	disputed := newSteerStatsT(t)
	feedSteers(disputed, modelA, SteerKindRules, 30, 15, 13, cfg) // 50% followed, 43% disputed
	if !isMuted(disputed, modelA, SteerKindRules) {
		t.Fatal("43% disputed should mute")
	}
	disputedAt := newSteerStatsT(t)
	feedSteers(disputedAt, modelA, SteerKindRules, 30, 15, 12, cfg) // exactly 40% disputed
	if isMuted(disputedAt, modelA, SteerKindRules) {
		t.Fatal("exactly 40% must not mute")
	}
}

// twin: tests/adaptive.test.ts:51 (the loader's validation of steers overrides belongs to the config lane; this twin
// covers the defaults and the thresholds being read from the config the store is given)
func TestAdaptiveThresholdsComeFromConfig(t *testing.T) {
	custom := SteersConfig{Adaptive: true, MinSteers: 10, MinFollowed: 0.5, MaxDisputed: 0.1, RecheckEvery: 20, ProbeEvery: 4}
	if DefaultConfig().Steers != defaultSteersConfig() || defaultSteersConfig() != (SteersConfig{Adaptive: true, MinSteers: 30, MinFollowed: 0.2, MaxDisputed: 0.4, RecheckEvery: 30, ProbeEvery: 5}) {
		t.Fatalf("defaults %+v", DefaultConfig().Steers)
	}
	s := newSteerStatsT(t)
	for i := range 10 {
		s.observe(modelA, SteerKindRules, SteerOutcome{Followed: i < 4}, custom)
	}
	if !isMuted(s, modelA, SteerKindRules) {
		t.Fatal("40% followed is under a 50% floor")
	}
}

// twin: tests/adaptive.test.ts:61
func TestAdaptiveOffSendsEverythingCountsNothing(t *testing.T) {
	off := defaultSteersConfig()
	off.Adaptive = false
	s := newSteerStatsT(t)
	for range 40 {
		s.observe(modelA, SteerKindRules, SteerOutcome{Disputed: true}, off)
	}
	if _, ok := s.pair(modelA, SteerKindRules); ok {
		t.Fatal("counted while off")
	}
	feedSteers(s, modelA, SteerKindRules, 30, 0, 0, defaultSteersConfig())
	if d := s.request(modelA, SteerKindRules, off); d != SteerSend {
		t.Fatalf("a muted pair is sent when adaptation is off, got %q", d)
	}
}

// twin: tests/adaptive.test.ts:70
func TestAdaptiveNeverMutedSet(t *testing.T) {
	cfg := defaultSteersConfig()
	for _, k := range []SteerKind{SteerKindHold, SteerKindConfirm, SteerKindDeny, SteerKindMasking, SteerKindCredentialNotice, SteerKindDone} {
		if !NeverMuted[k] {
			t.Errorf("%s not in the never-muted set", k)
		}
	}
	for _, k := range []SteerKind{SteerKindIntentMismatch, SteerKindRules, SteerKindSlop, SteerKindProse} {
		if NeverMuted[k] {
			t.Errorf("%s in the never-muted set", k)
		}
	}
	s := newSteerStatsT(t)
	for k := range NeverMuted {
		feedSteers(s, modelA, k, 60, 0, 60, cfg)
		if isMuted(s, modelA, k) {
			t.Errorf("%s muted", k)
		}
		if d := s.request(modelA, k, cfg); d != SteerSend {
			t.Errorf("%s: %q", k, d)
		}
	}
	if p := mustPair(t, s, modelA, SteerKindDone); p.Sent != 60 {
		t.Fatalf("the done-check's outcomes are still counted: %d", p.Sent)
	}
}

// twin: tests/adaptive.test.ts:82
func TestAdaptiveWhileMutedOneInFiveIsSentAndProbesDecide(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindOffTask, 30, 0, 0, cfg)
	decisions := make([]SteerDecision, 30)
	probes := 0
	for i := range decisions {
		decisions[i] = s.request(modelA, SteerKindOffTask, cfg)
		if decisions[i] == SteerProbe {
			probes++
		}
	}
	if probes != 6 {
		t.Fatalf("probes %d", probes)
	}
	if !reflect.DeepEqual(decisions[:5], []SteerDecision{SteerProbe, SteerTraceOnly, SteerTraceOnly, SteerTraceOnly, SteerTraceOnly}) {
		t.Fatalf("decisions %v", decisions[:5])
	}
	if !isMuted(s, modelA, SteerKindOffTask) {
		t.Fatal("no probe outcome was observed: still muted")
	}
	// Probes that are followed end the mute; the probe counts become the pair's counts.
	for i := range 30 {
		if s.request(modelA, SteerKindOffTask, cfg) == SteerProbe {
			s.observe(modelA, SteerKindOffTask, SteerOutcome{Followed: i < 10}, cfg)
		}
		if i == 28 && !isMuted(s, modelA, SteerKindOffTask) {
			t.Fatal("unmuted before the recheck")
		}
	}
	p := mustPair(t, s, modelA, SteerKindOffTask)
	if p.Muted || p.Sent != 6 || p.Followed != 2 {
		t.Fatalf("pair %+v", p)
	}
	if d := s.request(modelA, SteerKindOffTask, cfg); d != SteerSend {
		t.Fatalf("got %q", d)
	}
	// A fresh 30 steers must pass before it can be muted again.
	feedSteers(s, modelA, SteerKindOffTask, 23, 0, 0, cfg)
	if isMuted(s, modelA, SteerKindOffTask) {
		t.Fatal("muted before a fresh minSteers")
	}
	feedSteers(s, modelA, SteerKindOffTask, 1, 0, 0, cfg)
	if !isMuted(s, modelA, SteerKindOffTask) {
		t.Fatal("not muted again at 30")
	}
}

// twin: tests/adaptive.test.ts:106
func TestAdaptiveFailingProbesKeepPairMuted(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindSlop, 30, 0, 0, cfg)
	for round := range 3 {
		for range 30 {
			if s.request(modelA, SteerKindSlop, cfg) == SteerProbe {
				s.observe(modelA, SteerKindSlop, SteerOutcome{Disputed: true}, cfg)
			}
		}
		if !isMuted(s, modelA, SteerKindSlop) {
			t.Fatalf("round %d", round)
		}
	}
}

// twin: tests/adaptive.test.ts:115
func TestAdaptiveUnmuteResetsPair(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindRules, 30, 0, 0, cfg)
	if d := s.request(modelA, SteerKindRules, cfg); d != SteerProbe {
		t.Fatalf("got %q", d)
	}
	if !s.unmute(modelA, SteerKindRules) {
		t.Fatal("unmute")
	}
	if _, ok := s.pair(modelA, SteerKindRules); ok {
		t.Fatal("pair remains")
	}
	if d := s.request(modelA, SteerKindRules, cfg); d != SteerSend {
		t.Fatalf("got %q", d)
	}
	if s.unmute(modelA, SteerKindRules) {
		t.Fatal("nothing left to reset")
	}
}

// twin: tests/adaptive.test.ts:125
func TestAdaptivePairsAreIsolatedByModel(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindIntentMismatch, 30, 0, 0, cfg)
	feedSteers(s, modelB, SteerKindIntentMismatch, 30, 20, 0, cfg)
	if !isMuted(s, modelA, SteerKindIntentMismatch) || isMuted(s, modelB, SteerKindIntentMismatch) {
		t.Fatal("isolation")
	}
	if d := s.request(modelB, SteerKindIntentMismatch, cfg); d != SteerSend {
		t.Fatalf("got %q", d)
	}
	var muted []string
	for _, m := range s.muted() {
		muted = append(muted, m.Model)
	}
	if !reflect.DeepEqual(muted, []string{modelA}) {
		t.Fatalf("muted %v", muted)
	}
	with := s.modelsWith(SteerKindIntentMismatch)
	sort.Strings(with)
	if !reflect.DeepEqual(with, []string{modelA, modelB}) {
		t.Fatalf("modelsWith %v", with)
	}
	s.unmute(modelB, SteerKindIntentMismatch)
	if !isMuted(s, modelA, SteerKindIntentMismatch) {
		t.Fatal("resetting one model leaves the other")
	}
}

// twin: tests/adaptive.test.ts:138
func TestAdaptiveCountsPersistAcrossInstances(t *testing.T) {
	cfg := defaultSteersConfig()
	path := filepath.Join(t.TempDir(), "persist.json")
	first := NewSteerStats(path)
	feedSteers(first, modelA, SteerKindRules, 30, 0, 0, cfg)
	second := NewSteerStats(path)
	if !isMuted(second, modelA, SteerKindRules) {
		t.Fatal("not persisted")
	}
	if got := FormatMuted(second.muted(), cfg); !strings.Contains(got, "provider/model-a: rules (30 steers, 0% followed, 0% disputed)") {
		t.Fatalf("got %q", got)
	}
	off := cfg
	off.Adaptive = false
	if got := FormatMuted(nil, off); got != "Adaptive steers: off." {
		t.Fatalf("got %q", got)
	}
}

func view(text string, calls ...SteerCall) AssistantView {
	return AssistantView{Text: text, Calls: calls}
}
func call(name string, input map[string]any) SteerCall { return SteerCall{Name: name, Input: input} }

// twin: tests/adaptive.test.ts:148
func TestSteerOutcomeHeuristicsPerKind(t *testing.T) {
	type outcome struct{ followed, disputed bool }
	check := func(name string, kind SteerKind, subject SteerSubject, views []AssistantView, want outcome) {
		t.Helper()
		f, d := steerOutcome(kind, subject, views)
		if (outcome{f, d}) != want {
			t.Errorf("%s: got followed=%v disputed=%v, want %+v", name, f, d, want)
		}
	}
	npmTest := map[string]any{"command": "npm test"}
	check("done ran tests", SteerKindDone, SteerSubject{}, []AssistantView{view("Running the tests.", call("bash", npmTest))}, outcome{true, false})
	check("done said done", SteerKindDone, SteerSubject{}, []AssistantView{view("Done.")}, outcome{false, false})
	check("rules edit same", SteerKindRules, SteerSubject{Path: "src/a.ts"}, []AssistantView{view("", call("edit", map[string]any{"path": "/p/src/a.ts"}))}, outcome{true, false})
	check("rules edit other", SteerKindRules, SteerSubject{Path: "src/a.ts"}, []AssistantView{view("", call("edit", map[string]any{"path": "/p/src/b.ts"}))}, outcome{false, false})
	stuck := SteerSubject{Call: &SteerCall{Name: "bash", Input: npmTest}}
	check("stuck same", SteerKindStuck, stuck, []AssistantView{view("", call("bash", npmTest))}, outcome{false, false})
	check("stuck changed", SteerKindStuck, stuck, []AssistantView{view("", call("bash", map[string]any{"command": "npm run build"}))}, outcome{true, false})
	check("dispute", SteerKindIntentMismatch, SteerSubject{}, []AssistantView{view("As I said, this matches the plan.", call("bash", map[string]any{"command": "ls"}))}, outcome{false, true})
	check("asks", SteerKindIntentMismatch, SteerSubject{}, []AssistantView{view("Should I push now?")}, outcome{true, false})
	check("task and warden are no course change", SteerKindIntentMismatch, SteerSubject{}, []AssistantView{view("Back to the task: the warden note is about the push.", call("bash", map[string]any{"command": "git push"}))}, outcome{false, false})
	check("instead", SteerKindOffTask, SteerSubject{}, []AssistantView{view("I will run the linter instead.", call("bash", map[string]any{"command": "npm run lint"}))}, outcome{true, false})
	check("conscience", SteerKindConscience, SteerSubject{Capability: &SteerCapability{Kind: "tool", ID: "search_code"}}, []AssistantView{view("", call("search_code", map[string]any{}))}, outcome{true, false})
	check("loops", SteerKindLoops, SteerSubject{}, []AssistantView{view("", call("warden_loops", map[string]any{"action": "done", "id": float64(1)}))}, outcome{true, false})
}

// twin: tests/adaptive.test.ts:164
func TestSteerWatchSettlesAfterTwoMessagesEarlyOnFollowAndWaitsForNextTurn(t *testing.T) {
	w := &SteerWatch{}
	w.add(modelA, []SteerKind{SteerKindRules}, SteerSubject{Path: "src/a.ts"}, false)
	if got := w.assistant(assistantView([]any{map[string]any{"type": "text", "text": "Looking."}})); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	settled := w.assistant(assistantView([]any{map[string]any{"type": "toolCall", "id": "1", "name": "read", "arguments": map[string]any{"path": "x"}}}))
	if !reflect.DeepEqual(settled, []SteerOutcome{{Model: modelA, Kind: SteerKindRules}}) {
		t.Fatalf("settled %+v", settled)
	}
	w.add(modelA, []SteerKind{SteerKindRules}, SteerSubject{Path: "src/a.ts"}, false)
	got := w.assistant(assistantView([]any{map[string]any{"type": "toolCall", "id": "2", "name": "edit", "arguments": map[string]any{"path": "src/a.ts"}}}))
	if len(got) != 1 || !got[0].Followed {
		t.Fatalf("early follow: %+v", got)
	}
	w.add(modelA, []SteerKind{SteerKindIntentMismatch}, SteerSubject{}, false)
	if got := w.user(); len(got) != 0 {
		t.Fatalf("a steer with no reply before the user spoke is dropped: %+v", got)
	}
	w.add(modelB, []SteerKind{SteerKindProse}, SteerSubject{}, true)
	if got := w.assistant(assistantView("ignored: the note waits for the next prompt")); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	w.user()
	w.assistant(assistantView("Plain reply."))
	got = w.assistant(assistantView("Still plain."))
	if !reflect.DeepEqual(got, []SteerOutcome{{Model: modelB, Kind: SteerKindProse}}) {
		t.Fatalf("got %+v", got)
	}
}

func TestSteerStatsFileMatchesTheOriginalsFormat(t *testing.T) {
	cfg := defaultSteersConfig()
	path := filepath.Join(t.TempDir(), "steer-stats.json")
	s := NewSteerStats(path)
	feedSteers(s, "b/z", SteerKindRules, 2, 1, 0, cfg)
	feedSteers(s, "a/y", SteerKindStuck, 1, 0, 1, cfg)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// JSON.stringify of the original's data: insertion order of models and kinds, fixed key order, one newline.
	const want = `{"version":1,"models":{"b/z":{"rules":{"sent":2,"followed":1,"disputed":0,"muted":false,"asked":0,"probe":{"sent":0,"followed":0,"disputed":0}}},"a/y":{"stuck":{"sent":1,"followed":0,"disputed":1,"muted":false,"asked":0,"probe":{"sent":0,"followed":0,"disputed":0}}}}}` + "\n"
	if string(raw) != want {
		t.Fatalf("got\n%s\nwant\n%s", raw, want)
	}
	// And the file is read back in the same order.
	again := NewSteerStats(path)
	again.observe("b/z", SteerKindSlop, SteerOutcome{}, cfg)
	raw2, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw2), `{"version":1,"models":{"b/z":{"rules":`) || !strings.Contains(string(raw2), `"slop":`) {
		t.Fatalf("reread %s", raw2)
	}
}

func TestSteerWatchKeepsAtMostFiftyPending(t *testing.T) {
	w := &SteerWatch{}
	for i := range 51 {
		w.add("m", []SteerKind{SteerKindRules}, SteerSubject{Path: "p" + itoaInt(i)}, false)
	}
	w.mu.Lock()
	n, first := len(w.pending), w.pending[0].subject.Path
	w.mu.Unlock()
	if n != 50 || first != "p1" {
		t.Fatalf("%d pending, first %q: the oldest steer is dropped", n, first)
	}
	w.add("m", nil, SteerSubject{}, false)
	w.mu.Lock()
	n = len(w.pending)
	w.mu.Unlock()
	if n != 50 {
		t.Fatal("a steer with no kinds is not watched")
	}
}

func TestSteerWatchAUserMessageDoesNotSettleThroughAssistantAndDisputesSettleAtOnce(t *testing.T) {
	w := &SteerWatch{}
	w.add("m", []SteerKind{SteerKindRules}, SteerSubject{Path: "a.go"}, false)
	got := w.assistant(assistantView("As I said, this is expected."))
	if len(got) != 1 || !got[0].Disputed || got[0].Followed {
		t.Fatalf("a dispute settles the steer at once: %+v", got)
	}
}

func TestSteerStatsRecheckKeepsTheProbeCountsAndFreshStartsAfterwards(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindRules, 30, 0, 0, cfg)
	for i := range 30 {
		if s.request(modelA, SteerKindRules, cfg) == SteerProbe {
			s.observe(modelA, SteerKindRules, SteerOutcome{Followed: i < 11, Disputed: false}, cfg)
		}
	}
	p := mustPair(t, s, modelA, SteerKindRules)
	if p.Muted || p.Asked != 0 || p.Probe != (SteerCounts{}) || p.Sent != 6 || p.Followed != 3 {
		t.Fatalf("%+v", p)
	}
}

func TestSteerOutcomeConscienceSkillsAndEveryEdgeOfTheOtherKinds(t *testing.T) {
	f := func(kind SteerKind, subject SteerSubject, views ...AssistantView) bool {
		got, _ := steerOutcome(kind, subject, views)
		return got
	}
	skill := SteerSubject{Capability: &SteerCapability{Kind: "skill", ID: "impeccable"}}
	if !f(SteerKindConscience, skill, view("", call("read", map[string]any{"path": "/s/impeccable/SKILL.md"}))) || f(SteerKindConscience, skill, view("", call("read", map[string]any{"path": "/s/other/SKILL.md"}))) {
		t.Fatal("a skill is followed by reading its folder")
	}
	tool := SteerSubject{Capability: &SteerCapability{Kind: "tool", ID: "search_code"}}
	if !f(SteerKindConscience, tool, view("", call("mcp__x__search_code", map[string]any{}))) || f(SteerKindConscience, tool, view("", call("search", map[string]any{}))) || f(SteerKindConscience, SteerSubject{}, view("", call("search_code", map[string]any{}))) {
		t.Fatal("a tool is followed by calling it, or its suffix")
	}
	if !f(SteerKindDone, SteerSubject{}, view("Not verified yet.")) || !f(SteerKindDone, SteerSubject{}, view("", call("bash", map[string]any{"command": "go test ./..."}))) || f(SteerKindDone, SteerSubject{}, view("", call("read", map[string]any{"path": "go test"}))) {
		t.Fatal("done is followed by a check run in bash or an admission")
	}
	if !f(SteerKindRules, SteerSubject{Path: "a.go"}, view("", call("bash", map[string]any{"command": "sed -i s/x/y/ a.go"}))) || f(SteerKindRules, SteerSubject{}, view("", call("edit", map[string]any{"path": "a.go"}))) || f(SteerKindRules, SteerSubject{Path: "a.go"}, view("", call("read", map[string]any{"path": "a.go"}))) {
		t.Fatal("a rules note is followed by touching the named file with an edit or a bash command")
	}
	if f(SteerKindStuck, SteerSubject{}) || !f(SteerKindStuck, SteerSubject{}, view("", call("bash", map[string]any{"command": "ls"}))) {
		t.Fatal("a stuck note without a subject is followed by any call")
	}
	if !f(SteerKindIntentMismatch, SteerSubject{}, view("Is that right?  ")) || f(SteerKindIntentMismatch, SteerSubject{}, view("Is that right?", call("bash", map[string]any{"command": "ls"}))) || f(SteerKindIntentMismatch, SteerSubject{}, view("right"), view("Is that right?")) {
		t.Fatal("a question with no calls follows an intent note; only the first message counts")
	}
	if f(SteerKindProse, SteerSubject{}, view("", call("warden_loops", map[string]any{}))) || f(SteerKindLoops, SteerSubject{}, view("", call("bash", map[string]any{}))) {
		t.Fatal("only loops is followed by warden_loops")
	}
}

func TestSteerStatsKindsWithoutAFollowSignalAreJudgedByDisputesAlone(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindProse, 40, 0, 0, cfg)
	if isMuted(s, modelA, SteerKindProse) {
		t.Fatal("nobody can read whether a prose note was followed: no follow floor applies")
	}
	feedSteers(s, modelA, SteerKindSensitivePath, 40, 0, 25, cfg)
	if !isMuted(s, modelA, SteerKindSensitivePath) {
		t.Fatal("disputes still mute it")
	}
}

func TestSteerStatsANeverMutedKindIsSentEvenFromAnEditedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	seed := `{"version":1,"models":{"` + modelA + `":{"hold":{"sent":50,"followed":0,"disputed":50,"muted":true,"asked":1,"probe":{"sent":0,"followed":0,"disputed":0}}}}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSteerStats(path)
	if d := s.request(modelA, SteerKindHold, defaultSteersConfig()); d != SteerSend {
		t.Fatalf("holds are never trace-only: %q", d)
	}
}

func TestSteerStatsUnmuteOfAKindTheModelNeverHadResetsNothing(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	feedSteers(s, modelA, SteerKindRules, 3, 3, 0, cfg)
	if s.unmute(modelA, SteerKindSlop) {
		t.Fatal("the model has counts for rules, not slop")
	}
	if _, ok := s.pair(modelA, SteerKindRules); !ok {
		t.Fatal("the other pair stays")
	}
	if !s.unmute(modelA, SteerKindRules) {
		t.Fatal("unmute")
	}
	if names := s.modelsWith(SteerKindRules); len(names) != 0 {
		t.Fatalf("%v", names)
	}
}

func TestSteerStatsAFailedWriteIsWarnedAbout(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "x")
	if err := os.WriteFile(blocker, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSteerStats(filepath.Join(blocker, "stats.json"))
	var warned []string
	s.Warn = func(m string) { warned = append(warned, m) }
	feedSteers(s, modelA, SteerKindRules, 1, 0, 0, defaultSteersConfig())
	if len(warned) != 1 || !strings.HasPrefix(warned[0], "pi-warden: steer stats write failed: ") {
		t.Fatalf("%v", warned)
	}
	if p := mustPair(t, s, modelA, SteerKindRules); p.Sent != 1 {
		t.Fatal("the counts stay in memory")
	}
}

func TestSteerStatsMutedListsAreSortedByModelThenKind(t *testing.T) {
	cfg := defaultSteersConfig()
	s := newSteerStatsT(t)
	for _, c := range []struct {
		model string
		kind  SteerKind
	}{{"z/model", SteerKindSlop}, {"a/model", SteerKindRules}, {"a/model", SteerKindOffTask}} {
		feedSteers(s, c.model, c.kind, 30, 0, 0, cfg)
	}
	var got []string
	for _, m := range s.muted() {
		got = append(got, m.Model+" "+string(m.Kind))
	}
	if !reflect.DeepEqual(got, []string{"a/model off-task", "a/model rules", "z/model slop"}) {
		t.Fatalf("%v", got)
	}
}

func TestSteerOutcomeDisputePhrasesAreAllRecognised(t *testing.T) {
	for _, text := range []string{
		"that is a false positive", "a false alarm", "no credentials here", "no real secrets", "not a secret", "not real tokens", "not a credential",
		"only placeholder values", "just fixture data", "just dummy data", "only fake keys", "as I said", "as planned", "as stated", "as I planned",
		"matches what I said", "matches the plan", "this is expected", "warden is wrong", "warden misfired", "warden misread it", "a misfire", "spurious",
		"harmless", "benign", "nothing sensitive", "not sensitive", "it was intended", "intended",
	} {
		if _, disputed := steerOutcome(SteerKindRules, SteerSubject{}, []AssistantView{view(text)}); !disputed {
			t.Errorf("%q is not read as a dispute", text)
		}
	}
	if _, disputed := steerOutcome(SteerKindRules, SteerSubject{}, []AssistantView{view("Running the linter now.")}); disputed {
		t.Fatal("a plain reply is no dispute")
	}
}
