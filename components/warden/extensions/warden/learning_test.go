package warden

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLearning(t *testing.T) *Learning {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "pi-warden", "holds.db")
	t.Cleanup(CloseLearningDatabases)
	return NewLearning(path, nil)
}

func baseHold(project string, mod func(*HoldRecord)) HoldRecord {
	h := HoldRecord{
		Timestamp: time.Now().UnixMilli(), ProjectRoot: project, Tool: "bash", CommandPreview: "npm test",
		Scores: HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, Level: "allow", Held: true, Reasons: []string{"irreversible 0.5"},
	}
	if mod != nil {
		mod(&h)
	}
	return h
}

func mustHold(t *testing.T, l *Learning, h HoldRecord) int64 {
	t.Helper()
	id, err := l.RecordHold(h)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func history(t *testing.T, l *Learning, tool string, s HoldScores, project string) SmartHistory {
	t.Helper()
	h, err := l.QuerySmartHistory(tool, s, project)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// twin: tests/learning.test.ts:19
func TestLearningInitSchemaCreatesDatabase(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	if _, err := os.Stat(l.path); err != nil {
		t.Fatalf("database file exists (the folder was created too): %v", err)
	}
	d := l.open()
	rows, err := d.all("PRAGMA table_info(holds)")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rows {
		names = append(names, r["name"].(string))
	}
	want := []string{"id", "timestamp", "project_root", "tool", "signature_hash", "command_preview", "task", "plan", "context_summary", "preceding_actions", "scores", "level", "held", "reasons", "agent_reason", "outcome", "outcome_at", "confidence"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("columns %v", names)
	}
}

// twin: tests/learning.test.ts:24
func TestLearningSignatureHashIsConsistent(t *testing.T) {
	s := HoldScores{Irreversible: 0.5, Reasons: []string{"test"}}
	h1, h2 := SignatureHash("bash", s), SignatureHash("bash", s)
	if h1 != h2 || len(h1) != 16 {
		t.Fatalf("%q %q", h1, h2)
	}
	lo := 0.3
	if SignatureHash("bash", HoldScores{Irreversible: 0.5, Reasons: []string{"test"}, LargeOutput: &lo}) != h1 {
		t.Fatal("the large_output score does not split the signature")
	}
	// sha256 of the text JSON.stringify gives the original: bash:{"irreversible":0.5,"reasons":["test"]}.
	if h1 != "1a72a57c19f3314e" {
		t.Fatalf("hash %s", h1)
	}
}

// twin: tests/learning.test.ts:32
func TestLearningRecordHoldReturnsID(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	id := mustHold(t, l, baseHold("/test/project", func(h *HoldRecord) {
		h.Task, h.Plan, h.AgentReason = "run tests", "execute test suite", "Running tests as requested"
	}))
	if id <= 0 {
		t.Fatalf("id %d", id)
	}
}

// twin: tests/learning.test.ts:49
func TestLearningRecordOutcomeUpdates(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	id := mustHold(t, l, baseHold("/test/project", func(h *HoldRecord) { h.Task = "run tests" }))
	l.RecordOutcome(id, "approved")
	rows, _ := l.QueryHoldsForProject("/test/project", nil)
	if len(rows) != 1 || rows[0]["outcome"] != "approved" {
		t.Fatalf("rows %v", rows)
	}
}

// twin: tests/learning.test.ts:64
func TestLearningSmartHistoryFindsExactInSameProject(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	mustHold(t, l, baseHold("/test/project", func(h *HoldRecord) { h.Task = "run tests" }))
	h := history(t, l, "bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/test/project")
	if len(h.Exact) == 0 {
		t.Fatal("finds exact matches")
	}
}

// twin: tests/learning.test.ts:81
func TestLearningSmartHistoryIgnoresOtherProjects(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	mustHold(t, l, baseHold("/test/project", nil))
	h := history(t, l, "bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/other/project")
	if len(h.Exact) != 0 {
		t.Fatal("no matches in a different project")
	}
}

// twin: tests/learning.test.ts:86
func TestLearningConfidenceZeroForEmptyHistory(t *testing.T) {
	c := CalculateSmartConfidence(SmartHistory{SignatureHash: "abc"})
	if c.Confidence != 0 || c.Reason != "no history" {
		t.Fatalf("%+v", c)
	}
}

// twin: tests/learning.test.ts:92
func TestLearningConfidenceHighForApprovedHistory(t *testing.T) {
	now := time.Now().UnixMilli()
	c := CalculateSmartConfidence(SmartHistory{Exact: []Row{
		{"outcome": "approved", "timestamp": now - 1000},
		{"outcome": "approved", "timestamp": now - 2000},
		{"outcome": "approved", "timestamp": now - 3000},
	}, SignatureHash: "abc"})
	if c.Confidence <= 0.7 {
		t.Fatalf("%+v", c)
	}
}

// twin: tests/learning.test.ts:107
func TestLearningShouldSkipNeverSkipsDestructive(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	r, err := l.ShouldSkipHold("bash", HoldScores{Irreversible: 0.9, Reasons: []string{"destructive: git reset"}}, "/test/project")
	if err != nil || r.Skip || !strings.Contains(r.Reason, "destructive") {
		t.Fatalf("%+v %v", r, err)
	}
}

// twin: tests/learning.test.ts:113
func TestLearningShouldSkipWhenConfidentWithEnoughApprovals(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	now := time.Now().UnixMilli()
	for i := range 3 {
		id := mustHold(t, l, baseHold("/skip/project", func(h *HoldRecord) { h.Timestamp = now - int64(i)*1000 }))
		l.RecordOutcome(id, "approved")
	}
	r, err := l.ShouldSkipHold("bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/skip/project")
	if err != nil || !r.Skip || r.Confidence <= 0.8 {
		t.Fatalf("%+v %v", r, err)
	}
}

// twin: tests/learning.test.ts:133
func TestLearningSmartHistoryFindsSimilarByIrreversibleProximity(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	mustHold(t, l, baseHold("/sim/project", func(h *HoldRecord) {
		h.CommandPreview = "rm -rf /tmp/test"
		h.Scores = HoldScores{Irreversible: 0.6, Reasons: []string{"irreversible 0.6"}}
		h.Reasons = []string{"irreversible 0.6"}
	}))
	h := history(t, l, "bash", HoldScores{Irreversible: 0.7, Reasons: []string{"irreversible 0.7"}}, "/sim/project")
	if len(h.Similar) == 0 {
		t.Fatal("finds similar matches within the irreversible threshold")
	}
}

// twin: tests/learning.test.ts:152
func TestLearningThresholdsEmptyForInsufficientData(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	a, err := l.AnalyzeThresholds("project-alpha")
	if err != nil || len(a) != 0 {
		t.Fatalf("%v %v", a, err)
	}
}

// twin: tests/learning.test.ts:157
func TestLearningThresholdsLowerWhenPrecisionLow(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	for i := range 25 {
		id := mustHold(t, l, baseHold("project-beta", func(h *HoldRecord) {
			h.Timestamp = time.Now().UnixMilli() - int64(i)*1000
			h.CommandPreview, h.Level = "test-command", "confirm"
		}))
		l.RecordOutcome(id, "approved")
	}
	a, err := l.AnalyzeThresholds("project-beta")
	if err != nil || len(a) == 0 {
		t.Fatalf("%v %v", a, err)
	}
	if a[0].Guard != "action" || a[0].SuggestedThreshold >= a[0].CurrentThreshold {
		t.Fatalf("%+v", a[0])
	}
}

// twin: tests/learning.test.ts:181
func TestLearningPatternsEmptyForInsufficientData(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	p, err := l.AnalyzePatterns("project-gamma")
	if err != nil || len(p) != 0 {
		t.Fatalf("%v %v", p, err)
	}
}

// twin: tests/learning.test.ts:186
func TestLearningRecommendationsCombineInsights(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	for i := range 10 {
		mustHold(t, l, baseHold("project-delta", func(h *HoldRecord) {
			h.Timestamp = time.Now().UnixMilli() - int64(i)*1000
			h.CommandPreview, h.Level = "test-command", "confirm"
		}))
	}
	r, err := l.GenerateRecommendations("project-delta")
	if err != nil || r == nil {
		t.Fatalf("%v %v", r, err)
	}
}

// twin: tests/learning.test.ts:209
func TestLearningSteerReportEmptyForInsufficientData(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	r, err := l.AnalyzeSteerEffectivenessReport("project-nu")
	if err != nil || r.Overall != 0 || len(r.ByType) != 0 || len(r.Suggestions) != 0 || len(r.TopPatterns) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

// twin: tests/learning.test.ts:217
func TestLearningSteerReportTracksEffectivenessByType(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	for i := range 10 {
		id := mustHold(t, l, baseHold("project-xi", func(h *HoldRecord) {
			h.Timestamp = time.Now().UnixMilli() - int64(i)*1000
			h.CommandPreview, h.Level, h.AgentReason = "test-command", "confirm", "irreversible action detected"
		}))
		l.RecordOutcome(id, "approved")
	}
	r, err := l.AnalyzeSteerEffectivenessReport("project-xi")
	if err != nil || r.Overall <= 0 || len(r.ByType) == 0 || len(r.TopPatterns) == 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

// twin: tests/learning.test.ts:242
func TestToHoldRecordRedactsPasswordURLs(t *testing.T) {
	preview := "git push https://user:secret123@github.com/repo.git main"
	r := ToHoldRecord(HoldItem{At: time.Now().UnixMilli(), Tool: "bash", Level: "confirm", Reasons: []string{"test"}}, "/test/project", HoldContext{Preview: &preview})
	if strings.Contains(r.CommandPreview, "secret123") || !strings.Contains(r.CommandPreview, "[redacted]") {
		t.Fatalf("%q", r.CommandPreview)
	}
}

// twin: tests/learning.test.ts:252
func TestToHoldRecordCapsPreviewAt200(t *testing.T) {
	preview := "echo " + strings.Repeat("x", 300)
	r := ToHoldRecord(HoldItem{At: time.Now().UnixMilli(), Tool: "bash", Level: "confirm", Reasons: []string{"test"}}, "/test/project", HoldContext{Preview: &preview})
	if utf16Len(r.CommandPreview) > 200 {
		t.Fatalf("%d", utf16Len(r.CommandPreview))
	}
}

// twin: tests/learning.test.ts:262
func TestToHoldRecordFallsBackToToolName(t *testing.T) {
	r := ToHoldRecord(HoldItem{At: time.Now().UnixMilli(), Tool: "bash", Level: "confirm", Reasons: []string{"test"}}, "/test/project", HoldContext{})
	if r.CommandPreview != "bash" {
		t.Fatalf("%q", r.CommandPreview)
	}
}

// twin: tests/learning.test.ts:270
func TestLearningRecordOutcomePersistsReplanned(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	id := mustHold(t, l, baseHold("/replanned/project", nil))
	l.RecordOutcome(id, "replanned")
	h := history(t, l, "bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/replanned/project")
	found := false
	for _, r := range h.Exact {
		if r["outcome"] == "replanned" {
			found = true
		}
	}
	if !found {
		t.Fatal("replanned outcome is queryable via querySmartHistory")
	}
}

// twin: tests/learning.test.ts:291 (the original chains recordOutcome on the recordHold promise; the port's chain is
// the ordered store worker, learning_wire.go, and its race is proved in learning_wire_test.go; here the same
// sequence runs on the store)
func TestLearningOutcomeAfterRecordHoldIsPersisted(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	id := mustHold(t, l, baseHold("/race/project", nil))
	l.RecordOutcome(id, "approved")
	h := history(t, l, "bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/race/project")
	found := false
	for _, r := range h.Exact {
		if r["outcome"] == "approved" {
			found = true
		}
	}
	if !found {
		t.Fatal("outcome persisted")
	}
}

// twin: tests/learning.test.ts:313
func TestLearningJudgedAllowedCallProducesHeldZeroRow(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	preview := "npm test"
	no := false
	rec := ToHoldRecord(HoldItem{At: time.Now().UnixMilli(), Tool: "bash", Level: "allow", Reasons: []string{"irreversible 0.3"}, Scores: &CallScores{Irreversible: 0.3, Scope: "expected_step"}, Held: &no}, "/allowed/project", HoldContext{Preview: &preview})
	if rec.Held {
		t.Fatal("held is false for judged allowed calls")
	}
	mustHold(t, l, rec)
	rows, err := l.QueryHoldsForProject("/allowed/project", &no)
	if err != nil || len(rows) == 0 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0]["held"] != int64(0) || rows[0]["command_preview"] != "npm test" {
		t.Fatalf("%v", rows[0])
	}
}

// twin: tests/learning.test.ts:329
func TestLearningLargeOutputScoreLandsInScoresColumn(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	preview := "npm test"
	yes := true
	large := 0.3
	scored := ToHoldRecord(HoldItem{At: time.Now().UnixMilli(), Tool: "bash", Level: "confirm", Reasons: []string{}, Scores: &CallScores{Irreversible: 0.1, Scope: "expected_step", LargeOutput: &large}, Held: &yes}, "/large-output/project", HoldContext{Preview: &preview})
	if !reflect.DeepEqual(scored.Scores, HoldScores{Irreversible: 0.1, Reasons: []string{}, LargeOutput: &large}) {
		t.Fatalf("%+v", scored.Scores)
	}
	mustHold(t, l, scored)
	// The lookup carries no large_output score, so an exact match also proves the signature ignores it.
	h := history(t, l, "bash", HoldScores{Irreversible: 0.1, Reasons: []string{}}, "/large-output/project")
	if len(h.Exact) != 1 {
		t.Fatalf("exact %d", len(h.Exact))
	}
	if s := h.Exact[0]["scores"].(string); s != `{"irreversible":0.1,"reasons":[],"largeOutput":0.3}` {
		t.Fatalf("scores %s", s)
	}
	no := false
	unscored := ToHoldRecord(HoldItem{At: time.Now().UnixMilli(), Tool: "write", Level: "allow", Reasons: []string{}, Scores: &CallScores{Irreversible: 0.1, Scope: "expected_step"}, Held: &no}, "/large-output/project", HoldContext{})
	if unscored.Scores.LargeOutput != nil {
		t.Fatal("a verdict without the score writes no largeOutput key")
	}
}

// twin: tests/learning.test.ts:349
func TestLearningRegretOnAllowedCallPersisted(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	id := mustHold(t, l, baseHold("/regret-allowed/project", func(h *HoldRecord) {
		h.Tool, h.CommandPreview, h.Held = "write", "file.ts", false
		h.Scores, h.Reasons = HoldScores{Irreversible: 0.1, Reasons: []string{}}, []string{}
	}))
	l.RecordOutcome(id, "regretted")
	no := false
	rows, _ := l.QueryHoldsForProject("/regret-allowed/project", &no)
	if len(rows) != 1 || rows[0]["outcome"] != "regretted" {
		t.Fatalf("%v", rows)
	}
}

// twin: tests/learning.test.ts:367
func TestLearningSkippedCallProducesNoRow(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	rows, err := l.QueryHoldsForProject("/skipped/project", nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("%v %v", rows, err)
	}
}

// twin: tests/learning.test.ts:375
func TestLearningHoldStatsZeroForUnknownProject(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	s, err := l.HoldStats("/nonexistent/project")
	if err != nil || s != (HoldStats{}) {
		t.Fatalf("%+v %v", s, err)
	}
}

// twin: tests/learning.test.ts:387
func TestLearningHoldStatsCountsHeldRows(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	now := time.Now().UnixMilli()
	rec := func(ts int64, cmd string) HoldRecord {
		return baseHold("/stats/project", func(h *HoldRecord) {
			h.Timestamp, h.CommandPreview, h.Level = ts, cmd, "confirm"
			h.Scores, h.Reasons = HoldScores{Irreversible: 0.5, Reasons: []string{}}, []string{}
		})
	}
	id1 := mustHold(t, l, rec(now-3000, "cmd1"))
	id2 := mustHold(t, l, rec(now-2000, "cmd2"))
	id3 := mustHold(t, l, rec(now-1000, "cmd3"))
	l.RecordOutcome(id1, "approved")
	l.RecordOutcome(id2, "declined")
	l.RecordOutcome(id3, "replanned")
	s, err := l.HoldStats("/stats/project")
	if err != nil || s.Held != 3 || s.Labeled != 3 || s.Approved != 1 || s.Declined != 1 || s.Replanned != 1 || s.Oldest != now-3000 || s.Newest != now-1000 {
		t.Fatalf("%+v %v", s, err)
	}
}

// twin: tests/learning.test.ts:409
func TestLearningHoldStatsCountsAllowedRows(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	now := time.Now().UnixMilli()
	rec := func(ts int64) HoldRecord {
		return baseHold("/stats/allowed", func(h *HoldRecord) {
			h.Timestamp, h.Tool, h.CommandPreview, h.Level, h.Held = ts, "read", "file.ts", "allow", false
			h.Scores, h.Reasons = HoldScores{Irreversible: 0.1, Reasons: []string{}}, []string{}
		})
	}
	id1 := mustHold(t, l, rec(now-2000))
	id2 := mustHold(t, l, rec(now-1000))
	l.RecordOutcome(id1, "accepted")
	l.RecordOutcome(id2, "regretted")
	s, err := l.HoldStats("/stats/allowed")
	if err != nil || s.Allowed != 2 || s.Accepted != 1 || s.Regretted != 1 || s.Held != 0 {
		t.Fatalf("%+v %v", s, err)
	}
}

// twin: tests/learning.test.ts:428
func TestLearningBusyTimeoutIsSetOnFreshConnection(t *testing.T) {
	db, err := openSQLite(filepath.Join(t.TempDir(), "busy-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	row, ok, err := db.get("PRAGMA busy_timeout")
	if err != nil || !ok || row["timeout"] != int64(10000) {
		t.Fatalf("%v %v %v", row, ok, err)
	}
	mode, _, _ := db.get("PRAGMA journal_mode")
	if mode["journal_mode"] != "wal" {
		t.Fatalf("journal mode %v", mode)
	}
}

// twin: tests/learning.test.ts:438
func TestLearningInitSchemaDoesNotVacuumWhenNothingIsPastCutoff(t *testing.T) {
	db, err := openSQLite(filepath.Join(t.TempDir(), "vacuum-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	_, _, _ = db.exec("CREATE TABLE t (id INTEGER PRIMARY KEY, ts INTEGER)")
	_, _, _ = db.exec("INSERT INTO t (ts) VALUES (?)", time.Now().UnixMilli())
	freelist := func() int64 { r, _, _ := db.get("PRAGMA freelist_count"); return r["freelist_count"].(int64) }
	before := freelist()
	changes, _, _ := db.exec("DELETE FROM t WHERE ts < ?", time.Now().UnixMilli()-365*86_400_000)
	if changes != 0 || freelist() != before {
		t.Fatalf("changes %d, freelist %d -> %d", changes, before, freelist())
	}
}

// The pruning itself, which the original's test leaves to initSchema: rows past the retention window go, newer stay,
// and a retention of zero keeps everything.
func TestLearningInitSchemaPrunesPastRetention(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(0)
	old := time.Now().UnixMilli() - 400*86_400_000
	mustHold(t, l, baseHold("/prune", func(h *HoldRecord) { h.Timestamp = old }))
	mustHold(t, l, baseHold("/prune", nil))
	l.InitSchema(0)
	if rows, _ := l.QueryHoldsForProject("/prune", nil); len(rows) != 2 {
		t.Fatalf("retention 0 keeps everything: %d", len(rows))
	}
	l.InitSchema(365)
	if rows, _ := l.QueryHoldsForProject("/prune", nil); len(rows) != 1 {
		t.Fatalf("the old row is pruned: %d", len(rows))
	}
}

func dirsHold(project string) HoldRecord {
	return baseHold(project, func(h *HoldRecord) { h.CommandPreview, h.Level = "cmd", "confirm" })
}

// twin: tests/learning-dirs.test.ts:37
func TestLearningInterleavedDirectoriesKeepRowsApart(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	a := NewLearning(LearningPath(filepath.Join(t.TempDir(), "pi-warden"), func(string) string { return "" }), nil)
	b := NewLearning(LearningPath(filepath.Join(t.TempDir(), "pi-warden"), func(string) string { return "" }), nil)
	a.InitSchema(0)
	idA := mustHold(t, a, dirsHold("/proj"))
	if idA <= 0 {
		t.Fatal("id")
	}
	if _, err := os.Stat(a.path); err != nil {
		t.Fatalf("A's database is under A's dir: %v", err)
	}
	b.InitSchema(0)
	idB := mustHold(t, b, dirsHold("/proj"))
	if _, err := os.Stat(b.path); err != nil {
		t.Fatalf("B's database is under B's dir: %v", err)
	}
	againA, _ := a.QueryHoldsForProject("/proj", nil)
	rowsB, _ := b.QueryHoldsForProject("/proj", nil)
	if len(againA) != 1 || len(rowsB) != 1 {
		t.Fatalf("A sees %d, B sees %d", len(againA), len(rowsB))
	}
	a.RecordOutcome(idA, "approved")
	b.RecordOutcome(idB, "declined")
	sa, _ := a.HoldStats("/proj")
	sb, _ := b.HoldStats("/proj")
	if sa.Approved != 1 || sb.Declined != 1 || sb.Approved != 0 {
		t.Fatalf("%+v %+v", sa, sb)
	}
}

// twin: tests/learning-dirs.test.ts:62 (the override is PIGPEN_WARDEN_DB, deviation D6)
func TestLearningDBEnvironmentWinsOverDirectories(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	override := filepath.Join(t.TempDir(), "override.db")
	getenv := func(k string) string {
		if k == learningDBEnv {
			return override
		}
		return ""
	}
	a := NewLearning(LearningPath(filepath.Join(t.TempDir(), "pi-warden"), getenv), nil)
	b := NewLearning(LearningPath(filepath.Join(t.TempDir(), "pi-warden"), getenv), nil)
	a.InitSchema(0)
	mustHold(t, a, dirsHold("/proj"))
	if _, err := os.Stat(override); err != nil {
		t.Fatalf("the override path is used: %v", err)
	}
	if rows, _ := b.QueryHoldsForProject("/proj", nil); len(rows) != 1 {
		t.Fatalf("both directories resolve to the same override database: %d", len(rows))
	}
}

// twin: tests/learning-dirs.test.ts:77
func TestLearningFailedOpenIsRememberedAndWarnedOnce(t *testing.T) {
	t.Cleanup(CloseLearningDatabases)
	bad := t.TempDir()
	dbPath := filepath.Join(bad, "pi-warden", "holds.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o700)
	_ = os.WriteFile(dbPath, []byte("this is not a database"), 0o600)
	var mu sync.Mutex
	var warnings []string
	warn := func(s string) { mu.Lock(); warnings = append(warnings, s); mu.Unlock() }
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, w := range warnings {
			if strings.Contains(w, "could not open") {
				n++
			}
		}
		return n
	}
	badL := NewLearning(dbPath, warn)
	badL.InitSchema(0)
	badL.InitSchema(0)
	if id, err := badL.RecordHold(dirsHold("/proj")); id != 0 || err != nil {
		t.Fatalf("the NOOP database answers id 0 instead of failing: %d %v", id, err)
	}
	if count() != 1 {
		t.Fatalf("warned once, not per call: %v", warnings)
	}
	fresh := NewLearning(filepath.Join(t.TempDir(), "pi-warden", "holds.db"), warn)
	fresh.InitSchema(0)
	if id := mustHold(t, fresh, dirsHold("/proj")); id <= 0 {
		t.Fatal("a fresh directory records after another path failed")
	}
	if count() != 1 {
		t.Fatalf("no new warning for the good path: %v", warnings)
	}
}

// twin: tests/sqlite-adapter.test.ts:116 (the engine is the embedded one behind sqlStore; the original runs this on bun:sqlite)
func TestSQLiteStoreOpensARealDatabaseWithTheSchemaPragmasAndRowsLearningNeeds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "nested", "holds.db")
	db, err := openSQLite(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the store created the missing parent directory: %v", err)
	}
	if _, _, err := db.exec(HoldsSchema); err != nil {
		t.Fatal(err)
	}
	mode, _, _ := db.get("PRAGMA journal_mode")
	if mode["journal_mode"] != "wal" {
		t.Fatalf("journal_mode = WAL is set: %v", mode)
	}
	busy, _, _ := db.get("PRAGMA busy_timeout")
	if busy["timeout"] != int64(10000) {
		t.Fatalf("busy_timeout = 10000 is set: %v", busy)
	}
	changes, id, err := db.exec("INSERT INTO holds (timestamp, project_root, tool, signature_hash, scores, level, held, reasons) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		time.Now().UnixMilli(), "/bun/project", "bash", "hash", `{"irreversible":0.5,"reasons":[]}`, "confirm", 1, `["irreversible 0.5"]`)
	if err != nil || changes != 1 || id <= 0 {
		t.Fatalf("recordHold's id path works: %d %d %v", changes, id, err)
	}
	row, ok, _ := db.get("SELECT tool, held FROM holds WHERE id = ?", id)
	if !ok || row["tool"] != "bash" || row["held"] != int64(1) {
		t.Fatalf("row values: %v", row)
	}
	if _, ok, _ := db.get("SELECT tool FROM holds WHERE id = ?", 999_999); ok {
		t.Fatal("a miss answers no row")
	}
	if rows, _ := db.all("SELECT tool FROM holds WHERE id > ?", 999_999); len(rows) != 0 || rows == nil {
		t.Fatalf("no rows answers an empty list: %v", rows)
	}
	count, _, _ := db.get("SELECT COUNT(*) AS total FROM holds")
	if count["total"] != int64(1) {
		t.Fatalf("aggregate queries answer a row, as holdStats reads them: %v", count)
	}
}

func TestLearningShouldSkipNeedsThreeExactApprovals(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	now := time.Now().UnixMilli()
	for i := range 2 {
		id := mustHold(t, l, baseHold("/two/project", func(h *HoldRecord) { h.Timestamp = now - int64(i)*1000 }))
		l.RecordOutcome(id, "approved")
	}
	r, err := l.ShouldSkipHold("bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/two/project")
	if err != nil || r.Skip || r.Confidence <= 0.8 {
		t.Fatalf("two approvals are not enough, however sure: %+v %v", r, err)
	}
}

func TestLearningSmartHistoryIgnoresFarIrreversibleScoresAndOtherReasonCategories(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	mustHold(t, l, baseHold("/far/project", func(h *HoldRecord) {
		h.Scores = HoldScores{Irreversible: 0.9, Reasons: []string{"destructive: git force push"}}
		h.Reasons = []string{"destructive: git force push"}
	}))
	h := history(t, l, "bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/far/project")
	if len(h.Similar) != 0 || len(h.SameReason) != 0 || len(h.Exact) != 0 {
		t.Fatalf("0.9 is not within 0.2 of 0.5, and the categories differ: %+v", h)
	}
	same := history(t, l, "bash", HoldScores{Irreversible: 0.1, Reasons: []string{"destructive: anything"}}, "/far/project")
	if len(same.SameReason) != 1 {
		t.Fatalf("the same reason category matches across projects and scores: %+v", same)
	}
}

func approvedHolds(t *testing.T, l *Learning, project string, n int, outcome string, reasons ...string) {
	t.Helper()
	for i := range n {
		id := mustHold(t, l, baseHold(project, func(h *HoldRecord) {
			h.Timestamp = time.Now().UnixMilli() - int64(i)*1000
			h.Level = "confirm"
			h.Scores.Reasons, h.Reasons = reasons, reasons
		}))
		l.RecordOutcome(id, outcome)
	}
}

func TestLearningThresholdsNeedEnoughHoldsBeforeSuggestingAnything(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	approvedHolds(t, l, "few", 9, "approved", "irreversible 0.5")
	approvedHolds(t, l, "twelve", 12, "approved", "irreversible 0.5")
	approvedHolds(t, l, "stood", 25, "declined", "irreversible 0.5")
	approvedHolds(t, l, "twenty", 20, "approved", "irreversible 0.5")
	for _, c := range []struct {
		project string
		want    int
	}{{"few", 0}, {"twelve", 0}, {"stood", 0}, {"twenty", 1}} {
		a, err := l.AnalyzeThresholds(c.project)
		if err != nil || len(a) != c.want {
			t.Errorf("%s: %d adjustments (%v), want %d", c.project, len(a), err, c.want)
		}
	}
	a, _ := l.AnalyzeThresholds("twenty")
	if a[0].Reason != "Low precision (0%); consider lowering the confirmation threshold" || a[0].Confidence != 0.4 || a[0].SuggestedThreshold != 0.6 {
		t.Fatalf("%+v", a[0])
	}
}

func TestLearningHighRegretRateSuggestsRaisingTheThreshold(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	for i := range 30 {
		id := mustHold(t, l, baseHold("/regrets", func(h *HoldRecord) {
			h.Held, h.Level = false, "allow"
			h.Timestamp = time.Now().UnixMilli() - int64(i)*1000
		}))
		if i < 6 {
			l.RecordOutcome(id, "regretted")
		} else {
			l.RecordOutcome(id, "accepted")
		}
	}
	a, err := l.AnalyzeThresholds("/regrets")
	if err != nil || len(a) != 1 || a[0].SuggestedThreshold != 0.75 || a[0].Reason != "High regret rate (20%); consider raising the confirmation threshold" || a[0].Confidence != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	recs, _ := l.GenerateRecommendations("/regrets")
	if len(recs) != 1 || recs[0].Priority != "high" || recs[0].Message != "High regret rate (20%); consider raising the confirmation threshold (confidence: 100%)" {
		t.Fatalf("%+v", recs)
	}
	// Ten rows are the floor, and 15% is not over it.
	for _, c := range []struct{ rows, regrets int }{{9, 9}, {20, 3}} {
		l2 := testLearning(t)
		l2.InitSchema(365)
		for i := range c.rows {
			id := mustHold(t, l2, baseHold("/edge", func(h *HoldRecord) { h.Held, h.Level = false, "allow" }))
			if i < c.regrets {
				l2.RecordOutcome(id, "regretted")
			} else {
				l2.RecordOutcome(id, "accepted")
			}
		}
		if a, _ := l2.AnalyzeThresholds("/edge"); len(a) != 0 {
			t.Errorf("%+v adjusts", c)
		}
		CloseLearningDatabases()
	}
}

func TestLearningPatternInsightsNeedFiveHoldsAndMoreThanHalfApproved(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	approvedHolds(t, l, "p", 4, "approved", "risky: chmod")
	approvedHolds(t, l, "p", 6, "approved", "destructive: git force push", "irreversible 0.9")
	approvedHolds(t, l, "p", 4, "declined", "destructive: git force push", "irreversible 0.9")
	approvedHolds(t, l, "even", 5, "approved", "sensitive: .env")
	approvedHolds(t, l, "even", 5, "declined", "sensitive: .env")
	got, err := l.AnalyzePatterns("p")
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	// Sorted by false positive rate: destructive 6/10, irreversible 6/10 (both reasons carry the rows); chmod has 4 rows.
	for _, in := range got {
		if in.SampleSize != 10 || in.FalsePositiveRate != 0.6 {
			t.Fatalf("%+v", in)
		}
	}
	if got[0].Suggestion != "Pattern 'destructive' has a 60% false positive rate; consider adding it to exemptRules or raising its threshold" {
		t.Fatalf("%q", got[0].Suggestion)
	}
	if even, _ := l.AnalyzePatterns("even"); len(even) != 0 {
		t.Fatalf("50%% is not more than half: %+v", even)
	}
	recs, _ := l.GenerateRecommendations("p")
	for _, r := range recs {
		if r.Type != "exempt" || r.Priority != "medium" {
			t.Fatalf("60%% is medium: %+v", r)
		}
	}
	if len(recs) != 0 {
		// 0.6 is not over 0.6: nothing to recommend.
		t.Fatalf("%+v", recs)
	}
	approvedHolds(t, l, "q", 9, "approved", "destructive: rm")
	approvedHolds(t, l, "q", 1, "declined", "destructive: rm")
	recs, _ = l.GenerateRecommendations("q")
	if len(recs) != 1 || recs[0].Priority != "high" {
		t.Fatalf("90%% over 10 holds is high: %+v", recs)
	}
}

func TestLearningSteerReportSuggestsRewordingIneffectiveTypes(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	for _, c := range []struct {
		reason  string
		n       int
		outcome string
	}{{"off-task drift", 6, "declined"}, {"an intent mismatch: x", 1, "approved"}, {"pattern match", 3, "approved"}, {"intent mismatch", 4, "declined"}, {"weird", 2, "approved"}} {
		for range c.n {
			id := mustHold(t, l, baseHold("steers", func(h *HoldRecord) { h.AgentReason = c.reason; h.Level = "confirm" }))
			l.RecordOutcome(id, c.outcome)
		}
	}
	r, err := l.AnalyzeSteerEffectivenessReport("steers")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Suggestions) != 2 || r.Suggestions[0] != "Steer type 'intent-mismatch' has a 20% effectiveness rate; consider rewording or adding more specific guidance" ||
		r.Suggestions[1] != "Steer type 'off-task' has a 0% effectiveness rate; consider rewording or adding more specific guidance" {
		t.Fatalf("%q", r.Suggestions)
	}
	if r.ByType["intent-mismatch"] != (SteerTypeStats{Effective: 1, Total: 5, Rate: 0.2}) || r.ByType["other"].Total != 2 || r.ByType["pattern"].Rate != 1 {
		t.Fatalf("%+v", r.ByType)
	}
	if len(r.TopPatterns) != 3 || r.TopPatterns[0].Pattern != "pattern" {
		t.Fatalf("types with fewer than 3 samples are not patterns; best first: %+v", r.TopPatterns)
	}
}

func confidenceOf(t *testing.T, h SmartHistory) float64 {
	t.Helper()
	return CalculateSmartConfidence(h).Confidence
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLearningConfidenceWeightsAgeAndTheRowSets(t *testing.T) {
	now := time.Now().UnixMilli()
	week := int64(7 * 24 * 3600 * 1000)
	// exact approved five weeks ago (5 x 0.4) against a recent replanned similar row (2 x 1.0, counted half negative):
	// (2 - 1) / 4 = 0.25 -> (0.25 + 1) / 2.
	old := SmartHistory{Exact: []Row{{"outcome": "approved", "timestamp": now - 5*week}}, Similar: []Row{{"outcome": "replanned", "timestamp": now - 1000}}}
	if got := confidenceOf(t, old); !near(got, 0.625) {
		t.Fatalf("old exact rows weigh 40%% and an exact row 5: %v", got)
	}
	// exact approved two weeks ago (5 x 0.7) and a recent replanned same-reason row (1 x 1.0, half negative): 3 / 4.5.
	mid := SmartHistory{Exact: []Row{{"outcome": "approved", "timestamp": now - 2*week}}, SameReason: []Row{{"outcome": "replanned", "timestamp": now - 1000}}}
	if got := confidenceOf(t, mid); !near(got, (3.0/4.5+1)/2) {
		t.Fatalf("medium-age rows weigh 70%%: %v", got)
	}
	// Only replanned rows: ratio -0.5 -> 0.25 (the transform keeps it above zero).
	replanned := SmartHistory{Exact: []Row{{"outcome": "replanned", "timestamp": now - 1000}}}
	if got := confidenceOf(t, replanned); !near(got, 0.25) {
		t.Fatalf("replans subtract half: %v", got)
	}
	// Rows without a number timestamp or a string outcome do not count.
	junk := SmartHistory{Exact: []Row{{"outcome": nil, "timestamp": now}, {"outcome": "approved", "timestamp": "x"}}}
	if c := CalculateSmartConfidence(junk); c.Reason != "no history" {
		t.Fatalf("%+v", c)
	}
	if r := CalculateSmartConfidence(SmartHistory{Exact: []Row{{"outcome": "approved", "timestamp": now}}}); r.Reason != "high confidence approval" || r.ExactCount != 1 {
		t.Fatalf("%+v", r)
	}
	if r := CalculateSmartConfidence(replanned); r.Reason != "low confidence" {
		t.Fatalf("%+v", r)
	}
	borderline := SmartHistory{Exact: []Row{{"outcome": "approved", "timestamp": now}}, Similar: []Row{{"outcome": "declined", "timestamp": now}, {"outcome": "declined", "timestamp": now}, {"outcome": "declined", "timestamp": now}, {"outcome": "declined", "timestamp": now}}}
	if r := CalculateSmartConfidence(borderline); r.Reason != "borderline" {
		t.Fatalf("%+v %v", r, r.Confidence)
	}
}

func TestLearningInitSchemaPruneKeepsTheRecentRows(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(0)
	mustHold(t, l, baseHold("/prune2", func(h *HoldRecord) { h.Timestamp = time.Now().UnixMilli() - 400*86_400_000; h.CommandPreview = "old" }))
	mustHold(t, l, baseHold("/prune2", func(h *HoldRecord) { h.CommandPreview = "recent" }))
	l.InitSchema(365)
	rows, _ := l.QueryHoldsForProject("/prune2", nil)
	if len(rows) != 1 || rows[0]["command_preview"] != "recent" {
		t.Fatalf("%v", rows)
	}
}

func TestLearningShouldSkipNeedsConfidenceOverEightyPercent(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	approvedHolds(t, l, "/mixed", 3, "approved", "irreversible 0.5")
	approvedHolds(t, l, "/mixed", 3, "declined", "irreversible 0.5")
	r, err := l.ShouldSkipHold("bash", HoldScores{Irreversible: 0.5, Reasons: []string{"irreversible 0.5"}}, "/mixed")
	if err != nil || r.Skip || r.Confidence <= 0.7 || r.Confidence > 0.8 {
		t.Fatalf("six exact rows, half approved: not sure enough to skip: %+v %v", r, err)
	}
	if r.Reason != "high confidence approval" {
		t.Fatalf("%+v", r)
	}
}

func TestLearningPatternInsightsSortByFalsePositiveRateAndRecommendationsTakeTheTopThree(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	approvedHolds(t, l, "rank", 7, "approved", "aaa: first seen")
	approvedHolds(t, l, "rank", 3, "declined", "aaa: first seen")
	approvedHolds(t, l, "rank", 10, "approved", "bbb: second")
	approvedHolds(t, l, "rank", 5, "approved", "ccc: third")
	approvedHolds(t, l, "rank", 5, "approved", "ddd: fourth")
	approvedHolds(t, l, "rank", 5, "approved", "eee: fifth")
	got, err := l.AnalyzePatterns("rank")
	if err != nil || len(got) != 5 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[4].Pattern != "aaa" || got[4].FalsePositiveRate != 0.7 {
		t.Fatalf("the 70%% pattern sorts last: %+v", got)
	}
	recs, err := l.GenerateRecommendations("rank")
	exempt := 0
	for _, r := range recs {
		if r.Type == "exempt" {
			exempt++
			if r.Priority != "high" || strings.Contains(r.Message, "'aaa'") {
				t.Fatalf("%+v", r)
			}
		}
	}
	if err != nil || exempt != 3 {
		t.Fatalf("only the top three patterns are recommended: %d %v", exempt, err)
	}
	if len(recs) != 4 || recs[0].Priority != "high" || recs[3].Type != "threshold" || recs[3].Priority != "medium" {
		t.Fatalf("high priority first: the medium threshold note came first when found and comes last: %+v", recs)
	}
	// A 70% pattern alone is a medium recommendation.
	l2 := testLearning(t)
	l2.InitSchema(365)
	approvedHolds(t, l2, "p70", 7, "approved", "aaa: x")
	approvedHolds(t, l2, "p70", 3, "declined", "aaa: x")
	recs, _ = l2.GenerateRecommendations("p70")
	if len(recs) != 1 || recs[0].Priority != "medium" {
		t.Fatalf("%+v", recs)
	}
}

func TestLearningSteerReportNeedsFiveSteersBeforeSuggestingRewording(t *testing.T) {
	l := testLearning(t)
	l.InitSchema(365)
	for range 4 {
		id := mustHold(t, l, baseHold("few", func(h *HoldRecord) { h.AgentReason = "off-task drift"; h.Level = "confirm" }))
		l.RecordOutcome(id, "declined")
	}
	r, err := l.AnalyzeSteerEffectivenessReport("few")
	if err != nil || len(r.Suggestions) != 0 || len(r.TopPatterns) != 1 {
		t.Fatalf("four steers are too few to judge: %+v %v", r, err)
	}
}
