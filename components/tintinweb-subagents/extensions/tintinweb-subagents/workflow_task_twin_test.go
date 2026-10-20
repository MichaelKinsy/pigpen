package tintinweb_subagents

import (
	"testing"
	"time"
)

// Twins of test/workflow-task.test.ts.

type stubControl struct{ pauses, resumes int }

func (c *stubControl) Pause()         { c.pauses++ }
func (c *stubControl) Resume()        { c.resumes++ }
func (c *stubControl) IsPaused() bool { return false }
func (c *stubControl) Skip(int) bool  { return true }
func (c *stubControl) Retry(int) bool { return true }

func runningTask() (*workflowTask, *stubControl) {
	task := createWorkflowTask(workflowTaskInit{ID: "wf_abc123", StartTime: 1_000})
	control := &stubControl{}
	task.Control = control
	return task, control
}

func TestWorkflowTask(t *testing.T) {
	const f = "workflow-task"
	// pausing a run
	tw(t, f, "tells the run to hold, not just the record", func(t *testing.T) {
		// Flipping the status alone would show a paused run in every surface while it kept starting agents.
		task, control := runningTask()
		eq(t, pauseWorkflowTask(task, 5_000), true)
		eq(t, control.pauses, 1)
		eq(t, task.Status, "paused")
		eq(t, *task.PausedAt, int64(5_000))
	})
	tw(t, f, "banks the held time on resume, so elapsed does not count it", func(t *testing.T) {
		task, control := runningTask()
		pauseWorkflowTask(task, 5_000)
		eq(t, resumeWorkflowTask(task, 9_000), true)
		eq(t, control.resumes, 1)
		eq(t, task.Status, "running")
		eq(t, task.TotalPausedMs, int64(4_000))
		eq(t, task.PausedAt == nil, true)
	})
	tw(t, f, "accumulates across several pauses", func(t *testing.T) {
		task, _ := runningTask()
		pauseWorkflowTask(task, 2_000)
		resumeWorkflowTask(task, 3_000)
		pauseWorkflowTask(task, 4_000)
		resumeWorkflowTask(task, 10_000)
		eq(t, task.TotalPausedMs, int64(7_000))
	})
	tw(t, f, "refuses when there is no run behind the record", func(t *testing.T) {
		// A task whose run has settled keeps its progress but loses its control.
		task := createWorkflowTask(workflowTaskInit{ID: "wf_abc123"})
		eq(t, pauseWorkflowTask(task, wfNowMs()), false)
		eq(t, task.Status, "running")
	})
	tw(t, f, "refuses to pause twice or resume something running", func(t *testing.T) {
		task, control := runningTask()
		eq(t, pauseWorkflowTask(task, 1_000), true)
		eq(t, pauseWorkflowTask(task, 2_000), false)
		eq(t, control.pauses, 1)
		// And the first pause's clock is untouched by the refused second one.
		eq(t, *task.PausedAt, int64(1_000))
		eq(t, resumeWorkflowTask(task, 3_000), true)
		eq(t, resumeWorkflowTask(task, 4_000), false)
		eq(t, control.resumes, 1)
	})

	// settling a run
	result := func() *workflowRunResult {
		return &workflowRunResult{Status: "completed", Value: float64(1), ValueJSON: []byte("1"), Meta: workflowMeta{Name: "wf", Description: "d"}, Progress: []workflowEntry{}}
	}
	tw(t, f, "drops the control so a finished run cannot be paused", func(t *testing.T) {
		task, _ := runningTask()
		completeWorkflowTask(task, result())
		eq(t, task.Control == nil, true)
		eq(t, pauseWorkflowTask(task, wfNowMs()), false)
	})
	tw(t, f, "banks a pause that was still open when the run finished", func(t *testing.T) {
		// A run held at a pause can still settle; that time was spent held, and elapsed has to say so.
		task, _ := runningTask()
		pauseWorkflowTask(task, time.Now().UnixMilli()-3_000)
		completeWorkflowTask(task, result())
		eq(t, task.PausedAt == nil, true)
		if task.TotalPausedMs < 3_000 {
			t.Fatalf("banked %d", task.TotalPausedMs)
		}
		eq(t, task.Status, "completed")
	})
	tw(t, f, "drops the control when the run never started", func(t *testing.T) {
		task, _ := runningTask()
		failWorkflowTask(task, "bad meta")
		eq(t, task.Control == nil, true)
		eq(t, task.Status, "failed")
	})
}
