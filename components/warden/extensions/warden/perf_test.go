package warden

import (
	"fmt"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

// Measurements for the PR's Performance section, off unless WARDEN_PERF=1 (they are timing, not assertions):
//
//	WARDEN_PERF=1 go test -count=1 -run 'TestPerf' -v .
//
// The test uses only the fake-host harness, so the same file measures an older tree for comparison.
func perfOn(t *testing.T) {
	if os.Getenv("WARDEN_PERF") != "1" {
		t.Skip("set WARDEN_PERF=1")
	}
}

func measure(n int, f func()) (nsPerOp, allocsPerOp, bytesPerOp float64) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	for range n {
		f()
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	return float64(elapsed.Nanoseconds()) / float64(n), float64(after.Mallocs-before.Mallocs) / float64(n), float64(after.TotalAlloc-before.TotalAlloc) / float64(n)
}

func median(values []float64) float64 {
	sort.Float64s(values)
	return values[len(values)/2]
}

// TestPerfToolResultRoundTrip: one tool_result event through the host protocol, the path every tool call takes.
func TestPerfToolResultRoundTrip(t *testing.T) {
	perfOn(t)
	h := startHarness(t, harnessOpts{cfg: on(BackendNone)})
	h.start()
	// Distinct calls, as a session makes them: the same four commands forever would be a stuck loop, and a stuck
	// verdict (trace details, a steer, a model lookup) is a different path with its own cost.
	commands := []string{"go test ./pkg%d", "git status --short %d", "sleep 1; echo ok %d", "ls -la dir%d"}
	i := 0
	var ns, allocs, bytes []float64
	for range 12 {
		a, b, c := measure(300, func() {
			h.toolResult("bash", map[string]any{"command": fmt.Sprintf(commands[i%len(commands)], i)}, "ok  \tpkg\t0.2s\n", false)
			i++
		})
		ns, allocs, bytes = append(ns, a), append(allocs, b), append(bytes, c)
	}
	fmt.Printf("PERF tool_result_round_trip ns/op=%.0f allocs/op=%.0f bytes/op=%.0f\n", median(ns), median(allocs), median(bytes))
}

// TestPerfRegistration: building the extension and completing the host handshake (cold start to registration).
func TestPerfRegistration(t *testing.T) {
	perfOn(t)
	var ns []float64
	for range 12 {
		start := time.Now()
		h := startHarness(t, harnessOpts{})
		_ = h
		ns = append(ns, float64(time.Since(start).Nanoseconds()))
	}
	fmt.Printf("PERF registration_ns=%.0f\n", median(ns))
}

// TestPerfOnToolResult: the handler alone, without the protocol round trip, on distinct calls (needs the steering
// core's test rig, so it measures this tree and the trees stacked on the steering core).
func TestPerfOnToolResult(t *testing.T) {
	perfOn(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	r := newSteerRig(t, cfg, false, nil)
	commands := []string{"go test ./pkg%d", "git status --short %d", "sleep 1; echo ok %d", "ls -la dir%d", "grep -n foo src/f%d.go", "cat src/f%d.go"}
	r.run(func(ctx sdk.Context) {
		i := 0
		var ns, allocs, bytes []float64
		for range 12 {
			a, b, c := measure(2000, func() {
				data := map[string]any{"toolName": "bash", "toolCallId": "r", "input": map[string]any{"command": fmt.Sprintf(commands[i%len(commands)], i)}, "content": []any{map[string]any{"type": "text", "text": "ok  \tpkg\t0.2s\n"}}, "isError": false}
				_, _ = r.w.onToolResult(ctx, data)
				i++
			})
			ns, allocs, bytes = append(ns, a), append(allocs, b), append(bytes, c)
		}
		fmt.Printf("PERF on_tool_result ns/op=%.0f allocs/op=%.0f bytes/op=%.0f\n", median(ns), median(allocs), median(bytes))
	})
}
