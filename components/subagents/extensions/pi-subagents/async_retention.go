package pi_subagents

// Retention of old background state: runs and results that ended long ago are deleted, through tombstones, in bounded passes
// under one cleaner lock. Ports runs/background/async-retention.ts and its discovery worker
// (async-retention-discovery-worker.mjs; here a goroutine, since Go needs no worker thread to keep the commit side responsive).
//
// Every decision fails closed: a run or result is only deleted when it is proven terminal, old, not resumable and referenced
// by nothing. The persisted JSON is untrusted and read as loose maps (a malformed field means "unknown age", never "old").

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	asyncRetentionDays             = 30
	asyncRetentionBatchSize        = 100
	asyncRetentionDelayMs          = 60_000
	asyncRetentionTombstoneGraceMs = 24 * 60 * 60 * 1000

	retentionMs          = asyncRetentionDays * 24 * 60 * 60 * 1000
	retentionLockName    = ".async-retention.lock"
	retentionCursorName  = ".async-retention-cursor.json"
	retentionLogName     = "async-retention-maintenance.jsonl"
	runTombstonePrefix   = ".deleting-run-"
	resultTombstonePref  = ".deleting-result-"
	runTombstoneMarkers  = "async-retention-run-tombstones"
	retentionLockStaleMs = 24 * 60 * 60 * 1000
	missionBindingFile   = "mission.json"
)

var (
	retentionRunModes       = map[string]bool{"single": true, "parallel": true, "chain": true, "workflow": true}
	retentionTerminalStates = map[string]bool{"complete": true, "failed": true, "stopped": true, "rejected": true}
	resultTimestampFields   = []string{"endedAt", "completedAt", "createdAt", "writtenAt", "expiresAt", "timestamp"}
)

// ── options and result ──

// AsyncRetentionOptions are the inputs and seams of one cleanup pass.
type AsyncRetentionOptions struct {
	AsyncDirRoot        string
	ResultsDir          string
	WaitSubscriptionDir string
	// ProtectedRunIDs is read once, after the lock is held (runs the session tracks never go).
	ProtectedRunIDs func() []string
	Now             func() int64
	RetentionMs     *int64
	TombstoneGrace  *int64
	BatchSize       *int
	RandomID        func() string
	MaintenanceRoot string
	PID             *int
	Hostname        string
	// ProcessStartIdentity is this process's identity for the lock owner; GetProcessStartIdentity resolves another's ("" when unknown).
	ProcessStartIdentity    string
	IsProcessAlive          func(pid int) *bool
	GetProcessStartIdentity func(pid int) string
	Lstat                   func(path string) (fs.FileInfo, error)
	// Cancel ends the pass when closed.
	Cancel <-chan struct{}
	// Discover replaces the discovery pass (a test seam: the original's discoveryWorkerUrl).
	Discover      func(req RetentionDiscoveryRequest) (*RetentionDiscovery, error)
	ReconcileKill killFunc
}

// AsyncRetentionResult is what a pass did.
type AsyncRetentionResult struct {
	Acquired            bool            `json:"acquired"`
	Scanned             int             `json:"scanned"`
	RepairedRuns        int             `json:"repairedRuns"`
	DeletedRuns         int             `json:"deletedRuns"`
	DeletedResults      int             `json:"deletedResults"`
	ReapedTombstones    int             `json:"reapedTombstones"`
	Skipped             map[string]int  `json:"skipped"`
	Errors              []string        `json:"errors"`
	RawReads            int             `json:"rawReads"`
	SourceExhausted     map[string]bool `json:"sourceExhausted"`
	DiscoveryDurationMs int64           `json:"discoveryDurationMs"`
	CommitDurationMs    int64           `json:"commitDurationMs"`
	Cancelled           bool            `json:"cancelled"`
	WorkerFailed        bool            `json:"workerFailed"`
	DurationMs          int64           `json:"durationMs"`
}

// ── loose JSON ──

// parseLooseJSON parses JSON the way JSON.parse would: a number out of float64's range is an infinity, not an error.
func parseLooseJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return looseValue(v), nil
}

// readLooseJSON reads a JSON object file loosely; nil when it is missing, unparsable or not an object.
func readLooseJSON(path string) jm {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	v, err := parseLooseJSON(b)
	if err != nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

func looseValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := strconv.ParseFloat(string(x), 64) // ErrRange yields ±Inf, as JSON.parse does
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = looseValue(e)
		}
	case []any:
		for i, e := range x {
			x[i] = looseValue(e)
		}
	}
	return v
}

func finiteNum(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsInf(f, 0) && !math.IsNaN(f)
}

func jmHas(m jm, k string) bool { _, ok := m[k]; return ok }

func compactError(err error) string {
	return truncateRunes(strings.Join(strings.Fields(err.Error()), " "), 240)
}

func truncateRunes(s string, utf16Units int) string {
	u := jsUTF16(s)
	if len(u) <= utf16Units {
		return s
	}
	return string(utf16Runes(u[:utf16Units]))
}


func isNotFoundErr(err error) bool { return err != nil && errors.Is(err, fs.ErrNotExist) }

// ── runs ──

func retentionReadStatus(runDir string) jm {
	v := readLooseJSON(filepath.Join(runDir, "status.json"))
	if v == nil {
		return nil
	}
	runID, ok1 := v["runId"].(string)
	_, ok2 := v["state"].(string)
	mode, ok3 := v["mode"].(string)
	_, ok4 := finiteNum(v["startedAt"])
	if !ok1 || !ok2 || !ok3 || !retentionRunModes[mode] || !ok4 {
		return nil
	}
	_ = runID
	return v
}

func existingRegularFile(p any) bool {
	switch x := p.(type) {
	case nil:
		return false
	case string:
		if x == "" {
			return false
		}
		info, err := os.Lstat(x)
		if err != nil {
			return !isNotFoundErr(err)
		}
		return info.Mode().IsRegular()
	case bool:
		return x // true is truthy and uninspectable: fail closed
	case float64:
		return x != 0
	}
	return true
}

func validRunID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id
}

func statusTimestamp(status jm, runDir string) (float64, bool) {
	var logical *float64
	for _, k := range []string{"endedAt", "lastUpdate"} {
		if !jmHas(status, k) {
			continue
		}
		f, ok := finiteNum(status[k])
		if !ok {
			return 0, false
		}
		if logical == nil {
			logical = &f
		}
	}
	dirInfo, err1 := os.Stat(runDir)
	statusInfo, err2 := os.Stat(filepath.Join(runDir, "status.json"))
	if err1 != nil || err2 != nil {
		return 0, false
	}
	physical := math.Max(mtimeMs(dirInfo), mtimeMs(statusInfo))
	ts := physical
	if logical != nil {
		ts = math.Max(*logical, physical)
	}
	return ts, !math.IsInf(ts, 0) && !math.IsNaN(ts)
}

