package pig_music_test

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

// TestPerfCommands prints the latency and allocations of the commands the engine choice sits on, through the fake host and a
// fake mpv: `/music now` (the command line's code in process) and `/music` opening and hiding the player. It runs only when
// PIG_MUSIC_PERF is set, so that a change can be compared with the release before it (the numbers include the fake host, the
// same on both sides):
//
//	PIG_MUSIC_PERF=1 go test -run TestPerfCommands -count=1 -v
func TestPerfCommands(t *testing.T) {
	if os.Getenv("PIG_MUSIC_PERF") == "" {
		t.Skip("set PIG_MUSIC_PERF=1")
	}
	h, _, _, _ := playerEnv(t)
	perfCommands(t, h, "mpv")
}

// TestPerfCommandsNative is the same over the native engine's daemon (run in this process, with the synthetic opener and the
// paced null output). The release before this one could not run the player on that engine, so there is nothing to compare it to.
func TestPerfCommandsNative(t *testing.T) {
	if os.Getenv("PIG_MUSIC_PERF") == "" {
		t.Skip("set PIG_MUSIC_PERF=1")
	}
	perfCommands(t, nativeEnv(t, nil).h, "native")
}

func perfCommands(t *testing.T, h *overlayHost, engine string) {
	say(t, h, "play", "night")
	measure := func(name string, n int, op func()) {
		for i := 0; i < 5; i++ {
			op() // warm
		}
		times := make([]time.Duration, n)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for i := range times {
			start := time.Now()
			op()
			times[i] = time.Since(start)
		}
		runtime.ReadMemStats(&after)
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		fmt.Printf("PERF %s %s median_us=%d allocs_per_op=%d bytes_per_op=%d\n", engine, name, times[n/2].Microseconds(), (after.Mallocs-before.Mallocs)/uint64(n), (after.TotalAlloc-before.TotalAlloc)/uint64(n))
	}
	measure("quick-now", 60, func() { say(t, h, "now") })
	measure("open-hide", 30, func() {
		done := h.command("music")
		h.mu.Lock()
		next := len(h.opens) + 1
		h.mu.Unlock()
		h.waitOpenN(next)
		h.input("q")
		<-done
	})
}
