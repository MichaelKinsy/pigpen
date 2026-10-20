package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Twins of async-retention.test.ts.

const (
	dayMs  = int64(24 * 60 * 60 * 1000)
	nowRet = int64(1786838400000) // Date.UTC(2026, 7, 16)
	oldRet = nowRet - 31*dayMs
)

type retRoots struct{ root, asyncDirRoot, resultsDir, waitsDir string }

func makeRetRoots(t *testing.T) retRoots {
	t.Helper()
	root := tmp(t)
	r := retRoots{root, filepath.Join(root, "async-subagent-runs"), filepath.Join(root, "async-subagent-results"), filepath.Join(root, "wait-subscriptions")}
	for _, d := range []string{r.asyncDirRoot, r.resultsDir, r.waitsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

type missing struct{}

func utimes(t *testing.T, p string, ms int64) {
	t.Helper()
	ts := time.UnixMilli(ms)
	if err := os.Chtimes(p, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFile(t *testing.T, p string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, string(b))
}

func writeOldRun(t *testing.T, asyncDirRoot, runID string, overrides jm) string {
	t.Helper()
	runDir := filepath.Join(asyncDirRoot, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	status := jm{"runId": runID, "state": "complete", "mode": "single", "startedAt": oldRet - 1000, "endedAt": oldRet, "steps": []any{jm{"agent": "worker", "status": "complete"}}}
	for k, v := range overrides {
		if _, gone := v.(missing); gone {
			delete(status, k)
		} else {
			status[k] = v
		}
	}
	statusPath := filepath.Join(runDir, "status.json")
	writeJSONFile(t, statusPath, status)
	utimes(t, statusPath, oldRet)
	utimes(t, runDir, oldRet)
	return runDir
}

func writeOldResult(t *testing.T, resultsDir, runID string, overrides jm) string {
	t.Helper()
	p := filepath.Join(resultsDir, runID+".json")
	data := jm{"runId": runID, "sessionId": "session-a", "success": true, "state": "complete", "endedAt": oldRet}
	for k, v := range overrides {
		data[k] = v
	}
	writeJSONFile(t, p, data)
	utimes(t, p, oldRet)
	return p
}

func retOptions(r retRoots) AsyncRetentionOptions {
	return AsyncRetentionOptions{AsyncDirRoot: r.asyncDirRoot, ResultsDir: r.resultsDir, WaitSubscriptionDir: r.waitsDir, Now: func() int64 { return nowRet }, RandomID: func() string { return "fixed-tombstone" }}
}

func writeTestTombstoneMarker(t *testing.T, r retRoots, runID, tombstone string) string {
	t.Helper()
	dir := filepath.Join(r.root, "async-retention-run-tombstones")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, encodeIndexSegment(runID)+".json")
	writeJSONFile(t, p, jm{"version": 1, "runId": runID, "tombstonePath": tombstone, "createdAt": oldRet})
	return p
}

func mustCleanup(t *testing.T, o AsyncRetentionOptions) *AsyncRetentionResult {
	t.Helper()
	res, err := cleanupAsyncRetention(o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func countPrefix(names []string, prefix string) int {
	n := 0
	for _, s := range names {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

func countSuffix(names []string, suffix string) int {
	n := 0
	for _, s := range names {
		if strings.HasSuffix(s, suffix) {
			n++
		}
	}
	return n
}

func TestAsyncRetentionTwins(t *testing.T) {
	const f = "async-retention"
	tw(t, f, "repairs bounded dead running candidates before retention classification", func(t *testing.T) {
		r := makeRetRoots(t)
		runDir := writeOldRun(t, r.asyncDirRoot, "dead-running", jm{"state": "running", "endedAt": missing{}, "lastUpdate": oldRet, "pid": 12345, "sessionId": "session-a", "steps": []any{jm{"agent": "worker", "status": "running"}}})
		if err := os.MkdirAll(filepath.Join(r.asyncDirRoot, ".active-runs"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(r.asyncDirRoot, ".active-runs", "dead-running"), "")
		protectedDir := writeOldRun(t, r.asyncDirRoot, "protected-running", jm{"state": "running", "endedAt": missing{}, "lastUpdate": oldRet, "pid": 12346, "steps": []any{jm{"agent": "worker", "status": "running"}}})

		o := retOptions(r)
		o.ProtectedRunIDs = func() []string { return []string{"protected-running"} }
		o.ReconcileKill = func(pid, signal int) error { return errNoSuchProcess }
		repaired := mustCleanup(t, o)
		eq(t, repaired.RepairedRuns, 1, "repairedRuns")
		eq(t, repaired.DeletedRuns, 0, "deletedRuns")
		eq(t, repaired.Skipped["recent"], 1, "skipped recent")
		statusPath := filepath.Join(runDir, "status.json")
		var st map[string]any
		b, _ := os.ReadFile(statusPath)
		_ = json.Unmarshal(b, &st)
		eq(t, st["state"], "failed", "repaired state")
		utimes(t, statusPath, nowRet)
		utimes(t, runDir, nowRet)
		eq(t, exists(filepath.Join(r.asyncDirRoot, ".active-runs", "dead-running")), false, "active marker")
		eq(t, exists(filepath.Join(r.resultsDir, "dead-running.json")), true, "result file")
		b, _ = os.ReadFile(filepath.Join(protectedDir, "status.json"))
		st = nil
		_ = json.Unmarshal(b, &st)
		eq(t, st["state"], "running", "protected run state")
		eq(t, repaired.Skipped["runtime-reference"], 1, "runtime-reference")

		o2 := retOptions(r)
		o2.Now = func() int64 { return nowRet + 45*dayMs }
		retained := mustCleanup(t, o2)
		eq(t, retained.DeletedRuns, 1, "deletedRuns after retention")
		eq(t, exists(runDir), false, "repaired run removed")
	})

	tw(t, f, "deletes old proven-terminal runs and orphan results through tombstones", func(t *testing.T) {
		r := makeRetRoots(t)
		runDir := writeOldRun(t, r.asyncDirRoot, "old-run", nil)
		topLevelRoute := writeOldRun(t, r.asyncDirRoot, "top-level-route", jm{"isNested": false})
		nonResumable := writeOldRun(t, r.asyncDirRoot, "missing-session", nil)
		descriptorPath := filepath.Join(nonResumable, "recovery-descriptor.json")
		writeJSONFile(t, descriptorPath, jm{"sourceRunId": "missing-session", "sessionFile": filepath.Join(r.root, "missing.jsonl")})
		utimes(t, descriptorPath, oldRet)
		utimes(t, nonResumable, oldRet)
		resolvedHandoff := writeOldRun(t, r.asyncDirRoot, "resolved-handoff", nil)
		handoffPath := filepath.Join(resolvedHandoff, "handoff.json")
		writeJSONFile(t, handoffPath, jm{"version": 1, "groups": []any{jm{"cleanup": jm{"state": "complete"}}}})
		utimes(t, handoffPath, oldRet)
		utimes(t, resolvedHandoff, oldRet)
		resultPath := writeOldResult(t, r.resultsDir, "orphan-result", nil)

		res := mustCleanup(t, retOptions(r))
		eq(t, res.Acquired, true, "acquired")
		eq(t, res.DeletedRuns, 4, "deletedRuns")
		eq(t, res.DeletedResults, 1, "deletedResults")
		for _, p := range []string{runDir, topLevelRoute, nonResumable, resolvedHandoff, resultPath} {
			eq(t, exists(p), false, p)
		}
		eq(t, countPrefix(readDirNames(t, r.asyncDirRoot), ".deleting-run-"), 0, "run tombstones")
		eq(t, countPrefix(readDirNames(t, r.resultsDir), ".deleting-result-"), 0, "result tombstones")
		b, err := os.ReadFile(filepath.Join(r.root, "async-retention-maintenance.jsonl"))
		eq(t, err, nil, "log")
		var rec struct {
			DeletedRuns, DeletedResults, RawReads int
			Deleted                               []string
			SourceExhausted                       map[string]bool
			DurationMs                            float64
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &rec); err != nil {
			t.Fatal(err)
		}
		eq(t, [2]int{rec.DeletedRuns, rec.DeletedResults}, [2]int{4, 1}, "log counts")
		sort.Strings(rec.Deleted)
		eq(t, rec.Deleted, []string{"result:orphan-result", "run:missing-session", "run:old-run", "run:resolved-handoff", "run:top-level-route"}, "deleted ids")
		if rec.RawReads <= 0 {
			t.Error("rawReads")
		}
		if _, ok := rec.SourceExhausted["runs"]; !ok {
			t.Error("sourceExhausted.runs")
		}
	})

	tw(t, f, "fails closed for live, recent, resumable, wait, mission, workflow, handoff, and runtime references", func(t *testing.T) {
		r := makeRetRoots(t)
		writeOldRun(t, r.asyncDirRoot, "active", jm{"state": "running"})
		if err := os.MkdirAll(filepath.Join(r.asyncDirRoot, ".active-runs"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(r.asyncDirRoot, ".active-runs", "active"), "")
		writeOldRun(t, r.asyncDirRoot, "paused", jm{"state": "paused"})
		recent := writeOldRun(t, r.asyncDirRoot, "recent", jm{"endedAt": nowRet - dayMs})
		utimes(t, filepath.Join(recent, "status.json"), nowRet-dayMs)
		utimes(t, recent, nowRet-dayMs)
		resumable := writeOldRun(t, r.asyncDirRoot, "resumable", nil)
		sessionFile := filepath.Join(r.root, "resumable.jsonl")
		writeFile(t, sessionFile, "{}\n")
		writeJSONFile(t, filepath.Join(resumable, "recovery-descriptor.json"), jm{"sourceRunId": "resumable", "sessionFile": sessionFile})
		statusSession := filepath.Join(r.root, "status-session.jsonl")
		writeFile(t, statusSession, "{}\n")
		writeOldRun(t, r.asyncDirRoot, "status-session", jm{"sessionFile": statusSession})
		stepSession := filepath.Join(r.root, "step-session.jsonl")
		writeFile(t, stepSession, "{}\n")
		writeOldRun(t, r.asyncDirRoot, "step-session", jm{"steps": []any{jm{"agent": "worker", "status": "complete", "sessionFile": stepSession}}})
		writeOldRun(t, r.asyncDirRoot, "uninspectable-session", jm{"sessionFile": "bad\x00session.jsonl"})
		writeOldRun(t, r.asyncDirRoot, "malformed-ended-at", jm{"endedAt": "not-a-number"})
		writeOldRun(t, r.asyncDirRoot, "malformed-last-update", jm{"endedAt": missing{}, "lastUpdate": "not-a-number"})
		writeOldRun(t, r.asyncDirRoot, "missing-mode", jm{"mode": missing{}})
		nonFinite := writeOldRun(t, r.asyncDirRoot, "non-finite-ended-at", nil)
		nonFiniteStatus := filepath.Join(nonFinite, "status.json")
		writeFile(t, nonFiniteStatus, `{"runId":"non-finite-ended-at","state":"complete","mode":"single","startedAt":1,"endedAt":1e309,"steps":[{"agent":"worker","status":"complete"}]}`)
		utimes(t, nonFiniteStatus, oldRet)
		utimes(t, nonFinite, oldRet)
		mission := writeOldRun(t, r.asyncDirRoot, "mission", nil)
		writeFile(t, filepath.Join(mission, "mission.json"), "{}")
		writeOldRun(t, r.asyncDirRoot, "workflow", jm{"mode": "workflow"})
		writeOldRun(t, r.asyncDirRoot, "nested", jm{"isNested": true})
		writeOldRun(t, r.asyncDirRoot, "handoff", jm{"parallelHandoff": jm{"path": "/tmp/handoff.json"}})
		writeOldRun(t, r.asyncDirRoot, "waited", nil)
		writeJSONFile(t, filepath.Join(r.waitsDir, "wait.json"), jm{"runId": "waited", "expiresAt": nowRet + dayMs})
		writeOldRun(t, r.asyncDirRoot, "runtime", nil)

		o := retOptions(r)
		o.ProtectedRunIDs = func() []string { return []string{"runtime"} }
		res := mustCleanup(t, o)
		for _, id := range []string{"active", "paused", "recent", "resumable", "status-session", "step-session", "uninspectable-session", "malformed-ended-at", "malformed-last-update", "missing-mode", "non-finite-ended-at", "mission", "workflow", "nested", "handoff", "waited", "runtime"} {
			eq(t, exists(filepath.Join(r.asyncDirRoot, id)), true, id)
		}
		eq(t, res.DeletedRuns, 0, "deletedRuns")
		for reason, want := range map[string]int{"active-index": 1, "non-terminal": 1, "recent": 1, "resumable": 4, "invalid-status": 1, "unknown-age": 3, "mission-reference": 1, "workflow-reference": 1,
			"nested-reference": 1, "handoff-reference": 1, "wait-reference": 1, "runtime-reference": 1} {
			eq(t, res.Skipped[reason], want, "skipped "+reason)
		}
	})

	tw(t, f, "uses one cleaner, reaps stale tombstones safely, and limits processed runs", func(t *testing.T) {
		r := makeRetRoots(t)
		lock := filepath.Join(r.root, ".async-retention.lock")
		if err := os.Mkdir(lock, 0o755); err != nil {
			t.Fatal(err)
		}
		locked := mustCleanup(t, retOptions(r))
		eq(t, locked.Acquired, false, "locked acquired")
		eq(t, locked.Skipped["lock-busy"], 1, "lock-busy")
		_ = os.RemoveAll(lock)
		_ = os.Mkdir(lock, 0o755)
		writeJSONFile(t, filepath.Join(lock, "owner.json"), jm{"pid": os.Getpid(), "startedAt": nowRet - 6*60*1000})
		live := mustCleanup(t, retOptions(r))
		eq(t, live.Acquired, false, "live lock acquired")
		eq(t, live.Skipped["lock-busy"], 1, "live lock-busy")
		_ = os.RemoveAll(lock)
		_ = os.Mkdir(lock, 0o755)
		host := func(o AsyncRetentionOptions) AsyncRetentionOptions {
			o.Hostname, o.ProcessStartIdentity = "test-host", "current-process"
			o.GetProcessStartIdentity = func(int) string { return "current-process" }
			return o
		}
		writeJSONFile(t, filepath.Join(lock, "owner.json"), jm{"version": 1, "token": "old-token", "pid": os.Getpid(), "hostname": "test-host", "processStartIdentity": "old-process", "startedAt": nowRet - 60_000})
		reused := mustCleanup(t, host(retOptions(r)))
		eq(t, reused.Acquired, true, "reused pid lock acquired")
		_ = os.Mkdir(lock, 0o755)
		writeJSONFile(t, filepath.Join(lock, "owner.json"), jm{"version": 1, "token": "other-token", "pid": os.Getpid(), "hostname": "test-host", "processStartIdentity": "current-process", "startedAt": nowRet - 60_000})
		prot := mustCleanup(t, host(retOptions(r)))
		eq(t, prot.Acquired, false, "protected lock acquired")
		owner := readLooseJSON(filepath.Join(lock, "owner.json"))
		eq(t, owner["token"], "other-token", "protected owner")
		_ = os.RemoveAll(lock)
		replacement := jm{"version": 1, "token": "successor-token", "pid": os.Getpid(), "hostname": "test-host", "processStartIdentity": "current-process", "startedAt": nowRet}
		cursorPath := filepath.Join(r.root, ".async-retention-cursor.json")
		before, _ := os.ReadFile(cursorPath)
		o := host(retOptions(r))
		o.RandomID = func() string { return "owned-token" }
		o.ProtectedRunIDs = func() []string { writeJSONFile(t, filepath.Join(lock, "owner.json"), replacement); return nil }
		changed := mustCleanup(t, o)
		eq(t, changed.Acquired, true, "changed owner acquired")
		eq(t, changed.Skipped["lock-owner-changed"], 1, "lock-owner-changed")
		after, _ := os.ReadFile(cursorPath)
		eq(t, string(after), string(before), "cursor untouched")
		eq(t, readLooseJSON(filepath.Join(lock, "owner.json"))["token"], "successor-token", "successor owner")
		_ = os.RemoveAll(lock)
		_ = os.Mkdir(lock, 0o755)
		writeJSONFile(t, filepath.Join(lock, "owner.json"), jm{"startedAt": nowRet - 25*60*60*1000})
		utimes(t, lock, nowRet-25*60*60*1000)
		stale := mustCleanup(t, retOptions(r))
		eq(t, stale.Acquired, true, "stale lock acquired")

		tombstone := writeOldRun(t, r.asyncDirRoot, ".deleting-run-stale", jm{"runId": "stale-run"})
		writeTestTombstoneMarker(t, r, "stale-run", tombstone)
		for i := 0; i < 110; i++ {
			writeOldRun(t, r.asyncDirRoot, fmt.Sprintf("old-%03d", i), nil)
		}
		o2 := retOptions(r)
		o2.RandomID = func() string { return fmt.Sprintf("delete-%v", rand.Float64()) }
		res := mustCleanup(t, o2)
		if res.Scanned > asyncRetentionBatchSize {
			t.Errorf("scanned %d", res.Scanned)
		}
		eq(t, exists(tombstone), false, "stale tombstone")
		eq(t, res.ReapedTombstones, 1, "reapedTombstones")
		if n := countPrefix(readDirNames(t, r.asyncDirRoot), "old-"); n < 60 {
			t.Errorf("only %d old runs left", n)
		}
	})

	tw(t, f, "does not reclaim a live replacement owner after a reused pid identity changes", func(t *testing.T) {
		r := makeRetRoots(t)
		reusedPid := os.Getpid() + 100_000
		lock := filepath.Join(r.root, ".async-retention.lock")
		ownerPath := filepath.Join(lock, "owner.json")
		replacement := jm{"version": 1, "token": "replacement-token", "pid": reusedPid, "hostname": "test-host", "processStartIdentity": "replacement-process", "startedAt": nowRet}
		_ = os.Mkdir(lock, 0o755)
		writeJSONFile(t, ownerPath, jm{"version": 1, "token": "old-token", "pid": reusedPid, "hostname": "test-host", "processStartIdentity": "old-process", "startedAt": nowRet})
		currentIdentity := "old-process"
		o := retOptions(r)
		o.Hostname, o.ProcessStartIdentity = "test-host", "cleaner-process"
		o.IsProcessAlive = func(pid int) *bool { t := pid == reusedPid || pid == os.Getpid(); return &t }
		o.GetProcessStartIdentity = func(pid int) string {
			if pid == reusedPid {
				return currentIdentity
			}
			return "cleaner-process"
		}
		first := mustCleanup(t, o)
		eq(t, first.Acquired, false, "first acquired")
		eq(t, first.Skipped["lock-busy"], 1, "first lock-busy")
		writeJSONFile(t, ownerPath, replacement)
		currentIdentity = "replacement-process"
		second := mustCleanup(t, o)
		eq(t, second.Acquired, false, "second acquired")
		eq(t, second.Skipped["lock-busy"], 1, "second lock-busy")
		var got jm
		b, _ := os.ReadFile(ownerPath)
		_ = json.Unmarshal(b, &got)
		want := jm{}
		wb, _ := json.Marshal(replacement)
		_ = json.Unmarshal(wb, &want)
		eq(t, reflect.DeepEqual(got, want), true, "owner untouched")
	})

	tw(t, f, "limits result candidates to the cleanup batch", func(t *testing.T) {
		r := makeRetRoots(t)
		for i := 0; i < 90; i++ {
			writeOldResult(t, r.resultsDir, fmt.Sprintf("recent-result-%03d", i), jm{"endedAt": nowRet - dayMs})
		}
		stale := writeOldResult(t, r.resultsDir, "zz-stale-result", nil)
		res := mustCleanup(t, retOptions(r))
		for pass := 0; pass < 10 && exists(stale); pass++ {
			res = mustCleanup(t, retOptions(r))
		}
		eq(t, exists(stale), false, "stale result")
		if res.Scanned > asyncRetentionBatchSize {
			t.Errorf("scanned %d", res.Scanned)
		}
		if n := countSuffix(readDirNames(t, r.resultsDir), ".json"); n < 60 {
			t.Errorf("only %d results left", n)
		}
	})

	tw(t, f, "keeps replay cleanup fair when pending results fill their source budget", func(t *testing.T) {
		r := makeRetRoots(t)
		pendingRoot := filepath.Join(r.resultsDir, "result-pending")
		for s := 0; s < 13; s++ {
			sessionID := fmt.Sprintf("session-%03d", s)
			dir := filepath.Join(pendingRoot, sessionID)
			_ = os.MkdirAll(dir, 0o755)
			for i := 0; i < 13; i++ {
				runID := fmt.Sprintf("recent-%d-%d", s, i)
				file := filepath.Join(dir, runID+".json")
				writeJSONFile(t, file, jm{"runId": runID, "sessionId": sessionID, "success": true, "state": "complete", "endedAt": nowRet - dayMs})
				utimes(t, file, nowRet-dayMs)
			}
		}
		replayDir := filepath.Join(r.resultsDir, "completion-replay")
		_ = os.MkdirAll(replayDir, 0o755)
		replay := filepath.Join(replayDir, "stale-replay.json")
		writeJSONFile(t, replay, jm{"version": 1, "runId": "stale-replay", "createdAt": oldRet, "expiresAt": oldRet, "completion": jm{"runId": "stale-replay", "mode": "single"}})
		utimes(t, replay, oldRet)
		res := mustCleanup(t, retOptions(r))
		eq(t, exists(replay), false, "stale replay")
		eq(t, res.DeletedResults, 1, "deletedResults")
		if res.Scanned > asyncRetentionBatchSize {
			t.Errorf("scanned %d", res.Scanned)
		}
	})

	tw(t, f, "uses full delayed discovery so retained raw-order prefixes cannot starve stale runs", func(t *testing.T) {
		r := makeRetRoots(t)
		for _, id := range []string{"recent-a", "recent-b", "recent-c", "recent-d"} {
			writeOldRun(t, r.asyncDirRoot, id, jm{"endedAt": nowRet - dayMs})
		}
		victim := writeOldRun(t, r.asyncDirRoot, "zz-old-victim", nil)
		four := 4
		o := retOptions(r)
		o.BatchSize = &four
		res := mustCleanup(t, o)
		for pass := 0; pass < 5 && exists(victim); pass++ {
			if res.Scanned > asyncRetentionBatchSize {
				t.Errorf("scanned %d", res.Scanned)
			}
			if res.RawReads < 5 {
				t.Errorf("rawReads %d", res.RawReads)
			}
			res = mustCleanup(t, o)
		}
		eq(t, exists(victim), false, "victim")
	})

	tw(t, f, "uses the same ordering for mixed-case cursor filtering and selection", func(t *testing.T) {
		r := makeRetRoots(t)
		writeOldRun(t, r.asyncDirRoot, "a", jm{"endedAt": nowRet - dayMs})
		victim := writeOldRun(t, r.asyncDirRoot, "B", nil)
		one := 1
		o := retOptions(r)
		o.BatchSize = &one
		mustCleanup(t, o)
		for pass := 0; pass < 3 && exists(victim); pass++ {
			mustCleanup(t, o)
		}
		eq(t, exists(victim), false, "victim")
	})

	tw(t, f, "wraps a cursor and makes progress when no usable entries are after it", func(t *testing.T) {
		r := makeRetRoots(t)
		runDir := writeOldRun(t, r.asyncDirRoot, "cursor-wrap", nil)
		writeJSONFile(t, filepath.Join(r.root, ".async-retention-cursor.json"), jm{"version": 1, "runAfter": "zzzz"})
		one := 1
		o := retOptions(r)
		o.BatchSize = &one
		wrapped := mustCleanup(t, o)
		eq(t, exists(runDir), false, "run removed")
		eq(t, wrapped.RawReads, 1, "rawReads")
		eq(t, wrapped.DeletedRuns, 1, "deletedRuns")
	})

	tw(t, f, "uses exact run tombstone markers for result blocking", func(t *testing.T) {
		r := makeRetRoots(t)
		blocked := writeOldResult(t, r.resultsDir, "blocked-result", nil)
		free := writeOldResult(t, r.resultsDir, "free-result", nil)
		blocking := filepath.Join(r.root, "blocking-tombstone")
		_ = os.Mkdir(blocking, 0o755)
		writeTestTombstoneMarker(t, r, "blocked-result", blocking)
		res := mustCleanup(t, retOptions(r))
		eq(t, exists(blocked), true, "blocked result")
		eq(t, exists(free), false, "free result")
		eq(t, res.Skipped["run-tombstone-present"], 1, "run-tombstone-present")
	})

	tw(t, f, "removes a stray run tombstone marker when its tombstone is gone", func(t *testing.T) {
		r := makeRetRoots(t)
		result := writeOldResult(t, r.resultsDir, "stray-marker-result", nil)
		marker := writeTestTombstoneMarker(t, r, "stray-marker-result", filepath.Join(r.root, "missing-tombstone"))
		res := mustCleanup(t, retOptions(r))
		eq(t, exists(result), false, "result")
		eq(t, exists(marker), false, "marker")
		eq(t, res.DeletedResults, 1, "deletedResults")
	})

	writePending := func(t *testing.T, r retRoots, sessionID, runID string, endedAt int64) string {
		dir := filepath.Join(r.resultsDir, "result-pending", sessionID)
		_ = os.MkdirAll(dir, 0o755)
		file := filepath.Join(dir, runID+".json")
		writeJSONFile(t, file, jm{"runId": runID, "sessionId": sessionID, "success": true, "state": "complete", "endedAt": endedAt})
		utimes(t, file, endedAt)
		return file
	}
	tw(t, f, "advances pending result session windows beyond the first batch", func(t *testing.T) {
		r := makeRetRoots(t)
		for i := 0; i < 60; i++ {
			writePending(t, r, fmt.Sprintf("session-%03d", i), fmt.Sprintf("recent-%d", i), nowRet-dayMs)
		}
		stale := writePending(t, r, "session-zz", "stale-pending", oldRet)
		res := mustCleanup(t, retOptions(r))
		for pass := 0; pass < 5 && exists(stale); pass++ {
			res = mustCleanup(t, retOptions(r))
		}
		eq(t, exists(stale), false, "stale pending")
		if res.Scanned > asyncRetentionBatchSize {
			t.Errorf("scanned %d", res.Scanned)
		}
	})

	tw(t, f, "keeps pending result file cursors separate per session", func(t *testing.T) {
		r := makeRetRoots(t)
		for i := 0; i < 6; i++ {
			writePending(t, r, "session-a", fmt.Sprintf("recent-%02d", i), nowRet-dayMs)
		}
		stale := writePending(t, r, "session-a", "zz-stale", oldRet)
		writePending(t, r, "session-b", "recent-b", nowRet-dayMs)
		eight := 8
		o := retOptions(r)
		o.BatchSize = &eight
		res := mustCleanup(t, o)
		for pass := 0; pass < 20 && exists(stale); pass++ {
			if res.Scanned > asyncRetentionBatchSize {
				t.Errorf("scanned %d", res.Scanned)
			}
			res = mustCleanup(t, o)
		}
		eq(t, exists(stale), false, "stale pending")
	})

	tw(t, f, "prunes pending result cursors for removed session directories", func(t *testing.T) {
		r := makeRetRoots(t)
		live := filepath.Join(r.resultsDir, "result-pending", "live-session")
		_ = os.MkdirAll(live, 0o755)
		file := filepath.Join(live, "zz-live.json")
		writeJSONFile(t, file, jm{"runId": "zz-live", "sessionId": "live-session", "success": true, "state": "complete", "endedAt": nowRet - dayMs})
		utimes(t, file, nowRet-dayMs)
		writeJSONFile(t, filepath.Join(r.root, ".async-retention-cursor.json"), jm{"version": 1, "resultPendingAfterBySession": jm{
			"live-session": "result-pending/live-session/live.json", "removed-session": "result-pending/removed-session/stale.json"}})
		mustCleanup(t, retOptions(r))
		cursor := readLooseJSON(filepath.Join(r.root, ".async-retention-cursor.json"))
		pending, _ := cursor["resultPendingAfterBySession"].(map[string]any)
		if _, ok := pending["removed-session"]; ok {
			t.Error("removed-session cursor kept")
		}
		eq(t, pending["live-session"], filepath.Join("result-pending", "live-session", "zz-live.json"), "live-session cursor")
	})

	tw(t, f, "keeps orphan results with recoverability or durable references", func(t *testing.T) {
		r := makeRetRoots(t)
		writeOldResult(t, r.resultsDir, "waited-result", nil)
		writeJSONFile(t, filepath.Join(r.waitsDir, "wait.json"), jm{"runId": "waited-result", "expiresAt": nowRet + dayMs})
		writeOldResult(t, r.resultsDir, "workflow-result", jm{"mode": "workflow"})
		sessionFile := filepath.Join(r.root, "result-session.jsonl")
		writeFile(t, sessionFile, "{}\n")
		writeOldResult(t, r.resultsDir, "resumable-result", jm{"sessionFile": sessionFile})
		writeOldResult(t, r.resultsDir, "mission-result", nil)
		observerDir := filepath.Join(r.resultsDir, "result-index", "observers", "mission")
		_ = os.MkdirAll(observerDir, 0o755)
		writeFile(t, filepath.Join(observerDir, "mission-result.json"), "{}")
		recent := writeOldResult(t, r.resultsDir, "recent-result", jm{"endedAt": nowRet - dayMs})
		utimes(t, recent, nowRet-dayMs)
		raw := func(name, body string) {
			p := filepath.Join(r.resultsDir, name+".json")
			writeFile(t, p, body)
			utimes(t, p, oldRet)
		}
		raw("malformed-age-result", `{"runId":"malformed-age-result","success":true,"state":"complete","endedAt":"not-a-number"}`)
		raw("non-finite-age-result", `{"runId":"non-finite-age-result","success":true,"state":"complete","endedAt":1e309}`)
		raw("non-finite-timestamp-result", `{"runId":"non-finite-timestamp-result","success":true,"state":"complete","timestamp":1e309}`)
		writeOldResult(t, r.resultsDir, "uninspectable-session-result", jm{"sessionFile": "bad\x00session.jsonl"})
		writeOldResult(t, r.resultsDir, "runtime-result", nil)
		replayDir, archiveDir := filepath.Join(r.resultsDir, "completion-replay"), filepath.Join(r.resultsDir, "output-archives")
		_ = os.MkdirAll(replayDir, 0o755)
		_ = os.MkdirAll(archiveDir, 0o755)
		replay, archive := filepath.Join(replayDir, "workflow-replay.json"), filepath.Join(archiveDir, "workflow-replay.json")
		writeJSONFile(t, replay, jm{"version": 1, "runId": "workflow-replay", "createdAt": oldRet, "expiresAt": oldRet, "completion": jm{"runId": "workflow-replay", "mode": "workflow"}})
		writeJSONFile(t, archive, jm{"version": 1, "runId": "workflow-replay", "createdAt": oldRet, "entries": []any{}})
		utimes(t, replay, oldRet)
		utimes(t, archive, oldRet)
		o := retOptions(r)
		o.ProtectedRunIDs = func() []string { return []string{"runtime-result"} }
		res := mustCleanup(t, o)
		for _, id := range []string{"waited-result", "workflow-result", "resumable-result", "mission-result", "recent-result", "malformed-age-result", "non-finite-age-result", "non-finite-timestamp-result", "uninspectable-session-result", "runtime-result"} {
			eq(t, exists(filepath.Join(r.resultsDir, id+".json")), true, id)
		}
		eq(t, exists(replay), true, "replay")
		eq(t, exists(archive), true, "archive")
		eq(t, res.DeletedResults, 0, "deletedResults")
		for reason, want := range map[string]int{"wait-reference": 1, "workflow-reference": 2, "replay-reference": 1, "resumable": 2, "mission-reference": 1, "recent": 1, "unknown-age": 3, "runtime-reference": 1} {
			eq(t, res.Skipped[reason], want, "skipped "+reason)
		}
	})

	tw(t, f, "keeps the cursor unchanged for failed, malformed, and cancelled workers", func(t *testing.T) {
		r := makeRetRoots(t)
		cursorPath := filepath.Join(r.root, ".async-retention-cursor.json")
		cursorBytes := "{\"version\":1,\"runAfter\":\"keep-me\"}\n"
		writeFile(t, cursorPath, cursorBytes)
		unchanged := func(what string) {
			b, _ := os.ReadFile(cursorPath)
			eq(t, string(b), cursorBytes, what)
		}
		malformed := retOptions(r)
		malformed.Discover = func(RetentionDiscoveryRequest) (*RetentionDiscovery, error) {
			return &RetentionDiscovery{RunCandidates: []RetentionRunCandidate{{Name: "../escape", Relative: "../escape"}}}, nil
		}
		m := mustCleanup(t, malformed)
		eq(t, m.WorkerFailed, true, "malformed workerFailed")
		eq(t, m.Skipped["worker-failure"], 1, "malformed worker-failure")
		unchanged("malformed")
		failing := retOptions(r)
		failing.Discover = func(RetentionDiscoveryRequest) (*RetentionDiscovery, error) { return nil, errors.New("worker crash") }
		fres := mustCleanup(t, failing)
		eq(t, fres.WorkerFailed, true, "failed workerFailed")
		if len(fres.Errors) == 0 || !strings.Contains(fres.Errors[0], "worker crash") {
			t.Errorf("errors %v", fres.Errors)
		}
		unchanged("failed")
		cancel := make(chan struct{})
		hang := make(chan struct{})
		t.Cleanup(func() { close(hang) })
		hanging := retOptions(r)
		hanging.Cancel = cancel
		hanging.Discover = func(RetentionDiscoveryRequest) (*RetentionDiscovery, error) {
			<-hang
			return nil, errors.New("abandoned")
		}
		var cancelled *AsyncRetentionResult
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); cancelled = mustCleanup(t, hanging) }()
		time.Sleep(20 * time.Millisecond)
		close(cancel)
		wg.Wait()
		eq(t, cancelled.Cancelled, true, "cancelled")
		eq(t, cancelled.Skipped["cancelled"], 1, "skipped cancelled")
		unchanged("cancelled")
		eq(t, exists(filepath.Join(r.root, ".async-retention.lock")), false, "lock released")
	})

	tw(t, f, "advances past a candidate with a read error when no mutation started", func(t *testing.T) {
		r := makeRetRoots(t)
		unreadable := writeOldRun(t, r.asyncDirRoot, "aaa-unreadable", nil)
		victim := writeOldRun(t, r.asyncDirRoot, "zzz-victim", nil)
		two := 2
		o := retOptions(r)
		o.BatchSize = &two
		o.Lstat = func(p string) (fs.FileInfo, error) {
			if abs, _ := filepath.Abs(p); abs == func() string { a, _ := filepath.Abs(unreadable); return a }() {
				return nil, &fs.PathError{Op: "lstat", Path: p, Err: syscall.EIO}
			}
			return os.Lstat(p)
		}
		first := mustCleanup(t, o)
		eq(t, len(first.Errors), 1, "errors")
		if !strings.Contains(first.Errors[0], "input/output error") && !strings.Contains(first.Errors[0], "EIO") {
			t.Errorf("error %q", first.Errors[0])
		}
		eq(t, first.Skipped["commit-failure"], 0, "commit-failure")
		eq(t, exists(victim), true, "victim after the first pass")
		second := mustCleanup(t, o)
		eq(t, second.DeletedRuns, 1, "deletedRuns")
		eq(t, exists(victim), false, "victim after the second pass")
	})

	tw(t, f, "yields the extension event loop during full directory discovery", func(t *testing.T) {
		r := makeRetRoots(t)
		for i := 0; i < 2000; i++ {
			if err := os.Mkdir(filepath.Join(r.asyncDirRoot, fmt.Sprintf("run-%d", i)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		// the pass runs its discovery off the calling goroutine: another goroutine runs while it is in progress
		var mu sync.Mutex
		yielded := false
		started := make(chan struct{})
		o := retOptions(r)
		two := 2
		o.BatchSize = &two
		o.Discover = func(req RetentionDiscoveryRequest) (*RetentionDiscovery, error) {
			close(started)
			mu.Lock()
			defer mu.Unlock()
			return discoverRetention(req)
		}
		go func() { <-started; mu.Lock(); yielded = true; mu.Unlock() }()
		res := mustCleanup(t, o)
		mu.Lock()
		eq(t, yielded, true, "yielded")
		mu.Unlock()
		if res.RawReads < 2000 {
			t.Errorf("rawReads %d", res.RawReads)
		}
		eq(t, res.SourceExhausted["runs"], true, "sourceExhausted.runs")
	})
}
