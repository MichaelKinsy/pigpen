package eval

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
)

// upstream: packages/coding-agent/src/async/auto-background.ts:1-35,
//           packages/coding-agent/src/eval/settings.ts:71-90,
//           packages/coding-agent/src/tools/eval.ts:571-756,
//           packages/tui/src/tools/bash.ts:69-75

const (
	// DefaultAutoBackgroundThresholdMs is omp's cfgEvalAutoBackgroundThresholdMs default (60s).
	// upstream: packages/coding-agent/src/eval/settings.ts:84-89
	DefaultAutoBackgroundThresholdMs = 60_000

	// autoBackgroundTimeoutBufferMs is omp's timeoutBufferMs (1s).
	// upstream: packages/coding-agent/src/async/auto-background.ts:19
	autoBackgroundTimeoutBufferMs = 1_000

	// defaultMaxRunningEvalJobs matches omp's AsyncJobManager DEFAULT_MAX_RUNNING_JOBS (15).
	// upstream: packages/coding-agent/src/async/job-manager.ts:9
	defaultMaxRunningEvalJobs = 15

	// defaultEvalJobRetention matches omp's DEFAULT_RETENTION_MS (5m).
	defaultEvalJobRetention = 5 * time.Minute

	// defaultEvalJobConsumedEviction matches omp's CONSUMED_RESULT_EVICTION_MS (30s).
	defaultEvalJobConsumedEviction = 30 * time.Second
)

// resolveAutoBackgroundWaitMs mirrors omp's resolveAutoBackgroundWaitMs
// (packages/coding-agent/src/async/auto-background.ts:16-21).
//
// thresholdMs is the configured foreground-wait threshold in milliseconds (<=0
// means background immediately). timeoutSec is nil (or <=0) when the cell
// deadline is disabled (`timeout: 0`); otherwise it bounds the foreground wait
// to `timeoutMs - 1000` so a deadline expiry resolves inline instead of
// backgrounding moments before it fires.
func resolveAutoBackgroundWaitMs(thresholdMs int, timeoutSec *float64) int {
	if thresholdMs <= 0 {
		return 0
	}
	if timeoutSec == nil || *timeoutSec <= 0 {
		return thresholdMs
	}
	timeoutMs := int(math.Floor(*timeoutSec * 1000))
	budget := timeoutMs - autoBackgroundTimeoutBufferMs
	if budget <= 0 {
		return 0
	}
	if budget < thresholdMs {
		return budget
	}
	return thresholdMs
}

// formatEvalBackgroundNotice formats omp's model-facing footer when an eval
// cell auto-backgrounds, matching formatBackgroundNotice in
// packages/tui/src/tools/bash.ts:69-75 (imported by tools/eval.ts:10,754) plus
// the kernel-busy reminder from eval.md:96.
func formatEvalBackgroundNotice(jobID string, timeoutSec *float64) string {
	deadline := " (no deadline)"
	if timeoutSec != nil && *timeoutSec > 0 {
		deadline = fmt.Sprintf(
			" (killed once it has run %ss in total; `timeout: 0` disables the deadline)",
			formatTimeoutNumber(*timeoutSec),
		)
	}
	return fmt.Sprintf(
		"Backgrounded as job %s%s (kernel stays busy until it finishes); its output is injected into the conversation as a follow-up the moment it finishes. Do NOT poll for it (no `sleep`, `ps`, `pgrep`, `top`, `pidwait`, log tailing): every poll is a wasted turn. Do other work, or end your reply and wait to be woken.",
		jobID, deadline,
	)
}

func formatEvalJobLabel(title, code string) string {
	raw := strings.TrimSpace(title)
	if raw == "" {
		first, _, _ := strings.Cut(strings.TrimSpace(code), "\n")
		raw = strings.TrimSpace(first)
	}
	if raw == "" {
		raw = "eval cell"
	}
	runes := []rune(raw)
	if len(runes) > 120 {
		return string(runes[:117]) + "..."
	}
	return raw
}

