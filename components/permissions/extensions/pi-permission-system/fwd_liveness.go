package pi_permission_system

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Port of src/authority/forwarding-liveness.ts: is anyone draining a forwarded-permission inbox?
//
// The in-process answer is the ServingSessionRegistry; a child spawned as a separate process shares no memory with its parent, so
// the serving session publishes a heartbeat file (one record per serving session, refreshed while it polls, withdrawn when it
// stops). The filesystem is the only channel two processes share, and the only one this port can offer across processes.

// ServingHeartbeatRefreshMs is how often a serving session rewrites its heartbeat: four poll ticks.
const ServingHeartbeatRefreshMs = 4 * PermissionForwardingPollIntervalMs

// ServingHeartbeatStaleMs is how long a heartbeat may go unrefreshed before its writer is presumed gone: five refreshes.
const ServingHeartbeatStaleMs = 5 * ServingHeartbeatRefreshMs

// ServingHeartbeat is what a serving session publishes while it drains its forwarded-permission inbox.
type ServingHeartbeat struct {
	SessionID string
	// PID is the serving process, so a killed session is detectable without waiting out staleness.
	PID       int
	UpdatedAt float64
}

// HeartbeatState is how a session's heartbeat reads right now: "alive" (someone is draining the inbox), "absent" (exited, never
// served, or runs a version that does not publish), "stale" (the process survives but stopped polling) or "dead_pid" (killed).
type HeartbeatState string

// The four heartbeat states.
const (
	HeartbeatAlive   HeartbeatState = "alive"
	HeartbeatAbsent  HeartbeatState = "absent"
	HeartbeatStale   HeartbeatState = "stale"
	HeartbeatDeadPID HeartbeatState = "dead_pid"
)

// HeartbeatReader is the read side of the heartbeat channel, consumed by a forwarding child.
type HeartbeatReader interface {
	Read(sessionID string) HeartbeatState
	// ServingIDs lists every session whose record reads as alive, for the abandonment diagnostic.
	ServingIDs() []string
}

// TargetServingLookup is the query-side seam: is the session a forwarding target names being drained? It is keyed on the target
// because the answer depends on how the target was resolved.
type TargetServingLookup interface {
	// IsServing answers serving, and known=false when the target carries no signal (the original's null).
	IsServing(target PermissionForwardingTarget) (serving, known bool)
	// Describe is what the judge observed, for the review entry a child writes when it gives up.
	Describe(target PermissionForwardingTarget) ServingObservation
}

// ServingObservation is what answered a liveness question, and what it saw.
type ServingObservation struct {
	// Channel is "registry", "heartbeat", or "none" for a lookup that consulted none.
	Channel string
	// State is the heartbeat state behind a "heartbeat" answer; "" (null) on the other channels.
	State      HeartbeatState
	ServingIDs []string
}

// ForwardingLivenessJudgeDeps are the constructor config of ForwardingLivenessJudge.
type ForwardingLivenessJudgeDeps struct {
	// Registry answers for a target the requester shares a process with.
	Registry ServingLookup
	// Heartbeats answers for a target in another process.
	Heartbeats HeartbeatReader
}

// ForwardingLivenessJudge routes a liveness question to the channel that can answer it, keyed on PermissionForwardingTarget.Source.
type ForwardingLivenessJudge struct{ deps ForwardingLivenessJudgeDeps }

// NewForwardingLivenessJudge is `new ForwardingLivenessJudge(deps)`.
func NewForwardingLivenessJudge(deps ForwardingLivenessJudgeDeps) *ForwardingLivenessJudge {
	return &ForwardingLivenessJudge{deps: deps}
}

// IsServing implements TargetServingLookup. The judge always knows: every target names a channel.
func (j *ForwardingLivenessJudge) IsServing(target PermissionForwardingTarget) (bool, bool) {
	switch target.Source {
	case TargetSourceRegistry:
		return j.deps.Registry.IsServing(target.SessionID), true
	case TargetSourceEnv:
		return j.deps.Heartbeats.Read(target.SessionID) == HeartbeatAlive, true
	}
	return false, false
}

