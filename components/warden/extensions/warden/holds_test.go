package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func holdVerdict(mod func(*Verdict)) Verdict {
	v := Verdict{
		Level:    LevelConfirm,
		Source:   "typesafe",
		Summary:  ActionSummary{Tool: "bash"},
		Patterns: []PatternHit{{ID: "git-force-push", Severity: SeverityDestructive, Label: "git force push"}},
		Reasons:  []string{"destructive: git force push", "irreversible 0.91"},
		Judgment: &Judgment{Irreversible: 0.91, OffTask: 0.2, Scope: "expected_step", ScopeConfidence: 0.8, Mutates: fptr(0.95), Model: "jev-test", ElapsedMs: 5},
	}
	if mod != nil {
		mod(&v)
	}
	return v
}

func withCommand(c string) func(*Verdict) { return func(v *Verdict) { v.Summary.Command = c } }

func ids(records []*CallRecord) []int {
	out := []int{}
	for _, r := range records {
		out = append(out, r.ID)
	}
	return out
}

// twin: tests/holds.test.ts:35
func TestHoldReleasedByReplyIsFalsePositive(t *testing.T) {
	l := NewHoldLedger()
	write := l.Record(holdVerdict(func(v *Verdict) { v.Summary = ActionSummary{Tool: "write", Path: "a.ts"} }), HoldOptions{Held: true, Mode: "steer", At: 1})
	bash := l.Record(holdVerdict(withCommand("git push --force")), HoldOptions{Held: true, Mode: "steer", At: 2})
	if got := l.Approved("bash", ViaRetry, 3); got == nil || got.ID != bash.ID {
		t.Fatalf("approved %v, want bash %d", got, bash.ID)
	}
	if bash.Outcome != OutcomeApproved || bash.OutcomeVia != ViaRetry || bash.OutcomeAt == nil || *bash.OutcomeAt != 3 {
		t.Fatalf("bash %+v", bash)
	}
	if write.Outcome != OutcomePending {
		t.Fatalf("write %+v", write)
	}
	if got := l.Approved("edit", ViaRetry, 4); got == nil || got.ID != write.ID {
		t.Fatalf("no hold of that tool: the latest pending hold is released, got %v", got)
	}
	if got := l.Approved("bash", "", 0); got != nil {
		t.Fatalf("nothing left to release, got %v", got)
	}
	s := l.Snapshot()
	if s.Holds != 2 || s.Approved != 2 || s.Precision == nil || *s.Precision != 0 {
		t.Fatalf("snapshot %+v", s)
	}
	if note := OutcomeNote(*bash); !strings.Contains(note, "released on retry") || !strings.Contains(note, "false positive") {
		t.Fatalf("note %q", note)
	}
}

// twin: tests/holds.test.ts:53
func TestHoldNobodyApprovesIsReplan(t *testing.T) {
	l := NewHoldLedger()
	hold := l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "steer", At: 1})
	if got := l.PromptArrived(2); len(got) != 0 {
		t.Fatalf("the user has only just replied; the retry can still be approved: %v", ids(got))
	}
	if hold.Outcome != OutcomePending || l.Snapshot().Awaiting != 1 {
		t.Fatalf("hold %+v", hold)
	}
	changed := l.PromptArrived(3)
	if !reflect.DeepEqual(ids(changed), []int{hold.ID}) {
		t.Fatalf("changed %v", ids(changed))
	}
	if hold.Outcome != OutcomeReplanned || hold.OutcomeVia != ViaNextPrompt {
		t.Fatalf("hold %+v", hold)
	}
	if got := l.PromptArrived(4); len(got) != 0 {
		t.Fatalf("labels land once")
	}
	s := l.Snapshot()
	if s.Replanned != 1 || s.Awaiting != 0 || s.Precision == nil || *s.Precision != 1 || s.Labels != 1 {
		t.Fatalf("snapshot %+v", s)
	}
}

