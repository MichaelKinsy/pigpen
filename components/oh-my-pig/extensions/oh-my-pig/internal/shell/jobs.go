package shell

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
)

// upstream: packages/coding-agent/src/async/job-manager.ts, packages/coding-agent/src/async/auto-background.ts

const (
	// DefaultAutoBackgroundThreshold is omp's DEFAULT_AUTO_BACKGROUND_THRESHOLD_MS (60s).
	DefaultAutoBackgroundThreshold = 60 * time.Second
	// DefaultMaxRunningJobs is omp's DEFAULT_MAX_RUNNING_JOBS (15).
	DefaultMaxRunningJobs = 15
	// DefaultJobRetention is omp's DEFAULT_RETENTION_MS (5m).
	DefaultJobRetention = 5 * time.Minute
	// DefaultConsumedJobEviction is omp's CONSUMED_RESULT_EVICTION_MS (30s).
	DefaultConsumedJobEviction = 30 * time.Second
	// autoBackgroundTimeoutBuffer is omp's timeoutBufferMs (1s).
	autoBackgroundTimeoutBuffer = time.Second
)

// JobStatus is the lifecycle state of a managed background job.
type JobStatus string

const (
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// JobSnapshot is an immutable view of a background job for wait tools and proc:// readers.
type JobSnapshot struct {
	ID           string
	SessionID    string
	Type         string // "bash"
	Status       JobStatus
	Label        string
	Command      string
	Cwd          string
	StartTime    time.Time
	EndTime      time.Time
	PIDs         []int
	ProgressText string
	ResultText   string
	ErrorText    string
	Foreground   bool
}

// JobDeliverySink is invoked when a backgrounded job settles and its result
// should be delivered into the conversation.
type JobDeliverySink func(jobID string, text string, snap JobSnapshot)

type managedJob struct {
	mu            sync.Mutex
	id            string
	sessionID     string
	jobType       string
	status        JobStatus
	label         string
	command       string
	cwd           string
	intent        string
	startTime     time.Time
	endTime       time.Time
	cancel        context.CancelFunc
	done          chan struct{}
	pidsFn        func() []int
	progressText  string
	resultText    string
	errorText     string
	latestDetails map[string]any
	foreground    bool
	delivered     bool
	released      bool
	customSink    bool // true if JobManager's custom test sink already handles delivery
	evictTimer    *time.Timer
	widget        *jobs.FallbackWidgetTracker
}

// Compile-time assertions: managedJob implements jobs.Job, jobs.JobDetailer, and jobs.WidgetProvider.
var (
	_ jobs.Job            = (*managedJob)(nil)
	_ jobs.JobDetailer    = (*managedJob)(nil)
	_ jobs.WidgetProvider = (*managedJob)(nil)
)

func (j *managedJob) ID() string            { return j.id }
func (j *managedJob) Kind() jobs.Kind       { return jobs.KindBash }
func (j *managedJob) Label() string         { return j.label }
func (j *managedJob) Started() time.Time    { return j.startTime }
func (j *managedJob) Done() <-chan struct{} { return j.done }

func (j *managedJob) Status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return string(j.status)
}

func (j *managedJob) Output() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progressText
}

func (j *managedJob) Result() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.resultText
}

func (j *managedJob) Cancel() {
	j.mu.Lock()
	wasRunning := j.status == JobStatusRunning
	if wasRunning {
		j.status = JobStatusCancelled
		if j.endTime.IsZero() {
			j.endTime = time.Now()
		}
	}
	cancel := j.cancel
	j.mu.Unlock()
	if wasRunning && cancel != nil {
		cancel()
	}
}

func (j *managedJob) Ended() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.endTime
}

func (j *managedJob) ErrorText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.errorText
}

// ensureCancelledErrorText populates a cancelled job's errorText from the
// fallback cancellation message if its run goroutine did not settle within
// the cancel grace backstop.
func (j *managedJob) ensureCancelledErrorText() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status == JobStatusCancelled && j.errorText == "" && j.resultText == "" {
		j.errorText = "[Command cancelled]\n"
	}
}

func (j *managedJob) Command() string { return j.command }
func (j *managedJob) Intent() string  { return j.intent }

// WidgetView returns omp's deterministic fallback `JobWidgetView` for this
// bash job (`packages/coding-agent/src/job-widgets/runtime.ts:640-716`).
func (j *managedJob) WidgetView(now time.Time) *jobs.WidgetView {
	j.mu.Lock()
	w := j.widget
	displayLabel := j.label
	if j.intent != "" {
		displayLabel = j.intent
	}
	settled := j.status != JobStatusRunning
	start := j.startTime
	j.mu.Unlock()
	if w == nil {
		return jobs.BuildFallbackWidget(displayLabel, "", start)
	}
	return w.View(displayLabel, "", true, settled, now)
}