// EvalJob represents one auto-backgrounded eval cell tracked in `internal/jobs`.
// Implements jobs.Job, jobs.JobDetailer, and jobs.WidgetProvider.
type EvalJob struct {
	mu           sync.Mutex
	id           string
	sessionID    string
	label        string
	intent       string
	status       string // "running" | "completed" | "failed" | "cancelled"
	startedAt    time.Time
	endedAt      time.Time
	cancel       context.CancelFunc
	done         chan struct{}
	doneOnce     sync.Once
	progressText string
	resultText   string
	errorText    string
	latestDetail map[string]any
	latestSnap   *EvalToolDetails
	backgrounded bool
	released     bool
	evictTimer   *time.Timer
	widget       *jobs.FallbackWidgetTracker
}

var (
	_ jobs.Job            = (*EvalJob)(nil)
	_ jobs.JobDetailer    = (*EvalJob)(nil)
	_ jobs.WidgetProvider = (*EvalJob)(nil)
)

func (j *EvalJob) ID() string            { return j.id }
func (j *EvalJob) Kind() jobs.Kind       { return jobs.KindEval }
func (j *EvalJob) Label() string         { return j.label }
func (j *EvalJob) Started() time.Time    { return j.startedAt }
func (j *EvalJob) Done() <-chan struct{} { return j.done }
func (j *EvalJob) Command() string       { return "" }
func (j *EvalJob) Intent() string        { return j.intent }

func (j *EvalJob) Status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

func (j *EvalJob) Output() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progressText
}

func (j *EvalJob) Result() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.resultText
}

func (j *EvalJob) Ended() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.endedAt
}

func (j *EvalJob) ErrorText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.errorText
}

func (j *EvalJob) Details() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.latestDetail == nil {
		return nil
	}
	cp := make(map[string]any, len(j.latestDetail))
	for k, v := range j.latestDetail {
		cp[k] = v
	}
	return cp
}

func (j *EvalJob) SkipDelivery() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return !j.backgrounded || j.released || j.status == "cancelled"
}

func (j *EvalJob) WidgetView(now time.Time) *jobs.WidgetView {
	j.mu.Lock()
	w := j.widget
	displayLabel := j.label
	if j.intent != "" {
		displayLabel = j.intent
	}
	settled := j.status != "running"
	start := j.startedAt
	j.mu.Unlock()
	if w == nil {
		return jobs.BuildFallbackWidget(displayLabel, "", start)
	}
	return w.View(displayLabel, "", true, settled, now)
}

func (j *EvalJob) pushWidgetChunk(chunk string) {
	j.mu.Lock()
	w := j.widget
	j.mu.Unlock()
	if w != nil {
		w.PushChunk(chunk, time.Now())
	}
}

// Cancel interrupts the running eval cell cleanly and marks the job cancelled.
func (j *EvalJob) Cancel() {
	j.mu.Lock()
	wasRunning := j.status == "running"
	if wasRunning {
		j.status = "cancelled"
		if j.endedAt.IsZero() {
			j.endedAt = time.Now()
		}
	}
	cancel := j.cancel
	j.mu.Unlock()
	if wasRunning && cancel != nil {
		cancel()
	}
}

func (j *EvalJob) isBackgrounded() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.backgrounded
}

func (j *EvalJob) isReleased() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.released
}

func (j *EvalJob) setProgress(text string, det *EvalToolDetails) {
	j.mu.Lock()
	j.progressText = text
	if det != nil {
		cp := *det
		j.latestSnap = &cp
		j.latestDetail = evalDetailsToMap(det)
	}
	j.mu.Unlock()
}

func (j *EvalJob) latestPreview() (string, *EvalToolDetails) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var snap *EvalToolDetails
	if j.latestSnap != nil {
		cp := *j.latestSnap
		snap = &cp
	}
	return j.progressText, snap
}

