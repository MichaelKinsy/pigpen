package pi_goal_x

// How long a terminal key waits while a completion audit is in progress (port/PORT.md, "Performance"). The terminal-input listener
// is subscribed for the whole TUI session and PiG waits for its verdict on every key; the listener takes the core's lock
// (sdkHost.OnTerminalInput calls core.locked). The auditor's run releases that lock (core.await), so the key does not queue behind the
// audit itself; what it can still meet is the other work that holds the lock meanwhile: the auditor's progress callbacks, the 80 ms
// animation tick, the keys' own UI updates, and, at the end, the commit of the verdict (files, ledger, UI). Each of those ends with
// the UI update, whose SetStatus is a blocking host round trip (statusDelay stands for it).
//
//	go test -run '^$' -bench KeyWaitDuringAudit -benchtime 1x -v
//
// The benchmark runs the real update_goal(complete) path with a scripted auditor that reports progress every `progress` and ends
// after the audit's duration; a key (a plain key, then the toggle chord, alternating) is sent every 300 us from the moment the
// audit has started until update_goal has returned, and each key's wait is the time of the listener's call. It reports the
// median, p99 and maximum wait and the number of keys.

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkKeyWaitDuringAudit(b *testing.B) {
	for _, tc := range []struct {
		name     string
		progress time.Duration
		status   time.Duration
	}{
		{"progress1ms_status0", time.Millisecond, 0},
		{"progress1ms_status200us", time.Millisecond, 200 * time.Microsecond},
		{"progress1ms_status1ms", time.Millisecond, time.Millisecond},
		{"progress50ms_status200us", 50 * time.Millisecond, 200 * time.Microsecond},
	} {
		b.Run(tc.name, func(b *testing.B) {
			const auditDuration = 600 * time.Millisecond
			var waits []time.Duration
			for range b.N {
				f := newPerfFixture(b, perfMaxRunsOff)
				f.h.statusDelay = tc.status
				f.co.afterBase = time.AfterFunc // the audit animation's real timer
				f.tool("create_goal", map[string]any{"objective": "Ship the release\nSuccess criteria: the checks are green."})
				var started atomic.Bool
				f.co.deps.runCompletionAuditor = func(a runGoalCompletionAuditorArgs) goalAuditorResult {
					started.Store(true)
					for end := time.Now().Add(auditDuration); time.Now().Before(end); time.Sleep(tc.progress) {
						a.onProgress(auditorProgress{phase: "tool_executing", currentTool: "read", currentToolArgs: "goal_files.go", recentOutput: []string{"checking the ledger"}})
					}
					return goalAuditorResult{approved: true, output: "All good\n<approved/>", model: "m"}
				}
				done := make(chan error, 1)
				go func() {
					var err error
					f.co.locked(func() {
						_, err = executeUpdateGoal(f.co, map[string]any{"status": "complete", "completion_summary": "Done, with the checks green."})
					})
					done <- err
				}()
				for !started.Load() {
					time.Sleep(100 * time.Microsecond)
				}
				keys := []string{"a", "\x1b[116;6u"}
				for n := 0; ; n++ {
					select {
					case err := <-done:
						if err != nil {
							b.Fatal(err)
						}
						goto finished
					default:
					}
					t0 := time.Now()
					f.co.locked(func() { f.h.keyCB(keys[n%2]) }) // sdkHost.OnTerminalInput's wrapper
					waits = append(waits, time.Since(t0))
					time.Sleep(300 * time.Microsecond)
				}
			finished:
			}
			slices.Sort(waits)
			if len(waits) < 50 {
				b.Fatalf("only %d keys were sent during the audit", len(waits))
			}
			us := func(d time.Duration) float64 { return float64(d) / float64(time.Microsecond) }
			b.ReportMetric(us(waits[len(waits)/2]), "median-us")
			b.ReportMetric(us(waits[len(waits)*99/100]), "p99-us")
			b.ReportMetric(us(waits[len(waits)-1]), "max-us")
			b.ReportMetric(float64(len(waits)), "keys")
			if max := waits[len(waits)-1]; max > auditDuration/2 {
				b.Errorf("a key waited %v: it queued behind the audit", max)
			}
		})
	}
}