// pushWidgetChunk feeds a sanitized output chunk into this job's fallback
// widget tracker (called from executeCommand's OnChunk callback).
func (j *managedJob) pushWidgetChunk(chunk string) {
	j.mu.Lock()
	w := j.widget
	j.mu.Unlock()
	if w != nil {
		w.PushChunk(chunk, time.Now())
	}
}

func (j *managedJob) Details() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.latestDetails == nil {
		return nil
	}
	cp := make(map[string]any, len(j.latestDetails))
	for k, v := range j.latestDetails {
		cp[k] = v
	}
	return cp
}

func (j *managedJob) SkipDelivery() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.foreground || j.released || j.delivered || j.customSink || j.status == JobStatusCancelled
}

func (j *managedJob) snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	var pids []int
	if j.pidsFn != nil {
		pids = j.pidsFn()
	}
	return JobSnapshot{
		ID:           j.id,
		SessionID:    j.sessionID,
		Type:         j.jobType,
		Status:       j.status,
		Label:        j.label,
		Command:      j.command,
		Cwd:          j.cwd,
		StartTime:    j.startTime,
		EndTime:      j.endTime,
		PIDs:         pids,
		ProgressText: j.progressText,
		ResultText:   j.resultText,
		ErrorText:    j.errorText,
		Foreground:   j.foreground,
	}
}

// JobManager tracks async and auto-backgrounded bash jobs (`bg_1`, `bg_2`, ...)
// keyed per session, evicting settled jobs after Retention and mirroring every
// background job into the shared `internal/jobs` registry.
// upstream: packages/coding-agent/src/async/job-manager.ts
type JobManager struct {
	mu               sync.Mutex
	nextID           atomic.Uint64
	jobs             map[string]*managedJob // jobID -> job (ids are globally unique bg_N)
	maxRunningJobs   int
	retention        time.Duration
	consumedEviction time.Duration
	onComplete       JobDeliverySink
	onEvict          func(sessionID, jobID string) // optional package-internal hook for event-driven tests
	disposed         bool
	notifyCh         chan struct{}
	unsubConsumed    func()
}

// JobManagerOptions configures a JobManager.
type JobManagerOptions struct {
	MaxRunningJobs   int
	Retention        time.Duration // 0 => DefaultJobRetention; <0 => immediate eviction
	ConsumedEviction time.Duration // 0 => DefaultConsumedJobEviction
	OnJobComplete    JobDeliverySink
}

// NewJobManager constructs a new JobManager.
func NewJobManager(opts JobManagerOptions) *JobManager {
	maxJobs := opts.MaxRunningJobs
	if maxJobs <= 0 {
		maxJobs = DefaultMaxRunningJobs
	}
	retention := opts.Retention
	if retention == 0 {
		retention = DefaultJobRetention
	}
	consumed := opts.ConsumedEviction
	if consumed == 0 {
		consumed = DefaultConsumedJobEviction
	}
	m := &JobManager{
		jobs:             make(map[string]*managedJob),
		maxRunningJobs:   maxJobs,
		retention:        retention,
		consumedEviction: consumed,
		onComplete:       opts.OnJobComplete,
		notifyCh:         make(chan struct{}),
	}
	// Re-arm eviction timer to the shorter consumed grace once a settled job's
	// result is delivered or consumed by `wait` (upstream: job-manager.ts:1166-1187).
	m.unsubConsumed = jobs.OnConsumed(func(sessionID, jobID string) {
		m.onJobConsumed(sessionID, jobID)
	})
	return m
}

// SetDeliverySink replaces the completion delivery callback.
func (m *JobManager) SetDeliverySink(sink JobDeliverySink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onComplete = sink
}

// AtCapacity reports whether the manager has reached its running-job cap.
func (m *JobManager) AtCapacity() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.disposed {
		return true
	}
	return m.runningCountLocked("") >= m.maxRunningJobs
}

// AtCapacityForSession reports whether sessionID has reached its running-job cap.
func (m *JobManager) AtCapacityForSession(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.disposed {
		return true
	}
	return m.runningCountLocked(sessionID) >= m.maxRunningJobs
}

func (m *JobManager) runningCountLocked(sessionID string) int {
	n := 0
	for _, j := range m.jobs {
		if sessionID != "" && j.sessionID != sessionID {
			continue
		}
		j.mu.Lock()
		running := j.status == JobStatusRunning
		j.mu.Unlock()
		if running {
			n++
		}
	}
	return n
}

