package pi_background_tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The task runtime: one registry of tasks per extension load, the host effects it sends, and the session lifecycle.
// upstream: extensions/background-tasks.ts (the closure inside backgroundTasks()).
//
// The original is single-threaded: every timer, process event and promise continuation runs to completion before the next.
// The port keeps that order with one lock (mu) held by every job that touches a task, and two serial queues:
//
//   - loop runs the callbacks of timers, process exits and future continuations, one at a time, under mu;
//   - out sends the host calls a locked handler makes (a session entry, a notification) in the order the handler made
//     them, without mu held, so a host that answers a call by calling the extension back cannot deadlock against the lock.
//
// A handler of the SDK takes mu itself, releases it before it blocks, and waits for out before it returns, so the host
// sees a handler's effects in the order the original's synchronous calls made them.
//
// The host calls of a loop job (a wake, a persist, a refresh) are another matter (D5): Pi runs them from a timer or a
// promise continuation, so a host that models Pi's event loop holds them until the loop would turn, and the SDK marks
// every call made through the context of a request that has returned. Those calls go through timerOut, which no handler
// waits for: a held call at the head of out would hold every handler's calls behind it, and the handler would wait for
// the host, which waits for the handler to return. A call a handler makes goes through a request that is still running
// (liveContext), never through the context of an earlier request.

// serial runs posted jobs one at a time, in order, on a goroutine that exists only while there are jobs.
type serial struct {
	mu      sync.Mutex
	jobs    []func()
	running bool
}

func (s *serial) post(f func()) {
	s.mu.Lock()
	s.jobs = append(s.jobs, f)
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	go s.drain()
}

func (s *serial) drain() {
	for {
		s.mu.Lock()
		if len(s.jobs) == 0 {
			s.jobs = nil
			s.running = false
			s.mu.Unlock()
			return
		}
		f := s.jobs[0]
		s.jobs[0] = nil
		s.jobs = s.jobs[1:]
		s.mu.Unlock()
		f()
	}
}

// barrier blocks until every job posted before the call has run. It must not be called from a job of s.
func (s *serial) barrier() {
	done := make(chan struct{})
	s.post(func() { close(done) })
	<-done
}

// taskMap is the original's Map<string, ManagedTask>: iteration is in insertion order, and setting an existing key keeps its place.
type taskMap struct {
	byID  map[string]*task
	order []string
}

func (m *taskMap) get(id string) *task { return m.byID[id] }

func (m *taskMap) set(t *task) {
	if m.byID == nil {
		m.byID = map[string]*task{}
	}
	if _, ok := m.byID[t.ID]; !ok {
		m.order = append(m.order, t.ID)
	}
	m.byID[t.ID] = t
}

func (m *taskMap) delete(id string) {
	if _, ok := m.byID[id]; !ok {
		return
	}
	delete(m.byID, id)
	if i := slices.Index(m.order, id); i >= 0 {
		m.order = slices.Delete(m.order, i, i+1)
	}
}

func (m *taskMap) clear() {
	m.byID = map[string]*task{}
	m.order = nil
}

func (m *taskMap) size() int { return len(m.order) }

func (m *taskMap) values() []*task {
	out := make([]*task, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.byID[id])
	}
	return out
}

// guardedTimer is a timer whose callback never runs after Stop, as clearTimeout guarantees (a Go timer that fired already
// has its callback posted).
type guardedTimer struct {
	t         timer
	cancelled bool
}

func (g *guardedTimer) Stop() {
	g.cancelled = true
	if g.t != nil {
		g.t.Stop()
	}
}

