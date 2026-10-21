package pi_background_tasks

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// Twins of the original's persistence tests.

func boundedSnapshot(mods ...func(*snapshot)) snapshot {
	s := snapshot{
		Command: "printf ready", Cwd: "/tmp/worktree", ID: "bg-1", LastOutputAt: new(int64(0)), LogFile: "/tmp/bg-1.log", NotifyMode: "always", NotifyOnExit: true,
		Pid: 1234, StartedAt: 1_700_000_000_000, Status: "completed", Title: "fake task", UpdatedAt: 1_700_000_000_500,
		VoidedWakeSequences: []int{}, WakeEvents: []wakeEvent{}, PendingWakes: []wakePending{}, LastOutputDedupeBy: map[string]string{},
	}
	for _, m := range mods {
		m(&s)
	}
	return s
}

func TestTwinPersistence(t *testing.T) {
	sessionPersistence := func(t *testing.T, list func() []snapshot, appended *[]any) (*persistence, string) {
		agent := testAgent(t)
		_ = agent
		path := sidecarStatePath("session")
		return newPersistence(persistenceDeps{
			customType:    "kendex-background-tasks:state",
			appendEntry:   func(_ string, payload any) error { *appended = append(*appended, payload); return nil },
			activeSession: func() (sessionRef, bool) { return sessionRef{id: "session", sidecarPath: path}, true },
			listSnapshots: list,
		}), path
	}

	tw(t, "bounded-snapshot", "snapshot append decisions follow content changes", func(t *testing.T) {
		var appended []any
		var snapshots []snapshot
		p, sidecarFile := sessionPersistence(t, func() []snapshot { return snapshots }, &appended)
		for _, row := range []struct {
			name, status string
			updatedAt    int64
			reason       string
			count        int
		}{
			{"first snapshot appends", "running", 1, "appended", 1},
			{"identical snapshot does not append again", "running", 1, "unchanged", 1},
			{"changed snapshot appends again", "completed", 3, "appended", 2},
		} {
			snapshots = []snapshot{boundedSnapshot(func(s *snapshot) { s.Status, s.UpdatedAt = row.status, row.updatedAt })}
			result := p.persistSnapshots()
			raw, err := os.ReadFile(sidecarFile)
			if err != nil {
				t.Fatal(err)
			}
			var sidecar persistencePayload
			if err := json.Unmarshal(raw, &sidecar); err != nil {
				t.Fatal(err)
			}
			// The sidecar body is the compact serialization plus one newline.
			again, _ := json.Marshal(sidecar)
			eq(t, result, persistResult{appendEntry: true, sidecar: true, appendReason: row.reason}, row.name+": result")
			eq(t, len(appended), row.count, row.name+": append count")
			eq(t, sidecar.Tasks, snapshots, row.name+": sidecar tasks")
			eq(t, string(raw), string(again)+"\n", row.name+": sidecar text")
		}
	})

	tw(t, "bounded-snapshot", "oversized snapshots append a bounded manifest", func(t *testing.T) {
		for _, row := range []struct {
			name      string
			taskCount int
		}{{"completed tasks with large commands", 70}, {"large task list keeps a bounded fingerprint", 1000}} {
			snapshots := make([]snapshot, row.taskCount)
			for i := range snapshots {
				snapshots[i] = boundedSnapshot(func(s *snapshot) {
					s.ID, s.Command, s.LogFile = "bg-"+itoa(i), repeat("x", 10*1024), "/tmp/bg-"+itoa(i)+".log"
				})
			}
			var appended []any
			p, sidecarFile := sessionPersistence(t, func() []snapshot { return snapshots }, &appended)
			result := p.persistSnapshots()
			manifest, _ := appended[0].(bgTasksBoundedManifest)
			raw, _ := os.ReadFile(sidecarFile)
			var sidecar persistencePayload
			_ = json.Unmarshal(raw, &sidecar)
			a, _ := json.Marshal(sidecar.Tasks)
			b, _ := json.Marshal(snapshots)
			eq(t, result, persistResult{appendEntry: true, sidecar: true, appendReason: "manifest"}, row.name+": result")
			eq(t, len(appended), 1, row.name+": count")
			eq(t, isBgTasksBoundedManifest(&manifest), true, row.name+": manifest")
			eq(t, manifest.Counts.Tasks, row.taskCount, row.name+": task count")
			mustTrue(t, manifest.ByteSize > bgTasksSnapshotMaxBytes, row.name+": original exceeds the cap")
			mustTrue(t, jsonLen(manifest) <= bgTasksSnapshotMaxBytes, row.name+": manifest fits the cap")
			mustTrue(t, regexp.MustCompile(`^[0-9a-f]+$`).MatchString(manifest.Fingerprint) && len(manifest.Fingerprint) <= 128, row.name+": fingerprint")
			eq(t, string(a), string(b), row.name+": the sidecar keeps every task")
		}
	})

	tw(t, "bounded-snapshot-barrier", "custom entries restore the sidecar at bounded snapshot barriers", func(t *testing.T) {
		manifestEntry := map[string]any{"version": 2.0, "fullSnapshot": false, "reason": "payload-too-large", "byteSize": 999_999.0, "fingerprint": "abc", "counts": map[string]any{"tasks": 1.0}, "updatedAt": 2.0}
		full := func(id string) any {
			return map[string]any{"tasks": []snapshot{boundedSnapshot(func(s *snapshot) { s.ID = id })}, "updatedAt": 1.0}
		}
		rows := []struct {
			name       string
			initial    []string
			sidecar    []string
			hasSidecar bool
			entries    []any
			expected   [][]string
		}{
			{"older full snapshot is replaced by the later sidecar barrier", []string{"bg-new"}, []string{"bg-new"}, true, []any{full("bg-old"), manifestEntry}, [][]string{{"bg-old"}, {"bg-new"}}},
			{"barrier without a loaded sidecar preserves current tasks", []string{"bg-pre"}, nil, false,
				[]any{map[string]any{"version": 2.0, "fullSnapshot": false, "reason": "payload-too-large", "byteSize": 1.0, "fingerprint": "x", "counts": map[string]any{"tasks": 0.0}, "updatedAt": 0.0}}, [][]string{{"bg-pre"}}},
			{"barrier marker is independent of envelope version", []string{"bg-old"}, []string{"bg-sidecar"}, true,
				[]any{map[string]any{"version": 3.0, "fullSnapshot": false, "reason": "some-new-reason"}}, [][]string{{"bg-sidecar"}}},
		}
		for _, row := range rows {
			var current []snapshot
			for _, id := range row.initial {
				current = append(current, boundedSnapshot(func(s *snapshot) { s.ID = id }))
			}
			var sidecarTasks []snapshot
			for _, id := range row.sidecar {
				sidecarTasks = append(sidecarTasks, boundedSnapshot(func(s *snapshot) { s.ID = id }))
			}
			var states [][]string
			for _, data := range row.entries {
				applyCustomEntryWithBarrier(data, row.hasSidecar, sidecarTasks, func() { current = nil }, func(s snapshot) { current = append(current, s) })
				states = append(states, idsOf(current))
			}
			eq(t, states, row.expected, row.name)
		}
	})

	tw(t, "bounded-snapshot-manifest", "bounded manifest recognition distinguishes snapshot envelopes", func(t *testing.T) {
		for _, row := range []struct {
			name     string
			value    any
			expected bool
		}{
			{"bounded manifest envelope", map[string]any{"version": 2.0, "fullSnapshot": false, "reason": "payload-too-large", "byteSize": 999_999.0, "fingerprint": "abc", "counts": map[string]any{"tasks": 70.0}, "updatedAt": 0.0}, true},
			{"full snapshot envelope", map[string]any{"version": 1.0, "tasks": []any{}, "updatedAt": 0.0}, false},
			{"other version is a barrier but not a recognized manifest", map[string]any{"version": 3.0, "fullSnapshot": false, "reason": "some-new-reason"}, false},
		} {
			eq(t, isBgTasksBoundedManifest(row.value), row.expected, row.name)
		}
	})
}