func (m *JobManager) broadcastLocked() {
	close(m.notifyCh)
	m.notifyCh = make(chan struct{})
}

type jobRunFunc func(ctx context.Context, jobID string, reportProgress func(string), setDetails func(map[string]any), setPIDs func(func() []int)) (string, error)

// register starts a managed job and returns its handle. Non-foreground jobs are
// immediately registered in `internal/jobs` via jobs.Add; foreground jobs are
// registered when promoteToBackground is called.
func (m *JobManager) register(sessionID, jobType, label, command, cwd, intent string, foreground bool, run jobRunFunc) (*managedJob, error) {
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		return nil, fmt.Errorf("Async job manager is disposed")
	}
	if m.runningCountLocked(sessionID) >= m.maxRunningJobs {
		maxJobs := m.maxRunningJobs
		m.mu.Unlock()
		return nil, fmt.Errorf("Background job limit reached (%d). Wait for running jobs to finish or cancel one.", maxJobs)
	}
	id := fmt.Sprintf("bg_%d", m.nextID.Add(1))
	runCtx, cancel := context.WithCancel(context.Background())
	hasCustomSink := m.onComplete != nil
	startedAt := time.Now()
	job := &managedJob{
		id:         id,
		sessionID:  sessionID,
		jobType:    jobType,
		status:     JobStatusRunning,
		label:      label,
		command:    command,
		cwd:        cwd,
		intent:     intent,
		startTime:  startedAt,
		cancel:     cancel,
		done:       make(chan struct{}),
		foreground: foreground,
		customSink: hasCustomSink,
		widget:     jobs.NewFallbackWidgetTracker(startedAt),
	}
	m.jobs[id] = job
	m.mu.Unlock()

	if !foreground {
		jobs.EnsureDeliveryListener()
		jobs.Add(sessionID, job)
	}

	reportProgress := func(text string) {
		job.mu.Lock()
		job.progressText = text
		job.mu.Unlock()
	}
	setDetails := func(det map[string]any) {
		job.mu.Lock()
		job.latestDetails = det
		job.mu.Unlock()
	}
	setPIDs := func(fn func() []int) {
		job.mu.Lock()
		job.pidsFn = fn
		job.mu.Unlock()
	}

	go func() {
		defer cancel()
		text, err := run(runCtx, id, reportProgress, setDetails, setPIDs)

		m.mu.Lock()
		sink := m.onComplete
		m.mu.Unlock()

		job.mu.Lock()
		job.endTime = time.Now()
		job.pidsFn = nil
		if job.widget != nil {
			job.widget.Flush(job.endTime)
		}
		if err != nil {
			// upstream: async/job-manager.ts:665-673 — errorText is always
			// recorded (including for jobs already marked cancelled), while
			// status stays cancelled if cancel() ran first.
			job.errorText = err.Error()
			if job.status == JobStatusRunning {
				if runCtx.Err() != nil {
					job.status = JobStatusCancelled
				} else {
					job.status = JobStatusFailed
				}
			}
		} else {
			// upstream: async/job-manager.ts:656-663
			job.resultText = text
			if job.status == JobStatusRunning {
				job.status = JobStatusCompleted
			}
		}
		if sink != nil {
			job.customSink = true
		}
		deliverCustom := sink != nil && !job.foreground && !job.delivered && !job.released && job.status != JobStatusCancelled
		if deliverCustom {
			job.delivered = true
		}
		deliveryText := job.resultText
		if job.status == JobStatusFailed && job.errorText != "" {
			deliveryText = job.errorText
		}
		shouldScheduleEvict := !job.foreground && !job.released
		job.mu.Unlock()

		close(job.done)

		m.mu.Lock()
		m.broadcastLocked()
		m.mu.Unlock()

		if deliverCustom {
			sink(id, deliveryText, job.snapshot())
		}
		if shouldScheduleEvict {
			delay := m.retention
			if deliverCustom || jobs.IsConsumed(sessionID, id) {
				if m.consumedEviction > 0 && m.consumedEviction < delay {
					delay = m.consumedEviction
				}
			}
			m.scheduleEviction(id, delay)
		}
	}()

	return job, nil
}

func (m *JobManager) jobByID(id string) *managedJob {
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	return job
}