// bgRuntime is one extension load's state.
type bgRuntime struct {
	mu   sync.Mutex
	loop serial
	out  serial
	// timerOut carries the host calls of the loop's jobs; nothing waits for it.
	timerOut serial
	// timerOrigin is set while a loop job runs (under mu): the host calls it makes are timer calls.
	timerOrigin atomic.Bool
	// live are the handlers of the SDK that have not returned.
	liveMu sync.Mutex
	live   []*liveHandler
	timers timers
	// logs is the log writer every task of this runtime shares.
	logs *logWriter
	// beforeSend is a test seam: it sees each custom message at the moment the runtime hands it to the host queue, as the
	// original's synchronous sendMessage call is seen.
	beforeSend func(m customMessage)

	// active is activeCtx: the context of the last handler, nil after a shutdown. last survives a shutdown, for the host calls
	// a late exit wake still makes.
	active    bool
	ctx       sdk.Context
	haveLast  bool
	last      sdk.Context
	sessionID string

	tasks        taskMap
	counter      int
	shuttingDown bool
	dedupe       map[string]string
	exitWakeDue  map[*task]bool
	warned       map[string]bool
	forceAt      *int64

	backgroundBashShortcut string
	dashboardShortcut      string
	widgetToggleShortcut   string

	persistence *persistence
	persistSoon *coalescedCall
	uiRefresh   *coalescedCall
	reader      *taskOutputReader
	watcher     *orphanWatcher
	hooks       lifecycleHooks

	unsubscribeSettings func()

	// The mini-dashboard: its mode, the one-shot expiry timer of finished tasks and the stack that shares the host widget.
	visibility widgetVisibility
	expiry     *widgetExpiry
	stack      *miniStack
	// unwatchWidth ends the subscription to terminal width changes.
	unwatchWidth func()
}

func newRuntime(tm timers) *bgRuntime {
	rt := &bgRuntime{
		timers: tm, logs: newLogWriter(logWriterDeps{timers: tm}), dedupe: map[string]string{}, exitWakeDue: map[*task]bool{}, warned: map[string]bool{},
		backgroundBashShortcut: settingString("backgroundBashShortcut", defaultBackgroundBashShortcut, ""),
		dashboardShortcut:      settingString("dashboardShortcut", defaultDashboardShortcut, ""),
		widgetToggleShortcut:   settingString("widgetToggleShortcut", defaultWidgetToggleShortcut, ""),
	}
	rt.visibility = newWidgetVisibility(settingEnum("widgetDefaultMode", []string{widgetModeCompact, widgetModeExpanded, widgetModeHidden}, widgetModeCompact, ""))
	rt.stack = newMiniStack()
	rt.expiry = newWidgetExpiry(func() {
		rt.run(func() {
			if host := rt.currentHost(); host != nil {
				rt.syncWidget(host)
			}
		})
	}, tm)
	rt.hooks = lifecycleHooks{
		rememberSnapshot: rememberSnapshot,
		persistSnapshots: func() { rt.persistNow() },
		sendTaskEvent: func(eventType string, t *task) bool {
			return rt.sendTaskEvent(eventType, t, sendTaskWakeOptions{}, nil)
		},
		refreshUI:       rt.refreshUI,
		clearTaskTimers: rt.clearTaskTimers,
	}
	rt.persistence = newPersistence(persistenceDeps{
		appendEntry: rt.appendEntry, customType: bgStateType, activeSession: rt.activeSession, listSnapshots: rt.listSnapshots,
		notify: func(where string) {
			rt.notify("Background task state persistence failed ("+where+"). Recent task transitions may not survive a restart.", "warning")
		},
	})
	rt.persistSoon = newCoalescedCall(rt.job(func() { rt.persistNow() }), outputPersistMs*time.Millisecond, rt.timers)
	rt.uiRefresh = newCoalescedCall(rt.job(func() { rt.refreshUI() }), outputUIRefreshMs*time.Millisecond, rt.timers)
	rt.reader = newTaskOutputReader(
		func(logFile, err string) {
			logBackgroundDiagnostic("task log read failed", map[string]any{"logFile": logFile, "error": err})
		},
		func(t *task) bool {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			return rt.tasks.get(t.ID) == t && !rt.shuttingDown
		},
		func(t *task) string {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			return t.output
		},
		func(t *task) string {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			return t.LogFile
		})
	return rt
}