func retentionHasNestedReferences(status jm) bool {
	if status["isNested"] == true {
		return true
	}
	for _, s := range mapList(status["steps"]) {
		if c, ok := s["children"].([]any); ok && len(c) > 0 {
			return true
		}
	}
	return false
}

func retentionHasResumableContract(runDir string, status jm) bool {
	if existingRegularFile(status["sessionFile"]) {
		return true
	}
	for _, s := range mapList(status["steps"]) {
		if existingRegularFile(s["sessionFile"]) {
			return true
		}
	}
	descriptorPath := filepath.Join(runDir, "recovery-descriptor.json")
	if _, err := os.Stat(descriptorPath); err != nil {
		return false
	}
	d := readLooseJSON(descriptorPath)
	if d == nil {
		return true
	}
	if d["sourceRunId"] != status["runId"] {
		return true
	}
	sf, _ := d["sessionFile"].(string)
	return existingRegularFile(sf)
}

func pathExists(p string) bool { _, err := os.Stat(p); return err == nil }

func activeMarkerExists(asyncDirRoot, runID string) bool {
	return pathExists(filepath.Join(asyncDirRoot, activeRunIndexDir, runID))
}

func missionObserverIndexExists(resultsDir, runID string) bool {
	return pathExists(filepath.Join(resultsDir, "result-index", "observers", "mission", encodeIndexSegment(runID)+".json"))
}

func runTombstoneMarkerPath(root, runID string) string {
	return filepath.Join(root, runTombstoneMarkers, encodeIndexSegment(runID)+".json")
}

func writeRunTombstoneMarker(root, runID, tombstone string, now int64) error {
	p := runTombstoneMarkerPath(root, runID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return asyncWriteJSON(p, jm{"version": 1, "runId": runID, "tombstonePath": tombstone, "createdAt": now})
}

func removeRunTombstoneMarker(root, runID string) {
	_ = os.Remove(runTombstoneMarkerPath(root, runID))
}

type tombstoneMarker struct{ tombstonePath string }

// readRunTombstoneMarker is (marker, true) for a readable one, (nil, true) for none, and unreadable true for a damaged one.
func readRunTombstoneMarker(root, runID string) (marker *tombstoneMarker, unreadable bool) {
	p := runTombstoneMarkerPath(root, runID)
	info, err := os.Lstat(p)
	if err != nil {
		return nil, !isNotFoundErr(err)
	}
	if !info.Mode().IsRegular() {
		return nil, true
	}
	m := readLooseJSON(p)
	tp, _ := m["tombstonePath"].(string)
	if v, _ := m["version"].(float64); v != 1 || m["runId"] != runID || tp == "" {
		return nil, true
	}
	return &tombstoneMarker{tp}, false
}

func runTombstoneMarkerMatches(root, runID, tombstone string) bool {
	m, unreadable := readRunTombstoneMarker(root, runID)
	if m == nil || unreadable {
		return false
	}
	a, _ := filepath.Abs(m.tombstonePath)
	b, _ := filepath.Abs(tombstone)
	return a == b
}

func runTombstoneMarkerBlocks(root, runID string) bool {
	m, unreadable := readRunTombstoneMarker(root, runID)
	if unreadable {
		return true
	}
	if m == nil {
		return false
	}
	if pathExists(m.tombstonePath) {
		return true
	}
	removeRunTombstoneMarker(root, runID)
	return false
}

func unresolvedHandoff(manifestPath string) bool {
	m := readLooseJSON(manifestPath)
	if m == nil {
		return true
	}
	if v, _ := m["version"].(float64); v != 1 {
		return true
	}
	groups, ok := m["groups"].([]any)
	if !ok || len(groups) == 0 {
		return true
	}
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			return true
		}
		cleanup, ok := gm["cleanup"].(map[string]any)
		if !ok || cleanup["state"] != "complete" {
			return true
		}
	}
	return false
}

func hasUnresolvedRunHandoff(runDir string, status jm) bool {
	var paths []string
	if ph, ok := status["parallelHandoff"]; ok && ph != nil && ph != false {
		pm, _ := ph.(map[string]any)
		p, _ := pm["path"].(string)
		if p == "" {
			return true
		}
		paths = append(paths, p)
	}
	local := filepath.Join(runDir, "handoff.json")
	if pathExists(local) {
		found := false
		for _, p := range paths {
			found = found || p == local
		}
		if !found {
			paths = append(paths, local)
		}
	}
	for _, p := range paths {
		if unresolvedHandoff(p) {
			return true
		}
	}
	return false
}

type runSkipInput struct {
	runDir       string
	status       jm
	asyncDirRoot string
	resultsDir   string
	cutoff       float64
	protected    map[string]bool
	waitRunIDs   map[string]bool
}

func runSkipReason(in runSkipInput) string {
	status := in.status
	if status == nil {
		return "invalid-status"
	}
	runID, _ := status["runId"].(string)
	if !validRunID(runID) {
		return "identity-mismatch"
	}
	base := filepath.Base(in.runDir)
	if base != runID && !strings.HasPrefix(base, runTombstonePrefix) {
		return "identity-mismatch"
	}
	if in.protected[runID] {
		return "runtime-reference"
	}
	if in.waitRunIDs[runID] {
		return "wait-reference"
	}
	if activeMarkerExists(in.asyncDirRoot, runID) {
		return "active-index"
	}
	if state, _ := status["state"].(string); !retentionTerminalStates[state] {
		return "non-terminal"
	}
	if status["mode"] == "workflow" || truthy(status["parentWorkflowRunId"]) || truthy(status["workflowKey"]) {
		return "workflow-reference"
	}
	if retentionHasNestedReferences(status) {
		return "nested-reference"
	}
	if pathExists(filepath.Join(in.runDir, missionBindingFile)) || missionObserverIndexExists(in.resultsDir, runID) {
		return "mission-reference"
	}
	if hasUnresolvedRunHandoff(in.runDir, status) {
		return "handoff-reference"
	}
	if retentionHasResumableContract(in.runDir, status) {
		return "resumable"
	}
	ts, ok := statusTimestamp(status, in.runDir)
	if !ok {
		return "unknown-age"
	}
	if ts > in.cutoff {
		return "recent"
	}
	return ""
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

func parseWaitRunIDs(dir string) (runIDs map[string]bool, safe bool) {
	runIDs = map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotFoundErr(err) {
			return runIDs, true
		}
		return runIDs, false
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec := readLooseJSON(filepath.Join(dir, e.Name()))
		id, ok1 := rec["runId"].(string)
		_, ok2 := rec["expiresAt"].(float64)
		if rec == nil || !ok1 || !ok2 {
			return runIDs, false
		}
		runIDs[id] = true
	}
	return runIDs, true
}