// twin: tests/holds.test.ts:71
func TestHoldDialogDecisionsLabelAtOnce(t *testing.T) {
	l := NewHoldLedger()
	allowed := l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "confirm", Outcome: OutcomeApproved, Via: ViaDialog, At: 1})
	refused := l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "confirm", Outcome: OutcomeDeclined, Via: ViaDialog, At: 2})
	if allowed.OutcomeAt == nil || *allowed.OutcomeAt != 1 || refused.OutcomeVia != ViaDialog {
		t.Fatalf("%+v %+v", allowed, refused)
	}
	if p := l.Snapshot().Precision; p == nil || *p != 0.5 {
		t.Fatalf("precision %v", p)
	}
	if n := OutcomeNote(*allowed); !strings.Contains(n, "confirm dialog") || !strings.Contains(n, "false positive") {
		t.Fatalf("note %q", n)
	}
	if n := OutcomeNote(*refused); !strings.Contains(n, "declined") || !strings.Contains(n, "hold stood") {
		t.Fatalf("note %q", n)
	}
}

// twin: tests/holds.test.ts:82
func TestHoldAllowedCallsAreRegretCandidates(t *testing.T) {
	l := NewHoldLedger()
	l.Record(holdVerdict(func(v *Verdict) { v.Level = LevelAllow; v.Summary.Command = "npm test" }), HoldOptions{Mode: "steer"})
	push := l.Record(holdVerdict(func(v *Verdict) {
		v.Level = LevelWarn
		v.Summary.Command = "git push origin main " + strings.Repeat("x", 400)
	}), HoldOptions{Mode: "steer"})
	l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "steer"})
	if len(l.Candidates()) != 0 {
		t.Fatal("the user has not replied yet")
	}
	l.PromptArrived(0)
	candidates := l.Candidates()
	if len(candidates) != 2 || candidates[0].ID != "a1" || candidates[1].ID != "a2" {
		t.Fatalf("holds are not candidates: %+v", candidates)
	}
	if candidates[1].Tool != "bash" || utf16Len(candidates[1].Command) != 301 {
		t.Fatalf("commands are clipped for the request: %d", utf16Len(candidates[1].Command))
	}
	p := 0.88
	changed := l.Regret(RegretResult{Regretted: true, Target: "a2", Probability: &p, Via: ViaJev}, 0)
	got := [][2]any{}
	for _, r := range changed {
		got = append(got, [2]any{r.ID, r.Outcome})
	}
	if !reflect.DeepEqual(got, [][2]any{{1, OutcomeAccepted}, {2, OutcomeRegretted}}) {
		t.Fatalf("changed %v", got)
	}
	if push.Regret == nil || *push.Regret != 0.88 || push.OutcomeVia != ViaJev {
		t.Fatalf("push %+v", push)
	}
	if len(l.Candidates()) != 0 {
		t.Fatal("labelled calls leave the candidate set")
	}
	if got := l.Regret(RegretResult{Regretted: true, Via: ViaText}, 0); len(got) != 0 {
		t.Fatal("nothing left to label")
	}
	s := l.Snapshot()
	if s.Allowed != 2 || s.Regretted != 1 || s.Accepted != 1 {
		t.Fatalf("snapshot %+v", s)
	}
	if n := OutcomeNote(*push); !strings.Contains(n, "regrets this call (0.88); it should have been held") {
		t.Fatalf("note %q", n)
	}
}

// twin: tests/holds.test.ts:106
func TestHoldRegretWithoutTargetFallsOnLatest(t *testing.T) {
	l := NewHoldLedger()
	allow := func(v *Verdict) { v.Level = LevelAllow }
	l.Record(holdVerdict(allow), HoldOptions{Mode: "steer"})
	last := l.Record(holdVerdict(allow), HoldOptions{Mode: "steer"})
	l.PromptArrived(0)
	changed := l.Regret(RegretResult{Regretted: true, Target: "a9", Via: ViaText}, 0)
	if len(changed) != 2 || changed[0].Outcome != OutcomeAccepted || changed[1].Outcome != OutcomeRegretted {
		t.Fatalf("changed %+v", changed)
	}
	if last.Outcome != OutcomeRegretted {
		t.Fatalf("last %+v", last)
	}
	stale := NewHoldLedger()
	rec := stale.Record(holdVerdict(allow), HoldOptions{Mode: "steer"})
	stale.PromptArrived(0)
	stale.PromptArrived(0)
	if len(stale.Candidates()) != 0 {
		t.Fatal("candidates two prompts old are no longer asked about")
	}
	if rec.Outcome != OutcomePending {
		t.Fatalf("an allowed call that was never checked stays pending, not accepted: %v", rec.Outcome)
	}
}