// Describe implements TargetServingLookup.
func (j *ForwardingLivenessJudge) Describe(target PermissionForwardingTarget) ServingObservation {
	switch target.Source {
	case TargetSourceRegistry:
		return ServingObservation{Channel: "registry", ServingIDs: j.deps.Registry.ServingIDs()}
	case TargetSourceEnv:
		return ServingObservation{Channel: "heartbeat", State: j.deps.Heartbeats.Read(target.SessionID), ServingIDs: j.deps.Heartbeats.ServingIDs()}
	}
	return ServingObservation{Channel: "none"}
}

const servingHeartbeatDirectoryName = "serving"

// ServingHeartbeatDir is where serving heartbeats live: beside the sessions/ tree, never inside it, so liveness stays disjoint from
// the request/response cleanup whose removal ordering produced the ENOENT write loop (#398).
func ServingHeartbeatDir(forwardingDir string) string {
	return filepath.Join(forwardingDir, servingHeartbeatDirectoryName)
}

// ServingHeartbeatPath is the heartbeat record for sessionID, under ServingHeartbeatDir.
func ServingHeartbeatPath(forwardingDir, sessionID string) string {
	return filepath.Join(ServingHeartbeatDir(forwardingDir), EncodeSessionIDForPath(sessionID)+".json")
}

// ServingHeartbeatStoreDeps are the constructor config of ServingHeartbeatStore.
type ServingHeartbeatStoreDeps struct {
	ForwardingDir string
	Logger        DebugReviewLogger
	// Now is the clock in Unix milliseconds, injected so the refresh throttle and staleness are testable without sleeping; nil
	// reads the wall clock.
	Now func() int64
	// PID is the process to record; 0 records this process. Injected so a test can publish a pid it controls.
	PID int
	// IsProcessAlive is injected so a test can decide which pids are running; nil probes the operating system.
	IsProcessAlive func(pid int) bool
}

// ServingHeartbeatStore publishes this session's serving heartbeat to the filesystem and reads other sessions' records. It
// satisfies ServingAnnouncer (so ForwardingManager announces to both channels through one collaborator) and HeartbeatReader.
//
// MarkServing is idempotent and internally throttled, so the caller may announce on every poll tick. Nothing here panics or returns
// an error: it runs from a timer, and a filesystem failure must degrade to the pre-existing timeout rather than break the poll loop.
// Safe for concurrent use.
type ServingHeartbeatStore struct {
	forwardingDir  string
	logger         DebugReviewLogger
	now            func() int64
	pid            int
	isProcessAlive func(pid int) bool

	mu                  sync.Mutex
	published           *publishedHeartbeat
	hasSweptDeadRecords bool
}

type publishedHeartbeat struct {
	sessionID string
	at        int64
}

// NewServingHeartbeatStore is `new ServingHeartbeatStore(deps)`.
func NewServingHeartbeatStore(deps ServingHeartbeatStoreDeps) *ServingHeartbeatStore {
	s := &ServingHeartbeatStore{
		forwardingDir: deps.ForwardingDir, logger: deps.Logger, now: deps.Now, pid: deps.PID, isProcessAlive: deps.IsProcessAlive,
	}
	if s.now == nil {
		s.now = func() int64 { return time.Now().UnixMilli() }
	}
	if s.pid == 0 {
		s.pid = os.Getpid()
	}
	if s.isProcessAlive == nil {
		s.isProcessAlive = isRunningProcess
	}
	return s
}

// MarkServing publishes (or refreshes) sessionID's heartbeat. Throttled; never fails out.
func (s *ServingHeartbeatStore) MarkServing(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.now()
	if s.isThrottled(sessionID, at) {
		return
	}
	directory := ServingHeartbeatDir(s.forwardingDir)
	if !EnsureDirectoryExists(s.logger, directory, "permission forwarding serving heartbeat") {
		return
	}
	s.sweepDeadRecordsOnce()

	heartbeat := newObject()
	heartbeat.set("sessionId", sessionID)
	heartbeat.set("pid", float64(s.pid))
	heartbeat.set("updatedAt", float64(at))
	if err := WriteJSONFileAtomic(s.logger, ServingHeartbeatPath(s.forwardingDir, sessionID), heartbeat); err != nil {
		LogPermissionForwardingError(s.logger, "Failed to publish the serving heartbeat for session '"+sessionID+"'", err)
		return
	}
	s.published = &publishedHeartbeat{sessionID: sessionID, at: at}
}

