package changeset_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
)

// TestPerfCompute is the Go side of proof/perf/run.sh: it prints one line, "PERF {json}", with the
// median milliseconds of 5 computations per changeset kind, and the allocations of one of them.
// It does nothing unless AHP_PERF_REPO names a repository (proof/perf/mkrepo.sh makes one).
func TestPerfCompute(t *testing.T) {
	repo := os.Getenv("AHP_PERF_REPO")
	if repo == "" {
		t.Skip("set AHP_PERF_REPO to measure")
	}
	ctx := context.Background()
	backend := changeset.GitChanges{}
	ws, err := backend.Inspect(ctx, repo)
	if err != nil || ws == nil {
		t.Fatalf("inspect = %v, %v", ws, err)
	}
	out := map[string]any{}
	for _, kind := range changeset.Kinds {
		var times []float64
		files := 0
		for i := 0; i < 5; i++ {
			start := time.Now()
			got, err := backend.Compute(ctx, *ws, "sid", kind)
			if err != nil {
				t.Fatal(err)
			}
			files = len(got)
			times = append(times, float64(time.Since(start).Microseconds())/1000)
		}
		sort.Float64s(times)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, _ = backend.Compute(ctx, *ws, "sid", kind)
		runtime.ReadMemStats(&after)
		out[string(kind)] = map[string]any{
			"ms": float64(int(times[2]*100)) / 100, "files": files,
			"allocs": after.Mallocs - before.Mallocs, "allocKB": (after.TotalAlloc - before.TotalAlloc) / 1024,
		}
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	out["rssMB"] = mem.Sys / (1 << 20)
	raw, _ := json.Marshal(out)
	fmt.Printf("PERF %s\n", raw)
}