// twin: tests/holds.test.ts:122
func TestTextRegretsReadsOpeningOfReply(t *testing.T) {
	for _, text := range []string{"wait, don't push that", "Stop!", "no, that was wrong", "undo that", "Revert the last commit you made", "why did you delete the branch?", "hold on", "Please don't run that again", "  roll back what you did"} {
		if !TextRegrets(text) {
			t.Errorf("%q should regret", text)
		}
	}
	for _, text := range []string{"yes, go ahead", "wait for CI to finish, then push", "now add the tests", "looks good", "the build passed; next, update the docs", "", "wait until it is done", "Wait till then"} {
		if TextRegrets(text) {
			t.Errorf("%q should not regret", text)
		}
	}
	if !RegretsAt(0.7) || RegretsAt(0.69) {
		t.Fatal("threshold")
	}
}

// twin: tests/holds.test.ts:133
func TestFormatHoldsReportsCountsAndPrecision(t *testing.T) {
	l := NewHoldLedger()
	if got := FormatHolds(l.Snapshot(), ""); got != "Holds: no guarded call judged yet this session." {
		t.Fatalf("got %q", got)
	}
	l.Record(holdVerdict(func(v *Verdict) { v.Level = LevelAllow }), HoldOptions{Mode: "steer"})
	if got := FormatHolds(l.Snapshot(), ""); got != "Holds: 0 holds; precision not yet measurable; 1 allowed (0 regretted by you, 0 accepted)." {
		t.Fatalf("got %q", got)
	}
	l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "steer"})
	l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "confirm", Outcome: OutcomeDeclined, Via: ViaDialog})
	l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "confirm", Outcome: OutcomeApproved, Via: ViaDialog})
	line := FormatHolds(l.Snapshot(), "/tmp/holds.jsonl")
	if !strings.Contains(line, "3 holds; 1 approved by you, 1 declined, 0 re-planned, 1 awaiting your reply; precision 50% over 2 labels; 1 allowed") || !strings.HasSuffix(line, "Log: /tmp/holds.jsonl.") {
		t.Fatalf("got %q", line)
	}
}

// twin: tests/holds.test.ts:146
func TestHoldLogIsOneJSONLinePerCall(t *testing.T) {
	dir := t.TempDir()
	path := HoldLogPath(dir, "sess/../id 42", time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC), 1)
	if want := filepath.Join(dir, "holds", "2026-09-19-sessid42.jsonl"); path != want {
		t.Fatalf("path %q, want %q", path, want)
	}
	log := NewHoldLog(path)
	l := NewHoldLedger()
	l.Record(holdVerdict(withCommand("git push --force origin main")), HoldOptions{Held: true, Mode: "steer", At: 1})
	log.Save(l.Records())
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Approved("bash", ViaRetry, 2)
	l.Record(holdVerdict(func(v *Verdict) { v.Level = LevelAllow; v.Summary.Command = "rm -rf secret-dir" }), HoldOptions{Mode: "steer", At: 3})
	log.Save(l.Records())
	second, _ := os.Stat(path)
	if os.SameFile(first, second) {
		t.Fatal("a save replaces the file, so a reader never gets a line cut short")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("no temporary file is left beside the log: %v", entries)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the file holds the current state of every record, not an event per change: %d", len(lines))
	}
	var gotLine0 map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &gotLine0)
	var want map[string]any
	_ = json.Unmarshal([]byte(`{"id":1,"at":1,"tool":"bash","level":"confirm","source":"typesafe","mode":"steer","held":true,"patterns":["git-force-push"],"reasons":["destructive: git force push","irreversible 0.91"],"planChars":0,"scores":{"irreversible":0.91,"offTask":0.2,"scope":"expected_step","mutates":0.95},"outcome":"approved","outcomeAt":2,"outcomeVia":"retry","callExcerpt":"bash"}`), &want)
	if !reflect.DeepEqual(gotLine0, want) {
		t.Fatalf("line 0 %s", lines[0])
	}
	// The original's key order: what JSON.stringify writes for the record it builds.
	const order = `{"id":1,"at":1,"tool":"bash","level":"confirm","source":"typesafe","mode":"steer","held":true,"patterns":["git-force-push"],"reasons":["destructive: git force push","irreversible 0.91"],"planChars":0,"outcome":"approved","callExcerpt":"bash","scores":{"irreversible":0.91,"offTask":0.2,"scope":"expected_step","mutates":0.95},"outcomeAt":2,"outcomeVia":"retry"}`
	if lines[0] != order {
		t.Fatalf("key order\n got %s\nwant %s", lines[0], order)
	}
	var second1 map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &second1)
	if second1["outcome"] != "pending" {
		t.Fatalf("line 1 %s", lines[1])
	}
	if strings.Contains(text, "origin main") || strings.Contains(text, "secret-dir") {
		t.Fatal("commands never reach the log")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 && os.PathSeparator != '\\' {
		t.Fatalf("mode %v", info.Mode())
	}
	if log.LastFailure() != "" {
		t.Fatalf("failure %q", log.LastFailure())
	}
	broken := NewHoldLog(filepath.Join(path, "nested.jsonl"))
	broken.Save(l.Records())
	if broken.LastFailure() == "" {
		t.Fatal("a write into a file path fails and is remembered instead of thrown")
	}
}