// ── cursor ──

func readRetentionCursor(root string) jm {
	v := readLooseJSON(filepath.Join(root, retentionCursorName))
	if f, _ := v["version"].(float64); f == 1 {
		return v
	}
	return jm{"version": 1.0}
}

func cursorStr(c jm, key string) (string, bool) { s, ok := c[key].(string); return s, ok }

func cursorPending(c jm) map[string]any {
	m, _ := c["resultPendingAfterBySession"].(map[string]any)
	return m
}

// ── lock ──

type retentionLockOwner struct {
	Version              int    `json:"version"`
	Token                string `json:"token"`
	PID                  int    `json:"pid"`
	Hostname             string `json:"hostname"`
	StartedAt            int64  `json:"startedAt"`
	ProcessStartIdentity string `json:"processStartIdentity,omitempty"`
}

func parseLockOwner(lockDir string) *retentionLockOwner {
	o := readLooseJSON(filepath.Join(lockDir, "owner.json"))
	if o == nil {
		return nil
	}
	token, ok1 := o["token"].(string)
	pidF, ok2 := o["pid"].(float64)
	host, ok3 := o["hostname"].(string)
	startedF, ok4 := finiteNum(o["startedAt"])
	if v, _ := o["version"].(float64); v != 1 || !ok1 || !ok2 || pidF != math.Trunc(pidF) || pidF <= 0 || !ok3 || !ok4 {
		return nil
	}
	owner := &retentionLockOwner{Version: 1, Token: token, PID: int(pidF), Hostname: host, StartedAt: int64(startedF)}
	if psi, has := o["processStartIdentity"]; has {
		s, ok := psi.(string)
		if !ok {
			return nil
		}
		owner.ProcessStartIdentity = s
	}
	return owner
}

type lockProbes struct {
	hostname                string
	isProcessAlive          func(pid int) *bool
	getProcessStartIdentity func(pid int) string
}

func staleLock(lockDir string, now int64, p lockProbes) (stale bool, token string) {
	owner := parseLockOwner(lockDir)
	if owner == nil {
		info, err := os.Stat(lockDir)
		if err != nil {
			return false, ""
		}
		return float64(now)-mtimeMs(info) >= retentionLockStaleMs, ""
	}
	if owner.Hostname != p.hostname {
		return false, ""
	}
	alive := p.isProcessAlive(owner.PID)
	if alive != nil && !*alive {
		return true, owner.Token
	}
	if alive != nil && *alive && owner.ProcessStartIdentity != "" {
		if cur := p.getProcessStartIdentity(owner.PID); cur != "" && cur != owner.ProcessStartIdentity {
			return true, owner.Token
		}
	}
	return now-owner.StartedAt >= retentionLockStaleMs, owner.Token
}

func createLockDirectory(lockDir string, owner *retentionLockOwner) (bool, error) {
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	b, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), b, 0o600); err != nil {
		_ = os.RemoveAll(lockDir)
		return false, err
	}
	return true, nil
}

var unsafeLockKey = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func acquireRetentionLock(lockDir string, owner *retentionLockOwner, p lockProbes) (bool, error) {
	for attempt := 0; attempt < 4; attempt++ {
		ok, err := createLockDirectory(lockDir, owner)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
		stale, token := staleLock(lockDir, owner.StartedAt, p)
		if !stale {
			return false, nil
		}
		if token == "" {
			token = owner.Token
		}
		tombstone := lockDir + ".stale-" + unsafeLockKey.ReplaceAllString(token, "-")
		if err := os.Rename(lockDir, tombstone); err != nil {
			if isNotFoundErr(err) || pathExists(tombstone) {
				continue
			}
			return false, err
		}
	}
	return false, nil
}

func releaseRetentionLock(lockDir, token string) {
	if o := parseLockOwner(lockDir); o == nil || o.Token != token {
		return
	}
	_ = os.RemoveAll(lockDir)
}

var processStartedAt = time.Now()

