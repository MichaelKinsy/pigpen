package pi_background_tasks

import (
	"sync"
	"time"
)

// The liveness watcher for orphan-running tasks: restored tasks whose recorded pid was still alive at restore. When the pid
// disappears or was recycled, it routes through finalizeTaskLifecycle so the canonical exit wake fires that would have fired
// had Pi stayed alive. It is metadata-only: it never signals a pid. upstream: extensions/orphan-watcher.ts.

type orphanWatcherDeps struct {
	getTasks   func() []*task
	hooks      lifecycleHooks
	poll       time.Duration
	identity   identityProbe
	unitActive unitActiveProbe
	timers     timers
	onFinalize func(t *task, reason string)
	// lock guards the task fields the watcher reads and writes; nil when the caller is single-threaded.
	lock sync.Locker
}

type orphanWatcher struct {
	deps       orphanWatcherDeps
	mu         sync.Mutex
	timer      timer
	pass       *orphanPass
	generation int
}

type orphanPass struct {
	done      *future
	finalized int
}

func newOrphanWatcher(deps orphanWatcherDeps) *orphanWatcher {
	if deps.poll == 0 {
		deps.poll = defaultOrphanPoll
	}
	if deps.identity == nil {
		deps.identity = readProcessIdentity
	}
	if deps.unitActive == nil {
		deps.unitActive = systemdUnitActive
	}
	if deps.timers == nil {
		deps.timers = realTimers{}
	}
	return &orphanWatcher{deps: deps}
}

// isOrphanRunning: a running task restored from a snapshot (its child handle is gone) with a real pid.
func isOrphanRunning(t *task) bool {
	return t.Status == statusRunning && t.restored && t.child == nil && t.Pid > 0
}

func (w *orphanWatcher) lock() {
	if w.deps.lock != nil {
		w.deps.lock.Lock()
	}
}

func (w *orphanWatcher) unlock() {
	if w.deps.lock != nil {
		w.deps.lock.Unlock()
	}
}

func (w *orphanWatcher) runPass(generation int) int {
	w.lock()
	var orphans []*task
	for _, t := range w.deps.getTasks() {
		if isOrphanRunning(t) {
			orphans = append(orphans, t)
		}
	}
	targets := make([]livenessTarget, len(orphans))
	for i, t := range orphans {
		targets[i] = livenessTarget{pid: t.Pid, procIdent: t.ProcIdent, resourceControl: t.ResourceControl}
	}
	w.unlock()
	probes := livenessProbes{identity: w.deps.identity, unitActive: w.deps.unitActive}
	verdicts := mapWithConcurrency(targets, probeConcurrency, func(target livenessTarget) string { return livenessVerdict(target, probes) })
	finalized := 0
	for i, t := range orphans {
		reason := verdicts[i]
		if reason == "unknown" {
			logBackgroundDiagnostic("orphan liveness unknown", map[string]any{"id": t.ID, "pid": t.Pid})
		}
		if reason == "alive" || reason == "unknown" {
			continue
		}
		// The task may have been stopped, finalized or cleared while the probes ran, or the watcher stopped.
		w.mu.Lock()
		current := generation == w.generation
		w.mu.Unlock()
		w.lock()
		if !current || !isOrphanRunning(t) {
			w.unlock()
			continue
		}
		// The pid is gone or recycled: the real exit code is unrecoverable, so the exit is null (a failed task) and the reason says which.
		term := termOrphanedPidReused
		if reason == "pid-gone" {
			term = termOrphanedPidGone
		}
		finalizeTaskLifecycle(t, nil, w.deps.hooks, "", term)
		if w.deps.onFinalize != nil {
			w.deps.onFinalize(t, reason)
		}
		w.unlock()
		finalized++
	}
	return finalized
}

// checkOnce runs one pass, or joins the pass already running, and returns how many tasks it finalized.
func (w *orphanWatcher) checkOnce() int {
	w.mu.Lock()
	if w.pass != nil {
		pass := w.pass
		w.mu.Unlock()
		pass.done.wait()
		return pass.finalized
	}
	pass := &orphanPass{done: newFuture()}
	w.pass = pass
	generation := w.generation
	w.mu.Unlock()
	pass.finalized = w.runPass(generation)
	w.mu.Lock()
	if w.pass == pass {
		w.pass = nil
	}
	w.mu.Unlock()
	pass.done.resolve()
	return pass.finalized
}

func (w *orphanWatcher) start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		return
	}
	w.timer = w.deps.timers.Every(w.deps.poll, func() { w.checkOnce() })
}

func (w *orphanWatcher) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.generation++
	w.pass = nil
	if w.timer == nil {
		return
	}
	w.timer.Stop()
	w.timer = nil
}
