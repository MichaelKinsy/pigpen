package warden

import (
	"path/filepath"
	"testing"
)

// Benchmarks of the tools' own work, on the inputs port/bench-original.ts gives the original (PORT.md, Performance).

func BenchmarkLoopsAddAndListThroughTheFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "loops.json")
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		for _, in := range []map[string]any{{"action": "add", "text": "Follow up " + itoaInt(i)}, {"action": "list"}} {
			if _, err := updateLoops(path, func(s LoopStore) loopReply { return applyLoopAction(s, in, 1) }); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkRememberRecordLesson(b *testing.B) {
	scan := &PrefsScan{Counts: emptyWordCounts()}
	b.ReportAllocs()
	for b.Loop() {
		recordLesson(LessonInput{Lesson: "Never edit the generated client by hand", Session: "s", Now: 1, Scan: scan, Store: emptyPrefsStore()})
	}
}

func BenchmarkExtractPreferences(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		extractPreferences("ok, never force-push the release branch. always squash merge feature branches")
	}
}

func BenchmarkRecallText(b *testing.B) {
	one := 0
	snap := buildCompactSnapshot(snapshotInput{
		Checks:     []compactCheck{{Command: "npm test", Passed: true, RunIndex: 1}},
		Attempts:   []compactAttempt{{Key: "a", Call: "npm run build", Failed: true, Output: "src/a.ts: error TS2322: bad"}},
		Evidence:   &RunEvidence{Mutations: 1, ChecksBeforeMutation: &one, Checks: []Check{{Call: "npm test", Passed: true}}},
		ActiveTask: "Fix the build", Runs: 1,
	})
	b.ReportAllocs()
	for b.Loop() {
		recallText(snap)
	}
}

// BenchmarkNewExtension is construction plus the registration of the tools: what runs before the host sees the
// extension's register frame.
func BenchmarkNewExtension(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		New(Options{})
	}
}
