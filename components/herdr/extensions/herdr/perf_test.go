package herdr_test

// Latency of the handlers and of a report reaching herdr, for the performance section of a change. It runs only with
// HERDR_PERF=N (N interleavable samples; each prints one JSON line of medians in microseconds):
//
//	HERDR_PERF=30 go test -run TestPerf -count=1 .
//
// "handler" is the time the fake host waits for the extension's answer to an event (it includes the host round trips the
// extension makes for the session file, id and idle state over an in-memory pipe). "reaches herdr" is the time from the
// event until the fake herdr socket received the request.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	herdr "github.com/MichaelKinsy/pigpen/components/herdr/extensions/herdr"
)

func median(d []time.Duration) float64 {
	slices.Sort(d)
	return float64(d[len(d)/2].Nanoseconds()) / 1000
}

func TestPerf(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("HERDR_PERF"))
	if n == 0 {
		t.Skip("HERDR_PERF=N to measure")
	}
	register := make([]time.Duration, 0, n)
	handler := map[string][]time.Duration{}
	reach := map[string][]time.Duration{}
	for range n {
		h := installFakeHerdr(t)
		begin := time.Now()
		host := startHost(t, herdr.Extension(), "tui")
		register = append(register, time.Since(begin))

		step := func(name string, run func()) {
			before := len(h.requests())
			begin := time.Now()
			run()
			handler[name] = append(handler[name], time.Since(begin))
			if name == "shutdown-resume" || name == "bus-ignored" {
				return
			}
			for len(h.requests()) == before {
				time.Sleep(20 * time.Microsecond)
			}
			reach[name] = append(reach[name], time.Since(begin))
		}
		step("session_start", func() { host.fire("session_start", nil) })
		step("agent_start", func() { host.fire("agent_start", nil) })
		step("herdr:blocked", func() { host.emit("herdr:blocked", map[string]any{"active": true, "label": "approve command"}) })
		step("herdr:unblocked", func() { host.emit("herdr:blocked", map[string]any{"active": false}) })
		step("agent_settled", func() { host.fire("agent_settled", nil) })
		step("shutdown-resume", func() { host.fire("session_shutdown", map[string]any{"reason": "resume"}) })
		step("quit", func() { host.fire("session_shutdown", map[string]any{"reason": "quit"}) })
	}
	out := map[string]any{"impl": "go", "register_us": median(register)}
	for name, d := range handler {
		out["handler_"+name+"_us"] = median(d)
	}
	for name, d := range reach {
		out["reaches_herdr_"+name+"_us"] = median(d)
	}
	line, _ := json.Marshal(out)
	fmt.Println("PERF", string(line))
}
