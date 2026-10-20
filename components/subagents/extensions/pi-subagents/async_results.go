package pi_subagents

// Result files of finished background runs: the payload, its per-session and per-run indexes and the pending copy a result
// is promoted from. Ports src/runs/background/result-files.ts (the mission observer index is not ported: missions are not).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// The file operations of the result files, as variables so a test can make one of them fail the way the original's tests make
// node:fs fail (an access error, a name that is too long, a rename that cannot replace its destination).
var (
	resultReadFile = os.ReadFile
	resultReadDir  = os.ReadDir
	resultStat     = os.Stat
	resultRename   = os.Rename
)

const (
	observerIndexDirNm = "observers"
	missionObserver    = "mission"
)

// ResultIndexEntry points an index file at a result payload.
type ResultIndexEntry struct {
	Version   int    `json:"version"`
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	File      string `json:"file"`
	WrittenAt ms     `json:"writtenAt"`
	AsyncDir  string `json:"asyncDir,omitempty"`
}

func encodedJSONFileName(value string) string {
	return encodeIndexSegmentMax(value, maxJSONFileStemBytes) + jsonExtension
}

func encodedJSONFileNames(value string) []string {
	var out []string
	for _, seg := range indexSegmentAliases(value, maxJSONFileStemBytes) {
		out = append(out, seg+jsonExtension)
	}
	return out
}

func nonEmptyString(v any) string {
	s, _ := v.(string)
	return s
}

// isUnaddressable: a legacy alias can be valid metadata while still being unaddressable on this filesystem.
func isUnaddressable(err error) bool { return fsErrorCode(err) == "ENAMETOOLONG" }

func isAccessDenied(err error) bool {
	c := fsErrorCode(err)
	return c == "EPERM" || c == "EACCES"
}

func resultFileName(runID string) string { return runID + jsonExtension }

func resultFilePath(resultsDir, runID string) string {
	return filepath.Join(resultsDir, resultFileName(runID))
}

func sessionIndexDirs(resultsDir, sessionID string) []string {
	var out []string
	for _, seg := range indexSegmentAliases(sessionID, maxIndexSegmentBytes) {
		out = append(out, filepath.Join(resultsDir, resultIndexDir, sessionIndexDirNm, seg))
	}
	return out
}

func resultIndexPaths(resultsDir, sessionID, runID string) []string {
	var out []string
	for _, d := range sessionIndexDirs(resultsDir, sessionID) {
		for _, name := range encodedJSONFileNames(runID) {
			out = append(out, filepath.Join(d, name))
		}
	}
	return out
}

func runIndexPath(resultsDir, runID string) string {
	return filepath.Join(resultsDir, resultIndexDir, runIndexDirNm, encodedJSONFileName(runID))
}

func resultPendingPath(resultsDir, sessionID, runID string) string {
	return filepath.Join(resultsDir, resultPendingDir, encodeIndexSegment(sessionID), encodedJSONFileName(runID))
}

func resultPendingPaths(resultsDir, sessionID, runID string) []string {
	var out []string
	for _, seg := range indexSegmentAliases(sessionID, maxIndexSegmentBytes) {
		for _, name := range encodedJSONFileNames(runID) {
			out = append(out, filepath.Join(resultsDir, resultPendingDir, seg, name))
		}
	}
	return out
}

func pendingSessionDirs(resultsDir, sessionID string) []string {
	var out []string
	for _, seg := range indexSegmentAliases(sessionID, maxIndexSegmentBytes) {
		out = append(out, filepath.Join(resultsDir, resultPendingDir, seg))
	}
	return out
}

func toolCallIndexDirOf(resultsDir, toolCallID string) string {
	return filepath.Join(resultsDir, resultIndexDir, toolCallIndexDirNm, encodeIndexSegment(toolCallID))
}

func toolCallIndexPath(resultsDir, toolCallID, runID string) string {
	return filepath.Join(toolCallIndexDirOf(resultsDir, toolCallID), encodedJSONFileName(runID))
}

func existingResultFile(p string) bool {
	st, err := resultStat(p)
	return err == nil && st.Mode().IsRegular()
}

func firstExistingResultFile(paths []string) string {
	for _, p := range paths {
		if existingResultFile(p) {
			return p
		}
	}
	return ""
}

func parseResultIndexEntry(data []byte) (*ResultIndexEntry, bool) {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil, false
	}
	version, _ := raw["version"].(float64)
	runID, okRun := raw["runId"].(string)
	sessionID, okSession := raw["sessionId"].(string)
	file, okFile := raw["file"].(string)
	written, okWritten := raw["writtenAt"].(float64)
	if version != resultIndexVersion || !okRun || !okSession || !okFile || !okWritten {
		return nil, false
	}
	return &ResultIndexEntry{Version: resultIndexVersion, RunID: runID, SessionID: sessionID, File: file, WrittenAt: ms(written), AsyncDir: nonEmptyString(raw["asyncDir"])}, true
}

