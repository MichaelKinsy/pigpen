package shell

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// upstream: packages/coding-agent/src/async/job-manager.ts, packages/coding-agent/src/async/auto-background.ts

const (
	// DefaultAutoBackgroundThreshold is omp's DEFAULT_AUTO_BACKGROUND_THRESHOLD_MS (60s).
	DefaultAutoBackgroundThreshold = 60 * time.Second
	// DefaultMaxRunningJobs is omp's DEFAULT_MAX_RUNNING_JOBS (15).
	DefaultMaxRunningJobs = 15
	// DefaultJobRetention is omp's DEFAULT_RETENTION_MS (5m).
	DefaultJobRetention = 5 * time.Minute
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
	mu           sync.Mutex
	id           string
	jobType      string
	status       JobStatus
	label        string
	command      string
	cwd          string
	startTime    time.Time
	endTime      time.Time
	cancel       context.CancelFunc
	done         chan struct{}
	pidsFn       func() []int
	progressText string
	resultText   string
	errorText    string
	foreground   bool
	delivered    bool
	released     bool
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

// JobManager tracks async and auto-backgrounded bash jobs (`bg_1`, `bg_2`, ...).
// Exported methods provide the contract needed by a future `wait` tool and
// `proc://` reader.
// upstream: packages/coding-agent/src/async/job-manager.ts
type JobManager struct {
	mu             sync.Mutex
	nextID         atomic.Uint64
	jobs           map[string]*managedJob
	maxRunningJobs int
	onComplete     JobDeliverySink
	disposed       bool
	notifyCh       chan struct{} // closed and replaced whenever any job settles
}

// JobManagerOptions configures a JobManager.
type JobManagerOptions struct {
	MaxRunningJobs int
	OnJobComplete  JobDeliverySink
}

// NewJobManager constructs a new JobManager.
func NewJobManager(opts JobManagerOptions) *JobManager {
	maxJobs := opts.MaxRunningJobs
	if maxJobs <= 0 {
		maxJobs = DefaultMaxRunningJobs
	}
	return &JobManager{
		jobs:           make(map[string]*managedJob),
		maxRunningJobs: maxJobs,
		onComplete:     opts.OnJobComplete,
		notifyCh:       make(chan struct{}),
	}
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
	return m.runningCountLocked() >= m.maxRunningJobs
}

func (m *JobManager) runningCountLocked() int {
	n := 0
	for _, j := range m.jobs {
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

type jobRunFunc func(ctx context.Context, jobID string, reportProgress func(string), setPIDs func(func() []int)) (string, error)

// register starts a managed job and returns its handle.
func (m *JobManager) register(jobType, label, command, cwd string, foreground bool, run jobRunFunc) (*managedJob, error) {
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		return nil, fmt.Errorf("Async job manager is disposed")
	}
	if m.runningCountLocked() >= m.maxRunningJobs {
		maxJobs := m.maxRunningJobs
		m.mu.Unlock()
		return nil, fmt.Errorf("Background job limit reached (%d). Wait for running jobs to finish or cancel one.", maxJobs)
	}
	id := fmt.Sprintf("bg_%d", m.nextID.Add(1))
	runCtx, cancel := context.WithCancel(context.Background())
	job := &managedJob{
		id:         id,
		jobType:    jobType,
		status:     JobStatusRunning,
		label:      label,
		command:    command,
		cwd:        cwd,
		startTime:  time.Now(),
		cancel:     cancel,
		done:       make(chan struct{}),
		foreground: foreground,
	}
	m.jobs[id] = job
	m.mu.Unlock()

	reportProgress := func(text string) {
		job.mu.Lock()
		job.progressText = text
		job.mu.Unlock()
	}
	setPIDs := func(fn func() []int) {
		job.mu.Lock()
		job.pidsFn = fn
		job.mu.Unlock()
	}

	go func() {
		defer cancel()
		text, err := run(runCtx, id, reportProgress, setPIDs)

		job.mu.Lock()
		job.endTime = time.Now()
		job.pidsFn = nil
		if job.status == JobStatusRunning {
			if runCtx.Err() != nil && err != nil {
				job.status = JobStatusCancelled
				job.errorText = err.Error()
			} else if err != nil {
				job.status = JobStatusFailed
				job.errorText = err.Error()
				if text != "" {
					job.resultText = text
				}
			} else {
				job.status = JobStatusCompleted
				job.resultText = text
			}
		}
		deliver := !job.foreground && !job.delivered && !job.released && job.status != JobStatusCancelled
		if deliver {
			job.delivered = true
		}
		deliveryText := job.resultText
		if job.status == JobStatusFailed && job.errorText != "" {
			deliveryText = job.errorText
		}
		job.mu.Unlock()

		close(job.done)

		m.mu.Lock()
		sink := m.onComplete
		m.broadcastLocked()
		m.mu.Unlock()

		if deliver && sink != nil {
			sink(id, deliveryText, job.snapshot())
		}
	}()

	return job, nil
}

// promoteToBackground exposes a foreground-backed job in listings and enables
// completion delivery (delivering immediately if it already settled).
func (m *JobManager) promoteToBackground(jobID string) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	sink := m.onComplete
	m.mu.Unlock()
	if !ok {
		return
	}
	job.mu.Lock()
	job.foreground = false
	deliver := job.status != JobStatusRunning && job.status != JobStatusCancelled && !job.delivered && !job.released
	if deliver {
		job.delivered = true
	}
	deliveryText := job.resultText
	if job.status == JobStatusFailed && job.errorText != "" {
		deliveryText = job.errorText
	}
	job.mu.Unlock()

	if deliver && sink != nil {
		sink(jobID, deliveryText, job.snapshot())
	}
}

// releaseForegroundJob removes a foreground job that settled inline before
// backgrounding.
func (m *JobManager) releaseForegroundJob(jobID string) {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	if ok {
		job.mu.Lock()
		job.released = true
		job.mu.Unlock()
		delete(m.jobs, jobID)
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
	jobs := make([]*managedJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()

	out := make([]JobSnapshot, 0, len(jobs))
	for _, j := range jobs {
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
	jobs := make([]*managedJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()

	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Dispose cancels all running jobs and waits for them to exit up to ctx.
func (m *JobManager) Dispose(ctx context.Context) error {
	m.mu.Lock()
	m.disposed = true
	jobs := make([]*managedJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()

	for _, j := range jobs {
		j.mu.Lock()
		if j.status == JobStatusRunning {
			j.status = JobStatusCancelled
		}
		cancel := j.cancel
		j.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	var waitErr error
	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	m.mu.Lock()
	m.jobs = make(map[string]*managedJob)
	m.mu.Unlock()
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

// ServiceReady mirrors omp's ServiceReady (launch/services.ts:18).
type ServiceReady struct {
	Log     string
	Port    *int
	Host    string
	Timeout *float64
}

// ServiceSnapshot is an immutable view of a long-lived named service for proc:// readers.
type ServiceSnapshot struct {
	Name       string
	State      string // "starting", "running", "exited", "stopped"
	Ready      bool
	ReadyAt    time.Time
	PID        int
	Command    string
	Cwd        string
	StartTime  time.Time
	EndTime    time.Time
	ExitCode   *int
	ExitReason string
	Logs       string
}

type managedService struct {
	mu         sync.Mutex
	name       string
	command    string
	cwd        string
	state      string
	readyAt    time.Time
	pid        int
	startTime  time.Time
	endTime    time.Time
	exitCode   *int
	exitReason string
	cancel     context.CancelFunc
	done       chan struct{}
	sink       *OutputSink
	logNotify  chan struct{}
}

func (s *managedService) snapshot() ServiceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := ""
	if s.sink != nil {
		logs = s.sink.Preview()
	}
	return ServiceSnapshot{
		Name:       s.name,
		State:      s.state,
		Ready:      !s.readyAt.IsZero(),
		ReadyAt:    s.readyAt,
		PID:        s.pid,
		Command:    s.command,
		Cwd:        s.cwd,
		StartTime:  s.startTime,
		EndTime:    s.endTime,
		ExitCode:   s.exitCode,
		ExitReason: s.exitReason,
		Logs:       logs,
	}
}

// ServiceManager tracks named long-lived services started via `bash` with `name`.
type ServiceManager struct {
	mu       sync.Mutex
	services map[string]*managedService
}

// NewServiceManager constructs a new ServiceManager.
func NewServiceManager() *ServiceManager {
	return &ServiceManager{
		services: make(map[string]*managedService),
	}
}

// Get returns a snapshot of the named service, if registered.
func (m *ServiceManager) Get(name string) (ServiceSnapshot, bool) {
	m.mu.Lock()
	svc, ok := m.services[name]
	m.mu.Unlock()
	if !ok {
		return ServiceSnapshot{}, false
	}
	return svc.snapshot(), true
}

// List returns snapshots of all registered services.
func (m *ServiceManager) List() []ServiceSnapshot {
	m.mu.Lock()
	svcs := make([]*managedService, 0, len(m.services))
	for _, s := range m.services {
		svcs = append(svcs, s)
	}
	m.mu.Unlock()
	out := make([]ServiceSnapshot, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, s.snapshot())
	}
	return out
}

// Stop terminates a running named service and waits for it to exit.
func (m *ServiceManager) Stop(ctx context.Context, name string) (bool, error) {
	m.mu.Lock()
	svc, ok := m.services[name]
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	svc.mu.Lock()
	active := svc.state == "starting" || svc.state == "running"
	if active {
		svc.state = "stopped"
		svc.exitReason = "stopped"
	}
	cancel := svc.cancel
	svc.mu.Unlock()
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

// Dispose stops all running services and waits for them to exit up to ctx.
func (m *ServiceManager) Dispose(ctx context.Context) error {
	m.mu.Lock()
	svcs := make([]*managedService, 0, len(m.services))
	for _, s := range m.services {
		svcs = append(svcs, s)
	}
	m.mu.Unlock()

	for _, s := range svcs {
		s.mu.Lock()
		if s.state == "starting" || s.state == "running" {
			s.state = "stopped"
			s.exitReason = "disposed"
		}
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	var err error
	for _, s := range svcs {
		select {
		case <-s.done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	return err
}

// compileReadyRegex validates and compiles a service readiness log pattern.
func compileReadyRegex(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	return regexp.Compile(pattern)
}
