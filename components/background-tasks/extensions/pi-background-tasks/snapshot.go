package pi_background_tasks

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Snapshots, process identity, liveness and restore. upstream: extensions/snapshot.ts.

// taskSnapshot is the serializable view of a task, as the original's taskSnapshot builds it.
func taskSnapshot(t *task) *snapshot {
	s := t.snapshot
	s.ExitNotified = t.ExitNotified
	s.NotifyMode = normalizeNotifyMode(t.NotifyMode)
	s.WakeEvents = nonNilEvents(t.WakeEvents)
	if t.voidedWakes != nil {
		s.VoidedWakeSequences = sortedVoided(t.voidedWakes)
	} else {
		s.VoidedWakeSequences = nonNilInts(t.VoidedWakeSequences)
	}
	s.PendingWakes = nonNilPending(t.PendingWakes)
	s.LastOutputDedupeBy = t.LastOutputDedupeBy
	if s.LastOutputDedupeBy == nil {
		s.LastOutputDedupeBy = map[string]string{}
	}
	if t.OutputWakeBudget != nil {
		s.OutputWakeBudget = normalizeOutputWakeBudget(t.OutputWakeBudget)
	}
	return &s
}

func nonNilEvents(v []wakeEvent) []wakeEvent {
	if v == nil {
		return []wakeEvent{}
	}
	return v
}

func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

func nonNilPending(v []wakePending) []wakePending {
	if v == nil {
		return []wakePending{}
	}
	return v
}

// liveSnapshots is the module-level map of the latest snapshot per task id, which renderers read so an old tool result
// shows a task's current state.
var (
	liveMu        sync.Mutex
	liveSnapshots = map[string]*snapshot{}
)

func rememberSnapshot(t *task) *snapshot {
	s := taskSnapshot(t)
	liveMu.Lock()
	liveSnapshots[s.ID] = s
	liveMu.Unlock()
	return s
}

func forgetSnapshot(id string) {
	liveMu.Lock()
	delete(liveSnapshots, id)
	liveMu.Unlock()
}

// latestSnapshot is the live snapshot of s's task, else s itself.
func latestSnapshot(s *snapshot) *snapshot {
	if s == nil {
		return nil
	}
	liveMu.Lock()
	defer liveMu.Unlock()
	if live, ok := liveSnapshots[s.ID]; ok {
		return live
	}
	return s
}

// resolveTaskByToken finds a task by id or pid; token is a task id, a pid in decimal, or empty.
func resolveTaskByToken(tasks []*task, token string) *task {
	normalized := jsTrim(token)
	if normalized == "" {
		return nil
	}
	for _, t := range tasks {
		if t.ID == normalized || strconv.Itoa(t.Pid) == normalized {
			return t
		}
	}
	return nil
}

// identityReading is what one identity read learned about a pid. kind: "identity", "alive" (the pid lives but this host cannot
// read its identity), "gone", "unknown" (reason says why).
type identityReading struct {
	kind     string
	identity processIdentity
	reason   string
}

type identityProbe func(pid int) identityReading

// identityReaderDeps replaces the platform, the /proc read, the probe runner and the signal-0 check in a test.
type identityReaderDeps struct {
	platform     string
	readStat     func(path string) (string, error)
	run          probeRunner
	processAlive func(pid int) bool
}