func computeProcessStartIdentity(pid int) string {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return ""
		}
		stat := string(b)
		end := strings.LastIndex(stat, ")")
		if end == -1 {
			return ""
		}
		fields := strings.Fields(stat[end+1:])
		if len(fields) > 19 && fields[19] != "" {
			return "linux:" + fields[19]
		}
	case "windows":
		out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter "ProcessId=%d").CreationDate`, pid)).Output()
		if started := strings.TrimSpace(string(out)); err == nil && started != "" {
			return "win:" + started
		}
	}
	return ""
}

func retentionProcessAlive(pid int) *bool {
	err := defaultKill(pid, 0)
	t, f := true, false
	switch {
	case err == nil, errors.Is(err, errNotPermitted):
		return &t
	case errors.Is(err, errNoSuchProcess), errors.Is(err, syscall.ESRCH):
		return &f
	}
	return nil
}

// ── results ──

type resultCursorTarget struct {
	Type    string `json:"type"` // "result" or "pending"
	Key     string `json:"key,omitempty"`
	Session string `json:"session,omitempty"`
}

// RetentionResultCandidate is a result file a discovery pass picked (Path is joined on the commit side).
type RetentionResultCandidate struct {
	Name     string             `json:"name"`
	Relative string             `json:"relative"`
	Kind     string             `json:"kind"`
	Cursor   resultCursorTarget `json:"cursor"`
}

type resultCandidate struct {
	RetentionResultCandidate
	path string
}

// RetentionRunCandidate is a run directory a discovery pass picked.
type RetentionRunCandidate struct {
	Name     string `json:"name"`
	Relative string `json:"relative"`
}

// RetentionCursorOp is one change of the cursor a pass proposes.
type RetentionCursorOp struct {
	Type    string `json:"type"` // set, delete, set-pending, delete-pending
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	Session string `json:"session,omitempty"`
}

// RetentionDiscovery is what a discovery pass found.
type RetentionDiscovery struct {
	DiscoveryDurationMs int64
	RawReads            int
	SourceExhausted     map[string]bool
	RunCandidates       []RetentionRunCandidate
	ResultCandidates    []RetentionResultCandidate
	CursorOps           []RetentionCursorOp
}

// RetentionDiscoveryRequest is the input of a discovery pass.
type RetentionDiscoveryRequest struct {
	AsyncDirRoot, ResultsDir string
	Cursor                   map[string]any
	RunBudget, ResultBudget  int
}

func resultRunID(c resultCandidate, data jm) string {
	if s, ok := data["runId"].(string); ok {
		return s
	}
	if s, ok := data["id"].(string); ok {
		return s
	}
	if (c.Kind == "public" || c.Kind == "pending") && strings.HasSuffix(filepath.Base(c.path), ".json") {
		return strings.TrimSuffix(filepath.Base(c.path), ".json")
	}
	return ""
}

func resultTimestamp(data jm, p string) (float64, bool) {
	var logical []float64
	for _, k := range resultTimestampFields {
		v, has := data[k]
		if !has {
			continue
		}
		f, ok := finiteNum(v)
		if !ok {
			return 0, false
		}
		logical = append(logical, f)
	}
	info, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	ts := mtimeMs(info)
	for _, l := range logical {
		ts = math.Max(ts, l)
	}
	return ts, true
}

func terminalResult(data jm) bool {
	if data["success"] == true || data["success"] == false {
		return true
	}
	s, _ := data["state"].(string)
	return retentionTerminalStates[s]
}

func resultHasResumableSession(data jm) bool {
	if sf, ok := data["sessionFile"].(string); ok && existingRegularFile(sf) {
		return true
	}
	for _, r := range mapListStrict(data["results"]) {
		if sf, ok := r["sessionFile"].(string); ok && existingRegularFile(sf) {
			return true
		}
	}
	return false
}

// mapListStrict is the map elements of a JSON array; other elements are skipped.
func mapListStrict(v any) []jm {
	arr, _ := v.([]any)
	var out []jm
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func hasUnresolvedResultHandoff(data jm) bool {
	ph, has := data["parallelHandoff"]
	if !has {
		return false
	}
	pm, ok := ph.(map[string]any)
	if !ok {
		return true
	}
	p, _ := pm["path"].(string)
	return p == "" || unresolvedHandoff(p)
}

func completionMode(data jm) any {
	if c, ok := data["completion"].(map[string]any); ok {
		return c["mode"]
	}
	return nil
}

type resultSkipInput struct {
	candidate       resultCandidate
	data            jm
	asyncDirRoot    string
	resultsDir      string
	maintenanceRoot string
	cutoff          float64
	protected       map[string]bool
	waitRunIDs      map[string]bool
}

func resultSkipReason(in resultSkipInput) string {
	if in.data == nil {
		return "invalid-result"
	}
	runID := resultRunID(in.candidate, in.data)
	if runID == "" || !validRunID(runID) {
		return "invalid-result"
	}
	if runTombstoneMarkerBlocks(in.maintenanceRoot, runID) {
		return "run-tombstone-present"
	}
	if in.protected[runID] {
		return "runtime-reference"
	}
	if in.waitRunIDs[runID] {
		return "wait-reference"
	}
	if activeMarkerExists(in.asyncDirRoot, runID) {
		return "active-index"
	}
	if pathExists(filepath.Join(in.asyncDirRoot, runID)) {
		return "run-present"
	}
	if missionObserverIndexExists(in.resultsDir, runID) {
		return "mission-reference"
	}
	_, isStr1 := in.data["parentWorkflowRunId"].(string)
	_, isStr2 := in.data["workflowKey"].(string)
	if in.data["mode"] == "workflow" || completionMode(in.data) == "workflow" || isStr1 || isStr2 {
		return "workflow-reference"
	}
	if hasUnresolvedResultHandoff(in.data) {
		return "handoff-reference"
	}
	if resultHasResumableSession(in.data) {
		return "resumable"
	}
	if enc, ok := jsEncodeURIComponent(runID); in.candidate.Kind == "archive" && ok && pathExists(filepath.Join(in.resultsDir, "completion-replay", enc+".json")) {
		return "replay-reference"
	}
	if !terminalResult(in.data) && in.candidate.Kind != "replay" && in.candidate.Kind != "archive" && in.candidate.Kind != "tombstone" {
		return "non-terminal-result"
	}
	ts, ok := resultTimestamp(in.data, in.candidate.path)
	if !ok {
		return "unknown-age"
	}
	if ts > in.cutoff {
		return "recent"
	}
	return ""
}

func appendMaintenanceLog(root string, r *AsyncRetentionResult, now int64, deleted []string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if len(deleted) > asyncRetentionBatchSize {
		deleted = deleted[:asyncRetentionBatchSize]
	}
	if deleted == nil {
		deleted = []string{}
	}
	at := time.UnixMilli(now).UTC().Format("2006-01-02T15:04:05.000Z")
	line := logLine(at, r, deleted)
	f, err := os.OpenFile(filepath.Join(root, retentionLogName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func logLine(at string, r *AsyncRetentionResult, deleted []string) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	skipped := r.Skipped
	if skipped == nil {
		skipped = map[string]int{}
	}
	exhausted := r.SourceExhausted
	if exhausted == nil {
		exhausted = map[string]bool{}
	}
	return "{" + strings.Join([]string{
		`"at":` + enc(at), `"acquired":` + enc(r.Acquired), `"scanned":` + enc(r.Scanned), `"repairedRuns":` + enc(r.RepairedRuns), `"deletedRuns":` + enc(r.DeletedRuns),
		`"deletedResults":` + enc(r.DeletedResults), `"reapedTombstones":` + enc(r.ReapedTombstones), `"skipped":` + enc(skipped), `"deleted":` + enc(deleted),
		`"errors":` + enc(len(r.Errors)), `"rawReads":` + enc(r.RawReads), `"sourceExhausted":` + enc(exhausted), `"discoveryDurationMs":` + enc(r.DiscoveryDurationMs),
		`"commitDurationMs":` + enc(r.CommitDurationMs), `"cancelled":` + enc(r.Cancelled), `"workerFailed":` + enc(r.WorkerFailed), `"durationMs":` + enc(r.DurationMs),
	}, ",") + "}"
}

// ── discovery ──

func cmpUTF16(a, b string) int {
	ua, ub := jsUTF16(a), jsUTF16(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ua) < len(ub):
		return -1
	case len(ua) > len(ub):
		return 1
	}
	return 0
}

type dirCandidate struct{ relative, name string }

func insertSmallest(entries []dirCandidate, c dirCandidate, limit int) []dirCandidate {
	if limit <= 0 {
		return entries
	}
	idx := -1
	for i, e := range entries {
		if cmpUTF16(c.relative, e.relative) < 0 {
			idx = i
			break
		}
	}
	if idx == -1 {
		entries = append(entries, c)
	} else {
		entries = append(entries[:idx], append([]dirCandidate{c}, entries[idx:]...)...)
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

type dirWindow struct {
	entries       []dirCandidate
	rawReads      int
	exhausted     bool
	cursorCleared bool
}

// streamDirWindow reads dir once and keeps the `limit` smallest usable entries after the cursor (wrapping to the smallest when
// none is after it), so a pass never holds more than a batch in memory.
func streamDirWindow(dir string, limit int, after *string, relativePath func(name string) string, usable func(e fs.DirEntry) bool) (dirWindow, error) {
	if limit <= 0 {
		return dirWindow{exhausted: true}, nil
	}
	f, err := os.Open(dir)
	if err != nil {
		if isNotFoundErr(err) {
			return dirWindow{exhausted: true}, nil
		}
		return dirWindow{}, err
	}
	defer f.Close()
	var next, wrapped []dirCandidate
	raw := 0
	for {
		entries, err := f.ReadDir(256)
		for _, e := range entries {
			raw++
			if !usable(e) {
				continue
			}
			c := dirCandidate{relative: relativePath(e.Name()), name: e.Name()}
			wrapped = insertSmallest(wrapped, c, limit)
			if after == nil || cmpUTF16(c.relative, *after) > 0 {
				next = insertSmallest(next, c, limit)
			}
		}
		if err != nil {
			break // io.EOF
		}
	}
	cleared := after != nil && len(next) == 0
	out := next
	if cleared {
		out = wrapped
	}
	return dirWindow{entries: out, rawReads: raw, exhausted: true, cursorCleared: cleared}, nil
}

// discoverRetention is the discovery worker's pass: bounded windows over the runs and each result source.
func discoverRetention(req RetentionDiscoveryRequest) (*RetentionDiscovery, error) {
	started := time.Now()
	cursor := jm{}
	for k, v := range req.Cursor {
		cursor[k] = v
	}
	if p := cursorPending(req.Cursor); p != nil {
		clone := map[string]any{}
		for k, v := range p {
			clone[k] = v
		}
		cursor["resultPendingAfterBySession"] = clone
	}
	raw, exhausted := 0, map[string]bool{}
	record := func(source string, w dirWindow) {
		raw += w.rawReads
		exhausted[source] = w.exhausted
	}
	optStr := func(key string) *string {
		if s, ok := cursorStr(cursor, key); ok {
			return &s
		}
		return nil
	}
	nameOf := func(n string) string { return n }
	runScan, err := streamDirWindow(req.AsyncDirRoot, req.RunBudget, optStr("runAfter"), nameOf, func(e fs.DirEntry) bool { return e.IsDir() && e.Name() != activeRunIndexDir })
	if err != nil {
		return nil, err
	}
	record("runs", runScan)
	if runScan.cursorCleared {
		delete(cursor, "runAfter")
	}
	runCandidates := []RetentionRunCandidate{}
	for i, c := range runScan.entries {
		if i >= req.RunBudget {
			break
		}
		runCandidates = append(runCandidates, RetentionRunCandidate{Name: c.name, Relative: c.relative})
		cursor["runAfter"] = c.relative
	}

	resultCandidates := []RetentionResultCandidate{}
	sourceLimit := max(1, (req.ResultBudget+3)/4)
	addFiles := func(dir, kind string, target resultCursorTarget, source string, budget int) (int, error) {
		remaining := min(budget, req.ResultBudget-len(resultCandidates))
		if remaining <= 0 {
			return 0, nil
		}
		before := len(resultCandidates)
		var after *string
		if target.Type == "pending" {
			if s, ok := cursorPending(cursor)[target.Session].(string); ok {
				after = &s
			}
		} else {
			after = optStr(target.Key)
		}
		scan, err := streamDirWindow(dir, remaining, after, func(n string) string {
			rel, _ := filepath.Rel(req.ResultsDir, filepath.Join(dir, n))
			return rel
		}, func(e fs.DirEntry) bool {
			return e.Type().IsRegular() && (strings.HasPrefix(e.Name(), resultTombstonePref) || strings.HasSuffix(e.Name(), ".json"))
		})
		if err != nil {
			return 0, err
		}
		record(source, scan)
		if scan.cursorCleared {
			if target.Type == "pending" {
				delete(cursorPending(cursor), target.Session)
			} else {
				delete(cursor, target.Key)
			}
		}
		for i, c := range scan.entries {
			if i >= remaining {
				break
			}
			k := kind
			if strings.HasPrefix(c.name, resultTombstonePref) {
				k = "tombstone"
			}
			resultCandidates = append(resultCandidates, RetentionResultCandidate{Name: c.name, Relative: c.relative, Kind: k, Cursor: target})
			if target.Type == "pending" {
				p := cursorPending(cursor)
				if p == nil {
					p = map[string]any{}
					cursor["resultPendingAfterBySession"] = p
				}
				p[target.Session] = c.relative
			} else {
				cursor[target.Key] = c.relative
			}
		}
		return len(resultCandidates) - before, nil
	}
	if _, err := addFiles(req.ResultsDir, "public", resultCursorTarget{Type: "result", Key: "resultPublicAfter"}, "results.public", sourceLimit); err != nil {
		return nil, err
	}
	pendingRoot := filepath.Join(req.ResultsDir, "result-pending")
	pendingRemaining := min(req.ResultBudget, sourceLimit, req.ResultBudget-len(resultCandidates))
	pendingScan, err := streamDirWindow(pendingRoot, pendingRemaining, optStr("pendingSessionAfter"), nameOf, func(e fs.DirEntry) bool { return e.IsDir() })
	if err != nil {
		return nil, err
	}
	record("results.pendingSessions", pendingScan)
	live := map[string]bool{}
	for _, e := range pendingScan.entries {
		live[e.relative] = true
	}
	if p := cursorPending(cursor); p != nil {
		pruned := 0
		for _, session := range sortedKeys(p) {
			if pruned >= req.ResultBudget {
				break
			}
			if !live[session] && !pathExists(filepath.Join(pendingRoot, session)) {
				delete(p, session)
				pruned++
			}
		}
		if len(p) == 0 {
			delete(cursor, "resultPendingAfterBySession")
		}
	}
	if pendingScan.cursorCleared {
		delete(cursor, "pendingSessionAfter")
	}
	sessions := pendingScan.entries
	if len(sessions) > pendingRemaining {
		sessions = sessions[:pendingRemaining]
	}
	pendingFileBudget := min(sourceLimit, req.ResultBudget-len(resultCandidates))
	for _, session := range sessions {
		if pendingFileBudget <= 0 {
			break
		}
		cursor["pendingSessionAfter"] = session.relative
		n, err := addFiles(filepath.Join(pendingRoot, session.name), "pending", resultCursorTarget{Type: "pending", Session: session.relative}, "results.pending."+session.name, pendingFileBudget)
		if err != nil {
			return nil, err
		}
		pendingFileBudget -= n
	}
	if _, err := addFiles(filepath.Join(req.ResultsDir, "completion-replay"), "replay", resultCursorTarget{Type: "result", Key: "resultReplayAfter"}, "results.replay", sourceLimit); err != nil {
		return nil, err
	}
	if _, err := addFiles(filepath.Join(req.ResultsDir, "output-archives"), "archive", resultCursorTarget{Type: "result", Key: "resultArchiveAfter"}, "results.archive", sourceLimit); err != nil {
		return nil, err
	}

	ops := []RetentionCursorOp{}
	for _, key := range []string{"runAfter", "resultPublicAfter", "resultReplayAfter", "resultArchiveAfter", "pendingSessionAfter"} {
		oldV, hadOld := cursorStr(req.Cursor, key)
		newV, hasNew := cursorStr(cursor, key)
		if hadOld == hasNew && oldV == newV {
			continue
		}
		if !hasNew {
			ops = append(ops, RetentionCursorOp{Type: "delete", Key: key})
		} else {
			ops = append(ops, RetentionCursorOp{Type: "set", Key: key, Value: newV})
		}
	}
	oldP, newP := cursorPending(req.Cursor), cursorPending(cursor)
	sessionsSeen := map[string]bool{}
	var all []string
	for _, m := range []map[string]any{oldP, newP} {
		for k := range m {
			if !sessionsSeen[k] {
				sessionsSeen[k] = true
				all = append(all, k)
			}
		}
	}
	sort.Strings(all)
	for _, s := range all {
		o, hadO := oldP[s]
		n, hasN := newP[s]
		if hadO == hasN && o == n {
			continue
		}
		if !hasN {
			ops = append(ops, RetentionCursorOp{Type: "delete-pending", Session: s})
		} else {
			ops = append(ops, RetentionCursorOp{Type: "set-pending", Session: s, Value: fmt.Sprint(n)})
		}
	}
	return &RetentionDiscovery{DiscoveryDurationMs: max(0, time.Since(started).Milliseconds()), RawReads: raw, SourceExhausted: exhausted, RunCandidates: runCandidates, ResultCandidates: resultCandidates, CursorOps: ops}, nil
}

func safeRelative(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".." {
			return false
		}
	}
	return true
}

// validateDiscovery is parseDiscoveryResult: what a discovery returns decides which paths are deleted, so it is checked even
// though the pass runs in-process.
func validateDiscovery(d *RetentionDiscovery, runBudget, resultBudget int) error {
	if d == nil {
		return errors.New("Retention discovery returned nothing.")
	}
	if d.DiscoveryDurationMs < 0 {
		return errors.New("Retention discovery returned an invalid duration.")
	}
	if d.RawReads < 0 {
		return errors.New("Retention discovery returned an invalid raw-read count.")
	}
	if len(d.RunCandidates) > runBudget {
		return errors.New("Retention discovery exceeded the run budget.")
	}
	if len(d.ResultCandidates) > resultBudget {
		return errors.New("Retention discovery exceeded the result budget.")
	}
	if len(d.CursorOps) > runBudget+resultBudget+8 {
		return errors.New("Retention discovery returned invalid cursor operations.")
	}
	for _, c := range d.RunCandidates {
		if !validRunID(c.Name) || c.Relative != c.Name {
			return errors.New("Retention discovery returned an unsafe run candidate.")
		}
	}
	kinds := map[string]bool{"public": true, "pending": true, "replay": true, "archive": true, "tombstone": true}
	for _, c := range d.ResultCandidates {
		if filepath.Base(c.Name) != c.Name || !safeRelative(c.Relative) || filepath.Base(c.Relative) != c.Name || !kinds[c.Kind] {
			return errors.New("Retention discovery returned an unsafe result candidate.")
		}
		switch c.Cursor.Type {
		case "pending":
			if !validRunID(c.Cursor.Session) || filepath.Dir(c.Relative) != filepath.Join("result-pending", c.Cursor.Session) {
				return errors.New("Retention discovery returned an invalid pending candidate.")
			}
		case "result":
			dirs := map[string]string{"resultPublicAfter": ".", "resultReplayAfter": "completion-replay", "resultArchiveAfter": "output-archives"}
			want, ok := dirs[c.Cursor.Key]
			if !ok {
				return errors.New("Retention discovery returned an invalid result cursor.")
			}
			if filepath.Dir(c.Relative) != want {
				return errors.New("Retention discovery returned a result from the wrong source.")
			}
		default:
			return errors.New("Retention discovery returned an invalid result cursor target.")
		}
	}
	scalar := map[string]bool{"runAfter": true, "resultPublicAfter": true, "resultReplayAfter": true, "resultArchiveAfter": true, "pendingSessionAfter": true}
	for _, op := range d.CursorOps {
		switch op.Type {
		case "set", "delete":
			if !scalar[op.Key] || (op.Type == "set" && !safeRelative(op.Value)) {
				return errors.New("Retention discovery returned an invalid scalar cursor operation.")
			}
		case "set-pending", "delete-pending":
			if !validRunID(op.Session) || (op.Type == "set-pending" && !safeRelative(op.Value)) {
				return errors.New("Retention discovery returned an invalid pending cursor operation.")
			}
		default:
			return errors.New("Retention discovery returned an unknown cursor operation.")
		}
	}
	return nil
}

var errRetentionCancelled = errors.New("Retention discovery was cancelled.")

// runRetentionDiscovery runs discovery off the commit goroutine; closing cancel abandons it.
func runRetentionDiscovery(o AsyncRetentionOptions, req RetentionDiscoveryRequest) (*RetentionDiscovery, error) {
	discover := o.Discover
	if discover == nil {
		discover = discoverRetention
	}
	select {
	case <-o.Cancel:
		return nil, errRetentionCancelled
	default:
	}
	type reply struct {
		d   *RetentionDiscovery
		err error
	}
	ch := make(chan reply, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- reply{nil, fmt.Errorf("Retention discovery failed: %v", r)}
			}
		}()
		d, err := discover(req)
		ch <- reply{d, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if err := validateDiscovery(r.d, req.RunBudget, req.ResultBudget); err != nil {
			return nil, err
		}
		return r.d, nil
	case <-o.Cancel:
		return nil, errRetentionCancelled
	}
}

func applyCursorOperations(cursor jm, ops []RetentionCursorOp) {
	for _, op := range ops {
		switch op.Type {
		case "set":
			cursor[op.Key] = op.Value
		case "delete":
			delete(cursor, op.Key)
		case "set-pending":
			p := cursorPending(cursor)
			if p == nil {
				p = map[string]any{}
				cursor["resultPendingAfterBySession"] = p
			}
			p[op.Session] = op.Value
		case "delete-pending":
			if p := cursorPending(cursor); p != nil {
				delete(p, op.Session)
				if len(p) == 0 {
					delete(cursor, "resultPendingAfterBySession")
				}
			}
		}
	}
}

func increment(m map[string]int, key string) { m[key]++ }

func toSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// cleanupAsyncRetention is one bounded pass of the retention cleaner.
func cleanupAsyncRetention(o AsyncRetentionOptions) (*AsyncRetentionResult, error) {
	startedWall := time.Now()
	now := o.Now
	if now == nil {
		now = nowMs
	}
	currentTime := now()
	retention := int64(retentionMs)
	if o.RetentionMs != nil {
		retention = *o.RetentionMs
	}
	grace := int64(asyncRetentionTombstoneGraceMs)
	if o.TombstoneGrace != nil {
		grace = *o.TombstoneGrace
	}
	batch := asyncRetentionBatchSize
	if o.BatchSize != nil {
		batch = min(asyncRetentionBatchSize, max(1, *o.BatchSize))
	}
	randomID := o.RandomID
	if randomID == nil {
		randomID = randomUUID
	}
	root := o.MaintenanceRoot
	if root == "" {
		root = filepath.Dir(o.AsyncDirRoot)
	}
	lockDir := filepath.Join(root, retentionLockName)
	lockToken := randomID()
	pid := os.Getpid()
	if o.PID != nil {
		pid = *o.PID
	}
	host := o.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	getIdentity := o.GetProcessStartIdentity
	if getIdentity == nil {
		getIdentity = computeProcessStartIdentity
	}
	identity := o.ProcessStartIdentity
	if identity == "" {
		identity = getIdentity(pid)
	}
	if identity == "" && pid == os.Getpid() {
		identity = fmt.Sprintf("runtime:%d", processStartedAt.UnixMilli())
	}
	owner := &retentionLockOwner{Version: 1, Token: lockToken, PID: pid, Hostname: host, StartedAt: currentTime, ProcessStartIdentity: identity}
	alive := o.IsProcessAlive
	if alive == nil {
		alive = retentionProcessAlive
	}
	probes := lockProbes{hostname: host, isProcessAlive: alive, getProcessStartIdentity: getIdentity}
	res := &AsyncRetentionResult{Skipped: map[string]int{}, Errors: []string{}, SourceExhausted: map[string]bool{}}
	var deletedIDs []string
	lstat := o.Lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	var commitStartedAt *time.Time
	cursorCommitUnsafe := false
	finish := func() (*AsyncRetentionResult, error) {
		if commitStartedAt != nil {
			res.CommitDurationMs = max(0, time.Since(*commitStartedAt).Milliseconds())
		}
		res.DurationMs = max(0, time.Since(startedWall).Milliseconds())
		return res, appendMaintenanceLog(root, res, currentTime, deletedIDs)
	}
	cancelled := func() bool {
		select {
		case <-o.Cancel:
			res.Cancelled = true
			increment(res.Skipped, "cancelled")
			return true
		default:
			return false
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	ok, err := acquireRetentionLock(lockDir, owner, probes)
	if err != nil {
		return nil, err
	}
	if !ok {
		increment(res.Skipped, "lock-busy")
		return finish()
	}
	res.Acquired = true
	defer releaseRetentionLock(lockDir, lockToken)
	if cancelled() {
		return finish()
	}
	var protectedIDs []string
	if o.ProtectedRunIDs != nil {
		protectedIDs = o.ProtectedRunIDs()
	}
	protected := toSet(protectedIDs)
	cutoff := float64(currentTime - retention)
	cursor := readRetentionCursor(root)
	runBudget := (batch + 1) / 2
	resultBudget := batch - runBudget
	discoveryStarted := time.Now()
	req := RetentionDiscoveryRequest{AsyncDirRoot: o.AsyncDirRoot, ResultsDir: o.ResultsDir, Cursor: cursor, RunBudget: runBudget, ResultBudget: resultBudget}
	discovery, derr := runRetentionDiscovery(o, req)
	res.DiscoveryDurationMs = max(0, time.Since(discoveryStarted).Milliseconds())
	if derr != nil {
		if errors.Is(derr, errRetentionCancelled) {
			res.Cancelled = true
			increment(res.Skipped, "cancelled")
		} else {
			res.WorkerFailed = true
			increment(res.Skipped, "worker-failure")
			res.Errors = append(res.Errors, compactError(derr))
		}
		return finish()
	}
	res.RawReads, res.SourceExhausted = discovery.RawReads, discovery.SourceExhausted
	started := time.Now()
	commitStartedAt = &started
	if cancelled() {
		return finish()
	}
	if o2 := parseLockOwner(lockDir); o2 == nil || o2.Token != lockToken {
		increment(res.Skipped, "lock-owner-changed")
		return finish()
	}
	waitDir := o.WaitSubscriptionDir
	if waitDir == "" {
		waitDir = filepath.Join(root, "wait-subscriptions")
	}
	// This safety scan stays on the commit goroutine. Wait records are the active, swept subscription set; each destructive
	// action re-reads it to close the race with a newly armed wait.
	waitIDs, safe := parseWaitRunIDs(waitDir)
	if !safe {
		increment(res.Skipped, "wait-references-unknown")
		return finish()
	}
	for _, entry := range discovery.RunCandidates {
		if cancelled() {
			return finish()
		}
		res.Scanned++
		commitRun(o, res, root, entry, protected, waitIDs, waitDir, cutoff, currentTime, grace, now, randomID, lstat, &deletedIDs, &cursorCommitUnsafe)
	}
	for _, dc := range discovery.ResultCandidates {
		if cancelled() {
			return finish()
		}
		res.Scanned++
		commitResult(o, res, root, resultCandidate{RetentionResultCandidate: dc, path: filepath.Join(o.ResultsDir, dc.Relative)}, protected, waitIDs, waitDir, cutoff, currentTime, grace, randomID, lstat, &deletedIDs, &cursorCommitUnsafe)
	}
	if cursorCommitUnsafe {
		increment(res.Skipped, "commit-failure")
		return finish()
	}
	if cancelled() {
		return finish()
	}
	if o2 := parseLockOwner(lockDir); o2 == nil || o2.Token != lockToken {
		increment(res.Skipped, "lock-owner-changed")
		return finish()
	}
	applyCursorOperations(cursor, discovery.CursorOps)
	if err := asyncWriteJSON(filepath.Join(root, retentionCursorName), cursor); err != nil {
		return nil, err
	}
	return finish()
}

func commitRun(o AsyncRetentionOptions, res *AsyncRetentionResult, root string, entry RetentionRunCandidate, protected, waitIDs map[string]bool, waitDir string, cutoff float64, currentTime, grace int64,
	now func() int64, randomID func() string, lstat func(string) (fs.FileInfo, error), deletedIDs *[]string, unsafe *bool) {
	runDir := filepath.Join(o.AsyncDirRoot, entry.Name)
	mutated := false
	fail := func(err error) {
		if mutated {
			*unsafe = true
		}
		if !isNotFoundErr(err) {
			res.Errors = append(res.Errors, compactError(err))
		}
	}
	info, err := lstat(runDir)
	if err != nil {
		fail(err)
		return
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		increment(res.Skipped, "unsafe-run-path")
		return
	}
	status := retentionReadStatus(runDir)
	if status != nil && status["state"] == "running" {
		id, _ := status["runId"].(string)
		if validRunID(id) && filepath.Base(runDir) == id && !protected[id] {
			rec, rerr := reconcileAsyncRun(runDir, ReconcileOptions{ResultsDir: o.ResultsDir, Kill: o.ReconcileKill, Now: now})
			if rerr != nil {
				fail(rerr)
				return
			}
			status = retentionReadStatus(runDir)
			if rec.Repaired {
				res.RepairedRuns++
			}
		}
	}
	in := runSkipInput{runDir: runDir, status: status, asyncDirRoot: o.AsyncDirRoot, resultsDir: o.ResultsDir, cutoff: cutoff, protected: protected, waitRunIDs: waitIDs}
	if reason := runSkipReason(in); reason != "" {
		increment(res.Skipped, reason)
		return
	}
	runID := status["runId"].(string)
	if strings.HasPrefix(entry.Name, runTombstonePrefix) {
		if !runTombstoneMarkerMatches(root, runID, runDir) {
			increment(res.Skipped, "run-tombstone-marker")
			return
		}
		if float64(currentTime)-mtimeMs(info) < float64(grace) {
			increment(res.Skipped, "tombstone-grace")
			return
		}
		fresh, freshSafe := parseWaitRunIDs(waitDir)
		final := "wait-references-unknown"
		if freshSafe {
			in.waitRunIDs = fresh
			final = runSkipReason(in)
		}
		if final != "" {
			increment(res.Skipped, "recheck-"+final)
			return
		}
		mutated = true
		if err := os.RemoveAll(runDir); err != nil {
			fail(err)
			return
		}
		removeRunTombstoneMarker(root, runID)
		res.ReapedTombstones++
		*deletedIDs = append(*deletedIDs, "run-tombstone:"+runID)
		return
	}
	tombstone := filepath.Join(o.AsyncDirRoot, runTombstonePrefix+randomID())
	if err := writeRunTombstoneMarker(root, runID, tombstone, currentTime); err != nil {
		fail(err)
		return
	}
	mutated = true
	if err := os.Rename(runDir, tombstone); err != nil {
		removeRunTombstoneMarker(root, runID)
		mutated = false
		fail(err)
		return
	}
	recheck := retentionReadStatus(tombstone)
	fresh, freshSafe := parseWaitRunIDs(waitDir)
	reason := "wait-references-unknown"
	if freshSafe {
		reason = runSkipReason(runSkipInput{runDir: tombstone, status: recheck, asyncDirRoot: o.AsyncDirRoot, resultsDir: o.ResultsDir, cutoff: cutoff, protected: protected, waitRunIDs: fresh})
	}
	if reason != "" {
		if !pathExists(runDir) {
			if err := os.Rename(tombstone, runDir); err != nil {
				fail(err)
				return
			}
			mutated = false
		}
		removeRunTombstoneMarker(root, runID)
		if mutated {
			*unsafe = true
		}
		increment(res.Skipped, "recheck-"+reason)
		return
	}
	if err := os.RemoveAll(tombstone); err != nil {
		fail(err)
		return
	}
	removeRunTombstoneMarker(root, runID)
	res.DeletedRuns++
	*deletedIDs = append(*deletedIDs, "run:"+runID)
}

func commitResult(o AsyncRetentionOptions, res *AsyncRetentionResult, root string, c resultCandidate, protected, waitIDs map[string]bool, waitDir string, cutoff float64, currentTime, grace int64,
	randomID func() string, lstat func(string) (fs.FileInfo, error), deletedIDs *[]string, unsafe *bool) {
	mutated := false
	fail := func(err error) {
		if mutated {
			*unsafe = true
		}
		if !isNotFoundErr(err) {
			res.Errors = append(res.Errors, compactError(err))
		}
	}
	info, err := lstat(c.path)
	if err != nil {
		fail(err)
		return
	}
	if !info.Mode().IsRegular() {
		increment(res.Skipped, "unsafe-result-path")
		return
	}
	data := readLooseJSON(c.path)
	in := resultSkipInput{candidate: c, data: data, asyncDirRoot: o.AsyncDirRoot, resultsDir: o.ResultsDir, maintenanceRoot: root, cutoff: cutoff, protected: protected, waitRunIDs: waitIDs}
	if reason := resultSkipReason(in); reason != "" {
		increment(res.Skipped, reason)
		return
	}
	if c.Kind == "tombstone" {
		if float64(currentTime)-mtimeMs(info) < float64(grace) {
			increment(res.Skipped, "tombstone-grace")
			return
		}
		fresh, freshSafe := parseWaitRunIDs(waitDir)
		final := "wait-references-unknown"
		if freshSafe {
			in.waitRunIDs = fresh
			final = resultSkipReason(in)
		}
		if final != "" {
			increment(res.Skipped, "recheck-"+final)
			return
		}
		if err := os.Remove(c.path); err != nil {
			fail(err)
			return
		}
		mutated = true
		res.ReapedTombstones++
		*deletedIDs = append(*deletedIDs, "result-tombstone:"+resultRunID(c, data))
		return
	}
	tombstone := filepath.Join(filepath.Dir(c.path), resultTombstonePref+c.Kind+"-"+randomID())
	if err := os.Rename(c.path, tombstone); err != nil {
		fail(err)
		return
	}
	mutated = true
	tc := c
	tc.path, tc.Kind = tombstone, "tombstone"
	recheckData := readLooseJSON(tombstone)
	fresh, freshSafe := parseWaitRunIDs(waitDir)
	reason := "wait-references-unknown"
	if freshSafe {
		reason = resultSkipReason(resultSkipInput{candidate: tc, data: recheckData, asyncDirRoot: o.AsyncDirRoot, resultsDir: o.ResultsDir, maintenanceRoot: root, cutoff: cutoff, protected: protected, waitRunIDs: fresh})
	}
	if reason != "" {
		if !pathExists(c.path) {
			if err := os.Rename(tombstone, c.path); err != nil {
				fail(err)
				return
			}
			mutated = false
		}
		if mutated {
			*unsafe = true
		}
		increment(res.Skipped, "recheck-"+reason)
		return
	}
	if err := os.Remove(tombstone); err != nil {
		fail(err)
		return
	}
	res.DeletedResults++
	*deletedIDs = append(*deletedIDs, "result:"+resultRunID(c, data))
}