func (j *EvalJob) settle(status, resultText, errorText string, det *EvalToolDetails) (backgrounded, released bool) {
	j.mu.Lock()
	now := time.Now()
	if j.endedAt.IsZero() {
		j.endedAt = now
	}
	if j.widget != nil {
		j.widget.Flush(j.endedAt)
	}
	if j.status == "running" {
		j.status = status
	}
	if resultText != "" {
		j.resultText = resultText
		j.progressText = resultText
	}
	if errorText != "" {
		j.errorText = errorText
	}
	backgrounded = j.backgrounded
	released = j.released
	if det != nil {
		cp := *det
		if backgrounded {
			cp.Replies = nil
			cp.Async = &EvalAsyncDetail{
				State: j.status,
				JobID: j.id,
				Type:  "eval",
			}
		}
		j.latestSnap = &cp
		j.latestDetail = evalDetailsToMap(&cp)
	}
	j.mu.Unlock()
	j.doneOnce.Do(func() { close(j.done) })
	return backgrounded, released
}

func evalDetailsToMap(d *EvalToolDetails) map[string]any {
	if d == nil {
		return nil
	}
	m := map[string]any{}
	if d.Language != "" {
		m["language"] = string(d.Language)
	}
	if d.Async != nil {
		m["async"] = map[string]any{
			"state": d.Async.State,
			"jobId": d.Async.JobID,
			"type":  d.Async.Type,
		}
	}
	if d.IsError {
		m["isError"] = true
	}
	if d.Notice != "" {
		m["notice"] = d.Notice
	}
	if d.Meta != nil && d.Meta.Truncation != nil {
		t := d.Meta.Truncation
		trunc := map[string]any{
			"direction":   t.Direction,
			"truncatedBy": t.TruncatedBy,
			"totalLines":  t.TotalLines,
			"totalBytes":  t.TotalBytes,
			"outputLines": t.OutputLines,
			"outputBytes": t.OutputBytes,
		}
		if t.ArtifactID != "" {
			trunc["artifactId"] = t.ArtifactID
		}
		m["meta"] = map[string]any{"truncation": trunc}
	}
	return m
}

// evalJobRegistry tracks auto-backgrounded eval jobs per Manager, enforces the
// per-session running-job cap, and cleans up / evicts jobs on session end.
type evalJobRegistry struct {
	mu               sync.Mutex
	nextID           atomic.Uint64
	jobs             map[string]*EvalJob
	maxRunningJobs   int
	retention        time.Duration
	consumedEviction time.Duration
	unsubConsumed    func()
	disposed         bool
}

func newEvalJobRegistry() *evalJobRegistry {
	r := &evalJobRegistry{
		jobs:             make(map[string]*EvalJob),
		maxRunningJobs:   defaultMaxRunningEvalJobs,
		retention:        defaultEvalJobRetention,
		consumedEviction: defaultEvalJobConsumedEviction,
	}
	r.unsubConsumed = jobs.OnConsumed(func(sessionID, jobID string) {
		r.onJobConsumed(sessionID, jobID)
	})
	return r
}

func (r *evalJobRegistry) atCapacityForSession(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disposed {
		return true
	}
	running := 0
	for _, j := range r.jobs {
		if sessionID != "" && j.sessionID != sessionID {
			continue
		}
		if j.Status() == "running" {
			running++
		}
	}
	return running >= r.maxRunningJobs
}

func (r *evalJobRegistry) createJob(sessionID, label, intentStr string, backgrounded bool, cancel context.CancelFunc) (*EvalJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disposed {
		return nil, fmt.Errorf("eval job registry is disposed")
	}
	running := 0
	for _, existing := range r.jobs {
		if sessionID != "" && existing.sessionID != sessionID {
			continue
		}
		if existing.Status() == "running" {
			running++
		}
	}
	if running >= r.maxRunningJobs {
		return nil, fmt.Errorf("Background job limit reached (%d). Wait for running jobs to finish or cancel one.", r.maxRunningJobs)
	}
	id := fmt.Sprintf("eval_%d", r.nextID.Add(1))
	now := time.Now()
	j := &EvalJob{
		id:           id,
		sessionID:    sessionID,
		label:        label,
		intent:       intentStr,
		status:       "running",
		startedAt:    now,
		cancel:       cancel,
		done:         make(chan struct{}),
		backgrounded: backgrounded,
		widget:       jobs.NewFallbackWidgetTracker(now),
	}
	r.jobs[id] = j
	return j, nil
}