// job wraps fn as a callback of the loop: it runs under mu, after the jobs posted before it.
func (rt *bgRuntime) job(fn func()) func() {
	return func() { rt.loop.post(func() { rt.run(fn) }) }
}

// run executes fn under mu. A panic is logged, not fatal: an uncaught exception in one timer must not take the host down.
func (rt *bgRuntime) run(fn func()) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.timerOrigin.Store(true)
	defer rt.timerOrigin.Store(false)
	defer func() {
		if r := recover(); r != nil {
			logBackgroundDiagnostic("job panicked", map[string]any{"panic": fmt.Sprint(r), "stack": string(debug.Stack())})
		}
	}()
	fn()
}

// after is setTimeout: fn runs on the loop under mu unless the timer was stopped first.
func (rt *bgRuntime) after(d time.Duration, fn func()) *guardedTimer {
	g := &guardedTimer{}
	g.t = rt.timers.After(d, func() {
		rt.loop.post(func() {
			rt.run(func() {
				if !g.cancelled {
					fn()
				}
			})
		})
	})
	return g
}

// whenDone runs fn on the loop once f resolves: a promise continuation.
func (rt *bgRuntime) whenDone(f *future, fn func()) {
	f.then(rt.job(fn))
}

// settle waits for the host calls queued so far; a handler calls it, with mu released, before it returns.
func (rt *bgRuntime) settle() { rt.out.barrier() }

// liveHandler is a handler of the SDK that has not returned, with the work that waits for its return.
type liveHandler struct {
	ctx   sdk.Context
	after []func()
}

// enter registers a handler's context for as long as the handler runs (defer rt.enter(ctx)()); what whenHandlerReturns
// collected runs after the handler's last statement.
func (rt *bgRuntime) enter(ctx sdk.Context) func() {
	h := &liveHandler{ctx: ctx}
	rt.liveMu.Lock()
	rt.live = append(rt.live, h)
	rt.liveMu.Unlock()
	return func() {
		rt.liveMu.Lock()
		if i := slices.Index(rt.live, h); i >= 0 {
			rt.live = slices.Delete(rt.live, i, i+1)
		}
		after := h.after
		rt.liveMu.Unlock()
		for _, f := range after {
			f()
		}
	}
}

// liveContext is the context of the newest handler that has not returned.
func (rt *bgRuntime) liveContext() (sdk.Context, bool) {
	rt.liveMu.Lock()
	defer rt.liveMu.Unlock()
	if len(rt.live) == 0 {
		return sdk.Context{}, false
	}
	return rt.live[len(rt.live)-1].ctx, true
}

// whenHandlerReturns runs f once the newest running handler has returned, at once when none runs or a loop job asks. The
// original starts its identity read inside the handler, but the read is a chain of file system calls that Node's event loop
// carries on only after the handler's synchronous work and the result it returns have gone back to Pi.
func (rt *bgRuntime) whenHandlerReturns(f func()) {
	if !rt.timerOrigin.Load() {
		rt.liveMu.Lock()
		if n := len(rt.live); n > 0 {
			h := rt.live[n-1]
			h.after = append(h.after, f)
			rt.liveMu.Unlock()
			return
		}
		rt.liveMu.Unlock()
	}
	f()
}

// post queues a host call made through ctx. A handler's call goes through a request that is still running when the call is
// made, because the handler waits for it (settle); a loop job's call, and a call that finds no handler running, goes through
// base and is a timer call for the host.
func (rt *bgRuntime) post(base sdk.Context, call func(sdk.Context)) {
	if rt.timerOrigin.Load() {
		rt.timerOut.post(func() { call(base) })
		return
	}
	rt.out.post(func() {
		if live, ok := rt.liveContext(); ok {
			call(live)
			return
		}
		rt.timerOut.post(func() { call(base) })
	})
}