// ClearServing withdraws sessionID's heartbeat, leaving the directory for its siblings.
func (s *ServingHeartbeatStore) ClearServing(sessionID string) {
	s.mu.Lock()
	if s.published != nil && s.published.sessionID == sessionID {
		s.published = nil
	}
	s.mu.Unlock()
	SafeDeleteFile(s.logger, ServingHeartbeatPath(s.forwardingDir, sessionID), "permission forwarding serving heartbeat")
}

// Read is how sessionID's heartbeat reads right now.
func (s *ServingHeartbeatStore) Read(sessionID string) HeartbeatState {
	record := s.readRecord(ServingHeartbeatPath(s.forwardingDir, sessionID))
	if record == nil {
		return HeartbeatAbsent
	}
	return s.classify(*record)
}

// ServingIDs lists every session whose record reads as alive.
func (s *ServingHeartbeatStore) ServingIDs() []string {
	ids := []string{}
	for _, entry := range s.listRecords() {
		if entry.record != nil && s.classify(*entry.record) == HeartbeatAlive {
			ids = append(ids, entry.record.SessionID)
		}
	}
	return ids
}

// sweepDeadRecordsOnce deletes the records of processes that are provably gone, once per store. Only a dead pid (or an unusable
// record) is proof: a record that is merely stale belongs to a process that still exists. Caller holds s.mu.
func (s *ServingHeartbeatStore) sweepDeadRecordsOnce() {
	if s.hasSweptDeadRecords {
		return
	}
	s.hasSweptDeadRecords = true
	for _, entry := range s.listRecords() {
		if entry.record != nil && s.isProcessAlive(entry.record.PID) {
			continue
		}
		SafeDeleteFile(s.logger, entry.path, "abandoned permission forwarding serving heartbeat")
	}
}

type heartbeatEntry struct {
	path   string
	record *ServingHeartbeat
}

// listRecords is every published record, paired with its path; unusable ones read as nil.
func (s *ServingHeartbeatStore) listRecords() []heartbeatEntry {
	directory := ServingHeartbeatDir(s.forwardingDir)
	names, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var out []heartbeatEntry
	for _, n := range names {
		if !strings.HasSuffix(n.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, n.Name())
		out = append(out, heartbeatEntry{path: path, record: s.readRecord(path)})
	}
	return out
}

// readRecord reads a record, or nil when it is missing or unusable. Silent by design: a forwarding child calls this on every poll
// tick, so a warning per unreadable read would flood the review log.
func (s *ServingHeartbeatStore) readRecord(path string) *ServingHeartbeat {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	v, err := parseJSON(raw)
	if err != nil {
		return nil
	}
	return asServingHeartbeat(v)
}

// classify is which of the four states a well-formed record is in.
func (s *ServingHeartbeatStore) classify(record ServingHeartbeat) HeartbeatState {
	if !s.isProcessAlive(record.PID) {
		return HeartbeatDeadPID
	}
	if float64(s.now())-record.UpdatedAt >= ServingHeartbeatStaleMs {
		return HeartbeatStale
	}
	return HeartbeatAlive
}

// isThrottled: whether the record on disk is recent enough to leave alone. Time alone, with no existence probe. Caller holds s.mu.
func (s *ServingHeartbeatStore) isThrottled(sessionID string, at int64) bool {
	return s.published != nil && s.published.sessionID == sessionID && at-s.published.at < ServingHeartbeatRefreshMs
}

// asServingHeartbeat narrows a parsed record, or nil. pid must be a positive integer specifically: kill(0, 0) addresses the caller's
// own process group and kill(-n) a foreign one, so a malformed record must be rejected before it can reach the liveness probe.
func asServingHeartbeat(v any) *ServingHeartbeat {
	c, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	sessionID, okS := c.str("sessionId")
	pid, okP := c.num("pid")
	updatedAt, okU := c.num("updatedAt")
	if !okS || sessionID == "" || !okP || pid != math.Trunc(pid) || math.IsInf(pid, 0) || pid <= 0 || !okU || math.IsNaN(updatedAt) || math.IsInf(updatedAt, 0) {
		return nil
	}
	// A pid beyond the platform range names no process; clamp so the conversion is defined and the probe reports it dead.
	if pid > math.MaxInt32 {
		pid = math.MaxInt32
	}
	return &ServingHeartbeat{SessionID: sessionID, PID: int(pid), UpdatedAt: updatedAt}
}