// scheduleEviction arms a timer that removes the settled job from JobManager
// and `internal/jobs` and records an eviction tombstone for `proc://<id>`.
// upstream: async/job-manager.ts:1382
func (m *JobManager) scheduleEviction(jobID string, delay time.Duration) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	if !ok || m.disposed {
		m.mu.Unlock()
		return
	}
	if delay < 0 {
		m.mu.Unlock()
		m.evictJob(jobID, "expired")
		return
	}
	if delay == 0 {
		delay = m.retention
	}
	if m.retention > 0 && delay > m.retention {
		delay = m.retention
	}
	job.mu.Lock()
	if job.evictTimer != nil {
		job.evictTimer.Stop()
	}
	job.evictTimer = time.AfterFunc(delay, func() {
		m.evictJob(jobID, "expired")
	})
	job.mu.Unlock()
	m.mu.Unlock()
}

// onJobConsumed shortens a settled job's eviction timer to consumedEviction
// once its result has been delivered or read by `wait`.
// upstream: async/job-manager.ts:1164-1189
func (m *JobManager) onJobConsumed(sessionID, jobID string) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	disposed := m.disposed
	consumedDelay := m.consumedEviction
	m.mu.Unlock()
	if !ok || disposed {
		return
	}
	job.mu.Lock()
	settled := job.status != JobStatusRunning && !job.foreground && !job.released
	matchSession := sessionID == "" || job.sessionID == sessionID
	job.mu.Unlock()
	if !settled || !matchSession {
		return
	}
	m.scheduleEviction(jobID, consumedDelay)
}

func (m *JobManager) evictJob(jobID, reason string) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.jobs, jobID)
	evictHook := m.onEvict
	m.mu.Unlock()

	job.mu.Lock()
	if job.evictTimer != nil {
		job.evictTimer.Stop()
		job.evictTimer = nil
	}
	isFg := job.foreground
	sessionID := job.sessionID
	status := string(job.status)
	label := job.label
	endTime := job.endTime
	delivered := job.delivered || jobs.IsConsumed(sessionID, jobID)
	job.mu.Unlock()

	if !isFg && reason != "" {
		jobs.RecordEvicted(sessionID, jobs.EvictedRecord{
			ID:        jobID,
			Kind:      jobs.KindBash,
			Label:     label,
			Status:    status,
			EndTime:   endTime,
			Delivered: delivered,
			Reason:    reason,
		})
	}
	jobs.Remove(sessionID, jobID)
	if evictHook != nil {
		evictHook(sessionID, jobID)
	}
}

// promoteToBackground exposes a foreground-backed job in listings and registers
// it with `internal/jobs` so completion delivery and `wait`/`proc://` see it.
func (m *JobManager) promoteToBackground(jobID string) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	sink := m.onComplete
	m.mu.Unlock()
	if !ok {
		return
	}
	job.mu.Lock()
	wasForeground := job.foreground
	job.foreground = false
	if sink != nil {
		job.customSink = true
	}
	settled := job.status != JobStatusRunning
	deliverCustom := sink != nil && settled && job.status != JobStatusCancelled && !job.delivered && !job.released
	if deliverCustom {
		job.delivered = true
	}
	deliveryText := job.resultText
	if job.status == JobStatusFailed && job.errorText != "" {
		deliveryText = job.errorText
	}
	sessionID := job.sessionID
	job.mu.Unlock()

	if wasForeground {
		jobs.EnsureDeliveryListener()
		jobs.Add(sessionID, job)
	}
	if deliverCustom {
		sink(jobID, deliveryText, job.snapshot())
	}
	if settled {
		delay := m.retention
		if deliverCustom || jobs.IsConsumed(sessionID, jobID) {
			if m.consumedEviction > 0 && m.consumedEviction < delay {
				delay = m.consumedEviction
			}
		}
		m.scheduleEviction(jobID, delay)
	}
}

// releaseForegroundJob removes a foreground job that settled inline before
// backgrounding and returns its id to the counter when it holds the newest
// auto id (`bg_<nextID>`), matching omp's `#discardForegroundJob`
// (`async/job-manager.ts:838-841, 1191-1200`).
func (m *JobManager) releaseForegroundJob(jobID string) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	if ok {
		job.mu.Lock()
		wasForeground := job.foreground
		job.released = true
		if job.evictTimer != nil {
			job.evictTimer.Stop()
			job.evictTimer = nil
		}
		sessionID := job.sessionID
		job.mu.Unlock()
		delete(m.jobs, jobID)
		jobs.Remove(sessionID, jobID)
		if wasForeground {
			cur := m.nextID.Load()
			if cur > 0 && jobID == fmt.Sprintf("bg_%d", cur) {
				m.nextID.CompareAndSwap(cur, cur-1)
			}
		}
	}
	m.mu.Unlock()
}