// defaultReadProcessIdentity reads the kernel-stable identity of a pid. On Linux the fast path is /proc/<pid>/stat field 22
// (start time in jiffies since boot) and its comm; elsewhere, and where /proc cannot answer, `ps -o lstart=,comm= -p <pid>`
// as a time-limited probe. This detects pid reuse: the start token cannot collide for the same pid within one boot.
func defaultReadProcessIdentity(pid int, deps identityReaderDeps) identityReading {
	if pid <= 0 {
		return identityReading{kind: "gone"}
	}
	platform := deps.platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform == "linux" {
		readStat := deps.readStat
		if readStat == nil {
			readStat = func(path string) (string, error) {
				b, err := os.ReadFile(path)
				return string(b), err
			}
		}
		stat, err := readStat("/proc/" + strconv.Itoa(pid) + "/stat")
		if err == nil {
			lastParen := strings.LastIndex(stat, ")")
			// The fields after the closing paren of comm are space separated; starttime is field 22 overall, index 19 of them.
			starttime := ""
			if lastParen >= 0 {
				fields := strings.Fields(jsTrim(stat[lastParen+1:]))
				if len(fields) > 19 {
					starttime = fields[19]
				}
			}
			if starttime != "" {
				comm := stat[strings.Index(stat, "(")+1 : lastParen]
				return identityReading{kind: "identity", identity: processIdentity{Pid: pid, StartToken: starttime, Comm: comm}}
			}
		} else if errors.Is(err, fs.ErrNotExist) {
			return identityReading{kind: "gone"}
		}
	}
	run := deps.run
	if run == nil {
		run = runProbe
	}
	result := run("ps", []string{"-o", "lstart=,comm=", "-p", strconv.Itoa(pid)})
	switch result.kind {
	case "missing":
		alive := deps.processAlive
		if alive == nil {
			alive = processAlive
		}
		if alive(pid) {
			return identityReading{kind: "alive"}
		}
		return identityReading{kind: "gone"}
	case "unsettled":
		reason := "ps " + result.cause
		return identityReading{kind: "unknown", reason: reason}
	}
	if result.status != 0 {
		return identityReading{kind: "gone"}
	}
	line := jsTrim(result.stdout)
	if line == "" {
		return identityReading{kind: "gone"}
	}
	// lstart is "Day Mon DD HH:MM:SS YYYY" (five tokens), then comm.
	parts := splitJSSpace(line)
	if len(parts) < 6 {
		return identityReading{kind: "unknown", reason: "ps output unparseable: " + line}
	}
	return identityReading{kind: "identity", identity: processIdentity{Pid: pid, StartToken: strings.Join(parts[:5], " "), Comm: strings.Join(parts[5:], " ")}}
}

// splitJSSpace is s.split(/\s+/) for a trimmed s.
func splitJSSpace(s string) []string {
	return strings.FieldsFunc(s, isJSSpace)
}

// identityMatches is true iff the kernel-stable subset matches: pid and start token. comm is only a diagnostic (an exec
// changes it without changing the process). A snapshot with no identity matches: PID-only liveness is the degraded path.
func identityMatches(recorded *processIdentity, current processIdentity) bool {
	if recorded == nil {
		return true
	}
	return recorded.Pid == current.Pid && recorded.StartToken == current.StartToken
}

// tri is a three-valued answer: the original's true, false and null.
type tri int8

const (
	triNull tri = iota
	triFalse
	triTrue
)

type unitActiveProbe func(unit string) tri

// livenessProbes are the probes livenessVerdict asks; unitActive may be nil.
type livenessProbes struct {
	identity   identityProbe
	unitActive unitActiveProbe
}

// livenessTarget is what livenessVerdict reads of a task.
type livenessTarget struct {
	pid             int
	procIdent       *processIdentity
	resourceControl *resourceControlMeta
}

// livenessVerdict decides whether a running task's process still lives: "alive", "pid-gone", "pid-reused" or "unknown".
// A systemd-run task asks its transient unit first, since the unit is more authoritative than the wrapper pid.
func livenessVerdict(target livenessTarget, probes livenessProbes) string {
	unit := ""
	if rc := target.resourceControl; rc != nil && rc.Mode == "systemd-run" {
		unit = rc.UnitName
	}
	if unit != "" && probes.unitActive != nil {
		switch probes.unitActive(unit) {
		case triTrue:
			return "alive"
		case triFalse:
			return "pid-gone"
		}
	}
	current := probes.identity(target.pid)
	switch current.kind {
	case "alive":
		return "alive"
	case "gone":
		return "pid-gone"
	case "unknown":
		return "unknown"
	}
	if identityMatches(target.procIdent, current.identity) {
		return "alive"
	}
	return "pid-reused"
}

// restoreOptions is RestoreOptions.
type restoreOptions struct {
	now        int64
	identity   identityProbe
	sessionID  string
	hasSession bool
	unitActive unitActiveProbe
}

