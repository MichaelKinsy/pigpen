package rpiv_todo_test

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

// TestMeasure times the tool through the fake host (the tool call, the host's end event and the overlay refresh
// it triggers). It is the latency and allocation measurement of the PR bodies: skipped unless TODO_MEASURE is
// set, run alternately on two trees, the median of TODO_MEASURE rounds of 200 calls each. Allocations count the
// whole process, the fake host's goroutines included, the same on both trees.
func TestMeasure(t *testing.T) {
	rounds := 0
	if _, err := fmt.Sscan(os.Getenv("TODO_MEASURE"), &rounds); err != nil || rounds < 1 {
		t.Skip("set TODO_MEASURE=<rounds> to measure")
	}
	cases := []struct {
		name string
		mode string
		call func(i int) map[string]any
	}{
		{"create, overlay shown", "tui", func(i int) map[string]any {
			return map[string]any{"action": "create", "subject": fmt.Sprintf("task %d", i)}
		}},
		{"update, overlay shown", "tui", func(i int) map[string]any {
			return map[string]any{"action": "update", "id": 1.0, "status": []string{"in_progress", "pending"}[i%2]}
		}},
		{"list, overlay shown", "tui", func(int) map[string]any { return map[string]any{"action": "list"} }},
		{"create, no UI", "print", func(i int) map[string]any {
			return map[string]any{"action": "create", "subject": fmt.Sprintf("task %d", i)}
		}},
	}
	for _, c := range cases {
		r := startRig(t, HostOptions{Mode: c.mode})
		r.setBranch(snapshot(2, jtask(1, "seed")))
		r.Fire("session_start", nil)
		i := 0
		step := func() { i++; r.call(c.call(i)) }
		for range 50 {
			step()
		}
		var samples []float64
		for range rounds {
			start := time.Now()
			for range 200 {
				step()
			}
			samples = append(samples, float64(time.Since(start).Nanoseconds())/200)
		}
		slices.Sort(samples)
		allocs := testing.AllocsPerRun(200, step)
		fmt.Printf("measure %-24s ns/op=%.0f allocs/op=%.0f\n", c.name, samples[len(samples)/2], allocs)
	}
}