// Get returns a snapshot of the job with the given ID, if present and visible.
func (m *JobManager) Get(id string) (JobSnapshot, bool) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobSnapshot{}, false
	}
	snap := job.snapshot()
	if snap.Foreground {
		return JobSnapshot{}, false
	}
	return snap, true
}

// List returns snapshots of all visible (non-foreground) jobs.
func (m *JobManager) List() []JobSnapshot {
	m.mu.Lock()
	list := make([]*managedJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j)
	}
	m.mu.Unlock()

	out := make([]JobSnapshot, 0, len(list))
	for _, j := range list {
		snap := j.snapshot()
		if !snap.Foreground {
			out = append(out, snap)
		}
	}
	return out
}

// Running returns snapshots of all visible running jobs.
func (m *JobManager) Running() []JobSnapshot {
	all := m.List()
	out := make([]JobSnapshot, 0, len(all))
	for _, s := range all {
		if s.Status == JobStatusRunning {
			out = append(out, s)
		}
	}
	return out
}

// Cancel cancels a running job by ID and returns true if it was found and running.
func (m *JobManager) Cancel(id string) bool {
	m.mu.Lock()
	job, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	job.mu.Lock()
	wasRunning := job.status == JobStatusRunning
	if wasRunning {
		job.status = JobStatusCancelled
		if job.endTime.IsZero() {
			job.endTime = time.Now()
		}
	}
	cancel := job.cancel
	job.mu.Unlock()
	if wasRunning && cancel != nil {
		cancel()
	}
	return wasRunning
}

// Wait blocks until the job with id settles or ctx is cancelled.
func (m *JobManager) Wait(ctx context.Context, id string) (JobSnapshot, bool, error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobSnapshot{}, false, nil
	}
	select {
	case <-job.done:
		return job.snapshot(), true, nil
	case <-ctx.Done():
		return job.snapshot(), true, ctx.Err()
	}
}

// WaitAny blocks until any visible running job settles, or returns immediately
// if none are running (or ctx is cancelled).
func (m *JobManager) WaitAny(ctx context.Context) (JobSnapshot, bool, error) {
	for {
		m.mu.Lock()
		ch := m.notifyCh
		var running []*managedJob
		for _, j := range m.jobs {
			j.mu.Lock()
			if !j.foreground && j.status == JobStatusRunning {
				running = append(running, j)
			}
			j.mu.Unlock()
		}
		m.mu.Unlock()
		if len(running) == 0 {
			return JobSnapshot{}, false, nil
		}
		select {
		case <-ch:
			for _, j := range running {
				snap := j.snapshot()
				if snap.Status != JobStatusRunning {
					return snap, true, nil
				}
			}
		case <-ctx.Done():
			return JobSnapshot{}, false, ctx.Err()
		}
	}
}