// restoredTaskFromSnapshot rehydrates a persisted snapshot into a task placeholder. The child is gone in most cases:
// closed, no timers. A running snapshot whose process is gone or whose pid was reused becomes stopped (stopReason
// shutdown, exit not notified, so the missed-exit replay delivers it); one whose process lives stays running, closed false,
// restored true, for the orphan watcher. A snapshot of another session is rehydrated but never replays an exit.
func restoredTaskFromSnapshot(s snapshot, opts restoreOptions) *task {
	now := opts.now
	if now == 0 {
		now = realNow()
	}
	probe := opts.identity
	if probe == nil {
		probe = readProcessIdentity
	}
	wasRunning := s.Status == statusRunning
	foreign := opts.hasSession && s.SessionID != "" && s.SessionID != opts.sessionID
	pidStillAlive := false
	if wasRunning && !foreign {
		verdict := livenessVerdict(livenessTarget{pid: s.Pid, procIdent: s.ProcIdent, resourceControl: s.ResourceControl}, livenessProbes{identity: probe, unitActive: opts.unitActive})
		pidStillAlive = verdict == "alive" || verdict == "unknown"
	}
	coerced := wasRunning && !pidStillAlive

	var exitNotified bool
	switch {
	case coerced && !foreign:
		exitNotified = false
	case foreign:
		exitNotified = true
	default:
		exitNotified = s.ExitNotified
	}
	termination := s.TerminationReason
	if coerced && termination == "" {
		termination = termReconcileOnRestart
	}

	t := &task{snapshot: s}
	t.closed = !pidStillAlive
	t.ExitNotified = exitNotified
	t.lastAnnouncedLen = int(s.OutputBytes)
	if t.LastOutputDedupeBy == nil {
		t.LastOutputDedupeBy = map[string]string{}
	}
	t.matcher = parseOutputMatcher(s.NotifyPattern)
	t.NotifyMode = normalizeNotifyMode(s.NotifyMode)
	t.OutputPatternMatched = s.OutputPatternMatched
	t.OutputWakeBudget = normalizeOutputWakeBudget(s.OutputWakeBudget)
	t.PendingWakes = []wakePending{}
	switch {
	case pidStillAlive:
		t.Status = statusRunning
	case wasRunning:
		t.Status = statusStopped
	}
	if coerced {
		t.stopReason = stopShutdown
	}
	t.TerminationReason = termination
	t.VoidedWakeSequences = nonNilInts(s.VoidedWakeSequences)
	t.voidedWakes = map[int]bool{}
	for _, seq := range s.VoidedWakeSequences {
		t.voidedWakes[seq] = true
	}
	t.WakeEvents = nonNilEvents(s.WakeEvents)
	t.restored = true
	if coerced {
		t.UpdatedAt = now
	}
	if opts.hasSession {
		t.SessionID = opts.sessionID
	}
	return t
}

// exitCandidate is what selectMissedExits reads of a task: exitNotified is a pointer so an absent flag (the original's
// undefined) stays distinct from false.
type exitCandidate interface {
	exitState() (status string, notifyOnExit bool, exitNotified *bool)
}

func (t *task) exitState() (string, bool, *bool) { return t.Status, t.NotifyOnExit, &t.ExitNotified }

// selectMissedExits selects the tasks whose terminal transition never produced an exit wake: not running, exit wakes
// enabled, and the flag explicitly false.
func selectMissedExits[T exitCandidate](tasks []T) []T {
	var out []T
	for _, t := range tasks {
		status, notify, notified := t.exitState()
		if status == statusRunning || !notify || notified == nil || *notified {
			continue
		}
		out = append(out, t)
	}
	return out
}

// sortNewestFirst orders tasks (given in insertion order) newest-started first; equal starts keep insertion order.
func sortNewestFirst(list []*task) []*task {
	out := append([]*task{}, list...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// readProcessIdentity and systemdUnitActive are the process probes of spawn, restore and the orphan watcher; the tests replace
// them to hold or count the reads.
var (
	readProcessIdentity identityProbe   = func(pid int) identityReading { return defaultReadProcessIdentity(pid, identityReaderDeps{}) }
	systemdUnitActive   unitActiveProbe = defaultSystemdUnitActive
)