func (r *evalJobRegistry) promoteToBackground(ctx sdk.Context, j *EvalJob) {
	if j == nil {
		return
	}
	r.mu.Lock()
	disposed := r.disposed
	_, tracked := r.jobs[j.id]
	j.mu.Lock()
	if disposed || !tracked || j.released {
		j.mu.Unlock()
		r.mu.Unlock()
		return
	}
	alreadyBg := j.backgrounded
	j.backgrounded = true
	settled := j.status != "running"
	sessionID := j.sessionID
	jobID := j.id
	j.mu.Unlock()
	r.mu.Unlock()

	if !alreadyBg {
		jobs.EnsureDeliveryListener()
		jobs.BindSessionIfMissing(sessionID, ctx)
		jobs.Add(sessionID, j)
	}
	if settled {
		delay := r.retention
		if jobs.IsConsumed(sessionID, jobID) && r.consumedEviction > 0 && r.consumedEviction < delay {
			delay = r.consumedEviction
		}
		r.scheduleEviction(jobID, delay)
	}
}

func (r *evalJobRegistry) releaseForeground(j *EvalJob) {
	if j == nil {
		return
	}
	r.mu.Lock()
	j.mu.Lock()
	wasForeground := !j.backgrounded
	j.released = true
	if j.evictTimer != nil {
		j.evictTimer.Stop()
		j.evictTimer = nil
	}
	sessionID := j.sessionID
	jobID := j.id
	j.mu.Unlock()
	delete(r.jobs, jobID)
	if wasForeground {
		cur := r.nextID.Load()
		if cur > 0 && jobID == fmt.Sprintf("eval_%d", cur) {
			r.nextID.CompareAndSwap(cur, cur-1)
		}
	}
	r.mu.Unlock()
	jobs.Remove(sessionID, jobID)
}

func (r *evalJobRegistry) onJobSettled(j *EvalJob, backgrounded, released bool) {
	if !backgrounded || released {
		return
	}
	delay := r.retention
	if jobs.IsConsumed(j.sessionID, j.id) && r.consumedEviction > 0 && r.consumedEviction < delay {
		delay = r.consumedEviction
	}
	r.scheduleEviction(j.id, delay)
}

func (r *evalJobRegistry) onJobConsumed(sessionID, jobID string) {
	r.mu.Lock()
	j, ok := r.jobs[jobID]
	disposed := r.disposed
	delay := r.consumedEviction
	r.mu.Unlock()
	if !ok || disposed {
		return
	}
	j.mu.Lock()
	settled := j.status != "running" && j.backgrounded && !j.released
	match := sessionID == "" || j.sessionID == sessionID
	j.mu.Unlock()
	if !settled || !match {
		return
	}
	r.scheduleEviction(jobID, delay)
}

func (r *evalJobRegistry) scheduleEviction(jobID string, delay time.Duration) {
	r.mu.Lock()
	j, ok := r.jobs[jobID]
	if !ok || r.disposed {
		r.mu.Unlock()
		return
	}
	if delay < 0 {
		r.mu.Unlock()
		r.evictJob(jobID, "expired")
		return
	}
	if delay == 0 {
		delay = r.retention
	}
	if r.retention > 0 && delay > r.retention {
		delay = r.retention
	}
	j.mu.Lock()
	if j.evictTimer != nil {
		j.evictTimer.Stop()
	}
	j.evictTimer = time.AfterFunc(delay, func() {
		r.evictJob(jobID, "expired")
	})
	j.mu.Unlock()
	r.mu.Unlock()
}