// WaitAll blocks until all currently registered jobs finish or ctx is cancelled.
func (m *JobManager) WaitAll(ctx context.Context) error {
	m.mu.Lock()
	list := make([]*managedJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j)
	}
	m.mu.Unlock()

	for _, j := range list {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// DisposeSession cancels and removes ONLY the jobs belonging to sessionID,
// leaving the manager active and other sessions' jobs completely untouched.
func (m *JobManager) DisposeSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	m.mu.Lock()
	var list []*managedJob
	for id, j := range m.jobs {
		if j.sessionID == sessionID {
			list = append(list, j)
			delete(m.jobs, id)
		}
	}
	m.mu.Unlock()

	for _, j := range list {
		j.mu.Lock()
		if j.evictTimer != nil {
			j.evictTimer.Stop()
			j.evictTimer = nil
		}
		if j.status == JobStatusRunning {
			j.status = JobStatusCancelled
		}
		cancel := j.cancel
		j.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		jobs.Remove(sessionID, j.id)
	}
	var waitErr error
	for _, j := range list {
		select {
		case <-j.done:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	return waitErr
}

// Dispose cancels all running jobs in this manager and marks it disposed.
// Used only by isolated test runtimes — NEVER from a per-session shutdown hook.
func (m *JobManager) Dispose(ctx context.Context) error {
	m.mu.Lock()
	m.disposed = true
	unsub := m.unsubConsumed
	m.unsubConsumed = nil
	list := make([]*managedJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j)
	}
	m.jobs = make(map[string]*managedJob)
	m.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	for _, j := range list {
		j.mu.Lock()
		if j.evictTimer != nil {
			j.evictTimer.Stop()
			j.evictTimer = nil
		}
		if j.status == JobStatusRunning {
			j.status = JobStatusCancelled
		}
		cancel := j.cancel
		sessionID := j.sessionID
		j.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		jobs.Remove(sessionID, j.id)
	}
	var waitErr error
	for _, j := range list {
		select {
		case <-j.done:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	return waitErr
}

// resolveAutoBackgroundWait mirrors packages/coding-agent/src/async/auto-background.ts:16.
// Bounded by timeout minus 1s buffer so a deadline expiry resolves inline instead
// of backgrounding moments before it fires.
func resolveAutoBackgroundWait(threshold time.Duration, timeout *time.Duration) time.Duration {
	if threshold <= 0 {
		return 0
	}
	if timeout == nil {
		return threshold
	}
	budget := *timeout - autoBackgroundTimeoutBuffer
	if budget <= 0 {
		return 0
	}
	if budget < threshold {
		return budget
	}
	return threshold
}

// ServiceReady mirrors omp's ServiceReady (launch/services.ts:32).
type ServiceReady struct {
	Log     string
	Port    *int
	Host    string
	Timeout *float64
}

// ServiceSnapshot is an immutable view of a long-lived named service for proc:// readers.
type ServiceSnapshot struct {
	Name       string
	SessionID  string
	State      string // "starting", "running", "ready", "exited", "stopped"
	Ready      bool
	ReadyAt    time.Time
	PID        int
	Mode       string // "session"
	Command    string
	Cwd        string
	StartTime  time.Time
	EndTime    time.Time
	ExitCode   *int
	ExitReason string
	Logs       string
	LogPath    string
}

type managedService struct {
	mu            sync.Mutex
	name          string
	sessionID     string
	command       string
	cwd           string
	state         string
	mode          string // "session" (persist/detached unsupported without daemon)
	readyAt       time.Time
	pid           int
	usedPTY       bool
	startTime     time.Time
	endTime       time.Time
	exitCode      *int
	exitReason    string
	stopRequested bool
	cancel        context.CancelFunc
	done          chan struct{}
	sink          *OutputSink
	stdin         io.WriteCloser
	logFile       *os.File
	logPath       string
	logBytes      int
	logCapped     bool
	logNotify     chan struct{}
	notifier      *OutputNotifier
}

// Compile-time assertions: managedService implements jobs.Job, jobs.JobDetailer,
// and jobs.ServiceController.
var (
	_ jobs.Job               = (*managedService)(nil)
	_ jobs.JobDetailer       = (*managedService)(nil)
	_ jobs.ServiceController = (*managedService)(nil)
)

func (s *managedService) ID() string            { return s.name }
func (s *managedService) Kind() jobs.Kind       { return jobs.KindService }
func (s *managedService) Label() string         { return s.name }
func (s *managedService) Started() time.Time    { return s.startTime }
func (s *managedService) Done() <-chan struct{} { return s.done }
func (s *managedService) Command() string       { return s.command }
func (s *managedService) Intent() string        { return "" }
func (s *managedService) Details() map[string]any {
	return nil
}
func (s *managedService) SkipDelivery() bool { return true }

func (s *managedService) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case "starting", "running", "ready":
		return "running"
	case "stopped":
		return "cancelled"
	case "exited":
		if s.stopRequested {
			return "cancelled"
		}
		if s.exitCode != nil && *s.exitCode != 0 {
			return "failed"
		}
		return "completed"
	default:
		return s.state
	}
}

func (s *managedService) Output() string {
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		return replaceTabs(sink.Preview())
	}
	return ""
}

func (s *managedService) Result() string {
	return s.Output()
}

func (s *managedService) Ended() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endTime
}

func (s *managedService) ErrorText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitReason
}

func (s *managedService) Cancel() {
	s.mu.Lock()
	active := s.state == "starting" || s.state == "running" || s.state == "ready"
	if active {
		// upstream: daemon/units.ts:1982,2062,2068 (stopped services settle to "exited")
		s.stopRequested = true
		s.state = "exited"
		s.exitReason = "stopped"
	}
	cancel := s.cancel
	notifier := s.notifier
	s.mu.Unlock()
	if notifier != nil {
		notifier.Stop()
	}
	if active && cancel != nil {
		cancel()
	}
}

func (s *managedService) ServiceState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *managedService) ServiceReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.readyAt.IsZero() || s.state == "ready" || s.state == "running"
}

func (s *managedService) ServicePID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "exited" || s.state == "failed" || s.state == "stopped" {
		return 0
	}
	return s.pid
}

func (s *managedService) ServiceMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == "" {
		return "session"
	}
	return s.mode
}

// SetServiceMode accepts only "session" because services run in-process without
// an out-of-process daemon and are always reaped at session end.
func (s *managedService) SetServiceMode(mode string) error {
	switch mode {
	case "session":
		s.mu.Lock()
		s.mode = "session"
		s.mu.Unlock()
		return nil
	case "persist", "detached":
		return fmt.Errorf("Service modes other than session (%s) are not supported in this port (no out-of-process service daemon).", mode)
	default:
		return fmt.Errorf("Service mode must be persist, session, or detached")
	}
}

