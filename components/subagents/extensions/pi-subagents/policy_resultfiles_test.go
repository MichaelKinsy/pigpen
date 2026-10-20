package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twins of test/unit/result-files.test.ts against the result files of the background runs (async_results.go). The original's
// tests make node:fs fail by patching it; here the same failures come through the result file operation variables.

func seam[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func rfStrs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func rfPendingPath(resultsDir, sessionID, runID string) string {
	return filepath.Join(resultsDir, "result-pending", encodeIndexSegment(sessionID), encodeIndexSegmentMax(runID, maxJSONFileStemBytes)+".json")
}

func rfPayload(runID, sessionID string, success bool, extra ...any) map[string]any {
	m := map[string]any{"id": runID, "runId": runID, "success": success}
	if sessionID != "" {
		m["sessionId"] = sessionID
	}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

func rfWrite(t *testing.T, resultPath string, data map[string]any) string {
	t.Helper()
	state, err := writeAsyncResultFile(resultPath, data)
	if err != nil {
		t.Fatalf("writeAsyncResultFile: %v", err)
	}
	return state
}

func rfSuccess(t *testing.T, path string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m["success"] == true
}

func rfExists(p string) bool { _, err := os.Stat(p); return err == nil }

func rfFirstEntry(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no entries in %s: %v", dir, err)
	}
	return entries[0].Name()
}

func rfCopy(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

const rfWinSession = `C:\Users\theap\.pi\agent\sessions\leaf.jsonl`

func TestResultFileIndexes(t *testing.T) {
	const f = "result-files"
	tw(t, f, "resolves run ids without enumerating session indexes", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "direct-run.json")
		rfWrite(t, resultPath, rfPayload("direct-run", "session-a", true))
		seam(t, &resultReadDir, func(string) ([]os.DirEntry, error) {
			t.Error("result index enumerated")
			return nil, os.ErrPermission
		})
		equal(t, resultPayloadPathForIndexedRun(dir, "direct-run"), resultPath, "indexed run")
		equal(t, resultPayloadPathForIndexedRun(dir, "missing-run"), "", "missing run")
	})

	tw(t, f, "removes orphan index entries without deleting flat result files", func(t *testing.T) {
		dir := t.TempDir()
		rfWrite(t, filepath.Join(dir, "kept.json"), rfPayload("kept", "session-a", true))
		rfWrite(t, filepath.Join(dir, "missing.json"), rfPayload("missing", "session-a", true, "toolCallId", "call-missing"))
		if err := os.Remove(filepath.Join(dir, "missing.json")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "unindexed.json"), `{"id":"unindexed","sessionId":"session-a"}`)

		removed := cleanupResultIndexes(dir, time.Now().UnixMilli()+86_400_001, 86_400_000)
		equal(t, removed > 0, true, "removed some")

		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{"kept.json"}, "session files")
		equal(t, rfExists(filepath.Join(dir, "kept.json")), true, "kept")
		equal(t, rfExists(filepath.Join(dir, "unindexed.json")), true, "unindexed")
	})

	tw(t, f, "promotes an indexed pending result payload", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "late.json")
		if err := os.MkdirAll(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}
		equal(t, rfWrite(t, resultPath, rfPayload("late", "session-a", true)), "pending", "state")
		equal(t, rfExists(rfPendingPath(dir, "session-a", "late")), true, "pending exists")
		if err := os.RemoveAll(resultPath); err != nil {
			t.Fatal(err)
		}

		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{"late.json"}, "session files")
		equal(t, rfSuccess(t, resultPath), true, "promoted payload")
		equal(t, rfExists(rfPendingPath(dir, "session-a", "late")), false, "pending gone")
	})

	tw(t, f, "writes an indexed pending result without publishing it", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "pending-only.json")
		if err := os.MkdirAll(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writePendingAsyncResultFile(resultPath, rfPayload("pending-only", "session-a", true)); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(resultPath)
		equal(t, st.IsDir(), true, "still a directory")
		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{}, "no public files")
		equal(t, rfStrs(resultCandidateFilesForSession(dir, "session-a")), []string{"pending-only.json"}, "candidates")
		pending := rfPendingPath(dir, "session-a", "pending-only")
		got, err := resultPayloadPathForSessionRun(dir, "session-a", "pending-only")
		equal(t, err, nil, "error")
		equal(t, got, pending, "session run payload")
		equal(t, resultPayloadPathForIndexedRun(dir, "pending-only"), pending, "indexed run payload")
	})

	tw(t, f, "keeps pending results for long session ids below filesystem component limits", func(t *testing.T) {
		dir := t.TempDir()
		sessionID := "/Users/zhouatie/.config/pi/sessions/" + strings.Repeat("界", 100) + ".jsonl"
		runID := "pending-long-session"
		resultPath := filepath.Join(dir, runID+".json")
		if err := os.Mkdir(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writePendingAsyncResultFile(resultPath, rfPayload(runID, sessionID, true)); err != nil {
			t.Fatal(err)
		}
		payload := rfPendingPath(dir, sessionID, runID)
		equal(t, len(filepath.Base(filepath.Dir(payload))) <= 255, true, "directory name bytes")
		equal(t, rfExists(payload), true, "payload exists")
		equal(t, rfStrs(resultCandidateFilesForSession(dir, sessionID)), []string{runID + ".json"}, "candidates")
		got, _ := resultPayloadPathForSessionRun(dir, sessionID, runID)
		equal(t, got, payload, "session run payload")
		if err := os.RemoveAll(filepath.Join(dir, "result-index")); err != nil {
			t.Fatal(err)
		}
		equal(t, resultPayloadPathForIndexedRun(dir, runID), "", "no indexed run")
	})

	tw(t, f, "keeps pending results for near-limit run ids below filesystem component limits", func(t *testing.T) {
		dir := t.TempDir()
		sessionID := "session-a"
		for _, runID := range []string{strings.Repeat("x", 250), strings.Repeat("y", 251)} {
			resultPath := filepath.Join(dir, runID+".json")
			if len(runID) == 250 {
				if err := os.Mkdir(resultPath, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := writePendingAsyncResultFile(resultPath, rfPayload(runID, sessionID, true)); err != nil {
				t.Fatal(err)
			}
			payload := rfPendingPath(dir, sessionID, runID)
			equal(t, len(filepath.Base(payload)) <= 255, true, "file name bytes")
			equal(t, rfExists(payload), true, "payload exists")
			got, _ := resultPayloadPathForSessionRun(dir, sessionID, runID)
			equal(t, got, payload, "session run payload")
		}
		equal(t, filepath.Base(rfPendingPath(dir, sessionID, strings.Repeat("x", 250))), strings.Repeat("x", 250)+".json", "short name kept")
		polMatches(t, filepath.Base(rfPendingPath(dir, sessionID, strings.Repeat("y", 251))), `^~sha256-[a-f0-9]{64}\.json$`)
	})

	tw(t, f, "keeps a legacy valid index while the result payload is not visible yet", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "late.json")
		if err := writeResultIndexForData(resultPath, rfPayload("late", "session-a", true)); err != nil {
			t.Fatal(err)
		}
		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{}, "before the payload")
		writeFile(t, resultPath, `{"id":"late","runId":"late","sessionId":"session-a","success":true}`)
		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{"late.json"}, "after the payload")
	})

	tw(t, f, "keeps an unindexed pending result recoverable when its session index cannot be written", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "result-index"), "not a directory")
		resultPath := filepath.Join(dir, "blocked.json")

		if _, err := writeAsyncResultFile(resultPath, rfPayload("blocked", "session-a", true)); err == nil {
			t.Error("the write should fail")
		}
		equal(t, rfExists(resultPath), false, "no public payload")
		pending := rfPendingPath(dir, "session-a", "blocked")
		equal(t, rfExists(pending), true, "pending exists")
		got, _ := resultPayloadPathForSessionRun(dir, "session-a", "blocked")
		equal(t, got, pending, "session run payload")
		equal(t, resultPayloadPathForIndexedRun(dir, "blocked"), "", "no indexed run")
		equal(t, rfStrs(resultCandidateFilesForSession(dir, "session-a")), []string{"blocked.json"}, "candidates")
	})

	tw(t, f, "treats a missing mission observer index directory as absent without logging", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "result-index"), "not a directory")
		equal(t, resultPayloadPathForMissionObserverRun(dir, "mission-run"), "", "absent")
	})

	tw(t, f, "does not commit a result payload without a session id", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "blocked.json")
		_, err := writeAsyncResultFile(resultPath, map[string]any{"id": "blocked", "runId": "blocked", "success": true})
		if err == nil {
			t.Fatal("the write should fail")
		}
		polMatches(t, err.Error(), `sessionId`)
		equal(t, rfExists(resultPath), false, "no public payload")
	})

	tw(t, f, "prefers pending payload over an older public result", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "blocked.json")
		rfWrite(t, resultPath, rfPayload("blocked", "session-a", false))
		if err := os.Remove(resultPath); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}

		equal(t, rfWrite(t, resultPath, rfPayload("blocked", "session-a", true)), "pending", "state")
		if err := os.RemoveAll(resultPath); err != nil {
			t.Fatal(err)
		}
		writeFile(t, resultPath, `{"id":"blocked","runId":"blocked","sessionId":"session-a","success":false}`)

		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{"blocked.json"}, "session files")
		equal(t, rfSuccess(t, resultPath), true, "the pending payload won")
	})

	tw(t, f, "keeps a newer pending payload readable when Windows cannot replace the public result", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "blocked.json")
		rfWrite(t, resultPath, rfPayload("blocked", "session-a", false))
		if err := writePendingAsyncResultFile(resultPath, rfPayload("blocked", "session-a", true)); err != nil {
			t.Fatal(err)
		}
		seam(t, &resultRename, func(string, string) error { return newFSError("EEXIST", "destination exists") })

		pending := rfPendingPath(dir, "session-a", "blocked")
		payload, err := resultPayloadPathForSessionRun(dir, "session-a", "blocked")
		equal(t, err, nil, "error")
		equal(t, payload, pending, "payload path")
		equal(t, rfSuccess(t, payload), true, "pending payload")
		equal(t, rfSuccess(t, resultPath), false, "public result")
		equal(t, rfExists(pending), true, "pending kept")
	})

	tw(t, f, "removes pending payloads with result indexes", func(t *testing.T) {
		dir := t.TempDir()
		resultPath := filepath.Join(dir, "pending-cleanup.json")
		if err := os.MkdirAll(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}
		rfWrite(t, resultPath, rfPayload("pending-cleanup", "session-a", true))

		pending := rfPendingPath(dir, "session-a", "pending-cleanup")
		equal(t, rfExists(pending), true, "pending exists")
		removeResultIndex(dir, "session-a", "pending-cleanup", "")
		equal(t, rfExists(pending), false, "pending removed")
	})

	tw(t, f, "indexes results with oversized provider tool-call ids", func(t *testing.T) {
		dir := t.TempDir()
		runID := "workflow-long-tool-call"
		toolCallID := "call_" + strings.Repeat("opaque/+=", 80)
		resultPath := filepath.Join(dir, runID+".json")

		rfWrite(t, resultPath, rfPayload(runID, "session-a", true, "toolCallId", toolCallID))

		equal(t, rfStrs(resultFilesForToolCall(dir, toolCallID)), []string{runID + ".json"}, "tool call files")
		removeResultIndex(dir, "session-a", runID, toolCallID)
		equal(t, rfStrs(resultFilesForToolCall(dir, toolCallID)), []string{}, "after removal")
		equal(t, rfExists(resultPath), true, "payload kept")
	})

	tw(t, f, "commits a result payload when only an optional index fails", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "result-index"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "result-index", "tool-calls"), "not a directory")
		resultPath := filepath.Join(dir, "kept.json")

		rfWrite(t, resultPath, rfPayload("kept", "session-a", true, "toolCallId", "call-a"))

		equal(t, rfExists(resultPath), true, "payload committed")
		equal(t, rfStrs(resultFilesForSession(dir, "session-a")), []string{"kept.json"}, "session files")
	})

	tw(t, f, "round-trips results for Windows session file paths", func(t *testing.T) {
		dir := t.TempDir()
		runID := "win-session-run"
		rfWrite(t, filepath.Join(dir, runID+".json"), rfPayload(runID, rfWinSession, true))

		entries, err := os.ReadDir(filepath.Join(dir, "result-index", "sessions"))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, len(entries), 1, "session directories")
		polMatches(t, entries[0].Name(), `^~sha256-[a-f0-9]{64}$`)
		equal(t, rfStrs(resultFilesForSession(dir, rfWinSession)), []string{runID + ".json"}, "session files")
		equal(t, rfStrs(resultCandidateFilesForSession(dir, rfWinSession)), []string{runID + ".json"}, "candidates")
	})

	tw(t, f, "treats an unaddressable legacy session alias as absent and keeps canonical fallback candidates", func(t *testing.T) {
		dir := t.TempDir()
		runID := "legacy-alias-fallback"
		resultPath := filepath.Join(dir, runID+".json")
		enc, _ := jsEncodeURIComponent(rfWinSession)
		legacyDir := filepath.Join(dir, "result-index", "sessions", enc)
		rfWrite(t, resultPath, rfPayload(runID, rfWinSession, true))
		canonicalDir := filepath.Join(dir, "result-index", "sessions", encodeIndexSegment(rfWinSession))
		canonicalIndexFile := rfFirstEntry(t, canonicalDir)
		canonicalPending := rfPendingPath(dir, rfWinSession, runID)
		rfCopy(t, resultPath, canonicalPending)

		tooLong := newFSError("ENAMETOOLONG", "legacy alias is too long")
		realRead, realDir := resultReadFile, resultReadDir
		seam(t, &resultReadFile, func(p string) ([]byte, error) {
			if len(p) >= len(legacyDir) && p[:len(legacyDir)] == legacyDir {
				return nil, tooLong
			}
			return realRead(p)
		})
		seam(t, &resultReadDir, func(p string) ([]os.DirEntry, error) {
			if p == legacyDir {
				return nil, tooLong
			}
			return realDir(p)
		})

		equal(t, rfStrs(resultCandidateFilesForSession(dir, rfWinSession)), []string{runID + ".json"}, "candidates")
		rfCopy(t, resultPath, canonicalPending)
		if err := os.Remove(filepath.Join(canonicalDir, canonicalIndexFile)); err != nil {
			t.Fatal(err)
		}
		got, err := resultPayloadPathForSessionRun(dir, rfWinSession, runID)
		equal(t, err, nil, "error")
		equal(t, got, canonicalPending, "fallback payload")
	})

	tw(t, f, "leaves an unaddressable legacy index untouched during cleanup", func(t *testing.T) {
		dir := t.TempDir()
		runID := "legacy-cleanup"
		rfWrite(t, filepath.Join(dir, runID+".json"), rfPayload(runID, rfWinSession, true))
		canonicalDir := filepath.Join(dir, "result-index", "sessions", encodeIndexSegment(rfWinSession))
		enc, _ := jsEncodeURIComponent(rfWinSession)
		legacyDir := filepath.Join(dir, "result-index", "sessions", enc)
		if err := os.Rename(canonicalDir, legacyDir); err != nil {
			t.Fatal(err)
		}
		legacyIndex := filepath.Join(legacyDir, rfFirstEntry(t, legacyDir))
		realStat := resultStat
		seam(t, &resultStat, func(p string) (os.FileInfo, error) {
			if p == legacyIndex {
				return nil, newFSError("ENAMETOOLONG", "legacy alias is too long")
			}
			return realStat(p)
		})

		equal(t, cleanupResultIndexes(dir, time.Now().UnixMilli()+86_400_001, 86_400_000), 0, "removed")
		equal(t, rfExists(legacyIndex), true, "legacy index kept")
	})

	tw(t, f, "reads a pre-hash URI-encoded session index after the encoding change", func(t *testing.T) {
		dir := t.TempDir()
		runID := "legacy-session-run"
		resultPath := filepath.Join(dir, runID+".json")
		rfWrite(t, resultPath, rfPayload(runID, rfWinSession, true))
		enc, _ := jsEncodeURIComponent(rfWinSession)
		if err := os.Rename(filepath.Join(dir, "result-index", "sessions", encodeIndexSegment(rfWinSession)), filepath.Join(dir, "result-index", "sessions", enc)); err != nil {
			t.Fatal(err)
		}

		equal(t, rfStrs(resultFilesForSession(dir, rfWinSession)), []string{runID + ".json"}, "session files")
		got, _ := resultPayloadPathForSessionRun(dir, rfWinSession, runID)
		equal(t, got, resultPath, "payload")
	})

	tw(t, f, "reads a pre-hash extension-like run index filename", func(t *testing.T) {
		dir := t.TempDir()
		sessionID, runID := "session-a", "legacy.jsonl"
		resultPath := filepath.Join(dir, runID+".json")
		rfWrite(t, resultPath, rfPayload(runID, sessionID, true))
		indexDir := filepath.Join(dir, "result-index", "sessions", encodeIndexSegment(sessionID))
		current := rfFirstEntry(t, indexDir)
		if err := os.Rename(filepath.Join(indexDir, current), filepath.Join(indexDir, runID+".json")); err != nil {
			t.Fatal(err)
		}

		got, _ := resultPayloadPathForSessionRun(dir, sessionID, runID)
		equal(t, got, resultPath, "payload")
	})

	tw(t, f, "reads a pre-hash extension-like pending filename", func(t *testing.T) {
		dir := t.TempDir()
		sessionID, runID := "session-a", "legacy.jsonl"
		resultPath := filepath.Join(dir, runID+".json")
		if err := os.Mkdir(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writePendingAsyncResultFile(resultPath, rfPayload(runID, sessionID, true)); err != nil {
			t.Fatal(err)
		}
		current := rfPendingPath(dir, sessionID, runID)
		historical := filepath.Join(filepath.Dir(current), runID+".json")
		if err := os.Rename(current, historical); err != nil {
			t.Fatal(err)
		}

		got, _ := resultPayloadPathForSessionRun(dir, sessionID, runID)
		equal(t, got, historical, "payload")
	})

	tw(t, f, "throws access-denied direct session index reads", func(t *testing.T) {
		dir := t.TempDir()
		rfWrite(t, filepath.Join(dir, "blocked.json"), rfPayload("blocked", "session-a", true))
		denied := newFSError("EACCES", "permission denied")
		seam(t, &resultReadFile, func(string) ([]byte, error) { return nil, denied })

		_, err := resultPayloadPathForSessionRun(dir, "session-a", "blocked")
		if err != denied {
			t.Errorf("got %v, want the access error itself", err)
		}
	})

	tw(t, f, "returns no session candidates when the session index is unlistable", func(t *testing.T) {
		dir := t.TempDir()
		seam(t, &resultReadDir, func(string) ([]os.DirEntry, error) { return nil, newFSError("EPERM", "operation not permitted") })
		equal(t, rfStrs(resultCandidateFilesForSession(dir, "session-a")), []string{}, "candidates")
	})
}