// twin: tests/holds.test.ts:173
func TestRulesVerdictsLandInRecordsNotCandidates(t *testing.T) {
	l := NewHoldLedger()
	l.RecordRules(RulesRecord{Source: "typesafe", Findings: []RulesFinding{{Name: "No hardcoded secrets", Violation: 0.88}}})
	l.RecordRules(RulesRecord{Source: "typesafe"})
	records := l.Records()
	records = records[len(records)-2:]
	if records[0].Tool != "rules" || records[0].Level != LevelWarn || records[0].Held || !reflect.DeepEqual(records[0].Reasons, []string{"No hardcoded secrets 0.88"}) {
		t.Fatalf("%+v", records[0])
	}
	if records[1].Level != LevelAllow {
		t.Fatalf("%+v", records[1])
	}
	if len(l.Candidates()) != 0 {
		t.Fatal("they never join the regret labelling")
	}
	path := filepath.Join(t.TempDir(), "rules-log.jsonl")
	NewHoldLog(path).Save(l.Records())
	raw, _ := os.ReadFile(path)
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var rec struct {
			Tool    string
			Reasons []string
		}
		_ = json.Unmarshal([]byte(line), &rec)
		if rec.Tool == "rules" && len(rec.Reasons) == 1 && rec.Reasons[0] == "No hardcoded secrets 0.88" {
			found = true
		}
	}
	if !found {
		t.Fatal("the log carries the rules record")
	}
}

// twin: tests/holds.test.ts:193
func TestHoldResetClearsEveryRecord(t *testing.T) {
	l := NewHoldLedger()
	l.Record(holdVerdict(withCommand("git push --force origin main")), HoldOptions{Held: true, Mode: "steer"})
	l.RecordRules(RulesRecord{Source: "typesafe", Findings: []RulesFinding{{Name: "No hardcoded secrets", Violation: 0.88}}})
	if s := l.Snapshot(); s.Allowed != 1 || s.Holds != 1 {
		t.Fatalf("%+v", s)
	}
	l.Reset()
	if len(l.Records()) != 0 {
		t.Fatal("records from the previous session are gone, not just the tracked ones")
	}
	if !reflect.DeepEqual(l.Snapshot(), HoldSnapshot{}) {
		t.Fatalf("%+v", l.Snapshot())
	}
	if id := l.RecordRules(RulesRecord{Source: "typesafe"}).ID; id != 1 {
		t.Fatalf("the id counter restarts, got %d", id)
	}
}

func TestHoldApprovedPrefersTheSameToolEvenWhenAnotherHoldIsNewer(t *testing.T) {
	l := NewHoldLedger()
	bash := l.Record(holdVerdict(withCommand("git push --force")), HoldOptions{Held: true, Mode: "steer", At: 1})
	write := l.Record(holdVerdict(func(v *Verdict) { v.Summary = ActionSummary{Tool: "write", Path: "a.ts"} }), HoldOptions{Held: true, Mode: "steer", At: 2})
	if got := l.Approved("bash", ViaRetry, 3); got == nil || got.ID != bash.ID {
		t.Fatalf("the older bash hold is the one released: %v", got)
	}
	if write.Outcome != OutcomePending {
		t.Fatalf("%+v", write)
	}
}