// SendStdin writes content to the service's stdin (PTY or pipe). For PTY-backed
// services, appends CR (`\r`) when content does not already end with CR or LF,
// matching omp's launch/services.ts:433 (`empty = Enter`). For pipe-backed
// services (no line discipline translating CR), appends LF (`\n`).
func (s *managedService) SendStdin(content string) error {
	s.mu.Lock()
	st := s.state
	w := s.stdin
	isPTY := s.usedPTY
	s.mu.Unlock()
	if st != "starting" && st != "running" && st != "ready" {
		return fmt.Errorf("Service %s is not running (state: %s)", s.name, st)
	}
	if w == nil {
		return fmt.Errorf("Service %s stdin is unavailable", s.name)
	}
	data := content
	if !strings.HasSuffix(data, "\r") && !strings.HasSuffix(data, "\n") {
		if isPTY {
			data += "\r"
		} else {
			data += "\n"
		}
	}
	_, err := io.WriteString(w, data)
	return err
}

func (s *managedService) ServiceExitCode() *int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}

func (s *managedService) ServiceExitReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitReason
}

func (s *managedService) ServiceLogPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logPath
}

// writeLogChunk appends clean text to the service's temp log file, capped at
// DefaultServiceLogMaxBytes so a runaway service cannot fill the disk.
func (s *managedService) writeLogChunk(chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logFile == nil || s.logCapped || chunk == "" {
		return
	}
	rem := DefaultServiceLogMaxBytes - s.logBytes
	if rem <= 0 {
		s.logCapped = true
		_, _ = s.logFile.WriteString("\n[SERVICE LOG TRUNCATED: size limit reached]\n")
		return
	}
	if len(chunk) > rem {
		_, _ = s.logFile.WriteString(chunk[:rem])
		_, _ = s.logFile.WriteString("\n[SERVICE LOG TRUNCATED: size limit reached]\n")
		s.logBytes = DefaultServiceLogMaxBytes
		s.logCapped = true
		return
	}
	n, _ := s.logFile.WriteString(chunk)
	s.logBytes += n
}

func (s *managedService) cleanupLogFile() {
	s.mu.Lock()
	p := s.logPath
	f := s.logFile
	s.logFile = nil
	s.logPath = ""
	s.mu.Unlock()
	if f != nil {
		_ = f.Close()
	}
	if p != "" {
		_ = os.Remove(p)
	}
}

func (s *managedService) snapshot() ServiceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := ""
	if s.sink != nil {
		logs = replaceTabs(s.sink.Preview())
	}
	mode := s.mode
	if mode == "" {
		mode = "session"
	}
	pid := s.pid
	if s.state == "exited" || s.state == "failed" || s.state == "stopped" {
		pid = 0
	}
	return ServiceSnapshot{
		Name:       s.name,
		SessionID:  s.sessionID,
		State:      s.state,
		Ready:      !s.readyAt.IsZero(),
		ReadyAt:    s.readyAt,
		PID:        pid,
		Command:    s.command,
		Cwd:        s.cwd,
		StartTime:  s.startTime,
		EndTime:    s.endTime,
		ExitCode:   s.exitCode,
		ExitReason: s.exitReason,
		Logs:       logs,
		LogPath:    s.logPath,
	}
}

// ServiceManager tracks named long-lived services started via `bash` with
// `name`, strictly scoped by sessionID so two sessions can use the same name
// without colliding and shutting down one session never affects another.
type ServiceManager struct {
	mu       sync.Mutex
	services map[string]map[string]*managedService // sessionID -> name -> service
}

// NewServiceManager constructs a new ServiceManager.
func NewServiceManager() *ServiceManager {
	return &ServiceManager{
		services: make(map[string]map[string]*managedService),
	}
}

// GetForSession returns a snapshot of sessionID's named service.
func (m *ServiceManager) GetForSession(sessionID, name string) (ServiceSnapshot, bool) {
	m.mu.Lock()
	svc, ok := m.services[sessionID][name]
	m.mu.Unlock()
	if !ok {
		return ServiceSnapshot{}, false
	}
	return svc.snapshot(), true
}