func payloadRunID(data map[string]any) string {
	if s := nonEmptyString(data["runId"]); s != "" {
		return s
	}
	return nonEmptyString(data["id"])
}

// writeResultIndexForData writes the session, run and tool-call indexes of a result payload at resultPath.
func writeResultIndexForData(resultPath string, data map[string]any) error {
	runID := payloadRunID(data)
	if runID == "" {
		runID = strings.TrimSuffix(filepath.Base(resultPath), ".json")
	}
	sessionID := nonEmptyString(data["sessionId"])
	if runID == "" || sessionID == "" {
		return nil
	}
	entry := ResultIndexEntry{Version: resultIndexVersion, RunID: runID, SessionID: sessionID, File: filepath.Base(resultPath), WrittenAt: ms(nowMs()), AsyncDir: nonEmptyString(data["asyncDir"])}
	resultsDir := filepath.Dir(resultPath)
	if err := asyncWriteJSON(resultIndexPaths(resultsDir, sessionID, runID)[0], entry); err != nil {
		return err
	}
	_ = asyncWriteJSON(runIndexPath(resultsDir, runID), entry)
	if tc := nonEmptyString(data["toolCallId"]); tc != "" {
		_ = asyncWriteJSON(toolCallIndexPath(resultsDir, tc, runID), entry)
	}
	if dir := nonEmptyString(data["asyncDir"]); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, missionBindingFile)); err == nil {
			_ = asyncWriteJSON(observerIndexPath(resultsDir, missionObserver, runID), entry)
		}
	}
	return nil
}

func writeIndexedPendingResultFile(resultPath string, data map[string]any) (runID, sessionID, resultsDir string, err error) {
	runID = payloadRunID(data)
	if runID == "" {
		runID = strings.TrimSuffix(filepath.Base(resultPath), ".json")
	}
	sessionID = nonEmptyString(data["sessionId"])
	if sessionID == "" {
		return "", "", "", fmt.Errorf("Cannot write async result '%s' without a sessionId.", resultPath)
	}
	resultsDir = filepath.Dir(resultPath)
	if err = asyncWriteJSON(resultPendingPath(resultsDir, sessionID, runID), data); err != nil {
		return
	}
	err = writeResultIndexForData(resultPath, data)
	return
}

// writePendingAsyncResultFile writes the pending copy of a result and its indexes; promotion publishes it.
func writePendingAsyncResultFile(resultPath string, data map[string]any) error {
	_, _, _, err := writeIndexedPendingResultFile(resultPath, data)
	return err
}

// writeAsyncResultFile writes the pending copy, indexes it and promotes it to the public path; "public" when that worked.
func writeAsyncResultFile(resultPath string, data map[string]any) (string, error) {
	runID, sessionID, resultsDir, err := writeIndexedPendingResultFile(resultPath, data)
	if err != nil {
		return "", err
	}
	if promotePendingResultFile(resultsDir, sessionID, runID, filepath.Base(resultPath)) == "promoted" {
		return "public", nil
	}
	return "pending", nil
}

// removeResultIndex removes the indexes and the pending copy of a delivered result.
func removeResultIndex(resultsDir, sessionID, runID, toolCallID string) {
	if runID == "" {
		return
	}
	if sessionID != "" {
		for _, p := range resultIndexPaths(resultsDir, sessionID, runID) {
			_ = os.Remove(p)
		}
		for _, p := range resultPendingPaths(resultsDir, sessionID, runID) {
			_ = os.Remove(p)
		}
	}
	_ = os.Remove(runIndexPath(resultsDir, runID))
	if toolCallID != "" {
		_ = os.Remove(toolCallIndexPath(resultsDir, toolCallID, runID))
	}
	_ = os.Remove(observerIndexPath(resultsDir, missionObserver, runID))
}

func pendingResultExists(resultsDir, sessionID, runID string) bool {
	return firstExistingResultFile(resultPendingPaths(resultsDir, sessionID, runID)) != ""
}

// resultPayloadMatchesSessionRun: the payload names this run and this session.
func resultPayloadMatchesSessionRun(data any, sessionID, runID string) bool {
	m, ok := data.(map[string]any)
	if !ok {
		return false
	}
	return payloadRunID(m) == runID && nonEmptyString(m["sessionId"]) == sessionID
}

