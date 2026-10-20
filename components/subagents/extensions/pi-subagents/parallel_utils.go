package pi_subagents

import (
	"math"
	"strings"
	"sync"
)

// Port of src/runs/shared/parallel-utils.ts: the step shapes a parallel run flattens, the global concurrency semaphore, the
// bounded concurrent map and the aggregate of parallel outputs. A step is ordered JSON (*jsObject), as in the original's
// literals: sequential steps carry `agent` and `task`, a parallel group a `parallel` array, a dynamic group `expand`,
// `collect` and a single `parallel` step.

const (
	DefaultGlobalConcurrencyLimit = 20 // DEFAULT_GLOBAL_CONCURRENCY_LIMIT
	MaxParallelConcurrency        = 4  // MAX_PARALLEL_CONCURRENCY
)

// isParallelGroup is isParallelGroup: a step whose `parallel` is an array.
func isParallelGroup(step *jsObject) bool {
	_, ok := step.vals["parallel"].([]any)
	return ok
}

// isDynamicRunnerGroup is isDynamicRunnerGroup: expand, collect and a `parallel` that is not an array.
func isDynamicRunnerGroup(step *jsObject) bool {
	_, hasParallel := step.vals["parallel"]
	_, isList := step.vals["parallel"].([]any)
	_, hasExpand := step.vals["expand"]
	_, hasCollect := step.vals["collect"]
	return hasExpand && hasCollect && hasParallel && !isList
}

// flattenSteps is flattenSteps: the sequential steps and the members of parallel groups, in order; dynamic groups expand later.
func flattenSteps(steps []*jsObject) []*jsObject {
	flat := []*jsObject{}
	for _, step := range steps {
		switch {
		case isParallelGroup(step):
			for _, task := range step.vals["parallel"].([]any) {
				if t, ok := task.(*jsObject); ok {
					flat = append(flat, t)
				}
			}
		case isDynamicRunnerGroup(step):
		default:
			flat = append(flat, step)
		}
	}
	return flat
}

// Semaphore limits concurrent access across several mapConcurrent calls of one run: a global cap on the subagent tasks
// executing at once, whatever each step's own limit. Waiters are served in order.
type Semaphore struct {
	mu        sync.Mutex
	available int
	queue     []chan struct{}
}

// NewSemaphore is `new Semaphore(limit)`: at least one slot (an invalid limit is one).
func NewSemaphore(limit float64) *Semaphore {
	return &Semaphore{available: clampConcurrency(limit)}
}

func clampConcurrency(limit float64) int {
	n := math.Floor(limit)
	if math.IsNaN(n) || n == 0 {
		n = 1
	}
	if n < 1 {
		return 1
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(n)
}

// Acquire waits for a slot.
func (s *Semaphore) Acquire() {
	s.mu.Lock()
	if s.available > 0 {
		s.available--
		s.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	s.queue = append(s.queue, ch)
	s.mu.Unlock()
	<-ch
}

// Release hands the slot to the longest waiter, or frees it.
func (s *Semaphore) Release() {
	s.mu.Lock()
	if len(s.queue) > 0 {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		close(next)
		return
	}
	s.available++
	s.mu.Unlock()
}

// mapConcurrent is mapConcurrent: fn over items with at most limit (at least one) in flight, results in item order. It returns
// the first error as soon as it happens (as Promise.all rejects); the other workers go on with the remaining items, and
// onSchedulingSettled runs once every worker has stopped, including those that outlive the early return. A worker that failed
// takes no further item. global, when set, also gates each call of fn.
func mapConcurrent[T, R any](items []T, limit float64, fn func(item T, i int) (R, error), global *Semaphore, onSchedulingSettled func()) ([]R, error) {
	results := make([]R, len(items))
	workers := min(clampConcurrency(limit), len(items))
	notify := func() {
		if onSchedulingSettled != nil {
			func() {
				defer func() { _ = recover() }() // lifecycle cleanup must not replace the worker result
				onSchedulingSettled()
			}()
		}
	}
	if workers == 0 {
		notify()
		return results, nil
	}
	var (
		mu    sync.Mutex
		next  int
		live  = workers
		first = make(chan error, workers)
		done  = make(chan struct{})
	)
	worker := func() {
		var failure error
		defer func() {
			mu.Lock()
			live--
			last := live == 0
			mu.Unlock()
			if failure != nil {
				first <- failure
			}
			if last {
				notify()
				close(done)
			}
		}()
		for {
			mu.Lock()
			if next >= len(items) {
				mu.Unlock()
				return
			}
			i := next
			next++
			mu.Unlock()
			if global != nil {
				global.Acquire()
			}
			r, err := fn(items[i], i)
			if global != nil {
				global.Release()
			}
			if err != nil {
				failure = err
				return
			}
			results[i] = r
		}
	}
	for w := 0; w < workers; w++ {
		go worker()
	}
	select {
	case err := <-first:
		return results, err
	case <-done:
		select {
		case err := <-first:
			return results, err
		default:
		}
		return results, nil
	}
}

// ParallelTaskResult is the part of a parallel task's result the aggregate reads. ExitCode nil is the original's null.
type ParallelTaskResult struct {
	Agent              string
	TaskIndex          *int
	Output             string
	ExitCode           *int
	Error              string
	TimedOut           bool
	Model              string
	OutputTargetPath   string
	OutputTargetExists *bool
}

// aggregateParallelOutputs is aggregateParallelOutputs; headerFormat nil is "=== Parallel Task N (agent) ===".
func aggregateParallelOutputs(results []ParallelTaskResult, headerFormat func(index int, agent string) string) string {
	if headerFormat == nil {
		headerFormat = func(i int, agent string) string { return "=== Parallel Task " + itoa(i+1) + " (" + agent + ") ===" }
	}
	blocks := make([]string, len(results))
	for i, r := range results {
		index := i
		if r.TaskIndex != nil {
			index = *r.TaskIndex
		}
		header := headerFormat(index, r.Agent)
		hasOutput := jsTrim(r.Output) != ""
		var status string
		switch {
		case r.TimedOut:
			status = "TIMED OUT"
			if r.Error != "" {
				status += ": " + r.Error
			}
		case r.ExitCode != nil && *r.ExitCode == -1:
			status = "SKIPPED"
		case r.ExitCode != nil && *r.ExitCode != 0:
			status = "FAILED (exit code " + itoa(*r.ExitCode) + ")"
			if r.Error != "" {
				status += ": " + r.Error
			}
		case r.Error != "":
			status = "WARNING: " + r.Error
		case !hasOutput && r.OutputTargetPath != "" && r.OutputTargetExists != nil && !*r.OutputTargetExists:
			status = "EMPTY OUTPUT (expected output file missing: " + r.OutputTargetPath + ")"
		case !hasOutput && r.OutputTargetPath == "":
			status = "EMPTY OUTPUT (no textual response returned)"
		}
		body := r.Output
		if status != "" {
			body = status
			if hasOutput {
				body = status + "\n" + r.Output
			}
		}
		blocks[i] = header + "\n" + body
	}
	return strings.Join(blocks, "\n\n")
}