// Get returns a snapshot of the named service (checking "default" first, then
// any session for backwards compatibility with single-session callers).
func (m *ServiceManager) Get(name string) (ServiceSnapshot, bool) {
	m.mu.Lock()
	if svc, ok := m.services["default"][name]; ok {
		m.mu.Unlock()
		return svc.snapshot(), true
	}
	for _, byName := range m.services {
		if svc, ok := byName[name]; ok {
			m.mu.Unlock()
			return svc.snapshot(), true
		}
	}
	m.mu.Unlock()
	return ServiceSnapshot{}, false
}

// ListForSession returns snapshots of all services in sessionID.
func (m *ServiceManager) ListForSession(sessionID string) []ServiceSnapshot {
	m.mu.Lock()
	svcs := make([]*managedService, 0, len(m.services[sessionID]))
	for _, s := range m.services[sessionID] {
		svcs = append(svcs, s)
	}
	m.mu.Unlock()
	out := make([]ServiceSnapshot, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, s.snapshot())
	}
	return out
}

// List returns snapshots of all registered services across sessions.
func (m *ServiceManager) List() []ServiceSnapshot {
	m.mu.Lock()
	var svcs []*managedService
	for _, byName := range m.services {
		for _, s := range byName {
			svcs = append(svcs, s)
		}
	}
	m.mu.Unlock()
	out := make([]ServiceSnapshot, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, s.snapshot())
	}
	return out
}

// StopForSession stops sessionID's named service and waits for it to exit.
func (m *ServiceManager) StopForSession(ctx context.Context, sessionID, name string) (bool, error) {
	m.mu.Lock()
	svc, ok := m.services[sessionID][name]
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	return stopManagedService(ctx, svc)
}

// Stop terminates a running named service (searching "default" first, then any session).
func (m *ServiceManager) Stop(ctx context.Context, name string) (bool, error) {
	m.mu.Lock()
	var svc *managedService
	if s, ok := m.services["default"][name]; ok {
		svc = s
	} else {
		for _, byName := range m.services {
			if s, ok := byName[name]; ok {
				svc = s
				break
			}
		}
	}
	m.mu.Unlock()
	if svc == nil {
		return false, nil
	}
	return stopManagedService(ctx, svc)
}

func stopManagedService(ctx context.Context, svc *managedService) (bool, error) {
	svc.mu.Lock()
	active := svc.state == "starting" || svc.state == "running" || svc.state == "ready"
	if active {
		svc.stopRequested = true
		svc.state = "exited"
		svc.exitReason = "stopped"
	}
	cancel := svc.cancel
	notifier := svc.notifier
	svc.mu.Unlock()
	if notifier != nil {
		notifier.Stop()
	}
	if active && cancel != nil {
		cancel()
	}
	select {
	case <-svc.done:
		return active, nil
	case <-ctx.Done():
		return active, ctx.Err()
	}
}

// DisposeSession stops all services of sessionID concurrently, deletes their
// log files, and removes them from `internal/jobs` without touching any other
// session.
func (m *ServiceManager) DisposeSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	m.mu.Lock()
	byName := m.services[sessionID]
	delete(m.services, sessionID)
	svcs := make([]*managedService, 0, len(byName))
	for _, s := range byName {
		svcs = append(svcs, s)
	}
	m.mu.Unlock()
	return disposeServiceSlice(ctx, svcs)
}

// Dispose stops all running services across all sessions (for test cleanup).
func (m *ServiceManager) Dispose(ctx context.Context) error {
	m.mu.Lock()
	var svcs []*managedService
	for _, byName := range m.services {
		for _, s := range byName {
			svcs = append(svcs, s)
		}
	}
	m.services = make(map[string]map[string]*managedService)
	m.mu.Unlock()
	return disposeServiceSlice(ctx, svcs)
}

func disposeServiceSlice(ctx context.Context, svcs []*managedService) error {
	for _, s := range svcs {
		s.mu.Lock()
		if s.state == "starting" || s.state == "running" || s.state == "ready" {
			s.state = "stopped"
			s.exitReason = "disposed"
		}
		cancel := s.cancel
		notifier := s.notifier
		sessionID := s.sessionID
		s.mu.Unlock()
		if notifier != nil {
			notifier.Stop()
		}
		if cancel != nil {
			cancel()
		}
		jobs.Remove(sessionID, s.name)
	}
	var err error
	for _, s := range svcs {
		select {
		case <-s.done:
		case <-ctx.Done():
			err = ctx.Err()
		}
		s.cleanupLogFile()
	}
	return err
}

// compileReadyRegex validates and compiles a service readiness log pattern
// with multiline mode `(?m)` so `^` and `$` match line boundaries within
// accumulated output (upstream: launch/services.ts:337, daemon/protocol.ts:29).
func compileReadyRegex(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	return regexp.Compile("(?m)" + pattern)
}
