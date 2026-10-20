package pi_subagents

// Result files of finished background runs: the payload, its per-session and per-run indexes and the pending copy a result
// is promoted from. Ports src/runs/background/result-files.ts (the mission observer index is not ported: missions are not).

import ()

const (
	resultIndexVersion = 1
	resultIndexDir     = "result-index"
	sessionIndexDirNm  = "sessions"
	runIndexDirNm      = "runs"
	toolCallIndexDirNm = "tool-calls"
	resultPendingDir   = "result-pending"
	jsonExtension      = ".json"
)

var maxJSONFileStemBytes = maxIndexSegmentBytes - len(jsonExtension)

// ResultIndexEntry points an index file at a result payload.
type ResultIndexEntry struct {
	Version   int    `json:"version"`
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	File      string `json:"file"`
	WrittenAt ms     `json:"writtenAt"`
	AsyncDir  string `json:"asyncDir,omitempty"`
}

func encodedJSONFileName(value string) string { panic("not ported yet") }

func encodedJSONFileNames(value string) []string { panic("not ported yet") }

func nonEmptyString(v any) string { panic("not ported yet") }

// isUnaddressable: a legacy alias can be valid metadata while still being unaddressable on this filesystem.
func isUnaddressable(err error) bool { panic("not ported yet") }

func isAccessDenied(err error) bool { panic("not ported yet") }

func resultFileName(runID string) string { panic("not ported yet") }

func resultFilePath(resultsDir, runID string) string { panic("not ported yet") }

func sessionIndexDirs(resultsDir, sessionID string) []string { panic("not ported yet") }

func resultIndexPaths(resultsDir, sessionID, runID string) []string { panic("not ported yet") }

func runIndexPath(resultsDir, runID string) string { panic("not ported yet") }

func resultPendingPath(resultsDir, sessionID, runID string) string { panic("not ported yet") }

func resultPendingPaths(resultsDir, sessionID, runID string) []string { panic("not ported yet") }

func pendingSessionDirs(resultsDir, sessionID string) []string { panic("not ported yet") }

func toolCallIndexDirOf(resultsDir, toolCallID string) string { panic("not ported yet") }

func toolCallIndexPath(resultsDir, toolCallID, runID string) string { panic("not ported yet") }

func existingResultFile(p string) bool { panic("not ported yet") }

func firstExistingResultFile(paths []string) string { panic("not ported yet") }

func parseResultIndexEntry(data []byte) (*ResultIndexEntry, bool) { panic("not ported yet") }

func payloadRunID(data map[string]any) string { panic("not ported yet") }

// writeResultIndexForData writes the session, run and tool-call indexes of a result payload at resultPath.
func writeResultIndexForData(resultPath string, data map[string]any) error { panic("not ported yet") }

func writeIndexedPendingResultFile(resultPath string, data map[string]any) (runID, sessionID, resultsDir string, err error) {
	panic("not ported yet")
}

// writePendingAsyncResultFile writes the pending copy of a result and its indexes; promotion publishes it.
func writePendingAsyncResultFile(resultPath string, data map[string]any) error {
	panic("not ported yet")
}

// writeAsyncResultFile writes the pending copy, indexes it and promotes it to the public path; "public" when that worked.
func writeAsyncResultFile(resultPath string, data map[string]any) (string, error) {
	panic("not ported yet")
}

// removeResultIndex removes the indexes and the pending copy of a delivered result.
func removeResultIndex(resultsDir, sessionID, runID, toolCallID string) { panic("not ported yet") }

func pendingResultExists(resultsDir, sessionID, runID string) bool { panic("not ported yet") }

// resultPayloadMatchesSessionRun: the payload names this run and this session.
func resultPayloadMatchesSessionRun(data any, sessionID, runID string) bool { panic("not ported yet") }

func pendingPayloadMatches(filePath, sessionID, runID string) bool { panic("not ported yet") }

// promotePendingResultFile renames the pending copy over the public path: "none", "promoted" or "pending".
func promotePendingResultFile(resultsDir, sessionID, runID, file string) string {
	panic("not ported yet")
}

type resultPayloadLocation struct {
	File  string
	Path  string
	State string // public or pending
}

func pendingResultLocationForSessionRun(resultsDir, sessionID, runID, file string) *resultPayloadLocation {
	panic("not ported yet")
}

// fallbackResultPayloadPathForSessionRun is the pending copy of this session's run; a malformed copy is an error.
func fallbackResultPayloadPathForSessionRun(resultsDir, sessionID, runID string) (string, error) {
	panic("not ported yet")
}

func resultPayloadLocationFromIndex(resultsDir string, e *ResultIndexEntry) *resultPayloadLocation {
	panic("not ported yet")
}

// readResultIndexForSessionRun: access errors propagate (the caller falls back to the pending copy), a bad file is skipped.
func readResultIndexForSessionRun(resultsDir, sessionID, runID string) (*ResultIndexEntry, error) {
	panic("not ported yet")
}

// resultPayloadPathForSessionRun finds the payload of a session's run through its index, else its pending copy.
func resultPayloadPathForSessionRun(resultsDir, sessionID, runID string) (string, error) {
	panic("not ported yet")
}

// resultPayloadPathForIndexedRun finds a run's payload by the run index alone.
func resultPayloadPathForIndexedRun(resultsDir, runID string) string { panic("not ported yet") }

func indexedResultFile(resultsDir string, e *ResultIndexEntry, includePending bool) string {
	panic("not ported yet")
}

func listIndexFiles(dir string) []string { panic("not ported yet") }

func resultFilesFromIndexDir(resultsDir, dir string, includePending bool) []string {
	panic("not ported yet")
}

// resultFilesForSession lists the public result files of a session.
func resultFilesForSession(resultsDir, sessionID string) []string { panic("not ported yet") }

func pendingResultFilesForSession(resultsDir, sessionID string) []string { panic("not ported yet") }

// resultCandidateFilesForSession lists the result files of a session, pending ones included.
func resultCandidateFilesForSession(resultsDir, sessionID string) []string { panic("not ported yet") }

func resultFilesForToolCall(resultsDir, toolCallID string) []string { panic("not ported yet") }

func resultCandidateFilesForToolCall(resultsDir, toolCallID string) []string { panic("not ported yet") }

// cleanupResultIndexes removes index files older than maxAgeMs whose result is gone, and the empty directories.
func cleanupResultIndexes(resultsDir string, now, maxAgeMs int64) int { panic("not ported yet") }

// only an empty directory goes