// ── host effects ────────────────────────────────────────────────────────────

// setContext is `activeCtx = ctx`.
func (rt *bgRuntime) setContext(ctx sdk.Context) {
	rt.active, rt.ctx, rt.haveLast, rt.last = true, ctx, true, ctx
}

// cwd is activeCtx?.cwd, the project the settings are read for.
func (rt *bgRuntime) cwd() string {
	if rt.active {
		return rt.ctx.Cwd()
	}
	return ""
}

func (rt *bgRuntime) hasUI() bool { return rt.active && rt.ctx.HasUI() }

// notify is `activeCtx?.ui.notify?.(message, level)`.
func (rt *bgRuntime) notify(message, level string) {
	if !rt.active {
		return
	}
	rt.post(rt.ctx, func(ctx sdk.Context) { ctx.Notify(message, level) })
}

// sendMessage is pi.sendMessage; the host applies its delivery rules (a follow-up joins the running turn, a steer interrupts it,
// triggerTurn starts one when the agent is idle).
func (rt *bgRuntime) sendMessage(m customMessage, o sendOptions) {
	if !rt.haveLast {
		logBackgroundDiagnostic("sendMessage without a session", map[string]any{"customType": m.CustomType})
		return
	}
	if rt.beforeSend != nil {
		rt.beforeSend(m)
	}
	rt.post(rt.last, func(ctx sdk.Context) {
		err := ctx.SendCustomMessage(sdk.CustomMessage{CustomType: m.CustomType, Content: m.Content, Display: m.Display, Details: m.Details},
			sdk.SendMessageOptions{TriggerTurn: new(o.TriggerTurn), DeliverAs: o.DeliverAs})
		if err != nil {
			logBackgroundDiagnostic("sendMessage failed", map[string]any{"customType": m.CustomType, "error": err.Error()})
		}
	})
}

// appendEntry is pi.appendEntry. The call is queued; a failure is reported when it comes back, and the fingerprint the
// persistence layer remembered is dropped so the next persist writes the entry again.
func (rt *bgRuntime) appendEntry(customType string, payload any) error {
	if !rt.haveLast {
		return fmt.Errorf("no session")
	}
	rt.post(rt.last, func(ctx sdk.Context) {
		if err := ctx.AppendEntry(customType, payload); err != nil {
			rt.persistence.forgetFingerprints()
			reportPersistFailure("appendEntry", err, func(where string) {
				rt.notify("Background task state persistence failed ("+where+"). Recent task transitions may not survive a restart.", "warning")
			})
		}
	})
	return nil
}

// activeSession is what a persist needs of the active context.
func (rt *bgRuntime) activeSession() (sessionRef, bool) {
	if !rt.active {
		return sessionRef{}, false
	}
	id := rt.sessionID
	if id == "" {
		id = sessionIDFor("", "")
	}
	return sessionRef{id: id, sidecarPath: sidecarStatePath(id)}, true
}

func (rt *bgRuntime) listSnapshots() []snapshot {
	var out []snapshot
	for _, t := range rt.sortedTasks() {
		out = append(out, *rememberSnapshot(t))
	}
	return out
}

// persistNow is persistSnapshots(): the pending deferred persist is covered by this one.
func (rt *bgRuntime) persistNow() persistResult {
	rt.persistSoon.cancel()
	return rt.persistence.persistSnapshots()
}

// refreshUI is refreshUi(): the live snapshots are renewed and the widget follows the tasks.
func (rt *bgRuntime) refreshUI() {
	rt.uiRefresh.cancel()
	for _, t := range rt.tasks.values() {
		rememberSnapshot(t)
	}
	if host := rt.currentHost(); host != nil {
		rt.syncWidget(host)
	}
}

