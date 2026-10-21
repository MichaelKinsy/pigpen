package tintinweb_subagents

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

// TestPerfReport times the start-up of the extension and each tool on a representative input, using only helpers
// that this revision and the previous release both have (startRig, the fake child), so the same file measures both:
// TW_PERF=1 go test -run TestPerfReport -v. It prints one `PERF name median_us allocs/op` line per measurement.
func TestPerfReport(t *testing.T) {
	if os.Getenv("TW_PERF") == "" {
		t.Skip("set TW_PERF=1")
	}
	measure := func(name string, n int, f func()) {
		for i := 0; i < 3; i++ { // warm up
			f()
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		before := ms.Mallocs
		var d []time.Duration
		for i := 0; i < n; i++ {
			s := time.Now()
			f()
			d = append(d, time.Since(s))
		}
		runtime.ReadMemStats(&ms)
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		fmt.Printf("PERF %s %.1f %d\n", name, float64(d[len(d)/2].Nanoseconds())/1000, (ms.Mallocs-before)/uint64(n))
	}
	measure("registration", 20, func() { r := startRigIn(t, t.TempDir()); r.app.mgr.abortAll() })

	r := startRig(t)
	var ids []string
	measure("agent-background-start-and-finish", 40, func() {
		n := r.fleet.count()
		ids = append(ids, idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true})))
		r.fleet.wait(t, n+1)[n].finishWith(&childResult{Text: "done", ToolUses: 3})
		<-r.app.mgr.get(ids[len(ids)-1]).done
	})
	measure("get_subagent_result-finished", 100, func() { r.must("get_subagent_result", obj{"agent_id": ids[0]}) })
	measure("agent-not-found", 100, func() { r.must("get_subagent_result", obj{"agent_id": "nope"}) })
	n := r.fleet.count()
	run := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	r.fleet.wait(t, n+1)
	measure("steer_subagent", 100, func() { r.must("steer_subagent", obj{"agent_id": run, "message": "left"}) })
}