func TestHoldApprovedByAnotherToolReleasesTheLatestPendingHold(t *testing.T) {
	l := NewHoldLedger()
	first := l.Record(holdVerdict(withCommand("a")), HoldOptions{Held: true, Mode: "steer", At: 1})
	second := l.Record(holdVerdict(func(v *Verdict) { v.Summary = ActionSummary{Tool: "write", Path: "w"} }), HoldOptions{Held: true, Mode: "steer", At: 2})
	if got := l.Approved("edit", ViaRetry, 3); got == nil || got.ID != second.ID || first.Outcome != OutcomePending {
		t.Fatalf("the latest pending hold: %v", got)
	}
}

func TestHoldRegretLabelsTheLocatedCandidateNotJustTheLatest(t *testing.T) {
	l := NewHoldLedger()
	allow := func(v *Verdict) { v.Level = LevelAllow }
	first := l.Record(holdVerdict(allow), HoldOptions{Mode: "steer"})
	last := l.Record(holdVerdict(allow), HoldOptions{Mode: "steer"})
	l.PromptArrived(0)
	l.Regret(RegretResult{Regretted: true, Target: "a1", Via: ViaJev}, 0)
	if first.Outcome != OutcomeRegretted || last.Outcome != OutcomeAccepted {
		t.Fatalf("first %v, last %v", first.Outcome, last.Outcome)
	}
}

func TestHoldLogExcerptNamesToolAndPathNeverTheCommand(t *testing.T) {
	l := NewHoldLedger()
	rec := l.Record(holdVerdict(func(v *Verdict) {
		v.Summary = ActionSummary{Tool: "bash", Command: "cat secret.txt", Path: "secret.txt"}
	}), HoldOptions{Held: true, Mode: "steer"})
	if rec.CallExcerpt != "bash secret.txt" {
		t.Fatalf("%q", rec.CallExcerpt)
	}
	none := l.Record(holdVerdict(func(v *Verdict) { v.Summary = ActionSummary{} }), HoldOptions{Held: true, Mode: "steer"})
	if none.CallExcerpt != "" {
		t.Fatalf("%q", none.CallExcerpt)
	}
}

func TestHoldLogForgetsAFailureOnceASaveWorks(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "x")
	if err := os.WriteFile(blocker, []byte("a file where the log's directory should be"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewHoldLog(filepath.Join(blocker, "log.jsonl"))
	l := NewHoldLedger()
	l.Record(holdVerdict(nil), HoldOptions{Held: true, Mode: "steer"})
	log.Save(l.Records())
	if log.LastFailure() == "" {
		t.Fatal("the failure is remembered")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	log.Save(l.Records())
	if log.LastFailure() != "" {
		t.Fatalf("a later save that works clears it: %q", log.LastFailure())
	}
}

func TestSessionFileIDIsBoundedAndFallsBackToThePid(t *testing.T) {
	if got := SessionFileID(strings.Repeat("a", 50)+"/../x", 7); got != strings.Repeat("a", 40) {
		t.Fatalf("%q", got)
	}
	if got := SessionFileID("../..//", 4242); got != "4242" {
		t.Fatalf("%q", got)
	}
	if got := SessionFileID("ab_C-9.é", 1); got != "ab_C-9" {
		t.Fatalf("%q", got)
	}
}

func TestOutcomeNoteForADenyRule(t *testing.T) {
	deny := CallRecord{Outcome: OutcomeDeclined, OutcomeVia: ViaDeny}
	if got := OutcomeNote(deny); got != "outcome: blocked by a deny rule; the call never ran" {
		t.Fatalf("%q", got)
	}
	if got := OutcomeNote(CallRecord{Outcome: OutcomePending}); got != "outcome: pending" {
		t.Fatalf("%q", got)
	}
	if got := OutcomeNote(CallRecord{Outcome: OutcomeReplanned}); !strings.Contains(got, "re-planned, the hold stood") {
		t.Fatalf("%q", got)
	}
	p := 0.5
	if got := OutcomeNote(CallRecord{Outcome: OutcomeAccepted, Regret: &p}); got != "outcome: the user's next message does not regret this call (0.50)" {
		t.Fatalf("%q", got)
	}
}

func TestHoldPlanCharsCountUTF16Units(t *testing.T) {
	l := NewHoldLedger()
	rec := l.Record(holdVerdict(func(v *Verdict) { v.Plan = "éé😀" }), HoldOptions{Held: true, Mode: "steer"})
	if rec.PlanChars != 4 {
		t.Fatalf("two letters and an astral character are 4 UTF-16 units, got %d", rec.PlanChars)
	}
}
