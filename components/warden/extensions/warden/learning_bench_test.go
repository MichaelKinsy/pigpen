package warden

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Costs of the learning module on a representative history: 500 rows of one project, half held and labelled.

func benchLearning(b *testing.B) *Learning {
	b.Helper()
	l := NewLearning(filepath.Join(b.TempDir(), "holds.db"), nil)
	b.Cleanup(CloseLearningDatabases)
	l.InitSchema(365)
	now := time.Now().UnixMilli()
	for i := range 500 {
		h := HoldRecord{Timestamp: now - int64(i)*1000, ProjectRoot: "/bench/project", Tool: "bash", CommandPreview: "npm test " + strconv.Itoa(i),
			Scores: HoldScores{Irreversible: 0.5, Reasons: []string{"destructive: git force push", "irreversible 0.5"}}, Level: "confirm", Held: i%2 == 0,
			Reasons: []string{"destructive: git force push", "irreversible 0.5"}, AgentReason: "irreversible action detected"}
		id, err := l.RecordHold(h)
		if err != nil {
			b.Fatal(err)
		}
		l.RecordOutcome(id, []string{"approved", "declined", "replanned", "accepted"}[i%4])
	}
	return l
}

func BenchmarkLearningRecordHold(b *testing.B) {
	l := benchLearning(b)
	h := baseHold("/bench/project", nil)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := l.RecordHold(h); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLearningGenerateRecommendations(b *testing.B) {
	l := benchLearning(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := l.GenerateRecommendations("/bench/project"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLearningSteerEffectivenessReport(b *testing.B) {
	l := benchLearning(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := l.AnalyzeSteerEffectivenessReport("/bench/project"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLearningHoldStats(b *testing.B) {
	l := benchLearning(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := l.HoldStats("/bench/project"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSteerStatsObserveWithPersistence(b *testing.B) {
	s := NewSteerStats(filepath.Join(b.TempDir(), "steer-stats.json"))
	cfg := defaultSteersConfig()
	b.ReportAllocs()
	for b.Loop() {
		s.observe("provider/model", SteerKindRules, SteerOutcome{Followed: true}, cfg)
	}
}

func BenchmarkHoldLedgerRecordAndLabel(b *testing.B) {
	v := holdVerdict(withCommand("git push --force origin main"))
	b.ReportAllocs()
	for b.Loop() {
		l := NewHoldLedger()
		l.Record(v, HoldOptions{Held: true, Mode: "steer", At: 1})
		l.Approved("bash", ViaRetry, 2)
		l.Snapshot()
	}
}

func BenchmarkHoldLogSave(b *testing.B) {
	l := NewHoldLedger()
	v := holdVerdict(withCommand("git push --force origin main"))
	for range 50 {
		l.Record(v, HoldOptions{Held: true, Mode: "steer", At: 1})
	}
	log := NewHoldLog(filepath.Join(b.TempDir(), "holds", "log.jsonl"))
	recs := l.Records()
	b.ReportAllocs()
	for b.Loop() {
		log.Save(recs)
	}
}
