package warden

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// The learning database is the file the original writes with node:sqlite. This test reads and writes one with the
// original's own module on the other side (port/perf/learning-cross.mts); it runs only when the environment names the
// directory the two share:
//
//	WARDEN_LEARNING_CROSS=<dir> WARDEN_LEARNING_CROSS_MODE=read|write go test -run TestLearningCrossCheck .
//
// read: the original wrote <dir>/holds.db (mode write of the script) and its holdStats JSON is in <dir>/stats.json.
// write: this test writes <dir>/holds.db for the original to read back.
func TestLearningCrossCheck(t *testing.T) {
	dir := os.Getenv("WARDEN_LEARNING_CROSS")
	if dir == "" {
		t.Skip("WARDEN_LEARNING_CROSS names the directory shared with the original; not set")
	}
	t.Cleanup(CloseLearningDatabases)
	l := NewLearning(dir+"/holds.db", nil)
	const project = "/cross/project"
	switch os.Getenv("WARDEN_LEARNING_CROSS_MODE") {
	case "read":
		stats, err := l.HoldStats(project)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(dir + "/stats.json")
		if err != nil {
			t.Fatal(err)
		}
		var want map[string]float64
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		got := map[string]float64{"held": float64(stats.Held), "labeled": float64(stats.Labeled), "approved": float64(stats.Approved), "declined": float64(stats.Declined), "replanned": float64(stats.Replanned),
			"allowed": float64(stats.Allowed), "regretted": float64(stats.Regretted), "accepted": float64(stats.Accepted), "oldest": float64(stats.Oldest), "newest": float64(stats.Newest)}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the Go port reads %v from the original's file, the original reports %v", got, want)
		}
		h, err := l.QuerySmartHistory("bash", HoldScores{Irreversible: 0.5, Reasons: []string{"destructive: git force push"}}, project)
		if err != nil || len(h.Exact) != 4 || len(h.SameReason) != 4 {
			t.Fatalf("exact %d, same reason %d: %v", len(h.Exact), len(h.SameReason), err)
		}
	case "write":
		l.InitSchema(365)
		now := time.Now().UnixMilli()
		rows := []struct {
			held    bool
			outcome string
			age     int64
		}{{true, "approved", 1}, {true, "declined", 2}, {true, "replanned", 3}, {false, "accepted", 4}, {false, "regretted", 5}, {true, "approved", 6}}
		for _, r := range rows {
			lo := 0.3
			id, err := l.RecordHold(HoldRecord{Timestamp: now - r.age*1000, ProjectRoot: project, Tool: "bash", CommandPreview: "cmd", Scores: HoldScores{Irreversible: 0.5, Reasons: []string{"destructive: git force push"}, LargeOutput: &lo},
				Level: "confirm", Held: r.held, Reasons: []string{"destructive: git force push"}, AgentReason: "irreversible action detected"})
			if err != nil {
				t.Fatal(err)
			}
			l.RecordOutcome(id, r.outcome)
		}
	default:
		t.Fatal("WARDEN_LEARNING_CROSS_MODE must be read or write")
	}
}