func pendingPayloadMatches(filePath, sessionID, runID string) bool {
	data, err := resultReadFile(filePath)
	if err != nil {
		return false
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return resultPayloadMatchesSessionRun(v, sessionID, runID)
}

// promotePendingResultFile renames the pending copy over the public path: "none", "promoted" or "pending".
func promotePendingResultFile(resultsDir, sessionID, runID, file string) string {
	if file != filepath.Base(file) || !strings.HasSuffix(file, ".json") {
		return "none"
	}
	pendingPath := firstExistingResultFile(resultPendingPaths(resultsDir, sessionID, runID))
	if pendingPath == "" {
		return "none"
	}
	resultPath := filepath.Join(resultsDir, file)
	err := resultRename(pendingPath, resultPath)
	if err == nil {
		if existingResultFile(resultPath) {
			return "promoted"
		}
		return "pending"
	}
	switch fsErrorCode(err) {
	case "ENOENT", "EEXIST", "EPERM", "EACCES", "ENAMETOOLONG":
		if existingResultFile(pendingPath) {
			return "pending"
		}
		if existingResultFile(resultPath) {
			return "promoted"
		}
		return "none"
	}
	return "pending"
}

type resultPayloadLocation struct {
	File  string
	Path  string
	State string // public or pending
}

func pendingResultLocationForSessionRun(resultsDir, sessionID, runID, file string) *resultPayloadLocation {
	if file != filepath.Base(file) || !strings.HasSuffix(file, ".json") {
		return nil
	}
	p := firstExistingResultFile(resultPendingPaths(resultsDir, sessionID, runID))
	if p == "" || !pendingPayloadMatches(p, sessionID, runID) {
		return nil
	}
	return &resultPayloadLocation{File: file, Path: p, State: "pending"}
}

// fallbackResultPayloadPathForSessionRun is the pending copy of this session's run; a malformed copy is an error.
func fallbackResultPayloadPathForSessionRun(resultsDir, sessionID, runID string) (string, error) {
	p := firstExistingResultFile(resultPendingPaths(resultsDir, sessionID, runID))
	if p == "" {
		return "", nil
	}
	data, err := resultReadFile(p)
	if err != nil {
		return "", err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return "", err
	}
	if !resultPayloadMatchesSessionRun(v, sessionID, runID) {
		return "", nil
	}
	return p, nil
}

func resultPayloadLocationFromIndex(resultsDir string, e *ResultIndexEntry) *resultPayloadLocation {
	if e.File != filepath.Base(e.File) || !strings.HasSuffix(e.File, ".json") {
		return nil
	}
	pending := promotePendingResultFile(resultsDir, e.SessionID, e.RunID, e.File)
	if pending == "pending" {
		return pendingResultLocationForSessionRun(resultsDir, e.SessionID, e.RunID, e.File)
	}
	resultPath := filepath.Join(resultsDir, e.File)
	if pending == "promoted" || existingResultFile(resultPath) {
		return &resultPayloadLocation{File: e.File, Path: resultPath, State: "public"}
	}
	return nil
}

// readResultIndexForSessionRun: access errors propagate (the caller falls back to the pending copy), a bad file is skipped.
func readResultIndexForSessionRun(resultsDir, sessionID, runID string) (*ResultIndexEntry, error) {
	for _, p := range resultIndexPaths(resultsDir, sessionID, runID) {
		data, err := resultReadFile(p)
		if err != nil {
			if isAccessDenied(err) {
				return nil, err
			}
			continue
		}
		e, ok := parseResultIndexEntry(data)
		if !ok || e.SessionID != sessionID || e.RunID != runID {
			continue
		}
		return e, nil
	}
	return nil, nil
}

// resultPayloadPathForSessionRun finds the payload of a session's run through its index, else its pending copy.
func resultPayloadPathForSessionRun(resultsDir, sessionID, runID string) (string, error) {
	entry, err := readResultIndexForSessionRun(resultsDir, sessionID, runID)
	if err != nil {
		return "", err
	}
	if entry != nil {
		if loc := resultPayloadLocationFromIndex(resultsDir, entry); loc != nil {
			return loc.Path, nil
		}
	}
	if loc := pendingResultLocationForSessionRun(resultsDir, sessionID, runID, resultFileName(runID)); loc != nil {
		return loc.Path, nil
	}
	return "", nil
}

// resultPayloadPathForIndexedRun finds a run's payload by the run index alone.
func resultPayloadPathForIndexedRun(resultsDir, runID string) string {
	entryPath := runIndexPath(resultsDir, runID)
	data, err := resultReadFile(entryPath)
	if err != nil {
		return ""
	}
	e, ok := parseResultIndexEntry(data)
	if !ok || e.RunID != runID {
		_ = os.Remove(entryPath)
		return ""
	}
	if loc := resultPayloadLocationFromIndex(resultsDir, e); loc != nil {
		return loc.Path
	}
	_ = os.Remove(entryPath)
	return ""
}

func indexedResultFile(resultsDir string, e *ResultIndexEntry, includePending bool) string {
	loc := resultPayloadLocationFromIndex(resultsDir, e)
	if loc == nil {
		return ""
	}
	if includePending || loc.State == "public" {
		return loc.File
	}
	return ""
}

func listIndexFiles(dir string) []string {
	entries, err := resultReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func resultFilesFromIndexDir(resultsDir, dir string, includePending bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range listIndexFiles(dir) {
		data, err := resultReadFile(p)
		if err != nil {
			continue
		}
		e, ok := parseResultIndexEntry(data)
		if !ok {
			_ = os.Remove(p)
			continue
		}
		if f := indexedResultFile(resultsDir, e, includePending); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// resultFilesForSession lists the public result files of a session.
func resultFilesForSession(resultsDir, sessionID string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range sessionIndexDirs(resultsDir, sessionID) {
		for _, f := range resultFilesFromIndexDir(resultsDir, d, false) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

func pendingResultFilesForSession(resultsDir, sessionID string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range pendingSessionDirs(resultsDir, sessionID) {
		entries, err := resultReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := resultReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			if id := payloadRunID(m); id != "" && nonEmptyString(m["sessionId"]) == sessionID {
				f := resultFileName(id)
				if !seen[f] {
					seen[f] = true
					out = append(out, f)
				}
			}
		}
	}
	return out
}

// resultCandidateFilesForSession lists the result files of a session, pending ones included.
func resultCandidateFilesForSession(resultsDir, sessionID string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, d := range sessionIndexDirs(resultsDir, sessionID) {
		for _, f := range resultFilesFromIndexDir(resultsDir, d, true) {
			add(f)
		}
	}
	for _, f := range pendingResultFilesForSession(resultsDir, sessionID) {
		add(f)
	}
	return out
}

func resultFilesForToolCall(resultsDir, toolCallID string) []string {
	return resultFilesFromIndexDir(resultsDir, toolCallIndexDirOf(resultsDir, toolCallID), false)
}

func resultCandidateFilesForToolCall(resultsDir, toolCallID string) []string {
	return resultFilesFromIndexDir(resultsDir, toolCallIndexDirOf(resultsDir, toolCallID), true)
}

// cleanupResultIndexes removes index files older than maxAgeMs whose result is gone, and the empty directories.
func cleanupResultIndexes(resultsDir string, now, maxAgeMs int64) int {
	root := filepath.Join(resultsDir, resultIndexDir)
	cutoff := now - maxAgeMs
	removed := 0
	var visit func(dir string)
	visit = func(dir string) {
		entries, err := resultReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				visit(full)
				_ = os.Remove(full) // only an empty directory goes
				continue
			}
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			st, serr := resultStat(full)
			data, rerr := resultReadFile(full)
			if isUnaddressable(serr) || isUnaddressable(rerr) {
				continue
			}
			if serr != nil || rerr != nil {
				if os.Remove(full) == nil {
					removed++
				}
				continue
			}
			idx, ok := parseResultIndexEntry(data)
			resultFile := ""
			pending := false
			if ok {
				resultFile = indexedResultFile(resultsDir, idx, false)
				pending = pendingResultExists(resultsDir, idx.SessionID, idx.RunID)
			}
			if !ok || (resultFile == "" && !pending && st.ModTime().UnixMilli() <= cutoff) {
				if os.Remove(full) == nil {
					removed++
				}
			}
		}
	}
	visit(root)
	return removed
}

func observerIndexDir(resultsDir, observer string) string {
	return filepath.Join(resultsDir, resultIndexDir, observerIndexDirNm, observer)
}

func observerIndexPath(resultsDir, observer, runID string) string {
	return filepath.Join(observerIndexDir(resultsDir, observer), encodedJSONFileName(runID))
}

// resultPayloadPathForMissionObserverRun finds the payload of a mission-observed run by the observer index ("" when absent).
func resultPayloadPathForMissionObserverRun(resultsDir, runID string) string {
	data, err := resultReadFile(observerIndexPath(resultsDir, missionObserver, runID))
	if err != nil {
		return ""
	}
	e, ok := parseResultIndexEntry(data)
	if !ok || e.RunID != runID {
		return ""
	}
	if loc := resultPayloadLocationFromIndex(resultsDir, e); loc != nil {
		return loc.Path
	}
	return ""
}

func missionObserverResultFiles(resultsDir string) []string {
	return resultFilesFromIndexDir(resultsDir, observerIndexDir(resultsDir, missionObserver), false)
}

func missionObserverResultCandidateFiles(resultsDir string) []string {
	return resultFilesFromIndexDir(resultsDir, observerIndexDir(resultsDir, missionObserver), true)
}