func (r *evalJobRegistry) evictJob(jobID, reason string) {
	r.mu.Lock()
	j, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return
	}
	delete(r.jobs, jobID)
	r.mu.Unlock()

	j.mu.Lock()
	if j.evictTimer != nil {
		j.evictTimer.Stop()
		j.evictTimer = nil
	}
	bg := j.backgrounded
	sessionID := j.sessionID
	status := j.status
	label := j.label
	endedAt := j.endedAt
	j.mu.Unlock()

	if bg && reason != "" {
		jobs.RecordEvicted(sessionID, jobs.EvictedRecord{
			ID:        jobID,
			Kind:      jobs.KindEval,
			Label:     label,
			Status:    status,
			EndTime:   endedAt,
			Delivered: jobs.IsConsumed(sessionID, jobID),
			Reason:    reason,
		})
	}
	jobs.Remove(sessionID, jobID)
}

// cancelSession cancels and removes all eval jobs belonging to sessionID.
func (r *evalJobRegistry) cancelSession(sessionID string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	var list []*EvalJob
	for id, j := range r.jobs {
		if j.sessionID == sessionID {
			list = append(list, j)
			delete(r.jobs, id)
		}
	}
	r.mu.Unlock()

	for _, j := range list {
		j.mu.Lock()
		if j.evictTimer != nil {
			j.evictTimer.Stop()
			j.evictTimer = nil
		}
		j.released = true
		j.mu.Unlock()
		j.Cancel()
		jobs.Remove(sessionID, j.id)
	}
}

// cancelAll cancels and removes every eval job tracked by this registry.
func (r *evalJobRegistry) cancelAll() {
	r.mu.Lock()
	r.disposed = true
	list := make([]*EvalJob, 0, len(r.jobs))
	for id, j := range r.jobs {
		list = append(list, j)
		delete(r.jobs, id)
	}
	unsub := r.unsubConsumed
	r.unsubConsumed = nil
	r.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	for _, j := range list {
		j.mu.Lock()
		if j.evictTimer != nil {
			j.evictTimer.Stop()
			j.evictTimer = nil
		}
		j.released = true
		sessionID := j.sessionID
		j.mu.Unlock()
		j.Cancel()
		jobs.Remove(sessionID, j.id)
	}
}

// buildEvalBackgroundStartResult builds the ToolResult returned when an eval
// cell transitions to a background job.
// upstream: packages/coding-agent/src/tools/eval.ts:719-756
func buildEvalBackgroundStartResult(
	jobID string,
	cellLanguage EvalLanguage,
	title, code string,
	notice, previewText string,
	latestDetails *EvalToolDetails,
	timeoutSec *float64,
) sdk.ToolResult {
	var details EvalToolDetails
	if latestDetails != nil {
		details = *latestDetails
		if len(latestDetails.Cells) > 0 {
			details.Cells = append([]EvalCellResult(nil), latestDetails.Cells...)
		}
		if len(latestDetails.Languages) > 0 {
			details.Languages = append([]EvalLanguage(nil), latestDetails.Languages...)
		}
		if len(latestDetails.Images) > 0 {
			details.Images = append([]EvalImageDetail(nil), latestDetails.Images...)
		}
		if len(latestDetails.Replies) > 0 {
			details.Replies = append([]string(nil), latestDetails.Replies...)
		}
	} else {
		details = EvalToolDetails{
			Language:  cellLanguage,
			Languages: []EvalLanguage{cellLanguage},
			Cells: []EvalCellResult{{
				Index:    0,
				Title:    title,
				Code:     code,
				Language: cellLanguage,
				Output:   previewText,
				Status:   "running",
			}},
		}
	}
	if notice != "" && details.Notice == "" {
		details.Notice = notice
	}
	details.Async = &EvalAsyncDetail{
		State: "running",
		JobID: jobID,
		Type:  "eval",
	}

	var lines []string
	trimmedPreview := strings.TrimRight(previewText, " \t\r\n")
	if notice != "" && !strings.HasPrefix(trimmedPreview, notice) {
		lines = append(lines, notice, "")
	}
	if trimmedPreview != "" {
		lines = append(lines, trimmedPreview, "")
	}
	lines = append(lines, formatEvalBackgroundNotice(jobID, timeoutSec))
	return sdk.ToolResult{
		Content: strings.Join(lines, "\n"),
		Details: details,
	}
}
