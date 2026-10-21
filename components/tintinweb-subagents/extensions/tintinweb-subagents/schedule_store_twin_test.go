package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Twins of the original's test/schedule-store.test.ts.
const fScheduleStore = "schedule-store"

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestScheduleStore(t *testing.T) {
	tw(t, fScheduleStore, "resolveStorePath produces session-scoped path under .pi/subagent-schedules/", func(t *testing.T) {
		eq(t, resolveStorePath(filepath.FromSlash("/repo"), "abc123"), filepath.FromSlash("/repo/.pi/subagent-schedules/abc123.json"))
	})
	tw(t, fScheduleStore, "starts empty and round-trips a job through add/list", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.json")
		store := newScheduleStore(file)
		eq(t, len(store.list()), 0)
		job := makeJob(nil)
		eq(t, store.add(job), nil)
		eq(t, store.list(), []scheduledSubagent{job})

		// A new instance on the same file: persistence.
		eq(t, newScheduleStore(file).list(), []scheduledSubagent{job})
	})
	tw(t, fScheduleStore, "update returns merged record and persists the patch", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.json")
		store := newScheduleStore(file)
		job := makeJob(func(j *scheduledSubagent) { j.Name = "before" })
		_ = store.add(job)

		updated, ok, err := store.update(job.ID, func(j *scheduledSubagent) { j.Name, j.RunCount = "after", 3 })
		eq(t, err, nil)
		eq(t, ok, true)
		eq(t, updated.ID, job.ID)
		eq(t, updated.Name, "after")
		eq(t, updated.RunCount, 3)

		fresh := newScheduleStore(file).list()[0]
		eq(t, fresh.Name, "after")
		eq(t, fresh.RunCount, 3)
	})
	tw(t, fScheduleStore, "update returns undefined for unknown id and does not create a record", func(t *testing.T) {
		store := newScheduleStore(filepath.Join(t.TempDir(), "s.json"))
		_, ok, _ := store.update("nonexistent", func(j *scheduledSubagent) { j.Name = "x" })
		eq(t, ok, false)
		eq(t, len(store.list()), 0)
	})
	tw(t, fScheduleStore, "remove returns true on existing job and false on missing", func(t *testing.T) {
		store := newScheduleStore(filepath.Join(t.TempDir(), "s.json"))
		job := makeJob(nil)
		_ = store.add(job)
		ok, _ := store.remove(job.ID)
		eq(t, ok, true)
		eq(t, len(store.list()), 0)
		ok, _ = store.remove(job.ID)
		eq(t, ok, false)
	})
	tw(t, fScheduleStore, "hasName excludes a given id (for rename safety)", func(t *testing.T) {
		store := newScheduleStore(filepath.Join(t.TempDir(), "s.json"))
		job := makeJob(func(j *scheduledSubagent) { j.Name = "alpha" })
		_ = store.add(job)
		eq(t, store.hasName("alpha", ""), true)
		eq(t, store.hasName("alpha", job.ID), false) // excluded: its own record
		eq(t, store.hasName("beta", ""), false)
	})
	tw(t, fScheduleStore, "uses atomic temp+rename — write produces final file, no .tmp leftover", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.json")
		store := newScheduleStore(file)
		_ = store.add(makeJob(nil))
		eq(t, exists(file), true)
		eq(t, exists(file+".tmp"), false)
	})
	tw(t, fScheduleStore, "self-heals from a corrupt JSON file — load silently empties, next save rewrites", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.json")
		eq(t, os.WriteFile(file, []byte("{ this is not valid JSON"), 0o644), nil)
		store := newScheduleStore(file)
		eq(t, len(store.list()), 0)

		// The next mutation overwrites the broken file with healthy JSON.
		_ = store.add(makeJob(func(j *scheduledSubagent) { j.ID = "fresh" }))
		raw, _ := os.ReadFile(file)
		var data struct {
			Version int `json:"version"`
			Jobs    []struct {
				ID string `json:"id"`
			} `json:"jobs"`
		}
		eq(t, json.Unmarshal(raw, &data), nil)
		eq(t, data.Version, 1)
		eq(t, len(data.Jobs), 1)
		eq(t, data.Jobs[0].ID, "fresh")
	})
	tw(t, fScheduleStore, "recovers from a stale lock left by a dead process", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.json")
		lockFile := file + ".lock"
		// A stale lock file holding a non-existent PID (999999999 is virtually never a live process: kill -0 fails).
		eq(t, os.WriteFile(lockFile, []byte("999999999"), 0o644), nil)

		store := newScheduleStore(file)
		// The mutation detects the stale lock, removes it, and proceeds.
		eq(t, store.add(makeJob(nil)), nil)
		eq(t, len(store.list()), 1)
		eq(t, exists(lockFile), false)
	})
	tw(t, fScheduleStore, "releases the lock after a successful mutation so subsequent ones don't deadlock", func(t *testing.T) {
		store := newScheduleStore(filepath.Join(t.TempDir(), "s.json"))
		_ = store.add(makeJob(func(j *scheduledSubagent) { j.ID = "a" }))
		_ = store.add(makeJob(func(j *scheduledSubagent) { j.ID = "b" })) // would hang if the lock from the first add wasn't released
		var ids []string
		for _, j := range store.list() {
			ids = append(ids, j.ID)
		}
		eq(t, ids, []string{"a", "b"})
	})
	tw(t, fScheduleStore, "does not create the backing directory until a mutation persists", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".pi", "subagent-schedules")
		file := filepath.Join(dir, "sess.json")

		// Constructing and read-only use must not touch the filesystem.
		store := newScheduleStore(file)
		eq(t, len(store.list()), 0)
		eq(t, exists(dir), false)

		// The first mutation lazily creates the directory.
		_ = store.add(makeJob(nil))
		eq(t, exists(dir), true)
		eq(t, exists(file), true)
	})
	tw(t, fScheduleStore, "no-op update/remove of an unknown id never creates the backing directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".pi", "subagent-schedules")
		store := newScheduleStore(filepath.Join(dir, "sess.json"))

		_, ok, _ := store.update("nonexistent", func(j *scheduledSubagent) { j.Name = "x" })
		eq(t, ok, false)
		removed, _ := store.remove("nonexistent")
		eq(t, removed, false)
		eq(t, exists(dir), false)
	})
	tw(t, fScheduleStore, "deleteFileIfEmpty unlinks file only when no jobs remain", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.json")
		store := newScheduleStore(file)
		job := makeJob(nil)
		_ = store.add(job)
		store.deleteFileIfEmpty() // not empty: a no-op
		eq(t, exists(file), true)

		_, _ = store.remove(job.ID)
		store.deleteFileIfEmpty()
		eq(t, exists(file), false)
	})
}
