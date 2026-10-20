package pi_subagents

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of test/unit/parallel-utils.test.ts.

func pStep(agent, task string) *jsObject { return jo("agent", agent, "task", task) }

func pAgents(steps []*jsObject) []string {
	out := []string{}
	for _, s := range steps {
		out = append(out, s.vals["agent"].(string))
	}
	return out
}

// peak runs fn over items and returns the highest number of calls in flight at once.
func peak(t *testing.T, items []int, limit float64, sem *Semaphore, pause time.Duration) int {
	t.Helper()
	var running, max atomic.Int32
	_, err := mapConcurrent(items, limit, func(item, i int) (int, error) {
		n := running.Add(1)
		for {
			m := max.Load()
			if n <= m || max.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(pause)
		running.Add(-1)
		return item, nil
	}, sem, nil)
	if err != nil {
		t.Fatal(err)
	}
	return int(max.Load())
}

func TestParallelUtilsTwins(t *testing.T) {
	const f = "parallel-utils"
	tw(t, f, "returns true for a parallel step group", func(t *testing.T) {
		eq(t, isParallelGroup(jo("parallel", []any{pStep("a", "do stuff"), pStep("b", "do other stuff")})), true, "group")
	})
	tw(t, f, "returns false for a sequential step", func(t *testing.T) {
		eq(t, isParallelGroup(pStep("a", "do stuff")), false, "sequential")
	})
	tw(t, f, "returns false when parallel is not an array", func(t *testing.T) {
		eq(t, isParallelGroup(jo("parallel", "not-an-array", "agent", "a", "task", "x")), false, "string")
	})
	tw(t, f, "returns sequential steps unchanged", func(t *testing.T) {
		flat := flattenSteps([]*jsObject{pStep("a", "t1"), pStep("b", "t2")})
		eq(t, len(flat), 2, "length")
		eq(t, pAgents(flat), []string{"a", "b"}, "agents")
	})
	tw(t, f, "expands parallel groups into individual steps", func(t *testing.T) {
		flat := flattenSteps([]*jsObject{pStep("scout", "find info"), jo("parallel", []any{pStep("reviewer-a", "review part 1"), pStep("reviewer-b", "review part 2")}), pStep("summarizer", "combine")})
		eq(t, len(flat), 4, "length")
		eq(t, pAgents(flat), []string{"scout", "reviewer-a", "reviewer-b", "summarizer"}, "agents")
	})
	tw(t, f, "handles empty steps array", func(t *testing.T) {
		eq(t, flattenSteps(nil), []*jsObject{}, "empty")
	})
	tw(t, f, "handles empty parallel group", func(t *testing.T) {
		flat := flattenSteps([]*jsObject{pStep("before", "x"), jo("parallel", []any{}), pStep("after", "y")})
		eq(t, len(flat), 2, "length")
		eq(t, pAgents(flat), []string{"before", "after"}, "agents")
	})
	tw(t, f, "processes all items and preserves order", func(t *testing.T) {
		got, err := mapConcurrent([]int{10, 20, 30, 40}, 2, func(item, i int) (int, error) { return item * 2, nil }, nil, nil)
		eq(t, err, nil, "error")
		eq(t, got, []int{20, 40, 60, 80}, "results")
	})
	tw(t, f, "respects concurrency limit", func(t *testing.T) {
		if m := peak(t, []int{1, 2, 3, 4, 5, 6}, 2, nil, 10*time.Millisecond); m > 2 {
			t.Errorf("max concurrent was %d, expected <= 2", m)
		}
	})
	tw(t, f, "handles empty input", func(t *testing.T) {
		got, err := mapConcurrent([]int{}, 4, func(item, i int) (int, error) { return item, nil }, nil, nil)
		eq(t, err, nil, "error")
		eq(t, len(got), 0, "results")
	})
	tw(t, f, "clamps limit=0 to 1 (sequential execution)", func(t *testing.T) {
		eq(t, peak(t, []int{1, 2, 3}, 0, nil, 10*time.Millisecond), 1, "should run sequentially with limit=0")
	})
	tw(t, f, "clamps limit=-1 to 1 (sequential execution)", func(t *testing.T) {
		eq(t, peak(t, []int{1, 2, 3}, -1, nil, 10*time.Millisecond), 1, "should run sequentially with limit=-1")
	})
	tw(t, f, "does not stagger by default", func(t *testing.T) {
		// Every worker must be running before any may finish: a stagger would leave the barrier short.
		var started sync.WaitGroup
		started.Add(3)
		all := make(chan struct{})
		go func() { started.Wait(); close(all) }()
		_, err := mapConcurrent([]int{1, 2, 3}, 3, func(item, i int) (int, error) {
			started.Done()
			select {
			case <-all:
				return item, nil
			case <-time.After(10 * time.Second):
				return 0, errors.New("worker " + itoa(i) + " did not start alongside the others")
			}
		}, nil, nil)
		eq(t, err, nil, "all workers started together")
	})
	tw(t, f, "notifies scheduling settlement only after workers outliving an early rejection stop", func(t *testing.T) {
		gate := make(chan struct{})
		var mu sync.Mutex
		var started []int
		var settled atomic.Bool
		_, err := mapConcurrent([]int{0, 1, 2}, 2, func(item, i int) (int, error) {
			mu.Lock()
			started = append(started, item)
			mu.Unlock()
			if item == 0 {
				return 0, errors.New("early rejection")
			}
			<-gate
			return item, nil
		}, nil, func() { settled.Store(true) })
		hasMatch(t, err, "early rejection")
		eq(t, settled.Load(), false, "settled before the workers stopped")
		close(gate)
		for attempt := 0; attempt < 400 && !settled.Load(); attempt++ {
			time.Sleep(5 * time.Millisecond)
		}
		eq(t, settled.Load(), true, "settled")
		mu.Lock()
		defer mu.Unlock()
		eq(t, started, []int{0, 1, 2}, "started")
	})
	tw(t, f, "respects a shared global semaphore across simultaneous calls", func(t *testing.T) {
		sem := NewSemaphore(2)
		var running, max atomic.Int32
		run := func(items []int) []int {
			got, err := mapConcurrent(items, float64(len(items)), func(item, i int) (int, error) {
				n := running.Add(1)
				for {
					m := max.Load()
					if n <= m || max.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				running.Add(-1)
				return item, nil
			}, sem, nil)
			if err != nil {
				t.Error(err)
			}
			return got
		}
		var wg sync.WaitGroup
		var a, b []int
		wg.Add(2)
		go func() { defer wg.Done(); a = run([]int{1, 2, 3}) }()
		go func() { defer wg.Done(); b = run([]int{4, 5, 6}) }()
		wg.Wait()
		eq(t, a, []int{1, 2, 3}, "first")
		eq(t, b, []int{4, 5, 6}, "second")
		if m := max.Load(); m > 2 {
			t.Errorf("max concurrent was %d, expected <= 2", m)
		}
	})
	tw(t, f, "clamps invalid global semaphore limits to 1", func(t *testing.T) {
		eq(t, peak(t, []int{1, 2, 3}, 3, NewSemaphore(0), 10*time.Millisecond), 1, "max running")
	})
	tw(t, f, "is 20", func(t *testing.T) { eq(t, DefaultGlobalConcurrencyLimit, 20, "limit") })
	tw(t, f, "aggregates successful outputs with headers", func(t *testing.T) {
		zero := 0
		result := aggregateParallelOutputs([]ParallelTaskResult{{Agent: "reviewer-a", Output: "Looks good", ExitCode: &zero}, {Agent: "reviewer-b", Output: "Needs fixes", ExitCode: &zero}}, nil)
		for _, want := range []string{"=== Parallel Task 1 (reviewer-a) ===", "Looks good", "=== Parallel Task 2 (reviewer-b) ===", "Needs fixes"} {
			if !strings.Contains(result, want) {
				t.Errorf("missing %q in %q", want, result)
			}
		}
	})
	tw(t, f, "marks failed tasks", func(t *testing.T) {
		one := 1
		if r := aggregateParallelOutputs([]ParallelTaskResult{{Agent: "agent-a", Output: "partial output", ExitCode: &one}}, nil); !strings.Contains(r, "FAILED (exit code 1)") {
			t.Error(r)
		}
	})
	tw(t, f, "marks empty output", func(t *testing.T) {
		zero := 0
		if r := aggregateParallelOutputs([]ParallelTaskResult{{Agent: "agent-a", Output: "", ExitCode: &zero}}, nil); !strings.Contains(r, "EMPTY OUTPUT") {
			t.Error(r)
		}
	})
	tw(t, f, "treats whitespace-only output as empty", func(t *testing.T) {
		zero := 0
		if r := aggregateParallelOutputs([]ParallelTaskResult{{Agent: "agent-a", Output: "   \n  ", ExitCode: &zero}}, nil); !strings.Contains(r, "EMPTY OUTPUT") {
			t.Error(r)
		}
	})
	tw(t, f, "marks skipped tasks (exitCode=-1) distinctly from failed", func(t *testing.T) {
		zero, skipped := 0, -1
		r := aggregateParallelOutputs([]ParallelTaskResult{{Agent: "agent-a", Output: "done", ExitCode: &zero}, {Agent: "agent-b", Output: "(skipped — fail-fast)", ExitCode: &skipped}}, nil)
		if !strings.Contains(r, "SKIPPED") || strings.Contains(r, "FAILED") {
			t.Error(r)
		}
	})
	tw(t, f, "is 4", func(t *testing.T) { eq(t, MaxParallelConcurrency, 4, "limit") })
}