func (rt *bgRuntime) logWake(d wakeDiagnostic) { logBackgroundDiagnostic("wake diagnostic", d) }

func (rt *bgRuntime) noticeDeps() sendTaskNoticeDeps {
	return sendTaskNoticeDeps{logDiagnostic: rt.logWake, messageType: bgMessageType, rememberSnapshot: rememberSnapshot, sendMessage: rt.sendMessage}
}

// ── registry ────────────────────────────────────────────────────────────────

// sortedTasks is newest first; equal start times keep their insertion order, as a stable Array.sort does.
func (rt *bgRuntime) sortedTasks() []*task {
	out := rt.tasks.values()
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

func (rt *bgRuntime) resolveTask(token string) *task {
	return resolveTaskByToken(rt.tasks.values(), token)
}

// ownLaneDir is the lane directory this session's task logs are written to.
func (rt *bgRuntime) ownLaneDir() string {
	id := rt.sessionID
	if id == "" {
		id = fmt.Sprintf("ephemeral-%d", os.Getpid())
	}
	return taskLaneDir(id)
}

// removeTaskLog removes a finished task's log once the log writer holds no write for it: a write it still holds would
// create the file again.
func (rt *bgRuntime) removeTaskLog(t *task) {
	remove := func() {
		if err := os.Remove(t.LogFile); err != nil && !os.IsNotExist(err) {
			logBackgroundDiagnostic("task log removal failed", map[string]any{"id": t.ID, "logFile": t.LogFile, "error": err.Error()})
		}
	}
	if written := rt.logs.flush(t.LogFile); written != nil {
		written.then(remove)
		return
	}
	remove()
}

// forgetFinishedTask drops a task. A task restored from another session's branch keeps its log: the session that wrote it
// still reads it, and the lane prune removes it in time.
func (rt *bgRuntime) forgetFinishedTask(t *task) {
	voidPendingTaskWakes(t, "clear", rt.logWake, clockNow)
	rt.clearTaskTimers(t)
	rt.tasks.delete(t.ID)
	forgetSnapshot(t.ID)
	if filepath.Dir(t.LogFile) == rt.ownLaneDir() {
		rt.removeTaskLog(t)
	}
}

// boundFinishedTasks keeps at most maxFinishedTasks finished tasks, dropping the oldest. A task whose exit wake is queued or
// active is not counted.
func (rt *bgRuntime) boundFinishedTasks() int {
	var finished []*task
	for _, t := range rt.tasks.values() {
		if t.Status != statusRunning && !rt.exitWakeDue[t] {
			finished = append(finished, t)
		}
	}
	excess := len(finished) - maxFinishedTasks
	sort.SliceStable(finished, func(i, j int) bool { return finished[i].UpdatedAt < finished[j].UpdatedAt })
	removed := 0
	for _, t := range finished {
		if removed >= excess {
			break
		}
		rt.forgetFinishedTask(t)
		removed++
	}
	return removed
}

func (rt *bgRuntime) clearFinishedTasks() int {
	removed := 0
	for _, t := range rt.tasks.values() {
		if t.Status == statusRunning {
			continue
		}
		rt.forgetFinishedTask(t)
		removed++
	}
	rt.persistNow()
	rt.refreshUI()
	return removed
}

func (rt *bgRuntime) clearTaskTimers(t *task) {
	if t.outputTimer != nil {
		for _, p := range slices.Clone(t.PendingWakes) {
			if p.EventType == eventOutput {
				rt.persistScheduledOutputDrop(t, p, "cleared-on-task-exit", wakeDiagnostic{})
			}
		}
	}
	for _, tm := range []timer{t.outputTimer, t.timeoutTimer, t.forceKillTimer} {
		if tm != nil {
			tm.Stop()
		}
	}
	t.outputTimer, t.timeoutTimer, t.forceKillTimer = nil, nil, nil
}

// releaseExit ends the hold an exit wake has on the finished-task bound.
func (rt *bgRuntime) releaseExit(t *task) {
	delete(rt.exitWakeDue, t)
	if rt.boundFinishedTasks() > 0 {
		rt.persistNow()
	}
}

// ── session lifecycle ───────────────────────────────────────────────────────

func (rt *bgRuntime) ensureOrphanWatcher() {
	if rt.watcher != nil {
		return
	}
	rt.watcher = newOrphanWatcher(orphanWatcherDeps{
		getTasks: func() []*task { return rt.tasks.values() }, hooks: rt.hooks, unitActive: systemdUnitActive, lock: &rt.mu, timers: rt.timers,
		onFinalize: func(t *task, reason string) {
			logBackgroundDiagnostic("orphan task finalized", map[string]any{"id": t.ID, "pid": t.Pid, "reason": reason, "status": t.Status})
		},
	})
	rt.watcher.start()
}

func (rt *bgRuntime) replayMissedExits() { replayMissedExitsLifecycle(rt.tasks.values(), rt.hooks) }

// onSessionStart is the session_start handler.
func (rt *bgRuntime) onSessionStart(ctx sdk.Context) {
	settings.clear()
	rt.mu.Lock()
	rt.shuttingDown = false
	settings.recordProjectTrust(ctx.Cwd(), ctx.IsProjectTrusted)
	rt.setContext(ctx)
	rt.watchWidth(ctx)
	rt.mu.Unlock()
	if rt.unsubscribeSettings == nil {
		if off, err := ctx.Events().On(settingsChangedEvent, func(sdk.Context, any) error { settings.clear(); return nil }); err == nil {
			rt.unsubscribeSettings = off
		}
	}
	pruned := pruneLanes(taskLanesRoot(), nil, time.Now())
	for _, f := range pruned.failed {
		logBackgroundDiagnostic("task log prune failed", map[string]any{"path": f.path, "error": f.err})
	}
	rt.restoreSnapshots(ctx)
	rt.mu.Lock()
	rt.replayMissedExits()
	if rt.boundFinishedTasks() > 0 {
		rt.persistNow()
	}
	// Restore just probed every task the watcher would check, so the first pass waits one poll interval.
	rt.ensureOrphanWatcher()
	if host := rt.currentHost(); host != nil {
		rt.syncWidget(host)
	}
	rt.mu.Unlock()
}

// onSessionShutdown is the session_shutdown handler.
func (rt *bgRuntime) onSessionShutdown() {
	rt.mu.Lock()
	rt.shuttingDown = true
	if rt.watcher != nil {
		rt.watcher.stop()
		rt.watcher = nil
	}
	rt.uiRefresh.cancel()
	for _, t := range rt.tasks.values() {
		if t.Status == statusRunning {
			t.stopReason = stopShutdown
			// An explicit annotation, so a later restore can tell a shutdown kill from a reconcile on restart.
			t.TerminationReason = termSessionShutdown
			voidPendingTaskWakes(t, "shutdown", rt.logWake, clockNow)
			term := rt.killTaskProcess(t, "SIGTERM")
			kill := rt.killTaskProcess(t, "SIGKILL")
			if term.sent || kill.sent {
				t.Status = statusStopped
			} else {
				t.stopReason = ""
				t.TerminationReason = ""
				reason := term.err
				if reason == "" {
					reason = kill.err
				}
				if reason == "" {
					reason = "no stop signal sent"
				}
				rt.appendLogLine(t, "\n[shutdown stop skipped] "+reason+"\n")
			}
			t.UpdatedAt = clockNow()
			rememberSnapshot(t)
		}
		rt.clearTaskTimers(t)
	}
	rt.persistNow()
	// The persisted snapshots carry the tasks to the next session_start.
	rt.tasks.clear()
	rt.clearWidget(rt.currentHost())
	rt.reader.clear()
	if rt.unwatchWidth != nil {
		rt.unwatchWidth()
		rt.unwatchWidth = nil
	}
	rt.active = false
	rt.mu.Unlock()
	if rt.unsubscribeSettings != nil {
		rt.unsubscribeSettings()
		rt.unsubscribeSettings = nil
	}
	settings.clear()
	rt.settle()
	rt.logs.drain().wait()
}

// restoreSnapshots rebuilds the task set from the session: the sidecar first, then the branch's state entries and the
// tool results that carry snapshots, newest per task id; only then are the surviving tasks probed, once each.
func (rt *bgRuntime) restoreSnapshots(ctx sdk.Context) {
	id, _ := ctx.GetSessionID()
	var file string
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		file = *f
	}
	sessionID := sessionIDFor(id, file)

	type replayed struct {
		order []string
		byID  map[string]snapshot
	}
	rep := replayed{byID: map[string]snapshot{}}
	remember := func(s snapshot) {
		if s.ID == "" || s.Command == "" {
			return
		}
		if existing, ok := rep.byID[s.ID]; ok && existing.UpdatedAt >= s.UpdatedAt {
			return
		}
		if _, ok := rep.byID[s.ID]; !ok {
			rep.order = append(rep.order, s.ID)
		}
		rep.byID[s.ID] = s
	}
	clearReplayed := func() { rep.order, rep.byID = nil, map[string]snapshot{} }

	sidecarLoaded := false
	var sidecarTasks []snapshot
	if data, err := os.ReadFile(sidecarStatePath(sessionID)); err == nil {
		var doc struct {
			Tasks []snapshot `json:"tasks"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			logBackgroundDiagnostic("persistence failed (sidecar-read)", map[string]any{"error": err.Error()})
		} else if doc.Tasks != nil {
			sidecarTasks = doc.Tasks
			for _, s := range sidecarTasks {
				remember(s)
			}
			sidecarLoaded = true
		}
	} else if !os.IsNotExist(err) {
		logBackgroundDiagnostic("persistence failed (sidecar-read)", map[string]any{"error": err.Error()})
	}
	branch, _ := ctx.SessionManager().GetBranch(nil)
	for _, entry := range branch {
		typ, _ := entry["type"].(string)
		if typ == "custom" && entry["customType"] == bgStateType {
			applyCustomEntryWithBarrier(entry["data"], sidecarLoaded, sidecarTasks, clearReplayed, remember)
		}
		if typ != "message" {
			continue
		}
		msg, _ := entry["message"].(map[string]any)
		if msg["role"] != "toolResult" || (msg["toolName"] != "bg_task" && msg["toolName"] != "bg_status") {
			continue
		}
		details, _ := msg["details"].(map[string]any)
		if one := decodeSnapshots([]any{details["task"]}); details["task"] != nil && len(one) == 1 {
			remember(one[0])
		}
		applyBgToolResultTasksWithBarrier(details["tasks"], sidecarLoaded, sidecarTasks, clearReplayed, remember)
	}
	var snaps []snapshot
	for _, taskID := range rep.order {
		snaps = append(snaps, rep.byID[taskID])
	}
	restored := mapWithConcurrency(snaps, probeConcurrency, func(s snapshot) *task {
		return restoredTaskFromSnapshot(s, restoreOptions{sessionID: sessionID, hasSession: true, unitActive: systemdUnitActive})
	})
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.tasks.clear()
	rt.counter = 0
	rt.sessionID = sessionID
	for _, t := range restored {
		rt.tasks.set(t)
		rt.counter = max(rt.counter, numericTaskID(t.ID))
		rememberSnapshot(t)
	}
	if rt.tasks.size() > 0 {
		rt.persistNow()
	}
}

// numericTaskID is the counter a `bg-N` id carries, else 0.
func numericTaskID(id string) int {
	if len(id) < 4 || id[:3] != "bg-" {
		return 0
	}
	n := 0
	for _, c := range id[3:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
